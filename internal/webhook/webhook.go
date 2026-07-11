// Package webhook despacha los POST de las alertas (ADR-006) con la defensa
// anti-SSRF del SAD §8 en dos capas: ValidateURL al registrar (feedback
// inmediato con 400) y el mismo chequeo de nuevo al discar en cada despacho,
// pineando la IP ya validada — así un DNS que cambia entre el registro y el
// disparo (rebinding) no alcanza redes internas.
//
// Una API pública que hace POST a URLs ajenas es un proxy en potencia hacia
// localhost, la red del contenedor o la metadata del cloud; este paquete es
// la única puerta por la que Faro origina tráfico hacia terceros arbitrarios.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// dispatchTimeout acota el POST completo (conexión + respuesta): un receptor
// colgado no puede retener el ciclo de refresco. Sin reintentos en v1 — el
// próximo cruce real vuelve a disparar.
const dispatchTimeout = 10 * time.Second

// resolveTimeout acota el DNS de ValidateURL (corre dentro de una request de
// la API: debe fallar rápido, no colgar el registro).
const resolveTimeout = 5 * time.Second

// maxResponseBytes es lo máximo que se lee de la respuesta del receptor: solo
// interesa el status; el cuerpo se drena acotado para reusar la conexión.
const maxResponseBytes = 4 << 10

// Client valida URLs de webhook y despacha los POST. Crear con New.
type Client struct {
	// allowPrivate desactiva el bloqueo de redes privadas/loopback — SOLO
	// para desarrollo y tests locales (FARO_WEBHOOK_ALLOW_PRIVATE=1, ver
	// docs/SECURITY.md). En producción siempre false.
	allowPrivate bool
	httpc        *http.Client
}

// New crea el cliente de webhooks. El transporte no hereda proxy del entorno
// (un HTTP_PROXY inyectado sería otro camino SSRF) y no sigue redirects (un
// 302 hacia adentro tampoco): cada conexión pasa por el dial pineado.
func New(allowPrivate bool) *Client {
	c := &Client{allowPrivate: allowPrivate}
	c.httpc = &http.Client{
		Timeout: dispatchTimeout,
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: c.dialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return c
}

// ValidateURL decide si una webhook_url es registrable: esquema http/https,
// sin credenciales embebidas, y un host que no resuelva a loopback, redes
// privadas, link-local ni direcciones especiales (IPv4 e IPv6). Es la capa 1
// del anti-SSRF; el error es apto para mostrarse al cliente de la API.
func (c *Client) ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("webhook_url inválida")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("webhook_url debe ser http o https")
	}
	if u.User != nil {
		return fmt.Errorf("webhook_url no puede llevar credenciales embebidas")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("webhook_url sin host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	if _, err := c.resolveAllowed(ctx, host); err != nil {
		return err
	}
	return nil
}

// Post despacha el payload como JSON a la URL. Éxito = 2xx; cualquier otra
// cosa (incluidos redirects, que no se siguen) es error. La URL se vuelve a
// resolver y validar aquí (capa 2): registrar una URL sana y luego apuntar su
// DNS hacia adentro no sirve de nada.
func (c *Client) Post(ctx context.Context, rawURL string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("webhook: serializando payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: armando request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", "faro-webhook/1.0 (+https://faro.vibenest.net)")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: POST falló: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook: el receptor respondió %d", resp.StatusCode)
	}
	return nil
}

// dialContext resuelve el host, valida TODAS sus IPs y disca directo a una IP
// aprobada — no al hostname: entre el LookupNetIP y el dial no hay segunda
// resolución que un atacante pueda mover (rebinding).
func (c *Client) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("webhook: dirección %q: %w", addr, err)
	}
	ips, err := c.resolveAllowed(ctx, host)
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

// resolveAllowed resuelve un host (o interpreta la IP literal) y exige que
// TODAS sus direcciones sean públicas: si un nombre mezcla registros públicos
// y privados, se rechaza entero — elegir "la buena" dejaría al resolver del
// atacante decidir cuál se usa.
func (c *Client) resolveAllowed(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		if err := c.checkAddr(ip); err != nil {
			return nil, err
		}
		return []netip.Addr{ip}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("webhook_url: el host %q no resuelve", host)
	}
	for _, ip := range ips {
		if err := c.checkAddr(ip); err != nil {
			return nil, err
		}
	}
	return ips, nil
}

var v4Broadcast = netip.MustParseAddr("255.255.255.255")

// checkAddr rechaza los destinos que un webhook jamás debería alcanzar. La
// lista es la del SAD §8: loopback, privadas (RFC 1918 / ULA), link-local
// (incluye 169.254.169.254, la metadata de los clouds), multicast, no
// especificadas y broadcast.
func (c *Client) checkAddr(ip netip.Addr) error {
	if c.allowPrivate {
		return nil
	}
	ip = ip.Unmap() // 4-in-6 (::ffff:127.0.0.1) se evalúa como su IPv4
	bad := !ip.IsValid() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip == v4Broadcast
	if bad {
		return errors.New("webhook_url apunta a una red privada o reservada")
	}
	return nil
}

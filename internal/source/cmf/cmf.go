// Package cmf implementa indicator.IndicatorSource contra la API oficial de
// la CMF (Comisión para el Mercado Financiero) — la fuente v1 de Faro
// (ADR-002). Una llamada por indicador, API key por query param (llega vía
// ENV, ADR-009). Los valores vienen como strings en formato chileno
// ("40.842,07") y aquí se normalizan (CASE-003).
package cmf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/faborubio/faro/internal/indicator"
)

// DefaultBaseURL es la raíz de recursos de la API CMF (v3).
const DefaultBaseURL = "https://api.cmfchile.cl/api-sbifv3/recursos_api"

// defaultCodes es el catálogo v1 (SAD §1.2); debe existir en `indicators`.
var defaultCodes = []string{"uf", "dolar", "utm", "ipc"}

// Client consume la API de la CMF. El zero value no sirve: usar New.
type Client struct {
	BaseURL    string        // raíz de la API; los tests apuntan a httptest
	APIKey     string        // nunca se loguea ni viaja en mensajes de error
	Codes      []string      // indicadores a traer; default: uf/dolar/utm/ipc
	HTTPClient *http.Client  // default: timeout 20 s
	Backoff    time.Duration // base del backoff exponencial; default 500 ms
}

// New crea un cliente con defaults de producción. Los timeouts van POR FASE
// además del global: con un solo Client.Timeout, Go reporta cualquier cuelgue
// como "Client.Timeout exceeded while awaiting headers" sin decir si murió el
// DNS, el connect, el TLS o la espera de respuesta — en producción esa
// ambigüedad costó un diagnóstico entero (T-004). Con fases, el error nombra
// la fase: "dial tcp …: i/o timeout", "TLS handshake timeout", "timeout
// awaiting response headers".
func New(apiKey string) *Client {
	return &Client{
		BaseURL: DefaultBaseURL,
		APIKey:  apiKey,
		Codes:   defaultCodes,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second, // techo global (cuerpo incluido)
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: 6 * time.Second}).DialContext,
				TLSHandshakeTimeout:   6 * time.Second,
				ResponseHeaderTimeout: 10 * time.Second,
			},
		},
		Backoff: 500 * time.Millisecond,
	}
}

// Name identifica la fuente en sync_runs.
func (c *Client) Name() string { return "cmf" }

// El adapter también sabe traer histórico por año (backfill, CASE-006).
var _ indicator.HistoricalSource = (*Client)(nil)

// Fetch trae los indicadores configurados, una llamada por código (ADR-002).
// Ante fallas parciales devuelve los snapshots que sí se obtuvieron junto con
// el error agregado: el scheduler decide persistir lo parcial y registrar el
// fallo en sync_runs (resiliencia, SAD §8).
func (c *Client) Fetch(ctx context.Context) ([]indicator.Snapshot, error) {
	var snaps []indicator.Snapshot
	var errs []error
	for _, code := range c.codes() {
		s, err := c.fetchOne(ctx, code, url.PathEscape(code))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", code, err))
			continue
		}
		snaps = append(snaps, s...)
	}
	return snaps, errors.Join(errs...)
}

func (c *Client) codes() []string {
	if len(c.Codes) > 0 {
		return c.Codes
	}
	return defaultCodes
}

// FetchYear trae la serie completa de un indicador para un año (recurso
// "{code}/{año}", mismo retry/backoff que Fetch). Las series anuales reales
// son pequeñas — ≤ 25 KB el año más pesado (CASE-006) — así que el límite de
// 1 MB del adapter queda como está (AUD-003).
func (c *Client) FetchYear(ctx context.Context, code string, year int) ([]indicator.Snapshot, error) {
	return c.fetchOne(ctx, code, url.PathEscape(code)+"/"+strconv.Itoa(year))
}

// fetchOne pide un recurso de un indicador con hasta 3 intentos y backoff
// exponencial ante errores transitorios (red, HTTP 5xx) — SAD §8. Los errores
// definitivos (4xx, parseo) no se reintentan. code etiqueta los snapshots;
// resource es el path ya escapado bajo la raíz de la API.
func (c *Client) fetchOne(ctx context.Context, code, resource string) ([]indicator.Snapshot, error) {
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff << (attempt - 1)):
			}
		}
		snaps, err, retryable := c.doOnce(ctx, code, resource)
		if err == nil {
			return snaps, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return nil, lastErr
}

// entry es la forma cruda que entrega la CMF dentro del envoltorio del
// indicador ("UFs", "Dolares", "UTMs", "IPCs" — CASE-003).
type entry struct {
	Valor string `json:"Valor"`
	Fecha string `json:"Fecha"`
}

func (c *Client) doOnce(ctx context.Context, code, resource string) (snaps []indicator.Snapshot, err error, retryable bool) {
	u := strings.TrimRight(c.BaseURL, "/") + "/" + resource +
		"?apikey=" + url.QueryEscape(c.APIKey) + "&formato=json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("armando request: %w", err), false
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("error de red contra la CMF: %w", sanitizeNetErr(err)), true
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("la CMF respondió HTTP %d", resp.StatusCode), true
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("api key rechazada (HTTP %d)", resp.StatusCode), false
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("la CMF respondió HTTP %d", resp.StatusCode), false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, errors.New("leyendo respuesta de la CMF"), true
	}
	// El envoltorio varía por indicador; se acepta cualquier clave cuyo valor
	// sea el arreglo de entradas.
	var payload map[string][]entry
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("respuesta inesperada de la CMF: %w", err), false
	}
	for _, entries := range payload {
		for _, e := range entries {
			s, err := e.snapshot(code)
			if err != nil {
				return nil, err, false
			}
			snaps = append(snaps, s)
		}
	}
	if len(snaps) == 0 {
		return nil, errors.New("la CMF no trajo valores para el indicador"), false
	}
	return snaps, nil, false
}

// sanitizeNetErr rescata la causa de un error de transporte SIN la URL (que
// lleva la key): http.Client envuelve todo en *url.Error, cuyo String imprime
// la URL completa, pero su causa interna ("dial tcp …: i/o timeout",
// "lookup …: no such host") es segura y distingue DNS de timeout de TLS —
// el diagnóstico que un "error de red" a secas esconde (T-004).
func sanitizeNetErr(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		return uerr.Err
	}
	return errors.New("causa no disponible (error con URL embebida)")
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (e entry) snapshot(code string) (indicator.Snapshot, error) {
	v, err := parseCLNumber(e.Valor)
	if err != nil {
		return indicator.Snapshot{}, fmt.Errorf("valor %q: %w", e.Valor, err)
	}
	d, err := time.Parse("2006-01-02", e.Fecha)
	if err != nil {
		return indicator.Snapshot{}, fmt.Errorf("fecha %q: %w", e.Fecha, err)
	}
	return indicator.Snapshot{Code: code, Value: v, Date: d}, nil
}

// clNumberRe valida el formato numérico chileno ESTRICTO: entero con grupos
// de miles de exactamente 3 dígitos separados por punto (o sin puntos), y
// decimales tras una coma. Rechazar lo ambiguo es deliberado (CASE-003): si
// la CMF cambiara a punto decimal ("12.34"), convertir a la chilena daría
// 1234 — un valor silenciosamente corrupto. Mejor fallar ruidosamente.
var clNumberRe = regexp.MustCompile(`^-?(?:\d+|\d{1,3}(?:\.\d{3})+)(?:,\d+)?$`)

// parseCLNumber convierte el formato numérico chileno de la CMF a float64:
// punto = separador de miles, coma = separador decimal (CASE-003).
// "40.842,07" → 40842.07 · "71.649" → 71649 · "0,0" → 0.
func parseCLNumber(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if !clNumberRe.MatchString(s) {
		return 0, errors.New("no es un número en formato chileno")
	}
	s = strings.ReplaceAll(s, ".", "")
	s = strings.ReplaceAll(s, ",", ".")
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, errors.New("no es un número en formato chileno")
	}
	return v, nil
}

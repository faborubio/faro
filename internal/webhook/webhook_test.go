// Tests del anti-SSRF (SAD §8). Regla de oro del repo: cero red real — los
// casos de validación usan IPs literales y "localhost" (resuelve por
// /etc/hosts); el despacho corre contra httptest con allowPrivate.
package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateURLRechazaDestinosPeligrosos(t *testing.T) {
	c := New(false)
	cases := []struct {
		name string
		url  string
	}{
		{"loopback v4", "http://127.0.0.1/hook"},
		{"loopback v4 rango", "http://127.8.8.8/hook"},
		{"loopback v6", "http://[::1]/hook"},
		{"loopback por nombre", "http://localhost:9999/hook"},
		{"privada 10.x", "https://10.0.0.5/hook"},
		{"privada 172.16", "https://172.16.1.1/hook"},
		{"privada 192.168", "https://192.168.1.10/hook"},
		{"link-local (metadata cloud)", "http://169.254.169.254/latest/meta-data/"},
		{"link-local v6", "http://[fe80::1]/hook"},
		{"ULA v6", "http://[fd00::1]/hook"},
		{"no especificada", "http://0.0.0.0/hook"},
		{"broadcast", "http://255.255.255.255/hook"},
		{"4-in-6 loopback", "http://[::ffff:127.0.0.1]/hook"},
		{"esquema ftp", "ftp://example.com/hook"},
		{"esquema vacío", "example.com/hook"},
		{"sin host", "http:///hook"},
		{"credenciales embebidas", "http://user:pass@example.com/hook"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.ValidateURL(tc.url); err == nil {
				t.Errorf("ValidateURL(%q) = nil, quiero rechazo", tc.url)
			}
		})
	}
}

func TestValidateURLAceptaDestinosPublicos(t *testing.T) {
	c := New(false)
	// IPs literales públicas: validan sin tocar DNS ni red.
	for _, u := range []string{
		"https://93.184.216.34/hook",
		"http://8.8.8.8:8080/hook?q=1",
		"https://[2606:4700::6810:84e5]/hook",
	} {
		if err := c.ValidateURL(u); err != nil {
			t.Errorf("ValidateURL(%q) = %v, quiero nil", u, err)
		}
	}
}

func TestValidateURLConEscapeDeDevAceptaLoopback(t *testing.T) {
	c := New(true)
	if err := c.ValidateURL("http://127.0.0.1:8081/hook"); err != nil {
		t.Errorf("con allowPrivate el loopback debe pasar: %v", err)
	}
}

func TestPostEntregaJSONYExige2xx(t *testing.T) {
	type payload struct {
		Indicator string  `json:"indicator"`
		Value     float64 `json:"value"`
	}
	var got payload
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := New(true) // httptest vive en loopback: hace falta el escape de dev
	err := c.Post(context.Background(), srv.URL, payload{Indicator: "dolar", Value: 1005.3})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if got.Indicator != "dolar" || got.Value != 1005.3 {
		t.Errorf("el receptor recibió %+v, quiero el payload íntegro", got)
	}
	if !strings.HasPrefix(contentType, "application/json") {
		t.Errorf("Content-Type = %q, quiero application/json", contentType)
	}
}

func TestPostSinEscapeRechazaLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("el POST alcanzó al receptor loopback: el dial pineado no bloqueó")
	}))
	defer srv.Close()

	c := New(false)
	if err := c.Post(context.Background(), srv.URL, map[string]string{"x": "y"}); err == nil {
		t.Fatal("Post a loopback sin escape: quiero error")
	}
}

func TestPostNo2xxEsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(true)
	if err := c.Post(context.Background(), srv.URL, map[string]string{}); err == nil {
		t.Fatal("receptor 500: quiero error")
	}
}

func TestPostNoSigueRedirects(t *testing.T) {
	// Un receptor que redirige (potencialmente hacia adentro) no se sigue:
	// el 302 cuenta como entrega fallida.
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
	}))
	defer srv.Close()

	c := New(true)
	if err := c.Post(context.Background(), srv.URL, map[string]string{}); err == nil {
		t.Fatal("receptor con redirect: quiero error")
	}
	if hits != 1 {
		t.Errorf("el receptor se llamó %d veces, quiero 1 (sin seguir el redirect)", hits)
	}
}

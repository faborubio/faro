// Tests del token bucket con reloj inyectado: nada de time.Sleep.
package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestLimiter(rate float64, burst, maxKeys int) (*Limiter, *time.Time) {
	l := New(rate, burst, maxKeys)
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	return l, &now
}

func TestBurstYRecarga(t *testing.T) {
	l, now := newTestLimiter(5, 3, 100)

	for i := range 3 {
		if !l.Allow("ip1") {
			t.Fatalf("petición %d de la ráfaga rechazada, el burst es 3", i+1)
		}
	}
	if l.Allow("ip1") {
		t.Fatal("cuarta petición inmediata aceptada: el bucket debía estar vacío")
	}

	// 5 tokens/s: a los 200 ms hay 1 token de nuevo, pero no 2.
	*now = now.Add(200 * time.Millisecond)
	if !l.Allow("ip1") {
		t.Fatal("tras 200 ms a 5 req/s debía haber 1 token")
	}
	if l.Allow("ip1") {
		t.Fatal("no debía haber un segundo token todavía")
	}

	// La recarga tiene tope: tras una hora quieta, la ráfaga sigue siendo 3.
	*now = now.Add(time.Hour)
	for i := range 3 {
		if !l.Allow("ip1") {
			t.Fatalf("petición %d tras la pausa rechazada", i+1)
		}
	}
	if l.Allow("ip1") {
		t.Fatal("el bucket superó el burst tras la pausa larga")
	}
}

func TestLlavesIndependientes(t *testing.T) {
	l, _ := newTestLimiter(5, 1, 100)
	if !l.Allow("ip1") {
		t.Fatal("primera petición de ip1 rechazada")
	}
	if l.Allow("ip1") {
		t.Fatal("segunda de ip1 aceptada con burst 1")
	}
	if !l.Allow("ip2") {
		t.Fatal("ip2 pagó el consumo de ip1: las llaves deben ser independientes")
	}
}

func TestMapaAcotadoSeVacia(t *testing.T) {
	l, _ := newTestLimiter(5, 1, 2)
	l.Allow("a")
	l.Allow("b")
	l.Allow("c") // tope alcanzado: el mapa se vació antes de crear "c"
	if got := len(l.buckets); got > 2 {
		t.Errorf("buckets = %d llaves, el tope es 2", got)
	}
	// "a" perdió su historia (costo aceptado del vaciado burdo): vuelve a entrar.
	if !l.Allow("a") {
		t.Error("tras el vaciado, a debía partir con bucket fresco")
	}
}

func TestMiddleware429ConRetryAfter(t *testing.T) {
	l, _ := newTestLimiter(5, 1, 100)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/uf", nil)
	req.RemoteAddr = "203.0.113.7:5555"

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("primera petición = %d, quiero 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("segunda petición = %d, quiero 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 sin Retry-After")
	}
	if !strings.Contains(rec.Body.String(), "error") {
		t.Error("el 429 no es JSON con error (el formato de la API)")
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name string
		xff  string
		addr string
		want string
	}{
		{"sin proxy (dev local)", "", "203.0.113.7:5555", "203.0.113.7"},
		{"tras el proxy de la plataforma", "203.0.113.7", "10.0.1.2:80", "203.0.113.7"},
		{"XFF multi-hop: la última entrada manda", "1.2.3.4, 203.0.113.7", "10.0.1.2:80", "203.0.113.7"},
		{"XFF spoofeado por el cliente: su entrada no es la última", "8.8.8.8, 203.0.113.7", "10.0.1.2:80", "203.0.113.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.addr
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := ClientIP(req); got != tc.want {
				t.Errorf("ClientIP = %q, quiero %q", got, tc.want)
			}
		})
	}
}

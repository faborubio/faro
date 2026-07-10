package cmf

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient apunta el adapter a un httptest.Server: cero red real en CI
// (regla de oro, SAD §10).
func newTestClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("test-key")
	c.BaseURL = srv.URL
	c.Backoff = time.Millisecond // los reintentos no deben alargar la suite
	return c, srv
}

// fixtureHandler sirve las respuestas reales grabadas de la CMF (testdata/,
// capturadas el 2026-07-09 con scripts/smoke-cmf.sh).
func fixtureHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("apikey") == "" {
			t.Error("la request no llevó apikey")
		}
		if r.URL.Query().Get("formato") != "json" {
			t.Errorf("formato = %q, quiero json", r.URL.Query().Get("formato"))
		}
		// "uf" → testdata/uf.json; "uf/2026" (serie anual) → testdata/uf-2026.json.
		code := strings.ReplaceAll(strings.TrimPrefix(r.URL.Path, "/"), "/", "-")
		b, err := os.ReadFile(filepath.Join("testdata", code+".json"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	})
}

func TestFetchMapsRealResponses(t *testing.T) {
	c, _ := newTestClient(t, fixtureHandler(t))

	snaps, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snaps) != 4 {
		t.Fatalf("len(snaps) = %d, quiero 4", len(snaps))
	}

	want := map[string]struct {
		value float64
		date  string
	}{
		"uf":    {40842.07, "2026-07-08"},
		"dolar": {927.36, "2026-07-08"},
		"utm":   {71649, "2026-07-01"},
		"ipc":   {0, "2026-06-01"},
	}
	for _, s := range snaps {
		w, ok := want[s.Code]
		if !ok {
			t.Errorf("código inesperado %q", s.Code)
			continue
		}
		if s.Value != w.value {
			t.Errorf("%s: Value = %v, quiero %v", s.Code, s.Value, w.value)
		}
		if got := s.Date.Format("2006-01-02"); got != w.date {
			t.Errorf("%s: Date = %s, quiero %s", s.Code, got, w.date)
		}
	}
}

func TestFetchYearMapsRealYearSeries(t *testing.T) {
	c, _ := newTestClient(t, fixtureHandler(t))

	snaps, err := c.FetchYear(context.Background(), "uf", 2026)
	if err != nil {
		t.Fatalf("FetchYear: %v", err)
	}
	// La captura real del 2026-07-09 trae 221 entradas — incluye ~1 mes de
	// UF futura (CASE-006): el adapter entrega TODO lo que la fuente publica;
	// filtrar el futuro es decisión del scheduler, no de la fuente.
	if len(snaps) != 221 {
		t.Fatalf("len(snaps) = %d, quiero 221", len(snaps))
	}
	first, last := snaps[0], snaps[len(snaps)-1]
	if first.Value != 39731.79 || first.Date.Format("2006-01-02") != "2026-01-01" {
		t.Errorf("primera entrada = %v @ %s, quiero 39731.79 @ 2026-01-01", first.Value, first.Date.Format("2006-01-02"))
	}
	if last.Value != 40844.79 || last.Date.Format("2006-01-02") != "2026-08-09" {
		t.Errorf("última entrada = %v @ %s, quiero 40844.79 @ 2026-08-09", last.Value, last.Date.Format("2006-01-02"))
	}
	for _, s := range snaps {
		if s.Code != "uf" {
			t.Fatalf("snapshot con código %q, quiero uf", s.Code)
		}
	}
}

func TestParseCLNumber(t *testing.T) {
	cases := []struct {
		in      string
		want    float64
		wantErr bool
	}{
		{"40.842,07", 40842.07, false}, // UF real (CASE-003)
		{"927,36", 927.36, false},      // dólar real
		{"71.649", 71649, false},       // UTM real: el punto es de miles
		{"0,0", 0, false},              // IPC real
		{"-0,2", -0.2, false},          // IPC negativo (deflación)
		{"1.234.567,89", 1234567.89, false},
		{"1234567,89", 1234567.89, false}, // sin puntos de miles también es válido
		{"", 0, true},
		{"  ", 0, true},
		{"abc", 0, true},
		// Formato internacional (punto decimal): DEBE fallar ruidosamente,
		// no convertirse en 1234 (CASE-003 — corrupción silenciosa).
		{"12.34", 0, true},
		{"1.2345", 0, true},
		{"40,842.07", 0, true}, // formato en-US completo
		{"1..5", 0, true},
		{"1,2,3", 0, true},
	}
	for _, tc := range cases {
		got, err := parseCLNumber(tc.in)
		if tc.wantErr != (err != nil) {
			t.Errorf("parseCLNumber(%q) error = %v, wantErr %v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseCLNumber(%q) = %v, quiero %v", tc.in, got, tc.want)
		}
	}
}

func TestFetchRetriesOn5xxThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	fixtures := fixtureHandler(t)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway) // primer intento: transitorio
			return
		}
		fixtures.ServeHTTP(w, r)
	}))
	c.Codes = []string{"uf"}

	snaps, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch tras reintento: %v", err)
	}
	if len(snaps) != 1 || calls.Load() != 2 {
		t.Errorf("snaps=%d calls=%d, quiero 1 snap en 2 llamadas", len(snaps), calls.Load())
	}
}

func TestFetchGivesUpAfterRetries(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	c.Codes = []string{"uf"}

	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("quiero error tras agotar reintentos")
	}
	if calls.Load() != 3 {
		t.Errorf("calls = %d, quiero 3 (1 + 2 reintentos)", calls.Load())
	}
}

func TestFetchDoesNotRetryOnRejectedKey(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	c.Codes = []string{"uf"}

	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("quiero error con key rechazada")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, quiero 1 (4xx no se reintenta)", calls.Load())
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Error("el error expone la API key")
	}
}

func TestNetworkErrorCarriesCauseButNeverTheKey(t *testing.T) {
	// T-004: "error de red" a secas no distingue DNS de timeout de TLS. La
	// causa interna del *url.Error es diagnóstico seguro; la key (que viaja
	// en la URL) jamás debe aparecer.
	c, srv := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close() // puerto muerto → error de transporte real (connection refused)
	c.Codes = []string{"uf"}

	_, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("quiero error de red")
	}
	if !strings.Contains(err.Error(), "error de red contra la CMF:") {
		t.Errorf("el error no trae la causa de red: %v", err)
	}
	if strings.Contains(err.Error(), "test-key") || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("el error expone la key o la URL: %v", err)
	}
}

func TestFetchPartialFailureReturnsWhatItGot(t *testing.T) {
	fixtures := fixtureHandler(t)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimPrefix(r.URL.Path, "/") == "ipc" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fixtures.ServeHTTP(w, r)
	}))

	snaps, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatal("quiero error agregado por el indicador caído")
	}
	if len(snaps) != 3 {
		t.Errorf("len(snaps) = %d, quiero 3 (lo parcial se entrega igual)", len(snaps))
	}
	if !strings.Contains(err.Error(), "ipc") {
		t.Errorf("el error no identifica al indicador caído: %v", err)
	}
}

func TestFetchRejectsMalformedJSON(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"Mensaje": "no soy un arreglo de valores"}`))
	}))
	c.Codes = []string{"uf"}

	if _, err := c.Fetch(context.Background()); err == nil {
		t.Fatal("quiero error ante respuesta con forma inesperada")
	}
}

func TestName(t *testing.T) {
	if got := New("k").Name(); got != "cmf" {
		t.Errorf("Name() = %q, quiero cmf", got)
	}
}

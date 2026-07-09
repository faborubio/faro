// Tests de la API con httptest y un store falso: cero BD, cero red (regla 4
// del repo). El contrato bajo prueba es el del SAD §7: leer de Postgres con
// cache por delante, 404 para lo desconocido, 400 para rangos malformados.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

type fakeStore struct {
	mu      sync.Mutex
	calls   int
	catalog map[string]store.Indicator
	values  map[string][]indicator.Snapshot // ascendente por fecha
}

func (f *fakeStore) bump() {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
}

func (f *fakeStore) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeStore) GetIndicator(ctx context.Context, code string) (store.Indicator, error) {
	f.bump()
	ind, ok := f.catalog[code]
	if !ok {
		return store.Indicator{}, fmt.Errorf("indicador %q: %w", code, store.ErrNotFound)
	}
	return ind, nil
}

func (f *fakeStore) Latest(ctx context.Context, code string) (indicator.Snapshot, error) {
	f.bump()
	vals := f.values[code]
	if len(vals) == 0 {
		return indicator.Snapshot{}, fmt.Errorf("último valor de %q: %w", code, store.ErrNotFound)
	}
	return vals[len(vals)-1], nil
}

func (f *fakeStore) History(ctx context.Context, code string, from, to time.Time) ([]indicator.Snapshot, error) {
	f.bump()
	var out []indicator.Snapshot
	for _, sn := range f.values[code] {
		if !sn.Date.Before(from) && !sn.Date.After(to) {
			out = append(out, sn)
		}
	}
	return out, nil
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func newTestServer(t *testing.T, ttl time.Duration) (*httptest.Server, *fakeStore) {
	t.Helper()
	fs := &fakeStore{
		catalog: map[string]store.Indicator{
			"dolar": {Code: "dolar", Name: "Dólar observado", Unit: "CLP", Cadence: indicator.CadenceDaily},
			"uf":    {Code: "uf", Name: "Unidad de Fomento", Unit: "CLP", Cadence: indicator.CadenceDaily},
		},
		values: map[string][]indicator.Snapshot{
			"dolar": {
				{Code: "dolar", Value: 943.15, Date: date(2026, 7, 7)},
				{Code: "dolar", Value: 935.71, Date: date(2026, 7, 9)},
			},
		},
	}
	srv := New(fs, ttl, nil)
	srv.now = func() time.Time { return date(2026, 7, 9) } // /history determinista
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, fs
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("leyendo cuerpo: %v", err)
	}
	return resp, body
}

func TestCurrent(t *testing.T) {
	ts, _ := newTestServer(t, time.Minute)

	resp, body := get(t, ts.URL+"/api/dolar")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quiero 200 (cuerpo: %s)", resp.StatusCode, body)
	}
	var got currentResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	want := currentResponse{Code: "dolar", Name: "Dólar observado", Unit: "CLP", Value: 935.71, Date: "2026-07-09"}
	if got != want {
		t.Errorf("respuesta = %+v, quiero %+v", got, want)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestCurrentDesconocidoY404(t *testing.T) {
	ts, _ := newTestServer(t, time.Minute)

	// Código fuera del catálogo.
	resp, body := get(t, ts.URL+"/api/noexiste")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("código desconocido: status = %d, quiero 404", resp.StatusCode)
	}
	var e map[string]string
	if err := json.Unmarshal(body, &e); err != nil || e["error"] == "" {
		t.Errorf("el 404 debe traer JSON {error: …}, vino: %s", body)
	}

	// En el catálogo pero aún sin valores.
	resp, _ = get(t, ts.URL+"/api/uf")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("sin valores: status = %d, quiero 404", resp.StatusCode)
	}
}

func TestHistory(t *testing.T) {
	ts, _ := newTestServer(t, time.Minute)

	resp, body := get(t, ts.URL+"/api/dolar/history?desde=2026-07-01&hasta=2026-07-31")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (cuerpo: %s)", resp.StatusCode, body)
	}
	var got historyResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if len(got.Values) != 2 || got.Values[0].Date != "2026-07-07" || got.Values[1].Value != 935.71 {
		t.Errorf("values = %+v, quiero los 2 puntos de julio en orden", got.Values)
	}
	if got.Desde != "2026-07-01" || got.Hasta != "2026-07-31" {
		t.Errorf("eco del rango = %s..%s", got.Desde, got.Hasta)
	}
}

func TestHistoryDefaults(t *testing.T) {
	// Sin ?desde ni ?hasta: últimos 30 días hasta "hoy" (now inyectado).
	ts, _ := newTestServer(t, time.Minute)

	resp, body := get(t, ts.URL+"/api/dolar/history")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (cuerpo: %s)", resp.StatusCode, body)
	}
	var got historyResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if got.Desde != "2026-06-09" || got.Hasta != "2026-07-09" {
		t.Errorf("rango default = %s..%s, quiero 2026-06-09..2026-07-09", got.Desde, got.Hasta)
	}
	if len(got.Values) != 2 {
		t.Errorf("values = %+v, quiero 2 puntos", got.Values)
	}
}

func TestHistoryRangoVacioEsLista(t *testing.T) {
	ts, _ := newTestServer(t, time.Minute)

	resp, body := get(t, ts.URL+"/api/dolar/history?desde=2020-01-01&hasta=2020-12-31")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	// La lista vacía debe serializar como [], no null: contrato para clientes.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("JSON inválido: %v", err)
	}
	if string(raw["values"]) != "[]" {
		t.Errorf("values = %s, quiero []", raw["values"])
	}
}

func TestHistoryValidaciones(t *testing.T) {
	ts, _ := newTestServer(t, time.Minute)

	for _, tc := range []struct{ name, query string }{
		{"desde malformado", "?desde=07-01-2026"},
		{"hasta malformado", "?hasta=ayer"},
		{"desde posterior a hasta", "?desde=2026-07-31&hasta=2026-07-01"},
	} {
		resp, _ := get(t, ts.URL+"/api/dolar/history"+tc.query)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, quiero 400", tc.name, resp.StatusCode)
		}
	}

	resp, _ := get(t, ts.URL+"/api/noexiste/history")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("history de código desconocido: status = %d, quiero 404", resp.StatusCode)
	}
}

func TestCacheHitYExpiracion(t *testing.T) {
	ts, fs := newTestServer(t, 80*time.Millisecond)

	resp, _ := get(t, ts.URL+"/api/dolar")
	if h := resp.Header.Get("X-Cache"); h != "MISS" {
		t.Errorf("primera request: X-Cache = %q, quiero MISS", h)
	}
	after1 := fs.callCount()

	resp, body := get(t, ts.URL+"/api/dolar")
	if h := resp.Header.Get("X-Cache"); h != "HIT" {
		t.Errorf("segunda request: X-Cache = %q, quiero HIT", h)
	}
	if fs.callCount() != after1 {
		t.Errorf("el HIT tocó el store (%d → %d llamadas)", after1, fs.callCount())
	}
	var got currentResponse
	if err := json.Unmarshal(body, &got); err != nil || got.Value != 935.71 {
		t.Errorf("cuerpo cacheado corrupto: %s", body)
	}

	// Query distinta = entrada distinta.
	resp, _ = get(t, ts.URL+"/api/dolar/history?desde=2026-07-01")
	if h := resp.Header.Get("X-Cache"); h != "MISS" {
		t.Errorf("otra URL: X-Cache = %q, quiero MISS", h)
	}

	// Expiración: pasado el TTL vuelve al store.
	time.Sleep(100 * time.Millisecond)
	resp, _ = get(t, ts.URL+"/api/dolar")
	if h := resp.Header.Get("X-Cache"); h != "MISS" {
		t.Errorf("tras expirar el TTL: X-Cache = %q, quiero MISS", h)
	}
}

func TestErroresNoSeCachean(t *testing.T) {
	ts, fs := newTestServer(t, time.Minute)

	get(t, ts.URL+"/api/noexiste")
	before := fs.callCount()
	get(t, ts.URL+"/api/noexiste")
	if fs.callCount() == before {
		t.Error("el 404 se sirvió desde cache: los errores no deben cachearse")
	}
}

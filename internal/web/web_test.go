// Tests del dashboard: store falso, sin BD. El render debe funcionar tanto
// con histórico como en el estado "recién deployado" (sin valores).
package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

type fakeStore struct {
	catalog []store.Indicator
	latest  map[string]indicator.Snapshot
}

func (f *fakeStore) ListIndicators(ctx context.Context) ([]store.Indicator, error) {
	return f.catalog, nil
}

func (f *fakeStore) Latest(ctx context.Context, code string) (indicator.Snapshot, error) {
	if s, ok := f.latest[code]; ok {
		return s, nil
	}
	return indicator.Snapshot{}, store.ErrNotFound
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestIndexRindeTarjetas(t *testing.T) {
	st := &fakeStore{
		// El catálogo llega alfabético (dolar, uf): el dashboard reordena.
		catalog: []store.Indicator{
			{Code: "dolar", Name: "Dólar observado", Unit: "CLP", Cadence: indicator.CadenceDaily},
			{Code: "uf", Name: "Unidad de Fomento", Unit: "CLP", Cadence: indicator.CadenceDaily},
		},
		latest: map[string]indicator.Snapshot{
			"uf":    {Code: "uf", Value: 40844.79, Date: time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)},
			"dolar": {Code: "dolar", Value: 935.71, Date: time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)},
		},
	}
	rec := get(t, New(st, nil).Handler(), "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, quiero 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Unidad de Fomento", "40.844,79", // valor a la chilena
		"Dólar observado", "935,71",
		`data-code="uf"`, "al 2026-07-09",
		"CMF", // atribución de la fuente (gate legal)
	} {
		if !strings.Contains(body, want) {
			t.Errorf("el HTML no contiene %q", want)
		}
	}
	// uf va antes que dolar aunque el catálogo venga alfabético.
	if strings.Index(body, `data-code="uf"`) > strings.Index(body, `data-code="dolar"`) {
		t.Error("el orden de tarjetas no respeta displayRank (uf primero)")
	}
	// El convertidor está, con el valor crudo (no el string chileno) por opción.
	if !strings.Contains(body, `id="conv"`) || !strings.Contains(body, `value="40844.79"`) {
		t.Error("falta el convertidor o sus valores crudos")
	}
}

func TestConverterExcluyeElIPCYDesapareceSinValores(t *testing.T) {
	// El IPC es un % (no convertible); y sin ningún valor CLP no hay convertidor.
	st := &fakeStore{
		catalog: []store.Indicator{
			{Code: "ipc", Name: "IPC", Unit: "%", Cadence: indicator.CadenceMonthly},
			{Code: "uf", Name: "Unidad de Fomento", Unit: "CLP", Cadence: indicator.CadenceDaily},
		},
		latest: map[string]indicator.Snapshot{
			"ipc": {Code: "ipc", Value: 0.2, Date: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	body := get(t, New(st, nil).Handler(), "/").Body.String()
	if strings.Contains(body, `id="conv"`) {
		t.Error("hay convertidor sin unidades convertibles (solo IPC con valor)")
	}
}

func TestIndexSinValoresNoRevienta(t *testing.T) {
	st := &fakeStore{catalog: []store.Indicator{
		{Code: "uf", Name: "Unidad de Fomento", Unit: "CLP", Cadence: indicator.CadenceDaily},
	}}
	rec := get(t, New(st, nil).Handler(), "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("GET / sin valores = %d, quiero 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "aún sin datos") {
		t.Error("la tarjeta vacía no explica que faltan datos")
	}
}

func TestStaticSirveAssetsEmbebidos(t *testing.T) {
	h := New(&fakeStore{}, nil).Handler()
	for _, path := range []string{"/static/app.js", "/static/chart.umd.js"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, quiero 200", path, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Errorf("GET %s sin Cache-Control (%q)", path, cc)
		}
	}
}

func TestRutaDesconocidaEs404(t *testing.T) {
	rec := get(t, New(&fakeStore{}, nil).Handler(), "/no-existe")
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /no-existe = %d, quiero 404", rec.Code)
	}
}

func TestFormatCL(t *testing.T) {
	cases := []struct {
		v    float64
		unit string
		want string
	}{
		{40844.79, "CLP", "40.844,79"},
		{935.71, "CLP", "935,71"},
		{71649, "CLP", "71.649"},
		{1234567.5, "CLP", "1.234.567,5"},
		{0, "%", "0,0"}, // el 0,0 del IPC es un dato real (CASE-005)
		{-0.2, "%", "-0,2"}, // deflación
	}
	for _, tc := range cases {
		if got := formatCL(tc.v, tc.unit); got != tc.want {
			t.Errorf("formatCL(%v, %q) = %q, quiero %q", tc.v, tc.unit, got, tc.want)
		}
	}
}

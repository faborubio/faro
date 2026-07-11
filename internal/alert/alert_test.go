// Tests unitarios del evaluador: store y poster falsos, cero red y cero BD.
// La semántica bajo prueba es el CRUCE (ADR-006): dispara al pasar de "no
// satisface" a "satisface", nunca mientras se mantiene.
package alert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

type fakeStore struct {
	alerts    map[string][]store.Alert
	prev      map[string]indicator.Snapshot // por código; ausente = sin anterior
	latest    map[string]indicator.Snapshot // por código; ausente = el snap evaluado es el vigente
	triggered []int64
	prevCalls int
}

func (f *fakeStore) ListActiveAlertsByCode(ctx context.Context, code string) ([]store.Alert, error) {
	return f.alerts[code], nil
}

func (f *fakeStore) PreviousValue(ctx context.Context, code string, before time.Time) (indicator.Snapshot, error) {
	f.prevCalls++
	if s, ok := f.prev[code]; ok {
		return s, nil
	}
	return indicator.Snapshot{}, store.ErrNotFound
}

func (f *fakeStore) Latest(ctx context.Context, code string) (indicator.Snapshot, error) {
	if s, ok := f.latest[code]; ok {
		return s, nil
	}
	// Fecha cero: cualquier snap evaluado pasa como "el vigente".
	return indicator.Snapshot{Code: code}, nil
}

func (f *fakeStore) GetIndicator(ctx context.Context, code string) (store.Indicator, error) {
	return store.Indicator{Code: code, Name: "Indicador " + code, Unit: "CLP"}, nil
}

func (f *fakeStore) MarkAlertTriggered(ctx context.Context, id int64) error {
	f.triggered = append(f.triggered, id)
	return nil
}

type fakePoster struct {
	posts []Payload
	urls  []string
	err   error
}

func (f *fakePoster) Post(ctx context.Context, url string, payload any) error {
	if f.err != nil {
		return f.err
	}
	f.urls = append(f.urls, url)
	f.posts = append(f.posts, payload.(Payload))
	return nil
}

func snap(code string, value float64, day int) indicator.Snapshot {
	return indicator.Snapshot{Code: code, Value: value, Date: time.Date(2026, 7, day, 0, 0, 0, 0, time.UTC)}
}

func alertGT(id int64, code string, threshold float64) store.Alert {
	return store.Alert{ID: id, Token: "tok", IndicatorCode: code, Operator: store.OpGreater, Threshold: threshold, WebhookURL: "https://example.com/hook", Active: true}
}

func alertLT(id int64, code string, threshold float64) store.Alert {
	a := alertGT(id, code, threshold)
	a.Operator = store.OpLess
	return a
}

func service(st *fakeStore, p *fakePoster) *Service {
	return New(st, p, nil)
}

func TestCruceDispara(t *testing.T) {
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {alertGT(1, "dolar", 1000)}},
		prev:   map[string]indicator.Snapshot{"dolar": snap("dolar", 995.0, 9)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 1005.3, 10)})

	if len(p.posts) != 1 {
		t.Fatalf("posts = %d, quiero 1 (995 → 1005.3 cruza el umbral 1000)", len(p.posts))
	}
	got := p.posts[0]
	if got.Indicator != "dolar" || got.Value != 1005.3 || got.Threshold != 1000 || got.Operator != "gt" || got.Date != "2026-07-10" {
		t.Errorf("payload = %+v, no coincide con el cruce", got)
	}
	if len(st.triggered) != 1 || st.triggered[0] != 1 {
		t.Errorf("triggered = %v, quiero [1]", st.triggered)
	}
}

func TestSinCruceNoRedispara(t *testing.T) {
	// Ayer ya estaba sobre el umbral: hoy no es noticia.
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {alertGT(1, "dolar", 1000)}},
		prev:   map[string]indicator.Snapshot{"dolar": snap("dolar", 1002.0, 9)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 1005.3, 10)})

	if len(p.posts) != 0 {
		t.Errorf("posts = %d, quiero 0 (ya estaba cruzado)", len(p.posts))
	}
}

func TestCondicionFalsaNoDispara(t *testing.T) {
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {alertGT(1, "dolar", 1000)}},
		prev:   map[string]indicator.Snapshot{"dolar": snap("dolar", 990.0, 9)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 998.0, 10)})

	if len(p.posts) != 0 {
		t.Errorf("posts = %d, quiero 0 (998 no supera 1000)", len(p.posts))
	}
}

func TestSinValorAnteriorDispara(t *testing.T) {
	// Primer valor del indicador: no hay historia que diga "ya estaba ahí".
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {alertGT(1, "dolar", 1000)}},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 1005.3, 10)})

	if len(p.posts) != 1 {
		t.Errorf("posts = %d, quiero 1 (sin anterior + condición cierta)", len(p.posts))
	}
}

func TestOperadorLT(t *testing.T) {
	st := &fakeStore{
		alerts: map[string][]store.Alert{"uf": {alertLT(2, "uf", 40000)}},
		prev:   map[string]indicator.Snapshot{"uf": snap("uf", 40100, 9)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("uf", 39990, 10)})

	if len(p.posts) != 1 || p.posts[0].Operator != "lt" {
		t.Errorf("posts = %+v, quiero un disparo lt (40100 → 39990 cae bajo 40000)", p.posts)
	}
}

func TestUmbralYValorCeroNoSonCentinelas(t *testing.T) {
	// CASE-005: el IPC puede valer 0,0% legítimamente. Una alerta lt 0.5 con
	// IPC pasando de 0.8 a 0.0 debe disparar — el 0 es un valor real.
	st := &fakeStore{
		alerts: map[string][]store.Alert{"ipc": {alertLT(3, "ipc", 0.5)}},
		prev:   map[string]indicator.Snapshot{"ipc": snap("ipc", 0.8, 1)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("ipc", 0.0, 10)})

	if len(p.posts) != 1 {
		t.Errorf("posts = %d, quiero 1 (0.0 < 0.5, el cero es dato real)", len(p.posts))
	}
}

func TestPostFallidoNoMarcaDisparo(t *testing.T) {
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {alertGT(1, "dolar", 1000)}},
	}
	p := &fakePoster{err: errors.New("receptor caído")}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 1005.3, 10)})

	if len(st.triggered) != 0 {
		t.Errorf("triggered = %v, quiero vacío (el POST no entregó)", st.triggered)
	}
}

func TestBackfillSoloEvaluaElMasReciente(t *testing.T) {
	// Un lote con historia (backfill) no dispara por valores viejos: solo el
	// más reciente por código cuenta.
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {alertGT(1, "dolar", 1000)}},
		prev:   map[string]indicator.Snapshot{"dolar": snap("dolar", 1010, 9)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{
		snap("dolar", 1005, 8),  // viejo: cruzaba, pero no es el último
		snap("dolar", 1020, 10), // último: ya estaba cruzado desde el día 9
		snap("dolar", 990, 7),
	})

	if len(p.posts) != 0 {
		t.Errorf("posts = %d, quiero 0 (el último valor no cruza respecto a su anterior)", len(p.posts))
	}
}

func TestCorreccionHistoricaNoDispara(t *testing.T) {
	// La fuente corrige un valor VIEJO (día 9: 995 → 1001) mientras el día 10
	// ya existe: el valor vigente no se movió, así que no hay cruce que
	// anunciar — alertar con el dato de ayer sería mentir.
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {alertGT(1, "dolar", 1000)}},
		prev:   map[string]indicator.Snapshot{"dolar": snap("dolar", 990, 8)},
		latest: map[string]indicator.Snapshot{"dolar": snap("dolar", 1005, 10)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 1001, 9)})

	if len(p.posts) != 0 {
		t.Errorf("posts = %d, quiero 0 (corrección histórica, no cruce del vigente)", len(p.posts))
	}
}

func TestSinAlertasNoConsultaHistoria(t *testing.T) {
	st := &fakeStore{}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 1005, 10)})

	if st.prevCalls != 0 {
		t.Errorf("PreviousValue llamado %d veces sin alertas activas, quiero 0", st.prevCalls)
	}
}

func TestVariasAlertasMismoIndicador(t *testing.T) {
	st := &fakeStore{
		alerts: map[string][]store.Alert{"dolar": {
			alertGT(1, "dolar", 1000), // cruza
			alertGT(2, "dolar", 1100), // no alcanza
			alertLT(3, "dolar", 900),  // condición falsa
		}},
		prev: map[string]indicator.Snapshot{"dolar": snap("dolar", 995, 9)},
	}
	p := &fakePoster{}
	service(st, p).ValuesChanged(context.Background(), []indicator.Snapshot{snap("dolar", 1005, 10)})

	if len(p.posts) != 1 || p.posts[0].Threshold != 1000 {
		t.Errorf("posts = %+v, quiero solo el disparo del umbral 1000", p.posts)
	}
}

// Tests unitarios del scheduler: fuente y store falsos, cero red y cero BD.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

type fakeSource struct {
	snaps []indicator.Snapshot
	err   error
	calls int
	mu    sync.Mutex
}

func (f *fakeSource) Fetch(ctx context.Context) ([]indicator.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.snaps, f.err
}

func (f *fakeSource) Name() string { return "fake" }

func (f *fakeSource) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// histSource es un fakeSource que además implementa HistoricalSource.
type histSource struct {
	fakeSource
	byYear    map[int][]indicator.Snapshot // serie a devolver por año (todos los códigos)
	yearErr   map[int]error
	mu2       sync.Mutex
	yearCalls []string // "code/año", en orden
}

func (h *histSource) FetchYear(ctx context.Context, code string, year int) ([]indicator.Snapshot, error) {
	h.mu2.Lock()
	h.yearCalls = append(h.yearCalls, fmt.Sprintf("%s/%d", code, year))
	h.mu2.Unlock()
	if err := h.yearErr[year]; err != nil {
		return nil, err
	}
	var out []indicator.Snapshot
	for _, s := range h.byYear[year] {
		if s.Code == code {
			out = append(out, s)
		}
	}
	return out, nil
}

type finishCall struct {
	id      int64
	status  store.SyncStatus
	updated int
	errMsg  string
	ctxErr  error // estado del contexto al momento de cerrar el run
}

type fakeStore struct {
	changedPerUpsert int
	upsertErr        error
	startErr         error
	catalog          []store.Indicator             // para ListIndicators (backfill)
	latest           map[string]indicator.Snapshot // códigos que ya tienen valores

	mu       sync.Mutex
	upserted [][]indicator.Snapshot
	started  []string
	finished []finishCall
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

func (f *fakeStore) UpsertSnapshots(ctx context.Context, snaps []indicator.Snapshot) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserted = append(f.upserted, snaps)
	if f.upsertErr != nil {
		return f.changedPerUpsert, f.upsertErr
	}
	return f.changedPerUpsert, nil
}

func (f *fakeStore) StartSyncRun(ctx context.Context, source string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.started = append(f.started, source)
	return int64(len(f.started)), nil
}

func (f *fakeStore) FinishSyncRun(ctx context.Context, id int64, status store.SyncStatus, updated int, errMsg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finished = append(f.finished, finishCall{id: id, status: status, updated: updated, errMsg: errMsg, ctxErr: ctx.Err()})
	return nil
}

func (f *fakeStore) lastFinish(t *testing.T) finishCall {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.finished) == 0 {
		t.Fatal("ningún sync_run cerrado")
	}
	return f.finished[len(f.finished)-1]
}

func snap(code string, value float64) indicator.Snapshot {
	return indicator.Snapshot{Code: code, Value: value, Date: time.Date(2026, 7, 9, 0, 0, 0, 0, time.UTC)}
}

func TestRefreshOnceOK(t *testing.T) {
	src := &fakeSource{snaps: []indicator.Snapshot{snap("uf", 40842.07), snap("dolar", 945.80)}}
	st := &fakeStore{changedPerUpsert: 2}
	r := New(src, st, 0, nil)

	if err := r.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	if len(st.started) != 1 || st.started[0] != "fake" {
		t.Errorf("sync_runs abiertos = %v, quiero uno de 'fake'", st.started)
	}
	fin := st.lastFinish(t)
	if fin.status != store.SyncOK || fin.updated != 2 || fin.errMsg != "" {
		t.Errorf("cierre = %+v, quiero (ok, 2, sin error)", fin)
	}
	if len(st.upserted) != 1 || len(st.upserted[0]) != 2 {
		t.Errorf("upserted = %v, quiero un lote de 2 snapshots", st.upserted)
	}
}

func TestRefreshOnceSinCambiosEsOK(t *testing.T) {
	// ADR-011: fuente sana que trae valores ya conocidos (mensuales, fin de
	// semana) cierra 'ok' con 0 actualizados — no es un fallo.
	src := &fakeSource{snaps: []indicator.Snapshot{snap("utm", 71649)}}
	st := &fakeStore{changedPerUpsert: 0}
	r := New(src, st, 0, nil)

	if err := r.RefreshOnce(context.Background()); err != nil {
		t.Fatalf("RefreshOnce: %v", err)
	}
	fin := st.lastFinish(t)
	if fin.status != store.SyncOK || fin.updated != 0 {
		t.Errorf("cierre = %+v, quiero (ok, 0)", fin)
	}
}

func TestRefreshOnceFallaParcial(t *testing.T) {
	// La fuente entrega lo que pudo + error agregado: se persiste lo parcial
	// y el run cierra 'error' con el detalle (SAD §8).
	src := &fakeSource{
		snaps: []indicator.Snapshot{snap("uf", 40842.07)},
		err:   errors.New("ipc: la CMF respondió HTTP 500"),
	}
	st := &fakeStore{changedPerUpsert: 1}
	r := New(src, st, 0, nil)

	err := r.RefreshOnce(context.Background())
	if err == nil {
		t.Fatal("RefreshOnce con fuente a medias: quiero error")
	}
	if len(st.upserted) != 1 || len(st.upserted[0]) != 1 {
		t.Errorf("lo parcial no se persistió: upserted = %v", st.upserted)
	}
	fin := st.lastFinish(t)
	if fin.status != store.SyncError || fin.updated != 1 {
		t.Errorf("cierre = %+v, quiero (error, 1)", fin)
	}
	if !strings.Contains(fin.errMsg, "HTTP 500") {
		t.Errorf("errMsg = %q, quiero el detalle del fallo", fin.errMsg)
	}
}

func TestRefreshOnceUpsertFalla(t *testing.T) {
	src := &fakeSource{snaps: []indicator.Snapshot{snap("uf", 40842.07)}}
	st := &fakeStore{changedPerUpsert: 0, upsertErr: errors.New("conexión perdida")}
	r := New(src, st, 0, nil)

	if err := r.RefreshOnce(context.Background()); err == nil {
		t.Fatal("upsert fallido: quiero error")
	}
	fin := st.lastFinish(t)
	if fin.status != store.SyncError || !strings.Contains(fin.errMsg, "conexión perdida") {
		t.Errorf("cierre = %+v, quiero status error con el detalle", fin)
	}
}

func TestRefreshOnceSinBDNoLlamaALaFuente(t *testing.T) {
	src := &fakeSource{}
	st := &fakeStore{startErr: errors.New("BD caída")}
	r := New(src, st, 0, nil)

	if err := r.RefreshOnce(context.Background()); err == nil {
		t.Fatal("StartSyncRun fallido: quiero error")
	}
	if src.callCount() != 0 {
		t.Errorf("la fuente se llamó %d veces sin BD donde persistir, quiero 0", src.callCount())
	}
}

// cancelingSource cancela el contexto durante Fetch: simula un SIGTERM que
// llega en pleno refresco.
type cancelingSource struct {
	cancel context.CancelFunc
}

func (c *cancelingSource) Fetch(ctx context.Context) ([]indicator.Snapshot, error) {
	c.cancel()
	return nil, ctx.Err()
}

func (c *cancelingSource) Name() string { return "cancelador" }

func TestRefreshOnceCierraElRunAunqueElContextoMuera(t *testing.T) {
	// SIGTERM a mitad del ciclo: el sync_run debe cerrarse igual (con
	// contexto vivo), no quedar huérfano en 'running'.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	src := &cancelingSource{cancel: cancel}
	st := &fakeStore{}
	r := New(src, st, 0, nil)

	if err := r.RefreshOnce(ctx); err == nil {
		t.Fatal("fetch abortado por el contexto: quiero error")
	}
	fin := st.lastFinish(t)
	if fin.status != store.SyncError {
		t.Errorf("status = %q, quiero error", fin.status)
	}
	if fin.ctxErr != nil {
		t.Errorf("FinishSyncRun corrió con contexto muerto (%v): el cierre del run debe sobrevivir al shutdown", fin.ctxErr)
	}
}

func catalog(codes ...string) []store.Indicator {
	out := make([]store.Indicator, len(codes))
	for i, c := range codes {
		out[i] = store.Indicator{Code: c}
	}
	return out
}

func TestBackfillSoloIndicadoresVacios(t *testing.T) {
	// uf ya tiene valores; dolar está vacío → solo dolar se backfillea, con
	// el año actual y el anterior.
	year := time.Now().UTC().Year()
	src := &histSource{byYear: map[int][]indicator.Snapshot{
		year - 1: {snap("dolar", 900.10)},
		year:     {snap("dolar", 945.80)},
	}}
	st := &fakeStore{
		changedPerUpsert: 2,
		catalog:          catalog("uf", "dolar"),
		latest:           map[string]indicator.Snapshot{"uf": snap("uf", 40842.07)},
	}
	r := New(src, st, 0, nil)

	if err := r.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	wantCalls := []string{fmt.Sprintf("dolar/%d", year-1), fmt.Sprintf("dolar/%d", year)}
	if fmt.Sprint(src.yearCalls) != fmt.Sprint(wantCalls) {
		t.Errorf("FetchYear llamado con %v, quiero %v", src.yearCalls, wantCalls)
	}
	if len(st.started) != 1 || st.started[0] != "fake/backfill" {
		t.Errorf("sync_runs abiertos = %v, quiero uno de 'fake/backfill'", st.started)
	}
	fin := st.lastFinish(t)
	if fin.status != store.SyncOK || fin.updated != 2 {
		t.Errorf("cierre = %+v, quiero (ok, 2)", fin)
	}
	if len(st.upserted) != 1 || len(st.upserted[0]) != 2 {
		t.Errorf("upserted = %v, quiero un lote de 2 snapshots", st.upserted)
	}
}

func TestBackfillDescartaFechasFuturas(t *testing.T) {
	// La UF llega publicada ~1 mes adelante (CASE-006): lo futuro no se
	// persiste, o Latest lo reportaría como "vigente".
	now := time.Now().UTC()
	pasado := indicator.Snapshot{Code: "uf", Value: 40000, Date: now.AddDate(0, 0, -10)}
	futuro := indicator.Snapshot{Code: "uf", Value: 41000, Date: now.AddDate(0, 0, 10)}
	src := &histSource{byYear: map[int][]indicator.Snapshot{
		now.Year(): {pasado, futuro},
	}}
	st := &fakeStore{changedPerUpsert: 1, catalog: catalog("uf")}
	r := New(src, st, 0, nil)

	if err := r.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(st.upserted) != 1 || len(st.upserted[0]) != 1 {
		t.Fatalf("upserted = %v, quiero solo el snapshot pasado", st.upserted)
	}
	if got := st.upserted[0][0]; got.Value != pasado.Value {
		t.Errorf("se persistió %v, quiero el valor pasado %v", got.Value, pasado.Value)
	}
}

func TestBackfillSinPendientesEsNoOp(t *testing.T) {
	src := &histSource{}
	st := &fakeStore{
		catalog: catalog("uf"),
		latest:  map[string]indicator.Snapshot{"uf": snap("uf", 40842.07)},
	}
	r := New(src, st, 0, nil)

	if err := r.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(src.yearCalls) != 0 || len(st.started) != 0 || len(st.upserted) != 0 {
		t.Errorf("no-op esperado: llamadas=%v runs=%v upserts=%v", src.yearCalls, st.started, st.upserted)
	}
}

func TestBackfillFuenteSinHistoricoEsNoOp(t *testing.T) {
	// fakeSource no implementa HistoricalSource: el backfill se salta sin error.
	src := &fakeSource{}
	st := &fakeStore{catalog: catalog("uf")}
	r := New(src, st, 0, nil)

	if err := r.Backfill(context.Background()); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(st.started) != 0 {
		t.Errorf("sync_runs abiertos = %v, quiero ninguno", st.started)
	}
}

func TestBackfillFallaParcial(t *testing.T) {
	// Un año falla, el otro no: se persiste lo obtenido y el run cierra
	// 'error' con el detalle (mismo contrato que RefreshOnce).
	year := time.Now().UTC().Year()
	src := &histSource{
		byYear:  map[int][]indicator.Snapshot{year: {snap("uf", 40842.07)}},
		yearErr: map[int]error{year - 1: errors.New("la CMF respondió HTTP 500")},
	}
	st := &fakeStore{changedPerUpsert: 1, catalog: catalog("uf")}
	r := New(src, st, 0, nil)

	if err := r.Backfill(context.Background()); err == nil {
		t.Fatal("backfill a medias: quiero error")
	}
	if len(st.upserted) != 1 || len(st.upserted[0]) != 1 {
		t.Errorf("lo parcial no se persistió: upserted = %v", st.upserted)
	}
	fin := st.lastFinish(t)
	if fin.status != store.SyncError || !strings.Contains(fin.errMsg, "HTTP 500") {
		t.Errorf("cierre = %+v, quiero status error con el detalle", fin)
	}
}

func TestRunOnBootYTicker(t *testing.T) {
	src := &fakeSource{snaps: []indicator.Snapshot{snap("uf", 40842.07)}}
	st := &fakeStore{changedPerUpsert: 1}
	r := New(src, st, 10*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()

	// On-boot + al menos un tick: la fuente debe llamarse ≥ 2 veces.
	deadline := time.After(2 * time.Second)
	for src.callCount() < 2 {
		select {
		case <-deadline:
			t.Fatalf("tras 2 s la fuente se llamó %d veces, quiero ≥ 2 (on-boot + tick)", src.callCount())
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run no terminó tras cancelar el contexto")
	}
}

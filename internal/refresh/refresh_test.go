// Tests unitarios del scheduler: fuente y store falsos, cero red y cero BD.
package refresh

import (
	"context"
	"errors"
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

type finishCall struct {
	id      int64
	status  store.SyncStatus
	updated int
	errMsg  string
}

type fakeStore struct {
	changedPerUpsert int
	upsertErr        error
	startErr         error

	mu       sync.Mutex
	upserted [][]indicator.Snapshot
	started  []string
	finished []finishCall
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
	f.finished = append(f.finished, finishCall{id: id, status: status, updated: updated, errMsg: errMsg})
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

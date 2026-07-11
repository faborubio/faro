// Tests de integración contra un Postgres real (el de scripts/dev-db.sh).
// Corren solo con FARO_TEST_DATABASE_URL apuntando a una BD de pruebas:
//
//	docker exec faro-pg createdb -U faro faro_test   # una vez
//	FARO_TEST_DATABASE_URL='postgres://faro:faro@localhost:5432/faro_test' go test ./internal/store/
//
// Cada test BORRA el esquema y aplica las migraciones embebidas desde cero; por eso el
// helper exige que el nombre de la BD contenga "test" — nunca la BD de dev.
// Sin la variable, se saltan (CI aún no tiene servicio Postgres).
package store_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/migrate"
	"github.com/faborubio/faro/internal/store"
	"github.com/faborubio/faro/internal/testdb"
	"github.com/faborubio/faro/migrations"
)

func testStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := testdb.Acquire(t)
	resetSchema(t, dsn)

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("abrir pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return store.New(pool), pool
}

// resetSchema deja la BD como recién migrada: tira el esquema public y aplica
// las migraciones embebidas por el mismo camino que usa cmd/faro al boot
// (internal/migrate, AUD-002). Conexión aparte en protocolo simple porque el
// DROP trae dos sentencias por Exec.
func resetSchema(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parsear DSN: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("conectar para migrar: %v (¿está arriba ./scripts/dev-db.sh y existe la BD?)", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("resetear esquema: %v", err)
	}

	if _, err := migrate.Apply(ctx, dsn, migrations.FS, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("aplicar migraciones embebidas: %v", err)
	}
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func TestUpsertAndLatest(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	changed, err := s.UpsertSnapshots(ctx, []indicator.Snapshot{
		{Code: "dolar", Value: 943.15, Date: date(2026, 7, 7)},
		{Code: "dolar", Value: 945.80, Date: date(2026, 7, 8)},
	})
	if err != nil {
		t.Fatalf("upsert inicial: %v", err)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %d snapshots, quiero 2", len(changed))
	}

	got, err := s.Latest(ctx, "dolar")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if got.Value != 945.80 || !got.Date.Equal(date(2026, 7, 8)) {
		t.Errorf("Latest = %+v, quiero valor 945.80 del 2026-07-08", got)
	}

	// Mismo valor de nuevo: 0 cambios — la señal "sin dato nuevo" del ADR-011.
	changed, err = s.UpsertSnapshots(ctx, []indicator.Snapshot{
		{Code: "dolar", Value: 945.80, Date: date(2026, 7, 8)},
	})
	if err != nil {
		t.Fatalf("upsert repetido: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("upsert idéntico: changed = %d snapshots, quiero 0", len(changed))
	}

	// Corrección del mismo día: 1 cambio y el histórico no crece.
	changed, err = s.UpsertSnapshots(ctx, []indicator.Snapshot{
		{Code: "dolar", Value: 946.00, Date: date(2026, 7, 8)},
	})
	if err != nil {
		t.Fatalf("upsert corrección: %v", err)
	}
	if len(changed) != 1 || changed[0].Value != 946.00 {
		t.Errorf("corrección: changed = %+v, quiero exactamente el snapshot corregido", changed)
	}
	got, err = s.Latest(ctx, "dolar")
	if err != nil {
		t.Fatalf("Latest tras corrección: %v", err)
	}
	if got.Value != 946.00 {
		t.Errorf("Latest.Value = %v, quiero 946.00", got.Value)
	}
	hist, err := s.History(ctx, "dolar", date(2026, 7, 1), date(2026, 7, 31))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Errorf("histórico con %d filas tras corrección, quiero 2 (la corrección no debe duplicar)", len(hist))
	}
}

func TestLatestNotFound(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	// Código inexistente y código del catálogo sin valores aún: ambos NotFound.
	for _, code := range []string{"noexiste", "uf"} {
		_, err := s.Latest(ctx, code)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("Latest(%q): err = %v, quiero ErrNotFound", code, err)
		}
	}
}

func TestHistoryRangeAndOrder(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	_, err := s.UpsertSnapshots(ctx, []indicator.Snapshot{
		{Code: "uf", Value: 40842.07, Date: date(2026, 7, 9)},
		{Code: "uf", Value: 40810.50, Date: date(2026, 7, 1)},
		{Code: "uf", Value: 40790.00, Date: date(2026, 6, 15)},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	hist, err := s.History(ctx, "uf", date(2026, 7, 1), date(2026, 7, 31))
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(hist) != 2 {
		t.Fatalf("len = %d, quiero 2 (junio queda fuera del rango)", len(hist))
	}
	if !hist[0].Date.Before(hist[1].Date) {
		t.Errorf("histórico no ascendente: %v luego %v", hist[0].Date, hist[1].Date)
	}
	if hist[1].Value != 40842.07 {
		t.Errorf("valor NUMERIC no sobrevivió el viaje: %v, quiero 40842.07", hist[1].Value)
	}

	empty, err := s.History(ctx, "uf", date(2020, 1, 1), date(2020, 12, 31))
	if err != nil {
		t.Fatalf("History rango vacío: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("rango sin datos: %d filas, quiero 0 sin error", len(empty))
	}
}

func TestUpsertUnknownCodeIsPartial(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	// El primero persiste, el segundo viola la FK del catálogo: contrato de
	// falla parcial — devuelve lo logrado más el error.
	changed, err := s.UpsertSnapshots(ctx, []indicator.Snapshot{
		{Code: "dolar", Value: 940.00, Date: date(2026, 7, 8)},
		{Code: "fantasma", Value: 1.0, Date: date(2026, 7, 8)},
	})
	if err == nil {
		t.Fatal("upsert con código fuera del catálogo: quiero error")
	}
	if len(changed) != 1 {
		t.Errorf("changed = %d snapshots, quiero 1 (lo persistido antes del fallo)", len(changed))
	}
	if _, err := s.Latest(ctx, "dolar"); err != nil {
		t.Errorf("el snapshot válido debió persistir: %v", err)
	}
}

func TestCatalog(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	all, err := s.ListIndicators(ctx)
	if err != nil {
		t.Fatalf("ListIndicators: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("catálogo con %d indicadores, quiero 4 (seed de la migración 001)", len(all))
	}

	uf, err := s.GetIndicator(ctx, "uf")
	if err != nil {
		t.Fatalf("GetIndicator(uf): %v", err)
	}
	if uf.Cadence != indicator.CadenceDaily || uf.Unit != "CLP" {
		t.Errorf("uf = %+v, quiero cadencia daily y unidad CLP", uf)
	}
	utm, err := s.GetIndicator(ctx, "utm")
	if err != nil {
		t.Fatalf("GetIndicator(utm): %v", err)
	}
	if utm.Cadence != indicator.CadenceMonthly {
		t.Errorf("utm.Cadence = %q, quiero monthly", utm.Cadence)
	}

	_, err = s.GetIndicator(ctx, "noexiste")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetIndicator(noexiste): err = %v, quiero ErrNotFound", err)
	}
}

func TestPreviousValue(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	_, err := s.UpsertSnapshots(ctx, []indicator.Snapshot{
		{Code: "dolar", Value: 943.15, Date: date(2026, 7, 7)},
		{Code: "dolar", Value: 945.80, Date: date(2026, 7, 8)},
		{Code: "dolar", Value: 950.10, Date: date(2026, 7, 9)},
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}

	prev, err := s.PreviousValue(ctx, "dolar", date(2026, 7, 9))
	if err != nil {
		t.Fatalf("PreviousValue: %v", err)
	}
	if prev.Value != 945.80 || !prev.Date.Equal(date(2026, 7, 8)) {
		t.Errorf("PreviousValue = %+v, quiero 945.80 del 2026-07-08", prev)
	}

	// Antes del primer valor no hay anterior: ErrNotFound (la semántica
	// "sin anterior" de las alertas).
	_, err = s.PreviousValue(ctx, "dolar", date(2026, 7, 7))
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("PreviousValue sin anterior: err = %v, quiero ErrNotFound", err)
	}
}

func TestAlertLifecycle(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()

	created, err := s.CreateAlert(ctx, "tok-abc", "dolar", store.OpGreater, 1000, "https://example.com/hook")
	if err != nil {
		t.Fatalf("CreateAlert: %v", err)
	}
	if created.ID == 0 || !created.Active || !created.LastTriggeredAt.IsZero() {
		t.Errorf("alerta creada = %+v, quiero activa, con id y sin disparos", created)
	}

	got, err := s.GetAlertByToken(ctx, "tok-abc")
	if err != nil {
		t.Fatalf("GetAlertByToken: %v", err)
	}
	if got.IndicatorCode != "dolar" || got.Operator != store.OpGreater || got.Threshold != 1000 {
		t.Errorf("GetAlertByToken = %+v, no coincide con lo creado", got)
	}

	if _, err := s.GetAlertByToken(ctx, "tok-noexiste"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("token desconocido: err = %v, quiero ErrNotFound", err)
	}

	// Solo las activas del indicador entran a la evaluación.
	if _, err := s.CreateAlert(ctx, "tok-uf", "uf", store.OpLess, 40000, "https://example.com/hook"); err != nil {
		t.Fatalf("CreateAlert uf: %v", err)
	}
	active, err := s.ListActiveAlertsByCode(ctx, "dolar")
	if err != nil {
		t.Fatalf("ListActiveAlertsByCode: %v", err)
	}
	if len(active) != 1 || active[0].Token != "tok-abc" {
		t.Errorf("activas de dolar = %+v, quiero solo tok-abc", active)
	}

	if err := s.MarkAlertTriggered(ctx, created.ID); err != nil {
		t.Fatalf("MarkAlertTriggered: %v", err)
	}
	got, err = s.GetAlertByToken(ctx, "tok-abc")
	if err != nil {
		t.Fatalf("GetAlertByToken tras disparo: %v", err)
	}
	if got.LastTriggeredAt.IsZero() {
		t.Error("last_triggered_at sigue en cero tras MarkAlertTriggered")
	}

	if err := s.DeleteAlertByToken(ctx, "tok-abc"); err != nil {
		t.Fatalf("DeleteAlertByToken: %v", err)
	}
	if _, err := s.GetAlertByToken(ctx, "tok-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("alerta borrada sigue existiendo: err = %v", err)
	}
	if err := s.DeleteAlertByToken(ctx, "tok-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("borrar dos veces: err = %v, quiero ErrNotFound", err)
	}
}

func TestSyncRunLifecycle(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	id, err := s.StartSyncRun(ctx, "cmf")
	if err != nil {
		t.Fatalf("StartSyncRun: %v", err)
	}
	if err := s.FinishSyncRun(ctx, id, store.SyncOK, 3, ""); err != nil {
		t.Fatalf("FinishSyncRun ok: %v", err)
	}

	var (
		status  string
		updated int
		errText *string
		done    *time.Time
	)
	row := pool.QueryRow(ctx, "SELECT status, indicators_updated, error, finished_at FROM sync_runs WHERE id = $1", id)
	if err := row.Scan(&status, &updated, &errText, &done); err != nil {
		t.Fatalf("leer sync_run: %v", err)
	}
	if status != "ok" || updated != 3 {
		t.Errorf("sync_run = (%s, %d), quiero (ok, 3)", status, updated)
	}
	if errText != nil {
		t.Errorf("error = %q, quiero NULL cuando errMsg está vacío", *errText)
	}
	if done == nil {
		t.Error("finished_at sigue NULL tras cerrar el run")
	}

	// Ciclo con error: el mensaje sí se guarda.
	id2, err := s.StartSyncRun(ctx, "cmf")
	if err != nil {
		t.Fatalf("StartSyncRun 2: %v", err)
	}
	if err := s.FinishSyncRun(ctx, id2, store.SyncError, 0, "la CMF respondió 500"); err != nil {
		t.Fatalf("FinishSyncRun error: %v", err)
	}
	row = pool.QueryRow(ctx, "SELECT status, error FROM sync_runs WHERE id = $1", id2)
	if err := row.Scan(&status, &errText); err != nil {
		t.Fatalf("leer sync_run 2: %v", err)
	}
	if status != "error" || errText == nil || *errText != "la CMF respondió 500" {
		t.Errorf("run fallido = (%s, %v), quiero (error, mensaje)", status, errText)
	}
}

func TestSweepOrphanSyncRuns(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()

	// Un huérfano viejo (2 h), un run vivo reciente y uno ya cerrado.
	var oldID, freshID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO sync_runs (source, status, started_at) VALUES ('cmf', 'running', now() - interval '2 hours') RETURNING id",
	).Scan(&oldID); err != nil {
		t.Fatalf("sembrar huérfano: %v", err)
	}
	var err error
	if freshID, err = s.StartSyncRun(ctx, "cmf"); err != nil {
		t.Fatalf("StartSyncRun: %v", err)
	}
	doneID, err := s.StartSyncRun(ctx, "cmf")
	if err != nil {
		t.Fatalf("StartSyncRun: %v", err)
	}
	if err := s.FinishSyncRun(ctx, doneID, store.SyncOK, 0, ""); err != nil {
		t.Fatalf("FinishSyncRun: %v", err)
	}

	n, err := s.SweepOrphanSyncRuns(ctx)
	if err != nil {
		t.Fatalf("SweepOrphanSyncRuns: %v", err)
	}
	if n != 1 {
		t.Fatalf("barridos = %d, quiero 1 (solo el viejo)", n)
	}

	var status string
	var finished *time.Time
	if err := pool.QueryRow(ctx, "SELECT status, finished_at FROM sync_runs WHERE id = $1", oldID).Scan(&status, &finished); err != nil {
		t.Fatalf("leer huérfano: %v", err)
	}
	if status != "error" || finished == nil {
		t.Errorf("huérfano = (%s, %v), quiero (error, finished_at asignado)", status, finished)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM sync_runs WHERE id = $1", freshID).Scan(&status); err != nil {
		t.Fatalf("leer run vivo: %v", err)
	}
	if status != "running" {
		t.Errorf("el run vivo reciente quedó %q: el umbral de 1 h no lo protegió", status)
	}
}

// Package refresh es el scheduler de Faro (SAD §4): refresca los indicadores
// on-boot y luego a intervalo fijo, persiste los snapshots y deja auditoría en
// sync_runs. La API jamás llama a la fuente (ADR-003): este paquete es el
// único camino entre la fuente y Postgres.
package refresh

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

// Store es lo que el scheduler necesita de la persistencia (interfaz del lado
// del consumidor: en tests se sustituye por un fake sin BD).
type Store interface {
	UpsertSnapshots(ctx context.Context, snaps []indicator.Snapshot) (int, error)
	StartSyncRun(ctx context.Context, source string) (int64, error)
	FinishSyncRun(ctx context.Context, id int64, status store.SyncStatus, updated int, errMsg string) error
}

// Refresher orquesta un ciclo fuente → store. Crear con New.
type Refresher struct {
	source   indicator.IndicatorSource
	store    Store
	interval time.Duration
	log      *slog.Logger
}

// New crea un Refresher. interval <= 0 usa 24 h (el default del SAD: la CMF
// publica ~1 valor/día; CASE-004 caracterizará la hora antes de afinar esto).
func New(source indicator.IndicatorSource, st Store, interval time.Duration, log *slog.Logger) *Refresher {
	if interval <= 0 {
		interval = 24 * time.Hour
	}
	if log == nil {
		log = slog.Default()
	}
	return &Refresher{source: source, store: st, interval: interval, log: log}
}

// Run refresca de inmediato (on-boot) y luego cada intervalo, hasta que el
// contexto se cancele. Los errores de un ciclo se loguean y quedan en
// sync_runs, pero no detienen el scheduler: el próximo tick reintenta.
func (r *Refresher) Run(ctx context.Context) {
	if err := r.RefreshOnce(ctx); err != nil {
		r.log.Error("refresco on-boot falló", "error", err)
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.RefreshOnce(ctx); err != nil {
				r.log.Error("refresco falló", "error", err)
			}
		}
	}
}

// RefreshOnce ejecuta un ciclo completo: abre sync_run, trae de la fuente,
// persiste y cierra con el resultado. Ante falla parcial de la fuente persiste
// lo obtenido igual (SAD §8) y el run queda en 'error' con el detalle.
//
// 0 valores nuevos con fuente sana NO es error: para cadencias mensuales (y
// fines de semana de las diarias) es lo esperado — ADR-011. El run cierra 'ok'
// con indicators_updated = 0.
func (r *Refresher) RefreshOnce(ctx context.Context) error {
	id, err := r.store.StartSyncRun(ctx, r.source.Name())
	if err != nil {
		// Sin BD no hay dónde persistir: no tiene sentido llamar a la fuente.
		return err
	}

	snaps, fetchErr := r.source.Fetch(ctx)
	changed, upsertErr := r.store.UpsertSnapshots(ctx, snaps)

	status := store.SyncOK
	runErr := errors.Join(fetchErr, upsertErr)
	msg := ""
	if runErr != nil {
		status = store.SyncError
		msg = runErr.Error()
	}
	if err := r.store.FinishSyncRun(ctx, id, status, changed, msg); err != nil {
		return errors.Join(runErr, err)
	}

	if runErr != nil {
		r.log.Warn("refresco con errores", "sync_run", id, "snapshots", len(snaps), "actualizados", changed, "error", runErr)
	} else {
		r.log.Info("refresco ok", "sync_run", id, "snapshots", len(snaps), "actualizados", changed)
	}
	return runErr
}

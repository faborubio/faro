// Package refresh es el scheduler de Faro (SAD §4): refresca los indicadores
// on-boot y luego a intervalo fijo, persiste los snapshots y deja auditoría en
// sync_runs. La API jamás llama a la fuente (ADR-003): este paquete es el
// único camino entre la fuente y Postgres.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

// Store es lo que el scheduler necesita de la persistencia (interfaz del lado
// del consumidor: en tests se sustituye por un fake sin BD).
type Store interface {
	UpsertSnapshots(ctx context.Context, snaps []indicator.Snapshot) ([]indicator.Snapshot, error)
	StartSyncRun(ctx context.Context, source string) (int64, error)
	FinishSyncRun(ctx context.Context, id int64, status store.SyncStatus, updated int, errMsg string) error
	ListIndicators(ctx context.Context) ([]store.Indicator, error)
	Latest(ctx context.Context, code string) (indicator.Snapshot, error)
	SweepOrphanSyncRuns(ctx context.Context) (int, error)
}

// Notifier recibe los valores que realmente cambiaron en un ciclo — el gancho
// de las alertas (ADR-006). Se llama tras cerrar el sync_run: la evaluación de
// alertas jamás ensucia el registro de salud de la fuente.
type Notifier interface {
	ValuesChanged(ctx context.Context, changed []indicator.Snapshot)
}

// Refresher orquesta un ciclo fuente → store. Crear con New.
type Refresher struct {
	source   indicator.IndicatorSource
	store    Store
	interval time.Duration
	log      *slog.Logger
	notifier Notifier // opcional; nil = sin alertas
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

// WithNotifier registra el receptor de valores cambiados (las alertas).
// Devuelve el mismo Refresher para encadenar en cmd/faro.
func (r *Refresher) WithNotifier(n Notifier) *Refresher {
	r.notifier = n
	return r
}

// Run backfillea los indicadores vacíos, refresca de inmediato (on-boot) y
// luego cada intervalo, hasta que el contexto se cancele. Los errores de un
// ciclo se loguean y quedan en sync_runs, pero no detienen el scheduler: el
// próximo tick reintenta.
func (r *Refresher) Run(ctx context.Context) {
	// Barrido de huérfanos (AUD-004): runs en 'running' de instancias que
	// murieron sin cerrar. Solo ruido cosmético en el tablero — si falla, se
	// sigue igual.
	if n, err := r.store.SweepOrphanSyncRuns(ctx); err != nil {
		r.log.Warn("barrido de sync_runs huérfanos falló", "error", err)
	} else if n > 0 {
		r.log.Info("sync_runs huérfanos barridos", "cerrados", n)
	}
	if err := r.Backfill(ctx); err != nil {
		r.log.Error("backfill on-boot falló", "error", err)
	}
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

// Backfill puebla el histórico de los indicadores del catálogo que aún no
// tienen ningún valor — el estado de un deploy recién nacido, donde los
// gráficos del dashboard estarían vacíos. Trae de la fuente el año actual y
// el anterior (si la fuente sabe de histórico) y descarta fechas futuras: la
// UF llega publicada ~1 mes adelante y Latest la reportaría como "vigente"
// (CASE-006). Con el catálogo ya poblado no llama a la fuente ni abre
// sync_run: en boots normales es un no-op.
func (r *Refresher) Backfill(ctx context.Context) error {
	hist, ok := r.source.(indicator.HistoricalSource)
	if !ok {
		return nil
	}
	inds, err := r.store.ListIndicators(ctx)
	if err != nil {
		return err
	}
	var pending []string
	for _, ind := range inds {
		_, err := r.store.Latest(ctx, ind.Code)
		switch {
		case errors.Is(err, store.ErrNotFound):
			pending = append(pending, ind.Code)
		case err != nil:
			return err
		}
	}
	if len(pending) == 0 {
		return nil
	}

	id, err := r.store.StartSyncRun(ctx, r.source.Name()+"/backfill")
	if err != nil {
		return err
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var snaps []indicator.Snapshot
	var errs []error
	for _, code := range pending {
		for _, year := range []int{today.Year() - 1, today.Year()} {
			s, err := hist.FetchYear(ctx, code, year)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s/%d: %w", code, year, err))
				continue
			}
			for _, sn := range s {
				if sn.Date.After(today) {
					continue
				}
				snaps = append(snaps, sn)
			}
		}
	}
	changed, upsertErr := r.store.UpsertSnapshots(ctx, snaps)
	return r.finishRun(ctx, "backfill", id, len(snaps), changed, errors.Join(errors.Join(errs...), upsertErr))
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
	return r.finishRun(ctx, "refresco", id, len(snaps), changed, errors.Join(fetchErr, upsertErr))
}

// finishRun cierra un sync_run con el resultado del ciclo (refresco o
// backfill) y lo loguea. Cierra aunque el contexto muera a mitad del ciclo
// (SIGTERM en pleno refresco): si no, queda huérfano en 'running' para
// siempre. Con el run ya cerrado, entrega los valores cambiados al notifier
// (alertas, ADR-006): lo que pase con los webhooks no toca la salud de la
// fuente.
func (r *Refresher) finishRun(ctx context.Context, what string, id int64, snapCount int, changed []indicator.Snapshot, runErr error) error {
	status := store.SyncOK
	msg := ""
	if runErr != nil {
		status = store.SyncError
		msg = runErr.Error()
	}
	if err := r.store.FinishSyncRun(context.WithoutCancel(ctx), id, status, len(changed), msg); err != nil {
		return errors.Join(runErr, err)
	}

	if runErr != nil {
		r.log.Warn(what+" con errores", "sync_run", id, "snapshots", snapCount, "actualizados", len(changed), "error", runErr)
	} else {
		r.log.Info(what+" ok", "sync_run", id, "snapshots", snapCount, "actualizados", len(changed))
	}
	if r.notifier != nil && len(changed) > 0 {
		// Sin la cancelación del ctx: un SIGTERM a mitad de ciclo no debe
		// abortar el POST de un cruce real — no hay reintento (el próximo
		// tick trae 0 cambios) y el webhook tiene su propio timeout de 10 s.
		r.notifier.ValuesChanged(context.WithoutCancel(ctx), changed)
	}
	return runErr
}

// Package store persiste y sirve el histórico de indicadores en Postgres
// (ADR-004). Expone tipos del dominio (indicator.Snapshot) y esconde el código
// generado por sqlc (internal/store/db). Nadie más toca la base directamente.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store/db"
)

// ErrNotFound señala que el indicador no existe en el catálogo o aún no tiene
// valores. La API lo traduce a 404.
var ErrNotFound = errors.New("store: no encontrado")

// SyncStatus es el estado final de un sync_run (el catálogo vive en el CHECK
// de la tabla; 'running' lo pone StartSyncRun y nunca es un estado final).
type SyncStatus string

const (
	SyncOK    SyncStatus = "ok"
	SyncError SyncStatus = "error"
)

// Indicator es una entrada del catálogo (tabla indicators, sembrada por
// migración). El dominio solo modela Snapshot y Cadence; los metadatos de
// presentación (nombre, unidad) pertenecen al storage y a la API.
type Indicator struct {
	Code        string
	Name        string
	Unit        string
	Description string
	Cadence     indicator.Cadence
}

// Store es el único punto de acceso a Postgres.
type Store struct {
	q *db.Queries
}

// New crea un Store sobre un pool de pgx. El pool lo abre y cierra cmd/faro
// (config por ENV, ADR-009).
func New(pool *pgxpool.Pool) *Store {
	return &Store{q: db.New(pool)}
}

// UpsertSnapshots persiste snapshots por (código, fecha) y devuelve cuántos
// insertaron o corrigieron un valor — "mismo valor que ayer" cuenta 0, que es
// la señal que el scheduler interpreta según cadencia (ADR-011). Si un upsert
// falla (p. ej. código fuera del catálogo), devuelve lo persistido hasta ahí
// más el error, espejo del contrato de fallas parciales del adapter.
func (s *Store) UpsertSnapshots(ctx context.Context, snaps []indicator.Snapshot) (int, error) {
	changed := 0
	for _, snap := range snaps {
		rows, err := s.q.UpsertValue(ctx, db.UpsertValueParams{
			IndicatorCode: snap.Code,
			Date:          snap.Date,
			Value:         snap.Value,
		})
		if err != nil {
			return changed, fmt.Errorf("store: upsert de %q (%s): %w", snap.Code, snap.Date.Format("2006-01-02"), err)
		}
		changed += int(rows)
	}
	return changed, nil
}

// Latest devuelve el último valor conocido de un indicador.
func (s *Store) Latest(ctx context.Context, code string) (indicator.Snapshot, error) {
	row, err := s.q.LatestValue(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return indicator.Snapshot{}, fmt.Errorf("último valor de %q: %w", code, ErrNotFound)
	}
	if err != nil {
		return indicator.Snapshot{}, fmt.Errorf("store: último valor de %q: %w", code, err)
	}
	return indicator.Snapshot{Code: row.IndicatorCode, Value: row.Value, Date: row.Date}, nil
}

// History devuelve los valores de un indicador en [from, to], ascendente por
// fecha. Sin valores en el rango devuelve slice vacío, no error: un rango sin
// datos es una respuesta válida (la API lo sirve como lista vacía).
func (s *Store) History(ctx context.Context, code string, from, to time.Time) ([]indicator.Snapshot, error) {
	rows, err := s.q.HistoryByRange(ctx, db.HistoryByRangeParams{
		IndicatorCode: code,
		FromDate:      from,
		ToDate:        to,
	})
	if err != nil {
		return nil, fmt.Errorf("store: histórico de %q: %w", code, err)
	}
	snaps := make([]indicator.Snapshot, len(rows))
	for i, row := range rows {
		snaps[i] = indicator.Snapshot{Code: row.IndicatorCode, Value: row.Value, Date: row.Date}
	}
	return snaps, nil
}

// GetIndicator devuelve la entrada del catálogo para un código.
func (s *Store) GetIndicator(ctx context.Context, code string) (Indicator, error) {
	row, err := s.q.GetIndicator(ctx, code)
	if errors.Is(err, pgx.ErrNoRows) {
		return Indicator{}, fmt.Errorf("indicador %q: %w", code, ErrNotFound)
	}
	if err != nil {
		return Indicator{}, fmt.Errorf("store: indicador %q: %w", code, err)
	}
	return indicatorFromRow(row.Code, row.Name, row.Unit, row.Description, row.Cadence), nil
}

// ListIndicators devuelve el catálogo completo, ordenado por código.
func (s *Store) ListIndicators(ctx context.Context) ([]Indicator, error) {
	rows, err := s.q.ListIndicators(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: catálogo: %w", err)
	}
	out := make([]Indicator, len(rows))
	for i, row := range rows {
		out[i] = indicatorFromRow(row.Code, row.Name, row.Unit, row.Description, row.Cadence)
	}
	return out, nil
}

func indicatorFromRow(code, name, unit, description, cadence string) Indicator {
	return Indicator{
		Code:        code,
		Name:        name,
		Unit:        unit,
		Description: description,
		Cadence:     indicator.Cadence(cadence),
	}
}

// SweepOrphanSyncRuns cierra como 'error' los sync_runs que quedaron en
// 'running' hace más de una hora: la instancia que los abrió murió sin
// cerrarlos (crash duro — AUD-004). Devuelve cuántos barrió.
func (s *Store) SweepOrphanSyncRuns(ctx context.Context) (int, error) {
	n, err := s.q.SweepOrphanSyncRuns(ctx)
	if err != nil {
		return 0, fmt.Errorf("store: barrer sync_runs huérfanos: %w", err)
	}
	return int(n), nil
}

// StartSyncRun abre un sync_run en estado 'running' y devuelve su id.
func (s *Store) StartSyncRun(ctx context.Context, source string) (int64, error) {
	id, err := s.q.StartSyncRun(ctx, source)
	if err != nil {
		return 0, fmt.Errorf("store: abrir sync_run de %q: %w", source, err)
	}
	return id, nil
}

// FinishSyncRun cierra un sync_run con su estado final. errMsg vacío se guarda
// como NULL: la columna error solo tiene contenido cuando hubo error.
func (s *Store) FinishSyncRun(ctx context.Context, id int64, status SyncStatus, updated int, errMsg string) error {
	var errText *string
	if errMsg != "" {
		errText = &errMsg
	}
	err := s.q.FinishSyncRun(ctx, db.FinishSyncRunParams{
		ID:                id,
		Status:            string(status),
		IndicatorsUpdated: int32(updated),
		Error:             errText,
	})
	if err != nil {
		return fmt.Errorf("store: cerrar sync_run %d: %w", id, err)
	}
	return nil
}

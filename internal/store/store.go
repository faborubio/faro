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

// Operator es la condición de una alerta (el catálogo vive en el CHECK de la
// tabla alerts): gt = "avísame si supera el umbral", lt = "si cae bajo él".
type Operator string

const (
	OpGreater Operator = "gt"
	OpLess    Operator = "lt"
)

// Alert es una alerta por webhook (ADR-006), registrada por token opaco.
// LastTriggeredAt en cero significa "nunca disparó".
type Alert struct {
	ID              int64
	Token           string
	IndicatorCode   string
	Operator        Operator
	Threshold       float64
	WebhookURL      string
	Active          bool
	LastTriggeredAt time.Time
	CreatedAt       time.Time
}

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

// UpsertSnapshots persiste snapshots por (código, fecha) y devuelve los que
// insertaron o corrigieron un valor — "mismo valor que ayer" no cuenta, que es
// la señal que el scheduler interpreta según cadencia (ADR-011) y la que
// gatilla la evaluación de alertas (ADR-006: solo valores nuevos se evalúan).
// Si un upsert falla (p. ej. código fuera del catálogo), devuelve lo
// persistido hasta ahí más el error, espejo del contrato de fallas parciales
// del adapter.
func (s *Store) UpsertSnapshots(ctx context.Context, snaps []indicator.Snapshot) ([]indicator.Snapshot, error) {
	var changed []indicator.Snapshot
	for _, snap := range snaps {
		rows, err := s.q.UpsertValue(ctx, db.UpsertValueParams{
			IndicatorCode: snap.Code,
			Date:          snap.Date,
			Value:         snap.Value,
		})
		if err != nil {
			return changed, fmt.Errorf("store: upsert de %q (%s): %w", snap.Code, snap.Date.Format("2006-01-02"), err)
		}
		if rows > 0 {
			changed = append(changed, snap)
		}
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

// PreviousValue devuelve el valor inmediatamente anterior a una fecha — la
// otra mitad de la semántica de cruce de las alertas (ADR-006). Sin valor
// anterior devuelve ErrNotFound.
func (s *Store) PreviousValue(ctx context.Context, code string, before time.Time) (indicator.Snapshot, error) {
	row, err := s.q.PreviousValue(ctx, db.PreviousValueParams{IndicatorCode: code, Date: before})
	if errors.Is(err, pgx.ErrNoRows) {
		return indicator.Snapshot{}, fmt.Errorf("valor anterior de %q: %w", code, ErrNotFound)
	}
	if err != nil {
		return indicator.Snapshot{}, fmt.Errorf("store: valor anterior de %q: %w", code, err)
	}
	return indicator.Snapshot{Code: row.IndicatorCode, Value: row.Value, Date: row.Date}, nil
}

// CreateAlert registra una alerta. El token lo genera el llamador
// (crypto/rand) y la webhook_url llega ya validada (anti-SSRF, SAD §8).
func (s *Store) CreateAlert(ctx context.Context, token, code string, op Operator, threshold float64, webhookURL string) (Alert, error) {
	row, err := s.q.CreateAlert(ctx, db.CreateAlertParams{
		Token:         token,
		IndicatorCode: code,
		Operator:      string(op),
		Threshold:     threshold,
		WebhookUrl:    webhookURL,
	})
	if err != nil {
		return Alert{}, fmt.Errorf("store: crear alerta de %q: %w", code, err)
	}
	return alertFromRow(row), nil
}

// GetAlertByToken devuelve la alerta del token, o ErrNotFound.
func (s *Store) GetAlertByToken(ctx context.Context, token string) (Alert, error) {
	row, err := s.q.GetAlertByToken(ctx, token)
	if errors.Is(err, pgx.ErrNoRows) {
		return Alert{}, fmt.Errorf("alerta: %w", ErrNotFound)
	}
	if err != nil {
		return Alert{}, fmt.Errorf("store: alerta por token: %w", err)
	}
	return alertFromRow(row), nil
}

// DeleteAlertByToken borra la alerta del token (el contrato del DELETE de la
// API: la fila desaparece). Token desconocido devuelve ErrNotFound.
func (s *Store) DeleteAlertByToken(ctx context.Context, token string) error {
	rows, err := s.q.DeleteAlertByToken(ctx, token)
	if err != nil {
		return fmt.Errorf("store: borrar alerta: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("alerta: %w", ErrNotFound)
	}
	return nil
}

// ListActiveAlertsByCode devuelve las alertas activas de un indicador (las
// que el evaluador considera tras cada refresco).
func (s *Store) ListActiveAlertsByCode(ctx context.Context, code string) ([]Alert, error) {
	rows, err := s.q.ListActiveAlertsByCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("store: alertas activas de %q: %w", code, err)
	}
	out := make([]Alert, len(rows))
	for i, row := range rows {
		out[i] = alertFromRow(row)
	}
	return out, nil
}

// MarkAlertTriggered registra el disparo de una alerta (auditoría; la
// semántica de no re-disparo la da el cruce, no este timestamp).
func (s *Store) MarkAlertTriggered(ctx context.Context, id int64) error {
	if err := s.q.MarkAlertTriggered(ctx, id); err != nil {
		return fmt.Errorf("store: marcar disparo de alerta %d: %w", id, err)
	}
	return nil
}

func alertFromRow(row db.Alert) Alert {
	a := Alert{
		ID:            row.ID,
		Token:         row.Token,
		IndicatorCode: row.IndicatorCode,
		Operator:      Operator(row.Operator),
		Threshold:     row.Threshold,
		WebhookURL:    row.WebhookUrl,
		Active:        row.Active,
	}
	if row.LastTriggeredAt.Valid {
		a.LastTriggeredAt = row.LastTriggeredAt.Time
	}
	if row.CreatedAt.Valid {
		a.CreatedAt = row.CreatedAt.Time
	}
	return a
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

// Package alert evalúa las alertas por webhook (ADR-006) tras cada refresco.
// Semántica de disparo = CRUCE: una alerta dispara cuando el valor nuevo
// satisface su condición y el anterior no — "avísame si el dólar cruza $1.000"
// no re-notifica cada día que siga arriba. Solo se evalúan indicadores que
// trajeron valor nuevo en el ciclo (la señal del upsert, ADR-011); un ciclo
// sin cambios no evalúa nada.
package alert

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

// Store es lo que el evaluador necesita de la persistencia (fake en tests).
type Store interface {
	ListActiveAlertsByCode(ctx context.Context, code string) ([]store.Alert, error)
	PreviousValue(ctx context.Context, code string, before time.Time) (indicator.Snapshot, error)
	Latest(ctx context.Context, code string) (indicator.Snapshot, error)
	GetIndicator(ctx context.Context, code string) (store.Indicator, error)
	MarkAlertTriggered(ctx context.Context, id int64) error
}

// Poster despacha un payload a una webhook_url (implementado por
// internal/webhook con el anti-SSRF del SAD §8).
type Poster interface {
	Post(ctx context.Context, url string, payload any) error
}

// Payload es el JSON que recibe el receptor del webhook.
type Payload struct {
	Indicator string  `json:"indicator"`
	Name      string  `json:"name"`
	Unit      string  `json:"unit"`
	Operator  string  `json:"operator"` // gt | lt
	Threshold float64 `json:"threshold"`
	Value     float64 `json:"value"`
	Date      string  `json:"date"` // fecha del valor que cruzó (YYYY-MM-DD)
	Message   string  `json:"message"`
}

// Service evalúa y dispara alertas. Crear con New. Implementa
// refresh.Notifier: el scheduler lo llama con los valores que cambiaron,
// después de cerrar el sync_run.
type Service struct {
	store  Store
	poster Poster
	log    *slog.Logger
}

// New crea el evaluador de alertas.
func New(st Store, p Poster, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{store: st, poster: p, log: log}
}

// ValuesChanged evalúa las alertas de los indicadores con valor nuevo. Los
// errores se loguean y no se propagan: las alertas jamás afectan al ciclo de
// refresco ni a la salud de la fuente. Con varios valores nuevos del mismo
// código (backfill), solo el más reciente cuenta — el histórico no es noticia.
func (s *Service) ValuesChanged(ctx context.Context, changed []indicator.Snapshot) {
	for _, snap := range latestPerCode(changed) {
		if err := s.evaluateCode(ctx, snap); err != nil {
			s.log.Error("evaluando alertas", "code", snap.Code, "error", err)
		}
	}
}

func (s *Service) evaluateCode(ctx context.Context, snap indicator.Snapshot) error {
	alerts, err := s.store.ListActiveAlertsByCode(ctx, snap.Code)
	if err != nil {
		return err
	}
	if len(alerts) == 0 {
		return nil
	}

	// Una corrección histórica (la fuente re-emite un valor VIEJO mientras ya
	// existe uno más nuevo) no es un cruce del valor vigente: alertar con el
	// dato de ayer cuando hoy ya se conoce sería mentir. Solo se evalúa el
	// snapshot que ES el último del indicador.
	latest, err := s.store.Latest(ctx, snap.Code)
	if err != nil {
		return err
	}
	if latest.Date.After(snap.Date) {
		return nil
	}

	prev, err := s.store.PreviousValue(ctx, snap.Code, snap.Date)
	hasPrev := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}

	var ind store.Indicator
	var indLoaded bool
	for _, a := range alerts {
		// Cruce: el valor nuevo satisface y el anterior no. Sin valor
		// anterior (primer dato del indicador) se dispara si satisface: el
		// usuario pidió saber y no hay historia que diga "ya estaba ahí".
		// El umbral 0 es un valor como cualquiera (CASE-005).
		if !satisfies(a.Operator, snap.Value, a.Threshold) {
			continue
		}
		if hasPrev && satisfies(a.Operator, prev.Value, a.Threshold) {
			continue // ya estaba cruzado: no es noticia
		}
		if !indLoaded {
			if ind, err = s.store.GetIndicator(ctx, snap.Code); err != nil {
				return err
			}
			indLoaded = true
		}
		s.fire(ctx, a, ind, snap)
	}
	return nil
}

// fire despacha el webhook de una alerta cruzada. last_triggered_at se marca
// solo si el POST entregó (2xx): es auditoría de entregas, no de intentos.
// Sin reintentos en v1: el próximo cruce real vuelve a disparar.
func (s *Service) fire(ctx context.Context, a store.Alert, ind store.Indicator, snap indicator.Snapshot) {
	dir := "superó"
	if a.Operator == store.OpLess {
		dir = "cayó bajo"
	}
	date := snap.Date.Format("2006-01-02")
	p := Payload{
		Indicator: snap.Code,
		Name:      ind.Name,
		Unit:      ind.Unit,
		Operator:  string(a.Operator),
		Threshold: a.Threshold,
		Value:     snap.Value,
		Date:      date,
		Message:   fmt.Sprintf("%s %s el umbral %v: valor %v (%s)", ind.Name, dir, a.Threshold, snap.Value, date),
	}
	if err := s.poster.Post(ctx, a.WebhookURL, p); err != nil {
		s.log.Warn("webhook no entregado", "alerta", a.ID, "code", snap.Code, "error", err)
		return
	}
	if err := s.store.MarkAlertTriggered(ctx, a.ID); err != nil {
		s.log.Error("marcando disparo", "alerta", a.ID, "error", err)
	}
	s.log.Info("alerta disparada", "alerta", a.ID, "code", snap.Code, "valor", snap.Value, "umbral", a.Threshold)
}

func satisfies(op store.Operator, value, threshold float64) bool {
	switch op {
	case store.OpGreater:
		return value > threshold
	case store.OpLess:
		return value < threshold
	default:
		return false // operador desconocido jamás dispara (el CHECK de la tabla lo impide)
	}
}

// latestPerCode reduce el lote a un snapshot por código: el de fecha mayor.
func latestPerCode(snaps []indicator.Snapshot) []indicator.Snapshot {
	byCode := make(map[string]indicator.Snapshot)
	for _, s := range snaps {
		if cur, ok := byCode[s.Code]; !ok || s.Date.After(cur.Date) {
			byCode[s.Code] = s
		}
	}
	out := make([]indicator.Snapshot, 0, len(byCode))
	for _, s := range byCode {
		out = append(out, s)
	}
	return out
}

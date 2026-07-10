// Package indicator define el dominio de Faro: indicadores económicos de Chile,
// sus valores normalizados (Snapshot) y el contrato con las fuentes de datos
// (IndicatorSource). El dominio no sabe de la CMF ni de ninguna fuente concreta
// (ADR-002): consume snapshots ya normalizados.
package indicator

import (
	"context"
	"time"
)

// Cadence es la cadencia de publicación de un indicador (ADR-011). El scheduler
// sondea a diario, pero interpreta el resultado según la cadencia: para un
// indicador mensual, "hoy no hubo valor nuevo" es lo esperado, no un fallo.
type Cadence string

const (
	// CadenceDaily publica en días hábiles (dólar, euro).
	CadenceDaily Cadence = "daily"
	// CadenceMonthly publica una vez al mes, ~día 9 (UF, UTM, IPC — CASE-001).
	CadenceMonthly Cadence = "monthly"
)

// Valid informa si la cadencia es una de las conocidas.
func (c Cadence) Valid() bool {
	return c == CadenceDaily || c == CadenceMonthly
}

// Snapshot es el valor de un indicador en una fecha, ya normalizado por el
// adapter de la fuente (formato chileno "40.842,07" → 40842.07, CASE-003).
// La persistencia guarda el valor como NUMERIC; float64 basta aquí como
// transporte y para comparar umbrales de alertas.
type Snapshot struct {
	Code  string    // código del indicador: "uf", "dolar", "utm", "ipc"…
	Value float64   // valor normalizado
	Date  time.Time // fecha de publicación del valor (no de la consulta)
}

// IndicatorSource aísla la fuente de datos del dominio (ADR-002). La
// implementación v1 es la API oficial de la CMF; mindicador.cl queda como
// fallback. En tests se sustituye con httptest: cero red real en CI.
type IndicatorSource interface {
	// Fetch trae los valores vigentes de los indicadores del catálogo.
	Fetch(ctx context.Context) ([]Snapshot, error)
	// Name identifica la fuente en sync_runs ("cmf", "mindicador").
	Name() string
}

// HistoricalSource es la capacidad opcional de una fuente de entregar la serie
// completa de un año (backfill del dashboard, CASE-006). El scheduler la
// detecta por type assertion: una fuente sin histórico sigue siendo válida.
type HistoricalSource interface {
	// FetchYear trae todos los valores publicados de un indicador en un año.
	FetchYear(ctx context.Context, code string, year int) ([]Snapshot, error)
}

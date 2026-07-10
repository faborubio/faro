-- Queries de Faro (SAD §6). sqlc las compila a Go type-safe (ADR-004).
-- Regenerar con: sqlc generate

-- name: UpsertValue :execrows
-- Upsert por (código, fecha): nunca pisa el histórico, solo corrige el valor
-- del mismo día. El WHERE hace que "mismo valor" afecte 0 filas — así el
-- scheduler distingue "hubo dato nuevo" de "sin cambios" (ADR-011) sin query
-- extra. fetched_at solo se mueve cuando el valor realmente cambió.
INSERT INTO indicator_values (indicator_code, date, value)
VALUES (@indicator_code, @date, @value)
ON CONFLICT (indicator_code, date) DO UPDATE
    SET value = EXCLUDED.value, fetched_at = now()
    WHERE indicator_values.value IS DISTINCT FROM EXCLUDED.value;

-- name: LatestValue :one
-- La PK (code, date) resuelve esto con un backward scan: sin índice extra.
SELECT indicator_code, date, value
FROM indicator_values
WHERE indicator_code = @indicator_code
ORDER BY date DESC
LIMIT 1;

-- name: HistoryByRange :many
SELECT indicator_code, date, value
FROM indicator_values
WHERE indicator_code = @indicator_code
  AND date >= @from_date
  AND date <= @to_date
ORDER BY date;

-- name: GetIndicator :one
SELECT code, name, unit, description, cadence
FROM indicators
WHERE code = @code;

-- name: ListIndicators :many
SELECT code, name, unit, description, cadence
FROM indicators
ORDER BY code;

-- name: StartSyncRun :one
INSERT INTO sync_runs (source, status)
VALUES (@source, 'running')
RETURNING id;

-- name: FinishSyncRun :exec
UPDATE sync_runs
SET finished_at        = now(),
    status             = @status,
    indicators_updated = @indicators_updated,
    error              = @error
WHERE id = @id;

-- name: SweepOrphanSyncRuns :execrows
-- Barrido de huérfanos (AUD-004): un crash duro (OOM, kill -9) deja runs en
-- 'running' para siempre. El umbral de 1 hora protege a una instancia vieja
-- legítimamente a mitad de ciclo durante un rolling update (el ciclo más
-- largo observado son ~8 min con egress roto, T-004).
UPDATE sync_runs
SET finished_at = now(),
    status      = 'error',
    error       = 'huérfano: la instancia murió sin cerrar el run (barrido al boot, AUD-004)'
WHERE status = 'running'
  AND started_at < now() - interval '1 hour';

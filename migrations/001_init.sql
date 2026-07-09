-- 001 — Esquema inicial de Faro (SAD §6).

-- Catálogo de indicadores. `cadence` es la clave del ADR-011: el scheduler
-- sondea a diario pero interpreta según cadencia — para un indicador mensual,
-- "sin valor nuevo hoy" es lo esperado, no un fallo.
CREATE TABLE indicators (
    code        text PRIMARY KEY,
    name        text NOT NULL,
    unit        text NOT NULL,
    description text NOT NULL DEFAULT '',
    cadence     text NOT NULL CHECK (cadence IN ('daily', 'monthly'))
);

-- Histórico append-only (ADR-004): cada refresco agrega el valor del día con
-- upsert por (código, fecha); nunca pisa el histórico. El valor es NUMERIC,
-- no float — la CMF entrega montos con decimales chilenos (CASE-003).
-- La PK (code, date) sirve también al patrón "último valor / histórico
-- descendente" vía backward scan: no hace falta un índice extra.
CREATE TABLE indicator_values (
    indicator_code text NOT NULL REFERENCES indicators (code),
    date           date NOT NULL,
    value          numeric NOT NULL,
    fetched_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (indicator_code, date)
);

-- Alertas por webhook (ADR-006): registradas por token opaco, evaluadas tras
-- cada refresco. La webhook_url se valida en la app (anti-SSRF, SAD §8).
CREATE TABLE alerts (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token             text NOT NULL,
    indicator_code    text NOT NULL REFERENCES indicators (code),
    operator          text NOT NULL CHECK (operator IN ('gt', 'lt')),
    threshold         numeric NOT NULL,
    webhook_url       text NOT NULL,
    active            boolean NOT NULL DEFAULT true,
    last_triggered_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- Solo se evalúan las activas de cada indicador: índice parcial.
CREATE INDEX alerts_active_by_indicator ON alerts (indicator_code) WHERE active;

-- Auditoría de refrescos (espejo de Oteo): el tablero de salud de la fuente.
CREATE TABLE sync_runs (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source             text NOT NULL,
    started_at         timestamptz NOT NULL DEFAULT now(),
    finished_at        timestamptz,
    status             text NOT NULL CHECK (status IN ('running', 'ok', 'error')),
    indicators_updated integer NOT NULL DEFAULT 0,
    error              text
);

-- Catálogo v1 (SAD §1.2). Cadencias según la CMF (CASE-001): dólar publica en
-- días hábiles; UTM/IPC un valor por mes (~día 9). La UF tiene valor diario
-- aunque su serie se fija mensualmente → cadence 'daily' para frescura.
INSERT INTO indicators (code, name, unit, cadence, description) VALUES
    ('uf',    'Unidad de Fomento',               'CLP', 'daily',   'Unidad reajustable por IPC; valor diario (serie fijada mensualmente)'),
    ('dolar', 'Dólar observado',                 'CLP', 'daily',   'Tipo de cambio USD/CLP, días hábiles'),
    ('utm',   'Unidad Tributaria Mensual',       'CLP', 'monthly', 'Unidad tributaria; un valor por mes'),
    ('ipc',   'Índice de Precios al Consumidor', '%',   'monthly', 'Variación mensual del IPC');

# CLAUDE.md — Contexto operativo de Faro

> Lo primero que lee una sesión nueva (humana o IA). Objetivo: retomar el proyecto sin releer
> el historial. Se actualiza al cerrar cada sesión que cambió el estado.

## Qué es Faro
**API pública + dashboard de indicadores económicos de Chile** (dólar, UF, UTM, IPC…), en Go.
Refresca 1×/día desde una fuente tras adapter, guarda histórico en Postgres, y ofrece API rápida,
dashboard con gráficos, alertas por webhook y widgets embebibles. Corre liviano en el free tier de
**VibeNest** (256 MB). Pieza de portafolio + servicio real.

El diseño completo, las decisiones y sus trade-offs viven en **[SAD-Faro.md](SAD-Faro.md)** — es la
fuente de verdad. Este archivo solo registra cómo operar el repo. Metáfora hermana de **Oteo** (repo
del mismo autor): allá se otea el territorio; aquí Faro enciende una luz de referencia sobre la economía.

Faro sigue **El Método** (`~/Workspace/metodo/MANIFIESTO.md`): proporcionalidad, docs como sistema,
fases con Definition of Done. El SAD es la instancia proporcional de ese método para este proyecto.

## Stack
Go (stdlib `net/http`, sin framework pesado) · PostgreSQL · `sqlc` + `pgx` para queries type-safe ·
Chart.js embebido con `go:embed` · Docker (imagen mínima) · Deploy en VibeNest. Convención de docs y
disciplina de fases heredadas de Oteo/FleetPilot.

## Estado actual
**Fase 1 — Núcleo: CERRADA (DoD completo). Siguiente: Fase 2 — Dashboard + deploy.**
Repo público: `github.com/faborubio/faro` (remote HTTPS). SAD en 1.2.0.

**Lo que ya existe (no rehacer):**
- **Fuente v1 = CMF oficial** (ADR-002 enmendado); API key verificada, vive en `.env` (gitignored,
  ADR-009; plantilla en `.env.example`). mindicador.cl es fallback futuro (Fase 4).
- **Go 1.26.5** en `~/.local/go` y **sqlc v1.31** en `~/go/bin` (ambos fuera del PATH de shells
  no interactivos). Dependencias del módulo: solo `pgx/v5` (ADR-004).
- **Dominio** `internal/indicator`: `Snapshot`, `Cadence` (daily/monthly, ADR-011), interfaz
  `IndicatorSource`.
- **Postgres dev** en Docker (`./scripts/dev-db.sh`, contenedor `faro-pg`) + migraciones SQL
  numeradas (`./scripts/migrate.sh`, idempotente). 4 tablas del SAD §6, catálogo sembrado. En el
  mismo contenedor vive la BD `faro_test` de los tests de integración (la borran y re-migran).
- **Adapter `internal/source/cmf`**: 1 llamada/indicador, backoff en 5xx, fallas parciales
  entregan lo obtenido + error agregado, parseo chileno estricto (CASE-003). Tests `httptest`
  con fixtures reales; la key nunca viaja en errores.
- **Storage `internal/store`**: sqlc sobre pgx — queries en `queries.sql`, código generado en
  `db/` (regenerar: `sqlc generate`). Upsert por (código, fecha) que afecta 0 filas si el valor
  no cambió: la señal "sin dato nuevo" del ADR-011 sin query extra. `ErrNotFound` como sentinel.
  Tests de integración activados por `FARO_TEST_DATABASE_URL`; sin la variable se saltan.
- **Scheduler `internal/refresh`**: on-boot + ticker (`REFRESH_INTERVAL`, default 24h). Persiste
  lo parcial ante falla de fuente y cierra el `sync_run` siempre — incluso con SIGTERM a mitad de
  ciclo (`context.WithoutCancel`, con test). 0 cambios con fuente sana = run 'ok' (ADR-011,
  verificado con datos reales). Sin BD no llama a la fuente.
- **API `internal/api`**: `GET /api/{code}` y `GET /api/{code}/history?desde=…&hasta=…` (default
  últimos 30 días), ServeMux stdlib (sin chi), cache en memoria TTL 60 s con header `X-Cache`,
  errores 404/400 en JSON, jamás llama a la fuente (ADR-003). `cmd/faro` corre scheduler + server
  HTTP en el mismo binario con apagado graceful.
- **CI verde**: vet → staticcheck 2026.1 → test (unitarios + integración contra servicio
  Postgres 17 del job, BD `faro_test`) → build. Sin red real ni secretos.
- **Datos reales verificados** (2026-07-09): cadencias confirmadas (AUD-001 pagada), IPC 0,0%
  es valor legítimo (CASE-005), valores del día ya publicados a las 16:08 de Chile (CASE-004).

**Fase 2 — Dashboard + deploy (siguiente), alcance (SAD §13):** HTML server-rendered + Chart.js
embebido con `go:embed` (ADR-005); Dockerfile multi-stage mínimo; **primer deploy a VibeNest →
URL pública viva** (ADR-008). Pendientes que tocan Fase 2: AUD-002 (migraciones sin psql —
candidato: embeberlas y aplicarlas al boot), AUD-003 (backfill histórico si los gráficos piden
series hacia atrás), AUD-004 (sync_runs huérfanos tras crash duro), CASE-002 (observar un fin de
semana real en `sync_runs`), CASE-004 (hora del ticker, con evidencia).

**Arranque sugerido de Fase 2 (orden):**
1. **Migraciones embebidas**: `go:embed migrations/` + aplicación idempotente al boot (mismo
   contrato `schema_migrations` del script) — paga AUD-002 y desbloquea el deploy.
2. **Dashboard**: HTML + Chart.js embebidos, consumiendo la propia API (`/api/:code/history`).
   Si 30 días de histórico no bastan para los gráficos, decidir el backfill aquí (AUD-003).
3. **Dockerfile** multi-stage (imagen mínima, ADR-008) + prueba local del contenedor.
4. **Deploy a VibeNest**: `DATABASE_URL` y `CMF_API_KEY` por ENV del panel; URL viva y
   `sync_runs` acumulando evidencia (CASE-002, CASE-004, AUD-004).
5. **Cierre**: DoD de 7 pasos + sesión nueva.

## Comandos (una vez con Go instalado)
| Acción | Comando |
|---|---|
| Tests | `go test ./...` (los de integración se saltan sin la variable de abajo) |
| Tests + integración | `FARO_TEST_DATABASE_URL='postgres://faro:faro@localhost:5432/faro_test?sslmode=disable' go test ./...` |
| Vet | `go vet ./...` |
| Lint | `staticcheck ./...` |
| Regenerar sqlc | `sqlc generate` (tras tocar `queries.sql` o `migrations/`) |
| Correr local | `set -a; . ./.env; set +a; go run ./cmd/faro` (scheduler + API en :8080) |
| Build | `go build -o bin/faro ./cmd/faro` |
| BD de desarrollo | `./scripts/dev-db.sh` (levanta) / `./scripts/dev-db.sh stop` |
| Migraciones | `./scripts/migrate.sh` (SQL numerado en `migrations/`, idempotente) |
| BD de tests (una vez) | `docker exec faro-pg createdb -U faro faro_test` |

## Arquitectura en una línea
Un solo binario Go: **scheduler** (refresca 1×/día tras adapter → Postgres) + **API** (sirve de
Postgres + cache, nunca llama a la fuente en la request — ADR-003) + **dashboard** (Chart.js embebido).
`IndicatorSource` aísla la fuente: el dominio no sabe de la CMF (ADR-002). En tests, `httptest`.

## Roadmap (SAD §13)
- **Fase 0 — Cimientos: ✓ CERRADA** — gates + Go + scaffold + Postgres + adapter CMF testeado + CI.
- **Fase 1 — Núcleo: ✓ CERRADA** — storage sqlc/pgx + scheduler + `sync_runs` + API (actual + histórico) + cache + CI con Postgres. ← siguiente: Fase 2
- **Fase 2 — Dashboard + deploy:** HTML + Chart.js embebido; Dockerfile; **primer deploy a VibeNest** (URL viva).
- **Fase 3 — Distribución:** alertas por webhook + widgets embebibles + rate limiting + CORS.
- **Fase 4 — Robustez (solo con tracción):** mindicador.cl (y/o BCCh) como fallback de la CMF; docs OpenAPI; métricas.

## Cierre de fase — Definition of Done (obligatorio, El Método §4 — 7 pasos, en orden)
1. **Ronda crítica (vista de halcón)** — releer el código de la fase cazando bugs y casos borde.
2. **Casos borde → `docs/CASES.md`** — sobre todo tras datos reales (cadencia, días no hábiles, nulos).
3. **Deuda → `docs/AUDIT.md`** — todo trade-off aceptado con trabajo futuro obtiene su `AUD-NNN`.
4. **Incidentes → `docs/TROUBLESHOOTING.md`**.
5. **Contexto → este `CLAUDE.md` + `README.md`** — roadmap, comandos, estado, en sincronía.
6. **Verde** — `go vet` + `staticcheck` + `go test ./...` limpios.
7. **Commit + push.**

Tras el cierre, la fase siguiente **arranca en sesión nueva** (humana o IA): es la prueba real de
la regla 5 — este archivo debe bastar para retomar en frío. No se encadenan fases en una sesión.

## Reglas del repo
1. El SAD cambia **solo por ADR nuevo o enmienda versionada** (§16), nunca ediciones silenciosas.
2. Todo trade-off "aceptado" que implique trabajo futuro **debe** tener su `AUD-NNN` en AUDIT.md.
3. Antes de tocar una heurística/lista/config, el caso real se documenta en `docs/CASES.md`: se
   calibra con evidencia, no con intuición (El Método §3).
4. El adapter jamás llama a la fuente real en CI (respuestas grabadas con `httptest`).
5. Prueba de que este archivo funciona: una sesión nueva retoma el proyecto leyendo solo esto.

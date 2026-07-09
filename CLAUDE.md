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
**Fase 0 — Cimientos: CERRADA (DoD completo). Siguiente: Fase 1 — Núcleo.**
Repo público: `github.com/faborubio/faro` (remote HTTPS). SAD en 1.2.0.

**Lo que ya existe (no rehacer):**
- **Fuente v1 = CMF oficial** (ADR-002 enmendado); API key verificada, vive en `.env` (gitignored,
  ADR-009; plantilla en `.env.example`). mindicador.cl es fallback futuro (Fase 4).
- **Go 1.26.5** en `~/.local/go` (sin sudo; PATH en `~/.bashrc`). Cero dependencias: puro stdlib.
- **Dominio** `internal/indicator`: `Snapshot`, `Cadence` (daily/monthly, ADR-011), interfaz
  `IndicatorSource`.
- **Postgres dev** en Docker (`./scripts/dev-db.sh`, contenedor `faro-pg`) + migraciones SQL
  numeradas (`./scripts/migrate.sh`, idempotente). 4 tablas del SAD §6, catálogo sembrado.
- **Adapter `internal/source/cmf`**: 1 llamada/indicador, backoff en 5xx, fallas parciales
  entregan lo obtenido + error agregado, parseo chileno estricto (CASE-003: `"12.34"` falla, no
  corrompe). 10 tests `httptest` con fixtures reales (`testdata/`); la key nunca viaja en errores.
- **CI verde**: vet → staticcheck 2026.1 → test → build (sin red real ni secretos).

**Fase 1 — Núcleo (siguiente), alcance (SAD §13):** scheduler de refresco diario (ticker +
on-boot) → persistir snapshots (upsert código+fecha) + `sync_runs`; API `GET /api/:code` y
`/api/:code/history` + cache en memoria; `sqlc`/`pgx` entran aquí (primeras dependencias).
Pendientes que tocan Fase 1: AUD-001 (verificar cadencia con datos reales), AUD-003 (límite 1 MB
si hay backfill histórico), CASE-004 (caracterizar hora de publicación CMF antes de fijar el ticker).

## Comandos (una vez con Go instalado)
| Acción | Comando |
|---|---|
| Tests | `go test ./...` |
| Vet | `go vet ./...` |
| Lint | `staticcheck ./...` |
| Correr local | `go run ./cmd/faro` (o el path del main) |
| Build | `go build -o bin/faro ./cmd/faro` |
| BD de desarrollo | `./scripts/dev-db.sh` (levanta) / `./scripts/dev-db.sh stop` |
| Migraciones | `./scripts/migrate.sh` (SQL numerado en `migrations/`, idempotente) |

## Arquitectura en una línea
Un solo binario Go: **scheduler** (refresca 1×/día tras adapter → Postgres) + **API** (sirve de
Postgres + cache, nunca llama a la fuente en la request — ADR-003) + **dashboard** (Chart.js embebido).
`IndicatorSource` aísla la fuente: el dominio no sabe de la CMF (ADR-002). En tests, `httptest`.

## Roadmap (SAD §13)
- **Fase 0 — Cimientos: ✓ CERRADA** — gates + Go + scaffold + Postgres + adapter CMF testeado + CI.
- **Fase 1 — Núcleo:** scheduler de refresco + histórico + `sync_runs` + API (actual + histórico) + cache. ← siguiente
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

## Reglas del repo
1. El SAD cambia **solo por ADR nuevo o enmienda versionada** (§16), nunca ediciones silenciosas.
2. Todo trade-off "aceptado" que implique trabajo futuro **debe** tener su `AUD-NNN` en AUDIT.md.
3. Antes de tocar una heurística/lista/config, el caso real se documenta en `docs/CASES.md`: se
   calibra con evidencia, no con intuición (El Método §3).
4. El adapter jamás llama a la fuente real en CI (respuestas grabadas con `httptest`).
5. Prueba de que este archivo funciona: una sesión nueva retoma el proyecto leyendo solo esto.

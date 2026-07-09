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
**Fase 0 — Cimientos: EN CURSO.** Repo público en `github.com/faborubio/faro` (remote HTTPS).
Existen el SAD (1.2.0), este CLAUDE.md y los docs compañeros día-1 (`docs/AUDIT.md`, `docs/CASES.md`,
`docs/TROUBLESHOOTING.md`). Progreso — gates antes de código (El Método §4):
0. **Gates.** (a) **Legal ✓:** resuelto — fuente v1 = **CMF oficial** (ToS clara vía API key),
   mindicador.cl como fallback (ADR-002 enmendado). (b) **API key ✓:** obtenida y verificada con
   `scripts/smoke-cmf.sh` (UF/dólar/UTM/IPC responden). Vive en `.env` (gitignored, ADR-009).
   (c) **Viabilidad ✓:** Go instalable sin sudo (tarball `linux-amd64` → `~/.local`) + free tier
   VibeNest, confirmado. Sin gates verdes, no se construye.
1. **Instalar Go ✓** — `go1.26.5 linux/amd64` en `~/.local/go` (sin sudo, checksum verificado),
   PATH en `~/.bashrc` (incluye `~/go/bin` para herramientas). Verificado con build+run real.
2. `go mod init` (módulo `github.com/faborubio/faro` o similar), estructura de paquetes.
3. Postgres + migraciones (tablas `indicators`, `indicator_values`, `alerts`, `sync_runs` — §6 del SAD).
4. Adapter `CMF` (impl de `IndicatorSource`, ADR-002; una llamada por indicador, API key vía ENV) con
   tests `httptest` (cero red real).
5. CI (`go vet` + staticcheck + `go test`).

## Comandos (una vez con Go instalado)
| Acción | Comando |
|---|---|
| Tests | `go test ./...` |
| Vet | `go vet ./...` |
| Lint | `staticcheck ./...` |
| Correr local | `go run ./cmd/faro` (o el path del main) |
| Build | `go build -o bin/faro ./cmd/faro` |
| Migraciones | por definir (golang-migrate o SQL simple) |

## Arquitectura en una línea
Un solo binario Go: **scheduler** (refresca 1×/día tras adapter → Postgres) + **API** (sirve de
Postgres + cache, nunca llama a la fuente en la request — ADR-003) + **dashboard** (Chart.js embebido).
`IndicatorSource` aísla la fuente: el dominio no sabe de la CMF (ADR-002). En tests, `httptest`.

## Roadmap (SAD §13)
- **Fase 0 — Cimientos:** gates (API key CMF) + Go + scaffold + Postgres + adapter CMF con tests + CI. ← siguiente
- **Fase 1 — Núcleo:** scheduler de refresco + histórico + `sync_runs` + API (actual + histórico) + cache.
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

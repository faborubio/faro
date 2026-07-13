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
**Fase 3 — Distribución: CERRADA (DoD completo). Siguiente: Fase 4 — Robustez, SOLO con
tracción real (SAD §13); mientras tanto, pagar pendientes (abajo) y observar uso.**
Repo público: `github.com/faborubio/faro` (remote HTTPS). SAD en 1.2.0.
**URL pública viva: `https://faro.vibenest.net/`** (VibeNest sobre Coolify, Hetzner).

**⚠️ Lo único cojo: la CMF es inalcanzable desde el host de prod (AUD-005 / T-004).**
Re-diagnóstico con soporte (2026-07-13): NO es egress general — el host y el contenedor salen a
internet; **solo las IPs de la CMF hacen timeout desde ese host** (filtro de ruta/IP de origen:
la CMF responde desde otros nodos Hetzner y desde EE.UU.). Soporte investiga con el proveedor;
pidió no redeployar. Consecuencias: los **webhooks de alertas probablemente SÍ salen en prod**
(por confirmar con el primer cruce real); si el filtro no se destraba, adelantar el fallback
mindicador.cl (ADR-002) o pedir cambio de IP. Los datos de prod (seed por consola SQL, receta en
`docs/DEPLOY.md`) envejecen 1 día/día; cuando la CMF vuelva a ser alcanzable el scheduler retoma
solo — verificar el primer `refresco ok` y **cerrar AUD-005**. Localmente TODO está verificado
E2E contra la CMF real (incluido un cruce de alerta entregado a un receptor local).
**Plan A listo para activar:** `CMF_BASE_URL` (ENV opcional) re-apunta el adapter a un Worker
de Cloudflare propio (`scripts/cmf-proxy-worker.js`, receta en `docs/DEPLOY.md` §Plan A) — con
eso el scheduler completo vuelve a operar en prod sin seed manual. Requiere deployar Fase 3
(prod aún corre el binario de Fase 2 — `/healthz` da 404) y crear el Worker en Cloudflare.

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
  verificado con datos reales). Sin BD no llama a la fuente. **Backfill on-boot** (CASE-006):
  indicadores sin valores traen año actual + anterior vía `FetchYear` (capacidad opcional
  `indicator.HistoricalSource`), descartando fechas futuras — la UF llega publicada ~1 mes
  adelante. **Barrido de huérfanos** al boot: runs 'running' > 1 h se cierran 'error' (AUD-004).
- **API `internal/api`**: `GET /api/{code}` y `GET /api/{code}/history?desde=…&hasta=…` (default
  últimos 30 días), ServeMux stdlib (sin chi), cache en memoria TTL 60 s con header `X-Cache`,
  errores 404/400 en JSON, jamás llama a la fuente (ADR-003). `cmd/faro` corre scheduler + server
  HTTP en el mismo binario con apagado graceful.
- **Migraciones embebidas** (`internal/migrate`, AUD-002): `cmd/faro` aplica `migrations/*.sql`
  al boot — mismo contrato `schema_migrations` que `scripts/migrate.sh` (intercambiables), cada
  archivo en una transacción, advisory lock para boots solapados.
- **Dashboard `internal/web`** (ADR-005): HTML server-rendered + Chart.js 4.5.1 vendoreado
  (`go:embed`, sin CDN), un gráfico por indicador contra la propia API (líneas 90 días; barras
  13 meses para IPC), **convertidor** UF/dólar/UTM ↔ CLP (UI pura, valores del día), paleta
  validada (skill dataviz) claro/oscuro, tabla accesible, atribución CMF (gate legal). Assets
  con `?v=<hash>` (cache-busting) y `Cache-Control` 1 h.
- **Docker** (ADR-008): multi-stage → scratch, **19 MB** (binario estático + certs CA). `.env`
  excluido del contexto; usuario no-root. Verificado en contenedor real (migró + backfill por
  HTTPS + SIGTERM graceful).
- **Deploy VibeNest**: receta completa y aprendizajes reales en `docs/DEPLOY.md` (Internal Port
  = `PORT`, `CMF_API_KEY` exacta en Environment, seed por consola SQL como contingencia).
- **CI verde**: vet → staticcheck 2026.1 → test (unitarios + integración contra servicio
  Postgres 17 del job, BD `faro_test`) → build. Sin red real ni secretos.
- **Datos reales verificados** (2026-07-09): cadencias confirmadas (AUD-001 pagada), IPC 0,0%
  es valor legítimo (CASE-005), series anuales ≤ 25 KB y UF publicada a futuro (CASE-006).
- **Tests de integración**: todo paquete nuevo que toque la BD obtiene el DSN vía
  `internal/testdb.Acquire(t)` — jamás leyendo `FARO_TEST_DATABASE_URL` directo (`go test ./...`
  corre paquetes en paralelo y se pisan la BD compartida — T-003).

**Lo nuevo de Fase 3 (no rehacer):**
- **Alertas por webhook** (ADR-006): `POST /api/alerts` (registro sin login → token opaco
  crypto/rand de 64 hex, único handle), `GET`/`DELETE /api/alerts/{token}`. Semántica de
  **cruce** (edge-triggered — CASE-007 y sus 3 bordes: se-mantiene, ciclo-sin-cambios,
  corrección histórica). Evaluador en `internal/alert` (implementa `refresh.Notifier`; el
  scheduler le entrega SOLO los snapshots que cambiaron, con `context.WithoutCancel` para que
  un SIGTERM no aborte un POST en vuelo). Sin reintentos ni auto-disable (AUD-006). Migración
  002: índice único del token.
- **Anti-SSRF en `internal/webhook`** (SAD §8; postura completa en `docs/SECURITY.md`, que nace
  en esta fase): 2 capas — `ValidateURL` al registrar + dial pineado a la IP validada al
  despachar (mata DNS rebinding); sin proxy del entorno, sin redirects. Escape SOLO dev:
  `FARO_WEBHOOK_ALLOW_PRIVATE=1` (el boot lo grita en el log).
- **Widget embebible** (ADR-007): `GET /widget/{code}` en `internal/web` — mini-card HTML
  autocontenida (sin JS, claro/oscuro), `Cache-Control` 5 min, jamás X-Frame-Options. Snippets
  (iframe + fetch) en el README; link por tarjeta en el dashboard.
- **Rate limiting** (ADR-010): `internal/ratelimit`, token bucket por IP a mano (stdlib,
  ADR-004): 5 req/s, ráfaga 30, mapa acotado a 4096 IPs. IP = última entrada de
  X-Forwarded-For (la escribe el proxy de la plataforma). Envuelve todo en `cmd/faro` salvo
  `/healthz`. 429 JSON + Retry-After.
- **CORS abierto** en `/api/*` (middleware en `internal/api`, preflight OPTIONS → 204) y
  **`/healthz`** en `cmd/faro` (ping a Postgres → 200/503; catch-up del SAD §8).
- **Ruteo con trampa:** `GET /api/alerts/{token}` conflictúa con `GET /api/{code}/history` en un
  mismo ServeMux (ambos matchean `/api/alerts/history` → panic al registrar); las alertas viven
  en un sub-mux tras el prefijo literal `/api/alerts` — ver comentario en `api.Handler()`.
- **Contrato interno cambiado:** `store.UpsertSnapshots` devuelve `[]indicator.Snapshot` (los
  que cambiaron), no un conteo — esa es la señal que gatilla la evaluación de alertas.

**Pendientes:** **AUD-005** (egress en prod: bloquea refresco + webhooks — verificar y cerrar
apenas VibeNest arregle), **AUD-006** (reintentos/auto-disable de webhooks — Fase 4, con
tracción), CASE-002 (observar un fin de semana real en `sync_runs`), CASE-004 (hora del ticker
con evidencia — bloqueada por AUD-005).

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
| Migraciones | `./scripts/migrate.sh` (o solas al boot del binario — mismo contrato) |
| BD de tests (una vez) | `docker exec faro-pg createdb -U faro faro_test` |
| Imagen Docker | `docker build -t faro .` (scratch, ~19 MB) |
| Seed de prod (contingencia T-004) | receta en `docs/DEPLOY.md` (dump idempotente → consola SQL del panel) |
| E2E de alertas en local | correr con `FARO_WEBHOOK_ALLOW_PRIVATE=1` + receptor loopback (docs/SECURITY.md; SOLO dev) |

## Arquitectura en una línea
Un solo binario Go: **scheduler** (refresca 1×/día tras adapter → Postgres) + **API** (sirve de
Postgres + cache, nunca llama a la fuente en la request — ADR-003) + **dashboard** (Chart.js embebido).
`IndicatorSource` aísla la fuente: el dominio no sabe de la CMF (ADR-002). En tests, `httptest`.

## Roadmap (SAD §13)
- **Fase 0 — Cimientos: ✓ CERRADA** — gates + Go + scaffold + Postgres + adapter CMF testeado + CI.
- **Fase 1 — Núcleo: ✓ CERRADA** — storage sqlc/pgx + scheduler + `sync_runs` + API (actual + histórico) + cache + CI con Postgres.
- **Fase 2 — Dashboard + deploy: ✓ CERRADA** — migraciones al boot + dashboard con Chart.js + convertidor + backfill + Dockerfile scratch + **URL viva en VibeNest** (refresco en prod pendiente de plataforma, AUD-005).
- **Fase 3 — Distribución: ✓ CERRADA** — alertas por webhook (cruce + anti-SSRF 2 capas + token opaco) + widget embebible + rate limiting + CORS + `/healthz` + `docs/SECURITY.md`. Verificada E2E en local; en prod las alertas quedan latentes hasta el fix de AUD-005. ← siguiente: Fase 4 **solo con tracción**
- **Fase 4 — Robustez (solo con tracción):** mindicador.cl (y/o BCCh) como fallback de la CMF; docs OpenAPI; métricas; reintentos/auto-disable de webhooks (AUD-006).

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

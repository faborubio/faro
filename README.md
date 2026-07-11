# 🗼 Faro

[![ci](https://github.com/faborubio/faro/actions/workflows/ci.yml/badge.svg)](https://github.com/faborubio/faro/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**API pública + dashboard de indicadores económicos de Chile** — dólar, UF, UTM, IPC — con datos
de la fuente oficial (CMF), histórico, alertas por webhook y widgets embebibles.

> Un faro guía por su luz: valores de referencia que en Chile se consultan a diario, servidos
> rápido, con histórico y desde la fuente autoritativa.

**Estado: 🟢 en producción — [faro.vibenest.net](https://faro.vibenest.net/).** Dashboard con
tendencias y convertidor a pesos, API JSON con histórico desde 2025, **alertas por webhook,
widgets embebibles**, rate limiting + CORS, imagen Docker de 19 MB. Ver [roadmap](#roadmap).

---

## Por qué existe

En Chile, el dólar, la UF, la UTM y el IPC se necesitan todo el tiempo: un dev que calcula un
precio, un contador que aplica la UTM del mes, cualquiera que quiere ver "cómo viene el dólar".
Las opciones actuales obligan a elegir entre **comodidad** (agregadores de terceros, sin SLA ni
términos claros) y **autoridad** (fuentes oficiales, incómodas de consumir). Faro junta lo mejor:

- ⚡ **API JSON rápida** — valor actual + histórico, p95 < 100 ms (el dato se refresca 1×/día
  desde la fuente; ninguna request espera por ella).
- 📊 **Dashboard con tendencias** — Chart.js, server-rendered, todo embebido en el binario, y un
  **convertidor** UF/dólar/UTM ↔ pesos con los valores del día.
- 🔔 **Alertas por webhook** — *"avísame si el dólar cruza $1.000"* → POST a Slack/Discord/n8n.
- 🧩 **Widgets embebibles** — el valor del día en cualquier web con un `<iframe>`/snippet.
- 🏛️ **Fuente oficial** — API de la **CMF** (Comisión para el Mercado Financiero), con API key.

## Arquitectura en una línea

**Un solo binario Go** (~30 MB de RSS): un *scheduler* refresca los indicadores 1×/día tras un
adapter (`IndicatorSource`), persiste el histórico en **PostgreSQL**, y la **API** sirve siempre
desde la base + cache en memoria — nunca llama a la fuente en el camino de la request. Si la
fuente cae, Faro sigue sirviendo el último valor conocido.

```
scheduler (1×/día) ──► CMF ──► Snapshot ──► PostgreSQL ──► API / dashboard / widgets
                                  │                            ▲
                                  └── evalúa alertas ──► webhooks
```

El diseño completo — 11 ADRs con sus trade-offs — vive en **[SAD-Faro.md](SAD-Faro.md)**.

## API

Dos endpoints públicos de solo lectura (JSON, fechas `YYYY-MM-DD`):

```bash
# Valor vigente
$ curl https://faro.vibenest.net/api/dolar
{"code":"dolar","name":"Dólar observado","unit":"CLP","value":928.99,"date":"2026-07-10"}

# Histórico por rango (sin ?desde= son los últimos 30 días; ?hasta= por defecto es hoy)
$ curl "https://faro.vibenest.net/api/uf/history?desde=2026-07-01"
{"code":"uf","unit":"CLP","desde":"2026-07-01","hasta":"2026-07-10","values":[{"date":"2026-07-01","value":40823.03},…]}
```

Códigos v1: `uf` · `dolar` · `utm` · `ipc`. Un código desconocido responde `404 {"error":…}`;
un rango malformado, `400`. Las respuestas salen de Postgres con cache en memoria (TTL 60 s,
observable en el header `X-Cache`) — ninguna request espera por la CMF.

La API tiene **CORS abierto** (consumible por fetch desde cualquier web) y **rate limiting** por
IP (ráfagas acotadas; al exceder: `429` + `Retry-After`). Postura completa en
[docs/SECURITY.md](docs/SECURITY.md).

### Alertas por webhook

*"Avísame si el dólar cruza $1.000"*: se registra sin cuenta y devuelve un **token** — la única
llave para consultar o borrar la alerta (guárdalo). Dispara **al cruzar** el umbral (no re-notifica
mientras se mantenga) con un POST JSON al `webhook_url` (Slack/Discord/n8n/lo que sea).

```bash
# Registrar (operator: gt = supera el umbral, lt = cae bajo él)
$ curl -X POST https://faro.vibenest.net/api/alerts \
    -H 'Content-Type: application/json' \
    -d '{"indicator":"dolar","operator":"gt","threshold":1000,"webhook_url":"https://hooks.slack.com/…"}'
{"token":"7a30f7…","indicator":"dolar","operator":"gt","threshold":1000,…}

$ curl https://faro.vibenest.net/api/alerts/<token>      # consultar (incluye last_triggered_at)
$ curl -X DELETE https://faro.vibenest.net/api/alerts/<token>   # borrar (204)
```

El payload que recibe el webhook:

```json
{"indicator":"dolar","name":"Dólar observado","unit":"CLP","operator":"gt",
 "threshold":1000,"value":1005.3,"date":"2026-07-10",
 "message":"Dólar observado superó el umbral 1000: valor 1005.3 (2026-07-10)"}
```

La `webhook_url` debe ser pública (http/https): loopback, redes privadas y metadata de clouds se
rechazan con `400` — anti-SSRF en dos capas, ver [docs/SECURITY.md](docs/SECURITY.md).

### Embeber el valor en tu web

**Widget** (mini-card autocontenida, claro/oscuro automático, sin JS):

```html
<iframe src="https://faro.vibenest.net/widget/dolar"
        width="220" height="90" style="border:0" title="Dólar observado — Faro"></iframe>
```

**Snippet JS** contra la API (CORS abierto):

```html
<span id="dolar"></span>
<script>
  fetch("https://faro.vibenest.net/api/dolar")
    .then(r => r.json())
    .then(d => document.getElementById("dolar").textContent =
      `$${d.value.toLocaleString("es-CL")} (${d.date})`);
</script>
```

## Stack

| Capa | Elección | Por qué |
|---|---|---|
| Lenguaje | **Go** (stdlib `net/http`, sin framework) | binario estático, ~30 MB RSS, cabe en un free tier de 256 MB |
| Base de datos | **PostgreSQL** + `sqlc`/`pgx` | histórico append-only, queries type-safe verificadas en compilación |
| Front | HTML server-rendered + **Chart.js**, `go:embed` | un solo artefacto de deploy, sin CDN ni build de front |
| Deploy | **Docker** multi-stage (`scratch`/distroless) en VibeNest | imagen de pocos MB, arranque < 1 s |
| Datos | **API oficial CMF** (fallback: mindicador.cl) | fuente autoritativa con términos claros |

## Roadmap

| Fase | Entrega | Estado |
|---|---|---|
| **0 — Cimientos** | gates (legal · API key · viabilidad) · scaffold · Postgres · adapter CMF testeado · CI | ✅ |
| **1 — Núcleo** | scheduler + histórico + API (actual e histórico) + cache | ✅ |
| **2 — Dashboard + deploy** | Chart.js embebido · convertidor · Dockerfile 19 MB · **[URL pública viva](https://faro.vibenest.net/)** | ✅ |
| **3 — Distribución** | alertas webhook (cruce + anti-SSRF) · widgets embebibles · rate limiting · CORS · `/healthz` | ✅ |
| **4 — Robustez** *(solo con tracción)* | fallback mindicador.cl/BCCh · OpenAPI · métricas | ⏳ |

## Desarrollo

Requiere Go ≥ 1.26 y una API key de la CMF ([solicitud gratuita](https://api.sbif.cl/api/contactanos.jsp)).

```bash
cp .env.example .env         # completar CMF_API_KEY
./scripts/smoke-cmf.sh       # verificar que la key responde (UF/dólar/UTM/IPC)

./scripts/dev-db.sh          # Postgres 17 en Docker (contenedor faro-pg)
./scripts/migrate.sh         # esquema + catálogo sembrado

go test ./...                # tests (cero red real: la fuente se simula con httptest)
go vet ./... && staticcheck ./...
set -a; . ./.env; set +a; go run ./cmd/faro   # scheduler + API en :8080
```

Los tests de integración del store corren contra un Postgres real y se activan con
`FARO_TEST_DATABASE_URL` (una BD de pruebas que **borran** en cada corrida — ver `.env.example`);
sin la variable se saltan. En CI corren contra un Postgres efímero del job.

## Cómo está documentado

Este repo sigue **El Método**: el diseño manda y se versiona, las decisiones son ADRs, la deuda es
visible y cada fase cierra con una *Definition of Done*.

| Documento | Qué responde |
|---|---|
| [SAD-Faro.md](SAD-Faro.md) | ¿Cómo está diseñado y por qué? — la fuente de verdad (ADRs, trade-offs) |
| [CLAUDE.md](CLAUDE.md) | ¿Cómo retomo el proyecto en 5 minutos? |
| [docs/DEPLOY.md](docs/DEPLOY.md) | ¿Cómo se construye y despliega? (imagen, ENV, VibeNest) |
| [docs/SECURITY.md](docs/SECURITY.md) | ¿De qué me protejo y cómo? (anti-SSRF, tokens, rate limiting, CORS) |
| [docs/AUDIT.md](docs/AUDIT.md) | ¿Qué deuda técnica acepté y cómo se paga? |
| [docs/CASES.md](docs/CASES.md) | ¿Qué casos raros del dominio encontré? (con datos reales) |
| [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) | ¿Qué falló y cómo se arregló? |

## Datos y atribución

Los indicadores provienen de la **[API oficial de la CMF](https://api.cmfchile.cl/documentacion/index.html)**
(Comisión para el Mercado Financiero, Chile). Faro cachea y re-sirve los valores con su fecha de
publicación; la fuente autoritativa es siempre la CMF.

---

**Fabián Rubio** · [github.com/faborubio](https://github.com/faborubio) — proyecto hermano de
**Oteo**: allá se otea el territorio; aquí Faro enciende una luz de referencia sobre la economía.

# 🗼 Faro

[![ci](https://github.com/faborubio/faro/actions/workflows/ci.yml/badge.svg)](https://github.com/faborubio/faro/actions/workflows/ci.yml)
[![license: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**API pública + dashboard de indicadores económicos de Chile** — dólar, UF, UTM, IPC — con datos
de la fuente oficial (CMF), histórico, alertas por webhook y widgets embebibles.

> Un faro guía por su luz: valores de referencia que en Chile se consultan a diario, servidos
> rápido, con histórico y desde la fuente autoritativa.

**Estado: 🚧 Fase 0 — Cimientos (en construcción).** El diseño está completo y los gates de
viabilidad verificados; el código se está construyendo fase a fase. Ver [roadmap](#roadmap).

---

## Por qué existe

En Chile, el dólar, la UF, la UTM y el IPC se necesitan todo el tiempo: un dev que calcula un
precio, un contador que aplica la UTM del mes, cualquiera que quiere ver "cómo viene el dólar".
Las opciones actuales obligan a elegir entre **comodidad** (agregadores de terceros, sin SLA ni
términos claros) y **autoridad** (fuentes oficiales, incómodas de consumir). Faro junta lo mejor:

- ⚡ **API JSON rápida** — valor actual + histórico, p95 < 100 ms (el dato se refresca 1×/día
  desde la fuente; ninguna request espera por ella).
- 📊 **Dashboard con tendencias** — Chart.js, server-rendered, todo embebido en el binario.
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
| **0 — Cimientos** | gates (legal ✓ · API key ✓ · viabilidad ✓) · scaffold · Postgres · adapter CMF testeado · CI | 🚧 en curso |
| **1 — Núcleo** | scheduler + histórico + API (actual e histórico) + cache | ⏳ |
| **2 — Dashboard + deploy** | Chart.js embebido · Dockerfile · **URL pública viva** | ⏳ |
| **3 — Distribución** | alertas webhook · widgets embebibles · rate limiting · CORS | ⏳ |
| **4 — Robustez** *(solo con tracción)* | fallback mindicador.cl/BCCh · OpenAPI · métricas | ⏳ |

## Desarrollo

Requiere Go ≥ 1.26 y una API key de la CMF ([solicitud gratuita](https://api.sbif.cl/api/contactanos.jsp)).

```bash
cp .env.example .env         # completar CMF_API_KEY
./scripts/smoke-cmf.sh       # verificar que la key responde (UF/dólar/UTM/IPC)

go test ./...                # tests (cero red real: la fuente se simula con httptest)
go vet ./... && staticcheck ./...
go run ./cmd/faro            # correr local
```

## Cómo está documentado

Este repo sigue **El Método**: el diseño manda y se versiona, las decisiones son ADRs, la deuda es
visible y cada fase cierra con una *Definition of Done*.

| Documento | Qué responde |
|---|---|
| [SAD-Faro.md](SAD-Faro.md) | ¿Cómo está diseñado y por qué? — la fuente de verdad (ADRs, trade-offs) |
| [CLAUDE.md](CLAUDE.md) | ¿Cómo retomo el proyecto en 5 minutos? |
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

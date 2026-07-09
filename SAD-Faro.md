# Software Architecture Document (SAD)
## Faro — API + dashboard de indicadores económicos de Chile (Go + PostgreSQL)

| Campo | Valor |
|---|---|
| Proyecto | Faro — indicadores económicos de Chile como API pública + dashboard |
| Versión | 1.2.0 |
| Estado | Approved for implementation |
| Autor | Fabián Rubio — Full Stack |
| Audiencia | Portafolio + servicio público para devs y personas en Chile |
| Última revisión | 2026-07-08 |
| Método | Faro sigue **El Método** (`~/Workspace/metodo/MANIFIESTO.md`, v1.0.0) — este SAD es su instancia proporcional |

> **Nota de lectura.** Este SAD describe *por qué* Faro está construido así, no solo *qué* contiene.
> Las decisiones se registran como ADRs con su contexto y trade-offs. Un **faro** es un punto de
> referencia que guía por su luz; Faro guía por valores de referencia — dólar, UF, UTM, IPC — que
> en Chile todos consultan a diario. Hermana la metáfora de **Oteo** (otear desde la Atalaya): allá
> se otea el territorio comercial; aquí se enciende una luz de referencia sobre la economía. Es una
> **pieza de portafolio** y un servicio real, pensado para correr liviano en el free tier de VibeNest.
>
> **Cómo leer bajo El Método.** Faro no inventa su disciplina: la hereda de **El Método**
> (`~/Workspace/metodo/MANIFIESTO.md`), la doctrina de ingeniería del autor destilada de
> FleetPilot/Oteo. Este SAD es la **fuente de verdad** del proyecto; el método es la doctrina que lo
> gobierna. Todo lo que sigue está calibrado por el **principio rector de proporcionalidad** (§2.1):
> el rigor se dosifica a la escala real de Faro, ni más ni menos.

---

## 1. Contexto y objetivos

### 1.1 Problema que resuelve
En Chile, indicadores como el **dólar, la UF, la UTM y el IPC** se necesitan todo el tiempo: un dev
que calcula un precio, un contador que aplica la UTM del mes, alguien que quiere ver "cómo viene el
dólar". Hoy las opciones son (a) **mindicador.cl**, cómodo pero solo API y de un tercero sin SLA, o
(b) las fuentes **oficiales** (CMF, Banco Central), autoritativas pero incómodas (registro, códigos
de serie, sin dashboard). Falta un servicio que junte lo mejor: **API rápida + dashboard claro con
histórico + alertas + widgets embebibles**, corriendo con costo cercano a cero.

### 1.2 Objetivos (qué consideramos éxito)
- **API pública** de indicadores actuales + histórico, respondiendo en **< 100 ms** (cacheado).
- **Dashboard** que carga rápido y se ve bien: valores de hoy + gráficos de tendencia.
- **Alertas por webhook**: "avísame si el dólar cruza $1.000".
- **Widgets embebibles**: un `<iframe>`/snippet para poner el dólar en cualquier web.
- **Huella mínima**: corre holgado en el free tier de VibeNest (256 MB RAM / 0.5 vCPU).
- **Resultado**: un servicio en vivo, compartible, que luzca en el portafolio y demuestre Go + infra.

### 1.3 Fuera de alcance (v1)
- **Cuentas de usuario / auth**: la API y el dashboard son públicos; las alertas se registran por
  token, sin login.
- **Alertas por email/SMS**: requieren SMTP/servicio externo; v1 usa solo webhooks (ADR-006).
- **Series profundas del Banco Central** (PIB, agregados monetarios…): Faro cubre los indicadores
  comunes, no una base estadística completa.
- **Predicciones / modelos**: Faro informa valores, no pronostica.

---

## 2. Drivers de arquitectura y atributos de calidad

Prioridades ordenadas. Cuando dos chocan, gana el de más arriba.

| # | Atributo | Por qué prioriza | Cómo se mide |
|---|---|---|---|
| 1 | **Ligereza / bajo consumo** | Debe correr en 256 MB del free tier sin OOM | RSS del proceso < 100 MB en operación |
| 2 | **Latencia baja** | El valor cambia 1×/día: no hay excusa para ser lento | p95 de la API < 100 ms |
| 3 | **Resiliencia ante la fuente** | ninguna fuente externa (aun oficial) garantiza uptime | si la fuente cae, Faro sigue sirviendo el último valor; fallback secundario (ADR-002) |
| 4 | **Simplicidad operativa** | Un solo dev; el servicio no puede pedir niñera | un binario + un Postgres; cero servicios extra |
| 5 | **Legibilidad (portafolio)** | Es una carta de presentación | Go idiomático, testeado, `go vet`/staticcheck limpios |
| 6 | **Costo cero** | Free tier mientras dure; migrable | factura = $0 durante el trial; sin lock-in duro |

**Decisión consciente:** Faro optimiza para *ligereza y latencia*, no para escala masiva. Los datos
son diminutos (un puñado de indicadores, un valor por día) y cacheables casi por completo: casi toda
la complejidad está en hacerlo **simple, rápido y resiliente**, no en volumen.

### 2.1 Proporcionalidad — la dosis de rigor de Faro
El principio rector de El Método (MANIFIESTO §1) manda calibrar el rigor a la **escala real**, no a
un ideal: *el ceremonial de más no es virtud, es deuda*. Faro se ubica en un punto medio de esa
escala — más que una herramienta de un usuario, menos que un sistema con SLA. Su dosis declarada:

- **Sí, porque es API pública:** adapter tras interfaz (ADR-002), rate limiting + CORS (ADR-010),
  validación anti-SSRF de webhooks (§8), CI con vet/staticcheck/test, histórico auditable (`sync_runs`).
- **No, porque no lo merece la escala:** sin microservicios, sin colas, sin staging, sin auth/login,
  sin observabilidad tipo SIEM/tracing distribuido, sin `SECURITY.md`/`DEPLOY.md` hasta que la fase
  que los estrena llegue (§14). Un binario + un Postgres.

Cuando una decisión futura dude entre "más completo" y "proporcional", gana proporcional.

---

## 3. Restricciones y supuestos

**Restricciones**
- Lenguaje: **Go** (stdlib-first; router liviano `chi` solo si el ruteo crece — ADR-001).
- Base de datos: **PostgreSQL** (el Postgres gestionado de VibeNest expone un solo `DATABASE_URL`).
- Deploy: **VibeNest** (PaaS) con **Dockerfile propio** e imagen mínima (ADR-008).
- Fuente de datos v1: **CMF oficial** (API key gratuita vía ENV), tras un adapter; **mindicador.cl**
  como fallback (ADR-002).

**Supuestos**
- El refresco **diario** basta como cadencia de *sondeo*, pero los indicadores **no cambian todos a
  diario**: el dólar/UF varían en días hábiles, mientras **UTM e IPC son mensuales**. El refresco es
  idempotente (upsert por código+fecha), así que sondear a diario un valor mensual no duplica ni
  corrompe — solo confirma. Los casos de cadencia (valor sin cambio, día no hábil, publicación
  mensual, valor nulo de la fuente) se modelan con `cadence` en el catálogo (**ADR-011**), se
  registran en `docs/CASES.md` y se validan con datos reales en Fase 1 (AUD-001).
- Volumen bajo-moderado (portafolio): pocas peticiones por segundo, ráfagas ocasionales.
- El free tier de VibeNest (256 MB) es suficiente para Go + este volumen; si crece, se migra
  (es solo un binario + Postgres).

---

## 4. Vista general de la arquitectura

### 4.1 Un binario, tres responsabilidades
Faro es **un solo binario Go** que hace: (1) un **scheduler** interno (goroutine con ticker) que
refresca los indicadores 1×/día desde el adapter y los persiste; (2) una **API HTTP** que sirve los
datos ya persistidos (nunca llama a la fuente en la request); (3) un **dashboard** server-rendered con
assets embebidos (`go:embed`). Postgres guarda el histórico. No hay microservicios ni colas: a esta
escala, un binario stateless + una BD es todo.

```
                 (ticker diario, o refresco on-boot)
  ┌───────────────────────────────────────────────────────────┐
  │  Scheduler (goroutine)                                     │
  │      │  IndicatorSource (adapter, ADR-002)                 │
  │      ▼                                                     │
  │  CMF oficial ──► Snapshot normalizado ──► upsert           │
  │                                              │             │
  │                                              ▼             │
  │                          PostgreSQL (indicators,          │
  │                          indicator_values, alerts)         │
  │      │  tras cada refresco: evaluar alertas ──► webhooks   │
  └──────┬────────────────────────────────────────────────────┘
         │  (la API lee de Postgres + cache en memoria, ADR-003)
         ▼
   ┌───────────┐     ┌───────────┐     ┌───────────────┐
   │ API JSON  │     │ Dashboard │     │ Widgets       │
   │ /api/:cod │     │ (Chart.js)│     │ embebibles    │
   └───────────┘     └───────────┘     └───────────────┘
              Go (net/http) · un binario · VibeNest
```

### 4.2 El dominio no sabe de la fuente
`IndicatorSource` es un **adapter tras una interfaz** (mismo principio que `PlacesClient` en Oteo): el
scheduler y la API consumen un `Snapshot` normalizado, no el JSON crudo de la CMF. Si se agrega el
fallback mindicador.cl o la CMF cambia de formato, el dominio no se toca. En tests el adapter se
sustituye con `httptest` y respuestas grabadas — cero llamadas reales en CI.

---

## 5. Decisiones de arquitectura (ADRs)

### ADR-001 — Go con la librería estándar, sin framework pesado
**Contexto:** Faro es un servicio HTTP chico que debe caber en 256 MB. Frameworks tipo Gin/Echo suman
peso y dependencias; `net/http` de la stdlib es más que suficiente.
**Decisión:** **`net/http` de la stdlib**; se agrega `chi` (router liviano) solo si el ruteo crece.
Tests con el paquete `testing` estándar.
**Razón:** leanness (driver #1), legibilidad de portafolio (driver #5) y cero dependencias que
mantener. Go compila a un binario estático que arranca en milisegundos y ~15-30 MB de RSS.
**Trade-off:** más código boilerplate que con un framework. Aceptado: es poco y explícito.

### ADR-002 — Fuente de datos tras un adapter; **CMF oficial primero, mindicador.cl como fallback**
> **Enmienda (v1.2.0).** Invierte el orden de fuentes respecto a v1.0.0 (que ponía mindicador.cl
> primero y CMF después). Motivo: al revisar el **gate legal de Fase 0**, mindicador.cl resultó un
> agregador comunitario **sin ToS explícita** que *scrapea* al Banco Central, **sin SLA** (se cayó
> durante la propia revisión). La CMF es la API pública del **regulador**, con licencia clara de uso
> por API key y cobertura exacta del alcance de Faro. El adapter (esta misma interfaz) hace el cambio
> barato: el dominio no se toca.

**Contexto:** hay tres fuentes con distinta autoridad y fricción. **CMF** (Comisión para el Mercado
Financiero) expone una **API pública oficial** con API key gratuita, formato JSON, y cubre justo el
alcance de Faro (dólar, euro, IPC, UF, UTM). **mindicador.cl** es cómodo (sin registro, todo en una
llamada) pero es un tercero sin SLA que scrapea al BCCh y no publica términos de uso. **BCCh** (Base
de Datos Estadísticos) es la fuente última pero más compleja.
**Decisión:** una interfaz **`IndicatorSource`** que expone `Fetch() ([]Snapshot, error)`, con impl
**`CMF`** como **fuente v1** (una llamada por indicador, API key vía ENV — ADR-009). **mindicador.cl**
se implementa como **fallback secundario** cuando aporte resiliencia. La API key se obtiene por
formulario (gate de Fase 0, §13).
**Razón:** fuente **autoritativa y legalmente clara** desde el día 1 (mata la deuda de republicar sin
permiso), mejor resiliencia (driver #3: oficial + fallback comunitario) y más valor de portafolio.
Espejo del ADR-002 de Oteo (fuente tras interfaz).
**Trade-off:** requiere registrar una API key (gate pequeño, no bloqueante) y ~5 llamadas por refresco
(una por indicador) en vez de una. Aceptado: es exactamente el trabajo del adapter, y el refresco es
1×/día (ADR-003). Si el registro resultara bloqueante, mindicador.cl es el fallback inmediato.

### ADR-003 — Refresco programado + cache; la API nunca llama a la fuente en la request
**Contexto:** los indicadores cambian 1×/día. Llamar a la fuente en cada request sería lento, frágil
y abusivo con el tercero.
**Decisión:** un **scheduler** (ticker diario + refresco al arrancar) trae los datos y los **persiste**;
la API sirve **solo desde Postgres**, con una **cache en memoria** (mapa con TTL) por delante. La
fuente se toca ~1×/día, nunca en el camino de la request.
**Razón:** latencia < 100 ms (driver #2), resiliencia (driver #3: si la fuente cae, la API sigue con
lo último persistido) y respeto al tercero.
**Trade-off:** el dato puede tener hasta ~1 día de antigüedad. Aceptado: es la naturaleza del dato.

### ADR-004 — Persistencia con histórico; Postgres con queries type-safe
**Contexto:** el dashboard necesita gráficos de tendencia → hace falta histórico, no solo el último valor.
**Decisión:** tabla **`indicator_values`** append por (código, fecha) — cada refresco inserta el valor
del día sin pisar los anteriores. Acceso con **`sqlc`** (genera código Go type-safe desde SQL) sobre
`pgx`, o `database/sql` si se prefiere mínimo.
**Razón:** histórico para gráficos y alertas; `sqlc` da queries verificadas en compilación, idiomático
en Go y vistoso en portafolio.
**Trade-off:** una herramienta de codegen más. Aceptado: el type-safety paga.

### ADR-005 — Dashboard server-rendered + Chart.js, assets embebidos con `go:embed`
**Contexto:** el dashboard debe verse bien y desplegarse como parte del mismo binario, sin CDN externo
ni build de front separado.
**Decisión:** HTML server-rendered (templates de la stdlib) + **Chart.js** para los gráficos; todos
los assets (HTML/CSS/JS) **embebidos con `//go:embed`** en el binario.
**Razón:** un solo artefacto de deploy (simplicidad, driver #4), sin dependencia de CDN (funciona
aunque la red externa falle) y arranque instantáneo.
**Trade-off:** recompilar para cambiar el front. Aceptado a esta escala.

### ADR-006 — Alertas por webhook, no email en v1
**Contexto:** el valor diferenciador son las alertas ("avísame si el dólar cruza X"). Email/SMS
requieren SMTP o un servicio pago; webhooks no requieren infra.
**Decisión:** registrar una alerta (indicador, operador, umbral, `webhook_url`) por token; tras cada
refresco el scheduler evalúa y **dispara un POST** a las que cruzan el umbral.
**Razón:** cero infraestructura, y el público inicial (devs) ya consume webhooks (Slack, Discord,
n8n…). Email queda para una fase posterior si se justifica.
**Trade-off:** menos accesible para no-devs. Aceptado en v1.

### ADR-007 — Widgets embebibles como distribución
**Contexto:** un servicio de indicadores gana visibilidad si otros lo embeben.
**Decisión:** endpoints que devuelven un **widget** (mini-HTML por `<iframe>`) y snippets JSON+JS para
pegar el valor del dólar/UF en cualquier sitio.
**Razón:** distribución/viralidad y showcase; cada embed es publicidad del servicio.
**Trade-off:** hay que cuidar CORS y el tamaño del widget. Aceptado (ADR-010 cubre CORS).

### ADR-008 — Deploy en VibeNest con Dockerfile propio e imagen mínima
**Contexto:** VibeNest autodetecta con Nixpacks, pero para Go conviene controlar el build y producir
una imagen mínima.
**Decisión:** **Dockerfile multi-stage** (build en `golang`, runtime en `scratch`/`distroless` con el
binario estático); Postgres gestionado por VibeNest vía `DATABASE_URL`.
**Razón:** imagen de pocos MB, arranque instantáneo, control total del build; la BD gestionada evita
administrar Postgres.
**Trade-off:** mantener un Dockerfile. Aceptado: es corto y estable. **Nota:** al ser un solo
`DATABASE_URL`, no hay bases separadas — todo el esquema vive en una BD.

### ADR-009 — Configuración por variables de entorno (12-factor)
**Decisión:** toda la config (`DATABASE_URL`, `PORT`, fuente activa, intervalo de refresco) viene de
**ENV**; sin archivos de secretos en el repo.
**Razón:** PaaS-friendly (VibeNest inyecta ENV), portable y sin secretos versionados.
**Trade-off:** ninguno relevante.

### ADR-010 — API pública: CORS abierto + rate limiting básico
**Contexto:** la API y los widgets se consumen desde browsers de terceros (es el producto), pero una
API pública sin límites invita al abuso.
**Decisión:** **CORS abierto** para los endpoints de lectura + **rate limiting** por IP (token bucket
en memoria) para acotar ráfagas.
**Razón:** consumible desde el navegador y protegida de abuso, sin infra extra.
**Trade-off:** el rate limit en memoria no es distribuido. Aceptado: hay una sola instancia.

### ADR-011 — Cadencia por indicador en el catálogo; el scheduler sondea diario pero la interpreta
**Contexto:** no todos los indicadores cambian a diario (`docs/CASES.md`, CASE-001): dólar y UF varían
en días hábiles, mientras **UTM e IPC son mensuales**. Tratar todo como "diario" es una mentira del
dominio: confunde *"sin cambios (esperado)"* con *"fallo"* en `sync_runs`, y falsea la edad esperada
del dato para alertas y métricas de frescura.
**Decisión:** el catálogo `indicators` lleva un campo **`cadence`** (`daily` | `monthly`, enum
extensible). El scheduler mantiene **un único ticker diario** (simple, idempotente por código+fecha —
ADR-003), pero **usa `cadence` para interpretar** el resultado: para un indicador mensual, "hoy no
hubo valor nuevo" es lo esperado, no un hueco; la frescura y las alertas calculan la antigüedad
aceptable según su cadencia.
**Razón:** dominio honesto sin multiplicar schedulers (un solo ticker → simplicidad, driver #4);
`sync_runs`/métricas distinguen *esperado-sin-cambio* de *fallo* (resiliencia, driver #3).
**Trade-off:** una columna y un enum más que mantener, y se sigue sondeando a diario incluso a los
mensuales (barato e idempotente). Aceptado. Supera la parte de diseño de `AUD-001`, que queda como
verificación con datos reales.

---

## 6. Modelo de datos

- **`indicators`** — catálogo: `code` (uf, dolar, utm, ipc…), `name`, `unit`, `description`,
  **`cadence`** (`daily`/`monthly`, ADR-011: el scheduler la usa para distinguir "sin cambios" de "fallo").
- **`indicator_values`** — histórico: `indicator_code`, `date`, `value`, `fetched_at`. Único
  `(indicator_code, date)`: cada refresco agrega el valor del día, nunca pisa el histórico.
- **`alerts`** — `id`, `token`, `indicator_code`, `operator` (`gt`/`lt`), `threshold`, `webhook_url`,
  `active`, `last_triggered_at`.
- **`sync_runs`** (auditoría, espejo de Oteo) — `source`, `started_at`, `finished_at`, `status`,
  `indicators_updated`, `error`. El tablero de salud de la fuente.

Índices sobre `(indicator_code, date desc)` para el último valor y el histórico. A esta escala (miles
de filas al año) no hay particionado.

---

## 7. Flujo de datos end-to-end

1. **Refresco:** el scheduler (al arrancar y luego 1×/día) llama a `IndicatorSource.Fetch()`.
2. **Normalización:** el adapter emite `[]Snapshot` (código, valor, fecha, unidad); el scheduler hace
   **upsert** en `indicator_values` y cierra un `sync_run` con sus contadores.
3. **Alertas:** tras el refresco, evalúa las alertas activas y dispara webhooks a las que cruzan umbral.
4. **Lectura (API):** `GET /api/:code` y `GET /api/:code/history?desde=…` leen de Postgres con cache en
   memoria por delante (TTL corto); nunca tocan la fuente.
5. **Dashboard/widgets:** el binario sirve el HTML + Chart.js, que consumen la misma API.

---

## 8. Resiliencia, seguridad y observabilidad

**Resiliencia**
- La API sirve desde datos persistidos: si la fuente cae, Faro **sigue respondiendo** con el último
  valor (driver #3). El `sync_run` registra el fallo.
- Refresco idempotente (upsert por código+fecha): re-ejecutar no duplica.
- Timeouts y reintento con backoff en el adapter; el scheduler nunca tumba el server.

**Seguridad**
- API pública de solo lectura + CORS abierto (ADR-010); rate limiting por IP.
- Alertas por token opaco (no adivinable); `webhook_url` validada (esquema http/https, no loopback/SSRF).
- Config y secretos por ENV (ADR-009); nada en el repo.

**Observabilidad**
- `sync_runs` como tablero primario (última corrida, errores). Un endpoint interno `/healthz`
  (readiness) y `/metrics` básico (última actualización por indicador, edad del dato).
- Logs estructurados (slog de la stdlib).

---

## 9. Performance y escalabilidad (NFRs)

| Métrica | Objetivo |
|---|---|
| Latencia de la API (cacheada) | p95 < 100 ms |
| Huella de memoria en operación | RSS < 100 MB (cabe holgado en 256 MB) |
| Refresco diario completo | < 5 s |
| Arranque del binario | < 1 s |
| Disponibilidad del dato ante caída de la fuente | 100% (sirve el último persistido) |

**Tácticas:** cache en memoria por delante de Postgres; binario estático mínimo; assets embebidos
(sin round-trips a CDN); el dato se calcula 1×/día, no por request.

---

## 10. Estrategia de testing

| Nivel | Herramienta | Qué cubre |
|---|---|---|
| Unit | `testing` (stdlib) | mapeo del adapter (JSON CMF → Snapshot), lógica de evaluación de alertas |
| Adapter | `net/http/httptest` | `CMF` contra respuestas grabadas: parseo, errores, timeouts, API key. Cero red real en CI |
| Integración | `testing` + Postgres de test | endpoints de la API, upsert idempotente, histórico |
| Handlers | `httptest.Server` | CORS, rate limiting, formato JSON |

**Reglas de oro:** el adapter jamás llama a la CMF (ni a la fuente real) en CI (respuestas grabadas, espejo del
WebMock de Oteo); la idempotencia del refresco es un test, no una esperanza.

---

## 11. CI/CD e infraestructura

```
go vet → staticcheck → go test ./... → go build → docker build → deploy VibeNest
```
- GitHub Actions con servicio Postgres para los tests de integración.
- Imagen Docker multi-stage (build en `golang`, runtime `scratch`/`distroless`).
- Deploy a VibeNest (git-based o `docker push`); Postgres gestionado vía `DATABASE_URL`.
- Un ambiente (producción). Sin staging: lo cubren los tests y que el binario es trivial de rollback.

---

## 12. Riesgos y mitigaciones

| Riesgo | Impacto | Mitigación |
|---|---|---|
| **[GATE ✓] Licencia/ToS de la fuente** | Alto | **Resuelto en Fase 0** (MANIFIESTO §4): se descartó mindicador.cl como fuente v1 por no tener ToS explícita; se adopta la **API pública oficial de la CMF** (uso por API key, dato del regulador). Queda el gate menor de obtener la key (§13). Atribuir la fuente en dashboard y docs |
| La fuente (CMF) cae o cambia formato | Medio | Adapter aísla la fuente (ADR-002); cache persistente sirve el último valor (ADR-003); **mindicador.cl como fallback**. La CMF es oficial y estable, menor probabilidad que un agregador |
| Registro de API key de la CMF bloqueante | Bajo | El formulario pide nombre/RUT/email (obligatorios); el autor tiene RUT chileno → no bloquea. Si lo fuera (demora/rechazo), **mindicador.cl es el fallback inmediato** (el adapter lo hace barato) |
| Free tier 256 MB / trial 3 meses | Medio | Go es liviano (RSS < 100 MB); todo migrable (un binario + Postgres); sin lock-in duro |
| Abuso de la API pública | Medio | Rate limiting por IP + CORS acotado (ADR-010) |
| SSRF vía `webhook_url` de alertas | Medio | Validar esquema y bloquear loopback/redes privadas antes de disparar |
| Discrepancia entre fuentes (CMF vs. fallback) | Bajo | La CMF oficial manda; el fallback solo cubre caídas. Documentar la fuente y su latencia |
| Proyecto se construye pero no se muestra | Medio | El roadmap termina en un deploy público en Fase 2; el objetivo es una URL viva |

---

## 13. Roadmap por fases

**Fase 0 — Cimientos. Los *gates* primero** (MANIFIESTO §4: 30 minutos que evitan construir sobre
arena). **Gate legal ✓:** resuelto — fuente v1 = **CMF oficial** (ToS clara vía API key), mindicador
como fallback (ADR-002). **Gate de API key ✓:** obtenida por el formulario de la CMF
(`https://api.sbif.cl/api/contactanos.jsp`; pide nombre/RUT/email) y **verificada con smoke test**
(`scripts/smoke-cmf.sh`): UF/dólar/UTM/IPC responden JSON válido. **Gate de viabilidad ✓:** Go instalable sin sudo (tarball
`linux-amd64` a `~/.local`) y free tier VibeNest — confirmado. Recién con los gates verdes: `go mod
init`, estructura de paquetes, Postgres + migraciones, adapter `CMF` con tests (httptest, respuestas
grabadas), CI (vet/staticcheck/test). Suite verde.

**Fase 1 — Núcleo (API + refresco).** Scheduler de refresco diario + persistencia con histórico +
`sync_runs`; API `GET /api/:code` (valor actual) y `/api/:code/history` + cache en memoria. Primer
refresco real desde la CMF y verificación de los valores.

**Fase 2 — Dashboard + deploy.** HTML + Chart.js embebido (valores de hoy + gráficos de tendencia),
`/healthz`. **Dockerfile y primer deploy a VibeNest** → URL pública viva.

**Fase 3 — Distribución.** Alertas por webhook (registro por token, evaluación tras refresco, disparo)
+ widgets embebibles (`<iframe>`/snippet) + rate limiting + CORS.

**Fase 4 — Robustez (*solo con tracción*).** Reservada para señal real de uso (MANIFIESTO §4: la
última fase no se construye a futuro). Implementar **mindicador.cl como fallback** de la CMF (y/o
**BCCh** como tercera fuente); docs de API pública (OpenAPI); métricas. Email en alertas solo si se
justifica.

Cada fase entrega algo usable. La Fase 2 termina con Faro en vivo en una URL.

---

## 14. Documentación viva

El sistema de documentos de El Método (MANIFIESTO §2): cada documento responde **una** pregunta;
juntos evitan que una sesión (propia o IA) pierda contexto. Por **proporcionalidad** (§2.1) no todos
existen desde el día 1 — aparecen cuando la fase que los estrena llega.

| Documento | Responde a | Cuándo aparece en Faro |
|---|---|---|
| **`SAD-Faro.md`** | ¿Cómo está diseñado y por qué? — **fuente de verdad** | Existe. Cambia solo por ADR nuevo o enmienda versionada |
| **ADRs** (dentro del SAD, §5) | ¿Qué decidí y qué sacrifiqué? | Existe. Al tomar una decisión de arquitectura |
| **`CLAUDE.md`** (raíz) | ¿Cómo retomo el proyecto en 5 min? | Existe. Al cerrar cada sesión que cambió el estado |
| **`docs/AUDIT.md`** (`AUD-NNN`) | ¿Qué deuda acepté y cómo se paga? | **Fase 0** (día 1) |
| **`docs/CASES.md`** | ¿Qué casos raros del dominio encontré? | **Fase 0** (día 1); se llena con datos reales en Fase 1 (cadencia mensual, días no hábiles, valores nulos) |
| **`docs/TROUBLESHOOTING.md`** | ¿Qué falló y cómo se arregló? | **Fase 0** (día 1) |
| **`docs/DEPLOY.md`** | ¿Cómo se pone en producción? | **Fase 2**, con el primer deploy a VibeNest |
| **`docs/SECURITY.md`** | ¿De qué me protejo y cómo? (proporcional) | **Fase 3**, con alertas/webhooks (SSRF) y rate limiting; hasta entonces la postura vive en §8 |

### Cierre de fase — Definition of Done (obligatorio, en orden)
Los 7 pasos de El Método (MANIFIESTO §4). Ninguna fase se cierra sin completarlos:
1. **Ronda crítica (vista de halcón)** — releer el código de la fase cazando bugs y bordes; corregir
   los de riesgo real, diferir el resto como `AUD-NNN`.
2. **Casos borde → `CASES.md`** (sobre todo tras datos reales).
3. **Deuda → `AUDIT.md`** — todo trade-off aceptado obtiene su `AUD-NNN`.
4. **Incidentes → `TROUBLESHOOTING.md`** — toda falla resuelta durante la fase.
5. **Contexto → `CLAUDE.md` + `README`** en sincronía con el resto.
6. **Verde** — `go vet` + `staticcheck` + `go test ./...` limpios.
7. **Commit + push.**

El SAD solo cambia por ADR nuevo o enmienda versionada (§16). **Retro del método:** al cerrar un
proyecto/fase mayor, preguntar también *"¿en qué falló el método?"* y realimentar el MANIFIESTO
(MANIFIESTO §5). El repo de referencia vivo hoy es Oteo/FleetPilot.

---

## 15. Glosario rápido
- **Indicador:** valor económico de referencia (dólar, UF, UTM, IPC…).
- **Adapter / `IndicatorSource`:** interfaz que aísla la fuente de datos del dominio (ADR-002).
- **Snapshot:** registro normalizado que emite el adapter; el dominio no ve el JSON de la fuente.
- **UF:** Unidad de Fomento (unidad reajustable chilena). **UTM:** Unidad Tributaria Mensual.
- **Refresco:** traer y persistir los valores del día desde la fuente (1×/día).

---

## 16. Historial de revisiones

| Versión | Fecha | Cambios |
|---|---|---|
| 1.2.0 | 2026-07-08 | **Enmienda de ADR-002 — fuente oficial primero.** Tras revisar el gate legal de Fase 0, se **invierte el orden de fuentes**: la **API pública oficial de la CMF** pasa a ser la fuente v1 (API key gratuita vía ENV, ToS clara, cobertura exacta: dólar/euro/IPC/UF/UTM) y **mindicador.cl** baja a **fallback**. Motivo: mindicador no publica ToS, scrapea al BCCh y no tiene SLA (se cayó durante la revisión). El adapter (ADR-002) hace el cambio barato: el dominio no se toca. Ripios: §3 (restricción de fuente), §4.1/§4.2 (diagrama y prosa), §10 (tests del adapter `CMF`), §12 (gate legal ✓ + riesgos de fuente reordenados), §13 (Fase 0: gate de API key; Fase 4: fallback en vez de "subir a CMF"). Elimina la deuda AUD-002 (republicar sin permiso) de raíz. Confirma de paso la cadencia del ADR-011 con dato oficial: IPC/UF/UTM mensuales ~día 9, dólar/euro días hábiles. |
| 1.1.0 | 2026-07-08 | **Enmienda de alineación con El Método (MANIFIESTO v1.0.0) + ADR-011.** Se agrega: referencia a El Método como doctrina gobernante (header); §2.1 con el principio de proporcionalidad y la dosis de rigor declarada de Faro; sistema completo de 8 documentos con su "cuándo aparece" (nuevos: CASES/DEPLOY/SECURITY, proporcionales por fase) y DoD elevado de 6 a 7 pasos (nuevo paso *Casos borde → CASES.md*) más retro del método (§14); **gates primero** en Fase 0 y gate legal de licencia/ToS de mindicador.cl (§12–§13); Fase 4 marcada *solo con tracción*; supuesto de cadencia corregido (UTM/IPC son mensuales, no diarios) (§3). **ADR-011** nuevo: cadencia por indicador en el catálogo (`cadence` en `indicators`, §6) — el scheduler sondea diario pero interpreta según cadencia; supera la parte de diseño de AUD-001. Los ADR 001–010 se mantienen sin cambios. |
| 1.0.0 | 2026-07-08 | Baseline. Go + stdlib (ADR-001), fuente tras adapter con mindicador.cl primero y CMF después (ADR-002), refresco+cache sin llamar a la fuente en la request (ADR-003), histórico en Postgres con sqlc (ADR-004), dashboard con go:embed + Chart.js (ADR-005), alertas por webhook (ADR-006), widgets embebibles (ADR-007), deploy VibeNest con Dockerfile mínimo (ADR-008), config por ENV (ADR-009), CORS + rate limiting (ADR-010). Roadmap de 5 fases orientado a una URL pública viva. |

---

*Fin del documento. SAD vivo: cada decisión futura se agrega como un ADR nuevo con su contexto y
trade-offs. Una arquitectura no se documenta una vez; se mantiene.*

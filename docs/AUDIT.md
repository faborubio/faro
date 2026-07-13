# AUDIT — Deuda técnica de Faro

> Responde a: **¿qué deuda acepté y cómo se paga?** (El Método §2). Todo trade-off aceptado que
> implique trabajo futuro vive aquí con su `AUD-NNN` — un trade-off sin entrada es deuda invisible,
> la peor clase. Se actualiza al aceptar o pagar un atajo, y en el DoD de cada cierre de fase.

Formato de cada entrada: **estado** (abierta / pagada), **contexto**, **atajo aceptado**, **plan de
pago** (cómo y cuándo se salda).

---

## AUD-001 — Verificar la cadencia real de los indicadores con datos en vivo
- **Estado:** **pagada** (Fase 1, 2026-07-09). **Hallazgo:** el primer refresco real confirmó el
  modelo de ADR-011 sin ajustes — `dolar` y `uf` llegaron con fecha del día (2026-07-09), `utm`
  con un valor mensual fechado al 1° del mes (2026-07-01) e `ipc` ídem (2026-06-01: el IPC de un
  mes se publica al mes siguiente). El segundo refresco del día cerró `ok` con 0 actualizados —
  `sync_runs` distingue "sin cambios" de "fallo" (ver CASE-002). Sorpresa documentada aparte: el
  IPC puede valer 0,0% legítimamente (CASE-005). El enum `daily`/`monthly` queda como está.
- El **diseño** ya estaba decidido en **ADR-011** (campo `cadence` en el catálogo); esta entrada
  cubría solo la **verificación empírica**.
- **Contexto:** ADR-011 modela `daily`/`monthly` y la CMF confirma la cadencia oficial (dólar/euro
  días hábiles; IPC/UF/UTM mensual ~día 9, CASE-001), pero el comportamiento exacto de la **CMF**
  (fuente v1) ante días no hábiles y valores no publicados aún no está verificado con datos reales
  (SAD §3, `docs/CASES.md` CASE-002).
- **Atajo aceptado:** confiar en que el upsert idempotente (código+fecha) + `cadence` (ADR-011)
  absorben el sondeo diario de valores mensuales, antes de confirmarlo con datos reales.
- **Plan de pago:** en Fase 1, tras el primer refresco real, contrastar contra `CASES.md`, confirmar
  que `sync_runs` distingue "sin cambios" de "fallo", y ajustar valores del enum/umbrales de frescura
  si aparece una discrepancia. Cerrar esta entrada con el hallazgo.

---

## AUD-002 — Migraciones solo aplicables con script local (psql)
- **Estado:** **pagada** (Fase 2, 2026-07-09). **Cómo:** el candidato natural del plan, tal cual —
  `//go:embed` de `migrations/*.sql` (paquete `migrations`) + `internal/migrate` que las aplica al
  boot de `cmd/faro`, con el mismo contrato `schema_migrations` del script (versión = nombre de
  archivo, cada archivo en una transacción): ambos caminos son intercambiables, verificado
  arrancando el binario contra la BD dev migrada por el script (0 re-aplicadas). Advisory lock para
  boots solapados (verificado con doble boot simultáneo sobre BD vacía: una instancia migra, la
  otra espera y salta). `scripts/migrate.sh` sigue vigente para migrar a mano. Mecanismo
  documentado en `docs/DEPLOY.md` (nació con el Dockerfile, Fase 2 paso 3).
- **Contexto:** las migraciones corren con `scripts/migrate.sh` (bash + psql) contra `DATABASE_URL`.
  Funciona para desarrollo, pero el deploy en VibeNest (ADR-008) necesita un mecanismo que corra
  donde no hay psql ni shell garantizados.
- **Atajo aceptado:** en Fase 0–1 no hay producción, así que el script basta; se pospone la decisión
  del mecanismo de prod (embeber migraciones en el binario y aplicarlas al boot, o un job de deploy).
- **Plan de pago:** decidir e implementar en **Fase 2** junto con el Dockerfile, y documentarlo en
  `docs/DEPLOY.md` (que nace en esa fase). Candidato natural: `//go:embed` de `migrations/` +
  aplicación al arrancar (idempotente vía `schema_migrations`, mismo contrato del script).
- **Contexto:** las migraciones corren con `scripts/migrate.sh` (bash + psql) contra `DATABASE_URL`.
  Funciona para desarrollo, pero el deploy en VibeNest (ADR-008) necesita un mecanismo que corra
  donde no hay psql ni shell garantizados.
- **Atajo aceptado:** en Fase 0–1 no hay producción, así que el script basta; se pospone la decisión
  del mecanismo de prod (embeber migraciones en el binario y aplicarlas al boot, o un job de deploy).
- **Plan de pago:** decidir e implementar en **Fase 2** junto con el Dockerfile, y documentarlo en
  `docs/DEPLOY.md` (que nace en esa fase). Candidato natural: `//go:embed` de `migrations/` +
  aplicación al arrancar (idempotente vía `schema_migrations`, mismo contrato del script).

## AUD-003 — Respuesta de la CMF limitada a 1 MB en el adapter
- **Estado:** **pagada** (Fase 2, 2026-07-09). **Hallazgo:** medidas las series anuales reales
  (CASE-006): el año más pesado (`uf/2025`, 365 entradas) son 25 KB — 2,5% del límite. El backfill
  entró (año actual + anterior por indicador vacío, `internal/refresh.Backfill` + `cmf.FetchYear`)
  **sin tocar el límite ni paginar**: 1 MB da holgura de 40× sobre lo observado. Verificado contra
  BD vacía real: 973 snapshots en un run `cmf/backfill` cerrado 'ok'.
- **Contexto:** `doOnce` lee el cuerpo con `io.LimitReader(1<<20)`. Para el valor vigente sobra
  (≈100 bytes), pero el backfill de histórico (`/uf/2025`, series anuales) puede acercarse
  al límite o requerir paginación.
- **Atajo aceptado:** límite fijo simple mientras el adapter solo trae valores vigentes.
  **Nota (cierre Fase 1):** el backfill quedó **fuera** de la Fase 1 — el histórico se acumula
  refresco a refresco desde el deploy. El backfill entra cuando el dashboard (Fase 2) necesite
  series hacia atrás para los gráficos.
- **Plan de pago:** al implementar el backfill histórico, medir el tamaño real de las
  series anuales y subir el límite o paginar por año según evidencia (CASES primero).

## AUD-004 — sync_runs huérfanos en 'running' ante un crash duro
- **Estado:** **pagada** (Fase 2, 2026-07-10, cierre). **Decisión:** barrido on-boot con umbral —
  `SweepOrphanSyncRuns` cierra como 'error' los runs en 'running' con más de **1 hora** (query
  sqlc + llamada al inicio de `Run`). El umbral protege a una instancia vieja legítimamente a
  mitad de ciclo durante un rolling update; la evidencia real que lo calibró: el ciclo más largo
  observado fue ~8 min (backfill con egress roto, T-004) — 1 h da margen de 7×. Con test de
  integración (huérfano viejo barrido; run reciente intacto).
- **Contexto:** un `sync_run` se abre en `'running'` y se cierra al final del ciclo. El apagado
  ordenado está cubierto (SIGTERM en pleno refresco cierra el run igual, vía
  `context.WithoutCancel` — con test). Pero un **crash duro** (OOM del free tier, kill -9, caída
  de la máquina) deja el run en `'running'` para siempre.
- **Atajo aceptado:** sin barrido al boot; a esta escala un run huérfano es ruido cosmético en el
  tablero de salud, no corrompe datos (el próximo ciclo abre su propio run y el upsert es
  idempotente).
- **Plan de pago:** en Fase 2 (primer deploy real, donde el OOM es plausible), decidir: barrido
  on-boot (`UPDATE sync_runs SET status='error', error='huérfano' WHERE status='running'`) o
  umbral de frescura en el tablero. Calibrar con evidencia de `sync_runs` reales.

## AUD-005 — El refresco automático en producción depende del fix de egress de VibeNest (T-004)
- **Estado:** abierta (bloqueada por plataforma; ticket enviado 2026-07-10).
- **Contexto:** el egress TCP de la red de contenedores de VibeNest está roto (T-004): el
  scheduler no alcanza a la CMF y cada run diario queda en 'error'. La URL pública vive con
  datos sembrados por la consola SQL del panel (workaround en `docs/DEPLOY.md`).
- **Atajo aceptado:** los datos envejecen 1 día por día; el seed manual (abrir consola → pegar
  dump idempotente) los refresca cuando haga falta. Aceptable para una pieza de portafolio
  mientras el ticket avanza; inaceptable como estado final.
- **Plan de pago:** cuando VibeNest arregle el egress, verificar en logs el primer `refresco ok`
  automático y en `sync_runs` la vuelta a la normalidad; cerrar esta entrada con esa evidencia.
  Si el ticket muere sin fix: evaluar mover el refresco fuera del contenedor (GitHub Actions
  cron contra la BD, aceptando exponer el Postgres con TLS) o cambiar de plataforma.
- **Nota (Fase 3, 2026-07-10):** el egress roto ahora bloquea **dos** cosas en prod: el refresco
  desde la CMF **y el disparo de webhooks de alertas** (mismo camino de salida). Las alertas
  registradas en prod quedan latentes hasta el fix; localmente el ciclo completo está verificado
  E2E (cruce real → POST entregado).
- **Re-diagnóstico (2026-07-13, respuesta de soporte — T-004 actualizado):** NO es egress
  general: el contenedor y el host salen a internet sin problema; **solo las IPs de la CMF hacen
  timeout desde ese host** (filtro de ruta o de IP de origen; la CMF responde desde Núremberg
  AS24940 y desde EE.UU., así que no es ASN ni geo). Dos consecuencias:
  1. Los **webhooks probablemente sí funcionan en prod** (destinos ≠ CMF) — confirmar con el
     primer cruce real cuando el refresco vuelva, y recién entonces cerrar esta entrada completa.
  2. Si el filtro resulta ser de la CMF contra la IP del host y no se destraba: el plan de pago
     cambia de "esperar plataforma" a **adelantar el fallback mindicador.cl** (ADR-002/Fase 4 —
     este escenario es exactamente para lo que se diseñó) o pedir a VibeNest un cambio de IP.
     El plan B del refresco externo (GitHub Actions cron) sigue viable: la CMF responde desde
     infra de EE.UU. (HTTP 422 sin key, 2026-07-13).
- **Plan A dejado listo (2026-07-13):** `CMF_BASE_URL` opcional en el binario + Worker de
  Cloudflare (`scripts/cmf-proxy-worker.js`, receta en `docs/DEPLOY.md`) — re-apunta el adapter
  a un proxy propio que sí alcanza la CMF, y el scheduler completo (refresco + backfill +
  alertas) vuelve a operar en prod sin exponer la BD ni tocar código. Sin la variable no cambia
  nada. Activarlo es decisión operativa (requiere deployar Fase 3, hoy prod corre Fase 2);
  esta entrada se cierra igual solo con el `refresco ok` — directo o vía proxy.

## AUD-006 — Webhooks sin reintentos ni auto-desactivación de receptores muertos
- **Estado:** abierta (aceptada en Fase 3, 2026-07-10).
- **Contexto:** el disparo de una alerta es un único POST con timeout de 10 s (ADR-006). Si el
  receptor está caído justo en el cruce, la notificación se pierde: no hay cola ni reintento
  (`last_triggered_at` no se marca y el log deja `webhook no entregado`). Y una alerta cuyo
  receptor murió para siempre se seguirá intentando en cada cruce futuro — la columna `active`
  existe para auto-desactivarlas, pero nada la apaga todavía.
- **Atajo aceptado:** a esta escala un cruce es un evento raro (días o semanas entre disparos) y
  el costo de perder uno es bajo; una cola de reintentos es infraestructura que el volumen no
  justifica (proporcionalidad, SAD §2.1). El POST fallido queda observable en el log.
- **Plan de pago:** en Fase 4 (solo con tracción): reintento simple con backoff (2–3 intentos) y
  auto-desactivación tras N fallas consecutivas (columna `active`, que ya filtra el índice y la
  evaluación). Calibrar N con evidencia real de fallas en el log — CASES primero (regla 3).

---

*Cada atajo nuevo entra aquí antes de darse por aceptado.*

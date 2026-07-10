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
  otra espera y salta). `scripts/migrate.sh` sigue vigente para migrar a mano. Queda para el paso
  de deploy: documentar el mecanismo en `docs/DEPLOY.md` cuando ese archivo nazca.
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
- **Estado:** abierta (revisar cuando entre el backfill).
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
- **Estado:** abierta (revisar en Fase 2, con el deploy).
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

---

*Cada atajo nuevo entra aquí antes de darse por aceptado.*

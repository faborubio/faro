# AUDIT — Deuda técnica de Faro

> Responde a: **¿qué deuda acepté y cómo se paga?** (El Método §2). Todo trade-off aceptado que
> implique trabajo futuro vive aquí con su `AUD-NNN` — un trade-off sin entrada es deuda invisible,
> la peor clase. Se actualiza al aceptar o pagar un atajo, y en el DoD de cada cierre de fase.

Formato de cada entrada: **estado** (abierta / pagada), **contexto**, **atajo aceptado**, **plan de
pago** (cómo y cuándo se salda).

---

## AUD-001 — Verificar la cadencia real de los indicadores con datos en vivo
- **Estado:** abierta (se resuelve en Fase 1). El **diseño** ya está decidido en **ADR-011**
  (campo `cadence` en el catálogo); esta entrada cubre solo la **verificación empírica**.
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
- **Estado:** abierta (se paga en Fase 2, con el deploy).
- **Contexto:** las migraciones corren con `scripts/migrate.sh` (bash + psql) contra `DATABASE_URL`.
  Funciona para desarrollo, pero el deploy en VibeNest (ADR-008) necesita un mecanismo que corra
  donde no hay psql ni shell garantizados.
- **Atajo aceptado:** en Fase 0–1 no hay producción, así que el script basta; se pospone la decisión
  del mecanismo de prod (embeber migraciones en el binario y aplicarlas al boot, o un job de deploy).
- **Plan de pago:** decidir e implementar en **Fase 2** junto con el Dockerfile, y documentarlo en
  `docs/DEPLOY.md` (que nace en esa fase). Candidato natural: `//go:embed` de `migrations/` +
  aplicación al arrancar (idempotente vía `schema_migrations`, mismo contrato del script).

## AUD-003 — Respuesta de la CMF limitada a 1 MB en el adapter
- **Estado:** abierta (revisar en Fase 1).
- **Contexto:** `doOnce` lee el cuerpo con `io.LimitReader(1<<20)`. Para el valor vigente sobra
  (≈100 bytes), pero el backfill de histórico (Fase 1: `/uf/2025`, series anuales) puede acercarse
  al límite o requerir paginación.
- **Atajo aceptado:** límite fijo simple mientras el adapter solo trae valores vigentes.
- **Plan de pago:** al implementar el backfill histórico en Fase 1, medir el tamaño real de las
  series anuales y subir el límite o paginar por año según evidencia (CASES primero).

---

*Cada atajo nuevo entra aquí antes de darse por aceptado.*

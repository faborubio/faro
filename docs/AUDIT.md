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

*Sin más deuda abierta. Cada atajo nuevo entra aquí antes de darse por aceptado.*

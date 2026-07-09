# CASES — Casos del dominio de Faro

> Responde a: **¿qué casos raros del dominio encontré?** (El Método §2). Regla invariante: **antes de
> tocar una heurística/lista/config, el caso real se documenta aquí** — la config se calibra con
> evidencia, no con intuición. Se llena sobre todo con datos reales (Fase 1 en adelante).

Formato de cada caso: **síntoma observado**, **por qué pasa**, **cómo lo trata Faro**.

---

## CASE-001 — No todos los indicadores cambian a diario
- **Síntoma:** el refresco es diario, pero varios indicadores no producen un valor nuevo cada día.
- **Por qué pasa:** cadencias distintas por naturaleza del indicador. **Cadencia oficial de la CMF
  (fuente v1, ADR-002):** *"Dólar y Euro se actualizan diariamente de lunes a viernes excepto
  feriados; IPC, UF y UTM se actualizan una vez al mes alrededor del día 9."*
  - **Dólar / Euro:** días hábiles (L–V, salvo feriados).
  - **UF:** tiene un valor **cada día**, pero el conjunto del mes se **fija/publica una vez al mes**
    (~día 9): la UF diaria se pre-calcula desde el IPC del mes. Matiz: valor diario, publicación mensual.
  - **UTM / IPC:** **mensuales** — un valor por mes (~día 9).
- **Cómo lo trata Faro:** modelado en **ADR-011** — el catálogo lleva `cadence` (`daily`/`monthly`) y
  el scheduler sondea a diario pero la interpreta. El refresco es idempotente (upsert por
  `indicator_code`+`date`); sondear a diario un valor mensual **confirma**, no duplica. El histórico
  refleja la cadencia real de cada uno. Verificación con datos reales pendiente → ver `AUD-001`.

## CASE-003 — La CMF entrega valores como strings en formato chileno
- **Síntoma:** el smoke test (`scripts/smoke-cmf.sh`, datos reales 2026-07-08) devuelve el valor como
  **string** con formato local chileno, no como número JSON:
  - UF → `"Valor": "40.842,07"` · Dólar → `"927,36"` · UTM → `"71.649"` · IPC → `"0,0"`.
  - Punto `.` = separador de miles; coma `,` = separador decimal.
  - Cada indicador viene en su propio arreglo: `UFs`, `Dolares`, `UTMs`, `IPCs`, con `Valor` + `Fecha`.
- **Por qué pasa:** la API de la CMF serializa en convención numérica es-CL, no en formato JSON neutro.
- **Cómo lo trata Faro:** el adapter `CMF` **normaliza** al mapear a `Snapshot`: quita los `.` de
  miles, cambia `,` por `.` decimal, y parsea a número (guardar como `numeric`/decimal, no float, para
  no perder precisión en montos). **Esto se cubre con tests de mapeo** (§10 del SAD) usando estas
  respuestas grabadas. Un cambio de formato de la fuente debe romper un test, no producir un valor
  silenciosamente mal.

## CASE-002 — Día no hábil / valor aún no publicado *(pendiente de datos reales)*
- **Síntoma esperado:** fines de semana, feriados o el corte diario antes de publicación → la fuente
  puede no traer valor nuevo (o traer el último vigente).
- **Por qué pasa:** los mercados/entidades no publican en días no hábiles.
- **Cómo lo tratará Faro:** por confirmar en Fase 1 — el `sync_run` debe distinguir "sin cambios" de
  "fallo". Documentar el comportamiento exacto observado aquí antes de ajustar el scheduler.

---

*Cada caso nuevo del dominio entra aquí **antes** de tocar heurística o configuración.*

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
- **Refinamiento (ronda crítica, cierre Fase 0):** el parseo es **estricto** (`clNumberRe`): los
  grupos de miles deben ser de exactamente 3 dígitos. Un `"12.34"` (punto decimal, formato
  internacional) **falla ruidosamente** en vez de convertirse en `1234` — la corrupción silenciosa
  era posible con el reemplazo ingenuo de puntos. Cubierto con tests negativos.

## CASE-004 — La API de la CMF publica el valor del día el mismo día
- **Síntoma observado (2026-07-09):** fixtures capturadas el 07-08 traían UF/dólar con fecha 07-08;
  la verificación e2e del día siguiente trajo UF `40.844,79` y dólar `935,71` con fecha **07-09**.
- **Por qué pasa:** la CMF publica los indicadores diarios durante la mañana del mismo día.
- **Cómo lo trata Faro:** el refresco diario (ADR-003) captura el valor vigente; la hora exacta de
  publicación de la CMF aún no está caracterizada → si el refresco corre antes de la publicación,
  traerá el del día anterior (aceptable: el dato sigue siendo el último oficial). Caracterizar la
  hora con `sync_runs` reales antes de fijar la hora del ticker.
- **Dato (2026-07-09):** a las 16:08 hora de Chile los valores del día ya estaban publicados. El
  ticker v1 corre cada `REFRESH_INTERVAL` desde el boot (sin hora fija); con el servicio deployado
  (Fase 2), `sync_runs` acumulará la evidencia para decidir si conviene anclar la hora.

## CASE-002 — Día no hábil / valor aún no publicado *(mecanismo verificado; fin de semana pendiente)*
- **Síntoma esperado:** fines de semana, feriados o el corte diario antes de publicación → la fuente
  puede no traer valor nuevo (o traer el último vigente).
- **Por qué pasa:** los mercados/entidades no publican en días no hábiles.
- **Cómo lo trata Faro (verificado 2026-07-09):** el `sync_run` **sí distingue** "sin cambios" de
  "fallo": el segundo refresco real del mismo día trajo los 4 valores ya conocidos y cerró
  `status='ok', indicators_updated=0` — el upsert afecta 0 filas cuando `(código, fecha, valor)` ya
  existe idéntico (`IS DISTINCT FROM` en la query). Ese es exactamente el comportamiento esperado
  para un fin de semana (la CMF re-sirve el último valor vigente). Queda pendiente **observar** un
  fin de semana/feriado real en `sync_runs` para confirmar que la CMF no hace algo distinto (p. ej.
  devolver vacío, que hoy cerraría el run en 'error' por "no trajo valores").

## CASE-005 — Un IPC de 0,0% es un valor legítimo, no dato faltante
- **Síntoma observado (2026-07-09):** la CMF entrega `"Valor": "0,0"` para el IPC de junio 2026; en
  la base y en la API queda `value = 0`.
- **Por qué pasa:** la variación mensual del IPC puede ser exactamente 0,0% — es un dato real, no
  una ausencia. Verificado contra la respuesta cruda de la API.
- **Cómo lo trata Faro:** `0` viaja como cualquier otro valor (parseo, NUMERIC, JSON). Regla para el
  futuro: **jamás usar 0 como centinela de "sin dato"** — la ausencia se modela con la ausencia de
  fila, y `store.ErrNotFound`/404 en la API. Las alertas (Fase 3) deben comparar contra umbrales sin
  tratar 0 como caso especial.

---

*Cada caso nuevo del dominio entra aquí **antes** de tocar heurística o configuración.*

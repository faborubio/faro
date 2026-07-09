# TROUBLESHOOTING — Incidentes de Faro

> Responde a: **¿qué falló y cómo se arregló?** (El Método §2). Cada incidente resuelto se registra
> aquí (síntoma → causa → fix); los graves, con un breve postmortem. Se actualiza en cada incidente y
> en el DoD de cierre de fase.

Formato de cada entrada: **síntoma**, **causa raíz**, **fix**, y para los graves **postmortem**
(qué lo permitió, cómo evitarlo).

---

## T-001 — mindicador.cl caído durante la revisión del gate legal (2026-07-09)
- **Síntoma:** `Internal Server Error — socket hang up` en `mindicador.cl` (home y API) durante la
  revisión de gates de Fase 0.
- **Causa raíz:** servicio comunitario sin SLA; caída del lado del proveedor.
- **Fix:** ninguno posible del lado de Faro. **Consecuencia de diseño:** la caída, sumada a la falta
  de ToS, motivó la enmienda del ADR-002 (SAD 1.2.0): CMF oficial como fuente v1, mindicador
  degradado a fallback. Un incidente externo que mejoró la arquitectura antes de escribir código.

## T-002 — URL del formulario de API key de la CMF muerta en el dominio nuevo
- **Síntoma:** `https://api.cmfchile.cl/api/contactanos.jsp` → HTTP 404.
- **Causa raíz:** la CMF (ex-SBIF) mantiene el formulario solo en el dominio legado.
- **Fix:** usar `https://api.sbif.cl/api/contactanos.jsp` (HTTP 200, verificado). Los endpoints de
  *datos* sí viven en `api.cmfchile.cl/api-sbifv3/…`. Docs corregidos (SAD §13, CLAUDE.md).
  Si el dominio legado desaparece, buscar el formulario desde la doc oficial:
  `https://api.cmfchile.cl/documentacion/index.html`.

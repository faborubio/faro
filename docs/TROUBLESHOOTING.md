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

## T-003 — Tests de integración de dos paquetes se pisan la BD compartida (2026-07-09)
- **Síntoma:** al nacer `internal/migrate` (Fase 2), `go test ./...` con `FARO_TEST_DATABASE_URL`
  falla intermitente en `internal/store` e `internal/migrate` con
  `ERROR: relation "schema_migrations" does not exist` — una tabla creada dos sentencias antes
  "desaparece".
- **Causa raíz:** `go test ./...` corre los **paquetes en paralelo**. Ambos paquetes usan la misma
  `faro_test` y cada test hace `DROP SCHEMA public CASCADE` + re-migración: un paquete borraba el
  esquema mientras el otro estaba a mitad de un Apply. En Fase 1 nunca ocurrió porque solo
  `internal/store` tocaba la BD.
- **Fix:** `internal/testdb.Acquire(t)` — helper compartido que entrega el DSN y toma un
  `pg_advisory_lock` de sesión sobre la BD hasta el fin del test (`t.Cleanup`), serializando los
  paquetes. **Regla:** todo paquete nuevo con tests de integración debe obtener el DSN vía
  `testdb.Acquire`, nunca leyendo `FARO_TEST_DATABASE_URL` directo.

## T-004 — Egress TCP bloqueado en VibeNest: el contenedor no alcanza a la CMF (2026-07-10)
- **Síntoma:** primer deploy (Fase 2, paso 4): la app vive y sirve en `faro.vibenest.net`, pero
  todo backfill/refresco muere en "error de red contra la CMF". Con timeouts por fase, la causa:
  `dial tcp 152.230.198.x:443: i/o timeout` — el TCP connect jamás se establece.
- **Cadena de diagnóstico** (en orden, cada paso descartó una hipótesis):
  1. Key viva desde Chile: 200 en 0,3 s → no hay throttle de la key.
  2. check-host.net desde Núremberg (AS24940, el mismo de Hetzner donde corre VibeNest), Los
     Ángeles y São Paulo: la CMF responde en 1–3 s con y sin key → no hay geobloqueo ni bloqueo
     de datacenter (reportes: `check-host.net/check-report/44141e5fk428` y `…/44149a71k413`).
  3. El DNS del contenedor resuelve (Docker DNS delega en el host) y el dial muere por timeout →
     el egress TCP de la red de contenedores (Coolify) está roto: NAT/masquerade ausente o
     firewall del host.
- **Fix:** del lado de la plataforma (ticket a soporte de VibeNest con la evidencia). Faro degrada
  como fue diseñado: API y dashboard siguen sirviendo y cada fallo queda auditado en `sync_runs`
  (ADR-003, SAD §8). **Workaround (2026-07-10):** BD sembrada por la consola SQL del panel con un
  dump idempotente local (detalle en `docs/DEPLOY.md`) — la URL quedó viva con datos reales sin
  exponer puertos; el dato envejece hasta que el egress funcione y el scheduler retome.
- **Lo que el incidente mejoró del código:** (a) el error de red ahora propaga su causa interna
  sin exponer jamás la key (`278f541`); (b) timeouts por fase en el transport — dial/TLS/headers —
  porque el "Client.Timeout exceeded while awaiting headers" de Go no distingue la fase del cuelgue
  (`79e928b`). Regla: **un adapter debe fallar diciendo la fase**; la censura de secretos se hace
  quirúrgica (la causa interna del `*url.Error` es segura), no total.
- **Actualización (2026-07-13) — respuesta de soporte; el diagnóstico original queda REFUTADO en
  su causa.** Nikita (VibeNest) reprodujo el timeout **desde el host de Hetzner mismo**: otros
  endpoints HTTPS responden, pero **ambas IPs de la CMF hacen timeout** — el contenedor sí sale a
  internet en general. No es NAT de Docker ni Traefik (que solo maneja tráfico entrante): es un
  filtro de ruta o de IP de origen hacia ese destino puntual. Soporte investiga con el proveedor;
  pidió **no redeployar todavía**. Evidencia triangulada:
  - Núremberg AS24940 (mismo ASN de Hetzner, nodo de check-host) alcanzaba la CMF el 07-10 → no
    es bloqueo por ASN/datacenter.
  - Infra de EE.UU. (2026-07-13): la CMF responde **HTTP 422** (respuesta de aplicación — falta
    la key en la URL) → el host es alcanzable desde el extranjero, no es geobloqueo.
  - El host concreto de VibeNest: timeout solo hacia la CMF → el filtro apunta a **su IP/subred
    específica** (típico de WAF anti-bot con listas de IPs), o a una ruta rota host↔CMF.
- **Implicación importante para Fase 3:** como el egress general del contenedor funciona, los
  **webhooks de alertas probablemente SÍ salen en prod** (van a receptores arbitrarios, no a la
  CMF). Sin refresco no hay valores nuevos → no hay cruces → no se puede confirmar con un disparo
  real hasta que la CMF sea alcanzable; queda "por confirmar con el primer cruce real".

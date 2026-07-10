# DEPLOY — Cómo se construye y despliega Faro

> Responde a: **¿cómo llega Faro a producción?** (El Método §2). Imagen, configuración y el paso a
> paso de VibeNest. Se actualiza cuando cambia el mecanismo de deploy.

## La imagen (ADR-008)

Multi-stage: build en `golang:1.26-alpine`, runtime en `scratch`. **Todo viaja dentro del binario**
(migraciones, dashboard, Chart.js — `go:embed`): la imagen final son ~19 MB — el binario estático
más los certificados CA (necesarios para HTTPS contra la CMF).

```sh
docker build -t faro .
docker run -p 8080:8080 -e DATABASE_URL=… -e CMF_API_KEY=… faro
```

El contexto de build excluye `.env` (`.dockerignore`): los secretos jamás entran a una imagen.
El contenedor corre como usuario no-root (65534).

## Migraciones (AUD-002 — pagada)

**No hay paso de migración en el deploy.** El binario aplica las migraciones embebidas al boot
(`internal/migrate`), con el mismo contrato `schema_migrations` que `scripts/migrate.sh`: ambos
caminos son intercambiables. Dos réplicas arrancando a la vez se serializan con un advisory lock.
Un deploy nuevo = push de imagen; el esquema se pone al día solo.

## Primer arranque

Con la BD vacía, el boot backfillea el histórico (año actual + anterior por indicador,
`sync_run` con source `cmf/backfill`, ~1 min contra la CMF real) y luego refresca a diario.
El server HTTP sirve desde el primer segundo; las tarjetas dicen "aún sin datos" hasta que
el backfill termina.

## Configuración (ADR-009 — todo por ENV)

| Variable | Obligatoria | Default | Qué es |
|---|---|---|---|
| `DATABASE_URL` | sí | — | Postgres (VibeNest la inyecta con la BD gestionada) |
| `CMF_API_KEY` | sí | — | API key de la CMF (pedirla en `api.sbif.cl/api/contactanos.jsp`, T-002) |
| `PORT` | no | `8080` | puerto del server HTTP |
| `REFRESH_INTERVAL` | no | `24h` | intervalo del scheduler (formato Go) |

## VibeNest (Fase 2, paso 4 — deployado 2026-07-10)

**URL viva: `https://faro.vibenest.net/`.** Lo aprendido del primer deploy real:

1. Servicio desde el repo de GitHub — VibeNest detecta el `Dockerfile` (Build Pack: Dockerfile).
2. BD Postgres gestionada → `DATABASE_URL` inyectada sola (más las `POSTGRES_*` de Coolify,
   que Faro ignora).
3. `CMF_API_KEY` en Project Settings → Environment (nombre exacto; el valor jamás en el repo).
   Sin ella el binario sale de inmediato con el mensaje que la nombra — el crash loop inicial
   fue exactamente eso.
4. **Puertos:** el "Internal Port" del panel (default 3000) debe coincidir con el puerto real
   de la app. Alineado en 8080 (= `PORT`); desalineado da bad gateway intermitente.
5. Verificación: logs con `migraciones al día` + `backfill ok`, la URL sirviendo dashboard y
   `/api/{code}`, y `sync_runs` acumulando evidencia (CASE-002, CASE-004, AUD-004).

**Incidente abierto (T-004):** el egress TCP de la red de contenedores del servidor
(Hetzner-1-16Gb) está roto — la app no alcanza a la CMF (`dial tcp …:443: i/o timeout`) y el
refresco diario falla; ticket enviado a VibeNest con la evidencia. **Workaround aplicado
mientras tanto:** sembrar la BD por la consola SQL del panel (Storage → Open DB console) con
un dump idempotente generado localmente (`pg_dump --data-only --rows-per-insert=250
--on-conflict-do-nothing` sobre una BD local recién backfilleada). Sin exponer puertos ni
mover secretos. Cuando VibeNest arregle el egress, el scheduler interno retoma solo: no hay
nada que revertir.

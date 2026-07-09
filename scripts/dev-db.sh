#!/usr/bin/env bash
# Levanta el Postgres de DESARROLLO en Docker (contenedor `faro-pg`, datos en
# el volumen `faro-pgdata`). En producción la BD la gestiona VibeNest y llega
# por DATABASE_URL (ADR-008): este script no existe para prod.
# Uso: ./scripts/dev-db.sh [stop]
set -euo pipefail

if [ "${1:-}" = "stop" ]; then
  docker stop faro-pg >/dev/null && echo "faro-pg detenido"
  exit 0
fi

if docker ps -q -f name='^faro-pg$' | grep -q .; then
  echo "faro-pg ya está corriendo"
elif docker ps -aq -f name='^faro-pg$' | grep -q .; then
  docker start faro-pg >/dev/null && echo "faro-pg arrancado (contenedor existente)"
else
  docker run -d --name faro-pg \
    -e POSTGRES_USER=faro -e POSTGRES_PASSWORD=faro -e POSTGRES_DB=faro \
    -p 5432:5432 \
    -v faro-pgdata:/var/lib/postgresql/data \
    postgres:17-alpine >/dev/null
  echo "faro-pg creado (postgres:17-alpine)"
fi

# Esperar readiness (máx ~15 s)
for _ in $(seq 1 30); do
  if docker exec faro-pg pg_isready -U faro -d faro -q 2>/dev/null; then
    echo "Postgres listo en localhost:5432 (db=faro user=faro) ✓"
    exit 0
  fi
  sleep 0.5
done
echo "Postgres no respondió a tiempo" >&2
exit 1

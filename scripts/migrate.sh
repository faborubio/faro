#!/usr/bin/env bash
# Aplica las migraciones de `migrations/*.sql` en orden, una sola vez cada una
# (registro en `schema_migrations`). Cada archivo corre en UNA transacción.
# Lee DATABASE_URL de .env o del entorno. Uso: ./scripts/migrate.sh
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -f .env ]; then
  set -a; . ./.env; set +a
fi
: "${DATABASE_URL:?falta DATABASE_URL — ponla en .env o export DATABASE_URL=...}"

psql "$DATABASE_URL" -q -v ON_ERROR_STOP=1 -c \
  "CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now());"

applied=0
for f in migrations/*.sql; do
  v=$(basename "$f")
  if [ "$(psql "$DATABASE_URL" -tAc "SELECT 1 FROM schema_migrations WHERE version = '$v'")" = "1" ]; then
    echo "skip   $v"
    continue
  fi
  echo "apply  $v"
  psql "$DATABASE_URL" -q -v ON_ERROR_STOP=1 -1 -f "$f"
  psql "$DATABASE_URL" -q -v ON_ERROR_STOP=1 -c "INSERT INTO schema_migrations (version) VALUES ('$v');"
  applied=$((applied+1))
done
echo "Migraciones al día ✓ ($applied aplicadas ahora)"

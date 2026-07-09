#!/usr/bin/env bash
# Smoke test del gate de API key CMF (previo al adapter en Go).
# Lee CMF_API_KEY de .env (gitignored) o del entorno. NO imprime la key.
# Uso: ./scripts/smoke-cmf.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# Cargar .env si existe (sin exponer valores)
if [ -f .env ]; then
  set -a; . ./.env; set +a
fi
: "${CMF_API_KEY:?falta CMF_API_KEY — ponla en .env o export CMF_API_KEY=...}"

base="https://api.cmfchile.cl/api-sbifv3/recursos_api"
ok=0; fail=0
for ind in uf dolar utm ipc; do
  echo "== $ind =="
  # -sS: silencioso pero muestra errores; la URL (con key) no se imprime salvo error de red
  if body=$(curl -sS --max-time 25 "$base/$ind?apikey=$CMF_API_KEY&formato=json" 2>/dev/null) \
     && echo "$body" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(json.dumps(d, ensure_ascii=False, indent=2)[:600])' 2>/dev/null; then
    ok=$((ok+1))
  else
    echo "  (respuesta no-JSON o error — key inválida, indicador no disponible, o red)"
    fail=$((fail+1))
  fi
  echo
done
echo "Resultado: $ok OK, $fail con problema."
[ "$fail" -eq 0 ] && echo "Gate de API key: VERDE ✓"

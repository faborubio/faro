# SECURITY — Postura de seguridad de Faro

> Responde a: **¿de qué me protejo y cómo?** (El Método §2, proporcional). Nace en Fase 3 con las
> alertas por webhook — la primera vez que Faro **origina** tráfico hacia URLs ajenas y acepta
> escrituras públicas. Antes de esta fase la postura vivía entera en el SAD §8.

## Modelo de amenaza (proporcional a la escala)

Faro es una API pública **de solo lectura** salvo por un endpoint de registro de alertas, sin
cuentas ni datos personales más allá de una `webhook_url`. Lo que vale la pena defender:

1. **La red interna** — las alertas hacen POST a URLs que registra cualquiera: SSRF es el riesgo
   real de la fase (SAD §12).
2. **La disponibilidad** — API pública sin límites invita al abuso (ADR-010).
3. **La API key de la CMF** — el único secreto del sistema (ADR-009).

Lo que **no** se defiende porque la escala no lo amerita (SAD §2.1): sin WAF, sin auditoría de
accesos, sin rate limit distribuido (una sola instancia), sin auth (el diseño es público).

## Anti-SSRF en los webhooks (ADR-006, SAD §8)

Una API que hace POST a URLs arbitrarias es un proxy en potencia hacia localhost, la red del
contenedor o la metadata del cloud. Defensa en **dos capas**, ambas en `internal/webhook`:

- **Capa 1 — al registrar** (`ValidateURL`, responde 400): esquema http/https solamente, sin
  credenciales embebidas, y el host —literal o resuelto por DNS— no puede ser loopback, red
  privada (RFC 1918 / ULA), link-local (incluye `169.254.169.254`, la metadata de los clouds),
  multicast, no-especificada ni broadcast. IPv4 e IPv6; las 4-in-6 (`::ffff:127.0.0.1`) se
  evalúan como su IPv4. Si un nombre resuelve a una mezcla de IPs públicas y privadas se rechaza
  entero.
- **Capa 2 — al despachar** (dial pineado): cada POST **vuelve a resolver y validar** y disca
  directo a la IP aprobada — no al hostname. Un DNS que cambia entre el registro y el disparo
  (rebinding) no gana nada. Además el transporte **no hereda proxy del entorno** (`HTTP_PROXY`
  inyectado sería otro camino) y **no sigue redirects** (un 302 hacia adentro tampoco).

**Escape de desarrollo:** `FARO_WEBHOOK_ALLOW_PRIVATE=1` desactiva ambos bloqueos para probar
contra receptores en loopback. El binario lo grita en el log al arrancar. **Prohibido en
producción** — no existe caso legítimo.

Verificado E2E (2026-07-10): loopback y metadata rechazados con 400 sin el escape; entrega real
de un cruce con el escape en dev.

## Alertas: tokens y superficie de escritura

- El **token** es el único handle de una alerta: 32 bytes de `crypto/rand` en hex (64 chars),
  inadivinable; unicidad respaldada por índice único (migración 002). Quien tiene el token ve y
  borra **solo esa** alerta. No hay enumeración: sin token no hay listado.
- El cuerpo del registro se acota a **4 KB** (`MaxBytesReader`) y la `webhook_url` a 2048 chars.
- El disparo usa semántica de **cruce** (solo al pasar el umbral, no mientras se mantiene): un
  receptor no puede ser bombardeado a diario registrando una alerta siempre-cierta. Sin
  reintentos (AUD-006): el POST tiene timeout de 10 s y el resultado va al log.

## Rate limiting y CORS (ADR-010)

- **Token bucket por IP en memoria** (`internal/ratelimit`): 5 req/s con ráfaga de 30 (una carga
  completa del dashboard ≈ 11 requests). 429 en JSON con `Retry-After`. Mapa acotado a 4096 IPs
  (al tope se vacía — el costo es un bucket fresco para una ráfaga en curso, no una fuga).
- **IP del cliente:** la **última** entrada de `X-Forwarded-For` — la escribe el proxy de la
  plataforma (Traefik/Coolify), no el cliente; confiar en la primera dejaría al atacante elegir
  su propia llave. Esto asume que **solo el proxy alcanza al contenedor** (cierto en VibeNest);
  si Faro se expusiera directo a internet, habría que ignorar XFF.
- `/healthz` queda **fuera** del límite: el health check de la plataforma no compite con clientes.
- **CORS abierto** (`Access-Control-Allow-Origin: *`) en `/api/*` es diseño, no descuido: los
  widgets y snippets de terceros consumen la API por fetch — es el producto. El rate limiting
  acota el abuso. El widget (`/widget/{code}`) no manda `X-Frame-Options`: embeberlo es el punto;
  es HTML autocontenido sin JS ni cookies.

## Secretos y configuración (ADR-009)

- Todo por ENV; `.env` está gitignored y **excluido del contexto de Docker**. La API key de la
  CMF jamás viaja en errores ni logs (test del adapter lo cubre).
- La imagen corre `scratch` con usuario no-root (ADR-008).

## Qué observar si algo huele mal

- `sync_runs`: salud de la fuente (única salida de red además de los webhooks).
- Log del binario: `alerta disparada` / `webhook no entregado` (con id de alerta), y el WARN de
  arranque si el escape de dev quedó prendido donde no debía.

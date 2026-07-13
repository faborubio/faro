// cmf-proxy-worker.js — proxy mínimo hacia la API de la CMF (contingencia T-004).
//
// La CMF es inalcanzable desde la IP del host de producción (filtro de
// ruta/IP de origen), pero el egress general del contenedor funciona. Este
// Worker de Cloudflare (free tier) reenvía cada request a la CMF desde las
// IPs de Cloudflare; Faro se re-apunta con CMF_BASE_URL (ver docs/DEPLOY.md).
//
// Deploy: dash.cloudflare.com → Workers → Create → pegar esto → Deploy.
// Luego en VibeNest: CMF_BASE_URL=https://<nombre>.<cuenta>.workers.dev
//
// Solo GET y solo hacia la raíz de recursos de la CMF: no es un proxy
// abierto genérico. La API key viaja en la query como siempre (infra propia,
// mismo TLS de punta a punta por tramo). Cuando el ticket T-004 se resuelva,
// borrar la variable en el panel y este Worker.

const UPSTREAM = "https://api.cmfchile.cl/api-sbifv3/recursos_api";

export default {
  async fetch(request) {
    if (request.method !== "GET") {
      return new Response("solo GET", { status: 405 });
    }
    const url = new URL(request.url);
    return fetch(UPSTREAM + url.pathname + url.search, {
      headers: { accept: "application/json" },
    });
  },
};

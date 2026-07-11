// Package ratelimit acota ráfagas por IP en la API pública (ADR-010): token
// bucket en memoria, hecho a mano — la única dependencia del módulo es pgx
// (ADR-004) y a esta escala (una instancia, ADR-010 lo acepta explícito) no
// hace falta nada distribuido.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Limiter reparte tokens por llave (la IP del cliente). Crear con New.
type Limiter struct {
	rate    float64 // tokens que se reponen por segundo
	burst   float64 // tope del bucket (ráfaga máxima)
	maxKeys int     // tope del mapa de llaves

	mu      sync.Mutex
	buckets map[string]*bucket
	now     func() time.Time // inyectable en tests
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New crea un Limiter de `rate` peticiones/segundo con ráfaga máxima `burst`
// por llave. El mapa se acota a maxKeys llaves y al tope se vacía entero —
// el mismo patrón burdo-pero-suficiente del cache de la API: el costo es que
// una ráfaga en curso gana un bucket fresco, no una fuga de memoria.
func New(rate float64, burst, maxKeys int) *Limiter {
	return &Limiter{
		rate:    rate,
		burst:   float64(burst),
		maxKeys: maxKeys,
		buckets: make(map[string]*bucket),
		now:     time.Now,
	}
}

// Allow consume un token de la llave; false = 429.
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			clear(l.buckets)
		}
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Middleware aplica el límite por IP a todo lo que envuelve. El 429 va en
// JSON (el formato de la API) con Retry-After.
func (l *Limiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.Allow(ClientIP(r)) {
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"demasiadas peticiones: reintenta en unos segundos"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ClientIP identifica al cliente detrás del proxy de la plataforma: la
// ÚLTIMA entrada de X-Forwarded-For es la que agregó nuestro proxy (las
// anteriores las escribe quien quiera — confiar en la primera dejaría al
// cliente elegir su propia llave y saltarse el límite). Sin XFF (dev local),
// la IP de la conexión.
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

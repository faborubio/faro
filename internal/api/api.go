// Package api sirve la API JSON pública de Faro (SAD §7):
//
//	GET /api/{code}                          → valor vigente + metadatos
//	GET /api/{code}/history?desde=…&hasta=…  → histórico por rango
//
// Lee de Postgres con un cache en memoria por delante (TTL corto); JAMÁS
// llama a la fuente en la request (ADR-003) — si la CMF cae, Faro sigue
// respondiendo con el último valor persistido. Ruteo con ServeMux de la
// stdlib (ADR-001): los patrones con método y {code} bastan.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

// dateFmt es el formato de fechas de la API (query params y respuestas).
const dateFmt = "2006-01-02"

// defaultHistoryDays es el rango cuando /history llega sin ?desde=.
const defaultHistoryDays = 30

// maxCacheEntries acota el cache: las combinaciones de ?desde/?hasta son
// ilimitadas y el free tier tiene 256 MB. Al tope se vacía entero — burdo
// pero suficiente a esta escala (el refresco es 1×/día).
const maxCacheEntries = 1024

// Store es lo que la API necesita de la persistencia (fake en tests).
type Store interface {
	Latest(ctx context.Context, code string) (indicator.Snapshot, error)
	History(ctx context.Context, code string, from, to time.Time) ([]indicator.Snapshot, error)
	GetIndicator(ctx context.Context, code string) (store.Indicator, error)
}

// Server arma el handler HTTP de la API. Crear con New.
type Server struct {
	store Store
	ttl   time.Duration
	log   *slog.Logger
	now   func() time.Time // inyectable en tests (default de /history)

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	body    []byte
	expires time.Time
}

// New crea el server de la API. ttl <= 0 usa 60 s: corto de sobra para datos
// que cambian 1×/día, y tras un refresco lo viejo expira en un minuto.
func New(st Store, ttl time.Duration, log *slog.Logger) *Server {
	if ttl <= 0 {
		ttl = 60 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		store: st,
		ttl:   ttl,
		log:   log,
		now:   time.Now,
		cache: make(map[string]cacheEntry),
	}
}

// Handler devuelve el http.Handler con las rutas de la API montadas.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/{code}", s.handleCurrent)
	mux.HandleFunc("GET /api/{code}/history", s.handleHistory)
	return mux
}

// currentResponse es el valor vigente de un indicador con sus metadatos.
type currentResponse struct {
	Code  string  `json:"code"`
	Name  string  `json:"name"`
	Unit  string  `json:"unit"`
	Value float64 `json:"value"`
	Date  string  `json:"date"` // fecha de publicación del valor (YYYY-MM-DD)
}

func (s *Server) handleCurrent(w http.ResponseWriter, r *http.Request) {
	if s.serveFromCache(w, r) {
		return
	}
	code := r.PathValue("code")

	ind, err := s.store.GetIndicator(r.Context(), code)
	if err != nil {
		s.writeStoreError(w, r, err, "indicador desconocido")
		return
	}
	snap, err := s.store.Latest(r.Context(), code)
	if err != nil {
		s.writeStoreError(w, r, err, "el indicador aún no tiene valores")
		return
	}
	s.writeJSON(w, r, currentResponse{
		Code:  ind.Code,
		Name:  ind.Name,
		Unit:  ind.Unit,
		Value: snap.Value,
		Date:  snap.Date.Format(dateFmt),
	})
}

// historyResponse es el histórico de un indicador en un rango de fechas.
type historyResponse struct {
	Code   string       `json:"code"`
	Unit   string       `json:"unit"`
	Desde  string       `json:"desde"`
	Hasta  string       `json:"hasta"`
	Values []valuePoint `json:"values"` // ascendente por fecha; puede ser vacío
}

type valuePoint struct {
	Date  string  `json:"date"`
	Value float64 `json:"value"`
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if s.serveFromCache(w, r) {
		return
	}
	code := r.PathValue("code")

	hasta := s.now().UTC().Truncate(24 * time.Hour)
	if raw := r.URL.Query().Get("hasta"); raw != "" {
		var err error
		if hasta, err = time.Parse(dateFmt, raw); err != nil {
			s.writeError(w, http.StatusBadRequest, "hasta inválido: usar YYYY-MM-DD")
			return
		}
	}
	desde := hasta.AddDate(0, 0, -defaultHistoryDays)
	if raw := r.URL.Query().Get("desde"); raw != "" {
		var err error
		if desde, err = time.Parse(dateFmt, raw); err != nil {
			s.writeError(w, http.StatusBadRequest, "desde inválido: usar YYYY-MM-DD")
			return
		}
	}
	if desde.After(hasta) {
		s.writeError(w, http.StatusBadRequest, "desde es posterior a hasta")
		return
	}

	ind, err := s.store.GetIndicator(r.Context(), code)
	if err != nil {
		s.writeStoreError(w, r, err, "indicador desconocido")
		return
	}
	snaps, err := s.store.History(r.Context(), code, desde, hasta)
	if err != nil {
		s.writeStoreError(w, r, err, "")
		return
	}
	values := make([]valuePoint, len(snaps))
	for i, sn := range snaps {
		values[i] = valuePoint{Date: sn.Date.Format(dateFmt), Value: sn.Value}
	}
	s.writeJSON(w, r, historyResponse{
		Code:   ind.Code,
		Unit:   ind.Unit,
		Desde:  desde.Format(dateFmt),
		Hasta:  hasta.Format(dateFmt),
		Values: values,
	})
}

// serveFromCache responde desde el cache si hay entrada vigente para la URL
// completa (path + query). Solo se cachean respuestas 200.
func (s *Server) serveFromCache(w http.ResponseWriter, r *http.Request) bool {
	key := r.URL.RequestURI()
	s.mu.Lock()
	e, ok := s.cache[key]
	if ok && time.Now().After(e.expires) {
		delete(s.cache, key)
		ok = false
	}
	s.mu.Unlock()
	if !ok {
		return false
	}
	writeBody(w, http.StatusOK, e.body, "HIT")
	return true
}

func (s *Server) writeJSON(w http.ResponseWriter, r *http.Request, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Error("serializando respuesta", "path", r.URL.Path, "error", err)
		s.writeError(w, http.StatusInternalServerError, "error interno")
		return
	}
	s.mu.Lock()
	if len(s.cache) >= maxCacheEntries {
		clear(s.cache)
	}
	s.cache[r.URL.RequestURI()] = cacheEntry{body: body, expires: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	writeBody(w, http.StatusOK, body, "MISS")
}

// writeStoreError traduce errores del store: ErrNotFound → 404 con el mensaje
// dado; cualquier otro → 500 opaco (el detalle va al log, no al cliente).
func (s *Server) writeStoreError(w http.ResponseWriter, r *http.Request, err error, notFoundMsg string) {
	if errors.Is(err, store.ErrNotFound) {
		if notFoundMsg == "" {
			notFoundMsg = "no encontrado"
		}
		s.writeError(w, http.StatusNotFound, notFoundMsg)
		return
	}
	s.log.Error("error del store", "path", r.URL.Path, "error", err)
	s.writeError(w, http.StatusInternalServerError, "error interno")
}

func (s *Server) writeError(w http.ResponseWriter, status int, msg string) {
	body, _ := json.Marshal(map[string]string{"error": msg})
	writeBody(w, status, body, "")
}

func writeBody(w http.ResponseWriter, status int, body []byte, cache string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if cache != "" {
		w.Header().Set("X-Cache", cache)
	}
	w.WriteHeader(status)
	_, _ = w.Write(body) // si el cliente cortó, no hay nada útil que hacer
}

// Package web sirve el dashboard de Faro (ADR-005): HTML server-rendered con
// el valor vigente de cada indicador + Chart.js dibujando la tendencia contra
// la propia API (/api/{code}/history) — el dashboard es un cliente más, jamás
// toca la fuente ni la base directamente. Todos los assets (template, JS,
// Chart.js) viajan embebidos en el binario: un solo artefacto, sin CDN.
package web

import (
	"context"
	"embed"
	"errors"
	"hash/fnv"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/faborubio/faro/internal/indicator"
	"github.com/faborubio/faro/internal/store"
)

//go:embed templates static
var assets embed.FS

// assetVersion es un hash de los assets embebidos: viaja como ?v= en los
// <script>, así el cache del navegador (Cache-Control 1 h) se rompe exacto
// cuando un redeploy trae assets nuevos — sin él, un app.js viejo puede
// correr contra HTML nuevo hasta 1 h. Se calcula del contenido (no del
// commit) porque el build en Docker no lleva .git.
var assetVersion = func() string {
	h := fnv.New64a()
	err := fs.WalkDir(assets, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		_, _ = h.Write(b)
		return nil
	})
	if err != nil {
		panic(err) // imposible: el árbol embebido es de solo lectura
	}
	return strconv.FormatUint(h.Sum64(), 36)
}()

// displayRank fija el orden de las tarjetas (los más consultados primero);
// códigos fuera de la lista van al final, alfabéticos.
var displayRank = map[string]int{"uf": 0, "dolar": 1, "utm": 2, "ipc": 3}

// Store es lo que el dashboard necesita de la persistencia.
type Store interface {
	ListIndicators(ctx context.Context) ([]store.Indicator, error)
	Latest(ctx context.Context, code string) (indicator.Snapshot, error)
}

// Server sirve el dashboard. Crear con New.
type Server struct {
	store Store
	log   *slog.Logger
	tmpl  *template.Template
}

// New arma el server del dashboard (el template embebido se parsea una vez).
func New(st Store, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		store: st,
		log:   log,
		tmpl:  template.Must(template.ParseFS(assets, "templates/index.html.tmpl")),
	}
}

// Handler devuelve el http.Handler del dashboard: la portada y los assets
// estáticos. Cualquier otra ruta es 404 (la API vive en su propio handler).
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err) // imposible: el árbol embebido siempre trae static/
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.Handle("GET /static/", http.StripPrefix("/static/",
		cacheStatic(http.FileServerFS(static))))
	return mux
}

// card es una tarjeta del dashboard: metadatos del catálogo + valor vigente
// ya formateado (formato chileno, como la fuente — CASE-003). RawValue viaja
// aparte para el convertidor (JS necesita el número, no el string chileno).
type card struct {
	Code        string
	Name        string
	Unit        string
	Description string
	Cadence     string
	Value       string  // "" si el indicador aún no tiene valores
	RawValue    float64 // valor sin formatear, para data-attributes
	Date        string  // YYYY-MM-DD
	HasValue    bool
}

// Convertible informa si la tarjeta entra al convertidor: unidades
// denominadas en pesos con valor vigente (uf, dolar, utm — no el IPC, que
// es una variación porcentual, no una unidad).
func (c card) Convertible() bool {
	return c.HasValue && c.Unit == "CLP"
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	inds, err := s.store.ListIndicators(r.Context())
	if err != nil {
		s.log.Error("dashboard: catálogo", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
		return
	}
	sort.SliceStable(inds, func(i, j int) bool {
		ri, oki := displayRank[inds[i].Code]
		rj, okj := displayRank[inds[j].Code]
		switch {
		case oki && okj:
			return ri < rj
		case oki != okj:
			return oki // los conocidos primero
		default:
			return inds[i].Code < inds[j].Code
		}
	})

	cards := make([]card, 0, len(inds))
	for _, ind := range inds {
		c := card{
			Code:        ind.Code,
			Name:        ind.Name,
			Unit:        ind.Unit,
			Description: ind.Description,
			Cadence:     string(ind.Cadence),
		}
		snap, err := s.store.Latest(r.Context(), ind.Code)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// Recién deployado y sin backfill aún: la tarjeta lo dice.
		case err != nil:
			s.log.Error("dashboard: último valor", "code", ind.Code, "error", err)
			http.Error(w, "error interno", http.StatusInternalServerError)
			return
		default:
			c.Value = formatCL(snap.Value, ind.Unit)
			c.RawValue = snap.Value
			c.Date = snap.Date.Format("2006-01-02")
			c.HasValue = true
		}
		cards = append(cards, c)
	}

	hasConverter := false
	for _, c := range cards {
		if c.Convertible() {
			hasConverter = true
			break
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	err = s.tmpl.Execute(w, map[string]any{
		"Cards":        cards,
		"HasConverter": hasConverter,
		"AssetV":       assetVersion,
	})
	if err != nil {
		s.log.Error("dashboard: render", "error", err)
	}
}

// cacheStatic marca los assets como cacheables por 1 h: cambian solo con un
// redeploy y el dashboard los referencia igual — 1 h de desfase es aceptable.
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

// formatCL formatea un valor a la chilena (punto de miles, coma decimal),
// espejo del formato de la fuente (CASE-003). El % lleva siempre un decimal:
// "0,0%" comunica que el IPC de 0,0% es un dato real (CASE-005), no ausencia.
func formatCL(v float64, unit string) string {
	prec := -1 // la representación más corta que redondea de vuelta
	if unit == "%" {
		prec = 1
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac, _ := strings.Cut(s, ".")

	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i, d := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(d)
	}
	if frac != "" {
		b.WriteByte(',')
		b.WriteString(frac)
	}
	return b.String()
}

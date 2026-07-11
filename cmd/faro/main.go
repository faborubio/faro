// Faro — API pública + dashboard de indicadores económicos de Chile.
// Un solo binario: scheduler de refresco diario + API HTTP + dashboard
// (SAD §4). El dashboard entra en la Fase 2. Config por ENV (ADR-009):
// DATABASE_URL, CMF_API_KEY y opcionales PORT (default 8080) y
// REFRESH_INTERVAL (default 24h).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faborubio/faro/internal/alert"
	"github.com/faborubio/faro/internal/api"
	"github.com/faborubio/faro/internal/migrate"
	"github.com/faborubio/faro/internal/ratelimit"
	"github.com/faborubio/faro/internal/refresh"
	"github.com/faborubio/faro/internal/source/cmf"
	"github.com/faborubio/faro/internal/store"
	"github.com/faborubio/faro/internal/web"
	"github.com/faborubio/faro/internal/webhook"
	"github.com/faborubio/faro/migrations"
)

func main() {
	if err := run(); err != nil {
		slog.Error("faro no pudo arrancar", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("falta DATABASE_URL (ver .env.example)")
	}
	apiKey := os.Getenv("CMF_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("falta CMF_API_KEY (ver .env.example)")
	}
	interval := 24 * time.Hour
	if raw := os.Getenv("REFRESH_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return fmt.Errorf("REFRESH_INTERVAL inválido: %q (formato Go: 24h, 30m)", raw)
		}
		interval = d
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("abriendo pool de Postgres: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("postgres no responde (¿./scripts/dev-db.sh?): %w", err)
	}

	// Migraciones embebidas al boot (AUD-002): en VibeNest no hay psql ni
	// shell; el binario deja el esquema al día antes de tocar la base.
	applied, err := migrate.Apply(ctx, dbURL, migrations.FS, slog.Default())
	if err != nil {
		return fmt.Errorf("aplicando migraciones: %w", err)
	}
	slog.Info("migraciones al día", "aplicadas_ahora", applied)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	st := store.New(pool)
	source := cmf.New(apiKey)

	// Alertas (ADR-006): el scheduler notifica los valores que cambiaron y el
	// evaluador dispara los webhooks que cruzan umbral. El escape de redes
	// privadas es SOLO para dev (docs/SECURITY.md).
	allowPrivate := os.Getenv("FARO_WEBHOOK_ALLOW_PRIVATE") == "1"
	if allowPrivate {
		slog.Warn("FARO_WEBHOOK_ALLOW_PRIVATE=1: el anti-SSRF de webhooks está APAGADO — solo aceptable en desarrollo")
	}
	hook := webhook.New(allowPrivate)
	alerts := alert.New(st, hook, slog.Default())
	refresher := refresh.New(source, st, interval, slog.Default()).WithNotifier(alerts)

	// API y dashboard comparten binario y puerto: /api/* va a la API JSON,
	// el resto (portada + widgets + assets) al dashboard (ADR-005).
	mux := http.NewServeMux()
	mux.Handle("/api/", api.New(st, 0, slog.Default()).WithURLValidator(hook).Handler())
	mux.Handle("/", web.New(st, slog.Default()).Handler())

	// Rate limiting por IP (ADR-010) sobre todo lo público. /healthz queda
	// fuera: el health check de la plataforma no compite con los clientes.
	limiter := ratelimit.New(5, 30, 4096) // 5 req/s, ráfaga 30 (una carga del dashboard ≈ 11)
	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			http.Error(w, "sin base de datos", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	root.Handle("/", limiter.Middleware(mux))

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           root,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go refresher.Run(ctx)

	// El server corre hasta la señal; entonces se apaga graceful con un
	// plazo corto (VibeNest manda SIGTERM antes de matar el contenedor).
	errCh := make(chan error, 1)
	go func() { errCh <- server.ListenAndServe() }()
	slog.Info("faro arriba", "puerto", port, "fuente", source.Name(), "intervalo", interval.String())

	select {
	case err := <-errCh:
		return fmt.Errorf("server HTTP: %w", err)
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("apagando server: %w", err)
	}
	slog.Info("faro detenido")
	return nil
}

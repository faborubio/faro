// Faro — API pública + dashboard de indicadores económicos de Chile.
// Un solo binario: scheduler de refresco diario + API HTTP + dashboard
// (SAD §4). Hoy corre el scheduler (Fase 1, paso 2); la API entra en el
// paso 3. Config por ENV (ADR-009): DATABASE_URL, CMF_API_KEY y opcional
// REFRESH_INTERVAL (default 24h).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/faborubio/faro/internal/refresh"
	"github.com/faborubio/faro/internal/source/cmf"
	"github.com/faborubio/faro/internal/store"
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
		return fmt.Errorf("postgres no responde (¿./scripts/dev-db.sh y ./scripts/migrate.sh?): %w", err)
	}

	source := cmf.New(apiKey)
	refresher := refresh.New(source, store.New(pool), interval, slog.Default())

	slog.Info("faro arriba", "fuente", source.Name(), "intervalo", interval.String())
	refresher.Run(ctx) // bloquea hasta SIGINT/SIGTERM
	slog.Info("faro detenido")
	return nil
}

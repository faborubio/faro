// Package migrate aplica migraciones SQL embebidas al boot (AUD-002). Mismo
// contrato que scripts/migrate.sh: registro por nombre de archivo en
// schema_migrations, cada archivo corre en UNA transacción, orden lexicográfico
// (de ahí la numeración 001_, 002_, …). Sobre una BD ya migrada por el script
// no re-aplica nada: ambos caminos son intercambiables.
package migrate

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5"
)

// lockID identifica el advisory lock de sesión que serializa migradores
// concurrentes (dos boots solapados durante un redeploy). Constante arbitraria
// propia de Faro; se libera sola al cerrar la conexión.
const lockID int64 = 0x4641524f // "FARO"

// Apply aplica en orden las migraciones *.sql de fsys que no estén registradas
// en schema_migrations y devuelve cuántas aplicó ahora. Abre su propia conexión
// en protocolo simple porque cada archivo trae varias sentencias por Exec.
// Si una migración falla, su transacción se revierte y las anteriores quedan
// aplicadas: reintentar tras corregir retoma desde la que falló.
func Apply(ctx context.Context, dsn string, fsys fs.FS, log *slog.Logger) (int, error) {
	files, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return 0, fmt.Errorf("migrate: listar migraciones: %w", err)
	}
	if len(files) == 0 {
		return 0, fmt.Errorf("migrate: sin migraciones embebidas (¿go:embed roto?)")
	}
	sort.Strings(files)

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return 0, fmt.Errorf("migrate: parsear DATABASE_URL: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return 0, fmt.Errorf("migrate: conectar: %w", err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		return 0, fmt.Errorf("migrate: tomar advisory lock: %w", err)
	}

	if _, err := conn.Exec(ctx,
		"CREATE TABLE IF NOT EXISTS schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())",
	); err != nil {
		return 0, fmt.Errorf("migrate: crear schema_migrations: %w", err)
	}

	applied := 0
	for _, f := range files {
		var done bool
		if err := conn.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)", f,
		).Scan(&done); err != nil {
			return applied, fmt.Errorf("migrate: consultar %s: %w", f, err)
		}
		if done {
			continue
		}
		if err := applyOne(ctx, conn, fsys, f); err != nil {
			return applied, err
		}
		log.Info("migración aplicada", "version", f)
		applied++
	}
	return applied, nil
}

// applyOne corre una migración y su registro en la misma transacción: o queda
// aplicada y anotada, o nada.
func applyOne(ctx context.Context, conn *pgx.Conn, fsys fs.FS, name string) error {
	sql, err := fs.ReadFile(fsys, name)
	if err != nil {
		return fmt.Errorf("migrate: leer %s: %w", name, err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("migrate: abrir transacción para %s: %w", name, err)
	}
	defer tx.Rollback(ctx) // no-op tras Commit

	if _, err := tx.Exec(ctx, string(sql)); err != nil {
		return fmt.Errorf("migrate: aplicar %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", name); err != nil {
		return fmt.Errorf("migrate: registrar %s: %w", name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("migrate: confirmar %s: %w", name, err)
	}
	return nil
}

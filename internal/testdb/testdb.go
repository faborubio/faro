// Package testdb coordina el acceso de los tests de integración a la BD
// compartida (FARO_TEST_DATABASE_URL). `go test ./...` corre los paquetes en
// paralelo y cada test borra el esquema y lo re-migra: sin exclusión mutua,
// dos paquetes se pisan la BD a mitad de test. Acquire serializa con un
// advisory lock de sesión que se libera al terminar cada test.
package testdb

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// lockID identifica el lock de los tests. Distinto del de internal/migrate:
// aquel serializa migradores; este serializa tests entre paquetes.
const lockID int64 = 0x54455354 // "TEST"

// Acquire devuelve el DSN de la BD de tests y la reserva en exclusiva hasta el
// final del test (t.Cleanup). Sin FARO_TEST_DATABASE_URL el test se salta; el
// nombre de la BD debe contener "test" porque los tests borran el esquema.
func Acquire(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("FARO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("integración: exporta FARO_TEST_DATABASE_URL (una BD que los tests borran, p. ej. postgres://faro:faro@localhost:5432/faro_test)")
	}
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("FARO_TEST_DATABASE_URL inválida: %v", err)
	}
	if !strings.Contains(cfg.Database, "test") {
		t.Fatalf("la BD %q no parece de pruebas (el nombre debe contener \"test\"): los tests borran el esquema", cfg.Database)
	}

	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("conectar a la BD de tests: %v (¿está arriba ./scripts/dev-db.sh y existe la BD?)", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockID); err != nil {
		conn.Close(ctx)
		t.Fatalf("reservar la BD de tests: %v", err)
	}
	// Cerrar la conexión libera el lock de sesión.
	t.Cleanup(func() { conn.Close(context.Background()) })
	return dsn
}

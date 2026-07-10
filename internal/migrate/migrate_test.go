// Tests de integración de internal/migrate. Igual que en internal/store:
//
//	FARO_TEST_DATABASE_URL='postgres://faro:faro@localhost:5432/faro_test' go test ./internal/migrate/
//
// Cada test BORRA el esquema; el helper exige que el nombre de la BD contenga
// "test" — nunca la BD de dev. Sin la variable, se saltan.
package migrate_test

import (
	"context"
	"log/slog"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"

	"github.com/faborubio/faro/internal/migrate"
	"github.com/faborubio/faro/internal/testdb"
	"github.com/faborubio/faro/migrations"
)

// testConn devuelve el DSN de la BD de tests con el esquema recién borrado,
// más una conexión en protocolo simple para verificar el resultado.
func testConn(t *testing.T) (string, *pgx.Conn) {
	t.Helper()
	dsn := testdb.Acquire(t)
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parsear DSN: %v", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("conectar: %v (¿está arriba ./scripts/dev-db.sh y existe la BD?)", err)
	}
	t.Cleanup(func() { conn.Close(context.Background()) })

	if _, err := conn.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		t.Fatalf("resetear esquema: %v", err)
	}
	return dsn, conn
}

func discard() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestApplyRealMigrationsIsIdempotent(t *testing.T) {
	dsn, conn := testConn(t)
	ctx := context.Background()

	applied, err := migrate.Apply(ctx, dsn, migrations.FS, discard())
	if err != nil {
		t.Fatalf("primera aplicación: %v", err)
	}
	if applied < 1 {
		t.Fatalf("primera aplicación: applied = %d, quería >= 1", applied)
	}

	// El esquema quedó usable: el catálogo sembrado por 001 está completo.
	var n int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM indicators").Scan(&n); err != nil {
		t.Fatalf("consultar catálogo: %v", err)
	}
	if n != 4 {
		t.Fatalf("catálogo sembrado: %d indicadores, quería 4", n)
	}

	// Segunda pasada: todo registrado, nada que aplicar.
	applied, err = migrate.Apply(ctx, dsn, migrations.FS, discard())
	if err != nil {
		t.Fatalf("segunda aplicación: %v", err)
	}
	if applied != 0 {
		t.Fatalf("segunda aplicación: applied = %d, quería 0", applied)
	}
}

func TestApplyFailureRollsBackOnlyTheBrokenOne(t *testing.T) {
	dsn, conn := testConn(t)
	ctx := context.Background()

	fsys := fstest.MapFS{
		"001_ok.sql":  {Data: []byte("CREATE TABLE uno (id int);")},
		"002_bad.sql": {Data: []byte("CREATE TABLE dos (id int); ESTO NO ES SQL;")},
	}

	applied, err := migrate.Apply(ctx, dsn, fsys, discard())
	if err == nil {
		t.Fatal("quería error por 002_bad.sql")
	}
	if applied != 1 {
		t.Fatalf("applied = %d, quería 1 (solo 001)", applied)
	}

	// 001 quedó aplicada y registrada; 002 se revirtió entera (ni la tabla
	// ni el registro).
	var versions []string
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatalf("leer schema_migrations: %v", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		versions = append(versions, v)
	}
	if len(versions) != 1 || versions[0] != "001_ok.sql" {
		t.Fatalf("schema_migrations = %v, quería [001_ok.sql]", versions)
	}
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'dos')").Scan(&exists); err != nil {
		t.Fatalf("consultar tabla dos: %v", err)
	}
	if exists {
		t.Fatal("la tabla dos existe: la transacción de 002_bad.sql no se revirtió")
	}

	// Corregida la 002, un reintento retoma desde ella sin tocar la 001.
	fsys["002_bad.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE dos (id int);")}
	applied, err = migrate.Apply(ctx, dsn, fsys, discard())
	if err != nil {
		t.Fatalf("reintento: %v", err)
	}
	if applied != 1 {
		t.Fatalf("reintento: applied = %d, quería 1 (solo 002)", applied)
	}
}

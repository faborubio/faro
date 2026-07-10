// Package migrations embebe los .sql del esquema en el binario (AUD-002):
// en VibeNest no hay psql ni shell garantizados, así que cmd/faro las aplica
// al boot vía internal/migrate. scripts/migrate.sh sigue siendo válido para
// migrar a mano — ambos comparten el contrato de schema_migrations.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS

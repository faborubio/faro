// Faro — API pública + dashboard de indicadores económicos de Chile.
// Un solo binario: scheduler de refresco diario + API HTTP + dashboard
// (SAD §4). El servidor llega en la Fase 1; por ahora el binario solo
// declara que está en construcción.
package main

import (
	"log/slog"
	"os"
)

func main() {
	slog.Info("faro en construcción", "fase", 0, "detalle", "cimientos: el servidor HTTP llega en la Fase 1")
	os.Exit(0)
}

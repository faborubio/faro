-- 002 — Token de alertas único e indexado (Fase 3, ADR-006).
-- El token es el único handle de gestión de una alerta: la API lo busca en
-- cada GET/DELETE (el índice evita el seq scan) y jamás deben existir dos
-- alertas con el mismo (la unicidad respalda al crypto/rand de la app).
CREATE UNIQUE INDEX alerts_token_key ON alerts (token);

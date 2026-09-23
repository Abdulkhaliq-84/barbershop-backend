-- Extensions every module relies on (see docs/architecture/persistence.md):
--   postgis    geography points, radius search and distance ordering (discovery)
--   btree_gist lets one EXCLUDE constraint mix "=" and "&&" (no double booking)
--   pg_trgm    fuzzy text search on Arabic/English names (discovery)

-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
-- Intentionally keeps the extensions. They are shared database infrastructure
-- and may predate this migration (the postgis/postgis image pre-installs
-- postgis with postgis_topology and postgis_tiger_geocoder depending on it).
-- Dropping postgis would also destroy every geography column; removing an
-- extension is a deliberate manual decision, never a rollback side effect.
SELECT 1;

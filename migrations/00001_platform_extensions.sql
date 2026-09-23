-- Extensions every module relies on (see docs/architecture/persistence.md):
--   postgis    geography points, radius search and distance ordering (discovery)
--   btree_gist lets one EXCLUDE constraint mix "=" and "&&" (no double booking)
--   pg_trgm    fuzzy text search on Arabic/English names (discovery)

-- +goose Up
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS btree_gist;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
DROP EXTENSION IF EXISTS pg_trgm;
DROP EXTENSION IF EXISTS btree_gist;
DROP EXTENSION IF EXISTS postgis;

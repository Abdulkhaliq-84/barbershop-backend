# ADR-0005: PostgreSQL + PostGIS as the only stateful dependency

- Status: Accepted · Date: 2026-09-23

## Context
We need relational data with strong consistency (bookings), geo search (nearby branches), fuzzy text
search (Arabic/English names), a job queue (reminders, event delivery) and must run on localhost.

## Decision
PostgreSQL with extensions **PostGIS** (geography points, `ST_DWithin`, KNN ordering), **btree_gist**
(exclusion constraints), **pg_trgm** (fuzzy search). Jobs and outbox also in Postgres via River
(ADR-0009). No Redis, no Elasticsearch in v1.

## Consequences
- One container locally; one managed database in production.
- Search quality is "good enough" for v1; a dedicated search engine can be added behind the
  Discovery module's read model later without touching other modules.

# ADR-0004: Shared-schema multi-tenancy with `business_id`

- Status: Accepted · Date: 2026-09-23

## Context
Tenants are businesses. Options: database-per-tenant, schema-per-tenant, shared tables with a tenant
column. The customer side is a marketplace: map/city search must query **all** tenants at once. With
per-tenant schemas every migration runs N times (and can drift: "works for old tenants, broken for new
ones"), and cross-tenant search needs UNIONs or a separate index.

## Decision
Shared tables; every tenant-owned row has `business_id`. Isolation enforced at the API (tenant in the
path), the application layer (membership check first), and repositories (tenant ID is a required
parameter of every scoped query). Add Postgres Row-Level Security in M8 as defence in depth.
Postgres schemas are used per **module**, not per tenant.

## Consequences
- One migration path, cheap cross-tenant search, simple operations.
- A missing `business_id` filter would be a data leak → required parameters, tests for cross-tenant
  access, and RLS later.
- Very large tenants can be moved to dedicated infrastructure later if ever needed.

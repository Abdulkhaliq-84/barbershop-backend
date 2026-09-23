# ADR-0002: DDD modular monolith

- Status: Accepted · Date: 2026-09-23

## Context
The domain has clear bounded contexts (IAM, Business, Catalog, Scheduling, Booking, Discovery,
Billing, Notification, Media). One developer, early product, local hosting. Microservices would add
network failures, distributed transactions, deployment and observability cost before there is any
scaling need.

## Decision
Build one Go binary composed of modules, one module per bounded context, each with
`domain` / `app` / `adapters` layers. Modules communicate only through a small public API
(root package) or domain events. Each module owns a Postgres schema. Boundaries are enforced by
lint rules in CI.

## Consequences
- Simple to run, debug and deploy; transactions stay local.
- Boundaries are real, so a module (e.g. `notification`) can be extracted later if needed.
- Requires discipline: no "just this once" cross-module table reads — CI enforces imports, review enforces SQL.

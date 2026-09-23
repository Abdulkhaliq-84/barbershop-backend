# ADR-0009: Domain events via transactional outbox on River

- Status: Accepted · Date: 2026-09-23

## Context
Modules react to each other's events (booking → notification, business/catalog/scheduling →
discovery). Publishing after commit can lose events on crash; publishing before commit can announce
things that never happened. We also need scheduled jobs (reminders, expiries) and retries.

## Decision
Aggregates record domain events; repositories insert them as **River** jobs in the **same transaction**
as the state change (`InsertTx`). River workers dispatch each event to subscribed handlers in other
modules. Delivery is at-least-once, so handlers are idempotent (upserts, event IDs, status re-checks).
The same River instance runs scheduled and periodic jobs.

## Consequences
- No lost or phantom events; no extra broker (Redis/Kafka) in v1.
- Eventual consistency between modules (e.g. discovery updates a moment after a branch changes) — acceptable.
- If we later need a real broker, a River worker can forward events to it without changing modules.

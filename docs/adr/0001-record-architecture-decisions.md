# ADR-0001: Record architecture decisions

- Status: Accepted · Date: 2026-09-23

## Context
This project is also a learning journey from Node.js to Go, built with AI assistance. Decisions made
in chat get lost; future contributors (and future AI sessions) need to know *why* things are the way they are.

## Decision
Keep lightweight ADRs in `docs/adr/`, numbered, immutable once accepted. Any change to architecture,
stack, data model conventions or security model needs an ADR in the same PR.

## Consequences
- A reviewer (human or AI) can check a PR against recorded decisions.
- Slight overhead per significant decision — accepted.

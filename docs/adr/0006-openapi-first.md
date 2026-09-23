# ADR-0006: OpenAPI-first REST contract

- Status: Accepted · Date: 2026-09-23

## Context
The Flutter app will be built after the backend, possibly by other developers. Hand-written DTOs on
both sides drift. gRPC/Connect gives typed contracts but is heavier to learn and to debug from tools
like Postman.

## Decision
`api/openapi.yaml` (OpenAPI 3.0) is the source of truth. Go server interfaces and models are generated
with **oapi-codegen** (strict server, chi). The Flutter client is generated (openapi-generator,
`dart-dio`). Requests are validated against the schema at the edge. Changes to the API start as spec
changes in the PR.

## Consequences
- Contract reviewed before code; Flutter gets a typed client for free; docs are always current.
- Generated code committed and checked in CI.
- The spec must be kept tidy (shared components, error schema, pagination schema).

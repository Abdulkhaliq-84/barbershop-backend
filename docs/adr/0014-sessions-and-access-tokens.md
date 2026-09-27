# ADR-0014: Stateless access tokens, rotating refresh sessions, auth declared in the API spec

- Status: Accepted · Date: 2026-09-27 · Details the token part of [ADR-0007](0007-auth-phone-otp-jwt.md)

## Context
ADR-0007 chose a short-lived Ed25519 access JWT plus an opaque refresh token that rotates on every use,
with reuse revoking the session. M2.3 builds it, which leaves four questions: how requests are
authenticated, how refresh stays correct under concurrency, how other modules learn who is calling,
and what logging out or blocking a user means for tokens already issued.

## Decision
- **Access token**
  - An EdDSA (Ed25519) JWT that lives 15 minutes.
  - Claims: `iss`, `aud`, `sub` (user ID), `sid` (session ID), `platform_role`, `iat`, `nbf`, `exp`, `jti`.
  - The header carries `kid`, a fingerprint of the public key, so a future key rotation can tell keys apart.
  - The signing key is derived from `TOKEN_SIGNING_SECRET` (≥ 32 bytes; the seed is SHA-256 of the
    secret). Verification pins the algorithm, issuer, audience and key ID, and allows 30 s of clock skew.
- **Stateless checks**
  - Verifying an access token needs no database. After logout or blocking, an access token keeps
    working until it expires, at most 15 minutes.
  - Refresh is refused at once, and `GET /v1/me` rejects blocked users by reading the database.
- **Refresh token**
  - Format: `rt_` followed by 256 random bits (base64url). Only its SHA-256 is stored.
  - Each use marks it used and issues the next one in the same session (`iam.sessions` 1–n
    `iam.refresh_tokens`).
  - Presenting a used token revokes the whole session (`refresh_token_reused`).
  - The token row and its session row are locked (`SELECT … FOR UPDATE OF t, s`), so parallel
    refreshes take turns.
  - Clients must share one refresh between concurrent requests. A second refresh with the same token
    looks exactly like theft.
- **Auth lives in the contract**
  - `api/openapi.yaml` declares a `bearerAuth` scheme globally, so every operation is **protected by
    default**. Public operations say `security: []`.
  - The `bearerAuth` middleware verifies the header when present. The spec validator's
    `AuthenticationFunc` rejects protected operations without a principal (`401 unauthorized` +
    `WWW-Authenticate: Bearer …`). A bad token on a public operation is ignored.
- **The caller** is an `auth.Principal` (user ID, session ID, platform role) in the request context
  (`internal/platform/auth`).
  - Any module reads it with `auth.PrincipalFrom(ctx)` without importing `iam`.
  - Business roles are not in the token. They are checked per request against memberships
    (ADR-0007, M3).

## Consequences
- **Performance:** authentication costs one signature check (microseconds) per request, and no shared session store is needed.
- **Revocation window:** logout and blocking have a ≤ 15-minute delay for access tokens. If that ever
  matters, a per-request session check can be added in the `Authenticate` function without changing
  handlers.
- **Safe by default:** forgetting `security: []` fails closed. A new endpoint is private until someone
  deliberately makes it public, in the reviewed spec.
- **Deferred:**
  - key rotation (`kid` is ready) and a JWKS endpoint, until another service must verify tokens
  - a cleanup job for old refresh tokens (River, M3)
  - "sign out everywhere" and a device list

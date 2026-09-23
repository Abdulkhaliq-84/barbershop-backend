# ADR-0007: Own auth — phone OTP + JWT + rotating refresh tokens

- Status: Accepted · Date: 2026-09-23

## Context
Saudi users expect phone-number login. Managed providers (Firebase Auth) reduce work but add vendor
coupling and hide a core learning area. Roles differ per business (owner/manager/barber).

## Decision
- Login: phone number → 6-digit OTP via SMS (console adapter in dev). OTP stored as an HMAC hash,
  TTL 5 min, max 5 attempts, resend cooldown 60 s, hourly caps per phone and IP.
- Tokens: short-lived access JWT (15 min, **EdDSA/Ed25519**, claims: `sub`, `platform_role`, `exp`, `jti`)
  + opaque refresh token (30 days) stored hashed, **rotated on every use**; reuse of a rotated token
  revokes the whole session family.
- Business roles are *not* in the JWT; they're checked against memberships per request (so revoking
  a barber takes effect immediately).

## Consequences
- Full control and a great Go learning area (crypto, security testing).
- We own the security: rate limits, audit logs, careful review, `gosec` in CI.
- SMS costs per login — mitigated by long-lived refresh tokens.

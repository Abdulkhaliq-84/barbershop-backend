# ADR-0011: CI/CD on GitHub Actions — build once, publish versioned images, deploy later

- Status: Accepted · Date: 2026-09-23

## Context
The owner wants to learn the full pipeline lifecycle, not just "run tests on PR". Hosting is still
localhost, so there is no server to deploy to yet — but the delivery half of the pipeline (artifact,
scanning, provenance, versioning, releases) is independent of where it will run.

## Decision
- **GitHub Actions**, workflows in `.github/workflows/`, pinned by commit SHA, least-privilege `permissions:`.
- **CI** on every PR: format/lint (incl. module-boundary rules), generated-code drift, tests with `-race`
  against a PostGIS service container, build, security scans (govulncheck, gitleaks, dependency review, CodeQL).
- **Trunk-based** development, **Conventional Commits** PR titles, squash merges, branch ruleset on `main`.
- **CD** on push to `main`: build the Docker image **once** (multi-arch, distroless, nonroot), scan it
  (Trivy), attach provenance + SBOM attestations, push to **GHCR** as `:sha-<commit>`.
- **Releases** with **release-please**: SemVer from commit types, `CHANGELOG.md`, GitHub Releases; the
  image built for the release commit is **promoted** (re-tagged) to `:vX.Y.Z`, `:X.Y`, `:latest` — never rebuilt.
- **Deploy stage deferred** until hosting is chosen; it will add staging (auto) → production (manual
  approval), migrations before rollout, smoke tests and rollback-by-redeploy.

## Consequences
- Every merge yields a runnable, verifiable artifact from day one; the owner practises the lifecycle early.
- Supply-chain hygiene (pinned actions, scans, attestations) is built in, not retrofitted.
- Adding deployment later is additive (one workflow), not a redesign.
- More workflow YAML to maintain — offset by Dependabot updates and `make check` parity locally.

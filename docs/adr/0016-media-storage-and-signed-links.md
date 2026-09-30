# ADR-0016: Media — files on a storage port, private files through signed links

- Status: Accepted · Date: 2026-09-30 · Builds on [ADR-0015](0015-business-tenancy-and-authorization.md)

## Context
M3.3 adds file uploads, starting with the CR certificate that a platform reviewer checks before
approving a business. CR documents are private: only the owner and platform admins may see them.
Later the same module will hold public files (logos, branch photos, avatars). We need to decide:

- where the bytes live
- how a file is checked
- how a private file reaches the app without our API streaming it through an authenticated JSON call
- how the business module uses media without depending on its internals

## Decision
- **A `media` module owns every file.** `media.objects` records each one: purpose, sniffed type,
  size, SHA-256, uploader and time. The bytes go through a `Storage` port:
  - **local disk** now (`MEDIA_DIR`, a persistent volume)
  - an **S3-compatible** adapter later, with no change elsewhere
- **Storage keys are object IDs, never user file names.** The disk adapter:
  - works inside an `os.Root`, so no key, `..` or symlink can reach outside the media directory
  - writes to a temporary file and renames it into place
  - keeps files readable only by the server (mode 0600)
- **Files are checked by content, not by claims.**
  - The type comes from the first bytes (magic numbers). Only PDF, JPEG and PNG are accepted, so no
    HTML, SVG or executables.
  - Files are at most 10 MiB, counted while streaming. Uploads use `application/octet-stream`.
    The body cap follows the operation in the spec, never the client's Content-Type: 10 MiB for
    an operation whose body is `format: binary`, 1 MiB for everything else. Too big → `413`.
- **Private files are served through signed links.**
  - `GET /v1/media/{id}?expires=…&signature=…` is public: the link is the permission.
  - The signature is HMAC-SHA256 over the file ID and expiry, keyed by `MEDIA_SIGNING_SECRET`. It is
    checked in constant time before any database access.
  - Links live 5 minutes. The use case that hands one out decides who may have it (the owner now,
    reviewers in M3.4).
  - Files are always sent as `application/octet-stream` attachments with `nosniff` and `no-store`,
    so a browser saves them and never renders them as a page on our domain.
- **Other modules use media only through its root package**, wrapped in their own anti-corruption
  layer.
  - `business` declares a `DocumentFiles` port in its own words, implemented by
    `business/adapters/acl` over `media.Module`.
  - Media's errors are translated there. depguard enforces the boundary both ways.
- **No cross-module transaction.** An upload stores the file (media), then attaches it to the
  business (business, under a row lock that also enforces the document limit). If attaching fails,
  the file is deleted. A crash in between leaves an unreferenced file, never a reference to a
  missing one.

## Consequences
- Moving to S3/R2 later means a new adapter. The links may then become S3 presigned URLs, and the
  `download_url` field keeps its meaning.
- A leaked link works for at most 5 minutes. There is no revocation list; rotating
  `MEDIA_SIGNING_SECRET` kills every outstanding link.
- *Changed after review (M4):* the request validator used to buffer an upload in memory (three
  copies, and the security check a fourth) before the handler streamed it. Now an upload's body
  goes around the validator: its token, path and query are still checked, and its Content-Type
  after that, but the bytes reach media untouched. File transfers get two minutes of read and write
  time instead of the server's 15 s (`http.ResponseController`), enough for 10 MiB on a slow
  mobile link.
- Unreferenced files (crashes, refused attaches that failed to clean up) need a periodic sweep job
  once River arrives (M3.4).
- Deleting a user's data (PDPL) must include their files; the SHA-256 lets us spot duplicates and
  verify integrity.

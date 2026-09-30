-- media: stored files (docs/architecture/domain-model.md §3.9).
-- A row describes one file; the bytes live in object storage (local disk in
-- development, S3-compatible later) under the object's ID — never under a
-- name a user chose. Private files are served only through short-lived
-- signed links (ADR-0016).

-- +goose Up
CREATE SCHEMA media;

CREATE TABLE media.objects (
    id           uuid PRIMARY KEY,               -- UUIDv7, generated in Go; also the storage key
    purpose      text        NOT NULL CHECK (purpose IN ('cr_document')),
    content_type text        NOT NULL CHECK (content_type IN ('application/pdf', 'image/jpeg', 'image/png')),
    size_bytes   bigint      NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760),
    sha256       bytea       NOT NULL CHECK (length(sha256) = 32),
    created_by   uuid        NOT NULL,           -- iam user; no cross-schema foreign key (ADR-0015)
    created_at   timestamptz NOT NULL
);

-- +goose Down
DROP TABLE media.objects;
DROP SCHEMA media;

-- business: the documents an owner submits for verification (CR certificate).
-- The file itself belongs to the media module; this table only says which
-- business it belongs to and what it is. object_id references media.objects
-- by value only: no foreign keys across module schemas (ADR-0015).

-- +goose Up
CREATE TABLE business.verification_documents (
    object_id    uuid PRIMARY KEY,
    business_id  uuid        NOT NULL REFERENCES business.businesses (id),
    kind         text        NOT NULL CHECK (kind IN ('cr_certificate')),
    content_type text        NOT NULL,
    size_bytes   bigint      NOT NULL CHECK (size_bytes > 0),
    uploaded_by  uuid        NOT NULL,
    uploaded_at  timestamptz NOT NULL
);

CREATE INDEX verification_documents_business_id_idx ON business.verification_documents (business_id, uploaded_at);

-- +goose Down
DROP TABLE business.verification_documents;

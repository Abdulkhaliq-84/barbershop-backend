-- business: the platform's review of a business (docs/architecture/domain-model.md §3.2).
-- Expand-only: new nullable columns, so running code keeps working while this
-- is applied.

-- +goose Up
ALTER TABLE business.businesses
    ADD COLUMN submitted_at     timestamptz,
    ADD COLUMN reviewed_at      timestamptz,
    ADD COLUMN reviewed_by      uuid,          -- iam user (platform admin); no cross-schema foreign key
    ADD COLUMN rejection_reason text NOT NULL DEFAULT '' CHECK (char_length(rejection_reason) <= 500),
    -- A business under review, approved or suspended has been submitted.
    ADD CONSTRAINT businesses_submitted_check
        CHECK (status IN ('draft') OR submitted_at IS NOT NULL),
    -- A reviewer is recorded with every decision.
    ADD CONSTRAINT businesses_reviewed_check
        CHECK ((reviewed_at IS NULL) = (reviewed_by IS NULL));

-- The admin review queue: businesses of one status, oldest submission first.
CREATE INDEX businesses_review_queue_idx ON business.businesses (status, submitted_at, id)
    WHERE status <> 'draft';

-- +goose Down
DROP INDEX business.businesses_review_queue_idx;
ALTER TABLE business.businesses
    DROP CONSTRAINT businesses_reviewed_check,
    DROP CONSTRAINT businesses_submitted_check,
    DROP COLUMN rejection_reason,
    DROP COLUMN reviewed_by,
    DROP COLUMN reviewed_at,
    DROP COLUMN submitted_at;

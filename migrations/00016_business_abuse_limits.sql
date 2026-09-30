-- business: indexes for the invitation and registration limits
-- (docs/adr/0018-staff-invitations.md, "Changed after review").

-- +goose Up
-- Invitations to one phone from every business in the last day, and one
-- business's invitations in the last day.
CREATE INDEX invitations_phone_created_idx ON business.invitations (phone, created_at);
CREATE INDEX invitations_business_created_idx ON business.invitations (business_id, created_at);

-- +goose Down
DROP INDEX business.invitations_business_created_idx;
DROP INDEX business.invitations_phone_created_idx;

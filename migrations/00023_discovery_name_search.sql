-- discovery: searching by name (ADR-0029). search_text is the branch's
-- Arabic and English names, normalised by discovery (domain.Normalize) when
-- it saves the copy; searches are normalised the same way. A trigram index
-- finds names that contain the search, or are close to it (a letter off).

-- +goose Up
ALTER TABLE discovery.branch_listings
    ADD COLUMN search_text text NOT NULL DEFAULT '';

-- Only listed branches are ever searched.
CREATE INDEX branch_listings_search_idx
    ON discovery.branch_listings USING gin (search_text gin_trgm_ops)
    WHERE listed;

-- +goose Down
DROP INDEX discovery.branch_listings_search_idx;
ALTER TABLE discovery.branch_listings DROP COLUMN search_text;

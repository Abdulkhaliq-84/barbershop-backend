-- discovery: searching near a place (ADR-0028). The location as a PostGIS
-- geography, kept in step with latitude and longitude by the database, and a
-- GiST index so "within 10 km" reads a few index pages instead of every row.

-- +goose Up
ALTER TABLE discovery.branch_listings
    ADD COLUMN location geography(Point, 4326)
        GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography) STORED;

-- Only listed branches are ever searched.
CREATE INDEX branch_listings_location_idx
    ON discovery.branch_listings USING gist (location)
    WHERE listed;

-- +goose Down
DROP INDEX discovery.branch_listings_location_idx;
ALTER TABLE discovery.branch_listings DROP COLUMN location;

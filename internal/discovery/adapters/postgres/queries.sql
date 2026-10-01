-- discovery's queries. They touch only the discovery schema.

-- name: KeepListing :execrows
-- Saves discovery's copy of a branch, unless the copy already holds this
-- version or a newer one: events can arrive twice and out of order.
INSERT INTO discovery.branch_listings (
    branch_id, business_id, version, listed, name_ar, name_en, city_code, district, address,
    latitude, longitude, phone, timezone, updated_at
) VALUES (
    @branch_id, @business_id, @version, @listed, @name_ar, @name_en, @city_code, @district, @address,
    @latitude, @longitude, @phone, @timezone, @updated_at
)
ON CONFLICT (branch_id) DO UPDATE SET
    business_id = excluded.business_id,
    version     = excluded.version,
    listed      = excluded.listed,
    name_ar     = excluded.name_ar,
    name_en     = excluded.name_en,
    city_code   = excluded.city_code,
    district    = excluded.district,
    address     = excluded.address,
    latitude    = excluded.latitude,
    longitude   = excluded.longitude,
    phone       = excluded.phone,
    timezone    = excluded.timezone,
    updated_at  = excluded.updated_at
WHERE discovery.branch_listings.version < excluded.version;

-- name: ListingsInCity :many
-- A page of a city's listed branches, by Arabic name then ID, after the
-- last one of the previous page (none for the first page).
SELECT branch_id, business_id, version, listed, name_ar, name_en, city_code, district, address,
       latitude, longitude, phone, timezone, updated_at
FROM discovery.branch_listings
WHERE listed AND city_code = @city_code
  AND (sqlc.narg(after_name)::text IS NULL
       OR (name_ar, branch_id) > (sqlc.narg(after_name)::text, sqlc.narg(after_id)::uuid))
ORDER BY name_ar, branch_id
LIMIT @page_size;


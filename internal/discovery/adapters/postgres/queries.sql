-- discovery's queries. They touch only the discovery schema.

-- name: KeepListing :execrows
-- Saves discovery's copy of a branch, unless the copy already holds this
-- version or a newer one: events can arrive twice and out of order.
INSERT INTO discovery.branch_listings (
    branch_id, business_id, version, listed, name_ar, name_en, city_code, district, address,
    latitude, longitude, phone, timezone, updated_at, search_text
) VALUES (
    @branch_id, @business_id, @version, @listed, @name_ar, @name_en, @city_code, @district, @address,
    @latitude, @longitude, @phone, @timezone, @updated_at, @search_text
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
    updated_at  = excluded.updated_at,
    search_text = excluded.search_text
WHERE discovery.branch_listings.version < excluded.version;

-- name: ListingsInCity :many
-- A page of a city's listed branches, by Arabic name then ID, after the
-- last one of the previous page (none for the first page). Like every
-- search, only branches offering a service (in category, if given), each
-- with the least one costs (price_from) and whether it is open at the
-- instant at, in its own time zone (open_now); open_only keeps only those.
SELECT l.branch_id, l.business_id, l.version, listed, name_ar, name_en, city_code, district, address,
       latitude, longitude, phone, timezone, l.updated_at,
       (SELECT min(s.price_from) FROM discovery.branch_services s
        WHERE s.branch_id = l.branch_id AND s.offered
          AND (sqlc.narg(category)::text IS NULL OR s.category_code = sqlc.narg(category)::text))::bigint AS price_from,
       coalesce(discovery.open_at(h.open, @at::timestamptz, l.timezone), false)::bool AS open_now
FROM discovery.branch_listings l
LEFT JOIN discovery.branch_hours h ON h.branch_id = l.branch_id
WHERE listed AND city_code = @city_code
  AND (NOT @open_only::bool OR discovery.open_at(h.open, @at::timestamptz, l.timezone))
  AND EXISTS (SELECT FROM discovery.branch_services s
              WHERE s.branch_id = l.branch_id AND s.offered
                AND (sqlc.narg(category)::text IS NULL OR s.category_code = sqlc.narg(category)::text))
  AND (sqlc.narg(after_name)::text IS NULL
       OR (name_ar, l.branch_id) > (sqlc.narg(after_name)::text, sqlc.narg(after_id)::uuid))
ORDER BY name_ar, l.branch_id
LIMIT @page_size;


-- name: ListingsNear :many
-- A page of the listed branches within radius_m metres of a point, nearest
-- first, then by ID, after the last one of the previous page (none for the
-- first page); optionally only one city's. Distances are on a sphere: <->
-- and ST_DWithin(…, false) agree, so the radius, the order and the cursor
-- all use the same number. The GiST index finds the candidates. sort_key
-- is the distance in metres (the same name as ListingsMatching's, so the
-- two queries share a row type). Offering a service, price_from and
-- open_now as in ListingsInCity.
SELECT l.branch_id, l.business_id, l.version, listed, name_ar, name_en, city_code, district, address,
       latitude, longitude, phone, timezone, l.updated_at,
       (SELECT min(s.price_from) FROM discovery.branch_services s
        WHERE s.branch_id = l.branch_id AND s.offered
          AND (sqlc.narg(category)::text IS NULL OR s.category_code = sqlc.narg(category)::text))::bigint AS price_from,
       coalesce(discovery.open_at(h.open, @at::timestamptz, l.timezone), false)::bool AS open_now,
       (location <-> ST_MakePoint(@lng::float8, @lat::float8)::geography)::float8 AS sort_key
FROM discovery.branch_listings l
LEFT JOIN discovery.branch_hours h ON h.branch_id = l.branch_id
WHERE listed
  AND ST_DWithin(location, ST_MakePoint(@lng::float8, @lat::float8)::geography, @radius_m::float8, false)
  AND (NOT @open_only::bool OR discovery.open_at(h.open, @at::timestamptz, l.timezone))
  AND EXISTS (SELECT FROM discovery.branch_services s
              WHERE s.branch_id = l.branch_id AND s.offered
                AND (sqlc.narg(category)::text IS NULL OR s.category_code = sqlc.narg(category)::text))
  AND (sqlc.narg(city_code)::text IS NULL OR city_code = sqlc.narg(city_code)::text)
  AND (sqlc.narg(text)::text IS NULL
       OR search_text LIKE '%' || sqlc.narg(text_like)::text || '%'
       OR sqlc.narg(text)::text <% search_text)
  AND (sqlc.narg(after_distance)::float8 IS NULL
       OR (location <-> ST_MakePoint(@lng::float8, @lat::float8)::geography, l.branch_id)
          > (sqlc.narg(after_distance)::float8, sqlc.narg(after_id)::uuid))
ORDER BY location <-> ST_MakePoint(@lng::float8, @lat::float8)::geography, l.branch_id
LIMIT @page_size;

-- name: ListingsMatching :many
-- A page of the listed branches whose names match a search (normalised),
-- best match first, then by ID, after the last one of the previous page
-- (none for the first page); optionally only one city's. A name matches if
-- it contains the search, or a part of it is close to the search
-- (word_similarity at least pg_trgm.word_similarity_threshold, which the
-- caller sets). The trigram index finds both. sort_key is the match, 0–1.
-- Offering a service, price_from and open_now as in ListingsInCity.
SELECT l.branch_id, l.business_id, l.version, listed, name_ar, name_en, city_code, district, address,
       latitude, longitude, phone, timezone, l.updated_at,
       (SELECT min(s.price_from) FROM discovery.branch_services s
        WHERE s.branch_id = l.branch_id AND s.offered
          AND (sqlc.narg(category)::text IS NULL OR s.category_code = sqlc.narg(category)::text))::bigint AS price_from,
       coalesce(discovery.open_at(h.open, @at::timestamptz, l.timezone), false)::bool AS open_now,
       word_similarity(@text::text, search_text)::float8 AS sort_key
FROM discovery.branch_listings l
LEFT JOIN discovery.branch_hours h ON h.branch_id = l.branch_id
WHERE listed
  AND (search_text LIKE '%' || @text_like::text || '%' OR @text::text <% search_text)
  AND (NOT @open_only::bool OR discovery.open_at(h.open, @at::timestamptz, l.timezone))
  AND EXISTS (SELECT FROM discovery.branch_services s
              WHERE s.branch_id = l.branch_id AND s.offered
                AND (sqlc.narg(category)::text IS NULL OR s.category_code = sqlc.narg(category)::text))
  AND (sqlc.narg(city_code)::text IS NULL OR city_code = sqlc.narg(city_code)::text)
  AND (sqlc.narg(after_score)::float8 IS NULL
       OR word_similarity(@text::text, search_text)::float8 < sqlc.narg(after_score)::float8
       OR (word_similarity(@text::text, search_text)::float8 = sqlc.narg(after_score)::float8
           AND l.branch_id > sqlc.narg(after_id)::uuid))
ORDER BY word_similarity(@text::text, search_text)::float8 DESC, l.branch_id
LIMIT @page_size;

-- name: KeepService :execrows
-- Saves discovery's copy of a service, unless the copy already holds this
-- version or a newer one: events can arrive twice and out of order.
INSERT INTO discovery.branch_services (
    service_id, branch_id, business_id, version, offered, category_code, price_from, updated_at,
    name_ar, name_en, duration_minutes, sort_order
) VALUES (
    @service_id, @branch_id, @business_id, @version, @offered, @category_code, @price_from, @updated_at,
    @name_ar, @name_en, @duration_minutes, @sort_order
)
ON CONFLICT (service_id) DO UPDATE SET
    branch_id        = excluded.branch_id,
    business_id      = excluded.business_id,
    version          = excluded.version,
    offered          = excluded.offered,
    category_code    = excluded.category_code,
    price_from       = excluded.price_from,
    updated_at       = excluded.updated_at,
    name_ar          = excluded.name_ar,
    name_en          = excluded.name_en,
    duration_minutes = excluded.duration_minutes,
    sort_order       = excluded.sort_order
WHERE discovery.branch_services.version < excluded.version;

-- name: KeepHours :execrows
-- Saves discovery's copy of a branch's opening hours, unless the copy
-- already holds this version or a newer one. open is the week as text,
-- e.g. '{[540,1260),[1980,2700)}'; intervals the same week as set, as JSON
-- pairs, e.g. [[540,1260],[1980,2700]].
INSERT INTO discovery.branch_hours (branch_id, business_id, version, open, intervals, updated_at)
VALUES (@branch_id, @business_id, @version, (@open::text)::int4multirange, @intervals::jsonb, @updated_at)
ON CONFLICT (branch_id) DO UPDATE SET
    business_id = excluded.business_id,
    version     = excluded.version,
    open        = excluded.open,
    intervals   = excluded.intervals,
    updated_at  = excluded.updated_at
WHERE discovery.branch_hours.version < excluded.version;

-- name: BranchPage :one
-- A listed branch offering a service, as its public page shows it: the
-- listing, the least a service there costs, whether it is open at the
-- instant at, and its week as set ([] if its hours were never heard of).
-- No row: not listed, not offering anything, or no such branch.
SELECT l.branch_id, l.business_id, l.version, listed, name_ar, name_en, city_code, district, address,
       latitude, longitude, phone, timezone, l.updated_at,
       (SELECT min(s.price_from) FROM discovery.branch_services s
        WHERE s.branch_id = l.branch_id AND s.offered)::bigint AS price_from,
       coalesce(discovery.open_at(h.open, @at::timestamptz, l.timezone), false)::bool AS open_now,
       coalesce(h.intervals, '[]')::jsonb AS intervals
FROM discovery.branch_listings l
LEFT JOIN discovery.branch_hours h ON h.branch_id = l.branch_id
WHERE l.branch_id = @branch_id AND listed
  AND EXISTS (SELECT FROM discovery.branch_services s WHERE s.branch_id = l.branch_id AND s.offered);

-- name: BranchMenu :many
-- The services a branch offers, as its menu shows them: by sort order, then
-- Arabic name in code point order (the same on every machine), then ID.
SELECT service_id, category_code, name_ar, name_en, duration_minutes, price_from
FROM discovery.branch_services
WHERE branch_id = @branch_id AND offered
ORDER BY sort_order, name_ar COLLATE "C", service_id;

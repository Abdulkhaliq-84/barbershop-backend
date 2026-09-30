-- catalog's queries. Only the catalog schema is touched here, and every
-- query is scoped by business and branch.

-- name: InsertService :exec
INSERT INTO catalog.services (
    id, business_id, branch_id, category_code, name_ar, name_en, description_ar, description_en,
    duration_minutes, price_amount, price_currency, active, sort_order, version, created_at, updated_at
) VALUES (
    @id, @business_id, @branch_id, @category_code, @name_ar, @name_en, @description_ar, @description_en,
    @duration_minutes, @price_amount, @price_currency, @active, @sort_order, @version, @created_at, @updated_at
);

-- name: ServicesByBranch :many
SELECT * FROM catalog.services
WHERE business_id = $1 AND branch_id = $2
ORDER BY sort_order, created_at, id;

-- name: ServiceForUpdate :one
SELECT * FROM catalog.services
WHERE business_id = $1 AND branch_id = $2 AND id = $3
FOR UPDATE;

-- name: UpdateService :execrows
UPDATE catalog.services SET
    category_code = @category_code, name_ar = @name_ar, name_en = @name_en,
    description_ar = @description_ar, description_en = @description_en,
    duration_minutes = @duration_minutes, price_amount = @price_amount, price_currency = @price_currency,
    active = @active, sort_order = @sort_order, version = @version, updated_at = @updated_at
WHERE business_id = @business_id AND branch_id = @branch_id AND id = @id AND version = @expected_version;

-- name: OfferingsByBranch :many
SELECT o.* FROM catalog.service_offerings o
JOIN catalog.services s ON s.business_id = o.business_id AND s.id = o.service_id
WHERE s.business_id = $1 AND s.branch_id = $2
ORDER BY o.service_id, o.staff_id;

-- name: OfferingsByService :many
SELECT * FROM catalog.service_offerings
WHERE business_id = $1 AND service_id = $2
ORDER BY staff_id;

-- name: DeleteOfferings :exec
DELETE FROM catalog.service_offerings WHERE business_id = $1 AND service_id = $2;

-- name: InsertOffering :exec
INSERT INTO catalog.service_offerings (business_id, service_id, staff_id, price_amount, price_currency, duration_minutes)
VALUES (@business_id, @service_id, @staff_id, sqlc.narg(price_amount), sqlc.narg(price_currency), sqlc.narg(duration_minutes));

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
WHERE id = @id AND version = @expected_version;

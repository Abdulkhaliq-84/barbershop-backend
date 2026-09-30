-- billing's queries. Only the billing schema is touched here.

-- name: InsertSubscriptionIfAbsent :execrows
INSERT INTO billing.subscriptions (business_id, plan_code, status, current_period_end, created_at)
VALUES ($1, $2, 'trialing', $3, $4)
ON CONFLICT (business_id) DO NOTHING;

-- name: SubscriptionByBusiness :one
SELECT * FROM billing.subscriptions WHERE business_id = $1;

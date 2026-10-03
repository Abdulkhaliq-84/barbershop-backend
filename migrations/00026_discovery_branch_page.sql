-- discovery: what a branch's public page shows (ADR-0032). The services' copy
-- gains what the menu needs, and the hours' copy keeps the intervals as the
-- owner set them: the multirange merges back-to-back ones (seven all-day
-- intervals become one week-long range), which is right for "open now" but
-- not for showing the week day by day. Copies kept before this keep the
-- defaults until their next event; nothing is live, so there is no backfill.

-- +goose Up
ALTER TABLE discovery.branch_services
    ADD COLUMN name_ar          text    NOT NULL DEFAULT '',
    ADD COLUMN name_en          text    NOT NULL DEFAULT '',
    ADD COLUMN duration_minutes integer NOT NULL DEFAULT 0 CHECK (duration_minutes >= 0),
    ADD COLUMN sort_order       integer NOT NULL DEFAULT 0;

-- [[start, end], ...] in minutes after Sunday 00:00, branch-local, in the
-- order and shape scheduling sent them.
ALTER TABLE discovery.branch_hours
    ADD COLUMN intervals jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(intervals) = 'array');

-- +goose Down
ALTER TABLE discovery.branch_hours DROP COLUMN intervals;
ALTER TABLE discovery.branch_services
    DROP COLUMN sort_order,
    DROP COLUMN duration_minutes,
    DROP COLUMN name_en,
    DROP COLUMN name_ar;

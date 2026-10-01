-- discovery: when each branch is open, for "open now" (ADR-0031).
-- discovery's own copy of each branch's weekly opening hours, kept from
-- scheduling's events and never written by an API request. No foreign
-- keys: not into scheduling (ADR-0015), and not to branch_listings either,
-- as the hours are set (and their event arrives) before a branch is
-- published.

-- +goose Up
CREATE TABLE discovery.branch_hours (
    branch_id   uuid PRIMARY KEY,
    business_id uuid           NOT NULL,
    version     integer        NOT NULL CHECK (version > 0), -- the calendar's version this copy is of
    -- The week in the branch's own time zone, as minutes after Sunday 00:00:
    -- Sunday 09:00–21:00 is [540,1260). An interval past Saturday midnight
    -- ends after 10080 (a week's minutes). Empty: closed all week.
    open        int4multirange NOT NULL
        CHECK (lower(open) >= 0 AND upper(open) <= 2 * 10080 OR isempty(open)),
    updated_at  timestamptz    NOT NULL                      -- when the hours changed
);

-- Whether a week of opening hours (as above) is open at the instant at, in
-- the time zone tz: it contains the minute of the week at falls on there, or
-- that minute a week later (for an interval past Saturday midnight). Opening
-- at 09:00 is open from 09:00:00; closing at 21:00 is closed from 21:00:00.
-- +goose StatementBegin
CREATE FUNCTION discovery.open_at(open int4multirange, at timestamptz, tz text) RETURNS boolean
    LANGUAGE sql STABLE PARALLEL SAFE
AS $$
    SELECT open @> m OR open @> m + 10080
    FROM (SELECT (extract(dow FROM local) * 1440 + extract(hour FROM local) * 60 + extract(minute FROM local))::int AS m
          FROM (SELECT at AT TIME ZONE tz AS local) AS l) AS w
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION discovery.open_at(int4multirange, timestamptz, text);
DROP TABLE discovery.branch_hours;

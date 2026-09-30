-- scheduling: when a branch is open (docs/architecture/domain-model.md §3.4).
--
-- Opening hours are branch-local wall-clock time (weekday, minute after
-- midnight, length): "we open at 4 pm" stays 4 pm whatever the UTC offset.
-- An interval may run past midnight; it belongs to the day it starts on.
-- business_id and branch_id carry no foreign keys into business (ADR-0015).

-- +goose Up
CREATE SCHEMA scheduling;

CREATE TABLE scheduling.branch_calendars (
    business_id uuid        NOT NULL,
    branch_id   uuid        NOT NULL,
    version     integer     NOT NULL CHECK (version > 0),
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (business_id, branch_id)
);

CREATE TABLE scheduling.opening_hours (
    business_id      uuid     NOT NULL,
    branch_id        uuid     NOT NULL,
    weekday          smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),          -- 0 = Sunday
    start_minute     smallint NOT NULL CHECK (start_minute BETWEEN 0 AND 1435 AND start_minute % 5 = 0),
    duration_minutes smallint NOT NULL CHECK (duration_minutes BETWEEN 5 AND 1440 AND duration_minutes % 5 = 0),
    PRIMARY KEY (business_id, branch_id, weekday, start_minute),
    FOREIGN KEY (business_id, branch_id) REFERENCES scheduling.branch_calendars (business_id, branch_id)
);

-- +goose Down
DROP TABLE scheduling.opening_hours;
DROP TABLE scheduling.branch_calendars;
DROP SCHEMA scheduling;

-- scheduling: when each staff member works, per branch, and their time off
-- (docs/architecture/domain-model.md §3.4, ADR-0021).
--
-- Weekly templates and date overrides are branch-local wall-clock time, like
-- opening hours. Time off is real instants. staff_id, branch_id and
-- business_id carry no foreign keys into business (ADR-0015).

-- +goose Up
CREATE TABLE scheduling.barber_schedules (
    business_id uuid        NOT NULL,
    branch_id   uuid        NOT NULL,
    staff_id    uuid        NOT NULL,
    version     integer     NOT NULL CHECK (version > 0),
    updated_at  timestamptz NOT NULL,
    PRIMARY KEY (business_id, branch_id, staff_id)
);

-- "This person's schedules at their other branches" (the overlap check).
CREATE INDEX barber_schedules_staff_idx ON scheduling.barber_schedules (business_id, staff_id);

CREATE TABLE scheduling.barber_hours (
    business_id      uuid     NOT NULL,
    branch_id        uuid     NOT NULL,
    staff_id         uuid     NOT NULL,
    weekday          smallint NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_minute     smallint NOT NULL CHECK (start_minute BETWEEN 0 AND 1435 AND start_minute % 5 = 0),
    duration_minutes smallint NOT NULL CHECK (duration_minutes BETWEEN 5 AND 1440 AND duration_minutes % 5 = 0),
    PRIMARY KEY (business_id, branch_id, staff_id, weekday, start_minute),
    FOREIGN KEY (business_id, branch_id, staff_id) REFERENCES scheduling.barber_schedules (business_id, branch_id, staff_id)
);

-- A date that doesn't follow the weekly template. With no hours it's a day off.
CREATE TABLE scheduling.schedule_overrides (
    business_id uuid NOT NULL,
    branch_id   uuid NOT NULL,
    staff_id    uuid NOT NULL,
    on_date     date NOT NULL,
    PRIMARY KEY (business_id, branch_id, staff_id, on_date),
    FOREIGN KEY (business_id, branch_id, staff_id) REFERENCES scheduling.barber_schedules (business_id, branch_id, staff_id)
);

CREATE TABLE scheduling.override_hours (
    business_id      uuid     NOT NULL,
    branch_id        uuid     NOT NULL,
    staff_id         uuid     NOT NULL,
    on_date          date     NOT NULL,
    start_minute     smallint NOT NULL CHECK (start_minute BETWEEN 0 AND 1435 AND start_minute % 5 = 0),
    duration_minutes smallint NOT NULL CHECK (duration_minutes BETWEEN 5 AND 1440 AND duration_minutes % 5 = 0),
    PRIMARY KEY (business_id, branch_id, staff_id, on_date, start_minute),
    FOREIGN KEY (business_id, branch_id, staff_id, on_date) REFERENCES scheduling.schedule_overrides (business_id, branch_id, staff_id, on_date)
);

-- Time off is per person, at every branch. The exclusion constraint makes
-- overlapping time off impossible, even from two requests at once.
CREATE TABLE scheduling.time_off (
    id          uuid PRIMARY KEY,
    business_id uuid        NOT NULL,
    staff_id    uuid        NOT NULL,
    starts_at   timestamptz NOT NULL,
    ends_at     timestamptz NOT NULL,
    reason      text        NOT NULL DEFAULT '' CHECK (char_length(reason) <= 200),
    created_at  timestamptz NOT NULL,
    CHECK (ends_at > starts_at AND ends_at - starts_at <= interval '366 days'),
    CONSTRAINT time_off_no_overlap EXCLUDE USING gist (
        business_id WITH =, staff_id WITH =, tstzrange(starts_at, ends_at) WITH &&
    )
);

CREATE INDEX time_off_staff_idx ON scheduling.time_off (business_id, staff_id, ends_at);

-- +goose Down
DROP TABLE scheduling.time_off;
DROP TABLE scheduling.override_hours;
DROP TABLE scheduling.schedule_overrides;
DROP TABLE scheduling.barber_hours;
DROP TABLE scheduling.barber_schedules;

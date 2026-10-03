-- notification: tells people what happened, on their phones (ADR-0033).
-- The devices that can receive pushes, a copy of each branch's name and time
-- zone (kept from business's branch events, to word and time a message), and
-- the log of what was sent, so a retried event never pushes twice. No
-- foreign keys into other modules' schemas (ADR-0015).

-- +goose Up
CREATE SCHEMA notification;

-- A phone or tablet that can receive pushes, for one user at a time. token
-- is the push service's registration token: a credential, never logged.
CREATE TABLE notification.devices (
    id         uuid PRIMARY KEY,
    user_id    uuid        NOT NULL,
    token      text        NOT NULL UNIQUE CHECK (length(token) BETWEEN 32 AND 4096),
    platform   text        NOT NULL CHECK (platform IN ('ios', 'android')),
    locale     text        NOT NULL CHECK (locale IN ('ar', 'en')), -- the app's language on it
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL                                 -- when it last registered
);
-- A user's devices, newest first (pushes, and keeping only the newest few).
CREATE INDEX devices_user_idx ON notification.devices (user_id, updated_at DESC, id DESC);

-- What a message needs to know of a branch: its name and time zone.
CREATE TABLE notification.branches (
    branch_id uuid PRIMARY KEY,
    version   integer NOT NULL CHECK (version > 0), -- the branch's version this copy is of
    name_ar   text    NOT NULL CHECK (name_ar <> ''),
    name_en   text    NOT NULL DEFAULT '',
    timezone  text    NOT NULL
);

-- One row per event pushed to a device: a retried event skips the devices
-- it already reached.
CREATE TABLE notification.deliveries (
    event_id       uuid        NOT NULL,
    device_id      uuid        NOT NULL,
    user_id        uuid        NOT NULL,
    kind           text        NOT NULL,
    appointment_id uuid,
    sent_at        timestamptz NOT NULL,
    PRIMARY KEY (event_id, device_id)
);
CREATE INDEX deliveries_user_idx ON notification.deliveries (user_id, sent_at DESC);

-- +goose Down
DROP SCHEMA notification CASCADE;

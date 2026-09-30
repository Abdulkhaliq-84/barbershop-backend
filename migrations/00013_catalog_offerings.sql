-- catalog: who performs each service, optionally at their own price or
-- duration (docs/architecture/domain-model.md §3.3).
--
-- staff_id points into business.staff_members without a foreign key
-- (ADR-0015); the use case asks business that each one works at the branch.

-- +goose Up
-- A (business_id, id) key, so offerings can require that they belong to a
-- service of the same business.
ALTER TABLE catalog.services ADD CONSTRAINT services_business_id_id_key UNIQUE (business_id, id);

CREATE TABLE catalog.service_offerings (
    business_id      uuid     NOT NULL,
    service_id       uuid     NOT NULL,
    staff_id         uuid     NOT NULL,
    price_amount     bigint   CHECK (price_amount BETWEEN 0 AND 10000000),            -- NULL: the service's price
    price_currency   char(3)  CHECK (price_currency = 'SAR'),
    duration_minutes smallint CHECK (duration_minutes BETWEEN 5 AND 480 AND duration_minutes % 5 = 0), -- NULL: the service's
    PRIMARY KEY (service_id, staff_id),
    FOREIGN KEY (business_id, service_id) REFERENCES catalog.services (business_id, id),
    CHECK ((price_amount IS NULL) = (price_currency IS NULL))
);

-- "Which services does this barber do?" (their profile, availability in M5).
CREATE INDEX service_offerings_staff_idx ON catalog.service_offerings (business_id, staff_id);

-- +goose Down
DROP TABLE catalog.service_offerings;
ALTER TABLE catalog.services DROP CONSTRAINT services_business_id_id_key;

-- +goose Up
CREATE TABLE iam.otp_phone_guards (
 phone text PRIMARY KEY,
 failures smallint NOT NULL CHECK (failures >= 0),
 window_start timestamptz NOT NULL,
 locked_until timestamptz NOT NULL
);

-- +goose Down
DROP TABLE iam.otp_phone_guards;

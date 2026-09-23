-- Runs once, when the database volume is first created.
-- A separate database for `make test-all`, so tests never touch dev data.
CREATE DATABASE barbershop_test OWNER barbershop;

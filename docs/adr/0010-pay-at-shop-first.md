# ADR-0010: Pay at the shop in v1

- Status: Accepted · Date: 2026-09-23

## Context
Online payments (Moyasar / Tap) add merchant onboarding, refunds, reconciliation and compliance work.
Most barbershop customers in the target market already pay in the shop.

## Decision
v1 bookings carry a price snapshot but no payment. No-show risk is handled with booking policies
(cancellation window, max active bookings per customer) and staff-marked no-shows. A `payments`
module (deposits / prepayment, per-shop setting) is designed for later and will plug into booking
through events and a policy on the branch.

## Consequences
- Faster MVP, simpler onboarding for shops.
- No-show exposure until deposits and a reliability score arrive.

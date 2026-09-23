# Mockups — style canvas sources

The source of the **"Barbershop App Style"** design canvas (owner-only link:
https://claude.ai/artifact/7HbE86xoAQSfawdc9g1uxD). Each file is one artboard written as HTML with
inline styles, so the exact values (colours, sizes, radii, spacing, copy) are readable by people and by
AI sessions when building the Figma design or the Flutter screens.

| File | Artboard | Size |
|---|---|---|
| `Foundations.dc.html` | Colour scales, variants A/B, IBM Plex type specimen (AR + EN) | 1440 × 1320 |
| `Components.dc.html` | Buttons, chips & slots, status badges, barber selector, phone + OTP, stripe motif, radius & elevation | 1440 × 1020 |
| `Home.dc.html` | Customer home: map with price pins, glass search, filter chips, nearby sheet, floating nav (AR, RTL) | 390 × 844 |
| `Booking.dc.html` | Branch booking: services, barber (incl. "any barber"), date strip, slots, sticky total (AR, RTL) | 390 × 844 |
| `Business.dc.html` | Business mode "Today": hero with occupancy, bento KPIs, per-barber timeline, FAB, nav (AR, RTL) | 390 × 844 |
| `Confirmed.dc.html` | Booking confirmed ticket with stripe edge (EN, LTR) | 390 × 844 |

Notes

- They use the canvas runtime (`<x-dc>`, `{{holes}}`, `support.js`), so they don't render standalone in a
  browser — open the canvas link to see them. `Home` and `Business` have a `variant` switch
  (`navy` = A · green · navy · white, `green` = B · green · white).
- Sample names, prices and times are illustrative demo data.
- Tokens: `../tokens.json`. Rules and rationale: `../design-system.md`.

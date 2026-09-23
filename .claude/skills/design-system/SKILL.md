---
name: design-system
description: The barbershop app's mobile design system — modern heritage barbershop style, Arabic-first RTL, IBM Plex Sans Arabic + IBM Plex Sans, green · navy · white palette, barber-pole stripe motif, components and screen inventory. Use for ANY UI work in this project — Figma designs or libraries, Flutter ThemeData/widgets/screens, HTML mockups or artifacts, UX copy, colour/typography/spacing decisions, contrast checks, RTL layout, or when an API shape is being derived from a screen — even if the user only says "the design", "the style", "the app", "a screen", "colors" or "fonts".
---

# Barbershop app — design system

Arabic-first, RTL-native mobile app (Flutter, Material 3 customised so it doesn't look stock).
Customer mode + Business mode (owners, managers, barbers) in one app.

## Sources of truth (read before designing)

| What | Where |
|---|---|
| Full rules and rationale | `docs/design/design-system.md` |
| Tokens (colours, type, spacing, radius, shadow, motion, motif) | `docs/design/tokens.json` |
| Reference artboards with exact values | `docs/design/mockups/*.dc.html` (see its README) |
| Screens the backend must serve | `docs/design/design-system.md` §8 + `docs/api/overview.md` |

If this skill and those files disagree, the files win — then fix this skill.

## Identity in five lines

1. **Modern heritage barbershop**: craft and trust of a classic barbershop, calm 2026 mobile language.
2. **Green for action** `#127A56`, **navy for ink and hero surfaces** `#13203F` / `#0B1530`, **lots of white**, canvas `#FAFBFC`.
3. **IBM Plex Sans Arabic** (default) + **IBM Plex Sans** (Latin) + IBM Plex Mono (OTP, booking codes).
4. **Barber-pole stripe** — green · white · navy at 45° — only on splash, loading bar, ticket edge, empty states (≤ 5 % of a screen).
5. Large soft radii, **pill buttons**, **floating pill bottom nav**, glass controls over the map, bento tiles in Business mode.

## Palette variants

- **A · green · navy · white** (default): hero/nav `#0B1530`, text `#13203F`, active nav pill green.
- **B · green · white**: hero/nav `#0E6246`, text `#151A20`, active nav pill white with green text.
Build with semantic roles (`primary`, `secondary`, `hero`, `textPrimary`…) from `tokens.json`, never raw hex in widgets, so A/B is a theme switch.

## Non-negotiables

- **Contrast**: every text pair meets WCAG AA. Known traps: `neutral-400 #8E99A6` is disabled-only;
  warning text is `#A15F00` (not lighter); light labels on green use `#E8F5EF`, not `#C6E7D7`.
- **RTL**: design Arabic first; mirror directional icons (back, chevrons, arrows) but not clocks,
  checks or logos; phone numbers, OTP, prices and times stay **LTR** inside RTL text; horizontal lists start on the right.
- **Digits**: Latin 0–9 for times, prices, phones in both locales (beware `intl` `ar_SA` defaults to ٠١٢). Tabular figures for numbers in columns.
- **Touch**: targets ≥ 48 px; primary button 56 px, full width in the thumb zone; one primary action per screen.
- **Money** shown VAT-inclusive, `60 ر.س` / `60 SAR`; API sends halalas.
- **Time**: shops open past midnight — slot groups include "بعد منتصف الليل"; show "مفتوح حتى 2:00 ص".
- **Icons**: Phosphor (`phosphor_flutter`), regular by default, fill for active nav; no emoji.
- **Copy**: use the ubiquitous language (Business المنشأة, Branch الفرع, Barber الحلاق, Appointment الحجز, "Any barber" أي حلاق).

## Components (D1) — see mockups for exact styling

Buttons (primary green pill · secondary navy · tonal green-50 · outline · destructive text) · text field
(filled, radius 12) · phone field (`+966`, LTR) · OTP (6 boxes, Plex Mono) · glass search pill · filter
& choice chips · Map/List segmented control · branch card · barber selector ("Any barber" first) ·
service row with checkbox · date strip · slot grid (pills, grouped by part of day) · booking summary
bar · status badges (pending amber, confirmed green, completed navy, cancelled neutral, no-show red) ·
floating bottom nav · bento KPI tile · per-barber timeline with green now-line · stripe loading/empty · navy snackbar.

## Screen inventory

- **Customer (D2)**: splash · language · phone login · OTP · location permission · home (map ⇄ list,
  city, search, filters) · branch profile · booking (services → barber/any → date & slot → summary → success ticket) · my bookings · booking details · profile & settings.
- **Business (D3)**: onboarding wizard (business → CR upload → first branch + map pin → services →
  hours → invite barbers) · pending/rejected review states · **Today dashboard** · calendar ·
  booking actions · staff booking / walk-in · services · team & invitations · barber schedule & time off · branch settings & policy · plan.

## Workflows

- **Figma**: load the Figma skills first (`figma-use`, then `figma-generate-library` for D1 tokens/components,
  `figma-generate-design` for screens). Create variables from `tokens.json` (modes: light-a, light-b, dark),
  text styles from the type scale, then components, then screens — Arabic frames first, English as the mirrored variant.
- **Flutter** (separate repo): tokens → `lib/core/theme/tokens.dart` → `ThemeData(useMaterial3: true)` with a
  custom `ColorScheme` + `ThemeExtension`s (stripe, glass, bento, hero); bundle the IBM Plex fonts;
  `flutter_localizations` with `app_ar.arb` as source; one `Formatters` class for money/dates/digits.
- **Mockups/artifacts**: reuse the patterns in `docs/design/mockups/`; keep sizes 390 × 844 for phones.
- **Screen → API**: when a screen needs data, check `docs/api/overview.md`; if the endpoint doesn't
  return exactly what the screen needs, propose the change to `api/openapi.yaml` first.

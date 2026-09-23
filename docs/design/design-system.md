# Design System — "Modern Barbershop" (mobile, 2026)

Arabic-first, RTL-native, built for Flutter (Material 3 under the hood, customised so it doesn't look
like stock Material). Designed in Figma in parallel with the backend; screens drive the API contract.

| File | Purpose |
|---|---|
| [`tokens.json`](tokens.json) | Machine-readable tokens (colours, themes A/B/dark, type, spacing, radius, shadow, motion, motif) → Figma variables and Flutter theme |
| [`mockups/`](mockups/) | Source of the style canvas: foundations, components and four screens with exact values |
| `.claude/skills/design-system/SKILL.md` | Condensed rules AI sessions load for any UI work |

## 1. Direction

**Modern heritage barbershop**: the trust and craft of a classic barbershop, expressed with a clean,
calm, 2026 mobile language.

- **Palette** — barber green for action, deep navy for ink and hero surfaces, lots of white.
- **Signature motif** — the *barber-pole stripe*, redrawn in green · white · navy at 45°. Used
  sparingly (≤ 5 % of a screen): splash, loading bar, booking-success ticket edge, empty states.
- **Shapes** — large soft radii, pill buttons, floating (not edge-to-edge) bottom navigation.
- **Surfaces** — white cards on a very light cool-grey canvas; navy hero headers; frosted-glass
  controls floating over the map.
- **Layout** — bento-grid tiles for the business dashboard; generous spacing; one clear primary
  action per screen, always reachable by the thumb.
- **Motion** — short, springy, meaningful: slot selection, sheet transitions, success moments;
  light haptics on select and confirm.

## 2. Colour

Two variants are defined; **A is the default**, B is compared side by side in Figma (D1).

- **A · Green · Navy · White** — green actions, navy ink/headers, white surfaces. More premium, stronger hierarchy.
- **B · Green · White** — green actions and headers, neutral ink. Lighter and fresher.

### Primitives

| Step | Green | Navy | Neutral |
|---|---|---|---|
| 0 | — | — | `#FFFFFF` |
| 25 | — | — | `#FAFBFC` |
| 50 | `#E8F5EF` | `#EEF1F8` | `#F4F6F8` |
| 100 | `#C6E7D7` | `#D5DCEC` | `#E9EDF1` |
| 200 | `#9FD6BC` | `#AEBAD8` | `#D7DDE3` |
| 300 | `#6FC19C` | `#8093BF` | `#B9C2CC` |
| 400 | `#3FA97B` | `#56699F` | `#8E99A6` |
| 500 | `#1C9063` | `#394D82` | `#6B7684` |
| **600** | **`#127A56`** ← brand green | `#28396A` | `#525C69` |
| 700 | `#0E6246` | `#1C2A52` | `#3C4550` |
| **800** | `#0A4A35` | **`#13203F`** ← brand navy (ink) | `#262D35` |
| 900 | `#063324` | `#0B1530` | `#151A20` |
| 950 | — | `#070E22` | — |

Semantic: success `#127A56` on `#E8F5EF` · warning `#A15F00` on `#FFF4E0` · error `#C62F2F` on `#FDECEC` ·
info `#394D82` on `#EEF1F8`.

### Roles (light, variant A)

| Role | Token | Use |
|---|---|---|
| `primary` / `onPrimary` | green-600 / white | primary buttons, selected slot, active nav, links |
| `primaryContainer` / `onPrimaryContainer` | green-50 / green-800 | selected chips, "open now" badge, highlights |
| `secondary` / `onSecondary` | navy-800 / white | hero headers, secondary buttons, dark cards |
| `secondaryContainer` | navy-50 | info panels |
| `background` | neutral-25 | app canvas |
| `surface` | white | cards, sheets |
| `surfaceContainer` | neutral-50 | inputs, grouped lists |
| `outline` / `outlineStrong` | neutral-200 / neutral-300 | dividers, input borders |
| `text.primary` | navy-800 | titles and body |
| `text.secondary` | neutral-600 | meta info (distance, duration) |
| `text.placeholder` | neutral-500 | placeholders, hints |
| `text.disabled` | neutral-400 | disabled only (exempt from contrast) |
| accent on navy | green-300 | small highlights on navy headers |

Variant B swaps: `secondary` → green-800, hero headers → green-700, `text.primary` → neutral-900.

### Dark theme (tokens ready; ship after light)

`background` navy-950 · `surface` navy-900 · `surfaceContainer` navy-800 · `primary` green-400 ·
`onPrimary` navy-950 · `text.primary` navy-50 · `text.secondary` navy-200 · `outline` navy-700.

### Contrast (WCAG 2.2, checked)

| Pair | Ratio | |
|---|---|---|
| white on green-600 (primary button) | 5.32 : 1 | AA ✓ |
| green-600 text on white | 5.32 : 1 | AA ✓ |
| green-600 on green-50 (selected chip) | 4.75 : 1 | AA ✓ |
| navy-800 on white (body text) | 16.08 : 1 | AAA ✓ |
| neutral-600 on white (secondary text) | 6.79 : 1 | AA ✓ |
| neutral-500 on white (placeholder) | 4.62 : 1 | AA ✓ |
| green-300 on navy-900 (accent on hero) | 8.40 : 1 | AAA ✓ |
| error `#C62F2F` on white | 5.46 : 1 | AA ✓ |
| warning `#A15F00` on white | 5.06 : 1 | AA ✓ |
| neutral-400 on white | 2.89 : 1 | disabled only ✗ for text |

## 3. Typography — IBM Plex

| Script | Family | Weights used |
|---|---|---|
| Arabic (default locale) | **IBM Plex Sans Arabic** | 400 · 500 · 600 · 700 |
| English / Latin | **IBM Plex Sans** | 400 · 500 · 600 · 700 |
| Codes (OTP, booking ref) | IBM Plex Mono | 500 |

Fonts are **bundled** in the Flutter app (offline, no layout shift), declared as one family with the
Latin face as fallback so mixed text ("قص شعر · Fade") renders correctly in either locale.

| Style (Flutter `TextTheme`) | Size / line height | Weight | Use |
|---|---|---|---|
| `displaySmall` | 32 / 44 | 700 | Onboarding, success screens |
| `headlineSmall` | 24 / 34 | 700 | Screen titles |
| `titleLarge` | 20 / 30 | 600 | Section titles, sheet titles |
| `titleMedium` | 17 / 26 | 600 | Card titles (branch name) |
| `bodyLarge` | 16 / 26 | 400 | Main body |
| `bodyMedium` | 14 / 22 | 400 | Secondary body, descriptions |
| `labelLarge` | 15 / 22 | 600 | Buttons |
| `labelMedium` | 13 / 18 | 500 | Chips, badges, tabs |
| `bodySmall` | 12 / 18 | 400 | Captions, meta |

Arabic needs taller line heights than Latin — the scale above is tuned for Arabic (≈ 1.5–1.65).
Prices and times use **tabular figures** so columns of numbers line up.

**Digits**: Latin digits (0–9) by default for times, prices and phone numbers in both locales
(most readable in Saudi apps). Watch out: `intl` formats `ar_SA` with Arabic-Indic digits (٠١٢)
unless told otherwise — decide once in D1 and enforce in a single formatting helper.

## 4. Spacing, radius, elevation, motion

- **Spacing** (4-pt grid): 2 · 4 · 8 · 12 · 16 · 20 · 24 · 32 · 40 · 56. Screen side padding **20**.
- **Radius**: 8 (small chips, badges) · 12 (inputs) · 16 (cards) · 24 (sheets, large cards) ·
  32 (hero, bento tiles) · full (buttons, slot chips, search bar, bottom nav, avatars).
- **Touch targets**: ≥ 48 × 48; primary buttons 56 high, full width in the thumb zone.
- **Elevation** (navy-tinted, soft):
  - e1 — `0 1 2 rgba(19,32,63,.06)` · cards on canvas
  - e2 — `0 8 24 rgba(19,32,63,.08)` · raised cards, map pins
  - e3 — `0 16 40 rgba(19,32,63,.16)` · floating nav, bottom sheets
- **Glass** (only over map/photos): white 72 % + 20 px background blur + hairline white border.
- **Motion**: 150 ms micro · 250 ms standard · 400 ms sheets/pages; easing `cubic-bezier(0.2, 0, 0, 1)`;
  spring for chip/slot selection. Respect "reduce motion".

## 5. Iconography and imagery

- **Phosphor** icons (`phosphor_flutter`): *regular* by default, *fill* for active nav items; 24 px.
- Directional icons (back, chevrons, arrows) **mirror** in RTL; clocks, check marks, logos don't.
- Photography: real shop interiors and cuts, warm and natural; 4:3 cards, 16:9 heroes.
- Avatars: circular; a green ring means "available today".
- Map: custom muted style (light grey land, white roads, soft-green parks), green **pill pins** showing
  starting price or rating; selected pin turns navy and lifts (e2).

## 6. RTL rules

- Design Arabic first; English is the mirrored variant (Figma auto layout direction per locale).
- Phone numbers, OTP boxes, prices and times stay LTR inside RTL text (wrap with LTR direction).
- Horizontal lists (date strip, barber carousel) start from the **right** in Arabic.
- Mixed-script labels are tested in both locales in every component.

## 7. Core components (D1)

| Component | Notes |
|---|---|
| Buttons | Primary (green pill), Secondary (navy tonal / outline), Ghost, Destructive; loading state |
| Text field | filled `surfaceContainer`, radius 12, label above; error text below |
| Phone field | fixed `+966` prefix, LTR digits, Saudi mobile format hint `5X XXX XXXX` |
| OTP input | 6 boxes, IBM Plex Mono, autofill from SMS, resend countdown |
| Search bar | pill, glass over map, filter button inside |
| Chips | filter (multi), choice (single), with counts |
| Segmented control | Map / List toggle |
| Branch card | photo, name, rating · reviews, distance, "open now" badge, price from, favourite |
| Barber selector | horizontal avatars; first item is "Any barber" (أي حلاق) with a stripe icon |
| Service row | name, duration, price, check; sticky total bar |
| Date strip | 14 days, weekday + date, disabled closed days |
| Slot grid | pill chips grouped Morning / Afternoon / Evening / Late night (shops open past midnight) |
| Booking summary sheet | branch, barber, services, time, total, cancellation rule, confirm |
| Status badge | pending (amber), confirmed (green), completed (navy), cancelled (neutral), no-show (red) |
| Bottom nav | floating pill, e3 shadow; customer: Home · Bookings · Profile — business: Today · Calendar · Services · Team · More |
| Bento KPI tile | big tabular number, label, trend; radius 32 |
| Barber timeline | per-barber columns, appointments as blocks, now-line in green |
| Empty / loading | stripe motif shimmer, friendly copy in both languages |
| Toast / snackbar | navy background, green action |

## 8. Screen inventory

**Customer (D2)** — Splash (stripe) · Language · Phone login · OTP · Location permission · Home
(map ⇄ list, city switcher, search, filters) · Branch profile (photos, services, barbers, hours, map,
directions) · Booking: services → barber (or any) → date & slot → summary → success ticket ·
My bookings (upcoming / past, cancel) · Booking details · Profile & settings (language, notifications).

**Business mode (D3)** — Onboarding wizard (business info → CR upload → first branch + map pin →
services → hours → invite barbers) · Pending review / rejected states · **Today dashboard**
(bento KPIs: bookings today, expected revenue, utilisation, next appointment; per-barber timeline) ·
Calendar (day/week per barber) · Booking actions (confirm, complete, no-show, cancel) · New staff
booking / walk-in · Services & prices · Team & invitations · Barber schedule & time off · Branch
settings & booking policy · Plan & subscription.

## 9. Flutter hand-off (D4)

- Tokens exported from Figma variables → `lib/core/theme/tokens.dart` → `ThemeData(useMaterial3: true)`
  with a custom `ColorScheme` + `ThemeExtension`s for things M3 doesn't cover (stripe, glass, bento).
- `flutter_localizations` + ARB files (`app_ar.arb` is the source locale).
- One `Formatters` class for money, dates, times and digits.
- Components built as a small internal package so both customer and business modes share them.

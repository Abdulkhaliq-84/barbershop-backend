# ADR-0033: Notifications — devices per user, pushes from booking's events, sent once per device

- Status: Accepted · Date: 2026-10-03 · Builds on [ADR-0019](0019-outbox-delivery-and-billing-trial.md), [ADR-0025](0025-appointment-lifecycle.md) and [ADR-0027](0027-discovery-read-model.md)

## Context
A customer books, then waits for the shop. Today they only learn what happened by opening the
app. Booking already publishes an event for every change (ADR-0025, ADR-0026); nobody listens.

To push a notice to a phone, the server needs four things:

- **The phones.** Each app install gets a registration token from the push service (FCM). It
  also needs the user it belongs to and the language the app is in.
- **What to say.** A short title and body in Arabic or English, with the branch's name and the
  time in the branch's own time zone.
- **A sender.** FCM in production. In development, something you can see without a phone.
- **Once only.** The outbox delivers an event at least once (ADR-0019). A retry must not push
  the same notice to the same phone twice.

## Decision
- **A `notification` module that depends on no other module**, like billing and discovery. main
  subscribes it to booking's events and business's branch events. Its root package declares
  the event structs with the same fields as the publishers', so main converts them with a plain
  conversion. It is in its own `notification` schema, with no foreign keys out (ADR-0015).
- **Booking gets a subscribe function**: `booking.OnAppointmentChanged`. It covers all seven
  appointment events (booked, confirmed, rejected, cancelled, completed, no-show, expired) as one
  struct. Each is a separate subscription (`name + ".booked"`, …), so each event type keeps its
  own job and retries. The struct carries the event's ID, which is what "once" is measured by.
- **Devices.**
  - `POST /v1/me/devices` registers the caller's app install with its `token`, `platform` and
    `locale`.
  - A token belongs to one user at a time, whoever signed in on that phone last. If another
    user registers the same token, the device becomes theirs as a new device (new ID). The
    previous user stops getting pushes on a phone they've left.
  - Registering again refreshes the platform, locale and `updated_at`. It keeps the ID.
  - A user keeps at most 10 devices, and registering an 11th drops the oldest. This stops one
    account from making an event fan out to thousands of pushes.
  - `DELETE /v1/me/devices/{device_id}` is called when the app signs out. Another user's device
    answers `404`.
- **The token is a credential.** Anyone holding it can push to that phone, so:
  - it is never sent back to clients and never logged;
  - `domain.Token` redacts itself in slog (`LogValue`) and in fmt (`Format`), even when a whole
    `Device` is printed;
  - only `Reveal()` gives the value, and it is called only by the store and, from M7.2, the
    FCM sender.
- **Branch copy.** Notification keeps its own copy of each branch's name and time zone, kept
  from business's branch events (published or not, since a branch's bookings still need its
  name). An older version than the copy's changes nothing, as in ADR-0027.
- **What a customer hears (M7.1):**

  | Event | Push |
  |---|---|
  | booked, pending | "Booking request sent" |
  | booked, confirmed (at once, or booked by the shop) | "Booking confirmed" |
  | confirmed | "Booking confirmed" |
  | rejected | "Booking declined" |
  | expired | "Booking request expired" |
  | cancelled by the shop | "Booking cancelled" |
  | cancelled by the customer, completed, no-show | nothing |

  The customer isn't told what they did themselves, or what happened after the visit. Walk-ins
  (no account) hear nothing. Notices to the shop come in M7.4. `domain.CustomerNotice` decides
  all of this in one table.
- **Messages live in code**, Arabic and English, with Latin digits as the design system says.
  - The time is shown in the branch's time zone: "Thu 1 Oct, 16:30" or "الخميس 1 أكتوبر، 16:30".
  - An English message uses the branch's English name, or its Arabic name if it has none.
  - Each device gets its own language, so one person's Arabic phone and English tablet each get
    their own wording.
- **Once per (event, device).** `notification.deliveries` has one row per event pushed to a
  device, keyed `(event_id, device_id)`. For each of the customer's devices, the handler:
  1. skips the device if this event already reached it;
  2. otherwise pushes, then records the push.

  If one device fails, the others are still tried. The handler then returns the error, and the
  outbox retries the event later; the retry reaches only the devices it missed.
- **A branch not heard of yet is an error**, not a skip. Its event normally arrives long before
  any booking. If it hasn't, the outbox retries and the notice goes out late rather than never.
- **Senders behind a port.** `app.PushSender` is the port. `PUSH_PROVIDER=console` writes each
  push (device ID, platform, locale, kind, title, body, but never the token) to the log. It is
  the only provider until FCM (M7.2), and production refuses to start with it, as with
  `SMS_PROVIDER=console`.

## Consequences
- **At least once, not exactly once.** If the process dies between the push and the record, the
  retry pushes again. That is rare and harmless (a duplicate notice), and better than a push
  never sent. M7.2 can pass FCM a collapse key per (event, device) so the phone shows one.
- **No order between pushes.** Each event type is its own job. If the shop confirms a second
  after the booking, "request sent" may arrive after "confirmed". A push says something
  happened, and the app shows the booking's current status from the API.
- **The push is sent inside the event's job.** A slow push service slows that subscription's
  jobs, not the request that booked. M7.2 adds HTTP timeouts and backoff, and removes devices
  whose tokens FCM says are dead.
- **A device's language is set when it registers.** When the app's language changes, the app
  registers again.
- **The deliveries log grows by one row per push.** It is what a future "my notifications"
  screen reads, newest first by user (indexed). A retention job can trim it later.
- **Production can't start yet.** SMS can't send there either, so nothing changes until the real
  providers land (M7.2 FCM, M7.5 SMS).

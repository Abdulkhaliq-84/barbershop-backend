# ADR-0035: Reminders — a copy of each booking, a task every minute, one queued event per reminder

- Status: Accepted · Date: 2026-10-03 · Builds on [ADR-0026](0026-walk-ins-and-pending-expiry.md), [ADR-0033](0033-notifications-devices-and-pushes.md) and [ADR-0034](0034-fcm-sender.md)

## Context
Customers forget appointments, and a no-show costs the shop the slot. A push an hour before the
start fixes most of that. The plan said "reminders as scheduled River jobs, which check the
status again when they fire". Three things make this harder than it sounds:

- **Bookings change.** A reminder queued at confirmation can outlive a cancellation. Something
  must check, when it's time, that the booking is still on.
- **Notification depends on no other module** (ADR-0033). It can't ask booking whether an
  appointment is still confirmed.
- **Booking's events arrive at least once and in any order.** "Confirmed" can land after
  "cancelled".

There were two ways to schedule:

1. **One River job per appointment**, scheduled for start − 1 h when it is confirmed. This
   needs a "publish later" in the outbox. Every confirmation delivered twice queues a second
   job. Nothing is ever cancelled, and every job must still look up the status when it fires.
2. **A task every minute** that finds the appointments due now. There are no per-appointment
   jobs to track, and catching up after downtime is automatic.

## Decision
- **Notification keeps its own copy of each customer's booking.**
  - `notification.appointments`: customer, branch, start, status, when it was confirmed, and
    when it was reminded.
  - It is kept from the same booking events that already reach notification
    (`AppointmentChanged`): the copy is saved first, then the notice is pushed.
  - Walk-ins have no customer to remind and aren't kept.
  - **The status only moves forward:** pending → confirmed → closed (rejected, cancelled,
    expired, completed, no-show). It's one upsert whose `WHERE` compares ranks. A late
    "confirmed" never revives a cancelled booking, and the first confirmation's time is kept.
- **A reminder is due** when the booking is confirmed, not reminded yet, starts within the
  next hour but hasn't started, and was confirmed at least an hour before its start.
  - Someone who books for half an hour from now just got "Booking confirmed", and gets no
    reminder.
  - `domain.ReminderLead` is the one hour.
- **A task every minute** (`Bus.Every("notification.remind")`, like booking's expiry,
  ADR-0026) claims what's due in batches of 100.
  - **One transaction per batch.** It selects the rows `FOR UPDATE SKIP LOCKED`, sets
    `reminded_at`, and queues one `notification.reminder_due` event per appointment through
    the outbox (`PublishTx`).
  - **Exactly once.** A reminder is queued exactly once, overlapping runs skip each other's
    rows, and if queueing fails nothing is marked.
  - **Soonest first,** so a backlog reminds the most urgent.
  - A partial index on `starts_at` (`WHERE status = 'confirmed' AND reminded_at IS NULL`)
    keeps the search to the rows that can be due.
- **Sending is an ordinary event** (`notification.send_reminder`, notification subscribing to
  its own event, `internal/notification/events`).
  - **Check again before sending.** The handler checks the copy again: still confirmed, same
    start. A booking cancelled between queueing and sending gets nothing.
  - **Then the usual push path** (ADR-0033, ADR-0034): each device once (the deliveries log,
    keyed by the event's ID), in its language, the branch's time zone, retried by the outbox,
    and the event ID as collapse key.
- **The message:** "Your appointment is coming up / موعدك قريب", with the branch and the time.
  The title doesn't say "in an hour", because a reminder sent late after downtime would make
  it wrong.

## Consequences
- **Not exact to the minute.** A reminder goes out within about a minute of start − 1 h, plus
  delivery time. If the worker was down, the reminders still due go out when it's back.
  Bookings that already started are skipped.
- **A cancellation seconds before the reminder may lose the race:** the copy hears of it a
  moment after booking does. That is rare and harmless.
- **Rows aren't deleted yet.** Closed and past rows stay, so a late event can't revive a
  booking. Pruning old rows is a later job.
- **The 24 h reminder is the exercise.** It's a second "reminded" column and a second due rule.
  The window and the "confirmed at least that long before" rule carry over.
- **Shop-side notices are next (M7.4).** The copy here is the customer's view.

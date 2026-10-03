# ADR-0034: Pushes through FCM — a service account signs in, bounded retries, three kinds of failure

- Status: Accepted · Date: 2026-10-03 · Builds on [ADR-0033](0033-notifications-devices-and-pushes.md)

## Context
M7.1 decided what to tell whom and logged it to the console. To reach phones, the server must
call Firebase Cloud Messaging (FCM), the one service that delivers to both Android and iOS (it
passes iOS pushes on to Apple). Calling an outside API from a background job raises questions
the console never did:

- **How to sign in.** FCM's HTTP v1 API wants an OAuth 2.0 access token for a Google service
  account. The account's JSON key is a secret: whoever holds it can push to every install of
  the app.
- **How long to wait.** Go's `http.Client` has no timeout by default, so a hung connection
  would hold a worker slot for ever.
- **What to do when it fails.** Some failures pass if you try again (Google overloaded,
  throttling, network trouble). Some never will (the app was uninstalled, the token is
  malformed). Some need an operator (wrong project, a revoked key).

## Decision
- **`PUSH_PROVIDER=fcm`, with the key in a file.**
  - `FCM_CREDENTIALS_FILE` is the absolute path of the service account's JSON key, mounted as a
    secret file. It is not an environment variable, which can leak into process listings and
    crash reports.
  - main reads the file, and the server refuses to start if it isn't a service account key with
    an RSA private key and an `https` token address.
  - Neither the key nor any part of it appears in errors or logs. `.gitignore` and
    `.dockerignore` keep likely key files out of commits and images.
- **Sign-in by hand, with the JWT library already used for access tokens.**
  - The sender signs a one-hour RS256 assertion (`iss` = the account, `aud` = its token
    address, scope `firebase.messaging`) and trades it for an access token.
  - It reuses that token until five minutes before it expires, so none expires in flight. A
    lock makes concurrent pushes share one sign-in.
  - If FCM refuses the token (401), the sender signs in again and retries once.
  - No Google SDK: about 100 lines of standard library plus `golang-jwt` cover it, and every
    step is visible and tested.
- **Every wait is bounded.** `FCM_TIMEOUT` (default 5 s) bounds each request, connecting
  included. The transport also bounds dialing, the TLS handshake and waiting for response
  headers.
- **Retries in two layers.**
  1. **Inside one push.** Throttling (429), Google's server errors (5xx), timeouts and network
     errors are tried up to 3 times in all.
     - The pause before a retry is random, up to 250 ms·2ⁿ and at most 2 s ("full jitter"), so
       many workers retrying at once spread out.
     - If Google sends `Retry-After`, the sender waits that long when it's at most 10 s.
       Anything longer is left to the outbox.
     - A cancelled job stops waiting at once.
  2. **Across jobs.** The outbox's retry: what one job couldn't send, a later run of the event
     will. Devices already reached are skipped (ADR-0033).
- **Three kinds of failure.**

  | FCM says | Meaning | What happens |
  |---|---|---|
  | `UNREGISTERED` (404), `SENDER_ID_MISMATCH` (403) | the app was uninstalled, or the token belongs to another project | `app.ErrDeviceGone`: the device is forgotten, and the event isn't retried for it |
  | `INVALID_ARGUMENT` (400) | the token or the message is invalid | `app.ErrPushRejected`: recorded with outcome `rejected` (never tried again), logged as an error; the device is kept |
  | 429, 5xx, timeouts, network | may pass | retried, then left to the outbox |
  | other 4xx (`PERMISSION_DENIED`, `THIRD_PARTY_AUTH_ERROR`, …) | our setup is wrong | an error: the outbox retries, and it works once an operator fixes the setup |

  A rejected push keeps its device on purpose. A message bug would make every push fail with
  `INVALID_ARGUMENT`, and deleting devices for it would sign everyone out of notifications.
- **Deliveries record the outcome** (migration 00028): `sent` or `rejected`. Devices that are
  gone are deleted rather than logged.
- **One notice shows once.** Each push carries a collapse key, the event's ID (`android.collapse_key`,
  `apns-collapse-id`). If a crash makes the outbox push the same event twice, the phone shows
  it once, which closes the gap ADR-0033 left.
- **Notices are shown at once:** Android priority `high`, APNs priority 10.

## Consequences
- **Worst case per device**, with Google fully down, is about 3 × 5 s plus two pauses. A user
  has at most 10 devices, so a long outage can outlast a job. The job is then cancelled and
  retried, and the devices already reached are skipped.
- **Pushes go one device at a time.** Ten devices is the most one user can have, so this is
  simple and fast enough. Sending to several at once is the "Your turn".
- **Staging needs its own Firebase project and key**, and production refuses only `console`.
- **Testing never reaches Google.** The sender takes a base address and a transport, so the
  tests run a fake Google over TLS (token address and FCM) and check:
  - the signed assertion, the token reuse and renewal;
  - every kind of failure, the pauses and the timeouts;
  - that no token or key appears in errors or logs.
- **Production still can't start.** `SMS_PROVIDER=console` is refused there until M7.5.

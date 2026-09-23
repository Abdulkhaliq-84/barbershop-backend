// Package shared is the shared kernel: the few small, stable value objects
// that several bounded contexts must agree on — typed IDs, Saudi phone
// numbers, money, bilingual text, map points and time intervals.
//
// Rules (docs/architecture/overview.md §3):
//   - Only the standard library and github.com/google/uuid may be imported:
//     never platform code, pgx, net/http or a business module (lint-enforced).
//   - A type belongs here only when two or more modules need exactly the same
//     meaning. When in doubt, keep it inside the module.
//
// Every type is a value object: its fields are unexported, it is created by a
// constructor that validates it (returning an error), and it never changes
// afterwards. So if you hold a PhoneNumber, it is a valid phone number.
package shared

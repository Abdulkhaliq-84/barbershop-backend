package domain

// Kind is what a notification is about. The app gets it with the push, to
// open the right screen.
type Kind string

// Kinds of notice to a customer about their booking.
const (
	BookingRequested Kind = "booking_requested" // booked, waiting for the shop's answer
	BookingConfirmed Kind = "booking_confirmed" // confirmed, at once or by the shop
	BookingDeclined  Kind = "booking_declined"  // the shop said no
	BookingExpired   Kind = "booking_expired"   // the shop didn't answer in time
	BookingCancelled Kind = "booking_cancelled" // the shop cancelled it
)

// CustomerNotice says what to tell a customer when something happens to
// their appointment (what: booked, confirmed, …; status: the status now),
// if anything. They aren't told about what they did themselves (cancelling)
// or what's after the visit (completed, no-show).
func CustomerNotice(what, status, cancelledBy string) (Kind, bool) {
	switch what {
	case "booked":
		if status == "confirmed" {
			return BookingConfirmed, true
		}
		return BookingRequested, status == "pending"
	case "confirmed":
		return BookingConfirmed, true
	case "rejected":
		return BookingDeclined, true
	case "expired":
		return BookingExpired, true
	case "cancelled":
		return BookingCancelled, cancelledBy == "staff"
	}
	return "", false
}

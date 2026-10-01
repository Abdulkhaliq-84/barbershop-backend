package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/booking/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// BookAppointment is a customer's booking request.
type BookAppointment struct {
	Customer       shared.UserID
	IdempotencyKey uuid.UUID // the app's key for this request: a retry sends the same one
	Branch         shared.BranchID
	Start          time.Time
	Services       []shared.ServiceID // 1 to 5, performed back to back by one barber
	Barber         *shared.StaffID    // nil: any barber
	Note           string
}

// BookingAttempt is what the repository saves in one transaction: the
// first of Drafts whose barber is still free.
type BookingAttempt struct {
	// Requester owns the Idempotency-Key: the customer booking, or the
	// staff member booking for a walk-in.
	Requester   shared.UserID
	Branch      shared.BranchID
	Key         uuid.UUID
	RequestHash []byte // SHA-256 of what was asked, to tell a retry from a reused key
	Now         time.Time
	// Allow checks the customer's upcoming bookings at the branch, counted
	// under a lock on the customer. Nil for the shop's own bookings.
	Allow func(active int) error
	// Drafts are the same booking with each free barber, best first; none
	// when nobody looked free (the key is still claimed, to find a
	// concurrent first request).
	Drafts []*domain.Appointment
	// StillWorking re-reads a draft's barber's working windows, under their
	// lock: hours or time off may have changed since the slot was shown.
	StillWorking func(ctx context.Context, a *domain.Appointment) (bool, error)
}

// Bookings stores appointments (booking's own repository).
type Bookings interface {
	// Replay returns the appointment an earlier request with the
	// requester's key made (ok), domain.ErrIdempotencyReused if it asked for
	// something else, or nothing if the key is new.
	Replay(ctx context.Context, requester shared.UserID, key uuid.UUID, requestHash []byte) (a *domain.Appointment, ok bool, err error)
	// Book saves the first draft whose barber is still free, or returns the
	// appointment an earlier request with the same key made (replayed).
	// domain.ErrSlotUnavailable when nobody was.
	Book(ctx context.Context, attempt BookingAttempt) (a *domain.Appointment, replayed bool, err error)
	// ByCustomer returns one of the customer's appointments, or
	// domain.ErrNotFound (someone else's too).
	ByCustomer(ctx context.Context, customer shared.UserID, id domain.AppointmentID) (*domain.Appointment, error)
}

// BookHandlers are the booking use cases: any signed-in user books at a
// bookable branch; its staff book walk-ins there too.
type BookHandlers struct {
	*AvailabilityHandlers // the same reads: branch, menu, barbers, windows, appointments
	bookings              Bookings
	staff                 Staff
}

// NewBookHandlers wires the use cases.
func NewBookHandlers(availability *AvailabilityHandlers, bookings Bookings, staff Staff) *BookHandlers {
	return &BookHandlers{AvailabilityHandlers: availability, bookings: bookings, staff: staff}
}

// Book books the customer in. The slot shown a moment ago may be gone:
// the rules are checked again here, and the database has the last word
// (ADR-0008). With "any barber", the free barber with the fewest booked
// minutes that day gets it — spreading the work — then the next if they
// were just taken.
//
// A retry with the same Idempotency-Key gets the first request's
// appointment whatever changed since (the time is taken now — by that very
// appointment): the key is looked up before anything else.
func (h *BookHandlers) Book(ctx context.Context, cmd BookAppointment) (a *domain.Appointment, replayed bool, err error) {
	if len(cmd.Services) == 0 || len(cmd.Services) > domain.MaxServices || hasDuplicates(cmd.Services) {
		return nil, false, domain.ErrNoServices
	}
	hash := requestHash(cmd)
	if a, ok, err := h.bookings.Replay(ctx, cmd.Customer, cmd.IdempotencyKey, hash); ok || err != nil {
		return a, ok, err
	}
	branch, err := h.branches.Bookable(ctx, cmd.Branch)
	if err != nil {
		return nil, false, err
	}
	now := h.clock.Now()
	if err := checkStart(cmd.Start, now, branch); err != nil {
		return nil, false, err
	}
	menu, err := h.menus.Menu(ctx, branch.BusinessID, branch.ID)
	if err != nil {
		return nil, false, fmt.Errorf("book: %w", err)
	}
	offers, err := offersFor(cmd.Services, menu)
	if err != nil {
		return nil, false, err
	}
	if offers, err = h.stillThere(ctx, branch, offers); err != nil {
		return nil, false, err
	}
	assignment := domain.AnyBarber
	if cmd.Barber != nil {
		assignment = domain.RequestedBarber
		i := slices.IndexFunc(offers, func(o Offer) bool { return o.ID == *cmd.Barber })
		if i < 0 {
			return nil, false, domain.ErrBarberUnavailable
		}
		offers = offers[i : i+1]
	}
	day := domain.DayOf(cmd.Start.In(branch.Location))
	cands, err := h.candidates(ctx, branch, day, offers)
	if err != nil {
		return nil, false, err
	}
	free := freeAt(cmd.Start, cands, day, branch.Location)

	drafts := make([]*domain.Appointment, 0, len(free))
	for _, staff := range free {
		d, err := domain.Book(domain.Booking{
			ID: shared.NewID[domain.AppointmentTag](), Business: branch.BusinessID, Branch: branch.ID,
			Barber: staff, Customer: cmd.Customer, Start: cmd.Start, Items: itemsFor(cmd.Services, menu, staff),
			Buffer: branch.Policy.Buffer, Assignment: assignment, Note: strings.TrimSpace(cmd.Note),
			AutoConfirm: branch.Policy.AutoConfirm, PendingExpiry: branch.Policy.PendingExpiry,
			CancellationWindow: branch.Policy.CancellationWindow,
		}, now)
		if err != nil {
			return nil, false, err
		}
		drafts = append(drafts, d)
	}
	limit := branch.Policy.MaxActiveBookings
	return h.bookings.Book(ctx, BookingAttempt{
		Requester: cmd.Customer, Branch: branch.ID,
		Key: cmd.IdempotencyKey, RequestHash: hash, Now: now, Drafts: drafts,
		Allow: func(active int) error {
			if active >= limit {
				return domain.ErrTooManyBookings
			}
			return nil
		},
		StillWorking: h.stillWorking(branch),
	})
}

// stillWorking re-reads a draft's barber's working windows: hours or time
// off may have changed since the free time was found.
func (h *BookHandlers) stillWorking(branch Branch) func(context.Context, *domain.Appointment) (bool, error) {
	return func(ctx context.Context, a *domain.Appointment) (bool, error) {
		busy := a.Busy()
		windows, err := h.schedules.WorkingWindows(ctx, branch.BusinessID, branch.ID, []shared.StaffID{a.Barber()}, busy.Start(), busy.End())
		if err != nil {
			return false, err
		}
		return slices.ContainsFunc(windows[a.Barber()], func(w shared.Interval) bool { return w.Covers(busy) }), nil
	}
}

// StaffBooking is the shop booking someone in: a walk-in, or a customer who
// phoned. They have no app account here; the shop gives their name.
type StaffBooking struct {
	Actor          shared.UserID // the staff member booking
	Business       shared.BusinessID
	Branch         shared.BranchID
	IdempotencyKey uuid.UUID // the actor's key for this request
	Start          time.Time
	Services       []shared.ServiceID
	Barber         shared.StaffID
	CustomerName   string
	Note           string
}

// WalkInGrace is how far back the shop may start a booking: a walk-in may
// already be in the chair.
const WalkInGrace = 15 * time.Minute

// StaffBook books a walk-in. Who may: the owner and the branch's managers
// for any barber there, a barber for themselves (domain.ErrForbidden
// otherwise; not staff of the business, or not its branch, is
// domain.ErrNotFound). The branch must take bookings (published, its
// business active).
//
// The shop's rules differ from the app's: any whole minute (not only the
// grid), from WalkInGrace ago (no lead time), within the horizon; the
// barber must be working then and free. It is confirmed at once, and no
// limit of upcoming bookings applies. The Idempotency-Key is the staff
// member's.
func (h *BookHandlers) StaffBook(ctx context.Context, cmd StaffBooking) (a *domain.Appointment, replayed bool, err error) {
	if len(cmd.Services) == 0 || len(cmd.Services) > domain.MaxServices || hasDuplicates(cmd.Services) {
		return nil, false, domain.ErrNoServices
	}
	member, err := h.staff.MemberOf(ctx, cmd.Actor, cmd.Business)
	if err != nil {
		return nil, false, err
	}
	if _, err := h.staff.BranchLocation(ctx, cmd.Business, cmd.Branch); err != nil {
		return nil, false, err
	}
	if !member.worksAt(cmd.Branch) || (member.Role == RoleBarber && cmd.Barber != member.Staff) {
		return nil, false, domain.ErrForbidden
	}
	hash := staffRequestHash(cmd)
	if a, ok, err := h.bookings.Replay(ctx, cmd.Actor, cmd.IdempotencyKey, hash); ok || err != nil {
		return a, ok, err
	}
	branch, err := h.branches.Bookable(ctx, cmd.Branch)
	switch {
	case errors.Is(err, domain.ErrNotFound) || (err == nil && branch.BusinessID != cmd.Business):
		return nil, false, domain.ErrBranchNotBookable // the business's own branch, not published
	case err != nil:
		return nil, false, err
	}
	now := h.clock.Now()
	if err := checkStaffStart(cmd.Start, now, branch); err != nil {
		return nil, false, err
	}
	menu, err := h.menus.Menu(ctx, branch.BusinessID, branch.ID)
	if err != nil {
		return nil, false, fmt.Errorf("staff book: %w", err)
	}
	offers, err := offersFor(cmd.Services, menu)
	if err != nil {
		return nil, false, err
	}
	if offers, err = h.stillThere(ctx, branch, offers); err != nil {
		return nil, false, err
	}
	i := slices.IndexFunc(offers, func(o Offer) bool { return o.ID == cmd.Barber })
	if i < 0 {
		return nil, false, domain.ErrBarberUnavailable
	}
	day := domain.DayOf(cmd.Start.In(branch.Location))
	cands, err := h.candidates(ctx, branch, day, offers[i:i+1])
	if err != nil {
		return nil, false, err
	}
	var drafts []*domain.Appointment // none if they aren't free: the key still finds a first request
	if len(freeAt(cmd.Start, cands, day, branch.Location)) == 1 {
		d, err := domain.Book(domain.Booking{
			ID: shared.NewID[domain.AppointmentTag](), Business: branch.BusinessID, Branch: branch.ID,
			Barber: cmd.Barber, CustomerName: cmd.CustomerName, Source: domain.SourceStaff, Start: cmd.Start,
			Items: itemsFor(cmd.Services, menu, cmd.Barber), Buffer: branch.Policy.Buffer,
			Assignment: domain.RequestedBarber, Note: strings.TrimSpace(cmd.Note), AutoConfirm: true,
		}, now)
		if err != nil {
			return nil, false, err
		}
		drafts = append(drafts, d)
	}
	return h.bookings.Book(ctx, BookingAttempt{
		Requester: cmd.Actor, Branch: branch.ID, Key: cmd.IdempotencyKey, RequestHash: hash, Now: now,
		Drafts: drafts, StillWorking: h.stillWorking(branch), // no Allow: no limit for the shop
	})
}

// checkStaffStart applies the shop's rules for when its bookings may
// start: a whole minute, from WalkInGrace ago, within the horizon.
func checkStaffStart(start, now time.Time, b Branch) error {
	last := domain.DayOf(now.In(b.Location)).AddDays(b.Policy.HorizonDays)
	switch {
	case !start.Equal(start.Truncate(time.Minute)),
		start.Before(now.Add(-WalkInGrace)),
		!start.Before(last.AddDays(1).Start(b.Location)):
		return domain.ErrInvalidStart
	}
	return nil
}

// staffRequestHash is requestHash for the shop's bookings.
func staffRequestHash(cmd StaffBooking) []byte {
	services := make([]string, 0, len(cmd.Services))
	for _, s := range cmd.Services {
		services = append(services, s.String())
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "staff|%s|%s|%s|%s|%q|%q",
		cmd.Branch, cmd.Start.UTC().Format(time.RFC3339Nano), strings.Join(services, ","), cmd.Barber,
		strings.TrimSpace(cmd.CustomerName), strings.TrimSpace(cmd.Note)))
	return sum[:]
}

// Appointment returns one of the customer's own appointments.
func (h *BookHandlers) Appointment(ctx context.Context, customer shared.UserID, id domain.AppointmentID) (*domain.Appointment, error) {
	return h.bookings.ByCustomer(ctx, customer, id)
}

// checkStart applies the branch's rules for when bookings may start: on
// its clock grid, at least the lead time away, and within the horizon.
func checkStart(start, now time.Time, b Branch) error {
	last := domain.DayOf(now.In(b.Location)).AddDays(b.Policy.HorizonDays)
	switch {
	case !domain.OnGrid(start, b.Location, b.Policy.SlotInterval),
		start.Before(now.Add(b.Policy.MinLead)),
		!start.Before(last.AddDays(1).Start(b.Location)):
		return domain.ErrInvalidStart
	}
	return nil
}

// freeAt returns who can take start, the least booked that day first (then
// by ID, so the choice is stable).
func freeAt(start time.Time, cands []domain.Candidate, day domain.Day, loc *time.Location) []shared.StaffID {
	type load struct {
		staff  shared.StaffID
		booked time.Duration
	}
	from, to := day.Start(loc), day.AddDays(1).Start(loc)
	var free []load
	for _, c := range cands {
		if !c.CanTake(start) {
			continue
		}
		l := load{staff: c.Staff}
		for _, b := range c.Busy {
			s, e := later(b.Start(), from), earlier(b.End(), to)
			if e.After(s) {
				l.booked += e.Sub(s)
			}
		}
		free = append(free, l)
	}
	slices.SortFunc(free, func(a, b load) int {
		return cmp.Or(cmp.Compare(a.booked, b.booked), cmp.Compare(a.staff.String(), b.staff.String()))
	})
	out := make([]shared.StaffID, 0, len(free))
	for _, l := range free {
		out = append(out, l.staff)
	}
	return out
}

// itemsFor snapshots the chosen services as staff performs them: their
// name, and this barber's duration and price.
func itemsFor(services []shared.ServiceID, menu []MenuItem, staff shared.StaffID) []domain.Item {
	items := make([]domain.Item, 0, len(services))
	for _, id := range services {
		m := menu[slices.IndexFunc(menu, func(m MenuItem) bool { return m.ID == id })]
		p := m.Performers[slices.IndexFunc(m.Performers, func(p Performer) bool { return p.Staff == staff })]
		items = append(items, domain.Item{Service: id, Name: m.Name, Duration: p.Duration, Price: p.Price})
	}
	return items
}

// requestHash fingerprints what was asked, so a retry with the same
// Idempotency-Key is told from the key reused for something else.
func requestHash(cmd BookAppointment) []byte {
	barber := "any"
	if cmd.Barber != nil {
		barber = cmd.Barber.String()
	}
	services := make([]string, 0, len(cmd.Services))
	for _, s := range cmd.Services {
		services = append(services, s.String())
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%s|%s|%q",
		cmd.Branch, cmd.Start.UTC().Format(time.RFC3339Nano), strings.Join(services, ","), barber, strings.TrimSpace(cmd.Note)))
	return sum[:]
}

// later and earlier pick between two instants (time.Time can't use the
// built-in max and min: it isn't an ordered type).
func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// AppointmentView is an appointment with its barber's name.
type AppointmentView struct {
	*domain.Appointment
	BarberName string // "" if they no longer work at the branch
}

// View adds the barber's name to a.
func (h *BookHandlers) View(ctx context.Context, a *domain.Appointment) (AppointmentView, error) {
	return view(ctx, h.branches, a)
}

func view(ctx context.Context, branches Branches, a *domain.Appointment) (AppointmentView, error) {
	v, err := views(ctx, branches, a.Business(), a.Branch(), []*domain.Appointment{a})
	if err != nil {
		return AppointmentView{}, err
	}
	return v[0], nil
}

// views adds their barbers' names to one branch's appointments, asking
// business once.
func views(ctx context.Context, branches Branches, business shared.BusinessID, branch shared.BranchID, list []*domain.Appointment) ([]AppointmentView, error) {
	var staff []shared.StaffID
	for _, a := range list {
		if !slices.Contains(staff, a.Barber()) {
			staff = append(staff, a.Barber())
		}
	}
	names := make(map[shared.StaffID]string, len(staff))
	if len(staff) > 0 {
		barbers, err := branches.Barbers(ctx, business, branch, staff)
		if err != nil {
			return nil, fmt.Errorf("appointment barbers: %w", err)
		}
		for _, b := range barbers {
			names[b.ID] = b.Name
		}
	}
	out := make([]AppointmentView, 0, len(list))
	for _, a := range list {
		out = append(out, AppointmentView{Appointment: a, BarberName: names[a.Barber()]})
	}
	return out, nil
}

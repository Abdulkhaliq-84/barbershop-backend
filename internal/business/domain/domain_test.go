package domain_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/business/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func TestNewCRNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
		err      error
	}{
		{"1010123456", "1010123456", nil},
		{"١٠١٠١٢٣٤٥٦", "1010123456", nil}, // Arabic-Indic, as typed on an Arabic keyboard
		{"1010 123 456", "1010123456", nil},
		{"1010-123456", "1010123456", nil},
		{"", "", domain.ErrInvalidCRNumber},
		{"101012345", "", domain.ErrInvalidCRNumber},   // 9 digits
		{"10101234567", "", domain.ErrInvalidCRNumber}, // 11 digits
		{"101012345a", "", domain.ErrInvalidCRNumber},
		{"+1010123456", "", domain.ErrInvalidCRNumber},
	}
	for _, tt := range tests {
		got, err := domain.NewCRNumber(tt.in)
		if !errors.Is(err, tt.err) || got.String() != tt.want {
			t.Errorf("NewCRNumber(%q) = %q, %v; want %q, %v", tt.in, got, err, tt.want, tt.err)
		}
	}
}

func registration(t *testing.T) domain.Registration {
	t.Helper()
	name, err := shared.NewLocalizedText("صالون الأناقة", "Elegance Barbers")
	if err != nil {
		t.Fatal(err)
	}
	cr, err := domain.NewCRNumber("1010123456")
	if err != nil {
		t.Fatal(err)
	}
	return domain.Registration{DisplayName: name, LegalName: "  مؤسسة الأناقة للحلاقة ", CRNumber: cr}
}

func TestRegisterBusiness(t *testing.T) {
	t.Parallel()
	id, staffID, owner := shared.NewID[shared.BusinessTag](), shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag]()

	b, m, err := domain.RegisterBusiness(id, staffID, owner, registration(t), t0)
	if err != nil {
		t.Fatal(err)
	}
	if b.ID() != id || b.OwnerID() != owner || b.Status() != domain.StatusDraft || b.Version() != 1 ||
		b.LegalName() != "مؤسسة الأناقة للحلاقة" || b.CRNumber().String() != "1010123456" ||
		!b.CreatedAt().Equal(t0) || !b.UpdatedAt().Equal(t0) {
		t.Errorf("business = %+v", b)
	}
	// Times are kept at the database's precision (microseconds), in UTC.
	riyadh := time.FixedZone("AST", 3*60*60)
	b2, _, _ := domain.RegisterBusiness(id, staffID, owner, registration(t), t0.Add(1234*time.Nanosecond).In(riyadh))
	if want := t0.Add(time.Microsecond); b2.CreatedAt() != want || b2.UpdatedAt() != want {
		t.Errorf("created at %v, want %v", b2.CreatedAt(), want)
	}
	// The owner comes with the business: the first staff member.
	if m.ID() != staffID || m.BusinessID() != id || m.UserID() != owner || m.Role() != domain.RoleOwner || !m.IsActive() {
		t.Errorf("owner = %+v", m)
	}
}

func TestRegisterBusinessRejects(t *testing.T) {
	t.Parallel()
	long := func(n int) string { return strings.Repeat("ب", n) } // 2 bytes each: limits count characters

	tests := []struct {
		name   string
		owner  shared.UserID
		change func(r *domain.Registration)
		want   error
	}{
		{"no owner", shared.UserID{}, nil, domain.ErrOwnerRequired},
		{"no cr number", shared.NewID[shared.UserTag](), func(r *domain.Registration) { r.CRNumber = domain.CRNumber{} }, domain.ErrInvalidCRNumber},
		{"no display name", shared.NewID[shared.UserTag](), func(r *domain.Registration) { r.DisplayName = shared.LocalizedText{} }, shared.ErrArabicRequired},
		{"blank legal name", shared.NewID[shared.UserTag](), func(r *domain.Registration) { r.LegalName = "   " }, domain.ErrLegalNameRequired},
		{"legal name too long", shared.NewID[shared.UserTag](), func(r *domain.Registration) { r.LegalName = long(201) }, domain.ErrTextTooLong},
		{"display name too long", shared.NewID[shared.UserTag](), func(r *domain.Registration) {
			r.DisplayName, _ = shared.NewLocalizedText(long(101), "")
		}, domain.ErrTextTooLong},
		{"english name too long", shared.NewID[shared.UserTag](), func(r *domain.Registration) {
			r.DisplayName, _ = shared.NewLocalizedText("اسم", strings.Repeat("x", 101))
		}, domain.ErrTextTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := registration(t)
			if tt.change != nil {
				tt.change(&r)
			}
			_, _, err := domain.RegisterBusiness(shared.NewID[shared.BusinessTag](), shared.NewID[shared.StaffTag](), tt.owner, r, t0)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}

	// The limits are inclusive.
	r := registration(t)
	r.LegalName = long(200)
	r.DisplayName, _ = shared.NewLocalizedText(long(100), strings.Repeat("x", 100))
	if _, _, err := domain.RegisterBusiness(shared.NewID[shared.BusinessTag](), shared.NewID[shared.StaffTag](), shared.NewID[shared.UserTag](), r, t0); err != nil {
		t.Fatalf("names at the limit: %v", err)
	}
}

// The whole role matrix: who may do what needs at least role "need".
func TestStaffMemberAuthorize(t *testing.T) {
	t.Parallel()
	roles := []domain.Role{domain.RoleOwner, domain.RoleManager, domain.RoleBarber}
	allowed := map[domain.Role][]domain.Role{ // member role → what it satisfies
		domain.RoleOwner:   {domain.RoleOwner, domain.RoleManager, domain.RoleBarber},
		domain.RoleManager: {domain.RoleManager, domain.RoleBarber},
		domain.RoleBarber:  {domain.RoleBarber},
	}
	for _, has := range roles {
		for _, need := range roles {
			for _, active := range []bool{true, false} {
				m := domain.RehydrateStaffMember(shared.NewID[shared.StaffTag](), shared.NewID[shared.BusinessTag](), shared.NewID[shared.UserTag](), has, active, t0)
				want := active && slices.Contains(allowed[has], need)
				err := m.Authorize(need)
				if (err == nil) != want || (err != nil && !errors.Is(err, domain.ErrForbidden)) {
					t.Errorf("%s (active=%v) needs %s: error = %v, want allowed=%v", has, active, need, err, want)
				}
			}
		}
	}
}

func TestParse(t *testing.T) {
	t.Parallel()
	for _, s := range []string{"owner", "manager", "barber"} {
		if r, err := domain.ParseRole(s); err != nil || string(r) != s {
			t.Errorf("ParseRole(%q) = %q, %v", s, r, err)
		}
	}
	if _, err := domain.ParseRole("admin"); !errors.Is(err, domain.ErrUnknownRole) {
		t.Errorf("ParseRole(admin) error = %v", err)
	}
	for _, s := range []string{"draft", "pending_review", "active", "rejected", "suspended"} {
		if st, err := domain.ParseStatus(s); err != nil || string(st) != s {
			t.Errorf("ParseStatus(%q) = %q, %v", s, st, err)
		}
	}
	if _, err := domain.ParseStatus("deleted"); !errors.Is(err, domain.ErrUnknownStatus) {
		t.Errorf("ParseStatus(deleted) error = %v", err)
	}
}

func TestBusinessRename(t *testing.T) {
	t.Parallel()
	later := t0.Add(time.Hour)
	newName, _ := shared.NewLocalizedText("صالون الفخامة", "")

	tests := []struct {
		name   string
		status domain.Status
		legal  string
		text   shared.LocalizedText
		want   error
	}{
		{"draft", domain.StatusDraft, " مؤسسة الفخامة ", newName, nil},
		{"rejected: fix and resubmit", domain.StatusRejected, "مؤسسة الفخامة", newName, nil},
		{"pending review", domain.StatusPendingReview, "مؤسسة الفخامة", newName, domain.ErrInvalidStateTransition},
		{"active", domain.StatusActive, "مؤسسة الفخامة", newName, domain.ErrInvalidStateTransition},
		{"suspended", domain.StatusSuspended, "مؤسسة الفخامة", newName, domain.ErrInvalidStateTransition},
		{"blank legal name", domain.StatusDraft, "  ", newName, domain.ErrLegalNameRequired},
		{"legal name too long", domain.StatusDraft, strings.Repeat("ب", 201), newName, domain.ErrTextTooLong},
		{"no display name", domain.StatusDraft, "مؤسسة الفخامة", shared.LocalizedText{}, shared.ErrArabicRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := registration(t)
			cr := r.CRNumber
			b := domain.RehydrateBusiness(shared.NewID[shared.BusinessTag](), shared.NewID[shared.UserTag](), r.DisplayName, "مؤسسة الأناقة", cr, tt.status, 3, t0, t0)

			err := b.Rename(tt.text, tt.legal, later)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if tt.want != nil {
				// A refused rename changes nothing, not even the version.
				if b.Version() != 3 || b.LegalName() != "مؤسسة الأناقة" || b.DisplayName() != r.DisplayName || !b.UpdatedAt().Equal(t0) {
					t.Errorf("refused rename changed the business: %+v", b)
				}
				return
			}
			if b.Version() != 4 || b.LegalName() != "مؤسسة الفخامة" || b.DisplayName() != newName || !b.UpdatedAt().Equal(later) || !b.CreatedAt().Equal(t0) {
				t.Errorf("after rename: %+v", b)
			}
		})
	}
}

func TestCanAttachDocument(t *testing.T) {
	t.Parallel()
	r := registration(t)
	for _, tt := range []struct {
		status   domain.Status
		existing int
		want     error
	}{
		{domain.StatusDraft, 0, nil},
		{domain.StatusDraft, domain.MaxVerificationDocuments - 1, nil},
		{domain.StatusDraft, domain.MaxVerificationDocuments, domain.ErrDocumentLimitReached},
		{domain.StatusRejected, 2, nil}, // fixing the business after a rejection
		{domain.StatusPendingReview, 0, domain.ErrInvalidStateTransition},
		{domain.StatusActive, 0, domain.ErrInvalidStateTransition},
		{domain.StatusSuspended, 0, domain.ErrInvalidStateTransition},
	} {
		b := domain.RehydrateBusiness(shared.NewID[shared.BusinessTag](), shared.NewID[shared.UserTag](), r.DisplayName, "مؤسسة", r.CRNumber, tt.status, 1, t0, t0)
		if err := b.CanAttachDocument(tt.existing); !errors.Is(err, tt.want) {
			t.Errorf("%s with %d documents: error = %v, want %v", tt.status, tt.existing, err, tt.want)
		}
	}
	if _, err := domain.ParseDocumentKind("passport"); !errors.Is(err, domain.ErrUnknownDocumentKind) {
		t.Errorf("unknown kind: %v", err)
	}
}

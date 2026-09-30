package domain

// Limits are what the business's plan allows (billing decides them). They
// are checked when something is added; nothing is removed when a plan
// shrinks (ADR-0019).
type Limits struct {
	MaxBranches int
	MaxStaff    int // managers and barbers, including pending invitations; the owner is free
}

// AllowBranch says whether a business with existing branches may add one.
func (l Limits) AllowBranch(existing int) error {
	if existing >= l.MaxBranches {
		return ErrBranchLimitReached
	}
	return nil
}

// AllowStaff says whether a business using seats may invite one more person.
func (l Limits) AllowStaff(seats int) error {
	if seats >= l.MaxStaff {
		return ErrStaffLimitReached
	}
	return nil
}

// Package httpapi exposes scheduling's use cases over HTTP: translate the
// typed request into a command, call the use case, translate the result or
// error back. No rules and no authorization live here.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/apigen"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/auth"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/platform/httpx"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/app"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/scheduling/domain"
	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// Handlers serves the scheduling operations.
type Handlers struct {
	calendars *app.CalendarHandlers
	schedules *app.ScheduleHandlers
	logger    *slog.Logger
}

// NewHandlers wires the HTTP adapter to the use cases.
func NewHandlers(calendars *app.CalendarHandlers, schedules *app.ScheduleHandlers, logger *slog.Logger) *Handlers {
	return &Handlers{calendars: calendars, schedules: schedules, logger: logger}
}

var (
	errNoPrincipal   = errors.New("no authenticated caller")
	errBadIfMatch    = errors.New("if-match: not a version")
	errBadTime       = errors.New("not an HH:MM time")
	errDuplicateDate = errors.New("a date is listed twice")
)

// weekdays maps the API's names to time.Weekday (Sunday = 0).
var weekdays = map[apigen.Weekday]time.Weekday{
	apigen.Sunday: time.Sunday, apigen.Monday: time.Monday, apigen.Tuesday: time.Tuesday,
	apigen.Wednesday: time.Wednesday, apigen.Thursday: time.Thursday, apigen.Friday: time.Friday,
	apigen.Saturday: time.Saturday,
}

// GetOpeningHours handles GET …/branches/{branch_id}/opening-hours.
func (h *Handlers) GetOpeningHours(ctx context.Context, req apigen.GetOpeningHoursRequestObject) (apigen.GetOpeningHoursResponseObject, error) {
	fail := func(err error) (apigen.GetOpeningHoursResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.GetOpeningHoursdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(errNoPrincipal)
	}
	cal, err := h.calendars.OpeningHours(ctx, branchRef(p.UserID, req.BusinessId, req.BranchId))
	if err != nil {
		return fail(err)
	}
	return apigen.GetOpeningHours200JSONResponse(toAPIOpeningHours(cal)), nil
}

// SetOpeningHours handles PUT …/branches/{branch_id}/opening-hours.
func (h *Handlers) SetOpeningHours(ctx context.Context, req apigen.SetOpeningHoursRequestObject) (apigen.SetOpeningHoursResponseObject, error) {
	fail := func(err error) (apigen.SetOpeningHoursResponseObject, error) {
		problem, headers := h.problem(ctx, err)
		return apigen.SetOpeningHoursdefaultApplicationProblemPlusJSONResponse{Body: problem, StatusCode: problem.Status, Headers: headers}, nil
	}
	p, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return fail(errNoPrincipal)
	}
	version, err := parseIfMatch(req.Params.IfMatch)
	if err != nil {
		return fail(err)
	}
	intervals, err := fromAPIDays(req.Body.Days)
	if err != nil {
		return fail(err)
	}
	cal, err := h.calendars.SetOpeningHours(ctx, app.SetOpeningHours{
		BranchRef: branchRef(p.UserID, req.BusinessId, req.BranchId), ExpectedVersion: version, Intervals: intervals,
	})
	if err != nil {
		return fail(err)
	}
	return apigen.SetOpeningHours200JSONResponse(toAPIOpeningHours(cal)), nil
}

func fromAPIDays(days []apigen.DayHours) ([]domain.WeeklyInterval, error) {
	var out []domain.WeeklyInterval
	for _, d := range days {
		day, ok := weekdays[d.Weekday]
		if !ok {
			return nil, domain.ErrInvalidWeekday
		}
		for _, r := range d.Intervals {
			opens, err := parseClock(r.Opens)
			if err != nil {
				return nil, err
			}
			closes, err := parseClock(r.Closes)
			if err != nil {
				return nil, err
			}
			out = append(out, domain.IntervalFromClock(day, opens, closes))
		}
	}
	return out, nil
}

func toAPIOpeningHours(cal *domain.BranchCalendar) apigen.OpeningHours {
	out := apigen.OpeningHours{Days: toAPIWeek(cal.OpeningHours()), Version: cal.Version()}
	if !cal.UpdatedAt().IsZero() {
		at := cal.UpdatedAt()
		out.UpdatedAt = &at
	}
	return out
}

// toAPIWeek returns all seven days, Sunday first; a day without intervals
// is closed (or a day off).
func toAPIWeek(w domain.WeeklyHours) []apigen.DayHours {
	byDay := map[time.Weekday][]apigen.TimeRange{}
	for _, i := range w.Intervals() {
		opens, closes := i.Clock()
		byDay[i.Day] = append(byDay[i.Day], apigen.TimeRange{Opens: formatClock(opens), Closes: formatClock(closes)})
	}
	days := make([]apigen.DayHours, 0, 7)
	for name, day := range orderedWeekdays() {
		ranges := byDay[day]
		if ranges == nil {
			ranges = []apigen.TimeRange{}
		}
		days = append(days, apigen.DayHours{Weekday: name, Intervals: ranges})
	}
	return days
}

// orderedWeekdays yields the week, Sunday first.
func orderedWeekdays() func(yield func(apigen.Weekday, time.Weekday) bool) {
	return func(yield func(apigen.Weekday, time.Weekday) bool) {
		for day := time.Sunday; day <= time.Saturday; day++ {
			if !yield(apigen.Weekday(strings.ToLower(day.String())), day) {
				return
			}
		}
	}
}

// parseClock reads "HH:MM" (or "24:00") as minutes after midnight. The API
// schema has already checked the shape.
func parseClock(s string) (int, error) {
	hh, mm, ok := strings.Cut(s, ":")
	h, err1 := strconv.Atoi(hh)
	m, err2 := strconv.Atoi(mm)
	if !ok || err1 != nil || err2 != nil || h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
		return 0, errBadTime
	}
	return h*60 + m, nil
}

func formatClock(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

// problem maps a use-case error to an API error. Unknown errors are bugs or
// outages: logged, and answered with a generic 500.
func (h *Handlers) problem(ctx context.Context, err error) (apigen.Problem, apigen.ProblemResponseHeaders) {
	status, code, detail := http.StatusInternalServerError, "internal", ""
	var headers apigen.ProblemResponseHeaders
	switch {
	case errors.Is(err, errNoPrincipal):
		status, code, detail = http.StatusUnauthorized, "unauthorized", "a valid access token is required"
		challenge := httpx.BearerChallenge
		headers.WWWAuthenticate = &challenge
	case errors.Is(err, errBadIfMatch):
		status, code, detail = http.StatusBadRequest, "validation_failed", "If-Match: send the version you last read (0 the first time)"
	case errors.Is(err, errBadTime):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "times are HH:MM"
	case errors.Is(err, domain.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, domain.ErrForbidden):
		status, code = http.StatusForbidden, "forbidden"
	case errors.Is(err, domain.ErrVersionConflict):
		status, code, detail = http.StatusPreconditionFailed, "version_conflict", "it changed since you read it; reload and try again"
	case errors.Is(err, domain.ErrTimeOffOverlaps):
		status, code, detail = http.StatusConflict, "time_off_overlaps", "it overlaps other time off; change or remove that first"
	case errors.Is(err, errDuplicateDate):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", "overrides: each date at most once"
	case errors.Is(err, domain.ErrInvalidWeekday), errors.Is(err, domain.ErrInvalidInterval),
		errors.Is(err, domain.ErrTooManyIntervals), errors.Is(err, domain.ErrOverlappingIntervals),
		errors.Is(err, domain.ErrInvalidDate), errors.Is(err, domain.ErrTooManyOverrides),
		errors.Is(err, domain.ErrScheduleClash), errors.Is(err, domain.ErrInvalidTimeOff), errors.Is(err, domain.ErrReasonTooLong):
		status, code, detail = http.StatusUnprocessableEntity, "validation_failed", strings.TrimPrefix(err.Error(), "scheduling: ")
	default:
		h.logger.ErrorContext(ctx, "scheduling request failed", slog.String("error_type", fmt.Sprintf("%T", err)))
	}
	return httpx.APIProblem(ctx, status, code, detail), headers
}

// parseIfMatch reads a version from If-Match, bare (3) or as an ETag ("3").
// 0 is allowed: the version of hours never set.
func parseIfMatch(v string) (int, error) {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, errBadIfMatch
	}
	return n, nil
}

func branchRef(actor shared.UserID, business, branch apigen.BusinessID) app.BranchRef {
	return app.BranchRef{
		Actor:      actor,
		BusinessID: shared.IDFromUUID[shared.BusinessTag](business),
		BranchID:   shared.IDFromUUID[shared.BranchTag](branch),
	}
}

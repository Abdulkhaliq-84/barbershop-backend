package domain

import (
	"fmt"
	"time"

	"github.com/Abdulkhaliq-84/barbershop-backend/internal/shared"
)

// message is a notice's title and body in one language. The body's %[1]s is
// the branch's name and %[2]s when the appointment is.
type message struct{ title, body string }

var messages = map[Kind]map[shared.Language]message{
	BookingRequested: {
		shared.Arabic:  {"تم إرسال طلب الحجز", "%[1]s، %[2]s. سنخبرك حين يرد الصالون."},
		shared.English: {"Booking request sent", "%[1]s, %[2]s. We'll let you know when the shop answers."},
	},
	BookingConfirmed: {
		shared.Arabic:  {"تم تأكيد حجزك", "%[1]s، %[2]s. نراك قريبًا."},
		shared.English: {"Booking confirmed", "%[1]s, %[2]s. See you there."},
	},
	BookingDeclined: {
		shared.Arabic:  {"لم يُقبل طلب الحجز", "تعذّر على %[1]s قبول حجزك %[2]s. جرّب وقتًا آخر."},
		shared.English: {"Booking declined", "%[1]s can't take your booking for %[2]s. Try another time."},
	},
	BookingExpired: {
		shared.Arabic:  {"انتهت مهلة طلب الحجز", "لم يرد %[1]s على طلبك لموعد %[2]s في الوقت المحدد. جرّب وقتًا آخر."},
		shared.English: {"Booking request expired", "%[1]s didn't answer your request for %[2]s in time. Try another time."},
	},
	BookingCancelled: {
		shared.Arabic:  {"أُلغي موعدك", "ألغى %[1]s موعدك %[2]s."},
		shared.English: {"Booking cancelled", "%[1]s cancelled your booking for %[2]s."},
	},
	BookingReminder: {
		shared.Arabic:  {"موعدك قريب", "%[1]s، %[2]s. نراك قريبًا."},
		shared.English: {"Your appointment is coming up", "%[1]s, %[2]s. See you soon."},
	},
}

// Message words a notice in lang: about branch, for an appointment starting
// at start, which it shows in the branch's time zone. Times use Latin digits
// in both languages (docs/design/design-system.md).
func Message(k Kind, lang shared.Language, branch Branch, start time.Time) (title, body string, err error) {
	m, ok := messages[k][lang]
	if !ok {
		return "", "", fmt.Errorf("notification: no %s message in %q", k, lang)
	}
	loc, err := time.LoadLocation(branch.Timezone)
	if err != nil {
		return "", "", fmt.Errorf("notification: branch %s time zone: %w", branch.ID, err)
	}
	name := branch.Name.Ar()
	if lang == shared.English && branch.Name.En() != "" {
		name = branch.Name.En()
	}
	return m.title, fmt.Sprintf(m.body, name, when(lang, start.In(loc))), nil
}

var (
	arabicWeekdays = [7]string{"الأحد", "الاثنين", "الثلاثاء", "الأربعاء", "الخميس", "الجمعة", "السبت"}
	arabicMonths   = [12]string{"يناير", "فبراير", "مارس", "أبريل", "مايو", "يونيو", "يوليو", "أغسطس", "سبتمبر", "أكتوبر", "نوفمبر", "ديسمبر"}
)

// when shows a local time: "Thu 2 Oct, 16:00", "الخميس 2 أكتوبر، 16:00".
func when(lang shared.Language, t time.Time) string {
	if lang == shared.Arabic {
		return fmt.Sprintf("%s %d %s، %s", arabicWeekdays[t.Weekday()], t.Day(), arabicMonths[t.Month()-1], t.Format("15:04"))
	}
	return t.Format("Mon 2 Jan, 15:04")
}

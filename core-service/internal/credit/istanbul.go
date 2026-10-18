package credit

import "time"

// istanbul is the calendar the admin panel and campaign end dates use.
// Turkey has been on a fixed UTC+3 since 2016; the fixed zone is the
// fallback when the container has no tz database.
var istanbul = func() *time.Location {
	if loc, err := time.LoadLocation("Europe/Istanbul"); err == nil {
		return loc
	}
	return time.FixedZone("Europe/Istanbul", 3*60*60)
}()

// istanbulDay is the calendar day of t in Europe/Istanbul, as that date at
// 00:00 UTC (the form campaign end dates are kept in).
func istanbulDay(t time.Time) time.Time {
	y, m, d := t.In(istanbul).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ParseDay reads a calendar date "YYYY-MM-DD" as that date at 00:00 UTC.
func ParseDay(s string) (time.Time, error) {
	return time.Parse(time.DateOnly, s)
}

// dayStart is the instant the calendar day d (its year, month and day) begins
// in Europe/Istanbul.
func dayStart(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, istanbul)
}

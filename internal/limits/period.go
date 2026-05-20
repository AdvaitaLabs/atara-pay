package limits

import (
	"fmt"
	"time"
)

// Period bucket helpers.
//
// Atara's period cap accounting needs two strings per (now, timezone) pair:
//
//   PeriodKey  — the lookup string used in Redis and usage_counters.period_key.
//   NextReset  — the wall-clock instant the current bucket rolls over. Used
//                as Redis EXPIREAT so keys auto-expire without a sweeper.
//
// All math runs in the policy's configured timezone (defaulting to UTC).
// ISO-8601 week numbering is used for weekly buckets.

// period is the discriminator used by callers.
type period string

const (
	periodDaily   period = "daily"
	periodWeekly  period = "weekly"
	periodMonthly period = "monthly"
)

// periodKey formats the bucket string. now is converted to tz first.
//
//	daily:2026-05-21
//	weekly:2026-W21
//	monthly:2026-05
func periodKey(p period, now time.Time, tz *time.Location) string {
	if tz == nil {
		tz = time.UTC
	}
	local := now.In(tz)
	switch p {
	case periodDaily:
		return fmt.Sprintf("daily:%s", local.Format("2006-01-02"))
	case periodWeekly:
		y, w := local.ISOWeek()
		return fmt.Sprintf("weekly:%d-W%02d", y, w)
	case periodMonthly:
		return fmt.Sprintf("monthly:%s", local.Format("2006-01"))
	default:
		return string(p) + ":invalid"
	}
}

// nextReset returns the start of the NEXT bucket in tz. For Redis EXPIREAT
// this is the instant the key should disappear (counter resets to 0 for the
// new bucket).
func nextReset(p period, now time.Time, tz *time.Location) time.Time {
	if tz == nil {
		tz = time.UTC
	}
	local := now.In(tz)

	switch p {
	case periodDaily:
		// tomorrow 00:00 local
		t := time.Date(local.Year(), local.Month(), local.Day(),
			0, 0, 0, 0, tz).AddDate(0, 0, 1)
		return t
	case periodWeekly:
		// Next ISO Monday 00:00 local. ISOWeek treats Monday as the start
		// of the week; we step from "now" forward to the next Monday at
		// midnight. If today already is Monday, that means next Monday.
		days := daysUntilMonday(local.Weekday())
		anchor := time.Date(local.Year(), local.Month(), local.Day(),
			0, 0, 0, 0, tz)
		return anchor.AddDate(0, 0, days)
	case periodMonthly:
		// 1st of next month 00:00 local.
		t := time.Date(local.Year(), local.Month()+1, 1, 0, 0, 0, 0, tz)
		return t
	default:
		return now.Add(time.Hour)
	}
}

// daysUntilMonday returns the number of days until the next Monday given
// today's weekday. Monday → 7, Tue → 6, ..., Sun → 1.
func daysUntilMonday(w time.Weekday) int {
	if w == time.Monday {
		return 7
	}
	// time.Sunday=0, time.Monday=1, ..., time.Saturday=6
	// distance(today→Monday) = (1 - w + 7) % 7
	d := int(time.Monday-w+7) % 7
	if d == 0 {
		d = 7
	}
	return d
}

// loadTimezone resolves a policy's timezone string with sensible fallback
// to UTC on any parse error. Empty input → UTC.
func loadTimezone(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

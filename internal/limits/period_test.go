package limits

import (
	"testing"
	"time"
)

func TestPeriodKey(t *testing.T) {
	utc := time.UTC
	// Thursday 2026-05-21 14:30 UTC, ISO week 21.
	now := time.Date(2026, 5, 21, 14, 30, 0, 0, utc)

	cases := []struct {
		p    period
		want string
	}{
		{periodDaily, "daily:2026-05-21"},
		{periodWeekly, "weekly:2026-W21"},
		{periodMonthly, "monthly:2026-05"},
	}
	for _, c := range cases {
		got := periodKey(c.p, now, utc)
		if got != c.want {
			t.Errorf("periodKey(%s) = %q, want %q", c.p, got, c.want)
		}
	}
}

func TestNextResetDaily(t *testing.T) {
	utc := time.UTC
	now := time.Date(2026, 5, 21, 14, 30, 0, 0, utc)
	got := nextReset(periodDaily, now, utc)
	want := time.Date(2026, 5, 22, 0, 0, 0, 0, utc)
	if !got.Equal(want) {
		t.Errorf("nextReset(daily) = %v, want %v", got, want)
	}
}

func TestNextResetWeekly(t *testing.T) {
	utc := time.UTC
	// Thursday → next Monday is 4 days later (2026-05-25).
	now := time.Date(2026, 5, 21, 14, 30, 0, 0, utc)
	got := nextReset(periodWeekly, now, utc)
	want := time.Date(2026, 5, 25, 0, 0, 0, 0, utc)
	if !got.Equal(want) {
		t.Errorf("nextReset(weekly) Thursday = %v, want %v", got, want)
	}

	// On Monday itself, the next Monday should be 7 days out (not today).
	mon := time.Date(2026, 5, 18, 9, 0, 0, 0, utc)
	gotMon := nextReset(periodWeekly, mon, utc)
	wantMon := time.Date(2026, 5, 25, 0, 0, 0, 0, utc)
	if !gotMon.Equal(wantMon) {
		t.Errorf("nextReset(weekly) on Monday = %v, want %v", gotMon, wantMon)
	}

	// On Sunday, the next Monday is the next day (1 day later).
	sun := time.Date(2026, 5, 24, 22, 0, 0, 0, utc)
	gotSun := nextReset(periodWeekly, sun, utc)
	wantSun := time.Date(2026, 5, 25, 0, 0, 0, 0, utc)
	if !gotSun.Equal(wantSun) {
		t.Errorf("nextReset(weekly) on Sunday = %v, want %v", gotSun, wantSun)
	}
}

func TestNextResetMonthly(t *testing.T) {
	utc := time.UTC

	// Middle of May → 2026-06-01.
	now := time.Date(2026, 5, 21, 14, 30, 0, 0, utc)
	if got := nextReset(periodMonthly, now, utc); !got.Equal(
		time.Date(2026, 6, 1, 0, 0, 0, 0, utc),
	) {
		t.Errorf("nextReset(monthly) mid-May = %v", got)
	}

	// End of December rolls to next year's January 1.
	dec := time.Date(2026, 12, 31, 23, 59, 0, 0, utc)
	if got := nextReset(periodMonthly, dec, utc); !got.Equal(
		time.Date(2027, 1, 1, 0, 0, 0, 0, utc),
	) {
		t.Errorf("nextReset(monthly) end-of-year = %v", got)
	}
}

func TestScaleToMicros(t *testing.T) {
	cases := []struct {
		in  string
		out int64
		bad bool
	}{
		{"0", 0, false},
		{"1", 1_000_000, false},
		{"1.25", 1_250_000, false},
		{"0.000001", 1, false},
		{"100.5", 100_500_000, false},
		{"0.0000001", 0, true},  // 7 decimals — too precise
		{"-1", 0, true},
		{"abc", 0, true},
		{"", 0, true},
	}
	for _, c := range cases {
		got, err := scaleToMicros(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("scaleToMicros(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("scaleToMicros(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.out {
			t.Errorf("scaleToMicros(%q) = %d, want %d", c.in, got, c.out)
		}
	}
}

func TestMicrosToDecimal(t *testing.T) {
	cases := []struct {
		in  int64
		out string
	}{
		{0, "0"},
		{1, "0.000001"},
		{1_000_000, "1"},
		{1_250_000, "1.25"},
		{100_500_000, "100.5"},
	}
	for _, c := range cases {
		got := microsToDecimal(c.in)
		if got != c.out {
			t.Errorf("microsToDecimal(%d) = %q, want %q", c.in, got, c.out)
		}
	}
}

package schedule

import (
	"errors"
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) Spec {
	t.Helper()
	s, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

// series returns n consecutive occurrences after start, as UTC RFC 3339.
func series(t *testing.T, s Spec, loc *time.Location, start string, n int) []string {
	t.Helper()
	var out []string
	at := utc(start)
	for i := 0; i < n; i++ {
		next, ok := s.Next(at, loc)
		if !ok {
			t.Fatalf("no occurrence after %s", at)
		}
		if !next.After(at) {
			t.Fatalf("Next(%s)=%s is not strictly later", at, next)
		}
		out = append(out, next.UTC().Format(time.RFC3339))
		at = next
	}
	return out
}

func equal(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * 32 * *", "* * * 13 *", "* * * * 8",
		"*/0 * * * *", "5-1 * * * *", "a * * * *", "@reboot", "-1 * * * *", "1,,2 * * * *", "1/ * * * *", "*/61 * * * *",
		"? * * * *", "L * * * *", "0 0 L * *", "0 0 1W * *", "0 0 * * 1#2", "+1 * * * *", "100 * * * *",
		// never fires: 30 February, 31 April
		"0 0 30 2 *", "0 0 31 4 *", "0 0 31 2,4,6,9,11 *",
	} {
		if _, err := Parse(expr); !errors.Is(err, ErrInvalid) {
			t.Errorf("Parse(%q) = %v, want ErrInvalid", expr, err)
		}
	}
	long := make([]byte, MaxExpressionLength+1)
	for i := range long {
		long[i] = ' '
	}
	if _, err := Parse(string(long)); !errors.Is(err, ErrInvalid) {
		t.Fatal("overlong expression accepted")
	}
}

func TestParseForms(t *testing.T) {
	loc := mustLoc(t, "UTC")
	cases := []struct {
		expr, from string
		want       []string
	}{
		{"*/20 * * * *", "2026-01-01T00:00:00Z", []string{"2026-01-01T00:20:00Z", "2026-01-01T00:40:00Z", "2026-01-01T01:00:00Z"}},
		{"5-7,30 9 * * *", "2026-01-01T00:00:00Z", []string{"2026-01-01T09:05:00Z", "2026-01-01T09:06:00Z", "2026-01-01T09:07:00Z", "2026-01-01T09:30:00Z", "2026-01-02T09:05:00Z"}},
		{"10/25 0 * * *", "2026-01-01T00:00:00Z", []string{"2026-01-01T00:10:00Z", "2026-01-01T00:35:00Z", "2026-01-02T00:10:00Z"}},
		{"0 0 * JAN mon-FRI", "2026-01-30T12:00:00Z", []string{"2027-01-01T00:00:00Z", "2027-01-04T00:00:00Z"}}, // 2026-01-31 is Saturday
		{"0 0 * * 7", "2026-01-01T00:00:00Z", []string{"2026-01-04T00:00:00Z"}},                                 // 7 = Sunday
		{"@daily", "2026-01-01T00:00:00Z", []string{"2026-01-02T00:00:00Z"}},
		{"@HOURLY", "2026-01-01T00:30:00Z", []string{"2026-01-01T01:00:00Z"}},
		{"0 0 29 2 *", "2026-03-01T00:00:00Z", []string{"2028-02-29T00:00:00Z", "2032-02-29T00:00:00Z"}},
		{"00 07 * * *", "2026-01-01T00:00:00Z", []string{"2026-01-01T07:00:00Z"}},
	}
	for _, c := range cases {
		equal(t, series(t, mustParse(t, c.expr), loc, c.from, len(c.want)), c.want...)
	}
}

// Classic cron day rule: two restricted day fields match on either; a day
// field starting with "*" (even "*/2") leaves the other one in charge.
func TestDayOfMonthAndWeekdaySemantics(t *testing.T) {
	loc := mustLoc(t, "UTC")
	// 13th of the month OR Friday. 2026-02: Fridays 6,13,20,27; 2026-03-13 is a Friday too.
	equal(t, series(t, mustParse(t, "0 0 13 * 5"), loc, "2026-02-01T00:00:00Z", 5),
		"2026-02-06T00:00:00Z", "2026-02-13T00:00:00Z", "2026-02-20T00:00:00Z", "2026-02-27T00:00:00Z", "2026-03-06T00:00:00Z")
	// "*/2" day-of-month is a star field: Fridays on odd days only (AND).
	equal(t, series(t, mustParse(t, "0 0 */2 * 5"), loc, "2026-02-01T00:00:00Z", 3),
		"2026-02-13T00:00:00Z", "2026-02-27T00:00:00Z", "2026-03-13T00:00:00Z")
	// Restricted weekday with "*" day-of-month: weekday only.
	equal(t, series(t, mustParse(t, "0 0 * * 1"), loc, "2026-02-01T00:00:00Z", 2), "2026-02-02T00:00:00Z", "2026-02-09T00:00:00Z")
}

// Asia/Seoul has no DST: 09:00 local is 00:00 UTC every day.
func TestSeoul(t *testing.T) {
	equal(t, series(t, mustParse(t, "0 9 * * *"), mustLoc(t, "Asia/Seoul"), "2026-03-07T12:00:00Z", 3),
		"2026-03-08T00:00:00Z", "2026-03-09T00:00:00Z", "2026-03-10T00:00:00Z")
}

// America/New_York 2026: spring forward 2026-03-08 02:00 EST -> 03:00 EDT
// (07:00Z); fall back 2026-11-01 02:00 EDT -> 01:00 EST (06:00Z).
func TestNewYorkSpringForward(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// Wall-clock expression inside the gap: once, at the transition instant.
	equal(t, series(t, mustParse(t, "30 2 * * *"), ny, "2026-03-07T00:00:00Z", 3),
		"2026-03-07T07:30:00Z", "2026-03-08T07:00:00Z", "2026-03-09T06:30:00Z")
	// Two skipped times collapse into one occurrence.
	equal(t, series(t, mustParse(t, "0,30 2 * * *"), ny, "2026-03-08T00:00:00Z", 3),
		"2026-03-08T07:00:00Z", "2026-03-09T06:00:00Z", "2026-03-09T06:30:00Z")
	// Elapsed-time expression: the skipped hour does not occur; spacing stays one hour.
	equal(t, series(t, mustParse(t, "0 * * * *"), ny, "2026-03-08T05:30:00Z", 4),
		"2026-03-08T06:00:00Z", "2026-03-08T07:00:00Z", "2026-03-08T08:00:00Z", "2026-03-08T09:00:00Z")
	equal(t, series(t, mustParse(t, "*/30 * * * *"), ny, "2026-03-08T06:15:00Z", 3),
		"2026-03-08T06:30:00Z", "2026-03-08T07:00:00Z", "2026-03-08T07:30:00Z")
}

func TestNewYorkFallBack(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	// Wall-clock expression in the repeated hour: only the first (EDT) instant.
	equal(t, series(t, mustParse(t, "30 1 * * *"), ny, "2026-10-31T00:00:00Z", 3),
		"2026-10-31T05:30:00Z", "2026-11-01T05:30:00Z", "2026-11-02T06:30:00Z")
	// Elapsed-time expression: both instants of 01:00 occur, hourly spacing kept.
	equal(t, series(t, mustParse(t, "0 * * * *"), ny, "2026-11-01T03:30:00Z", 5),
		"2026-11-01T04:00:00Z", "2026-11-01T05:00:00Z", "2026-11-01T06:00:00Z", "2026-11-01T07:00:00Z", "2026-11-01T08:00:00Z")
	// The earlier-wall/later-instant order inside the repeated hour.
	equal(t, series(t, mustParse(t, "15,45 * * * *"), ny, "2026-11-01T05:20:00Z", 4),
		"2026-11-01T05:45:00Z", "2026-11-01T06:15:00Z", "2026-11-01T06:45:00Z", "2026-11-01T07:15:00Z")
	// A daily time outside the transition is unaffected.
	equal(t, series(t, mustParse(t, "0 12 * * *"), ny, "2026-10-31T00:00:00Z", 3),
		"2026-10-31T16:00:00Z", "2026-11-01T17:00:00Z", "2026-11-02T17:00:00Z")
}

func TestLatest(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	every := mustParse(t, "* * * * *")
	got, ok := every.Latest(utc("2026-05-01T10:00:00Z"), utc("2026-05-01T12:34:30Z"), time.UTC)
	if !ok || !got.Equal(utc("2026-05-01T12:34:00Z")) {
		t.Fatalf("every-minute latest %s %v", got, ok)
	}
	// A controller stopped for a year with an every-minute schedule: bounded work.
	start := time.Now()
	got, ok = every.Latest(utc("2025-05-01T10:00:00Z"), utc("2026-05-01T12:34:30Z"), ny)
	if !ok || !got.Equal(utc("2026-05-01T12:34:00Z")) || time.Since(start) > 2*time.Second {
		t.Fatalf("long downtime latest %s %v in %s", got, ok, time.Since(start))
	}
	daily := mustParse(t, "30 2 * * *")
	got, ok = daily.Latest(utc("2026-01-10T07:30:00Z"), utc("2026-03-08T12:00:00Z"), ny)
	if !ok || !got.Equal(utc("2026-03-08T07:00:00Z")) {
		t.Fatalf("daily latest across the gap %s %v", got, ok)
	}
	// floor itself when it is the only due occurrence.
	got, ok = daily.Latest(utc("2026-03-09T06:30:00Z"), utc("2026-03-09T06:30:00Z"), ny)
	if !ok || !got.Equal(utc("2026-03-09T06:30:00Z")) {
		t.Fatalf("floor latest %s %v", got, ok)
	}
	if _, ok = daily.Latest(utc("2026-03-10T06:30:00Z"), utc("2026-03-09T06:30:00Z"), ny); ok {
		t.Fatal("future floor reported due")
	}
	// Leap-day schedule: the latest occurrence years back is still found.
	leap := mustParse(t, "0 0 29 2 *")
	got, ok = leap.Latest(utc("2024-02-29T00:00:00Z"), utc("2031-06-01T00:00:00Z"), time.UTC)
	if !ok || !got.Equal(utc("2028-02-29T00:00:00Z")) {
		t.Fatalf("leap latest %s %v", got, ok)
	}
}

func TestLoadLocation(t *testing.T) {
	for _, name := range []string{"Asia/Seoul", "UTC", "America/New_York", "Europe/Berlin"} {
		if _, err := LoadLocation(name); err != nil {
			t.Errorf("LoadLocation(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "Local", "Not/AZone", "../etc/passwd", "Asia/Seoul ", `Asia\Seoul`, string(make([]byte, 65))} {
		if _, err := LoadLocation(name); !errors.Is(err, ErrInvalid) {
			t.Errorf("LoadLocation(%q) = %v, want ErrInvalid", name, err)
		}
	}
}

func TestPolicies(t *testing.T) {
	for _, p := range []string{"coalesce_latest", "skip", "queue"} {
		if !ValidOverlap(p) {
			t.Error(p)
		}
	}
	for _, p := range []string{"latest_once", "skip"} {
		if !ValidMisfire(p) {
			t.Error(p)
		}
	}
	if ValidOverlap("") || ValidOverlap("catch_up") || ValidMisfire("catch_up") || ValidMisfire("") {
		t.Fatal("unknown policy accepted")
	}
}

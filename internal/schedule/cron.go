// Package schedule parses 5-field cron expressions and computes their
// occurrences in an IANA time zone (W22, docs/09 §8, REQ-023). It is pure: it
// never reads the process clock or the host's local zone; callers pass "now"
// (the database clock). The time zone database is embedded (time/tzdata) so
// hosts without a system zone database (Windows) still load IANA names;
// time.LoadLocation prefers a system database when one exists.
//
// Daylight saving time policy (docs/38):
//
//   - An expression whose hour field covers every hour (0-23) runs in elapsed
//     time: a local time skipped by a spring-forward gap does not occur, and a
//     local time repeated by a fall-back overlap occurs twice (once per UTC
//     instant). "0 * * * *" therefore keeps a one-hour spacing across both
//     transitions.
//   - Any other expression runs by wall clock, at most once per matching local
//     time: a time inside a gap runs once at the first instant after the gap
//     (the transition), and a repeated time runs only at its first (earlier)
//     instant.
//
// Several local times can resolve to one UTC instant (for example 02:00 and
// 02:30 inside the same gap); they are one occurrence.
package schedule

import (
	"errors"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // IANA names load on hosts without a zone database (Windows)
)

// ErrInvalid is returned for an expression, time zone or policy that is not
// accepted. Callers map it to domain.ErrInvalid.
var ErrInvalid = errors.New("schedule: invalid")

// Overlap policies: what a due occurrence does while an earlier run of the
// same schedule has not ended.
const (
	OverlapCoalesceLatest = "coalesce_latest"
	OverlapSkip           = "skip"
	OverlapQueue          = "queue"
)

// Misfire policies: what an occurrence found late (the controller was not
// evaluating schedules at its time) does. Earlier occurrences that were missed
// in the same interval are never run (no catch-up); see Latest.
const (
	MisfireLatestOnce = "latest_once"
	MisfireSkip       = "skip"
)

// ValidOverlap and ValidMisfire report whether p is a known policy.
func ValidOverlap(p string) bool {
	return p == OverlapCoalesceLatest || p == OverlapSkip || p == OverlapQueue
}
func ValidMisfire(p string) bool { return p == MisfireLatestOnce || p == MisfireSkip }

// MaxExpressionLength bounds the stored cron text.
const MaxExpressionLength = 200

// Spec is a parsed cron expression. Bit i of a field is set when value i
// matches.
type Spec struct {
	minutes uint64 // 0-59
	hours   uint32 // 0-23
	dom     uint32 // 1-31
	months  uint16 // 1-12
	dow     uint8  // 0-6, Sunday = 0
	// domStar/dowStar record whether a day field starts with "*" (classic cron
	// rule, see dayMatches).
	domStar, dowStar bool
}

const allHours = 1<<24 - 1

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dayNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// Parse parses "minute hour day-of-month month day-of-week" (lists, ranges,
// steps, month/day names, 7 = Sunday) or one of the @yearly, @annually,
// @monthly, @weekly, @daily, @midnight, @hourly macros. Seconds, years, "?",
// "L", "W", "#" and @reboot are not supported.
func Parse(expr string) (Spec, error) {
	if len(expr) == 0 || len(expr) > MaxExpressionLength {
		return Spec{}, ErrInvalid
	}
	text := strings.TrimSpace(expr)
	if m, ok := macros[strings.ToLower(text)]; ok {
		text = m
	}
	fields := strings.Fields(text)
	if len(fields) != 5 {
		return Spec{}, ErrInvalid
	}
	var s Spec
	var err error
	var v uint64
	if v, err = field(fields[0], 0, 59, nil); err != nil {
		return Spec{}, err
	}
	s.minutes = v
	if v, err = field(fields[1], 0, 23, nil); err != nil {
		return Spec{}, err
	}
	s.hours = uint32(v)
	if v, err = field(fields[2], 1, 31, nil); err != nil {
		return Spec{}, err
	}
	s.dom = uint32(v)
	if v, err = field(fields[3], 1, 12, monthNames); err != nil {
		return Spec{}, err
	}
	s.months = uint16(v)
	if v, err = field(fields[4], 0, 7, dayNames); err != nil {
		return Spec{}, err
	}
	if v&(1<<7) != 0 { // 7 is Sunday
		v = v&^(1<<7) | 1
	}
	s.dow = uint8(v)
	s.domStar = strings.HasPrefix(fields[2], "*")
	s.dowStar = strings.HasPrefix(fields[4], "*")
	if !s.anyDayPossible() {
		return Spec{}, ErrInvalid
	}
	return s, nil
}

func value(text string, lo, hi int, names map[string]int) (int, bool) {
	if n, ok := names[strings.ToLower(text)]; ok {
		return n, true
	}
	if text == "" || len(text) > 2 || strings.Trim(text, "0123456789") != "" {
		return 0, false
	}
	n, _ := strconv.Atoi(text)
	return n, n >= lo && n <= hi
}

// field parses one comma-separated cron field into a bit set.
func field(text string, lo, hi int, names map[string]int) (uint64, error) {
	var out uint64
	for _, item := range strings.Split(text, ",") {
		rng, stepText, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			n, ok := value(stepText, 1, hi-lo+1, nil)
			if !ok {
				return 0, ErrInvalid
			}
			step = n
		}
		var a, b int
		switch {
		case rng == "*":
			a, b = lo, hi
		case strings.Contains(rng, "-"):
			first, last, _ := strings.Cut(rng, "-")
			var ok1, ok2 bool
			a, ok1 = value(first, lo, hi, names)
			b, ok2 = value(last, lo, hi, names)
			if !ok1 || !ok2 || a > b {
				return 0, ErrInvalid
			}
		default:
			var ok bool
			if a, ok = value(rng, lo, hi, names); !ok {
				return 0, ErrInvalid
			}
			b = a
			if hasStep { // "a/n" means a through the field maximum
				b = hi
			}
		}
		for i := a; i <= b; i += step {
			out |= 1 << uint(i)
		}
	}
	if out == 0 {
		return 0, ErrInvalid
	}
	return out, nil
}

// daysIn reports how many days month m has at most (February: 29).
var maxDays = [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// anyDayPossible rejects expressions such as "0 0 30 2 *" that can never match
// a calendar date. With two restricted day fields (OR) some day always
// matches. Otherwise both bit sets must hold on one date; every date of a
// month falls on each weekday in some year, so a valid (month, day) pair is
// enough.
func (s Spec) anyDayPossible() bool {
	if !s.dowStar && !s.domStar {
		return true
	}
	for m := 1; m <= 12; m++ {
		if s.months&(1<<uint(m)) == 0 {
			continue
		}
		if s.dom&(uint32(1)<<uint(maxDays[m]+1)-2) != 0 {
			return true
		}
	}
	return false
}

// dayMatches applies the classic cron day rule: when either day field starts
// with "*" both bit sets must match (a plain "*" matches every day), and when
// both are restricted a day matches if either does.
func (s Spec) dayMatches(y int, m time.Month, d int) bool {
	if s.months&(1<<uint(m)) == 0 {
		return false
	}
	domOK := s.dom&(1<<uint(d)) != 0
	dowOK := s.dow&(1<<uint(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday())) != 0
	if s.domStar || s.dowStar {
		return domOK && dowOK
	}
	return domOK || dowOK
}

// elapsed reports whether the expression runs in elapsed time across DST
// transitions (hour field covers every hour).
func (s Spec) elapsed() bool { return s.hours == allHours }

// LoadLocation loads an IANA time zone name from the embedded database. The
// empty name, "Local" and names longer than 64 bytes are rejected: the host
// zone must never decide when a schedule runs.
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || len(name) > 64 || name == "Local" || strings.ContainsAny(name, " \\") {
		return nil, ErrInvalid
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, ErrInvalid
	}
	return loc, nil
}

// searchDays bounds the forward search: every valid expression matches some
// date within 8 years (29 February), so 9 years always suffice.
const searchDays = 9*366 + 1

// Next returns the first occurrence strictly after after, in loc. ok is false
// only if none exists within searchDays (not possible for an expression Parse
// accepted).
func (s Spec) Next(after time.Time, loc *time.Location) (time.Time, bool) {
	local := after.In(loc)
	y, m, d := local.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC) // calendar arithmetic only
	for i := 0; i < searchDays; i++ {
		cy, cm, cd := day.Date()
		if s.dayMatches(cy, cm, cd) {
			// On a day without a zone transition, instants increase with the
			// wall clock, so the scan can start at after's own minute. A
			// transition day is scanned whole: a repeated hour can put an
			// earlier wall time after a later one.
			full := transitionNear(cy, cm, cd, loc)
			startH, startM := 0, 0
			if i == 0 && !full {
				startH, startM = local.Hour(), local.Minute()
			}
			if t, ok := s.firstOnDay(cy, cm, cd, startH, startM, full, after, loc); ok {
				return t, true
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return time.Time{}, false
}

// firstOnDay returns the earliest occurrence instant on the local date that is
// after after, scanning wall times from startH:startM. Without full the first
// match is the earliest (instants follow the wall clock on that day).
func (s Spec) firstOnDay(y int, m time.Month, d, startH, startM int, full bool, after time.Time, loc *time.Location) (time.Time, bool) {
	var best time.Time
	found := false
	for h := startH; h < 24; h++ {
		if s.hours&(1<<uint(h)) == 0 {
			continue
		}
		mins := s.minutes
		if h == startH {
			mins &^= 1<<uint(startM) - 1
		}
		for mins != 0 {
			mi := bits.TrailingZeros64(mins)
			mins &^= 1 << uint(mi)
			for _, t := range s.instants(y, m, d, h, mi, loc) {
				if t.After(after) && (!found || t.Before(best)) {
					best, found = t, true
				}
			}
			if found && !full {
				return best, true
			}
		}
	}
	return best, found
}

// instants resolves one local wall time to its occurrence instants under the
// DST policy of the package comment.
func (s Spec) instants(y int, m time.Month, d, h, mi int, loc *time.Location) []time.Time {
	wall := time.Date(y, m, d, h, mi, 0, 0, time.UTC)
	valid := resolve(wall, loc)
	switch {
	case len(valid) == 1:
		return valid
	case len(valid) > 1: // repeated local time
		if s.elapsed() {
			return valid
		}
		return valid[:1]
	}
	if s.elapsed() { // skipped local time: elapsed-time expressions skip it
		return nil
	}
	return []time.Time{gapEnd(wall, loc)}
}

// zoneOffsets lists the UTC offsets in effect within ±14h of wall (every
// instant that can display wall lies in that window).
func zoneOffsets(wall time.Time, loc *time.Location) []int {
	var out []int
	p := wall.Add(-14 * time.Hour).In(loc)
	limit := wall.Add(14 * time.Hour)
	for i := 0; i < 6; i++ {
		_, off := p.Zone()
		seen := false
		for _, o := range out {
			seen = seen || o == off
		}
		if !seen {
			out = append(out, off)
		}
		_, end := p.ZoneBounds()
		if end.IsZero() || end.After(limit) {
			break
		}
		p = end.In(loc)
	}
	return out
}

// resolve returns every instant whose local time in loc is exactly wall
// (given as a UTC-labelled wall clock), ascending.
func resolve(wall time.Time, loc *time.Location) []time.Time {
	var out []time.Time
	for _, off := range zoneOffsets(wall, loc) {
		u := wall.Add(-time.Duration(off) * time.Second).In(loc)
		if _, got := u.Zone(); got != off {
			continue
		}
		dup := false
		for _, t := range out {
			dup = dup || t.Equal(u)
		}
		if !dup {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// gapEnd is the first instant after the transition that skipped wall: the
// start of the zone in effect at the latest candidate instant.
func gapEnd(wall time.Time, loc *time.Location) time.Time {
	var hi time.Time
	for _, off := range zoneOffsets(wall, loc) {
		if u := wall.Add(-time.Duration(off) * time.Second); hi.IsZero() || u.After(hi) {
			hi = u
		}
	}
	start, _ := hi.In(loc).ZoneBounds()
	return start.In(loc)
}

// transitionNear reports whether a zone transition lies within the local date
// or 3 hours around it (conservative: a false positive only scans a day whole).
func transitionNear(y int, m time.Month, d int, loc *time.Location) bool {
	from := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-17 * time.Hour)
	_, end := from.In(loc).ZoneBounds()
	return !end.IsZero() && end.Before(from.Add(24*time.Hour+34*time.Hour))
}

// windows are the look-back spans Latest tries, smallest first.
var windows = []time.Duration{time.Hour, 26 * time.Hour, 32 * 24 * time.Hour, 367 * 24 * time.Hour, searchDays * 24 * time.Hour}

// Latest returns the latest occurrence t with floor <= t <= now, where floor
// is itself an occurrence (the first one not yet evaluated). It never
// enumerates the whole interval: it searches the smallest look-back window
// that contains an occurrence, so a controller stopped for months costs a
// bounded amount of work. ok is false when floor is after now.
func (s Spec) Latest(floor, now time.Time, loc *time.Location) (time.Time, bool) {
	if floor.After(now) {
		return time.Time{}, false
	}
	for _, w := range windows {
		start := now.Add(-w)
		if !start.After(floor) {
			start = floor.Add(-time.Nanosecond)
		}
		t, ok := s.Next(start, loc)
		if !ok || t.After(now) {
			continue
		}
		for {
			n, ok := s.Next(t, loc)
			if !ok || n.After(now) {
				return t, true
			}
			t = n
		}
	}
	return floor, true
}

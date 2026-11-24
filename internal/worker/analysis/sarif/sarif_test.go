package sarif

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var rawOpt = Options{URIForm: URIRawPath, BaseIDs: []string{"%SRCROOT%"}}

// The captured Opengrep 2.0.0 log: a taint result with its code flow, a
// suppressed result, rule metadata with CWE tags and raw (not
// percent-encoded) URIs.
func TestDecodeOpengrepLog(t *testing.T) {
	data := readTestdata(t, "opengrep-2.0.0.sarif")
	log, err := Decode(data, rawOpt)
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Runs) != 1 {
		t.Fatalf("runs %d", len(log.Runs))
	}
	run := log.Runs[0]
	if run.Driver.Name != "Opengrep" || run.Driver.SemanticVersion != "2.0.0" || !run.ExecutionSuccessful || run.Invocations != 1 {
		t.Fatalf("driver %+v", run)
	}
	rule := run.Rules[slices.IndexFunc(run.Rules, func(r Rule) bool { return r.ID == "codeye.python.command-injection" })]
	if !slices.Equal(rule.CWE, []string{"CWE-78"}) || rule.DefaultLevel != "error" {
		t.Fatalf("rule %+v", rule)
	}
	var flow, suppressed *Result
	for i := range run.Results {
		r := &run.Results[i]
		if string(data[r.ByteStart]) != "{" || string(data[r.ByteEnd-1]) != "}" {
			t.Fatalf("result %d range %d..%d", i, r.ByteStart, r.ByteEnd)
		}
		if r.Suppressed {
			suppressed = r
		} else if len(r.Steps) > 0 && flow == nil {
			flow = r
		}
	}
	if flow == nil || suppressed == nil {
		t.Fatalf("results %+v", run.Results)
	}
	if flow.Primary == nil || flow.Primary.Path == "" || flow.Primary.Outside || flow.Primary.StartLine < 1 || flow.Fingerprints["matchBasedId/v1"] == "" {
		t.Fatalf("primary %+v fingerprints %v", flow.Primary, flow.Fingerprints)
	}
	last := int64(-1)
	for i, s := range flow.Steps {
		if s.Kind != StepFlow || s.Position != i+1 || s.Of != len(flow.Steps) || s.Location == nil || s.ByteStart <= last || s.ByteStart < flow.ByteStart || s.ByteEnd > flow.ByteEnd {
			t.Fatalf("step %d %+v", i, s)
		}
		last = s.ByteStart
	}
	if !strings.HasPrefix(flow.Steps[0].Message, "Source:") || !strings.HasPrefix(flow.Steps[len(flow.Steps)-1].Message, "Sink:") {
		t.Fatalf("flow ends %q .. %q", flow.Steps[0].Message, flow.Steps[len(flow.Steps)-1].Message)
	}
}

// A hand-written log covering what the captured Opengrep log does not use:
// percent-encoded URIs, relatedLocations, partialFingerprints, several
// locations, an accepted and a rejected suppression and ruleIndex.
func TestDecodeSpecFeatures(t *testing.T) {
	log, err := Decode(readTestdata(t, "spec-features.sarif"), Options{URIForm: URIEncoded, BaseIDs: []string{"SRCROOT"}})
	if err != nil {
		t.Fatal(err)
	}
	rs := log.Runs[0].Results
	if len(rs) != 3 {
		t.Fatalf("results %d", len(rs))
	}
	a := rs[0]
	if a.RuleID != "R1" || a.Level != "error" || a.Primary.Path != "src/a b.c" || a.Fingerprints["partial:primaryLocationLineHash"] != "abc:1" {
		t.Fatalf("first %+v", a)
	}
	kinds := []string{}
	for _, s := range a.Steps {
		kinds = append(kinds, s.Kind)
	}
	if !slices.Equal(kinds, []string{StepLocation, StepRelated, StepFlow, StepFlow}) {
		t.Fatalf("steps %v", kinds)
	}
	if !a.Suppressed || rs[1].Suppressed || !slices.Equal(rs[1].SuppressionKinds, []string{"external"}) {
		t.Fatalf("suppressions %+v %+v", a, rs[1])
	}
	if rs[1].RuleID != "R2" || rs[1].Level != "note" {
		t.Fatalf("ruleIndex/default level %+v", rs[1])
	}
	if !rs[2].Primary.Outside || rs[2].Primary.Path != "" || !rs[2].Steps[0].Location.Outside {
		t.Fatalf("outside paths %+v %+v", rs[2].Primary, rs[2].Steps)
	}
	if got := log.Runs[0].Rules[0].CWE; !slices.Equal(got, []string{"CWE-22", "CWE-23"}) {
		t.Fatalf("cwe %v", got)
	}
}

func TestMapURI(t *testing.T) {
	enc := Options{URIForm: URIEncoded, BaseIDs: []string{"%SRCROOT%"}}
	raw := Options{URIForm: URIRawPath, BaseIDs: []string{"%SRCROOT%"}}
	cases := []struct {
		uri, base string
		opt       Options
		want      string
		outside   bool
	}{
		{"src/a.py", "", enc, "src/a.py", false},
		{"./src/a.py", "%SRCROOT%", enc, "src/a.py", false},
		{"src/a%20b.py", "", enc, "src/a b.py", false},
		{"src/a%20b.py", "", raw, "src/a%20b.py", false},
		{"sp ace/한글 %20.py", "", raw, "sp ace/한글 %20.py", false},
		{"../outside.py", "", raw, "", true},
		{"src/../../outside.py", "", enc, "", true},
		{"/etc/passwd", "", raw, "", true},
		{"file:///etc/passwd", "", enc, "", true},
		{"file:///etc/passwd", "", raw, "", true},
		{"C:/x.py", "", raw, "", true},
		{`src\a.py`, "", raw, "", true},
		{"a.py", "OTHER", enc, "", true},
		{"https://example.com/a.py", "", enc, "", true},
		{"..", "", raw, "", true},
	}
	for _, c := range cases {
		got, outside := mapURI(c.uri, c.base, c.opt)
		if got != c.want || outside != c.outside {
			t.Errorf("mapURI(%q, %q) = %q %v, want %q %v", c.uri, c.base, got, outside, c.want, c.outside)
		}
	}
}

// Malformed, unknown, oversized and out-of-range logs are shape errors,
// never an empty result list.
func TestDecodeRejects(t *testing.T) {
	good := string(readTestdata(t, "spec-features.sarif"))
	cases := map[string]string{
		"not json":           "{",
		"trailing data":      good + "{}",
		"array":              "[]",
		"wrong version":      strings.Replace(good, `"2.1.0"`, `"2.0.0"`, 1),
		"no version":         `{"runs":[{"tool":{"driver":{"name":"x"}},"results":[]}]}`,
		"no runs":            `{"version":"2.1.0","runs":[]}`,
		"no results member":  `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}}}]}`,
		"unknown field":      strings.Replace(good, `"ruleId": "R1"`, `"ruleId": "R1", "baselineState": "new"`, 1),
		"unknown top field":  strings.Replace(good, `"version"`, `"inlineExternalProperties": [], "version"`, 1),
		"duplicate key":      strings.Replace(good, `"ruleId": "R1"`, `"ruleId": "R1", "ruleId": "R9"`, 1),
		"bad level":          strings.Replace(good, `"level": "error"`, `"level": "fatal"`, 1),
		"zero start line":    strings.Replace(good, `"startLine": 3`, `"startLine": 0`, 1),
		"end before start":   strings.Replace(good, `"endLine": 3`, `"endLine": 1`, 1),
		"no message":         strings.Replace(good, `"message": {"text": "first"}`, `"message": {}`, 1),
		"control character":  strings.Replace(good, `"text": "first"`, `"text": "fi\u0007rst"`, 1),
		"bad suppression":    strings.Replace(good, `"kind": "inSource"`, `"kind": "elsewhere"`, 1),
		"result kind pass":   strings.Replace(good, `"ruleId": "R1"`, `"ruleId": "R1", "kind": "pass"`, 1),
		"ruleIndex mismatch": strings.Replace(good, `"ruleId": "R1"`, `"ruleId": "R1", "ruleIndex": 1`, 1),
		"empty thread flow":  `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[{"ruleId":"a","message":{"text":"m"},"codeFlows":[{"threadFlows":[{"locations":[]}]}]}]}]}`,
		"unknown schema":     strings.Replace(good, `"version"`, `"$schema": "https://example.com/other.json", "version"`, 1),
		"not utf-8":          "{\"version\":\"2.1.0\xff\"}",
	}
	for name, body := range cases {
		if _, err := Decode([]byte(body), Options{URIForm: URIEncoded}); !errors.Is(err, ErrShape) {
			t.Errorf("%s: err %v, want shape error", name, err)
		}
	}
	if _, err := Decode([]byte(good), Options{URIForm: URIEncoded, MaxBytes: int64(len(good) - 1)}); !errors.Is(err, ErrShape) {
		t.Errorf("oversized log accepted: %v", err)
	}
	if _, err := Decode([]byte(good), Options{URIForm: URIEncoded, MaxResults: 2}); !errors.Is(err, ErrShape) {
		t.Errorf("too many results accepted: %v", err)
	}
	deep := `{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"x"}},"results":[{"ruleId":"a","message":{"text":"m"},"properties":` + strings.Repeat(`{"a":`, 80) + `1` + strings.Repeat("}", 80) + `}]}]}`
	if _, err := Decode([]byte(deep), Options{}); !errors.Is(err, ErrShape) {
		t.Errorf("deep nesting accepted: %v", err)
	}
}

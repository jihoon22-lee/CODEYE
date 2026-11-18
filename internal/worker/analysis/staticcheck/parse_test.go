package staticcheck

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The captured 2026.2.2 stdout parses completely; byte ranges cut exactly
// one JSON line each.
func TestParseCapturedStreams(t *testing.T) {
	for name, want := range map[string]int{"findings.stdout": 7, "typeerr.stdout": 8, "dependency.stdout": 1, "overlap.stdout": 1, "clean.stdout": 0, "no-packages.stdout": 0, "toolchain.stdout": 0} {
		data := readTestdata(t, name)
		st := parseStream(data)
		if st.DecodeError != "" || st.ShapeErrors != 0 || st.UnparsedBytes != 0 || len(st.Records) != want || st.Values != want {
			t.Fatalf("%s: %+v", name, st)
		}
		lines := bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n"))
		for i, r := range st.Records {
			if r.Index != i || !bytes.Equal(data[r.ByteStart:r.ByteEnd], lines[i]) {
				t.Fatalf("%s record %d range %d..%d", name, i, r.ByteStart, r.ByteEnd)
			}
		}
	}
	st := parseStream(readTestdata(t, "findings.stdout"))
	var sa4023 *record
	for i := range st.Records {
		if st.Records[i].Code == "SA4023" {
			sa4023 = &st.Records[i]
		}
	}
	if sa4023 == nil || len(sa4023.Related) != 1 || sa4023.Related[0].Start != (pos{File: "[REDACTED]/find/find.go", Line: 27, Col: 18}) || sa4023.Start.Line != 28 {
		t.Fatalf("SA4023 %+v", sa4023)
	}
	u := st.Records[5]
	if u.Code != "U1000" || u.End != (pos{}) {
		t.Fatalf("U1000 has no end position in 2026.2: %+v", u)
	}
	te := parseStream(readTestdata(t, "typeerr.stdout")).Records[0]
	if te.Code != "compile" || te.Start.File != "" || compileUnit(te.Message) != "example.test/sc/typeerr" {
		t.Fatalf("compile record %+v", te)
	}
	if m := compilePos.FindStringSubmatch(te.Message); m == nil || m[1] != "typeerr/t.go" || m[2] != "3" || m[3] != "23" {
		t.Fatalf("compile position %v", m)
	}
	dep := parseStream(readTestdata(t, "dependency.stdout")).Records[0]
	if dep.Code != "compile" || dep.Start != (pos{File: "dep.go", Line: 3, Col: 8}) || compileUnit(dep.Message) != "" {
		t.Fatalf("dependency record %+v", dep)
	}
}

// Damaged stdout never becomes an empty, clean stream.
func TestParseMalformed(t *testing.T) {
	good := readTestdata(t, "findings.stdout")
	rec := `{"code":"SA1","severity":"error","location":{"file":"a.go","line":1,"column":1},"end":{"file":"a.go","line":1,"column":2},"message":"m"}`
	cases := []struct {
		name           string
		in             string
		records, shape int
		decode         string
		unparsed       bool
	}{
		{"truncated-mid-record", string(good[:len(good)-40]), 6, 0, "unexpected_eof", true},
		{"syntax", rec + "\n{\"code\" 1}\n", 1, 0, "syntax_error", true},
		{"garbage-prefix", "panic: x\n" + rec, 0, 0, "syntax_error", true},
		{"unknown-field", strings.Replace(rec, `"message":"m"`, `"message":"m","fix":{}`, 1), 0, 1, "", false},
		{"missing-code", strings.Replace(rec, `"code":"SA1",`, ``, 1), 0, 1, "", false},
		{"empty-code", strings.Replace(rec, `"code":"SA1"`, `"code":""`, 1), 0, 1, "", false},
		{"missing-location", strings.Replace(rec, `"location":{"file":"a.go","line":1,"column":1},`, ``, 1), 0, 1, "", false},
		{"negative-line", strings.Replace(rec, `"line":1,"column":1}`, `"line":-1,"column":1}`, 1), 0, 1, "", false},
		{"wrong-type", strings.Replace(rec, `"line":1,`, `"line":"1",`, 1), 0, 1, "", false},
		{"array-value", "[]\n" + rec, 1, 1, "", false},
		{"related-without-message", strings.Replace(rec, `"message":"m"`, `"message":"m","related":[{"location":{"file":"a.go","line":1,"column":1},"end":{"file":"a.go","line":1,"column":1}}]`, 1), 0, 1, "", false},
		{"blank", "\n \n\t\n", 0, 0, "", false},
	}
	for _, c := range cases {
		st := parseStream([]byte(c.in))
		if len(st.Records) != c.records || st.ShapeErrors != c.shape || st.DecodeError != c.decode || (st.UnparsedBytes > 0) != c.unparsed {
			t.Fatalf("%s: %+v", c.name, st)
		}
	}
}

func TestParseStderr(t *testing.T) {
	lines, dropped := parseStderr(readTestdata(t, "no-packages.stderr"))
	if dropped != 0 || len(lines) != 1 || lines[0].Kind != "no_packages" {
		t.Fatalf("no packages %+v", lines)
	}
	lines, _ = parseStderr(readTestdata(t, "toolchain.stderr"))
	if len(lines) != 1 || lines[0].Kind != "fatal" || !strings.Contains(lines[0].Message, "requires go >=") {
		t.Fatalf("toolchain %+v", lines)
	}
	lines, _ = parseStderr([]byte("note\r\n\nwarning: something else\n"))
	if len(lines) != 2 || lines[0].Kind != "other" || lines[0].Message != "note" || lines[1].Kind != "other" {
		t.Fatalf("other %+v", lines)
	}
	lines, dropped = parseStderr([]byte(strings.Repeat("x\n", maxStderrLines+5)))
	if len(lines) != maxStderrLines || dropped != 5 {
		t.Fatalf("cap %d %d", len(lines), dropped)
	}
}

func TestParseVersionContract(t *testing.T) {
	for in, want := range map[string]string{
		"staticcheck 2026.2.2 (0.8.2)\n":      "2026.2.2 (0.8.2)",
		"staticcheck 2026.2 (0.8.0)":          "2026.2 (0.8.0)",
		"staticcheck 2025.1.1 (0.6.1)\n":      "2025.1.1 (0.6.1)",
		"staticcheck 2026.2.2 (0.8.2)\nextra": "2026.2.2 (0.8.2)",
	} {
		if v, ok := ParseVersion([]byte(in)); !ok || v != want {
			t.Fatalf("%q: %q %v", in, v, ok)
		}
	}
	for _, in := range []string{"", "staticcheck (devel, v0.0.0-00010101000000-000000000000)", "staticcheck 2026.2.2", "golangci-lint 2026.2.2 (0.8.2)", "staticcheck 2026.2.2 (0.8.2) $(rm)"} {
		if v, ok := ParseVersion([]byte(in)); ok {
			t.Fatalf("%q accepted as %q", in, v)
		}
	}
	for v, want := range map[string]bool{"2026.2.2 (0.8.2)": true, "2026.2 (0.8.0)": true, "2026.20 (0.9.0)": false, "2026.1.1 (0.7.1)": false, "2027.1 (0.9.0)": false} {
		if VersionTested(v) != want {
			t.Fatalf("tested %q != %v", v, want)
		}
	}
}

func TestCategoryPolicyAndArgs(t *testing.T) {
	for code, want := range map[string]string{"SA1000": "correctness", "SA1019": "maintainability", "U1000": "maintainability", "S1000": "maintainability", "ST1003": "maintainability",
		"SA2001": "concurrency", "SA3000": "test_gap", "SA4006": "correctness", "SA5000": "correctness", "SA6000": "performance", "SA9003": "correctness"} {
		if got := category(code); got != want {
			t.Fatalf("%s: %s, want %s", code, got, want)
		}
	}
	args := CheckArgs([]string{"./..."})
	if !slices.Equal(args, []string{"-f", "json", "-checks", Checks, "-tests=true", "./..."}) {
		t.Fatalf("args %v", args)
	}
	for code := range VetOverlap {
		if !strings.Contains(Checks, "-"+code) {
			t.Fatalf("overlap check %s is not excluded from %s", code, Checks)
		}
	}
}

// Plan: one task per Go module in go vet's order, extra depth (not
// required), static_analysis; no Go module plans nothing.
func TestPlanModules(t *testing.T) {
	fx := newFixture(t, "", map[string]string{"go.mod": fixGoMod, "a.go": "package sc\n", "sub/go.mod": "module example.test/sub\n\ngo 1.22\n", "sub/b.go": "package sub\n", "sub/b_test.go": "package sub\n"}, domain.Scope{})
	tasks, err := Plan(context.Background(), fx.Job)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("plan %v %+v", err, tasks)
	}
	if tasks[0].Spec.Key != "staticcheck" || tasks[0].Module != "." || tasks[1].Spec.Key != "staticcheck.m01" || tasks[1].Module != "sub" || *tasks[1].FilesInScope != 2 {
		t.Fatalf("modules %+v %+v", tasks[0], tasks[1])
	}
	for _, task := range tasks {
		if task.Spec.Required || task.Spec.Stage != domain.StageStaticAnalysis || !pack.Owns(Name, task.Spec.Key) {
			t.Fatalf("task %+v", task.Spec)
		}
	}
	none := newFixture(t, "", map[string]string{"README.md": "# x\n", "main.py": "print(1)\n"}, domain.Scope{})
	if tasks, err := Plan(context.Background(), none.Job); err != nil || len(tasks) != 0 {
		t.Fatalf("no go: %v %+v", err, tasks)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Plan(ctx, fx.Job); err == nil {
		t.Fatal("cancelled plan succeeded")
	}
	if d := Descriptor(); d.ID != AnalyzerID || d.Required || d.Ecosystem != "go" || d.Tool != "staticcheck" || !slices.Equal(d.VersionArgs, []string{"-version"}) {
		t.Fatalf("descriptor %+v", d)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Config{Executable: "/x"}); err == nil {
		t.Fatal("no go accepted")
	}
	a, _ := replayAnalyzer(t)
	for _, exe := range []string{"staticcheck", filepath.Join(t.TempDir(), "missing"), t.TempDir()} {
		if _, err := New(Config{Executable: exe, Go: a.cfg.Go}); err == nil {
			t.Fatalf("%q accepted", exe)
		}
	}
}

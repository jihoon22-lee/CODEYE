package govet

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Captured from go1.27.4 linux/amd64 `go vet -json ./...` on the probe
// fixture, with the workspace root redacted by the executor. Package
// output order varies between runs; clean units print "{}".
const capturedVetStdout = `{}
{
	"example.test/probe/bad_test": {
		"printf": [
			{
				"posn": "[REDACTED]/bad/x_test.go:5:40",
				"end": "[REDACTED]/bad/x_test.go:5:42",
				"message": "fmt.Printf format %s has arg 2 of wrong type int"
			}
		]
	}
}
{
	"example.test/probe/bad": {
		"copylocks": [
			{
				"posn": "[REDACTED]/bad/bad.go:10:13",
				"end": "[REDACTED]/bad/bad.go:10:14",
				"message": "Copy passes lock by value: example.test/probe/bad.T contains sync.Mutex"
			}
		],
		"printf": [
			{
				"posn": "[REDACTED]/bad/bad.go:12:24",
				"end": "[REDACTED]/bad/bad.go:12:26",
				"message": "fmt.Printf format %d has arg \"x\" of wrong type string",
				"suggested_fixes": [{"message": "m", "edits": []}]
			}
		]
	}
}
`

const capturedTypeErrStderr = "# example.test/probe/typeerr\nvet: typeerr/t.go:3:23: cannot use \"s\" (untyped string constant) as int value in return statement\n"

func TestParseVetStreamCaptured(t *testing.T) {
	vs := parseVetStream([]byte(capturedVetStdout))
	if vs.Values != 3 || vs.EmptyValues != 1 || vs.UnparsedBytes != 0 || vs.ShapeErrors != 0 || vs.DecodeError != "" {
		t.Fatalf("stream = %+v", vs)
	}
	if len(vs.Diagnostics) != 3 {
		t.Fatalf("diagnostics = %d", len(vs.Diagnostics))
	}
	d := vs.Diagnostics[0]
	if d.PackageKey != "example.test/probe/bad_test" || d.JSONPointer != "/example.test~1probe~1bad_test/printf/0" || d.StreamIndex != 1 {
		t.Fatalf("first = %+v", d)
	}
	raw := capturedVetStdout[d.ByteStart:d.ByteEnd]
	if !strings.HasPrefix(raw, "{") || !strings.HasSuffix(raw, "}") || !strings.Contains(raw, "x_test.go:5:40") {
		t.Fatalf("byte range = %q", raw)
	}
	if vs.Diagnostics[2].SuggestedFixes != 1 {
		t.Fatalf("suggested fixes not counted")
	}
}

func TestParseVetStreamDamaged(t *testing.T) {
	half := capturedVetStdout[:len(capturedVetStdout)/2]
	cases := map[string]struct {
		in            string
		wantDiags     int
		wantUnparsed  bool
		wantShapeErrs int
	}{
		"truncated":      {half, 1, true, 0},
		"trailing":       {capturedVetStdout + "not json", 3, true, 0},
		"array":          {"[1,2]", 0, true, 0},
		"null value":     {"null", 0, false, 1},
		"bad analyzer":   {`{"p":{"a":"text"}}`, 0, false, 1},
		"bad package":    {`{"p":[1]}`, 0, false, 1},
		"error no field": {`{"p":{"a":{"x":1}}}`, 0, false, 1},
		"empty":          {"", 0, false, 0},
		"whitespace":     {"\n \t\n", 0, false, 0},
	}
	for name, c := range cases {
		vs := parseVetStream([]byte(c.in))
		if len(vs.Diagnostics) != c.wantDiags || (vs.UnparsedBytes > 0) != c.wantUnparsed || vs.ShapeErrors != c.wantShapeErrs {
			t.Fatalf("%s: %+v", name, vs)
		}
		if c.wantUnparsed && vs.DecodeError == "" {
			t.Fatalf("%s: decode error not classified", name)
		}
	}
	vs := parseVetStream([]byte(`{"example.test/p":{"printf":{"error":"analysis failed"}}}`))
	if len(vs.AnalyzerErrors) != 1 || vs.AnalyzerErrors[0].RawRef.JSONPointer != "/example.test~1p/printf/error" {
		t.Fatalf("analyzer error = %+v", vs.AnalyzerErrors)
	}
}

func TestParseStderr(t *testing.T) {
	in := capturedTypeErrStderr +
		"d.go:3:8: missing go.sum entry for module providing package example.invalid/x (imported by example.test/d); to add:\n\tgo get example.test/d\n" +
		"go: go.mod requires go >= 1.99 (running go 1.27.4; GOTOOLCHAIN=local)\n" +
		"pattern ./...: directory prefix . does not contain main module or its selected dependencies\n"
	lines, dropped := parseStderr([]byte(in))
	if dropped != 0 {
		t.Fatalf("dropped %d", dropped)
	}
	kinds := []string{}
	for _, l := range lines {
		kinds = append(kinds, l.Kind)
	}
	if strings.Join(kinds, ",") != "header,typecheck,position,continuation,go_command,other" {
		t.Fatalf("kinds = %v", kinds)
	}
	if lines[1].Package != "example.test/probe/typeerr" || lines[1].Pos != "typeerr/t.go:3:23" || lines[1].ByteOffset != int64(len("# example.test/probe/typeerr\n")) {
		t.Fatalf("typecheck = %+v", lines[1])
	}
	if !isDependencyError(lines[2].Message) || !isToolchainError(lines[4].Message) || !isModuleMissing(lines[5].Message) {
		t.Fatal("stable phrases not classified")
	}
	many := strings.Repeat("x.go:1:1: e\n", maxStderrLines+5)
	if l, d := parseStderr([]byte(many)); len(l) != maxStderrLines || d != 5 {
		t.Fatalf("bounded lines = %d dropped %d", len(l), d)
	}
}

func TestSplitPositionAndVersion(t *testing.T) {
	cases := []struct {
		in        string
		path      string
		line, col int
		ok        bool
	}{
		{"[REDACTED]/a/b.go:10:13", "[REDACTED]/a/b.go", 10, 13, true},
		{`C:\w\a.go:3:4`, `C:\w\a.go`, 3, 4, true},
		{"a.go:7", "a.go", 7, 0, true},
		{"a:b.go:1:2", "a:b.go", 1, 2, true},
		{"", "", 0, 0, false},
		{"a.go", "", 0, 0, false},
		{"a.go:x", "", 0, 0, false},
	}
	for _, c := range cases {
		p, l, col, ok := splitPosition(c.in)
		if p != c.path || l != c.line || col != c.col || ok != c.ok {
			t.Fatalf("%q → %q %d %d %v", c.in, p, l, col, ok)
		}
	}
	if v, p, ok := parseVersion([]byte("go version go1.27.4 linux/amd64\n")); !ok || v != "go1.27.4" || p != "linux/amd64" || !versionTested(v) {
		t.Fatalf("version = %s %s %v", v, p, ok)
	}
	if v, _, ok := parseVersion([]byte("go version go1.28rc1 windows/arm64")); !ok || v != "go1.28rc1" || versionTested(v) {
		t.Fatalf("rc version = %s %v", v, ok)
	}
	for _, bad := range []string{"", "go1.27.4", "go version devel +abc linux/amd64", "go version go1.27.4 linux/amd64\nextra"} {
		if _, _, ok := parseVersion([]byte(bad)); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	if versionTested("go1.270") {
		t.Fatal("prefix match must respect the minor boundary")
	}
}

func exited(code int, stdout, stderr string) *executor.Result {
	return &executor.Result{Status: executor.StatusExited, ExitCode: &code, Cleanup: executor.CleanupConfirmed,
		Stdout: executor.Stream{Data: []byte(stdout), TotalBytes: int64(len(stdout))}, Stderr: executor.Stream{Data: []byte(stderr), TotalBytes: int64(len(stderr))},
		Command: executor.CommandRecord{Executable: "/opt/go/bin/go", Argv: []string{"/opt/go/bin/go", "vet"}, Dir: "[REDACTED]", EnvKeys: []string{"GOPROXY", "PATH"}}}
}

const capturedList = `{"Dir":"[REDACTED]/bad","ImportPath":"example.test/probe/bad","Name":"bad","GoFiles":["bad.go"],"TestGoFiles":["bad_test.go"],"XTestGoFiles":["x_test.go"]}
{"Dir":"[REDACTED]/clean","ImportPath":"example.test/probe/clean","Name":"clean","GoFiles":["c.go"]}`

func syntheticInput(vetStdout string) assembleInput {
	files := map[string]source.Entry{}
	for _, p := range []string{"go.mod", "bad/bad.go", "bad/bad_test.go", "bad/x_test.go", "clean/c.go"} {
		files[p] = source.Entry{Path: p, Kind: "file", SHA256: strings.Repeat("a", 64)}
	}
	return assembleInput{
		TaskKey: "go-vet", ManifestDigest: strings.Repeat("b", 64), Module: ModuleRef{Dir: "."}, Patterns: []string{"./..."},
		Files: files, ScopeGoFiles: []string{"bad/bad.go", "bad/bad_test.go", "bad/x_test.go", "clean/c.go"},
		Lines:   func(string) (int, string, bool) { return 20, strings.Repeat("a", 64), true },
		Version: exited(0, "go version go1.27.4 linux/amd64\n", ""), ModEdit: exited(0, `{"Module":{"Path":"example.test/probe"}}`, ""),
		List: exited(0, capturedList, ""), Vet: exited(0, vetStdout, ""),
	}
}

// IDs and order do not depend on go vet's nondeterministic package order.
func TestAssembleOrderIndependent(t *testing.T) {
	parts := strings.SplitAfter(capturedVetStdout, "\n}\n")
	reordered := parts[1] + parts[0] + parts[2]
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "{}") {
		t.Fatalf("unexpected capture split: %d", len(parts))
	}
	a := assemble(syntheticInput(capturedVetStdout))
	b := assemble(syntheticInput(reordered))
	if len(a.Observations) != 3 || len(b.Observations) != 3 {
		t.Fatalf("counts %d %d", len(a.Observations), len(b.Observations))
	}
	for i := range a.Observations {
		if a.Observations[i].ID != b.Observations[i].ID || a.Observations[i].RuleID != b.Observations[i].RuleID {
			t.Fatalf("order-dependent output at %d", i)
		}
	}
	if a.Outcome.TaskStatus != domain.TaskSucceeded || a.Outcome.ExitClass != ExitSuccessWithFindings {
		t.Fatalf("outcome = %+v", a.Outcome)
	}
	if a.Observations[0].Location.Path != "bad/bad.go" || a.Observations[0].Location.StartLine != 10 {
		t.Fatalf("sorted first = %+v", a.Observations[0].Location)
	}
}

func TestAssembleLocationStates(t *testing.T) {
	stdout := `{"example.test/probe/bad":{"printf":[
{"posn":"[REDACTED]/bad/bad.go:99:1","end":"","message":"line out of range"},
{"posn":"/elsewhere/x.go:1:1","end":"","message":"outside /elsewhere"},
{"posn":"[REDACTED]/gen/zz.go:1:1","end":"","message":"generated"},
{"posn":"","end":"","message":"no position"},
{"posn":"[REDACTED]/bad/bad.go:3:1","end":"[REDACTED]/bad/bad.go:2:1","message":"end before start"}]}}`
	in := syntheticInput(stdout)
	doc := assemble(in)
	states := map[string]string{}
	for _, o := range doc.Observations {
		states[o.RawMessage] = o.LocationState
		if o.LocationState != LocationResolved && o.Location != nil {
			t.Fatalf("unresolved observation carries a location: %+v", o)
		}
	}
	want := map[string]string{"line out of range": LocationLineOutOfRange, "outside /elsewhere": LocationOutsideSnapshot, "generated": LocationMissingFile, "no position": LocationNoPosition, "end before start": LocationLineOutOfRange}
	for m, s := range want {
		if states[m] != s {
			t.Fatalf("%s: %s want %s", m, states[m], s)
		}
	}
	if doc.Parse.UnresolvedObservations != 5 {
		t.Fatalf("unresolved = %d", doc.Parse.UnresolvedObservations)
	}
	// Tool success with unresolved locations stays a success; the report builder must not link them.
	if doc.Outcome.TaskStatus != domain.TaskSucceeded {
		t.Fatalf("outcome = %+v", doc.Outcome)
	}
}

func TestAssembleExecutorStatuses(t *testing.T) {
	type tc struct {
		mut    func(*assembleInput)
		status domain.TaskStatus
		reason string
	}
	code := 1
	cases := map[string]tc{
		"vet start failed": {func(in *assembleInput) {
			in.Vet = &executor.Result{Status: executor.StatusStartFailed, Cleanup: executor.CleanupNotStarted}
		}, domain.TaskFailed, ReasonToolStartFailed},
		"unsupported": {func(in *assembleInput) {
			in.Version = &executor.Result{Status: executor.StatusUnsupported, Cleanup: executor.CleanupNotStarted}
			in.ModEdit, in.List, in.Vet = nil, nil, nil
		}, domain.TaskUnsupported, ReasonExecutionUnsupported},
		"vet not run":       {func(in *assembleInput) { in.Vet = nil }, domain.TaskFailed, ReasonNotRun},
		"go.mod unreadable": {func(in *assembleInput) { in.ModEdit = exited(1, "", "go: errors parsing go.mod") }, domain.TaskFailed, ReasonGoModUnreadable},
		"replace escapes": {func(in *assembleInput) {
			in.ModEdit = exited(0, capturedModEdit, "")
		}, domain.TaskSkippedPolicy, ReasonReplaceOutside},
		"drift": {func(in *assembleInput) { in.Drift = map[string]bool{"clean/c.go": true} }, domain.TaskFailed, ReasonWorkspaceDrift},
		"nothing vetted": {func(in *assembleInput) {
			in.List, in.Vet = exited(0, "", ""), exited(0, "", "")
		}, domain.TaskUnsupported, ReasonNoPackages},
		"cancelled before vet": {func(in *assembleInput) { in.Vet = nil; in.Cancelled = true }, domain.TaskCancelled, ReasonCancelled},
		"timeout with uncertain cleanup": {func(in *assembleInput) {
			in.Vet.Status, in.Vet.Cleanup, in.Vet.ExitCode = executor.StatusTimedOut, executor.CleanupUncertain, nil
		}, domain.TaskFailed, ReasonCleanupUncertain},
		"vet exit 2 no stderr": {func(in *assembleInput) { c := 2; in.Vet.ExitCode = &c }, domain.TaskFailed, ReasonVetFailed},
		"list package error": {func(in *assembleInput) {
			in.List = exited(0, `{"Dir":"[REDACTED]/bad","ImportPath":"example.test/probe/bad","Error":{"Err":"bad thing"}}`, "")
		}, domain.TaskFailed, ReasonPackageLoad},
		"list exit 1": {func(in *assembleInput) { in.List.ExitCode = &code }, domain.TaskFailed, ReasonPackageLoad},
		"analyzer error": {func(in *assembleInput) {
			in.Vet = exited(0, `{"example.test/probe/clean":{"printf":{"error":"boom"}}}`, "")
		}, domain.TaskFailed, ReasonAnalyzerError},
		"stderr incomplete": {func(in *assembleInput) { in.Vet.Stderr.Incomplete = true }, domain.TaskFailed, ReasonOutputTruncated},
		"bad version":       {func(in *assembleInput) { in.Version = exited(0, "weird\n", "") }, domain.TaskFailed, ReasonToolVersion},
	}
	for name, c := range cases {
		in := syntheticInput(capturedVetStdout)
		c.mut(&in)
		doc := assemble(in)
		got := ""
		if doc.Outcome.Reason != nil {
			got = *doc.Outcome.Reason
		}
		if doc.Outcome.TaskStatus != c.status || got != c.reason {
			t.Fatalf("%s: %s/%s", name, doc.Outcome.TaskStatus, got)
		}
		if doc.Outcome.TaskStatus == domain.TaskSucceeded || doc.Parse.OK {
			t.Fatalf("%s: reported success", name)
		}
	}
}

func TestMaskerAndManifestCommand(t *testing.T) {
	m := masker([]string{"/srv/data/acquire-1/ws"}, "/opt/go", "/opt/go/bin/go", "/var/cache/gomod", "/srv/scratch/a1", "/srv/data/acquire-1")
	got := m("[REDACTED]/a.go: /opt/go/src/fmt/print.go and /var/cache/gomod/x@v1/y.go /srv/scratch/a1/t /opt/go/bin/go /srv/data/acquire-1/ws/b.go")
	want := "a.go: $GOROOT/src/fmt/print.go and $GOMODCACHE/x@v1/y.go $SCRATCH/t $GO b.go"
	if got != want {
		t.Fatalf("mask = %q", got)
	}
	doc := assemble(syntheticInput(capturedVetStdout))
	for _, e := range doc.Executions {
		if e.Command.Argv[0] != "go" || e.Command.Dir != "." {
			t.Fatalf("command = %+v", e.Command)
		}
	}
}

// Captured shape of go1.27.4 `go mod edit -json`; "a//b" is a module path,
// not a comment, which a hand-written go.mod scanner gets wrong.
const capturedModEdit = `{
	"Module": {"Path": "example.test/m"},
	"Go": "1.99",
	"Replace": [
		{"Old": {"Path": "a//b"}, "New": {"Path": "/etc/x"}},
		{"Old": {"Path": "c", "Version": "v1.0.0"}, "New": {"Path": "../up"}},
		{"Old": {"Path": "d"}, "New": {"Path": "e", "Version": "v1.2.3"}},
		{"Old": {"Path": "f"}, "New": {"Path": "./inside/f"}},
		{"Old": {"Path": "g"}, "New": {"Path": "..\\win"}},
		{"Old": {"Path": "h"}, "New": {"Path": "C:\\abs"}}
	]
}
`

func TestParseModEditAndEscapes(t *testing.T) {
	mod, targets, ok := parseModEdit([]byte(capturedModEdit))
	if !ok || mod != "example.test/m" || strings.Join(targets, "|") != `/etc/x|../up|./inside/f|..\win|C:\abs` {
		t.Fatalf("parse = %q %q %v", mod, targets, ok)
	}
	if got := escapingReplaces(targets, ".", false); strings.Join(got, "|") != `/etc/x|../up|..\win|C:\abs` {
		t.Fatalf("root escapes = %q", got)
	}
	if got := escapingReplaces([]string{"[REDACTED]/sub", "[REDACTED]2/x", "./a/[REDACTED]"}, ".", false); len(got) != 3 {
		t.Fatalf("redacted targets must be refused: %q", got)
	}
	if got := escapingReplaces([]string{"../shared"}, "tools/sub", false); len(got) != 0 {
		t.Fatalf("nested escapes = %q", got)
	}
	if got := escapingReplaces([]string{"../../../up", ""}, "tools/sub", true); len(got) != 2 {
		t.Fatalf("nested escape missed: %q", got)
	}
	for _, bad := range []string{"", "{", capturedModEdit + "{}", "[]"} {
		if _, _, ok := parseModEdit([]byte(bad)); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
}

// Tool JSON escapes backslashes and "&", so the executor's byte redaction can miss the
// workspace root; decoded absolute roots (case-insensitive on Windows) are
// stripped by the parser itself.
func TestAssembleUnredactedRoots(t *testing.T) {
	win := syntheticInput(`{"example.test/probe/bad":{"printf":[{"posn":"c:\\W\\ws\\bad\\bad.go:12:24","end":"C:\\w\\ws\\bad\\bad.go:12:26","message":"m C:\\w\\ws\\bad\\bad.go"}]}}`)
	win.List = exited(0, `{"Dir":"C:\\w\\ws\\bad","ImportPath":"example.test/probe/bad","GoFiles":["bad.go"],"TestGoFiles":["bad_test.go"],"XTestGoFiles":["x_test.go"]}
{"Dir":"C:\\w\\ws\\clean","ImportPath":"example.test/probe/clean","GoFiles":["c.go"]}`, "")
	win.Windows, win.WorkspaceRoots = true, []string{`C:\w\ws`}
	win.Mask = masker(win.WorkspaceRoots, "", "", "", "", "")
	doc := assemble(win)
	if doc.Outcome.TaskStatus != domain.TaskSucceeded || len(doc.Observations) != 1 || doc.Observations[0].Location == nil || doc.Observations[0].Location.Path != "bad/bad.go" || stateOf(doc, "bad") != PackageAnalyzedWithFindings {
		t.Fatalf("windows doc: %+v %+v", doc.Outcome, doc.Inventory)
	}
	// Same input on a non-Windows node: backslash paths are not workspace paths.
	win.Windows = false
	if doc := assemble(win); doc.Observations[0].Location != nil {
		t.Fatal("backslash path resolved on a non-Windows node")
	}
	lin := syntheticInput(`{"example.test/probe/bad":{"printf":[{"posn":"/tmp/a\u0026b/ws/bad/bad.go:12:24","end":"","message":"m"}]}}`)
	lin.List = exited(0, strings.ReplaceAll(capturedList, "[REDACTED]", "/tmp/a\u0026b/ws"), "")
	lin.WorkspaceRoots = []string{"/tmp/a&b/ws"}
	doc = assemble(lin)
	if doc.Outcome.TaskStatus != domain.TaskSucceeded || doc.Observations[0].Location == nil || stateOf(doc, "clean") != PackageAnalyzedClean {
		t.Fatalf("linux & root: %+v", doc.Outcome)
	}
	forms := redactForms([]string{"/tmp/a&b/ws", `C:\w\ws`})
	if strings.Join(forms, "|") != `/tmp/a&b/ws|/tmp/a\u0026b/ws|C:\w\ws|C:\\w\\ws` {
		t.Fatalf("redact forms = %q", forms)
	}
}

func TestWindowsVetExecutablePrefix(t *testing.T) {
	lines, _ := parseStderr([]byte("# example.test/probe/typeerr\r\nvet.exe: typeerr\\t.go:3:23: cannot use value\r\n"))
	if len(lines) != 2 || lines[1].Kind != "typecheck" || lines[1].Pos != `typeerr\t.go:3:23` {
		t.Fatalf("%+v", lines)
	}
}

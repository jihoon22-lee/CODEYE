package envdoc_test

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
)

func tools() []envdoc.AnalyzerTool {
	return []envdoc.AnalyzerTool{
		{Tool: "golangci-next", Analyzers: []string{"go.next"}, Status: envdoc.ObsNotProbed},
		{Tool: "staticcheck", Analyzers: []string{"go.staticcheck"}, Status: envdoc.ObsObserved, Version: ptr("2026.2.2 (0.8.2)"), Execution: ptr("staticcheck.version")},
	}
}

// docs/ANALYZERS.md: analyzer_tools is additive (absent in older documents) and records only
// what a probe execution of this attempt showed.
func TestAnalyzerToolsValidation(t *testing.T) {
	in, env, _ := fixture(t)
	d := clone(t, env)
	d.Environment.AnalyzerTools = tools()
	b, err := envdoc.Marshal(d, in.Expect(d.Kind))
	if err != nil {
		t.Fatal(err)
	}
	got, err := envdoc.Decode(b, in.Expect(d.Kind))
	if err != nil || len(got.Environment.AnalyzerTools) != 2 {
		t.Fatalf("round trip %v %+v", err, got.Environment)
	}
	// Without tools the field is omitted: older documents stay identical.
	plain, _ := envdoc.Marshal(env, in.Expect(env.Kind))
	if strings.Contains(string(plain), "analyzer_tools") {
		t.Fatal("empty analyzer_tools serialized")
	}
	cases := map[string]func(l []envdoc.AnalyzerTool){
		"unsorted":                 func(l []envdoc.AnalyzerTool) { l[0], l[1] = l[1], l[0] },
		"runtime tool name":        func(l []envdoc.AnalyzerTool) { l[0].Tool = "go" },
		"bad tool name":            func(l []envdoc.AnalyzerTool) { l[0].Tool = "Golangci" },
		"no analyzers":             func(l []envdoc.AnalyzerTool) { l[1].Analyzers = nil },
		"bad analyzer id":          func(l []envdoc.AnalyzerTool) { l[1].Analyzers = []string{"staticcheck"} },
		"observed without version": func(l []envdoc.AnalyzerTool) { l[1].Version = nil },
		"version with a path":      func(l []envdoc.AnalyzerTool) { l[1].Version = ptr("/opt/staticcheck 1") },
		"not_probed with version":  func(l []envdoc.AnalyzerTool) { l[0].Version = ptr("1.0") },
		"not_probed with exec":     func(l []envdoc.AnalyzerTool) { l[0].Execution = ptr("golangci-next.version") },
		"observed without exec":    func(l []envdoc.AnalyzerTool) { l[1].Execution = nil },
		"other execution":          func(l []envdoc.AnalyzerTool) { l[1].Execution = ptr("go.version") },
		"failed without detail":    func(l []envdoc.AnalyzerTool) { l[1].Status, l[1].Version = envdoc.ObsProbeFailed, nil },
		"observed with detail":     func(l []envdoc.AnalyzerTool) { l[1].Detail = ptr("exit_nonzero") },
		"unknown status":           func(l []envdoc.AnalyzerTool) { l[0].Status = "configured" },
	}
	for name, edit := range cases {
		d := clone(t, env)
		l := tools()
		edit(l)
		d.Environment.AnalyzerTools = l
		if _, err := envdoc.Marshal(d, in.Expect(d.Kind)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

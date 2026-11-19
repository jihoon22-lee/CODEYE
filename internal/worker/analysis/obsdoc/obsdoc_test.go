package obsdoc

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// The example is a real staticcheck 2026.2.2 envelope (docs/ANALYZERS.md#staticcheck).
func example(t *testing.T) map[string]any {
	t.Helper()
	b, err := os.ReadFile("../../../../examples/observations-staticcheck.json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func encode(t *testing.T, d map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// lookupOf is the manifest the example was produced against, rebuilt from
// its own locations and processed paths.
func lookupOf(d map[string]any) func(string) (string, bool) {
	index := map[string]string{}
	for _, p := range d["coverage"].(map[string]any)["processed_paths"].([]any) {
		index[p.(string)] = strings.Repeat("b", 64)
	}
	for _, o := range d["observations"].([]any) {
		if l, ok := o.(map[string]any)["location"].(map[string]any); ok {
			index[l["path"].(string)] = l["blob_sha256"].(string)
		}
	}
	return func(p string) (string, bool) { s, ok := index[p]; return s, ok }
}

func TestDecodeExampleRoundTrip(t *testing.T) {
	d := example(t)
	want := Expect{TaskKey: "staticcheck", Snapshot: d["snapshot_manifest_digest"].(string), Lookup: lookupOf(d)}
	doc, err := Decode(encode(t, d), want)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Analyzer.ID != "go.staticcheck" || doc.Analyzer.Required || doc.Outcome.ExitClass != ExitSuccessWithFindings || len(doc.Observations) != 7 {
		t.Fatalf("decoded %+v %+v", doc.Analyzer, doc.Outcome)
	}
	b, err := Marshal(doc, want)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := domain.CanonicalJSON(encode(t, d))
	if string(b) != string(c) {
		t.Fatal("Marshal(Decode(x)) is not the canonical form of x")
	}
	if _, err := Decode(encode(t, d), Expect{TaskKey: "staticcheck.m01"}); !errors.Is(err, ErrInvalid) {
		t.Fatal("other task key accepted")
	}
	if _, err := Decode(encode(t, d), Expect{Snapshot: strings.Repeat("f", 64)}); !errors.Is(err, ErrInvalid) {
		t.Fatal("other snapshot accepted")
	}
}

func obs0(d map[string]any) map[string]any {
	return d["observations"].([]any)[0].(map[string]any)
}

// Every rule of Validate rejects a document that claims more than it records.
func TestValidateRejects(t *testing.T) {
	cases := map[string]func(d map[string]any){
		"go vet kind":                func(d map[string]any) { d["kind"] = "go_vet_observations" },
		"unknown field":              func(d map[string]any) { d["extra"] = 1 },
		"hyphen analyzer id":         func(d map[string]any) { d["analyzer"].(map[string]any)["id"] = "go.static-check" },
		"unknown ecosystem":          func(d map[string]any) { d["analyzer"].(map[string]any)["ecosystem"] = "java" },
		"success with partial parse": func(d map[string]any) { d["outcome"].(map[string]any)["partial_parse"] = true },
		"success with reason":        func(d map[string]any) { d["outcome"].(map[string]any)["reason"] = "x" },
		"success wrong exit class":   func(d map[string]any) { d["outcome"].(map[string]any)["exit_class"] = ExitSuccessWithoutFindings },
		"success without processed":  func(d map[string]any) { d["coverage"].(map[string]any)["processed_paths"] = nil },
		"success with tool error": func(d map[string]any) {
			d["tool_errors"] = []any{map[string]any{"execution": "staticcheck.check", "kind": "typecheck", "unit": "u", "message": "m", "path": nil, "line": 0, "column": 0}}
			d["parse"].(map[string]any)["tool_errors"] = 1
		},
		"failed with success class": func(d map[string]any) {
			d["outcome"].(map[string]any)["task_status"] = "failed"
			d["outcome"].(map[string]any)["reason"] = "x"
		},
		"not run with observations": func(d map[string]any) {
			d["outcome"] = map[string]any{"task_status": "unsupported", "reason": "tool_not_configured", "exit_class": ExitNotRun, "partial_parse": false}
		},
		"unclean execution on success": func(d map[string]any) {
			d["executions"].([]any)[0].(map[string]any)["stdout"].(map[string]any)["truncated"] = true
		},
		"node path in argv": func(d map[string]any) {
			e := d["executions"].([]any)[4].(map[string]any)["command"].(map[string]any)
			e["argv"] = append(e["argv"].([]any), "/home/u/x")
		},
		"path in argv0": func(d map[string]any) {
			d["executions"].([]any)[0].(map[string]any)["command"].(map[string]any)["argv"].([]any)[0] = "bin/go"
		},
		"absolute target property": func(d map[string]any) {
			d["target"].(map[string]any)["properties"].(map[string]any)["goroot"] = "/usr/lib/go"
		},
		"absolute rule selection":   func(d map[string]any) { d["rule_pack"].(map[string]any)["selection"] = []any{"-conf", "C:\\x"} },
		"unsorted limitations":      func(d map[string]any) { d["limitations"] = []any{"b_x", "a_x"} },
		"tested without version":    func(d map[string]any) { d["tool"].(map[string]any)["version"] = nil },
		"raw ref other stream sha":  func(d map[string]any) { obs0(d)["raw_ref"].(map[string]any)["stream_sha256"] = strings.Repeat("e", 64) },
		"raw ref past captured":     func(d map[string]any) { obs0(d)["raw_ref"].(map[string]any)["byte_end"] = 1 << 20 },
		"raw ref unknown exec":      func(d map[string]any) { obs0(d)["raw_ref"].(map[string]any)["execution"] = "staticcheck.other" },
		"empty range":               func(d map[string]any) { r := obs0(d)["raw_ref"].(map[string]any); r["byte_end"] = r["byte_start"] },
		"resolved without location": func(d map[string]any) { obs0(d)["location"] = nil },
		"location path differs":     func(d map[string]any) { obs0(d)["path"] = "find/other.go" },
		"unknown category":          func(d map[string]any) { obs0(d)["category"] = "style" },
		"duplicate observation id": func(d map[string]any) {
			d["observations"].([]any)[1].(map[string]any)["id"] = obs0(d)["id"]
		},
		"unresolved count":  func(d map[string]any) { d["parse"].(map[string]any)["unresolved_observations"] = 1 },
		"observation count": func(d map[string]any) { d["parse"].(map[string]any)["observations"] = 6 },
		"drifted file linked": func(d map[string]any) {
			d["workspace_drift"] = []any{"find/find.go"}
			d["outcome"] = map[string]any{"task_status": "failed", "reason": "workspace_drift", "exit_class": ExitExecutionError, "partial_parse": true}
			d["coverage"].(map[string]any)["processed_paths"] = nil
		},
		"manifest blob differs": func(d map[string]any) {
			obs0(d)["location"].(map[string]any)["blob_sha256"] = strings.Repeat("0", 64)
		},
		"processed path outside manifest": func(d map[string]any) {
			d["coverage"].(map[string]any)["processed_paths"] = []any{"clean/c.go", "find/find.go", "find/find_test.go", "zz/none.go"}
		},
		"processed paths unsorted": func(d map[string]any) {
			d["coverage"].(map[string]any)["processed_paths"] = []any{"find/find.go", "clean/c.go"}
		},
		// Rule context (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제).
		"config files with overridden config": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["config_files"] = []any{map[string]any{"path": "clean/c.go", "sha256": strings.Repeat("b", 64)}}
		},
		"null config files with overridden config": func(d map[string]any) { d["rule_pack"].(map[string]any)["config_files"] = nil },
		"missing config files":                     func(d map[string]any) { delete(d["rule_pack"].(map[string]any), "config_files") },
		"honored config file not in manifest": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["repository_config"] = RepoConfigHonored
			d["rule_pack"].(map[string]any)["config_files"] = []any{map[string]any{"path": "staticcheck.conf", "sha256": strings.Repeat("c", 64)}}
		},
		"honored config file other sha": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["repository_config"] = RepoConfigHonored
			d["rule_pack"].(map[string]any)["config_files"] = []any{map[string]any{"path": "clean/c.go", "sha256": strings.Repeat("c", 64)}}
		},
		"honored config files unsorted": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["repository_config"] = RepoConfigHonored
			d["rule_pack"].(map[string]any)["config_files"] = []any{map[string]any{"path": "find/find.go", "sha256": strings.Repeat("b", 64)},
				map[string]any{"path": "clean/c.go", "sha256": strings.Repeat("b", 64)}}
		},
		"missing unreported suppression rules": func(d map[string]any) { delete(d["rule_pack"].(map[string]any), "unreported_suppression_rules") },
		"null unreported suppression rules":    func(d map[string]any) { d["rule_pack"].(map[string]any)["unreported_suppression_rules"] = nil },
		"unreported suppression rules unsorted": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["unreported_suppression_rules"] = []any{"U1000", "SA4006"}
		},
		"unreported suppression rule with space": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["unreported_suppression_rules"] = []any{"U 1000"}
		},
		"unreported suppression rules under honored": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["source_suppressions"] = SuppressHonored
		},
		"suppressed without reported": func(d map[string]any) {
			d["rule_pack"].(map[string]any)["source_suppressions"] = SuppressHonored
			d["rule_pack"].(map[string]any)["unreported_suppression_rules"] = []any{}
			obs0(d)["suppressed"] = true
		},
		"only suppressed observations with findings class": func(d map[string]any) {
			for _, o := range d["observations"].([]any) {
				o.(map[string]any)["suppressed"] = true
			}
		},
		"related unresolved with location": func(d map[string]any) {
			for _, o := range d["observations"].([]any) {
				for _, r := range o.(map[string]any)["related"].([]any) {
					r.(map[string]any)["location_state"] = LocationNoPosition
				}
			}
		},
	}
	for name, edit := range cases {
		d := example(t)
		lookup := lookupOf(d)
		edit(d)
		if _, err := Decode(encode(t, d), Expect{Lookup: lookup}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted (%v)", name, err)
		}
	}
}

// A not-run document records nothing processed (known zero) and no
// observation; a failed one may keep what was parsed with unknown coverage.
func TestValidateNotRunAndFailed(t *testing.T) {
	d := example(t)
	d["outcome"] = map[string]any{"task_status": "unsupported", "reason": "tool_not_configured", "exit_class": ExitNotRun, "partial_parse": false}
	d["observations"] = []any{}
	d["parse"].(map[string]any)["observations"] = 0
	d["parse"].(map[string]any)["ok"] = false
	cov := d["coverage"].(map[string]any)
	cov["processed_paths"] = []any{}
	if _, err := Decode(encode(t, d), Expect{}); err != nil {
		t.Fatal(err)
	}
	cov["processed_paths"] = nil
	if _, err := Decode(encode(t, d), Expect{}); err == nil {
		t.Fatal("not_run with unknown coverage accepted")
	}
	f := example(t)
	f["outcome"] = map[string]any{"task_status": "failed", "reason": "package_errors", "exit_class": ExitExecutionError, "partial_parse": true}
	f["coverage"].(map[string]any)["processed_paths"] = nil
	f["parse"].(map[string]any)["ok"] = false
	if _, err := Decode(encode(t, f), Expect{}); err != nil {
		t.Fatal(err)
	}
}

// The rule context a pack may record (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제): honored repository
// configuration with the files read (bound to the manifest) or null when it
// cannot enumerate them, and suppressed observations under reported
// suppressions, which are not findings.
func TestRuleContextAccepted(t *testing.T) {
	d := example(t)
	lookup := lookupOf(d)
	rp := d["rule_pack"].(map[string]any)
	rp["repository_config"] = RepoConfigHonored
	rp["config_files"] = []any{map[string]any{"path": "clean/c.go", "sha256": strings.Repeat("b", 64)}}
	if doc, err := Decode(encode(t, d), Expect{Lookup: lookup}); err != nil || len(doc.RulePack.ConfigFiles) != 1 {
		t.Fatal(err)
	}
	rp["config_files"] = nil
	if doc, err := Decode(encode(t, d), Expect{Lookup: lookup}); err != nil || doc.RulePack.ConfigFiles != nil {
		t.Fatalf("unenumerated config files: %v", err)
	}
	rp["config_files"] = []any{}
	if doc, err := Decode(encode(t, d), Expect{Lookup: lookup}); err != nil || doc.RulePack.ConfigFiles == nil {
		t.Fatalf("no config file read: %v", err)
	}
	s := example(t)
	for _, o := range s["observations"].([]any) {
		o.(map[string]any)["suppressed"] = true
	}
	s["outcome"].(map[string]any)["exit_class"] = ExitSuccessWithoutFindings
	doc, err := Decode(encode(t, s), Expect{Lookup: lookupOf(s)})
	if err != nil || doc.Active() != 0 || len(doc.Observations) != 7 {
		t.Fatalf("all suppressed: %v", err)
	}
}

// Metrics (docs/ANALYZERS.md): a metric observation is a structure measurement over its
// threshold. It is never active (no finding, never success_with_findings)
// and its fields are bounded; older envelopes without metric decode as
// before.
func TestMetricObservations(t *testing.T) {
	d := example(t)
	for _, o := range d["observations"].([]any) {
		o.(map[string]any)["metric"] = map[string]any{"name": "cyclomatic_complexity", "value": 17, "threshold": 16, "unit": "count"}
	}
	d["outcome"].(map[string]any)["exit_class"] = ExitSuccessWithoutFindings
	doc, err := Decode(encode(t, d), Expect{Lookup: lookupOf(d)})
	if err != nil || doc.Active() != 0 || len(doc.Observations) != 7 || doc.Observations[0].Metric.Value != 17 {
		t.Fatalf("metric document: %v", err)
	}
	for name, m := range map[string]map[string]any{
		"under threshold": {"name": "cyclomatic_complexity", "value": 15, "threshold": 16, "unit": "count"},
		"unknown unit":    {"name": "cyclomatic_complexity", "value": 17, "threshold": 16, "unit": "percent"},
		"bad name":        {"name": "Cyclomatic Complexity", "value": 17, "threshold": 16, "unit": "count"},
		"negative":        {"name": "duplicated_lines", "value": -1, "threshold": -2, "unit": "lines"},
		"extra field":     {"name": "duplicated_lines", "value": 12, "threshold": 10, "unit": "lines", "x": 1},
	} {
		bad := example(t)
		obs0(bad)["metric"] = m
		if _, err := Decode(encode(t, bad), Expect{Lookup: lookupOf(bad)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s accepted", name)
		}
	}
	// With every observation a metric the class must not claim findings.
	d["outcome"].(map[string]any)["exit_class"] = ExitSuccessWithFindings
	if _, err := Decode(encode(t, d), Expect{Lookup: lookupOf(d)}); !errors.Is(err, ErrInvalid) {
		t.Fatal("metrics counted as findings")
	}
	if !IsSecretAnalyzer("any.gitleaks") || IsSecretAnalyzer("go.staticcheck") {
		t.Fatal("secret analyzers")
	}
}

func TestObservationIDIsContentDerived(t *testing.T) {
	a := ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 0)
	if a != ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 0) || !observationID.MatchString(a) {
		t.Fatal(a)
	}
	for _, b := range []string{
		ObservationID("go.vet", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 0),
		ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "m", 1),
		ObservationID("go.staticcheck", ".", "u", "SA1000", "f.go:1:1", "f.go:1:2", "n", 0),
	} {
		if a == b {
			t.Fatal("ID ignores a component")
		}
	}
	if !AnalyzerIDPattern.MatchString("cpp.clang_tidy") || AnalyzerIDPattern.MatchString("staticcheck") || AnalyzerIDPattern.MatchString("go.static-check") {
		t.Fatal("analyzer id pattern")
	}
	if SafeText("a\x00b", 10) || !SafeText("a\tb\nc", 10) || SafeText(strings.Repeat("x", 11), 10) {
		t.Fatal("SafeText")
	}
}

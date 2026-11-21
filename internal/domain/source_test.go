package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const rootID = "0b9f1c2e-6a8d-4c3b-9e71-2f4a5b6c7d8e"

func TestParseSourceAccepts(t *testing.T) {
	cases := map[string]Source{
		`{"kind":"local","source_root_id":"` + rootID + `","relative_path":"."}`:                                                     {Kind: SourceLocal, SourceRootID: rootID, RelativePath: "."},
		`{"kind":"local","source_root_id":"` + rootID + `","relative_path":"svc/api"}`:                                               {Kind: SourceLocal, SourceRootID: rootID, RelativePath: "svc/api"},
		`{"kind":"git","provider_id":"` + rootID + `","repository_url":"https://github.com/o/r.git","branch":"main"}`:                {Kind: SourceGit, ProviderID: rootID, RepositoryURL: "https://github.com/o/r.git", Branch: "main"},
		`{"kind":"git","provider_id":"` + rootID + `","repository_url":"https://ghe.example:8443/team/repo","branch":"release/1.2"}`: {Kind: SourceGit, ProviderID: rootID, RepositoryURL: "https://ghe.example:8443/team/repo", Branch: "release/1.2"},
		// No branch (or an empty one): the repository's default branch.
		`{"kind":"git","provider_id":"` + rootID + `","repository_url":"https://github.com/o/r"}`:             {Kind: SourceGit, ProviderID: rootID, RepositoryURL: "https://github.com/o/r"},
		`{"kind":"git","provider_id":"` + rootID + `","repository_url":"https://github.com/o/r","branch":""}`: {Kind: SourceGit, ProviderID: rootID, RepositoryURL: "https://github.com/o/r"},
	}
	for raw, want := range cases {
		got, err := ParseSource([]byte(raw))
		if err != nil || got != want {
			t.Fatalf("ParseSource(%s) = %+v, %v; want %+v", raw, got, err, want)
		}
	}
}

func TestParseSourceRejects(t *testing.T) {
	git := func(url, branch string) string {
		return `{"kind":"git","provider_id":"` + rootID + `","repository_url":"` + url + `","branch":"` + branch + `"}`
	}
	local := func(path string) string {
		return `{"kind":"local","source_root_id":"` + rootID + `","relative_path":"` + path + `"}`
	}
	invalid := []string{
		`{"kind":"svn"}`,
		`{"kind":"local","source_root_id":"` + rootID + `","relative_path":"a","extra":1}`,
		`{"kind":"local","source_root_id":"` + rootID + `","relative_path":"a","branch":"main"}`,
		`{"kind":"local","source_root_id":"x","relative_path":"a"}`,
		`{"kind":"local","kind":"git"}`,
		local(""), local("/etc"), local("../x"), local("a/../../b"), local(`a\\b`), local("C:/x"), local("a//b"),
		git("http://github.com/o/r", "main"),
		git("https://user:pw@github.com/o/r", "main"),
		git("https://github.com/o/r?x=1", "main"),
		git("https://github.com/o/r#frag", "main"),
		git("https://github.com/o", "main"),
		git("https://github.com/o/r/tree/main", "main"),
		git("https://github.com/o/../r", "main"),
		git("https://127.0.0.1/o/r", "main"),
		git("https://github.com/o/r", "a..b"),
		git("https://github.com/o/r", "-x"),
		git("https://github.com/o/r", "x.lock"),
		git("https://github.com/o/r", "a b"),
		git("https://github.com/o/r", "a@{1}"),
		`{"kind":"git","provider_id":"` + rootID + `","repository_url":"https://github.com/o/r","branch":"main","relative_path":"a"}`,
	}
	for _, raw := range invalid {
		if _, err := ParseSource([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseSource(%s) = %v, want ErrInvalid", raw, err)
		}
	}
	unsupported := []string{
		git("ssh://git@github.com/o/r.git", "main"),
		git("git@github.com:o/r.git", "main"),
		git("git://github.com/o/r", "main"),
	}
	for _, raw := range unsupported {
		_, err := ParseSource([]byte(raw))
		if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrInvalid) {
			t.Errorf("ParseSource(%s) = %v, want ErrUnsupported only", raw, err)
		}
	}
}

func TestRepositoryHostAndName(t *testing.T) {
	h, err := RepositoryHost("https://GitHub.com/Owner/Repo.git/")
	if err != nil || h != "github.com" {
		t.Fatalf("host = %q, %v", h, err)
	}
	if n := RepositoryName("https://github.com/o/repo.git"); n != "repo" {
		t.Fatalf("name = %q", n)
	}
}

func TestParseRunRequest(t *testing.T) {
	ok := []string{
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full","review_coverage":"extended"}}`,
		`{"profile":"focused","source_policy":"latest_at_acquire","scope":{"kind":"paths","paths":["src","docs/a.md"]}}`,
	}
	for _, raw := range ok {
		if _, err := ParseRunRequest([]byte(raw)); err != nil {
			t.Fatalf("ParseRunRequest(%s): %v", raw, err)
		}
	}
	bad := []string{
		`{}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire"}`,
		`{"profile":"fast","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`,
		`{"profile":"deep-review","source_policy":"pinned","scope":{"kind":"full"}}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full","paths":[]}}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full","review_coverage":"unbounded"}}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"paths","paths":[]}}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"paths","paths":["../x"]}}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"paths","paths":["a","a"]}}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"},"command":"rm -rf /"}`,
		`{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}} {}`,
		`[]`,
	}
	for _, raw := range bad {
		if _, err := ParseRunRequest([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseRunRequest(%s) = %v, want ErrInvalid", raw, err)
		}
	}
	many := `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"paths","paths":["` +
		strings.Repeat(`a","`, 100) + `b"]}}`
	if _, err := ParseRunRequest([]byte(many)); !errors.Is(err, ErrInvalid) {
		t.Errorf("101 scope paths accepted")
	}
}

func TestSplitRepositoryURL(t *testing.T) {
	cases := map[string][2]string{
		"https://github.com/o/r":                    {"https://github.com/o/r", ""},
		" https://github.com/o/r/ ":                 {"https://github.com/o/r", ""},
		"https://github.com/o/r.git":                {"https://github.com/o/r.git", ""},
		"github.com/o/r":                            {"https://github.com/o/r", ""},
		"https://github.com/o/r/tree/main":          {"https://github.com/o/r", "main"},
		"https://github.com/o/r/tree/release/1.2/":  {"https://github.com/o/r", "release/1.2"},
		"https://ghe.example:8443/team/repo/tree/x": {"https://ghe.example:8443/team/repo", "x"},
	}
	for in, want := range cases {
		u, b, err := SplitRepositoryURL(in)
		if err != nil || u != want[0] || b != want[1] {
			t.Errorf("SplitRepositoryURL(%q) = %q, %q, %v; want %q, %q", in, u, b, err, want[0], want[1])
		}
	}
	for _, in := range []string{"https://github.com/o", "https://github.com/o/r/blob/main/x.go", "http://github.com/o/r", "https://github.com/o/r/tree/a..b", "git@github.com:o/r.git", "/home/u/src"} {
		if _, _, err := SplitRepositoryURL(in); err == nil {
			t.Errorf("SplitRepositoryURL(%q) accepted", in)
		}
	}
}

// fetch_dependencies is optional everywhere (absent = on) and stored only
// when off, so a record written now still decodes for a reader that knows
// only the first two fields.
func TestFetchDependenciesChoice(t *testing.T) {
	for _, kind := range []SourceKind{SourceLocal, SourceGit} {
		if !DefaultAnalysisChoices(kind).FetchDependencies || !AnalysisChoicesOf(nil, kind).FetchDependencies {
			t.Fatalf("%s: default off", kind)
		}
	}
	two := `{"send_source_to_model":true,"run_build_scripts":false}`
	if c, err := ParseAnalysisChoices([]byte(two)); err != nil || !c.FetchDependencies {
		t.Fatalf("absent field: %+v %v", c, err)
	}
	if c, err := ParseAnalysisChoices([]byte(`{"send_source_to_model":true,"run_build_scripts":false,"fetch_dependencies":false}`)); err != nil || c.FetchDependencies {
		t.Fatalf("stored off: %+v %v", c, err)
	}
	var job AnalysisChoices
	if err := json.Unmarshal([]byte(two), &job); err != nil || !job.FetchDependencies || !job.SendSourceToModel {
		t.Fatalf("lenient decode: %+v %v", job, err)
	}
	on, _ := json.Marshal(AnalysisChoices{SendSourceToModel: true, FetchDependencies: true}.Stored())
	off, _ := json.Marshal(AnalysisChoices{SendSourceToModel: true}.Stored())
	if string(on) != `{"run_build_scripts":false,"send_source_to_model":true}` || string(off) != `{"fetch_dependencies":false,"run_build_scripts":false,"send_source_to_model":true}` {
		t.Fatalf("stored forms %s %s", on, off)
	}
	// An older strict reader of the first two fields reads the stored form.
	var old struct {
		SendSourceToModel *bool `json:"send_source_to_model"`
		RunBuildScripts   *bool `json:"run_build_scripts"`
	}
	if err := DecodeStrict(on, &old); err != nil || old.SendSourceToModel == nil || old.RunBuildScripts == nil {
		t.Fatalf("older reader: %v", err)
	}
}

func TestAnalysisChoices(t *testing.T) {
	if c := AnalysisChoicesOf(nil, SourceLocal); !c.SendSourceToModel || !c.RunBuildScripts {
		t.Fatalf("absent, local = %+v", c)
	}
	// A repository's build scripts do not run by default.
	if c := AnalysisChoicesOf([]byte("null"), SourceGit); !c.SendSourceToModel || c.RunBuildScripts {
		t.Fatalf("absent, git = %+v", c)
	}
	if c := AnalysisChoicesOf([]byte(`{"send_source_to_model":false,"run_build_scripts":true}`), SourceGit); c.SendSourceToModel || !c.RunBuildScripts {
		t.Fatalf("stored = %+v", c)
	}
	if c := AnalysisChoicesOf([]byte(`{"send_source_to_model":true}`), SourceLocal); c != (AnalysisChoices{}) {
		t.Fatalf("an undecodable object must turn both off, got %+v", c)
	}
	for _, raw := range []string{`{}`, `{"send_source_to_model":true}`, `{"send_source_to_model":true,"run_build_scripts":true,"x":1}`, `{"send_source_to_model":"yes","run_build_scripts":true}`,
		`{"send_source_to_model":true,"run_build_scripts":true,"fetch_dependencies":"no"}`} {
		if _, err := ParseAnalysisChoices([]byte(raw)); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseAnalysisChoices(%s) = %v", raw, err)
		}
	}
}

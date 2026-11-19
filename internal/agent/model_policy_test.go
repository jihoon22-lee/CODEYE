package agent

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

func TestModelSourceGrantIsSeparateFromAcquisition(t *testing.T) {
	approved, readOnly := domain.NewID(), domain.NewID()
	ag := Agent{cfg: Config{SourceRoots: map[string]string{approved: t.TempDir(), readOnly: t.TempDir()}, ModelSourceRoots: map[string]bool{approved: true}}}
	for _, tc := range []struct {
		kind, id string
		allowed  bool
	}{
		{"local", approved, true}, {"local", readOnly, false}, {"git", approved, false},
	} {
		a := activeAttempt{}
		a.job.Source = nodeproto.JobSource{Kind: tc.kind, SourceRootID: tc.id}
		if j := ag.buildJob(&a); j.AllowModelReview != tc.allowed {
			t.Fatalf("%s: unexpected model source grant", tc.kind)
		}
	}
}

// The repository-code grant (docs/ANALYZERS.md#저장소-코드-실행-허가) mirrors the model-source grant:
// it is node configuration per local source root, separate from acquisition
// and from the model grant, and git sources never get it.
func TestRepositoryCodeGrantIsSeparateFromAcquisition(t *testing.T) {
	granted, other := domain.NewID(), domain.NewID()
	ag := Agent{cfg: Config{SourceRoots: map[string]string{granted: t.TempDir(), other: t.TempDir()},
		ModelSourceRoots: map[string]bool{other: true}, RepositoryCodeSourceRoots: map[string]bool{granted: true}}}
	for _, tc := range []struct {
		kind, id     string
		code, models bool
	}{
		{"local", granted, true, false}, {"local", other, false, true}, {"git", granted, false, false},
	} {
		a := activeAttempt{}
		a.job.Source = nodeproto.JobSource{Kind: tc.kind, SourceRootID: tc.id}
		if j := ag.buildJob(&a); j.AllowRepositoryCode != tc.code || j.AllowModelReview != tc.models {
			t.Fatalf("%s: repository code %v, model %v", tc.kind, j.AllowRepositoryCode, j.AllowModelReview)
		}
	}
	// A grant for a root the node does not map is an option error.
	_, err := New(Config{ControllerURL: "http://127.0.0.1:1", StateDir: t.TempDir(), Workers: 1,
		SourceRoots: map[string]string{granted: t.TempDir()}, RepositoryCodeSourceRoots: map[string]bool{other: true}})
	var exit *ExitError
	if !errors.As(err, &exit) || exit.Code != ExitUsage || exit.Msg != "repository-code source root must have a local source mapping" {
		t.Fatalf("unmapped repository-code root: %v", err)
	}
}

// Under --project-analysis-policy the project's own choices decide, for
// local and Git sources alike; the per-root grants play no part.
func TestProjectAnalysisPolicyFollowsTheProject(t *testing.T) {
	root := domain.NewID()
	both := domain.AnalysisChoices{SendSourceToModel: true, RunBuildScripts: true}
	neither := domain.AnalysisChoices{}
	for _, tc := range []struct {
		kind     string
		model    string
		choices  *domain.AnalysisChoices
		reviewed bool
		code     bool
	}{
		{"local", "openai/m", &both, true, true},
		{"git", "openai/m", &both, true, true},
		{"git", "openai/m", &neither, false, false},
		{"local", "openai/m", nil, true, true}, // a controller that sends no choices: the defaults
		{"local", "", &both, true, true},       // no model: the review records model_not_configured
	} {
		ag := Agent{cfg: Config{SourceRoots: map[string]string{root: t.TempDir()}, ModelName: tc.model, ProjectAnalysisPolicy: true,
			GitHosts: []string{"github.com"}}}
		a := activeAttempt{}
		a.job.Source = nodeproto.JobSource{Kind: tc.kind, SourceRootID: root, RepositoryURL: "https://github.com/o/r", Host: "github.com"}
		a.job.Analysis = tc.choices
		if j := ag.buildJob(&a); j.AllowModelReview != tc.reviewed || j.AllowRepositoryCode != tc.code {
			t.Errorf("%s model=%q choices=%v: model %v, code %v", tc.kind, tc.model, tc.choices, j.AllowModelReview, j.AllowRepositoryCode)
		}
	}
	// Without the flag the stored choices change nothing (server mode).
	ag := Agent{cfg: Config{SourceRoots: map[string]string{root: t.TempDir()}, ModelName: "openai/m"}}
	a := activeAttempt{}
	a.job.Source = nodeproto.JobSource{Kind: "local", SourceRootID: root}
	a.job.Analysis = &both
	if j := ag.buildJob(&a); j.AllowModelReview || j.AllowRepositoryCode {
		t.Fatal("server-mode node followed the project's choices")
	}
}

// The service's offer bounds the project's choices and what only the offer
// took away is marked as withheld; a node on the controller's machine
// (ControllerSources) takes a root or Git host the administrator added in
// the web from the job; a Git fetch reads the project owner's own token
// file.
func TestOfferControllerSourcesAndOwnerToken(t *testing.T) {
	both := domain.AnalysisChoices{SendSourceToModel: true, RunBuildScripts: true}
	owner := domain.NewID()
	dir := filepath.Join(t.TempDir(), "github-users")
	ag := Agent{cfg: Config{ModelName: "openai/m", ProjectAnalysisPolicy: true, ControllerSources: true, GitCredentialDir: dir}}
	root := t.TempDir()
	a := activeAttempt{}
	a.job.Source = nodeproto.JobSource{Kind: "local", SourceRootID: domain.NewID(), RootPath: root, RelativePath: "."}
	a.job.Analysis = &both
	a.job.Offered = &nodeproto.AnalysisOffer{ModelReview: false, BuildScripts: true}
	j := ag.buildJob(&a)
	if j.Source.ApprovedRoot != root || j.AllowModelReview || !j.ModelReviewWithheld || !j.AllowRepositoryCode || j.RepositoryCodeWithheld {
		t.Fatalf("offer without model review: %+v", j)
	}
	a.job.Offered = &nodeproto.AnalysisOffer{ModelReview: true}
	if j := ag.buildJob(&a); !j.AllowModelReview || j.ModelReviewWithheld || j.AllowRepositoryCode || !j.RepositoryCodeWithheld {
		t.Fatalf("offer without build scripts: %+v", j)
	}
	// The project did not choose it: not withheld, just off.
	a.job.Analysis = &domain.AnalysisChoices{}
	if j := ag.buildJob(&a); j.AllowRepositoryCode || j.RepositoryCodeWithheld || j.ModelReviewWithheld {
		t.Fatalf("not chosen: %+v", j)
	}
	// Git: the host from the job, the owner's token file.
	a.job.Source = nodeproto.JobSource{Kind: "git", RepositoryURL: "https://ghe.example.com/o/r", Host: "GHE.example.com"}
	a.job.GitCredentialUser = owner
	if j := ag.buildJob(&a); j.Source.AllowedHost != "ghe.example.com" || j.GitCredentialFile != filepath.Join(dir, owner+".json") {
		t.Fatalf("git job %+v", j.Source)
	}
	a.job.GitCredentialUser = "../x"
	if j := ag.buildJob(&a); j.GitCredentialFile != "" {
		t.Fatalf("bad owner id read %q", j.GitCredentialFile)
	}
	// A separately started node takes neither from the job, and keeps its
	// single credential file.
	plain := Agent{cfg: Config{ModelName: "openai/m", ProjectAnalysisPolicy: true, GitCredentialFile: "/srv/github.json"}}
	a.job.GitCredentialUser = owner
	if j := plain.buildJob(&a); j.Source.AllowedHost != "" || j.GitCredentialFile != "/srv/github.json" {
		t.Fatalf("separate node git job %+v %q", j.Source, j.GitCredentialFile)
	}
	a.job.Source = nodeproto.JobSource{Kind: "local", SourceRootID: domain.NewID(), RootPath: root, RelativePath: "."}
	if j := plain.buildJob(&a); j.Source.ApprovedRoot != "" {
		t.Fatalf("separate node took the root path %q", j.Source.ApprovedRoot)
	}
}

// A proxy URL with a credential is a usage error that does not repeat it.
func TestDependencyProxyCredentialRefused(t *testing.T) {
	for _, cfg := range []Config{{GoProxy: "https://user:s3cret@proxy.example"}, {GoSumDB: "sum+k https://u:s3cret@sum.example"}} {
		cfg.ControllerURL, cfg.StateDir, cfg.Workers, cfg.DependencyFetch = "http://127.0.0.1:1", t.TempDir(), 1, true
		_, err := New(cfg)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != ExitUsage || strings.Contains(exit.Msg, "s3cret") {
			t.Fatalf("credential proxy: %v", err)
		}
	}
}

// Fetching dependencies needs the operator's switch and the project's
// choice, on every node; a job without choices keeps the default (on).
func TestDependencyFetchNeedsSwitchAndChoice(t *testing.T) {
	root := domain.NewID()
	on, off := domain.AnalysisChoices{FetchDependencies: true}, domain.AnalysisChoices{SendSourceToModel: true}
	for _, tc := range []struct {
		fetch, policy bool
		choices       *domain.AnalysisChoices
		want          bool
	}{
		{true, true, &on, true}, {true, true, &off, false}, {true, true, nil, true},
		{true, false, &on, true}, {true, false, &off, false}, {true, false, nil, true},
		{false, true, &on, false}, {false, false, nil, false},
	} {
		ag := Agent{cfg: Config{SourceRoots: map[string]string{root: t.TempDir()}, DependencyFetch: tc.fetch, ProjectAnalysisPolicy: tc.policy}}
		for _, kind := range []string{"local", "git"} {
			a := activeAttempt{}
			a.job.Source = nodeproto.JobSource{Kind: kind, SourceRootID: root}
			a.job.Analysis = tc.choices
			if j := ag.buildJob(&a); j.AllowDependencyFetch != tc.want {
				t.Errorf("%s switch=%v policy=%v choices=%v: fetch %v", kind, tc.fetch, tc.policy, tc.choices, j.AllowDependencyFetch)
			}
		}
	}
}

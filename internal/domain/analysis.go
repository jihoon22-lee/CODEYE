package domain

import "encoding/json"

// AnalysisChoices are a project's own analysis choices (the "analysis" object
// of the project configuration). A node started with
// --project-analysis-policy (personal mode) follows them for local and Git
// sources alike; other nodes keep the operator's per-source-root grants.
type AnalysisChoices struct {
	// SendSourceToModel lets the configured review model read the
	// project's source.
	SendSourceToModel bool `json:"send_source_to_model"`
	// RunBuildScripts lets analyzers that execute repository-controlled code
	// (build scripts, cargo check) run (docs/ANALYZERS.md#저장소-코드-실행-허가).
	RunBuildScripts bool `json:"run_build_scripts"`
	// FetchDependencies lets the dependency tasks fetch the third-party
	// dependencies the project's manifests and lockfiles name, so the
	// analyzers that resolve imports can analyze
	// (docs/ARCHITECTURE.md#의존성-준비). It also needs the node operator's
	// switch (--dependency-fetch). Absent means on: older records and
	// clients that do not know the field keep the default.
	FetchDependencies bool `json:"fetch_dependencies"`
	// CommentOnPullRequest is the project's remembered answer to "post the
	// result of a pull-request run as the pull request's CODEYE comment"
	// (docs/GITHUB.md#결과-댓글). Absent means off; it is written only when on,
	// so the choices of every other project keep their bytes.
	CommentOnPullRequest bool `json:"comment_on_pull_request,omitempty"`
}

// UnmarshalJSON decodes a choices object leniently (the node job and older
// controllers): an absent fetch_dependencies is on, absent booleans of the
// other two are off as before.
func (c *AnalysisChoices) UnmarshalJSON(raw []byte) error {
	var in struct {
		SendSourceToModel    bool  `json:"send_source_to_model"`
		RunBuildScripts      bool  `json:"run_build_scripts"`
		FetchDependencies    *bool `json:"fetch_dependencies"`
		CommentOnPullRequest bool  `json:"comment_on_pull_request"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return err
	}
	*c = AnalysisChoices{SendSourceToModel: in.SendSourceToModel, RunBuildScripts: in.RunBuildScripts, FetchDependencies: in.FetchDependencies == nil || *in.FetchDependencies,
		CommentOnPullRequest: in.CommentOnPullRequest}
	return nil
}

// Stored is the form written into the project configuration: the field
// added after the first two is stored only when it differs from its
// default, so a reader that knows only the first two still decodes the
// object.
func (c AnalysisChoices) Stored() map[string]bool {
	m := map[string]bool{"send_source_to_model": c.SendSourceToModel, "run_build_scripts": c.RunBuildScripts}
	if !c.FetchDependencies {
		m["fetch_dependencies"] = false
	}
	if c.CommentOnPullRequest {
		m["comment_on_pull_request"] = true
	}
	return m
}

// DefaultAnalysisChoices apply to a project that recorded none. A local
// folder is the user's own code: everything on. A Git repository may be
// anyone's: its source goes to the model and its dependencies are fetched,
// but its build scripts do not run until the project turns that on.
func DefaultAnalysisChoices(kind SourceKind) AnalysisChoices {
	return AnalysisChoices{SendSourceToModel: true, RunBuildScripts: kind != SourceGit, FetchDependencies: true}
}

// ParseAnalysisChoices strictly decodes an "analysis" object;
// send_source_to_model and run_build_scripts are required,
// fetch_dependencies is optional (absent = on), comment_on_pull_request is
// optional (absent = off).
func ParseAnalysisChoices(raw []byte) (AnalysisChoices, error) {
	var in struct {
		SendSourceToModel    *bool `json:"send_source_to_model"`
		RunBuildScripts      *bool `json:"run_build_scripts"`
		FetchDependencies    *bool `json:"fetch_dependencies"`
		CommentOnPullRequest *bool `json:"comment_on_pull_request"`
	}
	if err := DecodeStrict(raw, &in); err != nil {
		return AnalysisChoices{}, err
	}
	if in.SendSourceToModel == nil || in.RunBuildScripts == nil {
		return AnalysisChoices{}, invalidf("analysis needs send_source_to_model and run_build_scripts")
	}
	return AnalysisChoices{SendSourceToModel: *in.SendSourceToModel, RunBuildScripts: *in.RunBuildScripts, FetchDependencies: in.FetchDependencies == nil || *in.FetchDependencies,
		CommentOnPullRequest: in.CommentOnPullRequest != nil && *in.CommentOnPullRequest}, nil
}

// AnalysisChoicesOf reads the stored "analysis" object of a project
// configuration whose source is of kind; absent (or JSON null) yields the
// defaults of that kind. A stored object that does not decode is treated as
// everything off.
func AnalysisChoicesOf(raw json.RawMessage, kind SourceKind) AnalysisChoices {
	if len(raw) == 0 || string(raw) == "null" {
		return DefaultAnalysisChoices(kind)
	}
	c, err := ParseAnalysisChoices(raw)
	if err != nil {
		return AnalysisChoices{}
	}
	return c
}

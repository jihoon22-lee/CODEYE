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
}

// DefaultAnalysisChoices apply to a project that recorded none. A local
// folder is the user's own code: both on. A Git repository may be anyone's:
// its source goes to the model, but its build scripts do not run until the
// project turns that on.
func DefaultAnalysisChoices(kind SourceKind) AnalysisChoices {
	return AnalysisChoices{SendSourceToModel: true, RunBuildScripts: kind != SourceGit}
}

// ParseAnalysisChoices strictly decodes an "analysis" object; both fields
// are required.
func ParseAnalysisChoices(raw []byte) (AnalysisChoices, error) {
	var in struct {
		SendSourceToModel *bool `json:"send_source_to_model"`
		RunBuildScripts   *bool `json:"run_build_scripts"`
	}
	if err := DecodeStrict(raw, &in); err != nil {
		return AnalysisChoices{}, err
	}
	if in.SendSourceToModel == nil || in.RunBuildScripts == nil {
		return AnalysisChoices{}, invalidf("analysis needs send_source_to_model and run_build_scripts")
	}
	return AnalysisChoices{SendSourceToModel: *in.SendSourceToModel, RunBuildScripts: *in.RunBuildScripts}, nil
}

// AnalysisChoicesOf reads the stored "analysis" object of a project
// configuration whose source is of kind; absent (or JSON null) yields the
// defaults of that kind. A stored object that does not decode is treated as
// both off.
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

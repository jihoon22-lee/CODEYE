package record

import (
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	// MaxSourceBytes bounds snapshot files read by the model-input pipeline
	// and consequently bounds the number of lines in a non-empty file.
	MaxSourceBytes        = 512 << 10
	ScopeReasonWithheld   = "secret_file_withheld"
	ScopeReasonUnreadable = "source_file_unreadable"
	ScopeReasonTooLarge   = "source_file_too_large"
	ScopeReasonEmpty      = "source_file_empty"
	ScopeReasonLineLimit  = "review_line_input_limit"
	ScopeReasonSlotLimit  = "review_slice_limit"
	ScopeReasonBlankRange = "source_blank_range"
)

func validateCoverageScope(d ScopeDoc, want Expect, pullRequest bool) error {
	modules := map[string]ScopeModule{}
	for _, m := range d.Modules {
		if _, exists := modules[m.ID]; exists || !ValidModuleRef(m.ID) || m.FilesEligible < 1 ||
			m.Class != d.Priority.Secondary.ModuleClass(m.ID) || m.FilesSecondary < 0 || m.FilesSecondary > m.FilesEligible ||
			m.FilesNotPlanned < 0 || m.FilesNotPlanned > m.FilesEligible || m.FilesSecondaryNotPlanned < 0 ||
			m.FilesSecondaryNotPlanned > min(m.FilesNotPlanned, m.FilesSecondary) || (m.Class == ClassSecondary && m.FilesSecondary != m.FilesEligible) {
			return invalid("scope module")
		}
		modules[m.ID] = m
	}
	files := map[string]ScopeFile{}
	perModule := map[string]ScopeModule{}
	limits := d.Limits
	check := func(f ScopeFile) error {
		if _, known := modules[f.Module]; !known || domain.ValidateEvidencePath(f.Path) != nil || SourceKind(f.Path) == "" ||
			f.PlannedLines < 0 || f.TotalLines < f.PlannedLines || f.TotalLines > MaxSourceBytes || f.PlannedBytes < 0 ||
			(f.PlannedLines == 0) != (f.PlannedBytes == 0) ||
			(f.PullRequest != "" && (!pullRequest || (f.PullRequest != PullRequestChanged && f.PullRequest != PullRequestNeighbor))) {
			return invalid("scope file attributes")
		}
		if err := checkPriority(f, false, d.Priority.Secondary); err != nil {
			return err
		}
		sha, ok := want.Lookup(f.Path)
		if !ok || !hex64.MatchString(f.SHA256) || sha != f.SHA256 {
			return invalid("scope file not in snapshot")
		}
		if prior, known := files[f.Path]; known {
			if f.State != FilePlanned || (prior.State != FileCoveredByOverview && prior.State != FilePlanned) ||
				prior.Module != f.Module || prior.TotalLines != f.TotalLines || prior.Class != f.Class || prior.Secondary != f.Secondary ||
				prior.Rank != f.Rank || prior.Observations != f.Observations || prior.Severity != f.Severity || prior.PullRequest != f.PullRequest {
				return invalid("inconsistent scope file")
			}
			return nil
		}
		files[f.Path] = f
		m := perModule[f.Module]
		m.FilesEligible++
		if f.Class == ClassSecondary {
			m.FilesSecondary++
		}
		if f.State == FileNotPlanned {
			m.FilesNotPlanned++
			if f.Class == ClassSecondary {
				m.FilesSecondaryNotPlanned++
			}
		}
		perModule[f.Module] = m
		return nil
	}
	if len(d.Overview.Files) > limits.MaxFiles || len(d.Overview.Files) != len(d.Overview.Offered) || !slices.IsSorted(d.Overview.Offered) {
		return invalid("overview files")
	}
	if d.Upstream[0].State != UpstreamAccepted && len(d.Overview.Files) > 0 {
		return invalid("scope overview without accepted upstream")
	}
	for i, f := range d.Overview.Files {
		if f.State != FileCoveredByOverview || f.Path != d.Overview.Offered[i] || f.StartLine != 0 || f.Reason != "" ||
			f.PlannedLines == 0 || f.PlannedBytes > d.Budget.MaxRequestBytes || f.BoundBytes != f.PlannedBytes {
			return invalid("overview file")
		}
		if err := check(f); err != nil {
			return err
		}
	}
	for listIndex, list := range [][]ScopeFile{d.Excluded, d.Unplanned} {
		for _, f := range list {
			if f.StartLine != 0 || f.PlannedLines != 0 || f.BoundBytes != 0 {
				return invalid("excluded scope file window")
			}
			if f.State == FileNotPlanned && f.Reason != ScopeReasonSlotLimit && f.Reason != ScopeReasonLineLimit ||
				f.State == FileWithheld && (f.Reason != ScopeReasonWithheld || f.TotalLines != 0) ||
				f.State == FileUnreadable && (!slices.Contains([]string{ScopeReasonTooLarge, ScopeReasonUnreadable, ScopeReasonEmpty}, f.Reason) || f.TotalLines != 0) ||
				!slices.Contains([]string{FileWithheld, FileUnreadable, FileNotPlanned}, f.State) ||
				(f.State == FileNotPlanned) != (listIndex == 1) || (listIndex == 1 && f.TotalLines == 0) {
				return invalid("excluded scope file reason")
			}
			if err := check(f); err != nil {
				return err
			}
		}
	}
	if len(d.Slices) > limits.Slots {
		return invalid("slice count")
	}
	type lineRange struct{ start, end int }
	windows := map[string][]lineRange{}
	add := func(p string, start, end int) bool {
		if slices.ContainsFunc(windows[p], func(w lineRange) bool { return start <= w.end && w.start <= end }) {
			return false
		}
		windows[p] = append(windows[p], lineRange{start, end})
		return true
	}
	lastTier := 0
	for i, sl := range d.Slices {
		key := sliceKeyOf(i)
		if sl.Key != key || sl.InvestigateTask != "review.investigate."+key || sl.VerifyTask != "review.verify."+key ||
			len(sl.Files) == 0 || len(sl.Files) > limits.MaxFiles || len(sl.Modules) == 0 || len(sl.Modules) > MaxSliceModules ||
			!slices.IsSorted(sl.Modules) || sl.Rank != sl.Files[0].Rank {
			return invalid("slice %d", i+1)
		}
		tier := slices.Index([]string{PullRequestChanged, PullRequestNeighbor, ""}, sl.PullRequest)
		if tier < lastTier {
			return invalid("slice %d pull request order", i+1)
		}
		lastTier = tier
		used := map[string]bool{}
		bytes := 0
		for _, f := range sl.Files {
			if err := check(f); err != nil {
				return err
			}
			if f.State != FilePlanned || f.Reason != "" || f.Class != sl.Class || f.PullRequest != sl.PullRequest ||
				f.StartLine <= 0 || f.PlannedLines <= 0 || f.PlannedLines > limits.MaxFileLines || f.PlannedLines > f.TotalLines-f.StartLine+1 ||
				f.BoundBytes != f.PlannedBytes || f.PlannedBytes > limits.MaxFileBytes || !add(f.Path, f.StartLine, f.StartLine+f.PlannedLines-1) {
				return invalid("slice %d window", i+1)
			}
			used[f.Module] = true
			bytes += f.PlannedBytes
		}
		if bytes > limits.MaxBytes || len(used) != len(sl.Modules) {
			return invalid("slice %d bounds", i+1)
		}
		for j, module := range sl.Modules {
			if !used[module] || (j > 0 && sl.Modules[j-1] == module) {
				return invalid("slice %d modules", i+1)
			}
		}
	}
	for _, o := range d.Omitted {
		f, ok := files[o.Path]
		if !ok || o.StartLine <= 0 || o.StartLine > o.EndLine || o.EndLine > f.TotalLines ||
			!slices.Contains([]string{ScopeReasonLineLimit, ScopeReasonSlotLimit, ScopeReasonBlankRange}, o.Reason) || !add(o.Path, o.StartLine, o.EndLine) {
			return invalid("scope omitted window")
		}
	}
	for p, f := range files {
		accounted := 0
		if f.State == FileCoveredByOverview {
			accounted = f.PlannedLines
		}
		for _, w := range windows[p] {
			accounted += w.end - w.start + 1
		}
		if accounted != f.TotalLines {
			return invalid("scope file lines do not add up")
		}
	}
	for _, m := range d.Modules {
		c := perModule[m.ID]
		if c.FilesEligible != m.FilesEligible || c.FilesSecondary != m.FilesSecondary || c.FilesNotPlanned != m.FilesNotPlanned ||
			c.FilesSecondaryNotPlanned != m.FilesSecondaryNotPlanned {
			return invalid("module %s file count", m.ID)
		}
	}
	return nil
}

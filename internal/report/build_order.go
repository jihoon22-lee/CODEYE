package report

import (
	"sort"
)

func findingKey(f Finding) string {
	if len(f.Locations) == 0 {
		return "\xff" + f.ID
	}
	l := f.Locations[0]
	return l.Path + "\x00" + pad(l.StartLine) + "\x00" + f.ID
}

func sortReport(r *Report) {
	sort.SliceStable(r.Executions, func(a, b int) bool { return r.Executions[a].ID < r.Executions[b].ID })
	sort.SliceStable(r.Observations, func(a, b int) bool { return obsKey(r.Observations[a]) < obsKey(r.Observations[b]) })
	sort.SliceStable(r.Findings, func(a, b int) bool { return findingKey(r.Findings[a]) < findingKey(r.Findings[b]) })
	sort.SliceStable(r.Coverage.Tools, func(a, b int) bool { return r.Coverage.Tools[a].TaskKey < r.Coverage.Tools[b].TaskKey })
}

func obsKey(o Observation) string {
	p, line := "\xff", 0
	if o.Location != nil {
		p, line = o.Location.Path, o.Location.StartLine
	}
	return o.TaskKey + "\x00" + p + "\x00" + pad(line) + "\x00" + o.RuleID + "\x00" + o.ID
}

func sortedArtifacts(in []ArtifactRef) []ArtifactRef {
	out := append([]ArtifactRef(nil), in...)
	order := map[string]int{"source_manifest": 0, "result_manifest": 1, "observations": 2, "stdout": 3, "stderr": 4, "raw_output": 5, "other": 6}
	sort.SliceStable(out, func(a, b int) bool {
		if order[out[a].Role] != order[out[b].Role] {
			return order[out[a].Role] < order[out[b].Role]
		}
		return out[a].ID < out[b].ID
	})
	return out
}

func pad(n int) string {
	s := "000000000000" + itoa(n)
	return s[len(s)-12:]
}

func itoa(n int) string {
	if n < 0 {
		return "-"
	}
	var b [20]byte
	i := len(b)
	for {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
		if n == 0 {
			break
		}
	}
	return string(b[i:])
}

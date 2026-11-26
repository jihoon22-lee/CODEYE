package record

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Source leads and their follow-up (docs/REVIEW.md#단서-후속-조사).
//
// An investigation may name a short list of leads: the open premise of one
// of its candidates whose cause or trigger depends on code the request did
// not carry, each starting from sent code lines (an evidence ID with a
// range) and a symbol, with what it needs (the definition of the symbol
// called there, or the callers of the function defined there). Every lead
// names its candidate, so an unresolved or refused lead never loses a
// suspicion: the candidate goes to verify as it was. The platform resolves every lead
// from the snapshot with the review's source resolvers at the end of the
// investigate task and records the outcome on the lead: the locations it
// found (not evidence of the investigate record, which holds what that
// request sent) or the code of why it found none. One planned follow-up task
// per investigate task (review.leads, review.leads.sNNN) sends the resolved
// leads, the candidates they concern and the resolved excerpts in one
// request and returns the final candidates of that investigation; the
// verify task of the same investigation checks those.
const (
	StageLeads = "leads"
	// SchemaVersionLeads is an investigate record that carries leads and
	// every lead follow-up record: schema 0.7 plus the leads of an
	// investigation and the follow-up's sent excerpts and dispositions.
	SchemaVersionLeads = "0.9"
	// TaskLeads is the follow-up of the whole-run investigation; a slot's
	// is TaskLeads + "." + slot key.
	TaskLeads = "review." + StageLeads
	// LeadsMaxCalls bounds the calls of one follow-up task: one request and
	// one format repair or one retry of a reply that is not JSON.
	LeadsMaxCalls = 2
	// MaxLeads bounds the leads of one investigation's output.
	MaxLeads = 3
	// MaxLeadTargets bounds the resolved locations of one lead.
	MaxLeadTargets = 3
	// MaxLeadWindowLines bounds one resolved location (the caller and test
	// window size of the verify expansion).
	MaxLeadWindowLines = 60

	LeadNeedDefinition = "definition"
	LeadNeedCallers    = "callers"

	LeadResolved   = "resolved"
	LeadUnresolved = "unresolved"

	// A follow-up never removes a candidate: it keeps, refines or contests
	// each one and may add one per lead. A contested candidate goes to
	// verify unchanged with the follow-up's reason and cited lines as
	// counter-evidence; verify decides.
	DispositionKept      = "kept"
	DispositionRefined   = "refined"
	DispositionAdded     = "added"
	DispositionContested = "contested"
)

// Dispositions are the dispositions of a follow-up's candidates.
var Dispositions = []string{DispositionKept, DispositionRefined, DispositionContested, DispositionAdded}

// Why a lead was not followed up (Lead.Reason). Every one is a platform
// decision from the snapshot, the scope and the caps; none is a guess.
const (
	// The location the lead needs lies in an excerpt the investigate
	// request already carried: the model had it (InRequest names it).
	LeadInRequest = "lead_already_in_request"
	// The cited lines hold no call of the symbol (definition) or no
	// definition of it (callers).
	LeadSymbolNotAtAnchor = "lead_symbol_not_at_anchor"
	LeadDefinitionMissing = "lead_definition_not_found"
	LeadCallersMissing    = "lead_callers_not_found"
	// More than one declaration could be meant (build variants, two
	// definitions): the platform does not choose.
	LeadAmbiguous = "lead_ambiguous"
	// The location lies outside the run's scope, outside the files a pull
	// request changes, in a file the secrets screen withholds, in a
	// generated file or on another secondary path.
	LeadOutsideScope  = "lead_outside_scope"
	LeadOutsideChange = "lead_outside_change"
	LeadWithheld      = "lead_secret_withheld"
	LeadGenerated     = "lead_generated_file"
	LeadSecondary     = "lead_secondary_path"
	// The resolution scan reached its file or byte limit first.
	LeadScanLimit = "lead_scan_limit"
	// The follow-up's excerpt or byte cap had no room left for it.
	LeadOverCap = "lead_over_cap"
	// No resolver reads this language.
	LeadLanguage = "lead_language_unsupported"
	// The snapshot could not be read to resolve it.
	LeadReadFailed = "lead_read_failed"
)

// LeadReasons are the codes of an unresolved lead.
var LeadReasons = []string{LeadInRequest, LeadSymbolNotAtAnchor, LeadDefinitionMissing, LeadCallersMissing, LeadAmbiguous,
	LeadOutsideScope, LeadOutsideChange, LeadWithheld, LeadGenerated, LeadSecondary, LeadScanLimit, LeadOverCap, LeadLanguage, LeadReadFailed}

// Task reasons of a follow-up that made no call (domain.LeadsNotRun).
const (
	ReasonLeadsNotOffered  = domain.ReasonLeadsNotOffered
	ReasonNoLeads          = domain.ReasonNoLeads
	ReasonNoLeadsResolved  = domain.ReasonNoLeadsResolved
	ReasonLeadsNotAdmitted = domain.ReasonLeadsNotAdmitted
	ReasonLeadsSlotNotRun  = domain.ReasonLeadsSlotNotRun
)

// Lead is one lead of an investigation with the platform's resolution.
// Need, Symbol, EvidenceID, Suspicion and CandidateID are the model's
// (checked); Resolution, Reason, InRequest and Targets are the platform's.
type Lead struct {
	ID     string `json:"id"`
	Need   string `json:"need"`
	Symbol string `json:"symbol"`
	// EvidenceID is the sent code lines the lead starts from (an evidence
	// entry of the record, usually the narrowed cited range).
	EvidenceID string `json:"evidence_id"`
	Suspicion  string `json:"suspicion"`
	// CandidateID is the candidate of the same record whose premise the
	// lead follows.
	CandidateID string `json:"candidate_id"`
	Resolution  string `json:"resolution"`
	Reason      string `json:"reason,omitempty"`
	// InRequest names the sent excerpts holding what the lead needs
	// (Reason lead_already_in_request).
	InRequest []string `json:"in_request_evidence_ids,omitempty"`
	// Targets are the locations the platform resolved, bound to the
	// snapshot like evidence (ID from path, blob and lines). They are not
	// evidence of this record: its request did not carry them.
	Targets []LeadTarget `json:"targets,omitempty"`
}

// LeadTarget is one resolved location: a definition (origin context), a
// caller window (caller) or a test window (test). Partial marks a
// definition window that is not the whole declaration (it does not fit one
// window, or the request already sent some of its lines): it cannot show
// that a candidate's condition never happens.
type LeadTarget struct {
	ID      string `json:"id"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Start   int    `json:"start_line"`
	End     int    `json:"end_line"`
	Kind    string `json:"kind"`
	Origin  string `json:"origin"`
	Symbol  string `json:"symbol,omitempty"`
	Partial bool   `json:"partial,omitempty"`
}

// Excerpt is the target as an evidence entry (no text).
func (t LeadTarget) Excerpt() Excerpt {
	return Excerpt{ID: t.ID, Path: t.Path, SHA256: t.SHA256, Start: t.Start, End: t.End, Kind: t.Kind, Origin: t.Origin, Symbol: t.Symbol}
}

// LeadFollowup is what one follow-up sent and decided: the leads and
// excerpts its request carried, the resolved excerpts among them (new to
// the investigation), the partial windows among those, the count of their
// lines no excerpt of the investigation holds, and one disposition per
// original candidate in order, then one per added candidate.
type LeadFollowup struct {
	Leads        []string          `json:"leads"`
	Sent         []string          `json:"sent_evidence_ids"`
	New          []string          `json:"new_evidence_ids"`
	Partial      []string          `json:"partial_evidence_ids,omitempty"`
	NewLines     int               `json:"new_lines"`
	Dispositions []LeadDisposition `json:"dispositions"`
}

// LeadDisposition is what became of one candidate: kept as it was, refined
// (the model's corrected text), contested (unchanged, with the model's
// reason and the lines of a whole resolved declaration it cites as
// counter-evidence for verify) or added from a lead.
type LeadDisposition struct {
	CandidateID string   `json:"candidate_id,omitempty"`
	FinalID     string   `json:"final_id,omitempty"`
	Disposition string   `json:"disposition"`
	Lead        string   `json:"lead,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// Original is the text of the candidate a refinement replaced (without
	// its ID, which is CandidateID): the record shows what changed.
	Original *CandidateText `json:"original,omitempty"`
}

var (
	leadID        = regexp.MustCompile(`^lead_[1-9]$`)
	leadsSlotTask = regexp.MustCompile(`^review\.leads(\.(s(00[1-9]|0[1-9][0-9]|[1-9][0-9]{2})))?$`)
)

// ValidLeadID accepts lead_1 through lead_9.
func ValidLeadID(id string) bool { return leadID.MatchString(id) }

// LeadsTask splits a follow-up task key: review.leads (slot "") or
// review.leads.sNNN.
func LeadsTask(key string) (slot string, ok bool) {
	m := leadsSlotTask.FindStringSubmatch(key)
	if m == nil {
		return "", false
	}
	return m[2], true
}

// LeadsTaskKey is the follow-up task of an investigation (slot "" for the
// whole run).
func LeadsTaskKey(slot string) string {
	if slot == "" {
		return TaskLeads
	}
	return TaskLeads + "." + slot
}

// LeadReason reports a code of an unresolved lead.
func LeadReason(code string) bool { return slices.Contains(LeadReasons, code) }

// Resolved reports a lead the platform resolved.
func (l Lead) Resolved() bool { return l.Resolution == LeadResolved }

// checkLeads validates the leads of an investigate record and the
// follow-up of a leads record.
func checkLeads(r Record, catalog map[string]Excerpt, snapshot string, lookup func(string) (string, bool), b bounds) error {
	switch {
	case r.Schema != SchemaVersionLeads:
		if r.Leads != nil || r.LeadFollowup != nil || r.LeadLimit != 0 {
			return invalid("leads outside a 0.9 record")
		}
		return nil
	case r.Stage == StageInvestigate:
		// A 0.9 investigation was offered leads; it may have named none.
		if r.LeadLimit != MaxLeads || len(r.Leads) > r.LeadLimit || r.LeadFollowup != nil || !r.Accepted() {
			return invalid("investigate leads")
		}
	case r.Stage == StageLeads:
		if r.Leads != nil || r.LeadLimit != 0 || r.Accepted() != (r.LeadFollowup != nil) {
			return invalid("lead follow-up content")
		}
	default:
		return invalid("leads stage")
	}
	candidates := map[string]bool{}
	for _, c := range r.Candidates {
		candidates[c.ID] = true
	}
	for i, l := range r.Leads {
		e, ok := catalog[l.EvidenceID]
		switch {
		case l.ID != fmt.Sprintf("lead_%d", i+1):
			return invalid("lead id")
		case l.Need != LeadNeedDefinition && l.Need != LeadNeedCallers:
			return invalid("lead need")
		case !symbol.MatchString(l.Symbol) || Sensitive([]byte(l.Symbol)):
			return invalid("lead symbol")
		case !ok || (e.Kind != "code" && e.Kind != "test"):
			return invalid("lead evidence")
		case !ProseText(l.Suspicion, b.text):
			return invalid("lead suspicion")
		case !candidates[l.CandidateID]:
			return invalid("lead candidate")
		}
		switch l.Resolution {
		case LeadResolved:
			if l.Reason != "" || l.InRequest != nil || len(l.Targets) == 0 || len(l.Targets) > MaxLeadTargets {
				return invalid("resolved lead")
			}
		case LeadUnresolved:
			if !LeadReason(l.Reason) || l.Targets != nil || (l.Reason == LeadInRequest) != (len(l.InRequest) > 0) || len(l.InRequest) > MaxLeadTargets {
				return invalid("unresolved lead")
			}
			for j, id := range l.InRequest {
				if _, ok := catalog[id]; !ok || slices.Contains(l.InRequest[:j], id) {
					return invalid("lead in request")
				}
			}
		default:
			return invalid("lead resolution")
		}
		for j, t := range l.Targets {
			if err := checkLeadTarget(t, snapshot, lookup); err != nil {
				return err
			}
			if slices.ContainsFunc(l.Targets[:j], func(x LeadTarget) bool { return x.ID == t.ID }) {
				return invalid("duplicate lead target")
			}
		}
	}
	if r.LeadFollowup != nil {
		return checkLeadFollowup(r, catalog, b)
	}
	return nil
}

func checkLeadTarget(t LeadTarget, snapshot string, lookup func(string) (string, bool)) error {
	sha, ok := lookup(t.Path)
	switch {
	case domain.ValidateEvidencePath(t.Path) != nil || !ok || sha != t.SHA256 || t.Start < 1 || t.End < t.Start || t.End-t.Start+1 > MaxLeadWindowLines:
		return invalid("lead target location")
	case t.ID != ExcerptID(snapshot, t.Path, t.SHA256, t.Start, t.End):
		return invalid("lead target id")
	case (t.Kind != "code" && t.Kind != "test") || (t.Origin != "context" && t.Origin != "caller" && t.Origin != "test") || (t.Symbol != "" && !symbol.MatchString(t.Symbol)) ||
		(t.Partial && t.Origin != "context"):
		return invalid("lead target attributes")
	}
	return nil
}

// checkLeadFollowup validates a follow-up record on its own: the sent and
// new excerpts are evidence of the record, the new line count is theirs and
// every final candidate has one disposition.
func checkLeadFollowup(r Record, catalog map[string]Excerpt, b bounds) error {
	f := r.LeadFollowup
	if f.Leads == nil || f.Sent == nil || f.New == nil || f.Dispositions == nil || len(f.Leads) == 0 || len(f.Leads) > MaxLeads || len(f.New) == 0 {
		return invalid("lead follow-up lists")
	}
	for i, id := range f.Leads {
		if !ValidLeadID(id) || slices.Contains(f.Leads[:i], id) {
			return invalid("lead follow-up leads")
		}
	}
	for i, id := range f.Sent {
		if _, ok := catalog[id]; !ok || slices.Contains(f.Sent[:i], id) {
			return invalid("lead follow-up sent evidence")
		}
	}
	for i, id := range f.New {
		if !slices.Contains(f.Sent, id) || slices.Contains(f.New[:i], id) {
			return invalid("lead follow-up new evidence")
		}
	}
	for i, id := range f.Partial {
		if !slices.Contains(f.New, id) || slices.Contains(f.Partial[:i], id) {
			return invalid("lead follow-up partial evidence")
		}
	}
	// The new lines are those no excerpt of the investigation holds
	// (LeadFollowupMatches counts them against it).
	if f.NewLines < 0 || f.NewLines > LeadLines(f.New, catalog) {
		return invalid("lead follow-up new lines")
	}
	whole := slices.DeleteFunc(slices.Clone(f.New), func(id string) bool { return slices.Contains(f.Partial, id) })
	final := map[string]Candidate{}
	for _, c := range r.Candidates {
		if c.Origin != StageInvestigate || len(c.ObservationIDs) > 0 {
			return invalid("lead follow-up candidate origin")
		}
		final[c.ID] = c
	}
	seenFinal, seenOriginal := map[string]bool{}, map[string]bool{}
	added := false
	for _, d := range f.Dispositions {
		_, isFinal := final[d.FinalID]
		switch d.Disposition {
		case DispositionKept, DispositionRefined:
			if d.CandidateID == "" || !isFinal || d.Reason != "" || d.EvidenceIDs != nil || added || (d.Lead == "") != (d.Disposition == DispositionKept) ||
				(d.Original == nil) != (d.Disposition == DispositionKept) {
				return invalid("lead disposition")
			}
			if o := d.Original; o != nil && (o.ID != "" || !candidateTextWithin(*o, b) || !RefinementGrounded(final[d.FinalID].Evidence, o.Evidence, whole, catalog)) {
				return invalid("lead refinement")
			}
		case DispositionContested:
			// The counter-evidence cites lines of a whole resolved
			// declaration: a partial window cannot show the condition never
			// happens.
			if d.CandidateID == "" || !isFinal || d.Lead == "" || !ProseText(d.Reason, b.text) || !refs(d.EvidenceIDs, catalog, 6, "code") || added ||
				d.Original != nil || !slices.ContainsFunc(d.EvidenceIDs, func(id string) bool { return leadNewLines(id, whole, catalog) }) {
				return invalid("lead contest")
			}
		case DispositionAdded:
			// An added candidate is found in the new code.
			if d.CandidateID != "" || !isFinal || !slices.Contains(f.Leads, d.Lead) || d.Reason != "" || d.EvidenceIDs != nil || d.Original != nil ||
				!slices.ContainsFunc(final[d.FinalID].Evidence, func(id string) bool { return leadNewLines(id, f.New, catalog) }) {
				return invalid("lead addition")
			}
			added = true
		default:
			return invalid("lead disposition")
		}
		if d.Lead != "" && !slices.Contains(f.Leads, d.Lead) {
			return invalid("lead disposition lead")
		}
		if d.CandidateID != "" {
			if !strings.HasPrefix(d.CandidateID, StageInvestigate+"_") || seenOriginal[d.CandidateID] {
				return invalid("lead disposition candidate")
			}
			seenOriginal[d.CandidateID] = true
		}
		if d.FinalID != "" {
			if seenFinal[d.FinalID] {
				return invalid("lead disposition final id")
			}
			seenFinal[d.FinalID] = true
		}
	}
	if len(seenFinal) != len(final) {
		return invalid("lead follow-up candidates without disposition")
	}
	return nil
}

// RefinementGrounded reports a refinement that stays on the code its
// candidate started from and rests on new code: the refined citations keep
// every citation of the original and one lies in a whole resolved excerpt
// (whole: the new excerpts that are not partial windows).
func RefinementGrounded(refined, original, whole []string, catalog map[string]Excerpt) bool {
	for _, id := range original {
		if !slices.Contains(refined, id) {
			return false
		}
	}
	return slices.ContainsFunc(refined, func(id string) bool { return leadNewLines(id, whole, catalog) })
}

// leadNewLines reports a citation that lies in one of the new excerpts.
func leadNewLines(id string, newIDs []string, catalog map[string]Excerpt) bool {
	e, ok := catalog[id]
	if !ok {
		return false
	}
	return slices.ContainsFunc(newIDs, func(n string) bool {
		x := catalog[n]
		return x.Path == e.Path && x.SHA256 == e.SHA256 && x.Start <= e.Start && e.End <= x.End
	})
}

// LeadNewLineSet is, per file blob (path and SHA-256), the lines the
// listed new excerpts hold that no excerpt of the investigation does: what
// the follow-up let the model read for the first time.
func LeadNewLineSet(ids []string, catalog map[string]Excerpt, investigation []Excerpt) map[string]map[int]bool {
	out := map[string]map[int]bool{}
	for _, id := range ids {
		e, ok := catalog[id]
		if !ok {
			continue
		}
		key := e.Path + "\x00" + e.SHA256
		for line := e.Start; line <= e.End; line++ {
			if slices.ContainsFunc(investigation, func(x Excerpt) bool {
				return x.Path == e.Path && x.SHA256 == e.SHA256 && x.Start <= line && line <= x.End
			}) {
				continue
			}
			if out[key] == nil {
				out[key] = map[int]bool{}
			}
			out[key][line] = true
		}
	}
	return out
}

// LineCount counts the lines of a LeadNewLineSet.
func LineCount(set map[string]map[int]bool) int {
	n := 0
	for _, lines := range set {
		n += len(lines)
	}
	return n
}

// LeadLines counts the distinct lines of the listed excerpts.
func LeadLines(ids []string, catalog map[string]Excerpt) int {
	seen := map[string]map[int]bool{}
	n := 0
	for _, id := range ids {
		e, ok := catalog[id]
		if !ok {
			continue
		}
		key := e.Path + "\x00" + e.SHA256
		if seen[key] == nil {
			seen[key] = map[int]bool{}
		}
		for line := e.Start; line <= e.End; line++ {
			if !seen[key][line] {
				seen[key][line] = true
				n++
			}
		}
	}
	return n
}

// LeadFollowupMatches checks a follow-up record against the investigate
// record it read: every original candidate has one disposition in order, a
// kept or contested one is unchanged, a contested or refined one is a
// candidate of a sent lead, an added one comes from a sent lead, the sent
// leads are resolved leads of the investigation whose anchors travelled,
// the new excerpts are exactly targets of those leads with their partial
// marks, the new line count is theirs less the investigation's lines, and
// the record's evidence is the investigation's entries unchanged, those
// targets and ranges narrowed from what the request sent. The worker checks
// its record with it before writing; the report checks it again.
func LeadFollowupMatches(fu, inv Record) bool {
	f := fu.LeadFollowup
	if f == nil || !fu.Accepted() || !inv.Accepted() || fu.Stage != StageLeads || inv.Stage != StageInvestigate {
		return false
	}
	if (fu.Slice == nil) != (inv.Slice == nil) || (fu.Slice != nil && (fu.Slice.Key != inv.Slice.Key || !slices.Equal(fu.Slice.Modules, inv.Slice.Modules))) {
		return false
	}
	leads := map[string]Lead{}
	for _, l := range inv.Leads {
		leads[l.ID] = l
	}
	targets := map[string]LeadTarget{}
	linked := map[string]bool{}
	sent := map[string]bool{}
	for _, id := range f.Sent {
		sent[id] = true
	}
	for _, id := range f.Leads {
		l, ok := leads[id]
		if !ok || !l.Resolved() || !sent[l.EvidenceID] {
			return false
		}
		for _, t := range l.Targets {
			targets[t.ID] = t
		}
		if l.CandidateID != "" {
			linked[l.CandidateID] = true
		}
	}
	catalog := catalogOf(fu.Evidence)
	var partial []string
	for _, id := range f.New {
		t, ok := targets[id]
		if !ok || catalog[id] != t.Excerpt() {
			return false
		}
		if t.Partial {
			partial = append(partial, id)
		}
	}
	if !slices.Equal(partial, f.Partial) || f.NewLines != LineCount(LeadNewLineSet(f.New, catalog, inv.Evidence)) {
		return false
	}
	if !CarriesUpstreamEvidence(fu, inv) {
		return false
	}
	whole := slices.DeleteFunc(slices.Clone(f.New), func(id string) bool { return slices.Contains(f.Partial, id) })
	investigated := catalogOf(inv.Evidence)
	for _, e := range fu.Evidence {
		t, target := targets[e.ID]
		switch {
		case investigated[e.ID] == e, target && t.Excerpt() == e:
		case e.Origin == OriginCited && slices.ContainsFunc(f.Sent, func(id string) bool {
			x := catalog[id]
			return x.ID != e.ID && x.Path == e.Path && x.SHA256 == e.SHA256 && x.Start <= e.Start && e.End <= x.End
		}):
		default:
			return false
		}
	}
	final := map[string]Candidate{}
	for _, c := range fu.Candidates {
		final[c.ID] = c
	}
	i := 0
	for _, d := range f.Dispositions {
		if d.CandidateID == "" {
			if d.Disposition != DispositionAdded || !slices.Contains(f.Leads, d.Lead) {
				return false
			}
			continue
		}
		if i >= len(inv.Candidates) || inv.Candidates[i].ID != d.CandidateID {
			return false
		}
		original := inv.Candidates[i]
		i++
		if d.Disposition != DispositionKept && (!linked[d.CandidateID] || leads[d.Lead].CandidateID != d.CandidateID) {
			return false
		}
		switch d.Disposition {
		case DispositionKept, DispositionContested:
			c := final[d.FinalID]
			c.ID = original.ID
			if d.Original != nil || !sameCandidate(c, original) {
				return false
			}
		case DispositionRefined:
			// The record keeps the investigation's text, and the refinement
			// keeps the original's citations and cites lines of a whole
			// resolved excerpt.
			text := original.CandidateText
			text.ID = ""
			c, ok := final[d.FinalID]
			if !ok || d.Original == nil || !sameText(*d.Original, text) || c.Origin != original.Origin ||
				!RefinementGrounded(c.Evidence, original.Evidence, whole, catalog) {
				return false
			}
		}
	}
	return i == len(inv.Candidates)
}

func sameText(a, b CandidateText) bool {
	return a.ID == b.ID && a.Title == b.Title && a.Condition == b.Condition && a.Impact == b.Impact && a.Improvement == b.Improvement &&
		a.Counterpoint == b.Counterpoint && a.Mechanism == b.Mechanism && slices.Equal(a.Evidence, b.Evidence)
}

func sameCandidate(a, b Candidate) bool {
	return a.CandidateText.ID == b.CandidateText.ID && a.Title == b.Title && a.Condition == b.Condition && a.Impact == b.Impact &&
		a.Improvement == b.Improvement && a.Counterpoint == b.Counterpoint && a.Mechanism == b.Mechanism && slices.Equal(a.Evidence, b.Evidence) &&
		a.Origin == b.Origin && slices.Equal(a.ObservationIDs, b.ObservationIDs)
}

func catalogOf(list []Excerpt) map[string]Excerpt {
	m := make(map[string]Excerpt, len(list))
	for _, e := range list {
		m[e.ID] = e
	}
	return m
}

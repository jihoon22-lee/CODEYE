package discovery

import (
	"bytes"
	"context"
	"os"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/docpath"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Budget is the explicit document read budget. Sizes come from the verified
// manifest, so budget decisions are made before any byte is read.
type Budget struct {
	TotalBytes       int64
	PerDocumentBytes int64
	MaxDocuments     int
}

// DefaultBudget reads at most 512 KiB of one document and 4 MiB in total locally,
// and inventories at most 1000 documents.
var DefaultBudget = Budget{TotalBytes: 4 << 20, PerDocumentBytes: 512 << 10, MaxDocuments: envdoc.MaxDocuments}

func (b Budget) valid() bool {
	return b.TotalBytes > 0 && b.PerDocumentBytes > 0 && b.PerDocumentBytes <= b.TotalBytes && b.MaxDocuments > 0 && b.MaxDocuments <= envdoc.MaxDocuments
}

// Limitation codes recorded by the document inventory.
const (
	LimDocumentLimit       = "document_limit_exceeded"
	LimDocumentBudget      = "document_budget_exceeded"
	LimDocumentsOutOfScope = "documents_out_of_scope"
	LimConflictsUnassessed = "semantic_conflicts_not_assessed"
)

// Classify returns the document class of a snapshot path, or "" when the
// path is not a document (envdoc.ClassifyDocument).
func Classify(p string) string { return envdoc.ClassifyDocument(p) }

// EligibleDocuments lists, from the manifest alone, the guidance documents
// docs.discover may read in this scope: included regular files of a guidance
// class inside the scope. Sizes, budget and content checks are decided when
// the documents are read, so a run can still select none of them. The
// review planner uses it to decide whether the documents role is required.
func EligibleDocuments(m source.Manifest, scope domain.Scope) []string {
	s, ok := normalScope(scope)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range m.Entries {
		class := Classify(e.Path)
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil &&
			envdoc.IsGuidance(class) && class != envdoc.ClassJavaAPI && pathInScope(e.Path, s) {
			out = append(out, e.Path)
		}
	}
	sort.Strings(out)
	return out
}

// HasJavaAPIDocuments is used only when no ordinary guidance path already
// requires the documents role. It stops at the first verified public comment
// and shares the inventory's per-file and total read limits.
func HasJavaAPIDocuments(ctx context.Context, snapshot source.Snapshot, scope domain.Scope, pr *domain.PullRequestJob) (bool, error) {
	normal, ok := normalScope(scope)
	if !ok {
		return false, nil
	}
	var candidates []source.Entry
	for _, e := range snapshot.Manifest.Entries {
		if docpath.JavaAPIPath(e.Path) && e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil &&
			e.Encoding == "utf-8" && *e.Size <= DefaultBudget.PerDocumentBytes && pathInScope(e.Path, normal) &&
			(pr == nil || slices.Contains(pr.Changed, e.Path)) {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return false, nil
	}
	root, err := os.OpenRoot(snapshot.SnapshotDir)
	if err != nil {
		return false, err
	}
	defer root.Close()
	read := verifiedReader(root)
	slices.SortFunc(candidates, func(a, b source.Entry) int { return strings.Compare(a.Path, b.Path) })
	remaining := DefaultBudget.TotalBytes
	for _, e := range candidates[:min(len(candidates), DefaultBudget.MaxDocuments)] {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if *e.Size > remaining {
			continue
		}
		remaining -= *e.Size
		body, err := read(e)
		if err != nil {
			return false, err
		}
		if utf8.Valid(body) && bytes.IndexByte(body, 0) < 0 && !record.Sensitive(body) && !record.GeneratedHeader(body) &&
			len(javaproject.APIDocuments(e.Path, string(body))) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// readFunc returns the verified bytes of an included file entry; any error is
// a snapshot integrity failure.
type readFunc func(e source.Entry) ([]byte, error)

// Inventory lists documents under the budget. Under selection pressure,
// product guidance competes for the read budget before general documents.
func Inventory(m source.Manifest, scope envdoc.Scope, modules []envdoc.Module, budget Budget, read readFunc) (envdoc.Documents, []string, error) {
	type cand struct {
		e     source.Entry
		class string
	}
	var cands []cand
	var readable []string
	var readBytes int64
	for _, e := range m.Entries {
		if e.Kind == "directory" {
			continue
		}
		if c := Classify(e.Path); c != "" {
			cands = append(cands, cand{e, c})
			if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil &&
				e.Encoding == "utf-8" && *e.Size <= budget.PerDocumentBytes && pathInScope(e.Path, scope) {
				readable = append(readable, e.Path)
				readBytes += *e.Size
			}
		}
	}
	var selection *docpath.Selection
	if len(cands) > budget.MaxDocuments || readBytes > budget.TotalBytes {
		selection = docpath.NewSelection(readable)
	}
	file := func(c cand) (string, string) { return c.e.Path, c.class }
	if selection != nil && selection.HasDesign() {
		docpath.Sort(cands, selection, file)
	} else {
		docpath.Sort(cands, nil, file)
	}
	var lims []string
	if len(cands) > budget.MaxDocuments {
		lims = append(lims, LimDocumentLimit)
	}
	ds := envdoc.Documents{
		Budget: envdoc.Budget{TotalBytes: budget.TotalBytes, PerDocumentBytes: budget.PerDocumentBytes, MaxDocuments: budget.MaxDocuments},
		Items:  []envdoc.Doc{}, Modules: []envdoc.ModuleDocs{}, OverlappingGuidance: []envdoc.Overlap{},
	}
	for i := range min(len(cands), budget.MaxDocuments) {
		c := cands[i]
		e := c.e
		d := envdoc.Doc{Path: e.Path, AppliesTo: docpath.Scope(e.Path), Class: c.class}
		regular := e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" && e.Size != nil
		if regular {
			sha, size := e.SHA256, *e.Size
			d.SHA256, d.SizeBytes = &sha, &size
		}
		set := func(status, reason string) {
			d.Status = status
			if reason != "" {
				d.Reason = &reason
			}
		}
		switch {
		case e.ExcludedReason != "":
			set(envdoc.DocExcluded, e.ExcludedReason)
		case e.Kind == "lfs_pointer":
			set(envdoc.DocExcluded, envdoc.ReasonLFS)
		case !regular:
			set(envdoc.DocExcluded, envdoc.ReasonNotRegular)
		case !pathInScope(e.Path, scope):
			set(envdoc.DocExcluded, envdoc.ReasonOutOfScope)
			lims = append(lims, LimDocumentsOutOfScope)
		case e.Encoding != "utf-8":
			set(envdoc.DocUnreadable, envdoc.ReasonNotUTF8)
		case *e.Size > budget.PerDocumentBytes:
			set(envdoc.DocBudgetExceeded, envdoc.ReasonTooLarge)
			lims = append(lims, LimDocumentBudget)
		case ds.Budget.ReadBytes+*e.Size > budget.TotalBytes:
			set(envdoc.DocBudgetExceeded, envdoc.ReasonBudget)
			lims = append(lims, LimDocumentBudget)
		default:
			b, err := read(e)
			if err != nil {
				return envdoc.Documents{}, nil, err
			}
			ds.Budget.ReadBytes += *e.Size
			switch {
			case !utf8.Valid(b):
				set(envdoc.DocUnreadable, envdoc.ReasonNotUTF8)
				ds.Budget.ReadBytes -= *e.Size // the manifest said UTF-8; not counted as a read document
			case bytes.IndexByte(b, 0) >= 0:
				set(envdoc.DocUnreadable, envdoc.ReasonNUL)
			case record.Sensitive(b):
				set(envdoc.DocExcluded, envdoc.ReasonCredentialLike)
			case d.Class == envdoc.ClassJavaAPI && (record.GeneratedHeader(b) || len(javaproject.APIDocuments(e.Path, string(b))) == 0):
				set(envdoc.DocExcluded, envdoc.ReasonNoJavaAPI)
			default:
				set(envdoc.DocSelected, "")
				n := 0
				if len(b) > 0 {
					n = len(lines(b))
				}
				d.LineCount = &n
				if selection != nil && selection.Primary(e.Path) && selection.WithReadmeLinks(e.Path, string(b)) {
					docpath.Sort(cands[i+1:], selection, file)
				}
			}
		}
		ds.Items = append(ds.Items, d)
	}
	sort.Slice(ds.Items, func(i, j int) bool { return ds.Items[i].Path < ds.Items[j].Path })
	attach(&ds, modules)
	if len(ds.OverlappingGuidance) > 0 {
		lims = append(lims, LimConflictsUnassessed)
	}
	return ds, lims, nil
}

// attach fills module documents and overlap groups from guidance documents.
func attach(ds *envdoc.Documents, modules []envdoc.Module) {
	var guidance []envdoc.Doc
	for _, d := range ds.Items {
		if envdoc.IsGuidanceDocument(d) {
			guidance = append(guidance, d)
		}
	}
	for _, m := range modules {
		md := envdoc.ModuleDocs{Module: m.ID, State: envdoc.StateNone, Own: []string{}, Inherited: []string{}}
		for _, d := range guidance {
			switch {
			case inModuleScope(d.Path, m):
				md.Own = append(md.Own, d.Path)
			case d.AppliesTo != m.Path && envdoc.Under(m.Path, d.AppliesTo):
				md.Inherited = append(md.Inherited, d.Path)
			}
		}
		switch {
		case len(md.Own) > 0:
			md.State = envdoc.StateOwn
		case len(md.Inherited) > 0:
			md.State = envdoc.StateInherited
		}
		ds.Modules = append(ds.Modules, md)
	}
	// The set of applicable guidance only changes at a document directory, so
	// groups are formed there; a module without its own documents shares the
	// group of its nearest documented ancestor (see module documents).
	var dirs []string
	for _, d := range guidance {
		if !slices.Contains(dirs, d.AppliesTo) {
			dirs = append(dirs, d.AppliesTo)
		}
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		var group []string
		for _, d := range guidance {
			if envdoc.Under(dir, d.AppliesTo) {
				group = append(group, d.Path)
			}
		}
		if len(group) >= 2 {
			ds.OverlappingGuidance = append(ds.OverlappingGuidance, envdoc.Overlap{Path: dir, Documents: group, Assessment: envdoc.AssessmentNoModel})
		}
	}
}

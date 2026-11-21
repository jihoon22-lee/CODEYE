package run

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// saveSourceExcerpts runs after the analyzers and review roles, so even a
// partial secret scan can withhold the files it reported. Excerpt availability
// does not change the recorded status of a tool or model task.
func (w *worker) saveSourceExcerpts(a *attempt, tasks []*taskRun) {
	files := map[string]source.Entry{}
	for _, e := range a.snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" {
			files[e.Path] = e
		}
	}
	lookup := func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }
	var model, workflow, diagnostics [][]sourceexcerpt.Key
	modelEnds := map[sourceexcerpt.Key]int{}
	withheld := map[string]bool{}
	var host *taskRun
	for _, task := range tasks {
		if !task.started || a.ctx.Err() != nil {
			continue
		}
		for _, art := range task.result.Artifacts {
			switch art.MediaType {
			case analysis.MediaObservations:
				body := readExcerptInput(art.Path, obsdoc.MaxBytes)
				if len(body) == 0 {
					continue
				}
				keys := diagnosticAnchors(body, task.spec.Key, a.snapshot.Digest, lookup, withheld)
				if len(keys) > 0 {
					diagnostics = append(diagnostics, keys)
				}
			case record.MediaType:
				if task.status != domain.TaskSucceeded {
					continue
				}
				_, slice, _ := record.SliceTask(task.spec.Key)
				body := readExcerptInput(art.Path, record.MaxBytes)
				rec, err := record.Decode(body, record.Expect{RunID: a.job.Attempt.RunID, AttemptID: a.job.Attempt.ID,
					Snapshot: a.snapshot.Digest, Slice: slice, Lookup: lookup})
				if err != nil || !rec.Accepted() {
					continue
				}
				var keys []sourceexcerpt.Key
				for _, e := range rec.CitedEvidence() {
					key := sourceexcerpt.Key{Path: e.Path, SHA256: e.SHA256, Line: e.Start}
					keys = append(keys, key)
					modelEnds[key] = max(modelEnds[key], e.End)
				}
				if len(keys) > 0 {
					model = append(model, keys)
				}
				if task.spec.Key == "review.investigate" && rec.WorkflowReading != nil {
					var related []sourceexcerpt.Key
					for _, e := range rec.WorkflowReadingEvidence() {
						key := sourceexcerpt.Key{Path: e.Path, SHA256: e.SHA256, Line: e.Start}
						related = append(related, key)
						modelEnds[key] = max(modelEnds[key], e.End)
					}
					workflow = append(workflow, related)
				}
			}
		}
		if host == nil && task.status.Terminal() && len(task.manifest.Artifacts) > 0 {
			host = task
		}
	}
	if host == nil || len(model)+len(workflow)+len(diagnostics) == 0 {
		return
	}
	root, err := os.OpenRoot(a.snapshot.SnapshotDir)
	if err != nil {
		return
	}
	defer root.Close()

	doc := sourceexcerpt.Document{SchemaVersion: sourceexcerpt.Version, RunID: a.job.Attempt.RunID,
		AttemptID: a.job.Attempt.ID, Snapshot: a.snapshot.Digest}
	type request struct {
		sourceexcerpt.Key
		model bool
	}
	seen := map[request]bool{}
	windows := map[sourceexcerpt.WindowKey]bool{}
	add := func(groups [][]sourceexcerpt.Key, limit int, model bool) {
		if len(doc.Items) >= limit {
			return
		}
		remaining := max(0, limit-len(doc.Items)) * 2
		for i := 0; i < sourceexcerpt.MaxItems && a.ctx.Err() == nil; i++ {
			active := false
			for _, group := range groups {
				if i >= len(group) {
					continue
				}
				active = true
				key := group[i]
				req := request{key, model}
				if seen[req] || withheld[key.Path] {
					continue
				}
				seen[req] = true
				entry, ok := files[key.Path]
				if !ok || entry.SHA256 != key.SHA256 {
					continue
				}
				remaining--
				var e sourceexcerpt.Excerpt
				if model {
					e, ok = sourceexcerpt.ReadRange(root, entry, key.Line, modelEnds[key])
				} else {
					e, ok = sourceexcerpt.Read(root, entry, key.Line)
				}
				if ok {
					item := sourceexcerpt.Item{Path: key.Path, SHA256: key.SHA256, Anchor: key.Line, Excerpt: e}
					if bounds := item.WindowKey(); !windows[bounds] {
						doc.Items = append(doc.Items, item)
						windows[bounds] = true
					}
				}
				if len(doc.Items) == limit || remaining == 0 {
					return
				}
			}
			if !active {
				return
			}
		}
	}
	add(model, sourceexcerpt.MaxItems/2, true)
	add(workflow, min(sourceexcerpt.MaxItems, len(doc.Items)+3), true)
	add(diagnostics, sourceexcerpt.MaxItems, false)
	add(model, sourceexcerpt.MaxItems, true)
	if len(doc.Items) == 0 || a.ctx.Err() != nil {
		return
	}
	body, err := sourceexcerpt.Encode(doc)
	if err != nil || len(body) > sourceexcerpt.MaxBytes {
		return
	}
	p := filepath.Join(a.scratch, "source-excerpts.json")
	if err := os.WriteFile(p, body, 0600); err != nil {
		return
	}
	id, err := w.upload(a, p, nodeproto.RoleOther, sourceexcerpt.MediaType)
	if err == nil {
		host.manifest.Artifacts = append(host.manifest.Artifacts, analysis.ManifestArtifact{
			ArtifactID: id, Role: nodeproto.RoleOther, MediaType: sourceexcerpt.MediaType,
			SHA256: domain.Digest(body), SizeBytes: int64(len(body)),
		})
	}
}

func diagnosticAnchors(body []byte, task, snapshot string, lookup func(string) (string, bool), withheld map[string]bool) []sourceexcerpt.Key {
	if d, err := obsdoc.Decode(body, obsdoc.Expect{TaskKey: task, Snapshot: snapshot, Lookup: lookup}); err == nil {
		if d.Analyzer.IsSecrets() {
			for _, o := range d.Observations {
				if o.Path != nil {
					withheld[*o.Path] = true
				}
				if o.Location != nil {
					withheld[o.Location.Path] = true
				}
			}
			return nil
		}
		if d.Analyzer.Purpose == obsdoc.PurposeMetrics {
			return nil
		}
		var keys []sourceexcerpt.Key
		for _, o := range d.Observations {
			if l := o.Location; l != nil && !o.Suppressed {
				keys = append(keys, sourceexcerpt.Key{Path: l.Path, SHA256: l.BlobSHA256, Line: l.StartLine})
			}
		}
		return slices.Compact(keys)
	}
	if !strings.HasPrefix(task, "go-vet") {
		return nil
	}
	var d govet.Document
	if json.Unmarshal(body, &d) != nil || d.Kind != govet.DocumentKind || d.TaskKey != task || d.ManifestDigest != snapshot {
		return nil
	}
	var keys []sourceexcerpt.Key
	for _, o := range d.Observations {
		if l := o.Location; l != nil {
			keys = append(keys, sourceexcerpt.Key{Path: l.Path, SHA256: l.BlobSHA256, Line: l.StartLine})
		}
	}
	return slices.Compact(keys)
}

func readExcerptInput(path string, limit int64) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil
	}
	return b
}

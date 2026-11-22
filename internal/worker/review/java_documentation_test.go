package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const javaDocPackage = "src/main/java/example"
const javaDocSource = javaDocPackage + "/Options.java"

const javaDocBody = `package example;
public final class Options {
    private String marker = "/** not a document */ public int choose()";
    /* An ordinary comment is not a public API document. */
    public int ordinary() { return 1; }
    /** A private implementation detail. */
    private int internal() { return 2; }
    /** Returns a default value when the input is absent.
     * @param text the optional value
     * @return a friendly display label, or "missing" for null
     */
    @Deprecated
    public String choose(final String text) {
        if (text == null) {
            return "missing";
        }
        return text;
    }
    /** Normalizes an integer input.
     * @return the integer without scaling
     */
    public int choose(final int value) {
        return value;
    }
    static class Nested {
        /** Excluded because this is not the public top-level API. */
        public String choose() { return "nested"; }
    }
}
`

func TestOnlyJavaAPIDocumentRunsReviewAndKeepsSnapshotLines(t *testing.T) {
	j := treeJob(t, map[string]string{
		javaDocSource:                            javaDocBody,
		"src/test/java/example/OptionsTest.java": strings.ReplaceAll(javaDocBody, "class Options ", "class OptionsTest "),
	})
	script := maxScript(t)
	var selected docInput
	var docID string
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		if len(in.Documents) != 2 {
			t.Fatalf("only the two public overloads should be documented: %+v", in.Documents)
		}
		for _, d := range in.Documents {
			want := strings.Join(strings.Split(javaDocBody, "\n")[d.StartLine-1:d.EndLine], "\n")
			if d.Path != javaDocSource || d.Class != envdoc.ClassJavaAPI || d.AppliesTo != javaDocPackage || d.Text != want ||
				strings.Contains(d.Text, "public ") || strings.Contains(d.Text, "return text") {
				t.Fatalf("document excerpt mixed code and another kind of comment: %+v", d)
			}
			if d.JavaAPI == "example.Options#choose(final String text)" {
				selected = d
			}
		}
		if selected.StartLine != 8 || selected.EndLine != 11 {
			t.Fatalf("metadata does not identify the documented overload: %+v", selected)
		}
		entry := includedFiles(j.Snapshot.Manifest)[javaDocSource]
		docID = record.ExcerptID(j.Snapshot.Digest, javaDocSource, entry.SHA256, selected.StartLine, selected.EndLine)
		return asJSON(docsOutput{
			Contracts: []record.Contract{{Statement: "choose returns a missing label for absent text.",
				AppliesTo: javaDocPackage, EvidenceIDs: []string{selected.EvidenceID}}},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{},
		})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		if !slices.ContainsFunc(in.Documents.Evidence, func(e docEvidence) bool {
			return e.ID == docID && e.Path == javaDocSource && e.StartLine == 8 && e.EndLine == 11
		}) {
			t.Fatalf("investigation did not receive the accepted Javadoc reference: %+v", in.Documents)
		}
		var bodyID string
		for _, e := range in.Scope.Excerpts {
			if e.Path == javaDocSource && e.Kind == "code" && e.Start == 14 && e.End == 17 &&
				strings.Contains(e.Text, "if (text == null)") && !strings.Contains(e.Text, "return value") {
				bodyID = e.ID
			}
		}
		if bodyID == "" {
			t.Fatalf("no separate implementation excerpt for this overload: %+v", in.Scope.Excerpts)
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
			ContractIndex: 1, AppliesTo: javaDocPackage, Status: "match",
			Reason:              "The null condition returns the documented missing label.",
			DocumentEvidenceIDs: []string{docID}, CodeEvidenceIDs: []string{bodyID},
		}}})
	}
	h := Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}}
	plan, results := runRoleWith(t, j, h, "1.27.4")
	for _, key := range []string{"review.documents", "review.investigate"} {
		if results[key].Status != domain.TaskSucceeded {
			t.Fatalf("%s: %+v", key, results[key])
		}
	}
	if !slices.ContainsFunc(plan, func(task analysis.TaskSpec) bool {
		return task.Key == "review.documents" && task.Required
	}) {
		t.Fatal("only-Javadoc project did not require its documents role")
	}
	doc := readRecord(t, j, "documents")
	ref := catalogOf(doc.Evidence)[docID]
	if !ref.IsJavaAPIDocument() || ref.DirectivePath != "" || ref.Text != "" || ref.Start != 8 || ref.End != 11 {
		t.Fatalf("stored Javadoc provenance: %+v", ref)
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 2}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	for _, withCode := range []bool{true, false} {
		raw, err := report.Marshal(report.Project(r, withCode))
		if err != nil {
			t.Fatal(err)
		}
		projected, err := report.Unmarshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(projected.Documents.Comparisons) != 1 || projected.Documents.Comparisons[0].Status != "match" ||
			!slices.ContainsFunc(projected.Documents.Evidence, func(e report.SectionEvidence) bool {
				return e.ID == docID && e.Path == javaDocSource && e.StartLine == 8 && e.EndLine == 11 && e.Kind == "document"
			}) {
			t.Fatalf("report projection lost the validated Javadoc location: %+v", projected.Documents)
		}
		if !withCode && strings.Contains(string(raw), "friendly display label") {
			t.Fatal("the code-free report leaked Javadoc text")
		}
	}
}

func TestJavaAPIDocumentsRespectPathAndPullRequestScope(t *testing.T) {
	for _, c := range []struct {
		name     string
		source   string
		scope    domain.Scope
		pr       *domain.PullRequestJob
		required bool
	}{
		{name: "selected file", scope: domain.Scope{Kind: "paths", Paths: []string{javaDocSource}}, required: true},
		{name: "outside scope", scope: domain.Scope{Kind: "paths", Paths: []string{"src/test"}}, required: false},
		{name: "changed file", pr: &domain.PullRequestJob{Changed: []string{javaDocSource}}, required: true},
		{name: "unchanged file", pr: &domain.PullRequestJob{Changed: []string{"src/main/java/example/Other.java"}}, required: false},
		{name: "ordinary comment only", source: "package example; public class Options {\n/* normal comment */\npublic int poll() {return 1;}\n}\n"},
		{name: "generated source", source: "// Code generated by fixture. DO NOT EDIT.\n" + javaDocBody},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := c.source
			if body == "" {
				body = javaDocBody
			}
			j := treeJob(t, map[string]string{javaDocSource: body})
			j.Scope, j.PullRequest = c.scope, c.pr
			h := Handler{Static: vetStub{}}
			plan, err := h.Plan(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			i := slices.IndexFunc(plan, func(p analysis.TaskSpec) bool { return p.Key == "review.documents" })
			if i < 0 || plan[i].Required != c.required {
				t.Fatalf("incorrect Javadoc scope, plan: %+v", plan)
			}
		})
	}
}

func TestJavaComparisonRequiresTheCitedOverloadImplementation(t *testing.T) {
	job := treeJob(t, map[string]string{javaDocSource: javaDocBody})
	stage := roleStage{ctx: t.Context(), j: job}
	entry := includedFiles(job.Snapshot.Manifest)[javaDocSource]
	doc := record.Excerpt{ID: "doc", Kind: "document", Path: javaDocSource, Start: 8, End: 11, SHA256: entry.SHA256}
	bindings, err := stage.javaAPIBindings([]record.Excerpt{doc})
	if err != nil {
		t.Fatal(err)
	}
	compare := record.ContractComparison{Status: "match", DocumentEvidenceIDs: []string{"doc"}, CodeEvidenceIDs: []string{"cited"}}
	for _, c := range []struct {
		name    string
		excerpt record.Excerpt
		valid   bool
	}{
		{"right overload", record.Excerpt{Kind: "code", Path: javaDocSource, Start: 14, End: 17}, true},
		{"same name different overload", record.Excerpt{Kind: "code", Path: javaDocSource, Start: 23, End: 23}, false},
		{"comment labeled as code", record.Excerpt{Kind: "code", Path: javaDocSource, Start: 8, End: 11}, false},
		{"test reference", record.Excerpt{Kind: "test", Path: javaDocSource, Start: 14, End: 17}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			catalog := map[string]record.Excerpt{"doc": doc, "cited": c.excerpt}
			if got := javaAPIComparisonValid(compare, bindings, catalog); got != c.valid {
				t.Fatalf("validated %v with %+v", got, bindings)
			}
		})
	}
}

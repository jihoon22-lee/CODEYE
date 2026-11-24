package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const javaLocalClassHeader = "public class Options implements Contract"
const javaLocalHelper = "private String normalize(final String value)"
const javaLocalBody = `package example;
public class Options implements Contract {
    /**
     * Returns a normalized label while retaining the caller value.
     * @return normalized label
     */
    public String label(final String label) throws IllegalArgumentException {
        return this.normalize(label);
    }
    private String normalize(final int id) { return "numeric"; }
    private String normalize(final String value) {
        final var copy = new StringBuilder(value);
        copy.append("-normalized");
        return copy.toString();
    }
}
interface Contract { String label(String label); }
`

func TestJavaLocalCallResolutionRespectsReceiverAndOverloads(t *testing.T) {
	for _, test := range []struct {
		name, source, receiver string
		want                   bool
	}{
		{"this qualifier and parameter type", javaLocalBody, "this.normalize(label)", true},
		{"unqualified", strings.ReplaceAll(javaLocalBody, "this.normalize", "normalize"), "normalize(label)", true},
		{"class qualified static", strings.NewReplacer(javaLocalHelper, "private static String normalize(final String value)", "this.normalize", "Options.normalize").Replace(javaLocalBody), "Options.normalize(label)", true},
		{"class qualified instance", strings.ReplaceAll(javaLocalBody, "this.normalize", "Options.normalize"), "", false},
		{"unknown type in inherited class", strings.ReplaceAll(javaLocalBody, "normalize(label)", "normalize(transform(label))"), "", false},
		{"same line has two distinct overload calls", strings.ReplaceAll(javaLocalBody, "this.normalize(label)", "this.normalize(42) + this.normalize(label)"), "", false},
		{"overridable helper", strings.ReplaceAll(javaLocalBody, javaLocalHelper, "protected String normalize(final String value)"), "", false},
		{"varargs overload", strings.ReplaceAll(javaLocalBody, "final int id", "final int... id"), "", false},
		{"class member shadows qualifier", strings.NewReplacer(javaLocalHelper, "private static String normalize(final String value)", "this.normalize", "Options.normalize", javaLocalClassHeader+" {", javaLocalClassHeader+" {\n    Options Options;").Replace(javaLocalBody), "", false},
		{"anonymous receiver", strings.ReplaceAll(javaLocalBody, "return this.normalize(label);", "return new Other() { String nested() { return this.normalize(label); } }.nested();"), "", false},
		{"qualified another object", strings.ReplaceAll(javaLocalBody, "this.normalize", "other.normalize"), "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			lines := strings.Split(test.source, "\n")
			calls := javaLocalCalls(javaDocSource, lines, javaTokens(test.source))
			if !test.want {
				if len(calls) > 0 {
					t.Fatalf("ambiguous call emitted a binding: %+v", calls)
				}
				return
			}
			if len(calls) != 1 || calls[0].symbol != "normalize" {
				t.Fatalf("wanted one bound call: %+v", calls)
			}
			call := calls[0]
			caller := strings.Join(lines[call.caller.start-1:call.caller.end], "\n")
			callee := strings.Join(lines[call.callee.start-1:call.callee.end], "\n")
			if !strings.Contains(caller, test.receiver) || !strings.Contains(callee, "copy.append") || !strings.Contains(callee, "return copy.toString()") ||
				strings.Contains(callee, "numeric") {
				t.Fatalf("bound the wrong Java lines: %q / %q", caller, callee)
			}
		})
	}
}

func TestJavaLocalCallBoundsLongHelperEntryAndReturn(t *testing.T) {
	source := strings.Replace(javaLocalBody, "        return copy.toString();",
		strings.Repeat("        copy.append('x');\n", 28)+"        return copy.toString();", 1)
	lines := strings.Split(source, "\n")
	calls := javaLocalCalls(javaDocSource, lines, javaTokens(source))
	if len(calls) != 2 || calls[0].caller != calls[1].caller || calls[0].calleeExtent != calls[1].calleeExtent ||
		calls[0].callee.end >= calls[1].callee.start {
		t.Fatalf("expected two disjoint bounded sections of one helper: %+v", calls)
	}
	entry, tail := calls[0].callee, calls[1].callee
	if !strings.Contains(strings.Join(lines[entry.start-1:entry.end], "\n"), "new StringBuilder(value)") ||
		!strings.Contains(strings.Join(lines[tail.start-1:tail.end], "\n"), "return copy.toString()") {
		t.Fatalf("the body windows missed the copy or return: %+v", calls)
	}
}

func TestJavaDocumentContractReachesSameClassImplementation(t *testing.T) {
	j := treeJob(t, map[string]string{javaDocSource: javaLocalBody})
	script := maxScript(t)
	script[record.StageDocuments] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		if len(in.Documents) != 1 {
			t.Fatalf("expected one documented method: %+v", in.Documents)
		}
		return asJSON(docsOutput{Contracts: []record.Contract{{
			Statement: "label은 입력을 유지하면서 정규화된 문자열을 반환한다.",
			AppliesTo: javaDocPackage, EvidenceIDs: []string{in.Documents[0].EvidenceID},
		}}, Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
	}
	var entryID, helperID string
	script[record.StageInvestigate] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		if len(in.Scope.DirectCalls) != 1 {
			t.Fatalf("expected one public-to-helper link in the actual investigation: %+v\n%+v", in.Scope.DirectCalls, in.Scope.Excerpts)
		}
		link := in.Scope.DirectCalls[0]
		byID := catalogOf(in.Scope.Excerpts)
		entry, helper := byID[link.CallerEvidenceID], byID[link.CalleeEvidenceID]
		if entry.Path != javaDocSource || helper.Path != javaDocSource || entry.Start != 8 || entry.End != 8 ||
			helper.Start != 11 || helper.End != 15 || !strings.Contains(entry.Text, "return this.normalize(label)") ||
			!strings.Contains(helper.Text, "copy.append") || !strings.Contains(helper.Text, "return copy.toString()") {
			t.Fatalf("incorrect linked excerpts: %+v\n%+v\n%+v", link, entry, helper)
		}
		entryID, helperID = entry.ID, helper.ID
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
			ContractIndex: 1, AppliesTo: javaDocPackage, Status: "match",
			Reason:              "문자열의 복사본을 정규화하여 반환한다.",
			DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs,
			CodeEvidenceIDs:     []string{entry.ID, helper.ID},
		}}})
	}
	_, results := runRoleWith(t, j, Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
	if helperID == "" || results["review.investigate"].Status != domain.TaskSucceeded {
		t.Fatalf("comparison was not accepted: %+v", results["review.investigate"])
	}
	inv := readRecord(t, j, record.StageInvestigate)
	if len(inv.ContractComparisons) != 1 || inv.ContractComparisons[0].Status != "match" || len(inv.ContractComparisons[0].CodeEvidenceIDs) != 2 {
		t.Fatalf("expected two independent Java source locations: %+v", inv.ContractComparisons)
	}

	// Simulate an overview whose input budget only reached the declaration
	// and whose ordinary navigation probe found the unrelated overload.
	// The overview read the whole file as one unit; drop every excerpt
	// holding the helper's lines.
	helper := catalogOf(inv.Evidence)[helperID]
	inv.Evidence = slices.DeleteFunc(inv.Evidence, func(x record.Excerpt) bool {
		return x.ID == helperID || x.Path == helper.Path && x.Start <= helper.Start && helper.End <= x.End
	})
	inv.ContractComparisons[0].Status = "unknown"
	inv.ContractComparisons[0].CodeEvidenceIDs = []string{entryID}
	inv.ContractProbe = &record.ContractProbe{ContractIndex: 1, Candidates: []record.ContractLocation{{
		ID:   record.ContractLocationID(j.Snapshot.Digest, javaDocSource, helper.SHA256, 10, 10),
		Path: javaDocSource, SHA256: helper.SHA256, Start: 10, End: 10, Kind: "code",
	}}}
	inv.Schema = record.SchemaVersionCitedLines
	if _, err := persistRecord(j, inv); err != nil {
		t.Fatal(err)
	}
	script[record.StageContract] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[contractReviewInput](t, q)
		if !slices.ContainsFunc(in.Scope.DirectCalls, func(call directCall) bool {
			return call.CallerEvidenceID == entryID && call.CalleeEvidenceID == helperID
		}) {
			t.Fatalf("follow-up did not bind the newly read helper to the documented method: %+v", in.Scope)
		}
		comparison := in.Prior
		comparison.Status = "match"
		comparison.CodeEvidenceIDs = []string{entryID, helperID}
		return asJSON(map[string]any{"contract_comparisons": []record.ContractComparison{comparison}})
	}
	caller := &scriptedCaller{script: script}
	h := Handler{Static: vetStub{}, Caller: caller}
	if result := h.Run(t.Context(), j, analysis.TaskSpec{Key: record.TaskContract}, nil); result.Status != domain.TaskSucceeded {
		t.Fatalf("contract follow-up %+v, calls %d", result, caller.calls(record.StageContract))
	}
}

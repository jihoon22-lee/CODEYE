package review

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const shipmentJSONContract = "A shipment carries `delivery.carrier_id` and `delivery.route_version` from the selected carrier."

func shipmentJSONSources() map[string]string {
	return map[string]string{
		"go.mod":    "module example.net/shipping\n\ngo 1.24\n",
		"README.md": "# Tracking\n\n" + shipmentJSONContract + "\n",
		"shipping/types.go": "package shipping\n\n" + strings.Repeat("// earlier shapes\n", 55) + "type Shipment struct { Plan Plan `json:\"delivery\"` }\n" +
			"type Plan struct { Carrier string `json:\"carrier_id\"`; Route int `json:\"route_version\"` }\n",
		"shipping/produce.go": "package shipping\n\n" + strings.Repeat("// earlier workflow\n", 55) + "func Prepare() Shipment {\n" +
			"pkg := Shipment{Plan: Plan{Carrier: activeCarrier, Route: routeVersion()}}\nreturn pkg\n}\n",
		"shipping/select.go": "package shipping\n\n" + strings.Repeat("// earlier routing\n", 55) + "const activeCarrier = \"rail\"\n" +
			"func routeVersion() int {\n\treturn 4\n}\n",
		"shipping/guide_test.go": "package shipping\nfunc fixture() Shipment { return Shipment{Plan: Plan{Carrier: \"test-only\", Route: 5}} }\n",
		"archived/shape.go": "package archived\n" + strings.Repeat("// delivery.carrier_id, delivery.route_version\n", 8) +
			"type Shipment struct { Carrier string `json:\"carrier_id\"`; Route int `json:\"route_version\"` }\n" +
			"func Display() Shipment { return Shipment{Carrier: \"old\", Route: 4} }\n",
	}
}

func TestDocumentedGoJSONFieldsUseExactWireNamesFromCitation(t *testing.T) {
	const raw = "| `parcel.rate_delta` | selected `draft` uses `true`, `false`, `null` or `nil` |\nordinary storage"
	for _, tc := range []struct {
		name, statement string
		want            []string
	}{
		{"undecorated with Korean particles", "parcel.rate_delta는 계산 결과이고 draft가 표시된다.", []string{"parcel.rate_delta", "draft"}},
		{"punctuation and code spans", "(`parcel.rate_delta`), draft.", []string{"parcel.rate_delta", "draft"}},
		{"different case", "Parcel.rate_delta and DRAFT", nil},
		{"extended fields", "new.parcel.rate_delta, parcel.rate_delta.more, parcel.rate_delta_next, parcel.rate_delta-debug, draft2, draft.name, draft--debug, context..draft", nil},
		{"partial dotted path", "parcel, rate_delta", nil},
		{"uncited prose and literals", "storage true false null nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := documentedGoJSONFields(nil, tc.statement, raw); !slices.Equal(got, tc.want) {
				t.Fatalf("wire names = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDocumentedJSONFieldsReachBoundGoProducer(t *testing.T) {
	for _, carrier := range []string{"rail", "ferry"} {
		t.Run(carrier, func(t *testing.T) {
			tree := shipmentJSONSources()
			tree["shipping/produce.go"] = strings.Replace(tree["shipping/produce.go"], "func Prepare", "func reset() () { return }\n\nfunc Prepare", 1)
			tree["shipping/select.go"] = strings.ReplaceAll(tree["shipping/select.go"], `"rail"`, fmt.Sprintf("%q", carrier))
			for i := range 12 {
				tree[fmt.Sprintf("shipping/other%02d.go", i)] = "package shipping\n"
			}
			j := treeJob(t, tree)
			statement := shipmentJSONContract
			if carrier == "ferry" {
				statement = "delivery.carrier_id와 delivery.route_version은 배송 정보에 포함된다."
			}
			script := coverageScript(t)
			script["documents"] = func(_ int, request opencode.Request) opencode.Reply {
				in := decodeAs[docsInput](t, request)
				return asJSON(docsOutput{Contracts: []record.Contract{{Statement: statement, AppliesTo: ".", EvidenceIDs: []string{docID(in, "README.md")}}},
					Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
			}
			script["investigate"] = func(_ int, request opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, request)
				if in.Scope.WholeRun {
					for _, wanted := range []struct{ path, text string }{
						{"shipping/produce.go", "pkg := Shipment{Plan: Plan{Carrier: activeCarrier, Route: routeVersion()}}"},
						{"shipping/produce.go", "return pkg"}, {"shipping/types.go", `json:"delivery"`}, {"shipping/types.go", `json:"carrier_id"`},
						{"shipping/types.go", `json:"route_version"`}, {"shipping/select.go", fmt.Sprintf("activeCarrier = %q", carrier)},
						{"shipping/select.go", "return 4"},
					} {
						if !slices.ContainsFunc(in.Scope.Excerpts, func(e record.Excerpt) bool { return e.Path == wanted.path && strings.Contains(e.Text, wanted.text) }) {
							t.Fatalf("missing bound producer evidence %s: %q, got %+v", wanted.path, wanted.text, in.Scope.Excerpts)
						}
					}
					if len(in.Scope.Excerpts) > roleMaxCodeFiles || len(opencode.Prompt(request)) > MinRequestBytes {
						t.Fatal("overview bounded input grew")
					}
				}
				return asJSON(map[string]any{"candidates": []any{}})
			}
			_, results := runRoleWith(t, j, Handler{Budget: smallBudget, Static: secretsStub{}, Caller: &scriptedCaller{script: script}}, "1.27.4")
			if results["review.investigate"].Status != domain.TaskSucceeded {
				t.Fatalf("overview failed: %+v", results["review.investigate"])
			}
		})
	}
}

func TestDocumentedJSONFieldsRequireScopedUnambiguousGoBindings(t *testing.T) {
	for _, name := range []string{"unanchored", "ambiguous", "conditional type", "out of scope", "withheld", "discards result", "different type", "nested overwrite", "custom serializer"} {
		t.Run(name, func(t *testing.T) {
			tree := shipmentJSONSources()
			scope := domain.Scope{Kind: "full"}
			switch name {
			case "unanchored":
				tree["README.md"] = strings.ReplaceAll(tree["README.md"], "delivery.", "preview.")
			case "ambiguous":
				tree["archived/shape.go"] = strings.ReplaceAll(tree["shipping/types.go"], "package shipping", "package archived")
			case "conditional type":
				tree["shipping/types.go"] = "//go:build alternate\n\n" + tree["shipping/types.go"]
			case "out of scope":
				scope = domain.Scope{Kind: "paths", Paths: []string{"README.md", "shipping/produce.go", "shipping/select.go"}}
			case "discards result":
				tree["shipping/produce.go"] = strings.ReplaceAll(tree["shipping/produce.go"], "return pkg", "_ = pkg\nreturn Shipment{}")
			case "different type":
				tree["shipping/produce.go"] = strings.ReplaceAll(tree["shipping/produce.go"], "func Prepare() Shipment", "type Parcel Shipment\nfunc Prepare() Parcel")
			case "nested overwrite":
				tree["shipping/produce.go"] = strings.ReplaceAll(tree["shipping/produce.go"], "return pkg", "if unknown() { pkg.Plan = Plan{} }; return pkg")
			case "custom serializer":
				tree["shipping/types.go"] += "func (Plan) MarshalJSON() ([]byte, error) { return nil, nil }\n"
			}
			j := treeJob(t, tree)
			j.Scope = scope
			s := roleStage{j: j, ctx: t.Context()}
			if name == "withheld" {
				s.screen.withheld = map[string]bool{"shipping/types.go": true}
			}
			doc, _, ok, err := s.excerpt(s.entries()["README.md"], "document", "", 1, roleMaxDocLines, roleMaxDocBytes)
			if err != nil || !ok {
				t.Fatalf("document unavailable: %v", err)
			}
			groups := planCodeGroupsFor(j.Snapshot.Manifest, j.Scope, s.h.secondary(), changedPaths(j))
			selected, err := s.contractWitnesses(groups, record.Record{Evidence: []record.Excerpt{doc}, Contracts: []record.Contract{{
				Statement: shipmentJSONContract, AppliesTo: ".", EvidenceIDs: []string{doc.ID},
			}}})
			if err != nil || len(selected.goJSONFields) != 0 {
				t.Fatalf("unsafe preferred Go JSON evidence: %+v, %v", selected.goJSONFields, err)
			}
		})
	}
}

func TestDocumentedJSONFieldsSelectBothExplicitGoWrites(t *testing.T) {
	const statement = "The native response has `display.include_labels` set, and the compact response clears it."
	tree := map[string]string{
		"README.md": "# Gallery\n\n" + statement + "\n",
		"gallery/types.go": "package gallery\ntype Snapshot struct{ Display Display `json:\"display\"` }\n" +
			"type Display struct{ Labels bool `json:\"include_labels\"` }\n",
		"gallery/full.go":    "package gallery\nfunc Full() Snapshot { return Snapshot{Display: Display{Labels: true}} }\n",
		"gallery/compact.go": "package gallery\nfunc Compact(snapshot Snapshot) Snapshot {\n out := snapshot\n out.Display = Display{Labels: false}\n return out\n }\n",
		"gallery/outside.go": "package gallery\nfunc Other() int { return 99 }\n",
	}
	j := treeJob(t, tree)
	j.Scope = domain.Scope{Kind: "paths", Paths: []string{"README.md", "gallery/types.go", "gallery/full.go", "gallery/compact.go"}}
	s := roleStage{j: j, ctx: t.Context()}
	doc, _, ok, err := s.excerpt(s.entries()["README.md"], "document", "", 1, roleMaxDocLines, roleMaxDocBytes)
	if err != nil || !ok {
		t.Fatalf("document unavailable: %v", err)
	}
	groups := planCodeGroupsFor(j.Snapshot.Manifest, j.Scope, s.h.secondary(), changedPaths(j))
	selected, err := s.contractWitnesses(groups, record.Record{Evidence: []record.Excerpt{doc}, Contracts: []record.Contract{{
		Statement: statement, AppliesTo: ".", EvidenceIDs: []string{doc.ID},
	}}})
	if err != nil || len(selected.goJSONFields) != 1 {
		t.Fatalf("selected writes unavailable: %+v, %v", selected.goJSONFields, err)
	}
	core := selected.goJSONFields[0].core
	for _, expected := range []string{"Display{Labels: false}", "Display{Labels: true}", `json:"include_labels"`} {
		if !slices.ContainsFunc(core, func(x record.Excerpt) bool { return strings.Contains(x.Text, expected) }) {
			t.Fatalf("missing %q in %+v", expected, core)
		}
	}
	if slices.ContainsFunc(core, func(x record.Excerpt) bool { return x.Path == "gallery/outside.go" }) {
		t.Fatal("out-of-scope sibling was included")
	}
}

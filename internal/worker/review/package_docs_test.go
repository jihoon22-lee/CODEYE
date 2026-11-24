package review

import (
	"slices"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

const packageGuide = "site/docs/usage.md"
const examplePackage = "packages/price"
const packageRuntime = examplePackage + "/src/runtime.ts"
const packageManifestFile = examplePackage + "/package.json"
const examplePackageName = "@sample/price"
const namedPackageExample = "# Price display\n\n`displayPrice` renders cents with two decimal places.\n\n" +
	"```ts\nimport { formatPrice as displayPrice } from '@sample/price';\nconst result = displayPrice(1250);\n```\n"

func TestDocumentedPackageRuntimeReachesReport(t *testing.T) {
	const runtime = "export function formatPrice(cents: number) {\n  return (cents / 100).toFixed(2);\n}\n"
	j := treeJob(t, map[string]string{
		packageGuide:        namedPackageExample,
		packageManifestFile: `{"name":"@sample/price","exports":{".":{"types":"./src/runtime.d.ts","import":"./src/runtime.ts"}}}`,
		packageRuntime:      strings.Repeat("// Display helpers.\n", 70) + runtime,
		"site/package.json": `{"name":"@sample/documentation","main":"index.js"}`,
		"site/index.js":     "export function prepareSite() { return 'site' }\n",
	})
	script := maxScript(t)
	var cited docInput
	script["documents"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[docsInput](t, q)
		for _, d := range in.Documents {
			if d.Path == packageGuide {
				cited = d
			}
		}
		p := cited.PackageReference
		if cited.AppliesTo != "site" || cited.Text != strings.TrimSuffix(namedPackageExample, "\n") ||
			p == nil || p.Name != examplePackageName || p.Scope != examplePackage || p.Callable != "displayPrice" || p.Entry != packageRuntime {
			t.Fatalf("wrong document window or package reference: %+v (%+v)", cited, p)
		}
		statement := "`displayPrice`는 센트 입력을 소수점 두 자리 문자열로 반환한다."
		if in.Repair == nil {
			statement = "`@sample/price` 사용 페이지의 머리글을 화면에 표시한다."
		} else if !slices.Contains(in.Repair.OutputErrors, "scope_generalized") {
			t.Fatalf("an unrelated adjacent claim acquired the package scope: %+v", in.Repair)
		}
		return asJSON(docsOutput{Contracts: []record.Contract{{Statement: statement, AppliesTo: examplePackage, EvidenceIDs: []string{cited.EvidenceID}}},
			Conflicts: []record.Conflict{}, Questions: []record.Question{}, InstructionNotes: []record.InstructionNote{}})
	}
	script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
		in := decodeAs[investigateInput](t, q)
		if !in.Scope.WholeRun {
			return asJSON(map[string]any{"candidates": []any{}})
		}
		if !slices.ContainsFunc(in.Documents.Evidence, func(e docEvidence) bool {
			return e.Path == packageGuide && e.AppliesTo == "site" && e.PackageReference != nil && e.PackageReference.Scope == examplePackage
		}) || len(in.Documents.Contracts) != 1 {
			t.Fatalf("accepted contract missing from investigation: %+v", in.Documents)
		}
		var implID string
		for _, e := range in.Scope.Excerpts {
			if e.Path == packageRuntime && e.Kind == "code" && e.Start > 50 && strings.Contains(e.Text, "(cents / 100).toFixed(2)") {
				implID = e.ID
			}
		}
		if implID == "" {
			t.Fatalf("package implementation after the file prefix missing from the actual request: %+v", in.Scope.Excerpts)
		}
		return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{ContractIndex: 1,
			AppliesTo: examplePackage, Status: "match", Reason: "센트 입력을 100으로 나누고 소수점 두 자리로 변환한다.",
			DocumentEvidenceIDs: in.Documents.Contracts[0].EvidenceIDs, CodeEvidenceIDs: []string{implID}}}})
	}
	h := Handler{Static: vetStub{}, Caller: &scriptedCaller{script: script}}
	plan, results := runRoleWith(t, j, h, "1.27.4")
	for _, key := range []string{"review.documents", "review.investigate"} {
		if results[key].Status != domain.TaskSucceeded {
			t.Fatalf("%s: %+v", key, results[key])
		}
	}
	doc := readRecord(t, j, record.StageDocuments)
	byPath := includedFiles(j.Snapshot.Manifest)
	id := record.ExcerptID(j.Snapshot.Digest, packageGuide, byPath[packageGuide].SHA256, cited.StartLine, cited.EndLine)
	e := catalogOf(doc.Evidence)[id]
	if e.Path != packageGuide || e.Text != "" || e.PackageReference.ManifestSHA256 != byPath[packageManifestFile].SHA256 ||
		e.PackageReference.EntrySHA256 != byPath[packageRuntime].SHA256 || e.PackageReference.Export != "formatPrice" {
		t.Fatalf("raw record has incorrect citation or provenance: %+v", e)
	}
	if slices.ContainsFunc(doc.Evidence, func(e record.Excerpt) bool { return e.Path == packageRuntime || e.Path == packageManifestFile }) {
		t.Fatal("package resolution counted metadata inspection as a source excerpt")
	}
	for _, mutate := range []func(*record.Record){
		func(r *record.Record) { r.Evidence[0].PackageReference.EntrySHA256 = strings.Repeat("f", 64) },
		func(r *record.Record) { r.Contracts[0].AppliesTo = "." },
		func(r *record.Record) { r.Contracts[0].AppliesTo = "packages/price/src" },
		func(r *record.Record) { r.Contracts[0].Statement = "색상을 조정한다." },
		func(r *record.Record) {
			r.Questions = []record.Question{{Question: "가격을 표시하는가?", AppliesTo: examplePackage, EvidenceIDs: []string{id}}}
		},
	} {
		bad := doc
		bad.Evidence, bad.Contracts = slices.Clone(doc.Evidence), slices.Clone(doc.Contracts)
		mutate(&bad)
		if _, err := record.Marshal(bad, expectRecord(j, record.StageDocuments)); err == nil {
			t.Fatalf("accepted invalid package document: %+v", bad)
		}
	}
	r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 2}), report.DefaultRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if r.Coverage.LLMExposedFiles == nil || *r.Coverage.LLMExposedFiles != 3 {
		t.Fatalf("expected the guide and two JS/TS source files to be counted, got %+v", r.Coverage)
	}
	for _, includeCode := range []bool{true, false} {
		raw, err := report.Marshal(report.Project(r, includeCode))
		if err != nil {
			t.Fatal(err)
		}
		read, err := report.Unmarshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(read.Documents.Comparisons) != 1 || read.Documents.Comparisons[0].Status != "match" ||
			read.Documents.Comparisons[0].AppliesTo != examplePackage || !slices.ContainsFunc(read.Documents.Evidence, func(ev report.SectionEvidence) bool {
			return ev.ID == id && ev.Path == packageGuide && ev.PackageReference.EntryPath == packageRuntime &&
				(ev.PackageReference.Name != "") == includeCode && (ev.PackageReference.Callable != "") == includeCode &&
				(ev.PackageReference.Export != "") == includeCode
		}) {
			t.Fatalf("published comparison lost its original citation or scoped runtime: %+v", read.Documents)
		}
		if !includeCode && strings.Contains(string(raw), "displayPrice") {
			t.Fatal("code-free report contains the callable")
		}
	}
}

func TestPackageExampleUseRequiresTheSameBoundedPassage(t *testing.T) {
	for _, tc := range []struct {
		name, markdown, callable string
		start, end               int
	}{
		{name: "named import", markdown: namedPackageExample, callable: "displayPrice"},
		{name: "default import", markdown: "```js\nimport price from '@sample/price'\nprice(20)\n```", callable: "price"},
		{name: "namespace import", markdown: "```js\nimport * as price from '@sample/price'\nprice.format(20)\n```", callable: "price.format"},
		{name: "require alias", markdown: "```js\nconst {format: price} = require('@sample/price');\nprice(20);\n```", callable: "price"},
		{name: "inline call", markdown: "Use `import {price} from '@sample/price'` and call `price(20)`.", callable: "price"},
		{name: "separate callable example", markdown: "```js\nimport {price} from '@sample/price'\n```\n\nCall `price(20)`.", callable: "price"},
		{name: "inline noncall", markdown: "Use `import {price} from '@sample/price'` and see price(20)."},
		{name: "unrelated heading", markdown: "```js\nimport {price} from '@sample/price'\n```\n## Unrelated\nCall `price(20)`."},
		{name: "separate block", markdown: "```js\nimport {price} from '@sample/price'\n```\n```js\nprice(20)\n```"},
		{name: "block outside excerpt", markdown: namedPackageExample, start: 1, end: 6},
		{name: "opening outside excerpt", markdown: namedPackageExample, start: 6, end: 8},
		{name: "commented call", markdown: "```js\nimport price from '@sample/price'\n// price(20)\n```"},
		{name: "quoted call", markdown: "```js\nimport price from '@sample/price'\nconst message = 'price(20)'\n```"},
		{name: "shadowed call", markdown: "```js\nimport price from '@sample/price'\nfunction demo(price) { price(20) }\n```"},
		{name: "property unrelated to binding", markdown: "```js\nimport price from '@sample/price'\nother.price(20)\n```"},
		{name: "non-executable fence", markdown: "```text\nimport price from '@sample/price'\nprice(20)\n```"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split(tc.markdown, "\n")
			if tc.start == 0 {
				tc.start, tc.end = 1, len(lines)
			}
			uses := documentedPackageUses(lines, tc.start, tc.end)
			if tc.callable == "" && len(uses) != 0 || tc.callable != "" &&
				(len(uses) != 1 || uses[0].callable != tc.callable || uses[0].name != examplePackageName) {
				t.Fatalf("found %+v, wanted %q", uses, tc.callable)
			}
		})
	}
}

func TestDocumentedPackageRejectsUnverifiedRuntimeEntry(t *testing.T) {
	for _, tc := range []struct {
		name, manifest string
		extra          map[string]string
		scope          domain.Scope
		withhold       string
	}{
		{name: "external package", manifest: `{"name":"@sample/other","main":"src/runtime.ts"}`},
		{name: "ambiguous name", extra: map[string]string{"packages/other/package.json": `{"name":"@sample/price"}`}},
		{name: "withheld duplicate name", extra: map[string]string{"packages/other/package.json": `{"name":"@sample/price"}`}, withhold: "packages/other/package.json"},
		{name: "ambiguous local package use", extra: map[string]string{
			packageGuide:                "```js\nimport price from '@sample/price'\nimport fee from '@sample/fee'\nprice(20)\nfee(20)\n```\n",
			"packages/fee/package.json": `{"name":"@sample/fee","main":"index.js"}`,
			"packages/fee/index.js":     "export default function fee() { return 1; }\n",
		}},
		{name: "missing entry", manifest: `{"name":"@sample/price","main":"src/absent.ts"}`},
		{name: "ambiguous conditions", manifest: `{"name":"@sample/price","exports":{"import":"./src/runtime.ts","default":"./src/other.ts"}}`,
			extra: map[string]string{examplePackage + "/src/other.ts": "export function formatPrice() { return 1; }\n"}},
		{name: "generated header", extra: map[string]string{packageRuntime: "// Code generated. DO NOT EDIT.\nexport const formatPrice = () => 1;\n"}},
		{name: "build output", manifest: `{"name":"@sample/price","main":"dist/runtime.js"}`,
			extra: map[string]string{examplePackage + "/dist/runtime.js": "export function formatPrice() { return 1; }\n"}},
		{name: "nested package entry", extra: map[string]string{examplePackage + "/src/package.json": `{"name":"@sample/nested"}`}},
		{name: "entry outside scope", scope: domain.Scope{Kind: "paths", Paths: []string{"site", packageManifestFile}}},
		{name: "manifest withheld", withhold: packageManifestFile},
		{name: "secret file withheld", withhold: packageRuntime},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.manifest == "" {
				tc.manifest = `{"name":"@sample/price","main":"src/runtime.ts"}`
			}
			files := map[string]string{packageGuide: namedPackageExample, packageManifestFile: tc.manifest,
				packageRuntime: "export function formatPrice() { return 1; }\n"}
			for p, body := range tc.extra {
				files[p] = body
			}
			j := treeJob(t, files)
			if tc.scope.Kind != "" {
				j.Scope = tc.scope
			}
			s := roleStage{j: j, ctx: t.Context()}
			if tc.withhold != "" {
				s.screen.withheld = map[string]bool{tc.withhold: true}
			}
			g := s.entries()[packageGuide]
			x, _, ok, err := s.excerpt(g, "document", "", 1, roleMaxDocLines, roleMaxDocBytes)
			excerpts := []record.Excerpt{x}
			if !ok || err != nil {
				t.Fatalf("missing document excerpt: %v", err)
			}
			resolver := documentPackageResolver{stage: &s, files: s.entries()}
			lines := strings.Split(strings.TrimSuffix(files[packageGuide], "\n"), "\n")
			if err := resolver.attach(excerpts, map[string][]string{packageGuide: lines}); err != nil || !excerpts[0].PackageReference.IsZero() {
				t.Fatalf("unverified runtime acquired scope: %+v, err %v", excerpts[0].PackageReference, err)
			}
		})
	}
}

func TestPackageExampleKeepsCrossingFence(t *testing.T) {
	const intro = "# Using the price formatter\n"
	const sample = "```ts\nimport price from '@sample/price'\nimport printer from 'external-printer'\n\n" +
		"const cents = 20\nconst result = price(cents)\nprinter(result)\n```"
	guide := intro + strings.Repeat("Read a value from the catalog.\n", 34) + sample + "\n"
	in, stored, inventory := collectDocumentSelection(t, map[string]string{
		"README.md":           "Read the [price guide](site/docs/usage.md) to print the display price.\n",
		packageGuide:          guide,
		"site/docs/AGENTS.md": "# Local example\n" + sample + "\n",
		"site/package.json":   `{"name":"@sample/website","scripts":{"preview":"preview --password='placeholder-fixture-password'"}}`,
		packageManifestFile:   `{"name":"@sample/price","exports":{"import":{"types":"./src/runtime.d.ts","default":"./src/runtime.ts"}}}`,
		packageRuntime:        "export default function formatPrice(cents: number) { return String(cents); }\n",
	})
	var prefix, example docInput
	for _, d := range in.Documents {
		if d.Path == packageGuide {
			if d.StartLine == 1 {
				prefix = d
			}
			if d.PackageReference != nil {
				example = d
			}
		}
		if d.Path == "site/docs/AGENTS.md" && d.PackageReference != nil {
			t.Fatal("agent instructions gained a package reference")
		}
	}
	readme := slices.IndexFunc(in.Documents, func(d docInput) bool { return d.Path == "README.md" })
	packageExample := slices.IndexFunc(in.Documents, func(d docInput) bool { return d.PackageReference != nil })
	if readme < 0 || packageExample <= readme || prefix.EndLine != 35 || prefix.AppliesTo != "site" || prefix.PackageReference != nil ||
		example.StartLine != 36 || example.EndLine != 43 || example.AppliesTo != "site" || example.Text != sample ||
		example.PackageReference == nil || example.PackageReference.Name != examplePackageName || example.PackageReference.Callable != "price" {
		t.Fatalf("prefix and verified example were not fitted independently: %+v", in.Documents)
	}
	if !slices.ContainsFunc(stored.Evidence, func(e record.Excerpt) bool {
		return e.Path == example.Path && e.Start == example.StartLine && e.End == example.EndLine && !e.PackageReference.IsZero()
	}) || !slices.ContainsFunc(inventory.Items, func(d envdoc.Doc) bool {
		return d.Path == packageGuide && d.AppliesTo == "site"
	}) {
		t.Fatalf("published evidence or physical inventory changed: %+v, %+v", stored.Evidence, inventory.Items)
	}
}

func TestDocumentedPackageRespectsPullRequestTiersAndScope(t *testing.T) {
	const changed = "site/home.js"
	const runtime = "export function formatPrice(cents) {\n  return (cents / 100).toFixed(2);\n}\n"
	j := treeJob(t, map[string]string{
		packageGuide: namedPackageExample, changed: "export function home() { return 'home' }\n",
		packageManifestFile: `{"name":"@sample/price","main":"src/runtime.ts"}`,
		packageRuntime:      strings.Repeat("// File prefix.\n", 55) + runtime,
	})
	j.PullRequest = &domain.PullRequestJob{Number: 7, BaseSHA: strings.Repeat("1", 40), HeadSHA: strings.Repeat("2", 40), Changed: []string{packageGuide, changed}}
	s := roleStage{j: j, ctx: t.Context()}
	manifest, files := j.Snapshot.Digest, includedFiles(j.Snapshot.Manifest)
	document := files[packageGuide]
	lines := strings.Split(strings.TrimSuffix(namedPackageExample, "\n"), "\n")
	n := len(lines)
	selected := []envdoc.Doc{{Path: packageGuide, Class: envdoc.ClassDesign, AppliesTo: "site", SHA256: &document.SHA256, LineCount: &n}}
	evidence, err := s.documentExcerpts(selected)
	if err != nil || len(evidence.excerpts) != 1 || evidence.excerpts[0].PackageReference.IsZero() {
		t.Fatalf("full PR has no approved package context: %+v, %v", evidence, err)
	}
	id := record.ExcerptID(manifest, packageGuide, document.SHA256, 1, n)
	docs := record.Record{Evidence: evidence.excerpts, Contracts: []record.Contract{{Statement: "`displayPrice`는 센트 입력을 변환한다.",
		AppliesTo: examplePackage, EvidenceIDs: []string{id}}}}
	// The unchanged implementation may follow the changed source as an
	// ordinary unit (the whole file), never as a promoted package window.
	code, _, _, err := s.codeCorpus(docs)
	if err != nil || len(code) == 0 || code[0].Path != changed || slices.ContainsFunc(code, func(e record.Excerpt) bool {
		return e.Path == packageRuntime && e.Start > 1
	}) {
		t.Fatalf("unchanged implementation was promoted over changed source: %v, %+v", err, code)
	}
	s.j.Scope = domain.Scope{Kind: "paths", Paths: []string{packageGuide, changed}}
	evidence, err = s.documentExcerpts(selected)
	if err != nil || len(evidence.excerpts) != 1 || !evidence.excerpts[0].PackageReference.IsZero() {
		t.Fatalf("changed-only PR exposed a sibling implementation through docs: %+v, %v", evidence, err)
	}
	code, _, _, err = s.codeCorpus(docs)
	if err != nil || len(code) != 1 || code[0].Path != changed {
		t.Fatalf("changed-only PR added unauthorized code to the corpus: %v, %+v", err, code)
	}
}

func TestPackageDocumentScopeRepairKeepsOnlyValidatedRows(t *testing.T) {
	const other = "site/docs/configuration.md"
	guide := "# Usage\n" + strings.Repeat("The formatter receives a cent amount.\n", 34) +
		"```ts\nimport {formatPrice} from '@sample/price';\nconst cents = 1250;\n\n// Present one amount.\nconst shown = formatPrice(cents);\n```\n"
	for _, mode := range []string{"scope errors only", "fabricated evidence", "no usable rows"} {
		t.Run(mode, func(t *testing.T) {
			j := treeJob(t, map[string]string{
				packageGuide:        guide,
				other:               "# Configuration\n\nThe site has a footer.\n",
				packageManifestFile: `{"name":"@sample/price","exports":"./src/runtime.ts"}`,
				packageRuntime:      "export function formatPrice(cents: number) { return (cents / 100).toFixed(2); }\n",
			})
			script := maxScript(t)
			script["documents"] = func(n int, q opencode.Request) opencode.Reply {
				in := decodeAs[docsInput](t, q)
				var prefix, bound docInput
				for _, doc := range in.Documents {
					if doc.Path == packageGuide {
						if doc.PackageReference == nil {
							prefix = doc
						} else {
							bound = doc
						}
					}
				}
				adjacent := []string{prefix.EvidenceID, bound.EvidenceID}
				split := []string{bound.EvidenceID, docID(in, other)}
				if prefix.EndLine+1 != bound.StartLine || bound.PackageReference == nil || split[1] == "" {
					t.Fatalf("wrong input windows: %+v", in.Documents)
				}
				if n == 2 {
					if in.Repair == nil || !slices.Contains(in.Repair.OutputErrors, "scope_generalized") || len(in.Repair.DocumentScopes) != 2 {
						t.Fatalf("missing scope diagnostics: %+v", in.Repair)
					}
					x, y := in.Repair.DocumentScopes[0], in.Repair.DocumentScopes[1]
					if x.Collection != "contracts" || x.Index != 2 || !slices.Equal(x.EvidenceIDs, adjacent) ||
						!slices.Equal(x.ValidScopes, []documentScopeOption{{Scope: "site"}, {Scope: examplePackage, Callable: "formatPrice"}}) ||
						y.Collection != "questions" || y.Index != 2 || !slices.Equal(y.EvidenceIDs, split) ||
						!slices.Equal(y.ValidScopes, []documentScopeOption{{Scope: "site"}}) || len(opencode.Prompt(q)) > PromptBudget {
						t.Fatalf("incorrect supported scopes or request size: %+v", in.Repair.DocumentScopes)
					}
				}
				out := docsOutput{
					Contracts: []record.Contract{
						{Statement: "`formatPrice`는 센트를 소수점 두 자리 문자열로 반환한다.", AppliesTo: examplePackage, EvidenceIDs: adjacent},
						{Statement: "`formatPrice`는 센트를 인자로 받는다.", AppliesTo: ".", EvidenceIDs: adjacent},
					},
					Questions: []record.Question{
						{Question: "`formatPrice`에 0을 전달하면 무엇을 반환하는가?", AppliesTo: examplePackage, EvidenceIDs: adjacent},
						{Question: "`formatPrice`에서 사이트 바닥글 설정을 읽는가?", AppliesTo: examplePackage, EvidenceIDs: split},
					},
					Conflicts: []record.Conflict{}, InstructionNotes: []record.InstructionNote{},
				}
				if n == 2 && mode == "fabricated evidence" {
					out.Contracts[0].EvidenceIDs = []string{"D99"}
				}
				if n == 2 && mode == "no usable rows" {
					out.Contracts = []record.Contract{}
					out.Questions = out.Questions[1:]
				}
				return asJSON(out)
			}
			script["investigate"] = func(_ int, q opencode.Request) opencode.Reply {
				in := decodeAs[investigateInput](t, q)
				if mode != "scope errors only" || !in.Scope.WholeRun {
					return asJSON(map[string]any{"candidates": []any{}})
				}
				if len(in.Documents.Contracts) != 2 || len(in.Documents.Questions) != 1 {
					t.Fatalf("investigation received invalid or incomplete scoped rows: %+v", in.Documents)
				}
				c := in.Documents.Contracts[0]
				if c.AppliesTo != examplePackage || len(c.EvidenceIDs) != 2 {
					t.Fatalf("investigation received invalid or incomplete scoped rows: %+v", in.Documents)
				}
				return asJSON(map[string]any{"candidates": []any{}, "contract_comparisons": []record.ContractComparison{{
					ContractIndex: 1, AppliesTo: examplePackage, Status: "match", Reason: "센트 값을 100으로 나누고 소수점 두 자리로 반환한다.",
					DocumentEvidenceIDs: c.EvidenceIDs, CodeEvidenceIDs: []string{excerptID(in.Scope.Excerpts, packageRuntime)},
				}}})
			}
			caller := &scriptedCaller{script: script}
			h := Handler{Static: vetStub{}, Caller: caller}
			plan, results := runRoleWith(t, j, h, "1.27.4")
			doc := readRecord(t, j, record.StageDocuments)
			if len(doc.Calls) != 2 || !slices.Equal(doc.Calls[0].OutputErrors, []string{"scope_generalized"}) {
				t.Fatalf("scope repair did not use the existing single retry: %+v", doc.Calls)
			}
			if mode != "scope errors only" {
				if doc.Accepted() || doc.FailureReason != reasonOutputInvalid || len(doc.Contracts)+len(doc.Questions) != 0 {
					t.Fatalf("unusable repair escaped quarantine: %+v", doc)
				}
				return
			}
			if !doc.Accepted() || len(doc.Contracts) != 2 || len(doc.Questions) != 1 || doc.Contracts[1].AppliesTo != examplePackage ||
				!slices.Contains(doc.Limitations, record.LimDocumentScopeItemsOmitted) || !slices.Contains(doc.Limitations, record.LimDocumentPackageNarrowed) {
				t.Fatalf("validated repair rows or scope limitation missing: %+v", doc)
			}
			r, err := report.Build(exampleInput(t, j, h, plan, results, &exampleIDs{n: 2}), report.DefaultRegistry())
			if err != nil {
				t.Fatal(err)
			}
			for _, includeCode := range []bool{true, false} {
				projected := report.Project(r, includeCode)
				body, err := report.Marshal(projected)
				if err != nil {
					t.Fatal(err)
				}
				read, err := report.Unmarshal(body)
				if err != nil {
					t.Fatal(err)
				}
				if len(read.Documents.Comparisons) != 2 || read.Documents.Comparisons[0].Status != "match" ||
					len(read.Documents.Questions) != 1 || !slices.ContainsFunc(read.Gaps, func(g report.Gap) bool {
					return g.Code == record.LimDocumentScopeItemsOmitted && g.Section == report.SectionDocuments
				}) {
					t.Fatalf("report lost the retained scoped evidence or omitted-row warning: %+v", read.Documents)
				}
			}
		})
	}
}

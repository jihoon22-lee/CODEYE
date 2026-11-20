package review

import (
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

func TestResolveSelectedNavigationReferenceInComparisonReason(t *testing.T) {
	const pending = "처리 분기는 발췌에 없다. L7에서 구현 여부와 경계 조건을 확인할 수 있다."
	const resolved = "처리 분기는 발췌에 없다. settings.go:357–372에서 구현 여부와 경계 조건을 확인할 수 있다."
	snapshot := strings.Repeat("a", 64)
	location := record.ContractLocation{Path: "settings.go", SHA256: strings.Repeat("b", 64), Kind: "code", Start: 357, End: 372}
	location.ID = record.ContractLocationID(snapshot, location.Path, location.SHA256, location.Start, location.End)
	for _, test := range []struct {
		name, reason, want string
		edit               func(*record.ContractComparison, *record.ContractProbe, *investigateInput)
	}{
		{"selected location", pending, resolved, nil},
		{"not sent", strings.Replace(pending, "L7", "L70", 1), "", nil},
		{"offered but unselected", strings.Replace(pending, "L7", "L8", 1), "", nil},
		{"layer name", "L7 계층에서 요청의 동작을 확인할 수 있다.", "", nil},
		{"quoted symbol", strings.Replace(pending, "L7", "`L7`", 1), "", nil},
		{"ordinary token sentence", "L7은 이 옵션이 쓰는 코드 이름이다.", "", nil},
		{"literal in contract", pending, "", func(_ *record.ContractComparison, _ *record.ContractProbe, in *investigateInput) {
			in.Documents.Contracts[0].Statement = "L7 계층의 경계 조건을 검사한다."
		}},
		{"literal in actual code", pending, "", func(_ *record.ContractComparison, _ *record.ContractProbe, in *investigateInput) {
			in.Scope.Excerpts = []record.Excerpt{{Path: "settings.go", Text: "const L7 = 7;"}}
		}},
		{"another comparison", pending, "", func(c *record.ContractComparison, _ *record.ContractProbe, _ *investigateInput) {
			c.ContractIndex = 2
		}},
		{"already compared", pending, "", func(c *record.ContractComparison, _ *record.ContractProbe, _ *investigateInput) {
			c.Status = "partial"
		}},
		{"no valid probe", pending, "", func(_ *record.ContractComparison, p *record.ContractProbe, _ *investigateInput) {
			p.Candidates = nil
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := record.ContractProbe{ContractIndex: 1, Candidates: []record.ContractLocation{location}}
			c := record.ContractComparison{ContractIndex: 1, Status: "unknown", AppliesTo: ".", Reason: test.reason}
			in := investigateInput{Scope: investigateScope{Snapshot: snapshot},
				Documents: documentsHandoff{Contracts: []record.Contract{{Statement: "옵션의 입력값을 읽는다."}}}}
			offered := make([]contractNavigationCandidate, 8)
			offered[6].location = location
			offered[7].location = record.ContractLocation{ID: "unselected", Path: "other.go"}
			if test.edit != nil {
				test.edit(&c, &p, &in)
			}
			resolveContractProbeReason(&c, p, offered, in)
			want := test.want
			if want == "" {
				want = test.reason
			}
			if c.Reason != want {
				t.Fatalf("reason %q, want %q", c.Reason, want)
			}
		})
	}
}

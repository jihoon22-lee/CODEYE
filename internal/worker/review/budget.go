package review

import (
	"github.com/jihoon22-lee/CODEYE/internal/worker/opencode"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Budget is the node operator's size of one review request (codeye agent
// --review-request-bytes, --review-output-tokens; docs/REVIEW.md#요청-크기).
// Every role derives its source, unit, verification and prose limits from
// it with one rule (limitsOf), so a small budget gives smaller requests of
// the same shape. The plan never depends on it: slot keys stay a function
// of the manifest, the scope and constants (slice_plan.go).
type Budget struct {
	// RequestBytes bounds the full message text of one request
	// (opencode.Prompt); 0 means DefaultRequestBytes.
	RequestBytes int
	// OutputTokens is the output the model may write per call; it bounds
	// the prose and the candidate count the request asks for. 0 means
	// DefaultOutputTokens.
	OutputTokens int
}

const (
	// DefaultRequestBytes lets one request carry the whole files of a
	// module of usual size together.
	DefaultRequestBytes = opencode.DefaultRequestBytes
	// MinRequestBytes is the smallest accepted budget: the fixed part of a
	// request (instructions, contract, documents and tool facts) plus a
	// small code corpus.
	MinRequestBytes = opencode.MinRequestBytes
	// MaxRequestBytes is the largest accepted budget, the adapter's input
	// limit.
	MaxRequestBytes = opencode.MaxRequestBytes
	// DefaultOutputTokens leaves room for several candidates of a short
	// paragraph per field.
	DefaultOutputTokens = opencode.DefaultOutputTokens
	MinOutputTokens     = opencode.MinOutputTokens
	MaxOutputTokens     = opencode.MaxOutputTokens
)

// ValidRequestBytes reports whether n is an accepted request budget (0 is
// the default).
func ValidRequestBytes(n int) bool { return opencode.ValidRequestBytes(n) }

// ValidOutputTokens reports whether n is an accepted output budget (0 is
// the default).
func ValidOutputTokens(n int) bool { return opencode.ValidOutputTokens(n) }

// fixedRequestBytes is the part of a request the code corpus never takes:
// the instructions, contract, rules, documents, environment notes and tool
// facts, which have their own constant caps.
const fixedRequestBytes = 40000

// limits are the bounds one request budget derives.
type limits struct {
	request int // message text of one request
	reserve int // room kept for one repair request
	// source bounds the numbered code and test text of one investigate
	// request, unit one excerpt (a whole file or a declaration-bounded part)
	// and excerpts their count.
	source, unit, excerpts int
	// verifyExcerpts bounds the excerpts of a verification request;
	// expansion and expansionBytes the caller and test windows the verifier
	// adds (expand.go; each window keeps its fixed size).
	verifyExcerpts            int
	expansion, expansionBytes int
	// text bounds one model prose field (JSON bytes), mechanism a
	// candidate's mechanism, listText one verification list item; chars is
	// the character count the rules state for text.
	text, mechanism, listText, chars int
	// candidates bounds the candidates one investigation may return; more
	// sent source allows more (candidateLimit).
	candidates int
}

// limitsOf derives every bound from b. At MinRequestBytes and
// MinOutputTokens the bounds are those of the earlier fixed request
// (12000 source bytes, 400-byte prose, four candidates).
func (b Budget) limits() limits {
	req := b.RequestBytes
	if req == 0 {
		req = DefaultRequestBytes
	}
	out := b.OutputTokens
	if out == 0 {
		out = DefaultOutputTokens
	}
	req = min(max(req, MinRequestBytes), MaxRequestBytes)
	out = min(max(out, MinOutputTokens), MaxOutputTokens)
	l := limits{request: req, reserve: max(3072, req/32)}
	l.source = max(req*3/10, req-fixedRequestBytes-l.reserve)
	l.unit = max(roleMaxCodeFile, l.source/5)
	l.excerpts = min(max(l.source/1500, roleMaxCodeFiles), 64)
	l.verifyExcerpts = min(max(l.source/300, 40), 128)
	scale := max(1, l.source/roleMaxCodeBytes)
	l.expansion = min(maxExpansion*scale, 24)
	l.expansionBytes = maxExpansionBytes * scale
	// A short paragraph per field: 1200 bytes (360 Korean characters) once
	// the output allows 16384 tokens, 400 bytes (120 characters) at the
	// smallest output.
	l.text = min(max(out*record.MaxProseText/DefaultOutputTokens, 400), record.MaxProseText)
	l.mechanism = min(2*l.text, record.MaxMechanismText)
	l.listText = max(600, l.text)
	l.chars = l.text * 3 / 10
	// About 1536 output tokens per candidate with every field written out,
	// after 4096 for the strengths, comparisons and the envelope.
	l.candidates = min(max((out-4096)/1536, 4), 12)
	return l
}

// candidateLimit scales the candidate count with the code sent: four for
// up to 32 KiB of source, one more per further 32 KiB, at most
// l.candidates.
func (l limits) candidateLimit(sourceBytes int) int {
	return min(4+max(0, sourceBytes-1)/(32<<10), l.candidates)
}

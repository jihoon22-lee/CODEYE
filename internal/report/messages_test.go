package report

import (
	"strings"
	"testing"
)

// Every attention sentence renders with the count the overview stores and
// never leaves a format error (%!d, %!(EXTRA …)) in the text, whether or not
// its template uses the count.
func TestAttentionMessagesTakeTheirArguments(t *testing.T) {
	n := 0
	for key, tmpl := range Korean.Labels {
		code, ok := strings.CutPrefix(key, "attention.")
		if !ok {
			continue
		}
		n++
		got := attentionText(Korean, code, 7)
		if strings.Contains(got, "%!") {
			t.Errorf("%s renders a format error: %q", key, got)
		}
		if strings.Contains(tmpl, "%d") && !strings.Contains(got, "7") {
			t.Errorf("%s drops its count: %q", key, got)
		}
	}
	if n == 0 {
		t.Fatal("no attention messages")
	}
}

// The rendered example reports and a report without model review (every
// overview line, the attention item that has no count included) carry no
// format error in either export.
func TestRenderedReportsHaveNoFormatErrors(t *testing.T) {
	reports := map[string]Report{}
	for _, p := range []string{"../../examples/report.json", "../../examples/report-slices.json"} {
		reports[p] = storedExample(t, p)
	}
	f := newFixture(t, goVet(t))
	addDiscovery(t, f, nil, nil)
	reports["without model review"] = build(t, f.in, mustRegistry(t))
	for name, r := range reports {
		md := string(RenderMarkdown(r, strings.Repeat("0", 64), Korean))
		html, err := RenderHTML(r, strings.Repeat("0", 64), Korean)
		if err != nil {
			t.Fatal(err)
		}
		for format, out := range map[string]string{"markdown": md, "html": string(html)} {
			if i := strings.Index(out, "%!"); i >= 0 {
				t.Errorf("%s %s has a format error near %q", name, format, out[max(0, i-80):min(len(out), i+40)])
			}
		}
	}
}

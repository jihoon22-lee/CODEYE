package prcomment

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// Text from a finding cannot notify anyone or link anything: character
// references (&#64;, &commat;, &#35;) stay text because "&" is escaped
// first, and plain mentions, issue references, GH- references and URLs
// are broken.
func TestRenderedTextHasNoLiveMentionOrReference(t *testing.T) {
	r, err := report.Unmarshal(prReport(t, headA))
	if err != nil {
		t.Fatal(err)
	}
	r.Findings[0].Title = "printf 오류 &#64;someone &commat;someone @someone #12 &#35;13 GH-14 https://github.com/acme/widget/issues/15 www.example.com"
	r.Findings[0].Impact = "출력이 깨진다 &#x40;other &#X23;16"
	body := Render(Input{ProjectID: project, RunID: runA, Version: 100, Pin: pin(headA, true), Report: &r})
	t.Logf("%s", body)
	// The text after the two hidden marker lines is what GitHub renders.
	shown := body[strings.Index(body, "###"):]
	for _, live := range []*regexp.Regexp{
		regexp.MustCompile(`@[A-Za-z0-9]`),        // a mention
		regexp.MustCompile(`#[0-9]`),              // an issue reference
		regexp.MustCompile(`(?i)\bGH-[0-9]`),      // a GH- reference
		regexp.MustCompile(`&#|&commat;`),         // a character reference GitHub would decode
		regexp.MustCompile(`(?i)https?://|www\.`), // an autolink
	} {
		if m := live.FindString(shown); m != "" {
			t.Errorf("live %q in the rendered comment", m)
		}
	}
	for _, want := range []string{"&amp;#\u206064;someone", "&amp;commat;someone", "@⁠someone", "#⁠12", "&amp;#\u206035;13", "&amp;#x40;other"} {
		if !strings.Contains(shown, want) {
			t.Errorf("rendered text lacks %q", want)
		}
	}
}

// Nothing in the changes: the comment says so (not "결함 없음") and gives the
// counts of the existing code alone, without a "그 밖에" for listed items.
func TestRenderedCommentWithNothingInTheChanges(t *testing.T) {
	r, err := report.Unmarshal(prReport(t, headA))
	if err != nil {
		t.Fatal(err)
	}
	s := r.PullRequest
	s.Findings.ChangedFileOtherLines += s.Findings.ChangedLines + s.Findings.ChangedFile
	s.Observations.Elsewhere += s.Observations.ChangedLines + s.Observations.ChangedFile
	s.Findings.ChangedLines, s.Findings.ChangedFile, s.Observations.ChangedLines, s.Observations.ChangedFile = 0, 0, 0, 0
	s.Items = []report.PullRequestItem{}
	body := Render(Input{ProjectID: project, RunID: runA, Version: 100, Pin: pin(headA, true), Report: &r})
	if !strings.Contains(body, msg("none_in_changes")) || strings.Contains(body, "그 밖에 변경 부분") || strings.Contains(body, "#### "+msg("findings")) {
		t.Fatalf("comment:\n%s", body)
	}
	if want := "변경 밖(기존 코드)에는 발견 "; !strings.Contains(body, want) {
		t.Fatalf("comment lacks %q:\n%s", want, body)
	}
}

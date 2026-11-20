package github

import "testing"

func TestWebhookMentionIgnoresQuotesAndCode(t *testing.T) {
	for _, body := range []string{
		"> @codeye", "> quoted\n@codeye", "    @codeye", "\t@codeye", "`@codeye`", "`long\n@codeye\nexample`",
		"```go\n@codeye\n```", "~~~~\n@codeye\n~~~\n@codeye", "@codeye-other", "\\@codeye",
		"<pre>\n\n@codeye\n</pre>", "<!-- hidden\n\n@codeye\n-->",
	} {
		if containsCommand(body, "@codeye") {
			t.Errorf("treated quoted or unrelated text as a command: %q", body)
		}
	}
	for _, body := range []string{"@CodeYe", "Please review.\n\n @CoDeYe\r\n", "> quoted\n\n@codeye", "```text\nexample\n```\n\n@codeye"} {
		if !containsCommand(body, "@codeye") {
			t.Errorf("missed command: %q", body)
		}
	}
}

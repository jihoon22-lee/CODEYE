package report

import (
	"strings"
	"testing"
)

func TestRenderGoTestFailureDeclaration(t *testing.T) {
	const (
		test  = "TestRetainsFailureDeclaration"
		other = "TestOtherFailureDeclaration"
		file  = "internal/billing/adjustment_test.go"
	)
	r := exampleReport(t)
	r.Project = &ProjectInfo{Name: "Billing", Source: "https://github.com/example/billing"}
	r.Inputs.Profile, r.Inputs.SourceKind = "with-tests", "git"
	r.Inputs.CommitSHA = strPtr(strings.Repeat("9", 40))
	r.Export.IncludeCode = true
	r.Builder.Version = BuilderVersion
	r.Coverage.TestStatus = TestFailed
	r.Tasks = append(r.Tasks,
		Task{Key: "test.go", Status: "failed", Reason: strPtr("test_command_failed")},
		Task{Key: "test.go.m02", Status: "succeeded"},
		Task{Key: "test.python", Status: "failed", Reason: strPtr("test_command_failed")},
	)
	zero, one, two := 0, 1, 2
	r.Executions = append(r.Executions,
		Execution{TaskKey: "test.go", Name: "test.run", Status: "exited", Cleanup: "confirmed", ExitCode: &one, Cwd: ".",
			GoTestFailures: []GoTestFailure{{Name: test, Path: file, Line: 42}, {Name: other, Path: file, Line: 12}}},
		Execution{TaskKey: "test.go.m02", Name: "test.run", Status: "exited", Cleanup: "confirmed", ExitCode: &zero, Cwd: "modules/fees"},
		Execution{TaskKey: "test.python", Name: "test.run", Status: "exited", Cleanup: "confirmed", ExitCode: &two, Cwd: "."},
	)
	wantURL := "https://github.com/example/billing/blob/" + *r.Inputs.CommitSHA + "/" + file + "#L42-L42"
	for _, detail := range []string{DetailSummary, DetailFull} {
		for _, includeCode := range []bool{true, false} {
			projected := Project(r, includeCode)
			h, err := RenderHTMLAt(projected, "digest", Korean, detail)
			if err != nil {
				t.Fatal(err)
			}
			_, html, hasTable := strings.Cut(string(h), `class="report-test-result-table"`)
			html, _, _ = strings.Cut(html, "</table>")
			if !hasTable || !strings.Contains(html, `data-label="테스트 선언 위치"`) {
				t.Fatal("HTML did not render a responsive test results table")
			}
			md := string(RenderMarkdownAt(projected, "digest", Korean, detail))
			_, markdown, hasTable := strings.Cut(md, "| 테스트 | 테스트 선언 위치 | 상태 |")
			markdown, _, _ = strings.Cut(markdown, "</details>")
			if !hasTable {
				t.Fatal("Markdown did not render the test results table")
			}
			for _, body := range []string{html, markdown} {
				for _, label := range []string{"테스트 실패", "개별 테스트 미식별", "명령 실패", "명령 종료 코드 1", "명령 종료 코드 2", "명령 종료 코드 0", "통과", "modules/fees"} {
					if !strings.Contains(body, label) {
						t.Errorf("level %s, includes code %t: no %q in %s", detail, includeCode, label, body)
					}
				}
				if strings.Contains(body, test) != includeCode || strings.Contains(body, other) != includeCode || strings.Contains(body, wantURL) != includeCode || strings.Contains(body, "이름 생략") == includeCode {
					t.Errorf("level %s, includes code %t: declaration name or link visibility is inconsistent: %s", detail, includeCode, body)
				}
				if strings.Count(body, "명령 종료 코드 1") != 1 || strings.Count(body, "테스트 실패") != 2 {
					t.Errorf("the exit code should occur once across both failed tests: %s", body)
				}
				if strings.Contains(body, "(test.go)") {
					t.Error("test task identifier appeared as a source location")
				}
			}
			if !strings.Contains(markdown, file+":42") || !strings.Contains(html, `adjustment_test.go<span class="report-test-line">:42</span>`) ||
				!strings.Contains(html, `adjustment_test.go<span class="report-test-line">:12</span>`) {
				t.Error("the verified declaration was missing")
			}
		}
	}
}

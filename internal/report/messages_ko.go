package report

import "strings"

// Locale holds user-facing strings of the renderers. Codes stay in JSON; only
// renderers translate them, so another locale is one more table.
type Locale struct {
	Labels map[string]string
	Codes  map[string]string
}

// Korean is the default locale.
var Korean = Locale{
	Labels: map[string]string{
		"title":              "CODEYE 분석 보고서",
		"export_full":        "코드 포함 내보내기(권한 있는 사용자 전용)",
		"export_codefree":    "코드 미포함 내보내기: 도구 원문 메시지와 서술 원문을 생략했다",
		"summary":            "요약",
		"ai_review":          "AI 검토",
		"coverage":           "분석 범위",
		"tasks":              "작업 상태",
		"executions":         "실행 기록",
		"findings":           "발견 항목",
		"observations":       "원시 도구 관찰",
		"artifacts":          "산출물",
		"limitations":        "한계와 공백",
		"raw_to_findings":    "원시 진단 수와 항목 수",
		"no_findings":        "항목 없음. 이것은 결함이 없다는 뜻이 아니다. 아래 작업 상태와 공백을 확인한다.",
		"no_observations":    "도구가 보고한 관찰이 없다. 도구가 0건을 보고한 것과 분석하지 않은 범위는 다르다.",
		"unknown":            "미확정",
		"none":               "없음",
		"message_omitted":    "(코드 미포함: 생략)",
		"banner_partial":     "이 보고서는 부분 결과다. 필수 작업이나 AI 검토가 수행·검증되지 않았다.",
		"banner_failed":      "이 실행은 실패했다. 아래 내용은 수집된 구조화 기록이며 분석 결과로 간주하지 않는다.",
		"banner_cancelled":   "이 실행은 취소되었다. 아래 내용은 취소 전까지 수집된 기록이다.",
		"banner_completed":   "필수 작업과 AI 검토가 모두 확인되었다. 품질 판정은 안전 인증이 아니다.",
		"verdict_note":       "품질 판정은 안전 인증이 아니다.",
		"code_excerpt_none":  "코드 발췌 없음: controller는 snapshot 원본을 보관하지 않는다. 위치와 blob hash로 snapshot 코드를 확인한다.",
		"ai_review_optional": "선택 검토 작업(판정에 필요하지 않으며 수행으로 세지 않음)",
		// Structure metrics and secret detection (docs/ANALYZERS.md).
		"metric_observations": "그중 구조 지표(결함 아님)",
		"metric_marker":       "구조 지표, 결함 아님",
	},
	Codes: map[string]string{
		LimAIReviewNotPerformed:          "AI 검토를 수행하지 않았거나 그 결과를 검증하지 못했다. 도구 진단만 포함한다.",
		LimAIReviewPartial:               "필수 AI 검토 중 일부만 검증되었다.",
		LimResultManifestMissing:         "결과 manifest가 없다. DB에 기록된 작업 상태만 표시한다.",
		LimResultManifestRejected:        "결과 manifest 검증에 실패했다. DB에 기록된 작업 상태만 표시한다.",
		LimTestsNotPerformed:             "with-tests 프로필이 요청한 테스트 결과가 없다.",
		LimRequiredTaskIncomplete:        "필수 작업 중 성공하지 않은 작업이 있다.",
		LimStaticAnalysisGap:             "범위에 이 언어의 소스가 있으나 노드에 그 언어의 분석기가 없어 정적 분석을 하지 않았다.",
		LimStaticAnalysisNotPlanned:      "범위에 지원 언어의 소스가 없어 정적 분석 작업을 계획하지 않았다.",
		LimCleanupUncertain:              "하위 프로세스 정리를 확인하지 못했다.",
		LimArtifactRejected:              "분석 산출물 검증에 실패해 해당 작업의 결과를 제외했다.",
		LimArtifactMissing:               "분석 산출물을 읽지 못했다.",
		LimUnrecognizedArtifact:          "해석기가 등록되지 않은 산출물이 있다(결과에 반영하지 않음).",
		LimNoCodeExcerpts:                "controller는 snapshot 원본을 보관하지 않아 코드 발췌를 넣지 않았다.",
		LimTaskNotInManifest:             "결과 manifest에 없는 작업이 있다.",
		LimTaskStatusMismatch:            "산출물의 작업 상태가 controller가 접수한 상태와 달라 접수 상태를 따랐다.",
		LimRunCancelled:                  "실행이 취소되었다.",
		LimRunFailed:                     "실행이 실패했다.",
		NoteInventoryAllLanguages:        "inventory 파일 수는 snapshot manifest에 포함된 모든 파일 수다(언어 무관).",
		NoteToolFilesUnknown:             "도구가 처리한 파일 수를 확정할 수 없다(미측정이며 0이 아니다).",
		NoteNoTestsPlanned:               "테스트 실행 작업은 계획되지 않았다.",
		NoteToolZeroIsNotClean:           "도구가 0건을 보고한 작업이 있다. 결함이 없다는 뜻이 아니다.",
		"default_build_context_only":     "Go: 노드 기본 GOOS/GOARCH와 기본 build tag만 분석했다.",
		"cgo_disabled":                   "Go: cgo 파일은 분석하지 않았다.",
		"build_constraint_ignored_files": "Go: build constraint로 제외된 파일이 있다.",
		"unlisted_go_files":              "Go: go 명령이 보고하지 않은 파일이 있다.",
		"no_offline_module_cache":        "Go: 오프라인 모듈 캐시가 없다.",
		"tool_version_untested":          "Go: 시험하지 않은 toolchain 버전이다.",
		"workspace_drift":                "Go: 실행 중 workspace가 manifest와 달라졌다.",
		LimReviewLineage:                 "검토 단계 기록이 앞 단계·탐색·도구 산출물과 이어지지 않아 제외했다.",
		LimReviewPackMismatch:            "계획한 검토 팩과 다른 형식의 검토 기록이라 제외했다.",
		"review_input_trimmed":           "요청 크기 한도 때문에 일부 발췌를 모델 입력에서 뺐다.",
		"verification_budget_exceeded":   "요청 크기 한도 때문에 일부 후보를 검증하지 못했다(미검토).",
		"verification_call_failed":       "후속 검증 호출이 실패해 일부 후보를 검증하지 못했다(미검토). 앞서 수락한 판정은 유지했다.",
		"code_excerpts_go_only":          "코드 발췌는 Go 파일만 사용했다.",
		"execution_claim_rejected":       "실행·재현을 주장한 모델 서술을 거부했다.",
		// Structure, duplication and secrets pack (docs/ANALYZERS.md).
		"metrics_not_defects":                "구조 지표(복잡도·중복)는 임계값 초과 측정값이며 결함 근거가 아니다.",
		"function_end_lines_not_recorded":    "lizard: 함수 끝 줄을 기록하지 않아 시작 줄만 위치로 쓴다.",
		"in_source_suppressions_honored":     "소스 안의 도구 억제 주석을 도구가 적용했다.",
		"source_files_not_analyzed":          "도구가 읽지 못한 소스 파일이 있다.",
		"clone_columns_not_recorded":         "jscpd: 중복 구간의 열 위치를 기록하지 않았다.",
		"processed_paths_from_input_list":    "jscpd: 처리 파일은 CODEYE가 넘긴 입력 목록이다(도구가 파일별 처리를 보고하지 않음).",
		"source_files_not_compared":          "jscpd: 크기·인코딩 때문에 비교하지 않은 소스 파일이 있다.",
		"no_file_reached_clone_thresholds":   "jscpd: 중복 임계값에 닿는 파일이 없었다.",
		"history_not_scanned":                "gitleaks: workspace 사본만 검사했고 git 이력은 검사하지 않았다.",
		"secret_values_withheld":             "gitleaks: 비밀값과 그 일부·hash를 저장하지 않는다. 위치와 규칙만 남긴다.",
		"repository_ignore_file_honored":     "gitleaks: 저장소의 .gitleaksignore를 적용했다(digest 기록).",
		"binary_files_not_claimed_processed": "gitleaks: 텍스트가 아닌 파일은 처리 파일로 세지 않았다.",
		"secret_columns_not_recorded":        "gitleaks: 도구의 열 위치가 첫 줄 뒤에서 1씩 어긋나 줄 범위만 남긴다.",
		"rejected_option_gitleaksignore":     "gitleaks: 저장소 .gitleaksignore에 사본 밖을 가리키는 항목이 있어 검사를 거부했다(값은 기록하지 않음).",
		"data_directories_excluded":          "숨김·testdata·vendor·node_modules 아래 파일은 측정하지 않았다.",
		"workspace_link_refused":             "검사 대상 사본에 링크나 특수 파일이 있어 도구를 실행하지 않았다.",
		"repository_config_rejected":         "저장소 설정이 사본 밖을 가리켜 도구를 실행하지 않았다(값은 기록하지 않음).",
		"result_outside_snapshot":            "도구가 선택된 snapshot 파일이 아닌 곳의 결과를 냈다. 그 결과는 버렸고 작업은 실패다.",
		"model_input_not_secret_screened":    "비밀값 탐지가 실행되지 않아 모델 입력을 비밀값 기준으로 거르지 않았다.",
		"secret_files_withheld":              "비밀값이 탐지된 파일은 모델 입력에서 뺐다.",
		"secret_screen_failed":               "비밀값 탐지 결과를 쓸 수 없어 모델 검토를 건너뛰었다(코드 미전송).",
		// Review inputs of five languages (docs/REVIEW.md).
		"source_in_excluded_directories": "숨김·vendor·testdata·node_modules와 build·dist·target 같은 산출물·의존성 디렉터리 아래 소스는 모델 입력에서 뺐다.",
	},
}

// Code translates a code ("code" or "code:detail"); unknown codes are shown as is.
func (l Locale) Code(c string) string {
	base, detail, _ := strings.Cut(c, ":")
	if t, ok := l.Codes[base]; ok {
		if detail != "" {
			return t + " (" + detail + ")"
		}
		return t
	}
	return c
}

// Label returns a label or the key itself.
func (l Locale) Label(k string) string {
	if t, ok := l.Labels[k]; ok {
		return t
	}
	return k
}

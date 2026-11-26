package report

// labelExempt are the code-shaped strings the source scan finds that never
// reach a report reader, each with the reason. TestEmittedCodesHaveLabels
// fails on an entry the scan no longer finds or the catalog now words.
var labelExempt = map[string]string{
	"added":                     "unit-kind noun or a state of another record, not a tool coverage counter",
	"byte":                      "도구 열 단위를 가리키는 내부 접미사이며 보고서에 표시되지 않음",
	"changed":                   "unit-kind noun or a state of another record, not a tool coverage counter",
	"code":                      "증거 종류 이름(code)으로 모델 출력 검사의 인자일 뿐 보고서 코드가 아님",
	"code_evidence_missing":     "모델 출력 오류 코드로 호출 기록의 output_errors에만 남고 보고서 화면은 호출 결과(outcome)만 보임",
	"compile_db_flags_mapped":   "상수만 정의돼 있고 어떤 분석기도 이 제약 코드를 내보내지 않음",
	"copied":                    "unit-kind noun or a state of another record, not a tool coverage counter",
	"covered_by_overview":       "unit-kind noun or a state of another record, not a tool coverage counter",
	"crate":                     "unit-kind noun or a state of another record, not a tool coverage counter",
	"document":                  "증거 종류 이름(document)으로 모델 출력 검사의 인자일 뿐 보고서 코드가 아님",
	"duplicate_field":           "JSON 출력 해석 오류 종류(parse.decode_error)이며 보고서 사유로 보이지 않음",
	"evidence_id_dropped":       "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"evidence_id_duplicate":     "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"evidence_id_unknown":       "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"evidence_kind_invalid":     "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"evidence_list_too_long":    "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"execution_claim":           "호출 기록의 output_errors에만 남는 출력 오류 코드로 보고서에는 단계 한계 execution_claim_rejected가 따로 남음",
	"file":                      "unit-kind noun or a state of another record, not a tool coverage counter",
	"item_schema_invalid":       "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"mechanism_missing":         "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"modified":                  "unit-kind noun or a state of another record, not a tool coverage counter",
	"module":                    "unit-kind noun or a state of another record, not a tool coverage counter",
	"navigation_choice_invalid": "PublicLimitations가 작업·보고서 공백에서 걸러내는 내부 탐색 기록 코드",
	"navigation_no_choice":      "PublicLimitations가 작업·보고서 공백에서 걸러내는 내부 탐색 기록 코드",
	"not_an_object":             "JSON 출력 해석 오류 종류(parse.decode_error)이며 보고서 사유로 보이지 않음",
	"not_planned":               "unit-kind noun or a state of another record, not a tool coverage counter",
	"observation_id_duplicate":  "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"output_not_json":           "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"overview_not_sent":         "unit-kind noun or a state of another record, not a tool coverage counter",
	"package":                   "unit-kind noun or a state of another record, not a tool coverage counter",
	"planned":                   "unit-kind noun or a state of another record, not a tool coverage counter",
	"project":                   "unit-kind noun or a state of another record, not a tool coverage counter",
	"removed":                   "unit-kind noun or a state of another record, not a tool coverage counter",
	"renamed":                   "unit-kind noun or a state of another record, not a tool coverage counter",
	"rune":                      "도구 열 단위를 가리키는 내부 접미사이며 보고서에 표시되지 않음",
	"skipped":                   "unit-kind noun or a state of another record, not a tool coverage counter",
	"syntax_error":              "JSON decode error kind of the Python tool output parser (the counter of the same name is a unit text)",
	"test":                      "증거 종류 이름(test)으로 모델 출력 검사의 인자일 뿐 보고서 코드가 아님",
	"text_invalid":              "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"trailing_data":             "JSON 출력 해석 오류 종류(parse.decode_error)이며 보고서 사유로 보이지 않음",
	"translation_unit":          "unit-kind noun or a state of another record, not a tool coverage counter",
	"type_error":                "JSON 출력 해석 오류 종류(parse.decode_error)이며 보고서 사유로 보이지 않음",
	"unbalanced":                "cppcheck XML 해석기 내부의 오류 문구이며 저장되는 값은 unexpected_eof·syntax_error뿐임",
	"unchanged":                 "unit-kind noun or a state of another record, not a tool coverage counter",
	"unknown_candidate_id":      "호출 기록의 output_errors에만 남는 내부 출력 오류 코드로 보고서는 읽지 않음",
	"unreadable":                "unit-kind noun or a state of another record, not a tool coverage counter",
	"withheld":                  "unit-kind noun or a state of another record, not a tool coverage counter",
	"workspace_proof_mismatch":  "opencode 클라이언트 안에서 workspace_unreachable로 바뀌어 호출자에게 전달되지 않는 내부 값",
}

// storedExempt are codes in the stored example documents that are not report
// codes (an environment probe detail, a doctor check).
var storedExempt = map[string]string{}

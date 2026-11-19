package prcomment

// Korean strings of the pull-request comment. A key without text is shown
// as is.
var messages = map[string]string{
	"title":           "CODEYE 분석 결과",
	"no_report":       "head %s의 분석에 아직 보고서가 없다.",
	"status":          "상태: %s · 판정: %s",
	"head":            "분석한 head: %s (base %s)",
	"head_moved":      "이 분석 뒤 pull request의 head가 %s(으)로 바뀌었다. 새 커밋은 아직 분석하지 않았다.",
	"files":           "변경 파일 %d개 중 %d개를 모델에 보냈다.",
	"files_no_model":  "모델 검토를 하지 않아 변경 파일 %d개는 정적 분석만 거쳤다.",
	"files_removed":   "삭제된 파일 %d개는 검토하지 않았다.",
	"basis_mixed":     "GitHub이 파일 %d개의 변경 줄을 주지 않아 그 파일은 파일 단위로 셌다.",
	"basis_files":     "GitHub이 변경 줄을 주지 않아 변경 파일 단위로 셌다.",
	"findings":        "변경 부분의 주요 발견",
	"diagnostics":     "변경 부분의 도구 진단",
	"rest":            "그 밖에 변경 부분에 발견 %d건, 도구 진단 %d건이 있고, 변경 밖(기존 코드)에 발견 %d건, 도구 진단 %d건이 있다.",
	"none_in_changes": "변경 부분에서는 발견도 도구 진단도 나오지 않았다. 결함이 없다는 뜻은 아니다.",
	"gaps":            "확인하지 못한 부분",
	"gaps_more":       "그 밖의 공백 %d종",
	"footer":          "CODEYE가 고정한 커밋을 분석했다. 변경 파일을 먼저 검토했고 저장소의 나머지는 같은 깊이로 검토하지 않았을 수 있다. 모델의 발견은 후보이며 사람이 확인해야 한다. 자세한 내용은 CODEYE 보고서에 있다.",
	"cut":             "(길이 제한으로 줄였다.)",
}

var severities = map[string]string{
	"critical": "치명",
	"high":     "높음",
	"medium":   "보통",
	"low":      "낮음",
	"info":     "정보",
}

func msg(k string) string {
	if t, ok := messages[k]; ok {
		return t
	}
	return k
}

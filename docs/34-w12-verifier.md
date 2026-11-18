# W12 후보 반증과 근거 검증

`review.verify`는 Go 검토의 세 번째 필수 모델 작업이다. 워커는 같은 run·attempt의 `review.investigate` 기록을 읽고, 승인된 스냅샷과 scope에서 발췌 목록을 다시 구성한다. 후보가 없으면 추가 모델 호출 없이 `no_candidates_to_verify`를 기록한다. 앞선 작업의 미완료나 소스 전송 미승인은 해당 task의 상태와 사유로 남는다.

## 판정과 기록

- `supported`는 코드 또는 테스트 인용과 성립 조건을 요구한다. `refuted`는 코드 또는 테스트의 반증 근거와 주장이 성립하지 않는 조건을 요구한다. 두 경우 모두 원래 후보와 근거를 보존한다.
- `inconclusive`는 해소하지 못한 가정을 남긴다. 모델이 실행 재현을 주장하면 플랫폼은 `inconclusive`와 `reproduction_claim_rejected`를 기록한다. 모델이 제출한 실행 결과와 실행 식별자는 증거로 등록할 수 없다.
- 모델에는 플랫폼이 발급한 근거 ID를 준다. ID는 manifest digest, snapshot 상대경로, blob SHA-256, 행 범위에서 결정된다. 모델이 경로나 줄 번호를 새로 만들거나 프로젝트의 도구 실행을 요청해도 읽기·실행 범위가 늘어나지 않는다.
- 누락·중복 항목, 미등록 ID, 잘못된 JSON에는 한 번의 형식 복구 기회가 있다. 해당 후보, 허용된 근거와 고정 오류 코드만 다시 보낸다. 끝내 검증되지 않은 후보는 `unreviewed`로 남긴다. 일부만 유효하면 사용 가능한 판정은 보존하고 task는 `failed/review_output_incomplete`로 마친다. 사용 가능한 판정이 전혀 없으면 `failed/review_output_invalid`다.
- timeout, 권한 거부, 취소와 모델 미설정은 별도 상태·사유로 남는다. 실패한 모델 호출도 가능한 범위에서 시도 번호, 모델·OpenCode 버전, 지침 팩·요청 digest, 제공된 사용량을 보존한다. 상태와 부분 완료 여부는 워커가 결정한다.
- 저장 기록에는 메타데이터와 모델의 판정문·근거 참조를 담는다. 원본 발췌 필드, 원시 프롬프트, 내부 추론, 프로세스 출력은 저장하지 않는다. 사용량 누락은 `null`이며 요청을 구성하기 전에 실패하면 요청 digest가 빈 문자열일 수 있다.

산출물의 media type은 `application/vnd.codeye.review+json`이다. W11의 성공 기록은 `outcome`과 호출 시도 번호 없이도 읽을 수 있다. 각 역할 기록에는 선행 역할 호출 이력이 함께 들어 있으므로 합산 시에는 현재 역할의 호출만 센다. 보고서에서 검토 완료로 판단하려면 DB task 성공과 검증된 역할 기록을 함께 확인한다. [스키마](../schemas/review-record.schema.json) · [합성 호출로 만든 예시](../examples/review-record.json)

## 반례 탐색 범위

Go 코드에서 인용된 패키지 함수 최대 8개를 택한다. 같은 패키지의 직접 호출을 Go 구문 트리로 찾아 호출자 함수의 검증·소유권 이전·정리와 테스트의 관련 구간을 발췌한다. 추가 발췌는 최대 6개, 총 8,000바이트이고, 한 발췌는 최대 60줄·2,400바이트다. 탐색은 승인된 scope 내 Go 파일 80개·8MiB까지다. 파일을 열 때 manifest의 크기와 SHA-256을 대조한다.

간접 호출, 메서드 디스패치, 다른 패키지 호출과 외부 계약은 이 탐색 범위 밖이다. 모델과 보고서가 이 범위를 알 수 있도록 `caller_expansion_package_functions_only`를 남긴다. 범위 안에서 호출을 찾지 못했거나 탐색 예산을 소진한 경우도 구분한다. 파일·행·해시 검증은 인용의 출처를 확인한다. 인용을 해석한 모델 문장의 의미적 정확성에는 별도 평가가 필요하다.

## 수용 시험

| 시험 | 입력과 관찰 | 요구사항 |
|---|---|---|
| TC-87 · TC-10 | 최초 50줄에 없는 상위 호출자의 파일 정리와 테스트를 찾아 후보를 기각한다. 문서 유무 두 조건의 합성 응답 및 아래 실제 검증기 호출을 확인했다. | REQ-010,011 |
| TC-88 · TC-11 | 다른 run·attempt·snapshot, 허위 path·행·blob·근거 ID, 중복 판정, 근거 없는 기각, 모델이 단정한 재현·critical 심각도를 거부하거나 미확정으로 기록한다. W11 이전 형식 호환을 확인한다. | REQ-011,013,027 |
| TC-89 · TC-12 | 형식 복구 성공·재실패, 일부 후보 격리, 취소·권한 거부·timeout·요청 구성 전 실패를 구분한다. Linux 워커에서 실제 Go vet를 실행하고 합성 모델의 verifier timeout을 주입해 `partial` manifest, 성공한 도구 관찰, `timed_out` 검증기 기록을 확인했다. | REQ-012,026, NFR-008,010 |

2026-11-18 WSL2/Linux, Go 1.27.4, OpenCode 1.19.5 환경에서 통합 담당자가 `TestLiveVerifier`를 실행했다. W11 단계의 합성 응답으로 “Load는 정상적으로 읽고 반환할 때 파일을 항상 열어 둔다”는 후보 하나를 고정했다. 실제 GPT‑6.2 Sol 검증기는 한 번의 호출로 추가 발췌한 `load.go` 56–67행의 파일 종료 처리를 인용하고 `refuted`를 반환했다. 시험은 8.56초 걸렸으며 이 실행의 manifest digest는 `577f1c131f4a49ca98d683e3887902561ad126fde054ee95fa445f35091cc6ca`다. 이 표본은 정답을 아는 단일 반례이며 일반적인 정확도 추정치는 제공하지 않는다.

회귀 시험은 `go test -race ./internal/worker/review/... ./internal/worker/run`, 예시와 부정 입력 검사는 `python3 scripts/validate_bundle.py`로 실행한다. 위 실모델 시험은 `CODEYE_TEST_REVIEW_RELAY`와 `CODEYE_TEST_OPENCODE_BIN`이 명시된 환경에서 `go test -v -count=1 -run '^TestLiveVerifier$' ./internal/worker/review`로 별도 실행한다.

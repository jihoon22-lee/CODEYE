# CODEYE 분석 보고서

| 항목 | 값 |
|---|---|
| 진행 상태 | 부분 결과 |
| 검토 결과 | 추가 확인이 필요합니다\. |
| 보고서 작성 시각 | 2026\-11\-17T23\:50\:09\.29466596Z |

> 일부 검사를 마치지 못했습니다\. 분석 범위에서 해당 항목을 확인할 수 있습니다\.

## 요약

| 원시 진단 수와 항목 수 | 수 |
|---|---|
| 원시 도구 진단 | 1 |
| 위치 미확인 진단 | 0 |
| 항목 | 1 |
| 조치 항목\(supported/reproduced\) | 0 |
| 미확정 후보\(unreviewed/inconclusive\) | 1 |
| 기각\(refuted\) | 0 |

우선 확인:
- `fnd_8dc98efebf4ba6bda61d`

## AI 검토

상태: `not_performed` 이 실행에는 모델 검토 작업이 계획되지 않음 \(미실행\) `review_tasks_not_planned`

| task | 상태 | 결과 |
|---|---|---|
| `review.context` | `없음` | `not_planned` |
| `review.investigate` | `없음` | `not_planned` |
| `review.verify` | `없음` | `not_planned` |


## 분석 범위

- scope: `full`
- inventory_files: 2
- tool_processed_files: 1
- llm_exposed_files: 0
- test_status: `not_run`
- inventory 파일 수는 snapshot manifest에 포함된 모든 파일 수다\(언어 무관\)\.
- 테스트 실행 작업이 계획되지 않음 \(미실행\)

| task | 도구 | 버전 | exit class | 범위 파일 | 처리 파일 | 제외 | 미목록 | 단위 상태 | 한계 |
|---|---|---|---|---|---|---|---|---|---|
| `go-vet` | `go.vet` | `go1.27.4` | `success_with_findings` | 1 | 1 | 0 | 0 | 분석 완료, 진단 있음\: 1 `analyzed_with_findings` | Go\: cgo 파일은 분석하지 않음 \(범위 공백, 결함 아님\) `cgo_disabled`; Go\: 노드 기본 GOOS/GOARCH와 기본 build tag만 분석함 \(범위 공백, 결함 아님\) `default_build_context_only`; Go\: 오프라인 모듈 캐시가 없음 \(범위 공백\) `no_offline_module_cache` |

## 작업 상태

| task | 필수 | 상태 | 사유 |
|---|---|---|---|
| `go-vet` | true | `succeeded` | 없음 |

## 발견 항목

이전 보고서에는 파일 위치만 기록되어 있습니다\.

### go\.vet printf 진단 \(example\.go\:5\)

- id: `fnd_8dc98efebf4ba6bda61d`
- category / severity: `correctness` / `low` — 정책 go\-vet\-v1\: go vet은 심각도를 제공하지 않으며 영향을 검토하지 않았다\. 검토 전 기본값 low이다\.
- validation / disposition: `unreviewed` / `open`
- location: `example.go:5-5`
- 발생 조건 (`verified`) go vet이 이 snapshot 위치에 진단을 보고했다\. `ev_9a5607ac979b4c2b1416_tool`
- impact: 도구가 보고한 진단이다\. 실제 영향은 검토하지 않았다\(validation\=unreviewed\)\.
- claim (`observed`): 원문 생략\(코드 미포함 내보내기\)\. [`ev_9a5607ac979b4c2b1416_tool,ev_8dc98efebf4ba6bda61d_code`]
- evidence `ev_8dc98efebf4ba6bda61d_code` (`code`): 진단 위치\. 경로와 blob hash를 snapshot manifest와 대조했다\. 줄 범위는 노드가 manifest hash와 같은 snapshot 파일로 확인했다\. `example.go:5 sha256 33befd57c8b2`
- evidence `ev_9a5607ac979b4c2b1416_tool` (`tool`): go vet stdout의 원시 진단\(JSON pointer로 위치 지정\)\. `25992040-6180-4857-9880-8e7b4bb632a1 /smoke.test~1example/printf/0`
- observations: `obs_9a5607ac979b4c2b1416`
- recommendation: 도구 메시지와 해당 snapshot 코드를 확인한다\.
- verification: 수정 뒤 이 보고서의 실행 기록과 같은 argv로 go vet을 다시 실행해 진단이 사라지는지 확인한다\.
- limitation: 사람이나 AI가 검토하지 않은 도구 진단이다\.
- limitation: 정적 도구 관찰만 포함하며 실행으로 재현하지 않았다\.

## 원시 도구 관찰

| id | task | rule | 위치 | 상태 | 근거 | 메시지 |
|---|---|---|---|---|---|---|
| `obs_9a5607ac979b4c2b1416` | `go-vet` | `printf` | `example.go:5:28` | `resolved` | `verified_against_source_manifest` | `(코드 미포함: 생략)` |

## 실행 기록

| 실행 | 상태 | exit | 정리 | argv | cwd | stdout | stderr |
|---|---|---|---|---|---|---|---|
| `go-vet/go.list` | `exited` | `0` | `confirmed` | `go list -e -json=Dir,ImportPath,Name,GoFiles,CgoFiles,IgnoredGoFiles,TestGoFiles,XTestGoFiles,Incomplete,Error,DepsErrors ./...` | `.` | `8515fee3-0971-4964-8e65-caa478ebb9eb` | `a3622c40-0465-4c55-8f99-9f0ea6140e8e` |
| `go-vet/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json` | `.` | `f9d0b941-de63-4811-92a0-d13e5e6eae89` | `bb65a06a-175c-4dd5-b7a3-48ac39dce4d2` |
| `go-vet/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `94e413ab-7b6d-4c2e-b069-6d3191aed704` | `5bd73030-5674-4d3c-9387-02a2d978e9bb` |
| `go-vet/go.vet` | `exited` | `0` | `confirmed` | `go vet -json ./...` | `.` | `25992040-6180-4857-9880-8e7b4bb632a1` | `2f277363-0725-4f62-a262-38ca4086492e` |

## 산출물

| id | task | role | media | sha256 | bytes | decoder |
|---|---|---|---|---|---|---|
| `409e47bd-ab77-4b63-8591-a32c381498ad` | `-` | `source_manifest` | `application/vnd.codeye.source-manifest+json` | `934b65c3121ba7eaf10e02f05ca56a1b7f8d202a84374bdd1b01a322b8da9032` | 549 | `-` |
| `41cc6643-39ea-4180-9803-6972f94c0ce9` | `-` | `result_manifest` | `application/vnd.codeye.result-manifest+json` | `0a77bedf10d2a00a1635729dc09817ca1dc69b84e49382aeee9b8f92f8e4d4e5` | 3284 | `-` |
| `666d4936-6bab-42b9-b91e-bba312e26540` | `go-vet` | `observations` | `application/vnd.codeye.observations+json` | `d72c6dcec2dc1ee0ab3830abb3d6812e025a4f03c62a5a4f5319aeee6f4d12d5` | 4542 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=go_vet_observations` |
| `25992040-6180-4857-9880-8e7b4bb632a1` | `go-vet` | `stdout` | `text/plain` | `d90a5b0536243352cc04efe8b7437056a3fcbfa198e01fb4c2432138d0759307` | 221 | `-` |
| `8515fee3-0971-4964-8e65-caa478ebb9eb` | `go-vet` | `stdout` | `text/plain` | `04513871e92c8227bb2e06a808559f61e1c3379f36be77c6a39b14f68ce35ecc` | 115 | `-` |
| `94e413ab-7b6d-4c2e-b069-6d3191aed704` | `go-vet` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `f9d0b941-de63-4811-92a0-d13e5e6eae89` | `go-vet` | `stdout` | `text/plain` | `4ff055905c9ba3ee906c1de91948d632f90cdbab99fbd357e2276ea70dd11b65` | 66 | `-` |
| `2f277363-0725-4f62-a262-38ca4086492e` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `5bd73030-5674-4d3c-9387-02a2d978e9bb` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `a3622c40-0465-4c55-8f99-9f0ea6140e8e` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `bb65a06a-175c-4dd5-b7a3-48ac39dce4d2` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |

## 한계와 공백

- AI 검토를 수행하지 않았거나 그 결과를 검증하지 못함 — 도구 진단만 포함함 \(미검토\)
- 고정 스냅샷에서 표시할 발췌를 확보하지 못함 \(참고\)

### 기술 상세

| 항목 | 값 |
|---|---|
| report | `eebfcfd1-b7c6-4417-ab52-cf481ccf487a` rev `1` |
| digest | `90b935ba6a3c` |
| status | `partial` |
| quality\_verdict | `inconclusive` |
| run | `e54c0dd2-aa43-4f1c-9422-0b0c33e3fc4b` |
| attempt | `0db5c46f-5d30-4c27-aebe-628fa8730b26` #`1` epoch `1` |
| snapshot | `742de1c6-d825-4b33-8101-b1669082cf18` manifest `934b65c3121b` |
| profile | `deep-review` |
| created\_at | `2026-11-17T23:50:09.29466596Z` |
| export | 코드 미포함 내보내기\: 도구 원문 메시지와 서술 원문을 생략했다 |

digest sha256: `90b935ba6a3ca1e6eef5e59c4350a66649e6fbcf092ee383a8d2e0816488cfb7`

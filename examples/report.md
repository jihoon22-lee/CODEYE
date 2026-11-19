# CODEYE 분석 보고서

> **합성 예시.** 아래는 `fixtures/modules/input`에 대해 CODEYE의 탐색·검토 역할을 실제로 실행하고 보고서 builder가 만든 출력이다. 모델 응답은 미리 정한 스크립트이며 모델을 부르지 않았다. 정적 분석 진단, 복잡도 지표, 비밀정보 탐지는 합성한 도구 결과이고 실제 도구를 실행하지 않았다. 사용자 프로젝트의 결함 발견 결과가 아니다. 기계 판독 원본은 [report.json](report.json)이다.

| 항목 | 값 |
|---|---|
| report | `0195f3a0-0000-7000-8000-000000000504` rev `1` |
| digest | `7ec47130f4fb` |
| status | `partial` |
| quality\_verdict | `attention_required` 품질 판정은 안전 인증이 아니다\. |
| run | `0195f3a0-0000-7000-8000-000000000501` |
| attempt | `0195f3a0-0000-7000-8000-000000000502` #`1` epoch `1` |
| snapshot | `0195f3a0-0000-7000-8000-000000000503` manifest `bb0ed6120181` |
| profile | `deep-review` |
| created\_at | `2026-11-18T00:10:00Z` |
| export | 코드 포함 내보내기\(권한 있는 사용자 전용\) |

> 이 보고서는 부분 결과다\. 필수 작업이나 AI 검토가 수행·검증되지 않았다\.

## 종합

실행 상태는 부분 결과, 품질 판정은 확인·조치할 항목이 있다\.

| 항목 | 수 |
|---|---|
| 원시 도구 진단 | 4 |
| 위치 미확인 진단 | 0 |
| 항목 | 5 |
| 조치 항목\(supported/reproduced\) | 1 |
| 미확정 후보\(unreviewed/inconclusive\) | 3 |
| 기각\(refuted\) | 1 |
| 그중 구조 지표\(결함 아님\) | 1 |

### 분석 종류별 상태

| 분석 | 상태 | 핵심 수 |
|---|---|---|
| 환경 분석 | △ 일부 수행 | 범위 안 모듈 3 · 선언 요구 3 · 충족 2 · 불일치 1 · 판단 불가 0 · 의존성 받기 작업 3 · 그중 성공 1 · 모델 해석 메모 1 |
| 문서 분석 | ✓ 성공 | 발견 문서 4 · 읽은 문서 3 · 읽지 않은 문서 1 · 문서 없는 모듈 0 · 계약 2 · 충돌 1 · 질문 1 · 지침 메모 1 |
| 정적 분석 | △ 일부 수행 | 분석 작업 9 · 성공 6 · 성공하지 않음 3 · 진단 2 · 발견 3 · 조치 0 · 미확정 3 |
| 구조·중복 지표 | △ 일부 수행 | 측정 작업 2 · 임계 초과 측정 1 · 지표 종류 1 |
| 비밀정보 | ✓ 성공 | 탐지 작업 1 · 탐지 1 · 파일 1 |
| 코드 분석\(모델 검토\) | ✓ 성공 | 모델 호출 5 · 발견 2 · 조치 1 · 미확정 0 · 기각 1 |
| 분석 공백 | \- | 공백 7 |

### 먼저 볼 것

- 조치 항목 1개가 있다\. 코드 분석\(모델 검토\) 절
- 비밀정보로 보이는 값이 1곳에서 탐지됐다\. 비밀정보 절
- 판정에 필요한 분석 작업 1개가 성공하지 않았다\. 정적 분석 절
- 모듈이 선언한 도구 요구와 노드 관측이 1건 어긋난다\. 환경 분석 절
- 문서 안내끼리 충돌하는 곳이 1건 있다\(모델 해석\)\. 문서 분석 절
- 확인되지 않은 후보 3개가 남아 있다\. 정적 분석 절
- 분석하지 못했거나 일부만 분석한 범위가 7건 있다\. 분석 공백 절

### 우선 확인할 발견

- 재시도 반복에서 앞선 시도의 오류를 덮어씀 — supported / low `review_interpret_1`
- go\.staticcheck SA4006\: this value of err is overwritten before the previous attempt\'s error is logged \(synthetic example\) — unreviewed / low `fnd_e67a73d834b3bddb35e6`
- go\.staticcheck SA6005\: should use strings\.EqualFold instead \(synthetic example\) — unreviewed / low `fnd_4bc80f2ae454c254fb78`
- any\.gitleaks generic\-api\-key\: secret detected by gitleaks rule generic\-api\-key \(value withheld\) — unreviewed / low `fnd_ad49092ef174f1d0949c`

## 환경 분석

상태\: △ 일부 수행 · 작업 5개 중 3개 성공

탐색 산출물을 검증해 사용했다\.

| 항목 | 수 |
|---|---|
| 범위 안 모듈 | 3 |
| 선언 요구 | 3 |
| 충족 | 2 |
| 불일치 | 1 |
| 판단 불가 | 0 |
| 의존성 받기 작업 | 3 |
| 그중 성공 | 1 |
| 모델 해석 메모 | 1 |

### 모듈

| 모듈 | 생태계 | 경로 | 범위 |
|---|---|---|---|
| `go:.` | `go` | `.` | 범위 안 |
| `go:svc/a` | `go` | `svc/a` | 범위 안 |
| `go:svc/b` | `go` | `svc/b` | 범위 안 |

### 선언된 요구

| 모듈 | 도구 | 요구 | 비교 | 출처 |
|---|---|---|---|---|
| `go:.` | `go` | `1.22` | `minimum` | `go.mod:3` |
| `go:svc/a` | `go` | `1.21` | `minimum` | `svc/a/go.mod:3` |
| `go:svc/b` | `go` | `1.30` | `minimum` | `svc/b/go.mod:3` |

### 노드 관측\(런타임\)

| 도구 | 상태 | 버전 | 세부 |
|---|---|---|---|
| `go` | 관측 | `1.27.4` | `-` |

### 모듈별 판정

| 모듈 | 도구 | 요구 ID | 상태 | 이유 |
|---|---|---|---|---|
| `go:.` | `go` | `go_directive@go.mod` | ✓ 충족 | `-` |
| `go:svc/a` | `go` | `go_directive@svc/a/go.mod` | ✓ 충족 | `-` |
| `go:svc/b` | `go` | `go_directive@svc/b/go.mod` | ✕ 불일치 | `-` |

### 분석 도구 관측

| 도구 | 분석기 | 상태 | 버전 | 세부 |
|---|---|---|---|---|
| `gitleaks` | `any.gitleaks` | 관측 | `8.30.1` | `-` |
| `jscpd` | `any.jscpd` | 확인 안 함 | `-` | `-` |
| `lizard` | `any.lizard` | 관측 | `1.26.1` | `-` |
| `staticcheck` | `go.staticcheck` | 관측 | `2026.2.2` | `-` |

### 의존성 받기

| 모듈 | 패키지 관리자 | 상태 | 받은 수 | 이유 | 한계 |
|---|---|---|---|---|---|
| `go:.` | `go 1.27.4` | ✓ 받음 | 2/2 | \- | \- |
| `go:svc/a` | `go 1.27.4` | ✕ 받지 못함 | 0 | 의존성 저장소\(registry·proxy\)에 연결하지 못했다\. | \- |
| `go:svc/b` | `go 1.27.4` | – 받지 않음 | 0/0 | 받을 외부 의존성이 없다\. | \- |

### 환경 메모

> 모델 해석 — review\.environment 역할의 수락된 기록에서 가져온 모델 서술이다\. 도구나 실행으로 확인한 사실이 아니다\. 줄 끝의 근거 ID는 이 절의 근거 표에 있다\.

- go\:svc/b\: 영향 — svc/b 모듈의 go vet은 선언 버전보다 낮은 노드 toolchain 때문에 수행할 수 없다\. / 여전히 확인 가능 — 루트와 svc/a 모듈의 정적 검사와 코드 검토는 가능하다\. / 필요한 관측 — go 1\.30 이상 toolchain의 노드 관측 `ev_d5235bd29437d7ca0c3f6955`

### 근거

| 근거 ID | 종류 | 위치 | blob sha256 |
|---|---|---|---|
| `ev_d5235bd29437d7ca0c3f6955` | `manifest` | `svc/b/go.mod:3-3` | `190d2b487010` |

## 문서 분석

상태\: ✓ 성공 · 작업 2개 중 2개 성공

탐색 산출물을 검증해 사용했다\.

읽기 예산\: 문서당 32768바이트, 전체 262144바이트, 최대 1000개\. 이번에 읽은 양은 1112바이트다\.

| 항목 | 수 |
|---|---|
| 발견 문서 | 4 |
| 읽은 문서 | 3 |
| 읽지 않은 문서 | 1 |
| 문서 없는 모듈 | 0 |
| 계약 | 2 |
| 충돌 | 1 |
| 질문 | 1 |
| 지침 메모 | 1 |

### 발견한 문서

| 경로 | 종류 | 적용 범위 | 상태 | 이유 | 바이트 | 줄 |
|---|---|---|---|---|---|---|
| `AGENTS.md` | `agent_instructions` | `.` | ✓ 읽음 | `-` | 640 | 11 |
| `README.md` | `readme` | `.` | ✓ 읽음 | `-` | 288 | 9 |
| `docs/design.md` | `design` | `docs` | △ 예산 초과로 읽지 않음 | `document_too_large` | 41426 | \- |
| `svc/b/README.md` | `readme` | `svc/b` | ✓ 읽음 | `-` | 184 | 5 |

### 모듈별 문서

| 모듈 | 상태 | 자체 문서 | 상위에서 이어받은 문서 |
|---|---|---|---|
| `go:.` | 자체 문서 있음 | `AGENTS.md, README.md, docs/design.md` | `-` |
| `go:svc/a` | 상위 문서만 있음 | `-` | `AGENTS.md, README.md` |
| `go:svc/b` | 자체 문서 있음 | `svc/b/README.md` | `AGENTS.md, README.md` |

### 같은 범위에 겹치는 안내

| 경로 | 문서 | 평가 |
|---|---|---|
| `.` | `AGENTS.md, README.md` | 문서 역할이 함께 읽음\(충돌은 아래 목록\) |
| `docs` | `AGENTS.md, README.md, docs/design.md` | 문서 역할이 함께 읽음\(충돌은 아래 목록\) |
| `svc/b` | `AGENTS.md, README.md, svc/b/README.md` | 문서 역할이 함께 읽음\(충돌은 아래 목록\) |

### 탐색 한계

- 읽기 예산을 넘은 문서가 있어 그 문서는 읽지 않았다\. `document_budget_exceeded`
- 탐색은 안내끼리의 의미 충돌을 판단하지 않는다\(문서 역할이 판단한다\)\. `semantic_conflicts_not_assessed`

> 모델 해석 — review\.documents 역할의 수락된 기록에서 가져온 모델 서술이다\. 도구나 실행으로 확인한 사실이 아니다\. 줄 끝의 근거 ID는 이 절의 근거 표에 있다\.

### 문서가 정한 계약

- \[\.\] 하위 호출이 실패하면 최대 3번 재시도한다\. `ev_d0201000bc5f5928f7af84a0`
- \[svc/b\] 결제 호출은 요청마다 정확히 한 번만 한다\. `ev_22e8085e538626e19e503037`

### 안내 사이의 충돌

- \[svc/b\] 루트 안내는 재시도를, svc/b 안내는 재시도 금지를 요구한다\. / 영향\: svc/b 결제 호출의 재시도 여부를 코드에서 확인해야 한다\. `ev_d0201000bc5f5928f7af84a0` `ev_22e8085e538626e19e503037`

### 확인할 질문

- \[svc/b\] svc/b의 Charge 호출자가 실패 시 재시도하는가? `ev_22e8085e538626e19e503037`

### 지침 메모

- 권한·네트워크·키 조회를 요구하는 지침은 분석 자료로만 기록했다\. `ev_a04dcae24bc99f94e2c70208`

### 근거

| 근거 ID | 종류 | 위치 | blob sha256 |
|---|---|---|---|
| `ev_22e8085e538626e19e503037` | `document` | `svc/b/README.md:1-5` | `49ae0cef2ada` |
| `ev_a04dcae24bc99f94e2c70208` | `document` | `AGENTS.md:1-11` | `7ae2874cb0c4` |
| `ev_d0201000bc5f5928f7af84a0` | `document` | `README.md:1-9` | `cb8e4c2940c4` |

## 정적 분석

상태\: △ 일부 수행 · 작업 9개 중 6개 성공

도구가 보고한 진단과 그 발견이다\. 진단이 0건인 성공은 결함이 없다는 뜻이 아니다\. 성공하지 않은 작업의 범위는 분석되지 않았다\.

| 항목 | 수 |
|---|---|
| 분석 작업 | 9 |
| 성공 | 6 |
| 성공하지 않음 | 3 |
| 진단 | 2 |
| 발견 | 3 |
| 조치 | 0 |
| 미확정 | 3 |

### 언어\: go

| 작업 | 도구 | 필수 | 상태 | 결과 문서 | 버전 | 범위/처리 파일 | 진단 | 규칙별 수 |
|---|---|---|---|---|---|---|---|---|
| `go-vet` | `go.vet (go)` | 필수 | ✓ 성공 | 확인함 · success\_without\_findings | `go1.27.4` | 1 / 1 | 0 | `-` |
| `go-vet.m01` | `go.vet (go)` | 필수 | ✓ 성공 | 확인함 · success\_without\_findings | `go1.27.4` | 1 / 1 | 0 | `-` |
| `go-vet.m02` | `go.vet (go)` | 필수 | ✕ 실패 · 노드의 toolchain이 없거나 모듈 요구보다 낮아 실행하지 못했다\. | 확인함 · configuration\_error | `go1.27.4` | 1 / 미확정 | 실행하지 않음 | `-` |
| `staticcheck` | `go.staticcheck (staticcheck)` | 선택 | ✓ 성공 | 확인함 · success\_with\_findings | `2026.2.2` | 1 / 1 | 1 | `SA4006 1` |
| `staticcheck.m01` | `go.staticcheck (staticcheck)` | 선택 | ✓ 성공 | 확인함 · success\_with\_findings | `2026.2.2` | 1 / 1 | 1 | `SA6005 1` |
| `staticcheck.m02` | `go.staticcheck (staticcheck)` | 선택 | ✕ 실패 · 노드의 toolchain이 없거나 모듈 요구보다 낮아 실행하지 못했다\. | 확인함 · execution\_error | `2026.2.2` | 1 / 미확정 | 실행하지 않음 | `-` |

### 언어 무관\(지표·비밀정보 도구\)

이 도구들의 측정값과 탐지는 구조·중복 지표와 비밀정보 절에 있다\. 비밀정보 탐지의 발견은 이 절의 발견 목록에 있다\.

| 작업 | 도구 | 필수 | 상태 | 결과 문서 | 버전 | 범위/처리 파일 | 진단 | 규칙별 수 |
|---|---|---|---|---|---|---|---|---|
| `gitleaks` | `any.gitleaks (gitleaks)` | 선택 | ✓ 성공 | 확인함 · success\_with\_findings | `8.30.1` | 10 / 10 | 탐지 1 | `-` |
| `jscpd` | `any.jscpd (jscpd)` | 선택 | – 실행 안 함\(미지원\) · 이 도구가 필요로 하는 언어 toolchain이 노드에 지정되지 않아 실행하지 않았다\(0건 통과 아님\)\. | 확인함 · not\_run | `-` | 3 / 0 | 실행하지 않음 | `-` |
| `lizard` | `any.lizard (lizard)` | 선택 | ✓ 성공 | 확인함 · success\_without\_findings | `1.26.1` | 3 / 3 | 지표 1 | `-` |

### 진단

| id | 작업 | 규칙 | 위치 | 위치 확인 | 메시지 |
|---|---|---|---|---|---|
| `obs_26c8b6f61cb6312d0fb3` | `staticcheck` | `SA4006` | `gateway.go:13` | `verified_against_source_manifest` | `this value of err is overwritten before the previous attempt's error is logged (synthetic example)` |
| `obs_ce1cfe93957ba57dbc4e` | `staticcheck.m01` | `SA6005` | `svc/a/normalize.go:8` | `verified_against_source_manifest` | `should use strings.EqualFold instead (synthetic example)` |

### 발견

#### go\.staticcheck SA4006\: this value of err is overwritten before the previous attempt\'s error is logged \(synthetic example\)

- id: `fnd_e67a73d834b3bddb35e6`
- category / severity: `correctness` / `low` — 분석 도구 진단의 심각도는 검토 전 기본값 low이다\. 도구가 표시한 원문 등급은 관찰에 그대로 남기며 영향을 검토하지 않았다\.
- validation / disposition: `unreviewed` / `open`
- location: `gateway.go:13-13`
- 발생 조건 (`verified`) 정적 분석 도구가 이 snapshot 위치에 진단을 보고했다\. `ev_26c8b6f61cb6312d0fb3_tool`
- impact: 도구가 보고한 진단이다\. 실제 영향은 검토하지 않았다\(validation\=unreviewed\)\.
- claim (`observed`): go\.staticcheck SA4006 진단\: this value of err is overwritten before the previous attempt\'s error is logged \(synthetic example\) [`ev_26c8b6f61cb6312d0fb3_tool,ev_e67a73d834b3bddb35e6_code`]
- evidence `ev_e67a73d834b3bddb35e6_code` (`code`): 진단 위치\. 경로와 blob hash를 snapshot manifest와 대조했다\. 줄 범위는 노드가 manifest hash와 같은 snapshot 파일로 확인했다\. `gateway.go:13 sha256 a03fee1a659e`
- evidence `ev_26c8b6f61cb6312d0fb3_tool` (`tool`): 도구 출력 stream의 원시 진단\(레코드 순번과 byte 범위로 위치 지정\)\. `0195f3a0-0000-7000-8000-000000000546`
- observations: `obs_26c8b6f61cb6312d0fb3`
- recommendation: 도구 메시지와 해당 snapshot 코드를 확인한다\.
- verification: 수정 뒤 이 보고서의 실행 기록과 같은 argv로 같은 도구를 다시 실행해 진단이 사라지는지 확인한다\.
- limitation: 사람이나 AI가 검토하지 않은 도구 진단이다\.
- limitation: 정적 도구 관찰만 포함하며 실행으로 재현하지 않았다\.

#### go\.staticcheck SA6005\: should use strings\.EqualFold instead \(synthetic example\)

- id: `fnd_4bc80f2ae454c254fb78`
- category / severity: `performance` / `low` — 분석 도구 진단의 심각도는 검토 전 기본값 low이다\. 도구가 표시한 원문 등급은 관찰에 그대로 남기며 영향을 검토하지 않았다\.
- validation / disposition: `unreviewed` / `open`
- location: `svc/a/normalize.go:8-8`
- 발생 조건 (`verified`) 정적 분석 도구가 이 snapshot 위치에 진단을 보고했다\. `ev_ce1cfe93957ba57dbc4e_tool`
- impact: 도구가 보고한 진단이다\. 실제 영향은 검토하지 않았다\(validation\=unreviewed\)\.
- claim (`observed`): go\.staticcheck SA6005 진단\: should use strings\.EqualFold instead \(synthetic example\) [`ev_ce1cfe93957ba57dbc4e_tool,ev_4bc80f2ae454c254fb78_code`]
- evidence `ev_4bc80f2ae454c254fb78_code` (`code`): 진단 위치\. 경로와 blob hash를 snapshot manifest와 대조했다\. 줄 범위는 노드가 manifest hash와 같은 snapshot 파일로 확인했다\. `svc/a/normalize.go:8 sha256 2ebaac2aba07`
- evidence `ev_ce1cfe93957ba57dbc4e_tool` (`tool`): 도구 출력 stream의 원시 진단\(레코드 순번과 byte 범위로 위치 지정\)\. `0195f3a0-0000-7000-8000-00000000054c`
- observations: `obs_ce1cfe93957ba57dbc4e`
- recommendation: 도구 메시지와 해당 snapshot 코드를 확인한다\.
- verification: 수정 뒤 이 보고서의 실행 기록과 같은 argv로 같은 도구를 다시 실행해 진단이 사라지는지 확인한다\.
- limitation: 사람이나 AI가 검토하지 않은 도구 진단이다\.
- limitation: 정적 도구 관찰만 포함하며 실행으로 재현하지 않았다\.

#### any\.gitleaks generic\-api\-key\: secret detected by gitleaks rule generic\-api\-key \(value withheld\)

- id: `fnd_ad49092ef174f1d0949c`
- category / severity: `security` / `low` — 분석 도구 진단의 심각도는 검토 전 기본값 low이다\. 도구가 표시한 원문 등급은 관찰에 그대로 남기며 영향을 검토하지 않았다\.
- validation / disposition: `unreviewed` / `open`
- location: `svc/b/charge.go:6-6`
- 발생 조건 (`verified`) 정적 분석 도구가 이 snapshot 위치에 진단을 보고했다\. `ev_5d7b53aeeecc5810853b_tool`
- impact: 도구가 보고한 진단이다\. 실제 영향은 검토하지 않았다\(validation\=unreviewed\)\.
- claim (`observed`): any\.gitleaks generic\-api\-key 진단\: secret detected by gitleaks rule generic\-api\-key \(value withheld\) [`ev_5d7b53aeeecc5810853b_tool,ev_ad49092ef174f1d0949c_code`]
- evidence `ev_ad49092ef174f1d0949c_code` (`code`): 진단 위치\. 경로와 blob hash를 snapshot manifest와 대조했다\. 줄 범위는 노드가 manifest hash와 같은 snapshot 파일로 확인했다\. `svc/b/charge.go:6 sha256 4d5338492857`
- evidence `ev_5d7b53aeeecc5810853b_tool` (`tool`): 도구 출력 stream의 원시 진단\(레코드 순번과 byte 범위로 위치 지정\)\. `0195f3a0-0000-7000-8000-00000000055e`
- observations: `obs_5d7b53aeeecc5810853b`
- recommendation: 도구 메시지와 해당 snapshot 코드를 확인한다\.
- verification: 수정 뒤 이 보고서의 실행 기록과 같은 argv로 같은 도구를 다시 실행해 진단이 사라지는지 확인한다\.
- limitation: 사람이나 AI가 검토하지 않은 도구 진단이다\.
- limitation: 정적 도구 관찰만 포함하며 실행으로 재현하지 않았다\.

## 구조·중복 지표

상태\: △ 일부 수행 · 작업 2개 중 1개 성공

구조 지표는 임계값을 넘은 측정값이며 결함이 아니다\. 발견을 만들지 않고 품질 판정에 쓰지 않는다\.

| 항목 | 수 |
|---|---|
| 측정 작업 | 2 |
| 임계 초과 측정 | 1 |
| 지표 종류 | 1 |

### 측정 작업

| 작업 | 상태 | 이유 |
|---|---|---|
| `jscpd` | – 실행 안 함\(미지원\) | 이 도구가 필요로 하는 언어 toolchain이 노드에 지정되지 않아 실행하지 않았다\(0건 통과 아님\)\. |
| `lizard` | ✓ 성공 | \- |

### 지표별 요약

| 지표 | 단위 | 임계값 | 최댓값 | 수 |
|---|---|---|---|---|
| `cyclomatic_complexity` | `count` | 16 | 17 | 1 |

### 임계 초과 측정

| id | 작업 | 규칙 | 위치 | 위치 확인 | 메시지 |
|---|---|---|---|---|---|
| `obs_d92e654305882bf65d9b` | `lizard` | `cyclomatic_complexity` | `gateway.go:10` | `verified_against_source_manifest` | `[구조 지표, 결함 아님: cyclomatic_complexity 17 >= 16 count] Forward(...) at gateway.go:10: CCN 17 (synthetic example)` |

## 비밀정보

상태\: ✓ 성공 · 작업 1개 중 1개 성공

비밀정보 탐지는 위치와 규칙만 기록한다\. 값과 그 일부·hash는 저장하지 않는다\.

탐지된 파일은 모든 모델 입력에서 뺐다\.

| 항목 | 수 |
|---|---|
| 탐지 작업 | 1 |
| 탐지 | 1 |
| 파일 | 1 |

### 탐지 작업

| 작업 | 상태 | 이유 |
|---|---|---|
| `gitleaks` | ✓ 성공 | \- |

### 탐지 위치

| 규칙 | 위치 | 위치 확인 | 발견 |
|---|---|---|---|
| `generic-api-key` | `svc/b/charge.go:6-6` | `verified_against_source_manifest` | `fnd_ad49092ef174f1d0949c` |

## 코드 분석\(모델 검토\)

상태\: ✓ 성공 · 작업 3개 중 3개 성공

검토 팩\: review\-r1@2\.0\.0\(역할 팩\)

모델 호출 5회, 그중 수락 5회\(환경·문서 역할 호출 포함, 전체 합계\)\.

모델 요청 크기 합계 61504바이트\(지침과 입력을 합친 요청 크기이며 소스 크기가 아니다\)\.

모델에 보낸 파일 6개 / snapshot 파일 10개\.

| 항목 | 수 |
|---|---|
| 모델 호출 | 5 |
| 발견 | 2 |
| 조치 | 1 |
| 미확정 | 0 |
| 기각 | 1 |

### 코드 분석 역할

| 역할 | 작업 상태 | 결과 문서 | 이유 |
|---|---|---|---|
| `review.investigate` | ✓ 성공 | `accepted` | `-` |
| `review.interpret` | ✓ 성공 | `accepted` | `-` |
| `review.verify` | ✓ 성공 | `accepted` | `-` |

### 모델 검토 발견

#### Forward가 결제 호출에도 재시도를 적용할 수 있음

- id: `review_investigate_1`
- category / severity: `api_contract` / `medium` — 결제가 중복되면 고객에게 직접 영향이 있다\.
- validation / disposition: `refuted` / `open`
- location: `gateway.go:1-16`
- location: `README.md:1-9`
- 발생 조건 (`assumed`) svc/b 결제 호출을 Forward로 감싸는 경우 `ev_b180ff22dd53f0732c110ff8,ev_d0201000bc5f5928f7af84a0`
- 발생 조건 (`assumed`) 후보가 성립하지 않는 조건 · Forward는 인자로 받은 send만 재시도하며 발췌 안에서 결제 호출을 감싸는 곳이 없다\. `ev_b180ff22dd53f0732c110ff8`
- 반대 근거 `ev_b180ff22dd53f0732c110ff8`
- impact: 발췌 안에서 Forward가 결제 호출을 감싸는 경로가 없다\.
- claim (`inferred`): Forward가 결제 호출에도 재시도를 적용할 수 있음 [`ev_b180ff22dd53f0732c110ff8,ev_d0201000bc5f5928f7af84a0`]
- claim (`inferred`): Forward는 인자로 받은 send만 재시도하며 발췌 안에서 결제 호출을 감싸는 곳이 없다\. [`ev_b180ff22dd53f0732c110ff8`]
- evidence `ev_b180ff22dd53f0732c110ff8` (`code`): 고정 스냅샷의 code 발췌 위치 `gateway.go:1 sha256 a03fee1a659e`
- evidence `ev_d0201000bc5f5928f7af84a0` (`document`): 고정 스냅샷의 document 발췌 위치 `README.md:1 sha256 cb8e4c2940c4`
- recommendation: 결제 경로에서는 재시도를 끈다\.
- verification: 저장소 밖 호출자가 Charge를 Forward로 감싸는지 확인한다\.

#### 재시도 반복에서 앞선 시도의 오류를 덮어씀

- id: `review_interpret_1`
- category / severity: `error_handling` / `low` — 실패 원인 추적이 어려워지지만 동작 결과는 바뀌지 않는다\.
- validation / disposition: `supported` / `open`
- location: `gateway.go:3-16`
- 발생 조건 (`assumed`) send가 두 번 이상 실패하는 경우 `ev_f3f356075c54fb660cec2211`
- 발생 조건 (`assumed`) send가 두 번 이상 실패한다\. `ev_f3f356075c54fb660cec2211`
- impact: 재시도가 모두 실패하면 첫 실패의 원인을 로그에서 찾을 수 없다\.
- claim (`inferred`): 재시도 반복에서 앞선 시도의 오류를 덮어씀 [`ev_f3f356075c54fb660cec2211`]
- claim (`inferred`): 재시도가 모두 실패하면 첫 실패의 원인을 로그에서 찾을 수 없다\. [`ev_f3f356075c54fb660cec2211`]
- evidence `ev_f3f356075c54fb660cec2211` (`code`): 고정 스냅샷의 code 발췌 위치 `gateway.go:3 sha256 a03fee1a659e`
- observations: `obs_26c8b6f61cb6312d0fb3`
- recommendation: 시도마다 오류를 기록하거나 errors\.Join으로 묶어 반환한다\.
- verification: send가 연속으로 실패하는 테스트로 반환 오류를 확인한다\.

### 진단 해석의 한계

> 모델 해석 — review\.interpret 역할의 수락된 기록에서 가져온 모델 서술이다\. 도구나 실행으로 확인한 사실이 아니다\. 줄 끝의 ID는 정적 분석 절의 진단이다\.

- Normalize의 호출자와 비교 용도가 입력에 없어 진단의 영향을 판단할 수 없다\. `obs_ce1cfe93957ba57dbc4e`

## 분석 공백

분석하지 못했거나 일부만 분석한 범위를 모은 목록이다\. 이 범위의 결과는 0건이 아니라 미확인이다\.

| 분석 | 공백 | 대상 | 세부 |
|---|---|---|---|
| 환경 분석 | 의존성을 받지 못했다 | `deps.go.m01` | 의존성 저장소\(registry·proxy\)에 연결하지 못했다\. |
| 문서 분석 | 예산 초과로 읽지 않은 문서 | `docs/design.md` | 문서가 문서당 읽기 한도보다 크다\. |
| 정적 분석 | 작업 실패 | `go-vet.m02` | 노드의 toolchain이 없거나 모듈 요구보다 낮아 실행하지 못했다\. |
| 정적 분석 | 작업 실패 | `staticcheck.m02` | 노드의 toolchain이 없거나 모듈 요구보다 낮아 실행하지 못했다\. |
| 구조·중복 지표 | 실행하지 않음\(미지원\) | `jscpd` | 이 도구가 필요로 하는 언어 toolchain이 노드에 지정되지 않아 실행하지 않았다\(0건 통과 아님\)\. |
| 비밀정보 | git 이력은 검사하지 않았다 | `gitleaks` | \- |
| 보고서 전체 | 테스트를 실행하지 않았다 | `-` | \- |

## 부록\: 작업·실행·산출물

아래는 위 절이 근거로 쓴 원 기록이다\.

### AI 검토

상태: `performed`

| task | 상태 | 결과 |
|---|---|---|
| `review.documents` | `succeeded` | `accepted` |
| `review.investigate` | `succeeded` | `accepted` |
| `review.verify` | `succeeded` | `accepted` |

선택 검토 작업\(판정에 필요하지 않으며 수행으로 세지 않음\)

| task | 상태 | 결과 |
|---|---|---|
| `review.environment` | `succeeded` | `accepted` |
| `review.interpret` | `succeeded` | `accepted` |

- 모델 호출 `environment` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `da9c21fe08efaab7c307490102e69d35fc732f4d57a5faf2da9d2681768cfa3e`
- 모델 호출 `documents` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `42c12bd1d7e62d456d72f6b5af7caed424977dca248ee4f3014dbe5cf98b0d6a`
- 모델 호출 `investigate` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `2df8621fe980f31a57d0a1dc8ca698fa05585ea36361bba20ee3c4f31a1be0e7`
- 모델 호출 `interpret` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `9fdac5c6493f275c7ef17058ef4228176281eb6efbf3a88729138df168929921`
- 모델 호출 `verify` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `b5a7cc45e05d4df310c6311ab1ae496a72b6f1ac7d51848e5a59edfd9d4d98b0`

### 분석 범위

- scope: `full`
- inventory_files: 10
- tool_processed_files: 미확정
- llm_exposed_files: 6
- test_status: `not_run`
- inventory 파일 수는 snapshot manifest에 포함된 모든 파일 수다\(언어 무관\)\.
- 테스트 실행 작업은 계획되지 않았다\.
- 도구가 처리한 파일 수를 확정할 수 없다\(미측정이며 0이 아니다\)\.
- 도구가 0건을 보고한 작업이 있다\. 결함이 없다는 뜻이 아니다\.

| task | 도구 | 버전 | exit class | 범위 파일 | 처리 파일 | 제외 | 미목록 | 패키지 상태 |
|---|---|---|---|---|---|---|---|---|
| `gitleaks` | `any.gitleaks` | `8.30.1` | `success_with_findings` | 10 | 10 | 0 | 0 | `analyzed=10` |
| `go-vet` | `go.vet` | `go1.27.4` | `success_without_findings` | 1 | 1 | 0 | 0 | `analyzed_clean=1` |
| `go-vet.m01` | `go.vet` | `go1.27.4` | `success_without_findings` | 1 | 1 | 0 | 0 | `analyzed_clean=1` |
| `go-vet.m02` | `go.vet` | `go1.27.4` | `configuration_error` | 1 | 미확정 | 0 | 0 | `-` |
| `jscpd` | `any.jscpd` | `미확정` | `not_run` | 3 | 0 | 0 | 0 | `-` |
| `lizard` | `any.lizard` | `1.26.1` | `success_without_findings` | 3 | 3 | 0 | 0 | `analyzed=3` |
| `staticcheck` | `go.staticcheck` | `2026.2.2` | `success_with_findings` | 1 | 1 | 0 | 0 | `analyzed=1` |
| `staticcheck.m01` | `go.staticcheck` | `2026.2.2` | `success_with_findings` | 1 | 1 | 0 | 0 | `analyzed=1` |
| `staticcheck.m02` | `go.staticcheck` | `2026.2.2` | `execution_error` | 1 | 미확정 | 0 | 0 | `-` |

### 작업 상태

| task | 필수 | 상태 | 사유 |
|---|---|---|---|
| `deps.go` | false | `succeeded` | `없음` |
| `deps.go.m01` | false | `failed` | `registry_unreachable` |
| `deps.go.m02` | false | `skipped_policy` | `no_dependencies` |
| `docs.discover` | true | `succeeded` | `없음` |
| `env.discover` | true | `succeeded` | `없음` |
| `gitleaks` | false | `succeeded` | `없음` |
| `go-vet` | true | `succeeded` | `없음` |
| `go-vet.m01` | true | `succeeded` | `없음` |
| `go-vet.m02` | true | `failed` | `toolchain_unavailable` |
| `jscpd` | false | `unsupported` | `toolchain_not_configured` |
| `lizard` | false | `succeeded` | `없음` |
| `review.documents` | true | `succeeded` | `없음` |
| `review.environment` | false | `succeeded` | `없음` |
| `review.interpret` | false | `succeeded` | `없음` |
| `review.investigate` | true | `succeeded` | `없음` |
| `review.verify` | true | `succeeded` | `없음` |
| `staticcheck` | false | `succeeded` | `없음` |
| `staticcheck.m01` | false | `succeeded` | `없음` |
| `staticcheck.m02` | false | `failed` | `toolchain_unavailable` |

### 실행 기록

| 실행 | 상태 | exit | 정리 | argv | cwd | stdout | stderr |
|---|---|---|---|---|---|---|---|
| `deps.go.m01/go.download` | `exited` | `1` | `confirmed` | `go mod download -json -modfile=$SCRATCH/deps/deps.go.m01/modfile/go.mod` | `.` | `0195f3a0-0000-7000-8000-000000000520` | `0195f3a0-0000-7000-8000-000000000521` |
| `deps.go.m01/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json $SCRATCH/deps/deps.go.m01/modfile/go.mod` | `.` | `0195f3a0-0000-7000-8000-00000000051e` | `0195f3a0-0000-7000-8000-00000000051f` |
| `deps.go.m01/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-00000000051c` | `0195f3a0-0000-7000-8000-00000000051d` |
| `deps.go.m02/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json $SCRATCH/deps/deps.go.m02/modfile/go.mod` | `.` | `0195f3a0-0000-7000-8000-000000000526` | `0195f3a0-0000-7000-8000-000000000527` |
| `deps.go.m02/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-000000000524` | `0195f3a0-0000-7000-8000-000000000525` |
| `deps.go/go.download` | `exited` | `0` | `confirmed` | `go mod download -json -modfile=$SCRATCH/deps/deps.go/modfile/go.mod` | `.` | `0195f3a0-0000-7000-8000-000000000518` | `0195f3a0-0000-7000-8000-000000000519` |
| `deps.go/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json $SCRATCH/deps/deps.go/modfile/go.mod` | `.` | `0195f3a0-0000-7000-8000-000000000516` | `0195f3a0-0000-7000-8000-000000000517` |
| `deps.go/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-000000000514` | `0195f3a0-0000-7000-8000-000000000515` |
| `env.discover/gitleaks.version` | `exited` | `0` | `confirmed` | `gitleaks version` | `.` | `0195f3a0-0000-7000-8000-00000000050a` | `0195f3a0-0000-7000-8000-00000000050b` |
| `env.discover/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-000000000508` | `0195f3a0-0000-7000-8000-000000000509` |
| `env.discover/lizard.version` | `exited` | `0` | `confirmed` | `lizard version` | `.` | `0195f3a0-0000-7000-8000-00000000050c` | `0195f3a0-0000-7000-8000-00000000050d` |
| `env.discover/staticcheck.version` | `exited` | `0` | `confirmed` | `staticcheck version` | `.` | `0195f3a0-0000-7000-8000-00000000050e` | `0195f3a0-0000-7000-8000-00000000050f` |
| `gitleaks/gitleaks.run` | `exited` | `1` | `confirmed` | `gitleaks .` | `.` | `0195f3a0-0000-7000-8000-00000000055e` | `0195f3a0-0000-7000-8000-00000000055f` |
| `gitleaks/gitleaks.version` | `exited` | `0` | `confirmed` | `gitleaks --version` | `.` | `0195f3a0-0000-7000-8000-00000000055c` | `0195f3a0-0000-7000-8000-00000000055d` |
| `go-vet.m01/go.list` | `exited` | `0` | `confirmed` | `go list -e -json ./...` | `.` | `0195f3a0-0000-7000-8000-000000000538` | `0195f3a0-0000-7000-8000-000000000539` |
| `go-vet.m01/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json` | `.` | `0195f3a0-0000-7000-8000-000000000536` | `0195f3a0-0000-7000-8000-000000000537` |
| `go-vet.m01/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-000000000534` | `0195f3a0-0000-7000-8000-000000000535` |
| `go-vet.m01/go.vet` | `exited` | `0` | `confirmed` | `go vet -json ./...` | `.` | `0195f3a0-0000-7000-8000-00000000053a` | `0195f3a0-0000-7000-8000-00000000053b` |
| `go-vet.m02/go.modedit` | `exited` | `1` | `confirmed` | `go mod edit -json` | `.` | `0195f3a0-0000-7000-8000-000000000540` | `0195f3a0-0000-7000-8000-000000000541` |
| `go-vet.m02/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-00000000053e` | `0195f3a0-0000-7000-8000-00000000053f` |
| `go-vet/go.list` | `exited` | `0` | `confirmed` | `go list -e -json ./...` | `.` | `0195f3a0-0000-7000-8000-00000000052e` | `0195f3a0-0000-7000-8000-00000000052f` |
| `go-vet/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json` | `.` | `0195f3a0-0000-7000-8000-00000000052c` | `0195f3a0-0000-7000-8000-00000000052d` |
| `go-vet/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-00000000052a` | `0195f3a0-0000-7000-8000-00000000052b` |
| `go-vet/go.vet` | `exited` | `0` | `confirmed` | `go vet -json ./...` | `.` | `0195f3a0-0000-7000-8000-000000000530` | `0195f3a0-0000-7000-8000-000000000531` |
| `lizard/lizard.run` | `exited` | `0` | `confirmed` | `lizard .` | `.` | `0195f3a0-0000-7000-8000-000000000556` | `0195f3a0-0000-7000-8000-000000000557` |
| `lizard/lizard.version` | `exited` | `0` | `confirmed` | `lizard --version` | `.` | `0195f3a0-0000-7000-8000-000000000554` | `0195f3a0-0000-7000-8000-000000000555` |
| `staticcheck.m01/staticcheck.run` | `exited` | `1` | `confirmed` | `staticcheck ./...` | `.` | `0195f3a0-0000-7000-8000-00000000054c` | `0195f3a0-0000-7000-8000-00000000054d` |
| `staticcheck.m01/staticcheck.version` | `exited` | `0` | `confirmed` | `staticcheck --version` | `.` | `0195f3a0-0000-7000-8000-00000000054a` | `0195f3a0-0000-7000-8000-00000000054b` |
| `staticcheck.m02/staticcheck.run` | `exited` | `1` | `confirmed` | `staticcheck ./...` | `.` | `0195f3a0-0000-7000-8000-000000000550` | `0195f3a0-0000-7000-8000-000000000551` |
| `staticcheck/staticcheck.run` | `exited` | `1` | `confirmed` | `staticcheck ./...` | `.` | `0195f3a0-0000-7000-8000-000000000546` | `0195f3a0-0000-7000-8000-000000000547` |
| `staticcheck/staticcheck.version` | `exited` | `0` | `confirmed` | `staticcheck --version` | `.` | `0195f3a0-0000-7000-8000-000000000544` | `0195f3a0-0000-7000-8000-000000000545` |

### 산출물

| id | task | role | media | sha256 | bytes | decoder |
|---|---|---|---|---|---|---|
| `0195f3a0-0000-7000-8000-000000000506` | `-` | `source_manifest` | `application/vnd.codeye.source-manifest+json` | `bb0ed61201811310aa90a4c749482ce1750da7f2afc35103e36bca82f9c624c6` | 2081 | `-` |
| `0195f3a0-0000-7000-8000-00000000056b` | `-` | `result_manifest` | `application/vnd.codeye.result-manifest+json` | `d456675cbe8b25fec6f1612bfdbe49ed98225e252a7218e0b86d82b9bb0e36bd` | 21210 | `-` |
| `0195f3a0-0000-7000-8000-000000000532` | `go-vet` | `observations` | `application/vnd.codeye.observations+json` | `4e4af1e70e5412fcd5bb003c557472c33c1996e277a2d3e2d71c5bc88bdfdd40` | 3006 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=go_vet_observations` |
| `0195f3a0-0000-7000-8000-00000000053c` | `go-vet.m01` | `observations` | `application/vnd.codeye.observations+json` | `307cbc6cbdb30b5648e22d7132978f21d3ac0aa496fdf547d8e3d4f816c283c5` | 3038 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=go_vet_observations` |
| `0195f3a0-0000-7000-8000-000000000542` | `go-vet.m02` | `observations` | `application/vnd.codeye.observations+json` | `16adcaf2411bd6ec09ad14373f9aeb036adef56e485289c372fbbacd95d83a6f` | 1951 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=go_vet_observations` |
| `0195f3a0-0000-7000-8000-000000000548` | `staticcheck` | `observations` | `application/vnd.codeye.observations+json` | `704a87955bdfb148d439986203f4341847847f68608d5e09bcd15da3dccc6fee` | 2958 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-00000000054e` | `staticcheck.m01` | `observations` | `application/vnd.codeye.observations+json` | `842311c73839ff786e0d24b7d5f85c0b6314b202d650fabbb0cb4761b5aec75a` | 2943 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-000000000552` | `staticcheck.m02` | `observations` | `application/vnd.codeye.observations+json` | `3cee2d6608fc6b66fbe1de472c0e6e236c503e6f6dd574004b26e4295f7e6221` | 1744 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-000000000558` | `lizard` | `observations` | `application/vnd.codeye.observations+json` | `611f7f8b0c395c28d961a942293ab082360f6a18cab1b3176ed115aee609c8f2` | 3032 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-00000000055a` | `jscpd` | `observations` | `application/vnd.codeye.observations+json` | `52d6d4a20296cb541824d71d74dae422d14b1f1ab4d6d1314814f2e614c40e78` | 1145 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-000000000560` | `gitleaks` | `observations` | `application/vnd.codeye.observations+json` | `6d20a6befabe55b18c4149123d359648315f812cdf1bbfeb871ba2e768d2652c` | 3085 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-000000000508` | `env.discover` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-00000000050a` | `env.discover` | `stdout` | `text/plain` | `c9fd9ccb6682c54b5fcb0363757b6c6873564e7c067f70b3b5581b611528b9f4` | 7 | `-` |
| `0195f3a0-0000-7000-8000-00000000050c` | `env.discover` | `stdout` | `text/plain` | `55f4221aa0e36b70cbfc65ccc26728d31f6510eb1ead635badc007cdb5cd2bed` | 7 | `-` |
| `0195f3a0-0000-7000-8000-00000000050e` | `env.discover` | `stdout` | `text/plain` | `53199309ddfb6ea7ecdc24c7731f535dc3d6bba2e8f053b895b78b967b84c534` | 9 | `-` |
| `0195f3a0-0000-7000-8000-000000000514` | `deps.go` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-000000000516` | `deps.go` | `stdout` | `text/plain` | `b2dc726d2aaf6a7480723829b0d24e769b6c1727935a4de86ad4a1311c50658d` | 152 | `-` |
| `0195f3a0-0000-7000-8000-000000000518` | `deps.go` | `stdout` | `text/plain` | `d03272385be3935eb23249110fb03dad51fc10451492dbac51de73ecef9f5ed2` | 116 | `-` |
| `0195f3a0-0000-7000-8000-00000000051c` | `deps.go.m01` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-00000000051e` | `deps.go.m01` | `stdout` | `text/plain` | `2ff58078eb7c706b11aa839c286a5f16886c29083e1d094a49415b0fb1f0113b` | 111 | `-` |
| `0195f3a0-0000-7000-8000-000000000520` | `deps.go.m01` | `stdout` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000524` | `deps.go.m02` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-000000000526` | `deps.go.m02` | `stdout` | `text/plain` | `8ebc58eab026def3c7f211b05555f86327f95bdf178880cc45948dfef39ffe60` | 44 | `-` |
| `0195f3a0-0000-7000-8000-00000000052a` | `go-vet` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-00000000052c` | `go-vet` | `stdout` | `text/plain` | `aa6bffa862888ccc9390067c138809fc32c01173bc6f3ebc5942b87dafc4d734` | 39 | `-` |
| `0195f3a0-0000-7000-8000-00000000052e` | `go-vet` | `stdout` | `text/plain` | `8e5787da2db5f5eddea3953d4026e5d8d572ca0dcde653173be66d47d4057fd2` | 34 | `-` |
| `0195f3a0-0000-7000-8000-000000000530` | `go-vet` | `stdout` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000534` | `go-vet.m01` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-000000000536` | `go-vet.m01` | `stdout` | `text/plain` | `cb75f384fd85b5bdabd7e758604aa735350393b0cb62e7495e06682892b33d8f` | 45 | `-` |
| `0195f3a0-0000-7000-8000-000000000538` | `go-vet.m01` | `stdout` | `text/plain` | `3ccb0911aca4513dd8004c88bd4bc213aa470e15db012cfe08688a9b311cfa4a` | 40 | `-` |
| `0195f3a0-0000-7000-8000-00000000053a` | `go-vet.m01` | `stdout` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000053e` | `go-vet.m02` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-000000000540` | `go-vet.m02` | `stdout` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000544` | `staticcheck` | `stdout` | `text/plain` | `53199309ddfb6ea7ecdc24c7731f535dc3d6bba2e8f053b895b78b967b84c534` | 9 | `-` |
| `0195f3a0-0000-7000-8000-000000000546` | `staticcheck` | `stdout` | `text/plain` | `1ec0246bca5d215b0838416d77cf2e0ae0151879d51bdc8802cc823fb8eaeb48` | 122 | `-` |
| `0195f3a0-0000-7000-8000-00000000054a` | `staticcheck.m01` | `stdout` | `text/plain` | `53199309ddfb6ea7ecdc24c7731f535dc3d6bba2e8f053b895b78b967b84c534` | 9 | `-` |
| `0195f3a0-0000-7000-8000-00000000054c` | `staticcheck.m01` | `stdout` | `text/plain` | `6b74f001b8c319d58fd8fa477f34ba1824f59b15a3d99a405e24e227bec52fb3` | 87 | `-` |
| `0195f3a0-0000-7000-8000-000000000550` | `staticcheck.m02` | `stdout` | `text/plain` | `0818a3e891c9d6a4070fb66634783fc2577e0e27a5b2ff870e777a75e6aef4bb` | 51 | `-` |
| `0195f3a0-0000-7000-8000-000000000554` | `lizard` | `stdout` | `text/plain` | `55f4221aa0e36b70cbfc65ccc26728d31f6510eb1ead635badc007cdb5cd2bed` | 7 | `-` |
| `0195f3a0-0000-7000-8000-000000000556` | `lizard` | `stdout` | `text/plain` | `d28d60f3d8d59689726736a9bf691db00767456165b64ed57ea316074cf8924d` | 96 | `-` |
| `0195f3a0-0000-7000-8000-00000000055c` | `gitleaks` | `stdout` | `text/plain` | `c9fd9ccb6682c54b5fcb0363757b6c6873564e7c067f70b3b5581b611528b9f4` | 7 | `-` |
| `0195f3a0-0000-7000-8000-00000000055e` | `gitleaks` | `stdout` | `text/plain` | `5a68577b15a92abaf20ded4f74948f63d7b4c801e1f8b52c797cae8a29b8eb4a` | 102 | `-` |
| `0195f3a0-0000-7000-8000-000000000509` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000050b` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000050d` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000050f` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000515` | `deps.go` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000517` | `deps.go` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000519` | `deps.go` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000051d` | `deps.go.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000051f` | `deps.go.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000521` | `deps.go.m01` | `stderr` | `text/plain` | `4524b46961969a896e597bfa8896909862ffea253df63b8195be33b50b04e32d` | 156 | `-` |
| `0195f3a0-0000-7000-8000-000000000525` | `deps.go.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000527` | `deps.go.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000052b` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000052d` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000052f` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000531` | `go-vet` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000535` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000537` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000539` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000053b` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000053f` | `go-vet.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000541` | `go-vet.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000545` | `staticcheck` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000547` | `staticcheck` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000054b` | `staticcheck.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000054d` | `staticcheck.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000551` | `staticcheck.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000555` | `lizard` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000557` | `lizard` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000055d` | `gitleaks` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000055f` | `gitleaks` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000510` | `env.discover` | `other` | `application/vnd.codeye.environment+json` | `41748e673d90e5c025db06ab613f958fc0ab5d21581172f25b369906a5861fd4` | 2944 | `application/vnd.codeye.environment+json;schema_version=0.1` |
| `0195f3a0-0000-7000-8000-000000000512` | `docs.discover` | `other` | `application/vnd.codeye.environment+json` | `6030447c3afbf990a6f7c9259fc09afa707a54df799f9bf80ec775378ac210db` | 2319 | `application/vnd.codeye.environment+json;schema_version=0.1` |
| `0195f3a0-0000-7000-8000-00000000051a` | `deps.go` | `other` | `application/vnd.codeye.dependencies+json` | `5eb6ea88bc6bba3746ab683386c11658ec9cf8e2b6ffb7e261f17fd6fbddbf05` | 590 | `application/vnd.codeye.dependencies+json;schema_version=0.1;kind=dependencies` |
| `0195f3a0-0000-7000-8000-000000000522` | `deps.go.m01` | `other` | `application/vnd.codeye.dependencies+json` | `970617e302339b2315d2cf52a6d3205d2a20a5689307b0511b6af2e557731af9` | 618 | `application/vnd.codeye.dependencies+json;schema_version=0.1;kind=dependencies` |
| `0195f3a0-0000-7000-8000-000000000528` | `deps.go.m02` | `other` | `application/vnd.codeye.dependencies+json` | `d9e7058fcb668518d1e349b8769dae92fafc28f11e3d6e70e8d8f4c1dbe8ae03` | 613 | `application/vnd.codeye.dependencies+json;schema_version=0.1;kind=dependencies` |
| `0195f3a0-0000-7000-8000-000000000562` | `review.environment` | `other` | `application/vnd.codeye.review+json` | `9b61f7da90fc86c8f4be14381e89e789879208cb3aeea670fc66d1ef9e46a637` | 1578 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-000000000564` | `review.documents` | `other` | `application/vnd.codeye.review+json` | `63a59cca268c817cf292534145a6c06c6c18c046eaa25ade78502529c0312d7e` | 2461 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-000000000566` | `review.investigate` | `other` | `application/vnd.codeye.review+json` | `1955cffd011417714ccb34a320d8aa51d35eeefcf6a83800103e0eff60531cae` | 2381 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-000000000568` | `review.interpret` | `other` | `application/vnd.codeye.review+json` | `c46701518af1e8b5c57fdc1d9dca9fc1dd1c0f61b688864352a7ffb9426de0d6` | 3392 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-00000000056a` | `review.verify` | `other` | `application/vnd.codeye.review+json` | `eb32dbc4688a24f76c4321e71a70099d5f478cfb14c1d8133939eefd85c72461` | 4672 | `application/vnd.codeye.review+json;schema_version=0.2` |

### 한계와 공백

- caller\_expansion\_go\_only
- caller\_expansion\_package\_functions\_only
- controller는 snapshot 원본을 보관하지 않아 코드 발췌를 넣지 않았다\.
- files\_outside\_review\_context
- no\_callers\_found
- 필수 작업 중 성공하지 않은 작업이 있다\.
- 비밀값이 탐지된 파일은 모델 입력에서 뺐다\.
- tools\_outside\_interpret\_input

digest sha256: `7ec47130f4fb423cc61965d15ec196a2684e7902ba27870cdae61582cb8d4b04`

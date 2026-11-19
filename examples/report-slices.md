# CODEYE 분석 보고서

> **합성 예시.** 아래는 `fixtures/modules/slices/input`에 대해 CODEYE의 탐색·검토 역할과 모듈 단위 검토 분할을 호출 상한 15로 실제로 실행하고 보고서 builder가 만든 출력이다. 모델 응답은 미리 정한 스크립트(작업마다 호출 1회)이며 모델을 부르지 않았다. 비밀값 탐지는 탐지 없는 합성 결과이고 정적 분석 도구는 실행하지 않았다. 사용자 프로젝트의 결함 발견 결과가 아니다. 기계 판독 원본은 [report-slices.json](report-slices.json)이다.

| 항목 | 값 |
|---|---|
| report | `0195f3a0-0000-7000-8000-000000000604` rev `1` |
| digest | `507331f68342` |
| status | `partial` |
| quality\_verdict | `inconclusive` 품질 판정은 안전 인증이 아니다\. |
| run | `0195f3a0-0000-7000-8000-000000000601` |
| attempt | `0195f3a0-0000-7000-8000-000000000602` #`1` epoch `1` |
| snapshot | `0195f3a0-0000-7000-8000-000000000603` manifest `22d1b9e3af62` |
| profile | `deep-review` |
| created\_at | `2026-11-18T00:10:00Z` |
| export | 코드 포함 내보내기\(권한 있는 사용자 전용\) |

> 이 보고서는 부분 결과다\. 필수 작업이나 AI 검토가 수행·검증되지 않았다\.

## 종합

실행 상태는 부분 결과, 품질 판정은 판단 보류\: 확인하지 못한 범위나 미확정 후보가 남아 있다\.

| 항목 | 수 |
|---|---|
| 원시 도구 진단 | 0 |
| 위치 미확인 진단 | 0 |
| 항목 | 4 |
| 조치 항목\(supported/reproduced\) | 0 |
| 미확정 후보\(unreviewed/inconclusive\) | 4 |
| 기각\(refuted\) | 0 |

### 분석 종류별 상태

| 분석 | 상태 | 핵심 수 |
|---|---|---|
| 환경 분석 | ✓ 성공 | 범위 안 모듈 4 · 선언 요구 4 · 충족 2 · 불일치 0 · 판단 불가 2 · 모델 해석 메모 2 |
| 문서 분석 | ✓ 성공 | 발견 문서 1 · 읽은 문서 1 · 읽지 않은 문서 0 · 문서 없는 모듈 0 · 계약 1 · 충돌 0 · 질문 0 · 지침 메모 0 |
| 정적 분석 | △ 일부 수행 | 분석 작업 5 · 성공 3 · 성공하지 않음 2 · 진단 0 · 발견 0 · 조치 0 · 미확정 0 |
| 구조·중복 지표 | · 해당 작업 없음 | 측정 작업 0 · 임계 초과 측정 0 · 지표 종류 0 |
| 비밀정보 | ✓ 성공 | 탐지 작업 1 · 탐지 0 · 파일 0 |
| 코드 분석\(모델 검토\) | △ 일부 수행 | 모델 호출 10 · 모델에 보낸 파일\(검증 범위 아님\) 23 · 모델에 보내지 않은 파일 16 · 발견 4 · 조치 0 · 미확정 4 · 기각 0 |
| 분석 공백 | \- | 공백 12 |

### 먼저 볼 것

- 검토 대상 코드·테스트 파일 16개를 모델에 보내지 못했다\. 코드 분석\(모델 검토\) 절
- 확인되지 않은 후보 4개가 남아 있다\. 코드 분석\(모델 검토\) 절
- 분석하지 못했거나 일부만 분석한 범위가 12건 있다\. 분석 공백 절

### 우선 확인할 발견

- 설정 주소 검증 누락 — inconclusive / medium `review_investigate_1`
- 설정 주소 검증 누락 — inconclusive / medium `review_s001_investigate_1`
- 설정 주소 검증 누락 — inconclusive / medium `review_s002_investigate_1`
- 설정 주소 검증 누락 — inconclusive / medium `review_s003_investigate_1`

## 환경 분석

상태\: ✓ 성공 · 작업 2개 중 2개 성공

탐색 산출물을 검증해 사용했다\.

| 항목 | 수 |
|---|---|
| 범위 안 모듈 | 4 |
| 선언 요구 | 4 |
| 충족 | 2 |
| 불일치 | 0 |
| 판단 불가 | 2 |
| 모델 해석 메모 | 2 |

### 모듈

| 모듈 | 생태계 | 경로 | 범위 |
|---|---|---|---|
| `go:api` | `go` | `api` | 범위 안 |
| `go:store` | `go` | `store` | 범위 안 |
| `javascript:web` | `javascript` | `web` | 범위 안 |
| `python:worker` | `python` | `worker` | 범위 안 |

### 선언된 요구

| 모듈 | 도구 | 요구 | 비교 | 출처 |
|---|---|---|---|---|
| `javascript:web` | `node` | `>=20` | `minimum` | `web/package.json:4` |
| `go:api` | `go` | `1.23` | `minimum` | `api/go.mod:3` |
| `go:store` | `go` | `1.23` | `minimum` | `store/go.mod:3` |
| `python:worker` | `python` | `>=3.11` | `minimum` | `worker/pyproject.toml:3` |

### 노드 관측\(런타임\)

| 도구 | 상태 | 버전 | 세부 |
|---|---|---|---|
| `go` | 관측 | `1.27.4` | `-` |
| `node` | 확인 안 함 | `-` | `-` |
| `python` | 확인 안 함 | `-` | `-` |

### 모듈별 판정

| 모듈 | 도구 | 요구 ID | 상태 | 이유 |
|---|---|---|---|---|
| `go:api` | `go` | `go_directive@api/go.mod` | ✓ 충족 | `-` |
| `go:store` | `go` | `go_directive@store/go.mod` | ✓ 충족 | `-` |
| `javascript:web` | `node` | `engines_node@web/package.json` | ? 판단 불가 | `not_observed` |
| `python:worker` | `python` | `requires_python@worker/pyproject.toml` | ? 판단 불가 | `not_observed` |

### 분석 도구 관측

| 도구 | 분석기 | 상태 | 버전 | 세부 |
|---|---|---|---|---|
| `gitleaks` | `any.gitleaks` | 관측 | `8.30.1` | `-` |
| `jscpd` | `any.jscpd` | 확인 안 함 | `-` | `-` |
| `lizard` | `any.lizard` | 관측 | `1.26.1` | `-` |
| `staticcheck` | `go.staticcheck` | 관측 | `2026.2.2` | `-` |

### 탐색 한계

- `tool_not_probed`

### 환경 메모

> 모델 해석 — review\.environment 역할의 수락된 기록에서 가져온 모델 서술이다\. 도구나 실행으로 확인한 사실이 아니다\. 줄 끝의 근거 ID는 이 절의 근거 표에 있다\.

- javascript\:web\: 영향 — a가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가 / 여전히 확인 가능 — b가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가 / 필요한 관측 — c가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가 `ev_6081e869aacc33711a9b4f0d`
- python\:worker\: 영향 — a가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가 / 여전히 확인 가능 — b가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가 / 필요한 관측 — c가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가가 `ev_b360495bff3f00f683c6df1a`

### 근거

| 근거 ID | 종류 | 위치 | blob sha256 |
|---|---|---|---|
| `ev_6081e869aacc33711a9b4f0d` | `manifest` | `web/package.json:4-4` | `424d740d3aa6` |
| `ev_b360495bff3f00f683c6df1a` | `manifest` | `worker/pyproject.toml:3-3` | `5390cff5df26` |

## 문서 분석

상태\: ✓ 성공 · 작업 2개 중 2개 성공

탐색 산출물을 검증해 사용했다\.

읽기 예산\: 문서당 32768바이트, 전체 262144바이트, 최대 1000개\. 이번에 읽은 양은 168바이트다\.

| 항목 | 수 |
|---|---|
| 발견 문서 | 1 |
| 읽은 문서 | 1 |
| 읽지 않은 문서 | 0 |
| 문서 없는 모듈 | 0 |
| 계약 | 1 |
| 충돌 | 0 |
| 질문 | 0 |
| 지침 메모 | 0 |

### 발견한 문서

| 경로 | 종류 | 적용 범위 | 상태 | 이유 | 바이트 | 줄 |
|---|---|---|---|---|---|---|
| `README.md` | `readme` | `.` | ✓ 읽음 | `-` | 168 | 4 |

### 모듈별 문서

| 모듈 | 상태 | 자체 문서 | 상위에서 이어받은 문서 |
|---|---|---|---|
| `go:api` | 상위 문서만 있음 | `-` | `README.md` |
| `go:store` | 상위 문서만 있음 | `-` | `README.md` |
| `javascript:web` | 상위 문서만 있음 | `-` | `README.md` |
| `python:worker` | 상위 문서만 있음 | `-` | `README.md` |

> 모델 해석 — review\.documents 역할의 수락된 기록에서 가져온 모델 서술이다\. 도구나 실행으로 확인한 사실이 아니다\. 줄 끝의 근거 ID는 이 절의 근거 표에 있다\.

### 문서가 정한 계약

- \[\.\] 서비스는 설정을 읽어 요청을 처리한다\. `ev_54f17c93b602e2fab96fa1c3`

### 안내 사이의 충돌

없음

### 확인할 질문

없음

### 지침 메모

없음

### 근거

| 근거 ID | 종류 | 위치 | blob sha256 |
|---|---|---|---|
| `ev_54f17c93b602e2fab96fa1c3` | `document` | `README.md:1-4` | `80743a2e9629` |

## 정적 분석

상태\: △ 일부 수행 · 작업 5개 중 3개 성공

도구가 보고한 진단과 그 발견이다\. 진단이 0건인 성공은 결함이 없다는 뜻이 아니다\. 성공하지 않은 작업의 범위는 분석되지 않았다\.

| 항목 | 수 |
|---|---|
| 분석 작업 | 5 |
| 성공 | 3 |
| 성공하지 않음 | 2 |
| 진단 | 0 |
| 발견 | 0 |
| 조치 | 0 |
| 미확정 | 0 |

### 언어\: go

| 작업 | 도구 | 필수 | 상태 | 결과 문서 | 버전 | 범위/처리 파일 | 진단 | 규칙별 수 |
|---|---|---|---|---|---|---|---|---|
| `go-vet.m01` | `go.vet (go)` | 필수 | ✓ 성공 | 확인함 · success\_without\_findings | `go1.27.4` | 3 / 3 | 0 | `-` |
| `go-vet.m02` | `go.vet (go)` | 필수 | ✓ 성공 | 확인함 · success\_without\_findings | `go1.27.4` | 30 / 30 | 0 | `-` |

### 언어\: javascript

| 작업 | 도구 | 필수 | 상태 | 결과 문서 | 버전 | 범위/처리 파일 | 진단 | 규칙별 수 |
|---|---|---|---|---|---|---|---|---|
| `pack-gap.javascript` | `javascript.none (none)` | 선택 | – 실행 안 함\(미지원\) · 범위의 이 언어를 맡을 분석기가 이 빌드에 없다\. | 확인함 · not\_run | `-` | 3 / 0 | 실행하지 않음 | `-` |

### 언어\: python

| 작업 | 도구 | 필수 | 상태 | 결과 문서 | 버전 | 범위/처리 파일 | 진단 | 규칙별 수 |
|---|---|---|---|---|---|---|---|---|
| `pack-gap.python` | `python.none (none)` | 선택 | – 실행 안 함\(미지원\) · 범위의 이 언어를 맡을 분석기가 이 빌드에 없다\. | 확인함 · not\_run | `-` | 3 / 0 | 실행하지 않음 | `-` |

### 언어 무관\(지표·비밀정보 도구\)

이 도구들의 측정값과 탐지는 구조·중복 지표와 비밀정보 절에 있다\. 비밀정보 탐지의 발견은 이 절의 발견 목록에 있다\.

| 작업 | 도구 | 필수 | 상태 | 결과 문서 | 버전 | 범위/처리 파일 | 진단 | 규칙별 수 |
|---|---|---|---|---|---|---|---|---|
| `gitleaks` | `any.gitleaks (gitleaks)` | 선택 | ✓ 성공 | 확인함 · success\_without\_findings | `8.30.1` | 44 / 44 | 탐지 0 | `-` |

### 진단

이 절에 진단이 없다\. 위 표의 작업 상태와 분석 공백을 확인한다\.

### 발견

이 절에 발견이 없다\. 결함이 없다는 뜻이 아니다\.

## 구조·중복 지표

상태\: · 해당 작업 없음 · 작업 0개 중 0개 성공

구조 지표는 임계값을 넘은 측정값이며 결함이 아니다\. 발견을 만들지 않고 품질 판정에 쓰지 않는다\.

| 항목 | 수 |
|---|---|
| 측정 작업 | 0 |
| 임계 초과 측정 | 0 |
| 지표 종류 | 0 |

### 측정 작업

이 분석 작업이 계획되지 않았다

### 지표별 요약

임계를 넘은 측정값이 없다\. 측정 작업이 성공하지 않았다면 측정하지 않은 것이다\.

## 비밀정보

상태\: ✓ 성공 · 작업 1개 중 1개 성공

비밀정보 탐지는 위치와 규칙만 기록한다\. 값과 그 일부·hash는 저장하지 않는다\.

완료된 탐지로 모델 입력을 걸렀고 뺀 파일은 없다\.

| 항목 | 수 |
|---|---|
| 탐지 작업 | 1 |
| 탐지 | 0 |
| 파일 | 0 |

### 탐지 작업

| 작업 | 상태 | 이유 |
|---|---|---|
| `gitleaks` | ✓ 성공 | \- |

### 탐지 위치

탐지가 없다\. 탐지 작업이 성공하지 않았다면 검사하지 않은 것이다\.

## 코드 분석\(모델 검토\)

상태\: △ 일부 수행 · 작업 22개 중 9개 성공

검토 팩\: review\-r1@2\.0\.0\(역할 팩\)

모델 호출 10회, 그중 수락 10회\(환경·문서 역할 호출 포함, 전체 합계\)\.

모델 요청 크기 합계 205632바이트\(지침과 입력을 합친 요청 크기이며 소스 크기가 아니다\)\.

모델에 보낸 파일 26개 / snapshot 파일 44개\.

| 항목 | 수 |
|---|---|
| 모델 호출 | 10 |
| 모델에 보낸 파일\(검증 범위 아님\) | 23 |
| 모델에 보내지 않은 파일 | 16 |
| 발견 | 4 |
| 조치 | 0 |
| 미확정 | 4 |
| 기각 | 0 |

### 코드 분석 역할

| 역할 | 작업 상태 | 결과 문서 | 이유 |
|---|---|---|---|
| `review.investigate` | ✓ 성공 | `accepted` | `-` |
| `review.interpret` | – 정책 제외 | `skipped` | `no_tool_observations` |
| `review.verify` | ✓ 성공 | `accepted` | `-` |

### 모듈별 검토 장부

모듈마다 검토 대상 코드·테스트 파일 중 모델에 보낸 파일과 보내지 못한 이유를 센다\. 모델에 보낸 범위는 검증된 범위가 아니다\. 모델이 본 파일에도 후보로 지목되지 않은 결함이 있을 수 있다\.

장부 상태\: 일부 slice만 모델에 보냈다\.

검토 범위 작업 review\.scope\: ✓ 성공\.

모델 호출 10회 / 상한 15회\.

모델 요청 크기 합계 205632바이트, 요청당 상한 40000바이트\.

### 모듈

| 모듈 | 모듈 탐색 | 대상 | 모델에 보냄 | 보낸 바이트 | 잘림 | 비밀값 제외 | 읽을 수 없음 | 상한 초과 | 미계획 | 그 밖의 미전송 | 검증 역할까지 마침 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `go:api` | 탐색됨 | 3 | 2 | 678 | 0 | 0 | 0 | 1 | 0 | 0 | 2 |
| `go:store` | 탐색됨 | 30 | 17 | 20815 | 17 | 0 | 0 | 13 | 0 | 0 | 17 |
| `javascript:web` | 탐색됨 | 3 | 2 | 394 | 0 | 0 | 0 | 1 | 0 | 0 | 2 |
| `python:worker` | 탐색됨 | 3 | 2 | 546 | 0 | 0 | 0 | 1 | 0 | 0 | 2 |

### slice 작업

| 작업 | 상태 | 이유 | 모듈 |
|---|---|---|---|
| `review.investigate.s001` | ✓ 성공 | \- | `go:store` |
| `review.investigate.s002` | ✓ 성공 | \- | `go:store` |
| `review.investigate.s003` | ✓ 성공 | \- | `go:store` |
| `review.investigate.s004` | △ 예산 소진 | attempt의 모델 호출 상한에 닿아 실행하지 않았다\(호출 없음\)\. | `go:store` |
| `review.investigate.s005` | △ 예산 소진 | attempt의 모델 호출 상한에 닿아 실행하지 않았다\(호출 없음\)\. | `go:store` |
| `review.investigate.s006` | △ 예산 소진 | attempt의 모델 호출 상한에 닿아 실행하지 않았다\(호출 없음\)\. | `go:api, go:store, javascript:web, python:worker` |
| `review.verify.s001` | ✓ 성공 | \- | `go:store` |
| `review.verify.s002` | ✓ 성공 | \- | `go:store` |
| `review.verify.s003` | ✓ 성공 | \- | `go:store` |
| `review.verify.s004` | △ 예산 소진 | attempt의 모델 호출 상한에 닿아 실행하지 않았다\(호출 없음\)\. | `go:store` |
| `review.verify.s005` | △ 예산 소진 | attempt의 모델 호출 상한에 닿아 실행하지 않았다\(호출 없음\)\. | `go:store` |
| `review.verify.s006` | △ 예산 소진 | attempt의 모델 호출 상한에 닿아 실행하지 않았다\(호출 없음\)\. | `go:api, go:store, javascript:web, python:worker` |

### 모델 검토 발견

#### 설정 주소 검증 누락

- id: `review_investigate_1`
- category / severity: `error_handling` / `medium` — 재시도 범위가 문서 계약과 어긋날 수 있다\.
- validation / disposition: `inconclusive` / `open`
- location: `api/main.go:1-12`
- 발생 조건 (`assumed`) 주소가 비어 있는 경우 `ev_7598f744a473d4f167c7b428`
- impact: 실패 처리 방식이 모듈 안내와 다를 수 있다\.
- claim (`inferred`): 설정 주소 검증 누락 [`ev_7598f744a473d4f167c7b428`]
- claim (`inferred`): 실패 처리 방식이 모듈 안내와 다를 수 있다\. [`ev_7598f744a473d4f167c7b428`]
- evidence `ev_7598f744a473d4f167c7b428` (`code`): 고정 스냅샷의 code 발췌 위치 `api/main.go:1 sha256 053e0c319106`
- recommendation: 주소를 검사한다\.
- verification: Forward의 호출자와 결제 경로를 읽는다\.
- limitation: Forward의 실제 호출자를 발췌에서 확인하지 못했다\.

#### 설정 주소 검증 누락

- id: `review_s001_investigate_1`
- category / severity: `error_handling` / `medium` — 재시도 범위가 문서 계약과 어긋날 수 있다\.
- validation / disposition: `inconclusive` / `open`
- location: `store/codec.go:1-50`
- 발생 조건 (`assumed`) 주소가 비어 있는 경우 `ev_79e28e56234d66d1a4784bae`
- impact: 실패 처리 방식이 모듈 안내와 다를 수 있다\.
- claim (`inferred`): 설정 주소 검증 누락 [`ev_79e28e56234d66d1a4784bae`]
- claim (`inferred`): 실패 처리 방식이 모듈 안내와 다를 수 있다\. [`ev_79e28e56234d66d1a4784bae`]
- evidence `ev_79e28e56234d66d1a4784bae` (`code`): 고정 스냅샷의 code 발췌 위치 `store/codec.go:1 sha256 93f73314c526`
- recommendation: 주소를 검사한다\.
- verification: Forward의 호출자와 결제 경로를 읽는다\.
- limitation: Forward의 실제 호출자를 발췌에서 확인하지 못했다\.

#### 설정 주소 검증 누락

- id: `review_s002_investigate_1`
- category / severity: `error_handling` / `medium` — 재시도 범위가 문서 계약과 어긋날 수 있다\.
- validation / disposition: `inconclusive` / `open`
- location: `store/evict.go:1-50`
- 발생 조건 (`assumed`) 주소가 비어 있는 경우 `ev_65c77e2ba5d5936bf32aa304`
- impact: 실패 처리 방식이 모듈 안내와 다를 수 있다\.
- claim (`inferred`): 설정 주소 검증 누락 [`ev_65c77e2ba5d5936bf32aa304`]
- claim (`inferred`): 실패 처리 방식이 모듈 안내와 다를 수 있다\. [`ev_65c77e2ba5d5936bf32aa304`]
- evidence `ev_65c77e2ba5d5936bf32aa304` (`code`): 고정 스냅샷의 code 발췌 위치 `store/evict.go:1 sha256 bb412f06b5b1`
- recommendation: 주소를 검사한다\.
- verification: Forward의 호출자와 결제 경로를 읽는다\.
- limitation: Forward의 실제 호출자를 발췌에서 확인하지 못했다\.

#### 설정 주소 검증 누락

- id: `review_s003_investigate_1`
- category / severity: `error_handling` / `medium` — 재시도 범위가 문서 계약과 어긋날 수 있다\.
- validation / disposition: `inconclusive` / `open`
- location: `store/journal.go:1-50`
- 발생 조건 (`assumed`) 주소가 비어 있는 경우 `ev_6c80c76724ad1562986f1a02`
- impact: 실패 처리 방식이 모듈 안내와 다를 수 있다\.
- claim (`inferred`): 설정 주소 검증 누락 [`ev_6c80c76724ad1562986f1a02`]
- claim (`inferred`): 실패 처리 방식이 모듈 안내와 다를 수 있다\. [`ev_6c80c76724ad1562986f1a02`]
- evidence `ev_6c80c76724ad1562986f1a02` (`code`): 고정 스냅샷의 code 발췌 위치 `store/journal.go:1 sha256 e82c4becf3a6`
- recommendation: 주소를 검사한다\.
- verification: Forward의 호출자와 결제 경로를 읽는다\.
- limitation: Forward의 실제 호출자를 발췌에서 확인하지 못했다\.

## 분석 공백

분석하지 못했거나 일부만 분석한 범위를 모은 목록이다\. 이 범위의 결과는 0건이 아니라 미확인이다\.

| 분석 | 공백 | 대상 | 세부 |
|---|---|---|---|
| 환경 분석 | 탐색 한계 | `-` | tool\_not\_probed |
| 환경 분석 | 모듈 요구를 판단할 수 없다 | `javascript:web node` | not\_observed |
| 환경 분석 | 모듈 요구를 판단할 수 없다 | `python:worker python` | not\_observed |
| 정적 분석 | 실행하지 않음\(미지원\) | `pack-gap.javascript` | 범위의 이 언어를 맡을 분석기가 이 빌드에 없다\. |
| 정적 분석 | 실행하지 않음\(미지원\) | `pack-gap.python` | 범위의 이 언어를 맡을 분석기가 이 빌드에 없다\. |
| 구조·중복 지표 | 이 분석 작업이 계획되지 않았다 | `-` | \- |
| 비밀정보 | git 이력은 검사하지 않았다 | `gitleaks` | \- |
| 코드 분석\(모델 검토\) | 호출 상한 때문에 모델에 보내지 않은 파일이 있다 | `go:api` | \- |
| 코드 분석\(모델 검토\) | 호출 상한 때문에 모델에 보내지 않은 파일이 있다 | `go:store` | \- |
| 코드 분석\(모델 검토\) | 호출 상한 때문에 모델에 보내지 않은 파일이 있다 | `javascript:web` | \- |
| 코드 분석\(모델 검토\) | 호출 상한 때문에 모델에 보내지 않은 파일이 있다 | `python:worker` | \- |
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
| `review.interpret` | `skipped_policy` | `skipped` `no_tool_observations` |
| `review.investigate.s001` | `succeeded` | `accepted` |
| `review.investigate.s002` | `succeeded` | `accepted` |
| `review.investigate.s003` | `succeeded` | `accepted` |
| `review.investigate.s004` | `budget_exhausted` | `not_succeeded` `review_budget_exhausted` |
| `review.investigate.s005` | `budget_exhausted` | `not_succeeded` `review_budget_exhausted` |
| `review.investigate.s006` | `budget_exhausted` | `not_succeeded` `review_budget_exhausted` |
| `review.investigate.s007` | `skipped_policy` | `skipped` `no_review_scope` |
| `review.investigate.s008` | `skipped_policy` | `skipped` `no_review_scope` |
| `review.investigate.s009` | `skipped_policy` | `skipped` `no_review_scope` |
| `review.scope` | `succeeded` | `result_missing` |
| `review.verify.s001` | `succeeded` | `accepted` |
| `review.verify.s002` | `succeeded` | `accepted` |
| `review.verify.s003` | `succeeded` | `accepted` |
| `review.verify.s004` | `budget_exhausted` | `not_succeeded` `review_budget_exhausted` |
| `review.verify.s005` | `budget_exhausted` | `not_succeeded` `review_budget_exhausted` |
| `review.verify.s006` | `budget_exhausted` | `not_succeeded` `review_budget_exhausted` |
| `review.verify.s007` | `skipped_policy` | `skipped` `no_review_scope` |
| `review.verify.s008` | `skipped_policy` | `skipped` `no_review_scope` |
| `review.verify.s009` | `skipped_policy` | `skipped` `no_review_scope` |

- 모델 호출 `environment` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `4ff052a3f276969278c1d1f93010103b013187d0260ecf5ce717fb9a1f7c746e`
- 모델 호출 `documents` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `d0cdbb4ee15ac315dd2958e24878be7beb9ed3df0a3619596e0cd61249e095d2`
- 모델 호출 `investigate` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `70a650d426a4355b782c8c761d7d41598dfab26ad70157f0098b14c7fd823a80`
- 모델 호출 `verify` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `20a357b9a050beb03ae3eba175c14e95880c54374c86caf17a4f8872ed00b194`
- 모델 호출 `investigate` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `70a650d426a4355b782c8c761d7d41598dfab26ad70157f0098b14c7fd823a80`
- 모델 호출 `verify` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `20a357b9a050beb03ae3eba175c14e95880c54374c86caf17a4f8872ed00b194`
- 모델 호출 `investigate` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `70a650d426a4355b782c8c761d7d41598dfab26ad70157f0098b14c7fd823a80`
- 모델 호출 `verify` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `20a357b9a050beb03ae3eba175c14e95880c54374c86caf17a4f8872ed00b194`
- 모델 호출 `investigate` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `70a650d426a4355b782c8c761d7d41598dfab26ad70157f0098b14c7fd823a80`
- 모델 호출 `verify` · `fake/scripted` · OpenCode `1.19.5` · `accepted` · 지침 `review-r1@2.0.0` `20a357b9a050beb03ae3eba175c14e95880c54374c86caf17a4f8872ed00b194`

### 분석 범위

- scope: `full`
- inventory_files: 44
- tool_processed_files: 44
- llm_exposed_files: 26
- test_status: `not_run`
- inventory 파일 수는 snapshot manifest에 포함된 모든 파일 수다\(언어 무관\)\.
- 테스트 실행 작업은 계획되지 않았다\.
- 도구가 0건을 보고한 작업이 있다\. 결함이 없다는 뜻이 아니다\.

| task | 도구 | 버전 | exit class | 범위 파일 | 처리 파일 | 제외 | 미목록 | 패키지 상태 |
|---|---|---|---|---|---|---|---|---|
| `gitleaks` | `any.gitleaks` | `8.30.1` | `success_without_findings` | 44 | 44 | 0 | 0 | `analyzed=44` |
| `go-vet.m01` | `go.vet` | `go1.27.4` | `success_without_findings` | 3 | 3 | 0 | 0 | `analyzed_clean=1` |
| `go-vet.m02` | `go.vet` | `go1.27.4` | `success_without_findings` | 30 | 30 | 0 | 0 | `analyzed_clean=1` |
| `pack-gap.javascript` | `javascript.none` | `미확정` | `not_run` | 3 | 0 | 0 | 0 | `-` |
| `pack-gap.python` | `python.none` | `미확정` | `not_run` | 3 | 0 | 0 | 0 | `-` |

### 작업 상태

| task | 필수 | 상태 | 사유 |
|---|---|---|---|
| `docs.discover` | true | `succeeded` | `없음` |
| `env.discover` | true | `succeeded` | `없음` |
| `gitleaks` | false | `succeeded` | `없음` |
| `go-vet.m01` | true | `succeeded` | `없음` |
| `go-vet.m02` | true | `succeeded` | `없음` |
| `pack-gap.javascript` | false | `unsupported` | `analyzer_pack_missing` |
| `pack-gap.python` | false | `unsupported` | `analyzer_pack_missing` |
| `review.documents` | true | `succeeded` | `없음` |
| `review.environment` | false | `succeeded` | `없음` |
| `review.interpret` | false | `skipped_policy` | `no_tool_observations` |
| `review.investigate` | true | `succeeded` | `없음` |
| `review.investigate.s001` | false | `succeeded` | `없음` |
| `review.investigate.s002` | false | `succeeded` | `없음` |
| `review.investigate.s003` | false | `succeeded` | `없음` |
| `review.investigate.s004` | false | `budget_exhausted` | `review_budget_exhausted` |
| `review.investigate.s005` | false | `budget_exhausted` | `review_budget_exhausted` |
| `review.investigate.s006` | false | `budget_exhausted` | `review_budget_exhausted` |
| `review.investigate.s007` | false | `skipped_policy` | `no_review_scope` |
| `review.investigate.s008` | false | `skipped_policy` | `no_review_scope` |
| `review.investigate.s009` | false | `skipped_policy` | `no_review_scope` |
| `review.scope` | false | `succeeded` | `없음` |
| `review.verify` | true | `succeeded` | `없음` |
| `review.verify.s001` | false | `succeeded` | `없음` |
| `review.verify.s002` | false | `succeeded` | `없음` |
| `review.verify.s003` | false | `succeeded` | `없음` |
| `review.verify.s004` | false | `budget_exhausted` | `review_budget_exhausted` |
| `review.verify.s005` | false | `budget_exhausted` | `review_budget_exhausted` |
| `review.verify.s006` | false | `budget_exhausted` | `review_budget_exhausted` |
| `review.verify.s007` | false | `skipped_policy` | `no_review_scope` |
| `review.verify.s008` | false | `skipped_policy` | `no_review_scope` |
| `review.verify.s009` | false | `skipped_policy` | `no_review_scope` |

### 실행 기록

| 실행 | 상태 | exit | 정리 | argv | cwd | stdout | stderr |
|---|---|---|---|---|---|---|---|
| `env.discover/gitleaks.version` | `exited` | `0` | `confirmed` | `gitleaks version` | `.` | `0195f3a0-0000-7000-8000-00000000060a` | `0195f3a0-0000-7000-8000-00000000060b` |
| `env.discover/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-000000000608` | `0195f3a0-0000-7000-8000-000000000609` |
| `env.discover/lizard.version` | `exited` | `0` | `confirmed` | `lizard version` | `.` | `0195f3a0-0000-7000-8000-00000000060c` | `0195f3a0-0000-7000-8000-00000000060d` |
| `env.discover/staticcheck.version` | `exited` | `0` | `confirmed` | `staticcheck version` | `.` | `0195f3a0-0000-7000-8000-00000000060e` | `0195f3a0-0000-7000-8000-00000000060f` |
| `gitleaks/gitleaks.run` | `exited` | `0` | `confirmed` | `gitleaks .` | `.` | `0195f3a0-0000-7000-8000-00000000062a` | `0195f3a0-0000-7000-8000-00000000062b` |
| `gitleaks/gitleaks.version` | `exited` | `0` | `confirmed` | `gitleaks --version` | `.` | `0195f3a0-0000-7000-8000-000000000628` | `0195f3a0-0000-7000-8000-000000000629` |
| `go-vet.m01/go.list` | `exited` | `0` | `confirmed` | `go list -e -json ./...` | `.` | `0195f3a0-0000-7000-8000-000000000618` | `0195f3a0-0000-7000-8000-000000000619` |
| `go-vet.m01/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json` | `.` | `0195f3a0-0000-7000-8000-000000000616` | `0195f3a0-0000-7000-8000-000000000617` |
| `go-vet.m01/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-000000000614` | `0195f3a0-0000-7000-8000-000000000615` |
| `go-vet.m01/go.vet` | `exited` | `0` | `confirmed` | `go vet -json ./...` | `.` | `0195f3a0-0000-7000-8000-00000000061a` | `0195f3a0-0000-7000-8000-00000000061b` |
| `go-vet.m02/go.list` | `exited` | `0` | `confirmed` | `go list -e -json ./...` | `.` | `0195f3a0-0000-7000-8000-000000000622` | `0195f3a0-0000-7000-8000-000000000623` |
| `go-vet.m02/go.modedit` | `exited` | `0` | `confirmed` | `go mod edit -json` | `.` | `0195f3a0-0000-7000-8000-000000000620` | `0195f3a0-0000-7000-8000-000000000621` |
| `go-vet.m02/go.version` | `exited` | `0` | `confirmed` | `go version` | `.` | `0195f3a0-0000-7000-8000-00000000061e` | `0195f3a0-0000-7000-8000-00000000061f` |
| `go-vet.m02/go.vet` | `exited` | `0` | `confirmed` | `go vet -json ./...` | `.` | `0195f3a0-0000-7000-8000-000000000624` | `0195f3a0-0000-7000-8000-000000000625` |

### 산출물

| id | task | role | media | sha256 | bytes | decoder |
|---|---|---|---|---|---|---|
| `0195f3a0-0000-7000-8000-000000000606` | `-` | `source_manifest` | `application/vnd.codeye.source-manifest+json` | `22d1b9e3af626ed1380a0a5502e8ab3793d98308932434fa65f21d9c12ee059a` | 7829 | `-` |
| `0195f3a0-0000-7000-8000-000000000654` | `-` | `result_manifest` | `application/vnd.codeye.result-manifest+json` | `7ae1aa6b6b6f0b8317a4b79c82961f2133d76f20825b4b06ee589c0972ecc5c7` | 16503 | `-` |
| `0195f3a0-0000-7000-8000-00000000061c` | `go-vet.m01` | `observations` | `application/vnd.codeye.observations+json` | `b65a119bccda754ae0649457a2cbe10c4f4fa2ed1f47f78707788a11323de3f1` | 3072 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=go_vet_observations` |
| `0195f3a0-0000-7000-8000-000000000626` | `go-vet.m02` | `observations` | `application/vnd.codeye.observations+json` | `7e43e3f99236081c27d2744661f70cee0538f6c612276f8507cc27eeeb4cab5e` | 3543 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=go_vet_observations` |
| `0195f3a0-0000-7000-8000-00000000062c` | `gitleaks` | `observations` | `application/vnd.codeye.observations+json` | `75ab8fe8cee9af64b98b0a06ef7c994e89c976b88a282cad086ab258b8e7134c` | 3006 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-00000000062e` | `pack-gap.javascript` | `observations` | `application/vnd.codeye.observations+json` | `9d629182f29f5a0387d35216ff9f77ccb514ea65481d01d73d51a5dfb7fbcd70` | 1142 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-000000000630` | `pack-gap.python` | `observations` | `application/vnd.codeye.observations+json` | `35b911b36d5ad38dc68eba6d2b2adda2c3a662d8f24d67f62e2dba36080c1a3f` | 1130 | `application/vnd.codeye.observations+json;schema_version=0.1;kind=analyzer_observations` |
| `0195f3a0-0000-7000-8000-000000000608` | `env.discover` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-00000000060a` | `env.discover` | `stdout` | `text/plain` | `c9fd9ccb6682c54b5fcb0363757b6c6873564e7c067f70b3b5581b611528b9f4` | 7 | `-` |
| `0195f3a0-0000-7000-8000-00000000060c` | `env.discover` | `stdout` | `text/plain` | `55f4221aa0e36b70cbfc65ccc26728d31f6510eb1ead635badc007cdb5cd2bed` | 7 | `-` |
| `0195f3a0-0000-7000-8000-00000000060e` | `env.discover` | `stdout` | `text/plain` | `53199309ddfb6ea7ecdc24c7731f535dc3d6bba2e8f053b895b78b967b84c534` | 9 | `-` |
| `0195f3a0-0000-7000-8000-000000000614` | `go-vet.m01` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-000000000616` | `go-vet.m01` | `stdout` | `text/plain` | `45f63a941a43b0e40cbf0c7ca3e31910f84485b8eef76ee82896dd3db4669835` | 48 | `-` |
| `0195f3a0-0000-7000-8000-000000000618` | `go-vet.m01` | `stdout` | `text/plain` | `2bb15386bcd126dd898bf42f627459520bad3125c5aa674aff9d30c0df2a247e` | 43 | `-` |
| `0195f3a0-0000-7000-8000-00000000061a` | `go-vet.m01` | `stdout` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000061e` | `go-vet.m02` | `stdout` | `text/plain` | `a29205bd83967c2f70e6768fae009600038d6056499fa289de9d5f9b79beaef4` | 32 | `-` |
| `0195f3a0-0000-7000-8000-000000000620` | `go-vet.m02` | `stdout` | `text/plain` | `81d503b47847a8767cdc5b2fa8af2c93a59e41b3c34fee26cf3751deaa976803` | 50 | `-` |
| `0195f3a0-0000-7000-8000-000000000622` | `go-vet.m02` | `stdout` | `text/plain` | `c698b07632921ba3ff4259f687a08a9785232f4b62da15e201652d42714d095a` | 45 | `-` |
| `0195f3a0-0000-7000-8000-000000000624` | `go-vet.m02` | `stdout` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000628` | `gitleaks` | `stdout` | `text/plain` | `c9fd9ccb6682c54b5fcb0363757b6c6873564e7c067f70b3b5581b611528b9f4` | 7 | `-` |
| `0195f3a0-0000-7000-8000-00000000062a` | `gitleaks` | `stdout` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000609` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000060b` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000060d` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000060f` | `env.discover` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000615` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000617` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000619` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000061b` | `go-vet.m01` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000061f` | `go-vet.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000621` | `go-vet.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000623` | `go-vet.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000625` | `go-vet.m02` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000629` | `gitleaks` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-00000000062b` | `gitleaks` | `stderr` | `text/plain` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | 0 | `-` |
| `0195f3a0-0000-7000-8000-000000000610` | `env.discover` | `other` | `application/vnd.codeye.environment+json` | `ad18193c720bd7a91b6b25c25583bf10e9af4a1a6adc46555a2f768db8a9bcaf` | 3950 | `application/vnd.codeye.environment+json;schema_version=0.1` |
| `0195f3a0-0000-7000-8000-000000000612` | `docs.discover` | `other` | `application/vnd.codeye.environment+json` | `962fe2f9e87e2f315b6ac7f6f6514752da427c9754d8622ab6714c484069ba74` | 1463 | `application/vnd.codeye.environment+json;schema_version=0.1` |
| `0195f3a0-0000-7000-8000-000000000632` | `review.environment` | `other` | `application/vnd.codeye.review+json` | `49f998deba92769945ddfcb6463eb43cb2309b655437530aecf607534b66e72d` | 4199 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-000000000634` | `review.documents` | `other` | `application/vnd.codeye.review+json` | `9b3df22853e958c1943cc544c119c1a2fa267dba6ba973d21b6a0bac3967fdbd` | 1347 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-000000000636` | `review.investigate` | `other` | `application/vnd.codeye.review+json` | `b57822ca2ee5be2b978746229dc6c850ccb21bc14d667c10c10fb95c7abfd3e9` | 3320 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-000000000639` | `review.verify` | `other` | `application/vnd.codeye.review+json` | `fbe76650dc466f4886e164cfdabaf2eba3e33217cd9fca26254920a423799297` | 4056 | `application/vnd.codeye.review+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-00000000063b` | `review.scope` | `other` | `application/vnd.codeye.review-scope+json` | `e0ed4354a3a0de8ccf8b17da9b88c700e6ccfc592b6fd36e26858e664df9f235` | 12721 | `application/vnd.codeye.review-scope+json;schema_version=0.2` |
| `0195f3a0-0000-7000-8000-00000000063d` | `review.investigate.s001` | `other` | `application/vnd.codeye.review+json` | `2889c89a67002b31a6c6aac852ca50743dcf56ce53c62311a7acf18ff8a806f1` | 2897 | `application/vnd.codeye.review+json;schema_version=0.3` |
| `0195f3a0-0000-7000-8000-00000000063f` | `review.verify.s001` | `other` | `application/vnd.codeye.review+json` | `6fac9d3fa6ab6bc85769012a1864d8d9849f07001ccd209f4308d72fdcbfe1ec` | 3521 | `application/vnd.codeye.review+json;schema_version=0.3` |
| `0195f3a0-0000-7000-8000-000000000641` | `review.investigate.s002` | `other` | `application/vnd.codeye.review+json` | `9253280ab05a1fb7e27ba876d5161f2ae5920273b728595ba73fc54ea72259ac` | 2889 | `application/vnd.codeye.review+json;schema_version=0.3` |
| `0195f3a0-0000-7000-8000-000000000643` | `review.verify.s002` | `other` | `application/vnd.codeye.review+json` | `21f85c99def0a3f7fe2184d6e7d79c41816c63c4664ae08a28b77f15774f1c47` | 3513 | `application/vnd.codeye.review+json;schema_version=0.3` |
| `0195f3a0-0000-7000-8000-000000000645` | `review.investigate.s003` | `other` | `application/vnd.codeye.review+json` | `ac5a0a56656534d455060d504077179310d9a2ad9b9b15267321790221068835` | 2891 | `application/vnd.codeye.review+json;schema_version=0.3` |
| `0195f3a0-0000-7000-8000-000000000647` | `review.verify.s003` | `other` | `application/vnd.codeye.review+json` | `8a86a815d89516a00ac9c0b4f8a599c2e2de4fb2c4bbdd48423757278de6ca90` | 3515 | `application/vnd.codeye.review+json;schema_version=0.3` |

### 한계와 공백

- caller\_expansion\_go\_only
- caller\_expansion\_package\_functions\_only
- controller는 snapshot 원본을 보관하지 않아 코드 발췌를 넣지 않았다\.
- excerpt\_only
- files\_outside\_review\_context
- no\_callers\_found
- no\_symbol\_for\_expansion
- 범위에 이 언어의 소스가 있으나 노드에 그 언어의 분석기가 없어 정적 분석을 하지 않았다\. \(javascript\)
- 범위에 이 언어의 소스가 있으나 노드에 그 언어의 분석기가 없어 정적 분석을 하지 않았다\. \(python\)
- upstream\_interpret\_skipped

digest sha256: `507331f6834207d6edaa4216a62a4680aaeb70ac2a26ce4d213980802af5738a`

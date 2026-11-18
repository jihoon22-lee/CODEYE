# CODEYE 문서 안내

R0는 개인 모드의 Go 분석·승인된 모델 검토·근거 보고서까지 검증했다. R1은 모듈·문서·환경 탐색과 프로젝트별 진행 표시에서 시작한다. 새 세션은 [W15 작업 순서](17-implementation-plan.md)와 [수용 조건 TC-96~100](18-acceptance-and-traceability.md)을 함께 읽는다.

| 하려는 일 | 먼저 읽을 문서 |
|---|---|
| 개인 모드에서 분석 실행 | [서버·로그인](24-w03-api.md) · [노드 토큰](28-w06-node-protocol.md) · [에이전트](28-w06-agent-runtime.md) · [자동 증감](30-w07-scheduler.md) |
| 결과 읽기와 평가 | [보고서 사용법](35-w13-evidence-report.md) · [실제 파일럿과 한계](36-r0-pilot.md) · [Go vet 보고서 예시](../examples/report-go-vet.json) |
| 언어 분석 팩 구현(W16~W21) | [분석기 팩 계약](39-r1-analyzer-packs.md) · [도구 어댑터 계약](06-analyzer-contract.md) · [envelope schema](../schemas/analyzer-observations.schema.json) |
| 모델 역할 설계 | [역할별 지침과 현재 팩](07-llm-and-instructions.md) · [평가 기준](16-evaluation.md) |
| 프로젝트 진행 화면 구현 | [화면 단계·상태 예시](12-report-and-ui.md) · [조회와 재접속](11-api-and-events.md) · [내부 파이프라인](04-analysis-pipeline.md) |

## 제품 계약과 계획

| 분야 | 문서 |
|---|---|
| 제품과 범위 | [00 제품 정의](00-product-and-scope.md) · [01 요구사항](01-requirements.md) · [02 분석 품질](02-analysis-quality.md) |
| 실행 경로 | [03 아키텍처](03-architecture.md) · [04 파이프라인](04-analysis-pipeline.md) · [05 소스·환경](05-source-and-environments.md) · [06 도구 어댑터](06-analyzer-contract.md) |
| 모델과 노드 | [07 모델 역할](07-llm-and-instructions.md) · [08 Native 실행](08-native-execution.md) · [09 워커와 스케줄링](09-workers-and-scheduling.md) |
| 저장·조회·표시 | [10 데이터 무결성](10-data-model.md) · [11 API·이벤트](11-api-and-events.md) · [12 보고서·진행 표시](12-report-and-ui.md) |
| 연동과 운영 | [13 GitHub·GHES](13-github-and-ghes.md) · [14 보안·권한](14-security-and-governance.md) · [15 배포·운영](15-deployment-and-operations.md) |
| 개발과 인수 | [16 품질 평가](16-evaluation.md) · [17 구현 계획](17-implementation-plan.md) · [18 요구사항·시험 추적](18-acceptance-and-traceability.md) · [19 결정 기록](19-decisions-and-open-questions.md) |

## R0 구현 근거

단계별 기록의 환경·시험 수·미실행 항목은 각 단계의 검증 시점에 해당한다. 현재 통합 범위는 [W14 기록](36-r0-pilot.md)을 기준으로 확인한다.

| 확인할 범위 | 구현·시험 기록 |
|---|---|
| 환경과 저장 | [W01 doctor](21-w01-foundation.md) · [W02 PostgreSQL](23-w02-storage.md) · [W03 인증 API](24-w03-api.md) |
| 입력과 실행 | [W04 소스 사본](25-w04-source.md) · [W05 실행기](26-w05-native.md) · [W06 노드 프로토콜](28-w06-node-protocol.md) · [W06 런타임](28-w06-agent-runtime.md) · [W07 워커 풀](30-w07-scheduler.md) |
| 분석과 게시 | [W08 Go vet](29-w08-go-vet.md) · [W09 보고서 게시](31-w09-report.md) · [W10 OpenCode](32-w10-opencode.md) · [QA 예산 relay](r0-openai-gateway.md) |
| 검토와 검증 | [W11 맥락·후보](33-w11-context-review.md) · [W12 반증](34-w12-verifier.md) · [W13 근거 화면](35-w13-evidence-report.md) · [W14 품질 회귀](36-r0-pilot.md) |
| R1 탐색과 진행 | [W15 모듈·문서·환경 탐색과 단계 표시](37-w15-discovery-and-progress.md) · [W22 예약·가중 진행률·SSE](38-w22-schedules-progress-sse.md) |
| R1 분석 팩 | [W19 분석기 팩 계약과 Go 팩(Staticcheck)](39-r1-analyzer-packs.md) · [W18 JS/TS 팩(ESLint·tsc)](43-w18-js-ts-pack.md) · [W20 Rust 팩(cargo check·clippy)](44-w20-rust-pack.md) |
| R1 이력과 판정 | [W23 이력·비교·사람 검토](40-w23-history-comparison.md) |

HTTP 경로의 구현 표시는 [OpenAPI](../api/openapi.yaml)에 있다. 기계 판독 계약은 [schemas](../schemas), 입력·출력 예시는 [examples](../examples), 활성 모델 지침은 [R0 팩](../prompts/review-v1/pack.yaml)에서 찾을 수 있다.

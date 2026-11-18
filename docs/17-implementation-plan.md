# 17. 구현 작업 계획

W01~W14의 R0 수직 경로를 구현했다. Go 실도구와 실제 모델을 사용한 개인 프로젝트·합성 자료 평가 결과, 미검증 환경과 다음 W15의 착수 자료는 [R0 파일럿 기록](36-r0-pilot.md)에 있다. 단계별 수용 조건과 선행 관계는 아래 표를 따른다.

## 1. R0: 개인 PC의 실동작 수직 경로

| ID | 작업 | 선행 | 산출물·완료 조건 |
|---|---|---|---|
| W01 | 환경 doctor·프로젝트 skeleton — 기반 구현 완료 | 없음 | 실제 OS/도구/DB/모델 능력 결과. 실패 원인을 구분하고 Docker 요구 없음 |
| W02 | 도메인 계약·DB migration — 구현 완료 | W01 | run/attempt/task/evidence/project ACL·event·lease의 트랜잭션 테스트 |
| W03 | 최소 API·인증·웹 뼈대 — 구현 완료 | W02 | 프로젝트 등록→run 202→상태 조회. 무인증 LAN 공개 금지 |
| W04 | SourceAdapter Git/local — 구현 완료 | W02 | acquire 시 입력 고정, 원본 미변경, manifest·불안정 복사 검사 |
| W05 | NativeExecutor — 구현 완료 | W01 | argv/env/cwd, timeout, stdout cap, Windows/Linux 프로세스 그룹 종료 |
| W06 | [Agent·worker protocol 구현](28-w06-agent-runtime.md) | W02,W05 | enroll/heartbeat/claim/epoch, 두 에이전트로 중복 claim 방지 |
| W07 | [Scheduler·autoscaler 구현](30-w07-scheduler.md) | W06 | min/max, 0→N→0, drain, resource admission, lease expiry·late result 시험 |
| W08 | 첫 언어 분석 어댑터 — Go vet 구현 | W04,W05 | 실제 개인 프로젝트에 맞는 한 언어 선정; raw→normalize·실패 parser fixture |
| W09 | [구조화 보고서 게시·내보내기 구현](31-w09-report.md) | W03,W08 | LLM 없이도 task 상태·원시 근거·공백이 보이는 보고서 |
| W10 | [OpenCode adapter 실연동 검증](32-w10-opencode.md) | W04,W06 | 실제 버전 계약, workspace digest, model request/cancel/permission block 확인 |
| W11 | [Context·Investigator 실연동](33-w11-context-review.md) | W08,W10 | 문서 없는 코드와 문서 있는 코드 모두 근거 있는 후보 생성 |
| W12 | [Verifier·증거 검증 구현](34-w12-verifier.md) | W11 | 반례로 후보 철회, 허위 path·재현 주장 차단, 모델 실패 partial |
| W13 | [보고서 종합·상세 UI](35-w13-evidence-report.md) | W09,W12 | 요약→영역→근거, 숫자 일치, 미확정 표시, export |
| W14 | [R0 회귀·개인 파일럿](36-r0-pilot.md) | W07,W13 | 최소 합성 집합+승인된 개인 프로젝트에서 실제 end-to-end 실행 기록 |

R0의 두 에이전트 시험은 한 머신에서 별도 node identity로 먼저 수행할 수 있다. 실제 다중 머신·NFS·네트워크 partition 보증을 대체하지 않는다. Windows와 RHEL 중 개발에 사용하지 않은 환경의 시험은 미실행으로 명시한다.

## 2. R1: 개인 실용판과 분석 깊이

| ID | 작업 | 선행 | 완료 조건 |
|---|---|---|---|
| W15 | 모듈·문서·환경 탐색과 프로젝트별 단계 표시 — [구현](37-w15-discovery-and-progress.md) | W14 | 고정 입력에서 모듈별 환경·문서 근거 수집, 역할별 지침 검증, 저장된 단계로 목록과 상세를 갱신하는 첫 수직 경로 |
| W16 | C/C++ 분석 팩 — [구현](41-w16-cpp-pack.md) | W15 | compile DB path mapping, 생성 헤더·flags, 부분 TU 범위, clang/cppcheck 정규화 |
| W17 | Python 분석 팩 | W15 | Ruff+타입 검사+보안/오류 흐름, interpreter/stub/import 공백 표시 |
| W18 | JS/TS 분석 팩 | W15 | tsconfig/workspace/Node, type-aware lint, async·경계 검토 — [구현](43-w18-js-ts-pack.md) |
| W19 | Go 분석 팩 — [팩 계약·Staticcheck 구현](39-r1-analyzer-packs.md) | W15 | vet/Staticcheck, tags/CGO, context·goroutine·오류 경로. W16~W21이 쓰는 분석기 팩 계약(저장소 코드 실행 허가, 분석기 공백, 비교의 규칙 문맥·소스 안 억제) 포함 |
| W20 | Rust 분석 팩 — [구현](44-w20-rust-pack.md) | W15 | check/clippy, features/target, build script 신뢰, unsafe·panic 검토 |
| W21 | 구조/복잡도·비밀정보 팩 — [구현](45-w21-structure-secrets-pack.md) | W15 | 지표를 결함으로 오인하지 않음, secret raw redaction, 중복 엔진 제거 |
| W22 | 예약·진행·재접속 확장 — [구현](38-w22-schedules-progress-sse.md) | W07,W13,W15 | 예약 중첩·누락 처리, W15의 단계 조회와 같은 기준으로 계획 revision·가중 진행률·SSE gap 복원 |
| W23 | 이력·비교·사람 검토 — [구현](40-w23-history-comparison.md) | W13 | fingerprint, absent≠fixed, disposition 이유·기간·권한 |
| W24 | 품질 평가 runner | W12,W16-W21 | 정답 차단, holdout, 지표/표본·모델 변동·보고서 읽기 평가 |
| W25 | 개인 패키징·운영 검증 | W22-W24 | Native start/stop/doctor, disk quota, DB·artifact backup/복원, 사용 가이드 |

다섯 언어의 첫 공통 탐색 지원은 빠르게 넣을 수 있지만 정밀 어댑터 완료는 위 시험이 있어야 한다. 실제 첫 프로젝트의 언어에 맞춰 W16~W20 순서를 바꿀 수 있다. '다섯 언어 지원' 표시는 탐색/기본/정밀/실행 검증 수준을 분리한다.

## 3. R2: 부서 공개

| ID | 작업 | 선행 | 완료 조건 |
|---|---|---|---|
| W26 | 다중 사용자·권한 강화 | W25 | project/source-root/artifact/export/SSE 격리와 감사 테스트 |
| W27 | GHES/GitHub App | W26 | 연결 가이드, 최소 권한, webhook HMAC·dedupe·비동기 응답 |
| W28 | PR·댓글 분석 | W27 | SHA 고정, 호출자 권한, fork 정책, stale 댓글 방지, outbox |
| W29 | 다중 물리 노드 검증 | W25,W26 | 실제 node capability 배정, NFS 접근 차이, partition·복구·drain |
| W30 | 폐쇄망 배포팩 | W29 | 내부 mirror/CA, 외부 fallback 없음, offline assets, 툴 버전 계약 |
| W31 | 운영 수용·공개 | W28,W30 | 장애 훈련, backup 복원, 권한 철회, 품질 baseline·제한 문서 |

## 4. 후속 선택 작업

ContainerExecutor, 고비용 interfile 분석, 추가 OS, 인프라 CapacityProvider, PDF export는 필요에 따라 별도 작업으로 만든다. 기존 CI 통합과 의존성 CVE 검사를 자동으로 이 단계에 끼워 넣지 않는다. 제품 범위 변경은 소유자 결정이 필요하다.

## 5. 작업 티켓 형식

각 작업에는 목적, 요구 ID, 입력/출력, 변경 모듈, 선행 작업, 실패 시나리오, API/schema 영향, 수용 테스트, 관측 지표, rollback·마이그레이션, 완료 증거를 적는다. 구현 설명만 있고 실제 시험 결과가 없으면 done으로 처리하지 않는다.

## 6. 다음 세션에서 시작할 W15

기준은 R0의 `9c5bca2`다. W15는 기존 run의 취득·lease·게시 경로에 연결한다. 첫 입력으로 루트 안내가 있는 저장소에 하위 모듈 둘을 둔다. 한 모듈은 문서가 없고 다른 하나는 루트와 충돌하는 안내 및 노드와 다른 버전 요구를 갖게 한다. 정답과 비교 자료는 스냅샷 밖에 둔다.

| 순서 | 구현할 범위 | 확인할 결과 |
|---|---|---|
| 1. 환경과 문서 수집 | 워커가 고정된 manifest에서 모듈과 자료의 적용 경로를 찾고 버전 선언과 노드 관측을 구분한다. 기존 [맥락 수집](../internal/worker/review/context.go)을 출발점으로 삼는다. | 모델이 없어도 환경·문서 목록, 출처, 누락·제외 사유가 남는다. TC-96 |
| 2. 역할별 해석 | [역할 지침](07-llm-and-instructions.md)을 선택기에 연결하고 구조화 산출물과 검증기를 함께 버전 관리한다. 기존 context·investigate·verify 기록을 읽는 경로와 선택 첨부의 출처·모듈 범위를 보존한다. | 허위 근거·범위 이탈을 거부하고, 모델 실패 시 독립 수집 결과가 남는다. TC-97, TC-100 |
| 3. 진행 상태 게시 | 워커가 어떤 단계에서 task를 수행했는지 기록하고, 서버가 attempt/epoch와 task 전이를 검증해 저장한다. 환경·문서를 한 작업으로 묶었던 과거 실행에는 세부 단계를 추정하지 않는다. | 동일 실행의 프로젝트 요약과 상세가 일치한다. 중복 이벤트와 늦은 attempt가 화면을 되돌리지 않는다. TC-98, TC-99 |
| 4. 제품 화면·회귀 | 프로젝트 목록에 단계 요약, 상세에 단계별 상태·사유를 연결한다. 실제 워커·PostgreSQL·Chromium에서 polling 경로를 먼저 검증한다. | 복수 프로젝트를 구분하고 부분 실패·미기록을 볼 수 있다. 실제 모델 평가는 동일 예산으로 기존 팩과 대조한다. TC-98~100 |

W15에서는 모델 연결 없이도 수집과 단계 표시를 먼저 시험할 수 있다. 언어별 진단 해석은 W16~W21의 실제 어댑터와 연결한다. W22의 예약과 SSE 도입은 W15의 선행조건이 아니다. API/schema를 변경하는 구현 PR에서 호환 가능한 응답·예시·검증 스크립트와 migration 필요 여부를 함께 검토한다. [시험 시나리오](18-acceptance-and-traceability.md) · [화면 설계](12-report-and-ui.md)

## 7. 단계 종료 증거

R0는 실제 입력·명령·모델 연결·보고서·취소·autoscale 로그, R1은 언어별 계약 시험과 품질 baseline, R2는 권한·분산·폐쇄망 복구 시험을 남긴다. 코드 테스트와 제품 분석 정확도 평가를 별도 결과로 보고한다.

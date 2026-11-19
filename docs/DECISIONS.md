# 결정 기록

확정된 제품 결정(D), 설계 기본안과 구현 결정(A), 아직 확인할 환경 정보, 결정을 바꾸는 절차를 모은다. ID는 바꾸지 않는다. 각 결정은 영향 받는 요구 ID([요구사항](REQUIREMENTS.md))와 수용 시험 ID([시험](TESTING.md))를 적는다.

날짜: A11~A13의 2026-11-17은 원래 결정 기록의 날짜다. A14~A33의 날짜는 결정을 처음 기록한 커밋의 날짜(기록일, 커밋 기준)다.

## 제품 결정

| ID | 결정 | 이유/결과 |
|---|---|---|
| D01 | 이름 CODEYE | 사용자 선택. 이름 탐색을 다시 열지 않음 |
| D02 | 독립 분석·검토·보고서 | 기존 CI 연결/수집은 범위 밖 |
| D03 | Native 기본, Container 선택 | 개인 PC·폐쇄망에서 컨테이너 없이 사용 |
| D04 | 소스·문서 자동 파악 | 설명·첨부를 강제하지 않아 레거시도 사용 |
| D05 | 초기 CVE 제외 | 패키지 선언은 환경 파악에만 사용 |
| D06 | 단일 머신→여러 노드 | 초기부터 API agent·lease·capacity 계약 |
| D07 | 최소/최대 자동 증감 | 단순 병렬 개수 설정만으로 대체하지 않음 |
| D08 | Git 브랜치 최신/로컬 복사 | 분석 입력 고정, 재시도와 새로 분석 구분 |
| D09 | 플랫폼 SSH 없음 | 운영자가 서비스를 띄우고 API로 연결 |
| D10 | 사내 OpenCode 우선 | 허용되지 않은 직접 API나 외부 모델로 우회 금지 |
| D11 | 별도 벤치마킹 문서 없음 | 조사 결과는 CODEYE 요구·설계·계약에 통합 |
| D12 | 모델 작업마다 별도 지침과 입출력 계약을 둔다 | 환경·문서의 사실 수집, 코드 후보 조사, 도구 관찰 해석과 반증을 각각 평가할 수 있게 한다. [모델 검토](REVIEW.md) |
| D13 | 프로젝트마다 실행 단계를 시각적으로 표시한다 | 목록에서 현재 작업을 구분하고 상세에서 미실행·실패·재시도를 설명한다. [보고서와 화면](REPORT.md) |

## 설계 기본안

| ID | 선택 | 대안과 조정 조건 |
|---|---|---|
| A01 | Go control/agent/worker | OS 프로세스 제어와 API 계약 공유. 환경 검증에서 큰 제약이 나오면 재검토 |
| A02 | React·TS 정적 UI | 복잡한 보고서·상태 탐색 요구. Node 서버는 운영 필수 아님 |
| A03 | PostgreSQL 단일 backend | 개인 설치 비용을 감수하고 분산 상태의 일관성 확보. 이중 DB 구현은 초기 제외 |
| A04 | 한 attempt는 한 node | 복구·workspace 일관성 단순화. 여러 run 분산만 우선 |
| A05 | local artifact store+API | NFS가 없어도 노드 확장. 대형 운영에서 object store 추가 |
| A06 | 벡터 DB 비필수 | 심볼·경로·문서 검색으로 시작, 실제 컨텍스트 품질 부족을 측정한 뒤 확장 |
| A07 | 검토 역할 분리 | 후보와 반증을 분리하되 다중 모델 필수화 안 함 |
| A08 | 단일 점수 미제공 | 발견·근거·분석 공백을 지표로 제공 |
| A09 | 기본 report JSON/MD/HTML | 고정된 JSON 본문을 PostgreSQL에 저장하고 동일 revision에서 Markdown·HTML을 렌더링한다. 코드 제외가 기본이며 열람 권한을 요청마다 확인한다. [보고서와 화면](REPORT.md) |
| A10 | OS능력 기반 자원 enforcement | hard limit와 admission을 구분, 컨테이너 요구로 우회하지 않음 |

이 기본안은 구현 시 합리적 근거와 계약/테스트 수정으로 변경할 수 있다. 사용자가 확정한 제품 요구를 기술 편의 때문에 바꾸지 않는다.

## 기반·진단·개발 저장소

**A11 — 첫 실행 기반의 경계.** (2026-11-17) REQ-004/026/030, NFR-001/006에 대해 버전·접근·인증 query의 실측과 미검증 능력을 분리한다. 실행 기반의 첫 구성은 doctor와 로컬 진단 파일 뷰어이며, 저장 계층과 인증 API 없이 동작하는 임시 in-memory run API를 두지 않는다. `start`를 정적 데모 서버 의미로 쓰지 않는다(지금의 `start`는 개인 모드 supervisor다, A32). 진단 schema·예시·validator와 TC-36~39가 이 경계를 고정한다. 상세 근거는 [운영](OPERATIONS.md)을 따른다.

**A12 — 모델 사용량과 선택.** (2026-11-17) 개인 PC의 OpenCode 무료 모델 한도를 보존하도록 doctor는 모델을 호출하지 않는다. 실요청 확인은 명시적인 `codeye model probe`로만 한다. 기본 OpenCode 방향(D10)을 유지한다. Codex/Claude Code는 향후 명시적 선택 어댑터 후보이며 workspace·권한·구조화 출력·취소 계약을 별도로 검증해야 한다. 동일 연결 방식이나 무료 한도를 가정하지 않고 자동 fallback을 추가하지 않는다.

**A13 — 개발 저장소의 CI 범위.** (2026-11-17) PR 검증 요청에 따라 CODEYE 자체의 단위·DB 통합·웹·계약 테스트를 GitHub Actions에서 실행한다. D02/REQ-032의 비목표는 분석 대상의 기존 CI 연결·수집·워크플로 생성 기능이며, 제품 자체를 개발하는 저장소의 자동 테스트를 금지한다는 뜻이 아니다. 제품 기능·분석 프로필·완료율 분모는 변경하지 않는다. CI는 모델 credential·모델 호출·배포 없이 read-only 저장소 권한으로 실행한다. Windows runner 단위 시험은 Windows 11의 NativeExecutor·DB·전체 플랫폼 수용 시험을 대체하지 않는다. 실행 범위는 [시험](TESTING.md)에 있다.

## 저장과 API

**A14 — 저장 불변조건.** (기록 2026-11-18) pgx/v5 5.13.0과 내장 SQL migration을 사용한다. migration은 명시적 `codeye migrate`로만 실행하고 checksum·동시 적용·rollback을 검증한다. attempt는 leased/recovery_required/released, cleanup은 pending/uncertain/confirmed다. 만료/불확실 cleanup은 소유 슬롯을 유지하며 확인 후 failed로 정리한다. 명시적 failed/cancelled 재시도는 보관 snapshot과 확인된 cleanup만 허용한다. 동일 완료 receipt 재전송은 새 결과를 게시하지 않는다. event sequence와 entity version을 구분하고 task_executions로 재시도 전 이력을 보존한다. 성공 보고서 게시 게이트(A09)와 자원 admission(A18)은 이 저장 계층 위에 있다. 오류·호환·migration 세부는 [아키텍처](ARCHITECTURE.md)를 따른다.

**A15 — 개인 모드 인증.** (기록 2026-11-18) loopback 전용 `codeye serve`를 제공한다. controller+agent를 함께 띄우는 `start`는 별도 supervisor 명령이다(A32에서 구현). CLI에서 발급하는 일회용 launch link는 0600 파일에 저장하며 DB에는 token hash만 보관한다. 세션 cookie·same-origin Host/Origin·CSRF를 강제하고 비구성원은 404로 존재를 숨긴다. migration 0002는 forward migration이며 배포/doctor에서 자동 적용하지 않는다. source 접근은 declared_unverified이고 worker 연결 전 queued를 성공으로 바꾸지 않는다. 브라우저 CSP 호환을 위해 JSON validator는 빌드 시 생성한다. [API](API.md)를 따른다.

## 소스와 실행

**A16 — 소스 보존 경계.** (기록 2026-11-18) source root는 열린 디렉터리 핸들에서 하위 경로를 해석하고 플랫폼 data root는 전체 승인 source root 밖에 둔다. Git은 승인 HTTPS 또는 명시적 local root에서 plumbing 명령으로 객체를 읽는다. canonical manifest 전체 bytes의 hash로 limitations·시각·branch까지 묶으며 retained retry에서 원본을 재확보하지 않는다. fetch disk 감시는 soft limit이고 timeout은 recovery_required로 격리한다. private Git credential/CA profile과 snapshot의 노드 간 이동은 후속이다. [아키텍처](ARCHITECTURE.md)의 소스 확보와 스냅샷을 따른다.

**A17 — 종료 관측과 실행 경계.** (기록 2026-11-18) NativeExecutor는 argv/cwd/env와 실행 outcome·cleanup을 분리한다. Linux는 leader PID를 reap 전까지 유지해 group signal의 PID 재사용 위험을 줄이고, /proc 관측 실패에도 알려진 그룹을 종료하되 cleanup을 uncertain으로 남긴다. Windows는 suspended 생성 후 Job에 연결한 뒤 resume한다. OnStarted는 제한시간이 적용되는 사후 알림이며 callback 실패가 이미 실행된 코드의 부재를 의미하지 않는다. 경로 검증·env 제거는 OS sandbox가 아니고 CPU/메모리 한도는 advisory다. [실행](EXECUTION.md)을 따른다.

## 노드 프로토콜

**A18 — 노드 프로토콜 계약과 migration 0003.** (기록 2026-11-18) REQ-018/019/022/035, NFR-002/003/006, TC-21에 대해 노드 HTTP를 다음으로 확정한다.

1. capacity는 bytes 단위(`memory_available_bytes`/`disk_available_bytes`), 명령 cursor는 노드별 단조 `sequence`(`last_command_sequence`)이며 command_id로 중복 제거한다.
2. claim 응답은 `{lease, job}`이고 lease에 claim 직후 `run_version`을 넣는다. complete·이벤트는 body의 `worker_id`와 인증 노드로 소유자를 대조한다.
3. 별도 snapshot 경로와 3단계 artifact upload 대신 `snapshot_fixed` 이벤트와 attempt 범위 단일 PUT을 쓴다. manifest bytes를 먼저 artifact로 올리고 그 hash가 snapshot digest와 같아야 한다.
4. 노드 경로는 Bearer만 받고 Origin이 있으면 거부한다. 다른 노드의 attempt는 404, 소유권·epoch·만료 불일치는 409 `STALE_ATTEMPT`다.
5. 자원 회계에서 CPU는 정책 예산(활성 예약 전체 차감)이다. 메모리·disk는 관측 여유에서 마지막 heartbeat 이후 예약만 뺀다.
6. 대기 사유와 프로젝트의 `execution_unavailable`은 저장 데이터에서 계산한다.

대안으로 OpenAPI 초안의 MB 단위·command ID cursor·lease 단독 응답을 검토했다. 단위 혼동, cursor 순서 보장 불가, job 재조회 왕복 때문에 채택하지 않았다. migration 0003은 forward 전용이다. migration 0002 시점의 run 본문은 digest가 정확히 일치하는 scope=full 세 형태만 복원하고, 그 시점의 Git snapshot(digest=commit)은 수정하지 않고 재시도 불가로 둔다. 비용은 claim 직렬화(전역 limits 행 잠금)이며 단일 controller 규모에서 허용한다. 상세 계약은 [API](API.md)와 [실행](EXECUTION.md)을 따른다.

## 개인 모드 운영

**A32 — 개인 패키징과 운영.** (기록 2026-11-19) REQ-018/021/022, NFR-003/006/007과 TC-124~127, TC-32에 대해 다음을 확정한다.

1. **프로세스 관리.** `codeye start`가 controller와 로컬 에이전트를 setsid로 띄우고 pid와 프로세스 시작 시각을 0600 기록에 남긴다. 신호는 pidfd를 연 뒤 시작 시각이 같을 때만 보낸다. pid가 재사용된 다른 프로세스에 신호가 가지 않게 하기 위해서다. pidfd가 없는 커널에서는 신호를 보내지 않고 `recovery_required`로 남긴다. 강제 종료는 하지 않는다. systemd·Windows Service 등록은 OS마다 달라 하지 않았다. Windows는 빌드되며 명시적 미지원 오류를 낸다.
2. **데이터베이스.** 운영자가 준비한 PostgreSQL(`CODEYE_DATABASE_URL`)만 구현했다. 관리형 모드를 넣으면 initdb·포트·버전 업그레이드·데이터 디렉터리 백업 책임이 CODEYE로 넘어온다. 그래서 설정에 이름만 두고 거부한다. start는 migration을 하지 않는다. schema 변경은 운영자가 백업한 뒤 `codeye migrate`로만 한다.
3. **정지.** drain(SIGUSR1) → 시간 초과 때 취소(SIGTERM) → 에이전트 journal로 정리 판정 → controller 정지 순서다. 에이전트가 끝나지 않으면 controller를 남긴다. 그래야 정리 보고를 계속 받을 수 있다. 취소된 run은 에이전트 종료의 기존 의미대로 `failed`(`agent_shutdown`)다. 자동으로 다시 대기열에 넣지 않았다. 그 판단은 재시도가 이전 attempt 결과를 덮지 않는 규칙(REQ-035)과 함께 정해야 한다.
4. **저장 상한.** 적용은 claim 트랜잭션 한 지점이다. 노드 영역은 controller가 노드 파일시스템을 볼 수 없어서 에이전트가 재고 heartbeat로 보고한다. artifacts는 controller 저장소의 원천인 DB에서 sha256마다 한 번 센 크기 합으로 정한다. 자동 삭제는 하지 않는다. 참조·pin·보존 규칙 없이 지우면 근거를 잃기 때문이다. 정리 정책은 후속이다.
5. **백업 일관성.** export한 DB snapshot 하나에서 dump와 확정 산출물 목록을 함께 얻는다. 산출물은 내용 주소이고 확정 뒤 바뀌지 않는다. 그래서 복사 시점이 달라도 hash로 같은 바이트임을 확인할 수 있다. DB 잠금이나 정지 강제는 개인 사용 중 백업을 막아 채택하지 않았다. 다만 정지 뒤 백업을 권장한다.
6. **복원.** 빈 DB, 빈 산출물 디렉터리, 새 `state_dir`에만 복원한다. 병합이나 덮어쓰기는 ID 충돌과 근거 혼합 위험이 있어 하지 않는다. 복원 직후 노드·세션·쓰지 않은 토큰을 무효로 하고 `leased` attempt를 `recovery_required`로 둔다. 예약은 유지한다. 철회된 노드의 `recovery_required` attempt를 운영자가 정리하는 명령은 아직 없다(열린 항목).
7. **진단.** doctor는 선택 필드 `operations`만 더한다. `schema_version`은 0.1 그대로이고 `--config` 없는 출력은 바뀌지 않는다.

[운영](OPERATIONS.md) · [사용 안내서](GUIDE.md)

## 진행 단계 기록

**A20 — 단계 기록과 검토 대상 없음.** (기록 2026-11-18) REQ-005/013/016/022와 TC-96~99에 대해 다음을 확정한다.

1. 표시 단계는 워커가 계획 시 task마다 선언하고 서버가 enum을 검증해 한 번만 저장한다. key 접두어로 추정하거나 migration에서 채우지 않는다. 단계가 없는 task가 하나라도 있으면 실행 전체를 `not_recorded`로 표시한다. 재시도에서 저장값이 NULL이면 새 워커의 선언을 한 번 기록하고 이벤트를 남긴다.
2. 검토할 대상이 없어 생략한 역할에 새 task 상태를 만들지 않는다. `skipped_policy`와 통제된 사유 코드(`no_documents`, `no_tool_observations`, `no_environment_gap`)를 쓰고 정책 제외 `model_source_not_approved`와 구분한다. 이 상태는 모델 검토 성공으로 세지 않는다. 대안으로 `not_applicable` 상태 추가를 검토했다. DB check·상태 schema·OpenAPI·전이표·보고서를 함께 바꾸는 비용에 비해 구분 정보가 사유 코드와 같아 채택하지 않았다.
3. 탐색 사실만 남은 실행은 의미 있는 결과가 있으므로 `failed`가 아니라 `partial`이다.
4. 지침 팩은 노드 운영자 설정(`--review-pack`)으로만 선택하고 run 프로필 enum은 바꾸지 않는다. 기본 팩은 A33에서 `review-r1`로 바뀌었고 선택 경로는 그대로다.

[보고서와 화면](REPORT.md)

## 분석기

**A19 — 실제 분석기 판정 시점.** (기록 2026-11-18) 워커가 task를 시작한 뒤 모듈·도구 probe에서 미지원 입력이나 정책 제외를 확인할 수 있다. 이때 `running → unsupported/skipped_policy`를 기록한다. 두 상태는 종료 상태이며 성공 수에 포함되지 않는다. 상태 어휘와 기존 DB 행은 유지된다. [분석기](ANALYZERS.md)

**A22 — 분석기 팩 계약.** (기록 2026-11-18) REQ-007/008/012/013/015와 TC-101·102에 대해 다음을 확정한다.

1. 언어 팩은 도구마다 `pack.Analyzer`를 구현하고, deep-review handler는 고정 순서의 `pack.Composite`다. task key는 이름 하나가 소유한다. go vet은 계획·실행·v0.1 문서를 바꾸지 않고 멤버가 된다. result manifest의 handler 이름은 `analyzers`가 된다.
2. 공통 산출물은 같은 media·버전에 kind `analyzer_observations`를 둔 새 envelope다. go vet 문서를 바꾸지 않기 위해서다. go vet 문서를 일반화하는 안은 go vet decoder·예시·보고서 고정값을 바꾸므로 채택하지 않았다.
3. 도구 경로는 도구마다 에이전트 플래그 하나로 받는다. 미설정 도구의 작업도 계획하고 `unsupported`로 끝낸다. 계획에서 빼는 안은 공백이 보고서와 단계 표시에서 사라지므로 채택하지 않았다. 그 결과 Staticcheck가 없는 노드의 `static_analysis` 단계는 `partial`이다.
4. 생태계마다 기준 분석기 하나만 필수이며 추가 깊이는 필수가 아니다. capability는 기준 분석기가 probe에 성공할 때만 광고한다.
5. 보고서 관찰의 `raw_severity`는 schema가 이미 문자열을 허용했으나 validator가 null만 받았다. 짧은 단어 등급만 허용하도록 validator를 맞추고 그 밖의 값은 envelope에만 남긴다.
6. 처리 파일 수는 분석기 처리 경로(`coverage.tools[].processed_paths`)의 합집합이다. 팩 finding의 identity는 이력 비교의 공용 `ToolFingerprint`다. 새 migration은 없다.
7. TC-104: 이력 비교는 사라진 도구 finding을 양쪽 규칙 문맥(규칙 팩 id·version·selection, 저장소 설정 처리, 소스 안 억제 처리, 저장소 설정을 읽으면 읽은 파일의 경로·manifest SHA-256 digest)이 알려지고 같을 때만 `resolved_observed`로 본다. 문맥은 보고서 도구 행 `rule_context`(builder 4)에 두고 설정 경로·digest는 코드 제외 export에서 뺀다. go vet 문맥은 v0.1 문서를 바꾸지 않고 decoder와 비교가 합성한다. 억제된 진단을 보고할 수 있는 도구는 `reported`로 관찰에 남기고(finding 아님), 사라진 finding이 억제 관찰과 같으면 `suppressed_in_source`다. 억제를 보고하지 못하는 도구(`honored`)의 사라진 finding은 해결로 보지 않는다. 파일 SHA-256 변화로 수정과 억제 주석을 가르는 안은 구분할 수 없으므로 채택하지 않았다. staticcheck는 `-show-ignored`로 `reported`다. `reported`는 선택한 모든 규칙에서 성립해야 하므로 억제를 보고하지 않는 규칙은 `unreported_suppression_rules`에 적고 그 사라진 finding은 해결로 보지 않는다(staticcheck U1000, 실측).
8. 여러 팩이 쓰는 interpreter는 builtin의 공유 toolchain 표에 한 번 등록한다. 첫 항목은 `node`(`--node`, `--version`, `vN.N.N`)다. 팩은 `Descriptor.Toolchains`·`Interpreter`로 필요를 선언하고, builtin은 시작 시 executor로 한 번 검증한 핸들(경로·디렉터리·버전)을 build 함수에 넘긴다. 없으면 `toolchain_not_configured`, 검증 실패면 `tool_unavailable`이다. 경로는 hello `analyzer_tools`에 예약 이름으로 싣는다. 탐색은 go처럼 런타임 관측으로 기록한다. 도구 스크립트는 `<interpreter> <도구 경로>`로 실행하고 PATH는 interpreter 디렉터리 하나다. `#!` 줄로 실행하지 않는다. 팩마다 interpreter 설정을 두는 안은 세 팩(Python·JS/TS·구조·중복·비밀값)이 서로 다른 의미로 같은 설정을 만들므로 채택하지 않았다. go는 기존 `--go`를 유지한다.

[분석기](ANALYZERS.md)

**A23 — 저장소 코드 실행 허가.** (기록 2026-11-18) REQ-002/015와 TC-103에 대해 다음을 확정한다.

1. 정적 분석 작업이 저장소가 정한 코드(build script, 설정 프로그램, plugin, proc macro, configure 단계)를 실행하는지는 팩이 계획 시 작업마다 선언한다(`pack.Task.RepositoryCode`). 한 도구가 두 방식을 함께 계획할 수 있고, 실행은 선언을 넘지 않는다.
2. 실행 허가는 노드 운영자가 local source root마다 준다(`--repo-code-source-root`). 모델 전송 허가와 같은 경로로 agent 설정 → job `allow_repository_code` → `analysis.Job.AllowRepositoryCode`를 거친다. Git 소스는 받지 않는다. 스냅샷과 저장소 설정은 이 값을 올릴 수 없다.
3. 집행은 `Composite.Run`이 dispatch 전에 한 번 한다. 허가가 없으면 프로세스 없이 `unsupported`(`repository_code_not_allowed`)와 not-run envelope로 끝난다. 이 사유는 도구 미설정 사유보다 우선하고, 도구 사유는 제약으로 남는다. 팩마다 검사하는 안은 누락을 막을 수 없으므로 채택하지 않았다.
4. 이 허가는 [실행](EXECUTION.md)의 신뢰와 실행 권한이 말하는 프로젝트 단위·revision 기반 권한 저장소를 대신하는 노드 운영자 설정이다. 그 권한 저장소는 구현하지 않았다.
5. go vet과 staticcheck는 저장소 코드를 실행하지 않는다. Rust 기준 분석은 build script와 proc macro를 실행하므로 허가 없이 `unsupported`다.

[분석기](ANALYZERS.md)

**A24 — C/C++ 팩.** (기록 2026-11-18) REQ-002/006/007/008/013/015/028과 TC-105~107에 대해 다음을 확정한다.

1. 기준 분석기는 cppcheck(`cpp.cppcheck`, 필수), 추가 깊이는 clang-tidy(`cpp.clang_tidy`)다. cppcheck는 build flag 없이 번역 단위를 읽으므로 compile DB가 없는 스냅샷에서도 실행된다. clang-tidy는 clang frontend로 compile하므로 flag가 필요하다. clang-tidy를 기준으로 두는 안은 compile DB를 커밋하지 않은 대부분의 저장소에서 보고서를 그것만으로 `partial`로 만들므로 채택하지 않았다.
2. Clang Static Analyzer는 clang-tidy의 `clang-analyzer-*` 검사로만 실행하고 `clang --analyze`는 실행하지 않는다.
3. 스냅샷의 compile DB는 데이터다. 경로를 추정한 원래 루트 기준으로 작업 사본에 매핑하고, flag는 허용 목록과 값 형식 검사를 거쳐 CODEYE가 scratch에 다시 쓴다. 항목의 compiler·wrapper·plugin·응답 파일·driver 설정은 실행하거나 읽지 않는다. 두 작업 모두 `RepositoryCode=false`다.
4. compile DB 생성(configure·cmake·bear)은 저장소 코드를 실행하므로 구현하지 않았다. compile DB가 없으면 clang-tidy는 `unsupported`(`compile_db_missing`), cppcheck는 include 경로 없는 축소 모드이며 제약으로 표시한다. 축소 모드에서 include 경로를 추측하는 안은 근거 없는 범위 주장이 되므로 채택하지 않았다.
5. 찾지 못한 헤더는 결함이 아니라 범위 공백이다(`missing_include_files`, 단위 `partial_missing_include`·`failed_missing_include`). 처리 파일은 성공한 작업의 번역 단위만이다.
6. 저장소 `.clang-tidy`는 inline `--config`가 대체한다. compile DB는 `honored` 설정 파일로 digest를 기록한다. cppcheck는 `--inline-suppr` 없이 실행하므로 억제가 `disabled`다. clang-tidy NOLINT는 보고하거나 끌 수 없어 `honored`다.
7. 기준 분석기가 같은 줄에서 함께 보고한 것만 보고한 clang-tidy 검사와, bugprone 검사와 겹치기만 한 Analyzer 검사는 측정 후 선택에서 뺐다. 남긴 검사는 clang-tidy만 보고한 변형이 있다.
8. 저장 전에 clang-tidy의 소스 줄과 작업 사본 밖 위치의 진단, cppcheck의 밖 위치 레코드를 지우고 수만 남긴다. 도구가 운영자 계정이 읽을 수 있는 파일을 읽는 것은 막지 않는다(OS sandbox는 범위 밖).

[분석기](ANALYZERS.md)

**A25 — Python 팩.** (기록 2026-11-18) REQ-002/007/008/012/013/015와 TC-108~110에 대해 다음을 확정한다.

1. 기준 분석기는 ruff다. interpreter 없이 실행되는 단일 바이너리이고 저장소 코드를 실행하지 않으며 의존성 설치와 무관하다. mypy·pyright·bandit은 추가 깊이다.
2. 네 도구 모두 저장소 설정을 CODEYE 관리 설정으로 통째로 대신한다(ruff `--isolated`, mypy `--config-file=`, pyright scratch `-p`, bandit scratch `--ini`). mypy 경로 plugin은 Python 코드 실행이라 저장소 설정을 따르는 방식(`RepositoryCode=true`)은 두지 않았다. 그래서 모든 task가 `RepositoryCode=false`이고, 밖을 가리키는 설정 값을 읽어 거부할 경로 자체가 없다.
3. mypy·pyright는 대안이다. 둘 다 실행하려면 agent 플래그 `--python-both-type-checkers`가 필요하고 두 task에 `python_type_checkers_both_selected`를 남긴다. hello에 선택 필드를 더하는 안은 프로토콜을 바꾸므로 채택하지 않았다.
4. 대상 Python 버전은 env.discover 규칙으로 읽어 명시 전달한다. 팩은 discovery 아래에 있어 import할 수 없으므로 reader 셋을 규칙 그대로 옮기고 생성 표본 시험으로 discovery와 같음을 고정한다. 선언이 없으면 3.14와 `codeye_default`·`python_version_unknown`이다.
5. ruff S 규칙은 bandit과 중복이라 고르지 않는다. 타입 검사기의 ruff 중복 검사(`name-defined`·`no-redef`·`str-format`·`await-not-async`, pyright 다섯 규칙)와 bandit B613은 끈다.
6. ruff·bandit 억제는 억제를 끈 두 번째 실행과의 차이로 `reported`다. mypy·pyright는 보고 방법이 없어 `honored`다.
7. 수집 stream에는 소스 줄을 두지 않는다. ruff `--unfixable ALL`, bandit JSON은 scratch 파일이고 관찰은 CSV stdout에서 읽는다. 작업 공간 밖 파일의 진단은 버리고 개수만 남긴다. 위치 인자는 `--` 뒤에 `./`를 붙여 둔다.

[분석기](ANALYZERS.md)

**A26 — JS/TS 팩.** (기록 2026-11-18) REQ-007/008/012/013/015와 TC-111~113에 대해 다음을 확정한다.

1. 생태계 기준은 ESLint 구문 규칙(`javascript.eslint`)이다. 스냅샷에 node_modules가 없어 tsc는 실제 저장소에서 대부분 의존성 공백으로 끝나기 때문이다(실측). tsc와 type-aware ESLint는 추가 깊이다.
2. 세 작업 모두 저장소 코드를 실행하지 않는다. 저장소 ESLint 설정을 따르는 실행은 구현하지 않는다. 관리 설정과 `--no-config-lookup`·`--no-inline-config`·빈 suppressions 파일을 쓰고, plugin은 도구 디렉터리 절대 경로로만 읽는다.
3. 의존성 공백이 있는 tsc·type-aware 실행은 실패이며 진단을 모두 보류한다. 공백 실행의 TS2322가 의존성 누락의 결과였기 때문이다(실측). 공백 코드만 빼고 나머지를 결함으로 내는 안은 채택하지 않았다.
4. eslint JSON은 `-o` 파일이 아닌 stdout으로 받는다. `raw_ref`가 기록된 흐름에 묶여야 하기 때문이다.
5. tsconfig는 비신뢰 입력이다. `generateTrace`·`generateCpuProfile`은 값과 무관하게 거부한다. emit·tsbuildinfo 경로와 읽기 경로 옵션은 스냅샷 밖일 때 거부하고 옵션 이름만 기록한다. 스냅샷 안 emit 경로를 모두 거부하는 안은 채택하지 않았다. 적용 플래그 아래에서 아무것도 쓰지 않음을 canary로 쟀고, 거부하면 outDir을 쓰는 대부분의 저장소가 tsc 대상에서 빠진다.
6. 저장소에서 온 위치 인자는 `./` 접두로, eslint는 `--` 뒤에 둔다.

[분석기](ANALYZERS.md)

**A27 — Rust 팩.** (기록 2026-11-18) REQ-002/007/008/012/013/015/028과 TC-114~116에 대해 다음을 확정한다.

1. 기준 분석기는 cargo check(`rust.cargocheck`, 필수), 추가 깊이는 clippy(`rust.clippy`)다. cargo는 Rust 프로젝트가 있는 노드에 항상 있고 rustc 진단이 빌드가 말하는 사실이며, clippy는 rustup 구성요소라 없을 수 있다.
2. 두 작업 모두 build script·proc macro·저장소의 Cargo 설정을 실행하므로 `RepositoryCode=true`다. 노드 허가가 없으면 기준 분석도 `unsupported`(`repository_code_not_allowed`)이고 version probe 밖의 프로세스를 시작하지 않는다. 허가가 있는 작업은 계획에서 정한 것보다 더 실행하지 않는다.
3. 도구는 toolchain bin 디렉터리의 실제 `cargo`(`--cargo`)와 같은 디렉터리의 `cargo-clippy`(`--clippy`)다. rustup proxy는 저장소의 `rust-toolchain`이 toolchain을 고르고 내려받게 하므로 거부한다(resolve 결과가 cargo가 아니거나 같은 디렉터리에 `rustup`이 있음). PATH는 그 디렉터리 하나, `RUSTC`는 옆의 rustc다.
4. 링커: rustc가 build script를 링크하려면 `cc`가 필요한데 toolchain 디렉터리 PATH에는 없다(실측: `linker cc not found`). 새 도구 플래그를 만들지 않고 기존 운영자 설정 `--toolchain-path`를 hello `tool_path`로 builtin에 넘겨(`builtin.Config.ToolPath`, 추가 필드) 시작 시 `cc`를 한 번 찾고 그 절대 경로만 host target의 linker로 준다. 목록 자체를 자식의 PATH로 주지 않는다. `cc`가 없으면 링크가 필요한 작업만 `linker_unavailable`로 실패한다.
5. `--offline --locked`, `CARGO_NET_OFFLINE=true`, `CARGO_HOME`과 target은 task scratch다. 운영자 레지스트리 캐시 플래그는 만들지 않았다(cargo는 오프라인에서도 CARGO_HOME에 쓰므로 읽기 전용 캐시를 줄 수 없다). 의존성은 경로 의존성이나 저장소가 vendor한 소스뿐이며, 없으면 `dependency_unavailable`이다.
6. build target은 host로, rustc wrapper는 비움으로 환경에서 고정한다(저장소 `.cargo/config.toml`의 `build.target`·`rustc-wrapper`보다 환경이 앞선다는 것을 실측). 나머지 저장소 설정(rustflags, `[lints]`, `clippy.toml`)은 따른다: `repository_config=honored`, 읽은 파일을 `config_files`에 기록하고 작업 사본 위 디렉터리에 Cargo·clippy 설정이 있으면 `null`(실측: cargo와 clippy 모두 조상 디렉터리 설정을 읽는다).
7. 소스 안 억제는 `honored`다. `#[allow]`가 숨긴 진단을 보고하는 옵션이 없고 `--force-warn`은 숨긴 진단을 표시 없이 일반 경고로 내므로 억제와 구분할 수 없다(실측). 따라서 사라진 finding은 해결되지 않는다(`source_suppressions_hidden`).
8. clippy는 `cargo clippy`가 아니라 `cargo-clippy`로 실행한다(저장소 `[alias] clippy`가 외부 하위 명령을 가린다는 실측). lint 수준은 `--cap-lints warn`으로 낮춰 deny lint가 빌드를 실패시키고 의존 crate 검사를 막지 않게 하고, clippy가 함께 출력하는 rustc 진단은 버려 cargo check와 중복하지 않는다(실측으로 같은 집합임을 확인). 별도 cap이 없는 cargo check는 deny lint를 관찰로 남기고 `lints_denied`로 실패한다.
9. unsafe·panic review는 clippy의 allow-by-default lint 여섯 개를 켜서 한다(`undocumented_unsafe_blocks`, `multiple_unsafe_ops_per_block`, `panic_in_result_fn`, `unwrap_in_result`, `todo`, `unimplemented`). `unwrap_used` 등 더 시끄러운 restriction lint는 테스트 코드에서 쏟아지므로 켜지 않았다.
10. 모듈은 가장 바깥 Cargo.toml 디렉터리마다 하나이고 `--workspace --all-targets`, 기본 feature, host target으로 실행한다.

[분석기](ANALYZERS.md)

**A28 — 구조·중복·비밀값 팩.** (기록 2026-11-18) REQ-007/008/011/012/013/015/031, NFR-006과 TC-117~119에 대해 다음을 확정한다.

1. lizard(`any.lizard`)·jscpd(`any.jscpd`)·gitleaks(`any.gitleaks`)는 언어와 무관한 추가 깊이 분석기이고 세 작업 모두 `RepositoryCode=false`다. 저장소의 프로그램·plugin을 실행하지 않는다.
2. 구조·중복 결과는 envelope 관찰의 가산 필드 `metric{name, value, threshold, unit}`으로 표현한다. 지표 관찰은 활성 관찰이 아니다(`Active()`). finding·결함 수·조치 수·모델 입력이 되지 않는다. 보고서는 `summary.metric_observations`(0이면 생략)로 따로 센다. 새 진단 종류나 별도 산출물을 만들지 않은 이유가 있다. 원시 관찰의 결속(stream·byte 범위)과 위치 검증을 그대로 쓰고, `raw_observations`·`unresolved_observations`의 뜻을 바꾸지 않기 때문이다.
3. 위치를 확인하지 못한 지표는 미해결 관찰로 세어 판정을 `inconclusive`로 둔다. 측정값이어도 근거가 스냅샷과 맞는지 확인하지 못한 결과는 깨끗한 판정의 근거가 아니다.
4. 임계값은 규칙 팩 v1로 고정한다: CCN ≥ 16, 중복 minLines 10·minTokens 100·mode mild, 1,000,000바이트·UTF-8. 데이터 디렉터리(숨김·`testdata`·`vendor`·`node_modules`)는 지표에서 빼고 센다. 복잡도는 lizard, 중복은 jscpd만 잰다(`-Eduplicate`·jscpd complexity 미사용).
5. 비밀값 엔진은 gitleaks `dir`이다. CODEYE 템플릿이 규칙·파일·줄·열·entropy만 출력하고 `--redact=100`, info 로그를 쓴다. 관찰 문구는 고정이고 식별은 `ToolFingerprint(analyzer, rule, path, "", 고정 문구)`다. 비밀값의 hash는 어디에도 두지 않는다. 열은 gitleaks 8.30.1이 첫 줄 뒤에서 1씩 크게 보고하므로(실측) 기록하지 않는다. ruff S105·bandit B105와 합치지 않는다.
6. 저장소 설정: lizard·jscpd는 대체한다(`overridden`). gitleaks는 `.gitleaks.toml`을 대체하고, 끌 수 없는 `.gitleaksignore`는 검증 뒤 digest와 함께 따른다(`honored`). 사본 밖 항목은 값을 기록하지 않고 거부한다(`repository_config_rejected`). 인라인 allow는 끈다(`disabled`). 읽은 파일이 없는 `recorded` 문맥은 빈 목록의 digest를 쓰고 경로를 두지 않는다. 저장 뒤 다시 읽어도 같도록 검증을 맞췄다.
7. 모델 입력 선별은 세 경우다. 실행 안 됨이면 보내고 제약을 남긴다. 실행했으나 쓸 수 없으면 검토를 `secret_screen_failed`로 건너뛴다. 성공이면 탐지 파일을 뺀다. review-v1에도 적용하되 고정 요청 바이트는 바꾸지 않는다.
8. 보고서 builder 버전은 "4"로 둔다. 지표가 없는 보고서의 바이트가 그대로이고, 새 필드는 생략 가능한 가산 필드이며, 이전 revision의 비교 규칙이 바뀌지 않기 때문이다. 대가로 builder 버전만으로는 지표 표시 이전·이후를 구분할 수 없다.

[분석기](ANALYZERS.md)

## 모델 검토

**A21 — review-r1 단계 기록과 필수 검토 역할.** (기록 2026-11-18) REQ-009/010/011/013/027과 TC-97에 대해 다음을 확정한다.

1. review-r1 단계 기록은 같은 media type의 schema `0.2`다. 단계마다 자기 호출만 담고, 읽은 앞 단계·탐색·도구 산출물을 SHA-256으로 묶는다. 보고서는 이 값과 수락한 산출물을 대조한다. `0.1` 기록에 팩 필드를 더하는 안은 review-v1 기록·요청 바이트를 바꾸므로 채택하지 않았다. v1처럼 앞 단계 호출을 누적하는 안은 기록이 커지고 앞 단계 결과를 다시 검증해야 하므로 채택하지 않았다.
2. 필수 검토 역할은 계획된 `review.*` task의 `required_for_verdict`로 정한다. 환경 영향과 정적 진단 해석은 필수가 아니다. 문서 검토는 계획 시 범위 안에 안내 문서가 있을 때만 필수다. 필수 task가 없으면 `not_performed`다. review-r1 역할 key가 없는 계획은 review-v1의 세 역할(context·investigate·verify)로 해석한다.
3. 실행·재현 주장 거부는 정규식 휴리스틱이다. 놓친 주장이 있어도 상태·재현·통계는 구조화 기록에서만 정하므로 모델 서술이 판정을 바꾸지 못한다.

[모델 검토](REVIEW.md)

**A29 — 언어 지침 선택과 사례 없는 지침.** (기록 2026-11-18) REQ-027과 TC-100에 대해 다음을 확정한다.

1. 언어 지침은 스냅샷 scope 안의 소스 생태계로 고른다. 판별은 분석기 팩의 공백 계획과 같은 `pack.InScope` 하나다([분석기](ANALYZERS.md)의 범위에 없는 생태계와 팩 공백). 소스 확장자로 세고 data·vendor 디렉터리는 빼며, build 파일만으로는 생태계가 들어오지 않는다. 새 판별을 만들지 않았다. 고른 지침은 공통·역할·영역 지침 뒤에 생태계 이름 순서로 붙는다. 지원 생태계가 없으면 붙이지 않고, 지침이 없는 생태계는 아무것도 더하지 않는다. 언어 생태계(`obsdoc.Ecosystems`에서 `any`를 뺀 것)마다 지침이 있음을 시험으로 고정했다. 모든 지침을 항상 붙이는 안은 요청을 늘리고 범위 밖 언어의 관점을 섞으므로 채택하지 않았다. Go 지침만 붙이는 이전 방식은 다른 언어 스냅샷에 맞지 않는다.
2. 역할별 digest는 pack.yaml과 고른 파일만 덮는다. 따라서 digest는 (역할, 생태계 조합)마다 다르다. 호출 기록의 `pack_files`가 쓴 언어 지침을 보이므로 기록 schema는 바꾸지 않았다. 같은 사실을 담는 별도 필드는 두지 않았다.
3. 지침 파일에는 평가·시험 사례의 내용을 쓰지 않는다. 사례 ID, fixture 경로와 식별자, 특정 문서·재시도 횟수·버전 조합이 여기에 해당한다. 예시가 필요하면 사례 없는 규칙으로 쓴다. 이전 지침에 있던 사례 유래 예시는 모두 지우거나 규칙으로 바꿨다. `bundle_test.go`가 사례 ID와 fixture 이름을 검사한다. 같은 뜻의 다른 문장은 이 검사로 잡지 못하므로 지침을 바꿀 때 검토에서 확인한다.
4. 다섯 생태계가 모두 범위에 있으면 지침이 7,370바이트 늘어난다. 이때 합성 최악 입력의 검증 역할은 최대 길이 후보 6개 중 1개를 보내지 못한다. 그 후보는 `unreviewed`(`verification_budget_exceeded`)로 남고 task는 `review_input_incomplete`로 실패하며, 성공으로 바꾸지 않는다. 요청 한도(40,000바이트)와 역할별 지침 구성은 바꾸지 않았고 소유자 결정으로 남긴다. 다섯 언어 코드 발췌를 넣은 뒤의 측정과 역할별 지침 선택을 채택하지 않은 이유는 A31에 있다.
5. review-v1과 `TestLegacyPackIsPinned`는 바꾸지 않았다. 이 결정 시점의 팩은 실모델로 평가하지 않았고 기본으로 승격하지 않았다. 기본 팩 결정은 A33이다.

[모델 검토](REVIEW.md)

**A31 — 다섯 언어 검토 입력.** (기록 2026-11-19) REQ-004/009/027과 TC-123에 대해 다음을 확정한다.

1. **종류 판별.** 발췌의 code·test·config는 경로만으로 정한다(`record/source.go`). 같은 규칙을 review 입력 선택과 worker·보고서 decoder가 쓴다. code는 `pack.SourceFiles`와 같은 소스 확장자이고 시험이 두 표를 같게 묶는다. test는 언어별 이름·디렉터리 관례, config는 고정 manifest 목록이다. Go 규칙(`*_test.go`, go.mod)은 바뀌지 않아 이전 기록이 그대로 읽힌다. JSON schema의 제약은 바꾸지 않았고 설명에 경로 규칙을 적었다.
2. **review-v1 코퍼스.** 순위는 진입점, 문서, 소스, manifest, 테스트다. 예산·읽기·비밀값 선별은 기존 코퍼스 규칙과 같다. Go·README/AGENTS·go.mod 분기는 그대로 두고 다른 생태계만 덧붙였다. 그래서 Go 저장소의 선택은 구조적으로 바뀌지 않는다. vendor 아래 Go 파일도 계속 후보다. 다른 생태계의 파일과 manifest는 `pack.InScope`에 그 생태계가 있을 때만 넣는다. 팩 지침(Go 지침만)은 승격 근거 없이 바꿀 수 없어 그대로다.
3. **review-r1.** 다른 언어 파일은 그 생태계 manifest의 가장 깊은 디렉터리로 묶는다. 순회는 생태계마다 첫 묶음부터 돈다. 발췌는 파일 첫 줄부터이고 해석 창은 진단 위치 주변의 줄 창이다. 구문 분석기는 없다. 호출자 확장은 Go만 한다(`caller_expansion_go_only`). Go만 있는 스냅샷의 요청은 변경 전 캡처와 같다.
4. **제외.** 분석기 팩의 data 디렉터리 규칙에 더해, Go가 아닌 파일은 모든 깊이의 `build`·`dist`·`target`·`coverage`·`htmlcov`·`site-packages`·`venv`·`__pycache__`·`third_party` 아래에서 뺀다. 중첩 산출물을 모델에 보내지 않기 위해서다. 같은 이름의 소스 디렉터리도 빠지는 비용을 받아들였다. 이렇게 빠진 범위 안 소스는 `source_in_excluded_directories`로 기록한다. 이 규칙은 `pack.InScope`보다 넓다. 그래서 언어 지침은 붙지만 그 언어 코드가 없는 요청이 생길 수 있다. Go는 이 규칙에서 뺐다. internal/build 같은 Go 패키지가 흔하기 때문이다.
5. **언어 지침은 범위 안 생태계 규칙(A29)을 유지한다.** 역할 입력에 있는 언어만 고르는 안을 시험 구현했다. 큰 다섯 언어 최악 입력에서 verify는 두 규칙 모두 최대 후보 6개 중 5개를 보냈다. 조사 역할은 입력에 다섯 언어가 모두 있어 지침이 줄지 않았다. 줄어든 것은 한도 압박이 없던 해석 역할(32,223→26,676바이트)뿐이었다. 압박을 줄이지 못하고 digest 조합만 늘리므로 채택하지 않았다. 요청 한도는 소유자 결정으로 남는다.
6. **평가.** 이 결정은 실모델로 평가하지 않았다. 언어별 코드가 입력에 들어간다는 것은 확인했지만 검토 품질이 나아졌다는 근거는 아니다.

[모델 검토](REVIEW.md)

**A33 — 기본 검토 팩 review-r1 2.0.0.** (기록 2026-11-19) REQ-010/011/013/027, NFR-007/008/010과 TC-100, TC-128·TC-129에 대해 다음을 확정한다.

1. **기본 팩.** 소유자가 검토 팩을 하나로 합치기로 했다. `review-r1`을 2.0.0(`status: default`)으로 올려 팩을 지정하지 않은 에이전트·워커·개인 설정의 기본값으로 쓴다. `review-v1`은 지우지 않고 `--review-pack review-v1`로만 고르는 이전 팩으로 둔다. 기존 기록이 v1의 팩 id·digest로 해석되고 `TestLegacyPackIsPinned`가 재현 가능한 요청 바이트를 고정하기 때문이다. 디렉터리·id 이름을 바꾸는 안은 기존 기록의 팩 대조와 운영자 설정을 깨므로 채택하지 않았다. 근거는 보류 평가의 두 팩 비교다([평가 결과](EVALUATION-RESULTS.md)). 이 근거로 review-r1이 더 우월하다고 표시하지 않는다.
2. **v1 규칙 통합.** 팩마다 따로 고치고 비교하는 대신 v1 규칙을 r1에 대조해 옮긴다. 기준은 r1에 없고, 근거 없는 보고나 실행 주장을 막는 정밀도 규칙이거나 개발 입력에서 관찰한 오탐 유형을 다루는 것이다. 평가 결과를 읽지 않은 작성자가 대조했고 한 문장을 verify 지침에 옮겼다. 재현율·수정 조언 항목은 요청 크기를 늘리므로 옮기지 않았다.
3. **복구 경로.** JSON이 아닌 응답은 항목 정보가 없으므로 형식 복구를 쓰지 않고 한 번 재시도한다. 단계당 최대 3회 호출이고 기록 schema `0.2`의 상한을 3으로 넓혔다. verify에서 그 요청에 플랫폼이 넣은 관찰 ID만 틀린 항목은 그 ID를 빼고도 판정 규칙을 통과할 때만 받아들이고 `evidence_id_dropped`를 남긴다. 요청에 없던 ID는 빼지 않고 거부한다. 모르는 ID를 모두 빼는 안은 지어낸 근거를 받아들이는 길을 열어 채택하지 않았다. 후속 호출이 형식 이외의 이유로 실패하면 수락한 판정은 유지하고 나머지를 격리한다(`verification_call_failed`). 판정을 만들거나 미완료를 완료로 바꾸지 않는다. 보고서의 `review_calls` 상한은 15다. 지침·계약 문구로 막는 안은 보류 평가의 독립성을 지키기 위해 채택하지 않았다. 횟수 제한 없는 재시도는 비용 상한이 없어 채택하지 않았다.
4. **회귀 검증.** 지침 교체마다 두 팩을 나란히 비교하지 않는다. 기본 팩 개정은 기본 회귀 집합(`fixtures/r0-quality/`)을 사전 기준으로 실행하고, 독립 품질 주장은 새 보류 사례로만 한다. 2.0.0은 같은 팩 id의 다른 digest이므로 보류 집합 재실행은 `holdout_seen_by_prior_revision`이다.

[모델 검토](REVIEW.md) · [평가](EVALUATION.md)

## 품질 평가

**A30 — 품질 평가 runner.** (기록 2026-11-19) REQ-006/011/013/027, NFR-010과 TC-120~122에 대해 다음을 확정한다.

1. 정답 차단은 모델로 가는 모든 텍스트가 지나는 relay에서 한다. runner가 제출 전에 입력을 검사하는 안은 agent와 OpenCode가 실제로 만든 프롬프트를 볼 수 없어 채택하지 않았다. 일치한 요청은 가리거나 고쳐 보내지 않고 막는다. 가리면 실험 입력이 바뀌기 때문이다. 막힌 사례는 무효로 빼고 차단 수를 따로 보고한다. 모델이 앞 역할에서 쓴 표현이 다음 역할 입력에 들어가는 경우도 기본값은 막는 것이다. 다만 결함을 맞힌 사례일수록 막혀 recall이 낮게 치우치므로, relay가 직접 돌려준 답의 ID만 예외로 하는 `--allow-model-echo`를 선택 옵션으로 두고 runner가 사례 단위로 다시 확인한다. 방식은 manifest와 채점 결과에 남는다. 목록은 라벨 문구 중 입력과 명시한 허용 텍스트(지침·도구 진단·역할 계약 envelope)에 없는 것만 담는다. 허용 텍스트는 자동으로 더하지 않고 SHA-256을 남긴다.
2. dry run은 relay의 명시적 `--test-upstream`(loopback만)과 가짜 upstream으로 한다. 환경변수나 기본값 변경은 두지 않았다. 시험 장부는 header에 표시되어 실제 장부와 서로 거부한다.
3. holdout 재사용은 막지 않고 표시한다. 같은 팩 id에 다른 digest의 실제 실행이 먼저 있으면 `holdout_seen_by_prior_revision`이고, 이 표시가 있는 batch를 포함한 비교는 독립 비교가 아니다. dry run은 본 것으로 세지 않는다.
4. 예산은 relay의 예약·상한이 보장한다. runner의 계획은 그 위의 사전 거절이다. 상한 추정은 팩의 모든 역할이 변형마다 호출되고 형식 복구 10%와 여유 1.5배가 붙는다고 본다. 사례별 장부 귀속을 위해 review 조건은 사례를 하나씩 실행한다.
5. 지표 분모는 사람이 판정한 단위이고 미판정 수를 함께 낸다. 위치 자동 대조는 판정이 아닌 구조 지표다. 비용·시간과 정확도는 한 점수로 합치지 않는다. 보고서 읽기는 사람의 답이 있을 때만 측정하고 모델로 흉내 내지 않는다.
6. 평가 중 `validate_bundle.check_report`와 제품의 보고서 검증이 두 군데 어긋난 것을 발견했다. 검사기는 원시 관찰 stream을 stdout만 받았지만 제품은 stdout·stderr를 모두 허용하고 cppcheck는 stderr에 쓴다. 검사기는 기록된 규칙 문맥에 설정 경로와 digest를 모두 요구했지만 제품은 digest만 요구하고 도구가 읽은 파일이 없으면 경로를 생략한다(비밀값 팩). 둘 다 검사기를 제품 계약에 맞췄다.
7. 검토 결과는 task 상태로 분류한다. 비밀값 선별이 실패해 검토 역할이 `skipped_policy`(`secret_screen_failed`)이면 정책 결과로 따로 세고 모델 실패로 세지 않는다. 보고서의 `llm_exposed_files`는 수만 주고 README 같은 문서 발췌도 세므로 코드 노출의 지표로 쓰지 않는다(`model_saw_snapshot_files`). 코드 노출은 보존한 검토 기록의 근거 목록(경로·종류)에서 낸다(`model_saw_code`, `labelled_file_in_investigate`). 기록은 review input의 근거 목록을 담으므로 요청 dump가 없는 유료 batch에서도 계산되고, dry run 변형에서 요청 dump와 같음을 확인했다.

[평가](EVALUATION.md)

## 확인할 환경 정보

| 항목 | 확인 방법 | 설계 영향 |
|---|---|---|
| 개인 PC OS/arch/RAM/local disk | doctor, 사용자 환경 확인 | 첫 배포 타깃·resource budget |
| 사내 cgroup·서비스 권한·로컬 디스크 | 실제 머신 probe | Native enforcement·상주·cache 위치 |
| OpenCode 버전/API/모델 제한 | live contract·workspace/cancel 시험 | adapter profile·동시 호출 상한 |
| GHES 버전/App 권한 | 연결 진단 | endpoint·webhook·댓글 지원 |
| toolchain·분석기 mirror | 설치·version·fixture 시험 | language capability matrix |
| 실제 개인 프로젝트 구성 | 권한 있는 snapshot 탐색 | 언어 팩 우선순위와 후속 파일럿 순서 |
| 사내 브라우저 | 실제 UI smoke test | JS target·SSE fallback |
| 프로젝트 라이선스 | 소유자 결정 | 배포·외부 기여 조건 |
| RHEL 8.10 실기 | 내부 미러·CA가 있는 실제 머신에서 설치·분석 | 폐쇄망 배포팩·NFR-001 |
| 다중 물리 노드·NFS·네트워크 분할 | 여러 머신의 노드 등록과 장애 훈련 | lease 안전 여유·NFS 접근 차이·REQ-019 |
| 실제 독자의 보고서 읽기 | 사람의 응답을 받는 평가 | 보고서 구성·평가 지표 |

미확정 사항 때문에 문서 작성이나 Native 구조를 중단하지 않는다. 해당 항목은 capability와 명확한 실패 상태로 표현한다. 컨테이너 사용 가능 여부는 선택 실행기 검증 항목이지 출시 필수 전제가 아니다.

## 변경 절차

영향 받는 요구 ID, 변경 이유, 대안, migration/호환 영향, 보안/품질/운영 비용, 수정할 schema/API/fixture/수용 테스트를 기록한다. 숫자 기본값을 바꿀 때 실제 측정과 단순 추정을 구분한다. 모든 질문을 사용자 폼 필수 입력으로 해결하려 하지 않는다. 새 결정은 다음 A 번호를 받고 이 문서의 주제 절에 넣는다. 기록일을 적는다.

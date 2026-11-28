# 결정 기록

확정된 제품 결정(D), 설계 기본안과 구현 결정(A), 아직 확인할 환경 정보, 결정을 바꾸는 절차를 모은다. ID는 바꾸지 않는다. 각 결정은 영향 받는 요구 ID([요구사항](REQUIREMENTS.md))와 수용 시험 ID([시험](TESTING.md))를 적는다.

날짜: A11~A13의 2026-11-17은 원래 결정 기록의 날짜다. A14 이후의 날짜는 결정을 처음 기록한 커밋의 날짜(기록일, 커밋 기준)다.

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

**A14 — 저장 불변조건.** (기록 2026-11-18) pgx/v5 5.13.0과 내장 SQL migration을 사용한다. migration은 명시적 `codeye migrate`로만 실행하고 checksum·동시 적용·rollback을 검증한다(개인 모드의 관리형 PostgreSQL은 `up`·`start`가 사본을 쓴 뒤 적용한다, A38). attempt는 leased/recovery_required/released, cleanup은 pending/uncertain/confirmed다. 만료/불확실 cleanup은 소유 슬롯을 유지하며 확인 후 failed로 정리한다. 명시적 failed/cancelled 재시도는 보관 snapshot과 확인된 cleanup만 허용한다. 동일 완료 receipt 재전송은 새 결과를 게시하지 않는다. event sequence와 entity version을 구분하고 task_executions로 재시도 전 이력을 보존한다. 성공 보고서 게시 게이트(A09)와 자원 admission(A18)은 이 저장 계층 위에 있다. 오류·호환·migration 세부는 [아키텍처](ARCHITECTURE.md)를 따른다.

**A15 — 개인 모드 인증.** (기록 2026-11-18) loopback 전용 `codeye serve`를 제공한다. controller+agent를 함께 띄우는 `start`는 별도 supervisor 명령이다(A32에서 구현). CLI에서 발급하는 일회용 launch link는 0600 파일에 저장하며 DB에는 token hash만 보관한다(`codeye up`·`open`은 터미널에 출력한다, A38). 세션 cookie·same-origin Host/Origin·CSRF를 강제하고 비구성원은 404로 존재를 숨긴다. migration 0002는 forward migration이며 배포/doctor에서 자동 적용하지 않는다. source 접근은 declared_unverified이고 worker 연결 전 queued를 성공으로 바꾸지 않는다. 브라우저 CSP 호환을 위해 JSON validator는 빌드 시 생성한다. [API](API.md)를 따른다.

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

## 한 머신의 서비스 운영

**A32 — 개인 패키징과 운영.** (기록 2026-11-19) REQ-018/021/022, NFR-003/006/007과 TC-124~127, TC-32에 대해 다음을 확정한다.

1. **프로세스 관리.** `codeye start`가 controller와 로컬 에이전트를 setsid로 띄우고 pid와 프로세스 시작 시각을 0600 기록에 남긴다. 신호는 pidfd를 연 뒤 시작 시각이 같을 때만 보낸다. pid가 재사용된 다른 프로세스에 신호가 가지 않게 하기 위해서다. pidfd가 없는 커널에서는 신호를 보내지 않고 `recovery_required`로 남긴다. 강제 종료는 하지 않는다. systemd·Windows Service 등록은 OS마다 달라 하지 않았다. Windows는 빌드되며 명시적 미지원 오류를 낸다.
2. **데이터베이스.** 운영자가 준비한 PostgreSQL(`CODEYE_DATABASE_URL`)만 구현했다. 관리형 모드를 넣으면 initdb·포트·버전 업그레이드·데이터 디렉터리 백업 책임이 CODEYE로 넘어온다. 그래서 설정에 이름만 두고 거부한다. start는 migration을 하지 않는다. schema 변경은 운영자가 백업한 뒤 `codeye migrate`로만 한다. (A38에서 관리형 모드를 구현해 기본으로 바꿨다. external 모드는 이 규칙 그대로다.)
3. **정지.** drain(SIGUSR1) → 시간 초과 때 취소(SIGTERM) → 에이전트 journal로 정리 판정 → controller 정지 순서다. 에이전트가 끝나지 않으면 controller를 남긴다. 그래야 정리 보고를 계속 받을 수 있다. 취소된 run은 에이전트 종료의 기존 의미대로 `failed`(`agent_shutdown`)다. 자동으로 다시 대기열에 넣지 않았다. 그 판단은 재시도가 이전 attempt 결과를 덮지 않는 규칙(REQ-035)과 함께 정해야 한다.
4. **저장 상한.** 적용은 claim 트랜잭션 한 지점이다. 노드 영역은 controller가 노드 파일시스템을 볼 수 없어서 에이전트가 재고 heartbeat로 보고한다. artifacts는 controller 저장소의 원천인 DB에서 sha256마다 한 번 센 크기 합으로 정한다. 자동 삭제는 하지 않는다. 참조·pin·보존 규칙 없이 지우면 근거를 잃기 때문이다. 정리 정책은 후속이다.
5. **백업 일관성.** export한 DB snapshot 하나에서 dump와 확정 산출물 목록을 함께 얻는다. 산출물은 내용 주소이고 확정 뒤 바뀌지 않는다. 그래서 복사 시점이 달라도 hash로 같은 바이트임을 확인할 수 있다. DB 잠금이나 정지 강제는 개인 사용 중 백업을 막아 채택하지 않았다. 다만 정지 뒤 백업을 권장한다.
6. **복원.** 빈 DB, 빈 산출물 디렉터리, 새 `state_dir`에만 복원한다. 병합이나 덮어쓰기는 ID 충돌과 근거 혼합 위험이 있어 하지 않는다. 복원 직후 노드·세션·쓰지 않은 토큰을 무효로 하고 `leased` attempt를 `recovery_required`로 둔다. 예약은 유지한다. 철회된 노드의 `recovery_required` attempt를 운영자가 정리하는 명령은 아직 없다(열린 항목).
7. **진단.** doctor는 선택 필드 `operations`만 더한다. `schema_version`은 0.1 그대로이고 `--config` 없는 출력은 바뀌지 않는다.

[운영](OPERATIONS.md) · [사용 안내서](GUIDE.md)

**A38 — `codeye up`과 관리형 PostgreSQL 기본.** (기록 2026-11-19) REQ-001/004/015, NFR-003/006과 TC-170~175에 대해 다음을 확정한다. A32의 2와 A14·A15의 일부를 개인 모드에서 바꾼다.

1. **한 명령으로 시작.** 소유자가 아직 제품을 써 보지 못했고, 첫 보고서까지 사람이 하는 단계(PostgreSQL 준비·URL export·설정 작성·migrate·start·링크 파일 열기·root와 경로 입력)가 많았다. `codeye up [PATH]`이 설정 파일 생성, PostgreSQL, schema, controller·에이전트, 프로젝트 등록과 분석 요청, 로그인된 브라우저까지 한다. `start`·`stop`·`status`는 같은 핵심을 쓰고, 이미 실행 중인 프로세스는 그대로 쓴다. 에이전트 argv가 설정과 달라지면(새 root) drain으로 실행 중인 작업을 끝내게 한 뒤 다시 띄운다. drain 시간 안에 끝나지 않으면 취소하지 않고 다시 하라고 알린다. 실행 중인 분석을 버리지 않기 위해서다.
2. **관리형 PostgreSQL이 기본.** `database.mode`를 비우면 `managed`다. 설치된 프로그램(주 버전 14 이상)으로 `<state_dir>/postgres/data`에 cluster를 만들고 127.0.0.1의 빈 포트에서 scram-sha-256으로만 받는다. PostgreSQL을 내려받거나 시스템 서비스를 바꾸지 않는다. 명시한 프로그램 디렉터리가 조건을 채우지 않으면 다른 곳으로 넘어가지 않고 오류다. 사용자가 고른 경로를 조용히 바꾸지 않기 위해서다. 데이터 디렉터리와 주 버전이 다른 프로그램은 거부한다(pg_upgrade는 하지 않는다. 백업·복원이 길이다). external 모드는 이전과 같다. A32 2의 우려(초기화·포트·버전·백업 책임)는 위 규칙과 기존 백업·복원으로 다룬다.
3. **비밀번호는 상태 디렉터리에.** 생성 비밀번호를 `<state_dir>/postgres/password`(0600, 0700 디렉터리)에 둔다. "상태 디렉터리에 DB 인증정보를 두지 않는다"(A32)를 관리형 모드에서 바꾼다. 그 비밀번호는 이 상태 디렉터리의 cluster에만 쓰이고, 그 cluster의 데이터 파일이 이미 같은 디렉터리에 있어 파일을 읽을 수 있는 사람은 비밀번호 없이도 데이터를 읽는다. 비밀번호를 사람이 관리하게 하는 대안은 다시 수동 단계를 만든다. controller에는 환경변수로만 주고 설정·로그·출력에는 쓰지 않는다. `codeye` 사용자는 cluster superuser다(전용 cluster라 권한을 나누는 이득이 작다).
4. **schema 자동 적용.** 관리형 모드의 `up`·`start`는 이전 버전 schema에 migration을 적용한다. 데이터가 있으면 먼저 `pg_dump --format=custom` 사본을 백업 디렉터리에 쓰고, 사본이 실패하면 아무것도 바꾸지 않는다. 바이너리보다 새 schema와 불일치 schema는 거부한다. 사본은 같은 시점 백업 세트(A32 5)가 아니라 되돌림용 dump다. 백업 세트의 snapshot 쿼리가 이전 schema를 가정할 수 없기 때문이다. external 모드는 계속 `codeye migrate`만 schema를 바꾼다.
5. **`PG*` 변수 허용.** 관리형 모드는 셸의 `PG*` 변수와 `CODEYE_DATABASE_URL`을 프로세스에서 지우고 알린다. 저장 계층의 fail closed는 그대로라서 지운 뒤에야 연결한다. 셸 설정 때문에 시작하지 못하는 일을 없앤다.
6. **링크 출력과 7일 세션.** `up`·`open`은 일회용 로그인 링크(10분, 한 번)를 터미널에 출력하고 브라우저를 연다. 파일을 찾아 여는 단계를 없앴다. 토큰이 터미널 기록에 남지만 한 번 쓰면 무효이고 10분 뒤 만료된다. 링크 파일(`start`, `login-link`)은 그대로다. 개인 모드 controller(`serve --personal`)의 세션은 7일(저장 계층 상한)이다. 서버 모드는 12시간 그대로다.
7. **포트와 경로.** `listen`을 적지 않았으면 7331이 쓰일 때 7340까지 다음 빈 포트를 쓰고 controller 기록에 남긴다. 적은 주소는 바꾸지 않는다. `up PATH`는 PATH를 root로 설정 파일에 더하거나(폴더 이름 label) 이미 있는 root 아래 상대 경로로 쓴다. 상태·백업 디렉터리를 포함하는 폴더는 거부한다. 설정 파일은 `up`이 다루는 필드만 고치고 기본값을 쓰지 않는다. 다른 필드와 순서를 보존한다. 깊은 링크(`#project=…&run=…`)는 웹 앱이 canonical UUID인 두 키만 남긴다.
8. **도구와 모델의 자리.** `up`은 에이전트 argv를 만들기 전에 탐지된 도구를 적용하는 함수(`applyDetectedTools`)와 모델을 준비하는 함수(`prepareModel`)를 부른다. 지금 앞의 것은 설정에 없는 Go toolchain만 PATH에서 채우고, 뒤의 것은 `agent.model` 유무를 알린다. 설정이 항상 우선한다.

[운영](OPERATIONS.md#관리형-postgresql) · [사용 안내서](GUIDE.md#첫-시작)

**A40 — 경로·Git 주소 하나, 프로젝트별 분석 선택, 다시 시도.** (기록 2026-11-19) REQ-001/004/015/026, NFR-006과 TC-185~188에 대해 다음을 확정한다. A38 7, A39 4와 소스 확보의 data root 규칙을 개인 모드에서 바꾼다.

1. **입력은 하나.** `codeye up`과 첫 화면은 절대 경로나 공개 Git 주소 하나를 받는다. 화면의 입력란은 `/`로 시작하면 폴더, 그 밖에는 저장소 주소로 읽고(`https://`가 없으면 붙인다) `…/tree/BRANCH`의 브랜치를 쓴다. 등록 뒤 바로 run을 요청하고 그 실행 화면으로 간다. 진행 중인 run이 있으면 그것을 연다. 같은 폴더·저장소(대소문자, 끝의 `/`·`.git` 무시)는 같은 프로젝트다(`POST /projects`가 200). source root를 고르는 양식은 접어 둔다.
2. **홈이 기본 root.** 화면에서 경로를 받으려면 controller가 그 경로를 허가된 root에 대응시켜야 한다. 그래서 개인 모드는 홈 디렉터리를 설정에 쓰지 않는 root `home`으로 둔다. 다른 곳은 지금처럼 터미널의 `up`이 root로 더한다. 화면이 root를 만들게 하지 않은 것은 root 추가가 에이전트 재등록(drain)을 부르기 때문이다. 홈 root는 상태 디렉터리를 품으므로 (a) 설정 규칙을 "root가 `state_dir` 안이면 안 된다"로 줄이고, (b) 소스 확보의 data root 조건을 "승인된 root 전체 밖"에서 "확보하는 프로젝트 폴더 밖이고 그것을 품지 않음"으로, 실행기의 보호 경로를 root에서 프로젝트 폴더로 바꿨다. 대신 (c) 상태·백업 디렉터리를 품거나 그 안에 있는 프로젝트 폴더를 controller가 등록 때(어느 형식이든, symlink를 푼 뒤 `FOLDER_HOLDS_CODEYE_DATA`) 거절하고, 에이전트가 소스 확보 전에(`--protected-dir`, `source_holds_codeye_data`) 다시 거절한다. 등록을 우회한 프로젝트도 데이터 폴더를 읽지 못하게 하기 위해 두 곳에 둔다.
3. **기본 브랜치.** Git 브랜치는 선택이다. 비어 있으면 확보할 때마다 `git ls-remote --symref … HEAD`로 정하고 manifest의 `source_branch`에 남긴다. 등록 때 정해 저장하지 않은 것은 등록이 네트워크에 접근하지 않는 규칙(서버는 선언만 검사)을 지키고, 기본 브랜치가 바뀐 저장소를 따라가기 위해서다. 비교 기준은 프로젝트 소스 선언이 같으면 같다.
4. **공개 저장소만(그 뒤 A46에서 github.com token을 더했다).** credential을 보내지 않는다. 인증을 요구하는 응답(`could not read Username`, `Repository not found`, 401/403 등)은 `source_auth_required`, 연결 실패는 `source_unreachable`로 나눠 화면이 이유를 말하게 한다. GitHub는 없는 저장소와 비공개 저장소를 구별해 주지 않으므로 두 경우가 같은 사유다.
5. **프로젝트별 분석 선택.** `analysis = {send_source_to_model, run_build_scripts}`를 프로젝트 설정 JSON에 둔다(설정 revision이 이미 있으므로 migration이 없다). claim이 run의 고정된 revision에서 읽어 job에 싣고, `--project-analysis-policy`로 뜬 노드(개인 모드)는 로컬·Git 모두 그 선택만 따른다. A39 4의 "모든 로컬 root에 모델 허가"와 root의 `model_review`·`repository_code`를 대신한다. 서버 모드 노드는 바뀌지 않는다(운영자의 root별 허가). 기본값은 소스 종류로 정한다. 로컬 폴더는 둘 다 켬이다. 사용자 자신의 코드이고, 꺼 두면 Rust·C/C++의 기준 분석이 빠진 보고서가 먼저 나온다. Git 저장소는 모델 전송 켬, 빌드 스크립트 끔이다. 남의 저장소 코드를 사용자 권한으로 격리 없이 실행하는 일은 사용자가 그 저장소를 보고 고르게 한다. 모델 전송은 사용자가 키를 둔 공급자 하나로만 가므로 켠다. 빠진 분석은 보고서의 "더 나은 결과를 얻으려면"에서 "켜고 다시 시도"로 바로 바꾼다(`PUT /projects/{id}/analysis`, 새 revision과 `project.revised` 감사).
6. **다시 시도.** `POST /runs/{id}/retry`는 고정 입력을 노드가 갖고 있고 정리가 확인된 failed·cancelled run을 같은 run으로(기존 `Retry`), 그 밖의 끝난 run은 저장된 요청으로 새 run을 만든다(키 `retry:<run>:<version>`이라 같은 요청의 반복은 같은 run). 프로젝트 설정이 run의 revision 뒤에 바뀌었으면 새 선택을 따르도록 새 run이다. `recovery_required`는 다시 시도하지 않는다(정리를 확인할 경로가 먼저 필요하다. 열린 항목).
7. **설치 정보와 안내.** `GET /setup`이 모델(키 없이 ID와 마지막 확인 결과), git과 호스트, 읽을 수 있는 폴더, 찾은 도구(`tools.json`)를 준다. 노드마다 다른 정보라 서버 모드는 null이다. 도구 화면은 진단 파일 업로드 대신 이것을 보인다. "더 나은 결과를 얻으려면"은 보고서의 구조화된 공백(`gaps`의 code·detail·subject)에서만 만든다. 모델 서술을 쓰지 않는다.
8. **품질 쪽 기본값.** 개인 모드의 `agent.max_workers` 기본은 min(4, 논리 CPU/2)이고 `default` 풀의 상한도 같다. 빌드 스크립트(로컬)와 두 Python 타입 검사기는 켜고, 검토 호출 상한은 99다.
9. **확인한 범위.** 실제 공개 GitHub 저장소(BurntSushi/termcolor, 기본 브랜치 `master`)를 `codeye up 주소`와 화면 입력으로 분석해 커밋을 고정하고 `cargo-check`·clippy가 `repository_code_not_allowed`로 빠지는 것, 빌드 스크립트를 켜고 다시 시도하면 실행되는 것(의존성 내려받기가 없어 `dependency_unavailable`로 끝났다), 없는 저장소가 `source_auth_required`로 실패하고 다시 시도되는 것을 확인했다. CI는 시험 빌드의 file:// 미러로 같은 경로를 돈다. 모델 검토가 포함된 Git 보고서는 가짜 upstream으로만 확인했다(실모델 호출 없음).

[사용 안내서](GUIDE.md#프로젝트별-분석-선택) · [API](API.md#프로젝트-등록) · [보안](SECURITY.md#소스와-네트워크)

**A56 — 언어 도구 선택은 서비스 머신 단위로 저장한다.** REQ-004, NFR-006, TC-282. 관리 화면과 운영자 설정 파일은 같은 값을 사용한다. 관리형 워커는 작업을 시작할 때 수동 지정과 서비스가 시작 시 찾은 기본 경로를 읽어 해당 실행의 분석기와 테스트 도구를 선택한다. 이미 시작한 작업은 선택한 도구로 마친다. 독립 노드는 자체 도구를 선택한다.

## 여러 노드와 운영 수용

**A48 — 사람의 정리 확인으로 `recovery_required`를 끝낸다.** (기록 2026-11-20) REQ-021/022, NFR-003과 TC-246·TC-247에 대해 다음을 확정한다. A32 6과 A40 6의 열린 항목을 닫는다.

1. **증거는 사람의 확인이다.** 노드가 정리를 확인하지 못한 attempt(노드 사망·철회·복원 fence, 확인할 수 없는 프로세스 그룹)는 시간이 지나도 깨끗한 것으로 바꾸지 않는다. `POST /runs/{id}/recover`(본문 `{"cleanup_confirmed": true}`만 받는다)와 `codeye run recover --id RUN --confirm-cleanup`이 "그 attempt의 프로세스가 노드에 남지 않았다"는 확인을 받는다. 확인이 없으면 API는 400, CLI는 종료 코드 2다. 한 트랜잭션이 attempt를 `released`·`confirmed`로 두고 예약을 풀고 남은 task를 닫고 run을 `failed`로 바꾼다(이벤트 `attempt.cleanup_confirmed`, 사유 `operator_confirmed`). 같은 트랜잭션에 `run.recovery_confirmed` 감사 행을 쓴다. 누가 언제 확인했는지가 증거다.
2. **누가 확인하나.** 그 프로젝트를 실행할 수 있는 사용자(재시도와 같은 권한)와 운영자 CLI다. 개인 모드 사용자가 직접 정리할 수 없으면 쓸 수 없는 제품이 되므로 관리자 전용으로 두지 않았다. 웹은 확인 상자를 체크해야 단추가 켜진다.
3. **확인 뒤 다시 실행.** 확인 뒤 기존 재시도 규칙(A40 6)으로 다시 넣는다. 고정 snapshot을 heartbeat가 신선한 노드가 갖고 있으면 같은 run, 아니면 같은 요청의 새 run이다. 운영자 CLI는 요청자 대신 넣는다(감사 행위자 `system`). 요청자가 권한을 잃었으면 확인은 남고 run은 보통의 `failed`이며 출력이 `not_queued`와 사유를 적는다.
4. **재시도의 snapshot 보유.** 재시도가 snapshot을 "가진 노드"로 세는 것은 철회되지 않고 heartbeat가 신선한(`NodeStaleAfter`) 노드뿐이다. 죽은 노드에 묶인 같은 run은 영원히 대기하기 때문이다. 노드가 고정 snapshot을 읽지 못하고 `snapshot_unavailable`로 끝내면 그 노드의 위치 기록을 지운다.

대안으로 일정 시간 뒤 자동 해제, 노드 재등록 때 자동 해제를 검토했다. 둘 다 확인되지 않은 정리를 깨끗하다고 간주해 같은 작업이 두 번 실행될 수 있어 채택하지 않았다. migration 0013이 감사 action을 더한다. [실행](EXECUTION.md#장애-처리) · [API](API.md#정리-확인)

**A49 — 보존 정리(retention).** (기록 2026-11-20) REQ-018, NFR-006/007과 TC-248에 대해 다음을 확정한다. A32 4의 "정리 정책은 후속"을 닫는다.

1. **명시적 명령, 기본은 dry run.** `codeye cleanup --older-than 30d`가 지울 것을 보여 주고 `--apply`가 있을 때만 지운다. 자동·예약 정리는 없다. 기준은 1시간보다 짧을 수 없다.
2. **지우는 것.** 끝난 run(`completed`·`partial`·`failed`·`cancelled`)의 오래된 원시 산출물 역할 `stdout`·`stderr`·`raw_output`·`other`의 내용, 노드의 오래된 snapshot 사본(`acquire-*`)·작업 사본(`ws-*`, 열린 journal attempt 것은 제외)·은퇴한 에이전트 디렉터리(`agent.retired-*`)다. 보고서, 관찰(`observations`), manifest, DB 행은 남긴다. 보고서는 저장된 바이트로 그대로 열린다.
3. **지운 사실을 남긴다.** 행을 지우지 않고 `artifacts.content_removed_at`(한 번만 설정, migration 0013)을 적는다. 한 건의 `storage.retention_applied` 감사 행이 개수·바이트·기준 일수를 담는다. 실행 화면과 `GET /runs/{id}`의 `raw_evidence_removed_at`이 "원시 증거가 삭제됨"을 알린다. 파일은 DB가 커밋된 뒤, 남은 행이 참조하지 않을 때만 지운다. 백업과 산출물 상한은 지운 내용을 세지 않는다.
4. **감사 기록은 지우지 않는다.** 감사 표는 append-only(TC-153)이고 정리 자체가 감사 대상이라 보존 정리 대상에서 뺐다.

대안인 상한 도달 때 오래된 것부터 자동 삭제는 사용자가 모르는 사이 근거가 사라지므로 채택하지 않았다. [운영](OPERATIONS.md#보존-정리) · [보안](SECURITY.md#보관과-삭제)

**A50 — 로그아웃 뒤 지속 실행은 systemd 사용자 unit.** (기록 2026-11-20) NFR-003과 TC-249에 대해 정한다. A32 1의 "systemd 등록은 하지 않았다"를 Linux에서 바꾼다. `codeye service install`이 `~/.config/systemd/user/<name>.service`에 `codeye start`·`codeye stop`을 실행하는 oneshot unit(`RemainAfterExit=yes`, 절대 경로, 명시적 PATH, 정지 시간 = drain + 취소 + 60초)을 쓰고 `systemctl --user enable`을 실행한다. 바로 시작하지 않는 것은 이미 `codeye up`으로 띄운 설치를 unit 밖에서 한 번 더 띄우지 않기 위해서다(시작은 `systemctl --user start`). `uninstall`은 설정 없이 동작하고 CODEYE가 쓴 unit만 지운다. `up --no-open` 대신 `start`를 쓰는 것은 로그인 링크가 journal에 남지 않게 하기 위해서다. 로그아웃 뒤 유지는 `loginctl enable-linger`가 필요하며 CODEYE가 바꾸지 않는다. root 권한의 시스템 unit과 Windows Service는 만들지 않는다. 재부팅·로그아웃 뒤 실제 유지는 시험하지 않았다. [운영](OPERATIONS.md#사용자-서비스)

**A51 — 여러 노드 운영의 범위.** (기록 2026-11-20) REQ-018/019/021/022, NFR-002/003과 TC-21·TC-22·TC-250~254에 대해 다음을 확정한다.

1. **도구는 배정 기준이 아니다.** 배정은 `analysis:<profile>`, `source:local:<root>`(등록 token의 root 허가 포함), `source:git:<host>`, 고정 snapshot 위치만 본다. 노드는 기준 분석기가 하나라도 쓸 수 있을 때만 `analysis:deep-review`를 알린다(`--advertise-without-baseline` 예외). 그래서 도구가 하나도 없는 노드는 일을 받지 않지만, Python 도구만 있는 노드는 Go 프로젝트를 받아 Go 작업을 `unsupported`(`tool_not_configured`)로 끝낸다. 언어·도구 단위 배정은 능력 이름과 claim 조건을 넓혀야 하고, 한 run이 여러 언어를 가지므로 "할 수 있는 노드"의 정의가 정해지지 않았다. 운영자는 같은 root를 맡는 노드에 같은 도구를 둔다. 빠진 도구는 보고서와 비교에서 성공으로 보이지 않는다.
2. **drain 중인 노드는 증감 계획에서 빠진다.** drain 중인 에이전트는 heartbeat에서 `worker_scaling`을 static으로, `analysis:*` 능력을 비워 보낸다. 고치기 전에는 controller가 drain 중인 노드에 `start_worker`를 계획했고, 노드가 적용하지 않은 그 시작이 `StartTimeout`(90초) 동안 pool 예산을 쥐었다. 대기 run의 사유는 다른 노드가 없으면 `no_capable_node`다.
3. **활성 controller는 하나다.** 두 controller를 같은 DB에 띄우는 구성은 지원하지 않고 시험하지 않는다. sweep·증감 결정의 잠금(`SKIP LOCKED`)이 겹친 실행을 해치지 않게 하지만, 예약 materialize 외의 경로를 두 인스턴스로 검증하지 않았다.
4. **한 호스트 검증.** 여러 노드 시험은 한 WSL2 호스트의 노드 신원 여럿(각자 상태 디렉터리·도구·root), SIGSTOP/SIGKILL, 로컬 TCP 프록시 차단, PostgreSQL 정지로 한다. 측정 시각은 이 호스트의 값이며 보장값이 아니다. 여러 물리 노드, NFS, 실제 네트워크 분할은 실제 환경에서만 검증한다([로드맵](ROADMAP.md)).

[실행](EXECUTION.md#여러-노드에서-확인한-것) · [운영](OPERATIONS.md#장애-대응-runbook)

## 진행 단계 기록

**A20 — 단계 기록과 검토 대상 없음.** (기록 2026-11-18) REQ-005/013/016/022와 TC-96~99에 대해 다음을 확정한다.

1. 표시 단계는 워커가 계획 시 task마다 선언하고 서버가 enum을 검증해 한 번만 저장한다. key 접두어로 추정하거나 migration에서 채우지 않는다. 단계가 없는 task가 하나라도 있으면 실행 전체를 `not_recorded`로 표시한다. 재시도에서 저장값이 NULL이면 새 워커의 선언을 한 번 기록하고 이벤트를 남긴다.
2. 검토할 대상이 없어 생략한 역할에 새 task 상태를 만들지 않는다. `skipped_policy`와 통제된 사유 코드(`no_documents`, `no_tool_observations`, `no_environment_gap`)를 쓰고 정책 제외 `model_source_not_approved`와 구분한다. 이 상태는 모델 검토 성공으로 세지 않는다. 대안으로 `not_applicable` 상태 추가를 검토했다. DB check·상태 schema·OpenAPI·전이표·보고서를 함께 바꾸는 비용에 비해 구분 정보가 사유 코드와 같아 채택하지 않았다.
3. 탐색 사실만 남은 실행은 의미 있는 결과가 있으므로 `failed`가 아니라 `partial`이다.
4. 지침은 run 프로필 enum으로 고르지 않는다. (A52에서 노드 운영자의 지침 선택도 없앴다. 검토 지침은 하나다.)

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
4. compile DB 생성(configure·cmake·bear)은 저장소 코드를 실행하므로 구현하지 않았다. (A57이 CMake 프로젝트에 한해 바꾼다.) compile DB가 없으면 clang-tidy는 `unsupported`(`compile_db_missing`), cppcheck는 include 경로 없는 축소 모드이며 제약으로 표시한다. 축소 모드에서 include 경로를 추측하는 안은 근거 없는 범위 주장이 되므로 채택하지 않았다.
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
4. 링커: rustc가 build script를 링크하려면 `cc`가 필요한데 toolchain 디렉터리 PATH에는 없다(실측: `linker cc not found`). 새 도구 플래그를 만들지 않고 기존 운영자 설정 `--toolchain-path`를 hello `tool_path`로 builtin에 넘겨(`builtin.Config.ToolPath`, 추가 필드) 시작 시 `cc`를 한 번 찾고 그 절대 경로만 host target의 linker로 준다. 목록 자체를 자식의 PATH로 주지 않는다(A57이 바꾼다). `cc`가 없으면 링크가 필요한 작업만 `linker_unavailable`로 실패한다.
5. `--offline --locked`, `CARGO_NET_OFFLINE=true`, `CARGO_HOME`과 target은 task scratch다. 운영자 레지스트리 캐시 플래그는 만들지 않았다(cargo는 오프라인에서도 CARGO_HOME에 쓰므로 읽기 전용 캐시를 줄 수 없다). 의존성은 경로 의존성이나 저장소가 vendor한 소스뿐이며, 없으면 `dependency_unavailable`이다.
6. build target은 host로, rustc wrapper는 비움으로 환경에서 고정한다(저장소 `.cargo/config.toml`의 `build.target`·`rustc-wrapper`보다 환경이 앞선다는 것을 실측). 나머지 저장소 설정(rustflags, `[lints]`, `clippy.toml`)은 따른다: `repository_config=honored`, 읽은 파일을 `config_files`에 기록하고 작업 사본 위 디렉터리에 Cargo·clippy 설정이 있으면 `null`(실측: cargo와 clippy 모두 조상 디렉터리 설정을 읽는다).
7. 소스 안 억제는 `honored`다. `#[allow]`가 숨긴 진단을 보고하는 옵션이 없고 `--force-warn`은 숨긴 진단을 표시 없이 일반 경고로 내므로 억제와 구분할 수 없다(실측). 따라서 사라진 finding은 해결되지 않는다(`source_suppressions_hidden`).
8. clippy는 `cargo clippy`가 아니라 `cargo-clippy`로 실행한다(저장소 `[alias] clippy`가 외부 하위 명령을 가린다는 실측). lint 수준은 `--cap-lints warn`으로 낮춰 deny lint가 빌드를 실패시키고 의존 crate 검사를 막지 않게 하고, clippy가 함께 출력하는 rustc 진단은 버려 cargo check와 중복하지 않는다(실측으로 같은 집합임을 확인). 별도 cap이 없는 cargo check는 deny lint를 관찰로 남기고 `lints_denied`로 실패한다.
9. unsafe·panic review는 clippy의 allow-by-default lint 여섯 개를 켜서 한다(`undocumented_unsafe_blocks`, `multiple_unsafe_ops_per_block`, `panic_in_result_fn`, `unwrap_in_result`, `todo`, `unimplemented`). `unwrap_used` 등 더 시끄러운 restriction lint는 테스트 코드에서 쏟아지므로 켜지 않았다.
10. 모듈은 가장 바깥 Cargo.toml 디렉터리마다 하나이고 `--workspace --all-targets`, 기본 feature, host target으로 실행한다.

[분석기](ANALYZERS.md)

**A57 — 저장소 코드 실행 허가 아래의 build 문맥.** (기록 2026-11-23) REQ-002/006/007/015와 TC-285~286에 대해 A24 4와 A27 4를 다음처럼 바꾼다. 실제 프로젝트 결함 집합(dev 분할)에서 C/C++ 사례는 compile DB가 없어 clang-tidy·GCC가 실행되지 않고 cppcheck가 축소 모드에서 헤더를 찾지 못했으며, C 코드를 빌드하는 build script가 있는 Rust 사례는 모두 `build_script_failed`였다.

1. **CMake configure로 compile DB를 만든다.** 스냅샷에 compile DB가 없고 CMake 프로젝트가 있으면 분석기 앞에 `compile-db` 작업(단계 `build`, 판정 필수 아님)을 둔다. configure는 저장소 코드를 실행하므로 Rust의 cargo check와 같은 저장소 코드 실행 허가가 있어야 하고, 없으면 실행 없이 `unsupported`(`repository_code_not_allowed`·`repository_code_not_offered`)다. 이 규칙이 A24 4의 "구현하지 않았다"를 대체한다.
2. **보호 장치.** 작업 사본에서만 실행하고(스냅샷은 보호 경로, 원본 경로는 쓰지 않음), build 디렉터리는 작업 사본의 `.codeye/compile-db/<n>`, configure만 하고 target을 build하지 않으며, FetchContent는 끊고 사용자·시스템 package registry는 읽지 않는다. argv·cwd·`PATH`(`--toolchain-path`)·`LANG`·제한시간 5분으로 실행하고 정리 결과는 executor가 확인한다. 실행 전 `CMakeLists.txt`를 manifest 해시와 대조하고, 분석기는 생성 기록의 DB 해시가 다르면 실패한다. 생성한 DB도 스냅샷 DB와 같은 flag 허용 목록을 지나며 항목 compiler를 실행하지 않는다. cmake·make·ninja는 내려받지 않는다. 네트워크는 막지 않는다. FetchContent 밖의 configure 중 다운로드(`file(DOWNLOAD)`, `execute_process`의 git·curl, ExternalProject)는 노드 계정으로 실행될 수 있으며, 이것을 막는 OS 수준 차단은 Native 실행의 범위 밖이라 허가로 다룬다. configure가 소스 트리에 쓴 파일은 작업 뒤 복원이 지운다.
3. **한계를 결과로 남긴다.** 허가 없음·도구 없음·configure 실패는 분석기의 제약(`compile_db_generation_not_allowed`·`compile_db_generation_unavailable`·`compile_db_generation_failed`)으로 보이고 분석기는 축소 모드로 남는다. 생성한 DB로 분석한 결과는 `compile_db_source=generated`, `compile_db_generated`를 기록한다. 축소 모드에서 include 경로를 추측하지 않는다는 A24 4의 나머지는 그대로다. Meson·autotools·`bear`는 아직 생성하지 않는다.
4. **허가받은 build 코드의 PATH.** build script(`cc` crate)와 configure는 C 도구를 이름으로 찾으므로 절대 경로 하나로는 충분하지 않다. 저장소 코드 실행 허가를 받은 작업(cargo check·clippy·rustdoc, `compile-db`)에 한해 자식 PATH를 toolchain 디렉터리 뒤에 `--toolchain-path` 디렉터리로 넓힌다. 이는 운영자가 이미 시스템 도구 경로로 정한 값이며 새 설정이 아니다. 의존성 받기(`cargo fetch`)와 허가 없는 분석기는 그대로 toolchain 디렉터리 하나다. A27 4의 "목록 자체를 자식의 PATH로 주지 않는다"를 이 범위에서 대체한다.

[분석기](ANALYZERS.md#compile-db-생성)

**A28 — 구조·중복·비밀값 팩.** (기록 2026-11-18) REQ-007/008/011/012/013/015/031, NFR-006과 TC-117~119에 대해 다음을 확정한다.

1. lizard(`any.lizard`)·jscpd(`any.jscpd`)·gitleaks(`any.gitleaks`)는 언어와 무관한 추가 깊이 분석기이고 세 작업 모두 `RepositoryCode=false`다. 저장소의 프로그램·plugin을 실행하지 않는다.
2. 구조·중복 결과는 envelope 관찰의 가산 필드 `metric{name, value, threshold, unit}`으로 표현한다. 지표 관찰은 활성 관찰이 아니다(`Active()`). finding·결함 수·조치 수·모델 입력이 되지 않는다. 보고서는 `summary.metric_observations`(0이면 생략)로 따로 센다. 새 진단 종류나 별도 산출물을 만들지 않은 이유가 있다. 원시 관찰의 결속(stream·byte 범위)과 위치 검증을 그대로 쓰고, `raw_observations`·`unresolved_observations`의 뜻을 바꾸지 않기 때문이다.
3. 위치를 확인하지 못한 지표는 미해결 관찰로 세어 판정을 `inconclusive`로 둔다. 측정값이어도 근거가 스냅샷과 맞는지 확인하지 못한 결과는 깨끗한 판정의 근거가 아니다.
4. 임계값은 규칙 팩 v1로 고정한다: CCN ≥ 16, 중복 minLines 10·minTokens 100·mode mild, 1,000,000바이트·UTF-8. 데이터 디렉터리(숨김·`testdata`·`vendor`·`node_modules`)는 지표에서 빼고 센다. 복잡도는 lizard, 중복은 jscpd만 잰다(`-Eduplicate`·jscpd complexity 미사용).
5. 비밀값 엔진은 gitleaks `dir`이다. CODEYE 템플릿이 규칙·파일·줄·열·entropy만 출력하고 `--redact=100`, info 로그를 쓴다. 관찰 문구는 고정이고 식별은 `ToolFingerprint(analyzer, rule, path, "", 고정 문구)`다. 비밀값의 hash는 어디에도 두지 않는다. 열은 gitleaks 8.30.1이 첫 줄 뒤에서 1씩 크게 보고하므로(실측) 기록하지 않는다. ruff S105·bandit B105와 합치지 않는다.
6. 저장소 설정: lizard·jscpd는 대체한다(`overridden`). gitleaks는 `.gitleaks.toml`을 대체하고, 끌 수 없는 `.gitleaksignore`는 검증 뒤 digest와 함께 따른다(`honored`). 사본 밖 항목은 값을 기록하지 않고 거부한다(`repository_config_rejected`). 인라인 allow는 끈다(`disabled`). 읽은 파일이 없는 `recorded` 문맥은 빈 목록의 digest를 쓰고 경로를 두지 않는다. 저장 뒤 다시 읽어도 같도록 검증을 맞췄다.
7. 모델 입력 선별은 세 경우다. 실행 안 됨이면 보내고 제약을 남긴다. 실행했으나 쓸 수 없으면 검토를 `secret_screen_failed`로 건너뛴다. 성공이면 탐지 파일을 뺀다.
8. 보고서 builder 버전은 "4"로 둔다. 지표가 없는 보고서의 바이트가 그대로이고, 새 필드는 생략 가능한 가산 필드이며, 이전 revision의 비교 규칙이 바뀌지 않기 때문이다. 대가로 builder 버전만으로는 지표 표시 이전·이후를 구분할 수 없다.

[분석기](ANALYZERS.md)


**A43 — Rust와 JS/TS 의존성 받기.** (기록 2026-11-19) REQ-012/014/015, NFR-006과 TC-200~205에 대해 다음을 확정한다. A42의 틀을 그대로 쓴다.

1. **Rust는 공유 `CARGO_HOME`.** `cargo fetch --locked --target <host>`로 레지스트리 crate를 공유 캐시(`rust/cargo`)에 받고 cargo check·clippy는 그 `CARGO_HOME`을 `--offline`으로 읽는다. `Cargo.lock`이 없으면 `generate-lockfile`로 만든 것을 task 디렉터리에 두고 분석기가 실행하는 동안만 사본에 놓는다. 실행 사본에 남기면 작업 사이 되돌리기가 지우고, 뒤 작업이 manifest에 없는 파일을 보게 되기 때문이다.
2. **Rust 받기는 빌드 스크립트 허가를 따른다.** cargo는 받는 동안 저장소 `.cargo/config.toml`을 읽고, 받은 crate를 쓰는 분석기는 build script를 실행한다. 허가 없이 받으면 쓰지 않을 crate를 받는 셈이다. 그래서 허가가 없으면 아무것도 시작하지 않는다(`dependency_consumers_not_allowed`, 받을 것 없음과 같이 성공 취급).
3. **JS/TS는 attempt 사본에 설치한다.** tsc와 typescript-eslint는 `node_modules` 배치로 모듈을 푼다. 공유 캐시를 직접 읽게 하는 방법이 없으므로 다운로드 캐시만 공유하고(`javascript/{npm,pnpm,yarn}`) 트리는 실행 사본에 만들어 attempt와 함께 지운다. 기록의 `installed_dirs`가 그 트리를 적고, 분석기는 적힌 실제 디렉터리만 받아들인다. 출처를 manifest에 묶을 수 없는 다른 `node_modules`는 전처럼 거부한다.
4. **JS/TS 받기는 빌드 스크립트 허가와 무관하다.** lifecycle script와 pnpmfile을 끄고 실행하므로 저장소 코드를 실행하지 않는다. 허가에 묶으면 Git 저장소는 기본으로 받지 않게 되어 받기의 목적이 없어진다.
5. **관리자는 lockfile로 고른다.** lockfile이 정한 버전을 그대로 설치해야 결과가 저장소와 같다. 여러 lockfile은 `packageManager`가 정한다. yarn 2 이상과 bun은 받지 않는다(`lockfile_format_unsupported`). 시험하지 않은 형식을 다른 관리자로 설치하면 lockfile과 다른 트리가 되기 때문이다. lockfile이 없으면 npm으로 버전을 고르고 그렇다고 기록한다.
6. **관리자 릴리스와 위치는 운영자 것.** pnpm은 `packageManager`가 적은 릴리스를 모든 명령에서 내려받아 실행한다(실측). 그것을 끄고, 설치 위치·캐시·로그·레지스트리·proxy를 명령줄로 정한다. 명령줄이 `pnpm-workspace.yaml`·`.npmrc`보다 이기는 것을 실측했다. 저장소 `.npmrc`·`.yarnrc`는 비밀 파일 규칙으로 스냅샷에 없다.
7. **밖을 가리키는 링크는 지운다.** `file:`·`link:` 의존성은 작업 사본 밖을 가리키는 링크가 될 수 있고, 타입 검사기가 그것을 따라 호스트 파일을 읽는다. 설치 전체를 버리지 않고 그 링크만 지우고 기록한다. 나머지 의존성으로 분석하는 쪽이 사용자에게 낫기 때문이다. 그보다 먼저, `package.json`과 lockfile이 스냅샷 밖의 `file:`·`link:`·`portal:` 경로를 적으면 설치를 실행하지 않는다(`local_dependency_outside_project`). 패키지 관리자가 그 경로에 설치해 원본 밖 경로에서 install을 실행하게 되기 때문이다. 드문 경우이므로 그 의존성만 빼는 처리는 하지 않는다.
8. **패키지 관리자는 분석기가 아니다.** npm·pnpm·yarn은 uv·python과 같은 한 갈래다. 도구 찾기가 받기 task가 실행하는 방식으로 검증하고(`<node> <script> --version`), 플래그(`--npm`·`--pnpm`·`--yarn`)와 `agent.tools.*`는 다른 도구와 같으며, 워커에는 hello의 `dependencies.package_managers`로 간다(`deps.ManagerFlags`). 분석기 도구 표(`analyzer_tools`)에 넣으면 분석기 구성이 모르는 이름을 따로 걸러야 하고, 다음 생태계가 그 예외를 또 더해야 한다. 설치 안내와 "도구" 화면은 한 갈래 그대로다.
9. **버전 고정 공백은 받았을 때만.** 받기 전에 건너뛴 기록은 `pinned=false`이지만 이번에 버전을 고른 것이 아니다. 공백 `dependency_versions_not_pinned`는 `prepared`·`partial` 기록에만 단다. 저장된 보고서 바이트가 아니라 새로 만드는 보고서의 공백만 바뀌므로 builder 버전은 그대로다. 이런 기록을 담은 보고서는 Go 기록 말고는 아직 `main`에서 나온 적이 없고, Go 기록은 건너뛴 task가 실제로 그 공백을 단 적이 없다.

[아키텍처](ARCHITECTURE.md#의존성-준비) · [Rust 받기](ANALYZERS.md#rust-의존성-받기) · [JS/TS 받기](ANALYZERS.md#jsts-의존성-받기) · [한계](ANALYZERS.md#의존성-받기의-한계)

**A44 — Python 의존성 받기.** (기록 2026-11-19) REQ-007/014/015, NFR-006과 TC-210~218에 대해 다음을 확정한다. A42의 틀을 따른다.

1. **wheel만 받는다.** sdist를 빌드하면 저장소가 적은 패키지의 `setup.py`·build backend를 실행하게 된다. pip `--only-binary=:all:`, uv `--no-build`로 받고 wheel이 없는 요구사항은 빼고 다시 설치해 `partial`로 남긴다(최대 3번 더). 모듈 전체를 실패시키면 wheel 하나 때문에 나머지 패키지가 모두 빠진다(httpx의 `mkautodoc`, 실측).
2. **선언을 다시 쓴다.** 저장소의 requirements 파일을 설치 도구에 그대로 넘기면 저장소가 `--index-url`·`--find-links`·editable·로컬 경로로 설치 대상을 정한다. 요구사항 줄을 허용 목록으로 읽어 CODEYE의 파일을 쓰고, 나머지는 `dropped_inputs`에 사유와 함께 남긴다. index는 운영자 값뿐이다. `pyproject.toml`은 운영자 interpreter의 `tomllib`로 `-I -S`·stdin으로 읽는다. Go 쪽에 TOML parser 의존성을 더하지 않기 위해서다. `uv.lock`은 uv의 `export --frozen --offline`으로만 읽는다.
3. **mypy는 가짜 venv로 읽는다.** `MYPYPATH`에 site-packages를 주는 안은 실측에서 실패했다. mypy가 `mypy_extensions`를 stdlib 그림자로 거부하고(exit 2), 그 파일을 지우면 제3자 stub 안의 오류를 보고하며 `import-untyped`를 숨겼다. `--python-executable`에 운영자 interpreter와 받은 site-packages를 묶은 scratch venv를 주면 PEP 561 규칙대로 읽는다(httpx `import-not-found` 63 → 2). 대가로 mypy가 운영자 interpreter를 한 번 실행한다. pyright는 interpreter 없이 `venvPath`/`venv`로 같은 트리를 읽는다.
4. **시작 hook을 지운다.** 그 interpreter의 `site`가 받은 트리의 `*.pth` import 줄과 `sitecustomize`·`usercustomize`를 실행한다. 받은 직후 site-packages 맨 위의 그 파일을 지우고 `interpreter_startup_hooks_removed`를 남긴다. 받은 패키지의 다른 코드는 import하지 않는다. 운영 체제의 `sitecustomize`(예: Debian)는 운영자 interpreter의 것이라 그대로다.
5. **고정된 것만 공유한다.** 버전이 모두 고정됐을 때(`uv.lock` 또는 모든 줄 `==`)만 요구사항·interpreter·설치 도구·index로 정한 key의 공유 트리를 쓴다. 고정이 아니면 같은 선언이 run마다 다른 패키지를 받으므로 attempt 트리로 두고 attempt와 함께 지운다. 그래서 Python 분석기는 A42와 달리 attempt 캐시 기록도 쓴다.
6. **interpreter는 운영자 것 하나.** 트리는 `--python`의 버전·플랫폼으로 풀린다. 분석 대상 버전과 다를 수 있지만, 대상 버전 interpreter를 내려받는 것은 조용한 외부 다운로드이므로 하지 않는다(uv `UV_PYTHON_DOWNLOADS=never`). uv를 써도 `--python`이 필요하다.
7. **보고서 builder는 그대로.** 받기 꺼짐에서 Python 타입 검사기는 성공하므로 `dependency_unavailable`로는 공백이 생기지 않는다. coverage의 `import_unresolved`와 `third_party_packages_not_installed`를 `dependencies_not_fetched`의 조건으로 더했다. builder 7이 아직 출시되지 않았으므로 버전을 올리지 않는다.

[분석기](ANALYZERS.md#python-의존성-받기) · [아키텍처](ARCHITECTURE.md#의존성-준비)

**A45 — 검토 우선순위와 요약 보기.** (기록 2026-11-19) REQ-009/012/027, NFR-007/008과 TC-220~226에 대해 다음을 확정한다. 첫 자체 분석 실행에서 모델이 주요 코드 대신 시험 입력(`fixtures/`)을 검토했고(slice 32개 중 다수가 fixtures 모듈) 보고서 Markdown이 19,096줄이었다.

1. **slot은 계획 때, 파일은 실행 때.** 계획은 A37 2대로 manifest·scope·상수로만 정하되 slice 목록 대신 slot 수 `min(96, c0 + max(2, ⌈c0/8⌉))`만 정한다(c0는 계획 때 순서로 채운 조각 수). 파일은 정적 분석 뒤에 실행되는 `review.scope`가 도구 진단을 보고 [검토 우선순위](REVIEW.md#검토-우선순위)대로 채운다. 진단을 아는 쪽은 실행 때뿐이고, task key가 진단에 따라 달라지면 재시도에서 고아 task가 생기기 때문이다. 채우지 않은 slot은 호출 없이 `no_review_scope`다. 우선순위로 바뀐 묶음이 slot 수를 넘으면 디렉터리 경계, 그다음 순위 경계를 차례로 풀어 맞춘다(여유 없이는 무작위 트리에서 실행 때 묶음이 계획보다 많은 경우가 있었다). 상한 96에 닿으면 경계를 풀지 않고 뒤를 버린다. 큰 저장소에서 응집을 잃는 것보다 뒤 slot을 버리는 편이 낫다.
2. **96과 207.** slot 상한을 32에서 96으로, 호출 상한 최댓값을 207(15 + 96 × 2)로 맞췄다. 기본 상한 99에서 승인되는 slot은 많아야 42개다. 그래도 큰 저장소에서는 기본 설정의 비용이 늘 수 있다. 이전에는 slot 32개가 먼저 다해 상한 99를 다 쓰지 못했기 때문이다(이 저장소의 파일럿, 가짜 upstream: 호출 69회 → 95회, 요청 바이트 1.73 MB → 2.64 MB). 늘어난 몫도 상한 안이고, 줄이려면 운영자가 상한을 낮춘다.
3. **주요 먼저, 부차는 끝.** 부차 경로 조각과 생성 파일 표지를 한 곳(`record.DefaultSecondary`)에 두고 worker 설정으로 바꿀 수 있게 했다. 부차 파일을 아예 빼는 안은 시험 입력 자체의 결함(예: 잘못된 fixture)을 볼 길을 막아 채택하지 않았다. 주요 파일이 남는 한 부차 파일은 slot을 받지 않는다. overview 조사도 같은 분류로 순회한다. 부차 파일이 없는 저장소는 순서가 이전과 같아 회귀 집합의 요청이 바이트 단위로 같다.
4. **범위 산출물 0.2와 builder 8.** 범위 산출물은 파일마다 분류·순위·이유를 싣는다(0.1도 계속 읽는다). 장부는 모듈 `class`, `files_secondary`, `files_secondary_sent`, `files_secondary_not_reached`, `primary_slots_budget_exhausted`를 더한다. 같은 입력에서 장부·종합 수·공백·먼저 볼 항목이 달라지므로 A37 7과 같은 이유로 builder를 8로 올렸다.
5. **요약 보기는 렌더링이다.** Markdown·HTML의 기본을 요약 보기(`detail=summary`)로 하고 `detail=full`은 이전 배치와 바이트가 같다. 요약은 저장 본문·digest·JSON을 바꾸지 않으므로 builder를 올리지 않았고 이전 리비전도 같은 요약으로 보인다. 보고서 본문에 요약을 저장하는 안은 같은 사실을 두 번 저장하고 표시 규칙을 바꿀 때마다 builder를 올려야 해서 채택하지 않았다. 요약의 경로 판단은 경로 규칙만 쓴다(생성 파일 표지는 보고서가 읽지 않는다). 웹 화면은 같은 묶음을 쓰고 전체 행은 펼칠 때만 그린다.
6. **호출 안내.** "더 나은 결과를 얻으려면"은 장부로 필요한 상한을 센다(`calls_used + 2 × (거절된 주요 slot − 1) + 6`, 형식 복구가 없을 때). slot 상한 밖 파일은 상한을 올려도 닿지 않으므로 따로 적는다.

[모델 검토](REVIEW.md#검토-우선순위) · [보고서](REPORT.md#내보내기-상세도)

**검토 범위의 줄 단위 기록.** 범위 산출물 0.3은 A45의 분류를 유지하면서 각 모듈에 첫 원문 발췌를 배정한 뒤 남은 파일과 줄을 채운다. 문서 계약용 발췌를 먼저 보낸 파일도 나머지 줄을 검토할 수 있다. slice를 시작할 때 조사와 후보 확인에 한 호출씩을 남기고 형식 복구도 같은 attempt 상한을 사용한다. 보고서 builder 16은 기존 범위 산출물을 계속 읽으며 새 산출물에서 요청 범위·조사 응답이 수락된 범위·후보 확인 근거를 따로 센다. 모델 요청과 검토 결과의 범위를 재현 가능한 기록으로 보여 주기 위한 결정이다. [모델 검토](REVIEW.md#모듈-단위-검토-분할) · [보고서](REPORT.md#모듈별-검토-장부)

**확장 코드 검토.** 실행 범위에 선택을 고정하고, 노드 운영자가 호출 상한을 높이면 일반 모드보다 넓은 원문 범위를 처리한다. 노드 설정만으로 작업 키를 늘리면 낮은 상한의 노드에서 재시도할 때 이전 작업 행이 남을 수 있다. 선택한 범위와 스냅샷에서 작업 키를 정하고 노드 상한은 실행 승인에 적용해, 같은 입력의 재시도와 기본 호출 비용을 보존한다.

**A46 — 개인 모드의 GitHub token, pull request 분석, 결과 댓글.** (기록 2026-11-20) REQ-024/025, NFR-006과 TC-227~236에 대해 다음을 확정한다. 소유자는 GitHub App을 등록하지 않고 자기 컴퓨터에서 비공개 저장소와 pull request를 분석하고 결과를 pull request에 남기려 한다.

1. **설정 없는 token.** 개인 모드의 `codeye up`이 `GH_TOKEN`, `GITHUB_TOKEN`, `gh auth token --hostname github.com` 순으로 token을 찾고 `GET /user`로 확인해 `<state>/secrets/github.json`(0600)에 둔다. 모델 키(A39 3)와 같은 자리와 규칙이다. 설정 파일·argv·로그·보고서·감사 detail·DB에는 넣지 않고, `up` 출력과 `GET /setup`은 있음·계정·출처·이유만 보인다. 이미 로그인한 gh를 그대로 쓰면 사용자가 할 일이 없고, App 등록은 한 사람의 컴퓨터에 과하다. `"github": {"enabled": false}`가 발견을 끈다. 서버 모드는 token을 찾지 않는다(아래 6).
2. **token은 github.com과 두 경로에만.** 워커는 파일 경로만 받고 fetch마다 읽어 github.com 요청의 HTTP header로만 보낸다. header는 git 환경변수(`GIT_CONFIG_COUNT`의 `http.https://github.com/.extraHeader`)로 가서 URL·argv·작업 사본에 남지 않는다. credential helper를 여는 안은 사용자 설정의 helper를 부르게 되고 A16의 "사용자 Git 설정 차단"과 맞지 않아 택하지 않았다. API client는 redirect를 따르지 않고 다른 origin의 page 주소를 쓰지 않는다. 분석기와 모델은 token을 받지 않는다.
3. **pull request는 run에 한 번 고정한다.** controller가 run을 넣을 때 GitHub API로 base/head SHA, head 저장소, 변경 파일·상태·변경 줄을 읽어 `runs.pull_request`(migration 12, jsonb, 바꾸지 않음)에 쓴다. 노드는 `refs/pull/N/head`를 받아 고정 SHA를 확인한다. 노드가 GitHub API를 부르게 하면 token이 API용으로도 노드에 가고 run의 사실이 attempt마다 달라질 수 있다. idempotency digest는 보낸 body만 다룬다(같은 키의 반복이 GitHub 상태에 따라 충돌로 바뀌지 않게).
4. **변경 파일이 먼저, 전체를 같은 깊이로 봤다고 하지 않는다.** 검토 순서에서 tier(변경 → 같은 폴더 → 나머지)를 A45의 분류·순위보다 앞에 둔다. 그래서 pull request가 fixtures의 파일만 바꿨어도 그 파일이 먼저다. pull request가 아닌 run은 tier가 없고 계획이 이전과 같다(slice 계획 시험이 확인한다. 회귀 집합의 요청 dump 비교는 이 변경에서 실행하지 않았다). 보고서 builder 9는 `pull_request` 절에서 발견·진단을 변경된 줄·변경 파일·기존 코드로 나누고, patch가 없는 파일은 파일 단위로 센다고 적는다. 요약은 "저장소의 나머지는 같은 깊이로 검토하지 않았을 수 있다"고 쓴다.
5. **결과 댓글은 선택, 하나, 되돌아가지 않음.** 기본 끔이고 프로젝트마다 기억한다(`analysis.comment_on_pull_request`). 보고서 게시 transaction이 outbox 행을 만들고 controller가 lease로 게시한다. 댓글은 token 계정이 쓴 marker 댓글 하나를 고친다(다른 사람이 붙인 marker는 무시). publication version은 run 생성 시각(마이크로초)이고, 같은 pull request의 더 늦은 run이 게시했거나 GitHub의 댓글 version이 더 크면 `superseded`다. 실패는 run의 댓글 상태로만 남고 분석 상태를 바꾸지 않는다. 본문은 구조화된 보고서에서만 만들고 코드·도구 원문·증거·비밀정보를 싣지 않으며, `&`를 먼저 escape하고 mention·issue 참조·자동 링크를 끊는다. dry run(`--dry-run-comment`, 화면의 미리 보기)은 같은 렌더러로 GitHub에 쓰지 않는다.
6. **서버 모드는 그대로.** 여러 사람이 쓰는 설치의 계획은 GitHub App과 webhook이다. 한 사람의 token을 서버가 모든 사용자에게 쓰게 하면 권한 경계가 그 사람의 권한이 된다. 서버 모드의 pull request 요청은 `PULL_REQUEST_UNSUPPORTED`다. pull request를 주기적으로 확인하는 polling은 만들지 않았다. 새 push는 사용자가 다시 요청하고, 그 run의 비교 기준은 같은 pull request의 이전 run이다.
7. **fork의 pull request.** 개인 모드에서는 사용자가 주소를 직접 주므로 외부 fork도 같은 정책으로 분석한다. 저장소 코드를 실행하는 분석은 Git 프로젝트에서 기본 끔(A40 5)이라 자동으로 실행되지 않는다. App의 자동 이벤트에 대한 "fork는 Native 실행을 자동 승인하지 않음"(로드맵)은 그대로다.

[GitHub 연결](GITHUB.md) · [보고서](REPORT.md#pull-request-보고서) · [모델 검토](REVIEW.md#검토-우선순위) · [보안](SECURITY.md)


**A42 — 의존성 받기.** (기록 2026-11-19) REQ-004/014/015, NFR-006과 TC-194~199에 대해 다음을 확정한다.

1. **받아서 분석한다.** 분석기가 import를 풀지 못하면 타입 검사가 실패하고(`dependency_unavailable`) 그 모듈의 정적 분석은 비어 있다. 운영자가 미리 캐시를 채우게 하는 대신 분석 전에 모듈마다 의존성을 받는다. 분석기는 그 뒤에도 오프라인(`GOPROXY=off`)으로 실행하고 받은 캐시만 읽는다. 받는 단계와 읽는 단계를 나눠, 받기가 실패해도 분석기의 실행 경로와 실패 사유는 받기가 없을 때와 같다.
2. **선택은 둘.** 프로젝트 선택 `fetch_dependencies`(기본 켬, 로컬·Git 모두)와 노드 운영자 스위치(`--dependency-fetch`, 개인 모드는 켬)가 둘 다 켜져야 한다. 분석 품질을 기본으로 두고, 네트워크 접속을 노드 운영자가 끌 수 있게 했다. 빌드 스크립트와 달리 Git 저장소도 기본 켬이다. 받기는 저장소 코드를 실행하지 않고 go.sum으로 검증한다. 남는 영향은 모듈 이름이 proxy에 알려지는 것이다([보안](SECURITY.md#소스와-네트워크)).
3. **저장은 꺼짐일 때만.** 저장된 선택을 읽는 쪽은 필드를 엄격히 읽는다. 새 필드를 꺼짐일 때만 쓰고 없으면 켜짐으로 읽어, 이전 바이너리도 켜짐인 기록을 읽는다. 꺼짐 기록은 이전 바이너리에서 "모두 끔"이 된다. 이 경우가 생기면 바이너리를 맞춘다.
4. **계획 위치.** `deps.Wrap`을 탐색과 검토 사이에 둔다. 검토 역할은 정적 handler의 계획만 보므로 검토 요청 바이트가 바뀌지 않는다. 꺼져 있으면 task를 하나도 계획하지 않아 계획과 보고서가 받기 이전과 같다. task key 접미사는 분석기의 모듈 key를 따른다. 그래서 분석기는 자기 key로 기록을 찾고 받는 디렉터리와 분석 디렉터리가 같다.
5. **필수가 아니다.** 받기 task는 `required=false`다. 레지스트리가 느리거나 실패하면 그 task만 끝나고 분석은 계속한다. 판정 규칙은 그대로다. 받지 못해 분석기가 실패하면 그 분석기 task가 판정에 반영된다. 받기 실패를 따로 판정에 넣으면 같은 원인을 두 번 세게 된다.
6. **거부는 될 수 없는 것만.** lockfile을 미리 훑어 레지스트리 밖 출처를 거부하지 않는다. 받는 도구가 받을 수 있는 만큼 받고 실패한 항목과 사유를 남긴다(`partial`). 거부는 동작할 수 없는 경우(받는 도구 없음, snapshot 밖 `replace`)뿐이다.
7. **Go는 사본 go.mod로 받는다.** `-modfile`로 task 디렉터리의 go.mod·go.sum 사본을 쓰는 것을 측정으로 확인했다(사본은 `.mod` 확장자가 있어야 한다). 실행 사본과 snapshot의 go.mod·go.sum이 그대로이므로 go vet의 drift 검사가 유지된다. 받는 동안 사본 go.sum이 바뀌면 go.sum에 없던 항목이 있다는 뜻이고, 분석기는 `-mod=readonly`라 그 모듈을 쓰지 못한다. 그래서 `go_sum_incomplete`로 실패로 둔다.
8. **운영자 Go 설정을 따른다.** 개인 모드는 에이전트 시작 때 사용자 `go env`의 `GOPROXY`·`GOSUMDB`·`GOPRIVATE`·`GONOPROXY`·`GONOSUMDB` 중 기본값과 다른 것을 읽어 넘긴다. 서버 모드는 플래그로 준다. 비공개 모듈은 공개 proxy에 묻지 않는다. 비공개 모듈은 `file://` 출처를 포함한 모든 proxy를 건너뛰므로, 운영자 캐시에 이미 있는 것을 받기 캐시로 복사해 쓰고 없으면 `private_module_not_fetched`로 남긴다. VCS 직접 접근(`direct`)은 열지 않는다. 저장소가 정한 주소로 git을 실행하게 되기 때문이다.
9. **크기는 soft 제한.** 생태계 캐시 디렉터리 전체(압축 파일과 푼 트리)를 2초마다 잰다. 정확한 상한은 OS 수준 quota가 필요하고 Native 실행에서는 둘 수 없다. 같은 캐시를 쓰는 다른 워커의 내려받기도 함께 센다.
10. **공유 캐시는 지우지 않는다.** 받은 캐시는 run이 끝나도 남겨 다음 run이 다시 받지 않게 하고, cache 저장 영역에 센다. 자동 정리는 하지 않는다(저장 영역의 다른 데이터와 같은 이유).
11. **보고서 builder 7.** 환경 절에 `dependencies`를 더하고 공백 세 가지(`dependencies_not_prepared`, `dependencies_not_fetched`, `dependency_versions_not_pinned`)를 더한다. 필드는 의존성 task가 있을 때만 있다. builder 버전은 올린다. 같은 사실에서 공백이 달라질 수 있어(`dependencies_not_fetched`) 이전 builder와 구분해야 하기 때문이다.

[아키텍처](ARCHITECTURE.md#의존성-준비) · [분석기](ANALYZERS.md#go-의존성-받기)

**A41 — 실제 저장소에서 한 단위가 작업 전체를 실패시키지 않게.** (기록 2026-11-19) REQ-007/008/009/013/014/015/031, NFR-006/008과 TC-189~193에 대해 다음을 확정한다. A28 7과 A37 5의 `secret_screen_failed` 건너뛰기를 바꾼다.

1. **작업 사이 되돌리기.** 한 attempt의 작업은 작업 사본 하나를 차례로 쓰므로, Rust build script가 고쳐 쓴 소스가 뒤의 lizard·jscpd·gitleaks를 `workspace_drift`로 실패시켰다(`fixtures/rust/drift`, 실측). 워커가 작업마다 사본을 스냅샷 내용으로 되돌린다. 작업마다 새 사본을 복사하는 안은 큰 저장소에서 작업 수만큼 복사가 늘고, 분석기마다 drift를 무시하는 안은 manifest 파일이 바뀐 사본을 분석하게 되므로 채택하지 않았다. 바뀐 파일은 stamp(크기·수정 시각·권한, Linux inode·ctime)로 찾고 달라진 것만 hash로 확인해 다시 쓴다. 숨김 이름·`node_modules`·`vendor`·`testdata`는 분석기가 소스로 보지 않고 준비한 의존성이 놓이므로 지우지 않는다. 자기 실행 중에 사본을 바꾼 작업은 그대로 `workspace_drift`다.
2. **쓸 수 없는 선별은 미선별.** 비밀값 작업이 실패하거나 부분 결과이면 검토 전체가 건너뛰어져 실제 저장소에서 검토 70개가 실행되지 않았다(실측). 소유자 결정으로 이를 미선별과 같이 다룬다. 기본 읽기 필터만 거친 입력을 보내고 기록·보고서가 `model_input_not_secret_screened`와 쓸 수 없던 작업·사유를 밝힌다. 다만 읽을 수 있는 결과가 보고한 파일(지금까지 읽은 결과와 해석되는 실패·부분 envelope의 관찰 경로)은 계속 뺀다. 아무것도 읽을 수 없을 때만 빼는 파일이 없다. `secret_screen_failed`는 이전 worker의 기록을 읽기 위해서만 남는다. 보고서 builder 버전은 그대로다. 저장된 보고서 바이트는 바뀌지 않고 표시 문구만 상태에 따라 고른다.
3. **해석하지 못한 파일만 뺀다.** 파일마다 따로 해석하는 도구(ruff·mypy·pyright·bandit, ESLint)는 구문 분석하지 못한 파일을 처리 파일에서 빼고 `unparsable_files_not_analyzed`를 남긴 채 성공한다. 위치가 있는 구문 오류는 파일의 관찰 하나다. mypy는 첫 구문 오류에서 멈추므로 그 파일을 빼고, 남은 파일이 import해도 멈추지 않게 그 모듈을 `follow_imports = skip`으로 두고 다시 실행하며(최대 32회) 재실행은 작업 제한시간의 남은 시간만 쓴다. 파일 이름은 대상 속성 `unparsable_files`에 남는다. bandit은 위치를 주지 않아 관찰로 이름을 남길 수 없기 때문이다. 다른 parser로 미리 거르는 안은 그 문법 판정이 mypy 대상 버전과 다를 수 있어 채택하지 않았다. 프로그램·번역 단위·패키지를 함께 검사하는 도구(tsc, cppcheck·clang-tidy, go vet·Staticcheck, cargo)는 바꾸지 않았다. tsc는 구문 오류가 하나라도 있으면 타입 검사를 하지 않고 파일을 빼도 import로 다시 들어오며, C/C++ 단위 실패는 대개 빌드 설정 문제이고 cppcheck의 같은 분류에 도구 내부 오류가 섞여 있으며, Go·Rust는 컴파일하지 못한 패키지에 기대는 패키지의 결과도 불완전해 단위만 빼는 판정을 따로 정해야 하기 때문이다.

[분석기](ANALYZERS.md) · [모델 검토](REVIEW.md#모델-입력의-비밀값-선별)

**A60 — SARIF 입력 어댑터와 CODEYE 규칙의 Opengrep 파일 안 taint 팩.** (기록 2026-11-25) REQ-007/008/012/013, NFR-006과 TC-289~291·304~306에 대해 다음을 확정한다. [분석기](ANALYZERS.md#설계-계약미구현)의 설계 계약 두 항목(SARIF 입력 어댑터, 승인된 로컬 패턴 규칙)을 구현으로 옮긴다.

1. **SARIF는 팩 안의 입력 형식이다.** 공통 관찰 모델로 옮기는 어댑터(`internal/worker/analysis/sarif`)는 팩이 실행한 도구의 출력만 읽는다. 다른 곳에서 만든 SARIF를 올려 결과로 받는 경로는 만들지 않았다. 외부 결과를 받으면 실행 기록·규칙 문맥·원시 stream 근거가 없는 관찰이 생기고, 기존 CI 결과 수집을 하지 않는다는 제품 방향과 맞지 않는다. 모델링한 부분집합 밖의 입력은 형태 오류이며 0건이 아니다.
2. **엔진은 Opengrep 2.0.0(LGPL-2.1), 규칙은 CODEYE가 새로 쓴다.** 규칙 팩 `codeye-taint`(지금 v2)는 여섯 언어의 요청 입력 출발점과 명령·경로·SQL·SSRF·역직렬화·코드 평가·HTML 출력 위험 호출만 담는다(35개). Semgrep 레지스트리 규칙은 Semgrep Rules License v1.0이라 쓰지 않았고, 라이선스를 확인하지 않은 규칙 집합도 쓰지 않았다. 바이너리는 저장소에 넣거나 내려받지 않고 운영자가 설치하며, 없으면 `tool_not_configured`다.
3. **파일 안 흐름만 주장한다.** `--taint-intrafile`만 켜고 `--enable-interfile`은 켜지 않는다(2.0.0에 이 스위치가 있음을 확인했다). 파일 사이 데이터 흐름을 분석했다고 표시하지 않고 `intra_file_flows_only`·`request_input_sources_only`를 항상 남긴다.
4. **SARIF가 근거, JSON 사본은 대조.** 한 번의 scan이 stdout에 SARIF를, `--json-output`으로 scratch에 JSON을 쓴다. SARIF에는 검사한 파일 목록이 없고 오류 경로가 문구 안에만 있으므로, 처리 파일과 파일별 오류는 JSON 사본에서 읽고 결과 집합을 서로 대조한다. 두 번 실행하는 안은 시간이 두 배이고 두 실행 사이에 결과가 달라질 수 있어 채택하지 않았다. 넘긴 파일 하나에 대한 오류가 있으면 수준·유형과 관계없이(일부만 해석, 전혀 해석 못 함, 규칙 제한시간, 모르는 유형 모양) 그 파일을 처리 파일로 주장하지 않고, 작업은 실패시키지 않는다. 파일 하나 때문에 선택 분석기 전체를 잃지 않으면서 깨끗하다고 과장하지도 않는다. `executionSuccessful`은 문법 오류가 있어도 true라(측정) 완전성의 근거로 쓰지 않는다.
5. **규칙 문맥.** 파일을 명시해 넘기므로 저장소 `.semgrepignore`·`.gitignore`가 적용되지 않는다(측정: 디렉터리 대상은 적용, 파일 대상은 미적용). 그래서 `repository_config=overridden`이다. `nosemgrep`·`nosem`은 도구가 적용하고 SARIF가 알리므로 `source_suppressions=reported`다.
6. **흐름 단계는 관련 위치다.** `--dataflow-traces`의 단계를 새 관련 위치 종류 `tool_flow_step`으로 보고서까지 싣는다. 관련 위치를 싣는 분석기 목록(보고서 builder·validator, 웹 parser, 번들 검사)에 `any.opengrep`을 더했다. 단계 문구는 고정 문장으로 만들어 코드를 인용하지 않는다. 원시 SARIF에는 소스 조각이 남으므로 `tool_output_holds_source_text`를 기록한다.
7. **규칙 설명은 한 문단, 제목은 첫 문장.** 규칙마다 위험한 이유와 고치는 방법을 담은 한국어 설명(CWE 포함)을 규칙 설명 표에 둔다. 진단 제목에는 첫 문장만 쓴다(Go 보고서와 웹 모두). 기존 설명은 한 문장이라 제목이 바뀌지 않는다. 결함 후보의 영향은 설명의 앞 두 문장, 개선 방향은 나머지 문장이다. 이 문구들은 고정 문구라 코드 없는 보고서도 그대로 싣는다.
8. **엔진 중복.** bandit, ESLint 기준·type-aware, go vet·Staticcheck·errcheck, SpotBugs, clang-tidy(`bugprone-command-processor` = cert-env33-c), cppcheck를 같은 fixture에 실행했다. 모든 규칙에 다른 도구가 보고하지 않는 변형이 있어 규칙을 하나도 빼지 않았다. 같은 줄의 두 보고는 각자 관찰로 남는다([엔진 중복](ANALYZERS.md#엔진-중복)).
9. **측정.** 실제 프로젝트 결함 집합 dev 분할 11건의 분석기만 실행에서 Opengrep은 모든 사례의 범위 파일(합계 509개)을 끝까지 검사했고 진단은 0건이었다. 도구 진단의 결함 줄 적중(±2줄)은 전후 모두 1/11이다. 결과가 나오도록 규칙을 사례에 맞추지 않았다. 이 집합에서는 품질 향상이 측정되지 않았다. 이 측정은 v1 규칙이고 v2로는 다시 재지 않았다.
10. **규칙은 분할한 측정으로만 고친다.** 레이블 벤치마크(OWASP BenchmarkJava·BenchmarkPython, Juliet C/C++)와 공개 웹 애플리케이션을 결과를 보기 전에 개발·보고 전용으로 나눴다. 규칙은 개발 부분에서만 고쳤고, 고친 규칙이 생기면 팩 버전을 올린다(v2). 새 출발점·위험 호출·정리 함수마다 양성·음성 fixture를 둔다. 규칙을 고치는 동안 보고 전용 집계와 애플리케이션 진단이 함께 보였고, BenchmarkPython의 홀수 사례 하나와 Juliet C/C++의 홀수 변형을 열어 보았다. 이는 분할 위반이다. 그래서 그 자료를 모두 개발 숫자로 내렸다. 대신 규칙을 마지막으로 고친 뒤 처음 보는 Juliet Java 1.3과 애플리케이션 아홉 개를 최종 보고 전용 자료로 따로 정했다. 함께 정한 h2o는 첫 실행 뒤 C 규칙이 바뀌어 개발 숫자로 센다. 보고 전용 자료는 규칙을 고치는 동안 실행하지 않는다. 결과는 집계와 공개 프로젝트 이름으로만 [측정](ANALYZERS.md#opengrep-규칙-측정)에 둔다.
11. **어댑터는 쓰는 것만 검사하고, 바꿀 수 있는 것은 거부한다.** 쓰지 않는 문구의 내용 검사로 civetweb 스캔 전체를 잃었으므로 쓰지 않는 문구는 검사하지 않는다. 대신 해석을 바꿀 수 있는 입력은 거부한다. 대소문자만 다른 키(Go JSON 디코더가 같은 멤버로 읽는다), 허용 기준 ID를 다시 정의한 `originalUriBaseIds`, 팩과 다른 `columnKind`, 시작 줄 없는 위치가 그렇다. 열 단위는 도구가 적지 않으므로 팩이 측정값(Opengrep은 바이트)으로 정한다.
12. **선택은 불러온 규칙 파일이다.** `rule_pack.selection`은 그 실행이 불러온 규칙 파일만 적는다. 규칙 파일 전체를 적으면 Python만 있는 저장소와 Go만 있는 저장소의 실행이 같은 규칙 문맥으로 보인다.
13. **분류와 기본 심각도.** A61의 표에서 Opengrep의 명령·SQL·경로·코드 평가·역직렬화·HTML 규칙은 `injection` 계열, SSRF는 `security_weakness` 계열이고, 규칙의 상한(`Cap`)으로 기본 심각도를 medium·low로 낮춘다. 실제 애플리케이션 27개의 진단 10건 중 실제 흐름이 0건이어서 계열 등급을 뒷받침하는 측정이 없기 때문이다. 상한을 올리려면 실제 애플리케이션의 보고 전용 근거가 있어야 하고, 검증이 지지한 분류 문제 묶음은 자기 심각도를 가진 모델 finding이 된다. 흐름 단계는 cppcheck·clang-tidy의 메모와 같은 경로(`relatedForModel`)로 해석 창과 분류 요청에 관련 위치로 간다. 분류 요청에는 묶음 자신의 문맥 발췌 안에 있는 단계만 간다. 같은 줄의 다른 도구 보고와는 A61의 병합 계열로 한 묶음이 된다. 엔진 중복에서 같은 줄을 보고한 SpotBugs의 경로·서블릿 XSS 검출기도 병합을 위해 `injection`으로 두되 표에 없던 때의 low를 상한으로 지킨다(SpotBugs 표 2). 다른 팩의 기본 심각도는 바꾸지 않는다. 도구 finding의 영향·개선 방향은 고정 규칙 설명이고 모델의 분류·검증 문구로 바뀌지 않는다.

[분석기](ANALYZERS.md#opengrep-패턴taint-팩) · [SARIF 입력 어댑터](ANALYZERS.md#sarif-입력-어댑터)

**A67 — GCC 분석의 복잡도 한도는 그 단위의 한계이고, 끝까지 분석한 단위를 실패로 만들지 않는다.** (기록 2026-11-26) REQ-007/008/012, NFR-006과 TC-320에 대해 다음을 확정한다. 실제 C 프로젝트의 분석기만 실행에서 GCC 작업이 모두 `gcc_analyzer_too_complex`로 실패했고, 끝까지 분석한 단위가 있어도 처리 파일이 0이었다([평가 결과](EVALUATION-RESULTS.md#분석기-실행-범위도구만-조건)).

1. **단위 상태.** `-Wanalyzer-too-complex`를 낸 단위는 `partial_too_complex`다. GCC는 한도에 걸린 지점의 경로만 더 따라가지 않으므로 그 단위에서 찾은 진단은 관찰로 남는다. 끝까지 분석하지 않았으므로 처리 파일은 아니다.
2. **작업 상태.** 끝까지 분석한 단위가 하나라도 있으면 작업은 성공이고, 한계 `gcc_analyzer_too_complex`와 `target.properties.units_too_complex`를 남긴다. 모든 단위가 한도에 걸리면 `failed / gcc_analyzer_too_complex`다. 관찰 envelope 계약은 처리 파일 없는 성공을 받지 않는다(아무것도 끝까지 분석하지 않은 깨끗한 결과로 읽히므로). Opengrep·gitleaks·lizard도 처리 파일이 없으면 성공하지 않는다. 이 계약은 바꾸지 않았으므로 번역 단위 하나짜리 C 프로젝트에서 그 단위가 한도에 걸리면 작업은 전처럼 실패한다. 다른 단위의 컴파일 실패·시간 초과·출력 오류는 그대로 작업을 실패시키고 그 사유가 앞선다.
3. **채택하지 않은 안.** (가) 한도에 걸린 단위를 처리 파일로 세는 안은 GCC가 끝내지 않은 범위를 주장한다. (나) 탐색 한도를 올리는 안(`--param analyzer-bb-explosion-factor`, `analyzer-max-svalue-depth`, `-fanalyzer-call-summaries`)은 같은 사례에서 한도 경고가 사라진 파일이 하나뿐이어서 GCC 기본값을 유지했다. (다) 이전처럼 작업 전체를 실패시키는 안은 끝까지 분석한 단위의 결과를 버린다.
4. **게시 규칙은 그대로다.** 메모리·`FILE` 누수, 이중 해제, 해제 뒤 사용, 널 역참조 계열 `-Wanalyzer-*` 열두 규칙을 더한 별도 규칙 팩 버전으로 같은 사례를 다시 쟀다. 더 나온 진단은 모두 시험 코드에 있었고 배포 코드에는 없었다. 시험 코드의 진단은 대부분 시험 framework의 단언 매크로가 실패 시 돌아오지 않는다는 것을 GCC가 모르는 데서 나온 오탐이었다. 배포 코드의 정밀도 표본이 없으므로(0건) 규칙 팩은 파일 디스크립터 일곱 규칙 v1로 두고 기본 심각도도 바꾸지 않았다.

[GCC 파일 디스크립터 검사](ANALYZERS.md#gcc-파일-디스크립터-검사)

**A68 — compile DB에 없는 C/C++ 소스에 다른 항목의 컴파일 인자를 빌려 주지 않는다.** (기록 2026-11-26) REQ-007/008, NFR-006에 대해 다음을 확정한다. compile DB 모드의 범위 안 소스 중 DB에 항목이 없는 것은 전과 같이 `not_in_compile_db`이며 처리 파일이 아니다.

1. **측정.** clangd 식 보간(같은 디렉터리와 가장 긴 공통 경로의 항목, 같은 확장자 우선)으로 C 사례 세 개의 빠진 소스에 인자를 만들어 cppcheck·clang-tidy를 제품 선택 그대로 실행했다. 빠진 소스는 대부분 시험 코드와 시험 framework에 딸린 예제였고, 빌린 인자로는 framework 헤더를 찾지 못해 그 대부분이 clang-tidy 컴파일 오류로 끝났다. 배포 코드 경로의 빠진 소스는 몇 개뿐이었고 컴파일되어 진단을 냈다. 그것은 꺼진 build 옵션의 소스, fuzz harness, 시험 프로그램처럼 구성한 build가 컴파일하지 않는 파일이다. 그 진단이 실제 문제인지의 표본은 읽지 않았다. 수치는 [평가 결과](EVALUATION-RESULTS.md#분석기-실행-범위도구만-조건)에 있다.
2. **이유.** compile DB는 프로젝트가 구성한 build가 무엇을 어떤 인자로 컴파일하는지의 근거다. 빌린 인자의 결과에는 그 근거가 없고, 시험 경로에서는 컴파일 오류만 늘었다. 배포 코드 경로에서 더 얻는 진단은 구성한 build 밖의 코드이고 정밀도의 근거가 없다. 빌린 인자의 관찰과 DB 근거의 관찰을 한 작업에 섞으면 처리 파일과 작업 상태의 뜻이 흐려진다.
3. **다시 볼 조건.** 넣는다면 같은 디렉터리의 항목만 빌리고, DB 근거의 작업 결과(상태·처리 파일·단위 상태)를 바꾸지 않는 별도 최선 노력 단계로 둔다. 배포 코드에서 그렇게 얻은 진단이 실제 문제라는 표본이 있을 때 한다.

[번역 단위 범위](ANALYZERS.md#번역-단위-범위와-생성-헤더)

**A69 — cargo check·clippy는 시험·벤치·예제 대상만 컴파일되지 않으면 그 종류를 빼고 다시 실행한다.** (기록 2026-11-26) REQ-007/008/012, NFR-006과 TC-322에 대해 다음을 확정한다. A22 10의 `--all-targets`는 기본 선택으로 유지한다. 실제 Rust 프로젝트의 분석기만 실행에서 cargo check·clippy가 `package_errors`로 실패했고, 원인은 안정 toolchain이 nightly 전용 벤치의 `#![feature]`를 거부한 것(E0554)이었다. 라이브러리와 바이너리는 컴파일되었는데 배포 코드의 결과가 실패한 작업에 갇혔다([평가 결과](EVALUATION-RESULTS.md#분석기-실행-범위도구만-조건)).

1. **조건.** 첫 실행이 exit 101이고 stdout을 끝까지 해석했으며, lint가 아닌 컴파일 오류의 대상 종류가 모두 `test`·`bench`·`example`이고, stderr가 다른 원인(toolchain, lockfile, 의존성, linker, manifest, build script, 문서 충돌)을 말하지 않을 때만 다시 실행한다. 대상 종류는 cargo JSON 레코드의 `target.kind`에서 읽고 stderr 문구로 추측하지 않는다.
2. **재실행.** 실패한 종류만 뺀 대상 플래그로 한 번만 실행하고, 결과는 재실행의 판정 그대로다. 새 target 디렉터리를 쓴다. 실패한 대상이 남긴 dep-info가 처리 파일로 읽히지 않게 하기 위해서다. 한계 `failing_target_kinds_excluded`와 `excluded_target_kinds`·`targets`·선택으로 무엇을 빼고 검사했는지 남긴다.
3. **채택하지 않은 안.** (가) 처음부터 `--lib --bins`만 검사하는 안은 컴파일되는 시험 코드의 진단과 처리 범위를 모든 프로젝트에서 잃는다. (나) nightly toolchain으로 바꾸는 안은 운영자가 정한 toolchain을 바꾸고 다른 결과를 낸다. (다) 첫 실행의 실패한 대상 오류를 성공한 envelope의 도구 오류로 싣는 안은 envelope 계약(성공에는 도구 오류가 없음)에 어긋난다. 첫 실행의 원시 stream은 실행 기록으로 남는다.

[Rust 실행](ANALYZERS.md#rust-실행과-lint-수준)

**A70 — SpotBugs 컴파일은 모듈 기술자를 빼고, 전이 classpath는 Java 의존성 받기로 미룬다.** (기록 2026-11-26) REQ-007/008/012/014, NFR-006과 TC-323에 대해 다음을 확정한다. 실제 Java 프로젝트의 분석기만 실행에서 SpotBugs가 `java_compilation_failed`로 실패한 원인은 classpath가 아니라 모듈 기술자였다. `module-info.java`를 다른 소스와 함께 넘기자 javac가 이름 있는 모듈로 컴파일하며 모든 파일을 `file should be on source path, or on patch path for module`로 거부했다([평가 결과](EVALUATION-RESULTS.md#분석기-실행-범위도구만-조건)).

1. **기술자는 컴파일하지 않는다.** 클래스는 이름 없는 모듈로 컴파일한다. 직접 의존성 JAR가 classpath에 있으므로 이름 있는 모듈에서는 읽히지 않는 JAR도 읽힌다. SpotBugs는 같은 바이트코드를 읽는다. 모듈 경계는 검사하지 않으며 한계 `java_module_descriptor_not_compiled`로 남긴다. 기술자만 있으면 컴파일할 것이 없으므로 `unsupported / java_production_sources_unselected`다.
2. **채택하지 않은 안.** 기술자가 있는 디렉터리를 `-sourcepath`로 주는 안은 이 사례에서도 컴파일되었으나, 모듈이 `requires`하는 다른 모듈을 `--module-path`로 줘야 하고 classpath의 직접 의존성 JAR가 이름 있는 모듈에서 보이지 않는다. 지금의 classpath 계약(직접 의존성만, 카탈로그로 검증)과 맞지 않는다.
3. **전이 classpath의 범위.** A42~A44의 틀로 넣는다면 `deps.java` 받기 작업(필수 아님, 프로젝트 선택과 노드 스위치가 모두 켜져야 함, 받은 저장소는 Maven 저장소 배치의 공유 캐시)이 저장소 코드를 실행하지 않고 POM을 읽어 의존성을 푼다. 이를 위해서는 부모 POM 사슬, `import` 범위 BOM과 `dependencyManagement`, 속성 치환, 범위(`compile`·`provided`·`runtime`), 제외와 `optional`, 가까운 쪽이 이기는 버전 조정, 버전 범위와 relocation을 Maven과 같게 구현해야 한다. JDK·OS로 켜지는 profile은 평가하지 않는 한계로 남는다. lockfile이 없으므로 고정 근거는 저장소의 체크섬뿐이고 `dependency_versions_not_pinned`가 된다. Gradle은 Gradle을 실행하지 않고는 풀 수 없어 리터럴 선언과 버전 카탈로그까지만 다룬다. 받기 기록 schema·보고서 공백·분석기 연결을 함께 바꾸는 큰 변경이라 이 결정에서는 구현하지 않는다.
4. **측정이 말하는 우선순위.** 이 집합의 Java 사례에서는 직접 의존성만으로 인한 컴파일 실패가 없었다. 기술자를 뺀 뒤 SpotBugs 작업이 모두 컴파일되었다. 전이 classpath는 실제 프로젝트에서 그 때문에 실패하는 측정이 생길 때 다시 본다.
5. **범위 밖 보고는 따로 센다.** 위치를 연결하지 못했다고 센 SpotBugs 보고의 대부분은 경로 범위 밖에 있는 같은 모듈 운영 소스의 보고였다. 그 소스는 컴파일 문맥으로만 넘기고 근거는 선택한 소스에만 연결하므로 관찰로 만들지 않는 것이 계약이다. 위치 실패와 섞이지 않게 `java_findings_outside_scope`로 따로 세고 `observations_outside_scope_omitted`를 남긴다(Rust와 같은 한계 코드). 범위 밖 보고를 관찰로 싣는 안은 사용자가 고른 범위를 넘어 발견을 게시하므로 채택하지 않았다. 남은 위치 없는 보고는 클래스·메서드 수준이고 대부분 낮은 우선순위의 한 규칙이어서, 메서드 줄로 위치를 대신 잡는 변경은 정밀도 근거가 생길 때 한다.

[Java 팩](ANALYZERS.md#java-팩) · [Java 로컬 클래스패스](ANALYZERS.md#java-로컬-클래스패스)

**A71 — cargo doc은 라이브러리 이름이 겹치는 패키지 쌍에서 더 깊은 쪽을 빼고 다시 실행한다.** (기록 2026-11-26) REQ-007/012, NFR-006과 TC-327에 대해 다음을 확정한다. A69와 같은 방식으로 재실행한다. 실제 Rust workspace에서 rustdoc 작업이 `doc_output_collision`으로 실패했다. C API 래퍼 패키지가 본 패키지와 같은 라이브러리 이름을 써서 cargo가 workspace 전체의 문서화를 거부했다([평가 결과](EVALUATION-RESULTS.md#분석기-실행-범위도구만-조건)).

1. **조건.** 첫 실행이 exit 101이고 stream을 끝까지 받았으며, stderr가 출력 이름 충돌만 말하고(toolchain, lockfile, 의존성, linker, manifest, build script, 컴파일 오류가 없음) 충돌한 패키지 쌍을 cargo 문구에서 읽을 수 있을 때만 다시 실행한다.
2. **뺄 패키지.** 각 쌍에서 manifest 디렉터리가 더 깊은 패키지를 뺀다. 깊이가 같으면 이름 순서가 뒤인 것을 뺀다. 저장소 루트의 패키지가 보통 본 라이브러리이고 하위의 같은 이름은 래퍼라는 관찰에 따른 규칙이며, 결정적이어서 같은 입력에서 같은 패키지를 뺀다.
3. **기록.** 한계 `doc_colliding_packages_excluded`, `excluded_packages`, 선택의 `--exclude`로 무엇을 빼고 문서화했는지 남긴다. 채택하지 않은 안은 패키지마다 따로 문서화하는 것이다. 같은 출력 경로를 쓰므로 실행마다 target 디렉터리를 나눠야 하고, 실행 수가 패키지 수만큼 는다.

## 모델 검토

**A21 — 단계 기록과 필수 검토 역할.** (기록 2026-11-18) REQ-009/010/011/013/027과 TC-97에 대해 다음을 확정한다.

1. 단계 기록은 같은 media type의 schema `0.2`다. 단계마다 자기 호출만 담고, 읽은 앞 단계·탐색·도구 산출물을 SHA-256으로 묶는다. 보고서는 이 값과 수락한 산출물을 대조한다. 이전 팩의 `0.1` 기록에 팩 필드를 더하는 안은 그 기록·요청 바이트를 바꾸므로 채택하지 않았다. 앞 단계 호출을 누적하는 안은 기록이 커지고 앞 단계 결과를 다시 검증해야 하므로 채택하지 않았다. (A52에서 `0.1` 기록과 그 decoder를 지웠다.)
2. 필수 검토 역할은 계획된 `review.*` task의 `required_for_verdict`로 정한다. 환경 영향과 정적 진단 해석은 필수가 아니다. 문서 검토는 계획 시 범위 안에 안내 문서가 있을 때만 필수다. 필수 task가 없으면 `not_performed`다. (다섯 역할 key가 없는 계획을 이전 팩의 세 역할로 해석하던 규칙은 A52에서 지웠다. 검토 task가 없는 계획은 필수 역할이 없다.)
3. 실행·재현 주장 거부는 정규식 휴리스틱이다. 놓친 주장이 있어도 상태·재현·통계는 구조화 기록에서만 정하므로 모델 서술이 판정을 바꾸지 못한다.

[모델 검토](REVIEW.md)

**A29 — 언어 지침 선택과 사례 없는 지침.** (기록 2026-11-18) REQ-027과 TC-100에 대해 다음을 확정한다.

1. 언어 지침은 스냅샷 scope 안의 소스 생태계로 고른다. 판별은 분석기 팩의 공백 계획과 같은 `pack.InScope` 하나다([분석기](ANALYZERS.md)의 범위에 없는 생태계와 팩 공백). 소스 확장자로 세고 data·vendor 디렉터리는 빼며, build 파일만으로는 생태계가 들어오지 않는다. 새 판별을 만들지 않았다. 고른 지침은 공통·역할·영역 지침 뒤에 생태계 이름 순서로 붙는다. 지원 생태계가 없으면 붙이지 않고, 지침이 없는 생태계는 아무것도 더하지 않는다. 언어 생태계(`obsdoc.Ecosystems`에서 `any`를 뺀 것)마다 지침이 있음을 시험으로 고정했다. 모든 지침을 항상 붙이는 안은 요청을 늘리고 범위 밖 언어의 관점을 섞으므로 채택하지 않았다. Go 지침만 붙이는 이전 방식은 다른 언어 스냅샷에 맞지 않는다.
2. 역할별 digest는 pack.yaml과 고른 파일만 덮는다. 따라서 digest는 (역할, 생태계 조합)마다 다르다. 호출 기록의 `pack_files`가 쓴 언어 지침을 보이므로 기록 schema는 바꾸지 않았다. 같은 사실을 담는 별도 필드는 두지 않았다.
3. 지침 파일에는 평가·시험 사례의 내용을 쓰지 않는다. 사례 ID, fixture 경로와 식별자, 특정 문서·재시도 횟수·버전 조합이 여기에 해당한다. 예시가 필요하면 사례 없는 규칙으로 쓴다. 이전 지침에 있던 사례 유래 예시는 모두 지우거나 규칙으로 바꿨다. `bundle_test.go`가 사례 ID와 fixture 이름을 검사한다. 같은 뜻의 다른 문장은 이 검사로 잡지 못하므로 지침을 바꿀 때 검토에서 확인한다.
4. 다섯 생태계가 모두 범위에 있으면 지침이 7,370바이트 늘어난다. 이때 합성 최악 입력의 검증 역할은 최대 길이 후보 6개 중 1개를 보내지 못한다. 그 후보는 `unreviewed`(`verification_budget_exceeded`)로 남고 task는 `review_input_incomplete`로 실패하며, 성공으로 바꾸지 않는다. 요청 한도(40,000바이트)와 역할별 지침 구성은 바꾸지 않았고 소유자 결정으로 남긴다. 다섯 언어 코드 발췌를 넣은 뒤의 측정과 역할별 지침 선택을 채택하지 않은 이유는 A31에 있다. (A58에서 소유자 승인으로 요청 한도를 운영자 예산으로 바꿨다.)
5. 이전 팩과 그 고정 시험은 바꾸지 않았다. 이 결정 시점의 팩은 실모델로 평가하지 않았고 기본으로 승격하지 않았다. 기본 팩 결정은 A33이다. (A52에서 이전 팩을 지웠다.)

[모델 검토](REVIEW.md)

**A31 — 다섯 언어 검토 입력.** (기록 2026-11-19) REQ-004/009/027과 TC-123에 대해 다음을 확정한다.

1. **종류 판별.** 발췌의 code·test·config는 경로만으로 정한다(`record/source.go`). 같은 규칙을 review 입력 선택과 worker·보고서 decoder가 쓴다. code는 `pack.SourceFiles`와 같은 소스 확장자이고 시험이 두 표를 같게 묶는다. test는 언어별 이름·디렉터리 관례, config는 고정 manifest 목록이다. Go 규칙(`*_test.go`, go.mod)은 바뀌지 않아 이전 기록이 그대로 읽힌다. JSON schema의 제약은 바꾸지 않았고 설명에 경로 규칙을 적었다.
2. **이전 팩 코퍼스.** (A52에서 이전 팩과 함께 지웠다.) 순위는 진입점, 문서, 소스, manifest, 테스트다. 예산·읽기·비밀값 선별은 기존 코퍼스 규칙과 같다. Go·README/AGENTS·go.mod 분기는 그대로 두고 다른 생태계만 덧붙였다. 그래서 Go 저장소의 선택은 구조적으로 바뀌지 않는다. vendor 아래 Go 파일도 계속 후보다. 다른 생태계의 파일과 manifest는 `pack.InScope`에 그 생태계가 있을 때만 넣는다. 팩 지침(Go 지침만)은 승격 근거 없이 바꿀 수 없어 그대로다.
3. **다섯 역할 지침.** 다른 언어 파일은 그 생태계 manifest의 가장 깊은 디렉터리로 묶는다. 순회는 생태계마다 첫 묶음부터 돈다. 발췌는 파일 첫 줄부터이고 해석 창은 진단 위치 주변의 줄 창이다. 구문 분석기는 없다. 호출자 확장은 Go만 한다(`caller_expansion_go_only`). Go만 있는 스냅샷의 요청은 변경 전 캡처와 같다.
4. **제외.** 분석기 팩의 data 디렉터리 규칙에 더해, Go가 아닌 파일은 모든 깊이의 `build`·`dist`·`target`·`coverage`·`htmlcov`·`site-packages`·`venv`·`__pycache__`·`third_party` 아래에서 뺀다. 중첩 산출물을 모델에 보내지 않기 위해서다. 같은 이름의 소스 디렉터리도 빠지는 비용을 받아들였다. 이렇게 빠진 범위 안 소스는 `source_in_excluded_directories`로 기록한다. 이 규칙은 `pack.InScope`보다 넓다. 그래서 언어 지침은 붙지만 그 언어 코드가 없는 요청이 생길 수 있다. Go는 이 규칙에서 뺐다. internal/build 같은 Go 패키지가 흔하기 때문이다.
5. **언어 지침은 범위 안 생태계 규칙(A29)을 유지한다.** 역할 입력에 있는 언어만 고르는 안을 시험 구현했다. 큰 다섯 언어 최악 입력에서 verify는 두 규칙 모두 최대 후보 6개 중 5개를 보냈다. 조사 역할은 입력에 다섯 언어가 모두 있어 지침이 줄지 않았다. 줄어든 것은 한도 압박이 없던 해석 역할(32,223→26,676바이트)뿐이었다. 압박을 줄이지 못하고 digest 조합만 늘리므로 채택하지 않았다. 요청 한도는 소유자 결정으로 남는다. (A58에서 바꿨다.)
6. **평가.** 이 결정은 실모델로 평가하지 않았다. 언어별 코드가 입력에 들어간다는 것은 확인했지만 검토 품질이 나아졌다는 근거는 아니다.

[모델 검토](REVIEW.md)

**A33 — 기본 검토 팩 2.0.0.** (기록 2026-11-19) REQ-010/011/013/027, NFR-007/008/010과 TC-100, TC-128·TC-129에 대해 다음을 확정한다.

1. **기본 팩.** 소유자가 검토 팩을 하나로 합치기로 했다. 다섯 역할 지침을 2.0.0(`status: default`)으로 올려 팩을 지정하지 않은 에이전트·워커·개인 설정의 기본값으로 썼다. 세 역할 이전 지침은 지우지 않고 노드 설정으로만 고르는 팩으로 두었다. 기존 기록이 그 팩의 id·digest로 해석되고 고정 시험이 재현 가능한 요청 바이트를 고정했기 때문이다. 디렉터리·id 이름을 바꾸는 안은 기존 기록의 팩 대조와 운영자 설정을 깨므로 채택하지 않았다. 근거는 보류 평가의 두 팩 비교다([평가 결과](EVALUATION-RESULTS.md)). 이 근거로 다섯 역할 지침이 더 우월하다고 표시하지 않는다. (A52에서 이전 팩과 팩 선택을 지우고 디렉터리·id를 `prompts/review`·`review`로 바꿨다.)
2. **이전 팩 규칙 통합.** 팩마다 따로 고치고 비교하는 대신 이전 팩의 규칙을 다섯 역할 지침에 대조해 옮긴다. 기준은 다섯 역할 지침에 없고, 근거 없는 보고나 실행 주장을 막는 정밀도 규칙이거나 개발 입력에서 관찰한 오탐 유형을 다루는 것이다. 평가 결과를 읽지 않은 작성자가 대조했고 한 문장을 verify 지침에 옮겼다. 재현율·수정 조언 항목은 요청 크기를 늘리므로 옮기지 않았다.
3. **복구 경로.** JSON이 아닌 응답은 항목 정보가 없으므로 형식 복구를 쓰지 않고 한 번 재시도한다. 단계당 최대 3회 호출이고 기록 schema `0.2`의 상한을 3으로 넓혔다. verify에서 그 요청에 플랫폼이 넣은 관찰 ID만 틀린 항목은 그 ID를 빼고도 판정 규칙을 통과할 때만 받아들이고 `evidence_id_dropped`를 남긴다. 요청에 없던 ID는 빼지 않고 거부한다. 모르는 ID를 모두 빼는 안은 지어낸 근거를 받아들이는 길을 열어 채택하지 않았다. 후속 호출이 형식 이외의 이유로 실패하면 수락한 판정은 유지하고 나머지를 격리한다(`verification_call_failed`). 판정을 만들거나 미완료를 완료로 바꾸지 않는다. 보고서의 `review_calls` 상한은 15다(A37에서 모듈 단위 검토 분할의 호출 상한 최댓값 207로 넓혔다). 지침·계약 문구로 막는 안은 보류 평가의 독립성을 지키기 위해 채택하지 않았다. 횟수 제한 없는 재시도는 비용 상한이 없어 채택하지 않았다.
4. **회귀 검증.** 지침 교체마다 두 팩을 나란히 비교하지 않는다. 기본 팩 개정은 기본 회귀 집합(`fixtures/regression/`)을 사전 기준으로 실행하고, 독립 품질 주장은 새 보류 사례로만 한다. 2.0.0은 같은 팩 id의 다른 digest이므로 보류 집합 재실행은 `holdout_seen_by_prior_revision`이다.

[모델 검토](REVIEW.md) · [평가](EVALUATION.md)

**A37 — 모듈 단위 검토 분할과 호출 상한.** (기록 2026-11-19) REQ-009/012/027, NFR-007/008과 TC-160~169에 대해 다음을 확정한다. 7·8은 보고서 재구성(A34) 병합 뒤에 더했다. 4·5는 유료 회귀 실행 뒤 소유자 방향(기본 설정으로 쉽고 충분히 검토하고, 이 단계에서는 보안 관문으로 사용을 막지 않는다)에 따라 고쳤다.

1. **분할.** overview(전체 역할 다섯 개)는 그대로 두고, overview가 보내지 못하는 코드·테스트 파일을 모듈 단위 slice로 나눠 slice마다 investigate·verify 한 쌍으로 검토한다. 모듈 단위는 overview 코퍼스의 묶음이다. 지침 문구는 바꾸지 않았다. slice 요청은 overview의 요청 생성기·계약 문자열·팩 파일을 그대로 쓰므로 지침 hash가 같다. 문구를 바꾸려면 팩 버전, 독립 작성자, 유료 회귀 실행이 필요하다.
2. **결정적 계획.** 계획은 manifest·scope·상수로만 정한다. 재시도는 task 행을 queued로 되돌리고 남은 task가 있으면 완료를 거절하므로, 호출 상한이 task key를 정하면 상한을 바꾼 재시도에서 고아 task가 생긴다. overview 접두 P는 상한 크기 `min(2400, 6 × size + 2)`로 overview 순회를 흉내 내 "반드시 보내는" 파일만 담고, 실행 때 `review.scope`가 overview가 실제로 보낸 파일을 slice에서 뺀다. slice는 32개까지다(A45에서 계획은 slot 수만 정하고 실행 때 채우며 96개까지로 바꿨다).
3. **판정 필수 아님.** slice를 필수로 두면 상한보다 큰 저장소가 모두 `partial`이 되어 run 상태가 의미를 잃는다. 검토하지 못한 범위는 task 상태와 범위 산출물로 남기고, 진행 단계에서는 거절된 slice가 `code_review`를 `partial`로 남긴다.
4. **호출 상한과 승인.** 단위는 attempt당 모델 호출이고 노드 운영자 설정이다(`--review-max-calls`, 기본 99, 15~207, run 단위 변경 없음). 처음 기본값 33은 유료 회귀 실행에서 파일럿 Go 모듈 67개 파일 중 34개를 28회 호출로 검토하는 데 그쳤다. 기본 설정만으로 중간 규모 저장소의 대부분을 검토하도록 99로 올렸다(사용을 막지 않는다). 회귀 실행에서 실모델 호출 55회의 비용은 보고된 사용량으로 약 USD 0.94, 보수적 장부 차감으로 USD 2.02였다. 기본 상한 99회를 다 쓰는 run은 그 대략 두 배(보고 사용량 약 USD 1.7, 장부 차감 약 USD 3.6)다. 실제 비용은 모델·저장소 크기·형식 복구 횟수에 따라 다르다. 낮추려면 운영자가 상한을 정한다([안내서](GUIDE.md)). 전체 역할은 최대 15회이고 모든 slice보다 먼저 실행되므로 잘리지 않는다. 예약을 따로 두는 안보다 단순해서 이 순서를 택했다. slice는 계획 순서대로 `남은 호출 ≥ 6`일 때 승인한다. 최악 사례로 미리 승인하는 안은 상한의 대부분을 쓰지 않아 채택하지 않았다. 실제 사용량으로 승인하므로 승인되는 마지막 slice가 형식 복구 횟수에 따라 run마다 달라질 수 있고, 그 결과는 slice task 상태로 보인다. 계수는 attempt scratch의 파일이며 전역·프로세스 간 상태가 없다. 한 attempt 안의 slice는 같은 계수 파일과 계획 순서의 결정 규칙으로 함께 실행한다(A64).
5. **선별 관문 없음.** 처음에는 slice가 저장소를 몇 배 더 보내므로 완료된 비밀값 선별이 있을 때만 실행했다(`secret_screen_unavailable`). gitleaks가 없는 설치에서 추가 검토가 전혀 실행되지 않아, 사용을 막지 않는다는 이유로 이 관문과 사유 코드를 없앴다. 범위 작업과 slice는 전체 역할과 같은 규칙을 따른다. 선별이 끝났으면 보고된 파일을 빼고, 선별이 계획되지 않았거나 실행되지 않았으면 기본 읽기 필터(민감값 패턴·보류 파일)만 거친 입력을 보내며 범위 산출물(`screen: not_screened`)·기록·보고서가 미선별임을 밝힌다. 선별이 실행됐지만 결과를 쓸 수 없으면 전체 역할처럼 `secret_screen_failed`로 건너뛴다. 선별을 쓸 수 있으면 계속 쓴다. gitleaks는 모델 입력 파일이 범위에 있으면 언어와 관계없이 계획하고, "정적 분석 계획됨" 판단에서는 비밀값 작업을 세지 않는다.
6. **설계 보정.** 계획 단계에서 범위 산출물에 승인된 slice 목록을 두기로 했지만, 산출물은 어떤 slice보다 먼저 쓰이고 승인은 실행 중에 정해지므로 넣지 않았다. 승인 결과는 slice task 상태에서 읽는다. 계획 밖 파일은 목록 대신 모듈별 `files_not_planned`로 세고, P에 있지만 overview가 보내지 못한 파일은 `overview_not_sent`로 표시한다.
7. **보고서 장부와 builder 6.** 장부는 코드 분석 절의 선택 필드 `ledger`이고 `review.scope`가 계획된 실행에만 있다. slice 기록과 범위 산출물은 builder 5에서 `unrecognized_artifact`였으므로, 같은 입력(slice가 있는 실행)에서 보고서 본문이 달라진다. builder 버전은 입력 digest에 들어가고 게시 루프는 보고서가 있는 attempt를 다시 빌드하지 않으므로([보고서](REPORT.md#게시와-리비전)), 버전을 올리지 않으면 같은 입력 digest가 서로 다른 본문을 가리킨다. 그래서 새 필드가 모두 선택·추가 필드여도 builder를 6으로 올렸다. 이미 게시된 builder 5 리비전은 그대로 읽히고(장부·slice 발견·`task_key` 없음), 장부와 `task_key`는 builder 6 이상에서만 허용한다. 같은 버전에서 builder 5가 완료된 깨끗한 선별을 `not_screened`로 읽고 공백 `model_input_not_screened`를 남기던 비밀정보 절 `model_input`에 `screened`를 더했다(두 팩 모두 선별하지 않은 기록에는 `model_input_not_secret_screened`를 남긴다). 그래서 slice가 없는 실행의 builder 6 본문은 버전·digest·`review_calls[].task_key`와 함께, 비밀값 작업이 성공한 실행의 `model_input`·공백 목록·종합 공백 수도 builder 5와 다르다. 가짜 upstream dry run의 회귀 집합 22건이 모두 이 경우였다.
8. **장부의 수.** `calls_used`는 기록 합이 아니라 범위 산출물의 `calls_before_slices`(attempt 호출 기록에서 셈)에 slice 기록의 호출을 더한다. 기록 없이 실패한 전체 역할의 호출도 들어가므로, 상한 대비 사용량을 보고서만으로 확인할 수 있다. slice 기록 없이 호출했을 수 있는 task가 있으면 `null`이다. 모듈 행의 파일은 보냄·비밀값 제외·읽을 수 없음·상한 초과·계획 밖·그 밖의 미전송 중 한 곳에만 센다. 장부와 화면은 이 범위를 "모델에 보냄"으로만 부르고 검증 범위라고 하지 않는다.

[모델 검토](REVIEW.md#모듈-단위-검토-분할) · [분석기](ANALYZERS.md)

**A39 — API 키 하나로 모델 설정.** (기록 2026-11-19) REQ-026/030, NFR-006/008과 TC-182~184에 대해 다음을 확정한다. 소유자는 OpenAI API 키(`OPENAI_API_KEY`)와 `gpt-6.2-sol`을 OpenCode로 쓰려 하고, 그 전까지 제품의 실모델 경로는 QA 예산 relay로만 실행됐다.

1. **일반 공급자.** 모델은 (provider ID, API 키, 선택 endpoint)인 공급자 하나로 부른다. 예산 relay는 그 한 사례(`codeye-gateway`, OpenAI 호환 패키지, 고정 한도)이고 relay 프로필의 `OPENCODE_CONFIG_CONTENT`는 바이트가 이전과 같다(`TestRelayProfileUnchanged`). 회귀 파일럿과 평가 스크립트의 relay 경로를 바꾸지 않기 위해서다. 모델 ID는 `PROVIDER/MODEL`이면 받는다.
2. **키는 파일, 호출마다 읽는다.** 실행기는 OpenCode에 실행별 HOME을 주고 `*API_KEY*`·`OPENAI_*`·`OPENCODE_*` 환경변수를 거른다. 그래서 키는 relay token과 같은 자리(`OPENCODE_CONFIG_CONTENT`의 provider `options.apiKey`)로만 들어가고 기록에서 가린다. 에이전트·워커에는 키 파일 경로만 간다(argv·Hello·로그에 키 없음). 키를 에이전트 환경변수로 넘기는 안은 모든 자식 프로세스 환경과 진단 출력에 퍼질 수 있어 택하지 않았다. 호출마다 읽으므로 키 교체가 재시작 없이 적용된다. OpenCode 프로세스의 환경에는 키가 있다(같은 사용자는 `/proc/PID/environ`으로 읽을 수 있다). relay token과 같은 수준이고 개인 모드에서는 받아들인다.
3. **개인 모드의 기본값.** 키는 `agent.model.credential_file`, `<state_dir>/secrets/model.json`(0600, `codeye up`이 `OPENAI_API_KEY`·`ANTHROPIC_API_KEY`·`OPENCODE_API_KEY`에서 쓴다), OpenCode `auth.json`의 `api` 항목 순으로 찾는다. 모델은 `agent.model.name`, OpenCode 설정의 `model`, provider별 기본값(OpenAI `gpt-6.2-sol`) 순이다. 설정 파일에는 키를 두지 않는다. 설정 파일은 예시로 복사·공유되기 쉽고, 상태 디렉터리는 이미 0700이며 백업에 들어가지 않는다.
4. **개인 모드에서는 모델 검토를 기본으로 허가한다.** 모델이 설정되면 개인 사용자의 모든 로컬 source root가 `--model-source-root`를 받고, `model_review: false`인 root만 뺀다. 개인 모드의 사용자는 자기 키로 자기 코드를 보내는 한 사람이다. 루트마다 허가를 적게 하면 키를 넣어도 검토가 실행되지 않아 사용자가 원인을 찾기 어렵다. 서버 모드는 그대로 운영자의 명시 허가다. (A40에서 프로젝트별 선택으로 바꿨다.)
5. **OpenCode 버전은 막지 않는다.** 시험한 릴리스가 아닌 릴리스도 실행하고 기록·보고서에 `opencode_version_untested`를 남긴다. 시험한 릴리스는 근거와 함께 목록으로 둔다(2026-11-25부터 1.19.5·1.19.6, 근거는 [검토](REVIEW.md#opencode-어댑터-계약)). 운영 머신의 OpenCode가 CODEYE 밖에서 갱신되어 실제 호출이 새 릴리스로 수락된 근거가 생기면 목록에 더한다. 릴리스 형식이 아닌 `--version` 응답만 거부한다. OpenCode는 자주 갱신되고, 사용을 막기보다 한계를 기록하는 쪽이 지금의 제품 방향이다. 응답 수집 규칙(이벤트 형식, workspace 확인)은 그대로라 형식이 바뀐 릴리스는 `model_output_invalid`로 드러난다.
6. **모델은 항상 선언한다.** OpenCode 1.19.5의 오프라인 모델 목록에 `gpt-6.2-sol`이 없고 목록 갱신은 꺼져 있다(`OPENCODE_DISABLE_MODELS_FETCH`). 그래서 provider의 `models`에 모델을 선언한다. 목록에 있는 모델은 목록의 한도·추론 정보를 유지하는 것을 확인했다. 목록에 없는 모델은 기본값(추론 표시 없음, 출력 한도는 OpenCode 기본)으로 간다.
7. **실패해도 분석은 계속.** 모델 실패는 오류 이벤트의 HTTP 상태로 `model_auth_failed`·`model_not_found`·`model_rate_limited`·`model_unavailable`로 나누고, 키 파일을 쓸 수 없으면 `unsupported`(`model_credential_unavailable`)다. 시작 전 확인(`codeye model probe`)이 실패해도 시작을 막지 않는다. 정적 분석은 끝나고 보고서가 검토 미수행과 사유를 적는다. 다른 모델로 자동 전환하지 않는다(REQ-030).
8. **제한시간 180초.** 호출당 기본을 75초에서 180초로 올리고 1초~30분을 받는다. 추론 모델이 40 KB 안팎의 요청에 답하는 시간을 75초가 덮지 못할 수 있어서다. 측정값이 아니라 추정이며 실제 호출 시간을 보고 다시 정한다. (A58에서 600초로 바꿨다.)
9. **확인한 범위.** OpenCode 1.19.5가 이 설정으로 OpenAI Responses API에 스트리밍 요청(`POST {base_url}/responses`, `Authorization` 헤더)을 보내고, 가짜 upstream의 답으로 검토 단계가 수락되는 것까지 확인했다. OpenAI가 이 요청과 모델 ID를 받는지는 확인하지 않았다(유료 호출은 소유자 예산의 장부로 한 번 한다).

[모델 검토](REVIEW.md#모델-설정) · [운영](OPERATIONS.md)

**A52 — 검토 기준은 하나만 둔다.** (기록 2026-11-20) REQ-010/011/013/027, NFR-007/010과 TC-100, TC-129에 대해 다음을 확정한다. A20 4, A21 1·2, A29 5, A31 2, A33 1의 이전 팩 유지를 바꾼다.

1. **지침 하나.** 검토 지침은 `prompts/review`(id `review`, version 2.0.0) 하나다. 노드 플래그·워커 Hello·개인 설정·회귀 실행에서 지침을 고르는 설정을 없앴다. 세 역할(context·investigate·verify) 이전 팩, 그 기록 schema `0.1`과 decoder·보고서 조립 경로, 그 팩의 고정 시험, 두 팩 비교 도구도 지웠다. 제품을 쓴 사람이 아직 없어 저장된 기록·설정과 호환할 필요가 없다. 두 기준을 함께 두면 지침 개정·시험·보고서 해석이 모두 두 갈래가 되기 때문이다.
2. **이름은 기능으로.** 디렉터리·Go 패키지(`reviewpack`)·예시(`examples/review`)·작업 사본 파일(`review.*`)·schema `$defs`·코드 식별자에서 팩 세대를 나타내던 표시를 뺐다. 지침 본문은 공통 지침 제목과 역할 지침 머리말의 팩 이름만 바꿨고 다른 줄은 바이트 단위로 같다. 그래서 역할별 digest와 요청의 지침 해시는 바뀌고 입력 해시는 같다(`TestGoReviewInputsUnchanged`).
3. **보고서 builder 10.** 코드 분석 절의 팩 구분(`pack`)을 지우고 호출 행의 `pack_id`·`pack_version`을 필수로 했다. 계획 여부는 역할 목록으로 읽는다. 검토 task가 없는 계획은 필수 역할이 없고 AI 검토는 `not_performed`다. 지침을 읽지 못한 검토 task의 사유는 `review_instructions_invalid`다.
4. **개정과 보류 평가.** 지침 개정은 이 지침 하나에 하고 기본 회귀 집합의 실행 한 번으로 검증한다(A33 4). holdout 등록부에서 이전 id로 기록된 실행은 같은 지침의 이전 digest 실행으로 본다. 평가 runner의 id 대조는 보류 자료의 이름 변경과 함께 바꾼다.
5. **표시 검사.** `scripts/bundle_checks/docs.py`가 추적하는 모든 텍스트 파일의 내용과 경로에서 팩 세대·작업 단계 표시를 찾으면 실패한다. 외부 계약의 버전(`/api/v1`, upstream 경로, 모듈 버전, 분석기 형식 ID, `schema_version` 값)과 대소문자를 구분하는 회귀 사례 이름은 허용 목록으로 둔다.

[모델 검토](REVIEW.md) · [평가](EVALUATION.md)

**A58 — 모듈의 파일 전체를 읽고 줄 범위로 인용하는 검토 입력.** (기록 2026-11-23) REQ-009/010/011/012/027, NFR-007/008과 TC-161, TC-167, TC-287에 대해 다음을 확정한다. A29 4·A31 5의 40,000바이트 요청 한도(소유자 승인), A39 6의 relay 모델 출력 한도, A39 8의 제한시간 180초를 바꾼다. A37 2와 A45 1의 결정적 계획(slot 수와 작업 키)과 실행 때의 너비 우선 배정은 유지한다.

1. **요청 예산 하나.** 요청 크기는 노드 운영자 설정(`--review-request-bytes`, `--review-output-tokens`, 서비스 설정 `agent.model.review_request_bytes`·`review_output_tokens`)이다. 기본은 262,144바이트·16,384토큰이고 40,000~1,048,576바이트·1,536~65,536토큰을 받는다. 코드 원문·단위·발췌 수·verify 발췌와 확장·서술·실패 과정·후보 수의 상한은 모두 `Budget.limits` 한 규칙에서 나온다([요청 크기](REVIEW.md#요청-크기)). 최소 예산의 상한은 이전 고정 요청과 같으므로(코드 12,000바이트, 발췌 8개, 단위 2,400바이트, 서술 400바이트, 후보 4개) 작은 예산도 유효한 요청을 만든다. 차이는 후보에 선택 필드 `mechanism`(최대 800바이트)이 생긴 것이다. 그래서 모든 필드를 최대 길이로 쓴 합성 최악 입력에서 최소 예산의 verify는 후보 6개 중 4개를 보내고 나머지는 `unreviewed`(`verification_budget_exceeded`)로 남는다. 기본 예산은 한 모듈의 보통 크기 파일 여러 개가 한 요청에 들어가도록 고른 값이다. 품질·비용 측정에서 나온 값이 아니다. 예산은 Hello로 워커에 가고 run 요청으로 바꿀 수 없다. 계획(slot 수와 작업 키)은 예산과 무관하다(A37 2).
2. **검토 단위.** 코드 발췌의 기본 단위는 파일 전체다. 모듈 순회 순서로 파일의 첫 단위를 먼저 담고, 남은 예산에 같은 파일의 다음 단위를 담는다. 단위보다 큰 파일은 최상위 선언 경계에서 나눈다. Go는 `go/parser`, 다른 언어는 중괄호 깊이와 Python 들여쓰기의 작은 판별기를 쓰고 외부 파서·cgo를 들이지 않는다. 첫 줄에서 시작하지 않는 단위는 파일 머리와 함께 간다. overview와 slice가 같은 단위를 쓴다. 범위 산출물 0.3의 `limits`는 그 노드의 예산에서 나온 값이고 `max_file_lines`는 `max_file_bytes`와 같다. 이 값으로 워커는 범위 산출물이 단위 계획인지 이전 50줄 창 계획인지 구분한다. 산출물 schema는 0.3을 유지하고 slot과 overview의 파일 수 상한을 8에서 64로 넓혔다. 계획 규칙의 결과가 바뀌지 않아서다. slot 배정은 overview와 같이 너비 우선이다. 모든 파일이 앞 단위를 받은 뒤에 다음 단위를 배정하고, 한 파일의 단위는 순서대로 머리와 함께 간다. 파일 하나의 단위를 이어서 배정하는 깊이 우선 안을 시험했으나 채택하지 않았다. 합성 slice 예시(`fixtures/modules/slices/input`, 최소 예산, 호출 상한 15)에서 보낸 파일이 39개에서 28개로 줄었기 때문이다. 너비 우선에서는 39개를 모두 보내고 89줄 파일은 71줄까지 보낸다(이전 50줄 창은 50줄). 이전 범위 산출물은 계속 읽는다.
3. **선택기는 위치다.** 문서 계약·호출 근거·진단 등 기존 선택기와 그 창별 한도는 그대로 두고 먼저 배정한다. 선택기 창이 같은 요청의 단위 안에 있으면 원문 없이 `within` 참조로 보낸다. 근거 ID와 기록은 바뀌지 않는다. overview의 발췌 수는 이제 8개가 아니라 예산이 정한 수(기본 64개)까지다.
4. **줄 번호와 인용.** 원문은 요청을 인코딩할 때만 줄 번호가 붙는다. 모델은 `<ID>:<시작>-<끝>`으로 인용하고, 워커는 보낸 발췌 안인지 확인한 뒤 그 범위의 결정적 근거 ID(`origin: cited`)로 바꿔 저장한다. 발견과 보고서 발췌는 좁힌 범위를 가리킨다. 80줄보다 긴 발췌는 범위 없이 인용할 수 없다. 문서 계약 대조와 계약 후속 검토의 코드 근거는 범위를 확인하되 발췌의 원래 ID를 저장한다. 대조가 계약·위치 선택과 발췌 ID로 묶여 있기 때문이다. 모든 인용은 이전처럼 스냅샷 바이트로 다시 확인한다.
5. **서술과 후보.** 서술 필드는 출력 예산에 따라 최대 1,200바이트(지침 360자), 실패 과정은 2,400바이트다. 후보 수는 보낸 코드 32 KiB마다 하나씩 늘어 출력 예산이 정한 상한(4~12)까지다. 지어낸 ID, 실행 주장, 범위 확대, 모델의 `critical`을 거부하는 규칙은 그대로다.
6. **verify 입력.** 인용된 범위를 감싸는 선언(`origin: declaration`)을 인용 바로 뒤에 보낸다. 호출자·테스트 확장은 인용된 발췌만 기준으로 찾는다. 앞 역할의 긴 단위 안에 있던 호출자가 이전처럼 빠지지 않게 하기 위해서다. 요청 순서는 인용, 선언, 확장, 인용한 파일의 긴 단위, 짧은 문맥이다.
7. **기록과 보고서.** 검토 기록 schema 0.7을 모든 역할과 slice가 쓴다. 근거 256개, 후보 16개(`_1`~`_16`), 서술 1,200바이트, `mechanism`, origin cited·header·declaration을 받는다. 기록 상한은 1 MiB다. 0.2~0.6 기록은 이전 한도로 계속 읽는다. 보고서 생성기는 20이고 서술 상한은 생성기 버전으로 정한다. 실패 과정은 발견의 추론 주장으로 보인다. 0.6 기록은 HTTP 코드와 테스트를 함께 읽었을 때만 쓰였으므로 보고서 검증기는 decoder 이름으로 그 읽기를 확인했다. 0.7은 모든 역할이 쓰므로 보고서 본문만으로는 그 읽기가 있었는지 알 수 없다. 검증기에 그 사실을 알릴 보고서 필드를 더하는 안은 보고서 schema를 바꾸므로 이번에 하지 않았고 한계로 적었다([모델 검토](REVIEW.md#한계)). 생성기는 기록의 `workflow_reading`이 있을 때만 관련 흐름을 만든다.
8. **지침.** `prompts/review` 2.7.1에서 공통 지침에 줄 범위 인용과 짧은 문단 서술, 조사 지침에 파일 사이 호출 관계와 후보의 여섯 요소, 검증 지침에 감싸는 선언과 다른 파일 테스트를 최소한으로 더했다. 역할 계약 문자열도 같은 내용으로 바꿨다. 그래서 역할 digest와 Go 입력 고정값(`TestGoReviewInputsUnchanged`)을 다시 고정했다. 가짜 upstream 회귀 실행은 통과했다. 실제 모델로 개정 품질을 검증하지는 않았다([평가](EVALUATION.md)의 절차가 필요하다).
9. **제한시간과 relay.** 호출 제한시간 기본을 600초로 올렸다. 기본 예산의 요청을 읽고 출력 한도까지 쓰는 호출이 멈췄을 때의 상한으로 정한 추정이고 측정값이 아니다. 실제 호출 시간을 보고 다시 정한다. 관리형 OpenCode의 relay 모델은 출력 한도를 출력 예산으로, 문맥 한도를 요청 바이트 + 16,384 + 출력 예산으로 선언한다(A39 6의 OpenCode 기본 출력 한도를 대신한다). 어댑터의 입력 한도는 1 MiB, 응답 보관 한도는 512 KiB다. QA 예산 relay는 요청당 입력을 요청 예산에 맞추고(기본 278,528바이트), 출력 한도를 설정으로 받으며(`--max-output-tokens`, 기본 16,384), 장부 상한을 25 USD까지 받는다. 응답 전에 최대 입력·출력으로 예약하고 보고된 사용량으로 정산하는 보수적 차감은 그대로다. 실패·사용량 미보고 호출은 예약 전체를 차감한다. relay의 최악 요율로 입력과 출력이 기본 한도에 닿는 요청은 약 3.1 USD를 예약한다. 출력 한도가 예약에 들어가므로 가짜 upstream 회귀 실행의 작은 요청(입력 약 5천 토큰)도 요청당 약 0.52 USD를 예약했다. 예산이 작은 장부에서는 `--max-output-tokens`를 낮춘다. relay 입력 한도의 16,384바이트는 OpenCode가 덧붙이는 메시지 몫이다. 가짜 upstream 회귀 실행에서 잰 몫은 요청마다 5,178~5,181바이트였다(시스템 문구 4,574바이트와 identity 파일 읽기 문구, 작업 경로 길이에 따라 조금 달라진다). 남는 여유는 약 11,200바이트다. OpenCode 1.19.5는 provider 응답 헤더와 다음 스트림 조각을 각각 300초까지만 기다리고(`headerTimeout`·`chunkTimeout`), relay는 upstream 답이 끝나야 헤더를 보낸다. 가짜 upstream이 320초 늦게 답하고 호출 제한시간이 400초일 때, 두 대기를 정하지 않은 설정은 300초에 요청을 끊고 같은 요청을 다시 보냈다. relay는 버려진 첫 답을 차감하고 두 번째 요청도 upstream에 보냈으며, 호출은 400초에 `model_timeout`으로 끝났다. CODEYE가 쓰는 provider 설정(relay, 키 파일, native)은 이제 두 대기를 호출 제한시간으로 정한다. 같은 조건에서 upstream 호출 한 번으로 327초에 답을 받았다(`TestLiveRelaySlowAnswer`). native provider에 운영자가 정한 값은 그대로 둔다. OpenCode 자체 provider(설정 없이 `opencode/…`)는 CODEYE가 provider 설정을 쓰지 않으므로 OpenCode 기본을 따른다. relay는 앞 요청을 기다리는 동안 연결을 끊은 클라이언트의 요청을 예약·전달하지 않는다. 전달한 뒤 끊긴 요청은 이미 upstream에 갔으므로 그대로 차감한다.
10. **근거 계보.** 근거 ID는 위치에 묶이므로 verify가 만든 범위(감싸는 선언, 확장 창, 다시 좁힌 인용)가 앞 단계 발췌와 같은 ID일 수 있다. 실제 Go·Java 프로젝트의 가짜 upstream 실행에서 한 함수만 있는 파일의 감싸는 선언이 조사 단계가 보낸 파일 단위와 같아 `origin: declaration`으로 기록됐고, 보고서가 verify 기록을 `review_lineage_mismatch`로 제외해 필수 검토가 수행되지 않았다. 보고서의 엄격한 규칙(같은 ID는 앞 단계 항목 그대로)은 유지하고 워커가 따르게 했다. verify는 그런 범위를 앞 단계 항목으로 보내고 기록하며, 기록을 쓰기 전에 같은 검사(`record.CarriesUpstreamEvidence`)로 확인해 어긋나면 `review_lineage_mismatch`로 실패한다. 보고서와 `validate_bundle.py`도 같은 검사를 쓴다. 보고서가 위치만 같으면 출처 표시가 달라도 받게 하는 안은 기록이 앞 단계 근거를 바꿔 적는 것을 허용하므로 채택하지 않았다. 후보가 인용하지 않은 좁힌 범위(강점의 범위 등)는 그것을 담은 발췌가 같은 요청에 남을 때만 보낸다. 담은 단위 없이 보내면 기록 검사(`checkCitedRanges`)가 verify 전체를 실패시켰기 때문이다.
11. **문서 계약 대조의 인용과 거부.** 전체 조사의 문서 계약 대조가 `contract_comparison_invalid`로 거부되면, 후보가 유효해도 응답 전체가 거부되어 필수 검토가 끝나지 않았다. 합성 파일로 재현한 원인은 다음과 같다. 대조의 코드 근거는 범위를 확인한 뒤 발췌의 원래 ID를 저장했다. 그 발췌는 이제 파일 전체 단위이므로 같은 파일의 Javadoc·docstring·rustdoc 줄을 담는다. 그래서 구현이 인용한 문서와 겹치면 안 된다는 규칙과 문서화된 API 본문 안이어야 한다는 규칙을 만족할 수 없었다. 소스 안 문서가 없는 계약에서도 같은 단위의 두 범위가 같은 ID로 겹치는 경우, 범위를 붙인 문서 ID, 80줄을 넘는 범위, 다섯 개 이상의 근거가 모두 같은 하나의 코드로 거부됐다. 다음을 정한다. (가) 대조의 코드 근거는 후보처럼 좁힌 범위를 저장하고, 두 규칙은 그 범위로 판단한다. 80줄보다 긴 발췌를 범위 없이 인용하면 거부한다. 어느 줄이 계약을 구현하는지 말하지 않기 때문이다. 역할 계약 문자열에 이 규칙과 근거 수 한도를 적는다. (나) 거부 조건마다 고정 코드를 두고, 복구 요청에 대조 번호·필드·코드를 함께 보낸다. 하나의 코드로는 모델이 무엇을 고칠지 알 수 없어 복구도 매번 실패했다. (다) 대조는 선택 항목이다. 형식 복구를 쓴 뒤에도 틀린 대조는 그 대조만 빼고 `contract_comparison_omitted`를 남긴다. 보고서는 그 계약을 미확인으로 보이고 문서 절의 분석 공백에 적는다. 후보 오류는 이전처럼 응답을 거부한다. 형식 복구 전에 바로 빼는 안은 모델이 고칠 기회를 버리므로 채택하지 않았다. 계약 후속 검토는 앞선 대조가 인용한 범위를 담은 발췌가 함께 갈 때만 보낸다. 강점은 이전부터 항목마다 뺐고, 위치 선택(`contract_probe`)도 틀리면 한계만 남긴다. verify는 후보마다 판정을 따로 받는다. interpret의 한계 항목 하나가 틀리면 응답 전체를 거부하는 동작은 그대로다. 후보 근거 6개, verify 근거 목록 8개·조건 목록 4개의 한도가 역할 계약 문자열에 없어서 지침 2.7.1에서 적었다. 이 개정의 품질은 합성 회귀 집합으로 다시 확인해야 한다. (A59에서 후보 오류와 interpret 한계 항목의 형식 오류도 항목에서 정리하도록 바꿨다.)

**A59 — 형식 오류는 복구 한 번 뒤 항목에서 정리하고, 일부만 판정한 검증도 보고서에 싣는다.** (기록 2026-11-24) REQ-010/011/012/013/027, NFR-007과 TC-288에 대해 다음을 확정한다. A58 11의 "후보 오류는 이전처럼 응답을 거부한다"와 "interpret의 한계 항목 하나가 틀리면 응답 전체를 거부하는 동작은 그대로다"를 바꾼다. 실제 모델로 돌린 검토에서 verify의 두 호출이 모두 `citation_range_missing`(범위 없는 긴 발췌 인용)을 내고 후보 하나가 격리되어 필수 verify가 `failed`(`review_output_incomplete`)로 끝났다. 보고서는 수락된 다른 판정까지 버리고 모델 발견 9건을 모두 `unreviewed`로 게시했다. 지금까지의 실제 모델 실패는 모두 같은 모양이었다. 모델이 실제로 보낸 발췌를 가리켰지만 인용 형식이나 항목 하나의 규칙이 틀렸고, 그 하나가 단계 전체를 잃게 했다.

1. **두 분류.** 모든 출력 오류 코드를 무결성과 형식으로 나눠 등록한다([출력 오류 분류](REVIEW.md#출력-오류-분류)). 무결성은 플랫폼이 받을 수 없는 내용(요청에 없던 ID·관찰, 보내지 않은 줄, 실행 주장, 넓힌 범위, 모델의 `critical`, 근거 없는 판정, 읽을 수 없는 결과)이고 처리는 이전과 같다. 형식은 실제로 보낸 발췌를 가리키는 인용의 정밀도 문제나 항목 하나의 규칙 위반이다. 등록하지 않은 코드는 무결성으로 다룬다. 문서의 표와 등록, 시험이 읽는 기록의 코드를 시험이 대조한다.
2. **복구 한 번, 그다음 항목 정리.** 형식 오류는 형식 복구를 한 번 요청한다. 복구 요청은 항목(목록과 번호, verify는 후보 ID)·필드·인용한 발췌 ID와 코드를 담고 모델 서술은 담지 않는다. 복구 뒤에도 남거나 복구를 보낼 수 없으면(호출 상한, 요청 크기, 입력, 뒤 호출의 timeout·거절·모델 오류) 플랫폼이 항목 안에서 정리하거나 그 항목만 빼고 한계를 남긴다. 단계는 유지한다. 취소는 정리하지 않는다. 복구 전에 바로 정리하는 안은 모델이 고칠 기회를 버리므로 채택하지 않았다.
3. **범위가 없거나 틀린 긴 인용.** verify 근거와 반대 근거는 같은 후보가 인용한 범위가 그 발췌 안에 있으면 그 범위(`citation_range_from_candidate`), 없으면 보낸 발췌 전체(`citation_range_unspecified`)로 정리한다. 강점, 문서 계약 대조, 계약 후속 검토는 발췌 전체로 정리하고, 대조는 대조 규칙으로 다시 검사한다. 문서·manifest 근거에 붙인 범위는 문서·환경 역할에서도 형식 오류(`citation_range_invalid`)이고, 복구 뒤에는 범위를 지우고 그 근거 ID로 받는다(`citation_range_unspecified`). 이전에는 그런 ID를 요청에 없던 ID(`evidence_id_unknown`)로 보아 단계를 잃을 수 있었다. 80줄을 넘는 범위는 앞 80줄로 자르지 않고 범위를 정하지 않은 인용으로 본다. 자르면 모델이 가리킨 줄이 잘릴 수 있지만 보낸 발췌 전체는 그 줄을 모두 담는다. 조사 후보와 해석 가설은 정리하지 않고 뺀다(`review_candidate_omitted`, `interpretation_items_omitted`). 후보의 근거는 결함의 위치인데 파일 전체는 위치를 말하지 않기 때문이다. 반대 근거를 후보의 범위로 정리하면 그 범위가 반박하는 줄을 담는다는 보장이 없다. 이 한계는 판정의 한계 코드로 밝히고 [모델 검토](REVIEW.md#한계)에 적었다.
4. **반복·초과와 항목 규칙.** 반복 ID를 빼고 앞에서부터 한도까지 남긴다(`citation_list_trimmed`, `verification_list_trimmed`). 서술·근거·요구 누락처럼 항목 하나의 규칙 위반은 그 항목을 빼고 역할별 한계(`review_candidate_omitted`, `interpretation_items_omitted`, `document_items_omitted`, `environment_notes_omitted`)를 남긴다. 항목을 빼서 environment·interpret가 비면 응답은 거부되고 그 선택 역할은 실패한다. 두 역할의 기록은 항목이 하나 이상이어야 하고, 이 규칙을 바꾸면 기록 schema가 바뀌므로 유지했다. 남은 한계로 [모델 검토](REVIEW.md#한계)에 적었다. 정리한 기록은 `review_output_degraded`를 갖고, 정리한 호출은 `accepted`이면서 `output_errors`에 원래 코드를 남긴다.
5. **보고서의 뺀 항목.** 뺀 항목은 그 절의 분석 공백이다. 모델 후보나 가설을 뺀 보고서는 조치 항목이 없어도 `no_actionable_findings`가 아니라 `inconclusive`다. 뺀 후보는 무엇이었는지 확인되지 않았기 때문이다.
6. **일부 후보만 판정한 verify.** task 상태는 정직하게 둔다. 판정하지 못한 후보가 남은 verify는 이전처럼 `failed`(`review_output_incomplete`, 예산으로 보내지 못한 후보는 `review_input_incomplete`)다. 보고서 생성기 21은 그 task의 수락 기록이 계보 검사를 통과하면 판정을 발견에 싣고, 판정하지 못한 후보만 사유와 함께 `unreviewed`로 둔다. `coverage.ai_review`에서 그 task의 결과는 `accepted_incomplete`이고 수락으로 센다. 그래서 다른 필수 역할이 모두 수락됐으면 `ai_review`는 `performed`다. 필수 task가 실패했으므로 보고서는 `partial`이고 판정은 조치 항목이 없으면 `inconclusive`다. 보고서 한계 `review_verification_incomplete`와 분석 공백 `role_accepted_incomplete`가 남는다. `ai_review`를 `partial`로 두는 안은 수락한 판정이 있는데도 검토가 일부만 수행된 것처럼 보이게 하고, 이미 보고서 상태·공백·한계가 미완을 나타내므로 채택하지 않았다. 생성기 20 이하의 보고서에 `accepted_incomplete`가 있으면 검증이 거부한다. 모듈별 장부의 검토 완료 파일 수는 미완 verify를 완료로 세지 않는다.
7. **버전.** 보고서 생성기는 21이다. 같은 사실에서 판정·발견·한계가 달라지기 때문이다. 검토 기록 schema는 0.7을 유지한다. 필드와 형식이 바뀌지 않았고, 호출 순서 검사는 `review_output_degraded`가 있는 기록에서만 정리한 호출 뒤의 실패 호출을 받는다. 이전 기록에는 이 한계가 없어 같은 규칙으로 읽힌다. 역할 계약 문자열과 복구 규칙에 긴 발췌의 유효한 인용(ID와 범위, 80줄 이하, 요청의 ID 형식을 따른 예시)과 verify 복구의 `issues`·`range_required_evidence`를 적고, 공통 지침(`00-core.md`)과 검증 지침(`verify.md`)에 같은 인용 규칙을 한 문장씩 더해 검토 지침은 2.8.0이다. 지침 개정이므로 합성 회귀 집합 실행이 필요하지만 이 변경에서는 실행하지 않았다.
8. **시험.** scripted caller와 dry run 가짜 upstream이 각 형식 오류를 두 번 내는 경우(첫 응답과 복구 응답)를 역할마다 재생한다. 가짜 upstream은 `--shape-errors RATE`로 정한 비율의 항목에 매 호출(복구 포함) 형식 오류를, `--integrity-errors RATE`로 verify 판정에 요청에 없던 근거 ID를 넣는다. 후보 하나가 격리된 verify가 다른 판정을 게시하는 보고서를 전체와 slice로 시험한다. 실제 모델로 이 개정의 효과를 측정하지 않았다. 합성 회귀 집합 실행이 필요하다([평가](EVALUATION.md)).

[모델 검토](REVIEW.md) · [안내서](GUIDE.md#모델-검토-설정)

**A61 — 모든 도구 진단을 묶어 분류하고, 도구 finding의 심각도는 분석기 팩의 표가 정한다.** (기록 2026-11-25) REQ-007/009/010/011/012/013/027, NFR-007과 TC-292~300에 대해 다음을 확정한다. [분석기](ANALYZERS.md#기본-심각도)의 "도구 finding은 모두 `low`"를 바꾼다. 해석 역할(`review.interpret`) 한 번이 관찰 8개만 다루어 나머지 진단이 조치 항목에 오르지 못했다. A37 2·A45 1의 결정적 계획(slot 수와 작업 키는 manifest·범위·상수로만 정함)과 A59의 출력 오류 처리, TC-102(도구별 finding 유지)는 유지한다.

1. **기본 심각도.** 분석기 팩마다 규칙 계열 표를 두고 규칙 계열이 `info`·`low`·`medium`·`high` 중 하나를 정한다. `critical`은 기본값이 될 수 없다. 표는 구조화 데이터(분석기 ID, envelope의 규칙 팩 ID·버전, 규칙 ID, 원시 등급)만 읽고 모델은 관여하지 않는다. 원시 등급은 관찰에 그대로 남는다. 표에 없는 규칙과 표가 없는 분석기는 `low`다. 표는 자기가 쓰인 규칙 팩 버전 목록을 갖고, envelope의 버전이 목록에 없으면 `low`로 둔다. envelope의 규칙 팩 버전은 올리지 않았다. run 비교의 규칙 문맥(TC-104)이 그 버전에 묶여 있어, 표만 바꿨는데 이전 실행과의 비교가 끊기지 않게 하기 위해서다. 쓰인 표는 보고서 도구 행의 `severity_table`로 남는다.
2. **분류 slot.** 적격 소스 파일 12개마다 한 slot(최소 2개, 최대 24개, 적격 파일이 없으면 0)인 `review.triage.tNNN`·`review.verify.tNNN` 쌍을 계획한다. 판정 필수가 아니다. 쌍은 계획의 끝, 모듈 slice와 문서 계약 후속 검토 뒤에 온다(호출 몫은 7). 진단 수는 계획 때 알 수 없으므로 slot 수는 파일 수로만 정한다. 분석기 산출물이 정해진 뒤 실행 때 묶음을 slot에 배정하고, 묶음이 배정되지 않은 slot은 모델을 부르지 않고 `no_review_scope`로 끝난다.
3. **대상과 묶음.** 대상은 수락된 분석기 산출물의 진단 중 소스 억제·구조 지표·비밀값 탐지가 아닌 것이다. 수락된 해석 후보가 이미 다룬 진단은 다시 보내지 않는다. 같은 분석기·규칙·검토 단위의 진단을 한 묶음으로 하고, 두 도구가 같은 단위의 같은 줄에서 병합 계열이 같은 규칙을 보고하면 한 묶음(`cross_tool`)으로 보낸다. 이 병합은 분류 요청에만 쓰며 도구 finding은 도구마다 남는다(TC-102). 진단 64개를 넘는 묶음은 나누고, `cross_tool`은 나뉜 조각마다 그 조각의 도구가 둘 이상일 때만 붙는다. 기록 검사와 묶음 검사가 같은 규칙을 확인한다. 묶음은 기본 심각도, 진단 수 순으로 요청 예산 안에 채우고, 한 요청에서 같은 단위는 한 번만 보낸다. 위치·단위가 없거나 비밀값 선별이 보류한 파일의 진단, slot을 다 써서 남은 묶음은 분류하지 않고 한계 코드로 남긴다. 깨끗하다고 보이지 않는다. 도구 메시지를 보낼지는 해석 역할과 같은 한 규칙이 정한다.
4. **출력.** 묶음마다 `problem`(조건·영향·고칠 방향·묶음 문맥 안의 인용), `harmless`(코드 근거와 인용), `unclear`(무엇이 더 필요한지) 중 하나다. 모델은 도구 상태·수·원시 등급·규칙·기본 심각도를 바꾸지 못하고 심각도를 정하지 않는다. 출력 오류는 A59를 따른다. 형식 오류는 남은 묶음과 그 발췌만 담은 복구 한 번 뒤 그 묶음만 빼고(`triage_item_omitted`, 첫 답의 형식 오류 뒤 복구 답에서 빠진 묶음도 같다), 무결성 오류(요청에 없는 묶음·근거 ID)는 거부한다(`triage_item_rejected`). 다른 묶음의 발췌 인용은 형식 오류이고 복구 뒤에는 자기 묶음의 인용만 남긴다. 한 출력에 같은 묶음의 답이 둘이면 앞 답이 받아들여졌어도 `triage_group_duplicate`다. JSON이 아닌 출력(출력 토큰 한도에 잘린 답이 흔한 경우)은 형식 오류이고, 재시도는 그런 답마다 남은 묶음의 앞 절반만 묻고(역할 호출 세 번 안에서) 나머지는 `triage_item_omitted`로 둔다. 모든 호출이 JSON이 아니면 그때만 task가 실패한다. 분류한 묶음이 하나도 없으면 task가 실패한다. 요청을 만들지 못했거나 취소된 slot도 호출과 보낸 묶음(모두 미분류)을 담은 실패 기록을 남긴다.
5. **발견.** 문제 묶음은 후보 `triage_N`(origin `triage`)이 되고 같은 slot의 `review.verify.tNNN`이 검증한다. 도구 진단, 모델 후보, 검증 판정은 서로 다른 기록이고 관찰 ID로 연결된다. 무해 판단은 도구 finding을 숨기지 않고 이유와 인용을 붙인다. 보고서 생성기 22는 지지된 문제 묶음을 조치 항목에 올리고, 반증된 묶음은 접힌 도구 진단의 반증 칸에 둔다.
6. **수.** 보고서의 `code_review.diagnostic_triage`는 대상 진단, 분류한 진단, 해석 후보가 다룬 진단을 보고서 자신의 관찰과, 수락되고 계보 검사를 통과한 기록의 구조화 필드에서 센다. 나머지는 "분류하지 않음"이다. 모델이 쓴 숫자는 쓰지 않는다.
7. **호출 상한과 몫.** 분류 slot은 같은 attempt 호출 상한을 쓴다. 지금까지 측정한 품질 향상은 모듈 slice가 호출 20회 안에서 모든 줄을 보낸 데서 나왔으므로 분류가 그 몫을 먼저 쓰지 않게 한다. 계획 순서는 전체 역할, `review.scope`, 모듈 slice, 문서 계약 후속 검토, 분류 쌍이다(작업 키는 같다). 받아들이는 규칙은 실행 때 정한다. 전체 역할이 남긴 호출을 L, 아직 보낼 줄이 있는 slice 수에 slice 입장 예비(2)를 곱한 값을 필요량으로 한다. 필요량이 L 이하이면 분류 몫은 0이다. slice는 줄이지 않고 분류는 slice가 쓰고 남긴 호출을 쓴다. slice가 상한을 거의 다 쓰면(남은 호출이 둘 미만) 분류는 하나도 실행되지 않는다. 분류에 최저 몫을 두면 이 경우 slice가 줄어 모든 줄을 보내지 못하므로, slice가 다 들어가는 경우에는 분류의 최저 몫을 두지 않았다. 필요량이 L을 넘으면 L 16회마다 분류 쌍 하나(호출 2)를 몫으로 떼어 두고(최소 한 쌍, 묶음이 배정된 slot 수 이하), slice와 문서 계약 후속 검토는 그 몫을 쓰지 않는다. 첫 slot은 심각도가 가장 높은 묶음을 담는다. 16은 상한 20(평가 실행)에서 한 쌍, 기본 99에서 다섯 쌍이 되게 한 값이다. 상한 20에서 slice는 쌍 하나만큼만 양보하고, 99에서도 호출 대부분이 slice에 남는다. slice가 몫을 다 쓰지 않으면 남은 호출은 분류가 쓰고, 분류가 몫을 다 쓰지 못하면 남은 호출은 그대로 남는다(slice는 이미 끝났다). 몫은 attempt마다 첫 slice 입장 때 한 번 정해 scratch에 둔다. 분류 slot은 분류와 검증에 쓸 두 호출이 남을 때 시작한다. 범위 산출물의 `calls_before_slices`는 전체 역할의 호출만 센다. 보고서 장부는 분류 slot의 호출을 slice처럼 그 기록에서 더한다. 이 저장소의 가짜 upstream 실행(상한 20)에서 바꾸기 전에는 분류 slot 6개가 호출을 먼저 써서 모듈 slice가 하나도 들어가지 못했고(새로 보낸 줄 0), 바꾼 뒤에는 slice 5개(새 줄 441)와 분류 slot 1개가 들어갔다. 상한 99에서는 slice가 30개에서 41개로, 분류한 진단이 851개에서 493개로 바뀌었다. 실제 모델의 비용·품질 변화는 측정하지 않았다.
8. **버전.** 검토 기록 schema는 0.8(분류 기록, `triage_groups`, slot 키 `tNNN`), 보고서 생성기는 22, 검토 지침은 2.9.0(분류 역할 지침 `triage.md`와 역할 계약)이다. 이전 리비전은 `diagnostic_triage` 없이 이전처럼 보인다. 지침 개정이므로 합성 회귀 집합 실행이 필요하지만 이 변경에서는 가짜 upstream의 dry run만 했다([평가](EVALUATION.md)).
9. **순위와 비교.** 발견을 줄 세우거나 자르는 목록(PR 댓글, 요약의 우선순위, 보조 경로 발견 목록)은 검증 상태를 먼저 본다. 순서는 재현, 지지, 아직 확인하지 않은 분류 문제, 그 밖의 미확인(도구 finding·모델 후보), 반증이고 심각도는 같은 상태 안에서만 본다(요약의 우선순위는 재현과 지지를 한 상태로 둔다. 보고서 검사가 저장된 이전 요약을 다시 계산하기 때문이다). 표가 도구 finding에 `high`를 줄 수 있게 되어, 심각도를 먼저 보면 미검토 도구 finding이 지지된 발견을 짧은 목록 밖으로 밀어내기 때문이다. run 비교에서 두 쪽의 분석기 행이 다른 표를 썼으면(표 이전 생성기 포함) 도구 finding의 심각도 차이는 발견의 변경이 아니고, 비교의 도구 문맥 차이 `tool:<analyzer>:severity_table` 하나로 보고한다. 위치·규칙·메시지가 같은 진단들의 원시 등급이 다르면 한 도구 finding은 그중 높은 기본 심각도를 쓴다.

**A62 — 시험·예제 등 배포 코드가 아닌 경로의 도구 진단은 기본 심각도를 낮추고 분류에서 뒤에 둔다.** (기록 2026-11-25) REQ-007/009/010/011/013, NFR-007과 TC-301~303에 대해 다음을 확정한다. A61 1의 표는 경로를 보지 않았고, 실제 프로젝트 하나를 실제 모델로 돌려 독립 판정자가 분류 결정을 소스와 대조한 결과가 그 한계를 보였다. 시험·예제 코드의 진단이 대부분이었고 기본 심각도가 배포 코드와 같았다([평가 결과](EVALUATION-RESULTS.md#분류-묶음의-독립-판정)). 검토 지침과 역할 계약은 바꾸지 않았다(유료 회귀 실행 없이 입력을 바꾸지 않는다). 결정적 규칙만 더한다.

1. **경로 규칙.** 도구 진단의 파일이 시험·예제·벤치마크·생성물·외부 코드 경로이면 계열의 표 심각도를 그 계열의 "배포 코드가 아닌 경로" 등급으로 낮춘다. `high`·`medium` 계열은 `low`, 중단 경로·성능·유지보수 계열은 `info`, `deprecated_api`·`unused_code`는 `low` 그대로(남은 값이 논리 실수를 가리킬 수 있고 사용 중단 API는 예제의 문서 낡음을 알린다), 문서·스타일·서식은 `info`다. `info`보다 낮추지 않고 표에 없는 규칙(`low`)은 경로와 상관없이 그대로다. 원시 등급은 건드리지 않는다. 낮춘 finding의 사유는 계열과 경로 규칙 버전을 담은 고정 문장이고 코드 미포함 내보내기가 보존한다. 대안으로 모두 `info`로 내리는 안은 시험 코드의 동시성·널 역참조 같은 진짜 문제를 지나치게 숨겨 채택하지 않았다. 경로가 시험이어도 도구 진단을 지우거나 접지 않는다.
2. **경로의 종류는 하나의 공용 규칙이 정한다.** `record.PathClass`가 시험 경로 규칙(`IsTestPath`)과 기본 보조 경로 규칙(`DefaultSecondary`)에 벤치마크 디렉터리(`benchmark`·`benchmarks`·`benches`)를 더해 파일 경로만으로 정한다. 보고서 생성기는 노드 설정이나 파일 머리의 생성 표지를 보지 못하므로 쓰지 않는다. 그래서 도구 finding의 심각도와 분류 계획의 순서가 같은 값을 읽는다. 벤치마크 구간을 `DefaultSecondary`에 넣지 않은 것은 검토 우선순위와 범위 산출물(모델 입력의 순서)이 바뀌기 때문이다. Go의 벤치마크는 `_test.go`이므로 시험 규칙이 이미 덮는다.
3. **기록과 비교.** 도구 행의 `severity_table`을 `<규칙 팩>/<표 버전>+<경로 규칙>`(`paths.1`)으로 적는다. 경로 규칙을 바꾸면 버전을 올리고, run 비교는 이 값이 다르면 A61 9와 같이 심각도 차이를 발견의 변경으로 세지 않고 도구 문맥 차이로 한 번 보고한다(표를 올리는 방식을 따르지 않고 규칙 버전을 한곳에 둔 것은 모든 분석기 표를 건드리지 않기 위해서다). 관찰은 `path_class`로 파일의 종류를 싣고 접힘의 경로별 수와 종합의 `diagnostics_not_shipped`가 이를 읽는다. 보고서 생성기를 23으로 올렸다. 같은 입력에서 도구 finding의 심각도와 관찰이 달라지기 때문이다. 이전 리비전은 그대로 읽고 그린다.
4. **분류 순서.** 묶음은 (낮춘) 심각도가 높은 순, 같은 심각도에서는 배포 코드의 묶음이 먼저다. 계획의 slot 수와 작업 키, 묶음을 담는 한도는 그대로이고 요청의 `default_severity`도 표 값 그대로다. 담지 못한 묶음은 이전처럼 분류하지 않음으로 남는다. 분류 몫은 그대로이므로 몫이 작을 때(호출 상한 20에서 한 쌍) 그 몫이 배포 코드의 진단에 먼저 쓰인다.
5. **한계.** 파일 경로 규칙은 배포 코드와 시험 코드를 완전히 가르지 않는다. 경로 규칙이 시험으로 보지 않는 시험 보조 코드(예: 규칙이 없는 이름의 시험 도구 디렉터리)는 배포 코드로 남고, 배포 코드가 `vendor`나 `gen`이라는 이름의 디렉터리에 있으면 낮아진다. 시험 코드의 진짜 결함이 `low`로 보이는 것은 이 규칙의 비용이다(진단은 접히지 않고 분류 몫의 뒤에서 처리된다). 측정은 사례 하나이고 모델이 `unclear`로 남긴 시험 코드 진단을 모델이 판정하게 하는 일은 지침을 고쳐야 하며 유료 회귀 실행 뒤로 미뤘다([로드맵](ROADMAP.md#분석-품질-개선)).

[분석기](ANALYZERS.md#기본-심각도) · [검토](REVIEW.md#도구-진단-분류) · [보고서](REPORT.md#도구-진단-분류)

**A64 — 독립 검토 쌍을 함께 실행하되, 호출 상한의 판단은 계획 순서로 정한다.** (기록 2026-11-25) REQ-012/013/018/022, NFR-008/009/010과 TC-312~314에 대해 다음을 확정한다. A37 4의 계수 파일과 승인 문턱, A45의 분류 몫, A61의 계획 순서(slice 쌍, `review.contract`, 분류 쌍)는 유지한다. slice가 많은 run은 모델 응답을 하나씩 기다리느라 대부분의 시간을 썼다.

1. **함께 실행하는 범위.** 전체 역할 다섯 개와 `review.scope`는 뒤 작업의 입력을 만들기 때문에 차례대로 실행한다. 그 뒤의 slice 쌍, `review.contract`, 분류 쌍은 서로의 출력을 읽지 않는다. 예외인 `review.contract`(slice 기록을 읽음)는 앞의 단위가 모두 끝난 뒤 시작한다. 쌍 안의 verify는 investigate의 후보를 읽으므로 차례를 지킨다. 요청 생성기, 역할 계약 문자열, 지침 팩은 바꾸지 않았다. 따라서 모델 입력은 같다.
2. **결정 규칙.** 판단마다 계획 순서에서 앞선 단위의 호출과 자기 단위의 호출만 센다. 끝나지 않은 앞 단위에는 task 상한(쌍 6, `review.contract` 2)까지의 범위를 둔다. 범위 전체가 문턱의 한쪽에 있으면 바로 정하고, 걸쳐 있으면 기다린다. task 상한은 역할의 반복 상한과 같은 상수(`record.TaskMaxCalls`)에서 나온다. 계수는 이를 넘는 호출을 거절한다(`review_call_cap_exceeded`). 그래서 역할이 호출을 더 하도록 바뀌어도 규칙의 가정이 조용히 깨지지 않는다. 이 규칙은 앞 단위의 최종 호출 수로 내린 판단과 언제나 같다. 그래서 계획 순서에 대한 귀납으로 요청·응답·상태·보고서가 순차 실행과 같고, 상한도 넘지 않는다. 가장 앞의 끝나지 않은 단위는 바로 정하므로 교착이 없다. 다음 안은 채택하지 않았다.
   - 실제로 쓴 호출만 세는 안: 완료 순서에 따라 승인되는 쌍이 달라진다.
   - 쌍마다 최악의 6회를 미리 떼어 두는 안: 순차 실행이 승인했을 쌍을 거절해 상한을 남긴다.
   - 쌍마다 고정 몫을 나눠 주는 안: 형식 복구가 몫 안에서 잘려 결과가 순차 실행과 달라진다.
   
   상한 근처에서 뒤 단위가 기다리는 것이 이 규칙의 비용이다.
3. **공유 상태.** 결정 규칙의 상태(단위 목록, 끝난 단위, 대기 신호)는 attempt마다 하나이고 그 attempt의 task context로만 전한다. 계수 파일을 읽고 쓰는 일과 판단은 한 잠금 아래에서 한다. 분류 몫을 정하는 첫 입장도 잠금으로 한 번만 일어난다. 작업 사본 복원도 잠금으로 직렬화한다. 전역·프로세스 간 상태는 더하지 않았다. 서버에서는 한 attempt의 task 상태 변경이 동시에 들어온다. 그러나 모든 쓰기는 project → run → attempt → task 행 순서로 잠근다(상태 변경, 진행, 취소, 완료의 task 닫기, claim, 복구). 그래서 이 변경들은 run 행에서 차례로 처리되고 교착이 생기지 않는다. 잠금 순서는 바꾸지 않았다.
4. **병렬도.** 노드 운영자가 `--review-parallel`(서비스 설정 `agent.model.review_parallel`)로 정한다. 기본은 4이고 범위는 1~16이다. 병렬도를 싣지 않은 hello는 1로 읽어 이전 worker와 같게 동작한다. 기본 4는 공급자 동시 요청(worker 수 × 4)과 OpenCode 프로세스 메모리를 작게 유지하는 값이다. 이 저장소를 가짜 upstream(답마다 5초)으로 검토한 결과, 상한 99에서 검토 쌍 구간이 752초에서 264초로 줄었다. 상한 20에서는 상한 근처의 대기 때문에 123초에서 76초로만 줄었다([측정](REVIEW.md#호출-상한과-승인)). OpenCode 프로세스 하나는 약 0.5 GiB를 썼고, attempt 메모리 예약은 고정값 그대로다. 이보다 큰 기본값은 공급자 사용량 제한에 걸릴 가능성만 높인다.
5. **relay와 유료 평가.** 예산 relay는 요청을 하나씩 처리한다(semaphore 1). relay 대기열에서 기다린 시간이 모델 제한시간을 쓰므로, 모델이 relay인 attempt는 1로 실행한다. 에이전트 플래그(hello)의 relay와, 서비스가 attempt마다 설정 파일에서 고르는 relay 모두 해당한다. 서비스의 에이전트는 모델 플래그 없이 시작하므로 hello만 보면 이 경우를 놓친다. 유료 평가 실행은 이 경로이므로 직렬이다. relay가 함께 처리할 때 장부의 상한과 사례별 귀속을 지키는지는 [다음 작업](ROADMAP.md)에 남겼다.
6. **429와 한계.** HTTP 429는 지금처럼 `model_rate_limited`로 해당 task를 실패시키며 CODEYE가 다시 보내지 않는다. 그래서 재시도 폭주는 없다. 대신 병렬도를 높이면 사용량 제한으로 실패하는 task가 늘 수 있다. 그런 실패는 실행 환경의 조건이므로 순차 실행과 결과가 다를 수 있다. 안내서는 병렬도를 낮추라고 적는다. OpenCode·AI SDK 내부의 재시도와 실제 공급자의 동시 요청 한도는 확인하지 않았다. 비용은 병렬도와 관계없이 같고, 쓰는 속도만 빨라진다.

[검토](REVIEW.md#호출-상한과-승인) · [실행](EXECUTION.md) · [안내서](GUIDE.md)

**A76 — 모델의 소스 탐색은 OpenCode 파일 도구가 아니라 플랫폼이 해석하는 단서 후속 조사로 한다.** (기록 2026-11-26) REQ-009/010/011/012/013/027, NFR-007/008과 TC-335~339에 대해 다음을 확정한다. [로드맵](ROADMAP.md#현재-빌드가-놓치는-결함의-발견)의 후보 "모델의 읽기 전용 소스 탐색"을 구현한다. 이 결정은 비용과 계약에 관한 것이다. 탐색이 결함 발견을 늘린다는 품질 주장이 아니며, 효과는 유료 회귀 실행과 봉인 분할에서 잰다.

1. **모양.** 조사 역할은 후보와 함께 단서를 최대 셋 남길 수 있다. 단서는 보낸 코드의 줄과 식별자 하나에서 시작해 필요한 것(`definition`·`callers`)을 말하고, 그 조사의 후보 하나를 반드시 가리킨다. 단서는 후보가 기대는 확인하지 않은 전제를 이어 읽는 수단이고 그 자체로 발견이 아니므로, 후보를 가리키지 않는 단서는 빠진다(`review_lead_omitted`). 플랫폼이 조사 task 끝에서 기존 소스 해석기로 스냅샷에서 찾고, 조사마다 계획된 후속 조사 한 번이 원래 후보·단서·찾은 발췌를 보내 그 조사의 최종 후보를 받는다. 후속 조사는 후보를 고치거나(`refined`) 반박하고(`contested`), 단서마다 다른 문제의 후보 하나를 더할 수 있으며(`added`) 후보를 지우지 않는다. 같은 조사의 verify가 그 후보를 검증한다. 찾은 발췌는 실행 범위, PR 변경 경계, manifest 크기·SHA-256, 생성 파일 제외, 비밀값 선별, 요청별 한도를 거친다. 모호하거나 찾지 못한 단서는 이유 코드를 남기고 짐작하지 않으며, 모델이 이미 가진 코드는 기존 근거 ID로 가리키고 다시 보내지 않는다. [검토](REVIEW.md#단서-후속-조사)
2. **비용.** 예산 relay는 요청마다 최악의 비율로 예약한다(입력 한도 전체와 출력 한도 전체). 도구 반복은 읽기 k번에 요청 k+1번이 들고, k를 미리 알 수 없어 예약과 호출 상한의 계획이 그 상한에 묶인다. 단서 후속 조사는 단서를 남긴 조사 task마다 보통 요청 1번(형식 복구나 재시도까지 2번)이고 계획 때 task로 정해져 상한의 승인 순서에 자리가 있다.
3. **계약.** 도구 반복은 지금의 계약 다섯 가지를 깬다. 한 메시지를 모아 받는 계약(도구 호출은 `policy_blocked`로 막는다), 요청 digest(요청 하나가 호출 하나), 플랫폼이 발급하는 인용 ID(모델이 연 파일의 줄에는 플랫폼의 근거 ID가 없다), 노출 기록(무엇을 보냈는지 요청 전에 정해져야 한다), 비밀값 선별(모델이 고른 경로를 열기 전에 선별해야 한다)이다. 단서 후속 조사는 요청을 플랫폼이 만들므로 다섯 가지가 그대로다.
4. **OpenCode의 단계 상한.** OpenCode 1.19.6 설정에는 agent의 `steps` 필드가 있다. CODEYE가 쓰는 `run --pure`와 지금의 도구 정책에서 이 값이 도구 단계를 정확히 막는지는 시험하지 않았다. 도구 반복은 만들지 않았으므로 이 결정에 쓰지 않았다.
5. **지침.** 지침 2.11.0이 후속 조사 역할(`leads.md`)과, 단서를 받는 조사 요청에만 붙는 단서 계약 문장을 더한다. 조사 지침(`investigate.md`)은 그대로이므로 단서를 받지 않는 조사 요청의 지침 문장은 단서 이전과 같다. 역할 pack digest는 `pack.yaml`의 판이 바뀌어 모두 바뀐다. 조사의 다른 의무는 그대로다. 합성 회귀 집합의 유료 실행 한 번으로 회귀를 확인해야 하며 이 변경에서는 가짜 upstream의 dry run만 했다([평가](EVALUATION.md#회귀-집합-실행)).
6. **호출 대상만 찾는다.** 정의 단서는 인용한 줄에서 실제로 부르는 대상에만 묶는다. Go는 package 이름으로 부른 호출을 그 import package 안에서만 찾고, 그 밖의 호출은 같은 package 소스의 타입 검사(import는 읽지 않는다)로 정해지는 함수와, 수신자의 구체 타입이 정해지는 메서드를 찾는다. 표준 라이브러리, 외부 모듈, 인터페이스와 함수 값을 통한 호출, 타입을 정할 수 없는 값은 찾지 않고(`lead_definition_not_found`), 같은 함수의 선언이 빌드 변형마다 있으면 고르지 않는다(`lead_ambiguous`). 필드 대입은 호출이 아니다. Python·JavaScript는 호출 이름이나 그 import 이름이 식별자일 때만 잇고, 같은 줄의 인자 이름으로는 잇지 않는다. 이름만 같은 다른 선언을 보내면 모델이 틀린 코드로 후보를 고치므로 찾지 못한 것으로 남긴다. 단서 계약 문장이 찾을 수 있는 것과 없는 것을 모델에 말한다.
7. **거두지 않고 반박한다.** 후속 조사가 후보를 지우는 처리는 두지 않는다. 모델 판단 하나로 후보가 검증 없이 사라지면 LLM 서술이 결과를 정하기 때문이다. 반박한 후보는 그대로 verify로 가고, 반박 이유와 인용한 줄이 반대 근거로 함께 실린다(그 판단 문장은 반박이 있는 verify 요청에만 붙는다). verify가 반박을 받아들이면 보고서에 다른 반박된 후보처럼 실린다. 반박은 선언 전체인 찾은 발췌의 줄을 인용해야 하며, 부분 창이나 조사 요청의 줄만으로는 할 수 없다(`lead_contest_evidence_not_resolved`).
8. **부분 창.** 선언 전체가 한 창(60줄·2,400바이트)에 들어가지 않거나 조사 요청의 발췌와 겹치는 정의는 선언 일부를 보내고, 그 창을 요청(`partial_evidence_ids`), 기록(`partial`), 후속 조사 지침의 입력 설명에서 부분 창으로 표시한다. 보이지 않는 줄이 조건을 바꿀 수 있기 때문이다. Python·JavaScript 선언의 끝은 구문 분석이 아니라 토큰으로 정한다(한계).
9. **생성 파일과 scan.** 호출부 단서도 생성 파일의 본문은 읽지 않는다. 호출부 단서의 확장 읽기는 그 단서의 scan 한도(파일 80개·8MiB)를 함께 쓴다.

[검토](REVIEW.md#단서-후속-조사) · [보고서](REPORT.md#단서-후속-조사)

**A77 — 단서 후속 조사는 뒤 작업 전부의 최대 호출이 남을 때만 실행하고, 실행되지 않은 것만으로 단계를 낮추지 않으며, 찾은 선언은 한 창이면 전체를 보낸다.** (기록 2026-11-26) REQ-009/012/013/018/027, NFR-007과 TC-335·336에 대해 다음을 확정한다. A64의 결정 규칙, A37 4의 계수 파일과 A61 7의 분류 몫은 유지한다.

1. **승인.** 후속 조사는 자기 두 호출 뒤에도 계획 순서에서 뒤에 오는 일 전부의 최대 호출이 남을 때만 입장한다. 남겨 두는 것은 자기 verify의 3회, 전체 조사에 계약 probe가 있으면 `review.contract`의 2회, 줄이 계획된 뒤 slot마다 조사·검증 3회씩, 진단 분류 칸마다 분류·검증 3회씩이다. 입장하지 못한 후속 조사는 호출 없이 `skipped_policy`(`leads_not_admitted`)이고, 이을 slot 조사가 상한으로 실행되지 않은 후속 조사는 `leads_slot_not_run`이다. slot마다 입장 예비 2회만 남기면 뒤 작업이 복구나 재시도를 쓸 때 후속 조사가 그 호출을 가져가 다른 task의 결과가 단서 없는 실행과 달라진다(package 12개의 호출 사슬, 상한 17·18, 모든 verify의 첫 답이 `model_output_invalid`). 최대 호출을 남기므로 기존 검증·계약 후속 검토·slice·분류는 어떤 답이 와도 호출을 잃지 않는다. 계획된 줄을 덮는 일이 더 깊은 읽기보다 먼저다.
2. **전체 조사의 slot 수.** 전체 조사의 후속 조사는 `review.scope`보다 먼저 실행되므로 줄이 계획될 slot을 `review.scope`와 같은 계산(`scopeDoc`)으로 미리 구한다. 입력은 스냅샷, 수락된 조사 기록, 설정한 상한, 분석기 결과이고 후속 조사 기록은 입력이 아니다. 계산하지 못하면 계획된 slot마다 조사·검증의 최대 호출을 남긴다. 계획 시점의 slot 수(최대 96·999)를 늘 남기는 안은 거의 모든 프로젝트에서 전체 조사의 후속 조사를 막아 기능이 실행되지 않으므로 택하지 않았다. 이 후속 조사는 또 전체 역할 상한 15회 안에서 verify의 3회를 남긴다. `calls_before_slices`와 상한의 최솟값은 바뀌지 않는다.
3. **상한 안의 측정.** scripted caller에서 기본 상한 99는 줄이 계획된 slot 11개까지 후속 조사를 실행했고 23개부터는 하나도 실행하지 않았다. 상한 20에서는 slot 5개에서도 실행하지 않았다. 모든 경우에 slice 승인, 다른 task의 상태와 단계 상태는 단서가 없을 때와 같았다([측정](REVIEW.md#후속-조사-승인-측정)). 이 예약은 후속 조사를 작은 프로젝트에서만 실행한다. 큰 프로젝트에서 후속 조사를 쓰려면 상한을 줄이 계획된 slot의 조사·검증 최대 호출이 모두 들어갈 만큼 올린다.
4. **단계 상태.** 조사 요청이 단서를 받지 않았거나(`leads_not_offered`), 받았지만 조사가 남기지 않았거나(`no_leads`), 하나도 찾지 못했거나(`no_leads_resolved`), 상한이 입장을 허락하지 않았거나(`leads_not_admitted`), 이을 slot이 실행되지 않은(`leads_slot_not_run`) 후속 조사는 실행이 필요로 하지 않았거나 감당할 수 없던 선택 읽기로 세어 단계를 낮추지 않는다. 입장 거절을 낮춤으로 세면 빠짐없이 끝난 검토 단계가 단서를 받았다는 이유만으로 `partial`이 된다(package 16개의 호출 사슬, 상한 16). 단서를 받은 조사 기록은 남긴 단서가 없어도 0.9와 `lead_limit`을 가지므로 받지 않음과 남기지 않음을 기록에서 구별한다. 모델은 표준 라이브러리, 외부 모듈, 다른 package 타입의 메서드를 흔히 단서로 남기고 이것은 정적 연결로 찾을 수 없다. 이 저장소 사본의 가짜 upstream dry run에서 `no_leads_resolved`만으로 코드 분석 단계가 `partial`이 되었다. task는 `skipped_policy`와 그 사유로 남고, 찾지 못한 단서는 이유별로 기록과 보고서에 센다. 성공으로 쓰지 않는다. 상한이 거절한 후속 조사(`leads_not_admitted`)는 찾은 단서를 읽지 못한 것이라 "읽을 것이 없음"과 다르다. 그래서 단계·판정·AI 검토 상태를 낮추지 않고 역할의 분석 공백도 만들지 않지만(`domain.SkipKeepsStage`), 보고서 요약의 성공하지 않은 작업 목록에는 사유와 함께 실린다(`domain.NothingToReview`에 들지 않는다). 나머지 넷은 입력이 없던 역할처럼 그 목록에서 빠진다.
5. **선언 창.** 호출 연결은 24줄·1,500바이트를 넘는 Go 함수에 끝 반환 줄만 남긴다(계약 대조용). 단서의 창은 60줄·2,400바이트이므로, 어느 언어든 선언 전체가 한 창에 들어가고 조사 요청이 그 선언의 줄을 하나도 보내지 않았으면 선언 전체를 보낸다. 아니면 선언 일부(Go는 끝 반환을 담은 창)를 부분 창으로 보낸다(A76 8). 그 밖의 창(호출부, Go 호출자 확장, 부분 정의 창)은 조사 요청이 보낸 줄을 빼고 보낸다. 요청은 어느 줄을 앞서 보냈는지 표시하지 않으므로, 겹친 줄을 그대로 보내면 그 줄만 인용한 고침·반박은 지침을 따랐어도 새 줄 규칙(6)으로 빠진다. 규칙을 설명하는 대신 따를 필요가 없게 했다. 호출부와 Go 호출자 확장 창은 호출 줄을 하나라도 보냈으면 그 위치가 이미 요청에 있던 것이다. 앞뒤 줄의 나머지만 보내면 호출이 없는 조각(닫는 괄호 등)이 호출부로 서기 때문이다. 호출 줄을 보내지 않았으면 호출 줄이 든 쪽을 보낸다. 부분 정의 창은 나뉘면 찾은 위치의 줄이 든 쪽, 그 줄을 보냈으면 큰 쪽(같으면 앞쪽)을 보낸다. 잘라 내고 남은 쪽 가운데 코드가 없는 것(빈 줄, 괄호, 구두점만)은 어느 창이든 보내지 않는다. 보낼 쪽이 남지 않은 위치는 이미 요청에 있던 것이고, 보낼 다른 위치가 없고 한도·보류로 빠진 위치도 없으면 단서는 `lead_already_in_request`다. 그래서 찾은 발췌의 줄은 모두 조사 요청에 없던 줄이고 `new_lines`는 그 줄을 파일마다 한 번 센다. 기록 검사의 새 줄 규칙은 이 보증을 다시 확인하는 것으로 남긴다.
6. **고침의 범위.** 고친 후보(`refined`)는 새로 읽은 코드에 근거해 글(제목·조건·동작 방식·영향·개선 방향·반론)을 바꿀 수 있다. 시작한 코드, 곧 원래 후보가 인용한 근거 ID는 떠날 수 없다. 원래 인용을 모두(`lead_refinement_citations_dropped`) 그 순서대로 앞에 두어야 하고(`lead_refinement_citations_reordered`), 새 근거 하나나 둘을 그 뒤에 더한다(`lead_refinement_citations_over_cap`). 보고서 발견의 위치는 첫 인용에서 오므로 순서가 바뀌면 발견이 원래 후보의 코드를 떠난다. 순서를 바로잡아 받는 안은 모델의 답을 플랫폼이 고쳐 쓰는 것이라 택하지 않았다. 새 근거는 하나하나가 그 후보를 가리키는 단서의 선언 전체인 찾은 발췌 안에 있고, 그중 하나 이상이 조사 요청에 없던 줄(`new_lines`가 세는 줄)이어야 한다(`lead_refinement_evidence_not_resolved`). 새 근거 둘은 그 후보의 서로 다른 단서에서 올 수 있다. 계약 문장과 지침이 이를 막지 않으므로 모델이 피할 수 없는 거부가 되기 때문이다. 찾은 발췌는 조사 요청이 보낸 줄을 담지 않지만(5) 새 줄 규칙은 기록 검사의 보증으로 남기고, 다른 후보의 단서가 찾은 코드로 고치면 계보가 엉뚱한 단서를 가리킨다. 기록의 처리는 첫 새 근거를 찾은 단서(그 발췌를 가진 그 후보의 첫 단서)를 `lead`로 담는다. 이 넷을 어긴 결과는 복구 요청 없이 그 결과만 빠지고(코드는 그 호출의 출력 오류에, `lead_result_omitted`는 기록의 한계에 남는다) 원래 후보가 그대로 verify로 간다. 고침이 빠져도 원래 후보가 그대로 검증되어 잃는 판정이 없으므로 복구 호출을 쓰지 않는다. 반박도 그 후보를 가리키는 단서의 선언 전체인 찾은 발췌에서 조사 요청에 없던 줄을 하나 이상 인용해야 하고(`lead_contest_evidence_not_resolved`) 처리의 `lead`는 그 단서다. 더한 후보는 자기 단서가 찾은 발췌에서 조사 요청에 없던 줄을 인용해야 한다(`lead_addition_evidence_not_new`). 이 둘은 빠지면 판정이나 후보를 잃으므로 복구를 거친다. 이 규칙이 없으면 새 코드를 읽지 않고도 후보의 글만 약하게 바꿔 verify에 보낼 수 있다. 후보에는 심각도가 없으므로(verify가 받은 후보로 정한다) 심각도를 비교하는 규칙은 두지 않는다. 대신 기록의 `refined` 처리가 원래 후보의 글(`original`)을 담고, 기록 검사와 `record.LeadFollowupMatches`가 그 글이 조사 기록의 후보와 같은지와 인용 규칙(순서, 단서, 새 줄)을 본다. 보고서는 고친 발견에 원래 제목, 단서, 후속 조사 task를 싣고(`lead_refinement`), 화면과 내보내기가 이를 보여 무엇이 바뀌었는지 읽는 사람이 볼 수 있다. 원래 후보가 근거 여섯 개를 모두 썼어도 고칠 수 있도록 고친 후보만 근거를 8개(`record.MaxRefinedEvidence`)까지 인용한다. 더한 후보와 조사 후보의 상한은 6개 그대로다. 기록 schema는 후속 조사 기록과 verify 기록의 후보에만 8개를 받고, 기록 검사는 6개를 넘는 후보를 고친 후보로만 받는다. 보고서의 발견 근거에는 수 상한이 없다.
7. **호출부 창 뒤의 반박.** 호출부 창은 부르는 줄과 앞뒤 세 줄이라 호출하는 함수의 앞쪽 검사가 창 밖에 있을 수 있고, 반박은 그런 창 하나로도 선다. 창을 넓히면 요청마다 찾은 발췌가 커지고 `new_lines`가 늘어나므로 창은 그대로 둔다. 대신 인용한 범위마다 감싼 선언을 주는 verify의 기존 단계(`enclosingDeclarations`)가 반박이 인용한 찾은 발췌에도 적용되어, 선언이 한 단위에 들면 verify가 그 함수 전체를 읽고 판정한다. 후속 조사 지침의 입력 설명이 호출부 발췌는 호출 주변만 보인다고 말한다. 선언이 한 단위를 넘으면 verify도 창만 본다(한계).

[검토](REVIEW.md#후속-조사-계획과-호출-상한) · [구조](ARCHITECTURE.md#검토-대상이-없는-역할)

**A78 — 정의 단서는 단서 전용의 언어별 이름 찾기로 묶고, 읽지 못한 파일은 앵커 언어의 가시성 규칙이 허락할 때만 이유가 된다.** (기록 2026-11-27) REQ-009/010/011, NFR-009와 TC-340~351에 대해 다음을 확정한다. A76 6(호출 대상만 찾는다)을 Python·JavaScript·TypeScript·Rust·C·C++의 정의 단서로 넓힌다. Go·Java의 정의 단서와 모든 호출부 단서는 그대로다.

1. **단서 전용 층.** 정의 단서는 공유 호출 연결(`sourceCalls`)이 아니라 단서 전용의 언어별 이름 찾기로 묶는다. 호출 연결은 계약 대조·문서 대조·호출부 단서가 함께 쓰므로, 그 규칙을 넓히면 단서와 관계없는 요청이 바뀐다. 단서 전용 층은 호출 연결을 바꾸지 않고, 단서를 묻지 않는 가짜 upstream dry run의 요청 dump는 바꾸기 전과 같다. Rust·C·C++ 앵커를 토큰으로 읽지 못하면(토큰 한도, C++ 원시 문자열·숫자 구분자) 호출 연결이 정하고, Rust 이름 찾기가 정하지 못한 호출(멤버 호출, 다른 모듈로 가는 경로 호출)도 호출 연결의 `use` 연결이 정한다. Python·JavaScript·TypeScript 앵커를 토큰으로 읽지 못하면 묶지 않는다.
2. **이름 범위.** 같은 파일의 정의는 export 여부와 관계없이 받는다. 둘러싼 범위의 바인딩(매개변수, 지역 변수, 패턴, 중첩 함수)이 호출을 묶으면 다른 파일을 보지 않으므로, 같은 이름의 다른 범위 선언에 묶는 일이 없다. 모듈 수준 바인딩이 둘이거나 그 이름에 대입하면 실행 순서(Python)나 언어의 거부(JavaScript)가 정하므로 고르지 않는다. 이름을 읽는 곳은 막지 않는다. 읽기는 바인딩을 바꾸지 않고, 읽기를 막으면 콜백으로도 넘기는 export 함수가 정의로 서지 못한다. 이전 TypeScript 공백의 실제 원인이 이것이었다. 대입, 재선언, import만 막는다. `self`·`this`·클래스 이름을 통한 호출은 13대로 묶는다.
3. **TypeScript.** 반환 형식을 읽어 본문에 닿고, 읽지 못하는 형식(조건부 형식, 함수 형식)의 선언은 묶지 않는다. 틀린 창을 보내기보다 보내지 않는다. 상대 지정자는 적힌 파일이 없을 때 TypeScript가 출력을 소스로 옮기는 대응을 따른다(`.js`는 `.ts` 다음 `.tsx`, `.jsx`는 `.tsx`, `.mjs`는 `.mts`, `.cjs`는 `.cts`). 적힌 `.js`가 그 `.ts`와 함께 있으면 어느 쪽이 실행되는지 빌드가 정하므로 고르지 않는다. package import와 경로 별칭은 `tsconfig`·package 해석이 있어야 하므로 따라가지 않는다.
4. **Rust.** 호출이 든 모듈의 값 item이 그 이름의 `fn` 하나일 때만 묶는다. 같은 이름의 다른 값 item이나 명시적 `use`는 모호하고, glob `use`는 item에 양보한다(언어 규칙). `#[cfg]`·시험 속성 아래의 item은 빌드 설정이 정하므로 모호하다. `self.m`은 m이 부르는 메서드와 같은 방식으로 self를 받을 때만 고유 impl에 묶는다. 방식이 다르면 메서드 찾기의 auto-ref 순서에서 trait 메서드가 먼저 찾아질 수 있다.
5. **C·C++.** 앵커와 따옴표 include가 닿는 헤더를 언어의 이름 찾기로 읽는다. 시스템 include는 읽지 않고 읽기를 멈추지도 않는다(이전에는 시스템 include가 있는 파일에서 다른 파일의 정의를 찾지 않았다). 원형만 보이면 정의는 읽을 수 있는 C·C++ 소스 가운데 그 범위(C++은 서명도)의 static이 아니고 조건부가 아닌 파일 범위 정의 하나이고, 스캔이 한도에 닿았거나 읽지 못한 따옴표 include가 있으면 정하지 않는다. 그 정의와 헤더 원형은 한 binding의 두 목표다. 정의를 먼저 보내고 원형은 부분 발췌로 뒤에 둔다. 둘째 목표는 모호함을 만들지 않는다. 매크로와 조건부 선언은 전처리 결과를 모르므로 묶지 않는다. C++에서는 overload, inline 이름공간의 선언, 인자 의존 찾기가 더할 수 있는 선언(찾기가 닿지 않는 이름공간, 클래스의 friend)이 있으면 묶지 않는다. 인자 형식을 읽지 않기 때문이다. 시스템 헤더 이름공간으로 가는 인자 의존 찾기와 읽지 못한 따옴표 헤더의 매크로·overload는 볼 수 없다(한계). `.h`는 C로도 C++로도 포함될 수 있다. 헤더 앵커는 C++ 규칙(overload, 이름공간, 클래스를 아는 더 엄격한 쪽)으로 읽고, 포함된 헤더는 포함한 파일의 언어를 따른다. C 규칙으로 읽으면 C++에서 포함될 때의 overload를 놓친다.
6. **이유.** 찾지 못한 정의 단서가 읽지 못한 파일의 이유(보류, 변경 밖, 범위 밖, 생성 파일, 부차 경로)를 받는 것은 앵커 언어의 가시성 규칙이 그 파일에 호출 이름의 정의를 허락할 때뿐이다. 이전 규칙(같은 디렉터리, 그 이름을 적은 import 줄)은 내장 이름의 호출에도 옆 파일의 보류를 이유로 붙였다. 언어가 스스로 정의하는 이름은 언어마다 160개 이하의 고정 목록이고, 앵커의 import나 선언이 묶지 않으면 그런 이유를 받지 않는다. 그 이름의 프로젝트 정의는 그대로 찾는다. 새 이유 코드는 없다.
7. **견고성.** 해석기는 대상 프로젝트의 소스를 그대로 읽는다. review task의 panic은 worker가 task 실패(`handler_error`)로 바꾸지만 그 역할의 결과를 잃으므로, 해석기는 어떤 입력에도 멈추지 않아야 한다. 새 토크나이저와 언어별 찾기마다 fuzz 대상을 두고(CI는 seed만 실행한다), 모든 fixture 파일의 모든 접두를 읽는 시험이 CI에서 돈다. 둘 다 토큰의 close·parent·깊이·줄의 불변식과 찾은 위치가 파일 안에 있는지 확인한다.
8. **JavaScript·TypeScript의 `/`.** 단서 전용 토큰 읽기는 언어의 어휘 규칙대로 피연산자 뒤(이름, 숫자, 문자열·템플릿·정규식 리터럴, `]`, 조건이 아닌 `)`, 같은 줄의 TypeScript non-null `!`과 후위 `++`·`--`, `.` 뒤의 속성 이름)의 `/`를 나눗셈으로, 피연산자가 오는 곳(처음, 여는 괄호와 연산자 뒤, 식이 오는 keyword 뒤, if·while·for·with 조건의 `)` 뒤)의 `/`를 정규식 리터럴 하나의 불투명한 토큰으로 읽는다. 템플릿의 `${...}`도 코드로 읽어 그 끝을 찾는다. 정규식 리터럴은 JavaScript·TypeScript에 흔하므로, 파일 전체를 읽지 않던 이전 규칙은 그런 파일의 단서를 모두 버렸다. `}` 뒤(블록 끝인지 객체 끝인지 모른다), JSX 표지 밖의 `<` 뒤, 문자열 밖의 `\`, 줄이 끝나는 정규식이 있으면 파일을 읽지 않는다. 나눗셈을 정규식으로 잘못 읽으면 그 줄의 대입을 숨겨 틀린 binding을 만들 수 있으므로 어느 쪽인지 정하지 못하면 읽지 않는다. 식별자로 쓴 `of`·`yield`·`await` 뒤의 `/`는 정규식으로 읽는다(한계). 공유 호출 연결의 토큰은 그대로다.
9. **JSX.** JSX는 `.jsx`·`.tsx`만이 아니라 `.js`에도 흔한데, 이전 규칙은 JSX 닫는 태그(`</`)에서 파일 전체를 읽지 않아 그런 파일의 단서를 모두 버렸다. 단서 전용 토큰 읽기는 `.js`·`.jsx`·`.mjs`·`.cjs`·`.tsx`에서 피연산자가 오는 곳의 `<`를 JSX 요소로 읽는다. 표지(태그, 이름, 속성 문자열, 텍스트)는 불투명한 토큰이고, `{...}` 식은 코드로 읽어 그 안의 호출과 대입도 다른 곳처럼 묶거나 막는다. 식을 불투명하게 두면 그 안의 대입을 놓쳐 틀린 binding을 만들기 때문이다. TypeScript generic 화살표 함수와 함수 형식은 TypeScript 자신의 구분(`<T,>`, `<T extends U>`, `<T = U>`, `=>`나 `:`가 뒤따르는 `<T>(…)`)대로 형식 매개변수로 읽는다. JSX가 거부하는 표지(텍스트의 `>`·`}`)와 읽기가 따라가지 못하는 표지(요소의 형식 인자, 값이 요소인 속성, 맞지 않는 닫는 태그, 끝나지 않은 요소)가 있으면 그 파일을 읽지 않는다. `.ts`·`.mts`·`.cts`의 `<`는 형식 단언이므로 JSX로 읽지 않는다. 형식 매개변수인지 보려고 `<` 하나가 읽는 앞쪽은 2 KiB까지다. 제한이 없으면 `<T>(`의 `)`를 찾는 읽기가 `<`마다 파일 끝까지 가서, 괄호로 시작하는 표지 텍스트가 많은 1 MB 파일 하나를 읽는 데 8초가 걸렸다. 템플릿 식의 토큰도 파일의 토큰 한도에 센다. 템플릿 하나는 파일의 토큰 하나이고 그 식은 따로 읽으므로, 세지 않으면 `<`를 템플릿 안에 둔 파일은 한도에 닿지 않은 채 `<`마다 앞쪽을 읽었다(1 MB에 0.4초). `)`가 그보다 먼 `<T>(…)`는 JSX로 읽히고 그 표지가 더 읽히지 않으므로(`=>`의 `>`, 닫는 태그 없음) 파일을 읽지 않을 뿐 틀리게 나누지 않는다. 공유 호출 연결의 토큰은 그대로다.
10. **정하지 못한 binding.** 언어 규칙과 다르게 묶던 곳은 언어 규칙대로 읽고, 정하지 못하면 묶지 않는다. Python tuple·list 묶음 안의 이름(`for (k, f) in`, `(a, f) = `)은 대상이다. JavaScript·TypeScript에서 `)` 뒤에 본문이나 `=>`가 오는 괄호는 앞에 무엇이 있든(형식 매개변수, private·computed 이름, decorator) 매개변수 목록이고 조건 괄호는 아니며, `name<T>(…) {` 같은 멤버 머리는 호출이 아니다. `var`와 블록 안의 `function` 선언은 둘러싼 함수의 바인딩이라 모듈 수준 블록의 것은 모듈 바인딩을 다시 묶고(모호), 이름 있는 함수 식의 이름은 그 본문에서만 보인다. Rust 경로 호출의 첫 이름이 둘러싼 fn·impl·trait의 형식 매개변수이면 묶지 않는다. C·C++에서 `decltype`·`__typeof__`로 형식을 쓴 지역 선언은 지역 바인딩이고, 원형의 숫자 기본 인자는 선언을 막지 않는다. 본문이 식인 화살표 함수는 다음 줄이 식을 이을지 세미콜론 자동 삽입 규칙이 정하지 못하면(템플릿 등) 끝을 짐작하지 않고 그 창을 부분 발췌로 보낸다. 선언 일부를 선언 전체로 표시하면 후속 조사가 그 창만으로 후보를 반박할 수 있기 때문이다. 이름 범위는 반대로 넓게 읽는다. 넓은 범위는 묶지 않음으로 끝나지만 좁은 범위는 틀린 binding을 만든다.
11. **C·C++ 원형의 정의.** 원형의 정의는 닫힌 세계(14)가 답할 때만 정한다. 그 이름이 든 보류 소스나 토큰으로 나누지 못한 소스, 비밀 파일 정책이 담지 않은 소스가 있거나 한도를 넘으면 원형만 보낸다. 찾은 정의는 시험용 대역이고 빌드가 쓰는 정의가 모르는 소스에 있을 수 있기 때문이다. 보낼 수 없는 소스(실행 범위 밖, PR 변경 밖, 부차 경로, 검토가 코드로 읽지 않는 디렉터리)의 정의도 정의로 센다. 주요 경로의 정의와 함께 있으면 둘이므로 원형만 보낸다. 거기에만 있으면 그 정의는 보내지 않고(그 파일의 이유) 원형을 보낸다. 이전에는 보낼 수 없는 소스를 읽지 못한 소스로 세어, PR이나 경로 범위 실행에서는 변경하지 않았거나 범위 밖인 C 소스 하나, 전체 실행에서는 `vendor` 아래나 `examples`의 C 파일 하나, 어디든 보류한 C 소스 하나만 있어도 모든 헤더 원형 단서가 원형만 보냈다. 따옴표 include는 포함한 파일 옆에서만 찾는다. 경로 끝이 같은 다른 디렉터리의 헤더를 고르는 것은 빌드의 include 경로를 짐작하는 것이다. 전처리 줄만 있는 헤더는 토큰이 없어도 번역 단위의 일부로 읽는다. 이전에는 읽지 못한 include로 세어 그 매크로를 보지 못했고 원형의 정의도 찾지 않았다. 정의가 이미 요청에 있으면 발췌 여럿에 나뉘어 있어도 `lead_already_in_request`이고 원형을 대신 보내지 않는다.
12. **한 단서의 읽기.** 인용한 줄에 같은 이름의 호출이 많으면 호출마다 번역 단위와 다른 소스를 다시 읽어, 50 KB 소스 20개가 그 이름을 부르는 프로젝트에서 호출 200개의 단서 하나가 15초를 썼다. 단서마다 읽은 것(C·C++은 선언과 매크로·`using`·friend 검사와 다른 소스의 정의, Python·JavaScript·TypeScript는 import 바인딩마다 따라간 결과)을 한 번 구해 나눠 쓰고, 호출마다 범위와 서명으로 거른다. 결과는 호출마다 따로 읽을 때와 같다.
13. **메서드 호출.** `this`·`self`·클래스 이름을 통한 호출은 실행 때 그 이름에 답할 것이 클래스의 선언 하나뿐일 때만 묶는다. 하위 클래스의 재정의, 인스턴스나 클래스에 하는 쓰기, 같은 이름의 다른 선언, 클래스를 고치는 코드가 실행 때의 답을 바꾸기 때문이다. 틀린 binding은 후속 조사가 틀린 코드로 후보를 반박하게 하므로, 정하지 못하면 묶지 않는다. 재정의할 수 없는 메서드(JavaScript private, TypeScript private, Python 이름 변환, 클래스 이름으로 부른 static)는 그 클래스의 선언에 묶되 private이 아니면 실행 때의 쓰기와 클래스를 고치는 코드를 닫힌 세계에서 본다. TypeScript private은 하위 클래스의 같은 이름 선언을 타입 검사가 거부하는 데 기대므로, 그 검사가 닿지 않는 JavaScript 소스의 선언은 센다. 이름 변환 메서드는 변환한 이름(`_Class__m`)으로 어디서든 닿는다. 일반 메서드는 그 언어의 다른 어느 클래스도 그 이름의 멤버를 선언하지 않을 때만 묶는다. 상속 관계를 읽지 않으므로 관계없는 클래스의 같은 이름 메서드도 막지만, 넓게 막는 것은 묶지 않음으로 끝나고 틀리게 묶지 않는다. 클래스를 고치는 코드(이름을 계산한 `setattr`, `this[k] = `, prototype 대입 등)는 어느 클래스에 있든 막는다. 받은 옵션을 `this`·`self`에 복사하는 기반 클래스가 하위 클래스의 메서드를 가리기 때문이다. 수신 값은 그 클래스의 인스턴스여야 한다(화살표 함수만 거친 메서드의 `this`, 일반 메서드의 다시 묶지 않은 첫 매개변수 `self`). static 메서드 안의 `this`는 클래스라서 인스턴스 메서드에 묶지 않는다. 상속한 메서드는 기반 클래스를 정하는 해석(import, 혼합, 동적 기반)이 없어 묶지 않는다(한계). 클래스 decorator는 막지 않는다. 의존성의 decorator는 보이지 않고, 막으면 decorator를 쓰는 프레임워크의 클래스가 모두 빠진다. 새 이유 코드는 없다.
14. **닫힌 세계.** 다른 소스가 그 이름을 선언하거나 쓰지 않는지 보는 확인(11, 13)은 단서가 보낼 수 있는 파일이 아니라 앵커 언어의 스냅샷 소스 전부를 읽는다. 보낼 수 없는 소스(실행 범위 밖, PR 변경 밖, 부차 경로, 검토가 코드로 읽지 않는 디렉터리, 생성 파일)도 실행되거나 링크되는 코드이고, 그 원문은 보내지 않으므로 모델 입력의 경계는 그대로다. 비밀값 선별이 보류한 소스는 이름 검색에만 쓰고 토큰으로 나누거나 보내지 않으며 그 이름이 든 것만 답을 막는다. 결과에 닿는 것은 이름이 든다는 사실뿐이다. 이전에는 보류 소스 하나가 그 언어의 모든 원형 단서를 원형만 보내게 했다. 비밀 파일 정책이 담지 않은 그 언어의 소스는 내용을 모르므로 막는다. 그 정책은 소스가 아닌 파일 이름과 자격 증명 디렉터리만 가리키므로 막는 일이 드물다. 가져오지 않은 submodule, 따르지 않은 링크, 특수 파일은 닫힌 세계 밖이다. submodule은 의존성처럼 다른 저장소이고, 링크는 스냅샷 안을 가리키면 그 대상을 제 경로로 읽고 밖을 가리키면 프로젝트 밖이다. 플랫폼은 submodule을 가져오지 않으므로, 막으면 `.gitmodules`가 있는 저장소는 모든 언어에서 이 확인을 잃는다. 그 안의 정의는 보지 못한다(한계). 바이트 검색이 이름을 온전한 단어로 찾아 먼저 거른다. 토큰 읽기가 그 이름으로 읽는 토큰은 모두 그런 단어라 거르기가 답을 바꾸지 않고(fuzz 시험이 확인한다), 부분 문자열로 찾으면 짧은 이름(`rate`는 `generate`에 든다)이 거의 모든 소스를 토큰으로 나누게 한다. 한도는 단서의 스캔과 따로 확인마다 정한다. 한 언어의 소스가 64 MiB를 넘으면 읽지 않고, 한 확인이 토큰으로 나눌 소스가 200개를 넘으면 답하지 않는다(`lead_scan_limit`). task는 단서마다 확인 하나(최대 3)이므로 600개 이하다. 이 머신(WSL2)에서 JavaScript 소스 2,000개·58.6 MiB를 읽고 검증하는 데 0.17~0.2초, 여덟 단어로 찾는 데 0.07초, 그 이름이 든 30 KB 소스 199개를 토큰으로 나눠 답하는 데 0.4초가 걸렸다. 파일당 토큰 한도(16,384개)까지 찬 소스라면 그 두 배 안이다. task의 단서들은 읽은 소스와 검색과 파일마다의 답을 나눠 쓴다. 한도를 task 전체가 나눠 쓰면 단서의 답이 앞 단서가 쓴 양에 따라 달라지므로 확인마다 두고, 결과는 단서마다 따로 구한 것과 같다. 검토가 보내는 파일 한도(512 KiB)보다 큰 소스도 검색한다. 그 이름이 들면 토큰으로 나누지 못해 답을 막는다.

[검토](REVIEW.md#단서-해석)

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

## 보고서

**A55 — 실행 결과와 코드 검토를 같은 테스트 줄에서 연결한다.** REQ-009/010/011/012/013/014/027, TC-278~280.

- 워커는 실행·시도·스냅샷·작업·출력에 연결된 구조화 결과만 후속 검증에 전달한다. 모델이 읽는 값은 인용한 테스트 줄과 해당 케이스의 상태다.
- 보고서 생성기는 같은 실행의 두 출력과 결과 산출물의 참조를 대조한다. 전체 집계와 일부 케이스의 표시를 구별하고, 코드 미포함 내보내기에서는 케이스 이름을 생략한다.
- 프로젝트 전체의 테스트 통과로 개별 기능의 문서 일치나 모델 후보의 재현을 확정할 수 없다. 인용 범위에 연결되지 않은 결과는 실행기 표에서 확인한다.


**A34 — 분석 종류별 절.** (기록 2026-11-19) REQ-012/013/031, NFR-005/006과 TC-130~134에 대해 다음을 확정한다.

1. 보고서는 종합, 환경 분석, 문서 분석, 정적 분석(언어·도구별), 구조·중복 지표, 비밀정보, 코드 분석(모델 검토), 분석 공백 순의 절로 읽는다. 절은 선택 최상위 블록(`overview`, `environment`, `documents`, `static_analysis`, `metrics`, `secrets`, `code_review`, `gaps`)으로 더하고, 기존 필드의 모양과 값은 바꾸지 않는다. 블록은 관찰과 발견을 ID로만 가리킨다. 기존 필드를 절 아래로 옮기는 안은 비교·판정·평가·외부 소비자가 읽는 경로를 모두 바꾸므로 채택하지 않았다. `schema_version`은 `0.1`이다.
2. builder 버전을 5로 올린다. 절은 builder 5 보고서에만 있고 검증이 버전과 절의 유무를 함께 확인한다. 게시된 리비전은 다시 빌드하지 않으며, 세 형식과 웹은 절이 없는 리비전을 이전 평면 배치로 보여준다. 비교는 builder 4 기준과 builder 5 현재를 기존 규칙 그대로 대조한다.
3. 관찰은 정적 분석·지표·비밀정보 중 한 곳에, 발견은 정적 분석·코드 분석 중 한 곳에 정확히 한 번 들어가고, 절의 수는 `summary`와 같은 함수로 계산한다. 어느 절로 갈지는 분석기 envelope의 `analyzer.purpose`가 정한다. 분석기 ID 목록을 보고서 builder에 하드코딩하던 방식은 새 분석기마다 builder를 고쳐야 해서 바꿨다. 비밀정보 탐지의 도구 발견은 정적 분석에 두고 탐지가 그 발견을 가리킨다.
4. `ComputeSummary`, 상태·품질 판정 규칙(`DecideVerdict`)은 바꾸지 않는다. 절은 판정이 정해진 뒤 만들어지며 어떤 한계·상태·판정도 절에 의존하지 않는다.
5. 절의 단계 상태는 진행 화면의 단계 상태 어휘를 그대로 쓰고, 단계 기록이 없는 task가 있으면 `not_recorded`다. 모델 문장은 단계 연결 검사를 통과한 수락 기록에서만 오고 절 근거 목록의 ID를 인용하며 "모델 해석"으로 표시한다. 상태·수의 원천이 아니다. 표지의 실행 상태와 문서 대조 수는 구조화 값에서 계산한다. 종합의 핵심 문장은 수락된 검토 결과에서 고른다. 같은 동작의 코드 근거가 연결될 때 확인된 동작과 남은 조건을 함께 제시하고, 원문과 줄 위치는 선택해서 연다. 모델 서술이 빠진 내보내기는 해당 판정의 상태와 공개 가능한 범위만 표시한다.
6. "이전 실행과 비교"는 저장 문서의 절이 아니다. 비교는 요청 시점의 다른 리비전과 현재 판정에 따라 달라지므로 불변 문서에 넣으면 리비전 본문이 시점에 묶인다. 웹 화면만 종합 아래에 비교 결과를 보여준다.

[보고서와 화면](REPORT.md#분석-종류별-절)

**A72 — 사유·한계·단위 코드의 한국어 문구는 하나의 목록을 Go 렌더러와 웹이 함께 읽는다.** (기록 2026-11-26) REQ-012/013, NFR-006과 TC-329, TC-330에 대해 다음을 확정한다. 실제 보고서에서 `mypy_failed`·`cppcheck_failed`·`tsconfig_invalid`·`node_modules_outside_snapshot`·`java_compilation_failed`·`spotbugs_failed` 같은 분석기 사유가 Go 렌더러(Markdown·HTML)에 한국어 문구 없이 코드로 나왔고, 도구 표의 단위 수는 `key=n`으로, 도구 행의 한계는 어디에도 나오지 않았다.

1. **목록 하나.** [`internal/report/reportui/code_labels_ko.json`](../internal/report/reportui/code_labels_ko.json)을 Go가 `go:embed`하고 웹이 가져온다. 이미 `rule_descriptions_ko.json`·`overview_glossary.json`이 같은 방식이다. 이전에는 문구가 Go의 `Codes`와 `model_reason.*`, 웹의 단계 화면 사유, 보고서 사유, 모델 역할 사유 다섯 곳에 흩어져 서로 어긋났다(합집합 338개, Go와 웹이 함께 가진 145개 중 143개의 문구가 달랐다). 두 개의 맵과 어긋남을 잡는 시험을 두는 안은 같은 코드를 두 번 쓰고 말투 차이마다 시험을 고쳐야 해서 택하지 않았다. 다국어는 같은 모양의 파일 하나를 더하면 된다.
2. **코드 하나에 문구 하나.** 겹친 코드는 단계 화면의 문구(일어난 일과 뜻을 괄호로 쓴 형식)를 먼저, 그다음 웹 보고서, Go 문구를 따랐다. 단계 화면의 문구에서 뜻 표지가 빠지거나 Go 문구에 정보가 더 있던 코드(`ai_review_not_performed`, `ai_review_partial`, `request_input_sources_only`, `tool_unavailable`, `toolchain_not_configured`, `repository_code_not_allowed`, `repository_code_not_offered`, `tool_not_configured`)는 그 정보를 합쳤다. 맥락마다 뜻이 달랐던 `workspace_drift`, `snapshot_integrity_failed`, `lockfile_outdated`, `manifest_invalid`, `dependency_unavailable`는 어느 맥락에서나 맞는 중립 문구로 썼다. 문구는 한 말투(명사형 "~함·~않음 (뜻)")로 통일했다. 문장형("~습니다", "~다")이던 148개를 같은 뜻으로 고쳤고 옛 Go 문장이 암시하던 뜻 표지(범위 공백, 미검토, 참고 등)를 붙였다. 단계 화면의 문구에 있던 화면 안내("도구 메뉴에서 …", "네트워크를 확인하세요" 같은 명령형)는 내보내기에 맞지 않아 목록에서 떼어 웹 전용 힌트 맵(`web/src/locales/ko.ts`의 `stages.reasonHints`, 코드별)으로 옮겼다. 웹의 실행 화면과 보고서 화면은 목록 문구 뒤에 " — 힌트"를 붙이고 Go 내보내기는 붙이지 않는다. 설정 키(`agent.dependencies.go_proxy`, `agent.model.name` 등)는 내보내기 독자에게도 도움이 되므로 목록에 남겼다.
3. **이름공간.** `reasons`(작업 사유·한계), `model_reasons`(`result_missing`·`not_succeeded`처럼 분석기 사유와 낱말이 겹치는 역할 수준 사유, `reasons` 뒤에 찾고 두 이름공간은 겹치지 않는다), `units`(도구 행의 카운터 키: `dependency_unavailable`·`syntax_error`·`unknown`·`not_analyzed`처럼 사유와 뜻이 다른 낱말이 있으므로 따로 둔다. 단위 문구가 없으면 사유 문구를 쓴다), `upstream`(`upstream_<역할>_<상태>`는 역할이 작업 키에서 오므로 목록의 항목이 아니라 상태별 문구 틀이다).
4. **빠짐을 막는 시험.** Go 시험이 `internal/worker`·`internal/domain`·`internal/report`의 비테스트 소스를 AST로 읽어 사유·한계·카운터 키가 선언되는 자리(이름이 `Reason`·`Lim`·`Unit`·`Package`·`TU` 등으로 시작하는 상수, `Reason`·`Limitations` 필드, 한계 목록에 대한 대입·append·보조 함수 호출, 사유를 받는 호출, `Units`·`Packages`의 키)에서 코드를 모으고, 각각 문구가 있거나 이유를 적은 제외 목록에 있어야 한다. 쓰이지 않는 제외 항목도 실패한다. 보지 못하는 것은 부분으로 만든 코드(`upstream_*`는 틀로 처리), 보조 함수가 돌려주는 리터럴, 변수를 거친 값이다. 제외 목록의 항목(내부 처리기 오류, 모델 출력 오류 코드처럼 호출 기록의 `output_errors`에만 남는 코드, `PublicLimitations`가 거르는 탐색 기록, 구현만 있고 아무 분석기도 내지 않는 상수, 단위 이름 같은 명사)은 이유를 적었고, 여기에 든 코드는 보고서 독자에게 문구 없이 코드로 나올 수 있는 경우가 없다고 코드에서 확인한 것만 둔다. 이를 메우려고 저장된 예시 보고서와 관찰 문서(`examples/report*.json`, `observations-*.json`)의 한계·사유·단위도 모두 문구가 있어야 한다. 웹은 같은 파일을 가져오는지, 맵이 사본이 아닌지, 같은 저장 예시를 읽는지 시험한다.
5. **목록 밖에 남은 문구 맵.** Go 패키지 그래프의 한계(`graph.limit.*`)와 검토 장부의 줄 사유(`ledger.line_reason.*`)는 각자의 문구 맵을 쓰며 이 목록으로 옮기지 않았다. 두 맵의 코드는 해당 절에서만 나온다. 코드에 `:` 상세가 붙으면 Go와 웹 모두 문구 뒤 괄호에 붙인다.
6. **builder 버전.** 올리지 않는다. 저장 본문은 같고 Markdown·HTML은 요청 시점에 저장본에서 렌더링하므로 게시된 리비전에도 새 문구가 적용된다. 평면 배치(builder 4 이하)의 출력은 코드 문구와 도구 표의 한계 열 때문에 바이트가 달라졌고, 골든(`testdata/report-go-vet.*`)과 예시 Markdown(`examples/report.md`, `report-slices.md`)을 의도해서 다시 만들었다.

[보고서와 화면](REPORT.md#한계-코드)

## 계정과 권한

**A35 — viewer의 코드 포함 내보내기.** (기록 2026-11-19) REQ-028/031, NFR-006과 TC-144에 대해 viewer도 `include_code=true` 내보내기를 할 수 있게 둔다. 대신 모든 내보내기(형식 세 가지, 코드 포함 여부와 무관)는 본문을 보내기 전에 같은 트랜잭션에서 `report.exported` 감사 행(`format`, `include_code`, run·revision)을 남기고, 기록하지 못하면 본문을 보내지 않는다.

- 근거: 웹 보고서 화면은 역할과 무관하게 같은 보고서 본문을 보여 주고, 화면 자체가 `GET /runs/{id}/report?format=json&include_code=true`로 본문을 읽는다. 내보내기 영역의 "코드 포함" 선택도 역할로 가리지 않는다. 그래서 화면에서 보고서를 열 때마다 `report.exported`(`include_code: true`) 행이 남는다. 관리 서버에는 snapshot 파일 본문이 없어 `include_code=true`가 주는 것은 저장된 도구 메시지·규칙 문맥·검토 문구이고, viewer가 화면에서 이미 읽는 내용이다. 내보내기만 막으면 보호 효과 없이 화면과 파일의 권한이 어긋난다.
- 대안: 코드 포함 내보내기를 runner 이상으로 제한한다. 화면이 viewer에게 코드 발췌를 보이지 않게 바뀌거나 관리 서버가 snapshot 본문을 내보내게 되면 이 결정을 다시 연다.
- 감사 행은 누가 무엇을 가져갔는지 사후에 확인하는 수단이며 유출을 막지는 않는다.

**A36 — 로컬 계정, 재설정, 개인 사용자, TLS 수신.** (기록 2026-11-19) REQ-002, NFR-003/006과 TC-140~TC-157에 대해 다음을 확정한다.

1. **비밀번호 저장.** 표준 라이브러리의 PBKDF2-HMAC-SHA256(반복 600,000, salt 16바이트, 키 32바이트)을 쓰고 KDF 이름·반복 횟수·salt를 행에 함께 둔다. 외부 모듈(argon2·scrypt) 없이 감사 가능한 표준 구현을 우선했다. 메모리 하드가 아니므로 반복 횟수를 낮추지 않고, 기본값을 올리면 다음 로그인에서 다시 계산한다. 계산은 트랜잭션 밖에서 하고, 저장 직전에 hash가 그대로인지 다시 확인한다. 동시 계산은 프로세스마다 CPU 수만큼으로 제한하고 3초 안에 자리가 없으면 503이다. 자리는 Store 값에 두며 패키지 전역 변수가 아니다. controller가 여럿이면 각자 자기 CPU만 지킨다.
2. **제한.** 카운터를 DB에 둔다. 프로세스 메모리 카운터는 controller가 여럿이면 우회되고 재시작에 사라진다. 이름 키는 존재 여부와 무관하게 제출한 이름이라 잠금이 사용자 존재를 드러내지 않는다. 키는 설치별 비밀 키의 HMAC이다(잘못 친 비밀번호가 이름 칸에 들어갈 수 있다). 잠금 동안 KDF를 계산하지 않으므로 잠금은 CPU 소모 공격도 줄인다.
   - **짧은 지연.** (이름, 주소) 쌍 5회면 30초에서 두 배씩 최대 5분, 주소만으로는 500회에서 5분 지연한다. 15분 동안 실패가 없으면 0이다. 정상 사용(한 주소 뒤의 여러 사람 포함)을 막지 않고 관리자 없이 풀리는 것을 우선했다.
   - **이름 전체 잠금은 없다.** 여러 주소에서 한 이름을 추측하는 상한은 두지 않았고 비밀번호 규칙(12자 이상)에 맡긴다. 이름 전체 잠금은 남의 계정을 잠그는 수단이 된다.
   - **지연은 계산한다.** 종료 시각을 저장하지 않고 (실패 수, 마지막 실패 시각)에서 계산해 성공이 지연을 남기지 않는다. 행이 없는 키의 병렬 첫 시도는 키별 advisory lock으로 직렬화한다.
3. **재설정은 로그인 링크.** 관리자 재설정과 관리자 생성은 비밀번호를 지우고(또는 두지 않고) 일회용 로그인 링크(10분)를 준다. 사용자는 링크로 들어와 새 비밀번호를 정할 때까지 다른 경로를 쓸 수 없다. 임시 비밀번호를 쓰지 않은 이유: 관리자가 사용자의 비밀번호를 알게 되고, 임시 값이 다른 곳에 재사용되거나 오래 남으며, 링크는 이미 있는 일회용·hash 저장·짧은 수명 경로를 그대로 쓴다. 운영자 CLI의 `user passwd`는 DB 접근 권한을 가진 운영자의 직접 설정으로 남긴다. 사용자 이름이 없는 사용자는 재설정할 비밀번호가 없으므로 관리자 재설정을 409로 거절한다(이전에는 그런 사용자의 재설정이 묶이지 않은 완전한 세션을 주는 링크였다). 계정 생성·재설정 링크의 세션은 계정 표시와 별개로 세션 행에서 비밀번호 변경에 묶인다.
4. **관리자는 프로젝트를 열지 않는다. 다만 재설정으로 계정을 넘겨받을 수는 있다.** 계정 관리와 데이터 열람 권한을 분리한다. 관리자가 프로젝트를 봐야 하면 manager가 구성원으로 추가하고 그 사실이 감사 행으로 남는다. 관리자 역할 자체는 어떤 프로젝트도 열지 않지만, 비밀번호를 재설정한 관리자는 그 링크를 스스로 써서 그 계정을 넘겨받을 수 있다. 이것은 막지 않는다. 링크마다 발급자를 저장하고, 그 링크로 시작한 세션의 로그인과 모든 감사 행에 `link_issued_by`를 남기며, 사용자에게는 세션 조회의 `password_reset`(관리자·시각)으로 재설정 뒤 30일 동안 보인다. 알림은 지울 수 없다: 확인 경로를 두면 계정을 넘겨받은 관리자가 그 계정의 세션(비밀번호를 정한 뒤 새로 로그인한 평범한 세션 포함)으로 확인해 진짜 주인에게서 숨길 수 있고, "마지막 비밀번호 로그인 이후" 기준도 넘겨받은 관리자 자신의 로그인이 지운다. 자격을 가진 사람에게서 알림을 지킬 방법이 없으므로 고정 기간(30일, 상수) 동안 무조건 보이고, 권위 있는 기록은 감사 기록이다. 남는 한계: 넘겨받은 관리자가 새 비밀번호로 다시 로그인한 세션에는 `link_issued_by`가 없다(비밀번호의 출처를 세션마다 이어 붙이면 정상적인 재설정 뒤의 사용자 행이 모두 관리자 이름을 달게 되어 택하지 않았다). 마지막 활성 관리자는 같은 트랜잭션에서 활성 관리자 행 전체를 잠근 뒤 확인한다.
5. **개인 사용자는 관리자.** 개인 모드에는 사람이 한 명이고 그 사람이 운영자다. 관리자 경로(source root 허가 확인, 감사 기록)를 같은 브라우저에서 쓸 수 있게 한다. 이전 버전의 개인 사용자는 다음 start에서 관리자로 지정되고 감사 행이 남는다. 개인 사용자는 사용자 이름·비밀번호 없이 로그인 링크로 들어간다.
6. **TLS 직접 종료.** loopback이 아닌 주소는 `serve`가 인증서·키·public origin을 모두 받을 때만 연다. 앞단 reverse proxy 모드는 넣지 않았다. 신뢰할 proxy와 forwarded 헤더를 정하지 않으면 접속 주소별 제한과 Origin 검사가 거짓이 된다. TLS에서 cookie는 `__Host-` 이름과 Secure를 쓰고 HSTS를 보낸다.
7. **구성원은 정확한 사용자 이름으로도 추가한다.** 화면이 동료를 이름으로 추가해야 하므로 `POST /projects/{id}/members {username, role}`을 둔다. manager는 정확한 이름의 활성 계정 존재를 알게 되지만, 계정은 관리자가 아는 사람에게 만들어 주므로 받아들인다. 역할 확인이 조회보다 먼저이고, 검색·부분 일치는 없다. 이 경로의 시도 제한은 두지 않았다.
8. **migration 0010.** 기존 사용자는 이름·비밀번호 없는 비관리자 로그인 링크 사용자로 남는다(backfill 없음). 사용자 이름은 한 번 정하면 바꿀 수 없다(감사 기록과 제한 키의 의미를 고정).

[보안](SECURITY.md) · [API](API.md)

**A53 — 모드 대신 권한: `codeye up` 하나의 웹 서비스.** (기록 2026-11-20) REQ-001/002/024, NFR-006과 TC-256~TC-264에 대해 다음을 확정한다. A15의 "loopback 전용", A36 5·6의 "개인 사용자"와 "`codeye up`은 loopback만", A38 6의 `serve --personal`, A39 4의 "개인 사용자의 모든 로컬 source root", A40 2·7의 개인 모드 한정, A46 1·6의 "token은 `codeye up`을 실행한 사람 하나"를 바꾼다. 개인 모드·서버 모드라는 구분을 제품에서 없앤다. 이름에도 모드를 남기지 않는다. 설정 파일은 `$XDG_CONFIG_HOME/codeye/config.json`, 상태의 관리자 기록은 `admin.json`, 내부 패키지는 `instance`, controller의 세션 수명은 `serve --session-ttl`이다. 설치된 곳이 없으므로 이전 이름의 별칭이나 호환 경로는 두지 않는다. 앞선 결정에 남은 "개인 모드"는 `codeye up`·`start`가 띄운 서비스로, "서버 모드"는 운영자가 따로 띄운 controller·노드로 읽는다. 그 결정의 ID와 나머지 내용은 그대로이고 이 결정이 바꾼 부분만 대체된다.

1. **다른 머신에 서비스.** `codeye up --listen ADDR`은 TLS(`--tls-cert`·`--tls-key`·`--public-origin`) 또는 운영자가 명시한 `--plain-http`로 loopback이 아닌 주소를 듣는다. 평문 HTTP는 막지 않고 시작할 때 한 줄로 알린다(쉬운 사용을 막는 관문 대신 한계를 기록한다는 방향). controller는 수신 주소마다 Host·Origin·cookie 규칙을 따로 가진다(`ConnContext`로 고른다). 같은 머신의 에이전트와 명령은 별도의 loopback 수신 주소(`local_listen`, 기본 공개 포트 + 1)를 쓴다. TLS 주소에 붙으려면 에이전트가 사설 인증서를 믿어야 하고, 평문 공개 주소를 쓰면 노드 secret이 평문으로 흐르기 때문이다. 로그인 링크와 "이 서비스 주소"는 public origin이다.
2. **token은 사람마다, 쓰는 것은 행동한 사람의 것.** 사용자마다 `<state>/secrets/github-users/<id>.json`(0600)에 github.com token을 둔다. DB에 두지 않은 것은 백업에 비밀값을 넣지 않기 위해서다(복원 뒤 다시 넣는다). 관리자의 token은 `codeye up`이 찾은 것이고 그 사용자 파일 하나에만 둔다(A46 1의 `secrets/github.json`은 없앤다). 규칙은 하나다: **GitHub 호출은 그 호출이 대신하는 사람의 token만 쓴다.** run의 pull request 고정·Git fetch·결과 댓글은 run을 시작한 사람(예약 run은 예약의 actor), 미리보기는 요청한 사람이다. 대안이던 "프로젝트 소유자의 token"은 구성원이 고른 저장소나 구성원이 시작한 run에 소유자의 token이 쓰여 기각했다. 그 결과 구성원은 비공개 저장소를 자기 token으로 읽을 수 있어야 하고, token이 없는 사람의 run은 공개 저장소만 읽으며 댓글은 `no_token`으로 남는다.
3. **서버 폴더는 허가만.** 경로 분석은 허가된 폴더가 있는 모든 사용자에게 열린다. 관리자가 웹에서 폴더를 더하고 사용자별 또는 모두에게(`source_root_everyone`, migration 0014) 연다. source root 행은 바꿀 수 없게 되어 있어(trigger) "모두에게"를 별도 표로 두었다. 허가 판단은 SQL 술어 하나를 등록·목록·setup·자동 증감·배정 재확인이 같이 쓴다. OS 계정 권한 확인은 연결점만 둔다(서비스 계정 권한으로 읽는다는 한계를 문서에 적는다).
4. **두 스위치.** "다른 사용자도 모델 검토 사용"(기본 켬)과 "다른 사용자의 저장소 빌드 스크립트 실행 허용"(기본 끔)을 `service_settings`에 둔다. run을 시작한 사람이 활성 관리자가 아니면 배정의 `offered`가 그 범위를 노드에 알리고, 노드는 노드 상한·프로젝트 선택·`offered`를 모두 만족할 때만 실행한다. 스위치 때문에 빠진 검토는 `model_review_not_offered`·`repository_code_not_offered`로 따로 기록해 "프로젝트가 끔"과 구분한다. 기준을 소유자가 아닌 run을 시작한 사람으로 둔 것은 2와 같은 이유다.
5. **재시작 없는 웹 관리.** `codeye up`이 띄운 에이전트는 `source:controller` 능력을 알리고, 그런 노드에는 배정이 root 경로와 Git host를 실어 보낸다. 웹에서 더한 폴더·host가 에이전트 재등록 없이 쓰인다. 이 능력은 노드를 설치·등록하는 운영자 권한 안의 일이라 따로 막지 않는다.
6. **같은 첫 화면.** `GET /setup`이 사용자마다 쓸 수 있는 것과 이유(`available`)를 답하고 `mode`를 없앤다. 도구 경로는 관리자에게만 보인다. 첫 화면의 입력란은 모든 사용자에게 같고, 같은 소스의 프로젝트를 다시 여는 것은 `open_existing`으로 요청이 고른다(API의 기본은 `local_path`만 true라 이전 클라이언트의 201 동작을 바꾸지 않는다).
7. **사람이 쓰는 주소를 받는다.** 한 머신에도 주소가 여럿이다(LAN, Tailscale 같은 VPN, 호스트 이름, WSL2의 내부 주소). public origin 하나만 받으면 다른 네트워크의 사람이 403을 받아 쓸 수 없다. 그래서 loopback이 아닌 주소로 들을 때 controller는 이 머신 자신의 이름과 주소(인터페이스 주소, loopback, 호스트 이름과 search domain을 붙인 이름)를 listen 포트에서 모두 받고, `--public-origin`(여러 번, 설정 `public_origins`)은 이 머신의 것이 아닌 주소를 더하는 데만 쓴다. `0.0.0.0`의 평문 HTTP에 public origin을 요구하지 않는다. loopback으로만 들으면 다른 사람이 닿을 수 없어 넓게 받을 이유가 없으므로 이전처럼 loopback 이름(`127.0.0.1`, `localhost`, `[::1]`, 듣는 주소)과 설정한 origin만 받는다. 이 머신의 이름을 `127.0.0.1`로 풀리게 만든 웹 페이지를 막기 위해서다. Host 검사가 막을 것은 DNS rebinding이므로 이름은 정확히 일치할 때만 받고, DNS는 묻지 않는다(오프라인 설치가 자기를 알려고 질의하지 않게). 상태 변경 요청의 Origin은 그 요청의 Host와 같아야 하므로 주소마다 따로 로그인한다. 관리 화면의 로그인 링크는 관리자가 연 주소를 쓴다(loopback이면 알려 줄 주소). 받지 않는 Host의 403은 받는 주소와 더하는 명령을 말한다. 그 대신 Host를 틀린 누구에게나 이 머신의 주소 목록이 보인다. 이미 그 주소들에서 듣고 있어 접근 범위는 같고, 막는 관문보다 한계를 기록하는 방향을 따랐다. 설정 키는 `public_origin`에서 목록 `public_origins`로 바꿨다(설치된 곳이 없어 별칭을 두지 않는다). 실행 중인 controller의 인자가 설정과 다르면 `codeye up`이 controller만 다시 띄운다.

[보안](SECURITY.md#서버-경로-접근) · [GitHub](GITHUB.md#플랫폼-github-계정) · [안내서](GUIDE.md#다른-사람-초대)

**A54 — 플랫폼 계정으로 소스를 읽는다.** A53의 사용자별 GitHub token과 서버 폴더 허가를 대체하는 제품 결정이다. 웹 사용자는 이메일·비밀번호로 직접 가입한다. 정규화된 이메일은 유일하며 이메일 인증은 요구하지 않는다. 프로젝트·보고서의 소유권과 요청자 기록은 웹 사용자에게 둔다. Git 작업은 설치별 플랫폼 GitHub 계정, 파일 읽기는 서비스 프로세스의 Linux 계정, OpenCode 실행은 그 서비스 머신이 담당한다. 가정에서는 WSL과 개인 GitHub 계정으로 Tailscale 안에서 접속한다. 부서에서는 RHEL 8.10 서비스 계정이 NFS를 읽고 GHES 전용 계정으로 저장소에 접근한다. 저장소 관리자가 플랫폼 계정에 읽기를 부여한다. 후속 PR 댓글 호출은 실제 플랫폼 계정의 멘션을 사용한다.

같은 URL이나 폴더를 다른 웹 사용자가 자기 프로젝트로 분석하면 각각의 실행·보고서를 만든다. 기존 보고서는 소유자가 부여한 프로젝트 권한으로 열람한다. 가입 이메일은 사용자가 입력한 로그인 식별자이며 메일 수신 여부를 확인하지 않는다.

## 폐쇄망 배포

**A47 — 폐쇄망 배포팩과 오프라인 모드.** (기록 2026-11-20) REQ-030, NFR-001/006과 TC-30, TC-240~245에 대해 다음을 확정한다.

1. **묶음은 저장소 스크립트다.** `scripts/offline_pack.py`가 만든다. `codeye pack` 명령은 두지 않는다. 묶음은 저장소·Go·Node가 있는 연결된 머신에서만 만들 수 있고, 설치 머신의 실행 파일이 자기를 다시 묶을 일이 없다. 명령으로 두면 실행 파일에 빌드 절차와 웹 빌드 호출이 들어간다. 설치 프로그램은 두지 않는다. 풀어 놓은 디렉터리가 설치이고 `codeye`는 원래 옆의 `web/dist`를 찾는다.
2. **migration과 지침 팩은 실행 파일 안에.** 이미 embed되어 있으므로 파일로 따로 싣지 않는다. 실행 파일과 어긋날 사본을 만들지 않기 위해서다. 대신 MANIFEST의 `embedded`에 이름과 hash를 적어 무엇이 들어 있는지 밝힌다. 웹·문서·설정 예시는 파일이다.
3. **정적 링크를 검사한다.** `CGO_ENABLED=0`으로 만들고 ELF에 interpreter·dynamic section이 있으면 묶지 않는다. 그래서 실행 파일은 대상의 glibc와 무관하다(빈 chroot에서 실행을 확인). RHEL 8.10 실기 확인은 남는다. 분석 도구·PostgreSQL·OpenCode의 호환은 이것과 별개다.
4. **재현은 싼 만큼.** 항목 순서·시각·소유자·gzip 헤더를 고정해 같은 commit·같은 toolchain이면 같은 바이트가 되게 했다. Go·Node 버전과 `node_modules`까지 고정하는 빌드 환경은 만들지 않는다. 서명은 없고 sha256만 준다. 사내 서명 체계에 맞출 일이다.
5. **오프라인은 기본값만 바꾼다.** `offline: true`는 새 정책 엔진이 아니라, 운영자가 적지 않은 곳(공개 proxy·레지스트리·index·crates.io·github.com·공급자 공개 endpoint·`auth.json`·환경변수 키)을 기본값에서 빼는 스위치다. 운영자가 적은 미러·호스트·`base_url`은 그대로 쓴다. 빠진 곳은 시작하지 않은 작업(`skipped_policy`·`unsupported`)으로 보고서에 남는다(`offline_no_registry_configured`, `model_endpoint_not_configured`). 운영자 모듈 캐시만 읽은 Go 받기에서 캐시에 없는 모듈은 받기를 시도한 것이므로 `failed`이고 같은 사유이며, 그 모듈 이름이 `failed_items`에 남아 운영자가 캐시에 더할 것을 안다. Go는 운영자 모듈 캐시를 미러 없이 쓰는 길로 남기고, checksum DB를 정하지 않았으면 끈다(go.sum이 검증하고 go.sum에 없는 hash는 실패). OS 수준 차단은 하지 않는다(Native 실행, [보안](SECURITY.md#소스와-네트워크)).
6. **OpenCode의 레지스트리 조회는 loopback으로.** OpenCode 1.19.5는 플러그인을 꺼도 시작할 때 npm 레지스트리를 묻는다. 끄는 설정이 없어 `npm_config_registry`를 loopback의 닫힌 포트로 둔다. 오프라인이 아닐 때도 같다. 조회가 실패해도 검토 호출이 끝까지 진행됨을 확인했다.
7. **CA는 파일 하나, 변수 다섯.** 도구마다 CA 설정이 달라 같은 파일을 `SSL_CERT_FILE`·`NODE_EXTRA_CA_CERTS`·`GIT_SSL_CAINFO`·`PIP_CERT`·`CARGO_HTTP_CAINFO`로 준다. `SSL_CERT_FILE`이 시스템 CA를 대신하므로 합본을 요구한다. 시스템 CA를 CODEYE가 읽어 합치는 안은 OS마다 위치가 달라 채택하지 않았다. 검증을 끄는 설정은 두지 않는다. 에이전트는 시작할 때 파일에 인증서가 있는지 확인한다.
8. **도구는 싣지 않는다.** 라이선스·크기·OS별 빌드가 도구마다 다르다. `codeye tools --checklist`가 없는 도구와 옮기는 방법을 보인다.
9. **오프라인에서 GitHub은 꺼진다.** A46의 token 발견(환경변수·`gh auth token`)과 GitHub API(pull request 확인·댓글)는 공개 서비스로 가는 길이라 오프라인 모드에서는 실행하지 않는다. 저장된 token도 지운다. `git_hosts`에 github.com을 적은 경우에도 token을 보내지 않는다. 오프라인을 고른 설치가 그 사실을 모르고 github.com에 접속하지 않게, 켜는 길을 두지 않았다. 사내 Git 호스트(GHES 등)용 credential은 범위 밖이며 그 호스트는 인증 없이 읽을 수 있어야 한다. git fetch는 CA 변수와 GitHub header를 같은 프로세스 환경으로 함께 받는다(둘 다 argv·URL·작업 사본에 없음).

[운영](OPERATIONS.md#폐쇄망-배포팩) · [보안](SECURITY.md#소스와-네트워크)

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

**A63 — 검토 대상은 배포 코드이고, 판단 불가는 입력에 없는 것을 이름으로 적으며, 한 묶음은 부분마다 분류할 수 있다.** (기록 2026-11-25) REQ-009/010/011/012/013, NFR-007과 TC-307~311에 대해 다음을 확정한다. A59의 출력 오류 처리와 A61 4의 분류 출력, A62 1·2·3(경로의 종류와 `paths.1`)·4("요청의 `default_severity`도 표 값 그대로다"와 분류 순서)를 바꾼다. 실제 모델로 잰 집계에서 모델 발견의 일부가 시험·예제 코드의 것이었고(검증 호출과 후보 자리를 차지하고 보고서를 흐렸다), 분류에서 입력에 판단하는 코드가 있었는데도 판단 불가로 답한 묶음이 있었고, 문제 묶음 하나가 같은 규칙의 무해한 진단을 함께 문제로 게시했다. 지침 2.10.0은 이 개정이다. 합성 회귀 집합의 유료 실행으로 회귀를 확인해야 하며 이 변경에서는 가짜 upstream의 dry run만 했다([평가](EVALUATION.md)).

1. **경로의 종류는 요청에 구조 필드로 가고, 확실한 것만 센다.** 코드·시험 발췌를 보내는 모든 요청은 배포 코드가 아닌 경로(`record.PathClass`)의 발췌에 `path_class`를 싣는다. 이 규칙은 후보를 검증에서 빼고 모델에게 "맥락"이라고 알리므로 틀리면 배포 코드를 가린다. 그래서 A62의 규칙에서 추측을 뺐다. 소스 트리 안의 `test`·`demo`·`gen`·`build`·`fixtures`·`mocks`·`dist`·`generated` 디렉터리는 배포 코드로 두고, 언어의 시험 파일 이름과 알려진 뿌리(Java `src/test/`, `testdata`, `__tests__`, 소스 트리 밖의 `tests/`·`examples/`·`benchmarks/`·`vendor`)만 센다. A62의 도구 finding 심각도 낮춤, 접힘의 경로별 수, 분류 순서가 같은 규칙을 읽으므로 그 범위가 좁아졌다. 경로 규칙 표시를 `paths.2`로 올리고 보고서 생성기 24가 쓴다(builder 23 보고서는 `paths.1`과 이전 규칙의 `path_class`로 계속 읽는다). 생성 표지가 첫 줄에 있는 파일은 요청의 발췌에만 `generated`로 알린다(보고서는 헤더를 못 본다). 모델이 경로 이름으로 짐작하지 않게 하려는 것이다. 요청에만 있고 검토 기록에는 저장하지 않는다. 그래서 기록 schema는 그대로이고(0.7·0.8) 요청을 읽어 들인 발췌는 워커가 가진 발췌와 같다. 검토 지침은 조사(후보는 배포 코드의 결함이고 시험은 맥락·보조 근거이며 후보의 위치와 주된 근거는 배포 코드)와 검증(`path_class`가 있는 줄만으로 지지하지 않는다)에서 이를 쓴다. 시험이 덮지 않는다는 `test_gap` 관찰은 덮지 않는 배포 코드의 줄을 인용하므로 가려지지 않는다.
2. **그 경로만 인용한 조사 후보는 검증하지 않고 센다.** 인용한 코드·시험 근거가 모두 배포 코드가 아닌 경로에 있는 조사 후보(전체와 slice)를 verify가 보내지 않는다. 판정은 `unreviewed`에 한계 `candidate_not_shipped` 하나이고 후보는 검증 기록에 그대로 있다(계보 검사가 같다). 보낼 후보가 하나도 남지 않으면 호출 없이 성공한다. 보고서(생성기 24)는 이 후보를 지우지 않는다. 기존의 예제·시험 경로 항목 접힘에 제목·위치와 고정 문구로 싣고 요약에 수(`code_review.candidates_not_shipped`와 종합의 같은 이름의 수)를 적되, 조치 항목·미확정 후보(요약의 `pending_candidates`)·우선순위·PR 댓글의 짧은 목록에는 넣지 않아 판정과 status는 후보가 없는 실행과 같다. 처음에는 발견에서 빼고 수만 셌으나 모델이 낸 것을 숨기지 않는다는 규칙에 어긋나 고쳤다. 생성기 24는 이 PR 밖으로 나간 적이 없어 번호를 올리지 않고 저장 본문을 바꿨다. 채택하지 않은 안: (가) 형식 오류로 복구를 부르는 안은 호출 상한이 모듈 slice의 몫이어서 버렸다. (나) 후보를 검증 없이 `test_gap`으로 게시하는 안은 verify 판정이 범주를 정하므로 모델의 범주를 플랫폼이 만들게 된다. 그 관찰은 덮지 않는 배포 코드를 인용하면 그대로 후보다. (다) 검증하되 조치 항목에서만 빼는 안은 측정한 비용(검증 호출과 자리)을 그대로 남긴다. 결정은 기록된 근거만 읽고 보고서가 어느 경우에나 같은 규칙으로 다시 정한다(검증이 실패하거나 없어도 후보는 같은 목록에 같은 고정 문구로 나온다. 검증 기록의 표시는 결정과 같아야 하고 검증이 둘을 대조한다). 검토 범위에 배포 코드가 없으면(예제만 있는 저장소, 시험만 바꾼 PR) 그 코드가 대상이므로 아무것도 가리지 않는다. 이 조건은 스냅샷의 포함된 파일 경로·실행 범위·PR의 변경 파일만의 함수(`record.ShippedSourceIn`)로 정하고 워커, 기록 검증(`Expect.ShippedSource`), 보고서(`code_review.subject`)가 같은 함수를 쓴다. 한때 보고서가 다시 정할 수 없다는 이유로 없앴으나 시험만 바꾼 PR의 모든 후보가 검증 없이 조치 항목에서 빠지는 회귀라서 되돌렸다. 그 범위의 요청만 `scope.subject: non_shipped`와 계약 문장 하나를 더 싣고 다른 요청은 바이트가 같다. 해석 가설과 분류 문제 후보는 도구 진단에서 출발하므로 이 확인을 받지 않는다(해석 가설은 검증은 받고 보고서가 따로 가른다. [A65](DECISIONS.md)). 검증 호출이 줄어 같은 호출 상한 안에서 더 많은 모듈 slice가 들어갈 수 있다.
3. **판단 불가는 입력에 없는 것이 있을 때만이다.** `unclear` 답은 `missing`에 없는 것을 하나씩 적는다(종류 여섯과, 정의이면 식별자). 지침은 요청의 모든 발췌(다른 묶음의 것 포함)를 읽고 판정하게 하고 의존성·타입·헤더 공백도 같은 구조로 적게 해 규칙 3과 모순되지 않게 한다. `missing`은 저장하지 않는다. 틀렸거나 없으면 형식 오류이고 복구 뒤에는 이유만으로 받는다. 구조 확인은 정확한 경우에만 한다. Go 묶음에서 정의가 없다고 쓴 식별자가 같은 요청의 같은 디렉터리·같은 package의 단위에 최상위 선언으로 정확히 하나 있으면 형식 오류다. 복구는 그 발췌 ID를 가리키고 싣고, 묶음은 그 발췌를 인용할 수 있다(다른 묶음의 발췌를 인용하지 않는 규칙의 예외는 이 한 발췌뿐이다). 복구 뒤에도 같은 답이면 그 답이 모델의 답으로 남는다(`unclear`과 한계 `triage_unclear_definition_in_request`). 형식 오류가 단계를 잃게 하지 않는다는 A59의 규칙에 따라 아무것도 빼지 않고 slot도 실패하지 않는다. 확인이 보이는 선언만 보므로 틀릴 수 있어서이기도 하다. 전체 선언이 요청의 단위에 있어야 하고 빌드 조건이 붙은 파일은 색인하지 않으며 복구 요청에 발췌가 들어가지 않으면 그 발췌 없이 복구한다. 다른 언어는 선언 이름 색인을 정확히 만들 수 없어(단위 경계가 휴리스틱이다) 확인하지 않고, 메서드·한정 이름·여러 선언은 Go에서도 확인하지 않는다.
4. **시험·예제 경로의 진단도 모델이 분류한다.** 요청은 묶음의 `path_class`와 보고서가 도구 finding에 주는 `default_severity`(낮춘 값, A62 1)를 싣는다. 분류 순서는 모든 배포 코드 묶음이 먼저이고 그 안·밖에서 낮춘 심각도 순이다. 묶음을 담는 첫 적합 배치는 그대로여서 자리가 남으면 그 밖의 경로의 묶음도 앞 slot에 든다. 모델이 이 묶음을 아예 분류하지 않는 안은 측정한 17건이 모두 무해였지만 시험 코드의 진짜 문제(실패해야 할 단언을 가리는 버린 오류 등)를 일반 규칙으로 판정해야 하므로 채택하지 않았다. 지침은 이를 일반 규칙(버린 오류·쓰지 않은 값은 대개 무해하나 실패해야 할 검사를 가리면 문제)으로 쓴다.
5. **한 묶음의 일부만 분류하는 답.** 답 항목의 `positions`가 묶음 `lines`의 1부터 센 위치를 담으면 그 부분의 분류다(요청의 `diagnostics`는 진단 수라 같은 이름을 쓰지 않는다. 처음에 같은 이름을 썼다가 답이 숫자를 되풀이하면 출력 전체가 스키마 실패라는 검토 지적으로 바꿨다). 비어 있으면 묶음 전체다(이전 답이 그대로 유효). 위치는 범위 안·겹침 없음이고 부분은 `max(1, min(4, 64 ÷ 요청의 묶음 수 − 1))`개 이하다. 계약 문자열이 같은 수를 적고 나머지 위치의 저장 묶음 하나를 더해도 기록은 64개 이하다. `positions`와 `missing`은 항목마다 느슨하게 읽어 틀린 형식은 그 항목의 형식 오류(`triage_diagnostics_invalid`, `triage_missing_invalid`)이다. 복구 뒤에는 유효한 부분을 남기고 틀린 부분의 위치는 분류하지 않는다(처음에는 한 부분이 틀리면 묶음 전체를 잃었다). 검토 기록은 부분마다 묶음 하나를 저장한다(관찰·`cross_tool`·문제 후보가 그 부분의 것). 그래서 기록 형식과 schema는 그대로이고 모든 진단은 정확히 한 묶음에 속해 보고서의 진단별 계산이 바뀌지 않는다. 같은 묶음의 문제 부분 둘은 선택한 진단의 줄로 구별되는 서로 다른 지문을 가진다(지문은 줄을 보지 않아 같아지던 것을 같은 규칙·파일의 문제 후보가 둘 이상일 때만 진단의 줄로 나눈다). 계획에서 묶음을 더 잘게 나누는 안은 같은 규칙의 두 진단이 한 함수에 있는 경우를 나눌 수 없고(단위 경계는 휴리스틱이다), 진단마다 묶음을 두면 묶음당 출력 한도 가정과 한 요청의 묶음 수를 넘어 채택하지 않았다. 부분 답은 묶음당 출력 토큰 가정(1,024)을 넘길 수 있고 잘린 답은 `output_not_json` 재시도가 받는다.
6. **버전과 호환.** 검토 지침 2.10.0, 보고서 생성기 24(발견 목록과 `candidates_not_shipped`가 달라지므로)다. 검토 기록 schema는 0.7·0.8 그대로다(저장 필드가 없다). `candidate_not_shipped` 한계를 가진 검증 기록은 이전 decoder가 읽지 못할 수 있으나 같은 서비스의 워커와 보고서 생성기가 함께 나가므로 이전 기록을 새 decoder가 읽는지만 시험한다. 이전 리비전과 보고서는 그대로 읽고 그린다. 새 출력 오류 코드(`triage_missing_invalid`, `triage_unclear_input_present`, `triage_diagnostics_invalid`)는 모두 형식 오류다. 무결성 오류와 요청에 없는 근거 거부는 바뀌지 않는다.
7. **쓰지 않은 값·중복 분기 계열의 진단은 단서다.** 측정에서 "값을 대입하고 쓰지 않음" 진단이 알려진 결함 위에 있었고, 분류 답이 함수와 쓰지 않은 기호를 맞게 짚고도 틀린 분기·틀린 조건으로 영향을 서술해 결함이 보고되지 않았다. 요청이 묶음의 규칙 계열(`rule_families`, 기본 심각도 표의 계열 이름)을 싣고 지침이 `unused_code`·`logic_error` 계열에 일반 규칙을 둔다. 쓰지 않은 값이 쓰였어야 할 자리를 짝에서 찾고, 짝과 다른 곳은 결과가 갈리는 구체적 입력과 돌려주는 값 대 약속한 값을 적으며, `problem`은 그것이 있을 때만 쓴다. 이 규칙은 서술의 규칙이라 플랫폼이 내용을 검사하지 않는다. 계열 이름은 도구 규칙 ID가 아니므로 지침에 쓸 수 있다. 조사 지침에도 짝으로 설정한 변수·분기를 확인하게 하는 한 단락을 더했다. 요청 크기와 묶음 방식은 바꾸지 않았다(같은 코드를 절반 크기의 요청으로 보내도 놓친 결함을 찾지 못했다).
8. **한계.** 실제 모델이 `path_class`를 따라 시험 코드의 후보를 줄이는지, `missing`을 정확히 적는지, 부분 답을 쓰는지는 측정하지 않았다. 가짜 upstream과 scripted caller는 모양을 확인할 뿐이다. 경로 규칙의 한계는 A62 5와 같다. Go 정의 확인은 같은 디렉터리·package의 단위가 요청에 있을 때만 잡는다.

**A65 — 해석 요청은 분류와 같은 우선순위로 관찰을 고르고, 배포 코드가 아닌 경로의 진단에 대한 해석 가설은 조치 항목이 아니다.** (기록 2026-11-25) REQ-009/010/011/012/013, NFR-006/007과 TC-315~317에 대해 다음을 확정한다. A63 2의 "해석 가설은 이 확인을 받지 않는다"를 구체화하고, A62 4의 분류 순서를 해석 요청의 관찰 선택에도 쓴다. 실제 모델로 잰 집계에서 게시된 모델 발견 중 시험 파일에 관한 것이 모두 해석 역할에서 나왔다. 해석 요청이 관찰 8개를 경로 순으로 골라 시험 파일의 진단이 먼저 해석·검증되어 `supported` 가설이 되었고 배포 코드의 진단은 분류를 기다렸다. 이 변경은 지침과 요청의 필드를 건드리지 않는 결정적 변경이라 유료 회귀 실행 없이 넣었고, 실제 모델에서의 효과는 측정하지 않았다.

1. **해석 요청의 관찰 선택.** 요청이 싣는 관찰 8개를 분류 계획이 묶음을 줄 세우는 비교(`comparePriority`)로 고른다. 코드는 하나다(계획의 정렬도 같은 함수를 부른다). 배포 코드(`record.PathClass`가 빈 경로)의 관찰이 먼저이고, 같은 종류 안에서는 도구 finding의 심각도(경로 규칙으로 낮춘 값)가 높은 쪽이 먼저이며, 이 모두가 같으면 경로·줄·ID 순(이전 순서)이다. 분류는 묶음의 진단 수를 비교하지만 관찰은 하나가 진단 하나이므로 수 비교는 같다(묶음으로 키워 정렬하는 안은 묶음 단위가 파일이 아니라 검토 단위이고 위치 없는 관찰은 묶음이 없어 채택하지 않았다). 위치 없는 관찰은 배포 코드로 세지 않으므로 위치 있는 배포 코드 뒤에 오고 이전처럼 같은 종류에서는 마지막이다. 요청의 관찰 순서도 이 순서다. 줄 창 4개는 앞 관찰부터 받으므로 배포 코드의 관찰이 창을 먼저 받는다(경로 순으로 보내면 8개 안에서도 시험 파일이 창을 가져갔다). 한도(관찰 8, 창 4)와 요청의 필드는 그대로이고, 분류와의 분담도 그대로다. 분류가 건너뛰는 것은 수락된 가설이 단 관찰뿐이고(`diagnostics_interpreted`) 해석이 읽었는지는 보지 않으므로 모든 진단을 한 역할이 한 번만 다루고 하나도 빠지지 않으며, `diagnostic_triage`의 수는 같은 방식으로 맞는다(`TestInterpretAndTriageShareOneOrderAndSplit`). 이 변경은 관찰이 8개를 넘거나 시험 경로의 진단이 앞서던 요청의 내용만 바꾼다. 고정 요청을 비교하는 기존 시험은 바뀌지 않았다(여러 관찰을 싣는 고정 예시 `examples/report.json`의 관찰은 `gateway.go`·`svc/a/normalize.go`·`svc/b/charge.go`로 모두 배포 코드이고 순서가 이미 같아, 다시 만든 예시는 생성기 번호와 digest만 다르고 호출의 요청 해시는 그대로다). 합성 회귀 집합에는 도구 진단이 없어 가짜 upstream dry run의 요청 50개는 변경 전후가 같다(`--compare-dumps`).
2. **배포 코드가 아닌 경로의 진단에 대한 해석 가설은 조치 항목이 아니다.** 보고서가 이미 하던 것: 지지된 가설은 경로 휴리스틱(`productLocation`: 기본 부차 경로 규칙과 시험 디렉터리·파일 이름)이 부차 경로라고 하면 조치 카드 대신 예제·시험 경로 접힘에 실렸다. 빠져 있던 것: (가) 그 휴리스틱은 확실한 경로 규칙(`PathClass`)과 다르다(소스 트리 밖 `benchmarks/`·`example/`, 다른 언어의 시험 파일 이름이 주요 경로로 남는다). (나) 요약의 `actionable_findings`·`priorities`(그리고 판정 `attention_required`), 개요의 미확정 후보 수, PR 댓글의 짧은 목록은 그 휴리스틱을 쓰지 않아 시험 파일의 지지된 가설이 판정을 올리고 우선순위를 차지했다(이 서술은 코드를 읽은 것이고 변경 전 제품을 돌려 본 것이 아니다. 머리글의 결정은 같은 휴리스틱을 쓰지만 확실한 경로 규칙이 아니라는 점은 같다). 결정: 가설의 관찰이 모두 배포 코드가 아닌 경로에 위치하고 인용한 코드·시험 근거도 모두 그렇고 검토 범위에 배포 코드가 있으면(`record.ShippedSourceIn`) 보고서가 그 발견에 한계 `hypothesis_not_shipped`를 붙인다(`record.Candidate.HypothesisNotShipped`, 보고서 생성기 25). 이 발견은 모델 검증이 준 검증 상태 그대로 접힘에 실리고 그 접힘의 요약과 행이 고정 문구로 조치 항목이 아님을 밝힌다. 조치 카드, 요약의 `actionable_findings`·`pending_candidates`·`priorities`, 판정, 머리글의 결정, PR 댓글의 짧은 목록에는 들지 않는다. 코드 분석 절과 종합이 `hypotheses_not_shipped`로 센다(반증된 가설은 반증 목록에 있어 세지 않는다). 보고서는 이 결정을 기록된 근거에서 다시 정하고(검증이 성공·불완전·실패·부재 어느 경우든 같다), 검증은 마커·수·이전 생성기의 마커를 거부한다. 결정에는 가설 자신의 관찰과 인용(첫 주장의 근거)만 쓴다. 가설 자신의 관찰과 인용이 모두 비배포 경로이면 검증의 지지 근거가 배포 코드여도 조치 항목이 아니고(A63 2가 조사 후보에 쓰는 기준과 같다), 그 발견은 예제·시험 경로 목록에 검증 결과와 함께 계속 보이며, 시험 코드의 진단이 배포 코드의 결함을 가리키면 그 배포 코드를 인용하는 발견으로 드러난다고 본다. 웹 화면도 이 한계를 읽고 경로를 다시 추측하지 않으며, 이 한계가 없는 이전 리비전만 화면의 경로 규칙을 쓴다. 생성기 25 이전 리비전에서 `hypothesis_not_shipped`(24 이전 리비전에서 `candidate_not_shipped`)는 모델 문장일 수 있어, 읽을 때 그 문자열을 뺀 뒤 검증하고 처리한다(Go 판독기 `withoutLaterMarkers`와 웹 파서 `LATER_MARKERS`가 같은 표를 쓰고 웹 시험이 Go 상수와 대조한다). 검증이 더한 지지·반대 근거나 호출자 확장은 쓰지 않는다. 확장은 배포 코드일 수 있어도 가설이 무엇에 대한 것인지를 바꾸지 않는다. 채택하지 않은 안: (가) 검증에서 빼는 안은 전체 verify의 같은 호출에 이 가설이 함께 실려 호출이 줄지 않고 모델의 판정이라는 정보를 잃는다. (나) 지우는 안은 모델이 낸 것을 숨기지 않는다는 규칙에 어긋난다. (다) 카드에서만 빼고 요약 수는 두는 안은 시험 파일의 가설로 판정이 `attention_required`가 되는 문제를 그대로 둔다. (라) 새 보고서 필드를 두는 안은 schema를 키우므로 기존 한계 코드 방식(조사 후보와 같다)을 따랐다. 분류 문제 후보(`triage_N`)는 이 결정을 받지 않는다. 시험 코드의 진단에 진짜 문제(실패해야 할 단언을 가리는 버린 오류)가 있을 수 있어 분류가 이미 그 경로의 묶음을 낮은 우선순위로 모델에 묻고 지지되면 조치 항목이 되기 때문이다(A62 4).
3. **OpenCode 상태 격리.** 평가 운영자가 제품 실행 중 `~/.cache/opencode/models.json`과 `~/.local/state/opencode/locks`가 갱신된다고 보고했고, 같은 머신에서 사용자의 `opencode serve`가 따로 돈다. 제품의 OpenCode 실행(모델 호출, 호출 앞의 `--version`, `model probe`, `codeye tools`의 버전 확인)은 모두 실행기를 거치고 실행기는 환경을 처음부터 만든다(호출자의 환경을 물려주지 않고 `HOME`·`TMPDIR`을 실행별 디렉터리로 둔다). `strace -f`로 `--instance-model` 가짜 upstream smoke 전체(OpenCode 24회)와 `codeye tools`를 따라갔을 때 OpenCode가 쓰기로 연 경로는 모두 그 실행의 `HOME`·`TMPDIR` 아래였고 사용자 홈의 두 디렉터리는 열지도 않았다. 그 파일의 갱신은 같은 머신의 별도 `opencode serve`가 낸 것이다(추적한 실행이 없던 시각에도 갱신됐다). 그래서 제품은 바꾸지 않았다. 실행별 `XDG_*`를 따로 설정하는 안은 Linux에서 `HOME`의 기본값과 같은 경로라 효과가 없다. `TestEnvIsolation`이 호출자의 `XDG_*`가 자식에게 닿지 않음을 지킨다. 한계: Linux에서 OpenCode 1.19.6만 추적했고 `codeye doctor`는 같은 탐지 코드를 거치는 것을 코드로만 확인했다.
4. **CI 시간.** 바꾸지 않았다. CI 로그의 시각으로 읽으면 리눅스 job의 `go test -race ./...`는 9분(`internal/worker/review` 한 패키지가 402초이고 나머지는 130초 안에 끝난다), 그 뒤 순차 smoke가 6분, 문서 계약 단계가 2분이다. 코어 사용률은 재지 않았다. smoke를 Go 시험과 겹쳐 실행하면 시간이 줄 수 있으나 두 쪽이 모두 프로세스를 띄우고 smoke가 남은 프로세스 없음을 검사하므로 겹쳐도 안전한지는 확인하지 않았고, 확인 없이는 넣지 않는다.
5. **버전과 호환.** 보고서 생성기 25(`hypothesis_not_shipped` 한계와 `hypotheses_not_shipped`가 저장 본문과 요약을 바꾼다). 이전 리비전은 그대로 읽고 그린다. 검토 기록 schema와 요청, 검토 지침(2.10.0)은 바뀌지 않았다. schema는 `code_review.hypotheses_not_shipped`가 더해졌다.
6. **한계.** 실제 모델에서 이 선택이 시험 코드의 게시 발견 수를 줄이는지, 배포 코드의 진단이 해석에 더 들어가 가설이 늘거나 질이 바뀌는지는 측정하지 않았다. 해석 지침은 관찰의 경로 종류를 모르고(요청의 관찰에는 `path_class`가 없고 창 발췌에만 있다) 이 변경도 지침과 요청을 바꾸지 않았다. 시험 파일의 관찰이 앞서지 않는 것이 구조로 보장될 뿐 지침이 시험 파일의 가설을 덜 내게 하는 개정은 하지 않았다. 확실하지 않은 경로(소스 트리 안의 `test` 디렉터리 등)는 배포 코드로 남아 그 가설은 조치 항목이 될 수 있다.

**A66 — 소진된 보류 분할은 `spent`로 옮기고, 선정 규칙을 먼저 고정한 holdout 분할을 봉인한다.** (기록 2026-11-26) REQ-027, NFR-010과 TC-283·318에 대해 다음을 확정한다. A65까지의 제품 동작은 바뀌지 않는다. 평가 자료와 도구만 바뀐다.

1. **이유.** 프로젝트 규모 집합의 `holdout` 분할 아홉 사례는 2026-11-26에 변경 전 제품과 현재 제품의 짝 측정 한 번에 쓰여 봉인이 소진됐다. 그 분할로 하는 이후 평가는 독립 평가가 아니므로 독립 주장을 하려면 처음 보는 사례와 새 봉인이 필요하다.
2. **이름.** 새 사례가 `holdout`이다. 도구의 `--split holdout`은 그대로 봉인 분할을 가리킨다. 소진된 아홉 사례는 상태를 이름으로 하는 `spent` 분할로 옮겼다. 사례 ID(`PD-<언어>-NN`)는 바꾸지 않고, 새 사례는 언어마다 이어지는 번호를 쓴다(소진된 번호를 다시 쓰지 않는다). 날짜나 차수를 이름에 쓰지 않는다.
3. **선정은 규칙이 먼저다.** 사례를 하나도 찾아보기 전에 선정 규칙(구성·균형, 저장소·사례 형태·최근성 조건, 제외, 고정한 검색 질의와 후보 순서, 멈춤 규칙)을 [fixtures README](../fixtures/project-defects/README.md#보류-분할-선정-규칙)에 먼저 커밋했다(`d7d6f85dac4ec3e611a27f181ab12185eb28ef05`). 후보는 질의 결과 순서로 기준에 맞는 첫 후보를 쓰고, 건너뛴 이유를 코드로 기록했다. 사례 작성자(보조 에이전트)는 검토 지침·평가 결과를 읽지 않았고 CODEYE를 어떤 후보에도 실행하지 않았다. 모호한 규칙은 한 방향으로 정리해 규칙 변경 기록에 남겼다. Java 심은 사례는 1차 목록(저장소 113개)에서 슬롯 둘을 채우지 못했다고 보고 조정자가 승인한 2차 절차(별 50~8000, 질의당 100개, 형제 모듈 허용)를 2차 후보를 읽기 전에 커밋했다(`704786042b5ffacd1318e31721472b864ecd3e8a`). 2차 목록의 이름과 별 수는 커밋 전에 읽었지만 후보를 검토하지는 않았다. 이후 조정자가 결과 순서의 첫 후보 원칙을 적용해, 건너뛴 1차 후보 하나를 다른 사례가 쓰는 것과 같은 종류의 대체물로 다시 확인하게 했다. 그 후보가 첫 슬롯을 채웠고 사례 집합을 다시 봉인했다. 사례별 세부는 README의 규칙 변경 기록에 있다.
4. **봉인 digest.** 사례 파일의 `split` 값이 사례 digest에 들어가므로 이름을 옮긴 `spent`의 digest는 이전 값과 다르다. `corpus.py digest --split spent --as-split holdout`이 봉인 당시의 이름으로 다시 계산해 소진된 봉인 값(`d4cec596f2f7…`)과 같음을 `scripts/bundle_checks/docs.py`가 확인한다(`PROJECT_SPENT_SEAL`). 현재 holdout의 봉인 값과 기준 빌드(봉인 시점의 `main` 커밋)는 평가 문서의 봉인 표와 같은 파일의 `PROJECT_HOLDOUT_SEAL`·`PROJECT_HOLDOUT_BASELINE`에 있고, 표에 없으면 검사가 실패한다.
5. **등록부와 표지.** holdout 등록부는 corpus digest를 키로 하므로 새 digest로 처음부터 시작한다(이전 digest의 실행 기록은 새 holdout에 표지를 만들지 않는다). `spent` 분할의 batch에는 `split_seal_spent` 표지를 붙여 `score`가 그 비교를 독립으로 내지 않는다.
6. **구성 검사.** `validate`는 holdout이 18개이고 언어마다 3개, 종류 9/9, 같은 언어에서 범주가 다르고 범주마다 2~3개이며 두 번째 파일이 필요한 사례가 14개 이상인지 확인한다(`holdout_composition`). 분할 이름은 `dev`·`holdout`·`spent` 셋이다.
7. **채택하지 않은 안.** (가) 소진된 사례 일부를 holdout에 남기는 안은 이미 쓴 사례가 섞여 독립성을 되살리지 못해 버렸다. (나) 소진된 사례를 `dev`에 합치는 안은 조정에 쓰는 분할에 이미 평가한 사례가 섞여 두 분할의 의미가 흐려진다. (다) 사례를 기존 후보 가운데 읽어 보고 고르는 안은 선정 규칙을 먼저 고정하는 목적에 어긋난다.
8. **한계.** 독립성은 선정 규칙과 작성자의 비열람에 기댄다. 실제 수정은 2026-01-01 이후 병합으로 모델의 기억 가능성을 줄였을 뿐 없애지 못한다. 선정 질의가 제목에 "fix"가 있는 PR과 별 수가 중간인 저장소로 기운다. Java 심은 사례는 2차 절차로 의존성 없는 라이브러리 쪽으로 더 기운다. `real_fix` 네 개는 한 파일 안에서 알아볼 수 있다. 일부 사례의 검사는 프로젝트의 시험이 아니라 독립 검사이거나 외부 부품을 대체한 harness이고, 일부 프로젝트 시험은 이 노드에서 실행하지 못했다(사례마다 `verification.summary`). 규칙을 쓴 뒤 후보를 보고 정리한 해석 가운데 선정을 바꾼 것(라이선스 선언 충돌의 범위, 배포 코드 자리, 범주 판정, 검사의 대체물)은 규칙 변경 기록에 있고, 어느 하나가 뒤집히면 사례가 바뀐다. 이 분할은 아직 어떤 평가에도 쓰이지 않았다. 사례를 바꾸면 새 봉인이 필요하다.

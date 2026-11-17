# 27. W01~W05 계획 점검과 W06 착수 준비

2026-11-18 기준. 최초 구현 계획 `6941798`과 병합된 `main`의 `bcfad99`를 비교했다. W01~W05는 원래 선행 관계와 단계별 완료 조건에 맞춰 병합됐다. 다음 단계는 W02와 W05를 연결하는 W06이다. 현재 웹에서 생성한 run은 큐에서 대기한다.

## 1. 확인한 범위

| 단계 | PR | 수용 시험 | 확인한 동작과 범위 |
|---|---|---|---|
| W01 | #1 | TC-36~39 | CLI가 환경·도구·DB를 진단한다. OpenCode 버전 확인과 모델 요청 능력은 별도이며 실모델 시험은 W10에 있다. [W01 기록](21-w01-foundation.md) |
| W02 | #2 | TC-40~45 | 실제 PostgreSQL에서 migration, ACL, 멱등 enqueue, attempt의 소유권과 만료를 검사한다. 노드 등록·자원 예약은 후속이다. [W02 기록](23-w02-storage.md) |
| W03 | #3 | TC-46~52 | loopback 세션으로 프로젝트 등록, 영속 run 요청(202), 조회·취소까지 브라우저에서 연결된다. [W03 기록](24-w03-api.md) |
| W04 | #4 | TC-53~55 | 실제 로컬 Git fixture와 파일시스템으로 확보 시점 입력 고정, 변경 감지, 보존 사본에서의 재시도를 검증했다. private Git/GHES와 NFS 실기는 남아 있다. [W04 기록](25-w04-source.md) |
| W05 | #5 | TC-56~58 | argv·환경·출력 제한과 취소/timeout 후 자손 종료를 OS에서 확인했다. Windows CI와 기존 로컬 Windows 실행 기록이 있으며 로컬 symlink 권한에 따른 두 건의 skip이 있다. CPU·메모리 예약은 advisory다. [W05 기록](26-w05-native.md) |

PR #1~#5의 최종 head가 각각의 squash 병합 커밋과 같은 Git tree임을 확인했다. 확인 시점의 로컬·원격 브랜치는 main 하나였고 추가 Git worktree, 열린 PR, 실행 중인 보조 에이전트는 없었다. 병합된 main의 Linux/PostgreSQL, Windows, 웹 CI 세 작업 모두 성공했다. [main CI 기록](https://github.com/jihoon22-lee/CODEYE/actions/runs/44188576246)

이번 재검증 환경은 Ubuntu 26.04.1 / WSL2 amd64, Go 1.27.4, PostgreSQL 17.10, Node 24.18.0, Python 3.14.4다. 깨끗한 main 소스 사본과 기존 웹 개발 의존성으로 아래 결과를 얻었다.

| 실행 | 결과 |
|---|---|
| `python3 scripts/test_postgres.py --web` | 임시 Native DB에서 Go race 검사 8개 패키지, CLI migration 재적용, doctor DB 진단·JSON 검사, 실제 Chromium 세션·등록·요청·취소 1건 통과 |
| `npm --prefix web test`, `npm --prefix web run build` | 단위 시험 125개 통과, 타입 검사·정적 빌드 성공 |
| `go vet ./...`, `gofmt -l cmd internal migrations` | vet 통과, 포맷 차이 0건 |
| `python3 -m unittest discover -s fixtures/cleanup` | 합성 fixture 6건 통과 |

문서 갱신 후 계약 validator에서 로컬 링크 142개, JSON Schema 7개, 예시 계약 7개와 잘못된 입력의 거부 사례 8개가 통과했다.

이번 로컬 점검에서 Windows 시험을 다시 수행하지 않았다. RHEL 8.10 실기, 물리 노드 간 복구, 분석기·모델·보고서까지의 R0 수직 검증과 품질 측정은 남아 있다.

## 2. W06 작업 티켓

**목표**는 한 머신에서 서로 다른 identity를 가진 에이전트 두 개가 같은 run을 요청했을 때 한 워커만 소유권을 얻고, 권한·lease·프로세스 종료 결과가 API에서 DB까지 일치하는 것이다. W03 개인 모드 API를 진입점으로 쓰고 W02 저장 계약과 W05 실행기를 재사용한다. REQ-018/019/022/035, NFR-002/003/006 및 TC-21이 중심이다. TC-18/22/23 중 연결할 부분은 아래 시험에 명시했다. [워커 설계](09-workers-and-scheduling.md) · [노드 API](11-api-and-events.md) · [수용 기준](18-acceptance-and-traceability.md)

| 순서 | 입력과 변경 위치 | 산출물 및 실패 처리 |
|---|---|---|
| 1. 등록과 인증 | 운영자가 발급한 만료 있는 일회용 등록 토큰, 승인 pool·source root. CLI, migration, HTTP 인증 경로 | 등록 후 노드 전용 credential 발급. 사용 완료·만료·철회 토큰, 다른 노드 ID, 사용자 세션으로 노드 경로 접근을 거부한다. 워커로 토큰·DB 접속 정보를 전달하지 않는다. |
| 2. heartbeat와 claim | 노드가 보고한 실제 능력·가용량·idle worker, DB에 저장한 run·프로젝트 revision·권한 | 호환성·권한·예산 확인, 예약, attempt 생성, epoch 증가, 이벤트 삽입을 한 트랜잭션에 둔다. 기존 저장 함수 `BeginAttempt`는 소유권만 확보하므로 그대로 HTTP에 노출할 수 없다. 호환 작업이 없으면 204이고, 응답 유실 뒤 재전송해도 중복 예약이 없어야 한다. [저장 구현](../internal/storage/postgres/attempts.go) |
| 3. 워커와 lease | 인증된 에이전트가 한 attempt를 워커에 전달. 응답의 만료 시각·취소 의도와 W05 실행 결과를 수집 | 서버는 DB 시각과 node/worker/epoch로 갱신·완료를 검사한다. 에이전트는 갱신 단절 시 단조 시계로 새 작업을 중단하고 종료를 시도한다. PID와 시작 식별자, 실행 의도, 실제 종료를 대조한다. 정리 불확실 시 예약을 유지하고 `recovery_required`로 보낸다. [실행기 계약](../internal/worker/executor/types.go) |
| 4. 수용 증거 | 실 PostgreSQL·개인 모드 controller·별도 상태 디렉터리의 두 agent 프로세스·worker 프로세스 | 경쟁·재전송·취소 뒤 HTTP 응답, run/attempt ID·epoch, 활성 예약 수, 상태 이벤트, OS 종료 관측을 함께 기록한다. 프로토콜 시험용 작업은 fixture로 표시한다. 실제 분석기가 없는 제품 요청에는 대기 사유를 제공한다. |

W07에서 워커 목표 수 계산, 자동 증감, drain과 장애 후 재배정을 검증한다. W06의 최소 자원 예약은 W07 정책이 같은 예약을 재사용할 수 있게 설계한다. W08은 첫 실도구 분석, W09는 결과 발행을 담당한다.

### 구현 전 맞출 계약

- **노드 HTTP.** 설계 문서에는 enroll이 있고 현재 OpenAPI에는 heartbeat·claims·attempt heartbeat·complete만 노드 수직 경로로 선언돼 있다. 기존 준비 초안과 명세는 heartbeat의 용량 단위(MB/bytes), 명령 cursor(ID/sequence), claim 응답(lease 단독 또는 작업 명세 포함), complete의 worker 식별 방식이 다르다. 첫 변경에서 단위·재전송·인증된 소유자 매핑을 확정하고 OpenAPI·예시·route 대조 시험·결정 기록을 함께 갱신한다. [OpenAPI](../api/openapi.yaml) · [route 대조 시험](../internal/httpapi/contract_test.go)
- **snapshot을 연결할 때.** W02는 Git snapshot의 digest에 커밋 SHA를 허용한다. W04의 snapshot digest는 canonical manifest bytes의 SHA-256이다. 기존 Git 행에서 manifest hash를 추정할 수 없으므로 커밋과 manifest의 필드를 구별하고, 과거 행과 보존 사본이 없는 재시도 처리를 migration 시험에 넣는다. [W02 저장 코드](../internal/storage/postgres/evidence.go) · [W04 manifest 계약](25-w04-source.md)
- **종료와 복구.** W05의 `OnStarted`는 프로세스가 실행된 뒤 호출된다. 저널에 알림이 없다는 사실만으로 자식이 없음을 확인할 수 없다. 실행 의도를 먼저 기록하고, 실행기 한 번의 `not_started`는 attempt 전체의 다른 자식·미해결 의도까지 확인한 뒤 합산한다. 성공 결과는 W02의 `finalizing` 게이트를 따른다. [W05 결정](26-w05-native.md) · [W02 결정](23-w02-storage.md)

### 수용 시험과 마이그레이션

1. **TC-21** — 같은 큐의 run 하나에 두 agent가 동시에 claim한다. 활성 attempt·lease epoch·예약이 각각 하나이며 노드·전역 예산이 한 번만 줄어야 한다. 응답 유실 뒤 같은 요청을 재전송하고, 예약 후 이벤트 저장 실패를 유도해 부분 커밋이 없는지도 검사한다.
2. **REQ-019, NFR-006** — 허가받지 않은 source root, 오래된 heartbeat, 분석 능력 누락, 자원 부족에서 배정을 거부한다. 등록 토큰 동시 사용은 한 번만 성공하며 재사용·만료·노드 자격 철회 뒤에는 권한이 생기지 않아야 한다.
3. **TC-18/22/23의 프로토콜 범위** — 취소·갱신 단절 시 실제 자손 종료를 확인하고, 다른 worker·이전 epoch·만료 후의 갱신과 새 완료 쓰기는 409로 거부한다. 현재 소유자의 동일 완료 재전송은 기존 receipt를 확인한다. agent 재시작 시 PID 재사용 또는 실행 증거 누락은 불확실로 남기고 중복 실행을 막는다. partition 후 전체 재배정과 다중 물리 노드 복구는 W07·W29에서 확장 검증한다.
4. **DB 변경** — 0001/0002 checksum을 보존하는 후속 migration으로 노드 identity·토큰 hash·워커·예약을 추가한다. 빈 DB와 기존 run/attempt/snapshot이 있는 0002 DB 양쪽에서 이행·재적용·실패 rollback을 시험한다. 이미 적용한 DB에서 구버전 serve의 schema 거부와 백업 복원 절차를 확인한다.

W06 완료 기록에는 위 시험의 명령, OS/DB 버전, 두 에이전트의 경합·복구 결과와 미실행 환경을 남긴다. 이 문서는 착수 준비이며 W06 수용 결과는 구현 후 기록한다.

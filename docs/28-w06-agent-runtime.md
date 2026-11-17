# 28. W06 노드 agent·worker 런타임

상태: 노드 쪽(agent 감독·journal·lease watchdog·재시작 복구, worker 실행 루프, `codeye agent`/`codeye worker` 진입점)을 구현했다. Linux에서 실제 하위 프로세스로 시험했다 · 2026-11-18. controller·저장 계층은 [28 node protocol](28-w06-node-protocol.md)이 다룬다. 분석 handler(W08 Go vet)가 없는 제품 빌드는 `analysis:*` 능력을 광고하지 않고 claim하지 않는다. 시험용 fixture handler는 `-tags codeye_fixture`에서만 들어간다. Windows는 컴파일·vet만 확인했고 실행하지 않았다.

## 1. 범위와 요구사항

| 항목 | 연결 |
|---|---|
| outbound API agent, SSH 없음, 노드는 DB에 접근하지 않음 | REQ-018/019, [09 §1](09-workers-and-scheduling.md) |
| 한 worker는 한 attempt만 실행, attempt별 scratch·workspace 분리, 원본 경로에서 실행하지 않음 | REQ-014/015, TC-17 일부 |
| lease 갱신과 monotonic watchdog(lease − margin), 409 STALE에서 새 task 중지·취소 | REQ-022, TC-22 노드 범위 |
| 취소·timeout·agent 종료 시 손자까지 정리 확인, 불확실하면 uncertain | REQ-035, TC-18 |
| 프로세스 journal과 재시작 복구, PID 재사용 시 신호 없음 | TC-23 노드 범위, NFR-001 |
| worker에 자격증명 없음(env·argv·IPC·journal) | NFR-006, 14 |
| 고정 snapshot 재시도는 Load만, 재획득 없음 | REQ-003, TC-03 일부 |
| static worker 수와 W07 확장점(Scaler) | REQ-020/021 준비(자동 증감은 W07) |

코드: `internal/agent`(agent·client·journal·supervisor·reconcile·scaler·spawn), `internal/agent/procid`, `internal/workerproto`, `internal/worker/run`, `internal/worker/analysis/fixture`(태그), `internal/worker/analysis/builtin/fixture_on.go`(태그), `cmd/codeye/node.go`. `internal/nodeproto`는 controller 쪽 소유이며 같은 내용을 쓴다. `internal/worker/analysis`·`builtin/builtin.go`는 W08 소유 파일의 임시 사본이다.

## 2. CLI

```text
codeye agent --controller URL --state-dir DIR [--name NAME] [--enroll-token-file PATH]
             [--source-root ID=/abs/path ...] [--workers N] [--toolchain-path PATH]
             [--git /abs/git --git-host HOST ...] [--go /abs/go] [--go-mod-cache /abs/dir]
codeye worker      # agent만 실행한다. 옵션 없음.
```

- `--controller`는 https이거나, loopback 주소의 http만 허용한다. 프록시 환경변수를 쓰지 않고 redirect를 따르지 않는다.
- 첫 실행에는 `--enroll-token-file`이 필요하다. 토큰 파일은 읽기만 하고 로그에 남기지 않는다. 등록 결과는 `<state-dir>/node.json`(0600, 디렉터리 0700)에 저장한다. 저장된 controller와 `--controller`가 다르면 요청을 보내기 전에 종료한다(exit 2). 노드 비밀값을 다른 controller에 보내지 않기 위해서다.
- `<state-dir>/agent.lock`에 배타 flock을 건다. 같은 state dir의 두 번째 agent는 journal·identity를 건드리기 전에 종료한다(exit 1).
- exit code: 0 정상 종료, 1 실행 실패 또는 종료 시 정리 불확실, 2 옵션 오류, 3 등록 거부, 4 노드 철회.
- 로그는 stderr의 JSON 줄(`code` + ID)이다. 비밀값·토큰은 쓰지 않는다. 상태 판단은 controller API로 하고 로그는 진단용이다.
- `main.go` 연결(`runNode`)은 통합 단계에서 한다.

## 3. 구조

- **agent.** 등록 → journal 열기 → 복구(§5) → journal 압축 → worker 시작 → 노드 heartbeat(기본 5초) 순서로 동작한다. heartbeat는 관측 worker 상태, 실제 여유 메모리(`MemAvailable`), state 파일시스템 여유 disk, CPU 수, 능력, 명령 cursor, 정리 보고를 보낸다. controller가 heartbeat에서 `idle`로 본 worker만 claim할 수 있다. 그래서 claim은 마지막 heartbeat에서 idle로 보고된 worker 하나당 한 번만 하고, attempt가 끝나면 즉시 heartbeat를 보낸다. claim `Idempotency-Key`는 `<worker_id>:<agent 실행 nonce>-<counter>`이며 네트워크 재시도에서는 같은 key를 쓴다.
- **능력.** `analysis:*`는 준비된 worker가 `ready`에서 알린 handler에서만 만든다. handler가 없으면 claim 요청 자체를 보내지 않고 `no_analysis_handler`를 기록한다. `source:local:<id>`는 `--source-root` 매핑이 있는 root만, `source:git:<host>`는 `--git`과 `--git-host`가 모두 있을 때만 보낸다.
- **worker 실행.** argv는 `<codeye> worker`다. env는 `HOME`·`TMPDIR`/`TMP`/`TEMP`(worker 디렉터리 아래), `LANG=C.UTF-8`, `PATH`(`--toolchain-path`)만 쓴다. Windows는 `SystemRoot`를 더한다. 상속하는 값은 없다. Linux에서는 자체 process group(pgid = pid)으로 만들고 `PR_SET_PDEATHSIG=SIGTERM`을 건다. PDEATHSIG는 fork한 OS thread가 끝날 때 발동하므로, agent 수명 동안 유지하는 전용 locked thread에서 시작한다. 시작 직후 `/proc` identity를 journal에 fsync하고 나서 `hello`를 보낸다. worker의 stdin EOF나 SIGTERM은 실행 중 attempt를 취소하고, 정리 결과를 확인한 뒤 종료하게 한다.
- **IPC.** `codeye-worker/0.1` JSONL이다. 한 줄은 1 MiB 이하이고 unknown type·field는 프로토콜 오류로 처리해 worker를 종료한다. journal 성격의 메시지(`intent`·`process_started`·`process_ended`)는 읽기 goroutine에서 fsync 후 바로 답한다. HTTP가 필요한 `event`·`artifact`·`complete`는 worker별 순서 큐에서 처리한다. 그래서 executor `OnStarted`(5초 제한)가 업로드 뒤에서 기다리지 않는다. snapshot 위치는 IPC 전용 필드로 agent 비공개 cache(`snapshots.jsonl`)에만 기록하고 서버에는 보내지 않는다.
- **artifact.** worker가 보낸 경로는 다음을 모두 만족해야 받는다: 절대 경로, 마지막 요소가 symlink가 아님(`O_NOFOLLOW`), 일반 파일, 해당 worker scratch 또는 snapshot data root 안, 64 MiB 이하. 업로드하는 bytes와 같은 bytes로 hash를 다시 계산하고, worker가 보고한 hash와 크기가 다르면 거부한다. 따라서 tool이 만든 symlink로 `node.json`을 올릴 수 없다.
- **lease watchdog.** 마지막 갱신 성공 시점(요청 전송 시각, monotonic)부터 lease − margin(기본 30 − 10초)이 지나면 `stop_new_tasks`와 `cancel{lease_lost}`를 보낸다. 409 STALE를 받아도 같다. 그 뒤 worker의 complete는 보내지 않는다(stale). 실제 정리 결과는 heartbeat `cleanup_reports`로 보낸다. `cancel_requested=true` 응답과 `cancel_attempt` 명령은 사용자 취소로 전달한다.
- **worker 순서.** intent `source_acquire_started` → W04 획득 또는 `Load`+`CloneWorkspace`(고정 snapshot은 재획득 금지) → intent done → `source_manifest` artifact → `snapshot_fixed` → `Plan` → `task_created`×n → `run_advance running` → task마다 `running` → `Handler.Run` → stdout·stderr·artifact 업로드 → 종료 상태 → (취소·lease 상실이 없을 때만) `run_advance finalizing` → `result_manifest` → `complete`.
- **runner 감싸기.** worker는 handler에게 감싼 runner를 넘긴다. 이 runner는 매 호출 전에 intent `task_exec_started`를 journal에 남긴다. 또 `ProtectedRoots`에 snapshot·승인 원본 root를 덧붙이고(덮어쓰지 않는다) `TempParent`·`TaskID`·`AttemptEpoch`·`OnStarted`를 설정한다. 결과는 모두 worker가 직접 기록한다. 정리 상태와 "종료했는가" 판정은 이 기록에서 나오며 handler가 주장할 수 없다. handler가 `succeeded`라고 해도 기록된 실행이 신호 종료·비종료·잘림·불완전·정리 불확실이면 `failed`로 낮춘다. 명령 argv/dir과 stdout/stderr는 handler가 명시적으로 돌려준 실행만 게시한다. 기록됐지만 반환되지 않은 실행은 `unreported`로 상태·정리만 남긴다(모델 출력 등 비공개 내용 보호).
- **결과.** outcome은 구조화 상태로만 정한다. 사용자 취소는 `cancelled`, lease 상실·agent 종료는 `failed`(reason `lease_lost`/`agent_shutdown`)다. 필수 task가 모두 성공하면 `succeeded`, 일부만 읽을 수 있는 결과가 있으면 `partial`, 그 외는 `failed`다. ResultManifest는 W08의 `analysis.ResultManifest`로 만든다. `Validate`·canonical JSON을 거치고 노드 절대 경로는 workspace 상대 경로나 `[REDACTED]`로 바꾼다. cleanup은 실행·획득·intent 중 하나라도 불확실하면 `uncertain`이다. agent journal에 열린 intent가 있으면 worker가 `confirmed`라고 보고해도 `uncertain`으로 덮어쓴다.

## 4. Journal

`<state-dir>/journal.jsonl`(0600). 기록마다 fsync하며 기록 종류는 `worker_started/exited`, `attempt_assigned/done`, `intent`, `process_started/ended`, `command_applied`, `cleanup_pending/acked`다. 순서 규칙은 다음과 같다. worker는 `hello` 전에 기록한다. attempt는 `job` 전에 기록한다. 하위 프로세스가 생길 수 있는 단계(W04 획득, 매 `Runner.Run`)는 시작 전에 intent를 기록하고, agent 답을 받은 뒤에만 진행한다. W05 `OnStarted`는 실행이 시작된 뒤 호출된다. 따라서 journal은 실행을 막지 못하고 미확인 구간만 좁힌다. 마지막 줄이 잘린 journal은 잘린 부분을 버리고 `Torn`으로 표시한다. 그러면 미완료 attempt는 모두 uncertain이 된다. 압축은 시작 시에만 한다. 미완료 attempt가 참조하는 worker의 시작·종료 기록은 압축 후에도 남긴다.

## 5. 재시작 복구

복구는 첫 heartbeat 전에 실행한다.

1. journal에서 종료 기록이 없는 worker를 찾아 pidfd로 고정한 뒤 identity를 확인하고 group을 종료한다.
2. 미완료 attempt는 다음을 **모두** 만족할 때만 `confirmed`다:
   - worker가 종료로 기록돼 있거나, 이번 복구에서 사라졌음/종료됨을 확인했다(기록이 없으면 확인으로 보지 않는다).
   - journal된 task process group이 모두 비었다.
   - 열린 intent가 없고, 획득이 `recovery_required`로 끝나지 않았고, journal이 잘리지 않았다.
3. 결과는 `cleanup_pending` → heartbeat `cleanup_reports`로 보낸다. attempt는 다시 실행하지 않는다.

process group 종료(`procid.TerminateVerifiedGroup`):

- `pidfd_open(leader)`로 leader를 먼저 고정한다. 그 뒤 `/proc`에서 start time과 pgid == leader를 확인하고, `pidfd_send_signal(PIDFD_SIGNAL_PROCESS_GROUP)`로만 신호를 보낸다. 순서는 TERM → grace → KILL → 비었는지 관측이다. 확인과 신호 사이에 재사용된 PID·PGID를 맞힐 수 없다.
- start time이 다르면(재사용) 신호를 보내지 않고 uncertain이다.
- leader가 없으면 신호 없이 그 pgid의 프로세스가 없을 때만 확인으로 본다.
- pidfd group 신호를 지원하지 않는 커널(Linux 6.9 미만, 예: RHEL 8/9 기본 커널)이면 uncertain이다. `kill(-pgid)`로 대체하지 않는다.

## 6. 시험

모든 시험은 Linux(WSL2 6.18, Go 1.27.4)의 실제 하위 프로세스로 실행했다. `-race` 포함.

| 시험 | 핵심 단언 | 연결 |
|---|---|---|
| `workerproto` 7건 | request/reply 대응, 오류 코드, unknown type·field·1 MiB 초과·닫힌 연결, transcript | §IPC |
| `procid` 3건 | 손자 포함 group 확인 종료, 재사용 PID 미신호, leader 없는 group에 생존자가 있으면 uncertain(신호 없음) | TC-23, TC-18 |
| `worker/run` 8건 | 정상 순서·manifest canonical·노드 경로 없음·scratch 정리, 취소 시 손자 종료·`cancelled`, stale 답→`lease_lost`·task 미실행, stdin EOF 시 트리 정리, handler 없음, 신호 종료 성공 주장 강등, 미반환 실행 출력 미게시, 고정 snapshot 미보유 시 재획득 없이 `snapshot_unavailable` | TC-18, REQ-003 |
| `agent` 12건(시험용 strict fake controller) | 등록·claim·성공·manifest, worker `/proc/<pid>/environ` 키 6개뿐·IPC/log에 비밀값 없음·node.json 0600, handler 없으면 claim 0건·`analysis:*` 없음, `cancel_attempt` → 손자 종료·cancelled/confirmed, lease 갱신 정지 → lease 만료 전 정리 확인 보고·complete 없음, agent+worker SIGKILL 후 재시작 → 살아남은 task group 확인 종료·confirmed 보고·재실행 없음, journal의 재사용 PID → 무관 프로세스 생존·uncertain, 잘린 journal → uncertain, agent 종료 → `agent_shutdown`·confirmed·worker 종료 기록, 같은 state dir 두 번째 agent 거부, controller URL 정책, 다른 controller에 비밀값 미제시, 노드 패키지의 storage/pgx/httpapi 비의존(`go list -deps`, fixture 태그 포함) | TC-18, TC-22/23 노드 범위, NFR-006 |
| `cmd/codeye` 2건 | 옵션 오류 exit 2, 거부된 인자 미출력, 미등록 시 토큰 파일 요구 | NFR-006 |
| fixture(태그) 1건 | succeed/fail/partial/unsupported/sleep 취소 정리 | 시험 지원 |

**실제 controller 연동.**

- 통합 E2E: 통합 브랜치의 `scripts/node_smoke.py`가 실제 `codeye` 바이너리 두 종류(controller, `-tags codeye_fixture` agent 프로세스 2개)로 W03 enqueue부터 실행·실제 취소까지 검증한다(통합 담당 소유, 결과는 그쪽 기록을 따른다).
- 보조 확인(일회성, 저장소에 넣지 않음): controller 쪽 커밋(9df7c55)과 이 브랜치를 임시 로컬 브랜치에서 병합했고 충돌은 없었다. 실제 `httpapi`+PostgreSQL 17.10에 in-process agent 2개(worker 각 2개, 시험 바이너리 재실행)를 붙였다. 결과:
  - run 4개: 각각 attempt 1개로 `finalizing`, `released`/`confirmed`, 미해제 예약 0, 동시 예약 3개 겹침 0(전역 상한 2).
  - 손자를 만드는 run을 사용자가 취소: `cancelled`, `cleanup_state=confirmed`.
  - 일반 실행과 `-race` 모두 통과했다. 병합 상태 `go test ./...`도 통과했다. 예외는 비밀번호 인증 DB를 요구하는 doctor 시험 1건이며, 임시 인스턴스가 trust 인증이라 실패한 환경 차이다.
  - 임시 브랜치와 시험 파일은 삭제했다.
- TC-21의 공식 증거는 controller 쪽 DB 시험([28 node protocol §8](28-w06-node-protocol.md#8-시험))과 통합 smoke다.

**handler 구분.**

- 제품 registry(`builtin.Handlers`)에는 W08 전까지 handler가 없다.
- `-tags codeye_fixture` 빌드에만 `analysis/fixture` handler(capability `analysis:deep-review`, mode 파일 `codeye-fixture-mode`)가 들어간다. 통합 smoke가 이것을 쓴다.
- 위 Go 시험의 handler(`modeHandler`, `testHandler`)는 `_test.go` 안에만 있는 시험 전용이다. 제품·fixture 빌드 어디에도 포함되지 않는다.
- 어느 것도 분석 결과가 아니다.

미실행: Windows에서 PostgreSQL·두 에이전트를 함께 기동하는 전체 시험, RHEL 8.10, Git 원격 획득을 포함한 agent 경로(W04 단위 시험만), W08 go-vet handler와의 실제 연동, 노드 철회 중 실행(exit 4 경로는 fake 시험 없음).

## 7. 남은 제한

- Native 실행은 샌드박스가 아니다. 같은 OS 사용자로 도는 분석 도구는 `<state-dir>/node.json`(노드 비밀값)을 읽을 수 있다. worker와 IPC에는 비밀값이 없다. 하지만 파일 권한 0600은 같은 사용자를 막지 못한다. 별도 실행 계정 분리는 운영 배치 문제로 남긴다.
- W04 Git 획득의 git 하위 프로세스는 개별 journal하지 않는다. 획득 도중 agent가 죽으면 intent가 열린 채 남아 uncertain이 된다.
- handler 능력 probe(W08 `go version`)는 attempt 전 worker 시작 시 실행하며 journal하지 않는다. executor timeout으로만 제한된다.
- journal 압축은 시작 시에만 한다. snapshot cache GC와 노드 간 snapshot 전송은 없다.
- Windows에서는 에이전트가 suspended 워커를 전용 Job에 배치하고 시작한다. WSL interop을 통해 Windows 실행 파일에서 에이전트 강제 종료 후 워커와 자손 2개의 종료를 열린 프로세스 handle로 검증했다(`TestAgentDeathClosesWorkerJob`). 에이전트 재시작 후 이전 Job의 상태를 확인할 수 없으면 복구 상태를 유지한다.
- 자동 증감은 W07이다. W06은 고정 worker 수(`StaticScaler`)를 쓴다. `start/drain/stop_worker` 명령은 받아서 적용하지만 controller가 아직 보내지 않는다.
- `internal/worker/analysis`·`builtin/builtin.go`는 W08 파일의 임시 사본이다. 병합 시 W08 버전을 쓴다.

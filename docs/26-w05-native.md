# 26. W05 NativeExecutor

상태: W05 Linux 실제 subprocess 검증 완료 · Windows는 교차 컴파일·vet만 로컬 확인, 실행 시험은 CI `windows-2025` 결과로 판정 · 2026-11-18. agent·worker protocol(W06), 분석기 어댑터(W08)는 구현하지 않았다.

## 1. 범위와 요구사항

| 항목 | 연결 |
|---|---|
| argv 직접 실행, 셸 결합 없음, 실행 파일 절대 경로 | REQ-014, [08 §3](08-native-execution.md) |
| 원본/스냅샷 경로에서 실행 금지, 명시 env, 인증정보 미전달 | REQ-015, 08 §4 |
| stdout/stderr 별도 byte cap, 잘림 기록, 비밀값 마스킹 | 08 §7, TC-08 일부 |
| timeout·취소 시 프로세스 트리 종료 확인, 불확실하면 recovery_required | REQ-022, TC-18, 08 §5·§6·§8 |
| 원본 hash 불변, 작업별 env·temp·로그 독립 | TC-17 일부 |
| 자원 요청과 적용·관찰 구분 | 08 §7 |

코드: `internal/worker/executor`. 의존성은 `golang.org/x/sys` v0.50.0(Windows Job Object, Linux waitid)이다.

## 2. API 계약

```go
var e executor.Executor            // zero value 사용, 동시 Run 안전
res := e.Run(ctx, executor.Spec{
    Executable:    "/opt/tools/bin/cppcheck",  // 절대 경로, PATH 검색 없음
    Args:          []string{"--xml", "src"},  // argv[1:], 그대로 전달
    WorkspaceRoot: workCopy,                   // 승인된 작업 사본
    Dir:           workCopy,                   // realpath가 WorkspaceRoot 안
    ProtectedRoots: []string{snapshotPath},    // 스냅샷·원본: 겹치면 거부
    Env:           map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"},
    Timeout:       10 * time.Minute,
})
if res.RecoveryRequired() { /* attempt를 recovery_required로 */ }
```

W08 이후 분석기는 `executor.Runner` 인터페이스에 의존한다. 결과 채널은 `Result` 하나이며 오류를 별도로 반환하지 않는다.

| Result.Status | 의미 |
|---|---|
| `exited` | 주 프로세스가 스스로 종료. `ExitCode`(또는 Linux `Signal`)로 해석하며 성공을 뜻하지 않는다 |
| `timed_out` | `Spec.Timeout` 경과로 트리 종료 |
| `cancelled` | ctx 취소(`context_cancelled`) 또는 ctx deadline(`context_deadline`) |
| `rejected` | Spec 검증 실패. 프로세스를 만들지 않음 |
| `start_failed` | OS가 실행을 거부(exec format, 권한 등) |
| `unsupported` | 강제 자원 한도 요구, Job 연결 실패, 미구현 OS 등 능력 부족. 사용자 코드를 실행하지 않음 |

`Cleanup`은 W02 `cleanup_state` 어휘를 따른다: `confirmed`(격리 단위가 비었음을 관찰), `uncertain`(recovery_required), `not_started`. `Reason`은 안정 코드이고 `CleanupNote`는 `processes_remain_after_kill`, `escaped_descendant_alive`, `output_pipe_held_open`, `main_process_not_reaped`, `proc_scan_failed`, `job_query_failed` 등이다. `LeftoverKilled`는 주 프로세스 종료 후 남은 자손을 executor가 종료했음을 뜻한다.

`Command`에는 실행 파일·symlink 해석 경로·마스킹된 argv·cwd·env **키 이름만**·temp 경로가 저장된다. `SensitiveArgs` 인덱스는 `[REDACTED]`로, `Redact` 값과 민감 인자 값은 기록 argv와 캡처한 stdout/stderr에서 치환한다. cap 경계에 걸친 비밀값 일부는 치환되지 않을 수 있다. `Stdout`/`Stderr`는 `Data`, 전체 관찰 바이트, `Truncated`, `Incomplete`(EOF 미도달, 그때까지 캡처한 데이터는 유지)를 가진다. `Command.TempRetained`는 실행별 temp를 남겼음을 뜻한다. `OnStarted(ProcessIdentity{PID, StartTime})`는 W06 journal 기록용 hook이다.

## 3. 검증 규칙

- 실행 파일: 절대 경로, 존재, 일반 파일, Unix 실행 비트. Windows `.bat`/`.cmd`는 암묵적 `cmd.exe` 해석 때문에 `batch_script_unsupported`로 거부한다. 셸이 필요한 경우는 향후 명시 셸 프로파일로 다룬다.
- 경로: `WorkspaceRoot`·`Dir`·`TempParent`는 절대 경로이며 `EvalSymlinks` 후 비교한다(Windows는 대소문자 무시). `Dir`이 workspace 밖(`..`, symlink 포함)이거나 `ProtectedRoots`와 workspace/temp가 겹치면 거부한다. 검증 후 symlink 교체 TOCTOU는 남은 제한이다.
- env: 부모 env를 상속하지 않는다. `HOME`·`USERPROFILE`·`TMPDIR`·`TMP`·`TEMP`는 실행별 `codeye-exec-*/home|tmp`로 executor가 설정하며 호출자가 덮을 수 없다. `SSH_AUTH_SOCK`, `GIT_ASKPASS`, `DATABASE_URL`, `PGPASSWORD`, `*TOKEN*`, `*SECRET*`, `*PASSWORD*`, `*API_KEY*`, `AWS_*`, `ANTHROPIC_*`, `OPENAI_*` 등 자격증명형 키는 값과 무관하게 거부한다. Windows `os/exec`는 `SYSTEMROOT`가 없으면 부모 값을 추가한다. `PATH`가 필요하면 호출자가 명시한다.
- temp: 확인된 cleanup 후 삭제(Windows 공유 위반 대비 짧은 재시도)하고, 불확실하거나 삭제에 실패하면 남기고 `TempRetained`를 기록한다.
- `ProtectedRoots`는 호출자 의무다. W04 스냅샷·원본 경로를 W08 분석기가 넘기지 않으면 executor는 그 경로를 알 수 없다.
- `Grace`는 0이면 기본 2초다. 즉시 강제 종료를 요청하는 값은 W05에 없다.
- 출력: executor 소유 pipe의 `*os.File`을 자식에 넘기고 별도 reader가 cap 이후에도 계속 읽어 자식이 막히지 않게 한다. 기본 cap은 stream별 1 MiB, 최대 256 MiB다.

## 4. 프로세스 격리와 종료

**Linux**: `Setsid`로 새 session/process group(sid=pgid=pid)을 만든다. 주 프로세스는 `waitid(WNOWAIT)`로 종료만 관찰하고 reap하지 않아 PID/PGID 번호가 재사용되지 않은 상태에서 group에 신호를 보낸다. 종료 순서는 SIGTERM→grace(기본 2초)→SIGKILL 반복→`/proc` 재조사다. pgrp 또는 sid가 같은 살아 있는 프로세스가 없고 주 프로세스가 종료돼야 `confirmed`다. reap 후 `kill(-pgid, 0)`이 ESRCH인지 다시 확인한다. 주 프로세스가 정상 종료해도 group/session 구성원이 남으면 같은 절차로 종료하고 `LeftoverKilled`를 기록한다.

`/proc` 조사 시 트리에서 session/group을 떠난 자손(setsid 등)을 발견하면 종료하지 않고 PID+starttime으로 생존을 확인해 `escaped_descendant_alive`로 `uncertain`을 남긴다. 탈출 프로세스가 출력 pipe를 쥐고 있으면 `output_pipe_held_open`도 기록한다. 부모가 먼저 죽은 뒤 setsid하고 fd를 닫은 daemon은 관찰할 수 없으므로 `confirmed`는 "process group/session이 비었음"이며 "모든 자손이 없음"이 아니다. 에이전트 crash 시 고아 정리는 W06 journal·reconciliation 책임이다.

**Windows**: `CREATE_SUSPENDED`로 생성→전용 Job Object(`KILL_ON_JOB_CLOSE`, `DIE_ON_UNHANDLED_EXCEPTION`, BREAKAWAY_OK 없음)에 할당→단 하나의 주 스레드를 suspend count 1에서 resume한다. 그래서 Job 밖에서 자식이 생기는 race가 없다. Job 생성·할당·resume 중 하나라도 실패하면 suspended 프로세스를 종료하고 `unsupported`로 보고하며 Job 밖에서 실행하지 않는다. timeout·취소는 graceful 단계 없이 `TerminateJobObject` 후 `ActiveProcesses==0`과 주 프로세스 종료를 기다린다. 주 프로세스 종료 후 Job에 남은 프로세스도 종료한다. Job handle을 닫으면 남은 프로세스는 OS가 종료한다.

Linux·Windows 외 OS는 `platform_unsupported`로 실행하지 않는다.

## 5. 자원

`Spec.Resources`(CPU milli, memory bytes)는 예약/추정값으로 그대로 `Result.Resources.Requested`에 남는다. W05는 cgroup·Job 한도를 **적용하지 않으며** `Enforced`는 항상 false다. `RequireEnforced=true`면 `unsupported`다. 관찰값은 Linux에서 주 프로세스와 reap된 자식의 rusage CPU·max RSS(`main_process_rusage`), Windows에서 Job 전체 CPU·최대 메모리(`job`)다. Native 실행은 보안 샌드박스가 아니며 실행 계정의 파일 접근 권한을 그대로 가진다.

## 6. 시험

테스트 바이너리가 자기 자신을 `codeye-helper <mode>` argv로 재실행하는 실제 subprocess 시험이다. env를 격리하므로 mode는 env가 아닌 argv로 전달한다. 프로세스 사망은 executor 주장 대신 OS(`/proc`, `OpenProcess`+wait)로 확인한다.

| 시험 | 확인 |
|---|---|
| TestArgvPassedLiterally | 공백·한글·따옴표·역슬래시·`$()`·백틱·`;&|<>^%`·빈 문자열·개행이 그대로 전달, 셸 부작용 파일 없음, workspace 경로에 공백·한글 |
| TestEnvIsolation | 부모 `SSH_AUTH_SOCK`·`GITHUB_TOKEN`·canary 미상속, 허용 키만 존재, 실행별 HOME/TMP와 삭제, 민감·예약 키 거부 |
| TestSpecGuards / TestSymlinkCwdEscapeRejected | 상대·없는·디렉터리 실행 파일, workspace 밖·`..`·symlink cwd, 스냅샷 겹침, temp 겹침, NUL 인자 거부와 미실행 |
| TestOutputCapsPerStream | stdout 300000B 중 1024B 보존·잘림·총량, stderr 독립 |
| TestNonzeroExitIsNotSuccessOrCrash | exit 7을 `exited`+코드로 구분 |
| TestTimeoutKillsProcessTree / TestCancelKillsProcessTree | 주→자식→손자 트리 timeout·취소 후 세 PID 모두 OS에서 사라짐, `confirmed`, identity hook |
| TestLeftoverProcessesKilledAfterMainExit | 주 프로세스 exit 0 후 pipe를 쥔 자손 종료와 `LeftoverKilled` |
| TestSnapshotSourceUnchanged | 보호 원본 hash 불변, 작업 사본만 변경, 원본 cwd 거부 |
| TestRedaction | 민감 인자·값이 기록 argv·JSON·stdout에 없음 |
| TestEnforcedResourcesUnsupported | 강제 한도 요구 시 미실행, 기본은 `Enforced=false` |
| Linux 전용 | SIGTERM 정상 종료, SIGTERM 무시 시 grace 후 SIGKILL, setsid 탈출 자손 → `uncertain`·temp 보존, `/proc/stat` comm 파싱 |
| Windows 전용 | Job breakaway 거부, `.cmd` 거부, graceful 없는 Job 종료, Job 단위 CPU·메모리 관찰 |

로컬 실행 결과(Linux WSL2, Go 1.27.4, `GOTOOLCHAIN=local`): `go test -race -count=3`(보강 전)과 `-race -count=2`(최종) `./internal/worker/executor` 통과, `go test ./...`·`go vet ./...`·`GOOS=windows go vet ./...`·`GOOS=windows go test -c` 통과. 그룹 대신 주 프로세스에만 신호를 보내도록 바꾼 변이에서 트리 시험 3개가 실패함을 확인했다. **Windows 실행 시험은 로컬 미실행**이며 CI `windows-2025` 결과가 나오기 전까지 Windows 동작을 검증됐다고 표시하지 않는다. RHEL 8.10 실기 시험도 미실행이다.

## 7. 남은 제한

- cgroup v2/systemd scope 및 Job 메모리·CPU 한도 적용은 미구현(`Enforced=false`).
- 출력 무응답 감지, spool quota, 셸 프로파일, 실행 파일 버전·digest 기록은 W08 이후 분석기 계약에서 다룬다.
- Windows graceful 종료(CTRL_BREAK 등)는 없다. 서비스 종료 시 Job 정리 시험은 W06 agent에서 수행한다.
- 종료 불가 상태(D state 등)의 주 프로세스는 reap 대기 goroutine이 남을 수 있으며 결과는 `uncertain`이다.

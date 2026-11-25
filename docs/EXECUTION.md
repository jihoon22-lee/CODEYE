# 실행

[저장소 코드 실행 권한](#신뢰와-실행-권한)과 [기존 테스트 실행](#기존-테스트-실행)은 분석 프로필이 실제로 하는 일을 설명한다. [Native 실행기](#native-실행기), [노드 에이전트](#노드-에이전트), [노드 프로토콜](#노드-프로토콜), [워커 풀과 자동 증감](#워커-풀과-자동-증감), [예약](#예약)에서 실행 세부를 확인한다. 상태 전이와 데이터 모델은 [아키텍처](ARCHITECTURE.md), 서비스 시작과 백업은 [운영](OPERATIONS.md), 노드 경로의 HTTP 계약은 [API](API.md)에 있다.

## 객체와 범위

- 노드는 실제 PC나 서버다. 에이전트는 노드의 상주 관리자다. 워커는 한 번에 한 run attempt를 실행하는 프로세스다.
- 에이전트는 controller에 outbound API 연결을 만든다. SSH는 쓰지 않는다. 노드와 워커는 DB나 전체 프로젝트 자격증명에 접근하지 않는다.
- 노드는 등록 토큰이 정한 pool 하나에 속한다. pool은 OS·도구·신뢰 수준·소스 접근 특성이 같은 용량 묶음이다.
- 자동 증감은 등록된 노드 안에서 워커를 만들고 줄인다. VM 생성·삭제(CapacityProvider)는 설계 계약(미구현)이다.
- Native 실행이 기본 제품 경로다. 특정 분석 도구가 그 OS를 지원하지 않으면 그 도구만 `unsupported`로 표시한다. Windows 사용자에게 WSL이나 Docker를 요구하는 것으로 Native 목표를 대신하지 않는다. WSL은 별도의 Linux 노드로 등록할 수 있다.
- 실행기는 Linux와 Windows만 지원한다. 다른 OS는 `platform_unsupported`로 실행하지 않는다.

## 신뢰와 실행 권한

- 정적 분석도 JS 설정, build script, compiler plugin, proc macro 등을 실행할 수 있다. "정적"이라는 이름만으로 무실행으로 분류하지 않는다. 지금 허가를 받아야 하는 정적 분석 쪽 실행은 Rust cargo check·clippy·rustdoc과 C/C++ compile DB 생성(CMake configure, [compile DB 생성](ANALYZERS.md#compile-db-생성))이다. 이 작업들은 시스템 C 도구를 이름으로 찾으므로 자식 `PATH`에 `--toolchain-path`가 들어간다.
- Native에서 실행되는 자식은 실행 계정의 권한을 가진다. 디렉터리 복사, venv, 환경변수 allowlist는 보안 샌드박스가 아니다. 신뢰 경계가 다른 프로젝트는 별도 계정·노드를 쓴다.
- 같은 OS 사용자로 도는 분석 도구는 `<state-dir>/node.json`(노드 비밀값)을 읽을 수 있다. 파일 권한 0600은 같은 사용자를 막지 못한다. 실행 계정 분리는 운영 배치에서 한다.
- 저장소 코드 실행에는 프로젝트의 `run_build_scripts`, 실행자에 대한 서비스의 허용 범위, 노드의 실행 허가를 모두 적용한다. 프로젝트 기본값은 로컬 폴더는 켬, Git 저장소는 끔이다. 관리자가 시작한 실행에는 다른 사용자용 서비스 스위치를 적용하지 않는다.
- `codeye up`이 띄운 노드는 `--project-analysis-policy`로 로컬·Git 모두에서 프로젝트 설정을 적용한다. 운영자가 직접 시작한 기본 노드는 `--repo-code-source-root`를 지정한 로컬 루트에서만 허용한다. 저장소 설정과 PR 내용은 이 범위를 높일 수 없다. 허가가 없으면 `unsupported`(`repository_code_not_allowed`), 서비스가 실행자에게만 제공하지 않은 경우 `repository_code_not_offered`다. [분석기 허가 집행](ANALYZERS.md#저장소-코드-실행-허가)
- 검사기·플러그인·설치 스크립트·빌드·기존 테스트를 개별적으로 승인하는 상세 권한 정책은 설계 계약이다.
- 선택 Container·VM 실행기는 설계 계약(미구현)이다. 같은 실행 계약에 이미지 digest·mount·network·user·runtime 필드를 더한다. 모든 실행을 Container로 옮겨야만 쓸 수 있게 만들지 않는다. 컨테이너에서도 원본 쓰기 mount·운영 secret·무제한 자원을 기본 허용하지 않는다. 컨테이너 엔진이 있다고 네트워크 격리와 자원 제한이 적용된다고 보지 않고 실제 능력을 보고한다.

## 기존 테스트 실행

`with-tests`는 정적 분석 뒤 기존 테스트를 실행한다. 모델 검토를 켜면 독립 조사·도구 해석과 후보 검증 사이에 테스트를 배치한다. 검증 역할은 제시한 테스트 발췌와 실행 위치가 겹칠 때 해당 케이스의 결과를 함께 읽는다. 워커는 [저장소 코드 실행 허가](#신뢰와-실행-권한)를 적용하고, 고정된 스냅샷에서 찾은 테스트를 attempt 작업 사본에서 실행한다. 실행마다 새 home·temp를 사용한다. 선택 범위에 실행할 테스트 진입점이 없으면 `test.unavailable`에 `test_entries_unavailable`을 기록한다.

| 대상 | 실행 범위와 명령 |
|---|---|
| Go | 범위에 `_test.go`가 있는 모듈에 `go test -json -vet=off -count=1`과 모듈·패키지 패턴을 넘긴다. `go mod edit -json`으로 사본 밖 모듈 경로를 먼저 확인한다. `go vet`은 별도 정적 분석 작업이다. 운영자는 `-run`의 Go 정규식으로 하위 테스트를 고르고 `-race`를 켤 수 있다. |
| JS/TS | `package.json`에 선언된 `test:unit` 또는 `test`, `test:module` 또는 `test:integration`, `test:e2e` 또는 `e2e`를 `npm run`으로 실행한다. 동일한 명령을 한 번만 실행하며 전용 스크립트를 호출하는 묶음 `test`도 제외한다. Jest·Vitest·Playwright를 직접 실행하는 스크립트에 JSON reporter를 지정하고, `node --test`에서는 TAP을 읽는다. 다른 스크립트의 실행과 종료 코드는 기록되며 개별 테스트 근거가 없으면 `test_results_unavailable`로 끝난다. |
| Python | `test*.py`·`*_test.py`가 있는 모듈에서 pytest 설정·`conftest.py`·pytest 테스트 선언이 보이면 pytest와 JUnit XML을 사용한다. 다른 모듈은 `python -B -m unittest discover --verbose`를 쓴다. pytest는 다른 Python 모듈의 하위 디렉터리를 제외하고 프로젝트의 수집 설정을 따른다. 두 방식 모두 준비된 해당 모듈의 site-packages를 읽을 수 있다. |
| C/C++ | `enable_testing()` 또는 `include(CTest)`와 정적으로 확인할 수 있는 테스트 등록이 있는 CMake 프로젝트를 configure·build한 뒤 CTest를 실행한다. `add_subdirectory`로 포함된 하위 디렉터리는 상위 빌드와 함께 실행한다. 구성·빌드가 실패하면 `test_preparation_failed`이고 종료까지 도달한 CTest의 JUnit XML로 건수를 센다. |
| Java | `src/test`·`src/integrationTest`의 Java 테스트를 포함하는 Maven reactor 또는 Gradle 빌드 루트를 한 번 실행한다. Maven은 오프라인 `verify`로 Surefire 테스트와 POM에 연결된 Failsafe 테스트를 실행한다. Gradle은 오프라인 `test`를 실행한다. 빌드 설정의 provider가 JUnit 4·5 또는 TestNG를 실행해 표준 XML을 써야 테스트를 확인할 수 있다. |
| Rust | Cargo 모듈에서 `cargo test --workspace --offline`을 실행한다. `Cargo.lock`이 있으면 `--locked`를 추가한다. 모듈에 `.config/nextest.toml`이 있으면 nextest가 일반 테스트를 한 번 실행하고, 라이브러리 소스가 있으면 `cargo test --doc`이 문서 예제를 별도로 실행한다. 운영자가 설치한 `cargo-nextest`가 필요하며 nextest 재시도는 끈다. |

Go 선택과 race를 설치에서 지정하는 필드는 `agent.tests.go_run`과 `agent.tests.go_race`다. 예를 들어 `go_run: "^TestConfig$/^missing$"`은 선택한 패키지에서 `TestConfig/missing`을 요청한다. 같은 기능을 별도 노드에서는 `--test-go-run REGEXP`와 `--test-go-race`로 지정한다. race는 이를 지원하는 Go toolchain과 노드의 C 컴파일러가 필요하다. Java는 JDK와 `mvn` 또는 `gradle`이 필요하다. CMake·CTest, Maven·Gradle, nextest는 노드의 `agent.toolchain_path`에서 찾는다. 오프라인 Java 의존성과 플러그인이 필요하면 준비한 캐시 디렉터리를 `agent.tests.maven_repository` 또는 `agent.tests.gradle_home`으로 지정한다. 별도 노드의 플래그는 `--test-maven-repository DIR`와 `--test-gradle-home DIR`다. Maven·Gradle wrapper가 빌드 도구를 내려받는 경로는 사용하지 않는다. SpotBugs의 정적 컴파일에 쓸 JAR의 출처와 해시는 별도의 [Java 로컬 클래스패스](ANALYZERS.md#java-로컬-클래스패스)에서 설정한다.

JS/TS·Python·Rust·Java·CMake에서 모듈이나 빌드 루트 전체가 필요한 작업은 파일 선택만으로 실행 범위를 넓히지 않는다. Playwright의 브라우저가 필요한 테스트는 노드에 브라우저와 실행에 필요한 라이브러리가 준비되어 있어야 한다.

모든 테스트 명령은 Linux의 사용자·네트워크·PID namespace 안에서 실행한다. 노드에 `unshare`, `ip`, `sh`가 필요하며 해당 계정으로 namespace와 그 `/proc`을 만들 수 있어야 한다. `test.isolation` 확인 뒤 테스트 명령의 namespace에서 loopback 인터페이스만 올린다. 호스트의 IP 루프백 서비스와 외부 IP 라우트는 이 네트워크에 없다. 테스트 프로세스가 끝나면 PID namespace에 남은 프로세스도 종료한다. 전제 조건을 충족하지 못한 노드에서는 `test_isolation_unavailable`로 남긴다. 파일과 파일 경로의 UNIX 소켓은 여전히 실행 계정 권한 범위에서 접근 가능하므로 신뢰한 소스에만 실행을 허용한다. 실행 환경에 브라우저·데이터베이스·외부 서비스 등 별도 테스트 구성요소를 설치하거나 시작하는 기능은 제공 범위 밖이다.

각 테스트 명령은 최대 10분이며 stdout 8 MiB와 stderr 2 MiB를 기록한다. 완료한 테스트 중 통과한 항목이 하나 이상 있고, 실패가 없으며, 프로세스가 정상 종료한 경우에 작업을 성공으로 기록한다. 실행 결과가 비어 있거나 전부 건너뛴 경우는 `no_tests_executed`다. 확인할 수 있는 reporter 산출물이 없으면 `test_results_unavailable`다. 취소·제한시간·정리 불확실 상태는 기존 실행기 규칙을 적용한다. Java XML은 실행 전에 있었던 표준 보고서의 해시와 대조해 새로 작성되거나 내용이 바뀐 파일만 읽는다.

[테스트 결과 산출물](../schemas/test-results.schema.json)은 실행·시도·스냅샷·작업 키·`test.run`의 stdout·stderr SHA-256에 묶인다. reporter를 읽어 확인한 통과·실패·건너뜀 건수와 최대 128개의 예를 남기며 실패 예를 우선한다. 코드 위치가 있는 경우 스냅샷과 작업 사본의 SHA-256을 대조한다. Go의 하위 테스트는 같은 상태의 상위 테스트와 중복 집계하지 않고 유일한 리터럴 `t.Run` 위치를 확인한 경우만 그 줄을 연결한다. Java는 고유한 선언을 찾을 수 있는 경우 위치를 연결한다. 다른 이름·위치가 오거나 파일이 바뀐 경우에도 확인한 건수와 작업 상태는 남긴다. [합성 예시](../examples/test-results.json)

Go 작업이 0이 아닌 종료 코드로 끝나고 정리와 출력이 확인되면 `test.run`의 기존 `go test -json` stdout에서 최상위 테스트의 시작·실패 이벤트와 그 패키지의 실패를 함께 확인한다. 모듈 경로는 앞서 실행한 `go mod edit -json`에서 가져온다. 실행 패턴에 포함된 패키지 안에서 이름이 유일한 `func Test…(*testing.T)`를 찾아 고정 스냅샷의 파일 해시와 작업 사본을 대조한다. 같은 이름의 대체 구현이 있거나 선언이 빌드 조건에 따라 달라지는 파일에만 있으면 위치는 기록하지 않는다. 선언 위치는 테스트 함수의 실제 파일 줄이다.

상세 근거는 [Go 테스트 실패 산출물](../schemas/go-test-failures.schema.json)에 최대 64건을 기록한다. 보고서 빌더는 같은 `test.run` stdout의 SHA-256과 실패 이벤트의 바이트 범위를 다시 대조한다. 산출물에는 stderr, `Output` 메시지, 하위 테스트 이름이 들어가지 않는다. 다른 패키지가 오프라인 의존성으로 실패해도, 실행한 패키지에서 확인한 테스트 실패를 함께 기록하고 작업 사유는 유지한다. 이름을 확인할 수 없는 컴파일 실패, 취소·시간 초과·출력 잘림과 읽기 한도 초과는 작업 결과를 그대로 유지한다. 선언 선별에는 패키지의 테스트 파일을 최대 512개·32 MiB까지 사용하며 파일당 1 MiB를 읽는다. 이 근거는 기존 실행에서 추출한다. [합성 예시](../examples/go-test-failures.json) · [보고서의 테스트 결과](REPORT.md#테스트-결과)

## Native 실행기

코드: [`internal/worker/executor`](../internal/worker/executor). zero value `executor.Executor`를 쓰며 동시 `Run`이 안전하다. 분석기는 `executor.Runner` 인터페이스에 의존한다. 결과는 `Result` 하나이고 오류를 따로 반환하지 않는다.

```go
var e executor.Executor
res := e.Run(ctx, executor.Spec{
    Executable:     "/opt/tools/bin/cppcheck", // 절대 경로, PATH 검색 없음
    Args:           []string{"--xml", "src"},  // argv[1:], 그대로 전달
    WorkspaceRoot:  workCopy,                  // 승인된 작업 사본
    Dir:            workCopy,                  // realpath가 WorkspaceRoot 안
    ProtectedRoots: []string{snapshotPath},    // 스냅샷·원본: 겹치면 거부
    Env:            map[string]string{"PATH": "/usr/bin:/bin", "LANG": "C.UTF-8"},
    Timeout:        10 * time.Minute,
})
if res.RecoveryRequired() { /* attempt를 recovery_required로 */ }
```

### 실행 명세

| 필드 | 규칙 |
|---|---|
| `TaskID`, `AttemptEpoch` | 결과 대조용으로 그대로 복사한다. |
| `Executable` | 절대 경로, 존재, 일반 파일, Unix 실행 비트. PATH를 검색하지 않는다. |
| `Args` | argv[1:]. 각 원소를 shell 없이 그대로 전달한다. NUL이 든 인자는 거부한다. |
| `WorkspaceRoot`, `Dir` | 절대 경로. symlink 해석 뒤 `Dir`은 `WorkspaceRoot` 안이어야 한다. |
| `ProtectedRoots` | snapshot·원본 경로. workspace·temp와 겹치면 거부한다. 호출자가 넘기지 않으면 실행기는 그 경로를 알 수 없다. |
| `TempParent` | 실행별 home·temp를 만들 위치(기본 OS temp). `ProtectedRoots`와 겹치면 거부한다. |
| `Env` | 실행기가 정하는 home·temp 외의 전체 환경. 부모 환경을 상속하지 않는다. |
| `Stdin` | 바이트. nil이면 null device다. |
| `Timeout` | wall clock 한도. 필수이며 0보다 커야 한다. |
| `Grace` | Linux SIGTERM → SIGKILL 간격. 0이면 기본 2초. 즉시 강제 종료를 요청하는 값은 없다. |
| `StdoutLimit`, `StderrLimit` | stream별 캡처 상한. 기본 1 MiB, 최대 256 MiB. |
| `SensitiveArgs`, `Redact` | 기록에서 가릴 인자 위치와 값. |
| `Resources` | 자원 요청(예약·추정). `RequireEnforced`이면 실행하지 않는다. |
| `OnStarted` | 시작 사후 알림(journal용). |

- 요청 실행 파일(상위 symlink 해석 포함)이나 그 해석 대상이 `ProtectedRoots` 안이면 `executable_in_protected_root`로 거부한다. snapshot 안의 symlink가 바깥을 가리켜도 거부한다. 이것은 위생 규칙이며 OS 격리가 아니다.
- 실행은 해석된 파일로 하고 argv[0]은 요청 alias를 유지한다(multi-call 도구 호환). alias 교체 race를 줄일 뿐 샌드박스는 아니다. 경로 검증 뒤 symlink 교체 TOCTOU는 남는다.
- Windows `.bat`·`.cmd`는 암묵적 `cmd.exe` 해석 때문에 `batch_script_unsupported`로 거부한다.
- Windows 경로 비교는 대소문자를 무시한다.
- 셸이 필요한 경우를 위한 명시적 셸 프로파일(Linux 승인 bash·sh, Windows 승인 PowerShell·Git Bash)은 설계 계약(미구현)이다. 사용자 로그인 셸을 자동 상속하지 않는다. 직접 실행 가능한 검사기는 셸 없이 실행한다.
- PATH에 있는 같은 이름의 프로그램을 신뢰하지 않는다. 실행 파일의 버전·digest 기록은 분석기 팩이 맡는다([분석기](ANALYZERS.md)).

### 환경변수

- 부모 환경을 상속하지 않는다. `PATH`가 필요하면 호출자가 명시한다. Windows `os/exec`는 `SYSTEMROOT`가 없으면 부모 값을 더한다.
- `HOME`, `USERPROFILE`, `TMPDIR`, `TMP`, `TEMP`는 실행기가 실행별 `codeye-exec-*/home|tmp`로 정하며 호출자가 덮어쓸 수 없다.
- 자격증명형 키는 값과 무관하게 거부한다.
  - 정확한 이름: `SSH_AUTH_SOCK`, `SSH_AGENT_PID`, `SSH_ASKPASS`, `GIT_ASKPASS`, `DATABASE_URL`, `PGPASSWORD`, `PGPASSFILE`, `PGSERVICE`, `PGSERVICEFILE`, `PGSSLKEY`, `KUBECONFIG`, `NETRC`, `GOOGLE_APPLICATION_CREDENTIALS`, `DOCKER_CONFIG`, `NPM_CONFIG_USERCONFIG`.
  - 포함 문자열: `TOKEN`, `SECRET`, `PASSWORD`, `PASSWD`, `API_KEY`, `APIKEY`, `PRIVATE_KEY`, `CREDENTIAL`, `ACCESS_KEY`, `DATABASE_URL`, `_DSN`.
  - 접두어: `AWS_`, `AZURE_`, `CODEYE_DB`, `ANTHROPIC_`, `OPENAI_`, `OPENCODE_`.
- 기본 환경에는 최소 시스템 경로, 언어·locale, 명시적 미러·CA, task 전용 temp만 둔다. 운영 DB URL, 사용자 SSH agent, 클라우드 토큰, Git 댓글 토큰을 전달하지 않는다. 환경변수 제거만으로 원래 계정 비밀에 대한 OS 파일 접근까지 막았다고 하지 않는다.
- 모델 실행(OpenCode)은 같은 실행기를 쓰며 loopback relay에만 일회용 토큰을 준다([검토](REVIEW.md)).
- 실행기는 네트워크를 막거나 제한하지 않는다. 내려받지 않는 분석기는 `GOPROXY=off` 같은 도구 설정으로 오프라인을 지킨다. 의존성 받기는 레지스트리 주소를 argv·환경변수로 정할 뿐이며, 도구가 다른 주소로 접속하지 않는다는 것은 도구 설정에 기대고 플랫폼이 강제하지 않는다([의존성 준비](ARCHITECTURE.md#의존성-준비)).

### 실행별 파일

- 각 task는 temp·home·cache·build 경로를 명시적으로 받는다. 공유 cache는 toolchain·OS·lock digest·권한 namespace로 나누고 lock을 건다.
- readonly 입력을 요구하는 도구에 autofix를 켜지 않는다. 생성 artifact와 source drift를 기록한다.
- 실행별 temp는 정리가 확인되면 지운다(Windows 공유 위반에 대비해 짧게 재시도). 정리가 불확실하거나 삭제에 실패하면 남기고 `TempRetained`를 기록한다.

### 출력

- 실행기 소유 pipe의 `*os.File`을 자식에게 넘긴다. 별도 reader가 상한 이후에도 계속 읽어 자식이 막히지 않게 한다. 상한을 넘은 출력은 세기만 하고 버린다.
- `Stdout`·`Stderr`는 `Data`, 전체 관찰 바이트, `Truncated`, `Incomplete`(EOF 미도달, 그때까지 캡처한 데이터는 유지)를 가진다.
- 결과 parser가 필요로 하는 출력이 잘렸으면 파싱 성공으로 처리하지 않는다.
- spool quota와 출력 무응답 감지는 설계 계약(미구현)이다. wall clock timeout, 출력 무응답, 연결 상실은 서로 다른 이벤트다. 조용한 컴파일을 로그가 없다는 이유로 실패시키지 않는다.

### 마스킹

- `Command` 기록에는 요청 실행 파일 alias, 실제 실행한 해석 경로(`ResolvedExecutable`), 마스킹된 argv, cwd, 환경변수 **키 이름만**, temp 경로가 남는다.
- `SensitiveArgs` 위치의 인자는 `[REDACTED]`로 바뀐다.
- `Redact` 값과 민감 인자 값은 중복을 없앤 뒤 긴 값부터 기록 executable·argv·cwd·temp 경로와 캡처한 stdout·stderr에서 치환한다. 그래서 다른 비밀값을 포함하는 값의 꼬리가 남지 않는다.
- stream이 `Truncated`나 `Incomplete`이면 끝부분 중 비밀값의 진접두사와 일치하는 바이트를 버린다(짧은 접두사도 보수적으로 제거).

### 결과 상태

| `Result.Status` | 의미 |
|---|---|
| `exited` | 주 프로세스가 스스로 종료했다. `ExitCode`(Linux는 `Signal`도)로 해석하며 성공을 뜻하지 않는다. |
| `timed_out` | `Spec.Timeout`이 지나 트리를 종료했다. |
| `cancelled` | ctx 취소(`context_cancelled`) 또는 ctx deadline(`context_deadline`). |
| `rejected` | Spec 검증 실패. 프로세스를 만들지 않았다. |
| `start_failed` | OS가 실행을 거부했다(exec format, 권한 등). |
| `unsupported` | 강제 자원 한도 요구, Job 연결 실패, 미지원 OS 등 능력 부족. 사용자 코드를 실행하지 않았다. |

- `Cleanup`은 실행기 전용 값이다: `confirmed`(격리 단위가 비었음을 관찰), `uncertain`(recovery_required), `not_started`(자식이 생성되지 않음). DB·노드 프로토콜의 `cleanup_state`와 같은 enum이 아니다. 워커가 소유한 다른 프로세스의 불확실성까지 합쳐 `confirmed`/`uncertain`으로 바꾼다.
- `Reason`은 안정 코드다. `CleanupNote`는 `processes_remain_after_kill`, `escaped_descendant_alive`, `output_pipe_held_open`, `main_process_not_reaped`, `proc_scan_failed`, `job_query_failed` 등이다.
- `LeftoverKilled`는 주 프로세스가 끝난 뒤 남은 자손을 실행기가 종료했음을 뜻한다.
- `Containment`는 `linux_session_process_group`, `windows_job_object`, `none`이다.

### OnStarted

`OnStarted func(ctx, ProcessIdentity{PID, StartTime}) error`는 journal용 사후 알림이다.

- 프로세스가 격리 단위 안에서 시작된 뒤(Windows는 resume 뒤) 호출된다. 이미 코드가 실행됐을 수 있으므로 callback 실패가 "아무 코드도 실행되지 않았다"는 뜻이 아니다. 그래서 워커는 job 전달 전에 journal에 기록한다.
- timeout을 먼저 건 뒤 별도 goroutine에서 호출한다. 느리거나 멈춘 callback은 timeout·취소 처리를 막지 않는다.
- ctx는 timeout, run ctx 종료, `Run` 반환 중 하나가 일어나면 취소된다. ctx 종료로 생긴 오류는 무시하고 timeout·cancel로 분류한다.
- 오류를 반환하거나 panic하면 트리를 종료하고 `cancelled`/`on_started_failed`(`on_started_panic`)로 보고한다. 오류 문자열은 Detail에 넣지 않는다.
- ctx를 무시하고 I/O에 멈춘 callback goroutine은 `Run` 반환 뒤에도 남을 수 있다.

### Linux 프로세스 종료

- `Setsid`로 새 session·process group(sid = pgid = pid)을 만든다.
- 주 프로세스는 `waitid(WNOWAIT)`로 종료만 관찰하고 reap하지 않는다. 그래서 PID·PGID 번호가 재사용되지 않은 상태에서 group에 신호를 보낸다.
- 종료 순서: SIGTERM → grace(기본 2초) → SIGKILL 반복 → `/proc` 재조사. 고정된 PGID로의 TERM·KILL은 `/proc` 조사 성공 여부와 무관하게 먼저, 반복해서 보낸다.
- `/proc`는 같은 session의 다른 group 구성원 탐색, 탈출 탐지, 비었음 확인에만 쓴다. `/proc` 디렉터리나 PID별 stat 읽기에서 ENOENT·ESRCH(이미 종료) 외 오류(EMFILE, EACCES 등)가 한 번이라도 나면 `proc_scan_failed`로 `uncertain`이다.
- pgrp나 sid가 같은 살아 있는 프로세스가 없고 주 프로세스가 끝나야 `confirmed`다. reap 뒤 `kill(-pgid, 0)`이 ESRCH인지 다시 확인한다.
- 주 프로세스가 정상 종료해도 group·session 구성원이 남으면 같은 절차로 종료하고 `LeftoverKilled`를 기록한다.
- 트리에서 session·group을 떠난 자손(setsid 등)을 발견하면 종료하지 않고 PID와 starttime으로 생존을 확인해 `escaped_descendant_alive`로 `uncertain`을 남긴다. 그 자손이 출력 pipe를 쥐고 있으면 `output_pipe_held_open`도 남긴다.
- 부모가 먼저 죽은 뒤 setsid하고 fd를 닫은 daemon은 관찰할 수 없다. 그래서 `confirmed`는 "process group·session이 비었음"이지 "모든 자손이 없음"이 아니다.
- 종료할 수 없는 상태(D state 등)의 주 프로세스는 reap 대기 goroutine이 남을 수 있고 결과는 `uncertain`이다.
- 공유 서버에서 다른 사용자의 프로세스를 종료하지 않는다. PID 재사용을 고려하지 않은 kill은 금지한다.

### Windows 프로세스 종료

- `CREATE_SUSPENDED`로 만들고 → 전용 Job Object(`KILL_ON_JOB_CLOSE`, `DIE_ON_UNHANDLED_EXCEPTION`, `BREAKAWAY_OK` 없음)에 할당하고 → 단 하나의 주 스레드를 suspend count 1에서 resume한다. 그래서 Job 밖에서 자식이 생기는 race가 없다.
- Job 생성·할당·resume 중 하나라도 실패하면 suspended 프로세스를 종료하고 `unsupported`로 보고한다. Job 밖에서 실행하지 않는다.
- timeout·취소는 graceful 단계 없이 `TerminateJobObject` 후 `ActiveProcesses == 0`과 주 프로세스 종료를 기다린다. 주 프로세스가 끝난 뒤 Job에 남은 프로세스도 종료한다. Job handle을 닫으면 남은 프로세스는 OS가 종료한다.
- Windows graceful 종료(CTRL_BREAK 등)는 없다.
- PowerShell 종료만 확인하고 손자 빌드 프로세스가 끝났다고 처리하지 않는다. OS 접근 권한 격리는 Job Object만으로 제공되지 않는다.

### 자원

| 값 | 의미 | 현재 |
|---|---|---|
| reservation | scheduler가 할당한 CPU·메모리·disk·동시 작업 예산 | 배정 회계에 사용([자원 회계](#자원-회계)) |
| enforced_limit | OS가 실제로 적용한 한도 | 없음(`Enforced`는 항상 false) |
| observed_usage | 수집한 실제 사용량 | Linux: 주 프로세스와 reap된 자식의 rusage CPU·max RSS(`main_process_rusage`). Windows: Job 전체 CPU·최대 메모리(`job`) |
| estimated_requirement | 계획 단계의 예상 필요량 | `Spec.Resources`(CPU milli, memory bytes)를 `Result.Resources.Requested`에 그대로 남김 |

- cgroup v2·systemd scope·Job의 CPU·메모리·pids 한도 적용은 설계 계약(미구현)이다. 그때도 root 권한이나 호스트 cgroup 전환을 자동 요구하지 않고, 사용 가능한 기능을 probe한다.
- `RequireEnforced=true`이면 `unsupported`이고 실행하지 않는다.
- hard memory limit 없이 OOM을 예방한다고 주장하지 않는다. make·ninja·cargo·test의 내부 병렬도는 attempt 예산으로 제한해야 한다. 공유 머신의 운영자 reserve와 low priority·nice 선택은 설계 계약이다.

### 실행기 능력 표시

실행기 라이브러리의 구현과 각 머신의 실시간 능력 probe는 다르다. `doctor`는 프로세스 제어를 확인하는 항목을 두지 않는다. 바이너리가 빌드됐다는 이유로 그 계정에서 Job Object나 전체 프로세스 제어를 확인했다고 표시하지 않는다([운영](OPERATIONS.md#codeye-doctor)).

## 노드 에이전트

코드: [`internal/agent`](../internal/agent)(agent, client, journal, supervisor, reconcile, scaler, spawn), [`internal/agent/procid`](../internal/agent/procid), [`internal/workerproto`](../internal/workerproto), [`internal/worker/run`](../internal/worker/run), [`cmd/codeye/node.go`](../cmd/codeye/node.go). 노드 패키지는 저장 계층·pgx·HTTP 서버 패키지에 의존하지 않는다.

### 명령

```text
codeye agent --controller URL --state-dir DIR [--name NAME] [--enroll-token-file PATH]
             [--source-root ID=/abs/path ...] [--git /abs/git --git-host HOST ...]
             [--workers N | --autoscale [--max-workers N]] [--toolchain-path PATH]
             [--go /abs/go] [--go-mod-cache /abs/dir] [--<tool> /abs/path ...] [--node /abs/node]
             [--python-both-type-checkers] [--repo-code-source-root ID ...]
             [--dependency-fetch [--dependency-cache-dir DIR] [--dependency-timeout 10m]
              [--dependency-total-timeout 20m] [--dependency-max-bytes N]
              [--go-proxy LIST] [--go-sumdb NAME] [--go-private PATTERNS]
              [--go-noproxy PATTERNS] [--go-nosumdb PATTERNS] [--npm-registry URL]
              [--npm /abs/npm-cli.js] [--pnpm /abs/pnpm.cjs] [--yarn /abs/yarn.js]
              [--python /abs/python3] [--uv /abs/uv] [--pip-index-url URL]]
             [--opencode /abs/opencode --model PROVIDER/MODEL
              [--model-config-file PATH] [--model-credential-file PATH | --model-relay-file PATH]]
             [--model-timeout 180s] [--model-source-root ID ...]
             [--review-max-calls 15..207] [--review-parallel 1..16]
             [--storage-quota AREA=BYTES ...] [--storage-log-dir DIR]
codeye worker      # agent가 실행한다. 옵션 없음.
```

| 옵션 | 규칙 |
|---|---|
| `--controller` | https이거나 loopback 주소의 http만 허용한다. 프록시 환경변수를 쓰지 않고 redirect를 따르지 않는다. |
| `--state-dir` | 필수. 상대 경로는 절대 경로로 바꾼다. |
| `--enroll-token-file` | 첫 실행에 필요하다. 토큰 파일은 읽기만 하고 로그에 남기지 않는다. |
| `--source-root ID=/abs/path` | 반복 가능. 이 노드가 접근할 승인 source root. |
| `--git`, `--git-host` | 둘 다 있을 때만 Git 소스 능력을 알린다. |
| `--workers N` | 고정 워커 수(기본 1). `--autoscale`과 함께 쓸 수 없다. |
| `--autoscale`, `--max-workers N` | 워커 0개로 시작해 controller 명령을 따른다. 로컬 상한은 1~16, 기본 2. `--max-workers`는 `--autoscale`에서만 쓴다. |
| `--toolchain-path` | 워커 `PATH`(기본 `/usr/bin:/bin`). |
| `--go`, `--go-mod-cache`, `--<tool>`, `--node`, `--python-both-type-checkers` | 분석 도구와 공유 toolchain 경로. 절대 경로만 받고 PATH를 검색하거나 내려받지 않는다. 지정하지 않은 도구의 작업은 `unsupported`(`tool_not_configured`)로 끝난다([분석기](ANALYZERS.md)). |
| `--repo-code-source-root ID` | 그 local source root에서 저장소 코드를 실행하는 분석 작업을 허가한다. ID는 `--source-root` 매핑이 있어야 한다([분석기](ANALYZERS.md)). |
| `--dependency-fetch` 외 의존성 옵션 | 의존성 받기 운영자 스위치(기본 꺼짐)와 캐시·예산·Go 받기 설정, npm 레지스트리(`--npm-registry`)와 Python index(`--pip-index-url`), 패키지 관리자 경로(`--npm`·`--pnpm`·`--yarn`·`--python`·`--uv`, JS 관리자는 `--node`도 쓴다). 패키지 관리자는 hello `dependencies.package_managers`로 간다. 스위치가 꺼져 있으면 다른 의존성 옵션은 쓰이지 않는다. 켜면 hello `dependencies`로 워커에 전달되고, run마다 프로젝트 선택 `fetch_dependencies`도 켜져 있어야 계획한다([의존성 준비](ARCHITECTURE.md#의존성-준비), [분석기](ANALYZERS.md#에이전트-플래그)). |
| `--opencode`, `--model`, `--model-config-file`, `--model-credential-file`, `--model-relay-file`, `--model-timeout`, `--model-source-root` | 모델 실행, OpenCode JSON·JSONC 설정과 인증 파일(경로만 받고 워커가 읽는다), 호출당 제한시간(기본 600초, 1초~30분), root별 모델 전송 승인([검토](REVIEW.md#모델-설정)). 키 파일과 relay 파일은 함께 쓰지 않는다. |
| `--review-request-bytes N`, `--review-output-tokens N` | 모델 검토 요청 하나의 메시지 바이트(기본 262,144, 40,000~1,048,576)와 호출당 출력 토큰(기본 16,384, 1,536~65,536). 모든 역할의 코드 원문·단위·서술·후보 상한을 이 둘에서 계산한다. 범위 밖이면 사용 오류다. Hello `review_request_bytes`·`review_output_tokens`로 워커에 간다([검토](REVIEW.md#요청-크기)). |
| `--review-max-calls N` | attempt당 모델 검토 호출 상한(기본 99, 15~207). 상한을 다 쓰면 기본값 33일 때의 약 세 배 비용이 든다([안내서](GUIDE.md)). 범위 밖이면 사용 오류다. 전체 역할 다섯 개가 최대 15회를 쓰고 남은 호출로 모듈 단위 slice를 차례로 허용한다. Hello `review_max_calls`로 워커에 전달되고 run 요청으로는 바꿀 수 없다([검토](REVIEW.md#모듈-단위-검토-분할)). |
| `--review-parallel N` | attempt 안에서 함께 실행할 독립 검토 단위 수(slice 쌍, `review.contract`, 분류 쌍. 기본 4, 1~16). 1이면 하나씩 실행한다. 범위 밖이면 사용 오류다. Hello `review_parallel`로 워커에 가고, 이 필드가 없는 hello는 1이다. `--model-relay-file`을 쓰면 relay가 요청을 하나씩 처리하므로 1로 실행한다. 호출 수·비용·보고서는 병렬도와 관계없이 같다([호출 상한과 승인](REVIEW.md#호출-상한과-승인)). |
| `--storage-quota AREA=BYTES`, `--storage-log-dir` | 노드 저장 영역 상한(`snapshot`, `workspace`, `cache`, `logs`, 영역마다 한 번)([운영](OPERATIONS.md)). |

- 거부된 인자 값은 출력하지 않는다(자격증명이 들어 있을 수 있다).
- 등록 결과는 `<state-dir>/node.json`(0600, 디렉터리 0700)에 저장한다. 저장된 controller와 `--controller`가 다르면 요청을 보내기 전에 종료한다(exit 2). 노드 비밀값을 다른 controller에 보내지 않기 위해서다.
- `<state-dir>/agent.lock`에 배타 flock을 건다. 같은 state dir의 두 번째 agent는 journal·identity를 건드리기 전에 종료한다(exit 1).
- 로그는 stderr의 JSON 줄(`code`와 ID)이다. 비밀값·토큰을 쓰지 않는다. 상태 판단은 controller API로 하고 로그는 진단용이다.
- `SIGUSR1`(`codeye stop`)은 drain이다. 새 claim을 멈추고 실행 중 작업을 끝낸 뒤 정상 종료한다([운영](OPERATIONS.md)).

| 종료 코드 | 의미 |
|---|---|
| 0 | 정상 종료 |
| 1 | 실행 실패 또는 종료 시 정리 불확실 |
| 2 | 옵션 오류 |
| 3 | 등록 거부 |
| 4 | 노드 철회 |

### 동작 순서

등록 → journal 열기 → [재시작 복구](#재시작-복구) → journal 압축 → 워커 시작 → 노드 heartbeat(기본 5초).

- heartbeat는 관측한 워커 상태, 실제 여유 메모리(`MemAvailable`), state 파일시스템의 여유 disk, CPU 수, 능력, 명령 cursor, 정리 보고를 보낸다. 자동 모드는 `worker_scaling: controller`와 `max_workers`를, 저장 상한이 있으면 상한에 걸린 영역을 함께 보낸다. drain 중인 자동 모드 에이전트는 `worker_scaling`을 보내지 않는다(static). 그래서 controller의 증감 계획에서 빠지고 시작 명령을 받지 않는다.
- 능력:
  - `analysis:<profile>`은 준비된 워커가 `ready`에서 알린 handler에서만 만든다. handler가 없으면 claim을 보내지 않고 `no_analysis_handler`를 기록한다. drain 중에는 알리지 않는다.
  - 워커는 기준 분석기(언어별 필수 분석기)가 하나라도 쓸 수 있을 때만 `analysis:deep-review` handler를 알린다(`--advertise-without-baseline`이면 항상). 도구는 배정 기준이 아니다. 도구가 하나도 없는 노드는 일을 받지 않지만, 다른 언어의 도구만 있는 노드는 run을 받아 없는 도구의 작업을 `unsupported`(`tool_not_configured`)로 끝낸다. 같은 root를 맡는 노드에는 같은 도구를 둔다(A51).
  - `source:local:<id>`는 `--source-root` 매핑이 있는 root만 알린다.
  - `source:git:<host>`는 `--git`과 `--git-host`가 모두 있을 때만 알린다.
  - 능력 목록은 최대 128개다.
- claim은 마지막 heartbeat에서 `idle`로 보고한 워커 하나당 한 번만 한다. attempt가 끝나면 즉시 heartbeat를 보낸다. 워커는 자신의 `idle` 메시지를 받은 뒤에야 idle로 바뀐다.
- claim `Idempotency-Key`는 `<worker_id>:<agent 실행 nonce>-<counter>`다. HTTP 응답을 확정적으로 받을 때까지 같은 key를 다시 쓴다. 그래서 commit됐지만 응답을 잃은 claim도 replay로 회수한다.
- HTTP 요청 제한시간은 60초다. 연속 실패는 지수 backoff(최대 64초)로 재시도한다.

### 워커 프로세스

- argv는 `<codeye> worker`다.
- 환경은 `HOME`·`TMPDIR`·`TMP`·`TEMP`(`<state-dir>/workers/<id>/` 아래), `LANG=C.UTF-8`, `PATH`(`--toolchain-path`)만 쓴다. Windows는 `SystemRoot`를 더한다. 상속하는 값은 없다. 워커의 env·argv·IPC·journal에는 자격증명이 없다.
- Linux: 자체 process group(pgid = pid)으로 만들고 `PR_SET_PDEATHSIG=SIGTERM`을 건다. PDEATHSIG는 fork한 OS thread가 끝날 때 발동하므로 agent 수명 동안 유지하는 전용 locked thread에서 시작한다.
- Windows: suspended 워커를 전용 Job에 넣고 시작한다. 에이전트가 강제 종료되면 Job이 닫혀 워커와 자손이 종료된다. 재시작 뒤 이전 Job의 상태를 확인할 수 없으면 복구 상태를 유지한다.
- 시작 직후 `/proc` identity를 journal에 fsync하고 나서 `hello`를 보낸다. `hello` 뒤 `ready`까지 최대 60초를 기다린다(도구 probe가 이 사이에 실행된다).
- 워커의 stdin EOF나 SIGTERM은 실행 중 attempt를 취소하고, 정리 결과를 확인한 뒤 종료하게 한다.
- 제품 빌드의 handler registry에는 실제 분석기만 있다. 시험용 fixture handler(capability `analysis:deep-review`)는 `-tags codeye_fixture` 빌드에만 들어가며 분석 결과가 아니다.
- 워커 능력 probe(예: `go version`)는 attempt 전 워커 시작 때 실행하며 journal하지 않는다. 실행기 timeout으로만 제한된다.

### 에이전트–워커 IPC

- 프로토콜 `codeye-worker/0.1` JSONL이다. 한 줄은 1 MiB 이하다. unknown type·field는 프로토콜 오류이며 워커를 종료한다.
- 메시지 type: `hello`, `job`, `reply`, `stop_new_tasks`, `cancel`, `shutdown`, `ready`, `event`, `artifact`, `intent`, `process_started`, `process_ended`, `complete`, `idle`, `log`.
- reply 오류 코드: `stale_attempt`, `conflict`, `unavailable`, `integrity_mismatch`, `invalid`.
- intent 종류: `source_acquire_started`, `source_acquire_done`, `task_exec_started`. 취소 사유: `run_cancel_requested`, `lease_lost`, `agent_shutdown`.
- journal 성격의 메시지(`intent`, `process_started`, `process_ended`)는 읽기 goroutine에서 fsync한 뒤 바로 답한다. HTTP가 필요한 `event`, `artifact`, `complete`는 워커별 순서 큐에서 처리한다. 그래서 실행기 `OnStarted`(5초 제한)가 업로드 뒤에서 기다리지 않는다.
- snapshot 위치는 IPC 전용 필드로 agent 비공개 cache(`snapshots.jsonl`)에만 기록하고 서버에는 보내지 않는다.

### 산출물 수용

워커가 보낸 경로는 다음을 모두 만족해야 받는다.

- 절대 경로이고, 마지막 요소가 symlink가 아니고(`O_NOFOLLOW`), 일반 파일이다.
- 해당 워커 scratch 또는 snapshot data root 안이다.
- 64 MiB 이하다.

업로드하는 bytes로 hash를 다시 계산하고, 워커가 보고한 hash·크기와 다르면 거부한다. 그래서 도구가 만든 symlink로 `node.json`을 올릴 수 없다.

### 워커의 attempt 순서

1. intent `source_acquire_started`.
2. 소스 확보, 또는 고정 snapshot이면 `Load`+`CloneWorkspace`. 고정 snapshot은 다시 가져오지 않는다. 보유하지 않으면 `snapshot_unavailable`이다.
3. intent done → `source_manifest` artifact → `snapshot_fixed` 이벤트.
4. `Plan` → `task_created` × n → `run_advance running`.
5. task마다 `running` → handler 실행 → 작업 사본을 스냅샷 내용으로 되돌림 → stdout·stderr·artifact 업로드 → 종료 상태. 되돌리기는 바뀐 manifest 파일을 스냅샷에서 다시 쓰고 manifest에 없는 항목(숨김 이름·`node_modules`·`vendor`·`testdata` 제외)을 지운다. 바꾼 것이 있으면 그 task에 `workspace_restored_after_task`·`workspace_leftovers_removed`, 실패하면 `workspace_restore_failed`를 남긴다([분석기](ANALYZERS.md#실행-규칙)).
6. 취소·lease 상실이 없을 때만 `run_advance finalizing` → `result_manifest` artifact → `complete`.

### runner 감싸기

- 워커는 handler에게 감싼 runner를 넘긴다. 이 runner는 매 호출 전에 intent `task_exec_started`를 journal에 남긴다.
- `ProtectedRoots`에 snapshot과 승인 원본 root를 덧붙인다(덮어쓰지 않는다). `TempParent`, `TaskID`, `AttemptEpoch`, `OnStarted`를 설정한다.
- 결과는 모두 워커가 직접 기록한다. 정리 상태와 "종료했는가" 판정은 이 기록에서 나오며 handler가 주장할 수 없다.
- handler가 `succeeded`라고 해도 기록된 실행이 신호 종료·비종료·잘림·불완전·정리 불확실이면 `failed`로 낮춘다.
- 명령 argv·dir과 stdout·stderr는 handler가 명시적으로 돌려준 실행만 게시한다. 기록됐지만 반환되지 않은 실행은 `unreported`로 상태·정리만 남긴다(모델 출력 등 비공개 내용 보호).

### attempt 결과

- outcome은 구조화 상태로만 정한다. 사용자 취소는 `cancelled`, lease 상실·agent 종료는 `failed`(reason `lease_lost`·`agent_shutdown`)다. 필수 task가 모두 성공하면 `succeeded`, 일부만 읽을 수 있는 결과가 있으면 `partial`, 그 외는 `failed`다.
- result manifest는 `analysis.ResultManifest`로 만든다. `Validate`와 canonical JSON을 거치고, 노드 절대 경로는 workspace 상대 경로나 `[REDACTED]`로 바꾼다.
- cleanup은 실행·확보·intent 중 하나라도 불확실하면 `uncertain`이다. agent journal에 열린 intent가 있으면 워커가 `confirmed`라고 보고해도 `uncertain`으로 덮어쓴다.
- 완료 사유 코드: `snapshot_unavailable`, `source_unavailable`, `source_unstable`, `source_limit_exceeded`, `source_unsupported`, `snapshot_integrity_failed`, `analysis_unsupported`, `lease_lost`, `agent_shutdown`, `worker_lost`, `cancelled`, `handler_error`, `artifact_upload_failed`, `dependency_failed`, `actor_revoked` 등. 형식은 `^[a-z][a-z0-9_]{0,63}$`다.

### lease watchdog

- 마지막 갱신 성공 시점(요청 전송 시각, monotonic)부터 lease − margin(기본 30 − 10초)이 지나면 `stop_new_tasks`와 `cancel{lease_lost}`를 보낸다. 409 `STALE_ATTEMPT`를 받아도 같다. 노드 wall clock은 믿지 않는다.
- 워커의 `complete`는 항상 보낸다. agent의 판단이 서버 lease보다 먼저 오므로 서버가 아직 받으면 `failed/lease_lost`로 끝난다. 409이면 실제 정리 결과를 heartbeat `cleanup_reports`로 보낸다.
- 정리 보고는 수락되거나 최종 거부(`not_found`, `stale_attempt`, `invalid`, `uncertain_not_applied`)될 때까지 남긴다. `conflict`·`unavailable`(sweep 전이라 attempt가 아직 leased)은 heartbeat마다 최대 15분 다시 보낸다.
- attempt heartbeat 응답의 `cancel_requested=true`와 `cancel_attempt` 명령은 사용자 취소로 전달한다.

### 취소

취소는 desired state다. controller가 `cancel_requested`를 저장하고, 노드 heartbeat가 `cancel_attempt` 명령을 만든다. agent는 모델 호출과 task 시작을 멈춘 뒤 실행 그룹을 종료한다. 종료를 확인하면 `cancelled`로 보고하고, 확인하지 못하면 `recovery_required`다.

### journal

- `<state-dir>/journal.jsonl`(0600). 기록마다 fsync한다.
- 기록 종류: `worker_started`·`worker_exited`, `attempt_assigned`·`attempt_done`, `intent`, `process_started`·`process_ended`, `command_applied`, `cleanup_pending`·`cleanup_acked`, claim counter.
- 순서 규칙: 워커는 `hello` 전에 기록한다. attempt는 `job` 전에 기록한다. 하위 프로세스가 생길 수 있는 단계(소스 확보, 매 `Runner.Run`)는 시작 전에 intent를 기록하고, agent의 답을 받은 뒤에만 진행한다.
- 실행기 `OnStarted`는 실행이 시작된 뒤 호출된다. 그래서 journal은 실행을 막지 못하고 미확인 구간만 좁힌다.
- 마지막 줄이 잘린 journal은 잘린 부분을 버리고 `Torn`으로 표시한다. 그러면 미완료 attempt는 모두 uncertain이 된다.
- 압축은 시작 시에만 한다. 미완료 attempt가 참조하는 워커의 시작·종료 기록은 압축 뒤에도 남긴다.

### 재시작 복구

복구는 첫 heartbeat 전에 실행한다.

1. journal에서 종료 기록이 없는 워커를 찾아 pidfd로 고정한 뒤 identity를 확인하고 group을 종료한다.
2. 미완료 attempt는 다음을 모두 만족할 때만 `confirmed`다.
   - 워커가 종료로 기록돼 있거나, 이번 복구에서 사라졌음·종료됨을 확인했다. 기록이 없으면 확인으로 보지 않는다.
   - journal된 task process group이 모두 비었다.
   - 열린 intent가 없고, 확보가 `recovery_required`로 끝나지 않았고, journal이 잘리지 않았다.
3. 결과를 `cleanup_pending`으로 남기고 heartbeat `cleanup_reports`로 보낸다. attempt는 다시 실행하지 않는다.

process group 종료(`procid.TerminateVerifiedGroup`):

- `pidfd_open(leader)`로 leader를 먼저 고정한다. 그 뒤 `/proc`에서 start time과 pgid == leader를 확인하고 `pidfd_send_signal(PIDFD_SIGNAL_PROCESS_GROUP)`로만 신호를 보낸다. 순서는 TERM → grace(2초) → KILL → 비었는지 관측(최대 5초)이다. 확인과 신호 사이에 재사용된 PID·PGID를 맞힐 수 없다.
- start time이 다르면(재사용) 신호를 보내지 않고 uncertain이다.
- leader가 없으면 신호 없이, 그 pgid의 프로세스가 없을 때만 확인으로 본다.
- pidfd group 신호를 지원하지 않는 커널(Linux 6.9 미만, 예: RHEL 8·9 기본 커널)이면 uncertain이다. `kill(-pgid)`로 대체하지 않는다. 이 커널에서는 재시작 복구만이 아니라 실행 중 강제 종료(예: stdin EOF 뒤 끝나지 않는 워커의 group kill)도 막혀 uncertain으로 남는다.

### 에이전트의 알려진 한계

- `uncertain`으로 보고된 attempt는 agent가 다시 검사하지 않는다. 서버 예약은 사람이 정리를 확인할 때까지 잡혀 있다([lease와 만료](#lease와-만료)).
- claim이 commit된 뒤 `attempt_assigned` 기록 전에 agent가 죽으면 agent는 그 lease를 모른다. lease는 만료 뒤 `recovery_required`로 남고 정리 보고는 오지 않는다(이때 실행된 프로세스는 없다).
- 소스 확보의 git 하위 프로세스는 개별 journal하지 않는다. 확보 도중 agent가 죽으면 intent가 열린 채 남아 uncertain이 된다.
- 자동 snapshot cache GC와 노드 간 snapshot 전송은 없다. 오래된 snapshot·작업 사본은 운영자가 `codeye cleanup --agent-state-dir`로 지운다([운영](OPERATIONS.md#보존-정리)).

## 노드 프로토콜

경로·필드의 기준은 [OpenAPI](../api/openapi.yaml)의 노드 operation이며, route 대조 시험이 자동으로 검사한다. wire 타입은 [`internal/nodeproto`](../internal/nodeproto/nodeproto.go)(표준 라이브러리와 `internal/domain`만 import)에 있다. protocol version은 `0.1`이다.

| 경로 | 인증 | 용도 |
|---|---|---|
| `POST /api/v1/nodes/enroll` | 등록 토큰(본문) | 노드 등록. 노드 비밀값은 응답으로 한 번만 받는다. |
| `POST /api/v1/nodes/{node_id}/heartbeat` | Bearer | 관측·능력·정리 보고, 명령 수신 |
| `POST /api/v1/nodes/{node_id}/claims` | Bearer | attempt 배정(`Idempotency-Key` 필수). 200 또는 204(배정할 것 없음) |
| `POST /api/v1/attempts/{attempt_id}/heartbeat` | Bearer | lease 갱신, `cancel_requested` 수신 |
| `POST /api/v1/attempts/{attempt_id}/events` | Bearer | `snapshot_fixed`, `task_created`, `task_status`, `run_advance` |
| `PUT /api/v1/attempts/{attempt_id}/artifacts/{client_artifact_id}` | Bearer | 산출물 업로드 |
| `POST /api/v1/attempts/{attempt_id}/complete` | Bearer | attempt 완료 |
| `POST /api/v1/attempts/{attempt_id}/cleanup` | Bearer | 정리 확인(`confirmed`만) |

헤더: `Idempotency-Key`, `X-CODEYE-Worker-ID`, `X-CODEYE-Lease-Epoch`, `X-CODEYE-Artifact-Role`, `X-Content-SHA256`. 노드 오류 코드: `NODE_UNAUTHENTICATED`(401), `STALE_ATTEMPT`(409), `INTEGRITY_MISMATCH`(422), `PROTOCOL_UNSUPPORTED`(422). 공통 오류 형식은 [API](API.md)에 있다.

### 인증과 소유권

- `Authorization: Bearer <node_secret>`. 형식 오류·미등록·철회는 401 `NODE_UNAUTHENTICATED`다.
- 노드 경로는 cookie를 읽지 않고 CSRF를 쓰지 않는다. `Origin` header가 있으면 403 `ORIGIN_REJECTED`다(브라우저 요청 차단). Host allowlist는 사용자 경로와 같다. 사용자 경로는 Bearer를 받지 않는다. 본문은 `application/json`(UTF-8)이어야 한다.
- 경로의 node_id가 인증 노드와 다르거나 다른 노드의 attempt이면 404다.
- 같은 노드라도 다른 워커, 이전 epoch, 만료되거나 종료된 lease이면 409 `STALE_ATTEMPT`다. 모든 상태·artifact finalize·완료 요청은 attempt ID와 epoch를 포함한다.
- 오래된 raw artifact는 격리할 수 있으나 현재 보고서에 합치지 않는다. 이전 attempt가 늦게 도착해도 새 attempt를 덮지 않는다.

### 등록 토큰과 운영자 명령

```text
codeye node token create --pool NAME [--source-root ID[,ID...]] [--output .codeye/enroll-token] [--ttl 10m]
codeye node token revoke --id TOKEN_ID
codeye node list
codeye node revoke --id NODE_ID
codeye scheduler set [--global-max-running N] [--project-max-running N] [--artifact-quota-bytes N]
codeye pool set --name NAME [--min-workers N] [--max-workers N] [--idle-timeout-seconds N]
                [--scale-up-cooldown-seconds N] [--scale-down-cooldown-seconds N] [--expect-revision R]
codeye pool list
codeye serve [--artifact-dir .codeye/artifacts] [--reconcile-interval 5s] ...
```

- 등록 토큰은 32바이트 난수 base64url(43자)이다. 0600 파일에 같은 디렉터리 rename으로 쓴다. 기존 경로가 symlink 등 일반 파일이 아니면 거부한다. 표준 출력에는 token ID·pool·source root ID·만료만 쓰고 토큰은 쓰지 않는다. DB에는 SHA-256만 저장한다.
- 토큰은 일회용이다. TTL은 1분~24시간, 기본 10분이다. 사용·만료·철회된 토큰은 등록에 쓸 수 없다. 동시 등록에서도 한 건만 성공한다. 지원하지 않는 protocol version의 등록은 토큰을 소비하지 않는다. 사용된 토큰의 철회는 409이며, 그때는 노드를 철회한다.
- 등록 토큰의 `--source-root`가 그 노드가 claim할 수 있는 local root를 정한다(노드가 알린 능력과 둘 다 필요하다).
- pool은 이름으로 처음 쓸 때 만든다.
- `node revoke`는 credential을 즉시 무효화한다. 그 노드의 인증·heartbeat·claim이 바로 거부되고 실행 중인 에이전트는 작업을 취소한 뒤 종료 코드 4로 끝난다. 정리 보고를 보낼 수 없으므로 lease는 만료 뒤 `recovery_required`가 되고, 사람이 정리를 확인한다(`codeye run recover`, [lease와 만료](#lease와-만료)).
- `codeye run recover --id RUN_ID --confirm-cleanup [--config PATH]`은 `CODEYE_DATABASE_URL`의 DB(`--config`를 주거나 URL이 없으면 서비스 설정 파일의 DB)에서 `recovery_required` run의 정리를 운영자가 확인한 것으로 기록하고(감사 `run.recovery_confirmed`, 행위자 `operator_cli`) 요청자 대신 다시 대기열에 넣는다. `--confirm-cleanup`이 없으면 종료 코드 2다. 출력 JSON은 `confirmed_run`, `attempt_id`, `epoch`, `status`(`failed`), `retry`(`same_run`·`new_run`·`not_queued`), `run`, `not_queued_reason`이다.
- `scheduler set`: `--global-max-running`은 0..10000(초기값 2), `--project-max-running`은 1..10000(기본 1), `--artifact-quota-bytes`는 산출물 저장 상한(0이면 제거, [운영](OPERATIONS.md))이다.
- `serve`의 기본 artifact 디렉터리는 실행 위치 기준 `.codeye/artifacts`다.

### heartbeat와 명령

- 다른 노드의 워커 ID를 보고하면 409다. 보고되지 않은 워커는 `stopped`가 된다. cursor·형식을 검증한다.
- 워커 상태: `starting`, `idle`, `busy`, `draining`, `stopped`, `failed`.
- 명령 종류: `cancel_attempt`(run 취소에서 파생), `start_worker`, `drain_worker`, `stop_worker`(자동 모드 노드에만). `drain_worker`는 busy 워커가 attempt를 끝낸 뒤 나가게 하고, `stop_worker`는 idle 워커에만 보낸다.
- 명령은 `command_id`와 노드별 `sequence`를 가진다. sequence는 commit 순서대로 증가한다. agent는 `command_id`로 중복을 걸러 한 번만 적용한다. 취소 요청은 ack 전까지 `cancel_attempt` 한 건으로 다시 전달된다.
- 취소 명령은 `CancelRun`(project → run 잠금)이 노드 행을 잠그지 않고 저장하고, 노드 heartbeat가 노드 행 잠금 아래에서 만든다. 그래서 sequence 순서가 commit 순서와 같다.

### claim

- 마지막 heartbeat에서 `idle`로 보고됐고 활성(`leased`·`recovery_required`) attempt가 없는 워커만 claim할 수 있다. autoscaler가 이미 drain·stop을 명령한 워커도 거부한다. 그 외는 409 `CONFLICT`다.
- claim이 성공하면 서버가 워커를 `busy`로 표시한다. 다음 claim 전에 heartbeat로 idle을 보고해야 한다.
- 같은 `Idempotency-Key`와 같은 워커는 저장된 200·204를 그대로 받는다. 다른 워커는 409 `IDEMPOTENCY_CONFLICT`다. 저장 응답은 하루 뒤 정리한다.

claim 트랜잭션:

1. `scheduler_limits` 행을 잠그고 노드 행을 잠근다. 노드 철회, heartbeat 신선도(20초), 전역 활성 예약 수 < 상한, 노드 여유, 노드 저장 영역 상한, 산출물 저장 상한, pool 상한을 확인한다.
2. queued run을 공정 순서로 keyset page(200개)씩 끝까지 훑어 호환 run을 찾는다. 앞쪽의 비호환 run이 뒤 run을 굶기지 않는다. 프로젝트 동시 실행 상한에 걸린 프로젝트의 run은 건너뛴다.
3. 호환 조건: `analysis:<profile>` 능력. local이면 `source:local:<root>` 능력과 등록 토큰의 root 허가, 또는 controller와 같은 머신의 노드가 알리는 `source:controller`(모든 root). Git이면 `source:git:<host>` 또는 `source:controller`(승인된 모든 host). 고정 snapshot이면 그 노드의 `snapshot_locations`. 저장된 요청·소스가 현재 규칙으로 해석되지 않는 run은 배정하지 않고 그대로 둔다.
4. 후보의 project·run을 잠근 뒤 `queued` 여부와 미해제 attempt 부재를 다시 확인한다. 요청자가 비활성이거나 프로젝트 실행 권한을 잃었으면 `actor_revoked`로 run을 실패 처리하고 대기 task를 `skipped_policy`로 둔다.
5. attempt(epoch+1), `capacity_reservations`, run `preparing`, 워커 `busy`, 프로젝트 배정 시각, `attempt.leased` 이벤트, 멱등 응답을 같은 트랜잭션에 기록한다. 어느 단계든 실패하면 전부 rollback된다.

claim 응답의 job:

- local source는 `source_root_id`·`relative_path`만 보낸다. 서버는 노드 경로를 저장하지 않는다. `source:controller` 노드(`codeye up`이 띄운 에이전트, `--controller-sources`)에는 웹에서 더한 root도 쓸 수 있도록 controller의 root 경로(`root_path`)를 함께 보낸다. Git은 URL·branch·host를 보낸다.
- `analysis`는 프로젝트의 분석 선택, `offered`는 run을 시작한 사람에게 서비스가 허용하는 것(`{model_review, build_scripts}`, 활성 관리자는 둘 다 true, 아니면 관리자 스위치)이다. 프로젝트 선택을 따르는 노드는 둘의 교집합만 하고, `offered` 때문에 빠진 것은 보고서 한계 `model_review_not_offered`·`repository_code_not_offered`가 된다.
- Git fetch는 워커가 설정된 플랫폼 인증정보 파일에서 배정된 저장소 호스트의 토큰만 읽는다. 웹 요청자의 신원은 소스 인증정보 선택에 쓰지 않는다. [플랫폼 GitHub 계정](GITHUB.md#플랫폼-github-계정)
- 고정 입력 재시도는 `job.snapshot`(ID, manifest digest, kind, consistency, source SHA)을 함께 보낸다. 워커는 그것을 로컬에서 `Load`하고 다시 가져오지 않는다.
- `resources`는 그 claim이 잡은 예약이다(OS 제한이 아니다).

### 이벤트

- `snapshot_fixed`는 같은 attempt가 먼저 PUT한 `source_manifest` artifact(sha256 = `manifest_digest`)를 요구한다. kind·consistency 조합은 `local`/`stable_copy_best_effort`(source SHA null) 또는 `git`/`git_commit`(source SHA 40·64자리 hex)이다.
- `task_created`는 run 안의 key가 같으면 기존 task와 현재 version을 돌려준다. 선택 `stage`·`weight`의 기록 규칙은 [아키텍처](ARCHITECTURE.md)에 있다.
- `task_status`는 task의 `expected_version`을 요구한다. 오래된 version은 409다.
- `run_advance`는 `running` 또는 `finalizing`으로 옮긴다. `expected_version`은 선택이며 주면 현재 run version과 같아야 한다. 모든 응답에 `run_version`이 있다.
- 같은 `client_event_id`와 같은 canonical 본문은 저장된 응답을 다시 받는다. 본문이 다르면 409다.

### 산출물 업로드

- 최대 64 MiB, `Content-Length` 필수(없으면 411).
- role: `source_manifest`, `result_manifest`, `stdout`, `stderr`, `observations`, `raw_output`, `other`.
- bytes는 `<artifact-dir>/sha256/<aa>/<sha256>`에 저장한다. hash·크기가 다르면 잔여 파일을 남기지 않는다. 같은 크기의 변조 파일을 신뢰하지 않고 교체한다.
- 같은 client artifact ID 재전송은 같은 응답이다. 내용이 바뀌면 409다. lease가 만료된 뒤에는 새 업로드를 거부하고 기존 업로드의 재전송만 허용한다.
- 업로드가 중단되면 재전송한다. finalize 전에는 공개하지 않는다.
- artifact 파일은 DB 기록 전에 저장되므로 기록 실패 시 참조 없는 파일이 남을 수 있다(GC 없음). Windows에서는 디렉터리 fsync를 하지 않는다.

### 완료와 정리

- `succeeded`·`partial`은 이 attempt의 finalized `result_manifest` artifact가 필요하다. 같은 본문 재전송은 같은 202다.
- 완료 시 `cleanup_state=confirmed`면 예약을 해제하고, `uncertain`이면 `recovery_required`로 두고 예약을 유지한다.
- 성공한 attempt를 수용하면 run은 `finalizing`으로 가고, 서버가 보고서를 게시하며 종료 상태를 정한다.
- 정리 확인 방법: `process_group_empty`, `job_object_closed`, `no_process_started`.
- 보고서 리비전은 attempt에 묶이고 `(run_id, revision)`·`(run_id, input_digest)`로 유일하다. 같은 완료 메시지 재전송은 같은 응답이다.

### lease와 만료

- lease는 30초, 노드·attempt heartbeat는 5초 간격이다. 이 값은 등록 응답으로 노드에 알린다. 갱신·만료 판단은 DB `clock_timestamp()`를 쓴다.
- controller(`serve`)는 5초마다 만료된 lease(한 번에 최대 100개)를 `recovery_required`(cleanup `uncertain`)로 옮긴다. 이때 예약은 유지한다. 만료 뒤 갱신·이벤트·완료는 409다.
- `recovery_required` run은 자동으로 다시 `queued`가 되거나 다른 노드에 배정되지 않는다.
- 소유 노드가 `confirmed` 정리 결과(heartbeat `cleanup_reports` 또는 `/cleanup`)를 보고하면 attempt는 `released`, run은 `failed`가 되고 예약을 해제한다. `uncertain` 보고는 정보로만 받고 상태를 바꾸지 않는다(`uncertain_not_applied`).
- 네트워크 분할에서 이전 프로세스가 실제로 살아 있을 수 있다. fencing은 데이터 게시를 막을 뿐 OS 실행을 멈추지 않는다. 읽기 전용 검사도 중복 자원 비용이 생길 수 있다. 부작용이 있을 수 있는 build·test는 종료 증거가 없으면 `recovery_required`에서 운영자 판단이나 노드 복귀를 기다린다. 분할된 에이전트는 자기 watchdog(lease − 여유 = 20초)으로 작업을 먼저 멈추고, 연결이 돌아오면 정리 보고로 run을 `failed`로 끝낸다.
- 노드가 돌아오지 않으면(사망·철회·복원 fence) 사람이 정리를 확인한다. `POST /runs/{id}/recover`(`{"cleanup_confirmed": true}`, 프로젝트 실행 권한) 또는 `codeye run recover --id RUN_ID --confirm-cleanup`이 attempt를 `released`·`confirmed`로 두고 예약을 풀고 run을 `failed`로 바꾼 뒤(이벤트 `attempt.cleanup_confirmed`, 사유 `operator_confirmed`, 감사 `run.recovery_confirmed`) [다시 시도](API.md#다시-시도) 규칙으로 다시 넣는다. 확인 없이 시간이 지났다는 이유로 정리를 깨끗하다고 보지 않는다(A48). 확인하기 전에 노드에서 그 run의 프로세스(워커 `codeye worker`와 그 자식)가 없는지 본다.

### 장애 처리

| 실패 | 처리 |
|---|---|
| 일시적 HTTP 오류 | 같은 멱등 key·client ID로 재전송 |
| 워커 사망, agent 생존 | journal된 자식의 정리를 확인하고 attempt를 `failed`(reason `worker_lost`)와 실제 정리 상태로 보고. snapshot 유지. 새 attempt는 자동으로 만들지 않음 |
| agent·노드 상실 | lease 만료, fenced, `recovery_required`. 다른 노드에 자동 재배정하지 않음. 사람이 정리를 확인하면 다시 실행 |
| 노드와 controller 사이 연결 끊김 | 에이전트가 lease 여유에서 작업을 멈추고 정리. controller는 lease 만료로 `recovery_required`. 연결이 돌아오면 정리 보고로 `failed`, 늦은 complete는 409 |
| DB 중단 | API는 503(`/ready`는 `database_unavailable`). lease 여유 안이면 작업 계속, 넘으면 에이전트가 멈추고 DB가 돌아온 뒤 정리 보고 |
| agent 재시작 | journal 복구 후 정리 결과 보고. attempt를 다시 실행하지 않음 |
| controller 재시작 | DB의 run·lease·예약·명령·조정 상태로 이어서 판단. 시작 명령 중복 방지 |
| 업로드 중단 | 같은 client artifact ID로 재전송, finalize 전 비공개 |
| 용량 부족 | `queued`와 대기 사유. 요청 실패로 보이지 않게 함 |

### 대기 사유

`GET /runs/{id}`의 queued run `progress.waiting_reason`과 프로젝트 목록의 대기 사유는 저장 데이터에서 매번 계산한다. 이벤트가 없으므로 화면은 queued run을 주기적으로 다시 읽는다. 검사 순서는 다음과 같다.

| 순서 | 사유 | 조건 |
|---|---|---|
| 1 | `request_unavailable` | 요청 본문이 없는 이전 run |
| 2 | `execution_unavailable` | 철회되지 않은 노드가 없음 |
| 3 | `snapshot_unavailable` | 고정 snapshot run인데 해석할 수 없거나 그 snapshot을 보유한 노드가 없음 |
| 4 | `no_capable_node` | 요청·소스를 해석할 수 없는 비고정 run |
| 5 | `node_stale` | 신선한(20초 안 heartbeat) 노드가 없음 |
| 6 | `no_capable_node` | 신선한 노드 중 호환 노드가 없음 |
| 7 | `project_limit` | 프로젝트 동시 실행 상한 |
| 8 | `pool_paused` | 모든 호환 노드의 pool이 `max_workers=0` |
| 9 | `capacity_full` | 전역 상한 |
| 10 | `storage_quota_exceeded` | 산출물 저장 상한 도달, 또는 여유 있는 호환 노드가 모두 저장 영역 상한 |
| 11 | `capacity_full` | 그 밖의 호환 노드 여유 부족(자원, pool 상한) |
| — | null | 호환 노드에 여유가 있어 다음 idle 워커가 가져갈 수 있음 |

프로젝트 `capability_gaps`의 `execution_unavailable`은 등록 노드가 하나도 없을 때만 나온다. `source_access_unverified`와 `report_unavailable`은 계속 표시한다. 특정 run의 배정 가능성은 `waiting_reason`이 따로 말한다.

## 배정과 자원 회계

### 공정 순서

- 전역·프로젝트·pool·노드 상한과 heartbeat 신선도, CPU·RAM·disk, snapshot 위치, 요청자와 노드의 소스 접근을 PostgreSQL 트랜잭션 하나에서 확인한다. 정리 확인 전의 예약도 활성 수에 포함한다.
- 프로젝트별 동시 실행 기본 상한은 1이다.
- 순서는 프로젝트의 마지막 배정 시각 오름차순(배정된 적 없는 프로젝트가 먼저) → run 생성 시각 → run ID다. 프로젝트 사이 round-robin이고, 프로젝트 안에서는 오래 기다린 요청부터다.
- 프로세스 실행 중 DB 행 잠금을 유지하지 않는다.
- 우선순위 등급, 대기 시간 aging, resource-fit 실패 횟수 노출, 대형 작업의 예약 슬롯은 설계 계약(미구현)이다. PR이 예약 작업을 늘 굶기는 정책을 기본으로 두지 않는다.
- 한 노드의 동시 모델 요청은 많아야 워커 수 × `--review-parallel`이다. 전체 역할 다섯 개와 `review.scope`는 하나씩 실행하고 그 뒤의 검토 쌍만 함께 실행한다.
- endpoint별 동시 모델 요청 상한(`model_max_inflight`)과 노드·endpoint 전체의 모델 semaphore는 설계 계약(미구현)이다.

### 자원 회계

- 예약 수요는 attempt마다 고정값(CPU 1000m, 메모리 1 GiB, disk 1 GiB)이다. OS 제한이 아니라 admission 회계다.
- 예약 수요는 `--review-parallel`과 관계없이 같다. 함께 실행하는 모델 요청마다 OpenCode 프로세스가 하나씩 뜬다. 이 저장소를 가짜 upstream으로 검토할 때 프로세스 하나가 약 0.5 GiB를 썼다. 병렬도 4에서 OpenCode 프로세스 전체의 최대 RSS는 2.0~3.1 GiB였다(버전 확인 프로세스 포함). 메모리가 작은 노드는 병렬도를 낮춘다.
- CPU는 노드가 보고한 core 수를 정책 예산으로 보고 활성 예약 전체를 뺀다.
- 메모리·disk는 OS가 실행 중 사용량을 이미 뺀 관측 여유다. 그래서 마지막 heartbeat 이후 만들어져 아직 관측에 반영되지 않은 예약만 뺀다. 관측 여유에서 모든 예약을 다시 빼는 이중 계산을 하지 않는다.
- 관측 직후 아직 메모리를 쓰지 않은 워커는 다음 관측에서 과소 반영될 수 있다. 메모리·disk 관측은 heartbeat 간격만큼 늦다.
- heartbeat가 오래되거나 노드 저장 영역이 상한에 걸리면 새 배정을 멈춘다. 실행 중인 attempt는 중단하지 않는다.
- 노드 능력 보고에는 프로젝트 secret을 넣지 않는다.

## 워커 풀과 자동 증감

자동 증감 정책은 운영자가 CLI로 조정한다. UI·API 편집은 설계 계약(미구현)이다. 정책 값은 성능 실측값이 아닌 시작점이다.

```sh
codeye scheduler set --global-max-running 2 --project-max-running 1
codeye pool set --name default --min-workers 0 --max-workers 2 \
  --idle-timeout-seconds 180 --scale-up-cooldown-seconds 10 --scale-down-cooldown-seconds 60
codeye pool list
codeye serve --reconcile-interval 5s
```

노드의 등록·소스·도구 인수에 `--autoscale --max-workers N`을 더하면 자동 모드다. 등록된 노드의 pool 이름이 정책의 pool 이름과 같아야 한다.

### 설정값

| 설정 | 기본값 | 범위·의미 |
|---|---|---|
| `min_workers` | 0 | 유지하려는 워커 수 하한. 0..256 |
| `max_workers` | 2 | starting·idle·busy·draining 워커 수의 목표 상한. 0..256 |
| `idle_timeout_seconds` | 180 | 유휴 워커 축소 대기. 1..86400 |
| `scale_up_cooldown_seconds` | 10 | 급격한 생성 반복 완화. 0..3600 |
| `scale_down_cooldown_seconds` | 60 | 잦은 종료·재생성 완화. 0..3600 |
| `global_max_running` | 2 | 전체 동시 attempt 수 상한(`scheduler set`) |
| `project_max_running` | 1 | 프로젝트별 동시 attempt 수 상한(`scheduler set`) |
| `artifact_quota_bytes` | 없음 | 확정 산출물 합(sha256마다 한 번)의 상한. 도달하면 새 claim 거절([운영](OPERATIONS.md)) |
| reconcile 주기 | 5초 | `serve --reconcile-interval`. 음수면 끔 |
| heartbeat | 5초 | 노드·attempt 생존 갱신 |
| lease | 30초 | attempt 소유권 유효 기간 |

- `min_workers <= max_workers`, 음수 금지를 검증한다. `max_workers=0`은 `min_workers=0`일 때만 허용하며 새 배정을 멈춘다(`pool_paused`).
- 설정 수정은 `pool_policies`에 revision으로 쌓는다. 같은 설정의 재적용은 revision을 유지한다. `--expect-revision R`은 현재 revision이 다르면 충돌을 반환한다. pool에 정책이 없으면 기본값(revision 0)을 쓴다.
- 모든 pool의 최소값 합이 전역 상한을 넘으면 경고(`min_exceeds_global_running`)하고, 노드 용량을 넘으면 `min_exceeds_node_capacity`를 경고한다. 저장은 하고 목표를 못 채우면 `degraded_capacity`를 표시한다.
- 고정 모드(`--workers N`)는 정해진 수를 유지한다. 기존 등록 노드는 고정 모드로 남는다.

### 조정 알고리즘

각 pool은 하나의 짧은 트랜잭션에서 결정한다.

1. pool의 조정 상태 행을 `FOR UPDATE SKIP LOCKED`로 잡는다. 다른 controller가 결정 중이면 그 pool을 건너뛴다. 이어서 claim과 같은 순서로 `scheduler_limits`, 노드 행(ID 순)을 잠근다.
2. 신선한 자동 모드 노드의 워커만 센다. 오래된 노드에는 명령을 보내지 않는다. 시작 명령을 보냈지만 90초 안에 워커가 나타나지 않으면 대기 시작으로 세지 않고 새 명령으로 대체할 수 있다.
3. 수요는 이 pool의 신선한 자동 노드가 실행할 수 있는 queued run 수다. claim과 같은 관문(해석 가능한 요청, 능력, 노드의 root 허가, snapshot 위치, 요청자 권한과 root 권한)을 거치고, 남은 전역·pool·프로젝트 예산으로 자른다. 수요를 먼저 기존 유휴 워커에 맞추고, 남으면 프로세스가 적은 노드부터 새 워커를 계획한다.
4. 노드가 워커를 더 받을 수 있는지는 agent의 `max_workers`와 노드 자원 예산으로 정한다. busy가 아닌 워커와 계획된 시작마다 예약 하나를 매겨 warm 워커가 노드를 과약정하지 않게 한다.
5. 살아 있는 워커가 `max_workers`보다 많으면(상한을 낮춤): 시작하지 않는다. 유휴 워커를 바로 종료한다(유휴 시간·쿨다운 무시). 그래도 busy 수가 상한을 넘으면 busy 워커에 drain을 보낸다. busy 작업은 죽이지 않는다. drain된 워커는 현재 작업의 완료·정리를 보고한 뒤 나간다.
6. 그렇지 않으면: `min_workers`를 채울 만큼 시작을 계획한다. scale-up 쿨다운이 지나지 않았으면 이번 회차의 시작을 모두 취소한다. 시작이 없고, 마지막 증감 이후 scale-down 쿨다운이 지났고, 살아 있는 워커가 `max(min_workers, busy + 맞춘 수요)`보다 많을 때만, 수요에 맞추지 않은 유휴 워커 중 `idle_timeout` 이상 쉰 것부터 종료한다.
7. 명령은 노드 행 잠금 아래에서 노드별 sequence를 받아 저장하고, 조정 epoch·쿨다운 시각·결과를 같은 트랜잭션에 기록한다. controller가 재시작해도 보낸 명령과 준비 중인 워커를 계산에 포함한다.

- `min_workers`는 자원이 없어도 강제로 시작하라는 명령이 아니다. 목표를 못 채우면 원인을 표시한다.
- 모델 endpoint가 포화돼도 CPU 검사가 가능한 작업은 진행할 수 있다. 자동 증감은 모델 서비스의 실제 처리 용량을 늘리지 않는다. 대기 상태·예산·단계별 수요를 반영한 수요 계산은 설계 계약(미구현)이다.
- 긴급 kill은 별도의 명시적 취소다.

### pool 상태

`pool list`가 보여 주는 값:

| 필드 | 의미 |
|---|---|
| `capacity_state` | `healthy`, `paused`(`max_workers=0`), `over_target_draining`(실제 busy 수가 상한 초과), `degraded_capacity` |
| `reasons` | `no_controller_node`, `node_capacity`, `scale_up_cooldown`, `over_target`, `paused`, `min_exceeds_global_running` |
| `desired_workers` | `min(max(min_workers, busy + 맞춘 수요), max_workers)` |
| `live_workers` | 조정 뒤 유지할 워커 수(시작 요청 포함) |
| `busy_workers` | 실행 중 워커 수. 아직 실행 중인 drain 대상도 포함 |
| `draining_workers`, `pending_starts` | 종료 대기와 시작 요청 수 |
| `queued_demand` | 맞춘 수요와 배치하지 못한 수요의 합 |

- `over_target_draining`은 실제 busy 수가 상한을 넘는 동안 유지한다.
- 자동 모드 에이전트는 최초 한 번 워커 handshake로 실제 handler를 확인한 뒤 그 프로세스를 종료한다. 이후 시작 명령을 받으면 새 워커를 만들고 검증된 능력을 heartbeat에 보고한다.

### 검증 범위

실프로세스 시험은 Linux 한 호스트의 복수 에이전트에서 수행한다. 별도 물리 호스트 사이의 네트워크 분할, 노드 디스크 손실, Windows 전체 자동 증감 운영은 검증하지 않았다. 단일 머신 성공을 분산 복구 완료로 보지 않는다. 한 호스트에서 확인한 것은 다음 절에 있다.

## 여러 노드에서 확인한 것

`scripts/test_postgres.py --multinode`(`scripts/multinode_smoke.py`)가 한 WSL2 호스트(Linux 6.18, PostgreSQL 17.10)에서 실제 `codeye serve`와 노드 신원 8개(각자 상태 디렉터리·도구·source root)를 띄워 확인한다. 장애는 SIGSTOP/SIGKILL과 로컬 TCP 프록시 차단으로 만든다. controller는 `[::1]:P`에서만 듣고 분할 대상 노드만 `127.0.0.1:P`의 프록시로 붙는다(둘 다 loopback Host 허용 목록에 있다). IPv6 loopback이 없으면 controller는 `127.0.0.1:P`, 프록시는 `127.0.0.2:P`에서 듣고 같은 길이의 Host 값으로 바꿔 넘긴다(`CODEYE_TEST_MULTINODE_IPV4=1`로 강제, 이 호스트에서 두 방식 모두 통과). root·Docker·네트워크 namespace는 쓰지 않는다. 오래 걸리는 작업은 시험용 handler(`-tags codeye_fixture`의 `sleep`·`pause`)이고, 배정 시험의 분석은 실제 go vet이다.

| 시나리오 | 확인한 것 | 이 호스트의 측정값 |
|---|---|---|
| 능력 배정 | root·Git host별로 맞는 노드만 run을 받는다. go가 없고 다른 기준 분석기만 있는 노드는 Go 작업을 `unsupported`(`tool_not_configured`)로 끝낸다(성공 아님). 도구가 없는 노드는 root를 맡아도 능력을 알리지 않는다 | — |
| 대기와 시작 | 아무도 못 하는 run은 `no_capable_node`로 대기하고, 능력 있는 노드(local root, `--git-host`)가 등록되면 시작한다 | 등록 → claim 0.2초, Git 노드 등록 → 끝 5.8초 |
| 노드 사망 | 에이전트·워커·작업 프로세스 트리를 SIGKILL. lease 만료로 `recovery_required`(cleanup `uncertain`), 다른 노드로 자동 재배정 없음. 죽은 노드 자격·옛 epoch의 complete는 409 `STALE_ATTEMPT`와 `attempt.stale_rejected` 감사. 사람이 정리를 확인하면 새 run이 다른 노드에서 끝나고, 그 뒤의 옛 complete도 409이며 새 결과는 바뀌지 않는다. 같은 상태로 다시 띄운 에이전트는 옛 attempt를 정리 보고하고 새 일을 받는다 | 사망 → 노드 stale 19초, 사망 → `recovery_required` 34초(lease 30초 + sweep 5초 주기) |
| 연결 끊김 | 프록시 차단 동안 같은 run을 다른 노드가 실행하지 않는다. 에이전트가 lease 여유에서 작업을 멈추고(`lease_lost`) 연결이 돌아오면 정리 보고로 run이 `failed`, 늦은 complete는 409. 같은 노드가 다시 일을 받는다. lease 안의 8초 SIGSTOP은 아무 상태도 바꾸지 않는다 | 차단 → `lease_lost` 19초, 복구 → `failed` 1초 미만 |
| drain | SIGUSR1 뒤 하던 일은 첫 attempt로 끝나고, 새 run은 다른 노드가 없어 `no_capable_node`로 대기하며, 그 노드에 `start_worker`가 가지 않고, 에이전트는 종료 코드 0으로 끝난다 | drain → 종료 15초(작업 길이) |
| 에이전트 갱신 | drain → 종료 → `-trimpath`로 다시 빌드한 바이너리(hash 다름)를 같은 상태 디렉터리로 시작. node ID·비밀값 같음, 등록 감사 없음, protocol `0.1`, 대기 run을 받아 끝낸다 | 다시 빌드 7.5초, 시작 → run 끝 5.2초 |

- 전체 smoke는 이 호스트에서 약 2분이다(빌드 포함, `--smokes-only`).
- 시각은 측정값이며 보장값이 아니다. 노드 stale(20초)과 lease(30초)·sweep(5초)·에이전트 여유(10초)가 결정한다.
- **활성 controller는 하나다.** 두 controller를 같은 DB에 띄우는 구성은 지원하지 않고 시험하지 않는다(A51).
- 실제 환경에서만 검증할 수 있는 것은 [로드맵](ROADMAP.md#실제-환경에서만-검증할-수-있는-것)에 있다.

## 예약

시간대 예약으로 일반 분석 요청을 만든다. 코드: [`internal/schedule`](../internal/schedule/cron.go), [`schedules.go`](../internal/storage/postgres/schedules.go). 예약 API(목록·등록·수정·삭제, 권한, `Idempotency-Key`·`If-Match`)는 [API](API.md)에 있다.

### 정의

- 예약 본문의 `run`은 수동 요청과 같은 계약(`profile`, `source_policy: latest_at_acquire`, `scope`)이다. 예약 run의 브랜치 SHA는 실제 소스 확보 때 정한다. 코드가 같아도 규칙·지침이 바뀌면 분석할 수 있다.
- 프로젝트당 살아 있는 예약은 50개까지다.
- 9년 안에 발생하지 않는 cron 표현은 거부한다.
- 첫 occurrence는 생성 시각 이후다. 과거를 소급 실행하지 않는다. 예약을 다시 켜거나 cron·시간대를 바꾸면 그 시각 이후부터 다시 센다.

### cron과 시간대

- 5필드(분 시 일 월 요일)다. 목록·범위·간격, 월·요일 영문 약어, 요일 7=일요일, `@hourly`·`@daily`·`@weekly`·`@monthly`·`@yearly`를 받는다. 초 필드, `L`, `W`, `#`, `?`는 받지 않는다.
- 일과 요일이 모두 제한되면 OR, 하나가 `*`로 시작하면 AND다(`*/2`도 `*`로 시작하는 것으로 본다).
- 시간대는 IANA 이름만 받는다. 빈 값과 `Local`은 거부한다. 시스템 zone DB가 없어도 같은 결과가 나오도록 `time/tzdata`를 내장했다. Asia/Seoul도 시간대 문자열을 저장한다.
- 표현과 시간대 문자열을 저장하고, 각 occurrence는 UTC 시각(`scheduled_for`)으로 남긴다.

### 일광 절약 시간

| 표현 | 없는 시각(봄, 시계가 앞으로) | 반복 시각(가을, 시계가 뒤로) |
|---|---|---|
| 시 필드가 모든 시간(`0 * * * *`, `*/15 * * * *`) | 경과 시간 기준: 없는 시간대의 occurrence는 없다 | 두 시각 모두 실행한다(UTC로 1시간 간격 유지) |
| 특정 시(`30 2 * * *`) | 하루 한 번 보장: 전환 시각에 실행한다 | 앞선 시각(전환 전 offset)에 한 번만 실행한다 |

예: America/New_York 2026년에 `30 2 * * *`는 3월 8일 07:00Z(03:00 EDT), `30 1 * * *`는 11월 1일 05:30Z(01:30 EDT) 한 번이다. 매시 예약은 11월 1일 05:00Z와 06:00Z를 모두 만든다. DST가 없는 시간대는 벽시계 그대로다.

### 겹침

| 정책 | 이전 예약 실행이 끝나지 않았을 때 |
|---|---|
| `coalesce_latest`(기본) | 최신 occurrence 1개만 `pending`으로 남기고 더 이전 대기분은 `coalesced(superseded)`. 실행이 끝나면 다음 reconcile에서 일반 요청으로 넣는다. |
| `skip` | `skipped(overlap_active)` |
| `queue` | 모두 넣는다. 종료되지 않은 예약 run이 10개면 `skipped(queue_full)` |

"끝나지 않음"은 같은 예약이 만든 run 중 종료 상태(`completed`, `partial`, `failed`, `cancelled`)가 아닌 것이 있다는 뜻이다. 예약마다 `pending`은 최대 하나다(부분 유일 색인).

### 놓친 예약

- `latest_once`(기본): controller가 멈춘 사이 지난 occurrence 중 최신 하나만 평가한다. 이전 구간은 그 occurrence의 `missed_from`(첫 놓친 시각)으로만 남긴다. 1년 정지 뒤에도 행은 하나다(무제한 catch-up 없음).
- `skip`: 그 최신 occurrence도 예정 시각보다 2분 넘게 늦게 발견되면 `skipped(misfire)`다.
- 탐색은 정해진 창(1시간, 26시간, 32일, 367일, 9년)에서 최신 시각을 찾는다. 정지 기간에 비례해 계산하지 않는다.

### materialize와 다중 controller 안전성

- `codeye serve`의 reconcile 루프(`--reconcile-interval`, 기본 5초, 음수면 끔)가 자동 증감 전에 예약을 평가한다. 한 패스는 예약 100개까지 평가하고, 나머지는 다음 패스로 넘긴다. 프로세스 전역 예약 상태는 없다.
- 시각은 DB의 `clock_timestamp()`다. controller 시계가 어긋나도 판단이 바뀌지 않는다.
- 예약마다 짧은 트랜잭션에서 프로젝트 행, 예약 행 순서로 `FOR UPDATE`(API와 같은 순서)한 뒤 `next_at`을 다시 확인한다. 두 번째 controller는 기다린 뒤 이미 처리된 예약을 본다.
- occurrence 키 `(schedule_id, scheduled_for)`는 기본 키이고 삽입은 `ON CONFLICT DO NOTHING`이다. run은 `(schedule_id, scheduled_for)`로 occurrence를 참조하며 그 쌍은 유일하다.
- 실행 요청은 수동 요청과 같은 enqueue 경로를 쓴다. 요청자는 예약의 actor(마지막으로 정의를 바꾼 manager)이며 occurrence마다 권한을 다시 검사한다. 권한이 없으면 `skipped(actor_forbidden)`, 비활성 사용자는 `skipped(actor_disabled)`, 삭제된 프로젝트는 `skipped(project_deleted)`다.
- 멱등성 키는 `schedule <id> <unix 초>`다.
- 예약을 삭제(소프트 삭제)하면 대기 occurrence는 `cancelled(schedule_deleted)`가 된다.
- `run.queued` 이벤트와 `GET /runs/{id}`의 `trigger`가 출처(`manual` 또는 `schedule`+`schedule_id`+`scheduled_for`)를 보여 준다.

### 예약 데이터

- `schedules`: 정의, 버전, `next_at`, 소프트 삭제. 행 삭제는 거부한다.
- `schedule_occurrences`: 상태 `pending`, `enqueued`, `skipped`, `coalesced`, `cancelled`와 사유, `missed_from`, 결정 시각. 결정 뒤 불변이며 삭제·truncate를 거부한다.
- `runs.schedule_id`·`scheduled_for`: 쌍 CHECK, occurrence 외래키, 불변. NULL이면 수동 run이다.
- 백업 복원 뒤에도 대기 run, 예약, 이미 만든 occurrence는 그대로라서 같은 occurrence가 다시 만들어지지 않는다.

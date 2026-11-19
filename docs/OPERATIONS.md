# 배포와 운영

한 머신에서 CODEYE를 Native로 운영하는 개인 모드의 계약과, 아직 구현하지 않은 서버·노드·폐쇄망 배포의 설계 계약을 적는다. 사용자용 절차는 [사용 안내서](GUIDE.md)에 있다. 보안 경계와 보관·삭제 정책은 [보안](SECURITY.md), 실행기와 워커 정책은 [실행](EXECUTION.md)에 있다.

| 범위 | 상태 |
|---|---|
| `codeye up`·`open`·`start`·`stop`·`status`(개인 모드) | Linux에서 구현·검증. Windows는 빌드되며 명시적 미지원 오류를 낸다 |
| 데이터베이스 | 관리형(기본): 설치된 PostgreSQL 프로그램으로 상태 디렉터리에 cluster를 만들어 띄운다. external: 운영자가 준비한 PostgreSQL(`CODEYE_DATABASE_URL`). PostgreSQL을 내려받거나 설치하지 않는다 |
| 저장 영역 상한 | snapshot·workspace·cache·logs·artifacts. 새 작업만 거절하고 지우지 않는다 |
| 백업·복원 | DB와 확정 산출물을 같은 논리 시점으로 묶는다. 빈 대상에만 복원하고 이전 lease를 살리지 않는다 |
| `codeye doctor` | 호스트·도구·DB 진단. 도구는 에이전트가 쓰는 탐색과 같은 방법으로 찾는다. `--config`를 주면 저장 영역 사용량·상한, 최신 백업 나이, start/stop 상태 일관성을 더한다 |
| `codeye tools` | 이 머신에서 찾은 도구, 없는 도구, 추가 방법을 표로 보인다 |
| 서버 배포, 다중 노드, 폐쇄망 배포팩 | 설계 계약(미구현). 마지막 두 절 |

관련 요구사항: REQ-018(자원 제한), REQ-021(drain), REQ-022(lease·fencing·복구), NFR-003(재시작 뒤 복구), NFR-006(loopback 개인 모드, 민감값), NFR-007(산출물 hash). 수용 시험 TC-124~127, TC-32, TC-170~175는 [시험](TESTING.md)에 있다.

## 지원 범위

Docker·Podman·Kubernetes는 필요 조건이 아니다. 아래는 실제로 실행해 본 범위다. 적지 않은 조합은 확인하지 않았다.

| 대상 | 확인한 것 | 확인하지 않은 것 |
|---|---|---|
| Linux(WSL2, 커널 6.18), PostgreSQL 17.10, Go 1.27.4, Node 24.18.0 | 개인 모드 전체 경로(up·open·start·stop·status·backup·restore·doctor), 관리형 PostgreSQL(초기화·재사용·정지·백업·복원), 백업·복원 시험, Go 시험 전체 | 브라우저 자동 열기(`xdg-open`·`wslview`·`explorer.exe`)는 실행하지 않았다. 출력한 링크를 Playwright Chromium으로 열어 확인했다 |
| Ubuntu 24.04 CI 러너 | Go 시험 전체(임시 PostgreSQL 포함), vet, 웹 시험·빌드, 개인 모드 smoke(관리형 `up` 경로 포함). DB는 러너에 설치된 PostgreSQL을 쓰고 관리형 cluster는 PATH의 프로그램으로 만든다 | 백업·복원 시험(TC-32)과 smoke는 PostgreSQL 17.10에서만 실행했다 |
| RHEL 8.10 | — | 실행하지 않았다. 커널(4.18 기반)이 `pidfd_open`을 제공하는지 확인하지 않았다. 제공하지 않으면 `stop`은 신호를 보내지 않고 `action=not_signalled_unsupported`, 결과 `recovery_required`다. 같은 pid를 쓰는 다른 프로세스에 신호가 가지 않게 하려는 선택이다 |
| Windows | CI(Windows Server 2025)는 Go 단위 시험·vet·빌드만 실행한다. `GOOS=windows go vet ./...`가 통과한다. `doctor`는 동작하며 `processes.status=unsupported`를 낸다 | `start`·`stop`·`status`·`backup`·`restore`는 `codeye start/stop/status manage processes on Linux only …` 오류와 종료 코드 1로 끝난다. Windows 11의 Native 실행기(Job Object) 수용 시험은 실행하지 않았다. PostgreSQL이 필요한 시험은 Windows 작업에서 건너뛴다 |
| macOS | — | 빌드와 실행을 확인하지 않았다 |

- 개인 모드의 프로세스 관리는 `pidfd_open`(Linux 5.3 이상), `flock`, `setsid`, `SIGUSR1`(drain)·`SIGTERM`(취소)을 쓴다.
- Go 서비스 바이너리와 정적 웹 파일을 배포하되 분석 도구 런타임은 별도다. cgo나 동적 라이브러리를 쓰면 대상 glibc·시스템 라이브러리 호환성을 실제로 확인해야 한다. 최신 빌드 머신에서 컴파일됐다는 사실만으로 RHEL 8 호환을 가정하지 않는다.
- 시스템 서비스 등록(systemd, Windows Service)과 로그아웃 뒤 유지는 없다. 개인 모드 프로세스는 setsid로 터미널과 분리될 뿐이다.

## 구성 요소와 수동 실행

`codeye up`·`codeye start`는 아래 명령을 묶은 것이다. 개별 실행도 같은 계약을 따른다.

```text
codeye migrate                                   # schema 적용. start·serve는 migrate하지 않는다
codeye user create [--name NAME] [--username NAME [--password-file PATH]] [--admin]   # 첫 관리자는 CLI로 만든다
codeye source-root add --label LABEL --path ABSOLUTE_DIR
codeye source-root grant --root ROOT_ID --user USER_ID
codeye provider add --kind github|ghes --host HOST [--api-url https://HOST/api/v3]
codeye login-link --user USER_ID [--output .codeye/login-link] [--origin http://127.0.0.1:7331 | https://HOST[:PORT]]
codeye serve [--listen 127.0.0.1:7331] [--web-dir web/dist] [--artifact-dir .codeye/artifacts] [--reconcile-interval 5s]
             [--tls-cert CERT.pem --tls-key KEY.pem --public-origin https://HOST[:PORT]]
codeye node token create --pool NAME [--source-root ID[,ID...]] [--output .codeye/enroll-token] [--ttl 10m]
codeye agent --controller URL --state-dir DIR [--enroll-token-file PATH] [--autoscale --max-workers N] ...
```

- `serve`는 controller API, 노드 프로토콜(등록·claim·lease·산출물), 풀 자동 증감, 프로젝트 예약을 실행한다. 등록된 노드가 없으면 run은 `queued`로 남고 `waiting_reason`이 `execution_unavailable`이다.
- `serve --listen`이 loopback이 아니면 `--tls-cert`, `--tls-key`, `--public-origin`이 모두 있어야 한다. 하나라도 빠지면 종료 코드 2로 시작하지 않는다. 셋 중 일부만 주는 것도 loopback에서 같은 오류다. [TLS 수신](#tls-수신)을 본다.
- 로그인 링크와 등록 토큰은 0600 파일에만 쓴다. 표준 출력에는 토큰을 쓰지 않는다. DB에는 SHA-256만 저장한다.
- 에이전트의 옵션 전체는 `codeye --help`에 있다. 풀의 최소·최대와 전역 상한은 `codeye pool set`, `codeye scheduler set`으로 조정한다. 노드 철회는 `codeye node revoke --id NODE_ID`다.
- 한 머신 밖의 노드를 쓰는 배포는 검증하지 않았다([서버·노드 배포](#서버노드-배포-설계-계약-미구현)).

### 계정과 관리자 CLI

운영자 CLI는 `CODEYE_DATABASE_URL`로 DB에 직접 접근하며 감사 기록에는 행위자 `operator_cli`로 남는다. 사용자는 `--id USER_ID` 또는 `--username NAME` 중 하나로 가리킨다.

```text
codeye user create --username admin --admin --name "운영 담당"   # 비밀번호를 터미널에서 두 번 입력
codeye user create --username dana --password-file ./dana.pw     # 파일은 group·other 접근 불가(0600·0400)
codeye user passwd --username dana [--must-change]                # 새 비밀번호, 그 사용자의 모든 세션 폐기
codeye user list                                                  # {"items":[…]}. hash는 출력하지 않는다
codeye user admin --username dana --set | --unset
codeye user disable --username dana                               # 모든 세션 폐기, 되돌릴 수 없음
codeye user create [--name NAME]                                  # 사용자 이름 없는 로그인 링크 전용 사용자
```

- 비밀번호는 인자·환경변수로 받지 않는다. 터미널이면 입력을 숨기고 두 번 확인한다(Linux). 터미널이 아니면 표준 입력의 첫 줄, 또는 `--password-file`의 첫 줄을 읽는다. 비밀번호 파일은 일반 파일이어야 하고 group·other가 접근할 수 없어야 한다. 출력과 오류 문구에는 비밀번호를 넣지 않는다.
- `user create --username`은 비밀번호를 요구한다. 비밀번호 없이 사용자 이름만 가진 계정을 만들고 링크를 주려면 관리자 API(`POST /admin/users`)를 쓴다.
- 마지막 활성 관리자는 `user disable`, `user admin --unset`이 종료 코드 1로 거부한다.
- 관리자 역할은 프로젝트를 열지 않는다. 운영자가 다른 사람의 프로젝트를 봐야 하면 프로젝트 manager가 구성원으로 추가한다.
- 관리자 API의 사용자 생성·재설정이 주는 로그인 링크는 응답에만 있다. 관리자는 메신저 등 신뢰하는 경로로 사용자에게 전달하고, 10분 안에 쓰이지 않으면 다시 재설정한다.

### TLS 수신

LAN 등 loopback이 아닌 주소로 받으려면 `serve`가 TLS를 직접 종료한다.

```text
codeye serve --listen 0.0.0.0:8443 --tls-cert /etc/codeye/tls/cert.pem --tls-key /etc/codeye/tls/key.pem --public-origin https://codeye.example:8443
```

- `--tls-cert`는 PEM 인증서 체인, `--tls-key`는 그 개인키다. 키 파일 mode가 group·other에 열려 있으면(예: 0640) 시작하지 않는다. 인증서·키를 읽지 못한 오류 문구는 파일 내용을 담지 않는다.
- `--public-origin`은 사용자가 브라우저에 입력하는 `https://HOST[:PORT]`다. path·query·사용자 정보는 받지 않는다. Host 허용 목록과 Origin 비교, 로그인 링크가 이 값을 쓴다. 인증서의 이름이 이 host와 맞아야 브라우저가 연결한다.
- TLS 1.2 이상, HTTP/1.1이다. 세션 cookie는 `__Host-codeye_session`(Secure)이고 HSTS(`max-age=31536000`)를 보낸다. 같은 포트로 평문 HTTP 요청을 보내면 API를 주지 않는다.
- 인증서 갱신은 재시작으로 반영한다.
- Host 검사는 노드 경로에도 적용되므로 노드 에이전트도 `--controller https://HOST[:PORT]`(public origin)로 접속한다. 에이전트가 사설 CA나 자체 서명 인증서를 신뢰하게 하는 설정은 시험하지 않았다.
- TLS를 앞단 reverse proxy에서 종료하는 배포는 지원하지 않는다. 서버가 forwarded 헤더를 읽지 않아 접속 주소별 로그인 제한과 Origin·Host 검사가 맞지 않는다.
- 개인 모드(`codeye start`)의 `listen`은 계속 loopback만 받는다. TLS 수신은 `serve`를 직접 실행하는 배포의 기능이다.
- `codeye login-link --origin`은 `https://HOST[:PORT]`도 받는다(TLS 서버의 public origin을 준다).

## codeye tools

```text
codeye tools [--config PATH] [--json]
```

이 머신에서 CODEYE가 쓸 수 있는 도구를 찾아 표로 보인다. 찾은 도구는 경로·버전을, 없는 도구는 한 줄 추가 방법을 보인다. `--json`은 같은 결과를 [tools schema](../schemas/tools.schema.json)의 문서로 낸다. `--config`(또는 기본 설정 파일)의 `agent.go`·`agent.tools`·`agent.model.opencode`는 탐색보다 우선하고, 설정 파일 없이도 동작한다. 설정이 있고 그 `state_dir`가 이미 있으면 결과를 `<state_dir>/tools.json`에 쓴다. 그 밖에는 아무것도 만들거나 설치하지 않는다. 탐색 규칙은 [도구 찾기](ANALYZERS.md#도구-찾기)다. 종료 코드는 도구가 없어도 0이고, 설정 오류만 2다.

```text
TOOL         STATUS  VERSION   WHERE / HOW TO ADD
go           found   1.27.4    ~/.local/bin/go
cppcheck     missing -         not found - `micromamba install -p ~/.local/share/codeye-tools/cpp cppcheck` (or ...) or set `agent.tools.cppcheck`
```

시작할 때(`codeye start`·`up`)도 같은 탐색을 하고 같은 표를 출력한다.

## codeye doctor

```text
codeye doctor [--format text|json] [--work-dir .codeye/work] [--timeout 5s] [--config PATH]
```

호스트·도구·디스크·DB를 검사한다. 실제 모델 요청은 하지 않는다. 모델 연결은 `codeye model probe`(실제 추론을 요청하므로 비용이 들 수 있다)로 확인한다. [schema](../schemas/doctor.schema.json)와 [합성 예시](../examples/doctor.json)가 있다. 예시는 현재 머신의 실측 결과가 아니다.

**검사 항목(11개).** `host`, `work_disk`, 도구 검사 다섯 개(`git`, `go`, `node`, `rust`(cargo), `opencode_cli`), `postgres_client`(`psql`), `analyzers`, `configured_tools`, `database`. 도구는 PATH만 보지 않고 [도구 찾기](ANALYZERS.md#도구-찾기)와 같은 탐색으로 찾으므로 doctor가 보는 도구가 에이전트가 쓸 도구다. 보고서의 `tools` 배열에 이 탐색의 도구별 결과(경로 없이 상태·버전·시험한 계열 여부)를 싣고, 텍스트 출력은 `codeye tools`와 같은 표를 더한다. `schema_version`은 `0.1`이다. host에는 OS·arch·논리 CPU와, Linux에서는 RAM·디스크 관측치를 기록한다. Linux 외의 RAM·디스크 관측은 구현하지 않아 null이며 자원 한도를 적용한다는 뜻이 아니다.

| 상태 | 뜻 |
|---|---|
| `available` | 해당 probe 범위를 확인했다 |
| `unavailable` | 실패했거나 실행할 수 없다 |
| `not_configured` | DB 설정(`CODEYE_DATABASE_URL`)이 없다 |
| `unverified` | 계약이나 능력을 확인하지 않았다 |

- `version_only`는 버전 문자열만 확인했다는 뜻이다. 언어 어댑터 지원, 분석 품질, OS 호환성 인증이 아니다. 시험한 계열인지는 `tools[].version_tested`가 따로 알려 준다.
- `analyzers`는 기준 분석기(go vet, cppcheck, ruff, ESLint, cargo check) 중 하나라도 쓸 수 있으면 `available`이다. 하나도 없으면 분석이 모든 도구를 실행하지 않음으로 기록하므로 `unavailable`(`no_baseline_analyzer`)이다. `configured_tools`는 설정 파일에 적은 도구 경로가 모두 쓸 수 있는지 본다.
- 모델 연결과 프로세스 제어는 doctor가 확인하지 않는다. 항목으로 두지 않는다.
- **종료 코드.** 0은 이 모드에서 중요한 항목이 모두 정상, 1은 중요한 항목이 실패, 2는 사용법·출력 오류다. 중요한 항목은 보고서의 `checks[].blocking`이 `true`인 것이다. 작업 디스크를 쓸 수 없음, `CODEYE_DATABASE_URL`을 주었는데 질의가 실패함, 쓸 수 있는 기준 분석기가 하나도 없음, 설정에 적은 도구가 쓸 수 없음이다. `--config`의 `database.mode`가 `external`인데 DB 설정이 없는 것도 포함한다. 찾지 못한 선택 도구(git, rust, psql 등)와 DB를 설정하지 않은 상태는 상태 표시만 하고 종료 코드를 바꾸지 않는다. 이 값은 분석 run 상태와 무관하다.
- `--timeout`은 probe별 제한이며 기본 5초, 최대 1분이다. 여러 probe를 순서대로 실행하므로 전체 시간 제한은 아니다.
- 작업 디스크 probe는 `--work-dir`를 만들고 임시 파일 쓰기, sync, 읽기, 삭제를 확인한다. 분석 대상 프로젝트 경로를 지정하지 않는다. 같은 경로의 기존 파일을 덮어쓰지 않는다.
- 도구는 찾은 실행 파일을 argv로 호출한다(`psql`만 PATH). 임시 cwd/home, 제한된 환경, 출력 상한을 쓰고 계정 credential을 상속하지 않는다. 실행 파일 자체는 운영자가 신뢰하는 설치여야 한다. 이는 OS 샌드박스가 아니다.
- 원시 stdout/stderr, 실행 경로, DSN, 비밀번호를 결과에 넣지 않는다. 버전은 숫자 부분만 기록하고 실패는 고정 reason으로 표시한다. timeout은 종료를 확정하지 않고 `recovery_required`다. probe의 process group 중지는 실행기의 취소 계약을 대체하지 않는다.
- **DB 검사.** `CODEYE_DATABASE_URL`에 명시한 PostgreSQL URL만 검사한다. argv에 URL을 넣는 옵션은 없다. URL에는 host·user·database가 필요하고 `sslmode`, `sslrootcert`만 허용한다. 기본 TLS는 verify-full이며 `sslmode=disable`은 `127.0.0.1`/`::1`만 허용한다. 인증정보는 psql 자식 환경에만 전달하고 사용자 psqlrc, PGSERVICE, 기존 PGPASS, 운영 DB 환경을 상속하지 않는다. `psql -X -w`로 비대화형 읽기 쿼리 `SELECT current_setting('server_version')`만 수행한다. psql이 없으면 명확히 실패한다. TCP 포트가 열렸다는 이유로 DB 정상이라고 표시하지 않는다. DB 생성과 migration은 하지 않는다.

**`--config` 운영 점검.** 개인 모드 설정 경로를 주면 선택 필드 `operations`가 붙는다. `--config`가 없으면 출력에 이 필드가 없다. 종료 코드 규칙은 같다. 화면은 아직 이 값을 표시하지 않는다.

- `storage[]`: 다섯 영역마다 `status`(`within_limit`·`over_limit`·`no_limit`·`unmeasured`), `bytes`, `limit_bytes`, `filesystem_available_bytes`(statfs, Linux 외 null), `incomplete`, `enforcement`(`claim_refusal_node_heartbeat`·`claim_refusal_database_accounting`).
- `backup`: `backup_dir`의 가장 최근 완성 세트(manifest 형식이 맞는 것). `status`는 `not_configured`·`none`·`unreadable`·`recent`·`stale`(7일 초과)이고 `latest`, `created_at`, `age_seconds`가 붙는다. 세트 검증(hash)은 하지 않는다. 검증은 restore가 한다.
- `processes`: [프로세스 상태 일관성](#프로세스-상태-일관성) 검사. `status`는 `consistent`·`inconsistent`·`unsupported`, 역할별 상태, `issues`다. start·stop이 진행 중이면 `operation_in_progress`이고 에이전트 lock은 확인하지 않는다.

schema는 선택 속성 하나만 더했다. 상태와 필드의 조합(`unmeasured`면 `bytes`가 null, `consistent`면 문제 없음 등)은 `if/then`으로 묶었다. 웹의 Ajv strict 검증기도 이 schema로 생성한다.

## 개인 모드 설정 파일

기본 경로는 `$XDG_CONFIG_HOME/codeye/personal.json`(Go `os.UserConfigDir`)이다. 모든 개인 모드 명령은 `--config PATH`를 받는다. `codeye up`은 파일이 없으면 `{"schema_version": "0.1"}`만 담아 만들고(디렉터리 0700, 파일 0600), 그 뒤 자기가 다루는 필드(`source_roots`에 폴더 추가, `--postgres-bin-dir`의 `database.postgres_bin_dir`)만 고친다. 다른 필드와 순서는 그대로이고 기본값은 쓰지 않는다. 고친 결과가 검증을 통과할 때만 바꾼다(같은 디렉터리의 rename, symlink면 그 대상). 형식은 JSON 하나다. 알 수 없는 필드, 뒤에 붙은 값, 상대 경로, 정리되지 않은 경로는 거부한다. 오류 메시지에는 설정 경로나 거부된 값을 넣지 않는다. 예시는 [examples/personal.json](../examples/personal.json)이고, 이 빌드의 도구 이름으로 읽힌다는 시험이 있다. 코드는 [internal/personal/config.go](../internal/personal/config.go)다.

| 필드 | 기본값 | 검증 |
|---|---|---|
| `schema_version` | (필수) | `"0.1"` |
| `state_dir` | `$XDG_STATE_HOME/codeye`, 없으면 `~/.local/state/codeye` | 절대 경로. 0700으로 만들고, 소유자가 다르거나 symlink면 거부한다. source root를 품으면 안 된다 |
| `listen` | `127.0.0.1:7331`(쓰이고 있으면 7340까지 다음 빈 포트) | loopback 주소와 0이 아닌 포트만. 적으면 그 주소만 쓴다. 기본값에서 옮긴 주소는 controller 기록(`run/controller.json`의 `listen`)에 남고 `stop`·`status`·`open`이 그것을 쓴다 |
| `web_dir` | 실행 파일 옆 `web/dist`, 없으면 `../web/dist`(`index.html`이 있을 때), 아니면 빈 값(API만) | 빌드된 `web/dist`의 절대 경로 |
| `database.mode` | `managed` | `managed`(상태 디렉터리의 PostgreSQL, [관리형 PostgreSQL](#관리형-postgresql)) 또는 `external`(`CODEYE_DATABASE_URL`) |
| `database.postgres_bin_dir` | 빈 값 | managed: 처음 찾는 곳이다. 적은 디렉터리에 필요한 프로그램이 없으면 다른 곳을 찾지 않고 오류다. external: `pg_dump`·`pg_restore`가 있는 절대 경로. PATH에서 찾지 않는다. 백업·복원에 필요하다 |
| `source_roots[]` | 없음(0~32개). 홈 디렉터리는 적지 않아도 root `home`이다(설정에 쓰지 않는다. 적은 root가 홈을 덮거나 홈이 상태 디렉터리 안이면 더하지 않는다) | `label`(1~100자, 앞뒤 공백 없음), 절대 `path`. label과 path는 서로 유일해야 한다. root는 `state_dir` 안이면 안 된다(홈처럼 `state_dir`를 품는 root는 된다. 그 안의 데이터 폴더를 품는 프로젝트는 거부한다). 선택 `repository_code`·`model_review`는 받기만 하고 개인 모드에서는 쓰지 않는다. 프로젝트별 분석 선택이 정한다([안내서](GUIDE.md#프로젝트별-분석-선택)) |
| `git_hosts` | `["github.com"]`(필드가 없을 때) | 서로 다른 DNS 호스트 이름(선택 포트) 0~16개, 소문자로 바꾼다. `up`·`start`가 호스트마다 provider를 등록하고(`github.com`은 `github`, 그 밖은 `ghes`) 찾은 git과 함께 에이전트에 `--git`·`--git-host`로 넘긴다. 빈 목록이면 Git 주소를 받지 않는다 |
| `agent.name` | `personal` | 100자 이하 |
| `agent.max_workers` | min(4, 논리 CPU 수/2), 최소 1 | 1~16. 에이전트는 `--autoscale --max-workers N`으로 시작하고, `up`·`start`가 `default` 풀의 최대 워커 수를 같은 값으로 둔다 |
| `agent.toolchain_path` | `/usr/bin:/bin`(Linux) | 절대 경로 목록 |
| `agent.go`, `agent.go_mod_cache` | 빈 값 | 절대 경로. 비우면 [탐색](ANALYZERS.md#도구-찾기)이 찾은 go와 그 모듈 캐시를 쓴다 |
| `agent.tools` | 빈 값 | 이 빌드의 분석기·toolchain 플래그 이름(`staticcheck`, `node`, `ruff` 등)을 키로, 실행 파일의 절대 경로를 값으로. 모르는 이름은 거부한다. 적지 않은 도구는 탐색이 찾은 것을 쓰고, 적은 값은 항상 우선한다 |
| `agent.python_both_type_checkers` | false | mypy·pyright를 모두 실행. 탐색은 둘 다 찾으면 켠다 |
| `agent.dependencies` | 없음(의존성 받기 켜짐) | `enabled`(기본 true, false면 에이전트에 `--dependency-fetch`를 주지 않는다), `cache_dir`(절대 경로, 기본 `<state_dir>/cache/deps`), `go_proxy`·`go_sumdb`·`go_private`·`go_noproxy`·`go_nosumdb`(비우면 탐색이 읽은 사용자 `go env` 값, 그것도 없으면 Go 기본값), `npm_registry`(인증정보 없는 http(s) URL, 비우면 탐색이 읽은 사용자 npm 레지스트리, 그것도 없으면 npm 기본값), `timeout_seconds`(기본 600), `total_timeout_seconds`(기본 1200), `max_download_bytes`(기본 2 GiB). 프로젝트별로 끄는 것은 "고급"의 "의존성 받기"다([의존성 준비](ARCHITECTURE.md#의존성-준비)) |
| `agent.auto_detect` | true | `false`이면 도구를 탐색하지 않고 위 명시 설정만 쓴다 |
| `agent.model` | 없음(키 파일만으로도 모델을 쓴다) | 모든 항목 선택. `opencode`(절대 경로, 비우면 `~/.opencode/bin/opencode` 다음 PATH), `name`(`PROVIDER/MODEL` 또는 키 provider의 모델 이름, 비우면 provider 기본값), `credential_file`(절대 경로, CODEYE 키 파일 또는 OpenCode `auth.json`), `relay_file`(절대 경로, `credential_file`과 함께 쓰지 않음), `timeout_seconds`(1~1800, 비우면 180), `review_pack`(`review-r1` 기본·`review-v1` 이전 팩), `review_max_calls`(15~207, 비우면 99: attempt마다 review-r1 모델 호출 상한, `--review-max-calls`). 해석 순서는 [검토](REVIEW.md#모델-설정) |
| `quota.*_bytes` | 0(상한 없음) | `snapshot`·`workspace`·`cache`·`logs`·`artifacts`, 음수 거부 |
| `backup_dir` | 빈 값(백업은 `<state_dir>-backups`에 만든다) | 절대 경로. `state_dir` 안이거나 `state_dir`를 포함하면 거부 |
| `stop.drain_timeout_seconds` | 300 | 1~86400 |
| `stop.cancel_timeout_seconds` | 90 | 45~3600 |

- 설정에는 DB URL이나 토큰을 쓰지 않는다. external 모드의 DB 연결은 실행할 때의 환경변수 `CODEYE_DATABASE_URL`만 쓰고, `codeye` 프로세스 환경에 `PG*` 변수가 있으면 DB 열기를 거부한다(fail closed). 관리형 모드의 비밀번호는 상태 디렉터리의 `postgres/password`에만 있다.
- Git 저장소는 프로젝트로 등록한다(`codeye up https://github.com/OWNER/REPO` 또는 화면). 설정에는 허용 호스트(`git_hosts`)만 둔다([Git 연결](GITHUB.md)).
- 모델 API 키는 설정 파일에 쓰지 않는다. `<state_dir>/secrets/model.json`(0600, 디렉터리 `secrets/` 0700)이나 `agent.model.credential_file`, 사용자의 OpenCode `auth.json`에서 워커가 모델을 호출할 때마다 읽는다. 에이전트 인자에는 키 파일 경로만 있다. 백업은 산출물과 DB만 담으므로 키 파일은 백업에 들어가지 않는다. 복원한 설치에서는 키를 다시 둔다(`codeye up`).

## up, open, start, stop, status

```text
codeye up     [PATH | GIT-URL] [--config PATH] [--postgres-bin-dir DIR] [--no-open]
codeye open   [--config PATH] [--no-open]
codeye start  [--config PATH]
codeye stop   [--config PATH] [--format text|json]
codeye status [--config PATH] [--format text|json]
```

### 상태 디렉터리

배치는 모두 0700 디렉터리, 0600 파일이다.

| 경로 | 내용 |
|---|---|
| `run/controller.json`, `run/agent.json` | pid, 프로세스 시작 시각(`/proc/PID/stat` starttime), 실행 파일, 기록 시각 |
| `run/enroll-token` | 일회용 등록 토큰. 등록·heartbeat 확인 직후, 그리고 실패한 start의 끝에 지운다 |
| `personal.json` | 개인 사용자 ID |
| `login-link` | 일회용 로그인 링크(`codeye start`가 쓴다) |
| `postgres/data` | 관리형 PostgreSQL cluster(`initdb`가 만든 0700 디렉터리). `codeye.conf`는 시작마다 다시 쓰는 listen 주소·포트·socket 설정이다 |
| `postgres/password` | 관리형 PostgreSQL의 생성 비밀번호(0600) |
| `postgres/run` | 관리형 PostgreSQL의 Unix socket 디렉터리 |
| `logs/postgres.log` | 관리형 PostgreSQL 로그 |
| `personal.lock` | up·start·stop·backup·restore의 상호 배제 |
| `logs/controller.log`, `logs/agent.log` | 자식 프로세스 출력(O_APPEND) |
| `agent/` | 에이전트 상태 디렉터리(journal, node.json, 작업 사본) |
| `artifacts/` | controller 산출물 저장소 |
| `secrets/model.json` | 모델 API 키 파일(`codeye up`이 환경변수에서 쓴다). 워커의 모델 호출만 읽는다 |
| `run/personal-setup.json` | 화면의 설치 정보(`GET /setup`): 모델 ID와 마지막 확인 결과, git 사용 가능 여부와 호스트, 백업 디렉터리. 키는 없다 |
| `tools.json` | `codeye up`·`start`가 마지막으로 찾은 도구([codeye tools](#codeye-tools)) |
| `run/model-probe.json` | 마지막 모델 확인의 설정 지문(모델 ID·경로·키 파일 시각과 크기)과 결과. 키에서 나온 값은 없다 |

### start

개인 lock을 잡는다. 관리형 모드면 PostgreSQL을 띄운다([관리형 PostgreSQL](#관리형-postgresql)). 상태를 검사한다([일관성](#프로세스-상태-일관성)). 기록 없는 프로세스가 에이전트 lock이나 설정에 적은 `listen` 포트를 쥐고 있으면 거부한다. `listen`을 적지 않았고 기본 포트를 다른 프로그램이 쓰면 7340까지 다음 빈 포트를 쓴다. 기록의 프로세스가 끝났거나 pid가 다른 프로세스로 재사용됐으면 기록만 지운다(신호 없음). 그 뒤 순서는 다음과 같다. controller와 에이전트 중 이미 실행 중인 것은 그대로 쓴다. 둘 다 실행 중이고 에이전트 argv가 설정과 같으면 아무것도 띄우지 않고 종료 코드 0이다.

1. schema가 이 바이너리의 migration(현재 11)과 같은지 확인한다. external 모드에서 다르면 `run "codeye migrate" explicitly` 오류로 끝난다. 관리형 모드에서 이전 버전이면 migration을 적용한다. 이미 적용된 migration이 있으면(데이터가 있으면) 먼저 `pg_dump --format=custom`으로 `<백업 디렉터리>/codeye-pre-migrate-<UTC>/database.dump`(0600)를 쓰고, 실패하면 아무것도 바꾸지 않고 끝난다. 바이너리보다 새 schema나 불일치는 거부한다.
2. 개인 사용자를 만들거나 다시 쓴다. 개인 사용자는 관리자다(이전 버전에서 만든 사용자는 이때 관리자로 지정되고 감사 기록이 남는다). 설정의 source root를 label로 등록하고 사용자에게 허가한다. 같은 label이 다른 경로로 등록돼 있으면 거부한다. `quota.artifacts_bytes`를 `scheduler_limits.artifact_quota_bytes`에 반영한다(0이면 제거).
3. 등록이 필요한지 정한다. 처음 시작했거나, 노드가 철회됐거나, DB가 노드를 모르거나(복원 뒤), source root 집합이 바뀌었으면 다시 등록한다. 다시 등록하기 전에 에이전트 journal에 끝나지 않은 작업이 없는지 확인한다. 이전 노드는 철회하고, 이전 `agent/`는 `agent.retired-<ns>`로 이름만 바꾼다(삭제하지 않는다). 등록 토큰은 10분짜리 일회용이며 0600 파일로만 전달한다.
4. `codeye serve --personal --state-dir <state_dir>`을 setsid로 띄운다(브라우저 세션 7일, 경로로 등록·`GET /setup`·데이터 폴더 검사에 상태 디렉터리를 쓴다). 환경은 PATH·HOME·LANG·TZ와 `CODEYE_DATABASE_URL`뿐이다. `/api/v1/ready`를 30초까지 기다린다. 기록에 listen 주소를 남긴다.
5. `codeye agent`를 띄운다. 환경은 PATH·LANG뿐이며 DB 정보가 없다. argv는 설정에서 만든다(source root, `--project-analysis-policy`, 상태·백업 디렉터리의 `--protected-dir`, git과 `--git-host`, 도구, 모델(`--opencode`·`--model`·`--model-credential-file`), `--storage-quota`, `--storage-log-dir`). `--project-analysis-policy`는 root별 허가 대신 각 프로젝트의 분석 선택(소스를 모델에 보냄, 저장소의 빌드 스크립트 실행)을 로컬·Git 모두에 적용하게 한다. `--protected-dir`은 그 디렉터리를 품거나 그 안에 있는 프로젝트 폴더의 소스 확보를 `source_holds_codeye_data`로 거절하게 한다. 기록에 등록 토큰 플래그를 뺀 argv를 남긴다. `node.json`이 생기고 DB에 신선한 heartbeat가 올 때까지 120초를 기다린다. 실행 중인 에이전트의 기록 argv가 설정에서 만든 argv와 다르면(예: source root 추가) drain을 요청해 실행 중인 작업을 끝내게 하고(`stop.drain_timeout_seconds`까지), 끝나면 3단계부터 다시 띄운다. 그 안에 끝나지 않으면 취소하지 않고 오류로 끝난다. 에이전트는 작업을 마친 뒤 스스로 종료한다.
6. 실패하면 이 명령이 띄운 프로세스를 역순으로 SIGTERM하고 45초 기다린다. 끝나지 않은 프로세스의 기록은 지우지 않는다. 이 명령이 띄운 관리형 PostgreSQL은 다른 CODEYE 프로세스가 없으면 다시 멈춘다.

성공하면 새 로그인 링크를 `login-link`에 쓴다.

### up

`codeye up [PATH|GIT-URL]`은 설정 파일이 없으면 만들고 start와 같은 순서를 밟은 뒤 로그인 링크를 출력하고 브라우저를 연다. `://`가 있거나, 있는 경로가 아니고 `HOST/OWNER/REPO` 모양이면 Git 주소다. `https://`만 받고(없으면 붙인다), 호스트가 `git_hosts`에 없거나 저장소 주소 모양이 아니거나 git을 찾지 못했으면 아무것도 쓰거나 띄우기 전에 종료 코드 2로 끝난다. `…/tree/BRANCH`는 그 브랜치, 그 밖에는 기본 브랜치다.

1. `--postgres-bin-dir`가 설정과 다르면 `database.postgres_bin_dir`에 저장한다.
2. PATH를 절대 경로로 바꾸고 symlink를 푼다. 상태 디렉터리나 백업 디렉터리를 포함하거나 그 안이면 거부한다. 설정의 root나 홈 디렉터리(`home`) 안이면 가장 긴 root와 그 아래 상대 경로를 쓴다. 아니면 PATH 자체를 root(label은 폴더 이름, 겹치면 `-2`, `-3`…)로 설정에 더한다. DB에 같은 label이 다른 경로로 있으면 `home`은 `home-2`처럼 바꿔 등록한다.
3. Go toolchain이 설정에 없으면 PATH의 `go`(symlink를 푼 절대 경로)를 쓴다. 설정 파일에는 쓰지 않는다. start도 같다. 실행 중인 에이전트와 argv를 비교하기 전에 적용하므로 `up` 뒤의 `start`가 에이전트를 다시 띄우지 않는다.
4. start와 같다. 이미 실행 중이면 바뀐 것만 반영한다.
5. PATH가 있으면 그 root·상대 경로의 프로젝트를, Git 주소면 같은 provider와 저장소(대소문자·끝의 `/`·`.git` 무시)의 프로젝트를 찾고 없으면 등록한다(이름은 폴더·저장소 이름, 분석 선택은 소스 종류의 기본값). 그 프로젝트의 마지막 run이 대기·실행 중(`queued`·`preparing`·`running`·`finalizing`)이면 그 run을, 아니면(끝났거나 취소 중이거나 `recovery_required`) 새 run(`deep-review`, `latest_at_acquire`, 전체 범위)을 쓴다. 설정의 root 경로가 없어졌으면 시작은 하고 그 root의 프로젝트는 분석할 수 없다고 알린다.
6. 일회용 로그인 링크(10분)를 발급해 출력한다. PATH가 있으면 `&project=<ID>&run=<ID>`를 붙인다. `--no-open`이 없으면 브라우저로 연다: `$BROWSER`, WSL(`WSL_DISTRO_NAME` 또는 `/proc/sys/kernel/osrelease`의 `microsoft`)이면 `wslview`·`explorer.exe`, 그다음 `xdg-open` 중 PATH에 있는 첫 프로그램을 띄우고 기다리지 않는다. 열지 못해도 실패가 아니다.

`codeye open`은 controller가 실행 중일 때 6단계만 한다.

### stop

신호는 pidfd로 보낸다. pidfd를 연 뒤 그 프로세스의 시작 시각이 기록과 같은지 확인하고 `pidfd_send_signal`을 호출한다. 그래서 재사용된 pid의 다른 프로세스에는 신호가 가지 않는다. 강제 종료(SIGKILL)는 하지 않는다.

1. 에이전트에 SIGUSR1(drain)을 보낸다. 에이전트는 새 claim을 멈추고 유휴 워커를 정리한다. 실행 중인 attempt는 끝까지 수행해 결과를 보고한 뒤 스스로 종료한다(exit 0).
2. `stop.drain_timeout_seconds` 안에 끝나지 않으면 SIGTERM을 보낸다. 에이전트는 실행 중인 작업을 취소하고 하위 프로세스 정리를 확인한다. 그 attempt는 `failed`(`agent_shutdown`)로 끝나고 run도 `failed`다. 성공으로 바꾸지 않고 자동으로 다시 대기열에 넣지 않는다. 다시 분석하려면 새 run을 요청한다.
3. 에이전트가 끝나면 에이전트 journal을 읽어 정리를 판정한다(`agent_cleanup`: `confirmed`·`uncertain`). 그다음 controller에 SIGTERM을 보낸다.
4. 관리형 모드에서 에이전트와 controller가 모두 멈췄으면 `pg_ctl stop -m fast`로 PostgreSQL을 멈춘다. 하나라도 남았으면 PostgreSQL도 남긴다. JSON의 `database`는 `stopped`·`not_running`·`left_running`·`stop_failed`, external이면 `external`이다. 멈추지 않으면 결과는 `failed`다.
5. 결과는 `stopped`, `not_running`, `recovery_required`, `failed` 중 하나다. 다음이면 `recovery_required`다: 에이전트가 `cancel_timeout_seconds` 뒤에도 실행 중(이때 controller는 남겨 정리 보고를 받을 수 있게 한다), journal 판정 `uncertain`, 이 노드의 attempt가 DB에서 아직 `leased`·`recovery_required`. controller가 멈추지 않으면 `failed`다. 종료 코드 0은 `stopped`·`not_running`일 때뿐이다.

JSON 출력은 `outcome`, `processes[]`(`state_before`, `action`, `state_after`), `in_flight_at_stop`, `attempts_after_stop`, `agent_cleanup`, `database`, `notes`를 담는다. DB가 없으면 진행 중 작업을 읽지 못했다고 `notes`에 적는다.

### status

프로세스별 상태, controller `/ready`, DB 연결, 관리형 PostgreSQL(JSON `database`: `mode`, `postgres` = `running`·`stopped`·`not_initialized`·`unavailable`, `port`, `version`, `bin_dir`, `data_dir`), 노드 ID와 heartbeat 신선도, 워커 상태별 수, 이 노드의 진행 중 attempt, 저장 영역을 보여 준다. 상태는 `running`·`stopped`·`degraded`이며 `degraded`면 종료 코드 1이다. 다른 start·stop이 lock을 쥐고 있으면 `operation_in_progress`를 보고하고 에이전트 lock은 확인하지 않는다.

### 프로세스 상태 일관성

`start`·`status`·`doctor --config`는 기록, 살아 있는 프로세스, 에이전트 상태 lock(`agent/agent.lock`), listen 주소를 대조한다.

| 문제 | 뜻 |
|---|---|
| `stale_record` | 기록의 프로세스가 끝났거나 pid가 재사용됐다(`exited`·`pid_reused`) |
| `agent_running_without_record` | 기록 없는 프로세스가 에이전트 lock을 쥐고 있다 |
| `listen_in_use_without_record` | 기록 없는 프로세스가 설정에 적은 listen 주소를 쓰고 있다(적지 않은 기본 주소는 검사하지 않는다) |
| `partially_running` | controller와 에이전트 중 하나만 실행 중이다 |
| `record_unreadable` | 기록이나 lock을 읽지 못했다 |

기록되지 않은 프로세스에는 어떤 명령도 신호를 보내지 않는다.

## 관리형 PostgreSQL

`database.mode`가 `managed`(기본)이면 CODEYE가 상태 디렉터리에 전용 PostgreSQL cluster를 만들고 띄운다. 코드는 [internal/personal/pgcluster.go](../internal/personal/pgcluster.go)다. PostgreSQL은 내려받거나 설치하지 않는다.

**프로그램 찾기.** `initdb`·`pg_ctl`·`postgres`·`pg_dump`·`pg_restore`가 모두 있고 `pg_ctl --version`이 14 이상인 디렉터리를 쓴다. 명시한 곳(`up --postgres-bin-dir`, `database.postgres_bin_dir`, 환경변수 `CODEYE_POSTGRES_BIN_DIR` 순)이 있으면 그것만 본다. 그 디렉터리가 조건을 채우지 않으면 다른 곳을 찾지 않고 이유(없는 프로그램, 낮은 버전)와 함께 오류다. 명시한 곳이 없으면 PATH에서 `initdb`가 있는 디렉터리와 `/usr/lib/postgresql/*/bin`, `/usr/pgsql-*/bin`, `/usr/local/pgsql/bin`을 찾고 가장 높은 주 버전을 쓴다. cluster가 이미 있으면 그 주 버전(`PG_VERSION`)의 설치를 먼저 고른다. 아무것도 없으면 `PostgreSQL binaries not found (searched: …). Install `postgresql` (Ubuntu: `sudo apt install postgresql`), or set `database.postgres_bin_dir`, or use `database.mode: external`.`로 끝난다.

**초기화.** `postgres/data`가 비어 있으면 `initdb -D <state>/postgres/data -U codeye -E UTF8 --locale=C --auth=scram-sha-256 --pwfile=<0600 임시 파일> --no-instructions`를 실행한다. 비밀번호는 24바이트 난수의 hex이고, initdb가 성공한 뒤 `postgres/password`(0600)에 쓴다. 임시 파일은 바로 지운다. `postgresql.conf` 끝에 `include_if_exists = 'codeye.conf'`를 더한다. 비어 있지 않은데 `PG_VERSION`이 없는 디렉터리는 거부한다.

**시작.** 데이터 디렉터리의 주 버전과 프로그램의 주 버전이 다르면 `the managed data directory … was created by PostgreSQL 16, found 17 binaries (…) - point `database.postgres_bin_dir` at PostgreSQL 16 binaries, or back up with those and restore into a new state_dir`로 거부한다. 포트는 지난번 포트가 비어 있으면 그대로, 아니면 55432부터 200개 중 첫 빈 포트다. `codeye.conf`에 `listen_addresses = '127.0.0.1'`, `port`, `unix_socket_directories = '<state>/postgres/run'`(경로가 socket 길이 한계에 가까우면 빈 값으로 socket을 끈다)을 쓰고 `pg_ctl start -D … -l <state>/logs/postgres.log -w -t 90`을 실행한다. 실제 포트는 `postmaster.pid`에서 읽는다. `codeye` 데이터베이스가 없으면 `CREATE DATABASE codeye ENCODING 'UTF8' TEMPLATE template0`으로 만든다. 연결 URL `postgresql://codeye:<비밀번호>@127.0.0.1:<포트>/codeye?sslmode=disable`은 그 명령의 프로세스 환경 `CODEYE_DATABASE_URL`로만 쓰고, controller에는 start와 같은 방식으로 환경으로 준다. `codeye` 사용자는 cluster의 superuser다.

**환경.** PostgreSQL 프로그램은 `LC_ALL=C`, `LANG=C`, 프로그램 디렉터리와 `/usr/bin:/bin`의 PATH만 받는다. 셸의 `PG*` 변수와 `CODEYE_DATABASE_URL`은 이 모드의 모든 개인 모드 명령이 프로세스에서 지우고 `note: ignoring … from the environment`로 알린다. 저장 계층의 fail closed 규칙은 그대로다.

**명령별 동작.**

| 명령 | 관리형 PostgreSQL |
|---|---|
| `up`, `start` | 없으면 초기화, 멈춰 있으면 시작, DB가 없으면 생성, schema 맞춤 |
| `stop` | controller와 에이전트가 모두 멈춘 뒤 `pg_ctl stop -m fast` |
| `status`, `open` | 실행 중이면 연결한다. 시작하지 않는다 |
| `backup` | 멈춰 있으면 띄워 백업하고 다시 멈춘다. `pg_dump`는 cluster의 프로그램이다 |
| `restore` | 새 `state_dir`에 cluster와 빈 DB를 만들고 복원한다. 띄운 PostgreSQL은 끝나면 멈춘다 |
| `doctor --config` | 실행 중이면 그 cluster를 진단한다. 시작하지 않는다 |

## 저장 영역과 상한

| 영역 | 측정 대상 | 적용 근거 |
|---|---|---|
| `snapshot` | `agent/data/acquire-*`(그 안의 `workspace` 제외) | 에이전트가 직접 잰다. heartbeat `storage_quota_exceeded` |
| `workspace` | `agent/data/acquire-*/workspace`, `agent/data/ws-*` | 같음 |
| `cache` | `agent/workers`(워커 home·scratch), `agent/cache`, 받은 의존성(`state_dir/cache/deps` 또는 `agent.dependencies.cache_dir`), `agent.go_mod_cache`. 겹치는 디렉터리는 한 번만 센다 | 같음. 의존성 받기는 시작할 때 이 영역을 재고 남은 공간만큼만 받는다 |
| `logs` | `state_dir/logs` | 같음(`--storage-log-dir`) |
| `artifacts` | controller 산출물 | DB 집계. `artifacts`에서 sha256마다 한 번 센 크기 합 |

- 측정은 symlink를 따라가지 않고 파일 자체 크기를 센다. 읽지 못한 항목이 있으면 `incomplete`로 표시한다.
- 에이전트는 30초마다 잰다. 사용량이 상한 이상인 영역을 heartbeat에 보낸다. 상한이 없으면 필드를 보내지 않아 heartbeat 바이트가 바뀌지 않는다. 그 사이의 사용량은 반영되지 않는다.
- **거절 지점.** `ClaimAttempt` 트랜잭션 안에서 판정한다. 노드가 보고한 영역이 하나라도 있으면 그 노드는 새 claim을 받지 않는다(204). `artifact_quota_bytes`가 있고 확정 산출물 합이 그 이상이면 어떤 노드도 새 claim을 받지 않는다. 대기 중인 run의 `progress.waiting_reason`은 `storage_quota_exceeded`다. 기존 검사(`request_unavailable`부터 `project_limit`까지) 뒤의 순서는 `pool_paused` → 전역 상한 `capacity_full` → 산출물 상한 또는 "여유 있는 호환 노드가 모두 저장 상한"이면 `storage_quota_exceeded` → 그 밖의 여유 부족 `capacity_full`이다.
- **보호.** 실행 중인 attempt는 중단하지 않는다. 그래서 그 attempt의 업로드로 상한을 넘을 수 있다. 확정된 산출물과 보고서는 지우지 않는다.
- **받은 의존성.** `cache/deps`는 run이 끝나도 남아 다음 run이 다시 받지 않게 한다. 자동으로 지우지 않는다. 공간이 필요하면 에이전트를 멈춘 뒤 그 디렉터리를 지운다. 파일은 쓰기 가능하게 받으므로 일반 삭제로 지워진다. 다음 run은 처음부터 다시 받는다.
- **구현하지 않은 것.** 오래된 파일부터 지우는 정리, 정리 후보 목록(dry run), 보존 기간·참조 기반 정리, 참조 없는 임시 업로드 정리는 없다. 참조·pin·보존 규칙 없이 지우면 근거를 잃기 때문이다. 공간을 비우는 일은 운영자가 한다. 보관·삭제 정책의 제안은 [보안](SECURITY.md)에 있다.
- **측정과 적용의 구분.** `status`는 영역별 `basis`(`node_heartbeat`·`database_accounting`)를 보여 준다. `doctor`는 디렉터리를 지금 직접 재므로 적용 값과 다를 수 있다. 특히 artifacts의 디렉터리 크기와 DB 집계는 다르다.
- 상한 조정: 설정을 바꾸고 `codeye stop`·`codeye start`. 운영자 CLI `codeye scheduler set --artifact-quota-bytes N`(0이면 제거)도 있다.

저장은 [migration 0009](../migrations/sql/0009_storage_quota.sql)의 `nodes.storage_quota_exceeded`(영역 배열, 기본 빈 배열)와 `scheduler_limits.artifact_quota_bytes`(NULL 또는 양수)를 쓴다.

## 백업과 복원

```text
codeye backup  [--config PATH] [--output DIR] [--format text|json]
codeye restore [--config PATH] --from DIR [--format text|json]
```

백업은 전체 세트만 만든다. 증분, 압축, 암호화, 보존 개수, 자동 예약 백업은 없다. 백업 디렉터리 보호(권한·위치·외부 보관)는 운영자 책임이다. dump에는 프로젝트 이름·경로·보고서 같은 운영 데이터가 들어 있다. 인증정보 값은 없고 세션·토큰은 hash로만 저장된다. 코드는 [internal/backup](../internal/backup/backup.go), [internal/pgtool](../internal/pgtool/pgtool.go)다.

### 백업

출력 디렉터리는 없거나 비어 있어야 한다. `--output`이 없으면 `<backup_dir>/codeye-<UTC 시각>`이고, `backup_dir`가 없으면 `<state_dir>-backups/codeye-<UTC 시각>`이다. 실행 중에도(온라인) 정지 뒤에도 만들 수 있다. 개인 lock을 잡으므로 start·stop·restore와 겹치지 않는다.

1. `REPEATABLE READ READ ONLY` 트랜잭션을 열고 `pg_export_snapshot()`으로 snapshot ID를 얻는다. 같은 snapshot에서 migration 버전, 서버 버전, 확정 산출물 목록(`finalized`이고 저장 키가 있는 행의 sha256·크기), 표별 행 수를 읽는다.
2. 트랜잭션을 연 채 `pg_dump --format=custom --no-owner --no-privileges --snapshot=ID --schema=<현재 schema> --file=database.dump`를 실행한다. 파일은 미리 0600으로 만든다. `pg_dump` 주 버전이 서버보다 낮으면 거부한다.
3. 목록의 각 내용을 산출물 저장소에서 복사하며 다시 hash한다. sha256과 크기가 DB와 다르면 실패한다.
4. `artifacts.json`(정렬된 sha256·크기 목록)을 쓰고, 마지막에 `manifest.json`을 쓴다. manifest가 없으면 완성되지 않은 세트다. 실패하면 출력 디렉터리 내용을 지운다.

**같은 논리 시점.** dump와 산출물 목록은 같은 DB snapshot에서 나온다. 산출물은 내용 주소(sha256)이고 확정 뒤 바뀌지 않는다. 그래서 snapshot 뒤에 복사해도 같은 바이트이며, 복사할 때 hash로 확인한다. snapshot 뒤에 확정된 산출물은 세트에 들어가지 않고 그 산출물을 가리키는 행도 dump에 없다. 확정되지 않은 업로드는 백업하지 않는다. DB 잠금이나 정지 강제는 개인 사용 중 백업을 막아 채택하지 않았다. 정지 뒤 백업을 권장한다.

manifest 필드: `format`(`codeye-backup/0.1`), `created_at`, `consistency`(`exported_snapshot`), `migration_version`, `codeye_binary_migrations`, `postgres_server_version`, `pg_dump_version`, `database`(파일·형식·sha256·크기), `artifacts`(목록 파일 hash, 수, 바이트, 내용 없는 행 수), `rows`, `personal_user_id`. 호스트·사용자·DB 이름·비밀번호·URL은 넣지 않는다. 모든 파일은 0600, 디렉터리는 0700이다.

**인증정보.** `pg_dump`·`pg_restore`는 관리형 cluster의 프로그램 디렉터리, external 모드면 `database.postgres_bin_dir`에서만 찾는다. argv에는 플래그와 파일 경로만 들어간다. 연결 정보는 자식 환경의 `PGHOST`·`PGPORT`·`PGUSER`·`PGPASSWORD`·`PGDATABASE`·`PGSSLMODE`·`PGSSLROOTCERT`로만 전달한다. `PGPASSFILE=/dev/null`로 사용자 `.pgpass`를 읽지 않게 하고, cwd는 임시 디렉터리다. 오류 문구의 비밀번호는 `[REDACTED]`로 바꾼다. URL 규칙은 controller와 같다(loopback만 `sslmode=disable`, 그 외 `verify-full`).

### 복원 전 검증

- 대상 설치가 실행 중이거나 불일치하면 거부한다. 대상 `state_dir`에 `agent/node.json`이나 `personal.json`이 있으면 거부한다. 새 `state_dir`가 필요하다.
- 세트 검증: manifest를 엄격하게 읽고 형식을 확인한다. dump와 목록의 hash·크기, 목록 정렬, 각 내용의 hash·크기, 총계를 확인한다. 하나라도 다르면 대상에 아무것도 쓰지 않는다.
- manifest의 migration 버전이 이 바이너리보다 높으면 거부한다. 낮으면 복원 뒤 관리형 모드의 `up`·`start`가 migration을 적용하고(먼저 `pg_dump` 사본), external 모드의 start는 `codeye migrate`를 요구한다.
- 대상 DB의 현재 schema에 표가 하나라도 있으면 거부한다(`not empty`). 대상 산출물 디렉터리에 파일이 있으면 거부한다. 병합이나 덮어쓰기는 ID 충돌과 근거 혼합 위험이 있어 하지 않는다.

### 복원과 fencing

1. 내용을 산출물 저장소의 쓰기 경로(`artifactstore.Put`)로 넣는다.
2. `pg_restore --list` 결과에서 `SCHEMA public`·`COMMENT ON SCHEMA public` 항목을 뺀다. 그 목록으로 `pg_restore --use-list --exit-on-error --single-transaction --no-owner --no-privileges`를 실행한다.
3. 복원된 행 수가 manifest와 같은지 비교한다. 확정 산출물 행마다 내용이 있는지 다시 hash해 확인한다.
4. **fence.** 한 트랜잭션에서 모든 노드를 철회하고 그 워커를 `stopped`로 둔다. 모든 세션을 철회하고, 쓰지 않은 로그인 토큰을 사용 처리하고, 쓰지 않은 등록 토큰을 철회한다. `leased` attempt는 lease를 지금으로 끝낸 뒤 기존 만료 경로(`ExpireAttempt`)로 `recovery_required`로 둔다. 예약은 유지한다. 대기 중인 run, 예약, 이미 만든 예약 회차는 그대로라서 같은 회차가 다시 만들어지지 않는다.
5. `personal.json`에 manifest의 사용자 ID를 쓴다.

실패하면 대상 산출물 디렉터리를 비운다. `pg_restore`가 성공한 뒤의 실패라면 DB를 지우고 다시 만들라고 출력한다(복원 명령은 DB를 지우지 않는다).

복원 뒤 `codeye up`(또는 `start`)은 새 노드로 등록한다. 이전 노드 credential과 세션은 거부된다. 이전 epoch의 lease 갱신은 `ErrStaleAttempt`다. 복원 시점에 실행 중이던 run은 `recovery_required`로 남는다. 철회된 노드만 정리를 확인할 수 있어서, 이 run이 쥔 예약을 푸는 운영자 명령은 아직 없다. 전역 상한이 막히면 `codeye scheduler set --global-max-running N`으로 늘린다. 정지 뒤에 백업하면 이 경우가 생기지 않는다.

## 관측성

**측정 대상(설계 계약).** 웹/API 지연, queue wait, active/idle/draining workers, capacity 부족 이유, attempt retry, stale write 거부, tool failure/parser failure, 모델 latency/429/timeouts, artifact queue/용량, 보고서 생성 실패를 측정한다. endpoint별 모델 사용량은 공급자가 제공한 값과 추정치를 구분한다. 코드·프롬프트 전문을 일반 운영 로그에 넣지 않는다.

**지금 구현된 것.** 메트릭 수집·내보내기 endpoint는 없다. 운영자가 볼 수 있는 값은 다음이다.

- `codeye status`와 `GET /api/v1/ready`(DB 연결, schema 상태, 503 사유 `database_unavailable`·`schema_outdated`·`schema_newer`·`schema_inconsistent`).
- run의 이벤트와 진행률, `waiting_reason`(`execution_unavailable`, `pool_paused`, `capacity_full`, `project_limit`, `storage_quota_exceeded` 등), 노드 heartbeat와 워커 상태(`codeye node list`, `codeye pool list`).
- 모든 응답의 `X-Request-ID`, `state_dir/logs/controller.log`·`agent.log`. 로그와 출력에 DB 비밀번호와 토큰은 나오지 않는다.

## 운영 상황별 대응

| 상황 | 기본 조치 | 구현 |
|---|---|---|
| 모델 응답 실패 | 도구 결과 보존, 모델 재시도 한도, partial 보고서 | 구현([검토](REVIEW.md)) |
| node offline | 새 배정 중지, lease fence, recovery_required 확인 | 구현(lease 만료·epoch fencing). 한 머신에서만 시험했다 |
| DB unavailable | 신규 요청 503, 에이전트는 lease 안전 정책으로 중단 | 구현(API 503 `UNAVAILABLE`, lease 갱신 실패 시 lease 만료 전에 취소) |
| NFS 지연 | acquire timeout, 원본 불안정/권한 구분, 서버 전체 block 금지 | 소스 확보 timeout은 구현. NFS 실기는 검증하지 않았다 |
| tool version 변경 | capability 갱신, 계약 시험 후 catalog revision | 설계 계약 |
| max worker 축소 | drain, 진행 작업 강제 종료하지 않음 | 구현 |
| artifact quota 초과 | 신규 생성 제한, 참조 없는 임시 업로드 정리 | 새 작업 거절은 구현. 임시 업로드 정리는 미구현 |
| stop이 `recovery_required` | `status`·로그 확인 후 `codeye stop` 반복. 프로세스를 강제 종료하지 않는다 | 구현 |
| 복원 직후 `recovery_required` run | 새 run으로 다시 요청 | 구현. 예약 해제 명령은 없다 |

## 배포 전 점검

- 인증과 접근: 브라우저 세션, Host/Origin 검사, loopback이 아니면 TLS 필수, 첫 관리자와 사용자 계정, 프로젝트 구성원([보안](SECURITY.md)).
- source root 권한과 `repository_code`·`model_review` 허가를 root마다 의도대로 정했다.
- 도구 사용 조건과 Native 신뢰 범위, timeout과 취소, 자원 reserve(`max_workers`, 머신에 남길 CPU·메모리).
- 모델 한도와 외부 통신 금지. 모델 설정이 없으면 모델 검토가 실행되지 않는다.
- 백업 복원을 실제로 해 보았다. `backup_dir` 보호를 정했다.
- 내보내기에서 코드 발췌와 비밀값이 빠지는지 확인했다.
- `codeye doctor --config PATH`로 도구·DB·저장 상한을 확인했다.
- [예시 설정](../examples/platform.yaml)은 설계 예시이며 이 바이너리가 읽지 않는다. 출발점일 뿐이고 실제 머신 용량을 측정해 조정한다.

## 서버·노드 배포 (설계 계약, 미구현)

이 절과 다음 절은 설계다. 이 빌드에는 구현도 시험도 없다.

- controller는 단일 활성 인스턴스로 시작한다. DB와 artifact store를 지정하고 노드 에이전트를 운영자가 직접 설치한다. 서버가 노드에 SSH하지 않는다. 에이전트는 서버에서 작업을 받아 간다.
- Windows Service나 승인된 작업 실행 방식, Linux systemd/사용자 service 등 로그아웃 뒤에도 지속 실행되는 방법을 환경에 맞게 선택한다.
- enrollment token은 짧은 수명의 1회용이며 node identity와 pool·신뢰 범위를 부여한다. 에이전트 업데이트는 drain → binary 교체 → protocol 확인 → 재등록 없이 복귀 순서를 따른다. 최소/최대 정책은 controller가 유지한다.
- 배포 manifest에 실제 OS/arch, 빌드 ABI, toolchain, DB, 모델 adapter 버전을 고정한다.
- 서버는 RHEL 8.10과 내부 미러·CA·NFS 조건을 우선 검증할 대상이다. 아직 실행하지 않았다.
- 저장 위치: DB data, workspace, tool/runtime cache, 임시 결과는 로컬 디스크를 우선한다. NFS는 원본 source와 선택적 보관 산출물에 쓴다. DB data를 NFS로 옮기는 것은 별도 DB 운영 검증 없이 기본 지원하지 않는다. node cache를 공용 NFS에 두어 잠금·성능 문제가 생기는 것을 기본 설계로 삼지 않는다.
- 백업·복구 목표 시간은 실제 데이터 규모를 측정한 뒤 정한다. 복구 시험은 프로젝트·run·report·evidence 링크·ACL·예약 중복 방지를 확인한다. 개인 모드에서 구현한 같은 시점 백업과 lease 비복원 규칙을 서버에도 적용한다.
- 다중 사용자 권한과 감사 운영은 [보안](SECURITY.md)의 설계 계약을 따른다.

## 폐쇄망 배포팩 (설계 계약, 미구현)

- 버전·hash가 고정된 서비스 바이너리, 웹 자산, DB migration, 규칙팩, 도구 manifest를 제공한다. 폰트·아이콘·JS·CSS·차트에 외부 CDN 의존성을 두지 않는다. 도구 바이너리 재배포가 불가하면 사내 미러에서 설치할 경로와 검증 정보를 제공한다.
- 분석기 catalog, 언어 toolchain, 프로젝트 의존성 mirror는 구분한다. 미러에 없는 버전은 unavailable로 표시하고 외부 다운로드로 해결하지 않는다. CA 검증을 끄는 방식으로 연결 문제를 감추지 않는다.
- 설정의 `.invalid` 주소와 예시 경로는 실제 운영 정보로 교체하되 Git에 secret을 올리지 않는다.
- 외부 telemetry·업데이트·원격 규칙 다운로드·공유는 기본 차단한다.

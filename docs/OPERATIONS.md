# 배포와 운영

Linux 한 머신에서는 `codeye up`으로 웹 서버, 에이전트, 관리형 PostgreSQL을 함께 띄운다. 서비스를 실행하는 Linux 일반 계정이 소스 폴더와 분석 도구를 읽을 수 있어야 한다. 설치와 첫 분석은 [사용 안내서](GUIDE.md#첫-시작)를 따른다.

| 하려는 일 | 절차 |
|---|---|
| 새 설치·업그레이드 | [설치와 첫 분석](GUIDE.md#첫-시작) · [배포판 업그레이드](#배포판-업그레이드) |
| 서버 설정·운영 | [서비스 설정 파일](#서비스-설정-파일) · [시작·정지·상태](#up-open-start-stop-status) |
| 다른 머신에 서비스 | [TLS 수신](#tls-수신) · [받는 주소](#받는-주소) · [계정과 관리자 CLI](#계정과-관리자-cli) |
| 설치와 도구 점검 | [지원 범위](#지원-범위) · [codeye tools](#codeye-tools) · [codeye doctor](#codeye-doctor) |
| 보관·복구 | [저장 영역과 상한](#저장-영역과-상한) · [백업과 복원](#백업과-복원) · [장애 대응](#장애-대응-runbook) |
| 여러 노드·인터넷 없는 머신 | [서버·노드 배포](#서버노드-배포) · [폐쇄망 배포팩](#폐쇄망-배포팩) |

관련 요구사항은 REQ-018·021·022, NFR-003·006·007이며 수용 시험은 [시험](TESTING.md)의 TC-32, TC-124~127, TC-170~175, TC-246~255에 있다. [보안 경계와 보관 정책](SECURITY.md) · [실행기와 워커 정책](EXECUTION.md)

## 지원 범위

배포 파일은 Linux x86-64용과 ARM64용 정적 실행 파일 및 웹 화면을 제공한다. Docker·Podman·Kubernetes 없이 실행한다. 아래는 실제 시험 범위다.

| 환경 | 실행한 시험 |
|---|---|
| WSL2 Linux(커널 6.18), PostgreSQL 17.10 | 시작·재사용·정지, 웹과 로컬 에이전트, 관리형 DB의 백업·복원. 출력한 로그인 링크를 Playwright Chromium에서 열었다. 시스템 브라우저 자동 실행은 별도 시험하지 않았다 |
| Ubuntu 24.04 CI | Go 시험·vet, 웹 시험·빌드, 관리형 PostgreSQL을 쓰는 `codeye up` smoke |
| Linux ARM64(QEMU 에뮬레이션) | 배포 실행 파일 시작, PostgreSQL migration과 웹 서비스 실행. 실제 ARM64 머신에서는 실행하지 않았다 |
| Red Hat UBI 8.10 사용자 공간, WSL2 커널, PostgreSQL 17 | 일반 계정으로 오프라인 시작, 읽기 전용 폴더와 격리된 테스트 NFSv4.1 분석, 로그인·웹·HTML 보고서. [소스 확보 시험](TESTING.md#소스-확보) |
| Windows Server 2025 CI | Go 단위 시험·vet·빌드. Windows의 `doctor`는 동작하고 Linux용 서비스 명령은 미지원 오류를 반환한다 |

RHEL 8.10 호스트 커널과 실제 회사 NFS·CA·미러·GHES, macOS에서의 실행은 별도 검증이 필요하다. Linux 프로세스 관리는 `flock`·`setsid`와 `pidfd_open`(Linux 5.3 이상 또는 배포판의 백포트)을 사용한다. 기존 프로세스를 확인할 수 없는 커널에서는 `stop`이 신호를 보내지 않고 `not_signalled_unsupported`와 `recovery_required`를 반환한다.

## 구성 요소와 수동 실행

`codeye up`·`codeye start`는 아래 명령을 묶은 것이다. 개별 실행도 같은 계약을 따른다.

```text
codeye migrate                                   # 외부 PostgreSQL schema 적용
codeye user create [--name NAME] [--username NAME [--password-file PATH]] [--admin]   # 첫 관리자는 CLI로 만든다
codeye source-root add --label LABEL --path ABSOLUTE_DIR
codeye source-root grant --root ROOT_ID --user USER_ID
codeye provider add --kind github|ghes --host HOST [--api-url https://HOST/api/v3]
codeye login-link --user USER_ID [--output .codeye/login-link] [--origin http://127.0.0.1:7331 | https://HOST[:PORT]]
codeye serve [--listen 127.0.0.1:7331] [--web-dir web/dist] [--artifact-dir .codeye/artifacts] [--reconcile-interval 5s]
             [--tls-cert CERT.pem --tls-key KEY.pem --public-origin https://HOST[:PORT]... | --plain-http [--public-origin http://HOST[:PORT]]...]
             [--local-listen 127.0.0.1:PORT] [--state-dir DIR]
codeye node token create --pool NAME [--source-root ID[,ID...]] [--output .codeye/enroll-token] [--ttl 10m]
codeye agent --controller URL --state-dir DIR [--enroll-token-file PATH] [--autoscale --max-workers N] ...
```

- `serve`는 controller API, 노드 프로토콜(등록·claim·lease·산출물), 풀 자동 증감, 프로젝트 예약을 실행한다. 등록된 노드가 없으면 run은 `queued`로 남고 `waiting_reason`이 `execution_unavailable`이다.
- `serve --listen`이 loopback이 아니면 `--tls-cert`, `--tls-key`, `--public-origin`이 모두 있거나 `--plain-http`가 있어야 한다. 아니면 종료 코드 2로 시작하지 않는다. 셋 중 일부만 주는 것도 loopback에서 같은 오류다. [TLS 수신](#tls-수신)을 본다.
- `--local-listen`은 loopback 수신 주소를 하나 더 연다(같은 머신의 에이전트·명령용, 평문 loopback 규칙). `--state-dir`은 플랫폼 GitHub 인증정보·도구 기록·데이터 폴더 검사에 쓰는 상태 디렉터리다. 없으면 플랫폼 인증정보를 읽을 수 없고 `GET /setup`의 모델·도구가 null이다.
- 로그인 링크와 등록 토큰은 0600 파일에만 쓴다. 표준 출력에는 토큰을 쓰지 않는다. DB에는 SHA-256만 저장한다.
- 에이전트의 옵션 전체는 `codeye agent --help`에 있다. 풀의 최소·최대와 전역 상한은 `codeye pool set`, `codeye scheduler set`으로 조정한다. 노드 철회는 `codeye node revoke --id NODE_ID`다.
- 한 머신 밖의 노드를 쓰는 배포는 검증하지 않았다([서버·노드 배포](#서버노드-배포)).

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
- `--public-origin`은 사용자가 브라우저에 입력하는 `https://HOST[:PORT]`다. 여러 번 줄 수 있고 첫 값이 알려 줄 주소다. path·query·사용자 정보는 받지 않는다. 이 머신의 이름과 주소는 따로 주지 않아도 받는다([받는 주소](#받는-주소)). 인증서의 이름이 브라우저가 연 host와 맞아야 경고 없이 연결한다.
- TLS 1.2 이상, HTTP/1.1이다. 세션 cookie는 `__Host-codeye_session`(Secure)이고 HSTS(`max-age=31536000`)를 보낸다. 같은 포트로 평문 HTTP 요청을 보내면 API를 주지 않는다.
- 인증서 갱신은 재시작으로 반영한다.
- Host 검사는 노드 경로에도 적용되므로 노드 에이전트도 `--controller https://HOST[:PORT]`(public origin)로 접속한다. 에이전트가 사설 CA나 자체 서명 인증서를 신뢰하게 하는 설정은 시험하지 않았다.
- TLS를 앞단 reverse proxy에서 종료하는 배포는 지원하지 않는다. 서버가 forwarded 헤더를 읽지 않아 접속 주소별 로그인 제한과 Origin·Host 검사가 맞지 않는다.
- `codeye up`도 같은 수신을 한다. `codeye up --listen 0.0.0.0:7443 --tls-cert … --tls-key … --public-origin https://HOST:7443`이 설정 파일에 값을 저장하고 controller에 넘긴다. 같은 머신의 에이전트와 명령은 loopback 주소(`local_listen`, 기본 공개 포트 + 1)로 붙는다. 출력하는 로그인 링크와 "Other people open …" 줄은 알려 줄 주소를 쓰고, 그 아래에 받는 주소 전부를 묶어 출력한다.
- **평문 HTTP.** 믿을 수 있는 LAN에서는 `--plain-http`(설정 `plain_http: true`)로 TLS 없이 받는다. `0.0.0.0`·`[::]`에도 `--public-origin`이 필요 없다. 시작할 때 `plain HTTP on ADDR … sign-in cookies and passwords travel unencrypted on this network` 한 줄을 출력한다. 세션 cookie는 `codeye_session`이고 Secure가 아니며 HSTS를 보내지 않는다.
- `codeye login-link --origin`은 `https://HOST[:PORT]`와 평문 HTTP의 `http://HOST[:PORT]`(loopback이 아닌 host)도 받는다.

### 받는 주소

사람이 서비스를 여는 주소는 네트워크마다 다르다(LAN, VPN, 이 머신의 이름). loopback이 아닌 주소(`0.0.0.0` 포함)로 들으면 controller는 다음 Host를 모두 받는다. 따로 정할 것은 없다.

- 이 머신의 이름과 주소에 listen 포트를 붙인 것: `localhost`, 모든 loopback 주소(`127.0.0.0/8`, `::1`), 호스트 이름과 `호스트이름.local`, `/etc/resolv.conf`의 search domain을 붙인 이름(예: Tailscale MagicDNS의 `main.tail1234.ts.net`), 네트워크 인터페이스의 모든 주소(IPv4, 대괄호의 IPv6). 인터페이스는 몇 초마다 다시 읽어 나중에 켠 VPN 주소도 받는다. 이름은 정확히 같아야 하고(대소문자 무시) 앞부분만 같은 이름(`main.evil.example`)은 받지 않는다. 기본 포트(평문 80, TLS 443)면 포트 없는 Host도 받는다. DNS는 묻지 않는다.
- `--public-origin`(설정 `public_origins`)의 Host. 이 머신의 것이 아닌 주소, 예를 들어 다른 머신이 포트를 전달해 주는 주소나 DNS 이름을 더한다.

loopback으로만 들으면(`--listen` 없는 기본 `codeye up`) 다른 사람이 닿을 수 없으므로 loopback 이름(`127.0.0.1`, `localhost`, `[::1]`, 듣는 주소)과 `--public-origin`의 Host만 받는다. 호스트 이름이나 다른 인터페이스 주소는 받지 않는다. 그 이름을 `127.0.0.1`로 풀리게 만든 웹 페이지가 들어오지 못하게 하려는 것이다.

상태를 바꾸는 요청의 `Origin`은 그 요청의 scheme + Host와 같아야 한다. 받는 주소 A로 연 화면이 받는 다른 주소 B의 Origin을 보내면 거절한다. 세션 cookie는 연 주소마다 따로다.

`codeye up`은 받는 주소를 묶어 출력하고 관리 화면의 "이 서비스가 받는 주소"도 같다. 순서는 설정한 주소, LAN, VPN(Tailscale `100.64.0.0/10`·`fd7a:115c:a1e0::/48`, 이름이 `tailscale`·`wg`·`zt`·`tun`인 인터페이스), 이 머신 이름, 이 머신에서만(loopback)이다. 컨테이너 bridge(`docker*`, `br-*`, `veth*`, `virbr*`, `cni*`, `podman*` 등)와 link-local 주소는 보이지 않을 뿐 받는다. 특정 주소를 들으면 그 주소와 설정한 주소만 보인다. 알려 줄 주소(`Other people open …`, `service_origin`)는 첫 설정 주소, 없으면 첫 LAN, VPN, 이름 주소다. 관리 화면에서 만든 로그인 링크는 관리자가 지금 연 주소를 쓴다. 관리자가 loopback으로 열었으면 알려 줄 주소를 쓴다.

받지 않는 Host의 요청은 403 `HOST_REJECTED`다. 브라우저가 연 화면이면 쓴 주소, 받는 주소, 그 주소를 더하는 명령(`codeye up --public-origin http://그주소:PORT`, 이미 설정한 주소도 함께)을 한국어 화면으로 보이고, API는 같은 내용을 `message`와 `details`(`host`, `accepted_origins`, `add_command`)로 준다. 쓴 주소는 `host[:port]` 모양일 때만 되풀이한다. 받는 주소 목록은 Host를 틀린 누구에게나 보이므로 이 머신의 인터페이스 주소가 드러난다([보안](SECURITY.md#네트워크와-브라우저)). `codeye up --public-origin …`만 주면 `public_origins`만 바꾸고(그 값으로 바꾼다) 듣는 방식은 그대로 둔다. 실행 중인 controller의 인자가 설정과 달라지면 `up`이 controller만 다시 띄운다(에이전트와 실행 중인 작업은 그대로).

## codeye tools

```text
codeye tools [--config PATH] [--json | --checklist]
```

이 머신에서 CODEYE가 쓸 수 있는 도구를 찾아 표로 보인다. 찾은 도구는 경로·버전을, 없는 도구는 한 줄 추가 방법을 보인다. `--json`은 같은 결과를 [tools schema](../schemas/tools.schema.json)의 문서로 낸다. `--config`(또는 기본 설정 파일)의 `agent.go`·`agent.tools`·`agent.model.opencode`는 탐색보다 우선하고, 설정 파일 없이도 동작한다. 설정이 있고 그 `state_dir`가 이미 있으면 결과를 `<state_dir>/tools.json`에 쓴다. 그 밖에는 아무것도 만들거나 설치하지 않는다. 탐색 규칙은 [도구 찾기](ANALYZERS.md#도구-찾기)다. 종료 코드는 도구가 없어도 0이고, 설정 오류만 2다. `--checklist`는 폐쇄망용 Markdown 점검표다([분석 도구 옮기기](#분석-도구-옮기기)).

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

**`--config` 운영 점검.** 서비스 설정 파일 경로를 주면 선택 필드 `operations`가 붙는다. `--config`가 없으면 출력에 이 필드가 없다. 종료 코드 규칙은 같다. 화면은 아직 이 값을 표시하지 않는다.

- `storage[]`: 다섯 영역마다 `status`(`within_limit`·`over_limit`·`no_limit`·`unmeasured`), `bytes`, `limit_bytes`, `filesystem_available_bytes`(statfs, Linux 외 null), `incomplete`, `enforcement`(`claim_refusal_node_heartbeat`·`claim_refusal_database_accounting`).
- `backup`: `backup_dir`의 가장 최근 완성 세트(manifest 형식이 맞는 것). `status`는 `not_configured`·`none`·`unreadable`·`recent`·`stale`(7일 초과)이고 `latest`, `created_at`, `age_seconds`가 붙는다. 세트 검증(hash)은 하지 않는다. 검증은 restore가 한다.
- `processes`: [프로세스 상태 일관성](#프로세스-상태-일관성) 검사. `status`는 `consistent`·`inconsistent`·`unsupported`, 역할별 상태, `issues`다. start·stop이 진행 중이면 `operation_in_progress`이고 에이전트 lock은 확인하지 않는다.

schema는 선택 속성 하나만 더했다. 상태와 필드의 조합(`unmeasured`면 `bytes`가 null, `consistent`면 문제 없음 등)은 `if/then`으로 묶었다. 웹의 Ajv strict 검증기도 이 schema로 생성한다.

## 서비스 설정 파일

기본 경로는 `$XDG_CONFIG_HOME/codeye/config.json`이며 `XDG_CONFIG_HOME`이 없으면 `~/.config/codeye/config.json`이다. `up`·`open`·`start`·`stop`·`status`·`backup`·`restore` 등 서비스 명령은 모두 `--config PATH`를 받는다. `codeye up`은 파일이 없으면 `{"schema_version": "0.1"}`만 담아 만들고(디렉터리 0700, 파일 0600), 그 뒤 자기가 다루는 필드(`--offline`의 `offline`, `--postgres-bin-dir`의 `database.postgres_bin_dir`, `--listen`·`--tls-cert`·`--tls-key`·`--public-origin`·`--plain-http`의 수신 필드)만 고친다. 다른 필드와 순서는 그대로이고 기본값은 쓰지 않는다. 고친 결과가 검증을 통과할 때만 바꾼다(같은 디렉터리의 rename, symlink면 그 대상). 형식은 JSON 하나다. 알 수 없는 필드, 뒤에 붙은 값, 상대 경로, 정리되지 않은 경로는 거부한다. 오류 메시지에는 설정 경로나 거부된 값을 넣지 않는다. 예시는 [examples/config.json](../examples/config.json)이고, 이 빌드의 도구 이름으로 읽힌다는 시험이 있다. 코드는 [internal/instance/config.go](../internal/instance/config.go)다.

| 필드 | 기본값 | 검증 |
|---|---|---|
| `schema_version` | (필수) | `"0.1"` |
| `state_dir` | `$XDG_STATE_HOME/codeye`, 없으면 `~/.local/state/codeye` | 절대 경로. 0700으로 만들고, 소유자가 다르거나 symlink면 거부한다. source root를 품으면 안 된다 |
| `listen` | `127.0.0.1:7331`(쓰이고 있으면 7340까지 다음 빈 포트) | IP와 0이 아닌 포트. loopback이 아니면 TLS 필드나 `plain_http`가 있어야 한다. 적으면 그 주소만 쓴다. 기본값에서 옮긴 주소는 controller 기록(`run/controller.json`의 `listen`)에 남고 `stop`·`status`·`open`이 그것을 쓴다. `up --listen`이 저장한다 |
| `tls_cert`, `tls_key` | 빈 값 | 둘 다 절대 경로(PEM). 있으면 `public_origins`(`https://`)가 하나 이상 필요하고 `plain_http`와 함께 쓰지 않는다. `up --tls-cert`·`--tls-key`가 저장한다([TLS 수신](#tls-수신)) |
| `public_origins` | 없음 | 다른 사람이 여는 주소 중 이 머신의 것이 아닌 주소의 목록(최대 20). TLS면 `https://HOST[:PORT]`, 평문이면 `http://HOST[:PORT]`이고 첫 값이 알려 줄 주소다. TLS나 `plain_http` 없이는 받지 않는다. `up --public-origin`(여러 번)이 저장한다([받는 주소](#받는-주소)) |
| `plain_http` | false | true면 loopback이 아닌 `listen`을 TLS 없이 받는다(시작할 때 한 줄 경고). `up --plain-http`가 저장한다 |
| `local_listen` | `127.0.0.1:<listen 포트 + 1>`(TLS이거나 `listen`이 loopback이 아닐 때만 쓴다) | loopback 주소와 0이 아닌 포트. 이 머신의 에이전트와 명령이 controller에 붙는 주소 |
| `web_dir` | 실행 파일 옆 `web/dist`, 없으면 `../web/dist`(`index.html`이 있을 때), 아니면 빈 값(API만) | 빌드된 `web/dist`의 절대 경로 |
| `database.mode` | `managed` | `managed`(상태 디렉터리의 PostgreSQL, [관리형 PostgreSQL](#관리형-postgresql)) 또는 `external`(`CODEYE_DATABASE_URL`) |
| `database.postgres_bin_dir` | 빈 값 | managed: 처음 찾는 곳이다. 적은 디렉터리에 필요한 프로그램이 없으면 다른 곳을 찾지 않고 오류다. external: `pg_dump`·`pg_restore`가 있는 절대 경로. PATH에서 찾지 않는다. 백업·복원에 필요하다 |
| `source_roots[]` | 없음(0~32개). 홈 디렉터리는 적지 않아도 root `home`이다(설정에 쓰지 않는다. 적은 root가 홈을 덮거나 홈이 상태 디렉터리 안이면 더하지 않는다) | `label`(1~100자, 앞뒤 공백 없음), 절대 `path`. label과 path는 서로 유일해야 한다. root는 `state_dir` 안이면 안 된다(홈처럼 `state_dir`를 품는 root는 된다. 그 안의 데이터 폴더를 품는 프로젝트는 거부한다). 선택 `repository_code`·`model_review`는 받기만 하고 `up`이 띄운 에이전트는 쓰지 않는다. 프로젝트별 분석 선택과 관리자 스위치가 정한다([안내서](GUIDE.md#프로젝트별-분석-선택)). 웹 또는 CLI에서 처음 입력한 읽을 수 있는 경로는 DB에 매핑을 저장한다 |
| `offline` | false | true면 [오프라인 모드](#오프라인-모드). `codeye up --offline`이 이 값을 저장한다 |
| `ca_bundle` | 빈 값 | 시스템 CA와 사내 CA를 합친 PEM 파일의 절대 경로([사내 CA](#사내-ca)) |
| `git_hosts` | `["github.com"]`(필드가 없을 때, 오프라인이면 `[]`) | 서로 다른 DNS 호스트 이름(선택 포트) 0~16개, 소문자로 바꾼다. `up`·`start`가 호스트마다 provider를 등록하고(`github.com`은 `github`, 그 밖은 `ghes`) 찾은 git과 함께 에이전트에 `--git`·`--git-host`로 넘긴다. 빈 목록이면 Git 주소를 받지 않는다. 오프라인에서는 여기에 명시한 사내 호스트만 사용한다 |
| `github.enabled`, `github.token_env` | `true`, 없음 | `token_env`의 키는 `git_hosts`의 호스트, 값은 토큰을 받을 환경변수 이름이다. [플랫폼 GitHub 계정](GITHUB.md#플랫폼-github-계정) |
| `agent.name` | `local` | 100자 이하 |
| `agent.max_workers` | min(4, 논리 CPU 수/2), 최소 1 | 1~16. 에이전트는 `--autoscale --max-workers N`으로 시작하고, `up`·`start`가 `default` 풀의 최대 워커 수를 같은 값으로 둔다 |
| `agent.toolchain_path` | `/usr/bin:/bin`(Linux) | 절대 경로 목록 |
| `agent.java_classpath_catalog` | 비어 있음 | SpotBugs가 사용할 로컬 JAR 카탈로그의 절대 경로. [형식과 사용법](ANALYZERS.md#java-로컬-클래스패스) |
| `agent.go`, `agent.go_mod_cache` | 빈 값 | 절대 경로. 비우면 [탐색](ANALYZERS.md#도구-찾기)이 찾은 go와 그 모듈 캐시를 쓴다 |
| `agent.tools` | 빈 값 | 이 빌드의 분석기·toolchain 플래그 이름(`staticcheck`, `node`, `ruff` 등)을 키로, 실행 파일의 절대 경로를 값으로. 모르는 이름은 거부한다. 적지 않은 도구는 탐색이 찾은 것을 쓰고, 적은 값은 항상 우선한다 |
| `agent.python_both_type_checkers` | false | mypy·pyright를 모두 실행. 탐색은 둘 다 찾으면 켠다 |
| `agent.dependencies` | 없음(의존성 받기 켜짐) | `enabled`(기본 true, false면 에이전트에 `--dependency-fetch`를 주지 않는다), `cache_dir`(절대 경로, 기본 `<state_dir>/cache/deps`), `go_proxy`·`go_sumdb`·`go_private`·`go_noproxy`·`go_nosumdb`(비우면 탐색이 읽은 사용자 `go env` 값, 그것도 없으면 Go 기본값), `npm_registry`(인증정보 없는 http(s) URL, 비우면 탐색이 읽은 사용자 npm 레지스트리, 그것도 없으면 npm 기본값), `pip_index_url`(Python 패키지 index, 비우면 PyPI. 인증정보가 든 URL은 설정 오류), `timeout_seconds`(기본 600), `total_timeout_seconds`(기본 1200), `max_download_bytes`(기본 2 GiB). 프로젝트별로 끄는 것은 "고급"의 "의존성 받기"다([의존성 준비](ARCHITECTURE.md#의존성-준비)) |
| `agent.auto_detect` | true | `false`이면 도구를 탐색하지 않고 위 명시 설정만 쓴다 |
| `agent.model` | 탐색한 실행 파일과 서비스 계정의 설정 | `opencode`, `config_file`(OpenCode JSON/JSONC), `credential_file`(CODEYE 키 파일 또는 OpenCode `auth.json`)은 절대 경로다. `name`으로 모델을 지정한다. `relay_file`은 설정·인증 파일의 대안이다. `timeout_seconds`는 호출당 1~1800초(기본 180), `review_max_calls`는 attempt당 15~2,013회(기본 99)다. 일반 분석은 최대 207회, 확장 분석은 그보다 높은 설정값까지 쓸 수 있다. [모델 설정](GUIDE.md#모델-설정) · [검토 설정 해석](REVIEW.md#모델-설정) |
| `quota.*_bytes` | 0(상한 없음) | `snapshot`·`workspace`·`cache`·`logs`·`artifacts`, 음수 거부 |
| `backup_dir` | 빈 값(백업은 `<state_dir>-backups`에 만든다) | 절대 경로. `state_dir` 안이거나 `state_dir`를 포함하면 거부 |
| `stop.drain_timeout_seconds` | 300 | 1~86400 |
| `stop.cancel_timeout_seconds` | 90 | 45~3600 |

- 설정에는 DB URL이나 토큰을 쓰지 않는다. external 모드의 DB 연결은 실행할 때의 환경변수 `CODEYE_DATABASE_URL`만 쓰고, `codeye` 프로세스 환경에 `PG*` 변수가 있으면 DB 열기를 거부한다(fail closed). 관리형 모드의 비밀번호는 상태 디렉터리의 `postgres/password`에만 있다.
- Git 저장소는 프로젝트로 등록한다(`codeye up https://github.com/OWNER/REPO` 또는 화면). 비공개 저장소는 승인된 호스트와 플랫폼 계정을 설정한다. [Git 연결](GITHUB.md)
- 모델 API 키는 설정 파일에 쓰지 않는다. `<state_dir>/secrets/model.json`(0600, 디렉터리 `secrets/` 0700)이나 `agent.model.credential_file`, 사용자의 OpenCode `auth.json`에서 워커가 모델을 호출할 때마다 읽는다. 에이전트 인자에는 키 파일 경로만 있다. 백업은 산출물과 DB만 담으므로 키 파일은 백업에 들어가지 않는다. 복원한 설치에서는 키를 다시 둔다(`codeye up`).

## up, open, start, stop, status

```text
codeye up     [PATH | GIT-URL] [--config PATH] [--postgres-bin-dir DIR] [--no-open] [--no-probe] [--offline]
              [--listen ADDR] [--tls-cert PEM --tls-key PEM --public-origin https://HOST[:PORT]... | --plain-http [--public-origin http://HOST[:PORT]]...]
codeye open   [--config PATH] [--no-open]
codeye start  [--config PATH]
codeye stop   [--config PATH] [--format text|json]
codeye status [--config PATH] [--format text|json]
```

`codeye --help`는 자주 쓰는 명령(서비스 띄우기와 분석, 다른 머신에 열기, open·status·stop·tools·doctor)을 먼저 보이고, 그다음 운영(백업·복원, 정리, 사용자 서비스, 실행 복구, 모델 확인), 따로 띄운 controller와 노드의 관리 명령 순으로 묶는다. `codeye COMMAND --help`(또는 `-h`)는 그 명령의 사용법만 보이고 0으로 끝난다(`codeye user --help`는 user 명령 모두).

### 상태 디렉터리

배치는 모두 0700 디렉터리, 0600 파일이다.

| 경로 | 내용 |
|---|---|
| `run/controller.json`, `run/agent.json` | pid, 프로세스 시작 시각(`/proc/PID/stat` starttime), 실행 파일, 기록 시각 |
| `run/enroll-token` | 일회용 등록 토큰. 등록·heartbeat 확인 직후, 그리고 실패한 start의 끝에 지운다 |
| `admin.json` | 관리자 사용자 ID(`up`·`start`·`open`이 로그인시키는 사용자) |
| `login-link` | 일회용 로그인 링크(`codeye start`가 쓴다) |
| `postgres/data` | 관리형 PostgreSQL cluster(`initdb`가 만든 0700 디렉터리). `codeye.conf`는 시작마다 다시 쓰는 listen 주소·포트·socket 설정이다 |
| `postgres/password` | 관리형 PostgreSQL의 생성 비밀번호(0600) |
| `postgres/run` | 관리형 PostgreSQL의 Unix socket 디렉터리 |
| `logs/postgres.log` | 관리형 PostgreSQL 로그 |
| `instance.lock` | up·start·stop·backup·restore의 상호 배제 |
| `logs/controller.log`, `logs/agent.log` | 자식 프로세스 출력(O_APPEND) |
| `agent/` | 에이전트 상태 디렉터리(journal, node.json, 작업 사본) |
| `artifacts/` | controller 산출물 저장소 |
| `secrets/model.json` | 모델 API 키 파일(`codeye up`이 환경변수에서 쓴다). 워커의 모델 호출만 읽는다 |
| `secrets/github-platform.json` | 호스트별 플랫폼 GitHub 인증정보(0600). 컨트롤러와 워커의 Git 소스 확보만 읽는다. 백업에 포함하지 않는다 |
| `run/setup.json` | 화면의 설치 정보(`GET /setup`): 모델 ID와 마지막 확인 결과, git 사용 가능 여부와 호스트, 백업 디렉터리. 키는 없다 |
| `tools.json` | `codeye up`·`start`가 마지막으로 찾은 도구([codeye tools](#codeye-tools)) |
| `run/model-probe.json` | 마지막 모델 확인의 설정 지문(모델 ID·경로·키 파일 시각과 크기)과 결과. 키에서 나온 값은 없다 |

### start

서비스 lock(`instance.lock`)을 잡는다. 관리형 모드면 PostgreSQL을 띄운다([관리형 PostgreSQL](#관리형-postgresql)). 상태를 검사한다([일관성](#프로세스-상태-일관성)). 기록 없는 프로세스가 에이전트 lock이나 설정에 적은 `listen` 포트를 쥐고 있으면 거부한다. `listen`을 적지 않았고 기본 포트를 다른 프로그램이 쓰면 7340까지 다음 빈 포트를 쓴다. 기록의 프로세스가 끝났거나 pid가 다른 프로세스로 재사용됐으면 기록만 지운다(신호 없음). 그 뒤 순서는 다음과 같다. controller와 에이전트 중 이미 실행 중인 것은 그대로 쓴다. 둘 다 실행 중이고 에이전트 argv가 설정과 같으면 아무것도 띄우지 않고 종료 코드 0이다.

1. schema가 이 바이너리의 migration(`codeye version --json`의 `migrations`)과 같은지 확인한다. external 모드에서 뒤처진 schema는 `run "codeye migrate" explicitly` 오류로 끝난다. 관리형 모드에서 이전 버전이면 migration을 적용한다. 이미 적용된 migration이 있으면 먼저 `pg_dump --format=custom`으로 `<백업 디렉터리>/codeye-pre-migrate-<UTC>/database.dump`(0600)를 쓴다. dump가 실패하면 schema를 바꾸지 않는다. 바이너리보다 새 schema나 적용 이력 불일치는 거부한다.
2. 관리자 사용자를 준비한다. 설정의 source root를 label로 등록하고 `quota.artifacts_bytes`를 적용한다. 승인된 Git 호스트의 `github.token_env`를 읽어 토큰을 확인하고 플랫폼 인증정보 파일을 교체한다.
3. 등록이 필요한지 정한다. 처음 시작했거나, 노드가 철회됐거나, DB가 노드를 모르거나(복원 뒤), source root 집합이 바뀌었으면 다시 등록한다. 다시 등록하기 전에 에이전트 journal에 끝나지 않은 작업이 없는지 확인한다. 이전 노드는 철회하고, 이전 `agent/`는 `agent.retired-<ns>`로 이름만 바꾼다(삭제하지 않는다). 등록 토큰은 10분짜리 일회용이며 0600 파일로만 전달한다.
4. `codeye serve --session-ttl 168h --state-dir <state_dir>`을 setsid로 띄운다(설정의 수신 필드가 있으면 `--tls-cert`·`--tls-key` 또는 `--plain-http`, 설정한 주소마다 `--public-origin`, `--local-listen`도 준다. 브라우저 세션 7일, 경로로 등록·`GET /setup`·플랫폼 GitHub 인증정보·데이터 폴더 검사에 상태 디렉터리를 쓴다). 환경은 PATH·HOME·LANG·TZ와 `CODEYE_DATABASE_URL`뿐이다. `/api/v1/ready`를 30초까지 기다린다. 기록에 listen 주소와 argv를 남긴다. 실행 중인 controller의 기록 argv가 설정에서 만든 argv와 다르면(예: `public_origins`를 바꿈) controller만 SIGTERM으로 멈추고(45초) 다시 띄운다. 에이전트는 그대로 두고 다시 붙는다.
5. `codeye agent`를 띄운다. 에이전트에는 PATH·LANG과 HOME·XDG 설정·데이터 디렉터리를 전달한다. 워커는 분석별 임시 HOME을 쓰며 DB 인증정보를 받지 않는다. 에이전트는 `--model-instance-config`의 서비스 설정에서 새 분석의 모델을 읽는다. 프로젝트 분석 선택과 배정의 관리자 정책을 따르며 상태·백업 폴더와 겹치는 소스는 거부한다. 노드 heartbeat를 최대 120초 기다린다. 이미 실행 중인 에이전트의 시작 인자가 바뀌면 drain하고, `stop.drain_timeout_seconds` 내에 끝났을 때 다시 띄운다. 시간이 지나면 에이전트가 진행 중인 작업을 끝낸 뒤 종료한다.
6. 실패하면 이 명령이 띄운 프로세스를 역순으로 SIGTERM하고 45초 기다린다. 끝나지 않은 프로세스의 기록은 지우지 않는다. 이 명령이 띄운 관리형 PostgreSQL은 다른 CODEYE 프로세스가 없으면 다시 멈춘다.

성공하면 새 로그인 링크를 `login-link`에 쓴다.

### up

`codeye up [PATH|GIT-URL]`은 설정 파일이 없으면 만들고 start와 같은 순서를 밟은 뒤 로그인 링크를 출력하고 브라우저를 연다. `://`가 있거나, 있는 경로가 아니고 `HOST/OWNER/REPO` 모양이면 Git 주소다. `https://`만 받고(없으면 붙인다), 호스트가 `git_hosts`에 없거나 저장소 주소 모양이 아니거나 git을 찾지 못했으면 아무것도 쓰거나 띄우기 전에 종료 코드 2로 끝난다. `…/tree/BRANCH`는 그 브랜치, 그 밖에는 기본 브랜치다.

1. `--postgres-bin-dir`가 설정과 다르면 `database.postgres_bin_dir`에 저장한다.
2. PATH를 절대 경로로 해석해 서비스 계정으로 열어 확인한다. 상태·백업 디렉터리와 겹치는 경로는 거부한다. 기존 root 매핑을 재사용하거나 DB에 새 경로를 기록한다.
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

`database.mode`가 `managed`(기본)이면 CODEYE가 상태 디렉터리에 전용 PostgreSQL cluster를 만들고 띄운다. 코드는 [internal/instance/pgcluster.go](../internal/instance/pgcluster.go)다. PostgreSQL은 내려받거나 설치하지 않는다.

**프로그램 찾기.** PostgreSQL 14 이상의 `initdb`·`pg_ctl`·`postgres`·`pg_dump`·`pg_restore`가 같은 디렉터리에 있어야 한다. 설치 위치는 `up --postgres-bin-dir`, `database.postgres_bin_dir`, 환경변수 `CODEYE_POSTGRES_BIN_DIR` 순으로 지정할 수 있다. 지정한 위치에 필요한 프로그램이 없으면 오류에 이유를 표시한다. 자동 탐색은 PATH, `/usr/lib/postgresql/*/bin`, `/usr/pgsql-*/bin`, `/usr/local/pgsql/bin`을 사용한다. 기존 cluster의 `PG_VERSION`에 맞는 버전을 우선하고 새 설치는 가장 높은 버전을 고른다.

**데이터와 포트.** CODEYE는 `<state_dir>/postgres/data`에 UTF-8 cluster와 전용 DB를 만들고 인증 비밀번호를 `postgres/password`(0600)에 저장한다. 다시 시작할 때 데이터와 프로그램의 주 버전이 달라졌다면 이전 버전의 프로그램을 지정한다. PostgreSQL을 올리려면 이전 버전에서 백업하고 새 `state_dir`에 복원한다. 수신 주소는 `127.0.0.1`이다. 이전 포트나 55432부터 첫 빈 포트를 쓴다. 문제는 `<state_dir>/logs/postgres.log`에서 확인한다.

**환경.** PostgreSQL 프로그램은 `LC_ALL=C`, `LANG=C`, 프로그램 디렉터리와 `/usr/bin:/bin`의 PATH만 받는다. 셸의 `PG*` 변수와 `CODEYE_DATABASE_URL`은 이 모드의 모든 서비스 명령이 프로세스에서 지우고 `note: ignoring … from the environment`로 알린다. 저장 계층의 fail closed 규칙은 그대로다.

**명령별 동작.**

| 명령 | 관리형 PostgreSQL |
|---|---|
| `up`, `start` | 없으면 초기화, 멈춰 있으면 시작, DB가 없으면 생성, schema 맞춤 |
| `stop` | controller와 에이전트가 모두 멈춘 뒤 `pg_ctl stop -m fast` |
| `status`, `open` | 실행 중이면 연결한다. 시작하지 않는다 |
| `backup` | 멈춰 있으면 띄워 백업하고 다시 멈춘다. `pg_dump`는 cluster의 프로그램이다 |
| `restore` | 새 `state_dir`에 cluster와 빈 DB를 만들고 복원한다. 띄운 PostgreSQL은 끝나면 멈춘다 |
| `doctor --config` | 실행 중이면 그 cluster를 진단한다. 시작하지 않는다 |

## 외부 PostgreSQL

기존 서버를 쓰려면 CODEYE 전용의 빈 DB와 schema owner 계정을 준비한다. 서비스 설정의 `database.mode`를 `external`로 바꾸고, 백업·복원에 사용할 `pg_dump`·`pg_restore`의 절대 디렉터리를 `database.postgres_bin_dir`로 지정한다. 데이터베이스 URL은 저장소 바깥의 읽기 제한 파일에 두고, 서비스 명령을 실행하는 셸에서 `CODEYE_DATABASE_URL`로 전달한다.

```sh
export CODEYE_DATABASE_URL="$(cat ~/.config/codeye/database-url)"
codeye migrate
codeye up
```

연결 예는 `postgresql://USER:PASSWORD@127.0.0.1:5432/DBNAME?sslmode=disable`이다. loopback 밖에서는 `sslmode`를 생략해 기본 `verify-full`을 쓴다. `PG*` 변수는 연결을 열기 전에 셸에서 제거한다. 이후 schema 갱신은 백업 후 `codeye migrate`로 적용한다.

## 배포판 업그레이드

기존 설치에서 쓰던 Linux 계정으로 작업한다. 같은 설정 파일, `state_dir`와 DB를 사용하면 기존 웹 계정·프로젝트·실행·보고서를 열 수 있다. 기본 경로를 썼다면 `HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME` 값도 기존 실행 환경과 맞춘다. `state_dir`에 관리형 PostgreSQL 데이터와 산출물이 있으므로 배포판을 푸는 위치와 구분한다. 외부 PostgreSQL은 기존 연결 설정 `CODEYE_DATABASE_URL`을 사용한다. [서비스 설정 파일](#서비스-설정-파일) · [상태 디렉터리](#상태-디렉터리)

1. 머신의 OS와 CPU에 맞는 새 묶음과 SHA-256 검증 파일을 받아 기존 배포판과 다른 디렉터리에 푼다. 묶음의 `INSTALL.md`에 따라 압축 파일과 내용물을 확인한다. 이전 배포판의 실행 파일과 웹 파일은 복구에 쓸 수 있게 보관한다.
2. 아래 경로를 자신의 설치에 맞게 지정한다. 이전 배포판으로 버전과 상태를 확인하고 서비스를 정지한다. 종료 코드 0과 `stopped` 또는 `not_running` 결과를 확인한 뒤 백업한다. `recovery_required`나 `failed`가 나오면 문제를 먼저 해결한다. `backup`이 출력한 백업 세트 경로를 기록하고 설정 파일도 접근이 제한된 곳에 별도로 보관한다. [장애 대응](#장애-대응-runbook) · [백업과 복원](#백업과-복원)

   ```sh
   old=/절대/경로/이전-배포판
   new=/절대/경로/새-배포판
   config=/절대/경로/config.json
   "$old/codeye" version --json
   "$new/codeye" version --json
   "$old/codeye" status --config "$config" --format json
   "$old/codeye" stop --config "$config" --format json &&
     "$old/codeye" backup --config "$config"
   ```

3. `web_dir`를 지정했다면 같은 설정 파일에서 새 배포판의 `web/dist` 절대 경로를 가리키게 한다. 필드를 생략한 설치는 새 실행 파일 옆의 `web/dist`를 찾는다. 플랫폼 GitHub를 사용하면 시작 환경에 이전과 같은 호스트별 `github.token_env` 변수를 전달한다. 시작할 때 이 값으로 GitHub 인증 파일을 다시 쓰기 때문이다. 모델 인증 파일, TLS 파일, CA, 웹훅 secret도 사용 중인 경로에 둔다. 오프라인 설정은 기존 설정 파일에서 이어진다. [플랫폼 GitHub 계정](GITHUB.md#플랫폼-github-계정) · [오프라인 모드](#오프라인-모드)
4. 관리형 PostgreSQL은 기존 cluster의 주 버전과 맞는 서버 프로그램을 계속 쓴다. CODEYE의 DB schema가 뒤처졌으면 새 `up` 또는 `start`가 dump를 만든 뒤 schema를 갱신한다. 두 배포판의 migration 번호가 같고 DB의 적용 이력이 일치하면 그대로 시작한다. external 모드에서 schema 갱신이 필요하면 정상 백업을 확인한 뒤 같은 `CODEYE_DATABASE_URL`로 `"$new/codeye" migrate`를 먼저 실행한다. [관리형 PostgreSQL](#관리형-postgresql) · [외부 PostgreSQL](#외부-postgresql)
5. 새 배포판으로 기존 설정을 열고 상태를 확인한다. 아래 `up`에는 프로젝트 경로를 넘기지 않는다. 시작 시 모델 확인 요청을 생략하려면 `--no-probe`를 함께 준다.

   ```sh
   "$new/codeye" up --config "$config" --no-open &&
     "$new/codeye" status --config "$config" --format json
   ```

   systemd 사용자 unit으로 운영하면 `systemctl --user stop codeye`로 정지하고 `status`로 확인한 뒤 백업한다. unit에는 실행 파일 절대 경로가 저장된다. 새 배포판을 시작할 때 `"$new/codeye" service install --config "$config"`로 기존 unit을 갱신하고 `systemctl --user start codeye`를 실행한다. 다른 unit 이름을 쓰는 설치는 같은 이름을 `--name`과 systemctl 명령에 지정한다. [사용자 서비스](#사용자-서비스)
6. `status`에서 controller·에이전트·DB가 정상인지 확인한다. 이전에 쓰던 서비스 주소에서 기존 웹 계정으로 로그인해 같은 프로젝트와 보고서를 연다. 관리자용 링크는 `up` 출력에서 받거나 `"$new/codeye" open --config "$config" --no-open`으로 새로 발급한다.

복구가 필요하면 새 서비스를 정지하고 준비해 둔 배포판·설정 사본과 백업 세트를 사용한다. 변경 전 상태로 복구할 때는 백업 당시의 바이너리로 새 `state_dir`에 세트를 복원한다. 외부 PostgreSQL은 빈 전용 DB도 준비한다. 새 schema를 적용한 DB는 이전 바이너리가 읽지 못할 수 있다. 자동 migration 전의 dump는 DB만 담으므로 DB와 보고서 산출물을 함께 복원할 때는 `codeye backup`으로 만든 전체 세트를 쓴다. [백업과 복원](#백업과-복원)

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
- **정리.** 상한에 닿아도 자동으로 지우지 않는다. 공간은 운영자가 [보존 정리](#보존-정리)(`codeye cleanup`, 기본 dry run)로 비운다. 참조 없는 임시 업로드(DB 기록 전에 실패한 업로드 파일) 정리는 없다.
- **측정과 적용의 구분.** `status`는 영역별 `basis`(`node_heartbeat`·`database_accounting`)를 보여 준다. `doctor`는 디렉터리를 지금 직접 재므로 적용 값과 다를 수 있다. 특히 artifacts의 디렉터리 크기와 DB 집계는 다르다.
- 상한 조정: 설정을 바꾸고 `codeye stop`·`codeye start`. 운영자 CLI `codeye scheduler set --artifact-quota-bytes N`(0이면 제거)도 있다.

저장은 [migration 0009](../migrations/sql/0009_storage_quota.sql)의 `nodes.storage_quota_exceeded`(영역 배열, 기본 빈 배열)와 `scheduler_limits.artifact_quota_bytes`(NULL 또는 양수)를 쓴다.

## 백업과 복원

```text
codeye backup  [--config PATH] [--output DIR] [--format text|json]
codeye restore [--config PATH] --from DIR [--format text|json]
```

백업은 전체 세트만 만든다. 증분, 압축, 암호화, 보존 개수, 자동 예약 백업은 없다. 백업 디렉터리 보호(권한·위치·외부 보관)는 운영자 책임이다. 세트에는 프로젝트 이름·경로·보고서와 웹 계정의 비밀번호 해시 등 운영 데이터가 들어 있다. 설정 파일, 모델·GitHub 인증 파일, TLS 키 등은 별도로 보관한다. 코드는 [internal/backup](../internal/backup/backup.go), [internal/pgtool](../internal/pgtool/pgtool.go)다.

### 백업

출력 디렉터리는 없거나 비어 있어야 한다. 기본 위치는 `<backup_dir>/codeye-<UTC 시각>`이고 설정이 없으면 `<state_dir>-backups` 아래에 둔다. 서비스 운영 중에도 백업할 수 있다. 정지 뒤 백업하면 진행 중인 실행의 복구가 필요 없다.

DB 덤프와 확정 산출물 목록은 같은 논리 시점에 읽는다. 파일마다 SHA-256과 크기를 확인해 복사하며, 마지막에 `manifest.json`을 쓴다. manifest가 있는 세트만 유효하다. 백업이 끝났어도 text·JSON 안내를 출력하지 못하면 명령은 종료 코드 1을 반환하고 표준 오류에 저장한 경로를 남긴다. 백업은 서비스 lock을 잡아 start·stop·restore와 겹치지 않는다. `pg_dump`의 주 버전은 DB 서버와 같거나 높아야 한다.

**인증정보.** `pg_dump`·`pg_restore`는 관리형 cluster의 프로그램 디렉터리, external 모드면 `database.postgres_bin_dir`에서만 찾는다. argv에는 플래그와 파일 경로만 들어간다. 연결 정보는 자식 환경의 `PGHOST`·`PGPORT`·`PGUSER`·`PGPASSWORD`·`PGDATABASE`·`PGSSLMODE`·`PGSSLROOTCERT`로만 전달한다. `PGPASSFILE=/dev/null`로 사용자 `.pgpass`를 읽지 않게 하고, cwd는 임시 디렉터리다. 오류 문구의 비밀번호는 `[REDACTED]`로 바꾼다. URL 규칙은 controller와 같다(loopback만 `sslmode=disable`, 그 외 `verify-full`).

### 복원 실행

새 `state_dir`를 지정한 복원용 설정 파일을 따로 준비한다. 외부 DB라면 빈 전용 DB를 만들고 `CODEYE_DATABASE_URL`을 복원 대상에 맞춘다. 복원 뒤 외부 DB의 schema 갱신이 필요하면 `up` 전에 같은 URL로 `codeye migrate`를 실행한다.

```sh
codeye restore --config "$HOME/.config/codeye/restored.json" --from /mnt/backup/codeye-restore-point
```

복원이 끝나면 `codeye up --config "$HOME/.config/codeye/restored.json"`으로 시작한다.

### 복원 전 검증

- 대상 설치가 실행 중이거나 불일치하면 거부한다. 대상 `state_dir`에 `agent/node.json`이나 `admin.json`이 있으면 거부한다. 새 `state_dir`가 필요하다.
- 세트 검증: manifest를 엄격하게 읽고 형식을 확인한다. dump와 목록의 hash·크기, 목록 정렬, 각 내용의 hash·크기, 총계를 확인한다. 하나라도 다르면 대상에 아무것도 쓰지 않는다.
- manifest의 migration 버전이 이 바이너리보다 높으면 거부한다. 낮으면 복원 뒤 관리형 모드의 `up`·`start`가 migration을 적용하고(먼저 `pg_dump` 사본), external 모드의 start는 `codeye migrate`를 요구한다.
- 대상 DB의 현재 schema에 표가 하나라도 있으면 거부한다(`not empty`). 대상 산출물 디렉터리에 파일이 있으면 거부한다. 병합이나 덮어쓰기는 ID 충돌과 근거 혼합 위험이 있어 하지 않는다.

### 복원과 fencing

1. 내용을 산출물 저장소의 쓰기 경로(`artifactstore.Put`)로 넣는다.
2. `pg_restore --list` 결과에서 `SCHEMA public`·`COMMENT ON SCHEMA public` 항목을 뺀다. 그 목록으로 `pg_restore --use-list --exit-on-error --single-transaction --no-owner --no-privileges`를 실행한다.
3. 복원된 행 수가 manifest와 같은지 비교한다. 확정 산출물 행마다 내용이 있는지 다시 hash해 확인한다.
4. **fence.** 한 트랜잭션에서 모든 노드를 철회하고 그 워커를 `stopped`로 둔다. 모든 세션을 철회하고, 쓰지 않은 로그인 토큰을 사용 처리하고, 쓰지 않은 등록 토큰을 철회한다. `leased` attempt는 lease를 지금으로 끝낸 뒤 기존 만료 경로(`ExpireAttempt`)로 `recovery_required`로 둔다. 예약은 유지한다. 대기 중인 run, 예약, 이미 만든 예약 회차는 그대로라서 같은 회차가 다시 만들어지지 않는다.
5. `admin.json`에 manifest의 사용자 ID를 쓴다.

실패하면 복원 과정에서 쓴 대상 산출물을 비운다. `pg_restore`가 성공한 뒤 다른 단계에서 실패했다면 새 빈 DB와 새 `state_dir`를 준비해 다시 복원한다.

복원 뒤 `codeye up`(또는 `start`)은 새 노드로 등록한다. 이전 노드 credential과 세션은 거부된다. 이전 epoch의 lease 갱신은 `ErrStaleAttempt`다. 복원 시점에 실행 중이던 run은 `recovery_required`로 남고 예약을 쥔다. 원본 설치의 그 프로세스가 끝났는지(원본을 `codeye stop`했는지) 확인한 뒤 실행 화면의 "정리 확인 후 다시 실행"이나 `codeye run recover --id RUN_ID --confirm-cleanup`으로 정리를 확인하면 예약이 풀리고 같은 요청의 새 run이 들어간다(운영 훈련 D7에서 확인). 정지 뒤에 백업하면 이 경우가 생기지 않는다.

## 관측성

**측정 대상(설계 계약).** 웹/API 지연, queue wait, active/idle/draining workers, capacity 부족 이유, attempt retry, stale write 거부, tool failure/parser failure, 모델 latency/429/timeouts, artifact queue/용량, 보고서 생성 실패를 측정한다. endpoint별 모델 사용량은 공급자가 제공한 값과 추정치를 구분한다. 코드·프롬프트 전문을 일반 운영 로그에 넣지 않는다.

**지금 구현된 것.** 메트릭 수집·내보내기 endpoint는 없다. 운영자가 볼 수 있는 값은 다음이다.

- `codeye status`와 `GET /api/v1/ready`(DB 연결, schema 상태, 503 사유 `database_unavailable`·`schema_outdated`·`schema_newer`·`schema_inconsistent`).
- run의 이벤트와 진행률, `waiting_reason`(`execution_unavailable`, `pool_paused`, `capacity_full`, `project_limit`, `storage_quota_exceeded` 등), 노드 heartbeat와 워커 상태(`codeye node list`, `codeye pool list`).
- 모든 응답의 `X-Request-ID`, `state_dir/logs/controller.log`·`agent.log`. 로그와 출력에 DB 비밀번호와 토큰은 나오지 않는다.

## 장애 대응 runbook

상황마다 운영자가 보는 것, 할 일, 확인 방법을 적는다. "훈련" 열은 `scripts/test_postgres.py --ops-drill`(TC-255)과 `--multinode`(TC-250~254)가 한 WSL2 호스트에서 실제로 해 본 것이고, 시간은 그 호스트의 측정값이다(보장값이 아니다).

| 상황 | 보이는 것 | 할 일 | 확인 | 훈련 |
|---|---|---|---|---|
| 모델 endpoint 실패 | `codeye model probe`가 `FAILED at request: …`. run은 정적 분석을 마치고 `partial`, 보고서의 모델 검토가 `not_performed`와 사유(`model_unavailable`·`model_timeout`·`model_auth_failed` 등, Markdown에 문장) | 공급자 상태·키 파일·`base_url`을 고친다. 재시작은 필요 없다(키 파일은 호출마다 읽는다). 고친 뒤 다시 시도 | `model probe`가 `OK`, 다음 run의 모델 검토가 `performed` | D1: HTTP 500을 OpenCode가 호출 제한시간까지 재시도해 사유는 `model_timeout`, 제한시간 60초에서 run 136초 |
| 노드 offline(사망) | `codeye node list`의 `last_heartbeat_at`이 20초보다 오래됨, 그 노드만 할 수 있는 run은 `node_stale`·`no_capable_node`로 대기. 실행 중이던 run은 lease 만료 뒤 `recovery_required` | 노드가 돌아오면 기다린다(에이전트가 정리 보고를 보내 run이 `failed`가 되고 다시 시도할 수 있다). 돌아오지 않으면 그 노드에 그 run의 프로세스(`codeye worker`와 자식)가 없는지 확인한 뒤 `codeye run recover --id RUN_ID --confirm-cleanup`(또는 실행 화면의 확인 상자와 "정리 확인 후 다시 실행") | 출력의 `retry`가 `same_run`·`new_run`이고 그 run이 끝난다. 감사 `run.recovery_confirmed` | D2·TC-251: SIGKILL 뒤 `recovery_required` 34~35초, 확인 뒤 다른 노드에서 끝남 |
| 노드와 controller 사이 연결 끊김 | 노드 stale, 노드 로그 `heartbeat_failed`·`lease_lost`. run은 `recovery_required` | 연결을 복구한다. 노드가 돌아오면 정리 보고로 run이 `failed`가 된다. 같은 run은 그 사이 다른 노드에서 실행되지 않는다 | 노드 로그 `cleanup_reported accepted=true`, run `failed`, `POST /runs/{id}/retry`로 다시 실행 | TC-252: 차단 → `lease_lost` 19초, 복구 → `failed` 1초 미만 |
| DB unavailable | `GET /api/v1/ready`가 503 `database_unavailable`, 화면·API가 503. 노드 로그 `heartbeat_failed reply_code=unavailable` | PostgreSQL을 되살린다. controller·에이전트는 재시작하지 않아도 다시 연결한다. lease 여유(20초)보다 오래 끊겼으면 진행 중 작업은 에이전트가 멈췄고 DB가 돌아온 뒤 `failed`가 된다 | `/ready` 200, `codeye doctor`의 database `available`, 정합성 질의(아래) 0건, 새 run이 끝남 | D3: 8초 중단은 작업이 그대로 끝남. 40초 중단은 `lease_lost` 20초, DB 복귀 뒤 4.7초에 `failed` |
| 도구 버전이 바뀜·도구가 사라짐 | 비교의 차이 행 `tool:<analyzer>:version`. 사라진 도구의 작업은 `unsupported`(`tool_not_configured`), 그 진단은 `absent_unverified`(사유 `check_failed`)이며 `resolved_observed`가 아니다 | 의도한 변경이면 둔다. 아니면 에이전트의 도구 경로를 고치고 다시 띄운다 | `codeye tools`·`doctor`의 도구 버전, 다음 비교 | D4: ruff 0.16.3→0.16.4 차이 행, ruff를 뺀 run의 진단 1건 `absent_unverified` |
| 작업 중 max workers 축소 | `codeye pool list`의 `capacity_state=over_target_draining`, `drain_worker` 명령 | 기다린다. 실행 중 작업은 끝까지 돈다. 급하면 run을 취소한다(강제 종료는 없다) | 상태가 `healthy`, 워커 수 ≤ 새 상한, 취소 명령 없음 | D5: 축소 0.4초 뒤 drain, 두 run 모두 첫 attempt로 끝남 |
| 산출물·노드 저장 상한 도달 | 대기 run의 `storage_quota_exceeded`, `codeye status`의 `exceeded`. 기존 보고서는 열린다 | 상한을 올리거나 [보존 정리](#보존-정리)로 공간을 비운다 | 대기 run이 시작됨 | D6: 상한 해제 뒤 바로 시작. D9: 40일 지난 원시 산출물 정리 0.1초 |
| 실행 중 백업을 복원함 | 복원본에서 그 run이 `recovery_required`(fence `runs_recovery_required`) | 원본 설치가 멈췄는지 확인한 뒤 정리를 확인한다(`codeye run recover --id RUN_ID --confirm-cleanup --config PATH`, 복원본의 설정) | 새 run이 끝나고 예약이 풀림 | D7: 백업 0.1~0.3초, 복원 0.2~0.4초(작은 데이터), CLI로 확인 뒤 새 run |
| 사용자 비활성화·구성원 제거 | `codeye user disable`, `DELETE /projects/{id}/members/{user}` | — | 다음 요청이 401(비활성)·404(구성원 아님), 열린 SSE는 `event: closed`(`unauthenticated`·`access_revoked`) | D8: 두 stream이 0.5초 안에 닫힘 |
| 노드 철회 | `codeye node revoke --id NODE_ID`. 실행 중 에이전트는 작업을 취소하고 종료 코드 4로 끝난다 | 그 노드의 run은 정리 보고를 못 보내므로 `recovery_required`로 남는다. 노드 프로세스가 끝났는지 확인하고 정리를 확인한다 | `node list`의 `revoked_at`, run이 다시 실행됨 | D8: 철회 4.9초 뒤 종료 코드 4, 남은 프로세스 없음 |
| 에이전트 갱신 | — | `SIGUSR1`(`codeye up`의 에이전트는 `codeye stop`)로 drain → 종료(코드 0) → 바이너리 교체 → 같은 `--state-dir`·`--controller`로 시작. 등록 token은 필요 없다 | `node list`의 같은 `id`와 `protocol_version`, 새 run을 받음 | TC-254: 다시 빌드 7.5초, 시작 → run 끝 5.2초 |
| stop이 `recovery_required` | `codeye stop`의 `outcome` | `status`·로그 확인 후 `codeye stop` 반복. 프로세스를 강제 종료하지 않는다. 끝내 남은 run은 위 "노드 offline"처럼 정리를 확인한다 | `status`가 `stopped` | TC-125 |
| NFS 지연 | 소스 확보 timeout, `source_unstable` | 원본 경로·권한을 확인한다 | 다음 run의 확보 | 실제 NFS는 시험하지 않았다 |

**DB 정합성 질의.** DB가 돌아온 뒤 sweep(5초) 한 번이 지나면 다음이 모두 0이어야 한다(D3에서 확인).

```sql
SELECT count(*) FROM attempts WHERE status='leased' AND expires_at < clock_timestamp();
SELECT count(*) FROM capacity_reservations c JOIN attempts a ON a.id=c.attempt_id
 WHERE c.released_at IS NULL AND a.status='released';
SELECT count(*) FROM runs r WHERE r.status='running'
 AND NOT EXISTS (SELECT 1 FROM attempts a WHERE a.run_id=r.id AND a.status='leased');
```

**하지 않는 것.** 두 controller를 같은 DB에 띄우지 않는다(지원하지 않음, A51). 정리를 확인하지 못한 run을 시간이 지났다고 다시 실행하지 않는다. 노드 프로세스를 원격으로 죽이지 않는다(SSH 없음).

## 보존 정리

```text
codeye cleanup [--config PATH | --artifact-dir DIR] [--agent-state-dir DIR] [--older-than 30d] [--apply] [--format text|json]
```

기본은 dry run이다. 지울 것을 그대로 보여 주고 아무것도 바꾸지 않는다. `--apply`가 있을 때만 지운다. `--older-than`은 `30d` 같은 일 수나 Go duration(`36h`)이며 1시간보다 짧을 수 없다. 자동·예약 정리는 없다.

| 어디서 | 대상 |
|---|---|
| `codeye up` 서비스(`--config` 또는 기본 설정) | 서비스 lock을 잡고 그 DB(관리형이면 필요할 때 띄웠다가 멈춘다), `<state_dir>/artifacts`, `<state_dir>/agent`와 은퇴한 `agent.retired-*` |
| controller 호스트 | `--artifact-dir DIR`와 `CODEYE_DATABASE_URL` |
| 노드 | `--agent-state-dir DIR`. DB가 필요 없다 |

- **산출물.** 끝난 run(`completed`·`partial`·`failed`·`cancelled`)의 기준보다 오래된 원시 역할 `stdout`·`stderr`·`raw_output`·`other`의 내용만 지운다. 행은 남기고 `content_removed_at`을 적은 뒤(한 번만, migration 0013), 커밋된 다음 남은 행이 참조하지 않는 파일만 지운다. 빈 내용은 지우지 않는다. 감사 `storage.retention_applied`(행위자 `operator_cli`)가 개수·바이트·기준 일수를 남긴다.
- **노드 데이터.** 기준보다 오래된 snapshot 사본(`data/acquire-*`)과 작업 사본(`data/ws-*`), 은퇴한 에이전트 디렉터리. journal에서 끝나지 않은 attempt의 작업 사본은 지우지 않는다. 에이전트가 실행 중이면(state lock) snapshot 사본은 남긴다. 실행 중인 attempt가 그 안에서 일할 수 있기 때문이다. symlink는 따라가지 않는다. 고정 snapshot 재시도가 지운 사본을 찾으면 `snapshot_unavailable`로 끝나고 다음 재시도는 소스를 다시 받는다.
- **남기는 것.** 보고서(저장된 바이트로 그대로 열린다), 관찰(`observations`), `source_manifest`·`result_manifest`, run·이벤트·사람 판정, 감사 행(append-only라 정리 대상이 아니다).
- **보이는 것.** 지운 run의 `GET /runs/{id}`에 `raw_evidence_removed_at`이 생기고 실행 화면이 "원시 증거가 삭제됨"을 보인다. 백업은 지운 내용을 담지 않고 "내용 없는 행"으로 센다. 산출물 상한은 지운 내용을 세지 않는다.

## 사용자 서비스

```text
codeye service install|uninstall|print [--config PATH] [--name codeye]
```

Linux의 systemd 사용자 인스턴스에 `codeye up` 서비스 unit을 둔다. `print`는 unit을 출력만 한다. `install`은 `$XDG_CONFIG_HOME/systemd/user/<name>.service`(없으면 `~/.config/systemd/user/`)에 쓰고 `systemctl --user daemon-reload`와 `enable`을 실행한다. 시작은 하지 않는다(`systemctl --user start <name>`). 이미 `codeye up`으로 띄운 설치를 unit 밖에서 한 번 더 띄우지 않기 위해서다. `uninstall`은 설정 없이 `disable --now`와 파일 삭제를 한다. 두 명령 모두 CODEYE가 쓴 unit(첫 줄 표지)만 바꾸거나 지운다.

- unit은 `Type=oneshot`·`RemainAfterExit=yes`이고 `ExecStart=<codeye> start --config <설정>`, `ExecStop=<codeye> stop --config <설정>`이다. 경로는 절대 경로이고 systemd 특수 문자(`%`, `$`)를 escape한다. PATH는 install할 때의 PATH를 적는다(사용자 manager는 로그인 셸의 PATH를 읽지 않는다). 정지 제한시간은 `stop.drain_timeout_seconds + cancel_timeout_seconds + 60`초다.
- `start`를 쓰는 것은 로그인 링크가 journal에 남지 않게 하기 위해서다. 링크는 `codeye open`으로 새로 받는다.
- 로그아웃 뒤에도 유지하려면 `loginctl enable-linger <사용자>`가 필요하다. CODEYE는 이것을 바꾸지 않는다.
- WSL2(사용자 systemd, `XDG_RUNTIME_DIR=/run/user/1000`)에서 install → start(`running`) → stop(`stopped`, PostgreSQL 정지) → uninstall을 임시 이름과 설정으로 확인했다. 재부팅·로그아웃 뒤 유지는 시험하지 않았다.

## 배포 전 점검

- 인증과 접근: 브라우저 세션, Host/Origin 검사, loopback이 아니면 TLS 또는 명시적 평문 HTTP, 첫 관리자와 사용자 계정, 프로젝트 구성원([보안](SECURITY.md)).
- source root 권한과 `repository_code`·`model_review` 허가를 root마다 의도대로 정했다.
- 도구 사용 조건과 Native 신뢰 범위, timeout과 취소, 자원 reserve(`max_workers`, 머신에 남길 CPU·메모리).
- 모델 한도와 외부 통신 금지. 모델 설정이 없으면 모델 검토가 실행되지 않는다.
- 백업 복원을 실제로 해 보았다. `backup_dir` 보호를 정했다.
- 내보내기에서 코드 발췌와 비밀값이 빠지는지 확인했다.
- `codeye doctor --config PATH`로 도구·DB·저장 상한을 확인했다.
- [예시 설정](../examples/platform.yaml)은 설계 예시이며 이 바이너리가 읽지 않는다. 출발점일 뿐이고 실제 머신 용량을 측정해 조정한다.

## 서버·노드 배포

controller 하나와 노드 에이전트(`codeye serve`·`codeye agent`, 등록·heartbeat·lease·drain·재등록 없는 갱신)는 구현됐고 한 호스트의 노드 신원 여럿으로 시험했다([runbook](#장애-대응-runbook), [실행](EXECUTION.md#여러-노드에서-확인한-것)). 여러 머신에 설치·등록하는 묶음과 아래의 나머지는 설계이고 실제 여러 머신에서 실행하지 않았다.

- controller는 단일 활성 인스턴스로 시작한다. DB와 artifact store를 지정하고 노드 에이전트를 운영자가 직접 설치한다. 서버가 노드에 SSH하지 않는다. 에이전트는 서버에서 작업을 받아 간다.
- Windows Service나 승인된 작업 실행 방식, Linux systemd 등 로그아웃 뒤에도 지속 실행되는 방법을 환경에 맞게 선택한다. `codeye up` 서비스에는 [사용자 서비스](#사용자-서비스)가 있다.
- enrollment token은 짧은 수명의 1회용이며 node identity와 pool·신뢰 범위를 부여한다. 에이전트 업데이트는 drain → binary 교체 → protocol 확인 → 재등록 없이 복귀 순서를 따른다(한 호스트에서 시험함, [runbook](#장애-대응-runbook)). 최소/최대 정책은 controller가 유지한다.
- 배포 manifest에 실제 OS/arch, 빌드 ABI, toolchain, DB, 모델 adapter 버전을 고정한다.
- 서버는 RHEL 8.10과 내부 미러·CA·NFS 조건을 우선 검증할 대상이다. 아직 실행하지 않았다.
- 저장 위치: DB data, workspace, tool/runtime cache, 임시 결과는 로컬 디스크를 우선한다. NFS는 원본 source와 선택적 보관 산출물에 쓴다. DB data를 NFS로 옮기는 것은 별도 DB 운영 검증 없이 기본 지원하지 않는다. node cache를 공용 NFS에 두어 잠금·성능 문제가 생기는 것을 기본 설계로 삼지 않는다.
- 백업·복구 목표 시간은 실제 데이터 규모를 측정한 뒤 정한다. 복구 시험은 프로젝트·run·report·evidence 링크·ACL·예약 중복 방지를 확인한다. `codeye up` 서비스에서 구현한 같은 시점 백업과 lease 비복원 규칙을 서버에도 적용한다.
- 다중 사용자 권한과 감사 운영은 [보안](SECURITY.md)의 설계 계약을 따른다.

## 폐쇄망 배포팩

공개 배포 파일과 옆의 SHA-256 검증 파일을 인터넷이 연결된 머신에서 받아 함께 옮긴다. 설치 대상에서는 묶음의 `INSTALL.md`를 따라 검증하고 `./codeye up --offline`으로 실행한다. 운영자가 배포팩을 직접 만들 때는 연결된 빌드 머신에서 Go·Node와 [scripts/offline_pack.py](../scripts/offline_pack.py)를 쓴다. [배포 파일과 검증 파일](GUIDE.md#첫-시작)

```text
python3 scripts/offline_pack.py build [--out .codeye/pack] [--goos linux] [--goarch amd64] [--skip-web-build] [--allow-dirty]
python3 scripts/offline_pack.py verify codeye-VERSION-linux-amd64.tar.gz | 풀어 놓은 디렉터리
python3 scripts/offline_pack.py check-web web/dist
```

- `build`는 `npm --prefix web run build`(생략 가능), `CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags "-s -w -X main.buildVersion=… -X main.buildCommit=… -X main.buildTime=…"`, 웹 외부 주소 검사, 파일 hash, 묶음 쓰기 순이다. 작업 트리에 커밋하지 않은 변경이 있으면 `--allow-dirty` 없이는 만들지 않는다(그때 버전에 `-dirty`가 붙고 MANIFEST의 `dirty`가 true다).
- 버전은 `git describe --tags --always --dirty`, 빌드 시각은 commit 시각(`SOURCE_DATE_EPOCH`가 있으면 그것)이다.
- **재현성.** 같은 commit·같은 Go·Node 버전·같은 `web/node_modules`에서 두 번 만든 묶음은 바이트가 같다(이 머신에서 새로 clone한 사본으로 두 번 만들어 sha256이 같음을 확인했다). `--skip-web-build`로 만들면 웹을 만든 Node 버전을 모르므로 MANIFEST의 `node_version`이 null이 되어 바이트가 다르다. 항목 정렬, 모든 항목의 mtime을 빌드 시각으로, uid/gid 0, gzip 헤더의 시각 0·파일 이름 없음으로 쓴다. Go 버전, Node·Vite 버전, 의존성 잠금이 다르면 실행 파일·웹 산출물 바이트가 달라질 수 있고 그것까지 고정하지 않는다. 서명은 없다. 무결성은 sha256으로만 확인한다.

### 배포팩 내용

`codeye-VERSION-OS-ARCH.tar.gz`(최상위 디렉터리 하나)와 옆의 `.tar.gz.sha256`이다.

| 파일 | 내용 |
|---|---|
| `codeye` | 정적 링크 실행 파일(CGO 없음). `file`은 statically linked, `ldd`는 not a dynamic executable이고 ELF에 interpreter·dynamic section이 없다. build가 이를 검사해 동적이면 실패한다 |
| `web/dist/` | 빌드된 웹 화면. 외부 주소를 불러오지 않는다(아래) |
| `docs/` | 사용 안내서, 이 문서와 함께 읽는 주제 문서 |
| `CHANGELOG.md` | 배포판별 변경 이력 |
| `examples/config-offline.json` | 오프라인 설정 예시(`*.corp.example` 주소, 실제 주소 아님) |
| `INSTALL.md` | 설치 절차(확인, 놓을 곳, 설정, 점검, 실행) |
| `THIRD_PARTY_NOTICES/` | 함께 배포한 Go·웹 구성 요소의 라이선스 고지 |
| `MANIFEST.json` | `name`, `version`, `commit`, `dirty`, `build_time`, `os`, `arch`, `go_version`, `node_version`, `cgo_enabled`, `linking`, `embedded`, `not_included`, `files[]`(경로·크기·sha256·mode) |
| `SHA256SUMS` | 모든 파일과 MANIFEST.json의 sha256(`sha256sum -c`로 확인) |

- **실행 파일에 들어 있는 것.** DB migration(`migrations/sql`)과 검토 지침(`prompts/review`)은 Go embed로 실행 파일 안에 있다. MANIFEST의 `embedded`가 migration마다 이름·sha256, 지침의 id·version·파일 수·sha256을 적는다. `codeye version [--json]`이 실행 파일의 버전·commit·빌드 시각·Go 버전·OS/arch·최신 migration 번호를 보인다.
- **파일로 싣는 것.** 웹 화면, 문서, 설정 예시. `codeye`는 옆의 `web/dist`를 쓴다.
- **싣지 않는 것**(`not_included`). 분석 도구, 언어 toolchain, PostgreSQL, OpenCode, 프로젝트 의존성. 운영자가 따로 옮긴다([분석 도구 옮기기](#분석-도구-옮기기)).
- **웹 화면이 외부를 부르지 않음.** 응답의 CSP가 `default-src 'self'`라 브라우저가 다른 출처를 불러오지 않는다. 그와 별도로 `check-web`이 `web/dist`의 HTML·CSS·JS에서 `http(s)://`·`//host` 형태의 주소와 원격 source map을 찾아, 식별자·문구로만 쓰이는 주소(`www.w3.org`, `json-schema.org`, React 오류 안내 주소, `codeye.invalid`, 입력란 예시 `https://github.com/owner/repo`) 밖의 것이 있으면 실패한다. CI의 웹 작업이 빌드 뒤 이 검사를 실행하고, `scripts/test_offline_pack.py`가 글꼴·CDN·`@import`·`url()`·`fetch`·원격 source map 각각이 실패하는지 시험한다.

### 오프라인 모드

설정 `"offline": true` 또는 `codeye up --offline`(처음 한 번 주면 설정 파일에 `"offline": true`로 저장된다)이다. 에이전트는 `--offline`으로 받는다. 오프라인 모드가 바꾸는 것:

| 대상 | 오프라인이 아닐 때 | 오프라인 |
|---|---|---|
| Go 모듈 | `go_proxy`, 없으면 사용자 `go env`의 GOPROXY, 없으면 `proxy.golang.org`. checksum DB 기본 `sum.golang.org` | 설정·사용자 `go env`가 정한 proxy만 쓴다. 없으면 운영자 모듈 캐시(`agent.go_mod_cache`)만 읽고, 캐시에 없는 모듈은 `failed`(`offline_no_registry_configured`)이고 `failed_items`에 모듈 이름이 남는다. 캐시도 없으면 받지 않는다(`skipped_policy`, 같은 이유). `go_sumdb`를 정하지 않았으면 `GOSUMDB=off`(go.sum이 여전히 검증하며, go.sum에 없는 hash는 `go_sum_incomplete`). `go_proxy: "off"`이면 받기를 시작하지 않고 같은 이유다 |
| npm·pnpm·yarn | `npm_registry`, 없으면 사용자 npm 설정, 없으면 `registry.npmjs.org` | 설정이나 사용자 npm 설정의 레지스트리가 없으면 설치하지 않는다(`offline_no_registry_configured`) |
| Python | `pip_index_url`, 없으면 PyPI | `pip_index_url`이 없으면 받지 않는다(같은 이유) |
| Rust | crates.io 또는 저장소 Cargo 설정의 레지스트리 | 받지 않는다(같은 이유). cargo에는 CODEYE의 미러 설정이 없다 |
| Git | `git_hosts` 기본 `["github.com"]` | `git_hosts`에 명시한 사내 호스트만 쓴다. 기본값은 빈 목록이다 |
| GitHub | 명시한 `github.token_env`와 승인된 GitHub.com/GHES API를 사용 | `git_hosts`에 지정한 사내 GHES의 인증정보만 읽는다. GitHub.com은 이전 등록 여부와 관계없이 연결하지 않는다 |
| 모델 | OpenCode 설정, API 방식 `auth.json`, CODEYE 키 파일, 환경변수 키 또는 relay | OpenCode 설정의 `options.baseURL`, CODEYE 키 파일의 `base_url`, 또는 relay에 사내 주소를 명시한다. 필요한 인증 파일은 명시한 경로에서 읽는다. 주소가 없으면 `model_endpoint_not_configured`가 보고서에 남는다 |

- 관리형 서비스는 시작할 때 controller에 오프라인 Git 호스트와 CA를 전달한다. 화면용 설정 요약 파일을 읽지 못해도 이 정책을 적용한다. 기존 큐에서 허용되지 않은 호스트의 새 Git 확보는 기다리며, 이미 보관한 스냅샷은 원격 확보 없이 다시 사용할 수 있다.
- 도구 탐지는 원래 이 머신의 파일과 `--version` 실행만 한다. 오프라인 모드와 관계없이 네트워크를 쓰지 않는다.
- CODEYE에는 telemetry·업데이트 확인·원격 규칙 다운로드가 없다. 웹 화면도 외부를 부르지 않는다.
- OpenCode(1.19.5)는 플러그인을 꺼도 시작할 때마다 npm 레지스트리에 `GET /@opencode-ai%2fplugin`을 보낸다(측정). CODEYE는 OpenCode 실행 환경에 `npm_config_registry=http://127.0.0.1:9/`를 넣어 이 요청을 loopback의 닫힌 포트로 보낸다. 요청은 즉시 실패하고 검토는 계속된다. 오프라인이 아닐 때도 같다. 그 밖에 OpenCode가 하는 일은 아래 측정 범위만 안다. 자동 업데이트·모델 목록 받기·공유·기본 플러그인은 원래 끈다([검토](REVIEW.md)).
- **여전히 나갈 수 있는 것.** 저장소 코드 실행이 허가된 프로젝트의 build script·proc macro·설정 프로그램(Native 실행이라 OS 수준으로 막지 않는다), ESLint 설정이 불러오는 plugin이 스스로 하는 접속, 운영자가 설정한 미러·endpoint·Git 호스트. 확실히 막으려면 OS 방화벽이나 네트워크 정책을 둔다.

### 미러

언어별 미러 주소는 `agent.dependencies`에 적는다. 인증정보가 든 URL은 설정 오류다.

| 생태계 | 설정 | 미러가 줄 것 |
|---|---|---|
| Go | `go_proxy`(예: `https://mirror.corp.example/go`), 필요하면 `go_sumdb`(사내 sumdb 또는 `off`), `go_private`·`go_noproxy`·`go_nosumdb` | GOPROXY 프로토콜(`/<module>/@v/list`, `.info`, `.mod`, `.zip`). 정적 파일로 충분하다 |
| npm·pnpm·yarn 1 | `npm_registry`(예: `https://mirror.corp.example/npm/`) | npm 레지스트리 문서와 tarball. lockfile의 `resolved`가 공개 레지스트리를 가리킬 때 관리자마다 어떻게 받는지는 시험하지 않았다(시험한 lockfile은 미러 주소였다) |
| Python | `pip_index_url`(예: `https://mirror.corp.example/pypi/simple`) | PEP 503 simple index와 wheel(소스 배포판은 쓰지 않는다) |
| Rust | 없음 | 오프라인에서 받지 않는다. 사내 레지스트리 설정은 아직 없다 |
| Git | `git_hosts` 기본 `["github.com"]` | `git_hosts`에 명시한 사내 호스트만 쓴다. 기본값은 빈 목록이다 |

Go만 미러가 없어도 쓸 수 있는 길이 있다. 연결된 머신에서 `go mod download`로 채운 모듈 캐시를 옮겨 `agent.go_mod_cache`에 적으면 그 캐시를 읽기 전용 file proxy로 쓴다.

### 사내 모델 endpoint

OpenCode 설정 파일의 선택한 공급자에 `options.baseURL`로 사내 주소를 적는다. 기존 OpenAI 호환 키 파일을 쓸 때는 `<state_dir>/secrets/model.json`(0600)에 `base_url`로 적는다. 관리자 화면의 연결 확인과 `codeye model probe`는 지정한 endpoint에 실제 요청을 보낸다.

```json
{"schema_version": "0.1", "provider": "openai", "api_key": "...", "base_url": "https://llm.corp.example/v1"}
```

relay(`agent.model.relay_file`)를 쓰면 relay의 upstream이 사내 endpoint여야 한다. 오프라인 모드는 relay가 어디로 보내는지 검사하지 않는다.

### 사내 CA

`"ca_bundle": "/절대/경로.pem"`은 사내 CA를 신뢰하게 한다. 시스템 CA와 사내 CA를 합친 PEM 파일을 준다(RHEL은 `/etc/pki/tls/certs/ca-bundle.crt`가 `update-ca-trust` 뒤의 합본이다). 에이전트는 `--ca-bundle`로 받고 시작할 때 파일을 읽어 인증서가 하나도 없거나 8 MiB를 넘으면 시작하지 않는다. 인증서 검증을 끄는 설정은 없고 두지 않는다.

CODEYE는 자식 프로세스 환경에 다섯 변수를 같은 파일로 넣는다. 의존성 받기(go, npm·pnpm·yarn, pip·uv, cargo), 소스 확보의 git, 모델 검토의 OpenCode가 받는다. CODEYE 프로세스 자신은 미러·모델에 직접 TLS로 접속하지 않는다.

| 변수 | 읽는 도구 | 확인 |
|---|---|---|
| `SSL_CERT_FILE` | Go(`go mod download`), OpenSSL을 쓰는 도구, uv | 시스템 CA를 **대신**한다. 그래서 합본이어야 한다 |
| `NODE_EXTRA_CA_CERTS` | npm, pnpm, yarn, OpenCode(Bun) | 기본 CA에 **더한다** |
| `GIT_SSL_CAINFO` | git | |
| `PIP_CERT` | pip | |
| `CARGO_HTTP_CAINFO` | cargo | 오프라인에서는 cargo가 받지 않으므로 시험하지 않았다 |

`scripts/offline_smoke.py`가 각 도구를 사설 CA의 HTTPS 미러에 대고 변수 있이·없이 실행해, 있으면 성공하고 없으면 인증서 검증 오류로 실패함을 확인한다(결과는 [시험](TESTING.md)의 TC-243).

### 오프라인 준비 점검

`codeye doctor --config PATH`는 `offline` 절을 더한다(텍스트 출력은 "Offline readiness"). 오프라인 모드가 꺼져 있어도 보인다. 실패해도 종료 코드를 바꾸지 않는다.

- `enabled`, `ca_bundle`(`status`가 `not_configured`, `available`(`certificates`에 인증서 수), `unavailable`(`ca_bundle_unreadable`: 읽을 수 없거나 인증서가 없음)).
- `destinations[]`: 이 설정으로 CODEYE가 접속할 곳마다 `kind`(`go_proxy`·`go_sumdb`·`npm_registry`·`pip_index`·`crates_registry`·`git_host`·`github_api`·`model_endpoint`), `value`, `origin`(`config`·`user_go_env`·`user_npm_config`·`model_key_file`·`relay_file`·`builtin_default`·`none`), 접속하지 않으면 `reason`. 오프라인인데 `builtin_default`가 남으면 그것이 공개 서비스다.
- `postgres`: 관리형 DB에 쓸 PostgreSQL 프로그램(`initdb`, `pg_ctl`, `postgres`, `pg_dump`, `pg_restore`, `psql`)을 찾았는지와 버전.
- `languages[]`: Go·Python·JS/TS·Rust·C/C++·Java·공통·모델마다 없는 도구와 의존성 준비 상태.

schema는 [doctor schema](../schemas/doctor.schema.json)의 `offline`, 예시는 [examples/doctor.json](../examples/doctor.json)이다.

### 분석 도구 옮기기

분석 도구는 묶음에 없다. 연결된 머신에서 같은 OS/arch로 받은 것을 옮기거나 사내 미러에서 설치한다. 놓을 곳은 [도구 찾기](ANALYZERS.md#도구-찾기)의 도구 prefix `~/.local/share/codeye-tools`(`bin`, `go/bin`, `node/node_modules`, `cpp/bin`)와 PATH이고, 다른 곳이면 `agent.tools`에 절대 경로를 적는다.

`codeye tools --checklist [--config PATH]`는 Markdown 점검표를 낸다. 도구마다 찾았는지, 버전과 CODEYE가 시험한 계열(`tested 2026.2` 등), 없으면 폐쇄망에서 옮기는 방법(예: `uv tool install mypy --index-url <internal index>`, `npm install --prefix ~/.local/share/codeye-tools/node --registry <internal registry> typescript`, Rust standalone installer)을 적는다. 도구를 옮긴 뒤 다시 실행해 빈 칸을 지운다. `--json`과 함께 쓸 수 없다. `gh`는 선택이며 온라인에서만 쓴다(오프라인 모드는 GitHub을 쓰지 않으므로 옮기지 않아도 된다).

- Python 도구의 venv를 다른 경로로 복사하면 옛 interpreter 경로를 가리켜 거부된다. 사내 index로 그 머신에서 `uv tool install`한다.
- 시험한 계열과 다른 버전도 쓰지만 보고서와 점검표가 "시험하지 않은 버전"으로 표시한다.

### 확인한 것과 확인하지 않은 것

`scripts/offline_smoke.py --pack 묶음 --postgres-bin-dir DIR`(Linux, root 불필요, 수동 실행·CI 밖)이 묶음을 빈 디렉터리에 풀고 그 실행 파일만으로, 새 HOME·설정·상태 디렉터리로 실행한다. 전체를 새 user·network namespace(`unshare -rn`, loopback만 있음)에서 돌려 외부 접속이 즉시 실패하게 하고, `strace -f -e trace=connect`로 모든 connect를, resolv.conf의 nameserver 주소를 namespace의 loopback에 붙인 DNS 기록기로 모든 이름 조회를 기록한다. 결과는 [시험](TESTING.md)의 TC-240~245와 [변경 이력](../CHANGELOG.md)에 있다.

실제 폐쇄망, RHEL 8.10 호스트 커널, 회사 CA·미러, cargo의 CA, macOS·Windows 묶음은 별도 검증이 필요하다.

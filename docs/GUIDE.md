# CODEYE 사용 안내서

Linux에서 CODEYE를 시작하고 첫 프로젝트를 등록하는 방법이다. 설정, 백업과 서비스 운영은 [운영](OPERATIONS.md)에 있다.

## 준비물

| 하려는 일 | 준비물 |
|---|---|
| 웹 서비스 시작 | Linux x86-64 또는 ARM64(WSL2 포함), 일반 Linux 계정, CODEYE 배포 파일, PostgreSQL 서버 프로그램 14 이상. [지원 범위](OPERATIONS.md#지원-범위) |
| 폴더 분석 | 서비스 Linux 계정이 읽을 수 있는 프로젝트 폴더의 절대 경로. WSL 파일과 서버에 마운트된 NFS도 같은 방식으로 입력한다 |
| Git 저장소 분석 | `git`과 HTTPS 주소. 비공개 GitHub.com·GHES는 [플랫폼 GitHub 계정](GITHUB.md#플랫폼-github-계정)에 읽기 권한을 준다 |
| 정적 분석과 모델 검토 | 프로젝트 언어의 [분석 도구](ANALYZERS.md), 모델 검토를 쓸 경우 [서버의 OpenCode와 사용할 모델](#모델-설정) |

Ubuntu에서는 `sudo apt install postgresql`로 PostgreSQL을 설치할 수 있다. CODEYE가 `initdb`, `pg_ctl`, `postgres`, `pg_dump`, `pg_restore`를 찾아 전용 DB를 준비한다. 별도 경로는 `./codeye up --postgres-bin-dir /절대/경로/bin`으로 지정한다. 기존 DB 서버를 쓰려면 [외부 PostgreSQL](OPERATIONS.md#외부-postgresql)을 따른다.

## 첫 시작

CPU에 맞는 배포 파일과 SHA-256 검증 파일을 같은 폴더에 받는다. 압축을 이미 풀었다면 그 안의 `INSTALL.md`를 연다.

| Linux CPU | CODEYE v0.1.3 | 검증 파일 |
|---|---|---|
| x86-64 (`amd64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-amd64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-amd64.tar.gz.sha256) |
| ARM64 (`arm64`) | [다운로드](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-arm64.tar.gz) | [SHA-256](https://github.com/jihoon22-lee/CODEYE/releases/download/v0.1.3/codeye-v0.1.3-linux-arm64.tar.gz.sha256) |

**이전 배포판을 사용 중이라면** 기존 설정과 상태 디렉터리에서 계속 실행한다. 백업, 배포 파일 교체와 DB 처리 순서는 [배포판 업그레이드](OPERATIONS.md#배포판-업그레이드)를 따른다.

처음 설치하는 x86-64 머신에서는 내려받은 폴더에서 다음과 같이 실행한다. ARM64는 명령의 `amd64`를 `arm64`로 바꾼다.

```sh
sha256sum -c codeye-v0.1.3-linux-amd64.tar.gz.sha256 &&
  tar --keep-old-files -xzf codeye-v0.1.3-linux-amd64.tar.gz &&
  cd codeye-v0.1.3-linux-amd64 &&
  sha256sum -c SHA256SUMS &&
  ./codeye up
```

1. `up`이 데이터베이스, 웹 서버와 로컬 에이전트를 시작한다. 기본 주소는 `http://127.0.0.1:7331`이다. 사용 중이면 7340까지 다음 빈 포트를 고른다. [실행 명령과 설정 경로](OPERATIONS.md#up-open-start-stop-status)
2. 열린 브라우저에서 관리자 계정으로 로그인한다. 브라우저가 열리지 않으면 터미널에 출력된 링크를 연다. 링크는 10분 동안 한 번 쓸 수 있고 로그인은 7일 동안 유지된다. 새 링크는 `./codeye open`, 링크만 출력하려면 `./codeye open --no-open`을 쓴다.
3. **폴더 경로 또는 Git 주소**에 절대 경로, HTTPS 저장소·브랜치·PR 주소를 넣고 **분석**을 누른다. **고급**에서 이름과 분석 조건을 바꿀 수 있다. 분석 전에 등록만 하려면 **프로젝트 등록만 하기**를 연다. [프로젝트별 분석 선택](#프로젝트별-분석-선택) · [Git 주소 예시](GITHUB.md#저장소-등록)
4. 실행 화면에서 진행 단계를 확인한다. 분석이 끝나면 그 화면에 보고서가 열린다. 모델을 연결하려면 **관리 > OpenCode 연결**을 연다. [보고서 읽기](#보고서-읽기) · [OpenCode 연결](#관리자-화면에서-연결하기)

터미널에서 바로 분석을 요청할 수도 있다. 이 안내서의 `codeye`는 PATH에 둔 실행 파일을 뜻한다. 배포 폴더에서 직접 실행하면 `./codeye`를 쓴다.

```sh
./codeye up ~/src/my-project
./codeye up https://github.com/OWNER/REPO
```

### 첫 관리자 로그인

`./codeye up`을 처음 실행하면 서비스가 링크로 로그인하는 관리자 계정을 만든다. 브라우저가 없는 서버에서는 `./codeye up --no-open`을 실행하고 터미널에 표시된 링크를 연다. 링크를 다시 발급하려면 서비스 머신에서 같은 Linux 계정으로 `./codeye open --no-open`을 실행한다.

이메일·비밀번호로 관리하고 싶다면 별도 브라우저나 시크릿 창에서 본인 이메일로 가입한다. 처음 발급한 링크로 로그인한 창에서 **관리 > 사용자**를 열고 해당 이메일 행의 **관리자로 지정**을 누른다. 이후에는 그 이메일과 비밀번호로 관리 화면에 들어갈 수 있다.

## 다른 사람 초대

### 다른 머신에서 열 수 있게 하기

기본 주소는 이 머신에서만 열린다. 다른 머신에서 열려면 서비스가 받을 주소와 브라우저에 알려 줄 주소를 정한다. 아래 설정은 저장되므로 다음부터는 `codeye up`으로 시작한다.

```sh
codeye up --listen 0.0.0.0:7443 --tls-cert /etc/codeye/cert.pem --tls-key /etc/codeye/key.pem --public-origin https://codeye.example:7443
```

신뢰하는 LAN·VPN에서는 `codeye up --listen 0.0.0.0:7331 --plain-http`로 평문 HTTP를 선택할 수 있다. 이 경우 로그인 비밀번호와 쿠키도 암호화 없이 전송된다. 명령이 출력한 주소 중 상대의 네트워크에서 닿는 것을 공유한다. [TLS 수신](OPERATIONS.md#tls-수신) · [받는 주소](OPERATIONS.md#받는-주소)

**WSL에서 내 기기만 연결.** WSL 안에서 Tailscale을 실행하고 tailnet 접근 정책으로 접속 기기를 제한했다면 CODEYE는 loopback에 두고 Tailscale Serve로 TCP 포트를 전달할 수 있다. [Tailscale Serve 포트 전달](https://tailscale.com/docs/reference/tailscale-cli/serve)

```sh
tailnet_ip=$(tailscale ip -4) && test -n "$tailnet_ip" &&
  tailscale serve --bg --tcp=7331 tcp://127.0.0.1:7331 &&
  codeye up --listen 127.0.0.1:7331 --plain-http --public-origin "http://${tailnet_ip}:7331"
```

기본 수신으로 돌아가려면 설정 파일에서 `listen`·`tls_cert`·`tls_key`·`public_origins`·`plain_http`를 지우고 `codeye up`으로 반영한다.

### 사용자 더하기

**관리 > 서비스**의 주소를 공유한다. 다른 사람은 **회원가입**에서 이메일과 비밀번호를 입력해 바로 로그인한다. 이메일의 대소문자와 앞뒤 공백은 무시하며 확인 메일은 보내지 않는다. 기존 사용자 이름으로 로그인하거나, 관리자가 만든 일회용 링크를 쓸 수도 있다. [계정과 관리자 CLI](OPERATIONS.md#계정과-관리자-cli)

같은 소스를 각자 등록하면 각자의 프로젝트와 보고서가 생긴다. 공유하려면 프로젝트의 **구성원**에 상대의 이메일이나 기존 사용자 이름을 입력하고 보기·실행·관리 권한을 고른다. 서비스 관리자도 다른 사람의 프로젝트를 열려면 그 프로젝트의 구성원 권한이 필요하다. [프로젝트 구성원](API.md#구성원)

### 다른 사람이 분석할 수 있는 것

CODEYE 웹 계정은 프로젝트를 열거나 실행할 권한을 정한다. 소스 읽기는 서버의 서비스 계정과 플랫폼 GitHub 계정의 권한을 따른다. 적용되지 않는 기능과 이유는 첫 화면의 **이 서비스에서 지금 쓸 수 없는 것**에 표시된다.

| 기능 | 권한과 설정 |
|---|---|
| Git 저장소 분석 | 승인된 호스트의 공개 저장소를 읽는다. 비공개 저장소와 그 PR에는 플랫폼 GitHub 계정의 읽기 권한이 필요하고 결과 댓글에는 쓰기 권한이 필요하다. [플랫폼 GitHub 계정](GITHUB.md#플랫폼-github-계정) |
| 서버 폴더 분석 | 서비스 Linux 계정이 읽을 수 있는 절대 경로를 입력한다. 상태·백업 디렉터리를 포함하거나 그 안에 있는 경로는 거부한다 |
| 모델 검토 | 관리자는 모델 설정이 있으면 사용할 수 있다. 다른 사용자는 **관리 > 서비스 > 다른 사용자도 모델 검토 사용**을 따른다(기본 켬) |
| 저장소 코드 실행 | 관리자가 아닌 사용자가 시작한 분석에는 **관리 > 서비스 > 다른 사용자의 저장소 빌드 스크립트 실행 허용**도 필요하다(기본 끔) |

## 설정 파일

기본 경로는 `~/.config/codeye/config.json`이며 `XDG_CONFIG_HOME`을 따른다. `codeye up --config PATH`로 특정 설치의 설정을 지정할 수 있다. [서비스 설정 파일](OPERATIONS.md#서비스-설정-파일)

## 프로젝트와 source root

`source root`는 실행 노드에서 서버 폴더를 찾는 매핑이다. 웹이나 CLI에 절대 경로를 입력하면 CODEYE가 기존 매핑을 찾아 쓰거나 새 매핑을 등록한다. 운영자는 설정의 `source_roots`에 따로 추가할 수 있다. `codeye up`을 다시 실행하면 로컬 에이전트가 설정 변경을 반영한다. [서비스 설정 파일](OPERATIONS.md#서비스-설정-파일)

같은 폴더나 저장소를 다시 입력하면 내 기존 프로젝트에서 새 분석을 시작한다. 일반 분석이 진행 중이면 그 실행 화면을 연다. PR은 요청 시점의 head를 고정한다. 프로젝트 카드에서 이전 실행과 보고서를 볼 수 있다. 분석을 요청하면 문서·환경·소스를 자동으로 찾아 읽는다.

### 프로젝트별 분석 선택

처음 등록할 때 **고급**에서 설정한다. 보고서의 **더 나은 결과를 얻으려면**에도 결과에 따라 프로젝트 관리자가 쓸 수 있는 설정 버튼이 나온다.

| 선택 | 동작 | 로컬 기본 | Git 기본 |
|---|---|---|---|
| 소스를 모델에 보냄 | 연결한 모델이 검토할 소스와 문서를 읽는다. 끄면 모델 검토를 생략한다 | 켬 | 켬 |
| 저장소의 빌드 스크립트 실행 | 빌드 스크립트·Cargo·플러그인 등 저장소 코드가 실행되는 분석과 **테스트 포함 분석**을 허용한다. 작업 사본에서 서비스 Linux 계정 권한으로 실행한다 | 켬 | 끔 |
| 의존성 받기 | 분석기가 읽을 Go 모듈, Rust crate, JS/TS 패키지와 Python wheel을 준비한다. Rust는 빌드 스크립트 실행도 켜야 한다. 레지스트리 접근과 필요한 패키지 관리자는 실행 환경에 따른다. [의존성 준비](ARCHITECTURE.md#의존성-준비) | 켬 | 켬 |

관리자 스위치나 별도로 실행한 노드의 허가가 이 선택을 제한할 수 있다. 빠진 작업, 필요한 도구나 설정은 보고서에 사유가 기록된다. [분석기와 실행 조건](ANALYZERS.md)

## 언어별 분석과 테스트

저장소의 언어와 모듈은 자동으로 찾는다. **테스트 포함 분석**을 선택하면 프로젝트에 이미 있는 테스트를 실행한다. 검사나 테스트가 빠지면 보고서에서 해당 행의 사유를 확인할 수 있다.

| 대상 | 정적 분석에서 확인할 수 있는 것 | 기존 테스트 |
|---|---|---|
| Java | PMD 소스 규칙, SpotBugs 바이트코드 진단. Maven·Gradle 모듈 및 JDK 요구 버전 | Maven·Gradle 작업에서 JUnit·TestNG 결과 |
| Go | go vet, Staticcheck, errcheck의 무시된 오류, gofmt, 패키지 의존 관계 | go test, 선택한 하위 테스트·race 검사 |
| Python | Ruff, mypy·Pyright, Bandit, 선언된 Import Linter 계약 | unittest·pytest |
| JavaScript·TypeScript | ESLint, tsc, dependency-cruiser 기반 패키지 순환·의존 규칙 | npm 스크립트, Jest·Vitest·node:test·Playwright 결과 |
| C/C++ | Cppcheck, clang-tidy. C 소스에는 GCC 분석기의 파일 디스크립터 검사도 적용 가능. 저장소에 `compile_commands.json`이 없으면 build 스크립트 실행을 허용한 프로젝트에서 CMake configure로 만들어 쓴다(서비스 머신에 cmake·make 필요) | CMake·CTest. GoogleTest·Catch2 등은 CTest 등록 방식에 따라 결과를 읽는다. |
| Rust | Cargo·Clippy와 rustdoc의 API 문서 참조 검사 | Cargo 단위·통합·문서 테스트, 선택한 cargo-nextest |

여섯 언어 모두에서 Opengrep을 설치하면 CODEYE 규칙 팩으로 한 파일 안에서 요청 입력이 명령 실행·파일 경로·SQL·외부 요청·역직렬화·코드 평가·HTML 출력에 닿는 흐름을 찾는다. 파일 사이 흐름은 찾지 않는다. 설치는 릴리스 실행 파일 하나를 `~/.local/share/codeye-tools/bin/opengrep`에 두는 것이다. [Opengrep 패턴·taint 팩](ANALYZERS.md#opengrep-패턴taint-팩)

분석 노드에 설치한 도구, 프로젝트 설정, 읽을 수 있는 의존성이 실제 범위를 결정한다. 런타임에서만 결정되는 호출이나 다른 빌드 구성의 동작은 코드에서 확인한 경로와 구분한다. [도구와 규칙](ANALYZERS.md) · [테스트 실행 조건](EXECUTION.md#기존-테스트-실행)

Java를 처음 분석할 때는 웹의 **도구** 화면이나 `./codeye tools`에서 JDK·PMD·SpotBugs의 탐색 결과를 확인한다. 외부 JAR가 필요한 프로젝트는 노드의 로컬 클래스패스 목록을 설정한다. 기존 Java 테스트까지 실행하려면 해당 빌드에 필요한 Maven 또는 Gradle과 의존성 캐시를 준비한다. [Java 클래스패스](ANALYZERS.md#java-로컬-클래스패스) · [테스트 실행 조건](EXECUTION.md#기존-테스트-실행)

## 사용할 수 있는 도구 확인

```bash
codeye tools
```

이 머신에서 찾은 도구(경로와 버전), 찾지 못한 도구와 추가하는 방법을 표로 보여 준다. 화면의 **도구** 메뉴도 `codeye up`이 마지막으로 찾은 같은 표와 모델과 호스트별 플랫폼 GitHub 연결 상태를 보여 준다(도구 경로는 관리자에게만 보인다). `codeye up`은 시작할 때 같은 탐색을 하므로 도구 경로를 설정에 적지 않아도 된다. 설정의 `agent.tools.<이름>`에 적은 경로는 항상 탐색보다 우선한다. 없는 도구에 해당하는 분석은 보고서에 "미지원"으로 나온다([도구 찾기](ANALYZERS.md#도구-찾기), [운영](OPERATIONS.md#codeye-tools)).

### 언어 도구 경로

`codeye up`으로 시작한 서비스에서는 관리자로 로그인해 **관리 > 언어 도구 경로**를 연다. 웹에 접속한 PC가 아닌 **서비스 머신**에 설치된 실행 파일이나 설치 디렉터리를 입력하고 **경로 저장**을 누른다. 저장 시 실행 파일을 확인하며 다음 로컬 분석부터 적용한다. 실행 중인 분석은 시작할 때 선택한 도구로 마친다.

| 도구 | 웹 입력 예시 | 설정 파일 키 |
|---|---|---|
| Python | `/opt/venvs/qa/bin/python` 또는 가상환경 디렉터리 | `agent.tools.python` |
| JDK | `/opt/jdk-21` 또는 `bin/java`의 절대 경로. 같은 JDK의 `javac` 필요 | `agent.tools.java` |
| Go | `/opt/go` 또는 `/opt/go/bin/go` | `agent.go` |
| Rust | `rustup which cargo`가 출력한 실제 도구체인의 Cargo 경로 | `agent.tools.cargo` |
| Node.js | `/opt/node/bin/node` | `agent.tools.node` |

입력란을 비우고 저장하면 그 아래 표시한 자동 탐색 경로로 돌아간다. `agent.auto_detect`가 `false`이면 수동 지정만 해제된다. 별도 머신에서 띄운 노드는 그 머신의 에이전트 설정을 따른다.

설정 파일로 관리하려면 실행 파일의 절대 경로를 적는다. v0.1.2에서도 이 방법을 쓸 수 있다. 기본 파일은 `~/.config/codeye/config.json`이다. 기존 설정의 해당 항목을 바꾸고 서비스 머신에서 `./codeye up --no-open --no-probe`를 다시 실행한다.

```json
{
  "schema_version": "0.1",
  "agent": {
    "go": "/opt/go/bin/go",
    "tools": {
      "python": "/opt/venvs/qa/bin/python",
      "java": "/opt/jdk-21/bin/java",
      "cargo": "/opt/rust-toolchain/bin/cargo"
    }
  }
}
```

## 분석 실행

첫 화면에서 소스를 입력하거나 프로젝트의 **새로 분석**을 누른다. 실행 화면은 대기 사유와 단계별 상태를 표시한다. **실행 세부 정보**에서 실행 ID, 스냅샷과 진행률 계산 근거를 확인할 수 있다.

- **범위 선택.** 등록된 프로젝트에서 **분석 설정 > 분석할 상대 경로**에 파일이나 폴더를 한 줄씩 넣고 선택한 경로의 분석 버튼을 누른다. 비우면 전체를 분석한다. 다음 Git 커밋 또는 로컬 사본에 범위를 적용하며 이미 게시된 보고서는 유지한다. 한 동작을 보려면 사용 문서와 구현 경로, 그 코드를 호출하는 곳, 관련 테스트를 함께 선택한다.
- **테스트 포함 분석.** 첫 화면의 **고급 > 기존 테스트 포함**이나 프로젝트의 **테스트 포함 분석**을 선택한다. 저장소 코드 실행이 허용된 분석에서 Linux 노드는 작업 사본과 전용 loopback 네트워크를 사용한다. 언어별 실행 대상과 필요한 도구는 [언어별 분석과 테스트](#언어별-분석과-테스트)에서 확인한다. [기존 테스트 실행](EXECUTION.md#기존-테스트-실행)
- **확장 코드 검토.** 더 넓은 모델 조사 범위가 필요하면 **분석 설정 > 확장 코드 검토**를 선택한다. 노드 설정의 `agent.model.review_max_calls`가 207회를 넘어야 일반 검토보다 많은 원문을 조사할 수 있다. 실행 시간과 비용이 늘 수 있다. [모델 검토 설정](#모델-검토-설정)
- **다시 시도.** 실패·일부 완료·취소된 실행에서 요청할 수 있다. 실패·취소 때 고정한 입력이 노드에 남아 있으면 그 입력을 다시 사용한다. 소스 확보에 실패했거나 도구·분석 설정을 바꿨다면 새로 분석한다. 취소나 제한시간 뒤 정리를 확인하지 못한 실행은 **정리 확인 필요**(`recovery_required`)로 남으며 확인 뒤 다시 실행할 수 있다. [장애 대응](OPERATIONS.md#장애-대응-runbook)

## 보고서 읽기

1. **종합 > 주요 분석 결과**에서 영역별 상태와 조치할 항목을 확인한다. **분석 공백**에서는 미지원·실패·정책 제외·조사 한도로 빠진 범위와 원인을 읽는다. 일부만 완료된 실행에도 확보한 근거가 남는다. 도구를 추가했다면 `codeye up`으로 탐색을 갱신한 뒤 새로 분석한다.
2. **문서 분석**에서 문서에 적힌 동작과 구현을 대조하고 인용된 원문을 펼친다. **코드 분석**에서 판정을 고르면 근거 파일·줄, 발생 조건과 관련 코드·테스트를 볼 수 있다. **정적 분석**의 진단은 도구가 관찰한 결과다. 코드 분석에서 후보의 검증 상태를 확인하고 필요하면 사람 판정과 사유를 남긴다. **모듈별 검토 장부**에서 모델이 조사한 파일·줄과 한도 또는 비밀값 선별로 제외된 범위를 확인한다.
3. **종합 > 주요 분석 결과 > 기존 테스트**를 열어 통과·실패·건너뜀과 실패 위치를 확인한다. 프로젝트의 준비 실패, 테스트 없음, 테스트 단언 실패를 구분해 읽는다. 문서 대조에서 인용한 테스트 줄에 해당하는 실행 결과가 있으면 근거와 함께 표시된다.

**보고서 검색**으로 파일명, 문서 용어, 기호와 규칙 ID를 찾는다. 기록이 길면 **전체 목록**을 펼친다. 이전에 게시된 보고서의 배치는 작성 시점에 따라 다를 수 있다. [보고서의 영역과 판정 규칙](REPORT.md#분석-종류별-절)

JSON·Markdown·HTML로 내보낼 수 있다. 기본 내보내기에는 테스트 도구와 성공·실패·건너뜀 개수가 남는다. 테스트 이름과 확인된 선언 위치를 함께 보려면 **코드 포함**을 선택한다. Markdown·HTML은 기본 요약이며 모든 행은 **Markdown 전체**·**HTML 전체**에서 확인한다. JSON에는 전체 행이 담긴다. [내보내기 상세도](REPORT.md#내보내기-상세도)

## 예약 실행

프로젝트 관리자가 **예약 실행**에서 cron 식과 `Asia/Seoul` 같은 IANA 시간대를 입력한다. 평일 정오는 `0 12 * * 1-5`이며 `@daily`·`@weekly`도 쓸 수 있다. 서비스가 실행 중일 때 예약을 평가한다.

- 앞선 실행이 끝나지 않았으면 기본으로 최신 1건을 대기시킨다. 건너뛰거나 최대 10건을 대기시킬 수도 있다.
- 정지 중에 지난 예약은 기본으로 최신 1건만 실행한다. **2분보다 늦으면 건너뛰기**도 선택할 수 있다.
- 예약을 만든 관리자의 권한을 실행마다 확인하고 분석 시작 시 입력을 고정한다. 예약을 삭제해도 이미 대기열에 넣은 실행은 유지된다. [cron, 시간대와 일광 절약 시간 규칙](EXECUTION.md#예약)

## 이전 실행과 비교

보고서 화면의 **이전 실행과 비교**는 소스·프로필·범위가 같은 가장 최근 완료·부분 완료 보고서를 기준으로 한다.

| 상태 | 뜻 |
|---|---|
| `new` | 이번 실행에서 처음 나타남 |
| `persisting` | 두 실행에서 같은 개수·심각도·검증 상태·영역으로 관찰됨 |
| `changed` | 두 실행에 있으며 개수·심각도·검증 상태·영역 중 하나 이상이 달라짐 |
| `resolved_observed` | 같은 버전·규칙의 도구가 같은 범위에서 해당 파일을 실제로 재검사했고 이전 진단이 사라진 것으로 관찰됨 |
| `absent_unverified` | 이번 실행에 나타나지 않았지만 검사 실패·범위 변경 등으로 해결 여부를 확인할 수 없음 |

도구나 규칙이 달라지거나 억제 주석이 생긴 경우, 모델이 같은 후보를 다시 내지 않은 경우는 `absent_unverified`가 될 수 있다. 같은 식별자가 여러 개면 `ambiguous`로 표시하고 경로만 바뀐 항목은 이동 후보로 보여 준다. 위험 수용·오탐·억제는 조치 집계에서 빠져도 전체 발견과 비교에 남는다. 판정 사유는 감사 이력에 남고 과거 보고서에서는 게시 당시의 판정을 볼 수 있다. [비교와 판정](REPORT.md#run-비교)

## 모델 설정

모델 검토에는 서버에 설치된 OpenCode와 사용할 모델이 필요하다. 설정 전에도 정적 분석은 실행되며 보고서에 모델 검토를 수행하지 못한 이유가 나온다. 연결한 모델은 **소스를 모델에 보냄**이 켜진 프로젝트에 적용되고, 비밀값으로 선별된 파일은 제외된다.

### 관리자 화면에서 연결하기

1. **관리 > OpenCode 연결**을 연다. **도구** 화면에서도 같은 설정으로 이동할 수 있다.
2. 서비스 머신의 OpenCode 실행 파일 경로를 입력한다. 이미 쓰는 `opencode.json` 또는 `opencode.jsonc`가 있다면 지정한다. `~/`는 서비스 Linux 계정의 홈이다. 비운 항목에는 설치의 기본 설정을 적용한다.
3. 필요하면 `공급자/모델` 이름과 인증 파일 경로를 지정한다. OpenCode `auth.json`의 API 키나 CODEYE 모델 키 파일을 쓸 수 있다. OpenCode에서 OAuth로 로그인한 경우 공급자가 지원하는 API 인증정보를 따로 준비한다. 인증을 요구하지 않는 사내 모델도 연결할 수 있다.
4. **설정 저장**을 누른 뒤 **저장한 설정으로 연결 확인**을 누른다. 모델에 합성 요청 한 번을 보내며 공급자에 따라 비용이 들 수 있다. 인증이나 연결이 실패하면 화면에 나온 원인을 고친다.

`codeye up` 또는 `codeye start`로 운영하는 설치는 다음 분석부터 저장한 설정을 적용한다. 별도 노드는 운영자가 각 노드의 모델을 설정한다. 사내 서버를 연결하는 방법은 [사내 모델 endpoint](OPERATIONS.md#사내-모델-endpoint)에 있다.

### 터미널에서 연결하기

`codeye up`은 `~/.opencode/bin/opencode`와 PATH에서 OpenCode를 찾는다. 온라인 설치에서 `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` 또는 `OPENCODE_API_KEY`를 환경에 설정하고 `codeye up`을 실행하면 `<state_dir>/secrets/model.json`에 키를 저장한다(권한 `0600`). 이전에 확인한 모델 설정과 달라지면 연결을 한 번 확인한다. `codeye model probe`로 다시 확인할 수 있다. [인증 파일과 OpenAI 호환 서버](REVIEW.md#모델-설정)

## 모델 검토 설정

실행 파일·설정·인증 파일·모델과 호출당 제한시간은 `agent.model`에서 지정할 수 있다. `relay_file`은 운영자가 승인한 relay를 쓸 때의 설정이다. `review_max_calls`는 분석 시도당 모델 호출 상한이다(15~2,013회, 기본 99회). 일반 검토에는 최대 207회가 적용된다. 더 높은 상한을 사용하려면 실행할 때 **확장 코드 검토**를 선택한다. 모델을 호출하지 않고 설정을 점검하려면 `codeye doctor`를 사용한다. [서비스 설정](OPERATIONS.md#서비스-설정-파일) · [모듈 단위 검토와 호출 한도](REVIEW.md#모듈-단위-검토-분할)

모델은 한 요청에서 모듈의 파일 전체를 함께 읽고, 근거를 줄 범위로 인용하며, 후보마다 실패 과정을 짧은 문단으로 설명한다. 요청 하나의 크기는 `review_request_bytes`(40,000~1,048,576바이트, 기본 262,144), 모델이 쓸 수 있는 출력은 `review_output_tokens`(1,536~65,536, 기본 16,384)로 정한다. 호출당 제한시간 `timeout_seconds`의 기본은 600초다.

```json
{"agent": {"model": {"review_request_bytes": 131072, "review_output_tokens": 8192, "review_max_calls": 60}}}
```

비용은 호출 수와 각 호출의 입력·출력 크기에 따라 늘어난다. 기본값은 이전보다 한 요청이 훨씬 크므로 같은 호출 수에서도 비용이 더 들 수 있다. 줄이는 방법은 다음과 같다.

- `review_request_bytes`를 낮춘다. 한 번에 읽는 코드가 줄고 남은 줄은 모듈 단위 후속 검토로 넘어간다. 40,000이면 이전과 같은 크기의 요청이다.
- `review_output_tokens`를 낮춘다. 서술 길이와 후보 수가 준다. 1,536이면 이전과 같은 120자 서술과 후보 4개다.
- `review_max_calls`를 낮춘다. 후속 검토 수가 줄고 보내지 못한 줄은 보고서의 모듈별 장부에 남는다.

실제 비용과 응답 시간은 모델·공급자·저장소에 따라 다르고 CODEYE가 측정한 값이 없다. 운영자가 정한 예산 안에서 작은 저장소로 먼저 확인한다. [요청 크기](REVIEW.md#요청-크기)

## 저장 공간

상태 디렉터리에 DB, 소스·작업 사본, 로그, 산출물이 쌓인다. `codeye status`에서 사용량을 보고 필요하면 설정의 `quota`로 새 작업에 적용할 상한을 둔다. 오래된 도구 출력과 작업 사본을 지우기 전에는 `codeye cleanup --older-than 30d`로 대상을 확인한다. [저장 영역과 상한](OPERATIONS.md#저장-영역과-상한) · [보존 정리](OPERATIONS.md#보존-정리)

### 로그아웃 뒤에도 실행

Linux의 systemd 사용자 서비스에서 실행한다. `codeye service install`로 등록하고 `systemctl --user start codeye`로 시작한다. 로그인 링크는 `codeye open`으로 받는다. 로그아웃 뒤 유지 조건과 해제 방법은 [사용자 서비스](OPERATIONS.md#사용자-서비스)에 있다.

## 정지

`codeye stop`은 새 분석을 받지 않고 진행 중인 작업의 종료를 기다린다. 시간 안에 끝나지 않으면 취소와 정리 확인을 거친다. 마지막 줄의 `outcome`이 `recovery_required`이면 `codeye status`와 로그를 확인한다. 다음 `codeye up`은 같은 설치의 데이터를 이어서 쓴다. [정지 결과](OPERATIONS.md#stop)

## 백업과 복원

`codeye backup`으로 DB와 확정 산출물을 한 세트로 보관한다. 복원에는 빈 상태 디렉터리와 데이터베이스가 필요하다. 새 설치에서 `codeye restore --config PATH --from DIR`로 복원하고 `codeye up --config PATH`로 시작해 새 로그인 링크를 받는다. [백업과 복원](OPERATIONS.md#백업과-복원)

## 인터넷 없는 머신

연결된 머신에서 배포 파일과 검증 파일을 받아 함께 옮긴다. 묶음 안 `INSTALL.md`의 검증 절차를 마친 뒤 `./codeye up --offline /절대/프로젝트/경로`를 실행한다. 한 번 지정한 오프라인 설정은 저장된다. 필요한 PostgreSQL, 분석 도구, 모델 실행기는 대상과 같은 OS/arch용으로 따로 준비한다. [폐쇄망 배포팩](OPERATIONS.md#폐쇄망-배포팩)

<a id="업그레이드-설계-절차-시험-안-함"></a>

## 업그레이드

이전 버전에서 쓰던 계정, 프로젝트와 보고서를 이어서 쓰려면 기존 설치를 백업한 뒤 같은 설정과 상태 디렉터리로 v0.1.3을 시작한다. 관리형·외부 PostgreSQL별 이전 절차와 서비스 재시작 방법은 [배포판 업그레이드](OPERATIONS.md#배포판-업그레이드)에 있다.

## 문제 해결

`codeye status --format json`으로 프로세스와 DB 상태를 확인하고 `codeye doctor --config PATH`로 설치를 점검한다. 자세한 로그는 `state_dir/logs/`의 `controller.log`, `agent.log`, `postgres.log`에 있다. [점검 항목](OPERATIONS.md#codeye-doctor) · [장애 대응](OPERATIONS.md#장애-대응-runbook)

| 증상 | 확인할 것 |
|---|---|
| PostgreSQL 프로그램을 찾지 못함 | 설정한 `database.postgres_bin_dir`에 `initdb`, `pg_ctl`, `postgres`, `pg_dump`, `pg_restore`가 모두 있는지 확인한다. 기존 관리형 DB에는 해당 DB를 만든 PostgreSQL 주 버전이 필요하다. [관리형 PostgreSQL](OPERATIONS.md#관리형-postgresql) |
| 외부 DB 연결 또는 schema 오류 | `CODEYE_DATABASE_URL`과 연결에 영향을 주는 `PG*` 변수를 확인한다. DB가 이 실행 파일보다 새 schema라면 그 schema에 맞는 배포판을 사용한다. [외부 PostgreSQL](OPERATIONS.md#외부-postgresql) |
| 웹 화면 없음(`web UI not built`) | 배포된 실행 파일과 함께 제공된 `web/dist`가 있는지 확인한다. 다른 위치를 쓴다면 `web_dir`를 설정한다. |
| 기본 포트 또는 지정한 포트 사용 중 | 기본 포트가 사용 중이면 `up`이 선택한 주소를 터미널에서 확인한다. 직접 지정한 `listen`의 포트가 사용 중이면 설정을 바꾸거나 그 프로그램을 중지한다. [받는 주소](OPERATIONS.md#받는-주소) |
| 로그인 링크가 만료됐거나 이미 사용됨 | `codeye open`으로 새 링크를 받는다. |
| 소스 확보 실패 | 비공개 저장소는 플랫폼 GitHub 계정의 읽기 권한을, 폴더는 서비스 계정의 접근 권한을 확인한다. 상태·백업 디렉터리 안이거나 이를 포함하는 경로는 거부된다. |
| 의존성을 준비하지 못함 | 환경 분석에서 대상 모듈과 `registry_unreachable`, `dependency_budget_exhausted`, `private_module_not_fetched` 등 사유를 확인한다. [의존성 설정](ARCHITECTURE.md#의존성-준비) |
| 작업 대기 또는 정리 확인 필요 | 저장 공간 상한, 노드 상태와 실행 화면의 대기 사유를 확인한다. `recovery_required`라면 해당 노드의 워커와 자식 프로세스 정리를 확인한 뒤 **정리 확인 후 다시 실행**을 쓴다. [장애 대응](OPERATIONS.md#장애-대응-runbook) |
| 새 설치로 복원 실패 | 빈 DB와 새 상태 디렉터리를 지정하고 백업의 manifest와 검증 결과를 확인한다. [백업과 복원](OPERATIONS.md#백업과-복원) |

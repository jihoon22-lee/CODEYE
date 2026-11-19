# CODEYE 사용 안내서

한 사람이 자기 Linux(WSL2 포함) 머신에서 CODEYE를 쓰는 방법이다(개인 모드). 첫 시작, 프로젝트 추가와 분석, 보고서 읽기, 예약, 이전 실행과 비교, 정지, 저장 공간, 백업·복원, 업그레이드, 문제 해결을 다룬다. 설정 필드 전체 표와 명령의 동작 계약, 지원 범위는 [운영](OPERATIONS.md)에 있다.

아래 명령은 이 저장소의 빌드에서 Linux(WSL2)와 PostgreSQL 17.10으로 실제 실행해 확인했다. 실행하지 않은 절차는 **(설계 절차, 시험 안 함)**으로 표시했다. 경로와 이름은 예시다.

## 준비물

- **Linux.** `codeye up`·`start`·`stop`·`status`·`backup`·`restore`는 Linux 전용이다. Windows에서는 미지원 오류로 끝난다. RHEL 8에서는 실행하지 않았다([지원 범위](OPERATIONS.md#지원-범위)).
- **CODEYE 바이너리와 웹 화면.** 저장소에서 빌드한다. 외부에서 아무것도 내려받지 않는다. `codeye`는 실행 파일 옆의 `web/dist`(또는 `../web/dist`)를 화면으로 쓴다.

  ```sh
  go build -o codeye ./cmd/codeye
  cd web && npm ci && npm run build      # 결과: web/dist
  ```

- **PostgreSQL 프로그램.** 설치만 되어 있으면 된다. 서버를 띄우거나 DB·사용자를 만들 필요는 없다. CODEYE가 상태 디렉터리 안에 자기 전용 cluster를 만들고 띄운다. 주 버전 14 이상의 `initdb`·`pg_ctl`·`postgres`·`pg_dump`·`pg_restore`가 필요하다. Ubuntu는 `sudo apt install postgresql`이면 된다(`/usr/lib/postgresql/*/bin`에서 찾는다). 다른 위치에 있으면 `codeye up --postgres-bin-dir DIR`로 한 번 알려 주면 설정에 저장된다.
- **분석 도구.** Go 프로젝트에는 Go toolchain(`go vet`)이 필요하다. `codeye up`은 PATH의 `go`를 쓴다. Staticcheck·cppcheck·Ruff 같은 다른 도구는 설치한 것만 설정 `agent.tools`에 적는다. 적지 않은 도구는 보고서에 "미지원"으로 나온다. 0건 통과로 세지 않는다. 도구별 요구는 [분석기](ANALYZERS.md)에 있다.
- **디스크.** 상태 디렉터리에는 데이터베이스, 소스 사본, 작업 사본, 로그, 산출물이 쌓인다. 크기 상한은 [저장 공간](#저장-공간)에서 정한다.

## 첫 시작

```sh
codeye up ~/src/myproject
```

이 한 줄이 다음을 한다.

1. 설정 파일(`~/.config/codeye/personal.json`)이 없으면 `schema_version`만 담아 만든다. 비어 있는 필드는 기본값이다. 상태 디렉터리는 `~/.local/state/codeye`(`XDG_STATE_HOME`을 따른다)다.
2. PostgreSQL을 찾아 상태 디렉터리의 `postgres/data`에 cluster를 만들고 127.0.0.1의 빈 포트(55432부터)로 띄운다. 비밀번호는 만들어 `postgres/password`(0600)에 둔다. 셸의 `PG*` 변수와 `CODEYE_DATABASE_URL`은 무시하고 그렇다고 알린다.
3. schema를 만든다. 데이터가 있는 DB의 schema가 이전 버전이면 먼저 `pg_dump` 사본을 백업 디렉터리에 쓰고 migration을 적용한다.
4. controller(`127.0.0.1:7331`, 쓰이고 있으면 7340까지 다음 빈 포트)와 로컬 에이전트를 띄운다.
5. 홈 디렉터리 아래 폴더는 기본 root `home` 아래 상대 경로로, 그 밖의 폴더는 그 폴더 자체를 root로 설정에 더해 프로젝트로 등록하고 분석을 요청한다. 이미 등록한 폴더면 그 프로젝트에 새 분석을 요청한다(분석이 아직 대기·실행 중이면 그것을 보여 준다).
6. 로그인된 주소를 출력하고 브라우저로 연다. WSL에서는 `wslview` 또는 `explorer.exe`로 Windows 브라우저를 연다. 그 주소는 해당 프로젝트와 실행 화면으로 바로 간다.

첫 실행의 출력(실제 실행의 출력에서 경로·ID·pid만 예시로 바꾸고, 찾은 도구 줄은 줄였다):

```text
wrote a new configuration: /home/user/.config/codeye/personal.json
TOOL         STATUS   VERSION   WHERE / HOW TO ADD
go           found    1.27.4    /usr/local/go/bin/go
…
opencode     missing  -         not found - install OpenCode (`curl -fsSL https://opencode.ai/install | bash`) or set `agent.model.opencode`
go module cache: ~/go/pkg/mod; python type checkers: mypy and pyright both run
model: none (set OPENAI_API_KEY, ANTHROPIC_API_KEY or OPENCODE_API_KEY and run codeye up again) - static analysis only
initialized a PostgreSQL 17.10 cluster in /home/user/.local/state/codeye/postgres/data (binaries: /usr/lib/postgresql/17/bin)
PostgreSQL 17.10 started on 127.0.0.1:55432 (log /home/user/.local/state/codeye/logs/postgres.log)
database schema created (migration 11)
created the personal user 2ce93dc8-…
registered source root "home" (f655c03e-…)
registered the Git host github.com (public repositories)
CODEYE is running at http://127.0.0.1:7331 (controller pid 4101, agent pid 4113); logs in /home/user/.local/state/codeye/logs
registered project "myproject" (/home/user/src/myproject)
queued an analysis of "myproject" (run 4699c64a-…); the browser shows its progress and the report

Open CODEYE (signed in; the link works once, within 10 minutes):
  http://127.0.0.1:7331/#launch=…&project=26a4247c-…&run=4699c64a-…

opened the browser with xdg-open
The browser stays signed in for 7 days. New link: codeye open. Stop: codeye stop.
```

- 주소의 `#launch=` 값은 한 번만 쓰는 10분짜리 로그인 토큰이다. 브라우저가 읽자마자 주소창에서 지운다. 화면을 다시 열 때는 `codeye open`을 한다(새 링크를 출력하고 브라우저를 연다). 브라우저를 열지 않으려면 `up`·`open`에 `--no-open`을 준다.
- 로그인은 7일 동안 유지된다.
- 경로 없이 `codeye up`만 하면 시작(또는 이미 실행 중이면 새 링크)만 한다. 실행 중에 다른 폴더로 `codeye up`을 하면 그 폴더를 source root로 더하고, 에이전트를 다시 등록한다. 에이전트는 실행 중인 분석을 끝낸 뒤 다시 시작한다.
- 상태 디렉터리나 백업 디렉터리를 품거나 그 안에 있는 폴더(예: 홈 디렉터리 전체)는 받지 않는다. 오류가 문제의 폴더를 알려 주므로 프로젝트가 든 하위 폴더를 준다. 화면에서 등록하든 root와 상대 경로로 등록하든 같고, 에이전트도 소스를 읽기 전에 다시 확인한다.
- 홈 디렉터리는 설정에 적지 않아도 읽을 수 있는 폴더(`home` root)다. 다른 곳의 폴더는 처음 한 번 `codeye up <폴더>`로 더한다.

**공개 Git 저장소**는 주소를 그대로 준다. 브랜치를 비우면 저장소의 기본 브랜치다.

```sh
codeye up https://github.com/BurntSushi/termcolor
codeye up https://github.com/OWNER/REPO/tree/release   # 그 브랜치
```

실제 출력(이미 실행 중인 설치에서, 도구 표를 빼고 링크 토큰만 줄였다):

```text
go module cache: ~/go/pkg/mod; python type checkers: mypy and pyright both run
model: none (set OPENAI_API_KEY, ANTHROPIC_API_KEY or OPENCODE_API_KEY and run codeye up again) - static analysis only
CODEYE is already running at http://127.0.0.1:7331 (controller pid 1673797, agent pid 1673812)
registered project "termcolor" (https://github.com/BurntSushi/termcolor)
queued an analysis of "termcolor" (run 9e411b16-4873-401b-a7d2-6b490c2566a6); the browser shows its progress and the report

Open CODEYE (signed in; the link works once, within 10 minutes):
  http://127.0.0.1:7331/#launch=…&project=b97f6ebe-f847-460f-b65a-c94f1583e752&run=9e411b16-4873-401b-a7d2-6b490c2566a6
```

- 받는 주소는 `https://HOST/OWNER/REPO`다(`https://`는 생략해도 된다). 허용 호스트는 설정의 `git_hosts`이고 기본은 `["github.com"]`이다. `git`이 없으면 주소를 받지 않고 그렇다고 알린다.
- 비공개 저장소는 아직 지원하지 않는다. 인증을 요구하는 주소(비공개이거나 없는 저장소)는 소스 확보가 "저장소를 인증 없이 읽을 수 없음"으로 실패한다. 호스트에 닿지 못하면 "Git 호스트에 연결하지 못함"이다.
- 저장소의 빌드 스크립트(`cargo check` 등)는 기본으로 실행하지 않는다([프로젝트별 분석 선택](#프로젝트별-분석-선택)).
- `web/dist`가 없으면 화면 없이 API만 뜨고 `web UI not built - run …`을 출력한다.
- 개인 사용자는 관리자다. 사용자 이름과 비밀번호 없이 로그인 링크로 들어간다. 관리자 역할은 계정과 source root 허가를 다룰 뿐 프로젝트 권한은 아니며, 개인 사용자는 자기가 등록한 프로젝트의 소유자다. 다른 사람과 함께 쓰는 배포는 [계정과 관리자 CLI](OPERATIONS.md#계정과-관리자-cli)와 [TLS 수신](OPERATIONS.md#tls-수신)을 본다.

`codeye status`로 상태를 본다. `postgres running port=…`가 관리형 PostgreSQL이다.

`codeye start`도 같은 일을 하지만 경로를 받지 않고 브라우저를 열지 않는다. 로그인 링크는 `state_dir/login-link`(0600) 파일에 쓴다.

## 설정 파일

기본 위치는 `~/.config/codeye/personal.json`이다(`XDG_CONFIG_HOME`을 따른다). 다른 위치는 모든 명령에 `--config PATH`로 준다. `codeye up`이 만든 파일은 `schema_version`뿐이고, `up`은 자기가 다루는 필드(`source_roots`에 더한 폴더, `database.postgres_bin_dir`)만 고친다. 기본값은 파일에 쓰지 않는다. 더 정하고 싶은 것만 적는다. 예시는 [examples/personal.json](../examples/personal.json)이다.

```json
{
  "schema_version": "0.1",
  "source_roots": [{"label": "work", "path": "/home/user/src"}],
  "agent": {"max_workers": 2, "tools": {"staticcheck": "/home/user/go/bin/staticcheck"}},
  "quota": {"workspace_bytes": 10737418240, "artifacts_bytes": 21474836480},
  "backup_dir": "/home/user/codeye-backups"
}
```

- 경로는 모두 절대 경로다. `state_dir`와 source root는 서로 포함하면 안 된다. 알 수 없는 필드가 있으면 설정 전체를 거부한다.
- `listen`은 loopback 주소만 받는다. 적으면 그 포트만 쓴다(쓰이고 있으면 시작하지 않는다). 다른 사람이 접속하는 서버 배포는 개인 모드의 범위가 아니다.
- 필드 전체 표는 [개인 모드 설정 파일](OPERATIONS.md#개인-모드-설정-파일)에 있다.

**이미 있는 PostgreSQL을 쓰려면**(`"database": {"mode": "external", "postgres_bin_dir": "/usr/pgsql-17/bin"}`) 운영자가 서버와 CODEYE 전용의 빈 데이터베이스·사용자를 준비하고, 연결은 명령을 실행하는 셸의 `CODEYE_DATABASE_URL` 환경변수로 준다. 형식은 `postgresql://USER:PASSWORD@127.0.0.1:5432/DBNAME?sslmode=disable`이다. loopback이 아니면 `sslmode`를 생략해 `verify-full`로 둔다. 이 모드에서는 `codeye migrate`를 직접 하고, `PG*` 변수가 환경에 있으면 DB 열기를 거부하며, 백업·복원의 `pg_dump`·`pg_restore`는 `postgres_bin_dir`에서만 찾는다.

```sh
export CODEYE_DATABASE_URL="$(cat ~/.config/codeye/database-url)"
codeye migrate
codeye start
```

## 프로젝트와 source root

**source root**는 분석할 프로젝트가 들어 있는 디렉터리다. 에이전트는 설정에 적힌 root 안만 읽는다. `codeye up PATH`는 PATH가 이미 있는 root 안이면 그 root를 쓰고(상대 경로로 등록), 아니면 PATH 자체를 새 root로 더한다. root의 label은 폴더 이름이다(겹치면 `-2`를 붙인다).

설정에 직접 root를 더해도 된다(`{"label": "more", "path": "/home/user/more"}`). 그 뒤 `codeye up`을 하면 root를 등록하고 에이전트를 다시 등록한다. 이전 에이전트 상태는 지우지 않고 이름만 바꿔 둔다.

```text
registered source root "more" (b54bb497-…)
restarting the agent: its configuration changed (running work finishes first, up to 300s)
re-enrolling the local agent: the configured source roots changed (previous state kept in /…/state/agent.retired-…)
```

화면에서는 "분석할 코드"의 **폴더 경로 또는 Git 주소** 입력란에 절대 경로나 저장소 주소를 붙여넣고 **분석**을 누른다. 프로젝트를 등록하고(같은 폴더·저장소면 그 프로젝트를 쓴다) 분석을 요청한 뒤 바로 그 실행 화면으로 간다. 분석이 아직 진행 중이면 새로 요청하지 않고 그 실행을 보여 준다. 접힌 **고급**에는 이름, 브랜치(`…/tree/브랜치` 주소면 채워진다), 아래의 두 선택이 있다. source root를 골라 상대 경로로 등록하는 이전 양식은 "소스 루트를 골라 등록" 아래에 있다. 프로젝트 설명이나 요구사항 문서는 필요 없다. CODEYE가 문서·환경·소스를 직접 조사한다.

### 프로젝트별 분석 선택

개인 모드에서는 프로젝트마다 두 가지를 고른다. 등록할 때 고급에서 정하고, 나중에는 보고서의 "더 나은 결과를 얻으려면"에서 켠다.

| 선택 | 뜻 | 로컬 폴더 기본 | Git 저장소 기본 |
|---|---|---|---|
| 소스를 모델에 보냄 | 설정한 검토 모델이 이 프로젝트의 소스를 읽는다. 끄면 정적 분석만 한다 | 켬 | 켬 |
| 저장소의 빌드 스크립트 실행 | build script·`cargo check`·plugin처럼 저장소의 코드를 실행하는 분석기를 돌린다([저장소 코드 실행 허가](ANALYZERS.md)) | 켬 | 끔 |

- 내 폴더는 내 코드라 둘 다 켠다. Git 저장소는 누구의 코드든 될 수 있어 빌드 스크립트를 실행하지 않는다. 그래서 Rust 저장소는 처음에 `cargo check`·clippy가 "저장소 코드 실행이 허가되지 않아 실행하지 않음"으로 빠진다. 켜면 그 저장소의 코드가 이 컴퓨터에서 내 권한으로 실행된다.
- 설정 파일 source root의 `repository_code`·`model_review`는 개인 모드에서 쓰이지 않는다. 서버 모드의 노드는 계속 운영자의 root별 허가를 따른다.

## 사용할 수 있는 도구 확인

```bash
codeye tools
```

이 머신에서 찾은 도구(경로와 버전), 찾지 못한 도구와 추가하는 방법을 표로 보여 준다. 화면의 **도구** 메뉴도 `codeye up`이 마지막으로 찾은 같은 표와 모델·Git·경로로 분석할 수 있는 폴더를 보여 준다. 개인 모드는 시작할 때 같은 탐색을 하므로 도구 경로를 설정에 적지 않아도 된다. 설정의 `agent.tools.<이름>`에 적은 경로는 항상 탐색보다 우선한다. 없는 도구에 해당하는 분석은 보고서에 "미지원"으로 나온다([도구 찾기](ANALYZERS.md#도구-찾기), [운영](OPERATIONS.md#codeye-tools)).

## 분석 실행

위의 입력란으로 시작하거나, 프로젝트를 고르고 "분석 요청 등록"을 누른다. 요청은 대기열에 저장되고 에이전트가 가져가 실행한다. 화면은 단계(소스 확보, 환경 분석, 문서 검토, 정적 분석, 코드 분석, 보고서 생성)별 상태와 진행률을 보여 준다.

- **일부만 완료(partial)**: 일부 작업을 하지 못했다는 뜻이다. 화면에 이유가 나온다. 예: "노드 운영자가 이 분석 도구 경로를 지정하지 않아 실행하지 않음", "소스의 모델 전송이 승인되지 않아 제외됨".
- **대기 사유**: 시작하지 않은 run에는 이유가 표시된다. 저장 공간 상한에 걸리면 "저장 공간 상한에 도달해 새 작업을 시작하지 않습니다"가 나온다([저장 공간](#저장-공간)).
- 실행을 취소하면 실행 중인 도구 프로세스를 정리하고 정리 결과를 확인한 뒤 취소로 표시한다. 확인하지 못하면 `recovery_required`로 남는다.
- **다시 시도**: 실패·일부 완료·취소된 실행에 "다시 시도" 버튼이 있다. 고정한 입력이 노드에 남아 있는 실패·취소 실행은 같은 입력으로 다시 하고, 그 밖(소스 확보 실패, 도구를 설치하거나 선택을 바꾼 뒤)에는 소스를 다시 받아 새 실행을 시작한다. 화면은 그 실행을 따라간다. `recovery_required`는 다시 시도하지 못한다.
- 소스를 읽지 못한 실행은 이유를 보여 준다. 예: "저장소를 인증 없이 읽을 수 없음 — 비공개 저장소는 아직 지원하지 않습니다".

## 보고서 읽기

실행이 끝나 보고서가 게시되면 실행 화면에 저절로 열린다. 보고서의 구조와 판정 규칙은 [보고서](REPORT.md#분석-종류별-절)에 있다. 보고서는 분석 종류별 절로 나뉜다. 맨 위 종합부터 읽고, 필요한 절을 펼친다.

- **더 나은 결과를 얻으려면.** 보고서 위에, 분석 공백 중 사용자가 채울 수 있는 것을 짧게 적는다. 없는 도구는 설치 방법(도구 화면과 같은 문구), 모델이 없으면 키 설정, 호출 상한 때문에 못 본 파일이 있으면 `review_max_calls`, Git 저장소의 빌드 스크립트가 꺼져 빠진 분석이 있으면 "켜고 다시 시도" 버튼이다. 버튼은 프로젝트 관리자에게만 보인다. 도구를 설치한 뒤에는 `codeye up`을 다시 하고 "다시 시도"를 누른다.
- **종합.** 품질 판정, 도구 진단·발견 사항·조치 검토·미확정·반증됨의 수, 분석 종류별 상태 표, 먼저 볼 것이다. 표의 절 이름이나 먼저 볼 것의 버튼을 누르면 그 절이 열린다. 수치는 저장된 구조화 데이터에서 계산한다. 모델의 서술로 바뀌지 않는다.
- **이전 실행과 비교.** 종합 아래에 화면에서만 보인다([이전 실행과 비교](#이전-실행과-비교)). 내보낸 파일에는 없다.
- **환경 분석.** 모듈, 모듈이 선언한 도구 요구(예: `go 1.22`), 노드에서 관측한 도구 버전, 요구별 판정(충족·불일치·판단 불가)이다.
- **문서 분석.** 찾은 문서와 읽었는지 여부(예산 초과·제외 포함), 모듈별로 적용되는 문서, 같은 범위에 겹치는 안내다.
- **정적 분석.** 언어별, 그 안에서 도구별로 실행 결과, 처리한 파일 수, 규칙별 진단 수와 도구가 만든 발견이다. 실행하지 않은 도구는 "실행하지 않음"으로 나오고 0건이 아니다.
- **구조·중복 지표.** 복잡도·중복 같은 측정값과 임계 초과 위치다. 결함이 아니다.
- **비밀정보.** 비밀값으로 보이는 값을 탐지한 위치와 규칙이다. 값 자체는 어디에도 저장하지 않는다. 모델 입력에서 그 값을 걸렀는지도 나온다.
- **코드 분석(모델 검토).** 모델 역할별 결과, 호출 수, 모델이 본 파일 수, 모델 검토 발견이다. 파일이 많아 모듈 단위로 나눠 검토한 실행에는 **모듈별 검토 장부**가 있다. 모듈마다 검토 대상 코드·테스트 파일 중 모델에 보낸 수, 호출 상한(`agent.model.review_max_calls`) 때문에 보내지 못한 수, 비밀값 탐지로 뺀 수 등을 보여 준다. 모델에 보낸 범위는 검증된 범위가 아니다. 보내지 못한 파일이 많으면 상한을 올리거나 범위를 좁혀 다시 실행한다. 종합의 코드 분석 줄에도 보낸 파일과 보내지 않은 파일 수가 나온다.
- **모델 해석.** 환경·문서·코드 분석 절에서 "모델 해석"이라고 표시된 상자는 모델이 쓴 문장이다. 근거 위치가 함께 나오며 판정이나 수치의 근거가 아니다. 모델 검토를 하지 않았으면 그 이유가 나온다.
- **분석 공백.** 하지 못했거나 일부만 한 분석과 그 이유를 모은 목록이다. 여기 적힌 범위는 "문제 없음"이 아니다.
- **발견.** 정적 분석과 코드 분석 절의 발견마다 발생 조건, 심각도 근거, 영향, 개선 방향, 확인 방법, 근거가 있다. 근거는 snapshot의 파일 위치와 hash, 도구 원시 출력 산출물이다.
- **이전 보고서.** 절이 생기기 전에 게시된 보고서는 요약, 분석 범위와 한계, 영역별 발견 사항의 이전 배치로 보인다.
- **사람 판정.** 발견에 "확인함", "위험 수용", "오탐" 같은 판정과 사유를 기록한다. 기록은 감사 이력으로 남고 수정·삭제되지 않는다. 다음 실행의 비교에 반영된다.
- **내보내기.** JSON·Markdown·HTML이다. "코드 포함"을 고르지 않으면 코드 발췌를 빼고 내보낸다.

## 예약 실행

프로젝트 화면의 "예약 실행"에서 정해진 시각마다 같은 분석 요청을 대기열에 넣는다. 예약 등록과 삭제는 프로젝트 관리자만 한다.

- **cron 식**은 5필드(분 시 일 월 요일)다. 예: `0 12 * * 1-5`(평일 12:00), `@daily`, `@weekly`. 초 필드와 `L`·`W`·`#`·`?`는 받지 않는다.
- **시간대**는 IANA 이름(`Asia/Seoul`, `UTC`)만 받는다. `Local`은 거부한다.
- **이전 실행이 끝나지 않았을 때**: 기본은 최신 1건만 대기시키고 더 이전 대기분은 최신 예약으로 대체한다. "이번 예약 건너뛰기"나 "모두 대기열에 넣기(최대 10건)"를 고를 수 있다.
- **서버가 멈춰 지난 예약**: 기본은 멈춘 사이 지난 예약 중 최신 1건만 실행한다. 멈춘 시간만큼 몰아서 실행하지 않는다. "2분보다 늦으면 건너뛰기"를 고를 수 있다. 실행하지 않은 구간은 화면에 "서버가 멈춘 사이 …부터의 예약은 실행하지 않았습니다"로 나온다.
- 예약 실행은 등록한 관리자의 권한으로 요청되고, 실행할 때마다 권한을 다시 확인한다. 입력은 실행이 시작되는 시점에 고정한다.
- 서머타임이 있는 시간대에서 특정 시각(`30 2 * * *`)의 예약은 하루 한 번만 실행된다. 매시·15분 같은 간격 예약은 경과 시간 기준으로 돌고, 시계가 뒤로 가는 날의 반복 시각에도 실행한다.
- 예약을 삭제해도 이미 대기열에 넣은 실행은 취소되지 않는다. 대기 중이던 예약 회차만 취소된다.
- 예약은 `codeye start`로 띄운 controller가 실행 중일 때만 평가된다. 정지해 둔 동안의 예약은 위 규칙으로 처리한다.

## 이전 실행과 비교

보고서 화면은 같은 프로젝트의 이전 실행과 비교한다. 기준은 같은 소스(Git 저장소와 브랜치, 또는 source root와 경로), 같은 프로필과 범위의 가장 최근 완료·부분 완료 보고서다. 비교를 불러오지 못해도 보고서는 그대로 보인다.

| 상태 | 뜻 |
|---|---|
| `new` | 이번에만 있다 |
| `persisting` | 양쪽에 있고 개수·심각도·검증 상태·영역이 같다. 줄 위치만 바뀐 것도 여기에 든다 |
| `changed` | 양쪽에 있고 개수·심각도·검증 상태·영역 중 하나가 다르다 |
| `resolved_observed` | 기준에만 있고, 이번 실행이 같은 소스·범위에서 같은 도구를 성공적으로 실행해 그 파일을 실제로 검사했다. 도구 버전과 규칙이 같고, 소스 안 억제 주석 때문에 사라진 것이 아니다 |
| `absent_unverified` | 기준에만 있으나 위 조건을 확인하지 못했다. 사유가 함께 나온다 |

- **사라진 발견을 해결로 세지 않는다.** 검사가 빠졌거나 실패했거나 범위가 줄었거나 규칙이 바뀌었거나 억제 주석이 생겼으면 `absent_unverified`다. 모델이 후보를 다시 내지 않은 것은 관찰이 아니므로 모델 후보는 `resolved_observed`가 되지 않는다.
- 같은 식별의 발견이 한쪽에 여럿이면 하나씩 짝짓지 않고 묶음 전체의 상태를 `ambiguous`로 표시한다. 경로만 바뀐 항목은 이동 후보로 보여 줄 뿐 자동으로 합치지 않는다.
- **사람 판정의 적용.** 판정은 발견의 식별(fingerprint)마다 최신 기록 하나가 적용된다. 식별이 약하거나 모호하면 자동 적용하지 않고 사람이 그 발견에서 다시 판정해야 한다. 만료 시각이 지났거나 경로 범위 밖이면 `open`으로 돌아간다.
- 위험 수용·오탐·억제로 판정한 발견은 조치 집계에서 빠지고 "숨김 판정"으로 따로 센다. 전체 발견 수와 원시 진단, 비교에는 그대로 남는다.
- 과거 리비전을 열면 그 리비전을 게시할 때의 판정이 보이고, 지금의 판정은 따로 표시한다.

비교와 판정의 계약은 [보고서](REPORT.md)에 있다.

## 모델 설정

모델 검토에는 OpenCode(`~/.opencode/bin/opencode`)와 API 키 하나만 있으면 된다. OpenAI 키를 쓰는 예:

```bash
export OPENAI_API_KEY=sk-...      # 이미 ~/.bashrc에 있으면 그대로 둔다
codeye up
```

- `codeye up`은 `OPENAI_API_KEY`(또는 `ANTHROPIC_API_KEY`, `OPENCODE_API_KEY`)를 `<state_dir>/secrets/model.json`(본인만 읽을 수 있는 파일)에 저장하고, 모델 `openai/gpt-6.2-sol`로 한 번 시험 호출(`codeye model probe`)한 뒤 결과를 보여 준다. 키를 바꾼 뒤 다시 `codeye up`을 하면 새 키로 다시 시험한다.
- 키는 이 파일에만 남는다. 로그·보고서·화면·프로세스 인자에는 나오지 않는다. 키를 지우려면 그 파일을 지운다.
- 모델을 바꾸려면 설정에 `"agent": {"model": {"name": "gpt-6.1-sol"}}`처럼 이름만 적는다(`PROVIDER/MODEL`도 된다). OpenCode에 `opencode auth login`으로 API 키를 넣어 두었다면 환경변수 없이 그 키를 쓴다.
- 시험 호출이 실패해도 분석은 실행된다. 보고서의 코드 분석 절에 모델 검토를 하지 못한 이유(예: "모델 공급자가 API 키를 거부했다")가 나온다. 언제든 `codeye model probe`로 다시 확인한다. 실제 추론을 한 번 요청하므로 비용이 조금 든다.
- 모델이 설정되면 "소스를 모델에 보냄"이 켜진 프로젝트(기본 켬, 로컬 폴더와 Git 저장소 모두)의 코드를 모델에 보낸다. 빼려면 등록할 때 고급에서 끈다. 비밀값으로 선별된 파일은 보내지 않는다.
- OpenAI 호환 서버(사내 게이트웨이, 로컬 추론 서버)를 쓰려면 `model.json`에 `"base_url": "https://…/v1"`을 더한다. chat completions만 지원하는 서버면 `"openai_compatible": true`도 더한다([검토](REVIEW.md#모델-설정)).

## 모델 검토 설정

모델 설정이 없으면 문서·코드의 모델 검토 단계는 실행하지 않는다. 보고서에는 "모델 검토 미수행"으로 나온다. 다른 모델이나 인터넷 서비스로 자동 전환하지 않는다.

`agent.model`의 모든 항목은 선택이다. 위 [모델 설정](#모델-설정)만으로 충분하다.

```json
"agent": {
  "model": {"name": "openai/gpt-6.2-sol", "opencode": "/home/user/.opencode/bin/opencode",
            "credential_file": "/home/user/.config/codeye/model.json", "timeout_seconds": 180,
            "review_pack": "review-r1", "review_max_calls": 99}
}
```

- `opencode`를 비우면 `~/.opencode/bin/opencode`, 없으면 PATH의 `opencode`를 쓴다. `name`을 비우면 키 provider의 기본 모델이다. `credential_file`은 키 파일을 다른 곳에 둘 때만 적는다. `timeout_seconds`는 모델 호출 한 번의 제한시간이다(기본 180).
- `relay_file`은 승인된 로컬 relay를 쓸 때만 넣는다. 이 저장소의 `scripts/model_budget_relay.py`는 예산 상한을 강제하는 QA 전용 relay이며 제품 구성요소가 아니다. 사용법은 그 파일의 머리말과 `--help`에 있다.
- `review_pack`의 기본은 `review-r1`이다. 비워 두면 `review-r1`을 쓴다. `review-v1`은 이전 기록을 같은 조건으로 재현할 때만 고른다([검토](REVIEW.md)).
- `review_max_calls`는 run 하나(시도 하나)가 쓸 수 있는 모델 호출 수의 상한이다. 비우면 99이고 15~207을 받는다. 처음 다섯 역할이 최대 15회를 쓰고, 남은 호출로 overview가 보내지 못한 파일을 모듈 단위로 더 검토한다. 기본값으로 중간 규모 저장소의 대부분이 모델에 간다. 비용: 회귀 실행에서 실모델 호출 55회의 비용은 보고된 사용량으로 약 USD 0.94, 보수적 장부 차감으로 USD 2.02였다. 기본 상한 99회를 다 쓰는 run은 그 대략 두 배(보고 사용량 약 USD 1.7, 장부 차감 약 USD 3.6)다. 실제 비용은 모델·저장소 크기·형식 복구 횟수에 따라 다르다. 비용을 줄이려면 `review_max_calls`를 낮춘다(예: 33). 15는 처음 다섯 역할만 실행하고 모듈 단위 추가 검토를 하지 않는다. 비밀값 선별(`agent.tools.gitleaks`)이 있으면 선별된 파일을 빼고 보내고, 없으면 기본 민감값 패턴 필터만 거쳐 보내며 보고서에 "비밀값 미선별"이 표시된다([검토](REVIEW.md#모듈-단위-검토-분할)).
- 모델 연결은 `codeye model probe`(개인 설정의 모델) 또는 `codeye model probe --executable PATH --model PROVIDER/MODEL [--credential-file PATH | --relay-file PATH]`로 확인한다. 이 명령은 실제 추론을 요청하므로 비용이 들 수 있다. `codeye doctor`는 모델을 호출하지 않는다.

## 저장 공간

설정 `quota`에 영역별 상한(바이트)을 둔다. 영역은 소스 사본(`snapshot_bytes`), 작업 사본(`workspace_bytes`), 도구 cache(`cache_bytes`), 로그(`logs_bytes`), 분석 산출물(`artifacts_bytes`)이다. 0이나 생략은 상한 없음이다.

- 상한에 도달하면 **새 분석만** 시작하지 않는다. 실행 중인 분석은 끝까지 하며, 그동안 상한을 넘을 수 있다. 보고서와 산출물은 지우지 않는다.
- 사용량은 `codeye status`(적용 기준)와 `codeye doctor --config PATH`(지금 디렉터리를 잰 값과 파일시스템 여유)로 본다.

```text
storage workspace  0 bytes limit=2000000000 exceeded=false (node_heartbeat)
storage artifacts  19459 bytes limit=1000000000 exceeded=false (database_accounting)
```

- 공간이 부족하면 운영자가 정리하거나 상한을 늘린다. 상한을 늘리려면 설정을 바꾸고 `codeye stop` → `codeye up`을 한다. CODEYE는 오래된 파일을 자동으로 지우지 않는다.

## 정지

```sh
codeye stop
```

1. 에이전트에 drain을 요청한다. 새 작업을 받지 않고 실행 중인 분석을 끝낸다.
2. `stop.drain_timeout_seconds`(기본 300초) 안에 끝나지 않으면 실행 중인 분석을 취소하고 정리를 확인한다. 취소된 run은 `failed`로 남는다. 다시 분석하려면 새로 요청한다.
3. 에이전트가 끝나면 controller를 멈춘다.
4. 둘 다 멈췄으면 관리형 PostgreSQL을 멈춘다(`postgres   stopped`). 에이전트나 controller가 남아 있으면 PostgreSQL도 남겨 둔다(`left_running`).

다음 `codeye up`은 같은 cluster와 데이터를 그대로 쓴다.

마지막 줄의 `outcome`을 확인한다.

| outcome | 뜻과 조치 |
|---|---|
| `stopped` | 정상 정지 |
| `not_running` | 이미 멈춰 있었다 |
| `recovery_required` | 정리를 확인하지 못했다. 에이전트가 아직 실행 중이면 controller도 남겨 둔다. `codeye status`와 `state_dir/logs`를 확인하고 잠시 뒤 `codeye stop`을 다시 한다 |
| `failed` | controller나 PostgreSQL이 멈추지 않았다. 강제 종료하지 않았다 |

CODEYE는 프로세스를 강제 종료(SIGKILL)하지 않는다. 기록한 프로세스와 시작 시각이 같은 프로세스에만 신호를 보낸다.

## 백업과 복원

**백업.** 실행 중에도 할 수 있다. 다만 정지 뒤에 하면 진행 중인 분석이 없어 복원 결과가 깔끔하다.

```sh
codeye stop
codeye backup                     # backup_dir/codeye-<UTC 시각>에 만든다
codeye backup --output /mnt/backup/codeye-1   # 위치를 직접 줄 때(없거나 빈 디렉터리)
```

`backup_dir`를 적지 않았으면 상태 디렉터리 옆의 `<state_dir>-backups`(기본 `~/.local/state/codeye-backups`)에 만든다. 관리형 PostgreSQL이 멈춰 있으면 백업 동안만 띄웠다가 다시 멈춘다. `pg_dump`는 그 cluster의 프로그램을 쓴다.

```text
backup written to /…/codeye-backups/codeye-20261118T191428Z
  database: migration 9, PostgreSQL 17.10, dump 175706 bytes sha256 8ea9902…
  artifacts: 14 contents, 19418 bytes (finalized at the same snapshot)
  rows: projects 1, runs 1, reports 1, evidence 0, schedules 0
```

백업 세트에는 DB dump와 확정된 산출물이 같은 시점으로 들어 있다. 연결 정보와 비밀번호는 없지만 프로젝트·보고서 같은 운영 데이터가 있다. 세트는 권한을 지켜 보관한다. 백업은 전체 세트만 만든다. 증분·압축·암호화·보존 개수·자동 예약 백업은 없다.

**복원.** 새 빈 데이터베이스와 새 `state_dir`에만 복원한다. 기존 설치를 덮어쓰지 않는다. 관리형 PostgreSQL이면 새 `state_dir`를 적은 설정 파일 하나만 만든다. restore가 그 상태 디렉터리에 새 cluster와 빈 DB를 만들고 복원한다.

```sh
echo '{"schema_version": "0.1", "state_dir": "/home/user/.local/state/codeye-restored"}' > ~/.config/codeye/restored.json
codeye restore --config ~/.config/codeye/restored.json --from /…/codeye-backups/codeye-20261118T191428Z
codeye up --config ~/.config/codeye/restored.json
```

`database.mode: external`이면 빈 데이터베이스를 직접 만들고(`codeye migrate`는 하지 않는다), 새 `state_dir`로 설정을 만들고, `CODEYE_DATABASE_URL`을 그 DB로 둔 셸에서 같은 restore·start를 한다.

```text
restored /…/codeye-20261118T191428Z (created 2026-11-18T19:14:28Z, migration 9)
  verified: dump and 14 artifact contents by sha256; every restored artifact row has its bytes
  rows: projects 1, runs 1, reports 1, evidence 0, finding reviews 1, schedules 0
  fenced: 1 nodes revoked, 1 sessions revoked, 0 leased attempts -> recovery_required (0 reservations kept)
next: codeye up (the local agent enrolls as a new node)
```

복원은 세트의 hash부터 모두 확인한다. 이전 노드, 로그인 세션, 쓰지 않은 링크는 무효가 된다. 그래서 복원본의 `up`이 새 로그인 링크를 주고, 에이전트는 새 노드로 등록한다. 백업 때 실행 중이던 분석은 `recovery_required`로 남는다. 그 run은 다시 요청한다. 이 run이 쥔 예약을 풀어 주는 운영자 명령은 아직 없다([백업과 복원](OPERATIONS.md#백업과-복원)).

## 업그레이드 (설계 절차, 시험 안 함)

이전 버전에서 올리는 절차 전체는 시험하지 못했다. 각 명령은 개별로 실행해 보았다.

```sh
codeye stop
codeye backup
# 새 codeye 바이너리와 web/dist로 교체
codeye up
```

관리형 PostgreSQL이면 `codeye up`(과 `start`)이 schema를 맞춘다. 데이터가 있으면 먼저 `pg_dump` 사본(`<백업 디렉터리>/codeye-pre-migrate-<UTC>/database.dump`, 빈 DB에 `pg_restore`로 되돌린다)을 쓴다. `database.mode: external`이면 migration은 `codeye migrate`만 하고, start는 schema가 다르면 시작하지 않는다.

## 문제 해결

| 증상(출력) | 원인과 조치 |
|---|---|
| `PostgreSQL binaries not found (searched: …)` | PostgreSQL을 설치한다(Ubuntu: `sudo apt install postgresql`). 다른 위치면 `codeye up --postgres-bin-dir DIR` 또는 `database.postgres_bin_dir`. 이미 있는 서버를 쓰려면 `database.mode: external` |
| `database.postgres_bin_dir …: missing pg_restore` 같은 오류 | 적은 디렉터리에 필요한 프로그램이 없다. 다섯 프로그램이 모두 있는 `bin` 디렉터리를 준다. 적은 경로가 틀리면 다른 곳을 찾지 않는다 |
| `the managed data directory … was created by PostgreSQL 16, found 17 binaries …` | cluster를 만든 주 버전의 프로그램을 `postgres_bin_dir`로 준다. 버전을 올리려면 그 버전으로 백업하고 새 `state_dir`에 복원한다 |
| `PostgreSQL did not start (…); see …/logs/postgres.log` | 그 로그를 본다 |
| `web UI not built - run …` | 실행 파일 옆에 `web/dist`가 없다. 빌드하거나 `web_dir`를 적는다. API는 떠 있다 |
| `note: ignoring PGHOST, … from the environment` | 관리형 모드는 셸의 `PG*`·`CODEYE_DATABASE_URL`을 쓰지 않는다. 정상 |
| `CODEYE_DATABASE_URL is not set (database.mode external)` | 셸에 DB URL이 없다. [설정 파일](#설정-파일) |
| `database unavailable: invalid input`, status의 `database=false` | external: URL 형식이 틀렸거나 환경에 `PGPASSWORD` 같은 `PG*` 변수가 있다. 그 변수를 지운다. managed: PostgreSQL이 멈춰 있다. `codeye up` |
| `database schema is outdated (applied N, this binary expects M)…` | external: 백업한 뒤 `codeye migrate` |
| `database schema is newer than this codeye binary …` | 더 새 바이너리로 만든 DB다. 그 바이너리를 쓴다 |
| `config is not valid: …` | 설정 오류. 메시지가 필드를 알려 준다(예: `agent.toolchain_path entries must be absolute, clean paths`) |
| `CODEYE is already running (controller …)` | 이미 실행 중이다. 종료 코드 0. `codeye up`은 이때 새 로그인 링크를 준다 |
| `127.0.0.1:7331 is in use by another program; using 127.0.0.1:7332` | 다른 프로그램이 기본 포트를 쓴다. 다음 빈 포트를 썼고 `stop`·`status`·`open`이 그 포트를 안다 |
| `the listen address … is already in use by a process codeye start did not record` | 설정에 적은 `listen` 포트를 다른 프로그램이 쓴다. `listen`을 바꾸거나 지우거나 그 프로그램을 멈춘다 |
| `the agent is still finishing its work and takes no new work; …` | 설정이 바뀌어 에이전트를 다시 띄우려 했지만 실행 중인 분석이 drain 시간 안에 끝나지 않았다. 에이전트는 그 분석을 끝내고 종료한다. `codeye status`로 확인한 뒤 같은 명령을 다시 한다 |
| `… contains or is inside the CODEYE data directory …` | 상태·백업 디렉터리를 포함하는 폴더는 분석하지 않는다. 프로젝트 폴더를 준다 |
| `an agent that codeye start did not record holds …/agent/agent.lock …` | 수동으로 띄운 `codeye agent`가 같은 상태 디렉터리를 쓴다. 그 프로세스를 직접 멈춘다 |
| `removed stale controller record (pid … pid_reused)` | 이전 프로세스가 비정상 종료됐다. 기록만 지웠고 신호는 보내지 않았다. 정상 |
| `status`의 `issue: stale_record`, 종료 코드 1 | 위와 같다. `codeye start`가 정리한다 |
| `the agent did not enroll and report a heartbeat in time` | `state_dir/logs/agent.log` 확인. 도구 경로·source root 경로가 맞는지 본다 |
| 분석이 시작되지 않고 저장 공간 상한 문구가 나온다 | [저장 공간](#저장-공간). `codeye status`의 `exceeded=true` 영역을 정리하거나 상한을 늘린다 |
| stop의 `outcome: recovery_required` | [정지](#정지) |
| `the target database is not empty; restore only into a new, empty database` | 복원 대상 DB가 비어 있지 않다. 새 DB를 만든다 |
| `the target state_dir already has personal.json; restore needs a fresh state_dir` | 새 `state_dir`를 쓴다 |
| `backup verification failed: …` | 세트가 손상됐거나 완성되지 않았다(manifest 없음). 다른 세트를 쓴다 |
| 로그인 링크가 만료됐거나 이미 썼다 | `codeye open` |
| `codeye start/stop/status manage processes on Linux only …` | Windows·macOS는 개인 모드를 지원하지 않는다 |

점검 순서: `codeye status --format json` → `codeye doctor --config PATH` → `state_dir/logs/controller.log`, `agent.log`, `postgres.log`. 로그와 `status`·`stop` 출력에는 DB 비밀번호와 토큰이 나오지 않는다(`up`·`open`이 출력하는 로그인 링크에는 일회용 토큰이 있다). 문의할 때 설정 파일과 로그를 그대로 보내도 연결 정보는 들어 있지 않다. 다만 경로와 프로젝트 이름은 들어 있다. 관리형 DB 비밀번호는 `state_dir/postgres/password`에만 있으니 상태 디렉터리를 통째로 보내지 않는다.

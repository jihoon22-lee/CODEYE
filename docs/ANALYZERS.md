# 정적 분석기

정적 분석 도구가 실행되는 조건과 결과를 관찰로 바꾸는 규칙을 다룬다. [분석기 목록](#분석기-목록)에서 도구·규칙 팩을, [도구 설정](#도구-설정)에서 플래그를 찾을 수 있다. [Go 내부 패키지 의존 관계](#go-내부-패키지-의존-관계)와 [구조·중복 지표 및 비밀값](#구조중복-지표와-비밀값-팩)은 보고서의 해당 절로 연결된다. 노드 설정은 [운영](OPERATIONS.md), 테스트 실행은 [실행](EXECUTION.md#기존-테스트-실행), 관찰 표시와 비교는 [보고서](REPORT.md)를 따른다.

구현 위치는 `internal/worker/analysis/` 아래다. 공통 산출물은 [`analyzer_observations` schema](../schemas/analyzer-observations.schema.json), 도구별 실행 서술은 [`analyzer.schema.json`](../schemas/analyzer.schema.json)을 따르는 `examples/analyzer-*.yaml`, 실제 실행으로 얻은 envelope 예시는 `examples/observations-*.json`이다.

## 선정 원칙

- 도구를 나열하지 않고 분석 관점의 빈틈을 채운다. 같은 검사기를 포함한 wrapper와 원본을 함께 실행하지 않는다.
- 도구가 진단한 사실과 실제 결함 여부를 분리한다. 모든 도구 진단은 검토 전의 낮은 등급 관찰이다.
- 모든 도구를 필수로 설치하지 않는다. 생태계마다 기준 분석기 하나만 판정 필수이고 나머지는 추가 깊이다.
- 실제로 실행해 확인한 버전에 parser와 종료 코드 해석을 고정한다. 확인하지 않은 OS·버전의 호환성을 주장하지 않는다.
- 기본 정적 규칙은 동작·오류·보안·성능 위험을 우선한다. Go의 서식 비교는 독립 작업이다. 모든 규칙 일괄 활성화를 기본으로 삼지 않는다.
- 의존성 CVE 검사, 상용 분석 엔진, 자동 수정 적용은 하지 않는다. suggested fix는 개수만 센다.

## 분석기 목록

카탈로그(`builtin.catalog()`) 순서가 계획 순서다. 생태계마다 기준 분석기 다음에 추가 깊이 분석기를 둔다.

| 분석기 ID | task key | 필수 | 도구 플래그 | 저장소 코드 실행 | 규칙 팩 | 저장소 설정 / 소스 안 억제 |
|---|---|---|---|---|---|---|
| `go.vet` | `go-vet`, `go-vet.mNN` | 예 | `--go` | 아니오 | `codeye-go-vet-default` v1(합성) | `none` / `none` |
| `go.staticcheck` | `staticcheck`, `staticcheck.mNN` | 아니오 | `--staticcheck` (+`--go`) | 아니오 | `codeye-go-staticcheck` v2 | `overridden` / `reported` |
| `go.gofmt` | `gofmt` | 아니오 | `--go` 도구 체인에 함께 설치된 `gofmt` | 아니오 | `codeye-go-format` v1 | `none` / `none` |
| `go.errcheck` | `errcheck`, `errcheck.mNN` | 아니오 | `--errcheck` (+`--go`) | 아니오 | `codeye-go-errcheck` v1 | `none` / `none` |
| `cpp.cppcheck` | `cppcheck` | 예 | `--cppcheck` | 아니오 | `codeye-cpp-cppcheck` v2 | compile DB 모드 `honored`, 축소 모드 `none` / `disabled` |
| `cpp.clang_tidy` | `clang-tidy` | 아니오 | `--clang-tidy` | 아니오 | `codeye-cpp-clang-tidy` v1 | `honored` / `honored` |
| `cpp.gcc_analyzer` | `gcc-analyzer` | 아니오 | `--gcc` | 아니오 | `codeye-c-gcc-file-descriptors` v1 | `honored` / `honored` |
| `python.ruff` | `ruff`, `ruff.mNN` | 예 | `--ruff` | 아니오 | `codeye-python-ruff` v1 | `overridden` / `reported` |
| `python.mypy` | `mypy`, `mypy.mNN` | 아니오 | `--mypy` | 아니오 | `codeye-python-mypy` v1 | `overridden` / `honored` |
| `python.pyright` | `pyright`, `pyright.mNN` | 아니오 | `--pyright` + `--node` | 아니오 | `codeye-python-pyright` v1 | `overridden` / `honored` |
| `python.import_linter` | `import-linter` | 아니오 | `--import-linter` | 아니오 | `codeye-import-linter` v1 | `honored` / `none` |
| `python.bandit` | `bandit`, `bandit.mNN` | 아니오 | `--bandit` | 아니오 | `codeye-python-bandit` v1 | `overridden` / `reported` |
| `javascript.eslint` | `eslint`, `eslint.mNN` | 예 | `--eslint` + `--node` | 아니오 | `codeye-js-eslint` v2 | `overridden` / `disabled` |
| `javascript.tsc` | `tsc`, `tsc.mNN` | 아니오 | `--tsc` + `--node` | 아니오 | `codeye-ts-tsc` v1 | `honored` / `honored` |
| `javascript.dependency_cruiser` | `dependency-cruiser` | 아니오 | `--dependency-cruiser` + `--node` | 아니오 | `codeye-dependency-cruiser` v1 | JSON 규칙 `honored` / `none` |
| `javascript.eslint_typed` | `eslint-typed`, `eslint-typed.mNN` | 아니오 | `--eslint` + `--node` | 아니오 | `codeye-ts-eslint-typed` v1 또는 `codeye-js-eslint-rejection-types` v1 | TS `honored`, JS `overridden` / `disabled` |
| `rust.cargocheck` | `cargo-check`, `cargo-check.mNN` | 예 | `--cargo` | **예** | `codeye-rust-cargo-check` v1 | `honored` / `honored` |
| `rust.clippy` | `clippy`, `clippy.mNN` | 아니오 | `--clippy` (+`--cargo`) | **예** | `codeye-rust-clippy` v1 | `honored` / `honored` |
| `rust.rustdoc` | `cargo-doc`, `cargo-doc.mNN` | 아니오 | `--cargo`, 같은 도구 체인의 rustdoc | **예** | `codeye-rust-doc` v1 | `honored` / `honored` |
| `java.pmd` | `pmd`, `pmd.mNN` | 예 | `--pmd` + `--java` | 아니오 | `codeye-java-pmd` v1 | `overridden` / `honored` |
| `java.spotbugs` | `spotbugs`, `spotbugs.mNN` | 아니오 | `--spotbugs` + `--java`(같은 JDK의 javac) | 아니오 | `codeye-java-spotbugs` v1 | `overridden` / `honored` |
| `any.lizard` | `lizard` | 아니오 | `--lizard` | 아니오 | `codeye-structure-lizard` v2 | `overridden` / `honored` |
| `any.jscpd` | `jscpd` | 아니오 | `--jscpd` + `--node` | 아니오 | `codeye-duplication-jscpd` v2 | `overridden` / `honored` |
| `any.gitleaks` | `gitleaks` | 아니오 | `--gitleaks` | 아니오 | `codeye-secrets-gitleaks` v1 | `honored` / `disabled` |
| `any.opengrep` | `opengrep` | 아니오 | `--opengrep` | 아니오 | `codeye-taint` v2 | `overridden` / `reported` |

go vet의 규칙 팩은 관찰 문서에 들어 있지 않고 보고서 decoder와 비교가 합성한다([규칙 문맥과 소스 안 억제](#규칙-문맥과-소스-안-억제)). 모듈 단위 분석기는 루트 모듈의 key에 이름만 쓰고 나머지 모듈은 경로 정렬 순서로 `.m01`부터 붙인다. 모듈이 64개를 넘으면 계획 오류다. 무엇이 모듈인지는 팩별 절이 정한다. C/C++과 `any` 분석기는 작업 하나다.

## 도구 설정

### 에이전트 플래그

도구 경로는 노드 운영자만 정한다. run 요청, 프로젝트 프로필, 스냅샷, 대상 저장소 설정은 도구·허가를 바꾸지 못한다.

| 플래그 | 값 | 쓰는 분석기 |
|---|---|---|
| `--go /abs/go` | Go toolchain의 `go` | go vet, Staticcheck(`go list`), 같은 경로에 설치된 gofmt, env.discover |
| `--go-mod-cache DIR` | 운영자가 준비한 오프라인 모듈 캐시(읽기 전용 보호 경로). 없으면 빈 scratch 캐시 | go vet, Staticcheck |
| `--node /abs/node` | 공유 node toolchain | pyright, ESLint, tsc, jscpd, env.discover |
| `--staticcheck /abs/staticcheck` | 실행 파일 | Staticcheck |
| `--cppcheck /abs/cppcheck`, `--clang-tidy /abs/clang-tidy`, `--gcc /abs/gcc` | 실행 파일(symlink는 해석한 실제 일반 파일을 검사) | cppcheck, clang-tidy, GCC C 파일 디스크립터 검사 |
| `--ruff /abs/ruff` | 단일 바이너리 | ruff |
| `--mypy /abs/mypy`, `--bandit /abs/bandit` | 운영자 venv의 스크립트. 첫 줄은 절대 경로 `#!` interpreter여야 하고 `#!/usr/bin/env`는 거부 | mypy, bandit |
| `--pyright /abs/node_modules/pyright/index.js` | JS 진입 스크립트 | pyright |
| `--python-both-type-checkers` | mypy와 pyright를 둘 다 실행하겠다는 명시 선택 | mypy, pyright |
| `--advertise-without-baseline` | 기준 분석기가 하나도 쓸 수 없어도 분석 capability를 광고. 기본은 광고하지 않음([광고 규칙](#빌트인-카탈로그와-capability-광고)) | 모든 분석기 |
| `--eslint /abs/node_modules/eslint/bin/eslint.js` | JS 진입 스크립트. 같은 도구 `node_modules`에 `@eslint/js`·`typescript-eslint`가 있어야 함 | ESLint 기준·type-aware |
| `--tsc /abs/node_modules/typescript/bin/tsc` | JS 진입 스크립트 | tsc |
| `--cargo /abs/<toolchain>/bin/cargo` | toolchain의 실제 cargo(rustup proxy 거부) | cargo check, `deps.rust` |
| `--java /abs/jdk/bin/java` | 운영자가 설치한 JDK의 Java 실행 파일 | Java 환경 관측, PMD, SpotBugs |
| `--pmd /abs/pmd/lib/pmd-cli-*.jar` | PMD 공식 배포의 CLI JAR. 같은 배포판의 lib와 conf를 함께 둔다 | PMD |
| `--spotbugs /abs/spotbugs/lib/spotbugs.jar` | SpotBugs 공식 배포의 JAR. 같은 lib 디렉터리의 의존 JAR도 함께 둔다 | SpotBugs |
| `--clippy /abs/<toolchain>/bin/cargo-clippy` | cargo와 같은 디렉터리의 `cargo-clippy` | clippy |
| `--lizard /abs/lizard` | 설치된 실행 파일 | lizard |
| `--jscpd /abs/node_modules/jscpd/run-jscpd.js` | JS 진입 스크립트(`node_modules/.bin` shim 거부) | jscpd |
| `--gitleaks /abs/gitleaks` | 실행 파일 | gitleaks |
| `--opengrep /abs/opengrep` | Opengrep 릴리스의 단일 실행 파일([설치](#opengrep-설치)) | 패턴 taint 규칙 |
| `--npm /abs/node_modules/npm/bin/npm-cli.js`, `--pnpm /abs/node_modules/pnpm/bin/pnpm.cjs`, `--yarn /abs/node_modules/yarn/bin/yarn.js` | JS 진입 스크립트. `--node`가 있어야 함. 분석기가 아니라 받는 도구다 | `deps.javascript` |
| `--toolchain-path PATH` | 절대 디렉터리 목록, 기본 `/usr/bin:/bin`. 분석기는 보조 프로그램 조회에만 쓴다(Rust 링커 `cc`) | cargo check, clippy |
| `--repo-code-source-root ID` (반복) | 저장소 코드 실행 허가. 같은 agent의 `--source-root ID=...`가 있어야 함 | 저장소 코드를 실행하는 작업 |
| `--dependency-fetch` | 의존성 받기 운영자 스위치. 기본 꺼짐, `codeye up`의 에이전트는 켬. 프로젝트 선택 `fetch_dependencies`도 켜져 있어야 함([의존성 준비](ARCHITECTURE.md#의존성-준비)) | `deps.*` |
| `--dependency-cache-dir DIR` | 받은 의존성의 공유 캐시. 기본 `<state-dir>/cache/deps`(`codeye up`은 `state_dir/cache/deps`를 준다) | `deps.*`, 받은 캐시를 읽는 분석기 |
| `--dependency-timeout D`, `--dependency-total-timeout D`, `--dependency-max-bytes N` | 모듈당 제한시간(10분), attempt 합계(20분), 내려받기 크기 상한(2 GiB) | `deps.*` |
| `--go-proxy`, `--go-sumdb`, `--go-private`, `--go-noproxy`, `--go-nosumdb` | Go 모듈 받기의 `GOPROXY`·`GOSUMDB`·`GOPRIVATE`·`GONOPROXY`·`GONOSUMDB`. 비우면 Go 기본값([Go 의존성 받기](#go-의존성-받기)) | `deps.go` |
| `--npm-registry URL` | npm·pnpm·yarn이 쓸 레지스트리. 인증정보 없는 http(s) URL만. 비우면 npm은 `https://registry.npmjs.org/`, yarn은 `https://registry.yarnpkg.com`([JS/TS 의존성 받기](#jsts-의존성-받기)) | `deps.javascript` |
| `--python /abs/python3` | pip을 가진(또는 uv가 설치할 대상인) Python interpreter. 3.11 이상이어야 `pyproject.toml`을 읽는다 | `deps.python`, 받은 패키지를 읽는 mypy |
| `--uv /abs/uv` | uv 실행 파일. 있으면 pip 대신 uv가 설치하고 `uv.lock`을 읽는다([Python 의존성 받기](#python-의존성-받기)) | `deps.python` |
| `--pip-index-url URL` | Python 패키지 index(`--index-url`). 비우면 설치 도구 기본값(PyPI). 인증정보가 든 URL은 거부 | `deps.python` |

- 도구 플래그는 분석기 카탈로그에서 자동으로 만든다(`builtin.ToolFlags()`, `builtin.ToolchainFlags()`). 플래그 이름은 Descriptor의 `Tool`이고 `--eslint` 하나가 두 분석기에 쓰인다.
- 모든 경로는 절대 경로다. PATH를 검색하지 않고, 내려받지 않으며, 다른 위치로 대체하지 않는다. 셸·`.cmd` shim은 받지 않는다.
- `--mypy`와 `--pyright`를 함께 주면서 `--python-both-type-checkers`가 없거나, 이 플래그만 있고 두 경로가 다 없으면 agent는 사용법 오류(exit 2)로 시작하지 않는다.
- agent는 값을 worker hello로 넘긴다. `go_executable`, `go_mod_cache`, `tool_path`, `analyzer_tools`(도구 이름 → 경로, 공유 toolchain은 예약 이름 `node`로 같은 맵에 들어감), 그리고 스위치가 켜졌을 때만 `dependencies`(캐시 root, 예산, Go 받기 설정, 받는 도구 경로 `package_managers`, `pip_index_url`)다. 이 빌드가 모르는 도구 이름은 `unknown_tool`로 보고하고 실행하지 않는다.

서비스 설정 파일(`config.json`)에서는 `agent.go`, `agent.go_mod_cache`, `agent.toolchain_path`, `agent.tools`(도구 플래그 이름 → 절대 경로, `node` 포함), `agent.python_both_type_checkers`, `agent.dependencies`(`enabled` 기본 켜짐, `cache_dir`, `go_proxy`, `go_sumdb`, `go_private`, `go_noproxy`, `go_nosumdb`, `npm_registry`, `pip_index_url`, `timeout_seconds`, `total_timeout_seconds`, `max_download_bytes`)가 위 플래그가 된다. 패키지 관리자 `agent.tools.npm`·`pnpm`·`yarn`·`uv`·`python`은 같은 이름의 플래그다. 저장소 코드 실행은 `codeye up`의 에이전트에서 프로젝트별 선택이다(관리자가 아닌 사람이 시작한 run은 관리자 스위치 안에서)(`source_roots[].repository_code`는 쓰이지 않는다, A40)([예시](../examples/config.json)). `agent.tools`에 모르는 이름이 있으면 설정 오류다.

### 도구 찾기

`codeye up`·`start`는 도구 경로를 손으로 적지 않아도 된다. 시작할 때마다 `internal/toolfind`가 이 머신의 도구를 찾고, 위 플래그가 될 값을 정한다. `codeye tools`가 같은 탐색을 하고 표로 보여 준다([운영](OPERATIONS.md#codeye-tools)). 서버·노드 에이전트를 직접 띄울 때는 탐색이 없고 위 플래그만 쓴다.

로컬 워커가 사용할 Python, JDK, Go, Cargo, Node.js는 관리 화면에서 지정할 수 있다. 실행 파일이나 설치 디렉터리를 저장하면 다음 분석에서 그 도구를 사용한다. 분석기 자체의 실행 파일은 기존 설정 키로 지정한다. [언어 도구 경로](GUIDE.md#언어-도구-경로)

**찾는 대상.** go, git, node, java, OpenCode와 이 빌드의 분석기 도구 전부다(staticcheck, gitleaks, ruff, mypy, pyright, bandit, lizard, cppcheck, clang-tidy, gcc, eslint, tsc, jscpd, opengrep, cargo, clippy, pmd, spotbugs, import-linter, dependency-cruiser), 그리고 의존성을 받는 패키지 관리자 npm, pnpm, yarn, uv, python이다. 결과의 `name`은 에이전트 플래그 이름이며 `agent.tools.<name>`과 같다. go는 `agent.go`, git은 `agent.git`, OpenCode는 `agent.model.opencode`다.

**찾는 위치.** 순서대로 사용자의 `PATH`, `~/.local/bin`, `~/go/bin`, `/usr/local/go/bin`과 시스템 디렉터리, `~/.opencode/bin`, CODEYE 도구 접두사 `~/.local/share/codeye-tools/{bin,cpp/bin,go/bin}`이다. 모양이 정해진 도구는 다음처럼 찾는다.

| 도구 | 찾는 방법 |
|---|---|
| pyright, eslint, tsc, jscpd | node가 읽을 진입 스크립트 파일(`pyright/index.js`, `eslint/bin/eslint.js`, `typescript/bin/tsc`, `jscpd/run-jscpd.js`)을 `node_modules` 디렉터리에서 찾는다. 디렉터리는 `~/.local/share/codeye-tools/node/node_modules`, `~/.local/share/codeye-tools/node_modules`, 찾은 node의 `<prefix>/lib/node_modules`, `~/.npm-global/lib/node_modules`, 시스템 `node_modules`다. `PATH`의 npm shim은 가리키는 진입 스크립트로 풀어서 쓴다. shim 자체는 쓰지 않는다. eslint는 같은 `node_modules`에 `@eslint/js`·`typescript-eslint`·`typescript`가 있어야 한다. |
| npm, pnpm, yarn | 위 JS 도구와 같은 `node_modules` 디렉터리에서 `npm/bin/npm-cli.js`(node 설치에 함께 온다), `pnpm/bin/pnpm.cjs`, `yarn/bin/yarn.js`를 찾는다. corepack shim과 `.cmd`·셸 shim은 쓰지 않는다. 검증은 `<node> <script> --version`이다. |
| Java, PMD, SpotBugs | Java는 설치 설정 또는 PATH·JAVA_HOME의 실행 파일을 확인한다. PMD와 SpotBugs는 설정한 JAR 또는 PATH의 배포 launcher에서 찾은 같은 설치의 JAR를 검사한다. |
| cargo, clippy | `~/.rustup/settings.toml`의 `default_toolchain`, 이어서 나머지 toolchain의 `toolchains/<이름>/bin/cargo`. clippy는 선택한 cargo와 같은 디렉터리의 `cargo-clippy`만 쓴다. `~/.cargo/bin`의 rustup proxy는 [거부 규칙](#도구와-rustup-proxy-거부)대로 쓰지 않는다. |
| mypy, bandit, lizard | 첫 줄이 절대 경로 interpreter인 스크립트여야 한다. `#!/usr/bin/env`인 후보는 다음 후보로 넘어간다. |
| go | 실행 파일이 GOROOT의 `bin/go`여야 한다. |
| uv, python | 실행 파일 `uv`와 `python3`(Windows `python`)을 같은 위치에서 찾는다. python은 3.8 이상이어야 하고 `python -I -m pip --version`이 pip을 보여야 한다. pip이 없는 interpreter는 uv를 찾았을 때만 쓴다(uv가 그 interpreter에 설치한다). 시험한 계열은 uv 0.12, Python 3.11 이상이다. |

**검증.** 후보마다 에이전트가 시작할 때 하는 것과 같은 검증을 한다. 분석기를 만들고(모양 규칙) 버전 probe를 실행한다(`builtin.CheckTool`). 패키지 관리자는 분석기가 아니므로 받기 task가 실행하는 방식대로 검증한다(npm·pnpm·yarn은 node와 진입 스크립트, uv·python은 버전과 pip). 그래서 `found`인 도구는 에이전트가 받아들이는 도구다. git과 OpenCode는 `--version`만 실행한다. 한 도구의 후보가 여러 개면 버전이 시험한 계열(위 [확인한 도구 버전](#확인한-도구-버전))인 첫 후보를 쓰고, 없으면 쓸 수 있는 첫 후보를 쓴다.

| 상태 | 뜻 |
|---|---|
| `found` | 후보가 검증을 통과했다. 경로, 버전, 어디서 찾았는지(`source`), `version_tested`를 기록한다. 시험한 계열 밖이면 `version_tested=false`와 `notes=["tool_version_untested"]`를 남기고 그대로 쓴다. |
| `unusable` | 후보는 있으나 모두 검증에 실패했다(버전을 알아볼 수 없음, 모양 규칙 위반, 필요한 go·node·cargo 없음). `reason`과 `tried`에 이유가 남는다. |
| `not_found` | 후보가 없다. |

찾지 못한 도구와 쓸 수 없는 도구에는 `hint`(한 줄 추가 방법)가 붙는다. 예: ``cppcheck: not found - `micromamba install -p ~/.local/share/codeye-tools/cpp cppcheck` ... or set `agent.tools.cppcheck` ``.

**명시 설정이 우선한다.** `agent.go`, `agent.go_mod_cache`, `agent.tools.*`, `agent.model.opencode`에 적은 값은 탐색 결과로 바뀌지 않는다. 그 경로가 쓸 수 없으면 다른 후보로 바꾸지 않고 `unusable`(`source=config`)로 보이며 `codeye doctor`가 실패로 센다. `"agent": {"auto_detect": false}`이면 탐색하지 않고 명시 설정만 쓴다.

**함께 정하는 값.**

- **Go 모듈 캐시.** 찾은 go의 `go env GOMODCACHE`(사용자의 실제 환경 기준)가 실제 디렉터리이면 `--go-mod-cache`로 넘긴다. go vet은 `GOPROXY=off`로 실행하므로 의존성이 있는 모듈은 이 캐시가 있어야 분석된다. 캐시는 저장 영역 상한의 `cache` 영역에 센다([운영](OPERATIONS.md#저장-영역과-상한)). 명시한 `agent.go_mod_cache`가 있으면 그것이 우선한다. 의존성 받기를 켜면 의존성은 이 캐시가 아니라 받기 캐시에서 읽고, 이 캐시는 받기의 첫 출처가 된다([Go 의존성 받기](#go-의존성-받기)).
- **Go 받기 설정.** 같은 go로 `go env -json GOPROXY GOSUMDB GOPRIVATE GONOPROXY GONOSUMDB`를 읽어 Go 기본값과 다른 값만 `tools.json`의 `go_settings`에 남기고, 설정 파일이 비워 둔 `agent.dependencies`의 Go 값을 채운다. 의존성 받기가 꺼져 있으면 채우지 않는다.
- **npm 레지스트리.** 찾은 npm으로 사용자의 실제 환경에서 `npm config get registry`를 읽어, npm 기본값과 다르고 인증정보가 없으면 `tools.json`의 `npm_registry`에 남기고 설정 파일이 비워 둔 `agent.dependencies.npm_registry`를 채운다. 인증정보가 든 값은 버린다(공개 레지스트리로 받는다).
- **설정 probe는 홈에 쓰지 않는다.** 위 세 probe는 사용자의 설정을 읽어야 하므로 사용자 환경으로 실행하지만, 쓰는 위치는 탐색의 임시 probe 디렉터리로 옮기고 끝나면 지운다. npm은 명령마다 `~/.npm/_logs`에 debug log를, 캐시에 파일을 쓰고, go 명령은 `go env`에서도 사용자 설정 디렉터리에 telemetry counter를 쓴다(실측). 그래서 npm probe는 `HOME`·캐시(`npm_config_cache`)·로그 디렉터리를 probe 디렉터리에 두고 로그 파일을 끄며(`npm_config_logs_max=0`) 사용자 설정 파일은 `npm_config_userconfig`(사용자가 정했으면 그 값, 아니면 `~/.npmrc`)로 그대로 읽는다. go probe는 `HOME`을 그대로 두고(기본 GOPATH·모듈 캐시가 그 아래다) `XDG_CONFIG_HOME`을 telemetry를 끈 probe 디렉터리로, `GOENV`를 사용자의 go env 파일(사용자가 정했으면 그 값)로 준다. darwin·Windows는 go가 `XDG_CONFIG_HOME`을 읽지 않으므로 go probe의 환경은 바꾸지 않는다(TC-319).
- **Python 타입 검사기.** 결과 도구 집합에 mypy와 pyright가 모두 있으면 `--python-both-type-checkers`를 함께 준다([타입 검사기 선택](#타입-검사기-선택)). 하나만 있으면 주지 않는다. 에이전트의 검사는 그대로이며(둘 다 있으면서 플래그가 없거나 그 반대면 사용법 오류), `codeye up`은 값을 정할 때 그 규칙을 맞춘다.

결과는 `<state_dir>/tools.json`에 쓴다([schema](../schemas/tools.schema.json), [예시](../examples/tools.json)). 탐색 결과와 명시 설정을 합친 에이전트 인자는 `toolfind.Resolve`가 만든다. 명시 값이 있으면 그 값이, 없으면 `found` 도구의 경로가 쓰이며 쓸 수 없는 탐색 결과는 넘기지 않는다.

### 실행 제한값

| 분석기 | 실행당 제한시간 | stdout 상한 | stderr 상한 |
|---|---|---|---|
| go vet, Staticcheck | 15분 | 32 MiB | 4 MiB |
| gofmt | 작업 전체 2분 | 배치마다 32 KiB | 배치마다 64 KiB |
| cppcheck, clang-tidy | 30분 | 32 MiB | 32 MiB(cppcheck XML이 stderr로 나옴) |
| GCC | 단위당 45초, 작업 합계 5분 | 64 KiB | 2 MiB(JSON 진단) |
| ruff, mypy, pyright, bandit | 15분 | 32 MiB | 4 MiB |
| ESLint 기준·type-aware | 15분 | 64 MiB | 4 MiB |
| tsc | 15분 | 32 MiB | 4 MiB |
| cargo check, clippy | 30분 | 64 MiB | 4 MiB |
| lizard, jscpd, gitleaks | 15분 | 32 MiB | 4 MiB |
| Opengrep | scan당 15분(규칙 하나·파일 하나당 30초) | 32 MiB | 4 MiB |
| 시작 probe·탐색 probe·toolchain probe | 30초 | 64 KiB | |

### 확인한 도구 버전

실제 도구 실행은 Linux x86-64에서만 확인했다. Windows는 단위 시험과 캡처 재생 시험만, macOS는 실행하지 않았다. CI에는 분석 도구가 없으며, 캡처한 실제 출력을 재생하는 시험이 같은 판정 경로를 검사한다([시험](TESTING.md)).

| 도구 | 확인한 버전 | `version_tested=true`인 범위 |
|---|---|---|
| go | go1.27.4 | `go1.27` |
| staticcheck | 2026.2.2 (0.8.2) | `2026.2` |
| cppcheck | 2.22.0 | `2.22` |
| clang-tidy | LLVM 23.1.5 | `23.1`(`23.1.5git` 같은 개발 빌드는 미인식) |
| GCC | Linux GCC 13.4.0, 15.2.0 | `13.4`, `15.2` |
| ruff | 0.16.3 | `0.16` |
| mypy | 2.4.1 | `2.4` |
| pyright | 1.1.414 | `1.1.414`만 |
| bandit | 1.9.4 | `1.9` |
| node | v24.18.0 | 버전 계열 검사 없음(형식만 검사) |
| eslint | 10.15.0 (@eslint/js 10.0.1, typescript-eslint 8.73.0) | `10.15`. plugin은 `plugin_version_untested`로 따로 표시 |
| typescript(tsc) | 6.0.3 | `6.0` |
| cargo, rustc | 1.99.1 | `1.99` |
| clippy | 0.1.99 | `0.1.99` |
| dependency-cruiser | 18.8.0 | `18.8` |
| Import Linter | 2.17 | `2.17` |
| PMD | 7.29.0 | `7.29` |
| SpotBugs | 4.10.5 | `4.10` |
| lizard | 1.26.1 | `1.26` |
| jscpd | 5.4.3 | `5.4` |
| gitleaks | 8.30.1 | `8.30` |
| Opengrep | 2.0.0 | `2.0` |

형식이 맞지만 범위 밖인 버전은 실행하고 `tool_version_untested`(rustc는 `rustc_version_untested`)와 `version_tested=false`를 남긴다. 버전 출력을 알아보지 못하면 도구를 코드에 실행하지 않는다(`tool_version_unrecognized`).

### 실제 도구 시험과 출력 캡처

실제 도구 시험(`TestReal*`)은 도구 경로 변수가 있을 때만 실행하고 없으면 건너뛴다. 같은 실행에 캡처 변수를 주면 고정 버전의 stdout·stderr·종료 코드(Rust는 dep-info도)를 패키지의 `testdata/`에 다시 쓰고, CI의 재생 시험(`TestReplay*`)이 그 바이트로 같은 판정 경로를 검사한다. 덤프 변수는 `examples/observations-*.json`을 만든 envelope를 꺼낸다. **fixture나 parser가 바라는 도구 버전을 바꾸면 다시 캡처한다.** 캡처는 노드 경로를 표지(`@MODULE@`, `@HOME@`, `$HOME`, `$SCRATCH`, `$TOOLCHAIN`)로 바꿔 저장하고, 노드 경로나 생성 비밀값이 든 출력은 쓰지 않는다. 변수 이름은 코드의 식별자 그대로다.

| 대상 | 도구 경로 변수 | 캡처·덤프 변수 | 시험 선택 |
|---|---|---|---|
| go vet | `CODEYE_TEST_GO`(없으면 테스트 바이너리의 `GOROOT/bin/go`) | `CODEYE_GOVET_TARGET`·`CODEYE_GOVET_OUT`·`CODEYE_GOVET_MODCACHE`·`CODEYE_GOVET_DUMP` | `./internal/worker/analysis/govet/` |
| gofmt | 테스트 바이너리의 `GOROOT/bin/gofmt` | `CODEYE_TEST_DUMP_GOFMT` | `-run TestReadOnlyGoFmtCoverage ./internal/worker/analysis/gofmtcheck/` |
| Staticcheck | `CODEYE_TEST_STATICCHECK`, 선택 `CODEYE_TEST_GOMODCACHE` | `CODEYE_STATICCHECK_CAPTURE`, `CODEYE_STATICCHECK_DUMP`, `CODEYE_STATICCHECK_REPORT_DUMP` | `-run TestRealStaticcheck ./internal/worker/analysis/staticcheck/` |
| C/C++ | `CODEYE_TEST_CPPCHECK`, `CODEYE_TEST_CLANG_TIDY` | `CODEYE_CPP_CAPTURE`, `CODEYE_CPP_DUMP`, `CODEYE_CPP_PRINT`(기대값 비교 대신 실제 결과 기록) | `-run TestRealFixtureCases ./internal/worker/analysis/cpp/`, 기대값 `fixtures/cpp/expected.json` |
| GCC C | `CODEYE_TEST_GCC` | `CODEYE_GCC_EXAMPLE_OUTPUT`(관찰 예시 기록) | `-run TestRealGCCAnalyzerFileDescriptorFlow ./internal/worker/analysis/cpp/` |
| Python | `CODEYE_TEST_RUFF`, `CODEYE_TEST_MYPY`, `CODEYE_TEST_BANDIT`, `CODEYE_TEST_PYRIGHT` + `CODEYE_TEST_NODE` | `CODEYE_PYTHON_CAPTURE`, `CODEYE_PYTHON_DUMP` | `-run TestReal ./internal/worker/analysis/python/`. bandit CSV는 CRLF이므로 `testdata/.gitattributes`가 줄바꿈 변환을 끈다 |
| JS/TS | `CODEYE_TEST_NODE`, `CODEYE_TEST_ESLINT`, `CODEYE_TEST_TSC` | `CODEYE_JSTS_CAPTURE`, `CODEYE_JSTS_DUMP` | `-run 'TestRealESLint\|TestRealEngine' ./internal/worker/analysis/eslint/`, `-run TestRealTsc ./internal/worker/analysis/tsc/` |
| Rust | `CODEYE_TEST_CARGO`, `CODEYE_TEST_CLIPPY`, 링커 시험은 `CODEYE_TEST_TOOLPATH`(`cc` 포함) | `CODEYE_RUST_CAPTURE`, `CODEYE_RUST_DUMP` | `./internal/worker/analysis/rustpack/` |
| lizard, jscpd, gitleaks | `CODEYE_TEST_LIZARD`, `CODEYE_TEST_JSCPD` + `CODEYE_TEST_NODE`, `CODEYE_TEST_GITLEAKS` | `CODEYE_STRUCTURE_CAPTURE`, `CODEYE_STRUCTURE_DUMP` | `-run TestReal ./internal/worker/analysis/{lizard,jscpd,gitleaks}/` |
| Opengrep | `CODEYE_TEST_OPENGREP`, 네트워크 추적은 `CODEYE_TEST_STRACE` | `CODEYE_OPENGREP_CAPTURE`(`testdata/fixtures.*`), `CODEYE_OPENGREP_EXAMPLE`(관찰 예시 기록) | `-run TestRealOpengrep ./internal/worker/analysis/opengrep/`, 기대값은 `fixtures/taint/`의 `expect:` 표지 |

시험 대상 소스는 저장소의 gofmt·vet·분석기가 보지 않도록 시험 코드 문자열이나 `fixtures/` 아래에 둔다. 비밀값 fixture는 시험 시점에 생성하고 저장소에는 실제와 비슷한 토큰을 두지 않는다. 시험 전체 구성은 [시험](TESTING.md)을 따른다.

## 어댑터 계약

### 실행 규칙

- 프로세스는 executor `Runner`로만 시작한다. 분석 패키지에서 `os/exec`, `net`, `net/http`, `syscall` import는 시험으로 금지한다. 어댑터는 정리 결과를 주장하지 않고 worker가 기록한 실행 결과로 합산한다.
- 실행은 argv 배열, cwd, 명시 환경변수, 제한시간, 출력 상한으로 정한다. 사용자 값과 셸 문자열을 이어 붙이지 않는다. 환경은 상속하지 않는다. 공통값은 `PATH`(해석한 도구 또는 interpreter 디렉터리 하나), `LANG=C.UTF-8`, Windows의 `SystemRoot`이고, executor가 실행별 `HOME`·임시 디렉터리를 더한다.
- 원본 사용자 경로와 스냅샷에서 실행하지 않는다. cwd는 attempt 작업 사본(또는 scratch의 빈 디렉터리)이고 스냅샷 디렉터리와 운영자 캐시는 보호 경로다. 캐시·설정·보고서 파일은 task scratch `analyzers/<key>/`(go vet은 `go-vet/<key>/`)에 둔다.
- 실행 전에 도구가 읽는 manifest 파일(`go.mod`, `Cargo.toml` 등)을 manifest SHA-256과 대조한다. 실행 뒤 작업 사본이 manifest와 다르면 `workspace_drift`로 실패하고 그 파일의 관찰은 위치를 연결하지 않는다.
- 한 attempt의 작업은 작업 사본 하나를 차례로 쓴다. 워커는 작업이 끝날 때마다 사본을 스냅샷 내용으로 되돌린다. 바뀐 manifest 파일(크기·수정 시각·권한, Linux에서는 inode·ctime이 다르면 내용 hash로 확인)은 스냅샷에서 다시 쓰고, manifest에 없는 파일·디렉터리는 지운다. manifest에 없는 데이터 이름(숨김 이름·`node_modules`·`vendor`·`testdata`, 분석기가 소스로 보지 않는 이름)은 남기고 그 안을 읽지 않는다. 준비한 의존성이 그곳에 있다. 그 작업의 결과에는 `workspace_restored_after_task`·`workspace_leftovers_removed`가 남고, 되돌리지 못하면 `workspace_restore_failed`가 남는다. 그래서 한 작업이 남긴 파일(build script가 고쳐 쓴 소스, 생성물)이 뒤 작업의 `workspace_drift`가 되지 않는다. 자기 실행 중에 사본을 바꾼 작업은 여전히 `workspace_drift`로 실패한다.
- 저장소에서 온 위치 인자는 옵션으로 읽히지 않게 한다. `--` 뒤에 두거나, `./` 접두를 붙이거나, 절대 경로로 바꾸거나, scratch 목록 파일로만 넘긴다.
- 작업 사본 절대 경로는 executor 마스킹으로 기록 stream과 명령에서 `[REDACTED]`가 된다. 구조화 산출물에는 노드 절대 경로와 환경변수 값을 넣지 않는다. result manifest에는 실행 파일 base name만 남는다.
- 도구의 백그라운드 자식(데몬, telemetry, 언어 서버)은 팩마다 측정하고 끈다. executor는 setsid로 프로세스 그룹을 벗어난 자손을 추적하지 않으므로 정리 확인이 자손 전체를 보장하지 않는다.

### 분석기 서술 파일

`examples/analyzer-<tool>.yaml`은 도구별 실행 계약이다. 필수 필드는 `id`, `languages`, `planned_platforms`(`windows`·`linux`·`macos`), `availability`(`unverified`·`verified`·`unavailable`), `version_contract`, `analysis_scope`(`text`·`syntax`·`typed`·`intraprocedural`·`interprocedural`·`interfile`·`structural` 중 실제 활성 범위), `executes_repository_code`, `requires_build`, `network_policy`, `version_probe`, `execute`(argv·cwd·제한시간·출력 상한·env allowlist), `exit_codes`, `parser`다. `scripts/validate_bundle.py`가 서술의 argv·규칙 팩 상수를 Go 코드와 대조한다. schema를 바꾸면 서술·예시·검증 스크립트·시험을 함께 고친다.

### 종료 등급과 결과

envelope의 `outcome.exit_class`는 다음 중 하나다.

| exit class | 뜻 |
|---|---|
| `success_without_findings` | 도구가 완전히 실행됐고 억제되지 않은 관찰이 없다. 결함이 없다는 뜻이 아니다 |
| `success_with_findings` | 도구가 완전히 실행됐고 억제되지 않은 관찰이 있다 |
| `configuration_error` | 도구·toolchain·의존성·설정 문제(오프라인 의존 누락, 헤더 없음, 버전 미인식 등) |
| `execution_error` | 도구 실행 또는 출력 해석 실패 |
| `timeout`, `cancelled` | 제한시간 초과, 취소. 종료 코드와 별도로 기록 |
| `not_run` | 도구 실행을 시도하지 않음. 관찰 0, `processed_paths=[]` |

- 도구마다 실제 종료 코드를 측정해 매핑한다. 종료 코드만으로 결과를 정하지 않는 도구가 많다. 레코드 유무와 종료 코드가 어긋나면 `exit_status_mismatch`로 실패한다.
- 성공은 완전한 parse, 도구 오류·drift 없음, 실행 1개 이상, 모든 실행의 정상 종료·정리 확인·잘림 없음, 비어 있지 않은 `processed_paths`일 때만이다.
- 출력이 잘리면 `output_truncated`, 해석하지 못하면 `output_unparsed`다. 일부를 읽었으면 `partial_parse=true`로 두고 이미 읽은 관찰은 보존한다. **parse 실패를 빈 결과로 바꾸지 않는다.** 알 수 없는 필드·빠진 필드·잘못된 타입은 형태 오류다.
- 정리 불확실은 `failed`/`cleanup_uncertain`, 취소는 `cancelled`, 시간 초과는 `timed_out`이다. 성공으로 바꾸지 않는다.
- 실패한 작업은 처리 파일을 주장하지 않는다(`processed_paths=null`).
- 타입·의존 오류처럼 분석하지 못한 단위는 관찰이 아니라 `tool_errors`다. 성공 envelope에는 tool error가 없어야 한다.
- 파일마다 따로 해석하는 도구(Python 네 도구, ESLint)는 구문 분석하지 못한 파일을 빼고 나머지를 분석한다. 그 파일은 처리 파일에 없고(`syntax_error`·`parse_error` 단위) 제약 `unparsable_files_not_analyzed`가 남으며, 도구가 위치를 주면 구문 오류가 그 파일의 관찰 하나가 된다. 해석할 수 있는 파일이 없을 때만 실패다. 프로그램·번역 단위·패키지 전체를 함께 검사하는 도구(tsc, C/C++, go vet·Staticcheck, cargo)의 단위 오류는 그대로 실패다.

### 관찰과 위치

- 관찰은 `rule_id`, 원문 `raw_severity`·`raw_message`, 위치, `related[]`, `raw_ref`, 단위, `suggested_fix_count`, `suppressed`, 선택 `metric`을 갖는다. 원시 진단은 LLM이 수정하지 않는다. 메시지는 경로만 상대화하거나 표지(`$GOROOT` 등)로 바꾼다.
- `raw_ref`는 실행 이름, stream, stream SHA-256, 레코드 순번, JSON pointer, byte 범위로 기록된 원시 출력에 묶인다.
- 위치 상태는 `resolved`, `unresolved_no_position`, `unresolved_outside_snapshot`, `unresolved_missing_file`, `unresolved_line_out_of_range`, `source_drift`다. `resolved`만 스냅샷 상대 경로·1부터의 줄·열·manifest `blob_sha256`을 갖고 코드 근거가 될 수 있다. 줄 범위는 manifest hash와 일치하는 스냅샷 파일로 검사한다.
- 열은 스냅샷 바이트로 byte 열(1부터)로 바꾼다. 바꾸지 못하면 `column_unit`에 `utf16` 또는 `rune`과 원값을 둔다.
- 관찰 ID는 `obs_` + SHA-256(분석기, 모듈, 단위, 규칙, 마스킹한 위치, 메시지, 같은 튜플 안의 순번) 앞 20자다. 도구 출력 순서와 무관하다.
- 범주(`category`)는 검사의 주제이며 영향 판정이 아니다. 값은 `api_contract`, `concurrency`, `correctness`, `data_integrity`, `error_handling`, `maintainability`, `performance`, `resource_lifecycle`, `security`, `test_gap` 중 하나다.
- 도구가 작업 사본 밖 파일의 진단을 보고하면 팩이 정한 방식으로 그 메시지를 저장하지 않는다(팩별 절).

### 범위 정보

`coverage`는 `unit`(`crate`·`file`·`module`·`package`·`project`·`translation_unit`), `files_in_scope`(null=셀 수 없음), `processed_paths`(null=모름, []=0), `files_skipped`, `files_unlisted`, 단위 상태별 수를 담는다. 도구가 주지 않는 상세 범위는 모름으로 남긴다.

### SARIF 입력 어댑터

`internal/worker/analysis/sarif`는 팩이 실행한 도구의 SARIF 2.1.0 출력을 공통 관찰 모델로 옮기는 입력 어댑터다. 팩 안에서 도구 출력을 읽는 데만 쓰며, 다른 곳에서 만든 SARIF를 올려 결과로 받는 경로는 없다. 지금 쓰는 팩은 [Opengrep 패턴 taint 팩](#opengrep-패턴taint-팩)이다.

- **원시 stream이 근거다.** SARIF는 도구의 stdout으로 받아 그대로 기록한다. 결과마다 `/runs/i/results/j` JSON pointer와 그 결과의 byte 범위를, 위치·관련 위치·흐름 단계마다 자기 byte 범위를 기록하므로 관찰의 `raw_ref`와 관련 위치의 `raw_span`이 원시 파일의 바이트를 가리킨다.
- **옮기는 것.** `results`(규칙 ID, level, 메시지, 주 위치), `locations`, `relatedLocations`, `codeFlows/threadFlows/locations`(흐름·스레드·순서·`kinds`·`nestingLevel`을 보존하고 도구가 만든 흐름 단계로 표시), `partialFingerprints`·`fingerprints`, `suppressions`(종류), driver의 규칙 메타데이터(ID, 짧은·긴 설명, 기본 level, `CWE-<n>` 태그), 실행 알림(`toolExecutionNotifications`)과 `executionSuccessful`이다.
- **엄격한 검증.** 모델링한 부분집합 밖의 필드, 중복 키, 64단계를 넘는 중첩, 뒤에 붙은 데이터, 크기 상한(32 MiB)·결과 수 상한(100000)·결과당 흐름 단계 상한(4096) 초과, `kind`가 `fail`이 아닌 결과는 모두 형태 오류(`sarif.ErrShape`)다. 형태 오류는 결과 0건이 아니라 `output_unparsed`다.
  - 키는 정확히 일치해야 한다. 모델링한 멤버와 대소문자만 다른 키(`Results`, `startline`)는 모르는 필드로 넘기지 않고 형태 오류다. JSON 디코더가 대소문자를 무시하고 같은 멤버로 읽는 것을 막는다. 이름이 자유로운 객체(`properties`, `fingerprints`, `partialFingerprints`, `originalUriBaseIds`)만 예외다.
  - 위치에 `region`의 시작 줄이 없으면 형태 오류다. 0–0 범위의 위치로 만들지 않는다.
  - 쓰지 않는 문구는 내용을 검사하지 않는다. 코드 조각(`snippet`), 알림·위치·관련 위치·흐름 단계 메시지, 규칙의 `help`·`helpUri`, 억제의 `justification`이 그렇다. 실제 C 프로젝트(civetweb)의 알림 메시지에 제어 문자가 있어 스캔 전체가 `output_unparsed`가 됐다. 관찰에 옮기는 문구(결과 메시지, 규칙 설명)는 그대로 검사한다.
- **실행 상태.** run의 `invocations[].executionSuccessful`이 false면 팩은 결과를 쓰지 않고 실패로 본다. 반대로 true라고 해서 모든 파일을 해석했다는 뜻은 아니다. Opengrep 2.0.0은 문법 오류가 있어도 true를 쓴다(측정). 파일별 완전성은 팩이 따로 판정한다.
- **열 단위.** `Options.ColumnKind`는 도구가 쓰는 열 단위다(`bytes`·`utf16CodeUnits`·`unicodeCodePoints`). run의 `columnKind`가 있고 이 값과 다르면 형태 오류다. 없으면 팩이 정한 값을 쓴다. Opengrep은 `columnKind`를 쓰지 않고 열을 바이트로 센다(측정: 한국어 두 글자와 이모지 뒤의 호출이 바이트 열 28, 코드 포인트 열 21, UTF-16 열 22에서 바이트 열 28로 나왔다). 관찰의 `column_unit`은 `byte`다.
- **경로.** `artifactLocation.uri`는 작업 사본 상대 경로로만 받는다. 절대 경로, 스킴(`file:` 포함), `..`, 역슬래시, 드라이브 문자(`C:`, `C:/…`), `./` 뒤의 절대 경로(`.//etc/passwd`), 모르는 `uriBaseId`는 작업 사본을 벗어난 위치로 표시되고, 팩은 그 결과를 버리고 작업을 실패시킨다(`result_outside_snapshot`). `a:b.py`처럼 두 번째 글자가 콜론이지만 드라이브가 아닌 이름은 POSIX 상대 이름으로 받는다. URI 형식(퍼센트 인코딩 여부)과 허용 `uriBaseId`는 팩이 도구의 실측 출력대로 고른다.
  - 허용한 `uriBaseId`를 run의 `originalUriBaseIds`가 정의하면 형태 오류다. 기준을 작업 사본 밖으로 옮길 수 있기 때문이다. 다른 ID의 정의는 쓰지 않으므로 받는다. Opengrep 출력에는 이 멤버가 없다(측정).
  - `sarif.Maps(path, opt)`는 작업 사본 경로가 도구가 쓸 URI로 갔다가 같은 경로로 돌아오는지 알려준다. 팩은 돌아오지 않는 파일을 도구에 넘기지 않는다(Opengrep은 `skipped_unmappable_name`으로 센다). 스냅샷이 역슬래시·콜론 이름을 이미 받지 않으므로(`source_unsupported`) 지금 이 경로는 방어선이다.
- 관련 위치의 종류 `tool_flow_step`은 도구가 보고한 데이터 흐름 단계다. 순서는 원시 stream 순서이며 보고서까지 그대로 간다([보고서](REPORT.md)).

### 설계 계약(미구현)

다음은 설계만 있고 구현하지 않았다.

- 승인된 digest·버전을 가진 외부 플러그인 카탈로그. 임의 다운로드 플러그인은 지원하지 않는다.
- 사용자가 규칙을 끈 이유·범위·기간 기록과 프로젝트별 규칙 정책 revision.
- 여러 검사기의 진단을 rule family·symbol·문맥으로 묶는 자동 병합. 현재는 도구 사이 자동 병합을 하지 않는다([엔진 중복](#엔진-중복)).

## 분석기 팩 계약

### Analyzer와 Descriptor

팩은 도구 하나마다 `pack.Analyzer`를 구현한다(`internal/worker/analysis/pack`).

```go
type Descriptor struct {
    ID           string   // "<ecosystem>.<tool>", 하이픈 없음: "go.vet", "cpp.clang_tidy"
    Name         string   // task key 소유 이름이자 scratch 디렉터리: "go-vet", "clang-tidy"
    Ecosystem    string   // any | cpp | go | java | javascript | python | rust
    Required     bool     // 기준 분석기 여부
    Tool         string   // 운영자 도구 이름 = 에이전트 플래그 이름
    VersionArgs  []string // 버전 출력 argv(실행 파일 다음)
    ParseVersion func(stdout []byte) (string, bool)
    Toolchains   []string // 필요한 공유 toolchain, 정렬·중복 없음, go와 자기 도구 이름 제외
    Interpreter  string   // Toolchains 중 도구 스크립트를 실행하는 것("node") 또는 ""
}

type Analyzer interface {
    Descriptor() Descriptor
    Plan(ctx, job) ([]pack.Task, error)             // manifest와 scope만 읽는다. 도구 불필요
    Run(ctx, job, task, runner) analysis.TaskResult  // 프로세스는 runner로만 시작
}
```

- `Plan`은 도구 없이 결정적이어야 한다. 도구가 없는 노드도 같은 작업을 계획해 공백을 보인다. 생태계의 모듈이 범위에 없으면 계획하지 않는다. `pack.Task`는 `TaskSpec`, 모듈 디렉터리, 도구 범위 인자, `FilesInScope`(셀 수 없으면 nil), `RepositoryCode`를 갖는다.
- `Run`은 envelope를 `obsdoc.ArtifactPath(scratch, key)` = `<scratch>/analyzers/<key>/observations.json`에 `pack.WriteEnvelope`로 쓴다. 이 함수는 문서를 정렬·검증하고 파일을 배타 생성한다. go vet만 자기 v0.1 문서를 자기 경로에 쓴다.

### Composite

`pack.Composite`는 분석기 목록을 deep-review capability(`analysis:deep-review`)의 handler 하나로 만든다. handler 이름은 `analyzers`, 버전은 `1`이다.

- 계획은 멤버 순서대로 이어 붙인다. 단계는 모두 `static_analysis`다.
- task key는 정확히 한 분석기가 소유한다. `key == Name`이거나 `Name + "."`로 시작해야 한다. 접두어만 같은 key(`go-vetx`)는 소유하지 않는다.
- 생성 시 ID 중복, 이름 겹침, 예약 이름(`review*`, `env.discover`·`docs.discover`, `pack-gap`)을 거부한다.
- 계획 시 소유하지 않은 key, 중복 key, Descriptor와 다른 `Required`, `static_analysis`가 아닌 단계, 잘못된 모듈 경로는 오류다.
- `Run`은 key의 소유자에게만 보낸다. 소유자가 없거나 소유 분석기의 계획에 없는 key는 `failed`(`handler_error`, `unknown_task`)다.

### 빌트인 카탈로그와 capability 광고

`builtin.catalog()`가 이 빌드의 분석기 목록이다. 팩은 자기 항목(`{desc, plan, build}`)만 추가한다. `build`는 노드 설정으로 분석기를 만들고 시작 probe를 실행한다. 실패하면 이유를 돌려주고 카탈로그는 그 자리에 `NotConfigured` 대역을 넣는다. 에이전트 플래그와 탐색 probe 목록은 카탈로그에서 자동으로 만든다.

deep-review capability는 시작 probe에 성공한 기준 분석기가 하나 이상 있을 때만 광고한다. 추가 깊이 도구만 있는 노드는 광고하지 않는다. 노드 운영자가 `--advertise-without-baseline`을 주면 기준 분석기가 하나도 없어도 광고한다(`codeye up`이 켠다). 쓸 수 없는 도구의 작업은 [설정되지 않은 도구](#설정되지-않은-도구)대로 `unsupported`로 기록되고, run은 도구를 기다리며 대기열에 머무르는 대신 빈틈이 보이는 부분 보고서로 끝난다. 예를 들어 `--cargo`만, `--ruff`만, `--cppcheck`만, `--node`+`--eslint`만 있는 노드도 광고한다. capability는 노드 단위이고 생태계별 배정은 하지 않는다. 그래서 한 생태계 도구만 있는 노드가 다른 언어 스냅샷을 받으면 그 언어의 작업은 `tool_not_configured`로 보인다.

### 도구 경로

- 도구가 언어 toolchain을 써야 하면 그 toolchain 설정을 공유한다(Staticcheck는 `--go`의 `go list`를 쓴다).
- 도구가 실행 중 보조 프로그램을 PATH에서 찾아야 하면, 분석기 생성 시 **한 번** `--toolchain-path`(`builtin.Config.ToolPath`, hello `tool_path`)의 절대 디렉터리에서 찾아 그 절대 경로를 도구 고유 설정으로 준다(Rust 링커 `cc`). 찾지 못하면 그 프로그램이 필요한 작업만 구성 오류로 끝나고, 사용했으면 제약을 남긴다. 보조 프로그램의 버전은 식별하지 않는다.
- 저장소가 정한 build 코드가 프로그램 이름으로 시스템 도구를 찾는 작업은 절대 경로를 하나씩 넘길 수 없다(`cc` crate의 build script가 `cc`·`ar`를, CMake configure가 컴파일러를 찾는다). 이 작업은 [저장소 코드 실행 허가](#저장소-코드-실행-허가)를 받은 작업에 한해 자식 PATH를 toolchain 디렉터리 뒤에 `--toolchain-path`의 절대 디렉터리로 넓힌다. 사용처는 [Rust 환경](#rust-환경)의 cargo check·clippy·rustdoc과 [compile DB 생성](#compile-db-생성)이다. 넓혔으면 Rust 작업은 `build_scripts_used_toolchain_path`를 남긴다. 의존성 받기(`cargo fetch`)는 build script를 실행하지 않으므로 toolchain 디렉터리 하나만 쓴다.
- `codeye tools`와 `tools.json`의 `toolchain_path`는 이 경로에서 `cmake`·`make`·`ninja`·`cc`·`c++`·`ar`이 있는지만 확인해 보여 준다(실행하지 않는다). `codeye doctor`의 `build_tools` 검사는 compile DB 생성에 필요한 cmake, make 또는 ninja, C/C++ 컴파일러가 모두 있으면 `available`이고, 없으면 막지 않는 `unavailable`(`build_tools_missing`)이다.

### 공유 interpreter toolchain

여러 도구가 같은 interpreter를 쓰므로 toolchain 표(`builtin/toolchain.go`의 `toolchainTable()`)를 하나 둔다. 팩은 필요를 선언만 한다.

| 이름 | 에이전트 플래그 | 버전 argv | 버전 parser |
|---|---|---|---|
| `node` | `--node /abs/node` | `--version` | 한 줄 `v<major>.<minor>.<patch>`(각 1~4자리) → `<major>.<minor>.<patch>`. pre-release·nightly·여러 줄은 거부 |

- `go`는 표에 넣지 않고 `--go`·`GoExecutable`을 그대로 쓴다. python toolchain은 없다. mypy·bandit은 운영자 venv의 절대 `#!` interpreter로 실행한다.
- toolchain 이름은 예약어다. 분석기 도구 이름으로 쓸 수 없고 env.discover 런타임 도구 이름(`go`·`node`·`python`·`rust` 등)이어야 한다.
- **검증**: 워커 시작 시 toolchain마다 한 번 한다. 절대 경로의 실행 가능한 일반 파일인지 확인한 뒤 `<node> --version`을 새 probe 디렉터리, `PATH=<해석한 실행 파일 디렉터리>`, `LANG=C.UTF-8`(Windows는 `SystemRoot` 추가), 30초, 64 KiB로 실행한다. 정리 불확실, 0이 아닌 종료, 잘린 출력, 미인식 버전은 실패다. 성공하면 핸들 `pack.Toolchain{Name, Executable, Dir, Version}`이 된다.
- **선언과 build**: builtin은 `Descriptor.Toolchains`가 모두 설정되고 probe에 성공했을 때만 build 함수를 호출하고 검증된 핸들 map을 넘긴다. build 함수는 hello 경로를 다시 읽지 않는다.
- **실행**: `Interpreter`가 있으면 argv는 `<핸들.Executable> <도구 경로> <args>`이고 자식 `PATH`는 `핸들.Dir` 하나다. 도구 스크립트의 `#!` 줄을 쓰지 않는다. 도구 경로는 interpreter가 읽을 진입 스크립트여야 한다.
- **사유 우선순위**(build 전에 builtin이 정함): (1) 도구 경로 없음 `tool_not_configured`(누락 코드 `<분석기>:not_configured`), (2) 필요한 toolchain 미설정 `toolchain_not_configured`(`<분석기>:toolchain_not_configured`), (3) toolchain 경로가 잘못됐거나 probe 실패 `tool_unavailable`(`<분석기>:toolchain_unavailable`, toolchain 행 `node:invalid_config`·`node:probe_failed`도 남음), (4) build 함수 자신의 검증·시작 probe. 설정하지 않은 toolchain은 따로 누락 행을 만들지 않는다.

### 설정되지 않은 도구

도구 경로가 없거나, 검증·시작 probe에 실패하거나, 필요한 toolchain이 없으면 분석기는 `pack.NotConfigured` 대역이 된다. 대역은 실제 분석기와 **같은 작업을 계획**하고 프로세스 없이 끝낸다.

| `TaskResult.Reason` | 조건 |
|---|---|
| `tool_not_configured` | 운영자가 도구 경로를 주지 않음 |
| `tool_unavailable` | 경로가 있으나 검증·시작 probe 실패(실행 파일 아님, 버전 미인식, 거부된 shim·proxy 등), 또는 필요한 toolchain의 검증·probe 실패 |
| `toolchain_not_configured` | 필요한 toolchain이 설정되지 않음(Staticcheck에 `--go` 없음, node 도구에 `--node` 없음, clippy에 `--cargo` 없음) |

상태는 `unsupported`이며 성공·0건 통과로 세지 않는다. envelope는 `exit_class=not_run`, 실행 0, 관찰 0, `processed_paths=[]`, 어댑터·parser `codeye-not-run`이다. 보고서에는 not-run 도구 행이 남는다. 같은 단계의 다른 작업이 성공해도 `static_analysis` 단계는 `partial`이 되고 사유가 보인다.

### 기준 분석기와 추가 깊이

- 생태계마다 기준 분석기 하나가 `Required=true`이고 그 작업은 `required_for_verdict`다. 기준 분석기는 해당 언어 노드에서 가장 흔히 쓸 수 있고 build 없이 끝까지 실행되는 도구로 고른다(팩별 절에 근거).
- 추가 깊이 분석기의 실패·미지원은 보고서에 보이지만 판정 필수 작업 실패로 세지 않는다.
- 기준 분석기 작업이 성공하지 않으면 보고서는 `partial`(`required_task_not_succeeded`)이다.
- `Required`는 Descriptor·계획·envelope(`analyzer.required`)·DB task에서 같아야 한다. 보고서 decoder는 다르면 거부한다.

### 저장소 코드 실행 허가

정적 분석도 build script·proc macro처럼 저장소가 정한 코드를 실행할 수 있다. 노드 에이전트가 [실행 권한](EXECUTION.md#신뢰와-실행-권한)을 적용하고 워커에 결과를 `analysis.Job.AllowRepositoryCode`로 보낸다. 운영자가 `--repo-code-source-root ID`를 쓸 때는 같은 에이전트에 해당 `--source-root` 매핑이 있어야 한다. [결정 A23](DECISIONS.md)
- **선언**: `pack.Task.RepositoryCode`. `Plan`이 manifest와 scope만으로 정하므로 대역도 같은 선언을 계획한다. 한 도구가 두 방식을 함께 계획할 수 있다. `Run`은 `Plan`이 선언한 것보다 더 실행하지 않는다. false로 선언한 작업이 실행 중 저장소 코드 경로로 넘어가는 대체 경로는 금지다.
- **집행**: `Composite.Run`이 분석기의 계획에서 선언을 읽고 도구 상태보다 먼저 허가를 검사한다. 허가가 없으면 실행 없이 `unsupported`와 not-run envelope를 기록한다. 사유는 `repository_code_not_allowed` 또는 `repository_code_not_offered`이며 도구 상태는 제약으로 남긴다. 허가가 있어 프로세스를 실행하면 `repository_code_executed`를 기록한다.
- **필수 여부와 무관**: 허가가 없는 기준 분석기 작업은 필수 작업 미완료이며 보고서는 `partial`이다.

현재 분석기 팩 중 Rust의 cargo check와 clippy에 이 권한을 적용한다. C/C++ [compile DB 생성](#compile-db-생성) 작업과 기존 테스트 실행은 분석기 팩 밖의 별도 작업이며 같은 허가를 같은 방식으로 검사한다. 저장소 ESLint 설정 실행과 CMake 밖의 build 설명(Meson, autotools `configure`, `bear`로 감싼 make)으로 compile DB를 만드는 일은 후속 작업이다. [기존 테스트 실행](EXECUTION.md#기존-테스트-실행)

### 범위에 없는 생태계와 팩 공백

- **"범위에 있다"의 정의는 `pack.SourceFiles` 하나다.** 범위 안 포함 파일 중 소스 확장자를 센다. Go `.go`, Python `.py`·`.pyi`, JS/TS `.js`·`.jsx`·`.mjs`·`.cjs`·`.ts`·`.tsx`·`.mts`·`.cts`, Rust `.rs`, Java `.java`, C/C++ `.c`·`.cc`·`.cpp`·`.cxx`·`.c++`·`.h`·`.hh`·`.hpp`·`.hxx`·`.h++`다. 숨김 디렉터리, `testdata`, `vendor`, `node_modules` 아래 파일은 세지 않는다. build 파일(Makefile, package.json, pyproject.toml)만으로는 생태계가 범위에 들어오지 않는다. 확장자가 없는 스크립트는 세지 않는다.
- **범위에 모듈이 없는 생태계는 작업을 계획하지 않는다.** 다른 생태계의 분석기가 자리 작업을 만들어 실패를 만들지 않는다.
- **분석할 수 없는 소스는 그 생태계의 팩이 보인다.** 소스가 있는데 모듈을 만들 수 없으면 그 팩이 보이는 자리 작업을 계획한다(go vet `go_module_missing`, Python `no_analyzable_python_files`, Rust `cargo_manifest_missing`, C/C++ `no_translation_units` 등). composite는 이를 강제하지 않으므로 팩이 지켜야 한다.
- **범위의 생태계에 분석기가 하나도 없으면 공백 작업을 계획한다.** composite가 생태계마다 `pack-gap.<생태계>`를 만든다. 필수가 아니고 단계는 `static_analysis`다. 프로세스 없이 `unsupported`(`analyzer_pack_missing`)로 끝나고 not-run envelope(분석기 ID `<생태계>.none`, 도구 `none`, `files_in_scope`=소스 수)를 쓴다. 보고서는 `static_analysis_gap:<생태계>` 제약과 not-run 도구 행으로 `partial`이다. 공백 작업만 있으면 단계는 `skipped`다. 이 빌드에는 모든 언어 생태계에 팩이 있으므로 이 경로는 팩을 빼고 만든 카탈로그에서만 나타난다.
- 생태계의 멤버(설정된 분석기든 대역이든)가 하나라도 있으면 공백 작업을 만들지 않는다. `any` 생태계 분석기는 언어 공백을 메우지 않는다.
- **지원 언어 소스가 하나도 없으면** 보고서는 `static_analysis_not_planned` 제약과 함께 `partial`이다. lizard·jscpd는 지원 언어 소스가 범위에 있을 때만 계획된다. gitleaks는 모델 입력이 될 수 있는 파일이 범위에 있으면 계획되므로(아래 [구조·중복 지표와 비밀값 팩](#구조중복-지표와-비밀값-팩)), 문서·설정 파일만 있는 스냅샷에는 gitleaks 작업 하나만 있고 단계는 그 작업의 결과다. "정적 분석 계획됨" 판단은 비밀값 작업을 세지 않는다(`obsdoc.IsSecretTask`). 그런 파일도 없으면 단계는 `no_tasks`다.

### 관찰 envelope

role `observations`, media `application/vnd.codeye.observations+json`, `kind=analyzer_observations`, `schema_version=0.1`, canonical JSON. go vet 문서(`kind=go_vet_observations`)와 media·버전이 같고 kind로 구별한다. 노드 절대 경로와 환경변수 값을 담지 않는다. 형식은 [schema](../schemas/analyzer-observations.schema.json)가 정한다.

| 필드 | 내용 |
|---|---|
| `analyzer` | `id`, `ecosystem`, `required`, 선택 `purpose`([분석기 목적](#분석기-목적)) |
| `adapter`, `parser` | 구현 ID와 버전 |
| `task_key`, `snapshot_manifest_digest` | 소비자가 신뢰하는 task와 스냅샷에 묶음 |
| `module` | 스냅샷 상대 `dir`과 생태계 이름(`name`) |
| `patterns` | 도구 범위 인자 |
| `tool` | `name`, 관측한 `version`(없으면 null), `version_tested` |
| `target` | `platform`과 생태계별 `properties` |
| `rule_pack` | `id`·`version`·`selection`, `repository_config`(`overridden`/`honored`/`none`), `config_files`(경로·manifest SHA-256), `source_suppressions`(`honored`/`reported`/`disabled`/`none`), `unreported_suppression_rules` |
| `rules` | 선택. 도구가 읽었다고 보고한 규칙의 `id`, `short_description`, `cwe`(`CWE-<n>` 목록). ID 순으로 정렬·중복 없음이고 관찰의 모든 `rule_id`가 여기에 있어야 한다(Opengrep) |
| `outcome` | `task_status`, `reason`, `exit_class`, `partial_parse` |
| `executions` | 실행마다 이름, 상태, exit code, 정리 결과, 상대화한 argv·cwd·환경변수 이름, stdout/stderr의 SHA-256·크기·잘림 |
| `coverage` | [범위 정보](#범위-정보) |
| `observations` | [관찰과 위치](#관찰과-위치). 지표 관찰은 `metric{name, value, threshold, unit}` |
| `tool_errors` | 분석하지 못한 단위(타입 오류, 의존 누락, 로드 실패). 코드 관찰이 아님 |
| `parse` | `ok`, 레코드 수, 미해석 바이트, 형태·decode 오류, 관찰·미해결·도구 오류 수 |
| `workspace_drift`, `limitations` | 작업 사본 변경 파일, 정렬된 제약 코드 |

`obsdoc.Validate`의 주요 규칙:

- 성공이면 exit class가 억제되지 않은 비지표 관찰(`Document.Active`) 유무와 맞아야 한다. 억제된 관찰이나 지표 관찰만 있으면 `success_without_findings`다.
- 실패 상태는 성공 exit class를 가질 수 없다. `not_run`은 관찰이 없고 `processed_paths=[]`다.
- 관찰의 `raw_ref`는 기록된 실행의 stream SHA-256과 수집 크기 안에 있어야 한다. 위치는 manifest의 파일·blob SHA-256과 일치해야 하고 drift 파일에 연결할 수 없다.
- `suppressed=true`는 `source_suppressions=reported`일 때만 허용한다. `unreported_suppression_rules`는 정렬·중복 없는 규칙 ID 목록이며 `reported`가 아니면 `[]`다.
- `config_files`는 `repository_config=honored`가 아니면 빈 목록이다. `honored`이면 null(열거하지 못함) 또는 경로 순으로 정렬된 중복 없는 256개 이하 목록이고, 각 SHA-256은 manifest의 그 파일과 같아야 한다.
- 지표는 `unit`이 `count`·`lines`·`tokens` 중 하나이고 `value >= threshold`이며 억제될 수 없다.
- `analyzer.purpose`가 `metrics`이면 모든 관찰이 지표이고, `secrets`이면 지표 관찰이 없다.

### 분석기 목적

`analyzer.purpose`는 분석기 관찰이 무엇인지를 envelope에 적는다. 소비자(보고서 builder, 모델 입력의 비밀값 선별)는 도구 이름 목록 대신 이 값으로 지표·비밀값 분석기를 가린다.

| 값 | 뜻 | 분석기 |
|---|---|---|
| (없음) | 진단. 결함 후보가 될 수 있는 도구 진단 | 나머지 모든 분석기 |
| `metrics` | 구조·중복 측정. 모든 관찰이 `metric`을 갖는다 | lizard, jscpd |
| `secrets` | 비밀값 위치. 지표 관찰이 없고 문구는 고정 템플릿이다 | gitleaks |

- 값은 `pack.Descriptor.Purpose`에서 오며 같은 분석기의 모든 envelope(설정되지 않은 도구의 `not_run` envelope 포함)에 같은 값이 들어간다. 그래서 관찰이 없어도 보고서가 그 작업을 지표·비밀정보 절에 놓을 수 있다.
- 진단 분석기는 필드를 쓰지 않는다. 다른 분석기 envelope의 바이트는 바뀌지 않는다. 이 필드 이전에 쓴 envelope는 진단으로 읽고, 그 안의 지표 관찰은 그대로 지표다.
- 서술 파일(`examples/analyzer-*.yaml`)의 `purpose`는 envelope 값과 같아야 한다(`validate_bundle`이 확인).

### 보고서로 옮기는 규칙

`report.AnalyzerDecoder`는 (media, `0.1`, `analyzer_observations`)로 등록된다. 보고서 모델 자체는 [보고서](REPORT.md)를 따른다.

1. `obsdoc.Decode`로 task key·스냅샷·manifest와 함께 검증한다.
2. `analyzer.required`가 DB task와 다르거나, DB task가 성공인데 문서가 성공이 아니면 거부한다. 문서가 성공인데 DB가 아니면 DB를 따르고 제약을 남긴다.
3. 문서의 각 실행은 워커가 기록한 실행이어야 한다. stdout·stderr artifact의 SHA-256·크기, 성공 task의 exit code를 대조한다.
4. `raw_severity`는 짧은 단어(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)일 때만 보고서 관찰로 옮긴다. byte가 아닌 열은 0으로 두고 제약을 남긴다.
5. manifest와 맞고 억제되지 않은 비지표 관찰은 **검토 전 도구 finding**이 된다. severity는 [기본 심각도](#기본-심각도) 표가 정하고(builder `22`부터. 그 전 보고서는 모두 `low`. 시험·예제 등 배포 코드가 아닌 경로의 진단은 builder `23`부터 [경로 규칙](#기본-심각도)으로 낮춘다), validation `unreviewed`, disposition `open`이며 검토 없이는 actionable이 아니다. 문구는 도구 이름이나 코드를 담지 않는 고정 템플릿이다. 심각도 사유도 표가 정한 고정 문장이다. 억제된 관찰과 지표 관찰은 보고서 관찰로만 남고, 그것을 인용하는 finding은 보고서 검증이 거부한다.
6. 분석기·규칙·경로·시작 행·시작 열·메시지가 모두 같은 관찰만 한 finding으로 묶는다(TC-09). 도구 사이 자동 병합은 하지 않는다(TC-102).
7. fingerprint는 공용 `report.ToolFingerprint(analyzer, rule, path, "", message)`(fp2)로 만든다. 팩 decoder가 자기 hash를 만들지 않는다. 줄 이동은 identity를 바꾸지 않는다.
8. task마다 도구 범위 행(`coverage.tools`)을 하나 만든다. `processed_files`는 DB가 성공으로 받은 task이거나 `not_run`(0)일 때만 센다. `processed_paths`는 성공한 task에만 기록하며, run 비교는 이 경로의 위치만 다시 검사한 것으로 본다. 보고서 `tool_processed_files`는 처리 경로의 합집합이다(같은 파일을 두 분석기가 읽으면 한 번). 처리 수와 경로 수가 다른 행이 있으면 합계를 쓴다. 행에는 `rule_context`를 기록한다.

거부된 envelope는 `artifact_rejected:<key>` 제약이 되고, 그 task는 해석되지 않은 것으로, 처리 파일 수는 미확정으로 처리한다.

### 기본 심각도

도구 finding의 심각도는 분석기 팩마다 둔 표가 정한다([`internal/worker/analysis/severity`](../internal/worker/analysis/severity/severity.go), 팩 디렉터리의 `severity.go`). 모델은 관여하지 않는다. 표는 구조화 데이터만 읽는다. 분석기 ID, envelope의 규칙 팩 ID·버전, 규칙 ID, 도구 원시 등급이다.

1. 표는 규칙 ID(정확히 같은 ID, 또는 `*`로 끝나는 접두사)를 **규칙 계열**에 대응시킨다. 규칙이 표에 없으면 소문자로 바꾼 원시 등급의 대응을 보고, 그것도 없으면 표의 기본 계열을 쓴다. 계열이 심각도를 정한다.
2. 표는 자기가 쓰인 규칙 팩 버전 목록을 갖는다. envelope의 규칙 팩 버전이 목록에 없으면 표를 쓰지 않고 `low`로 둔다(사유: 규칙 팩 버전이 표와 다름). 규칙 선택을 바꿔 규칙 팩 버전을 올리면 표도 함께 검토해 목록에 넣는다. 표가 없는 분석기, 대응이 없는 규칙도 `low`다.
3. 원시 등급은 관찰에 그대로 남는다. 표는 원시 등급을 고치지 않는다.
4. `critical`은 기본값이 될 수 없다. 실행 근거나 사람의 판단이 필요하다.
5. 보고서 도구 행에는 쓰인 표와 경로 규칙이 `severity_table`(`<규칙 팩>/<표 버전>+<경로 규칙>`, 예: `codeye-go-vet-default/1+paths.2`)로 남는다. builder 22 보고서는 `+<경로 규칙>` 없이 표만 적고 builder 23 보고서는 `+paths.1`을 적는다. 표가 없는 행은 이 필드를 생략한다.
6. 지표(`metrics`)와 비밀값(`secrets`) 분석기는 표를 두지 않는다. 카탈로그의 진단 분석기는 모두 표가 있어야 한다(TC-292).
7. 계열의 `merge`는 서로 다른 두 도구가 같은 파일·같은 줄에서 그 계열의 규칙을 보고하면 같은 구성으로 본다는 뜻이다. 이 판단은 [도구 진단 분류](REVIEW.md#도구-진단-분류)의 묶음에만 쓰고 finding은 합치지 않는다(TC-102 유지).
8. **경로 규칙**(`paths.2`, builder 24부터. builder 23은 `paths.1`). 시험·예제·벤치마크·생성물·외부 코드 경로의 진단은 계열의 표 심각도를 그대로 쓰지 않고 표의 "배포 코드가 아닌 경로" 열로 낮춘다. `high`·`medium` 계열은 `low`, 중단 경로·성능·유지보수 계열은 `info`, 그 밖은 그대로다. 낮춘 finding의 심각도 사유는 계열 이름과 경로 규칙 버전을 담은 고정 문장이고(`severity.Reasons()`가 모두 싣는다) 원시 등급은 관찰에 그대로 남는다. 표에 없는 규칙과 다른 규칙 팩 버전은 경로와 상관없이 `low`다. 근거는 실제 프로젝트 하나의 분류 측정이다([평가 결과](EVALUATION-RESULTS.md#분류-묶음의-독립-판정)). 배포 코드의 진단은 이전과 같다.
9. 경로의 종류는 하나의 공용 규칙([`record.PathClass`](../internal/worker/review/record/priority.go))이 파일 경로만 보고 **확실한 것만** 정한다. 요청의 `path_class`, 조사 후보를 검증에서 빼는 결정, 도구 진단의 심각도와 분류 순서가 모두 이 값 하나를 읽는다. 확실한 것: 언어의 시험 파일 이름 규칙(Go `*_test.go`, Python `test_*.py`·`*_test.py`·`conftest.py`, JS·TS `*.test.*`·`*.spec.*`, Java `*Test.java`·`*Tests.java`, Rust `tests.rs`, C/C++ `test_*`·`*_test` 등)과 잘 알려진 뿌리 아래의 파일이 `test`(Java `src/test/`, `testdata`·`test-data`·`__tests__`·`__snapshots__`, 소스 트리 안이 아닌 `tests/`와 저장소 첫 디렉터리 `test/`)·`example`(소스 트리 안이 아닌 `examples/`)·`benchmark`(소스 트리 안이 아닌 `benchmarks/`·`benches/`)·`vendored`(`vendor`·`third_party`·`node_modules`)다. 소스 트리 안은 앞선 디렉터리에 `src`·`lib`·`main`·`internal`·`pkg`·`app`·`cmd`·`include`가 있는 경우다. 소스 트리 안의 `test`·`demo`·`gen`·`build`·`fixtures`·`mocks`·`dist`·`generated` 같은 디렉터리 이름은 추측이라 쓰지 않고 그 코드는 배포 코드로 남는다(builder 23의 규칙은 이것들을 시험·예제·생성물로 보았다. `record.LegacyPathClass`가 builder 23 보고서의 검증에만 남는다). 파일 첫 줄의 생성 표지는 보고서 생성기가 보지 못하므로 도구 심각도와 후보 결정에는 쓰지 않는다. 모델 요청에서 파일의 첫 줄을 싣는 발췌에만 그 표지가 있으면 `path_class: generated`로 알린다(`record.GeneratedHeader`). 경로 종류 `generated`는 그래서 보고서의 관찰에는 나타나지 않는다. 벤치마크 구간은 보조 경로 규칙이 아니므로 검토 우선순위와 범위 산출물은 그대로다. 관찰은 위치가 있으면 배포 코드가 아닐 때 그 종류를 `path_class`로 싣고, 도구 finding의 심각도와 도구 진단 접힘의 경로별 수가 이 값을 읽는다.
10. 경로 규칙을 바꾸면(계열의 낮춘 등급, 경로의 종류) `paths.N`의 N을 올린다. run 비교는 `severity_table` 값이 다르면 그 도구의 finding 심각도 차이를 발견의 변경으로 세지 않고 도구 문맥 차이 `tool:<analyzer>:severity_table` 하나로 보고한다([비교](REPORT.md#run-비교)). 이전 보고서와 비교하면 같은 시험 파일의 finding이 `persisting`이고 심각도 차이는 이 차이 하나다.
11. 표의 규칙은 계열 등급보다 낮은 **상한**(`Cap`)을 둘 수 있다. 그 규칙이 실제 코드에서 보고하는 것의 정확도가 계열 등급을 뒷받침한다는 측정이 없을 때 쓴다. 같은 줄 병합은 계열로 정하므로 상한을 두어도 다른 도구 보고와 한 묶음이 된다. 상한으로 낮춘 심각도의 사유는 별도의 고정 문장이다. 상한은 계열 등급보다 낮아야 한다(표 검사). 경로 규칙은 상한 뒤에 적용한다. 상한이 정한 등급보다 경로 열이 낮으면 경로 열이 되고 사유는 경로 규칙의 문장이다.

| 계열 | 기본 심각도 | 배포 코드가 아닌 경로 | 같은 줄 병합 | 근거 |
|---|---|---|---|---|
| `null_dereference` | high | low | 예 | 경로에 들어오면 프로세스가 멈추거나 정의되지 않은 동작이 된다 |
| `memory_safety` | high | low | 예 | 범위 밖 접근·해제 뒤 사용·초기화하지 않은 값은 데이터 손상과 보안 문제로 이어진다 |
| `undefined_behavior` | high | low | 예 | 컴파일러와 플랫폼에 따라 결과가 달라 시험으로 드러나지 않을 수 있다 |
| `concurrency` | high | low | 예 | 경합·잠금 복사·공유 변수 포획은 재현이 어렵고 데이터를 바꾼다 |
| `injection` | high | low | 예 | 외부 입력이 명령·질의·경로·코드로 실행된다 |
| `resource_leak` | medium | low | 예 | 오래 실행하면 파일·연결·잠금이 고갈되지만 한 번의 호출은 대개 끝난다 |
| `unchecked_error` | medium | low | 예 | 실패가 조용히 사라져 이후 동작이 틀린 상태로 이어질 수 있다 |
| `exception_handling` | medium | low | 아니오 | 빈 처리부·너무 넓은 처리는 실패를 숨기지만 영향은 호출 경로마다 다르다 |
| `logic_error` | medium | low | 예 | 항상 같은 비교·자기 대입 등은 의도와 다른 동작일 가능성이 높다 |
| `api_misuse` | medium | low | 예 | 라이브러리 계약을 어겨 특정 입력에서 잘못된 결과가 난다 |
| `type_error` | medium | low | 아니오 | 타입·컴파일 오류는 빌드 문맥에 따라 분석 공백일 수도 있다 |
| `security_weakness` | medium | low | 아니오 | 약한 암호·안전하지 않은 설정은 배포 맥락에 따라 위험이 달라진다 |
| `panic_path` | low | info | 아니오 | unwrap·panic·assert는 의도한 불변식 표현인 경우가 많다 |
| `deprecated_api` | low | low | 아니오 | 지금 동작은 맞고 이후 버전에서 문제가 된다 |
| `unused_code` | low | low | 예 | 동작에는 영향이 없거나 작고, 남은 값이 논리 실수를 가리킬 때만 문제다 |
| `performance` | low | info | 아니오 | 정확성은 맞고 비용만 늘어난다 |
| `maintainability` | low | info | 아니오 | 복잡도·의존 규칙·시험 형태는 변경 비용의 문제다 |
| `documentation` | info | info | 아니오 | 문서 주석의 형식이다 |
| `style` | info | info | 아니오 | 명명·단순화 제안이다 |
| `formatting` | info | info | 아니오 | 서식 차이다 |

팩별 표: go vet(합성 규칙 팩 `codeye-go-vet-default`@1), Staticcheck(`SA5011` 널 역참조, `SA1019` 사용 중단, `SA1*` API 오용, `SA2*` 동시성, `SA4*`·`SA5*` 논리, `SA6*` 성능, `S1*`·`ST1*`·`QF*` 스타일 등), errcheck(오류 무시), gofmt(서식), cppcheck(규칙 ID와 cppcheck 등급 대응), clang-tidy(검사 이름 접두사), GCC 파일 디스크립터 검사(자원 누수), ruff·mypy·pyright·bandit, import-linter·dependency-cruiser(유지보수), ESLint 세 구성과 tsc, cargo check·clippy·rustdoc, PMD·SpotBugs, Opengrep다. 표 목록은 [`builtin.SeverityTables`](../internal/worker/analysis/builtin/severity.go)가 한곳에 모은다.

- **Opengrep**(`codeye-taint` v2, 표 1). 규칙마다 요청 입력이 위험한 호출에 닿는 흐름이다.
  - 명령·SQL·경로·코드 평가·역직렬화·HTML 출력은 요청 바이트가 명령·질의·파일 이름·코드·페이지가 되므로 `injection` 계열이다. 같은 구성을 보고하는 bandit(셸·SQL·Markup), SpotBugs(SQL·경로·서블릿 XSS), clang-tidy(`bugprone-command-processor`), ESLint(eval 계열)와 같은 계열이어서, 같은 줄의 두 보고는 한 분류 묶음이 된다.
  - SSRF는 요청이 나가는 요청의 대상을 고르게 한다. 무엇이 드러나는지는 서버가 닿을 수 있는 곳에 달려 있어 `security_weakness` 계열이고 다른 도구 보고와 합치지 않는다.
  - **기본 심각도는 상한으로 낮춘다.** `injection` 규칙은 medium, SSRF는 low다. 오탐 없는 높은 재현율은 한 파일짜리 벤치마크 코드에서만 쟀다. 실제 애플리케이션 27개에서는 진단 10건 중 실제 흐름이 0건이었다(오탐 9, 설계상 무해 1, [측정](#opengrep-규칙-측정)). 실제 애플리케이션의 정확도를 재지 못했거나 낮으므로, high로 두면 오탐이 목록과 분류의 맨 앞에 선다. 상한을 올리려면 실제 애플리케이션에서 잰 보고 전용 근거가 있어야 한다. 분류가 `problem`으로 보고 검증이 지지한 묶음은 검증이 정한 심각도를 가진 모델 finding이 되므로, 실제 흐름은 이 상한 때문에 낮아지지 않는다(`TestOpengrepDiagnosticTriage`).
  - 규칙을 더하면 계열과 상한을 정해야 한다(`TestSeverityCoversEveryRule`).
- SpotBugs 표 2는 [엔진 중복](#엔진-중복-1)에서 taint 팩과 같은 줄을 보고한 경로(`PT_*`)·서블릿 XSS(`XSS_*`) 검출기를 `injection` 계열로 더해 같은 줄 보고를 한 묶음으로 만든다. 기본 심각도는 표에 없던 때와 같은 low 상한이다. 이 검출기가 실제 코드에서 보고하는 것을 재지 않았고, 포함 검사를 거친 음성 fixture에서 `PT_ABSOLUTE_PATH_TRAVERSAL`이 보고됐다. bandit·clang-tidy 표는 바꾸지 않았다.

### 규칙 문맥과 소스 안 억제

도구 버전 말고도 진단을 정하는 것이 셋 있다. CODEYE 규칙 선택, 저장소 설정, 소스 안 억제 주석이다. run 비교는 사라진 도구 finding을 **양쪽의 규칙 문맥이 알려져 있고 같을 때만** `resolved_observed`로 보고 그 밖은 `absent_unverified`로 둔다(TC-104). 모든 팩이 이 계약을 따른다.

보고서 도구 행의 `rule_context`(보고서 builder `4`부터):

| 필드 | 내용 |
|---|---|
| `rule_pack`, `rule_pack_version`, `selection` | envelope `rule_pack`의 id·version·selection |
| `repository_config` | `overridden`(CLI가 저장소 설정을 대체), `honored`(도구가 저장소 설정을 읽음), `none`(설정 없음) |
| `source_suppressions` | `reported`, `honored`, `disabled`, `none` |
| `unreported_suppression_rules` | `reported`인데도 억제를 보고하지 않는 규칙(비면 생략) |
| `config` | `not_read`(`honored`가 아님), `recorded`(읽은 파일을 기록함), `unrecorded`(`honored`인데 `config_files=null`) |
| `config_paths`, `config_digest` | `recorded`일 때만. 읽은 파일이 없으면 `config_paths`는 생략되고 digest만 있다. digest는 SHA-256(`codeye-rule-config-v1\0` + 경로 순으로 각 `path\0sha256\n`)이다. **코드 제외 export에서 뺀다.** 모드와 `config` 상태는 남는다 |

문맥이 **알려짐**은 `config`가 `not_read`이거나 `recorded`이고 digest가 있는 경우다. 문맥이 없는 행(builder 3 이하 보고서의 go vet 외 분석기)은 **모름**이다. 두 실행에서 단일 발견 경로를 처리한 성공 행을 확인할 수 있으면 해당 행들의 문맥을 비교한다. 처리 수에 비해 경로 목록이 짧은 성공 행도 그 파일을 처리했을 수 있어 함께 비교한다. 이전 보고서에서 해당 행을 확인할 수 없으면 분석기 전체의 문맥을 비교한다. 실행 요약의 도구 차이는 모든 행을 보여 준다.

| 사유 | 조건 |
|---|---|
| `suppressed_in_source` | 현재 쪽 도구가 같은 fp2의 진단을 억제됨으로 보고함. 기준 쪽이 억제로 보고한 진단이 현재 새로 나타나면 `new`에 같은 사유 |
| `rule_context_unknown` | 한쪽이라도 문맥 모름 |
| `rule_context_changed` | 규칙 팩·버전·선택·모드·설정 digest 중 하나가 다름. 차이는 `tool:<analyzer>:rule_context`에 보임(digest는 앞 12자) |
| `source_suppressions_hidden` | 현재 쪽이 `source_suppressions=honored`이거나 그 규칙이 `unreported_suppression_rules`에 있음 |

우선순위는 위치 미검사(`check_failed`, `location_not_inspected`) → 원시 관찰의 억제·활성 진단(`suppressed_in_source`, `active_observation_remains`) → 도구 버전(`rules_unknown`, `rules_changed`) → 규칙 문맥(`rule_context_unknown`, `rule_context_changed`) → `source_suppressions_hidden`이다.

팩이 지킬 규칙:

1. **저장소 설정.** CLI 인자로 저장소 설정을 대체하면 `overridden`이며, 대체되지 않는 설정 항목이 진단에 영향을 주지 않음을 측정해야 한다. 저장소 설정을 읽게 두면 `honored`이고 도구가 실제로 읽은 설정 파일(상속·include 포함)을 모두 `config_files`에 적는다. 열거할 수 없거나 스냅샷 밖 설정(사용자·전역 설정, 설치된 공유 설정 패키지)을 읽을 수 있으면 `null`이다. 열거하지 못하면서 `overridden`이나 빈 목록을 주장하지 않는다.
2. **소스 안 억제.** 도구가 억제된 진단을 표시해 보고할 수 있으면 `reported`로 하고 관찰에 `suppressed=true`를 쓴다. `reported`는 선택한 모든 규칙에서 성립해야 하며 규칙 계열마다 측정한다. 억제를 보고하지 않는 규칙은 `unreported_suppression_rules`에 적는다. 억제된 관찰은 finding이 되지 않고 검토 해석 입력에도 들어가지 않으며 제약 `source_suppressed_observations`를 남긴다. 억제를 끄고 모든 진단을 받으면 `disabled`, 억제 문법이 없으면 `none`, 어느 것도 할 수 없으면 `honored`다. `honored` 분석기의 사라진 finding은 언제나 `absent_unverified`(`source_suppressions_hidden`)다. 파일 SHA-256 변화만으로는 수정과 억제 주석 추가를 가를 수 없기 때문이다.
3. 규칙 선택이나 그 의미를 바꾸면 `rule_pack.version`을 올린다.

### 탐색 probe

`builtin.Probes(cfg)`가 `pack.ToolProbe` 목록(도구, 분석기 ID, 생태계, 실행 파일, 버전 argv, parser)을 만들고 `discovery.ProbeObserver.Analyzers`로 env.discover에 넘긴다.

- 스냅샷 범위의 생태계(런타임 도구에서 역산)와 `any`에 속한 도구만 다룬다.
- 설정된 도구는 executor로 `<tool> <VersionArgs>`를 실행한다. cwd는 attempt scratch, 환경은 `PATH=<도구 디렉터리>`·`LANG=C.UTF-8`(Windows는 `SystemRoot`), 제한시간 30초, 실행 이름 `<tool>.version`이다. `Interpreter`가 있으면 `<interpreter> <도구> <VersionArgs>`이고 PATH는 interpreter 디렉터리다.
- 설정하지 않은 도구는 `not_probed`, 실행 파일이 없으면 `not_found`, 시작·종료·출력 문제는 `probe_failed`와 사유 코드다. interpreter가 없으면 `probe_failed`(`toolchain_not_configured`), 경로가 실행 파일이 아니면 `probe_failed`(`toolchain_unavailable`)이며 아무것도 시작하지 않는다. 팩이 거부한 경로(예: rustup proxy)도 실행하지 않고 `probe_failed`로 기록한다.
- 결과는 환경 문서 `environment.analyzer_tools`에 쓴다. 선언과 비교하지 않는다. 보고서는 관측 버전에 기록된 정상 probe 실행이 있어야 받아들인다. 버전을 식별하지 못한 실행 파일은 코드에 실행하지 않는다.
- 공유 toolchain은 `builtin.ToolchainProbes(cfg)`가 `discovery.ProbeObserver.Toolchains`로 넘기고 **런타임 관측**(`environment.observations`)으로 기록한다. 설정된 node는 `node.version`으로 실행하고 관측 버전을 선언(`engines.node` 등)과 비교해 판정을 만든다. 설정하지 않으면 `not_probed`와 `tool_not_probed` 제약이다. 범위 생태계의 분석기가 그 toolchain을 필요로 하면 모듈 선언이 없어도 관측하고(`Observed.Needed`), 이 관측은 판정을 만들지 않는다.

### 검토 해석 입력

`review.interpret` 역할은 계획된 분석기 task의 envelope를 `obsdoc.Decode`로 검증해 읽는다. 없으면 go-vet 소유 key에 한해 v0.1 문서를 읽는다. 둘 다 없으면 상류 `unavailable`(`record_missing`)이며 0건으로 바꾸지 않는다. 억제된 관찰과 지표 관찰은 넣지 않는다. 도구 상태 행은 최대 8개, 관찰은 최대 8개이고 넘치면 `tools_outside_interpret_input`, `observations_outside_interpret_input` 제약을 남긴다. `related` 위치는 envelope와 해석 입력에만 있고 보고서 finding은 주 위치만 보인다. 역할 구성은 [검토](REVIEW.md)를 따른다.

### 엔진 중복

- 같은 엔진을 두 번 실행하지 않는다(clang-tidy 안의 CSA와 `clang --analyze`, ruff S와 bandit, lizard `-Eduplicate`와 jscpd).
- 서로 다른 엔진이 같은 줄에서 같은 구성을 보고하는 검사는 기준 분석기 쪽을 남기고 추가 깊이 쪽 검사를 선택에서 뺀다. 판단은 고정 fixture에서 두 도구를 함께 실행해 측정한다. 추가 깊이 쪽만 보고한 변형이 있으면 그 검사는 남긴다. 측정은 그 fixture의 구성에 한정되므로 다른 코드에서 다시 고를 때는 규칙 팩 버전을 올린다.
- 남은 같은 줄 보고는 각 task의 관찰로 남고 보고서는 도구 사이에서 자동 병합하지 않는다. 같은 원인인지 판단하는 일은 검토 단계에 둔다. [도구 진단 분류](REVIEW.md#도구-진단-분류)는 같은 선언 단위·같은 줄·병합 계열의 두 도구 진단을 한 묶음으로 모델에 보내고 그 사실을 기록하지만 finding은 도구마다 남는다. 처리 파일은 합집합으로 센다.

## 의존성 준비

`deps.*` task는 분석기가 읽을 의존성을 분석 전에 모듈마다 받는다. 흐름·캐시·예산·기록과 프로젝트 선택은 [의존성 준비](ARCHITECTURE.md#의존성-준비)를 따른다(결정 A42, A43, A44). 각 task는 그 의존성을 읽는 분석기와 같은 모듈 계획을 쓴다. Rust·JS/TS·Python은 그 분석기를 이 노드에서 쓸 수 없으면 `no_dependency_consumer`로 건너뛴다. 받는 도구는 분석기가 아니다. go·cargo는 분석기의 toolchain(`--go`, `--cargo`)이고, npm·pnpm·yarn·uv·python은 패키지 관리자 플래그이며 hello의 `dependencies.package_managers`로 워커에 간다(`deps.ManagerFlags`).

| 생태계 | task | 받는 도구 | 저장소 코드 실행 허가 | 받은 것의 위치 | 읽는 분석기 |
|---|---|---|---|---|---|
| Go | `deps.go` | `--go` | 필요 없음 | 공유 캐시 `go/mod` | go vet, Staticcheck |
| Rust | `deps.rust` | `--cargo` | 필요(분석기가 build script를 실행한다) | 공유 캐시 `rust/cargo` | cargo check, clippy |
| JS/TS | `deps.javascript` | `--node`와 `--npm`·`--pnpm`·`--yarn` | 필요 없음(lifecycle script 없이 설치) | attempt 실행 사본의 `node_modules` | tsc, type-aware ESLint |
| Python | `deps.python` | `--python`, `--uv` | 필요 없음(wheel만) | 고정이면 공유 `python/trees/<key>`, 아니면 attempt 트리 | mypy, pyright |

### Go 의존성 받기

`deps.go`·`deps.go.mNN` task가 go vet과 같은 모듈 계획으로 모듈마다 의존성을 받는다([의존성 준비](ARCHITECTURE.md#의존성-준비)). go vet과 Staticcheck는 그 뒤에도 `GOPROXY=off`로 실행하고 받은 캐시만 읽는다.

- 명령: 실행 사본의 모듈 디렉터리에서 `go version`, `go mod edit -json <사본>`, `go mod download -json -modfile=<사본>`. 사본은 attempt의 task 디렉터리에 복사한 `go.mod`·`go.sum`이다. 그래서 받기가 go.mod·go.sum을 고치더라도 실행 사본과 snapshot은 그대로이고 go vet의 drift 검사가 유지된다. 실행 전에 두 파일 hash를 manifest와 대조하고, 받은 뒤 실행 사본의 두 파일이 바뀌었으면 `workspace_drift`다.
- 환경: `GOENV=off`, `GOWORK=off`, `GOTOOLCHAIN=local`, `GOFLAGS=-mod=readonly -modcacherw`, `CGO_ENABLED=0`, `GOVCS=*:off`, `GOMODCACHE=<받기 캐시>/go/mod`, task별 `GOCACHE`·`GOPATH`, telemetry 끔.
- `GOPROXY`: 운영자 모듈 캐시(`--go-mod-cache`)의 `cache/download`에 모듈 파일(`.mod`·`.zip`)이 있으면 `file://` 출처를 맨 앞에 둔다(읽기만 한다, 한계 `seeded_from_operator_cache`). 그 뒤에 운영자 `GOPROXY`(기본 `https://proxy.golang.org`)에서 `direct`·`off`를 뺀 목록이 온다. 목록이 비면 `off`다. VCS에서 직접 받지 않는다. `GOSUMDB`는 운영자 값, 없으면 `sum.golang.org`다.
- 비공개 모듈: 운영자의 `GOPRIVATE`·`GONOPROXY`·`GONOSUMDB`를 그대로 넘기므로 비공개 모듈은 공개 proxy와 checksum DB에 묻지 않는다. 운영자 캐시에 이미 있는 비공개 모듈은 받기 캐시로 복사해 쓰고(한계 `private_modules_seeded`), 없으면 `private_module_not_fetched`로 끝난다. URL에 인증정보(`user:token@`)가 든 `GOPROXY`·`GOSUMDB`는 넘기지 않는다. 탐지는 그 값을 `off`로 바꾸고(공개 proxy로 대신 받지 않는다), 설정과 플래그는 거부한다. 인증이 필요한 proxy는 지원하지 않는다.
- 받을 것이 없음: `vendor/modules.txt`가 있으면 `vendored_dependencies`, `require`가 없으면 `no_dependencies`(둘 다 `skipped_policy`). 스냅샷 밖 `replace`는 go vet과 같이 `replace_outside_snapshot`이다.
- 캐시: 공유 캐시 key `go/mod`. 모듈 zip과 푼 트리 모두 크기 상한과 cache 저장 영역에 센다.
- 사유: `module_not_on_proxy`(일부 모듈만 실패하면 `partial`), `checksum_mismatch`, `go_sum_incomplete`(go.sum 사본이 받는 도중 바뀜. go.sum에 없는 항목이 있다는 뜻이고 go vet은 `-mod=readonly`로 실패한다), `go_mod_unreadable`, `snapshot_integrity_failed`, `workspace_drift`, `private_module_not_fetched`, `toolchain_unavailable`(go.mod가 노드 go보다 높은 버전 요구), `registry_unreachable`, `package_manager_not_configured`(`--go` 없음)와 공통 사유.
- 오프라인 모드(`--offline`): 운영자 proxy가 없으면 운영자 모듈 캐시만 읽고, 캐시에 없는 모듈은 `failed`(`offline_no_registry_configured`, `failed_items`에 그 모듈과 `module_not_on_proxy`)다. 캐시도 proxy도 없으면 `skipped_policy`다. `go_proxy`가 `off`이면 받기를 시작하지 않고(`skipped_policy`) 같은 사유다. checksum DB를 정하지 않았으면 `GOSUMDB=off`다. JS/TS(레지스트리 없음), Python(index 없음), Rust(항상)도 오프라인에서는 받지 않고 같은 사유로 `skipped_policy`다([오프라인 모드](OPERATIONS.md#오프라인-모드)). `--ca-bundle`을 주면 모든 관리자 실행에 CA 변수가 붙는다([사내 CA](OPERATIONS.md#사내-ca)).
- stderr는 사유를 정하는 데만 쓰고 받기 캐시 경로는 마스킹한다.

### Rust 의존성 받기

`deps.rust`·`deps.rust.mNN` task가 cargo check와 같은 모듈 계획으로 모듈마다 crate를 받는다([의존성 준비](ARCHITECTURE.md#의존성-준비)). cargo check·clippy는 그 뒤에도 `--offline`·`CARGO_NET_OFFLINE=true`로 실행하고 받은 `CARGO_HOME`만 읽는다.

- **허가.** cargo는 받는 동안 저장소 Cargo 설정(`.cargo/config.toml`)을 읽고, 받은 crate를 쓰는 cargo check·clippy는 build script를 실행한다. 그래서 저장소 코드 실행 허가(`codeye up`의 에이전트는 프로젝트의 "저장소의 빌드 스크립트 실행"과 관리자 스위치)가 없으면 아무것도 시작하지 않고 `skipped_policy`/`dependency_consumers_not_allowed`다. `--cargo`가 없으면 `package_manager_not_configured`, cargo check를 쓸 수 없으면 `no_dependency_consumer`, source replacement로 vendor 디렉터리를 쓰면 `vendored_dependencies`, `Cargo.lock`에 레지스트리·git crate가 없으면 `no_dependencies`다.
- **명령.** 실행 사본의 모듈 디렉터리에서 `rustc -vV`, `cargo --version`, (`Cargo.lock`이 없을 때) `cargo generate-lockfile`, `cargo fetch --locked --target <host>`. 환경은 cargo check와 같고(`RUSTUP_TOOLCHAIN` 없이 운영자 toolchain, task별 `CARGO_TARGET_DIR`) `CARGO_HOME=<받기 캐시>/rust/cargo`이며 `CARGO_NET_OFFLINE`만 뺀다.
- **lockfile 없음.** `generate-lockfile`이 버전을 고른다(한계 `dependency_versions_not_pinned`). 만든 `Cargo.lock`은 task 디렉터리에 옮겨 두고 실행 사본에서는 지운다. cargo check·clippy는 실행하는 동안만 그 파일을 모듈 디렉터리에 두고(`--locked`가 읽는다) 끝나면 지운다. 파일이 없으면 기록을 쓰지 않은 것처럼 실행한다.
- **캐시.** 공유 캐시 key `rust/cargo`. 레지스트리 색인·crate 파일·푼 소스가 크기 상한과 cache 저장 영역에 센다. 받은 뒤 `Cargo.toml`·`Cargo.lock`이 바뀌었으면 `workspace_drift`다.
- **분석기.** cargo check·clippy는 기록이 `prepared`·`partial`이면 그 `CARGO_HOME`을 쓰고 `offline_scratch_cargo_home` 대신 `dependencies_prepared`(`partial`이면 `dependencies_partially_prepared`)를 남긴다.
- **사유.** `lockfile_outdated`(`--locked`인데 lockfile을 고쳐야 함), `manifest_invalid`, `dependency_not_found`, `registry_auth_required`, `registry_unreachable`, `toolchain_unavailable`, `snapshot_integrity_failed`, `workspace_drift`와 공통 사유.

### JS/TS 의존성 받기

`deps.javascript`·`deps.javascript.mNN` task가 설치 루트마다 의존성을 이번 attempt의 실행 사본에 설치한다([의존성 준비](ARCHITECTURE.md#의존성-준비)).

- **설치 루트.** JS/TS 모듈마다 자기 디렉터리나 상위에서 가장 가까운 lockfile 디렉터리(workspace 루트가 멤버를 맡는다), lockfile이 없으면 가장 바깥 `package.json` 디렉터리다(`jsts.InstallRoots`). 루트 디렉터리는 접미사가 없고 나머지는 경로 순서로 `.m01`...이다. tsc·type-aware ESLint가 같은 계획으로 자기 모듈의 기록을 찾는다. 계획 입력은 manifest뿐이다.
- **관리자 선택.** 설치 루트의 lockfile로 고른다: `pnpm-lock.yaml` → pnpm, `yarn.lock`(`# yarn lockfile v1`) → yarn 1, `npm-shrinkwrap.json`·`package-lock.json` → npm, lockfile 없음 → npm(`npm install --no-package-lock --no-save`, 한계 `dependency_versions_not_pinned`). 여러 관리자의 lockfile이 있으면 `package.json`의 `packageManager`가, 없으면 위 순서의 첫째가 정하고 `multiple_lockfiles`를 남긴다. yarn 2 이상(`__metadata:`)과 bun lockfile은 `unsupported`/`lockfile_format_unsupported`다. tsconfig가 있는 모듈이 없거나 tsc·type-aware ESLint를 쓸 수 없으면 `no_dependency_consumer`, 받을 패키지가 없으면 `no_dependencies`다.
- **명령.** `<node> <entry script>`로 실행한다. 먼저 `--version`(pnpm은 `--config.pm-on-fail=ignore --version`), 그다음 설치다.
  - npm: `ci`(lockfile 없으면 `install --no-package-lock --no-save`) `--ignore-scripts --no-bin-links --no-audit --no-fund --include=dev --include=optional --include=peer --engine-strict=false --prefer-offline --prefix <루트> --cache <캐시> --logs-dir <task>/npm-logs --userconfig <빈 파일> --globalconfig <빈 파일> --registry <레지스트리> --proxy=null --https-proxy=null --install-links=false --fetch-retries=1 --fetch-retry-mintimeout=2000 --fetch-retry-maxtimeout=10000 --git=git`. 기본 재시도로는 닿지 않는 레지스트리에서 npm 11이 5분 넘게 재시도했다(실측). 재시도를 하나로 줄이면 1분 안에 `registry_unreachable`로 끝난다. pnpm도 같은 재시도 값을, yarn은 `--network-timeout 60000`을 받는다.
  - pnpm: `install --frozen-lockfile --ignore-scripts --ignore-pnpmfile --prefer-offline --dir <루트> --modules-dir node_modules --virtual-store-dir node_modules/.pnpm --store-dir <캐시>`와 `lockfile-dir`(루트), `cache-dir`·`state-dir`·`global-dir`(task 디렉터리), `registry`, `proxy=false`·`https-proxy=false`, 릴리스 전환 끄기(`pm-on-fail=ignore`, `manage-package-manager-versions=false`), `side-effects-cache=false`, `verify-deps-before-run=false`.
  - yarn 1: `install --frozen-lockfile --ignore-scripts --no-bin-links --non-interactive --ignore-engines --prefer-offline --modules-folder <루트>/node_modules --cache-folder <캐시> --registry <레지스트리>`.
- **환경.** `PATH`는 node 디렉터리뿐, `LANG`, `CI=true`, `NO_UPDATE_NOTIFIER=1`. `HOME`은 executor의 실행별 디렉터리라 사용자 `~/.npmrc`(토큰)를 읽지 않는다. pnpm은 `pnpm_config_pm_on_fail=ignore`와 `manage_package_manager_versions=false`도 받는다. pnpm 11은 `--version`을 포함한 모든 명령에서 `packageManager`가 적은 릴리스로 바꾸고 내려받는다(실측). 위 값이 그것을 막는다. yarn은 `YARN_IGNORE_PATH=1`로 `.yarnrc`의 `yarn-path`를 실행하지 않는다.
- **위치 고정.** 저장소 `.npmrc`·`.yarnrc`·`.yarnrc.yml`은 비밀 파일 규칙으로 스냅샷에 들어오지 않는다. pnpm은 `pnpm-workspace.yaml`과 `package.json`에서도 설정을 읽으므로 위치·레지스트리·proxy를 명령줄로 정하고, 명령줄 값이 두 파일보다 이기는 것을 실측했다(npm 11.16, pnpm 11.28, yarn 1.22). 그래서 쓰기는 설치 루트의 `node_modules`, 공유 캐시(`javascript/{npm,pnpm,yarn}`), task 디렉터리 안에 머문다.
- **밖을 가리키는 로컬 의존성.** 설치 전에 설치 루트 아래 `package.json`들의 의존성 필드와 lockfile의 로컬 항목(npm `packages` key와 link `resolved`, pnpm importer의 `link:`·`file:`, yarn 항목)을 읽어, `file:`·`link:`·`portal:` 경로가 스냅샷 밖(절대 경로, `..`로 벗어남)이면 설치를 실행하지 않는다. 패키지 관리자가 그 디렉터리에 설치하게 되기 때문이다. `skipped_policy`/`local_dependency_outside_project`이고 `failed_items`가 그 의존성을 적으며 분석기는 받기가 없을 때처럼 실행한다. 스냅샷 안의 로컬 의존성과 workspace 참조는 그대로 설치한다. 설치 루트 아래의 모든 `package.json`을 보므로 workspace 멤버가 아닌 시험 자료의 `package.json`도 설치를 막을 수 있다.
- **트리.** 설치가 끝나면 설치 루트 아래의 `node_modules`(멤버 포함, `node_modules` 안으로 내려가지 않음, 최대 256)를 `installed_dirs`에 적는다. 한 번 더 훑어 작업 사본과 패키지 캐시 밖을 가리키는 링크(`file:`·`link:` 의존성, junction 포함)를 지우고 `dropped_inputs`(사유 `link_outside_workspace`, 최대 64)와 한계 `links_outside_workspace_removed`로 남긴다. 나머지 설치는 그대로 쓴다. 실패·중단·drift인 설치는 트리를 지우고 `installed_dirs`를 남기지 않는다. 트리는 attempt 실행 사본과 함께 지워진다.
- **분석기.** tsc·type-aware ESLint는 기록이 `prepared`·`partial`이고 `installed_dirs`가 있으면 그 디렉터리를 받아들이고 `type_check_without_dependencies` 대신 `dependencies_prepared`(`partial`이면 `dependencies_partially_prepared`)를 남긴다. 기록에 없는 `node_modules`나 링크인 `node_modules`는 전과 같이 거부한다. gitleaks는 설치 트리를 건너뛰고(pnpm 배치의 링크 때문에 걸어 다니기 검사가 실패하지 않도록) jscpd·lizard는 manifest 파일만 읽으므로 설치 트리를 세지 않는다.
- **사유.** `lockfile_outdated`(frozen lockfile이 package.json과 맞지 않음), `lockfile_version_unsupported`, `lockfile_format_unsupported`, `manifest_invalid`, `dependency_not_found`(레지스트리에 없음, `failed_items`에 이름), `registry_auth_required`, `registry_unreachable`, `local_dependency_outside_project`(`skipped_policy`), `snapshot_integrity_failed`, `workspace_drift`, `toolchain_unavailable`(버전을 읽을 수 없음), `package_manager_not_configured`(`--node`나 관리자 스크립트 없음, corepack shim)와 공통 사유.
- **설치 패키지의 쓰임.** 설치한 패키지는 타입 검사기(tsc·type-aware ESLint)만 읽는다. 스냅샷의 일부가 아니므로 그 파일 내용은 보고서의 코드 발췌나 모델 입력에 들어가지 않는다(진단 메시지가 의존성의 타입 이름을 담을 수는 있다). 프로젝트 밖을 가리키는 `file:` 의존성은 위 사전 검사가 막고, 링크 정리가 둘째 방어선이다.

### Python 의존성 받기

`deps.python`·`deps.python.mNN` task가 mypy·pyright와 같은 모듈 계획으로 모듈마다 제3자 패키지를 wheel로만 받아 트리 하나에 설치한다([의존성 준비](ARCHITECTURE.md#의존성-준비), 결정 A44). mypy나 pyright가 이 노드에서 쓸 수 없으면 `no_dependency_consumer`로 건너뛴다. 저장소 코드(`setup.py`, build backend)는 실행하지 않는다.

**읽는 선언.** 모듈 디렉터리의 다음을 읽어 CODEYE의 요구사항 파일을 새로 쓴다. 저장소의 파일을 설치 도구에 그대로 넘기지 않는다.

| 선언 | 읽는 방법 |
|---|---|
| `uv.lock` | `--uv`가 있을 때만. `uv export --frozen --offline --no-config --all-extras --all-groups --no-emit-project --no-emit-workspace --no-emit-local --format requirements-txt`로 꺼낸다(네트워크·프로젝트 수정 없음). 있으면 아래 선언은 읽지 않는다. uv가 없으면 `uv_not_configured`로 빼고 아래를 읽는다 |
| `pyproject.toml` | `[project] dependencies`, 모든 `optional-dependencies`, PEP 735 `dependency-groups`(`include-group` 펼침, group마다 한 번, 요구사항 10,000개 상한). 운영자 interpreter를 `-I -S`로 실행해 표준 라이브러리 `tomllib`로 읽고 파일은 stdin으로 준다(3.11 이상) |
| `requirements*.txt` | 모듈 디렉터리의 `requirements*.txt`. `-r`·`-c`는 snapshot 안 파일만 따라간다 |

받지 않는 입력은 기록의 `dropped_inputs`에 사유와 함께 남는다(최대 100개): `editable_requirement_not_fetched`(`-e`), `local_requirement_not_fetched`(로컬 경로), `vcs_requirement_not_fetched`(`git+` 등), `installer_option_ignored`(`--index-url`·`--extra-index-url`·`--find-links`·`--trusted-host` 등 저장소가 적은 설치 옵션), `requirement_not_understood`, `included_file_not_in_snapshot`, `uv_not_configured`, `poetry_dependencies_not_read`, `lockfile_format_unsupported`(`poetry.lock`, `Pipfile`, `Pipfile.lock`, `pdm.lock`). 요구사항 줄은 허용 목록(이름, extras, PEP 440 지정자, marker, `--hash`, `name @ https://...`)으로만 다시 쓴다. 저장소가 적은 index 옵션을 따르지 않으므로 index는 운영자의 `--pip-index-url`(없으면 PyPI)뿐이다.

**설치.** 빈 task 작업 디렉터리(`<scratch>/deps/<key>/work`)에서 실행한다.

| 도구 | argv |
|---|---|
| uv | `uv pip install --target <트리>/lib/python3.N/site-packages --python <--python> --no-build --no-config --no-progress --color never --cache-dir <캐시>/python/uv -r <CODEYE 요구사항> [-c <제약>] [--index-url URL]`, 환경 `UV_PYTHON_DOWNLOADS=never` |
| pip | `<--python> -I -m pip install --target ... --only-binary=:all: --isolated --no-input --disable-pip-version-check --no-warn-script-location --progress-bar off --no-color --retries 2 --timeout 30 --cache-dir <캐시>/python/pip -r ... [-c ...] [--index-url URL]`, 환경 `PYTHONNOUSERSITE=1` |

- uv가 있으면 uv, 없으면 `--python`의 pip이다. 어느 쪽이든 `--python`이 필요하다(트리는 그 interpreter 버전으로 풀린다). 없으면 `unsupported`/`package_manager_not_configured`다.
- wheel이 없거나 index에 없는 요구사항은 빼고 다시 설치한다(최대 3번 더). 전이 의존성이 원인이면 그것을 끌어온 직접 요구사항을 뺀다. 뺀 것은 `failed_items`에 남고 기록은 `partial`이다.
- 설치가 끝나면 site-packages 맨 위의 `*.pth`, `sitecustomize.py`, `usercustomize.py`를 지운다(한계 `interpreter_startup_hooks_removed`). 받은 트리를 읽는 interpreter가 그 코드를 실행하지 않게 하기 위해서다.
- 일부 요구사항에만 `--hash`가 있으면 설치 도구가 섞인 목록을 거부하므로 hash를 모두 떼고 `requirement_hashes_not_checked`를 남긴다. 모두 있으면 그대로 넘겨 설치 도구가 확인한다.
- 크기 상한은 내려받기 캐시, 만드는 트리, 설치 도구가 푸는 임시 디렉터리를 함께 잰다.

**캐시.** `uv.lock`에서 왔거나 모든 줄이 `==`(와일드카드 없음)이면 고정(`pinned=true`)이다. 고정이면 트리를 공유 캐시 `python/trees/<sha256 앞 32자>`에 둔다. key는 요구사항·제약·interpreter 버전·설치 도구와 버전·index다. 같은 key의 트리가 있으면 다시 받지 않는다(한계 `prepared_tree_reused`). 고정이 아니면 attempt 트리(`<scratch>/deps/<key>/tree`, 기록 cache `attempt`)이고 attempt scratch와 함께 지워지며 `dependency_versions_not_pinned` 공백이 남는다.

**분석기 연결.** 같은 snapshot·attempt·모듈의 기록이 `prepared`·`partial`이고 트리가 있으면 쓴다.

- pyright: CODEYE `pyrightconfig.json`의 `venvPath`/`venv`가 빈 venv 대신 받은 트리를 가리킨다.
- mypy: `--no-site-packages` 대신 `--python-executable=<scratch 가짜 venv>/bin/python`이다. 가짜 venv는 `pyvenv.cfg`, 운영자 `--python`을 가리키는 `bin/python` symlink, 받은 site-packages를 가리키는 symlink다. `rule_pack.selection`에는 `--python-executable=codeye-prepared`로 남는다. Windows는 가짜 venv를 만들지 않아 mypy는 패키지 없이 검사하고 `prepared_packages_not_used`를 남긴다.
- 트리 경로는 메시지에서 `$DEPS`로 가리고 보호 경로다.

**사유.** `no_binary_distribution`(uv: wheel 없음), `no_matching_distribution`(pip: wheel 없음과 이름 없음을 구분하지 않는다), `dependency_not_found`, `resolution_conflict`, `registry_auth_required`, `hash_mismatch`, `registry_unreachable`, `dynamic_dependencies_not_read`(`dynamic`에 dependencies가 있음, `unsupported`), `lockfile_format_unsupported`(`unsupported`), `pyproject_unreadable`, `too_many_requirements`, `lockfile_export_failed`, `workspace_drift`, `snapshot_integrity_failed`와 공통 사유. 받을 것이 없으면 `no_dependencies`다.

### 의존성 받기의 한계

- 공통: 인증이 필요한 레지스트리·index·proxy는 지원하지 않는다. 받는 주소가 운영자 설정 밖으로 나가지 않는다는 것은 도구 설정에 기대며 OS 수준으로 막지 않는다.
- Rust: 공유 `CARGO_HOME`은 cargo check·clippy가 build script를 실행하는 동안 읽기 전용으로 보호되지 않는다. executor의 보호 root는 cwd·실행 파일 위치만 검사하고 쓰기를 막지 않으므로, 한 저장소의 build script가 다른 저장소가 읽을 crate 소스를 바꿀 수 있다. 저장소 코드 실행 허가는 그 위험까지 받아들이는 선택이다.
- JS/TS: 사용자 npm 설정의 레지스트리에 인증정보가 있으면 쓰지 않는다. `HTTP_PROXY` 같은 환경변수 proxy는 넘기지 않는다. yarn 1은 proxy를 지우는 명령줄 값이 없다(`.yarnrc`가 스냅샷에 없으므로 저장소가 정할 수는 없다). git 의존성은 PATH에 git이 없어 실패한다. lockfile이 루트를 맡지만 workspace 멤버가 아닌 하위 패키지(예: `examples/*`)는 설치되지 않아 그 모듈의 tsc는 공백으로 남는다. pnpm `configDependencies`는 pnpm이 설치 전에 받는다(스크립트는 실행하지 않는다).
- Python: 트리는 `--python`의 버전·플랫폼으로 풀린다. 분석 대상 버전(예: `requires-python` 하한 3.10)과 다르면 marker로 갈리는 패키지가 다를 수 있다. 사용자 pip 설정(`pip.conf`)과 proxy 환경변수는 넘기지 않는다(`--isolated`, 고정 환경).

## Go 팩

go vet은 기준 정적 분석기, Staticcheck는 심화 분석기, gofmt는 파일 형식 검사기다. 기존 Go 테스트 실행은 `with-tests`에서 별도로 수행한다. [기존 테스트 실행](EXECUTION.md#기존-테스트-실행)

- go vet과 Staticcheck는 테스트 파일을 포함한 패키지를 읽고 타입 검사한다. `-vettool`·`-toolexec`·`-exec`와 `go generate`는 사용하지 않는다.
- `CGO_ENABLED=0`이므로 cgo 파일을 빌드하지 않고 C 컴파일러·`#cgo`·pkg-config를 호출하지 않는다.
- go.mod의 `go` 줄은 언어 판이다. 일부 analyzer의 대상이 바뀌지만 프로그램 의미의 일부로 본다.

go vet은 패키지 목록을 바탕으로 [Go 내부 패키지 의존 관계](#go-내부-패키지-의존-관계)를 함께 남긴다. 문서에 나온 HTTP 처리 경로와 호출·테스트의 연결은 [모델 검토](REVIEW.md#go-http-사용자-동작)에서 다룬다.

### 모듈과 계획

`Plan`은 manifest만 읽는다. 포함된 `go.mod`마다 task 하나를 만든다. `vendor`, `testdata`, `.`·`_`로 시작하는 디렉터리는 제외한다. scope `paths`는 모듈별 `./dir/...` 또는 단일 패키지 `./dir` 패턴이 된다. Staticcheck는 go vet과 같은 모듈 순서로 task를 만든다. 범위에 Go 소스가 없으면 아무것도 계획하지 않는다. Go 소스는 있는데 모듈이 없으면 go vet이 `go-vet` 자리 작업 하나를 `unsupported`(`analysis_unsupported`, 한계 `go_module_missing` 등)로 계획한다.

### Go 형식 비교

`gofmt -l -- ./<파일>`을 분석 작업 사본에서 실행한다. `--go` 실행 파일을 해석한 위치의 형제 실행 파일만 사용하며, 빌드 정보가 `cmd/gofmt`인지 확인하고 기록된 Go 버전을 보고서에 남긴다. 운영자 PATH, 저장소 서식 설정, 외부 다운로드를 사용하지 않는다. 모듈 읽기·패키지 로드·타입 검사 없이 선택된 `.go` 파일을 비교하며, 모듈 밖 파일과 빌드 조건·테스트가 붙은 파일도 비교할 수 있다. 일반 분석 팩의 데이터 디렉터리 제외를 적용한다.

- 파일당 최대 2 MiB, 합계 최대 64 MiB에서 상대 경로 argv를 12 KiB씩 최대 64회로 나눠 넘긴다. 전체 실행 제한시간은 2분이다. 입력 한도로 빠진 파일은 `format_input_limit`와 대상·처리 파일 수로 확인할 수 있다.
- 쓰기 옵션을 사용하지 않는다. 종료 코드 0에서 stdout 목록은 서식이 다른 파일이다. 항목마다 파일 단위 진단과 첫 줄 위치를 제공한다. 구문·읽기 오류, 잘린 출력, 목록에 없던 경로, 작업 사본 변경이 있으면 작업은 실패하고 전체 처리 완료를 주장하지 않는다.
- 실행 파일을 읽을 수 없거나 Go 빌드 정보를 확인할 수 없으면 실행하지 않고 도구 사유를 남긴다. Go 1.27.4의 Linux formatter로 서식 일치·차이·구문 오류를 확인했다. [실제 서식 차이 관찰](../examples/observations-gofmt.json)

### Go 공통 환경과 빌드 문맥

| 변수 | 값 |
|---|---|
| `PATH`, `LANG` | go 바이너리 디렉터리, `C.UTF-8`(Windows는 `SystemRoot` 추가) |
| `GOENV=off`, `GOWORK=off`, `GOFLAGS=`(빈 값) | 저장소·사용자 설정이 명령 옵션을 바꾸지 못함 |
| `GOTOOLCHAIN=local` | go.mod의 `toolchain`·`go` 줄이 다른 toolchain을 내려받거나 실행하게 하지 못함 |
| `GOPROXY=off`, `GOSUMDB=off`, `GOVCS=*:off` | 모듈 다운로드·VCS 명령 없음 |
| `CGO_ENABLED=0` | cgo 끔 |
| `GOCACHE`, `GOPATH`, `STATICCHECK_CACHE` | task scratch |
| `GOMODCACHE` | 이 attempt의 `deps.go` 기록이 쓸 수 있으면 받기 캐시(보호 경로), 아니면 `--go-mod-cache`(읽기 전용 보호) 또는 빈 scratch 캐시 |
| `XDG_CONFIG_HOME` | scratch의 `config`. 그 안 `go/telemetry/mode`에 `off`를 쓴다 |

- **Go telemetry를 끈다.** mode 파일이 없으면 go 명령은 사용자 설정 디렉터리에 counter를 쓰고 분리된(setsid) upload 자식을 시작한다. 그 자식은 정리 확인 뒤에도 실행별 디렉터리에 쓴다. `GOTELEMETRY=off` 환경변수는 효과가 없다. 그래서 go vet·Staticcheck 실행과 probe, env.discover의 go probe는 mode 파일로 끈다(`govet.DisableTelemetry`). darwin은 `XDG_CONFIG_HOME`을 읽지 않아 끌 수단이 없고, Windows는 executor 환경에 `AppData`가 없어 telemetry가 꺼진다(소스 확인).
- **빌드 문맥은 하나다.** 노드 기본 GOOS/GOARCH, 사용자 build tag 없음, CGO 끔. `GOFLAGS`를 비워 Go 기본값(vendor 디렉터리가 없으면 `-mod=readonly`, 있으면 `-mod=vendor`)을 따른다. `-mod=readonly`를 강제하면 vendor 모듈 프로젝트가 실패한다. 이 값은 `target.properties`(goos, goarch, cgo_enabled, build_tags, go_flags, toolchain, tests)와 제약 `default_build_context_only`·`cgo_disabled`로 항상 기록한다. 다른 tag·CGO·target 조합은 계획하지 않는다. build constraint로 빠진 파일은 `files_skipped`와 `build_constraint_ignored_files`로 보인다.
- **go.mod 검사.** 실행 전에 go.mod hash를 manifest와 대조한다(다르면 `snapshot_integrity_failed`). `replace` 판정은 `go mod edit -json` 결과로 한다. 마스킹 표지(`[REDACTED]`)가 든 대상과 절대 경로·스냅샷 밖 디렉터리 `replace`는 `skipped_policy`(`replace_outside_snapshot`)로 끝내고 `go list`·분석을 실행하지 않는다. 스냅샷 안 `replace`는 허용한다.
- 운영자 모듈 캐시의 무결성은 go.sum 대조에 의존하고 `GOSUMDB=off`다. 원시 stderr에는 모듈 캐시나 GOROOT 절대 경로가 남을 수 있으므로 원시 bytes를 렌더링하지 않는다.

### go vet

| 실행 이름 | argv | 용도 |
|---|---|---|
| `go.version` | `go version` | 도구 버전·플랫폼 |
| `go.modedit` | `go mod edit -json` | module path와 디렉터리 `replace` |
| `go.list` | `go list -e -json=Dir,ImportPath,Name,GoFiles,CgoFiles,IgnoredGoFiles,TestGoFiles,XTestGoFiles,Incomplete,Error,DepsErrors <patterns>` | 패키지·파일 inventory |
| `go.vet` | `go vet -json <patterns>` | 진단 |

앞 단계가 취소·timeout·미지원·거부·정리 불확실로 끝나면 뒤 단계를 실행하지 않는다. scratch는 `<Scratch>/go-vet/<task>/{exec/, observations.json, gocache/, gopath/, tmp/, config/}`다.

출력 사실(go1.27): `go vet -json`은 진단이 있어도 exit 0이므로 진단 유무는 stdout parser가 정한다. stdout은 단위마다 최상위 JSON 객체가 이어 붙고 진단 없는 단위도 `{}`를 낸다. 패키지 출력 순서는 실행마다 다르다. 타입 오류 패키지는 stdout에 아무것도 내지 않고 stderr `vet: <path>:L:C: msg`와 exit 1이다. JSON은 경로의 역슬래시와 `&`·`<`·`>`를 escape하므로 escape 형태까지 마스킹한다.

성공은 네 실행이 모두 exit 0·정리 확인·잘림 없음, go.mod를 읽음·스냅샷 밖 `replace` 없음, drift 없음, vet stdout을 남김없이 해석, analyzer·타입·의존·toolchain·모듈 오류 없음, `go list` 오류·불완전 패키지 없음, 분석 패키지 1개 이상일 때다. stderr의 `go:` 경고 줄은 기록하지만 그것만으로 실패하지 않는다. 여러 조건에 걸리면 먼저 나오는 사유를 쓴다.

| 우선 | 조건 | 상태 / reason | exit class |
|---|---|---|---|
| 1 | 실행 하나라도 정리 `uncertain` | failed / `cleanup_uncertain` | execution_error |
| 2 | 취소 | cancelled / `cancelled` | cancelled |
| 3 | timeout | timed_out / `timeout` | timeout |
| 4 | executor `unsupported`·`start_failed`·`rejected` | unsupported / `execution_unsupported`, failed / `tool_start_failed`·`execution_rejected` | not_run |
| 5 | `go version` 해석 불가 | failed / `tool_version_unrecognized` | configuration_error |
| 6 | `go mod edit -json` 실패 | failed / `go_mod_unreadable` | configuration_error |
| 7 | 스냅샷 밖 디렉터리 `replace` | skipped_policy / `replace_outside_snapshot` | not_run |
| 8 | 단계 미실행 | failed / `vet_not_run` | not_run |
| 9 | workspace drift | failed / `workspace_drift` | execution_error |
| 10 | toolchain 부족 · 모듈 없음 · 의존 모듈 없음 | failed / `toolchain_unavailable`·`go_module_missing`·`dependency_unavailable` | configuration_error |
| 11 | `matched no packages` | unsupported / `no_analyzable_packages` | configuration_error |
| 12 | 출력 잘림·불완전 | failed / `output_truncated` | execution_error |
| 13 | JSON 손상·형태 오류 | failed / `output_unparsed` | execution_error |
| 14 | 패키지 타입 오류 | failed / `package_errors` | execution_error |
| 15 | vet 기타 비정상 exit · list 오류 · analyzer 오류 | failed / `vet_failed`·`package_load_error`·`analyzer_error` | execution_error |
| 16 | vet exit 0이지만 분석 패키지 0개 | unsupported / `no_analyzable_packages` | configuration_error |

`partial_parse`는 실패했는데 vet stdout을 일부 읽은 경우에만 true다. 오프라인에서 의존 모듈이 없으면 내려받지 않고 `dependency_unavailable`이며 빈 scratch 모듈 캐시에 파일이 생기지 않는다.

**관찰 문서 `go_vet_observations` v0.1.** go vet은 [전용 schema](../schemas/go-vet-observations.schema.json)의 문서를 바이트 고정으로 쓴다([예시](../examples/observations-go-vet.json)). 형식을 바꾸지 않으며 보고서 decoder가 규칙 문맥을 합성한다.

- `rule_id`는 analyzer 이름이고 `raw_severity`는 항상 null이다. 메시지의 workspace 접두는 상대 경로로, GOROOT·모듈 캐시·scratch·스냅샷 경로는 `$GOROOT` 같은 이름으로 바꾼다.
- ID는 `obs_` + sha256(도구, package key, rule, 정규화 위치, 메시지, 같은 튜플 순번) 앞 20자다. `raw_ref`는 `go.vet` stdout의 sha256, 최상위 값 순번, RFC 6901 JSON pointer, byte 범위다. `stream_index`는 ID에 넣지 않는다.
- 패키지 상태는 `analyzed_clean`, `analyzed_with_findings`, `failed_typecheck`, `dependency_unavailable`, `load_error`, `not_analyzed`, `unknown`이다. vet이 완전히 성공했을 때만 진단 없는 패키지가 `analyzed_clean`이고, 실패한 실행의 진단 없는 패키지는 `unknown`이다. manifest에는 있지만 `go list`가 보고하지 않은 Go 파일은 `not_analyzed`(`not_listed_by_go_command`)다. `processed_go_files`는 분석 패키지의 파일 수이고 `unknown`이 하나라도 있으면 null이다.
- 한계 코드는 항상 `default_build_context_only`·`cgo_disabled`이고, 조건에 따라 `build_constraint_ignored_files`, `unlisted_go_files`, `no_offline_module_cache`, `go_work_ignored`, `vendor_directory_used`, `tool_version_untested`, `workspace_drift`, `unattributed_vet_package_key`, `local_replace_outside_snapshot`을 더한다. 받기 캐시를 쓰면 `no_offline_module_cache` 대신 `dependencies_prepared` 또는 `dependencies_partially_prepared`다(Staticcheck도 같다).
- 보고서는 실행 이름과 stdout sha256으로 result manifest의 `executions[].name`과 artifact에 다시 연결하고, 스냅샷은 `snapshot_manifest_digest`로 식별한다.

go vet은 composite 안에서도 계획(key·제목·필수·단계), 실행, 문서 바이트가 단독 `govet.Handler`와 같다. 규칙 문맥은 `codeye-go-vet-default` v1(selection `[]`, `repository_config=none`, `source_suppressions=none`)로 합성한다. go vet의 분석기 집합은 toolchain 버전이 정하고 저장소 설정과 억제 문법이 없기 때문이다.

### Staticcheck

작업 사본의 모듈 디렉터리에서 차례로 실행한다.

1. `go version` — toolchain 식별(실패·미인식 시 중단)
2. `staticcheck -version` — 도구 식별(실패·미인식 시 중단)
3. `go mod edit -json` — 모듈 경로와 replace. 스냅샷 밖 filesystem replace는 `go list` 전에 거부(`skipped_policy`, `replace_outside_snapshot`)
4. `go list -e -json=<필드> ./...` — 같은 build context의 패키지 목록(처리·무시 파일 결정)
5. `staticcheck -f json -checks SA*,U1000,-SA1014,-SA1017,-SA2000,-SA2002,-SA4021,-SA5009 -tests=true -show-ignored ./...`

**규칙 팩 `codeye-go-staticcheck` v2.** SA(정확성·동시성·테스트·죽은 코드·성능)와 U1000(미사용 코드)을 실행하고 S1·ST는 실행하지 않는다. 제외한 SA 검사는 go vet 중복이다. `-checks`가 저장소 `staticcheck.conf`의 checks를 대체하므로 `repository_config=overridden`, `config_files=[]`다. 범주는 SA1019·U1000 → maintainability, SA2 → concurrency, SA3 → test_gap, SA6 → performance, 나머지 → correctness다. 원문 severity(`error`)는 그대로 둔다.

**소스 안 억제(`-show-ignored`, `reported`).**

- `//lint:ignore`·`//lint:file-ignore`로 숨긴 문제는 severity `ignored` 레코드로 나오고 `suppressed=true` 관찰이 된다. 제약 `source_suppressed_observations`를 남긴다.
- 숨긴 문제만 있어도 exit 1이므로 exit class는 억제되지 않은 관찰로 정한다.
- 이유가 빠진 잘못된 지시문은 `compile` 레코드(`malformed linter directive`)로 `package_errors` 실패다. 아무것도 숨기지 않은 지시문은 규칙 `staticcheck`의 일반 관찰이 된다.
- **U1000은 억제를 보고하지 않는다.** 선언 위의 `//lint:ignore U1000`·`//lint:file-ignore U1000`, 생성 파일 표지(`// Code generated … DO NOT EDIT.`)는 `-show-ignored`에서도 기록을 남기지 않는다. 그래서 `unreported_suppression_rules=["U1000"]`이고 사라진 U1000 finding은 `source_suppressions_hidden`이다. 생성 파일 표지가 있어도 SA 검사는 보고된다.

**버전 계약.** `staticcheck -version` 출력 `staticcheck 2026.2.2 (0.8.2)`에서 `2026.2.2 (0.8.2)`를 읽는다. 개발 빌드(`staticcheck (devel, …)`)나 형식 불일치는 check 전에 `failed`(`tool_version_unrecognized`)이고 시작 probe에서 거부되면 대역이 `tool_unavailable`이다. JSON parser는 2026.2 레코드 형식에 고정했다.

**종료 코드와 결과.** `-fail` 기본값에서 문제가 하나라도 있으면 exit 1, 없으면 0, 사용법 오류·crash는 2다. compile 레코드와 `err:` 치명 오류도 exit 1이므로 stdout·stderr parser가 결과를 정한다.

| 관측 | 결과 |
|---|---|
| exit 0, 레코드 없음, stderr 없음 | succeeded / `success_without_findings` |
| exit 1, 검사 레코드만, stderr 정상 | succeeded / `success_with_findings` |
| exit 1, `ignored` 레코드만 | succeeded / `success_without_findings`(관찰은 모두 `suppressed`) |
| exit 0 + stderr `matched no packages` | unsupported / `no_analyzable_packages` |
| exit 1 + `compile` 레코드(타입 오류) | failed / `package_errors`, 다른 패키지 관찰 보존, 처리 파일 모름 |
| 오프라인 의존 누락, toolchain 부족, go.mod 모듈 누락 | failed / `dependency_unavailable`·`toolchain_unavailable`·`go_module_missing`(configuration_error) |
| exit 1 + `err:`만, exit 2, signal | failed / `staticcheck_failed` |
| exit 0인데 레코드 있음 | failed / `exit_status_mismatch` |
| stdout 잘림·손상 | failed / `output_truncated`·`output_unparsed` |
| timeout·취소·정리 불확실 | timed_out, cancelled, failed/`cleanup_uncertain` |

### go vet과 Staticcheck 중복

Staticcheck는 go vet을 실행하지 않는 별도 엔진이다. 같은 구성을 보고하는 다음 검사는 선택에서 뺀다.

| 제외한 Staticcheck 검사 | 같은 구성을 보고하는 go vet analyzer |
|---|---|
| SA1014 | unmarshal |
| SA1017 | sigchanyzer |
| SA2000 | waitgroup |
| SA2002 | testinggoroutine |
| SA4021 | appends |
| SA5009 | printf |

SA4000(동일 피연산자)은 go vet `bools`보다 넓어 유지한다.

## C/C++ 팩

cppcheck가 기준 분석기이고 clang-tidy와 GCC가 추가 검사를 수행한다. Clang Static Analyzer(CSA)는 clang-tidy의 `clang-analyzer-*`로 실행한다. 세 작업 모두 `RepositoryCode=false`다. compile DB가 없는 CMake 프로젝트의 configure는 분석기 작업이 아니라 그 앞의 [compile DB 생성](#compile-db-생성) 작업이다.

- **기준이 cppcheck인 이유.** cppcheck는 compiler가 아니어서 모르는 헤더·매크로를 견디며 compile DB가 없어도 실행할 수 있는 유일한 C/C++ 도구다. 단일 실행 파일과 자체 library 설정으로 동작해 대상 system header와 맞출 필요가 없다. clang-tidy는 build flag가 필요한데 C/C++ 저장소는 대개 compile DB를 커밋하지 않는다. `--clang-tidy`만 있는 노드는 deep-review를 광고하지 않는다.
- **계획.** 범위에 C/C++ 파일이 하나라도 있으면 분석기마다 작업 하나(`cppcheck`, `clang-tidy`)를 계획한다. 헤더만 있으면 그 작업은 `unsupported`(`no_translation_units`)다. GCC는 범위에 `.c` 번역 단위가 있을 때 계획한다.
- **저장소 코드를 실행하지 않는 근거.** cppcheck는 소스를 읽어 분석할 뿐 compile·link·실행하지 않고 CODEYE가 다시 쓴 compile DB를 데이터로 읽는다. library 설정은 설치본의 것이다. `--inline-suppr`·`--suppressions-list`·`--addon`·`--rule-file`·`--cppcheck-build-dir`은 쓰지 않는다. clang-tidy는 frontend parse와 분석만 하며 plugin·`-load`·`-fplugin`·응답 파일·driver 설정은 허용 목록이 막고, 저장소 `.clang-tidy`는 `--config`가 대체하며, `--load`와 캐시는 쓰지 않는다. 두 도구는 자식 프로세스를 만들지 않고 파일을 쓰지 않는다.

### compile DB 매핑

스냅샷의 `compile_commands.json`은 원래 build 머신의 데이터다. 어댑터는 이를 작업 사본에 맞게 다시 써 task scratch(`<scratch>/work/compile_commands.json`)에 두고 도구는 그것만 읽는다. 항목의 compiler는 실행하지 않으며 driver 이름은 언어 모드만 정한다. GCC는 별도 DB 파일 없이 같은 매핑에서 허용된 인자만 가져온다.

- **원래 루트 추정.** 항목 파일 절대 경로의 접두어 중 스냅샷에 실제로 있는 파일에 가장 많이 맞는 것을 고른다. 동률이면 더 긴 접미어가 맞는 쪽이다. 추정한 루트는 다른 머신의 절대 경로이므로 기록하지 않는다. 추정하지 못하면 `compile_db_root_not_inferred`다.
- **경로 변환.** `directory`·`file`·`-I`·`-isystem`·`-iquote`·`-idirafter`·`-include`·`-imacros`를 그 루트 아래 스냅샷 경로로 바꾼다. 상대 경로는 항목 `directory` 기준이다. 원래 build 디렉터리(예: `_build/gen`)도 같은 규칙으로 작업 사본 경로가 된다. 없으면 `include_dirs_not_in_snapshot`, 강제 include 파일이 없으면 `forced_include_files_not_in_snapshot`다. 루트 밖 include 경로(system 경로 포함)는 버린다(`include_paths_outside_snapshot`). 도구는 자기 system header를 쓴다. Windows 경로의 `command`는 CommandLineToArgvW 규칙으로 나눈다.
- include 검색 디렉터리가 추정한 프로젝트 루트이면 `-I.`나 `-iquote .`도 작업 사본 루트의 절대 경로로 바꾼다. 루트를 가리키는 `.`은 디렉터리 인자에만 쓴다. 빈 include 인자와 제어 문자가 섞인 경로는 정규화 전에 버린다.
- **제한.** JSON이 아니거나 64 MiB, 100,000 항목, 항목당 4,096 인자를 넘으면 `compile_db_unreadable`로 실패한다. 둘 이상의 compile DB가 있으면 얕은 경로부터 읽고 `multiple_compile_dbs`를 남긴다.

| 항목 제외 사유 | 경우 |
|---|---|
| `entry_malformed` | `directory`·`file`·`command`/`arguments` 누락·타입 오류, 닫히지 않은 따옴표 |
| `directory_not_absolute` | `directory`가 절대 경로가 아님 |
| `file_outside_snapshot_root` | 파일이 추정 루트 밖 |
| `file_not_in_snapshot` | 루트 안이지만 스냅샷에 없음 |
| `data_directory` | 숨은·`testdata`·`vendor`·`node_modules` 아래 |
| `header_entry` | 헤더가 주 파일 |
| `not_cpp_source` | C/C++ 확장자가 아님 |
| `out_of_scope` | 작업 범위 밖 |
| `duplicate_entry` | 같은 파일의 두 번째 이후 항목(첫 항목만 씀) |
| `msvc_driver_unsupported`, `unsupported_driver` | `cl`·`clang-cl`, `nvcc`·형식이 맞지 않는 driver |
| `unsafe_argument` | 허용 목록을 지난 뒤에도 안전하지 않은 인자가 남은 항목 |

제외 수는 `target.properties.entries_excluded`와 제약 `compile_db_entries_excluded`·`duplicate_compile_db_entries`로 남는다. 범위 안 소스의 모든 항목이 제외되면 그 단위는 `compile_db_entry_excluded`, 항목이 없으면 `not_in_compile_db`이며 처리 파일이 아니다. compile DB에 없는 소스에 가까운 항목의 인자를 빌려 주지 않는다(clangd 식 보간). 그런 소스는 구성한 build가 컴파일하지 않는 파일(꺼진 build 옵션의 소스, 시험 framework에 딸린 예제)이다. 빌린 인자로 시험 경로의 소스는 헤더를 찾지 못해 대부분 컴파일되지 않았고, 배포 코드 경로의 소스가 낸 진단은 정밀도의 근거가 없다. [A68](DECISIONS.md)

**flag 허용 목록.** 분석 의미를 바꾸는 인자만 남긴다.

- `-D`·`-U`(이름 형식 검사, 값 1,024바이트 이하), `-std=`(형식 검사), `-x`(`c`·`c++`·`c-header`·`c++-header`만), `--target=`(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`, 붙여서 출력), `-stdlib=`(`libc++`·`libstdc++`·`platform`만), `-O` 수준 하나
- 언어 방언 `-f` 옵션(`-fno-exceptions`, `-fno-rtti`, `-fms-extensions`, `-funsigned-char` 등), `-m` target 옵션, `-ansi`·`-pthread`·`-nostdinc`·`-nostdinc++`, 매핑한 include 경로

그 밖은 버리고 범주별로 센다(`target.properties.flags_dropped`).

| 범주 | 예 | 제약 |
|---|---|---|
| `code_loading` | `-fplugin=`, `-fpass-plugin=`, `-Xclang -load`, `-Xanalyzer`, `-mllvm` | `code_loading_flags_dropped` |
| `response_file` | `@flags.rsp` | `response_files_dropped` |
| `driver_config`, `driver_mode`, `toolchain_path` | `--config=`, `--driver-mode=`, `-B`, `--sysroot`, `--gcc-toolchain`, `-resource-dir` | `compile_flags_dropped` |
| `modules`, `instrumentation`, `precompiled_header`, `backend_option`, `positional_argument`, `not_allowlisted` | `-fmodules`, `-fsanitize=`, `-include-pch`, `-Wa,` | `compile_flags_dropped` |
| 구조(세지만 보고하지 않음) | `-c`, `-o`, `-MD`, `-MF`, `-g`, `-W…`, 링커 인자 | — |

- 남기는 값은 고정 집합이나 엄격한 형식만 통과한다. `@`로 시작하는 원소는 어느 위치에도 남지 않는다(clang은 `@` 원소를 응답 파일로 펼친다). 그래도 안전하지 않은 인자가 남으면 그 항목은 `unsafe_argument`로 제외되고, 렌더링은 driver 다음 모든 원소가 옵션·절대 경로·언어 값인지 다시 검사한다.
- compiler wrapper(`ccache`, `sccache`, `distcc`, `icecc`, `buildcache`, `colorgcc`)는 argv[0]에서 벗긴다(`compiler_wrappers_stripped`). 출력 driver는 C++이면 `clang++`, 아니면 `clang`이다. driver 이름에 `++`가 있거나 `-x c++`이거나 확장자가 C++이면 C++이다. 저장소 driver 경로는 쓰지 않고 basename만 기록한다(`drivers`).
- 주 파일은 옵션 검사보다 먼저 알아본다. 그래서 `-x.c`, `--help.cpp` 같은 파일 이름도 파일로 분석한다. 주 파일, include 경로, `-p`, `--project=`, `--file-list=`와 목록의 각 줄은 모두 절대 경로다.

**저장소가 도구를 바꾸는 경로 차단.**

| 경로 | 막는 방법 |
|---|---|
| cppcheck가 cwd의 `std.cfg`·`posix.cfg` 등 library 설정을 읽음 | cwd는 task scratch의 빈 디렉터리 `cwd` |
| clang driver가 driver 경로 디렉터리의 `<triple>-<driver>.cfg`·`<driver>.cfg`를 읽음 | `--extra-arg-before=--no-default-config`, 출력 driver는 basename |
| 저장소 `.clang-tidy`(`Checks`, `WarningsAsErrors`, `HeaderFilterRegex`, `ExtraArgs`, `CheckOptions`) | inline `--config`로 모든 키 고정 |
| `CPATH`·`C_INCLUDE_PATH`·`CCC_OVERRIDE_OPTIONS` 등 환경변수 | `PATH`(도구 디렉터리)·`LANG=C.UTF-8`만 전달 |

### compile DB 생성

스냅샷에 `compile_commands.json`이 없고 CMake 프로젝트가 있으면, C/C++ 분석기 앞에 생성 작업 `compile-db`(단계 `build`, 판정 필수 아님) 하나를 계획한다(`cpp.WrapGeneration`). 순서는 탐색 → 의존성 받기 → `compile-db` → 분석기다. configure는 저장소의 `CMakeLists.txt`와 그 안의 `try_compile`·`try_run`·`execute_process`를 실행하므로 [저장소 코드 실행 허가](#저장소-코드-실행-허가)가 있어야 실행한다.

- **계획 조건.** 쓸 수 있는 C/C++ 분석기(cppcheck, clang-tidy, GCC 중 하나)가 있고, 정적 계획에 그 작업이 있고, 범위에 번역 단위가 있고, 스냅샷에 compile DB가 없어야 한다. 프로젝트는 상위 디렉터리에 `CMakeLists.txt`가 없는 가장 바깥 `CMakeLists.txt` 디렉터리 중 범위 안 번역 단위를 가진 것이다. 데이터 디렉터리(숨은·`testdata`·`vendor`·`node_modules`) 아래는 프로젝트가 아니다. 얕은 경로부터 최대 8개다. manifest와 범위만으로 정한다.
- **허가가 없을 때.** 작업은 계획되어 빈틈이 보이고, 프로세스 없이 `unsupported`(`repository_code_not_allowed` 또는 `repository_code_not_offered`, 제약 `compile_db_generation_not_allowed`)로 끝난다.
- **도구.** cmake, 그리고 make(없으면 ninja)를 운영자의 `--toolchain-path`에서 찾는다. cmake가 없으면 `unsupported`(`build_tool_not_configured`, 제약 `cmake_not_found`), make와 ninja가 모두 없으면 같은 사유에 `cmake_generator_not_found`다. 다운로드하지 않는다. 자식 환경은 `PATH=<--toolchain-path의 절대 디렉터리>`, `LANG=C.UTF-8`(executor가 HOME·TMP를 task 전용으로 더함)뿐이며, configure가 찾는 C/C++ 컴파일러도 이 PATH에서 찾는다. 먼저 `cmake --version`을 빈 probe 디렉터리에서 실행한다. 시험한 계열은 4.2이며 밖이면 `tool_version_untested`를 남긴다.
- **실행.** 프로젝트마다 `cmake -S <작업 사본>/<프로젝트> -B <작업 사본>/.codeye/compile-db/<n> -G "Unix Makefiles"|Ninja -DCMAKE_MAKE_PROGRAM=<절대 경로> -DCMAKE_EXPORT_COMPILE_COMMANDS=ON -DFETCHCONTENT_FULLY_DISCONNECTED=ON -DCMAKE_FIND_USE_PACKAGE_REGISTRY=OFF -DCMAKE_FIND_USE_SYSTEM_PACKAGE_REGISTRY=OFF -Wno-dev --no-warn-unused-cli`를 cwd=build 디렉터리, 제한시간 5분, 출력 1 MiB로 실행한다. configure만 하며 target을 build하지 않는다. 원본 사용자 경로와 스냅샷에서는 실행하지 않는다(스냅샷은 보호 경로). 실행 전에 각 `CMakeLists.txt`를 manifest 해시와 대조한다(`snapshot_integrity_failed`).
- **build 디렉터리 위치.** 작업 사본의 `.codeye/compile-db/<n>`이다. 스냅샷은 어떤 깊이의 `.codeye`도 담지 않고, task 사이 작업 사본 복원은 숨은 디렉터리를 남기며, 분석기는 이를 소스로 보지 않는다. configure가 쓴 헤더(`configure_file`)가 작업 사본 안에 있어 분석기가 include 경로로 읽을 수 있다. 다음 attempt는 새 작업 사본에서 다시 만든다.
- **결과.** 모든 프로젝트가 읽을 수 있는 `compile_commands.json`을 쓰면 `succeeded`(제약 `compile_db_generated`, `compile_db_generator_cmake`)다. 일부만 성공하면 `failed`(`compile_db_generation_failed`, 제약 `compile_db_generation_partial`)이고 분석기는 성공한 것을 쓴다. 모두 실패하면 `failed`이며, configure가 모두 0으로 끝났는데 DB가 없으면 `compile_db_not_written`, 아니면 `compile_db_generation_failed`다. 취소·timeout·정리 불확실은 각각의 사유로 끝난다. 프로세스를 실행했으면 `repository_code_executed`를 남긴다. 이 작업은 관찰 envelope를 쓰지 않는다. 작업의 제약(`cmake_not_found`, `compile_db_generation_partial`, `repository_code_executed` 등)은 result manifest에 남고 보고서 builder가 보고서 한계로 옮기며, 성공하지 않은 작업은 분석 공백 `compile_db_not_generated`가 된다([한계 코드](REPORT.md#한계-코드)). 정적 분석 절의 도구 행은 아니다.
- **분석기에 넘기는 방법.** 작업은 attempt scratch에 기록(`<scratch>/compile-db/record.json`: snapshot digest, attempt, 프로젝트별 build 디렉터리·DB SHA-256·항목 수, CMake 입력 파일)을 남긴다. 업로드하지 않는다. 분석기는 기록의 snapshot·attempt가 맞을 때만 DB를 읽고, 파일 해시가 기록과 다르면 `snapshot_integrity_failed`로 실패한다(조용히 축소 모드로 바꾸지 않는다). 읽은 DB는 스냅샷 DB와 같은 [매핑](#compile-db-매핑)과 flag 허용 목록을 지난다. 항목 compiler는 실행하지 않는다. 생성 build 디렉터리 아래 include 경로는 스냅샷에 없어도 없는 경로로 세지 않고 제약 `generated_include_dirs_used`를 남긴다.
- **envelope 기록.** `target.properties.compile_db_source=generated`, `compile_db_generator=cmake`, `compile_db_generator_version`, `rule_pack.selection`의 `compile-db=cmake-<버전>`, 제약 `compile_db_generated`·`compile_db_generator_cmake`(일부 실패면 `compile_db_generation_partial`). `rule_pack.config_files`는 스냅샷의 `CMakeLists.txt`·`*.cmake`와 SHA-256이다. 256개를 넘으면 목록 없이 `cmake_inputs_not_enumerated`다.
- **한계.** 네트워크를 막지 않는다. `FETCHCONTENT_FULLY_DISCONNECTED`는 FetchContent만 끊으며, configure 중의 `file(DOWNLOAD)`, `execute_process`로 부르는 git·curl, ExternalProject 설정은 노드 계정의 네트워크와 toolchain 경로의 프로그램으로 실행될 수 있다. 이것이 허가가 필요한 이유다. configure가 소스 트리에 쓴 파일은 작업 뒤 작업 사본 복원이 지우므로(`workspace_restored_after_task`·`workspace_leftovers_removed`) 분석기가 읽지 않는다. build 디렉터리 밖에 생성 헤더를 쓰는 프로젝트는 그 헤더가 없는 단위가 `missing_include_files`일 수 있다. 외부 패키지(`find_package`)가 노드에 없으면 configure가 실패할 수 있다. 이때는 실패로 남고 분석기는 축소 모드다. configure 결과는 노드의 컴파일러·system header에 따라 달라진다. CMake 밖의 build 설명(Meson, autotools `configure`, Makefile만 있는 프로젝트)은 생성하지 않는다(분석기 제약 `compile_db_build_description_unsupported`).

### compile DB가 없을 때

스냅샷 DB도 생성한 DB도 없을 때다. 분석기 제약이 이유를 말한다.

| 제약 | 경우 |
|---|---|
| `compile_db_build_description_unsupported` | CMake 프로젝트가 없다 |
| `compile_db_generation_not_allowed` | 저장소 코드 실행 허가가 없다 |
| `compile_db_generation_unavailable` | 생성 작업이 계획되지 않았거나(쓸 수 있는 C/C++ 분석기 없음) cmake·make·ninja가 toolchain 경로에 없거나 취소·timeout으로 끝났다 |
| `compile_db_generation_failed` | configure가 실패했거나 DB를 쓰지 않았다 |

- clang-tidy와 GCC는 프로세스를 시작하지 않고 `unsupported`(`compile_db_missing`)로 끝난다.
- cppcheck는 **축소 모드**다. 범위 안 번역 단위를 `--file-list`로 주고 include 경로·매크로를 주지 않는다. 제약 `compile_db_missing`·`reduced_mode_no_build_flags`, `target.properties.mode=reduced`, 단위 상태 `analyzed_*_without_build_flags`로 남긴다. 이 결과는 정밀 분석 완료가 아니다. include 디렉터리가 여러 개인 프로젝트는 사용자 헤더를 찾지 못해 `missing_include_files`로 실패한다. include 경로는 추측하지 않는다.

### cppcheck

1. `cppcheck --version` → `Cppcheck 2.22.0`. XML의 `<cppcheck version>`이 이와 다르면 형태 오류다.
2. `cppcheck --output-format=xmlv2 --enable=warning,performance,portability,information,missingInclude,style --check-level=normal --platform=native --error-exitcode=2 --project=<scratch>/work/compile_commands.json`. 축소 모드는 `--file-list=<scratch>/work/files.txt`다.

결과는 stderr의 XML(results version 2)과 stdout의 `Checking …` 진행 줄이다. 한 실행이 모든 단위를 본다. XML은 streaming decoder로 읽고 끝(`</results>`)이 없거나 알 수 없는 요소·속성이 있으면 형태 오류다. `--error-exitcode=2`는 레코드가 하나라도 있으면 exit 2다. 실행마다 나오는 `checkersReport` 요약만 있으면 exit 0이므로 종료 코드 대조에서 이 레코드는 세지 않는다. 범위 레코드(information·missingInclude)는 관찰이 아니라 도구 오류·제약이다.

| 관측 | 결과 |
|---|---|
| exit 0, `checkersReport`만 | succeeded / `success_without_findings` |
| exit 2, 검사 레코드 | succeeded / `success_with_findings` |
| exit 2, `missingInclude`(사용자 헤더) | failed / `missing_include_files`(configuration_error), 그 단위 `partial_missing_include`, 다른 관찰 보존 |
| `missingInclude`가 헤더 안(`file0` 없음) | 모든 단위 `possibly_partial_missing_include`, failed / `missing_include_files` |
| exit 2, `missingIncludeSystem`만 | 성공 가능, 제약 `system_includes_not_parsed` |
| exit 2, `syntaxError`·`internalError` 등 | failed / `translation_unit_errors`(`failed_parse`) |
| `toomanyconfigs`·`normalCheckLevelMaxBranches` 등 | 제약 `preprocessor_configurations_limited`·`check_level_branches_limited` |
| 단위가 `Checking` 줄에 없음 | failed / `translation_units_not_processed` |
| exit 1 + `cppcheck: error:`, signal | failed / `cppcheck_failed` |
| exit 0인데 레코드, exit 2인데 레코드 없음 | failed / `exit_status_mismatch` |
| XML 잘림·손상 | failed / `output_truncated`·`output_unparsed` |

cppcheck는 `--project`가 준 전처리 설정만 확인한다(`preprocessor_configs_checked`). 다른 `#ifdef` 조합은 확인하지 않는다.

### clang-tidy

1. `clang-tidy --version` → `LLVM version 23.1.5`.
2. 단위를 16개씩 묶어 실행한다(`clang-tidy.check.N`): `clang-tidy -p <scratch>/work --config=<inline YAML> --extra-arg-before=--no-default-config --use-color=false <단위 절대 경로>…`. `--`는 쓰지 않는다. 쓰면 fixed compile DB 모드가 되어 `-p`를 무시한다.

결과는 stdout의 진단 흐름(`file:line:col: level: message [check]`와 note)과 stderr의 진행·요약 줄이다. `--export-fixes`는 기록된 stream에 묶을 수 없어 쓰지 않는다. 컴파일 경고(`clang-diagnostic-*`)는 선택하지 않고 그래도 나온 것은 finding이 아니다(`compiler_warnings_ignored`). stderr `W warnings and E errors generated.`의 E는 한 실행 안에서 누적되고 `Error while processing F.`는 누적 수가 0을 넘은 뒤 모든 단위에 찍힌다. 그래서 단위 실패는 그 단위에서 누적 수가 늘었는지로 정한다. 단위가 하나인 실행은 오류 줄로 정한다.

| 관측 | 결과 |
|---|---|
| exit 0, 경고만 | succeeded / `success_with_findings` |
| exit 0, 진단 없음 | succeeded / `success_without_findings` |
| exit 1, 한 단위의 사용자 헤더 `file not found` | failed / `missing_include_files`(`failed_missing_include`), 다른 단위 관찰 보존 |
| exit 1, 구문·의미 오류 | failed / `translation_unit_errors`(`failed_compile`) |
| `Running without flags.`, compile DB 읽기 오류, `Error:`, crash·signal | failed / `clang_tidy_failed` |
| `Skipping F. Compile command not found.`, 단위 블록 없음 | failed / `translation_units_not_processed` |
| exit 0인데 오류 진단, exit 1인데 누적 오류 0 | failed / `exit_status_mismatch` |
| stdout 알 수 없는 줄·잘림 | failed / `output_unparsed`·`output_truncated` |
| compile DB 없음 | unsupported / `compile_db_missing`(프로세스 없음) |
| 범위 안 단위 0 | unsupported / `no_translation_units` |

cppcheck CTU와 clang-tidy CSA는 한 실행 안의 단위만 본다. clang-tidy는 16단위씩 나누므로 단위 사이 분석은 하지 않는다.

### GCC 파일 디스크립터 검사

GCC 13 이상이 설치된 노드에서 `gcc --version`을 확인한다. 실기 시험은 Linux의 13.4.0과 15.2.0에서 한 C 번역 단위씩 실행했다. `.c` 파일이 compile DB에서 C++이나 헤더 언어로 지정되면 제외한다. compile DB가 없으면 `unsupported / compile_db_missing`이다.

- 설정한 GCC에 정제한 컴파일 인자와 `-fanalyzer -Wanalyzer-too-complex -fdiagnostics-format=json -fdiagnostics-column-unit=byte -fdiagnostics-color=never -fdiagnostics-show-option -S -o /dev/null` 및 작업 사본 파일의 절대 경로를 준다. `-S`의 컴파일러 프런트엔드는 실행되며 어셈블리 출력은 버린다. 프로젝트의 빌드 스크립트·컴파일러 wrapper·플러그인·응답 파일은 사용하지 않는다. 컴파일러 환경변수는 전달하지 않는다.
- 파일 디스크립터의 접근 모드·유형·상태 오류, 중복 닫기, 누수, 닫힌 디스크립터 사용, 확인하지 않은 디스크립터 사용 등 `-Wanalyzer-fd-*` 일곱 규칙만 게시한다. 다른 GCC 경고는 발견 수에서 제외한다. 닫은 디스크립터를 읽은 합성 예제는 GCC에서 보고하고, 같은 입력을 기본 검사로 실행한 clang-tidy 21.1.8과 cppcheck 2.22.0은 이 진단을 보고하지 않았다. [실측 관찰 예시](../examples/observations-gcc-analyzer.json)
- 경고가 있어도 종료 코드 0이며 정상 예제는 게시할 경고가 없다. 실패한 단위, 시간 초과, 잘리거나 파싱할 수 없는 진단은 실패 또는 미완료 범위로 남긴다. 종료 코드가 성공한 단위의 분석 내용이 남을 수 있어도 실패한 작업 전체는 처리 완료로 기록하지 않는다.
- **복잡도 한도는 그 단위의 한계다.** GCC가 `-Wanalyzer-too-complex`로 한 단위의 경로 탐색을 일부 멈추면 그 단위는 `partial_too_complex`다. 그 단위에서 찾은 진단은 남지만 처리 파일이 아니다. 끝까지 분석한 단위가 하나라도 있으면 작업은 성공하고 한계 `gcc_analyzer_too_complex`와 `target.properties.units_too_complex`(한도에 걸린 단위 수)를 남긴다. 모든 단위가 한도에 걸리면 끝까지 분석한 것이 없으므로 `failed / gcc_analyzer_too_complex`다. 다른 단위의 컴파일 실패·시간 초과·출력 오류는 전과 같이 작업을 실패시키고 그 사유가 앞선다. 탐색 한도(`--param analyzer-*`, `-fanalyzer-call-summaries`)는 GCC 기본값 그대로다. [A67](DECISIONS.md)
- 저장 stderr는 작업 사본에 위치를 확인한 진단의 구조화된 원문만 사용한다. 기본 진단과 상태 전이의 위치는 스냅샷 해시·줄 및 저장된 JSON의 바이트 범위에 연결한다. 컴파일 DB digest와 소스 안 억제 상태를 함께 기록한다. [GCC 13 정적 분석 옵션](https://gcc.gnu.org/onlinedocs/gcc-13.4.0/gcc/Static-Analyzer-Options.html)

### C/C++ 규칙 문맥과 억제

| | cppcheck `codeye-cpp-cppcheck` v1 | clang-tidy `codeye-cpp-clang-tidy` v1 |
|---|---|---|
| 선택 | `--enable=warning,performance,portability,information,missingInclude,style --check-level=normal --platform=native`. error 검사는 항상 켜며 information·missingInclude는 범위 판단에 쓴다. style에서는 `redundantAssignment`만 관찰로 제공한다. | `-*`, `clang-analyzer-*`(osx·optin·webkit·fuchsia·DeprecatedOrUnsafeBufferHandling·중복 제외), `bugprone-*`(이름·형식·변환 계열과 중복 제외), `performance-*`(avoid-endl, enum-size 제외), `concurrency-thread-canceltype-asynchronous`, `misc-redundant-expression`, `misc-unconventional-assign-operator`, `misc-throw-by-value-catch-by-reference`. 고정 키 `WarningsAsErrors: ""`, `HeaderFilterRegex: .*`, `ExcludeHeaderFilterRegex: ""`, `SystemHeaders: false`, `FormatStyle: none`, `CheckOptions: {}`. 별칭 계열(`cert-*`, `hicpp-*`, `cppcoreguidelines-*`)은 고르지 않음 |
| `repository_config` | compile DB 모드 `honored`, `config_files`=compile DB. 축소 모드 `none` | `honored`, `config_files`=compile DB(`repository_clang_tidy_config=overridden`) |
| `source_suppressions` | `disabled`. `--inline-suppr`가 없으면 `// cppcheck-suppress`가 효력이 없음 | `honored`. NOLINT·NOLINTNEXTLINE·NOLINTBEGIN을 보고하거나 끌 옵션이 없음. stderr `Suppressed N warnings (K NOLINT)`의 K를 `nolint_suppressed`로 세고 제약 `nolint_suppressed_diagnostics` |

compile DB가 바뀌면 설정 digest가 바뀌어 이전 run과 `rule_context_changed`다. 사라진 clang-tidy finding은 언제나 `absent_unverified`이고, cppcheck는 같은 compile DB이면 `resolved_observed`가 될 수 있다.

범주: cppcheck는 severity performance → performance, memleak·resourceLeak·mismatchAllocDealloc·doubleFree 계열 → resource_lifecycle, except·throw 계열 → error_handling, style → maintainability, 나머지 → correctness. clang-tidy는 `performance-*` → performance, `clang-analyzer-security.*`와 일부 bugprone → security, Malloc·NewDelete·MismatchedDeallocator 계열 → resource_lifecycle, `concurrency-*` → concurrency, 예외·반환값 계열 → error_handling, `deadcode` → maintainability, 나머지 → correctness.

Cppcheck 2.22.0은 개별 규칙 이름으로 `redundantAssignment`를 켤 수 없어 `style` 그룹을 실행한다. 값이 사용되기 전에 같은 필드를 다시 쓰는 진단은 두 대입 위치와 함께 관찰로 남긴다. 나머지 style 결과는 원시 XML과 종료 코드 판단에 남고 관찰·발견 수에서는 제외한다. C 구조체의 연속 대입, 앞 대입을 지운 수정본, 두 대입 사이에 값을 읽는 대조군, 다른 style만 나온 파일을 실제 도구와 재생 시험으로 확인한다.

### 번역 단위 범위와 생성 헤더

단위(`coverage.unit=translation_unit`)는 compile DB 모드에서 매핑된 단위, 축소 모드에서 범위 안 소스 파일이다.

| 상태 | 뜻 | 처리 파일 |
|---|---|---|
| `analyzed_clean`, `analyzed_with_findings` | 끝까지 분석함 | 예 |
| `analyzed_clean_without_build_flags`, `analyzed_with_findings_without_build_flags` | 축소 모드에서 분석함 | 예(성공한 작업만) |
| `partial_missing_include` | cppcheck가 사용자 헤더 없이 분석함 | 아니오 |
| `possibly_partial_missing_include` | 헤더 안 missingInclude는 포함한 단위 없이 한 번만 보고됨 | 아니오 |
| `failed_missing_include`, `failed_compile`, `failed_parse` | clang-tidy 컴파일 실패, cppcheck 구문 실패 | 아니오 |
| `not_processed`, `unknown` | 출력이 처리를 보여 주지 않음, 실행이 끝나지 않음 | 아니오 |
| `partial_too_complex` | GCC가 복잡도 한도로 일부 경로 탐색을 멈춤(진단은 남음) | 아니오 |
| `not_in_compile_db`, `compile_db_entry_excluded` | compile DB에 없음, 모든 항목 제외 | 아니오 |

스냅샷 compile DB가 가리키는, configure가 build 디렉터리에 만드는 생성 헤더(`config.h` 등)가 스냅샷에 없으면 두 도구 모두 그 단위에서 `missing_include_files`(configuration_error)로 실패한다. 찾지 못한 헤더는 도구 오류이며 결함이 아니다. 다른 단위의 관찰은 남지만 처리 파일은 주장하지 않는다. 같은 헤더가 compile DB의 `-I`가 가리키는 위치에 스냅샷으로 있으면 성공한다. 헤더 안 위치의 관찰은 남지만 헤더를 처리 파일로 주장하지 않는다. 생성 소스·생성 헤더가 없는 것은 범위 공백이다. [compile DB 생성](#compile-db-생성)으로 만든 DB는 configure가 작업 사본 build 디렉터리에 쓴 헤더를 읽으므로 이 공백이 없다(`generated_include_dirs_used`). 같은 파일을 다른 매크로로 compile하는 두 번째 이후 항목은 분석하지 않는다. MSVC·nvcc 항목은 분석하지 않는다.

### C/C++ 엔진 중복

cppcheck(자체 parser·value flow)와 clang-tidy(clang AST matcher·CSA 기호 실행)는 서로 감싸지 않으므로 둘 다 실행한다. 기준 분석기가 같은 줄에서 함께 보고한 것만 보고한 clang-tidy 검사와, clang-tidy 안에서 bugprone 검사와 같은 것만 보고한 Analyzer 검사는 뺐다(`fixtures/cpp/overlap/`에서 측정).

| 제외한 clang-tidy 검사 | 같은 구성을 보고한 검사 |
|---|---|
| `bugprone-assert-side-effect` | cppcheck assignmentInAssert |
| `bugprone-suspicious-memset-usage` | cppcheck memsetZeroBytes, memsetValueOutOfRange |
| `clang-analyzer-core.BitwiseShift` | cppcheck shiftTooManyBits·integerOverflow, shiftNegative |
| `clang-analyzer-core.StackAddressEscape` | cppcheck returnDanglingLifetime, danglingLifetime, autoVariables |
| `clang-analyzer-cplusplus.InnerPointer` | cppcheck stlcstr·returnDanglingLifetime, invalidContainer |
| `clang-analyzer-cplusplus.Move` | clang-tidy `bugprone-use-after-move` |
| `clang-analyzer-security.FloatLoopCounter` | clang-tidy `bugprone-float-loop-counter` |

`bugprone-unused-return-value`, `clang-analyzer-unix.MismatchedDeallocator`, `clang-analyzer-security.ArrayBound`, `clang-analyzer-core.DivideZero`, `clang-analyzer-core.NullDereference`, `clang-analyzer-unix.Malloc`, `bugprone-unhandled-self-assignment`, `bugprone-standalone-empty`, `bugprone-branch-clone`, `bugprone-use-after-move`는 cppcheck와 같은 줄을 보고하기도 하지만 clang-tidy만 보고한 변형이 있어 남겼다.

### 작업 사본 밖 파일 내용의 저장 차단

저장소는 `#include "/etc/…"`나 `..`로 운영자 계정이 읽을 수 있는 파일을 도구가 읽게 만들 수 있다. clang-tidy는 진단마다 소스 줄을 찍고(`-fno-caret-diagnostics`로 없앨 수 없음), cppcheck XML은 메시지·`<symbol>`에 그 파일의 이름·token을 담는다. 그래서 실행 결과를 기록하기 전에 stream을 고친다(`cpp/sanitize.go`).

- clang-tidy stdout: 모든 snippet 줄을 지운다(`source_snippets=removed`). 위치가 작업 사본 밖인 진단은 note와 함께 지우고, 남긴 진단의 밖 note도 지운다.
- cppcheck stderr: 위치 하나라도 작업 사본 밖인 `<error>` 레코드를 지운다. id·severity·단위만 판정에 쓴다.
- 지운 오류는 메시지 없는 도구 오류(`compile_error`·`parse_error`, "message withheld")로 남고 그 단위는 실패한다. 수는 `target.properties.outside_records_dropped`와 제약 `outside_workspace_diagnostics_dropped`로 남고 종료 코드 대조는 지운 레코드도 센다.
- 허용하는 위치는 작업 사본과 clang 의사 파일(`<command line>`, `<built-in>`)뿐이다. system header 위치의 진단도 지운다.

남은 위험: 도구는 운영자 계정이 읽을 수 있는 모든 파일을 읽을 수 있고 막는 것은 저장뿐이다. 작업 사본 안 위치의 진단도 밖 파일에서 계산한 값(`#embed`와 `_Static_assert`, 밖 헤더 매크로 전개)을 담을 수 있다. 신뢰 경계가 다른 저장소는 별도 계정이나 노드에서 분석한다([보안](SECURITY.md)).

## Python 팩

ruff(기준), mypy·pyright(타입 검사, 서로 대안), bandit(보안)이다. 결정은 A25([결정](DECISIONS.md))다. 모든 task는 `RepositoryCode=false`다. 어떤 도구도 프로젝트의 interpreter·venv·plugin·설정 프로그램을 실행하지 않는다.

Sphinx·Markdown에서 Python docstring, 함수 구현과 테스트 단언을 잇는 스냅샷 탐색은 [모델 검토](REVIEW.md#문서-발췌documents)에서 수행한다. 이 탐색은 팩의 정적 도구 결과에 합산하지 않는다.

- **기준이 ruff인 이유.** 단일 정적 바이너리라 Python interpreter도 node도 필요 없다. `--isolated`이면 설정 파일을 읽지 않고 import 대상도 실행하지 않는다. 선택 규칙(pyflakes·bugbear·오류 흐름)은 파일 단위라 의존성 설치 없이 결과가 안정적이다. ruff만 있는 노드도 deep-review를 광고한다.
- **모듈.** `pyproject.toml`·`setup.cfg`·`.python-version`·`requirements*.txt`가 있는 디렉터리다. 모듈마다 도구별 task를 계획한다. 숨김 디렉터리·숨김 파일·`__pycache__`·`__pypackages__`·`node_modules`·`site-packages`·`testdata`·`vendor`·`venv`는 분석하지 않는다. 스냅샷은 `pyvenv.cfg`가 있는 가상환경과 symlink를 복사하지 않는다. Python 소스가 있지만 분석할 파일이 없으면 자리 task 하나가 `no_analyzable_python_files`로 not-run envelope를 남긴다. 노트북(`.ipynb`)은 분석하지 않는다. 하위 모듈 디렉터리 이름에 쉼표·glob 문자가 있으면 그 task는 `nested_module_name_unsupported`로 실행을 거부한다.

### 저장소 설정과 interpreter

| 도구 | 저장소가 정할 수 있는 것 | CODEYE 처리 |
|---|---|---|
| ruff | `ruff.toml`·`.ruff.toml`·`[tool.ruff]`(`extend`, `src`, `cache-dir`, 규칙) | `--isolated`로 설정 파일을 읽지 않음. `--no-cache`, `RUFF_CACHE_DIR`=scratch |
| mypy | `mypy.ini`·`.mypy.ini`·`setup.cfg`·`[tool.mypy]`(`plugins`, `mypy_path`, `files`, `cache_dir`, 보고서) | `--config-file=`(빈 값)으로 설정 파일을 읽지 않음. 경로 형식 `plugins`는 Python 코드 실행이라 저장소 설정을 따르는 방식은 두지 않음. 캐시·보고서는 scratch |
| pyright | `pyrightconfig.json`·`[tool.pyright]`(`extraPaths`, `stubPath`, `typeshedPath`, `venvPath`/`venv`, `include`/`exclude`, 규칙 수준) | `-p <scratch>/cfg`의 CODEYE `pyrightconfig.json`만 읽음. 실행 root는 모듈 상대 경로. 빈 scratch venv(`venvs/codeye-empty/lib/python3.N/site-packages`) 지정. 설정이 없으면 pyright가 PATH의 `python`을 실행해 검색 경로를 묻는데 CODEYE 설정에서는 호출하지 않음 |
| bandit | `.bandit`(대상이 `.`일 때 자동으로 읽음), `-c` YAML·pyproject | `--ini <scratch>/cfg/bandit.ini`(빈 `[bandit]`)가 `.bandit`을 대신함. `-c`는 쓰지 않음 |

네 도구 모두 `repository_config=overridden`, `config_files=[]`다. 저장소 설정 값은 경로를 정하지 못하며 쓰기 위치(캐시, 보고서, bandit JSON, pyright 설정·빈 venv)는 모두 task scratch다.

mypy·bandit 스크립트는 운영자 venv의 절대 `#!` interpreter로 실행한다(`#!` 검사는 Unix에서만). 환경에는 `PYTHONDONTWRITEBYTECODE=1`·`PYTHONNOUSERSITE=1`·`PYTHONSAFEPATH=1`을 둔다. mypy는 `--no-site-packages`라 설치 패키지를 묻지 않는다. 이 attempt가 Python 패키지를 받았으면 대신 `--python-executable`로 task scratch의 가짜 venv(운영자 `--python`을 가리키는 `bin/python`과 받은 트리의 site-packages)를 준다. mypy는 그 interpreter로 자기 `pyinfo.py`를 한 번 실행해 검색 경로를 묻는다(`PYTHONSAFEPATH=1`, 실행 환경의 `PYTHONNOUSERSITE=1`). 그 실행은 `site`를 거치므로 site-packages의 `*.pth`를 처리한다. 받은 트리의 시작 hook(`*.pth`, `sitecustomize`)은 받을 때 지웠다([Python 의존성 받기](#python-의존성-받기)). pyright는 공유 node toolchain이 진입 스크립트를 실행한다. 네 도구 모두 PATH에서 찾는 보조 프로그램이 없다.

### 대상 Python 버전

env.discover와 같은 규칙으로 모듈 선언에서 읽는다. 순서는 `pyproject.toml` `[project] requires-python`, `setup.cfg` `[options] python_requires`, `.python-version` 첫 버전이다. 파일은 manifest SHA-256으로 확인한 뒤 읽는다. 팩은 discovery를 import하지 않고 같은 reader를 옮겨 쓰며 시험이 두 구현을 묶는다.

- PEP 440 지정자 목록에서 하한(`>=`, `~=`, `==`, `>`)이 가장 높은 것의 3.x minor를 쓴다. `.python-version`은 `3.N[.M]`만 이해한다.
- 이해하지 못한 선언은 `python_version_constraint_not_understood`, 읽지 못한 선언은 `python_version_declaration_unreadable`로 남기고 다음 선언을 본다.
- 선언이 없으면 3.14를 쓰고 `python_version_source=codeye_default`와 `python_version_unknown`을 남긴다. 새 기본값은 버전별 진단 일부를 놓칠 뿐이지만 오래된 기본값은 새 문법을 구문 오류로 만들기 때문이다.
- 도구가 받는 범위로 맞추고 벗어나면 `python_version_below_tool_minimum`·`python_version_above_tool_maximum`을 남긴다. ruff 3.7~3.15, mypy 3.10~3.15, pyright 3.3~3.15다.
- `target.properties`에 `python_version`, `python_version_source`, `python_version_declared`(ruff는 `target_version` `py3N`도)를 남긴다. bandit은 자기 interpreter의 ast로 파싱하므로 `python_version_applied=false`와 `parser_python_version`을 남긴다.
- 한 모듈에 대상 버전·플랫폼 하나만 검사한다(`single_target_python_version`). 조건부 import·버전 분기는 그 조합에서만 본다.

### 타입 검사기 선택

- `--mypy`만 주면 mypy, `--pyright`(+`--node`)만 주면 pyright가 실행된다. 다른 쪽 task는 계획되고 `unsupported`(`tool_not_configured`)이므로 `static_analysis` 단계는 `partial`이다.
- 둘 다 실행하려면 `--python-both-type-checkers`가 필요하다. 둘 다 선택하면 두 task에 `python_type_checkers_both_selected`를 남긴다. 실제로 실행됐는지는 각 task 상태가 기록한다(예: `--node`가 없으면 pyright는 `toolchain_not_configured`).
- 이 선택은 agent 플래그 검사로만 집행하고 hello에 새 필드를 두지 않는다. 워커는 두 경로가 모두 오면 둘 다 실행한다. 두 검사기의 같은 구성 진단은 별도 관찰·별도 finding이다.

### Python 규칙 팩

| 규칙 팩 | 선택(`rule_pack.selection`) |
|---|---|
| `codeye-python-ruff` v1 | `--isolated --no-respect-gitignore --select E711,E712,E713,E714,E721,E722,E9,F,B,BLE,PLE,ASYNC --ignore B905,B911,B912 --unfixable ALL`. 비교·bare except·IO/구문, pyflakes, bugbear, blind except, pylint 오류, async. 스타일 규칙과 S(flake8-bandit)는 고르지 않음. B905·B911·B912는 `strict=` 명시 요구라 제외 |
| `codeye-python-mypy` v1 | `--config-file= --no-site-packages --explicit-package-bases --follow-imports=silent --check-untyped-defs --no-native-parser --disable-error-code=await-not-async --disable-error-code=name-defined --disable-error-code=no-redef --disable-error-code=str-format`. `--no-native-parser`는 비 ASCII 줄에서 native parser의 열이 틀려서 |
| `codeye-python-pyright` v1 | `config=codeye-managed typeCheckingMode=standard`, `reportAssertAlwaysTrue`·`reportRedeclaration`·`reportUndefinedVariable`·`reportUnsupportedDunderAll`·`reportUnusedExpression`을 `none` |
| `codeye-python-bandit` v1 | `--ini=codeye-managed --skip B101,B105,B106,B107,B613`. B101 assert는 pytest assert마다 나옴, B105~B107은 메시지에 값 자체가 들어감(비밀값은 gitleaks가 다룸), B613은 ruff PLE2502와 같은 구성 |

### Python 엔진 중복

ruff가 기준이므로 같은 구성을 보는 타입 검사기 쪽 검사를 껐다.

| ruff 규칙 | mypy | pyright | 처리 |
|---|---|---|---|
| F821 정의되지 않은 이름 | `name-defined` | `reportUndefinedVariable` | 두 검사기에서 끔 |
| F811 재정의 | `no-redef` | `reportRedeclaration` | 두 검사기에서 끔 |
| F507/F524 `%`·`format` 인자 | `str-format` | 없음 | mypy에서 끔 |
| PLE1142 async 밖 `await` | `await-not-async` | 규칙 없는 구문 오류 | mypy에서 끔. pyright는 그대로 |
| F631 tuple assert | `misc` | `reportAssertAlwaysTrue` | pyright에서 끔. mypy `misc`는 다른 검사도 담아 끌 수 없어 두 관찰이 남음 |
| B015 쓸모없는 비교 | 없음 | `reportUnusedExpression` | pyright에서 끔 |
| PLE0604 `__all__`의 비문자열 | `list-item` | `reportUnsupportedDunderAll` | pyright에서 끔 |
| PLE2502 양방향 제어 문자 | 없음 | 없음 | bandit B613을 끔 |

ruff S 규칙은 bandit 검사를 다시 구현한 것이라 고르지 않는다. E722/BLE001과 bandit B110/B112는 같은 줄에 나오지만 다른 검사라 둘 다 둔다.

### Python 소스 안 억제

| 계열 | 처리 |
|---|---|
| ruff `# noqa: X`, bare `# noqa`, `# ruff: noqa: X`, `# flake8: noqa`, `# ruff: ignore[X]`, `# ruff: disable[X]`/`enable` | `reported`. 억제 존중 실행과 `--ignore-noqa` 실행의 차이가 `suppressed=true` 관찰이고 원시 참조는 `--ignore-noqa` stdout |
| bandit `# nosec`, `# nosec B602`, import 줄 nosec | `reported`. 억제 존중 실행과 `--ignore-nosec` 실행의 차이 |
| mypy `# type: ignore[...]`, `# mypy: ignore-errors` | `honored`. 숨긴 오류를 보고할 방법이 없음 |
| pyright `# type: ignore`, `# pyright: ignore[rule]` | `honored` |

두 실행 차이는 (파일·규칙·위치·메시지) 다중집합으로 맞춘다. 억제 존중 실행에만 있는 진단이 있으면 `suppression_diff_inconsistent`로 실패한다.

### Python 입력 보호

- 도구가 보고한 파일이 작업 사본 밖으로 풀리면(절대 경로, `..`) 그 관찰과 tool error를 메시지째 버리고 개수만 `diagnostics_outside_workspace_dropped`로 남긴다. 처리 파일로 세지 않는다. 원시 stdout artifact에는 도구가 낸 그대로 남는다(executor 경로 마스킹은 적용).
- 수집 stream에 소스 줄을 두지 않는다. ruff는 fix edit에 원본 소스를 옮기므로 `--unfixable ALL`로 비운다. bandit JSON은 `code`에 소스 줄을 담으므로 scratch 파일(`-o`)로만 쓰고 관찰은 소스 줄이 없는 CSV stream에서 읽는다. mypy `-O json`, pyright `--outputjson`에는 소스 줄이 없다. pyright 메시지는 문자열 literal 타입을 값째 적으며 이 팩은 메시지를 가리지 않는다.
- ruff·bandit 대상은 `--` 뒤에 두고 `.`이 아니면 `./`를 붙인다. mypy `@` 파일 목록의 각 줄과 pyright stdin 목록도 `./`로 시작한다.

### import·stub 공백

검사기는 패키지를 설치하지 않는다. 같은 attempt의 `deps.python` 기록이 쓸 수 있으면 그 트리를 읽는다([Python 의존성 받기](#python-의존성-받기)). 해결되지 않은 import는 결함이 아니라 범위 공백이다. 두 검사기 모두 해결되지 않은 모듈을 `Any`로 다루지만 후속 오류가 없지는 않다. 해결되지 않은 decorator가 만드는 생성자를 모르면 그 호출이 오류가 된다(attrs 클래스의 `call-arg`·`reportCallIssue`, 실측). 후속 오류를 따로 거르지 않으므로 받기를 켜는 것이 해결책이다. 받은 트리가 없으면 타입 검사기 task에 `third_party_packages_not_installed`가 붙고, 있으면 `third_party_packages_prepared`(일부만 받았으면 `third_party_packages_partially_prepared`)다. 프로젝트 venv를 쓰는 방식은 저장소 코드 실행이라 두지 않았다.

| 도구 | 진단 | coverage 단위 | 제약 |
|---|---|---|---|
| mypy | `import-not-found` | `import_unresolved` | `imports_unresolved` |
| mypy | `import-untyped` | `stub_missing` | `type_stubs_missing` |
| pyright | `reportMissingImports` | `import_unresolved` | `imports_unresolved` |
| pyright | `reportMissingModuleSource` | `import_source_missing` | `import_sources_missing` |
| pyright | `reportMissingTypeStubs` | `stub_missing` | `type_stubs_missing` |

### Python 결과 판정

| 도구 | exit | 판정 |
|---|---|---|
| ruff | 0 + 레코드 없음 / 1 + 레코드 | 성공. 처리 파일은 `ruff.files` 실행의 `--show-files` 목록 |
| ruff | 0·1이 레코드와 어긋남 / 2 | `exit_status_mismatch` / `ruff_failed` |
| mypy | 0 + 오류 없음 / 1 + 오류(blocking·구문 없음) | 성공. 처리 파일은 `--lineprecision-report`의 모듈과 계획 파일 대조 |
| mypy | 2 + 구문 오류 | 그 파일을 빼고 다시 실행(`mypy.check.r01`…, 최대 32회). 다시 실행할 수 없으면 `source_syntax_errors`, 제한시간이 다하면 `timed_out`(`mypy_reruns_time_limit_reached`) |
| mypy | 2 그 밖 | `mypy_failed` |
| pyright | 0·1이 summary 오류 수와 맞음 | 성공. `filesAnalyzed`가 계획 파일 수와 같을 때만 처리 파일 인정 |
| pyright | 2·3·4 | `pyright_failed` |
| bandit | 두 실행 모두 0 + 결과 없음 / 1 + 결과 | 성공. 처리 파일은 scratch JSON `metrics`에서 `errors` 파일을 뺀 `.py` |
| bandit | 결과와 어긋남 / 2 | `exit_status_mismatch` / `bandit_failed` |

- 구문 오류(ruff `invalid-syntax`·`E999`·`E902`, mypy `syntax`, pyright 규칙 없는 오류, bandit `errors` 항목)가 난 파일은 `syntax_error` 단위로 세고 처리 파일에서 빼며 제약 `unparsable_files_not_analyzed`를 남긴다. 나머지 파일의 결과로 작업은 성공한다. 위치가 있는 구문 오류는 파일마다 첫 하나가 관찰이다(ruff·mypy는 그 규칙, pyright는 `syntax`). 줄 끝을 넘는 열은 줄 끝으로 맞춘다. bandit은 위치를 주지 않아 관찰이 없다. 네 도구 모두 그 파일 이름을 대상 속성 `unparsable_files`(정렬, 쉼표 구분, 256바이트를 넘는 나머지는 `+N more`)에 적는다.
- mypy는 첫 구문 오류에서 멈추므로(exit 2, 레코드 하나) 보고된 파일을 목록에서 빼고 다시 실행한다. 첫 실행과 재실행은 작업 제한시간 하나를 나눠 쓰며 재실행은 남은 시간만 받는다. 남은 시간이 없으면 재실행을 시작하지 않고 찾은 구문 오류를 남긴 채 `timed_out`이다. 재실행은 빠진 파일의 모듈을 scratch 설정의 모듈 절(`follow_imports = skip`)로 건너뛴다. 남은 파일이 그 모듈을 import하면 mypy가 같은 구문 오류에서 다시 멈추기 때문이다(실측). 구문 오류에서 멈추는 실행은 빠르므로(실측: 파일 세 개, 첫 실행과 재실행 두 번이 약 1초) 미리 다른 parser로 거르지 않는다. 그래서 구문 오류 파일이 32개를 넘으면 작업은 `source_syntax_errors`로 실패한다. 다른 parser의 문법 판정이 mypy 대상 버전의 판정과 다를 수 있기 때문이다.
- 계획했지만 도구가 처리 목록에 넣지 않은 파일은 `files_unlisted`·`not_analyzed`와 `planned_files_not_processed`로 남는다. ruff 기본 제외(`build`, `dist` 등)에 걸린 계획 파일도 여기에 든다.
- 열은 ruff code point, pyright 0부터의 UTF-16, mypy(`--no-native-parser`)·bandit UTF-8 바이트이며 모두 byte 열로 바꾼다. bandit `issue_severity`는 `raw_severity`로만 남는다.

### Python import 계약

[Import Linter](https://import-linter.readthedocs.io/en/stable/index.html)는 루트의 `.importlinter`, `setup.cfg`, `pyproject.toml` 순으로 실제 선언을 찾는다. 번들된 계약 종류(`forbidden`, `layers`, `independence`, `protected`, `private_modules`, `acyclic_siblings`)를 평가하고 사용자 정의 계약과 외부 패키지를 포함하는 계약은 사유를 남긴다. 루트 또는 `src/` 아래의 최상위 Python 패키지에서만 그래프를 만든다. 한 분석에서 두 배치를 섞거나 점을 포함한 하위 패키지를 루트로 사용하지 않는다.

운영자가 설치한 Python 환경의 Import Linter가 스냅샷에서 확인한 소스만 복사한 임시 디렉터리를 읽는다. 사용자 설정을 프로그램으로 로드하지 않고 Python 격리 모드, 사용자 소스 실행 감시, 캐시 끄기를 적용한다. 계약 결과가 첫 import 연결과 줄 번호를 제공하면 그 위치를 기록한다. 연결을 확인할 수 없는 위반도 도구 관찰로 남는다. 선언이 없거나 파일·폴더만 골라 전체 그래프를 볼 수 없는 실행에는 미실행 사유가 표시된다. 실제 2.17로 정상·위반·사용자 정의 계약 제외를 확인했다. [분석기 계약](../examples/analyzer-import-linter.yaml) · [관찰 예시](../examples/observations-import-linter.json)

## JS/TS 팩

ESLint 구문 규칙(기준), tsc, type-aware ESLint(추가 깊이)를 공유 `--node` toolchain으로 실행한다. 구현은 `internal/worker/analysis/{jsts,eslint,tsc}`, 결정은 A26([결정](DECISIONS.md))이다. 세 작업 모두 `RepositoryCode=false`다.

- **기준이 ESLint 구문 규칙인 이유.** 스냅샷은 `node_modules`를 항상 제외하므로 실제 저장소에서 tsc는 대부분 의존성 공백으로 끝난다. 구문 규칙(`@eslint/js` recommended, TS 파일에는 typescript-eslint recommended)은 타입 정보 없이 JS·TS 모듈 모두에서 끝까지 실행된다. `--node`와 `--eslint`만 있는 노드도 deep-review를 광고한다.

### 모듈과 tsconfig

| 분석기 | 계획 단위 |
|---|---|
| ESLint 기준 | JS/TS 소스가 있는 모든 모듈 |
| tsc | `tsconfig.json`이 있는 모듈 |
| ESLint type-aware | `tsconfig.json`과 TS 소스가 있는 모듈 또는 `tsconfig.json` 없이 범위에 JS 소스가 있는 모듈 |

- `package.json`이 있는 디렉터리(testdata·숨김 디렉터리 제외)가 모듈이고, 어느 모듈에도 속하지 않는 소스는 루트 모듈(`.`)에 둔다. 루트에 `package.json`이 없으면 `no_package_json_module` 제약을 남긴다. 중첩 모듈의 파일은 그 모듈의 작업이 맡는다. `jsts.PackageModules`는 env.discover의 모듈 탐지와 같은 결과를 내도록 시험으로 묶었다.
- tsconfig는 `<모듈>/tsconfig.json` 하나다. tsc와 TS용 type-aware 작업은 그 체인(`extends`·`references`)을 `config_files`에 기록한다.
- `tsconfig.json`이 없는 JS 모듈은 기준선과 선택 보강 작업을 받는다. 보강은 격리된 관리 프로그램에서만 `checkJs`를 켠다. 저장소 tsconfig가 `allowJs`·`checkJs`를 켜면 tsc가 따른다.
- 도구 스크립트는 같은 이름의 npm 패키지 안에 있어야 하고 `<node> <script> <args>`로 실행한다. `--node` 없이 도구만 지정하면 `toolchain_not_configured`다.

### JS/TS 실행

환경은 `PATH`=node 디렉터리, `LANG=C.UTF-8`(Windows는 `SystemRoot`)이고 cwd는 작업 사본의 모듈 디렉터리다. 설정 파일은 task scratch(`analyzers/<key>/cfg/`)에 둔다.

```text
eslint: <node> <eslint.js> --no-config-lookup -c <scratch>/cfg/eslint.config.cjs --no-inline-config
        --suppressions-location <scratch>/cfg/eslint-suppressions.json --no-warn-ignored
        --no-error-on-unmatched-pattern --format json -- <./경로...>
tsc:    <node> <tsc> --noEmit --pretty false --incremental false --composite false --ignoreDeprecations 6.0
        --noUnusedLocals false --noUnusedParameters false --noFallthroughCasesInSwitch false -p ./tsconfig.json [--showConfig]
```

- 실행 순서: eslint는 `eslint.version` → `eslint.check`, tsc는 `tsc.version` → `tsc.config`(`--showConfig`) → `tsc.check`.
- **solution 설정.** `files: []`와 `references`만 있는 설정은 tsc가 아무것도 검사하지 않고 exit 0을 낸다. 이때는 모듈 안 참조 프로젝트를 최대 8개까지 `tsc.config.rNN`·`tsc.check.rNN`으로 검사하고 `solution_config_references_checked`를 남긴다. 모듈 밖 참조와 상한 초과는 제약으로 남긴다.
- eslint JSON은 `-o` 파일이 아니라 stdout으로 받는다. 관찰의 `raw_ref`가 기록된 stream에 묶여야 하기 때문이다.
- ESLint와 tsc의 열은 UTF-16이며 스냅샷 원문(BOM 제외)으로 byte 열로 바꾼다.
- eslint는 `--cache`를 쓰지 않는다. tsc는 `--noEmit`에 `--incremental false --composite false`를 더한다(`--noEmit`만으로는 `tsconfig.tsbuildinfo`를 쓴다). `--build`는 쓰지 않는다.
- TypeScript 6의 폐기 옵션(`moduleResolution: node`, `baseUrl` 등)은 옵션 단계에서 검사를 멈추므로 `--ignoreDeprecations 6.0`으로 계속하고 type-aware 작업에서 보이면 `typescript_deprecations_ignored`를 남긴다.

### 저장소 코드와 tsconfig 검사

- **ESLint 설정.** 저장소 `eslint.config.*`·`.eslintrc.*`는 실행 가능한 JS이므로 `--no-config-lookup -c <관리 설정>`으로 읽지 않는다. 관리 설정은 `@eslint/js`·`typescript-eslint`를 eslint와 같은 도구 `node_modules`의 절대 진입 경로로 `require`하고 저장소 `node_modules`에서 plugin·parser를 찾지 않는다. `repository_config=overridden`과 `repository_eslint_config_not_loaded`를 기록한다. 저장소 설정을 따르는 실행은 구현하지 않았으므로 저장소가 정한 규칙·plugin 결과는 보이지 않는다. `.eslintignore`는 eslint 10이 읽지 않으며 `eslintignore_unsupported`로 남긴다. `package.json`의 `eslintConfig`는 flat config가 읽지 않고, cwd의 `eslint-suppressions.json`은 관리 경로가 대신한다.
- **tsc.** `tsconfig.json`은 JSONC 데이터로 읽는다. tsc와 typescript-eslint `parserOptions.project` 프로그램은 tsconfig 언어 서비스 `plugins`를 읽지 않는다. `NODE_ENV`는 넘기지 않는다.
- **TS type-aware.** 관리 설정의 `project`는 `./tsconfig.json`, `tsconfigRootDir`는 모듈 디렉터리다. 절대 작업 경로의 `(`·`[`·`*`를 typescript-eslint가 glob으로 확장하므로 상대 경로를 쓴다. projectService 방식은 쓰지 않는다.
- **관리 JS 타입 보강.** `tsconfig.json`이 없는 모듈에서 범위 내 일반 `.js`·`.jsx`·`.mjs`·`.cjs` 파일만 스냅샷 해시와 대조해 별도 임시 디렉터리에 복제한다. 링크, 벤더·생성·예제 경로, 최소화 파일과 ESLint 설정 파일을 입력으로 받지 않는다. 선택한 파일과 운영자가 설치한 TypeScript의 ES2022·DOM 표준 타입 파일만 읽는 컴파일러를 사용한다. 모듈 import·파일 경로와 패키지에 대한 타입 참조·`@types`·프로젝트 설정은 해석하지 않는다. 관찰 문서에 `type_analysis=isolated_javascript_primitive_rejections`를 기록하고, 보고서에서 규칙 팩·표준 타입 가정·검사 한계를 확인할 수 있다.
- 관리 JS 타입 보강은 모듈당 최대 48개 파일, 파일당 128 KiB, 소스 합계 1 MiB, 실행 단계당 25초다. Node old-space를 192 MiB로 제한한다. 소스가 한도를 넘으면 `javascript_type_input_budget_exceeded`, 지원하지 않는 텍스트면 `javascript_type_input_unsupported`로 보강 작업을 건너뛴다. `third-party`나 `third_party` 등 보강 대상이 아닌 파일은 관찰 문서에 제외 수를 남긴다. 선택 범위의 JS 파일이 모두 제외되면 `javascript_type_no_eligible_files`로 표시한다. 실행기 또는 도구가 실패하면 결과와 사유를 남긴다. 필수 구문 분석 작업은 따로 실행한다.
- **의존성 코드.** 모듈 경로나 그 상위에 `node_modules`가 있으면 tsc와 TS용 type-aware 작업은 실행 전에 `skipped_policy`(`node_modules_outside_snapshot`)다. 출처를 manifest에 묶을 수 없기 때문이다. 예외는 이번 attempt의 `deps.javascript` 기록이 `installed_dirs`로 적은 디렉터리이며, 링크가 아닌 실제 디렉터리일 때만 받아들인다([JS/TS 의존성 받기](#jsts-의존성-받기)). 기준선과 관리 JS 타입 보강은 의존성 디렉터리를 사용하지 않는다.

tsc와 type-aware 작업은 실행 전에 tsconfig 체인 전체(루트, 로컬 `extends`, `references`)를 검사한다. 기록에는 옵션 이름만 남긴다(`tsconfig_option_<snake_name>`).

- **쓰기 옵션 → `skipped_policy`(`tsconfig_write_option`).** `generateTrace`·`generateCpuProfile`은 값과 무관하게 거부한다. `outDir`·`declarationDir`·`outFile`·`tsBuildInfoFile`은 적용 플래그 아래에서 아무것도 쓰지 않으므로 스냅샷 안을 가리키면 허용하고, 절대 경로이거나 스냅샷을 벗어나면 거부한다. 실효 설정(`--showConfig`)에서 `noEmit`이 true가 아니거나 `incremental`·`composite`가 켜져 있으면 거부한다.
- **호스트 경로 → `skipped_policy`(`tsconfig_outside_snapshot`).** `files`·`include`·`exclude`·`baseUrl`·`rootDir`·`rootDirs`·`typeRoots`, 경로 모양 `types`, `paths`(자기 `baseUrl` 또는 설정 디렉터리 기준), `extends`·`references`가 절대 경로이거나 정규화 후 스냅샷을 벗어나면 거부한다. `${configDir}`는 검사 대상 설정 디렉터리로 푼다. 작업 사본 안 symlink를 따라 밖으로 나가는 경로도 거부한다(`tsconfig_option_symlink`). `--showConfig` 결과도 같은 검사를 거친다. 루트 파일이 스냅샷 밖이면 `program_files_outside_snapshot`이다.
- **진단 본문.** 메시지의 작업·도구 경로는 표지로 바꾸고 남은 따옴표 안 절대 경로는 `[host path withheld]`로 바꾼다. 작업 사본 밖 설정 파일의 진단은 본문 없이 기록한다.
- **argv.** eslint는 `--` 뒤에 `./` 접두 경로를 둔다. tsc는 `--`를 받지 않으므로(TS5023) `-p` 값에 항상 `./`를 붙인다.

### 의존성 공백

해석되지 않는 모듈·타입은 coverage 공백이며 결함이나 clean으로 세지 않는다. 공백 코드는 `jsts.GapCodes`(TS2307, 2580, 2582, 2591, 2593, 2688, 2792, 2867, 2868, 2875, 2882, 6305, 7016, 7026)다.

- 공백이 있는 실행에서는 공백과 무관해 보이는 진단(TS2322 등)도 의존성 누락의 결과였다. 그래서 공백이 하나라도 있는 tsc 실행은 `failed`(`dependency_unavailable`)이고, 공백은 `dependency` tool error, 나머지 진단은 모두 `withheld` tool error로 남기며 관찰로 내지 않는다.
- type-aware 작업은 관리 규칙 `codeye/program-diagnostics`로 프로그램의 설정·전역 진단과 파일별 공백 코드를 보고받는다. 공백이나 설정 오류가 있으면 모든 진단을 보류한다(`diagnostics_withheld_incomplete_types`). tsconfig가 로드되지 않으면("Unable to parse the specified 'tsconfig' file") 구문 오류가 아니라 `tsconfig_invalid`로 세고 진단을 보류한다.
- tsconfig 프로그램 밖 TS 파일은 type-aware 작업에서 `outside_tsconfig_program`(건너뜀)이다.
- 구문 오류는 tsc가 타입 검사 전에 멈추므로 `syntax_errors`로 실패하고 구문 진단만 tool error로 남긴다.
- 의존성 받기가 꺼져 있으면 외부 패키지를 import하는 TS 모듈의 tsc·type-aware 결과는 대부분 공백(실패)이고 기준선만 결과를 낸다. 받은 뒤에도 빌드 산출물을 가리키는 import(자기 패키지의 `..`·`dist`)나 선언하지 않은 `@types`는 공백으로 남는다(빌드는 하지 않는다).

### JS/TS 규칙 문맥과 억제

| 분석기 | 규칙 팩 | repository_config | config_files | source_suppressions |
|---|---|---|---|---|
| eslint | `codeye-js-eslint` v2 | `overridden` | `[]` | `disabled` |
| eslint-typed | `codeye-ts-eslint-typed` v1 | `honored`(tsconfig) | 체인(열거 가능할 때) | `disabled` |
| eslint-typed 관리 JS | `codeye-js-eslint-rejection-types` v1 | `overridden` | `[]` | `disabled` |
| tsc | `codeye-ts-tsc` v1 | `honored` | 체인과 참조 체인 | `honored` |

`selection`에는 plugin 버전(예: `@eslint/js@10.0.1 recommended`), 추가 규칙과 해당 범위, 끈 플래그를 적는다. plugin을 올리면 규칙 문맥이 바뀐다. [이전 ESLint 관찰 예시](../examples/observations-eslint.json)는 당시 v1 문맥으로 읽는다.

- 기준선은 `.js`·`.jsx`·`.mjs`·`.cjs` 파일에 `prefer-promise-reject-errors`를 켠다. 전역 `Promise.reject(value)`와 전역 `new Promise`의 실행자에 전달된 거부 콜백을 직접 호출하는 코드에서 문자열·객체 리터럴처럼 구문상 `Error` 객체가 될 수 없는 인자를 경고한다. 오류의 메시지와 스택이 필요한 곳에서 실패 원인을 추적하는 데 영향을 준다. 인자가 없는 호출은 `allowEmptyReject=true`로 허용한다. 변수·함수 반환값·멤버 접근의 타입은 추론하지 않아, 예를 들어 HTTP 응답 본문에서 받은 문자열을 변수로 전달하면 이 규칙에서 경고가 없을 수 있다.
- 관리 JS 작업은 타입 정보로 확인한 거부 사유의 모든 타입이 원시값일 때만 `@typescript-eslint/prefer-promise-reject-errors` 경고를 남긴다. HTTP 응답 본문에서 받은 문자열이 해당한다. `Error`·객체·`any`·`unknown` 가능성이 있거나 인자가 없으면 보강 경고를 내지 않는다. 두 작업이 성공했고 구문 진단과 보강 진단이 같은 경로·줄·바이트 열에서 확인되면 기준선 관찰을 인용하는 발견 한 건을 표시한다. 두 관찰과 원시 출력은 각각 보존하며 모델 검토의 보강 관찰 인용도 유효하다. 기준선 결과가 없거나 실패하면 검증된 보강 진단으로 발견을 만든다.

- ESLint 억제 계열: `eslint-disable`·`-line`·`-next-line`·블록, `/* eslint rule: off */`(기록 없이 진단을 지움), `/* eslint rule: error */`(선택 밖 규칙을 켬), `/* eslint-env */`, cwd의 `eslint-suppressions.json`. `--no-inline-config`와 빈 관리 suppressions 파일이면 모두 꺼져 억제가 0건이므로 `disabled`다. 그 실행에서 억제 메시지가 하나라도 나오면 `source_suppressions_not_disabled`로 실패한다. 없는 `--suppressions-location` 파일은 exit 2다.
- tsc의 `@ts-ignore`·`@ts-expect-error`·`@ts-nocheck`는 숨긴 진단을 보고하지 않으므로 `honored`다. 기준선 TS 규칙의 `@typescript-eslint/ban-ts-comment`가 이 주석을 볼 수 있다.

### JS/TS 엔진 중복

- typescript-eslint recommended의 eslint-recommended는 tsc가 검사하는 core 규칙 22개를 TS 파일에서 끈다(constructor-super, getter-return, no-array-constructor, no-class-assign, no-const-assign, no-dupe-args, no-dupe-class-members, no-dupe-keys, no-func-assign, no-import-assign, no-new-native-nonconstructor, no-new-symbol, no-obj-calls, no-redeclare, no-setter-return, no-this-before-super, no-undef, no-unreachable, no-unsafe-negation, no-unused-expressions, no-unused-vars, no-with).
- tsc의 `noUnusedLocals`·`noUnusedParameters`·`noFallthroughCasesInSwitch`는 저장소 설정과 무관하게 끈다. 기준선의 `@typescript-eslint/no-unused-vars`·`no-fallthrough`가 같은 구성을 보고한다.
- type-aware 규칙(`recommendedTypeCheckedOnly`)은 eslint-recommended를 빼서 기준선과 겹치지 않는다. async·경계 검사(no-floating-promises, no-misused-promises, require-await, no-unsafe-*)는 이 규칙이 맡는다.
- TypeScript의 Promise 거부 사유는 기존 `@typescript-eslint/prefer-promise-reject-errors`로 tsconfig 프로그램 안에서 검사한다. 기준선에는 이 검사가 없으므로 타입 검사 작업을 계획할 수 없거나 타입 공백으로 작업이 실패하면 TS 파일에서 해당 진단을 얻을 수 없다.
- JS 기준선은 core 규칙을 유지한다. no-undef는 이 관리 설정이 전역을 선언하지 않으므로 JS에서도 끈다.

### JS/TS 종료 코드와 결과

| 도구 | 종료 코드 | 판정 |
|---|---|---|
| eslint | 0 오류 없음(경고 가능), 1 오류 메시지(치명 구문 오류 포함), 2 crash·설정 오류·대상 없음 | exit 0인데 오류가 있거나 exit 1인데 오류가 없으면 `exit_status_mismatch`. 경고만 있는 성공은 `success_with_findings`이며 원문 등급은 `warning`이다. 2와 "Oops! Something went wrong!"는 `eslint_failed`. 해석하지 못한 파일(치명 메시지)은 `parse-error` 관찰 하나와 `parse_error` 단위, 제약 `unparsable_files_not_analyzed`이고 나머지 파일로 성공한다. 해석한 파일이 없으면 `parse_errors` |
| tsc | 0 진단 없음, 2 진단 있음(`--noEmit`), 1 명령행 오류·설정 로드 실패 | 레코드 유무와 0/2가 어긋나면 `exit_status_mismatch`. 그 밖은 `tsc_failed` |

- 파일 상태: eslint는 `linted_clean`·`linted_with_findings`·`parse_error`·`outside_tsconfig_program`·`not_linted`·`unknown`, tsc는 `checked`·`not_verified`·`dependency_gap`·`outside_tsconfig_program`이다. tsc의 `files_in_scope`는 모듈의 JS/TS 파일 수이고 프로그램 루트가 아닌 파일은 건너뜀으로 센다.
- 설정 오류(`tsconfig_invalid`), 패키지 extends 미해석(TS6053 → `tsconfig_extends_unresolved`), 입력 없음(TS18003 → `unsupported`/`no_input_files`)은 성공이 아니다.

### JS/TS 모듈 의존 규칙

[dependency-cruiser](https://github.com/sverweij/dependency-cruiser/blob/main/doc/cli.md)는 전체 소스 범위에서 모듈 의존과 순환을 확인한다. 루트에 `.dependency-cruiser.json`이 있으면 선언된 JSON 규칙을 읽는다. 파일이 없으면 관리되는 순환 규칙을 사용한다. JavaScript·TypeScript 형식의 실행 가능한 설정과 webpack·resolver 등 프로젝트 모듈을 불러오는 옵션은 지원하지 않는다. 독립된 Node 도구 설치에 TypeScript가 있으면 TS 모듈도 읽을 수 있다.

Node 권한 모드가 사본 디렉터리와 운영자 도구 설치만 읽게 한다. 별도 프로젝트 구성이나 설치된 프로젝트 의존성을 사용하지 않고, 일반적인 소스 상대 import를 해석한다. 해석하지 못한 의존성과 도구가 다루지 않은 파일은 별도로 센다. JS/TS 입력 중 특정 파일·폴더만 고른 경우 전체 그래프에 대한 판정을 생략한다. 규칙 결과는 도구의 JSON으로 읽으며 선택 소스에서 유일한 import 위치를 확인할 때 근거로 연결한다.

- **따르지 않은 대상.** 출력 `modules`에는 분석한 소스 말고도 따라가지 않은 import 대상(Node core 모듈, 해석하지 못한 이름, `doNotFollow`에 걸린 `node_modules` 패키지)이 항목으로 들어 있다. 이 항목은 소스가 아니므로 분석 소스로 세지 않고 `target.properties.dependency_targets_not_followed`로 센다. 이런 대상을 금지한 규칙(`dependencyTypes: ["core"]`, `couldNotResolve` 등)의 위반은 해당 import 줄의 관찰로 남긴다. 이 항목을 소스로 읽어 출력 전체를 `output_unparsed`로 버리던 결함을 고쳤다.
- 같은 소스가 두 번 나오거나 형식이 맞지 않는 항목은 여전히 `output_unparsed`다. 분석한 소스가 하나도 없으면 `unsupported`(`dependency_cruiser_no_parsed_sources`)다. 어느 import에도 연결하지 못한 위반은 `target.properties.dependency_violation_unmapped`로 세고 제약 `dependency_violations_not_represented`를 남긴다(조용히 버리지 않는다).

실제 18.8.0으로 정상·순환·실행 가능한 설정 제외, 외부 패키지 import, core·미해석 대상 금지 규칙을 확인했다. [분석기 계약](../examples/analyzer-dependency-cruiser.yaml) · [관찰 예시](../examples/observations-dependency-cruiser.json)

## Rust 팩

cargo check(기준)와 clippy(추가 깊이)는 build script(`build.rs`), proc macro, 저장소 Cargo 설정을 사용할 수 있어 `RepositoryCode=true`다. [저장소 코드 실행 허가](#저장소-코드-실행-허가)를 적용한다. 기준 분석이 허가 문제로 `unsupported`이면 보고서는 `partial`(`required_task_not_succeeded`)이다.

README 예제를 rustdoc, 공개 함수 구현과 테스트 소스로 잇는 [모델 검토](REVIEW.md#문서-발췌documents)는 Cargo를 실행하지 않는다. 빌드 구성에서 rustdoc 참조를 검사하는 `cargo-doc`은 [문서 참조와 오류 반환 검사](#문서-참조와-오류-반환-검사)에서 다룬다.

### 도구와 rustup proxy 거부

- `--cargo`는 toolchain의 실제 `cargo`, `--clippy`는 같은 디렉터리의 `cargo-clippy`다. `--clippy`만 주면 clippy는 `toolchain_not_configured`, 두 경로가 다른 toolchain 디렉터리면 `invalid_config`(`tool_unavailable`)다. `rustc`는 cargo 옆의 실제 바이너리다.
- **rustup proxy는 받지 않는다.** proxy는 저장소의 `rust-toolchain.toml`이 toolchain을 고르게 하고 없는 toolchain을 내려받을 수 있다. 경로를 해석한 결과가 `cargo`가 아니거나 같은 디렉터리에 `rustup`이 있으면 거부한다. env.discover도 거부한 경로를 실행하지 않는다.
- 실제 바이너리를 쓰므로 `rust-toolchain.toml`은 효과가 없고, 있으면 `rust_toolchain_file_ignored`를 남긴다.
- `rust`는 env.discover 런타임 이름으로 예약되어 있어 도구 이름은 `cargo`·`clippy`다.

### Rust 환경

환경은 명시적이고 상속하지 않는다.

| 변수 | 값 |
|---|---|
| `PATH` | toolchain bin 디렉터리, 이어서 `--toolchain-path`의 실제 절대 디렉터리(toolchain 디렉터리와 같은 것은 뺀다). 의존성 받기(`cargo fetch`)는 toolchain bin 디렉터리 하나 |
| `RUSTC` | 그 디렉터리의 rustc 절대 경로 |
| `CARGO` | 운영자가 지정한 cargo(cargo-clippy가 부름) |
| `CARGO_HOME`, `CARGO_TARGET_DIR` | task scratch의 빈 디렉터리 |
| `CARGO_NET_OFFLINE=true` + `--offline --locked` | 내려받지 않음 |
| `CARGO_BUILD_TARGET` | `rustc -vV`의 host triple |
| `RUSTC_WRAPPER`, `RUSTC_WORKSPACE_WRAPPER` | 빈 값(저장소가 정한 wrapper를 실행하지 않음) |
| `CARGO_TARGET_<HOST>_LINKER` | `--toolchain-path`에서 찾은 `cc` 절대 경로(찾은 경우만) |
| `CARGO_TERM_COLOR=never`, `CARGO_TERM_PROGRESS_WHEN=never`, `CARGO_INCREMENTAL=0`, `LANG=C.UTF-8` | 결정적 출력 |

`RUSTFLAGS`는 주지 않는다. 환경의 `RUSTFLAGS`는 저장소 `build.rustflags`를 통째로 대체하기 때문이다.

### 저장소 Cargo 설정과 features·target

cargo는 작업 디렉터리의 조상마다 `.cargo/config.toml`(옛 이름 `.cargo/config`)을 읽는다. 모듈 디렉터리와 스냅샷 안 조상, 작업 사본 위의 디렉터리들(노드 data·scratch와 그 조상), `CARGO_HOME`(빈 scratch)이다. clippy의 `clippy.toml`도 조상에서 읽는다.

- 환경이 설정 파일보다 우선하는 키만 고정한다. `CARGO_TARGET_DIR`은 `build.target-dir`보다, `CARGO_BUILD_TARGET`은 `build.target`보다, 빈 `RUSTC_WRAPPER`는 `build.rustc-wrapper`보다 앞선다. 그래서 target은 항상 host, wrapper는 항상 없음이다(`host_target_only`, `rustc_wrapper_overridden`).
- **features와 target은 기본 feature와 host target 하나다**(`default_features_only`, `host_target_only`). `--all-features`는 쓰지 않는다. 서로 배타적인 feature와 선택 의존성이 오프라인에서 구해지지 않는 경우가 흔하기 때문이다. 다른 조합·교차 target은 계획하지 않는다. 명시 `--target`이면 cargo 문서상 `rustflags`가 build script·proc macro에 적용되지 않으므로, 그 부분에 `rustflags`가 필요한 저장소는 다르게 빌드될 수 있다.
- 그 밖의 설정(`rustflags`, `[env]`, `[alias]`, `[lints]`, profile)은 저장소 것을 따르고 규칙 문맥에 기록한다.

### 링커와 `--toolchain-path`

rustc는 build script와 proc macro를 링크하려고 `cc`를 PATH에서 찾는다. 분석기 생성 시 한 번 `--toolchain-path`의 절대 디렉터리에서 실행 가능한 `cc`를 찾아 그 절대 경로를 host linker 변수로 준다. build script는 링크된 뒤 실행되면서 다시 C 도구를 이름으로 찾는다(`cc` crate는 `cc`·`ar`를, 어셈블러가 필요하면 `as`를 부른다). 그래서 cargo check·clippy·rustdoc의 자식 PATH는 toolchain 디렉터리 뒤에 `--toolchain-path`의 디렉터리를 붙이고 `build_scripts_used_toolchain_path`를 남긴다([도구 경로](#도구-경로)). 이 PATH가 없으면 C 코드를 빌드하는 build script가 있는 crate가 모두 `build_script_failed`였다. 찾지 못하면 링크가 필요한 작업만 `failed`/`linker_unavailable`(configuration_error)이고 build script·proc macro가 없는 crate는 그대로 분석된다. 사용하면 `linker_from_toolchain_path`를 남긴다. 이 `cc`는 버전을 식별하지 않는다. Windows는 이 조회를 하지 않으므로 build script가 있는 crate는 `linker_unavailable`일 수 있다.

### 오프라인 의존성

의존성 받기가 없으면 `CARGO_HOME`은 scratch의 빈 디렉터리이고 cargo는 오프라인에서도 여기에 캐시를 쓴다. 운영자 레지스트리 캐시를 주는 방식은 없다. 의존성은 (1) 경로 의존성, (2) 저장소가 `cargo vendor`로 둔 소스와 그 source replacement 설정(`[source.crates-io] replace-with`)으로만 구한다. `deps.rust`가 받았으면 그 `CARGO_HOME`을 쓴다([Rust 의존성 받기](#rust-의존성-받기)). 스냅샷 안 디렉터리로의 source replacement는 자료로 허용한다. 저장소의 `[registries]`·`[net]`·`[http]`가 네트워크를 가리켜도 `--offline`과 `CARGO_NET_OFFLINE=true`가 막는다. 레지스트리 의존성의 소스가 없으면 `dependency_unavailable`이고 아무것도 내려받지 않는다. 받은 crate가 없을 때 `offline_scratch_cargo_home`을 남긴다.

### Rust 계획

- 모듈은 상위 디렉터리에 `Cargo.toml`이 없는 `Cargo.toml`의 디렉터리다(가장 바깥 판단은 조상의 `Cargo.toml` 유무). 가상 workspace와 멤버는 한 모듈이고 `--workspace`로 한 번에 실행한다. 멤버는 `coverage.units`(crate)로 보인다. `vendor`·`node_modules`·`target`도 제외한다.
- 멤버가 아닌 중첩 crate는 빌드에 들어가지 않는다. 실행 뒤 처리 파일과 범위 파일의 차이가 `files_unlisted`와 `rust_files_not_compiled`로 보이며 깨끗한 것으로 세지 않는다.
- 경로 범위는 범위와 겹치는 모듈의 작업만 만든다. cargo는 패키지 단위로만 범위를 줄이므로 모듈 전체를 컴파일하되 범위 밖 진단은 버리고 `observations_outside_scope_omitted`를 남긴다. 처리 파일과 `files_in_scope`도 범위 안만 센다.
- 범위에 `.rs`가 있는데 모듈이 없으면 `cargo-check` 자리 작업 하나(필수, `RepositoryCode=false`)가 `unsupported`(`cargo_manifest_missing` 또는 `scope_has_no_cargo_package`)로 끝난다. `RepositoryCode=false`인 이유는 허가 사유가 진짜 사유를 가리지 않게 하기 위해서다. clippy는 이 경우 아무것도 계획하지 않는다.

### Rust 실행과 lint 수준

```text
cargo check --offline --locked --message-format=json --workspace --all-targets
cargo-clippy clippy --offline --locked --message-format=json --workspace --all-targets -- --cap-lints warn -W clippy::undocumented_unsafe_blocks -W clippy::multiple_unsafe_ops_per_block -W clippy::panic_in_result_fn -W clippy::unwrap_in_result -W clippy::todo -W clippy::unimplemented
```

- 실행 순서는 `rustc.version`(`rustc -vV`) → `cargo.version` → (clippy: `clippy.version`) → `cargo.check` 또는 `clippy.check`다(아래 재실행이 있으면 그 뒤 `cargo.check.targets`·`clippy.check.targets`). 모듈 `Cargo.toml`은 실행 전에 manifest SHA-256과 대조한다.
- `--all-targets`는 라이브러리, 바이너리, 통합 시험, 예제, 벤치, 단위 시험 하니스를 모두 검사한다. 개발 의존성도 오프라인으로 구해져야 한다. 같은 소스가 두 번 컴파일되어 같은 진단이 두 번 나오므로 코드·수준·위치·메시지가 같은 진단은 합치고 `duplicate_diagnostics_merged`를 남긴다.
- **시험·벤치·예제 대상만 컴파일되지 않으면 그 종류를 빼고 다시 실행한다.** 첫 실행이 exit 101로 끝났고, stdout을 끝까지 읽었고, lint가 아닌 컴파일 오류가 모두 대상 종류 `test`·`bench`·`example`인 대상에서 났고, stderr가 의존성·lockfile·linker·build script·toolchain 같은 다른 원인을 말하지 않으면, 실패한 종류만 뺀 대상 플래그(`--lib --bins --tests --benches --examples` 중 남은 것)로 한 번 더 실행한다. 안정 toolchain에서 `#![feature]`를 쓰는 nightly 전용 벤치(E0554)가 실제 프로젝트에서 잰 경우다. 재실행은 새 `CARGO_TARGET_DIR`을 쓴다. 실패한 대상이 남긴 dep-info가 처리 파일로 읽히지 않게 하기 위해서다. 결과는 재실행의 판정이고 한계 `failing_target_kinds_excluded`, `target.properties.excluded_target_kinds`(뺀 종류)·`targets`(남은 선택), 선택(`rule_pack.selection`)에는 `--all-targets` 대신 남은 플래그를 적는다. 뺀 대상의 소스는 처리 파일이 아니어서 `rust_files_not_compiled`로 보인다. `--tests`를 빼면 라이브러리의 `#[cfg(test)]` 코드도 검사하지 않는다. 첫 실행의 원시 stream은 실행 기록으로 남는다. 오류가 하나라도 배포 대상(lib·bin·build script·proc macro)에 있으면 재실행하지 않고 전처럼 `package_errors`다. [A69](DECISIONS.md)
- **clippy는 `cargo-clippy clippy`로 직접 실행한다.** `cargo clippy`는 외부 하위 명령이라 저장소 `[alias] clippy = ...`가 가릴 수 있다. 내장 명령 `check`는 alias로 가릴 수 없다.
- **rustc 진단 중복 제거.** clippy는 rustc 진단도 함께 내므로 clippy 작업은 코드가 `clippy::`로 시작하는 진단만 남긴다(`rustc_diagnostics_excluded`). clippy 출력의 rustc 진단 집합은 cargo check 관찰 집합과 같다.
- **deny lint.** deny가 기본인 lint(rustc `overflowing_literals`, clippy correctness 그룹)가 있으면 exit 101이고 의존 crate가 컴파일되지 않는다. cargo check에서 lint 이름 코드의 `error`는 관찰(`raw_severity=error`)로 남기되 작업은 `failed`/`lints_denied`이고 처리 파일은 주장하지 않는다. 저장소가 `-D warnings`나 `#![deny(warnings)]`를 쓰면 경고가 있는 한 cargo check는 실패한다. clippy는 `--cap-lints warn`으로 수준을 낮춰 correctness lint가 실패한 작업에 갇히지 않게 한다(`cap_lints_warn`).

### unsafe·panic lint

clippy 작업은 allow-by-default인 여섯 lint를 `-W`로 켠다. 소스의 `#[allow]`는 그대로 이긴다.

| lint | 주제 | 범주 |
|---|---|---|
| `clippy::undocumented_unsafe_blocks` | `// SAFETY:` 설명 없는 unsafe 블록 | security |
| `clippy::multiple_unsafe_ops_per_block` | unsafe 블록 하나에 위험 연산 여럿 | security |
| `clippy::panic_in_result_fn` | `Result`를 돌려주는 함수 안의 panic | error_handling |
| `clippy::unwrap_in_result` | `Result`를 돌려주는 함수 안의 unwrap/expect | error_handling |
| `clippy::todo`, `clippy::unimplemented` | 남겨진 `todo!()`·`unimplemented!()` | error_handling |

기본으로 켜진 `clippy::missing_safety_doc`과 rustc `unsafe_op_in_unsafe_fn`은 그대로 보고된다. `unwrap_used`·`expect_used`·`panic`·`indexing_slicing` 같은 restriction lint는 시험 코드의 unwrap까지 보고해 출력의 대부분이 되므로 켜지 않는다. 범주는 직접 유지하는 표(`categoryOf`)로 정하며, 표에 없는 lint는 maintainability, rustc 오류 코드는 correctness다. 새 lint는 표를 고칠 때까지 maintainability로 보인다.

### Rust 규칙 문맥과 억제

| 필드 | cargo check | clippy |
|---|---|---|
| 규칙 팩 | `codeye-rust-cargo-check` v1 | `codeye-rust-clippy` v1 |
| selection | `--workspace`, `--all-targets` | 같음 + `--`, `--cap-lints`, `warn`, `-W <lint>`×6, `keep:clippy::*` |
| `repository_config` | `honored` | `honored` |
| `config_files` | 읽은 설정 파일, 열거할 수 없으면 `null` | 같음 + `clippy.toml` |
| `source_suppressions` | `honored` | `honored` |

- `config_files`에는 스냅샷 안의 모듈 디렉터리와 조상의 `.cargo/config.toml`·`.cargo/config`, `[lints]`·`[workspace.lints]` 표가 있는 `Cargo.toml`, (clippy) 모듈 아래와 조상의 `clippy.toml`·`.clippy.toml`을 적는다. 표가 없는 `Cargo.toml`은 적지 않는다. 버전·의존성 변경이 규칙 문맥 변경으로 보이지 않게 하기 위해서다. `edition`, `rust-version`, `[profile]`, `[package.metadata]`는 기록하지 않으므로 이것만 바뀌면 사라진 finding이 해결로 보일 수 있다.
- **작업 사본 위의 설정은 열거할 수 없다.** 실행 뒤 작업 사본 위 모든 조상(이름 그대로의 경로와 symlink를 해석한 경로)에서 위 설정 파일을 검사해 하나라도 있거나 확인할 수 없으면 `config_files=null`과 `external_cargo_config_present`·`repository_config_not_enumerated`를 남긴다.
- **소스 안 억제는 `honored`다.** `#[allow(...)]`가 숨긴 진단은 어떤 옵션으로도 억제됨으로 표시되어 나오지 않는다(`--force-warn`은 일반 경고로 내고 억제 표지가 없다). 사라진 Rust finding은 언제나 `absent_unverified`다.
- **stderr는 실패한 실행의 설명으로만 쓴다.** 종료 코드 0과 성공한 `build-finished`는 stderr가 무엇을 인용하든 성공이다. 0이 아닌 종료에서만 첫 열이 `error`인 cargo 자신의 줄을 사유 분류에 쓴다. `warning:` 줄(build script의 `cargo:warning=`)과 들여 쓴 줄은 증거가 아니다.

### Rust 결과 판정

경고만 있어도 exit 0, 오류가 있으면 101이다. 종료 코드와 stdout 마지막 `build-finished` 레코드가 일치해야 한다. stderr 문구 분류는 cargo 버전의 문구에 묶여 있어 문구가 바뀌면 `cargo_failed`로 떨어지며 성공이 되지 않는다.

| 관측 | 결과 |
|---|---|
| exit 0, `build-finished` 하나·`success:true`, 레코드 완독, 도구 오류·drift 없음, 처리 파일 있음 | succeeded |
| exit 0인데 `build-finished`가 없거나 `success:false` | failed / `build_exit_mismatch` |
| exit 101 + 컴파일 오류(코드 `E####`·코드 없음) | failed / `package_errors`, 오류는 `tool_errors`, 관찰 보존 |
| exit 101 + 컴파일 오류가 모두 시험·벤치·예제 대상 | 그 종류를 뺀 재실행의 판정, `failing_target_kinds_excluded` |
| exit 101 + lint 이름 코드의 `error`만 | failed / `lints_denied`, 관찰 보존 |
| exit 101 + 오프라인 의존성 없음 | failed / `dependency_unavailable`(configuration_error) |
| `--locked`인데 Cargo.lock 없음 / 갱신 필요 | failed / `lockfile_missing` / `lockfile_outdated`(configuration_error) |
| `linker 'cc' not found` | failed / `linker_unavailable`(configuration_error) |
| Cargo.toml 파싱 실패 | failed / `manifest_invalid`(configuration_error) |
| `rust-version`이 rustc보다 높음 | failed / `toolchain_unavailable`(configuration_error) |
| `failed to run custom build command` | failed / `build_script_failed` |
| rustdoc의 `document output filename collision`(같은 이름의 crate 둘을 문서화) | failed / `doc_output_collision`(configuration_error) |
| 그 밖의 종료 코드, 101인데 설명 없음 | failed / `cargo_failed` |
| 버전 출력 미인식 | failed / `tool_version_unrecognized` |
| stdout 잘림·손상·알 수 없는 레코드 | failed / `output_truncated`·`output_unparsed` |
| 성공이지만 범위의 소스를 하나도 컴파일하지 않음 | unsupported / `no_analyzable_crates` |
| 실행 뒤 작업 사본이 manifest와 다름 | failed / `workspace_drift` |

### 위치, 처리 파일, build script

- 진단의 primary span을 쓴다. cargo는 작업 디렉터리 기준 상대 경로(밖은 절대 경로)를 주며 모듈 디렉터리 기준으로 스냅샷 경로에 매핑한다. `[REDACTED]/`로 시작하는데 manifest에 없으면 scratch·toolchain·생성 파일로 보고 `outside_snapshot`이다.
- rustc `column_start`는 문자 수이므로 스냅샷 줄을 읽어 byte 열로 바꾼다(BOM·CRLF 처리).
- 처리 파일은 성공한 task에 한해 fresh `CARGO_TARGET_DIR` 아래 모든 dep-info(`*.d`)가 나열한 소스 중 manifest에 있는 `.rs`와 범위의 교집합이다. 컴파일되지 않은 파일(비멤버 crate, 비활성 feature·cfg)은 빠진다. dep-info를 읽지 못하거나 20,000개·128 MiB를 넘으면 성공이 아니다(`no_analyzable_crates`).
- crate 상태는 `analyzed_clean`, `analyzed_with_findings`, `failed_compile`이다(`coverage.unit=crate`).
- 코드와 위치가 있는 `warning`·`error`만 관찰이다. 없으면 버리고 `diagnostics_without_location_omitted`를 남긴다. 부가 span과 자식 진단 위치는 `related`, 제안 수정 수는 `suggested_fix_count`다.
- `build-script-executed` 레코드가 있으면 `build_script_executed`, proc-macro artifact가 있으면 `proc_macro_executed`를 남기고 `Composite.Run`이 `repository_code_executed`를 더한다.
- 실행 뒤 모듈의 `.rs`·`Cargo.toml`·`Cargo.lock`·Cargo 설정·`clippy.toml`을 manifest와 다시 대조한다. build script가 소스를 고쳐 쓰면 `workspace_drift`로 실패한다.
- 허가 아래의 build script·proc macro는 노드 계정이 읽을 수 있는 파일을 읽고 `cargo:warning` 등으로 출력할 수 있다. cargo JSON(`rendered` 포함)은 실행 stream으로 저장된다. 스냅샷 밖 위치의 진단은 위치 없는 관찰로 남지만 원시 stream까지 거르지는 않는다. build script는 분리된 자식을 만들 수 있으며 그 위험은 허가 범위 안에 있다. 신뢰 경계가 다른 저장소에는 허가를 주지 않거나 별도 계정·노드를 쓴다.

C/C++ 진단의 관련 위치는 도구가 반환한 순서로 한 건의 상세에 표시한다. Cppcheck의 위치 설명, clang-tidy의 메모와 GCC 파일 디스크립터 경로를 보존하고, `clang-analyzer-*`가 명시한 가정과 분기 선택을 구분한다. 실제 실행에 대한 증거가 아니다. 각 위치에서 확인한 파일·줄 및 raw 스트림의 바이트 구간을 남긴다. 이전 산출물에 바이트 구간이 없더라도 알려진 위치를 표시한다. 미해결 위치는 소스 발췌를 만들지 않는다. 코드 미포함 내보내기에서는 관련 메모와 코드 발췌를 제외한다.

## Java 팩

[PMD](https://docs.pmd-code.org/pmd-doc-7.29.0/pmd_userdocs_cli_reference.html)는 기준 소스 검사고 [SpotBugs](https://spotbugs.readthedocs.io/en/stable/running.html)는 바이트코드를 읽는 추가 검사다. 웹 서비스는 설치 설정의 `agent.tools.java`·`pmd`·`spotbugs`로 도구를 받는다. 절대 경로를 사용하고 설치한 JDK의 `java --version`으로 버전을 확인한다. Linux의 OpenJDK 17.0.21.1과 21.0.13.1에서 PMD 7.29.0과 SpotBugs 4.10.5를 실행했다. [PMD 배포](https://github.com/pmd/pmd/releases/tag/pmd_releases/7.29.0) · [SpotBugs 배포](https://github.com/spotbugs/spotbugs/releases/tag/4.10.5)

- Maven의 `pom.xml`과 정적으로 나열한 reactor 모듈, Gradle의 `build.gradle(.kts)`, `settings.gradle(.kts)`와 문자 그대로 적은 `include`를 읽는다. 선택 범위의 Java 파일은 가장 가까운 모듈에 배정한다. 환경 분석은 프로젝트의 빌드 종류와 빌드 루트, 읽은 컴파일러 release·source 및 Gradle toolchain 선언을 기록한다. Gradle 실행으로 결정되는 모듈, POM profile과 외부 상속·변수는 평가하지 않는다. 확인한 선언과 노드 관측 버전을 구분한다.
- PMD는 선택한 파일의 읽기 목록과 CODEYE 지정 Best Practices·Error Prone·Multithreading 규칙을 사용한다. 소스의 억제 표기는 적용한다. 캐시와 대상 프로젝트의 규칙·classpath를 사용하지 않는다. 확인한 release가 있으면 Java 구문 버전으로 지정한다. 파싱·규칙 설정 오류가 남으면 작업을 실패로 기록한다.
- SpotBugs는 같은 JDK의 `javac`로 attempt 작업 사본의 검증된 소스를 컴파일한다. 표준 `src/main/java`가 있으면 같은 모듈의 운영 소스를 컴파일 문맥으로 쓰고 결과의 근거는 사용자가 선택한 운영 소스에만 연결한다. 프로젝트 빌드·플러그인·annotation processor와 저장소의 기존 class/JAR는 사용하지 않는다. 클래스 산출물은 해당 작업의 임시 디렉터리에만 쓴다. 모듈 기술자(`module-info.java`)는 javac에 넘기지 않고 클래스를 이름 없는 모듈로 컴파일한다. 기술자를 넘기면 javac가 이름 있는 모듈로 컴파일해 모든 소스가 모듈 소스 경로에 있어야 하고 classpath의 JAR를 읽지 못한다(실측: 모든 파일이 `file should be on source path, or on patch path for module`). 뺀 수는 `java_module_descriptors_not_compiled`, 한계는 `java_module_descriptor_not_compiled`다. SpotBugs는 같은 클래스의 바이트코드를 읽으며 모듈 경계(`exports`·`requires`)는 검사하지 않는다. [A70](DECISIONS.md)
- SpotBugs의 XML 분석 오류·누락 클래스와 실패 코드는 별도 상태다. 도구가 보고한 클래스와 스냅샷의 선택 소스가 유일하게 대응하는 위치만 근거가 된다. 경로 범위 밖의 같은 모듈 운영 소스(컴파일 문맥)에 대한 보고는 관찰이 되지 않고 `java_findings_outside_scope`와 `observations_outside_scope_omitted`로 센다. 선택한 소스의 보고인데 BugInstance 아래 줄 범위가 없는 것(클래스·메서드 수준 보고)은 `java_findings_location_unmapped`다. 두 수는 보고서의 도구 행에만 남는다. 도구 진단은 스냅샷과 원시 stdout의 바이트 구간에 연결한다. 두 도구 모두 위치를 확인한 파일 수와 더 읽지 못한 범위를 보고한다.
- 모듈마다 선택한 스냅샷 파일 최대 4,096개를 처리한다. 파일당 2 MiB, 합계 64 MiB를 넘거나 UTF-8로 읽을 수 없는 소스는 제외 건수로 남긴다. 컴파일과 각 분석 명령은 각각 최대 2분이다.

### Java 로컬 클래스패스

운영자가 준비한 소스 외부의 JSON 파일을 `agent.java_classpath_catalog` 또는 별도 노드의 `--java-classpath-catalog`로 지정한다. `artifacts`의 각 항목에 정확한 `groupId:artifactId:version`과 소문자 SHA-256을 넣는다. 절대 `path`로 저장된 JAR를 지정하거나, `maven_repository` 아래 표준 Maven 경로와 일치하는 JAR를 쓴다. 예시의 Commons Lang 3.17.0은 `org/apache/commons/commons-lang3/3.17.0/commons-lang3-3.17.0.jar`에 둔다. 카탈로그가 없으면 JDK의 타입만 사용한다. [클래스패스 예시](../examples/java-classpath.json) · [Maven 아티팩트 좌표](https://maven.apache.org/repositories/artifacts.html)

- Maven은 모듈 POM의 직접 `compile`·`provided` JAR 의존성을 읽는다. 같은 POM의 단일 속성 참조와 직접 버전 관리 항목을 인식한다. Gradle은 최상위 `dependencies` 블록에서 `implementation`·`api`·`compileOnly`·`compileOnlyApi`의 한 줄 리터럴만 읽는다. 부모 POM·BOM·전이 의존성·동적 버전·버전 카탈로그와 다른 모듈의 빌드 결과는 해석하지 않는다. [Gradle 의존성 선언](https://docs.gradle.org/current/userguide/declaring_dependencies_basics.html)
- 정확한 좌표와 체크섬을 확인한 일반 JAR만 사용한다. ZIP과 클래스 항목의 형식을 확인한다. Maven 좌표 정보가 든 JAR는 선언과 일치하는 항목이 있어야 한다. 외부 경로를 가리키는 매니페스트 클래스패스와 중복 타입이 있는 조합은 거부한다. 확인한 사본만 작업별 임시 디렉터리에서 컴파일 클래스패스와 SpotBugs 보조 클래스패스로 넘긴다. 의존성을 다운로드하거나 Maven·Gradle을 실행하지 않는다. [SpotBugs 명령행 옵션](https://spotbugs.readthedocs.io/en/stable/running.html)
- 보고서는 서로 다른 직접 선언 수, 카탈로그 지정 JAR·로컬 미러별로 검증된 JAR 수, 확인하지 못한 직접 의존성 수, 적용한 클래스패스의 다이제스트를 기록한다. 확인하지 못한 전이 범위는 한계로 남는다. 선택한 소스에 필요한 타입이 없으면 컴파일 실패 또는 SpotBugs의 누락 클래스 상태를 표시한다.

[PMD 분석기 계약](../examples/analyzer-pmd.yaml) · [SpotBugs 분석기 계약](../examples/analyzer-spotbugs.yaml) · [PMD 진단 예시](../examples/observations-pmd.json) · [SpotBugs 진단 예시](../examples/observations-spotbugs.json)

## 문서 참조와 오류 반환 검사

- **Go errcheck.** go vet과 같은 패키지 패턴·모듈 캐시·오프라인 빌드 환경으로 호출자가 버린 오류의 위치를 찾는다. `go list`의 실제 선택 파일과 진단 줄을 고정 스냅샷에 연결한다. 코드에 명시적으로 처리한 오류나 활성 빌드에서 제외된 파일을 검사 완료로 보고하지 않는다. 패키지는 `-mod=readonly`로 읽는다. 모듈에 `vendor/modules.txt`가 있으면 go vet·Staticcheck(go 명령의 기본)처럼 `-mod=vendor`로 vendor 디렉터리에서 읽고 `vendor_directory_used`를 남긴다. 선택(`rule_pack.selection`)이 실제 플래그다. 이전에는 vendor를 쓰는 모듈에서도 `-mod=readonly`가 오프라인 proxy에 모듈을 물어 `errcheck_packages_unavailable`로 실패했다.
- **Rust rustdoc.** 저장소 코드 실행을 허용한 노드에서 `cargo doc --offline --locked --workspace --no-deps`로 현재 빌드 구성의 문서 참조를 확인한다. rustdoc 진단만 남긴다. 생성한 문서의 crate 진입 파일과 실제 진단 위치를 처리 범위에 기록한다. 나머지 하위 모듈의 문서화 여부는 이 결과로 확정할 수 없다.

## Go 내부 패키지 의존 관계

기존 go vet 작업이 남긴 `go list -e` 목록을 사용한다. 워커는 목록에 포함된 Go 파일을 고정 스냅샷에서 읽고 manifest 해시를 대조한 뒤 표준 Go 파서로 import 선언을 읽는다. 각 연결은 모듈 안의 확인된 두 패키지와 import가 나온 파일·실제 줄·해시로 구성한다. 운영 소스와 내부·외부 테스트 import를 나눈다. 테스트 열은 그 패키지를 import한 테스트의 소유 패키지를 센다. 테스트 실행 결과나 런타임 커버리지를 뜻하지 않는다.

같은 모듈의 목록과 물리 디렉터리를 대조한다. 중첩 모듈은 따로 처리하며 목록 밖 내부 패키지는 확인하지 못한 대상으로 센다. 선택 범위의 Go 파일 중 완전한 도구 목록에 이름이 없는 파일은 빌드 구성에서 제외됐다고 목록에 나온 파일 및 읽기·파싱 실패와 따로 센다. 패키지 전체가 빌드 조건에 걸리면 이런 파일이 생길 수 있다. 목록 출력이 불완전하면 이 수를 표시하지 않는다. Go 도구가 사용하는 빌드 구성(`CGO_ENABLED=0`, `GOWORK=off`, 도구 기본 태그)에서 선택한 파일만 읽는다. 경로 선택은 작업 계획에 잡힌 Go 패키지에 한정하며 선택 파일을 포함한 패키지의 다른 활성 파일도 포함될 수 있다. 파일 읽기가 모두 성공해도 선택 범위의 그래프는 부분 결과다. 목록이 불완전한 구성으로 저장소 전체의 순환 여부를 판정하지 않는다.

표에서 어떤 패키지와 테스트가 이 코드를 import하는지 확인한다. 운영 코드의 순환을 관찰하면 연결 하나마다 import 증거를 표시한다. 순환이 여럿 있더라도 한 경로를 표시한다. 간접 소비자는 확인한 두 연결의 예 한 개를 보여 준다. 변경과 테스트 범위를 고를 때 참고할 정보이며 이 결과로 발견 수나 품질 판정을 바꾸지 않는다. Go 목록을 확보하지 못한 작업은 그래프를 만들지 않는다. 한 모듈에서 최대 패키지 1,024개·파일 12,000개·연결 12,000개를 기록한다. 한 파일은 최대 1 MiB, 읽기는 64 MiB까지다. 보고서에 붙이는 그래프 산출물의 합은 4 MiB까지다. [그래프 산출물 스키마](../schemas/go-package-graph.schema.json)와 [실제 도구를 거친 합성 예시](../examples/go-package-graph.json)를 참조한다.

## 구조·중복 지표와 비밀값 팩

언어와 무관한(`ecosystem: any`) 추가 깊이 분석기 셋이다. **lizard**는 함수 복잡도, **jscpd**는 중복 블록, **gitleaks**는 비밀값을 본다. 공통 실행부는 `internal/worker/analysis/anypack`, 빌트인 연결은 `builtin/any_ecosystem.go`다.

- 셋 다 추가 깊이이므로 이 플래그만으로는 deep-review를 광고하지 않는다. 경로가 없으면 작업은 계획되고 `unsupported`(`tool_not_configured`)다.
- 저장소 코드를 실행하지 않는다(`RepositoryCode=false`). lizard는 넘긴 파일의 구문만 세고, jscpd는 토큰을 비교하고, gitleaks는 파일 바이트를 정규식으로 검사한다.
- 환경 공통값에 더해 lizard는 `PYTHONDONTWRITEBYTECODE=1`·`PYTHONNOUSERSITE=1`·`PYTHONIOENCODING=utf-8`, jscpd는 `JSCPD_NO_TIPS=1`·`JSCPD_CACHE_DIR`·`XDG_CACHE_HOME`(scratch)을 받는다. 네트워크를 쓰지 않는다. jscpd의 semantic 모드(모델 내려받기)는 켜지 않는다.
- **계획.** 분석 하나당 작업 하나(`lizard`, `jscpd`, `gitleaks`, 모듈 `.`, 패턴은 범위 루트)다. lizard·jscpd는 범위에 지원 언어 소스(`pack.SourceFiles`)가 있고 그 도구가 받을 파일이 하나 이상이면 언어와 관계없이 계획한다.
- **gitleaks 계획.** gitleaks는 모델 검토의 비밀값 선별이기도 하므로 언어 조건 대신 범위에 모델 입력이 될 수 있는 파일이 하나라도 있으면 계획한다(`envdoc.ModelInputPath`: 지원 언어 소스, 안내 문서(README·AGENTS·CONTRIBUTING 등 문서 분류), 모듈 manifest). 문서·설정만 있는 스냅샷의 문서도 내장 민감값 패턴 외에 gitleaks 선별을 받는다. gitleaks를 설정하지 않았으면 작업은 계획되고 `unsupported`(`tool_not_configured`)로 끝나 공백이 보인다. 이때 전체 검토 역할은 미선별 경로(`model_input_not_secret_screened`)를 그대로 따르고, 모듈 단위 slice는 보내지 않는다([검토](REVIEW.md#모듈-단위-검토-분할)).
- **입력 파일.** lizard와 jscpd는 C/C++, Python, JS/TS, Go, Rust, Java 소스만 받는다(lizard는 reader가 있는 확장자만). 숨김 디렉터리, `testdata`, `vendor`, `node_modules` 아래 파일은 `excluded_data_directory`로 센다. `third-party`·`third_party`·`thirdparty`라는 디렉터리 아래 외부 코드도 넘기지 않고 `excluded_third_party_directory`로 따로 센다(디렉터리 이름의 대소문자 무시). gitleaks는 범위의 모든 포함 파일(설정·문서·데이터 포함)을 본다.
- **링크.** 스냅샷은 symlink를 복사하지 않는다. 작업 사본에 링크나 특수 파일이 생기면 도구를 시작하지 않고 `failed`/`workspace_link_refused`다(lizard·jscpd는 넘길 파일과 조상, gitleaks는 검사 루트 아래 전체). gitleaks `--follow-symlinks`와 jscpd `followSymlinks`는 꺼져 있다.
- **사본 밖 결과.** 선택된 스냅샷 파일이 아닌 경로의 결과는 버리고 경로·문구를 저장하지 않으며 작업은 `result_outside_snapshot`으로 실패한다. 도구 오류 문구는 고정이고 건수만 담는다. lizard와 jscpd는 목록에 없는 파일의 결과를 형태 오류로 본다.
- **argv.** lizard·jscpd는 저장소 경로를 argv에 넣지 않고 scratch 목록·설정 파일로 넘긴다. gitleaks는 범위 루트를 `--` 뒤에 `.` 또는 `./<root>`로 넘긴다.
- stream에 남는 저장소 유래 문자열은 경로, 줄 번호, 규칙 이름, lizard 함수 이름, entropy뿐이다. 함수 이름 자체가 민감한 저장소는 관찰 문구를 비우는 코드 제외 내보내기를 쓴다.

### 지표는 결함이 아니다

lizard·jscpd가 내는 것은 구조 지표다. 지표는 임계값을 넘은 측정값일 뿐이고 결함, finding, 결함 수, 조치 수, 모델 검토 입력이 되지 않는다(결정 A28).

- envelope 관찰의 선택 필드 `metric{name, value, threshold, unit}`. `unit`은 `count`·`lines`·`tokens`이고 `value >= threshold`이며 억제될 수 없다.
- `obsdoc.Document.Active()`는 지표 관찰을 세지 않는다. 지표만 있는 성공은 `success_without_findings`이고, 지표만 있는데 `success_with_findings`를 주장하면 무효다.
- 보고서 decoder는 지표를 finding으로 만들지 않고, 지표를 `observation_ids`로 인용한 finding이 있는 보고서는 무효다. `summary.metric_observations`는 `raw_observations` 중 지표 관찰 수이며 0이면 생략한다. 화면·Markdown·HTML은 "구조 지표, 결함 아님" 표지를 붙이고, 웹 parser는 요약의 수와 `metric`을 가진 관찰 수가 다르면 보고서를 거부한다([보고서](REPORT.md)).
- 검토 해석 역할은 지표 관찰을 읽지 않고 제약 `metrics_not_defects`만 남는다.
- 지표는 `attention`을 만들지 않는다. 위치를 확인하지 못한 지표(예: `source_drift`)는 미해결로 세어 판정을 `inconclusive`로 둔다.
- 중복 블록의 상대 위치는 생략 가능한 보고서 필드다. 이전 보고서에서 이 위치를 기록하지 않았으면 저장한 첫 위치와 당시 측정값을 표시한다.

| 분석기 | 지표 | 임계값(규칙 팩 v2) | 근거 |
|---|---|---|---|
| lizard | `cyclomatic_complexity`(count) | CCN ≥ 16 | lizard 기본 경고 "CCN > 15"와 같음 |
| jscpd | `duplicated_lines`(lines) | minLines 10, minTokens 100, mode `mild` | 기본값(5줄·50토큰)은 짧은 반복까지 잡아 지표가 흐려짐 |
| jscpd | 파일 크기 | 1,000,000바이트 이하, UTF-8 | jscpd가 조용히 건너뛰는 파일은 넘기지 않고 `skipped_too_large`·`skipped_not_utf8`로 셈 |

임계값과 데이터 디렉터리 제외 목록은 고정이며 프로젝트별 조정 설정은 없다. 넘었다고 결함이 아니고, 못 넘었다고 구조가 좋다는 뜻도 아니다.

### lizard

- argv: `--xml --no-gitignore --input_file <scratch>/analyzers/lizard/files.txt`. `--xml`은 함수 이름(`fn(...) at file:line`), NCSS, CCN과 분석한 모든 파일을 준다. CSV는 `long_name`이 매개변수 기본값까지 복사해 그 안의 비밀값이 stream에 남으므로 쓰지 않는다. `-E` 확장(`-Eduplicate` 등)은 켜지 않는다.
- XML 모드는 자체 임계값과 관계없이 exit 0이므로 CODEYE가 임계값을 적용한다. 완전하게 파싱한 함수 수는 임계값 미만까지 포함해 `functions_measured`로 센다. Latin-1 파일도 해독해 측정한다.
- 규칙 문맥: `repository_config=overridden`(`config_files=[]`), `source_suppressions=honored`(`#lizard forgive`와 `GENERATED CODE` 표지가 함수를 기록 없이 숨김, 제약 `in_source_suppressions_honored`). `whitelizard.txt`는 XML에 영향이 없다. 함수 끝 줄은 기록하지 않는다(`function_end_lines_not_recorded`).

| 종료/출력 | 결과 |
|---|---|
| exit 0, XML 완전, 넘긴 파일 수 = 분석 파일 수 | 성공(`success_without_findings`) |
| exit 0이지만 XML 손상·모양 오류·파일 수 불일치 | `failed`/`output_unparsed` |
| exit ≠ 0 | `failed`/`lizard_failed` |
| 출력 잘림 | `failed`/`output_truncated` |
| 읽을 파일 없음 | `unsupported`/`no_analyzable_files` |

### jscpd

- argv: `<node> <run-jscpd.js> --config <scratch>/analyzers/jscpd/jscpd.json`. 설정의 `path`에 넘길 파일의 절대 경로를 적고 `output`은 scratch, `reporters=[xcode]`, `absolute`, `noGitignore`, `noTips`, `noColors`를 켠다. json 보고기는 중복된 소스 텍스트를 복사하므로 쓰지 않는다. xcode는 경로와 줄 범위만 stdout에 쓴다. complexity·dead-code·semantic·history 모드는 켜지 않는다.
- `--config`를 주면 저장소 `.jscpd.json`, `.config/jscpd.json`, `package.json`의 `"jscpd"` 키를 읽지 않는다(`repository_config=overridden`, `config_files=[]`). `jscpd:ignore-start`/`jscpd:ignore-end`는 블록을 기록 없이 숨기므로 `source_suppressions=honored`이고 사라진 지표를 개선으로 읽지 않는다.
- 중복이 있든 없든 exit 0이다. xcode 줄 `… Found N lines (s-e) duplicated on file …`의 N(=end−start)이 지표 값이고 N < minLines는 형태 오류다. 열은 기록하지 않는다(`clone_columns_not_recorded`). 두 블록의 스냅샷 위치를 각각 검증한다. 새 보고서에서 확인된 상대 블록은 `observation.metric.counterpart`에 남는다.
- 처리 파일은 CODEYE가 넘긴 목록이다(`processed_paths_from_input_list`). 임계값에 닿는 파일이 없으면 stderr "analyzed no files"를 `no_file_reached_clone_thresholds`로 남긴다.

| 종료/출력 | 결과 |
|---|---|
| exit 0, 레코드 수 = `Found N clones.` | 성공(`success_without_findings`) |
| 알 수 없는 stdout 줄, 요약 불일치 | `failed`/`output_unparsed` |
| 알 수 없는 stderr | `failed`/`jscpd_failed`(도구 오류 `tool_stderr`) |
| exit ≠ 0 | `failed`/`jscpd_failed` |
| 비교할 파일 없음 | `unsupported`/`no_analyzable_files` |

### gitleaks

gitleaks 탐지는 미검토 낮음 보안 finding이다. 비밀값 엔진은 gitleaks 하나이며, Python 팩은 하드코딩 암호 규칙(bandit B105~B107, ruff S)을 고르지 않는다. 다른 분석기가 같은 줄을 보고하더라도 finding 묶음 키에 분석기가 들어가므로 합치지 않는다.

- argv(범위 루트마다): `dir --no-banner --no-color --log-level info --redact=100 --config <scratch>/analyzers/gitleaks/codeye-gitleaks.toml --gitleaks-ignore-path <scratch>/analyzers/gitleaks/ignore --ignore-gitleaks-allow --max-archive-depth 0 --max-decode-depth 5 --exit-code 3 --report-format template --report-template <scratch>/analyzers/gitleaks/report.tmpl --report-path - -- ./<root>`. 보고서는 stdout으로 받는다. 기본 JSON 보고서의 `Match`·`Line`은 가려도 주변 텍스트를 담을 수 있어 쓰지 않는다.
- CODEYE 설정은 기본 규칙 확장(`[extend] useDefault = true`)이고 `--config`가 저장소 `.gitleaks.toml`을 대신한다. 사본 밖 `[extend] path`도 따라가지 않는다.
- 인라인 `gitleaks:allow`는 무시한다(`source_suppressions=disabled`).
- 실행 전에 범위 루트에 링크가 없는지 걸어서 확인한다. 이번 attempt의 `deps.javascript`가 `installed_dirs`로 적은 실제 디렉터리는 건너뛴다(설치 트리는 스냅샷 파일이 아니고, pnpm 배치는 링크를 쓴다). gitleaks 기본 allowlist가 `node_modules`를 읽지 않는다.
- **`.gitleaksignore`는 따른다.** gitleaks는 검사 루트의 `.gitleaksignore`를 항상 읽고 끌 수 없으므로 `repository_config=honored`이고 그 파일을 `config_files`에 넣는다. 스냅샷 사본에서 미리 읽어, `:`로 나눈 필드 중 하나라도 `/`·`\`로 시작하거나 `..` 구간·NUL이 있으면 gitleaks를 시작하지 않는다. 결과는 `failed`/`repository_config_rejected`와 `rejected_option_gitleaksignore`이며 항목 값은 기록하지 않는다.
- 기본 전역 allowlist(이미지·글꼴·문서 확장자, `go.mod`·`go.sum`, `vendor/modules.txt`·일부 vendor 경로, `node_modules`, lock 파일 등)에 걸린 파일은 gitleaks가 읽지 않으므로 `processed_paths`에서 뺀다(`skipped_default_allowlist`).
- 이진 파일도 검사하지만 UTF-8 텍스트가 아닌 파일은 처리 파일로 주장하지 않는다(`binary_files_not_claimed_processed`). 압축은 풀지 않고(depth 0) 인코딩된 값은 5단계까지 해독한다. git 이력은 검사하지 않는다(`history_not_scanned`).
- 열은 버린다(`secret_columns_not_recorded`). 8.30은 첫 줄 뒤에서 열을 1씩 크게 보고하는데 큰 파일 경계 동작을 측정하지 않아 보정하지 않는다. 위치는 줄 범위뿐이다.

| 종료/로그 | 결과 |
|---|---|
| exit 0, 로그 "no leaks found", 레코드 0, 종료 레코드 `findings:0` | 성공(`success_without_findings`) |
| exit 3, 로그 "leaks found: N" = 레코드 수 N > 0 | 성공(`success_with_findings`) |
| exit 1, 건너뛴 경로·오류 로그 | `failed`/`scan_incomplete`(루트별 도구 오류) |
| 종료 코드·로그·레코드 수 불일치 | `failed`/`exit_status_mismatch` |
| 템플릿 레코드 손상, 종료 레코드 없음 | `failed`/`output_unparsed` |
| 선택된 스냅샷 파일이 아닌 결과 | 레코드를 버리고 `failed`/`result_outside_snapshot` |
| 그 밖의 종료 코드 | `failed`/`gitleaks_failed` |

### 비밀값 처리

원래 비밀값, 그 일부, 소문자형, hex, hash는 다음 어디에도 없어야 한다. executor가 저장하는 stdout·stderr 산출물, envelope와 보고서, 전체·코드 제외 내보내기(JSON, HTML, Markdown), DB 행, 로그, 웹 UI, CODEYE가 만드는 모델 입력이다.

- gitleaks는 CODEYE 보고 템플릿으로만 출력한다. 템플릿은 규칙, 파일, symlink 표시, 줄·열, entropy만 읽고 `.Secret`·`.Match`·`.Line`·`.Fingerprint`·커밋·작성자는 읽지 않는다(`validate_bundle`이 템플릿 상수를 검사). `--redact=100`은 로그도 가린다. 로그 수준은 info로 고정한다(debug·trace는 원문과 해독값을 찍는다). `-v`는 주지 않는다.
- 관찰 문구는 고정 템플릿 `secret detected by gitleaks rule <rule> (value withheld)`이며 entropy와 위치도 넣지 않는다.
- fingerprint는 `report.ToolFingerprint(analyzer, rule, path, "", 고정 문구)`다. 비밀값 hash를 쓰지 않는다. 같은 파일에서 같은 규칙이 두 번 탐지되면 fingerprint가 같은 finding 두 개가 되고, 사람 판정은 이월되지 않으며, run 비교는 `ambiguous`로 표시한다.
- 보고서에는 코드 발췌가 없다. lizard 산출물은 함수 이름·줄·지표, jscpd 산출물은 경로·줄 범위만 담는다.
- 비밀값 finding 위치의 `blob_sha256`은 파일 전체 hash이며 스냅샷 manifest에도 이미 있다. 짧은 비밀값 한 줄만 담은 파일은 내용을 추측해 대조할 수 있다. 코드 제외 내보내기에서도 지우지 않는다.
- 비밀값 분석기는 envelope의 `analyzer.purpose=secrets`로 판별한다([분석기 목적](#분석기-목적)). 도구 이름 목록을 두지 않는다.

### 모델 입력의 비밀값 선별

검토 역할은 모델 입력을 만들기 전에 비밀값 분석기(`purpose=secrets`인 descriptor와 envelope)의 envelope로 파일을 거른다. 규칙은 `internal/worker/review/screen.go`에 있고, 세 경우(실행 안 됨 → 선별 없이 보내고 `model_input_not_secret_screened`, 실행했으나 쓸 수 없음 → 실행 안 됨과 같이 보내되 읽을 수 있는 결과가 보고한 파일은 빼고 `model_input_not_secret_screened`, 완전한 성공 → 탐지 파일을 모든 발췌에서 빼고 `secret_files_withheld`)의 처리는 [검토](REVIEW.md)가 정한다. 분석기 쪽에서 지켜야 할 것은 다음과 같다.

- "실행 안 됨"은 비밀값 작업이 계획에 없거나, `unsupported`이면서 `not_run`인 envelope(도구 미설정 등)일 때뿐이다. 계획에 있는데 envelope가 없거나 무효이면 쓸 수 없는 결과로 본다. 실패·부분 envelope라도 해석되면 그 관찰 경로는 모델 입력에서 뺀다. 워커는 계획 순서대로 작업을 하나씩 실행하고 검토 역할은 정적 계획 뒤에 붙으므로 검토가 읽을 때 비밀값 작업은 끝나 있다.
- "완전한 성공"은 `succeeded`, 부분 파싱 아님, parse OK다. 이때 탐지 파일에 위치한 다른 도구 관찰은 규칙과 위치만 보내고 문구를 뺀다. 하드코딩 암호 규칙처럼 문구가 값을 인용할 수 있기 때문이다.
- 검토 역할은 선별을 언어 지침 선택과 요청 작성보다 먼저 한다.

### 선별의 한계

- gitleaks 기본 allowlist에 걸린 파일(`go.mod`·`go.sum`, lock 파일, `vendor/` 일부, `node_modules`, 이미지 등)은 검사하지 않으므로 그 안의 비밀값은 탐지되지 않고 모델 입력에서도 빠지지 않는다. 플랫폼의 일반 민감값 마스킹(`record.Sensitive`)만 적용된다.
- 저장소 `.gitleaksignore`가 숨긴 탐지도 같다. 저장소가 받아들인 값으로 보고 따르며 그 파일은 digest와 함께 규칙 문맥에 남는다.
- 비밀값 규칙이 모르는 형식(임의 문자열 암호)과 git 이력 안의 지운 비밀값은 선별되지 않는다.
- pyright 메시지의 문자열 literal 값처럼 다른 분석기의 진단 문구는 이 팩이 가리지 않는다. 탐지된 파일에 위치한 관찰의 문구만 모델 입력에서 뺀다.

## Opengrep 패턴·taint 팩

`any.opengrep`은 CODEYE가 처음부터 쓴 규칙 팩 `codeye-taint`를 Opengrep으로 실행해 **한 파일 안에서** 요청 입력이 위험한 호출에 닿는 흐름을 찾는 추가 깊이 분석기다. 구현은 `internal/worker/analysis/opengrep`, 출력 해석은 [SARIF 입력 어댑터](#sarif-입력-어댑터), 빌트인 연결은 `builtin/any_ecosystem.go`다. [서술](../examples/analyzer-opengrep.yaml) · [실측 관찰 예시](../examples/observations-opengrep.json)

### 지원 범위

- **파일 안 흐름만.** 출발점과 위험한 호출이 같은 파일에 있어야 한다. 그 파일의 함수 사이 호출(인자 → 매개변수 → 반환값)은 따라간다. 파일 사이 흐름, 프레임워크가 주입하는 값, 전역 상태·DB·파일을 거친 흐름은 찾지 않는다(`intra_file_flows_only`). Opengrep 2.0.0에는 파일 사이 분석 스위치(`--enable-interfile`)가 있으나 켜지 않는다. 파일 사이 데이터 흐름을 분석했다고 표시하지 않는다.
- **출발점은 요청 입력뿐이다**(`request_input_sources_only`). 언어별 출발점은 아래 표와 같다. 명령행 인자, 그 밖의 환경변수, 표준 입력, 파일·DB에서 읽은 값은 출발점이 아니다.
- **규칙이 아는 API만.** 표에 적은 위험 호출과 값 정리 함수만 안다. 처음 보는 래퍼 함수, 사용자 정의 escape, 다른 라이브러리의 같은 동작은 놓치거나(미탐) 정리되지 않은 것으로 본다(오탐).
- **포함 검사 정리.** 경로 규칙은 `startsWith`·`HasPrefix`·`starts_with`·`strncmp` 같은 포함 검사를 받은 값을 그 뒤로 안전하다고 본다. 검사 전에 경로를 정규화했는지(`realpath`, `canonicalize`, `normalize`)는 확인하지 않는다.
- 언어 판정은 확장자다. Python `.py`, JS/TS `.js .jsx .mjs .cjs .ts .tsx .mts .cts`, Go `.go`, Java `.java`, C/C++ `.c .h .cc .cpp .cxx .c++ .hh .hpp .hxx .h++`, Rust `.rs`. 숨김·`testdata`·`vendor`·`node_modules`와 `third-party` 계열 디렉터리는 넘기지 않는다(구조 팩과 같은 선택).

| 언어 | 출발점 | 규칙(CWE) |
|---|---|---|
| Python | Flask `request.*`(args, form, values, json, data, cookies, headers, files, view_args, path, url, query_string, stream, get_data·get_json), Django·DRF·aiohttp형 `request.GET/POST/body/COOKIES/FILES/META/data/headers/cookies/query_params/path_params/match_info`, Starlette·aiohttp형 `await request.json()/form()/body()`, Flask 경로 변수(`@app.route("/<name>")` 뷰의 매개변수), `BaseHTTPRequestHandler`의 `self.path/headers/rfile.read` | command-injection(78), path-traversal(22), sql-injection(89), ssrf(918), unsafe-deserialization(502), code-evaluation(95), html-injection(79) |
| JavaScript·TypeScript | Express형 `req`·`request`의 query, body, params, headers, cookies, originalUrl, url, path와 `get()`·`header()`, Koa `ctx`(query, params, headers, `ctx.request.*`, `ctx.cookies.get()`), socket.io `socket.on` 처리 함수의 인자. HTML 출력 규칙만 브라우저의 `location.*`, `document.URL`, `document.documentURI`도 | command-injection(78), path-traversal(22), sql-injection(89), ssrf(918), unsafe-deserialization(502, node-serialize), code-evaluation(95), html-injection(79) |
| Go | 파일에서 형이 선언된 `*http.Request`(FormValue, PostFormValue, PathValue, URL.Query, URL.RawQuery, URL.Path, Form, PostForm, Header, Body, Cookie), `*gin.Context`, `echo.Context`의 요청 값, gorilla `mux.Vars`, chi `URLParam`·`URLParamFromCtx`, 부작용으로 채우는 `json.NewDecoder(r.Body).Decode(&v)`·gin `Bind*`·`ShouldBind*`·echo `Bind`의 대상 변수 | command-injection(78), path-traversal(22), sql-injection(89), ssrf(918), html-injection(79) |
| Java | 파일에서 형이 선언된 `HttpServletRequest`(패키지 이름을 쓴 형 포함)의 getParameter 계열·getParameterNames·getHeader·getHeaderNames·getQueryString·getPathInfo·getRequestURI·getCookies·getInputStream·getReader, Spring MVC `@RequestParam`·`@PathVariable`·`@RequestHeader`·`@RequestBody`·`@CookieValue` 매개변수 | command-injection(78), path-traversal(22), sql-injection(89), ssrf(918), unsafe-deserialization(502), code-evaluation(95), html-injection(79) |
| C·C++ | `recv`·`recvfrom`으로 받은 버퍼(버퍼의 오프셋 위치 `buf + n`에 받은 경우 포함), CGI 요청 변수(`QUERY_STRING`, `PATH_INFO`, `PATH_TRANSLATED`, `REQUEST_URI`, `SCRIPT_NAME`, `CONTENT_TYPE`, `HTTP_*`)의 `getenv`. `strcpy`·`strcat`·`snprintf`·`memcpy`·`realpath` 등의 복사를 따라간다 | command-injection(78), path-traversal(22), sql-injection(89), ssrf(918, libcurl) |
| Rust | axum·actix-web의 `Query`·`Path`·`Json`·`Form` 추출 매개변수(`web::` 접두 포함), actix `HttpRequest`의 query_string·uri·path·headers·match_info | command-injection(78), path-traversal(22), sql-injection(89), ssrf(918, reqwest), html-injection(79) |

규칙 ID는 `codeye.<언어>.<규칙>`이다(C·C++은 `codeye.c.*`). Go·C/C++·Rust에는 역직렬화·코드 평가 규칙이 없다. 표준 디코더(encoding/json, serde)는 코드를 실행하지 않고 언어에 eval이 없다. C/C++에는 HTML 출력 규칙이 없다. 규칙마다 한국어 설명(위험한 이유와 고치는 방법, CWE)이 보고서 규칙 설명 표(`internal/report/reportui/rule_descriptions_ko.json`, key `any.opengrep:<규칙 ID>`)에 있고, 보고서 제목은 그 첫 문장을 쓴다.

### 규칙 팩

- **출처.** 모든 규칙은 CODEYE가 각 언어·프레임워크의 공개 API를 보고 새로 썼다. Semgrep 레지스트리 규칙(Semgrep Rules License v1.0, [원문](https://github.com/semgrep/semgrep-rules/blob/develop/LICENSE))이나 라이선스를 확인하지 않은 규칙 집합을 복사하거나 고쳐 쓰지 않았다. 규칙 파일은 저장소의 다른 소스와 같은 조건이다.
- **내장과 식별.** 규칙 파일 `rules/<언어>.yaml`은 바이너리에 내장(`go:embed`)되고 실행마다 task scratch의 `rules/`에 써서 `--config`로 넘긴다. `rule_pack.selection`은 엔진 스위치와 **이번 실행이 불러온** 규칙 파일마다 SHA-256 앞 12자(`rules/python.yaml@…`)다. 범위에 없는 언어의 규칙 파일은 적지 않는다. 언어가 다른 두 실행은 규칙 문맥이 다르다. 규칙을 바꾸면 선택이 바뀌어 규칙 문맥이 달라진다. 팩 버전은 `codeye-taint` v2다. v2는 아래 [측정](#opengrep-규칙-측정)의 개발 부분에서 찾은 출발점·위험 호출·정리 함수를 더했다.
- **정밀도 우선.** 규칙마다 양성 fixture(`fixtures/taint/<언어>/<규칙>.*`, 위험 줄에 `expect: <규칙 ID>`)와 음성 fixture(`*_safe.*`: 인자 목록 실행, 매개변수 바인딩, 상수 호스트 접두사, escape, 숫자 변환, 포함 검사)가 있다. 새 출발점·위험 호출·정리 함수의 형태마다 양성과 음성 줄이 있다(프레임워크 출발점은 `framework_sources.*`). 정규식으로 묶은 이름 목록(요청 속성, 정리 함수 이름 등)은 목록마다 대표 이름 몇 개로 시험한다. 음성 줄마다 정리를 뺀 사본을 만들어 같은 규칙이 그 줄을 보고하는지 실제 Opengrep으로 확인했다(28줄, 저장소 밖에서 한 번 실행). 실제 Opengrep 실행에서 양성 122줄을 모두 찾고 음성에서 결과가 없어야 시험을 통과한다. 음성에서 정확하게 만들 수 없는 위험 호출은 규칙에 넣지 않았다(예: Go의 `$X.Get(a, b)` 형태 SQL 호출은 캐시 클라이언트와 구별할 수 없어 뺐다).
- **YAML.** Opengrep은 YAML anchor를 받지 않으므로 규칙 파일은 출발점·정리 블록을 규칙마다 펼쳐 쓴다.

### 실행

- 버전 probe(`opengrep --version`, 한 줄 `X.Y.Z`) 뒤 scan 한 번(파일 목록이 argv 한도를 넘으면 나눠 최대 64번)이다. 경로 인자의 한도는 Windows 24 KiB, 그 밖 96 KiB다. 경로마다 바이트 수에 `./`, 구분 공백, 공백이 든 이름을 감싸는 Windows 따옴표 두 개(5바이트)를 더해 센다. Windows 명령줄은 UTF-16 32767 단위까지이고 UTF-8 경로의 바이트 수는 UTF-16 단위 수보다 작지 않으며 나머지 argv는 2 KiB 아래다. Linux의 전체 인자 한도(`ARG_MAX`, 보통 2 MiB)보다는 충분히 작다. Windows 한도는 계산값이고 Windows에서 실행해 보지 않았다. 64번을 넘으면 실행하지 않고 `unsupported`/`too_many_files`다.
- argv: `scan --config <scratch>/…/rules/<언어>.yaml … --no-rewrite-rule-ids --sarif --dataflow-traces --taint-intrafile --json-output <scratch>/…/opengrep.scan.json --project-root . --quiet --jobs 4 --timeout 30 --timeout-threshold 3 --max-target-bytes 1000000 -- ./<파일>…`. cwd는 작업 사본, 환경은 공통값(`PATH`, `LANG`, `SystemRoot`)과 executor의 실행별 `HOME`·임시 디렉터리뿐이다. 범위에 있는 언어의 규칙 파일만 넘긴다.
- **한 번의 실행, 두 출력.** stdout의 SARIF가 원시 stream이고 관찰의 근거다. `--json-output`이 scratch에 쓰는 JSON 사본은 SARIF에 없는 두 가지, 검사한 파일 목록(`paths.scanned`)과 파일별 구조화 오류를 준다. 두 출력은 대조한다. 오류 수와 SARIF 알림 수가 같아야 하고, 억제되지 않은 결과의 (규칙, 경로, 줄) 다중집합이 같아야 한다(JSON 사본은 억제된 결과를 빼고 쓴다). 다르면 `output_unparsed`다.
- **네트워크를 쓰지 않는다.** 규칙은 로컬 파일로만 넘기고 레지스트리(`p/…`, `r/…`, `auto`), URL, `git+` 설정, `SEMGREP_RULES` 환경변수를 쓰지 않는다. Opengrep 2.0.0에는 지표 전송 옵션이 없다. 버전 확인도 하지 않는다. 시험 `TestRealOpengrepNoNetwork`(TC-291)가 팩이 만든 argv·환경 그대로 strace(`trace=network,process`)로 실행해 `socket`·`connect`·`sendto`·`bind`가 없고 `execve`가 하나뿐임을 확인했다. 재생 시험은 기록된 argv와 환경에 레지스트리·URL·파일 사이 스위치가 없음을 CI에서 검사한다.
- 1 MB를 넘는 파일(`files_over_size_limit_skipped`)과 UTF-8이 아닌 파일(`non_text_files_skipped`)은 CODEYE가 넘기지 않는다. 처리 파일이 아니며 단위 상태로 센다.

### 종료 코드와 결과

| 상황 | 판정 |
|---|---|
| exit 0, 두 출력 일치 | 결과가 있으면 `success_with_findings`, 없으면 `success_without_findings`. 결과가 있어도 exit 0이다(측정) |
| exit 4(규칙 패턴), 5(YAML), 7(설정 없음), 8(언어), 규칙 오류 알림, driver 규칙 목록이 내장 규칙과 다름 | `failed`/`rule_pack_load_failed`. 규칙을 읽지 못한 실행은 0건이 아니다 |
| 그 밖의 0이 아닌 종료(2 등) | `failed`/`opengrep_failed` |
| run의 `executionSuccessful=false` | `failed`/`opengrep_failed` |
| driver가 `Opengrep`가 아니거나 버전이 probe와 다름, `columnKind`가 바이트가 아님, 형태 오류, JSON 사본 없음·불일치 | `failed`/`output_unparsed` |
| 넘기지 않은 파일의 결과나 오류 | 그 결과는 버리고 `failed`/`result_outside_snapshot` |
| 넘긴 파일 하나에 대한 오류(수준·유형과 관계없이) | 그 파일은 처리 파일에서 빼고 `files_not_fully_analyzed`(파일 목록은 `target.properties.incomplete_files`). 나머지 파일의 결과는 유지하고 작업은 실패하지 않는다 |
| 파일이 없는 규칙 오류 / 파일도 규칙도 없는 실행 오류 | `failed`/`rule_pack_load_failed` / `failed`/`opengrep_failed` |
| 검사했다고 보고하지 않은 파일 | 처리 파일에서 빼고 `planned_files_not_scanned` |
| SARIF URI로 같은 경로를 돌려받을 수 없는 이름 | 넘기지 않고 `skipped_unmappable_name`, `file_names_not_mappable_skipped` |

파일별 오류의 실제 모양(Opengrep 2.0.0, `testdata/errors.json`에 캡처):

- 일부만 해석한 파일은 수준 `warn`, 유형 `["PartialParsing", [범위…]]`다. 끝나지 않은 JS 문자열, 본문 앞의 모르는 C 매크로에서 나왔다.
- 전혀 해석하지 못한 파일은 수준 `warn`, 유형이 문자열 `"Syntax error"`다. 1 MB 가까운 C 파일(civetweb.c, sqlite3.c)에서 나왔다.
- 이 두 경우에도 SARIF의 `executionSuccessful`은 true이고 종료 코드는 0이다. Python 2 `print` 문, 일부 깨진 JS·Go·C 파일은 오류 없이 지나갔다. 오류가 없는 파일이 끝까지 해석됐다는 보장은 도구가 주지 않는다.
- 유형의 모양을 모르면 유형 이름을 `unknown`으로 적는다. 오류가 있다는 사실만으로 그 파일을 불완전으로 센다(TC-304).

### 관찰

- 범주는 `security`, 원문 등급은 SARIF level(`error`·`warning`), 문구는 규칙 메시지(고정 영어 문장)다. 위치는 SARIF의 주 위치, 곧 규칙의 위험 호출에 일치한 식이다(`os.system(…)` 호출 전체, 측정). 열은 바이트 단위(`column_unit=byte`)다.
- **결과 순서는 실행마다 같다.** 같은 입력을 `--jobs 4`로 세 번 실행한 SARIF가 바이트까지 같았다(Java 600파일, 결과 168건). 관찰 순서와 `raw_ref`의 결과 번호는 이 순서를 따른다. 실제 엔진 시험 `TestRealOpengrepResultOrder`가 fixture를 세 번 실행해 관찰 순서·결과 pointer·SARIF 바이트가 같은지 본다.
- `--dataflow-traces`의 흐름 단계는 `related`의 `tool_flow_step`이다. 문구는 CODEYE가 만든 고정 문장(`taint flow step N of M: source|propagation|call|callee parameter|callee return|sink`)이고 코드를 인용하지 않는다. 각 단계는 원시 stream의 자기 byte 범위를 가리킨다. 64단계를 넘으면 앞 63단계와 마지막 단계만 남긴다(`flow_steps_truncated`).
- envelope의 `rules`에 이번 실행이 읽은 규칙의 ID, 짧은 설명, CWE가 있다. `target.properties`는 `analysis=intra_file_taint`, `sources=request_input`, `rule_languages`(넘긴 규칙 파일)다.
- **원시 stream에는 소스 코드가 있다.** SARIF는 일치한 코드 조각과 흐름 단계의 원문을 담는다(`tool_output_holds_source_text`). 관찰과 보고서 문구에는 넣지 않는다. 코드 제외 내보내기는 원시 stream을 내보내지 않는다.

### 규칙 문맥과 억제

- `repository_config=overridden`, `config_files=[]`. 파일을 명시해 넘기므로 저장소의 `.semgrepignore`·`.gitignore`는 적용되지 않는다(측정). 저장소 안의 규칙 파일·설정은 읽지 않는다.
- `source_suppressions=reported`. `nosemgrep`·`nosem` 주석(규칙 ID를 적으면 그 규칙만)은 도구가 적용하고 SARIF `suppressions`(`inSource`)로 알린다. 그 결과는 `suppressed=true` 관찰로 남고 발견이 되지 않는다. fixture에서 규칙 없는 주석과 같은 규칙 ID 주석은 억제되고 다른 규칙 ID 주석은 억제되지 않았다(TC-290).

### 엔진 중복

[엔진 중복](#엔진-중복) 절차대로 같은 fixture에 기존 분석기를 CODEYE 선택으로 실행해 같은 줄 보고를 쟀다. 모든 규칙에 다른 도구가 보고하지 않는 변형이 있어 규칙을 모두 남긴다. 같은 줄의 두 보고는 각 작업의 관찰로 남고 보고서는 합치지 않는다.

| 기존 분석기(선택) | 같은 줄에서 함께 보고 | Opengrep만 보고한 변형 | 기존 분석기만 보고(음성 fixture) |
|---|---|---|---|
| bandit 1.9.4 (B602·B605 셸 실행, B608 SQL 문자열, B310 urlopen, B301·B506 pickle·yaml, B307·B102 eval·exec, B703·B704 Markup) | Python 양성 23줄 중 11줄 | 경로 4줄 전부, 함수 사이 명령 흐름, requests SSRF, 템플릿 HTML, `yaml.unsafe_load`, `compile` | 셸 상수 명령·숫자 인자·quote 처리 등 음성 8줄(B605·B602·B608·B307·B301). bandit은 입력 출처를 보지 않는다 |
| ESLint 10.15 기준선(`@eslint/js` recommended) | 없음 | 전부 | 없음 |
| ESLint type-aware(`@typescript-eslint/no-implied-eval`) | TS의 `new Function(입력)` | `eval(입력)`, vm 실행, JS 파일 | `setTimeout(문자열)` |
| go vet, Staticcheck(SA·U1000), errcheck | 없음(errcheck는 무시한 오류 반환만) | 전부 | 없음 |
| SpotBugs 4.10.5(전체 검출기, `-low`) | 서블릿 XSS 2줄(`XSS_REQUEST_PARAMETER_TO_SERVLET_WRITER`), 경로 1줄(`PT_RELATIVE_PATH_TRAVERSAL`), SQL 1줄(`SQL_NONCONSTANT_STRING_PASSED_TO_EXECUTE`) | 메서드 사이 XSS, `Files.*`·`File.delete` 경로, JdbcTemplate SQL, Spring 매개변수 출발점, 명령·SSRF·역직렬화·SpEL | 포함 검사를 거친 경로 1줄(`PT_ABSOLUTE_PATH_TRAVERSAL`) |
| clang-tidy(`bugprone-command-processor`, cert-env33-c와 같은 검사) | `system`·`popen` 2줄 | `execl("/bin/sh", …, "-c", 입력)` | 상수·숫자 명령의 `system` 2줄 |
| cppcheck | 없음 | 전부 | 없음 |

- Java 측정은 fixture가 참조하는 서블릿·Spring API의 최소 stub으로 컴파일해 SpotBugs를 실행했다(저장소에 두지 않음). Rust clippy 선택(6개 lint)에는 이 구성을 보는 lint가 없어 실행하지 않았다.
- 측정은 이 fixture의 구성에 한정된다. 규칙을 바꾸면 다시 잰다. 이 표는 v1 fixture로 쟀고 v2에서 더한 줄로는 다시 재지 않았다. C 명령 fixture는 v2에서 바뀌지 않아 clang-tidy 행은 그대로 맞다.

### Opengrep 규칙 측정

**결론.**

- 이 팩이 믿을 만하게 찾는 것은 한 파일 안에서 요청 입력이 규칙이 아는 위험 호출에 닿는 흐름이다. 한 파일짜리 벤치마크 사례(Juliet Java의 한 파일 변형)에서 명령·SQL·HTML 재현율은 0.95이고 오탐은 0건이었다.
- 찾지 못하는 것은 파일을 건너는 모든 흐름(Juliet Java 여러 파일 변형 540건을 모두 놓침)과 [출발점 표](#opengrep-패턴taint-팩)에 적은 것 밖의 프레임워크 입력이다.
- 실제 애플리케이션 27개에서는 실제 흐름을 하나도 찾지 못했고, 오탐이거나 설계상 무해한 진단 10건(오탐 9, 무해 1)을 냈다.
- 그래서 이 팩은 진단마다 [분류](REVIEW.md#도구-진단-분류)가 필요한 선택형 깊이 분석기다. 결과가 없다는 것은 보안 검사 범위의 근거가 아니다. 기본 심각도도 이에 맞춰 낮췄다([기본 심각도](#기본-심각도)).

모델 호출 없이 규칙 팩 그대로(`TestRealOpengrepMeasureDirectory`, TC-306) 레이블 벤치마크와 공개 웹 애플리케이션을 실행해 쟀다. Opengrep 2.0.0, Linux x86-64. 제3자 소스는 저장소에 넣지 않았고 아래는 집계와 공개 프로젝트 이름뿐이다.

**분할과 오염.** 결과를 보기 전에 개발 부분과 보고 전용 부분을 정했다.

- 벤치마크는 시험 번호의 짝수가 개발, 홀수가 보고 전용이다. Juliet C/C++은 흐름 변형 번호(`_NN`)로 나눴다.
- 애플리케이션은 언어마다 개발용과 보고 전용을 라이선스·프레임워크로 골라 이름을 먼저 적었다.
- 규칙은 개발 부분의 놓친 사례와 오탐을 보고 고쳤다.

그러나 분할을 끝까지 지키지 못했다.

- 규칙을 고치는 동안 매번 채점 출력에 두 OWASP 벤치마크의 보고 전용 집계 행이 함께 보였다.
- 처음 정한 보고 전용 애플리케이션도 매번 함께 실행하고 진단을 읽었다.
- Python 정리 함수를 설계하면서 BenchmarkPython의 보고 전용 사례 하나를 열어 보았다.
- C/C++ 규칙을 고치면서 Juliet의 `_01`(홀수) 변형 파일을 보았다.

보고 전용 자료로 규칙을 고치지는 않았지만, 그렇지 않았다고 보일 방법이 없다. 그래서 **아래 OWASP 두 벤치마크와 Juliet C/C++의 홀수 부분, 처음 정한 보고 전용 애플리케이션은 모두 개발 숫자로 센다.**

규칙을 마지막으로 고친 뒤 처음 보는 자료를 따로 정해 최종 보고 전용으로 썼다. 레이블 집합인 Juliet Java 1.3과 애플리케이션 아홉 개(아래 표의 "새 보고 전용")다. 이 자료를 본 뒤에는 규칙을 고치지 않았다. 처음에는 h2o를 더해 열 개였지만, h2o를 처음 실행한 뒤 C 규칙의 `recvfrom` 수정(아래 Juliet C/C++)이 있었다. fixture에서 찾은 수정이고 고친 뒤 다시 실행한 h2o 결과도 같았지만, 그 실행 뒤에 규칙이 바뀌었으므로 h2o는 개발 숫자로 센다. Juliet Java는 Java 규칙만 쓰므로 이 수정과 관계없다. Python·C/C++용으로 보지 않은 레이블 집합은 따로 찾지 않았다. Juliet C/C++의 다른 출발점(환경변수, 파일, 콘솔)은 규칙의 요청 입력 출발점이 아니다.

**레이블 벤치마크.** 사례는 그 범주의 규칙이 시험 파일 안에서 결과를 냈으면 검출이다. 범주는 규칙이 다루는 것만 셌다.

| 벤치마크(고정) | 라이선스 |
|---|---|
| OWASP BenchmarkJava `10dfcc54` | GPL-2.0 |
| OWASP BenchmarkPython `f1291485` | GPL-3.0 |
| NIST Juliet C/C++ 1.3(CWE-78, CWE-23·36의 POSIX `char` socket 변형 1968파일) | 미국 정부 저작물 |
| NIST Juliet Java 1.3(CWE-78·89·80·81·83·23·36의 서블릿 출발점 `getParameter`·`getQueryString`·`getCookies` 변형 2175파일, zip SHA-256 `d985f417…`) | 미국 정부 저작물 |

**새 보고 전용: Juliet Java 1.3, v2 규칙.** 메서드 단위로 셌다(`bad` 메서드의 결과는 TP, `good` 메서드의 결과는 FP). 결과 651건은 모두 `bad` 메서드에 있었다. 전체 1,332사례의 재현율은 0.49(651건)다. 한 파일 변형은 0.82(651/792), 여러 파일 변형은 0이다. 여러 파일에 걸친 변형(`_51a`… 등)은 한 사례다.

| CWE | 한 파일 변형 TP/FN/FP/TN | 여러 파일 변형 TP/FN/FP/TN | 한 파일 변형 재현율 |
|---|---|---|---|
| 명령(78) | 63/3/0/66 | 0/45/0/45 | 0.95 |
| SQL(89) | 315/15/0/330 | 0/225/0/225 | 0.95 |
| HTML(80) | 126/6/0/132 | 0/90/0/90 | 0.95 |
| HTML 오류 메시지(81) | 0/66/0/66 | 0/45/0/45 | 0 |
| HTML 속성(83) | 63/3/0/66 | 0/45/0/45 | 0.95 |
| 상대 경로(23) | 42/24/0/66 | 0/45/0/45 | 0.64 |
| 절대 경로(36) | 42/24/0/66 | 0/45/0/45 | 0.64 |

한 파일 변형에서 놓친 141건의 원인:

- `response.sendError(…)` 위험 호출을 모델링하지 않음: 66 (CWE-81 전부).
- `getQueryString`을 잘라 읽는 `token.startsWith("id=")`를 경로 규칙이 포함 검사 정리로 봄: 44 (CWE-23·36).
- 클래스 필드를 거친 값(변형 45): 31.

여러 파일 변형 540건은 파일 사이 흐름이라 모두 놓쳤다. `good` 메서드에서는 결과가 없어 오탐은 0건이다.

BenchmarkJava, TP/FN/FP/TN(v1 → v2). 짝수·홀수 모두 개발 숫자다(위 오염).

| 범주(CWE) | 짝수 v1 | 짝수 v2 | 홀수 v1 | 홀수 v2 | 홀수 v2 재현율·오탐률 |
|---|---|---|---|---|---|
| 명령(78) | 15/49/1/63 | 18/46/1/63 | 7/55/7/54 | 10/52/7/54 | 0.16 · 0.11 |
| 경로(22) | 0/60/0/72 | 31/29/18/54 | 0/73/0/63 | 34/39/13/50 | 0.47 · 0.21 |
| SQL(89) | 54/84/12/104 | 89/49/19/97 | 48/86/15/101 | 77/57/23/93 | 0.57 · 0.20 |
| HTML(79) | 49/71/18/84 | 110/10/17/85 | 46/80/17/90 | 110/16/13/94 | 0.87 · 0.12 |

BenchmarkJava 홀수 부분의 놓친 사례와 오탐의 원인(v2):

| 범주 | 놓친 사례 | 오탐 |
|---|---|---|
| 명령 | 새 프로세스의 환경변수로 넘김 27, 실행 시점에 목록으로 만든 `ProcessBuilder` 인자 9, 변수에 둔 셸(`sh`, `-c`) 8, 다른 파일의 출발점 7, 다른 파일 메서드를 거친 흐름 1 | 목록·맵 원소 6, 상수 조건 1 |
| 경로 | `File.exists`·길이 같은 존재·메타데이터 검사만 함 26, 다른 파일의 출발점 8, 다른 파일 메서드를 거친 흐름 5 | 목록·맵 원소 7, 상수 조건 6 |
| SQL | 다른 파일에 선언한 JdbcTemplate 46, 다른 파일의 출발점 11 | 목록·맵 원소 15, 상수 조건 8 |
| HTML | 다른 파일의 출발점 16 | 목록·맵 원소 10, 상수 조건 3 |

BenchmarkPython, TP/FN/FP/TN. 짝수·홀수 모두 개발 숫자다(위 오염).

| 범주(CWE) | 짝수 v1 | 짝수 v2 | 홀수 v1 | 홀수 v2 |
|---|---|---|---|---|
| 명령(78) | 4/1/0/5 | 4/1/0/5 | 2/6/0/2 | 2/6/0/2 |
| 코드 평가(95) | 5/5/9/9 | 5/5/9/9 | 6/4/7/8 | 6/4/7/8 |
| 역직렬화(502) | 3/6/4/16 | 3/6/4/16 | 8/1/0/16 | 8/1/0/16 |
| 경로(22) | 4/24/15/42 | 9/19/18/39 | 13/24/6/40 | 20/17/7/39 |
| SQL(89) | 1/1/0/4 | 1/1/0/4 | 1/2/0/7 | 1/2/0/7 |
| HTML(79) | 0/13/0/31 | 7/6/6/25 | 0/18/0/27 | 10/8/5/22 |

홀수 사례의 원인(v2):

- 놓친 사례: 다른 파일의 출발점(`separate_request` 래퍼) 12, 모델링하지 않은 위험 호출 14, 사전·목록 원소 4, 문자열 slice 6, 헤더·폼 이름(`keys()`) 2.
- 오탐: 허용 목록·리터럴 비교를 정리로 보지 못함 5, 목록 원소 6, 상수 조건 3, 모르는 정리 함수 5.

Juliet은 함수 단위로 셌다(`bad` 함수의 결과는 TP, `good` 함수의 결과는 FP). v1은 결과가 0건이었다. v2는 `std::ifstream`·`std::ofstream` 위험 호출과 버퍼 오프셋 수신을 더했다. 모두 개발 숫자다.

| CWE | 위험 호출 | 짝수 v2 | 홀수 v2 |
|---|---|---|---|
| 23·36 | `ifstream`·`ofstream`(각각) | 20/28/0/48 | 26/22/0/48 |
| 23·36 | `fopen`·`open` | 0/48/0/48 | 0/48/0/48 |
| 78 | `system`·`popen`·`execl`·`execlp` | 0/48/0/48 | 0/48/0/48 |

- Juliet은 `fopen`·`system` 등을 매크로로 감싸 호출한다. Opengrep은 매크로 뒤의 호출을 보지 못해 그 위험 호출은 하나도 찾지 못했다. 실제 코드의 같은 호출은 fixture에서 찾는다.
- `fstream`의 놓친 사례는 여러 파일에 걸친 변형(짝수 88, 홀수 72)과 포인터·참조·union·함수 포인터·전역을 거친 한 파일 변형(짝수 24, 홀수 16)이다.
- 측정 뒤 새 출발점마다 양성·음성 줄을 맞추다가, `recvfrom`으로 버퍼 오프셋에 받은 뒤 종료 문자를 넣으면(`buf[n] = '\0'`) 흐름이 끊기는 것을 fixture에서 찾아 고쳤다(`recv`에는 있던 형태). 고친 뒤 Juliet C/C++, civetweb, lighttpd, h2o를 다시 실행했고 숫자는 같았다.

**레이블 집합이 없는 언어.** JS/TS·Go·Rust에서 라이선스가 분명한 웹 taint 레이블 벤치마크를 찾지 못했다. SecBench.js는 라이선스가 없어 쓰지 않았다. 이 언어들은 애플리케이션 진단만 쟀다.

**애플리케이션.** 고정 커밋의 저장소 전체를 팩 그대로 실행했다. 진단을 모두 읽고 한 줄 이유와 함께 판정했다. 판정은 세 가지다: 실제 흐름, 오탐, 실제 흐름이지만 설계상 무해. 숫자는 v2 규칙의 활성 진단이다.

처리 파일 수 뒤의 괄호는 일부만 해석해 처리 파일에서 뺀 파일 수다. "새 보고 전용"은 v2 규칙으로만 실행했다.

| 부분 | 언어 | 애플리케이션(커밋, 라이선스) | 처리 파일 | 진단 v1 → v2 | v2 진단의 판정 |
|---|---|---|---|---|---|
| 개발 | Python | CTFd/CTFd (`f9fa28b2`, Apache-2.0) | 522 (4) | 2 → 3 | 경로 3. 설계상 무해 1: 관리자 전용이고 경로 변환기가 `/`를 막는다. 오탐 2: `send_file`이 경로가 아니라 파일 객체를 받는다 |
| 개발 | Python | healthchecks/healthchecks (`c1039ad2`, BSD-3-Clause) | 700 | 1 → 0 | v1의 1건은 `urlencode` 결과의 HTML 출력으로 무해했다. v2에서 정리로 본다 |
| 개발 | JS/TS | louislam/uptime-kuma (`9431d555`, MIT) | 516 | 0 → 0 | |
| 개발 | JS/TS | umami-software/umami (`dbb8d606`, MIT) | 1518 | 0 → 0 | |
| 개발 | Java | spring-projects/spring-petclinic (`500158f7`, Apache-2.0) | 50 | 0 → 0 | |
| 개발 | Java | jhipster/jhipster-sample-app (`6b000b5d`, Apache-2.0) | 425 (1) | 0 → 0 | |
| 개발 | Go | gotify/server (`912af9f3`, MIT) | 215 | 0 → 0 | |
| 개발 | Go | go-shiori/shiori (`aebb4021`, MIT) | 229 | 0 → 0 | 출발점을 더한 중간 규칙이 오탐 5건(`strconv.Itoa`로 만든 숫자 ID)을 냈고 v2에서 숫자 변환을 정리로 본다 |
| 개발 | Rust | orhun/rustypaste (`507fcbd4`, MIT) | 13 | 6 → 0 | v1의 6건은 다른 파일의 `safe_path_join`으로 결합한 경로라 오탐이었다. v2에서 이름으로 정리로 본다 |
| 개발 | Rust | atuinsh/atuin (`2fdcb592`, MIT) | 632 (21) | 0 → 0 | |
| 개발 | C | civetweb/civetweb (`588860e3`, MIT) | 37 (1) | 실패 → 0 | v1 어댑터는 알림 메시지의 제어 문자로 `output_unparsed`였다. 본체 `civetweb.c`는 엔진이 해석하지 못해 처리 파일이 아니다 |
| 처음 정한 보고 전용(개발로 셈) | Python | getredash/redash (`f561a53d`, BSD-2-Clause) | 970 | 0 → 0 | |
| 처음 정한 보고 전용(개발로 셈) | JS/TS | hoppscotch/hoppscotch (`6177eae1`, MIT) | 1287 (4) | 4 → 4 | 오탐 4. 앱이 보내려고 만드는 클라이언트 쪽 `request` 객체를 요청 입력으로 보았다(SQL 3건은 이름이 같은 `execute`, SSRF 1건) |
| 처음 정한 보고 전용(개발로 셈) | Java | macrozheng/mall (`dcaa93b3`, Apache-2.0) | 524 | 1 → 1 | 오탐 1. 숫자 `@PathVariable Long`이 고정 호스트 URL에 붙는다 |
| 처음 정한 보고 전용(개발로 셈) | Go | miniflux/v2 (`5117f365`, Apache-2.0) | 453 | 0 → 0 | |
| 처음 정한 보고 전용(개발로 셈) | Rust | svix/svix-webhooks (`c5e2ded5`, MIT) | 2067 (8) | 0 → 0 | |
| 처음 정한 보고 전용(개발로 셈) | C | lighttpd/lighttpd1.4 (`2ddc5138`, BSD-3-Clause) | 174 (23) | 0 → 0 | |
| 새 보고 전용 | Python | netbox-community/netbox (`4cfaa8f7`, Apache-2.0) | 1354 (1) | 2 | HTML 2, 모두 오탐. `dcim/views.py`: 장치 이름은 `escape()`로 정리했고 남은 값 `device.get_absolute_url()`은 정수 기본 키로 만든 URL이다. `generic/object_views.py`: 같은 구성으로 객체 이름은 `escape()`, URL은 기본 키에서 만든다 |
| 새 보고 전용 | Python | saleor/saleor (`e1ccf4d5`, BSD-3-Clause) | 4373 (2) | 0 | |
| 새 보고 전용 | Java | elunez/eladmin (`646c58c3`, Apache-2.0) | 274 | 0 | |
| 새 보고 전용 | Java | yangzongzhuan/RuoYi (`d6708994`, MIT) | 376 (1) | 0 | |
| 새 보고 전용 | JS/TS | keystonejs/keystone (`25f427e8`, MIT) | 1008 (3) | 0 | |
| 새 보고 전용 | JS/TS | parse-community/parse-server (`a3f152f6`, Apache-2.0) | 377 | 0 | |
| 새 보고 전용 | Go | filebrowser/filebrowser (`833d9088`, Apache-2.0) | 181 | 0 | |
| 새 보고 전용 | Go | gogs/gogs (`c2380640`, MIT) | 691 (3) | 0 | |
| 새 보고 전용 | Rust | qdrant/qdrant (`23065a0b`, Apache-2.0) | 2344 (35) | 0 | |
| 새로 정했으나 개발로 셈 | C | h2o/h2o (`d3e43fcf`, MIT) | 761 (26) | 0 | 첫 실행 뒤 C 규칙이 바뀌어 개발로 옮겼다(위 분할). 바뀐 규칙으로 다시 실행해도 0건이다 |

- v2 진단은 모두 10건이고 실제 흐름은 없다. 개발 3건은 무해 1, 오탐 2다. 처음 정한 보고 전용(개발로 셈) 5건은 모두 오탐이다. h2o는 0건이다.
- 새 보고 전용 애플리케이션 아홉 개(Python 2, Java 2, JS/TS 2, Go 2, Rust 1)의 진단은 2건(netbox)이고 둘 다 오탐이다. 실제 흐름은 0건이다.
- 애플리케이션 27개에서 실제 결함을 하나도 찾지 못했다. 그래서 애플리케이션 측정은 오탐을 보여 줄 뿐이고 재현율의 근거가 아니다. 진단이 없는 애플리케이션에 흐름이 없다는 뜻도 아니다.
- 언어·규칙별 v2 진단:
  - Python 경로 3, Python HTML 2.
  - JS/TS SQL 3, JS/TS SSRF 1.
  - Java SSRF 1.
  - Go·Rust·C 0.
- `clang-tidy cert-env33-c`(`bugprone-command-processor`의 별칭, LLVM 23.1.5)는 C 명령 fixture에서 쟀다([엔진 중복](#엔진-중복-1) 표의 clang-tidy 행). 양성의 `system`·`popen`을 찾고 `execl("/bin/sh", "-c", 입력)`은 놓쳤다. 상수·숫자 명령의 `system` 두 줄도 보고했다. Juliet·애플리케이션에서는 재지 않았다.

**측정의 한계.** 아래는 v2 규칙이 다루지 않는다. 이 구성에서 결과가 없는 것은 결함이 없다는 뜻이 아니다.

- 파일 사이 흐름: 다른 파일의 출발점·래퍼·헬퍼(JdbcTemplate), 프레임워크가 주입하는 값.
- 매크로로 감싼 위험 호출(Juliet `fopen`·`system`), 포인터·참조·union·함수 포인터·전역·Java 클래스 필드를 거친 값, 상수 조건, 목록·맵 원소의 구분, Python 문자열 slice.
- 디코드한 본문은 통째로 오염된다. DRF serializer의 `validated_data`, 요청 본문으로 만든 모델 객체, Go에서 구조체로 디코드한 변수의 모든 필드가 출발점으로 이어진다(확인함). 숫자형 필드나 serializer 검증은 정리로 보지 않으므로 과하게 보고할 수 있다.
- NestJS 매개변수 데코레이터(`@Query()`, `@Body()`, `@Param()`): Opengrep 2.0.0이 javascript 언어에서 이 패턴을 해석하지 못해 출발점에 없다.
- socket.io는 `socket.on` 처리 함수의 인자만 본다. 다른 이름의 소켓 객체나 미들웨어는 보지 않는다.
- 위험 호출 밖의 동작: 새 프로세스의 환경변수(`exec(cmd, env)`), 실행 시점에 고른 셸, 존재·메타데이터 검사만 하는 경로 사용. Java `response.sendError(…)`의 HTML 메시지도 위험 호출로 보지 않는다.
- 정리: 허용 목록·정규식·리터럴 비교 같은 검증 분기는 정리로 보지 않는다. 경로 규칙의 포함 검사(`startsWith`·`HasPrefix` 등)는 검사한 문자열이 경로인지 보지 않는다. 질의 문자열 토큰의 `token.startsWith("id=")` 뒤의 값도 안전하다고 본다(Juliet Java 44건). Go의 `strings.HasPrefix` 검사는 부작용 정리로 보아 검사 뒤에는 어느 분기에서도 값을 안전하다고 본다. 거부 분기(`if !HasPrefix { 사용; return }`) 안의 사용도 보고하지 않는다(확인함). Python escape·Rust 경로 결합 정리는 함수 이름(`escape_html`, `safe_path_join` 등)으로 인정하므로 이름만 같은 다른 함수도 정리로 본다.
- 파일 내용: Python에서 파일에서 읽은 값(`open(…).read()`)은 경로 규칙 밖에서는 정리된 값으로 본다. 업로드 파일을 읽어 곧바로 쓰는 흐름은 놓친다.

### Opengrep 설치

Opengrep은 [opengrep/opengrep](https://github.com/opengrep/opengrep)의 LGPL-2.1 프로그램이다([LICENSE](https://github.com/opengrep/opengrep/blob/main/LICENSE)). CODEYE는 바이너리를 저장소에 넣거나 내려받지 않는다. 운영자가 설치한다.

1. [릴리스 v2.0.0](https://github.com/opengrep/opengrep/releases/tag/v2.0.0)에서 플랫폼 파일을 받는다. Linux x86-64는 `opengrep_linux_x86`이다(시험한 파일 SHA-256 `37e93f4da5fd04a18c5c1febbdd12f7e98dfccd4d0c9cf2593bcb25eeb462da0`). 같은 릴리스의 `.sig`·`.cert`로 서명을 확인할 수 있다(CODEYE 시험에서는 확인하지 않았다).
2. 이름을 `opengrep`으로 바꿔 `~/.local/share/codeye-tools/bin/`에 두고 실행 권한을 준다. 다른 위치면 `agent.tools.opengrep`(에이전트 플래그 `--opengrep`)에 절대 경로를 적는다.
3. `codeye tools`에서 `opengrep`이 `found`, 버전 2.0.0, `version_tested=true`인지 확인한다. `codeye doctor`도 같은 검사를 한다.

설치하지 않으면 지원 언어 소스가 있는 분석에서 작업이 계획되고 `unsupported`/`tool_not_configured`로 끝난다(0건 통과 아님). 실제 실행은 Linux x86-64에서만 확인했다. macOS·Windows 릴리스 파일은 시험하지 않았다.

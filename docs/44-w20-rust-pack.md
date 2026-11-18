# 44. W20 Rust 팩: cargo check와 clippy

W20은 [분석기 팩 계약](39-r1-analyzer-packs.md)의 두 번째 언어 팩이다. 기준 분석기 **cargo check**(`rust.cargocheck`, 필수)와 추가 깊이 분석기 **clippy**(`rust.clippy`)를 구현한다. 두 작업은 build script, proc macro, 저장소의 Cargo 설정을 실행하므로 모두 `RepositoryCode=true`이고 노드 운영자의 `--repo-code-source-root` 허가가 있는 소스에서만 실행한다.

검증 범위: Linux(WSL2) x86-64, rustc/cargo 1.99.1 (5f94df478 2026-08-27), clippy 0.1.99 (58cf8f9c35 2026-10-13)의 실제 실행. Windows와 macOS는 컴파일과 단위 시험만 대상이고 실제 cargo를 실행하지 않았다. CI에는 Rust toolchain이 없다. 실제 도구 시험은 `CODEYE_TEST_CARGO`·`CODEYE_TEST_CLIPPY`가 있을 때만 실행하고, CI에서는 캡처한 실제 출력과 dep-info를 재생하는 시험이 같은 판정 경로를 검사한다(§9).

## 1. 구성 요약

| 부분 | 위치 | 역할 |
|---|---|---|
| Rust 팩 | `internal/worker/analysis/rustpack` | 계획(`plan.go`), 실행(`rustpack.go`), cargo JSON parser(`parse.go`), 판정(`assemble.go`) |
| 빌트인 연결 | `internal/worker/analysis/builtin/rust.go` | `--cargo`·`--clippy` 설정, 시작 probe, 카탈로그 항목 |
| 서술 | [cargo check](../examples/analyzer-cargo-check.yaml) · [clippy](../examples/analyzer-clippy.yaml) | 실행 계약(`executes_repository_code: true`) |
| 실제 산출물 | [cargo check](../examples/observations-cargo-check.json) · [clippy](../examples/observations-clippy.json) | `fixtures/w20-rust/findings`에서 얻은 envelope |
| fixture | `fixtures/w20-rust/` | 의도한 결함이 있는 16개 프로젝트 |
| 캡처 | `internal/worker/analysis/rustpack/testdata/` | 고정 버전의 stdout·stderr·종료 코드·dep-info |

팩 계약의 변경은 하나다. `builtin.Config`에 `ToolPath`(운영자의 `--toolchain-path`, hello `tool_path`)를 더했다(§2.4). 추가 필드이며 기존 시험은 그대로 통과한다. 공유 카탈로그와 시험의 기대 목록에는 이 팩의 항목을 더했다.

**capability 광고.** cargo check가 Rust의 기준 분석기이므로 `--cargo`만 있고 `--go`가 없는 노드도 deep-review를 광고한다([§2.6](39-r1-analyzer-packs.md#26-필수required-규칙)의 "현재 기준 분석기는 go vet뿐"은 W20부터 사실이 아니다. 이 문장은 W19 기록이라 고치지 않았다). 그 노드에서 Go 소스는 `go-vet`·`staticcheck`가 `tool_not_configured`로 보이고 Rust만 분석한다(`TestCargoAloneAdvertisesDeepReview`).

## 2. 도구, toolchain, 환경

### 2.1 플래그와 도구

| 플래그 | 값 | 분석기 |
|---|---|---|
| `--cargo /abs/<toolchain>/bin/cargo` | toolchain의 실제 `cargo` | cargo check (기준) |
| `--clippy /abs/<toolchain>/bin/cargo-clippy` | 같은 디렉터리의 `cargo-clippy` | clippy (추가 깊이) |

- 도구 이름 `cargo`·`clippy`는 런타임 도구 이름(`rust` 등)과 겹치지 않는다. `rust`는 env.discover의 런타임 이름으로 예약되어 있다.
- clippy는 cargo가 필요하다. `--cargo` 없이 `--clippy`만 주면 clippy는 `toolchain_not_configured`다. 두 경로가 다른 toolchain 디렉터리에 있으면 `invalid_config`(`tool_unavailable`)다.
- **rustup proxy는 받지 않는다.** `~/.cargo/bin/cargo`는 proxy다. proxy는 저장소의 `rust-toolchain.toml`이 toolchain을 고르게 하고 없는 toolchain을 내려받을 수 있다. 경로를 해석한 결과가 `cargo`가 아니거나(심볼릭 링크의 proxy), 같은 디렉터리에 `rustup`이 있으면(하드링크의 proxy) 거부한다. `rustc`는 cargo 옆의 실제 바이너리다. 시험: `TestNewValidation`, `TestRealRustupProxyIsRefused`(실제 `~/.cargo/bin/cargo`).
- **env.discover도 거부한 경로를 실행하지 않는다.** `builtin.Probes`는 팩이 거부한 `--cargo`·`--clippy` 경로를 상대 경로 표지로 바꿔, 탐색이 그 경로를 실행하지 않고 `probe_failed`로 기록한다(`TestRustProxyIsNeitherUsedNorProbed`). proxy를 `--version`으로 실행하는 것도 toolchain 해석과 내려받기를 일으킬 수 있기 때문이다.
- 실제 바이너리를 쓰므로 `rust-toolchain.toml`은 효과가 없다. 채널 `1.0.0`을 적은 fixture에서도 rustc 1.99.1이 실행되는 것을 실측했다. 이 파일이 있으면 제약 `rust_toolchain_file_ignored`로 남긴다.

### 2.2 환경

모든 실행의 환경은 명시적이고 아무것도 상속하지 않는다.

| 변수 | 값 | 이유 |
|---|---|---|
| `PATH` | toolchain bin 디렉터리 하나 | cargo가 rustc와 같은 toolchain의 `cargo-clippy`를 찾는다 |
| `RUSTC` | 그 디렉터리의 rustc 절대 경로 | |
| `CARGO` | 운영자가 지정한 cargo | cargo-clippy가 cargo를 부른다 |
| `CARGO_HOME`, `CARGO_TARGET_DIR` | task scratch의 빈 디렉터리 | 사용자·저장소 홈을 읽지 않고, 산출물이 작업 사본·스냅샷에 쓰이지 않는다 |
| `CARGO_NET_OFFLINE=true` + `--offline --locked` | | 내려받지 않는다 |
| `CARGO_BUILD_TARGET` | `rustc -vV`의 host triple | 빌드 target 고정(§2.3) |
| `RUSTC_WRAPPER`, `RUSTC_WORKSPACE_WRAPPER` | 빈 값 | 저장소가 정한 wrapper를 실행하지 않는다(§2.3) |
| `CARGO_TARGET_<HOST>_LINKER` | 절대 경로의 `cc` | 링커(§2.4), 찾은 경우만 |
| `CARGO_TERM_COLOR=never`, `CARGO_TERM_PROGRESS_WHEN=never`, `CARGO_INCREMENTAL=0`, `LANG=C.UTF-8` | | 결정적 출력 |

`RUSTFLAGS`는 주지 않는다. 환경의 `RUSTFLAGS`는 저장소 `.cargo/config.toml`의 `build.rustflags`를 통째로 대체하므로, 저장소가 정한 `--cfg`·`-D warnings` 등을 따르려면 비워 둬야 한다.

### 2.3 저장소의 Cargo 설정과 고정하는 값

cargo는 작업 디렉터리의 조상마다 `.cargo/config.toml`(과 옛 이름 `.cargo/config`)을 읽는다. 읽는 곳은 (1) 모듈 디렉터리와 스냅샷 안의 조상 디렉터리, (2) 작업 사본 위의 디렉터리들(노드의 data·scratch 디렉터리와 그 조상), (3) `CARGO_HOME`(빈 scratch)이다. 시험으로 (2)가 실제로 읽힌다는 것을 확인했다. 작업 사본의 조상에 `build.rustflags = ["-A", "unused_variables"]`를 둔 설정은 진단을 지웠다(실측). clippy의 `clippy.toml`도 조상 디렉터리에서 읽는다(실측).

환경이 설정 파일보다 우선하는 키만 고정한다. `CARGO_TARGET_DIR`도 저장소의 `build.target-dir`보다 앞선다(실측: `workspace` fixture의 `target-dir = "repo-target"`가 작업 사본에 만들어지지 않음). 실측: `.cargo/config.toml`에 `build.target = "wasm32-unknown-unknown"`과 `build.rustc-wrapper = "/bin/false"`를 둔 저장소에서 환경 없이 실행하면 wrapper가 실행되어 실패했고, `CARGO_BUILD_TARGET`과 빈 `RUSTC_WRAPPER`를 주면 host target으로 정상 실행됐다. 그래서 target은 항상 host, wrapper는 항상 없음이다(제약 `host_target_only`, `rustc_wrapper_overridden`). 명시적인 `--target`은 cargo 문서상 `RUSTFLAGS`·`build.rustflags`가 build script와 proc macro(host 산출물)에는 적용되지 않게 한다. 이 동작은 이 팩에서 실측하지 않았다. 따라서 `rustflags`가 build script·proc macro의 컴파일에 필요한 저장소는 그 부분이 다르게 빌드될 수 있다(남은 제한). 그 밖의 설정(`rustflags`, `[env]`, `[alias]`, `[lints]`, profile)은 저장소 것을 따른다. 이것들은 규칙 문맥(§5)에 기록한다.

### 2.4 링커와 `--toolchain-path`

rustc는 build script와 proc macro를 링크하려고 `cc`를 PATH에서 찾는다. PATH가 toolchain 디렉터리뿐이면 `linker 'cc' not found`로 build script가 있는 모든 crate가 실패한다(실측). 새 플래그를 만들지 않고 기존 운영자 설정 `--toolchain-path`(기본 `/usr/bin:/bin`)를 쓴다.

- `builtin.Config.ToolPath`가 그 목록을 받는다. 분석기 생성 시 **한 번** 목록의 절대 경로 디렉터리에서 실행 가능한 `cc`를 찾고, 그 절대 경로만 host target의 linker 환경 변수로 준다. 목록 자체를 자식의 PATH로 주지 않는다. `cc`가 `ld`를 찾는 것은 gcc 자신의 설치 경로 규칙이다(실측: PATH가 toolchain 디렉터리뿐이어도 링크됨).
- `cc`를 찾지 못하면 링크가 필요한 작업만 `failed`/`linker_unavailable`(configuration_error)이다. build script와 proc macro가 없는 crate는 링크가 필요 없어 그대로 분석된다. 사용한 경우 제약 `linker_from_toolchain_path`를 남긴다. Windows는 이 조회를 하지 않는다(MSVC 링커는 자신의 환경이 필요하다).

### 2.5 CARGO_HOME과 오프라인 의존성

`CARGO_HOME`은 작업 scratch의 빈 디렉터리다. 운영자 레지스트리 캐시를 읽기 전용으로 주는 방식은 만들지 않았다. 실측: cargo는 오프라인에서도 `CARGO_HOME`에 `.package-cache`·`.global-cache`와 `registry/`를 쓴다(`TestRealVendoredDependencies`). 읽기 전용 캐시를 줄 수 없다. 의존성을 구하는 방법은 (1) 경로 의존성, (2) 저장소가 `cargo vendor`로 둔 소스와 그 source replacement 설정(저장소 `.cargo/config.toml`)뿐이다. (2)는 실측했다: `vendored` fixture는 `[source.crates-io] replace-with`와 `vendor/` 디렉터리로 scratch `CARGO_HOME`에서 오프라인으로 성공한다. 저장소의 `[registries]`·`[net]`·`[http]` 설정이 네트워크를 가리켜도 `--offline`과 `CARGO_NET_OFFLINE=true`가 접근을 막는다([docs/05](05-source-and-environments.md): 저장소 설정이 registry·proxy를 대체하지 못한다). 스냅샷 안 디렉터리로의 source replacement는 자료이므로 허용한다. 레지스트리 의존성이 있는데 그 소스가 없으면 `dependency_unavailable`(configuration_error)이고 아무것도 내려받지 않는다(시험: scratch `CARGO_HOME`에 `.crate`가 없음). 제약 `offline_scratch_cargo_home`을 항상 남긴다. 운영자 캐시 지원은 남은 제한이다(§10).

## 3. 계획: 모듈, workspace, 범위

계획은 manifest와 scope만 읽는다. 도구가 없는 노드에서도 같은 작업을 계획해 공백을 보인다.

- **모듈**은 상위 디렉터리에 `Cargo.toml`이 없는 `Cargo.toml`의 디렉터리다. W15 탐색은 `Cargo.toml`마다 모듈을 만들어 workspace 멤버도 별도 모듈로 나열하지만(숨김·`testdata` 디렉터리 제외), 분석 작업은 cargo의 빌드 단위인 가장 바깥 `Cargo.toml`이다. 멤버는 `coverage.units`(crate)로 보인다. 분석 작업은 여기에 `vendor`·`node_modules`·`target`도 제외한다(vendor된 crate를 프로젝트 소스로 세지 않는다). 가장 바깥 판단은 정렬 순서가 아니라 조상 디렉터리의 `Cargo.toml` 유무다. 가상 workspace와 그 멤버는 한 모듈이고 `--workspace`로 한 번에 실행한다. 한 모듈에 한 작업이므로 workspace를 멤버별 작업으로 쪼개지 않는다.
- 키는 루트 모듈이 `cargo-check`, 나머지는 `cargo-check.m01`…이다(정렬 순서, 계획된 모듈만 센다). clippy는 `clippy`, `clippy.m01`…이다. 최대 64개다.
- **멤버가 아닌 중첩 crate**(`examples/other/Cargo.toml`이 루트 package의 workspace 멤버가 아닐 때)는 빌드에 들어가지 않는다. 계획이 TOML을 읽지 않으므로 계획에서는 알 수 없고, 실행 뒤 dep-info의 처리 파일과 범위 파일의 차이가 `files_unlisted`와 제약 `rust_files_not_compiled`로 보인다(§8). 깨끗한 것으로 세지 않는다.
- **경로 범위**(`scope.kind=paths`)는 범위와 겹치는 모듈의 작업만 만들고 `patterns`에 모듈 기준 상대 경로를 적는다. cargo는 패키지 단위로만 범위를 줄일 수 있으므로 모듈 전체를 컴파일하되, 범위 밖 경로의 진단은 버리고 제약 `observations_outside_scope_omitted`로 남긴다. 처리 파일과 `files_in_scope`도 범위 안만 센다.
- **공백**: 범위에 `.rs`가 있는데 모듈이 없으면 `cargo-check` 하나가 자리 작업(필수, `RepositoryCode=false`)으로 계획되고 `unsupported`(`cargo_manifest_missing` 또는 `scope_has_no_cargo_package`)로 끝난다. `RepositoryCode=false`인 이유는 허가 사유가 진짜 사유를 가리지 않게 하기 위해서다. clippy는 이 경우 아무것도 계획하지 않는다(공백은 한 번만 보인다).

## 4. cargo check와 clippy: 서로 다른 작업과 중복

### 4.1 argv

```text
cargo check --offline --locked --message-format=json --workspace --all-targets
cargo-clippy clippy --offline --locked --message-format=json --workspace --all-targets -- --cap-lints warn -W clippy::undocumented_unsafe_blocks -W clippy::multiple_unsafe_ops_per_block -W clippy::panic_in_result_fn -W clippy::unwrap_in_result -W clippy::todo -W clippy::unimplemented
```

- cwd는 작업 사본의 모듈 디렉터리다. 원본 경로와 스냅샷에서는 실행하지 않는다.
- **기본 feature와 host target 하나**다(제약 `default_features_only`, `host_target_only`). `--all-features`는 쓰지 않는다. 서로 배타적인 feature와 선택 의존성이 오프라인에서 구해지지 않는 경우가 흔하기 때문이다. 다른 feature·target 조합은 계획하지 않는다.
- **`--all-targets`**: 라이브러리, 바이너리, 통합 시험, 예제, 벤치와 단위 시험 하니스를 모두 검사한다. 시험 코드도 검토 대상이기 때문이다. 대신 개발 의존성도 오프라인으로 구해져야 한다. 같은 소스가 라이브러리와 그 시험 하니스에서 두 번 컴파일되므로 **같은 진단이 두 번 나온다**(실측: 경고마다 2건). 코드·수준·위치·메시지가 같은 진단은 하나로 합치고 제약 `duplicate_diagnostics_merged`를 남긴다.

### 4.2 clippy를 `cargo-clippy`로 직접 실행

`cargo clippy`는 외부 하위 명령이라 저장소의 `[alias] clippy = "check --message-format=short"`가 가린다(실측: 경고 `user-defined alias 'clippy' is shadowing an external subcommand`, 다른 명령이 실행됨). 그러면 clippy가 아닌 출력이 깨끗한 clippy 결과처럼 보일 수 있다. 그래서 `cargo-clippy clippy …`를 직접 실행하고 `CARGO`로 cargo를 알려 준다. 같은 alias fixture에서 정상 실행됨을 확인했다. 내장 명령 `check`는 alias로 가릴 수 없다(cargo가 무시).

### 4.3 엔진 중복

clippy는 rustc의 진단도 함께 출력한다. 기준 분석기가 cargo check이므로 clippy 작업은 코드가 `clippy::`로 시작하는 진단만 남기고 나머지를 버린다(제약 `rustc_diagnostics_excluded`). cargo check 작업은 `clippy::` 진단이 없다. 실측으로 확인했다: 같은 fixture에서 clippy 출력의 rustc 진단 집합(코드, 줄)이 cargo check 작업의 관찰 집합과 같다(`TestRealClippyRustcOverlapIsTheCheckTask`). 그래서 이 선택은 중복만 제거하고 다른 것을 버리지 않는다. 두 작업의 처리 파일은 보고서에서 합집합으로 센다.

### 4.4 lint 수준과 deny lint

rustc와 clippy에는 deny가 기본인 lint가 있다(rustc `overflowing_literals`, clippy `eq_op` 등 correctness 그룹). 실측: deny lint가 있으면 exit 101, `build-finished`는 `success:false`, 의존 crate는 컴파일되지 않는다. 진단 자체는 정상적으로 나온다(수준 `error`, 코드는 lint 이름).

- **cargo check**: 컴파일 오류 코드(`E0308` 등)나 코드가 없는 오류는 도구 오류(`package_errors`)다. lint 이름 코드의 `error`는 관찰(`raw_severity=error`)로 남기되 작업은 `failed`/`lints_denied`이고 처리 파일은 주장하지 않는다(프로젝트가 정한 대로는 컴파일되지 않는다). 저장소가 `-D warnings`나 `#![deny(warnings)]`를 쓰면 경고가 있는 한 이 작업은 실패한다. 의도한 결과다.
- **clippy**: `--cap-lints warn`을 주어 수준을 낮춘다(실측: 같은 fixture에서 `eq_op`·`overflowing_literals`가 `warning`으로 나오고 exit 0). 그래서 가장 가치가 큰 correctness lint가 실패한 작업 안에 갇히지 않는다. 제약 `cap_lints_warn`.

## 5. 규칙 문맥과 소스 안 억제

| 필드 | cargo check | clippy |
|---|---|---|
| 규칙 팩 | `codeye-rust-cargo-check` v1 | `codeye-rust-clippy` v1 |
| selection | `--workspace`, `--all-targets` | 같음 + `--`, `--cap-lints`, `warn`, `-W <lint>`×6, `keep:clippy::*` |
| `repository_config` | `honored` | `honored` |
| `config_files` | 읽은 설정 파일(아래), 열거할 수 없으면 `null` | 같음 + `clippy.toml` |
| `source_suppressions` | `honored` | `honored` |
| `unreported_suppression_rules` | `[]` | `[]` |

**저장소 설정은 따른다**(`honored`). 읽은 파일은 스냅샷 안의 다음 파일이고 manifest SHA-256과 함께 `config_files`에 적는다: 모듈 디렉터리와 조상의 `.cargo/config.toml`·`.cargo/config`, `[lints]`·`[workspace.lints]` 표가 있는 `Cargo.toml`, (clippy) 모듈 아래와 조상의 `clippy.toml`·`.clippy.toml`. 표가 없는 `Cargo.toml`은 적지 않는다. 버전·의존성 변경이 규칙 문맥 변경으로 보이면 고친 finding이 해결로 인정되지 않기 때문이다. `edition`, `rust-version`, `[profile]`은 기록하지 않는다(남은 제한).

**작업 사본 위의 설정은 열거할 수 없다.** cargo와 clippy는 작업 사본의 조상 디렉터리 설정도 읽는다(§2.3). 실행 뒤 작업 사본 위 모든 조상 디렉터리(이름 그대로의 경로와 심볼릭 링크를 해석한 경로 둘 다)에서 `.cargo/config.toml`·`.cargo/config`(clippy는 `clippy.toml`·`.clippy.toml`도)를 검사해 하나라도 있으면(또는 확인할 수 없으면) `config_files`를 `null`로 두고 제약 `external_cargo_config_present`·`repository_config_not_enumerated`를 남긴다. 그 분석기의 사라진 finding은 비교에서 해결되지 않는다(`rule_context_unknown`).

**소스 안 억제는 `honored`다.** `#[allow(...)]`가 숨긴 진단은 어떤 옵션으로도 억제됨으로 표시되어 나오지 않는다. 실측(`TestRealSuppressionsCannotBeReported`): fixture의 `#[allow(unused_variables)]` 변수는 보고되지 않고, `RUSTFLAGS=--force-warn=unused_variables`를 주면 보고되지만 일반 경고이고 억제를 알리는 표지가 없다(명령행으로 켠 다른 진단에만 "requested on the command line" 주석이 붙는다). 그래서 사라진 진단이 고쳐졌는지 allow가 붙었는지 구분할 수 없다. 규칙 팩은 `reported`를 주장하지 않으므로 `unreported_suppression_rules`는 `[]`이고(`honored`일 때만 허용되는 값), W23 비교에서 이 팩의 사라진 finding은 언제나 `absent_unverified`(`source_suppressions_hidden`)다(`TestRustFindingsCompare`).

**stderr는 실패한 실행의 설명으로만 쓴다.** 종료 코드 0과 성공한 `build-finished`는 stderr가 무엇을 인용하든 그대로 성공이다. 0이 아닌 종료일 때만, cargo 자신의 오류 줄(첫 열에서 `error`로 시작)만 사유 분류에 쓴다. `warning:` 줄(build script의 `cargo:warning=`이 아무 문구나 낼 수 있다)과 들여 쓴 줄(실패한 build script·도구의 stderr)은 증거가 아니다(`TestReplayStderrIsNotEvidenceOfAnythingButAFailure`).

규칙 선택(`-W` lint 목록, `keep`)이나 그 의미를 바꾸면 규칙 팩 버전을 올린다.

## 6. unsafe·panic 검토

clippy 작업은 allow-by-default인 여섯 lint를 `-W`로 켠다. 소스의 `#[allow]`는 그대로 이긴다.

| lint | 주제 | 범주 |
|---|---|---|
| `clippy::undocumented_unsafe_blocks` | `// SAFETY:` 설명 없는 unsafe 블록 | security |
| `clippy::multiple_unsafe_ops_per_block` | unsafe 블록 하나에 위험 연산 여럿 | security |
| `clippy::panic_in_result_fn` | `Result`를 돌려주는 함수 안의 panic | error_handling |
| `clippy::unwrap_in_result` | `Result`를 돌려주는 함수 안의 unwrap/expect | error_handling |
| `clippy::todo`, `clippy::unimplemented` | 남겨진 `todo!()`·`unimplemented!()` | error_handling |

기본으로 켜진 `clippy::missing_safety_doc`(unsafe 함수에 `# Safety` 절 없음)와 rustc의 `unsafe_op_in_unsafe_fn`은 각 분석기가 그대로 보고한다. `unwrap_used`·`expect_used`·`panic`·`indexing_slicing` 같은 restriction lint는 켜지 않았다. 실측: `--all-targets`에서는 라이브러리 `.unwrap()` 1건과 시험 코드 `.unwrap()` 5건이 모두 보고되어(6건) 시험이 많은 프로젝트에서 이 lint가 대부분의 출력이 된다. 사람이 읽을 수 있는 규모를 지키려는 선택이며 품질 개선 주장이 아니다.

범주는 lint의 **주제**이며 영향 판정이 아니다. CODEYE의 표(`categoryOf`)에 있는 lint만 correctness·security·error_handling·concurrency·performance·resource_lifecycle·test_gap이고, 표에 없는 lint는 maintainability, rustc 오류 코드는 correctness다. clippy의 JSON에는 lint 그룹이 없어서 표는 직접 유지하며 규칙 팩 버전과 함께 바뀐다. 원문 수준(`warning`/`error`)은 `raw_severity`로 그대로 두고, 보고서의 finding은 항상 `low`·`unreviewed`다. LLM의 unsafe 경계 검토는 R1 지침의 범위이고 이 팩은 도구 관찰만 만든다.

## 7. 실행, 종료 코드, 결과

task마다 실행 순서는 `rustc.version`(`rustc -vV`) → `cargo.version` → (clippy: `clippy.version`) → `cargo.check` 또는 `clippy.check`다. 앞의 식별이 실패하거나 인식하지 못하면 코드에 도구를 실행하지 않는다. 모듈 `Cargo.toml`은 실행 전에 manifest SHA-256과 대조한다.

cargo 1.99.1의 종료 코드를 실측한 결과다. 경고만 있어도 0이고 오류가 있으면 101이다. 그래서 종료 코드와 stdout 마지막의 `build-finished` 레코드가 일치해야 한다. 아래 사유 중 stderr 문구로 분류하는 것은 고정 fixture의 실제 출력을 캡처해 분류를 시험한다(의존성 없음, 잠금 파일 없음·갱신 필요, 링커 없음, 파싱 불가 manifest, 높은 `rust-version`, build script 실패). 문구 분류는 cargo 버전의 문구에 묶여 있으므로 다른 버전에서 문구가 바뀌면 `cargo_failed`로 떨어지고 성공이 되지는 않는다.

| 관측 | 결과 |
|---|---|
| exit 0, `build-finished` 하나이고 `success:true`, 레코드를 모두 읽음, 도구 오류·drift 없음, 처리 파일 있음 | succeeded / `success_without_findings` 또는 `success_with_findings` |
| exit 0인데 `build-finished`가 없거나 `success:false` | failed / `build_exit_mismatch` |
| exit 101 + 컴파일 오류(코드 `E####`·코드 없음) | failed / `package_errors`, 오류는 `tool_errors`, 관찰 보존 |
| exit 101 + lint 이름 코드의 `error`만 | failed / `lints_denied`, 관찰 보존 |
| exit 101 + 오프라인 의존성 없음 | failed / `dependency_unavailable` (configuration_error) |
| exit 101 + `--locked`인데 Cargo.lock 없음 / 갱신 필요 | failed / `lockfile_missing` / `lockfile_outdated` (configuration_error) |
| `linker 'cc' not found` | failed / `linker_unavailable` (configuration_error) |
| Cargo.toml 파싱 실패 | failed / `manifest_invalid` (configuration_error) |
| `rust-version`이 rustc보다 높음("rustc 1.99.1 is not supported by the following packages") | failed / `toolchain_unavailable` (configuration_error) |
| `failed to run custom build command` | failed / `build_script_failed` |
| 그 밖의 종료 코드, 101인데 설명 없음 | failed / `cargo_failed` |
| 버전 출력 미인식 | failed / `tool_version_unrecognized`, 분석을 실행하지 않음 |
| stdout 잘림·손상·알 수 없는 레코드 | failed / `output_truncated` / `output_unparsed` (parse 보존) |
| 성공이지만 범위의 소스를 하나도 컴파일하지 않음 | unsupported / `no_analyzable_crates` |
| 실행 뒤 작업 사본이 manifest와 다름 | failed / `workspace_drift` |
| timeout·취소·정리 불확실 | timed_out, cancelled, failed/`cleanup_uncertain` |

실패한 작업은 처리 파일을 주장하지 않는다(`processed_paths=null`). 레코드 모양은 고정 버전에 맞췄다. 알 수 없는 레코드 종류나 필드는 형태 오류이며 결과를 0건으로 만들지 않는다. 다른 버전도 형식이 맞으면 실행하고 `tool_version_untested`(cargo·clippy 계열), `rustc_version_untested`를 남긴다. 시험한 계열은 cargo 1.99, clippy 0.1.99, rustc 1.99다.

## 8. 위치, 처리 파일, 증거

- **위치**: 진단의 primary span을 쓴다. 파일 이름은 cargo가 작업 디렉터리 기준 상대 경로로(작업 사본 밖은 절대 경로로) 주며, 모듈 디렉터리 기준으로 스냅샷 경로에 매핑한다. 작업 사본의 절대 경로는 W05 redaction으로 지워지므로 `[REDACTED]/`로 시작하는 경로가 manifest에 없으면 scratch·toolchain·생성 파일로 보고 `outside_snapshot`으로 둔다(manifest에 없는 상대 경로는 `missing_file`). manifest의 SHA-256과 달라졌거나 줄이 범위를 벗어나면 위치를 해결하지 않는다.
- **열**: rustc의 `column_start`는 **문자 수**다(실측: 한글 4자 뒤의 열 56이 바이트 열 64). 스냅샷 파일에서 그 줄을 읽어 바이트 열로 바꾸고 `column_unit=byte`로 기록한다. BOM과 CRLF를 처리하고, 줄에 없는 열이면 `line_out_of_range`다.
- **처리 파일**: 성공한 task에 한해, 이 실행의 fresh `CARGO_TARGET_DIR` 아래 모든 dep-info(`*.d`) 파일이 나열한 소스 중 manifest에 있는 `.rs`와 범위의 교집합이다. rustc가 실제로 읽은 `mod` 파일을 포함하고 컴파일되지 않은 파일(비멤버 crate, 비활성 feature·cfg의 파일)은 빠진다. 범위 파일과의 차이가 `files_unlisted`다. dep-info를 읽지 못하거나 20000개·128 MiB 한계를 넘으면 처리 집합을 모르므로 성공이 아니다(`no_analyzable_crates`).
- **crate 상태**: manifest에 있는 package마다 `analyzed_clean`, `analyzed_with_findings`, `failed_compile`로 센다(`coverage.unit=crate`).
- **관찰**: 코드가 있고 위치가 있는 `warning`·`error`만 관찰이다. 코드나 위치가 없는 진단은 버리고 제약 `diagnostics_without_location_omitted`를 남긴다. 부가 span과 자식 진단(note·help)의 위치는 `related`, 제안 수정 수는 `suggested_fix_count`다.
- **증거**: `raw_ref`는 stdout 스트림의 레코드 바이트 범위다. 스트림 SHA-256과 크기는 실행 기록과 일치해야 한다.
- 실행 기록의 stdout에는 scratch·toolchain의 경로가 들어 있을 수 있다(`compiler-artifact`의 `filenames`, `build-script-executed`의 `out_dir`). W05 redaction으로 작업 사본·scratch·toolchain 경로는 `[REDACTED]`로 바꾸고 envelope에는 노드 절대 경로를 넣지 않는다(시험이 확인한다).
- **build script와 proc macro**: `build-script-executed` 레코드가 있으면 제약 `build_script_executed`, proc-macro target의 artifact가 있으면 `proc_macro_executed`를 남긴다. 허가를 받아 프로세스를 실행했으므로 `Composite.Run`이 `repository_code_executed`도 더한다.
- **작업 사본 보호**: 실행 뒤 모듈의 `.rs`·`Cargo.toml`·`Cargo.lock`·Cargo 설정·`clippy.toml`을 manifest와 다시 대조한다. build script가 `src/lib.rs`를 고쳐 쓰는 fixture에서 `workspace_drift`로 실패하고 원본은 그대로임을 확인했다.

## 9. 시험과 실행 근거

실제 도구 출력 캡처는 다음 명령으로 만들었다(`CODEYE_TEST_CARGO`·`CODEYE_TEST_CLIPPY`는 toolchain bin 디렉터리의 절대 경로).

```text
TC=$HOME/.rustup/toolchains/stable-x86_64-unknown-linux-gnu/bin
CODEYE_W20_CAPTURE=$PWD/internal/worker/analysis/rustpack/testdata \
CODEYE_TEST_CARGO=$TC/cargo CODEYE_TEST_CLIPPY=$TC/cargo-clippy \
go test -count=1 ./internal/worker/analysis/rustpack/
```

[실제 산출물 예시](../examples/observations-cargo-check.json)·[clippy](../examples/observations-clippy.json)는 같은 실행에 `CODEYE_W20_DUMP=<dir>`를 주어 얻은 `TestRealCargoCheckFindings`·`TestRealClippyFindings`의 envelope다. 캡처에서 홈·scratch·toolchain 경로는 `$HOME`·`$SCRATCH`·`$TOOLCHAIN`으로 바꿨다. fixture를 바꾸면 다시 캡처해야 한다.

| 시험 | 범위 |
|---|---|
| `rustpack.TestReal*` (도구 필요: `CODEYE_TEST_CARGO`, clippy는 `CODEYE_TEST_CLIPPY`) | 경고 exit 0과 병합, 바이트 열, 필요 환경·argv·scratch(TC-114), clippy 필터와 review lint, rustc 진단 중복이 cargo check 관찰과 같은 집합, 소스 억제 측정(`#[allow]`·`--force-warn`), deny lint, 컴파일 오류, workspace와 설정·`rust-toolchain.toml` 무시(TC-115), 비멤버 crate, 경로 범위, build script·proc macro와 링커(`CODEYE_TEST_TOOLPATH`에 `cc` 필요), 링커 없음, build script 실패·작업 사본 drift, 의존성·Cargo.lock 없음(내려받지 않음), timeout·취소, rustup proxy 거부(TC-116) |
| `rustpack.TestReplay*` (CI) | 캡처 바이트와 dep-info 재생: 위 판정, 종료 코드 표, 훼손된 출력(잘림·손상·알 수 없는 레코드·exit/`build-finished` 불일치)이 성공이 아님, 버전 계약, dep-info 없음, argv·cwd·환경·제한시간, 자리 작업 |
| `rustpack.TestRealVendoredDependencies`, `TestRealSiblingModules`, `TestRealNoWritesAfterCleanup` (도구 필요) | vendor 디렉터리 의존성과 `CARGO_HOME` 쓰기, 모듈 디렉터리가 `.`가 아닌 형제 crate(`.m01`·`.m02`, span·dep-info·설정 파일 매핑), cargo check·clippy·build script 실행 뒤 scratch가 0.5초 뒤에도 같음(분리된 자식의 늦은 쓰기 없음) |
| `rustpack.TestReplaySiblingModules`, `TestReplayStderrIsNotEvidenceOfAnythingButAFailure` (CI) | 위 캡처 재생, stderr 인용문이 성공을 뒤집지 못함 |
| `rustpack.TestParse*`, `TestPlanModules`, `TestNewValidation`, `TestFindLinker`, `TestRuleConfig`, `TestByteColumn`, `TestCategoryPolicy`, `TestDescriptorsAndComposite` | parser 엄격성, stderr 분류(캡처 포함), dep-info, 계획(모듈·범위·제외 디렉터리), proxy 거부, 링커 조회와 PATH 비전달, 설정 파일 목록과 조상 설정, 열 변환, 범주 표 |
| `run.TestRustPackWithoutRepositoryCodeGrant` (Linux, CI, TC-103/116) | 실제 워커·composite·보고서·단계: 허가 없는 소스에서 도구 미설정·설정 모두 기준 분석기 포함 `unsupported`(`repository_code_not_allowed`), 도구 사유는 제약, 만든 프로세스는 version probe뿐, `static_analysis` 단계와 보고서가 부분이고 `required_task_not_succeeded` |
| `run.TestRustPackWithRealToolchain` (Linux, 도구 필요) | 허가가 있을 때 두 분석기가 같은 작업 사본에서 성공, 실행 이름·`repository_code_executed`, env.discover의 도구 관측, 보고서의 unreviewed 도구 finding |
| `report.TestAnalyzerDecoderRust*` | 실제 envelope의 decode, `ToolFingerprint`, 규칙 문맥(`honored`·`recorded`/`unrecorded`), 처리 파일 합집합, 미실행 행, 코드 제외 export |
| `history.TestRustFindingsCompare` (TC-104) | 줄 이동 persisting, 억제를 알 수 없어 사라진 finding이 해결되지 않음, 설정 digest·미열거 설정·이전 builder 문맥·미실행 |
| `builtin.TestToolFlagsAndProbes`, `TestHandlersAdvertiseOnlyProbedToolchains`, `main.TestWorkerHandlers*` | 카탈로그·플래그·probe 목록에 `cargo`·`clippy` |
| `builtin.TestCargoAloneAdvertisesDeepReview`, `TestRustProxyIsNeitherUsedNorProbed` (Linux, CI), `builtin.TestHandlersProbeRealRustToolchain` (도구 필요) | `--cargo`만으로 광고, clippy는 cargo 필요, proxy 거부와 탐색 비실행, 실제 시작 probe 뒤 probe 디렉터리가 비어 있음 |
| `scripts/validate_bundle.py` (`check_w20_rust_pack`) | 서술 yaml과 예시 envelope의 schema, 규칙 팩 상수·selection·argv 일치, 20개 거부 사례 |

## 10. 남은 제한

- 허가(`--repo-code-source-root`) 아래의 build script·proc macro는 노드 계정이 읽을 수 있는 파일을 읽고 `cargo:warning` 등으로 출력할 수 있다. cargo의 JSON 출력(`rendered` 포함)은 실행 stream으로 저장된다. 스냅샷 밖 위치의 진단은 위치 없는 관찰로 남기지만 원시 stream까지 걸러내지는 않는다. 이는 [docs/08 §2](08-native-execution.md)의 신뢰 경계에 따른 것이며, 신뢰 경계가 다른 저장소에는 허가를 주지 않거나 별도 계정·노드를 쓴다.
- Windows와 macOS에서 실제 cargo를 실행하지 않았다. Windows는 링커 조회를 하지 않으므로 build script가 있는 crate는 `linker_unavailable`일 수 있다.
- 오프라인 레지스트리 캐시 플래그가 없다. 레지스트리 의존성이 있는 저장소는 vendor 디렉터리가 없으면 `dependency_unavailable`이다. 읽기 전용 캐시를 지원하려면 `CARGO_HOME`을 복사하거나 cargo의 `registry` 설정과 vendor 형식을 써야 하며 이 단계에서 만들지 않았다.
- 기본 feature, host target 하나다. `--all-features`, feature 조합, 교차 target은 계획하지 않는다. 비멤버 crate와 `cfg`·feature로 꺼진 파일은 컴파일되지 않고 `files_unlisted`로 보일 뿐 분석되지 않는다.
- cargo는 패키지 단위로만 범위를 줄일 수 있어 경로 범위도 모듈 전체를 컴파일한다. `-p`로 패키지를 고르지 않았다.
- 규칙 문맥에 `edition`, `rust-version`(clippy의 msrv), `[profile]`을 기록하지 않는다. 이것들만 바뀌면 사라진 finding이 해결로 보일 수 있다. `[lints]` 표가 없는 `Cargo.toml`의 `[package.metadata]`도 읽지 않는다.
- 소스 안 억제를 보고할 수 없다(§5). 사라진 finding은 항상 해결로 인정되지 않는다.
- clippy 범주 표는 직접 유지하는 목록이다. 표에 없는 새 lint는 maintainability로 보인다.
- 보고서 finding은 주 위치만 보인다(`related`는 envelope와 R1 입력에만 있음). R1 해석의 호출자 확장은 Go 코드에서만 동작한다(W15).
- executor는 setsid로 프로세스 그룹을 벗어난 자손을 추적하지 않는다. 측정: cargo check, clippy, build script와 proc macro가 있는 workspace 실행과 시작 probe에서 `cleanup=confirmed`였고, 정리 뒤 0.5초 동안 scratch와 probe 디렉터리에 쓰기가 없었다(`TestRealNoWritesAfterCleanup`, `TestHandlersProbeRealRustToolchain`). 이 측정은 시험한 fixture에 한한다. build script는 저장소 코드이므로 분리된 자식을 만들 수 있고, 그 위험은 허가 범위 안에 있다.
- 링커는 `--toolchain-path` 목록에서 `cc`를 이름으로 찾는 PATH 조회다(§2.4). 도구는 절대 경로 플래그로만 받는다는 규칙의 예외이며, 이 `cc`는 버전을 식별하지 않는다. 대안은 전용 플래그(`--cc /abs/cc`)다. 이 단계에서는 hello 필드와 agent 설정을 늘리지 않으려고 기존 설정을 썼다.
- 운영자가 `--cargo`를 바꾸면 도구 버전이 달라져 비교는 `rules_changed`로 처리된다(W23).

# 29. W08 첫 언어 분석 어댑터: Go vet

상태: 2026-11-18. Linux(Ubuntu 26.04.1 / WSL2 amd64, Go 1.27.4)에서 실제 `go` 프로세스를 W05 실행기로 실행해 검증했다. Windows는 교차 컴파일과 Windows 형태 경로의 단위 시험만 했다. Windows 실행 결과는 Windows CI 전까지 미검증이다. W06 agent·worker 통합, W09 보고서 renderer, LLM task 계획은 이 작업 범위가 아니다.

## 1. 선정과 범위

첫 언어는 Go다. CODEYE 자체가 Go 프로젝트라서 공개된 실제 저장소를 고정 snapshot으로 분석해 결과를 재현할 수 있다. 도구는 Go toolchain에 포함된 `go vet` 하나다. Staticcheck 등 추가 도구, 다른 언어, suggested fix 적용은 하지 않는다.

| 요구 | 구현 |
|---|---|
| REQ-006,007,008 / TC-07,08 | stdout JSON에서 관찰 생성, exit 0 + 진단과 exit 1 오류 구분, 부분 파싱·손상·잘림은 실패로 기록 |
| REQ-014,015 / TC-17 | W05 `executor.Runner`로만 실행, argv·cwd·명시 env·timeout·출력 상한, 원본·snapshot 미변경 확인 |
| REQ-022,035 / TC-18 | timeout·취소·정리 불확실을 성공으로 바꾸지 않음. 정리 판정은 W05 결과를 따름 |
| AGENTS 오프라인 원칙 | `GOPROXY=off`, `GOTOOLCHAIN=local`, 다운로드 fallback 없음. 모듈 캐시가 없으면 `dependency_unavailable` |

## 2. 패키지와 진입점

| 패키지 | 역할 |
|---|---|
| `internal/worker/analysis` | W06 계약 §8의 `Handler`, `Job`, `TaskSpec`, `Execution`, `TaskResult`, `Registry`; `WriteExecution`; result manifest 타입·검증(`ResultManifest`, `CommandFor`) |
| `internal/worker/analysis/builtin` | `Handlers(ctx, Config{GoExecutable, GoModCache, ProbeDir}, runner)`: 실제 `go version` probe가 성공한 handler만 반환한다. 나머지는 `Unavailable{not_configured\|invalid_config\|probe_failed}`로 돌려준다 |
| `internal/worker/analysis/govet` | Go vet handler(`go-vet`, version `1`, capability `analysis:deep-review`), parser `go-vet-json-v1` |

W06 동결 계약에서 바뀐 점(W06 runtime과 조율했고 계약 change log는 W06 쪽에서 기록한다):

1. `Builtin()`을 `builtin.Handlers`로 옮겼다. govet과 W06 fixture가 `analysis`를 import하므로 그대로 두면 import 순환이 생긴다. `codeye_fixture` 태그 handler는 W06이 `builtin/fixture_on.go`로 추가한다. 기본 빌드의 `fixture_off.go`는 아무것도 추가하지 않는다.
2. agent `hello`에 선택 필드 `go_executable`, `go_mod_cache`(절대 경로)를 추가한다. 값이 비어 있거나 probe가 실패하면 capability를 광고하지 않는다.
3. `Execution.Name`을 추가했다. 값은 `go.version`, `go.modedit`, `go.list`, `go.vet`이며 result manifest의 `executions[].name`이 된다.
4. stdout/stderr 파일은 handler가 `analysis.WriteExecution`으로 `Job.Scratch` 안에 쓴다.
5. handler는 workspace 절대 경로를 W05 `Spec.Redact`로 넘긴다. 경로 그대로의 형태와 JSON escape 형태를 모두 넘기므로 저장되는 stream과 `Command.Dir`에는 `[REDACTED]/상대경로`가 남는다. manifest에는 실행 파일 base name만 기록한다(`CommandFor`).

분석 패키지 아래에서는 `os/exec`, `net`, `net/http`, `syscall` import를 시험으로 금지한다. handler는 cleanup을 주장하지 않고, worker가 기록한 실행 결과로 합산한다.

## 3. 계획과 실행

`Plan`은 manifest만 읽는다. 포함된 `go.mod`마다 required task 하나를 만든다. `vendor`, `testdata`, `.`이나 `_`로 시작하는 디렉터리는 제외한다. 루트 모듈의 task 이름은 `go-vet`이고 나머지는 정렬 순서대로 `go-vet.m01`…이다. scope `paths`는 모듈별 `./dir/...` 또는 단일 패키지 `./dir` 패턴으로 바뀐다.

Go 모듈이 없으면 `go-vet` 하나를 계획하고 실행 없이 `unsupported`(`analysis_unsupported`)로 끝낸다. 한계 코드는 `no_go_files`, `go_module_missing`, `scope_has_no_go_packages` 중 하나다. 모듈이 64개를 넘으면 계획 오류다.

`Run`은 task마다 아래 순서로 실행한다. 앞 단계가 취소·timeout·미지원·거부·정리 불확실로 끝나면 뒤 단계는 실행하지 않는다. `go.modedit`가 실패하거나 snapshot 밖 `replace`를 보고해도 `go.list`·`go.vet`을 실행하지 않는다.

| 이름 | argv | 용도 |
|---|---|---|
| `go.version` | `go version` | 도구 버전·플랫폼 기록. 테스트한 minor는 `go1.27`이며 다르면 `tool_version_untested` |
| `go.modedit` | `go mod edit -json` | go 명령 자신의 파서로 `go.mod`를 읽어 module path와 디렉터리 `replace` 대상을 얻는다(`go.mod`만 읽음) |
| `go.list` | `go list -e -json=Dir,ImportPath,Name,GoFiles,CgoFiles,IgnoredGoFiles,TestGoFiles,XTestGoFiles,Incomplete,Error,DepsErrors <patterns>` | 패키지·파일 inventory |
| `go.vet` | `go vet -json <patterns>` | 진단 |

실행 조건:

- **위치와 보호:** cwd는 attempt workspace 사본 안의 모듈 디렉터리다. `ProtectedRoots`에는 snapshot 디렉터리와 설정된 모듈 캐시가 들어간다. W05가 보호 root 안의 실행 파일을 거부하므로 GOROOT는 넣지 않는다.
- **환경 변수 전체:**
  - `PATH=<go 바이너리 디렉터리>`, `LANG=C.UTF-8`
  - `GOENV=off`, `GOWORK=off`, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOVCS=*:off`, `GOFLAGS=`, `CGO_ENABLED=0`
  - `GOCACHE`·`GOPATH`=task scratch, `GOMODCACHE`=운영자 캐시 또는 빈 scratch 디렉터리
  - Windows는 `SystemRoot`를 추가한다. HOME·TMP는 W05가 실행별로 만든다.
- **대상 코드 미실행:** cgo를 끄고 toolchain 전환과 모듈 다운로드를 막는다. `go generate`, test 실행, `-vettool`도 쓰지 않는다.
- **go.mod 검사:**
  - 실행 전에 snapshot의 `go.mod` hash를 manifest와 대조한다. 다르면 `failed`(`snapshot_integrity_failed`)로 끝낸다.
  - `replace` 판정은 직접 만든 문법 해석기가 아니라 `go mod edit -json`의 결과를 기준으로 한다. 직접 해석하면 따옴표 안의 `//` 같은 경우에 go 명령과 결과가 달라진다.
  - W05 마스킹 표지(`[REDACTED]`)가 들어간 대상은 원래 workspace 절대 경로였으므로 상대 경로로 해석하지 않고 거부한다.
  - 디렉터리 `replace`가 절대 경로이거나 snapshot 밖을 가리키면 `skipped_policy`(`replace_outside_snapshot`)로 끝내고 `go list`·`go vet`을 실행하지 않는다. 네이티브 `go`가 그 호스트 경로를 읽기 때문이다. snapshot 안의 `replace`는 허용한다.
- **제한값:** 실행마다 timeout 15분, stdout 32 MiB, stderr 4 MiB가 기본이다. 새 GOCACHE에서 표준 라이브러리 정보를 다시 만들기 때문에 CODEYE 전체 분석에 약 12초가 걸렸다.
- **scratch 배치:** `<Scratch>/go-vet/<task>/{exec/<name>.stdout|.stderr, observations.json, gocache/, gopath/, tmp/}`.

## 4. 실제 Go 1.27.4 출력에서 확인한 사실

합성 모듈로 직접 실행해 확인했고 fixture와 시험에 반영했다.

1. `go vet -json`은 진단이 있어도 **exit 0**이다. 진단 유무는 stdout을 읽어야 알 수 있다.
2. stdout은 단위마다 최상위 JSON 객체가 이어 붙은 형태다. 진단 없는 단위도 이름 없는 `{}`를 출력한다. 따라서 `{}`가 어느 패키지의 출력인지 알 수 없고, "출력 없음 = 깨끗함"도 성립하지 않는다.
3. 패키지 출력 순서는 실행마다 다르다. 테스트 변형은 `pkg`(내부 test 포함)와 `pkg_test`(외부 test) 키로 나오며 `[pkg.test]` 접미사는 붙지 않는다.
4. 타입 오류가 있는 패키지는 stdout에 아무것도 출력하지 않는다. stderr에 `# <pkg>`와 `vet: <cwd 기준 상대경로>:L:C: msg`가 나오고 전체가 exit 1로 끝난다. 다른 패키지의 JSON은 정상 출력된다.
5. 오프라인에서 모듈이 없으면 exit 1이고 stdout은 비어 있다. stderr에는 `go: downloading …` 뒤에 `module lookup disabled by GOPROXY=off`가 나온다. `go list -e`는 exit 0으로 끝나며 `DepsErrors`를 보고한다. 시험은 실행 후 task의 빈 모듈 캐시에 파일이 하나도 생기지 않았음을 확인한다.
6. `go.mod`의 go 버전이 실행 중인 Go보다 높으면 `go list`·`go vet` 모두 exit 1이고 `requires go >= … (GOTOOLCHAIN=local)`을 출력한다. `go mod edit -json`은 이 경우에도 exit 0으로 파싱 결과를 낸다.
7. build constraint나 cgo 때문에 모든 파일이 제외된 패키지는 `./...`의 `go list`와 vet 결과에서 **조용히 빠진다**. 모든 패키지가 빠지면 `matched no packages` 경고 뒤 vet가 `no packages to vet`로 exit 1을 낸다.
8. `go list`/`go vet` JSON은 경로의 역슬래시와 `&`·`<`·`>`를 escape한다. 그래서 W05의 바이트 단위 마스킹이 그대로는 맞지 않는다. handler는 escape된 형태까지 마스킹하고, parser는 해석된 절대 workspace 경로도 직접 제거한다(Windows는 대소문자 무시).

## 5. 결과 판정

성공(`succeeded`)은 다음 조건을 모두 만족할 때만 판정한다.

- 네 실행 모두 exit 0이고 cleanup이 `confirmed`다.
- stdout·stderr가 잘리거나 불완전하지 않다.
- `go.mod`를 읽을 수 있고 snapshot 밖 `replace`가 없다.
- 실행 뒤 workspace의 Go 입력이 manifest와 같다.
- vet stdout을 남는 byte 없이 해석했다.
- analyzer 오류, 타입 오류, 의존·toolchain·모듈 오류가 없다.
- `go list`에 오류·의존 오류·불완전 패키지가 없다.
- 분석된 패키지가 1개 이상이다.

stderr의 `go:` 경고 줄은 기록하지만 그것만으로 실패 처리하지는 않는다. 실패 판정은 아래 표의 조건을 따른다. 여러 조건에 걸리면 표에서 먼저 나오는 사유를 기록한다. 이미 읽은 observation은 보존한다.

| 우선 | 조건 | task 상태 / reason | exit class |
|---|---|---|---|
| 1 | 실행 하나라도 cleanup `uncertain` | failed / `cleanup_uncertain` | execution_error |
| 2 | 취소(ctx 또는 executor) | cancelled / `cancelled` | cancelled |
| 3 | timeout | timed_out / `timeout` | timeout |
| 4 | executor `unsupported`·`start_failed`·`rejected` | unsupported / `execution_unsupported`, failed / `tool_start_failed`·`execution_rejected` | not_run |
| 5 | `go version` 해석 불가 | failed / `tool_version_unrecognized` | configuration_error |
| 6 | `go mod edit -json` 실패 | failed / `go_mod_unreadable` | configuration_error |
| 7 | snapshot 밖 디렉터리 `replace` | skipped_policy / `replace_outside_snapshot` | not_run |
| 8 | 단계 미실행 | failed / `vet_not_run` | not_run |
| 9 | workspace drift | failed / `workspace_drift` | execution_error |
| 10 | toolchain 부족 · 모듈 없음 · 의존 모듈 없음 | failed / `toolchain_unavailable`·`go_module_missing`·`dependency_unavailable` | configuration_error |
| 11 | 분석할 패키지 없음(`matched no packages`) | unsupported / `no_analyzable_packages` | configuration_error |
| 12 | 출력 잘림·불완전 | failed / `output_truncated` | execution_error |
| 13 | JSON 손상·형태 오류 | failed / `output_unparsed` | execution_error |
| 14 | 패키지 타입 오류(stderr) | failed / `package_errors` | execution_error |
| 15 | vet 기타 비정상 exit · list 오류 · analyzer 오류 | failed / `vet_failed`·`package_load_error`·`analyzer_error` | execution_error |
| 16 | vet exit 0이지만 분석된 패키지 0개 | unsupported / `no_analyzable_packages` | configuration_error |

`partial_parse`는 task가 실패했는데 vet stdout을 일부 읽은 경우에만 `true`다. 의존성 실패처럼 stdout이 비어 있으면 `false`다. 성공 시 exit class는 관찰이 있으면 `success_with_findings`, 없으면 `success_without_findings`다. `success_without_findings`는 도구가 0건을 보고했다는 뜻일 뿐 결함이 없다는 뜻이 아니다.

## 6. 관찰 문서(observations artifact)

handler는 task마다 `observations.json`을 만든다. role은 `observations`, media는 `application/vnd.codeye.observations+json`이고 형식은 canonical JSON이다. 형식은 [Schema](../schemas/go-vet-observations.schema.json)와 실제 실행으로 만든 [예시](../examples/observations-go-vet.json)에 있다. 도구 서술은 [analyzer 예시](../examples/analyzer-go-vet.yaml)에 있다. analyzer 예시의 `findings` 종료 코드는 빈 배열이다. 진단 여부는 parser가 결정하기 때문이다.

- **observation:** 도구 진단 하나다.
  - `rule_id`는 analyzer 이름이다. 원문에 severity가 없으므로 `raw_severity`는 항상 null이다.
  - 메시지는 원문 그대로 두고 경로만 바꾼다. workspace 접두는 상대 경로로 만들고, GOROOT·모듈 캐시·scratch·snapshot 경로는 `$GOROOT` 같은 이름으로 바꾼다.
  - suggested fix는 개수만 센다.
- **ID:** `obs_` 뒤에 sha256(도구, package key, rule, 정규화 위치, 메시지, 동일 tuple 순번)의 앞 20자를 붙인다. vet 출력 순서가 바뀌어도 ID와 정렬은 같다(시험으로 확인). `raw_ref.stream_index`는 위치를 찾는 용도로만 쓰고 ID에는 넣지 않는다.
- **raw_ref:** `go.vet` stdout의 sha256, 최상위 값 순번, RFC 6901 JSON pointer, byte 범위를 기록한다. 서버 artifact ID 대신 hash로 원시 출력과 연결한다.
- **location_state:** 아래 6가지 중 하나다.
  - `resolved`
  - `unresolved_no_position`
  - `unresolved_outside_snapshot`
  - `unresolved_missing_file`
  - `unresolved_line_out_of_range`
  - `source_drift`

  `resolved`만 `location`(snapshot 상대 경로·1-based 줄·byte 열·manifest의 `blob_sha256`)을 가지며 코드 근거가 될 수 있다. 줄 범위는 manifest hash와 일치하는 snapshot 파일로 검사한다.
- **workspace drift:** 실행 뒤 workspace의 Go 입력이 manifest와 다르면 `workspace_drift`에 기록하고 task를 실패로 처리한다. 해당 파일의 관찰은 보존하지만 위치를 연결하지 않는다.
- **inventory와 coverage:** 패키지 상태는 아래 7가지 중 하나다.
  - `analyzed_clean`
  - `analyzed_with_findings`
  - `failed_typecheck`
  - `dependency_unavailable`
  - `load_error`
  - `not_analyzed`
  - `unknown`

  vet가 완전히 성공했을 때만 진단 없는 패키지를 `analyzed_clean`으로 판정한다. 실패한 실행에서 진단이 없는 패키지는 `unknown`이다. manifest에는 있지만 `go list`가 보고하지 않은 Go 파일은 `not_analyzed`(`not_listed_by_go_command`)다. `processed_go_files`는 분석된 패키지의 파일 수이며, `unknown`이 하나라도 있으면 null이다.
- **한계 코드:** 항상 `default_build_context_only`(노드 GOOS/GOARCH, 사용자 build tag 없음)와 `cgo_disabled`를 기록한다. 조건에 따라 다음 코드를 추가한다.
  - `build_constraint_ignored_files`
  - `unlisted_go_files`
  - `no_offline_module_cache`
  - `go_work_ignored`
  - `vendor_directory_used`
  - `tool_version_untested`
  - `workspace_drift`
  - `unattributed_vet_package_key`
  - `local_replace_outside_snapshot`

구조화 산출물(observations, manifest command)에는 노드 절대 경로와 env 값이 없다. 실제 실행 시험은 다음 두 가지를 검사한다.

- 문서 바이트에 base·scratch·workspace·snapshot·GOROOT 경로가 없다. 공백·비ASCII·`&`·`<>`가 들어간 경로로 확인했다.
- 원시 stdout/stderr에 workspace 경로의 어떤 형태도 남지 않는다.

모듈 캐시나 GOROOT 경로는 원시 오류 메시지에 남을 수 있다(§9).

## 7. 시험과 실행 결과

| TC | 검증 | 시험 |
|---|---|---|
| TC-66 | 실제 `go vet -json`이 exit 0으로 진단 5건(copylocks 2, printf 3, 내부·외부 test 포함)을 내고 관찰로 변환됨<br>위치·hash·raw byte 범위 검증, 공백·비ASCII·`&<>` 경로, 비ASCII 파일명, 노드 경로 미노출, 원본·snapshot 불변, 재실행 시 ID 동일<br>깨끗한 모듈은 `success_without_findings`<br>JSON escape된 workspace 경로와 Windows 형태 경로 해석 | `TestRealVetFindingsExitZero`, `TestRealVetCleanModule`, `TestParseVetStreamCaptured`, `TestAssembleOrderIndependent`, `TestAssembleUnredactedRoots` |
| TC-67 | 타입 오류로 exit 1인 부분 파싱: 다른 패키지 관찰 보존, `unknown`, coverage null<br>실제 출력의 잘림·손상·analyzer 오류·형태 오류는 실패로 기록하고 0건으로 바꾸지 않음 | `TestRealVetTypeErrorPartial`, `TestRealVetFailureModes`, `TestParseVetStreamDamaged`, `TestParseStderr`, `TestAssembleExecutorStatuses` |
| TC-68 | 오프라인 의존 모듈 없음·toolchain 부족은 configuration_error, 모듈 캐시에 파일이 생기지 않음<br>snapshot 밖 `replace`(따옴표 안의 `//`, 마스킹된 절대 경로 포함)는 `go list`·`go vet` 전에 거부, 내부 `replace`는 허용 | `TestRealVetOfflineConfigurationErrors`, `TestReplaceOutsideSnapshotRefused`, `TestParseModEditAndEscapes` |
| TC-69 | 실제 timeout·사전 취소·실행 파일 없음·cleanup 불확실 처리<br>Go가 아닌 snapshot은 `unsupported`<br>probe 실패·미설정 시 capability 미광고 | `TestRealVetTimeoutAndCancel`, `TestRealVetFailureModes`, `TestNonGoSnapshotUnsupported`, `TestHandlersAdvertiseOnlyProbedToolchains` |
| TC-70 | build constraint·cgo로 빠진 패키지는 `not_analyzed`, 무시 파일 집계<br>모든 패키지가 빠지면 `unsupported`<br>중첩 모듈·scope, workspace drift는 실패와 위치 미연결<br>registry·manifest 검증, 분석 패키지 import 금지 | `TestRealVetFindingsExitZero`, `TestRealVetNothingAnalyzable`, `TestRealVetNestedModuleScope`, `TestWorkspaceDriftIsolatesLocations`, `TestRegistry`, `TestResultManifestValidate`, `TestNoProcessOrNetworkImports` |

실제 도구 시험의 구성은 다음과 같다.

- **입력 준비:** W04 `AcquireLocal`→`Load`→`CloneWorkspace`로 snapshot과 attempt workspace를 만들고, 실제 `executor.Executor`로 `go`를 실행한다.
- **고장 주입:** 잘림·손상·정리 불확실은 실제 실행 결과를 감싼 runner가 결과를 바꿔 만든다. 실제 W05 timeout과는 구분해서 봐야 한다.
- **시험 대상 코드:** 대상 Go 코드는 시험 코드 안의 문자열로 두고 임시 디렉터리에 쓴다. 저장소의 gofmt·`go vet ./...` 대상에 들어가지 않게 하기 위해서다.
- **toolchain:** 테스트 바이너리를 만든 toolchain(`GOROOT/bin/go`)을 PATH 검색 없이 쓴다. `CODEYE_TEST_GO`로 바꿀 수 있고, toolchain이 없으면 이유를 남기고 skip한다.

실행 결과(2026-11-18, 위 환경):

| 명령 | 결과 |
|---|---|
| `go test -count=1 ./internal/worker/analysis/...` | 3개 패키지 통과(govet은 실제 `go` 실행 포함 약 35초) |
| `python3 scripts/test_postgres.py` (PG 17.10, `-race`) | 11개 패키지 통과, migration·CLI·schema 검사 통과. 브라우저 smoke(`--web`)는 이번 변경과 관계없어 실행하지 않음 |
| `gofmt -l cmd internal migrations`, `go vet ./...`, `GOOS=windows go vet ./internal/worker/...`, Windows 대상 test 바이너리 컴파일 | 차이 0, 통과 |
| `python3 scripts/validate_bundle.py` | JSON Schema 8개, 예시 계약 9개, 거부 사례 15개 통과 |
| 최종 코드로 실제 실행해 만든 관찰 문서 17개(시험 15개, CODEYE 실제 실행 2개)에 Schema와 교차 검사 적용 | 모두 통과 |

**CODEYE 자체 실제 실행.**

- 대상은 커밋 `829aea054026158ca42c4d9948fbc2ab36bd0bfb`를 `git archive`로 푼 격리 사본이다. 이후 커밋은 마스킹 표지가 든 `replace` 거부만 추가했으며 `replace`가 없는 CODEYE 결과에는 영향이 없다.
- 모듈 캐시는 이 머신의 기존 Go 다운로드 캐시에서 `GOPROXY=file://…`로 별도 디렉터리에 오프라인으로 구성했다(54 MB).
- 실행 시험은 `CODEYE_W08_TARGET`·`CODEYE_W08_OUT`·`CODEYE_W08_MODCACHE`를 설정한 `TestRealTargetEvidence`다. 환경 변수가 없으면 skip한다.

| 구성 | manifest | 결과 |
|---|---|---|
| 오프라인 모듈 캐시 있음 | 231 항목<br>`b6bb630fd0d1161be3f720e680889ef25224b196e69eadb58fcd0db2de486360` | `succeeded` / `success_without_findings`<br>11개 패키지 `analyzed_clean`, `{}` 11개<br>범위 Go 파일 82개 중 74개 처리, Windows 전용 등 8개는 build constraint로 무시<br>실행 4개 모두 exit 0 · cleanup confirmed, 약 12초 |
| 모듈 캐시 없음 | 231 항목<br>`f5b97578cc9cce39c3324cc6a11cf5473b1b19393c2e532e65a30c106205054e` | `failed` / `dependency_unavailable`(configuration_error)<br>8개 패키지 `dependency_unavailable`, 3개 `unknown`, `processed_go_files` null |

두 경우 모두 원본 사본·snapshot·모듈 캐시의 트리 hash가 실행 전후로 같았다. snapshot은 확보할 때마다 manifest digest가 새로 생기므로 두 digest가 다르다. CODEYE에서 나온 vet 진단은 0건이다. 진단이 있는 경로와 실패 경로는 합성 fixture 시험으로만 검증했다.

## 8. W09 계획과 다른 점

W09 계획 §6과 실제가 다른 부분이다. renderer 계약을 정할 때 반영해야 한다.

- **출력 형태:** 진단 없는 단위는 `{}`를 출력하고 출력 순서는 실행마다 바뀐다. 그래서 ID는 내용 기반이다.
- **실행·artifact 연결:** 보고서의 `Execution.ID`와 artifact ID는 서버가 부여한다. 관찰 문서는 실행 이름과 stdout sha256으로 연결하므로 W09는 result manifest의 `executions[].name`과 stdout artifact의 sha256으로 연결을 다시 만든다.
- **snapshot 식별:** handler는 `ObsLocation.snapshot_id`를 알 수 없다. 대신 문서의 `snapshot_manifest_digest`를 쓴다.
- **추가한 값:**
  - 위치 상태 `unresolved_no_position`, 패키지 상태 `load_error`
  - task 상태 `skipped_policy`(`replace_outside_snapshot`), `unsupported`(`no_analyzable_packages`)
- **실행 구성:** 한 task 안에 `go.version`, `go.modedit`, `go.list`, `go.vet` 네 실행을 둔다.
- **LLM task:** go-vet handler는 Go vet task만 계획하고 필수 LLM task(`context_review`, `verify`)는 계획하지 않는다. W09는 이 task 집합만 보고 `completed`를 판정하면 안 된다. AI 검토가 빠졌다는 사실은 profile이나 W10 이후 task가 표현해야 한다.

## 9. 남은 제한

- **Windows:** 경로 해석(드라이브, 대소문자, 역슬래시, JSON escape)은 Windows 형태 입력의 단위 시험으로만 확인했다. Windows에서 실제로 실행하지 않았으므로 8.3 짧은 이름 같은 차이는 Windows CI 결과로 확인해야 한다. RHEL 8.10도 실행하지 않았다.
- **분석 범위:** build context는 노드 기본 GOOS/GOARCH와 기본 tag만 쓴다. cgo 파일은 분석하지 않고 vet analyzer 선택·flag 설정도 없다.
- **원시 출력의 경로:** 원시 stderr에는 모듈 캐시나 GOROOT 절대 경로가 남을 수 있다. 구조화 문서에서는 이 경로를 가린다. W09 export는 원시 bytes를 렌더링하지 않는 규칙을 지켜야 한다.
- **모듈 캐시:** 운영자가 준비한다. 캐시 무결성은 go.sum 대조에 의존하고 `GOSUMDB=off`다. 캐시를 쓰지 않는다는 보장은 OS 권한과 Go의 기본 읽기 전용 모드에 의존한다.
- **W06 통합:** W06 agent/worker를 거친 end-to-end 실행, 서버 artifact 업로드, result manifest 생성은 W06 통합 후 검증한다. 이 문서의 시험은 handler를 직접 호출했다.
- **품질:** 시험 통과는 Go vet의 결함 탐지 정확도나 CODEYE 분석 품질의 증거가 아니다. 진단 수가 늘어난 것을 품질 향상으로 해석하지 않는다.

## W06 제품 워커 통합 시험

`python3 scripts/test_postgres.py --nodes`에서 일반 빌드 에이전트에 Go 실행 파일과 전용 source root를 등록했다. 합성 Go 모듈 요청에서 실제 printf 관찰, 고정 입력 상대 위치, 게시한 observations bytes의 SHA-256과 원본 파일 불변을 확인했다. 시험용 handler로 배정된 노드에는 이 root 권한이 없다.

# 39. R1 분석기 팩 계약과 W19 Go 팩

W19는 두 가지를 구현한다. 첫째는 W16(C/C++), W17(Python), W18(JS/TS), W20(Rust), W21(구조·복잡도·비밀정보)이 함께 쓰는 **분석기 팩 계약**이다. 둘째는 그 계약의 첫 사용자인 **Go 팩의 Staticcheck 어댑터**다. Go vet(W08)은 계약의 기준 분석기로 편입했고 동작은 바꾸지 않았다.

검증 범위: Linux(WSL2) x86-64, go1.27.4, staticcheck 2026.2.2 (0.8.2)의 실제 실행. Windows는 컴파일과 단위 시험만 대상이다. 실제 staticcheck를 Windows에서 실행하지 않았다. CI에는 staticcheck가 없다. 실제 도구 시험은 `CODEYE_TEST_STATICCHECK`가 있을 때만 실행하고, CI에서는 캡처한 실제 출력을 재생하는 시험이 같은 판정 경로를 검사한다(§10).

## 1. 구성 요약

| 부분 | 위치 | 역할 |
|---|---|---|
| 팩 계약 | `internal/worker/analysis/pack` | `Descriptor`·`Analyzer`·`Composite`·`NotConfigured`·`WriteEnvelope`·`ToolProbe` |
| 관찰 envelope | `internal/worker/analysis/obsdoc`, [schema](../schemas/analyzer-observations.schema.json) | 분석기 공통 산출물 `analyzer_observations` v0.1과 검증 |
| 빌트인 카탈로그 | `internal/worker/analysis/builtin` | 분석기 목록(계획 순서), 도구 플래그, 시작 probe, 탐색 probe 목록 |
| 보고서 decoder | `internal/report/analyzer.go`(fingerprint는 W23 `fingerprint.go`) | envelope → 관찰·검토 전 도구 finding·도구 범위 행 |
| 탐색 probe | `internal/worker/discovery/observe.go` | env.discover에서 설정된 분석 도구의 버전 확인 |
| R1 해석 입력 | `internal/worker/review/r1_inputs.go` | review.interpret가 envelope와 go vet v0.1을 함께 읽음 |
| Go 팩 | `internal/worker/analysis/staticcheck`, [서술](../examples/analyzer-staticcheck.yaml), [실제 산출물](../examples/observations-staticcheck.json) | Staticcheck 어댑터 |

## 2. 팩 계약

### 2.1 Analyzer와 Descriptor

팩은 도구 하나마다 `pack.Analyzer`를 구현한다.

```go
type Descriptor struct {
    ID           string   // "<ecosystem>.<tool>", 하이픈 없음: "go.vet", "go.staticcheck"
    Name         string   // task key 소유 이름이자 scratch 디렉터리: "go-vet", "staticcheck"
    Ecosystem    string   // any | cpp | go | javascript | python | rust
    Required     bool     // 기준 분석기 여부 (§2.6)
    Tool         string   // 운영자 도구 이름 = 에이전트 플래그 이름 (§2.4)
    VersionArgs  []string // 버전 출력 argv (실행 파일 다음)
    ParseVersion func(stdout []byte) (string, bool)
}

type Analyzer interface {
    Descriptor() Descriptor
    Plan(ctx, job) ([]pack.Task, error)            // manifest와 scope만 읽는다. 도구 불필요
    Run(ctx, job, task, runner) analysis.TaskResult // 프로세스는 runner로만 시작
}
```

`Plan`은 도구 없이 결정적이어야 한다. 도구가 없는 노드에서도 같은 작업을 계획해 공백을 보이기 위해서다(§2.5). 생태계의 모듈이 범위에 없으면 작업을 계획하지 않는다. `pack.Task`는 `TaskSpec`과 함께 모듈 디렉터리, 도구 범위 인자, manifest 기준 대상 파일 수(`FilesInScope`, 셀 수 없으면 nil)를 갖는다.

`Run`은 결과 envelope를 `obsdoc.ArtifactPath(scratch, key)` = `<scratch>/analyzers/<key>/observations.json`에 `pack.WriteEnvelope`로 쓴다. 이 함수는 문서를 정렬·검증하고 파일을 배타 생성한다. 같은 경로를 두 번 쓰지 않는다. go vet만 기존 v0.1 문서를 기존 경로에 쓴다.

### 2.2 Composite

`pack.Composite`는 분석기 목록을 deep-review capability의 handler 하나로 만든다. handler 이름은 `analyzers`, 버전은 `1`이다.

- 계획은 멤버 순서대로 이어 붙인다.
- task key는 정확히 한 분석기가 소유한다. 소유 규칙은 `key == Name` 또는 `key`가 `Name + "."`로 시작하는 경우다. `go-vetx`처럼 접두어만 같은 key는 소유하지 않는다.
- 생성 시 ID 중복, 이름 겹침(한 이름이 다른 이름의 소유 범위에 들어감), 예약 이름(`review*`, `env`·`docs` 탐색 key)을 거부한다.
- 계획 시 소유하지 않은 key, 중복 key, Descriptor와 다른 `Required`, `static_analysis`가 아닌 단계, 잘못된 모듈 경로를 오류로 처리한다.
- `Run`은 key의 소유자에게만 보낸다. 소유자가 없으면 `failed`(`handler_error`, `unknown_task`)다.

### 2.3 빌트인 카탈로그

`builtin.catalog()`가 이 빌드의 분석기 목록이다. 순서가 계획 순서다. 생태계마다 기준 분석기 다음에 추가 깊이 분석기를 둔다. 팩은 자기 항목만 추가한다.

```go
{desc: staticcheck.Descriptor(), plan: staticcheck.Plan, build: buildStaticcheck},
```

`build`는 노드 설정으로 분석기를 만들고 시작 probe를 실행한다. 실패하면 이유를 돌려주고, 카탈로그는 그 자리에 `NotConfigured` 대역을 넣는다. 에이전트 플래그(§2.4)와 탐색 probe 목록(§6)은 카탈로그에서 자동으로 만든다.

### 2.4 도구 경로와 에이전트 플래그

- 도구마다 에이전트 플래그 하나: `codeye agent --<Tool> /abs/path`. W19는 `--staticcheck`를 추가했다. `go`는 기존 `--go`(toolchain)다.
- 절대 경로만 받는다. PATH를 검색하지 않고, 내려받지 않으며, 다른 위치로 대체하지 않는다.
- 값은 hello의 `analyzer_tools`(도구 이름 → 경로)로 워커에 간다. 이 빌드가 모르는 도구 이름(새 에이전트·옛 워커)은 `unknown_tool`로 보고하고 실행하지 않는다.
- 도구가 언어 toolchain을 써야 하면(staticcheck는 `go list`로 패키지를 읽는다) 그 toolchain 설정을 공유한다.

### 2.5 설정되지 않은 도구

도구 경로가 없거나, 검증·시작 probe에 실패하거나, 필요한 toolchain이 없으면 분석기는 `pack.NotConfigured` 대역이 된다. 대역은 실제 분석기와 **같은 작업을 계획**하고 프로세스 없이 끝낸다.

| 사유 (`TaskResult.Reason`) | 조건 |
|---|---|
| `tool_not_configured` | 운영자가 도구 경로를 주지 않음 |
| `tool_unavailable` | 경로가 있으나 검증·시작 probe 실패(실행 파일 아님, 버전 미인식 등) |
| `toolchain_not_configured` | 도구가 필요로 하는 언어 toolchain이 없음(staticcheck에 `--go` 없음) |

상태는 `unsupported`다. 성공·0건 통과로 세지 않는다. envelope도 쓴다. `exit_class=not_run`, 실행 0개, 관찰 0개, `processed_paths=[]`(처리 파일 0으로 알려짐), 어댑터·parser는 `codeye-not-run`이다. 단계 표시는 같은 단계의 다른 작업이 성공해도 `partial`이 되고 사유가 보인다. 예: go vet 성공 + staticcheck `tool_not_configured`이면 `static_analysis`가 `partial`이다.

### 2.6 필수(Required) 규칙

- 생태계마다 **기준 분석기 하나**가 `Required=true`다. 기준 분석기의 작업은 `required_for_verdict`다. Go는 go vet이다.
- 추가 깊이 분석기는 `Required=false`다. Staticcheck가 여기에 해당한다. 실패·미지원은 보고서에 보이지만 판정 필수 작업 실패로 세지 않는다.
- `Required`는 Descriptor·계획·envelope(`analyzer.required`)·DB task에서 같아야 한다. 보고서 decoder는 다르면 거부한다(§5).
- deep-review capability는 시작 probe에 성공한 기준 분석기가 하나 이상 있을 때만 광고한다. 추가 깊이 도구만 있는 노드는 광고하지 않는다. 현재 기준 분석기는 go vet뿐이므로 `--go` 없는 노드는 deep-review를 광고하지 않는다. W17 등이 자기 생태계의 기준 분석기를 추가하면 그 도구만 있는 노드도 광고할 수 있다.
- 기준 분석기는 해당 언어 노드에서 가장 흔히 쓸 수 있는 도구로 고른다. 팩 문서에 선택 근거를 기록한다.

### 2.7 저장소 코드 실행 허가

정적 분석도 저장소가 정한 코드를 실행할 수 있다. JS 설정 파일, build script, compiler plugin, proc macro, configure 단계가 그렇다([Native 실행기 §2](08-native-execution.md#2-신뢰와-실행-정책)). 이런 작업은 노드 운영자가 명시적으로 허가한 소스에서만 실행한다(결정 A23).

- **허가**: `codeye agent --repo-code-source-root ID`(반복 가능). 모델 전송 허가 `--model-source-root`와 같은 방식이다. ID는 같은 agent의 `--source-root` 매핑이 있어야 하며, 없으면 옵션 오류다. agent는 그 local source root의 job에만 `allow_repository_code=true`를 보낸다. Git 소스는 허가를 받지 않는다. 워커는 이 값을 `analysis.Job.AllowRepositoryCode`로 받는다. 값을 정하는 것은 노드 설정과 job뿐이다. 스냅샷·저장소 설정·run 요청은 이 값을 올릴 수 없다.
- **선언**: `pack.Task.RepositoryCode`. `Plan`이 manifest와 scope만으로 정하므로 미설정 대역도 같은 선언을 계획한다. 한 도구가 두 방식을 함께 계획할 수 있다. 예를 들어 기존 compile DB를 읽는 clang-tidy 작업은 false, configure 단계를 거치는 작업은 true다. `Run`은 `Plan`이 선언한 것보다 더 실행하지 않는다. false로 선언한 작업이 실행 중 configure 단계로 넘어가는 대체 경로는 금지다.
- **집행**: `Composite.Run`이 dispatch 전에 한 번만 검사한다. 선언은 워커가 돌려준 TaskSpec이 아니라 소유 분석기의 계획에서 다시 얻는다. 계획에 없는 key는 실행하지 않고 실패(`unknown_task`)로 끝낸다. 허가가 없으면 `unsupported`, 사유 `repository_code_not_allowed`다. 분석기를 호출하지 않으므로 프로세스가 시작되지 않는다. not-run envelope(실행 0, 관찰 0, `processed_paths=[]`)를 쓴다. 허가가 있어 프로세스를 실행한 작업에는 `repository_code_executed` 제약을 남긴다.
- **우선순위**: 허가 검사가 도구 검사보다 먼저다. 허가가 없으면 도구도 없더라도(`tool_not_configured`·`tool_unavailable`·`toolchain_not_configured`) 작업 사유는 `repository_code_not_allowed`다. 도구 사유는 제약 목록에 함께 남는다. 허가가 있으면 대역의 사유가 그대로 쓰인다.
- **필수 여부와 무관**: 허가가 없는 기준 분석기 작업은 필수 작업 미완료다. 그 보고서는 `partial`이다.
- 이 허가는 [docs/08 §2](08-native-execution.md#2-신뢰와-실행-정책)의 프로젝트 단위·revision 기반 권한 저장소를 대신하는 노드 운영자 설정이다. 프로젝트 권한 저장소는 구현하지 않았다.

**Go 팩은 저장소 코드를 실행하지 않는다.** go vet과 staticcheck 작업은 `RepositoryCode=false`다.

- 두 도구는 패키지를 읽어 type-check하고 분석할 뿐 패키지 코드를 실행하지 않는다. 테스트 파일도 type-check만 한다. 분석 코드는 toolchain의 vet analyzer와 staticcheck 자신의 것이다. `-vettool`·`-toolexec`·`-exec`는 쓰지 않는다.
- `CGO_ENABLED=0`이므로 cgo 파일을 빌드하지 않는다. C 컴파일러, `#cgo` 지시문, pkg-config를 호출하지 않는다.
- `//go:generate`는 `go generate`만 실행한다. go vet·`go list`·staticcheck는 실행하지 않는다.
- `GOTOOLCHAIN=local`이므로 go.mod의 `toolchain`·`go` 줄이 다른 toolchain을 내려받거나 실행하게 만들지 못한다. `GOFLAGS=`(빈 값), `GOENV=off`, `GOWORK=off`이므로 저장소·사용자 설정이 명령 옵션을 바꾸지 못한다. `GOPROXY=off`, `GOVCS=*:off`이므로 모듈을 내려받거나 VCS 명령을 실행하지 않는다.
- go.mod의 `go` 줄은 언어 판이다. 일부 analyzer의 대상이 바뀌지만(예: Go 1.22의 반복 변수) 프로그램의 의미를 바꾸는 코드의 일부로 본다.

**W20 메모**: `cargo check`와 `cargo clippy`는 항상 build script(`build.rs`)와 proc macro를 컴파일하고 실행한다. 따라서 Rust 기준 분석기 작업은 `RepositoryCode=true`이고, 허가가 없는 소스에서 Rust 기준 분석은 `unsupported`(`repository_code_not_allowed`)다.

### 2.8 범위에 없는 생태계와 분석기 공백

- **범위에 모듈이 없는 생태계는 작업을 계획하지 않는다.** 다른 생태계의 분석기가 자리 작업을 계획해 실패를 만들지 않는다. go vet은 팩 멤버로서 Go 소스가 범위에 없으면 아무것도 계획하지 않는다. R0의 필수 자리 작업(`no_go_files`, `scope_has_no_go_packages`)은 composite에서 사라졌다.
- **분석할 수 없는 소스는 그 생태계의 팩이 보인다.** Go 소스가 범위에 있는데 go.mod가 없거나 패키지를 만들 수 없으면 go vet은 기존 자리 작업(`unsupported`, `go_module_missing` 등)을 그대로 계획한다. 다른 팩도 자기 생태계의 소스를 분석할 수 없으면 같은 방식으로 보이는 작업을 계획한다.
- **범위의 생태계에 분석기가 없으면 공백 작업 하나를 계획한다.** composite가 생태계마다 `pack-gap.<생태계>` 작업을 만든다. 이 작업은 필수가 아니고 단계는 `static_analysis`다. 프로세스 없이 `unsupported`(`analyzer_pack_missing`)로 끝나고 not-run envelope(분석기 ID `<생태계>.none`, 도구 `none`, `files_in_scope`=해당 소스 수)를 쓴다. 단계 표시는 그 사유를 보인다. 예: Python만 있는 스냅샷은 `static_analysis`가 `skipped`(`analyzer_pack_missing`)다. 보고서에는 `static_analysis_gap:<생태계>` 제약과 not-run 도구 행이 남고 상태는 `partial`이다. 필수 작업으로 세지 않으므로 `required_task_not_succeeded`가 되지 않는다. 분석하지 않은 소스를 0건 통과로 보이지 않기 위해 보고서는 완료가 아니다.
- **지원 언어 소스가 하나도 없으면** 분석기 작업이 없다. 단계는 `no_tasks`이고 보고서는 `static_analysis_not_planned` 제약과 함께 `partial`이다.
- **"범위에 있다"의 정의는 하나다**: `pack.SourceFiles`. 범위 안의 포함 파일 중 소스 확장자(Go `.go`, Python `.py`·`.pyi`, JS/TS `.js`·`.jsx`·`.mjs`·`.cjs`·`.ts`·`.tsx`·`.mts`·`.cts`, Rust `.rs`, C/C++ `.c`·`.cc`·`.cpp`·`.cxx`·`.h`·`.hh`·`.hpp`·`.hxx` 등)를 센다. 숨김 디렉터리, `testdata`, `vendor`, `node_modules` 아래 파일은 세지 않는다. build 파일만으로는(Makefile, package.json, pyproject.toml) 생태계가 범위에 들어오지 않는다. Makefile이 있는 Go 저장소에 C/C++ 공백이 생기지 않게 하기 위해서다. 모듈 판단은 각 팩이 한다.
- **분석기가 있는지는 카탈로그가 정한다.** 생태계의 멤버(설정된 분석기든 미설정 대역이든)가 하나라도 있으면 공백 작업을 만들지 않는다. 그 생태계는 그 팩이 계획한다. 팩은 카탈로그에 자기 항목만 추가하면 된다. `any` 생태계 분석기(W21 구조·비밀정보)는 언어 공백을 메우지 않는다.
- `pack-gap` 이름은 예약어다. 분석기 이름으로 쓸 수 없고 다른 분석기의 소유 범위와 겹칠 수 없다.

review-v1 고정은 공백 작업과 무관하다. `TestReviewV1IsPinned`는 정적 계획 대신 고정 stub으로 역할 key·지침·코퍼스·요청 바이트를 고정한다. 정적 작업 목록이 바뀌어도 v1 요청은 같다.

## 3. review-v1과 go vet 고정

- go vet은 `govet.Handler.Analyzer()`로 멤버가 된다. 계획(key·제목·필수·단계), 실행, v0.1 문서 바이트가 W08과 같다. `builtin.TestGoVetUnchangedInComposite`가 go vet 단독 handler와 composite 경유 결과를 바이트 단위로 비교한다.
- `review.TestReviewV1IsPinned`는 수정하지 않았다. review-v1의 요청·지침·코퍼스는 그대로다.
- 바뀐 것: result manifest의 handler 이름이 `go-vet`에서 `analyzers`로 바뀐다. Go 모듈이 있는 스냅샷의 계획에 `staticcheck`(·`.mNN`) 작업이 go vet 작업 뒤에 추가된다. staticcheck를 설정하지 않은 노드에서는 §2.5에 따라 `static_analysis` 단계가 `partial`로 표시된다. Go 소스가 범위에 없는 스냅샷에서는 go vet 자리 작업 대신 §2.8의 공백 규칙을 따른다(단독 `govet.Handler`의 W08 동작과 TC-69 단위 시험은 그대로다).

## 4. 관찰 envelope `analyzer_observations` v0.1

role `observations`, media `application/vnd.codeye.observations+json`, `kind=analyzer_observations`, `schema_version=0.1`. go vet 문서(`kind=go_vet_observations`)와 media·버전은 같고 kind로 구별하므로 W08 decoder는 바뀌지 않는다. canonical JSON이며 노드 절대 경로와 환경변수 값을 담지 않는다.

| 필드 | 내용 |
|---|---|
| `analyzer` | `id`(`<생태계>.<도구>`), `ecosystem`, `required` |
| `adapter`, `parser` | 구현 ID와 버전 |
| `task_key`, `snapshot_manifest_digest` | 소비자가 신뢰하는 task와 스냅샷에 묶음 |
| `module` | 스냅샷 상대 `dir`과 생태계 이름(`name`, Go 모듈 경로 등) |
| `patterns` | 도구 범위 인자 |
| `tool` | `name`, 관측한 `version`(없으면 null), `version_tested` |
| `target` | `platform`과 생태계별 `properties`(Go: goos, goarch, cgo_enabled, build_tags, go_flags, toolchain, tests) |
| `rule_pack` | CODEYE 규칙 선택 `id`·`version`·`selection`, 저장소 설정 처리(`overridden`/`honored`/`none`), 읽은 저장소 설정 파일 `config_files`(경로·manifest SHA-256), 소스 내 억제 처리(`honored`/`reported`/`disabled`/`none`), `reported`인데도 억제를 보고하지 않는 규칙 `unreported_suppression_rules`. 규칙은 §5.1 |
| `outcome` | `task_status`, `reason`, `exit_class`, `partial_parse` |
| `executions` | 실행마다 이름, 상태, exit code, 정리 결과, 상대화한 argv·cwd·환경변수 이름, stdout/stderr의 SHA-256·크기·잘림 |
| `coverage` | `unit`, `files_in_scope`(null=셀 수 없음), `processed_paths`(null=모름, []=0), `files_skipped`, `files_unlisted`, `units`(단위 상태별 수) |
| `observations` | `id`, `rule_id`, `category`, `raw_severity`, `raw_message`, `path`, `location`(`column_unit`: byte/utf16/rune), `location_state`, `related[]`, `raw_ref`, `unit`, `suggested_fix_count`, `suppressed`(도구가 소스 안 억제로 숨겨졌다고 보고한 진단) |
| `raw_ref` | `execution`, `stream`, `stream_sha256`, `record_index`, `json_pointer`, `byte_start`/`byte_end` |
| `tool_errors` | 분석하지 못한 단위(타입 오류, 의존 누락, 로드 실패). 코드 관찰이 아님 |
| `parse` | `ok`, 레코드 수, 미해석 바이트, 형태 오류, decode 오류, 관찰·미해결·도구 오류 수 |
| `workspace_drift`, `limitations` | 작업 사본 변경 파일, 정렬된 제약 코드 |

`obsdoc.Validate`의 주요 규칙은 다음과 같다.

- 성공이면 exit class가 **억제되지 않은** 관찰(`Document.Active`) 유무와 맞아야 한다. 억제된 관찰만 있으면 `success_without_findings`다. 완전한 parse, 도구 오류·drift 없음, 실행 1개 이상, 모든 실행의 정상 종료·정리 확인·잘림 없음, `processed_paths`가 비어 있지 않아야 한다.
- 실패 상태는 성공 exit class를 가질 수 없다. `not_run`은 관찰이 없고 `processed_paths=[]`이어야 한다.
- 관찰의 `raw_ref`는 문서에 기록된 실행의 stream SHA-256과 수집 크기 안에 있어야 한다. 위치는 manifest의 파일·blob SHA-256과 일치해야 한다. drift 파일에 연결할 수 없다.
- 관찰 ID는 `obs_` + SHA-256(analyzer, 모듈, 단위, 규칙, 마스킹한 위치, 메시지, 같은 튜플 안의 순번) 앞 20자리다. 출력 순서와 무관하다.
- `suppressed=true` 관찰은 `source_suppressions=reported`일 때만 허용한다. `unreported_suppression_rules`는 정렬되고 중복 없는 규칙 ID 목록이며 `reported`가 아니면 `[]`이다.
- `config_files`는 `repository_config`가 `honored`가 아니면 빈 목록이다. `honored`이면 null(팩이 읽은 파일을 열거하지 못함) 또는 경로 순으로 정렬되고 중복 없는 256개 이하 목록이며, 각 SHA-256은 manifest의 그 파일과 같아야 한다.

## 5. 보고서 decoder

`report.AnalyzerDecoder`는 기본 registry에 (media, `0.1`, `analyzer_observations`)로 등록된다.

1. `obsdoc.Decode`로 task key·스냅샷·manifest 조회와 함께 검증한다.
2. `analyzer.required`가 DB task의 필수 여부와 다르면 거부한다. DB task가 성공인데 문서가 성공이 아니면 거부한다. 문서가 성공인데 DB가 아니면 DB를 따르고 제약을 남긴다.
3. 문서의 각 실행은 워커가 기록한 실행이어야 한다. stdout·stderr artifact의 SHA-256·크기, 성공 task의 exit code를 대조한다. 관찰은 기록된 stream artifact의 범위 안에 있어야 한다.
4. 관찰은 보고서 관찰이 된다. `raw_severity`는 짧은 단어(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`, 예 `error`)일 때만 옮기고, 그 밖은 envelope에만 남긴다. byte가 아닌 열 단위는 열을 0으로 두고 제약을 남긴다.
5. manifest와 맞고 억제되지 않은 관찰은 **검토 전 도구 finding**이 된다. 억제된 관찰은 보고서 관찰(`suppressed: true`)로 남고 finding이 되지 않는다. 억제된 관찰을 인용하는 finding은 보고서 검증이 거부한다. severity `low`, validation `unreviewed`, disposition `open`이고 검토 없이는 actionable이 아니다. 문구는 도구 이름이나 코드를 담지 않는 고정 템플릿이므로 코드 제외 export에도 남는다.
6. **analyzer·규칙·경로·시작 행·시작 열·메시지가 모두 같은 관찰만** 한 finding으로 묶는다(TC-09). 같은 행이라도 메시지가 다르면 별도 finding이다. 실제 예: SA4006 두 건(`err`, `n`)은 같은 위치지만 두 finding이다. 도구 사이의 자동 병합은 하지 않는다(§9).
7. fingerprint는 W23의 공용 `report.ToolFingerprint(analyzer, rule, path, "", message)`로 만든다(fp2와 `fingerprint_basis`, [W23 기록](40-w23-history-comparison.md)). decoder가 자기 hash를 만들지 않는다. 팩 decoder도 이 함수를 호출해야 한다. 줄 이동은 identity를 바꾸지 않는다.
8. task마다 도구 범위 행을 하나 만든다(`coverage.tools`, `analyzer`는 envelope의 analyzer ID). 처리 파일 수(`processed_files`)는 DB가 성공으로 받은 task이거나 `not_run`일 때만 센다(`not_run`은 0). 처리 경로(`processed_paths`)는 DB가 성공으로 받은 task에만 기록한다. W23 비교는 이 경로에 있는 위치만 다시 검사한 것으로 본다. 그래서 staticcheck가 설정되지 않은 run에서 사라진 staticcheck finding은 `resolved_observed`가 아니라 `absent_unverified`다. 보고서의 `tool_processed_files`는 처리 경로의 **합집합**이다. go vet과 staticcheck가 같은 파일을 읽으면 한 번 센다. 처리 수와 경로 수가 다른 행이 있으면 합계를 쓴다. 행에는 envelope `rule_pack`에서 만든 `rule_context`를 기록한다(§5.1).

거부된 envelope는 `artifact_rejected:<key>` 제약이 된다. 그 task는 해석되지 않은 것으로 처리되고 처리 파일 수는 미확정이 된다.

### 5.1 규칙 문맥과 소스 안 억제 (builder 4, TC-104)

도구 버전 말고도 어떤 진단이 나올지 정하는 것이 있다. CODEYE 규칙 선택, 저장소 설정, 소스 안 억제 주석이다. W23 비교는 사라진 도구 finding을 **양쪽의 규칙 문맥이 알려져 있고 같을 때만** `resolved_observed`로 본다. 그렇지 않으면 `absent_unverified`다. 모든 팩이 이 계약을 따른다.

보고서 도구 행의 `rule_context`(보고서 builder `4`부터):

| 필드 | 내용 |
|---|---|
| `rule_pack`, `rule_pack_version`, `selection` | envelope `rule_pack`의 id·version·selection |
| `repository_config` | `overridden`(CLI가 저장소 설정을 대체), `honored`(도구가 저장소 설정을 읽음), `none`(설정 없음) |
| `source_suppressions` | `reported`, `honored`, `disabled`, `none`(아래) |
| `unreported_suppression_rules` | `reported`인데도 억제를 보고하지 않는 규칙(비어 있으면 생략) |
| `config` | `not_read`(`honored`가 아님), `recorded`(읽은 파일을 기록함), `unrecorded`(`honored`인데 `config_files=null`) |
| `config_paths`, `config_digest` | `recorded`일 때만. digest는 SHA-256(`codeye-rule-config-v1\0` + 경로 순으로 각 `path\0sha256\n`)이다. **코드 제외 export에서 뺀다.** `fingerprint_basis`처럼 추측한 설정 내용을 확인하는 수단이 되기 때문이다. 모드와 `config` 상태는 남는다 |

문맥이 **알려짐**은 `config`가 `not_read`이거나, `recorded`이고 digest가 있는 경우다. 문맥이 없는 행(builder 3 이하 보고서의 go vet 외 분석기)은 오류가 아니라 **모름**이다. 비교 규칙(같은 분석기의 모든 행을 모아 비교한다):

- 한쪽이라도 모름 → `rule_context_unknown`
- 양쪽 다 알려졌고 다름(규칙 팩·버전·선택·모드·설정 digest 중 하나) → `rule_context_changed`. 차이는 `tool:<analyzer>:rule_context`에 보인다(설정은 digest 앞 12자만)
- 현재 쪽 도구가 같은 fp2의 진단을 **억제됨으로 보고함** → `suppressed_in_source`. 새 finding의 기준 쪽이 그 진단을 억제됨으로 보고했으면 `new`에 같은 사유를 붙인다(억제를 지운 경우)
- 현재 쪽이 `source_suppressions=honored`이거나 그 finding의 규칙이 `unreported_suppression_rules`에 있음 → 사라진 finding은 `source_suppressions_hidden`

우선순위는 위치 미검사(`check_failed`, `location_not_inspected`) → `suppressed_in_source` → 도구 버전(`rules_unknown`, `rules_changed`) → 규칙 문맥(`rule_context_unknown`, `rule_context_changed`) → `source_suppressions_hidden`이다. 억제 보고는 직접 관찰이므로 버전 차이보다 앞선다.

팩이 지킬 규칙:

1. **저장소 설정.** CLI 인자로 저장소 설정을 대체하면 `overridden`이다. 이때 대체되지 않는 저장소 설정 항목이 진단에 영향을 주지 않는다는 것을 측정해야 한다. 저장소 설정을 읽게 두면 `honored`이고, 도구가 실제로 읽은 설정 파일(상속·include 포함)을 `config_files`에 모두 적어야 한다. 열거할 수 없으면 `null`로 두며, 그 분석기의 finding은 비교에서 해결되지 않는다(`rule_context_unknown`). 스냅샷 밖에서 읽은 설정(사용자·전역 설정, 설치된 공유 설정 패키지)은 manifest SHA-256으로 묶을 수 없으므로 기록할 수 없다. 도구가 그런 설정을 읽을 수 있으면 `null`이다. 열거하지 못하면서 `overridden`이나 빈 목록을 주장하지 않는다.
2. **소스 안 억제.** 도구가 억제된 진단을 표시해 보고할 수 있으면 `reported`로 하고 관찰에 `suppressed=true`를 쓴다. `reported`는 선택한 **모든 규칙**에서 성립해야 하며 규칙 계열마다 측정한다. 억제를 보고하지 않는 규칙은 `unreported_suppression_rules`에 적는다. 비교는 그 규칙의 사라진 finding을 `source_suppressions_hidden`으로 둔다. 억제된 관찰은 finding이 되지 않고 R1 해석 입력에도 들어가지 않는다(§7). 제약 `source_suppressed_observations`를 남긴다. 도구의 억제를 끄고 모든 진단을 받으면 `disabled`, 억제 문법이 없으면 `none`이다. 어느 것도 할 수 없으면 `honored`이다. 이 경우 비교는 사라진 진단이 고쳐졌는지 억제됐는지 구분할 수 없다. 파일 SHA-256이 바뀐 것만으로는 수정과 억제 주석 추가를 가를 수 없으므로 `honored` 분석기의 사라진 finding은 언제나 `absent_unverified`(`source_suppressions_hidden`)다.
3. 규칙 선택이나 그 의미를 바꾸면 `rule_pack.version`을 올린다. 이전 결과와는 `rule_context_changed`가 되어 해결로 보지 않는다.

**go vet.** v0.1 문서는 byte 고정이고 rule pack이 없다. 문서는 바꾸지 않고, 보고서 decoder와 비교가 `codeye-go-vet-default` v1(selection `[]`, `repository_config=none`, `source_suppressions=none`)을 합성한다. go vet의 분석기 집합은 toolchain 버전이 정하고, 그 버전은 이미 `tool_version`으로 비교한다. go vet에는 저장소 설정과 억제 문법이 없다. 이전 builder의 go vet 행도 같은 문맥이 되므로 W23 3회 smoke(수정 → `resolved_observed`)는 그대로 통과한다.

**staticcheck**는 `overridden`(§8.3)과 `reported`(`-show-ignored`), `unreported_suppression_rules=["U1000"]`을 기록한다. SA 검사에 `//lint:ignore`를 추가해 사라진 finding은 현재 run의 억제 관찰과 fp2가 같으므로 `suppressed_in_source`다. U1000은 억제해도 기록이 없으므로(§8.3) 사라진 U1000 finding은 `source_suppressions_hidden`이다.

## 6. 탐색 probe hook

`builtin.Probes(cfg)`가 `pack.ToolProbe` 목록(도구, 사용하는 분석기 ID, 생태계, 실행 파일, 버전 argv, parser)을 만든다. `discovery.ProbeObserver.Analyzers`로 env.discover에 들어간다.

- 스냅샷 범위의 생태계(런타임 도구에서 역산)와 `any`에 속한 도구만 다룬다. Python만 있는 스냅샷에서는 staticcheck를 기록하지 않는다.
- 설정된 도구: executor로 `<tool> <VersionArgs>`를 실행한다. cwd는 attempt scratch, 환경은 `PATH=<도구 디렉터리>`와 `LANG=C.UTF-8`(Windows는 `SystemRoot` 추가), 제한시간은 30초, 실행 이름은 `<tool>.version`이다.
- 설정되지 않은 도구: `not_probed`. 실행 파일이 없으면 `not_found`, 시작·종료·출력 문제는 `probe_failed`와 사유 코드다.
- 결과는 환경 문서의 `environment.analyzer_tools`에 쓴다. 추가 필드이며 W19 이전 문서에는 없다. 선언과 비교하지 않는다. 보고서는 관측 버전에 기록된 정상 probe 실행이 있어야 받아들인다.
- 버전 명령이 없거나 출력을 인식할 수 없는 도구는 쓸 수 없다. 식별되지 않은 실행 파일을 코드에 실행하지 않는다.

## 7. review-r1 해석 입력

`review.interpret`는 계획된 분석기 task(`record.AnalyzerTaskKey`: 탐색·검토 key가 아닌 key)의 산출물을 읽는다. 먼저 generic envelope를 `obsdoc.Decode`(manifest 조회 포함)로 검증하고, 없으면 go-vet 소유 key에 한해 v0.1 문서를 기존 검증으로 읽는다. 둘 다 없으면 상류 `unavailable`(`record_missing`)이다. 0건으로 바꾸지 않는다. 억제된 관찰(§5.1)은 넣지 않는다. 프로젝트가 숨기기로 한 진단이므로 후보가 되면 안 된다. 도구 행의 제약 `source_suppressed_observations`가 빠진 관찰이 있음을 알린다. 도구 상태 행은 최대 8개, 관찰은 최대 8개다. 넘치면 `tools_outside_interpret_input`, `observations_outside_interpret_input` 제약을 남긴다.

지침(`prompts/review-r1`)은 바꾸지 않았다. `languages/go.md`가 이미 context·goroutine·오류 경로를 다루고, 지침 변경에는 평가 데이터 회귀 검증이 필요하기 때문이다. review-v1은 변경하지 않았다.

## 8. Go 팩: Staticcheck 어댑터

### 8.1 실행

Go 모듈마다 task 하나(`staticcheck`, `staticcheck.m01`, … go vet과 같은 모듈 순서)를 만든다. 작업 사본의 모듈 디렉터리에서 다음 순서로 실행한다.

1. `go version` — toolchain 식별(실패·미인식 시 중단)
2. `staticcheck -version` — 도구 식별(실패·미인식 시 중단, §8.4)
3. `go mod edit -json` — 모듈 경로와 replace. 스냅샷 밖 filesystem replace는 `go list` 전에 거부(`skipped_policy`, `replace_outside_snapshot`)
4. `go list -e -json=<필드> ./...` — 같은 build context의 패키지 목록(처리 파일·무시 파일 결정)
5. `staticcheck -f json -checks <선택> -tests=true -show-ignored ./...`

go.mod는 실행 전에 manifest SHA-256과 대조한다. 원본 경로·스냅샷에서는 실행하지 않는다. 스냅샷 디렉터리와 운영자 모듈 캐시는 보호 경로다.

### 8.2 환경과 build context

go vet과 같은 명시적 환경을 쓴다: `PATH=<go bin>`, `LANG=C.UTF-8`, `GOENV=off`, `GOWORK=off`, `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOVCS=*:off`, `GOFLAGS=`(빈 값), `CGO_ENABLED=0`, task scratch의 `GOCACHE`·`GOPATH`·`STATICCHECK_CACHE`, `GOMODCACHE`는 운영자 캐시(`--go-mod-cache`, 읽기 전용 보호) 또는 빈 scratch 캐시다.

**Go telemetry를 끈다.** go1.27.4(Linux)에서 측정한 결과, telemetry mode 파일이 없으면 go 명령(`go version` 포함, staticcheck가 부르는 `go list`도 같다)은 `os.UserConfigDir()/go/telemetry`에 counter 파일을 쓰고 분리된(setsid) upload 자식을 시작한다. W05의 실행별 HOME 아래였으므로 이 자식은 executor가 HOME을 지우고 정리를 확인한 **뒤에** 파일을 다시 만들었다. probe 디렉터리가 비지 않는 간헐 실패(`builtin.TestHandlersAdvertiseOnlyProbedToolchains`)가 이 때문이었다. 프로세스 그룹을 벗어난 자손이 정리 확인 뒤에도 쓴다는 뜻이므로, 정리 확인이 자손 전체를 보장하지 않는다는 executor 제한이기도 하다. `GOTELEMETRY=off` 환경 변수는 효과가 없었다(읽기 전용 보고 값). 그래서 go vet·staticcheck의 실행과 probe, env.discover의 go probe는 scratch의 `config`를 `XDG_CONFIG_HOME`으로 주고 `config/go/telemetry/mode`에 `off`를 쓴다. mode가 `off`면 go 명령은 counter를 열지 않고 자식도 시작하지 않는다(`govet.DisableTelemetry`, `TestRealGoVetTelemetryOff`). `os.UserConfigDir`는 darwin을 뺀 Unix에서만 `XDG_CONFIG_HOME`을 읽는다. Windows는 executor 환경에 `AppData`가 없어 설정 디렉터리를 정할 수 없으므로 telemetry가 꺼진다(소스 확인, 실행 측정 아님). darwin은 덮어쓸 수단이 없어 다루지 않는다.

`GOFLAGS`는 비워 Go 기본값을 따른다. 기본값은 vendor 디렉터리가 없으면 `-mod=readonly`, 있으면 `-mod=vendor`다. `-mod=readonly`를 강제하면 vendor 모듈 프로젝트가 실패하는 것을 측정했다. go vet도 같은 선택이다. build tag 지정은 없다(기본 context), CGO는 끈다, GOOS/GOARCH는 노드 기본값이다. 이 값은 `target.properties`와 제약(`default_build_context_only`, `cgo_disabled`)으로 기록한다. 그 밖의 tag 파일은 `files_skipped`와 `build_constraint_ignored_files`로 보인다.

### 8.3 규칙 팩 `codeye-go-staticcheck` v2

`-checks SA*,U1000,-SA1014,-SA1017,-SA2000,-SA2002,-SA4021,-SA5009 -tests=true -show-ignored`. SA(정확성·동시성·테스트·죽은 코드·성능)와 U1000(미사용 코드)을 실행한다. S1(단순화)·ST(스타일)는 실행하지 않는다. 제외한 SA 검사는 §9의 go vet 중복이다. `-checks`가 저장소 `staticcheck.conf`의 checks를 대체하므로 `repository_config=overridden`, `config_files=[]`이다.

v2는 `-show-ignored`를 더했다(v1은 `source_suppressions=honored`였다). staticcheck 2026.2.2에서 측정한 결과는 다음과 같다.

- `//lint:ignore`와 `//lint:file-ignore`로 숨긴 문제는 severity `ignored` 레코드로 나온다. 어댑터는 이를 `suppressed=true` 관찰로 두고 `source_suppressions=reported`와 제약 `source_suppressed_observations`를 기록한다(§5.1).
- 숨긴 문제만 있어도 exit 1이다. 그래서 exit class는 억제되지 않은 관찰로 정한다(§8.5).
- 이유가 빠진 잘못된 지시문은 `compile` 레코드(`malformed linter directive`)다. 타입 오류와 같이 `package_errors`로 실패한다.
- 아무것도 숨기지 않은 지시문은 code `staticcheck` 레코드("linter directive didn't match anything")다. 규칙 `staticcheck`인 일반 관찰과 finding이 된다.
- **U1000은 예외다.** 선언 위의 `//lint:ignore U1000`과 `//lint:file-ignore U1000`은 `-show-ignored`에서도 아무 레코드를 남기지 않는다. 같은 파일의 억제하지 않은 미사용 함수는 U1000으로 보고된다. 함수 본문 안에 둔 `//lint:ignore U1000`은 숨기지 못하는데, "didn't match" 레코드도 나오지 않는다. 그래서 규칙 팩은 U1000을 `unreported_suppression_rules`에 적는다. 실제 도구 시험이 이 측정을 고정한다(§10).
- 생성 파일 표지(`// Code generated … DO NOT EDIT.`)가 있는 파일에서도 SA1000·SA4006은 그대로 보고되고, 미사용 함수의 U1000은 `-show-ignored`에서도 기록이 없다. 표지를 붙여 사라진 U1000 finding도 위 규칙으로 `source_suppressions_hidden`이 된다. SA 검사 중 측정한 것은 이 둘뿐이다.

범주 정책: SA1019·U1000·S1·ST → maintainability, SA2 → concurrency, SA3 → test_gap, SA6 → performance, 나머지 → correctness. 범주는 검사의 주제이며 영향 판정이 아니다. 원문 severity(staticcheck는 `error`)는 그대로 둔다.

### 8.4 버전 계약

- `staticcheck -version` 출력 `staticcheck 2026.2.2 (0.8.2)`에서 `2026.2.2 (0.8.2)`를 읽는다. 시험한 계열은 `2026.2`다.
- 다른 계열이라도 형식이 맞으면 실행하고 `tool_version_untested`와 `version_tested=false`를 기록한다.
- 개발 빌드(`staticcheck (devel, …)`)나 형식 불일치는 check를 실행하기 전에 `failed`(`tool_version_unrecognized`)로 끝난다. 시작 probe에서 거부되면 대역이 `tool_unavailable`로 끝난다.
- JSON parser는 2026.2 레코드 형식에 고정했다. 알 수 없는 필드, 빠진 필드, 잘못된 타입은 형태 오류이며 결과를 0건으로 만들지 않는다.

### 8.5 종료 코드와 결과

staticcheck 2026.2.2(`-fail` 기본값 all)를 실측한 결과다. 문제를 하나라도 보고하면 exit 1, 없으면 0이다. 사용법 오류·crash는 2다. compile 레코드와 `err:` 치명 오류도 exit 1이므로 종료 코드만으로 판단하지 않는다.

| 관측 | 결과 |
|---|---|
| exit 0, 레코드 없음, stderr 없음 | succeeded / `success_without_findings` |
| exit 1, 검사 레코드만, stderr 정상 | succeeded / `success_with_findings` (TC-07) |
| exit 1, `ignored` 레코드만 | succeeded / `success_without_findings`, 관찰은 모두 `suppressed` (TC-104) |
| exit 0 + stderr `matched no packages` | unsupported / `no_analyzable_packages` |
| exit 1 + `compile` 레코드(타입 오류) | failed / `package_errors`, 다른 패키지 관찰 보존, 처리 파일 모름 (TC-08) |
| 오프라인 의존 누락, toolchain 부족, go.mod 모듈 누락 | failed / `dependency_unavailable`, `toolchain_unavailable`, `go_module_missing` (configuration_error, TC-68) |
| exit 1 + `err:`만, exit 2, signal | failed / `staticcheck_failed` |
| exit 0인데 레코드 있음 | failed / `exit_status_mismatch` |
| stdout 잘림·손상 | failed / `output_truncated`, `output_unparsed` (parse 보존) |
| timeout·취소·정리 불확실 | timed_out, cancelled, failed/`cleanup_uncertain` |

실패한 실행은 처리 파일을 주장하지 않는다(`processed_paths=null`). 오프라인 누락에서 내려받지 않으며, 빈 scratch 모듈 캐시에 파일이 생기지 않음을 시험했다.

## 9. go vet과 Staticcheck: 서로 다른 엔진과 중복

staticcheck는 go vet을 실행하지 않는 별도 엔진이다. 일부 검사는 go vet analyzer와 같은 구성을 보고한다. 한 fixture에서 go1.27.4 go vet과 staticcheck 2026.2.2를 함께 실행해 측정했다(`TestRealStaticcheckVetOverlapExcluded`).

| 제외한 staticcheck 검사 | 같은 구성을 보고하는 go vet analyzer |
|---|---|
| SA1014 | unmarshal |
| SA1017 | sigchanyzer |
| SA2000 | waitgroup |
| SA2002 | testinggoroutine |
| SA4021 | appends |
| SA5009 | printf |

SA4000(동일 피연산자)은 go vet `bools`보다 넓은 검사여서 유지한다. 같은 줄에서 두 도구가 보고하면 관찰 두 개가 각 task에 남는다. 보고서는 도구 사이에서 자동 병합하지 않는다. 같은 원인인지 판단하는 일은 검토 단계에 둔다. 처리 파일은 합집합으로 센다(§5).

## 10. 시험과 실행 근거

실제 도구 출력 캡처(redacted stdout·stderr·exit code, `internal/worker/analysis/staticcheck/testdata/`)는 다음 명령으로 만들었다.

```text
CODEYE_TEST_STATICCHECK=$HOME/.local/share/codeye-tools/go/bin/staticcheck \
CODEYE_W19_CAPTURE=$PWD/internal/worker/analysis/staticcheck/testdata \
go test -count=1 -run TestRealStaticcheck ./internal/worker/analysis/staticcheck/
```

[실제 산출물 예시](../examples/observations-staticcheck.json)는 같은 실행에 `CODEYE_W19_DUMP=<dir>`를 주어 얻은 `TestRealStaticcheckFindingsExitOne` envelope다. fixture 소스는 저장소의 gofmt·vet·staticcheck가 보지 않도록 `helpers_test.go`에 문자열로 둔다. 바꾸면 다시 캡처해야 한다.

| 시험 | 범위 |
|---|---|
| `staticcheck.TestRealStaticcheck*` (도구 필요) | findings exit 1, 정상, 타입 오류 부분 parse, 오프라인 의존·toolchain, 분석 대상 없음, 스냅샷 밖 replace, 손상 출력, 취소·timeout, go vet 중복 제외, 원본·스냅샷 불변, 노드 경로 미노출, 재실행 ID 동일 |
| `staticcheck.TestRealStaticcheckOperatorModuleCache` (도구와 `CODEYE_TEST_GOMODCACHE` 필요) | 운영자 모듈 캐시의 의존 모듈로 오프라인 성공, 캐시는 보호 경로이며 실행 전후 내용 동일 |
| `staticcheck.TestReplay*` (CI) | 캡처 바이트 재생: 위 판정, exit 매핑 표, 버전 계약, argv·cwd·환경·제한시간 |
| `staticcheck.TestParse*`, `TestPlanModules`, `TestCategoryPolicyAndArgs` | parser(손상·잘림·알 수 없는 필드), stderr, 버전, 계획 key·순서·필수 아님·Go 없음 |
| `pack.Test*` | 계획 연결·dispatch·소유권·예약 이름·필수 불일치·대역 envelope·배타 쓰기 |
| `pack.TestRepositoryCodeGrantIsEnforcedBeforeDispatch`, `agent.TestRepositoryCodeGrantIsSeparateFromAcquisition` (TC-103) | 허가 없음·있음, 한 도구의 두 방식, 대역의 같은 선언과 사유 우선순위, 계획 밖 key 거부, 프로세스 미시작, Git 소스 미허가, 매핑 없는 ID 옵션 오류 |
| `obsdoc.Test*` | 실제 예시 round trip, 46개 거부 사례, 규칙 문맥(`config_files`·`suppressed`) 수용 |
| `builtin.TestGoVetUnchangedInComposite`, `TestHandlers*` | go vet 고정, capability 광고, 도구 플래그 |
| `govet.TestDisableTelemetry`, `govet.TestRealGoVetTelemetryOff` (Linux, go 필요) | 운영체제별 telemetry 끄기, 실제 go vet 실행 뒤 telemetry 디렉터리에 mode 파일만 남음(counter·upload 자식 없음), 모든 실행의 `XDG_CONFIG_HOME` |
| `report.TestAnalyzerDecoder*` | TC-09·TC-13, fp2 fingerprint, 처리 경로·합집합, 미설정 행, 위조 거부, 코드 제외 export |
| `history.TestStaticcheckFindingsCompare` | W23 비교: 줄 이동 persisting, 성공한 재검사 resolved, 미설정 run은 absent_unverified |
| `history.TestTC104RuleContextGatesResolution`, `history.TestGoVetRuleContextIsSynthesized` (TC-104) | builder 3 staticcheck 기준·현재 행의 문맥 없음, 선택·규칙 팩 버전·설정 모드·설정 파일 변경, unrecorded, 억제 보고와 그 우선순위, honored 억제, 억제를 보고하지 않는 규칙(U1000)과 같은 run의 SA 해결, 같은 설정의 해결, 다른 메시지의 억제, 억제 해제 후 new, 차이 표시, go vet 합성 문맥의 builder 3 해결 |
| `report.TestRuleContextAndSuppressedObservations`, `report.TestRuleContextConfigDigest` (TC-104) | 억제 관찰은 finding이 아님, 억제 fingerprint, staticcheck·go vet 행 문맥, 억제 관찰을 인용한 finding 거부, 설정 digest 규칙, 코드 제외 export에서 경로·digest 제거와 원본 불변, 모드와 상태 불일치 거부 |
| `staticcheck.TestRealStaticcheckSuppressionsReported` (도구 필요), `staticcheck.TestReplaySuppressionsReported` (CI) (TC-104) | `-show-ignored`: 줄·파일 억제가 `ignored` 관찰로 남음, 억제한 U1000은 기록 없음(억제하지 않은 U1000은 보고됨), 억제만 있는 exit 1의 `success_without_findings`, `reported`와 `unreported_suppression_rules=["U1000"]` 기록 |
| `discovery.TestAnalyzerToolProbe*`, `envdoc.TestAnalyzerToolsValidation` | probe 실행 계약, not_probed, 실패 기록, 생태계 범위, 실제 probe |
| `review.TestR1InterpretReadsGenericEnvelope` | R1 해석 입력에 두 엔진 관찰, not-run 행, 누락 상류, 억제 관찰 제외 |
| `run.TestAnalyzerPackWith*` (Linux) | 실제 워커: CI 조건의 미설정 가시성, 실제 go vet + staticcheck end-to-end |
| `pack.TestGapsForEcosystemsWithoutAnalyzer`, `govet.TestNonGoSnapshotUnsupported`, `report.TestStaticAnalysisGapMakesTheReportPartial` (TC-69) | 공백 작업 계획·실행·envelope, 확장자·scope·데이터 디렉터리 규칙, `any` 미충족, 예약 이름, go vet 멤버의 비 Go 무계획과 `go_module_missing` 유지, 보고서 `partial` |
| `run.TestNonGoSnapshotsShowTheGapNotARequiredFailure` (Linux, TC-69) | 실제 워커·composite·보고서·단계: Python만 있는 스냅샷은 `static_analysis` `skipped`(`analyzer_pack_missing`)·보고서 `partial`/`inconclusive`·필수 정적 작업 없음, 지원 언어가 없는 스냅샷은 `no_tasks`·`static_analysis_not_planned` |

실제 staticcheck finding이 든 보고서(전체·코드 제외)는 `CODEYE_W19_REPORT_DUMP=<dir> go test -run TestAnalyzerDecoderStaticcheckFindings ./internal/report/`로 꺼내 `report.schema.json`·`finding.schema.json`으로 검증했다(fixture의 Go 프로젝트에서는 staticcheck 진단이 없어 node smoke가 이 형태를 만들지 않는다). Windows는 바뀐 패키지의 시험을 windows/amd64로 빌드해 WSL interop으로 실행했다. Windows용 Go가 없는 환경이라 Go toolchain이 필요한 시험(재생, go vet 고정, 실제 probe)은 건너뛰었고 Windows CI의 Go로 실행된다. review-v1 역할은 스냅샷 코퍼스만 읽고 정적 계획·도구 산출물을 읽지 않으므로 staticcheck 작업이 추가돼도 v1 요청 바이트는 같다.

실행 결과와 명령별 통과 여부는 W19 PR 보고에 적는다. 실제 end-to-end는 `CODEYE_TEST_STATICCHECK`를 설정한 `go test -run TestAnalyzerPackWithRealStaticcheck ./internal/worker/run/`과 `scripts/test_postgres.py --nodes`(같은 변수로 `--staticcheck` 전달)로 실행한다.

## 11. 남은 제한과 다음 팩을 위한 메모

- Windows에서 실제 staticcheck를 실행하지 않았다. 경로 정규화는 Windows 단위 시험까지만 확인했다.
- executor는 setsid로 프로세스 그룹을 벗어난 자손을 추적하지 않는다. Go telemetry 자식은 끄는 것으로 막았지만(§8.2), 다른 도구가 분리된 자손을 만들면 정리 확인 뒤에도 실행별 디렉터리에 쓸 수 있다. 다른 팩은 도구의 백그라운드 자식(데몬, telemetry, 언어 서버)을 측정하고 끄는 방법을 기록해야 한다.
- darwin에서는 Go telemetry를 끄지 못한다(§8.2).
- build context는 노드 기본 GOOS/GOARCH, tag 없음, CGO 끔 하나다. tag·CGO·다른 target 조합은 계획하지 않는다.
- `related` 위치는 envelope와 R1 입력에만 있다. 보고서 finding은 주 위치만 보인다.
- R1 해석의 호출자 확장은 Go 코드에서만 동작한다(W15). 다른 언어 팩이 해석 입력을 쓰려면 코드 창 선택을 확인해야 한다.
- 공백 판단은 소스 확장자 기준이다(§2.8). 확장자가 없는 스크립트나 생성 파일은 세지 않는다. 범위에 소스가 있어도 그 생태계의 팩이 모듈을 찾지 못하면 그 팩이 보이는 작업을 계획해야 한다. composite는 이를 강제하지 않는다.
- 탐색 probe는 범위 안 생태계의 도구만 기록한다. `any` 생태계 도구(W21)는 항상 기록된다.
- `tool_processed_files` 합집합은 처리 수가 있는 모든 행이 같은 수의 경로를 줄 때만 쓴다. 그렇지 않으면 합계로 돌아간다.
- 보고서 builder 버전은 `4`다(§5.1의 `rule_context`와 관찰 `suppressed`). builder 3 이하의 go vet 외 분석기 행은 문맥을 모르므로, 그 기준과 비교한 사라진 finding은 해결되지 않는다(`rule_context_unknown`). W19 이전에 게시된 staticcheck 보고서는 없다.
- 규칙 문맥은 분석기 단위로 비교한다. 같은 분석기의 행이 여럿(모듈별 task)이면 모든 행의 문맥 집합이 같아야 한다. 한 모듈의 설정만 바뀌어도 그 분석기의 모든 사라진 finding이 `rule_context_changed`가 된다.
- `source_suppressions=honored`인 분석기의 finding은 비교에서 해결되지 않는다(§5.1). 억제를 보고하거나 끌 수 있는지 각 팩이 측정해야 한다.

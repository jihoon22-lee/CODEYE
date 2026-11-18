# 06. 정적 분석 도구와 어댑터 계약

R0의 제품 어댑터는 [오프라인 Go vet](29-w08-go-vet.md)다. W19에서 언어 팩이 공유하는 [분석기 팩 계약](39-r1-analyzer-packs.md)과 Go 팩의 Staticcheck 어댑터를 구현했다. 아래 다언어 도구 표의 나머지는 W16~W18·W20·W21의 구현 후보이며, 선택한 도구의 실제 지원 여부는 노드별 probe와 시험 결과로 판단한다. R1에서 모델이 원시 관찰을 해석할 때는 [진단 해석 지침](../prompts/review-r1/interpret-diagnostics.md)을 적용 후보로 평가한다.

## 1. 선정 원칙

도구를 나열하는 대신 분석 관점의 빈틈을 채운다. 동일 검사기를 포함한 wrapper와 원본을 중복 실행하지 않는다. 도구가 진단한 사실과 실제 결함 여부를 분리한다. 모든 도구를 필수 설치하지 않으며 프로젝트·OS·언어 버전·예산에 따라 계획한다.

## 2. 초기 카탈로그 후보

아래는 구현할 어댑터 후보이며 설치·버전·OS 호환성이 검증됐다는 뜻은 아니다. 정확한 CLI는 고정한 버전별 계약 테스트로 확인한다.

| 대상 | 기본 어댑터 후보 | 추가 깊이/선택 | 주의 |
|---|---|---|---|
| C/C++ | Cppcheck(기준, W16 구현), clang-tidy·Clang Static Analyzer(추가 깊이, W16 구현. CSA는 clang-tidy `clang-analyzer-*`로만 실행) | 컴파일 경고, 승인된 CTU, 복잡도/중복 | compile DB·헤더·매크로·ABI에 따라 분석 범위가 달라짐. compile DB는 데이터로 매핑하고 compiler·configure를 실행하지 않음. compile DB가 없으면 cppcheck 축소 모드·clang-tidy 미지원. cppcheck와 같은 구성만 보고한 clang-tidy 검사는 제외([§7](41-w16-cpp-pack.md#7-엔진-중복)) |
| Python | Ruff(기준, W17 구현), Pyright 또는 mypy·Bandit(추가 깊이, W17 구현) | 일부 Pylint 의미 규칙·모듈 의존 구조 | 타입 정보 부족은 별도 표시. Pyright/mypy 모두 실행은 명시 선택(`--python-both-type-checkers`). ruff S 규칙은 bandit과 중복이라 제외, 타입 검사기의 ruff 중복 검사도 제외([W17 §7](42-w17-python-pack.md#7-엔진-중복과-제외)) |
| JS/TS | ESLint 구문 규칙(기준, W18 구현), tsc·ESLint 타입 인지 규칙(추가 깊이, W18 구현) | dependency-cruiser, 승인된 로컬 Semgrep 규칙 | Node·tsconfig·프로젝트 서비스가 맞아야 함. JS config는 실행 가능 코드라 관리 설정만 사용. node_modules 없는 스냅샷의 타입 정보 공백은 진단 보류([W18](43-w18-js-ts-pack.md)) |
| Go | go vet(기준, 구현), Staticcheck(추가 깊이, W19 구현) | 소유권/취소 관련 선택 검사·패키지 구조 | tags·CGO·toolchain·target 기록. wrapper 내부 같은 엔진 중복 금지. go vet과 같은 구성을 보는 Staticcheck 검사는 제외([§9](39-r1-analyzer-packs.md#9-go-vet과-staticcheck-서로-다른-엔진과-중복)) |
| Rust | cargo check(기준, W20 구현), clippy(추가 깊이, W20 구현) | allow-by-default unsafe·panic review lint(구현)·unsafe 경계의 LLM 검토 | 모든 작업이 build script·proc macro·저장소 Cargo 설정을 실행하므로 노드 허가가 있어야 함. features·target은 기본값·host 하나로 기록. clippy 작업은 `clippy::` 진단만 남겨 rustc 진단 중복 제거([§4](44-w20-rust-pack.md#4-cargo-check와-clippy-서로-다른-작업과-중복)) |
| 공통 | 승인된 로컬 패턴 규칙, Gitleaks, 구조/복잡도 추출 | jscpd·Lizard·언어별 심볼/의존 그래프 | CVE 검사 아님. 구조 지표는 결함 확정 근거가 아님 |

Semgrep CE 어댑터는 지원하는 범위만 명세하며 파일 간 완전한 데이터 흐름 분석을 했다고 표시하지 않는다. 상용 분석 엔진을 필수 종속성으로 두지 않는다. 도구·규칙팩·바이너리의 사용/재배포 조건은 운영자 카탈로그에 기록한다.

## 3. 능력 모델

어댑터는 `languages`, `os`, `tool_version`, `target_versions`, `analysis_scope`, `requires_build`, `executes_repository_code`, `network_requirements`, `output_formats`, `rule_pack_digest`, `cancellation_support`, `resource_hint`를 제공한다.

analysis_scope는 text, syntax, typed, intraprocedural, interprocedural, interfile, structural 중 실제 지원 범위를 나열한다. 엔진에 따라 가능한 옵션이라도 이번 실행에서 꺼졌으면 실행 manifest에는 활성 범위만 기록한다. 미지원·검증 전·지원은 각각 구분한다.

## 4. 어댑터 인터페이스

```text
probe(node, catalog) -> availability + version + capabilities + reasons
plan(project_facts, policy) -> tasks + unsupported_scopes
prepare(task, environment) -> execution_spec
parse(raw_artifacts, exit_status) -> observations + parse_diagnostics
normalize(observations, source_map) -> normalized_observations
```

실제 프로세스 생성은 Executor가 한다. 어댑터가 독자적으로 무제한 subprocess를 시작하지 않는다. 외부 플러그인은 승인된 digest·버전을 가진 카탈로그에서만 로드한다. 초기에는 빌트인 어댑터 우선으로 구현하고 임의 다운로드 플러그인은 지원하지 않는다.

ExecutionSpec은 executable의 절대 경로, argv 배열, cwd, env allowlist, stdin 정책, 시간 제한, CPU/memory 예산, 출력 크기 제한, 예상 산출물, exit-code mapping을 갖는다. 각 tool version의 지원 CLI와 parser를 함께 버전 관리한다.

## 5. 원시 관찰

observation에는 ID, task/attempt, tool/version, rule ID, 원문 severity/message, 원시 출력 artifact+offset, 정규화 위치, 흐름 단계, engine profile, parser version, fingerprint를 저장한다. 원시 진단은 LLM이 수정하지 않는다. 마스킹이 필요한 비밀값은 저장 전 제거하고 redaction 위치를 남긴다.

SARIF를 쓰는 도구는 locations·relatedLocations·codeFlows·fingerprints·invocations를 읽어 공통 모델로 옮기되 원시 파일을 보존한다. SARIF를 지원하지 않아도 JSON/XML/text parser를 제공할 수 있다. SARIF는 외부 도구 어댑터 형식이며 제품 전체 보고서 모델을 대신하지 않는다.

## 6. 종료 코드와 파싱

도구별로 success_without_findings, success_with_findings, configuration_error, execution_error를 매핑한다. 예컨대 Ruff 어댑터의 0/1/2 처리는 고정 버전 문서와 fixture로 검증한다. timeout은 exit code와 별도로 기록한다. 출력이 잘렸거나 일부 파일 파싱에 실패하면 partial_parse=true와 affected scope를 남긴다.

파싱 실패를 빈 배열로 바꾸지 않는다. 해당 도구의 발견 0건과 검사 실패는 서로 다른 UI다. 경로가 snapshot에 없으면 generated artifact 매핑을 시도하고, 없으면 unresolved_location으로 격리한다.

## 7. 규칙 팩

기본 팩은 동작·오류·보안·성능 위험을 우선한다. 스타일/formatting은 낮은 우선순위 별도 그룹이다. all rules 일괄 활성화를 기본으로 삼지 않는다. 언어 버전·프로젝트 패턴별로 적용 가능한 팩을 고르고 각 팩은 ID·version·digest·규칙 목록·긍정/부정 fixture를 갖는다.

사용자가 규칙을 끄면 이유·범위·기간을 기록한다. 분석 도중 저장소 설정이 바뀌어도 고정된 정책 revision은 변하지 않는다. 검사기 설정을 그대로 존중할지 CODEYE 팩으로 추가 검사할지 계획에 표시하고, 정책 차이로 발견 수가 변한 것을 코드 회귀로 오해하지 않게 한다.

## 8. 분석 범위와 중복

파일 발견 수, 실제 파싱 수, 타입 해결 수, 도구가 처리한 단위, timeout/제외를 제공한다. 도구가 제공하지 않는 상세 범위는 unknown으로 남긴다. 여러 검사기의 중복은 rule family·symbol·normalized context·root-cause hypothesis로 묶되 자동 병합 근거를 저장한다. 두 진단이 같은 원인인지 불분명하면 관련 항목으로만 연결한다.

## 9. 어댑터 수용 조건

Go vet(W08) 구현과 실제 종료 코드 매핑은 [W08 기록](29-w08-go-vet.md)과 [Go vet 서술](../examples/analyzer-go-vet.yaml)에 있다. `-json` 모드는 진단이 있어도 exit 0이므로 발견 여부를 stdout parser가 결정한다. Staticcheck(W19)는 exit 0/1/2를 쓰지만 exit 1이 진단과 compile 오류에 함께 쓰여 stdout·stderr parser가 결정한다([서술](../examples/analyzer-staticcheck.yaml), [§8.5](39-r1-analyzer-packs.md#85-종료-코드와-결과)).

W16 이후 팩은 [분석기 팩 계약](39-r1-analyzer-packs.md)을 따른다. 도구마다 `pack.Analyzer` 하나, 에이전트 플래그 하나, 생태계마다 필수 기준 분석기 하나를 둔다. 공통 산출물은 [`analyzer_observations` envelope](../schemas/analyzer-observations.schema.json)다. 미설정 도구의 작업도 계획해 `unsupported`로 보인다.

정상/결함 fixture, findings exit, parse 실패, 비ASCII·공백 경로, timeout·취소, 버전 불일치, offline install 실패, 원본 미변경, raw→normalized 추적을 검증한다. CLI 호출 성공만으로 어댑터를 완료 처리하지 않는다. [Schema](../schemas/analyzer.schema.json)와 [예시](../examples/analyzer-ruff.yaml)를 함께 갱신한다.

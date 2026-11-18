# 45. W21 구조·중복·비밀값 팩: lizard, jscpd, gitleaks

W21은 [분석기 팩 계약](39-r1-analyzer-packs.md)에 언어와 무관한(`ecosystem: any`) 추가 깊이 분석기 세 개를 더한다. **lizard**(`any.lizard`)는 함수 복잡도, **jscpd**(`any.jscpd`)는 중복 블록, **gitleaks**(`any.gitleaks`)는 비밀값을 본다. 앞의 둘이 내는 것은 **구조 지표**다. 지표는 임계값을 넘은 측정값일 뿐이고 결함, finding, 결함 수, 조치 수, 모델 검토 입력이 되지 않는다. gitleaks의 탐지는 미검토(unreviewed) 낮음(low) 보안 finding이다. 비밀값 자체와 그 일부·hash는 어디에도 저장하지 않는다.

검증 범위: Linux(WSL2) x86-64에서 lizard 1.26.1(Python 3), jscpd 5.4.3(node v24.18.0의 `run-jscpd.js`와 그 플랫폼 바이너리), gitleaks 8.30.1을 실제로 실행했다. Windows는 교차 컴파일한 시험 바이너리를 WSL interop로 실행해 단위·재생(replay) 시험만 확인했다. 실도구 시험과 링크 시험은 Windows에서 건너뛴다(§11). CI에는 세 도구가 없다. 실도구 시험은 `CODEYE_TEST_LIZARD`·`CODEYE_TEST_JSCPD`·`CODEYE_TEST_NODE`·`CODEYE_TEST_GITLEAKS`가 있을 때만 실행한다. CI에서는 같은 판정 경로를 캡처한 실제 출력의 재생으로 검사한다.

## 1. 구성 요약

| 부분 | 위치 | 역할 |
|---|---|---|
| 공통 실행부 | `internal/worker/analysis/anypack` | 범위 루트, 파일 선택, 데이터 디렉터리 제외, 링크 거부, 위치 해석, 실행·결과 우선순위 |
| 시험 도구 | `internal/worker/analysis/anypack/anypacktest` | fixture, 캡처 기록·재생, 생성 비밀값과 부재 검사, 바깥 canary 링크(시험에서만 import) |
| 어댑터 | `internal/worker/analysis/{lizard,jscpd,gitleaks}` | 계획, 실행, parser, 판정(`assemble.go`) |
| 빌트인 연결 | `internal/worker/analysis/builtin/w21.go` | `--lizard`·`--jscpd`(+`--node`)·`--gitleaks`, 시작 probe, 카탈로그 항목(`any` 절은 Rust 다음) |
| 지표 계약 | `obsdoc.Metric`, `report.ObservationMetric`, `summary.metric_observations` | 지표 관찰의 envelope·보고서 표현(§4) |
| 모델 입력 선별 | `internal/worker/review/screen.go` | R1과 기본 review-v1의 비밀값 선별(§8) |
| 서술 | [lizard](../examples/analyzer-lizard.yaml) · [jscpd](../examples/analyzer-jscpd.yaml) · [gitleaks](../examples/analyzer-gitleaks.yaml) | 실행 계약(`executes_repository_code: false`) |
| 실제 산출물 | [lizard](../examples/observations-lizard.json) · [jscpd](../examples/observations-jscpd.json) · [gitleaks](../examples/observations-gitleaks.json) | 실도구 시험의 envelope |
| 캡처 | `internal/worker/analysis/{lizard,jscpd,gitleaks}/testdata/` | 고정 버전의 stdout·stderr·종료 코드 |

fixture 소스는 저장소에 두지 않고 시험 코드(`helpers_test.go`)가 시험마다 임시 디렉터리에 만든다. 비밀값을 시험 시점에 생성해야 하기 때문이다(§7.4). 그래서 `fixtures/w21-structure-secrets/`는 만들지 않았다.

## 2. 도구, 플래그, 환경

| 플래그 | 값 | 분석기 |
|---|---|---|
| `--lizard /abs/lizard` | 설치된 lizard 실행 파일 | lizard |
| `--jscpd /abs/node_modules/jscpd/run-jscpd.js` + `--node /abs/node` | jscpd 진입 스크립트와 공유 node toolchain([docs/39 §2.4.1](39-r1-analyzer-packs.md)) | jscpd |
| `--gitleaks /abs/gitleaks` | 설치된 gitleaks 실행 파일 | gitleaks |

- 세 분석기 모두 추가 깊이다(`required: false`). 기준 분석기가 아니므로 이 플래그만으로는 노드가 deep-review를 광고하지 않는다. 경로가 없으면 작업은 계획되고 `unsupported`(`tool_not_configured`)로 끝난다. 처리 파일은 0으로 알려지고 통과로 세지 않는다.
- 도구는 PATH에서 찾지 않는다. jscpd는 `<node> <run-jscpd.js> args`로 실행하고 PATH에는 node 디렉터리만 둔다. npm이 만드는 셸 shim(`node_modules/.bin/jscpd`)은 거부한다. node toolchain이 없으면 설정 오류다(`TestNewRejectsShims`).
- 환경은 상속하지 않는다. 공통값은 `PATH`(도구 디렉터리), `LANG=C.UTF-8`, executor가 정하는 `HOME`·`TMPDIR`·`TMP`·`TEMP`·`USERPROFILE`(task 임시 디렉터리)이다. lizard는 `PYTHONDONTWRITEBYTECODE=1`·`PYTHONNOUSERSITE=1`·`PYTHONIOENCODING=utf-8`, jscpd는 `JSCPD_NO_TIPS=1`·`JSCPD_CACHE_DIR`·`XDG_CACHE_HOME`(scratch)을 더 받는다. 기록된 명령의 노드 경로는 `[REDACTED]`로 가린다.
- 네트워크를 쓰지 않는다. jscpd의 semantic 모드(모델 내려받기)는 켜지 않는다.
- **저장소 코드 실행(`RepositoryCode`)은 세 작업 모두 `false`다.** lizard는 CODEYE가 넘긴 파일을 읽어 구문만 센다. jscpd는 읽은 토큰을 비교한다. gitleaks는 파일 바이트를 정규식으로 검사한다. 셋 다 저장소의 설정 프로그램·plugin·build script를 실행하지 않는다. 저장소 설정 파일은 CODEYE 설정으로 대체하거나 검증 뒤 따른다(§5).

## 3. 계획과 입력 파일

- 작업은 분석 하나당 하나다(`lizard`, `jscpd`, `gitleaks`). 범위(scope)의 루트에 소스가 있으면 언어와 관계없이 계획한다. 그래서 TC-69의 Python 전용 프로젝트에도 세 작업이 보인다.
- lizard와 jscpd는 CODEYE 주요 언어의 소스 파일만 받는다(C/C++, Python, JS/TS, Go, Rust. lizard는 1.26의 reader가 있는 확장자만 받는다). 숨김 디렉터리, `testdata`, `vendor`, `node_modules` 아래 파일은 넘기지 않는다. 대신 `excluded_data_directory` 단위로 세고 `target.properties.excluded_directories`와 제약 `data_directories_excluded`로 남긴다. 생성·외부 코드의 복잡도와 중복은 프로젝트 유지보수 지표를 흐리기 때문이다.
- gitleaks는 범위의 모든 포함 파일(설정·문서·데이터 포함)을 대상으로 한다. 무엇을 건너뛸지는 gitleaks 기본 전역 allowlist가 정한다(§5.3).
- 입력은 스냅샷에서 만든 attempt 작업 사본이다. 원본 경로에서 실행하지 않는다. 스냅샷은 심볼릭 링크를 복사하지 않는다(`link_not_followed`).

## 4. 지표는 결함이 아니다

### 4.1 계약

envelope 관찰에 선택 필드 `metric{name, value, threshold, unit}`을 더했다(가산 변경). `unit`은 `count`·`lines`·`tokens` 중 하나이고 `value >= threshold`여야 한다. 지표 관찰은 suppressed일 수 없다.

- `obsdoc.Document.Active()`는 지표 관찰을 세지 않는다. 지표만 있는 성공은 `success_without_findings`다. 지표만 있는데 `success_with_findings`를 주장하는 문서는 무효다(`TestMetricObservations`).
- 보고서 decoder는 지표 관찰을 finding으로 만들지 않는다. 지표 관찰을 `observation_ids`로 인용한 finding이 있는 보고서는 무효다(`TestW21ValidateRejectsMetricFinding`).
- `summary.metric_observations`는 `raw_observations` 중 지표 관찰 수다. 0이면 생략한다. `raw_observations`·`unresolved_observations`의 뜻은 바꾸지 않았다. 지표도 원시 관찰이다.
- Markdown·HTML은 원시 관찰 수 아래에 "그중 구조 지표(결함 아님)" 행을 0보다 클 때만 넣는다. 지표 관찰에는 "구조 지표, 결함 아님" 표지를 붙인다. 웹 보고서도 같다. 웹 parser는 요약의 수와 metric을 가진 관찰 수가 다르면 보고서를 거부한다.
- 모델 입력: R1 해석 역할의 관찰 창은 지표 관찰을 읽지 않는다(`readToolDoc`).
- 판정: 지표는 `attention`을 만들지 않는다. 위치를 확인하지 못한 지표(예: `source_drift`)는 다른 관찰처럼 미해결로 세어 판정을 `inconclusive`로 둔다(`TestW21UnresolvedMetricIsInconclusive`, A28). 근거가 스냅샷과 맞는지 확인하지 못한 결과는 측정값이라도 깨끗한 판정의 근거가 될 수 없다.

### 4.2 임계값(규칙 팩 v1)

| 분석기 | 지표 | 임계값 | 근거 |
|---|---|---|---|
| lizard | `cyclomatic_complexity`(count) | CCN ≥ 16 | lizard 기본 경고 "CCN > 15"와 같다 |
| jscpd | `duplicated_lines`(lines) | minLines 10, minTokens 100, mode `mild` | jscpd 기본값(5줄·50토큰)은 짧은 반복까지 잡아 지표가 흐려진다 |
| jscpd | 파일 크기 | 1,000,000바이트 이하, UTF-8 | jscpd가 조용히 건너뛰는 파일(실측)은 넘기지 않고 `skipped_too_large`·`skipped_not_utf8`로 센다 |

임계값은 유지보수 신호의 기준일 뿐이다. 넘었다고 결함이라는 뜻이 아니고, 못 넘었다고 구조가 좋다는 뜻도 아니다.

## 5. 도구별 실행, 규칙 문맥, 종료 코드

### 5.1 lizard

- argv: `--xml --no-gitignore --input_file <scratch>/analyzers/lizard/files.txt`. 저장소 경로는 argv에 들어가지 않는다. 파일 목록은 scratch에 쓴다.
- `--xml`(cppncss 형식)은 함수 이름, NCSS, CCN과 분석한 모든 파일을 준다. CSV는 쓰지 않는다. CSV의 `long_name`은 매개변수 목록을 기본값까지 복사하므로, 기본값에 넣은 비밀값이 stream에 남는다. XML의 항목 이름은 `fn(...) at file:line`이다(실측).
- `-Eduplicate` 등 확장은 켜지 않는다. 중복은 jscpd가 맡는다(§6).
- 실측(1.26.1): XML 모드는 자체 임계값과 관계없이 exit 0이다. 그래서 CODEYE가 CCN을 보고 임계값을 적용한다. Latin-1 파일도 해독해 측정한다(`TestRealLizardReadsNonUTF8`). 소스 안 `#lizard forgive` 주석과 `GENERATED CODE` 표지는 함수를 기록 없이 결과에서 숨긴다. `whitelizard.txt`는 경고 출력만 거르며 XML에는 영향이 없다.
- 규칙 문맥: `repository_config=overridden`(설정 파일을 읽지 않음, `config_files=[]`), `source_suppressions=honored`(제약 `in_source_suppressions_honored`). 함수의 끝 줄은 기록하지 않는다(`function_end_lines_not_recorded`).

| 종료/출력 | 결과 |
|---|---|
| exit 0, XML 완전, 넘긴 파일 수 = 분석 파일 수 | 성공. 지표가 있어도 `success_without_findings` |
| exit 0이지만 XML 손상·모양 오류·파일 수 불일치 | `failed`/`output_unparsed` |
| exit ≠ 0 | `failed`/`lizard_failed` |
| 출력 잘림 | `failed`/`output_truncated` |
| 읽을 파일 없음 | `unsupported`/`no_analyzable_files` |

### 5.2 jscpd

- argv: `<node> <run-jscpd.js> --config <scratch>/analyzers/jscpd/jscpd.json`. 설정의 `path`에 넘길 파일의 절대 경로를 적고 `output`은 scratch로 둔다. `reporters=[xcode]`, `absolute`, `noGitignore`, `noTips`, `noColors`를 켠다.
- `--config`를 주면 저장소의 `.jscpd.json`, `.config/jscpd.json`, `package.json`의 `"jscpd"` 키를 읽지 않는다(실측). 그래서 저장소 설정의 `path`·`output`·`reporters`가 입력이나 쓰기 위치를 바꿀 수 없다. 규칙 문맥은 `repository_config=overridden`, `config_files=[]`다. 소스 안 `jscpd:ignore-start`/`jscpd:ignore-end` 표지는 블록을 기록 없이 숨긴다(실측). 그래서 `source_suppressions=honored`이고, 사라진 지표를 개선으로 읽지 않는다.
- 실측(5.4.3): 중복이 있든 없든 exit 0이다(`--exit-code`·threshold를 주지 않음). xcode 출력 `…/lib/b.js:1:0: warning: Found 16 lines (1-17) duplicated on file …/src/a.js (1-17)`에서 N은 end − start다. 지표 값은 이 N이고 N < minLines는 모양 오류다. 열 위치는 기록하지 않는다(`clone_columns_not_recorded`). 실행 시간 줄은 100초 실행에서도 `time: 100.32s`처럼 초 단위였다(`TestReplayLongRunTiming`).
- 처리 파일은 CODEYE가 넘긴 목록이다(`processed_paths_from_input_list`). jscpd는 파일별 처리를 보고하지 않는다. 임계값에 닿는 파일이 없으면 jscpd는 stderr에 "analyzed no files"를 쓰고, CODEYE는 이를 제약 `no_file_reached_clone_thresholds`로 남긴다.

| 종료/출력 | 결과 |
|---|---|
| exit 0, 레코드 수 = `Found N clones.` | 성공. 지표가 있어도 `success_without_findings` |
| 알 수 없는 stdout 줄, 요약 불일치 | `failed`/`output_unparsed` |
| 알 수 없는 stderr | `failed`/`jscpd_failed`(도구 오류 `tool_stderr`) |
| exit ≠ 0 | `failed`/`jscpd_failed` |
| 비교할 파일 없음 | `unsupported`/`no_analyzable_files` |

### 5.3 gitleaks

- argv(범위 루트마다 한 번): `dir --no-banner --no-color --log-level info --redact=100 --config <scratch>/codeye-gitleaks.toml --gitleaks-ignore-path <scratch>/ignore --ignore-gitleaks-allow --max-archive-depth 0 --max-decode-depth 5 --exit-code 3 --report-format template --report-template <scratch>/report.tmpl --report-path - -- ./<root>`.
- CODEYE 설정은 기본 규칙 확장(`[extend] useDefault = true`)이다. `--config`를 주면 저장소 `.gitleaks.toml`을 읽지 않는다(실측: fixture의 `.gitleaks.toml` allowlist가 가리킨 `src/app.py`도 탐지됨).
- 인라인 `gitleaks:allow`는 무시한다(`--ignore-gitleaks-allow`). 저장소가 주석 한 줄로 탐지를 끌 수 없다. 규칙 문맥은 `source_suppressions=disabled`다.
- **`.gitleaksignore`는 따른다.** gitleaks는 검사 루트의 `.gitleaksignore`를 `--gitleaks-ignore-path`와 관계없이 항상 읽는다(실측). 끌 수 없으므로 `repository_config=honored`로 기록하고, 그 파일을 `config_files`에 SHA-256과 함께 넣는다. 항목이 사본 밖을 가리키면(절대 경로, `..`, NUL) 작업을 거부한다(§9.1).
- 기본 전역 allowlist(`gitleaks.toml`, 이미지·글꼴·문서 확장자, `go.mod`·`go.sum`, `vendor/modules.txt`·일부 vendor 경로, `node_modules`, lock 파일 등)에 걸린 파일은 gitleaks가 읽지 않는다. CODEYE는 같은 목록을 복사해 그 파일을 `processed_paths`에서 뺀다(`skipped_default_allowlist`).
- 이진 파일도 검사한다(실측: `assets/blob.dat`에서 탐지). 다만 UTF-8 텍스트가 아닌 파일은 처리 파일로 주장하지 않는다(`binary_files_not_claimed_processed`). 압축 파일은 풀지 않는다(depth 0). 인코딩된 값은 5단계까지 해독한다.
- git 이력은 검사하지 않는다(`history_not_scanned`). 스냅샷에는 이력이 없다.
- 열 위치는 버린다(`secret_columns_not_recorded`). gitleaks 8.30.1은 파일 조각의 첫 줄 뒤에서 시작·끝 열을 모두 1씩 크게 보고한다(캡처로 측정, `TestCapturedColumnsAreOffByOne`). 조각 경계는 도구의 chunk 읽기에 달려 있어 보정할 수 없다. 줄 범위는 정확하다.

| 종료/로그 | 결과 |
|---|---|
| exit 0, 로그 "no leaks found", 레코드 0, 종료 레코드 `findings:0` | 성공(`success_without_findings`) |
| exit 3, 로그 "leaks found: N" = 레코드 수 N > 0 | 성공(`success_with_findings`) |
| exit 1, 또는 건너뛴 경로·오류 로그 | `failed`/`scan_incomplete`(루트별 도구 오류) |
| 종료 코드·로그·레코드 수 불일치 | `failed`/`exit_status_mismatch` |
| 템플릿 레코드 손상, 종료 레코드 없음 | `failed`/`output_unparsed` |
| 선택된 스냅샷 파일이 아닌 결과 | 그 레코드를 버리고 `failed`/`result_outside_snapshot` |
| 그 밖의 종료 코드 | `failed`/`gitleaks_failed` |

## 6. 엔진 중복

- 복잡도는 lizard만 잰다. lizard의 `-Eduplicate` 확장은 켜지 않는다. jscpd의 complexity·dead-code·semantic·history 모드도 켜지 않는다. `validate_bundle`이 lizard 서술 argv에 `-E`가 없음을 확인한다.
- 중복은 jscpd만 잰다.
- 비밀값 엔진은 gitleaks다. Python 팩의 ruff `S105`·bandit `B105`(하드코딩 암호 문자열)가 같은 줄을 보고하더라도 합치지 않는다. finding의 묶음 키가 분석기를 포함하므로 도구별 finding으로 남는다(TC-102와 같은 규칙). 두 엔진은 근거가 다르다. gitleaks는 알려진 자격증명 형식과 entropy를, S105·B105는 변수 이름을 본다. 한쪽이 다른 쪽을 대신한다고 보지 않는다.

## 7. 비밀값 처리

### 7.1 비밀값이 나타나면 안 되는 곳

다음 어디에도 원래 비밀값, 그 일부, 소문자형, hex는 없어야 한다.

- executor가 저장하는 stdout·stderr 산출물
- envelope과 보고서
- 전체·코드 제외 내보내기(JSON, HTML, Markdown)
- DB 행, 로그, 웹 UI
- CODEYE가 이 결과로 만드는 모델 입력

### 7.2 방법

- gitleaks는 CODEYE 보고 템플릿으로만 출력한다. 템플릿은 규칙, 파일, 심볼릭 링크 표시, 줄·열, entropy만 읽는다. `.Secret`·`.Match`·`.Line`·`.Fingerprint`·커밋·작성자는 읽지 않는다(`validate_bundle`이 템플릿 상수를 검사). `--redact=100`은 로그도 가린다. 로그 수준은 info로 고정한다. debug·trace는 원문과 해독값을 찍는다. `-v`는 주지 않는다.
- 관찰 문구는 고정 템플릿 `secret detected by gitleaks rule <rule> (value withheld)`다. entropy와 위치도 넣지 않는다.
- 식별(fp2)은 `report.ToolFingerprint(analyzer, rule, path, "", 고정 문구)`다. 비밀값의 plain hash를 쓰지 않는다. 같은 파일에서 같은 규칙이 두 번 탐지되면 fingerprint가 같은 finding 두 개가 된다. 비교(W23)는 이를 ambiguous로 보고 사람 판정을 이월하지 않는다(`TestW21SameRuleTwiceInOneFile`).
- 코드 발췌: controller는 스냅샷 원문을 보관하지 않으므로 보고서에 코드 발췌가 없다. 전체 내보내기에도 비밀값을 담은 줄이 들어가지 않는다.
- lizard 산출물은 함수 이름·줄·지표만 담고, jscpd 산출물은 경로·줄 범위만 담는다. 실도구 시험이 두 도구의 stream에 소스 조각이 없음을 확인한다(`AssertNoSnippet`).

### 7.3 location의 `blob_sha256`

비밀값 finding의 위치에도 파일 전체의 SHA-256이 있다. 이것은 비밀값의 hash가 아니다. 같은 값은 스냅샷 manifest에 모든 파일에 대해 이미 있다. 다만 짧은 비밀값 한 줄만 담은 파일은 내용을 추측해 이 hash와 맞춰 볼 수 있다. 코드 제외 내보내기에서도 이 값을 지우지 않았다. 지우더라도 manifest에는 남기 때문이다. 이는 남은 제한이다(§12).

### 7.4 시험

비밀값은 시험 시점에 `anypacktest.Secret`이 만든다(`AKIA` + 임의 base32 16자). 저장소에는 실제와 비슷한 토큰을 두지 않는다. 재생 시험은 같은 길이의 자리 값(`placeholder-not-key0`)으로 캡처한 줄·열 범위를 맞춘다. 캡처 함수는 노드 경로나 생성 비밀값이 든 출력을 쓰기를 거부한다.

- 어댑터: 비밀값과 형태가 다른 표기가 envelope, scratch, 결과(stream 포함)에 없다(`TestRealGitleaksFindsWithoutValues` 등).
- 워커 종단: `TestW21PackWithRealTools`(TC-119)가 실제 세 도구를 제품 composite와 워커로 돌린다. 업로드한 산출물, 프로토콜 메시지, scratch, 전체·코드 제외 JSON과 HTML·Markdown 렌더링에 비밀값이 없음을 확인한다.
- 노드·controller·DB 종단: `scripts/node_smoke.py`의 `w21_secrets`(네 도구 변수가 있을 때만)가 실제 agent·controller·PostgreSQL로 돌린다. 산출물 디렉터리의 모든 파일, 게시된 보고서, 내보내기 네 가지, controller·agent 로그, `pg_dump --data-only`로 얻은 DB 행, 노드 상태 디렉터리(스냅샷·작업 사본을 담은 `data/` 제외)에서 비밀값이 없음을 확인한다.

## 8. 모델 입력의 비밀값 선별

검토 역할이 모델 입력을 만들 때 비밀값 작업의 결과로 파일을 거른다. R1(`prompts/review-r1`)과 기본 팩 review-v1 모두 같은 규칙이다(`screen.go`).

| 비밀값 작업 상태 | 동작 |
|---|---|
| **실행 안 됨**: 계획에 없음, 또는 `unsupported`이면서 `not_run`(도구 미설정 등) | 거르지 않고 보낸다. 검토 기록과 보고서에 제약 `model_input_not_secret_screened`를 남긴다. 모델 입력 바이트에는 넣지 않는다. |
| **실행했으나 쓸 수 없음**: 실패, 시간 초과, 취소, 부분 파싱, envelope 없음·무효 | 모든 검토 역할을 `skipped_policy`/`secret_screen_failed`로 건너뛴다. 모델 요청을 보내지 않는다. 거르지 않은 코드로 대신 보내지 않는다. |
| **완전한 성공**(`succeeded`, 부분 파싱 아님, parse OK) | 탐지된 파일을 모든 발췌에서 뺀다. 대상은 코드 문맥, 문서, 관찰 창, 검증 확장, v1 corpus다. 제약 `secret_files_withheld`를 남긴다. |

- 순서: 워커는 계획 순서대로 작업을 한 번에 하나씩 실행하고, 검토 역할은 정적 계획 뒤에 붙는다. 그래서 검토가 envelope을 읽을 때 비밀값 작업은 이미 끝나 있다. 계획에 있는데 envelope이 없으면 "실행 안 됨"이 아니라 실패로 본다. 경쟁 상태가 생겨도 열린 쪽으로 처리되지 않는다.
- review-v1의 고정 요청 바이트는 바뀌지 않는다(`TestReviewV1IsPinned`). 선별 결과는 corpus에서 파일을 빼는 방식으로만 반영한다. 제약은 기록에만 들어간다. R1 예시(`examples/review-r1/`)는 기록의 제약과 상위 기록 SHA만 바뀌었다. 요청 SHA는 그대로다.
- 시험: `TestR1WithholdsSecretFiles`, `TestR1SecretScreenCases`(실행 안 됨·실패·envelope 없음), `TestV1SecretScreen`(성공·실행 안 됨·실패·없음).

## 9. 보안: 적대적 저장소

대상 저장소의 설정과 내용은 적대적이라고 본다.

### 9.1 저장소 설정

- lizard: 저장소 설정을 읽지 않는다. 입력은 CODEYE 파일 목록이고 `--no-gitignore`로 `.gitignore`도 무시한다. `whitelizard.txt`는 XML 결과를 바꾸지 않는다(실측).
- jscpd: CODEYE `--config`가 저장소 설정을 통째로 대신한다(실측). 쓰기 위치(`output`)는 scratch다. 보고기는 stdout에 쓰는 xcode 하나다. 저장소 설정의 `path`·`output`·`reporters`는 읽히지 않는다.
- gitleaks: `.gitleaks.toml`은 읽지 않는다(`--config`). 사본 밖 `[extend] path`도 따라갈 수 없다. 끌 수 없는 `.gitleaksignore`는 스냅샷 사본(manifest SHA-256에 묶임)에서 미리 읽어 검사한다. ':'로 나눈 필드 중 하나라도 `/`·`\`로 시작하거나, `..` 구간이 있거나, NUL이 있으면 gitleaks를 시작하지 않는다. 그때 결과는 `failed`/`repository_config_rejected`이고 옵션 이름을 담은 제약 `rejected_option_gitleaksignore`를 남긴다. 항목 값은 기록하지 않는다(`TestReplayRefusesHostileIgnoreFile`, `TestIgnoreFileEntries`).

### 9.2 링크

- 스냅샷은 심볼릭 링크를 복사하지 않는다.
- 작업 사본에 링크나 특수 파일이 생기면 도구를 시작하지 않는다. lizard·jscpd는 넘길 파일과 그 조상 디렉터리를, gitleaks는 검사 루트 아래 트리 전체를 확인한다. 결과는 `failed`/`workspace_link_refused`다.
- gitleaks의 `--follow-symlinks`는 주지 않는다(기본 꺼짐). jscpd의 `followSymlinks`도 기본값(꺼짐) 그대로다. lizard는 목록의 파일만 연다.
- 시험(`TestReal*HostileNamesAndLinks`): 작업 사본 밖 임시 파일에 고유 canary(생성 비밀값)를 두고 그 파일과 디렉터리를 가리키는 링크를 소스에 둔다. canary가 어떤 출력에도 없음을 확인한다. `TestReplayRefusesWorkspaceLink`는 작업 사본에 직접 만든 링크에 대해 거부를 확인한다.

### 9.3 사본 밖 결과

선택된 스냅샷 파일이 아닌 경로의 결과는 버린다. 그 경로와 문구는 저장하지 않는다. 작업은 `result_outside_snapshot`으로 실패한다. 도구 오류의 문구는 고정이고 건수만 담는다(`TestReplayDropsResultsOutsideSnapshot`). lizard와 jscpd는 목록에 없는 파일의 결과를 모양 오류로 본다.

### 9.4 argv 주입

저장소에서 온 경로는 옵션으로 읽힐 수 있는 위치에 두지 않는다.

- lizard·jscpd: 저장소 경로가 argv에 아예 없다. 파일 목록은 scratch의 목록 파일·설정 파일에 쓴다.
- gitleaks: 범위 루트를 `--` 뒤에 `.` 또는 `./<root>`로 넘긴다.
- 시험: `--help`·`-x`라는 이름의 디렉터리와 파일이 있는 fixture를 전체 범위와 경로 범위로 돌린다(`TestReal*HostileNamesAndLinks`).

### 9.5 산출물 stream

세 도구의 실제 stdout·stderr를 검사했다. 소스 조각과 비밀값이 없다(§7). stream에 남는 저장소 유래 문자열은 경로, 줄 번호, 규칙 이름, lizard의 함수 이름, entropy다. 절대 경로는 executor가 `[REDACTED]`로 가린다.

### 9.6 모델 입력 선별의 실패 시 동작

이전 구현은 envelope이 없거나 무효이면 빈 선별 목록으로 진행했다. 실패할 때 열리는 동작이었다. 지금은 §8의 세 경우로 나눈다. 쓸 수 없는 결과는 검토를 건너뛴다. 기본 팩 review-v1에도 같은 규칙을 적용했고 고정 요청 시험은 그대로 통과한다.

## 10. 계약 변경(가산)

- `schemas/analyzer-observations.schema.json`: 관찰의 선택 필드 `metric`.
- `schemas/report.schema.json`: 관찰의 선택 필드 `metric`, `summary.metric_observations`(1 이상, 0이면 생략).
- `obsdoc.Metric`, `obsdoc.MetricUnits`, `obsdoc.SecretAnalyzers`. `Active()`는 지표를 세지 않는다.
- 보고서 builder 버전은 "4" 그대로다. 지표가 없는 보고서는 바이트가 같고, 새 필드는 생략 가능한 가산 필드다(A28).
- 웹 parser가 `metric_observations`와 관찰 `metric`을 읽는다.
- `api/openapi.yaml`은 보고서 schema를 참조하므로 따로 바꾸지 않았다.
- W19·W20·W23 시험은 그대로 통과한다. 카탈로그 기대 목록, 누락 사유 목록, 플래그 목록, TC-69의 Python 전용 계획, `node_smoke`의 Go run 기대값(작업 수 5, `unsupported` 3·4)에는 W21 항목을 더했다.

## 11. 시험과 실행 근거

캡처 명령(각 패키지의 `testdata/`에 stdout·stderr·종료 코드를 쓴다):

```text
CODEYE_TEST_LIZARD=/abs/lizard \
CODEYE_W21_CAPTURE=$PWD/internal/worker/analysis/lizard/testdata \
go test -count=1 -run TestReal ./internal/worker/analysis/lizard/

CODEYE_TEST_JSCPD=/abs/node_modules/jscpd/run-jscpd.js CODEYE_TEST_NODE=/abs/node \
CODEYE_W21_CAPTURE=$PWD/internal/worker/analysis/jscpd/testdata \
go test -count=1 -run TestReal ./internal/worker/analysis/jscpd/

CODEYE_TEST_GITLEAKS=/abs/gitleaks \
CODEYE_W21_CAPTURE=$PWD/internal/worker/analysis/gitleaks/testdata \
go test -count=1 -run TestReal ./internal/worker/analysis/gitleaks/
```

실제 산출물 예시는 같은 실행에 `CODEYE_W21_DUMP=<dir>`를 주어 얻은 `TestRealLizardMetrics`·`TestRealJscpdClones`·`TestRealGitleaksFindsWithoutValues`의 envelope다. gitleaks 캡처는 자리 값으로 만든 fixture에서 얻었다. 생성 비밀값으로 돌린 실행의 출력은 캡처 함수가 쓰지 않는다.

| 시험 | 범위 |
|---|---|
| `lizard.TestReal*`·`jscpd.TestReal*`·`gitleaks.TestReal*` (도구 필요) | 실제 출력, 임계값, 제외 디렉터리, 처리 파일, 규칙 문맥, 비UTF-8, 범위 루트, 깨끗한 결과(TC-117·118) |
| `*.TestReplay*` (CI) | 캡처 재생: 위 판정, 실패·잘림·손상·버전 계약, 긴 실행 시간 줄, 열 어긋남 기록 |
| `*.TestReal*HostileNamesAndLinks`, `*.TestReplayRefusesWorkspaceLink`, `gitleaks.TestReplayRefusesHostileIgnoreFile`, `TestReplayDropsResultsOutsideSnapshot`, `TestIgnoreFileEntries` | §9의 적대적 저장소 |
| `anypack.Test*` | 범위 루트, 결과 우선순위, 지표가 finding 수에 들어가지 않음, 파일 선택과 제외 수 |
| `obsdoc.TestMetricObservations` | 지표 관찰의 모양·임계값·단위, 지표만 있는 성공, 비밀값 분석기 판별 |
| `report.TestW21*` | 지표 비finding, 렌더링, 지표를 인용한 finding 거부, 같은 파일의 같은 규칙, 위치 못 찾은 지표 |
| `review.TestR1WithholdsSecretFiles`, `TestR1SecretScreenCases`, `TestV1SecretScreen`, `TestReviewV1IsPinned` | §8 |
| `run.TestW21PackWithRealTools` (Linux, 도구 필요, TC-119) | 실제 워커 종단과 비밀값 부재 |
| `builtin.TestW21*`, `main.TestWorkerHandlers*` | 카탈로그·플래그·probe·빌드 사유 |
| `scripts/validate_bundle.py` (`check_w21_contracts`) | 서술 yaml·envelope schema, 기록된 argv와 서술 일치, 규칙 팩 상수, 템플릿 필드, 13개 거부 사례 |
| `scripts/node_smoke.py` (`w21_secrets`, 도구 변수 필요) | §7.4 노드·controller·DB 종단 |
| 웹 `ReportPanel.test.tsx` | 지표 행·표지, 요약 수 불일치 거부 |

Windows: 바뀐 패키지(`cmd/codeye`, `report`, `anypack`, `builtin`, `gitleaks`, `jscpd`, `lizard`, `obsdoc`, `review`, `run`)를 `GOOS=windows`로 교차 컴파일했다. 그 바이너리를 WSL interop로 패키지 디렉터리에서 실행했고 모두 통과했다. 실도구 시험, 링크 시험(Windows에서 링크 생성 권한 필요), 일부 기존 시험은 건너뛴다.

## 12. 남은 제한

- **allowlist 파일은 선별되지 않는다.** gitleaks 기본 allowlist(`go.mod`·`go.sum`, lock 파일, `vendor/` 일부 등)에 걸린 파일은 검사하지 않는다. 그 파일에 든 비밀값은 탐지되지 않고 모델 입력에서도 빠지지 않는다. 플랫폼의 일반 민감값 마스킹만 적용된다. `.gitleaksignore`가 숨긴 탐지도 같다. 저장소가 명시적으로 받아들인 값으로 본다.
- git 이력은 검사하지 않는다. 지운 비밀값은 찾지 못한다.
- 비밀값 위치는 줄 범위뿐이다(§5.3). 위치의 파일 SHA-256은 짧은 비밀 파일의 추측 대조에 쓰일 수 있다(§7.3).
- 이진·압축 파일 안의 비밀값: 이진은 검사하지만 처리 파일로 주장하지 않는다. 압축은 풀지 않는다.
- lizard 산출물과 보고서 관찰에는 함수 이름이 남는다. 함수 이름 자체가 민감한 저장소는 코드 제외 내보내기를 써야 한다. 코드 제외 내보내기는 관찰 문구를 비운다.
- 지표 임계값은 고정이다(규칙 팩 v1). 프로젝트별 조정 설정은 없다. 데이터 디렉터리 제외 목록도 고정이다.
- jscpd는 파일별 처리를 보고하지 않아, 처리 파일은 넘긴 목록이다. 1,000,000바이트 초과·비UTF-8 소스는 비교하지 않는다.
- lizard는 함수 끝 줄을 기록하지 않는다. 지표 위치는 시작 줄 하나다.
- Windows와 macOS에서 실제 도구를 실행하지 않았다. 링크 거부의 Windows 동작(재분석 지점·junction)은 측정하지 않았다.
- 보고서 builder 버전을 올리지 않았으므로, 이전 builder로 만든 보고서와 지표 표시 유무만으로 구분할 수 없다(A28).

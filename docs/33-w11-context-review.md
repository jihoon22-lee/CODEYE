# W11 맥락과 독립 코드 검토

관리자가 허용한 고정 입력에 대해 Go vet의 검사 뒤 두 개의 필수 task를 실행한다. REQ-004/005/009/027, TC-05/10/16/33의 R0 범위다.

- `review.context`는 자료의 종류와 근거 ID를 붙인 사실·추론을 반환한다. 문서가 없는 경우에도 소스에서 시작한다.
- `review.investigate`는 발생 조건, 영향, 개선 방향, 반례 가능성과 실제 코드 근거를 가진 후보를 반환한다. 후보가 없으면 빈 배열을 명시한다. 누락된 배열, 알 수 없는 필드, 존재하지 않는 ID 또는 문서만 인용한 코드 후보는 거부한다.
- 플랫폼에 포함한 core·역할·영역·Go 지침과 manifest의 선택 목록 및 SHA-256, 모델·CLI 버전, 요청 digest, 제공된 사용량을 기록한다. 대상 README와 AGENTS는 자료로 전달하며 모델 권한을 설정할 수 없다.
- 한 요청의 입력 범위는 선택된 파일 최대 6개, 파일당 최대 50줄, 발췌 합계 약 12KB다. 제한된 scope, 줄 번호, blob SHA-256을 확인한다. 비텍스트, 민감값 패턴에 걸린 파일과 예산 밖의 자료를 제외한다. 이 방식은 완전한 비밀 탐지를 보장하지 않는다. 미분석 파일과 문서 부재를 한계로 표시한다.
- 노드 운영자는 일반 소스 매핑과 별도로 `--model-source-root ROOT_ID`로 모델 전송을 허용한다. 실제 모델을 쓰려면 agent에 `--opencode ABSOLUTE_PATH --model PROVIDER/MODEL`을 준다. 예산 relay를 선택한 경우에만 `--model-relay-file ABSOLUTE_PATH`를 추가한다. W15 후보 팩은 `--review-pack review-r1`로만 선택했고 기본값은 `review-v1`이었다([W15 2단계](37-w15-discovery-and-progress.md#10-결과--2단계후보-팩-연결-실모델-없음)). 2.0.0부터 기본값은 `review-r1`이고, v1은 `--review-pack review-v1`로 고르는 이전 팩이다([docs/49](49-review-pack-default.md)). relay에만 실제 OpenAI 키가 있고, 관리형 OpenCode는 loopback 전용 접근 값을 사용한다. Git 소스에 대한 개별 모델 전송 승인은 후속이다.
- 미설정, 모델 오류, 비허용 root를 구조화 상태와 reason에 남긴다. 도구 관찰과 모델 후보는 서로 다른 산출물이다. 모델이 서술한 숫자를 run 상태 또는 통계로 쓰지 않는다.

## 실행 근거

2026-11-18 Ubuntu 26.04.1 / WSL2에서 OpenCode 1.19.5와 예산 relay의 GPT‑6.2 Sol로 `TestLiveQualityRoles`를 실행했다. 마지막 sample을 합산하지 않는 동일한 Go fixture를 문서 있음·없음 두 번 검토했다.

| TC | 시험 | 관측 결과 |
|---|---|---|
| TC-85 | 문서 없이 Context → Investigator | 실제 모델 응답에서 사실 1개와 후보 1개를 받았다. 소스 근거 ID 확인 후 저장했다. |
| TC-86 | README가 있는 같은 코드, 별도 비밀값 fixture | 사실 2개와 후보 1개를 받았다. 허용한 발췌와 참조만 처리했고 별도 비밀값 fixture는 제외했다. |

검토 파일 선택·scope, 고정 사본 변경 거부, 문서 근거 요구, 미등록 ID 거부, 모델 미설정·root 미승인 경로도 단위 시험으로 확인했다. 두 표본의 후보 수는 탐지율이나 오탐률 추정치가 아니다. 일반 CI에서는 실제 유료 모델을 호출하지 않는다.

## 다섯 언어 코퍼스(W24 2c)

W24 2c부터 세 역할(`review.context`·`review.investigate`·`review.verify`)의 코퍼스 후보에 C/C++·Python·JS/TS·Rust 파일이 들어간다. 그 전에는 Go 파일만 코드로 보았으므로 다른 언어 스냅샷의 코퍼스에는 README·AGENTS만 있었다. 예산(파일 6개, 파일당 50줄, 코드 파일당 3,200바이트, 합계 약 12KB)과 읽기·blob 대조·민감값 패턴·W21 비밀값 선별은 바꾸지 않았다. 순위가 낮은 것부터 고르고, 같은 순위는 디렉터리 깊이와 경로 순이다. 발췌는 파일 첫 줄부터다.

| 순위 | 종류 | 파일 |
|---|---|---|
| 0 | code | 진입점. Go `main.go`, Python `__main__`·`main`·`app`·`cli`·`manage`(`.py`), JS/TS `main`·`app`·`server`·`cli`(소스 확장자)와 모듈 루트(가장 깊은 package.json·tsconfig.json 디렉터리) 또는 그 `src`의 `index`, Rust `main.rs`·`lib.rs`, C/C++ `main`(`.c`·`.cc`·`.cpp`·`.cxx`·`.c++`) |
| 1 | document | README.md, AGENTS.md(최대 2개) |
| 2 | code | 그 밖의 소스. 확장자는 `pack.SourceFiles`와 같다([docs/39 §2.8](39-r1-analyzer-packs.md#28-범위에-없는-생태계와-분석기-공백)). |
| 3 | config | go.mod, pyproject.toml·setup.cfg, package.json·tsconfig.json, Cargo.toml, CMakeLists.txt |
| 4 | test | Go `*_test.go`. Python `test_*`·`*_test`·`conftest.py`. JS/TS `*.test.*`·`*.spec.*`. Rust `tests.rs`. C/C++ `test_*`·`*_test`·`*_tests`·`*_unittest`·`*-test`. Go가 아닌 언어는 test·tests 디렉터리(JS/TS는 `__tests__`도, Rust는 tests만) 아래 파일도 테스트다. |

- **Go는 그대로다.** Go 파일·README/AGENTS·go.mod의 순위와 포함 규칙은 W11과 같다. vendor 아래 Go 파일도 이전처럼 후보다. 기존 Go fixture의 요청 바이트는 `TestReviewV1IsPinned`가 고정한다. `TestW24GoReviewInputsUnchanged`도 고정한다. 이 시험은 변경 전 코드(aa2ae13)로 두 팩의 모든 요청 해시를 캡처했다. 다른 생태계의 build 파일, vendor·testdata·숨김 디렉터리를 가진 Go 트리도 포함한다.
- **다른 생태계는 범위 안에 그 소스가 있을 때만 넣는다.** 판별은 `pack.InScope`다. Go 저장소의 package.json·Cargo.toml처럼 build 파일만 있으면 config 발췌가 되지 않는다.
- **제외.** Go가 아닌 파일은 분석기 팩의 data 디렉터리 아래에 있으면 후보가 아니다. 숨김·testdata·vendor·node_modules 디렉터리다(`anypack.DataDir`). 어느 깊이든 `build`·`dist`·`target`·`coverage`·`htmlcov`·`site-packages`·`venv`·`__pycache__`·`third_party` 아래 파일도 제외한다. snapshot은 node_modules·`__pycache__`를 모든 깊이에서, build·dist·target·venv를 최상위에서 이미 뺀다. 이 규칙은 그 아래 중첩된 산출물을 뺀다. 예: crate의 target, packages/x/dist, C/C++ build 트리. 그래서 이 이름을 가진 Python·JS/TS·Rust·C/C++ 소스 디렉터리도 빠진다. 범위 안 소스가 이렇게 빠지면 `source_in_excluded_directories`를 남긴다. 코드 발췌가 비거나 적은 이유를 기록에서 볼 수 있다. Go 패키지(internal/build 등)는 이 규칙을 받지 않는다.
- **종류는 경로로만 정한다.** worker와 보고서 decoder가 같은 규칙(`internal/worker/review/record/source.go`)을 쓴다. code는 다섯 생태계의 소스 확장자다. test는 위 테스트 경로, config는 위 manifest다. 이전 기록(Go만)은 그대로 읽힌다. JSON schema의 제약은 바꾸지 않았다. 경로 규칙은 decoder만 검사하므로 schema 설명에만 적었다.
- **한계.**
  - review-v1 팩은 바꾸지 않았다. 그래서 언어와 무관하게 Go 지침만 붙는다. 팩 변경에는 [docs/16 §8](16-evaluation.md)의 승격 근거가 필요하다.
  - 구문 분석기를 쓰지 않는다. 발췌는 파일 첫 50줄이고 함수 경계를 모른다.
  - 검증 역할의 호출자 확장은 Go만이다. 다른 언어 후보는 `no_symbol_for_expansion`과 `caller_expansion_go_only`를 남기고 코퍼스 발췌로만 검증된다.
  - 확장자 없는 스크립트와 생성 파일 이름(예: `.min.js`)은 판별하지 않는다.
  - `compile_commands.json`은 config 후보가 아니다. 생성된 빌드 산출물이고 보통 생성한 머신의 절대 경로를 담으며, 검토 가치보다 크기가 크다. C/C++ 팩이 자료로 읽는다([W16](41-w16-cpp-pack.md)).
  - 파일 6개 상한에서 진입점·소스가 먼저 차므로 테스트는 자주 빠진다(`files_outside_review_context`).
- **시험(TC-123).**
  - `TestW24V1CorpusPerLanguage`: `fixtures/w24-review-inputs`의 언어별 다파일 입력. 코퍼스 내용·순서, 세 역할 실행, 기록 decode, 같은 신원의 두 취득에서 같은 요청 바이트를 확인한다.
  - `TestW24ExclusionsHonoured`: snapshot이 남기는 경로에서 제외 규칙과 build 파일만 있는 생태계를 확인한다.
  - `TestW24ExcludedSourceIsExplained`: 소스가 build 아래에만 있는 Python 프로젝트의 제약 기록을 확인한다.
  - `TestW24BarrelIndexIsNotAnEntryPoint`: 중첩 index 파일이 진입점으로 상한을 채우지 않는지 확인한다.
  - `TestReviewInputsOfPythonThroughTheWorkerAndReport`(`internal/worker/run`): 실제 worker와 기본 보고서 registry로 두 팩의 Python 기록을 확인한다.
  - `TestW24SecretScreenOnPython`: 비 Go 파일의 탐지, 쓸 수 없는 탐지.
  - `TestW24GoReviewInputsUnchanged`.
  - 모두 scripted caller로만 실행했고 실모델 품질 근거가 아니다.

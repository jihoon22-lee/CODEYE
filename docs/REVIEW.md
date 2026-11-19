# 모델 검토

모델 검토는 고정 스냅샷에서 정적 분석 뒤에 실행되는 제한된 모델 작업이다. 모델은 플랫폼이 고른 발췌와 근거 ID만 받는다. 모델의 출력은 후보와 판정 서술일 뿐이다. task 상태, 재현 여부, 통계, 보고서 수치는 워커와 서버가 구조화 기록으로 정한다. 이 문서는 역할, 지침 팩, 입력 선택과 크기 한도, 비밀값 선별, 출력 검사와 검증 규칙, 복구 경로, OpenCode 어댑터, 소스 정책, 검토 기록 형식을 정한다.

- 구현 위치: [`internal/worker/review`](../internal/worker/review), [`internal/worker/review/record`](../internal/worker/review/record), [`internal/worker/opencode`](../internal/worker/opencode), [`prompts/review-r1`](../prompts/review-r1), [`prompts/review-v1`](../prompts/review-v1)
- 요구사항: REQ-004/005/009/010/011/012/013/026/027/030, NFR-007/008/010([요구사항](REQUIREMENTS.md))
- 수용 시험: TC-05, TC-10~12, TC-16, TC-18, TC-33, TC-34, TC-76~79, TC-85~89, TC-97, TC-100, TC-123, TC-128, TC-129([시험](TESTING.md))
- 품질 평가와 지침 개정 절차: [평가](EVALUATION.md)

## 공통 경계

- 플랫폼이 승인한 스냅샷·모듈·발췌와 근거 ID만 모델에 전달한다. 원본 절대 경로, 인증정보, 평가 정답, 개인 모델 세션은 입력이 아니다.
- 대상 저장소의 README·AGENTS·설정에 있는 실행·권한·네트워크 요청은 문서 내용이다. 요청 설정·도구·권한을 바꾸지 못한다. 모든 입력은 `<review-input>` 안에 들어가고, 프롬프트는 그 내용을 신뢰하지 않는 검토 자료로 다루고 도구를 쓰지 말라고 지시한다.
- 문서에서 읽은 기대 동작, 코드에서 관찰한 동작, 그로부터 추론한 영향을 구분한다. 근거는 상대 경로·행 범위·blob SHA-256 또는 검증된 도구 관찰 참조로 남는다.
- 모델은 새 근거 ID를 발급하지 못한다. 셸·네트워크·추가 모델을 호출하지 못한다. 경로나 줄 번호를 지어내도 읽기·실행 범위가 늘지 않는다.
- 워커가 스냅샷의 바이트와 행 범위를 확인한다. 서버는 run·attempt·manifest·근거 연결을 확인한다.
- 실패·시간 초과·정책 제외는 해당 task에 남긴다. 이미 확보한 도구 관찰과 탐색 사실은 보존한다.
- 다른 모델이나 외부 모델로 자동 전환하지 않는다. 모델 설정이 없으면 검토 task는 `unsupported`(`model_not_configured`)다.
- 모델의 내부 추론(reasoning), 원시 이벤트 스트림, 원시 프롬프트, 프로세스 출력은 기록·로그·보고서·화면에 넣지 않는다.

## 검토 팩

검토 지침은 저장소에 포함된 팩으로 빌드 시점에 묶인다(`embed`). 대상 저장소가 지침이나 digest를 제공할 수 없다.

| 팩 | `pack.yaml` id·version | 상태 | 기록 schema | 역할 |
|---|---|---|---|---|
| [`review-r1`](../prompts/review-r1/pack.yaml) | `review-r1` 2.0.0 | `status: default`. 팩을 지정하지 않으면 이 팩을 쓴다 | `0.2` | environment, documents, investigate, interpret, verify |
| [`review-v1`](../prompts/review-v1/pack.yaml) | `codeye.review` 1.0.0 | 이전 팩. 이전 기록을 읽고 같은 조건으로 재현할 때만 명시해서 고른다 | `0.1` | context, investigate, verify |

- 지침 개정은 기본 팩 하나에만 한다. 개정의 검증 절차는 [평가](EVALUATION.md)에 있다. 두 팩을 나란히 비교하는 일은 새 팩 id로 기본값을 바꿀 때만 한다.
- review-v1 디렉터리와 id는 지우거나 바꾸지 않는다. 기존 기록이 v1의 팩 id·digest로 해석되고, `TestLegacyPackIsPinned`가 v1의 역할별 digest·지침 해시·계획 key·발췌 선택·정규화한 요청 해시를 고정한다. v1 요청 바이트를 바꾸는 변경은 시험 갱신이 아니라 승격 근거가 필요한 지침 변경이다.
- review-v1에는 synthesize·report 지침과 Go 이외 언어 지침 파일이 있지만 선택되지 않는다. v1은 언어와 무관하게 공통·역할·영역·Go 지침만 붙인다. 보고서는 Go가 만들므로 종합·보고 역할에 모델을 호출하지 않는다.

### 팩 구성과 digest

- review-r1은 공통 지침 `00-core.md`, 역할 지침 다섯, 영역 지침 `domains.md`, 언어 지침 다섯(`languages/cpp.md`·`go.md`·`javascript.md`·`python.md`·`rust.md`)으로 구성한다.
- 한 역할의 지침 텍스트는 공통, 역할, 영역, 고른 언어 지침 순으로 이어 붙인다.
- 언어 지침은 스냅샷 scope 안에 소스가 있는 생태계만 생태계 이름 순서로 붙는다. 판별은 분석기 팩의 공백 계획과 같은 `pack.InScope`다([분석기](ANALYZERS.md)). 소스 확장자로 세고, data·vendor 디렉터리는 빼며, build 파일만으로는 생태계가 들어오지 않는다. 지원 생태계가 없으면 언어 지침을 붙이지 않는다.
- 역할별 digest는 SHA-256이다. 입력은 `pack.yaml`과 실제로 고른 파일이고, UTF-8 상대 경로 오름차순으로 `경로 NUL 내용 NUL`을 이어 해시한다. 따라서 같은 역할도 생태계 조합마다 digest가 다르고, `pack.yaml`의 version·status가 바뀌면 모든 역할 digest가 바뀐다.
- 호출 기록마다 팩 id·version·역할 digest(`pack_sha256`)·고른 파일 목록(`pack_files`)이 남는다. 보고서는 실제 사용한 팩을 표시한다.
- digest는 run 생성 시 계산한다. 저장소가 제공한 digest를 믿지 않는다.
- 저장소 `.gitattributes`(`* text=auto eol=lf`)가 모든 checkout을 LF로 고정한다. CRLF로 바뀐 checkout은 지침 바이트가 달라져 digest와 요청 크기가 OS마다 달라진다.

### 지침 작성 규칙

- 지침 파일에는 평가·시험 사례의 내용을 쓰지 않는다. 사례 ID, 작업 ID, fixture 경로와 식별자, 특정 문서·재시도 횟수·버전 조합이 여기에 해당한다. 예시가 필요하면 사례 없는 규칙으로 쓴다.
- [`prompts/review-r1/bundle_test.go`](../prompts/review-r1/bundle_test.go)가 사례 ID·fixture 이름을 검사한다. 같은 뜻을 다른 말로 쓴 문장은 이 검사로 잡지 못하므로 지침 검토에서 확인한다.
- 언어 생태계마다 언어 지침이 있어야 한다(시험으로 고정). 의존성·타입·헤더가 없어 생긴 진단은 결함이 아니라 분석 공백으로 다룬다. 지침에 도구 규칙 ID를 쓰지 않는다.
- 지침 작성자는 보류 평가 결과와 보류 사례를 읽지 않는다. 범위는 [평가](EVALUATION.md)에 있다.

## 팩 선택

팩은 노드 운영자 설정이다. run 요청, 프로필, API로는 고를 수 없다.

| 경로 | 동작 |
|---|---|
| `codeye agent --review-pack` | 기본 `review-r1`. `review-r1`·`review-v1`만 받는다. 다른 값은 사용 오류(종료 코드 2)이며 기본값으로 바꾸지 않는다 |
| 워커 Hello `review_pack` | 비어 있으면 `review-r1`. 모르는 값이면 워커가 시작하지 않는다 |
| 개인 설정 `agent.model.review_pack` | 비우면 `review-r1`. 다른 값은 설정 오류([사용 안내](GUIDE.md)) |
| `scripts/regression_pilot.py`의 `CODEYE_PILOT_REVIEW_PACK` | 비우면 에이전트 기본값(`review-r1`) |
| `scripts/validate_bundle.py` | review-r1 `pack.yaml`의 `status: default`를 확인한다 |
| 보고서 | 계획에 있는 역할 key로 해석한다. review-v1 계획은 세 역할로 읽는다 |

## review-r1 역할과 산출물

워커는 deep-review 계획에서 탐색 task(`env.discover`, `docs.discover`)와 정적 분석 task 뒤에 다섯 역할을 이 순서로 둔다. 탐색 산출물은 [구조](ARCHITECTURE.md)에 있다. 한 역할의 출력은 다음 역할에 넘길 자료다. 역할 수가 모델 호출 수를 정하지 않는다. 환경 버전 비교, 문서 존재 확인, 정적 검사 실행, 보고서 게시는 모델 없이 플랫폼이 처리한다.

| task | 단계 | 판정 필수 | 호출 없이 끝나는 경우 | 입력 | 출력 |
|---|---|---|---|---|---|
| `review.environment` | `environment` | 아니오 | 선언된 요구가 모두 `satisfied`면 `no_environment_gap`. 탐색 산출물을 쓸 수 없으면 `dependency_failed` | 차이가 있는 모듈의 요구·근거 ID·노드 관측·판정 | gap 모듈마다 영향 메모 하나. 플랫폼 판정은 바꾸지 못한다 |
| `review.documents` | `documents` | 계획 시 범위 안에 안내 문서가 있을 때만 | 문서 없음 `no_documents`(필수 아님), 선택된 문서 없음 `documents_not_selected`, `dependency_failed` | 선택 문서 발췌 | 적용 경로가 있는 계약·충돌·질문, 지침성 문구 메모 |
| `review.investigate` | `code_review` | 예 | — | 수락된 문서·환경 결과, 모듈별 코드·테스트 발췌. 원시 도구 진단은 넣지 않는다(REQ-009) | 후보 `investigate_N`(0~4개) |
| `review.interpret` | `code_review` | 아니오 | 해석할 도구 관찰이 없으면 `no_tool_observations` | 관찰 ID·규칙·위치와 주변 줄 창 | 관찰에 연결한 가설 `interpret_N`(최대 2) 또는 해석 한계 |
| `review.verify` | `code_review` | 예 | investigate·interpret가 모두 수락되지 않으면 `dependency_failed`. 후보가 없으면 호출 없이 성공(`no_candidates_to_verify`) | 수락된 두 역할의 후보, 문서 계약, 관찰, 발췌와 호출자·테스트 확장 | 후보별 판정 |

역할 지침과 허용 입력은 다음과 같다.

| 지침 | 허용된 입력 | 다음 단계에 남길 결과 |
|---|---|---|
| [환경 분석](../prompts/review-r1/environment.md) | 모듈 범위, 선언 버전, 노드 관측값과 출처 | 검사별 요구·관측·불일치·미확인과 그 영향 |
| [문서 검토](../prompts/review-r1/documents.md) | 경로별 문서·설정·선택 첨부와 제한된 코드 발췌 | 적용 경로, 문서상 계약, 상충하는 근거, 미확인 질문 |
| [독립 코드 분석](../prompts/review-r1/investigate.md) | 모듈 계약, 환경 한계, 코드·테스트 발췌 | 원인·발생 조건·영향·반증 가능성을 가진 후보 또는 빈 목록 |
| [정적 진단 해석](../prompts/review-r1/interpret-diagnostics.md) | 어댑터가 발급한 관찰 ID, 버전·범위·파싱 상태와 관련 발췌 | 원시 관찰에 연결한 영향 가설 또는 해석 한계 |
| [후보 검증](../prompts/review-r1/verify.md) | 이전 후보 원문과 ID, 호출부·테스트·반대 근거 | 지지·기각·판단 보류의 이유, 유효·무효 조건, 추가 확인 사항 |

- 도구 진단 해석은 독립 조사와 별도 입력으로 수행하고 원시 메시지·규칙·위치를 보존한다. 도구의 실행 상태, finding 수, 심각도를 다시 말하지 않고, 진단이 없다는 사실을 깨끗한 코드로 서술하지 않는다.
- 출력 개수 한도: documents는 계약 6, 충돌 3, 질문 4, 지침성 문구 메모 3개까지. interpret는 가설 2, 한계 3개까지이고 합해서 하나 이상. investigate는 후보 4개까지.
- 하위 역할은 수락된 결과만 받는다. 앞 역할이 생략·실패·사용 불가면 `upstream_<역할>_<상태>` 한계를 남긴다(예: `upstream_documents_skipped`).
- 모델 전송이 승인되지 않으면 모든 역할이 `skipped_policy`(`model_source_not_approved`)다.

### 검토 대상이 없는 역할

새 task 상태를 만들지 않는다. `skipped_policy`와 통제된 사유 코드를 함께 쓴다. 사유 코드는 task의 `reason`에 저장하고 화면은 코드별 문구로 표시한다.

| 사유 코드 | 의미 | 모델 검토 성공으로 셈 |
|---|---|---|
| `model_source_not_approved` | 소스의 모델 전송 미승인(정책 제외) | 아니오 |
| `no_documents` | 스냅샷 범위에 문서가 없어 문서 역할 생략 | 아니오 |
| `no_tool_observations` | 해석할 도구 관찰이 없어 진단 해석 생략 | 아니오 |
| `no_environment_gap` | 모든 선언이 관측과 일치해 환경 해석 생략 | 아니오 |
| `secret_screen_failed` | 비밀값 분석이 실행됐지만 결과를 쓸 수 없음(아래 비밀값 선별) | 아니오 |
| `no_review_scope` | slice의 파일을 overview가 이미 보냈거나 모두 보류·읽기 불가([모듈 단위 검토 분할](#모듈-단위-검토-분할)) | 아니오 |

필수 여부는 계획 시 정한다. 문서가 없어 생략한 문서 역할을 모델 검토 성공으로 세지 않는다. 필수 모델 작업이 실패했으면 도구 결과가 남아도 검토 전체를 완료로 올리지 않는다. 진행 단계 판정에서 이 생략을 어떻게 셈하는지는 [구조](ARCHITECTURE.md)에 있다.

## review-v1 역할

| task | 단계 | 판정 필수 | 출력 |
|---|---|---|---|
| `review.context` | `documents` | 예 | 사실 1~4개. 종류 `observed`·`documented`·`inferred`, 근거 ID. `documented`는 문서·설정 근거만 인용한다 |
| `review.investigate` | `code_review` | 예 | 후보 0~4개(`candidate_N`). 각 후보는 code 근거를 하나 이상 인용한다. 같은 코퍼스로 만든 context 기록이 없으면 `dependency_failed` |
| `review.verify` | `code_review` | 예 | 후보별 판정(아래 검증 규칙) |

- 문서가 없어도 소스에서 시작한다. 후보가 없으면 빈 배열을 명시해야 한다. 누락된 배열, 알 수 없는 필드, 존재하지 않는 ID, 문서만 인용한 코드 후보는 거부한다.
- context·investigate는 한 번만 호출하고 형식 복구가 없다. 출력이 무효면 task는 `failed`(`review_evidence_invalid`)다.

## 검토 입력 선택

모든 발췌는 고정 스냅샷의 manifest 항목(크기·SHA-256)과 대조해 읽는다. 512 KiB를 넘는 파일, UTF-8이 아니거나 NUL을 포함한 파일, 민감값 패턴에 걸린 파일, 비밀값 선별이 보류한 파일은 발췌하지 않는다. 구문 분석기는 쓰지 않고, 발췌는 줄 단위다. 근거 ID는 manifest digest, 스냅샷 상대 경로, blob SHA-256, 행 범위에서 결정된다(`record.ExcerptID`).

review-r1의 한도는 발췌 텍스트를 JSON으로 인코딩한 바이트로 센다(`encoding/json`은 `<`·`>`·`&`를 6바이트로 바꾼다). 상수는 [`rolepack_inputs.go`](../internal/worker/review/rolepack_inputs.go), review-v1 코퍼스 상수는 [`context.go`](../internal/worker/review/context.go)에 있다.

### 종류 판별과 제외 디렉터리

- 발췌 종류(code·test·config·document)는 경로만으로 정한다. worker와 보고서 decoder가 같은 규칙([`record/source.go`](../internal/worker/review/record/source.go))을 쓴다. code는 분석기 팩의 `pack.SourceFiles`와 같은 소스 확장자다. test는 언어별 이름·디렉터리 관례, config는 고정 manifest 목록이다. 경로 규칙은 decoder만 검사하고 JSON schema에는 설명으로만 적었다.
- 테스트 경로: Go `*_test.go`. Python `test_*`·`*_test`·`conftest.py`. JS/TS `*.test.*`·`*.spec.*`. Rust `tests.rs`. C/C++ `test_*`·`*_test`·`*_tests`·`*_unittest`·`*-test`. Go가 아닌 언어는 `test`·`tests` 디렉터리 아래 파일도 테스트다(JS/TS는 `__tests__`도, Rust는 `tests`만).
- manifest(config): go.mod, pyproject.toml·setup.cfg, package.json·tsconfig.json, Cargo.toml, CMakeLists.txt. `compile_commands.json`은 생성 산출물이고 보통 생성한 머신의 절대 경로를 담으므로 config 후보가 아니다.
- Go가 아닌 파일은 분석기 팩의 data 디렉터리(숨김·testdata·vendor·node_modules, `anypack.DataDir`) 아래에 있으면 후보가 아니다. 어느 깊이든 `build`·`dist`·`target`·`coverage`·`htmlcov`·`site-packages`·`venv`·`__pycache__`·`third_party` 아래 파일도 뺀다. 같은 이름의 소스 디렉터리도 빠진다. 범위 안 소스가 이렇게 빠지면 `source_in_excluded_directories`를 남긴다.
- Go 파일은 이 제외 규칙을 받지 않는다(internal/build 같은 Go 패키지가 흔하다). review-r1의 Go 발췌는 vendor·testdata·`.`·`_`로 시작하는 디렉터리를 건너뛴다. review-v1은 vendor 아래 Go 파일도 후보로 둔다.
- 다른 생태계의 파일과 manifest는 그 생태계의 소스가 범위 안에 있을 때만 넣는다(`pack.InScope`). Go 저장소의 package.json·Cargo.toml처럼 build 파일만 있으면 config 발췌가 되지 않는다.
- 확장자 없는 스크립트와 생성 파일 이름(예: `.min.js`)은 판별하지 않는다.

### 코드 발췌(review-r1 investigate)

- Go 파일은 가장 깊은 go.mod 디렉터리(모듈)로 묶는다. 다른 언어 파일은 같은 생태계 manifest가 있는 가장 깊은 디렉터리로 묶고, 없으면 `.`다.
- 묶음 안의 순서는 진입점, 소스, 테스트이고 같은 순위는 깊이와 경로 순이다.
- 진입점: Go `main.go`. Python `__main__`·`main`·`app`·`cli`·`manage`(`.py`). JS/TS `main`·`app`·`server`·`cli`(소스 확장자)와 `index`. `index`는 모듈 루트(가장 깊은 package.json·tsconfig.json 디렉터리)나 그 `src`에 있을 때만 진입점이다. Rust `main.rs`·`lib.rs`. C/C++ `main`(`.c`·`.cc`·`.cpp`·`.cxx`·`.c++`). 테스트 경로는 진입점이 아니다.
- 순회(round-robin): 각 순회에서 묶음마다 다음 파일 하나를 고른다. 묶음은 생태계 이름 순으로 돈다. 첫 순회가 범위 안의 모든 모듈과 언어를 지난 뒤에 어느 모듈도 두 번째 파일을 얻는다.
- 발췌는 파일 첫 줄부터 최대 50줄·2,400바이트, 합계 8개·12,000바이트다. 남은 예산이 128바이트 미만이면 멈춘다. 발췌가 파일 끝에 못 미치면 `excerpt_only`, 고르지 못한 후보 파일이 있으면 `files_outside_review_context`를 남긴다.
- 발췌가 있는 모듈 디렉터리는 `modules`(최대 32개)에 들어간다.
- 범위 안 소스가 모두 제외 디렉터리 아래 있거나 발췌가 하나도 없으면 investigate는 `failed`(`review_input_unavailable`)다.

### 문서 발췌(review-r1 documents)

- 대상은 탐색 산출물에서 `selected` 상태인 안내 문서다. 순서는 에이전트 지침, README, CONTRIBUTING, 설계 문서이고 같은 분류는 얕은 경로가 먼저다.
- 문서당 첫 40줄·2,000바이트, 최대 8개, 합계 10,000바이트다. 넘으면 `documents_outside_review_input`, 문서 일부만 보내면 `document_excerpt_only`를 남긴다.
- 읽지 않은 안내 문서(최대 16개), 모듈별 문서 연결과 겹침 묶음(각 최대 32·16개, 목록당 8개)을 함께 준다. 줄이면 `document_lists_shortened`다.
- 문서 규칙의 적용 경로(`applies_to`)는 인용한 문서의 디렉터리와 같거나 그 아래여야 한다.

### 환경 입력(review-r1 environment)

- gap 모듈(판정이 `satisfied`가 아닌 모듈) 최대 4개, 그 모듈들의 요구 최대 16개다. 넘으면 `environment_gaps_outside_input`·`requirements_outside_input`를 남긴다.
- 요구마다 원문 줄(600바이트 이하)과 근거 ID를 준다. 원문을 쓸 수 없으면 `requirement_source_omitted`다.
- 출력 메모는 자기 모듈의 요구·근거 ID만 인용한다. 한 모듈·호스트에서 본 버전을 다른 모듈로 옮기지 않는다.

### 도구 관찰과 해석 창(review-r1 interpret)

- 계획된 분석기 task의 관찰 산출물을 다시 검증해 읽는다. 소스에서 억제된 진단과 구조 지표는 넣지 않는다([분석기](ANALYZERS.md)).
- 도구 상태는 최대 8개(`tools_outside_interpret_input`), 관찰은 경로·줄·ID 순으로 최대 8개다(`observations_outside_interpret_input`).
- 관찰 메시지가 UTF-8이 아니거나, 민감값 패턴에 걸리거나, 600바이트를 넘으면 `(omitted by the platform)`으로 바꾸고 `observation_message_omitted`를 남긴다. 비밀값 선별이 보류한 파일에 대한 메시지도 규칙과 위치만 보낸다.
- 진단 위치가 다섯 언어 중 하나의 소스 파일이면 위치 10줄 앞부터 21줄, 1,600바이트 이하의 줄 창을 만든다. 창은 최대 4개다(`observation_context_outside_input`). manifest·설정·문서처럼 소스가 아닌 파일에는 창이 없고 관찰만 간다.

### 검증 입력과 호출자 확장(verify)

- 후보가 인용한 발췌를 스냅샷에서 다시 읽어 ID 연결을 확인한다. 확인하지 못하면 `review_input_unavailable`이다.
- 호출자·테스트 확장은 Go만 한다. 인용된 Go 발췌에서 패키지 함수 최대 8개를 고르고, 같은 패키지의 직접 호출을 Go 구문 트리로 찾아 호출자 함수의 검증·소유권 이전·정리와 테스트의 관련 구간을 발췌한다.
  - 추가 발췌는 최대 6개·합계 8,000바이트, 한 발췌는 최대 60줄·2,400바이트다.
  - 탐색은 승인된 scope의 Go 파일 80개·8 MiB까지다. 파일을 열 때 manifest의 크기와 SHA-256을 대조한다.
  - 간접 호출, 메서드 디스패치, 다른 패키지 호출, 외부 계약은 범위 밖이며 `caller_expansion_package_functions_only`를 남긴다. 호출을 찾지 못하면 `no_callers_found`, 예산 밖이면 `callers_outside_review_context`다.
  - 확장을 시도한 verify 기록에는 Go만 있는 스냅샷을 포함해 항상 `caller_expansion_go_only`와 `caller_expansion_package_functions_only`가 남는다. 인용에서 Go 패키지 함수를 찾지 못하면(다른 언어만 인용한 후보 등) `no_symbol_for_expansion`이 더해진다. 다른 언어 후보는 인용한 발췌와 문서 계약으로만 검증된다.
- 비밀값 선별이 보류한 파일의 호출자·테스트 발췌는 빼고 `secret_files_withheld`를 남긴다.
- review-r1 verify 요청의 발췌는 인용 발췌, 확장, 인용되지 않은 문맥 순으로 최대 40개다(인용 발췌는 항상 남는다). 요청이 한도를 넘으면 확장·비인용 문맥·문서 계약을 먼저 줄이고(`review_input_trimmed`, `callers_outside_review_context`, `contracts_outside_verify_input`), 그래도 넘으면 후보를 앞에서부터(investigate 다음 interpret) 보낼 수 있는 만큼만 보낸다. 보내지 못한 후보는 `unreviewed`(`verification_budget_exceeded`)로 남고 task는 `failed`(`review_input_incomplete`)다. 성공으로 바꾸지 않는다.

### review-v1 코퍼스

context·investigate·verify가 같은 코퍼스를 쓴다. 순위가 낮은 것부터 고르고 같은 순위는 디렉터리 깊이와 경로 순이다. 발췌는 파일 첫 줄부터다.

| 순위 | 종류 | 파일 |
|---|---|---|
| 0 | code | 진입점(위 코드 발췌의 진입점 규칙과 같다) |
| 1 | document | README.md, AGENTS.md(최대 2개) |
| 2 | code | 그 밖의 소스 |
| 3 | config | 위 manifest 목록 |
| 4 | test | 위 테스트 경로 |

- 한 요청에 파일 최대 6개, 파일당 최대 50줄, 코드 파일당 3,200바이트·문서당 1,200바이트, 합계 12,000바이트다.
- 문서를 하나도 고르지 못하면 `no_document_in_selected_context`, 상한 때문에 빠진 후보가 있으면 `files_outside_review_context`를 남긴다. 진입점·소스가 먼저 차므로 테스트는 자주 빠진다.
- Go 파일·README/AGENTS·go.mod의 순위와 포함 규칙은 고정이다. 기존 Go 입력의 요청 바이트는 `TestLegacyPackIsPinned`와 `TestGoReviewInputsUnchanged`가 고정한다.

### 요청 크기

| 한도 | 값 | 적용 |
|---|---|---|
| review-r1 요청 한도 `PromptBudget` | 40,000바이트 | 한 요청의 전체 메시지 텍스트(`opencode.Prompt`: 지침, 계약, 복구 규칙, 입력, 감싸는 문장) |
| 복구 여유 | 3,072바이트 | 첫 요청을 만들 때 형식 복구 요청이 들어갈 자리를 남긴다 |
| review-v1 verify 한도 | 60 KiB | v1 검증 요청 |
| OpenCode 어댑터 입력 한도 | 64 KiB | 지침 + 입력. 넘으면 `invalid_review_input` |
| QA 예산 relay 입력 한도 | 49,152바이트(기본) | relay가 받는 요청당 메시지 텍스트. 40,000바이트와의 차이는 OpenCode 클라이언트 자체 메시지 몫이다([평가](EVALUATION.md)) |
| 출력 | relay 기본 1,536 토큰 | OpenCode 관리 설정의 모델 출력 한도와 같다 |

- 요청은 메시지 하나이고 도구 정의를 쓰지 않는다.
- 보내기 전에 메시지 바이트를 재고, 넘으면 발췌를 뒤에서부터 줄이고 `review_input_trimmed`를 남긴다. 최소 발췌로도 넘으면 task는 `failed`(`review_input_too_large`)다.
- 예산이 모자라면 목록 뒤쪽이 먼저 빠진다. 첫 순회에서 생태계 이름 순으로 뒤인 언어의 발췌가 여기에 해당한다. 다섯 생태계가 모두 범위에 있으면 언어 지침이 커져 코드 발췌가 줄고, 최대 길이 후보가 많은 verify는 일부 후보를 보내지 못할 수 있다. 요청 한도와 역할별 지침 구성의 변경은 소유자 결정이다.

## 모듈 단위 검토 분할

overview 조사(`review.investigate`)가 보내지 못하는 코드·테스트 파일을 모듈 단위 slice로 나누어 slice마다 조사·검증 한 쌍(`review.investigate.sNNN`, `review.verify.sNNN`)으로 검토한다. 계획은 manifest·scope·상수로만 정하고, 호출 상한 안에서 slice를 차례로 허용한다. 구현은 [`slice_plan.go`](../internal/worker/review/slice_plan.go)(계획)와 [`slice_run.go`](../internal/worker/review/slice_run.go)(실행)이다.

| task | 단계 | 판정 필수 | 모델 호출 | 내용 |
|---|---|---|---|---|
| 전체 역할 다섯 개 | 위 표 | 위 표 | 위 표 | 바뀌지 않는다. overview verify는 계속 interpret 후보도 검증한다 |
| `review.scope` | `code_review` | 아니오 | 없음 | slice가 하나 이상일 때만 계획한다. overview가 실제로 보낸 파일을 빼고 범위 산출물을 쓴다 |
| `review.investigate.sNNN` | `code_review` | 아니오 | 1~3 | slice 하나의 코드·테스트 발췌로 조사한다 |
| `review.verify.sNNN` | `code_review` | 아니오 | 0~3 | 같은 slice investigate의 후보만 검증한다 |

slice는 판정 필수가 아니다. 필수로 두면 호출 상한보다 큰 저장소는 모두 `partial`이 되어 run 상태가 의미를 잃는다. 검토하지 못한 범위는 범위 산출물과 slice task 상태로 남는다. 진행 단계에서는 `no_review_scope`만 단계를 낮추지 않고, 상한으로 거절된 slice나 선별이 없어 건너뛴 slice는 `code_review` 단계를 `partial`로 남긴다([구조](ARCHITECTURE.md#표시-단계와-task-단계)).

### 계획

- 모듈 단위는 overview 코퍼스의 묶음과 같다(위 [코드 발췌](#코드-발췌review-r1-investigate)). Go는 가장 깊은 go.mod 디렉터리, 다른 언어는 같은 생태계 manifest가 있는 가장 깊은 디렉터리, 없으면 `.`이다. ID는 `<ecosystem>:<dir>`이고, 범위 산출물의 `discovered`는 모듈 탐색이 같은 ID를 찾았는지 표시한다.
- 파일 상한 `c(f) = min(2400, 6 × size + 2)`. JSON 인코딩은 바이트 하나를 최대 6바이트(`\u00XX`)로 바꾸고 따옴표 두 개가 붙으므로 실제 발췌 바이트보다 작지 않다.
- overview 접두 P: 모든 파일을 상한 크기로 두고 overview의 순회(묶음 순서, 묶음 안 순위)를 그대로 흉내 낸다. 고른 파일이 8개 미만이고 남은 예산이 128바이트 이상이며 상한이 남은 예산에 들어가는 동안 파일이 P에 들어간다. 처음 들어가지 않는 파일에서 P가 끝난다. overview는 같은 순서로 파일을 보고 상한보다 많이 쓰지 않으므로 P의 파일을 반드시 낸다.
- 남은 파일은 묶음마다 순위대로 연속 조각(8개·12,000바이트 이하, 상한 기준)으로 자른다. 작은 묶음은 통째로 한 조각이다. 조각은 순회마다(모든 묶음의 첫 조각, 그다음 둘째 조각) 묶음 순서로 나오고, 이웃한 조각은 한도 안에서 한 slice를 같이 쓴다(slice당 모듈 8개 이하). 그래서 너비가 깊이보다 먼저다.
- slice는 32개(`s001`~`s032`)까지다. 그 뒤의 파일은 계획하지 않고 모듈별로 세며 `review_slice_limit`을 남긴다.
- 모든 적격 파일이 overview에 들어가는 저장소는 slice가 없다. 계획·요청 바이트·기록이 이전과 같다(회귀 집합과 작은 fixture 전부).
- 계획은 manifest, scope, 상수로만 정하고 호출 상한을 쓰지 않는다. 재시도는 모든 task 행을 queued로 되돌리고 남은 task가 있으면 완료하지 않으므로, 상한에 따라 task key가 달라지면 고아 task가 생긴다.
- 계획 순서는 전체 역할 다섯, `review.scope`, slice 쌍이다. 모든 task가 run이 `running`이 되기 전에 만들어지므로 계획 revision은 1이고 가중치를 선언하지 않는다(진행 기준 `task_count`).

### 호출 상한과 승인

- 단위는 모델 호출이고 attempt마다의 상한이다. `codeye agent --review-max-calls N`(기본 99, 15~207), Hello `review_max_calls`, 개인 설정 `agent.model.review_max_calls`로 정한다. run이나 profile로 바꿀 수 없다(팩 선택과 같다). review-r1에만 적용하고 review-v1은 그대로다.
- 기본 99는 중간 규모 저장소의 대부분을 기본 설정으로 검토하기 위한 값이다(처음 기본값 33은 유료 회귀 실행에서 파일럿 Go 모듈 67개 파일 중 34개를 28회 호출로 검토했다). 회귀 실행에서 실모델 호출 55회의 비용은 보고된 사용량으로 약 USD 0.94, 보수적 장부 차감으로 USD 2.02였다. 기본 상한 99회를 다 쓰는 run은 그 대략 두 배(보고 사용량 약 USD 1.7, 장부 차감 약 USD 3.6)다. 실제 비용은 모델·저장소 크기·형식 복구 횟수에 따라 다르다. 비용을 줄이려면 상한을 낮춘다. 15는 전체 역할만 실행하고 slice를 승인하지 않는다([안내서](GUIDE.md)).
- 전체 역할 다섯 개는 최대 15회(5 × 3)를 쓰고 잘리지 않는다. 그래서 최솟값이 15다. 전체 역할이 모든 slice보다 먼저 실행되므로 그 몫을 따로 예약할 필요가 없다.
- 계수는 attempt 안에서만 한다. attempt 하나는 worker 프로세스 하나이고 task를 차례로 실행한다. 모든 role-pack 호출은 보내기 전에 attempt scratch의 계수 파일에 한 줄을 쓴다. 계수를 쓰지 못한 호출은 보내지 않는다(`review_call_count_failed`). 전역 상태나 프로세스 사이 상태는 없다. 한 attempt 안에서 slice를 병렬로 실행하려면 공유 계수가 필요하다.
- 승인은 계획 순서대로, 실제 사용량으로 한다. slice k의 investigate를 시작하기 전에 `상한 − 사용한 호출 ≥ 6`(investigate·verify 한 쌍의 최악 3 + 3)이면 승인한다. 처음 거절된 slice와 그 뒤의 모든 slice는 호출과 기록 없이 `budget_exhausted`(`review_budget_exhausted`)로 끝나고 짝 verify도 같다. 결정은 attempt scratch에 남기고 verify가 따른다.
- 그래서 attempt의 호출은 상한을 넘지 않는다. 최악 사례로 미리 승인하면 상한의 세 분의 이가 버려지지만, 실제 사용량으로 승인하면 모델이 형식 복구를 많이 할수록 승인되는 마지막 slice가 run마다 달라질 수 있다. 승인 결과는 slice task 상태로 보인다.

| 상한 | 한 번에 끝나는 task(1회) | 모든 task가 3회 | 최악 사례 사전 승인 |
|---|---|---|---|
| 15 | coverage fixture의 slice 7개 중 3개 | 0개 | 0개 |
| 99 | 7개 모두 | 7개 모두 | 7개 모두 |
| 207 | 7개 모두 | 7개 모두 | 7개 |

(`TestSliceAdmissionKeepsTheCeiling`, [`fixtures/modules/slices/input`](../fixtures/modules/slices/input). environment는 차이가 없어 호출하지 않는다.)

### 비밀값 선별과 판단 순서

- `review.scope`와 slice는 전체 역할과 같은 선별 규칙을 따른다. 선별이 끝났으면 보고된 파일을 `withheld`로 빼고, 선별이 계획되지 않았거나 실행되지 않았으면(gitleaks 미설정) 기본 읽기 필터만 거친 입력으로 실행한다. 이때 범위 산출물은 `screen: not_screened`이고 범위 산출물과 모든 slice 기록이 `model_input_not_secret_screened`를 싣는다. 보고서는 비밀정보 절 `model_input`을 미선별로 보이고 공백 `model_input_not_screened`를 남긴다. 선별이 실행됐지만 결과를 쓸 수 없으면 `skipped_policy`(`secret_screen_failed`)이고 요청을 보내지 않는다.
- 선별은 task마다 시작할 때 다시 읽는다. slice 발췌도 같은 읽기 경로를 지나므로 보류 파일과 민감값 패턴 필터를 똑같이 받는다.
- slice task의 판단 순서: 모델 전송 허가 → 선별 결과를 쓸 수 있음(아니면 `secret_screen_failed`) → 범위 산출물을 읽을 수 있음(아니면 `dependency_failed`) → 보낼 파일이 남음(아니면 `no_review_scope`) → 호출 상한. 보낼 파일이 없는 slice는 상한이 다해도 `no_review_scope`다.

### 범위 산출물

`review.scope`는 모델을 호출하지 않는다. overview investigate 기록(수락이든 실패든, 요청을 보냈을 때)의 context 발췌에서 overview가 실제로 보낸 코드·테스트 파일을 읽고, 계획의 모든 파일을 선별과 읽기 필터로 다시 확인한 뒤 산출물 하나를 role `other`, media type `application/vnd.codeye.review-scope+json`으로 올린다. [스키마](../schemas/review-scope.schema.json) · [예시](../examples/review-r1/slices/scope.json)

- 필드: `schema_version`(0.1), `kind`(`review_scope`), `pack`, `snapshot_manifest_digest`, `run_id`, `attempt_id`, `screen`(`applied` 또는 `not_screened`. `not_screened`이면 `limitations`에 `model_input_not_secret_screened`가 있다), `budget`(`max_calls`, `max_request_bytes`, `whole_run_max_calls` 15, `slice_reserve_calls` 6, `calls_before_slices`, `admission` `sequential_reserve`), `limits`, `upstream`(overview investigate 기록의 SHA-256), `modules[]`(`id`, `discovered`, `files_eligible`, `files_not_planned`), `overview`(`files`: P와 32개 뒤에서 overview가 보낸 파일, `offered`: overview가 보낸 경로), `slices[]`(`key`, `investigate_task`, `verify_task`, `modules`, `files`), `limitations`.
- 파일마다 `path`, `sha256`, `module`, `bound_bytes`, `planned_lines`, `planned_bytes`, `total_lines`, `state`. 상태는 `planned`(slice가 보낸다), `covered_by_overview`(overview가 보냈다), `withheld`(선별이 보류), `unreadable`(UTF-8 아님·NUL·민감값 패턴·빈 파일), `overview_not_sent`(P에 있지만 overview가 보내지 않음: 요청을 줄였거나 보내기 전에 실패)다. 적격 파일은 모두 정확히 한 곳에 있다. 계획하지 않은 파일은 목록 대신 모듈별 `files_not_planned`로 센다.
- 승인된 slice 목록은 산출물에 없다. 산출물은 어떤 slice도 실행되기 전에 쓰이고 승인은 실행 중에 차례로 정해지므로, 승인 결과는 slice task 상태(`budget_exhausted`/`review_budget_exhausted`)에서 읽는다.
- decoder(`record.DecodeScope`)는 경로와 blob을 스냅샷 manifest와 대조하고, 모듈별 수, `covered_by_overview`와 `offered`의 일치, 파일 중복을 검사한다.

### slice 요청과 기록

- slice 요청은 overview의 요청 생성기, 역할 계약 문자열, 팩 파일을 그대로 쓴다. 지침 hash가 overview와 같고(`modules-worst-screened` 고정값), `prompts/`는 바뀌지 않는다.
- slice investigate는 overview처럼 수락된 environment·documents 결과를 받고, 코퍼스는 그 slice에서 `planned`인 파일(첫 50줄, 파일당 2,400바이트, 합계 8개·12,000바이트)이다. 읽기 필터와 선별을 다시 적용하고, 그새 읽을 수 없게 된 파일은 건너뛴다. `modules`는 slice의 모듈 디렉터리이고 `files_outside_review_context`를 항상 남긴다. upstream은 `review.environment`, `review.documents`, `review.scope`다.
- slice verify는 자기 slice investigate의 후보만 받는다(interpret 후보 없음). upstream은 `review.documents`, `review.investigate.sNNN`, `review.scope`다.
- 기록은 schema 0.3이다(아래 [검토 기록](#검토-기록)). [investigate 예시](../examples/review-r1/slices/investigate.s001.json) · [verify 예시](../examples/review-r1/slices/verify.s001.json)
- 다섯 언어가 모두 범위에 있는 최악 사례에서도 slice 요청은 40,000바이트 안이다(`TestSliceRequestSizeWorstCaseAllLanguages`).
- 보고서(builder 6)는 0.3 기록과 범위 산출물을 읽는다. slice 후보는 `review_sNNN_<후보>` 발견이 되고, 코드 분석 절의 [모듈별 검토 장부](REPORT.md#모듈별-검토-장부)가 모듈마다 모델에 보낸 파일과 보내지 못한 이유(상한·비밀값·계획 밖 등)를 센다. 모델에 보낸 범위는 검증된 범위가 아니다. `ai_review`는 필수 역할만 본다.

## 모델 입력의 비밀값 선별

검토 역할은 정적 분석 계획의 비밀값 분석 task가 끝난 뒤 실행된다. 각 역할은 시작할 때 그 결과를 읽는다([`screen.go`](../internal/worker/review/screen.go)). 비밀값 분석기 자체는 [분석기](ANALYZERS.md), 비밀정보 정책은 [보안](SECURITY.md)에 있다.

| 비밀값 task 결과 | 처리 |
|---|---|
| 계획되지 않았거나 미실행(`unsupported`/`not_run`) | 선별 없이 보낸다. 기록과 보고서에 `model_input_not_secret_screened`를 남긴다. 조용히 넘어가지 않는다 |
| 실행됐지만 쓸 수 없음(실패, 시간 초과, 취소, 부분 파싱, 산출물 누락·무효) | fail closed. 역할을 `skipped_policy`(`secret_screen_failed`)로 끝내고 어떤 요청도 보내지 않는다 |
| 완전한 파싱으로 성공 | 보고된 파일을 모든 발췌(코드 문맥, 문서, 관찰 창, 검증 확장)에서 뺀다. `secret_files_withheld`를 남긴다 |

- 모든 발췌는 같은 읽기 경로(`readLines`, review-v1은 `buildScreenedCorpus`)를 지나므로 새 입력 종류도 같은 선별을 받는다.
- 이와 별도로 민감값 패턴(`record.Sensitive`: 개인 키 블록, `sk-`·`gh?_` 형식 토큰, Authorization 헤더 값, 따옴표로 감싼 api_key·password·client_secret 값)에 걸린 파일은 발췌하지 않는다. 이 패턴은 완전한 비밀 탐지를 보장하지 않는다.
- 모델이 쓴 서술도 같은 패턴에 걸리면 기록에 남지 않는다(`record.SafeText`).

## 출력 검사

- 출력은 `workspace_proof`와 `result`를 가진 JSON 객체 하나여야 한다. `result`는 역할 계약의 엄격한 JSON이다. 계약의 목록 key는 모두 배열로 있어야 하고 알 수 없는 필드는 거부한다(`output_schema_invalid`).
- 그 호출에 준 근거·관찰 ID만 인용할 수 있다. 오류 코드: `evidence_id_unknown`, `evidence_id_duplicate`, `evidence_kind_invalid`, `evidence_missing`, `evidence_list_too_long`, `observation_id_unknown`, `evidence_not_at_observation`, `code_evidence_missing`.
- 문서 규칙의 적용 경로를 상위 디렉터리나 저장소 전체로 넓히면 거부한다(`scope_generalized`, `scope_unknown_path`, `scope_invalid`). 충돌의 두 쪽은 서로 다른 문서를 인용해야 한다(`conflict_same_document`).
- 실행·재현·테스트 통과를 주장하는 서술은 거부한다. 호출 오류 코드는 `execution_claim`이고, 단계 기록에는 `execution_claim_rejected` 한계가 남는다. 이 검사는 정규식 휴리스틱이다. 놓친 주장이 있어도 상태·재현·통계는 구조화 기록에서만 정하므로 모델 서술이 판정을 바꾸지 못한다.
- review-r1 서술 필드는 JSON 인코딩 400바이트 이하다(지침은 120자 이하를 요구한다). 기록에 남는 모델 서술은 2,400바이트 이하이고 민감값 패턴에 걸리지 않아야 한다.
- 환경 메모는 플랫폼 판정을 다시 쓰거나 올리지 못한다. 판정 필드를 붙인 출력은 거부한다.

## 후보 검증과 근거 규칙

- `supported`는 code 또는 test 근거 ID 하나 이상과 성립 조건 하나 이상을 요구한다.
- `refuted`는 반증 근거 ID와 주장이 성립하지 않는 조건을 요구한다. 호출자가 자원을 정리하는 등 유효한 반대 근거가 있으면 후보는 기각되고, 원래 후보와 근거는 보존된다.
- 그 밖에는 `inconclusive`이고 해소하지 못한 가정을 남긴다. 확인하지 못한 조건은 성립 조건이 아니라 남은 가정으로 적는다. 성립 조건이 모두 가정인 후보는 지지할 수 없어 판단 보류가 된다.
- 모델이 `reproduced`를 내면 플랫폼은 `inconclusive`로 내리고 `reproduction_claim_rejected`를 남긴다. 모델이 제출한 실행 결과와 실행 식별자는 증거로 등록할 수 없다. 재현은 플랫폼의 실행 산출물로만 성립한다.
- 심각도는 `high`·`medium`·`low`·`info`만 받는다. `critical`은 실행 근거나 사람의 판단이 필요하므로 모델이 줄 수 없다(`severity_not_assignable`).
- 범주는 `correctness`, `error_handling`, `concurrency`, `resource_lifecycle`, `data_integrity`, `api_contract`, `security`, `maintainability`, `performance`, `test_gap` 중 하나다.
- 후보마다 정확히 한 판정이 있어야 한다. 누락·중복 항목, 모르는 후보 ID는 거부한다. 다음 확인 사항(1~3개)은 명령이 아니라 읽기·검토 단계다.
- 끝내 검증되지 않은 후보는 플랫폼이 `unreviewed`로 표시한다(`verification_quarantined`, `verification_not_completed`, `verification_budget_exceeded`).
- 파일·행·해시 검증은 인용의 출처를 확인할 뿐이다. 인용을 해석한 모델 문장의 의미적 정확성은 [평가](EVALUATION.md)의 판정으로만 잰다.

## 복구 경로

review-r1의 모든 역할에 적용한다. 단계당 호출은 최대 3회(`record.RoleMaxCalls = 3`)다.

1. **형식 복구.** 출력이 JSON이지만 계약·근거 검사에서 거부되면, 고정 오류 코드만 담은 복구 요청을 한 번 보낸다. verify는 거부된 후보, 허용된 근거와 오류 코드만 다시 보낸다.
2. **JSON이 아닌 응답의 재시도.** JSON이 아닌 응답은 항목에 대해 아무 정보도 주지 않는다. 형식 복구 기회를 쓰지 않고 한 번 더 요청한다. 재시도 요청은 같은 입력에 복구 규칙과 오류 코드 `output_not_json`을 담은 `repair` 블록을 붙인다. 앞서 복구 요청이 있었으면 그 오류 코드(verify는 후보별 오류)를 유지하고 `output_not_json`을 더한다.
3. **끝.** 두 번 연속 JSON이 아니거나 두 번 거부되면 2회에서 끝난다. 그래도 틀리면 task는 `failed`이고 모델 내용은 격리(저장하지 않음)된다. 탐색 사실과 도구 관찰은 남는다.
4. **관찰 ID만 틀린 verify 항목.** 항목의 오류가 `evidence_id_unknown` 하나뿐이고 범위 밖 ID가 모두 그 요청에 플랫폼이 넣은 관찰 ID일 때만, 그 ID를 빼고 같은 검사를 다시 한다. 통과하면 받아들이고 그 호출의 `output_errors`에 `evidence_id_dropped`를 남긴다. 게시되는 판정은 범위 안의 근거만 인용한다.
   - 요청에 없던 ID(지어낸 `ev_…` 등)가 하나라도 있으면 원래 오류로 거부하고 형식 복구로 보낸다.
   - ID를 빼고 통과하지 못하면 원래 오류로 거부한다. 관찰 ID만으로 지지한 항목은 코드 근거가 없어 거부된다.
5. **후속 호출 실패(verify).** 이미 수락한 판정이 하나 이상 있고, 형식 복구나 재시도 호출이 형식 이외의 이유(timeout, 거절, 모델 오류)로 실패하면 수락한 판정을 버리지 않는다. 남은 후보는 격리하고 `verification_call_failed` 한계를 남긴다. task는 `failed`(`review_output_incomplete`)이고 실패 사유는 그 호출의 `outcome`에 남는다. 수락한 판정이 없거나 취소이면 단계 전체가 실패한다.
6. **verify 결과.** 일부만 유효하면 사용 가능한 판정을 보존하고 task는 `failed`(`review_output_incomplete`)다. 사용 가능한 판정이 전혀 없으면 `failed`(`review_output_invalid`)다.

- 횟수 제한 없는 재시도는 하지 않는다. 단계마다 호출이 하나 늘 수 있어 최악의 비용과 시간이 그만큼 늘어난다.
- review-v1 verify는 이 경로를 쓰지 않는다. 형식 복구가 한 번이고 최대 2회 호출이며, 복구 호출이 실패하면 task가 실패한다.

## 실패와 task 결과

| 상황 | task 상태(사유) |
|---|---|
| 모델 전송 미승인 | `skipped_policy`(`model_source_not_approved`) |
| 비밀값 선별 결과를 쓸 수 없음 | `skipped_policy`(`secret_screen_failed`) |
| slice에 보낼 파일이 없음 | `skipped_policy`(`no_review_scope`) |
| 호출 상한으로 slice 거절 | `budget_exhausted`(`review_budget_exhausted`) |
| 호출 계수를 쓰거나 읽지 못함 | `failed`(`review_call_count_failed`) |
| 앞 역할 미수락 | `skipped_policy`(`dependency_failed`) |
| 모델 미설정 | `unsupported`(`model_not_configured`) |
| 지원하지 않는 OpenCode 버전 | `unsupported`(`opencode_version_unsupported`) |
| 예상 밖의 도구 호출·권한 이벤트 | `skipped_policy`(`policy_blocked`) |
| 시간 초과 | `timed_out`(`model_timeout`) |
| 취소 | `cancelled` |
| 입력을 만들 수 없음·스냅샷 무결성 불일치 | `failed`(`review_input_unavailable`) |
| 요청 한도 초과 | `failed`(`review_input_too_large`) |
| 보내지 못한 verify 후보 | `failed`(`review_input_incomplete`) |
| 출력 무효(복구 뒤에도) | `failed`(`review_output_invalid`), v1 context·investigate는 `review_evidence_invalid` |
| verify 일부 판정만 유효 | `failed`(`review_output_incomplete`) |
| 지침 팩을 읽지 못함 | `failed`(`review_pack_invalid`) |
| 기록을 쓰지 못함 | `failed`(`review_record_failed`) |

실패한 모델 호출도 가능한 범위에서 시도 번호, 모델·OpenCode 버전, 지침 팩·요청 digest, 제공된 사용량을 보존한다. 상태와 부분 완료 여부는 워커가 결정한다. 통신 단절이나 모델 사용량 미제공은 미확인으로 남는다.

## OpenCode 어댑터 계약

모델은 관리형 로컬 OpenCode CLI로 호출한다([`client.go`](../internal/worker/opencode/client.go), [`executor/opencode.go`](../internal/worker/executor/opencode.go)). 실행 규칙(argv, 제한시간, 프로세스 트리 정리)은 [실행](EXECUTION.md)을 따른다. 원격 OpenCode attach나 별도 serve 연결은 구현하지 않았다. 추가할 때는 인증·버전·취소·workspace marker/digest를 대상 환경에서 검증한다.

- **고정 버전.** 지원 버전은 OpenCode 1.19.5 하나다. 호출마다 먼저 `--version`(8초 이하)을 실행하고 다르면 `opencode_version_unsupported`로 끝낸다. `codeye doctor`의 버전 확인은 모델 비용을 쓰지 않는다.
- **운영자 설정.** 실행 파일은 절대 경로다. 모델은 `opencode/<model>`(OpenCode Zen) 또는, relay 파일이 있을 때만 `codeye-gateway/gpt-6.2-sol`·`codeye-gateway/gpt-6.1-sol`이다. 에이전트의 호출당 제한시간은 75초이고, 설정 가능한 범위는 0 초과 10분 이하다. 설정이 없거나 형식이 틀리면 요청을 보내지 않는다(`model_not_configured`). 공급자 변경은 운영자 선택이다.
- **작업 디렉터리와 workspace digest.** 요청마다 attempt scratch 아래 새 디렉터리를 만든다. 그 안의 `identity.txt`에 snapshot digest, 요청 digest, 무작위 32바이트 nonce에 묶인 확인값을 쓰고 첨부로 준다. 응답의 `workspace_proof`가 이 값과 다르면 `workspace_unreachable`이다. 프롬프트와 선택된 데이터는 표준 입력으로만 전달한다. 요청 digest는 정확한 메시지 텍스트의 SHA-256이다.
- **권한 차단.** `run --pure`로 실행하고, 실행별 HOME/TMP와 플랫폼이 만든 설정(`OPENCODE_CONFIG_CONTENT`)만 쓴다. 설정은 `permission: deny`, 모든 도구 `false`, share·snapshot·autoupdate·LSP·formatter·plugin 끔, 허용 provider 하나다. 프로젝트 설정, 기본 플러그인, 외부 skill, 모델 목록 조회, LSP 다운로드는 환경변수로 끈다. 사용자 설정과 대상 프로젝트 설정을 읽는 경로를 분리한다. 스냅샷 디렉터리는 보호 루트로 넘긴다.
- **응답 수집.** 한 세션·한 메시지의 완료된 응답만 받는다. `tool_use`·`permission` 이벤트는 `policy_blocked`, `error`는 `model_unavailable`, 세션 불일치·순서 위반은 `model_output_invalid`, 완료 사유가 `stop`이 아니거나 잘린 출력(64 KiB 초과)은 `model_output_incomplete`다. `reasoning` 이벤트는 버린다. 공급자 사용량 제한 메시지는 `model_rate_limited`로만 남긴다.
- **relay 파일.** 예산 relay를 쓸 때 ready 파일은 일반 파일이고 그룹·기타 권한이 없어야 하며(Windows 제외) 4,096바이트 이하다. `base_url`은 `http://127.0.0.1:PORT/v1`, token은 32~256자다. OpenCode에는 이 임시 로컬 token만 넘기고 실행 기록에서 가린다. 상위 API 키는 relay만 가진다. relay 운영은 [평가](EVALUATION.md)에 있다.
- **취소.** 컨텍스트 취소·제한시간은 프로세스 트리 정리까지 확인한다. 정리를 확인하지 못하면 `recovery_required`로 남기고 작업 디렉터리를 지우지 않는다. 이미 전송된 공급자 추론은 취소 뒤에도 청구될 수 있다. 공급자 측 생성의 확정 중단은 보장하지 않는다.
- **직접 확인.** `codeye model probe --executable ABSOLUTE_PATH --model PROVIDER/MODEL [--relay-file ABSOLUTE_PATH] [--timeout 75s]`는 합성 입력으로 실제 추론을 한 번 요청하고 `{model, opencode_version, verified, reason, cleanup}`을 출력한다. 비용이 드는 명령이다.
- 네트워크 차단의 OS 수준 강제는 배포 환경에 의존한다([보안](SECURITY.md)).

## 모델에 보낼 수 있는 소스

- 모델 전송은 노드 운영자가 로컬 소스 루트마다 허용한다. `codeye agent --model-source-root ROOT_ID`(반복 가능)이고, 개인 설정에서는 `source_roots[].model_review: true`가 같은 플래그가 된다(`agent.model` 필요).
- 실제 모델을 쓰려면 에이전트에 `--opencode ABSOLUTE_PATH --model PROVIDER/MODEL`을 준다. 예산 relay를 쓸 때만 `--model-relay-file ABSOLUTE_PATH`를 더한다.
- 일반 소스 매핑(`--source-root`)과 모델 전송 허가는 별개다. 허가가 없는 루트의 run은 모든 검토 task가 `model_source_not_approved`이고 정적 분석 결과만 남는다.
- Git 소스는 모델에 보낼 수 없다. Git 취득 run의 검토 task는 항상 `model_source_not_approved`다. Git 소스의 개별 모델 전송 승인은 설계 계약(미구현)이다.
- run이나 스냅샷의 어떤 내용도 이 허가를 올리지 못한다. 저장소 코드 실행 허가(`--repo-code-source-root`)와도 별개다([분석기](ANALYZERS.md)).

## 검토 기록

검토 task마다 산출물 하나를 role `other`, media type `application/vnd.codeye.review+json`으로 올린다. 크기는 256 KiB 이하다. 보고서 registry는 `schema_version`으로 decoder를 고른다. [스키마](../schemas/review-record.schema.json) · [v1 예시](../examples/review-record.json) · [r1 예시](../examples/review-r1/verify.json)(scripted caller로 만든 다섯 단계 기록 중 하나)

공통 규칙:

- 메타데이터와 모델의 판정문·근거 참조만 담는다. 발췌의 원문(`text`), 원시 프롬프트, 내부 추론, 프로세스 출력은 저장하지 않는다.
- 근거(`evidence`)는 `id`, `path`, `sha256`, `start_line`, `end_line`, `kind`, 선택 `origin`과 `symbol`을 가진다. `kind`는 0.1이 code·test·document·config, 0.2가 code·test·document·manifest(환경 요구 원문)다. `origin`은 0.1이 context·caller·test, 0.2는 여기에 observation(해석 창)을 더한다. document·manifest 근거에는 origin이 없다.
- decoder(`record.Decode`, `record.DecodeRole`)는 기록을 그 attempt의 run ID·attempt ID·snapshot manifest digest·단계와 대조한다. 근거마다 ID가 (snapshot digest, 경로, blob SHA-256, 행 범위)에서 다시 계산되는지, 경로가 manifest에 있고 blob이 같은지 확인한다. 모든 참조는 그 기록의 근거 목록 안에 있어야 한다. 허위 경로·행·blob·근거 ID, 다른 run·attempt·snapshot의 기록은 거부된다.
- 사용량이 없으면 `null`이다. 요청을 만들기 전에 실패하면 요청 digest가 빈 문자열일 수 있다.
- 실패한 기록은 시도한 호출, 준 입력, 앞 단계 상태를 남기고 모델 내용은 격리한다. 수락됐다가 버려진 호출의 `outcome`은 `result_discarded`다. verify가 실패하면 후보는 모두 `unreviewed`로 남는다.
- 보고서가 검토 완료로 판단하려면 DB task 성공과 검증된 역할 기록을 함께 확인한다.

### schema 0.2(review-r1)

- 단계마다 하나이고 자기 호출만 담는다. 필드는 `schema_version`, `pack`(id·version), `snapshot_manifest_digest`, `run_id`, `attempt_id`, `stage`, `outcome`(`accepted`·`failed`), `failure_reason`, `upstream`, `evidence`(최대 40), `observations`(최대 16), 역할별 내용, `limitations`, `calls`다.
- 역할별 내용: environment `environment_notes`, documents `contracts`·`conflicts`·`questions`·`instruction_notes`, investigate·interpret `candidates`(`investigate_1`~`4`, `interpret_1`~`4`), interpret `interpretation_limits`, verify `candidates`(앞 단계 후보 원문)와 `verifications`.
- `upstream`은 입력 하나마다 `source`, `state`(`accepted`·`skipped`·`failed`·`unavailable`), `reason`, 읽은 산출물 바이트의 `sha256`을 가진다. 탐색·도구 산출물과 앞 단계 기록 모두 이렇게 묶는다.
- `calls[]`: `role`, `model`, `opencode_version`, `pack_id`, `pack_version`, `pack_sha256`, `pack_files`, `request_sha256`, `request_bytes`, `usage`, `attempt`(1~3), `outcome`(`accepted`, `output_rejected`, `result_discarded` 또는 실패 사유 코드), `output_errors`.
- 모듈 ID는 `<ecosystem>:<path>`, 요구 ID는 `<kind>@<선언 파일 경로>` 형식이다.

### schema 0.3(review-r1 slice)

- slice 기록(`review.investigate.sNNN`, `review.verify.sNNN`)만 쓴다. 0.2에 필수 `slice`(`key` `s001`~`s032`, `modules` 1~8개)를 더한 것이다. 단계는 investigate·verify뿐이다.
- slice investigate의 upstream은 `review.scope`도 이름으로 가질 수 있고, slice verify는 `review.investigate.sNNN`과 `review.scope`를 가진다. slice verify의 후보는 investigate 후보뿐이다.
- 0.2 기록에는 `slice`가 없고 `review.scope`·slice upstream을 가질 수 없다. `slice`는 생략 가능 필드라 0.2 기록의 바이트는 그대로다(`TestRolePackExampleRecords`).
- decoder는 기록의 slice key와 단계가 task key와 같은지 확인한다(`record.Expect.Slice`).

### schema 0.1(review-v1)

- 각 역할 기록에 앞 역할 호출 이력이 누적된다. 합산할 때는 현재 역할의 호출만 센다.
- `calls[].attempt`는 1~2다. `outcome`과 시도 번호가 없는 이전 성공 기록도 읽는다.
- 이전 기록(Go만 있는 코퍼스)은 그대로 읽힌다.

## 보고서 연결

보고서 규칙의 전체는 [보고서](REPORT.md)에 있다. 검토 기록과 직접 맞물리는 규칙은 다음과 같다.

- 필수 검토 역할은 계획된 `review.*` task의 `required_for_verdict`로 정한다. 하나 이상이고 모두 수락됐을 때만 `ai_review: performed`, 하나도 없으면 `not_performed`다. 필수가 아닌 역할은 `coverage.ai_review.optional_tasks`에 결과와 사유를 적고 수행으로 세지 않는다. review-r1 역할 key가 없는 계획은 review-v1의 세 역할로 해석한다.
- `coverage.review_calls`는 0.2·0.3 기록에서 `pack_id`·`pack_version`을, builder 6부터 호출이 든 task(`task_key`)를 싣는다. 상한은 207(호출 상한의 최댓값)이다. `llm_exposed_files`는 slice 기록이 보낸 경로도 센다.
- 앞 단계·탐색·도구 산출물의 SHA-256, 관찰 ID, verify 후보와 앞 단계 후보가 맞지 않으면 그 단계를 `review_lineage_mismatch:<task>`로 제외한다. 같은 발췌가 앞 단계마다 다른 출처 표시(`context`·`observation`)로 올 수 있으므로 어느 한 표시와 같으면 받는다. 계획과 다른 팩의 기록은 `review_pack_mismatch:<task>`로 제외한다.
- 모델이 서술한 숫자를 run 상태나 통계로 쓰지 않는다. 도구 관찰과 모델 후보는 서로 다른 산출물이다.

## 시험

- 회귀: `go test -race ./internal/worker/review/... ./internal/worker/run`. 예시와 부정 입력: `python3 scripts/validate_bundle.py`. 일반 CI는 유료 모델을 호출하지 않으며 scripted caller만 쓴다. scripted caller 결과는 플랫폼 검사의 근거이고 지침 품질의 근거가 아니다.
- 실모델 시험은 명시적으로 환경을 준 경우에만 실행한다. `CODEYE_TEST_OPENCODE_BIN`과 `CODEYE_TEST_REVIEW_RELAY`(필요하면 `CODEYE_TEST_REVIEW_MODEL`, `CODEYE_TEST_REVIEW_LEDGER`, `CODEYE_TEST_OPENCODE_MODEL`)를 주고 `go test -v -count=1 -run '^TestLiveVerifier$' ./internal/worker/review`처럼 하나씩 실행한다. 대상은 `TestLiveQualityRoles`, `TestLiveVerifier`(`internal/worker/review`), `TestLiveOpenCode`, `TestLiveInFlightCancel`(`internal/worker/opencode`)이다. 비용이 든다. 시험 체계는 [시험](TESTING.md)에 있다.

## 한계

- 발췌는 파일 첫 줄부터다. 진단이 없는 파일의 뒷부분과 함수 경계는 보이지 않는다.
- 호출자 확장은 Go만 한다. Go가 아닌 후보의 파일 간 결함은 발췌에 들어간 범위에서만 보인다.
- 실행·재현 주장 거부와 민감값 패턴은 정규식이다. 다른 표현을 모두 잡지 못한다.
- 미완료가 사라진다는 보장은 없다. 끝내 검증되지 않은 후보는 `unreviewed`로 격리하고 task는 실패한다.
- `roleRules`는 "근거·관찰 ID"를 인용하라고 하고 verify 계약은 "근거 ID"만 말한다. 이 어긋남은 구조(관찰 ID 처리 규칙)로 막았고, 문구 정리는 다음 지침 개정에서 독립 작성자가 판단한다.
- 모델의 자신감과 서술은 측정된 정확도가 아니다. 품질은 [평가](EVALUATION.md)의 판정과 지표로만 말한다.

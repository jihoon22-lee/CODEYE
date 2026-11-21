# 모델 검토

모델 검토는 고정 스냅샷에서 정적 분석 뒤에 실행되는 제한된 모델 작업이다. 모델은 플랫폼이 고른 발췌와 근거 ID만 받는다. 모델의 출력은 후보와 판정 서술일 뿐이다. task 상태, 재현 여부, 통계, 보고서 수치는 워커와 서버가 구조화 기록으로 정한다. 이 문서는 역할, 지침 팩, 입력 선택과 크기 한도, 비밀값 선별, 출력 검사와 검증 규칙, 복구 경로, OpenCode 어댑터, 소스 정책, 검토 기록 형식을 정한다.

- 구현 위치: [`internal/worker/review`](../internal/worker/review), [`internal/worker/review/record`](../internal/worker/review/record), [`internal/worker/opencode`](../internal/worker/opencode), [`prompts/review`](../prompts/review)
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

## 검토 지침

검토 기준은 하나다(A52). 지침은 저장소의 [`prompts/review`](../prompts/review/pack.yaml)에 있고 빌드 시점에 실행 파일에 묶인다(`embed`). 대상 저장소가 지침이나 digest를 제공할 수 없고, run 요청·프로필·API·노드 설정으로 다른 지침을 고를 수 없다.

| `pack.yaml` id·version | 기록 schema | 역할 |
|---|---|---|
| `review` 2.5.2 | `0.2`, slice `0.3`, 계약 후속 검토 `0.4`, HTTP 경로를 고른 문서 `0.5` | environment, documents, investigate, interpret, verify, contract |

- 지침 개정은 이 지침 하나에 한다. 개정의 검증 절차는 [평가](EVALUATION.md)에 있다.
- 보고서는 Go가 만들므로 종합·보고 역할에 모델을 호출하지 않는다.
- `scripts/validate_bundle.py`가 `pack.yaml`의 id·역할·언어 지침과 예시 기록의 역할별 digest를 확인한다.

### 팩 구성과 digest

- 지침은 공통 지침 `00-core.md`, 역할 지침 여섯, 영역 지침 `domains.md`, 언어 지침 다섯(`languages/cpp.md`·`go.md`·`javascript.md`·`python.md`·`rust.md`)으로 구성한다.
- 한 역할의 지침 텍스트는 공통, 역할, 영역, 고른 언어 지침 순으로 이어 붙인다.
- 언어 지침은 스냅샷 scope 안에 소스가 있는 생태계만 생태계 이름 순서로 붙는다. 판별은 분석기 팩의 공백 계획과 같은 `pack.InScope`다([분석기](ANALYZERS.md)). 소스 확장자로 세고, data·vendor 디렉터리는 빼며, build 파일만으로는 생태계가 들어오지 않는다. 지원 생태계가 없으면 언어 지침을 붙이지 않는다.
- 역할별 digest는 SHA-256이다. 입력은 `pack.yaml`과 실제로 고른 파일이고, UTF-8 상대 경로 오름차순으로 `경로 NUL 내용 NUL`을 이어 해시한다. 따라서 같은 역할도 생태계 조합마다 digest가 다르고, `pack.yaml`의 version·status가 바뀌면 모든 역할 digest가 바뀐다.
- 호출 기록마다 팩 id·version·역할 digest(`pack_sha256`)·고른 파일 목록(`pack_files`)이 남는다. 보고서는 실제 사용한 팩을 표시한다.
- digest는 run 생성 시 계산한다. 저장소가 제공한 digest를 믿지 않는다.
- 저장소 `.gitattributes`(`* text=auto eol=lf`)가 모든 checkout을 LF로 고정한다. CRLF로 바뀐 checkout은 지침 바이트가 달라져 digest와 요청 크기가 OS마다 달라진다.

### 지침 작성 규칙

- 지침 파일에는 평가·시험 사례의 내용을 쓰지 않는다. 사례 ID, 작업 ID, fixture 경로와 식별자, 특정 문서·재시도 횟수·버전 조합이 여기에 해당한다. 예시가 필요하면 사례 없는 규칙으로 쓴다.
- [`prompts/review/bundle_test.go`](../prompts/review/bundle_test.go)가 사례 ID·fixture 이름을 검사한다. 같은 뜻을 다른 말로 쓴 문장은 이 검사로 잡지 못하므로 지침 검토에서 확인한다.
- 언어 생태계마다 언어 지침이 있어야 한다(시험으로 고정). 의존성·타입·헤더가 없어 생긴 진단은 결함이 아니라 분석 공백으로 다룬다. 지침에 도구 규칙 ID를 쓰지 않는다.
- 지침 작성자는 보류 평가 결과와 보류 사례를 읽지 않는다. 범위는 [평가](EVALUATION.md)에 있다.

## 역할과 산출물

워커는 deep-review 계획에서 탐색 task(`env.discover`, `docs.discover`)와 정적 분석 task 뒤에 기본 역할 다섯을 둔다. 계약 후속 검토를 계획하면 모듈 검토까지 끝난 뒤 실행한다. 탐색 산출물은 [구조](ARCHITECTURE.md)에 있다. 한 역할의 출력은 다음 역할에 넘길 자료다. 역할 수가 모델 호출 수를 정하지 않는다. 환경 버전 비교, 문서 존재 확인, 정적 검사 실행, 보고서 게시는 모델 없이 플랫폼이 처리한다.

| task | 단계 | 판정 필수 | 호출 없이 끝나는 경우 | 입력 | 출력 |
|---|---|---|---|---|---|
| `review.environment` | `environment` | 아니오 | 선언된 요구가 모두 `satisfied`면 `no_environment_gap`. 탐색 산출물을 쓸 수 없으면 `dependency_failed` | 차이가 있는 모듈의 요구·근거 ID·노드 관측·판정 | gap 모듈마다 영향 메모 하나. 플랫폼 판정은 바꾸지 못한다 |
| `review.documents` | `documents` | 계획 시 범위 안에 안내 문서가 있을 때만 | 문서 없음 `no_documents`(필수 아님), 선택된 문서 없음 `documents_not_selected`, `dependency_failed` | 선택 문서 발췌 | 적용 경로가 있는 계약·충돌·질문, 지침성 문구 메모, 근거가 있는 문서 품질 판단 |
| `review.investigate` | `code_review` | 예 | — | 수락된 문서·환경 결과, 모듈별 코드·테스트 발췌. 원시 도구 진단은 넣지 않는다(REQ-009) | 후보 `investigate_N`(0~4개), 구현 근거가 있는 강점, 전체 조사에서 문서 계약과 코드의 대조 |
| `review.interpret` | `code_review` | 아니오 | 해석할 도구 관찰이 없으면 `no_tool_observations` | 관찰 ID·규칙·위치와 주변 줄 창 | 관찰에 연결한 가설 `interpret_N`(최대 2) 또는 해석 한계 |
| `review.verify` | `code_review` | 예 | investigate·interpret가 모두 수락되지 않으면 `dependency_failed`. 후보가 없으면 호출 없이 성공(`no_candidates_to_verify`) | 수락된 두 역할의 후보, 문서 계약, 관찰, 발췌와 호출자·테스트 확장 | 후보별 판정 |
| `review.contract` | `code_review` | 아니오 | 유효한 선택과 새 구현 줄이 없으면 `no_review_scope`, 선행 기록이 없거나 맞지 않으면 `dependency_failed`, 호출 상한이면 `review_budget_exhausted` | 수락된 문서와 앞선 미확인 판정, 다시 읽은 범위 안의 발췌 | 같은 계약에 대한 대조 한 건 |

역할 지침과 허용 입력은 다음과 같다.

| 지침 | 허용된 입력 | 다음 단계에 남길 결과 |
|---|---|---|
| [환경 분석](../prompts/review/environment.md) | 모듈 범위, 선언 버전, 노드 관측값과 출처 | 검사별 요구·관측·불일치·미확인과 그 영향 |
| [문서 검토](../prompts/review/documents.md) | 경로별 문서·설정·선택 첨부와 제한된 코드 발췌 | 적용 경로, 문서상 계약, 상충하는 근거, 미확인 질문 |
| [독립 코드 분석](../prompts/review/investigate.md) | 모듈 계약, 환경 한계, 코드·테스트 발췌 | 원인·발생 조건·영향·반증 가능성을 가진 후보 또는 빈 목록 |
| [정적 진단 해석](../prompts/review/interpret-diagnostics.md) | 어댑터가 발급한 관찰 ID, 버전·범위·파싱 상태와 관련 발췌 | 원시 관찰에 연결한 영향 가설 또는 해석 한계 |
| [후보 검증](../prompts/review/verify.md) | 이전 후보 원문과 ID, 호출부·테스트·반대 근거 | 지지·기각·판단 보류의 이유, 유효·무효 조건, 추가 확인 사항 |
| [문서 계약 후속 검토](../prompts/review/contract.md) | 문서 약속과 원래 판단, 새 구현과 관련 호출자·테스트 발췌 | 적용 범위와 문서 근거가 같은 대조 또는 미확인 |

- 도구 진단 해석은 독립 조사와 별도 입력으로 수행하고 원시 메시지·규칙·위치를 보존한다. 도구의 실행 상태, finding 수, 심각도를 다시 말하지 않고, 진단이 없다는 사실을 깨끗한 코드로 서술하지 않는다.
- 출력 개수 한도: documents는 계약 6, 충돌 3, 질문 4, 지침성 문구 메모 3개까지. interpret는 가설 2, 한계 3개까지이고 합해서 하나 이상. investigate는 후보 4개와 강점 3개까지다. 문서 검토는 명확성·완전성·실행 가능성·일관성에 관한 근거 있는 판단을 4개까지, 전체 조사 역할은 고른 문서 계약과 코드의 대조를 6개까지 기록한다. 대조는 계약 순번과 그 문서 근거에 묶인다. 일치·부분 일치·불일치에는 해당 경로의 구현 코드 근거가 필요하고, 미확인은 근거 부족을 설명한다. 보고서에서 대조가 없는 수락 계약은 미확인으로 표시한다.
- 문서 응답의 계약·질문·문서 품질 항목은 각각 서로 다른 근거 ID를 1~4개 인용한다. 지침성 문구 메모와 충돌의 각 편은 각각 1~3개다. 요청은 가장 직접적인 한 근거를 우선하도록 안내하고, 형식 복구 오류 코드에는 문제가 있는 근거 ID 필드 이름을 함께 표시한다.
- 문서 검토는 README·사용 안내·공개 인터페이스에서 사용자가 확인할 수 있는 동작을 우선 고른다. 하나의 계약이 한 조건과 관찰 가능한 결과를 설명하며, 지원 범위와 예외는 별도로 판정할 수 있을 때 각각 기록한다. 설계에서 읽은 기대에는 구현을 확인할 질문을 연결한다.
- 조사 역할은 사용자 진입점에서 결정적인 분기를 거쳐 결과가 전달되는 근거를 대조한다. 계약이 함수 자체의 동작으로 한정되면 그 함수의 구현을 대조한다. 확인하지 못한 호출 경로나 결과는 미확인으로 남긴다. 검토에서 읽은 테스트 단언은 해당 조건의 보조 근거다.
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
| `secret_screen_failed` | 이전 worker만 쓴 사유: 비밀값 분석이 실행됐지만 결과를 쓸 수 없어 검토를 건너뜀. 지금은 미선별로 검토한다(아래 비밀값 선별) | 아니오 |
| `no_review_scope` | slice의 파일을 overview가 이미 보냈거나 모두 보류·읽기 불가([모듈 단위 검토 분할](#모듈-단위-검토-분할)) | 아니오 |

필수 여부는 계획 시 정한다. 문서가 없어 생략한 문서 역할을 모델 검토 성공으로 세지 않는다. 필수 모델 작업이 실패했으면 도구 결과가 남아도 검토 전체를 완료로 올리지 않는다. 진행 단계 판정에서 이 생략을 어떻게 셈하는지는 [구조](ARCHITECTURE.md)에 있다.

## 검토 입력 선택

모든 발췌는 고정 스냅샷의 manifest 항목(크기·SHA-256)과 대조해 읽는다. 512 KiB를 넘는 파일, UTF-8이 아니거나 NUL을 포함한 파일, 민감값 패턴에 걸린 파일, 비밀값 선별이 보류한 파일은 발췌하지 않는다. 발췌는 원문의 줄 단위다. 근거 ID는 manifest digest, 스냅샷 상대 경로, blob SHA-256, 행 범위에서 결정된다(`record.ExcerptID`).

한도는 발췌 텍스트를 JSON으로 인코딩한 바이트로 센다(`encoding/json`은 `<`·`>`·`&`를 6바이트로 바꾼다). 상수는 [`inputs.go`](../internal/worker/review/inputs.go)에 있다.

### 종류 판별과 제외 디렉터리

- 일반 발췌에서 code·test·config·document 종류는 경로로 정한다. worker와 보고서 decoder가 같은 규칙([`record/source.go`](../internal/worker/review/record/source.go))을 쓴다. code는 분석기 팩의 `pack.SourceFiles`와 같은 소스 확장자다. test는 언어별 이름·디렉터리 관례, config는 고정 manifest 목록이다. 공개 API 페이지가 참조하는 Python docstring은 `document` 종류를 받고 별도 출처 경로를 기록한다.
- 테스트 경로: Go `*_test.go`. Python `test_*`·`*_test`·`conftest.py`. JS/TS `*.test.*`·`*.spec.*`. Rust `tests.rs`. C/C++ `test_*`·`*_test`·`*_tests`·`*_unittest`·`*-test`. Go가 아닌 언어는 `test`·`tests` 디렉터리 아래 파일도 테스트다(JS/TS는 `__tests__`도, Rust는 `tests`만).
- manifest(config): go.mod, pyproject.toml·setup.cfg, package.json·tsconfig.json, Cargo.toml, CMakeLists.txt. `compile_commands.json`은 생성 산출물이고 보통 생성한 머신의 절대 경로를 담으므로 config 후보가 아니다.
- Go가 아닌 파일은 분석기 팩의 data 디렉터리(숨김·testdata·vendor·node_modules, `anypack.DataDir`) 아래에 있으면 후보가 아니다. 어느 깊이든 `build`·`dist`·`target`·`coverage`·`htmlcov`·`site-packages`·`venv`·`__pycache__`·`third_party` 아래 파일도 뺀다. 같은 이름의 소스 디렉터리도 빠진다. 범위 안 소스가 이렇게 빠지면 `source_in_excluded_directories`를 남긴다.
- Go 파일은 이 제외 규칙을 받지 않는다(internal/build 같은 Go 패키지가 흔하다). Go 발췌는 vendor·testdata·`.`·`_`로 시작하는 디렉터리를 건너뛴다.
- 다른 생태계의 파일과 manifest는 그 생태계의 소스가 범위 안에 있을 때만 넣는다(`pack.InScope`). Go 저장소의 package.json·Cargo.toml처럼 build 파일만 있으면 config 발췌가 되지 않는다.
- 확장자 없는 스크립트와 생성 파일 이름(예: `.min.js`)은 판별하지 않는다.

### 코드 발췌(investigate)

- Go 파일은 가장 깊은 go.mod 디렉터리(모듈)로 묶는다. 다른 언어 파일은 같은 생태계 manifest가 있는 가장 깊은 디렉터리로 묶고, 없으면 `.`다.
- 묶음 안의 순서는 진입점, 소스, 테스트이고 같은 순위는 깊이와 경로 순이다.
- 진입점: Go `main.go`. Python `__main__`·`main`·`app`·`cli`·`manage`(`.py`). JS/TS `main`·`app`·`server`·`cli`(소스 확장자)와 `index`. `index`는 모듈 루트(가장 깊은 package.json·tsconfig.json 디렉터리)나 그 `src`에 있을 때만 진입점이다. Rust `main.rs`·`lib.rs`. C/C++ `main`(`.c`·`.cc`·`.cpp`·`.cxx`·`.c++`). 테스트 경로는 진입점이 아니다.
- 순회(round-robin): 각 순회에서 묶음마다 다음 파일 하나를 고른다. 묶음은 생태계 이름 순으로 돈다. 첫 순회가 범위 안의 모든 모듈과 언어를 지난 뒤에 어느 모듈도 두 번째 파일을 얻는다.
- 전체 조사에서는 수락된 문서 계약의 문장과 인용 원문의 관련 구절로 구현·테스트 위치를 찾는다. 명시된 파일 이름, 함수·명령·옵션처럼 코드에 등장하는 식별자, 사용자 오류 메시지에 쓰인 한국어 어휘를 함께 비교한다. 여러 계약에 반복되는 일반 어휘의 가중치는 낮춘다. 코드 경로는 계약의 적용 범위 안에서만 연결한다.
- Python docstring에서 채택한 계약은 인용된 선언의 docstring 뒤에서 결과를 반환하거나 오류를 내는 구현 줄을 우선 찾는다. 이름이 대응하는 pytest 파일의 단순한 숫자 입력 행에서 동일한 입력과 옵션에 따라 다른 기대값이 나오고 한 창에 담기면 그 행을 함께 검토한다. 테스트 매개변수는 정적 기대값이다. 문서 일치·부분 일치·불일치 판정에는 인용한 docstring과 겹치지 않는 구현 근거가 하나 이상 필요하다.
- 계약 범위에 `app`·`src`·`lib` 등 프로젝트 소스가 함께 있으면 릴리스·배포 등에 쓰는 보조 스크립트와 같은 이름의 테스트는 계약 어휘 점수를 낮춘다. 계약에서 해당 파일이나 운영 작업을 직접 지칭하면 일반 점수를 적용한다. 보조 스크립트도 일반 순회의 대상이다.
- 구현과 벤치마크가 공존하면 기본 동작 계약에서 벤치마크 경로의 어휘 점수를 낮추고, 성능 계약과 명시된 경로에는 일반 점수를 적용한다. JavaScript·TypeScript 구현이 있는 범위에서는 런타임 계약에 대한 타입 선언 파일의 점수도 낮춘다. 타입 계약·명시된 경로·타입 선언만 있는 범위에는 일반 점수를 적용한다. `test.js`·`test.ts`·`*.test-d.ts`는 테스트로 분류한다.
- 경로를 먼저 대조하고 고정 스냅샷의 적격 소스·테스트를 최대 192개·4 MiB까지 읽는다. 일반 우선 탐색에서 생성 헤더·부차 경로·minified JavaScript 파일을 제외한다. PR은 변경 파일 안에서만 이 우선 탐색을 한다. 일반 계약의 구현·호출부·테스트는 합쳐서 최대 4개, 각 24줄·1,500바이트 발췌로 보낸다. 발췌를 함수·분기·실행문 등 관련 줄에서 시작하고, 바로 앞에 검증 조건이 있으면 함께 보낸다. 보고서는 모델이 인용한 경우 같은 스냅샷 위치에 코드 발췌를 붙인다.
- PR 이외의 전체 조사에서 인용한 예제의 Go 공개 함수 이름이 수락한 계약에도 정확히 있으면 문서 적용 디렉터리의 Go 패키지를 확인한다. 같은 디렉터리의 비테스트 Go 파일을 모두 검사할 수 있을 때 AST로 생성 래퍼의 인자 전달, 반환값 또는 일부 결과를 버린 뒤의 반환, 한 곳에서 선언한 같은 패키지 함수를 확인한다. 중간 수기 함수의 직접 전달 호출을 한 번 더 따라갈 수 있다. 예제의 리터럴을 전달받은 매개변수로 분기하는 `switch`나 반환 조건을 포함한 구현 창을 먼저 고른다. 탐색은 기존 읽기 한도를 공유하며 한 패키지에 최대 32개·512 KiB를 쓴다. 범위와 비밀값 검사를 적용하고 부차 디렉터리를 건너뛴다. 빌드 조건이 있는 정의, 모호한 정의, 메서드, 다른 패키지는 이 경로에서 제외한다.
- 위 Go 경로는 전체 조사에서 최대 두 개, 발췌는 합계 다섯 개까지다. 생성 파일은 확인한 래퍼의 줄만 발췌하며 장부에는 `generated_file`로 분류하고 고유 파일을 한 번 센다. 발췌 하나를 우선 배정할 관련 테스트가 있으면 함께 보낸다. 같은 파일의 여러 창은 50줄·2,400바이트 한도를 공유하고 전체 조사 요청 한도를 적용한다. 실제 남은 창 사이의 직접 호출을 `scope.direct_calls`로 전달한다. 우선 발췌로 밀린 적격 파일은 범위 계획의 기존 배정 규칙을 따른다.
- 수락한 계약과 인용 원문에 같은 Go CLI 명령과 첫 인자가 나오면, 범위 안에서 유일한 `cmd/<명령>/`의 `package main`을 찾는다. `os.Args[1]` 또는 `os.Args[1:]`를 전달한 인자의 첫 값과 같은 문자열을 비교하는 분기와 그 구현을 우선 발췌한다. 최대 두 번의 직접 인자 전달을 따라가며 같은 패키지의 정의와 스냅샷 줄을 사용한다. 이렇게 찾은 경로에는 기본 발췌 한도를 적용하고 명시하지 않은 QA·벤치마크 스크립트 대신 제품 구현과 관련 테스트를 우선한다. 모호한 분기나 정의, 생성 소스의 경유, 범위 밖 파일, PR에는 이 연결을 만들지 않으며 일반 발췌를 사용한다. 이 연결은 문서 동작을 대조할 위치를 제공한다.
- `cmd/<명령>/README`에서 해당 명령의 단독 실행이나 옵션 실행을 인용한 계약에는 같은 디렉터리의 `main`을 우선 발췌할 수 있다. `main`이 직접 호출하는 같은 패키지의 구현이 하나이고 두 함수의 전체 줄이 발췌 한도에 맞으면 두 함수를 함께 보낸다. 여러 곳에서 같은 구현을 부르면 한 구현으로 센다. 후보가 여럿이거나 구현 창이 한도를 넘으면 발췌 가능한 `main`만 보낸다.
- C/C++의 `#if`·`#ifdef`·`#ifndef`·`#elif`가 계약의 옵션과 어휘로 연결되고 같은 분기 실행문을 발췌에 넣을 수 있으면 전처리 조건부터 보낸다. 조건과 실행문을 함께 검토해 해당 설정이 코드에 적용되는 경로를 확인한다.
- JavaScript·TypeScript 계약이 멤버 접근이나 호출을 이름으로 지칭하면 메서드 본문이나 `Object.defineProperties`의 해당 속성 정의를 찾는다. 테스트가 상대 경로로 같은 모듈의 팩터리나 생성자를 가져오고 그 객체로 같은 동작을 검사할 때, 본문과 단언 한 쌍을 앞의 네 발췌 안에 우선 배정한다. 독립된 호출 직후의 상태 단언을 접근자 읽기나 단언 내부 호출보다 먼저 고른다. 모호한 구현 경로는 일반 탐색으로 넘긴다.
- 이 쌍의 정확한 두 발췌가 요청에 남으면 `scope.member_test`에 멤버 이름과 구현·테스트 근거 ID를 넣는다. 모델은 연결을 읽을 위치로 사용하며 문서 대조의 판정은 모델이 인용한 발췌의 구현 조건과 테스트 단언을 검증해 기록한다. 테스트 실행 여부는 실행 근거로 판단한다.
- Python의 모듈 최상위 `from … import …`, JavaScript·TypeScript의 최상위 상대 경로 named import에서 계약과 관련된 직접 호출을 한 번 따라간다. 같은 스냅샷에 해당 이름의 모듈 최상위 함수 정의가 하나 있고 결과를 반환하거나 오류를 내거나 단언하는 줄까지 발췌에 들어갈 때, 호출부와 정의를 함께 고른다. Python의 상대 모듈과 일반 모듈·`src` 배치, JavaScript·TypeScript의 상대 소스 파일과 `index` 배치를 확인한다. 경로 후보가 여럿이거나 재바인딩·동적 해석·외부 의존성·제외 경로가 걸리면 일반 위치 탐색을 이어간다. 파일마다 최대 16,384토큰, 괄호 깊이 128, 직접 호출 후보 256으로 제한하며 모든 읽기는 위 한도에 포함한다.
- 일반 발췌에서 이미 두 위치를 볼 수 있으면 일반 순회를 따른다. 우선 선택한 두 발췌가 실제 요청에 남으면 `scope.direct_calls`에 발췌 ID 두 개와 함수 이름을 넣는다. 모델은 이 연결을 위치 탐색에 쓰며, 문서 계약 판단과 보고서 인용은 각 발췌의 입력 조건·구현 결과·테스트 단언에 근거한다.
- 남은 선택은 일반 순회를 따른다. 일반 발췌는 파일 첫 줄부터 최대 50줄·2,400바이트다. 전체 합계는 8개·12,000바이트이며 남은 예산이 128바이트 미만이면 멈춘다. 파일 일부만 발췌하면 `excerpt_only`, 고르지 못한 후보 파일이 있으면 `files_outside_review_context`를 남긴다. 토큰 일치는 검토할 위치를 고르는 기준이다. 계약과 구현의 대조는 모델이 제공된 근거를 인용해 판정한다.
- 전체 조사에서 이름이나 경로가 인용 문서와 연결된 짧은 구현의 일부를 먼저 골랐으면 이미 검증한 원문으로 앞쪽의 관련 분기와 반환도 함께 보낼 수 있다. 원문 전체는 50줄·2,400바이트 이내이며 기존 발췌를 모두 둔 상태로 전체 코드와 요청 한도에 들어가야 한다. 이 파일을 참조하는 직접 호출·테스트 연결이나 다른 발췌가 있으면 기존 줄 범위를 유지한다. 원문 전체를 보낼 때는 그 줄 범위로 근거 ID와 실제 노출량을 기록한다.
- 수락한 계약과 같은 적용 범위의 인용 원문에 Python 공개 함수 호출이 있고 선택한 구현 창이 그 함수의 본문에 속하면, 같은 파일의 선언 한 곳을 별도 근거로 보낼 수 있다. 이름과 스코프를 확인하고 여러 줄 매개변수·기본값·반환 형식을 원문 그대로 보낸다. 중복 선언·가려진 이름·동적 바인딩·데코레이터 때문에 함수 관계를 확인할 수 없으면 추가하지 않는다. 기존 구현과 테스트를 먼저 유지하고 같은 파일·전체 발췌 한도를 공유한다. 두 위치는 코드 근거이며 대조 판정은 별도로 기록한다.
- 모든 적격 파일에 발췌가 있고 예산이 남으면 다른 계약과 직접 어휘가 맞는 구현 창을 최대 두 개 더 붙인다. 창마다 최대 13줄을 쓰며 기존 창과 겹치지 않는 줄 범위를 인용한다. 한 파일의 합계는 50줄·2,400바이트 이내다. 범위 장부에서 파일은 한 번 세고 실제 전달한 창들의 줄과 바이트를 합산한다.
- 발췌가 있는 모듈 디렉터리는 `modules`(최대 32개)에 들어간다.
- 전체 investigate는 수락한 문서 계약·질문과 함께 관련 코드·테스트의 위치 후보를 최대 12개 보낼 수 있다. 확정한 조사 입력과 대조해 미제공 실행문이 포함된 코드 창을 고른다. 계약과 인용 원문 양쪽에 있는 정확한 식별자와 관련 분기를 우선하고, 겹치지 않는 구현 위치를 추가할 수 있다. 확인할 수 있는 구현 단서가 여러 파일의 일반 함수 위치에 그치면 후보에서 뺀다. 적격 코드가 있는 계약에는 관련 테스트 위치도 보낸다. JavaScript·TypeScript 테스트에서 확인한 단언을 구조 후보로 사용하며, 확인할 수 없는 구문은 일반 함수·호출 위치를 사용한다.
- 위치 선별은 앞서 읽은 계약 관련 소스 발췌용 파일을 재사용한다. 별도 읽기 한도는 코드와 테스트 각각 16개·256 KiB이며, 같은 디렉터리의 다른 파일보다 아직 방문하지 않은 곳을 먼저 읽는다. 범위와 비밀값 선별을 따르며 PR은 변경 파일만 대상으로 삼는다. 메시지 상한에 걸리면 기존 발췌를 유지하고 위치부터 줄인다. 위치는 경로·줄 범위·실제 이름을 제공하며 코드 원문 전달·근거 인용으로 집계하지 않는다.
- 위치 후보를 보낸 요청은 `L1`부터 번호를 붙인다. 모델은 같은 응답의 `contract_probe`에 `unknown`으로 대조한 계약 하나와 해당 범위의 제공된 위치 ID 1~3개를 고르거나 `null`을 낸다. 워커는 코드 위치를 하나 이상 고른 선택을 수락한다. 추가 검색은 입력에서 확인된 대소문자 구분 식별자 최대 3개를 정확히 찾는다. 검증한 위치는 스냅샷에 묶인 원래 ID·해시·줄 범위로 저장한다. 적격 위치가 있는 미확인 대조의 응답이 수락되면 선택 누락·`null`과 검증 실패를 검토 원시 기록에서만 구분하고 일반 후보와 대조를 유지한다. [위치 선택 기록 예시](../examples/review/investigate-probe.json)
- 범위 안 소스가 모두 제외 디렉터리 아래 있거나 발췌가 하나도 없으면 investigate는 `failed`(`review_input_unavailable`)다.

- 수락된 미확인 대조의 이유가 선택한 위치를 후속 확인 지점으로 명시하면 요청 별칭을 검증한 파일과 줄 범위로 바꿔 기록한다. 보고서는 그 위치를 앞으로 확인할 곳으로 읽는다.

### 문서 발췌(documents)

- 대상은 탐색 산출물에서 `selected` 상태인 안내 문서다. 문서 수나 발췌 크기 상한 때문에 모두 넣기 어려우면 [문서 탐색](ARCHITECTURE.md#문서-탐색)과 같은 순위로 대표 README, 제품 안내와 지역 문서를 고른다. Markdown 링크는 비밀값 검사에 통과한 대표 README 원문과 이번 검토에 쓸 수 있는 후보만 대상으로 삼는다. 모두 넣을 수 있는 경우 에이전트 지침, README, CONTRIBUTING, 설계 문서 순이고 같은 분류는 얕은 경로가 먼저다.
- 제목과 한 개의 리터럴 `::: package.module` 또는 `::: package.module.object`만 있는 Markdown API 페이지는 최대 두 개까지 원래 발췌 자리에서 Python docstring을 전달한다. 페이지 적용 디렉터리와 그 아래 `src/`의 `.py`·`__init__.py`를 고정 스냅샷에서 찾는다. 한 경로로 정해지고 범위·비밀값 검사를 통과할 때만 모듈 또는 공개 선언의 첫 문자열에서 옵션·예제·반환 설명을 우선 고른다. 파이썬과 문서 생성기를 실행하지 않는다. 문서 생성 옵션·가져오기로 다시 공개한 이름·동적 객체 해석은 처리 범위 밖이다. 발췌 한도는 다른 문서와 같다.
- Python docstring 근거의 `path`·해시·줄 범위는 실제 `.py`를 가리키며 `directive_path`는 문서 적용 범위를 정하는 원래 Markdown 페이지를 가리킨다. docstring의 `>>>` 예제는 정적 문서 근거다.
- `docs/units.md`의 `::: counters.units`가 `src/counters/units.py`로 연결되면 문서 근거의 `path`는 `src/counters/units.py`, `directive_path`는 `docs/units.md`다.
- Markdown·MDX 안내에 리터럴 npm 패키지 이름을 쓰는 `import` 또는 `require`와 그 바인딩을 호출하는 예제가 함께 있으면 워커가 호출의 패키지 범위를 찾는다. 실제 JS/TS 코드 블록의 짧은 호출이나 인접한 인라인 코드의 호출을 읽는다. 고정 스냅샷의 검증된 `package.json`에 해당 이름이 하나이고, 이번 분석 범위 안에 한 런타임 진입점이 있을 때만 참조를 만든다. 외부 패키지는 참조하지 않는다. 바인딩을 가리거나 변경할 가능성, 여러 로컬 패키지, 모호한 진입점, 생성 파일 또는 비밀값 보류 파일이 있으면 연결을 생략한다. 패키지나 예제를 실행하지 않는다.
- `input.documents[]`의 `package_reference`는 해당 발췌의 `applies_to`와 별도로 그 호출의 범위를 담는다. 호출 이름을 명시한 계약이나 질문이 예제를 인용하면 같은 파일·blob에서 예제 바로 앞이나 뒤에 이어진 설명도 함께 인용할 수 있다. 충돌·지역 지침은 기존 디렉터리 범위를 따른다. 앞 발췌의 끝에서 검증 가능한 짧은 코드 블록이 끊기면 기존 상한과 여유 슬롯 안에서 전체 예제를 별도로 보낸다. 요청 예산은 검색 문서 목록보다 해당 예제를 우선 보존한다. 조사 역할은 진입점과 호출 이름을 범위 안 코드 탐색에 활용하며 실제로 보낸 구현 발췌만 대조 근거가 된다.

`site/docs/usage.md`에서 `import { format } from '@sample/price'`와 `format(1250)`을 읽은 요청의 참조 예시다. 이 문서의 `applies_to`는 `site`이고, 이 호출에 관한 계약은 `packages/price`에 적용할 수 있다.

```json
{
  "package_reference": {
    "name": "@sample/price",
    "callable": "format",
    "scope": "packages/price",
    "entry_path": "packages/price/src/index.ts"
  }
}
```

- 발췌는 각 40줄·2,000바이트, 총 8개·10,000바이트까지다. 여러 문서가 자리를 두고 경쟁하면 대표 README와 제품 요구사항, 주제별 안내를 고른다. 긴 제품 안내에서는 사용자 흐름·역할·기능 요구처럼 제목과 본문이 함께 있는 뒤쪽 절을 대표 발췌로 고를 수 있다. 대표 README의 도입부와 가져오기·호출 예제를 담은 발췌는 보존한다. 제품 본문이 더 있으면 부차 경로의 미연결 문서나 일반 안내보다 한 구간을 먼저 보낸다. 대표 README가 연결한 예제와 부차 경로에만 있는 안내도 검토 대상이다. 여유가 생기면 README 또는 사용 안내의 뒤쪽 절에서 API·사용법·옵션·예제 등 제목과 조건·반환·오류 설명을 추가한다. 설치·목차·후원·변경 이력 등은 추가 위치 탐색에서 제외한다. 두 발췌는 같은 스냅샷 파일의 독립적인 줄 범위를 인용한다.
- 뒤쪽 절에 여러 API 멤버 제목이 들어 있고 발췌 개수에 여유가 있으면 같은 줄 범위를 제목 경계에서 나눈다. 나눈 발췌의 합계는 원래 선택한 절과 같으며 각 발췌에 별도의 근거 ID를 부여한다. 모델이 해당 멤버의 발췌를 인용하면 보고서가 그 제목 주변을 보여준다.
- 기존 발췌를 모두 둔 뒤에도 예산이 남으면 대표 README부터 각 사용 안내의 빠진 본문을 한 구간씩 돌아가며 추가한다. 제목이 없거나 인식되지 않은 절도 본문을 담으며, 이미 선택한 범위와 운영 섹션을 피하고 원래 줄 경계에서 자른다. 발췌 개수와 바이트 합계는 같은 요청 상한을 따른다. 보고서는 요청에 실린 줄을 근거로 연결한다.
- Markdown 목록은 같은 파일의 인접 발췌를 합쳐서 확인한다. 요청 한도에 맞춘 뒤 목록 일부가 빠진 발췌에는 `incomplete_list`를 붙인다. 모델은 보이는 항목을 근거로 쓰고 전체 목록의 구성이나 실행 순서를 단정하지 않는다.
- 예산 때문에 문서가 빠지면 `documents_outside_review_input`, 문서 일부만 보내면 `document_excerpt_only`를 남긴다.
- 읽지 않은 안내 문서(최대 16개), 모듈별 문서 연결과 겹침 묶음(각 최대 32·16개, 목록당 8개)을 함께 준다. 줄이면 `document_lists_shortened`다.
- 발췌와 부가 목록을 함께 보낼 수 있는지 먼저 확인한다. 요청 상한에 닿으면 읽지 않은 문서·모듈 연결·겹침 묶음을 먼저 줄여 문서 원문을 보존한다. 목록을 모두 덜어 내도 상한에 닿으면 뒤쪽 발췌부터 줄인다. 요청과 검토 기록에 `document_lists_shortened`를 남기고 탐색 산출물은 유지한다.
- 문서 요청의 `evidence_id`는 전달한 발췌 순서에 따른 `D1`, `D2` 같은 짧은 참조다. 형식 복구에서도 같은 참조를 보낸다. 워커는 응답의 모든 문서 인용을 스냅샷 근거 ID로 바꾼 뒤 범위와 중복을 검사한다. 정확히 제공된 참조와 그 발췌의 근거 ID만 받으며 저장 기록과 후속 검토에는 스냅샷 근거 ID를 사용한다.
- 범위 복구 요청의 `repair.document_scopes`에는 문제가 된 목록 이름, 1부터 세는 항목 번호와 실제 인용, 모든 인용이 적용되는 물리 범위 및 확인된 패키지 범위를 담는다. 패키지 범위에는 인용이 가리키는 호출 이름을 붙인다. 패키지 참조가 있는 문서 요청에서 복구 뒤에도 범위가 너무 넓으면 명시된 호출의 근거가 뒷받침하는 하위 패키지가 하나일 때만 그 경로로 좁힌다(`document_package_scope_narrowed`). 범위 오류만 남고 유효한 항목이 있으면 해당 오류 항목을 제외하고 `document_scope_items_omitted`를 보고한다. 유효한 항목이 없거나 근거 ID·서술 등 다른 검증이 실패하면 응답을 거부한다.
- 문서 규칙의 적용 경로(`applies_to`)는 탐색에서 정한 문서의 적용 디렉터리와 같거나 그 아래여야 한다.

### 환경 입력(environment)

- gap 모듈(판정이 `satisfied`가 아닌 모듈) 최대 4개, 그 모듈들의 요구 최대 16개다. 넘으면 `environment_gaps_outside_input`·`requirements_outside_input`를 남긴다.
- 요구마다 원문 줄(600바이트 이하)과 근거 ID를 준다. 원문을 쓸 수 없으면 `requirement_source_omitted`다.
- 출력 메모는 자기 모듈의 요구·근거 ID만 인용한다. 한 모듈·호스트에서 본 버전을 다른 모듈로 옮기지 않는다.

### 도구 관찰과 해석 창(interpret)

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
- 검증 지침은 코드로 확인한 프로그램 상태, 근거로 확인한 런타임 동작, 실제 호출 경로에서 선택 가능한 지원 입력·설정을 유효 조건으로 쓸 수 있게 한다. 후보의 발동이나 영향에 필요한 외부 동작이 아직 확인되지 않았으면 `inconclusive`를 반환하고 미확인 전제를 적는다.
- verify 요청의 발췌는 인용 발췌, 확장, 인용되지 않은 문맥 순으로 최대 40개다(인용 발췌는 항상 남는다). 요청이 한도를 넘으면 확장·비인용 문맥·문서 계약을 먼저 줄인다(`review_input_trimmed`, `callers_outside_review_context`, `contracts_outside_verify_input`). 이어서 발췌의 SHA-256을 요청에서만 생략할 수 있다(`verification_excerpt_hashes_omitted`). 근거 ID와 경로·줄·코드 원문은 유지하고 검토 기록에는 스냅샷과 대조한 해시를 저장한다. 그래도 넘으면 후보를 앞에서부터(investigate 다음 interpret) 보낼 수 있는 만큼만 보낸다. 보내지 못한 후보는 `unreviewed`(`verification_budget_exceeded`)로 남고 task는 `failed`(`review_input_incomplete`)다.

- Go 입력의 요청 바이트는 `TestGoReviewInputsUnchanged`가 역할마다 지침 해시와 입력 해시로 고정한다.

### 요청 크기

| 한도 | 값 | 적용 |
|---|---|---|
| 요청 한도 `PromptBudget` | 40,000바이트 | 한 요청의 전체 메시지 텍스트(`opencode.Prompt`: 지침, 계약, 복구 규칙, 입력, 감싸는 문장) |
| 복구 여유 | 3,072바이트 | 첫 요청을 만들 때 형식 복구 요청이 들어갈 자리를 남긴다 |
| OpenCode 어댑터 입력 한도 | 64 KiB | 지침 + 입력. 넘으면 `invalid_review_input` |
| QA 예산 relay 입력 한도 | 49,152바이트(기본) | relay가 받는 요청당 메시지 텍스트. 40,000바이트와의 차이는 OpenCode 클라이언트 자체 메시지 몫이다([평가](EVALUATION.md)) |
| 출력 | relay 기본 1,536 토큰 | OpenCode 관리 설정의 모델 출력 한도와 같다 |

- 요청은 메시지 하나이고 도구 정의를 쓰지 않는다.
- 보내기 전에 메시지 바이트를 재고, 넘으면 발췌를 뒤에서부터 줄이고 `review_input_trimmed`를 남긴다. 최소 발췌로도 넘으면 task는 `failed`(`review_input_too_large`)다.
- 예산이 모자라면 목록 뒤쪽이 먼저 빠진다. 첫 순회에서 생태계 이름 순으로 뒤인 언어의 발췌가 여기에 해당한다. 다섯 생태계가 모두 범위에 있으면 언어 지침이 커져 코드 발췌가 줄고, 최대 길이 후보가 많은 verify는 일부 후보를 보내지 못할 수 있다. 요청 한도와 역할별 지침 구성의 변경은 소유자 결정이다.

## 모듈 단위 검토 분할

overview 조사(`review.investigate`)가 보내지 못하는 코드·테스트 파일을 slot(slice)에 나누어 slot마다 조사·검증 한 쌍(`review.investigate.sNNN`, `review.verify.sNNN`)으로 검토한다. 계획은 manifest·scope·상수로 slot 수만 정하고, 정적 분석 뒤에 실행되는 `review.scope`가 [검토 우선순위](#검토-우선순위)대로 파일을 slot에 채운다. 호출 상한 안에서 slot을 차례로 허용한다. 구현은 [`slice_plan.go`](../internal/worker/review/slice_plan.go)(계획·채우기)와 [`slice_run.go`](../internal/worker/review/slice_run.go)(실행)이다.

| task | 단계 | 판정 필수 | 모델 호출 | 내용 |
|---|---|---|---|---|
| 전체 역할 다섯 개 | 위 표 | 위 표 | 위 표 | 바뀌지 않는다. overview verify는 계속 interpret 후보도 검증한다 |
| `review.scope` | `code_review` | 아니오 | 없음 | slot이 하나 이상일 때만 계획한다. overview가 실제로 보낸 파일을 빼고 남은 파일을 우선순위대로 slot에 채워 범위 산출물을 쓴다 |
| `review.investigate.sNNN` | `code_review` | 아니오 | 1~3 | slot 하나의 코드·테스트 발췌로 조사한다 |
| `review.verify.sNNN` | `code_review` | 아니오 | 0~3 | 같은 slot investigate의 후보만 검증한다 |

slice는 판정 필수가 아니다. 필수로 두면 호출 상한보다 큰 저장소는 모두 `partial`이 되어 run 상태가 의미를 잃는다. 검토하지 못한 범위는 범위 산출물과 slice task 상태로 남는다. `review.scope`가 채우지 않은 slot은 호출 없이 `skipped_policy`(`no_review_scope`)로 끝난다. 진행 단계에서는 `no_review_scope`만 단계를 낮추지 않고, 상한으로 거절된 slice나 선별이 없어 건너뛴 slice는 `code_review` 단계를 `partial`로 남긴다([구조](ARCHITECTURE.md#표시-단계와-task-단계)).

### 계획

- 모듈 단위는 overview 코퍼스의 묶음과 같다(위 [코드 발췌](#코드-발췌investigate)). Go는 가장 깊은 go.mod 디렉터리, 다른 언어는 같은 생태계 manifest가 있는 가장 깊은 디렉터리, 없으면 `.`이다. ID는 `<ecosystem>:<dir>`이고, 범위 산출물의 `discovered`는 모듈 탐색이 같은 ID를 찾았는지 표시한다.
- 파일 상한 `c(f) = min(2400, 6 × size + 2)`. JSON 인코딩은 바이트 하나를 최대 6바이트(`\u00XX`)로 바꾸고 따옴표 두 개가 붙으므로 실제 발췌 바이트보다 작지 않다.
- overview 접두 P: 모든 파일을 상한 크기로 두고 overview의 일반 순회(묶음 순서, 묶음 안 순위)를 흉내 낸다. 고른 파일이 8개 미만이고 남은 예산이 128바이트 이상이며 상한이 남은 예산에 들어가는 동안 파일이 P에 들어간다. 처음 들어가지 않는 파일에서 P가 끝난다. `review.scope`는 overview가 실제 보낸 파일을 확인한다. 문서 계약과 관련된 줄을 먼저 보내 P에서 밀린 적격 파일은 남은 slice에 배정한다.
- 계획은 slot 수만 정한다. 남은 파일을 manifest만으로 아는 사실(경로 분류, 진입점·소스·테스트)로 [우선순위](#검토-우선순위)대로 묶어 본 수에 여유(묶음 8개마다 하나, 최소 둘)를 더한다. 실행 때 도구 진단이 순서를 바꾸면 진단이 있는 파일이 따로 묶여 묶음 수가 늘 수 있기 때문이다. 그래도 모자라면 `review.scope`는 같은 순서를 유지한 채 묶음 경계를 푼다: 먼저 디렉터리 규칙을, 그다음 순위 경계를 푼다(slot의 순위는 첫 파일의 순위다). 경계를 풀어도 담을 수 없으면(파일이 상한 96개 slot보다 많으면) 경계를 지킨 묶음의 앞 slot만 채운다. 상한 96 아래에서는 이렇게 해서 계획 밖 파일이 생기지 않는다(`TestSlotSlackCoversRunTimeOrder`: 임의 트리 약 3,000개, 파일의 30%에 진단, 모듈 일부에 실패한 분석기, 일부 생성 파일).
- slot은 96개(`s001`~`s096`)까지다. slot이 모두 차면 남은 파일은 모듈별로 세며(주요·부차를 나누어) `review_slice_limit`을 남긴다.
- 모든 적격 파일이 overview 접두에 들어가는 저장소는 slice가 없다. 관련 위치가 일반 발췌에 들어가고 계약에 새로 연결할 근거가 없으면 기존 파일 순회를 따른다.
- 계획은 manifest, scope, 상수로만 정하고 호출 상한을 쓰지 않는다. 재시도는 모든 task 행을 queued로 되돌리고 남은 task가 있으면 완료하지 않으므로, 상한에 따라 task key가 달라지면 고아 task가 생긴다.
- 계획 순서는 기본 역할 다섯, `review.scope`, slot 쌍, 조건이 맞으면 `review.contract`다. 모든 task가 run이 `running`이 되기 전에 만들어지므로 계획 revision은 1이고 가중치를 선언하지 않는다(진행 기준 `task_count`).

### 검토 우선순위

`review.scope`는 정적 분석이 끝난 뒤 실행되므로 도구 진단을 안다. overview가 보낸 파일을 뺀 나머지를 다음 순서로 slot에 채운다. pull request run은 그 앞에 변경 단계를 하나 더 둔다(0번). 채우기는 `packSlots`([`slice_plan.go`](../internal/worker/review/slice_plan.go)), 분류 규칙은 [`priority.go`](../internal/worker/review/record/priority.go)다. 결과는 결정적이며 마지막 동률은 경로 순이다.

0. **pull request 변경(pull request run만).** pull request가 변경한 파일(`changed`)이 가장 먼저이고, 그다음 변경 파일과 같은 디렉터리의 파일(`neighbor`), 그다음 나머지다. 이 단계는 분류보다 위다. 변경한 fixture도 도구 진단이 있는 주요 파일보다 먼저 검토한다. 삭제된 파일은 head snapshot에 없어 들어오지 않는다. 각 단계 안은 아래 1~5를 그대로 따르고, slot은 단계를 섞지 않는다. 변경 목록은 run에 고정한 pin(`Job.pull_request.changed`)이므로 재시도해도 같은 계획이다.
1. **분류.** 주요(`primary`) 파일이 부차(`secondary`) 파일보다 먼저다. 부차 파일은 디렉터리 경로에 부차 경로 조각이 있거나(대소문자 무시, `tests/data`처럼 `/`가 든 조각은 연속한 디렉터리) 발췌 첫 10줄에 생성 파일 표지(`Code generated` … `DO NOT EDIT`, `@generated`)가 있는 파일이다. 기본 조각은 `fixtures`, `testdata`, `test-data`, `tests/data`, `examples`, `samples`, `demo`, `vendor`, `third_party`, `node_modules`, `generated`, `gen`, `mocks`, `__snapshots__`, `dist`, `build`이다. `build`와 `dist`는 `.go` 파일에 적용하지 않는다(Go 패키지 이름일 수 있다). 모듈 디렉터리가 부차 경로 아래면 모듈도 부차다. 파일마다 이유(`secondary_path:<조각>`, `generated_file`)가 범위 산출물에 남는다.
2. **순위.** 같은 분류 안에서 비지표 도구 진단이 있는 파일(`tool_observations`: 가장 높은 심각도, 그다음 진단 수 순), 필수 분석 task가 성공하지 않은 모듈의 파일(`analyzer_not_succeeded`), 진입점(`entry_point`: Go는 `main.go`, 다른 언어는 overview 코퍼스의 진입점 규칙), 그 밖의 소스(`source`), 테스트(`test`) 순이다. 구조·중복 지표와 억제된 진단은 세지 않는다. 심각도는 도구의 원래 값을 `high`(critical·fatal·error·high), `medium`(warning·warn·medium·moderate), `low`(그 밖과 값 없음), `ignored`(ignored·none·off)로 묶는다. 진단은 overview interpret과 같은 검증된 읽기로 분석 산출물에서 읽고, 읽을 수 없는 산출물은 아무것도 더하지 않는다.
3. **응집.** 모듈마다 위 순서, 디렉터리, 코퍼스 순위 순으로 파일을 늘어놓고 연속 조각(8개·12,000바이트, 상한 기준)으로 자른다. 한 조각은 한 분류·한 순위이고, 현재 조각에서 끝낼 수 없는 디렉터리는 새 조각에서 시작한다. 그래서 slot은 되도록 한 모듈·한 디렉터리다.
4. **모듈 사이.** 다음 조각은 모든 모듈의 맨 앞 조각 가운데 분류·순위·심각도·진단 수가 가장 앞선 것이다. 같으면 그 분류의 첫 조각을 아직 받지 않은 모듈이 먼저이고, 그다음 그 분류의 바이트가 큰 모듈이 비례해 더 많이 받는다(받은 조각당 바이트가 가장 큰 모듈). 큰 모듈이 먼저 많이 받되 두 번째 주요 모듈이 굶지 않는다.
5. **합치기.** 같은 분류의 이웃한 조각은 한도 안에서 slot 하나를 같이 쓴다(모듈 8개 이하). 작은 모듈이 slot을 하나씩 차지하지 않는다.

- overview의 일반 순회도 같은 분류를 따른다. 코퍼스는 주요 묶음과 주요 파일을 먼저 순회하고, 주요 파일이 다 나간 뒤에야 부차 묶음을 연다. 묶음 안에서 부차 파일은 주요 파일 뒤다. overview 접두 P의 계산도 같은 순회를 흉내 낸다. 계약별 위치를 먼저 보낸 경우도 범위 산출물에는 실제 보낸 경로와 줄 수가 남는다.
- pull request run의 overview는 모든 묶음의 변경 파일을 먼저 순회하고, 이어 같은 디렉터리 파일, 그다음 위의 주요·부차 순회를 한다(`codeGroups.phases`). pull request가 없는 run은 계획·요청이 이전과 바이트 단위로 같다(`TestPullRequestChangedFilesFirst`가 같은 계획을, 기존 요청 고정 시험이 같은 요청을 확인한다). 검토 지침은 바뀌지 않으며 모델에게 변경 파일이라고 따로 알리지 않는다.
- 규칙은 한 곳(`record.DefaultSecondary`)에 있고 worker의 `review.Handler.Secondary`로 바꿀 수 있다. 운영자 설정 항목은 아직 없다. 실행에 쓴 규칙은 범위 산출물 `priority.secondary`에 남는다.
- 보고서(builder 8)의 [장부](REPORT.md#모듈별-검토-장부)는 주요 파일 중 상한에 막힌 파일(`files_budget_exhausted`)과 slot이 남지 않은 파일(`files_not_planned`), 부차 파일 중 보내지 못한 파일(`files_secondary_not_reached`)을 따로 센다.

### 호출 상한과 승인

- 단위는 모델 호출이고 attempt마다의 상한이다. `codeye agent --review-max-calls N`(기본 99, 15~207), Hello `review_max_calls`, 서비스 설정 `agent.model.review_max_calls`로 정한다. run이나 profile로 바꿀 수 없다.
- 기본 99는 중간 규모 저장소의 대부분을 기본 설정으로 검토하기 위한 값이다(처음 기본값 33은 유료 회귀 실행에서 파일럿 Go 모듈 67개 파일 중 34개를 28회 호출로 검토했다). 회귀 실행에서 실모델 호출 55회의 비용은 보고된 사용량으로 약 USD 0.94, 보수적 장부 차감으로 USD 2.02였다. 기본 상한 99회를 다 쓰는 run은 그 대략 두 배(보고 사용량 약 USD 1.7, 장부 차감 약 USD 3.6)다. 실제 비용은 모델·저장소 크기·형식 복구 횟수에 따라 다르다. 비용을 줄이려면 상한을 낮춘다. 15에서도 전체 검토 후 남은 호출이 여섯 번 이상이면 다음 slice를 승인한다([안내서](GUIDE.md)).
- 전체 역할 다섯 개는 최대 15회(5 × 3)를 쓰고 잘리지 않는다. 그래서 최솟값이 15다. 전체 역할이 모든 slice보다 먼저 실행되므로 그 몫을 따로 예약할 필요가 없다.
- 계수는 attempt 안에서만 한다. attempt 하나는 worker 프로세스 하나이고 task를 차례로 실행한다. 모든 검토 호출과 재시도는 보내기 전에 attempt scratch의 계수 파일에서 남은 예산을 확인하고 한 줄을 쓴다. 계수를 읽거나 쓰지 못한 호출은 보내지 않는다(`review_call_count_failed`). 전역 상태나 프로세스 사이 상태는 없다. 한 attempt 안에서 slice를 병렬로 실행하려면 공유 계수가 필요하다.
- 최댓값 207은 전체 역할 15회와 slot 96개마다 두 번(investigate·verify 한 번씩, 형식 복구가 없을 때)이다. 복구가 많으면 207에서도 96개를 다 승인하지 못할 수 있다.
- 승인은 계획 순서대로, 실제 사용량으로 한다. slice k의 investigate를 시작하기 전에 `상한 − 사용한 호출 ≥ 6`(investigate·verify 한 쌍의 최악 3 + 3)이면 승인한다. 처음 거절된 slice와 그 뒤의 모든 slice는 호출과 기록 없이 `budget_exhausted`(`review_budget_exhausted`)로 끝나고 짝 verify도 같다. 결정은 attempt scratch에 남기고 verify가 따른다.
- 그래서 attempt의 호출은 상한을 넘지 않는다. 최악 사례로 미리 승인하면 상한의 세 분의 이가 버려지지만, 실제 사용량으로 승인하면 모델이 형식 복구를 많이 할수록 승인되는 마지막 slice가 run마다 달라질 수 있다. 승인 결과는 slice task 상태로 보인다.
- `review.contract`는 모듈 검토 뒤 동일한 계수에서 남은 호출을 확인한다. 처음 요청 뒤 [복구 경로](#복구-경로)를 적용해 최대 두 번 호출한다. 남은 호출이 한 번이면 첫 응답이 거부되더라도 다시 보내지 않는다. 기본 역할이나 slice가 쓸 호출을 후속 검토에 예약하지 않는다.

| 상한 | 한 번에 끝나는 task(1회) | 모든 task가 3회 | 최악 사례 사전 승인 |
|---|---|---|---|
| 15 | coverage fixture의 slot 8개 중 채운 6개 가운데 3개 | 0개 | 0개 |
| 99 | 6개 모두 | 6개 모두 | 6개 모두 |
| 207 | 6개 모두 | 6개 모두 | 6개 모두 |

(`TestSliceAdmissionKeepsTheCeiling`, [`fixtures/modules/slices/input`](../fixtures/modules/slices/input). environment는 차이가 없어 호출하지 않는다.)

### 비밀값 선별과 판단 순서

- `review.scope`와 slice는 전체 역할과 같은 선별 규칙을 따른다. 선별이 끝났으면 보고된 파일을 `withheld`로 빼고, 선별이 계획되지 않았거나 실행되지 않았으면(gitleaks 미설정) 기본 읽기 필터만 거친 입력으로 실행한다. 이때 범위 산출물은 `screen: not_screened`이고 범위 산출물과 모든 slice 기록이 `model_input_not_secret_screened`를 싣는다. 보고서는 비밀정보 절 `model_input`을 미선별로 보이고 공백 `model_input_not_screened`를 남긴다. 선별이 실행됐지만 결과를 쓸 수 없어도 같은 미선별 입력으로 실행하되, 읽을 수 있는 결과가 보고한 파일은 계속 `withheld`로 뺀다.
- 선별은 task마다 시작할 때 다시 읽는다. slice 발췌도 같은 읽기 경로를 지나므로 보류 파일과 민감값 패턴 필터를 똑같이 받는다.
- slice task의 판단 순서: 모델 전송 허가 → 범위 산출물을 읽을 수 있음(아니면 `dependency_failed`) → `review.scope`가 채운 slot이고 보낼 파일이 남음(아니면 `no_review_scope`) → 호출 상한. 보낼 파일이 없는 slot은 상한이 다해도 `no_review_scope`다.

### 범위 산출물

`review.scope`는 모델을 호출하지 않는다. overview investigate 기록(수락이든 실패든, 요청을 보냈을 때)의 context 발췌에서 overview가 실제로 보낸 코드·테스트 파일을 읽고, 계획의 모든 파일을 선별과 읽기 필터로 다시 확인하고, 분석 산출물의 진단으로 우선순위를 매겨 slot을 채운 뒤 산출물 하나를 role `other`, media type `application/vnd.codeye.review-scope+json`으로 올린다. [스키마](../schemas/review-scope.schema.json) · [예시](../examples/review/slices/scope.json)

- 필드(schema 0.2): `schema_version`(0.2), `kind`(`review_scope`), `pack`, `snapshot_manifest_digest`, `run_id`, `attempt_id`, `screen`(`applied` 또는 `not_screened`. `not_screened`이면 `limitations`에 `model_input_not_secret_screened`가 있다), `budget`(`max_calls`, `max_request_bytes`, `whole_run_max_calls` 15, `slice_reserve_calls` 6, `calls_before_slices`, `admission` `sequential_reserve`), `limits`(`max_slices` 96, 계획이 만든 `slots`), `priority`(`order`: 보통 `primary_first_observations_first`, pull request run은 `pull_request_changed_first`, `secondary`: 쓴 `segments`와 `not_for_go`), `upstream`(overview investigate 기록의 SHA-256), `modules[]`(`id`, `discovered`, `class`, `files_eligible`, `files_secondary`, `files_not_planned`, `files_secondary_not_planned`), `overview`(`files`: P와 그 밖에서 overview가 보낸 파일, `offered`: overview가 보낸 경로), `slices[]`(채운 slot만 차례로: `key`, `investigate_task`, `verify_task`, 첫 파일의 `class`·`rank`·`pull_request`, `modules`, `files`), `excluded`(slot에 넣지 않은 `withheld`·`unreadable` 파일), `limitations`.
- 파일마다 `path`, `sha256`, `module`, `bound_bytes`, `planned_lines`, `planned_bytes`, `total_lines`, `state`, `class`, `rank`, 부차 파일의 `secondary`(이유), 진단이 있는 파일의 `observations`(수)·`severity`(가장 높은 값), pull request run에서 변경 파일과 그 이웃의 `pull_request`(`changed`·`neighbor`). 상태는 `planned`(slot이 보낸다), `covered_by_overview`(overview가 보냈다), `withheld`(선별이 보류), `unreadable`(UTF-8 아님·NUL·민감값 패턴·빈 파일), `overview_not_sent`(P에 있지만 overview가 보내지 않음: 요청을 줄였거나 보내기 전에 실패)다. 적격 파일은 모두 정확히 한 곳에 있다. slot이 남지 않은 파일은 목록 대신 모듈별 `files_not_planned`로 센다.
- schema 0.1(계획 때 정한 slice, 32개까지, 분류 없음)은 이전 worker의 산출물을 읽기 위해 계속 해석한다. 0.1에서는 slice가 `covered_by_overview`·`withheld`·`unreadable` 파일도 가질 수 있다. worker는 0.2만 쓴다.
- 승인된 slice 목록은 산출물에 없다. 산출물은 어떤 slice도 실행되기 전에 쓰이고 승인은 실행 중에 차례로 정해지므로, 승인 결과는 slice task 상태(`budget_exhausted`/`review_budget_exhausted`)에서 읽는다.
- decoder(`record.DecodeScope`)는 경로와 blob을 스냅샷 manifest와 대조하고, 모듈별 수, `covered_by_overview`와 `offered`의 일치, 파일 중복을 검사한다. 0.2에서는 채운 slot이 `slots` 이하인지, 파일 분류가 기록된 규칙과 맞는지, slot 순서가 우선순위를 따르는지(주요 slot이 부차 slot보다 앞)도 본다. `pull_request` 표시는 `pull_request_changed_first` 순서에서만 받고, slot 하나의 파일은 같은 표시를 가져야 한다.

### slice 요청과 기록

- slice 요청은 overview의 요청 생성기, 역할 계약 문자열, 팩 파일을 그대로 쓴다. 지침 hash가 overview와 같고(`modules-worst-screened` 고정값), `prompts/`는 바뀌지 않는다.
- slice investigate는 overview처럼 수락된 environment·documents 결과를 받고, 코퍼스는 그 slice에서 `planned`인 파일(첫 50줄, 파일당 2,400바이트, 합계 8개·12,000바이트)이다. 읽기 필터와 선별을 다시 적용하고, 그새 읽을 수 없게 된 파일은 건너뛴다. `modules`는 slice의 모듈 디렉터리이고 `files_outside_review_context`를 항상 남긴다. upstream은 `review.environment`, `review.documents`, `review.scope`다.
- slice verify는 자기 slice investigate의 후보만 받는다(interpret 후보 없음). upstream은 `review.documents`, `review.investigate.sNNN`, `review.scope`다.
- 기록은 schema 0.3이다(아래 [검토 기록](#검토-기록)). [investigate 예시](../examples/review/slices/investigate.s001.json) · [verify 예시](../examples/review/slices/verify.s001.json)
- 다섯 언어가 모두 범위에 있는 최악 사례에서도 slice 요청은 40,000바이트 안이다(`TestSliceRequestSizeWorstCaseAllLanguages`).
- 보고서(builder 6부터)는 0.3 기록과 범위 산출물을 읽는다. builder 8은 schema 0.2 산출물의 분류도 읽는다. slice 후보는 `review_sNNN_<후보>` 발견이 되고, 코드 분석 절의 [모듈별 검토 장부](REPORT.md#모듈별-검토-장부)가 모듈마다 모델에 보낸 파일과 보내지 못한 이유(상한·비밀값·계획 밖 등)를 센다. 모델에 보낸 범위는 검증된 범위가 아니다. `ai_review`는 필수 역할만 본다.

## 문서 계약 후속 검토

모델 검토가 승인되고 안내 문서와 지원 언어 구현 경로가 계획 범위에 있으면 마지막에 `review.contract`를 한 건 둔다. 앞선 독립 조사에서 수락된 `contract_probe`와 명시적인 `unknown` 대조가 있어야 보낸다. 같은 문서 근거와 범위의 계약이 충돌할 때는 기존 판정을 보존한다.

요청은 같은 스냅샷에서 다시 읽은 문서 약속과 앞선 이유, 위치 선택으로 얻은 구현·호출·테스트 발췌를 담는다. 수락된 계약과 그 원문에 모두 이름이 나오는 함수는 최대 두 개까지 적용 범위 안에서 정확히 찾아 진입점의 본문을 함께 보낼 수 있다. 선언만 있거나 같은 이름의 정의가 여러 개면 그 검색 결과는 쓰지 않는다. 관련 모듈 검토가 실제로 제공한 코드·테스트 줄도 사용할 수 있다. 해당 기록과 `review.scope`의 digest를 묶는다. 실제 발췌만 근거 목록과 코드 노출에 포함하며 `loc_` 위치 ID는 그 자체로 인용할 수 없다.

선택한 구현 창에서 독립 조사에 없던 Go 패키지 함수 본문을 찾으면, 같은 패키지에서 그 함수를 이름으로 직접 호출하고 결과를 분기 또는 반환에 쓰는 창을 최대 두 개 보낸다. 함수 선언이 창 밖에 있으면 같은 파일의 구문 트리로 본문이 속한 함수를 확인한다. 선택한 줄의 조건이나 반환에서 직접 호출한 짧은 구현도 두 개 중 하나의 창으로 보낼 수 있다. 호출이나 결과의 줄도 새로 읽은 줄이어야 한다. 적용 범위와 PR 변경 경로의 Go 파일을 최대 32개·512 KiB까지 읽으며, 테스트·생성물·보류 파일은 호출자 후보에서 빠진다. 모든 분석 대상 파일을 읽은 디렉터리에서만 다른 파일의 빌드 조건 없는 정의를 연결한다. 지역 이름이 가리는 호출과 같은 이름의 선언이 여럿인 경우는 제외한다. Go 파서가 연결한 직접 호출을 발췌하므로 해당 프로젝트의 빌드나 실행은 필요하지 않다.

선택한 계약과 원문에서 공통으로 찾은 코드 식별자나 마침표를 포함한 이름·버전을 수락된 모듈 검토의 강점과 대조한다. 같은 단서가 그 강점이 인용한 구현의 새 실행문 줄에도 있으면 발췌를 최대 두 개 우선 넣는다. 해당 모듈 검토와 범위 산출물의 digest를 연결하고 원문을 다시 검증한다. 강점의 제목과 설명은 검색에만 쓰며 대조 판정은 후속 응답이 인용한 구현 근거로 검증한다.

선택한 위치, 검증된 리터럴 검색, 또는 문서에 명시된 이름으로 찾은 새 실행문과 앞선 발췌가 같은 구현 파일에 있으면, 전체가 50줄·1,600바이트 이하이고 요청 한도에 들어갈 때 함수 선언과 나머지 분기를 함께 보낸다. 원본 스냅샷에서 다시 읽어 새 근거 ID를 부여하고, 기존 인용과 모듈 기록에 연결된 발췌, 서로 다른 분기를 보여 주는 발췌를 유지한다. 같은 내용을 포함하는 발췌가 있으면 그 안에 포함되는 선택 발췌는 생략할 수 있다.

응답은 기존 계약 인덱스·적용 범위·문서 근거 ID를 그대로 반환한 대조 한 건이다. 일치·부분 일치·불일치를 저장하려면 독립 조사 입력 전체의 구현 발췌에 없던 실행문이 대상 범위 안에 한 줄 이상 인용돼야 한다. Rust의 테스트 전용 항목과 JS·TS 템플릿 문자열 본문은 실행문 근거에서 제외한다. 호출 경로와 결과가 실제 계약과 이어지는지는 모델의 정적 판단이다. 테스트 원문에서 해당 입력과 단언을 확인할 수 있고 테스트 실행 결과는 없다. 위반 조건과 준수 조건을 각각 확인했을 때 부분 일치를 사용한다. 일부만 확인했으면 `unknown`과 `contract_comparison_inconclusive`를 기록하고 보고서에서 원래 판단을 유지한다. 새로 확인한 구현은 추가 관찰로 남길 수 있다. 불일치한 참조, 잘못된 출력, 선별 제한, 예산 부족에도 원래 결과를 유지한다.

### Go HTTP 사용자 동작

문서의 기본·보충 요청은 목록 안에서 여유가 있을 때 `handler`에 확인된 Go 처리기의 파일명과 함수명을 읽기 쉬운 짧은 표현으로 표시한다. 긴 이름과 안전한 형식으로 줄일 수 없는 이름은 생략한다. 이름을 보내기 전에 등록과 처리기에 범위·스냅샷·비밀값 선별 규칙을 적용한다. 같은 메서드·패턴이 여러 곳에 있거나 패턴에 리터럴 경로 조각이 없을 때 이름을 먼저 담는다. 이름을 생략해도 앞서 들어간 경로 선택지와 문서 발췌는 유지한다.

워커는 Go 소스에서 리터럴 패턴과 처리기 본문을 모두 확인할 수 있는 등록을 찾는다. 표준 라이브러리의 HTTP 등록, 지원하는 `gorilla/mux` 등록과 접두 경로, 메서드·경로·처리기 필드를 전달하는 제한된 형태의 경로 테이블을 읽는다. 조건부·모호한 등록과 실행 시 계산되는 값은 목록에서 빠질 수 있다. 경로는 해당 등록의 패턴으로, 상위 라우터에 붙는 상대 경로일 수 있다. 워커는 서비스를 구동하거나 실제 도달 여부를 판정하지 않는다.

검색은 범위 내 주요 Go 파일 최대 512개, 파일당 96 KiB, 전체 4 MiB 안에서 수행한다. pull request에서는 변경 파일 경계를 적용한다. 선별에서 보류된 파일, 생성 소스, 테스트와 부차 경로는 후보에서 제외한다. 확인한 경로의 패턴은 128바이트 이하다. 파악한 등록을 최대 64개까지 서로 다른 경로 묶음에 걸쳐 모아 요청에 최대 5,000바이트의 목록으로 넣는다. 여유 공간이 부족하면 먼저 문서 부가 목록을 줄이고 문서 원문은 보존한다.

문서 검토는 같은 호출에서 `workflow_routes`의 짧은 ID를 수락된 사용자 동작 계약에 선택적으로 연결한다. 상태 변경과 이후 조회처럼 한 계약에 여러 경로가 필요할 수 있으며 전체 응답에서 최대 네 건을 선택한다. 검토 기록에는 최종 수락된 계약 번호와 run·attempt·스냅샷·등록·실제 코드 위치에 묶인 digest만 남는다. 누락하거나 잘못된 선택은 일반 문서 계약·질문을 유지한다. 잘못된 선택은 원시 기록에 `workflow_selection_invalid`로 남는다. 경로 ID와 digest 자체는 인용할 근거가 아니다.

기본 요청과 보충 요청은 문서의 동작을 직접 수행하는 경로를 고르고, 같은 계약의 결과를 확인하는 데 필요할 때 선행 동작과 이후 조회도 함께 고르도록 지시한다. 출력 형식에는 실제 후보 번호나 경로 ID 예시를 넣지 않는다.

전체 조사는 같은 범위와 선별 규칙으로 경로를 다시 확인한 뒤 여섯 줄 창 이내로 처리기와 등록부를 우선 보낸다. 요청에 들어간 실제 발췌의 ID는 해당 계약과 함께 전달한다. 처리기와 이어지는 구현에 아직 읽지 않은 줄이 있으면 기존 위치 선택 후보에 포함할 수 있다. 후속 검토에는 같은 계약과 적용 범위에 해당하는 실제 호출부와 구현 발췌를 사용할 수 있다. 소스에서 확인한 한 가지 호출 구성은 배포 전체의 동작을 입증하지 않는다. 다시 확인하지 못한 근거는 상세의 `workflow_evidence_unavailable`로 남기며, 기존 모델 호출 상한과 새 구현 근거 조건을 적용한다.

처리기가 호출하는 함수가 16줄·1,600바이트 안에 들어가면 선언과 본문 전체를 유지한다. 긴 함수에서는 바깥 블록에 확인된 HTTP 응답이 하나 있고 앞 발췌에 그 응답이 없을 때, 인용할 응답 호출과 앞쪽의 읽기·오류 처리 등 한도 안에 들어오는 문장을 함께 고른다. 표준 HTTP 함수나 실제 `http.ResponseWriter` 매개변수의 쓰기 및 그 매개변수로 만든 JSON·XML 인코더의 호출을 찾는다. 응답 후보가 모호하거나 발췌에 들어가지 않으면 앞 발췌를 유지한다.

호출이 이어지는 구현에서 요청이 읽거나 바꾸는 상태의 소스가 확인되면, 경로가 선택된 뒤에만 결과 발췌를 찾는다. 같은 패키지의 직접 반환 호출과 실제 같은 수신자의 호출, 인자를 직접 호출하는 것으로 확인한 콜백을 최대 두 호출까지 추적한다. 수신자 타입을 확인한 `database/sql`·pgx의 리터럴 SQL 연산과 선언을 확인한 구조체 필드 연산을 읽는다. 호출과 결과 발췌는 각각 최대 16줄·1,600바이트다. 이름에 특정 기능 용어가 나오는지보다 확인한 호출 관계와 실제 연산을 사용한다.

전체 조사에서는 처리기와 등록부를 먼저 배정한다. 전체 여섯 발췌 한도의 남은 자리에서 연결된 호출부와 결과 구현을 함께 담을 수 있을 때 결과 근거를 추가하고, 이어서 적격한 테스트 발췌를 붙일 수 있다. 후속 검토는 같은 계약의 원문 뒤에 연결된 구현을 우선 둔다. 요청에서 필요한 발췌가 모두 연결되면 해당 경로에 `conditional_source_unexecuted`를 표시한다. 실제 실행이 없으므로, 이 표시는 그 조건에서 코드가 시도하는 동작을 읽었다는 뜻이다.

선택된 경로에는 적용 범위와 PR 변경 경계를 지키는 등록부·처리기 디렉터리의 Go 테스트를 최대 32개·512 KiB까지 확인해 테스트 발췌 하나를 붙일 수 있다. 같은 테스트 함수에서 `httptest.NewRequest` 또는 그 메서드·경로 인자를 그대로 넘기는 짧은 함수의 요청을 찾고, `ServeHTTP`에 전달한 recorder에서 상태·응답 본문·헤더의 구체적 기대값을 확인한다. 확인한 리터럴 경로와 단순 경로 변수에 맞는 등록이 하나일 때 사용한다. 같은 서버를 이용한 변경 요청, 관련 GET과 두 단언을 한 창에 담을 수 있으면 우선한다. 구현 발췌 뒤에 최대 28줄·1,600바이트의 테스트 원문을 보낸다. 테스트의 통과 여부는 시험 실행 결과가 있을 때 확인한다.

## 모델 입력의 비밀값 선별

검토 역할은 정적 분석 계획의 비밀값 분석 task가 끝난 뒤 실행된다. 각 역할은 시작할 때 그 결과를 읽는다([`screen.go`](../internal/worker/review/screen.go)). 비밀값 분석기 자체는 [분석기](ANALYZERS.md), 비밀정보 정책은 [보안](SECURITY.md)에 있다.

| 비밀값 task 결과 | 처리 |
|---|---|
| 계획되지 않았거나 미실행(`unsupported`/`not_run`) | 선별 없이 보낸다. 기록과 보고서에 `model_input_not_secret_screened`를 남긴다. 조용히 넘어가지 않는다 |
| 실행됐지만 쓸 수 없음(실패, 시간 초과, 취소, 부분 파싱, 산출물 누락·무효) | 검토를 건너뛰지 않는다. 지금까지 읽은 결과와 해석되는 실패·부분 envelope의 관찰 경로가 가리키는 파일은 계속 뺀다(`secret_files_withheld`). 나머지는 기본 읽기 필터만 거쳐 보내고 `model_input_not_secret_screened`를 남긴다. 아무것도 읽을 수 없을 때만 빼는 파일이 없다. 보고서는 미선별과 쓸 수 없던 작업·사유를 적는다 |
| 완전한 파싱으로 성공 | 보고된 파일을 모든 발췌(코드 문맥, 문서, 관찰 창, 검증 확장)에서 뺀다. `secret_files_withheld`를 남긴다 |

- 모든 발췌는 같은 읽기 경로(`readLines`)를 지나므로 새 입력 종류도 같은 선별을 받는다.
- 이와 별도로 민감값 패턴(`record.Sensitive`: 개인 키 블록, `sk-`·`gh?_` 형식 토큰, Authorization 헤더 값, 따옴표로 감싼 api_key·password·client_secret 값)에 걸린 파일은 발췌하지 않는다. 이 패턴은 완전한 비밀 탐지를 보장하지 않는다.
- 모델이 쓴 서술도 같은 패턴에 걸리면 기록에 남지 않는다(`record.SafeText`).

## 출력 검사

- 출력은 `workspace_proof`와 `result`를 가진 JSON 객체 하나여야 한다. `result`는 역할 계약의 엄격한 JSON이다. 계약의 목록 key는 모두 배열로 있어야 하고 알 수 없는 필드는 거부한다(`output_schema_invalid`).
- 그 호출에 준 근거·관찰 ID만 인용할 수 있다. 오류 코드: `evidence_id_unknown`, `evidence_id_duplicate`, `evidence_kind_invalid`, `evidence_missing`, `evidence_list_too_long`, `observation_id_unknown`, `evidence_not_at_observation`, `code_evidence_missing`.
- 탐색에서 정한 문서의 적용 경로보다 상위로 넓히면 거부한다(`scope_generalized`, `scope_unknown_path`, `scope_invalid`). 충돌의 두 쪽은 서로 다른 문서를 인용해야 한다(`conflict_same_document`).
- 실행·재현·테스트 통과를 주장하는 서술은 거부한다. 호출 오류 코드는 `execution_claim`이고, 단계 기록에는 `execution_claim_rejected` 한계가 남는다. 이 검사는 정규식 휴리스틱이다. 놓친 주장이 있어도 상태·재현·통계는 구조화 기록에서만 정하므로 모델 서술이 판정을 바꾸지 못한다.
- 서술 필드는 JSON 인코딩 400바이트 이하다(지침은 120자 이하를 요구한다). 기록에 남는 모델 서술은 2,400바이트 이하이고 민감값 패턴에 걸리지 않아야 한다.
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

모든 역할에 적용한다. 단계당 호출은 최대 3회(`record.RoleMaxCalls = 3`)이고 문서 계약 후속 검토는 최대 2회다. 각 호출은 attempt의 남은 예산을 확인한다.

1. **형식 복구.** 출력이 JSON이지만 계약·근거 검사에서 거부되면, 고정 오류 코드만 담은 복구 요청을 한 번 보낸다. verify는 거부된 후보, 허용된 근거와 오류 코드만 다시 보낸다.
2. **JSON이 아닌 응답의 재시도.** JSON이 아닌 응답은 항목에 대해 아무 정보도 주지 않는다. 형식 복구 기회를 쓰지 않고 한 번 더 요청한다. 재시도 요청은 같은 입력에 복구 규칙과 오류 코드 `output_not_json`을 담은 `repair` 블록을 붙인다. 앞서 복구 요청이 있었으면 그 오류 코드(verify는 후보별 오류)를 유지하고 `output_not_json`을 더한다.
3. **작업공간 확인값 불일치.** 완료 이벤트까지 검사한 모델 응답이 첨부의 확인값 검사를 통과하지 못하면 응답 전체를 버린다. 정리가 확인되고 단계와 attempt에 호출이 남아 있을 때 같은 요청을 새 디렉터리와 새 확인값으로 단계당 한 번 다시 보낸다. 기존 형식 복구가 있었다면 그 요청을 그대로 보낸다. 취소되거나 디렉터리·marker 쓰기, 공급자 호출 또는 정리에 실패하면 이 재시도를 하지 않는다. 원래 호출의 `workspace_unreachable` 사유와 제공된 사용량은 기록한다.
4. **끝.** JSON이 아닌 응답이나 형식 거부가 각각 두 번 발생하거나 호출 상한에 닿으면 다시 보내지 않는다. 미수락 응답은 저장하지 않는다. 탐색 사실과 도구 관찰은 남는다.
5. **관찰 ID만 틀린 verify 항목.** 항목의 오류가 `evidence_id_unknown` 하나뿐이고 범위 밖 ID가 모두 그 요청에 플랫폼이 넣은 관찰 ID일 때만, 그 ID를 빼고 같은 검사를 다시 한다. 통과하면 받아들이고 그 호출의 `output_errors`에 `evidence_id_dropped`를 남긴다. 게시되는 판정은 범위 안의 근거만 인용한다.
   - 요청에 없던 ID(지어낸 `ev_…` 등)가 하나라도 있으면 원래 오류로 거부하고 형식 복구로 보낸다.
   - ID를 빼고 통과하지 못하면 원래 오류로 거부한다. 관찰 ID만으로 지지한 항목은 코드 근거가 없어 거부된다.
6. **후속 호출 실패(verify).** 이미 수락한 판정이 하나 이상 있고, 형식 복구나 재시도 호출이 형식 이외의 이유(timeout, 거절, 모델 오류)로 실패하면 수락한 판정을 보존한다. 남은 후보는 격리하고 `verification_call_failed` 한계를 남긴다. task는 `failed`(`review_output_incomplete`)이고 실패 사유는 그 호출의 `outcome`에 남는다. 수락한 판정이 없거나 취소이면 단계 전체를 종료한다.
7. **verify 결과.** 일부만 유효하면 사용 가능한 판정을 보존하고 task는 `failed`(`review_output_incomplete`)다. 사용 가능한 판정이 전혀 없으면 `failed`(`review_output_invalid`)다.

## 실패와 task 결과

| 상황 | task 상태(사유) |
|---|---|
| 모델 전송 미승인 | `skipped_policy`(`model_source_not_approved`) |
| slice에 보낼 파일이 없음 | `skipped_policy`(`no_review_scope`) |
| 호출 상한으로 slice 거절 | `budget_exhausted`(`review_budget_exhausted`) |
| 호출 계수를 쓰거나 읽지 못함 | `failed`(`review_call_count_failed`) |
| 앞 역할 미수락 | `skipped_policy`(`dependency_failed`) |
| 모델 미설정 | `unsupported`(`model_not_configured`) |
| 오프라인 모드인데 사내 endpoint(`base_url`)가 없는 키·키 없는 공급자 모델 | `unsupported`(`model_endpoint_not_configured`) |
| API 키 파일이 없음·다른 사용자가 읽을 수 있음·공급자 항목 없음 | `unsupported`(`model_credential_unavailable`) |
| `--version` 응답이 OpenCode 릴리스 형식이 아님 | `unsupported`(`opencode_version_unsupported`) |
| OpenCode `--version` 확인 제한시간 초과 | `timed_out`(`opencode_version_timeout`) |
| 공급자가 키를 거부(HTTP 401·403) | `failed`(`model_auth_failed`) |
| 공급자에 그 모델이 없음(HTTP 404) | `failed`(`model_not_found`) |
| 사용량 제한(HTTP 429) | `failed`(`model_rate_limited`) |
| 그 밖의 OpenCode·공급자 오류 | `failed`(`model_unavailable`) |
| 작업공간 준비 실패·복구되지 않은 확인값 불일치 | `failed`(`workspace_unreachable`) |
| 예상 밖의 도구 호출·권한 이벤트 | `skipped_policy`(`policy_blocked`) |
| 모델 요청 제한시간 초과 | `timed_out`(`model_timeout`) |
| 취소 | `cancelled` |
| 입력을 만들 수 없음·스냅샷 무결성 불일치 | `failed`(`review_input_unavailable`) |
| 요청 한도 초과 | `failed`(`review_input_too_large`) |
| 보내지 못한 verify 후보 | `failed`(`review_input_incomplete`) |
| 출력 무효(복구 뒤에도) | `failed`(`review_output_invalid`) |
| verify 일부 판정만 유효 | `failed`(`review_output_incomplete`) |
| 지침 팩을 읽지 못함 | `failed`(`review_instructions_invalid`) |
| 기록을 쓰지 못함 | `failed`(`review_record_failed`) |

시험하지 않은 OpenCode 릴리스로 호출한 단계는 실행하고, 그 기록의 `limitations`에 `opencode_version_untested`를 남긴다(보고서 한계에 나온다). 모델 호출이 실패해도 정적 분석·탐색 task는 그대로 끝나고, 보고서는 `ai_review: not_performed`와 각 검토 task의 사유 코드를 싣는다.

실패한 모델 호출도 가능한 범위에서 시도 번호, 모델·OpenCode 버전, 지침 팩·요청 digest, 제공된 사용량을 보존한다. 상태와 부분 완료 여부는 워커가 결정한다. 통신 단절이나 모델 사용량 미제공은 미확인으로 남는다.

## OpenCode 어댑터 계약

모델은 관리형 로컬 OpenCode CLI로 호출한다([`client.go`](../internal/worker/opencode/client.go), [`executor/opencode.go`](../internal/worker/executor/opencode.go)). 실행 규칙(argv, 제한시간, 프로세스 트리 정리)은 [실행](EXECUTION.md)을 따른다. 원격 OpenCode attach나 별도 serve 연결은 구현하지 않았다. 추가할 때는 인증·버전·취소·workspace marker/digest를 대상 환경에서 검증한다.

- **버전.** 시험한 버전은 OpenCode 1.19.5다(`opencode.Version`). 호출마다 먼저 `--version`을 실행하며 확인에는 설정한 호출 제한시간과 30초 중 짧은 값을 적용한다. 실행 중인 버전 확인이 제한시간을 넘기면 모델 호출 전에 `opencode_version_timeout`을 기록한다. 확인과 요청은 호출당 전체 제한시간을 함께 사용한다. 다른 릴리스(`MAJOR.MINOR.PATCH[-…]`)도 실행하고 그 단계 기록에 `opencode_version_untested`를 남긴다. 릴리스 형식이 아닌 응답만 `opencode_version_unsupported`다. `codeye doctor`의 버전 확인은 모델 비용을 쓰지 않는다.
- **운영자 설정.** 실행 파일은 절대 경로다. 모델 ID는 `PROVIDER/MODEL`이다. provider는 소문자·숫자·`._-`, 모델 부분은 영숫자와 `._:@+-/`이고 `.`·`..` 경로 조각은 받지 않는다(라우터식 `vendor/model`은 받는다). 평가용 `codeye-gateway`의 Sol·Terra·Luna 모델은 relay 파일이 있을 때만 쓴다. 호출당 제한시간은 `--model-timeout`(기본 180초, 1초~30분)이다. 설정이 없거나 형식이 틀리면 요청을 보내지 않는다(`model_not_configured`). 공급자 변경은 운영자 선택이다([모델 설정](#모델-설정)).
- **작업 디렉터리와 workspace digest.** 요청마다 attempt scratch 아래 새 디렉터리를 만든다. 그 안의 `identity.txt`에 snapshot digest, 요청 digest, 무작위 32바이트 nonce에 묶인 확인값을 쓰고 첨부로 준다. 응답의 `workspace_proof`가 이 값과 다르면 `workspace_unreachable`이다. 프롬프트와 선택된 데이터는 표준 입력으로만 전달한다. 요청 digest는 정확한 메시지 텍스트의 SHA-256이다.
- **권한 차단.** `run --pure`로 실행하고, 실행별 HOME/TMP와 플랫폼이 만든 설정(`OPENCODE_CONFIG_CONTENT`)만 쓴다. 설정은 `permission: deny`, 모든 도구 `false`, share·snapshot·autoupdate·LSP·formatter·plugin 끔, 허용 provider 하나다. 프로젝트 설정, 기본 플러그인, 외부 skill, 모델 목록 조회, LSP 다운로드는 환경변수로 끈다. 사용자 설정과 대상 프로젝트 설정을 읽는 경로를 분리한다. 스냅샷 디렉터리는 보호 루트로 넘긴다.
- **응답 수집.** 한 세션·한 메시지의 완료된 응답만 받는다. `tool_use`·`permission` 이벤트는 `policy_blocked`, `error`는 `model_unavailable`, 세션 불일치·순서 위반은 `model_output_invalid`, 완료 사유가 `stop`이 아니거나 잘린 출력(64 KiB 초과)은 `model_output_incomplete`다. `reasoning` 이벤트는 버린다. 공급자 사용량 제한 메시지는 `model_rate_limited`로만 남긴다.
- **공급자와 인증.** 워커는 선택한 provider와 모델만 요청별 `OPENCODE_CONFIG_CONTENT`에 넣는다. OpenCode의 전송 방식·엔드포인트·인증은 [모델 설정](#모델-설정)을 따른다. 키 값은 argv에 넣지 않으며 실행 기록과 표준 출력·오류에서 가린다. 워커는 격리한 HOME과 환경변수로 실행한다.
- **relay 파일.** 예산 relay를 쓸 때 ready 파일은 일반 파일이고 그룹·기타 권한이 없어야 하며(Windows 제외) 4,096바이트 이하다. `base_url`은 `http://127.0.0.1:PORT/v1`, token은 32~256자다. OpenCode에는 이 임시 로컬 token만 넘기고 실행 기록에서 가린다. 상위 API 키는 relay만 가진다. relay 운영은 [평가](EVALUATION.md)에 있다.
- **취소.** 컨텍스트 취소·제한시간은 프로세스 트리 정리까지 확인한다. 정리를 확인하지 못하면 `recovery_required`로 남기고 작업 디렉터리를 지우지 않는다. 이미 전송된 공급자 추론은 취소 뒤에도 청구될 수 있다. 공급자 측 생성의 확정 중단은 보장하지 않는다.
- **실패 분류.** `opencode run`이 실패하면 JSON 오류 이벤트의 HTTP 상태와 오류 이름만 읽는다. 401·403은 `model_auth_failed`, 404는 `model_not_found`, 429는 `model_rate_limited`, 나머지는 `model_unavailable`이다. 공급자의 오류 문장은 남기지 않는다. OpenCode는 5xx를 몇 번 다시 시도하므로 실패까지 1분 넘게 걸릴 수 있다.
- **직접 확인.** `codeye model probe`는 합성 입력으로 실제 추론을 한 번 요청한다. 비용이 드는 명령이다. 인자 없이(또는 `--config PATH`) 실행하면 서비스 설정에서 `codeye start`와 같은 방법으로 모델을 정해 확인한다. `--executable ABSOLUTE_PATH --model PROVIDER/MODEL [--credential-file PATH | --relay-file PATH]`는 그 설정 그대로 확인한다. `--timeout`(기본 180초), `--json`. 출력은 provider, 모델, OpenCode 버전(시험한 버전인지), 성공 또는 실패한 단계(`configuration`·`credential`·`opencode`·`request`·`output`)와 사유 코드다. 실패하면 종료 코드 1이고, 분석은 그대로 실행된다.
- 네트워크 차단의 OS 수준 강제는 배포 환경에 의존한다([보안](SECURITY.md)).

## 모델 설정

**관리 → OpenCode 연결**에서 서비스 머신의 실행 파일, OpenCode 설정 파일, 인증 파일과 모델을 지정한다. `codeye up` 또는 `codeye start`로 띄운 서비스에서 관리자가 저장하면 다음 분석에 적용된다. **연결 확인**은 저장한 모델에 짧은 합성 요청을 한 번 보낸다. [화면에서 연결하기](GUIDE.md#관리자-화면에서-연결하기)

설정 파일은 OpenCode 1.x의 JSON·JSONC 형식이다. 모델을 비워 두면 파일의 `model`을 쓰고, 값을 입력하면 그 모델을 선택한다. 사내 OpenAI 호환 서버는 선택한 `provider`의 `npm`과 `options.baseURL`로 연결한다. 인증 파일을 함께 지정하면 그 파일의 provider 키를 사용한다. 키 파일은 서비스 계정만 읽을 수 있게 둔다. 관리자가 지정한 모델 연결 항목만 읽으며, 프로젝트의 플러그인이나 도구 권한은 실행 설정으로 가져오지 않는다.

- **에이전트.** `codeye agent --opencode ABSOLUTE_PATH --model PROVIDER/MODEL [--model-credential-file ABSOLUTE_PATH | --model-relay-file ABSOLUTE_PATH] [--model-timeout 180s]`. 키 파일 경로와 제한시간은 Hello의 `model_credential_file`, `model_timeout_seconds`로 워커에 간다. 키 파일 없이 고른 provider는 키가 필요 없는 경우(OpenCode Zen의 무료 모델)에만 답한다.
- **키 파일 형식.** CODEYE 키 파일 `{"schema_version":"0.1","provider":"openai","api_key":"…","base_url":"…","openai_compatible":false}`(`base_url`·`openai_compatible` 선택, 알 수 없는 필드 거부) 또는 OpenCode의 `auth.json`이다. `auth.json`은 모델 provider의 항목이 `{"type":"api","key":"…"}`일 때만 쓴다(OAuth 항목은 쓰지 않는다). `base_url`은 http·https URL이다. OpenAI 호환 서버, 프록시, 시험용 가짜 upstream에 쓴다.
- **OpenCode 설정 파일을 지정하지 않았을 때의 키.** 순서대로 찾는다. (1) `agent.model.credential_file`, (2) `<state_dir>/secrets/model.json`(0600, 디렉터리 0700), (3) 사용자의 OpenCode `auth.json`(`$XDG_DATA_HOME/opencode/auth.json`, 기본 `~/.local/share/opencode/auth.json`)에서 모델 provider의 `api` 항목. `agent.model.name`이 없으면 (3)은 아래 표의 provider를 표 순서로 찾는다. 환경변수 `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `OPENCODE_API_KEY` 중 처음 있는 것을 `codeye up`이 (2)에 쓴다(`instance.WriteModelSecretFromEnv`). 같은 키면 파일을 다시 쓰지 않는다.
- **OpenCode 설정 파일을 지정하지 않았을 때의 모델.** `agent.model.name`(`PROVIDER/MODEL`, 또는 키 provider의 모델 이름), 없으면 사용자 OpenCode 설정(`~/.config/opencode/opencode.json`, 주석 없는 JSON일 때)의 `model` 중 그 provider의 것, 없으면 provider별 기본값이다. OpenCode 실행 파일은 `agent.model.opencode`, 없으면 `~/.opencode/bin/opencode`, 없으면 PATH의 `opencode`다. 제한시간은 `agent.model.timeout_seconds`(1~1800, 기본 180)다.

| provider | 키 환경변수 | 기본 모델 |
|---|---|---|
| `openai` | `OPENAI_API_KEY` | `gpt-6.2-sol` |
| `anthropic` | `ANTHROPIC_API_KEY` | `claude-opus-5-5` |
| `opencode`(OpenCode Zen) | `OPENCODE_API_KEY` | `gpt-6.1-sol` |

- 키를 찾지 못하면 `agent.model`이 없을 때는 모델 없이 시작한다(검토 task `model_not_configured`). `agent.model`이 있는데 그 provider의 키가 없으면 모델 플래그는 넘기되 호출은 실패한다. 키 파일이 있으나 쓸 수 없거나(권한·형식), 기본 모델이 없는 provider에 이름이 없거나, OpenCode를 찾지 못하면 모델 없이 시작한다. 어느 경우든 정적 분석은 실행된다.
- 오프라인 모드에서는 관리자가 명시한 엔드포인트(OpenCode의 `options.baseURL`, CODEYE 키 파일의 `base_url` 또는 relay)를 사용한다. 환경변수의 키를 저장하지 않는다([오프라인 모드](OPERATIONS.md#오프라인-모드)).
- OpenCode는 플러그인을 꺼도 시작할 때 npm 레지스트리에 `@opencode-ai/plugin`을 묻는다(1.19.5 측정). CODEYE는 OpenCode 환경에 `npm_config_registry=http://127.0.0.1:9/`를 넣어 그 요청이 loopback에서 바로 실패하게 한다. `--ca-bundle`이 있으면 `NODE_EXTRA_CA_CERTS` 등 CA 변수도 넣는다.
- `codeye model probe`가 같은 해석으로 확인한다. `codeye up`은 모델 설정(모델 ID와 실행 파일 경로, 지정한 설정·인증 파일 정보)이 마지막 확인과 다를 때 한 번 확인하고 결과를 `<state_dir>/run/model-probe.json`에 둔다(키에서 나온 값은 없다).
- 이 표의 기본값 중 `gpt-6.2-sol`은 OpenCode 1.19.5의 오프라인 모델 목록에 없다. OpenCode가 요청을 OpenAI Responses API(`POST {base_url}/responses`, 스트리밍)로 보내는 것은 가짜 upstream으로 확인했고, OpenAI가 이 요청을 받는지는 실제 확인 한 번으로만 알 수 있다.

## 모델에 보낼 수 있는 소스

- 모델 전송은 노드 운영자가 로컬 소스 루트마다 허용한다. `codeye agent --model-source-root ROOT_ID`(반복 가능)다.
- `codeye up`이 띄운 에이전트는 프로젝트마다 고른다(관리자가 아닌 사람이 시작한 run은 관리자 스위치 안에서). 에이전트가 `--project-analysis-policy`로 뜨고, job에 실린 프로젝트의 분석 선택 `send_source_to_model`(로컬 폴더·Git 저장소 모두 기본 켬)이 모델 전송을 정한다. Git 저장소 프로젝트도 모델 검토를 받는다. `source_roots[].model_review`는 더 이상 쓰이지 않는다(A40). 모델이 설정되지 않았으면 선택과 관계없이 검토 단계가 `model_not_configured`로 끝난다.
- 실제 모델을 쓰려면 에이전트에 `--opencode ABSOLUTE_PATH --model PROVIDER/MODEL`과 키 파일(`--model-credential-file`)을 준다. 예산 relay를 쓸 때는 `--model-relay-file ABSOLUTE_PATH`를 준다.
- 일반 소스 매핑(`--source-root`)과 모델 전송 허가는 별개다. 허가가 없는 루트의 run은 모든 검토 task가 `model_source_not_approved`이고 정적 분석 결과만 남는다.
- Git 소스는 모델에 보낼 수 없다. Git 취득 run의 검토 task는 항상 `model_source_not_approved`다. Git 소스의 개별 모델 전송 승인은 설계 계약(미구현)이다.
- run이나 스냅샷의 어떤 내용도 이 허가를 올리지 못한다. 저장소 코드 실행 허가(`--repo-code-source-root`)와도 별개다([분석기](ANALYZERS.md)).

## 검토 기록

검토 task마다 산출물 하나를 role `other`, media type `application/vnd.codeye.review+json`으로 올린다. 크기는 256 KiB 이하다. 보고서 registry는 `schema_version`으로 decoder를 고른다. [스키마](../schemas/review-record.schema.json) · [예시](../examples/review/verify.json)(scripted caller로 만든 다섯 단계 기록 중 하나)

공통 규칙:

- 메타데이터와 모델의 판정문·근거 참조만 담는다. 발췌의 원문(`text`), 원시 프롬프트, 내부 추론, 프로세스 출력은 저장하지 않는다.
- 근거(`evidence`)는 `id`, `path`, `sha256`, `start_line`, `end_line`, `kind`, 선택 `origin`과 `symbol`을 가진다. `kind`는 code·test·document·manifest(환경 요구 원문)다. `origin`은 context·caller·test·observation(해석 창)이다. document·manifest 근거에는 origin이 없다.
- decoder(`record.Decode`)는 기록을 그 attempt의 run ID·attempt ID·snapshot manifest digest·단계와 대조한다. 근거마다 ID가 (snapshot digest, 경로, blob SHA-256, 행 범위)에서 다시 계산되는지, 경로가 manifest에 있고 blob이 같은지 확인한다. 모든 참조는 그 기록의 근거 목록 안에 있어야 한다. 허위 경로·행·blob·근거 ID, 다른 run·attempt·snapshot의 기록은 거부된다.
- 사용량이 없으면 `null`이다. 요청을 만들기 전에 실패하면 요청 digest가 빈 문자열일 수 있다.
- 실패한 기록은 시도한 호출, 준 입력, 앞 단계 상태를 남기고 모델 내용은 격리한다. 수락됐다가 버려진 호출의 `outcome`은 `result_discarded`다. verify가 실패하면 후보는 모두 `unreviewed`로 남는다.
- 보고서가 검토 완료로 판단하려면 DB task 성공과 검증된 역할 기록을 함께 확인한다.

### schema 0.2

- 단계마다 하나이고 자기 호출만 담는다. 필드는 `schema_version`, `pack`(id·version), `snapshot_manifest_digest`, `run_id`, `attempt_id`, `stage`, `outcome`(`accepted`·`failed`), `failure_reason`, `upstream`, `evidence`(최대 40), `observations`(최대 16), 역할별 내용, `limitations`, `calls`다.
- 역할별 내용: environment `environment_notes`, documents `contracts`·`conflicts`·`questions`·`instruction_notes`, investigate·interpret `candidates`(`investigate_1`~`4`, `interpret_1`~`4`), interpret `interpretation_limits`, verify `candidates`(앞 단계 후보 원문)와 `verifications`.
- `upstream`은 입력 하나마다 `source`, `state`(`accepted`·`skipped`·`failed`·`unavailable`), `reason`, 읽은 산출물 바이트의 `sha256`을 가진다. 탐색·도구 산출물과 앞 단계 기록 모두 이렇게 묶는다.
- `calls[]`: `role`, `model`, `opencode_version`, `pack_id`, `pack_version`, `pack_sha256`, `pack_files`, `request_sha256`, `request_bytes`, `usage`, `attempt`(1~3), `outcome`(`accepted`, `output_rejected`, `result_discarded` 또는 실패 사유 코드), `output_errors`.
- 모듈 ID는 `<ecosystem>:<path>`, 요구 ID는 `<kind>@<선언 파일 경로>` 형식이다.

### schema 0.3(slice)

- slice 기록(`review.investigate.sNNN`, `review.verify.sNNN`)만 쓴다. 0.2에 필수 `slice`(`key` `s001`~`s096`, `modules` 1~8개)를 더한 것이다. 단계는 investigate·verify뿐이다.
- slice investigate의 upstream은 `review.scope`도 이름으로 가질 수 있고, slice verify는 `review.investigate.sNNN`과 `review.scope`를 가진다. slice verify의 후보는 investigate 후보뿐이다.
- 0.2 기록에는 `slice`가 없고 `review.scope`·slice upstream을 가질 수 없다. `slice`는 생략 가능 필드라 0.2 기록의 바이트는 그대로다(`TestReviewExampleRecords`).
- decoder는 기록의 slice key와 단계가 task key와 같은지 확인한다(`record.Expect.Slice`).

### schema 0.4(문서 계약)

- 수락된 전체 investigate에 유효한 `contract_probe`가 있으면 0.4를 쓴다. `contract_index`와 선택한 `candidates`의 위치·줄·blob SHA-256, 선택 `literals`를 담는다. 기존 전체 investigate는 0.2를 쓴다.
- 마지막 contract 기록에는 `slice`와 후보·탐색 메모가 없다. `upstream`은 수락된 documents와 전체 investigate를 반드시 참조한다. 입력을 가져온 모듈 검토가 있으면 `review.scope`와 관련 slice 역할 기록을 함께 참조한다.
- 수락 기록의 `contract_comparisons`는 한 건이고 `calls`는 최대 두 건이다. 마지막 대조가 부분 일치이면 `partial_support`의 `matching`과 `violating`에 서로 다른 조건과 각 구현 근거 ID가 필요하다. 실패 기록은 시도한 호출과 발췌 메타데이터를 남긴다. 보고서가 앞선 기록의 digest와 계약 연결을 다시 검사한다. [선행 문서](../examples/review/contract/documents.json) · [선행 조사](../examples/review/contract/investigate.json) · [후속 대조](../examples/review/contract/contract.json)

### schema 0.5(문서의 HTTP 경로 선택)

- 수락된 `documents`에 유효한 경로 선택이 있으면 0.5를 쓴다. `workflow_selections`의 행은 `contract_index`의 오름차순이고 각 행의 `route_digests`는 선택 순서다. 한 계약 안에서 같은 digest를 반복할 수 없고 전체 선택은 네 건 이하다. 0.5 기록의 `evidence`는 문서 발췌다. [기록 예시](../examples/review/documents-workflow.json)
- 문서 계약은 수락됐고 유효한 경로 선택이 없을 때, 확인 가능한 경로가 여섯 건 이상이면 계약과 경로 목록만 담아 보충 요청을 한 번 보낼 수 있다. 문서 검토의 세 호출 안에서 보내고, 전체 조사·도구 해석·검증에 최대 아홉 호출을 남긴다. 보충 요청은 재시도하지 않으며 이미 수락된 문서 원문과 계약을 유지한다. 이 호출을 보낸 기록도 0.5를 쓰며, 기존 문서 응답 뒤에 `calls[].purpose=workflow_selection`으로 실제 요청과 결과를 남긴다.

## 보고서 연결

보고서 규칙의 전체는 [보고서](REPORT.md)에 있다. 검토 기록과 직접 맞물리는 규칙은 다음과 같다.

- 필수 검토 역할은 계획된 `review.*` task의 `required_for_verdict`로 정한다. 하나 이상이고 모두 수락됐을 때만 `ai_review: performed`, 하나도 없으면 `not_performed`다. 필수가 아닌 역할은 `coverage.ai_review.optional_tasks`에 결과와 사유를 적고 수행으로 세지 않는다. 검토 task가 없는 계획은 필수 역할이 없다.
- `coverage.review_calls`는 호출마다 `pack_id`·`pack_version`을, builder 6부터 호출이 든 task(`task_key`)를 싣는다. 상한은 207(호출 상한의 최댓값)이다. `llm_exposed_files`는 slice 기록이 보낸 경로도 센다.
- 앞 단계·탐색·도구 산출물의 SHA-256, 관찰 ID, verify 후보와 앞 단계 후보가 맞지 않으면 그 단계를 `review_lineage_mismatch:<task>`로 제외한다. 같은 발췌가 앞 단계마다 다른 출처 표시(`context`·`observation`)로 올 수 있으므로 어느 한 표시와 같으면 받는다.
- 모델이 서술한 숫자를 run 상태나 통계로 쓰지 않는다. 도구 관찰과 모델 후보는 서로 다른 산출물이다.

## 시험

- 회귀: `go test -race ./internal/worker/review/... ./internal/worker/run`. 실제 OpenCode와 가짜 upstream으로 `codeye start`의 키 파일 경로 전체(시작, probe, 검토 단계 수락, 키 거부 사유, 시험하지 않은 버전, 키 미노출)를 `python3 scripts/test_postgres.py --smokes-only --instance-model`이 확인한다(비용 없음). 예시와 부정 입력: `python3 scripts/validate_bundle.py`. 일반 CI는 유료 모델을 호출하지 않으며 scripted caller만 쓴다. scripted caller 결과는 플랫폼 검사의 근거이고 지침 품질의 근거가 아니다.
- 실모델 시험은 명시적으로 환경을 준 경우에만 실행한다. `CODEYE_TEST_OPENCODE_BIN`과 `CODEYE_TEST_REVIEW_RELAY`(필요하면 `CODEYE_TEST_REVIEW_MODEL`, `CODEYE_TEST_REVIEW_LEDGER`, `CODEYE_TEST_OPENCODE_MODEL`)를 주고 `go test -v -count=1 -run '^TestLiveVerifier$' ./internal/worker/review`처럼 하나씩 실행한다. 대상은 `TestLiveOpenCode`(키 파일은 `CODEYE_TEST_MODEL_CREDENTIAL`), `TestLiveInFlightCancel`(`internal/worker/opencode`)이다. 검토 역할 전체를 실모델로 돌리는 경로는 회귀 파일럿(`scripts/regression_pilot.py --pilot`, [평가](EVALUATION.md))이다. 비용이 든다. 시험 체계는 [시험](TESTING.md)에 있다.

## 한계

- 문서 계약이나 도구 진단과 연결할 수 있는 코드·테스트는 해당 줄 주변을 발췌한다. 나머지 파일은 앞부분을 보내며 파일 뒷부분이나 함수 전체는 보이지 않을 수 있다.
- 호출자 확장은 Go만 한다. Go가 아닌 후보의 파일 간 결함은 발췌에 들어간 범위에서만 보인다.
- 실행·재현 주장 거부와 민감값 패턴은 정규식이다. 다른 표현을 모두 잡지 못한다.
- 미완료가 사라진다는 보장은 없다. 끝내 검증되지 않은 후보는 `unreviewed`로 격리하고 task는 실패한다.
- `roleRules`는 "근거·관찰 ID"를 인용하라고 하고 verify 계약은 "근거 ID"만 말한다. 이 어긋남은 구조(관찰 ID 처리 규칙)로 막았고, 문구 정리는 다음 지침 개정에서 독립 작성자가 판단한다.
- 모델의 자신감과 서술은 측정된 정확도가 아니다. 품질은 [평가](EVALUATION.md)의 판정과 지표로만 말한다.

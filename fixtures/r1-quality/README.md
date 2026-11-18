# R1 다언어 보류 평가 사례

R1 검토 지침과 모델 구성을 평가할 때 쓰는 보류(holdout) 사례 집합이다. C/C++, Python, JavaScript/TypeScript, Go, Rust마다 결함 쌍 9개와 까다로운 정상 사례 2개가 있다. 모든 사례는 실제 도구로 실행되는 검사를 가진다. 결함 쌍의 before는 검사에 실패하고 after는 통과한다. 정상 사례는 통과한다. 평가 방법과 지표는 [16. 분석 품질 평가와 회귀 검증](../../docs/16-evaluation.md)을 따른다.

이 디렉터리는 평가 자료일 뿐이다. 플랫폼 기능이나 CI 단계로 연결되어 있지 않으며, 여기 있는 검사가 통과했다고 해서 CODEYE 플랫폼 구현이 통과한 것은 아니다.

## 독립성 규칙

- 사례는 검토 지침(`prompts/`), 지침 설계 문서, 과거 모델 결과를 보지 않고 작성했다. 지침이 사례에 맞춰지지 않게 하고, 사례가 지침에 맞춰지지 않게 하기 위해서다.
- R0 사례(`fixtures/r0-quality/`)의 결함 계열과 그 변형은 다시 쓰지 않았다. 형식만 참고했다.
- 코드는 모두 새로 작성했다. 공개 데이터셋이나 공개 저장소의 코드를 복사하지 않았다.
- 모델 입력이 되는 파일에는 정답을 암시하는 내용을 넣지 않는다. `BUG`·`FIXME`·`leak` 같은 주석, 결함을 이름으로 드러내는 식별자, 테스트 파일, 라벨 문장과 사례별 고유 표현, 변형 이름이 금지된다. `verify`와 `validate`는 이를 자동으로 검사하는 누설 검사를 돌리고, 일부러 심은 누설 4종을 잡아내는지 자체 시험도 한다.
- 계약(contract)은 실제 프로젝트처럼 코드의 문서 주석에 들어가고, `--documented`로 만들면 README에도 들어간다. 입력 도메인을 명시해서 계약 밖 입력에 대한 지적과 결함 지적을 구분할 수 있게 했다.

## 구성

| 언어 | 결함 쌍 | 까다로운 정상 | 여러 파일에 걸친 사례 | 검사 도구 |
|---|---|---|---|---|
| C/C++ (`cpp`) | 9 | 2 | 6 | g++ (C++17, ASan·UBSan) |
| Python (`python`) | 9 | 2 | 5 | CPython |
| JavaScript/TypeScript (`javascript`) | 9 | 2 | 8 | Node (TypeScript는 타입 제거 실행) |
| Go (`go`) | 9 | 2 | 5 | `go test` |
| Rust (`rust`) | 9 | 2 | 5 | rustc (라이브러리 crate와 검사 실행 파일) |

언어마다 7개 범주(오류 처리, 자원 수명·소유권, 동시성·비동기, 경계·산술, API·표준 라이브러리 오용, 상태·불변식, 입력 검증)를 모두 포함한다. 정확한 수는 `validate` 출력의 `counts`가 기준이다.

## 형식

`corpus.json`은 사례 배열이다. 사례 하나의 주요 필드는 다음과 같다.

| 필드 | 뜻 |
|---|---|
| `id` | `R1-<언어>-<번호>`. 정상 사례는 언어마다 `-10`, `-11`이다. |
| `language`, `split` | 언어와 분할. `split`은 항상 `holdout`이다. |
| `family`, `category` | 결함 계열(사례마다 고유)과 7개 범주 중 하나. |
| `kind` | `defect_pair` 또는 `tricky_normal`. |
| `detection` | `static_likely` 또는 `semantic_only`. 아래 정의를 따른다. |
| `cross_file` | 결함이나 반증을 판단하려면 두 파일 이상을 읽어야 하는지. |
| `project`, `contract`, `contract_file` | 만들어질 프로젝트 이름·요약, 계약 문장, 계약 주석이 들어갈 파일. |
| `files` | `shared`(공통), `before`, `after` 파일. 각 파일은 `{{CONTRACT}}` 표지 줄에 계약이 주입된다. |
| `check` | 입력 디렉터리 밖에 놓이는 검사 파일, 빌드·실행 argv, `failure_signature` 정규식, 제한시간. |
| `label` | `trigger`, `expected_impact`, `positive_locations`(경로·심볼·before 기준 줄 범위), 정상 사례의 `suspect_locations`, `known_counterevidence`, `corrected_variant`, `reviewer_notes`, `distinctive_tokens`. |
| `digests` | 변형별(`before`, `before_documented`, `after`, `after_documented`) 입력 트리 sha256. |

`detection`의 `static_likely`는 널리 쓰이는 분석기 규칙이나 컴파일러 경고가 해당 위치를 지적한다는 뜻이다. 규칙이 기본 설정에 들어 있지 않을 수도 있다. `reviewer_notes`에서 `Observed`나 `(observed)`로 표시한 내용만 도구와 버전을 적어 실제로 실행한 결과이고, `Expected but not run here`는 실행하지 않은 예상이다. `semantic_only`는 계약과 코드 의미를 함께 읽어야 판단할 수 있다는 뜻이다. 어느 쪽도 모델이나 도구의 측정된 정확도가 아니다.

작성 시점(2026-11-18)에 before 입력에 분석기를 한 번 실행한 결과는 다음과 같다.

- 기본 설정(g++ `-Wall -Wextra`, clang-tidy 기본 검사, ruff 기본 규칙, ESLint recommended, `go vet`, staticcheck, clippy)이 정답 위치를 지적한 사례: R1-CPP-04, R1-JS-02, R1-GO-06, R1-GO-09, R1-RS-09. 기본 설정이 지적한 `semantic_only` 사례는 없었다.
- 이름 붙은 규칙을 켜거나 더 엄격한 preset을 써야 지적된 사례: R1-CPP-02, R1-CPP-03, R1-PY-09(근처 위치), R1-JS-05, R1-JS-07.
- 실행하지 않은 보안 분석 질의에 기댄 예상: R1-JS-04, R1-GO-07.
- 정상 사례의 의심 위치를 지적해 오탐이 되는 경우: R1-CPP-11(bugprone-empty-catch), R1-PY-10(SIM115), R1-PY-11(BLE001).
- 처음에 `static_likely`로 예상했지만 실행한 도구가 지적하지 않아 `semantic_only`로 바꾼 사례: R1-CPP-05, R1-PY-06, R1-GO-03.

검사 결과는 구조화 데이터로만 분류한다. 빌드 단계는 종료 코드 0이어야 한다. 실행 결과는 다음과 같이 나뉜다.

- 통과: 종료 코드 0, 표준 출력에 `CHECK-PASS`, `CHECK-FAIL`과 `failure_signature` 없음.
- 위반: 종료 코드가 0이 아니고 출력이 `failure_signature`와 일치.
- 그 밖의 결과(빌드 실패, 서명 불일치, 제한시간 초과, 입력 트리 변경)는 오류이며 기대 결과로 인정하지 않는다.

## 검증 방법

표준 라이브러리만 쓰며 `python3 -I`로 실행한다. 도구는 플래그나 `CODEYE_TEST_GO`, `CODEYE_TEST_PYTHON`, `CODEYE_TEST_NODE`, `CODEYE_TEST_RUSTC`, `CODEYE_TEST_CXX` 환경변수의 절대 경로로만 받고 PATH에서 찾지 않는다. 도구가 없는 언어는 `SKIP`으로 보고하며 통과로 세지 않는다.

```sh
# 구조, 위치, digest, 누설 검사 (도구 불필요)
python3 -I fixtures/r1-quality/corpus.py validate

# 실제 도구로 before 실패, after 통과, 정상 사례 통과 확인
python3 -I fixtures/r1-quality/corpus.py verify \
  --go /abs/path/go --python /abs/path/python3 --node /abs/path/node \
  --rustc /abs/path/rustc --cxx /abs/path/g++ --require-all --repeat 3

# 모델 입력만 만들기 (입력 트리 digest 출력)
python3 -I fixtures/r1-quality/corpus.py materialize R1-GO-03 before /tmp/r1-go-03 --documented

# 보류 집합 봉인용 corpus digest
python3 -I fixtures/r1-quality/corpus.py digest
```

- 실행된 사례가 하나도 없으면 `verify`는 실패한다. `--require-all`은 건너뛴 언어나 사례가 하나라도 있으면 실패한다. `--language`, `--case`로 일부만 실행할 수 있다.
- Rust는 링크 단계에 C/C++ 드라이버가 필요해 `CODEYE_TEST_CXX`(또는 `--cxx`)도 있어야 실행된다.
- Go 검사는 `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOFLAGS=`, `CGO_ENABLED=0`, `-vet=off`로 실행한다. 네트워크와 모델 호출은 없다.
- 사례를 고친 뒤에는 `rehash`로 저장된 digest를 갱신하고, 변경 이유를 검토한 뒤 다시 `verify`한다.

## 보류 정책

- 이 사례들은 검토 지침을 조정하는 데 절대 쓰지 않는다.
- 어떤 지침 개정판에 대해 이 사례의 결과를 한 번이라도 보았다면, 그 개정판을 이 사례에 맞춰 다시 조정할 수 없다. 조정이 필요하면 새 개정판을 만들고, 이 사례가 아닌 개발용 자료로 조정한다.
- 모델 입력에는 `materialize`가 만든 파일만 넣는다. `corpus.json`, 이 README, 검사 파일은 모델에 보여 주지 않는다. 모델 입력이나 검색 대상에 정답이 섞이면 그 실험은 무효다.
- 평가 전에 `digest` 값을 기록해 봉인하고, 평가 보고서에 같은 값을 남긴다. 사례가 바뀌면 digest가 바뀌므로 다른 집합으로 취급한다.
- 결과가 공개되거나 지침 작성에 노출된 사례는 보류 집합에서 빼고 개발용으로 옮긴다.

## 알려진 한계

- C/C++ 검사는 g++ 15.2(libstdc++)에서만 실행했다. 이 환경의 clang++ 23은 기본 설정으로 libstdc++ 헤더를 찾지 못해 검사 실행에는 쓰지 않았다(정적 분석에는 `--gcc-install-dir`을 주어 사용했다).
- 새니타이저에 의존하는 검사가 있다. R1-CPP-01·02·10은 AddressSanitizer, R1-CPP-03은 UndefinedBehaviorSanitizer 보고를 실패 근거로 쓴다. 새니타이저가 없는 컴파일러에서는 결과가 다를 수 있다.
- R1-CPP-09는 libstdc++의 terminate 처리기가 출력하는 문구를 서명으로 쓴다. 다른 표준 라이브러리에서는 서명을 바꿔야 한다.
- 시간과 스레드 실행 순서에 기대는 검사가 있다(R1-JS-06, R1-JS-10, R1-GO-01, R1-GO-10, R1-CPP-05, R1-CPP-09, R1-CPP-10). 여유를 두었고 반복 실행으로 확인했지만, 매우 느리거나 부하가 큰 머신에서는 흔들릴 수 있다. R1-PY-04, R1-RS-01, R1-RS-08은 멈춤을 감지하는 감시 시간을 쓴다.
- R1-GO-06은 Go 런타임의 동시 map 쓰기 감지나 합계 불일치로 실패가 드러나며, 둘 다 스케줄링에 따라 확률적이다. 반복 실행에서는 매번 실패했다.
- Linux 전용 요소가 있다. R1-GO-03은 `RLIMIT_NOFILE`, R1-JS-11은 `/proc/self/fd`, R1-CPP-11은 `/dev/full`을 쓴다.
- TypeScript 사례는 Node의 타입 제거 실행만 확인했다. `tsc` 타입 검사는 검사 조건이 아니다.
- 검사는 계약의 일부만 확인한다. 검사 통과가 after나 정상 사례의 모든 측면이 옳다는 증명은 아니다.
- 사례 수가 언어마다 11개라 언어별 지표의 신뢰구간이 넓다. 범주별 비교는 참고용이다.
- 분석기 결과는 한 번 실행해 본 것이고 검사 조건이 아니다. 도구 버전이나 설정이 바뀌면 `detection` 분류가 달라질 수 있다.

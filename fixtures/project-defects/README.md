# 프로젝트 규모 결함 사례

공개 저장소의 실제 코드에서 CODEYE가 결함을 찾는지 재는 평가 사례 집합이다. 사례마다 공개 저장소의 커밋을 고정하고, 분석할 하위 디렉터리(범위)를 정한다. 결함 위치는 파일 앞부분 50줄 밖에 있고, 대부분 다른 파일(호출자, 피호출자, 설정, 타입 정의)을 함께 읽어야 알아볼 수 있다. 평가 방법과 지표는 [분석 품질 평가](../../docs/EVALUATION.md#프로젝트-규모-결함-집합)를 따른다.

이 디렉터리는 평가 자료이며 제품 기능이나 CI 단계와 연결되지 않는다. 여기의 검사를 통과했다고 해서 CODEYE 플랫폼 구현을 통과한 것은 아니다.

## 독립성 규칙

- 검토 지침(`prompts/`)을 작성하는 사람은 이 디렉터리를 읽지 않는다. 사례와 라벨을 다른 문서에 요약해 옮기지도 않는다.
- 사례 작성자는 검토 지침과 과거 모델 결과를 읽지 않고 사례를 만들었다.
- `holdout` 분할은 봉인한다. 봉인 digest는 [평가](../../docs/EVALUATION.md#보류-집합과-봉인)의 봉인 표와 `scripts/bundle_checks/docs.py`에 있다. `dev` 분할은 runner와 입력 개선을 조정하는 데 쓴다.
- 두 분할은 저장소와 결함 계열(`family`)이 겹치지 않는다. `validate`가 검사한다.

## 두 가지 사례

| 종류 | before 변형 | after 변형 | 라벨 출처 |
|---|---|---|---|
| `real_fix` | 실제 결함 수정 커밋의 부모 | 수정 커밋 | 수정 diff(`diffs/<ID>.diff`, 비시험 소스만) |
| `seeded` | 고정 커밋에 작은 패치(`patches/<ID>.patch`)를 적용한 트리 | 고정 커밋 그대로 | 패치가 바꾼 한 가지 동작 |

공개된 수정은 모델이 학습했을 수 있다. 심은 사례는 그 효과를 가르는 대조군이다. 두 종류의 결과를 따로 읽는다.

## 구성

| 경로 | 내용 |
|---|---|
| `cases/<ID>.json` | 사례 하나의 메타데이터와 라벨. 저장소 URL·커밋 SHA·라이선스, 범위, 분할, 범주, 계열, 저장한 트리 digest와 범위 줄 수, 라벨, 검사, 검증 상태 |
| `patches/<ID>.patch` | 심은 사례의 패치(고정 커밋 → before) |
| `diffs/<ID>.diff` | 실제 수정 사례의 수정 diff(before → after) |
| `checks/<ID>/` | 독립 입출력 검사나 수정 커밋이 추가한 시험의 사본. 검사 실행 때 작업 사본에만 복사한다 |
| `corpus.py` | 검사·digest·입력 생성·정답 목록 |

제3자 저장소의 소스는 저장소에 넣지 않는다. 패치·diff·검사 사본에는 그 저장소의 짧은 문맥 줄이 들어가며, 출처와 라이선스는 사례 파일의 `repo`에 적는다. 허용 라이선스(MIT, BSD, Apache-2.0, ISC 등)의 공개 프로젝트만 쓴다.

## 라벨

라벨은 분석 입력 밖(사례 파일)에만 있다. 필드는 `trigger`(발생 조건), `expected_impact`(영향), `positive_locations`(before 변형의 파일·줄 범위·앵커), `context_locations`(결함을 알아보는 데 필요한 다른 파일과 이유), `known_counterevidence`(반대 근거), `fix_summary`, `distinctive_tokens`다. after 변형의 대조 위치는 저장한 diff로 before 범위를 옮겨 계산하므로 네트워크가 없어도 채점할 수 있다.

## 선정 조건

- 범위 안 주 소스(시험·예제·문서·빌드 산출물 제외)가 1,500~6,000줄이다.
- 양성 위치는 모두 파일의 51번째 줄 이후이고 범위 안에 있으며, 적어도 하나가 diff의 before 쪽 변경 줄과 겹친다.
- `needs_cross_file`이면 양성·문맥 위치가 두 파일 이상을 가리킨다.
- 코드를 읽어 알아볼 수 있는 결함이다. 성능 수치나 외부 서비스에 기대지 않는다.
- 범주는 오류 처리, 자원 수명, 동시성, 경계·정확성, API 계약, 데이터 무결성, 입력에서 위험한 사용처까지의 보안 일곱 가지다. 언어는 Go, Python, JS/TS, Java, C/C++, Rust다.
- 라벨 문장의 5-gram과 고유 토큰은 두 변형의 트리 어디에도 나오지 않는다. 심은 패치의 추가 줄에는 정답을 암시하는 단어가 없다.

## 보류 분할 선정 규칙

`holdout` 분할은 사례를 고르기 전에 이 규칙을 먼저 고정하고 기록한다. 규칙을 적은 커밋이 사례를 하나라도 찾아보기 전의 기록이다. 사례 작성자는 검토 지침(`prompts/`), 평가 결과, 평가 산출물을 읽지 않았고 CODEYE를 어떤 후보에도 실행하지 않는다. 언어 toolchain과 프로젝트 자체의 시험은 결함과 수정을 확인하는 데만 쓴다. 이 절차를 바꾸려면 바꾸는 이유를 아래 "규칙 변경 기록"에 남긴다. 조용히 완화하지 않는다.

### 분할의 상태

| 분할 | 상태 |
|---|---|
| `dev` | runner와 입력을 조정하는 데 쓴다. 독립 평가가 아니다 |
| `holdout` | 봉인한다. 평가에 한 번 쓰기 전에는 아무도 사례 내용을 바꾸지 않고, 지침 작성자는 읽지 않는다 |
| `spent` | 이전 `holdout` 사례 아홉 개. 한 번 평가에 쓰여 봉인이 소진됐다. 독립 평가에 쓰지 않는다 |

### 구성과 균형

- 사례는 18개이고 Go, Python, JS/TS, Java, C/C++, Rust가 3개씩이다.
- 종류는 `real_fix` 9개와 `seeded` 9개다. 언어별 `real_fix`/`seeded`는 Go 2/1, Python 2/1, JS/TS 2/1, Java 1/2, C/C++ 1/2, Rust 1/2다. 한 언어에서 조건을 만족하는 `real_fix`를 찾지 못하면 조정자에게 묻고 결정을 규칙 변경 기록에 남긴다.
- 한 언어의 세 사례는 범주가 서로 다르다. 범주 일곱 가지마다 사례가 2~3개다(18 = 3×4 + 2×3).
- `needs_cross_file`은 14개 이상이다. 한 저장소는 사례 하나에만 쓰고, 어느 분할의 기존 사례와도 저장소와 계열 이름이 겹치지 않는다.

### 저장소 조건

- 라이선스는 위 허용 목록(`PERMISSIVE`)에 있고 GitHub API의 SPDX 값으로 확인한다. 보관(archived)된 저장소, 포크, 템플릿 저장소는 쓰지 않는다.
- 기본 브랜치에 2026-01-01 이후의 커밋이 있어야 한다.
- 범위는 빌드 매니페스트(`go.mod`, `Cargo.toml`, `package.json`, `pom.xml`, `build.gradle`, `pyproject.toml`·`setup.py`, `CMakeLists.txt`·`Makefile`)가 있는 디렉터리이거나 저장소 루트다. `real_fix`는 수정이 바꾼 비시험 소스를 모두 포함하는 가장 깊은 매니페스트 디렉터리(없으면 루트)이고, 그 범위의 주 소스가 before와 after 모두 1,500~6,000줄이 아니면 더 넓은 범위를 시도하지 않고 `scope_size`로 건너뛴다. `seeded`는 저장소 루트가 그 줄 수 안이면 루트이고, 아니면 매니페스트 디렉터리를 경로 순으로 읽어 처음으로 줄 수 안에 드는 곳이다. 범위 안 주 소스는 앞의 선정 조건대로 센다.
- 결함과 수정은 이 노드의 toolchain(Go, Python과 pytest, Node.js, OpenJDK `javac`·`java`, gcc·g++·CMake, rustc·cargo)만으로 확인한다. Maven·Gradle은 없으므로 Java는 순수 `javac`로 컴파일되는 코드(제3자 라이브러리 없이 범위의 소스로 닫히는 검사)만 쓴다. 의존성은 작업 디렉터리 안의 캐시로만 받고 홈 디렉터리에는 쓰지 않는다. 서비스·데이터베이스·장치·컨테이너가 있어야 하는 후보는 `needs_service`, 이 노드에서 실행할 수 없는 후보는 `not_verifiable`로 건너뛴다.

### 사례 형태

- 양성 위치는 모두 파일의 51번째 줄 이후이고, 앞의 선정 조건을 그대로 따른다.
- 제외: 서식, 문서만, 시험만, 의존성 올리기, 생성 코드, 코드를 읽지 않아도 알 만큼 사소한 수정, 실행 중인 서비스나 외부 서버에서만 보이는 결함, 성능 수치로만 보이는 결함.
- `real_fix`는 병합 날짜가 2026-01-01 이후인 커밋을 쓴다(아래 검색 절차 2의 2차 기준을 쓴 경우는 제외). 수정은 부모가 하나인 커밋 하나이고(squash·rebase 병합이나 한 커밋짜리 PR), 범위 안 비시험 소스 1~3개 파일의 변경 줄 80줄 이하이며, 시험은 수정에 포함된 것이나 독립 검사로 before 실패·after 통과를 이 노드에서 확인한다. 되돌림(revert)과 보안 권고(advisory)·CVE 번호가 붙은 수정은 쓰지 않는다.
- `seeded`는 고정 커밋에 한 가지 동작만 바꾸는 패치(변경 40줄 이하, 컴파일 가능)다. 고정 커밋은 기본 브랜치의 선택 시점 최신 커밋이고 2026-01-01 이후여야 한다. 심는 변경의 종류는 범주마다 아래 목록에서 고른다. 프로젝트의 시험이 패치를 잡는지는 `verification`에 기록한다.

| 범주 | 심는 변경의 종류 |
|---|---|
| `error_handling` | 오류를 삼키거나 다른 값으로 바꿔 돌려줌, 오류 경로에서 정리나 전파를 생략 |
| `resource_lifecycle` | 한 경로에서 닫기·해제를 빼거나 마지막 사용 앞으로 옮김 |
| `concurrency` | 공유 상태의 잠금·원자성·순서를 한 곳에서 느슨하게 함 |
| `boundary_correctness` | 경계 비교, 길이와 색인, 포함·제외를 한 곳에서 어긋나게 함 |
| `api_contract` | 호출자가 기대하는 반환·기본값·순서·nil 처리를 한 곳에서 바꿈 |
| `data_integrity` | 복사·별칭, 키·병합·덮어쓰기, 잘림으로 값이 다른 곳과 어긋나게 함 |
| `input_to_sink_security` | 입력과 위험한 사용처 사이의 검증·이스케이프·포함 검사를 한 곳에서 약하게 함 |

### 검색 절차

절차는 후보를 보기 전에 고정하며 후보 순서는 검색 결과 순서이고 기준에 맞는 첫 후보를 쓴다. 후보를 건너뛰면 이유를 목록(작업 중 기록)에 남기고, 보고에는 이유별 개수만 싣는다.

1. **언어 이름.** GitHub의 `language` 값은 Go는 `go`, Python은 `python`, JS/TS는 `javascript` 다음 `typescript`, Java는 `java`, C/C++는 `c` 다음 `c++`, Rust는 `rust`다. 여러 값이 있으면 순서대로 이어서 읽는다.
2. **`real_fix` 질의.** 범주마다 질의문이 하나다.

   | 순서 | 범주 | 질의문 |
   |---|---|---|
   | 1 | `concurrency` | `fix race condition` |
   | 2 | `resource_lifecycle` | `fix leak` |
   | 3 | `boundary_correctness` | `fix off-by-one` |
   | 4 | `error_handling` | `fix ignored error` |
   | 5 | `data_integrity` | `fix corrupt` |
   | 6 | `api_contract` | `fix return value` |
   | 7 | `input_to_sink_security` | `fix path traversal` |

   질의 실행: `gh search prs "<질의문>" --language <값> --merged --merged-at ">=2026-01-01" --match title --sort created --order asc --limit 100`. 언어 순서는 Go, Python, JS/TS, Java, C/C++, Rust이고 언어 i(0부터)는 질의 순서 i번째부터 일곱 개를 한 바퀴 돈다. 언어가 맡은 `real_fix` 수만큼, 질의 순서대로 질의마다 기준에 맞는 첫 PR을 하나 쓴다. 그 질의의 결과 100개에 기준에 맞는 PR이 없거나 범주가 이미 3개이거나 같은 언어에 이미 있는 범주이면 다음 질의로 간다. 일곱 질의를 모두 돌고도 그 언어가 못 채웠으면 **2차 기준**으로 같은 일곱 질의를 `--merged-at ">=2025-01-01"`로 다시 돌아 남은 슬롯만 채운다. 2차 기준을 쓴 사례는 사례 파일의 `verification.summary`와 보고에 표시한다. 그래도 못 채우면 멈추고 조정자에게 묻는다. 사례의 범주는 질의문이 아니라 수정이 실제로 고친 결함으로 정한다(위 표). 정한 범주가 이미 3개이거나 그 언어에 이미 있으면 그 PR은 건너뛴다.
3. **`seeded` 범주 정하기.** 모든 `real_fix`를 고른 뒤에 정한다. `real_fix`의 범주 개수를 센다. `seeded` 슬롯을 언어 순서(Go, Python, JS/TS, Java, C/C++, Rust)로 하나씩 채우며, 슬롯마다 지금까지 사례 수가 가장 적은 범주를 쓴다. 같은 수이면 위 표의 순서로 앞선 범주를 쓰고, 그 언어에 이미 있는 범주는 건너뛴다. 언어의 `seeded` 슬롯이 둘이면 앞 슬롯부터 채운다.
4. **`seeded` 저장소.** `gh search repos --language <값> --license <spdx> --stars 300..8000 --archived=false --updated ">=2026-01-01" --sort stars --order desc --limit 30`을 `mit`, `apache-2.0`, `bsd-3-clause`, `bsd-2-clause`, `isc` 순서로 실행해 합친 뒤 별 수가 많은 순(같으면 `소유자/이름` 순)으로 읽는다. 저장소 조건과 사례 형태(범위 줄 수, 결함을 심을 자리가 파일의 51번째 줄 이후이고 다른 파일을 읽어야 알아볼 수 있는 곳)를 만족하는 첫 저장소를 쓴다. 이미 쓴 저장소는 건너뛴다. 심을 자리는 범위 안의 주 소스 파일을 경로의 바이트 순으로, 파일 안에서는 줄 순으로 읽어 처음으로 다음을 모두 만족하는 곳이다. 자리는 파일의 51번째 줄 이후이고, 위 표의 해당 범주 변경 종류를 한 곳에 적용해도 컴파일되며, 함수가 여덟 줄 이상이고, 그 동작의 올바름이 범위 안 다른 파일의 정의나 호출을 읽어야 보인다(`needs_cross_file`). 이 자리를 만들지 못한 파일은 건너뛴다. 이렇게 자리가 없는 저장소는 다음 저장소로 간다. 모델의 반응을 추측해 자리를 고르지 않는다. 한 저장소에서 `needs_cross_file`인 자리가 없고 사례 전체의 `needs_cross_file` 14개가 이미 채워졌다면 다른 파일이 필요 없는 자리도 쓸 수 있다.
5. **후보의 검사 순서(가벼운 것부터).** 라이선스·보관·포크 → 이미 쓴 저장소 → 병합 날짜·커밋 모양 → 변경 파일 종류와 크기(서식·문서·시험·의존성·생성물 제외) → 범위 크기 → 양성 위치와 두 번째 파일 → 이 노드에서 결함·수정을 확인할 수 있는가. 앞 검사에 걸리면 뒤 검사는 하지 않는다. 걸린 이유는 `license`, `archived_or_fork`, `repo_used`, `not_single_commit`, `not_defect_fix`, `files_or_size`, `scope_size`, `positive_in_first_50`, `trivial`, `needs_service`, `not_verifiable`, `category_full`, `category_in_language`, `other` 중 하나로 기록한다.
6. **멈춤.** 열여덟 슬롯이 모두 채워지면 멈춘다. 채운 뒤에는 기준을 더 잘 만족하는 후보로 바꾸지 않는다.

### 봉인

모든 사례의 `verify`(가능하면 `--run-checks`)가 통과하면 `digest --split holdout`을 계산해 [평가](../../docs/EVALUATION.md#보류-집합과-봉인)의 봉인 표와 `scripts/bundle_checks/docs.py`의 `PROJECT_HOLDOUT_SEAL`에 기록한다. 기준 빌드는 봉인 시점의 `main` 커밋이다. 봉인한 뒤 사례를 고치면 새 digest로 다시 봉인하고 이전 값을 소진된 이력으로 남긴다.

### 규칙 변경 기록

(없음)

## 명령

```bash
python3 -I fixtures/project-defects/corpus.py validate            # 오프라인: 메타데이터·패치·라벨 위치·분할 규칙
python3 -I fixtures/project-defects/corpus.py digest --split holdout   # 오프라인: 봉인 digest
python3 -I fixtures/project-defects/corpus.py materialize PD-GO-01 before DEST --cache CACHE
python3 -I fixtures/project-defects/corpus.py verify --cache CACHE [--split S] [--case ID] \
  [--run-checks --go /abs/go --python /abs/python3 ...] [--network]
python3 -I fixtures/project-defects/corpus.py forbidden --split dev --cache CACHE \
  --allow-text prompts/review --allow-text ALLOW --output FILE
```

- `materialize`와 `verify`는 `CACHE`에 고정 커밋만 얕게 받아 둔다(네트워크 필요). 네트워크가 없고 캐시에 커밋이 없으면 이유와 함께 실패한다. 만든 트리에는 `.git`, 사례 ID, 변형 이름, 라벨이 없고 digest가 사례 파일에 저장한 값과 같아야 한다.
- `verify`는 pin이 해석되는지, 실제 수정 사례의 before가 수정 커밋의 유일한 부모인지, 저장한 diff·패치가 정확히 적용되는지, 트리 digest·범위 줄 수·앵커·라이선스 파일을 확인하고 라벨 누설을 검사한다. `--run-checks`는 사례의 검사를 두 변형의 작업 사본에서 실행해 before는 실패, after는 통과하는지 본다. 분석 입력 트리 자체에서는 빌드·시험을 실행하지 않는다.
- `verification.status`는 사례마다 무엇을 확인했는지 적는다. `verified`는 위 검사를 기록된 toolchain으로 실행해 기대대로 나온 경우뿐이다. 실행하지 못한 검사는 `partially_verified`나 `not_verified`와 그 이유로 남긴다.

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

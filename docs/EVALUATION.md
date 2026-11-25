# 분석 품질 평가

실제 결함을 찾고, 근거를 정확히 짚고, 사용자가 보고서에서 조치할 일을 찾을 수 있는지 평가한다. 실행한 버전·대상·지표는 [평가 결과](EVALUATION-RESULTS.md)에 기록한다.

| 확인할 일 | 절차 |
|---|---|
| 검토 지침 변경 뒤 회귀 확인 | [지침 개정](#지침-개정과-회귀-검증)과 [회귀 집합 실행](#회귀-집합-실행) |
| 같은 프로젝트에서 도구·모델 검토 비교 | [실험 구성](#실험-구성), [평가 runner](#평가-runner), [지표와 분모](#지표와-분모) |
| 유료 모델 실행과 비용 확인 | [유료 batch 절차](#유료-batch-절차), [예산 relay와 장부](#예산-relay와-장부) |
| 배포 판단 | [출시 판단](#출시-판단), [평가 한계](#평가-한계), [제품 수용 시험](TESTING.md) |

검토 역할·입출력은 [모델 검토](REVIEW.md)에 있다. 관련 요구사항은 REQ-010/011/013/027, NFR-007/008/010이고 수용 시험은 TC-100, TC-120~122, TC-128, TC-129다. [요구사항](REQUIREMENTS.md) · [수용 시험](TESTING.md)

## 평가 원칙

- 많은 경고, 긴 보고서, 많은 모델 호출을 품질로 보지 않는다. 경고 수나 에이전트 수 증가를 품질 향상으로 발표하지 않는다.
- 평가할 질문은 네 가지다. 실제로 중요한 문제를 찾는가, 정상 동작을 잘못 지적하지 않는가, 근거가 맞는가, 사용자가 조치할 수 있는가.
- 지표는 보고서·검토 기록·장부의 구조화 값과 판정에서만 나온다. 모델의 자신감이나 서술을 지표 원천으로 쓰지 않고, 측정된 정확도로 표현하지 않는다.
- 비용·속도와 정확도를 하나의 임의 점수로 합치지 않는다. 더 많은 비용을 쓴 조합을 지침 효과라고 하지 않는다.
- 평가를 실행하지 못했으면 미측정으로 기록한다. 예산 부족·모델 미승인으로 비교를 못 하면 후보 상태와 미측정 지표를 남긴다.

## 구현 상태

| 범위 | 상태 |
|---|---|
| 회귀 집합과 실행(`regression_pilot.py`), 보류 집합과 봉인 검사, 평가 runner(`tools`·`review` 조건, 정답 차단, holdout 등록부, 표본·예산 계획과 거절, 판정 양식, 판정 지표와 Wilson 구간, 반복 분산, 보고서 읽기 과제와 채점기), 예산 relay | 구현 |
| 프로젝트 규모 결함 집합(`fixtures/project-defects/`, 공개 저장소의 실제 수정과 심은 결함, dev·holdout 분할과 봉인), runner의 corpus·분할·범위 선택, `--build-root`, 줄 범위 위치 지표, 블라인드 판정 묶음 | 구현. dev 분할의 일곱 사례로 실모델 기준 측정이 있고, 그중 다섯 사례는 모듈 단위 검토 입력 변경의 전후 비교 한 번이 있다([평가 결과](EVALUATION-RESULTS.md)). 비교는 다섯 사례라 개선을 입증하지 못한다. 그중 일부 사례는 이후 요청 예산 실험과 지침 2.10.0 측정에도 쓰였고, 그 집계 관찰이 지침 작성자에게 전달되었으므로 조정에 쓴 사례다(독립 평가가 아니다). holdout 봉인은 아직 쓰지 않았다 |
| 아래 데이터셋 표 중 저장소에 corpus가 없는 집합(사용 허가된 개인 프로젝트의 수정, 레거시 프로젝트, 별도 공격/장애 corpus) | 평가 기준(설계). 공격/장애 경계는 단위 시험이 덮는다 |
| 도구만·모델 맥락만·도구+모델·도구+모델+반증의 네 갈래 비교 | 평가 기준(설계). runner 조건은 `tools`와 `review` 둘이다 |
| reversal quality 지표, CPU/RAM·캐시 기록 | 평가 기준(설계). 채점기가 내지 않는다. 철회 경로는 회귀 집합의 반례 입력에서 사람이 판정한다 |
| 독립 검토자와 불일치 조정 절차, 판정자 간 일치도 | 설계. 채점기는 판정 충돌을 미판정으로 세고 목록만 낸다 |
| precision/recall 출시 기준값 | 미정. 첫 라벨링 baseline 뒤 결정한다 |

## 데이터셋

| 집합 | 구성 | 용도 |
|---|---|---|
| 합성 결함 쌍 | 결함 전/수정 후, 동일 API, 발생 조건·정답·반례 | 알려진 결함 발견과 수정 후 오탐 |
| 정상/까다로운 코드 | 상위 방어, 소유권 이전, 의도된 예외, 비동기 종료 | 과도한 주장 억제 |
| 프로젝트 규모 결함 | 허용 라이선스 공개 저장소의 고정 커밋과 범위. 실제 결함 수정의 전후와, 고정 커밋에 한 동작만 바꾼 패치를 심은 전후([아래](#프로젝트-규모-결함-집합)) | 파일 뒷부분·다른 파일이 필요한 실제 코드의 결함 발견, 수정 후 재지적 |
| 실제 결함 수정 | 사용 허가된 개인 프로젝트의 수정 전후 | 현실적인 구조·맥락 검토 |
| 레거시 프로젝트 | 문서 없음, 불완전 build, mixed versions | 부분 분석·공백 전달 |
| 공격/장애 fixture | prompt injection, malformed output, invalid path, timeout | 실행·근거 경계 |
| 보고서 읽기 과제 | 동일 결과를 다른 구조로 표현 | 이해도·우선순위 판단 |

- 구성 목표는 언어별 결함/정상 10쌍 이상이다. 충분한 통계 검증을 보장하는 수가 아니다. 라이선스와 비공개 코드 반출 권한을 확인한다.
- 튜닝 집합과 보류 집합은 프로젝트·원인 family 기준으로 나눈다. 같은 결함의 변수명만 바꾼 쌍을 양쪽에 두지 않는다.
- 정답, 기대 finding, 수정 후 설명은 분석 대상 스냅샷 밖에 둔다. 프롬프트·검색 대상에 정답이 섞이면 그 실험은 무효다.
- 이미 내용을 점검했거나 지침 조정에 쓴 사례는 회귀 근거일 뿐 독립 평가가 아니다.

### 회귀 집합

[`fixtures/regression/corpus.json`](../fixtures/regression/corpus.json)의 Go 결함·수정 10쌍과 고정 입력이다. 모든 사례의 내용이 이미 점검되었으므로 회귀 집합으로만 쓴다(사례의 `split` 필드는 원래의 개발·보류 구분을 보존한다).

- 사례 필드: `id`, `split`, `family`, `contract`, `imports`, `before`, `after`, `check`, `label`. 모든 함수에 짧은 공개 계약 주석이 있다. corpus 순서로 짝수 번째 사례에는 같은 계약을 담은 README를 붙인다.
- 실행 입력에는 평가 라벨, 전후 구분, 확인용 테스트가 들어가지 않는다. 사례는 무작위 이름의 디렉터리에 만든다.
- 각 원래 구현은 별도 계약 시험에서 실패하고 수정 구현은 통과한다. `python3 fixtures/regression/corpus.py --check`가 모델 없이 이 계약 시험을 실행한다.
- 고정 입력(`scripts/regression_pilot.py`의 `FIXED_INPUTS`와 기본 입력):
  - `modules`: [`fixtures/modules/input`](../fixtures/modules/input). 루트 안내가 있는 저장소에 하위 모듈 둘(문서 없는 모듈, 루트와 상충하는 안내와 노드보다 높은 Go 버전 요구를 가진 모듈), 읽기 예산을 넘는 설계 문서, 권한 변경을 요구하는 에이전트 지침을 담는다. 정답 [`expected.json`](../fixtures/modules/expected.json)은 입력 밖에 있다. 평가 스냅샷에서는 개발 에이전트용 경고 주석 한 줄을 뺀다.
  - `caller-closes`: [`internal/worker/review/testdata/verify`](../internal/worker/review/testdata/verify). 상위 호출자가 자원을 닫는 반례로, 검증 역할의 철회 경로를 따로 채점한다.
  - `pilot`: 공개 CODEYE의 고정 커밋 사본. `git`: 공개 CODEYE HTTPS `main`(Git 소스이므로 모델 검토는 정책 제외로 끝나는 것이 정상이다).
- 탐지는 발생 조건과 잘못된 결과가 모두 맞아야 센다. 계약 밖 입력(예: 계약이 비어 있지 않은 입력이나 양의 분모를 명시한 경우)에 대한 경고는 수정 전 결함과 분리해 채점한다.

### 보류 집합과 봉인

보류 집합은 `fixtures/quality-holdout/`의 다언어 corpus다. C/C++·Python·JS/TS·Go·Rust 언어마다 결함 쌍 9개와 까다로운 정상 사례 2개, 총 55개 사례(변형 100개)다. 사례는 실제 toolchain으로 수정 전 실패·수정 후 통과(정상 사례는 통과)를 확인했고, 정답 문구가 분석 입력에 들어가지 않는지 자동 검사한다. 지침 작성자가 읽지 않는 자료이므로 이 문서는 사례 내용을 담지 않는다.

봉인 기록은 이곳에 둔다. 보류 집합을 바꾸면 새 digest와 날짜, 바꾼 이유를 이 표에 더하고 [`scripts/validate_bundle.py`](../scripts/validate_bundle.py)의 `sealed` 값을 같은 커밋에서 바꾼다.

| 봉인 | corpus digest(SHA-256) | 비고 |
|---|---|---|
| 현재 | `99904b7bdf1239eb3f0732a465a43ec51ff735594f15f23af11ec701bbc424be` | 사례 ID와 디렉터리 이름만 바꾼 뒤의 값이다. 사례 내용과 변형별 입력 digest는 그대로이고, corpus digest가 사례 ID를 포함하므로 값이 달라졌다 |
| 이름 변경 전 | `ab776f5f68cf34ece0dd32b0f18dab962d07fad1131a35c32a6149eebc045fe4` | 지침 조정에 쓰지 않았고, 작성자는 지침과 과거 모델 결과를 읽지 않았다. 첫 보류 평가가 이 값으로 실행됐다 |
| 프로젝트 규모 holdout | `d4cec596f2f7b17619113848663d7031f55102b244a9b77294e90ec71875107f` | [프로젝트 규모 결함 집합](#프로젝트-규모-결함-집합)의 `holdout` 분할(`corpus.py digest --split holdout`). 2026-11-23 처음 봉인했다. 작성자는 지침과 과거 모델 결과를 읽지 않았고, 아직 실모델 평가에 쓰지 않았다 |

- 확인: `python3 scripts/validate_bundle.py`가 `python3 -I fixtures/quality-holdout/corpus.py validate`와 `... digest`를 실행해 구조·위치·digest·누설 검사와 봉인 digest 일치를 확인한다. 프로젝트 규모 집합은 `fixtures/project-defects/corpus.py validate`와 `digest --split holdout`을 네트워크 없이 실행해 메타데이터·패치·라벨 위치와 holdout 봉인을 확인한다(값은 `scripts/bundle_checks/docs.py`의 `PROJECT_HOLDOUT_SEAL`). 불일치면 실패한다.
- 봉인을 바꾸면 이전 봉인으로 얻은 결과와 새 결과는 다른 집합의 결과다. 같은 표에서 비교하지 않는다.

### 프로젝트 규모 결함 집합

[`fixtures/project-defects/`](../fixtures/project-defects/README.md)는 허용 라이선스 공개 저장소의 실제 코드에서 결함을 찾는지 잰다. 합성 집합은 짧은 함수라서 파일 뒷부분에 있거나 두 번째 파일이 있어야 알아볼 수 있는 결함을 재지 못한다. 이 집합은 그런 결함만 고르므로 모델 입력의 범위·크기를 바꾸기 전후를 비교할 수 있다. 지침 작성자가 읽지 않는 자료이므로 이 문서는 사례 내용을 담지 않는다.

- 측정 상태: 변경 전 제품으로 dev 분할 사례 일곱을 실행했고(장부 상한에서 멈췄다), 변경 뒤 제품으로 그중 다섯을 실행해 같은 다섯 사례를 블라인드 판정으로 비교했다. dev의 나머지 사례(건너뛴 큰 사례 둘 포함)와 holdout 분할은 실행하지 않았다. holdout 봉인은 한 번도 쓰지 않았다. 수치와 한계는 [평가 결과](EVALUATION-RESULTS.md)에 있고, dev 분할 결과는 독립 평가가 아니다([보류 독립성](#보류-독립성)).
- 사례 하나는 저장소 URL·커밋 SHA·라이선스와 분석 범위(하위 디렉터리)를 고정한다. 범위 안 주 소스는 1,500~6,000줄이다. 실행은 사례마다 범위 경로를 run scope(`paths`)로 준다. 트리 전체(`.git` 제외)를 스냅샷으로 만들므로 범위 밖 파일도 문맥으로 읽을 수 있다.
- 종류는 둘이고 대략 절반씩이다. `real_fix`는 실제 결함 수정 커밋의 부모가 before, 수정 커밋이 after이고 라벨은 수정 diff에서 나온다. `seeded`는 고정 커밋에 한 동작만 바꾼 작은 패치를 적용한 트리가 before이고 고정 커밋이 after다. 공개된 수정은 모델이 학습했을 수 있으므로 심은 사례를 대조군으로 둔다.
- 선정 조건: 양성 위치는 모두 파일의 첫 50줄 밖이다. 대부분은 두 번째 파일(호출자·피호출자·설정·타입 정의)을 읽어야 알아볼 수 있고 `needs_cross_file`로 표시한다. 코드를 읽어 알아볼 수 있는 결함만 쓴다. 범주는 오류 처리·자원 수명·동시성·경계와 정확성·API 계약·데이터 무결성·입력에서 위험한 사용처까지의 보안이고, 언어는 Go·Python·JS/TS·Java·C/C++·Rust다.
- 분할: `dev` 10개 이상, `holdout` 8개 이상. 두 분할은 저장소와 결함 계열이 겹치지 않는다. 모든 사례에 두 변형이 있어 수정된 코드의 재지적을 잴 수 있다.
- 라벨(발생 조건·영향·양성 위치·문맥 위치·반대 근거·수정 요약·검증 상태)은 사례 파일에만 있다. 입력 트리에는 `.git`·사례 ID·변형 이름·라벨이 없고 디렉터리 이름은 무작위다. 라벨 문장의 5-gram과 고유 토큰이 두 트리에 나오면 `verify`가 실패한다. 심은 패치의 추가 줄에는 정답을 암시하는 단어가 없어야 한다.
- 검증: 사례마다 프로젝트의 기존 시험이나 독립 입출력 검사로 before 실패·after 통과를 확인하고, 실행한 toolchain과 결과를 `verification`에 남긴다. 실행하지 못한 검사는 `partially_verified`·`not_verified`로 남긴다. 사례별 상태는 `validate` 출력의 `counts.verification`에서 본다.
- 오프라인 검사(`validate`·`digest`)는 메타데이터·패치·diff만 읽는다. 입력을 만드는 `materialize`와 `verify`·`forbidden`은 `--cache` 디렉터리에 고정 커밋을 받아 둔다(네트워크 필요).

## 정답 라벨

- 각 사례는 ID, language, source digest, defect family, trigger, expected impact, positive locations, known counterevidence, corrected variant, reviewer notes를 갖는다.
- 문구가 expected message와 다르다는 이유만으로 오답 처리하지 않는다. 원인·조건·영향이 같은지 평가한다.
- 사람 판정은 `valid_actionable`, `valid_informational`, `false_positive`, `duplicate`, `insufficient_evidence`, `out_of_scope`다.
- 중요한 사례는 독립 검토자와 불일치 조정 절차를 둔다. 사용자 dismissal을 자동 오탐 정답으로 쓰지 않는다.

## 지표와 분모

모든 비율은 판정할 수 있는 단위만 분모에 넣고 판정할 수 없는 단위는 `unjudged`로 따로 센다. 비율마다 Wilson 95% 구간을 붙인다. precision은 점 추정만 내지 않고 표본 수·불확실성 구간·언어/원인별 편차를 함께 보고한다.

- 변형에 범위 안 finding이 하나도 없으면 판정 없이 "아님"이다. 판정된 finding 하나가 조건을 만족하면 "맞음"이다. 그 밖에 미판정 finding이 남아 있으면 미판정이다.
- 무효 행(정답 차단, 예산 거절, 입력 변경, 예외 ID 미확인, review 조건의 비밀값 선별 실패, 미실행)은 지표에서 빼고 사유별로 센다.

| 지표 | 단위·분모 | 분자와 제한 |
|---|---|---|
| known-defect recall | 결함 쌍의 before 변형 | `matches_label=true`이고 `valid_actionable`·`valid_informational`인 finding이 있는 변형. 모든 finding과 모델 finding만의 두 가지. 일반 프로젝트의 미지 결함 recall을 뜻하지 않는다 |
| same-defect re-flag | after 변형 | `matches_label=true`인 finding(같은 결함이 남았다는 주장)이 있는 변형 |
| false-positive rate | 정상 사례 변형, after 변형, 둘을 합친 것 | `false_positive` finding이 있는 변형. 범위는 actionable finding(`supported`·`reproduced`, open)과 모든 finding 두 가지. 알려지지 않은 코드 전체를 정상 분모로 쓰지 않는다 |
| actionable precision | 판정된 actionable finding | `valid_actionable`(엄격), `valid_*`(넓게). 미판정은 제외하고 수를 따로 낸다 |
| evidence validity | 게시 보고서 | `validate_bundle.check_report`로 새로 만든 입력과 대조해 통과한 보고서. 근거·claim 수를 함께 센다 |
| unsupported assertions | 판정된 finding / 구조 | `insufficient_evidence` 비율. 구조로는 실행 근거 없는 `reproduced`, 검증기가 거부한 출력의 오류 분류(꾸민 근거·범위 일반화·실행 주장), 통계 불일치 보고서 |
| duplicate burden | 판정된 finding / 구조 | `duplicate` 수. 구조로는 같은 변형에서 위치가 겹치는 finding 수 |
| reversal quality(설계) | 반례가 있는 후보 | 유효한 반대 근거로 철회한 수/반례 수, 실제 결함을 유지한 수. 채점기가 내지 않으며 사람이 판정한다 |
| report usefulness | 보고서 읽기 과제 | 영향·조건·수정/확인 방법을 독자가 정확히 찾는지(아래 보고서 읽기 평가) |
| 비용·시간 | batch | 검토 호출(역할·결과별), usage, 장부 요청 수·차감, batch 차감, 변형당 wall time(합·중앙값·최대). CPU/RAM과 캐시 여부는 설계이고 아직 기록하지 않는다 |
| 검토 결과 분류 | 보고서가 있는 모든 변형(무효 포함) | `review_outcomes`: `performed`, `secret_screen_failed`, `review_input_unavailable`, `model_source_not_approved`(tools 조건), 그 밖 `not_performed:<사유>`. 비밀값 선별 실패는 정책 결과이고 모델 실패가 아니므로 따로 센다. 모델이 실행되지 않은 변형을 모델이 놓친 것으로 세지 않는다 |
| 구조 지표 | 변형 | `auto_location_hit_before`, `auto_location_reflag_after`, `auto_suspect_flag_normal`, `auto_any_actionable_on_after_or_normal`, `ai_review_performed`, `model_saw_snapshot_files`(`llm_exposed_files>0`. README 같은 문서 발췌도 센다. 모델이 코드를 봤다는 뜻이 아니다) |
| 줄 범위 위치 지표 | before 변형, after 변형 | `location_lines`: 라벨 파일과 같은 경로이고 줄 범위가 ±`--line-slack`(기본 2) 안에서 라벨 줄과 겹치는 위치가 있는 변형. 모델 finding(finding 자체의 위치만. 인용한 발췌는 세지 않는다)과 도구 진단(보고서 `observations`)을 따로 낸다. before는 `positive_locations`, after는 저장한 diff로 옮긴 같은 범위다. 심볼·제목 대조는 쓰지 않는다. 범위가 큰 사례는 진단이 많아 우연히 겹칠 수 있으므로 위치 수·라벨 파일 안의 수·겹친 수를 함께 낸다. 판정이 아니다 |
| 코드 노출 | 변형 | 보존한 검토 기록의 근거 목록에서 낸다. `model_saw_code`(어느 역할 입력에 kind `code` 발췌가 있음), `labelled_file_in_investigate`(before 변형에서 라벨 결함 파일이 investigate 입력에 있음. recall의 상한을 읽는 값), `no_code_variants`. 모델이 답한(수락 또는 출력 거부) 호출이 있는 역할의 기록만 센다 |

- 분해: 언어, 범주(category), `detection`별로 recall·FP와 구조 지표를 낸다.
- 비교: `score A B ...`가 batch를 나란히 표로 낸다. `tools`와 `review`, 여러 팩을 같은 표에 둘 수 있다.
- 지표 개선을 위해 모든 발견을 숨기는 구성은 recall과 미확정 비율로 드러나야 한다.

## 실험 구성

- 도구만, 모델 맥락 검토만, 도구+모델, 도구+모델+반증을 같은 스냅샷과 예산 조건에서 비교한다(설계. 현재 runner는 `tools`와 `review` 두 조건을 같은 표에 낸다). 전체 token·call·time 예산 차이를 명시한다.
- 비결정적 모델은 대표 사례를 반복 실행해 분산을 본다. 한 번 깨끗한 결과로 실행마다 달라지는 문제가 해소됐다고 보지 않는다.
- 기록: 도구·규칙·팩 digest, 모델 endpoint identity/revision(가용 시), 어댑터 버전, 입력 선택, temperature 등 제공 설정, OS/runtime, 시간 제한, 캐시. 변경된 요소가 여러 개면 효과를 분리할 추가 실험을 계획한다.

### 분석 품질과 보고서 디자인 검토

진행자는 실제 CODEYE 서비스로 분석을 실행하고 대상 버전, 선택한 범위, 실행 정책, 모델·호출 한도와 도구 버전을 남긴다. 개선 전후를 비교할 때는 같은 입력을 사용한다. 보고서를 읽고 답할 질문과 비교할 동작은 실행 전에 정해 평가 자료로만 보관한다.

프로젝트와 회차마다 아래 다섯 세션을 새로 연다. 같은 실행의 결과를 사용하며, 각 세션은 다른 세션의 의견을 보기 전에 독립적으로 근거와 수정 우선순위를 제출한다.

| 담당 | 모델 | 검토할 질문 |
|---|---|---|
| 분석 범위와 공백 | GPT-6.2-Astra · high | 어떤 모듈과 파일을 발견했고 실제 내용을 얼마나 전달해 검토를 마쳤는가? 남은 범위의 원인과 결과에 미치는 영향이 드러나는가? |
| 문서와 구현 근거 | GPT-6.2-Astra · high | 문서의 명확성, 실제 동작의 일치·차이, 유지할 구현과 보완할 부분을 각각 근거로 설명하는가? 테스트 소스의 단언과 실행 결과가 구분되는가? |
| 정적 분석과 구조 | GPT-6.2-Astra · high | 어떤 검사와 규칙을 실제로 적용했는가? 형식·린트·타입 검사 결과와 복잡도·중복 위치가 유지보수 판단에 도움이 되는가? |
| CODEYE 자체 코드리뷰 | GPT-6.2-Astra · high | 플랫폼 소스와 변경 diff에서 실행·권한 경계, 결과 집계, 실패 처리, 이전 보고서 호환성을 추적한다. 결함 후보마다 위치와 검증 방법을 제시한다. |
| 보고서 디자인·사용성 | Fable-5.2 | 데스크톱·휴대폰에서 실제 보고서를 열어 첫 화면의 결론, 정보 순서, 글자·대비, 목차 이동, 표와 코드 펼치기를 점검한다. 반복 인용 때문에 서로 다른 판정을 혼동하는 곳이 있는가? |

- 진행자는 지적된 내용을 해당 소스, 원시 도구 결과 또는 렌더링 화면과 대조한다. 한 동작을 여러 번 언급한 횟수를 품질 점수로 합산하지 않는다. 실제 사람의 사용성 시험과 모델의 검토 결과는 구분한다.
- 구현은 GPT-6.2-Sol 작업 세션에 나누고 진행자가 통합한다. 같은 보고서 데이터를 다시 표시해 전달 방식의 변화만 확인한 경우와, 수정한 CODEYE로 분석을 다시 실행한 경우를 구분한다.
- 실행마다 요청한 모델, 실제 사용한 모델과 검토 완료 여부를 기록한다. 담당 모델을 호출할 수 없었던 항목은 미실시로 남긴다.
- 동작의 기준값은 보고서 밖에서 확인한다. 평가자가 별도로 수행한 시험은 CODEYE의 테스트 실적에 더하지 않는다. 도구 진단을 대조할 때에는 같은 커밋에서 의미가 같은 규칙의 파일·줄을 맞춘다.

디자인 검수에는 소스를 포함한 실제 보고서를 쓴다. 같은 실행에서 데스크톱과 모바일의 기본 화면, 항목 하나를 선택한 화면을 따로 확인한다.

| 읽기 과제 | 확인할 내용 |
|---|---|
| 종합에서 결과 파악 | 첫 화면에 영역별 결과 표가 보인다. 독자가 검사한 범위, 발견, 미실행을 구별하고 원하는 상세 내역으로 이동할 수 있다. |
| 상세 근거 확인 | 문서·코드·정적 분석 결과에서 항목 하나를 선택하면 그 판단에 해당하는 파일·줄과 짧은 설명이 보인다. 원문을 제공한 보고서에서는 같은 자리에서 코드를 읽을 수 있다. |
| 긴 목록 탐색 | 여러 항목이 같은 근거를 가리킬 때 개별 결과를 구별할 수 있다. 좁은 화면에서도 상태, 줄 번호, 펼치기 조작이 잘리지 않는다. |

### 한 프로젝트의 심층 검토 평가

하나의 사용자 동작을 골라 문서, 생성 코드, 호출자, 저장·조회 경계와 기존 테스트를 같은 커밋에서 고정한다. 개선 전후에 같은 경로·모델·호출 상한을 사용하고, 읽은 파일 수와 모든 줄을 읽은 파일 수를 나누어 기록한다. 더 높은 호출 한도로 전체 경로를 조사한 결과는 같은 비용의 전후 비교와 별도로 기록한다.

- **문서 대조**에서 양쪽이 추출한 같은 약속을 대응시킨다. 판정과 인용한 줄을 함께 비교한다. 필드 선언, 값을 만드는 분기, 최종 반환·저장은 각각 다른 근거다. 복합 약속은 확인된 조건과 남은 조건을 구별한다.
- **코드 리뷰**에서는 정상본과 한 가지 동작만 달라지는 격리 사본을 준비한다. 기존 테스트 또는 독립적인 입력·출력 확인으로 차이를 먼저 기록한다. 검토자에게는 정답을 주지 않는다. 실제 보고서의 발생 조건·결과·파일과 줄이 맞는지, 정상본에도 같은 지적을 냈는지 확인한다.
- **절 간 일관성**을 확인한다. 검증된 코드 발견에 나온 구현을 문서 절에서 찾지 못했다고 기록한 경우, 두 절이 받은 근거와 인용을 따로 대조한다. 새 근거가 도달한 건수, 올바른 판정으로 바뀐 건수, 미확인으로 남은 건수를 구별한다.

### 언어별 분석 깊이 비교

| 비교 | 같은 조건으로 고정할 것 | 확인할 결과 |
|---|---|---|
| 한 동작을 여러 언어로 구현 | 문서의 약속, 정상·실패 입력, 관찰할 값, 한 곳만 다른 수정 전후 코드 | 원인을 지목한 코드 줄, 실패를 입증한 테스트와 단언, 수정본에 같은 지적을 반복했는지 |
| 공개 프로젝트의 같은 커밋을 두 버전으로 분석 | 커밋, 분석 범위, 모델, 호출 한도, 테스트 실행 여부 | 대표 동작의 조건과 결과를 어디까지 답하는지, 문서·구현·테스트 중 실제 보고서가 인용한 근거 |
| 실제 도구와 테스트 연동 | 버전, 실행 가능한 도구·의존성, 대상 범위 | 실행 성공 여부와 발견 결과, 누락된 모듈, 실패·통과한 테스트 수와 원본의 해당 줄 |

판독에는 완전 답변·부분 답변·미포착·오답을 쓴다. 질문을 언제 정했고 모델에 전달했는지, 같은 결함을 여러 언어로 옮긴 대조인지, 처음 보는 프로젝트인지 함께 기록한다. 한 가지 결함으로 여섯 언어의 전반적인 분석 정확도를 추정하지 않는다.

### 관점별 코드 검토 비교

같은 입력을 같은 모델에 보내 일반 지침의 독립 조사 두 번과 관점을 나눈 조사 두 번을 비교한다. 각 비교군에는 같은 후보 검증을 적용한다. 입력 파일과 줄, 호출 수, 모델별 사용량, 수정 전후의 실제 동작을 기록한다. 서로 다른 문장으로 같은 원인을 두 번 지적한 경우와, 준비한 정답에 없지만 소스에서 확인되는 별도 문제를 구별해 채점한다. 호출이 늘어난 만큼 다른 파일 검토가 줄어드는지도 확인한다.

### 후속 검토의 판독

미확인 계약을 다시 조사한 경우에는 초기 판정, 선택한 동작, 실제로 더 읽은 코드·테스트의 줄, 최종 판정을 고정 커밋과 대조한다. 모델에 전달한 위치 후보와 실제 인용은 구별한다. 후속 호출이 없었던 프로젝트는 그 이유를 남기며, 이미 확인된 항목이나 질문이 보고서에 없던 경우를 개선 건수에 합산하지 않는다.

동일 프로젝트·동일 모델·동일 호출 한도의 전후 비교와 반복을 따로 기록한다. 보강에 쓰지 않은 새 저장소의 질문도 사전에 고정한다. 독자가 문서 설명으로 답한 경우, 구현으로 확인한 경우, 테스트 코드를 읽거나 실제로 실행한 경우를 구분한다.

## 출시 판단

- 프로젝트별 검토에서 소유자의 실제 질문, 처음 본 독자의 해석, 고정한 소스의 동작을 대조한다. 질문에 답한 사례와 답하지 못한 사례를 함께 보존한다. 문서와 구현을 구분할 수 있는지, 문제나 강점의 이유를 설명하고 해당 코드를 찾을 수 있는지, 구체적인 다음 행동을 고를 수 있는지가 보고서 판독의 기준이다.
- 외부 프로젝트 결과로 출시를 검토할 수준에 이르면 그 시점의 CODEYE 자체를 같은 절차로 분석한다. 자체 분석의 대상 버전과 도구 버전을 적고, 세 영역의 분석 검토, CODEYE 자체 코드리뷰, 디자인 검토를 마친 뒤 최종 판단을 남긴다. 개발 중 예전에 분석해 둔 자체 보고서는 기준선으로 사용한다.
- 게시되는 근거 참조의 구조적 유효성은 100%여야 한다. 무효 참조는 숨기고 제한을 표시해야 하므로 무효 링크를 그대로 게시하면 출시 차단이다.
- 실행 근거 없는 `reproduced`, 검사 실패를 0건 통과로 표시, 다른 프로젝트 데이터 노출, 늦게 온 결과의 덮어쓰기도 출시 차단이다.
- 분석 precision/recall 기준값은 첫 라벨링 baseline의 표본·분산을 보고 [결정](DECISIONS.md)에 확정한다. 측정 전에 임의 정확도 수치를 보장하지 않는다.
- 판정자 간 일치도를 측정하지 않았으면 그렇게 적는다.

## 지침 개정과 회귀 검증

- 검토 기준은 하나다(A52). 개정은 검토 지침(`prompts/review`) 하나에 한다. 지침 구성과 작성 규칙은 [모델 검토](REVIEW.md)에 있다.
- 지침 개정은 회귀 집합을 실모델로 **한 번** 실행해 검증한다. 대상 사례와 판정 기준(사전 기준)은 실행 전에 함께 정하고, 결과와 함께 [평가 결과](EVALUATION-RESULTS.md)에 기록한다. 결과를 보고 기준이나 대상을 바꾸지 않는다.
- 다음 차단 조건 중 하나라도 나오면 개정을 병합하지 않는다: 허위 근거, 실행 없이 재현을 단정한 서술, 통계 오염(모델 서술이 상태·수치에 들어감), 중요한 기존 사례의 승인되지 않은 성능 저하.
- 경고 수 감소만으로 개정을 받아들이지 않는다. 계약 밖 경고 부담을 줄이면서 수정 전 결함을 계속 찾는지 함께 본다.
- 회귀 한 번의 통과는 품질 우월의 근거가 아니다. 독립 품질 주장은 조정에 쓰지 않은 새 보류 사례로만 한다.
- 기록할 지표:

| 역할 | 대조할 사례 | 기록할 지표 |
|---|---|---|
| 환경·문서 | 중첩 모듈, 문서 없는 모듈, 상충하는 하위 지침, 선언 버전과 관측의 차이 | 사실의 출처·적용 범위 일치 수/판정 수, 누락·오귀속, 계약마다 독립 판정할 수 있는 조건과 결과의 유무 |
| 조사·도구 해석 | 기존 전후 쌍과 원시 진단, 실패한 검사 | 알려진 원인 포착, 수정 후 같은 주장 재발, 계약 밖 별도 경고, 원시 관찰 추적 여부, 계약 판정의 결정 경로와 근거 일치 여부 |
| 검증 | 상위 호출자가 자원을 닫는 반례, 근거 없는 후보 | 유효한 반대 근거로 철회한 수/반례 수, 실제 결함 유지 수, 무효 참조·재현 과장 |

## 보류 독립성

- 지침(프롬프트) 작성자는 [평가 결과](EVALUATION-RESULTS.md), `fixtures/quality-holdout/`, `fixtures/project-defects/`를 읽지 않는다. 저장소 밖 평가 결과 디렉터리, 판정 양식, 블라인드 판정 묶음, answer key도 읽지 않는다. 그 경로를 지나는 검색도 하지 않는다. 프로젝트 규모 집합은 `dev` 분할도 읽지 않는다. 지침 작성자가 dev 사례를 보면 그 지침의 dev 결과는 조정된 결과가 된다.
- 보류 사례나 결과를 본 사람(조정자)은 지침 문구를 쓰지 않는다. 결과에서 알게 된 실패는 지침 문구를 그 사례에 맞추지 않고, 가능하면 구조(플랫폼 검사)로 막는다.
- 지침 작성 지시에 보류 결과에서 얻은 관찰을 옮기지 않는다. 옮겼다면 그 사실을 결과와 함께 기록한다.
- 결과는 [평가 결과](EVALUATION-RESULTS.md)에만 둔다. 다른 문서, 코드 주석, 지침, 커밋 메시지에 결과 수치나 사례 내용을 옮기지 않는다.
- holdout 등록부(아래)가 같은 지침 id의 이전 digest 실행을 표시한다. 현재 보류 집합은 이미 한 번 평가에 쓰였다([평가 결과](EVALUATION-RESULTS.md)). 그 평가는 사례 ID를 바꾸기 전 corpus digest로 등록됐으므로 등록부가 이를 자동으로 연결하지 않는다. 같은 사례로 하는 이후 평가는 독립 비교가 아니고, 독립 보류 평가에는 새 사례(새 봉인)가 필요하다.

## 회귀 집합 실행

회귀 실행은 실제 PostgreSQL 컨트롤러, 별도 신원의 에이전트 두 대, 제품 워커, 정적 분석, 모델 검토, 보고서 게시·조회, 브라우저 내보내기까지 제품 경로 전체를 쓴다.

- 준비물: Go, PostgreSQL 도구(`initdb`, `pg_ctl`, `psql`), 잠금 파일대로 설치한 웹 의존성, Chromium, 고정 버전 OpenCode, 이미 시작한 예산 relay의 ready file. Go 도구는 설치된 로컬 모듈 캐시를 쓴다.
- 실행: `CODEYE_PILOT_RELAY_FILE`에 relay ready file을, `CODEYE_PILOT_RESULTS`에 새 비공개 결과 디렉터리를 지정하고 `python3 scripts/test_postgres.py --pilot`을 실행한다. 실제 모델 요청을 보낸다.
- 대상 한정: `CODEYE_PILOT_CASES`에 `Q01-before,Q01-after,...,modules,caller-closes,pilot,git`처럼 쉼표로 준다. 비우면 합성 쌍 전체, 고정 공개 커밋, 공개 HTTPS `main`을 실행한다. 고정 입력 `modules`·`caller-closes`는 명시할 때만 들어간다.
- 모듈 단위 slice: `CODEYE_PILOT_GITLEAKS`(gitleaks 절대 경로. 없으면 slice는 미선별로 실행되고 보고서에 미선별 공백이 남는다)와 `CODEYE_PILOT_REVIEW_MAX_CALLS`(비우면 에이전트 기본값 99)를 에이전트에 넘긴다. `CODEYE_PILOT_TOOLS`(공백으로 구분한 `name=path`, 예: `ruff=/abs/ruff node=/abs/node eslint=/abs/eslint/bin/eslint.js`)는 에이전트 플래그 `--name path`가 된다. 파일럿 사본은 Python·JS 모듈의 ruff·eslint가 판정에 필요하므로, 이 도구가 없으면 파일럿은 `partial`이고 아래 조건을 통과하지 못한다. `CODEYE_PILOT_LEDGER`를 주면 그 relay 장부도 검사한다. 실행 뒤 `check_coverage`가 다음을 확인하고 결과를 `coverage-check.json`에 남긴다. 하나라도 어긋나면 실행이 실패한다.
  - 짧은 합성 사례에서는 범위 장부를 만들 수 있고, 예약된 후속 조사·검증은 남은 줄이 없을 때 모델 호출 없이 끝난다. 검토 호출에는 선택한 언어 지침의 고정 digest가 기록된다.
  - 파일럿은 `completed`이고 AI 검토가 `performed`다. 보고서가 검토 산출물을 모두 읽었다(`unrecognized_artifact`·`artifact_rejected`·`review_lineage_mismatch` 등이 없다). 범위 산출물과 보고서 [장부](REPORT.md#모듈별-검토-장부)의 상한이 설정과 같고, 장부의 `calls_used`(slice 앞 attempt 호출 수와 slice 기록의 호출)가 알려져 있고 상한 이하다. `budget_exhausted` slice에는 기록도 호출도 없다. 후속 조사는 같은 파일에서도 앞선 조사에 없던 줄을 보낼 수 있다. 추가로 읽은 줄이 있어야 하고, 이미 조사한 줄을 다시 보내면 검증에 실패한다. 장부의 모델에 보낸 파일 합과 보고서의 `llm_exposed_files`는 조사에 실제로 쓰인 파일 수 이상이어야 한다.
  - 시험 장부(가짜 upstream)의 dry run에서는 두 원인을 실패가 아닌 `dry_run_expected`로 적는다. 가짜 upstream 응답이 거부된 검토 task(`review_output_invalid`, 이때 `llm_exposed_files`는 미확정)와 이 호스트에 설정하지 않은 필수 분석기(`tool_not_configured`) 때문에 파일럿이 `partial`인 경우다. 두 원인은 대개 함께 나타난다. 분석기가 없으면 `review.interpret`에 들어갈 관찰이 없고, 가짜 upstream은 관찰이 없을 때 항목 없는 응답을 돌려줘 거부된다. `CODEYE_PILOT_TOOLS`로 ruff·eslint·node를 주면 dry run 파일럿도 `completed`이고 `dry_run_expected`는 비어 있다. 다른 원인은 그대로 실패이고, 유료 실행에는 이 예외가 없다.
  - relay 장부에 `refused`·`blocked` 요청이 없다.
- 비용 없는 dry run: `python3 scripts/regression_pilot.py --dry-run --relay-file R --ledger L --results DIR --cases ... [--gitleaks P] [--max-calls N] [--tool name=path ...] [--build-root SRC] [--no-coverage-check]`. 장부 header가 `test-loopback`(relay `--test-upstream`)이 아니면 거부한다. 웹 빌드와 브라우저 내보내기는 하지 않는다. `--build-root`로 다른 checkout의 바이너리를 만들어 기준으로 삼고, `--compare-dumps BASE CAND`가 두 가짜 upstream `--dump` 디렉터리의 요청을 실행마다 다른 ID를 바꾼 뒤 다중집합으로 비교한다.
- 에이전트 두 대는 같은 호스트에서 풀 최소·최대 0과 2로 돈다. 합성 사례와 공개 사본의 트리 해시를 실행 전후에 대조한다.
- 감사: `python3 scripts/regression_pilot.py --audit-results "$CODEYE_PILOT_RESULTS"`가 보관된 보고서를 원본과 다시 대조한다(구조·통계·스냅샷, 주장과 근거의 파일 위치·줄 범위·blob SHA). Git 입력의 commit 객체가 로컬에 있어야 한다. 이 검사는 주장 자체가 옳다는 보증과 구분한다.
- 결과 디렉터리와 판정은 저장소 밖 비공개 경로에 둔다. 결과 요약은 [평가 결과](EVALUATION-RESULTS.md)에 쓴다.

## 평가 runner

[`scripts/quality_eval.py`](../scripts/quality_eval.py)는 보류 집합을 실제 controller·agent·분석기·OpenCode·relay 경로로 실행하고 결과를 보관한다. 채점은 [`scripts/quality_score.py`](../scripts/quality_score.py)가 한다. runner는 모델 키를 읽지 않고 relay를 시작하지 않는다.

| 하위 명령 | 역할 |
|---|---|
| `plan` | 층별 표본 선택과 호출·비용 추정만. 제품을 실행하지 않는다. `--ledger`를 주면 거절 판단까지 한다(거절이면 종료 코드 1) |
| `run` | batch 하나 실행. `--condition tools`(분석기만) 또는 `--condition review`(제품 전체 경로) |
| `score` | 판정 지표와 Wilson 구간, batch 나란히 비교 |
| `allow-text` | tools batch의 도구 메시지와 capture dry run의 envelope를 허용 텍스트로 모은다 |
| `capture-list` | 아무것도 막지 않는 감시용 목록(`purpose: envelope_capture`). 시험용 upstream 전용 |
| `reader` | 보고서 읽기 과제와 평가자용 answer key |
| `reader-score` | 사람의 답 채점. 답이 없으면 `not_measured` |
| `blind` | batch 하나의 블라인드 판정 묶음과 별도 answer key([블라인드 판정](#블라인드-판정)) |
| `blind-merge` | 판정한 묶음과 answer key를 `score`가 읽는 판정 파일로 되돌린다 |

**corpus와 분할.** `--corpus quality-holdout`(기본)과 `--corpus project-defects`가 있다. 프로젝트 규모 집합에는 `--split dev|holdout`과 `--cache DIR`(저장소 캐시, 처음에는 네트워크 필요)이 필수다. 분할을 생략해서 holdout을 실수로 실행하는 일이 없게 하기 위해서다. `plan`·`run`·`score`·`blind`·`reader`·`capture-list`가 같은 옵션을 받는다. corpus digest는 선택한 분할의 digest이고, 정답 목록은 `corpus.py forbidden --split S`로 같은 분할에서 만든다. 프로젝트 사례는 README를 덧붙이지 않고(저장소 자체 문서만), 변형마다 사례의 범위 경로를 run scope로 준다. 행에 `scope_paths`가 남는다.

**제품 빌드 위치.** `run --build-root DIR`은 다른 checkout(예: 기준 커밋)에서 제품 바이너리를 만들고, runner·corpus·채점은 지금 checkout을 쓴다. manifest의 `checkout`·`pack`(지침 트리 digest, holdout 등록부 항목)은 build root를 가리키고 `runner_checkout`이 runner의 커밋을 따로 남긴다. 기준 바이너리를 새 runner로 잴 때 결과가 어느 제품의 것인지 섞이지 않게 하기 위해서다. build root는 `git worktree add`로 만든 checkout이 좋다. `git archive`로 푼 트리처럼 Git 저장소가 아니면 빌드는 되지만 `checkout.commit`이 `null`로 남는다.

| 부분 | 위치 | 역할 |
|---|---|---|
| 정답 목록 | `fixtures/quality-holdout/corpus.py forbidden`, `fixtures/project-defects/corpus.py forbidden --split S --cache DIR` | 라벨 문구 n-gram과 고유 토큰 중 어떤 입력에도 나오지 않는 것을 JSON으로 내보낸다. 두 corpus가 같은 형식과 대조 규칙을 쓴다 |
| relay 차단 | `scripts/model_budget_relay.py --forbidden-phrases FILE [--allow-model-echo]` | 목록 항목이 든 요청을 예약·전달 없이 막는다 |
| 시험용 upstream | `--test-upstream http://127.0.0.1:PORT`, [`scripts/fake_model_upstream.py`](../scripts/fake_model_upstream.py) | dry run 전용 loopback 가짜. 키를 읽지 않는다. 응답은 요청 구조에서 만든 고정 문구이고 품질 정보가 없다. documents 역할은 처음 세 입력 문서마다 계약 하나를 말하고, 전체 실행 investigate는 입력 계약마다 대조(`contract_comparisons`)를 답한다(소스 안 API 문서는 문서화된 선언 본문의 한 줄을 범위로 인용한다). `--mode`는 `normal`·`expensive`·`fail`·`unauthorized`다. `--echo-text`는 운영자가 정한 문장을 investigate 후보 제목에 붙이고, `--dump DIR`은 요청을 저장하고, `--delay-seconds N`은 모든 응답을 N초 늦춘다(0~3600). `--shape-errors RATE`(0~1)는 항목의 일부가 복구 호출에서도 형식 오류(범위 없는 긴 인용, 반복 ID)를 내게 해 형식 복구 뒤 항목별 처리 경로를 실행한다. `--integrity-errors RATE`(0~1)는 verify 판정의 일부가 요청에 없던 근거 ID를 인용하게 해 제품이 계속 거부하는 무결성 오류 경로를 실행한다. 항목의 선택은 역할·스냅샷 digest·항목에서 정해져 복구 요청에도 같은 오류가 반복된다. TLS 옵션(`--tls-cert`, `--tls-key`)도 있다 |
| 공용 실행 틀 | `scripts/eval_harness.py`, `scripts/test_postgres.py`의 `temporary_postgres` | 임시 PostgreSQL, 제품 빌드, controller·agent, 보고서 조회. 회귀 실행(`regression_pilot.py`)도 같은 코드를 쓴다 |

### 조건

| 조건 | 실행 | 모델 | 대상 |
|---|---|---|---|
| `tools` | 분석기만. agent에 모델 설정을 주지 않는다 | 없음. 검토 task는 `skipped_policy`(`model_source_not_approved`) | 보통 corpus 전체 |
| `review` | 제품 전체 경로. 검토 지침은 실행 파일에 묶인 하나다 | 운영자가 시작한 relay | 표본 |

- 사례 변형마다 `corpus.py materialize`로 무작위 이름의 디렉터리에 입력만 만든다. 프로젝트 이름도 무작위(`eval-<hex>`)다. 사례 ID·변형 이름·라벨은 스냅샷에 들어가지 않는다. README 포함 여부는 corpus 순서로 고정(짝수 번째 사례)이고 모든 batch가 같은 선택을 쓴다.
- 분석기 플래그는 `--tool NAME=/abs/path`로 받아 agent에 `--NAME PATH`로 넘긴다. 값이 없는 agent 옵션(예: `python-both-type-checkers`)은 `--agent-flag NAME`으로 준다. 둘 다 manifest에 남고 tools 조건의 holdout digest에 들어간다. 이 빌드가 모르는 이름이면 agent가 사용 오류로 끝나고 runner가 멈춘다. 새 분석기 팩이 생기면 runner를 고치지 않고 플래그만 더한다. 분석기 공백은 보고서의 task(`pack-gap.<생태계>`, `tool_not_configured` 등)에서 읽는다.
- corpus source root에는 기본으로 저장소 코드 실행 허가(`--repo-code-source-root`)를 준다(build script를 실행하는 분석기가 있다). 서비스는 관리자가 아닌 사용자의 run에 build script를 기본으로 제공하지 않으므로(`repository_code_not_offered`), 허가를 줄 때 평가용 계정은 관리자로 만든다. `--no-repo-code-grant`로 둘 다 끄고, 그 선택은 manifest에 남는다. Java 분석기 플래그는 실행 스크립트가 아닌 jar를 가리킨다(`--tool pmd=…/lib/pmd-cli-<버전>.jar`, `--tool spotbugs=…/lib/spotbugs.jar`). `codeye start`로 띄운 서비스는 기본으로 의존성을 받지만 runner의 agent는 받지 않는다. 같은 조건을 재려면 `--agent-flag dependency-fetch`를 준다(네트워크가 필요하고, 받은 의존성 버전은 실행 시점에 따라 달라질 수 있다). 이 플래그가 없으면 Cargo 분석기와 형식 검사는 `dependency_unavailable`로 끝날 수 있다.
- 실패한 run도 자료로 기록한다. 보고서를 조회하지 못하면 `report_error`에 HTTP 상태와 오류 코드를 남긴다.
- `review` 조건은 사례를 하나씩 실행한다. 사례 전후의 장부를 비교해 호출·차감·차단·거절을 그 사례에 귀속하기 위해서다. `tools` 조건은 `--agents × --max-workers`개까지 동시에 실행한다. batch 전체 제한시간은 `--timeout`(기본 6시간)이다.
- `--dry-run`은 `--test-upstream` relay를 쓰는 batch다. 결과에 표시가 붙고 평가로 세지 않는다. `--plant-leak CASE:VARIANT`는 dry run 자기 시험용으로 그 입력 상단 주석에 라벨 문구를 심는다.

### run manifest

batch 디렉터리의 `manifest.json`에 다음을 남긴다. 비밀값과 relay token은 넣지 않고, 경로는 `~` 기준 상대 경로로 적는다.

- 팩 id·version·status, `prompts/review` 트리 digest, 보고서 `review_calls`에서 관측한 역할별 `pack_sha256`
- corpus digest, 모델 id와 OpenCode 버전, checkout commit과 수정 여부, OS·Python
- 분석기 실행 파일의 SHA-256, 보고서에서 관측한 분석기별 도구 버전, 저장소 코드 허가, toolchain 경로
- 장부의 시작·끝 상태와 batch 차감, 예약·여유, 계획 추정, 정답 목록 SHA-256·항목 수·허용 텍스트
- holdout 표지, 표본 선택(seed·층별 수), 반복 수, 시작·끝 시각, 중단 사유

### 정답 차단

모델 요청에 정답 문구가 들어가면 relay가 보내지 않고 장부에 남긴다.

**목록 만들기.** `corpus.py forbidden [--allow-text PATH ...] --output FILE`는 corpus의 누설 검사 규칙을 그대로 쓴다.

- 후보: 사례마다 라벨 문장(trigger, expected_impact, reviewer_notes, 반례 note, 의심 위치 note, 검사 요약)의 5-gram과 `distinctive_tokens`.
- 제외 1: 모든 사례의 모든 변형(README 포함·미포함) 입력 어디에든 나오는 후보. 다른 사례의 입력에 정상적으로 나오는 표현을 막지 않기 위해서다.
- 제외 2: `--allow-text`로 준 텍스트에 나오는 후보. 모델에 정당하게 가는 텍스트를 막지 않기 위해서다. 허용 텍스트의 이름(팩 기준 상대 경로)·SHA-256은 목록과 manifest에 남는다. 자동으로 추가하지 않는다.
- 제외된 후보는 ID로 남는다. `dropped.in_inputs`는 ID 목록, `dropped.in_allowed_text`는 ID·문구·제외한 허용 텍스트 이름이다. 지침 팩이 라벨 표현을 담고 있으면 여기서 보인다. 팩을 바꾸면 이 목록을 다시 확인한다.
- 일반 금지어(`BANNED_WORDS`·`BANNED_STEMS`)는 내보내지 않는다. 지침과 모델 출력에 흔한 단어라 정상 요청을 막기 때문이다.
- 대조 방식(`codeye-words-v1`): 한 단어 토큰은 단어 전체와 camelCase·snake_case 조각, 여러 단어는 `[a-z0-9]+` 단어열, 그 밖은 소문자 부분 문자열. relay와 corpus.py가 같은 규칙을 쓴다.

**허용 텍스트 만들기.** 모델에 가는 텍스트 중 입력이 아닌 것은 지침 팩(`prompts/`), 도구 진단, envelope(OpenCode 시스템 프롬프트와 Go 코드의 역할 계약) 세 가지다. envelope는 저장소 파일이 아니므로 dry run 요청에서 모은다.

1. `run --condition tools`로 도구 진단을 모은다.
2. `quality_eval.py capture-list --output FILE`로 감시용 목록을 만든다. relay는 `--test-upstream`일 때만 이 목록으로 시작하고, runner도 dry run이 아니면 거부한다.
3. 가짜 upstream(`--dump DIR`)과 감시용 목록으로 review dry run을 돌린다. 모든 역할의 요청이 막히지 않고 dump에 남는다.
4. `quality_eval.py allow-text --tools-batch B --request-dump DIR --output FILE`가 도구 메시지·규칙 ID와 envelope(system 메시지와 `<review-input>`을 뺀 user 텍스트)를 모은다. review input은 가져오지 않는다.
5. `corpus.py forbidden --allow-text prompts/review --allow-text FILE`로 목록을 만든다.

제품 코드·지침·분석기 구성이 바뀌면 envelope와 도구 진단도 바뀐다. 유료 batch 직전에 같은 checkout으로 목록을 다시 만든다.

**relay 동작.**

- `--forbidden-phrases FILE`이 없거나 비었거나 형식이 틀리면 시작하지 않는다(종료 코드 2).
- 시작할 때 목록 SHA-256·항목 수·corpus digest를 장부에 `forbidden` 기록으로 남긴다.
- JSON 요청을 해석한 뒤 전달할 메시지 텍스트를 하나씩 대조한다. 전달하지 않는 키(`tools` 등)는 대조하지 않는다.
- 일치하면 예약하지 않고 상위로 보내지 않는다. 장부에 `blocked`(일치 항목 ID 최대 16개, 수, 바이트, 메시지 수, 역할 표지)를 남기고 HTTP 403 `blocked_answer_leak`를 돌려준다. 차감은 0이고 라벨 문구 자체는 장부에 쓰지 않는다.
- 역할 표지는 프롬프트의 `Role: <역할>` 줄이다. 예약 기록에도 같은 표지가 붙어 역할별 요청 크기를 볼 수 있다.
- `--allow-model-echo`(선택, 기본 꺼짐): 이 relay 프로세스가 상위에서 받아 돌려준 답에 나온 항목 ID는 이후 요청에서 막지 않는다. 클라이언트가 보낸 assistant 메시지는 근거로 쓰지 않는다. ID는 메모리에만 두므로 재시작하면 다시 엄격해진다. 예외가 아닌 항목이 하나라도 있으면 요청 전체를 막는다. 예외 요청의 `reserve`에 `echo_matches`(최대 16개)·`echo_match_count`가 남는다. 목록 없이 이 옵션만 주면 시작하지 않는다.
- 예산·요청 수·정지로 거절하면 `refused`(사유 코드, 차감 0)를 남긴다.

**runner 처리.**

- review batch 시작 전에 relay 프로세스(ready file의 pid)가 이 목록을 현재 corpus digest로 읽었는지 장부에서 확인한다. 아니면 시작하지 않는다.
- 사례 실행 중 `blocked`가 생기면 그 사례를 `invalid: blocked_answer_leak`으로 빼고, 일치 항목·역할과 원인 분류(`model_echo`: 모든 항목이 그 사례의 모델 출력에도 있음, `input_or_instruction`: 그 밖)를 남긴다.
- `--allow-model-echo` relay에서는 예외 ID가 같은 사례의 모델 출력(수락된 단계 기록)에 있는지 다시 확인한다. 다른 사례에서 나온 표현이거나 ID 목록이 잘렸으면 `invalid: answer_echo_unverified`다.
- manifest의 `forbidden.allow_model_echo`와 `score`의 `answer_blocking.mode`(`strict`·`model_echo_allowed`)가 방식을 남긴다. 방식이 다른 batch를 비교하면 표 아래에 표시한다(빼는 변형이 다르다).

**잘못 막을 수 있는 경로.** 차단은 보수적이다. 정답이 아닌데 막히면 그 사례는 무효가 되고 표본이 준다. 앞 역할의 호출은 이미 차감된 뒤다.

- 역할 계약 문구(Go 코드의 envelope)가 사례의 고유 토큰과 같을 수 있다. envelope를 허용 텍스트에 넣어 푼다.
- 도구 진단 메시지가 고유 토큰과 같을 수 있다. tools batch의 도구 메시지를 허용 텍스트에 넣어 푼다.
- 지침 팩 문장이 라벨 표현을 담으면 그 항목은 목록에서 빠진다. 정답 누설은 아니지만 지침이 그 결함 종류를 직접 언급한다는 뜻이다.
- 모델 자신의 문장: investigate 후보의 제목·설명이 verify 입력에 그대로 들어간다. 엄격 방식에서는 결함을 맞힌 사례일수록 verify가 막혀 무효가 되므로, 남은 사례의 recall이 낮은 쪽으로 치우친다. 그래서 예외 방식을 선택 옵션으로 두었다. 기본값은 엄격 방식이다. 어느 방식을 쓸지는 유료 batch를 실행하는 사람이 정하고 결과에 함께 적는다.

### holdout 등록부

- `--results-root`(모든 batch 위의 고정 디렉터리)의 `holdout-registry.jsonl`에 (팩 id, 팩 트리 digest, corpus digest, 조건, 처음 실행 시각, batch, dry run 여부)를 한 번씩 덧붙인다. 지우거나 고치지 않는다.
- 같은 팩 id·같은 corpus에 다른 digest의 실제(dry run이 아닌) 실행이 먼저 있으면 이번 batch에 `holdout_seen_by_prior_revision`과 이전 digest를 표시한다. 실행은 막지 않는다.
- dry run 기록은 "본 것"으로 세지 않는다. 가짜 upstream의 출력에는 정답 정보가 없다.
- `score`는 batch 중 하나라도 표시되었거나 dry run이면 비교를 `independent: false`로 낸다. 결과 문서는 모든 batch에 표시가 없을 때만 독립 비교라고 쓸 수 있다.
- `tools` 조건은 팩 id `analyzers`, digest는 go와 분석기 실행 파일 SHA-256의 정규 JSON digest다.
- 표시는 같은 결과 루트 안에서만 동작한다. 다른 `--results-root`의 batch는 보이지 않으므로 모든 batch를 한 결과 루트에 둔다.

### 표본과 예산

**층별 표본.** `--sample N --seed S`는 언어 × kind × `detection`(`static_likely`/`semantic_only`) 층으로 나눈다. 층마다 N × (층 크기/전체)의 내림을 배정하고, N이 층 수 이상이면 층마다 최소 1개를 둔다. 나머지는 나머지가 큰 층부터 채운다. 층 안에서는 `random.Random(seed)`로 섞어 뽑는다. 같은 seed는 같은 표본이다. 선택 기록(seed, 층별 가능·선택 수)은 `plan.json`과 manifest에 남는다. `--cases`, `--languages`, `--kinds`, `--variants`로 범위를 좁힌다. 정상 사례에는 before 변형만 있다.

**계획.** `plan`과 `run`은 batch의 호출 수와 장부 차감을 추정한다.

- 기대값: 이력에 같은 변형이 있으면 그 변형의 역할별 호출 수, 없으면 이력의 변형당 평균(이력이 없으면 상수표) × 호출당 비용.
- 상한: 팩의 모든 역할이 변형마다 한 번 호출된다고 본다(이력에 그 변형의 호출 수가 더 많으면 그 수). 도구 진단 분류는 분류 호출마다 그 slot의 검증 호출 하나를 더한다. 형식 복구 10%를 더하고 호출당 비용에 여유 계수 1.5를 곱한다(`--margin`).
- 이력의 그 변형 호출 수가 역할 수보다 많으면 모듈 단위 slice가 계획된 변형이다. slice는 남은 호출이 있는 동안 승인되므로 형식 복구가 많으면 attempt 상한까지 쓸 수 있다. 이 변형의 상한은 `--review-max-calls`(기본 99, 에이전트에 그대로 넘긴다)회 × investigate·verify 중 비싼 호출당 비용 × 여유 계수다. 이력이 없는 변형은 slice 여부를 알 수 없으므로 같은 표본의 capture dry run을 먼저 돌려 이력으로 준다([검토](REVIEW.md#모듈-단위-검토-분할)).
- `plan`·`run`의 `--review-request-bytes`(40000~1048576)와 `--review-output-tokens`(1536~65536)는 `codeye agent`의 같은 이름 옵션으로 모든 에이전트에 넘어가는 요청 예산이다([요청 크기](REVIEW.md#요청-크기)). 주지 않으면 아무것도 넘기지 않아 제품 기본값이 적용되고 manifest와 등록부도 이전과 같다. 주면 manifest의 `agent_budget`과 holdout 등록부 항목에 적히고, 등록부는 예산이 다른 실행을 다른 구성으로 등록한다. review 조건에만 쓴다. 비용 추정은 이력의 입력 크기와 출력 토큰 가정을 그 예산으로 낮출 뿐이다. 예산이 작을 때 늘어나는 호출 수(slice)는 이력(`--history`)에서만 오므로 같은 예산의 dry run을 이력으로 준다.
- `--history DIR`은 manifest의 지침 id로 같은 지침의 batch만 쓴다. 쓸 수 있는 행이 없으면 상수표로 돌아가지 않고 오류로 끝난다.
- 호출당 비용의 출처 우선순위: (1) 실제 batch 이력이면 역할별 usage 평균 × relay 단가(입력 5, 출력 10 microUSD/token), (2) dry run 이력이면 역할별 최대 요청 바이트 ÷ 3(바이트당 토큰) × 5 + 출력 600 토큰 × 10, (3) 상수표(`quality_eval.py`의 `CONSTANTS`). 상수표는 단일 파일 Go 입력에서 나온 값이라 다파일·다언어 사례에서는 작을 수 있다.
- 바이트당 3토큰과 여유 계수 1.5는 실측에 맞춘 값이 아닌 보수적 근사다. 실제 batch의 usage를 이력으로 주면 그 값을 쓴다.
- 프로젝트 규모 집합은 범위가 커서 대부분의 변형이 모듈 slice를 계획한다. 이력이 없는 변형의 상한은 slice가 있다고 보고 `--review-max-calls` 회로 잡는다. 상수표의 기대값은 단일 파일 입력에서 나온 값이라 이 집합에서는 크게 작다. 같은 분할의 dry run(가짜 upstream)을 먼저 돌려 `--history`로 주고, 그 추정은 dry run을 실행한 제품의 입력 크기를 반영한다는 점을 결과와 함께 적는다. 입력 구성이 바뀐 제품에는 그 제품으로 dry run을 다시 한다.

**거절.**

- 시작 전: runner는 relay `--report`를 읽는다. 상한 차감이 `remaining_microusd − 예약`(`--reserve-usd`, 기본 $1.00)을 넘거나, 상한 호출 수가 `max_requests − requests − 5`(`--request-reserve`)를 넘으면 batch를 시작하지 않는다. 사유는 `plan.json`과 manifest에 남는다.
- relay의 예약·상한이 실제 보장이다. 계획은 batch가 중간에 끊겨 앞 호출 비용만 쓰는 일을 줄이는 사전 거절이다.
- 중간 정지: 사례마다 시작 전에 남은 금액·요청 수가 변형 하나의 상한에 미달하면 `budget_reserve_reached`로 멈춘다. 실행 중 장부에 `refused`가 생기면 그 사례를 `invalid: budget_refused`로 두고 `relay_refused_budget`으로 멈춘다. 실행하지 않은 변형은 `not_run:<사유>` 행으로 남는다.

### 채점

**자동 위치 대조(pre-fill).** 보고서의 finding(도구 진단, 모델 후보, 모든 validation 상태)마다 위치와 code 근거 위치를 라벨 위치와 비교한다.

- before 변형은 `positive_locations`, after 변형은 before 줄 범위를 diff로 옮긴 위치(`after_mapped`), 정상 사례는 `suspect_locations`와 비교한다.
- 같은 경로이고 줄 범위가 ±2줄(`--line-slack`) 안에서 겹치면 `lines`, 아니고 심볼 이름이 같거나 제목에 있으면 `symbol`이다.
- 위치가 맞는다는 것은 판정이 아니다. 원인·조건이 맞는지는 사람이 정한다. 위치 기반 비율은 `auto_*` 구조 지표로 따로 보고한다.

**판정 양식.** `run`이 끝나면 batch에 `judgments-template.json`을 만든다. 무효 행은 들어가지 않는다. 항목마다 라벨 요약(trigger, expected_impact, 대조 위치, 계약)과 finding 목록이 있고, finding마다 자동 대조 결과, 비어 있는 `verdict`(위 사람 판정 여섯 가지), 비어 있는 `matches_label`(라벨의 원인·조건과 같은 결함인가), `duplicate_of`, `notes`를 둔다.

- 양식에는 라벨 문장이 들어 있으므로 평가자 쪽에만 둔다.
- 판정 파일을 여러 개 주면 합친다. 같은 finding에 판정이 다르면 그 finding은 미판정으로 세고 충돌 목록에 남긴다. 한쪽을 임의로 고르지 않는다. 조정은 조정한 파일 하나를 다시 주는 방식이다.
- 판정 파일이 없으면 recall·FP·precision은 대부분 미판정으로 남는다. 판정자 간 일치도는 판정이 둘 이상일 때 충돌 목록으로만 보인다.

### 블라인드 판정

판정 양식은 라벨과 변형 이름을 보여 주므로 판정이 정답에 끌려갈 수 있다. `quality_eval.py blind BATCH --seed S --output DIR`(프로젝트 규모 집합이면 `--corpus project-defects --split S`)는 같은 finding을 판정자가 변형과 라벨을 모르는 상태로 보게 만든다.

| 파일 | 내용 | 누가 본다 |
|---|---|---|
| `packet.json` | 무효가 아닌 변형마다 불투명 ID(`U001`)의 단위와 그 finding(`F0001`). finding마다 제목·범주·심각도·검증 상태·생산자(모델/도구)·발생 조건·영향·주장·위치·인용 발췌, 비어 있는 `verdict`·`matches_label`·`duplicate_of`·`notes`. 순서는 seed로 섞는다. 사례 ID·변형 이름·키·run·snapshot·fingerprint ID와 라벨이 없다. 같은 사례의 두 변형은 같은 `reference_id`를 가진다 | 판정자 |
| `references.json` | `reference_id`마다 라벨 결함의 발생 조건·영향·파일(줄 범위 없음) | 판정자. 첫 판정을 마친 뒤에만 연다 |
| `answer-key.json` | 단위·finding ID를 batch 행 키·finding ID로 되돌리는 표, 참조 ID와 사례 | 평가자 |

- 판정은 두 번에 나눈다. 1차에는 `references.json`을 열지 않고 finding의 조건·영향·위치·발췌만으로 `verdict`(위 여섯 가지 판정)와 `duplicate_of`(같은 단위의 finding ID)를 정한다. 2차에 `references.json`을 열어 finding이 참조 결함의 원인·조건을 말하는지 `matches_label`을 정한다. 어느 단위가 결함판이고 어느 단위가 수정판인지는 묶음에 없다.
- `quality_eval.py blind-merge --packet 판정한-packet.json --key answer-key.json --output J.json`이 기존 판정 형식(`codeye-quality-judgments`, `blind: true`)을 만든다. 모르는 판정 값, 묶음에 없는 단위·finding, 다른 단위를 가리키는 `duplicate_of`는 거부한다. `score --judgments J.json`은 다른 판정 파일과 같이 읽는다.

### 반복 실행 분산

`--repeat N`(`--repeat-cases`로 사례 한정)은 같은 변형을 서로 다른 프로젝트로 N번 실행한다. `score`의 `variance`는 반복마다 자동 위치 대조 결과(최빈값 비율), actionable 수의 일치 비율과 값, finding fingerprint Jaccard 평균, `ai_review` 상태 일치를 낸다. 판정이 있으면 recall 단위의 결과도 반복별로 비교한다. 반복 수가 작으면 일치도는 참고값이다.

### 보고서 읽기 평가

- 독자에게 중요한 위험 3개, 각 발생 조건, 근거 위치, 분석하지 못한 범위, 다음 조치를 찾게 한다. 답의 정확성, 추가 질문 수, 소요 시간을 잰다. 차트 수나 문장 길이는 대리 지표가 아니다. 모호한 심각도, 중복, 수치 불일치, 과장된 단정은 결함으로 기록한다.
- `quality_eval.py reader BATCH --sample N --seed S --output DIR`는 무효가 아닌 첫 반복 보고서에서 kind·변형별로 고르게 N개를 뽑아 과제 `sheets/Snn.json`과 보고서 사본 `sheets/Snn-report.json`을 만든다. 파일 이름과 내용에 사례 ID·라벨이 없다.
- `answer-key.json`은 평가자용이다. before 변형은 라벨 조건·영향·위치와 참고 조치, 그 밖은 "라벨 결함 없음"과 반례 위치를 담는다. 분석하지 못한 범위는 보고서 구조(미지원·생략·실패 task, 제약 코드, `ai_review`, `llm_exposed_files`)에서 만든다.
- `reader-score --key K --answers A ...`는 결함 위치를 상위 3개 위험에서 찾은 비율, 반례 위치를 결함으로 지목한 비율, 공백 범위 재현율, 사람이 채점한 조건·조치 정답률(`graded`), 시간 중앙값, 추가 질문 평균을 낸다.
- 답이 없으면 결과는 `not_measured`다. 모델로 독자를 흉내 내지 않는다.

### 단위 시험

`scripts/test_model_budget_relay.py`(relay)와 `scripts/test_quality_eval.py`(runner·채점)는 모델·키·네트워크 없이 돌고, CI의 문서 계약 단계가 `python -m unittest discover`로 실행한다. 실행: `python3 -m unittest discover -s scripts -p 'test_model_budget_relay.py' -v`, `python3 -m unittest discover -s scripts -p 'test_quality_eval.py' -v`. dry run은 runner·relay·채점 경로가 동작한다는 근거일 뿐이고 가짜 upstream의 위치 대조·검토 수·차감에는 품질 의미가 없다.

## 유료 batch 절차

키는 relay를 시작하는 운영자의 셸에만 있다. runner는 relay를 시작하지 않고 키를 읽지 않는다. 아래는 저장소 루트에서 실행한다. `$R`은 영구 비공개 결과 루트이고, 모든 batch와 holdout 등록부가 같은 곳에 있어야 한다. `$LEDGER`는 예산 기간 동안 공유하는 relay 장부다. 도구 경로는 노드에 맞게 바꾸고, tools와 review 조건에 같은 도구 플래그를 준다.

```bash
# 노드에 맞게 도구 경로를 정한다. 임시 PostgreSQL을 쓰므로 PostgreSQL 도구(initdb, pg_ctl, psql)가 PATH에 있어야 한다.
TOOLS=(--go "$GO" --tool staticcheck="$STATICCHECK" --tool cargo="$CARGO" --tool clippy="$CLIPPY"
       --tool cppcheck="$CPPCHECK" --tool clang-tidy="$CLANG_TIDY"
       --tool ruff="$RUFF" --tool mypy="$MYPY" --tool bandit="$BANDIT" --tool pyright="$PYRIGHT_JS"
       --agent-flag python-both-type-checkers
       --tool lizard="$LIZARD" --tool jscpd="$JSCPD_JS" --tool gitleaks="$GITLEAKS"
       --tool node="$NODE" --tool eslint="$ESLINT_JS" --tool tsc="$TSC_JS")
REVIEW=(--opencode "$OPENCODE" --model codeye-gateway/gpt-6.2-sol)
SEL=(--sample N --seed S)   # 표본. 5단계 plan으로 예산 안인지 확인한다

# 1. tools(모델 없음): 전체와 비교용 표본
python3 scripts/quality_eval.py run --condition tools --results-root "$R" --batch tools-all "${TOOLS[@]}" --max-workers 3
python3 scripts/quality_eval.py run --condition tools --results-root "$R" --batch tools-sel "${SEL[@]}" "${TOOLS[@]}"

# 2. envelope 수집 dry run(가짜 upstream, 시험 장부, 키 없음). 이 batch가 계획 이력도 된다.
python3 scripts/quality_eval.py capture-list --output "$R/capture-list.json"
mkdir -p "$R/cap"
env -u OPENAI_API_KEY python3 scripts/fake_model_upstream.py --ready-file "$R/cap/fake.json" --dump "$R/cap/dump" &
FAKE_PID=$!
until [ -s "$R/cap/fake.json" ]; do sleep 0.2; done
PORT=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["port"])' "$R/cap/fake.json")
env -u OPENAI_API_KEY python3 scripts/model_budget_relay.py --ready-file "$R/cap/relay.json" \
  --ledger "$R/cap/ledger.jsonl" --model gpt-6.2-sol --forbidden-phrases "$R/capture-list.json" \
  --test-upstream "http://127.0.0.1:$PORT" &
CAP_RELAY_PID=$!
until [ -s "$R/cap/relay.json" ]; do sleep 0.2; done
python3 scripts/quality_eval.py run --condition review --dry-run --results-root "$R" \
  --batch capture "${SEL[@]}" --relay-file "$R/cap/relay.json" --ledger "$R/cap/ledger.jsonl" \
  --forbidden-phrases "$R/capture-list.json" "${TOOLS[@]}" "${REVIEW[@]}"
kill $CAP_RELAY_PID $FAKE_PID

# 3. 정답 목록. 출력의 dropped.in_allowed_text에서 허용 텍스트가 뺀 라벨 표현을 확인한다.
python3 scripts/quality_eval.py allow-text --tools-batch "$R/tools-all" --request-dump "$R/cap/dump" --output "$R/allow.txt"
python3 -I fixtures/quality-holdout/corpus.py forbidden --allow-text prompts/review \
  --allow-text "$R/allow.txt" --output "$R/forbidden.json"

# 4. 실제 relay(운영자 셸, 키는 이 프로세스만 읽는다). --allow-model-echo는 선택이다.
READY="$R/relay-$(date +%s).json"
python3 scripts/model_budget_relay.py --ready-file "$READY" --ledger "$LEDGER" \
  --model gpt-6.2-sol --forbidden-phrases "$R/forbidden.json" [--allow-model-echo] &
until [ -s "$READY" ]; do sleep 0.2; done
RELAY=(--relay-file "$READY" --ledger "$LEDGER" --forbidden-phrases "$R/forbidden.json")

# 5. 계획과 장부 확인(거절이면 종료 코드 1, 출력의 refusal에 사유)
python3 scripts/quality_eval.py plan "${SEL[@]}" --history "$R/capture" --ledger "$LEDGER"

# 6. 유료 batch
python3 scripts/quality_eval.py run --condition review --results-root "$R" --batch paid \
  "${SEL[@]}" --history "$R/capture" "${RELAY[@]}" "${TOOLS[@]}" "${REVIEW[@]}"

# 7. 판정과 채점: 각 batch의 judgments-template.json을 채운 뒤
python3 scripts/quality_eval.py score "$R/tools-sel" "$R/paid" \
  --judgments J1.json [--judgments J2.json] --output "$R/score.json"
python3 scripts/quality_eval.py reader "$R/paid" --sample 6 --seed S --output "$R/reading"
```

- 2단계 capture는 유료 batch와 같은 `SEL`로 돌린다. 시험 장부의 요청 상한(기본 200건) 안에 들어가야 한다. 넘으면 언어마다 새 시험 relay를 쓴다.
- 지침 개정 전후를 비교할 때: 개정 전 checkout(baseline)과 개정 뒤 checkout(candidate)에서 같은 표본으로 2단계 capture와 5~6단계를 각각 하고(`--batch capture-baseline`, `--batch paid-baseline`처럼 이름으로 구분한다), 7단계 `score`에 두 batch를 나란히 준다. 상한 호출이 큰 쪽을 먼저 실행하고, 끝난 뒤의 장부로 다른 쪽을 다시 계획한다. 거절이면 다른 쪽을 실행하지 않는다. 시작 전 남은 요청이 적으면 `남은 요청 − 먼저 실행한 쪽 기대 호출 − 5 ≥ 다른 쪽 상한 호출`이 되는 표본을 고른다.
- 유료 batch 전에 가짜 upstream 확인을 두 번 한다. 일반 capture와, `--shape-errors`(예: 0.5)로 형식 오류를 섞은 capture다. 둘 다 모든 사례의 AI 검토가 `performed`이고 필수 검토 task를 잃지 않아야 한다. 일반 가짜 응답은 형식이 항상 맞아 형식 오류 경로를 지나지 않는다.
- 독립 코드 검토가 후보 커밋에 대해 진행 중이면 유료 실행은 그 수정을 기다린다. 검토가 워커를 바꾸면 앞서 받은 가짜 upstream 실행은 쓰지 않고, 수정이 끝난 최종 커밋에서 두 가지 확인을 다시 한다. 최종 커밋이 사전 실행을 한 커밋과 같은 요청을 보낸다는 것도 `regression_pilot.py --compare-dumps`의 요청 다중집합 비교로 확인한다([회귀 집합 실행](#회귀-집합-실행)). 이전 커밋의 사전 실행으로 유료 실행의 요청을 보증하지 않는다.
- `plan`의 호출 수는 가짜 upstream capture(이력)에서 온다. 가짜 응답은 실모델과 달라 slice 조사·검증 호출 수가 유료 실행과 다를 수 있다. 진단 분류처럼 응답에 따라 갈리는 호출도 그렇다. 기대 차감은 그 이력에서 나온 추정이므로 실제 호출 수와 차감은 실행 뒤 장부로 기록한다.
- 이어서 유료 canary를 한두 사례로 먼저 돌려 통과 조건을 확인한 뒤 batch를 시작한다. 통과 조건은 실행 전에 적는다(예: AI 검토 `performed`, 필수 검토 task 실패 0, 장부의 거절 0, 사례당 비용이 계획 안). 실모델 출력에서만 나타나는 거부는 가짜로 만들 수 없고, 이를 batch 중간에서 발견하면 앞 사례의 비용이 무효가 된다. canary에서 결함이 나와 제품을 고쳤으면 고치기 전 제품의 결과는 비교에서 빼고 무효 실행으로 기록한다.
- 예산이 중간에 끊길 수 있으면 가장 큰 사례를 마지막에 둔다. 정지로 잃는 쪽이 가장 큰 사례가 되어 비교할 사례 수를 가장 많이 남긴다. 기준과 후보는 둘 다 실행한 같은 사례로만 비교한다.
- 기준 빌드와 후보 빌드 사이에 분석기가 바뀌었으면(버전, 새 분석 작업, 입력 해석) 블라인드 묶음에서 도구 진단을 뺀다. 도구 진단의 차이를 모델 입력 변경의 효과로 읽지 않는다. 위치 겹침 같은 구조 지표는 따로 보고하되 그 한계를 적는다.
- 결과 수치와 판단은 [평가 결과](EVALUATION-RESULTS.md)에 쓰고 batch 디렉터리는 저장소 밖에 둔다.
- 프로젝트 규모 집합: 모든 `plan`·`run`·`score`·`capture-list`·`blind`에 `--corpus project-defects --split dev --cache "$CACHE"`(holdout이면 `--split holdout`)를 더하고, 3단계 정답 목록은 `python3 -I fixtures/project-defects/corpus.py forbidden --split dev --cache "$CACHE" --allow-text prompts/review --allow-text "$R/allow.txt" --output "$R/forbidden.json"`로 만든다. `$CACHE`는 영구 비공개 경로에 둔다. 첫 `verify --cache "$CACHE"`가 캐시를 채운다. 기준 커밋의 제품을 잴 때는 그 커밋의 checkout을 따로 만들어 2단계 capture와 6단계 `run`에 `--build-root BASE`를 주고, runner·corpus는 지금 checkout을 쓴다. 7단계 판정은 `blind`로 만든 묶음을 판정한 뒤 `blind-merge`로 판정 파일을 만든다.

검토 모델을 비교할 때는 같은 고정 입력을 `gpt-6.1-terra`와 `gpt-6-luna`로 번갈아 실행하고 두 relay가 장부 하나를 공유한다. 파일럿의 모델 ID는 ready 파일에서 읽는다.

## 예산 relay와 장부

[`scripts/model_budget_relay.py`](../scripts/model_budget_relay.py)는 사람이 승인한 실모델 평가에서 유료 모델 호출 상한을 강제하는 QA 전용 보조 도구다. 제품 런타임·CI 의존성이 아니다. 모델 연결 방식(허용된 OpenCode 우선, 자동 fallback 금지)을 바꾸지 않는다. 제품 쪽 relay 파일 사용은 [모델 검토](REVIEW.md)에 있다. 이 저장소의 시험은 과금 호출을 하지 않는다.

### 키 취급

- 상위 API 키는 운영자 셸의 `OPENAI_API_KEY`에만 둔다. relay만 읽고, 시작 시 환경에서 제거한다. 파일·로그·응답·장부·ready file에 쓰지 않는다. 키가 없거나 형식이 틀리면 값을 보이지 않고 종료한다.
- 키를 출력하지 않는다. `echo`, `env`, `set -x`, 명령줄 인자, 결과 디렉터리, 프롬프트, 커밋 어디에도 키 값을 두지 않는다. 키가 있는지만 확인할 때도 값을 보이지 않는 방법을 쓴다.
- runner, 가짜 upstream, `--test-upstream` relay는 키를 읽지 않는다. dry run 프로세스는 `env -u OPENAI_API_KEY`로 띄운다. 시험은 실제 키와 프록시 변수를 자식 환경에서 제거한다.
- OpenCode에는 ready file의 임시 로컬 token만 간다. 상위 오류 메시지(키 일부를 포함할 수 있음)는 버린다.

### 시작

```bash
python3 scripts/model_budget_relay.py \
  --ready-file "$DIR/ready-$(date +%s).json" \
  --ledger "$LEDGER" \
  --model gpt-6.2-sol            # --budget-usd 기본 9.99, 소유자 승인 범위에서 최대 25
```

| 옵션 | 기본 | 제한 |
|---|---|---|
| `--model` | — | `gpt-6.2-sol`, `gpt-6.1-sol`, `gpt-6.1-terra`, `gpt-6-luna` |
| `--budget-usd` | 9.99 | 0 초과 25 이하, 소수 6자리까지. 기존 장부의 상한을 낮출 수만 있다 |
| `--max-requests` | 200 | 1~1000. 장부 전체 요청 수 상한이며 낮출 수만 있다 |
| `--max-input-bytes` | 278528 | 1~1,064,960. 요청당 메시지 텍스트 UTF-8 바이트 합. 기본은 제품의 기본 요청 예산(256 KiB)에 OpenCode가 덧붙이는 메시지 몫 16 KiB를 더한 값이고, 상한은 제품 요청 예산의 최대(1 MiB)에 같은 몫을 더한 값이다 |
| `--max-output-tokens` | 16384 | 1~65536. 요청당 출력 토큰 상한이며 예약에 전부 들어간다. 클라이언트가 더 작은 값을 보내면 그 값을 쓴다 |
| `--port` | 0(임시) | 항상 127.0.0.1에만 bind |
| `--forbidden-phrases` | 없음 | 정답 목록. 주면 일치 요청을 막는다. 파일이 없거나 형식이 틀리면 종료 코드 2. 감시용 목록은 `--test-upstream`에서만 받는다 |
| `--allow-model-echo` | 꺼짐 | 위 정답 차단의 예외 방식. 목록 없이 주면 종료 코드 2 |
| `--test-upstream` | 없음 | dry run 전용. `http://127.0.0.1:PORT`만 받는다. 키를 읽지 않고 환경에서 지운다. 장부 header에 `upstream: test-loopback`을 남기고 실제 장부와 섞지 않는다 |

- 상한은 장부 하나에 걸린다(`HARD_CAP_MICROUSD = 25_000_000`, 모든 relay 프로세스 합계 최대 USD 25). 더 큰 금액을 지정해 재시작해도 기존 장부의 상한과 잔액은 그대로 적용한다. 장부를 잃거나 새 경로를 쓰면 별도 예산이 생기므로, 예산 보장은 같은 장부 파일을 유지하는 운영 절차에 의존한다. 예산 기간마다 영구 경로의 장부 하나를 쓰고(`/tmp` 같은 휘발 경로 금지), 소유자 승인 없이 새 장부를 만들지 않는다.
- ready file은 반드시 새 경로여야 한다(이미 있으면 종료 코드 2). 0600으로 원자적으로 만들고 내용은 `{"base_url":"http://127.0.0.1:PORT/v1","token":"...","model":"gpt-6.2-sol","pid":N}`이다. SIGTERM/SIGINT 정상 종료 시 삭제한다. SIGKILL 뒤 남은 파일은 무효 token을 담으므로 지운 뒤 새 경로로 다시 시작한다.
- OpenCode는 OpenAI 호환 provider의 `baseURL`·`apiKey`에 ready file의 `base_url`·`token`을, 모델 ID에 `model`을 쓴다. CODEYE에서는 `--model codeye-gateway/<model> --model-relay-file READY`로 고른다.
- relay는 상위 요청을 하나씩 보낸다. relay를 쓰는 노드는 `--review-parallel`과 관계없이 검토 쌍을 하나씩 실행하므로, 유료 평가 실행은 직렬이다. 대기열에서 기다린 시간이 모델 제한시간을 쓰지 않게 하려는 것이다([호출 상한과 승인](REVIEW.md#호출-상한과-승인)). 같은 응답에서는 병렬도가 요청·호출 수·보고서를 바꾸지 않으므로, 직렬로 잰 평가 결과가 병렬 실행에도 적용된다(사용량 제한으로 실패한 호출은 예외다).
- 보고: `python3 scripts/model_budget_relay.py --ledger PATH --report`(키 불필요).
- 정지 해제: `python3 scripts/model_budget_relay.py --ledger PATH --clear-halt "검토 내용"`. 장부에 감사 기록을 더할 뿐 상한·청구액은 바꾸지 않는다. 원인을 확인한 사람만 실행한다. 원인을 고치지 않고 해제하면 실패 차감이 계속 쌓인다.
- 실행이 끝나면 정산 대기 0건을 확인한 뒤 relay를 종료한다. 공급자 청구서는 장부와 별개다.

### 로컬 경계

- `POST /v1/chat/completions`, `GET /v1/models`(로컬 응답, 상위 호출 없음)만 제공한다. 나머지는 404.
- 매 실행 임의 bearer token, peer 127.0.0.1, `Host`가 `127.0.0.1:PORT` 또는 `localhost:PORT`여야 한다. `Origin`·CORS preflight 헤더가 있으면 403이고 CORS 헤더를 보내지 않는다.
- 본문은 `Content-Length` 필수(chunked 거부), 4 MiB 이하, 헤더 합 16 KiB 이하, 읽기 30초. 한 프로세스에서 상위 호출은 동시에 1개이고 나머지는 최대 660초 대기 후 거부한다(`relay_busy`).
- 클라이언트 오류는 재시도되지 않는 상태 코드(400/401/402/403/404/411/413/415/424)만 쓴다. 408/409/429/5xx를 내지 않아 SDK 자동 재시도가 유료 호출을 늘리지 못하게 한다. 예산·요청 수·정지는 402, 상위 실패는 424.
- 오류 본문에는 relay 코드, 상위 HTTP 상태, 형식이 안전한 상위 `error.code`/`error.type`만 담는다.

### 상위 요청

- OpenCode 쪽은 `/v1/chat/completions`이고, relay→OpenAI 구간만 Responses API를 쓴다.
- 고정 대상은 `https://api.openai.com/v1/responses`이고 `http.client`로 직접 연결한다. 프록시 환경변수·redirect·재시도를 쓰지 않는다. 대상을 바꾸는 환경변수는 없다. 유일한 예외는 dry run용 `--test-upstream`이고 loopback 주소만 받으며 실제 대상으로 돌아가는 fallback이 없다.
- payload는 매번 새로 만든다: `model`, `input`(메시지를 순서대로 `{role, content: 문자열}`로 재구성, role `system|developer|user|assistant`), `max_output_tokens`(`--max-output-tokens` 이하, 추론 토큰 포함, 클라이언트 `max_tokens`/`max_completion_tokens`와 최소값), `reasoning: {effort: "low"}`, `service_tier: "default"`, `store: false`. 응답은 하나이며 스트리밍하지 않는다.
- 응답에서는 assistant `message` 항목의 `output_text`(없으면 `refusal`)만 읽는다. reasoning 등 다른 출력 항목은 클라이언트에 보내지 않는다. `completed`는 `finish_reason: stop`, `incomplete`는 `max_output_tokens`→`length`, `content_filter`→`content_filter`다. 추론이 상한을 모두 쓰면 빈 content와 `length`로 200을 돌려준다. `failed`·`cancelled` 등은 424이고 예약 전액을 차감한다.
- content가 text part 배열이면 줄바꿈으로 합친다. 이미지·오디오·파일 part, `tool` role, `tool_calls`/`function_call`/`tool_call_id`가 있는 메시지는 400으로 거부한다. 메시지는 64개 이하.
- `tools`, `tool_choice`, `temperature`, cache·retention·tier 지정 등 그 밖의 최상위 키는 버리고 이름만 장부 `dropped_keys`에 남긴다. OpenCode가 도구 정의를 보내도 모델은 도구 없이 텍스트로만 답한다.
- `stream: true` 요청은 한 번의 비스트리밍 결과로 `chat.completion.chunk` SSE(role → content → finish → `stream_options.include_usage`일 때 usage → `[DONE]`)를 만든다.
- 상위 전체 제한시간 600초(연결·전송·헤더 대기·본문 읽기 합계, 제품의 기본 호출 제한시간과 같다), 응답 4 MiB.
- 클라이언트가 요청을 취소하거나 연결을 끊어도 이미 보낸 상위 호출과 그 차감은 멈추지 않는다. 대기열에서 기다리는 동안 연결을 끊은 요청은 예약도 전달도 하지 않는다. SIGTERM 시 진행 중 호출의 정산을 최대 610초 기다린 뒤 종료한다.

### 장부와 차감

- JSONL, 0600, 소유자 본인, symlink 거부. 모든 갱신은 `flock` 배타 잠금(프로세스 간)과 스레드 잠금 아래에서 전체를 다시 읽고 한 줄을 추가한 뒤 `fsync`한다.
- 기록 종류: `header`(상한·가격 상수), `cap`(상한 하향), `reserve`, `settle`, `halt`, `clear_halt`, `repair`, `forbidden`(정답 목록 SHA-256·항목 수·corpus digest), `blocked`(정답 차단, 차감 0), `refused`(예산·요청 수·정지로 거절, 차감 0). 상태 계산은 `forbidden`·`blocked`·`refused`를 무시한다. 가격 상수가 다른 장부는 거부한다. `reserve`에는 프롬프트의 `Role:` 줄에서 읽은 역할 표지가 붙는다.
- `reserve`는 상위 요청 전에 durable하게 기록한다. 마지막 줄이 줄바꿈 없이 끊겨 있으면 쓰기가 끝나지 않은 것이므로(그 요청은 전송되지 않았다) 잘라내고 `repair`를 남긴다. 중간 줄이 깨졌으면 동작을 거부한다.
- 금액은 정수 microUSD다. 단가는 공식 standard 단기 컨텍스트 단가 입력 $2, 출력 $10 /1M과 캐시 쓰기 30분 $2.50, 12시간 $5 /1M 중 최악값을 쓴다.
  - 입력 토큰 상한 = 텍스트 UTF-8 바이트 + 메시지당 16 + 고정 256(BPE 토큰은 최소 1바이트를 덮는다).
  - 예약 = 2 × (입력 상한 × 5 + 출력 상한 × 10) + 1000.
  - 정산: 응답의 `usage.input_tokens`·`output_tokens`(추론 포함)가 정수이고 상한 안이며 `service_tier`가 `default`로 확인되면 `input × 5 + output × 10`. 장부에는 `prompt_tokens`=input, `completion_tokens`=output, `cached_tokens`, `reasoning_tokens`로 남긴다.
  - 그 밖의 모든 경우(usage 없음, tier 미표시, HTTP 오류, redirect, timeout, 전송 오류, 응답 이상, relay 오류)는 예약 전액을 차감한다. 미정산(충돌·SIGKILL·동시 실행 중) 예약도 전액으로 센다.
- 승인: `정산액 합 + 미정산 예약 합 + 새 예약 > 상한`이면 상위 요청 없이 402.
- 정지(halt): 연속 실패 3회, usage 상한 초과, 응답 모델이 요청 모델(또는 그 날짜 snapshot)과 다름, `service_tier`가 `default`가 아님. 장부에 기록되어 재시작 뒤에도 유지된다.

| 보고 필드 | 의미 |
|---|---|
| `reserved_microusd_total` | 지금까지 예약한 총액(최대 노출) |
| `settled_charged_microusd` / `pending_reserved_microusd` | 정산된 보수적 차감액 / 미정산 예약 |
| `charged_microusd_total` | 예산 차감 기준(위 둘의 합). `remaining_microusd`는 상한과의 차 |
| `usage` | 응답이 보고한 prompt/completion/cached/reasoning 토큰 합 |
| `standard_rate_estimate_microusd` | 보고된 입력·출력 토큰에 요청 모델의 Standard 요율을 적용한 정보용 추정(캐시 할인 미적용). 청구서가 아니며 승인에 쓰지 않는다 |
| `settled_by_status`, `halted`, `halt_reasons` | 결과 분포와 정지 상태 |
| `upstream`, `forbidden_lists`, `blocked_answer_leaks`, `blocked_by_role`, `echo_exempted_requests`, `echo_exempted_by_role`, `refused_by_code` | 장부 종류(시험용이면 `test-loopback`), 읽은 정답 목록과 `allow_model_echo`, 차단·예외·거절 수 |

### relay 한계

- 바이트 기반 토큰 상한은 BPE 토크나이저 가정에 기댄다. 상한을 넘는 usage가 보이면 즉시 정지한다.
- 정산은 응답의 usage를 신뢰한다. usage가 없거나 형식이 다르면 예약 전액으로 남겨 실제보다 크게 차감한다.
- `fcntl`을 쓰므로 Linux/macOS 전용이다. 같은 장부를 공유하는 프로세스는 같은 파일시스템의 로컬 잠금을 써야 한다(NFS 잠금은 보장하지 않는다).

## 평가 한계

- 호출자 확장은 Go만 한다. Go가 아닌 사례의 파일 간 결함은 발췌에 들어간 범위에서만 보이고, corpus에서 라벨 결함 파일이 입력에 들어갔다는 사실이 큰 저장소에서도 그렇다는 근거는 아니다.
- 분석기는 corpus의 구성에 따라 돌지 않을 수 있다(예: lockfile이 필요한 Rust 분석, compile DB가 필요한 clang-tidy). 그 분석기의 recall은 측정되지 않은 것으로 읽고 보고서의 공백 task를 함께 본다. 비밀값·중복·복잡도 라벨이 없는 corpus에서는 구조·비밀값 분석의 recall을 잴 수 없다.
- 정답 목록은 표현 일치만 본다. 다른 말로 바꾼 정답, 다른 언어로 쓴 설명, 코드 모양의 힌트는 막지 않는다.
- 엄격 차단이 맞힌 사례를 얼마나 무효로 만드는지는 실모델 batch에서만 보인다. 예외 방식의 범위는 relay 프로세스 전체이고 사례 단위 확인은 runner가 한다. runner를 거치지 않고 relay를 쓰면 이 확인이 없다.
- 예외 ID 확인은 순서를 보지 않는다. runner는 예외 ID가 같은 사례의 모델 출력에 있는지만 확인하고, 그 출력이 요청보다 먼저였는지는 보지 않는다.
- 평가 실행 환경이 한 종류의 호스트이면 다른 OS·다중 노드에서의 결과로 일반화하지 않는다.
- 프로젝트 규모 집합은 분할마다 사례가 10개 안팎이다. 언어·범주별 수는 한두 개이므로 언어·범주별 차이를 결론으로 쓰지 않는다. 비율에는 Wilson 구간을 붙이고 구간이 겹치는 차이를 개선이라고 하지 않는다.
- 실제 수정 사례는 공개된 수정이라 모델이 학습했을 수 있다. 실제 수정과 심은 사례의 결과를 나누어 보고, 심은 사례에서 크게 떨어지면 기억의 효과를 의심한다. 심은 결함은 한 사람이 만든 것이라 실제 결함의 분포를 대표하지 않는다.
- 스냅샷은 저장소 트리 전체다. 범위 밖 파일과 시험 파일도 문맥이 되고, 도구가 범위 밖을 분석하는지는 분석기마다 다르다. 위치 지표는 라벨 파일과 줄로만 세고 범위 밖 지적은 따로 판정한다.
- 라벨은 한 결함만 담는다. 같은 범위의 다른 실제 결함을 지적한 finding은 라벨과 맞지 않아도 오탐이 아닐 수 있으므로 사람이 판정한다(`valid_*`이고 `matches_label=false`).
- 사례의 기존 시험이나 독립 검사는 결함판이 실패하고 수정판이 통과한다는 것만 보인다. 수정판 전체에 결함이 없다는 뜻이 아니다.
- 큰 사례는 한 시도의 모델 호출 상한(`--review-max-calls`)에 닿아 slice 일부만 검토될 수 있다. 행의 `review_calls` 수가 상한과 같으면 그 변형은 범위 전체를 검토했다고 보지 않는다.

# 46. W24 R1 품질 평가 runner

W24 2단계는 [R1 보류 corpus](../fixtures/r1-quality/README.md)로 분석기와 검토 지침을 측정하는 평가 도구다. [평가 기준](16-evaluation.md)의 지표·분모·실험 구성을 실행 가능한 절차로 만들었다. 구성은 다음과 같다.

- 정답 차단: 모델 요청에 정답 문구가 들어가면 relay가 보내지 않고 장부에 남긴다.
- runner `scripts/r1_eval.py`: 실제 controller·agent·분석기·OpenCode·relay 경로로 사례를 실행하고 결과를 보관한다.
- 채점 `scripts/r1_score.py`: 판정 양식, 지표와 Wilson 구간, 반복 실행 일치도, 보고서 읽기 과제를 만든다.

이 문서의 실행 결과는 모두 **시험용 가짜 upstream**(dry run)에서 얻었다. 가짜 upstream은 모델이 아니고 정답도 모른다. 따라서 아래 수치는 runner·relay·채점 경로가 동작한다는 근거일 뿐 분석 품질의 근거가 아니다. 유료 실모델 batch는 coordinator가 직접 실행한다(§11).

## 1. 구성

| 부분 | 위치 | 역할 |
|---|---|---|
| 정답 목록 | `fixtures/r1-quality/corpus.py forbidden` | 라벨 문구 n-gram과 고유 토큰 중 어떤 입력에도 나오지 않는 것을 JSON으로 내보낸다 |
| relay 차단 | `scripts/r0_openai_gateway.py --forbidden-phrases FILE [--allow-model-echo]` | 전달할 메시지에 목록 항목이 있으면 예약·전달 없이 `blocked` 기록, HTTP 403. 선택하면 relay가 돌려준 모델 답의 표현은 예외로 기록한다(§3.4) |
| 시험용 upstream | `--test-upstream http://127.0.0.1:PORT`, `scripts/r1_fake_upstream.py` | dry run 전용 loopback 가짜. 키를 읽지 않는다. `--echo-text`는 운영자가 정한 문장을 investigate 후보 제목에 붙인다 |
| 공용 실행 틀 | `scripts/eval_harness.py`, `scripts/test_postgres.py`의 `temporary_postgres` | 임시 PostgreSQL, 제품 빌드, controller·agent, 보고서 조회. `r0_pilot.py`도 같은 코드를 쓴다 |
| runner | `scripts/r1_eval.py` | `plan`·`run`·`score`·`allow-text`·`capture-list`·`reader`·`reader-score` |
| 채점 | `scripts/r1_score.py` | 위치 대조 pre-fill, 판정 양식, 지표, 분산, 읽기 과제 |
| 시험 | `scripts/test_r0_openai_gateway.py`(relay), `scripts/test_r1_eval.py`(runner·채점) | CI에서 실행(§10) |

## 2. 조건

| 조건 | 실행 | 모델 | 대상 |
|---|---|---|---|
| `tools` | 분석기만. agent에 모델 설정을 주지 않는다 | 없음. 검토 task는 `skipped_policy`(`model_source_not_approved`) | 55개 사례 전체(변형 100개) |
| `review` | 제품 전체 경로. `--pack review-v1` 또는 `review-r1`을 agent의 `--review-pack`으로 준다 | coordinator가 시작한 relay | 표본 |

- 사례 변형마다 `corpus.py materialize`로 무작위 이름의 디렉터리에 입력만 만든다. 프로젝트 이름도 무작위(`eval-<hex>`)다. 사례 ID·변형 이름·라벨은 스냅샷에 들어가지 않는다. README 포함 여부(`--documented`)는 corpus 순서로 고정했다(짝수 번째 사례). 모든 batch가 같은 선택을 쓴다.
- 분석기 플래그는 `--tool NAME=/abs/path`로 받아 agent에 그대로 넘긴다. 이 빌드가 모르는 이름이면 agent가 사용 오류로 끝나고 runner가 멈춘다. 새 팩이 병합되면 runner를 고치지 않고 플래그만 더한다. 공백은 보고서의 task(`pack-gap.<생태계>`, `tool_not_configured` 등)에서 읽는다.
- corpus source root에는 기본으로 저장소 코드 실행 허가(`--repo-code-source-root`)를 준다. Rust 기준 분석기가 build script를 실행하기 때문이다. `--no-repo-code-grant`로 끌 수 있고 manifest에 기록한다.
- 실패한 run도 자료로 기록한다. 보고서를 조회하지 못하면 `report_error`에 HTTP 상태와 오류 코드를 남긴다.
- `review` 조건은 사례를 **하나씩** 실행한다. 사례 전후의 relay 장부를 비교해 호출·차감·차단·거절을 그 사례에 귀속하기 위해서다. `tools` 조건은 `--agents × --max-workers`개까지 동시에 실행한다.

### 2.1 run manifest

batch 디렉터리의 `manifest.json`에 다음을 남긴다. 비밀값과 relay token은 넣지 않는다. 경로는 `~` 기준 상대 경로로 적는다.

- 팩 id·version·status, `prompts/<팩>` 트리 digest, 보고서 `review_calls`에서 관측한 역할별 `pack_sha256`
- corpus digest, 모델 id와 OpenCode 버전, checkout commit과 수정 여부, OS·Python
- 분석기 실행 파일의 SHA-256, 보고서에서 관측한 분석기별 도구 버전, 저장소 코드 허가, toolchain 경로
- 장부의 시작·끝 상태와 batch 차감, 예약·여유, 계획 추정, 정답 목록 SHA-256·항목 수·허용 텍스트
- holdout 표지, 표본 선택(seed·층별 수), 반복 수, 시작·끝 시각, 중단 사유

## 3. 정답 차단

### 3.1 목록 만들기

`corpus.py forbidden [--allow-text PATH ...] --output FILE`는 corpus의 누설 검사 규칙을 그대로 쓴다.

- 후보: 사례마다 라벨 문장(trigger, expected_impact, reviewer_notes, 반례 note, 의심 위치 note, 검사 요약)의 5-gram과 `distinctive_tokens`.
- 제외 1: 모든 사례의 모든 변형(README 포함·미포함) 입력 어디에든 나오는 후보. 다른 사례의 입력에 정상적으로 나오는 표현을 막지 않기 위해서다.
- 제외 2: `--allow-text`로 준 텍스트에 나오는 후보. 모델에 정당하게 가는 텍스트(지침 팩, 도구 진단, 역할 계약 envelope)를 막지 않기 위해서다. 허용 텍스트의 이름(팩 기준 상대 경로)·SHA-256은 목록과 manifest에 남는다. 자동으로 추가하지 않는다.
- 제외된 후보는 수가 아니라 ID로 남는다. `dropped.in_inputs`는 ID 목록, `dropped.in_allowed_text`는 ID·문구·제외한 허용 텍스트 이름이다. 지침 팩이 라벨 표현을 담고 있으면 여기서 보인다(§10.2).
- 일반 금지어(`BANNED_WORDS`·`BANNED_STEMS`, 예: before·fix)는 내보내지 않는다. 지침과 모델 출력에 흔한 단어라 정상 요청을 막기 때문이다.
- 대조 방식(`codeye-words-v1`): 한 단어 토큰은 단어 전체와 camelCase·snake_case 조각, 여러 단어는 `[a-z0-9]+` 단어열, 그 밖은 소문자 부분 문자열. relay와 corpus.py가 같은 규칙을 쓴다.
- dry run 최종 목록은 7,723개 항목이다. 허용 텍스트는 두 팩, tools batch의 도구 메시지 12줄, 역할 envelope 84개다. 입력에 있어 12개, 허용 텍스트에 있어 12개를 뺐다.

### 3.1.1 허용 텍스트 만들기

모델에 가는 텍스트 중 입력이 아닌 것은 세 가지다. 지침 팩(`prompts/`), 도구 진단, OpenCode와 CODEYE가 붙이는 envelope(OpenCode 시스템 프롬프트, Go 코드에 들어 있는 역할 계약)다. envelope는 저장소 파일이 아니므로 dry run 요청에서 모은다.

1. `run --condition tools`로 도구 진단을 모은다.
2. `r1_eval.py capture-list --output FILE`로 아무것도 막지 않는 감시용 목록을 만든다. `purpose: envelope_capture`가 붙은 목록이다. relay는 `--test-upstream`일 때만 이 목록으로 시작한다. runner도 dry run이 아니면 거부한다.
3. 가짜 upstream(`--dump DIR`)과 감시용 목록으로 두 팩의 review dry run을 돌린다. 모든 역할의 요청이 막히지 않고 dump에 남는다.
4. `r1_eval.py allow-text --tools-batch B --request-dump DIR --output FILE`가 도구 메시지·규칙 ID와 envelope를 모은다. envelope는 system 메시지와 `<review-input>`을 뺀 user 텍스트다. review input은 가져오지 않는다.
5. `corpus.py forbidden --allow-text prompts/review-v1 --allow-text prompts/review-r1 --allow-text FILE`로 목록을 만든다.

제품 코드·지침·분석기 구성이 바뀌면 envelope와 도구 진단도 바뀐다. 유료 batch 직전에 같은 checkout으로 다시 만든다(§11).

### 3.2 relay

- `--forbidden-phrases FILE`이 없거나 비었거나 형식이 틀리면 시작하지 않는다(종료 코드 2).
- 시작할 때 목록 SHA-256·항목 수·corpus digest를 장부에 `forbidden` 기록으로 남긴다. `--report`의 `forbidden_lists`에 보인다.
- JSON 요청을 해석한 뒤 **전달할 메시지 텍스트**를 하나씩 대조한다. JSON 문자열 안의 줄바꿈은 해석된 뒤 단어 경계가 된다. 전달하지 않는 키(`tools` 등)는 대조하지 않는다.
- 일치하면 예약하지 않고 상위로 보내지 않는다. 장부에 `blocked` 기록(일치 항목 ID 최대 16개, 수, 바이트, 메시지 수, 역할 표지)을 남기고 HTTP 403 `blocked_answer_leak`를 돌려준다. 차감은 0이다. 라벨 문구 자체는 장부에 쓰지 않는다.
- 역할 표지는 프롬프트의 `Role: <역할>` 줄이다. OpenCode가 붙이는 첨부 뒤에 있어도 찾는다. 예약 기록에도 같은 표지를 남겨 역할별 요청 크기를 볼 수 있다.
- `--allow-model-echo`(선택, 기본 꺼짐): 이 relay 프로세스가 상위에서 받아 **돌려준 답**에 나온 항목 ID는 이후 요청에서 막지 않는다. 클라이언트가 보낸 assistant 메시지는 근거로 쓰지 않는다. ID는 메모리에만 두므로 재시작하면 다시 엄격해진다. 예외가 아닌 항목이 하나라도 있으면 요청 전체를 막는다. 예외로 보낸 요청의 `reserve` 기록에 `echo_matches`(ID 최대 16개)·`echo_match_count`가 남는다. 시작 기록 `forbidden`과 `--report`에 `allow_model_echo`, `echo_exempted_requests`·`echo_exempted_by_role`이 보인다. 목록 없이 이 옵션만 주면 시작하지 않는다.
- 예산·요청 수·정지 때문에 거절하면 `refused` 기록(사유 코드, 차감 0)을 남긴다. runner가 batch 중간의 거절을 알아채는 신호다.
- 새 기록 종류는 기존 상태 계산이 무시한다. 가격 상수·장부 버전·기존 header는 바꾸지 않았다.

### 3.3 runner의 처리

- review batch를 시작하기 전에 relay 프로세스(ready file의 pid)가 **이 목록**을 현재 corpus digest로 읽었는지 장부에서 확인한다. 아니면 시작하지 않는다. 차단 없이 유료 batch가 도는 일을 막는다.
- 사례 실행 중 장부에 `blocked`가 생기면 그 사례를 `invalid: blocked_answer_leak`으로 표시하고 지표에서 뺀다. 일치 항목·역할과 원인 분류(`model_echo`: 모든 항목이 그 사례의 모델 출력에도 있음, `input_or_instruction`: 그 밖)가 행에 남는다.
- `--allow-model-echo` relay에서는 예외로 보낸 ID가 **같은 사례**의 모델 출력(수락된 단계 기록)에 있는지 다시 확인한다. relay의 예외 범위는 프로세스 전체이고, runner는 review 사례를 하나씩 실행하므로 사례 단위로 좁힐 수 있다. 다른 사례에서 나온 표현이거나 ID 목록이 잘렸으면 `invalid: answer_echo_unverified`다.
- manifest의 `forbidden.allow_model_echo`와 `score`의 `answer_blocking.mode`(`strict`·`model_echo_allowed`)가 방식을 남긴다. 방식이 다른 batch를 비교하면 표 아래에 표시한다. 두 방식은 빼는 변형이 다르기 때문이다.

### 3.4 잘못 막을 수 있는 경로

차단은 보수적이다. 정답이 아닌데 막히면 그 사례는 무효가 되고 표본이 준다. 앞 역할의 호출은 이미 차감된 뒤다. dry run에서 확인한 경로는 다음과 같다(§10.2).

- **역할 계약.** Go 코드에 들어 있는 verify 역할 계약에 `ownership transfer`가 있다. 세 사례(R1-JS-11, R1-PY-10, R1-RS-10)의 고유 토큰이다. 지침 팩만 허용했을 때 Go 사례의 verify 요청이 모두 막혔다. envelope를 허용 텍스트에 넣어 풀었다.
- **도구 진단.** R1-GO-06의 go vet 메시지(`copylocks`, `passes lock by value`)가 고유 토큰과 같다. review-r1의 interpret 입력에 이 관찰이 들어가면 막힌다. tools batch의 도구 메시지를 허용 텍스트에 넣어 풀었다. R1-GO-09(`unexported`, `structtag`)와 R1-JS-02(`unsafe-finally`)도 같은 이유로 빠졌다.
- **지침 팩.** 팩 문장이 라벨 표현을 담아 목록에서 빠진 항목이 있다. `stale`(review-v1 js-ts.md), `reference cycle`·`cycle`(review-v1 rust.md·cpp.md), `partial write`(두 팩의 domains.md)다. 정답 누설은 아니지만 지침이 이 결함 종류를 직접 언급한다는 뜻이다. 팩을 바꾸면 `dropped.in_allowed_text`를 다시 확인한다.
- **모델 자신의 문장.** investigate 후보의 제목·설명이 verify 입력에 그대로 들어간다. 가짜 upstream에 `--echo-text "goroutine leak"`(R1-GO-01의 고유 토큰)를 주어 확인했다. 엄격 방식에서는 두 사례의 verify가 모두 막혔다(원인 `model_echo`). `--allow-model-echo`에서는 둘 다 끝났고, 예외 ID가 같은 사례의 후보 문장에 있음을 runner가 확인했다.

마지막 경로가 유료 batch에서 가장 크다. Go 사례의 고유 토큰은 `goroutine leak`, `shadowed`, `unsigned subtraction`, `index out of range`처럼 결함을 맞힌 모델이 쓸 법한 말이다. 엄격 방식에서는 모델이 결함을 맞힌 사례일수록 verify가 막혀 무효가 되므로, 남은 사례로 계산한 recall이 낮은 쪽으로 치우친다. 그래서 예외 방식을 선택 옵션으로 두었다. 이 방식에서도 relay가 직접 돌려준 답에 없던 표현은 그대로 막고, runner가 사례 단위로 다시 확인한다. 기본값은 모든 일치를 막는 엄격 방식이다. 어느 방식을 쓸지는 유료 batch를 실행하는 사람이 정하고, 결과에 방식을 함께 적는다.

## 4. holdout 등록부

- `--results-root`(모든 batch 위의 고정 디렉터리)의 `holdout-registry.jsonl`에 (팩 id, 팩 트리 digest, corpus digest, 조건, 처음 실행 시각, batch, dry run 여부)를 한 번씩 덧붙인다. 지우거나 고치지 않는다.
- 같은 팩 id·같은 corpus에 **다른 digest의 실제(dry run이 아닌) 실행**이 먼저 있으면 이번 batch에 `holdout_seen_by_prior_revision`과 이전 digest를 표시한다. 실행은 막지 않는다.
- dry run 기록은 "본 것"으로 세지 않는다. 가짜 upstream의 출력에는 정답 정보가 없기 때문이다.
- `score`는 batch 중 하나라도 표시되었거나 dry run이면 비교를 `independent: false`로 낸다. 결과 문서는 두 batch 모두 표시가 없을 때만 독립 비교라고 쓸 수 있다.
- `tools` 조건은 팩 id `analyzers`, digest는 go와 분석기 실행 파일 SHA-256의 정규 JSON digest다.

## 5. 표본과 예산

### 5.1 층별 표본

`--sample N --seed S`는 언어 × kind × `detection`(`static_likely`/`semantic_only`) 층으로 나눈다. 층마다 N × (층 크기/전체)의 내림을 배정하고, N이 층 수 이상이면 층마다 최소 1개를 둔다. 나머지는 나머지가 큰 층부터 채운다. 층 안에서는 `random.Random(seed)`로 섞어 뽑는다. 선택 기록(seed, 층별 가능·선택 수)은 `plan.json`과 manifest에 남는다. `--cases`, `--languages`, `--kinds`, `--variants`로 범위를 좁힐 수 있다. 정상 사례에는 before 변형만 있다.

### 5.2 계획과 거절

`plan`과 `run`은 batch의 호출 수와 장부 차감을 추정한다.

- **기대값**: 이력에 같은 변형이 있으면 그 변형의 역할별 호출 수, 없으면 이력의 변형당 평균(이력이 없으면 상수표) × 호출당 비용.
- **상한**: 팩의 모든 역할이 변형마다 한 번 호출된다고 본다(이력에 그 변형의 호출 수가 더 많으면 그 수. 예: interpret는 도구 관찰 묶음마다 호출되어 2번일 수 있다). 여기에 형식 복구 10%를 더하고 호출당 비용에 여유 계수 1.5를 곱한다.
- `--history DIR`은 manifest의 팩 id(`prompts/<팩>/pack.yaml`의 id. review-v1은 `codeye.review`)로 같은 팩의 batch만 쓴다. 준 디렉터리에 쓸 수 있는 행이 없으면 상수표로 돌아가지 않고 오류로 끝난다.
- 호출당 비용의 출처는 다음 우선순위다.
  1. 이력이 실제 batch면 역할별 usage 평균 × relay 단가(입력 5, 출력 10 microUSD/token)
  2. 이력이 dry run이면 역할별 최대 요청 바이트 ÷ 3(바이트당 토큰) × 5 + 출력 600 토큰 × 10
  3. 아래 상수표

| 팩 | 출처 | 역할별 변형당 호출 | 호출당 장부 차감 |
|---|---|---|---|
| review-v1 | [docs/37 §11](37-w15-discovery-and-progress.md#11-결과--tc-100-실모델-대조활성-팩-대-후보-팩) TC-100: 입력 11, 호출 32, $0.789 | context 1.0, investigate 1.0, verify 0.91 | 24,656 microUSD |
| review-r1 | 같은 곳: 입력 11, 호출 27, $0.598 | environment 0.09, documents 0.64, investigate 1.0, interpret 0, verify 0.73 | 22,148 microUSD |

상수표는 단일 파일 Go 발췌(R0 쌍·W15)에서 나온 값이다. R1 사례는 여러 파일이고 언어가 다르므로 실제 요청은 더 클 수 있다. 그래서 상한에 여유 계수를 둔다.

- **시작 전 거절**: runner는 relay의 `--report`를 읽는다. 상한 차감이 `remaining_microusd - 예약`(기본 $1.00)을 넘으면 batch를 시작하지 않는다. 상한 호출 수가 `max_requests - requests - 5`를 넘어도 마찬가지다. 장부 전체의 요청 수 상한(기본 200)도 같은 402로 거절되기 때문이다. 거절 사유는 `plan.json`과 manifest에 남는다.
- relay의 예약·상한이 실제 보장이다. 계획은 batch가 중간에 끊겨 앞 호출 비용만 쓰는 일을 줄이는 사전 거절이다. 응답 usage가 relay 상한에 가까운 가짜(`--mode expensive`)에서는 상수표 상한이 실제 차감보다 작았다. 그래도 relay가 상한에서 거절했고 runner가 멈췄다(§10.2).
- **중간 정지**: 사례마다 시작 전에 남은 금액·요청 수가 변형 하나의 상한에 미달하면 `budget_reserve_reached`로 멈춘다. 사례 실행 중 장부에 `refused`(예산·요청 수·정지)가 생기면 그 사례를 `invalid: budget_refused`로 두고 `relay_refused_budget`으로 멈춘다. 실행하지 않은 변형은 `not_run:<사유>` 행으로 남긴다.

## 6. 채점

### 6.1 자동 위치 대조(pre-fill)

보고서의 finding(도구 진단, 모델 후보, 모든 validation 상태)마다 위치와 code 근거 위치를 라벨 위치와 비교한다.

- before 변형: `positive_locations`. after 변형: before 줄 범위를 diff로 옮긴 위치(`after_mapped`). 정상 사례: `suspect_locations`.
- 같은 경로이고 줄 범위가 ±2줄 안에서 겹치면 `lines`, 그렇지 않고 심볼 이름이 같거나 제목에 있으면 `symbol`.
- 위치가 맞는다는 것은 판정이 아니다. 원인·조건이 맞는지는 사람이나 coordinator가 정한다. 위치 기반 비율은 `auto_*` 구조 지표로 따로 보고한다.

### 6.2 판정 양식

`run`이 끝나면 batch에 `judgments-template.json`을 만든다. 무효 행은 들어가지 않는다. 항목마다 라벨 요약(trigger, expected_impact, 대조 위치, 계약)과 finding 목록이 있다. finding마다 다음을 둔다.

- 자동 대조 결과
- 비어 있는 `verdict`: [docs/16 §3](16-evaluation.md#3-정답-라벨)의 `valid_actionable`·`valid_informational`·`false_positive`·`duplicate`·`insufficient_evidence`·`out_of_scope`
- 비어 있는 `matches_label`(라벨의 원인·조건과 같은 결함인가)
- `duplicate_of`, `notes`

양식에는 라벨 문장이 들어 있으므로 평가자 쪽에만 둔다. 판정 파일을 여러 개 주면 합친다. 같은 finding에 판정이 다르면 그 finding은 미판정으로 세고 충돌 목록에 남긴다. 한쪽을 임의로 고르지 않는다. 조정은 조정한 파일 하나를 다시 주는 방식이다.

## 7. 지표와 분모

모든 비율은 판정할 수 있는 단위만 분모에 넣는다. 판정할 수 없는 단위는 `unjudged`로 따로 센다. 변형에 범위 안 finding이 하나도 없으면 판정 없이 "아님"으로 정해진다. 판정된 finding 하나가 조건을 만족하면 "맞음"이다. 그 밖에 미판정 finding이 남아 있으면 미판정이다. 비율마다 Wilson 95% 구간을 붙인다. 무효 행(차단, 예산 거절, 입력 변경, 미실행)은 빼고 사유별로 센다.

| 지표 | 단위·분모 | 분자 |
|---|---|---|
| known-defect recall | 결함 쌍의 before 변형 | `matches_label=true`이고 `valid_actionable`·`valid_informational`인 finding이 있는 변형. 모든 finding과 모델 finding만의 두 가지를 낸다 |
| same-defect re-flag | after 변형 | `matches_label=true`인 finding(같은 결함이 남았다는 주장)이 있는 변형 |
| false-positive rate | 정상 사례 변형, after 변형, 둘을 합친 것 | `false_positive`인 finding이 있는 변형. 범위는 actionable finding(`supported`·`reproduced`, open)과 모든 finding 두 가지 |
| actionable precision | 판정된 actionable finding | `valid_actionable`(엄격), `valid_*`(넓게) |
| evidence validity | 게시 보고서 | `validate_bundle.check_report`로 새로 만든 입력과 대조해 통과한 보고서. 근거·claim 수를 함께 센다. 100%가 출시 조건이다 |
| unsupported assertions | 판정된 finding / 구조 | `insufficient_evidence` 비율. 구조로는 실행 근거 없는 `reproduced`, 검증기가 거부한 출력·오류 분류(꾸민 근거·범위 일반화·실행 주장, `w15_pack_compare.structural`), 통계 불일치 보고서 |
| duplicate burden | 판정된 finding / 구조 | `duplicate` 수. 구조로는 같은 변형에서 위치가 겹치는 finding 수 |
| 비용·시간 | batch | 검토 호출(역할·결과별), usage, 장부 요청 수·차감, batch 차감, 변형당 wall time(합·중앙값·최대) |
| 구조 지표 | 변형 | `auto_location_hit_before`, `auto_location_reflag_after`, `auto_suspect_flag_normal`, `auto_any_actionable_on_after_or_normal`, `ai_review_performed`, `model_saw_code`(`llm_exposed_files>0`) |

- 분해: 언어, 범주(category), `detection`별로 recall·FP와 구조 지표를 낸다.
- 비교: `score A B ...`가 batch를 나란히 표로 낸다. `tools`와 `review`, 두 팩을 같은 표에 둔다.
- 비용·시간과 정확도는 하나의 점수로 합치지 않는다.
- 모델의 자신감이나 서술을 지표 원천으로 쓰지 않는다. 지표는 보고서·기록·장부의 구조화 값과 판정에서만 나온다.

## 8. 반복 실행 분산

`--repeat N`(`--repeat-cases`로 사례 한정)은 같은 변형을 서로 다른 프로젝트로 N번 실행한다. `score`의 `variance`는 같은 변형의 반복마다 다음 일치도를 낸다.

- 자동 위치 대조 결과(최빈값 비율)
- actionable 수의 일치 비율과 값
- finding fingerprint Jaccard 평균
- `ai_review` 상태 일치

판정이 있으면 recall 단위의 결과도 반복별로 비교할 수 있다. 반복 수가 작으면 일치도는 참고값이다.

## 9. 보고서 읽기 평가

- `r1_eval.py reader BATCH --sample N --seed S --output DIR`는 무효가 아닌 첫 반복 보고서에서 kind·변형별로 고르게 N개를 뽑는다. 과제 `sheets/Snn.json`과 보고서 사본 `sheets/Snn-report.json`을 만든다. 과제 질문은 [docs/16 §7](16-evaluation.md#7-보고서-평가)를 따른다(중요한 위험 3개와 발생 조건·근거 위치, 분석하지 못한 범위, 다음 조치, 시간과 추가 질문 수). 파일 이름과 내용에 사례 ID·라벨이 없다.
- `answer-key.json`은 평가자용이다. before 변형은 라벨 조건·영향·위치와 참고 조치, 그 밖은 "라벨 결함 없음"과 반례 위치를 담는다. 분석하지 못한 범위는 보고서 구조(미지원·생략·실패 task, 제약 코드, `ai_review`, `llm_exposed_files`)에서 만든다.
- `reader-score --key K --answers A ...`는 사람의 답을 채점한다. 결함 위치를 상위 3개 위험에서 찾은 비율, 반례 위치를 결함으로 지목한 비율, 공백 범위 재현율을 낸다. 사람이 채점한 조건·조치 정답률(`graded`), 시간 중앙값, 추가 질문 평균도 낸다.
- 답이 없으면 결과는 `not_measured`다. 모델로 독자를 흉내 내지 않는다.

## 10. 시험과 실행 결과

### 10.1 단위 시험(CI)

- `scripts/test_r0_openai_gateway.py`(37개, W24 추가 12개).
  - 정답 차단: 줄바꿈으로 나뉜 문구, camelCase에 숨은 토큰, 대소문자가 다른 부분 문자열을 막는다. 예약·전달·차감이 없고 장부에는 ID만 남는다. 근접 문구와 전달하지 않는 키는 통과한다. 거절 기록이 남는다. 형식이 틀린 목록은 거부한다. 엄격 방식에서는 모델이 돌려준 표현도 막는다.
  - `--allow-model-echo`: relay가 돌려준 답에 나온 ID만 예외다. 아직 나오지 않은 ID, 클라이언트가 보낸 assistant 메시지, 예외 ID와 다른 ID가 섞인 요청은 막는다. 예외 기록과 보고 필드가 남는다. 목록 없이 옵션만 주면 시작하지 않는다.
  - corpus 목록 교차 시험: 모든 사례·변형·README 입력에서 0건이고, 사례마다 심은 토큰과 문구는 잡는다.
  - 시험용 upstream: loopback URL만 받고 키를 읽거나 보내지 않는다. 시험 장부와 실제 장부를 서로 거부한다. 감시용 목록은 시험용 upstream에서만 받는다.
- `scripts/test_r1_eval.py`(16개).
  - runner: Wilson 기준값, diff 위치 이동, 층별 표본의 seed 재현성과 층 포함, 상수표·이력 추정과 거절(금액·요청 수·정지), 다른 팩 이력의 오류 처리, 등록부 표시(dry run 제외), 장부 귀속, 예외 ID의 사례 단위 확인, 감시용 목록.
  - 채점: 위치 pre-fill, 미판정 계수, 판정 지표, 판정 충돌, 분산, 독립성 표시.
  - 보고서 읽기: 과제의 라벨 비노출, 답이 없을 때 `not_measured`, 답이 있을 때의 채점.
  - dry run에서 얻은 실제 tools 보고서 3개(`scripts/testdata/r1_eval`)는 새로 만든 입력과 근거 대조까지 통과한다.
- CI: `verify.yml`의 "Documentation contracts and regression fixture" 단계가 두 시험을 `python -m unittest discover`로 실행한다. 모델·키·네트워크가 필요 없다.

### 10.2 dry run

모두 같은 WSL2 머신(12코어, 다른 작업과 공유)에서 실행했다. 모델 호출은 없다. review 조건은 가짜 upstream과 `--test-upstream` relay(시험 장부)를 썼다. 가짜의 답은 입력 구조에서 만든 고정 문장이므로 아래 위치 대조·검토 수치에는 품질 의미가 없다. 가짜는 첫 code 근거를 인용하는데, 발췌가 결함 파일 둘레이므로 위치 대조가 높게 나온다.

**tools, 55개 사례 전체(변형 100개).** W18 병합 뒤 go·staticcheck·cargo·clippy·node·eslint·tsc 플래그로 실행했다. 100개 모두 `partial`이다(검토 task가 `model_source_not_approved`).

| 언어 | 결과 |
|---|---|
| C/C++, Python | 변형 40개 모두 `pack-gap.<생태계>:analyzer_pack_missing`. 분석기 미실행 |
| Go | go vet·Staticcheck 실행. finding 3개(R1-GO-06 before의 copylocks, R1-GO-09 before의 structtag·SA5008) |
| JS/TS | ESLint 20개, tsc·type-aware ESLint는 TS 모듈 4개. finding 5개(R1-JS-02 before의 `no-unsafe-finally`, R1-JS-07 전후) |
| Rust | 변형 20개 모두 보고서 조회 500 `REPORT_INVALID`(§12). 분석은 `lockfile_missing` |

보고서가 있는 80개는 새로 만든 입력과의 근거 대조를 모두 통과했다. 관측한 도구 버전은 go1.27.4, Staticcheck 2026.2.2, ESLint 10.15.0, tsc 6.0.3이다.

**정답 차단과 envelope.**

1. 지침 팩만 허용한 목록으로 review dry run을 돌렸다. review-v1 변형 11개 중 3개, review-r1 14개 중 6개가 막혔다. 모두 Go 사례의 verify(역할 계약의 `ownership transfer`)와 R1-GO-06 before의 interpret(go vet 메시지)였다.
2. 감시용 목록으로 두 팩의 envelope를 모았다(Go 3개 사례와 C++ 1개, 변형 각 8개, envelope 84개).
3. 도구 메시지와 envelope를 허용한 목록(7,724개)으로 Go 11개 사례 전체와 C++·JS 1개씩을 다시 돌렸다.
   - review-v1: 변형 24개, 막힘 0. Go 20개 `completed`, C++·JS 4개는 검토 입력이 없어 `partial`. 시험 장부 요청 60건.
   - review-r1: 변형 25개(R1-GO-10 before 2회 반복 포함). 막힌 요청은 R1-GO-01 before에 일부러 심은 문구 하나뿐이었다(investigate, ID `R1-GO-01/prose/0.0`, `invalid: planted_leak`). 시험 장부 요청 58건.
4. W18 병합 뒤 tools 결과로 목록을 다시 만들었다(7,723개). 허용 텍스트 때문에 빠진 12개는 §3.4에 적었다.
5. 가짜에 `--echo-text "goroutine leak"`를 주고 R1-GO-02·03 before를 두 방식으로 돌렸다. 엄격 방식에서는 두 verify가 막혔다(원인 `model_echo`). `--allow-model-echo`에서는 둘 다 `completed`이고 verify 2건이 예외로 기록되었으며 사례 단위 확인을 통과했다.

**예산 거절.**

- 시작 전: 상한 $5 시험 장부에서 review-r1 13개 사례(변형 25개) batch의 상한 추정 $4.568이 남은 금액 − 예약 $1.00을 넘어 시작하지 않았다. `plan.json`과 manifest에 사유가 남았다.
- 중간: 상한 $0.80 시험 장부, 가짜 `--mode expensive`, `--reserve-usd 0.05`로 review-v1 3개 사례(변형 6개)를 돌렸다. 상한 추정 $0.732는 시작을 허용했다. R1-GO-02 before가 끝난 뒤 after의 세 번째 호출을 relay가 `budget_exhausted`로 거절했다. 그 변형은 `invalid: budget_refused`, 남은 4개는 `not_run:relay_refused_budget`이고 batch는 `relay_refused_budget`으로 멈췄다. 장부는 요청 5건, 차감 $0.5587, 남은 금액 $0.2414이다.

**채점·읽기.** `score`로 tools·두 팩 dry run을 나란히 냈다. 판정 파일이 없으므로 판정 지표는 finding이 없는 변형만 "아님"으로 정해지고 나머지는 미판정이다. 비교는 `independent: false`(dry run)다. 근거 유효성은 tools 80/80, review-v1 24/24, review-r1 24/24다. `reader --sample 6`은 라벨·사례 ID 없는 과제 6개와 평가자용 answer key를 만들었다. 답 없이 `reader-score`를 실행하면 `not_measured`다.

**R0 파일럿.** 공용 실행 틀로 옮긴 `r0_pilot.py`를 가짜 relay와 합성 사례 하나(`CODEYE_R0_CASES=Q01-before`)로 실행했다. 보고서 1개가 `completed`, `ai_review: performed`(context·investigate·verify 수락)로 게시·조회되었고 브라우저 시험 1개가 통과했다. 시험 장부 요청은 3건이다. 실모델·Git·고정 공개 커밋 입력은 이번에 실행하지 않았다.

## 11. 유료 batch 실행 방법(coordinator)

키는 coordinator의 셸에만 있다. runner는 relay를 시작하지 않고 키를 읽지 않는다. 아래는 저장소 루트에서 실행한다. `$R`은 영구 비공개 결과 루트, `$LEDGER`는 R1이 공유하는 relay 장부다. 도구 경로는 노드에 맞게 바꾼다. tools와 review 조건에 같은 도구 플래그를 준다.

```bash
TOOLS=(--go "$GO" --tool staticcheck="$STATICCHECK" --tool cargo="$CARGO" --tool clippy="$CLIPPY"
       --tool node="$NODE" --tool eslint="$ESLINT_JS" --tool tsc="$TSC_JS")
REVIEW=(--opencode "$OPENCODE" --model codeye-gateway/gpt-6.2-sol)

# 1. tools(모델 없음): 전체와 비교용 Go 표본
python3 scripts/r1_eval.py run --condition tools --results-root "$R" --batch tools-all "${TOOLS[@]}" --max-workers 3
python3 scripts/r1_eval.py run --condition tools --results-root "$R" --batch tools-go --languages go "${TOOLS[@]}"

# 2. envelope 수집 dry run(가짜 upstream, 시험 장부, 키 없음). 이 batch가 계획 이력도 된다.
python3 scripts/r1_eval.py capture-list --output "$R/capture-list.json"
mkdir -p "$R/cap"
env -u OPENAI_API_KEY python3 scripts/r1_fake_upstream.py --ready-file "$R/cap/fake.json" --dump "$R/cap/dump" &
PORT=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["port"])' "$R/cap/fake.json")
env -u OPENAI_API_KEY python3 scripts/r0_openai_gateway.py --ready-file "$R/cap/relay.json" \
  --ledger "$R/cap/ledger.jsonl" --model gpt-6.2-sol --forbidden-phrases "$R/capture-list.json" \
  --test-upstream "http://127.0.0.1:$PORT" &
for PACK in review-r1 review-v1; do
  python3 scripts/r1_eval.py run --condition review --pack $PACK --dry-run --results-root "$R" \
    --batch capture-$PACK --languages go --relay-file "$R/cap/relay.json" --ledger "$R/cap/ledger.jsonl" \
    --forbidden-phrases "$R/capture-list.json" "${TOOLS[@]}" "${REVIEW[@]}"
done
# 가짜와 시험 relay를 SIGTERM으로 끝낸다.

# 3. 정답 목록. dropped.in_allowed_text를 읽고 허용 텍스트가 뺀 라벨 표현을 확인한다.
python3 scripts/r1_eval.py allow-text --tools-batch "$R/tools-all" --request-dump "$R/cap/dump" --output "$R/allow.txt"
python3 -I fixtures/r1-quality/corpus.py forbidden --allow-text prompts/review-v1 --allow-text prompts/review-r1 \
  --allow-text "$R/allow.txt" --output "$R/forbidden.json"

# 4. 실제 relay(coordinator 셸). --allow-model-echo는 선택이다(§3.4).
python3 scripts/r0_openai_gateway.py --ready-file "$R/relay-$(date +%s).json" --ledger "$LEDGER" \
  --model gpt-6.2-sol --forbidden-phrases "$R/forbidden.json" [--allow-model-echo] &

# 5. 계획과 장부 확인(거절이면 종료 코드 1)
python3 scripts/r1_eval.py plan --pack review-r1 --languages go --history "$R/capture-review-r1" --ledger "$LEDGER"
python3 scripts/r1_eval.py plan --pack review-v1 --languages go --history "$R/capture-review-v1" --ledger "$LEDGER"

# 6. 유료 batch: review-r1을 먼저 실행한다(요청 수 상한 때문, 아래 표).
RELAY=(--relay-file "$R/relay-<시각>.json" --ledger "$LEDGER" --forbidden-phrases "$R/forbidden.json")
python3 scripts/r1_eval.py run --condition review --pack review-r1 --results-root "$R" --batch paid-r1-go \
  --languages go --history "$R/capture-review-r1" "${RELAY[@]}" "${TOOLS[@]}" "${REVIEW[@]}"
python3 scripts/r1_eval.py run --condition review --pack review-v1 --results-root "$R" --batch paid-v1-go \
  --languages go --history "$R/capture-review-v1" "${RELAY[@]}" "${TOOLS[@]}" "${REVIEW[@]}"

# 7. 판정과 채점: 각 batch의 judgments-template.json을 채운 뒤
python3 scripts/r1_eval.py score "$R/tools-go" "$R/paid-r1-go" "$R/paid-v1-go" \
  --judgments J1.json [--judgments J2.json] --output "$R/score.json"
python3 scripts/r1_eval.py reader "$R/paid-r1-go" --sample 6 --seed 24 --output "$R/reading-r1"
```

추정은 이 문서의 dry run(Go 사례 전체, 변형 20개)을 이력으로 계산했다. 호출당 비용은 요청 바이트 ÷ 3 토큰과 출력 600 토큰을 relay 단가로 셈한 보수값이다(호출당 약 $0.032~0.041). W15의 실측 호출당 차감($0.022~0.025)보다 크다. 남은 장부는 docs/37의 W15 기록($8.492, 요청 137건)을 가정했다. 실제 값은 5단계의 `plan --ledger`로 확인한다.

| 순서 | batch | 기대 호출 | 기대 차감 | 상한 호출 | 상한 차감 | 시작 전 남은 값 | 판정 |
|---|---|---|---|---|---|---|---|
| A | review-r1 Go 20 | 53.4 | $1.88 | 113 | $5.71 | $8.49, 137건 | 시작 |
| A | review-v1 Go 20 | 60.0 | $2.32 | 66 | $3.83 | $6.62, 83건 | 시작 |
| A 역순 | review-v1 Go 20 다음 review-r1 Go 20 | 60.0 / 53.4 | $2.32 / $1.88 | 66 / 113 | $3.83 / $5.71 | r1 시작 전 $6.17, 77건 | r1 거절(113건 > 77 − 5, $5.71 > $6.17 − $1.00) |
| B | `--sample 8 --seed 24`(변형 14) r1 → v1 | 33.4 / 42.0 | $1.18 / $1.62 | 79 / 47 | $3.98 / $2.68 | $8.49, 137건 → $7.31, 103건 | 둘 다 시작 |
| C | B 다음 분산: `--cases R1-GO-02,R1-GO-09 --variants before --repeat 3`, r1 → v1 | 21.0 / 18.0 | $0.73 / $0.70 | 37 / 20 | $1.85 / $1.15 | $5.68, 61건 → $4.95, 40건 | 둘 다 시작 |

요청 수 상한(장부 전체 200건)이 금액보다 먼저 묶인다. A는 Go 사례 전체를 두 팩으로 비교하고 반복 실행은 남지 않는다(기대 사용 뒤 약 23건). B+C는 표본이 작지만 두 사례의 반복 분산을 볼 수 있다. Go가 아닌 사례는 검토 입력이 없어(§12) 비용만 쓰므로 유료 batch에서 뺐다.

## 12. 제한

- **Go가 아닌 사례는 모델 검토를 측정할 수 없다.** 검토용 code 발췌가 Go만 지원한다(`code_excerpts_go_only`). C/C++·Python·JS/TS·Rust 사례에서 v1의 context·investigate와 r1의 investigate는 `review_input_unavailable`로 끝난다. r1은 환경 역할을 여전히 호출해 비용을 쓴다. 발췌가 다른 언어를 지원하기 전에는 review 조건을 Go로 한정한다.
- **Rust 보고서를 조회하지 못한다.** main의 보고서 검증이 설정 파일 없는 Rust 규칙 문맥(`config=recorded`, 경로 없음)을 거부해 500 `REPORT_INVALID`가 난다. W21 브랜치에 고친 검증이 있다. 또 corpus의 Rust 사례에는 Cargo.lock이 없어 `--offline --locked` 분석이 `lockfile_missing`으로 끝난다. Rust tools 결과는 둘이 고쳐질 때까지 측정되지 않는다.
- **C/C++·Python 분석 팩이 아직 없다.** 이 조건의 tools 결과는 `analyzer_pack_missing` 공백뿐이다. 팩이 병합되면 runner는 그대로 두고 `--tool` 플래그만 더한다.
- **모델 표현 차단의 크기를 모른다.** 엄격 방식이 맞힌 사례를 얼마나 무효로 만드는지는 실모델 batch에서만 보인다. 가짜로는 경로만 확인했다. 예외 방식의 범위는 relay 프로세스 전체이고 사례 단위 확인은 runner가 한다. runner를 거치지 않고 relay를 쓰면 이 확인이 없다.
- **dry run 수치에는 품질 의미가 없다.** 가짜 upstream은 정답을 모르고 입력 구조로 답한다. 위치 대조·검토 수·차감은 경로 동작의 근거일 뿐이다.
- **정답 목록은 표현 일치만 본다.** 다른 말로 바꾼 정답, 다른 언어로 쓴 설명, 코드 모양의 힌트는 막지 않는다. 라벨 문장의 5-gram과 고유 토큰만 대상이다.
- **판정은 사람이 한다.** 위치 자동 대조는 pre-fill이다. 판정 파일이 없으면 recall·FP·precision은 대부분 미판정으로 남는다. 판정자 간 일치도는 판정이 둘 이상일 때 충돌 목록으로만 보인다.
- **보고서 읽기는 측정하지 않았다.** 과제와 채점기만 있다. 모델로 독자를 흉내 내지 않는다.
- **비용 추정은 보수적 근사다.** 바이트당 3토큰과 여유 계수 1.5는 실측에 맞춘 값이 아니다. 실제 batch의 usage를 이력으로 주면 그 값을 쓴다.
- **holdout 표시는 같은 결과 루트 안에서만 동작한다.** 다른 `--results-root`에서 실행한 batch는 등록부가 달라 보이지 않는다.
- review-r1 rc2 등 팩을 바꾸면 envelope와 지침 허용 텍스트가 바뀐다. 목록을 다시 만들고(§3.1.1), 같은 팩 id의 이전 실제 실행이 있으면 새 batch는 `holdout_seen_by_prior_revision`으로 표시된다.
- 실행 환경은 WSL2 한 대다. Windows·RHEL 실측, 여러 노드 분할 실행은 하지 않았다.

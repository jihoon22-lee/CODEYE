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
- 분석기 플래그는 `--tool NAME=/abs/path`로 받아 agent에 `--NAME PATH`로 그대로 넘긴다. 값이 없는 agent 옵션(예: 두 타입 검사기를 함께 쓰는 `python-both-type-checkers`)은 `--agent-flag NAME`으로 준다. 둘 다 manifest에 남고 tools 조건의 holdout digest에 들어간다. 이 빌드가 모르는 이름이면 agent가 사용 오류로 끝나고 runner가 멈춘다. 새 팩이 병합되면 runner를 고치지 않고 플래그만 더한다. 공백은 보고서의 task(`pack-gap.<생태계>`, `tool_not_configured` 등)에서 읽는다.
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
- dry run 최종 목록(모든 분석 팩, W24 2c 다섯 언어 검토 입력, review-r1 2.0.0-rc2 병합 뒤)은 7,722개 항목이다. 허용 텍스트는 두 팩, tools batch의 도구 메시지·규칙 ID 22줄, corpus 전체를 두 팩으로 돌린 capture의 envelope 1,200개(요청 600건의 system·user)다. 입력에 있어 12개, 허용 텍스트에 있어 13개를 뺐다. W17·W21·2c 병합 뒤에도 항목과 뺀 ID는 바뀌지 않았다. 2c 뒤에는 R1-RS-07의 `reference cycle`·`cycle`이 Rust 사례 envelope(언어 지침)에도 나온다.

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
- **지침 팩.** 팩 문장이 라벨 표현을 담아 목록에서 빠진 항목이 있다. `double free`(review-r1 languages/cpp.md), `stale`(review-v1 languages/js-ts.md, review-r1 languages/javascript.md), `reference cycle`·`cycle`(두 팩의 rust.md, review-v1 cpp.md), `partial write`(두 팩의 domains.md)다. rc2는 스냅샷 생태계의 언어 지침을 envelope에 넣으므로 같은 표현이 envelope에도 있다. 정답 누설은 아니지만 지침이 이 결함 종류를 직접 언급한다는 뜻이다. 팩을 바꾸면 `dropped.in_allowed_text`를 다시 확인한다.
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

모든 비율은 판정할 수 있는 단위만 분모에 넣는다. 판정할 수 없는 단위는 `unjudged`로 따로 센다. 변형에 범위 안 finding이 하나도 없으면 판정 없이 "아님"으로 정해진다. 판정된 finding 하나가 조건을 만족하면 "맞음"이다. 그 밖에 미판정 finding이 남아 있으면 미판정이다. 비율마다 Wilson 95% 구간을 붙인다. 무효 행(차단, 예산 거절, 입력 변경, 예외 ID 미확인, review 조건의 비밀값 선별 실패, 미실행)은 빼고 사유별로 센다.

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
| 검토 결과 분류 | 보고서가 있는 모든 변형(무효 포함) | `review_outcomes`: `performed`, `secret_screen_failed`(W21 비밀값 작업이 실행됐으나 실패해 모든 검토 역할이 `skipped_policy`), `review_input_unavailable`, `model_source_not_approved`(tools 조건), 그 밖 `not_performed:<사유>`. 비밀값 선별 실패는 정책 결과이고 모델 실패가 아니므로 따로 센다. review 조건에서는 그 변형을 `invalid: secret_screen_failed`로 지표에서 뺀다. 모델이 실행되지 않은 변형을 모델이 놓친 것으로 세지 않기 위해서다. 행의 `review_outcome`과 진행 출력에도 같은 값이 남는다 |
| 구조 지표 | 변형 | `auto_location_hit_before`, `auto_location_reflag_after`, `auto_suspect_flag_normal`, `auto_any_actionable_on_after_or_normal`, `ai_review_performed`, `model_saw_snapshot_files`(`llm_exposed_files>0`. 보고서는 수만 주고 README 같은 문서 발췌도 센다. 모델이 코드를 봤다는 뜻이 아니다) |
| 코드 노출 | 변형 | 보존한 검토 기록의 근거 목록(경로·종류)에서 낸다. `model_saw_code`: 어느 검토 역할의 입력에 kind `code` 발췌가 있음. `labelled_file_in_investigate`: before 변형에서 라벨 결함 파일이 investigate 입력에 있음(recall의 상한을 읽는 값). `no_code_variants`: review 조건에서 코드가 가지 않은 변형 목록. 모델이 답한(수락 또는 출력 거부) 호출이 있는 역할의 기록만 센다. 호출이 모두 막혔거나 거절·실패했거나 호출이 없는 기록은 준비된 입력일 뿐이므로 세지 않는다. 기록은 review input의 근거 목록을 그대로 담으므로 요청 dump가 없는 유료 batch에서도 계산된다. dry run 200개 변형에서 역할마다 dump의 경로와 같음을 확인했다(§10.2 항목 7) |

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
- `scripts/test_r1_eval.py`(21개).
  - runner: Wilson 기준값, diff 위치 이동, 층별 표본의 seed 재현성과 층 포함, 상수표·이력 추정과 거절(금액·요청 수·정지), 다른 팩 이력의 오류 처리, 등록부 표시(dry run 제외), 장부 귀속, 예외 ID의 사례 단위 확인, 감시용 목록, 비밀값 선별 실패를 별도 결과로 분류하고 review 지표에서 빼는 무효 사유 순서, `--agent-flag` 이름 검사.
  - 채점: 위치 pre-fill, 미판정 계수, 판정 지표, 판정 충돌, 분산, 독립성 표시, 기록 기반 코드 노출(README만 본 변형은 코드 노출이 아님).
  - 보고서 읽기: 과제의 라벨 비노출, 답이 없을 때 `not_measured`, 답이 있을 때의 채점.
  - dry run에서 얻은 실제 tools 보고서 3개(`scripts/testdata/r1_eval`)는 새로 만든 입력과 근거 대조까지 통과한다. 기록된 규칙 문맥은 digest만 있으면 통과하고 digest가 없으면 거부된다.
- CI: `verify.yml`의 "Documentation contracts and regression fixture" 단계가 두 시험을 `python -m unittest discover`로 실행한다. 모델·키·네트워크가 필요 없다.

### 10.2 dry run

모두 같은 WSL2 머신(12코어, 다른 작업과 공유)에서 실행했다. 모델 호출은 없다. review 조건은 가짜 upstream과 `--test-upstream` relay(시험 장부)를 썼다. 가짜의 답은 입력 구조에서 만든 고정 문장이므로 아래 위치 대조·검토 수치에는 품질 의미가 없다. 가짜는 첫 code 근거를 인용하는데, 발췌가 결함 파일 둘레이므로 위치 대조가 높게 나온다.

**tools, 55개 사례 전체(변형 100개).** 모든 분석 팩(W16~W21) 병합 뒤 go·staticcheck·cargo·clippy·cppcheck·clang-tidy·ruff·mypy·pyright·bandit·lizard·jscpd·gitleaks·node·eslint·tsc 플래그와 `--agent-flag python-both-type-checkers`로 실행했다. 100개 모두 `partial`이고 검토 결과 분류는 모두 `model_source_not_approved`다. 분석기 이름은 모두 이 빌드의 agent가 받았다.

| 언어 | 결과 |
|---|---|
| C/C++ | cppcheck는 20개 모두 `missing_include_files` 구성 오류로 끝났고 R1-CPP-04 before에서 finding 2개를 남겼다. clang-tidy는 compile DB가 없어(`compile_db_missing`) 실행되지 않았다 |
| Python | ruff·mypy·pyright·bandit 20개 모두 실행. finding 5개(ruff 3개 변형, mypy 2개 변형. R1-PY-04 전후, R1-PY-11 before). pyright·bandit은 finding 없음 |
| Go | go vet·Staticcheck 실행. finding 3개(R1-GO-06 before의 copylocks, R1-GO-09 before의 structtag·SA5008) |
| JS/TS | ESLint 20개, tsc·type-aware ESLint는 TS 모듈 4개. finding 5개(R1-JS-02 before의 `no-unsafe-finally`, R1-JS-07 전후) |
| Rust | 보고서 20개 모두 조회된다(W21이 main의 Rust 규칙 문맥 검증을 고쳤다). cargo check·Clippy는 20개 모두 `lockfile_missing` 구성 오류, finding 0개(§12) |
| 구조·비밀값(W21) | lizard·jscpd·gitleaks가 변형 100개 모두에서 실행되어 finding 없이 끝났다. gitleaks가 성공했으므로 비밀값 선별로 막힌 검토는 없다 |

보고서 100개 모두 새로 만든 입력과의 근거 대조를 통과했다. 관측한 도구 버전은 go1.27.4, Staticcheck 2026.2.2, cppcheck 2.22.0, ruff 0.16.3, mypy 2.4.1, pyright 1.1.414, bandit 1.9.4, ESLint 10.15.0, tsc 6.0.3, cargo check 1.99.1, Clippy 0.1.99, lizard 1.26.1, jscpd 5.4.3, gitleaks 8.30.1이다. finding은 모두 15개다(Python 5, JS/TS 5, Go 3, C/C++ 2).

**정답 차단과 envelope.** 항목 1~6은 W24 2c(다섯 언어 검토 입력) 병합 전, 항목 7은 병합 뒤의 실행이다.

1. review-r1 rc1 시점에 지침 팩만 허용한 목록으로 review dry run을 돌렸다. review-v1 변형 11개 중 3개, review-r1 14개 중 6개가 막혔다. 모두 Go 사례의 verify(역할 계약의 `ownership transfer`)와 R1-GO-06 before의 interpret(go vet 메시지)였다.
2. 감시용 목록으로 envelope를 모으고, 도구 메시지와 envelope를 허용한 목록으로 Go 11개 사례 전체와 C++·JS 1개씩을 다시 돌렸다. 두 팩 모두 심은 문구 말고는 막힌 요청이 없었다(rc1, W18 병합 전).
3. W16 C/C++ 팩과 review-r1 2.0.0-rc2가 병합된 뒤 같은 절차를 다시 했다(W17 Python 팩 병합 뒤 tools 결과로 목록을 한 번 더 만들었고 항목은 같았다). 감시용 목록으로 6개 사례(Go 3, C++·JS·Python 1)의 envelope 100개를 모았고(역할: v1 context·investigate·verify 각 6, r1 environment 8, documents 4, investigate 6, interpret 8, verify 6), tools 결과와 함께 최종 목록(7,722개)을 만들었다. 이 목록으로 Go 11개 사례 전체와 C++·JS 1개씩을 돌렸다.
   - review-v1(1.0.0): 변형 24개, 막힘 0. Go 20개 `completed`, C++·JS 4개는 검토 입력이 없어 `partial`. 시험 장부 요청 60건(역할마다 20).
   - review-r1(2.0.0-rc2): 변형 25개(R1-GO-10 before 2회 반복 포함). 막힌 요청은 R1-GO-01 before에 일부러 심은 문구 하나뿐이었다(investigate, ID `R1-GO-01/prose/0.0`, `invalid: planted_leak`). 시험 장부 요청 62건(investigate·verify 20, documents 10, interpret 8, environment 4). C++·JS 사례에서도 environment·interpret가 호출되었다.
4. 가짜에 `--echo-text "goroutine leak"`를 주고 R1-GO-02·03 before를 두 방식으로 돌렸다. 엄격 방식에서는 두 verify가 막혔다(원인 `model_echo`). `--allow-model-echo`에서는 둘 다 `completed`이고 verify 2건이 예외로 기록되었으며 사례 단위 확인을 통과했다.
5. 모든 분석 팩 병합 뒤, W24 2c 병합 전에 tools 결과로 목록을 다시 만들고(항목 같음), 7개 사례(Go 4, C++·Python·Rust 1)의 변형 13개를 두 팩으로 돌렸다. R1-GO-02 before에는 문구를 심었다.
   - review-v1: 막힘 0. Go 7개와 Python·Rust 4개가 `performed`, C++ 2개는 `review_input_unavailable`. 시험 장부 요청 29건. Python·Rust의 `performed`는 README만 본 것이었다(요청 dump 확인. 기록 기반 `model_saw_code`는 7/13).
   - review-r1: 막힌 요청은 심은 문구 하나뿐이었다(investigate, `invalid: planted_leak`). Go 6개가 `performed`, C++·Python·Rust 6개는 `review_input_unavailable`. 시험 장부 요청 39건.
   - 이 실행에서 가짜의 review-r1 environment·interpret 답이 rc2 계약에 맞지 않아 `output_rejected`가 되고, 형식 복구 호출도 같은 이유로 거부된 것을 발견했다. 항목 1~3의 r1 environment·interpret도 모두 이렇게 두 번 거부되어 끝났다(필수 역할이 아니어서 `completed`에는 영향이 없었다). 두 호출의 요청은 모두 relay의 대조를 거쳤으므로 그 요청에 대한 차단 확인은 유효하다. 수락된 출력이 뒤 역할의 입력으로 가는 경로는 고친 뒤의 실행에서 확인했다. 가짜를 고쳐 environment는 gap 모듈마다 그 모듈의 요구사항·근거 ID를 인용하고 interpret는 첫 관찰의 한계 하나를 낸다. 고친 뒤 4개 사례(R1-GO-06·09, R1-PY-04, R1-CPP-04 before)에서 모든 역할이 한 번에 수락되었다(요청 12건).
6. 비밀값 선별 실패: 버전 질의에는 실제 도구로 답하고 검사는 실패하는 대역을 비밀값 분석기 경로로 주고 R1-GO-06 before를 review-r1로 돌렸다. 비밀값 task는 `failed`(`scan_incomplete`), 검토 역할 5개는 모두 `skipped_policy`(`secret_screen_failed`)였고 relay 요청은 0건이다. 행은 `review_outcome: secret_screen_failed`, `invalid: secret_screen_failed`로 지표에서 빠졌고 `score`의 검토 결과 분류에 `secret_screen_failed 1`이 보인다.
7. W24 2c(다섯 언어 검토 입력) 병합 뒤 corpus 전체(55개 사례, 변형 100개)를 두 팩으로 감시용 목록 아래 돌렸다. 시험 장부의 요청 상한(200건) 때문에 언어마다 새 시험 relay를 썼다.
   - 두 팩 모두 변형 100개가 `performed`이고 모든 호출이 한 번에 수락되었다. review-v1은 context·investigate·verify 각 100건(요청 300건), review-r1은 investigate·verify 각 100건, documents 51건, environment 40건(C/C++·Python), interpret 9건(요청 300건)이다. `completed` 60개, `partial` 40개다(C/C++의 cppcheck 구성 오류와 Rust의 `lockfile_missing` 때문).
   - 코드가 모델에 가지 않은 변형은 없다. 두 팩 모두 변형 100개의 investigate·verify 입력에 code 발췌가 있고, defect before 변형 45개 모두 라벨 결함 파일이 investigate 입력에 있었다. `source_in_excluded_directories`는 어느 변형에도 없었다. 모든 변형에 `caller_expansion_go_only`가 남는다.
   - 근거 대조는 두 팩 모두 100/100이다. 보존한 검토 기록의 근거 경로가 dump의 review input 경로와 역할마다 같음을 200개 변형 모두에서 확인했다.
   - 정답 목록을 이 capture의 envelope로 다시 만들었다(항목·뺀 ID 같음). tools 결과는 2c가 분석기를 바꾸지 않았으므로 W21 병합 뒤 것을 썼다.
   - 오프라인 대조: capture 요청 600건 전부를 relay와 같은 대조기(`load_forbidden(...).matches`, 전달할 `input` 메시지)로 새 목록과 대조했다. 일치는 0건이다. relay가 막은 결과가 아니라 같은 규칙으로 다시 본 결과다.
   - relay 차단: 새 목록을 쓰는 시험 relay로 Go가 아닌 4개 사례(R1-CPP-04·PY-04·JS-02·RS-01 before)를 두 팩으로 돌렸다. r1에는 R1-PY-04(Python `#` 주석), v1에는 R1-RS-01(Rust `//` 주석)에 문구를 심었다. 심은 문구만 막혔고(r1 investigate, v1 context, `invalid: planted_leak`) 나머지 변형 6개는 막힘 없이 끝났다.

**예산 거절.**

- 시작 전: 상한 $5 시험 장부에서 review-r1 13개 사례(변형 25개) batch의 상한 추정 $4.568이 남은 금액 − 예약 $1.00을 넘어 시작하지 않았다. `plan.json`과 manifest에 사유가 남았다.
- 중간: 상한 $0.80 시험 장부, 가짜 `--mode expensive`, `--reserve-usd 0.05`로 review-v1 3개 사례(변형 6개)를 돌렸다. 상한 추정 $0.732는 시작을 허용했다. R1-GO-02 before가 끝난 뒤 after의 세 번째 호출을 relay가 `budget_exhausted`로 거절했다. 그 변형은 `invalid: budget_refused`, 남은 4개는 `not_run:relay_refused_budget`이고 batch는 `relay_refused_budget`으로 멈췄다. 장부는 요청 5건, 차감 $0.5587, 남은 금액 $0.2414이다.

**채점·읽기.** `score`로 최종 tools·두 팩 dry run을 나란히 냈다. 판정 파일이 없으므로 판정 지표는 finding이 없는 변형만 "아님"으로 정해지고 나머지는 미판정이다. 비교는 `independent: false`(dry run)다. 근거 유효성은 W21 이전 tools 80/80, review-v1 24/24, review-r1 24/24이고 W21 이후 tools 100/100, 항목 5의 review-v1 13/13, review-r1 12/12(심은 변형 제외), 고친 가짜 4/4다. 검사기는 두 번 제품 계약에 맞췄다. 처음에는 세 batch 모두 R1-CPP-04 before 하나가 실패했다. `validate_bundle.check_report`가 원시 관찰의 stream을 stdout만 받았는데, cppcheck는 XML을 stderr에 쓰고 제품의 보고서 검증(`internal/report/analyzer.go`)은 두 stream을 모두 허용한다. 다음으로 W21 병합 직후 tools 100개가 모두 실패했다. 검사기가 기록된 규칙 문맥(`config: recorded`)에 설정 경로와 digest를 모두 요구했는데, 제품(`internal/report/validate.go`)은 digest만 요구하고 도구가 읽은 파일이 없으면 경로를 생략한다(gitleaks 기본 규칙). 둘 다 검사기를 제품 쪽에 맞췄다. `reader --sample 6`은 라벨·사례 ID 없는 과제 6개와 평가자용 answer key를 만들었다. 답 없이 `reader-score`를 실행하면 `not_measured`다.

**R0 파일럿.** 공용 실행 틀로 옮긴 `r0_pilot.py`를 가짜 relay와 합성 사례 하나(`CODEYE_R0_CASES=Q01-before`)로 실행했다. 보고서 1개가 `completed`, `ai_review: performed`(context·investigate·verify 수락)로 게시·조회되었고 브라우저 시험 1개가 통과했다. 시험 장부 요청은 3건이다. 실모델·Git·고정 공개 커밋 입력은 이번에 실행하지 않았다.

## 11. 유료 batch 실행 방법(coordinator)

키는 coordinator의 셸에만 있다. runner는 relay를 시작하지 않고 키를 읽지 않는다. 아래는 저장소 루트에서 실행한다. `$R`은 영구 비공개 결과 루트(모든 batch와 holdout 등록부가 같은 곳에 있어야 한다), `$LEDGER`는 R1이 공유하는 relay 장부다. 도구 경로는 노드에 맞게 바꾼다. tools와 review 조건에 같은 도구 플래그를 준다. 1~3단계와 5~7단계는 이 문서 작성 중 시험 relay로 그대로 실행해 확인했다(4단계만 가짜 upstream과 `--test-upstream --allow-model-echo`로 바꾸고 6단계에 `--dry-run`을 더함. 표본은 R1-GO-06·R1-PY-04(Go와 Go가 아닌 사례 하나씩), tools-all은 R1-GO-06·R1-JS-02·R1-PY-01로 줄였다. W24 2c 병합 뒤 아래 블록 그대로 다시 실행했다. 두 계획 모두 거절 없음, batch 6개 모두 끝남, 두 review batch 모두 변형 4개에서 코드 노출 4/4, 라벨 결함 파일의 investigate 입력 2/2).

```bash
# 노드에 맞게 도구 경로를 정한다. 임시 PostgreSQL을 쓰므로 PostgreSQL 도구(initdb, pg_ctl, psql)가 PATH에 있어야 한다.
TOOLS=(--go "$GO" --tool staticcheck="$STATICCHECK" --tool cargo="$CARGO" --tool clippy="$CLIPPY"
       --tool cppcheck="$CPPCHECK" --tool clang-tidy="$CLANG_TIDY"
       --tool ruff="$RUFF" --tool mypy="$MYPY" --tool bandit="$BANDIT" --tool pyright="$PYRIGHT_JS"
       --agent-flag python-both-type-checkers
       --tool lizard="$LIZARD" --tool jscpd="$JSCPD_JS" --tool gitleaks="$GITLEAKS"
       --tool node="$NODE" --tool eslint="$ESLINT_JS" --tool tsc="$TSC_JS")
REVIEW=(--opencode "$OPENCODE" --model codeye-gateway/gpt-6.2-sol)
SEL=(--sample 8 --seed 24)   # 유료 표본(다섯 언어, E안). 다른 안은 아래 표

# 1. tools(모델 없음): 전체와 비교용 표본
python3 scripts/r1_eval.py run --condition tools --results-root "$R" --batch tools-all "${TOOLS[@]}" --max-workers 3
python3 scripts/r1_eval.py run --condition tools --results-root "$R" --batch tools-sel "${SEL[@]}" "${TOOLS[@]}"

# 2. envelope 수집 dry run(가짜 upstream, 시험 장부, 키 없음). 이 batch가 계획 이력도 된다.
python3 scripts/r1_eval.py capture-list --output "$R/capture-list.json"
mkdir -p "$R/cap"
env -u OPENAI_API_KEY python3 scripts/r1_fake_upstream.py --ready-file "$R/cap/fake.json" --dump "$R/cap/dump" &
FAKE_PID=$!
until [ -s "$R/cap/fake.json" ]; do sleep 0.2; done
PORT=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["port"])' "$R/cap/fake.json")
env -u OPENAI_API_KEY python3 scripts/r0_openai_gateway.py --ready-file "$R/cap/relay.json" \
  --ledger "$R/cap/ledger.jsonl" --model gpt-6.2-sol --forbidden-phrases "$R/capture-list.json" \
  --test-upstream "http://127.0.0.1:$PORT" &
CAP_RELAY_PID=$!
until [ -s "$R/cap/relay.json" ]; do sleep 0.2; done
for PACK in review-r1 review-v1; do
  python3 scripts/r1_eval.py run --condition review --pack $PACK --dry-run --results-root "$R" \
    --batch capture-$PACK "${SEL[@]}" --relay-file "$R/cap/relay.json" --ledger "$R/cap/ledger.jsonl" \
    --forbidden-phrases "$R/capture-list.json" "${TOOLS[@]}" "${REVIEW[@]}"
done
kill $CAP_RELAY_PID $FAKE_PID

# 3. 정답 목록. 출력의 dropped.in_allowed_text에서 허용 텍스트가 뺀 라벨 표현을 확인한다.
python3 scripts/r1_eval.py allow-text --tools-batch "$R/tools-all" --request-dump "$R/cap/dump" --output "$R/allow.txt"
python3 -I fixtures/r1-quality/corpus.py forbidden --allow-text prompts/review-v1 --allow-text prompts/review-r1 \
  --allow-text "$R/allow.txt" --output "$R/forbidden.json"

# 4. 실제 relay(coordinator 셸, 키는 이 프로세스만 읽는다). --allow-model-echo는 선택이다(§3.4).
READY="$R/relay-$(date +%s).json"
python3 scripts/r0_openai_gateway.py --ready-file "$READY" --ledger "$LEDGER" \
  --model gpt-6.2-sol --forbidden-phrases "$R/forbidden.json" [--allow-model-echo] &
until [ -s "$READY" ]; do sleep 0.2; done
RELAY=(--relay-file "$READY" --ledger "$LEDGER" --forbidden-phrases "$R/forbidden.json")

# 5. 계획과 장부 확인(거절이면 종료 코드 1, 출력의 refusal에 사유)
python3 scripts/r1_eval.py plan --pack review-r1 "${SEL[@]}" --history "$R/capture-review-r1" --ledger "$LEDGER"

# 6. 유료 batch: review-r1을 먼저 실행한다(요청 수 상한 때문, 아래 표).
python3 scripts/r1_eval.py run --condition review --pack review-r1 --results-root "$R" --batch paid-r1 \
  "${SEL[@]}" --history "$R/capture-review-r1" "${RELAY[@]}" "${TOOLS[@]}" "${REVIEW[@]}"
# r1이 끝난 뒤의 장부로 v1을 다시 계획한다. 거절이면 v1을 실행하지 않는다.
python3 scripts/r1_eval.py plan --pack review-v1 "${SEL[@]}" --history "$R/capture-review-v1" --ledger "$LEDGER" &&
python3 scripts/r1_eval.py run --condition review --pack review-v1 --results-root "$R" --batch paid-v1 \
  "${SEL[@]}" --history "$R/capture-review-v1" "${RELAY[@]}" "${TOOLS[@]}" "${REVIEW[@]}"

# 7. 판정과 채점: 각 batch의 judgments-template.json을 채운 뒤
python3 scripts/r1_eval.py score "$R/tools-sel" "$R/paid-r1" "$R/paid-v1" \
  --judgments J1.json [--judgments J2.json] --output "$R/score.json"
python3 scripts/r1_eval.py reader "$R/paid-r1" --sample 6 --seed 24 --output "$R/reading-r1"
```

추정은 W24 2c 병합 뒤 corpus 전체 capture(§10.2 항목 7)를 이력으로 계산했다. 이력의 역할별 호출 수는 제품의 입력 구조(gap 모듈, 문서, 도구 관찰)가 정한다. 가짜는 형식 복구를 부르지 않으므로 기대값에 복구 호출이 없고, 상한에는 복구 10%와 여유 1.5배가 붙는다. 호출당 비용은 요청 바이트 ÷ 3 토큰과 출력 600 토큰을 relay 단가로 셈한 보수값이다(기대 기준 호출당 약 $0.038~0.041). W15의 실측 호출당 차감($0.022~0.025)보다 크다. 남은 장부는 docs/37의 W15 기록($8.492, 요청 137건)을 가정했다. 실제 값은 5단계의 `plan --ledger`로 확인한다. 실제 R1 장부의 요청 상한은 1000건이었다. 그래서 이번 평가는 요청 수가 아니라 금액으로 묶였다(§13.4).

| 안 | 표본(`SEL`) | batch | 기대 호출 | 기대 차감 | 상한 호출 | 상한 차감 | 시작 전 남은 값 | 판정 |
|---|---|---|---|---|---|---|---|---|
| E(기본) | `--sample 8 --seed 24`: 사례 8개(다섯 언어), 변형 16 | review-r1 | 45.0 | $1.70 | 88 | $4.80 | $8.49, 137건 | 시작 |
| E | 같음 | review-v1 | 48.0 | $1.98 | 53 | $3.27 | $6.79, 92건 | 시작 |
| E 다음 분산 | `--cases R1-GO-05,R1-PY-07 --variants before --repeat 3`(E 안의 사례) | review-r1 → v1 | 15.0 / 18.0 | $0.58 / $0.74 | 33 / 20 | $1.80 / $1.22 | $4.81, 44건 → $4.23, 29건 | 둘 다 시작. v1 여유 4건 |
| D | `--sample 10 --seed 24`: 사례 10개(언어마다 2개), 변형 20 | review-r1 → v1 | 59.0 / 60.0 | $2.23 / $2.47 | 110 / 66 | $6.01 / $4.08 | $8.49, 137건 → $6.27, 78건 | 둘 다 시작. v1 여유 7건 |
| A | `--languages go`: 사례 11개, 변형 20 | review-r1 → v1 | 51.0 / 60.0 | $1.97 / $2.47 | 110 / 66 | $6.01 / $4.08 | $8.49, 137건 → $6.53, 86건 | 둘 다 시작 |
| E 역순 | E를 v1 → r1 | review-v1 → r1 | 48.0 / 45.0 | $1.98 / $1.70 | 53 / 88 | $3.27 / $4.80 | r1 시작 전 $6.51, 89건 | r1 거절(88건 > 89 − 5) |

E의 사례는 R1-CPP-03·CPP-08·GO-05·GO-07·JS-02·JS-03·PY-07·RS-07이고, D는 여기에 R1-PY-04·RS-08을 더한다(seed로 정해진다). 요청 수 상한(장부 전체 200건)이 금액보다 먼저 묶이고, r1의 상한이 크므로 r1을 먼저 실행한다. E는 다섯 언어를 모두 포함하고 두 batch 뒤 기대 기준 약 44건이 남아 두 사례의 반복 분산을 볼 수 있다. 분산 사례는 E 안에서 골라 2단계 capture가 이력을 갖게 했다. v1 분산의 여유는 4건뿐이므로 r1 분산 뒤에 다시 계획한다. D는 언어마다 사례 2개지만 r1 뒤 v1의 여유가 7건뿐이어서 r1에 형식 복구가 조금만 늘어도 v1이 거절된다. A는 Go만 깊게 본다(2c 전의 기본안). 6단계는 r1이 끝난 뒤 v1을 다시 계획하고 거절이면 v1을 실행하지 않는다. 5단계의 `plan --ledger`에서 r1 시작 전 남은 요청이 137건보다 적으면 `남은 요청 − r1 기대 호출 − 5 ≥ v1 상한 호출`이 되는 안을 고른다. 2단계의 capture는 같은 `SEL`로 돌리므로 요청이 E 93건, D 119건, A 111건이고 시험 장부 상한(200건) 안이다.

## 12. 제한

- **2c 전 결과는 Go가 아닌 사례의 모델 검토를 측정하지 않았다.** 2c 전에는 검토용 code 발췌가 Go만 지원해 review-r1의 Go가 아닌 사례 investigate가 `review_input_unavailable`(`code_excerpts_go_only`)이었고, review-v1의 Python·Rust `performed`는 README만 본 것이었다(§10.2 항목 5). 2c 뒤에는 corpus 전체에서 두 팩 모두 코드가 모델에 간다(항목 7). 코드 노출은 `llm_exposed_files`가 아니라 기록 기반 `model_saw_code`·`labelled_file_in_investigate`로 본다.
- **호출자 확장은 Go만 한다.** 모든 변형에 `caller_expansion_go_only`가 남는다. Go가 아닌 사례의 파일 간 결함은 발췌에 들어간 범위에서만 보인다. corpus에서는 라벨 결함 파일이 모두 investigate 입력에 있었지만, 큰 저장소에서도 그렇다는 근거는 아니다.
- **Rust 분석기는 corpus에서 돌지 않는다.** W21 병합으로 Rust 보고서는 조회된다. 그러나 corpus의 Rust 사례에는 Cargo.lock이 없어 `--offline --locked` 분석(cargo check·Clippy)이 `lockfile_missing`으로 끝난다. Rust 분석기 결과는 corpus에 lockfile을 더하기 전까지 측정되지 않는다.
- **C/C++ 팩은 corpus에서 깊이 돌지 않는다.** C/C++ 사례에는 compile DB가 없어 clang-tidy가 실행되지 않고 cppcheck는 include 누락 구성 오류로 끝난다.
- **구조·비밀값 팩(W21)은 corpus에서 finding을 내지 않았다.** corpus에는 비밀값·중복·복잡도 라벨이 없으므로 이 팩의 recall은 측정되지 않는다. corpus 실행에서 비밀값 선별이 실패한 적은 없다. `secret_screen_failed` 처리는 단위 시험과 실패하는 대역(§10.2 항목 6)으로만 확인했다.
- **모델 표현 차단의 크기를 모른다.** 엄격 방식이 맞힌 사례를 얼마나 무효로 만드는지는 실모델 batch에서만 보인다. 가짜로는 경로만 확인했다. 예외 방식의 범위는 relay 프로세스 전체이고 사례 단위 확인은 runner가 한다. runner를 거치지 않고 relay를 쓰면 이 확인이 없다.
- **dry run 수치에는 품질 의미가 없다.** 가짜 upstream은 정답을 모르고 입력 구조로 답한다. 위치 대조·검토 수·차감은 경로 동작의 근거일 뿐이다.
- **정답 목록은 표현 일치만 본다.** 다른 말로 바꾼 정답, 다른 언어로 쓴 설명, 코드 모양의 힌트는 막지 않는다. 라벨 문장의 5-gram과 고유 토큰만 대상이다.
- **판정은 사람이 한다.** 위치 자동 대조는 pre-fill이다. 판정 파일이 없으면 recall·FP·precision은 대부분 미판정으로 남는다. 판정자 간 일치도는 판정이 둘 이상일 때 충돌 목록으로만 보인다.
- **보고서 읽기는 측정하지 않았다.** 과제와 채점기만 있다. 모델로 독자를 흉내 내지 않는다.
- **비용 추정은 보수적 근사다.** 바이트당 3토큰과 여유 계수 1.5는 실측에 맞춘 값이 아니다. 실제 batch의 usage를 이력으로 주면 그 값을 쓴다.
- **예외 ID 확인은 순서를 보지 않는다.** runner는 예외 ID가 같은 사례의 모델 출력에 있는지만 확인하고, 그 출력이 요청보다 먼저였는지는 보지 않는다. corpus 입력에는 구성상 목록 항목이 없으므로 실제 차이는 없다고 보지만 확인하지 않았다.
- **holdout 표시는 같은 결과 루트 안에서만 동작한다.** 다른 `--results-root`에서 실행한 batch는 등록부가 달라 보이지 않는다.
- review-r1 rc2 등 팩을 바꾸면 envelope와 지침 허용 텍스트가 바뀐다. 목록을 다시 만들고(§3.1.1), 같은 팩 id의 이전 실제 실행이 있으면 새 batch는 `holdout_seen_by_prior_revision`으로 표시된다.
- 실행 환경은 WSL2 한 대다. Windows·RHEL 실측, 여러 노드 분할 실행은 하지 않았다.

## 13. 결과 — R1 보류 평가 1회차(2026년 11월 19일)

> **지침 작성자는 이 절을 읽지 않는다.** 이 절은 보류 사례 ID와 사례별 결과를 담는다. 다음 review-r1 개정을 쓰는 작성자가 읽으면 그 개정의 보류 평가는 독립 비교가 아니다([§4](#4-holdout-등록부)).

같은 WSL2 호스트(x86-64, Linux 6.18)에서 실행했다. 실제 PostgreSQL 컨트롤러·에이전트·제품 워커, OpenCode 1.19.5, 승인 relay를 거친 `gpt-6.2-sol`을 썼다. 도구는 §11 블록의 고정 버전 전부다(Python 네 도구, W21 세 도구, ESLint·tsc, staticcheck, cppcheck·clang-tidy, cargo·Clippy). corpus digest는 `ab776f5f…`, 정답 목록 sha256은 `5b75c199…`(7,722항목), relay는 `--allow-model-echo`로 실행했다. 판정은 팩을 가린 독립 개발 에이전트(Opus)가 했다. **사람 판정이 아니다.**

### 13.1 review-r1 rc2의 R0 회귀

docs/37 §11의 rc1 결과를 기준으로, 실행 전에 회귀 조건 넷을 적어 두었다. 대상은 R0 쌍 Q01·Q03·Q05·Q08·Q10, W15 입력, `w12-close`이고 실행은 `test_postgres.py --r0-pilot`(go vet만)이다. review-v1은 다시 돌리지 않았다. 지침과 Go 요청 바이트가 고정돼 있으므로(`TestReviewV1IsPinned`, `TestW24GoReviewInputsUnchanged`) W15의 v1 결과가 그대로 기준이다.

| 조건 | rc2 | rc1(W15) |
|---|---|---|
| 1. 차단 조건(꾸민 근거, 실행 없는 재현, 통계 오염) | 없음 | 없음 |
| 2. 수정 전 결함 포착(Q01·Q03·Q05·Q10) | 4/4 | 4/4 |
| Q08 수정 전(rc2가 첫 유효 측정) | 포착 | 무효 |
| 3. 수정 후 같은 결함 재지적 | 0/5 | 0/5 |
| 4. W15·`w12-close`의 새 조치 대상 오탐 | **해당**: W15 1회차에 low 1건 | 0(1회) |

조건 4의 low 1건은 상위 호출자가 결제를 재시도하면 중복 청구될 수 있다는 지적이다. 조건이 모두 `assumed`이고 `Forward`와 `Charge`를 잇는 호출이 없다. W15를 두 번 더 실행했더니 한 번은 finding이 없었고, 한 번은 조치 대상이 아닌 대기 후보 1건이었다. 관찰 빈도는 3회 중 1회다. 미리 정한 규칙에 따라 이것은 rc1 대비 회귀 신호로 기록한다(rc1은 1회만 측정). 조건 1~3은 유지됐다. 호출 37건, 장부 차감 $0.847.

### 13.2 보류 비교: review-r1 rc2 대 review-v1

표본은 D안(`--sample 10 --seed 24`, 다섯 언어 각 2사례)이다. 실행 순서는 E안 8사례를 먼저 두 팩으로 돌리고, 그다음 R1-PY-04·R1-RS-08을 두 팩으로 더했다. 모두 결함 쌍이라 tricky normal은 없다. 두 팩의 holdout 표시는 없다(`independent comparison: True`). 수치는 판정된 단위의 비율과 Wilson 95% 구간이다.

| 지표 | review-r1 rc2 | review-v1 | tools만 |
|---|---|---|---|
| 알려진 결함 recall(수정 전 10) | 10/10 [0.72, 1.00] | 9/10 [0.60, 0.98] | 0/10 [0.00, 0.28] |
| 수정 후 같은 결함 재지적 | 0/10 [0.00, 0.28] | 0/10 [0.00, 0.28] | 0/10 |
| 수정 후 변형의 조치 대상 오탐 | 2/10 [0.06, 0.51] | 4/10 [0.17, 0.69] | 0/10 |
| 수정 후 변형의 오탐(모든 finding) | 3/10 [0.11, 0.60] | 5/10 [0.24, 0.76] | 1/10 [0.02, 0.40] |
| 조치 대상 precision(`valid_actionable`/조치 대상) | 10/27 [0.22, 0.56] | 10/28 [0.21, 0.54] | — |
| 근거 없는 주장 | 0/42 | 0/37 | 0/5 |
| 실행 없는 `reproduced` | 0 | 0 | 0 |
| 근거 유효성(보고서 검사) | 20/20 | 20/20 | 20/20 |
| 모델 검토 `performed` | 19/20 | 20/20 | — |
| 코드가 모델에 감 / 라벨 결함 파일이 investigate 입력에 있음 | 20/20 / 10/10 | 20/20 / 10/10 | — |
| 호출 / 장부 차감 | 60 / $1.659 | 59 / $1.772 | 0 / $0 |

- **차이가 난 변형은 셋이다.**
  - R1-PY-04 수정 전: rc2만 라벨 결함을 냈다.
  - R1-JS-02 수정 후, R1-PY-07 수정 후: v1만 조치 대상 오탐을 냈다.
  - 나머지 변형에서는 두 팩의 라벨 포착과 오탐이 같았다.
- **rc2의 미완료 1건(R1-JS-02 수정 전).** verify 출력이 검증기에서 격리됐고(`verification_quarantined`), 한 번의 재시도도 `review_output_incomplete`였다. 그래서 보고서는 `ai_review_partial`이다. 검증되지 않은 내용은 게시되지 않았다. 라벨 결함은 같은 변형의 다른 finding으로 포착됐다.
- **두 팩 모두 precision이 낮다.** 제품이 조치 대상으로 센 finding(rc2 27건, v1 28건) 중 `valid_informational`이 rc2 14건, v1 13건이다. 사실이지만 조치는 필요 없는 지적으로, 계약이 정하지 않은 입력이나 호출자가 인자를 바꾸는 경우 등이다. `false_positive`는 3건, 5건이다. 팩 차이보다 제품의 조치 대상 기준에서 나온 결과다.
- **tools만 조건의 recall이 0이다.** 엄격한 판정 규칙(원인과 조건을 모두 말해야 일치) 때문이다. ESLint `no-unsafe-finally`는 R1-JS-02의 결함 줄을 정확히 짚었지만 trigger·영향을 말하지 않아 `valid_actionable`, 라벨 불일치로 판정됐다. 위치 자동 대조로는 수정 전 2/10이다. C/C++(compile DB 없음)와 Rust(Cargo.lock 없음) 분석기는 corpus에서 깊이 돌지 않았다(§12).
- **결론.** 이 표본(n=10)에서는 두 팩의 차이를 통계적으로 구분할 수 없다. 모든 구간이 겹친다. rc2는 recall이 같거나 높고, 수정 후 오탐이 적고, 호출당 비용이 약간 낮게 관찰됐다. 대신 미완료가 1건 있었다. 정확도 향상으로 발표할 근거는 아니다.

### 13.3 반복 실행 분산

R1-GO-05·R1-PY-07 수정 전 변형을 팩마다 세 번 실행했다. 두 팩 모두 세 번 모두 같은 finding 하나를 냈고(fingerprint Jaccard 1.0, 조치 대상 수·검토 결과 일치 1.0), 모두 라벨과 일치했다(6/6). 두 사례에서는 모델 변동이 결과를 바꾸지 않았다. 다른 사례, 특히 finding이 여럿인 사례의 변동은 측정하지 않았다. R0의 W15 입력(§13.1)에서는 같은 팩의 세 번이 서로 달랐다.

### 13.4 정답 차단과 비용

- **정답 차단.**
  - relay가 막은 요청은 0건이다.
  - 모델 표현 예외는 1건이다(review-v1 R1-CPP-03 수정 전 verify). 같은 변형의 investigate 출력에 있던 표현이었고 runner 확인을 통과했다.
  - 허용 텍스트 때문에 목록에서 빠진 라벨 표현은 13개다. 지침 언어 안내의 일반 용어(예: reference cycle)와 도구 진단 이름(예: copylocks)이다. 이 사례들은 일반 지침이나 도구 관찰의 도움을 받을 수 있다.
- **비용(보수적 장부 차감).**

  | 항목 | 호출 | 차감 |
  |---|---|---|
  | R0 회귀 | 37 | $0.847 |
  | 보류 평가(시험 실행·본 batch·분산·추가) | 154 | $4.344 |
  | W24 합계 | 191 | $5.191 |

  - R1 장부 전체는 254건, $6.689이고, $3.301이 남았다. 모든 정산은 `ok`, 실패와 halt는 0건이다.
  - 표준 단가 추정은 장부 전체 $3.285다. 공급자 청구서는 확인하지 않았다.
  - §11 표의 요청 수 가정(장부 상한 200건, 남은 137건)은 실제 장부와 달랐다. 실제 상한은 1000건이었고, 이번 평가는 금액 기준으로 묶였다.

### 13.5 승격 판단

review-r1은 `candidate-not-promoted`로 둔다. 기본 팩 변경은 소유자가 정한다(docs/37 §11). 판단 근거는 다음과 같다.

- **유리:**
  - 보류 비교에서 recall이 같거나 높았다(10/10 대 9/10).
  - 수정 후 오탐이 적었다(2/10 대 4/10).
  - 비용이 약간 낮았다.
  - 근거 없는 주장과 실행 없는 재현은 두 팩 모두 0이다.
- **불리:**
  - 표본이 작아 모든 차이가 구간 안에 있다.
  - R0 회귀 조건 4가 해당했다(W15 입력, 3회 중 1회 low 오탐).
  - 미완료 검토가 1건 있었다.
  - tricky normal 사례를 표본에 넣지 않아 정상 코드의 오탐률은 측정하지 않았다.
- **측정하지 않은 것:**
  - 사람 판정과 판정자 간 일치도;
  - 보고서 읽기 평가(독자 답 없음);
  - tricky normal;
  - C/C++·Rust 분석기 깊이;
  - W21 지표의 유용성.

결과 디렉터리, 판정 파일, 팩 가림 대응표는 저장소 밖 비공개 경로에 보관했다. 재현에는 §11 절차와 `--sample 10 --seed 24`를 쓴다. 이 batch 뒤에 같은 팩 id의 다른 digest로 실행하면 `holdout_seen_by_prior_revision`으로 표시된다. 다음 보류 비교는 새 사례나 tricky normal을 포함해야 독립 비교가 된다.

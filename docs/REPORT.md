# 보고서와 화면

CODEYE 보고서가 지금 담는 내용, 발견 항목을 읽는 방법, 웹 화면과 표시 규칙, run 비교와 사람 판정, 내보내기와 접근성 요구를 다룬다. HTTP 경로·헤더·상태 코드와 진행 필드의 계산 규칙은 [API](API.md), 검토 역할과 지침 팩은 [검토](REVIEW.md), 분석기별 산출물과 규칙 문맥은 [분석기](ANALYZERS.md), 게시 전 실행·완료 절차는 [실행](EXECUTION.md)을 따른다.

## 보고서 문서

보고서는 run의 리비전마다 저장하는 구조화 JSON 한 벌이다. 형식의 기준은 [보고서 스키마](../schemas/report.schema.json)와 [발견 스키마](../schemas/finding.schema.json)이고 생성 코드는 [`internal/report`](../internal/report/build.go)다.

| 항목 | 현재 값과 규칙 |
|---|---|
| `schema_version` | `0.1`. 새 필드는 모두 선택 필드로 추가했다. |
| `demo` | 제품이 게시한 보고서는 항상 `false`다. [`examples/report.json`](../examples/report.json)은 현재 builder가 실제 worker 파이프라인과 대본 모델 응답으로 만든 합성 예시(`demo: true`)이고, [`examples/report-go-vet.json`](../examples/report-go-vet.json)은 builder 1 시절의 실제 코드 미포함 내보내기다(분석 종류별 절 없음). |
| `builder.id` / `builder.version` | `codeye-report` / `8`. 버전 3부터 `fp2` 발견 식별을, 버전 4부터 도구 행의 `rule_context`와 관찰의 `suppressed`를, 버전 5부터 [분석 종류별 절](#분석-종류별-절)을, 버전 6부터 [모듈 단위 검토 분할](#모듈별-검토-장부)의 기록·발견·장부와 `review_calls[].task_key`를, 버전 7부터 환경 절의 [의존성 목록](#의존성-목록)과 의존성 공백을, 버전 8부터 [검토 우선순위](REVIEW.md#검토-우선순위)로 채운 범위 산출물(schema 0.2)의 분류(장부 행의 `class`와 부차 파일 수, 상한에 막힌 주요 slot 수)를 기록한다. |
| `builder.input_digest` | builder 입력(run·snapshot·attempt 사실, task 행, 결과 manifest, 산출물 행, 게시 시점 판정 집합, builder ID·버전·스키마 버전)의 SHA-256. 보고서 ID·리비전·생성 시각은 넣지 않는다. |
| `export.include_code` | 저장본은 `true`다. 코드 미포함 내보내기는 같은 저장본의 projection이며 `false`다. |

보고서 builder는 I/O를 하지 않고 모델을 부르지 않는다. 게시 서비스([`internal/reportsvc`](../internal/reportsvc/publisher.go))가 DB 행과 산출물 바이트를 읽어 크기·SHA-256을 다시 확인한 뒤 넘긴다. 저장할 때와 읽을 때 모두 같은 검증(`ValidateReport`)을 거친다. 저장 본문이 검증에 실패하면 조회는 500 `REPORT_INVALID`로 끝나며 깨진 본문을 내보내지 않는다.

### 게시와 리비전

1. 노드가 스냅샷을 고정하고 원시 stdout/stderr, 관찰 문서, 결과 manifest를 올린다. 모든 task가 실패해도 실행한 task가 있으면 결과 manifest 업로드를 시도한다.
2. 관리 서버의 게시 루프(기본 2초 간격, attempt 완료를 접수하면 즉시 한 번)가 release와 정리 확인(`cleanup_state=confirmed`)을 마친 현재 attempt 중 아직 보고서도 거부 기록도 없는 것을 고른다. 대상 run 상태는 `finalizing`, 완료 접수 뒤 취소 처리 중인 run, `failed`, `cancelled`다.
3. task 성공 여부는 PostgreSQL task 행에서 읽는다. 결과 manifest의 run·attempt·epoch·스냅샷·task 식별자와 산출물 참조를 저장된 행과 대조한다. manifest가 DB가 성공으로 기록하지 않은 task를 성공이라고 주장하면(`task_status_upgrade`) manifest를 거부한다. 거부된 manifest는 보고서를 막지 않는다. 보고서는 DB task 행만으로 만들고 거부 사유를 한계에 남긴다.
4. 같은 트랜잭션에서 현재 epoch·release·정리·manifest·run 상태를 다시 확인하고 보고서 본문, run 종결 상태, `report.published` 이벤트를 함께 저장한다.
5. 같은 run에서 같은 `input_digest`의 보고서가 이미 있으면 그 리비전을 그대로 쓴다. 리비전 번호는 run마다 1부터 빈틈없이 오른다. 동시 게시는 유일 제약으로 하나만 성공하고 나머지는 다음 패스에서 다시 읽는다.
6. 스냅샷을 고정한 뒤 실패·취소된 실행도 확인할 수 있는 사실을 담아 해당 상태의 보고서를 게시한다. 스냅샷 고정 전에 끝난 실행은 보고서 없이 run 상태와 사유로만 추적한다.
7. 저장 오류로 멈추면 다음 패스에서 다시 시도한다. 검증할 수 없는 입력(소스 manifest 없음·무결성 실패, task key 없음, builder 입력 거부)은 attempt마다 한 번 거부로 기록하며, `finalizing`이던 run은 `failed`가 된다.

**builder 버전을 올리면.** 버전은 `input_digest`에 들어가므로 같은 사실이라도 새 builder의 입력은 다른 digest가 된다. 게시 루프는 이미 보고서가 있는 attempt를 다시 빌드하지 않는다. 따라서 게시된 리비전은 바뀌지 않고, 새 builder는 그 뒤에 게시되는 보고서에만 적용된다. 재시도로 새 attempt가 생기면 그 attempt의 보고서가 다음 리비전이 된다. 비교는 리비전마다 저장된 `builder.version`을 읽어 처리 방식을 고른다([발견 식별](#발견-식별-fp2)).

### 세 형식

JSON·Markdown·자체 포함 HTML은 같은 리비전의 같은 projection에서 만든다. Markdown과 HTML은 요청 시점에 저장본에서 렌더링하며 run에서 다시 빌드하지 않는다. 두 형식은 기본이 요약 보기이고 `detail=full`이면 모든 행을 싣는다([내보내기 상세도](#내보내기-상세도)). 모든 형식이 같은 `summary`와 같은 projection JSON digest를 보여준다. 경로·헤더는 [API](API.md)에 있다. PDF는 후속 선택 항목이다.

## 보고서 구성

| 필드 | 내용 |
|---|---|
| `id`, `run_id`, `revision`, `created_at` | 리비전 식별과 게시 시각 |
| `status`, `quality_verdict` | [상태와 판정](#상태와-판정) |
| `inputs` | `snapshot_id`, `source_kind`(git·local), 고정 방식 설명, `commit_sha`(Git만), `manifest_sha256`, `profile` |
| `attempt` | 보고서가 설명하는 attempt의 ID·번호·lease epoch·완료 시 run 상태·정리 상태·사유 |
| `summary` | [집계](#집계) |
| `coverage` | [분석 범위와 한계](#분석-범위와-한계) |
| `tasks` | DB task 행: key, 상태, `required_for_verdict`, 사유 코드 |
| `executions` | task 안의 프로세스 실행: argv, 작업공간 상대 cwd, 환경변수 이름, 종료 코드, 정리, stdout/stderr 산출물과 잘림 여부, 시각 |
| `artifacts` | attempt 산출물의 ID·task·role·media type·SHA-256·크기·수락한 decoder. 원시 바이트는 넣지 않는다. |
| `observations` | 원시 도구 진단. 뒤 단계가 고쳐 쓰지 않는다. |
| `findings` | 발견 항목과 후보. [발견 항목 읽기](#발견-항목-읽기) |
| `limitations` | 한계 코드 목록(`code` 또는 `code:detail`) |
| `overview`, `environment`, `documents`, `static_analysis`, `metrics`, `secrets`, `code_review`, `gaps` | [분석 종류별 절](#분석-종류별-절)(builder 5부터) |

실행 기록은 절대 경로를 싣지 않는다. 검증이 cwd는 작업공간 상대 경로(또는 `[REDACTED]`)인지, argv에 절대 경로가 없는지 확인한다.

관찰은 원시 산출물에 묶인다. `raw_ref`는 그 task 실행의 원시 stream 산출물 ID, SHA-256, stream 순번, JSON pointer, 바이트 범위를 가리킨다. 위치는 도구가 확정한 줄 범위를 쓰고, 경로와 blob hash가 snapshot manifest와 일치할 때만 `evidence_state`가 `verified_against_source_manifest`다. 일치하지 않으면 `source_manifest_mismatch`, 확인할 수 없으면 `unresolved`이며 그 위치는 발견에 연결하지 않는다. `location_state`는 분석기 decoder가 준 값을 그대로 싣는다([분석기](ANALYZERS.md)).

## 분석 종류별 절

builder 5부터 보고서는 위 필드에 더해 분석 종류마다 한 블록을 싣는다. 기존 필드(`observations`, `findings`, `coverage`, `summary`, `tasks`, `limitations`)의 모양과 값은 그대로이며 새 블록은 모두 선택 필드다. 블록은 관찰과 발견을 ID로만 가리키고 본문을 복사하지 않는다. builder 4 이하 리비전에는 블록이 없고 다시 빌드하지 않으며, 세 형식과 웹 화면은 그 리비전을 [이전 평면 배치](#markdown과-html-렌더링)로 보여준다.

| 순서 | 절 | 블록 | 담는 것 |
|---|---|---|---|
| 1 | 종합 | `overview` | 절마다 단계 상태와 수(`sections[]`: `key`, `state`, `counts`), 먼저 볼 항목(`attention[]`: `section`, `code`, `count`) |
| 2 | 환경 분석 | `environment` | 탐색 결과 수락 여부(`discovery`), 모듈·manifest, 모듈이 선언한 도구 요구, 노드가 관측한 도구·분석기 버전, 요구별 판정, 의존성 받기 결과(`dependencies`, builder 7부터, 의존성 task가 없으면 생략), 모델 해석(`model`, `notes`)과 그 근거 목록 |
| 3 | 문서 분석 | `documents` | 문서 목록과 읽음·예산 초과·제외 상태, 읽기 예산, 모듈별 적용 문서, 겹치는 안내, 모델 해석(계약·충돌·질문·지침 메모)과 근거 목록 |
| 4 | 정적 분석 | `static_analysis` | 생태계(`languages[].ecosystem`)별, 그 안에서 분석기 task별 한 행: 목적, 작업 상태·사유, 결과 수락 여부, exit class, 규칙 팩, 범위·처리 파일 수, 규칙별 관찰 수, 관찰·발견 ID |
| 5 | 구조·중복 지표 | `metrics` | 지표 분석기 task와 지표별 묶음(이름·단위·임계값·최댓값·관찰 ID) |
| 6 | 비밀정보 | `secrets` | 비밀정보 분석기 task, 탐지마다 규칙·위치·근거 상태·관찰 ID·도구 발견 ID, 모델 입력에서 비밀값을 걸렀는지(`model_input`) |
| 7 | 코드 분석(모델 검토) | `code_review` | 지침 팩, 역할별 결과, 호출 수·수락 수·요청 바이트, 모델에 노출한 파일 수, 해석 한계, 모델 후보 발견 ID, slice가 계획된 실행이면 [모듈별 검토 장부](#모듈별-검토-장부)(`ledger`) |
| 8 | 분석 공백 | `gaps` | 분석하지 못했거나 일부만 분석한 범위(`section`, `code`, 선택 `subject`·`detail`). 비어 있으면 생략한다. |

"이전 실행과 비교"는 저장 문서의 절이 아니다. 비교는 요청마다 계산하므로([run 비교](#run-비교)) 웹 화면만 종합 아래에 그 결과를 따로 보여주고, 내보내기에는 넣지 않는다.

**분할.** 모든 관찰은 정적 분석·지표·비밀정보 중 정확히 한 절에 한 번 들어간다. 진단은 정적 분석 행, 지표 관찰은 지표 묶음, 비밀정보 분석기의 관찰은 탐지 목록이다. 모든 발견은 정적 분석(도구 발견)과 코드 분석(모델 후보) 중 정확히 한 곳에 들어간다. 비밀정보 탐지의 도구 발견은 정적 분석 쪽에 두고 탐지는 그 발견을 `finding_id`로 가리킨다. 두 절의 발견 집계를 같은 함수(`ComputeSummary`)로 계산해 더하면 `summary`와 같아야 한다. 정적 분석 행의 규칙별 수 합은 그 행의 관찰 수와 같고, 지표 묶음의 최댓값·임계값은 관찰에서 다시 계산한 값과 같아야 한다. 종합 블록은 다른 절에서 순수 함수로 다시 계산한 값과 같아야 한다. 검증(`ValidateReport`)이 저장 시와 조회 시에 이 규칙을 모두 확인한다.

**분석기 목적.** 관찰이 어느 절에 들어갈지는 분석기 envelope의 `analyzer.purpose`(`metrics`, `secrets`, 없으면 진단)로 정한다([분석기](ANALYZERS.md#분석기-목적)). 생태계도 수락한 envelope의 `analyzer.ecosystem`에서 읽는다(go vet 문서는 `go`). 수락한 결과가 없는 task 행은 생태계를 모르므로 생태계 없는 묶음에 둔다.

**단계 상태.** 절의 `stage`는 진행 화면과 같은 분류기(`progress.TaskStage`)로 task 행의 단계 열에서 계산한다. 값은 진행 단계 상태(`plan_pending`, `no_tasks`, `waiting`, `running`, `succeeded`, `skipped`, `failed`, `cancelled`, `partial`)와 같고, 단계별 상태 수(`counts`)·사유 코드(`reasons`)·task 수·성공 수를 함께 싣는다. 단계 열이 없는 task가 하나라도 있으면 `not_recorded`("세부 단계 미기록")다. 지표·비밀정보 절은 정적 분석 단계 중 그 목적의 task만 센다. 미실행·미지원·실패 task는 "실행하지 않음"이나 해당 상태로 보이고 0건으로 읽히지 않는다.

**모델 해석.** 환경·문서·코드 분석 절의 모델 문장은 단계 연결 검사를 통과해 수락된 역할 기록에서 한 번씩만 가져온다. 역할이 수행되지 않았으면(`model.state: not_performed`와 사유 코드) 모델 문장은 하나도 없다. 문장은 그 절의 근거 목록(경로·줄 범위·blob SHA-256)에 있는 근거 ID만 인용할 수 있다. 화면과 내보내기는 이 문장에 "모델 해석"과 역할 이름을 붙여 플랫폼 판정과 구분한다. 모델 해석은 상태·수·판정의 원천이 아니다.

**고정 문구.** 절의 제목·상태·수 이름·먼저 볼 항목 문장은 구조화 값에서 만든 템플릿이다. Go 렌더러는 [`messages_ko.go`](../internal/report/messages_ko.go), 웹은 [`report-ko.ts`](../web/src/locales/report-ko.ts)에 있으며 모르는 코드는 원문 그대로 보인다.

**코드 미포함.** projection은 모델 문장을 모두 생략 문구로 바꾸고, 모듈 요구의 제약 문자열은 버전 제약 형식일 때만 남긴다. 경로·줄 번호·코드·수·blob hash는 남는다. 비밀정보 탐지는 처음부터 값과 그 일부·hash를 싣지 않는다.

예시는 [`examples/report.json`](../examples/report.json)과 [`examples/report.md`](../examples/report.md)이다. `fixtures/modules/input`을 대상으로 실제 worker 파이프라인과 대본 모델 응답으로 만든 합성 예시(`demo: true`)이며, 생성 시험은 [`report_example_test.go`](../internal/worker/review/report_example_test.go)다.

### 모듈별 검토 장부

builder 6부터 보고서는 [모듈 단위 검토 분할](REVIEW.md#모듈-단위-검토-분할)의 산출물을 읽는다. slice 기록(review 기록 0.3)은 자기 slice task에 붙은 것만 받고, 검토 범위 산출물(`application/vnd.codeye.review-scope+json` 0.2, 이전 worker의 0.1도 읽는다)은 `review.scope`에 붙은 것만 받는다. 둘 다 attempt·snapshot manifest와 대조하며, slice 기록의 단계 연결은 전체 역할과 같은 규칙에 범위 산출물 digest를 더해 확인한다. slice verify는 자기 slice investigate의 후보만 이어받는다.

- **발견.** slice 후보는 `review_sNNN_<후보 ID>`(예: `review_s002_investigate_1`)로 코드 분석 절의 발견 분할에 들어간다. 판정은 같은 slice의 verify 기록에서만 온다. 계획되지 않은 slice 키의 발견은 검증에서 거부한다.
- **호출과 노출.** `review_calls[]`는 builder 6부터 호출이 든 검토 task(`task_key`, 전체 역할 또는 slice)를 싣고 최대 207개(호출 상한의 최댓값)다. `llm_exposed_files`는 같은 뜻(모델에 보낸 서로 다른 경로 수)이며 slice 기록도 센다. 예산 소진·정책 제외·미지원 task는 보낸 것이 없다. `ai_review`는 필수 task만 본다.
- **역할과 장부.** 코드 분석 절의 `roles`는 전체 역할만 싣는다. `review.scope`와 slice task는 장부에 있다.

`code_review.ledger`는 `review.scope`가 계획된 실행(overview 밖 파일이 있는 계획)에만 있다. slice가 없는 계획이나 builder 5 이하 리비전에는 없다.

| 필드 | 뜻 |
|---|---|
| `scope` | `review.scope` task와 결과. 범위 산출물을 받았으면 `accepted`, 아니면 작업 상태·사유(예: `skipped` / `model_source_not_approved`) |
| `max_calls`, `max_request_bytes` | 범위 산출물이 기록한 attempt 호출 상한과 요청 크기 상한. 산출물이 없으면 `null` |
| `calls_used` | slice 앞 호출 수(범위 산출물의 `calls_before_slices`, attempt 호출 기록에서 셈. 기록 없이 실패한 전체 역할 호출도 들어간다)와 slice 기록의 호출 합. 범위 산출물이 없으면 전체 역할 기록에서 센다. 호출했을 수 있는 검토 task에 쓸 수 있는 기록이 없으면 `null` |
| `request_bytes_sent` | 모든 검토 기록의 요청 크기 합(프롬프트 바이트). 같은 조건에서 `null`이며, 값이 있으면 `code_review.request_bytes`와 같다 |
| `status` | `complete`(모든 대상 파일을 보냈고 검증 역할까지 마쳤다), `overview_only`(성공한 slice investigate가 없다), 그 밖은 `partial` |
| `modules[]` | 범위 산출물의 모듈마다 한 행(모듈 ID 순) |

모듈 행은 `module_id`, `discovered`(모듈 탐색이 같은 ID를 찾았는지), `files_eligible`과 그 파일을 한 번씩 나눈 수 `files_sent_to_model`·`files_withheld`(비밀값 탐지)·`files_unreadable`·`files_budget_exhausted`(호출 상한으로 거절된 slot의 주요 파일)·`files_not_planned`(slot이 남지 않은 주요 파일)·`files_secondary_not_reached`(builder 8: 거절됐거나 slot이 남지 않은 부차 파일)·`files_not_sent`(그 밖: overview가 보내지 못했거나 slot이 실패·생략됨)를 싣는다. 일곱 수의 합은 `files_eligible`이다. builder 8은 schema 0.2 범위 산출물에서 `class`(`primary`·`secondary`, 모든 행에 함께 있다), `files_secondary`(부차 경로·생성 파일 수), `files_secondary_sent`(그중 보낸 파일)도 싣고 0인 수는 생략한다. 부차 모듈의 대상 파일은 모두 부차다. 장부의 `primary_slots_budget_exhausted`는 호출 상한으로 거절된 주요 파일 slot 수다(보통 slot마다 호출 2회). 주요 파일의 `files_budget_exhausted`가 있으면 반드시 있고, 분류가 없는 장부에는 없다. 파일은 수락된 검토 기록 하나라도 그 파일의 발췌를 보냈으면 보낸 것으로 센다. verify가 반대 근거로 보낸 시험 파일 발췌도 들어가므로, 상한으로 거절된 slice의 파일도 보낸 쪽에 들 수 있다. `bytes_sent`는 실제 요청의 발췌가 아니라 보낸 파일의 범위 산출물 계획 바이트(첫 50줄, 파일당 2,400바이트 이하)의 합이고, `files_truncated`는 발췌가 파일 끝까지 닿지 않은 보낸 파일, `files_with_completed_review`는 보낸 파일 중 검증 역할 기록까지 수락된 파일(overview 파일은 전체 verify, slice 파일은 그 slice의 verify)이다. `slices[]`는 그 모듈 파일을 가진 slice task마다 `task_key`·`status`·`reason`이며 task 행과 같아야 한다. 분류가 있는 장부의 표(세 형식과 웹)는 모듈 뒤에 분류 열, 미계획 뒤에 "부차 경로·예산 밖"(`files_secondary_not_reached`) 열을 더해 행의 합이 대상 수와 맞게 보인다. 분류가 없는 장부의 표는 그대로다.

**모델에 보낸 범위는 검증된 범위가 아니다.** 장부는 무엇을 모델에 보냈는지만 센다. 모델이 본 파일에도 후보로 지목되지 않은 결함이 있을 수 있고, 화면과 내보내기는 이 범위를 "모델에 보냄"으로만 부른다.

**종합과 공백.** 장부에 모듈 행이 있으면 종합의 코드 분석 줄에 `files_sent_to_model`과 `files_not_sent_to_model`(대상 파일 − 보낸 파일)이 더해지고, 보내지 못한 파일이 있으면 먼저 볼 항목 `review_scope_not_sent`가 생긴다. builder 8에서 부차 파일이 있으면 `primary_modules`·`primary_files`·`primary_files_sent_to_model`·`secondary_files`·`secondary_files_not_reached`가 더해지고, 먼저 볼 항목은 보내지 못한 주요 파일만 세는 `review_primary_not_sent`다. 분석 공백에는 모듈마다 `review_scope_budget_exhausted`·`review_scope_not_planned`·`review_scope_secondary_not_reached`·`review_scope_withheld`·`review_scope_unreadable`·`review_scope_not_sent`(대상 모듈 ID), 범위 산출물을 받지 못했으면 `review_scope_<결과>`(`review.scope`, 사유)가 들어간다.

**검증.** 장부는 builder 6 이상이고 `review.scope` task가 있을 때만 있어야 하며, 있으면 반드시 있어야 한다. 분류는 builder 8 이상에서 모든 행에 함께 있거나 함께 없고, 부차 수는 대상·보낸 파일을 넘지 않는다. 모듈 행의 합, 보낸 파일 이하인 잘림·검증 완료 수, 상한 초과 파일이 있으면 그런 slice가 있을 것, 상태가 행과 task 행에서 다시 계산한 값과 같을 것, `calls_used`가 `review_calls` 수 이상일 것, 호출의 `task_key`가 같은 역할의 계획된 task일 것을 저장 시와 조회 시에 확인한다. 장부는 모듈 ID·task 키·코드·수만 담으므로 코드 미포함 projection에서도 그대로다. 세 형식과 웹 화면은 장부의 같은 수를 보인다.

**비밀값 선별 상태.** builder 6부터 비밀정보 절의 `model_input`에 `screened`(완료된 탐지로 걸렀고 뺀 파일 없음)가 있다. builder 5는 이 경우를 `not_screened`로 읽었다. 비밀값 작업이 실행됐지만 결과를 쓸 수 없어(실패·부분 결과) 미선별로 검토한 실행도 `not_screened`다. 그 결과가 보고한 파일을 뺐어도(`secret_files_withheld`) `withheld`보다 `not_screened`가 앞선다. 나머지 입력은 거르지 않았기 때문이다. Markdown과 웹은 이때 쓸 수 없던 작업과 상태·사유를 적는다(예시 `internal/report/testdata/report-screen-unusable.json`). 이전 worker가 검토를 건너뛴 실행(`secret_screen_failed`)은 그대로 `screen_failed`로 읽는다.

예시는 [`examples/report-slices.json`](../examples/report-slices.json)과 [`examples/report-slices.md`](../examples/report-slices.md)이다. `fixtures/modules/slices/input`을 호출 상한 15로 실행해(작업마다 대본 응답 1회, 모델 호출 없음) 계획한 slot 9개 중 `review.scope`가 6개를 채우고, 그중 3개가 승인되고 3개가 `budget_exhausted`로, 빈 slot 3개가 `no_review_scope`로 끝난 합성 예시이며, builder 7이 쓴 같은 예시는 `internal/report/testdata/report-slices-builder7.json`으로 남아 계속 읽히는지 시험한다. 이 예시와 [`examples/report.md`](../examples/report.md)의 Markdown은 요약 보기이며, 생성 시험은 [`report_slices_example_test.go`](../internal/worker/review/report_slices_example_test.go)다.

### 의존성 목록

builder 7부터 환경 절은 `deps.*` task마다 한 행(`environment.dependencies[]`, key 순서)을 싣는다([의존성 준비](ARCHITECTURE.md#의존성-준비)). 의존성 task가 계획되지 않은 run에는 필드가 없고 보고서는 builder 6과 같은 모양이다.

- 행 필드: `task_key`, `task_status`, `task_reason`, 기록 수락 여부 `result`(`accepted`, `result_rejected`, `result_missing`), 그리고 수락한 기록의 `module`·`ecosystem`·`path`·`manager`·`manager_version`·`state`·`reason`·`pinned`·`lockfile`·`declared`·`prepared`·`failed`·`cache_kind`·`failed_items`·`limitations`. 기록을 수락하지 못했으면 기록 필드는 null이고 task 상태·사유만 보인다.
- 기록은 `DependenciesDecoder`가 attempt·snapshot·task와 대조한다. 기록의 `task_status`가 task 행과 다르면 거부한다. 관찰·발견·도구 coverage에는 더하지 않는다.
- 종합의 환경 절 수에 `dependencies`(의존성 task 수)와 `dependencies_prepared`(성공한 task 수)가 더해진다. 의존성 task가 없으면 두 수는 없다.
- 공백(`gaps`, 절 `environment`):
  - `dependencies_not_prepared`: 성공하지 않은 의존성 task. 받을 것이 없어 건너뛴 task는 넣지 않는다. `subject`는 task key, `detail`은 사유다.
  - `dependencies_not_fetched`: 분석기 task가 `dependency_unavailable`로 끝났는데 그 생태계의 의존성 task가 없다(의존성 받기가 꺼져 있었다). `subject`는 분석기 task key다.
  - `dependency_versions_not_pinned`: lockfile이 버전을 고정하지 않아 받는 도구가 버전을 골랐다.
- 판정 규칙은 바뀌지 않는다. 의존성 task는 필수가 아니므로 실패해도 그것만으로 보고서가 `partial`이 되지 않는다. 의존성이 없어 분석기가 실패하면 그 분석기 task가 판정에 반영된다.
- Markdown·HTML은 환경 절에 모듈·받는 도구·결과·받은 수·사유·한계 표를 보인다.


## 상태와 판정

상태와 판정은 구조화된 사실로만 정한다. 모델 서술은 원천이 아니다.

| `status` | 조건(위에서부터 처음 맞는 것) |
|---|---|
| `cancelled` | run이 취소로 끝났다. |
| `failed` | run이 실패했거나, 수락된 결과 manifest와 함께 성공하고 거부되지 않은 task가 하나도 없다. |
| `partial` | 필수 task 미성공(정적 분석이 모두 실패하고 탐색만 성공한 실행도 여기다), 성공한 필수 task의 산출물 누락, 산출물 거부, 정적 분석 공백(`static_analysis_gap`) 또는 미계획(`static_analysis_not_planned`), 정리 미확인, `with-tests`인데 테스트 결과 없음, 또는 AI 검토가 `performed`가 아니다. |
| `completed` | 위 어느 것도 아니다. |

| `quality_verdict` | 조건(처음 맞는 것) |
|---|---|
| `attention_required` | 조치 항목(`actionable_findings`)이 하나 이상이다. |
| `inconclusive` | 상태가 `completed`가 아니거나, AI 검토가 `performed`가 아니거나, 미확정 후보나 위치 미확인 진단이 남아 있다. |
| `no_actionable_findings` | 그 밖의 경우. 안전 인증이 아니며, 모든 형식이 이 문구를 함께 보여준다. |

`completed` 보고서는 필수 task가 모두 성공하고 AI 검토가 `performed`여야 한다. 검증이 이 규칙과 판정 규칙을 저장 시와 조회 시에 다시 확인한다. 검토가 수행되지 않은 보고서의 builder 문구에는 "검토 완료", "문제 없음" 같은 완료 주장을 넣지 못한다. 검토 기록이 없거나 필수 도구 결과가 누락되면 `no_actionable_findings`를 게시할 수 없다.

### AI 검토 상태

`coverage.ai_review.status`는 필수 검토 task가 실제로 수락됐는지를 나타낸다. task 행이 성공하고 등록된 decoder가 그 결과 바이트를 수락해야 수락으로 센다. task가 성공해도 결과를 수락하지 못하면 미수행이다.

| 상태 | 뜻 |
|---|---|
| `performed` | 필수 검토 task가 하나 이상이고 모두 수락됐다. |
| `partial` | 일부만 수락됐다(`reason: required_review_incomplete`). |
| `not_performed` | 필수 검토 task가 계획되지 않았거나(`review_tasks_not_planned`) 수락된 것이 없다(`review_results_not_accepted`). |
| `not_requested` | 스키마 enum에 있으나 현재 builder는 쓰지 않는다. |

필수 역할은 계획에서 정한다. 계획에 `review-r1` 역할(`review.environment`, `review.documents`, `review.interpret` 중 하나)이 있으면 `required_for_verdict`인 `review.*` task가 필수이고, 나머지는 `optional_tasks`에 결과·사유와 함께 적으며 수행으로 세지 않는다. 그 밖의 계획(`review-v1` 기록, 검토 task 없음)은 `review.context`, `review.investigate`, `review.verify` 세 역할로 해석하고 계획되지 않은 역할은 `not_planned`로 적는다. task별 `result`는 `accepted`, `not_planned`, `not_succeeded`, `result_missing`, `result_rejected`, `skipped`(review-r1의 정책 생략)다.

`coverage.review_calls`는 검토 기록이 있는 리비전에서 호출마다 역할·모델·OpenCode 버전·지침 팩 digest·요청 SHA-256·호출 결과를 실제 담당 역할에 한 번씩 싣는다. review-r1 기록은 `pack_id`·`pack_version`도 싣는다. 호출 결과가 `accepted`여도 후보의 판정은 `refuted`일 수 있다. 수락한 응답이 실제 실행이나 사람의 확인을 대신하지 않는다. 검토 산출물의 수락 조건과 단계 연결 검사(`review_lineage_mismatch:<task>`, `review_pack_mismatch:<task>`)는 [검토](REVIEW.md)에 있다.

## 집계

`summary`의 모든 숫자는 `findings`와 `observations`에서 한 함수(`ComputeSummary`)로 계산한다. 저장된 값이 재계산과 다르면 검증 실패다. LLM 서술은 숫자를 만들 수 없다.

| 필드 | 계산 |
|---|---|
| `total_findings` | 발견 항목 전체 수. 숨김 판정 항목도 포함한다. |
| `actionable_findings` | `supported`·`reproduced`이면서 숨김 판정이 아닌 항목 |
| `pending_candidates` | `unreviewed`·`inconclusive` 항목(숨김 판정 포함) |
| `refuted_findings` | `refuted` 항목 |
| `raw_observations` | 원시 도구 진단 수 |
| `unresolved_observations` | 위치를 snapshot manifest로 확인하지 못한 진단(`evidence_state`가 `verified_against_source_manifest`가 아님) |
| `metric_observations` | 원시 진단 중 구조 지표(복잡도·중복) 수. 0이면 생략한다. 결함이 아니다. |
| `hidden_findings` | 게시 시점 판정이 `accepted_risk`, `false_positive`, `suppressed`, `fixed_by_user`인 항목 수. 0이면 생략한다. 전체 수와 비교에는 남는다. |
| `priorities` | 우선 확인할 항목 ID 최대 5개. 숨김이 아닌 조치 항목을 먼저, 그다음 숨김이 아닌 미확정 후보를 두고, 같은 묶음 안에서는 심각도, 첫 위치(경로·시작 줄), ID 순이다. |

원시 진단 수와 원인별 항목 수를 나란히 보여 중복 제거를 설명한다. 한 개의 100점 품질 점수는 만들지 않는다. 같은 항목은 한 번만 센다. 검증 산출물이 없거나 선행 기록이 바뀌면 조사 단계의 후보는 미검증으로 남는다.

## 분석 범위와 한계

| `coverage` 필드 | 내용 |
|---|---|
| `scope_mode` | `full` 또는 `paths` |
| `inventory_files` | snapshot manifest에 포함된 파일 수(언어 무관) |
| `tool_processed_files` | 분석기 task가 모두 처리 파일 수를 알려 줄 때만 숫자다. 처리 경로를 모두 기록했으면 경로 합집합 크기(여러 도구가 읽은 파일을 한 번만 셈), 아니면 합이다. 모르면 null이며 0이 아니다. |
| `llm_exposed_files` | 모델에 보낸 파일 수. 검토 task가 계획되지 않았으면 0, 계산할 수 없으면 null(미확인)이다. |
| `test_status` | 현재 builder는 항상 `not_run`이다. 테스트 실행 작업이 없으므로 `with-tests` 프로필 보고서는 언제나 `partial`이다(`required_tests_not_performed`). |
| `notes` | 해석 메모 코드: `inventory_counts_all_included_files`, `tests_not_planned`, `tool_processed_files_unknown`, `tool_reported_zero_is_not_clean`(도구가 0건을 보고한 것은 결함 없음이 아니다) |
| `ai_review`, `review_calls` | [AI 검토 상태](#ai-검토-상태) |
| `tools` | 분석기 task마다 한 행 |

도구 행은 task key, 분석기 ID, 도구 버전, exit class, 부분 파싱 여부, 범위·처리·제외·미목록 파일 수, 패키지 상태별 수, stderr 실패·분석기 오류 수, 행별 한계를 담는다. 처리 경로(`processed_paths`)는 성공·수락된 task의 경로만 싣고, 비교가 사라진 발견의 위치를 다시 검사했는지 판단할 때 쓴다. 규칙 문맥(`rule_context`: 규칙 팩·버전·선택·저장소 설정 처리·소스 안 억제 처리)의 정의와 분석기별 값은 [분석기](ANALYZERS.md)에 있다.

### 한계 코드

`limitations`와 `notes`는 코드만 저장한다. 렌더러가 [한국어 문구 표](../internal/report/messages_ko.go)로 번역하고 모르는 코드는 그대로 보여준다. 주요 보고서 수준 코드는 다음과 같다. 분석기·검토 단계가 더하는 코드는 해당 문서에 있다.

| 코드 | 뜻 |
|---|---|
| `ai_review_not_performed`, `ai_review_partial` | 필수 AI 검토가 수행·검증되지 않았거나 일부만 검증됐다. |
| `result_manifest_unavailable`, `result_manifest_rejected:<사유>` | 결과 manifest가 없거나 거부돼 DB task 행만 보여준다. |
| `required_task_not_succeeded` | 성공하지 않은 필수 task가 있다. |
| `analysis_artifact_missing:<task>`, `artifact_rejected:<task>` | 분석 산출물을 읽지 못했거나 검증에 실패해 그 task 결과를 뺐다. |
| `unrecognized_artifact:<task>` | decoder가 없는 JSON 산출물이 있다(결과에 반영하지 않음). |
| `task_not_in_result_manifest:<task>`, `artifact_status_differs_from_accepted_task:<task>` | manifest와 DB 행이 어긋나 DB 행을 따랐다. |
| `static_analysis_gap:<생태계>` | 범위에 그 언어 소스가 있으나 노드에 분석기가 없다. |
| `static_analysis_not_planned` | 범위에 지원 언어 소스가 없어 분석기 task를 계획하지 않았다. |
| `cleanup_uncertain` | 하위 프로세스 정리를 확인하지 못했다. |
| `controller_holds_no_snapshot_bytes` | 관리 서버는 snapshot 원본을 보관하지 않아 코드 발췌를 넣지 않았다. 모든 보고서에 붙는다. |
| `required_tests_not_performed` | `with-tests`가 요청한 테스트 결과가 없다. |
| `run_cancelled`, `run_failed` | 실행이 취소·실패했다. |

### 현재 보고서에 없는 것

- **builder 4 이하 리비전의 탐색 결과.** `env.discover`와 `docs.discover`의 산출물은 `EnvironmentDecoder`([`environment.go`](../internal/report/environment.go))가 attempt·snapshot·probe 실행 기록과 대조해 검증한다. builder 5부터 그 결과가 환경 분석·문서 분석 절이 되지만, 이전 리비전은 다시 빌드하지 않으므로 산출물 행과 task 상태·사유로만 보인다. 관찰·발견·도구 행에 더하지 않는 것은 같다.
- **코드 발췌.** 관리 서버에는 snapshot 파일 본문이 없어 보고서에 코드 발췌를 넣지 않는다. 노드 원본 경로에서 다시 읽지도 않는다. 위치와 blob SHA-256으로 snapshot 코드를 확인한다.
- **테스트 실행 결과.** 위 `test_status`와 같다.
- **결함 영역별 절.** 절은 분석 종류(환경·문서·정적 분석·지표·비밀정보·모델 검토)로 나눈다. 발견의 `category`(정확성·동시성 등) 축마다 수행한 검사와 제한을 보여주는 절은 없고, 발견 목록이 category로 묶일 뿐이다.
- **Markdown·HTML의 일부 필드.** 두 형식은 도구 행의 `rule_context`, `hidden_findings`, 현재 판정을 렌더링하지 않는다. 판정은 게시 시점 값만 보인다. JSON과 [비교 응답](#run-비교)에서 읽는다.

## 발견 항목 읽기

발견 항목 하나는 "어떤 조건에서 무엇이 잘못되는지"를 근거와 함께 적는다. 화면과 내보내기에서 다음 순서로 읽는다.

| 필드 | 읽는 법 |
|---|---|
| `title` | 구체적인 결과와 조건. "코드를 개선하세요" 같은 제목은 쓰지 않는다. |
| `category` | `correctness`, `error_handling`, `concurrency`, `resource_lifecycle`, `data_integrity`, `api_contract`, `security`, `maintainability`, `performance`, `test_gap` |
| `severity`, `severity_reason` | 심각도와 그 이유 |
| `validation` | 검증 상태(아래) |
| `disposition` | 게시 시점의 사람 판정. 현재 판정은 비교 응답에서 따로 읽는다. |
| `conditions` | 발생 조건. `verified`(근거 ID 필수) 또는 `assumed`(남은 전제) |
| `impact` | 사용자·데이터·서비스·운영 영향과 범위 |
| `locations` | snapshot ID·경로·줄 범위·선택 심볼 |
| `claims` | 주장과 근거 ID. `observed`(수집한 사실) 또는 `inferred`(추론) |
| `evidence` | 근거. `code`·`document`는 snapshot 위치와 blob SHA-256, `tool`·`execution`은 attempt 산출물과 task 실행 ID(`execution`은 종료 상태 필수) |
| `counterevidence_ids` | 반대 근거. 확인한 방어 코드나 배제한 가능성 |
| `observation_ids` | 이 항목을 만든 원시 도구 진단 |
| `recommendation` | 변경 방향과 호환성·부작용 고려 |
| `verification` | 회귀 시험 입력·조건 또는 검토 절차 |
| `limitations` | 이 항목의 한계 |

근거는 수집 계층(검증된 산출물을 읽는 decoder)만 만든다. 코드 근거의 경로·hash가 snapshot manifest에 없거나, 도구 근거가 이 attempt의 산출물·task 실행에 묶이지 않으면 그 산출물을 거부한다. 다른 출처의 위치는 격리한다. 억제된 관찰과 구조 지표 관찰로는 발견을 만들 수 없다. schema 검증이나 근거 확인에 실패한 주장은 본문에 확정적으로 싣지 않는다.

### 심각도

| 값 | 기준 |
|---|---|
| `critical` | 현실적인 조건에서 광범위하고 중대한 영향 |
| `high` | 중요한 경로의 심각한 실패 |
| `medium` | 제한된 조건의 실질적 결함 |
| `low` | 영향이 낮은 개선 |
| `info` | 설명·관찰 |

영향이 크다는 가정만으로 `critical`로 올리지 않는다.

### 검증 상태

| 값 | 뜻 |
|---|---|
| `unreviewed` | 검토 전 후보. 도구 진단에서 만든 항목은 항상 이 값이다. |
| `supported` | 근거가 주장을 지지한다. |
| `reproduced` | 실행으로 재현했다. 실행 근거가 없으면 검증 실패다. |
| `refuted` | 반대 근거로 기각했다. |
| `inconclusive` | 판단할 근거가 부족하다. |

도구 진단·모델 후보·재현된 결함·사람의 판단은 서로 다른 기록이다. 도구가 보고했다는 사실은 실제 결함 확정과 같지 않다. 검증 상태와 검토 우선순위는 별개다. 영향이 클 수 있지만 미확정인 후보도 우선 확인 목록에 들 수 있다. 모델의 숫자 confidence를 정확도처럼 표시하지 않는다. 검증 상태와 근거 종류로 정성적 근거 수준을 보여준다.

## 코드 미포함 내보내기

`include_code=false`가 기본이다. 저장본을 바꾸지 않고 다음 projection을 만든다([`Project`](../internal/report/validate.go)).

- 관찰의 `raw_message`를 모두 null로 만든다. Markdown·HTML은 그 자리에 "(코드 미포함: 생략)"을 쓴다.
- 발견 제목을 고정 형식(`<도구> <규칙> 진단 (<경로>:<줄>)` 또는 `<category> 항목 (<경로>:<줄>)`)으로 바꾼다.
- 심각도 이유·영향·개선·확인·한계·조건·근거 설명 중 builder의 고정 문구가 아닌 것은 생략 문구로 바꾸고, 주장(`assertion`)은 모두 생략한다. 서술에 섞인 코드 인용도 이렇게 빠진다.
- 위치와 근거 위치의 심볼 이름을 지운다.
- `fingerprint`, `fingerprint_basis`, `disposition_version`을 싣지 않는다. 키 없는 digest는 짧은 코드 줄을 추측해 맞춰 볼 수 있는 확인 수단이 되기 때문이다. 식별과 현재 판정은 프로젝트 권한을 검사하는 비교·판정 API로만 읽는다. 프로젝트별 키로 감싼 식별자는 키 수명 관리가 필요해 만들지 않았다.
- 도구 행 `rule_context`의 `config_paths`·`config_digest`를 지운다. 설정 바이트의 digest도 같은 이유로 추측 확인 수단이 된다.
- 분석 종류별 절의 모델 문장(환경 메모, 문서 계약·충돌·질문·지침 메모, 진단 해석의 한계)을 생략 문구로 바꾸고, 모듈 요구의 제약 문자열은 버전 제약 형식이 아니면 지운다([분석 종류별 절](#분석-종류별-절)).

스키마의 code-free 규칙과 [`scripts/validate_bundle.py`](../scripts/validate_bundle.py)의 부정 사례가 식별·규칙 문맥 규칙을, `ValidateReport`가 절의 모델 문장 규칙을 강제한다. 경로·줄 번호·규칙 ID·산출물 hash는 남는다. 비밀값과 그 일부·hash는 처음부터 저장하지 않는다([분석기](ANALYZERS.md)). 코드 포함 내보내기는 같은 프로젝트를 읽을 수 있는 사용자에게 저장된 도구 메시지·검토 문구·심볼을 그대로 준다. 내보내기 다운로드도 요청마다 프로젝트 권한을 검사한다.

## Markdown과 HTML 렌더링

두 형식은 같은 view 모델에서 만든다. 분석 종류별 절이 있는 리비전은 상세도가 둘이다([내보내기 상세도](#내보내기-상세도)). 아래 첫 항목은 전체 보기(`detail=full`)의 배치다. 머리 표(리비전·digest·상태·판정·run·attempt·snapshot·커밋·프로필·생성 시각·내보내기 종류)와 상태 배너가 먼저 온다. 배너는 부분·실패·취소 결과임을 첫머리에 알린다.

- **분석 종류별 절이 있는 리비전.** 종합(판정·요약 표·분석 종류별 상태 표·먼저 볼 것·우선 확인할 발견), 환경 분석, 문서 분석, 정적 분석(언어별 도구 표·진단·도구 발견), 구조·중복 지표, 비밀정보, 코드 분석(역할·모델 검토 발견·진단 해석의 한계), 분석 공백, 부록(AI 검토, 분석 범위, 작업 상태, 실행 기록, 산출물, 한계와 공백), projection JSON digest 순이다. 각 절은 첫 줄에 단계 상태와 task·성공 수를 쓰고, 표의 수는 종합 블록의 수와 같다. 모델 문장은 "모델 해석(역할)" 표시와 근거 ID를 붙여 쓴다. HTML은 절 목차와 절마다 `id`를 두고, 상태 표의 수 칸에 `data-count`를 붙인다.
- **이전 리비전(builder 4 이하).** 상세도와 관계없이 요약 표, 우선 확인 목록, AI 검토(필수·선택 task와 모델 호출), 분석 범위와 도구 표, 작업 상태, 발견 항목, 원시 도구 관찰, 실행 기록, 산출물, 한계와 공백, projection JSON digest 순의 평면 배치다. 출력은 절 도입 전과 바이트 단위로 같다([`testdata`](../internal/report/testdata/report-go-vet.md)).

### 내보내기 상세도

Markdown과 HTML의 기본은 요약 보기(`detail=summary`)다. 실제 자체 분석 보고서(builder 6, 도구 진단 813건·모델 검토 발견 48건·작업 183개·실행 445개·산출물 883개)는 전체 보기에서 19,096줄이었고 요약 보기에서 782줄이다. 요약 보기는 렌더링만 바꾸며 저장 본문·digest·JSON 내보내기는 같다. 그래서 이전 builder의 리비전도 같은 요약으로 보인다. 구현은 [`render_summary.go`](../internal/report/render_summary.go)다.

- **순서.** 종합, 코드 분석(모델 검토), 정적 분석, 구조·중복 지표, 비밀정보, 환경 분석, 문서 분석, 분석 공백, 부록 순이다. 종합 표도 이 순서다.
- **종합 문장.** 판정 문장 뒤에 구조화 데이터로 센 문장을 쓴다. 모델 검토 발견 수와 그중 근거로 지지한 수(재현·판단 보류·기각·검증 전), 장부가 있으면 주요 경로 파일 중 모델에 보낸 수와 부차 경로 파일 중 보낸 수, 도구 진단 수와 규칙·도구 종류, 억제된 진단과 부차 경로 진단 수, 성공하지 않은 필수 분석 작업, 지표 임계 초과, 비밀정보 탐지, 분석 공백 수와 종류다. 지지는 모델의 판단이며 재현이나 사람의 판정이 아니라고 함께 쓴다.
- **코드 분석.** 근거로 지지했거나 재현한 발견 중 주요 경로의 것은 짧은 카드(범주·심각도·판정, 위치, 영향, 권고)로, 위치가 모두 부차 경로인 지지 발견과 그 밖의 발견은 한 줄씩 표로 보인다. 장부는 주요 모듈마다 한 행과 부차 모듈의 합계 한 행이고, slot은 같은 상태·이유가 이어지는 구간을 한 행으로 묶는다.
- **정적 분석.** 언어마다 도구 표(분석기마다 한 행: 작업·필수·성공 수, 진단 수, 성공하지 않은 작업과 이유 앞 5개)와 규칙 표(분석기·규칙마다 한 행: 진단 수, 가장 높은 심각도, 첫 진단 메시지 한 줄(100자), 위치)다. `.mNN` 작업은 분석기별로 합친다. 위치는 주요 경로를 먼저 앞 5곳 보이고 나머지는 "외 N곳(그중 부차 경로 M곳)"으로 센다. 위치가 5곳 이하면 모두 보인다. 주요 경로 위치가 있는 규칙이 먼저 온다. 억제 표시된 진단은 규칙 표에 넣지 않고 수만 적는다. 개별 진단과 도구 발견은 전체 보기와 JSON에 있다.
- **지표·비밀정보.** 지표마다 한 행(임계값, 최댓값, 수, 주요 경로부터 큰 값 앞 5곳). 비밀정보는 주요 경로 탐지만 표로 보이고 부차 경로 탐지는 규칙별 수로 적는다.
- **환경·문서.** 부차 경로 아래의 모듈·요구·판정·문서·문서 모듈·지침 겹침은 표에서 빼고 수로 적는다. 판정은 충족·불일치를 한 줄씩, 판단 불가는 도구·이유별로 묶는다. 불일치는 부차 모듈이어도 보인다. 읽지 않은 문서는 상태별로 묶는다.
- **공백·부록.** 분석 공백은 절·공백·세부별 한 행(수, 대상 앞 5개)이다. 부록은 AI 검토 상태와 호출 수, 분석 범위 수, 성공하지 않은 작업을 상태·이유별로 묶은 표(입력이 없어 건너뛴 작업 제외), 실행 기록·산출물의 수, 한계다.
- **부차 경로.** 경로 판단은 [검토 우선순위](REVIEW.md#검토-우선순위)의 기본 부차 경로 규칙(`record.DefaultSecondary`)을 경로에만 적용한다. 생성 파일 표지는 보지 않는다. 장부 행은 builder 8이면 저장된 `class`를, 그 이전이면 같은 경로 규칙을 쓴다.
- **링크.** 요약에 없는 대상(도구 발견, 관찰 행)을 가리키는 문서 안 링크는 지운다. HTML의 모든 `href="#…"`는 같은 문서의 `id`를 가리킨다(시험).
- **코드 미포함.** 규칙 표의 메시지는 projection의 메시지(생략 문구)를 쓰므로 도구 원문이 나오지 않는다.

전체 보기는 위 배치의 모든 행이다. 요약 보기 도입 전의 출력과 바이트 단위로 같다(시험).

항목이 없으면 "결함이 없다는 뜻이 아니다"를, 관찰이 없으면 "도구가 0건을 보고한 것과 분석하지 않은 범위는 다르다"를 함께 쓴다. 실행하지 않은 도구 행은 진단 수 대신 "실행하지 않음"을 쓴다.

- **Markdown.** 모든 동적 값은 이스케이프한 평문이거나 코드 span이다. CommonMark 문장부호에 역슬래시, HTML 메타문자에 entity, 표의 `|`에 이스케이프를 붙이고 줄바꿈·제어 문자는 공백으로 바꾼다. 코드 span의 fence는 값 안의 가장 긴 backtick 연속보다 길다. 도구 문자열이 링크·이미지·HTML·표 구조를 만들 수 없다.
- **HTML.** `html/template`의 문맥별 escaping을 쓴다. 스크립트·외부 이미지·폰트·CDN이 없고 링크는 같은 문서 안 fragment뿐이다. CSP(`default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`)를 응답 헤더와 `<meta>`에 함께 넣어 저장한 파일도 같은 정책을 갖는다. 긴 표와 코드는 가로 스크롤 영역에 둔다.
- 모델 서술과 코드 안의 HTML·스크립트는 렌더링하지 않고 이스케이프한다. 외부 이미지 로딩과 자동 링크 fetch는 하지 않는다.

## 발견 식별 fp2

발견 식별 규칙은 [`internal/report/fingerprint.go`](../internal/report/fingerprint.go) 한 곳에만 있다. 분석기 decoder는 `ToolFingerprint`, 모델 후보는 같은 파일의 함수를 부르며 자체 해시를 만들지 않는다.

```
fingerprint = "fp2:" + sha256( NUL로 이은 [
  "codeye-fingerprint", "fp2", producer, analyzer, rule,
  paths(정렬·중복 제거, 줄바꿈으로 이음), symbols(같음), context, condition ])
move_key    = 같은 식에서 첫 값을 "codeye-move-key"로, paths를 빈 값으로 바꾼 것
```

| 입력 | 도구 발견 | 모델 후보 |
|---|---|---|
| producer / analyzer | `tool` / 분석기 ID(예: `go.vet`) | `model` / `review` |
| rule | 도구 규칙 ID | 원인 분류 `candidate:<검증기 category>`(판정이 있을 때), 없으면 `candidate:unclassified` |
| paths | 진단 위치의 snapshot 상대 경로 | 후보가 인용한 코드·테스트 발췌의 경로 집합(없으면 문서 경로) |
| symbols | 둘러싼 심볼(분석기가 줄 때만) | 인용 발췌의 심볼 집합 |
| context | `unavailable` | `unavailable` |
| condition | 정규화한 도구 메시지: 파일 위치(`a.go:12:3`)→`<pos>`, `line N`→`line <n>`, 공백 압축 | 연결된 도구 관찰의 (도구, 규칙, 경로, 정규화 메시지) 집합 |

- 줄·열 번호, snapshot·run·attempt ID, 모델이 쓴 제목·서술, 근거 인용 순서는 입력이 아니다. 줄 이동이나 제목 변경은 같은 식별을 유지한다.
- 프로젝트와 source variant는 해시 입력이 아니라 비교 범위다. 판정은 프로젝트 단위, 기준 보고서는 source variant 단위로 찾는다.
- 한 보고서 안에서 식별은 유일하지 않다(같은 파일·규칙·메시지의 진단 두 개). 비교와 판정은 식별을 묶음 키로 다루고 하나씩 짝짓지 않는다.
- 보고서에는 `fingerprint`와 `fingerprint_basis`(scheme, producer, analyzer, rule, paths, symbols, context, move_key)를 저장한다. 정규화한 condition은 도구 원문이므로 저장하지 않고 해시에만 쓴다.
- 코드 문맥 해시는 아직 없다(`context: unavailable`). 관리 서버는 snapshot 원문을 갖지 않고 현재 산출물이 정규화 문맥을 싣지 않는다. 그래서 같은 파일·규칙·메시지의 진단은 같은 식별을 공유하고 모호한 묶음이 된다.
- **약한 식별.** 심볼도 연결 관찰도 없는 모델 후보는 경로만으로 식별된다(`move_key` 비어 있음). 같은 파일의 서로 다른 문제가 같은 식별을 가질 수 있다.
- builder 2 이하 보고서의 식별(`go-vet-fp-v1:…` 등)은 저장된 그대로 읽는다. 다시 계산하거나 변환하지 않는다.
- 새 분석기 decoder는 `ToolFingerprint`로 식별을 만들고 성공한 작업의 `processed_paths`를 채워야 그 분석기의 사라진 발견이 `resolved_observed`가 될 수 있다. 채우지 않으면 사라진 발견은 항상 `location_not_inspected`다.

## run 비교

비교는 같은 프로젝트의 게시된 두 리비전을 대조한다. 저장 테이블 없이 요청마다 불변 보고서 본문과 run 사실로 다시 계산한다([`internal/history`](../internal/history/compare.go), 순수 함수). 비교는 불변 보고서에서 결정적으로 계산되므로 파생 테이블은 원천과 어긋날 위험만 더한다. 경로·권한은 [API](API.md)에 있고 응답 예시는 [`examples/comparison.json`](../examples/comparison.json)이다.

### 기본 기준

기준을 지정하지 않으면 같은 프로젝트에서 이 run보다 먼저 만든 run 중 다음을 모두 만족하는 가장 최근 run의 가장 최근 리비전이다.

- source variant가 같다. 그 run의 프로젝트 설정 `source`(Git 저장소+브랜치 또는 source root+경로)가 같다.
- 요청의 `profile`과 `scope`가 같다.
- 보고서가 `completed` 또는 `partial`이고 builder 3 이상이다. `failed`·`cancelled` 보고서는 기본 기준이 아니며, 명시하면 비교할 수 있다.

응답의 `baseline.selection`은 `default` 또는 `explicit`이다. 기준이 없으면 `comparable=false`, `not_comparable_reason=no_baseline`이다. 한쪽이라도 builder 3 미만이면 `comparable=false`, `not_comparable_reason=fingerprint_scheme_differs`이고 run 차이(`builder_version` 등)만 보여준다.

### 항목 상태

| 상태 | 조건 |
|---|---|
| `new` | 이번에만 있다. 기준 run이 이 위치를 같은 방식으로 볼 수 없었다면 `reason`에 그 이유를 단다. |
| `persisting` | 양쪽에 있고 개수·심각도·검증 상태·영역이 같다. 줄 위치만 다르면 `changes: ["lines"]`인 persisting이다. |
| `changed` | 양쪽에 있고 `count`·`severity`·`validation`·`category` 중 하나가 다르다(`changes`에 적는다). |
| `resolved_observed` | 기준에만 있고 아래 조건을 모두 확인했다. 도구 발견만 가능하다. |
| `absent_unverified` | 기준에만 있고 아래 조건 중 하나라도 확인되지 않는다. `reason`이 필수다. |

**`resolved_observed` 조건.** 이번 run이 같은 source variant·scope에서 같은 분석기를 성공적으로 실행했다(task 성공, 성공 exit class, 부분 파싱 아님, 산출물 거부 아님, 처리 파일 수 확인). 그 분석기의 `processed_paths`가 발견의 모든 경로를 포함한다. 도구 버전이 양쪽에서 알려져 있고 같다. 규칙 문맥이 양쪽에서 알려져 있고 같다. 이번 run의 도구가 그 진단을 소스 안 억제로 숨겼다고 보고하지 않았다. 이번 run의 도구가 그 규칙의 억제를 보고하지 않는 방식(`source_suppressions=honored`, 또는 그 규칙이 `unreported_suppression_rules`에 있음)도 아니다.

**사라짐은 해결이 아니다.** 도구 실패·산출물 거부·부분 파싱·미계획·범위 축소·파일 미분석·도구 버전이나 규칙 문맥 변경·버전 미상·source variant 차이 때문에 사라진 발견은 `absent_unverified`와 이유로 남는다. 모델이 후보를 다시 내지 않은 것은 관찰이 아니므로 모델 후보는 절대 `resolved_observed`가 되지 않는다. 모델 후보의 해결은 사람 판정(`fixed_by_user`)이나 도구 관찰로만 남는다.

### 이유 코드와 우선순위

| `reason` | 뜻 |
|---|---|
| `source_variant_differs` | 두 run의 source variant가 다르다. |
| `scope_reduced` | 관찰한 쪽 scope가 발견 경로를 덮지 않는다. |
| `check_not_run` | 관찰한 쪽에 그 분석기 결과가 없고 task도 계획되지 않았다. |
| `check_failed` | 계획했지만 수락된 결과가 없거나, 실행이 실패해 위치를 검사하지 못했다. |
| `location_not_inspected` | 분석기는 성공했지만 처리 경로가 발견 경로를 포함하지 않는다. |
| `suppressed_in_source` | 관찰한 쪽 도구가 같은 fp2의 진단을 억제됨으로 보고했다(new에서는 기준 쪽이 억제했다). |
| `rules_unknown`, `rules_changed` | 도구 버전을 모르거나 다르다. 모델 후보에서는 역할·모델·지침 digest가 다르면 `rules_changed`다. |
| `rule_context_unknown`, `rule_context_changed` | 한쪽 규칙 문맥을 모르거나(문맥이 없는 행, 저장소 설정을 읽으면서 파일을 기록하지 못함) 규칙 팩·버전·선택·저장소 설정 모드·설정 digest·억제 처리 중 하나가 다르다. |
| `source_suppressions_hidden` | 현재 쪽 도구가 그 규칙의 억제를 적용하고 숨긴 진단을 보고하지 않는다(예: staticcheck U1000). |
| `model_review_not_performed`, `model_review_partial` | 관찰한 쪽 모델 검토가 수행되지 않았거나 일부만 수행됐다. |
| `model_absence_not_observation` | 모델 검토는 같은 조건으로 수행됐지만 다시 내지 않은 것은 해결 관찰이 아니다. |

도구 발견의 이유는 source variant → scope → 미실행·실패 → 위치 미검사 → `suppressed_in_source` → 도구 버전 → 규칙 문맥 → `source_suppressions_hidden` 순으로 처음 맞는 하나다. go vet 문서에는 규칙 팩이 없어 builder 4 decoder가 `codeye-go-vet-default` v1을 합성해 행에 쓰고, 문맥이 없는 builder 3 go vet 행은 비교가 같은 문맥을 합성한다. 그래서 builder 3과 4의 go vet 보고서는 서로 비교된다. 다른 분석기의 문맥 없는 행은 `rule_context_unknown`이다.

### 모호한 묶음과 이동 후보

- `ambiguous`: 한쪽에 같은 식별의 발견이 여럿이면 true다. 상태는 묶음 전체로 정하고 하나씩 짝짓지 않는다.
- `move_candidates`: `move_key`가 같고 경로만 다른 새 항목과 사라진 항목을 서로 가리킨다. 이동·이름 변경 후보로만 보여주고 자동으로 합치거나 판정을 옮기지 않는다.

### 차이 목록과 집계

- `differences`는 값이 다른 항목만 싣는다. 키는 `source_variant`, `source_revision`(커밋 SHA 또는 manifest digest), `profile`, `scope`, `builder_version`, `tool:<analyzer>:version|rule_context|result|processed_files`, `ai_review`, `review:<role>`(모델·지침 digest)다. 화면은 비교 대상의 환경·규칙·범위 차이를 이 목록으로 상단에 보인다.
- `rule_context` 차이 값은 `pack@version [선택] config=<모드>[:설정 digest 앞 12자|:unrecorded] suppressions=<모드>[ unreported=<규칙,…>]`이고 문맥이 없는 행은 `unknown`이다. 설정 digest 앞부분은 코드 미포함 내보내기에서 빼는 값이지만, 비교 응답은 코드 포함 보고서를 읽을 수 있는 같은 프로젝트 읽기 권한자에게만 가고 이미 fp2 식별을 싣는다.
- `counts`는 entry(식별) 단위 상태 수다. `unidentified`는 fp2 식별이 없어 비교하지 않은 발견 수(현재·기준)다.
- 비교는 두 리비전 사이만 본다. 첫 발견 시각과 run 사이 연속 이력은 계산하지 않는다.

## 사람 판정

사람 판정(disposition)은 발견 식별에 이유·기간·범위·기록자와 함께 남긴다. 경로·헤더·오류는 [API](API.md)에 있고 예시는 [`examples/finding-reviews.json`](../examples/finding-reviews.json)이다.

### 판정 기록

판정 값은 `open`, `acknowledged`, `accepted_risk`, `false_positive`, `fixed_by_user`, `suppressed` 중 하나다. 판정 기록 하나는 프로젝트·fingerprint의 다음 version(1부터 빈틈없이 증가)이며 다음을 갖는다.

- 사유: 필수, 공백 제외 1..2000자
- 선택 만료 시각: 미래 시각만
- 선택 경로 범위: snapshot 상대 경로 접두사. 그 경로와 그 아래에만 적용한다. glob은 받지 않는다.
- 기록자, 기록 시각(DB 시각)
- 사람이 판단한 게시 보고서의 발견: `run_id`, `revision`, `finding_id`. 그 본문에 같은 fingerprint로 있어야 한다.

`open`은 만료·경로 범위를 갖지 않는다. 판정 행은 수정·삭제·TRUNCATE할 수 없고(DB trigger) 이 행들이 감사 기록이다. 판정 기록 권한은 프로젝트 소유자·manager·runner이고 viewer는 읽기만 한다.

### 적용 규칙

적용하는 판정은 fingerprint마다 최신 version 하나다.

| `review_match` | 뜻 | 현재 판정 |
|---|---|---|
| `none` | 판정 없음 | `open` |
| `applied` | 적용 중 | 판정 값 |
| `candidate` | 같은 식별이 그 리비전에 여럿이거나 식별이 약하다. 사람이 그 발견에서 다시 판정해야 적용된다. | `open` |
| `expired` | 만료 시각이 지났다(DB 시각 기준) | `open` |
| `out_of_scope` | 경로 범위가 발견 경로를 모두 덮지 않는다 | `open` |
| `reverted` | 최신 판정이 `open`이다 | `open` |

판정이 직접 가리킨 발견(같은 run·리비전·finding ID)에는 식별이 약하거나 모호해도 현재 판정으로 적용한다. 그 밖에는 그 리비전에 같은 식별이 하나뿐이고 식별이 약하지 않을 때만 자동 적용한다.

이 규칙의 결과로 다음 제약이 생긴다.

- 모호한 묶음에서 한 발견을 판정한 뒤 같은 묶음의 다른 발견을 판정하면 앞의 판정은 후보(열림)로 돌아간다. 이후 run의 게시 시점에도 그 묶음에는 고정되지 않는다.
- 원인 분류·경로·심볼이 모두 같은 다른 모델 후보가 이후 리비전에 하나만 있으면 강한 단일 일치로 보아 판정을 이어받는다. 원인 분류는 검증기 category 수준이라 같은 심볼의 서로 다른 결함을 구분하지 못한다.

### 게시 시점 판정과 현재 판정

- **게시 시점.** 보고서를 만들 때 프로젝트의 fingerprint별 최신 판정을 위 규칙(직접 지정 예외 없이)으로 발견의 `disposition`·`disposition_version`에 고정한다. 판정 집합은 `input_digest`에 들어가며, 이후 판정이 바뀌어도 그 리비전 본문은 바뀌지 않는다. 과거 리비전을 열면 그 리비전의 게시 시점 판정이 보인다.
- **현재.** 비교 응답의 `findings[]`가 발견마다 `publication_disposition`, `current_disposition`, `review_match`, 최신 `review`를 따로 준다.
- **집계.** 숨김 판정(`accepted_risk`, `false_positive`, `suppressed`, `fixed_by_user`)은 조치 집계와 우선 확인에서 빠지고 `hidden_findings`로 센다. `total_findings`·원시 진단·비교에는 그대로 남는다. 비교 응답의 `summary.at_publication`과 `summary.current`는 같은 함수로 각각 계산한다.
- 일괄 판정, 이동 후보로의 판정 이전, 첫 발견 시각 이력은 없다.

## 웹 화면

웹은 `codeye serve`가 같은 origin에서 제공하는 단일 화면 앱([`web/src`](../web/src/App.tsx))이다. 모든 상태·수·판정은 서버 응답의 구조화 값이다. 화면 문자열은 [`web/src/locales`](../web/src/locales/ko.ts)에 분리되어 있다.

### 공통 표시 규칙

- 서버 응답은 화면에 쓰기 전에 형식을 검증한다. 예상과 다른 응답은 "응답 형식 오류"이고 추측해서 표시하지 않는다. 보고서는 항목 수·관찰 수·구조 지표 수가 `summary`와 맞지 않으면 거부하고, 비교는 `counts`가 entry 상태 수와 맞지 않으면 거부한다.
- run 상태·단계 상태·비교 상태·판정·사유는 열린 문자열이다. 모르는 값은 원문과 "알 수 없음"으로 보이고 성공·해결·숨김으로 읽지 않는다.
- 단계 상태와 실행 상태는 글자에 기호(◷ 대기, ◉ 진행 중, ✓ 성공, ✕ 실패, △ 일부 수행 등)를 붙여 보이며 색만으로 구분하지 않는다. 비교 상태와 판정은 글자로 보인다.
- 새 분석을 시작해도 이전 게시 보고서를 감추지 않는다. 현재 실행과 마지막 게시 보고서를 함께 보인다.
- 연결이나 조회가 실패하면 마지막으로 받은 값과 확인 시각을 유지하고 재시도 경로를 보여준다. 오류에는 서버가 준 요청 ID를 함께 보인다.
- 모델 토큰 수·heartbeat·경과 시간은 완료율의 근거가 아니다.

### 세션

첫 화면은 세션 확인 중, 서버 연결 불가(재시도 버튼), 로그인 필요(링크 거부·만료·로그아웃 사유), 로그인됨 중 하나다. 로그인 링크의 토큰은 URL fragment에서 읽은 즉시 주소창에서 지운다. 인증 절차는 [API](API.md)에 있다. 머리글 메뉴는 "프로젝트·분석"과 "환경 진단"이다.

### 프로젝트 목록과 단계 표시

목록의 각 프로젝트 행은 이름(없으면 소스 요약), 소스 요약, 대표 실행 상태, 실제 진행 중인 단계 이름, 마지막 게시 보고서 링크를 보여준다. 대표 실행 선택과 단계 상태 계산은 서버가 하며([API](API.md)), 목록과 실행 상세는 같은 서버 집계를 읽는다.

| 화면 단계 | 볼 수 있는 일 |
|---|---|
| 소스 확보 | Git 또는 로컬 입력 고정 |
| 환경 분석 | 모듈과 런타임·도구 조건 확인 |
| 문서 검토 | README·하위 지침·설계 문서의 계약 확인 |
| 빌드·테스트 | 계획된 실행에만 나타난다 |
| 정적 분석 | 계획한 분석기 실행 |
| 코드 분석 | 독립 후보 조사와 호출부·반례 검증 |
| 보고서 생성 | 관찰·후보·범위를 묶어 게시 |

- 단계는 사용자가 읽는 순서로 놓는다. 실제로 둘 이상이 실행 중이면 모두 "진행 중"으로 보여준다. `no_tasks` 단계는 숨긴다.
- 실패·부분·취소 단계(또는 모르는 상태)가 있으면 주의 항목으로 보여준다. 진행 중 단계가 없는 대기 실행은 대기 사유를, 계획 미확정이면 "계획 미확정"을, 대기 중 단계가 있으면 그 단계를 보여준다. 대기열에 있다는 이유로 소스 확보를 시작했다고 표시하지 않는다.
- 단계 안에 성공과 실패·미지원·제외가 섞이면 "일부 수행"과 각각의 건수를 보인다. 문서가 없어 문서 검토를 생략했으면 그 사유를 함께 보인다. 취소·복구 중에는 마지막 확인 단계와 시각을 유지한다.
- 나머지 활성 실행이 있으면 그 수를 함께 보인다. 실행 기록이 없으면 "실행 기록 없음", 단계 근거가 없는 실행은 "세부 단계 미기록"과 확인 가능한 실행 상태를 보인다.
- 마지막 게시 보고서 링크는 그 보고서의 run·리비전에 고정된 코드 미포함 HTML 내보내기다. "최신"으로 따라가지 않는다.
- 목록은 대표 실행이나 다른 활성 실행이 끝나지 않은 프로젝트가 있을 때만 5초마다 다시 조회한다. 탭이 숨겨지면 멈추고 다시 보이면 이어간다. 실패하면 마지막 값과 확인 시각을 유지하고 간격을 두 배씩 최대 60초까지 늘린다. 실행 요청·상태 변경·더 보기·프로젝트 등록이 조회를 다시 시작한다.

### 프로젝트 등록과 상세

등록 필수 입력은 Git URL+브랜치 또는 source root+상대 경로다. 이름은 비워 두면 서버가 정한다. 상세는 소스, 소스 상태(등록만 확인한 소스는 미검증), 프로젝트 ID, 설정 리비전, 기능 공백(`source_access_unverified`, `execution_unavailable`, `report_unavailable`), 마지막 실행 열기를 보여주고 아래에 실행 패널과 예약 패널을 둔다.

### 실행 상세

- 실행 요청 본문은 고정이다: `deep-review`, `latest_at_acquire`, `full`. 응답을 잃어 다시 누르면 같은 Idempotency-Key를 다시 보내 같은 실행을 받는다.
- 실행 상태는 글자 배지로 보인다. 알 수 없는 상태는 원문과 안내를 함께 보인다.
- 실행 ID·version·요청 경로(사용자 요청 또는 예약 시각)·snapshot·종료 task 수/전체 수·추정 백분율(`progress` 요소와 숫자)·계획 revision과 종료 가중치/계획 가중치·가중치 근거·분모 변경 이력과 사유·대기 사유·품질 판정을 보여준다. 계획이 확정된 경우에만 "종료 수/계획 수"를 보이고 성공 수는 따로 보인다. 확정 전이면 "계획 미확정"이다.
- 단계 목록은 단계마다 실제 버튼으로 펼친다(키보드 조작). 펼치면 종료 수/계획 수, 성공 수, 시작·종료 시각을 보인다. 단계 옆에 종료 상태별 건수와 사유 문구를 붙인다. 마지막 이벤트 시각, 서버 확인 시각, 마지막 연결 확인 시각을 함께 보인다.
- 갱신 방식(실시간 연결, 연결 중, polling 대체, 숨김으로 일시 중지, 5초 polling)과 마지막 확인 시각을 글자로 보인다. 연결이 끊기면 마지막 확인 시각을 유지한다. 종료된 실행은 자동 갱신을 멈춘다.
- 취소는 확인 단계를 거친다. 취소 요청 뒤에는 "취소 요청됨"을 보인다. 취소 응답은 실제 프로세스 종료를 뜻하지 않으므로 상태 갱신으로 결과를 확인한다.

### 보고서 패널

종료된 실행에 게시 보고서가 있으면 "보고서 열기"로 최신 리비전의 코드 포함 JSON을 읽는다(같은 프로젝트 읽기 권한자 전용).

분석 종류별 절이 있는 리비전([`ReportSections.tsx`](../web/src/ReportSections.tsx)):

- 종합이 먼저 온다. 품질 판정, 집계, 분석 종류별 상태 표(절 이름은 그 절을 여는 버튼), 먼저 볼 것(항목마다 해당 절을 여는 버튼), 우선 확인할 발견을 보인다. 버튼을 누르면 그 절이 열리고 절 제목으로 초점이 옮겨진다.
- 그 아래 "이전 실행과 비교"를 화면에서만 보인다. 저장 문서와 내보내기에는 없다.
- 분석 종류마다 `details` 요소 하나다. 제목 옆에 단계 상태를 글자와 기호로 붙이고, 먼저 볼 것이 가리키는 절은 처음부터 열어 둔다. 절 안의 수 목록은 종합 블록의 수 그대로다. 모델 문장은 "모델 해석" 표시와 역할 이름을 붙인 별도 상자에 둔다. 실행하지 않은 도구는 "실행하지 않음"으로 보인다.
- 정적 분석과 코드 분석 절은 아래와 같은 발견 목록(category 묶음, 검색, 숨김 판정 체크박스)을 그 절의 발견으로 보인다. 부록에는 모델 호출 목록, 분석 범위 수, task 표, 원시 관찰, 한계 문구를 둔다.
- 화면은 [요약 보기](#내보내기-상세도)와 같은 순서와 묶음이다([`reportGroups.ts`](../web/src/reportGroups.ts)). 종합에 같은 종합 문장을 보이고, 정적 분석은 언어마다 분석기 표와 규칙 표, 장부는 주요 모듈 행·부차 모듈 합계 행·slot 구간, 지표는 큰 값 열, 공백은 절·공백·세부별 묶음이다. 코드 분석 절은 주요 경로의 지지·재현 발견을 먼저 보인다. 개별 진단·작업별 표·도구 발견, 그 밖의 검토 발견, 모든 장부 행과 slice 작업, 부차 경로의 환경·문서 행과 비밀정보 탐지, 모든 공백 행은 "전체 목록" 펼침 요소 뒤에 있고 펼칠 때만 그린다(키보드로 열 수 있는 `summary`). 경로 판단의 부차 경로 규칙은 범위 산출물 예시가 기록한 규칙과 같은지 시험한다. 실제 자체 분석 보고서(관찰 1,553개)는 처음 화면에 표 행 449개다.
- 해석기([`reportSections.ts`](../web/src/reportSections.ts))는 일곱 블록이 모두 있는지, 관찰·발견의 분할, 절별 발견 수의 합, 공백 수를 확인한다. 어긋나면 보고서 전체를 "응답 형식 오류"로 거부한다.

절이 없는 이전 리비전은 아래 평면 배치로 보인다.

- 품질 판정, AI 검토 상태, 집계(전체·조치·미확정·기각·원시 진단, 구조 지표가 있으면 그 수), 한계 문구, 분석 범위 수(모르면 "미확정"), 화면 전용 비교 결과, 모델 호출 목록(역할·모델·OpenCode 버전·결과·지침 digest 앞부분), snapshot ID를 보인다. 모델이 본 파일 수를 계산할 수 없는 부분 실행은 미확인으로 표시한다.
- 발견은 category별로 묶고, 항목이 둘 이상이면 제목·영향·영역 이름으로 거른다. 각 항목은 펼침 요소이며 제목 옆에 검증 상태·심각도·비교 상태·현재 판정을 글자로 붙인다. 펼치면 발생 조건, 심각도 이유, 영향, 개선, 확인 방법, 주장(관찰·추론 구분)과 근거 링크, 근거 목록(반대 근거 표시, snapshot 경로와 줄 범위 또는 산출물 ID), 한계를 보인다. 근거 링크는 같은 발견 안의 근거 위치로 이동한다.
- 현재 판정이 `accepted_risk`·`false_positive`·`suppressed`인 항목은 목록에서만 빼고 체크박스로 다시 보이게 한다. 집계에는 남는다. 서버 집계는 `fixed_by_user`도 숨김으로 세지만 목록은 이 항목을 계속 보여준다.
- task 표와 원시 관찰 목록을 펼쳐 볼 수 있다.
- JSON·Markdown·HTML 내보내기 링크는 표시 중인 리비전에 고정된다. "코드 포함" 체크박스는 기본 꺼짐이다. Markdown·HTML은 요약 보기이고 "Markdown 전체"·"HTML 전체"가 `detail=full`이다.

### 비교와 판정 화면

보고서 패널은 표시 중인 리비전의 비교를 따로 읽는다. 실패해도 보고서는 그대로 보이고 다시 읽기 버튼이 나온다. 그사이 새 리비전이 게시되어도 표시 중인 리비전의 비교를 읽는다.

- 기준 run·리비전·게시 시각·선택 방식, 비교 불가 사유, run 차이 목록, 상태별 수, 식별 없는 발견 수, 기준에만 있던 발견(상태·생산자·분석기·규칙·기준 위치·이유·이동 후보 수), 판정 반영 집계(게시 시점/현재의 전체·조치·숨김 수)를 보인다.
- 발견을 펼치면 비교 상태와 새 항목의 이유·변경 항목·모호성·이동 후보, 게시 시점 판정과 현재 판정·적용 여부 설명, 최신 판정의 사유·version·기록자·시각·만료·경로·판정한 run과 리비전을 보인다.
- 판정 양식(판정 선택, 필수 사유, `open`이 아닐 때 선택 만료일·선택 경로)은 `can_review`일 때만 나온다. viewer에게는 안내 문구만 보인다. native 양식 요소라 키보드로 조작한다. 만료일은 그날 현지 시각 끝으로 보내고, 과거 날짜와 빈 사유는 보내기 전에 막는다.
- 같은 요청(본문과 전제 조건)에는 같은 Idempotency-Key를 다시 쓴다. 409 `CONFLICT`면 최신 비교를 다시 읽어 다음 시도가 새 version을 쓰게 한다. 결과를 알 수 없는 오류면 다시 보내도 중복되지 않는다고 안내한다.
- 판정 화면은 최신 판정만 보여준다. 감사 이력 목록 화면은 없고 API로 이력을 읽는다.

### 예약 화면

프로젝트 구성원에게 예약 목록(cron, 시간대, 겹침·놓침 정책, 활성 여부, 다음 실행 시각, 마지막·대기 occurrence와 사유)을 보여준다. 다음 실행 시각은 서버 값이다. `can_manage`이면 등록(브라우저 시간대가 기본값, 같은 본문 재시도는 같은 Idempotency-Key)과 삭제(`If-Match`)를 제공한다. 예약 수정은 API만 있고 화면은 없다. 예약의 의미는 [API](API.md)에 있다.

### 환경 진단 화면

`codeye doctor --format json`이 만든 파일(최대 1 MiB)을 브라우저 안에서만 읽어 호스트와 검사 결과 표를 보여준다. 업로드·저장하지 않는다. 검증기는 빌드 시 스키마에서 생성하며 브라우저에서 `eval`·`new Function`을 쓰지 않는다.

## 설계 계약(미구현)

다음은 화면·보고서 설계 목표이며 현재 구현되어 있지 않다.

- **화면 구성.** 등록 연결 진단(브랜치 조회 포함), 프로젝트 개요, 영역별 분석, 운영자 노드·풀·모델·도구 설정 화면. 풀·노드 관리는 지금 운영자 CLI를 쓴다. 등록의 설명·첨부·관심 영역·테스트는 접힌 선택 항목으로 두며, 자동 탐색 결과를 고칠 수 있게 하되 매 실행 승인 폼을 강제하지 않는다.
- **결함 영역별 표시.** 정확성, 오류·취소, 동시성, 자원 수명, 데이터·인터페이스 계약, 소스 보안, 구조·유지보수성, 성능 구조, 테스트 공백 축마다 발견뿐 아니라 수행한 검사·검토와 제한을 표시하고 수행하지 않은 영역을 녹색으로 칠하지 않는다. 지금 보고서는 [분석 종류별 절](#분석-종류별-절)로 나누고 발견을 category로 묶을 뿐이다. 종합의 신규·지속 항목 수도 저장 문서에는 없고 화면의 비교 결과에서만 읽는다.
- **코드 뷰.** 기본 코드 뷰는 분석한 snapshot이며 현재 브랜치의 최신 파일로 자동 연결하지 않는다. 파일·행·심볼·related flow를 연결하고 원시 진단과 해석을 펼친다. snapshot이 만료되면 source unavailable을 표시하고 다른 버전 소스로 대체하지 않는다. 흐름이 도구가 생성한 경로인지 모델이 추정한 경로인지 표시한다. 비밀정보 발견은 마스킹된 위치와 조치만 보여준다.
- **데이터 기반 대체 서술.** 모델 서술 생성이 실패해도 데이터만으로 보고서를 열람할 수 있게 한다. 현재 보고서는 처음부터 모델 서술 생성 없이 구조화 데이터로 만든다.
- **판정 일괄 적용, 판정 감사 이력 화면, 이동 후보 판정 이전, 첫 발견 시각.**

## 접근성

구현된 것:

- 색상 외에 글자·기호로 상태를 구분한다(NFR-005).
- 모든 조작은 키보드로 할 수 있다. 펼침은 실제 버튼(`aria-expanded`)이나 `details` 요소를 쓰고, 양식은 native 요소를 쓴다. 본문 바로가기 링크를 둔다.
- 상태 변화는 `role="status"`·`aria-live`로, 오류는 `role="alert"`로 알린다.
- 긴 코드와 표는 가로 스크롤 영역에 둔다. 보고서의 발견은 검색어로 거를 수 있다. 분석 종류별 절은 `details` 요소이고 종합의 절 버튼은 실제 버튼이다.
- 조회 실패에는 재시도 경로를 둔다.
- 화면과 HTML 내보내기는 외부 CDN·폰트를 쓰지 않는다. 첫 화면에는 차트 대신 실행 상태와 행동 우선순위를 둔다.

요구(검증하지 않았거나 미구현):

- 적절한 대비. 대비를 측정하지 않았다.
- 큰 표와 목록의 페이지 처리. 프로젝트 목록만 "더 보기" cursor 방식이고 보고서의 표는 나누지 않는다.
- 그래프를 넣을 때는 표 대안을 둔다. 지금 화면에는 그래프가 없다.

## 사용자 수용 기준

예시 보고서를 처음 보는 개발자가 짧게 읽고 핵심 위험, 근거, 미확인 범위, 수정·검증 방법을 찾을 수 있는지로 평가한다. 읽기 시간을 실측하기 전에는 보장치로 쓰지 않는다. 보고서 장식이나 경고 수를 전달 품질로 평가하지 않는다. 출력 구조의 기준 예시는 [`examples/report.md`](../examples/report.md)(합성 예시)다.

관련 요구사항·수용 시험: REQ-028, REQ-029, NFR-005, NFR-006(판정 감사 기록 일부), TC-14, TC-31(코드 미포함 내보내기에 식별 없음), TC-80~TC-84(게시·판정 규칙·재생성 동일성·동시 게시·권한과 escaping), TC-90~TC-92(근거 보고서와 화면), TC-98(대표 실행과 단계 표시), TC-130~TC-134(분석 종류별 절). 요구사항 정의는 [요구사항](REQUIREMENTS.md), 수용 시험 정의는 [시험](TESTING.md)에 있다.

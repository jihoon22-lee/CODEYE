# 보고서와 화면

종합에서 결과를 읽는 순서는 [보고서 패널](#보고서-패널)에 있다. [분석 종류별 절](#분석-종류별-절), [테스트 결과](#테스트-결과), [run 비교](#run-비교), [내보내기](#markdown과-html-렌더링)에서 각 결과의 표시 규칙과 근거를 확인한다. HTTP 계약은 [API](API.md), 분석 입력과 관찰은 [검토](REVIEW.md)와 [분석기](ANALYZERS.md), 실행·완료는 [실행](EXECUTION.md)을 따른다.

## 보고서 문서

보고서는 run의 리비전마다 저장하는 구조화 JSON 한 벌이다. 형식의 기준은 [보고서 스키마](../schemas/report.schema.json)와 [발견 스키마](../schemas/finding.schema.json)이고 생성 코드는 [`internal/report`](../internal/report/build.go)다.

| 항목 | 현재 값과 규칙 |
|---|---|
| `schema_version` | `0.1`. 새 필드는 모두 선택 필드로 추가했다. |
| `demo` | 제품이 게시한 보고서는 항상 `false`다. [`examples/report.json`](../examples/report.json)은 현재 builder가 실제 worker 파이프라인과 대본 모델 응답으로 만든 합성 예시(`demo: true`)이고, [`examples/report-go-vet.json`](../examples/report-go-vet.json)은 builder 1 시절의 실제 코드 미포함 내보내기다(분석 종류별 절 없음). |
| `builder.id` / `builder.version` | `codeye-report` / `22`. 생성 시점의 버전을 저장한다. 이전 보고서에 없는 필드는 비워 두고, 화면은 저장된 절과 근거만 표시한다. |
| `builder.input_digest` | run·snapshot·attempt 사실, task 행, 결과 manifest, 산출물 행, 게시 시점 판정 집합과 builder ID·버전·스키마 버전을 SHA-256으로 계산한다. 프로젝트 정보가 있으면 프로젝트명·소스 경로·분석 시작 및 종료 시각을 포함한다. PR 실행에는 고정한 PR 정보도 포함한다. 보고서 ID·리비전·보고서 생성 시각은 제외한다. |
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
| 5 | 구조·중복 지표 | `metrics` | 복잡도·중복 분석기 작업과 측정값(이름·단위·임계값·최댓값·관찰 ID), Go 내부 패키지 의존 관계 |
| 6 | 비밀정보 | `secrets` | 비밀정보 분석기 task, 탐지마다 규칙·위치·근거 상태·관찰 ID·도구 발견 ID, 모델 입력에서 비밀값을 걸렀는지(`model_input`) |
| 7 | 코드 분석(모델 검토) | `code_review` | 지침 팩, 역할별 결과, 호출 수·수락 수·요청 바이트, 모델에 노출한 파일 수, 해석 한계, 모델 후보 발견 ID, slice가 계획된 실행이면 [모듈별 검토 장부](#모듈별-검토-장부)(`ledger`), 분류 slot이 계획된 실행이면 [도구 진단 분류](#도구-진단-분류)(`diagnostic_triage`, builder 22부터) |
| 8 | 분석 공백 | `gaps` | 분석하지 못했거나 일부만 분석한 범위(`section`, `code`, 선택 `subject`·`detail`). 비어 있으면 생략한다. |

"이전 실행과 비교"는 저장 문서의 절이 아니다. 비교는 요청마다 계산하므로([run 비교](#run-비교)) 웹 화면만 종합 아래에 그 결과를 따로 보여주고, 내보내기에는 넣지 않는다.

정적 분석 화면과 HTML·Markdown은 알려진 분석기·규칙에 한 문장 설명을 붙인다. 같은 분석기·규칙의 위치는 한 행에서 읽으며, 확인한 위치와 원시 진단을 상세 근거에서 볼 수 있다. 사용자가 정의한 의존 규칙은 규칙 이름과 해당 분석 방식으로 설명한다. 코드 미포함 내보내기에도 규칙 설명과 분석기 출처가 표시된다.

정적 진단의 `related_locations`에는 도구가 보고한 관련 위치와 메모가 붙는다. 가정과 분기 위치를 주 진단에서 읽으며 진단 수는 한 건이다. 구조·중복 절의 `go_package_dependencies`에는 확인된 패키지·테스트 소비자와 의존 관계, 운영 코드의 순환을 뒷받침하는 import 줄이 있다. 적용 패키지와 사용한 빌드 구성을 함께 읽는다. [Go 패키지 관계](ANALYZERS.md#go-내부-패키지-의존-관계)

**분할.** 모든 관찰은 정적 분석·지표·비밀정보 중 정확히 한 절에 한 번 들어간다. 진단은 정적 분석 행, 지표 관찰은 지표 묶음, 비밀정보 분석기의 관찰은 탐지 목록이다. 모든 발견은 정적 분석(도구 발견)과 코드 분석(모델 후보) 중 정확히 한 곳에 들어간다. 비밀정보 탐지의 도구 발견은 정적 분석 쪽에 두고 탐지는 그 발견을 `finding_id`로 가리킨다. 두 절의 발견 집계를 같은 함수(`ComputeSummary`)로 계산해 더하면 `summary`와 같아야 한다. 정적 분석 행의 규칙별 수 합은 그 행의 관찰 수와 같고, 지표 묶음의 최댓값·임계값은 관찰에서 다시 계산한 값과 같아야 한다. 종합 블록은 다른 절에서 순수 함수로 다시 계산한 값과 같아야 한다. 검증(`ValidateReport`)이 저장 시와 조회 시에 이 규칙을 모두 확인한다.

**분석기 목적.** 관찰이 어느 절에 들어갈지는 분석기 envelope의 `analyzer.purpose`(`metrics`, `secrets`, 없으면 진단)로 정한다([분석기](ANALYZERS.md#분석기-목적)). 생태계도 수락한 envelope의 `analyzer.ecosystem`에서 읽는다(go vet 문서는 `go`). 수락한 결과가 없는 task 행은 생태계를 모르므로 생태계 없는 묶음에 둔다.

**단계 상태.** 절의 `stage`는 진행 화면과 같은 분류기(`progress.TaskStage`)로 task 행의 단계 열에서 계산한다. 값은 진행 단계 상태(`plan_pending`, `no_tasks`, `waiting`, `running`, `succeeded`, `skipped`, `failed`, `cancelled`, `partial`)와 같고, 단계별 상태 수(`counts`)·사유 코드(`reasons`)·task 수·성공 수를 함께 싣는다. 단계 열이 없는 task가 하나라도 있으면 `not_recorded`("세부 단계 미기록")다. 지표·비밀정보 절은 정적 분석 단계 중 그 목적의 task만 센다. 미실행·미지원·실패 task는 "실행하지 않음"이나 해당 상태로 보이고 0건으로 읽히지 않는다.

**모델 해석.** 환경·문서·코드 분석 절의 모델 문장은 단계 연결 검사를 통과해 수락된 역할 기록에서 한 번씩만 가져온다. 역할이 수행되지 않았으면(`model.state: not_performed`와 사유 코드) 모델 문장은 하나도 없다. 문장은 그 절의 JSON 근거 목록에 있는 근거 ID만 인용할 수 있다. 환경과 문서 분석은 각 항목에서 파일과 줄 범위를 보여 주고, 근거 ID와 blob SHA-256은 JSON에 보존한다. 화면과 내보내기는 모델 해석을 플랫폼 판정과 구분한다. 모델 해석은 상태·수·판정의 원천이 아니다.

**고정 문구.** 절의 제목·상태·수 이름·먼저 볼 항목 문장은 구조화 값에서 만든 템플릿이다. Go 렌더러는 [`messages_ko.go`](../internal/report/messages_ko.go), 웹은 [`report-ko.ts`](../web/src/locales/report-ko.ts)에 있으며 모르는 코드는 원문 그대로 보인다.

**코드 미포함.** projection은 모델 문장을 모두 생략 문구로 바꾸고, 모듈 요구의 제약 문자열은 버전 제약 형식일 때만 남긴다. 경로·줄 번호·코드·수·blob hash는 남는다. 비밀정보 탐지는 처음부터 값과 그 일부·hash를 싣지 않는다.

예시는 [`examples/report.json`](../examples/report.json)과 [`examples/report.md`](../examples/report.md)이다. `fixtures/modules/input`을 대상으로 실제 worker 파이프라인과 대본 모델 응답으로 만든 합성 예시(`demo: true`)이며, 생성 시험은 [`report_example_test.go`](../internal/worker/review/report_example_test.go)다.

### 모듈별 검토 장부

builder 6부터 보고서는 [모듈 단위 검토 분할](REVIEW.md#모듈-단위-검토-분할)의 산출물을 읽는다. slice 기록(review 기록 0.3)은 자기 slice task에 붙은 것만 받고, 검토 범위 산출물(`application/vnd.codeye.review-scope+json` 0.1~0.3)은 `review.scope`에 붙은 것만 받는다. 둘 다 attempt·snapshot manifest와 대조한다. 범위 산출물 0.3에서는 overview와 slice의 실제 발췌 줄도 대조한다. slice verify는 자기 slice investigate의 후보만 이어받는다.

- **발견.** slice 후보는 `review_sNNN_<후보 ID>`(예: `review_s002_investigate_1`)로 코드 분석 절의 발견 분할에 들어간다. 판정은 같은 slice의 verify 기록에서만 온다. 계획되지 않은 slice 키의 발견은 검증에서 거부한다.
- **호출과 노출.** `review_calls[]`는 검토 호출과 해당 작업 키를 최대 2,013개까지 싣는다. `llm_exposed_files`는 모델에 보낸 서로 다른 경로 수이며 후속 조사와 최종 문서 대조의 요청도 포함한다. `ai_review`는 필수 작업 상태를 집계한다.
- **역할과 장부.** 코드 분석 절의 `roles`는 전체 역할과 계획된 선택 작업 `review.contract`를 싣는다. `review.scope`와 slice task는 장부에 있다. 최종 문서 대조가 실제로 보낸 호출과 요청 바이트는 장부의 전체 사용량에 포함된다. 모듈 행은 전체 검토와 slice에 계획된 발췌 범위의 검토를 센다.

`code_review.ledger`는 `review.scope`가 계획된 실행에 있다. 줄 범위를 기록하는 worker는 작은 프로젝트에서도 대상 소스가 있으면 이 작업을 계획한다.

| 필드 | 뜻 |
|---|---|
| `scope` | `review.scope` task와 결과. 범위 산출물을 받았으면 `accepted`, 아니면 작업 상태·사유(예: `skipped` / `model_source_not_approved`) |
| `max_calls`, `max_request_bytes` | 범위 산출물이 기록한 attempt 호출 상한과 요청 크기 상한. 산출물이 없으면 `null` |
| `extended_coverage` | 확장 코드 검토를 요청했고 줄 장부가 있는 경우 `true`. 일반 모드에서는 생략 |
| `calls_used` | slice 앞 호출 수(범위 산출물의 `calls_before_slices`, attempt 호출 기록에서 셈. 기록 없이 실패한 전체 역할 호출도 들어간다)와 slice 기록의 호출 합. 범위 산출물이 없으면 전체 역할 기록에서 센다. 호출했을 수 있는 검토 task에 쓸 수 있는 기록이 없으면 `null` |
| `request_bytes_sent` | 모든 검토 기록의 요청 크기 합(프롬프트 바이트). 같은 조건에서 `null`이며, 값이 있으면 `code_review.request_bytes`와 같다 |
| `status` | `complete`(줄 장부의 모든 대상 파일의 전체 원문 줄을 수락된 조사 응답에서 다룸. 이전 장부는 파일을 보냈고 확인 역할이 끝난 경우), `overview_only`(성공한 slice investigate가 없다), 그 밖은 `partial` |
| `modules[]` | 범위 산출물의 모듈마다 한 행(모듈 ID 순) |

모듈 행은 `module_id`, `discovered`(모듈 탐색이 같은 ID를 찾았는지), `files_eligible`과 그 파일을 한 번씩 나눈 수 `files_sent_to_model`·`files_withheld`(비밀값 탐지)·`files_unreadable`·`files_budget_exhausted`(호출 상한으로 거절된 slot의 주요 파일)·`files_not_planned`(slot이 남지 않은 주요 파일)·`files_secondary_not_reached`(builder 8: 거절됐거나 slot이 남지 않은 부차 파일)·`files_not_sent`(그 밖: overview가 보내지 못했거나 slot이 실패·생략됨)를 싣는다. 일곱 수의 합은 `files_eligible`이다. builder 8은 schema 0.2 범위 산출물에서 `class`(`primary`·`secondary`, 모든 행에 함께 있다), `files_secondary`(부차 경로·생성 파일 수), `files_secondary_sent`(그중 보낸 파일)도 싣고 0인 수는 생략한다. 부차 모듈의 대상 파일은 모두 부차다. 장부의 `primary_slots_budget_exhausted`는 호출 상한으로 거절된 주요 파일 slot 수다(보통 slot마다 호출 2회). 주요 파일의 `files_budget_exhausted`가 있으면 반드시 있고, 분류가 없는 장부에는 없다. 파일은 수락된 검토 기록 하나라도 그 파일의 발췌를 보냈으면 보낸 것으로 센다. verify가 반대 근거로 보낸 시험 파일 발췌도 들어가므로, 상한으로 거절된 slice의 파일도 보낸 쪽에 들 수 있다. `bytes_sent`는 실제 요청의 발췌가 아니라 보낸 파일의 범위 산출물 계획 바이트(첫 50줄, 파일당 2,400바이트 이하)의 합이고, `files_truncated`는 발췌가 파일 끝까지 닿지 않은 보낸 파일, `files_with_completed_review`는 보낸 파일 중 검증 역할 기록까지 수락된 파일(overview 파일은 전체 verify, slice 파일은 그 slice의 verify)이다. `slices[]`는 그 모듈 파일을 가진 slice task마다 `task_key`·`status`·`reason`이며 task 행과 같아야 한다. 분류가 있는 장부의 표(세 형식과 웹)는 모듈 뒤에 분류 열, 미계획 뒤에 "부차 경로·예산 밖"(`files_secondary_not_reached`) 열을 더해 행의 합이 대상 수와 맞게 보인다. 분류가 없는 장부의 표는 그대로다.

**파일과 줄.** builder 16은 범위 산출물 0.3에서 `files[]`를 추가한다. `total_lines`는 스냅샷 원문 줄 수이며, 읽을 수 없거나 보류된 파일은 대신 `reason`을 싣는다. `sent_lines`는 기록된 조사 요청에 들어간 줄의 합집합, `reviewed_lines`는 응답이 수락된 조사에 들어간 줄의 합집합이다. `remaining[]`은 수락 범위 밖의 줄과 이유를 원래 줄 번호로 기록한다. `investigation`은 응답 수락, 수락되지 않은 요청, 요청 여부 미확인, 미요청을 구분한다.

모듈 행의 `files_requested`와 `files_accepted`는 각각 요청과 수락에 코드 발췌가 하나 이상 있는 파일을 센다. `files_sent_to_model`은 이 장부에서 `files_requested`와 같다. `bytes_sent`는 범위 산출물에서 크기를 대조할 수 있는 발췌의 합이다. `files_with_completed_review`는 수락된 조사 파일 중 실제 검증 결과에서 후보의 긍정·반대 근거가 된 파일을 센다. 후보가 없어 확인 역할이 모델 호출 없이 끝났다면 0이다. 모든 줄이 수락 범위에 들어갔더라도 줄마다의 해석이나 결함 유무는 측정할 수 없다.

**종합과 공백.** 장부에 모듈 행이 있으면 종합의 코드 분석 줄에 `files_sent_to_model`과 `files_not_sent_to_model`(대상 파일 − 보낸 파일)이 더해지고, 보내지 못한 파일이 있으면 먼저 볼 항목 `review_scope_not_sent`가 생긴다. builder 8에서 부차 파일이 있으면 `primary_modules`·`primary_files`·`primary_files_sent_to_model`·`secondary_files`·`secondary_files_not_reached`가 더해지고, 먼저 볼 항목은 보내지 못한 주요 파일만 세는 `review_primary_not_sent`다. 분석 공백에는 모듈마다 `review_scope_budget_exhausted`·`review_scope_not_planned`·`review_scope_secondary_not_reached`·`review_scope_withheld`·`review_scope_unreadable`·`review_scope_not_sent`(대상 모듈 ID), 범위 산출물을 받지 못했으면 `review_scope_<결과>`(`review.scope`, 사유)가 들어간다.

**검증.** 장부는 builder 6 이상이고 `review.scope` task가 있을 때만 있어야 하며, 있으면 반드시 있어야 한다. 분류는 builder 8 이상에서 모든 행에 함께 있거나 함께 없고, 부차 수는 대상·보낸 파일을 넘지 않는다. 모듈 행의 합, 보낸 파일 이하인 잘림·검증 완료 수, 상한 초과 파일이 있으면 그런 slice가 있을 것, 상태가 행과 task 행에서 다시 계산한 값과 같을 것, `calls_used`가 `review_calls` 수 이상일 것, 호출의 `task_key`가 같은 역할의 계획된 task일 것을 저장 시와 조회 시에 확인한다. 장부는 모듈 ID·task 키·코드·수만 담으므로 코드 미포함 projection에서도 그대로다. 세 형식과 웹 화면은 장부의 같은 수를 보인다.

**비밀값 선별 상태.** builder 6부터 비밀정보 절의 `model_input`에 `screened`(완료된 탐지로 걸렀고 뺀 파일 없음)가 있다. builder 5는 이 경우를 `not_screened`로 읽었다. 비밀값 작업이 실행됐지만 결과를 쓸 수 없어(실패·부분 결과) 미선별로 검토한 실행도 `not_screened`다. 그 결과가 보고한 파일을 뺐어도(`secret_files_withheld`) `withheld`보다 `not_screened`가 앞선다. 나머지 입력은 거르지 않았기 때문이다. Markdown과 웹은 이때 쓸 수 없던 작업과 상태·사유를 적는다(예시 `internal/report/testdata/report-screen-unusable.json`). 이전 worker가 검토를 건너뛴 실행(`secret_screen_failed`)은 그대로 `screen_failed`로 읽는다.

예시는 [`examples/report-slices.json`](../examples/report-slices.json)과 [`examples/report-slices.md`](../examples/report-slices.md)이다. `fixtures/modules/slices/input`을 호출 상한 15로 실행해(작업마다 대본 응답 1회, 모델 호출 없음) 계획한 slot 9개 중 `review.scope`가 6개를 채우고, 그중 3개가 승인되고 3개가 `budget_exhausted`로, 빈 slot 3개가 `no_review_scope`로 끝난 합성 예시이며, builder 7이 쓴 같은 예시는 `internal/report/testdata/report-slices-builder7.json`으로 남아 계속 읽히는지 시험한다. 이 예시와 [`examples/report.md`](../examples/report.md)의 Markdown은 요약 보기이며, 생성 시험은 [`report_slices_example_test.go`](../internal/worker/review/report_slices_example_test.go)다.

### 도구 진단 분류

builder 22부터 분류 slot(`review.triage.tNNN`·`review.verify.tNNN`, [검토](REVIEW.md#도구-진단-분류))이 계획된 보고서의 코드 분석 절은 `diagnostic_triage`를 싣는다. 이전 리비전에는 없고 화면은 이전처럼 분석기·규칙별 도구 진단만 보인다. [생성과 검증](../internal/report/review_triage.go)

| 필드 | 내용 |
|---|---|
| `tasks` | 분류·검증 task마다 상태와 결과(`accepted`·`skipped`·`not_succeeded` 등). task 행과 같아야 한다 |
| `diagnostics` | 보고서의 대상 진단 수: 정적 분석 절의 진단 분석기 행(지표·비밀정보 제외)의 관찰 중 억제되지 않은 것 |
| `triaged` | 그중 수락되고 계보 검사를 통과한 분류 기록의 분류된 묶음에 든 진단 수 |
| `interpreted` | 분류되지 않았고 해석 역할 후보의 발견이 다룬 진단 수 |
| `groups[]` | 분류된 묶음: `task_key`, `group_id`, `classification`(`problem`·`harmless`·`unclear`), `bucket`, `observation_ids`, `cross_tool`, 모델의 `reason`(무해·판단 불가), 인용한 코드 위치 `locations`, 문제 묶음의 발견 `finding_id` |
| `limitations` | 수락된 분류 기록의 공개 한계 코드(예: `diagnostics_outside_triage_slots`, `diagnostic_groups_not_triaged`) |

- 수는 모두 보고서 자신의 관찰·발견과 수락된 기록의 구조화 필드에서 센다. 모델이 쓴 숫자는 쓰지 않는다. `diagnostics - triaged - interpreted`는 분류하지 않은 진단이며 깨끗하다는 뜻이 아니다. slot이 모두 생략·상한·실패·계보 거부여도 이 차이로 남는다.
- `bucket`은 분류와 발견에서 정한다. `harmless`는 무해, 검증이 `refuted`로 판정한 문제는 `refuted`, 그 밖의 문제와 `unclear`는 `attention`이다. 검증은 이 규칙을 다시 계산해 대조한다.
- 문제 묶음은 발견 `review_tNNN_triage_N`(코드 분석 절)이 되고 그 slot 검증의 판정을 싣는다. 도구 발견은 바뀌지 않으며 묶음의 관찰마다 도구 발견이 그대로 남는다. 두 도구가 함께 든 묶음(`cross_tool`)도 도구 발견은 도구마다 남는다(TC-102).
- 코드 미포함 내보내기는 `reason`을 생략 문구로 바꾸고 위치·수·코드는 남긴다.
- 종합의 코드 분석 수에 `diagnostics`(분류 대상 도구 진단)와 `diagnostics_triaged`(그중 모델이 답한 진단, 판단 보류 포함)가 더해진다. 도구 진단 접힘의 제목은 `problem`·`harmless`로 판정한 진단과 `unclear`로 판단을 보류한 진단을 따로 센다.

### 의존성 목록

builder 7부터 환경 절은 `deps.*` task마다 한 행(`environment.dependencies[]`, key 순서)을 싣는다([의존성 준비](ARCHITECTURE.md#의존성-준비)). 의존성 task가 계획되지 않은 run에는 필드가 없고 보고서는 builder 6과 같은 모양이다.

- 행 필드: `task_key`, `task_status`, `task_reason`, 기록 수락 여부 `result`(`accepted`, `result_rejected`, `result_missing`), 그리고 수락한 기록의 `module`·`ecosystem`·`path`·`manager`·`manager_version`·`state`·`reason`·`pinned`·`lockfile`·`declared`·`prepared`·`failed`·`cache_kind`·`failed_items`·`limitations`. 기록을 수락하지 못했으면 기록 필드는 null이고 task 상태·사유만 보인다.
- 기록은 `DependenciesDecoder`가 attempt·snapshot·task와 대조한다. 기록의 `task_status`가 task 행과 다르면 거부한다. 관찰·발견·도구 coverage에는 더하지 않는다.
- 종합의 환경 절 수에 `dependencies`(의존성 task 수)와 `dependencies_prepared`(성공한 task 수)가 더해진다. 의존성 task가 없으면 두 수는 없다.
- 공백(`gaps`, 절 `environment`):
  - `dependencies_not_prepared`: 성공하지 않은 의존성 task. 받을 것이 없어 건너뛴 task는 넣지 않는다. `subject`는 task key, `detail`은 사유다.
  - `dependencies_not_fetched`: 그 생태계의 의존성 task가 없는데(의존성 받기가 꺼져 있었다) 분석기가 의존성을 풀지 못했다. 분석기 task가 `dependency_unavailable`로 끝났거나(`detail`은 그 사유), Python 타입 검사기가 성공했지만 coverage에 `import_unresolved` 단위와 `third_party_packages_not_installed`가 있다(`detail` `imports_unresolved`). `subject`는 분석기 task key다. Python 조건은 아직 출시하지 않은 builder 7 안에서 더했으므로 builder 버전은 그대로다.
  - `dependency_versions_not_pinned`: lockfile이 버전을 고정하지 않아 받는 도구가 버전을 골랐다. 받은 것이 있는 기록(`prepared`·`partial`)에만 단다. 받기 전에 건너뛴 기록은 `pinned`가 false여도 달지 않는다.
- 판정 규칙은 바뀌지 않는다. 의존성 task는 필수가 아니므로 실패해도 그것만으로 보고서가 `partial`이 되지 않는다. 의존성이 없어 분석기가 실패하면 그 분석기 task가 판정에 반영된다.
- Markdown·HTML은 환경 절에 모듈·받는 도구·결과·받은 수·사유·한계 표를 보인다.


### pull request 보고서

builder 9부터 pull request run([pull request 분석](GITHUB.md#pull-request-분석))의 보고서는 `pull_request` 절을 싣는다. 다른 run에는 이 필드가 없고 input digest도 이전과 같다. 예시는 [`examples/report-pull-request.json`](../examples/report-pull-request.json)이다(합성 go vet 사실에 pull request를 고정한 것).

- 고정값: `number`, `url`, `title`, `repository`(HOST/OWNER/REPO), `head_repository`(fork면 다르다), `base_ref`·`head_ref`, `base_sha`·`head_sha`, `resolved_at`, `head_analyzed`(snapshot의 commit이 고정한 head와 같은지).
- 파일 수: `changed_files`(GitHub의 수), `files_listed`(목록에 온 수, `files_truncated`), `files_removed`, `review.reviewable`(head에 있는 변경 파일), `review.in_snapshot`(snapshot이 읽을 수 있는 파일로 가진 것. 나머지는 비밀정보·생성·cache 경로, 링크 규칙으로 빠졌다), `review.sent_to_model`(검토 기록이 보낸 것으로 보여 주는 변경 파일. 검토 기록의 호출 중 실제로 요청을 보낸 것(request digest)이 없으면(모델 없음, 소스 전송 끔) null이고 요약은 "0개를 보냈다" 대신 정적 분석만 거쳤다고 쓴다), 보내지 못한 경로 `review.not_sent`(최대 50개)와 `review.not_sent_total`.
- 나누기: 발견과 도구 진단(지표·억제 진단 제외)을 위치로 나눈다. `changed_lines`(pull request가 더하거나 바꾼 head 쪽 줄과 겹친다), `changed_file`(GitHub이 patch를 주지 않은 변경 파일. 파일 단위로 변경에 넣는다), `changed_file_other_lines`(변경 파일의 다른 줄, 기존 코드), `elsewhere`(변경하지 않은 파일). `line_basis`는 `lines`, `files`(patch가 하나도 없다), `mixed`이고 `files_without_lines`가 patch 없는 파일 수다. 변경 부분의 항목은 `items`(종류, ID, 위치, 경로·줄)에 있다.
- 요약: 종합 절 첫 문단 뒤에 pull request 번호와 브랜치·SHA, 모델에 보낸 변경 파일 수, 검토하지 않은 삭제 파일, snapshot에 없는 변경 파일, 목록이 잘렸는지, 나눈 기준, 변경 부분과 기존 부분의 수를 적고, 변경 파일을 먼저 검토했으며 저장소의 나머지는 같은 깊이로 검토하지 않았을 수 있다고 적는다. 한계 노트 `pull_request_focus`가 같은 뜻이다.
- 코드 미포함 내보내기에도 절이 그대로 있다(경로·줄·수·SHA만 담는다). 비교는 이 절을 읽지 않는다. 기본 기준이 같은 pull request의 이전 run이기 때문이다([기본 기준](#기본-기준)).

## 상태와 판정

상태와 판정은 구조화된 사실로만 정한다. 모델 서술은 원천이 아니다.

| `status` | 조건(위에서부터 처음 맞는 것) |
|---|---|
| `cancelled` | run이 취소로 끝났다. |
| `failed` | run이 실패했거나, 수락된 결과 manifest와 함께 성공하고 거부되지 않은 task가 하나도 없다. |
| `partial` | 필수 task 미성공(정적 분석이 모두 실패하고 탐색만 성공한 실행도 여기다), 성공한 필수 task의 산출물 누락, 산출물 거부, 정적 분석 공백(`static_analysis_gap`) 또는 미계획(`static_analysis_not_planned`), 정리 미확인, `with-tests`의 테스트 결과가 `passed`가 아닌 경우, 또는 AI 검토가 `performed`가 아니다. |
| `completed` | 위 어느 것도 아니다. |

| `quality_verdict` | 조건(처음 맞는 것) |
|---|---|
| `attention_required` | 조치 항목이 있거나, 문서와 구현 대조에서 불일치·부분 일치가 확인됐다. 문서 대조 결과는 발견 건수와 별도로 센다. 확인할 항목이 없고 근거가 부족한 문서 계약만 남으면 판정을 보류한다. |
| `inconclusive` | 상태가 `completed`가 아니거나, AI 검토가 `performed`가 아니거나, 미확정 후보나 위치 미확인 진단이 남아 있거나, 형식 복구 뒤 근거 위치를 확정하지 못해 뺀 모델 후보·해석 가설이 있다(한계 `review_candidate_omitted`·`interpretation_items_omitted`). |
| `no_actionable_findings` | 확인한 범위에서 조치할 항목을 찾지 못했다. |

`completed` 보고서는 필수 task가 모두 성공하고 AI 검토가 `performed`여야 한다. 검증이 이 규칙과 판정 규칙을 저장 시와 조회 시에 다시 확인한다. 검토가 수행되지 않은 보고서의 builder 문구에는 "검토 완료", "문제 없음" 같은 완료 주장을 넣지 못한다. 검토 기록이 없거나 필수 도구 결과가 누락되면 `no_actionable_findings`를 게시할 수 없다.

### AI 검토 상태

`coverage.ai_review.status`는 필수 검토 task가 실제로 수락됐는지를 나타낸다. task 행이 성공하고 등록된 decoder가 그 결과 바이트를 수락해야 수락으로 센다. task가 성공해도 결과를 수락하지 못하면 미수행이다. 예외는 일부 후보만 판정한 verify다(생성기 21부터, A59). verify task가 판정하지 못한 후보 때문에만 실패했고(`review_output_incomplete`, `review_input_incomplete`, 전체와 slice) 그 수락 기록이 계보 검사를 통과하면 결과는 `accepted_incomplete`이고 수락으로 센다. 그 판정은 발견에 실리고 판정하지 못한 후보만 사유와 함께 `unreviewed`다. task는 실패로 남으므로 보고서는 `partial`이고, 한계 `review_verification_incomplete`와 분석 공백 `role_accepted_incomplete`(절 `code_review`)가 남는다. 생성기 20 이하의 보고서가 `accepted_incomplete`를 가지거나 그 결과와 한계가 어긋나면 검증 실패다.

| 상태 | 뜻 |
|---|---|
| `performed` | 필수 검토 task가 하나 이상이고 모두 수락됐다. |
| `partial` | 일부만 수락됐다(`reason: required_review_incomplete`). |
| `not_performed` | 필수 검토 task가 계획되지 않았거나(`review_tasks_not_planned`) 수락된 것이 없다(`review_results_not_accepted`). |
| `not_requested` | 스키마 enum에 있으나 현재 builder는 쓰지 않는다. |

필수 역할은 계획에서 정한다. `required_for_verdict`인 `review.*` task가 필수이고, 나머지는 `optional_tasks`에 결과·사유와 함께 적으며 수행으로 세지 않는다. 검토 task가 없는 계획은 필수 역할이 없고 AI 검토는 `not_performed`(`review_tasks_not_planned`)다. task별 `result`는 `accepted`, `accepted_incomplete`(일부 후보만 판정한 verify), `not_planned`, `not_succeeded`, `result_missing`, `result_rejected`, `skipped`(정책 생략)다.

`coverage.review_calls`는 검토 기록이 있는 리비전에서 호출마다 역할·모델·OpenCode 버전·지침 id·version·digest·요청 SHA-256·호출 결과를 실제 담당 역할에 한 번씩 싣는다. 호출 결과가 `accepted`여도 후보의 판정은 `refuted`일 수 있다. 수락한 응답이 실제 실행이나 사람의 확인을 대신하지 않는다. 검토 산출물의 수락 조건과 단계 연결 검사(`review_lineage_mismatch:<task>`)는 [검토](REVIEW.md)에 있다.

생성기 14부터 HTTP 경로 보충 선택을 호출했으면 문서 역할의 호출 내역에 `purpose=workflow_selection`을 표시한다. 상세의 모델 호출 기록과 전체 호출 수에 포함한다. 보충 응답이 실패해도 먼저 수락한 문서 응답을 보존한다.

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

구조·중복 관찰의 `metric.excerpt`와 `metric.counterpart_excerpt`는 스냅샷에서 검증한 위치에 저장된 짧은 발췌가 있을 때만 코드 포함 내보내기에 들어간다. 구조·중복 및 정적 분석의 결과 표에서 개별 상세를 열어 발췌와 줄 번호를 확인한다. 종합과 정적 분석 상세는 진단 작업 가운데 결과를 수락한 작업을 같은 방식으로 집계한다. 구조·중복 측정과 비밀 탐지는 각각의 절에서 확인한다.
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
| `test_status` | 기존 테스트의 확인된 실행 상태. [테스트 결과](#테스트-결과) |
| `notes` | 해석 메모 코드: `inventory_counts_all_included_files`, `tests_not_planned`, `tool_processed_files_unknown`, `tool_reported_zero_is_not_clean`(도구가 0건을 보고한 것은 결함 없음이 아니다) |
| `ai_review`, `review_calls` | [AI 검토 상태](#ai-검토-상태) |
| `tools` | 분석기 task마다 한 행 |

도구 행은 task key, 분석기 ID, 도구 버전, exit class, 부분 파싱 여부, 범위·처리·제외·미목록 파일 수, 패키지 상태별 수, stderr 실패·분석기 오류 수, 행별 한계를 담는다. 처리 경로(`processed_paths`)는 성공·수락된 task의 경로만 싣고, 비교가 사라진 발견의 위치를 다시 검사했는지 판단할 때 쓴다. 규칙 문맥(`rule_context`: 규칙 팩·버전·선택·저장소 설정 처리·소스 안 억제 처리)의 정의와 분석기별 값은 [분석기](ANALYZERS.md)에 있다.

### 테스트 결과

종합의 **기존 테스트** 표에서 실행기별 통과·실패·건너뜀과 실패한 케이스를 확인한다. 구조화된 결과를 기록한 실행은 명령의 종료 상태와 개별 테스트 결과를 함께 보여준다. 위치를 확인한 실패는 실제 파일과 줄로 연결한다. 코드 미포함 내보내기는 개별 케이스 이름을 생략하고 집계를 남긴다.

문서 대조에서는 인용한 테스트 근거 안에 이번 실행이 확인한 케이스의 줄이 있을 때 **인용한 테스트의 실행 결과**를 표시한다. 전체 스위트가 통과했더라도 위치가 연결되지 않은 기능에는 통과 판정을 붙이지 않는다.

`with-tests` 보고서는 종합의 **기존 테스트** 행에서 실행 결과로 이동한다. 표에서 테스트 종류와 모듈, 성공·실패·미실행 상태, 기록한 종료 코드와 사유를 확인한다. 문서나 코드 분석에서 인용한 테스트는 테스트 실행 집계와 별개로 읽는다.

빌더는 접수한 테스트 작업과 결과 manifest의 `test.run`을 대조한다. 정상 종료와 정리가 확인된 실행 중 접수 상태가 성공이고 종료 코드가 0이면 해당 작업은 통과다. 접수 상태가 실패이고 종료 코드가 양수거나 구조화된 결과에서 실패한 케이스를 확인했으면 실패다. `no_tests_executed`는 미실행으로 분류한다. 확인된 실패가 하나라도 있으면 `coverage.test_status`는 `failed`다. 모두 통과하면 `passed`, 일부만 통과하면 `partial`, 확인된 통과·실패가 없으면 `not_run`이다. 정책·도구·격리로 검사하지 못한 사유는 작업 행에 남는다.

빌더 18은 완료된 Go stdout의 실패 이벤트와 고정 스냅샷의 테스트 선언을 대조한 뒤 실행별 `executions[].go_test_failures`에 최대 64개를 저장한다. 각 항목은 `name`, 스냅샷 상대 `path`, 선언 `line`을 담는다. 이름과 선언 위치를 확인할 수 없는 명령 실패에도 작업 상태와 종료 코드는 남는다. 실패 로그 문장과 패키지명은 발췌와 모델 요청에서 제외한다. 코드 미포함 내보내기에는 이름을 생략하고 경로·줄·실행 상태를 보존한다. 실패 위치를 추가로 수락해도 테스트 상태나 발견 집계는 같다. [실행과 근거 수집](EXECUTION.md#기존-테스트-실행)

### 한계 코드

`limitations`와 `notes`는 코드만 저장한다. 렌더러가 [한국어 문구 표](../internal/report/messages_ko.go)로 번역하고 모르는 코드는 그대로 보여준다. 주요 보고서 수준 코드는 다음과 같다. 분석기·검토 단계가 더하는 코드는 해당 문서에 있다.

| 코드 | 뜻 |
|---|---|
| `ai_review_not_performed`, `ai_review_partial` | 필수 AI 검토가 수행·검증되지 않았거나 일부만 검증됐다. |
| `review_verification_incomplete` | 검증 task가 일부 후보만 판정하고 끝났다. 수락한 판정은 발견에 싣고 나머지 후보는 사유와 함께 미검토로 남겼다. |
| `review_candidate_omitted`, `interpretation_items_omitted`, `document_items_omitted`, `environment_notes_omitted` | 형식 복구 뒤에도 규칙을 통과하지 못한 모델 출력 항목을 뺐다. 각각 코드 분석·문서 분석·환경 분석 절의 분석 공백으로도 보인다. [출력 오류 분류](REVIEW.md#출력-오류-분류) |
| `result_manifest_unavailable`, `result_manifest_rejected:<사유>` | 결과 manifest가 없거나 거부돼 DB task 행만 보여준다. |
| `required_task_not_succeeded` | 성공하지 않은 필수 task가 있다. |
| `analysis_artifact_missing:<task>`, `artifact_rejected:<task>` | 분석 산출물을 읽지 못했거나 검증에 실패해 그 task 결과를 뺐다. |
| `unrecognized_artifact:<task>` | decoder가 없는 JSON 산출물이 있다(결과에 반영하지 않음). |
| `task_not_in_result_manifest:<task>`, `artifact_status_differs_from_accepted_task:<task>` | manifest와 DB 행이 어긋나 DB 행을 따랐다. |
| `static_analysis_gap:<생태계>` | 범위에 그 언어 소스가 있으나 노드에 분석기가 없다. |
| `static_analysis_not_planned` | 범위에 지원 언어 소스가 없어 분석기 task를 계획하지 않았다. |
| `cleanup_uncertain` | 하위 프로세스 정리를 확인하지 못했다. |
| `controller_holds_no_snapshot_bytes` | 고정 snapshot에서 수집한 표시용 코드 발췌를 사용할 수 없다. 이전 워커에서 생성했거나 읽을 수 있는 위치가 없는 보고서에 표시한다. |
| `required_tests_not_performed` | `with-tests`가 요청한 테스트 일부 또는 전체에서 확인된 결과가 없다. |
| `test_execution_unverified` | 성공으로 접수된 테스트 작업의 명령 종료 코드를 확인하지 못했거나 하위 프로세스 정리를 확인하지 못했다. |
| `run_cancelled`, `run_failed` | 실행이 취소·실패했다. |
| `compile_db_generated`, `compile_db_generation_partial`, `compile_db_generation_not_allowed`, `cmake_not_found`, `cmake_generator_not_found`, `repository_code_executed` 등 | C/C++ [compile DB 생성](ANALYZERS.md#compile-db-생성) 작업(`compile-db`)의 제약. 이 작업은 관찰 envelope를 쓰지 않으므로 builder가 결과 manifest의 그 task 제약 중 `cmake_*`·`compile_db_*`(`compile_db_generator_cmake` 제외)와 `repository_code_executed`를 보고서 한계로 옮긴다. |

**compile DB 생성 작업.** `compile-db` task는 분석기 task가 아니므로 정적 분석 절의 도구 행이 되지 않는다(산출물이 없다는 `result_missing` 공백도 생기지 않는다). 성공하지 않았으면 분석 공백 `compile_db_not_generated`(절 `static_analysis`, `subject` `compile-db`, `detail`은 task 사유)가 생긴다. 이 task는 이 처리를 더한 worker부터 있으므로 같은 사실에서 이전 생성기의 결과가 달라지지 않는다. 그래서 이 처리 때문에 builder 버전을 올리지 않았다.

### 현재 보고서에 없는 것

- **builder 4 이하 리비전의 탐색 결과.** `env.discover`와 `docs.discover`의 산출물은 `EnvironmentDecoder`([`environment.go`](../internal/report/environment.go))가 attempt·snapshot·probe 실행 기록과 대조해 검증한다. builder 5부터 그 결과가 환경 분석·문서 분석 절이 되지만, 이전 리비전은 다시 빌드하지 않으므로 산출물 행과 task 상태·사유로만 보인다. 관찰·발견·도구 행에 더하지 않는 것은 같다.
- **원본 파일 전체.** 인용 위치와 저장한 발췌를 표시한다. 발췌가 없는 이전 리비전은 위치를 표시한다.
- **결함 영역별 절.** 절은 분석 종류(환경·문서·정적 분석·지표·비밀정보·모델 검토)로 나눈다. 발견의 `category`(정확성·동시성 등) 축마다 수행한 검사와 제한을 보여주는 절은 없고, 발견 목록이 category로 묶일 뿐이다.
- **Markdown·HTML의 일부 필드.** 두 형식은 도구 행의 `rule_context`, `hidden_findings`, 현재 판정을 렌더링하지 않는다. 판정은 게시 시점 값만 보인다. JSON과 [비교 응답](#run-비교)에서 읽는다.

### 근거 발췌와 문서 대조

표시용 발췌는 워커가 크기와 blob SHA-256을 대조한 고정 스냅샷에서 가져온다. 워커는 비밀값 탐지 결과가 가리키는 파일, 자격증명 경로나 패턴에 해당하는 파일, 바이너리를 건너뛴다. 도구 진단은 표시 줄 주변 최대 9줄을 저장한다. 모델 인용은 인용의 첫 줄부터 끝까지 최대 50줄을 연속해서 저장한다. 검토 기록 0.7의 인용은 워커가 좁힌 줄 범위(최대 80줄)이므로 대개 인용 전체가 보이고, 더 긴 인용은 앞부분만 보인다([인용](REVIEW.md#인용)). JSON 문자열로 인코딩한 크기가 1,800바이트를 넘기기 전의 마지막 완전한 줄에서 멈춘다. 생성기 18은 검증한 문서 인용의 전체 구간이 최대 40줄·2,000바이트에 맞을 때 마지막 인용 줄까지 저장할 수 있다. 이 문서 전용 창은 `source-excerpts` 산출물 `0.3`의 `kind: document` 항목으로 분리한다. 한 시도의 상한은 128개와 384 KiB다.

관리 서버는 시도와 스냅샷이 일치하는 산출물을 확인한 뒤 인용 위치에 발췌를 붙인다. 발췌 산출물 `0.2` 이상에서는 같은 시작 줄에도 도구 문맥과 모델 인용이 각각 있을 수 있다. 같은 시작 줄을 가리키는 모델 인용은 저장한 공통 범위에서 각각의 인용 끝 줄까지 사용한다. 이전 산출물(`0.1`)과 이전 보고서는 당시 저장한 문맥을 읽는다. 인용과 발췌의 줄 범위가 다르면 실제 저장된 줄을 표시하고 사용할 수 있는 경우 고정 커밋의 인용 범위로 연결한다. 화면을 열 때 스냅샷이나 Git에서 원문을 다시 가져오지 않는다. 보고서에 붙이는 발췌 원문 상한은 256 KiB이며 코드 미포함 내보내기는 발췌 필드를 생략한다.

문서 분석은 실제 인용된 파일 경로별로 읽는다. 문서마다 **문서와 구현**에 동작과 저장된 대조 상태·이유를, **문서 품질**에 명확성·설명 범위 등의 모델 해석을 표시한다. 하나의 판단이 여러 문서를 인용하면 각 문서의 목록에서 같은 상세로 이동할 수 있다. 인용의 정확한 줄 범위는 해당 문서의 원문 발췌로 연결한다. 같은 발췌 또는 같은 줄을 그대로 포함한 공통 발췌는 문서 분석 절에서 한 번 표시한다. 발췌가 없는 리비전이나 코드 미포함 내보내기는 인용 위치와 대조 상태를 보여 준다. 문서 대조는 도구 진단이나 발견 건수에 합산하지 않는다. 문서와 구현 사이에 불일치 또는 부분 일치가 있으면 종합에서 해당 범위를 확인할 수 있다.

코드 분석은 주요 경로의 근거로 지지하거나 재현한 조치 항목을 심각도순으로 먼저 표시한다. 추가 확인이 필요한 후보, 시험·예제 경로의 항목, 기각된 후보, 처리 상태가 기록된 항목은 각각 해당 목록에서 확인한다. 저장된 구현 관찰은 **코드에서 관찰한 동작**을 펼쳐 모듈 범위와 인용 위치를 확인한다. 모델 검토가 수락되지 않은 경우는 코드 절에서 해당 상태를 표시한다. 수락된 검토가 있고 조치 항목이 없으면 검토한 주요 경로를 기준으로 설명한다.

같은 문서 계약에 연결하여 전체 검토가 함께 읽은 조회 처리기·변경 처리기·테스트 단언의 위치가 있으면 **함께 읽은 코드와 테스트**에서 확인한다. 웹·HTML에서는 동작 상세와 종합의 해당 사용 흐름에서 펼칠 수 있으며 Markdown에서는 같은 인용을 읽을 수 있다. 테스트 실행 상태는 분석 기록에서 확인한다. 코드 미포함 내보내기는 파일·줄과 세 근거 ID를 보존한다. [API 응답](API.md#보고서-경로)

선택 작업 `review.contract`가 처음에 미확인으로 기록한 항목 하나를 같은 문서와 적용 범위에서 다시 대조해 일치·부분 일치·불일치로 판정하면 `documents.comparisons[].followup`이 `true`다. 보고서 작성 시 같은 시도와 스냅샷의 수락된 문서·전체 조사 및 사용한 모듈 검토가 연결되는지 확인한다. 새 구현 위치와 함께 인용한 테스트 코드는 발췌와 줄 위치를 보이며, 테스트의 실행 상태는 실행 기록에서 결정한다. 기록이 없거나 남은 예산이 부족하거나 전송 정책·범위에 막히거나 선행 기록이 맞지 않으면 기존 판정과 이유를 유지한다. 이전 리비전에는 필드가 없다. 최종 대조가 여전히 미확인이어도 새 구현 줄을 인용했으면 `additional_observation`에 확인한 범위와 최종 대조가 인용한 코드·테스트 근거를 순서대로 싣는다. 처음 조사에서 읽었던 호출부가 새 구현과 함께 인용되면 두 위치를 모두 읽을 수 있다. 상세에서 두 설명이 공유하는 인용은 한 번 표시한다. 원래 판정·이유·근거와 미확인 수를 유지한다. 테스트만 인용하거나 처음 조사와 같은 줄만 인용했을 때는 추가 관찰을 싣지 않는다. 코드 미포함 내보내기는 추가 대조 여부, 판정, 적용 범위 및 문서·코드·테스트 위치를 유지하고 서술과 원문을 생략한다.

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

영향이 크다는 가정만으로 `critical`로 올리지 않는다. 도구 발견의 심각도는 분석기 팩의 [기본 심각도](ANALYZERS.md#기본-심각도) 표가 정한다(builder 22부터. 그 전은 모두 `low`). 도구 행의 `severity_table`이 쓰인 표를 가리킨다. 모델 검증은 `critical`을 정하지 않는다.

### 검증 상태

| 값 | 뜻 |
|---|---|
| `unreviewed` | 검토 전 후보. 도구 진단에서 만든 항목은 항상 이 값이다. |
| `supported` | 근거가 주장을 지지한다. |
| `reproduced` | 실행으로 재현했다. 실행 근거가 없으면 검증 실패다. |
| `refuted` | 반대 근거로 기각했다. |
| `inconclusive` | 판단할 근거가 부족하다. |

도구 진단·모델 후보·재현된 결함·사람의 판단은 서로 다른 기록이다. 도구가 보고했다는 사실은 실제 결함 확정과 같지 않다. 검증 상태와 검토 우선순위는 별개다. 영향이 클 수 있지만 미확정인 후보도 우선 확인 목록에 들 수 있다.

발견을 줄 세우거나 개수로 자르는 목록(요약의 `priorities`, PR 댓글의 발견, 보조 경로 발견 목록)은 검증 상태를 먼저 본다. 순서는 `reproduced`, `supported`, 아직 확인하지 않은 분류 문제(`review_tNNN_…`의 `unreviewed`·`inconclusive`), 그 밖의 `unreviewed`·`inconclusive`(도구 finding·모델 후보), `refuted`이고 심각도는 같은 상태 안에서만 본다. 도구 finding은 기본 심각도 표로 `high`일 수 있지만 검토 전이므로, 지지된 발견을 짧은 목록 밖으로 밀어내지 않는다. 조치 카드와 확인 필요 후보 목록은 이미 한 상태만 담으므로 심각도 순이다. 모델의 숫자 confidence를 정확도처럼 표시하지 않는다. 검증 상태와 근거 종류로 정성적 근거 수준을 보여준다.

## 코드 미포함 내보내기

`include_code=false`가 기본이다. 저장본을 바꾸지 않고 다음 projection을 만든다([`Project`](../internal/report/validate.go)).

- 관찰의 `raw_message`를 모두 null로 만든다. Markdown·HTML은 그 자리에 "(코드 미포함: 생략)"을 쓴다.
- 발견 제목을 고정 형식(`<도구> <규칙> 진단 (<경로>:<줄>)` 또는 `<category> 항목 (<경로>:<줄>)`)으로 바꾼다.
- 심각도 이유·영향·개선·확인·한계·조건·근거 설명 중 builder의 고정 문구가 아닌 것은 생략 문구로 바꾸고, 주장(`assertion`)은 모두 생략한다. 서술에 섞인 코드 인용도 이렇게 빠진다.
- 위치와 근거 위치의 심볼 이름을 지운다.
- `fingerprint`, `fingerprint_basis`, `disposition_version`을 싣지 않는다. 키 없는 digest는 짧은 코드 줄을 추측해 맞춰 볼 수 있는 확인 수단이 되기 때문이다. 식별과 현재 판정은 프로젝트 권한을 검사하는 비교·판정 API로만 읽는다. 프로젝트별 키로 감싼 식별자는 키 수명 관리가 필요해 만들지 않았다.
- 도구 행 `rule_context`의 `config_paths`·`config_digest`를 지운다. 설정 바이트의 digest도 같은 이유로 추측 확인 수단이 된다.
- 분석 종류별 절의 모델 문장(환경 메모, 문서 계약·충돌·질문·지침 메모, 진단 해석의 한계, 도구 진단 분류의 이유)을 생략 문구로 바꾸고, 모듈 요구의 제약 문자열은 버전 제약 형식이 아니면 지운다([분석 종류별 절](#분석-종류별-절)).

스키마의 code-free 규칙과 [`scripts/validate_bundle.py`](../scripts/validate_bundle.py)의 부정 사례가 식별·규칙 문맥 규칙을, `ValidateReport`가 절의 모델 문장 규칙을 강제한다. 경로·줄 번호·규칙 ID·산출물 hash는 남는다. 비밀값과 그 일부·hash는 처음부터 저장하지 않는다([분석기](ANALYZERS.md)). 코드 포함 내보내기는 같은 프로젝트를 읽을 수 있는 사용자에게 저장된 도구 메시지·검토 문구·심볼을 그대로 준다. 내보내기 다운로드도 요청마다 프로젝트 권한을 검사한다.

## Markdown과 HTML 렌더링

두 형식은 같은 view 모델에서 만든다. 분석 종류별 절이 있는 리비전은 상세도가 둘이다([내보내기 상세도](#내보내기-상세도)). 표지에는 프로젝트명, 등록한 Git URL 또는 로컬·NFS 경로, 실행 상태와 기록된 종료 또는 게시 시각을 표시한다. 시작·종료는 해당 시도의 워커 자원 배정과 반납 시각이다. HTML은 커밋을 표지에 두고 run·attempt·snapshot·digest가 있는 기술 상세를 접어 둔다. 배너는 부분·실패·취소 결과임을 첫머리에 알린다.

- **분석 종류별 절이 있는 리비전.** 종합([조치 항목 우선 배치](#조치-항목-우선-배치): 조치 항목·문서와 구현이 다른 곳·함께 확인할 것·접힌 후보와 도구 진단 뒤에 상태 표·먼저 볼 것·우선 확인할 발견), 환경 분석, 문서 분석, 정적 분석(언어와 검사 목적별 실행 결과·진단·도구 발견), 구조·중복 지표, 비밀정보, 코드 분석(역할·모델 검토 발견·진단 해석의 한계), 분석 공백, 부록(AI 검토, 분석 범위, 작업 상태, 실행 기록, 산출물, 한계와 공백), projection JSON digest 순이다. 각 절의 처음에 수행 결과를 표시한다. 전체 분석기 작업의 집계는 구조·중복과 비밀 탐지를 포함한다고 표시하고 펼쳐 볼 수 있게 둔다. 환경 메모와 문서 해석의 인용 위치에서 허용된 발췌와 고정 Git 커밋의 원본을 열 수 있다. 로컬 및 코드 미포함 보고서에서는 파일과 줄 범위를 확인할 수 있다. HTML은 절 목차와 절마다 `id`를 두고, 집계의 수 칸에 `data-count`를 붙인다.
- **이전 리비전(builder 4 이하).** 상세도와 관계없이 요약 표, 우선 확인 목록, AI 검토(필수·선택 task와 모델 호출), 분석 범위와 도구 표, 작업 상태, 발견 항목, 원시 도구 관찰, 실행 기록, 산출물, 한계와 공백, projection JSON digest 순의 평면 배치다. 출력은 절 도입 전과 바이트 단위로 같다([`testdata`](../internal/report/testdata/report-go-vet.md)).

### 조치 항목 우선 배치

분석 종류별 절이 있는 리비전의 Markdown·HTML·웹 화면은 같은 저장 필드를 개발자가 먼저 고칠 것부터 읽는 순서로 놓는다. 저장 본문·집계·판정은 바뀌지 않으며 새 문장은 고정 문구와 저장 필드에서만 만든다. 점수·신뢰도·심각도 변경은 없다. 구현은 [`render_actions.go`](../internal/report/render_actions.go)(Markdown·HTML)와 [`ReportActions.tsx`](../web/src/ReportActions.tsx)(웹)다. builder 4 이하의 평면 배치는 이 순서를 쓰지 않고 바이트 단위로 그대로다.

| 순서 | 내용 |
|---|---|
| 1 | 판정. 표지의 실행 상태와 부분·실패·취소 안내는 그대로 맨 위에 있다. 바로 아래 한 줄이 분석하지 못한 범위(미성공 필수 작업, 분석 공백, 모델에 보내지 못한 파일, 모델 검토 미수행·일부)를 쓰고 상태 표로 연결한다. 해당하는 것이 없으면 이 줄은 없다. |
| 2 | **조치 항목.** `supported`·`reproduced` 발견 중 숨김 판정이 아니고, 도구 발견과 중복되지 않으며 주요 경로에 있는 것을 카드로 보인다. 심각도 순이고 같은 심각도에서는 `reproduced`가 앞선다. 종합 표의 "주요 코드 조치" 수와 같은 발견이다. 카드는 제목, 검증 상태와 심각도, 확인된 발생 조건과 가정, 영향, 저장된 코드 발췌 또는 위치, 개선 방향, 확인 방법, 판단 범위(기록된 한계), 근거·반대 근거로 가는 링크를 담는다. 링크는 코드 분석 절의 같은 발견 행(발견 상세 전체)으로 간다. |
| 3 | **문서와 구현이 다른 곳.** 문서 대조의 `mismatch`·`partial`을 불일치부터 보인다. 카드는 문서가 약속한 동작(제목), 구현에서 관찰한 동작, 적용 범위, 문서 위치와 코드 위치, 문서 분석 절의 대조 근거 링크이며 모델 해석이라는 표시를 단다. 5건을 넘으면 나머지는 접는다. |
| 4 | **함께 확인할 것**(비밀정보 탐지, 환경 요구 불일치, 문서 안내 충돌: 종합이 저장한 먼저 볼 항목)과 접힌 목록 둘. 접힌 **추가 확인이 필요한 후보**는 주요 경로의 `inconclusive`·`unreviewed` 모델 후보로 제목, 상태, 위치, 기록된 확인 방법을 보인다. 접힌 **예제·시험 입력 등에서 발견한 항목**은 부차 경로의 같은 종류와 근거로 지지한 항목이다. 접힌 **도구 진단**은 분석기·규칙별 한 행(건수, 심각도, 규칙 설명)이고 펼치면 위치(최대 25곳, 나머지는 정적 분석 절과 JSON)가 나온다. `diagnostic_triage`가 있으면 제목이 "도구 진단 M건 중 N건 분류"이고 확인이 필요한 진단·검증에서 반증된 진단·무해하다고 판단한 진단·분류하지 않은 진단 순으로 나눈 뒤 그 안에서 분석기·규칙별로 묶는다. 행은 기본 심각도를 보이고 펼치면 묶음의 분류, 무해·판단 불가의 이유와 인용 위치, 문제 묶음의 발견으로 가는 링크가 위치 목록 앞에 나온다. 지지된 문제 묶음은 2의 조치 항목 카드로도 보인다. |
| 5 | 분석 범위: 주요 분석 결과 표(영역별 상태), 문서 대조와 판단 근거, 검토 범위와 집계(접힘), 이어서 환경·문서·정적 분석 등의 절과 부록. 모두 이전과 같은 내용이며 위치만 아래로 옮겼다. |

조치 항목이 없을 때의 문구는 검토 상태에 따라 다르다. 수락한 검토가 있고 찾지 못했으면 "검토한 주요 코드 경로에서 근거로 지지하거나 재현한 조치 항목을 찾지 못했습니다"와 모델에 보낸 주요 경로 파일 수, "결함이 없다는 뜻이 아니다"를 쓴다. 모델 검토를 수행하지 못했거나 실행이 실패·취소됐으면 "조치 항목을 판단하지 못했다"고 쓰며 빈 목록을 확인한 결과로 읽히지 않게 한다. 조치 항목이 없어도 후보·부차 경로 항목·문서 불일치가 있으면 그 수를 같은 자리에 적는다. 코드 미포함 내보내기는 서술과 발췌를 생략하고 위치·검증 상태·심각도만 보이며, 생략 안내는 카드마다 반복하지 않고 한 번 쓴다.

HTML·웹은 접힌 블록을 `details`로 만들고 링크 대상이 접힌 블록 안에 있으면 둘러싼 블록을 연다. 휴대폰 너비에서는 카드의 라벨과 값이 한 열로 쌓이고, 검증 상태·심각도·줄 번호·펼침 단추가 잘리지 않으며, 긴 경로와 코드는 줄바꿈하거나 자기 창 안에서 스크롤한다. 사용자에게 도움이 되는지는 사람으로 측정하지 않았다.

### 내보내기 상세도

Markdown과 HTML의 기본은 요약 보기(`detail=summary`)다. 요약 보기는 렌더링만 바꾸며 저장 본문·digest·JSON 내보내기는 같다. 근거를 선별하는 규칙은 [`render_overview.go`](../internal/report/render_overview.go)에 있다.

- **종합.** [조치 항목 우선 배치](#조치-항목-우선-배치) 아래에서 결과 표에 환경·문서·정적 분석·구조와 중복·비밀정보·코드·선택한 테스트·공백을 기록이 있는 순서대로 보여 준다. 문서를 읽은 수와 구현 대조 결과, 기준을 넘은 복잡도 함수 수와 보고된 중복 쌍 수, 주요 코드와 시험·예제의 조치 수를 구분한다. 모델 조사 기록은 수락된 발췌에 포함된 파일과 줄을 표시한다. 각 행에서 관련 상세로 이동할 수 있다.
- **문서 대조와 판단 근거.** 종합의 선택 항목에서 설명서와 구현 인용, 판단 이유, 프로젝트 사용 흐름을 펼친다. 루트 README의 저장된 소개는 표지에서 펼친다. 문서 대조는 불일치, 일부 일치, 일치, 미확인 순으로 살펴보고 각 항목에서 적용 범위를 확인한다. 설명서의 저장된 발췌에서 JavaScript 리터럴 호출과 `//=>` 출력 또는 Python 리터럴 `assert`를 정확히 짝지을 수 있으면 예시 옆에 보여 준다. 예시는 문서에 적힌 내용이며 판정이나 집계에 반영하지 않는다.
- **코드 분석.** 주요 코드의 근거를 확인한 발견, 추가 확인 후보, 시험·예제의 항목을 표에서 판정·심각도·위치와 함께 읽는다. 웹과 HTML의 요약에서 선택한 행을 열면 저장된 발췌, 영향, 권고와 확인 방법을 볼 수 있다. 기각된 후보와 처리 상태가 기록된 발견은 접힌 행에서 확인한다. 장부는 모듈과 파일별 검토 범위를 표시한다.
- **정적 분석.** 언어에서 정적 규칙·타입/컴파일·형식과 기타 검사별로 어떤 도구가 실행됐는지 읽는다. 결과를 수락한 성공에는 진단 결과와 처리 파일 범위를 표시하고, 실패·미지원은 사유를, 작업 기록이 없는 검사 목적은 미계획으로 표시한다. 상태·사유·버전·규칙 팩이 같은 분석기 작업은 함께 표시하며 모듈별 작업과 보존된 처리 파일을 펼쳐 확인한다. 타입 정보를 사용하는 ESLint와 독립적인 타입 검사는 각각 구분한다. 규칙별 진단은 주요 경로 순으로 표시하며 개별 진단과 도구 발견은 전체 보기와 JSON에서 확인할 수 있다.
- **지표·비밀정보.** 복잡도와 중복 측정 도구의 상태, 처리 또는 전달 파일 수, 기준과 제외 범위를 읽는다. 복잡도는 함수 수가 기록됐으면 함께 보이고, 높은 값부터 함수 시작 줄을 보여 준다. 중복 쌍이 저장된 줄 범위를 공유하면 한 묶음에 표시하며, 펼치면 각 쌍의 위치를 확인할 수 있다. 상대 위치를 별도 필드에 저장하지 않은 이전 보고서는 원시 기록을 구분해서 제공한다. 코드 미포함 내보내기는 확인된 양쪽 위치와 측정값을 유지한다. 요약에는 높은 측정값이 있는 위치와 묶음부터 표시한다. 비밀정보는 주요 경로 탐지만 표로 보이고 부차 경로 탐지는 규칙별 수로 적는다.
- **환경·문서.** 부차 경로 아래의 모듈·요구·판정·문서·문서 모듈·지침 겹침은 표에서 빼고 수로 적는다. 판정은 충족·불일치를 한 줄씩, 판단 불가는 도구·이유별로 묶는다. 불일치는 부차 모듈이어도 보인다. 읽지 않은 문서는 상태별로 묶는다.
- **공백·부록.** 분석 공백은 절·공백·세부별 한 행(수, 대상 앞 5개)이다. 부록은 AI 검토 상태와 호출 수, 분석 범위 수, 성공하지 않은 작업을 상태·이유별로 묶은 표(입력이 없어 건너뛴 작업 제외), 실행 기록·산출물의 수, 한계다.
- **부차 경로.** 경로 판단은 [검토 우선순위](REVIEW.md#검토-우선순위)의 기본 부차 경로 규칙(`record.DefaultSecondary`)을 경로에만 적용한다. 생성 파일 표지는 보지 않는다. 장부 행은 builder 8이면 저장된 `class`를, 그 이전이면 같은 경로 규칙을 쓴다.
- **근거.** "코드 보기"와 "문서 보기"에 파일과 줄 범위를 붙인다. 웹과 HTML의 상세 행에서 저장된 발췌를 보고서 안에서 읽는다. GitHub.com·GHES 고정 커밋 링크가 함께 있으면 펼친 곳에서 커밋의 인용 줄도 열 수 있다. 저장된 발췌가 없고 링크가 있으면 고정 커밋의 인용 줄을 새 탭에서 연다. 저장한 발췌가 인용 범위의 일부일 때는 실제 발췌 줄 범위를 표시한다. 종합과 문서 대조 항목에는 내부 범위 표기를 붙이지 않는다.
- **판단 방식.** 종합의 주요 코드 근거가 있는 발견은 검증 상태에 따라 "코드 검토로 확인" 또는 "실행으로 재현"으로 표시한다. 지지·판단 보류 발견의 기록에 서술형 한계가 있으면 첫 한계를 "판단 범위"로 보여 준다. 지지된 발견은 원래 권고를 "조건 확인 후 조치"로 표시하고, 판단 보류 발견은 기록된 확인 방법을 표시한다. 이전 리비전에도 적용하며 저장된 판정과 집계는 보존한다. 코드에서 확인한 동작은 저장된 강점의 제목과 효과를 함께 보여 주며 해당 구성 요소의 적용 범위가 기록되어 있으면 함께 표시한다. 같은 문구가 반복되면 한 번만 표시한다. 아직 확인할 조건이 함께 있는 경우에도 제목과 효과를 먼저 읽을 수 있다.
- **용어 풀이.** 웹과 HTML의 종합에서 선택한 판단·이유·확인 방법에 실제 나온 기술 용어와 인용 위치만 접힌 "용어 풀이"에 표시한다. HTTP 상태 코드 설명은 응답·코드 문맥에서만 제공한다. 두 화면은 같은 로컬 용어 목록을 쓰며, 코드 미포함 HTML에서는 생략한 문장을 용어 선택에 쓰지 않는다. 판단 근거가 부족할 때는 모델 검토 상태와 번역할 수 있는 기록 사유를 이용해 설명한다.
- **코드 미포함.** 경로와 줄 범위 및 구조화된 검토 상태를 표시한다. 원문 메시지, 모델 서술, 코드 발췌와 원본 링크는 코드 미포함 정책을 따른다.

전체 보기는 개별 도구 진단과 발견, 작업·실행·산출물의 전체 행을 표시한다.

같은 도구 관찰을 참조하거나 정확히 같은 코드 근거와 확인한 규칙을 설명하는 발견은 요약에서 한 번 센다. 묶인 기록은 별도로 열 수 있으며 JSON과 전체 내보내기에서 원래 발견을 읽을 수 있다. `with-tests`를 선택한 실행에서는 테스트 미실행·결과 미확인·일부 통과·명령 실패·통과를 구분한다. 테스트 종류, 작업 사본의 상대 경로, 종료 코드와 사유는 테스트 상세에서 확인한다.

문서 분석은 파일마다 한 표에 문서와 구현의 대조, 문서 품질, 계약과 질문을 모은다. 행을 열면 저장된 구현 발췌를 읽는다. 구현을 대조하지 못한 항목은 인용한 문서 원문과 저장된 이유를 보여 준다. 여러 문서에 걸친 항목은 원래 결과로 연결한다. 같은 파일에 범위와 본문이 일치하는 저장 발췌가 있으면 전체 원문은 공통으로 열 수 있다. 코드 발견에 정확한 위치나 발췌 안의 고유한 이름이 있으면 주변 줄과 함께 보여 준다. 유일한 위치를 고를 수 없으면 저장된 범위를 강조 없이 열어 준다.

문서 분석의 **검색한 안내 문서에서 찾지 못한 CLI 기능**은 스냅샷에서 확인한 공개 선언, 리터럴 기본값이 있는 경우 그 값, 검색한 문서와 전체 줄 범위를 보여 준다. 명시한 문서에서 이름 표기를 찾지 못한 결과다. 이 밖의 문서나 사용법의 의미는 해당 범위에 들지 않는다. 코드 미포함 내보내기에는 선언과 문서 위치가 남고 이름과 기본값은 빠진다. [코드에서 출발하는 문서 검색](ARCHITECTURE.md#코드에서-출발하는-문서-검색) · [탐색 산출물 예시](../examples/documentation-gaps.json)

항목이 없으면 "결함이 없다는 뜻이 아니다"를, 관찰이 없으면 "도구가 0건을 보고한 것과 분석하지 않은 범위는 다르다"를 함께 쓴다. 실행하지 않은 도구 행은 진단 수 대신 "실행하지 않음"을 쓴다.

- **Markdown.** 모든 동적 값은 이스케이프한 평문이거나 코드 span이다. CommonMark 문장부호에 역슬래시, HTML 메타문자에 entity, 표의 `|`에 이스케이프를 붙이고 줄바꿈·제어 문자는 공백으로 바꾼다. 코드 span의 fence는 값 안의 가장 긴 backtick 연속보다 길다. 도구 문자열이 링크·이미지·HTML·표 구조를 만들 수 없다.
- **HTML.** 절이 있는 보고서의 표지에 프로젝트, 등록된 분석 대상, 실행 상태, 기록된 종료 또는 게시 시각(UTC), Git 커밋을 표시한다. 종료한 일부 완료 실행의 상태는 "일부 검사 미완료로 종료"로 보이며 날짜는 "종료 시각"으로 표시한다. 넓은 화면에는 옆 목차를 두고 좁은 화면에서는 위쪽의 목차를 펼쳐 절로 이동한다. 목차는 스크롤 위치와 문서 내부 링크를 따라 현재 절을 표시한다. 이전 평면 배치 리비전은 저장한 HTML의 렌더링 형식을 유지한다.
- **인쇄.** 자체 포함 HTML은 밝은 종이색 스타일로 인쇄한다. 접힌 분석 내용과 근거를 펼치고 화면용 탐색과 조작 버튼을 숨긴다. 코드와 표는 인쇄 너비에 맞게 줄을 바꾸며 코드 행을 유지한다.
- **자체 포함 파일.** `html/template`의 문맥별 escaping을 쓴다. CSS, 아이콘, 글꼴 부분 집합과 목차 갱신 스크립트를 HTML에 넣는다. 절이 있는 보고서의 응답 헤더와 `<meta>` CSP는 글꼴 데이터 URL과 SHA-256 해시를 명시한 목차 스크립트 하나를 허용한다. 외부 자산 요청은 기본 차단한다. 응답 헤더의 `frame-ancestors 'none'`은 다른 페이지에 삽입되는 것을 막는다. 긴 표와 코드는 가로 스크롤 영역에 둔다.
- 모델 서술과 코드 안의 HTML·스크립트는 렌더링하지 않고 이스케이프한다. 외부 이미지 로딩과 자동 링크 fetch는 하지 않는다.

### 화면 자산

웹과 절이 있는 HTML은 Tabler Core 1.7.5의 카드·버튼·배지·탐색 스타일과 Tabler Icons 3.52.0의 윤곽 아이콘 중 사용하는 항목을 함께 번들한다. CSS는 보고서에만 적용하도록 범위를 제한했다. MIT 고지는 코드와 함께 보관하며 HTML 파일에서도 하단의 "화면 자산 라이선스"에서 확인할 수 있다. [Tabler Core 원본](https://github.com/tabler/tabler/tree/4257ebcd1cd5494f71b84c485fd15749fc6d5f92), [Tabler Icons 원본](https://github.com/tabler/tabler-icons/releases/tag/v3.52.0), [Tabler Core 라이선스](../internal/report/reportui/LICENSE.tabler-core), [Tabler Icons 라이선스](../internal/report/reportui/LICENSE.tabler-icons), [Bootstrap 기반 스타일 라이선스](../internal/report/reportui/LICENSE.bootstrap)

Noto Sans KR 2.004-H2를 FontTools 4.68.0으로 글꼴 굵기 400–750, KS X 1001 완성형 한글 2,350자, 기본 한글 자모, 라틴 문자와 구두점에 맞춰 WOFF2로 줄였다. 그 밖의 글자는 시스템 글꼴을 사용한다. 웹은 서비스에 동봉한 글꼴을, HTML 내보내기는 파일에 내장된 글꼴을 사용한다. [글꼴 원본](https://github.com/google/fonts/tree/b38c5c93af322c45f633e17ac440ec1e6c94d489/ofl/notosanskr), [SIL Open Font License](../internal/report/reportui/LICENSE.noto-sans-kr)

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
- 같은 pull request다. pull request run은 같은 번호의 pull request run만, 다른 run은 pull request가 없는 run만 기준으로 삼는다.
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

단일 파일의 도구 발견을 비교할 때 두 실행에 그 파일을 처리한 성공 행이 있으면 도구 버전, 규칙 문맥과 억제 처리도 해당 행을 기준으로 확인한다. 처리 경로를 일부만 기록한 성공 행은 함께 비교한다. 두 실행 중 한쪽에서 파일을 처리한 행을 확인할 수 없으면 기존과 같이 분석기 전체의 규칙 문맥을 사용한다.

같은 식별의 진단을 이번 실행의 성공 행이 계속 관찰하고 있으면 발견 목록에서 사라져도 `absent_unverified`로 남긴다. 예를 들어 관리 JS와 구문 검사에서 같은 호출을 지적해 한 건만 표시해도 원시 진단은 유지된다.

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
| `active_observation_remains` | 현재 보고서의 발견 목록에서 사라졌지만 성공한 도구 작업의 원시 관찰에 같은 fp2의 활성 진단이 남아 있다. |
| `rules_unknown`, `rules_changed` | 도구 버전을 모르거나 다르다. 모델 후보에서는 역할·모델·지침 digest가 다르면 `rules_changed`다. |
| `rule_context_unknown`, `rule_context_changed` | 한쪽 규칙 문맥을 모르거나(문맥이 없는 행, 저장소 설정을 읽으면서 파일을 기록하지 못함) 규칙 팩·버전·선택·저장소 설정 모드·설정 digest·억제 처리 중 하나가 다르다. |
| `source_suppressions_hidden` | 현재 쪽 도구가 그 규칙의 억제를 적용하고 숨긴 진단을 보고하지 않는다(예: staticcheck U1000). |
| `model_review_not_performed`, `model_review_partial` | 관찰한 쪽 모델 검토가 수행되지 않았거나 일부만 수행됐다. |
| `model_absence_not_observation` | 모델 검토는 같은 조건으로 수행됐지만 다시 내지 않은 것은 해결 관찰이 아니다. |

도구 발견의 이유는 source variant → scope → 미실행·실패 → 위치 미검사 → `suppressed_in_source` → `active_observation_remains` → 도구 버전 → 규칙 문맥 → `source_suppressions_hidden` 순으로 처음 맞는 하나다. go vet 문서에는 규칙 팩이 없어 builder 4 decoder가 `codeye-go-vet-default` v1을 합성해 행에 쓰고, 문맥이 없는 builder 3 go vet 행은 비교가 같은 문맥을 합성한다. 그래서 builder 3과 4의 go vet 보고서는 서로 비교된다. 다른 분석기의 문맥 없는 행은 `rule_context_unknown`이다.

### 모호한 묶음과 이동 후보

- `ambiguous`: 한쪽에 같은 식별의 발견이 여럿이면 true다. 상태는 묶음 전체로 정하고 하나씩 짝짓지 않는다.
- `move_candidates`: `move_key`가 같고 경로만 다른 새 항목과 사라진 항목을 서로 가리킨다. 이동·이름 변경 후보로만 보여주고 자동으로 합치거나 판정을 옮기지 않는다.

### 차이 목록과 집계

- `differences`는 값이 다른 항목만 싣는다. 키는 `source_variant`, `source_revision`(커밋 SHA 또는 manifest digest), `profile`, `scope`, `builder_version`, `tool:<analyzer>:version|rule_context|severity_table|result|processed_files`, `ai_review`, `review:<role>`(모델·지침 digest)다. 화면은 비교 대상의 환경·규칙·범위 차이를 이 목록으로 상단에 보인다. 문서 대조 호출의 차이는 `review:contract`에 기록한다. 발견의 비교는 발견을 만드는 역할들의 모델과 지침을 사용한다.
- `rule_context` 차이 값은 `pack@version [선택] config=<모드>[:설정 digest 앞 12자|:unrecorded] suppressions=<모드>[ unreported=<규칙,…>]`이고 문맥이 없는 행은 `unknown`이다. 설정 digest 앞부분은 코드 미포함 내보내기에서 빼는 값이지만, 비교 응답은 코드 포함 보고서를 읽을 수 있는 같은 프로젝트 읽기 권한자에게만 가고 이미 fp2 식별을 싣는다.
- `severity_table` 차이 값은 분석기 행이 적용한 기본 심각도 표 ID이고 표가 없는 행(builder 21 이전)은 `none`이다. 두 쪽의 표가 다르면 도구 발견의 심각도 차이는 이 표 차이 하나로 보고 발견별 `changes`에 `severity`를 넣지 않는다. 같은 식별의 도구 발견은 `persisting`으로 남는다. 모델 발견과 같은 표를 쓴 두 쪽의 심각도 차이는 그대로 변경이다.
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

첫 화면은 세션 확인 중, 서버 연결 불가(재시도 버튼), 로그인 필요(링크 거부·만료·로그아웃 사유), 로그인됨 중 하나다. 로그인 링크의 토큰은 URL fragment에서 읽은 즉시 주소창에서 지운다. 인증 절차는 [API](API.md)에 있다. 머리글 메뉴는 "프로젝트·분석"과 "도구"(`codeye up`이 기록한 도구가 있으면 찾은 도구 표, 없으면 환경 진단 파일 보기), 관리자에게는 "관리"다. 로그인 화면과 연결 불가 화면은 터미널에서 실행할 명령(`codeye open`, `codeye up`)을 복사 단추와 함께 보인다. 브라우저 탭 제목은 보고 있는 화면·프로젝트·실행 상태를 따른다.

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
- 나머지 활성 실행이 있으면 그 수를 함께 보인다. 실행 기록이 없으면 "아직 분석하지 않음", 단계 근거가 없는 실행은 "세부 단계 미기록"과 확인 가능한 실행 상태를 보인다.
- 마지막 게시 보고서 링크는 그 보고서의 run·리비전에 고정된 코드 미포함 HTML 내보내기다. "최신"으로 따라가지 않는다.
- 목록은 대표 실행이나 다른 활성 실행이 끝나지 않은 프로젝트가 있을 때만 5초마다 다시 조회한다. 탭이 숨겨지면 멈추고 다시 보이면 이어간다. 실패하면 마지막 값과 확인 시각을 유지하고 간격을 두 배씩 최대 60초까지 늘린다. 실행 요청·상태 변경·더 보기·프로젝트 등록이 조회를 다시 시작한다.

### 프로젝트 등록과 상세

등록 필수 입력은 Git URL+브랜치 또는 source root+상대 경로다. 이름은 비워 두면 서버가 정한다. 첫 화면은 모든 사용자에게 같고, 폴더 경로·Git 주소 입력과 분석 단추를 맨 위에, 프로젝트 목록을 그 아래 카드로 둔다. 상세는 이름·소스·내 역할을 머리에, 실행을 막는 기능 공백(`execution_unavailable`, `report_unavailable`)을 그 아래에 보이고, 소스 상태(등록만 확인한 소스는 미검증), 프로젝트 ID, 설정 리비전, `source_access_unverified`는 "프로젝트 정보"에 접어 둔다. 프로젝트를 고르면 최근 실행(링크가 가리킨 실행이 있으면 그 실행)을 바로 열고, 다른 실행을 보고 있으면 "최근 실행 보기"를 보인다. 아래에 실행 패널과 예약 패널을 둔다.

### 실행 상세

- 웹의 수동 실행은 `latest_at_acquire`로 소스를 고정한다. 기본 `deep-review`와 허용된 `with-tests`를 선택할 수 있다. 범위는 전체 또는 입력한 경로이며, 코드를 모델에 보낼 수 있으면 확장 검토를 선택할 수 있다. 응답을 받지 못해 같은 요청을 다시 보내면 Idempotency-Key를 재사용한다. [검토 범위](REVIEW.md#모듈-단위-검토-분할)
- 실행 상태는 글자 배지로 보인다. 알 수 없는 상태는 원문과 안내를 함께 보인다.
- 상태 배지, 추정 백분율(`progress` 요소와 숫자), 종료 task 수/전체 수, 대기 사유, 품질 판정, 단계 목록을 먼저 보여주고, 실행 ID·version·요청(직접 요청 또는 예약 시각)·snapshot·계획 revision과 종료 가중치/계획 가중치·가중치 근거·분모 변경 이력과 사유는 "실행 세부 정보"에 접어 둔다. 다시 시도·취소 단추는 보고서보다 앞에 있다. 계획이 확정된 경우에만 "종료 수/계획 수"를 보이고 성공 수는 따로 보인다. 확정 전이면 "계획 미확정"이다.
- 단계 목록은 단계마다 실제 버튼으로 펼친다(키보드 조작). 펼치면 종료 수/계획 수, 성공 수, 시작·종료 시각을 보인다. 단계 옆에 종료 상태별 건수와 사유 문구를 붙인다. 마지막 변화 시각과 서버 확인 시각은 "3분 전"처럼 상대 시각으로 보이고 정확한 시각은 마우스를 올리면 보인다. 마지막 연결 확인 시각을 함께 보인다.
- 갱신 방식(실시간 연결, 연결 중, polling 대체, 숨김으로 일시 중지, 5초 polling)과 마지막 확인 시각을 글자로 보인다. 연결이 끊기면 마지막 확인 시각을 유지한다. 종료된 실행은 자동 갱신을 멈춘다.
- 취소는 확인 단계를 거친다. 취소 요청 뒤에는 "취소 요청됨"을 보인다. 취소 응답은 실제 프로세스 종료를 뜻하지 않으므로 상태 갱신으로 결과를 확인한다.

### 보고서 패널

종료된 실행에 게시 보고서가 있으면 최신 리비전의 코드 포함 JSON을 자동으로 읽는다(같은 프로젝트 읽기 권한자 전용). 프로젝트와 실행을 함께 지정한 공유 링크에서는 보고서가 화면에 그려진 뒤 제목으로 초점을 옮기고 종합의 시작을 표시한다. 사용자가 먼저 다른 곳으로 이동하거나 화면을 조작하면 현재 위치와 초점을 유지한다. 브라우저 탭이 숨겨져 있으면 화면이 다시 보일 때 한 번 이동한다. 실행 중에는 진행 상태를, 보고서를 읽을 수 없으면 조회 오류와 다시 가져오기를 표시한다.

보고서를 열면 작업 영역의 폭을 사용한다. **목록 열기**에서 다른 프로젝트나 최근 보고서를 선택한다. 종합 표의 상태는 글자와 도형, 테두리 모양으로 구분한다. 비밀정보 행은 탐지가 보고되면 **탐지 있음**, 검사를 완료하고 탐지가 0건이면 **탐지 없음**을 표시한다. 검사 범위에서 Git 이력이 빠진 경우 함께 적는다. 영역 탐색에는 이름과 기록된 대표 수치를 표시한다.

코드 분석에서는 표의 항목을 선택해 위치와 근거를 확인한다. 넓은 화면은 목록 옆에서 상세를 읽고 좁은 화면은 선택한 결과만 표시하고 **결과 목록**으로 돌아간다. 키보드로 행을 골라 열 수 있다. 발췌와 저장 원문을 전환할 수 있다. 좁은 화면에서 긴 줄은 기본으로 접어 표시하고 **줄바꿈**을 끄면 가로로 스크롤할 수 있다. 웹과 HTML에는 기록된 시각을 UTC로 표시한다. 짧게 표시한 발췌와 넓은 인용 범위는 따로 적는다.

| 읽는 순서 | 화면에서 볼 수 있는 내용 |
|---|---|
| 종합 | 표지에서 등록 경로, 상태, 시각과 커밋을 읽고 소개는 필요할 때 펼친다. 바로 아래에 분석하지 못한 범위 한 줄, 조치 항목 카드, 문서와 구현이 다른 곳, 함께 확인할 것, 접힌 후보와 도구 진단이 오고([조치 항목 우선 배치](#조치-항목-우선-배치)) 카드의 단추로 코드 분석·문서 분석 탭의 해당 근거로 이동한다. 그 뒤의 결과 표에서 영역별 집계를 읽고 상세로 이동한다. **문서 대조와 판단 근거** 또는 **분석 상태와 통계**를 펼쳐 근거와 작업 기록을 더 읽을 수 있다. [종합 화면](../web/src/ReportOverviewTable.tsx) |
| 환경·문서 | 언어·모듈·의존성, 문서가 약속한 동작과 대조한 구현을 읽는다. Python docstring은 실제 `.py` 줄로, npm 예제는 해당 안내와 진입점으로 연결한다. [분석 종류별 절](#분석-종류별-절) |
| 정적 분석·구조 지표 | 도구 실행 상태, 규칙과 처리 범위를 살핀다. 지표 절에서 복잡도·중복의 근거 줄과 Go 패키지 관계를 연다. [분석 종류별 절](#분석-종류별-절) |
| 코드 분석 | 주요 경로의 발견을 먼저 보여 준다. 경로와 줄을 열고 고정 Git 커밋으로 이동할 수 있다. 긴 목록과 부차 경로는 접혀 있고 선택한 절의 사람 판정 필터를 적용할 수 있다. [발견 항목 읽기](#발견-항목-읽기) |
| 테스트 | `with-tests`의 작업별 상태, 개별 케이스 수와 확인된 실패 위치를 연다. [테스트 결과](#테스트-결과) |
| 부록·비교 | 부록에 검토 호출과 범위, 작업, 원시 관찰과 한계를 모은다. 웹의 **이전 실행과 비교**에서 두 실행의 차이를 확인한다. [run 비교](#run-비교) |
| 내보내기 | 현재 리비전의 JSON·Markdown·HTML을 받는다. 코드 포함 여부와 요약·전체 출력을 선택할 수 있으며, 처음에는 코드가 제외된다. [내보내기 상세도](#내보내기-상세도) |

웹의 **보고서 검색**은 현재 리비전의 문서 대조와 발견에서 제목·경로·규칙을 찾고 해당 근거로 이동한다. 코드 포함 상태에서는 관련 서술과 저장 발췌도 검색한다. [검색 화면](../web/src/ReportSearch.tsx)

넓은 화면은 옆에, 좁은 화면은 위쪽에 탭을 둔다. 방향키와 Home·End로 탭을 선택한다. 프로젝트 정보가 없는 이전 보고서는 절별로 펼쳐 본다. 절도 없는 리비전은 기존 집계·발견·작업 표를 표시한다. 저장된 일곱 블록의 분할이나 집계가 맞지 않으면 응답 형식 오류를 표시한다. [화면 구성](../web/src/ReportSections.tsx) · [데이터 검사](../web/src/parseReportSections.ts)

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

서비스의 **도구** 화면은 설치된 분석기와 사용 가능한 모델 검토를 보여 준다. 관리자는 **OpenCode 연결**을 열어 서비스 머신의 경로를 저장하고 모델 응답을 확인한다. [관리자 설정](GUIDE.md#관리자-화면에서-연결하기)

`codeye doctor --format json`이 만든 파일(최대 1 MiB)을 브라우저 안에서만 읽어 호스트와 검사 결과 표를 보여준다. 업로드·저장하지 않는다. 검증기는 빌드 시 스키마에서 생성하며 브라우저에서 `eval`·`new Function`을 쓰지 않는다.

## 설계 계약(미구현)

다음은 화면·보고서 설계 목표이며 현재 구현되어 있지 않다.

- **추가 운영 화면.** GitHub App 등록 연결 진단, 운영자 노드·풀·도구 설정. 풀·노드 관리는 현재 CLI에서 하고 모델 연결은 웹 관리자 화면에서 설정한다. 등록의 설명·첨부·관심 영역과 자동 탐색의 후속 조정은 선택 기능으로 설계한다.
- **결함 영역별 표시.** 정확성, 오류·취소, 동시성, 자원 수명, 데이터·인터페이스 계약, 소스 보안, 구조·유지보수성, 성능 구조, 테스트 공백 축마다 발견뿐 아니라 수행한 검사·검토와 제한을 표시하고 수행하지 않은 영역을 녹색으로 칠하지 않는다. 지금 보고서는 [분석 종류별 절](#분석-종류별-절)로 나누고 발견을 category로 묶을 뿐이다. 종합의 신규·지속 항목 수도 저장 문서에는 없고 화면의 비교 결과에서만 읽는다.
- **코드 뷰.** 기본 코드 뷰는 분석한 snapshot이며 현재 브랜치의 최신 파일로 자동 연결하지 않는다. 파일·행·심볼·related flow를 연결하고 원시 진단과 해석을 펼친다. snapshot이 만료되면 source unavailable을 표시하고 다른 버전 소스로 대체하지 않는다. 흐름이 도구가 생성한 경로인지 모델이 추정한 경로인지 표시한다. 비밀정보 발견은 마스킹된 위치와 조치만 보여준다.
- **판정 일괄 적용, 판정 감사 이력 화면, 이동 후보 판정 이전, 첫 발견 시각.**

## 접근성

구현된 것:

- 색상 외에 글자·기호로 상태를 구분한다(NFR-005).
- 모든 조작은 키보드로 할 수 있다. 펼침은 실제 버튼(`aria-expanded`)이나 `details` 요소를 쓰고, 양식은 native 요소를 쓴다. 본문 바로가기 링크를 둔다.
- 상태 변화는 `role="status"`·`aria-live`로, 오류는 `role="alert"`로 알린다.
- 긴 코드와 표는 가로 스크롤 영역에 둔다. 보고서의 발견은 검색어로 거를 수 있다. 분석 종류별 절은 `details` 요소이고 종합의 절 버튼은 실제 버튼이다.
- 조회 실패에는 재시도 경로를 둔다.
- 화면과 HTML 내보내기는 외부 CDN·폰트를 쓰지 않는다. 첫 화면에는 차트 대신 실행 상태와 행동 우선순위를 둔다.
- 화면의 색 대비. 스타일시트([`style.css`](../web/src/style.css))의 색 값으로 계산해 본문·표지 글자는 배경과 4.5:1 이상(가장 낮은 조합은 연한 초록 배경의 성공 표지 5.7:1), 입력 테두리와 초점 표시는 3:1 이상이다. 화면을 측정 도구로 잰 값은 아니다. 초점은 모든 단추·링크·입력·펼침 요소에 3px 테두리로 보인다.

요구(검증하지 않았거나 미구현):

- HTML 내보내기의 대비. 화면 스타일시트만 계산했다.
- 큰 표와 목록의 페이지 처리. 프로젝트 목록만 "더 보기" cursor 방식이고 보고서의 표는 나누지 않는다.
- 그래프를 넣을 때는 표 대안을 둔다. 지금 화면에는 그래프가 없다.

## 사용자 수용 기준

예시 보고서를 처음 보는 개발자가 짧게 읽고 핵심 위험, 근거, 미확인 범위, 수정·검증 방법을 찾을 수 있는지로 평가한다. 읽기 시간을 실측하기 전에는 보장치로 쓰지 않는다. 보고서 장식이나 경고 수를 전달 품질로 평가하지 않는다. 출력 구조의 기준 예시는 [`examples/report.md`](../examples/report.md)(합성 예시)다.

관련 요구사항·수용 시험: REQ-028, REQ-029, NFR-005, NFR-006(판정 감사 기록 일부), TC-14, TC-31(코드 미포함 내보내기에 식별 없음), TC-80~TC-84(게시·판정 규칙·재생성 동일성·동시 게시·권한과 escaping), TC-90~TC-92(근거 보고서와 화면), TC-98(대표 실행과 단계 표시), TC-130~TC-134(분석 종류별 절). 요구사항 정의는 [요구사항](REQUIREMENTS.md), 수용 시험 정의는 [시험](TESTING.md)에 있다.

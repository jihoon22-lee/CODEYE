# R0 QA 전용 OpenAI 예산 relay

> **QA 전용·제품 경로 아님.** 이 relay는 사람이 승인한 R0 OpenCode 연동 시험(W10)을 위해 유료 모델 호출 상한을 강제하는 보조 도구다. CODEYE의 모델 연결 방식(D10: 허용된 OpenCode 우선, A12: 자동 fallback 금지)을 바꾸지 않으며, 제품 런타임·CI 의존성이 아니다. 아래 단위 시험은 로컬 합성 응답을 사용한다. 실제 연결 증거는 [W10 실행 기록](32-w10-opencode.md)에 있다.

## 승인 범위

- 모델: `gpt-6.2-sol` 또는 `gpt-6.1-sol`만 허용한다.
- 예산: 한 R0 실행의 모든 relay 프로세스 합계 최대 **USD 9.99**(`HARD_CAP_MICROUSD = 9_990_000`). 별도 직접 probe에 남긴 USD 0.01을 더해 총 USD 10 이하로 유지한다.
- 키: 운영자 셸의 `OPENAI_API_KEY`. relay만 읽으며 시작 시 환경에서 제거하고 파일·로그·응답에 쓰지 않는다.
- 실제 호출은 상위 작업자가 수행한다. 이 저장소의 테스트는 과금 호출을 하지 않는다.

## 실행

```bash
python3 scripts/r0_openai_gateway.py \
  --ready-file "$R0_DIR/ready-$(date +%s).json" \
  --ledger "$R0_DIR/ledger.jsonl" \
  --model gpt-6.2-sol            # --budget-usd 기본 9.99, 그 이하만 허용
```

| 옵션 | 기본 | 제한 |
|---|---|---|
| `--budget-usd` | 9.99 | 0 초과 9.99 이하, 소수 6자리까지. 기존 ledger 상한을 낮출 수만 있다 |
| `--max-requests` | 200 | 1~1000. ledger 전체 요청 수 상한이며 낮출 수만 있다 |
| `--max-input-bytes` | 49152 | 1~65536. 요청당 메시지 텍스트 UTF-8 바이트 합 |
| `--port` | 0(임시) | 항상 127.0.0.1에만 bind |
| `--forbidden-phrases` | 없음 | W24 정답 목록(`corpus.py forbidden`). 주면 일치 요청을 막는다. 파일이 없거나 형식이 틀리면 종료 코드 2. 감시용 목록(`purpose: envelope_capture`)은 `--test-upstream`에서만 받는다 |
| `--allow-model-echo` | 꺼짐 | W24 선택. 이 프로세스가 돌려준 상위 답에 나온 목록 ID는 이후 요청에서 막지 않고 `reserve`에 `echo_matches`로 남긴다. 목록 없이 주면 종료 코드 2 |
| `--test-upstream` | 없음 | **dry run 전용.** `http://127.0.0.1:PORT`만 받는다. 키를 읽지 않고 환경에서 지운다. 장부 header에 `upstream: test-loopback`을 남기고 실제 장부와 섞지 않는다 |

- **ledger는 R0 전체에서 하나의 영구 경로**(`/tmp` 같은 휘발 경로 금지)를 쓴다. ledger를 잃거나 새 경로를 쓰면 새 USD 9.99 예산이 생기므로, 예산 보장은 같은 ledger 파일을 유지하는 운영 절차에 의존한다.
- ready file은 반드시 새 경로여야 한다(이미 있으면 종료 코드 2). 0600으로 원자적으로 만들고 내용은 `{"base_url":"http://127.0.0.1:PORT/v1","token":"...","model":"gpt-6.2-sol","pid":N}`이다. SIGTERM/SIGINT 정상 종료 시 삭제한다. SIGKILL 뒤 남은 파일은 무효 token을 담고 있으므로 지운 뒤 새 경로로 다시 시작한다.
- OpenCode는 OpenAI 호환 provider의 `baseURL`과 `apiKey`에 ready file의 `base_url`, `token`을, 모델 ID에 `model`을 그대로 쓴다.
- 보고: `python3 scripts/r0_openai_gateway.py --ledger PATH --report` (키 불필요).
- 정지 해제: `python3 scripts/r0_openai_gateway.py --ledger PATH --clear-halt "검토 내용"`. ledger에 감사 기록을 추가할 뿐 상한·청구액은 바꾸지 않는다. 원인을 확인한 사람만 실행한다.

## 로컬 경계

- `POST /v1/chat/completions`, `GET /v1/models`(로컬 응답, 상위 호출 없음)만 제공한다. 나머지는 404.
- 매 실행 임의 bearer token, peer 127.0.0.1, `Host`가 `127.0.0.1:PORT` 또는 `localhost:PORT`여야 한다. `Origin`·CORS preflight 헤더가 있으면 403이며 CORS 헤더를 보내지 않는다.
- 본문은 `Content-Length` 필수(chunked 거부), 1 MiB 이하, 헤더 합 16 KiB 이하, 읽기 30초. 한 프로세스에서 상위 호출은 동시에 1개이며 나머지는 최대 300초 대기 후 거부한다.
- 클라이언트 오류는 재시도되지 않는 상태 코드(400/401/402/403/404/411/413/415/424)만 사용한다. 408/409/429/5xx를 내지 않아 SDK 자동 재시도가 유료 호출을 늘리지 못하게 한다. 예산·요청 수·정지는 402, 상위 실패는 424.
- 오류 본문에는 relay 코드와 상위 HTTP 상태, 형식이 안전한 상위 `error.code`/`error.type`만 담는다. 상위 오류 메시지(키 일부를 포함할 수 있음)는 버린다.

## 상위 요청

- OpenCode 쪽은 `/v1/chat/completions` 그대로이고, relay→OpenAI 구간만 Responses API를 쓴다(실호출에서 Chat Completions payload가 `unsupported_parameter`로 거부되고 같은 계정·모델의 Responses 직접 호출이 성공한 결과에 따른 변경).
- 고정 대상: `https://api.openai.com/v1/responses`, `http.client` 직접 연결. 프록시 환경변수·redirect·재시도를 쓰지 않는다. 대상을 바꾸는 환경변수는 없다. 유일한 예외는 W24 dry run용 `--test-upstream`이다. loopback 주소만 받고, 그 밖의 값은 시작을 거부하며, 실제 대상으로 돌아가는 fallback이 없다.
- payload는 매번 새로 만든다: `model`, `input`(메시지를 순서대로 `{role, content: 문자열}`로 재구성, role `system|developer|user|assistant`), `max_output_tokens`(≤1536, 추론 토큰 포함, 클라이언트 `max_tokens`/`max_completion_tokens`와 최소값), `reasoning: {effort: "low"}`, `service_tier: "default"`, `store: false`. 응답은 하나이며 스트리밍하지 않는다.
- 응답에서는 assistant `message` 항목의 `output_text`(없으면 `refusal`)만 읽는다. reasoning 등 다른 출력 항목은 클라이언트에 보내지 않는다. `status: completed`는 `finish_reason: stop`, `incomplete`는 `max_output_tokens`→`length`, `content_filter`→`content_filter`이며 추론이 상한을 모두 쓰면 빈 content와 `length`로 200을 돌려준다. `failed`·`cancelled` 등은 424이고 예약 전액을 차감한다.
- content가 text part 배열이면 줄바꿈으로 합친다. 이미지·오디오·파일 part, `tool` role, `tool_calls`/`function_call`/`tool_call_id`가 있는 메시지는 400으로 거부한다. 메시지는 64개 이하.
- `tools`, `tool_choice`, `temperature`, cache·retention·tier 지정 등 그 밖의 최상위 키는 **버리고** 이름만 ledger `dropped_keys`에 남긴다. 따라서 OpenCode가 도구 정의를 보내도 모델은 도구 없이 텍스트로만 답한다.
- `stream: true` 요청은 한 번의 비스트리밍 결과로 `chat.completion.chunk` SSE(role → content → finish → `stream_options.include_usage`일 때 usage → `[DONE]`)를 만든다.
- 상위 전체 제한시간 240초(연결·전송·헤더 대기·본문 읽기 합계), 응답 1 MiB.
- 클라이언트가 요청을 취소하거나 연결을 끊어도 이미 보낸 상위 호출과 그 차감은 멈추지 않는다. 대기열의 요청도 최대 300초 뒤 전송될 수 있다. SIGTERM 시 진행 중 호출의 정산을 최대 250초 기다린 뒤 종료한다.

## 예산 ledger

- JSONL, 0600, 소유자 본인, symlink 거부. 모든 갱신은 `flock` 배타 잠금(프로세스 간)과 스레드 잠금 아래에서 전체를 다시 읽고 한 줄을 추가한 뒤 `fsync`한다.
- 기록: `header`(상한·가격 상수), `cap`(상한 하향), `reserve`, `settle`, `halt`, `clear_halt`, `repair`. 가격 상수가 다른 ledger는 거부한다.
- W24 추가 기록: `forbidden`(시작 시 정답 목록 SHA-256·항목 수·corpus digest), `blocked`(정답 차단, 일치 항목 ID·역할 표지, 차감 0), `refused`(예산·요청 수·정지로 거절, 차감 0). 기존 상태 계산은 이 기록을 무시한다. `reserve`에는 프롬프트의 `Role:` 줄에서 읽은 역할 표지가 붙는다. 자세한 내용은 [W24 평가](46-w24-evaluation.md#3-정답-차단)를 따른다.
- `reserve`는 상위 요청 전에 durable하게 기록한다. 마지막 줄이 줄바꿈 없이 끊겨 있으면 쓰기가 끝나지 않은 것이므로(그 요청은 전송되지 않았다) 잘라내고 `repair`를 남긴다. 중간 줄이 깨졌으면 동작을 거부한다.
- 금액은 정수 microUSD. 공식 standard 단기 컨텍스트 단가 입력 $2, 출력 $10 /1M과 캐시 쓰기 30분 $2.50, 12시간 $5 /1M 중 최악값을 쓴다.
  - 입력 토큰 상한 = 텍스트 UTF-8 바이트 + 메시지당 16 + 고정 256 (BPE 토큰은 최소 1바이트를 덮는다).
  - 예약 = 2 × (입력 상한 × 5 + 출력 상한 × 10) + 1000. 기본 최대 요청(48 KiB, 64메시지, 1536 출력)도 USD 0.6 미만이다.
  - 정산: 응답의 `usage.input_tokens`·`output_tokens`(추론 포함)가 정수이고 상한 안이며 `service_tier`가 `default`로 확인되면 `input × 5 + output × 10`. ledger에는 기존 형식 그대로 `prompt_tokens`=input, `completion_tokens`=output, `cached_tokens`, `reasoning_tokens`로 남긴다(가격 상수·기록 형식은 Chat Completions 시절 ledger와 호환). 그 밖의 모든 경우(usage 없음, tier 미표시, HTTP 오류, redirect, timeout, 전송 오류, 응답 이상, relay 오류)는 예약 전액이다. 미정산(충돌·SIGKILL·동시 실행 중) 예약도 전액으로 센다.
- 승인: `정산액 합 + 미정산 예약 합 + 새 예약 > 상한`이면 상위 요청 없이 402.
- 정지(halt): 연속 실패 3회, usage 상한 초과, 응답 모델이 요청 모델(또는 그 날짜 snapshot)과 다름, `service_tier`가 `default`가 아님. ledger에 기록되어 재시작 뒤에도 유지된다.

## 보고 필드

| 필드 | 의미 |
|---|---|
| `reserved_microusd_total` | 지금까지 예약한 총액(최대 노출) |
| `settled_charged_microusd` / `pending_reserved_microusd` | 정산된 보수적 차감액 / 미정산 예약 |
| `charged_microusd_total` | 예산 차감 기준(위 둘의 합), `remaining_microusd`는 상한과의 차 |
| `usage` | 응답이 보고한 prompt/completion/cached/reasoning 토큰 합 |
| `standard_rate_estimate_microusd` | 보고 usage × $2/$10 정보용 추정. 청구서가 아니며 승인에 쓰지 않는다 |
| `settled_by_status`, `halted`, `halt_reasons` | 결과 분포와 정지 상태 |
| `upstream`, `forbidden_lists`, `blocked_answer_leaks`, `blocked_by_role`, `echo_exempted_requests`, `echo_exempted_by_role`, `refused_by_code` | W24: 장부 종류(시험용이면 `test-loopback`), 읽은 정답 목록과 `allow_model_echo`, 차단·예외·거절 수 |

## 검증

```bash
python3 -m unittest discover -s scripts -p 'test_r0_*.py' -v
```

로컬 가짜 OpenAI(Responses API 형식) 서버와 가짜 키·reasoning canary로 25개 시험을 수행한다: 예산 파싱·$9.99 상한, 예약 계산, payload 재구성과 거부, 고정 HTTPS 생성자(기록기), ledger 상한 비증가·재시작·찢긴 줄 복구·손상 거부·권한, 6개 프로세스 동시 예약의 상한 준수, 연속 실패 정지와 해제, HTTP 경계(token·Origin·Host·크기·chunked·미지원 method), 오류 비노출과 전액 차감, usage 누락, tier/usage 이상 정지, 예산 소진, 전체 제한시간, SSE 형식, reasoning 비노출, incomplete/failed 응답, `unsupported_parameter` 오류의 param만 노출, tier 미표시·모델 불일치, CLI ready file·종료·키 비노출, 요청 중 SIGKILL 뒤 재시작 시 전액 차감. 테스트는 실제 `OPENAI_API_KEY`와 프록시 변수를 자식 환경에서 제거한다. W24에서 정답 차단·모델 표현 예외·corpus 목록·시험용 upstream 시험 12개를 더했다(합 37개). CI(`verify.yml`)가 이 시험을 실행한다.

## 남은 제한

- Responses API 경로는 상위 작업자의 직접 호출 1회 성공만 근거로 하며 relay 경유 실호출은 아직 없다. 실제 응답 형식(`model` 문자열, `service_tier` 표시, usage 필드), 실제 청구액과 usage의 일치, OpenCode가 보내는 실제 요청 형식·크기·동시성은 미검증이다. 첫 실호출 결과로 확인한다.
- 바이트 기반 토큰 상한은 BPE 토크나이저 가정에 기댄다. 상한을 넘는 usage가 보이면 즉시 정지하도록 했다.
- 예약 정산은 응답의 usage를 신뢰한다. usage가 없거나 형식이 다르면 예약 전액으로 남겨 실제보다 크게 차감한다.
- `fcntl` 사용으로 Linux/macOS 전용이다. 같은 ledger를 공유하는 프로세스는 같은 파일시스템의 로컬 잠금을 써야 한다(NFS 잠금은 보장하지 않음).
- `clear_halt`는 사람의 판단을 전제로 한다. 정지 원인을 고치지 않고 해제하면 실패 차감이 계속 쌓인다(상한은 그대로 유지).

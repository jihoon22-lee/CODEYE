# 04. 분석 파이프라인과 상태 전이

## 1. 단계와 산출물

| 단계 | 산출물 | 주요 실패 처리 |
|---|---|---|
| acquire | snapshot, source SHA, manifest | 접근 실패/불안정 복사면 failed |
| discover | 모듈 지도·환경 사실·문서 맥락 | 문서 없음은 정상, 읽지 못한 범위만 표시 |
| plan | revision 있는 DAG, 목적·scope·예산 | 미지원 검사를 숨기지 않고 계획 이유 기록 |
| prepare | 실행 사본·런타임·도구 manifest | 해당 task blocked, 나머지 가능한 검사 계속 |
| analyze | 도구 observation, 맥락 중심 hypothesis | task별 제한시간과 부분 실패 |
| verify | 지지·기각·보류와 counterevidence | 예산 종료 시 미검증 상태 유지 |
| synthesize | finding group, risk/coverage ledger | 무효 증거를 참조한 결론은 게시하지 않음 |
| report | 구조화 보고서·서술·export | 서술 실패 시 데이터 기반 대체 보고서 |
| finalize | artifact finalize, 프로세스 종료 확인 | 미완료 정리는 recovery_required |

## 2. 상태의 분리

`run.status`: queued → preparing → running → finalizing → completed/partial/failed. 어느 활성 상태에서든 cancel_requested → cancelling → cancelled로 갈 수 있다. queued는 실행 자식이 없음을 확인한 뒤 cancelled로 바로 종료할 수 있다. lease 상실·정리 불확실 시 recovery_required다.

`completed`는 요청한 필수 작업이 성공하고 산출물이 게시된 상태다. `partial`은 읽을 수 있는 결과가 있으나 필수 작업 일부가 수행되지 않은 상태다. `failed`는 입력 확보 실패 등 의미 있는 분석 결과를 만들지 못한 상태다. 선택 작업 실패도 보고서에 노출하며 완료 상태 판정 규칙을 설정 revision에 기록한다.

`quality_verdict`: attention_required / no_actionable_findings / inconclusive. 필수 범위가 빠지면 결함 후보가 있더라도 완전한 품질 판정으로 표현하지 않는다. 주요 발견과 판정 보류를 동시에 표시할 수 있다. 이 필드는 안전 인증이 아니다.

`task.status`: queued, blocked, running, succeeded, failed, timed_out, cancelled, unsupported, skipped_policy, budget_exhausted. blocked는 선행조건을 기다리는 비종료 상태다. 최종 선행조건 실패 시 skipped_policy 또는 failed로 전환하고 reason=dependency_failed를 남긴다. unsupported와 skipped는 성공 검사 수에 넣지 않는다.

`finding.validation`: unreviewed, supported, reproduced, refuted, inconclusive. `disposition`: open, acknowledged, accepted_risk, false_positive, fixed_by_user, suppressed. 검증 상태와 사용자 처리 상태는 독립적이다.

## 3. DAG 계약

각 task는 ID, kind, purpose, scope, depends_on, required_for_verdict, capabilities, resource_request, budget, completion_contract를 갖는다. task ID는 run 안에서 유일하고 순환이 없어야 한다. plan_revision은 추가 검사나 범위 변경 때 증가하며 기존 task ID와 완료 이력은 유지한다.

동일 workspace를 수정하는 prepare/build는 write lease를 갖고 병렬화하지 않는다. 읽기 전용 분석끼리는 병렬화할 수 있다. 테스트·빌드가 파일을 바꾸면 독립 workspace 또는 단계 간 source-drift 검사를 사용한다. 여러 task가 같은 파일의 변경된 버전을 원본처럼 읽는 것을 금지한다.

## 4. 독립 탐색과 도구 해석

맥락 중심 검토에는 진단 목록을 보기 전의 예산을 보장한다. 진단 중심 검토는 발생 조건·영향을 확장한다. 이후 verify가 두 경로를 교차 검토한다. 역할 분리는 동일 모델의 판단을 통계적으로 독립되게 만드는 장치가 아니다.

LLM이 추가 검사를 제안하면 등록된 도구·예산·프로젝트 실행 권한 안에서 task로 변환한다. 자유 shell 명령을 관리 서버에서 바로 실행하지 않는다. 수행할 수 없는 제안은 reason과 함께 남긴다.

## 5. 실패와 재시도

| 상황 | 정책 |
|---|---|
| findings를 뜻하는 종료 코드 | 어댑터의 코드 매핑에 따라 succeeded+observations |
| parser 오류 | raw 보존, parser_error, 0건 주장 금지 |
| 모델 timeout/429 | 제한된 지수 backoff, 예산 소진 시 partial |
| 잘못된 JSON/증거 참조 | 오류 항목만 전달해 1회 형식 복구, 계속 실패하면 격리 |
| 노드 lease 만료 | fenced, recovery_required. 부작용 가능한 작업은 종료 확인 전 재시도 금지 |
| Git 댓글 실패 | 보고서 유지, outbox만 재시도 |
| 원본과 workspace의 불일치 | 변경 파일과 이유 기록, stale 근거 invalid 처리 |

재시도 attempt는 이미 고정한 snapshot과 설정을 유지한다. 입력을 새로 가져오려면 새 run을 만든다. snapshot이 보관 기간 만료로 없으면 같은 입력 재실행을 명시적으로 거부한다.

## 6. 캐시

도구 키는 snapshot/module/tool version/rule digest/target runtime·ABI/flags/lock digest/adapter version/environment policy다. 모델 키에는 pack digest/model identity·가용 revision/decoding/context hashes를 더한다. 모델 revision을 모르면 unknown을 저장하고 TTL로 제한한다. 동일 입력도 비결정적 모델 출력의 bitwise 재현은 보장하지 않는다.

실패·부분 파싱을 성공 캐시로 쓰지 않는다. 프로젝트별 namespace와 권한을 적용한다. 소스가 같아도 지침·규칙·모델이 바뀌면 재검토한다. 이전 결과를 재사용했다면 실행 날짜와 재사용 근거를 사용자에게 보여준다.

## 7. 종료 조건

requested task가 모두 종료 상태이고, 참조 artifact가 finalize되고, worker가 소유한 프로세스 종료 결과를 보고했을 때만 완료를 확정한다. 완료 상태 판정과 보고서 생성은 서버가 수행한다. LLM이 '끝났다'고 썼다는 이유로 완료하지 않는다.


## 8. W02 재시도와 실행 이력

`failed`/`cancelled`는 한 attempt의 종료 이후 명시적 retry 서비스로만 queued가 될 수 있다. 고정 snapshot과 모든 attempt의 confirmed cleanup이 필요하다. 일반 상태 전이 함수로 terminal을 되돌리지 않는다. completed/partial 재시도 정책은 후속에서 추가한다. 만료 attempt는 recovery_required 소유 슬롯을 유지하고 cleanup 확인 후 failed로 정리한다. task의 현재 상태와 attempt별 task_executions를 분리해 재시도가 과거 실패·취소 이력을 지우지 않게 한다.

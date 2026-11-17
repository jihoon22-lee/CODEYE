# 22. W02 착수 준비: 도메인 계약과 PostgreSQL

상태: 준비 계획 · 구현/시험 미실행. W01 PR 검토 후 다음 작업으로 사용한다. W02 완료 표시나 W03 API 착수 승인이 아니다.

## 1. 목적과 선행조건

W03의 프로젝트 등록→run 영속화→상태 조회, W04의 입력 고정, W06의 소유권·fencing을 재사용 가능한 저장 계약으로 준비한다. 프로세스 전역 map이나 SQLite 대체 구현을 거치지 않고 PostgreSQL 트랜잭션으로 검증한다.

선행은 W01 실행 기반과 실제 Native PostgreSQL 테스트다. Windows 11/RHEL/OpenCode 능력 미확정은 DB 계약 착수를 막지 않으며 해당 플랫폼·모델의 지원 완료로 해석하지 않는다. 모델을 호출할 필요가 없는 단계다.

설계 근거는 [상태 전이](04-analysis-pipeline.md), [스케줄링·lease](09-workers-and-scheduling.md), [데이터 모델](10-data-model.md), [API 계약](11-api-and-events.md), [수용 테스트](18-acceptance-and-traceability.md)다.

## 2. 구현 순서와 산출물

| 순서 | 범위 | 산출물·검증 |
|---|---|---|
| W02-1 | 도메인 계약 명시 | run/task/attempt 상태, cleanup 상태, 허용 전이, 오류 분류, actor/project/node/attempt 식별 경계. docs·OpenAPI·Schema와 대조 |
| W02-2 | migration 기반 | `migrations/`의 버전·checksum, 적용 이력, 동시 적용 잠금, 트랜잭션 실패 rollback, 알 수 없는 미래 버전 거부 |
| W02-3 | 프로젝트와 ACL | users·project_members·projects·immutable project_revisions, actor가 명시된 저장 서비스, 다른 프로젝트 조회/변경 차단 |
| W02-4 | durable run과 event | 프로젝트 revision 동결, 멱등 키/body digest, version 기반 갱신, 상태 변경과 event 삽입의 원자성 |
| W02-5 | attempt·lease 저장 계약 | run별 활성 소유권 유일성, epoch·DB 시각 기반 만료, owner 확인과 변경의 동일 트랜잭션, 오래된 쓰기 거부 |
| W02-6 | task·snapshot·evidence 참조 | run/attempt/project 범위 FK, snapshot null→고정 ID 한 번, task 상태와 원시 근거 metadata, 잘못된 범위 연결 거부 |
| W02-7 | 회귀·추적표 | 실제 PostgreSQL 동시성/rollback 시험, 재접속 복구, 계약 예시·오류 문서·실행 결과 갱신 |

주 모듈은 `internal/domain`, `internal/storage`, `migrations`다. DB 드라이버와 migration 라이브러리는 착수 시 실제 Go/PG 버전·TLS·취소 동작을 확인하고 고정한다. W01 psql 진단을 제품 저장 계층으로 재사용할 필요는 없다.

대형 source/log/artifact bytes는 DB에 저장하지 않는다. snapshot/artifact/node/worker는 참조 무결성을 위해 필요한 metadata와 identity만 다룬다. 등록·인증 protocol, 파일 확보/업로드, worker 실행을 가짜 성공 구현으로 채우지 않는다.

## 3. 트랜잭션 계약과 실패 시나리오

- **프로젝트 생성:** owner·ACL·첫 revision이 함께 생성되거나 모두 rollback된다. 설정 변경은 새 revision을 만들고 기존 run의 revision은 변경하지 않는다.
- **run 생성:** actor 권한 검사, revision 선택, `(project_id, actor_id, idempotency_key)` 유일성과 body digest, queued 상태, 최초 event가 함께 저장된다. 동일 요청은 같은 run을 반환하고 다른 body는 충돌이다. DB 오류 후 성공 응답을 내보내는 API는 W03에서도 금지한다.
- **상태/event:** 상태 version과 event sequence는 별도 개념으로 유지한다. 한 version 변경에 여러 event가 생겨도 sequence가 중복되지 않아야 한다. event 삽입 실패 시 상태도 rollback한다. 단순 PostgreSQL sequence의 gapless 성질을 가정하지 않는다.
- **소유권/fencing:** `(run_id, attempt_number)` 유일성 및 활성 소유 attempt 최대 한 개를 DB에서 강제한다. owner/epoch/만료/허용 상태 검사와 쓰기를 같은 트랜잭션으로 묶어 검사 직후 만료·재할당 경합을 다룬다. 만료는 DB 시각으로 판단한다.
- **종료/재시도:** `recovery_required`를 일반 대기열에 되돌리지 않는다. cleanup 불확실 상태는 완료나 자동 재시도 조건이 아니다. terminal 변경과 동일 메시지 재전송을 구분하고 이전 attempt가 최신 상태를 덮지 못하게 한다.
- **참조 무결성:** 존재하는 ID라도 다른 project/run의 snapshot/task/artifact/evidence 연결을 거부한다. evidence의 위치·hash metadata 저장은 실제 파일 존재/행 범위 검증 완료와 별개다. 실제 수집·내용 검증은 W04/W08/W12에서 이어간다.

프로덕션 claim은 docs/09에 따라 capacity 예약도 포함해야 한다. W02 소유권 원자성 시험을 scheduler/resource admission 구현으로 발표하지 않는다. W06/W07에서 capacity 예약을 같은 트랜잭션에 연결하고 두 실제 agent·자동 증감 시험을 수행한다.

## 4. W02에서 구체화할 미확정 항목

현재 설계의 방향 충돌은 없지만 아래 값은 migration/코드 작성 전에 같은 변경 안에서 확정해야 한다.

1. attempt 내부 상태 enum과 `recovery_required`의 활성 소유권 해제 조건. run/task enum을 attempt에 그대로 복사하지 않는다.
2. event sequence 할당과 entity version 증가 규칙, 상태를 바꾸지 않는 재전송/진행 event의 처리.
3. 완료 메시지 재전송의 인증·동일 payload 확인 순서와 이미 만료된 epoch의 응답 규칙. 멱등 응답이 오래된 결과를 다시 게시하는 경로가 되지 않아야 한다.
4. migration의 실행 명령·운영 DB role·지원 PG 버전. doctor나 서버 시작에서 사용자 DB를 조용히 변경하지 않는다.
5. 소스 확보 전 snapshot=null인 run의 실패/취소 전이. 보고서가 없고 실제 task가 시작되지 않은 경우의 종료 증거를 명시한다.

확정 시 결정 기록·관련 docs·API/Schema·fixture를 함께 수정한다. 소유자가 확정한 제품 방향을 바꿀 필요는 없다.

## 5. 요구사항·시험 연결

다음은 구현할 시험 목록이며 아직 통과 결과가 아니다. W02 저장 계층 시험과 제품 전체 TC를 구분한다.

| 요구 | W02 검증 | 이어지는 제품 TC |
|---|---|---|
| REQ-002 | 프로젝트 ACL, 권한 철회 후 재조회·변경 거부, 다른 프로젝트 ID 참조 거부 | TC-02,31: HTTP/SSE/export는 W03 이후 |
| REQ-003 | 설정 revision 불변, snapshot 최초 연결 및 동일 값 재전송, 다른 값 거부 | TC-03,04: 실제 Git/local 확보는 W04 |
| REQ-013 | enum/허용 전이, 미실행·실패를 succeeded/completed로 승격하지 않음 | TC-12,13: 보고서는 W09 이후 |
| REQ-022,035 | 별도 DB 연결의 동시 소유권 경쟁, expired/stale epoch 쓰기 차단, rollback, 동일 입력 재시도 | TC-21,22,23: agent·프로세스 시험은 W06/W07 |
| NFR-002,003 | 멱등 enqueue, 트랜잭션 실패 원자성, 저장 서비스 재생성·재접속 후 데이터 복원 | TC-21,23: 202 응답/서비스 복구는 W03 이후 |
| NFR-007 | finalized metadata와 scope 참조 제약 | TC-22,32: 실제 bytes/hash/finalize·복원은 후속 |

동시성 시험은 하나의 connection을 순서대로 호출하는 방식으로 대체하지 않는다. 별도 연결·barrier를 사용하고 실제 PostgreSQL에서 결과와 rollback 후 행 수/event를 확인한다. SQLite/mock 통과는 DB 수용 증거가 아니다.

## 6. 완료 기준과 운영 영향

빈 DB migration·재적용·checksum 변경 감지·실패 rollback·동시 migration, ACL, 멱등 enqueue, 상태/event 원자성, 활성 attempt 유일성·fencing, snapshot/근거 scope를 실제 DB에서 검증해야 W02를 완료한다. DB가 없으면 integration 미실행으로 기록하며 CI에서는 실제 DB 시험이 필수다.

관측 결과에는 적용 migration 버전, 테스트명, DB/Go/OS 버전, SQLSTATE 기반 오류 분류와 실제 실행 여부를 남긴다. DSN·password·source 본문은 남기지 않는다. 기존 운영 데이터가 생긴 이후 파괴적인 down migration을 기본 복구 방법으로 사용하지 않는다. 초기 개발은 별도 임시 DB에서 재생성하고, 배포 migration은 forward 수정·백업/복구 절차를 함께 검토한다.

이번 준비에서는 W02 코드를 작성하거나 migration을 실행하지 않는다. W03 인증 API, W04 SourceAdapter, W05 프로세스 실행, W06/W07 worker·autoscale, W10 모델 연결은 각 선행조건과 별도 완료 증거를 따른다.

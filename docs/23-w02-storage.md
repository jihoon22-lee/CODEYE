# 23. W02 도메인과 PostgreSQL 저장 계약

상태: W02 구현·로컬 실제 DB 검증 완료 · 2026-11-18. W03 HTTP 인증·W06 노드 protocol·W09 보고서 게시를 구현한 것은 아니다.

## 1. 구현 범위

- `internal/domain`: run/task 상태·전이, 역할, UUID, JSON canonicalization, 근거 상대 경로 검증, 공개 오류 분류.
- `migrations`: 내장 SQL과 SHA-256 이력, 동일 DB의 동시 적용 잠금, 한 트랜잭션 적용/rollback, 미래·누락 버전과 checksum 변경 거부.
- `internal/storage/postgres`: 프로젝트 ACL·immutable revision, 멱등 run, 상태/event 트랜잭션, attempt 소유권·lease·복구·재시도, task 실행 이력, snapshot·evidence metadata 무결성.
- `codeye migrate`: 운영자가 명시적으로 실행하는 schema 적용 명령. `doctor`와 저장 서비스 Open은 migration을 호출하지 않는다.

저장 서비스는 신뢰된 control plane 내부 경계다. actor ID는 W03 인증 계층이, node/worker identity와 cleanup 증거는 W06 실행 protocol이 검증한 뒤 전달해야 한다. `CreateUser`는 운영자 provisioning, `BeginAttempt`는 소유권 예약 계약이다. HTTP로 임의 ID를 전달해 사용하는 API가 아니며 capacity admission·등록된 노드 검사는 W06/W07에서 같은 트랜잭션 경계에 결합한다.

## 2. 연결과 migration

Go 의존성은 pgx/v5 5.13.0과 go.sum에 고정했다. PostgreSQL 16/17을 우선 시험 대상으로 삼고 다른 버전을 시험했다고 주장하지 않는다. Go/DB 패키지 다운로드는 명시적인 개발 준비 단계이며 실행 중 설치·외부 fallback은 없다.

운영자는 CODEYE 전용 DB와 schema owner 계정을 준비하고 `CODEYE_DATABASE_URL`을 프로세스 환경으로 전달한다. DSN을 명령행 인자나 저장소에 넣지 않는다.

```bash
go build -o bin/codeye ./cmd/codeye
./bin/codeye migrate
```

연결은 명시적인 PostgreSQL URL의 host/user/database가 필요하다. TLS 기본은 verify-full이며 평문은 literal loopback만 허용한다. 원치 않는 PGSERVICE·PGPASSWORD·PGOPTIONS 등을 상속하지 않도록 저장 계층은 `PG*` 환경이 있으면 거부한다. W01 doctor는 별도 최소 자식 환경을 사용한다. psql 설정, 개인 client certificate, passfile을 자동 사용하지 않는다. 승인된 root CA는 URL의 sslrootcert로 지정한다. 원시 DSN·SQL 오류 detail은 CLI/API 공개 오류로 보내지 않는다.

`migrate`는 최대 1분의 호출 제한으로 전용 DB의 public schema에 적용한다. schema 변경 권한은 이 명령에만 필요하다. 실서비스 DB 역할 최소 권한 배포는 W03 이후 운영 구성에서 검증한다. 원래 DB·사용자 권한을 자동 생성/변경하지 않는다.

migration 전체는 READ COMMITTED transaction과 `pg_advisory_xact_lock`을 사용한다. 이력은 `codeye_schema_migrations(version,name,checksum,applied_at)`다. 적용 이력과 바이너리 SQL이 맞지 않으면 멈춘다. 이미 공유·적용한 0001 파일은 수정하지 않고 다음 번호로 변경한다. 개발 시험은 별도 임시 schema/DB를 제거해 복원하며 운영 rollback은 데이터 파괴 down migration 대신 forward 수정과 backup/복구를 설계한다.

## 3. 상태와 동시성

run/task enum은 docs/04·[state Schema](../schemas/state.schema.json)·OpenAPI와 일치한다. [상태 예시](../examples/state.json)는 cleanup 불확실 시 task가 아직 running으로 남을 수 있음을 보여주는 합성 데이터다.

| 항목 | W02 결정 |
|---|---|
| attempt.status | leased / recovery_required / released |
| cleanup_state | pending / uncertain / confirmed. released에는 confirmed 필수 |
| 활성 소유권 | 한 run에서 leased 또는 recovery_required인 attempt 최대 한 개 |
| epoch | 재배정 때 증가. node/worker/attempt/epoch와 DB 시각을 잠금 안에서 검사 |
| 만료 | recovery_required로 fence. 프로세스 종료와 동의어가 아님 |
| cleanup 재조정 | 소유 노드의 확인을 받은 뒤 released·run failed. 이후 명시적 재시도 가능 |
| 재시도 | failed/cancelled + 보관 snapshot + 모든 attempt cleanup confirmed. 입력과 설정 revision 유지 |
| 완료 응답 재전송 | 동일 소유자·epoch·payload만 멱등 응답. 새 attempt나 queued 재시도 이후에는 수락하지 않음 |
| 성공 outcome | W02에서는 finalizing에 남는다. task·근거·artifact의 실제 검증과 보고서 게시 없이는 completed로 승격하지 않음 |

주요 잠금 순서는 project→run→attempt→task다. 권한 변경과 mutation은 project 잠금으로 직렬화한다. 읽기와 worker 갱신은 공유 project 잠금을 사용하고 다른 run 간 불필요한 직렬화를 줄인다. 쿼리/잠금 제한시간으로 대기를 제한한다. pool·state를 프로세스 전역 map에 보관하지 않는다.

run의 entity version과 event sequence는 별도다. 최초 queued 상태는 version 1이며 첫 event sequence 1이다. 상태 변경은 version과 sequence를 올린다. 근거 등록과 같은 상태 비변경 event는 sequence만 올릴 수 있다. 모든 event와 관련 상태는 같은 transaction이다. event 삽입 실패는 상태·claim·enqueue도 rollback한다.

## 4. 입력과 근거의 보존

설정은 immutable revision으로 저장한다. JSON 객체의 키 순서와 공백을 정규화하며 숫자를 float로 반올림하지 않는다. 프로젝트 config는 JSONB 정규화 이후 digest를 산출해 조회 결과와 일치시킨다. 멱등 요청 body는 별도 canonical JSON digest이며 숫자 표기 `1`과 `1.0`을 서로 다른 요청으로 취급한다. 중복 키·잘못된 UTF-8·100단계 초과 중첩은 거부한다.

run은 생성 당시 project_revision에 묶이고 snapshot은 null에서 한 번만 고정한다. 같은 digest 재전송은 허용하지만 다른 입력으로 교체/해제하지 않는다. Git snapshot 식별자는 SHA-1/SHA-256, 파일 blob SHA-256은 64자리 소문자 hex다.

tasks는 최신 논리 상태, task_executions는 attempt별 상태 이력이다. 재시도는 logical task를 재대기시키되 이전 execution과 근거를 남긴다. 성공 캐시 재사용은 아직 없다. 확인된 실패·취소 cleanup에서는 미종료 task/execution도 종료 처리한다. 불확실 cleanup에서는 실행 중이라는 가능성을 남긴다.

evidence는 같은 project/run/attempt/task, 해당 run의 고정 snapshot 또는 같은 run의 finalized artifact만 참조한다. 실행 이력이 없는 task의 근거를 거부한다. ID는 저장/수집 계층이 발급한다. DB 외래키는 삭제를 제한하고 cascade로 근거를 제거하지 않는다. 실제 파일 존재·행 범위·hash·artifact bytes 검증은 W04/W08/W12 수집 계층의 책임이며 metadata 등록 성공을 실제 분석·재현 증거로 표현하지 않는다.

## 5. 실패 분류

공개 저장 오류는 invalid / forbidden / not_found / conflict / stale_attempt / unavailable / migration으로 제한한다. SQLSTATE class 22의 값 오류와 제약 오류를 unavailable로 숨기지 않는다. 사용자 입력·비밀번호가 포함될 수 있는 pgx error detail은 버린다.

DB 불변조건 위반 코드는 CY001(불변 행/열), CY002(고정 참조), CY003(카운터 감소), CY004(금지 상태 전이)이며 저장 서비스에서 conflict로 분류한다. 이는 향후 HTTP 409 등의 근거이며 현재 HTTP endpoint 구현은 아니다.

## 6. 수용 시험과 남은 범위

로컬 Ubuntu/WSL2, Go 1.27.4, Native PostgreSQL 17.10에서 전체 5개 Go 패키지 race 검사, migration 20개·저장 서비스 20개 테스트(하위 사례 별도), 명시적 CLI migration 2회와 doctor JSON 검증을 통과했다. Go vet·Windows amd64 교차 빌드·문서 validator(6 Schema, 5 예시)도 통과했다. CI PostgreSQL 16/Windows 시험 결과는 PR checks로 별도 확인한다. Windows 11/RHEL 제품 실행·실제 source acquire·OS cleanup·artifact bytes 검증은 미실행이다.

실행 명령:

```bash
go test ./...
go vet ./...
python3 scripts/test_postgres.py
python3 scripts/validate_bundle.py
```

Native PostgreSQL의 initdb/postgres/psql을 PATH에 두고 test_postgres.py를 실행하면 임시 DB에서 migration·저장 서비스 시험이 실제 실행된다. DB 설정 없이 go test만 실행하면 통합 시험은 skip되므로 W02 완료 증거로 삼지 않는다. CI Linux job에서는 실제 DB 시험이 필수다.

요구사항 연결은 REQ-002/003/013/022/035, NFR-002/003/007의 저장 기반이다. TC-40~45를 별도로 정의하고 기존 TC-02/03/04/12/13/21/22/23/31/32의 전체 제품 경로와 구분한다. W03 인증 HTTP, W04 실제 소스 고정, W05 OS 종료, W06/W07 자원·agent·autoscale, W09 실제 보고서 게시가 다음 작업이다.

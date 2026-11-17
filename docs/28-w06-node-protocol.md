# 28. W06 노드 프로토콜: controller·저장 계층

상태: controller 쪽(migration 0003, 저장 함수, HTTP 노드 경로, 노드 관리 CLI, artifact 저장소) 구현과 실제 PostgreSQL 시험 완료 · 2026-11-18. 노드 agent·worker 프로세스 감독·journal은 별도 작업 범위이며 이 문서는 그 결과를 주장하지 않는다. 분석 handler가 없는 제품 빌드는 `analysis:*` 능력을 광고하지 않으므로 run은 `no_capable_node`로 대기한다(W08 전). 성공 attempt는 `finalizing`에 머물며 `completed`/`partial` 전이는 W09 보고서 게시가 맡는다.

## 1. 범위와 요구사항

| 항목 | 연결 |
|---|---|
| 일회용·만료·철회 가능 등록 토큰, 노드 전용 credential, 사용자 세션과 분리 | REQ-019, NFR-006, [09](09-workers-and-scheduling.md) |
| 호환성·권한·예산 확인과 예약·attempt·epoch·이벤트의 단일 트랜잭션 claim | REQ-018/019, TC-21, NFR-002/003 |
| DB 시각 lease, node/worker/epoch fencing, 만료 후 갱신·완료 409 | REQ-022/035, TC-22 프로토콜 범위 |
| 정리 불확실 시 예약 유지와 `recovery_required`, 실제 정리 확인 후 해제 | REQ-035, TC-18 프로토콜 범위 |
| 응답 유실 재전송의 멱등(claim·event·artifact·complete) | NFR-002/003, TC-21 |
| 구조화 데이터에서 계산한 대기 사유 | REQ-013 원칙(상태의 원천은 구조화 데이터) |

코드: `migrations/sql/0003_nodes_and_leases.sql`, `internal/storage/postgres/{nodes,claim,nodeattempt,attempts,evidence}.go`, `internal/httpapi/node.go`, `internal/artifactstore`, `internal/nodeproto`(wire 타입, 표준 라이브러리와 `internal/domain`만 import), `cmd/codeye/node_admin.go`.

## 2. 운영자 CLI

```text
codeye node token create --pool NAME [--source-root ID[,ID...]] [--output .codeye/enroll-token] [--ttl 10m]
codeye node token revoke --id TOKEN_ID
codeye node list
codeye node revoke --id NODE_ID
codeye scheduler set --global-max-running N
codeye serve [--artifact-dir .codeye/artifacts] ...
```

- 등록 토큰은 32바이트 난수 base64url(43자)이며 0600 파일에 같은 디렉터리 rename으로 쓴다. 기존 경로가 symlink 등 일반 파일이 아니면 거부한다. 표준 출력에는 token ID·pool·source root ID·만료만 쓰고 토큰은 쓰지 않는다. DB에는 SHA-256만 저장한다.
- pool은 이름으로 처음 사용할 때 만든다. pool 정책(min/max worker, revision)은 W07이다.
- TTL은 1분~24시간, 기본 10분이다. 사용·만료·철회된 토큰은 등록에 쓸 수 없다. 사용된 토큰의 철회는 409이며 그때는 노드를 철회한다.
- `node revoke`는 즉시 credential을 무효화한다. 그 노드의 lease는 갱신되지 않아 만료 후 `recovery_required`가 된다.
- `scheduler set`은 전역 동시 attempt 상한(초기값 2)을 바꾼다.
- 이 명령의 진입점은 `runNodeAdmin(ctx, args, out, stderr) (code, ok)`이며 `main.go`의 dispatch 연결은 통합 단계에서 한다.

## 3. HTTP 계약 요약

경로·필드는 [OpenAPI](../api/openapi.yaml)의 `x-codeye-status: implemented-w06` operation이 기준이고 route 대조 시험이 자동으로 검사한다. [API 개요](11-api-and-events.md#4-노드-프로토콜)

- **인증.** `Authorization: Bearer <node_secret>`. 형식 오류·미등록·철회는 401 `NODE_UNAUTHENTICATED`. 노드 경로는 cookie를 읽지 않고 CSRF를 쓰지 않으며 `Origin` header가 있으면 403 `ORIGIN_REJECTED`(브라우저 요청 차단). Host allowlist(loopback)는 W03과 같다. 사용자 경로는 Bearer를 받지 않는다.
- **소유권 오류.** 경로의 node_id가 인증 노드와 다르거나 다른 노드의 attempt이면 404. 같은 노드라도 다른 worker·이전 epoch·만료·종료 lease는 409 `STALE_ATTEMPT`.
- **claim.** 마지막 heartbeat에서 `idle`로 보고되었고 활성(`leased`/`recovery_required`) attempt가 없는 워커만 가능하다(그 외 409 `CONFLICT`). claim 성공 시 서버가 워커를 `busy`로 표시하므로 다음 claim 전에 heartbeat로 idle을 보고해야 한다. 같은 `Idempotency-Key`+같은 worker는 저장된 200/204를 그대로, 다른 worker는 409 `IDEMPOTENCY_CONFLICT`. 저장 응답은 하루 뒤 정리한다.
- **job.** local source는 `source_root_id`·`relative_path`만 보낸다. 서버는 노드 경로를 보내지도 저장하지도 않는다. Git은 URL·branch·host. 고정 입력 재시도는 `job.snapshot`(manifest digest 포함)을 함께 보낸다.
- **이벤트.** `snapshot_fixed`는 같은 attempt가 먼저 PUT한 `source_manifest` artifact(sha256 = manifest_digest)를 요구한다. kind/consistency 조합은 `local/stable_copy_best_effort`(source_sha null) 또는 `git/git_commit`(source_sha 40|64 hex)이다. `task_created`는 run 안의 key가 같으면 기존 task와 현재 version을 돌려준다. `run_advance`의 expected_version은 선택이며 모든 응답에 `run_version`이 있다.
- **artifact.** 최대 64 MiB, `Content-Length` 필수(없으면 411). bytes는 `<artifact-dir>/sha256/<aa>/<sha256>`에 저장한다.
- **완료.** `succeeded`/`partial`은 이 attempt의 finalized `result_manifest` artifact가 필요하다. 동일 본문 재전송은 같은 202다.

## 4. 저장 모델(migration 0003)

0001/0002 checksum을 바꾸지 않는 forward migration이다. 추가 테이블: `worker_pools`, `scheduler_limits`(단일 행, `global_max_running=2`), `node_enrollment_tokens`(+`node_enrollment_token_roots`), `nodes`(`command_seq`, 관측 capacity, capabilities), `node_source_roots`, `workers`, `node_commands`(`UNIQUE(node_id, sequence)`, attempt당 cancel 1개), `capacity_reservations`(attempt당 1개), `node_claims`(claim 멱등), `attempt_client_events`, `snapshot_locations`. 열 추가: `runs.request`, `tasks.key/reason`, `task_executions.reason`, `snapshots.manifest_digest/source_sha`, `artifacts.attempt_id/client_artifact_id/role/media_type/storage_key/created_at`, `attempts.result_manifest_id/completion_reason`. 식별·hash·한 번 고정된 값은 트리거로 불변이다(CY001/CY002).

**기존 행 처리.**

- `runs.request`: W03은 canonical 요청의 SHA-256만 저장했다. W03이 받던 scope=full 본문 세 가지(profile 3종)는 canonical bytes가 하나로 정해지므로 digest가 정확히 같은 행만 그 본문으로 복원한다. 그 외(scope=paths 등)는 NULL로 남고 claim되지 않으며 `waiting_reason=request_unavailable`로 보인다. 추측해 채우지 않는다. 복원 literal이 Go canonical 인코딩과 같은지 시험이 검사한다.
- `snapshots`: 기존 Git 행의 digest는 커밋 SHA이고 manifest가 없다. 불변 행이므로 고치지 않고 `manifest_digest`/`source_sha` NULL인 "W06 이전 snapshot"으로 둔다. 위치 기록이 없으므로 고정 입력 재시도가 배정되지 않는다(`snapshot_unavailable`). 새 행은 `digest = manifest_digest`이고 Git은 `source_sha`를 따로 가진다.
- `attempts.node_id`: W02 시험 경로의 미등록 ID가 있을 수 있어 `nodes` 외래키를 두지 않는다. W02 `BeginAttempt`/`FixSnapshot`은 시험·호환용으로 남으며 HTTP에 노출하지 않는다.

**구버전 거부와 복원.** 0003 적용 DB에 0001~0002만 아는 바이너리가 `migrate`하면 `ErrUnknownVersion`으로 아무것도 바꾸지 않는다(시험함). `serve`의 schema 검사는 같은 이력 검증 함수를 쓰며 `newer` 상태 거부는 W03 TC-51 시험(`TestSchemaStatusReadOnly`)이 다룬다. 0001~0002 바이너리 자체를 빌드해 `serve`를 띄우는 시험은 하지 않았다. 되돌리려면 0003 적용 전 백업에서 복원한다(down migration 없음). 백업·복원 절차는 실행하지 않았다.

## 5. claim 트랜잭션과 회계 모델

잠금 순서: `scheduler_limits` → 노드 행 → (후보별) 프로젝트 → run → 워커 예약 기록. 기존 쓰기 경로의 project → run → attempt 순서와 같은 방향이다. 취소 명령은 `CancelRun`(project → run)에서 노드 행을 잠그지 않고, 노드 heartbeat가 노드 행 잠금 아래에서 만든다. 그래서 sequence 순서가 commit 순서와 같다.

1. 노드 철회·heartbeat 신선도(20초)·전역 활성 예약 수 < 상한·노드 여유를 확인한다.
2. queued run을 생성 순으로 keyset page(200개)씩 끝까지 훑어 호환 run을 찾는다. 앞쪽의 비호환 run이 뒤 run을 굶기지 않는다. 호환 조건은 `analysis:<profile>` 능력, local이면 `source:local:<root>` 능력과 등록 토큰의 root 허가, Git이면 `source:git:<host>`, 고정 snapshot이면 그 노드의 `snapshot_locations`이다.
3. 후보의 프로젝트·run을 잠근 뒤 queued 여부와 미해제 attempt 부재를 다시 확인한다. 요청자가 비활성·권한 철회 상태이면 `actor_revoked`, local 프로젝트의 source root grant를 잃었으면 `source_grant_revoked`로 run을 failed 처리한다. 대기 task는 `skipped_policy`가 된다.
4. attempt(epoch+1)·`capacity_reservations`·run `preparing`·워커 `busy`·`attempt.leased` 이벤트·멱등 응답을 같은 트랜잭션에 기록한다. 어느 단계든 실패하면 전부 rollback된다.

예약 수요는 W06 고정값(CPU 1000m, 메모리 1 GiB, disk 1 GiB)이며 OS 제한이 아니라 admission 회계다. CPU는 노드가 보고한 core 수를 정책 예산으로 보고 활성 예약 전체를 뺀다. 메모리·disk는 OS가 실행 중 사용량을 이미 뺀 관측 여유다. 따라서 마지막 heartbeat 이후 만들어져 아직 관측에 반영되지 않은 예약만 뺀다(docs/09의 이중 계산 금지). 관측 직후의 아직 메모리를 쓰지 않은 워커는 다음 관측에서 과소 반영될 수 있다. 이 근사는 W07 정책 예산에서 보완한다.

## 6. lease·만료·정리

- lease는 30초이고 갱신·만료 판단은 DB `clock_timestamp()`를 쓴다. controller는 5초마다 만료 lease를 `recovery_required`(cleanup `uncertain`)로 옮긴다. 이때 예약은 유지한다.
- `recovery_required` run은 자동으로 다시 queued가 되거나 다른 노드에 배정되지 않는다. 소유 노드가 `confirmed` 정리 결과(heartbeat `cleanup_reports` 또는 `/cleanup`)를 보고하면 run은 failed가 되고 예약을 해제한다. `uncertain` 보고는 정보로만 받아 상태를 바꾸지 않는다(`uncertain_not_applied`).
- 완료 시 `cleanup_state=confirmed`면 예약을 해제하고, `uncertain`이면 `recovery_required`로 두고 예약을 유지한다.
- 철회된 노드의 `recovery_required` attempt는 정리 결과를 보낼 수 없으므로 예약이 전역 상한을 계속 차지한다. W06에는 운영자 강제 해제 명령이 없다. 실제 프로세스 부재를 운영자가 확인한 뒤 해제하는 절차는 W07/W29 범위로 남긴다.

## 7. 대기 사유와 프로젝트 공백

`GET /runs/{id}`의 queued run `progress.waiting_reason`은 저장 데이터에서 매번 계산한다. 검사 순서: `request_unavailable` → `execution_unavailable`(철회되지 않은 노드 없음) → `snapshot_unavailable` → `node_stale`(신선한 노드 없음) → `no_capable_node` → `capacity_full`(전역 상한 또는 호환 노드 여유 부족) → null(호환 노드에 여유가 있어 다음 idle 워커가 가져갈 수 있음). 프로젝트 `capability_gaps`의 `execution_unavailable`은 등록 노드가 하나도 없을 때만 나온다. `source_access_unverified`와 `report_unavailable`은 계속 표시한다. 특정 run의 배치 가능성은 waiting_reason이 따로 말한다.

## 8. 시험

PostgreSQL 17.10(임시 Native 인스턴스, 시험별 격리 스키마)에서 실행했다. skip은 통과가 아니다. 9절의 `-v` 실행에서 아래 패키지의 skip은 0건이었다.

| 시험 | 핵심 단언 | 연결 |
|---|---|---|
| `TestEnrollmentTokenSingleUseExpiryRevocation` | 같은 토큰 8개 동시 등록에서 1건만 성공. 재사용·만료·철회 토큰 거부, 미지원 protocol은 토큰을 소비하지 않음, 원문 미저장 | REQ-019, NFR-006 |
| `TestNodeAuthenticationAndRevocation` | 철회 즉시 인증·heartbeat·claim 불가 | NFR-006 |
| `TestHeartbeatWorkersCommandsAndValidation` | 다른 노드의 워커 ID 409, 미보고 워커 stopped, cursor·형식 검증, 취소 요청이 cancel_attempt 1건으로 ack 전까지 재전달 | 11 §4 |
| `TestTC21ConcurrentClaimSingleOwner` | 두 노드·네 워커 동시 claim에서 attempt·epoch·예약·leased 이벤트가 각 1개. 같은 key 재전송은 새 예약 없음, 다른 worker는 IDEMPOTENCY_CONFLICT, busy/starting/미등록 워커 거부 | TC-21 |
| `TestTC21ClaimFailureLeavesNoPartialState` | `attempt.leased` 이벤트 삽입 실패를 trigger로 유도해도 attempt·예약·멱등 기록·epoch·워커 상태가 남지 않음 | TC-21 |
| `TestClaimRefusalsAndWaitingReasons` | 허가 없는 root, analysis 능력 없음(제품 빌드), source 능력 없음, 오래된 heartbeat, CPU·메모리·disk 부족, 전역 상한에서 204와 해당 대기 사유. W06 이전 본문은 request_unavailable | REQ-019, NFR-006 |
| `TestCapacityAccountingWithoutDoubleCounting` | 관측 여유에서 heartbeat 이후 예약만 차감, 다음 관측 뒤 재차감 없음, CPU는 전체 예약 차감 | 09 §자원 |
| `TestClaimPagesPastIncompatibleQueueHead` | 비호환 run 205개 뒤의 호환 run 배정 | 기아 방지 |
| `TestClaimRechecksActorPermissionAndSourceGrant` | 권한 철회·grant 상실 run은 attempt 없이 failed(`actor_revoked`/`source_grant_revoked`) | REQ-002 |
| `TestNodeAttemptHappyPathToFinalizing` | manifest artifact 검증, 이벤트 멱등·본문 충돌, task key 재사용, 오래된 run version 409, result manifest 없는 성공 거부, 성공은 finalizing, 예약 해제 | TC-45, 23 §W02 게이트 |
| `TestLeaseFencingExpiryAndCleanup` | 다른 worker·epoch 409, 다른 노드 404, 만료 후 갱신·이벤트·완료 409, sweep 후 예약 유지·재배정 없음, 정리 확인 후 failed·예약 해제·재전송 멱등 | TC-22, TC-18 프로토콜 |
| `TestUncertainCompletionKeepsReservation`, `TestNodeCommandsCannotBeDeleted` | uncertain 완료는 recovery_required와 예약 유지. 예약·명령 행 삭제는 CY001로 거부(조용히 0행 처리하지 않음) | REQ-035 |
| `TestGitSourceClaimEligibility` | Git run은 `source:git:<host>` 능력 노드에만 배정, job에 URL·branch·host만 포함 | REQ-018 |
| `TestPinnedRetryRequiresSnapshotLocation` | 재시도는 snapshot 보유 노드에만 배정, 보유 노드 철회 시 snapshot_unavailable, 다른 manifest는 409, 재계획 시 같은 key task 재사용(version 증가)과 상태 전이 | REQ-003 |
| `TestArtifactFinalizeIdempotencyAndFencing` | 같은 client ID 재전송, 내용 변경 409, 잘못된 storage key, 만료 후 새 업로드 거부·기존 재전송 허용 | NFR-007 |
| `TestW06MigrationFromPopulatedW03Schema` 외 2건 | 데이터가 있는 0002 DB에서 0003 적용·재적용, 본문 복원과 미복원, 기존 Git snapshot 보존, 구버전 거부, 중간 실패 전체 rollback | NFR-003 |
| `TestNode*`(HTTP 3건), `TestServeSweepsExpiredLeases` | Origin·Bearer·세션 분리, 경로 노드 불일치 404, 철회, 로그에 비밀값 없음, 실제 HTTP로 등록→claim→artifact→이벤트→finalizing, 취소 명령·uncertain·정리 확인, `Serve`의 sweeper가 만료 lease를 recovery_required로 이동 | NFR-006, TC-22 |
| `TestRequestBackfillLiteralsAreCanonical`, `nodeproto` 2건 | 복원 literal이 Go canonical 인코딩과 같음, 능력 형식·wire 필드 이름 고정 | NFR-003 |
| `TestPut*`(artifactstore 2건) | hash/크기 불일치 시 잔여 파일 없음, 같은 크기의 변조 파일을 신뢰하지 않고 교체 | NFR-007 |
| `TestNodeAdminDispatchAndValidation`, `TestNodeTokenCreateWritesPrivateFile` | 토큰 미출력·0600·symlink 거부, 미지원 인자 값 미노출 | NFR-006 |

전체 실행 결과는 9절에 기록한다.

## 9. 실행 결과

환경: Ubuntu 26.04.1 LTS / WSL2 x86_64(Linux 6.18), Go 1.27.4, PostgreSQL 17.10(임시 Native), Node 24.18.0, Python 3.14.4.

| 실행 | 결과 |
|---|---|
| `python3 scripts/test_postgres.py --web` | 임시 DB에서 `go test -race ./...` 시험이 있는 10개 패키지 통과, CLI migration 2회 재적용(0003 포함), doctor DB 진단·JSON schema, 실제 Chromium 세션·등록·요청·취소 1건 통과, `PASS:` 출력과 종료 코드 0 |
| `go test -race -count=1 -v` (migrations, storage/postgres, httpapi, cmd/codeye, artifactstore, nodeproto) | 최상위 시험 94개 통과(W06 신규 30개), SKIP·FAIL 0건 |
| `go test -race -count=15 -run TC21`, W06 저장·HTTP 시험 `-count=5` | 반복 통과(경합 승자 순서와 무관) |
| `go vet ./...`, `GOOS=windows go build ./...`·`go vet`, `gofmt -l` | 통과, 포맷 차이 0건. Windows는 컴파일·정적 검사만 했다 |
| `go list -deps ./internal/nodeproto` | storage·migrations·pgx 의존 없음 |
| `python3 scripts/validate_bundle.py` | 로컬 링크·Schema·예시·OpenAPI 참조 검사 통과 |

웹 브라우저 smoke는 이 작업 트리에 웹 의존성이 없어 기존 체크아웃의 `web/node_modules`를 연결하고 `npm --prefix web run build`로 정적 파일을 만든 뒤 실행했다. 브라우저 시험은 W03 경로이며 노드 경로를 브라우저로 검사하지 않는다.

## 10. 남은 제한

- 노드 agent·worker 프로세스·journal과 두 agent 프로세스 간 실제 경쟁·OS 종료 관측은 [워커 실행 기록](28-w06-agent-runtime.md)에서 확인할 수 있다. 이 문서의 시험은 controller와 저장 계층이다.
- 철회 노드의 recovery_required 예약 강제 해제, pool 정책, 워커 자동 증감, 노드 간 snapshot 전송·GC는 W07 이후다.
- artifact 파일은 DB 기록 전에 저장되므로 기록 실패 시 참조 없는 파일이 남을 수 있다(GC 미구현). Windows에서는 디렉터리 fsync를 하지 않는다.
- 예약 수요는 고정값이며 OS 제한이 아니다. 메모리·disk 관측은 heartbeat 간격만큼 늦다.
- Windows·RHEL 8.10에서 노드 경로를 실행하지 않았다.
- 0003 적용 전 백업에서의 복원과 구버전 바이너리 `serve` 실기는 실행하지 않았다.
- `serve` 기본 artifact 디렉터리는 실행 위치 기준 `.codeye/artifacts`다.

-- CODEYE W06: 노드 등록·인증, 워커, 원자적 claim 예약, 노드 명령, 멱등 이벤트, artifact 메타데이터,
-- snapshot 위치와 manifest digest 분리.
--
-- 0001/0002와 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 트리거는 NEW/OLD/TG_*만 사용,
-- 외래키는 ON DELETE RESTRICT, token·secret 원문은 저장하지 않고 SHA-256 hex만 저장한다.
--
-- 기존 행 처리:
--   * runs.request: W03은 요청 본문을 저장하지 않고 canonical JSON의 SHA-256(request_digest)만 저장했다.
--     W03이 받던 scope=full 본문 세 가지(profile 3종 × source_policy 1종)는 canonical bytes가 하나로
--     정해지므로 digest가 정확히 일치하는 행만 그 본문으로 복원한다. 일치하지 않는 행(scope=paths 등)은
--     NULL로 남고 claim 대상이 아니다(waiting_reason request_unavailable).
--   * snapshots: 기존 Git 행의 digest는 커밋 SHA이고 manifest가 없다. 불변 행이므로 갱신하지 않는다.
--     manifest_digest/source_sha가 NULL인 행은 "W06 이전 snapshot"이며 snapshot_locations가 없으므로
--     고정 입력 재시도가 배정되지 않는다(snapshot_unavailable). 새 행은 digest = manifest_digest다.
--   * attempts.node_id/worker_id: W02 시험 경로의 미등록 식별자가 있을 수 있으므로 nodes 외래키를 두지 않는다.

-- ---------------------------------------------------------------------------
-- runs.request (claim이 job을 만드는 원천)
-- ---------------------------------------------------------------------------

ALTER TABLE runs
    ADD COLUMN request jsonb NULL CHECK (request IS NULL OR jsonb_typeof(request) = 'object');

UPDATE runs SET request = c.body::jsonb
FROM (VALUES
    ('{"profile":"deep-review","scope":{"kind":"full"},"source_policy":"latest_at_acquire"}'),
    ('{"profile":"with-tests","scope":{"kind":"full"},"source_policy":"latest_at_acquire"}'),
    ('{"profile":"focused","scope":{"kind":"full"},"source_policy":"latest_at_acquire"}')
) AS c(body)
WHERE runs.request IS NULL
  AND runs.request_digest = encode(sha256(convert_to(c.body, 'UTF8')), 'hex');

CREATE FUNCTION codeye_runs_request_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.request IS NOT NULL AND NEW.request IS DISTINCT FROM OLD.request THEN
        RAISE EXCEPTION 'runs.request is fixed once stored' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER runs_request_guard
    BEFORE UPDATE ON runs
    FOR EACH ROW EXECUTE FUNCTION codeye_runs_request_guard();

CREATE INDEX runs_queued_created_idx ON runs (created_at, id) WHERE status = 'queued';

-- ---------------------------------------------------------------------------
-- tasks.key / reason
-- ---------------------------------------------------------------------------

ALTER TABLE tasks
    ADD COLUMN key text NULL CHECK (key IS NULL OR key ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    ADD COLUMN reason text NULL CHECK (reason IS NULL OR reason ~ '^[a-z][a-z0-9_]{0,63}$');

CREATE UNIQUE INDEX tasks_run_key_idx ON tasks (run_id, key) WHERE key IS NOT NULL;

ALTER TABLE task_executions
    ADD COLUMN reason text NULL CHECK (reason IS NULL OR reason ~ '^[a-z][a-z0-9_]{0,63}$');

CREATE FUNCTION codeye_tasks_key_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.key IS NOT NULL AND NEW.key IS DISTINCT FROM OLD.key THEN
        RAISE EXCEPTION 'tasks.key is immutable once set' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tasks_key_guard
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION codeye_tasks_key_guard();

-- ---------------------------------------------------------------------------
-- snapshots: manifest digest와 Git commit 분리
-- ---------------------------------------------------------------------------

ALTER TABLE snapshots
    ADD COLUMN manifest_digest text NULL CHECK (manifest_digest IS NULL OR manifest_digest ~ '^[0-9a-f]{64}$'),
    ADD COLUMN source_sha text NULL CHECK (source_sha IS NULL OR source_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    ADD CONSTRAINT snapshots_manifest_is_digest CHECK (manifest_digest IS NULL OR manifest_digest = digest),
    ADD CONSTRAINT snapshots_source_sha_git_only CHECK (source_sha IS NULL OR consistency = 'git_commit'),
    ADD CONSTRAINT snapshots_w06_git_has_sha CHECK (
        manifest_digest IS NULL OR consistency <> 'git_commit' OR source_sha IS NOT NULL);

-- ---------------------------------------------------------------------------
-- worker pools, scheduler limits
-- ---------------------------------------------------------------------------

CREATE TABLE worker_pools (
    id         uuid        PRIMARY KEY,
    name       text        NOT NULL UNIQUE CHECK (name ~ '^[a-z][a-z0-9_-]{0,62}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER worker_pools_immutable
    BEFORE UPDATE ON worker_pools
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- 단일 행. claim 트랜잭션의 첫 잠금이며 전역 동시 실행 상한이다(W07에서 pool 정책 revision 추가).
CREATE TABLE scheduler_limits (
    singleton          boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    global_max_running integer NOT NULL CHECK (global_max_running BETWEEN 0 AND 10000)
);

INSERT INTO scheduler_limits (singleton, global_max_running) VALUES (true, 2);

CREATE TRIGGER scheduler_limits_no_delete
    BEFORE DELETE ON scheduler_limits
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- enrollment tokens (운영자 CLI 발급, 일회용, 만료, 철회 가능)
-- ---------------------------------------------------------------------------

CREATE TABLE node_enrollment_tokens (
    id           uuid        PRIMARY KEY,
    token_sha256 text        NOT NULL UNIQUE CHECK (token_sha256 ~ '^[0-9a-f]{64}$'),
    pool_id      uuid        NOT NULL REFERENCES worker_pools (id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz NULL,
    revoked_at   timestamptz NULL,
    CHECK (expires_at > created_at),
    CHECK (used_at IS NULL OR used_at >= created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE FUNCTION codeye_enrollment_tokens_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.token_sha256 <> OLD.token_sha256 OR NEW.pool_id <> OLD.pool_id
       OR NEW.created_at <> OLD.created_at OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'enrollment token identity is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.used_at IS NOT NULL AND NEW.used_at IS DISTINCT FROM OLD.used_at THEN
        RAISE EXCEPTION 'enrollment token is already used' USING ERRCODE = 'CY002';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'enrollment token is already revoked' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER node_enrollment_tokens_guard
    BEFORE UPDATE ON node_enrollment_tokens
    FOR EACH ROW EXECUTE FUNCTION codeye_enrollment_tokens_guard();

CREATE TABLE node_enrollment_token_roots (
    token_id       uuid NOT NULL REFERENCES node_enrollment_tokens (id) ON DELETE RESTRICT,
    source_root_id uuid NOT NULL REFERENCES source_roots (id) ON DELETE RESTRICT,
    PRIMARY KEY (token_id, source_root_id)
);

CREATE TRIGGER node_enrollment_token_roots_immutable
    BEFORE UPDATE OR DELETE ON node_enrollment_token_roots
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- nodes
-- ---------------------------------------------------------------------------

CREATE TABLE nodes (
    id                     uuid        PRIMARY KEY,
    pool_id                uuid        NOT NULL REFERENCES worker_pools (id) ON DELETE RESTRICT,
    enrollment_token_id    uuid        NOT NULL UNIQUE REFERENCES node_enrollment_tokens (id) ON DELETE RESTRICT,
    name                   text        NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    secret_sha256          text        NOT NULL UNIQUE CHECK (secret_sha256 ~ '^[0-9a-f]{64}$'),
    os                     text        NOT NULL CHECK (os ~ '^[a-z0-9_]{1,32}$'),
    arch                   text        NOT NULL CHECK (arch ~ '^[a-z0-9_]{1,32}$'),
    protocol_version       text        NOT NULL CHECK (protocol_version ~ '^[0-9]+\.[0-9]+$'),
    enrolled_at            timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at             timestamptz NULL,
    last_heartbeat_at      timestamptz NULL,
    cpu_cores              numeric     NULL CHECK (cpu_cores IS NULL OR (cpu_cores >= 0 AND cpu_cores <= 4096)),
    memory_available_bytes bigint      NULL CHECK (memory_available_bytes IS NULL OR memory_available_bytes >= 0),
    disk_available_bytes   bigint      NULL CHECK (disk_available_bytes IS NULL OR disk_available_bytes >= 0),
    capabilities           text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(capabilities) <= 128),
    -- 노드별 명령 sequence. 노드 행 잠금 아래에서만 증가하므로 commit 순서 = sequence 순서다.
    command_seq            bigint      NOT NULL DEFAULT 0 CHECK (command_seq >= 0),
    CHECK (revoked_at IS NULL OR revoked_at >= enrolled_at)
);

CREATE INDEX nodes_pool_idx ON nodes (pool_id);

CREATE FUNCTION codeye_nodes_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.pool_id <> OLD.pool_id OR NEW.enrollment_token_id <> OLD.enrollment_token_id
       OR NEW.secret_sha256 <> OLD.secret_sha256 OR NEW.enrolled_at <> OLD.enrolled_at THEN
        RAISE EXCEPTION 'node identity and credential hash are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'revoked node cannot be restored' USING ERRCODE = 'CY002';
    END IF;
    IF NEW.command_seq < OLD.command_seq THEN
        RAISE EXCEPTION 'nodes.command_seq must not decrease' USING ERRCODE = 'CY003';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER nodes_guard
    BEFORE UPDATE ON nodes
    FOR EACH ROW EXECUTE FUNCTION codeye_nodes_guard();

-- 등록 토큰이 허가한 source root. 서버는 노드의 실제 경로를 저장하지 않는다.
CREATE TABLE node_source_roots (
    node_id        uuid NOT NULL REFERENCES nodes (id) ON DELETE RESTRICT,
    source_root_id uuid NOT NULL REFERENCES source_roots (id) ON DELETE RESTRICT,
    PRIMARY KEY (node_id, source_root_id)
);

CREATE INDEX node_source_roots_root_idx ON node_source_roots (source_root_id);

-- ---------------------------------------------------------------------------
-- workers (W06 정적 모드: agent가 ID를 만들고 첫 heartbeat에서 등록)
-- ---------------------------------------------------------------------------

CREATE TABLE workers (
    id            uuid        PRIMARY KEY,
    node_id       uuid        NOT NULL REFERENCES nodes (id) ON DELETE RESTRICT,
    state         text        NOT NULL CHECK (state IN ('starting', 'idle', 'busy', 'draining', 'stopped', 'failed')),
    -- 마지막 heartbeat에서 agent가 보고한 attempt. 소유권의 원천은 attempts 행이다.
    attempt_id    uuid        NULL,
    first_seen_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_seen_at  timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE INDEX workers_node_idx ON workers (node_id);

CREATE FUNCTION codeye_workers_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.node_id <> OLD.node_id OR NEW.first_seen_at <> OLD.first_seen_at THEN
        RAISE EXCEPTION 'worker identity is immutable' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER workers_guard
    BEFORE UPDATE ON workers
    FOR EACH ROW EXECUTE FUNCTION codeye_workers_guard();

-- ---------------------------------------------------------------------------
-- node commands
-- ---------------------------------------------------------------------------

CREATE TABLE node_commands (
    id         uuid        PRIMARY KEY,
    node_id    uuid        NOT NULL REFERENCES nodes (id) ON DELETE RESTRICT,
    sequence   bigint      NOT NULL CHECK (sequence >= 1),
    kind       text        NOT NULL CHECK (kind IN ('cancel_attempt', 'start_worker', 'drain_worker', 'stop_worker')),
    worker_id  uuid        NULL,
    attempt_id uuid        NULL REFERENCES attempts (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    acked_at   timestamptz NULL,
    UNIQUE (node_id, sequence),
    CHECK (kind <> 'cancel_attempt' OR attempt_id IS NOT NULL)
);

CREATE UNIQUE INDEX node_commands_one_cancel_per_attempt
    ON node_commands (attempt_id) WHERE kind = 'cancel_attempt';

CREATE FUNCTION codeye_node_commands_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% on % is not allowed', TG_OP, TG_TABLE_NAME USING ERRCODE = 'CY001';
    END IF;
    IF NEW.id <> OLD.id OR NEW.node_id <> OLD.node_id OR NEW.sequence <> OLD.sequence OR NEW.kind <> OLD.kind
       OR NEW.worker_id IS DISTINCT FROM OLD.worker_id OR NEW.attempt_id IS DISTINCT FROM OLD.attempt_id
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'node command is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.acked_at IS NOT NULL AND NEW.acked_at IS DISTINCT FROM OLD.acked_at THEN
        RAISE EXCEPTION 'node command is already acknowledged' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER node_commands_guard
    BEFORE UPDATE OR DELETE ON node_commands
    FOR EACH ROW EXECUTE FUNCTION codeye_node_commands_guard();

-- ---------------------------------------------------------------------------
-- capacity reservations (attempt당 하나, 정리 확인 시에만 해제)
-- 회계 모델: CPU는 노드가 보고한 core 수(정책 예산)에서 활성 예약 합을 뺀다. 메모리·disk는 OS가 이미
-- 실행 중 사용량을 차감해 보고한 관측 여유에서, 마지막 heartbeat 이후 생긴(아직 관측에 반영되지 않은)
-- 예약만 뺀다. 관측 여유에서 모든 예약을 다시 빼는 이중 계산을 하지 않는다.
-- ---------------------------------------------------------------------------

CREATE TABLE capacity_reservations (
    attempt_id   uuid        PRIMARY KEY REFERENCES attempts (id) ON DELETE RESTRICT,
    node_id      uuid        NOT NULL REFERENCES nodes (id) ON DELETE RESTRICT,
    worker_id    uuid        NOT NULL REFERENCES workers (id) ON DELETE RESTRICT,
    cpu_millis   bigint      NOT NULL CHECK (cpu_millis >= 0),
    memory_bytes bigint      NOT NULL CHECK (memory_bytes >= 0),
    disk_bytes   bigint      NOT NULL CHECK (disk_bytes >= 0),
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    released_at  timestamptz NULL,
    CHECK (released_at IS NULL OR released_at >= created_at)
);

CREATE INDEX capacity_reservations_active_node_idx ON capacity_reservations (node_id) WHERE released_at IS NULL;

CREATE FUNCTION codeye_reservations_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% on % is not allowed', TG_OP, TG_TABLE_NAME USING ERRCODE = 'CY001';
    END IF;
    IF NEW.attempt_id <> OLD.attempt_id OR NEW.node_id <> OLD.node_id OR NEW.worker_id <> OLD.worker_id
       OR NEW.cpu_millis <> OLD.cpu_millis OR NEW.memory_bytes <> OLD.memory_bytes OR NEW.disk_bytes <> OLD.disk_bytes
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'reservation is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.released_at IS NOT NULL AND NEW.released_at IS DISTINCT FROM OLD.released_at THEN
        RAISE EXCEPTION 'reservation is already released' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER capacity_reservations_guard
    BEFORE UPDATE OR DELETE ON capacity_reservations
    FOR EACH ROW EXECUTE FUNCTION codeye_reservations_guard();

-- ---------------------------------------------------------------------------
-- claim idempotency (응답 유실 뒤 재전송이 두 번째 예약을 만들지 않게 한다)
-- ---------------------------------------------------------------------------

CREATE TABLE node_claims (
    node_id         uuid        NOT NULL REFERENCES nodes (id) ON DELETE RESTRICT,
    idempotency_key text        NOT NULL CHECK (idempotency_key ~ '^[!-~]{1,200}$'),
    worker_id       uuid        NOT NULL,
    attempt_id      uuid        NULL REFERENCES attempts (id) ON DELETE RESTRICT,
    -- NULL은 204(배정 없음) 응답이다.
    response        jsonb       NULL CHECK (response IS NULL OR jsonb_typeof(response) = 'object'),
    created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (node_id, idempotency_key),
    CHECK ((attempt_id IS NULL) = (response IS NULL))
);

CREATE INDEX node_claims_created_idx ON node_claims (node_id, created_at);

CREATE TRIGGER node_claims_immutable
    BEFORE UPDATE ON node_claims
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- attempt client events (client_event_id 멱등)
-- ---------------------------------------------------------------------------

CREATE TABLE attempt_client_events (
    attempt_id      uuid        NOT NULL REFERENCES attempts (id) ON DELETE RESTRICT,
    client_event_id uuid        NOT NULL,
    request_digest  text        NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    response        jsonb       NOT NULL CHECK (jsonb_typeof(response) = 'object'),
    created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (attempt_id, client_event_id)
);

CREATE TRIGGER attempt_client_events_immutable
    BEFORE UPDATE OR DELETE ON attempt_client_events
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- snapshot locations (보존 사본이 있는 노드; 고정 입력 재시도는 이 노드에만 배정)
-- ---------------------------------------------------------------------------

CREATE TABLE snapshot_locations (
    snapshot_id uuid        NOT NULL REFERENCES snapshots (id) ON DELETE RESTRICT,
    node_id     uuid        NOT NULL REFERENCES nodes (id) ON DELETE RESTRICT,
    created_at  timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (snapshot_id, node_id)
);

CREATE TRIGGER snapshot_locations_immutable
    BEFORE UPDATE ON snapshot_locations
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- artifacts: attempt 업로드 메타데이터
-- ---------------------------------------------------------------------------

ALTER TABLE artifacts
    ADD COLUMN attempt_id uuid NULL,
    ADD COLUMN client_artifact_id uuid NULL,
    ADD COLUMN role text NULL CHECK (role IS NULL OR role IN (
        'source_manifest', 'result_manifest', 'stdout', 'stderr', 'observations', 'raw_output', 'other')),
    ADD COLUMN media_type text NULL CHECK (media_type IS NULL OR media_type ~ '^[a-z0-9][a-z0-9!#$&^_.+-]{0,63}/[a-z0-9][a-z0-9!#$&^_.+-]{0,126}$'),
    -- controller artifact 저장소 안의 상대 key(sha256/aa/<sha>). 절대 경로가 아니다.
    ADD COLUMN storage_key text NULL CHECK (storage_key IS NULL OR storage_key ~ '^sha256/[0-9a-f]{2}/[0-9a-f]{64}$'),
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    ADD CONSTRAINT artifacts_attempt_fk FOREIGN KEY (run_id, attempt_id) REFERENCES attempts (run_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT artifacts_attempt_client_unique UNIQUE (attempt_id, client_artifact_id),
    ADD CONSTRAINT artifacts_upload_complete CHECK (
        (attempt_id IS NULL AND client_artifact_id IS NULL AND role IS NULL AND media_type IS NULL AND storage_key IS NULL)
        OR (attempt_id IS NOT NULL AND client_artifact_id IS NOT NULL AND role IS NOT NULL AND media_type IS NOT NULL
            AND storage_key IS NOT NULL AND storage_key = 'sha256/' || substr(sha256, 1, 2) || '/' || sha256));

CREATE FUNCTION codeye_artifacts_upload_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.attempt_id IS DISTINCT FROM OLD.attempt_id OR NEW.client_artifact_id IS DISTINCT FROM OLD.client_artifact_id
       OR NEW.role IS DISTINCT FROM OLD.role OR NEW.media_type IS DISTINCT FROM OLD.media_type
       OR NEW.storage_key IS DISTINCT FROM OLD.storage_key OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'artifact upload metadata is immutable' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER artifacts_upload_guard
    BEFORE UPDATE ON artifacts
    FOR EACH ROW EXECUTE FUNCTION codeye_artifacts_upload_guard();

-- ---------------------------------------------------------------------------
-- attempts: 완료 결과 manifest와 사유
-- ---------------------------------------------------------------------------

ALTER TABLE attempts
    ADD COLUMN result_manifest_id uuid NULL REFERENCES artifacts (id) ON DELETE RESTRICT,
    ADD COLUMN completion_reason text NULL CHECK (completion_reason IS NULL OR completion_reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    ADD CONSTRAINT attempts_completion_extras CHECK (
        completion_digest IS NOT NULL OR (result_manifest_id IS NULL AND completion_reason IS NULL));

CREATE FUNCTION codeye_attempts_completion_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.completion_digest IS NOT NULL
       AND (NEW.result_manifest_id IS DISTINCT FROM OLD.result_manifest_id
            OR NEW.completion_reason IS DISTINCT FROM OLD.completion_reason) THEN
        RAISE EXCEPTION 'attempt completion is already recorded' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER attempts_completion_guard
    BEFORE UPDATE ON attempts
    FOR EACH ROW EXECUTE FUNCTION codeye_attempts_completion_guard();

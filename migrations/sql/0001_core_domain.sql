-- CODEYE core domain schema.
--
-- 이 파일은 migrations.Apply가 단일 트랜잭션 안에서 실행한다. BEGIN/COMMIT을 넣지 않는다.
-- 모든 이름은 스키마를 지정하지 않으며 호출자의 search_path 첫 스키마(current_schema)에 생성된다.
-- 트리거 함수는 테이블을 비한정 이름으로 참조하지 않는다(실행 시점 search_path에 따라 다른 스키마를
-- 가리킬 수 있음). 다른 테이블 조회가 필요하면 TG_TABLE_SCHEMA로 한정한다.
--
-- 오류 SQLSTATE (migrations 패키지의 SQLState* 상수와 일치):
--   CY001 immutable row/column 변경
--   CY002 한 번 고정된 참조(snapshot, completion) 교체·해제
--   CY003 단조 증가 counter 감소
--   CY004 종료 상태 또는 금지된 상태 역행
--
-- 외래키는 모두 ON DELETE RESTRICT이며 CASCADE를 사용하지 않는다. 근거·감사 데이터 삭제는
-- 후속 명시적 GC 작업의 책임이다. evidence의 실제 파일 존재·행 범위·hash 검증은 이 schema가
-- 보장하지 않는다(소스 취득과 보고서 builder가 확인한다). 여기서는 metadata 형식과 범위 참조만 강제한다.

-- ---------------------------------------------------------------------------
-- 공통 트리거 함수
-- ---------------------------------------------------------------------------

CREATE FUNCTION codeye_reject_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% on % is not allowed: rows are immutable', TG_OP, TG_TABLE_NAME
        USING ERRCODE = 'CY001';
END;
$$;

-- ---------------------------------------------------------------------------
-- users, projects, project_members, project_revisions
-- ---------------------------------------------------------------------------

CREATE TABLE users (
    id uuid PRIMARY KEY
);

CREATE TABLE projects (
    id               uuid        PRIMARY KEY,
    owner_id         uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    name             text        NOT NULL CHECK (char_length(btrim(name)) > 0),
    current_revision bigint      NOT NULL CHECK (current_revision >= 1),
    deleted_at       timestamptz NULL
);

CREATE INDEX projects_owner_id_idx ON projects (owner_id);

CREATE TABLE project_members (
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    role       text NOT NULL CHECK (role IN ('manager', 'runner', 'viewer')),
    PRIMARY KEY (project_id, user_id)
);

CREATE INDEX project_members_user_id_idx ON project_members (user_id);

CREATE TABLE project_revisions (
    project_id    uuid   NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    revision      bigint NOT NULL CHECK (revision >= 1),
    config        jsonb  NOT NULL CHECK (jsonb_typeof(config) = 'object'),
    config_digest text   NOT NULL CHECK (config_digest ~ '^[0-9a-f]{64}$'),
    PRIMARY KEY (project_id, revision)
);

-- project 생성과 첫 revision은 같은 트랜잭션에서 만들어야 한다(commit 시 검사).
ALTER TABLE projects
    ADD CONSTRAINT projects_current_revision_fk
    FOREIGN KEY (id, current_revision) REFERENCES project_revisions (project_id, revision)
    ON DELETE RESTRICT
    DEFERRABLE INITIALLY DEFERRED;

CREATE TRIGGER project_revisions_immutable
    BEFORE UPDATE OR DELETE ON project_revisions
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

CREATE TRIGGER project_revisions_no_truncate
    BEFORE TRUNCATE ON project_revisions
    FOR EACH STATEMENT EXECUTE FUNCTION codeye_reject_mutation();

CREATE FUNCTION codeye_projects_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id THEN
        RAISE EXCEPTION 'projects.id is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.current_revision < OLD.current_revision THEN
        RAISE EXCEPTION 'projects.current_revision must not decrease (% -> %)',
            OLD.current_revision, NEW.current_revision USING ERRCODE = 'CY003';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER projects_guard
    BEFORE UPDATE ON projects
    FOR EACH ROW EXECUTE FUNCTION codeye_projects_guard();

-- ---------------------------------------------------------------------------
-- snapshots
-- ---------------------------------------------------------------------------

CREATE TABLE snapshots (
    id          uuid PRIMARY KEY,
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    -- Git commit SHA-1(40) / SHA-256(64) 또는 manifest SHA-256(64), 소문자 hex.
    digest      text NOT NULL CHECK (digest ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    -- 어휘는 소스 취득 어댑터가 정한다(예: stable_copy_best_effort). 여기서는 토큰 형식만 검사한다.
    consistency text NOT NULL CHECK (consistency ~ '^[a-z][a-z0-9_]*$'),
    UNIQUE (project_id, id)
);

CREATE TRIGGER snapshots_immutable
    BEFORE UPDATE ON snapshots
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- runs
-- ---------------------------------------------------------------------------

CREATE TABLE runs (
    id               uuid        PRIMARY KEY,
    project_id       uuid        NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    actor_id         uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    idempotency_key  text        NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 255),
    request_digest   text        NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    project_revision bigint      NOT NULL,
    snapshot_id      uuid        NULL,
    status           text        NOT NULL CHECK (status IN (
                         'queued', 'preparing', 'running', 'finalizing',
                         'completed', 'partial', 'failed',
                         'cancel_requested', 'cancelling', 'cancelled',
                         'recovery_required')),
    version          bigint      NOT NULL DEFAULT 1 CHECK (version >= 1),
    event_sequence   bigint      NOT NULL DEFAULT 0 CHECK (event_sequence >= 0),
    lease_epoch      bigint      NOT NULL DEFAULT 0 CHECK (lease_epoch >= 0),
    created_at       timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (project_id, actor_id, idempotency_key),
    UNIQUE (project_id, id),
    FOREIGN KEY (project_id, project_revision)
        REFERENCES project_revisions (project_id, revision) ON DELETE RESTRICT,
    -- snapshot은 같은 project의 것이어야 한다. snapshot_id가 NULL이면 검사하지 않는다(MATCH SIMPLE).
    FOREIGN KEY (project_id, snapshot_id)
        REFERENCES snapshots (project_id, id) ON DELETE RESTRICT
);

CREATE INDEX runs_snapshot_id_idx ON runs (snapshot_id) WHERE snapshot_id IS NOT NULL;
CREATE INDEX runs_actor_id_idx ON runs (actor_id);
CREATE INDEX runs_project_status_idx ON runs (project_id, status);

CREATE FUNCTION codeye_runs_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    unreleased boolean;
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.project_id <> OLD.project_id
       OR NEW.actor_id <> OLD.actor_id
       OR NEW.idempotency_key <> OLD.idempotency_key
       OR NEW.request_digest <> OLD.request_digest
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'runs identity/request columns are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.project_revision <> OLD.project_revision THEN
        RAISE EXCEPTION 'runs.project_revision is fixed at creation' USING ERRCODE = 'CY002';
    END IF;
    IF OLD.snapshot_id IS NOT NULL AND NEW.snapshot_id IS DISTINCT FROM OLD.snapshot_id THEN
        RAISE EXCEPTION 'runs.snapshot_id is already fixed and cannot be replaced or cleared'
            USING ERRCODE = 'CY002';
    END IF;
    IF NEW.version < OLD.version
       OR NEW.event_sequence < OLD.event_sequence
       OR NEW.lease_epoch < OLD.lease_epoch THEN
        RAISE EXCEPTION 'runs version/event_sequence/lease_epoch must not decrease'
            USING ERRCODE = 'CY003';
    END IF;
    IF NEW.status <> OLD.status THEN
        IF OLD.status IN ('completed', 'partial') THEN
            RAISE EXCEPTION 'run status % is terminal (attempted %)', OLD.status, NEW.status
                USING ERRCODE = 'CY004';
        END IF;
        -- failed/cancelled는 재시도(queued)로만 벗어날 수 있다. 입력 snapshot이 고정되어 있고
        -- 모든 attempt가 정리 확인 후 released여야 한다. attempts는 트리거 테이블과 같은 스키마에서
        -- 찾는다(search_path에 의존하지 않음).
        IF OLD.status IN ('failed', 'cancelled') THEN
            IF NEW.status <> 'queued' THEN
                RAISE EXCEPTION 'run status % may only be retried to queued (attempted %)', OLD.status, NEW.status
                    USING ERRCODE = 'CY004';
            END IF;
            IF NEW.snapshot_id IS NULL THEN
                RAISE EXCEPTION 'run retry requires a fixed snapshot' USING ERRCODE = 'CY004';
            END IF;
            EXECUTE format(
                'SELECT EXISTS (SELECT 1 FROM %I.attempts WHERE run_id = $1 '
                'AND (status <> ''released'' OR cleanup_state <> ''confirmed''))',
                TG_TABLE_SCHEMA)
                INTO unreleased USING NEW.id;
            IF unreleased THEN
                RAISE EXCEPTION 'run retry requires every attempt released with confirmed cleanup'
                    USING ERRCODE = 'CY004';
            END IF;
        END IF;
        IF OLD.status = 'recovery_required' AND NEW.status = 'queued' THEN
            RAISE EXCEPTION 'recovery_required run must not return to queued' USING ERRCODE = 'CY004';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER runs_guard
    BEFORE UPDATE ON runs
    FOR EACH ROW EXECUTE FUNCTION codeye_runs_guard();

-- ---------------------------------------------------------------------------
-- attempts
-- ---------------------------------------------------------------------------
-- node_id/worker_id는 향후 인증된 controller가 발급하는 불투명 식별자다. 아직 등록 node 테이블이
-- 없으므로 외래키를 두지 않는다(매달린 node 테이블을 만들지 않는다).

CREATE TABLE attempts (
    id                uuid        PRIMARY KEY,
    run_id            uuid        NOT NULL REFERENCES runs (id) ON DELETE RESTRICT,
    node_id           uuid        NOT NULL,
    worker_id         uuid        NOT NULL,
    number            bigint      NOT NULL CHECK (number >= 1),
    epoch             bigint      NOT NULL CHECK (epoch >= 1),
    expires_at        timestamptz NOT NULL,
    status            text        NOT NULL CHECK (status IN ('leased', 'recovery_required', 'released')),
    cleanup_state     text        NOT NULL DEFAULT 'pending'
                                  CHECK (cleanup_state IN ('pending', 'uncertain', 'confirmed')),
    completion_digest text        NULL CHECK (completion_digest ~ '^[0-9a-f]{64}$'),
    completion_status text        NULL CHECK (completion_status ~ '^[a-z][a-z0-9_]*$'),
    UNIQUE (run_id, number),
    UNIQUE (run_id, id),
    UNIQUE (run_id, epoch),
    CHECK ((completion_digest IS NULL) = (completion_status IS NULL)),
    -- 소유권 해제는 하위 프로세스 정리 확인 뒤에만 가능하다. 불확실하면 recovery_required로 남는다.
    CHECK (status <> 'released' OR cleanup_state = 'confirmed')
);

-- run당 활성 소유 attempt(leased 또는 recovery_required)는 최대 하나.
CREATE UNIQUE INDEX attempts_one_active_per_run
    ON attempts (run_id) WHERE status IN ('leased', 'recovery_required');

CREATE INDEX attempts_leased_expires_at_idx ON attempts (expires_at) WHERE status = 'leased';

CREATE FUNCTION codeye_attempts_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.run_id <> OLD.run_id
       OR NEW.number <> OLD.number
       OR NEW.node_id <> OLD.node_id
       OR NEW.worker_id <> OLD.worker_id THEN
        RAISE EXCEPTION 'attempts identity columns are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.epoch < OLD.epoch THEN
        RAISE EXCEPTION 'attempts.epoch must not decrease' USING ERRCODE = 'CY003';
    END IF;
    IF OLD.completion_digest IS NOT NULL
       AND (NEW.completion_digest IS DISTINCT FROM OLD.completion_digest
            OR NEW.completion_status IS DISTINCT FROM OLD.completion_status) THEN
        RAISE EXCEPTION 'attempt completion is already recorded and cannot be replaced or cleared'
            USING ERRCODE = 'CY002';
    END IF;
    IF NEW.status <> OLD.status THEN
        IF OLD.status = 'released' THEN
            RAISE EXCEPTION 'released attempt is terminal (attempted %)', NEW.status
                USING ERRCODE = 'CY004';
        END IF;
        IF OLD.status = 'recovery_required' AND NEW.status = 'leased' THEN
            RAISE EXCEPTION 'recovery_required attempt must not return to leased' USING ERRCODE = 'CY004';
        END IF;
    END IF;
    IF OLD.cleanup_state = 'confirmed' AND NEW.cleanup_state <> 'confirmed' THEN
        RAISE EXCEPTION 'confirmed cleanup must not regress to %', NEW.cleanup_state
            USING ERRCODE = 'CY004';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER attempts_guard
    BEFORE UPDATE ON attempts
    FOR EACH ROW EXECUTE FUNCTION codeye_attempts_guard();

-- ---------------------------------------------------------------------------
-- events (append-only)
-- ---------------------------------------------------------------------------

CREATE TABLE events (
    run_id         uuid        NOT NULL REFERENCES runs (id) ON DELETE RESTRICT,
    sequence       bigint      NOT NULL CHECK (sequence >= 1),
    entity_version bigint      NOT NULL CHECK (entity_version >= 1),
    type           text        NOT NULL CHECK (type ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$'),
    payload        jsonb       NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (run_id, sequence)
);

CREATE TRIGGER events_immutable
    BEFORE UPDATE OR DELETE ON events
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

CREATE TRIGGER events_no_truncate
    BEFORE TRUNCATE ON events
    FOR EACH STATEMENT EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- tasks
-- ---------------------------------------------------------------------------

CREATE TABLE tasks (
    id                   uuid    NOT NULL,
    run_id               uuid    NOT NULL REFERENCES runs (id) ON DELETE RESTRICT,
    status               text    NOT NULL CHECK (status IN (
                             'queued', 'blocked', 'running',
                             'succeeded', 'failed', 'timed_out', 'cancelled',
                             'unsupported', 'skipped_policy', 'budget_exhausted')),
    required_for_verdict boolean NOT NULL,
    version              bigint  NOT NULL DEFAULT 1 CHECK (version >= 1),
    PRIMARY KEY (run_id, id)
);

CREATE FUNCTION codeye_tasks_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.run_id <> OLD.run_id
       OR NEW.required_for_verdict <> OLD.required_for_verdict THEN
        RAISE EXCEPTION 'tasks identity/required_for_verdict are immutable' USING ERRCODE = 'CY001';
    END IF;
    -- 논리 task는 run 재시도 시 queued로 되돌아갈 수 있다(새 version). 실행 이력은 task_executions에 남는다.
    IF NEW.version < OLD.version THEN
        RAISE EXCEPTION 'tasks.version must not decrease' USING ERRCODE = 'CY003';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tasks_guard
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION codeye_tasks_guard();

-- ---------------------------------------------------------------------------
-- task_executions (attempt별 task 실행 이력; 재시도 시 이전 행을 보존)
-- ---------------------------------------------------------------------------

CREATE TABLE task_executions (
    run_id     uuid   NOT NULL,
    attempt_id uuid   NOT NULL,
    task_id    uuid   NOT NULL,
    ordinal    bigint NOT NULL DEFAULT 1 CHECK (ordinal >= 1),
    status     text   NOT NULL CHECK (status IN (
                   'queued', 'blocked', 'running',
                   'succeeded', 'failed', 'timed_out', 'cancelled',
                   'unsupported', 'skipped_policy', 'budget_exhausted')),
    version    bigint NOT NULL DEFAULT 1 CHECK (version >= 1),
    PRIMARY KEY (attempt_id, task_id, ordinal),
    FOREIGN KEY (run_id, attempt_id) REFERENCES attempts (run_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (run_id, task_id) REFERENCES tasks (run_id, id) ON DELETE RESTRICT
);

CREATE INDEX task_executions_run_task_idx ON task_executions (run_id, task_id);
CREATE INDEX task_executions_run_attempt_idx ON task_executions (run_id, attempt_id);

CREATE FUNCTION codeye_task_executions_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.run_id <> OLD.run_id
       OR NEW.attempt_id <> OLD.attempt_id
       OR NEW.task_id <> OLD.task_id
       OR NEW.ordinal <> OLD.ordinal THEN
        RAISE EXCEPTION 'task_executions identity columns are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.version < OLD.version THEN
        RAISE EXCEPTION 'task_executions.version must not decrease' USING ERRCODE = 'CY003';
    END IF;
    -- 한 실행의 종료 결과는 바뀌지 않는다. 재시도는 새 실행 행(다른 attempt 또는 ordinal)이다.
    IF NEW.status <> OLD.status
       AND OLD.status IN ('succeeded', 'failed', 'timed_out', 'cancelled',
                          'unsupported', 'skipped_policy', 'budget_exhausted') THEN
        RAISE EXCEPTION 'task execution status % is terminal (attempted %)', OLD.status, NEW.status
            USING ERRCODE = 'CY004';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER task_executions_guard
    BEFORE UPDATE ON task_executions
    FOR EACH ROW EXECUTE FUNCTION codeye_task_executions_guard();

-- ---------------------------------------------------------------------------
-- artifacts (metadata only; bytes는 artifact store)
-- ---------------------------------------------------------------------------

CREATE TABLE artifacts (
    id         uuid    PRIMARY KEY,
    project_id uuid    NOT NULL,
    run_id     uuid    NOT NULL,
    -- 업로드 생성 시 선언한 예상 SHA-256. finalize가 실제 bytes와 대조한다(후속 작업).
    sha256     text    NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    size_bytes bigint  NOT NULL CHECK (size_bytes >= 0),
    finalized  boolean NOT NULL DEFAULT false,
    UNIQUE (project_id, run_id, id),
    FOREIGN KEY (project_id, run_id) REFERENCES runs (project_id, id) ON DELETE RESTRICT
);

CREATE INDEX artifacts_run_id_idx ON artifacts (run_id);

CREATE FUNCTION codeye_artifacts_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.project_id <> OLD.project_id
       OR NEW.run_id <> OLD.run_id
       OR NEW.sha256 <> OLD.sha256
       OR NEW.size_bytes <> OLD.size_bytes THEN
        RAISE EXCEPTION 'artifacts identity/hash/size are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.finalized AND NOT NEW.finalized THEN
        RAISE EXCEPTION 'finalized artifact cannot be un-finalized' USING ERRCODE = 'CY004';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER artifacts_guard
    BEFORE UPDATE ON artifacts
    FOR EACH ROW EXECUTE FUNCTION codeye_artifacts_guard();

-- ---------------------------------------------------------------------------
-- evidence (metadata only)
-- ---------------------------------------------------------------------------
-- 기준은 snapshot 또는 artifact 중 정확히 하나다. tool_output은 artifact 기반이어야 한다.
-- code/document는 snapshot 경로(path 필수) 또는 생성 파일 artifact 기반일 수 있다.
-- path/행 범위/blob hash가 실제 내용과 일치하는지는 이 schema가 검증하지 않는다(소스 취득과 보고서 builder가 확인한다).

CREATE TABLE evidence (
    id          uuid   PRIMARY KEY,
    project_id  uuid   NOT NULL,
    run_id      uuid   NOT NULL,
    attempt_id  uuid   NOT NULL,
    task_id     uuid   NOT NULL,
    snapshot_id uuid   NULL,
    artifact_id uuid   NULL,
    path        text   NULL,
    start_line  bigint NULL,
    end_line    bigint NULL,
    blob_sha256 text   NULL CHECK (blob_sha256 ~ '^[0-9a-f]{64}$'),
    kind        text   NOT NULL CHECK (kind IN ('code', 'document', 'tool_output')),
    FOREIGN KEY (project_id, run_id) REFERENCES runs (project_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (run_id, attempt_id) REFERENCES attempts (run_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (run_id, task_id) REFERENCES tasks (run_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (project_id, snapshot_id) REFERENCES snapshots (project_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (project_id, run_id, artifact_id)
        REFERENCES artifacts (project_id, run_id, id) ON DELETE RESTRICT,
    CONSTRAINT evidence_exactly_one_basis CHECK (num_nonnulls(snapshot_id, artifact_id) = 1),
    CONSTRAINT evidence_tool_output_is_artifact CHECK (kind <> 'tool_output' OR artifact_id IS NOT NULL),
    CONSTRAINT evidence_snapshot_requires_path CHECK (snapshot_id IS NULL OR path IS NOT NULL),
    -- snapshot 상대 경로: 비어 있지 않고, 절대/드라이브/백슬래시/'..' 구간 금지.
    CONSTRAINT evidence_path_relative CHECK (
        path IS NULL OR (
            char_length(path) BETWEEN 1 AND 4096
            AND path !~ '^/'
            AND path !~ '^[A-Za-z]:'
            AND position(E'\\' IN path) = 0
            AND path !~ '(^|/)\.\.(/|$)'
        )),
    -- 1-based inclusive 행 범위. 둘 다 없거나 둘 다 있다.
    CONSTRAINT evidence_line_range CHECK (
        (start_line IS NULL AND end_line IS NULL)
        OR (start_line IS NOT NULL AND end_line IS NOT NULL
            AND start_line >= 1 AND end_line >= start_line)),
    CONSTRAINT evidence_lines_require_path_or_artifact CHECK (
        start_line IS NULL OR path IS NOT NULL OR artifact_id IS NOT NULL)
);

CREATE INDEX evidence_run_attempt_idx ON evidence (run_id, attempt_id);
CREATE INDEX evidence_run_task_idx ON evidence (run_id, task_id);
CREATE INDEX evidence_project_run_idx ON evidence (project_id, run_id);
CREATE INDEX evidence_snapshot_idx ON evidence (project_id, snapshot_id) WHERE snapshot_id IS NOT NULL;
CREATE INDEX evidence_artifact_idx ON evidence (artifact_id) WHERE artifact_id IS NOT NULL;

CREATE TRIGGER evidence_immutable
    BEFORE UPDATE ON evidence
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

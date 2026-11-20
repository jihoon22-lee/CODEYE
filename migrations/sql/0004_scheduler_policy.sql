-- CODEYE: pool 정책 revision, controller 자동 증감 상태, 프로젝트 동시 실행 상한과 공정 배정 기록,
-- 노드의 워커 증감 방식(static/controller), 워커 상태 변경 시각.
--
-- 0001~0003과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 트리거는 NEW/OLD/TG_*만 사용,
-- 외래키는 ON DELETE RESTRICT. 새 pool의 정책·상태 행은 트리거가 아니라 pool을 만드는 Go 트랜잭션이
-- 함께 쓴다(IssueEnrollmentToken). 이 파일은 이미 있는 pool만 기본값으로 채운다.
--
-- 기존 행 처리:
--   * worker_pools: 각 pool에 기본 정책 revision 1(docs/EXECUTION.md 워커 풀과 자동 증감, min 0, max 2, idle 180s, scale-up 10s,
--     scale-down 60s)과 빈 증감 상태를 만든다. 기존 노드는 모두 static이므로 자동 증감 명령을 받지 않는다.
--   * scheduler_limits: project_max_running 기본 1(docs/EXECUTION.md 공정 순서), revision 1.
--   * workers: state_since는 적용 시각으로 채운다(유휴 시간은 이후 관측부터 잰다).

-- ---------------------------------------------------------------------------
-- scheduler_limits: 프로젝트별 동시 실행 상한과 revision
-- ---------------------------------------------------------------------------

ALTER TABLE scheduler_limits
    ADD COLUMN project_max_running integer NOT NULL DEFAULT 1 CHECK (project_max_running BETWEEN 1 AND 10000),
    ADD COLUMN revision bigint NOT NULL DEFAULT 1 CHECK (revision >= 1),
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT clock_timestamp();

CREATE FUNCTION codeye_scheduler_limits_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.revision < OLD.revision THEN
        RAISE EXCEPTION 'scheduler_limits.revision must not decrease' USING ERRCODE = 'CY003';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER scheduler_limits_guard
    BEFORE UPDATE ON scheduler_limits
    FOR EACH ROW EXECUTE FUNCTION codeye_scheduler_limits_guard();

-- ---------------------------------------------------------------------------
-- pool 정책 revision (append-only). 현재 정책 = pool의 가장 큰 revision.
-- ---------------------------------------------------------------------------

CREATE TABLE pool_policies (
    pool_id                     uuid        NOT NULL REFERENCES worker_pools (id) ON DELETE RESTRICT,
    revision                    bigint      NOT NULL CHECK (revision >= 1),
    min_workers                 integer     NOT NULL CHECK (min_workers BETWEEN 0 AND 256),
    max_workers                 integer     NOT NULL CHECK (max_workers BETWEEN 0 AND 256),
    idle_timeout_seconds        integer     NOT NULL CHECK (idle_timeout_seconds BETWEEN 1 AND 86400),
    scale_up_cooldown_seconds   integer     NOT NULL CHECK (scale_up_cooldown_seconds BETWEEN 0 AND 3600),
    scale_down_cooldown_seconds integer     NOT NULL CHECK (scale_down_cooldown_seconds BETWEEN 0 AND 3600),
    created_at                  timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (pool_id, revision),
    -- max=0은 min=0일 때만 허용된다(새 배정 중지).
    CHECK (min_workers <= max_workers)
);

CREATE TRIGGER pool_policies_immutable
    BEFORE UPDATE OR DELETE ON pool_policies
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

INSERT INTO pool_policies (pool_id, revision, min_workers, max_workers, idle_timeout_seconds,
                           scale_up_cooldown_seconds, scale_down_cooldown_seconds)
SELECT id, 1, 0, 2, 180, 10, 60 FROM worker_pools;

-- ---------------------------------------------------------------------------
-- pool 자동 증감 상태. reconcile 트랜잭션이 이 행을 잠가 pool당 하나의 controller만 판단하게 한다
-- (epoch는 판단마다 증가). 쿨다운 시각과 마지막 판단 결과를 DB에 두어 controller 재시작 뒤에도 유지한다.
-- ---------------------------------------------------------------------------

CREATE TABLE pool_scaler_state (
    pool_id            uuid        PRIMARY KEY REFERENCES worker_pools (id) ON DELETE RESTRICT,
    epoch              bigint      NOT NULL DEFAULT 0 CHECK (epoch >= 0),
    policy_revision    bigint      NULL CHECK (policy_revision IS NULL OR policy_revision >= 1),
    capacity_state     text        NOT NULL DEFAULT 'healthy'
        CHECK (capacity_state IN ('healthy', 'degraded_capacity', 'over_target_draining', 'paused')),
    reasons            text[]      NOT NULL DEFAULT '{}' CHECK (cardinality(reasons) <= 16),
    desired_workers    integer     NOT NULL DEFAULT 0 CHECK (desired_workers >= 0),
    live_workers       integer     NOT NULL DEFAULT 0 CHECK (live_workers >= 0),
    busy_workers       integer     NOT NULL DEFAULT 0 CHECK (busy_workers >= 0),
    draining_workers   integer     NOT NULL DEFAULT 0 CHECK (draining_workers >= 0),
    pending_starts     integer     NOT NULL DEFAULT 0 CHECK (pending_starts >= 0),
    queued_demand      integer     NOT NULL DEFAULT 0 CHECK (queued_demand >= 0),
    last_scale_up_at   timestamptz NULL,
    last_scale_down_at timestamptz NULL,
    reconciled_at      timestamptz NULL
);

CREATE FUNCTION codeye_pool_scaler_state_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% on % is not allowed', TG_OP, TG_TABLE_NAME USING ERRCODE = 'CY001';
    END IF;
    IF NEW.pool_id <> OLD.pool_id THEN
        RAISE EXCEPTION 'pool_scaler_state.pool_id is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.epoch < OLD.epoch THEN
        RAISE EXCEPTION 'pool_scaler_state.epoch must not decrease' USING ERRCODE = 'CY003';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER pool_scaler_state_guard
    BEFORE UPDATE OR DELETE ON pool_scaler_state
    FOR EACH ROW EXECUTE FUNCTION codeye_pool_scaler_state_guard();

INSERT INTO pool_scaler_state (pool_id) SELECT id FROM worker_pools;

-- ---------------------------------------------------------------------------
-- 프로젝트별 마지막 배정 시각(round-robin 공정성). claim 트랜잭션이 프로젝트 행 잠금 아래에서 갱신한다.
-- ---------------------------------------------------------------------------

CREATE TABLE project_dispatch (
    project_id         uuid        PRIMARY KEY REFERENCES projects (id) ON DELETE RESTRICT,
    last_dispatched_at timestamptz NOT NULL,
    dispatch_count     bigint      NOT NULL CHECK (dispatch_count >= 1)
);

CREATE FUNCTION codeye_project_dispatch_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION '% on % is not allowed', TG_OP, TG_TABLE_NAME USING ERRCODE = 'CY001';
    END IF;
    IF NEW.project_id <> OLD.project_id THEN
        RAISE EXCEPTION 'project_dispatch.project_id is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.dispatch_count < OLD.dispatch_count THEN
        RAISE EXCEPTION 'project_dispatch.dispatch_count must not decrease' USING ERRCODE = 'CY003';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER project_dispatch_guard
    BEFORE UPDATE OR DELETE ON project_dispatch
    FOR EACH ROW EXECUTE FUNCTION codeye_project_dispatch_guard();

-- ---------------------------------------------------------------------------
-- nodes: 워커 증감 방식과 agent 로컬 상한
--   static: agent가 --workers N을 스스로 유지한다. controller는 시작·축소 명령을 보내지 않는다.
--   controller: agent는 0개에서 시작하고 controller 명령으로만 워커를 시작·drain·종료한다.
-- ---------------------------------------------------------------------------

ALTER TABLE nodes
    ADD COLUMN worker_scaling text NOT NULL DEFAULT 'static' CHECK (worker_scaling IN ('static', 'controller')),
    ADD COLUMN max_workers integer NULL CHECK (max_workers IS NULL OR max_workers BETWEEN 1 AND 64),
    ADD CONSTRAINT nodes_controller_scaling_has_max CHECK (worker_scaling = 'static' OR max_workers IS NOT NULL);

-- ---------------------------------------------------------------------------
-- workers: 마지막 상태 변경 시각(유휴 시간 측정). last_seen_at은 heartbeat마다 바뀌므로 쓰지 않는다.
-- ---------------------------------------------------------------------------

ALTER TABLE workers
    ADD COLUMN state_since timestamptz NOT NULL DEFAULT clock_timestamp();

-- ---------------------------------------------------------------------------
-- node_commands: 워커 명령은 worker_id가 필요하다. 워커별 명령 조회 index.
-- ---------------------------------------------------------------------------

ALTER TABLE node_commands
    ADD CONSTRAINT node_commands_worker_kind_has_worker CHECK (kind = 'cancel_attempt' OR worker_id IS NOT NULL);

CREATE INDEX node_commands_worker_idx ON node_commands (worker_id)
    WHERE kind IN ('start_worker', 'drain_worker', 'stop_worker');

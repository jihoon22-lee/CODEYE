-- CODEYE W22: 예약(schedules)과 UTC occurrence, 예약으로 만든 run의 출처, task 가중치·계획 변경 순번.
--
-- 0001~0006과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 트리거는 NEW/OLD/TG_*만 사용한다. 이전 migration의 테이블·트리거는 바꾸지 않는다(열·테이블·트리거·색인만 추가).
--
-- 기존 행 처리(forward 전용):
--   * runs.schedule_id / scheduled_for: 기존 run은 모두 NULL(수동 요청)이다. 0007 이전에는 예약 경로가 없었으므로
--     사실과 같다. 값은 예약 occurrence를 등록하는 트랜잭션에서만 정하며 바꿀 수 없다.
--   * tasks.weight: 기존 행은 NULL(가중치 미선언)이다. 진행률 계산은 미선언 task를 1로 센다(작업 수 기준).
--     워커가 task_created의 선택 필드 weight로 선언한 값만 저장하고, 한 번 저장한 값은 바꿀 수 없다.
--   * tasks.created_sequence: 기존 행은 NULL이다. 이후 task를 만든 run 이벤트(task.created)의 sequence를 같은
--     트랜잭션에서 기록한다. 계획 확정(run.status running) 이후 생긴 task를 계획 변경으로 셀 때만 쓴다.
--     NULL인 task는 처음 계획에 속한 것으로 본다(추정해 채우지 않는다).

-- ---------------------------------------------------------------------------
-- tasks: 가중치와 생성 순번
-- ---------------------------------------------------------------------------

ALTER TABLE tasks
    ADD COLUMN weight integer NULL CHECK (weight IS NULL OR weight BETWEEN 1 AND 1000),
    ADD COLUMN created_sequence bigint NULL CHECK (created_sequence IS NULL OR created_sequence >= 1);

CREATE FUNCTION codeye_tasks_plan_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.weight IS NOT NULL AND NEW.weight IS DISTINCT FROM OLD.weight THEN
        RAISE EXCEPTION 'tasks.weight is immutable once set' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.created_sequence IS DISTINCT FROM OLD.created_sequence THEN
        RAISE EXCEPTION 'tasks.created_sequence is fixed at creation' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tasks_plan_guard
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION codeye_tasks_plan_guard();

-- ---------------------------------------------------------------------------
-- schedules
-- ---------------------------------------------------------------------------
-- cron은 5필드 표현, timezone은 IANA 이름이다(형식·발생 가능 여부는 서버가 검사). next_at은 아직 평가하지 않은
-- 첫 occurrence의 UTC 시각이며 비활성·삭제된 예약은 NULL이다. actor_id는 예약 run을 요청하는 사용자(마지막으로
-- 저장한 관리자)이고 occurrence마다 일반 요청과 같은 권한 검사를 받는다. created_by·idempotency_key·request_digest는
-- 생성 요청의 멱등성 키다(run의 멱등성 키와 별개).

CREATE TABLE schedules (
    id              uuid        PRIMARY KEY,
    project_id      uuid        NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    created_by      uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    actor_id        uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    idempotency_key text        NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 255),
    request_digest  text        NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    cron            text        NOT NULL CHECK (char_length(cron) BETWEEN 1 AND 200),
    timezone        text        NOT NULL CHECK (char_length(timezone) BETWEEN 1 AND 64),
    run_request     jsonb       NOT NULL CHECK (jsonb_typeof(run_request) = 'object'),
    overlap         text        NOT NULL CHECK (overlap IN ('coalesce_latest', 'skip', 'queue')),
    misfire         text        NOT NULL CHECK (misfire IN ('latest_once', 'skip')),
    enabled         boolean     NOT NULL,
    next_at         timestamptz NULL,
    version         bigint      NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    deleted_at      timestamptz NULL,
    UNIQUE (project_id, created_by, idempotency_key),
    UNIQUE (project_id, id),
    CONSTRAINT schedules_next_requires_enabled CHECK (next_at IS NULL OR (enabled AND deleted_at IS NULL)),
    CONSTRAINT schedules_deleted_disabled CHECK (deleted_at IS NULL OR NOT enabled)
);

CREATE INDEX schedules_due_idx ON schedules (next_at) WHERE next_at IS NOT NULL;
CREATE INDEX schedules_project_idx ON schedules (project_id, created_at, id) WHERE deleted_at IS NULL;

CREATE FUNCTION codeye_schedules_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.project_id <> OLD.project_id
       OR NEW.created_by <> OLD.created_by
       OR NEW.idempotency_key <> OLD.idempotency_key
       OR NEW.request_digest <> OLD.request_digest
       OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'schedules identity/request columns are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.version < OLD.version THEN
        RAISE EXCEPTION 'schedules.version must not decrease' USING ERRCODE = 'CY003';
    END IF;
    IF OLD.deleted_at IS NOT NULL AND NEW.deleted_at IS DISTINCT FROM OLD.deleted_at THEN
        RAISE EXCEPTION 'deleted schedule cannot be restored' USING ERRCODE = 'CY004';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER schedules_guard
    BEFORE UPDATE ON schedules
    FOR EACH ROW EXECUTE FUNCTION codeye_schedules_guard();

CREATE TRIGGER schedules_no_delete
    BEFORE DELETE ON schedules
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- schedule_occurrences
-- ---------------------------------------------------------------------------
-- (schedule_id, scheduled_for)가 occurrence의 유일 키다. 같은 예약 시각을 두 controller가 동시에 평가해도 한 행만
-- 생긴다. pending은 coalesce_latest에서 실행 중인 run이 끝나기를 기다리는 최신 occurrence이며 예약마다 최대 하나다.
-- 종료 상태(enqueued, skipped, coalesced, cancelled)는 바뀌지 않는다. enqueued occurrence의 run은
-- runs(schedule_id, scheduled_for)로 찾는다. missed_from은 latest_once가 실행하지 않고 건너뛴 이전 occurrence 구간의
-- 시작(그 이전 평가 이후 첫 occurrence)이다. 개별 누락 occurrence는 행으로 만들지 않는다(무제한 catch-up 금지).

CREATE TABLE schedule_occurrences (
    schedule_id   uuid        NOT NULL REFERENCES schedules (id) ON DELETE RESTRICT,
    scheduled_for timestamptz NOT NULL,
    state         text        NOT NULL CHECK (state IN ('pending', 'enqueued', 'skipped', 'coalesced', 'cancelled')),
    reason        text        NULL CHECK (reason IS NULL OR reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    missed_from   timestamptz NULL,
    created_at    timestamptz NOT NULL DEFAULT clock_timestamp(),
    decided_at    timestamptz NULL,
    PRIMARY KEY (schedule_id, scheduled_for),
    CONSTRAINT schedule_occurrences_missed_before CHECK (missed_from IS NULL OR missed_from < scheduled_for),
    CONSTRAINT schedule_occurrences_decided CHECK ((state = 'pending') = (decided_at IS NULL)),
    CONSTRAINT schedule_occurrences_reason CHECK (state NOT IN ('skipped', 'coalesced', 'cancelled') OR reason IS NOT NULL)
);

CREATE UNIQUE INDEX schedule_occurrences_one_pending ON schedule_occurrences (schedule_id) WHERE state = 'pending';

CREATE FUNCTION codeye_schedule_occurrences_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.schedule_id <> OLD.schedule_id
       OR NEW.scheduled_for <> OLD.scheduled_for
       OR NEW.created_at <> OLD.created_at
       OR NEW.missed_from IS DISTINCT FROM OLD.missed_from THEN
        RAISE EXCEPTION 'schedule_occurrences identity columns are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.state <> 'pending' THEN
        RAISE EXCEPTION 'schedule occurrence % is decided (attempted %)', OLD.state, NEW.state USING ERRCODE = 'CY004';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER schedule_occurrences_guard
    BEFORE UPDATE ON schedule_occurrences
    FOR EACH ROW EXECUTE FUNCTION codeye_schedule_occurrences_guard();

CREATE TRIGGER schedule_occurrences_no_delete
    BEFORE DELETE ON schedule_occurrences
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

CREATE TRIGGER schedule_occurrences_no_truncate
    BEFORE TRUNCATE ON schedule_occurrences
    FOR EACH STATEMENT EXECUTE FUNCTION codeye_reject_mutation();

-- ---------------------------------------------------------------------------
-- runs: 예약 출처
-- ---------------------------------------------------------------------------
-- 예약 run은 occurrence 한 개에만 묶인다(같은 occurrence로 run 두 개를 만들 수 없음). 예약과 run은 같은 프로젝트다.

ALTER TABLE runs
    ADD COLUMN schedule_id uuid NULL,
    ADD COLUMN scheduled_for timestamptz NULL,
    ADD CONSTRAINT runs_schedule_pair CHECK ((schedule_id IS NULL) = (scheduled_for IS NULL)),
    ADD CONSTRAINT runs_schedule_project_fk FOREIGN KEY (project_id, schedule_id)
        REFERENCES schedules (project_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT runs_schedule_occurrence_fk FOREIGN KEY (schedule_id, scheduled_for)
        REFERENCES schedule_occurrences (schedule_id, scheduled_for) ON DELETE RESTRICT;

CREATE UNIQUE INDEX runs_schedule_occurrence_idx ON runs (schedule_id, scheduled_for) WHERE schedule_id IS NOT NULL;
CREATE INDEX runs_schedule_status_idx ON runs (schedule_id, status) WHERE schedule_id IS NOT NULL;

CREATE FUNCTION codeye_runs_schedule_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.schedule_id IS DISTINCT FROM OLD.schedule_id OR NEW.scheduled_for IS DISTINCT FROM OLD.scheduled_for THEN
        RAISE EXCEPTION 'runs schedule origin is fixed at creation' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER runs_schedule_guard
    BEFORE UPDATE ON runs
    FOR EACH ROW EXECUTE FUNCTION codeye_runs_schedule_guard();

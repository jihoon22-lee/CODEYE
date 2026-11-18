-- CODEYE W15: task 표시 단계와 서버가 수용한 task 시작·종료 시각, 실행 수명 이벤트 조회 색인.
--
-- 0001~0005와 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 트리거는 NEW/OLD/TG_*만 사용.
-- 이전 migration의 테이블·트리거는 바꾸지 않는다(열·트리거·색인만 추가).
--
-- 기존 행 처리(forward 전용):
--   * tasks.stage: 모든 기존 행은 NULL로 남는다. backfill하지 않고 key 접두어로 추정하지 않는다.
--     단계가 없는 task가 있는 실행은 API에서 detail "not_recorded"로 표시한다(docs/37 §2, §8).
--     값은 워커의 task_created 선택 필드 stage에서만 저장하며, 한 번 저장한 값은 바꿀 수 없다.
--   * tasks.started_at / finished_at: 기존 행은 NULL이다. 이후 서버가 task 전이를 수용한 DB 시각
--     (clock_timestamp)으로 채운다. 워커 시계는 쓰지 않는다. run 재시도로 task가 queued로 돌아가면
--     둘 다 NULL로 되돌린다(실행 이력은 task_executions에 남는다).

ALTER TABLE tasks
    ADD COLUMN stage text NULL CHECK (stage IS NULL OR stage IN (
        'environment', 'documents', 'static_analysis', 'code_review', 'build', 'test')),
    ADD COLUMN started_at timestamptz NULL,
    ADD COLUMN finished_at timestamptz NULL;

CREATE FUNCTION codeye_tasks_stage_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.stage IS NOT NULL AND NEW.stage IS DISTINCT FROM OLD.stage THEN
        RAISE EXCEPTION 'tasks.stage is immutable once set' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER tasks_stage_guard
    BEFORE UPDATE ON tasks
    FOR EACH ROW EXECUTE FUNCTION codeye_tasks_stage_guard();

-- 단계 집계는 현재 attempt의 경계(마지막 lease 또는 재시도)와 그 뒤의 run 상태·snapshot 이벤트만 읽는다.
-- run마다 수가 적은 수명 이벤트만 담는 부분 색인으로 목록 조회가 run의 전체 이벤트를 훑지 않게 한다.
CREATE INDEX events_lifecycle_idx ON events (run_id, sequence)
    WHERE type IN ('attempt.leased', 'run.retried', 'run.status', 'snapshot.fixed', 'snapshot.confirmed');

-- CODEYE: pull request 분석 run의 고정 입력과 pull request 댓글 게시 기록.
--
-- 0001~0011과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 트리거는 NEW/OLD/TG_*만 사용한다. 기존 행은 바꾸지 않는다.

-- ---------------------------------------------------------------------------
-- runs.pull_request: run을 대기열에 넣을 때 provider API로 확인한 pull request 고정값
-- ---------------------------------------------------------------------------
-- runs.request는 사용자가 보낸 요청(번호와 선택적 댓글 선택)이고 idempotency 비교의 원천이다.
-- 고정값(base/head SHA, head 저장소, 변경 파일과 줄 범위)은 서버가 만들어 이 열에 한 번만 쓴다.

ALTER TABLE runs
    ADD COLUMN pull_request jsonb NULL CHECK (pull_request IS NULL OR jsonb_typeof(pull_request) = 'object');

CREATE FUNCTION codeye_runs_pull_request_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.pull_request IS DISTINCT FROM OLD.pull_request THEN
        RAISE EXCEPTION 'runs.pull_request is fixed at creation' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER runs_pull_request_guard
    BEFORE UPDATE ON runs
    FOR EACH ROW EXECUTE FUNCTION codeye_runs_pull_request_guard();

-- ---------------------------------------------------------------------------
-- pull_request_comments: 결과 댓글 게시 outbox(run마다 한 행)
-- ---------------------------------------------------------------------------
-- 보고서가 게시된 pull request run 가운데 댓글을 켠 run이 한 행을 갖는다. controller가 pending 행을
-- next_attempt_at 순으로 게시하고 결과를 남긴다. 게시 실패는 분석 결과를 바꾸지 않는다.
-- 같은 pull request(repository, number)에서 더 늦게 만든 run의 댓글이 이미 게시됐으면 이전 run은
-- superseded로 끝나 새 head SHA의 댓글을 덮지 않는다.

CREATE TABLE pull_request_comments (
    run_id          uuid        PRIMARY KEY REFERENCES runs (id) ON DELETE RESTRICT,
    project_id      uuid        NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    -- host/OWNER/REPO (소문자)와 번호.
    repository      text        NOT NULL CHECK (char_length(repository) BETWEEN 3 AND 512),
    number          integer     NOT NULL CHECK (number > 0),
    head_sha        text        NOT NULL CHECK (head_sha ~ '^([0-9a-f]{40}|[0-9a-f]{64})$'),
    state           text        NOT NULL CHECK (state IN ('pending', 'posted', 'superseded', 'failed')),
    attempts        integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    reason          text        NULL CHECK (reason IS NULL OR reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    comment_id      bigint      NULL CHECK (comment_id IS NULL OR comment_id > 0),
    comment_url     text        NULL CHECK (comment_url IS NULL OR char_length(comment_url) <= 2048),
    report_revision integer     NULL CHECK (report_revision IS NULL OR report_revision >= 1),
    created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (project_id, run_id) REFERENCES runs (project_id, id) ON DELETE RESTRICT
);

CREATE INDEX pull_request_comments_due_idx ON pull_request_comments (next_attempt_at) WHERE state = 'pending';
CREATE INDEX pull_request_comments_target_idx ON pull_request_comments (repository, number);

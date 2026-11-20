-- CODEYE: 게시된 구조화 보고서와 게시 거부 기록.
--
-- 0001~0004과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT.
-- 이전 migration의 테이블·트리거는 바꾸지 않는다(새 테이블만 추가).
--
-- reports는 append-only다. 같은 run의 revision은 1부터 증가하고 같은 builder 입력(input_digest)은
-- 한 번만 게시된다(controller 재시작·동시 reconcile의 멱등성). 본문은 canonical JSON bytes이며
-- body_sha256은 DB가 본문에서 다시 계산한 값과 같아야 한다. 보고서는 attempt(run_id, id)와
-- 그 attempt의 lease epoch에 묶인다.

CREATE TABLE reports (
    id                 uuid        PRIMARY KEY,
    project_id         uuid        NOT NULL,
    run_id             uuid        NOT NULL,
    revision           integer     NOT NULL CHECK (revision >= 1),
    attempt_id         uuid        NOT NULL,
    lease_epoch        bigint      NOT NULL CHECK (lease_epoch >= 1),
    result_manifest_id uuid        NULL REFERENCES artifacts (id) ON DELETE RESTRICT,
    input_digest       text        NOT NULL CHECK (input_digest ~ '^[0-9a-f]{64}$'),
    schema_version     text        NOT NULL CHECK (schema_version ~ '^[0-9]+\.[0-9]+$'),
    builder_version    text        NOT NULL CHECK (builder_version ~ '^[a-z0-9][a-z0-9._-]{0,31}$'),
    status             text        NOT NULL CHECK (status IN ('completed', 'partial', 'failed', 'cancelled')),
    quality_verdict    text        NOT NULL CHECK (quality_verdict IN ('attention_required', 'no_actionable_findings', 'inconclusive')),
    body               bytea       NOT NULL CHECK (octet_length(body) BETWEEN 2 AND 67108864),
    body_sha256        text        NOT NULL CHECK (body_sha256 ~ '^[0-9a-f]{64}$'),
    created_at         timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT reports_body_digest CHECK (body_sha256 = encode(sha256(body), 'hex')),
    -- 깨끗한 판정은 completed 보고서에서만 가능하다(나머지 규칙은 게시 서비스가 검사).
    CONSTRAINT reports_clean_requires_completed CHECK (quality_verdict <> 'no_actionable_findings' OR status = 'completed'),
    UNIQUE (run_id, revision),
    UNIQUE (run_id, input_digest),
    FOREIGN KEY (project_id, run_id) REFERENCES runs (project_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (run_id, attempt_id) REFERENCES attempts (run_id, id) ON DELETE RESTRICT
);

CREATE INDEX reports_project_idx ON reports (project_id);
CREATE INDEX reports_attempt_idx ON reports (attempt_id);

CREATE TRIGGER reports_immutable
    BEFORE UPDATE OR DELETE ON reports
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

CREATE TRIGGER reports_no_truncate
    BEFORE TRUNCATE ON reports
    FOR EACH STATEMENT EXECUTE FUNCTION codeye_reject_mutation();

-- 결정적으로 거부된 게시 입력(예: source manifest bytes 없음·불일치). 같은 attempt를 무한히 재시도하지
-- 않기 위한 기록이며 attempt당 한 행이다. 일시 오류(DB·파일 읽기 실패)는 기록하지 않는다.
CREATE TABLE report_rejections (
    attempt_id   uuid        PRIMARY KEY,
    run_id       uuid        NOT NULL,
    reason       text        NOT NULL CHECK (reason ~ '^[a-z][a-z0-9_]{0,63}$'),
    input_digest text        NULL CHECK (input_digest IS NULL OR input_digest ~ '^[0-9a-f]{64}$'),
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (run_id, attempt_id) REFERENCES attempts (run_id, id) ON DELETE RESTRICT
);

CREATE TRIGGER report_rejections_immutable
    BEFORE UPDATE OR DELETE ON report_rejections
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

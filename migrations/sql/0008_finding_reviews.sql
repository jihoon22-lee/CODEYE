-- CODEYE: 사람의 finding 판정 이력(append-only).
--
-- 0001~0007과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 이전 migration의 테이블·트리거는 바꾸지 않는다(새 테이블만 추가).
--
-- 비교(new/persisting/changed/absent_unverified/resolved_observed)는 저장하지 않는다. 두 불변 보고서
-- revision 본문(fp2 fingerprint·검사한 경로 포함)과 run의 source variant·scope에서 요청 때마다 같은
-- 결과로 다시 계산한다. 파생 테이블을 두면 보고서와 어긋날 수 있기 때문이다.
--
-- finding_reviews는 한 project 안 fingerprint별 판정의 version 이력이자 감사 기록이다. 행은 수정·삭제할 수
-- 없고 새 판정은 version+1 행이다. 현재 판정은 최신 version이며, 만료(expires_at)가 지나면 현재 화면에서는
-- open으로 읽는다. 각 행은 사람이 판단한 보고서 revision과 그 안의 finding을 가리킨다(run_id, report_revision,
-- finding_id). 같은 actor의 같은 Idempotency-Key는 한 행만 만든다(request_digest로 다른 요청을 구분).

CREATE TABLE finding_reviews (
    id              uuid        PRIMARY KEY,
    project_id      uuid        NOT NULL REFERENCES projects (id) ON DELETE RESTRICT,
    fingerprint     text        NOT NULL CHECK (fingerprint ~ '^fp2:[0-9a-f]{64}$'),
    version         bigint      NOT NULL CHECK (version >= 1),
    disposition     text        NOT NULL CHECK (disposition IN (
                        'open', 'acknowledged', 'accepted_risk', 'false_positive', 'fixed_by_user', 'suppressed')),
    reason          text        NOT NULL CHECK (char_length(btrim(reason)) BETWEEN 1 AND 2000),
    -- snapshot 상대 경로. 이 경로(또는 그 아래)에 있는 발견에만 적용한다.
    path_prefix     text        NULL CHECK (path_prefix IS NULL OR (char_length(path_prefix) BETWEEN 1 AND 1024
                        AND path_prefix !~ '^/' AND path_prefix !~ '(^|/)\.\.?(/|$)' AND strpos(path_prefix, E'\\') = 0)),
    expires_at      timestamptz NULL,
    actor_id        uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    run_id          uuid        NOT NULL,
    report_revision integer     NOT NULL CHECK (report_revision >= 1),
    finding_id      text        NOT NULL CHECK (char_length(finding_id) BETWEEN 1 AND 200),
    idempotency_key text        NOT NULL CHECK (char_length(idempotency_key) BETWEEN 1 AND 255),
    request_digest  text        NOT NULL CHECK (request_digest ~ '^[0-9a-f]{64}$'),
    created_at      timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (project_id, fingerprint, version),
    UNIQUE (project_id, actor_id, idempotency_key),
    FOREIGN KEY (project_id, run_id) REFERENCES runs (project_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (run_id, report_revision) REFERENCES reports (run_id, revision) ON DELETE RESTRICT,
    CHECK (expires_at IS NULL OR expires_at > created_at),
    -- open으로 되돌리는 판정은 만료·경로 범위를 갖지 않는다.
    CHECK (disposition <> 'open' OR (expires_at IS NULL AND path_prefix IS NULL))
);

CREATE INDEX finding_reviews_actor_idx ON finding_reviews (actor_id);
CREATE INDEX finding_reviews_run_idx ON finding_reviews (run_id, report_revision);

CREATE TRIGGER finding_reviews_immutable
    BEFORE UPDATE OR DELETE ON finding_reviews
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

CREATE TRIGGER finding_reviews_no_truncate
    BEFORE TRUNCATE ON finding_reviews
    FOR EACH STATEMENT EXECUTE FUNCTION codeye_reject_mutation();

-- version은 fingerprint마다 1부터 빈틈없이 증가한다(동시 기록은 저장 계층의 project 행 잠금과 유일 제약이 막는다).
CREATE FUNCTION codeye_finding_reviews_version() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    latest bigint;
BEGIN
    EXECUTE format('SELECT COALESCE(max(version), 0) FROM %I.finding_reviews WHERE project_id = $1 AND fingerprint = $2',
                   TG_TABLE_SCHEMA)
        INTO latest USING NEW.project_id, NEW.fingerprint;
    IF NEW.version <> latest + 1 THEN
        RAISE EXCEPTION 'finding review version must follow % (got %)', latest, NEW.version USING ERRCODE = 'CY003';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER finding_reviews_version
    BEFORE INSERT ON finding_reviews
    FOR EACH ROW EXECUTE FUNCTION codeye_finding_reviews_version();

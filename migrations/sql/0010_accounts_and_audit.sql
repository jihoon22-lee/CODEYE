-- CODEYE: 로컬 계정(사용자 이름·비밀번호), 전역 관리자, 로그인 제한, 감사 기록.
--
-- 0001~0009와 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 트리거는 NEW/OLD/TG_*만 사용한다. 이전 migration의 테이블·트리거는 바꾸지 않고 열·테이블만 더한다.
--
-- 기존 users 행은 사용자 이름·비밀번호가 없는 "로그인 링크 전용" 사용자로 읽힌다(is_admin=false,
-- must_change_password=false). 비밀번호 원문은 저장하지 않는다. 사용자마다 다른 salt와 KDF 이름·반복 수를
-- 해시 옆에 저장하므로 반복 수를 나중에 올릴 수 있다(로그인 성공 때 다시 계산한다).

ALTER TABLE users
    -- 소문자로 정규화한 이름만 저장한다. 대소문자를 구분하지 않는 유일성은 이 정규화가 보장한다.
    ADD COLUMN username text NULL
        CHECK (username IS NULL OR username ~ '^[a-z0-9][a-z0-9._-]{1,63}$'),
    ADD COLUMN is_admin boolean NOT NULL DEFAULT false,
    ADD COLUMN must_change_password boolean NOT NULL DEFAULT false,
    ADD COLUMN password_kdf text NULL CHECK (password_kdf IS NULL OR password_kdf IN ('pbkdf2-sha256')),
    ADD COLUMN password_iterations integer NULL CHECK (password_iterations IS NULL OR password_iterations >= 1000),
    ADD COLUMN password_salt bytea NULL CHECK (password_salt IS NULL OR octet_length(password_salt) BETWEEN 16 AND 64),
    ADD COLUMN password_hash bytea NULL CHECK (password_hash IS NULL OR octet_length(password_hash) = 32),
    ADD COLUMN password_set_at timestamptz NULL,
    ADD CONSTRAINT users_password_complete CHECK (
        (password_kdf IS NULL) = (password_iterations IS NULL)
        AND (password_kdf IS NULL) = (password_salt IS NULL)
        AND (password_kdf IS NULL) = (password_hash IS NULL)
        AND (password_kdf IS NULL) = (password_set_at IS NULL)),
    -- 비밀번호는 사용자 이름으로만 쓴다. 비밀번호 변경 요구는 사용자 이름이 있는 계정에만 둔다.
    ADD CONSTRAINT users_password_needs_username CHECK (password_kdf IS NULL OR username IS NOT NULL),
    ADD CONSTRAINT users_must_change_needs_username CHECK (NOT must_change_password OR username IS NOT NULL);

CREATE UNIQUE INDEX users_username_idx ON users (username) WHERE username IS NOT NULL;
CREATE INDEX users_enabled_admin_idx ON users (id) WHERE is_admin AND disabled_at IS NULL;

-- 사용자 이름은 한 번 정하면 바꾸지 않는다. 감사 기록은 사용자 ID를 가리키지만 화면은 이름으로 읽는다.
CREATE FUNCTION codeye_users_account_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.username IS NOT NULL AND NEW.username IS DISTINCT FROM OLD.username THEN
        RAISE EXCEPTION 'username is immutable once set' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER users_account_guard
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION codeye_users_account_guard();

-- ---------------------------------------------------------------------------
-- 로그인 제한
-- ---------------------------------------------------------------------------

-- 제한 키의 HMAC 키(한 행, 불변). 제출된 사용자 이름(사람이 비밀번호를 잘못 넣을 수 있다)과 client 주소를
-- 원문이나 단순 hash로 저장하지 않으려고 쓴다. 여러 controller가 같은 키로 같은 행을 가리킨다.
CREATE TABLE auth_secrets (
    id           smallint    PRIMARY KEY CHECK (id = 1),
    throttle_key bytea       NOT NULL CHECK (octet_length(throttle_key) = 32),
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO auth_secrets (id, throttle_key)
VALUES (1, decode(replace(gen_random_uuid()::text || gen_random_uuid()::text, '-', ''), 'hex'));

CREATE TRIGGER auth_secrets_immutable
    BEFORE UPDATE OR DELETE ON auth_secrets
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

-- 실패 횟수와 잠금 시각은 DB 시각으로 판단한다. scope account의 키는 제출된 사용자 이름(존재 여부와 무관),
-- client의 키는 연결 주소(IPv6는 /64)다. 행은 저장 계층이 갱신·삭제한다.
CREATE TABLE auth_throttle (
    scope           text        NOT NULL CHECK (scope IN ('account', 'client')),
    key_hmac        text        NOT NULL CHECK (key_hmac ~ '^[0-9a-f]{64}$'),
    failures        integer     NOT NULL DEFAULT 0 CHECK (failures >= 0),
    last_failure_at timestamptz NULL,
    locked_until    timestamptz NULL,
    -- 이번 잠금에서 거절을 감사 기록에 남겼는지. 잠금마다 첫 거절 한 번만 기록한다.
    lock_audited    boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (scope, key_hmac)
);

-- ---------------------------------------------------------------------------
-- 감사 기록(append-only)
-- ---------------------------------------------------------------------------

-- 시각은 DB 시각이다. detail은 저장 계층이 허용 key로만 만든 작은 객체이며 비밀번호·token·비밀값·소스 본문·
-- 서버 절대 경로를 담지 않는다. 모델 추론·프롬프트는 기록하지 않는다.
CREATE TABLE audit_events (
    id            bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    actor_kind    text        NOT NULL CHECK (actor_kind IN ('user', 'operator_cli', 'node', 'system', 'anonymous')),
    -- user: 행위자. system: 그 행위를 대신 실행한 사용자(예약의 actor). anonymous: 없음.
    actor_user_id uuid        NULL REFERENCES users (id) ON DELETE RESTRICT,
    actor_node_id uuid        NULL REFERENCES nodes (id) ON DELETE RESTRICT,
    action        text        NOT NULL CHECK (action IN (
                      'auth.login_succeeded', 'auth.login_failed', 'auth.login_throttled', 'auth.logout',
                      'auth.password_changed', 'auth.password_change_failed', 'auth.password_set',
                      'auth.password_reset', 'auth.login_link_issued',
                      'user.created', 'user.disabled', 'user.admin_granted', 'user.admin_revoked',
                      'source_root.added', 'source_root.granted', 'source_root.grant_revoked', 'provider.added',
                      'project.registered', 'project.revised', 'project.member_set', 'project.member_removed',
                      'run.requested', 'run.cancel_requested', 'report.exported', 'finding_review.recorded',
                      'schedule.created', 'schedule.updated', 'schedule.deleted',
                      'node.enrollment_token_created', 'node.enrollment_token_revoked', 'node.enrolled', 'node.revoked',
                      'pool.policy_set', 'scheduler.limits_set', 'scheduler.artifact_quota_set',
                      'attempt.stale_rejected')),
    target_type   text        NULL CHECK (target_type IS NULL OR target_type IN (
                      'user', 'session', 'project', 'run', 'report', 'finding_review', 'schedule', 'source_root',
                      'provider', 'node', 'enrollment_token', 'pool', 'scheduler', 'attempt')),
    target_id     text        NULL CHECK (target_id IS NULL OR char_length(target_id) BETWEEN 1 AND 200),
    project_id    uuid        NULL REFERENCES projects (id) ON DELETE RESTRICT,
    request_id    text        NULL CHECK (request_id IS NULL OR request_id ~ '^req_[0-9a-f]{24}$'),
    detail        jsonb       NOT NULL DEFAULT '{}'::jsonb
                      CHECK (jsonb_typeof(detail) = 'object' AND octet_length(detail::text) <= 4096),
    CHECK ((target_type IS NULL) = (target_id IS NULL)),
    CHECK (actor_kind <> 'user' OR actor_user_id IS NOT NULL),
    CHECK (actor_kind NOT IN ('operator_cli', 'anonymous') OR actor_user_id IS NULL),
    CHECK ((actor_kind = 'node') = (actor_node_id IS NOT NULL))
);

CREATE INDEX audit_events_time_idx ON audit_events (occurred_at DESC, id DESC);
CREATE INDEX audit_events_project_idx ON audit_events (project_id, id DESC) WHERE project_id IS NOT NULL;
CREATE INDEX audit_events_actor_idx ON audit_events (actor_user_id, id DESC) WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_events_action_idx ON audit_events (action, id DESC);

CREATE TRIGGER audit_events_immutable
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

CREATE TRIGGER audit_events_no_truncate
    BEFORE TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION codeye_reject_mutation();

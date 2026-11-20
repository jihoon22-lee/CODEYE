-- CODEYE: 로그인 링크 발급자 기록, 비밀번호 초기화 알림, 로그인 제한 재설계.
--
-- 0001~0010과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 트리거는 NEW/OLD/TG_*만 사용한다.
--
-- 이 migration은 기존 행을 두 곳에서 바꾼다.
--   1. 아직 쓰지 않은 로그인 링크를 끝낸다(used_at). 0010까지는 링크에 발급자가 없어 관리자가 발급한 링크와
--      운영자 CLI가 발급한 링크를 구분할 수 없다. 링크는 길어야 한 시간이므로 운영자가 다시 발급한다.
--   2. auth_throttle을 다시 만든다. 행은 최근 실패 횟수뿐이며 잃어도 제한이 처음부터 다시 셀 뿐이다.

-- ---------------------------------------------------------------------------
-- 로그인 링크 발급자와 목적
-- ---------------------------------------------------------------------------

ALTER TABLE auth_launch_tokens
    -- 발급한 관리자. NULL은 운영자 CLI(DB URL을 가진 사람)다.
    ADD COLUMN issued_by uuid NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- login: 운영자의 login-link·serve --user·codeye start. account_created: 계정 생성.
    -- password_reset: 비밀번호 초기화.
    ADD COLUMN purpose text NOT NULL DEFAULT 'login'
        CHECK (purpose IN ('login', 'account_created', 'password_reset'));

UPDATE auth_launch_tokens SET used_at = clock_timestamp()
WHERE used_at IS NULL AND expires_at > clock_timestamp();

CREATE FUNCTION codeye_launch_tokens_origin_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.issued_by IS DISTINCT FROM OLD.issued_by OR NEW.purpose <> OLD.purpose THEN
        RAISE EXCEPTION 'launch token origin is immutable' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER auth_launch_tokens_origin_guard
    BEFORE UPDATE ON auth_launch_tokens
    FOR EACH ROW EXECUTE FUNCTION codeye_launch_tokens_origin_guard();

ALTER TABLE auth_sessions
    -- 관리자가 발급한 링크로 시작한 세션이면 그 관리자. 이 세션이 남기는 감사 행마다 같은 값을 적는다.
    ADD COLUMN link_issued_by uuid NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- 계정 생성·초기화 링크로 시작한 세션은 이 세션에서 비밀번호를 정할 때까지 비밀번호 변경만 할 수 있다.
    -- true에서 false로만 바뀐다.
    ADD COLUMN password_required boolean NOT NULL DEFAULT false;

CREATE FUNCTION codeye_sessions_origin_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.link_issued_by IS DISTINCT FROM OLD.link_issued_by THEN
        RAISE EXCEPTION 'session origin is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF NEW.password_required AND NOT OLD.password_required THEN
        RAISE EXCEPTION 'a session cannot gain a password requirement' USING ERRCODE = 'CY001';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER auth_sessions_origin_guard
    BEFORE UPDATE ON auth_sessions
    FOR EACH ROW EXECUTE FUNCTION codeye_sessions_origin_guard();

-- ---------------------------------------------------------------------------
-- 비밀번호 초기화 알림
-- ---------------------------------------------------------------------------

ALTER TABLE users
    -- 관리자가 한 마지막 비밀번호 초기화의 시각과 그 관리자. 사용자의 세션 조회가 초기화 뒤 정해진 기간 동안
    -- 보여 준다. 사용자가 지울 수 없다(계정을 넘겨받은 사람도 지우지 못하게). 권위 있는 기록은 감사 행이다.
    ADD COLUMN password_reset_at timestamptz NULL,
    ADD COLUMN password_reset_by uuid NULL REFERENCES users (id) ON DELETE RESTRICT,
    ADD CONSTRAINT users_password_reset_pair CHECK ((password_reset_at IS NULL) = (password_reset_by IS NULL));

-- ---------------------------------------------------------------------------
-- 로그인 제한(재설계)
-- ---------------------------------------------------------------------------

-- 지연 종료 시각을 저장하지 않고 (failures, last_failure_at)에서 계산한다. 마지막 실패에서 15분이
-- 지난 행은 0회와 같으며 저장 계층이 지운다. 키는 auth_secrets.throttle_key의 HMAC이다.
--   pair: 제출된 사용자 이름(존재 여부와 무관)과 client 주소의 쌍. client: 주소.
DROP TABLE auth_throttle;

CREATE TABLE auth_throttle (
    scope           text        NOT NULL CHECK (scope IN ('pair', 'client')),
    key_hmac        text        NOT NULL CHECK (key_hmac ~ '^[0-9a-f]{64}$'),
    failures        integer     NOT NULL CHECK (failures >= 0),
    last_failure_at timestamptz NOT NULL,
    -- 이번 잠금에서 거절을 감사 기록에 남겼는지. 실패를 셀 때마다 false로 돌아간다.
    lock_audited    boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (scope, key_hmac)
);

CREATE INDEX auth_throttle_last_failure_idx ON auth_throttle (last_failure_at);

-- ---------------------------------------------------------------------------
-- 감사 action(0010 목록에 더한다)
-- ---------------------------------------------------------------------------

ALTER TABLE audit_events DROP CONSTRAINT audit_events_action_check;

ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check CHECK (action IN (
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
    'attempt.stale_rejected'));

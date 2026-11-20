-- CODEYE: 사용자 세션, 일회용 launch token, source root·provider 등록.
--
-- 0001과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 트리거는 NEW/OLD/TG_*만 사용,
-- 외래키는 ON DELETE RESTRICT. token 원문은 저장하지 않고 SHA-256 hex만 저장한다.
-- source root·provider 등록은 선언일 뿐이다. 실제 경로·저장소 접근은 소스 취득(acquire)에서만 확인한다.
-- provider에는 자격증명 열이 없다(공개 HTTPS GitHub/GHES만; 비공개 인증은 미구현).

ALTER TABLE users
    ADD COLUMN display_name text NULL
        CHECK (display_name IS NULL OR char_length(btrim(display_name)) BETWEEN 1 AND 200),
    ADD COLUMN disabled_at timestamptz NULL,
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT clock_timestamp();

CREATE FUNCTION codeye_users_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id OR NEW.created_at <> OLD.created_at THEN
        RAISE EXCEPTION 'users identity is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.disabled_at IS NOT NULL AND NEW.disabled_at IS DISTINCT FROM OLD.disabled_at THEN
        RAISE EXCEPTION 'disabled user cannot be re-enabled or re-dated' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER users_guard
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION codeye_users_guard();

ALTER TABLE projects
    ADD COLUMN created_at timestamptz NOT NULL DEFAULT clock_timestamp();

CREATE INDEX projects_created_at_id_idx ON projects (created_at DESC, id DESC);

-- ---------------------------------------------------------------------------
-- launch token / session
-- ---------------------------------------------------------------------------

CREATE TABLE auth_launch_tokens (
    token_sha256 text        PRIMARY KEY CHECK (token_sha256 ~ '^[0-9a-f]{64}$'),
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at   timestamptz NOT NULL,
    used_at      timestamptz NULL,
    CHECK (expires_at > created_at),
    CHECK (used_at IS NULL OR used_at >= created_at)
);

CREATE INDEX auth_launch_tokens_user_idx ON auth_launch_tokens (user_id);

CREATE FUNCTION codeye_launch_tokens_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.token_sha256 <> OLD.token_sha256
       OR NEW.user_id <> OLD.user_id
       OR NEW.created_at <> OLD.created_at
       OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'launch token identity is immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.used_at IS NOT NULL AND NEW.used_at IS DISTINCT FROM OLD.used_at THEN
        RAISE EXCEPTION 'launch token is already used' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER auth_launch_tokens_guard
    BEFORE UPDATE ON auth_launch_tokens
    FOR EACH ROW EXECUTE FUNCTION codeye_launch_tokens_guard();

CREATE TABLE auth_sessions (
    id           uuid        PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    token_sha256 text        NOT NULL UNIQUE CHECK (token_sha256 ~ '^[0-9a-f]{64}$'),
    created_at   timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz NULL,
    CHECK (expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
);

CREATE INDEX auth_sessions_user_idx ON auth_sessions (user_id);

CREATE FUNCTION codeye_sessions_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id <> OLD.id
       OR NEW.user_id <> OLD.user_id
       OR NEW.token_sha256 <> OLD.token_sha256
       OR NEW.created_at <> OLD.created_at
       OR NEW.expires_at <> OLD.expires_at THEN
        RAISE EXCEPTION 'session identity and expiry are immutable' USING ERRCODE = 'CY001';
    END IF;
    IF OLD.revoked_at IS NOT NULL AND NEW.revoked_at IS DISTINCT FROM OLD.revoked_at THEN
        RAISE EXCEPTION 'session is already revoked' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER auth_sessions_guard
    BEFORE UPDATE ON auth_sessions
    FOR EACH ROW EXECUTE FUNCTION codeye_sessions_guard();

-- ---------------------------------------------------------------------------
-- source roots (운영자가 CLI로 등록하는 서버 로컬 경로) / provider registry
-- ---------------------------------------------------------------------------

CREATE TABLE source_roots (
    id         uuid        PRIMARY KEY,
    label      text        NOT NULL UNIQUE CHECK (char_length(btrim(label)) BETWEEN 1 AND 100),
    -- 서버(controller) 로컬 절대 경로. API 응답에 포함하지 않는다.
    path       text        NOT NULL CHECK (char_length(path) BETWEEN 1 AND 4096),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER source_roots_immutable
    BEFORE UPDATE ON source_roots
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

CREATE TABLE source_root_grants (
    source_root_id uuid        NOT NULL REFERENCES source_roots (id) ON DELETE RESTRICT,
    user_id        uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (source_root_id, user_id)
);

CREATE INDEX source_root_grants_user_idx ON source_root_grants (user_id);

CREATE TABLE providers (
    id         uuid        PRIMARY KEY,
    kind       text        NOT NULL CHECK (kind IN ('github', 'ghes')),
    host       text        NOT NULL CHECK (host ~ '^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:[0-9]{1,5})?$'),
    api_url    text        NULL CHECK (api_url IS NULL OR api_url ~ '^https://[^\s@?#]+$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (kind, host),
    CHECK (kind <> 'github' OR host = 'github.com')
);

CREATE TRIGGER providers_immutable
    BEFORE UPDATE ON providers
    FOR EACH ROW EXECUTE FUNCTION codeye_reject_mutation();

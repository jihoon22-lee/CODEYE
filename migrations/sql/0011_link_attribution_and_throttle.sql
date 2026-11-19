-- CODEYE: 로그인 제한 재설계.
--
-- 0001~0010과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 트리거는 NEW/OLD/TG_*만 사용한다.
--
-- auth_throttle을 다시 만든다. 행은 최근 실패 횟수뿐이며 잃어도 제한이 처음부터 다시 셀 뿐이다.

-- ---------------------------------------------------------------------------
-- 로그인 제한(재설계)
-- ---------------------------------------------------------------------------

-- 잠금 시각을 저장하지 않고 (failures, last_failure_at)에서 계산한다. 마지막 실패에서 15분이 지난 행은
-- 0회와 같으며 저장 계층이 지운다. 키는 auth_secrets.throttle_key의 HMAC이다.
--   pair: 제출된 사용자 이름과 client 주소의 쌍. account: 사용자 이름(존재 여부와 무관). client: 주소.
DROP TABLE auth_throttle;

CREATE TABLE auth_throttle (
    scope           text        NOT NULL CHECK (scope IN ('pair', 'account', 'client')),
    key_hmac        text        NOT NULL CHECK (key_hmac ~ '^[0-9a-f]{64}$'),
    failures        integer     NOT NULL CHECK (failures >= 0),
    last_failure_at timestamptz NOT NULL,
    -- 이번 잠금에서 거절을 감사 기록에 남겼는지. 실패를 셀 때마다 false로 돌아간다.
    lock_audited    boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (scope, key_hmac)
);

CREATE INDEX auth_throttle_last_failure_idx ON auth_throttle (last_failure_at);

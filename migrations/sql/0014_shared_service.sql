-- CODEYE: 여러 사람이 쓰는 한 서비스의 허가(모든 사용자에게 연 폴더, 서비스 설정)와 감사 action.
--
-- 0001~0013과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 트리거는 NEW/OLD/TG_*만 사용한다. 기존 행은 바꾸지 않는다.

-- ---------------------------------------------------------------------------
-- source_root_everyone: 모든 활성 사용자가 쓸 수 있는 source root
-- ---------------------------------------------------------------------------
-- source_roots 행은 바꿀 수 없으므로(0002 트리거) 열림 여부를 별도 행으로 둔다. 행이 있으면 사용자별
-- source_root_grants 행과 같은 효과다(등록, claim의 재확인, 대기 사유). 지우면 닫힌다.

CREATE TABLE source_root_everyone (
    source_root_id uuid        PRIMARY KEY REFERENCES source_roots (id) ON DELETE RESTRICT,
    created_at     timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- ---------------------------------------------------------------------------
-- service_settings: 관리자가 정하는 서비스 전체 설정(행 하나)
-- ---------------------------------------------------------------------------
-- others_model_review: 관리자가 아닌 사용자가 소유한 프로젝트의 소스를 모델에 보낼 수 있다(기본 켬).
-- others_build_scripts: 관리자가 아닌 사용자가 소유한 프로젝트의 빌드 스크립트를 서비스 계정으로
--   실행할 수 있다(기본 끔). 관리자 소유 프로젝트는 두 값과 무관하다. claim이 job의 분석 선택에 적용한다.

CREATE TABLE service_settings (
    id                   smallint    PRIMARY KEY CHECK (id = 1),
    others_model_review  boolean     NOT NULL DEFAULT true,
    others_build_scripts boolean     NOT NULL DEFAULT false,
    updated_at           timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_by           uuid        NULL REFERENCES users (id) ON DELETE RESTRICT
);

INSERT INTO service_settings (id) VALUES (1);

-- ---------------------------------------------------------------------------
-- 감사 action과 대상(0013 목록에 더한다)
-- ---------------------------------------------------------------------------
-- source_root.everyone_set: 관리자가 source root를 모든 사용자에게 열거나 닫았다(detail.everyone).
-- service.settings_set: 관리자가 서비스 설정을 바꿨다(detail에 바뀐 값).
-- account.github_token_set / account.github_token_removed: 사용자가 자기 GitHub token을 저장하거나
--   지웠다. detail에는 token을 넣지 않는다(계정 이름과 host만).

ALTER TABLE audit_events DROP CONSTRAINT audit_events_action_check;

ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check CHECK (action IN (
    'auth.login_succeeded', 'auth.login_failed', 'auth.login_throttled', 'auth.logout',
    'auth.password_changed', 'auth.password_change_failed', 'auth.password_set',
    'auth.password_reset', 'auth.login_link_issued',
    'user.created', 'user.disabled', 'user.admin_granted', 'user.admin_revoked',
    'source_root.added', 'source_root.granted', 'source_root.grant_revoked', 'source_root.everyone_set', 'provider.added',
    'project.registered', 'project.revised', 'project.member_set', 'project.member_removed',
    'run.requested', 'run.cancel_requested', 'run.recovery_confirmed', 'report.exported', 'finding_review.recorded',
    'schedule.created', 'schedule.updated', 'schedule.deleted',
    'node.enrollment_token_created', 'node.enrollment_token_revoked', 'node.enrolled', 'node.revoked',
    'pool.policy_set', 'scheduler.limits_set', 'scheduler.artifact_quota_set',
    'storage.retention_applied',
    'service.settings_set', 'account.github_token_set', 'account.github_token_removed',
    'attempt.stale_rejected'));

ALTER TABLE audit_events DROP CONSTRAINT IF EXISTS audit_events_target_type_check;

ALTER TABLE audit_events ADD CONSTRAINT audit_events_target_type_check CHECK (target_type IS NULL OR target_type IN (
    'user', 'session', 'project', 'run', 'report', 'finding_review', 'schedule', 'source_root',
    'provider', 'node', 'enrollment_token', 'pool', 'scheduler', 'attempt', 'service'));

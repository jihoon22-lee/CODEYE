-- CODEYE: 원시 산출물 보존 정리 표시와 운영자의 recovery 확인.
--
-- 0001~0012와 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT,
-- 트리거는 NEW/OLD/TG_*만 사용한다. 기존 행은 바꾸지 않는다.

-- ---------------------------------------------------------------------------
-- artifacts.content_removed_at: 보존 정리(codeye cleanup)가 내용을 지운 시각
-- ---------------------------------------------------------------------------
-- 행(id·hash·크기·역할·저장 키)은 남는다. 보고서는 저장된 본문으로 계속 읽히고, 백업·산출물 상한 집계는
-- 이 값이 있는 행을 내용 없는 행으로 센다. 한 번 쓴 값은 바꾸거나 지울 수 없다.

ALTER TABLE artifacts
    ADD COLUMN content_removed_at timestamptz NULL,
    ADD CONSTRAINT artifacts_content_removed_finalized CHECK (content_removed_at IS NULL OR (finalized AND storage_key IS NOT NULL));

CREATE FUNCTION codeye_artifacts_removal_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.content_removed_at IS NOT NULL AND NEW.content_removed_at IS DISTINCT FROM OLD.content_removed_at THEN
        RAISE EXCEPTION 'artifacts.content_removed_at is set once' USING ERRCODE = 'CY002';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER artifacts_removal_guard
    BEFORE UPDATE ON artifacts
    FOR EACH ROW EXECUTE FUNCTION codeye_artifacts_removal_guard();

CREATE INDEX artifacts_content_present_idx ON artifacts (sha256) WHERE content_removed_at IS NULL;

-- ---------------------------------------------------------------------------
-- 감사 action(0011 목록에 더한다)
-- ---------------------------------------------------------------------------
-- run.recovery_confirmed: 사람(운영자 CLI 또는 실행 권한이 있는 사용자)이 recovery_required attempt의
-- 프로세스가 모두 끝났다고 확인했다. 그 확인이 정리의 근거다.
-- storage.retention_applied: codeye cleanup --apply가 원시 산출물 내용을 지웠다.

ALTER TABLE audit_events DROP CONSTRAINT audit_events_action_check;

ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check CHECK (action IN (
    'auth.login_succeeded', 'auth.login_failed', 'auth.login_throttled', 'auth.logout',
    'auth.password_changed', 'auth.password_change_failed', 'auth.password_set',
    'auth.password_reset', 'auth.login_link_issued',
    'user.created', 'user.disabled', 'user.admin_granted', 'user.admin_revoked',
    'source_root.added', 'source_root.granted', 'source_root.grant_revoked', 'provider.added',
    'project.registered', 'project.revised', 'project.member_set', 'project.member_removed',
    'run.requested', 'run.cancel_requested', 'run.recovery_confirmed', 'report.exported', 'finding_review.recorded',
    'schedule.created', 'schedule.updated', 'schedule.deleted',
    'node.enrollment_token_created', 'node.enrollment_token_revoked', 'node.enrolled', 'node.revoked',
    'pool.policy_set', 'scheduler.limits_set', 'scheduler.artifact_quota_set',
    'storage.retention_applied',
    'attempt.stale_rejected'));

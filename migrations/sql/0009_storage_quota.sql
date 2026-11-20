-- CODEYE: 저장 영역 quota(docs/OPERATIONS.md 저장 영역과 상한).
--
-- 0001~0008과 같은 규칙: BEGIN/COMMIT 없음, 비한정 이름(current_schema), 외래키는 ON DELETE RESTRICT.
-- 기존 테이블에는 기본값이 있는 열만 더한다. 기존 행은 "한도 없음·초과 없음"으로 읽힌다.
--
--   * nodes.storage_quota_exceeded: 노드가 마지막 heartbeat에서 보고한 한도 도달 영역
--     (snapshot·workspace·cache·logs). 비어 있지 않으면 claim은 그 노드에 새 attempt를 만들지 않고,
--     대기 run의 사유는 storage_quota_exceeded다. 실행 중 attempt와 그 산출물은 건드리지 않는다.
--   * scheduler_limits.artifact_quota_bytes: controller artifact 저장 한도(NULL = 한도 없음).
--     artifacts 행의 서로 다른 sha256 크기 합이 한도에 도달하면 새 attempt를 만들지 않는다.

ALTER TABLE nodes
    ADD COLUMN storage_quota_exceeded text[] NOT NULL DEFAULT '{}'
        CHECK (storage_quota_exceeded <@ ARRAY['snapshot', 'workspace', 'cache', 'logs']::text[]);

ALTER TABLE scheduler_limits
    ADD COLUMN artifact_quota_bytes bigint NULL
        CHECK (artifact_quota_bytes IS NULL OR artifact_quota_bytes > 0);

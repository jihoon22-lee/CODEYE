import { useCallback, useEffect, useState } from 'react';
import { personName, type AuditFilter } from './accounts';
import type { Api } from './api';
import { AuditLog, type ActorOption } from './AuditLog';
import { ko as t } from './locales/ko';

// The project's audit log for its owner and managers (GET /projects/{id}/audit). The actor
// filter offers the project's members: the only users a manager can name.
export function ProjectAudit({ api, projectId, onForbidden }: { api: Api; projectId: string; onForbidden: () => void }) {
  const [actors, setActors] = useState<ActorOption[]>([]);
  useEffect(() => {
    const abort = new AbortController();
    api.listMembers(projectId, abort.signal).then(
      items => setActors(items.map(m => ({ id: m.user_id, label: personName(m) ?? m.user_id.slice(0, 8) }))),
      () => { /* the filter works without names; the log itself reports access errors */ },
    );
    return () => abort.abort();
  }, [api, projectId]);
  const load = useCallback((filter: AuditFilter, cursor: string | null, signal?: AbortSignal) => api.listProjectAudit(projectId, filter, cursor, signal), [api, projectId]);
  return <section className="panel" aria-labelledby="project-audit-title">
    <h2 id="project-audit-title">{t.accounts.audit.title}</h2>
    <AuditLog load={load} actors={actors} projectFilter={false} intro={t.accounts.audit.projectIntro} onForbidden={onForbidden} />
  </section>;
}

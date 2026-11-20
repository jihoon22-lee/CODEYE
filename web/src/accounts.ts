// Accounts, project members, administrator views and the audit log (docs/API.md "인증",
// "구성원", "계정과 관리자"; api/openapi.yaml SessionView, Member, UserView, UserWithLink,
// AuditPage). Anything that does not match the contract is an invalid response, never a guess:
// a missing role or admin flag must not turn into a permission.

export type ProjectRole = 'owner' | 'manager' | 'runner' | 'viewer';
export type MemberRole = Exclude<ProjectRole, 'owner'>;
export const PROJECT_ROLES: readonly ProjectRole[] = ['owner', 'manager', 'runner', 'viewer'];
export const MEMBER_ROLES: readonly MemberRole[] = ['manager', 'runner', 'viewer'];

export type SessionInfo = {
  user_id: string;
  csrf_token: string;
  expires_at: string;
  username: string | null;
  email?: string;
  display_name: string | null;
  is_admin: boolean;
  must_change_password: boolean;
  has_password: boolean;
};

export type Member = { user_id: string; username: string | null; email?: string; display_name: string | null; role: ProjectRole; disabled: boolean };

export type UserView = {
  id: string;
  username: string | null;
  email?: string;
  display_name: string | null;
  is_admin: boolean;
  must_change_password: boolean;
  has_password: boolean;
  disabled: boolean;
  disabled_at: string | null;
  created_at: string;
  last_login_at: string | null;
};
export type UserPage = { items: UserView[]; next_cursor: string | null };
// The one-time login link is answered once; callers keep it in component state only.
export type LoginLink = { url: string; expires_at: string };
export type UserWithLink = { user: UserView; login_link: LoginLink };
export type CreateUserBody = { username: string; display_name?: string; is_admin?: boolean };

export type SourceRootGrant = { user_id: string; username: string | null; granted_at: string };
// path is the folder on the server; everyone: every user may read it without a grant.
export type AdminSourceRoot = { id: string; label: string; path: string | null; everyone: boolean; created_at: string; grants: SourceRootGrant[] };
export type AdminProvider = { id: string; kind: string; host: string; api_url: string | null };

export type AuditEvent = {
  id: string;
  occurred_at: string;
  actor_kind: string;
  actor_user_id: string | null;
  actor_username: string | null;
  actor_node_id: string | null;
  action: string;
  target_type: string | null;
  target_id: string | null;
  project_id: string | null;
  request_id: string | null;
  detail: Record<string, unknown>;
};
export type AuditPage = { items: AuditEvent[]; next_cursor: string | null };
// Only set filters are sent: the server answers 400 to unknown or empty parameters.
export type AuditFilter = { since?: string; until?: string; actor?: string; action?: string; project?: string };

// The closed action list of api/openapi.yaml AuditAction, in its order.
export const AUDIT_ACTIONS = [
  'auth.login_succeeded', 'auth.login_failed', 'auth.login_throttled', 'auth.logout', 'auth.password_changed',
  'auth.password_change_failed', 'auth.password_set', 'auth.password_reset', 'auth.login_link_issued',
  'user.created', 'user.disabled', 'user.admin_granted', 'user.admin_revoked',
  'source_root.added', 'source_root.granted', 'source_root.grant_revoked', 'provider.added',
  'project.registered', 'project.revised', 'project.member_set', 'project.member_removed',
  'run.requested', 'run.cancel_requested', 'report.exported', 'finding_review.recorded',
  'schedule.created', 'schedule.updated', 'schedule.deleted',
  'node.enrollment_token_created', 'node.enrollment_token_revoked', 'node.enrolled', 'node.revoked',
  'pool.policy_set', 'scheduler.limits_set', 'scheduler.artifact_quota_set', 'attempt.stale_rejected',
  'source_root.everyone_set', 'service.settings_set', 'account.github_token_set', 'account.github_token_removed',
] as const;

export const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function auditQuery(filter: AuditFilter, cursor: string | null): string {
  const q = new URLSearchParams();
  for (const key of ['since', 'until', 'actor', 'action', 'project'] as const) {
    const value = filter[key];
    if (value) q.set(key, value);
  }
  if (cursor) q.set('cursor', cursor);
  const text = q.toString();
  return text === '' ? '' : `?${text}`;
}

// ---- validation ----

function rec(v: unknown): Record<string, unknown> {
  if (typeof v !== 'object' || v === null || Array.isArray(v)) throw new TypeError('object');
  return v as Record<string, unknown>;
}
function str(v: unknown): string {
  if (typeof v !== 'string' || v === '') throw new TypeError('string');
  return v;
}
function nullableStr(v: unknown): string | null {
  return v === null ? null : str(v);
}
function bool(v: unknown): boolean {
  if (typeof v !== 'boolean') throw new TypeError('boolean');
  return v;
}
function date(v: unknown): string {
  const s = str(v);
  if (Number.isNaN(Date.parse(s))) throw new TypeError('date');
  return s;
}
function nullableDate(v: unknown): string | null {
  return v === null ? null : date(v);
}
function list<T>(v: unknown, item: (x: unknown) => T): T[] {
  if (!Array.isArray(v)) throw new TypeError('array');
  return v.map(item);
}
function cursor(v: unknown): string | null {
  return v === null || v === undefined ? null : str(v);
}

export function parseRole(v: unknown): ProjectRole {
  if (typeof v !== 'string' || !(PROJECT_ROLES as readonly string[]).includes(v)) throw new TypeError('role');
  return v as ProjectRole;
}

export function parseSession(v: unknown): SessionInfo {
  const o = rec(v);
  return {
    user_id: str(o.user_id),
    csrf_token: str(o.csrf_token),
    expires_at: date(o.expires_at),
    username: nullableStr(o.username),
    ...(o.email === undefined ? {} : { email: str(o.email) }),
    display_name: nullableStr(o.display_name),
    is_admin: bool(o.is_admin),
    must_change_password: bool(o.must_change_password),
    has_password: bool(o.has_password),
  };
}

export function parseMember(v: unknown): Member {
  const o = rec(v);
  return { user_id: str(o.user_id), username: nullableStr(o.username), ...(o.email === undefined ? {} : { email: str(o.email) }),
    display_name: nullableStr(o.display_name), role: parseRole(o.role), disabled: bool(o.disabled) };
}
export const parseMembers = (v: unknown): Member[] => list(rec(v).items, parseMember);

export function parseUser(v: unknown): UserView {
  const o = rec(v);
  return {
    id: str(o.id),
    username: nullableStr(o.username),
    ...(o.email === undefined ? {} : { email: str(o.email) }),
    display_name: nullableStr(o.display_name),
    is_admin: bool(o.is_admin),
    must_change_password: bool(o.must_change_password),
    has_password: bool(o.has_password),
    disabled: bool(o.disabled),
    disabled_at: nullableDate(o.disabled_at),
    created_at: date(o.created_at),
    last_login_at: nullableDate(o.last_login_at),
  };
}
export function parseUserPage(v: unknown): UserPage {
  const o = rec(v);
  return { items: list(o.items, parseUser), next_cursor: cursor(o.next_cursor) };
}
export function parseUserWithLink(v: unknown): UserWithLink {
  const o = rec(v);
  const link = rec(o.login_link);
  const url = str(link.url);
  // The link must be this kind of link: a launch fragment on an http(s) origin.
  const parsed = new URL(url);
  if ((parsed.protocol !== 'https:' && parsed.protocol !== 'http:') || !parsed.hash.startsWith('#launch=')) throw new TypeError('login link');
  return { user: parseUser(o.user), login_link: { url, expires_at: date(link.expires_at) } };
}

export function parseAdminSourceRoot(item: unknown): AdminSourceRoot {
  const o = rec(item);
  return {
    id: str(o.id),
    label: str(o.label),
    // Older servers send neither: no path shown, not open to everyone.
    path: o.path == null ? null : str(o.path),
    everyone: o.everyone == null ? false : bool(o.everyone),
    created_at: date(o.created_at),
    grants: list(o.grants, g => {
      const x = rec(g);
      return { user_id: str(x.user_id), username: nullableStr(x.username), granted_at: date(x.granted_at) };
    }),
  };
}
export const parseAdminSourceRoots = (v: unknown): AdminSourceRoot[] => list(rec(v).items, parseAdminSourceRoot);
export function parseAdminProvider(item: unknown): AdminProvider {
  const o = rec(item);
  return { id: str(o.id), kind: str(o.kind), host: str(o.host), api_url: o.api_url === undefined ? null : nullableStr(o.api_url) };
}
export const parseAdminProviders = (v: unknown): AdminProvider[] => list(rec(v).items, parseAdminProvider);

function parseAuditEvent(v: unknown): AuditEvent {
  const o = rec(v);
  return {
    id: str(o.id),
    occurred_at: date(o.occurred_at),
    actor_kind: str(o.actor_kind),
    actor_user_id: nullableStr(o.actor_user_id),
    actor_username: nullableStr(o.actor_username),
    actor_node_id: nullableStr(o.actor_node_id),
    action: str(o.action),
    target_type: nullableStr(o.target_type),
    target_id: nullableStr(o.target_id),
    project_id: nullableStr(o.project_id),
    request_id: nullableStr(o.request_id),
    detail: o.detail === null || o.detail === undefined ? {} : rec(o.detail),
  };
}
export function parseAuditPage(v: unknown): AuditPage {
  const o = rec(v);
  return { items: list(o.items, parseAuditEvent), next_cursor: cursor(o.next_cursor) };
}

// "Dana (dana)", "dana", "Dana", or null for a user known only by ID.
export function personName(p: { username: string | null; email?: string; display_name: string | null }): string | null {
  const identifier = p.email ?? p.username;
  if (p.display_name && identifier && p.display_name !== identifier) return `${p.display_name} (${identifier})`;
  return p.display_name ?? identifier;
}

// A user's name for lists and pickers; the short ID when the user has no name.
export const userLabel = (u: { id: string; username: string | null; email?: string; display_name: string | null }) => personName(u) ?? u.id.slice(0, 8);

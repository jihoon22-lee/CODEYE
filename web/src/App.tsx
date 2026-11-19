import { useEffect, useState, useSyncExternalStore } from 'react';
import { AdminView } from './AdminView';
import type { Api } from './api';
import { DoctorView } from './DoctorView';
import type { Idempotency } from './idempotency';
import { ko as t } from './locales/ko';
import { PasswordForm } from './PasswordForm';
import { ProjectsView } from './ProjectsView';
import type { SessionController, SessionState } from './session';
import { SignInForm } from './SignInForm';

type View = 'projects' | 'doctor' | 'account' | 'admin';
type Props = { api: Api; controller: SessionController; idempotency: Idempotency };
type Authenticated = Extract<SessionState, { status: 'authenticated' }>;

export function App({ api, controller, idempotency }: Props) {
  const session = useSyncExternalStore(controller.subscribe, controller.getState);
  const [view, setView] = useState<View>('projects');
  const [passwordSet, setPasswordSet] = useState(false);
  useEffect(() => { void controller.start(); }, [controller]);
  const go = (next: View) => { setPasswordSet(false); setView(next); };

  const signedIn = session.status === 'authenticated' ? session : null;
  // A session that must change its password sees only that screen and logout: nothing else is
  // mounted, so no other request is made with it.
  const forced = signedIn?.mustChangePassword === true;
  // The admin area exists only for admins; a stale choice falls back to projects.
  const shown: View = forced ? 'account' : view === 'admin' && !signedIn?.isAdmin ? 'projects' : view === 'account' && !signedIn ? 'projects' : view;

  return <>
    <a className="skip" href="#main">{t.skipToMain}</a>
    <main>
      <header>
        <span className="brand">{t.appName}<span>{t.eyebrow}</span></span>
        {!forced && <nav aria-label={t.nav.label}>
          <NavButton current={shown} view="projects" onSelect={go}>{t.nav.projects}</NavButton>
          <NavButton current={shown} view="doctor" onSelect={go}>{t.nav.doctor}</NavButton>
          {signedIn?.isAdmin && <NavButton current={shown} view="admin" onSelect={go}>{t.accounts.nav.admin}</NavButton>}
        </nav>}
        <div className="session">
          <span className="stage">{t.stage}</span>
          {signedIn && <AccountMenu session={signedIn} forced={forced} current={shown} onAccount={() => go('account')} onLogout={() => void controller.logout()} />}
        </div>
      </header>
      {signedIn?.logoutFailed && <p role="alert" className="error">{t.session.logoutFailed}</p>}
      {passwordSet && !forced && shown === 'projects' && <p role="status">{t.accounts.password.done}</p>}
      <div id="main" tabIndex={-1}>
        {shown === 'doctor' ? <DoctorView />
          : !signedIn ? <ConnectionPanel session={session} controller={controller} />
          : shown === 'account' ? <>
            <PasswordForm key={signedIn.userId} controller={controller} user={signedIn} forced={forced} onDone={() => { if (forced) { setPasswordSet(true); setView('projects'); } }} />
            {!forced && <p><button type="button" onClick={() => go('projects')}>{t.accounts.password.back}</button></p>}
          </>
          : shown === 'admin' ? <AdminView api={api} controller={controller} me={signedIn.userId} />
          : <ProjectsView api={api} userId={signedIn.userId} idempotency={idempotency} />}
      </div>
    </main>
  </>;
}

function NavButton({ current, view, onSelect, children }: { current: View; view: View; onSelect: (v: View) => void; children: string }) {
  return <button type="button" aria-current={current === view ? 'page' : undefined} onClick={() => onSelect(view)}>{children}</button>;
}

function AccountMenu({ session, forced, current, onAccount, onLogout }: { session: Authenticated; forced: boolean; current: View; onAccount: () => void; onLogout: () => void }) {
  const m = t.accounts.menu;
  const name = session.displayName && session.username && session.displayName !== session.username
    ? `${session.displayName} (${session.username})`
    : session.displayName ?? session.username ?? m.unnamed(session.userId.slice(0, 8));
  return <div className="account" role="group" aria-label={m.label}>
    <span className="small">{name}</span>
    {session.isAdmin && <span className="badge role-badge">{m.admin}</span>}
    {!forced && <button type="button" aria-current={current === 'account' ? 'page' : undefined} onClick={onAccount}>{m.account}</button>}
    <button type="button" onClick={onLogout} disabled={session.loggingOut}>{session.loggingOut ? t.session.loggingOut : t.session.logout}</button>
  </div>;
}

function ConnectionPanel({ session, controller }: { session: SessionState; controller: SessionController }) {
  if (session.status === 'checking') return <section className="panel" role="status"><p>{t.session.checking}</p></section>;
  if (session.status === 'unavailable') {
    return <section className="panel" aria-labelledby="conn-title">
      <h1 id="conn-title">{t.session.unavailable.title}</h1>
      <p role="alert">{t.session.unavailable.body}</p>
      <p className="small">{t.session.unavailable.hint}</p>
      <button type="button" onClick={() => void controller.retry()}>{t.session.unavailable.retry}</button>
    </section>;
  }
  if (session.status === 'anonymous') {
    return <section className="panel" aria-labelledby="login-title">
      <h1 id="login-title">{t.session.anonymous.title}</h1>
      <p role={session.reason === 'launch_rejected' || session.reason === 'expired' ? 'alert' : undefined}>{t.session.anonymous[session.reason]}</p>
      <SignInForm controller={controller} />
    </section>;
  }
  return null;
}

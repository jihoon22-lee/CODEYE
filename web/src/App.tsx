import { useEffect, useState, useSyncExternalStore } from 'react';
import type { Api } from './api';
import { DoctorView } from './DoctorView';
import type { Idempotency } from './idempotency';
import { ko as t } from './locales/ko';
import { ProjectsView } from './ProjectsView';
import type { SessionController } from './session';

type View = 'projects' | 'doctor';
type Props = { api: Api; controller: SessionController; idempotency: Idempotency };

export function App({ api, controller, idempotency }: Props) {
  const session = useSyncExternalStore(controller.subscribe, controller.getState);
  const [view, setView] = useState<View>('projects');
  useEffect(() => { void controller.start(); }, [controller]);

  return <>
    <a className="skip" href="#main">{t.skipToMain}</a>
    <main>
      <header>
        <span className="brand">{t.appName}<span>{t.eyebrow}</span></span>
        <nav aria-label={t.nav.label}>
          <button type="button" aria-current={view === 'projects' ? 'page' : undefined} onClick={() => setView('projects')}>{t.nav.projects}</button>
          <button type="button" aria-current={view === 'doctor' ? 'page' : undefined} onClick={() => setView('doctor')}>{t.nav.doctor}</button>
        </nav>
        <div className="session">
          <span className="stage">{t.stage}</span>
          {session.status === 'authenticated' && <>
            <span className="small">{t.session.user(session.userId.slice(0, 8))}</span>
            <button type="button" onClick={() => void controller.logout()} disabled={session.loggingOut}>{session.loggingOut ? t.session.loggingOut : t.session.logout}</button>
          </>}
        </div>
      </header>
      {session.status === 'authenticated' && session.logoutFailed && <p role="alert" className="error">{t.session.logoutFailed}</p>}
      <div id="main" tabIndex={-1}>
        {view === 'doctor' ? <DoctorView />
          : session.status === 'authenticated' ? <ProjectsView api={api} userId={session.userId} idempotency={idempotency} />
          : <ConnectionPanel session={session} onRetry={() => void controller.retry()} />}
      </div>
    </main>
  </>;
}

function ConnectionPanel({ session, onRetry }: { session: ReturnType<SessionController['getState']>; onRetry: () => void }) {
  if (session.status === 'checking') return <section className="panel" role="status"><p>{t.session.checking}</p></section>;
  if (session.status === 'unavailable') {
    return <section className="panel" aria-labelledby="conn-title">
      <h1 id="conn-title">{t.session.unavailable.title}</h1>
      <p role="alert">{t.session.unavailable.body}</p>
      <p className="small">{t.session.unavailable.hint}</p>
      <button type="button" onClick={onRetry}>{t.session.unavailable.retry}</button>
    </section>;
  }
  if (session.status === 'anonymous') {
    return <section className="panel" aria-labelledby="login-title">
      <h1 id="login-title">{t.session.anonymous.title}</h1>
      <p role={session.reason === 'launch_rejected' || session.reason === 'expired' ? 'alert' : undefined}>{t.session.anonymous[session.reason]}</p>
    </section>;
  }
  return null;
}

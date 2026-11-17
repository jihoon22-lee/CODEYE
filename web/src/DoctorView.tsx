import { useRef, useState } from 'react';
import { parseReport, type Report } from './doctor';
import { ko as t } from './locales/ko';

// Alternate view: reads a doctor JSON file locally. Nothing is uploaded or stored.
export function DoctorView() {
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState(false);
  const importRevision = useRef(0);
  return <>
    <section><p className="eyebrow">NATIVE · SELF-HOSTED</p><h1>{t.title}</h1><p>{t.intro}</p><p className="notice">{t.limitation}</p></section>
    <section className="panel" aria-labelledby="diagnostic"><h2 id="diagnostic">{t.diagnostic}</h2><p>{t.commandHelp}</p><pre><code>codeye doctor --format json &gt; doctor.json</code></pre>
      <label className="file">{t.open}<input type="file" accept=".json,application/json" onChange={async event => {
        const file = event.target.files?.[0]; if (!file) return;
        const revision = ++importRevision.current;
        const input = event.currentTarget;
        input.value = '';
        setReport(null); setError(false);
        try {
          if (file.size > 1048576) throw new Error('size');
          const result = parseReport(await file.text());
          if (revision === importRevision.current) setReport(result);
        } catch { if (revision === importRevision.current) setError(true); }
      }} /></label><p className="small">{t.privacy}</p>
      {error && <p role="alert" className="error">{t.invalid}</p>}
      <div aria-live="polite">{report ? <>
        <h3>{t.device}</h3><p>{report.host.os} / {report.host.arch} · {report.host.logical_cpus} CPUs</p>
        <div className="table"><table><caption>{t.checks}</caption><thead><tr>{t.columns.map(c => <th key={c} scope="col">{c}</th>)}</tr></thead><tbody>{report.checks.map(c => <tr key={c.id}><th scope="row">{c.id}</th><td>{t.statuses[c.status]}</td><td>{c.reason}</td><td>{c.version ?? '—'}</td></tr>)}</tbody></table></div>
      </> : <p>{t.empty}</p>}</div>
    </section>
    <section><h2>{t.next}</h2><ol>{t.steps.map(step => <li key={step}>{step}</li>)}</ol></section>
  </>;
}

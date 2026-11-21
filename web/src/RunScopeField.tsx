import { useId } from 'react';
import type { ManualRunBody, Source } from './api';
import { ko } from './locales/ko';

const t = ko.runs.pathScope;
const maxPaths = 100;
const maxPathBytes = 4096;
const utf8 = new TextEncoder();

export function requestedRunScope(value: string): ManualRunBody['scope'] | null {
  const input = value.split(/\r?\n/).map(line => line.trim()).filter(Boolean);
  if (input.length === 0) return { kind: 'full' };
  const paths = [...new Set(input.map(path => path.replace(/^(?:\.\/)+/, '').replace(/\/+$/, '')))].sort();
  if (paths.length > maxPaths || paths.some(path => /[:\\\u0000-\u001f\u007f]/.test(path) ||
    utf8.encode(path).length > maxPathBytes || path.split('/').some(segment => !segment || segment === '.' || segment === '..'))) return null;
  return { kind: 'paths', paths };
}

export function RunScopeField({ value, onChange, scope, kind, disabled }: {
  value: string; onChange: (value: string) => void; scope: ManualRunBody['scope'] | null; kind: Source['kind']; disabled: boolean;
}) {
  const id = useId();
  return <div className="field">
    <label htmlFor={id}>{t.label}</label>
    <textarea id={id} rows={3} value={value} onChange={e => onChange(e.target.value)} disabled={disabled}
      spellCheck={false} aria-invalid={scope === null} aria-describedby={`${id}-hint${scope === null ? ` ${id}-invalid` : ''}`} />
    <small id={`${id}-hint`}>{t.help} {kind === 'git' ? t.git : t.local}</small>
    {scope === null && <small id={`${id}-invalid`} className="error" role="alert">{t.invalid}</small>}
    {scope?.kind === 'paths' && <ul className="plain small" aria-label={t.selected}>
      {scope.paths.map(path => <li key={path}><code>{path}</code></li>)}
    </ul>}
  </div>;
}

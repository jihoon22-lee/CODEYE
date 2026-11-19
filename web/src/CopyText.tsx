import { useEffect, useState } from 'react';
import { formatInstant, formatRelative } from './format';
import { ko as t } from './locales/ko';

// A command or path the user is told to use, with a button that copies it. Without a
// clipboard (an insecure origin, a denied permission) the text stays selectable.
export function CopyText({ text, label }: { text: string; label?: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  useEffect(() => {
    if (state === 'idle') return;
    const id = setTimeout(() => setState('idle'), 2500);
    return () => clearTimeout(id);
  }, [state]);
  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setState('copied');
    } catch {
      setState('failed');
    }
  }
  return <span className="copy">
    {label && <span className="small">{label}</span>}
    <code>{text}</code>
    <button type="button" onClick={() => void copy()} aria-label={t.copy.labelFor(text)}>{state === 'copied' ? t.copy.done : t.copy.button}</button>
    <span role="status" className="visually-hidden">{state === 'copied' ? t.copy.done : state === 'failed' ? t.copy.failed : ''}</span>
  </span>;
}

// A time as "3분 전" with the exact local time on hover and for assistive technology.
export function Time({ iso, now }: { iso: string; now?: Date }) {
  const exact = formatInstant(iso);
  return <time dateTime={iso} title={exact}>{formatRelative(iso, now ?? new Date())}</time>;
}

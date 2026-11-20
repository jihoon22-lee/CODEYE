import { useEffect, useRef, useState } from 'react';
import { copyText } from './clipboard';
import { formatInstant, formatRelative } from './format';
import { ko as t } from './locales/ko';

// A command or path the user is told to use, with a button that copies it. Without a
// clipboard (a plain-HTTP origin, a denied permission) the text is selected for Ctrl+C.
export function CopyText({ text, label, id }: { text: string; label?: string; id?: string }) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const shown = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (state !== 'copied') return;
    const id = setTimeout(() => setState('idle'), 2500);
    return () => clearTimeout(id);
  }, [state]);
  async function copy() {
    setState(await copyText(text, shown.current) ? 'copied' : 'failed');
  }
  return <span className="copy">
    {label && <span className="small">{label}</span>}
    <code id={id} ref={shown}>{text}</code>
    <button type="button" onClick={() => void copy()} aria-label={t.copy.labelFor(text)}>{state === 'copied' ? t.copy.done : state === 'failed' ? t.copy.failedShort : t.copy.button}</button>
    <span role="status" className="visually-hidden">{state === 'copied' ? t.copy.done : state === 'failed' ? t.copy.failed : ''}</span>
  </span>;
}

// A time as "3분 전" with the exact local time on hover and for assistive technology.
// Without a fixed `now` it re-renders every 30 seconds so "방금" does not stay on screen.
export function Time({ iso, now }: { iso: string; now?: Date }) {
  const [, setTick] = useState(0);
  useEffect(() => {
    if (now) return;
    const id = setInterval(() => setTick(n => n + 1), 30_000);
    return () => clearInterval(id);
  }, [now]);
  const exact = formatInstant(iso);
  return <time dateTime={iso} title={exact}>{formatRelative(iso, now ?? new Date())}</time>;
}

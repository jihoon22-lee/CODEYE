import { codeText } from './locales/codeLabels';
import { ko } from './locales/ko';

// A reason code in words from the shared catalog, followed by the web-only hint of where to
// act when the code has one; null when the catalog has no text. The exports never carry the
// hint (docs/DECISIONS.md A72).
export function reasonWithHint(value: string): string | null {
  const text = codeText(value);
  if (text == null) return null;
  const code = value.split(':')[0];
  return Object.hasOwn(ko.stages.reasonHints, code) ? `${text} — ${ko.stages.reasonHints[code]}` : text;
}

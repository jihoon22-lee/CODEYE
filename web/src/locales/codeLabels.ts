import catalog from '../../../internal/report/reportui/code_labels_ko.json';

// The texts of reason, limitation and coverage counter codes. The Go renderers
// embed the same file (internal/report/code_labels.go), so the HTML, the
// Markdown and this screen read a code the same way.
export const codeLabels = catalog as {
  reasons: Record<string, string>;
  model_reasons: Record<string, string>;
  upstream: Record<string, string>;
  units: Record<string, string>;
};

// The text of a reason or limitation code ("code" or "code:detail"), or null
// when the code has none. Role-level model reasons come after the analyzer
// reasons, as in the Go renderers.
export function codeText(value: string): string | null {
  const [code, ...detail] = value.split(':');
  if (!code) return null;
  const text = Object.hasOwn(codeLabels.reasons, code) ? codeLabels.reasons[code]
    : Object.hasOwn(codeLabels.model_reasons, code) ? codeLabels.model_reasons[code] : upstreamText(code);
  if (text == null) return null;
  return detail.length > 0 ? `${text} (${detail.join(':')})` : text;
}

// upstream_<role>_<state> names the role of an earlier review stage and how it
// ended; it is built from parts, so it is not a catalog key of its own.
function upstreamText(code: string): string | null {
  if (!code.startsWith('upstream_')) return null;
  const rest = code.slice('upstream_'.length);
  for (const [state, text] of Object.entries(codeLabels.upstream)) {
    const suffix = '_' + state;
    if (rest.length > suffix.length && rest.endsWith(suffix)) return text.replace('{role}', rest.slice(0, -suffix.length));
  }
  return null;
}

// The text of a coverage counter key; a key with no counter text reads as its
// reason text, and an unknown key as itself.
export function unitText(key: string): string {
  if (Object.hasOwn(codeLabels.units, key)) return codeLabels.units[key];
  if (Object.hasOwn(codeLabels.reasons, key)) return codeLabels.reasons[key];
  return key;
}

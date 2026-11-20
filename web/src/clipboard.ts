// Copying text, also where the Clipboard API is missing: a page served over plain HTTP to
// another machine is not a secure context, so navigator.clipboard is undefined there. The
// fallback selects the shown text (an element, or a temporary read-only field) and asks the
// browser to copy the selection. When even that fails the text stays selected, so Ctrl+C copies it.
export async function copyText(text: string, shown?: HTMLElement | null): Promise<boolean> {
  const clip = typeof navigator === 'undefined' ? undefined : navigator.clipboard;
  if (clip && typeof clip.writeText === 'function') {
    try {
      await clip.writeText(text);
      return true;
    } catch { /* denied: try the selection below */ }
  }
  return copySelection(text, shown ?? null);
}

function copySelection(text: string, shown: HTMLElement | null): boolean {
  if (typeof document === 'undefined') return false;
  if (shown instanceof HTMLInputElement || shown instanceof HTMLTextAreaElement) {
    shown.focus();
    shown.select();
    return execCopy();
  }
  if (shown) {
    const range = document.createRange();
    range.selectNodeContents(shown);
    const sel = window.getSelection();
    sel?.removeAllRanges();
    sel?.addRange(range);
    return execCopy();
  }
  const field = document.createElement('textarea');
  field.value = text;
  field.readOnly = true;
  field.style.position = 'fixed';
  field.style.opacity = '0';
  document.body.appendChild(field);
  field.select();
  const ok = execCopy();
  field.remove();
  return ok;
}

function execCopy(): boolean {
  try {
    return typeof document.execCommand === 'function' && document.execCommand('copy');
  } catch {
    return false;
  }
}

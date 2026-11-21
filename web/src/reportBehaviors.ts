import type { Citation, RelatedWorkflow, Sections } from './parseReportSections';
import { primaryCitation, secondaryReviewScope } from './reportGroups';
const purposeHeading = /^(overview|introduction|about(?: .+)?|what is .+|welcome to .+|개요|소개)[.!]?$/i;
const usageHeading = /^(usage|features|quick start|quickstart|examples|how to use(?: .+)?|why .+|사용법|사용 예|주요 기능|기능|시작하기)$/i;
const operationalHeading = /^(install|installation|building|configuration|parameters|deployment|requirements|environment variables|command-line flags|run .+|설치|환경 변수|설정|배포|요구사항)$/i;
const usageFile = /(?:^|\/)(readme(?:\.(?:md|markdown|rst))?|usage\.(?:md|markdown|rst)|user[-_ ]guide\.(?:md|markdown|rst))$/i;
const operationalClaim = /\b(installation|deployment|cmake)\b|(?:설치|빌드|배포|환경 변수)|(?:경로|포트).*(?:기본값|기본으로|생략)/i;

function focusEvidence(citation: Citation, start: number, end: number, text: string): Citation {
  return { ...citation, line: start, end, excerpt: { start, end, text } };
}

function usageDocument(citation: Citation) {
  if (!citation.excerpt || !usageFile.test(citation.path)) return;
  let description: Citation | undefined;
  let usage = false;
  let purpose = false;
  let operational = false;
  let inPurpose = citation.excerpt.start <= 3;
  let fenced = false;
  const lines = citation.excerpt.text.split('\n');
  for (const [i, raw] of lines.entries()) {
    const line = raw.trim();
    if (line.startsWith('```') || line.startsWith('~~~')) {
      fenced = !fenced;
      continue;
    }
    if (fenced) continue;
    if (line.startsWith('##')) {
      const heading = line.replace(/^#+|#+$/g, '').trim();
      inPurpose = purposeHeading.test(heading);
      purpose ||= inPurpose;
      usage ||= usageHeading.test(heading);
      operational ||= operationalHeading.test(heading);
      continue;
    }
    const n = citation.excerpt.start + i;
    if (inPurpose && !description && n >= citation.line && n <= citation.end &&
      [...line].length <= 160 && /[.!?。！？]$/.test(line) &&
      !/[`\[\]<>"\\]|:\/\/|^[#\-+*!]/.test(line)) {
      description = focusEvidence(citation, n, n, raw);
    }
  }
  return { citation, description, usage: usage || (!operational && (purpose || Boolean(description))), operational };
}

const responseCue = /\b(?:Status[A-Z]\w*|HTTP_[A-Z_]+|status_code)\b|\b(?:status|sendStatus|writeHead)\([2-5]\d\d\b/;
const exitCue = /^\s*(?:return\b|throw\b|raise\b)/;
const branchCue = /^\s*(?:if\b|else if\b|except\b|catch\b|}\s*else if\b)/;
const typedConditionCue = /\.\([\w.]+\)|\b(?:errors\.Is|instanceof|isinstance|except|catch)\b/;
const implementationOutcome = /^\s*(?:return|throw|raise)(?:\s+[^\s;/#}]|\()/;
const implementationBranch = /^\s*(?:if|elif|for|while|switch|match|except|catch)(?:\s+[\w(]|\()/;
const implementationUpdate = /^\s*[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?\s*(?:\+=|-=|\*=|\/=|:=)\s*\S/;
const statusRank: Record<string, number> = { mismatch: 0, partial: 1, match: 2 };
export const maxProjectBehaviorRows = 4;

function hasImplementation(citation: Citation) {
  const excerpt = citation.excerpt;
  return excerpt?.text.split('\n').some((line, offset) => {
    const n = excerpt.start + offset;
    return n >= citation.line && n <= citation.end &&
      (implementationOutcome.test(line) || implementationBranch.test(line) || implementationUpdate.test(line));
  }) ?? false;
}

// Keep the preview as a verbatim window. Lexical cues choose a smaller range
// without interpreting the handler or inferring the comparison's verdict.
function codePreview(citation: Citation) {
  const excerpt = citation.excerpt;
  if (!excerpt) return;
  let best: { source: Citation; rank: number } | undefined;
  const lines = excerpt.text.split('\n');
  for (const [i, line] of lines.entries()) {
    let rank = responseCue.test(line) ? 2 : exitCue.test(line) && line.trim().split(/\s+/).length > 1 ? 1 : 0;
    if (rank === 0) continue;
    let start = i;
    let end = i;
    if (i > 0 && branchCue.test(lines[i - 1])) {
      start = i - 1;
      rank += typedConditionCue.test(lines[start]) ? 2 : 1;
      if (i > 1 && lines[i - 2].includes('=')) start--;
    }
    if (start === i) continue;
    if (i + 1 < lines.length && exitCue.test(lines[i + 1])) end++;
    const startLine = excerpt.start + start;
    const endLine = excerpt.start + end;
    if (rank <= (best?.rank ?? 0) || startLine < citation.line || endLine > citation.end) continue;
    const text = lines.slice(start, end + 1).join('\n');
    if ([...text].length > 290) continue;
    best = { source: focusEvidence(citation, startLine, endLine, text), rank };
  }
  return best;
}

export function projectBehaviors(documents: Sections['documents'], includesCode: boolean) {
  const rows: {
    index: number; statement: string; status: string; explanation: string; additional?: string; document: Citation;
    code?: Citation; preview?: Citation; workflow?: RelatedWorkflow; implementation: boolean;
  }[] = [];
  let description: Citation | undefined;
  if (!includesCode) return { rows, description };
  const usage = new Map<string, NonNullable<ReturnType<typeof usageDocument>>>();
  for (const citation of documents.citations) {
    if (!primaryCitation(citation, 'document')) continue;
    const document = usageDocument(citation);
    if (!document) continue;
    if (document.description && /^readme/i.test(citation.path) && !citation.path.includes('/') &&
      (!description || document.description.line < description.line)) {
      description = document.description;
    }
    if (document.usage) usage.set(citation.id, document);
  }
  const seen = new Set<string>();
  const primaryCode = documents.citations.filter(citation => primaryCitation(citation, 'code'));
  for (const [index, comparison] of documents.comparisons.entries()) {
    const key = comparison.statement.trim().split(/\s+/).join(' ').toLowerCase().replace(/[.。!?！？]+$/, '').trimEnd();
    if (!key || seen.has(key) || secondaryReviewScope(comparison.scope)) continue;
    const document = comparison.documentRefs.map(id => usage.get(id)).find(d => d && !(d.operational && operationalClaim.test(comparison.statement)))?.citation
      ?? (comparison.workflow && documents.citations.find(c => comparison.documentRefs.includes(c.id) && primaryCitation(c, 'document')));
    if (!document) continue;
    const code = primaryCode.filter(citation => comparison.codeRefs.includes(citation.id));
    if (comparison.status !== 'unknown' && code.length === 0) continue;
    seen.add(key);
    const extra = comparison.additional;
    const extraCode = extra?.reason.trim() ? primaryCode.filter(citation => extra.codeRefs.includes(citation.id)) : [];
    const sources = [...extraCode, ...code.filter(citation => !extraCode.includes(citation))];
    let preview: ReturnType<typeof codePreview>;
    for (const citation of primaryCode) {
      if (!sources.includes(citation)) continue;
      const visible = codePreview(citation);
      if (visible && visible.rank > (preview?.rank ?? 0)) preview = visible;
    }
    rows.push({ index, statement: comparison.statement, status: comparison.status, explanation: comparison.reason,
      additional: extraCode.length > 0 && extra!.reason.trim() !== comparison.reason.trim() ? extra!.reason : undefined,
      document, code: primaryCode.find(citation => sources.includes(citation)), preview: preview?.source,
      workflow: comparison.workflow, implementation: comparison.status === 'unknown' && sources.some(hasImplementation) });
  }
  rows.sort((a, b) => (statusRank[a.status] ?? 3) - (statusRank[b.status] ?? 3) || Number(b.implementation) - Number(a.implementation));
  return { rows, description };
}

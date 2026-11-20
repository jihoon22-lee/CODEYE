import type { Citation } from './reportSections';

export type DocumentExample = { call: string; output: string };
type Candidate = DocumentExample & { name: string; args: string[] };

const quoted = String.raw`(?:"[^"\\\x60\r\n\t]*"|'[^'\\\x60\r\n\t]*')`;
const number = String.raw`-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?`;
const name = '[A-Za-z_][A-Za-z0-9_]*';
const jsValue = `(?:${quoted}|${number}|true|false|null|undefined|NaN)`;
const pythonValue = `(?:${quoted}|${number}|True|False|None)`;
const pythonArg = `(?:${name} *= *)?${pythonValue}`;
const jsCall = new RegExp(String.raw`^((${name})\( *(${jsValue}(?: *, *${jsValue})*) *\));?$`);
const jsOutput = new RegExp(`^//=> *(${jsValue})$`);
const pythonAssert = new RegExp(String.raw`^assert ((${name})\( *(${pythonArg}(?: *, *${pythonArg})*) *\)) *== *(${pythonValue})$`);
const pythonImport = new RegExp(`^from ${name} import ${name}$`);
const argument = new RegExp(`(?:(${name}) *= *)?(${jsValue}|${pythonValue})`, 'g');
const argumentSeparator = /^(?:에|와|과|으로|로|,)? +$/;
const conditionEnd = /^(?:(?:을|를) (?:적용하면|입력받으면|전달하면)|인 경우|일 때)$/;
const fenceMarker = /^ {0,3}(`{3,}|~{3,})(.*)$/;

function exampleArguments(text: string, prose: boolean): string[] | undefined {
  const positional: string[] = [];
  const named = new Map<string, string>();
  let end = 0;
  for (const item of text.matchAll(argument)) {
    if (prose && (end === 0 ? item.index !== 0 : !argumentSeparator.test(text.slice(end, item.index)))) return;
    end = item.index + item[0].length;
    const [, option, value] = item;
    if (!option) {
      if (!prose && named.size > 0) return;
      positional.push(value);
      continue;
    }
    if (named.has(option)) return;
    named.set(option, `${option}=${value}`);
  }
  if (prose && !conditionEnd.test(text.slice(end))) return;
  return [...positional, ...[...named.values()].sort()];
}

function fenceCandidates(language: string, body: string[]): Candidate[] {
  if (['js', 'javascript'].includes(language) && body.length === 2) {
    const call = jsCall.exec(body[0]);
    const output = jsOutput.exec(body[1]);
    if (call && output) {
      const args = exampleArguments(call[3], false);
      if (args?.length) return [{ call: call[1], output: output[1], name: call[2], args }];
    }
  }
  if (['py', 'python'].includes(language)) {
    const candidates: Candidate[] = [];
    for (const line of body) {
      if (!line.trim() || line.startsWith('#') || pythonImport.test(line)) continue;
      // Any other setup, nesting or quoted block makes the context ambiguous.
      const assertion = pythonAssert.exec(line);
      const args = assertion && exampleArguments(assertion[3], false);
      if (!assertion || !args?.length) return [];
      candidates.push({ call: assertion[1], output: assertion[4], name: assertion[2], args });
    }
    return candidates;
  }
  return [];
}

function sameArguments(first: string[] | undefined, second: string[]) {
  return first?.length === second.length && first.every((arg, i) => arg === second[i]);
}

function matches(statement: string, example: Candidate) {
  const { call, output, name, args } = example;
  const endings = [` ${output}을 반환한다.`, ` ${output}를 반환한다.`];
  for (const subject of [call, '`' + call + '`']) {
    if (statement === `${subject} returns ${output}.` ||
      endings.some(end => ['은', '는'].some(topic => statement === subject + topic + end))) return true;
  }
  for (const subject of [name, '`' + name + '`']) {
    for (const start of [subject + '은 ', subject + '는 ']) {
      if (!statement.startsWith(start)) continue;
      if (endings.some(end => statement.endsWith(end) &&
        sameArguments(exampleArguments(statement.slice(start.length, -end.length), true), args))) return true;
    }
  }
  return false;
}

const encoder = new TextEncoder();

// Like the exports, use a complete fence from exactly the selected citation.
export function citedDocumentExample(statement: string, citation: Citation | undefined): DocumentExample | undefined {
  const excerpt = citation?.excerpt;
  if (!citation || citation.kind !== 'document' || !/\.(?:md|markdown)$/i.test(citation.path) ||
    !excerpt || excerpt.start !== citation.line || excerpt.end > citation.end || /\[REDACTED\]/i.test(excerpt.text)) return;
  const lines = excerpt.text.split('\n');
  if (excerpt.start > 1 && !/^#{1,6} /.test(lines[0])) return;
  const candidates: Candidate[] = [];
  let fence: { marker: string; language: string; start: number } | undefined;
  for (const [i, line] of lines.entries()) {
    const marker = fenceMarker.exec(line);
    if (!marker) continue;
    if (!fence) {
      fence = { marker: marker[1], language: marker[2].trim(), start: i };
      continue;
    }
    if (marker[1][0] === fence.marker[0] && marker[1].length >= fence.marker.length && !marker[2].trim()) {
      candidates.push(...fenceCandidates(fence.language, lines.slice(fence.start + 1, i)));
      fence = undefined;
    }
  }
  const selected = candidates.filter(candidate => matches(statement.trim(), candidate));
  if (selected.length !== 1) return;
  const [example] = selected;
  if (encoder.encode(example.call + example.output).length > 120 || candidates.some(other =>
    other.name === example.name && sameArguments(other.args, example.args) && other.output !== example.output)) return;
  return { call: example.call, output: example.output };
}

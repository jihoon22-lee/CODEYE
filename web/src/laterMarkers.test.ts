import { describe, expect, it } from 'vitest';
import sectioned from '../../examples/report.json';
import types from '../../internal/report/types.go?raw';
import record from '../../internal/worker/review/record/record.go?raw';
import { LATER_MARKERS, parseAnalysisReport, withoutLaterMarkers } from './analysisReport';
import { findingHeldBack, projectReadings } from './reportGroups';

// A marker string means something only from the builder that writes it; in an
// older revision it is model text, as the Go reader treats it
// (withoutLaterMarkers in internal/report/validate.go).
function load(builder: string, edit: (findings: Record<string, any>[]) => void) {
  const d = structuredClone(sectioned) as Record<string, any>;
  d.demo = false;
  d.builder.version = builder;
  edit(d.findings);
  return parseAnalysisReport(d, d.run_id);
}

let heldID = '';
const supportedIndex = (findings: Record<string, any>[]) => findings.findIndex(f => f.validation === 'supported');

describe('markers of a later builder', () => {
  it('has the builders and strings of the Go constants', () => {
    const since = (name: string) => Number(new RegExp(`\\b${name}\\s*=\\s*(\\d+)`).exec(types)![1]);
    const code = (name: string) => new RegExp(`\\b${name}\\s*=\\s*"([a-z_]+)"`).exec(record)![1];
    expect(LATER_MARKERS).toEqual({
      [code('LimCandidateNotShipped')]: since('ShippedFocusSince'),
      [code('LimHypothesisNotShipped')]: since('HypothesisNotShippedSince'),
    });
  });

  it('takes the strings out of limitations only for an older builder', () => {
    const both = ['candidate_not_shipped', 'hypothesis_not_shipped', 'other'];
    expect(withoutLaterMarkers(both, '23')).toEqual(['other']);
    expect(withoutLaterMarkers(both, '24')).toEqual(['candidate_not_shipped', 'other']);
    expect(withoutLaterMarkers(both, '25')).toEqual(both);
    expect(withoutLaterMarkers(both, '026')).toEqual(['other']);
  });

  it('builder 23: both strings are model text and the finding is shown normally', () => {
    const report = load('23', findings => {
      findings[supportedIndex(findings)].limitations = ['candidate_not_shipped', 'hypothesis_not_shipped'];
    });
    const f = report.findings.find(x => x.validation === 'supported')!;
    expect(f.limitations).toEqual([]);
    expect(findingHeldBack(f)).toBe(false);
    expect(projectReadings(report, report.sections!).actions.map(a => a.finding.id)).toContain(f.id);
  });

  it('builder 24: the hypothesis string is model text, a real candidate marker holds back', () => {
    const report = load('24', findings => {
      const i = supportedIndex(findings);
      findings[i].limitations = ['hypothesis_not_shipped'];
      const other = findings.find((_, k) => k !== i)!;
      other.validation = 'unreviewed';
      other.limitations = ['candidate_not_shipped'];
      heldID = other.id;
    }) as ReturnType<typeof parseAnalysisReport>;
    const shown = report.findings.find(x => x.validation === 'supported')!;
    expect(shown.limitations).toEqual([]);
    expect(findingHeldBack(shown)).toBe(false);
    expect(projectReadings(report, report.sections!).actions.map(a => a.finding.id)).toContain(shown.id);
    expect(findingHeldBack(report.findings.find(x => x.id === heldID)!)).toBe(true);
  });

  it('builder 25: both real markers hold findings back', () => {
    const report = load('25', findings => {
      const i = supportedIndex(findings);
      findings[i].limitations = ['hypothesis_not_shipped'];
      const other = findings.find((_, k) => k !== i)!;
      other.validation = 'unreviewed';
      other.limitations = ['candidate_not_shipped'];
      heldID = other.id;
    });
    const held = report.findings.find(x => x.limitations.includes('hypothesis_not_shipped'))!;
    expect(findingHeldBack(held)).toBe(true);
    expect(findingHeldBack(report.findings.find(x => x.id === heldID)!)).toBe(true);
    expect(projectReadings(report, report.sections!).actions.map(a => a.finding.id)).not.toContain(held.id);
  });
});

import type { AnalysisReport, Finding } from './analysisReport';
import type { Sections } from './parseReportSections';
import { duplicateReadingFindings, secondaryReviewFinding, sevRank } from './reportGroups';

// What a reader acts on first, chosen from the stored report only. The cards
// are the findings with a supported or reproduced validation on primary paths;
// a model sibling of a tool finding counts once. The overview row that says how
// many code actions there are counts the same findings.
const PUBLISHED_HIDDEN = new Set(['accepted_risk', 'false_positive', 'suppressed', 'fixed_by_user']);

export const MISMATCH_SHOWN = 5;
export const DIAGNOSTIC_PLACES = 25;
// Attention codes that say something was not analysed, and those that point at
// another section's result.
export const COVERAGE_ATTENTION = new Set(['required_tools_not_succeeded', 'analysis_gaps', 'review_scope_not_sent', 'review_primary_not_sent', 'model_review_not_performed']);
export const NOTICE_ATTENTION = new Set(['secrets_detected', 'environment_mismatch', 'document_conflicts']);

const bySeverity = (a: Finding, b: Finding) => sevRank(b.severity) - sevRank(a.severity);

export type ActionPlan = {
  actions: Finding[];
  candidates: Finding[];
  secondary: Finding[];
  mismatches: { index: number; comparison: Sections['documents']['comparisons'][number] }[];
  // Whether the review was accepted well enough to read an empty list as "found none".
  assessed: boolean;
};

export function actionPlan(report: AnalysisReport, sections: Sections, isDisposed: (f: Finding) => boolean = () => false): ActionPlan {
  const visible = (f: Finding) => !PUBLISHED_HIDDEN.has(f.disposition) && !isDisposed(f);
  const recorded = report.findings.filter(f => visible(f) && (f.validation === 'supported' || f.validation === 'reproduced'));
  const duplicates = duplicateReadingFindings(recorded, report.observations);
  const actionable = recorded.filter(f => !duplicates.has(f.id));
  const actions = actionable.filter(f => !secondaryReviewFinding(f))
    .sort((a, b) => bySeverity(a, b) || Number(b.validation === 'reproduced') - Number(a.validation === 'reproduced'));
  const reviewed = new Set(sections.codeReview.findings);
  const pending = report.findings.filter(f => reviewed.has(f.id) && visible(f) && (f.validation === 'inconclusive' || f.validation === 'unreviewed'));
  const pendingDuplicates = duplicateReadingFindings(pending, report.observations);
  const open = pending.filter(f => !pendingDuplicates.has(f.id));
  const candidates = open.filter(f => !secondaryReviewFinding(f)).sort(bySeverity);
  const secondary = [...actionable.filter(secondaryReviewFinding), ...open.filter(secondaryReviewFinding)].sort(bySeverity);
  const mismatches = sections.documents.comparisons
    .map((comparison, index) => ({ index, comparison }))
    .filter(row => row.comparison.status === 'mismatch' || row.comparison.status === 'partial')
    .sort((a, b) => Number(b.comparison.status === 'mismatch') - Number(a.comparison.status === 'mismatch'));
  const assessed = (report.aiReview === 'performed' || report.aiReview === 'partial') && report.status !== 'failed' && report.status !== 'cancelled';
  return { actions, candidates, secondary, mismatches, assessed };
}

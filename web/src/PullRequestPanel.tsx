import { useState } from 'react';
import type { Api } from './api';
import { describeError, type ErrorText } from './format';
import { ko } from './locales/ko';
import { shortSHA, type CommentPreview, type RunPullRequest } from './pullRequest';
import { ErrorNote } from './RunPanel';

const t = ko.pullRequest;

// The pull-request line of a run: which pull request and commits the run analyzes, the state
// of its result comment, and the preview of that comment (nothing is posted).
export function PullRequestPanel({ api, runId, pr, hasReport }: { api: Api; runId: string; pr: RunPullRequest; hasReport: boolean }) {
  const [preview, setPreview] = useState<CommentPreview | null>(null);
  const [error, setError] = useState<ErrorText | null>(null);
  const [loading, setLoading] = useState(false);
  const repo = pr.repository.replace(/^github\.com\//, '');
  const fork = pr.head_repository !== '' && pr.head_repository.toLowerCase() !== repo.toLowerCase();
  const c = pr.comment;
  const state = Object.hasOwn(t.commentStates, c.state) ? t.commentStates[c.state] : c.state;

  async function show() {
    setLoading(true);
    setError(null);
    try {
      setPreview(await api.previewPullRequestComment(runId));
    } catch (e) {
      setError(describeError(e));
    } finally {
      setLoading(false);
    }
  }

  return <section className="pull-request" aria-label={t.label}>
    <p>
      <a href={pr.url} target="_blank" rel="noreferrer noopener">{t.line(pr.number, repo)}</a>{' '}
      {t.commits(pr.head_ref, shortSHA(pr.head_sha), pr.base_ref, shortSHA(pr.base_sha))}
      {fork && <> {t.fork(pr.head_repository)}</>} {t.files(pr.changed_files)}
    </p>
    <p className="small">
      {t.comment}: {state}
      {c.reason && <> ({c.reason})</>}
      {c.url && <> · <a href={c.url} target="_blank" rel="noreferrer noopener">{t.openComment}</a></>}
    </p>
    {hasReport && <div className="actions">
      <button type="button" onClick={show} disabled={loading} aria-busy={loading}>{t.preview}</button>
    </div>}
    {error && <ErrorNote error={error} />}
    {preview && <div>
      <p className="small">{t.previewNote}{preview.head_moved && preview.current_head_sha && <> {t.headMoved(shortSHA(preview.current_head_sha))}</>}</p>
      <pre className="comment-preview" style={{ whiteSpace: 'pre-wrap' }} aria-label={t.preview}>{preview.body}</pre>
    </div>}
  </section>;
}

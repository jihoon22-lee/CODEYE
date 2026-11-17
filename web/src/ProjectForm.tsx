import { useId, useState, type FormEvent } from 'react';
import type { Api, Project, Provider, SourceRoot } from './api';
import { describeError, type ErrorText } from './format';
import { ko as t } from './locales/ko';
import { validateProjectForm, type FormErrors, type FormValues } from './projectForm';
import { ErrorNote } from './RunPanel';

const empty: FormValues = { name: '', kind: 'local', sourceRootId: '', relativePath: '', providerId: '', repositoryUrl: '', branch: '' };

type Props = { api: Api; roots: SourceRoot[]; providers: Provider[]; onCreated: (project: Project) => void };

export function ProjectForm({ api, roots, providers, onCreated }: Props) {
  const [values, setValues] = useState<FormValues>(empty);
  const [errors, setErrors] = useState<FormErrors>({});
  const [serverError, setServerError] = useState<ErrorText | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [done, setDone] = useState(false);
  const uid = useId();
  const id = (field: keyof FormValues) => `${uid}-${field}`;
  const set = <K extends keyof FormValues>(field: K, value: FormValues[K]) => {
    setValues(v => ({ ...v, [field]: value }));
    setErrors(e => ({ ...e, [field]: undefined }));
    setDone(false);
  };
  const p = t.projects;

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (submitting) return;
    const { body, errors: found } = validateProjectForm(values);
    setErrors(found);
    setServerError(null);
    setDone(false);
    if (!body) return;
    setSubmitting(true);
    try {
      const project = await api.createProject(body);
      setValues(empty);
      setDone(true);
      onCreated(project);
    } catch (e) {
      setServerError(describeError(e));
    } finally {
      setSubmitting(false);
    }
  }

  function field(name: keyof FormValues, label: string, input: (props: { id: string; 'aria-invalid': boolean; 'aria-describedby': string | undefined }) => React.ReactNode, help?: string) {
    const error = errors[name];
    const describedBy = [help ? `${id(name)}-help` : '', error ? `${id(name)}-error` : ''].filter(Boolean).join(' ') || undefined;
    return <div className="field">
      <label htmlFor={id(name)}>{label}</label>
      {input({ id: id(name), 'aria-invalid': Boolean(error), 'aria-describedby': describedBy })}
      {help && <small id={`${id(name)}-help`} className="small">{help}</small>}
      {error && <small id={`${id(name)}-error`} role="alert" className="error">{error}</small>}
    </div>;
  }

  return <form onSubmit={submit} noValidate aria-labelledby={`${uid}-title`}>
    <h3 id={`${uid}-title`}>{p.createTitle}</h3>
    {field('name', p.name, props => <input {...props} type="text" maxLength={200} autoComplete="off" value={values.name} onChange={e => set('name', e.target.value)} />, p.nameHelp)}
    <fieldset>
      <legend>{p.sourceKind}</legend>
      <label className="inline"><input type="radio" name={`${uid}-kind`} checked={values.kind === 'local'} onChange={() => set('kind', 'local')} /> {p.kindLocal}</label>
      <label className="inline"><input type="radio" name={`${uid}-kind`} checked={values.kind === 'git'} onChange={() => set('kind', 'git')} /> {p.kindGit}</label>
    </fieldset>
    {values.kind === 'local' ? <>
      {field('sourceRootId', p.sourceRoot, props => <select {...props} value={values.sourceRootId} onChange={e => set('sourceRootId', e.target.value)}>
        <option value="">{roots.length === 0 ? p.sourceRootNone : p.sourceRootPick}</option>
        {roots.map(r => <option key={r.id} value={r.id}>{r.label}</option>)}
      </select>)}
      {field('relativePath', p.relativePath, props => <input {...props} type="text" autoComplete="off" spellCheck={false} value={values.relativePath} onChange={e => set('relativePath', e.target.value)} />, p.relativePathHelp)}
    </> : <>
      {field('providerId', p.provider, props => <select {...props} value={values.providerId} onChange={e => set('providerId', e.target.value)}>
        <option value="">{providers.length === 0 ? p.providerNone : p.sourceRootPick}</option>
        {providers.map(x => <option key={x.id} value={x.id}>{`${x.host} (${x.kind})`}</option>)}
      </select>)}
      {field('repositoryUrl', p.repositoryUrl, props => <input {...props} type="text" inputMode="url" autoComplete="off" spellCheck={false} value={values.repositoryUrl} onChange={e => set('repositoryUrl', e.target.value)} />, p.repositoryHelp)}
      {field('branch', p.branch, props => <input {...props} type="text" autoComplete="off" spellCheck={false} value={values.branch} onChange={e => set('branch', e.target.value)} />)}
    </>}
    <button type="submit" className="primary" disabled={submitting} aria-busy={submitting}>{submitting ? p.submitting : p.submit}</button>
    {serverError && <ErrorNote error={serverError} />}
    {done && <p role="status">{p.created}</p>}
  </form>;
}

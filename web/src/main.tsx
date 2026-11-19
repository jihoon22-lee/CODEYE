import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { createApi } from './api';
import { readDeepLink } from './deepLink';
import { browserStorage, createIdempotency } from './idempotency';
import { createSessionController } from './session';
import './style.css';

const api = createApi();
const controller = createSessionController(api);
const idempotency = createIdempotency(browserStorage());
// Read before the session controller removes the launch token from the fragment.
const link = readDeepLink(window.location.hash);

createRoot(document.getElementById('root')!).render(<StrictMode><App api={api} controller={controller} idempotency={idempotency} link={link} /></StrictMode>);

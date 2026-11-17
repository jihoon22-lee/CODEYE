import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { App } from './App';
import { createApi } from './api';
import { browserStorage, createIdempotency } from './idempotency';
import { createSessionController } from './session';
import './style.css';

const api = createApi();
const controller = createSessionController(api);
const idempotency = createIdempotency(browserStorage());

createRoot(document.getElementById('root')!).render(<StrictMode><App api={api} controller={controller} idempotency={idempotency} /></StrictMode>);

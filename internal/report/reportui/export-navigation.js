(() => {
  const links = [...document.querySelectorAll('.report-rail a[href^="#"]')];
  const sections = [...document.querySelectorAll('.report-main > .export-section[id]')];
  const mobileCurrent = document.querySelector('.export-mobile-current');
  if (!links.length || !sections.length) return;

  let current;
  let frame;

  function select(section) {
    if (section === current) return;
    current = section;
    for (const link of links) {
      if (link.hash === `#${section.id}`) link.setAttribute('aria-current', 'location');
      else link.removeAttribute('aria-current');
    }
    if (mobileCurrent) mobileCurrent.textContent = section.querySelector('h2')?.textContent ?? '';
    document.body.classList.add('report-scroll-navigation');
  }

  function onScroll() {
    if (frame !== undefined) return;
    frame = requestAnimationFrame(() => {
      frame = undefined;
      const readingLine = Math.min(96, window.innerHeight / 5);
      let section = sections[0];
      for (const next of sections) {
        if (next.getBoundingClientRect().top > readingLine) break;
        section = next;
      }
      const page = document.documentElement;
      if (window.scrollY > 0 && window.scrollY + window.innerHeight >= page.scrollHeight - 1) {
        section = sections.at(-1);
      }
      select(section);
    });
  }

  function onFragment() {
    let id;
    try {
      id = decodeURIComponent(window.location.hash.slice(1));
    } catch {
      onScroll();
      return;
    }
    const target = document.getElementById(id);
    const section = target?.closest('.export-section');
    if (section) {
      for (let block = target.closest('details'); block; block = block.parentElement?.closest('details')) block.open = true;
      const result = target.closest('.report-result-details');
      if (result) {
        result.open = true;
        const workbench = result.closest('.report-export-workbench');
        if (workbench) inspect(workbench, true);
      }
      if (id === 'test-results') target.querySelector('details')?.setAttribute('open', '');
      if (frame !== undefined) cancelAnimationFrame(frame);
      frame = undefined;
      select(section);
    } else {
      onScroll();
    }
  }

  const workbenches = [...document.querySelectorAll('.report-export-workbench')];
  const wide = window.matchMedia('(min-width: 62rem)');
  const narrow = window.matchMedia('(max-width: 36rem)');
  const moved = new Map();
  let printed;

  function restore(workbench) {
    const shown = moved.get(workbench);
    if (!shown) return;
    shown.row.append(shown.body);
    shown.inspector.hidden = true;
    workbench.removeAttribute('data-inspecting');
    moved.delete(workbench);
  }

  function inspect(workbench, focus) {
    if (printed || !wide.matches && !narrow.matches) {
      restore(workbench);
      return;
    }
    const row = workbench.querySelector('details.report-result-details[open]');
    const shown = moved.get(workbench);
    if (shown && shown.row === row) {
      workbench.dataset.inspecting = narrow.matches ? 'compact' : 'true';
      return;
    }
    restore(workbench);
    const body = row?.querySelector('.export-workbench-body');
    const inspector = workbench.querySelector('.export-inspector');
    if (!body || !inspector) return;
    inspector.append(body);
    inspector.hidden = false;
    workbench.dataset.inspecting = narrow.matches ? 'compact' : 'true';
    moved.set(workbench, { row, body, inspector });
    if (focus) {
      const title = body.querySelector('h6');
      title?.focus({ preventScroll: true });
      const bounds = title?.getBoundingClientRect();
      if (bounds && (bounds.top < 0 || bounds.bottom > window.innerHeight)) title.scrollIntoView({ block: 'start' });
    }
  }

  document.addEventListener('toggle', event => {
    if (!event.target.matches('.report-export-workbench details.report-result-details')) return;
    inspect(event.target.closest('.report-export-workbench'), event.target.open);
  }, true);
  function updateWorkbenches() {
    for (const workbench of workbenches) inspect(workbench, false);
  }
  wide.addEventListener('change', updateWorkbenches);
  narrow.addEventListener('change', updateWorkbenches);


  const sourceOverrides = new WeakMap();
  function setWrap(preview, wrap) {
    preview.dataset.wrap = String(wrap);
    preview.querySelector('[data-report-source-wrap]')?.setAttribute('aria-pressed', String(wrap));
  }
  function updateSourceWrap() {
    for (const preview of document.querySelectorAll('.report-selected-source')) {
      setWrap(preview, sourceOverrides.get(preview) ?? narrow.matches);
    }
  }
  narrow.addEventListener('change', updateSourceWrap);
  updateSourceWrap();

  document.addEventListener('click', event => {
    const button = event.target.closest('button');
    if (!button) return;
    if (button.hasAttribute('data-report-inspector-close')) {
      const workbench = button.closest('.report-export-workbench');
      const row = moved.get(workbench)?.row ?? button.closest('details.report-result-details');
      if (row) {
        row.open = false;
        restore(workbench);
        row.querySelector('summary')?.focus();
      }
      return;
    }
    const preview = button.closest('.report-selected-source');
    if (!preview) return;
    if (button.hasAttribute('data-report-source-wrap')) {
      const wrap = preview.dataset.wrap !== 'true';
      sourceOverrides.set(preview, wrap);
      setWrap(preview, wrap);
    } else if (button.hasAttribute('data-report-source-mode')) {
      const { reportSourceMode: mode } = button.dataset;
      for (const option of preview.querySelectorAll('[data-report-source-mode]')) {
        option.setAttribute('aria-pressed', String(option.dataset.reportSourceMode === mode));
      }
      for (const window of preview.querySelectorAll('pre[data-mode]')) {
        window.hidden = window.dataset.mode !== mode;
      }
    }
  });

  window.addEventListener('beforeprint', () => {
    if (printed) return;
    for (const workbench of workbenches) restore(workbench);
    printed = [...document.querySelectorAll('main.report-main details')].map(details => ({
      details, open: details.open, name: details.getAttribute('name'),
    }));
    for (const { details } of printed) {
      details.removeAttribute('name');
      details.open = true;
    }
  });
  window.addEventListener('afterprint', () => {
    if (!printed) return;
    for (const { details, open, name } of printed) {
      details.open = open;
      if (name !== null) details.setAttribute('name', name);
    }
    printed = undefined;
    for (const workbench of workbenches) inspect(workbench, false);
  });

  window.addEventListener('scroll', onScroll, { passive: true });
  window.addEventListener('resize', onScroll);
  window.addEventListener('hashchange', onFragment);
  document.addEventListener('toggle', onScroll, true);
  onFragment();
  for (const workbench of workbenches) inspect(workbench, false);
})();

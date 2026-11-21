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
      const result = target.closest('.report-result-details');
      if (result) result.open = true;
      if (id === 'test-results') target.querySelector('details')?.setAttribute('open', '');
      if (frame !== undefined) cancelAnimationFrame(frame);
      frame = undefined;
      select(section);
    } else {
      onScroll();
    }
  }

  window.addEventListener('scroll', onScroll, { passive: true });
  window.addEventListener('resize', onScroll);
  window.addEventListener('hashchange', onFragment);
  document.addEventListener('toggle', onScroll, true);
  onFragment();
})();

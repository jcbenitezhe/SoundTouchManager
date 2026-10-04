// Home screen for the Android app. The tab strip that works on a desktop is
// small and hides half its entries off-screen on a phone, so the app opens on a
// grid of large, labelled buttons instead, each opening one view full-screen
// with a plain "Back" bar above it. The views themselves are the desktop ones;
// this only changes how you get to them.
//
// Navigation state lives in the browser history, so the phone's back button
// (which MainActivity maps to WebView.goBack) returns to the home screen.

// The first entry is the main one and is drawn larger. Podcasts is left out:
// it is a planned feature with nothing to use yet.
export const MOBILE_HOME_ITEMS = [
  { view: 'box', label: 'nav.music', hint: 'mobile.hint.box' },
  { view: 'recent', label: 'nav.recent', hint: 'mobile.hint.recent' },
  { view: 'spotify', label: 'nav.spotify', hint: 'mobile.hint.spotify' },
  { view: 'tunein', label: 'nav.tunein', hint: 'tunein.tileHint' },
  { view: 'library', label: 'nav.library', hint: 'mobile.hint.library' },
  { view: 'multiroom', label: 'nav.multiroom', hint: 'mobile.hint.multiroom' },
  { view: 'settings', label: 'nav.speakerSettings', hint: 'mobile.hint.settings' },
  { view: 'setup', label: 'nav.setupStick', hint: 'mobile.hint.setup' },
];

export const VIEW_EVENT = 'str:view';

// Lucide "coffee", ISC licensed, drawn like the tab icons.
const COFFEE_ICON = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M10 2v2"/><path d="M14 2v2"/><path d="M16 8a1 1 0 0 1 1 1v8a4 4 0 0 1-4 4H7a4 4 0 0 1-4-4V9a1 1 0 0 1 1-1h14a4 4 0 1 1 0 8h-1"/><path d="M6 2v2"/></svg>';

// The donate tile is not a view: it opens the donate dialog. On a phone the
// desktop's donate rail is always hidden and the footer link is small print.
// It sits right under the main tile; at the end of the grid it went unseen.
function donateTileMarkup(t, escapeHtml) {
  return `
    <button type="button" class="m-tile m-tile-donate" id="mDonate">
      <span class="m-tile-icon" aria-hidden="true">${COFFEE_ICON}</span>
      <span class="m-tile-text">
        <span class="m-tile-label">${escapeHtml(t('credits.donate'))}</span>
        <span class="m-tile-hint">${escapeHtml(t('footer.donateSlogan'))}</span>
      </span>
    </button>`;
}

// Lucide "circle-plus", ISC licensed.
const PLUS_ICON = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="10"/><path d="M8 12h8"/><path d="M12 8v8"/></svg>';

// Adding a missing station is not a view either: it opens its own dialog, and
// it works without a speaker, unlike the Music view that hides its search.
function addStationTileMarkup(t, escapeHtml) {
  return `
    <button type="button" class="m-tile" id="mAddStation">
      <span class="m-tile-icon" aria-hidden="true">${PLUS_ICON}</span>
      <span class="m-tile-text">
        <span class="m-tile-label">${escapeHtml(t('rbadd.tileLabel'))}</span>
        <span class="m-tile-hint">${escapeHtml(t('rbadd.tileHint'))}</span>
      </span>
    </button>`;
}

export function mobileHomeMarkup(t, escapeHtml, icon, withDonate = false, withAddStation = false) {
  const tiles = MOBILE_HOME_ITEMS.map((it, i) => `${i === 1 && withDonate ? donateTileMarkup(t, escapeHtml) : ''}${i === 2 && withAddStation ? addStationTileMarkup(t, escapeHtml) : ''}
    <button type="button" class="m-tile${i === 0 ? ' m-tile-main' : ''}" data-view="${escapeHtml(it.view)}">
      <span class="m-tile-icon" aria-hidden="true">${icon(it.view)}</span>
      <span class="m-tile-text">
        <span class="m-tile-label">${escapeHtml(t(it.label))}</span>
        <span class="m-tile-hint">${escapeHtml(t(it.hint))}</span>
      </span>
    </button>`).join('');
  return `<h2 class="m-home-title">${escapeHtml(t('mobile.homeTitle'))}</h2><div class="m-grid">${tiles}</div>`;
}

// mountMobileHome inserts the home screen and the back bar after the tab strip
// and takes over navigation. openView is the app's switchView; every call to
// it, from a tile or from any button inside a view, is announced through
// VIEW_EVENT and moves the app off the home screen.
export function mountMobileHome({ t, escapeHtml, icon, openView, onDonate, onAddStation, doc = document, win = window }) {
  const tabs = doc.querySelector('.tabs');
  if (!tabs) return null;
  const html = doc.documentElement;
  const labelOf = (view) => {
    const it = MOBILE_HOME_ITEMS.find((i) => i.view === view);
    return it ? t(it.label) : '';
  };

  const home = doc.createElement('nav');
  home.id = 'mHome';
  home.className = 'm-home';
  home.setAttribute('aria-label', t('mobile.home'));
  home.innerHTML = mobileHomeMarkup(t, escapeHtml, icon, typeof onDonate === 'function', typeof onAddStation === 'function');

  const bar = doc.createElement('div');
  bar.id = 'mBar';
  bar.className = 'm-bar';
  bar.innerHTML = `<button type="button" class="m-back" id="mBack"><span class="m-back-arrow" aria-hidden="true">&#8249;</span>${escapeHtml(t('mobile.back'))}</button><span class="m-bar-title" id="mBarTitle"></span>`;
  tabs.after(home, bar);
  const title = bar.querySelector('#mBarTitle');

  let atHome = true;
  let restoring = false;

  const goHome = () => {
    atHome = true;
    html.classList.add('m-at-home');
    win.scrollTo(0, 0);
  };
  const leaveHome = (view) => {
    title.textContent = labelOf(view);
    if (atHome) win.scrollTo(0, 0);
    atHome = false;
    html.classList.remove('m-at-home');
  };

  doc.addEventListener(VIEW_EVENT, (e) => {
    const view = e.detail;
    if (!restoring) {
      const st = { stmView: view };
      if (atHome) win.history.pushState(st, '');
      else win.history.replaceState(st, '');
    }
    leaveHome(view);
  });

  home.addEventListener('click', (e) => {
    const tile = e.target.closest('.m-tile');
    if (!tile) return;
    if (tile.id === 'mDonate') onDonate();
    else if (tile.id === 'mAddStation') onAddStation();
    else openView(tile.dataset.view);
  });

  bar.querySelector('#mBack').addEventListener('click', () => {
    if (win.history.state && win.history.state.stmView) win.history.back();
    else goHome();
  });

  win.addEventListener('popstate', (e) => {
    const view = e.state && e.state.stmView;
    if (!view) { goHome(); return; }
    restoring = true;
    try { openView(view); } finally { restoring = false; }
  });

  goHome();
  return { goHome };
}

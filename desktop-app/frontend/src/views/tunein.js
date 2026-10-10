// views/tunein.js — the "TuneIn" tab.
//
// A second, independent radio directory next to radio-browser. Browsing and
// searching run in the app's Go backend (TuneInBrowse / TuneInSearch); nothing
// here talks to TuneIn directly. A station is played and saved as its
// canonical reference "tunein:<id>": the speaker resolves the temporary stream
// URL itself at play time, so no stream URL or access key ever reaches this
// view. Shared TuneIn links (from the TuneIn app's Share sheet, or pasted into
// the search box) open the station they point at.

import { $, escapeHtml, escapeAttr, showError, showToast } from '../utils.js';
import { t } from '../i18n/index.js';
import { TuneInBrowse, TuneInSearch, TuneInStationInfo, TuneInResolveShare, TuneInListenURL, SetPreset, isMissingBinding } from '../api.js';
import { logoImgTag } from '../logos.js';
import { prefersNativeHls, previewCandidates } from '../rbadd.js';

// A silent WAV played inside the tap that starts listening: WebKit and the
// Android WebView only let an element play after a gesture, and the stream is
// resolved after an await, which ends that gesture.
const SILENT_WAV = 'data:audio/wav;base64,UklGRiQAAABXQVZFZm10IBAAAAABAAEARKwAAIhYAQACABAAZGF0YQAAAAA=';

let deps = {
  playStation: async () => {},
  showSlotPicker: () => {},
  currentBox: () => null,
  toggleFav: () => false,
  isFav: () => false,
  favStations: () => [],
  isPlaying: () => false,
  pause: async () => {},
};

// ui is the view's own navigation state: a stack of opened directory pages,
// the current page, and a station opened from a shared link.
const ui = {
  stack: [],
  page: null,
  query: '',
  shared: null,
  loading: false,
  rendered: false,
  // Search is the tab the view always opens on. Starred is a second pane.
  pane: 'search',
};

export function initTuneInView(injected) {
  deps = { ...deps, ...injected };
}

// looksLikeTuneInLink says whether text carries a TuneIn link or a bare
// station id, i.e. something to resolve rather than search for.
export function looksLikeTuneInLink(text) {
  const s = String(text || '').trim();
  if (/^[spt]\d{1,12}$/i.test(s)) return true;
  return /(^|\s|\/\/)(www\.)?(tunein\.com|tun\.in)\//i.test(s);
}

// guideIdOf returns the TuneIn id stored on a favorite ("tunein:s345726" -> "s345726").
export function guideIdOf(station) {
  const ref = String((station && (station.stationuuid || station.url)) || '');
  return ref.startsWith('tunein:') ? ref.slice('tunein:'.length) : '';
}

// tuneInFavorites keeps only TuneIn stations out of the shared favorites list.
export function tuneInFavorites(stations) {
  return (stations || []).filter(s => guideIdOf(s));
}

// stationFromInfo turns a TuneInStationInfo reply into the station object the
// shared play/preset/favorite helpers understand. The ref doubles as the
// station identity, so favorites and recents key off the TuneIn id.
export function stationFromInfo(info) {
  return {
    stationuuid: info.ref,
    name: info.name || '',
    url: info.ref,
    url_resolved: info.ref,
    codec: info.codec || '',
    bitrate: info.bitrate || 0,
    favicon: info.image || '',
    homepage: '',
    country: info.location || '',
    tags: info.genre || '',
  };
}

// formatDuration renders an episode length as h:mm:ss or m:ss.
export function formatDuration(sec) {
  const n = Math.max(0, Math.floor(Number(sec) || 0));
  if (!n) return '';
  const h = Math.floor(n / 3600);
  const m = Math.floor((n % 3600) / 60);
  const s = String(n % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`;
}

export function renderTuneIn() {
  const root = $('view-tunein');
  if (!root) return;
  if (!ui.rendered) {
    root.innerHTML = `
      <div class="tunein-view">
        <div class="tunein-head">
          <h2 class="tunein-title">TuneIn</h2>
          <p class="muted">${escapeHtml(t('tunein.lead'))}</p>
        </div>
        <div class="tunein-tabs" role="tablist">
          <button type="button" class="btn tunein-tab is-on" id="tuneinTabSearch" role="tab">${escapeHtml(t('tunein.search'))}</button>
          <button type="button" class="btn tunein-tab" id="tuneinTabStarred" role="tab">${escapeHtml(t('tunein.starred'))}</button>
        </div>
        <form class="tunein-search" id="tuneinSearchForm" role="search">
          <input type="search" id="tuneinQuery" class="input" autocomplete="off"
            placeholder="${escapeAttr(t('tunein.searchPlaceholder'))}" aria-label="${escapeAttr(t('tunein.searchPlaceholder'))}"/>
          <button type="submit" class="btn btn-primary">${escapeHtml(t('tunein.search'))}</button>
        </form>
        <p class="muted small tunein-share-hint">${escapeHtml(t('tunein.shareHint'))}</p>
        <div id="tuneinShared"></div>
        <div class="tunein-nav" id="tuneinNav"></div>
        <div class="tunein-list" id="tuneinList" aria-live="polite"></div>
        <p class="muted small tunein-attrib">${escapeHtml(t('tunein.attribution'))}</p>
      </div>`;
    $('tuneinSearchForm').onsubmit = (e) => {
      e.preventDefault();
      submitQuery($('tuneinQuery').value);
    };
    $('tuneinTabSearch').onclick = () => showSearch();
    $('tuneinTabStarred').onclick = () => showStarred();
    ui.rendered = true;
  }
  // Opening the view always lands on Search. Starred stays one tap away.
  ui.pane = 'search';
  showSearchChrome(true);
  markTabs();
  renderShared();
  if (!ui.page && !ui.loading) openRef('', t('tunein.home'), true);
  else renderPage();
}

// openTuneInShare is the entry point for a link shared from the TuneIn app
// (native share intent on Android/iOS, or a paste on desktop).
export async function openTuneInShare(text) {
  renderTuneIn();
  try {
    const info = await TuneInResolveShare(String(text || ''));
    await showShared(info);
  } catch (err) {
    ui.shared = null;
    renderShared();
    showError(friendly(err, t('tunein.errLink')));
  }
}

async function submitQuery(raw) {
  const q = String(raw || '').trim();
  if (!q) return;
  ui.pane = 'search';
  showSearchChrome(true);
  markTabs();
  if (looksLikeTuneInLink(q)) {
    await openTuneInShare(q);
    return;
  }
  ui.query = q;
  await load(() => TuneInSearch(q), t('tunein.resultsFor', { q }), false);
}

async function openRef(ref, title, reset) {
  await load(() => TuneInBrowse(ref), title, reset, ref);
}

async function load(fetcher, title, reset, ref = null) {
  ui.loading = true;
  const list = $('tuneinList');
  if (list) list.innerHTML = `<div class="muted">${escapeHtml(t('common.loading'))}</div>`;
  try {
    const page = await fetcher();
    if (reset) ui.stack = [];
    else if (ui.page) ui.stack.push(ui.page);
    ui.page = { title: title || page.title || 'TuneIn', items: page.items || [], ref };
  } catch (err) {
    if (list) list.innerHTML = `<div class="muted">${escapeHtml(friendly(err, t('tunein.errLoad')))}</div>`;
    ui.loading = false;
    return;
  }
  ui.loading = false;
  renderPage();
}

function goBack() {
  if (!ui.stack.length) return;
  ui.page = ui.stack.pop();
  renderPage();
}

function renderPage() {
  const nav = $('tuneinNav');
  const list = $('tuneinList');
  if (ui.pane === 'starred') return;
  if (!nav || !list || !ui.page) return;
  nav.innerHTML = `
    ${ui.stack.length ? `<button class="btn btn-secondary btn-mini" id="tuneinBack">&larr; ${escapeHtml(t('tunein.back'))}</button>` : ''}
    <span class="tunein-crumb">${escapeHtml(ui.page.title)}</span>
    ${ui.stack.length ? `<button class="btn btn-secondary btn-mini" id="tuneinHome">${escapeHtml(t('tunein.home'))}</button>` : ''}`;
  const back = $('tuneinBack');
  if (back) back.onclick = goBack;
  const home = $('tuneinHome');
  if (home) home.onclick = () => openRef('', t('tunein.home'), true);

  const flat = [];
  const html = ui.page.items.map(it => itemHtml(it, flat)).join('');
  list.innerHTML = html || `<div class="muted">${escapeHtml(t('tunein.empty'))}</div>`;
  wireItems(list, flat);
}

function itemHtml(it, flat) {
  if (it.kind === 'section') {
    return `<section class="tunein-section">
      <h3 class="tunein-section-title">${escapeHtml(it.text)}</h3>
      ${(it.children || []).map(c => itemHtml(c, flat)).join('')}
    </section>`;
  }
  if (it.kind === 'text') return `<div class="muted tunein-text">${escapeHtml(it.text)}</div>`;
  const i = flat.push(it) - 1;
  if (it.kind === 'link' || it.kind === 'show') {
    return `<button class="tunein-link" data-i="${i}">
      ${it.kind === 'show' && it.image ? `<span class="result-logo">${logoImgTag({ favicon: it.image, name: it.text }, 'fav')}</span>` : ''}
      <span class="tunein-link-text">${escapeHtml(it.text)}${it.subtext ? `<span class="muted small"> &middot; ${escapeHtml(it.subtext)}</span>` : ''}</span>
      <span class="tunein-chevron" aria-hidden="true">&rsaquo;</span>
    </button>`;
  }
  const meta = [];
  if (it.subtext) meta.push(escapeHtml(it.subtext));
  if (it.kind === 'station' && it.bitrate) meta.push(`${it.bitrate} kbit/s`);
  if (it.kind === 'episode' && it.durationSec) meta.push(escapeHtml(formatDuration(it.durationSec)));
  const favKey = { stationuuid: 'tunein:' + it.guideId, name: it.text, url: 'tunein:' + it.guideId };
  const fav = it.kind === 'station' && deps.isFav(favKey);
  return `<div class="result-row tunein-row" data-i="${i}">
    <div class="result-logo">${logoImgTag({ favicon: it.image || '', name: it.text }, 'fav')}</div>
    <div class="result-text">
      <div class="result-name"><span class="result-name-text">${escapeHtml(it.text)}</span></div>
      ${meta.length ? `<div class="result-meta">${meta.join(' &middot; ')}</div>` : ''}
    </div>
    <div class="result-actions">
      ${playButtonHtml('tunein:' + it.guideId, `data-i="${i}"`)}
      ${it.kind === 'station' ? `
      <button class="btn btn-mini tunein-pick" data-i="${i}" title="${escapeAttr(t('search.assignToKey'))}">+</button>
      <button class="btn btn-mini tunein-fav${fav ? ' is-fav' : ''}" data-i="${i}" title="${escapeAttr(fav ? t('search.removeFav') : t('search.addFav'))}">${fav ? '&#9733;' : '&#9734;'}</button>` : ''}
    </div>
  </div>`;
}

function wireItems(root, flat) {
  root.querySelectorAll('.tunein-link').forEach(b => {
    b.onclick = () => {
      const it = flat[+b.dataset.i];
      if (it && it.ref) openRef(it.ref, it.text, false);
    };
  });
  root.querySelectorAll('.tunein-play').forEach(b => {
    b.onclick = () => toggle(b.dataset.ref, () => withInfo(flat[+b.dataset.i], play));
  });
  root.querySelectorAll('.tunein-pick').forEach(b => {
    b.onclick = () => withInfo(flat[+b.dataset.i], assign);
  });
  root.querySelectorAll('.tunein-fav').forEach(b => {
    b.onclick = () => withInfo(flat[+b.dataset.i], (info) => {
      deps.toggleFav(stationFromInfo(info));
      if (ui.pane === 'starred') renderStarred();
      else {
        const now = deps.isFav(stationFromInfo(info));
        b.classList.toggle('is-fav', now);
        b.innerHTML = now ? '&#9733;' : '&#9734;';
        b.title = now ? t('search.removeFav') : t('search.addFav');
      }
    });
  });
}

function showSearchChrome(on) {
  const form = $('tuneinSearchForm');
  const hint = document.querySelector('#view-tunein .tunein-share-hint');
  const nav = $('tuneinNav');
  if (form) form.classList.toggle('hidden', !on);
  if (hint) hint.classList.toggle('hidden', !on);
  if (nav) nav.classList.toggle('hidden', !on);
}

function markTabs() {
  const search = $('tuneinTabSearch');
  const starred = $('tuneinTabStarred');
  if (search) {
    search.classList.toggle('is-on', ui.pane !== 'starred');
    search.setAttribute('aria-selected', ui.pane !== 'starred' ? 'true' : 'false');
  }
  if (starred) {
    starred.classList.toggle('is-on', ui.pane === 'starred');
    starred.setAttribute('aria-selected', ui.pane === 'starred' ? 'true' : 'false');
  }
}

function showSearch() {
  ui.pane = 'search';
  showSearchChrome(true);
  markTabs();
  if (ui.page) renderPage();
  else if (!ui.loading) openRef('', t('tunein.home'), true);
}

function showStarred() {
  ui.pane = 'starred';
  showSearchChrome(false);
  markTabs();
  renderStarred();
}

function favToItem(s) {
  return {
    kind: 'station',
    guideId: guideIdOf(s),
    text: s.name || guideIdOf(s),
    image: s.favicon || '',
    subtext: s.country || s.tags || '',
    bitrate: s.bitrate || 0,
  };
}

function renderStarred() {
  const list = $('tuneinList');
  if (!list) return;
  const favs = tuneInFavorites(deps.favStations());
  if (!favs.length) {
    list.innerHTML = `<div class="muted">${escapeHtml(t('tunein.starredEmpty'))}</div>`;
    return;
  }
  const flat = [];
  list.innerHTML = favs.map(s => itemHtml(favToItem(s), flat)).join('');
  wireItems(list, flat);
}

// withInfo checks an item with the backend (is there a stream the speaker can
// play right now?) before acting on it, so a dead station fails here with a
// clear message instead of on the speaker.
async function withInfo(it, fn) {
  if (!it || !it.guideId) return;
  try {
    const info = await TuneInStationInfo(it.guideId);
    if (!info.playable) {
      showToast(info.reason === 'show' ? t('tunein.openShow') : t('tunein.unavailable', { name: info.name || it.text }));
      return;
    }
    if (!info.image && it.image) info.image = it.image;
    await fn(info);
  } catch (err) {
    showError(friendly(err, t('tunein.errLoad')));
  }
}

// toggle is every play button's tap: pause what this button is playing (here
// or on the speaker), otherwise start it. Without a speaker the station plays
// on this device, so the audio element is unlocked while the tap still counts.
function toggle(ref, start) {
  if (isLocalActive(ref)) { pauseLocal(); return; }
  if (deps.isPlaying(ref)) { pause(); return; }
  if (!deps.currentBox()) {
    if (local.ref === ref && local.state === 'paused') { resumeLocal(); return; }
    primeLocalAudio();
  }
  start();
}

async function play(info) {
  if (!deps.currentBox()) { await listenHere(info); return; }
  stopLocal();
  await deps.playStation(stationFromInfo(info));
  syncTuneInPlayButtons();
}

async function pause() {
  await deps.pause();
  syncTuneInPlayButtons();
}

// local is the app's own player, used when no speaker is selected. The
// resolved URL lives only in the audio element (or hls.js) while it plays.
const local = { audio: null, hls: null, ref: '', id: '', name: '', state: '', retried: false, tries: [], attempt: 0 };

function isLocalActive(ref) {
  return !!ref && local.ref === ref && (local.state === 'loading' || local.state === 'playing');
}

function isActive(ref) {
  return isLocalActive(ref) || deps.isPlaying(ref);
}

function localAudio() {
  if (local.audio) return local.audio;
  const a = document.createElement('audio');
  a.preload = 'none';
  const real = () => !String(a.currentSrc || a.src || '').startsWith('data:');
  a.addEventListener('playing', () => { if (real()) setLocalState('playing'); });
  a.addEventListener('pause', () => { if (real() && local.state === 'playing') setLocalState('paused'); });
  a.addEventListener('ended', () => { if (real()) setLocalState(''); });
  a.addEventListener('error', () => { if (real() && !local.hls) localFailed(local.ref); });
  local.audio = a;
  return a;
}

function primeLocalAudio() {
  const a = localAudio();
  if (local.state === 'loading' || local.state === 'playing') return;
  a.src = SILENT_WAV;
  const p = a.play();
  if (p && p.catch) p.catch(() => {});
}

function setLocalState(st) {
  local.state = st;
  syncTuneInPlayButtons();
}

function teardownMedia() {
  if (local.hls) { local.hls.destroy(); local.hls = null; }
}

function stopLocal() {
  if (!local.audio) return;
  teardownMedia();
  local.audio.pause();
  local.audio.removeAttribute('src');
  try { local.audio.load(); } catch { /* nothing loaded */ }
  local.ref = '';
  setLocalState('');
}

function pauseLocal() {
  if (local.audio) local.audio.pause();
  setLocalState('paused');
}

function resumeLocal() {
  setLocalState('loading');
  playLocal(local.ref);
}

async function listenHere(info) {
  teardownMedia();
  Object.assign(local, { ref: info.ref, id: info.id, name: info.name || '', retried: false });
  setLocalState('loading');
  showToast(t('tunein.listeningHere', { name: local.name }));
  await startLocal();
}

async function startLocal() {
  const ref = local.ref;
  let st;
  try {
    st = await TuneInListenURL(local.id);
  } catch (err) {
    if (local.ref !== ref) return;
    setLocalState('');
    showError(friendly(err, t('tunein.errLoad')));
    return;
  }
  if (local.ref !== ref) return;
  const audio = localAudio();
  teardownMedia();
  if (st.hls && !prefersNativeHls(audio, window.navigator)) {
    try {
      const Hls = (await import('hls.js/light')).default;
      if (local.ref !== ref) return;
      if (Hls && Hls.isSupported()) {
        local.hls = new Hls({ enableWorker: false });
        local.hls.on(Hls.Events.ERROR, (_, d) => { if (d && d.fatal) localFailed(ref); });
        local.hls.loadSource(st.url);
        local.hls.attachMedia(audio);
        playLocal(ref);
        return;
      }
    } catch { /* fall back to the element's own HLS support */ }
  }
  local.tries = previewCandidates(st.url, window.location.protocol !== 'http:');
  local.attempt = 0;
  audio.src = local.tries[0];
  playLocal(ref);
}

function playLocal(ref) {
  const p = localAudio().play();
  if (p && p.catch) p.catch(() => localFailed(ref));
}

// localFailed tries the next URL candidate, then resolves the station once
// more (the temporary URL may have expired), then gives up.
function localFailed(ref) {
  if (!ref || local.ref !== ref || local.state === '' || local.state === 'paused') return;
  if (!local.hls && local.attempt + 1 < local.tries.length) {
    local.attempt++;
    local.audio.src = local.tries[local.attempt];
    playLocal(ref);
    return;
  }
  if (!local.retried) {
    local.retried = true;
    startLocal();
    return;
  }
  const name = local.name;
  stopLocal();
  showToast(t('tunein.unavailable', { name }));
}

function playButtonHtml(ref, attrs) {
  const playing = isActive(ref);
  return `<button class="btn btn-mini tunein-play${playing ? ' is-playing' : ''}" ${attrs} data-ref="${escapeAttr(ref)}" title="${escapeAttr(playing ? t('controls.pause') : t('search.playNow'))}">${playing ? '&#9208;&#xFE0E;' : '&#9654;&#xFE0E;'}</button>`;
}

// syncTuneInPlayButtons flips each play button between play and pause in
// place, so the status poll never re-renders the list just to move a glyph.
export function syncTuneInPlayButtons() {
  const view = $('view-tunein');
  if (!view) return;
  view.querySelectorAll('.tunein-play[data-ref]').forEach(btn => {
    const playing = isActive(btn.dataset.ref);
    if (btn.classList.contains('is-playing') === playing) return;
    btn.classList.toggle('is-playing', playing);
    btn.innerHTML = playing ? '&#9208;&#xFE0E;' : '&#9654;&#xFE0E;';
    btn.title = playing ? t('controls.pause') : t('search.playNow');
  });
}

function assign(info) {
  const box = deps.currentBox();
  if (!box) { showError(t('search.errSelectSpeaker')); return; }
  if (info.finite) { showToast(t('tunein.episodeNoPreset')); return; }
  deps.showSlotPicker({
    title: t('preset.assignStationTitle'),
    subtitle: info.name + (info.bitrate ? ` (${info.bitrate} kbit/s)` : ''),
    onPick: async (slot) => {
      await SetPreset(box.host, box.port, slot, info.name, info.ref, info.image || '', info.bitrate || 0, '', info.codec || '');
      showToast(t('preset.savedToKey', { n: slot, name: info.name }));
    },
  });
}

async function showShared(info) {
  ui.shared = info;
  renderShared();
  const card = $('tuneinShared');
  if (card && card.scrollIntoView) card.scrollIntoView({ block: 'nearest' });
}

function renderShared() {
  const el = $('tuneinShared');
  if (!el) return;
  const info = ui.shared;
  if (!info) { el.innerHTML = ''; return; }
  const status = info.playable ? '' : `<div class="muted small">${escapeHtml(info.reason === 'show' ? t('tunein.openShow') : t('tunein.unavailable', { name: info.name }))}</div>`;
  el.innerHTML = `<div class="tunein-shared card">
    <div class="muted small">${escapeHtml(t('tunein.sharedTitle'))}</div>
    <div class="result-row">
      <div class="result-logo">${logoImgTag({ favicon: info.image || '', name: info.name }, 'fav')}</div>
      <div class="result-text">
        <div class="result-name"><span class="result-name-text">${escapeHtml(info.name)}</span></div>
        <div class="result-meta">${[info.genre, info.location].filter(Boolean).map(escapeHtml).join(' &middot; ')}</div>
        ${status}
      </div>
      <div class="result-actions">
        ${info.playable ? `
        ${playButtonHtml(info.ref, 'id="tuneinSharedPlay"')}
        ${info.finite ? '' : `<button class="btn btn-primary btn-mini" id="tuneinSharedPick">${escapeHtml(t('tunein.saveToKey'))}</button>`}`
          : info.reason === 'show' ? `<button class="btn btn-mini" id="tuneinSharedOpen">${escapeHtml(t('tunein.openEpisodes'))}</button>` : ''}
        <button class="btn btn-secondary btn-mini" id="tuneinSharedClose" title="${escapeAttr(t('common.close'))}">&times;</button>
      </div>
    </div>
  </div>`;
  const p = $('tuneinSharedPlay');
  if (p) p.onclick = () => toggle(info.ref, () => play(info));
  const k = $('tuneinSharedPick');
  if (k) k.onclick = () => assign(info);
  const o = $('tuneinSharedOpen');
  if (o) o.onclick = () => openRef('Tune.ashx?c=pbrowse&id=' + encodeURIComponent(info.id), info.name, false);
  const c = $('tuneinSharedClose');
  if (c) c.onclick = () => { ui.shared = null; renderShared(); };
}

function friendly(err, fallback) {
  if (isMissingBinding(err)) return t('tunein.needsUpdate');
  const msg = String((err && err.message) || err || '');
  if (/invalid reference/i.test(msg)) return t('tunein.errLink');
  if (/unavailable|timeout|deadline/i.test(msg)) return t('tunein.errOffline');
  return fallback;
}

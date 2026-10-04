// "Add to Radio Browser": find a station in radio-browser, and when no working
// entry exists, prepare one and submit it through the API (POST /json/add)
// instead of radio-browser.info's web form. One dialog, four steps:
// search -> form -> review (exact fields, duplicates, stream check, explicit
// confirmation) -> done (UUID and radio-browser's own check, refreshable).
//
// Every safety check also runs on the backend at submit time
// (desktop-app/radio_submit.go); the review step only shows them.

export const STATUS_ICON = { working: '🟢', broken: '🔴', unknown: '🟡' };

// Fields in the order radio-browser.info/add shows them.
export const FORM_FIELDS = [
  { key: 'name', label: 'rbadd.field.name', required: true },
  { key: 'url', label: 'rbadd.field.url', required: true, type: 'url' },
  { key: 'homepage', label: 'rbadd.field.homepage', type: 'url' },
  { key: 'favicon', label: 'rbadd.field.favicon', type: 'url' },
  { key: 'countrycode', label: 'rbadd.field.countrycode', required: true, placeholder: 'MX', maxlength: 2 },
  { key: 'iso_3166_2', label: 'rbadd.field.iso31662', placeholder: 'MX-CMX', hint: 'rbadd.hint.iso31662' },
  { key: 'state', label: 'rbadd.field.state', placeholder: 'Ciudad de México' },
  { key: 'languagecodes', label: 'rbadd.field.languagecodes', placeholder: 'spa', hint: 'rbadd.hint.languagecodes' },
  { key: 'tags', label: 'rbadd.field.tags', placeholder: 'romantic,pop,music' },
];

// lastCheckAge renders radio-browser's "YYYY-MM-DD HH:MM:SS" (UTC) as a
// relative time, or '' for a station that was never checked.
export function lastCheckAge(lastchecktime, locale, now = Date.now()) {
  const s = String(lastchecktime || '').trim();
  if (!s || s.startsWith('0000')) return '';
  const ms = Date.parse(s.replace(' ', 'T') + (/[zZ]|[+-]\d\d:?\d\d$/.test(s) ? '' : 'Z'));
  if (Number.isNaN(ms)) return s;
  const secs = Math.round((ms - now) / 1000);
  const rtf = new Intl.RelativeTimeFormat(locale || 'en', { numeric: 'auto' });
  const abs = Math.abs(secs);
  if (abs < 60) return rtf.format(secs, 'second');
  if (abs < 3600) return rtf.format(Math.round(secs / 60), 'minute');
  if (abs < 86400) return rtf.format(Math.round(secs / 3600), 'hour');
  return rtf.format(Math.round(secs / 86400), 'day');
}

export function codecLine(codec, bitrate) {
  const parts = [];
  if (codec) parts.push(codec);
  if (bitrate) parts.push(`${bitrate} kbps`);
  return parts.join(' • ');
}

// hasWorkingEntry decides whether to offer "add a new station" up front.
export function hasWorkingEntry(list) {
  return (list || []).some((s) => s.status === 'working');
}

// draftFrom collects the form values, trimmed, upper/lower-casing the codes.
export function draftFrom(values) {
  const v = (k) => String(values[k] || '').trim();
  return {
    name: v('name'),
    url: v('url'),
    homepage: v('homepage'),
    favicon: v('favicon'),
    countrycode: v('countrycode').toUpperCase(),
    iso_3166_2: v('iso_3166_2').toUpperCase(),
    state: v('state'),
    languagecodes: v('languagecodes').toLowerCase(),
    language: '',
    tags: v('tags'),
  };
}

// suggestFromCheck fills empty form fields from the stream's ICY headers.
export function suggestFromCheck(values, check) {
  const out = { ...values };
  if (!check) return out;
  if (!out.name && check.icyName) out.name = check.icyName;
  if (!out.homepage && /^https?:\/\//i.test(check.icyUrl || '')) out.homepage = check.icyUrl;
  if (!out.tags && check.icyGenre) {
    out.tags = check.icyGenre.split(/[,/;]+/).map((s) => s.trim().toLowerCase()).filter(Boolean).join(',');
  }
  return out;
}

function setBusy(btn, on) {
  if (!btn) return;
  btn.disabled = on;
  btn.setAttribute('aria-busy', on ? 'true' : 'false');
  const sp = btn.querySelector('.btn-spinner');
  if (on && !sp) btn.insertAdjacentHTML('afterbegin', '<span class="btn-spinner" aria-hidden="true"></span>');
  if (!on && sp) sp.remove();
}

// searchWebURL opens in the system browser. A web search page is the only
// portable choice: there is no URL that means "the user's default engine".
export function searchWebURL(name, t = (k, p) => `${p.name} radio`) {
  return 'https://www.google.com/search?q=' + encodeURIComponent(t('rbadd.searchWebQuery', { name }));
}

// errorKey maps a failed call to an i18n key, or '' to show the raw message.
// fetch rejects with browser-specific wording when the backend is gone:
// Chromium "Failed to fetch", WebKit "Load failed", Firefox "NetworkError".
export function errorKey(e) {
  const msg = String((e && e.message) || e || '');
  if (/failed to fetch|load failed|networkerror|^5\d\d\b|ECONNREFUSED/i.test(msg)) return 'rbadd.err.network';
  if (msg.startsWith('STM_NOT_CONFIRMED')) return 'rbadd.err.notConfirmed';
  if (msg.startsWith('STM_PRECHECK_FAILED')) return 'rbadd.err.precheck';
  return '';
}

// previewSource picks what to play for a directory station (url_resolved) or a
// stream check (resolvedUrl): playlists like .pls do not play in <audio>.
export function previewSource(o) {
  const src = (o && (o.url_resolved || o.resolvedUrl || o.url)) || '';
  return /^https?:\/\//i.test(src) ? src : '';
}

// isHlsSource reports an HLS (.m3u8) stream, either from the URL or from what
// the directory or the stream check learned about it.
export function isHlsSource(o, src) {
  if (o && (o.hls === 1 || o.hls === true)) return true;
  if (o && /mpegurl/i.test(o.contentType || '')) return true;
  return /\.m3u8($|[?#])/i.test(src || '');
}

export function playControls(src, t, esc, hls = false) {
  if (!src) return '';
  return `<div class="rb-play" data-src="${esc(src)}"${hls ? ' data-hls="1"' : ''} data-state="stopped">
    <button type="button" class="btn btn-mini" data-act="play">▶ ${esc(t('rbadd.play.play'))}</button>
    <button type="button" class="btn btn-mini" data-act="pause" disabled>⏸ ${esc(t('rbadd.play.pause'))}</button>
    <button type="button" class="btn btn-mini" data-act="stop" disabled>⏹ ${esc(t('rbadd.play.stop'))}</button>
    <span class="rb-play-state" aria-live="polite"></span>
  </div>`;
}

// createPreview drives one shared audio element for every row of controls:
// starting a station stops the one before. It plays on this device, not on
// the speaker; the point is to hear that the station is the right one.
// HLS plays natively on Apple WebKit (nativeHls: the iOS and macOS apps),
// where hls.js would lose the tap's user activation across its async load
// and its segment fetches would hit App Transport Security. Elsewhere it goes
// through hls.js (loadHls) when Media Source Extensions exist: the Android
// WebView answers "maybe" to canPlayType for HLS yet fails on fMP4 segments.
export function prefersNativeHls(audio, nav) {
  if (!nav || !/Apple/.test(nav.vendor || '')) return false;
  return !!(audio.canPlayType && audio.canPlayType('application/vnd.apple.mpegurl'));
}

// previewCandidates lists the URLs to try for one stream. A page not served
// over http (the macOS app's wails:// origin) cannot load http audio: WebKit
// blocks it as mixed content and App Transport Security refuses it. So there
// the https twin goes first; elsewhere it is the fallback when http fails.
// Most stream CDNs answer on both.
export function previewCandidates(src, httpsFirst = false) {
  if (!/^http:\/\//i.test(src)) return [src];
  const secure = src.replace(/^http:/i, 'https:');
  return httpsFirst ? [secure, src] : [src, secure];
}

export function createPreview({ audio, t, loadHls, nativeHls = false, httpsFirst = false }) {
  let cur = null;
  let hls = null;
  let tries = [];
  let attempt = 0;
  const label = { loading: 'rbadd.play.loading', playing: 'rbadd.play.playing', paused: 'rbadd.play.paused', error: 'rbadd.play.error' };
  const show = (row, st) => {
    if (!row) return;
    row.dataset.state = st;
    const btn = (a) => row.querySelector(`[data-act="${a}"]`);
    btn('play').disabled = st === 'loading' || st === 'playing';
    btn('pause').disabled = st !== 'playing';
    btn('stop').disabled = st === 'stopped' || st === 'error';
    const s = row.querySelector('.rb-play-state');
    if (s) s.textContent = label[st] ? t(label[st]) : '';
  };
  const stop = () => {
    const row = cur;
    cur = null;
    if (hls) { hls.destroy(); hls = null; }
    audio.pause();
    audio.removeAttribute('src');
    try { audio.load(); } catch { /* nothing loaded */ }
    show(row, 'stopped');
  };
  audio.addEventListener('playing', () => show(cur, 'playing'));
  audio.addEventListener('waiting', () => show(cur, 'loading'));
  // The error event and the rejected play() both report one failed URL;
  // the attempt number keeps them from skipping a candidate.
  const fail = (row, i = attempt) => {
    if (cur !== row || i !== attempt) return;
    if (i + 1 < tries.length) { load(row, i + 1); return; }
    show(row, 'error');
  };
  audio.addEventListener('error', () => fail(cur));
  const start = (row, i = attempt) => {
    const p = audio.play();
    if (p && p.catch) p.catch(() => fail(row, i));
  };
  function load(row, i) {
    attempt = i;
    audio.src = tries[i];
    start(row, i);
  }
  const playNative = (row) => {
    tries = previewCandidates(row.dataset.src, httpsFirst);
    load(row, 0);
  };
  const attachHls = async (row) => {
    const Hls = await loadHls();
    if (cur !== row) return;
    if (!Hls || !Hls.isSupported()) { playNative(row); return; }
    tries = [row.dataset.src];
    attempt = 0;
    hls = new Hls({ enableWorker: false });
    hls.on(Hls.Events.ERROR, (_, d) => { if (d && d.fatal) fail(row); });
    hls.loadSource(row.dataset.src);
    hls.attachMedia(audio);
    start(row);
  };
  return {
    play(row) {
      if (row === cur) { show(row, 'loading'); start(row); return; }
      stop();
      cur = row;
      show(row, 'loading');
      if (row.dataset.hls === '1' && loadHls && !nativeHls) {
        attachHls(row).catch(() => { if (cur === row) playNative(row); });
        return;
      }
      playNative(row);
    },
    pause() {
      if (!cur) return;
      audio.pause();
      show(cur, 'paused');
    },
    stop,
  };
}

// countrySelect renders the search's country filter; countries is the Music
// search list ({cc, name}, "" first for every country).
export function countrySelect(countries, selected, esc, flag, label) {
  if (!countries.length) return '';
  const opts = countries.map((c) =>
    `<option value="${esc(c.cc)}"${c.cc === selected ? ' selected' : ''}>${flag(c.cc)}${esc(c.name)}</option>`).join('');
  return `<select id="rbCountry" aria-label="${esc(label)}" title="${esc(label)}">${opts}</select>`;
}

// openRadioAdd shows the dialog. deps: { api, t, escapeHtml, openURL, locale,
// doc, countries, country, optFlag }. api holds RadioLookupForAdd,
// RadioCheckStream, RadioPrecheckSubmit, RadioSubmitStation, RadioStationByUUID.
// country starts the search filter (the Music search's pinned country).
export function openRadioAdd(initialQuery, deps) {
  const { api, t, escapeHtml: esc, openURL, locale } = deps;
  const doc = deps.doc || document;
  const modal = doc.createElement('div');
  modal.className = 'modal rb-modal';
  modal.innerHTML = '<div class="modal-content rb-add" role="dialog" aria-modal="true"></div>';
  doc.body.appendChild(modal);
  const box = modal.firstElementChild;
  const audio = doc.createElement('audio');
  audio.preload = 'none';
  const loadHls = deps.loadHls || (() => import('hls.js/light').then((m) => m.default));
  const win = deps.win || doc.defaultView;
  const nativeHls = prefersNativeHls(audio, win && win.navigator);
  const httpsFirst = !!(win && win.location && win.location.protocol !== 'http:');
  const preview = createPreview({ audio, t, loadHls, nativeHls, httpsFirst });
  box.addEventListener('click', (e) => {
    const b = e.target.closest && e.target.closest('.rb-play [data-act]');
    if (!b) return;
    if (b.dataset.act === 'play') preview.play(b.closest('.rb-play'));
    else if (b.dataset.act === 'pause') preview.pause();
    else preview.stop();
  });
  // A history entry for the open dialog, so the phone's Back button closes it
  // (and stops the preview) instead of leaving the screen underneath.
  const hist = win && win.history;
  const onPop = () => close(true);
  if (hist) {
    hist.pushState({ rbadd: true }, '');
    win.addEventListener('popstate', onPop);
  }
  function close(fromPop) {
    preview.stop();
    modal.remove();
    if (!hist) return;
    win.removeEventListener('popstate', onPop);
    if (fromPop !== true && hist.state && hist.state.rbadd) hist.back();
  }
  modal.addEventListener('click', (e) => { if (e.target === modal) close(); });

  const state = { query: String(initialQuery || '').trim(), country: String(deps.country || '').toUpperCase(), values: { name: String(initialQuery || '').trim() }, check: null, pc: null, uuid: '' };
  const countries = Array.isArray(deps.countries) ? deps.countries : [];
  const flag = deps.optFlag || (() => '');
  const $ = (id) => box.querySelector('#' + id);
  const header = (title) => `<div class="rb-head"><h3>${esc(title)}</h3><button type="button" class="rb-x" id="rbClose" aria-label="${esc(t('common.close'))}">&times;</button></div>`;
  const wireClose = () => { const x = $('rbClose'); if (x) x.onclick = close; };
  const errText = (e) => { const k = errorKey(e); return esc(k ? t(k) : String((e && e.message) || e || '')); };

  const stationCard = (s) => {
    const fav = s.favicon ? `<img class="rb-fav" src="${esc(s.favicon)}" alt="" loading="lazy" referrerpolicy="no-referrer">` : '<span class="rb-fav rb-fav-empty"></span>';
    const resolved = s.url_resolved && s.url_resolved !== s.url
      ? `<div class="rb-kv"><span>${esc(t('rbadd.resolvedUrl'))}</span><code>${esc(s.url_resolved)}</code></div>` : '';
    const age = lastCheckAge(s.lastchecktime, locale);
    return `<div class="rb-card rb-${esc(s.status)}">
      <div class="rb-card-top">${fav}<div class="rb-card-title"><b>${esc(s.name)}</b>
        <span class="rb-status">${STATUS_ICON[s.status] || ''} ${esc(t('rbadd.status.' + s.status))}</span></div></div>
      ${playControls(previewSource(s), t, esc, isHlsSource(s, previewSource(s)))}
      <div class="rb-kv"><span>${esc(t('rbadd.streamUrl'))}</span><code>${esc(s.url)}</code></div>
      ${resolved}
      ${s.homepage ? `<div class="rb-kv"><span>${esc(t('rbadd.field.homepage'))}</span><code>${esc(s.homepage)}</code></div>` : ''}
      <div class="rb-kv"><span>${esc(t('rbadd.codec'))}</span>${esc(codecLine(s.codec, s.bitrate) || '—')}</div>
      <div class="rb-kv"><span>${esc(t('rbadd.lastCheck'))}</span>${esc(age || t('rbadd.neverChecked'))}</div>
      <div class="rb-kv rb-uuid"><span>UUID</span><code>${esc(s.stationuuid)}</code></div>
    </div>`;
  };

  // Step 1: search radio-browser first.
  function renderSearch(list, error) {
    preview.stop();
    const searched = Array.isArray(list);
    const working = hasWorkingEntry(list);
    let results = '';
    if (error) results = `<p class="rb-error">${errText(error)}</p>`;
    else if (searched && list.length === 0) results = `<p class="rb-note">${esc(t('rbadd.noneFound'))}</p>`;
    else if (searched) results = list.map(stationCard).join('');
    const offer = searched ? `
      <div class="rb-offer${working ? '' : ' rb-offer-strong'}">
        <p>${esc(t(working ? 'rbadd.offerWorking' : 'rbadd.offerNone'))}</p>
        <div class="rb-actions">
          <button type="button" class="btn ${working ? '' : 'btn-primary'}" id="rbNew">${esc(t('rbadd.addNew'))}</button>
          <button type="button" class="btn" id="rbWeb">${esc(t('rbadd.searchWeb'))}</button>
        </div>
      </div>` : '';
    box.innerHTML = `${header(t('rbadd.title'))}
      <p class="modal-sub">${esc(t('rbadd.intro'))}</p>
      <div class="rb-search-row">
        <input type="text" id="rbQuery" value="${esc(state.query)}" placeholder="${esc(t('rbadd.queryPlaceholder'))}">
        ${countrySelect(countries, state.country, esc, flag, t('rbadd.countryFilter'))}
        <button type="button" class="btn btn-primary" id="rbSearch">${esc(t('search.btn'))}</button>
      </div>
      ${offer}
      <div class="rb-results">${results}</div>`;
    wireClose();
    const q = $('rbQuery');
    const go = async () => {
      state.query = q.value.trim();
      if (!state.query) return;
      setBusy($('rbSearch'), true);
      try { renderSearch(await api.RadioLookupForAdd(state.query, state.country) || []); } catch (e) { renderSearch(null, e); }
    };
    $('rbSearch').onclick = go;
    q.onkeydown = (e) => { if (e.key === 'Enter') go(); };
    const cs = $('rbCountry');
    if (cs) cs.onchange = () => { state.country = cs.value; go(); };
    const n = $('rbNew');
    if (n) n.onclick = () => {
      if (!state.values.name) state.values.name = state.query;
      if (!state.values.countrycode && state.country) state.values.countrycode = state.country;
      renderForm();
    };
    const w = $('rbWeb');
    if (w) w.onclick = () => openURL(searchWebURL(state.query, t));
    if (!searched) q.focus();
  }

  const checkLine = (c) => {
    if (!c) return '';
    if (c.ok) {
      const warn = c.tokenParams && c.tokenParams.length ? ` <span class="rb-warn">${esc(t('rbadd.problem.expiring_token'))}</span>` : '';
      return `<div class="rb-check rb-working">🟢 ${esc(t('rbadd.status.working'))} · ${esc(codecLine(c.codec, c.bitrate) || c.contentType || '')}${warn}</div>${playControls(previewSource(c), t, esc, isHlsSource(c, previewSource(c)))}`;
    }
    return `<div class="rb-check rb-broken">🔴 ${esc(t('rbadd.streamErr.' + (c.error || 'unreachable')))}${c.detail ? ` <small>(${esc(c.detail)})</small>` : ''}</div>`;
  };

  // Step 2: the station's details.
  function renderForm() {
    preview.stop();
    const fields = FORM_FIELDS.map((f) => `
      <label class="rb-field">
        <span>${esc(t(f.label))}${f.required ? ' *' : ''}</span>
        <input type="${f.type || 'text'}" id="rbf_${f.key}" value="${esc(state.values[f.key] || '')}"
          ${f.placeholder ? `placeholder="${esc(f.placeholder)}"` : ''} ${f.maxlength ? `maxlength="${f.maxlength}"` : ''}
          autocomplete="off" autocapitalize="off" spellcheck="false">
        ${f.hint ? `<small>${esc(t(f.hint))}</small>` : ''}
        ${f.key === 'url' ? `<button type="button" class="btn btn-mini" id="rbTest">${esc(t('rbadd.test'))}</button><div id="rbCheck">${checkLine(state.check)}</div>` : ''}
        ${f.key === 'favicon' ? `<img id="rbFavPreview" class="rb-fav rb-fav-lg${state.values.favicon ? '' : ' hidden'}" alt="" src="${esc(state.values.favicon || '')}" referrerpolicy="no-referrer">` : ''}
      </label>`).join('');
    box.innerHTML = `${header(t('rbadd.formTitle'))}
      <p class="modal-sub">${esc(t('rbadd.formIntro'))}</p>
      <div class="rb-form">${fields}</div>
      <div class="rb-actions rb-footer">
        <button type="button" class="btn" id="rbBack">${esc(t('mobile.back'))}</button>
        <button type="button" class="btn btn-primary" id="rbReview">${esc(t('rbadd.review'))}</button>
      </div>`;
    wireClose();
    const read = () => { FORM_FIELDS.forEach((f) => { state.values[f.key] = $('rbf_' + f.key).value; }); };
    $('rbBack').onclick = () => { read(); renderSearch(); };
    $('rbf_favicon').oninput = (e) => {
      const img = $('rbFavPreview');
      img.src = e.target.value.trim();
      img.classList.toggle('hidden', !e.target.value.trim());
    };
    $('rbf_url').oninput = () => { state.check = null; $('rbCheck').innerHTML = ''; };
    $('rbTest').onclick = async () => {
      read();
      const btn = $('rbTest');
      setBusy(btn, true);
      try {
        state.check = await api.RadioCheckStream(draftFrom(state.values).url);
        state.values = suggestFromCheck(state.values, state.check);
        renderForm();
      } catch (e) {
        setBusy(btn, false);
        $('rbCheck').innerHTML = `<p class="rb-error">${errText(e)}</p>`;
      }
    };
    $('rbReview').onclick = () => { read(); renderReview(); };
  }

  // Step 3: duplicates, stream check, the exact payload, explicit consent.
  async function renderReview() {
    preview.stop();
    box.innerHTML = `${header(t('rbadd.reviewTitle'))}<p class="rb-note rb-busy-line"><span class="btn-spinner"></span> ${esc(t('rbadd.checking'))}</p>`;
    wireClose();
    let pc;
    try {
      pc = await api.RadioPrecheckSubmit(draftFrom(state.values));
    } catch (e) {
      box.innerHTML = `${header(t('rbadd.reviewTitle'))}<p class="rb-error">${errText(e)}</p>
        <div class="rb-actions rb-footer"><button type="button" class="btn" id="rbEdit">${esc(t('rbadd.edit'))}</button></div>`;
      wireClose();
      $('rbEdit').onclick = renderForm;
      return;
    }
    state.pc = pc;
    const fieldRows = Object.keys(pc.fields || {}).map((k) => `<tr><th>${esc(k)}</th><td><code>${esc(pc.fields[k])}</code></td></tr>`).join('');
    const problems = (pc.problems || []).map((p) => `<li>${esc(t('rbadd.problem.' + p))}</li>`).join('');
    const warnings = (pc.warnings || []).map((w) => `<li>${esc(t('rbadd.warn.' + w))}</li>`).join('');
    const fav = pc.fields.favicon ? `<img class="rb-fav rb-fav-lg" src="${esc(pc.fields.favicon)}" alt="" referrerpolicy="no-referrer">` : '';
    box.innerHTML = `${header(t('rbadd.reviewTitle'))}
      <div class="rb-preview">${fav}<div><b>${esc(pc.fields.name || '')}</b>${checkLine(pc.stream)}</div></div>
      ${problems ? `<div class="rb-problems"><b>${esc(t('rbadd.problemsTitle'))}</b><ul>${problems}</ul></div>` : ''}
      ${pc.sameUrl && pc.sameUrl.length ? `<div class="rb-dups"><b>${esc(t('rbadd.sameUrlTitle'))}</b>${pc.sameUrl.map(stationCard).join('')}</div>` : ''}
      ${warnings ? `<div class="rb-warnings"><ul>${warnings}</ul></div>` : ''}
      ${pc.similarName && pc.similarName.length ? `<details class="rb-dups"><summary>${esc(t('rbadd.similarTitle', { n: pc.similarName.length }))}</summary>${pc.similarName.map(stationCard).join('')}</details>` : ''}
      <p class="rb-note">${esc(t('rbadd.payloadIntro'))}</p>
      <table class="rb-payload">${fieldRows}</table>
      ${pc.canSubmit ? `<label class="rb-confirm"><input type="checkbox" id="rbConfirm"> ${esc(t('rbadd.confirm'))}</label>` : ''}
      <div class="rb-actions rb-footer">
        <button type="button" class="btn" id="rbEdit">${esc(t('rbadd.edit'))}</button>
        <button type="button" class="btn" id="rbRecheck">${esc(t('rbadd.recheck'))}</button>
        ${pc.canSubmit ? `<button type="button" class="btn btn-primary" id="rbSubmit" disabled>${esc(t('rbadd.submit'))}</button>` : ''}
      </div>`;
    wireClose();
    $('rbEdit').onclick = renderForm;
    $('rbRecheck').onclick = renderReview;
    const cb = $('rbConfirm');
    const sub = $('rbSubmit');
    if (cb && sub) {
      cb.onchange = () => { sub.disabled = !cb.checked; };
      sub.onclick = async () => {
        setBusy(sub, true);
        try {
          const res = await api.RadioSubmitStation(draftFrom(state.values), true);
          state.uuid = res.uuid;
          renderDone();
        } catch (e) {
          setBusy(sub, false);
          const msg = String((e && e.message) || e || '');
          if (msg.startsWith('STM_PRECHECK_FAILED')) { renderReview(); return; }
          box.querySelector('.rb-footer').insertAdjacentHTML('beforebegin', `<p class="rb-error">${errText(e)}</p>`);
        }
      };
    }
  }

  // Step 4: the new UUID and radio-browser's own check of it.
  async function renderDone() {
    preview.stop();
    box.innerHTML = `${header(t('rbadd.doneTitle'))}
      <p class="rb-success">✅ ${esc(t('rbadd.submitted'))}</p>
      <div class="rb-kv rb-uuid"><span>UUID</span><code>${esc(state.uuid)}</code></div>
      <div id="rbStatus"><p class="rb-note rb-busy-line"><span class="btn-spinner"></span> ${esc(t('rbadd.checking'))}</p></div>
      <div class="rb-actions rb-footer">
        <button type="button" class="btn" id="rbRefresh">${esc(t('rbadd.refresh'))}</button>
        <button type="button" class="btn btn-primary" id="rbDone">${esc(t('common.close'))}</button>
      </div>`;
    wireClose();
    $('rbDone').onclick = close;
    const refresh = async () => {
      const btn = $('rbRefresh');
      setBusy(btn, true);
      let html;
      try {
        const r = await api.RadioStationByUUID(state.uuid);
        html = statusTable(r && r.found ? r.station : null);
      } catch (e) {
        html = `<p class="rb-error">${errText(e)}</p>`;
      }
      if (!modal.isConnected) return;
      $('rbStatus').innerHTML = html;
      setBusy(btn, false);
    };
    $('rbRefresh').onclick = refresh;
    refresh();
  }

  function statusTable(s) {
    const waiting = !s || s.status === 'unknown';
    const check = waiting ? `⏳ ${t('rbadd.waiting')}` : `${STATUS_ICON[s.status]} ${t('rbadd.status.' + s.status)}`;
    const row = (k, v) => `<tr><th>${esc(k)}</th><td>${esc(v)}</td></tr>`;
    const age = s ? lastCheckAge(s.lastchecktime, locale) : '';
    return `<table class="rb-payload rb-status-table">
        ${row(t('rbadd.submission'), '✅')}
        ${row(t('rbadd.streamCheck'), check)}
        ${row(t('rbadd.codec'), (s && !waiting && s.codec) || '—')}
        ${row(t('rbadd.bitrate'), (s && !waiting && s.bitrate) ? `${s.bitrate} kbps` : '—')}
        ${age ? row(t('rbadd.lastCheck'), age) : ''}
      </table>
      ${!s ? `<p class="rb-note">${esc(t('rbadd.notYetVisible'))}</p>` : ''}
      ${s && s.status === 'working' ? `<p class="rb-ready">${esc(t('rbadd.ready'))}</p>` : ''}
      ${s && s.status === 'broken' ? `<p class="rb-error">${esc(t('rbadd.brokenAfter'))}</p>` : ''}`;
  }

  renderSearch();
  if (state.query) $('rbSearch').click();
  return { close };
}

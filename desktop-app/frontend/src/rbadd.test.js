import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import {
  lastCheckAge, codecLine, hasWorkingEntry, draftFrom, suggestFromCheck, searchWebURL, errorKey, previewSource, isHlsSource, prefersNativeHls, previewCandidates, countrySelect, playControls, createPreview, FORM_FIELDS,
} from './rbadd.js';

const read = (rel) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), 'utf8');
const bundleDir = fileURLToPath(new URL('./i18n/bundles/', import.meta.url));
const bundle = (lang) => JSON.parse(readFileSync(bundleDir + lang + '.json', 'utf8'));

describe('lastCheckAge', () => {
  const now = Date.parse('2026-09-30T12:00:00Z');
  it('reads radio-browser time as UTC and renders it relative', () => {
    expect(lastCheckAge('2026-09-30 11:58:00', 'en', now)).toBe('2 minutes ago');
    expect(lastCheckAge('2026-09-30 09:00:00', 'es', now)).toBe('hace 3 horas');
  });
  it('is empty for a station that was never checked', () => {
    expect(lastCheckAge('', 'en', now)).toBe('');
    expect(lastCheckAge('0000-00-00 00:00:00', 'en', now)).toBe('');
  });
});

describe('helpers', () => {
  it('codecLine joins what is known', () => {
    expect(codecLine('AAC', 64)).toBe('AAC • 64 kbps');
    expect(codecLine('MP3', 0)).toBe('MP3');
    expect(codecLine('', 0)).toBe('');
  });

  it('hasWorkingEntry looks only at working stations', () => {
    expect(hasWorkingEntry([{ status: 'broken' }, { status: 'unknown' }])).toBe(false);
    expect(hasWorkingEntry([{ status: 'broken' }, { status: 'working' }])).toBe(true);
    expect(hasWorkingEntry(null)).toBe(false);
  });

  it('draftFrom trims and normalises the codes', () => {
    expect(draftFrom({ name: ' Amor 95.3 ', url: ' https://example.com/s ', countrycode: 'mx', iso_3166_2: 'mx-cmx', languagecodes: 'SPA' }))
      .toEqual({ name: 'Amor 95.3', url: 'https://example.com/s', homepage: '', favicon: '', countrycode: 'MX', iso_3166_2: 'MX-CMX', state: '', languagecodes: 'spa', language: '', tags: '' });
  });

  it('suggestFromCheck fills only empty fields from the ICY headers', () => {
    const check = { icyName: 'Amor', icyUrl: 'https://example.com/', icyGenre: 'Romantic/Pop' };
    expect(suggestFromCheck({ name: 'Mine' }, check)).toEqual({ name: 'Mine', homepage: 'https://example.com/', tags: 'romantic,pop' });
    expect(suggestFromCheck({}, { icyUrl: 'javascript:alert(1)' }).homepage).toBeUndefined();
  });

  it('searchWebURL builds a localised Google search', () => {
    const es = bundle('es')['rbadd.searchWebQuery'];
    const t = (k, p) => es.replace('{{name}}', p.name);
    expect(searchWebURL('Amor 95.3 & más', t)).toBe('https://www.google.com/search?q=' + encodeURIComponent(es.replace('{{name}}', 'Amor 95.3 & más')));
    expect(searchWebURL('Amor 95.3 & más', t)).toContain('Amor%2095.3%20%26%20m%C3%A1s');
  });

  it('errorKey turns a lost backend into a readable message', () => {
    expect(errorKey(new TypeError('Failed to fetch'))).toBe('rbadd.err.network');
    expect(errorKey(new TypeError('Load failed'))).toBe('rbadd.err.network');
    expect(errorKey('502 bad gateway')).toBe('rbadd.err.network');
    expect(errorKey('STM_PRECHECK_FAILED: duplicate_url')).toBe('rbadd.err.precheck');
    expect(errorKey('STM_NOT_CONFIRMED')).toBe('rbadd.err.notConfirmed');
    expect(errorKey('radio-browser said no')).toBe('');
  });

  it('the form has the fields radio-browser accepts', () => {
    const goDraft = read('../../radio_submit.go');
    for (const f of FORM_FIELDS) expect(goDraft).toContain(`json:"${f.key}"`);
  });
});

describe('country filter', () => {
  const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;');
  const list = [{ cc: '', name: 'All countries' }, { cc: 'MX', name: 'México' }, { cc: 'DE', name: 'A<b>' }];

  it('marks the pinned country and escapes names', () => {
    const html = countrySelect(list, 'MX', esc, (cc) => (cc ? `[${cc}] ` : ''), 'Country');
    expect(html).toContain('id="rbCountry"');
    expect(html).toContain('<option value="MX" selected>[MX] México</option>');
    expect(html).toContain('<option value="">All countries</option>');
    expect(html).toContain('A&lt;b>');
  });

  it('renders nothing without a country list', () => {
    expect(countrySelect([], '', esc, () => '', 'Country')).toBe('');
  });
});

describe('station preview', () => {
  function fakeAudio() {
    const on = {};
    return {
      src: '', calls: [], playResult: Promise.resolve(),
      addEventListener(type, fn) { (on[type] ||= []).push(fn); },
      fire(type) { (on[type] || []).forEach((fn) => fn()); },
      play() { this.calls.push('play:' + this.src); return this.playResult; },
      pause() { this.calls.push('pause'); },
      load() { this.calls.push('load'); },
      removeAttribute(a) { if (a === 'src') this.src = ''; },
    };
  }
  function fakeRow(src) {
    const btns = { play: { disabled: false }, pause: { disabled: true }, stop: { disabled: true } };
    const state = { textContent: '' };
    return {
      dataset: { src, state: 'stopped' }, btns, state,
      querySelector(sel) {
        if (sel === '.rb-play-state') return state;
        return btns[/data-act="(\w+)"/.exec(sel)[1]];
      },
    };
  }
  const t = (k) => k;

  it('previewSource prefers the resolved stream and refuses non-http', () => {
    expect(previewSource({ url: 'http://x/a.pls', url_resolved: 'http://y/live' })).toBe('http://y/live');
    expect(previewSource({ url: 'https://x/s', resolvedUrl: 'https://x/s.aac' })).toBe('https://x/s.aac');
    expect(previewSource({ url: 'javascript:alert(1)' })).toBe('');
    expect(previewSource(null)).toBe('');
  });

  it('playControls escapes the source and starts stopped', () => {
    const html = playControls('https://x/s?a=1&b="2"', t, (s) => String(s).replace(/&/g, '&amp;').replace(/"/g, '&quot;'));
    expect(html).toContain('data-src="https://x/s?a=1&amp;b=&quot;2&quot;"');
    expect(html).toContain('data-act="pause" disabled');
    expect(playControls('', t, (s) => s)).toBe('');
  });

  it('plays, pauses, resumes and stops one row', async () => {
    const audio = fakeAudio();
    const p = createPreview({ audio, t });
    const row = fakeRow('https://x/one');
    p.play(row);
    expect(audio.src).toBe('https://x/one');
    expect(row.dataset.state).toBe('loading');
    audio.fire('playing');
    expect(row.dataset.state).toBe('playing');
    expect(row.btns.play.disabled).toBe(true);
    expect(row.btns.pause.disabled).toBe(false);
    p.pause();
    expect(row.dataset.state).toBe('paused');
    expect(row.btns.play.disabled).toBe(false);
    p.play(row);
    expect(audio.calls.filter((c) => c === 'play:https://x/one')).toHaveLength(2);
    p.stop();
    expect(row.dataset.state).toBe('stopped');
    expect(audio.src).toBe('');
    expect(row.btns.stop.disabled).toBe(true);
  });

  it('starting another station stops the first', () => {
    const audio = fakeAudio();
    const p = createPreview({ audio, t });
    const a = fakeRow('https://x/a');
    const b = fakeRow('https://x/b');
    p.play(a);
    audio.fire('playing');
    p.play(b);
    expect(a.dataset.state).toBe('stopped');
    expect(b.dataset.state).toBe('loading');
    expect(audio.src).toBe('https://x/b');
  });

  it('shows an error when the stream will not play here', async () => {
    const audio = fakeAudio();
    audio.playResult = Promise.reject(new Error('NotSupportedError'));
    const p = createPreview({ audio, t });
    const row = fakeRow('https://x/hls.m3u8');
    p.play(row);
    await Promise.resolve();
    await Promise.resolve();
    expect(row.dataset.state).toBe('error');
    expect(row.state.textContent).toBe('rbadd.play.error');
  });

  it('isHlsSource reads the URL, the directory flag and the content type', () => {
    expect(isHlsSource({}, 'https://x/256.m3u8?accessKey=1')).toBe(true);
    expect(isHlsSource({ hls: 1 }, 'https://x/live')).toBe(true);
    expect(isHlsSource({ contentType: 'application/vnd.apple.mpegurl' }, 'https://x/live')).toBe(true);
    expect(isHlsSource({ hls: 0 }, 'https://x/live.mp3')).toBe(false);
    expect(playControls('https://x/a.m3u8', t, (s) => s, true)).toContain('data-hls="1"');
    expect(playControls('https://x/a.mp3', t, (s) => s)).not.toContain('data-hls');
  });

  function fakeHls(supported = true) {
    const made = [];
    class Hls {
      static isSupported() { return supported; }
      constructor(cfg) { this.cfg = cfg; this.on_ = {}; this.destroyed = false; made.push(this); }
      on(ev, fn) { this.on_[ev] = fn; }
      loadSource(src) { this.src = src; }
      attachMedia(m) { this.media = m; }
      destroy() { this.destroyed = true; }
    }
    Hls.Events = { ERROR: 'hlsError' };
    return { Hls, made };
  }
  const tick = () => new Promise((r) => setTimeout(r, 0));

  it('plays HLS through hls.js even when canPlayType says maybe', async () => {
    const audio = fakeAudio();
    audio.canPlayType = () => 'maybe';
    const { Hls, made } = fakeHls();
    const p = createPreview({ audio, t, loadHls: async () => Hls });
    const row = fakeRow('https://x/256.m3u8');
    row.dataset.hls = '1';
    p.play(row);
    expect(row.dataset.state).toBe('loading');
    await tick();
    expect(made).toHaveLength(1);
    expect(made[0].src).toBe('https://x/256.m3u8');
    expect(made[0].media).toBe(audio);
    expect(audio.calls).toContain('play:');
    made[0].on_.hlsError(null, { fatal: true });
    expect(row.dataset.state).toBe('error');
    p.stop();
    expect(made[0].destroyed).toBe(true);
  });

  it('falls back to native HLS without Media Source Extensions', async () => {
    const audio = fakeAudio();
    const { Hls, made } = fakeHls(false);
    const p = createPreview({ audio, t, loadHls: async () => Hls });
    const row = fakeRow('https://x/256.m3u8');
    row.dataset.hls = '1';
    p.play(row);
    await tick();
    expect(made).toHaveLength(0);
    expect(audio.src).toBe('https://x/256.m3u8');
    expect(audio.calls).toContain('play:https://x/256.m3u8');
  });

  it('falls back to native HLS when hls.js fails to load', async () => {
    const audio = fakeAudio();
    const p = createPreview({ audio, t, loadHls: () => Promise.reject(new Error('chunk')) });
    const row = fakeRow('https://x/256.m3u8');
    row.dataset.hls = '1';
    p.play(row);
    await tick();
    expect(audio.src).toBe('https://x/256.m3u8');
  });

  it('plays HLS natively on Apple WebKit without loading hls.js', () => {
    const audio = fakeAudio();
    let loaded = false;
    const p = createPreview({ audio, t, nativeHls: true, loadHls: async () => { loaded = true; } });
    const row = fakeRow('https://x/256.m3u8');
    row.dataset.hls = '1';
    p.play(row);
    expect(audio.src).toBe('https://x/256.m3u8');
    expect(audio.calls).toContain('play:https://x/256.m3u8');
    expect(loaded).toBe(false);
  });

  it('prefersNativeHls only for Apple WebKit that can play HLS', () => {
    const can = { canPlayType: () => 'maybe' };
    const cannot = { canPlayType: () => '' };
    expect(prefersNativeHls(can, { vendor: 'Apple Computer, Inc.' })).toBe(true);
    expect(prefersNativeHls(cannot, { vendor: 'Apple Computer, Inc.' })).toBe(false);
    expect(prefersNativeHls(can, { vendor: 'Google Inc.' })).toBe(false);
    expect(prefersNativeHls(can, undefined)).toBe(false);
  });

  it('previewCandidates pairs an http stream with its https twin', () => {
    expect(previewCandidates('http://h/s')).toEqual(['http://h/s', 'https://h/s']);
    expect(previewCandidates('http://h/s', true)).toEqual(['https://h/s', 'http://h/s']);
    expect(previewCandidates('https://h/s', true)).toEqual(['https://h/s']);
  });

  it('tries https first where http audio is blocked, then falls back once', async () => {
    const audio = fakeAudio();
    audio.playResult = Promise.reject(new Error('blocked'));
    const p = createPreview({ audio, t, httpsFirst: true });
    const row = fakeRow('http://h/XHSHFMAAC_SC');
    p.play(row);
    expect(audio.src).toBe('https://h/XHSHFMAAC_SC');
    audio.fire('error');
    await tick();
    expect(audio.src).toBe('http://h/XHSHFMAAC_SC');
    expect(audio.calls.filter((c) => c.startsWith('play:'))).toEqual(['play:https://h/XHSHFMAAC_SC', 'play:http://h/XHSHFMAAC_SC']);
    expect(row.dataset.state).toBe('error');
  });

  it('retries an http stream over https when it fails', async () => {
    const audio = fakeAudio();
    const p = createPreview({ audio, t });
    const row = fakeRow('http://h/live');
    p.play(row);
    expect(audio.src).toBe('http://h/live');
    audio.fire('error');
    expect(audio.src).toBe('https://h/live');
    expect(row.dataset.state).toBe('loading');
    audio.fire('playing');
    expect(row.dataset.state).toBe('playing');
  });

  it('never loads hls.js for a plain stream', () => {
    const audio = fakeAudio();
    let loaded = false;
    const p = createPreview({ audio, t, loadHls: async () => { loaded = true; } });
    p.play(fakeRow('https://x/live.mp3'));
    expect(audio.src).toBe('https://x/live.mp3');
    expect(loaded).toBe(false);
  });

  it('drops an hls.js load that finishes after the user stopped', async () => {
    const audio = fakeAudio();
    audio.canPlayType = () => '';
    const { Hls, made } = fakeHls();
    const p = createPreview({ audio, t, loadHls: async () => Hls });
    const row = fakeRow('https://x/256.m3u8');
    row.dataset.hls = '1';
    p.play(row);
    p.stop();
    await tick();
    expect(made).toHaveLength(0);
    expect(row.dataset.state).toBe('stopped');
  });

});

describe('translations', () => {
  const goSubmit = read('../../radio_submit.go');
  const goCheck = read('../../../streamcheck/streamcheck.go');
  const codes = (src, re) => [...src.matchAll(re)].map((m) => m[1]);
  const problems = codes(goSubmit, /\bproblem[A-Z]\w*\s*=\s*"([a-z_]+)"/g);
  const warnings = codes(goSubmit, /\bwarn[A-Z]\w*\s*=\s*"([a-z_]+)"/g);
  const streamErrs = codes(goCheck, /\bErr[A-Z]\w*\s*=\s*"([a-z_]+)"/g);
  const used = [...new Set([...read('./rbadd.js').matchAll(/t\('(rbadd\.[\w.]+[\w])'/g)].map((m) => m[1]))];

  it('found the backend codes', () => {
    expect(problems.length).toBeGreaterThan(5);
    expect(warnings.length).toBeGreaterThan(3);
    expect(streamErrs.length).toBeGreaterThan(5);
  });

  for (const lang of ['en', 'es', 'de']) {
    it(`${lang} has every rbadd string and every backend code`, () => {
      const b = bundle(lang);
      const keys = [
        ...used,
        ...problems.map((c) => 'rbadd.problem.' + c),
        ...warnings.map((c) => 'rbadd.warn.' + c),
        ...streamErrs.map((c) => 'rbadd.streamErr.' + c),
        ...['working', 'broken', 'unknown'].map((s) => 'rbadd.status.' + s),
        ...['network', 'notConfirmed', 'precheck'].map((s) => 'rbadd.err.' + s),
        ...['loading', 'playing', 'paused', 'error'].map((s) => 'rbadd.play.' + s),
        ...FORM_FIELDS.flatMap((f) => [f.label, f.hint].filter(Boolean)),
      ];
      const missing = keys.filter((k) => !b[k]);
      expect(missing).toEqual([]);
    });
  }

  it('no other bundle carries a stale rbadd key', () => {
    const en = bundle('en');
    for (const f of readdirSync(bundleDir)) {
      const b = JSON.parse(readFileSync(bundleDir + f, 'utf8'));
      const stale = Object.keys(b).filter((k) => k.startsWith('rbadd.') && !(k in en));
      expect(stale, f).toEqual([]);
    }
  });
});

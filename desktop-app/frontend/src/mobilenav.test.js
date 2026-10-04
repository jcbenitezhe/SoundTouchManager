import { describe, it, expect } from 'vitest';
import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { MOBILE_HOME_ITEMS, mobileHomeMarkup, mountMobileHome, VIEW_EVENT } from './mobilenav.js';

const dir = fileURLToPath(new URL('./i18n/bundles/', import.meta.url));
const bundles = readdirSync(dir).filter((f) => f.endsWith('.json'))
  .map((f) => [f, JSON.parse(readFileSync(dir + f, 'utf8'))]);
const main = readFileSync(new URL('./main.js', import.meta.url), 'utf8');
const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/"/g, '&quot;');

describe('mobile home tiles', () => {
  it('every tile names a real view', () => {
    for (const it of MOBILE_HOME_ITEMS) {
      expect(main, `no #view-${it.view} in main.js`).toContain(`id="view-${it.view}"`);
    }
  });

  it('every label and hint is translated in every language', () => {
    const keys = ['mobile.home', 'mobile.homeTitle', 'mobile.back',
      ...MOBILE_HOME_ITEMS.flatMap((i) => [i.label, i.hint])];
    for (const [file, b] of bundles) {
      for (const k of keys) expect(b[k], `${file} lacks ${k}`).toBeTruthy();
    }
  });

  it('marks only the first tile as the main one and escapes the text', () => {
    const html = mobileHomeMarkup((k) => `<${k}>`, esc, () => '');
    expect(html.match(/m-tile-main/g)).toHaveLength(1);
    expect(html).not.toContain('<nav.music>');
    expect(html).toContain('&lt;nav.music>');
  });

  it('adds the donate tile only when asked', () => {
    const t = (k) => k;
    expect(mobileHomeMarkup(t, esc, () => '')).not.toContain('mDonate');
    const html = mobileHomeMarkup(t, esc, () => '', true);
    expect(html).toContain('id="mDonate"');
    const order = [...html.matchAll(/data-view="([a-z]+)"|id="(mDonate)"/g)].map((m) => m[1] || m[2]);
    expect(order.slice(0, 3)).toEqual(['box', 'mDonate', 'recent']);
    expect(html).toContain('credits.donate');
    expect(html).toContain('footer.donateSlogan');
  });

  it('donate texts exist in every language', () => {
    for (const [file, b] of bundles) {
      for (const k of ['credits.donate', 'footer.donateSlogan']) expect(b[k], `${file} lacks ${k}`).toBeTruthy();
    }
  });

  it('puts the add-station tile after Recent, only when asked', () => {
    const t = (k) => k;
    expect(mobileHomeMarkup(t, esc, () => '', true)).not.toContain('mAddStation');
    const html = mobileHomeMarkup(t, esc, () => '', true, true);
    const order = [...html.matchAll(/data-view="([a-z]+)"|id="(m[A-Z]\w+)"/g)].map((m) => m[1] || m[2]);
    expect(order.slice(0, 5)).toEqual(['box', 'mDonate', 'recent', 'mAddStation', 'spotify']);
    expect(html).toContain('rbadd.tileLabel');
    expect(html).toContain('rbadd.tileHint');
  });
});

// A minimal stand-in for the few DOM and history calls mountMobileHome makes.
function fakeEnv() {
  const listeners = {};
  const on = (target) => (type, fn) => { (target[type] ||= []).push(fn); };
  const fire = (target, type, ev) => (target[type] || []).forEach((fn) => fn(ev));
  const docL = {}; const winL = {};
  const classes = new Set();
  const nodes = {};
  const el = (id) => (nodes[id] ||= { id, textContent: '', listeners: {}, addEventListener(t, f) { (this.listeners[t] ||= []).push(f); } });
  const makeNode = () => {
    const n = { innerHTML: '', setAttribute() {}, listeners: {}, addEventListener(t, f) { (this.listeners[t] ||= []).push(f); } };
    n.querySelector = (sel) => el(sel.replace('#', ''));
    return n;
  };
  const created = [];
  const doc = {
    documentElement: { classList: { add: (c) => classes.add(c), remove: (c) => classes.delete(c) } },
    querySelector: (sel) => (sel === '.tabs' ? { after: () => {} } : null),
    createElement: () => { const n = makeNode(); created.push(n); return n; },
    addEventListener: on(docL),
  };
  const stack = [{ state: null }];
  let idx = 0;
  const history = {
    get state() { return stack[idx].state; },
    pushState(s) { stack.splice(idx + 1); stack.push({ state: s }); idx += 1; },
    replaceState(s) { stack[idx] = { state: s }; },
    back() { idx -= 1; fire(winL, 'popstate', { state: stack[idx].state }); },
  };
  const win = { history, scrollTo() {}, addEventListener: on(winL) };
  return { doc, win, classes, created, emit: (v) => fire(docL, VIEW_EVENT, { detail: v }), el, listeners };
}

describe('mountMobileHome navigation', () => {
  function mount() {
    const env = fakeEnv();
    const opened = [];
    let donated = 0;
    let added = 0;
    const openView = (v) => { opened.push(v); env.emit(v); };
    mountMobileHome({ t: (k) => k, escapeHtml: esc, icon: () => '', openView, onDonate: () => { donated += 1; }, onAddStation: () => { added += 1; }, doc: env.doc, win: env.win });
    return { env, opened, openView, donated: () => donated, added: () => added };
  }

  it('the add-station tile opens its dialog and stays on the home screen', () => {
    const { env, opened, added } = mount();
    const home = env.created[0];
    home.listeners.click[0]({ target: { closest: () => ({ id: 'mAddStation', dataset: {} }) } });
    expect(added()).toBe(1);
    expect(opened).toEqual([]);
    expect(env.classes.has('m-at-home')).toBe(true);
  });

  it('the donate tile opens the dialog and stays on the home screen', () => {
    const { env, opened, donated } = mount();
    const home = env.created[0];
    const tile = (id, view) => ({ target: { closest: () => ({ id, dataset: { view } }) } });
    home.listeners.click[0](tile('mDonate'));
    expect(donated()).toBe(1);
    expect(opened).toEqual([]);
    expect(env.classes.has('m-at-home')).toBe(true);
    home.listeners.click[0](tile('', 'library'));
    expect(opened).toEqual(['library']);
  });

  it('starts on the home screen', () => {
    const { env } = mount();
    expect(env.classes.has('m-at-home')).toBe(true);
  });

  it('leaves home on any view switch and comes back with Back', () => {
    const { env, openView } = mount();
    openView('settings');
    expect(env.classes.has('m-at-home')).toBe(false);
    expect(env.el('mBarTitle').textContent).toBe('nav.speakerSettings');
    env.el('mBack').listeners.click[0]();
    expect(env.classes.has('m-at-home')).toBe(true);
  });

  it('a switch inside a view replaces the entry, so one Back reaches home', () => {
    const { env, openView } = mount();
    openView('box');
    openView('setup');
    expect(env.win.history.state).toEqual({ stmView: 'setup' });
    env.win.history.back();
    expect(env.classes.has('m-at-home')).toBe(true);
  });
});

import { describe, it, expect } from 'vitest';
import { takeBridgeToken, createBridge, nativeHost } from './bridge.js';

function memoryStore() {
  const m = new Map();
  return { getItem: (k) => (m.has(k) ? m.get(k) : null), setItem: (k, v) => m.set(k, String(v)) };
}

describe('takeBridgeToken', () => {
  it('moves the token from the address bar into session storage', () => {
    const store = memoryStore();
    const replaced = [];
    const hist = { replaceState: (_s, _t, url) => replaced.push(url) };
    const loc = { search: '?stmToken=abc&lang=es', pathname: '/', hash: '#x' };
    expect(takeBridgeToken(loc, store, hist)).toBe('abc');
    expect(store.getItem('str.bridgeToken')).toBe('abc');
    expect(replaced).toEqual(['/?lang=es#x']);
  });

  it('falls back to the stored token after a reload', () => {
    const store = memoryStore();
    store.setItem('str.bridgeToken', 'kept');
    expect(takeBridgeToken({ search: '', pathname: '/', hash: '' }, store, {})).toBe('kept');
  });

  it('returns nothing outside the bridge', () => {
    expect(takeBridgeToken({ search: '', pathname: '/', hash: '' }, memoryStore(), {})).toBe('');
  });
});

describe('createBridge', () => {
  function fakeFetch(reply) {
    const calls = [];
    const impl = (url, init) => {
      calls.push({ url, init });
      return Promise.resolve({ ok: true, json: () => Promise.resolve(reply) });
    };
    return { calls, impl };
  }

  it('posts positional arguments with the token and resolves the result', async () => {
    const f = fakeFetch({ result: false });
    const b = createBridge('tok', f.impl);
    await expect(b.go.main.App.SetBoxVolume('192.0.2.1', 30)).resolves.toBe(false);
    expect(f.calls[0].url).toBe('/api/call/SetBoxVolume');
    expect(f.calls[0].init.headers['X-STM-Token']).toBe('tok');
    expect(JSON.parse(f.calls[0].init.body)).toEqual(['192.0.2.1', 30]);
  });

  it('rejects with the Go error message as a plain string, like the Wails IPC', async () => {
    const b = createBridge('tok', fakeFetch({ result: null, error: 'speaker offline' }).impl);
    await expect(b.go.main.App.Pause('192.0.2.1')).rejects.toBe('speaker offline');
  });

  it('delivers events with their arguments and honours the callback limit', () => {
    const b = createBridge('tok', fakeFetch({}).impl);
    const seen = [];
    b.runtime.EventsOnMultiple('install:progress', (...d) => seen.push(d), 1);
    b.dispatch('install:progress', [{ step: 1 }]);
    b.dispatch('install:progress', [{ step: 2 }]);
    expect(seen).toEqual([[{ step: 1 }]]);
  });

  it('stops delivering after EventsOff and after the returned unsubscribe', () => {
    const b = createBridge('tok', fakeFetch({}).impl);
    let a = 0;
    let c = 0;
    const off = b.runtime.EventsOnMultiple('x', () => { a += 1; }, -1);
    b.runtime.EventsOnMultiple('y', () => { c += 1; }, -1);
    off();
    b.runtime.EventsOff('y');
    b.dispatch('x', []);
    b.dispatch('y', []);
    expect([a, c]).toEqual([0, 0]);
  });
});

describe('nativeHost', () => {
  function withWindow(props, fn) {
    const had = 'window' in globalThis;
    const prev = globalThis.window;
    globalThis.window = props;
    try { return fn(); } finally { if (had) globalThis.window = prev; else delete globalThis.window; }
  }

  it('finds the Android and the iOS host, preferring STMNative', () => {
    const android = { openURL() {} };
    const ios = { openURL() {} };
    expect(withWindow({ STMAndroid: android }, nativeHost)).toBe(android);
    expect(withWindow({ STMNative: ios }, nativeHost)).toBe(ios);
    expect(withWindow({ STMAndroid: android, STMNative: ios }, nativeHost)).toBe(ios);
    expect(withWindow({}, nativeHost)).toBe(null);
  });

  it('routes BrowserOpenURL to the native host', () => {
    const opened = [];
    withWindow({ STMNative: { openURL: (u) => opened.push(u) } }, () => {
      createBridge('tok', () => Promise.resolve({})).runtime.BrowserOpenURL('https://example.com/');
    });
    expect(opened).toEqual(['https://example.com/']);
  });
});

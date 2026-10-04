// HTTP stand-in for the Wails IPC, used when the frontend is served by the
// bridge backend (desktop-app/bridge.go) instead of a Wails window, as in the
// Android app. It fills in window.go and window.runtime, so the generated
// wailsjs bindings and everything built on api.js work unchanged.
//
// The host app opens the page as /?stmToken=<secret>. The token is moved to
// sessionStorage and stripped from the address bar; without it (a normal
// Wails window, a Vite dev server) this module does nothing.

const TOKEN_KEY = 'str.bridgeToken';

export function takeBridgeToken(loc = window.location, store = window.sessionStorage, hist = window.history) {
  const params = new URLSearchParams(loc.search);
  const fromURL = params.get('stmToken');
  if (fromURL) {
    try { store.setItem(TOKEN_KEY, fromURL); } catch { /* storage disabled */ }
    params.delete('stmToken');
    const rest = params.toString();
    try { hist.replaceState(null, '', loc.pathname + (rest ? '?' + rest : '') + loc.hash); } catch { /* ignore */ }
    return fromURL;
  }
  try { return store.getItem(TOKEN_KEY) || ''; } catch { return ''; }
}

export function createBridge(token, fetchImpl = (...a) => fetch(...a)) {
  const listeners = new Map();

  function call(name, args) {
    return fetchImpl('/api/call/' + encodeURIComponent(name), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-STM-Token': token },
      body: JSON.stringify(args),
    }).then(async res => {
      if (!res.ok) throw String(res.status + ' ' + (await res.text()).trim());
      const body = await res.json();
      // The Wails IPC rejects with the Go error's message as a plain string.
      if (body.error) throw body.error;
      return body.result;
    });
  }

  function dispatch(name, data) {
    const list = listeners.get(name);
    if (!list) return;
    for (const l of [...list]) {
      if (l.remaining === 0) continue;
      if (l.remaining > 0) l.remaining -= 1;
      if (l.remaining === 0) list.delete(l);
      try { l.cb(...data); } catch (e) { console.error('bridge event handler', name, e); }
    }
  }

  const runtime = {
    EventsOnMultiple(name, cb, maxCallbacks) {
      const l = { cb, remaining: maxCallbacks > 0 ? maxCallbacks : -1 };
      if (!listeners.has(name)) listeners.set(name, new Set());
      listeners.get(name).add(l);
      return () => listeners.get(name)?.delete(l);
    },
    EventsOff(name, ...more) {
      for (const n of [name, ...more]) listeners.delete(n);
    },
    EventsOffAll() { listeners.clear(); },
    EventsEmit(name, ...data) { dispatch(name, data); },
    BrowserOpenURL(url) {
      const host = nativeHost();
      if (host && typeof host.openURL === 'function') {
        host.openURL(String(url));
      } else {
        window.open(url, '_blank', 'noopener');
      }
    },
    LogPrint() {}, LogTrace() {}, LogDebug() {}, LogInfo() {},
    LogWarning() {}, LogError() {}, LogFatal() {},
  };

  const App = new Proxy({}, {
    get: (_, name) => (typeof name === 'string' ? (...args) => call(name, args) : undefined),
  });

  return { go: { main: { App } }, runtime, dispatch };
}

function connectEvents(token, dispatch) {
  let delay = 1000;
  const open = () => {
    const es = new EventSource('/api/events?t=' + encodeURIComponent(token));
    es.onopen = () => { delay = 1000; };
    es.onmessage = ev => {
      try {
        const msg = JSON.parse(ev.data);
        dispatch(msg.name, Array.isArray(msg.data) ? msg.data : []);
      } catch { /* malformed frame */ }
    };
    es.onerror = () => {
      es.close();
      setTimeout(open, delay);
      delay = Math.min(delay * 2, 15000);
    };
  };
  open();
}

export function installBridge() {
  if (typeof window === 'undefined' || window.go) return false;
  const token = takeBridgeToken();
  if (!token) return false;
  const bridge = createBridge(token);
  window.go = bridge.go;
  window.runtime = bridge.runtime;
  document.documentElement.classList.add('stm-bridge');
  connectEvents(token, bridge.dispatch);
  return true;
}

// The phone apps inject an object with openURL before the page loads:
// STMAndroid from the Android app, STMNative from the iOS app.
export function nativeHost() {
  if (typeof window === 'undefined') return null;
  return window.STMNative || window.STMAndroid || null;
}

// A native host marks the phone build: big-button navigation, and no USB
// drives or file dialogs.
export function markMobileApp() {
  if (!nativeHost()) return false;
  document.documentElement.classList.add('stm-mobile');
  return true;
}

installBridge();
markMobileApp();

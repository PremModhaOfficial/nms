/* ==========================================================================
 * NMS Dev Dashboard - app core
 * Auth, fetch wrapper, tab router, polling helper, shared state and utils.
 * Single deliberate global: window.NMS (curated API). Everything else stays
 * inside this IIFE. No external dependencies, CSP-safe.
 * ========================================================================== */
(function () {
  'use strict';

  var TOKEN_KEY = 'nms.jwt';
  var listeners = new Map();
  var cache = {
    traces: [],          // newest-first metadata list
    full: new Map(),     // trace_id -> full trace (spans)
    lastTraceId: null,
    counters: {}         // node id -> span count across last N traces
  };
  var selectedTrace = null;
  var currentView = 'topology';
  var authed = false;
  var liveState = 'connecting';

  /* ---------------- event bus ---------------- */

  function on(event, cb) {
    if (!listeners.has(event)) listeners.set(event, new Set());
    listeners.get(event).add(cb);
  }

  function off(event, cb) {
    var set = listeners.get(event);
    if (set) set.delete(cb);
  }

  function emit(event, data) {
    var set = listeners.get(event);
    if (!set) return;
    set.forEach(function (fn) {
      try { fn(data); } catch (e) { /* module errors never break the bus */ }
    });
  }

  /* ---------------- token / auth ---------------- */

  function getToken() {
    try { return localStorage.getItem(TOKEN_KEY); } catch (e) { return null; }
  }

  function setToken(token) {
    try { localStorage.setItem(TOKEN_KEY, token); } catch (e) { /* private mode */ }
  }

  function clearToken() {
    try { localStorage.removeItem(TOKEN_KEY); } catch (e) { /* noop */ }
  }

  function isAuthed() { return authed; }

  function handle401() {
    clearToken();
    authed = false;
    showLogin();
    toast('Session expired, sign in again', 'err');
  }

  /* ---------------- api wrapper ---------------- */

  // Returns { status, json, durationMs, ok } — never throws for HTTP-level
  // failures; network errors return status 0 with a readable message.
  function api(path, opts) {
    opts = opts || {};
    var headers = opts.headers || {};
    var body = opts.body;

    if (typeof body === 'string' && opts.json !== false) {
      headers['Content-Type'] = 'application/json';
    }

    var token = getToken();
    if (token && opts.auth !== false) {
      headers['Authorization'] = 'Bearer ' + token;
    }

    var start = performance.now();

    return fetch(path, {
      method: opts.method || 'GET',
      headers: headers,
      body: body,
      signal: opts.signal || undefined,
      cache: 'no-store'
    }).then(function (res) {
      var durationMs = Math.round(performance.now() - start);
      return res.text().then(function (text) {
        var json = null;
        if (text) {
          try { json = JSON.parse(text); }
          catch (e) { json = { raw: text }; }
        }
        if (res.status === 401 && path.indexOf('/login') !== 0) {
          handle401();
        }
        return { status: res.status, json: json, durationMs: durationMs, ok: res.ok };
      });
    }).catch(function (err) {
      var durationMs = Math.round(performance.now() - start);
      if (err && err.name === 'AbortError') {
        return { status: 0, json: null, durationMs: durationMs, ok: false, aborted: true };
      }
      return {
        status: 0,
        json: { error: { message: 'Network error: ' + (err && err.message ? err.message : 'unreachable') } },
        durationMs: durationMs,
        ok: false
      };
    });
  }

  /* ---------------- login / logout ---------------- */

  function showLogin() {
    authed = false;
    document.getElementById('login-view').hidden = false;
    document.getElementById('app-view').hidden = true;
    document.getElementById('login-error').hidden = true;
  }

  function showApp() {
    authed = true;
    document.getElementById('login-view').hidden = true;
    document.getElementById('app-view').hidden = false;
    emit('auth:login');
  }

  function logout() {
    clearToken();
    selectedTrace = null;
    cache.traces = [];
    cache.lastTraceId = null;
    emit('auth:logout');
    showLogin();
    toast('Signed out', 'ok');
  }

  function handleLogin(ev) {
    ev.preventDefault();
    var errorEl = document.getElementById('login-error');
    var submit = document.getElementById('login-submit');
    var username = document.getElementById('login-username').value.trim();
    var password = document.getElementById('login-password').value;

    errorEl.hidden = true;
    submit.disabled = true;
    submit.textContent = 'Signing in…';

    api('/login', {
      method: 'POST',
      auth: false,
      body: JSON.stringify({ username: username, password: password })
    }).then(function (r) {
      if (r.ok && r.json && r.json.token) {
        setToken(r.json.token);
        showApp();
        toast('Signed in as ' + username, 'ok');
      } else {
        var msg = (r.json && r.json.error && r.json.error.message) || 'invalid credentials';
        errorEl.textContent = msg;
        errorEl.hidden = false;
      }
    }).finally(function () {
      submit.disabled = false;
      submit.textContent = 'Sign in';
    });
  }

  /* ---------------- router ---------------- */

  var VIEWS = ['topology', 'traces', 'explorer'];

  function showView(name) {
    if (VIEWS.indexOf(name) === -1) name = 'topology';
    currentView = name;
    var tabs = document.querySelectorAll('.nav-tab');
    for (var i = 0; i < tabs.length; i++) {
      var tab = tabs[i];
      var active = tab.getAttribute('data-view') === name;
      tab.classList.toggle('is-active', active);
      tab.setAttribute('aria-selected', active ? 'true' : 'false');
    }
    var panels = document.querySelectorAll('[data-view-panel]');
    for (var j = 0; j < panels.length; j++) {
      panels[j].hidden = panels[j].getAttribute('data-view-panel') !== name;
    }
    emit('view:' + name);
  }

  /* ---------------- traces shared state ---------------- */

  // Fetch the newest-first trace list; emits 'traces:updated' with
  // { list, newIds } where newIds are trace ids not seen in the previous poll.
  function refreshTraces(limit) {
    limit = limit || 50;
    return api('/api/v1/traces?limit=' + limit).then(function (r) {
      if (!r.ok) return r;
      var list = (r.json && Array.isArray(r.json.traces)) ? r.json.traces : [];
      var prev = new Set(cache.traces.map(function (t) { return t.trace_id; }));
      var newIds = [];
      for (var i = 0; i < list.length; i++) {
        if (!prev.has(list[i].trace_id)) newIds.push(list[i].trace_id);
      }
      cache.traces = list;
      cache.lastTraceId = list.length ? list[0].trace_id : null;
      emit('traces:updated', { list: list, newIds: newIds });
      return r;
    });
  }

  // Select a trace by id: fetch the full trace (spans) on first access and
  // cache it. Emits 'trace:selected' with the full trace or null.
  function selectTrace(id, opts) {
    opts = opts || {};
    var cached = cache.full.get(id);
    var p = cached
      ? Promise.resolve({ ok: true, json: cached })
      : api('/api/v1/traces/' + encodeURIComponent(id));

    return p.then(function (r) {
      if (!r.ok || !r.json) {
        if (r.status === 404) toast('Trace not found', 'err');
        else if (r.status !== 0) toast('Could not load trace', 'err');
        selectedTrace = null;
        emit('trace:selected', null);
        return null;
      }
      var trace = r.json;
      cache.full.set(id, trace);
      selectedTrace = trace;
      emit('trace:selected', trace);
      if (opts.view) showView(opts.view);
      return trace;
    });
  }

  function getSelectedTrace() { return selectedTrace; }

  /* ---------------- polling helper ---------------- */

  // poll(fn, ms): run fn immediately, then every ms. Pauses while the tab is
  // hidden; resumes on visibility. Returns { stop, restart }.
  function poll(fn, ms) {
    var timer = null;
    var paused = false;
    var running = false;

    function tick() {
      if (running || paused) return;
      running = true;
      Promise.resolve()
        .then(fn)
        .catch(function () { /* swallow; next tick retries */ })
        .then(function () { running = false; });
    }

    function start() {
      if (timer !== null) return;
      tick();
      timer = setInterval(tick, ms);
    }

    function stop() {
      if (timer !== null) { clearInterval(timer); timer = null; }
    }

    function onVis() {
      if (document.hidden) { paused = true; stop(); }
      else { paused = false; start(); }
    }

    document.addEventListener('visibilitychange', onVis);
    start();

    return {
      stop: function () { document.removeEventListener('visibilitychange', onVis); stop(); },
      restart: function () { stop(); paused = false; start(); }
    };
  }

  /* ---------------- live pill ---------------- */

  function setLive(state) {
    liveState = state;
    var pill = document.getElementById('live-pill');
    var label = document.getElementById('live-label');
    if (!pill || !label) return;
    pill.classList.remove('ok', 'err');
    if (state === 'ok') {
      pill.classList.add('ok');
      label.textContent = 'live';
    } else if (state === 'err') {
      pill.classList.add('err');
      label.textContent = 'offline';
    } else {
      label.textContent = 'connecting';
    }
  }

  /* ---------------- utils ---------------- */

  function fmtDuration(ms) {
    if (ms === null || ms === undefined || isNaN(ms)) return '–';
    if (ms < 1000) return Math.round(ms) + ' ms';
    return (ms / 1000).toFixed(2) + ' s';
  }

  function fmtTime(iso) {
    if (!iso) return '–';
    var d = new Date(iso);
    if (isNaN(d.getTime())) return String(iso);
    var p = function (n, w) { n = String(n); while (n.length < (w || 2)) n = '0' + n; return n; };
    return p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds()) + '.' + p(d.getMilliseconds(), 3);
  }

  // Map an HTTP status to a badge class and label.
  function statusInfo(code) {
    if (code === 0) return { cls: 'err', label: 'NET ERR' };
    if (code >= 200 && code < 300) return { cls: code === 202 ? 'run' : 'ok', label: String(code) };
    if (code >= 300 && code < 400) return { cls: 'warn', label: String(code) };
    return { cls: 'err', label: String(code) };
  }

  function methodClass(m) {
    var up = String(m || '').toUpperCase();
    if (up === 'GET') return 'm-get';
    if (up === 'POST') return 'm-post';
    if (up === 'PUT') return 'm-put';
    if (up === 'DELETE') return 'm-del';
    return 'muted';
  }

  // Map a span/trace component string to one of the 10 topology node ids.
  // Order matters: component-specific checks run before generic ones.
  function nodeIdForComponent(c) {
    if (c === null || c === undefined) return null;
    var s = String(c).toLowerCase();
    if (s === 'api' || s === 'http' || s.indexOf('middleware') !== -1) return 'api';
    if (s.indexOf('publish') !== -1) return 'api';
    if (s.indexOf('entity') !== -1) return 'entity';
    if (s.indexOf('schedul') !== -1) return 'scheduler';
    if (s.indexOf('poll') !== -1) return 'poller';
    // Discovery worker pool must win before the generic plugin/pool/worker rule.
    if (s.indexOf('discover') !== -1 && (s.indexOf('pool') !== -1 || s.indexOf('worker') !== -1)) return 'discoverypool';
    if (s.indexOf('plugin') !== -1 || s.indexOf('pool') !== -1 || s.indexOf('worker') !== -1) return 'pluginpool';
    if (s.indexOf('metric') !== -1) return 'metrics';
    if (s.indexOf('discover') !== -1) return 'discovery';
    if (s.indexOf('health') !== -1 || s.indexOf('monitor') !== -1 || s.indexOf('fail') !== -1) return 'health';
    if (s.indexOf('database') !== -1 || s.indexOf('postgres') !== -1 || s.indexOf('pgx') !== -1 ||
        s.indexOf('sql') !== -1 || s === 'db') return 'db';
    return null;
  }

  function escapeHtml(s) {
    return String(s === null || s === undefined ? '' : s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // Tiny DOM builder: el('div', {class:'x', onclick: fn, text:'hi'}, child...)
  // Event handlers attached via addEventListener (CSP-safe, no inline attrs).
  function el(tag, attrs) {
    var node = document.createElement(tag);
    var children = Array.prototype.slice.call(arguments, 2);
    if (attrs && typeof attrs === 'object') {
      Object.keys(attrs).forEach(function (k) {
        var v = attrs[k];
        if (v === null || v === undefined || v === false) return;
        if (k === 'class') node.setAttribute('class', v);
        else if (k === 'text') node.textContent = v;
        else if (k === 'html') node.innerHTML = v; // trusted only
        else if (k === 'dataset') Object.assign(node.dataset, v);
        else if (k === 'style' && typeof v === 'string') {
          // Applied via CSSOM, not a style="" attribute: CSP style-src
          // without 'unsafe-inline' allows CSSOM writes but blocks the
          // attribute form.
          v.split(';').forEach(function (decl) {
            var idx = decl.indexOf(':');
            if (idx === -1) return;
            var prop = decl.slice(0, idx).trim();
            var val = decl.slice(idx + 1).trim();
            if (prop) node.style.setProperty(prop, val);
          });
        }
        else if (k.indexOf('on') === 0 && typeof v === 'function') node.addEventListener(k.slice(2), v);
        else if (v === true) node.setAttribute(k, '');
        else node.setAttribute(k, v);
      });
    }
    children.forEach(function (c) {
      if (c === null || c === undefined) return;
      node.appendChild(c.nodeType ? c : document.createTextNode(String(c)));
    });
    return node;
  }

  function prettyJson(v) {
    if (v === null || v === undefined) return '';
    try {
      if (typeof v === 'string') return JSON.stringify(JSON.parse(v), null, 2);
      return JSON.stringify(v, null, 2);
    } catch (e) {
      return typeof v === 'string' ? v : String(v);
    }
  }

  var toastWrap = null;
  function toast(msg, type) {
    if (!toastWrap) toastWrap = document.getElementById('toast-wrap');
    if (!toastWrap) return;
    var t = el('div', { class: 'toast' + (type === 'err' ? ' err' : type === 'ok' ? ' ok' : ''), text: msg });
    toastWrap.appendChild(t);
    setTimeout(function () {
      if (t.parentNode) t.parentNode.removeChild(t);
    }, 3600);
  }

  /* ---------------- boot ---------------- */

  function boot() {
    var loginForm = document.getElementById('login-form');
    var logoutBtn = document.getElementById('logout-btn');
    var tabs = document.querySelectorAll('.nav-tab');

    loginForm.addEventListener('submit', handleLogin);
    logoutBtn.addEventListener('click', logout);
    tabs.forEach(function (tab) {
      tab.addEventListener('click', function () { showView(tab.getAttribute('data-view')); });
    });

    if (getToken()) {
      showApp();
      // Verify the token still works; a 401 flips to the login view.
      refreshTraces(20).then(function (r) { if (r.status === 401 || r.status === 0) showLogin(); });
    } else {
      showLogin();
    }
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', boot);
  } else {
    boot();
  }

  /* ---------------- public API ---------------- */

  window.NMS = {
    api: api,
    on: on,
    off: off,
    emit: emit,
    getToken: getToken,
    setToken: setToken,
    clearToken: clearToken,
    isAuthed: isAuthed,
    handle401: handle401,
    showLogin: showLogin,
    showApp: showApp,
    logout: logout,
    showView: showView,
    getCurrentView: function () { return currentView; },
    refreshTraces: refreshTraces,
    selectTrace: selectTrace,
    getSelectedTrace: getSelectedTrace,
    poll: poll,
    setLive: setLive,
    getLiveState: function () { return liveState; },
    cache: cache,
    fmtDuration: fmtDuration,
    fmtTime: fmtTime,
    statusInfo: statusInfo,
    methodClass: methodClass,
    nodeIdForComponent: nodeIdForComponent,
    escapeHtml: escapeHtml,
    el: el,
    prettyJson: prettyJson,
    toast: toast
  };
})();

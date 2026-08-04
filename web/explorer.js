/* ==========================================================================
 * NMS Dev Dashboard - API Explorer
 * Static catalog of every endpoint the backend exposes. Click an endpoint to
 * get a prefilled JSON body; Send executes it against the live server and
 * renders status, duration and the pretty-printed response. Successful calls
 * refresh the trace stream and flash the newest trace in the Traces tab.
 * ========================================================================== */
(function () {
  'use strict';

  var N = window.NMS;

  /* ---------------- endpoint catalog ---------------- */

  var CATALOG = [
    {
      category: 'Auth',
      items: [
        {
          id: 'login', method: 'POST', path: '/login',
          desc: 'Exchange admin credentials for a JWT. On success the token is stored and the dashboard signs in.',
          body: { username: 'admin', password: 'admin' }
        }
      ]
    },
    {
      category: 'Credentials',
      items: [
        { id: 'cred-list', method: 'GET', path: '/api/v1/credentials', desc: 'List all credential profiles (payloads masked as [HIDDEN]).', body: null },
        { id: 'cred-get', method: 'GET', path: '/api/v1/credentials/{id}', desc: 'Fetch one credential profile by id.', body: null },
        {
          id: 'cred-create', method: 'POST', path: '/api/v1/credentials',
          desc: 'Create a credential profile. payload is a JSON-encoded string; it is encrypted at rest.',
          body: { name: 'VirtualBox VMs', protocol: 'winrm', payload: '{"username":"vboxuser","password":"admin"}' }
        },
        {
          id: 'cred-update', method: 'PUT', path: '/api/v1/credentials/{id}',
          desc: 'Update a credential profile. Omit or blank payload to keep the stored value.',
          body: { name: 'VirtualBox VMs Updated', protocol: 'winrm' }
        },
        { id: 'cred-delete', method: 'DELETE', path: '/api/v1/credentials/{id}', desc: 'Delete a credential profile.', body: null }
      ]
    },
    {
      category: 'Devices',
      items: [
        { id: 'dev-list', method: 'GET', path: '/api/v1/devices', desc: 'List all devices.', body: null },
        { id: 'dev-get', method: 'GET', path: '/api/v1/devices/{id}', desc: 'Fetch one device by id.', body: null },
        {
          id: 'dev-create', method: 'POST', path: '/api/v1/devices',
          desc: 'Create a device bound to a credential and discovery profile.',
          body: {
            hostname: 'WIN-A', ip_address: '127.0.0.1', plugin_id: 'winrm', port: 15985,
            credential_profile_id: 1, discovery_profile_id: 1, polling_interval_seconds: 60, should_ping: true
          }
        },
        {
          id: 'dev-update', method: 'PUT', path: '/api/v1/devices/{id}',
          desc: 'Update mutable device fields. credential/discovery profile ids are immutable after creation.',
          body: { hostname: 'WIN-A-Updated', polling_interval_seconds: 120, should_ping: false }
        },
        { id: 'dev-delete', method: 'DELETE', path: '/api/v1/devices/{id}', desc: 'Delete a device.', body: null },
        {
          id: 'dev-provision', method: 'POST', path: '/api/v1/devices/{id}/provision',
          desc: 'Queue provisioning for a discovered device. polling_interval_seconds must be 60-3600.',
          body: { polling_interval_seconds: 60 }
        }
      ]
    },
    {
      category: 'Discovery Profiles',
      items: [
        { id: 'disc-list', method: 'GET', path: '/api/v1/discovery_profiles', desc: 'List all discovery profiles.', body: null },
        { id: 'disc-get', method: 'GET', path: '/api/v1/discovery_profiles/{id}', desc: 'Fetch one discovery profile by id.', body: null },
        {
          id: 'disc-create', method: 'POST', path: '/api/v1/discovery_profiles',
          desc: 'Create a discovery profile. target may be a single IP, CIDR, or range.',
          body: { name: 'LocalVMs', target: '127.0.0.1', port: 15985, credential_profile_id: 1, auto_provision: true }
        },
        {
          id: 'disc-update', method: 'PUT', path: '/api/v1/discovery_profiles/{id}',
          desc: 'Update a discovery profile.',
          body: { name: 'LocalVMs Updated', target: '127.0.0.1', port: 15985, credential_profile_id: 1, auto_provision: false }
        },
        { id: 'disc-delete', method: 'DELETE', path: '/api/v1/discovery_profiles/{id}', desc: 'Delete a discovery profile.', body: null },
        { id: 'disc-run', method: 'POST', path: '/api/v1/discovery_profiles/{id}/run', desc: 'Queue a discovery run for a profile. Returns 202 Accepted.', body: null }
      ]
    },
    {
      category: 'Metrics',
      items: [
        {
          id: 'metrics-query', method: 'POST', path: '/api/v1/metrics',
          desc: 'Query stored metrics for one or more devices. start/end are RFC3339; limit is capped at 1000.',
          body: { device_ids: [1], path: 'cpu.total', start: '2026-01-01T00:00:00Z', end: '2026-12-31T23:59:59Z', limit: 100 }
        }
      ]
    }
  ];

  var current = null;
  var currentBody = '';
  var lastResult = null;

  /* ---------------- rendering ---------------- */

  function init() {
    var panel = document.getElementById('view-explorer');
    panel.textContent = '';

    var head = N.el('div', { class: 'view-head' },
      N.el('div', null,
        N.el('h1', { class: 'view-title' }, 'API Explorer'),
        N.el('p', { class: 'view-sub' }, 'Call any endpoint against the live server and inspect the response')));

    var grid = N.el('div', { class: 'explorer-grid' });
    var catalog = N.el('div', { class: 'panel catalog', id: 'explorer-catalog' });
    var detail = N.el('div', { class: 'panel explorer-detail', id: 'explorer-detail' });
    detail.appendChild(N.el('div', { class: 'empty' },
      N.el('div', { class: 'empty-icon' }, '▤'),
      N.el('div', null, 'Pick an endpoint from the catalog')));

    grid.appendChild(catalog);
    grid.appendChild(detail);
    panel.appendChild(head);
    panel.appendChild(grid);

    renderCatalog();
  }

  function renderCatalog() {
    var box = document.getElementById('explorer-catalog');
    if (!box) return;
    box.textContent = '';
    CATALOG.forEach(function (group) {
      var g = N.el('div', { class: 'cat-group' });
      g.appendChild(N.el('div', { class: 'cat-group-title' }, group.category));
      group.items.forEach(function (ep) {
        var item = N.el('div', {
          class: 'cat-item' + (current && current.id === ep.id ? ' is-active' : ''),
          'data-ep': ep.id
        });
        item.appendChild(N.el('span', { class: 'badge method ' + N.methodClass(ep.method) }, ep.method));
        item.appendChild(N.el('span', { class: 'cat-path' }, ep.path));
        item.addEventListener('click', function () { selectEndpoint(ep); });
        g.appendChild(item);
      });
      box.appendChild(g);
    });
  }

  function selectEndpoint(ep) {
    current = ep;
    var bodyText = ep.body !== null && ep.body !== undefined ? JSON.stringify(ep.body, null, 2) : '{}';
    currentBody = bodyText;
    renderDetail(ep, bodyText);
    renderCatalog();
  }

  function renderDetail(ep, bodyText) {
    var box = document.getElementById('explorer-detail');
    if (!box) return;
    box.textContent = '';

    var head = N.el('div', { class: 'panel-head' },
      N.el('span', { class: 'panel-title' },
        ep.method + ' ' + ep.path),
      N.el('span', { class: 'badge ' + N.methodClass(ep.method) }, ep.method));

    var body = N.el('div', { class: 'panel-body' });

    body.appendChild(N.el('p', { style: 'margin:0 0 14px;color:var(--text-muted);font-size:12.5px;' }, ep.desc));

    // request editor
    body.appendChild(N.el('label', { class: 'field' },
      N.el('span', { class: 'field-label' }, 'Request body (JSON)'),
      N.el('textarea', { id: 'explorer-body', spellcheck: 'false' }, bodyText)));

    var actions = N.el('div', { style: 'display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-bottom:14px;' });
    actions.appendChild(N.el('button', { class: 'btn btn-primary', id: 'explorer-send', type: 'button' },
      'Send ' + ep.method));
    actions.appendChild(N.el('button', { class: 'btn btn-sm', id: 'explorer-format', type: 'button' }, 'Format JSON'));
    actions.appendChild(N.el('span', { class: 'td-muted', id: 'explorer-hint' }, bodyHint(ep)));

    // response panel (kept empty until first send, or last result re-rendered)
    var resp = N.el('div', { id: 'explorer-response' });
    if (lastResult && lastResult.epId === ep.id) renderResult(resp, lastResult.result);
    else resp.appendChild(N.el('div', { class: 'empty', style: 'padding:18px;' },
      N.el('div', null, 'Response will appear here')));

    body.appendChild(actions);
    body.appendChild(resp);
    box.appendChild(head);
    box.appendChild(body);

    var ta = document.getElementById('explorer-body');
    if (ta) {
      ta.addEventListener('input', function () { currentBody = ta.value; });
      var fmt = document.getElementById('explorer-format');
      if (fmt) fmt.addEventListener('click', function () { formatEditor(); });
      var send = document.getElementById('explorer-send');
      if (send) send.addEventListener('click', function () { doSend(); });
    }
  }

  function bodyHint(ep) {
    if (ep.method === 'GET' || ep.method === 'DELETE') return 'no body needed';
    if (ep.body === null || ep.body === undefined) return 'body optional';
    return 'JSON body';
  }

  function formatEditor() {
    var ta = document.getElementById('explorer-body');
    if (!ta) return;
    try {
      ta.value = JSON.stringify(JSON.parse(ta.value || '{}'), null, 2);
      currentBody = ta.value;
      N.toast('Formatted', 'ok');
    } catch (e) {
      N.toast('Invalid JSON: ' + e.message, 'err');
    }
  }

  function pathWithId(path) {
    return path.replace('{id}', '1');
  }

  function doSend() {
    if (!current) return;
    var sendBtn = document.getElementById('explorer-send');
    var method = current.method;
    var bodyText = currentBody.trim() || '{}';

    var opts = { method: method };
    var parsed = null;
    try {
      parsed = JSON.parse(bodyText);
    } catch (e) {
      N.toast('Request body is not valid JSON', 'err');
      return;
    }
    if (method !== 'GET' && method !== 'DELETE') {
      opts.body = JSON.stringify(parsed);
    }

    if (sendBtn) { sendBtn.disabled = true; sendBtn.textContent = 'Sending…'; }

    N.api(pathWithId(current.path), opts).then(function (r) {
      if (sendBtn) { sendBtn.disabled = false; sendBtn.textContent = 'Send ' + method; }
      lastResult = { epId: current.id, result: r };
      var box = document.getElementById('explorer-response');
      if (box) renderResult(box, r);

      if (r.ok && method !== 'GET' && method !== 'DELETE') {
        N.toast(method + ' ' + current.path + ' → ' + r.status, 'ok');
      } else if (!r.ok && r.status !== 0) {
        var msg = (r.json && r.json.error && r.json.error.message) || 'request failed';
        N.toast(method + ' failed (' + r.status + '): ' + msg, 'err');
      }

      if (current.id === 'login' && r.ok && r.json && r.json.token) {
        N.setToken(r.json.token);
        N.toast('Token stored — dashboard signed in', 'ok');
      }

      // Every API call generates a trace; refresh the stream and flash the
      // newest one in the Traces tab.
      if (current.id !== 'login') {
        N.refreshTraces(50).then(function (tr) {
          if (tr.ok && tr.json && tr.json.traces && tr.json.traces.length) {
            flashNewest(tr.json.traces[0].trace_id);
          }
        });
      }
    });
  }

  function flashNewest(traceId) {
    N.toast('New trace generated — see it in the Traces tab', 'ok');
    N.showView('traces');
    setTimeout(function () {
      var rows = document.querySelectorAll('#view-traces tbody tr');
      rows.forEach(function (r) {
        if (r.getAttribute('data-trace') === traceId) {
          r.classList.remove('flash');
          void r.offsetWidth; // restart the animation
          r.classList.add('flash');
        }
      });
    }, 60);
  }

  function renderResult(box, r) {
    box.textContent = '';
    var info = N.statusInfo(r.status);

    var reqLine = N.el('div', { class: 'summary-line' },
      N.el('span', { class: 'summary-key' }, 'Request'),
      N.el('span', { class: 'badge method ' + N.methodClass(current.method) }, current.method),
      N.el('span', { class: 'summary-val', style: 'font-family:var(--mono);font-size:12px;' }, pathWithId(current.path)));

    var resLine = N.el('div', { class: 'summary-line' },
      N.el('span', { class: 'summary-key' }, 'Response'),
      N.el('span', { class: 'resp-status' },
        N.el('span', { class: 'badge ' + info.cls }, info.label),
        N.el('span', { class: 'duration' }, r.durationMs + ' ms')));

    box.appendChild(reqLine);
    box.appendChild(resLine);

    var payload = r.json !== null && r.json !== undefined ? r.json : { message: 'empty response body' };
    box.appendChild(N.el('pre', { class: 'code', style: 'max-height:420px;margin-top:10px;' }, N.prettyJson(payload)));

    var copy = N.el('button', { class: 'btn btn-ghost btn-sm copy-btn', type: 'button', style: 'position:static;margin-top:8px;opacity:1;' }, 'copy response');
    copy.addEventListener('click', function () {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(N.prettyJson(payload)).then(function () {
          N.toast('Copied', 'ok');
        }, function () { N.toast('Copy failed', 'err'); });
      } else { N.toast('Clipboard unavailable', 'err'); }
    });
    box.appendChild(copy);
  }

  /* ---------------- wiring ---------------- */

  N.on('view:explorer', function () {
    var panel = document.getElementById('view-explorer');
    if (!panel || !panel.children.length) init();
  });
})();

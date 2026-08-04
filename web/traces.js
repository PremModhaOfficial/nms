/* ==========================================================================
 * NMS Dev Dashboard - Traces view
 * Live table of recent traces (polled every 3s). Clicking a row loads the
 * full trace and renders a CSS-positioned waterfall: one row per span,
 * indented by parent chain, with a proportional duration bar. Expanding a
 * row shows its events; request.body / response.body render as scrollable
 * code blocks with credentials already masked by the server.
 * ========================================================================== */
(function () {
  'use strict';

  var N = window.NMS;
  var pollHandle = null;
  var currentList = [];
  var expandedTraceId = null;
  var expandedSpans = new Set();
  var initialized = false;

  /* ---------------- helpers ---------------- */

  function componentChips(spans) {
    var out = [];
    var seen = {};
    var comps = spans.map(function (s) { return s.component; });
    comps.forEach(function (c) {
      if (!c || seen[c]) return;
      seen[c] = true;
      var nid = N.nodeIdForComponent(c);
      out.push(N.el('span', { class: 'chip c-' + (nid || 'other') }, c));
    });
    return out;
  }

  // Build the span tree (parent -> children) from the flat span list.
  function spanTree(spans) {
    var byId = {};
    var roots = [];
    spans.forEach(function (s) { byId[s.span_id] = s; });
    spans.forEach(function (s) {
      var parent = s.parent_id ? byId[s.parent_id] : null;
      s._children = s._children || [];
      s._depth = (parent ? (parent._depth || 0) + 1 : 0);
      if (parent) {
        parent._children = parent._children || [];
        parent._children.push(s);
      } else {
        roots.push(s);
      }
    });
    return roots;
  }

  // Flatten the tree in display order (parents before children, depth-first),
  // computing each span's timeline offset relative to the trace root.
  function flattenTree(spans) {
    var roots = spanTree(spans);
    var order = [];
    var t0 = spans.length ? Math.min.apply(null, spans.map(function (s) {
      var t = new Date(s.started_at || 0).getTime();
      return isNaN(t) ? 0 : t;
    })) : 0;

    var visit = function (node) {
      order.push(node);
      (node._children || []).forEach(visit);
    };
    roots.forEach(visit);

    order.forEach(function (s) {
      var st = new Date(s.started_at || 0).getTime();
      s._t0 = (isNaN(st) ? t0 : st) - t0;
      s._dur = typeof s.duration_ms === 'number' ? s.duration_ms : 0;
    });
    return order;
  }

  function computeWaterfall(spans) {
    // Copy the spans before building the tree: spanTree/flattenTree stamp
    // _children/_depth/_t0/_startPct onto the span objects, and the cached
    // trace is re-rendered on every poll. Mutating it in place would grow the
    // tree on each re-render (5 spans -> 10 -> 20 -> ...).
    var copies = spans.map(function (s) {
      return Object.assign({}, s, { events: s.events, attributes: s.attributes });
    });
    var order = flattenTree(copies);
    var maxEnd = order.reduce(function (m, s) { return Math.max(m, s._t0 + s._dur); }, 0) || 1;
    order.forEach(function (s) {
      s._startPct = Math.max(0, Math.min(100, (s._t0 / maxEnd) * 100));
      s._widthPct = Math.max(0, Math.min(100 - s._startPct, (s._dur / maxEnd) * 100));
    });
    return { order: order, maxEnd: maxEnd };
  }

  function colorFor(component) {
    var nid = N.nodeIdForComponent(component);
    var colors = {
      api: '#5b9dff', entity: '#4dd0e1', scheduler: '#b39ddb', poller: '#ffb74d',
      pluginpool: '#ff8a65', metrics: '#81c784', db: '#ef5350', discovery: '#4db6ac',
      discoverypool: '#26c6da', health: '#f06292'
    };
    return colors[nid] || '#6d7a94';
  }

  function eventBody(event) {
    if (!event || !event.attributes) return null;
    if (typeof event.attributes.body === 'string') return event.attributes.body;
    if (event.attributes.body && typeof event.attributes.body === 'object') {
      return JSON.stringify(event.attributes.body, null, 2);
    }
    return null;
  }

  /* ---------------- rendering ---------------- */

  function emptyState() {
    var panel = document.getElementById('view-traces');
    panel.textContent = '';
    panel.appendChild(N.el('div', { class: 'panel' },
      N.el('div', { class: 'empty' },
        N.el('div', { class: 'empty-icon' }, '◎'),
        N.el('div', null, 'No traces yet'),
        N.el('div', null, 'Run a request from the API Explorer and it will appear here'))));
  }

  function renderList() {
    var panel = document.getElementById('view-traces');
    panel.textContent = '';

    if (!currentList.length) { emptyState(); return; }

    var head = N.el('div', { class: 'view-head' },
      N.el('div', null,
        N.el('h1', { class: 'view-title' }, 'Traces'),
        N.el('p', { class: 'view-sub' },
          'Newest first · ' + currentList.length + ' shown · polling every 3s · click a trace to open the waterfall')),
      N.el('div', { class: 'view-actions' },
        N.el('span', { class: 'badge muted' }, 'auto-refresh'),
        N.el('button', { class: 'btn btn-sm', id: 'traces-refresh', type: 'button' }, 'Refresh now')));

    var box = N.el('div', { class: 'panel' });
    var wrap = N.el('div', { class: 'table-wrap' });
    var table = N.el('table', { class: 'table' });

    var thead = N.el('thead');
    var hr = N.el('tr');
    ['Time', 'Method', 'Path', 'Status', 'Duration', 'Components', ''].forEach(function (h) {
      hr.appendChild(N.el('th', { text: h }));
    });
    thead.appendChild(hr);
    table.appendChild(thead);

    var tbody = N.el('tbody');
    currentList.forEach(function (t) {
      var info = N.statusInfo(t.status_code);
      var row = N.el('tr', { class: 'clickable', 'data-trace': t.trace_id });

      row.appendChild(N.el('td', { class: 'td-time' }, N.fmtTime(t.started_at)));
      row.appendChild(N.el('td', null, N.el('span', { class: 'badge method ' + N.methodClass(t.method) }, t.method || '')));
      row.appendChild(N.el('td', { class: 'td-path' }, t.path || '–'));
      row.appendChild(N.el('td', null, N.el('span', { class: 'badge ' + info.cls }, info.label)));
      row.appendChild(N.el('td', { class: 'td-dur' }, N.fmtDuration(t.duration_ms)));
      row.appendChild(N.el('td', null, N.el('div', { style: 'display:flex;gap:4px;flex-wrap:wrap;' },
        componentChipsFromMeta(t))));
      row.appendChild(N.el('td', { class: 'td-muted' }, t.error ? '⚠' : ''));

      row.addEventListener('click', function () { openTrace(t.trace_id); });
      tbody.appendChild(row);
    });
    table.appendChild(tbody);
    wrap.appendChild(table);
    box.appendChild(wrap);
    panel.appendChild(head);
    panel.appendChild(box);

    var refreshBtn = document.getElementById('traces-refresh');
    if (refreshBtn) refreshBtn.addEventListener('click', function () { N.refreshTraces(50); });
  }

  function componentChipsFromMeta(t) {
    var comps = t.component_ids || [];
    var out = [];
    var seen = {};
    comps.forEach(function (c) {
      if (!c || seen[c]) return;
      seen[c] = true;
      out.push(N.el('span', { class: 'chip c-' + (N.nodeIdForComponent(c) || 'other') }, c));
    });
    if (!out.length && (t.span_count || 0) > 0) {
      out.push(N.el('span', { class: 'badge muted' }, String(t.span_count) + ' spans'));
    }
    return out;
  }

  /* ---------------- waterfall ---------------- */

  function openTrace(id) {
    expandedTraceId = id;
    N.selectTrace(id).then(function (full) {
      if (full) renderDetail(full);
    });
  }

  function renderDetail(trace) {
    var panel = document.getElementById('view-traces');
    panel.textContent = '';

    var spans = trace.spans || [];
    var info = N.statusInfo(trace.status_code);
    var comps = trace.component_ids || spans.map(function (s) { return s.component; });

    var head = N.el('div', { class: 'view-head' },
      N.el('div', null,
        N.el('h1', { class: 'view-title' }, 'Trace detail'),
        N.el('p', { class: 'view-sub' }, 'Waterfall of the spans that handled this request')));

    var detailHead = N.el('div', { class: 'trace-detail-head' },
      N.el('span', {
        class: 'trace-id-link',
        title: 'Open this trace in the topology view'
      }, 'Trace: ' + trace.trace_id),
      N.el('span', { class: 'badge ' + N.methodClass(trace.method) }, trace.method || ''),
      N.el('span', { class: 'td-path', style: 'color:var(--text);' }, trace.path || ''),
      N.el('span', { class: 'badge ' + info.cls }, info.label),
      N.el('span', { class: 'td-muted' }, N.fmtDuration(trace.duration_ms)));

    detailHead.firstChild.addEventListener('click', function () {
      N.selectTrace(trace.trace_id, { view: 'topology' });
    });

    var box = N.el('div', { class: 'panel' });

    if (!spans.length) {
      box.appendChild(N.el('div', { class: 'empty' },
        N.el('div', { class: 'empty-icon' }, '∅'),
        N.el('div', null, 'This trace has no span data yet'),
        N.el('div', null, 'Meta: ' + (trace.method || '') + ' ' + (trace.path || '') + ' · ' +
          (trace.span_count || 0) + ' spans · ' + N.fmtDuration(trace.duration_ms))));
      panel.appendChild(head);
      panel.appendChild(detailHead);
      panel.appendChild(box);
      return;
    }

    var wf = computeWaterfall(spans);

    var wfHead = N.el('div', { class: 'wf-head' },
      N.el('span', null, 'Span'),
      N.el('span', null, 'Component'),
      N.el('span', { style: 'text-align:right;' }, 'Duration'),
      N.el('span', null, 'Timeline'));

    box.appendChild(wfHead);
    box.appendChild(N.el('div', { class: 'bar-total', style: 'padding:8px 12px;border-bottom:1px solid var(--border);' },
      'total ' + N.fmtDuration(trace.duration_ms)));

    wf.order.forEach(function (s) {
      var pair = buildSpanRow(s, wf);
      box.appendChild(pair[0]);
      box.appendChild(pair[1]);
    });

    panel.appendChild(head);
    panel.appendChild(detailHead);
    panel.appendChild(box);
  }

  function buildSpanRow(s, wf) {
    var row = N.el('div', { class: 'wf-row' + (expandedSpans.has(s.span_id) ? ' is-open' : ''),
      'data-span': s.span_id });

    var indent = s._depth * 16;

    var nameCell = N.el('div', { class: 'wf-cell' });
    var nameWrap = N.el('div', { class: 'wf-name', style: 'padding-left:' + indent + 'px;' });
    nameWrap.appendChild(N.el('span', { class: 'caret', text: '▸' }));
    nameWrap.appendChild(N.el('span', { text: s.name || '(span)' }));
    nameCell.appendChild(nameWrap);

    var compCell = N.el('div', { class: 'wf-cell' });
    compCell.appendChild(N.el('span', { class: 'wf-comp' }, s.component || '–'));

    var durCell = N.el('div', { class: 'wf-cell' });
    durCell.appendChild(N.el('span', { class: 'wf-dur' }, N.fmtDuration(s.duration_ms)));

    var trackCell = N.el('div', { class: 'wf-cell' });
    var track = N.el('div', { class: 'wf-track' });
    track.appendChild(N.el('div', {
      class: 'wf-bar',
      style: 'left:' + s._startPct + '%;width:' + Math.max(s._widthPct, 0.6) + '%;background:' + colorFor(s.component) + ';'
    }));
    trackCell.appendChild(track);

    row.appendChild(nameCell);
    row.appendChild(compCell);
    row.appendChild(durCell);
    row.appendChild(trackCell);

    row.addEventListener('click', function () { toggleSpan(row, s); });

    // events block (sibling of the row, spans the full grid width)
    var eventsBlock = N.el('div', { class: 'wf-events' });
    eventsBlock.style.display = expandedSpans.has(s.span_id) ? 'block' : 'none';
    var evs = s.events || [];
    if (!evs.length) {
      eventsBlock.appendChild(N.el('div', { class: 'empty', style: 'padding:14px;' },
        N.el('div', null, 'No events recorded for this span')));
    } else {
      evs.forEach(function (ev) {
        eventsBlock.appendChild(buildEvent(ev));
      });
    }

    return [row, eventsBlock];
  }

  function toggleSpan(row, s) {
    var open = !expandedSpans.has(s.span_id);
    if (open) expandedSpans.add(s.span_id); else expandedSpans.delete(s.span_id);
    row.classList.toggle('is-open', open);
    var block = row.nextElementSibling;
    if (block && block.classList.contains('wf-events')) block.style.display = open ? 'block' : 'none';
  }

  function buildEvent(ev) {
    var item = N.el('div', { class: 'event-item' });

    var head = N.el('div', { class: 'event-head' },
      N.el('span', { class: 'event-name' }, ev.name || 'event'),
      N.el('span', { class: 'event-time' }, N.fmtTime(ev.time)));

    item.appendChild(head);

    var attrs = ev.attributes || {};
    var keys = Object.keys(attrs);

    if (attrs.body !== undefined) {
      var block = N.el('div', { class: 'body-block code-block' });
      block.appendChild(N.el('pre', { class: 'code', style: 'max-height:240px;' }, N.prettyJson(attrs.body)));
      var copy = N.el('button', { class: 'btn btn-ghost btn-sm copy-btn', type: 'button' }, 'copy');
      copy.addEventListener('click', function () {
        copyToClipboard(typeof attrs.body === 'string' ? attrs.body : JSON.stringify(attrs.body, null, 2));
      });
      block.appendChild(copy);
      item.appendChild(block);
    }

    if (keys.length) {
      var grid = N.el('div', { class: 'event-attrs' });
      keys.forEach(function (k) {
        if (k === 'body') return;
        var v = attrs[k];
        var line = N.el('div', { class: 'attr-line' },
          N.el('span', { class: 'attr-key' }, k),
          N.el('span', { class: 'attr-val' }, typeof v === 'string' ? v : N.prettyJson(v)));
        grid.appendChild(line);
      });
      if (grid.children.length) item.appendChild(grid);
    }
    return item;
  }

  function copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        N.toast('Copied to clipboard', 'ok');
      }, function () { N.toast('Copy failed', 'err'); });
      return;
    }
    N.toast('Clipboard unavailable', 'err');
  }

  /* ---------------- wiring ---------------- */

  function onTracesUpdated(ev) {
    currentList = ev.list;
    if (N.getCurrentView() !== 'traces') return;
    renderList();

    // Highlight the newest trace id in the table.
    var newest = ev.list.length ? ev.list[0].trace_id : null;
    if (newest) {
      var rows = document.querySelectorAll('#view-traces tbody tr');
      rows.forEach(function (r) {
        if (r.getAttribute('data-trace') === newest) r.classList.add('flash');
      });
    }

    // Keep the currently-open waterfall fresh if it's still in the list.
    if (expandedTraceId) {
      var still = ev.list.some(function (t) { return t.trace_id === expandedTraceId; });
      if (still && N.getSelectedTrace() && N.getSelectedTrace().trace_id === expandedTraceId) {
        renderDetail(N.getSelectedTrace());
      }
    }
  }

  function onViewTraces() {
    if (!initialized) {
      initialized = true;
      if (!pollHandle) {
        pollHandle = N.poll(function () {
          return N.refreshTraces(50);
        }, 3000);
      }
    }
    if (currentList.length) renderList();
    else renderList(); // shows empty state
  }

  N.on('traces:updated', onTracesUpdated);
  N.on('view:traces', onViewTraces);
})();

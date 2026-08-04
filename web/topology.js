/* ==========================================================================
 * NMS Dev Dashboard - Topology view
 * Renders the ARCHITECTURE.md component graph as inline SVG with hand-placed
 * coordinates, then highlights the components/edges a selected trace
 * traversed. Polls traces every 2s; follows the newest trace when live mode
 * is on. Per-node counters tally spans across the last 20 traces.
 * ========================================================================== */
(function () {
  'use strict';

  var N = window.NMS;
  var EDGES = [
    { id: 'e-api-es',      from: 'api',       to: 'entity',     label: 'Request/Reply' },
    { id: 'e-api-ms',      from: 'api',       to: 'metrics',    label: 'Request/Reply' },
    { id: 'e-es-db',       from: 'entity',    to: 'db',         label: 'sqlx' },
    { id: 'e-es-sched',    from: 'entity',    to: 'scheduler',  label: 'Events' },
    { id: 'e-es-disc',     from: 'entity',    to: 'discovery',  label: 'Events' },
    { id: 'e-sched-poll',  from: 'scheduler', to: 'poller',     label: 'Devices' },
    { id: 'e-sched-hm',    from: 'scheduler', to: 'health',     label: 'Failures' },
    { id: 'e-poll-pool',   from: 'poller',    to: 'pluginpool', label: 'Jobs' },
    { id: 'e-pool-ms',     from: 'pluginpool', to: 'metrics',   label: 'Results' },
    { id: 'e-pool-hm',     from: 'pluginpool', to: 'health',    label: 'Results' },
    { id: 'e-ms-db',       from: 'metrics',   to: 'db',         label: 'pgx.CopyFrom' },
    { id: 'e-hm-es',       from: 'health',    to: 'entity',     label: 'OpDeactivateDevice' },
    { id: 'e-disc-pool',   from: 'discovery', to: 'discoverypool', label: 'Jobs' },
    { id: 'e-pool-es',     from: 'discoverypool', to: 'entity',  label: 'Results' }
  ];

  // The API returns node ids/type/label. Layout is hand-placed to mirror the
  // ARCHITECTURE.md mermaid diagram (canvas 1360 x 960, box 210 x 76).
  var LAYOUT = {
    api:       { x: 575, y: 48,  type: 'service' },
    entity:    { x: 575, y: 192, type: 'service' },
    metrics:   { x: 575, y: 512, type: 'service' },
    scheduler: { x: 172, y: 336, type: 'service' },
    poller:    { x: 172, y: 528, type: 'service' },
    pluginpool:{ x: 172, y: 720, type: 'service' },
    discovery: { x: 978, y: 336, type: 'service' },
    health:    { x: 978, y: 528, type: 'service' },
    discoverypool: { x: 978, y: 720, type: 'service' },
    db:        { x: 575, y: 800, type: 'db' }
  };

  var BOX_W = 210;
  var BOX_H = 76;
  var NODE_IDS = Object.keys(LAYOUT);
  var nodeStatus = {};   // node id -> 'ok' | 'busy' | 'err'
  var activeTraceId = null;
  var sideRendered = false;
  var topologyMeta = null;
  var stats = {};        // node id -> { spanCount, errCount }

  // Warm fallback for the edge target anchor: the discovery worker pool's
  // Results edge reaches the entity node's left side via an elbow path.
  function anchor(node, dir) {
    var l = LAYOUT[node];
    if (!l) return { x: 0, y: 0 };
    var cx = l.x + BOX_W / 2;
    var cy = l.y + BOX_H / 2;
    var top = l.y;
    var bottom = l.y + BOX_H;
    var left = l.x;
    var right = l.x + BOX_W;
    switch (dir) {
      case 'top': return { x: cx, y: top };
      case 'bottom': return { x: cx, y: bottom };
      case 'left': return { x: left, y: cy };
      case 'right': return { x: right, y: cy };
      case 'topleft': return { x: left + 36, y: top };
      case 'topright': return { x: right - 36, y: top };
      case 'bottomleft': return { x: left + 36, y: bottom };
      case 'bottomright': return { x: right - 36, y: bottom };
      default: return { x: cx, y: cy };
    }
  }

  // Computed concrete geometry for every edge. Never collides with any node
  // box on the canvas (verified against the LAYOUT coordinates).
  var EDGE_GEO = {
    'e-api-es':     { a: 'bottom', b: 'top', points: null },
    'e-api-ms':     { a: 'right',  b: 'right', points: [[875, 86], [875, 550]] },
    'e-es-db':      { a: 'bottom', b: 'top', points: [[680, 330], [430, 330], [430, 780], [680, 780]] },
    'e-es-sched':   { a: 'left',   b: 'right', points: null },
    'e-es-disc':    { a: 'right',  b: 'left', points: null },
    'e-sched-poll': { a: 'bottom', b: 'top', points: null },
    'e-sched-hm':   { a: 'right',  b: 'bottom', points: [[453, 374], [453, 630], [1083, 630]] },
    'e-poll-pool':  { a: 'bottom', b: 'top', points: null },
    'e-pool-ms':    { a: 'right',  b: 'bottom', points: null },
    'e-pool-hm':    { a: 'right',  b: 'left', points: null },
    'e-ms-db':      { a: 'bottom', b: 'top', points: null },
    'e-hm-es':      { a: 'right',  b: 'right', points: [[1210, 566], [1210, 230], [875, 230]] },
    'e-disc-pool':  { a: 'bottom', b: 'right', points: [[1210, 480], [1210, 758]] },
    'e-pool-es':    { a: 'top',    b: 'left', points: [[1083, 700], [40, 700], [40, 230]] }
  };

  /* ---------------- geometry resolution ---------------- */

  function resolveEdge(e) {
    var geo = EDGE_GEO[e.id] || { a: 'right', b: 'left', points: null };
    var from = anchor(e.from, geo.a || 'right');
    var to = anchor(e.to, geo.b || 'left');
    return {
      from: from,
      to: to,
      points: geo.points,
      label: e.label,
      fromNode: e.from,
      toNode: e.to
    };
  }

  function svgPathData(resolved) {
    var pts = resolved.points;
    var from = resolved.from;
    var to = resolved.to;
    if (!pts || !pts.length) {
      var mx = (from.x + to.x) / 2;
      var my = (from.y + to.y) / 2;
      // Straight line: build a mild bezier so the arrow head stays clean.
      return 'M ' + from.x + ' ' + from.y + ' C ' + mx + ' ' + from.y + ', ' + mx + ' ' + to.y + ', ' + to.x + ' ' + to.y;
    }
    var d = 'M ' + from.x + ' ' + from.y;
    var p;
    for (var i = 0; i < pts.length; i++) {
      p = pts[i];
      d += ' L ' + p[0] + ' ' + p[1];
    }
    d += ' L ' + to.x + ' ' + to.y;
    return d;
  }

  // Midpoint along a path's geometry, for the label. Uses the polyline length
  // for waypoint edges and the bezier midpoint otherwise.
  function labelPoint(resolved) {
    var pts = resolved.points;
    var from = resolved.from;
    var to = resolved.to;
    if (pts && pts.length) {
      var seq = [from].concat(pts, [to]);
      var total = 0;
      var i;
      for (i = 1; i < seq.length; i++) total += Math.hypot(seq[i].x - seq[i - 1].x, seq[i].y - seq[i - 1].y);
      var target = total / 2;
      var acc = 0;
      for (i = 1; i < seq.length; i++) {
        var seg = Math.hypot(seq[i].x - seq[i - 1].x, seq[i].y - seq[i - 1].y);
        if (acc + seg >= target) {
          var t = seg === 0 ? 0 : (target - acc) / seg;
          return { x: seq[i - 1].x + (seq[i].x - seq[i - 1].x) * t, y: seq[i - 1].y + (seq[i].y - seq[i - 1].y) * t };
        }
        acc += seg;
      }
      return { x: to.x, y: to.y };
    }
    var mx = (from.x + to.x) / 2;
    return { x: mx, y: (from.y + to.y) / 2 };
  }

  /* ---------------- trace path computation ---------------- */

  // Build the ordered node chain for a trace from component_ids plus span
  // start times. Skips unknown components; dedupes consecutive repeats.
  function traceNodePath(trace) {
    var ids = trace.component_ids;
    var spans = trace.spans || [];
    if (Array.isArray(ids) && ids.length) {
      return ids.map(function (c) { return N.nodeIdForComponent(c); })
        .filter(Boolean);
    }
    var seen = [];
    var byTime = spans.slice().sort(function (a, b) {
      return (a.started_at || '').localeCompare(b.started_at || '');
    });
    byTime.forEach(function (s) {
      var nid = N.nodeIdForComponent(s.component);
      if (nid && seen[seen.length - 1] !== nid) seen.push(nid);
    });
    return seen;
  }

  // Map the ordered node chain to an ordered edge id list (the sequence of
  // traversed edges). Known edges win; adjacency fallback fills gaps.
  function traceEdgePath(nodes) {
    var out = [];
    var byId = {};
    EDGES.forEach(function (e) { byId[e.id] = e; });
    var byPair = {};
    EDGES.forEach(function (e) {
      var k = e.from + '>' + e.to;
      byPair[k] = e.id;
    });
    for (var i = 0; i + 1 < nodes.length; i++) {
      var a = nodes[i];
      var b = nodes[i + 1];
      if (a === b) continue;
      var id = byPair[a + '>' + b];
      if (id) { out.push(id); continue; }
      var inv = byPair[b + '>' + a];
      if (inv) { out.push(inv); continue; }
      // Adjacency fallback: is there a structural edge touching both?
      var hits = EDGES.filter(function (e) { return e.from === a || e.to === a; })
        .filter(function (e) { return e.from === b || e.to === b; });
      if (hits.length) out.push(hits[0].id);
    }
    return out;
  }

  function hasError(trace) {
    return !!(trace && (trace.error === true || trace.error === 'true' || trace.error === 1));
  }

  /* ---------------- counters ---------------- */

  function updateCounters(list) {
    stats = {};
    var seen = new Set();
    list.forEach(function (t) {
      if (seen.has(t.trace_id)) return;
      seen.add(t.trace_id);
      var nodes = traceNodePath(t);
      var isErr = hasError(t);
      nodes.forEach(function (nid) {
        if (!stats[nid]) stats[nid] = { spanCount: 0, errCount: 0 };
        stats[nid].spanCount++;
        if (isErr) stats[nid].errCount++;
      });
    });
    N.cache.counters = stats;
    renderNodeBadges();
  }

  /* ---------------- SVG construction ---------------- */

  var svg = null;
  var nodeEls = {};
  var edgeEls = {};

  function buildSvg() {
    var panel = document.getElementById('view-topology');
    panel.textContent = '';

    svg = N.el('svg', {
      class: 'topo-svg',
      viewBox: '0 0 1360 960',
      xmlns: 'http://www.w3.org/2000/svg',
      role: 'img',
      'aria-label': 'NMS component topology'
    });

    var defs = N.el('defs');
    defs.appendChild(N.el('marker', {
      id: 'arrow', viewBox: '0 0 10 10', refX: '9', refY: '5', markerWidth: '7',
      markerHeight: '7', orient: 'auto-start-reverse'
    }, N.el('path', { d: 'M 0 0 L 10 5 L 0 10 z', fill: '#2c3750' })));
    defs.appendChild(N.el('marker', {
      id: 'arrow-hot', viewBox: '0 0 10 10', refX: '9', refY: '5', markerWidth: '7',
      markerHeight: '7', orient: 'auto-start-reverse'
    }, N.el('path', { d: 'M 0 0 L 10 5 L 0 10 z', fill: '#5b9dff' })));
    defs.appendChild(N.el('marker', {
      id: 'arrow-err', viewBox: '0 0 10 10', refX: '9', refY: '5', markerWidth: '7',
      markerHeight: '7', orient: 'auto-start-reverse'
    }, N.el('path', { d: 'M 0 0 L 10 5 L 0 10 z', fill: '#f85149' })));
    svg.appendChild(defs);

    // edges (behind nodes)
    EDGES.forEach(function (e) {
      var geo = resolveEdge(e);
      var g = N.el('g', { class: 'edge', 'data-edge': e.id });
      g.appendChild(N.el('path', {
        class: 'edge-path',
        d: svgPathData(geo),
        'data-edge-path': e.id
      }));
      var lp = labelPoint(geo);
      g.appendChild(N.el('text', {
        class: 'edge-label', x: lp.x, y: lp.y - 5, 'data-edge-label': e.id
      }, e.label));
      svg.appendChild(g);
      edgeEls[e.id] = g;
    });

    // pulse dots layer (above edges, below node boxes)
    pulseLayer = N.el('g', { class: 'pulse-layer' });
    svg.appendChild(pulseLayer);

    // nodes
    NODE_IDS.forEach(function (id) {
      var l = LAYOUT[id];
      var meta = topologyMeta && topologyMeta.nodes ? topologyMeta.nodes[id] : null;
      var label = (meta && meta.label) || prettyNodeName(id);
      var isDb = l.type === 'db';
      var g = N.el('g', { class: 'node' + (isDb ? ' node-db' : ''), 'data-node': id, transform: 'translate(' + l.x + ',' + l.y + ')' });

      g.appendChild(N.el('rect', { class: 'box', width: BOX_W, height: BOX_H }));
      g.appendChild(N.el('circle', {
        class: 'status-dot status-ok', cx: 16, cy: 17, r: 4.5
      }));
      g.appendChild(N.el('text', { class: 'node-label', x: BOX_W / 2, y: 34 }, label));
      g.appendChild(N.el('text', { class: 'node-type', x: BOX_W / 2, y: 55 },
        isDb ? 'database' : 'service'));

      // per-node counter badge (top-right corner)
      var badge = N.el('g', { class: 'node-badge-wrap', transform: 'translate(' + (BOX_W - 14) + ',12)' });
      badge.appendChild(N.el('rect', { class: 'node-badge-bg', width: 20, height: 15 }));
      badge.appendChild(N.el('text', { class: 'node-badge', x: 10, y: 11.5, textAnchor: 'middle', 'data-badge': id }));
      badge.style.display = 'none';
      g.appendChild(badge);

      svg.appendChild(g);
      nodeEls[id] = g;
    });

    panel.appendChild(svg);
  }

  function prettyNodeName(id) {
    var map = {
      api: 'API',
      entity: 'EntityService',
      metrics: 'MetricsService',
      scheduler: 'Scheduler',
      poller: 'Poller',
      pluginpool: 'PluginWorkerPool',
      discoverypool: 'DiscoveryWorkerPool',
      discovery: 'DiscoveryService',
      health: 'HealthMonitor',
      db: 'PostgreSQL'
    };
    return map[id] || id;
  }

  function renderNodeBadges() {
    NODE_IDS.forEach(function (id) {
      var g = nodeEls[id];
      if (!g) return;
      var s = stats[id];
      var wrap = g.querySelector('.node-badge-wrap');
      var text = g.querySelector('[data-badge]');
      if (!wrap || !text) return;
      if (!s || !s.spanCount) {
        wrap.style.display = 'none';
        return;
      }
      wrap.style.display = '';
      text.textContent = String(s.spanCount);
      wrap.classList.toggle('has-err', s.errCount > 0);
    });
  }

  function setNodeStatus(id, status) {
    var g = nodeEls[id];
    if (!g) return;
    var dot = g.querySelector('.status-dot');
    g.classList.toggle('node-hot', status === 'busy');
    g.classList.toggle('node-err', status === 'err');
    if (dot) dot.setAttribute('class', 'status-dot ' + (status === 'err' ? 'status-err-dot' : status === 'busy' ? 'status-busy' : 'status-ok'));
  }

  /* ---------------- trace highlight ---------------- */

  var pulseLayer = null;
  var lastPulseTimer = null;
  var lastNodes = [];

  function clearHighlight() {
    Object.keys(edgeEls).forEach(function (id) {
      edgeEls[id].classList.remove('edge-hot', 'edge-err');
    });
    NODE_IDS.forEach(function (id) { setNodeStatus(id, 'ok'); });
    if (pulseLayer) pulseLayer.textContent = '';
    lastNodes = [];
  }

  function highlightTrace(trace) {
    clearHighlight();
    if (!trace) return;

    var nodes = traceNodePath(trace);
    var edges = traceEdgePath(nodes);
    lastNodes = nodes;

    if (hasError(trace)) {
      nodes.forEach(function (id) { setNodeStatus(id, 'err'); });
      edges.forEach(function (id) {
        if (edgeEls[id]) edgeEls[id].classList.add('edge-err');
      });
    } else {
      nodes.forEach(function (id) { setNodeStatus(id, 'busy'); });
      edges.forEach(function (id) {
        if (edgeEls[id]) edgeEls[id].classList.add('edge-hot');
      });
    }

    if (pulseLayer) {
      pulseLayer.textContent = '';
      animatePulse(nodes, edges, 0);
    }
  }

  // Animated pulse dot marching along the traversed path. Spans 600ms per hop.
  function animatePulse(nodes, edges, index) {
    if (!nodes.length || !edges.length) return;
    if (index >= nodes.length) {
      lastPulseTimer = setTimeout(function () {
        if (pulseLayer) pulseLayer.textContent = '';
      }, 800);
      return;
    }
    var id = nodes[index];
    var l = LAYOUT[id];
    var isErr = hasError(N.getSelectedTrace());
    if (l) {
      var cx = l.x + BOX_W / 2;
      var cy = l.y + BOX_H / 2;
      var c = document.createElementNS('http://www.w3.org/2000/svg', 'circle');
      c.setAttribute('cx', cx);
      c.setAttribute('cy', cy);
      c.setAttribute('r', 7);
      c.setAttribute('class', 'pulse-dot' + (isErr ? ' err' : ''));
      pulseLayer.appendChild(c);
      var anim = document.createElementNS('http://www.w3.org/2000/svg', 'animate');
      anim.setAttribute('attributeName', 'r');
      anim.setAttribute('values', '4;12;4');
      anim.setAttribute('dur', '0.6s');
      anim.setAttribute('repeatCount', '1');
      c.appendChild(anim);
      var fade = document.createElementNS('http://www.w3.org/2000/svg', 'animate');
      fade.setAttribute('attributeName', 'opacity');
      fade.setAttribute('values', '1;1;0.15');
      fade.setAttribute('dur', '0.6s');
      fade.setAttribute('repeatCount', '1');
      c.appendChild(fade);
    }
    lastPulseTimer = setTimeout(function () {
      if (pulseLayer) pulseLayer.textContent = '';
      animatePulse(nodes, edges, index + 1);
    }, 600);
  }

  function stopPulse() {
    if (lastPulseTimer) { clearTimeout(lastPulseTimer); lastPulseTimer = null; }
    if (pulseLayer) pulseLayer.textContent = '';
  }

  /* ---------------- side panel ---------------- */

  function renderSide() {
    var panel = document.getElementById('view-topology');
    if (sideRendered) return;
    sideRendered = true;

    var grid = document.createElement('div');
    grid.className = 'topo-grid';
    panel.textContent = '';

    var canvas = N.el('div', { class: 'panel topo-canvas' });
    canvas.appendChild(svg);
    grid.appendChild(canvas);

    var side = N.el('div', { class: 'panel topo-side' });
    var head = N.el('div', { class: 'panel-head' });
    head.appendChild(N.el('span', { class: 'panel-title' }, 'Trace stream'));
    head.appendChild(N.el('label', { class: 'live-toggle', title: 'Auto-follow the newest trace' },
      N.el('span', { text: 'Live' }),
      N.el('input', { type: 'checkbox', checked: true, id: 'topo-live' }),
      N.el('span', { class: 'switch' })));

    var body = N.el('div', { class: 'side-body' });
    var traceList = N.el('div', { class: 'trace-list', id: 'topo-trace-list' });
    var detail = N.el('div', { id: 'topo-detail' });
    body.appendChild(traceList);
    body.appendChild(detail);

    side.appendChild(head);
    side.appendChild(body);
    grid.appendChild(side);
    panel.appendChild(grid);
  }

  function emptyTraceList() {
    var list = document.getElementById('topo-trace-list');
    if (!list) return;
    list.textContent = '';
    list.appendChild(N.el('div', { class: 'empty' },
      N.el('div', { class: 'empty-icon' }, '◍'),
      N.el('div', null, 'No traces yet'),
      N.el('div', null, 'Poke the API explorer to generate traffic')));
  }

  function renderTraceList(list) {
    var box = document.getElementById('topo-trace-list');
    if (!box) return;
    box.textContent = '';
    if (!list.length) { emptyTraceList(); return; }

    list.forEach(function (t) {
      var info = N.statusInfo(t.status_code);
      var row = N.el('div', {
        class: 'trace-row' + (activeTraceId === t.trace_id ? ' is-active' : ''),
        'data-trace': t.trace_id,
        title: t.trace_id
      });
      row.appendChild(N.el('span', { class: 'tr-method ' + N.methodClass(t.method) }, t.method || ''));
      row.appendChild(N.el('span', { class: 'tr-path' }, t.path || ''));
      row.appendChild(N.el('span', { class: 'tr-time' }, N.fmtTime(t.started_at)));
      row.addEventListener('click', function () {
        N.selectTrace(t.trace_id, { view: 'topology' }).then(function (full) {
          if (full) markActive(full);
        });
      });
      box.appendChild(row);
    });
  }

  function markActive(trace) {
    activeTraceId = trace.trace_id;
    var rows = document.querySelectorAll('#topo-trace-list .trace-row');
    rows.forEach(function (r) {
      r.classList.toggle('is-active', r.getAttribute('data-trace') === trace.trace_id);
    });
    renderDetail(trace);
  }

  function renderDetail(trace) {
    var box = document.getElementById('topo-detail');
    if (!box) return;
    box.textContent = '';

    if (!trace) {
      box.appendChild(N.el('div', { class: 'empty' },
        N.el('div', { class: 'empty-icon' }, '▣'),
        N.el('div', null, 'Select a trace to see the path it traversed')));
      return;
    }

    var info = N.statusInfo(trace.status_code);
    var spans = trace.spans || [];
    var nodes = traceNodePath(trace);
    var comps = trace.component_ids || spans.map(function (s) { return s.component; });

    box.appendChild(N.el('div', { class: 'side-section-title' }, 'Trace'));
    box.appendChild(N.el('div', { class: 'summary-line' },
      N.el('span', { class: 'summary-key' }, 'Method'),
      N.el('span', { class: 'badge ' + N.methodClass(trace.method) }, trace.method || '')));
    box.appendChild(N.el('div', { class: 'summary-line' },
      N.el('span', { class: 'summary-key' }, 'Path'),
      N.el('span', { class: 'summary-val' }, trace.path || '–')));
    box.appendChild(N.el('div', { class: 'summary-line' },
      N.el('span', { class: 'summary-key' }, 'Status'),
      N.el('span', { class: 'badge ' + info.cls }, info.label)));
    box.appendChild(N.el('div', { class: 'summary-line' },
      N.el('span', { class: 'summary-key' }, 'Duration'),
      N.el('span', { class: 'summary-val' }, N.fmtDuration(trace.duration_ms))));
    box.appendChild(N.el('div', { class: 'summary-line' },
      N.el('span', { class: 'summary-key' }, 'Spans'),
      N.el('span', { class: 'summary-val' }, String(spans.length || trace.span_count || 0))));

    if (comps && comps.length) {
      box.appendChild(N.el('div', { class: 'side-section-title' }, 'Path'));
      var chips = N.el('div', { class: 'trace-path-chips' });
      comps.forEach(function (c, i) {
        var nid = N.nodeIdForComponent(c);
        var label = nid && topologyMeta && topologyMeta.nodes && topologyMeta.nodes[nid]
          ? topologyMeta.nodes[nid].label : String(c);
        chips.appendChild(N.el('span', { class: 'chip c-' + (nid || 'other') }, label));
        if (i < comps.length - 1) chips.appendChild(N.el('span', { class: 'path-arrow' }, '→'));
      });
      box.appendChild(chips);
    }

    box.appendChild(N.el('div', { class: 'side-section-title' }, 'Spans'));
    if (spans.length) {
      var sl = N.el('div', { class: 'span-list' });
      spans.forEach(function (s) {
        var row = N.el('div', { class: 'span-row' });
        row.appendChild(N.el('span', { class: 'sp-name' }, s.name || '(span)' ));
        row.appendChild(N.el('span', { class: 'sp-right' },
          N.el('span', { class: 'chip c-' + (N.nodeIdForComponent(s.component) || 'other') }, s.component || '?'),
          N.el('span', null, N.fmtDuration(s.duration_ms))));
        row.addEventListener('click', function () {
          N.showView('traces');
          var wfRow = document.querySelector('.wf-row[data-span="' + s.span_id + '"]');
          if (wfRow) {
            wfRow.scrollIntoView({ behavior: 'smooth', block: 'center' });
            wfRow.classList.add('is-open');
            var events = wfRow.nextElementSibling;
            if (events && events.classList.contains('wf-events')) events.style.display = 'block';
          }
        });
        sl.appendChild(row);
      });
      box.appendChild(sl);
    }
  }

  function renderCountersLegend() {
    var canvas = document.getElementById('view-topology').querySelector('.topo-canvas');
    if (!canvas || canvas.querySelector('.counter-legend')) return;
    canvas.appendChild(N.el('div', { class: 'counter-legend' },
      N.el('span', { class: 'legend-hint' }, 'badge = spans touching this component (last 20 traces)')));
  }

  /* ---------------- live behavior ---------------- */

  var tracePoll = null;
  var liveFollow = true;

  function onTracesUpdated(ev) {
    var list = ev.list;
    if (sideRendered) renderTraceList(list);
    updateCounters(list);

    // Auto-select the newest trace if live-follow is on and nothing has been
    // manually selected yet, or a genuinely new trace arrived.
    var newId = ev.newIds && ev.newIds.length ? ev.newIds[0] : null;
    var cur = N.getSelectedTrace();
    var shouldFollow = liveFollow && (newId || !cur) && list.length;
    if (shouldFollow) {
      N.selectTrace(newId || list[0].trace_id).then(function (full) {
        if (full) markActive(full);
      });
    } else if (cur) {
      markActive(cur);
    }
  }

  function onTraceSelected(trace) {
    if (activeTraceId !== (trace && trace.trace_id)) {
      if (trace) markActive(trace);
    }
    highlightTrace(trace);
  }

  function onView() {
    if (N.getCurrentView() !== 'topology') return;
    // Re-render the side panel if it was torn down by an auth cycle.
    if (!sideRendered && svg) renderSide();
    if (sideRendered) {
      renderTraceList(N.cache.traces);
      updateCounters(N.cache.traces);
      var cur = N.getSelectedTrace();
      if (cur) { markActive(cur); highlightTrace(cur); }
      else renderDetail(null);
    }
  }

  /* ---------------- topology data ---------------- */

  function loadTopology() {
    return N.api('/api/v1/topology').then(function (r) {
      if (!r.ok || !r.json) return null;
      var nodes = {};
      (r.json.nodes || []).forEach(function (n) { nodes[n.id] = n; });
      topologyMeta = { nodes: nodes, edges: r.json.edges || [] };
      if (svg) {
        // refresh labels from server data (ids are authoritative)
        NODE_IDS.forEach(function (id) {
          var meta = nodes[id];
          if (meta && nodeEls[id]) {
            var t = nodeEls[id].querySelector('.node-label');
            if (t) t.textContent = meta.label || prettyNodeName(id);
          }
        });
      }
      return topologyMeta;
    });
  }

  /* ---------------- boot ---------------- */

  N.on('auth:login', function () {
    buildSvg();
    renderSide();
    renderCountersLegend();
    renderDetail(null);
    renderTraceList([]);

    loadTopology();
    N.refreshTraces(20);

    var liveInput = document.getElementById('topo-live');
    if (liveInput) {
      liveInput.addEventListener('change', function () {
        liveFollow = liveInput.checked;
        if (liveFollow && N.cache.traces.length) {
          var cur = N.getSelectedTrace();
          N.selectTrace(cur ? cur.trace_id : N.cache.lastTraceId || N.cache.traces[0].trace_id)
            .then(function (full) { if (full) markActive(full); });
        }
      });
    }

    if (!tracePoll) {
      tracePoll = N.poll(function () {
        return N.refreshTraces(20).then(function (r) {
          N.setLive(r.ok ? 'ok' : 'err');
          return null;
        });
      }, 2000);
    } else {
      tracePoll.restart();
    }
  });

  N.on('auth:logout', function () {
    stopPulse();
    activeTraceId = null;
    sideRendered = false;
    var panel = document.getElementById('view-topology');
    if (panel) panel.textContent = '';
    if (tracePoll) { tracePoll.stop(); tracePoll = null; }
  });

  N.on('traces:updated', onTracesUpdated);
  N.on('trace:selected', onTraceSelected);
  N.on('view:topology', onView);
})();

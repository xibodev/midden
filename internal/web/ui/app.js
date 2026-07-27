'use strict';

const $ = (s) => document.querySelector(s);
const el = (tag, cls, text) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

const get = async (path) => {
  const r = await fetch(path);
  if (!r.ok) throw new Error(await r.text());
  return r.json();
};

function bytes(n) {
  if (!n) return '0 B';
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${u[i]}`;
}

function riskPill(risk) {
  if (risk === 'ok') return null;
  const cls = risk === 'critical' ? 'crit' : risk === 'warn' ? 'warn' : '';
  return el('span', `pill ${cls}`, risk);
}

// ---- navigation -------------------------------------------------------------

const loaders = {};

document.querySelectorAll('.tab').forEach((tab) => {
  tab.addEventListener('click', () => {
    document.querySelectorAll('.tab').forEach((t) => t.classList.remove('active'));
    document.querySelectorAll('.view').forEach((v) => v.classList.remove('active'));
    tab.classList.add('active');
    const id = tab.dataset.view;
    $('#' + id).classList.add('active');
    if (loaders[id]) loaders[id]();
  });
});

// ---- drawer -----------------------------------------------------------------

$('#drawer-close').addEventListener('click', () => { $('#drawer').hidden = true; });
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') $('#drawer').hidden = true;
});

async function openSession(id) {
  const drawer = $('#drawer');
  const body = $('#drawer-body');
  body.replaceChildren(el('p', 'note', 'loading...'));
  drawer.hidden = false;

  try {
    const d = await get('/api/session?id=' + encodeURIComponent(id));
    const s = d.session;
    body.replaceChildren();

    body.append(el('h2', null, s.title));
    const meta = el('p', 'meta');
    meta.append(el('span', 'tool ' + s.tool, s.tool), document.createTextNode('  ' + s.id));
    body.append(meta);
    body.append(el('p', 'meta', s.dir));

    const facts = el('table');
    const addRow = (k, v) => {
      const tr = el('tr');
      tr.append(el('td', 'meta', k), el('td', null, v));
      facts.append(tr);
    };
    addRow('last touched', s.age + ' ago');
    if (s.span_days >= 1) addRow('span', s.span_days + ' days');
    if (s.turns) addRow('turns', String(s.turns));
    if (s.bytes) addRow('transcript', bytes(s.bytes) + ' (' + s.risk + ')');
    if (s.live) addRow('status', 'open right now — do not resume');
    if (!s.dir_exists) addRow('workspace', 'MISSING');
    body.append(facts);

    body.append(el('h2', null, 'Resume'));
    body.append(el('pre', 'mono', s.resume));

    if (d.harvest) {
      const h = d.harvest;
      if (h.goal) {
        body.append(el('h2', null, 'Original goal'));
        body.append(el('pre', null, h.goal.text));
      }
      if (h.recent && h.recent.length) {
        body.append(el('h2', null, 'Recent exchanges'));
        h.recent.forEach((t) => {
          const d2 = el('div', 'turn ' + (t.role === 'user' ? 'user' : ''));
          d2.append(el('div', 'who', t.role));
          d2.append(el('div', null, t.text.slice(0, 1400)));
          body.append(d2);
        });
      }
    }
  } catch (e) {
    body.replaceChildren(el('p', 'note', 'failed: ' + e.message));
  }
}

// ---- overview ---------------------------------------------------------------

loaders.overview = async () => {
  const cards = $('#stat-cards');
  cards.replaceChildren();

  let h;
  try {
    h = await get('/api/health');
  } catch (e) {
    cards.append(el('p', 'note', 'failed to load: ' + e.message));
    return;
  }

  const card = (k, v, n) => {
    const c = el('div', 'card');
    c.append(el('div', 'k', k), el('div', 'v', v));
    if (n) c.append(el('div', 'n', n));
    return c;
  };

  const tools = Object.entries(h.by_tool || {})
    .map(([t, n]) => `${t} ${n}`).join(' · ');
  const nugTotal = Object.values(h.nuggets || {}).reduce((a, b) => a + b, 0);

  cards.append(
    card('on disk', bytes(h.footprint), tools),
    card('sessions', h.sessions, (h.dead_workspaces || 0) + ' dead workspaces'),
    card('open now', h.live, 'live terminals'),
    card('at risk', (h.at_risk || []).length, 'may fail to resume'),
    card('nuggets', nugTotal, 'reclaimed')
  );

  const riskPanel = $('#risk-panel');
  const riskList = $('#risk-list');
  riskList.replaceChildren();
  if ((h.at_risk || []).length) {
    riskPanel.hidden = false;
    h.at_risk.forEach((s) => riskList.append(sessionRow(s)));
  } else {
    riskPanel.hidden = true;
  }

  // assay
  const bars = $('#assay-bars');
  try {
    const a = await get('/api/assay');
    if (!a.assayed) return;
    bars.replaceChildren();

    const total = a.bytes || 1;
    const bar = el('div', 'bar');
    ['signal', 'exhaust', 'artifact', 'bookkeeping'].forEach((k) => {
      const s = el('span', k);
      s.style.width = (100 * (a[k] || 0) / total) + '%';
      bar.append(s);
    });
    bars.append(bar);

    const legend = el('div', 'legend');
    [['signal', '#6ee7b7'], ['exhaust', '#48536b'], ['artifact', '#58c4dd'], ['bookkeeping', '#333c4d']]
      .forEach(([k, c]) => {
        const item = el('span', null);
        const i = el('i');
        i.style.background = c;
        item.append(i, document.createTextNode(
          `${k} ${bytes(a[k] || 0)} (${(100 * (a[k] || 0) / total).toFixed(1)}%)`));
        legend.append(item);
      });
    bars.append(legend);

    bars.append(el('p', 'note',
      `${bytes(a.reclaimable)} reclaimable · ${a.compression}x compression · ` +
      `${a.images} images in ${a.image_clusters} clusters · ${a.assayed} of ${a.sessions} sessions assayed`));
  } catch (e) { /* index not built yet */ }
};

// ---- sessions ---------------------------------------------------------------

function sessionRow(s) {
  const row = el('div', 'row');
  row.addEventListener('click', () => openSession(s.id));

  const top = el('div', 'top');
  top.append(el('span', 'tool ' + s.tool, s.tool));
  top.append(el('span', 'title', s.title));
  const r = riskPill(s.risk);
  if (r) top.append(r);
  if (s.live) top.append(el('span', 'pill live', 'open'));
  if (!s.dir_exists) top.append(el('span', 'pill crit', 'dir missing'));
  if (s.span_days >= 2) top.append(el('span', 'pill', s.span_days.toFixed(0) + 'd span'));
  top.append(el('span', 'meta', s.age + ' ago'));
  if (s.bytes) top.append(el('span', 'meta', bytes(s.bytes)));
  else if (s.turns) top.append(el('span', 'meta', s.turns + ' turns'));
  row.append(top);

  row.append(el('div', 'dir', s.dir));
  row.append(el('div', 'cmd mono', s.resume));
  return row;
}

let allSessions = [];

async function loadSessions() {
  const list = $('#session-list');
  list.replaceChildren(el('p', 'note', 'loading...'));

  const p = new URLSearchParams();
  if ($('#f-tool').value) p.set('tool', $('#f-tool').value);
  if ($('#f-days').value) p.set('days', $('#f-days').value);
  if ($('#f-all').checked) p.set('all', '1');
  p.set('limit', '400');

  try {
    allSessions = await get('/api/sessions?' + p.toString());
    renderSessions();
  } catch (e) {
    list.replaceChildren(el('p', 'note', 'failed: ' + e.message));
  }
}

function renderSessions() {
  const list = $('#session-list');
  const q = $('#f-search').value.toLowerCase();
  const rows = allSessions.filter((s) =>
    !q || (s.title + ' ' + s.dir).toLowerCase().includes(q));

  list.replaceChildren();
  if (!rows.length) {
    list.append(el('p', 'empty', 'no sessions match'));
    return;
  }
  rows.forEach((s) => list.append(sessionRow(s)));
}

loaders.sessions = loadSessions;
['#f-tool', '#f-days', '#f-all'].forEach((s) =>
  $(s).addEventListener('change', loadSessions));
$('#f-search').addEventListener('input', renderSessions);

// ---- nuggets ----------------------------------------------------------------

async function loadNuggets() {
  const list = $('#nugget-list');
  list.replaceChildren(el('p', 'note', 'loading...'));

  const p = new URLSearchParams();
  if ($('#n-kind').value) p.set('kind', $('#n-kind').value);
  if ($('#n-search').value) p.set('search', $('#n-search').value);

  try {
    const ns = await get('/api/nuggets?' + p.toString());
    list.replaceChildren();
    if (!ns || !ns.length) {
      list.append(el('p', 'empty', 'no nuggets yet — run: midden reclaim --days 7'));
      return;
    }
    ns.forEach((n) => {
      const d = el('div', 'nugget');
      d.append(el('h3', null, n.title));
      d.append(el('p', null, n.body));
      const m = el('div', 'meta');
      m.append(el('span', 'pill', n.kind));
      m.append(document.createTextNode(
        ` ${Math.round((n.confidence || 0) * 100)}% · ${n.session_id.slice(0, 8)} · ${n.workspace || ''}`));
      if (n.redacted) m.append(el('span', 'pill warn', 'redacted'));
      d.append(m);
      list.append(d);
    });
  } catch (e) {
    list.replaceChildren(el('p', 'note', 'failed: ' + e.message));
  }
}

loaders.nuggets = loadNuggets;
$('#n-kind').addEventListener('change', loadNuggets);
$('#n-search').addEventListener('input', () => {
  clearTimeout(window.__nt);
  window.__nt = setTimeout(loadNuggets, 220);
});

// ---- artifacts --------------------------------------------------------------

loaders.artifacts = async () => {
  const list = $('#artifact-list');
  list.replaceChildren(el('p', 'note', 'loading...'));
  try {
    const as = await get('/api/artifacts');
    list.replaceChildren();
    if (!as || !as.length) {
      list.append(el('p', 'empty', 'nothing generated yet — run: midden catalog'));
      return;
    }
    as.forEach((a) => {
      const d = el('div', 'row');
      const top = el('div', 'top');
      top.append(el('span', 'pill', a.kind));
      top.append(el('span', 'title', a.title));
      top.append(el('span', 'meta', new Date(a.created_at).toLocaleString()));
      d.append(top);
      d.append(el('div', 'dir', a.path));
      d.append(el('div', 'meta', `${(a.nugget_ids || []).length} nuggets · ${a.model}`));
      list.append(d);
    });
  } catch (e) {
    list.replaceChildren(el('p', 'note', 'failed: ' + e.message));
  }
};

// ---- operation log ----------------------------------------------------------

loaders.ops = async () => {
  const list = $('#ops-list');
  list.replaceChildren(el('p', 'note', 'loading...'));
  try {
    const ops = await get('/api/ops');
    list.replaceChildren();
    if (!ops || !ops.length) {
      list.append(el('p', 'empty', 'nothing has been modified'));
      return;
    }
    const t = el('table');
    const head = el('tr');
    ['when', 'op', 'tool', 'session', 'before', 'after', 'ok'].forEach((h) =>
      head.append(el('th', null, h)));
    t.append(head);
    ops.forEach((o) => {
      const tr = el('tr');
      tr.append(
        el('td', 'meta', new Date(o.created_at).toLocaleString()),
        el('td', null, o.op),
        el('td', 'tool ' + o.tool, o.tool),
        el('td', 'mono', (o.session_id || '').slice(0, 8)),
        el('td', null, bytes(o.before)),
        el('td', null, bytes(o.after)),
        el('td', null, o.ok ? 'ok' : 'FAILED'));
      t.append(tr);
    });
    list.append(t);
  } catch (e) {
    list.replaceChildren(el('p', 'note', 'failed: ' + e.message));
  }
};

loaders.overview();

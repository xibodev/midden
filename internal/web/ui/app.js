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
  if (!r.ok) throw new Error((await r.text()) || r.statusText);
  return r.json();
};

const post = async (path, body) => {
  const r = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) throw new Error((await r.text()) || r.statusText);
  return r.json();
};

function bytes(n) {
  if (!n) return '0 B';
  const u = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i === 0 ? 0 : 1)} ${u[i]}`;
}

// indexAge says how old the indexed view is. The page is served from an index,
// not from a live read of the stores; hiding that is how a dashboard starts
// showing yesterday's world as though it were now.
function indexAge(iso) {
  if (!iso) return 'not yet scanned';
  const mins = (Date.now() - new Date(iso).getTime()) / 60000;
  if (!isFinite(mins) || mins < 0) return '';
  if (mins < 2) return 'indexed just now';
  if (mins < 90) return `indexed ${Math.round(mins)}m ago`;
  const hrs = mins / 60;
  if (hrs < 36) return `indexed ${Math.round(hrs)}h ago`;
  return `indexed ${Math.round(hrs / 24)}d ago \u2014 run a scan`;
}

function tok(n) {
  if (!n) return '0';
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return Math.round(n / 1e3) + 'k';
  return String(n);
}

function toast(msg, kind) {
  const t = $('#toast');
  t.textContent = msg;
  t.className = 'toast' + (kind ? ' ' + kind : '');
  t.hidden = false;
  clearTimeout(window.__toast);
  window.__toast = setTimeout(() => { t.hidden = true; }, 3200);
}

// A copy button beside every command, because selecting a long shell line by
// hand is exactly the friction this tool exists to remove.
function copyBtn(text, label) {
  const b = el('button', 'copy', label || 'copy');
  b.addEventListener('click', (e) => {
    e.stopPropagation();
    navigator.clipboard.writeText(text).then(
      () => { b.textContent = 'copied'; toast('Copied to clipboard'); setTimeout(() => { b.textContent = label || 'copy'; }, 1400); },
      () => toast('Copy failed', 'bad')
    );
  });
  return b;
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

function show(view) {
  document.querySelector(`.tab[data-view="${view}"]`).click();
}

// ---- modal ------------------------------------------------------------------

function openModal(build) {
  const box = $('#modal-body');
  box.replaceChildren();
  build(box);
  $('#modal').hidden = false;
}
$('#modal-close').addEventListener('click', () => { $('#modal').hidden = true; });
$('#modal').addEventListener('click', (e) => {
  if (e.target.id === 'modal') $('#modal').hidden = true;
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape') { $('#modal').hidden = true; $('#drawer').hidden = true; }
});

// ---- DO: the action surface -------------------------------------------------

const ACTIONS = [
  {
    op: 'reclaim', title: 'Mine sessions for knowledge', costly: true,
    blurb: 'Extract decisions, fixes, gotchas and dead ends as reusable nuggets.',
    build: scopeForm,
  },
  {
    op: 'refine', title: 'Write something from what I know', costly: true,
    blurb: 'Turn nuggets into a tutorial, FAQ, ADR, troubleshooting guide or post.',
    build: refineForm,
  },
  {
    op: 'prune', title: 'Recover disk space', costly: false,
    blurb: 'Replace bulky tool payloads with markers. Every record is preserved and verified.',
    build: scopeForm,
  },
  {
    op: 'archive', title: 'Archive old transcripts', costly: false,
    blurb: 'Move transcripts out of the tool\u2019s active path, keeping a manifest.',
    build: scopeForm,
  },
];

loaders.do = async () => {
  const wrap = $('#action-cards');
  wrap.replaceChildren();
  ACTIONS.forEach((a) => {
    const c = el('div', 'action reveal');
    c.append(el('h3', null, a.title));
    c.append(el('p', null, a.blurb));
    const foot = el('div', 'foot');
    foot.append(el('span', 'pill' + (a.costly ? ' warn' : ''), a.costly ? 'SPENDS' : 'FREE'));
    const go = el('button', 'btn primary', 'Start');
    const open = () => openModal((box) => a.build(box, a));
    go.addEventListener('click', open);
    foot.append(go);
    c.append(foot);

    // The card looks like the target, so it should be one. Only the small
    // button responded, which meant the obvious click did nothing at all.
    c.tabIndex = 0;
    c.setAttribute('role', 'button');
    c.setAttribute('aria-label', a.title);
    c.addEventListener('click', (e) => { if (e.target !== go) open(); });
    c.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); open(); }
    });

    wrap.append(c);
  });

  refreshJobs();
  loadGuidance();
};

// Guidance is computed from real state, so the first thing you see is what
// actually matters today rather than a fixed menu.
async function loadGuidance() {
  let d;
  try { d = await get('/api/next'); } catch { return; }

  const cheap = $('#cheap-list');
  cheap.replaceChildren();
  (d.cheapest || []).forEach((t) => cheap.append(el('li', null, t)));

  const panel = $('#next-panel');
  const list = $('#next-list');
  const steps = d.steps || [];
  if (!steps.length) { panel.hidden = true; return; }

  panel.hidden = false;
  list.replaceChildren();

  steps.slice(0, 4).forEach((s, i) => {
    const row = el('div', 'next reveal');
    row.style.animationDelay = (i * 60) + 'ms';

    const top = el('div', 'top');
    top.append(el('span', 'pill' + (s.cost === 'SPENDS' ? ' warn' : ''), s.cost));
    top.append(el('span', 'title', s.why));
    row.append(top);

    const cmd = el('div', 'cmd mono');
    cmd.append(el('span', null, s.command));
    cmd.append(copyBtn(s.command));
    row.append(cmd);
    row.append(el('div', 'meta', s.value));

    // Actions the UI can perform directly get a button; the rest are
    // copy-and-run, which is honest rather than pretending everything is
    // clickable.
    const briefMatch = /^midden brief (\S+)/.exec(s.command);
    if (briefMatch) {
      const b = el('button', 'btn', 'Rescue it here');
      b.addEventListener('click', () => openModal((box) => briefForm(box, briefMatch[1])));
      row.append(b);
    } else {
      const opMatch = /^midden (prune|archive|reclaim|refine|catalog)\b/.exec(s.command);
      if (opMatch && ACTIONS.some((a) => a.op === opMatch[1])) {
        const act = ACTIONS.find((a) => a.op === opMatch[1]);
        const b = el('button', 'btn', 'Do it here');
        b.addEventListener('click', () => openModal((box) => act.build(box, act)));
        row.append(b);
      }
    }
    list.append(row);
  });
}

// briefForm rescues a session that is past the resume cliff.
//
// This is the action the tool ranks above every other, and it used to be the
// only one you could not perform here — the most urgent thing in the product
// was a string to copy into a terminal. It is free, read-only and
// deterministic, so it runs on open rather than making you ask twice.
function briefForm(box, sessionID) {
  box.append(costHeading({ costly: false, title: 'Rescue this session' }));
  box.append(el('p', 'note',
    'Reads the transcript and writes a paste-able prompt that carries the work '
    + 'into a fresh session. Nothing is modified, and no model is called.'));

  const out = el('div', 'result');
  out.append(el('p', 'note', 'reading transcript...'));
  box.append(out);

  post('/api/action', { op: 'brief', session_id: sessionID, apply: false })
    .then((job) => pollJob(job.id, (j) => {
      out.replaceChildren(el('p', 'note', j.progress || 'working...'));
    }))
    .then((done) => {
      const r = (done && done.result) || {};
      if (!r.body) throw new Error(done && done.error ? done.error : 'no brief produced');

      out.replaceChildren();
      out.append(el('p', 'note',
        `${r.user_turns} user turn(s) recovered from ${r.records} records \u00b7 ${r.dir || ''}`));
      if (r.warning) out.append(el('p', 'warn-note', r.warning));

      const pre = el('pre', 'brief-body mono');
      pre.textContent = r.body;
      out.append(pre);

      const bar = el('div', 'modal-foot');
      // Say where it was kept. A rescue you cannot find again is one you
      // have to redo.
      bar.append(el('span', 'foot-note', r.saved
        ? 'Saved \u2014 also on the Artifacts tab. Paste it into a new session in that workspace.'
        : 'Paste this into a new session in that workspace to continue the work.'));
      const copy = el('button', 'btn primary', 'Copy handoff');
      copy.addEventListener('click', async () => {
        await navigator.clipboard.writeText(r.body);
        toast('Handoff copied \u2014 paste it into a fresh session');
      });
      bar.append(copy);
      out.append(bar);
    })
    .catch((e) => out.replaceChildren(el('p', 'bad', e.message)));
}

// costHeading puts the cost class on the dialog itself.
//
// The badge was on the card behind the dialog and nowhere inside it, so the
// one fact worth knowing disappeared at the exact moment of committing, and a
// free action and a paid one looked identical.
function costHeading(action) {
  const h = el('h2');
  h.append(el('span', 'pill' + (action.costly ? ' warn' : ''), action.costly ? 'SPENDS' : 'FREE'));
  h.append(el('span', 'h2-text', action.title));
  return h;
}

function field(label, node) {
  const w = el('label', 'field');
  w.append(el('span', null, label));
  w.append(node);
  return w;
}

function scopeForm(box, action) {
  box.append(costHeading(action));
  box.append(el('p', 'note', action.blurb));

  const ws = el('input');
  ws.type = 'text';
  ws.placeholder = 'e.g. orvantix (blank = all)';

  const days = el('select');
  [['', 'any time'], ['7', 'last 7 days'], ['14', 'last 14 days'], ['30', 'last 30 days']]
    .forEach(([v, t]) => { const o = el('option', null, t); o.value = v; days.append(o); });
  days.value = action.op === 'reclaim' ? '7' : '';

  const tool = el('select');
  [['', 'all tools'], ['copilot', 'copilot'], ['claude', 'claude'], ['opencode', 'opencode']]
    .forEach(([v, t]) => { const o = el('option', null, t); o.value = v; tool.append(o); });

  box.append(field('Workspace contains', ws));
  box.append(field('Time range', days));
  box.append(field('Tool', tool));

  const out = el('div', 'result');
  box.append(out);

  const bar = el('div', 'modal-foot');
  const preview = el('button', 'btn primary', action.costly ? 'Estimate cost' : 'Preview');
  const run = el('button', 'btn', 'Run');
  run.disabled = true;
  // Run is gated behind a preview, which is the safest thing this dialog
  // does — but a greyed button with no explanation reads as broken rather
  // than as protection, so say what unlocks it.
  run.title = action.costly
    ? 'Estimate the cost first — the estimate is free'
    : 'Preview first — nothing is written until you do';
  bar.append(el('span', 'foot-note', action.costly
    ? 'The estimate is free. Nothing is charged until you press Run.'
    : 'Preview is free and writes nothing. Nothing changes until you press Run.'));
  bar.append(preview, run);
  box.append(bar);

  const req = () => ({
    op: action.op,
    workspace: ws.value.trim(),
    days: parseInt(days.value || '0', 10),
    tool: tool.value,
  });

  preview.addEventListener('click', async () => {
    out.replaceChildren(el('p', 'note', 'checking...'));
    try {
      const job = await post('/api/action', { ...req(), apply: false });
      const done = await pollJob(job.id, (j) => {
        out.replaceChildren(el('p', 'note', j.progress || 'working...'));
      });
      renderPreview(out, done, action);
      // The preview has been seen, so Run becomes the primary action and the
      // preview steps back. Emphasis follows what is now safe to do.
      run.disabled = false;
      run.className = 'btn primary';
      preview.className = 'btn';
    } catch (e) {
      out.replaceChildren(el('p', 'bad', e.message));
    }
  });

  run.addEventListener('click', async () => {
    if (action.op === 'archive' && !confirm('Archive moves transcripts out of the tool\u2019s path. Continue?')) return;
    run.disabled = true;
    try {
      const job = await post('/api/action', { ...req(), apply: true, confirm: true });
      $('#modal').hidden = true;
      show('do');
      trackJob(job.id);
      toast('Started \u2014 progress below');
    } catch (e) {
      out.replaceChildren(el('p', 'bad', e.message));
      run.disabled = false;
    }
  });
}

function refineForm(box, action) {
  box.append(el('h2', null, action.title));

  const ws = el('input');
  ws.type = 'text';
  ws.placeholder = 'workspace (blank = all evidence)';
  box.append(field('Evidence from', ws));

  const list = el('div', 'template-list');
  box.append(el('p', 'note', 'loading what your evidence supports...'));
  box.append(list);

  const out = el('div', 'result');
  box.append(out);

  const bar = el('div', 'modal-foot');
  const preview = el('button', 'btn', 'Estimate cost');
  const run = el('button', 'btn primary', 'Write');
  run.disabled = true;
  bar.append(preview, run);
  box.append(bar);

  const chosen = new Set();
  get('/api/templates').then((d) => {
    list.replaceChildren();
    if (!d.nuggets) {
      list.append(el('p', 'bad', 'No nuggets yet \u2014 run "Mine sessions" first.'));
      return;
    }
    d.templates.forEach((t) => {
      const row = el('label', 'tpl' + (t.supported ? '' : ' unsupported'));
      const cb = el('input');
      cb.type = 'checkbox';
      cb.disabled = !t.supported;
      cb.addEventListener('change', () => {
        cb.checked ? chosen.add(t.name) : chosen.delete(t.name);
      });
      row.append(cb);
      const txt = el('div');
      txt.append(el('strong', null, t.title));
      txt.append(el('div', 'meta', t.supported ? t.why : 'not enough evidence yet'));
      row.append(txt);
      list.append(row);
    });
  });

  const req = () => ({
    op: 'refine', workspace: ws.value.trim(), templates: [...chosen],
  });

  preview.addEventListener('click', async () => {
    if (!chosen.size) { out.replaceChildren(el('p', 'bad', 'Choose at least one.')); return; }
    out.replaceChildren(el('p', 'note', 'estimating...'));
    try {
      const job = await post('/api/action', { ...req(), apply: false });
      const done = await pollJob(job.id);
      renderPreview(out, done, action);
      run.disabled = false;
    } catch (e) {
      out.replaceChildren(el('p', 'bad', e.message));
    }
  });

  run.addEventListener('click', async () => {
    run.disabled = true;
    try {
      const job = await post('/api/action', { ...req(), apply: true });
      $('#modal').hidden = true;
      show('do');
      trackJob(job.id);
      toast('Writing \u2014 progress below');
    } catch (e) {
      out.replaceChildren(el('p', 'bad', e.message));
      run.disabled = false;
    }
  });
}

function renderPreview(out, job, action) {
  out.replaceChildren();
  if (job.status === 'failed') {
    out.append(el('p', 'bad', job.error));
    return;
  }
  const r = job.result || {};

  if (r.estimate_text) {
    const p = el('div', 'estimate');
    p.append(el('div', 'k', 'Estimated cost'));
    p.append(el('div', 'v', r.estimate_text));
    if (r.sessions) p.append(el('div', 'meta', `${r.sessions} session(s) in scope`));
    if (r.artifacts) p.append(el('div', 'meta', `${r.artifacts} artifact(s) from ${r.nuggets} nuggets`));
    out.append(p);
    out.append(el('p', 'note', 'Costs your existing CLI seat \u2014 no API key, no separate bill.'));
    return;
  }

  if (r.rows) {
    out.append(el('p', null,
      `${r.rows.length} transcript(s) \u2014 about ${bytes(r.total_saved)} recoverable.`));
    const t = el('table');
    r.rows.slice(0, 8).forEach((row) => {
      const tr = el('tr');
      tr.append(el('td', null, row.session.title.slice(0, 34)));
      tr.append(el('td', 'meta', bytes(row.before)));
      tr.append(el('td', null, '\u2192 ' + bytes(row.after)));
      tr.append(el('td', 'good', bytes(row.saved)));
      t.append(tr);
    });
    out.append(t);
    out.append(el('p', 'note', 'Originals are never modified. Pruned copies are written separately and verified.'));
  }
}

// ---- jobs -------------------------------------------------------------------

const tracked = new Set();

function trackJob(id) {
  tracked.add(id);
  refreshJobs();
  pollJob(id, null, true);
}

async function pollJob(id, onProgress, redraw) {
  for (;;) {
    const j = await get('/api/job-status?id=' + encodeURIComponent(id))
      .catch(() => get('/api/jobs?id=' + encodeURIComponent(id)));
    if (onProgress) onProgress(j);
    if (redraw) refreshJobs();
    if (j.status === 'done' || j.status === 'failed') {
      if (redraw) {
        toast(j.status === 'done' ? 'Finished' : 'Failed: ' + j.error,
              j.status === 'done' ? 'good' : 'bad');
      }
      return j;
    }
    await new Promise((r) => setTimeout(r, 1200));
  }
}

async function refreshJobs() {
  let jobs = [];
  try { jobs = await get('/api/jobs'); } catch { return; }

  const panel = $('#job-panel');
  const list = $('#job-list');
  const active = jobs.filter((j) => j.status === 'running' || j.status === 'queued' || tracked.has(j.id));
  if (!active.length) { panel.hidden = true; return; }

  panel.hidden = false;
  list.replaceChildren();
  active.slice(0, 8).forEach((j) => {
    const d = el('div', 'job ' + j.status);
    const top = el('div', 'top');
    top.append(el('span', 'pill', j.op));
    top.append(el('span', 'title', j.scope));
    top.append(el('span', 'meta', j.status));
    d.append(top);
    if (j.progress) d.append(el('div', 'meta', j.progress));
    if (j.error) d.append(el('div', 'bad', j.error));

    if (j.cost && j.cost.usage) {
      const u = j.cost.usage;
      const charge = u.aiu ? u.aiu.toFixed(1) + ' AIU'
                   : u.usd ? '$' + u.usd.toFixed(2)
                   : tok(u.input_tokens + u.output_tokens + u.cache_read_tokens + u.cache_write_tokens) + ' tok';
      d.append(el('div', 'cost-line',
        `cost ${charge} \u00b7 ${j.cost.items} item(s) \u00b7 ${Math.round((u.duration_ms || 0) / 1000)}s`));
    }
    if (j.result && j.result.count) d.append(el('div', 'good', `${j.result.count} nugget(s) stored`));
    if (j.result && j.result.written) d.append(el('div', 'good', `${j.result.written} artifact(s) written`));
    if (j.result && j.result.total_saved) d.append(el('div', 'good', `${bytes(j.result.total_saved)} recovered`));
    list.append(d);
  });
}

setInterval(() => { if ($('#do').classList.contains('active')) refreshJobs(); }, 2500);

// ---- drawer: session detail + resume composer -------------------------------

$('#drawer-close').addEventListener('click', () => { $('#drawer').hidden = true; });

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
    if (s.live) addRow('status', 'open right now \u2014 do not resume');
    if (!s.dir_exists) addRow('workspace', 'MISSING');
    body.append(facts);

    // Resume composer: set an instruction, get a one-liner, copy it.
    body.append(el('h2', null, 'Resume'));
    const instr = el('textarea');
    instr.placeholder = 'optional instruction to deliver on resume, e.g. "re-run the audit, writing incrementally"';
    instr.rows = 2;
    body.append(instr);

    const cmdBox = el('pre', 'mono');
    cmdBox.textContent = s.resume;
    body.append(cmdBox);

    const bar = el('div', 'row-actions');
    const build = el('button', 'btn', 'Build command');
    build.addEventListener('click', async () => {
      try {
        const r = await get('/api/resume?id=' + encodeURIComponent(s.id) +
          '&instruction=' + encodeURIComponent(instr.value));
        cmdBox.textContent = r.command;
        warnBox.replaceChildren();
        (r.warnings || []).forEach((w) => warnBox.append(el('div', 'bad', w)));
        copyWrap.replaceChildren(copyBtn(r.command, 'copy command'));
      } catch (e) { toast(e.message, 'bad'); }
    });
    const copyWrap = el('span');
    copyWrap.append(copyBtn(s.resume, 'copy command'));
    bar.append(build, copyWrap);
    body.append(bar);

    const warnBox = el('div');
    body.append(warnBox);
    if (s.live) warnBox.append(el('div', 'bad', 'This session is open right now \u2014 switch to that terminal instead.'));
    if (s.risk === 'critical') warnBox.append(el('div', 'bad', 'Past the resume cliff \u2014 prefer a handoff brief.'));

    // Per-session actions.
    const acts = el('div', 'row-actions');

    // Depth is explicit because the three levels have three different prices,
    // and a single "summarise" button would hide that.
    const sum = el('button', 'btn', 'Summarise');
    sum.addEventListener('click', () => {
      $('#drawer').hidden = true;
      openModal((bx) => summarizeForm(bx, s));
    });
    acts.append(sum);

    const mine = el('button', 'btn primary', 'Mine this session');
    mine.addEventListener('click', () => {
      $('#drawer').hidden = true;
      openModal((bx) => sessionActionForm(bx, s, 'reclaim'));
    });
    acts.append(mine);

    if (s.bytes > 20 * 1024 * 1024) {
      const pr = el('button', 'btn', 'Preview prune');
      pr.addEventListener('click', () => {
        $('#drawer').hidden = true;
        openModal((bx) => sessionActionForm(bx, s, 'prune'));
      });
      acts.append(pr);
    }
    body.append(acts);

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
          d2.append(el('div', null, (t.text || '').slice(0, 1400)));
          body.append(d2);
        });
      }
    }
  } catch (e) {
    body.replaceChildren(el('p', 'bad', 'failed: ' + e.message));
  }
}

// summarizeForm lets the operator pick how hard to look, with the price of
// each depth stated before the choice is made.
function summarizeForm(box, s) {
  const DEPTHS = [
    { name: 'shallow', label: 'Shallow', spends: false,
      desc: 'The original ask and where it left off, quoted directly. No model call.' },
    { name: 'deep', label: 'Deep', spends: true,
      desc: 'Synthesised across the whole session: what was decided, what was tried, where it ended.' },
    { name: 'xray', label: 'X-ray', spends: true,
      desc: 'Read against the workspace it changed — claims checked against git rather than repeated.' },
  ];

  box.append(el('h2', null, 'Summarise'));
  box.append(el('p', 'note', s.title));

  let chosen = 'shallow';
  const picker = el('div', 'depths');
  DEPTHS.forEach((d) => {
    const row = el('label', 'depth' + (d.name === 'shallow' ? ' on' : ''));
    const radio = el('input');
    radio.type = 'radio';
    radio.name = 'depth';
    radio.checked = d.name === 'shallow';
    radio.addEventListener('change', () => {
      chosen = d.name;
      picker.querySelectorAll('.depth').forEach((x) => x.classList.remove('on'));
      row.classList.add('on');
      out.replaceChildren();
      run.textContent = d.spends ? 'Run' : 'Show';
      run.disabled = d.spends;
      estimate.disabled = !d.spends;
    });
    row.append(radio);
    const txt = el('div');
    const head = el('div', 'head');
    head.append(el('strong', null, d.label));
    head.append(el('span', 'pill' + (d.spends ? ' warn' : ''), d.spends ? 'SPENDS' : 'FREE'));
    txt.append(head);
    txt.append(el('div', 'meta', d.desc));
    row.append(txt);
    picker.append(row);
  });
  box.append(picker);

  const out = el('div', 'result');
  box.append(out);

  const bar = el('div', 'modal-foot');
  const estimate = el('button', 'btn', 'Estimate cost');
  estimate.disabled = true;
  const run = el('button', 'btn primary', 'Show');
  bar.append(estimate, run);
  box.append(bar);

  estimate.addEventListener('click', async () => {
    out.replaceChildren(el('p', 'note', 'gathering evidence...'));
    try {
      const job = await post('/api/action',
        { op: 'summarize', session_id: s.id, depth: chosen, apply: false });
      const done = await pollJob(job.id, (j) =>
        out.replaceChildren(el('p', 'note', j.progress || 'working...')));
      const r = done.result || {};
      out.replaceChildren();
      const p = el('div', 'estimate reveal');
      p.append(el('div', 'k', 'Estimated cost'));
      p.append(el('div', 'v', r.estimate_text || '—'));
      p.append(el('div', 'meta', r.describe || ''));
      out.append(p);
      out.append(el('p', 'note', 'Shallow is free and often enough — try it first.'));
      run.disabled = false;
    } catch (e) { out.replaceChildren(el('p', 'bad', e.message)); }
  });

  run.addEventListener('click', async () => {
    run.disabled = true;
    out.replaceChildren(el('p', 'note', 'working...'));
    try {
      const job = await post('/api/action',
        { op: 'summarize', session_id: s.id, depth: chosen, apply: true });
      const done = await pollJob(job.id, (j) =>
        out.replaceChildren(el('p', 'note', j.progress || 'working...')));
      out.replaceChildren();
      if (done.status === 'failed') { out.append(el('p', 'bad', done.error)); return; }

      const r = done.result || {};
      const card = el('div', 'answer reveal');
      card.append(markdown(r.body || ''));
      if (done.cost && done.cost.usage) card.append(costLine(done.cost));
      else if (r.free) card.append(el('div', 'meta', 'free — no model was called'));
      card.append(copyBtn(r.body || '', 'copy markdown'));
      out.append(card);
    } catch (e) {
      out.replaceChildren(el('p', 'bad', e.message));
    } finally { run.disabled = false; }
  });
}

function sessionActionForm(box, s, op) {
  const action = ACTIONS.find((a) => a.op === op);
  box.append(el('h2', null, action.title));
  box.append(el('p', 'note', s.title));

  const out = el('div', 'result');
  box.append(out);

  const bar = el('div', 'modal-foot');
  const preview = el('button', 'btn', op === 'reclaim' ? 'Estimate cost' : 'Preview');
  const run = el('button', 'btn primary', 'Run');
  run.disabled = true;
  bar.append(preview, run);
  box.append(bar);

  const req = { op, session_id: s.id };

  preview.addEventListener('click', async () => {
    out.replaceChildren(el('p', 'note', 'checking...'));
    try {
      const job = await post('/api/action', { ...req, apply: false });
      renderPreview(out, await pollJob(job.id), action);
      run.disabled = false;
    } catch (e) { out.replaceChildren(el('p', 'bad', e.message)); }
  });

  run.addEventListener('click', async () => {
    run.disabled = true;
    try {
      const job = await post('/api/action', { ...req, apply: true, confirm: true });
      $('#modal').hidden = true;
      show('do');
      trackJob(job.id);
      toast('Started');
    } catch (e) { out.replaceChildren(el('p', 'bad', e.message)); run.disabled = false; }
  });
}

// ---- overview ---------------------------------------------------------------

loaders.overview = async () => {
  const cards = $('#stat-cards');
  cards.replaceChildren();

  let h;
  try { h = await get('/api/health'); }
  catch (e) { cards.append(el('p', 'bad', 'failed: ' + e.message)); return; }

  const card = (k, v, n) => {
    const c = el('div', 'card');
    c.append(el('div', 'k', k), el('div', 'v', v));
    if (n) c.append(el('div', 'n', n));
    return c;
  };
  const tools = Object.entries(h.by_tool || {}).map(([t, n]) => `${t} ${n}`).join(' \u00b7 ');
  const nugTotal = Object.values(h.nuggets || {}).reduce((a, b) => a + b, 0);

  cards.append(
    card('on disk', h.footprint_pending ? 'measuring…' : bytes(h.footprint), tools),
    card('sessions', h.sessions, [
      (h.dead_workspaces || 0) + ' dead workspaces',
      indexAge(h.indexed_at),
    ].filter(Boolean).join(' \u00b7 ')),
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
  } else { riskPanel.hidden = true; }

  const bars = $('#assay-bars');
  try {
    const a = await get('/api/assay');
    if (!a.assayed) return;
    bars.replaceChildren();
    const total = a.bytes || 1;
    const bar = el('div', 'bar');
    ['signal', 'exhaust', 'artifact', 'bookkeeping'].forEach((k) => {
      const sp = el('span', k);
      sp.style.width = (100 * (a[k] || 0) / total) + '%';
      bar.append(sp);
    });
    bars.append(bar);

    const legend = el('div', 'legend');
    [['signal', '#6ee7b7'], ['exhaust', '#48536b'], ['artifact', '#58c4dd'], ['bookkeeping', '#333c4d']]
      .forEach(([k, c]) => {
        const item = el('span');
        const i = el('i');
        i.style.background = c;
        item.append(i, document.createTextNode(
          `${k} ${bytes(a[k] || 0)} (${(100 * (a[k] || 0) / total).toFixed(1)}%)`));
        legend.append(item);
      });
    bars.append(legend);
    bars.append(el('p', 'note',
      `${bytes(a.reclaimable)} reclaimable \u00b7 ${a.compression}x compression \u00b7 ` +
      `${a.images} images in ${a.image_clusters} clusters \u00b7 ${a.assayed} of ${a.sessions} assayed`));
  } catch { /* index not built */ }
};

// ---- sessions ---------------------------------------------------------------

function sessionRow(s) {
  const row = el('div', 'row');
  row.addEventListener('click', () => openSession(s.id));

  const top = el('div', 'top');
  top.append(el('span', 'tool ' + s.tool, s.tool));
  top.append(el('span', 'title', s.title));
  if (s.risk && s.risk !== 'ok') {
    top.append(el('span', 'pill ' + (s.risk === 'critical' ? 'crit' : 'warn'), s.risk));
  }
  if (s.live) top.append(el('span', 'pill live', 'open'));
  if (!s.dir_exists) top.append(el('span', 'pill crit', 'dir missing'));
  if (s.span_days >= 2) top.append(el('span', 'pill', s.span_days.toFixed(0) + 'd span'));
  top.append(el('span', 'meta', s.age + ' ago'));
  top.append(el('span', 'meta', s.bytes ? bytes(s.bytes) : (s.turns ? s.turns + ' turns' : '')));
  row.append(top);
  row.append(el('div', 'dir', s.dir));

  const cmd = el('div', 'cmd mono');
  cmd.append(el('span', null, s.resume));
  cmd.append(copyBtn(s.resume));
  row.append(cmd);
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
    list.replaceChildren(el('p', 'bad', 'failed: ' + e.message));
  }
}

function groupKey(s, mode) {
  if (mode === 'tool') return s.tool;
  if (mode === 'workspace') return s.dir || '(none)';
  if (mode === 'drive') {
    const m = /^([A-Za-z]:)/.exec(s.dir || '');
    return m ? m[1] : (s.dir || '').split('/')[1] || '(root)';
  }
  return '';
}

function renderSessions() {
  const list = $('#session-list');
  const q = $('#f-search').value.toLowerCase();
  const mode = $('#f-group').value;
  const rows = allSessions.filter((s) => !q || (s.title + ' ' + s.dir).toLowerCase().includes(q));

  list.replaceChildren();
  if (!rows.length) { list.append(el('p', 'empty', 'no sessions match')); return; }

  if (!mode) { rows.forEach((s) => list.append(sessionRow(s))); return; }

  const groups = new Map();
  rows.forEach((s) => {
    const k = groupKey(s, mode);
    if (!groups.has(k)) groups.set(k, []);
    groups.get(k).push(s);
  });

  [...groups.entries()]
    .sort((a, b) => b[1].length - a[1].length)
    .forEach(([k, items]) => {
      const totalBytes = items.reduce((n, s) => n + (s.bytes || 0), 0);
      const h = el('div', 'group-head');
      h.append(el('span', 'title', k));
      h.append(el('span', 'meta', `${items.length} session(s) \u00b7 ${bytes(totalBytes)}`));
      list.append(h);
      items.forEach((s) => list.append(sessionRow(s)));
    });
}

loaders.sessions = loadSessions;
['#f-tool', '#f-days', '#f-all'].forEach((s) => $(s).addEventListener('change', loadSessions));
$('#f-group').addEventListener('change', renderSessions);
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
      const e = el('p', 'empty', 'No nuggets yet. ');
      const b = el('button', 'btn primary', 'Mine some sessions');
      b.addEventListener('click', () => show('do'));
      e.append(b);
      list.append(e);
      return;
    }
    ns.forEach((n) => {
      const d = el('div', 'nugget');
      d.append(el('h3', null, n.title));
      d.append(el('p', null, n.body));
      const m = el('div', 'meta');
      m.append(el('span', 'pill', n.kind));
      m.append(document.createTextNode(
        ` ${Math.round((n.confidence || 0) * 100)}% \u00b7 ${(n.session_id || '').slice(0, 8)} \u00b7 ${n.workspace || ''} \u00b7 ${n.model || ''}`));
      if (n.redacted) m.append(el('span', 'pill warn', 'redacted'));
      m.append(copyBtn(n.title + '\n\n' + n.body, 'copy'));
      d.append(m);
      list.append(d);
    });
  } catch (e) {
    list.replaceChildren(el('p', 'bad', 'failed: ' + e.message));
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
      const e = el('p', 'empty', 'Nothing written yet. ');
      const b = el('button', 'btn primary', 'Write something');
      b.addEventListener('click', () => show('do'));
      e.append(b);
      list.append(e);
      return;
    }
    as.forEach((a) => {
      const d = el('div', 'row');
      const top = el('div', 'top');
      top.append(el('span', 'pill', a.kind));
      top.append(el('span', 'title', a.title));
      top.append(el('span', 'meta', new Date(a.created_at).toLocaleString()));
      d.append(top);
      const p = el('div', 'cmd mono');
      p.append(el('span', null, a.path));
      p.append(copyBtn(a.path, 'copy path'));
      d.append(p);
      const meta = el('div', 'meta',
        `${(a.nugget_ids || []).length} nuggets \u00b7 ${a.model || ''}`);
      d.append(meta);

      // A path is not the document. Reading back what midden wrote used to
      // mean leaving the tool and opening a file.
      const read = el('button', 'btn', 'Read');
      read.addEventListener('click', () => openModal((box) => artifactReader(box, a)));
      d.append(read);

      list.append(d);
    });
  } catch (e) {
    list.replaceChildren(el('p', 'bad', 'failed: ' + e.message));
  }
};

// artifactReader shows a generated document without leaving the page.
function artifactReader(box, a) {
  box.append(costHeading({ costly: false, title: a.title || a.kind }));
  box.append(el('p', 'note', a.path));

  const out = el('div', 'result');
  out.append(el('p', 'note', 'reading...'));
  box.append(out);

  get('/api/artifact?path=' + encodeURIComponent(a.path))
    .then((r) => {
      out.replaceChildren();
      const pre = el('pre', 'brief-body mono');
      pre.textContent = r.body;
      out.append(pre);

      const bar = el('div', 'modal-foot');
      bar.append(el('span', 'foot-note', r.name));
      const copy = el('button', 'btn primary', 'Copy');
      copy.addEventListener('click', async () => {
        await navigator.clipboard.writeText(r.body);
        toast('Copied');
      });
      bar.append(copy);
      out.append(bar);
    })
    .catch((e) => out.replaceChildren(el('p', 'bad', e.message)));
}

// ---- ask -------------------------------------------------------------------

loaders.ask = async () => {
  const box = $('#ask-suggestions');
  box.replaceChildren();
  try {
    const d = await get('/api/ask-suggestions');
    if (!(d.suggestions || []).length) return;
    box.append(el('p', 'note', 'Questions this evidence can answer:'));
    d.suggestions.forEach((q) => {
      const b = el('button', 'chip', q);
      b.addEventListener('click', () => {
        $('#ask-input').value = q;
        $('#ask-run').disabled = true;
        $('#ask-estimate-out').replaceChildren();
      });
      box.append(b);
    });
  } catch { /* no evidence yet */ }
};

$('#ask-estimate').addEventListener('click', async () => {
  const q = $('#ask-input').value.trim();
  const out = $('#ask-estimate-out');
  if (!q) { out.replaceChildren(el('p', 'bad', 'Type a question first.')); return; }

  out.replaceChildren(el('p', 'note', 'assembling evidence...'));
  try {
    const job = await post('/api/action', { op: 'ask', question: q, apply: false });
    const done = await pollJob(job.id);
    const r = done.result || {};
    out.replaceChildren();
    const p = el('div', 'estimate reveal');
    p.append(el('div', 'k', 'Estimated cost'));
    p.append(el('div', 'v', r.estimate_text || '—'));
    p.append(el('div', 'meta', `${r.nuggets || 0} nugget(s) and ${r.findings || 0} finding(s) as evidence`));
    out.append(p);
    $('#ask-run').disabled = false;
  } catch (e) {
    out.replaceChildren(el('p', 'bad', e.message));
  }
});

$('#ask-run').addEventListener('click', async () => {
  const q = $('#ask-input').value.trim();
  if (!q) return;
  const ans = $('#ask-answer');
  $('#ask-run').disabled = true;
  ans.replaceChildren(el('p', 'note', 'thinking...'));

  try {
    const job = await post('/api/action', { op: 'ask', question: q, apply: true });
    const done = await pollJob(job.id, (j) => {
      ans.replaceChildren(el('p', 'note', j.progress || 'working...'));
    });
    ans.replaceChildren();
    if (done.status === 'failed') {
      ans.append(el('p', 'bad', done.error));
      return;
    }
    const card = el('div', 'answer reveal');
    card.append(el('div', 'q', q));
    card.append(markdown((done.result || {}).answer || ''));
    if (done.cost && done.cost.usage) card.append(costLine(done.cost));
    ans.append(card);
  } catch (e) {
    ans.replaceChildren(el('p', 'bad', e.message));
  } finally {
    $('#ask-run').disabled = false;
  }
});

function costLine(run) {
  const u = run.usage || {};
  const charge = u.aiu ? u.aiu.toFixed(1) + ' AIU'
               : u.usd ? '$' + u.usd.toFixed(2)
               : tok((u.input_tokens || 0) + (u.output_tokens || 0)) + ' tok';
  return el('div', 'cost-line',
    `cost ${charge} · ${Math.round((u.duration_ms || 0) / 1000)}s`);
}

// markdown renders the small subset models actually emit here. Deliberately
// minimal and text-only — never innerHTML, so a model response can never
// inject markup.
function markdown(src) {
  const wrap = el('div', 'md');
  let list = null;

  src.split('\n').forEach((line) => {
    const h = /^(#{1,4})\s+(.*)$/.exec(line);
    const li = /^\s*[-*]\s+(.*)$/.exec(line);

    if (h) {
      list = null;
      wrap.append(el('h' + Math.min(4, h[1].length + 1), null, h[2]));
      return;
    }
    if (li) {
      if (!list) { list = el('ul'); wrap.append(list); }
      list.append(el('li', null, stripEmphasis(li[1])));
      return;
    }
    if (/^\s*```/.test(line)) { list = null; return; }
    if (!line.trim()) { list = null; return; }

    list = null;
    const p = el('p', null, stripEmphasis(line));
    wrap.append(p);
  });
  return wrap;
}

function stripEmphasis(s) {
  return s.replace(/\*\*(.+?)\*\*/g, '$1').replace(/`(.+?)`/g, '$1');
}

// ---- cost -------------------------------------------------------------------

loaders.cost = async () => {
  const cards = $('#cost-cards');
  cards.replaceChildren(el('p', 'note', 'loading...'));
  let d;
  try { d = await get('/api/cost'); }
  catch (e) { cards.replaceChildren(el('p', 'bad', e.message)); return; }

  const t = d.totals || {};
  const card = (k, v, n) => {
    const c = el('div', 'card');
    c.append(el('div', 'k', k), el('div', 'v', v));
    if (n) c.append(el('div', 'n', n));
    return c;
  };
  const charge = t.aiu ? t.aiu.toFixed(1) + ' AIU' : t.usd ? '$' + t.usd.toFixed(2) : '\u2014';

  cards.replaceChildren(
    card('total charged', charge, 'on your existing seat'),
    card('tokens', tok(t.tokens || 0), `${t.runs || 0} run(s)`),
    card('items produced', t.items || 0, 'nuggets + artifacts'),
    card('free commands', 'everything else', 'ls, doctor, assay, prune, advise\u2026')
  );

  const cal = $('#calibration');
  cal.replaceChildren();
  const entries = Object.entries(d.calibration || {});
  if (!entries.length) {
    cal.append(el('p', 'note', 'No calibration yet \u2014 run something that uses a model.'));
  } else {
    entries.forEach(([op, c]) => {
      const row = el('div', 'calib');
      row.append(el('span', 'pill', op));
      row.append(document.createTextNode(
        ` actual is ${c.mean_factor}\u00d7 the raw prompt estimate ` +
        `(range ${c.min_factor}\u2013${c.max_factor}\u00d7 over ${c.samples} run(s))`));
      cal.append(row);
    });
  }

  const runs = $('#run-list');
  runs.replaceChildren();
  if (!(d.runs || []).length) {
    runs.append(el('p', 'note', 'No runs recorded.'));
    return;
  }
  const table = el('table');
  const head = el('tr');
  ['when', 'op', 'scope', 'items', 'tokens', 'charged', 'est err', 'secs'].forEach((h) =>
    head.append(el('th', null, h)));
  table.append(head);
  d.runs.forEach((r) => {
    const u = r.usage || {};
    const billable = (u.input_tokens || 0) + (u.output_tokens || 0) +
                     (u.cache_read_tokens || 0) + (u.cache_write_tokens || 0);
    const ch = u.aiu ? u.aiu.toFixed(1) + ' AIU' : u.usd ? '$' + u.usd.toFixed(2) : '\u2014';
    const err = r.est_tokens && billable ? (billable / r.est_tokens).toFixed(0) + '\u00d7' : '\u2014';
    const tr = el('tr');
    tr.append(
      el('td', 'meta', new Date(r.started_at).toLocaleString()),
      el('td', null, r.op),
      el('td', 'meta', r.scope || ''),
      el('td', null, String(r.items || 0)),
      el('td', null, tok(billable)),
      el('td', null, ch),
      el('td', 'meta', err),
      el('td', 'meta', Math.round((u.duration_ms || 0) / 1000)));
    table.append(tr);
  });
  runs.append(table);
};

// ---- log --------------------------------------------------------------------

loaders.ops = async () => {
  const list = $('#ops-list');
  list.replaceChildren(el('p', 'note', 'loading...'));
  try {
    const ops = await get('/api/ops');
    list.replaceChildren();
    if (!ops || !ops.length) {
      list.append(el('p', 'empty', 'Nothing has been modified.'));
      return;
    }
    const t = el('table');
    const head = el('tr');
    ['when', 'op', 'tool', 'session', 'before', 'after', 'ok'].forEach((h) => head.append(el('th', null, h)));
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
        el('td', o.ok ? 'good' : 'bad', o.ok ? 'ok' : 'FAILED'));
      t.append(tr);
    });
    list.append(t);
  } catch (e) {
    list.replaceChildren(el('p', 'bad', 'failed: ' + e.message));
  }
};

loaders.do();

'use strict';

const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = String(text);
  return node;
}

function append(parent, ...children) {
  children.flat().forEach((child) => {
    if (child === undefined || child === null || child === false) return;
    parent.append(child instanceof Node ? child : document.createTextNode(String(child)));
  });
  return parent;
}

function clear(node) {
  node.replaceChildren();
  return node;
}

function button(label, className = 'button', onClick) {
  const node = el('button', className, label);
  node.type = 'button';
  if (onClick) node.addEventListener('click', onClick);
  return node;
}

function badge(label, kind = '') {
  return el('span', `badge ${kind}`.trim(), label);
}

function field(label, control) {
  const wrap = el('label', 'field');
  append(wrap, el('span', null, label), control);
  return wrap;
}

function textInput(value = '', placeholder = '') {
  const input = el('input');
  input.type = 'text';
  input.value = value || '';
  input.placeholder = placeholder;
  return input;
}

function selectInput(options, value = '') {
  const select = el('select');
  options.forEach((option) => {
    const node = el('option', null, option.label);
    node.value = option.value;
    node.selected = option.value === value;
    select.append(node);
  });
  return select;
}

function textarea(value = '', placeholder = '') {
  const node = el('textarea');
  node.value = value || '';
  node.placeholder = placeholder;
  return node;
}

function pageHead(kicker, title, copy, actions = []) {
  const head = el('header', 'page-head');
  const text = el('div', 'page-copy');
  append(text, el('div', 'eyebrow', kicker), el('h1', null, title), el('p', null, copy));
  append(head, text);
  if (actions.length) {
    const actionWrap = el('div', 'page-actions');
    actions.forEach((action) => actionWrap.append(action));
    head.append(actionWrap);
  }
  return head;
}

function sectionHead(kicker, title, copy, actions = []) {
  const head = el('div', 'panel-head');
  const text = el('div');
  append(text, el('div', 'eyebrow', kicker), el('h2', 'section-title', title));
  if (copy) text.append(el('p', 'section-copy', copy));
  head.append(text);
  if (actions.length) {
    const wrap = el('div', 'row-actions');
    actions.forEach((action) => wrap.append(action));
    head.append(wrap);
  }
  return head;
}

function emptyState(title, copy, action) {
  const wrap = el('div', 'empty');
  append(wrap, el('strong', null, title), el('span', null, copy));
  if (action) {
    const actions = el('div', 'row-actions');
    actions.style.justifyContent = 'center';
    actions.style.marginTop = '14px';
    actions.append(action);
    wrap.append(actions);
  }
  return wrap;
}

function formatBytes(value) {
  let n = Number(value || 0);
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let index = 0;
  while (n >= 1024 && index < units.length - 1) {
    n /= 1024;
    index += 1;
  }
  return `${n.toFixed(index ? 1 : 0)} ${units[index]}`;
}

function formatCount(value) {
  const n = Number(value || 0);
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}k`;
  return String(n);
}

function formatDate(value) {
  if (!value) return 'not yet';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return new Intl.DateTimeFormat(undefined, {
    month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
  }).format(date);
}

function truncateText(value, limit) {
  const text = String(value || '').trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, limit).trimEnd()}…`;
}

function relativeAge(value) {
  if (!value) return 'not yet scanned';
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return '';
  const minutes = Math.max(0, Math.round((Date.now() - timestamp) / 60000));
  if (minutes < 2) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  if (minutes < 1440) return `${Math.round(minutes / 60)}h ago`;
  return `${Math.round(minutes / 1440)}d ago`;
}

function statusKind(status) {
  const value = String(status || '').toLowerCase();
  if (['ready', 'connected', 'done', 'reviewed', 'exported', 'pass', 'complete'].includes(value)) return 'ready';
  if (['running', 'progress', 'queued', 'waiting'].includes(value)) return 'running';
  if (['failed', 'rejected', 'review', 'needs_attention', 'locked'].includes(value)) return 'danger';
  if (['approved'].includes(value)) return 'blue';
  if (['draft', 'evidence_review', 'guided', 'lab', 'ready_to_test', 'legacy'].includes(value)) return 'lab';
  return '';
}

function humanStatus(status) {
  return String(status || 'unknown').replaceAll('_', ' ');
}

async function get(path) {
  const response = await fetch(path);
  if (!response.ok) throw new Error((await response.text()) || response.statusText);
  return response.json();
}

async function post(path, body) {
  const response = await fetch(path, {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-Midden-Request': '1'},
    body: JSON.stringify(body),
  });
  if (!response.ok) throw new Error((await response.text()) || response.statusText);
  return response.json();
}

function storedConductorMessages() {
  try {
    const value = JSON.parse(localStorage.getItem('midden.conductor.messages') || '[]');
    return Array.isArray(value) ? value.slice(-40) : [];
  } catch {
    return [];
  }
}

const state = {
  overview: null,
  activeView: 'home',
  selectedRecipe: '',
  recipeDetail: null,
  conductorMode: 'auto',
  conductorMessages: storedConductorMessages(),
  conductorDraft: '',
  operationsTab: 'sessions',
  connections: null,
  integrations: null,
  plugins: null,
};

const viewMeta = {
  home: ['Evidence refinery', 'Home'],
  mine: ['Source to yield', 'Mine'],
  studio: ['Recipes and review', 'Studio'],
  knowledge: ['Retain the learning', 'Knowledge'],
  'agent-forge': ['Evidence to behavior', 'Agent forge'],
  personalization: ['Private data readiness', 'Personalization'],
  connections: ['Capabilities in context', 'Connections'],
  conductor: ['Conversational control', 'Conductor'],
  operations: ['Sources, cost, and audit', 'Operations'],
};

let toastTimer;
function toast(message, kind = '') {
  const node = $('#toast');
  node.textContent = message;
  node.className = `toast ${kind}`.trim();
  node.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { node.hidden = true; }, 2200);
}

function notice(message, kind = 'bad', actions = []) {
  clearTimeout(toastTimer);
  $('#toast').hidden = true;
  const region = clear($('#notice-region'));
  region.className = `notice-region ${kind}`.trim();
  const copy = el('div');
  append(copy, el('strong', null, kind === 'bad' ? 'Action needs attention' : 'Done'),
    el('span', null, message));
  region.append(copy);
  if (actions.length) {
    const wrap = el('div', 'row-actions');
    actions.forEach((action) => wrap.append(action));
    region.append(wrap);
  }
  region.hidden = false;
  region.scrollIntoView({block: 'nearest'});
}

function clearNotice() {
  const region = $('#notice-region');
  region.hidden = true;
  clear(region);
}

function announce(message) {
  $('#live-region').textContent = message;
}

let modalReturnFocus = null;
let drawerReturnFocus = null;

function openModal(title, copy) {
  const modal = $('#modal');
  modalReturnFocus = document.activeElement;
  const body = clear($('#modal-body'));
  const heading = el('h2', null, title);
  heading.id = 'modal-title';
  append(body, heading);
  if (copy) body.append(el('p', 'note', copy));
  modal.hidden = false;
  requestAnimationFrame(() => $('#modal-close').focus());
  return body;
}

function closeModal() {
  if ($('#modal').hidden) return;
  $('#modal').hidden = true;
  clear($('#modal-body'));
  if (modalReturnFocus?.isConnected) modalReturnFocus.focus();
  modalReturnFocus = null;
}

function openDrawer(title) {
  const drawer = $('#drawer');
  drawerReturnFocus = document.activeElement;
  const body = clear($('#drawer-body'));
  body.append(el('h2', null, title));
  drawer.hidden = false;
  requestAnimationFrame(() => $('#drawer-close').focus());
  return body;
}

function closeDrawer() {
  if ($('#drawer').hidden) return;
  $('#drawer').hidden = true;
  clear($('#drawer-body'));
  if (drawerReturnFocus?.isConnected) drawerReturnFocus.focus();
  drawerReturnFocus = null;
}

$('#modal-close').addEventListener('click', closeModal);
$('[data-close-modal]').addEventListener('click', closeModal);
$('#drawer-close').addEventListener('click', closeDrawer);
$('[data-close-drawer]').addEventListener('click', closeDrawer);
$('#nav-backdrop').addEventListener('click', () => {
  document.body.classList.remove('nav-open');
  $('#nav-backdrop').hidden = true;
  $('#nav-toggle').focus();
});
$('#nav-close').addEventListener('click', () => {
  document.body.classList.remove('nav-open');
  $('#nav-backdrop').hidden = true;
  $('#nav-toggle').focus();
});

function trapFocus(container, event) {
  const focusable = $$('button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])', container)
    .filter((node) => !node.hidden && node.getClientRects().length);
  if (!focusable.length) return;
  const first = focusable[0];
  const last = focusable[focusable.length - 1];
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault();
    first.focus();
  }
}

document.addEventListener('keydown', (event) => {
  if (event.key === 'Tab' && !$('#modal').hidden) {
    trapFocus($('#modal'), event);
    return;
  }
  if (event.key === 'Tab' && !$('#drawer').hidden) {
    trapFocus($('#drawer'), event);
    return;
  }
  if (event.key === 'Escape') {
    closeModal();
    closeDrawer();
    document.body.classList.remove('nav-open');
    $('#nav-backdrop').hidden = true;
  }
});

$('#nav-toggle').addEventListener('click', () => {
  document.body.classList.toggle('nav-open');
  $('#nav-backdrop').hidden = !document.body.classList.contains('nav-open');
});

$$('.nav-item[data-view]').forEach((item) => {
  item.addEventListener('click', () => activateView(item.dataset.view));
});

function activateView(view) {
  clearNotice();
  state.activeView = view;
  $$('.nav-item[data-view]').forEach((item) => item.classList.toggle('active', item.dataset.view === view));
  $$('.view').forEach((section) => section.classList.toggle('active', section.id === view));
  const meta = viewMeta[view] || ['', view];
  $('#view-kicker').textContent = meta[0];
  $('#view-title').textContent = meta[1];
  document.body.classList.remove('nav-open');
  $('#nav-backdrop').hidden = true;
  window.scrollTo({top: 0, behavior: 'instant'});
  renderActiveView().then(() => {
    window.scrollTo({top: 0, behavior: 'instant'});
  }).catch(showError);
}

function updateGlobalBadges() {
  const wrap = clear($('#global-badges'));
  if (!state.overview) return;
  const stats = state.overview.stats || {};
  append(wrap,
    badge(`${formatCount(stats.nuggets)} evidence`, stats.nuggets ? 'free' : ''),
    badge(`${formatCount(stats.outputs)} outputs`, stats.outputs ? 'blue' : ''),
  );
  if (stats.indexed_at) wrap.append(badge(`indexed ${relativeAge(stats.indexed_at)}`));
}

async function loadOverview(force = false) {
  if (!state.overview || force) state.overview = await get('/api/refinery');
  updateGlobalBadges();
  return state.overview;
}

async function refreshOverview() {
  state.overview = null;
  state.recipeDetail = null;
  state.connections = null;
  await loadOverview(true);
  await renderActiveView();
}

function showError(error) {
  console.error(error);
  notice(friendlyError(error), 'bad');
}

function friendlyError(error) {
  const message = error?.message || String(error);
  if (/failed to fetch|networkerror|internet disconnected/i.test(message)) {
    return 'The local Midden service did not respond. Confirm the app is still running, then retry.';
  }
  if (/scan lock|index refresh is already running|locked a portion/i.test(message)) {
    return 'Midden is finishing the source refresh. This action will be available as soon as that refresh completes.';
  }
  return message;
}

async function pollJob(id, onProgress) {
  for (;;) {
    const job = await get(`/api/job-status?id=${encodeURIComponent(id)}`);
    if (onProgress) onProgress(job);
    if (job.status === 'done' || job.status === 'failed') return job;
    await new Promise((resolve) => setTimeout(resolve, 850));
  }
}

async function runJob(request, title, copy) {
  const job = await post('/api/action', request);
  const body = openModal(title, copy);
  const card = el('div', 'estimate-box');
  const status = el('div', 'eyebrow', 'Queued');
  const progress = el('strong', null, 'Starting');
  const estimate = el('p', 'note');
  append(card, status, progress, estimate);
  body.append(card);

  const finished = await pollJob(job.id, (current) => {
    status.textContent = humanStatus(current.status);
    progress.textContent = current.progress || humanStatus(current.status);
    if (current.estimate) {
      const e = current.estimate;
      estimate.textContent = `${formatCount(e.low)}–${formatCount(e.high)} tokens`
        + (e.unit_mid ? ` · ~${Number(e.unit_mid).toFixed(1)} ${e.unit}` : '');
    }
  });
  closeModal();
  if (finished.status === 'failed') throw new Error(finished.error || `${request.op} failed`);
  return finished;
}

async function refreshSources() {
  const buttonNode = $('#refresh-data');
  const original = buttonNode.textContent;
  buttonNode.disabled = true;
  buttonNode.textContent = 'Refreshing…';
  try {
    const job = await runJob({op: 'refresh'}, 'Refreshing source index',
      'Reads the configured session stores, reconciles stale rows, and writes only Midden’s local index.');
    const result = job.result || {};
    toast(result.partial ? 'Sources partially refreshed; one store needs attention.' : 'Source index refreshed.',
      result.partial ? 'bad' : 'good');
    await refreshOverview();
  } catch (error) {
    showError(error);
  } finally {
    buttonNode.disabled = false;
    buttonNode.textContent = original;
  }
}

$('#refresh-data').addEventListener('click', refreshSources);

async function runMine() {
  try {
    const job = await runJob({op: 'mine', days: 30}, 'Mining the last 30 days',
      'This pass is deterministic and free: it classifies evidence and calculates yield without calling a model.');
    const result = job.result || {};
    await refreshOverview();
    notice(`Mine complete: ${result.assayed || 0} assayed, ${result.skipped || 0} unchanged, ${result.failed || 0} failed.`, 'good');
  } catch (error) {
    showError(error);
  }
}

function workspaceOptions(includeAll = true, evidenceOnly = false) {
  const options = [];
  if (includeAll) options.push({value: '', label: 'All evidence'});
  (state.overview?.workspaces || [])
    .filter((workspace) => !evidenceOnly || workspace.nuggets > 0)
    .forEach((workspace) => {
    options.push({
      value: workspace.id,
      label: `${workspace.name} · ${workspace.nuggets} evidence · ${workspace.sessions} sessions`,
    });
  });
  return options;
}

function backendFields() {
  const backend = selectInput([
    {value: '', label: 'Auto-detect signed-in CLI'},
    {value: 'copilot', label: 'Copilot CLI'},
    {value: 'claude', label: 'Claude Code'},
    {value: 'opencode', label: 'OpenCode'},
  ]);
  const model = textInput('', 'optional model override');
  return {backend, model};
}

function openReclaimModal(prefillWorkspace = '') {
  const body = openModal('Extract reusable evidence',
    'Midden first estimates the model cost from an ASSAY-filtered slice. Nothing is charged until the second approval.');
  const workspace = selectInput(workspaceOptions(true), prefillWorkspace);
  const days = selectInput([
    {value: '7', label: 'Last 7 days'},
    {value: '30', label: 'Last 30 days'},
    {value: '90', label: 'Last 90 days'},
  ], '30');
  const depth = selectInput([
    {value: 'summary', label: 'Summary · cheapest'},
    {value: 'deep', label: 'Deep · cross-turn synthesis'},
    {value: 'xray', label: 'X-ray · project context'},
  ], 'summary');
  const fields = backendFields();
  append(body, field('Workspace', workspace), field('Time range', days), field('Depth', depth),
    field('Model backend', fields.backend), field('Model override', fields.model));

  const actions = el('div', 'modal-actions');
  actions.append(button('Cancel', 'button ghost', closeModal));
  actions.append(button('Estimate evidence extraction', 'button', async () => {
    closeModal();
    try {
      const request = {
        op: 'reclaim', workspace: workspace.value, days: Number(days.value),
        depth: depth.value, backend: fields.backend.value, model: fields.model.value,
        apply: false,
      };
      const preview = await runJob(request, 'Estimating evidence extraction',
        'Reads the bounded evidence slice and predicts cost. No model is invoked.');
      openSpendConfirmation('Extract evidence', preview, async () => {
        const run = await runJob({...request, apply: true}, 'Extracting evidence',
          'The selected signed-in CLI is mining redacted evidence. Raw transcripts are not stored as nuggets.');
        toast(`${run.result?.count || 0} evidence item(s) stored`, 'good');
        await refreshOverview();
      });
    } catch (error) {
      showError(error);
    }
  }));
  body.append(actions);
}

function openSpendConfirmation(title, job, onApprove) {
  const result = job.result || {};
  const estimate = result.estimate || job.estimate || {};
  const body = openModal(title, estimate.samples
    ? 'Review the estimate calibrated from your previous runs.'
    : 'Review this conservative first-run estimate before spending.');
  const box = el('div', 'estimate-box');
  append(box,
    el('div', 'eyebrow', estimate.samples ? `Calibrated from ${estimate.samples} prior run(s)` : 'First-run conservative estimate'),
    el('strong', null, result.estimate_text || `${formatCount(estimate.low)}–${formatCount(estimate.high)} tokens`),
  );
  const facts = el('div', 'estimate-facts');
  [
    ['Scope', `${result.sessions || result.nuggets || 0} source item(s)`],
    ['Backend', result.backend || 'auto-detect signed-in CLI'],
    ['Model', result.model || 'backend default'],
    ['Time', result.estimated_seconds ? `about ${Math.ceil(result.estimated_seconds / 60)} minute(s)` : 'local / immediate'],
    ['Charge', estimate.unit_mid ? `~${Number(estimate.unit_mid).toFixed(1)} ${estimate.unit}` : 'unknown until the first run is reconciled'],
    ['External writes', 'none'],
  ].forEach(([label, value]) => {
    const item = el('div', 'estimate-fact');
    append(item, el('span', null, label), el('strong', null, value));
    facts.append(item);
  });
  box.append(facts);
  body.append(box);
  if (!estimate.samples && estimate.mid) {
    body.append(el('div', 'warning',
      'This range includes CLI system prompts and cache overhead. Run one small scope first; future estimates will calibrate against actual usage.'));
  }
  const actions = el('div', 'modal-actions');
  actions.append(button('Not now', 'button ghost', closeModal));
  const approve = button('Approve spend and run', 'button good', async () => {
    approve.disabled = true;
    closeModal();
    try {
      await onApprove();
    } catch (error) {
      showError(error);
    }
  });
  actions.append(approve);
  body.append(actions);
}

function sourceCards(sources) {
  const grid = el('div', 'source-grid');
  sources.forEach((source) => {
    const card = el('article', 'source-card reveal');
    append(card,
      el('div', 'eyebrow', source.tool),
      el('h3', null, source.name),
      el('p', null, source.sessions
        ? `${source.sessions} sessions detected · ${source.nuggets} reclaimed evidence`
        : 'No local session store detected'),
    );
    const foot = el('div', 'source-foot');
    foot.append(badge(source.ready ? 'ready to mine' : 'not detected', source.ready ? 'free' : ''));
    if (source.last_updated) foot.append(el('span', 'progress-note', ` updated ${relativeAge(source.last_updated)}`));
    card.append(foot);
    grid.append(card);
  });
  return grid;
}

function metricGrid(items) {
  const grid = el('div', 'metric-grid');
  items.forEach((item) => {
    const card = el('article', 'metric-card');
    append(card, el('div', 'eyebrow', item.label), el('strong', null, item.value), el('span', null, item.copy));
    grid.append(card);
  });
  return grid;
}

function yieldCards(yieldMap) {
  const grid = el('div', 'yield-grid');
  (yieldMap.categories || []).forEach((category) => {
    const card = el('article', 'yield-card reveal');
    append(card,
      el('div', 'eyebrow', category.title),
      el('div', 'yield-count', `${category.count} ${category.unit}`),
      el('p', null, (category.details || []).join(' · ')),
    );
    const bar = el('div', 'quality-bar');
    const fill = el('i');
    fill.style.width = `${Math.max(0, Math.min(100, category.quality || 0))}%`;
    bar.append(fill);
    card.append(bar);
    grid.append(card);
  });
  return grid;
}

function journeySteps(steps) {
  const journey = el('div', 'journey');
  steps.forEach((step, index) => {
    if (index) journey.append(el('span', 'journey-arrow', '→'));
    const node = el('div', 'journey-step');
    append(node, el('strong', null, step[0]), el('span', null, step[1]));
    journey.append(node);
  });
  return journey;
}

async function renderHome() {
  const overview = await loadOverview();
  const root = clear($('#home-content'));
  if (overview.onboarding) {
    append(root,
      pageHead('Install and first mine', 'See what your AI sessions can become.',
        'Midden mines decisions, fixes, commands, screenshots, lessons, and preferences, then turns them into grounded new assets.'),
      sourceCards(overview.sources),
    );
    const hero = el('section', 'hero');
    const copy = el('div');
    append(copy,
      el('div', 'eyebrow', 'First pass · free · local'),
      el('h2', null, 'Mine the last 30 days and calculate the yield.'),
      el('p', null, 'Midden classifies the session exhaust locally, preserves provenance, and shows what content, knowledge, and improvement packs the evidence can honestly support.'),
    );
    append(hero, copy, button('Mine my AI work', 'button good', runMine));
    root.append(hero);
    root.append(journeySteps([
      ['Detect sources', 'read-only local stores'],
      ['Explicit mine', 'free classification'],
      ['See yield', 'opportunities before spend'],
      ['Extract evidence', 'estimate first'],
      ['Build drafts', 'review before export'],
    ]));
    const promise = el('section', 'grid-3');
    [
      ['Create', 'Tutorials, release packs, slides, diagrams, and video briefs.'],
      ['Teach', 'Handbooks, flashcards, quizzes, and notebook source packs.'],
      ['Improve', 'Skills, instruction proposals, evals, and private data packs.'],
    ].forEach(([title, copyText]) => {
      const card = el('article', 'panel');
      append(card, el('div', 'eyebrow', title), el('h3', 'section-title', copyText));
      promise.append(card);
    });
    root.append(promise);
    return;
  }

  const evidenceReady = Boolean(overview.yield?.ready);
  const actions = evidenceReady
    ? [
      button('Ask the Conductor', 'button', () => activateView('conductor')),
      button('Mine new work', 'button ghost', runMine),
    ]
    : [
      button('Extract evidence', 'button', () => openReclaimModal()),
      button('Mine again', 'button ghost', runMine),
    ];
  append(root,
    pageHead(evidenceReady ? 'What changed' : 'Assay complete',
      evidenceReady ? 'Your refinery has new yield.' : 'Your sessions are measured. Reclaim evidence next.',
      evidenceReady
        ? 'Midden compares new evidence against saved recipes, reviewed outputs, agent proposals, and private data packs.'
        : 'Assay measured session signal without using a model. Extract a small evidence scope before Midden recommends any output.',
      actions),
    metricGrid([
      {label: 'Evidence', value: formatCount(overview.stats.nuggets), copy: 'redacted, provenanced items'},
      evidenceReady
        ? {label: 'Grounded yield', value: overview.yield.total, copy: 'evidence-backed opportunities'}
        : {label: 'Sessions assayed', value: overview.yield.assayed || 0, copy: 'free deterministic measurement'},
      {label: 'Saved recipes', value: overview.stats.recipes, copy: 'reusable production plans'},
      {label: 'Draft outputs', value: overview.stats.outputs, copy: 'reviewed individually'},
    ]),
  );

  if ((overview.updates || []).length) {
    const panel = el('section', 'panel');
    panel.append(sectionHead('Retention and compounding', 'Useful changes, not dashboard vanity',
      'Only new evidence and its impact are surfaced.'));
    const grid = el('div', 'update-grid');
    overview.updates.slice(0, 6).forEach((update) => {
      const card = el('article', 'update-card');
      append(card, el('div', 'eyebrow', humanStatus(update.kind)), el('h3', null, update.title), el('p', null, update.detail));
      if (update.recipe_id) card.append(button('Review update', 'button ghost compact', () => openRecipe(update.recipe_id)));
      grid.append(card);
    });
    panel.append(grid);
    root.append(panel);
  }

  const recommended = overview.yield.recommended || {};
  if (evidenceReady && recommended.workspace) {
    const hero = el('section', 'hero');
    const copy = el('div');
    append(copy,
      el('div', 'eyebrow', `Best first bundle · ${workspaceName(recommended.workspace)}`),
      el('h2', null, recommended.title),
      el('p', null, `${recommended.why} One evidence review feeds ${(recommended.outputs || []).length} deliverables.`),
    );
    append(hero, copy, button('Build this bundle', 'button', () => {
      openDesignModal({
        workspace: recommended.workspace,
        prompt: 'Turn the strongest recent work into a deep tutorial, a 12-slide deck, an architecture diagram, a 60-second video brief, and an evaluated agent skill proposal.',
        kinds: recommended.outputs,
      });
    }));
    root.append(hero);
  }

  const yieldPanel = el('section', 'panel');
  if (evidenceReady) {
    yieldPanel.append(sectionHead('Grounded yield', `${overview.yield.total} useful opportunities`,
      'Every opportunity is derived from reclaimed evidence. Nothing is generated until you approve a recipe and its evidence.'));
    yieldPanel.append(yieldCards(overview.yield));
  } else {
    yieldPanel.append(sectionHead('Next step', 'Extract a small evidence scope',
      overview.yield.message || 'Reclaimed evidence is required before any production can be recommended.'));
    const summary = el('div', 'assay-summary');
    append(summary,
      badge('FREE ASSAY COMPLETE', 'free'),
      el('strong', null, `${overview.yield.assayed || 0} session(s) measured`),
      el('p', 'note', `${formatBytes(overview.yield.signal_bytes || 0)} of signal identified. Start with one workspace; you will see the estimate before any model call.`),
    );
    yieldPanel.append(summary);
  }
  root.append(yieldPanel);

  const recipes = (overview.recipes || []).filter((recipe) => recipe.status !== 'archived').slice(0, 6);
  const recent = el('section', 'panel');
  recent.append(sectionHead('Saved productions', recipes.length ? 'Continue where you left off' : 'No saved recipes yet',
    'Recipes preserve the output mix and evidence scope for future incremental runs.',
    recipes.length ? [button('Open Studio', 'button ghost compact', () => activateView('studio'))] : []));
  recent.append(recipes.length ? recipeGrid(recipes) :
    evidenceReady
      ? emptyState('Design the first production', 'Ask for an outcome; Conductor will clarify it and preview the plan.',
        button('Ask Conductor', 'button', () => activateView('conductor')))
      : emptyState('No evidence-backed production yet', 'Use the primary Extract evidence action above before creating a recipe.'));
  root.append(recent);
}

function workspaceName(id) {
  const match = (state.overview?.workspaces || []).find((workspace) => workspace.id === id);
  return match?.name || id || 'all evidence';
}

function recipeGrid(recipes) {
  const grid = el('div', 'recipe-grid');
  recipes.forEach((recipe) => {
    const card = el('article', 'recipe-card reveal');
    append(card,
      el('div', 'eyebrow', workspaceName(recipe.workspace)),
      el('h3', null, recipe.title),
      el('p', null, recipe.request || 'Saved refinery production plan.'),
    );
    const meta = el('div', 'recipe-meta');
    append(meta, badge(humanStatus(recipe.status), statusKind(recipe.status)),
      badge(`${recipe.outputs?.length || 0} outputs`),
      badge(`${recipe.evidence_ids?.length || 0} evidence`, 'free'));
    card.append(meta);
    const actions = el('div', 'card-actions');
    actions.append(button('Open production', 'button', () => openRecipe(recipe.uid)));
    card.append(actions);
    grid.append(card);
  });
  return grid;
}

async function renderMine() {
  const overview = await loadOverview();
  const root = clear($('#mine-content'));
  const assayReady = Number(overview.yield.assayed || 0) > 0;
  const extractAction = button('Extract evidence', 'button', () => openReclaimModal());
  extractAction.disabled = !assayReady;
  if (!assayReady) extractAction.title = 'Run the free mine first.';
  append(root,
    pageHead('Source to yield', 'Mine once. Reuse the evidence everywhere.',
      'The free pass classifies source sessions and calculates yield. Evidence extraction is separately estimated before it spends.',
      [
        button('Scan and calculate yield', 'button good', runMine),
        extractAction,
      ]),
    sourceCards(overview.sources),
  );

  const yieldPanel = el('section', 'panel');
  if (overview.yield.ready) {
    yieldPanel.append(sectionHead('Yield map',
      `${overview.yield.total} grounded opportunities`,
      'Every count is supported by reclaimed evidence, not session volume alone.'));
    yieldPanel.append(yieldCards(overview.yield));
  } else {
    yieldPanel.append(sectionHead('Assay result', 'No output is recommended yet',
      overview.yield.message || 'Extract evidence before designing a production.'));
    const summary = el('div', 'assay-summary');
    append(summary,
      badge(assayReady ? 'ASSAY COMPLETE · NOT YET RECLAIMED' : 'MINE REQUIRED', assayReady ? 'free' : ''),
      el('strong', null, assayReady ? `${overview.yield.assayed} session(s) assayed` : 'No sessions assayed yet'),
      el('p', 'note', assayReady
        ? `${formatBytes(overview.yield.signal_bytes || 0)} of signal is available for a bounded evidence extraction.`
        : 'Use the primary Scan and calculate yield action before choosing a model-backed evidence scope.'),
    );
    yieldPanel.append(summary);
  }
  root.append(yieldPanel);

  if (overview.yield.ready && overview.yield.recommended?.workspace) {
    const recommended = overview.yield.recommended;
    const hero = el('section', 'hero');
    const copy = el('div');
    append(copy,
      el('div', 'eyebrow', `Recommended · ${workspaceName(recommended.workspace)}`),
      el('h2', null, recommended.title),
      el('p', null, recommended.why),
    );
    append(hero, copy, button('Design recommended bundle', 'button', () => openDesignModal({
      workspace: recommended.workspace,
      kinds: recommended.outputs,
      prompt: 'Create the strongest multi-output bundle this evidence supports and stop at reviewed drafts.',
    })));
    root.append(hero);
  }

  const evidencePanel = el('section', 'panel');
  evidencePanel.append(sectionHead('Evidence library', 'Reclaimed claims with provenance',
    'Recipes select from this redacted index; raw transcripts never enter the production chat.'));
  try {
    const nuggets = (await get('/api/nuggets?limit=60')) || [];
    if (!nuggets.length) {
      evidencePanel.append(emptyState('No evidence extracted yet',
        assayReady
          ? 'Use the primary Extract evidence action above to choose a small workspace and review the estimate.'
          : 'Use the primary Scan and calculate yield action above before extraction.'));
    } else {
      const list = el('div', 'evidence-list');
      nuggets.forEach((nugget) => list.append(evidenceRow(nugget, false)));
      evidencePanel.append(list);
    }
  } catch (error) {
    evidencePanel.append(emptyState('Evidence could not be loaded', error.message));
  }
  root.append(evidencePanel);

  root.append(journeySteps([
    ['Choose scope', 'workspace and time'],
    ['Assay', 'free classification'],
    ['Extract', 'bounded model slice'],
    ['Review evidence', 'claims and conflicts'],
    ['Reuse', 'one graph, many assets'],
  ]));
}

function evidenceRow(nugget, selectable, checked = false) {
  const row = el('div', `evidence-row ${nugget.confidence < .75 ? 'needs-review' : ''}`.trim());
  if (selectable) {
    const input = el('input');
    input.type = 'checkbox';
    input.value = nugget.uid;
    input.checked = checked;
    input.dataset.evidence = nugget.uid;
    row.append(input);
  } else {
    row.append(badge(nugget.kind));
  }
  const copy = el('div');
  const heading = el('div', 'evidence-title');
  append(heading, el('h4', null, nugget.title || humanStatus(nugget.kind)),
    nugget.confidence < .75 ? badge('needs review', 'spend') : null);
  append(copy, heading,
    el('p', null, truncateText(nugget.body || 'No preview available.', selectable ? 420 : 250)));
  row.append(copy);
  row.append(el('div', 'evidence-source',
    `${String(nugget.tool || '').toUpperCase()} · ${Math.round((nugget.confidence || 0) * 100)}%`));
  return row;
}

function openDesignModal(options) {
  if (!state.overview?.yield?.ready && !(options.evidenceIds || []).length) {
    const body = openModal('Evidence required',
      'Midden will not create an unsupported recipe. Mine your sessions, then extract a small evidence scope first.');
    const actions = el('div', 'modal-actions');
    actions.append(button('Close', 'button ghost', closeModal));
    actions.append(button('Extract evidence', 'button', () => {
      closeModal();
      openReclaimModal(options.workspace || '');
    }));
    body.append(actions);
    return;
  }
  const body = openModal(options.title || 'Design a production',
    'Describe the finished outcome. The Conductor assembles a recipe; it does not run or spend yet.');
  const workspace = selectInput(workspaceOptions(true, true), options.workspace || '');
  const prompt = textarea(options.prompt || '',
    'e.g. Turn the last three weeks of work into a tutorial, slide deck, architecture diagram, video brief, and evaluated skill proposal.');
  const title = textInput('', 'optional production name');
  append(body, field('Evidence scope', workspace), field('Finished outcome', prompt), field('Production name', title));

  const outputBox = el('div');
  outputBox.append(el('div', 'field-label', 'Deliverables · optional, leave empty to infer from the request'));
  const checks = el('div', 'checkbox-list');
  const selected = new Set(options.kinds || []);
  const templates = refineryTemplateFallback();
  templates.forEach((template) => {
    const label = el('label', 'check-card');
    const input = el('input');
    input.type = 'checkbox';
    input.value = template.kind;
    input.checked = selected.has(template.kind);
    const text = el('span');
    append(text, el('strong', null, template.title),
      el('small', null, `${template.maker} · ${template.cost_class === 'free' ? 'free' : 'spends'}`));
    append(label, input, text);
    checks.append(label);
  });
  outputBox.append(checks);

  const actions = el('div', 'modal-actions');
  actions.classList.add('modal-actions-top');
  actions.append(button('Cancel', 'button ghost', closeModal));
  const create = button('Prepare production plan', 'button', async () => {
    create.disabled = true;
    try {
      const outputKinds = $$('input[type=checkbox]:checked', checks).map((node) => node.value);
      const result = await post('/api/refinery/action', {
        action: 'design', workspace: workspace.value, prompt: prompt.value,
        title: title.value, output_kinds: outputKinds,
        evidence_ids: options.evidenceIds || [],
      });
      closeModal();
      state.overview = null;
      state.selectedRecipe = result.recipe.uid;
      state.recipeDetail = null;
      activateView('studio');
      toast('Production plan prepared · nothing has run yet', 'good');
    } catch (error) {
      create.disabled = false;
      showError(error);
    }
  });
  actions.append(create);
  body.append(actions, outputBox);
}

function refineryTemplateFallback() {
  return [
    {kind: 'tutorial', title: 'Deep technical tutorial', maker: 'Midden + Pandoc', cost_class: 'spends'},
    {kind: 'adr', title: 'Architecture decision record', maker: 'Midden', cost_class: 'spends'},
    {kind: 'release_pack', title: 'Release and launch pack', maker: 'Midden', cost_class: 'spends'},
    {kind: 'slides', title: 'Presentation deck', maker: 'Marp', cost_class: 'spends'},
    {kind: 'diagram', title: 'Architecture diagram', maker: 'D2', cost_class: 'spends'},
    {kind: 'video_brief', title: 'Video production brief', maker: 'Midden / OpenMontage', cost_class: 'spends'},
    {kind: 'handbook', title: 'Project field guide', maker: 'Quarto / Pandoc', cost_class: 'spends'},
    {kind: 'flashcards', title: 'Spaced-repetition deck', maker: 'Anki export', cost_class: 'spends'},
    {kind: 'quiz', title: 'Scenario quiz', maker: 'Midden / H5P', cost_class: 'spends'},
    {kind: 'notebook_pack', title: 'Notebook source pack', maker: 'Open Notebook', cost_class: 'free'},
    {kind: 'skill', title: 'Agent skill proposal', maker: 'Agent Skills', cost_class: 'spends'},
    {kind: 'instruction_patch', title: 'Instruction patch proposal', maker: 'Midden', cost_class: 'spends'},
    {kind: 'agent_profile', title: 'Specialist agent proposal', maker: 'Midden', cost_class: 'spends'},
    {kind: 'eval_pack', title: 'Evaluation pack', maker: 'Promptfoo', cost_class: 'free'},
    {kind: 'retrieval_pack', title: 'Retrieval memory pack', maker: 'Midden', cost_class: 'free'},
    {kind: 'sft_pack', title: 'Supervised examples pack', maker: 'Midden', cost_class: 'free'},
    {kind: 'preference_pack', title: 'Preference-pair pack', maker: 'Midden', cost_class: 'free'},
    {kind: 'privacy_manifest', title: 'Privacy manifest', maker: 'Midden', cost_class: 'free'},
    {kind: 'provenance_manifest', title: 'Provenance manifest', maker: 'Midden', cost_class: 'free'},
  ];
}

async function openRecipe(id) {
  state.selectedRecipe = id;
  state.recipeDetail = null;
  activateView('studio');
}

async function loadRecipeDetail(force = false) {
  if (!state.selectedRecipe) return null;
  if (!state.recipeDetail || force || state.recipeDetail.recipe.uid !== state.selectedRecipe) {
    state.recipeDetail = await get(`/api/refinery/recipe?id=${encodeURIComponent(state.selectedRecipe)}`);
  }
  return state.recipeDetail;
}

async function renderStudio() {
  const overview = await loadOverview();
  const root = clear($('#studio-content'));
  if (!state.selectedRecipe) {
    append(root,
      pageHead('Recipes and review', 'Create many assets from one approved evidence set.',
        'Every plan shows outputs, makers, cost, egress, review gates, and destinations before it runs.',
        [button('Design a production', 'button', () => openDesignModal({}))]),
    );
    const recipes = (overview.recipes || []).filter((recipe) => recipe.status !== 'archived');
    if (recipes.length) root.append(recipeGrid(recipes));
    else root.append(emptyState('No productions yet',
      'Describe an outcome in Conductor, review its interpretation, then create the plan.',
      button('Open Conductor', 'button ghost', () => activateView('conductor'))));
    return;
  }

  let detail;
  try {
    detail = await loadRecipeDetail();
  } catch (error) {
    state.selectedRecipe = '';
    state.recipeDetail = null;
    throw error;
  }
  const recipe = detail.recipe;
  const deterministicOnly = recipe.outputs.every((output) => !output.requires_model);
  append(root,
    pageHead('Production plan', recipe.title,
      recipe.request || 'A saved multi-output refinery production.',
      [
        button('Back to productions', 'button ghost', () => {
          state.selectedRecipe = '';
          state.recipeDetail = null;
          renderStudio().catch(showError);
        }),
        button('Clone recipe', 'button ghost', () => cloneRecipe(recipe.uid)),
      ]),
  );

  const summary = el('div', 'metric-grid');
  [
    ['Status', humanStatus(recipe.status), 'explicit workflow state'],
    ['Evidence', recipe.evidence_ids.length, `${detail.evidence_report.quality || 0}% quality`],
    ['Outputs', recipe.outputs.length, 'reviewed independently'],
    ['Estimate', deterministicOnly ? 'FREE' : detail.estimate?.unit_mid ? `~${Number(detail.estimate.unit_mid).toFixed(1)} ${detail.estimate.unit}` : formatCount(detail.estimate?.mid),
      deterministicOnly ? 'no model invocation' : detail.estimate?.samples ? 'grounded by prior runs' : 'uncalibrated'],
  ].forEach(([label, value, copy]) => {
    const card = el('article', 'metric-card');
    append(card, el('div', 'eyebrow', label), el('strong', null, value), el('span', null, copy));
    summary.append(card);
  });
  root.append(summary);

  root.append(renderRecipePlan(detail));
  if (['draft', 'evidence_review', 'approved', 'failed'].includes(recipe.status)) {
    root.append(renderEvidenceWorkbench(detail));
  }
  if (['approved', 'failed'].includes(recipe.status)) {
    root.append(renderRunCommitment(detail));
  }
  if ((detail.runs || []).length) root.append(renderRunTimeline(detail.runs[0]));
  if ((detail.outputs || []).length) root.append(renderReviewStudio(detail));
}

function renderRecipePlan(detail) {
  const recipe = detail.recipe;
  const panel = el('section', 'panel');
  panel.append(sectionHead('Recipe bundle', `${recipe.outputs.length} deliverables from one context`,
    'Outputs share a claim graph but retain separate review and export gates.',
    ['draft', 'evidence_review'].includes(recipe.status)
      ? [button('Edit deliverables', 'button ghost compact', () => editRecipeOutputs(detail))]
      : []));
  const grid = el('div', 'recipe-grid');
  recipe.outputs.forEach((output) => {
    const card = el('article', 'recipe-card');
    append(card,
      el('div', 'eyebrow', output.format),
      el('h3', null, output.title),
      el('p', null, `${output.maker}. Audience: ${output.audience}.`),
    );
    const meta = el('div', 'recipe-meta');
    append(meta, badge(output.requires_model ? 'spends' : 'free', output.requires_model ? 'spend' : 'free'),
      badge(output.maker));
    card.append(meta);
    grid.append(card);
  });
  panel.append(grid);
  return panel;
}

function editRecipeOutputs(detail) {
  const body = openModal('Edit deliverables',
    'Changing the output mix returns the recipe to evidence review.');
  const selected = new Set(detail.recipe.outputs.map((output) => output.kind));
  const checks = el('div', 'checkbox-list');
  (detail.templates || refineryTemplateFallback()).forEach((template) => {
    const label = el('label', 'check-card');
    const input = el('input');
    input.type = 'checkbox';
    input.value = template.kind;
    input.checked = selected.has(template.kind);
    const copy = el('span');
    append(copy, el('strong', null, template.title),
      el('small', null, `${template.maker} · ${template.requires_model ? 'spends' : 'free'}`));
    append(label, input, copy);
    checks.append(label);
  });
  body.append(checks);
  const actions = el('div', 'modal-actions');
  actions.append(button('Cancel', 'button ghost', closeModal));
  actions.append(button('Save deliverables', 'button', async () => {
    const kinds = $$('input:checked', checks).map((input) => input.value);
    if (!kinds.length) {
      toast('Choose at least one deliverable', 'bad');
      return;
    }
    try {
      await post('/api/refinery/action', {
        action: 'update_recipe', recipe_id: detail.recipe.uid, output_kinds: kinds,
      });
      closeModal();
      state.recipeDetail = null;
      state.overview = null;
      await renderStudio();
      toast('Deliverables updated; review the evidence again.', 'good');
    } catch (error) {
      showError(error);
    }
  }));
  body.append(actions);
}

function renderEvidenceWorkbench(detail) {
  const recipe = detail.recipe;
  const report = detail.evidence_report || {};
  const selected = new Set(recipe.evidence_ids || []);
  const panel = el('section', 'panel');
  panel.append(sectionHead('Evidence workbench', 'Control what every output is allowed to claim',
    'Changes here propagate to the tutorial, slides, visuals, knowledge packs, and agent proposals.'));
  const layout = el('div', 'evidence-layout');
  const left = el('div');
  const filters = el('div', 'filter-row evidence-filters');
  const search = textInput('', 'Search selected evidence');
  const needsReview = el('input');
  needsReview.type = 'checkbox';
  const reviewLabel = el('label', 'check');
  append(reviewLabel, needsReview, el('span', null, 'Needs review only'));
  append(filters, search, reviewLabel);
  const list = el('div', 'evidence-list');
  const candidatesByID = new Map((detail.candidates || []).map((nugget) => [nugget.uid, nugget]));
  (detail.evidence || []).forEach((nugget) => candidatesByID.set(nugget.uid, nugget));
  const allCandidates = Array.from(candidatesByID.values());
  const selectedCandidates = allCandidates.filter((nugget) => selected.has(nugget.uid));
  const unselectedCandidates = allCandidates.filter((nugget) => !selected.has(nugget.uid));
  const displayedCandidates = selectedCandidates.concat(
    unselectedCandidates.slice(0, Math.max(0, 180 - selectedCandidates.length)),
  );
  const renderCandidates = () => {
    clear(list);
    const query = search.value.trim().toLowerCase();
    displayedCandidates
      .filter((nugget) => !needsReview.checked || nugget.confidence < .75)
      .filter((nugget) => !query ||
        String(nugget.title || '').toLowerCase().includes(query) ||
        String(nugget.body || '').toLowerCase().includes(query) ||
        String(nugget.kind || '').toLowerCase().includes(query))
      .forEach((nugget) => {
        list.append(evidenceRow(nugget, true, selected.has(nugget.uid)));
      });
    if (!list.children.length) {
      list.append(emptyState('No evidence matches', 'Clear the search or review-only filter.'));
    }
  };
  search.addEventListener('input', renderCandidates);
  needsReview.addEventListener('change', renderCandidates);
  renderCandidates();
  left.append(filters, list);
  const right = el('div', 'rail');
  const quality = el('div', 'rail-card');
  append(quality,
    el('div', 'eyebrow', 'Evidence quality'),
    el('h3', null, `${report.quality || 0}% bundle quality`),
    el('p', null, `${report.selected || 0} selected · ${report.coverage || 0}% claim-type coverage`),
  );
  const bar = el('div', 'quality-bar');
  const fill = el('i');
  fill.style.width = `${report.quality || 0}%`;
  bar.append(fill);
  quality.append(bar);
  right.append(quality);
  (report.warnings || []).forEach((warning) => right.append(el('div', 'warning', warning)));
  if (!(report.warnings || []).length) right.append(el('div', 'success', 'The evidence set is ready for an explicit approval.'));

  const controls = el('div', 'rail-card');
  append(controls, el('h3', null, 'Evidence gate'),
    el('p', null, 'Save selections freely. Approval is a separate state transition before any run.'));
  const save = button('Save selection', 'button ghost', () => saveEvidenceSelection(detail, false));
  const approve = button('Approve evidence set', 'button good', () => saveEvidenceSelection(detail, true));
  append(controls, el('div', 'row-actions'), save, approve);
  const actionWrap = $('.row-actions', controls);
  actionWrap.append(save, approve);
  right.append(controls);
  append(layout, left, right);
  panel.append(layout);
  return panel;
}

async function saveEvidenceSelection(detail, approve) {
  const root = $('#studio-content');
  const inputs = $$('input[data-evidence]', root);
  const visibleIDs = new Set(inputs.map((input) => input.value));
  const ids = new Set((detail.recipe.evidence_ids || []).filter((id) => !visibleIDs.has(id)));
  inputs.filter((input) => input.checked).forEach((input) => ids.add(input.value));
  try {
    await post('/api/refinery/action', {
      action: approve ? 'approve_evidence' : 'save_evidence',
      recipe_id: detail.recipe.uid,
      evidence_ids: Array.from(ids),
    });
    state.recipeDetail = null;
    state.overview = null;
    await renderStudio();
    toast(approve ? 'Evidence approved. The recipe can now run.' : 'Evidence selection saved.', 'good');
  } catch (error) {
    showError(error);
  }
}

function renderRunCommitment(detail) {
  const panel = el('section', 'hero');
  const copy = el('div');
  const modelOutputs = detail.recipe.outputs.filter((output) => output.requires_model).length;
  append(copy,
    el('div', 'eyebrow', 'Commitment'),
    el('h2', null, 'Generate drafts only.'),
    el('p', null, `${modelOutputs} output(s) use one warm model context. Deterministic packs stay local and free. The run writes only to Midden’s artifact workspace and stops before publishing, installation, upload, or training.`),
  );
  append(panel, copy, button('Preview cost and run', 'button good', () => previewProduction(detail.recipe.uid)));
  return panel;
}

async function previewProduction(recipeID) {
  try {
    const preview = await runJob({op: 'production', recipe_id: recipeID, apply: false},
      'Previewing production', 'Calculates the full bundle estimate from the approved evidence and output mix.');
    const result = preview.result || {};
    const modelOutputs = (result.recipe?.outputs || []).filter((output) => output.requires_model).length;
    const body = openModal('Approve production run',
      'This approval creates drafts only. No destination receives anything.');
    const estimate = result.estimate || preview.estimate || {};
    const box = el('div', 'estimate-box');
    append(box,
      el('div', 'eyebrow', modelOutputs === 0 ? 'Free local production' : estimate.samples ? `${estimate.samples} calibrated prior run(s)` : 'Uncalibrated estimate'),
      el('strong', null, result.estimate_text || (modelOutputs === 0 ? '0 model calls' : `${formatCount(estimate.low)}–${formatCount(estimate.high)} tokens`)),
    );
    const facts = el('div', 'estimate-facts');
    [
      ['Outputs', String(result.recipe?.outputs?.length || 0)],
      ['Evidence', `${result.evidence_report?.selected || 0} item(s)`],
      ['Backend', result.backend || 'auto-detect signed-in CLI'],
      ['Model', result.model || 'backend default'],
      ['Time', modelOutputs === 0 ? 'immediate' : `about ${Math.max(1, Math.ceil((result.estimated_seconds || 60) / 60))} minute(s)`],
      ['External writes', 'none'],
    ].forEach(([label, value]) => {
      const item = el('div', 'estimate-fact');
      append(item, el('span', null, label), el('strong', null, value));
      facts.append(item);
    });
    box.append(facts);
    body.append(box);
    if (modelOutputs > 0 && !estimate.samples) {
      body.append(el('div', 'warning',
        'This is a conservative first-run token range. Complete one small run to calibrate future credit and time estimates.'));
    }
    const fields = modelOutputs > 0 ? backendFields() : {backend: {value: ''}, model: {value: ''}};
    if (modelOutputs > 0) {
      append(body, field('Model backend', fields.backend), field('Model override', fields.model));
    }
    const actions = el('div', 'modal-actions');
    actions.append(button('Not now', 'button ghost', closeModal));
    const run = button('Approve and run', 'button good', async () => {
      run.disabled = true;
      closeModal();
      try {
        const finished = await runJob({
          op: 'production', recipe_id: recipeID, apply: true,
          backend: fields.backend.value, model: fields.model.value,
        }, 'Running production', 'Every stage writes a durable timeline and pauses at the review studio.');
        toast(`${finished.result?.outputs?.length || 0} draft(s) ready for review`, 'good');
        state.recipeDetail = null;
        state.overview = null;
        await renderStudio();
      } catch (error) {
        showError(error);
        state.recipeDetail = null;
        state.overview = null;
        await renderStudio().catch(showError);
      }
    });
    actions.append(run);
    body.append(actions);
  } catch (error) {
    showError(error);
  }
}

function renderRunTimeline(run) {
  const panel = el('section', 'run-card');
  panel.append(sectionHead('Run timeline', `Production ${humanStatus(run.status)}`,
    'Every transformation and checkpoint remains visible after the browser closes.'));
  (run.stages || []).forEach((stage) => {
    const row = el('div', 'run-line');
    const time = el('time', null, stage.started_at ? formatDate(stage.started_at) : '--');
    const dot = el('span', `status-dot ${stage.status || ''}`.trim());
    const copy = el('div');
    append(copy, el('strong', null, stage.label), el('small', null, stage.detail || 'Waiting'));
    append(row, time, dot, copy, badge(humanStatus(stage.status), statusKind(stage.status)));
    panel.append(row);
  });
  if (run.error) panel.append(el('div', 'warning', run.error));
  return panel;
}

function renderReviewStudio(detail) {
  const panel = el('section', 'panel');
  panel.append(sectionHead('Review studio', 'Inspect every draft before export',
    'Edit the source, trace provenance, approve or reject it, then choose a destination.'));
  const grid = el('div', 'output-grid');
  detail.outputs.forEach((output) => {
    const card = el('article', 'output-card');
    const preview = `${String(output.kind).toUpperCase()}\n${output.format} · ${output.maker}`;
    append(card, el('div', 'output-preview', preview), el('h3', null, output.title),
      el('p', null, `${output.evidence_ids.length} evidence item(s) · ${output.quality}% quality`));
    const meta = el('div', 'output-meta');
    append(meta, badge(humanStatus(output.status), statusKind(output.status)), badge(output.format));
    card.append(meta);
    const actions = el('div', 'card-actions');
    actions.append(button(output.status === 'draft' ? 'Review' : 'Open', 'button', () => reviewOutput(output.uid)));
    if (['reviewed', 'exported'].includes(output.status)) {
      actions.append(button(output.status === 'exported' ? 'Exported' : 'Export local', 'button ghost',
        () => exportOutput(output.uid)));
    }
    card.append(actions);
    grid.append(card);
  });
  panel.append(grid);
  return panel;
}

async function reviewOutput(outputID) {
  try {
    const detail = await get(`/api/refinery/output?id=${encodeURIComponent(outputID)}`);
    const output = detail.output;
    const body = openDrawer(output.title);
    body.classList.add('review-workbench');
    const meta = el('div', 'row-actions');
    append(meta, badge(humanStatus(output.status), statusKind(output.status)),
      badge(output.format), badge(`${output.evidence_ids.length} evidence`, 'free'));
    body.append(meta);
    const tabs = el('div', 'tabs review-tabs');
    const previewTab = button('Review', 'tab-button active');
    const rawTab = button('Raw source', 'tab-button');
    const provenanceTab = button('Provenance', 'tab-button');
    append(tabs, previewTab, rawTab, provenanceTab);
    body.append(tabs);

    const reviewPanel = el('div', 'structured-review');
    const rawPanel = el('div', 'raw-review');
    rawPanel.hidden = true;
    const provenancePanel = el('div', 'provenance-review');
    provenancePanel.hidden = true;
    const editor = textarea(detail.body || '');
    editor.className = 'document-editor';
    rawPanel.append(field('Editable source', editor));
    const structured = structuredOutput(detail.body || '', output.format);
    renderStructuredOutput(reviewPanel, structured, output);
    renderProvenance(provenancePanel, detail.provenance);
    append(body, reviewPanel, rawPanel, provenancePanel);

    let active = 'review';
    let rawDirty = false;
    editor.addEventListener('input', () => { rawDirty = true; });
    const activate = (name) => {
      active = name;
      previewTab.classList.toggle('active', name === 'review');
      rawTab.classList.toggle('active', name === 'raw');
      provenanceTab.classList.toggle('active', name === 'provenance');
      reviewPanel.hidden = name !== 'review';
      rawPanel.hidden = name !== 'raw';
      provenancePanel.hidden = name !== 'provenance';
    };
    previewTab.addEventListener('click', () => activate('review'));
    rawTab.addEventListener('click', () => activate('raw'));
    provenanceTab.addEventListener('click', () => activate('provenance'));

    const currentBody = () => {
      if (rawDirty || active === 'raw' || structured.kind !== 'jsonl') return editor.value;
      return structured.records
        .filter((record) => record.selected)
        .map((record) => JSON.stringify(record.value))
        .join('\n') + '\n';
    };
    const currentEvidenceIDs = () => structured.kind === 'jsonl'
      ? (rawDirty || active === 'raw' ? null : Array.from(new Set(structured.records
        .filter((record) => record.selected)
        .flatMap((record) => recordEvidenceIDs(record.value)))))
      : null;
    const actions = el('div', 'modal-actions');
    actions.append(button('Reject', 'button danger', () => saveOutputReview(output, currentBody(), 'rejected', currentEvidenceIDs())));
    actions.append(button('Save draft', 'button ghost', () => saveOutputReview(output, currentBody(), 'draft', currentEvidenceIDs())));
    actions.append(button('Approve output', 'button good', () => saveOutputReview(output, currentBody(), 'reviewed', currentEvidenceIDs())));
    body.append(actions);
  } catch (error) {
    showError(error);
  }
}

function recordEvidenceIDs(value) {
  return [
    value?.id,
    value?.evidence_id,
    value?.metadata?.evidence_id,
    value?.provenance?.evidence_id,
    value?.provenance?.chosen_evidence_id,
    value?.provenance?.rejected_evidence_id,
  ].filter(Boolean);
}

function structuredOutput(body, format) {
  if (format === 'jsonl') {
    const records = [];
    for (const [index, line] of body.split(/\r?\n/).entries()) {
      if (!line.trim()) continue;
      try {
        records.push({index, value: JSON.parse(line), selected: true});
      } catch {
        return {kind: 'text', body, error: `Line ${index + 1} is not valid JSON.`};
      }
    }
    return {kind: 'jsonl', records};
  }
  if (format === 'json') {
    try {
      return {kind: 'json', value: JSON.parse(body)};
    } catch {
      return {kind: 'text', body, error: 'This file is not valid JSON.'};
    }
  }
  return {kind: 'text', body};
}

function recordTitle(value, index) {
  return value?.metadata?.title || value?.description || value?.instruction ||
    value?.prompt || value?.title || `Record ${index + 1}`;
}

function recordBody(value) {
  return value?.text || value?.response || value?.expected || value?.chosen ||
    value?.content || JSON.stringify(value, null, 2);
}

function renderStructuredOutput(panel, structured, output) {
  clear(panel);
  if (structured.error) panel.append(el('div', 'warning', structured.error));
  if (structured.kind === 'jsonl') {
    const toolbar = el('div', 'review-toolbar');
    const count = el('strong', null, `${structured.records.length} record(s)`);
    const selectAll = button('Select all', 'button ghost compact', () => {
      structured.records.forEach((record) => { record.selected = true; });
      $$('input[data-record-index]', panel).forEach((input) => { input.checked = true; });
      updateCount();
    });
    const selectNone = button('Select none', 'button ghost compact', () => {
      structured.records.forEach((record) => { record.selected = false; });
      $$('input[data-record-index]', panel).forEach((input) => { input.checked = false; });
      updateCount();
    });
    const updateCount = () => {
      count.textContent = `${structured.records.filter((record) => record.selected).length} of ${structured.records.length} included`;
    };
    append(toolbar, count, el('div', 'row-actions'));
    $('.row-actions', toolbar).append(selectAll, selectNone);
    panel.append(toolbar);
    const list = el('div', 'record-review-list');
    structured.records.forEach((record, index) => {
      const card = el('article', 'record-review-card');
      const check = el('input');
      check.type = 'checkbox';
      check.checked = true;
      check.dataset.recordIndex = String(index);
      check.addEventListener('change', () => {
        record.selected = check.checked;
        card.classList.toggle('excluded', !check.checked);
        updateCount();
      });
      const copy = el('div');
      const title = el('h3', null, recordTitle(record.value, index));
      const metadata = el('div', 'record-meta');
      const kind = record.value?.metadata?.kind || record.value?.kind || output.kind;
      const confidence = record.value?.metadata?.confidence ?? record.value?.confidence;
      append(metadata, badge(kind || 'record'),
        confidence !== undefined ? badge(`${Math.round(Number(confidence) * 100)}% confidence`, Number(confidence) < .75 ? 'spend' : 'free') : null,
        record.value?.metadata?.tool ? badge(record.value.metadata.tool) : null);
      const text = el('p', null, recordBody(record.value));
      append(copy, title, metadata, text);
      append(card, check, copy);
      list.append(card);
    });
    panel.append(list);
    updateCount();
    return;
  }
  if (structured.kind === 'json') {
    const summary = el('div', 'json-summary');
    Object.entries(structured.value || {}).slice(0, 12).forEach(([key, value]) => {
      const row = el('div', 'json-summary-row');
      append(row, el('strong', null, humanStatus(key)),
        el('span', null, typeof value === 'object' ? JSON.stringify(value, null, 2) : String(value)));
      summary.append(row);
    });
    panel.append(summary);
    return;
  }
  panel.append(el('pre', 'document-preview', structured.body || 'This output is empty.'));
}

function renderProvenance(panel, body) {
  clear(panel);
  if (!body) {
    panel.append(emptyState('No provenance sidecar found', 'This output cannot be approved until provenance is available.'));
    return;
  }
  try {
    const parsed = JSON.parse(body);
    append(panel,
      metricGrid([
        {label: 'Recipe', value: String(parsed.recipe_id || '').slice(0, 8), copy: parsed.title || 'production'},
        {label: 'Model', value: parsed.model || 'deterministic', copy: parsed.maker || 'Midden'},
        {label: 'Evidence', value: parsed.evidence?.length || 0, copy: 'source records'},
        {label: 'Raw transcripts', value: parsed.raw_transcripts_included ? 'included' : 'excluded', copy: 'privacy boundary'},
      ]),
    );
    const list = el('div', 'provenance-list');
    (parsed.evidence || []).forEach((item) => {
      const row = el('article', 'provenance-card');
      append(row, el('h3', null, item.title || item.evidence_id),
        el('p', 'note', `${item.tool || 'source'} · ${String(item.session_id || '').slice(0, 8)} · ${Math.round(Number(item.confidence || 0) * 100)}%`));
      list.append(row);
    });
    panel.append(list);
  } catch {
    panel.append(el('pre', 'provenance', body));
  }
}

async function saveOutputReview(output, body, decision, evidenceIDs = null) {
  try {
    await post('/api/refinery/action', {
      action: 'review_output', output_id: output.uid, decision, body,
      evidence_ids: evidenceIDs,
    });
    closeDrawer();
    state.recipeDetail = null;
    state.overview = null;
    await renderStudio();
    toast(`Output ${humanStatus(decision)}`, decision === 'rejected' ? '' : 'good');
  } catch (error) {
    showError(error);
  }
}

async function exportOutput(outputID) {
  try {
    const result = await post('/api/refinery/action', {
      action: 'export_output', output_id: outputID, destination: 'local_vault',
    });
    state.recipeDetail = null;
    state.overview = null;
    await renderStudio();
    notice('The reviewed output and provenance were exported locally.', 'good', [
      button('Copy path', 'button ghost compact', () => copyText(result.path)),
    ]);
  } catch (error) {
    showError(error);
  }
}

async function cloneRecipe(recipeID) {
  try {
    const result = await post('/api/refinery/action', {action: 'clone_recipe', recipe_id: recipeID});
    state.overview = null;
    state.selectedRecipe = result.recipe.uid;
    state.recipeDetail = null;
    await renderStudio();
    toast('Recipe cloned against the current evidence set.', 'good');
  } catch (error) {
    showError(error);
  }
}

async function renderKnowledge() {
  const overview = await loadOverview();
  const root = clear($('#knowledge-content'));
  append(root,
    pageHead('Knowledge and learning', 'Turn lessons into a system you can revisit.',
      'The same approved evidence becomes durable reference material, retrieval context, and spaced repetition.'),
  );
  const grid = el('div', 'recipe-grid');
  [
    {
      kicker: 'Handbook', title: 'Project field guide',
      copy: 'Architecture, decisions, commands, troubleshooting, and glossary.',
      kinds: ['handbook', 'provenance_manifest'],
    },
    {
      kicker: 'Learning', title: 'Flashcards and scenario quiz',
      copy: 'Atomic cards, applied questions, rationales, and source citations.',
      kinds: ['flashcards', 'quiz', 'provenance_manifest'],
    },
    {
      kicker: 'Research', title: 'Local notebook source pack',
      copy: 'A reviewed, redacted source pack for a local vault or Open Notebook.',
      kinds: ['notebook_pack', 'retrieval_pack', 'provenance_manifest'],
    },
  ].forEach((item) => {
    const card = el('article', 'recipe-card');
    append(card, el('div', 'eyebrow', item.kicker), el('h3', null, item.title), el('p', null, item.copy));
    const actions = el('div', 'card-actions');
    actions.append(button('Build this pack', 'button', () => openDesignModal({
      prompt: `Create ${item.title.toLowerCase()} from the selected project evidence.`,
      kinds: item.kinds,
    })));
    card.append(actions);
    grid.append(card);
  });
  root.append(grid);

  const outputs = (overview.outputs || []).filter((output) =>
    ['handbook', 'flashcards', 'quiz', 'notebook_pack', 'retrieval_pack'].includes(output.kind));
  const panel = el('section', 'panel');
  panel.append(sectionHead('Knowledge library',
    outputs.length ? `${outputs.length} knowledge output(s)` : 'No knowledge packs yet',
    'Connections appear only after a reviewed pack needs a destination.'));
  if (outputs.length) {
    const outputGrid = el('div', 'output-grid');
    outputs.forEach((output) => {
      const card = el('article', 'output-card');
      append(card, el('div', 'output-preview', `${output.kind.toUpperCase()}\n${output.format}`),
        el('h3', null, output.title), el('p', null, `${output.quality}% evidence quality`));
      const actions = el('div', 'card-actions');
      actions.append(button('Open production', 'button', () => openRecipe(output.recipe_id)));
      card.append(actions);
      outputGrid.append(card);
    });
    panel.append(outputGrid);
  } else {
    panel.append(emptyState('Keep the learning',
      'Choose a workspace and build a handbook, flashcards, quiz, or notebook pack.',
      button('Build knowledge pack', 'button', () => openDesignModal({
        kinds: ['handbook', 'flashcards', 'quiz', 'notebook_pack', 'provenance_manifest'],
      }))));
  }
  root.append(panel);
  root.append(journeySteps([
    ['Choose lessons', 'workspace evidence'],
    ['Build pack', 'handbook and learning'],
    ['Review truth', 'citations and gaps'],
    ['Connect destination', 'only when needed'],
    ['Return', 'new evidence and cadence'],
  ]));
}

async function renderAgentForge() {
  const overview = await loadOverview();
  const root = clear($('#agent-content'));
  append(root,
    pageHead('Agent improvement', 'Turn repeated mistakes into measurable proposals.',
      'Midden proposes skills, instructions, and specialists from repeated evidence, creates regression cases, and installs nothing automatically.'),
  );
  const proposals = overview.agent_proposals || [];
  if (proposals.length) {
    const grid = el('div', 'proposal-grid');
    proposals.forEach((proposal) => {
      const card = el('article', 'proposal-card');
      append(card,
        el('div', 'eyebrow', humanStatus(proposal.kind)),
        el('h3', null, proposal.title),
        el('p', null, proposal.summary),
      );
      const meta = el('div', 'recipe-meta');
      append(meta, badge(`${proposal.support} observations`, 'free'),
        badge(`${proposal.eval_cases} evals`), badge('proposal only', 'danger'));
      card.append(meta);
      const actions = el('div', 'card-actions');
      actions.append(button('Prepare proposal + evals', 'button', () => openDesignModal({
        workspace: proposal.workspace,
        prompt: `Use the repeated evidence behind "${proposal.title}" to propose a scoped ${humanStatus(proposal.kind)} and an evaluation pack. Do not install anything.`,
        kinds: [proposal.kind, 'eval_pack', 'provenance_manifest'],
        evidenceIds: proposal.evidence_ids,
      })));
      card.append(actions);
      grid.append(card);
    });
    root.append(grid);
  } else {
    root.append(emptyState('No repeated pattern is strong enough yet',
      'Single observations never become agent rules. Mine more work or review the evidence library.',
      button('Mine new work', 'button', runMine)));
  }

  const gate = el('section', 'panel');
  gate.append(sectionHead('Evaluation before installation', 'Evidence → proposal → eval → approval',
    'A plausible instruction is not enough. The proposed behavior must beat the current behavior on held-out cases.'));
  const list = el('div', 'step-list');
  [
    ['A', 'Generate evidence-derived cases', 'Successes, failures, edge cases, and adversarial variants.', 'FREE'],
    ['B', 'Run current vs proposed behavior', 'Use Promptfoo or a selected-agent comparison.', 'SPENDS'],
    ['C', 'Approve one reversible scope', 'Project, user, or selected agent only.', 'HUMAN GATE'],
    ['D', 'Monitor later sessions', 'Measure whether the original failure repeats.', 'EVIDENCE'],
  ].forEach(([n, title, copy, status]) => {
    const row = el('div', 'step-row');
    const text = el('div');
    append(text, el('strong', null, title), el('small', null, copy));
    append(row, el('span', 'step-number', n), text, badge(status, status === 'SPENDS' ? 'spend' : status === 'HUMAN GATE' ? 'danger' : 'free'));
    list.append(row);
  });
  gate.append(list);
  root.append(gate);

  const outputs = (overview.outputs || []).filter((output) =>
    ['skill', 'instruction_patch', 'agent_profile', 'eval_pack'].includes(output.kind));
  if (outputs.length) {
    const panel = el('section', 'panel');
    panel.append(sectionHead('Proposal history', `${outputs.length} draft or reviewed improvement output(s)`,
      'Open the parent production to inspect evidence, evals, and review state.'));
    const grid = el('div', 'output-grid');
    outputs.forEach((output) => {
      const card = el('article', 'output-card');
      append(card, el('div', 'output-preview', `${output.kind.toUpperCase()}\nPROPOSAL · NOT INSTALLED`),
        el('h3', null, output.title), el('p', null, `${output.evidence_ids.length} evidence item(s)`));
      const actions = el('div', 'card-actions');
      actions.append(button('Open production', 'button', () => openRecipe(output.recipe_id)));
      card.append(actions);
      grid.append(card);
    });
    panel.append(grid);
    root.append(panel);
  }
}

async function renderPersonalization() {
  const overview = await loadOverview();
  const report = overview.personalization || {};
  const root = clear($('#personalization-content'));
  append(root,
    pageHead('Personalization lab', report.training_ready
      ? 'Your evidence passes the conservative data-volume gates.'
      : 'Your history can support useful packs before a reliable fine-tune.',
    'Midden separates retrieval memory, supervised examples, preference pairs, and held-out evals instead of dumping transcripts into training.'),
    metricGrid([
      {label: 'Retrieval memories', value: formatCount(report.retrieval), copy: 'useful immediately after review'},
      {label: 'SFT candidates', value: formatCount(report.sft), copy: 'high-confidence examples'},
      {label: 'Preference pairs', value: formatCount(report.preference_pairs), copy: 'accepted versus rejected'},
      {label: 'Eval cases', value: formatCount(report.evals), copy: 'held-out expected behavior'},
    ]),
  );

  const layout = el('div', 'grid-2');
  const gates = el('section', 'panel');
  gates.append(sectionHead('Quality and privacy gates', 'Training stays locked until every gate passes',
    'Retrieval and eval exports remain valuable even when training is not ready.'));
  const list = el('div', 'step-list');
  (report.gates || []).forEach((gate, index) => {
    const row = el('div', 'step-row');
    const copy = el('div');
    append(copy, el('strong', null, gate.label),
      el('small', null, `${gate.detail} Current: ${gate.current}${gate.target ? ` / ${gate.target}` : ''}.`));
    append(row, el('span', 'step-number', index + 1), copy,
      badge(humanStatus(gate.status), statusKind(gate.status)));
    list.append(row);
  });
  gates.append(list);

  const recommendation = el('section', 'panel');
  recommendation.append(el('div', report.training_ready ? 'success' : 'warning', report.recommendation));
  const actions = el('div', 'row-actions');
  actions.append(button('Prepare safe data pack', 'button', () => openDesignModal({
    prompt: 'Prepare a local, privacy-reviewed retrieval, SFT, preference, and evaluation data pack. Include provenance and privacy manifests. Do not train or upload anything.',
    kinds: ['retrieval_pack', 'sft_pack', 'preference_pack', 'eval_pack', 'privacy_manifest', 'provenance_manifest'],
  })));
  actions.append(button('Inspect evidence', 'button ghost', () => activateView('mine')));
  recommendation.append(actions);
  append(layout, gates, recommendation);
  root.append(layout);

  const outputs = (overview.outputs || []).filter((output) =>
    ['retrieval_pack', 'sft_pack', 'preference_pack', 'eval_pack', 'privacy_manifest'].includes(output.kind));
  if (outputs.length) {
    const panel = el('section', 'panel');
    panel.append(sectionHead('Private data packs', `${outputs.length} generated output(s)`,
      'Every pack remains local and review-gated.'));
    const grid = el('div', 'output-grid');
    outputs.forEach((output) => {
      const card = el('article', 'output-card');
      append(card, el('div', 'output-preview', `${output.kind.toUpperCase()}\nLOCAL · PROVENANCED`),
        el('h3', null, output.title), el('p', null, humanStatus(output.status)));
      const actions = el('div', 'card-actions');
      actions.append(button('Open production', 'button', () => openRecipe(output.recipe_id)));
      card.append(actions);
      grid.append(card);
    });
    panel.append(grid);
    root.append(panel);
  }
}

async function loadConnections(force = false) {
  if (!state.connections || force) {
    const [connections, integrations] = await Promise.all([
      get('/api/refinery/connections'),
      get('/api/integrations'),
    ]);
    state.connections = connections;
    state.integrations = integrations;
  }
  return {connections: state.connections, integrations: state.integrations};
}

async function renderConnections() {
  const root = clear($('#connections-content'));
  append(root, pageHead('Capabilities in context', 'Connect tools when a recipe needs them.',
    'Midden groups capabilities by the outcome they unlock. Opening this page performs local detection only; service tests remain explicit.'));
  let data;
  try {
    data = await loadConnections();
  } catch (error) {
    root.append(emptyState('Connections could not be loaded', error.message));
    return;
  }
  const integrationByID = new Map((data.integrations || []).map((item) => [item.id, item]));
  const groups = new Map();
  data.connections.forEach((connection) => {
    if (!groups.has(connection.category)) groups.set(connection.category, []);
    groups.get(connection.category).push(connection);
  });
  let groupIndex = 0;
  groups.forEach((connections, category) => {
    const section = el('details', 'connection-group');
    section.open = groupIndex < 2 || connections.some((connection) => connection.managed || connection.status === 'ready');
    const summary = el('summary', 'connection-group-summary');
    const readyCount = connections.filter((connection) => connection.status === 'ready').length;
    append(summary, el('h2', null, category),
      badge(readyCount ? `${readyCount} ready` : `${connections.length} capability`, readyCount ? 'ready' : ''));
    section.append(summary);
    const grid = el('div', 'connection-grid');
    connections.forEach((connection) => {
      const card = el('article', 'connection-card');
      append(card, el('div', 'eyebrow', connection.outcome), el('h3', null, connection.name),
        el('p', null, connection.description));
      const meta = el('div', 'connection-meta');
      append(meta, badge(humanStatus(connection.status), statusKind(connection.status)),
        badge(connection.cost, connection.cost === 'spends' ? 'spend' : connection.cost === 'lab' ? 'lab' : 'free'));
      card.append(meta);
      card.append(el('div', 'connection-outcome', connection.detail));
      const actions = el('div', 'card-actions');
      const managed = integrationByID.get(connection.id);
      if (managed) {
        actions.append(button(managed.state === 'not_set_up' ? 'Set up' : 'Edit setup', 'button ghost',
          () => configureIntegration(managed)));
        if (!['not_set_up', 'turned_off'].includes(managed.state)) {
          actions.append(button('Test explicitly', 'button', () => testIntegration(managed.id)));
        }
      } else if (connection.id === 'training') {
        actions.append(button('View readiness', 'button ghost', () => activateView('personalization')));
      } else if (connection.id === 'anki') {
        actions.append(button('Build learning pack', 'button ghost', () => activateView('knowledge')));
      } else if (connection.status === 'ready') {
        actions.append(badge('available now', 'ready'));
      } else {
        actions.append(button(connection.action, 'button ghost', () => {
          toast(connection.detail);
        }));
      }
      card.append(actions);
      grid.append(card);
    });
    section.append(grid);
    root.append(section);
    groupIndex += 1;
  });

  const advanced = el('section', 'panel');
  advanced.append(sectionHead('Advanced configuration', 'Declarative manifests remain available',
    'Midden never probes an advanced target until you explicitly request it.',
    [button('Check advanced manifests', 'button ghost compact', probeAdvancedPlugins)]));
  const list = el('div');
  advanced.append(list);
  try {
    const plugins = state.plugins || await get('/api/plugins');
    state.plugins = plugins;
    renderPluginList(list, plugins);
  } catch (error) {
    list.append(el('p', 'note', error.message));
  }
  root.append(advanced);
}

function configureIntegration(item) {
  if (item.id === 'open-notebook') return configureOpenNotebook(item);
  if (item.id === 'openmontage') return configureOpenMontage(item);
}

function configureOpenNotebook(item) {
  const body = openModal('Set up Open Notebook',
    'Midden saves local URLs and whether a password is required. Password values remain action-scoped and are never stored.');
  const api = textInput(item.settings?.api_url || 'http://127.0.0.1:5055/api');
  const ui = textInput(item.settings?.ui_url || 'http://127.0.0.1:8502');
  const password = el('input');
  password.type = 'checkbox';
  password.checked = Boolean(item.settings?.password_required);
  const passwordLabel = el('label', 'check-card');
  const copy = el('span');
  append(copy, el('strong', null, 'Password required'),
    el('small', null, 'The password is requested only when testing or sending.'));
  append(passwordLabel, password, copy);
  append(body, field('API URL', api), field('UI URL', ui), passwordLabel);
  if (item.github_url) {
    const link = el('a', null, 'Open official setup guide');
    link.href = item.github_url;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    body.append(link);
  }
  const actions = el('div', 'modal-actions');
  actions.append(button('Cancel', 'button ghost', closeModal));
  actions.append(button('Save setup', 'button', async () => {
    try {
      await post('/api/integrations/configure', {
        id: item.id, enabled: true, api_url: api.value, ui_url: ui.value,
        password_required: password.checked, replace_legacy: item.legacy,
      });
      closeModal();
      state.connections = null;
      state.integrations = null;
      await renderConnections();
      toast('Open Notebook setup saved. Test it explicitly when ready.', 'good');
    } catch (error) {
      showError(error);
    }
  }));
  body.append(actions);
}

function configureOpenMontage(item) {
  const body = openModal('Set up OpenMontage',
    'Midden records the checked-out repository path and the signed-in CLI OpenMontage should use. It does not install dependencies.');
  const home = textInput(item.settings?.home || '', 'absolute path to OpenMontage');
  const backend = selectInput([
    {value: 'copilot', label: 'Copilot CLI'},
    {value: 'claude', label: 'Claude Code'},
    {value: 'opencode', label: 'OpenCode'},
  ], item.settings?.backend || 'copilot');
  append(body, field('OpenMontage home', home), field('Model backend', backend));
  if (item.github_url) {
    const link = el('a', null, 'Open official setup guide');
    link.href = item.github_url;
    link.target = '_blank';
    link.rel = 'noopener noreferrer';
    body.append(link);
  }
  const actions = el('div', 'modal-actions');
  actions.append(button('Cancel', 'button ghost', closeModal));
  actions.append(button('Save setup', 'button', async () => {
    try {
      await post('/api/integrations/configure', {
        id: item.id, enabled: true, home: home.value, backend: backend.value,
        replace_legacy: item.legacy,
      });
      closeModal();
      state.connections = null;
      state.integrations = null;
      await renderConnections();
      toast('OpenMontage setup saved. Test prerequisites explicitly.', 'good');
    } catch (error) {
      showError(error);
    }
  }));
  body.append(actions);
}

async function testIntegration(id) {
  try {
    await post('/api/integrations/test', {id});
    state.connections = null;
    state.integrations = null;
    await renderConnections();
    toast('Integration test complete.', 'good');
  } catch (error) {
    showError(error);
  }
}

function renderPluginList(root, plugins) {
  clear(root);
  if (!plugins.length) {
    root.append(el('p', 'note', 'No advanced manifests are installed.'));
    return;
  }
  const tableWrap = el('div', 'table-wrap');
  const table = el('table');
  const head = el('thead');
  const row = el('tr');
  ['Name', 'Kind', 'Cost', 'Status', 'Detail'].forEach((label) => row.append(el('th', null, label)));
  head.append(row);
  const body = el('tbody');
  plugins.forEach((plugin) => {
    const item = el('tr');
    append(item, el('td', null, plugin.name), el('td', null, plugin.kind), el('td', null, plugin.cost),
      el('td', null, humanStatus(plugin.status)), el('td', null, plugin.detail));
    body.append(item);
  });
  append(table, head, body);
  tableWrap.append(table);
  root.append(tableWrap);
}

async function probeAdvancedPlugins() {
  try {
    state.plugins = await post('/api/plugins/probe', {});
    await renderConnections();
    toast('Advanced manifests checked explicitly.', 'good');
  } catch (error) {
    showError(error);
  }
}

function saveConductorMessages() {
  try {
    localStorage.setItem('midden.conductor.messages', JSON.stringify(state.conductorMessages.slice(-40)));
  } catch {}
}

function addConductorMessage(role, text, extra = {}) {
  state.conductorMessages.push({role, text, at: new Date().toISOString(), ...extra});
  saveConductorMessages();
}

function clearConductorError() {
  const error = $('#conductor-error');
  if (error) {
    error.hidden = true;
    error.textContent = '';
  }
}

function setConductorError(message) {
  const error = $('#conductor-error');
  if (!error) return;
  error.textContent = message;
  error.hidden = false;
}

function classifyConductorInput(value) {
  const text = value.trim().toLowerCase();
  if (/^(hi|hello|hey|yo|good (morning|afternoon|evening))[!.?]*$/.test(text)) return 'greeting';
  if (/^(help|what can you do|how does this work|show me what you can do)[!.?]*$/.test(text)) return 'help';
  if (/^(what|which|why|how|where|when|who|show|tell|find|list|summarize|search)\b/.test(text) || text.endsWith('?')) return 'question';
  if (/\b(create|turn|build|prepare|write|make|generate|export|propose|draft|produce)\b/.test(text) ||
      /\b(tutorial|article|adr|release|slides?|deck|diagram|video|handbook|flashcards?|quiz|notebook|skill|eval|retrieval|sft|preference|manifest)\b/.test(text)) {
    return 'design';
  }
  return 'ambiguous';
}

function simpleConductorAnswer(value, overview) {
  const text = value.toLowerCase();
  if (/what can|what.*create|show.*opportunit|what.*yield/.test(text)) {
    if (!overview.yield?.ready) {
      return 'I can see your session sources, but I do not have reclaimed evidence yet. Run the free Mine pass, then extract a small evidence scope before I recommend outputs.';
    }
    const recommended = overview.yield.recommended;
    const category = (overview.yield.categories || [])
      .filter((item) => item.count > 0)
      .map((item) => `${item.count} ${item.title.toLowerCase()}`)
      .join(', ');
    return `You have ${overview.stats.nuggets} reclaimed evidence items supporting ${category || `${overview.yield.total} opportunities`}. The strongest current scope is ${workspaceName(recommended.workspace)} with ${recommended.evidence} evidence items.`;
  }
  if (/how much evidence|how many evidence|what evidence/.test(text)) {
    return `Midden currently has ${overview.stats.nuggets} reclaimed evidence items across ${(overview.workspaces || []).filter((workspace) => workspace.nuggets > 0).length} evidenced workspace(s).`;
  }
  return '';
}

function renderConductorMessage(message, composer) {
  const node = el('div', `message ${message.role}`);
  node.append(el('p', null, message.text));
  if (message.plan?.recipe) {
    const recipe = message.plan.recipe;
    const plan = el('div', 'conductor-plan');
    append(plan,
      el('div', 'eyebrow', 'Proposed plan'),
      el('h3', null, recipe.title),
      el('p', 'note', `${recipe.evidence_ids.length} evidence items · ${recipe.outputs.length} outputs · ${message.plan.estimated_seconds ? `about ${Math.ceil(message.plan.estimated_seconds / 60)} minute(s) · ` : ''}${message.plan.estimate_text}`),
    );
    if (message.plan.estimate?.mid && !message.plan.estimate?.samples) {
      plan.append(badge('first-run conservative estimate', 'spend'));
    }
    const outputs = el('div', 'conductor-plan-outputs');
    recipe.outputs.forEach((output) => outputs.append(badge(output.title, output.requires_model ? 'spend' : 'free')));
    plan.append(outputs);
    const actions = el('div', 'row-actions');
    if (message.plan_status === 'created') {
      actions.append(badge('plan created', 'ready'));
    } else {
      actions.append(button('Create this plan', 'button good compact', () => confirmConductorPlan(message)));
      actions.append(button('Adjust request', 'button ghost compact', () => {
        composer.value = recipe.request;
        composer.focus();
        composer.setSelectionRange(composer.value.length, composer.value.length);
      }));
    }
    plan.append(actions);
    node.append(plan);
  }
  return node;
}

async function confirmConductorPlan(message) {
  const recipe = message.plan?.recipe;
  if (!recipe || message.plan_status === 'created') return;
  try {
    const result = await post('/api/refinery/action', {
      action: 'design',
      workspace: recipe.workspace,
      prompt: recipe.request,
      title: recipe.title,
      output_kinds: recipe.outputs.map((output) => output.kind),
      evidence_ids: recipe.evidence_ids,
    });
    message.plan_status = 'created';
    saveConductorMessages();
    addConductorMessage('agent',
      `Created “${result.recipe.title}”. Nothing has run or spent. Review the evidence before approving it.`);
    state.overview = null;
    state.selectedRecipe = result.recipe.uid;
    state.recipeDetail = null;
    activateView('studio');
  } catch (error) {
    setConductorError(error.message);
  }
}

async function renderConductor() {
  const overview = await loadOverview();
  const root = clear($('#conductor-content'));
  append(root, pageHead('Conversational control', 'Ask, create, and run—one step at a time.',
    'Conductor clarifies your intent first, previews a plan second, and creates or runs only after your confirmation.'));
  const layout = el('div', 'chat-layout');
  const chat = el('section', 'chat-shell');
  const head = el('div', 'chat-head');
  append(head, el('strong', null, 'Conductor'));
  const modes = el('div', 'mode-switch');
  ['auto', 'ask', 'design', 'run'].forEach((mode) => {
    const node = button(mode.toUpperCase(), `mode-button ${state.conductorMode === mode ? 'active' : ''}`.trim(), () => {
      state.conductorMode = mode;
      renderConductor().catch(showError);
    });
    modes.append(node);
  });
  modes.append(button('CLEAR', 'mode-button', () => {
    state.conductorMessages = [];
    state.conductorDraft = '';
    saveConductorMessages();
    renderConductor().catch(showError);
  }));
  head.append(modes);
  chat.append(head);
  const composer = textarea(state.conductorDraft, conductorPlaceholder());
  composer.id = 'conductor-input';
  composer.rows = 3;
  composer.addEventListener('input', () => {
    state.conductorDraft = composer.value;
    clearConductorError();
  });
  composer.addEventListener('keydown', (event) => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      handleConductor(composer.value);
    }
  });
  const messages = el('div', 'messages');
  if (!state.conductorMessages.length) {
    messages.append(renderConductorMessage({
      role: 'agent',
      text: 'Hi. I can search your reclaimed evidence, explain what it supports, design a reviewed production, or run an approved plan. What are you trying to accomplish?',
    }, composer));
  }
  state.conductorMessages.forEach((message) => {
    messages.append(renderConductorMessage(message, composer));
  });
  const suggestions = el('div', 'conductor-suggestions');
  [
    'What can I create from my evidence?',
    'Create a tutorial and diagram from Orvantix evidence.',
    'Show my approved plans.',
  ].forEach((label) => suggestions.append(button(label, 'chip', () => {
    composer.value = label;
    state.conductorDraft = label;
    composer.focus();
  })));
  messages.append(suggestions);
  chat.append(messages);
  const composerWrap = el('div', 'composer');
  composerWrap.append(composer);
  const inlineError = el('div', 'inline-error');
  inlineError.id = 'conductor-error';
  inlineError.hidden = true;
  composerWrap.append(inlineError);
  const composerActions = el('div', 'row-actions');
  const safety = el('span', 'progress-note', conductorSafety());
  const send = button(conductorButtonLabel(), 'button', () => handleConductor(composer.value));
  append(composerActions, safety, send);
  composerWrap.append(composerActions);
  chat.append(composerWrap);

  const rail = el('div', 'rail');
  const scope = el('div', 'rail-card');
  const evidencedWorkspaces = (overview.workspaces || []).filter((workspace) => workspace.nuggets > 0).length;
  append(scope, el('h3', null, 'Current evidence'),
    el('p', null, `${overview.stats.nuggets} evidence item(s) · ${evidencedWorkspaces} evidenced workspace(s) · ${overview.stats.outputs} generated output(s)`));
  rail.append(scope);
  const safetyCard = el('div', 'rail-card');
  append(safetyCard, el('h3', null, 'Safety boundary'),
    el('p', null, 'The Conductor sees the mined index and selected evidence slices, never raw session stores. Publishing, install, upload, training, and shell each remain separate approvals.'));
  rail.append(safetyCard);
  const recipes = (overview.recipes || []).filter((recipe) => recipe.status === 'approved');
  const ready = el('div', 'rail-card');
  append(ready, el('h3', null, 'Approved recipes'),
    el('p', null, recipes.length ? `${recipes.length} production(s) are ready for a cost preview.` : 'No recipe is approved yet.'));
  rail.append(ready);
  const shell = el('div', 'rail-card');
  append(shell, el('h3', null, 'Shell boundary'),
    el('p', null, 'Shell is disabled. Enabling it later will require exact command, working directory, and a separate approval.'));
  rail.append(shell);
  append(layout, chat, rail);
  root.append(layout);
  messages.scrollTop = messages.scrollHeight;
}

function conductorPlaceholder() {
  switch (state.conductorMode) {
  case 'auto': return 'Say hi, ask a question, or describe the outcome you want.';
  case 'ask': return 'What repeated failures are costing me the most time?';
  case 'run': return 'Which approved plan should I run?';
  default: return 'Create a tutorial, diagram, and evaluated skill from Orvantix evidence.';
  }
}

function conductorSafety() {
  switch (state.conductorMode) {
  case 'auto': return 'CLARIFY FIRST · NO ACTION';
  case 'ask': return 'ESTIMATE BEFORE MODEL';
  case 'run': return 'APPROVED RECIPES ONLY';
  default: return 'PREVIEW PLAN · NO ACTION';
  }
}

function conductorButtonLabel() {
  switch (state.conductorMode) {
  case 'run': return 'Choose plan';
  default: return 'Send';
  }
}

async function handleConductor(prompt) {
  const value = String(prompt || '').trim();
  clearConductorError();
  if (state.conductorMode !== 'run' && !value) {
    setConductorError('Tell me what you want to learn, create, or run.');
    return;
  }
  if (state.conductorMode === 'run') {
    const approved = (state.overview?.recipes || []).filter((recipe) => recipe.status === 'approved');
    if (!approved.length) {
      addConductorMessage('agent', 'There is no approved plan to run yet. Create a plan, review its evidence, and approve it first.');
      state.conductorDraft = '';
      await renderConductor();
      return;
    }
    const body = openModal('Choose an approved production',
      'Run mode previews cost before starting and never publishes automatically.');
    const list = el('div', 'step-list');
    approved.forEach((recipe) => {
      const row = el('div', 'step-row');
      const copy = el('div');
      append(copy, el('strong', null, recipe.title),
        el('small', null, `${recipe.outputs.length} outputs · ${recipe.evidence_ids.length} evidence items`));
      append(row, el('span', 'step-number', 'R'), copy,
        button('Preview run', 'button compact', () => {
          closeModal();
          previewProduction(recipe.uid);
        }));
      list.append(row);
    });
    body.append(list);
    return;
  }

  addConductorMessage('user', value);
  state.conductorDraft = '';
  const intent = state.conductorMode === 'auto' ? classifyConductorInput(value) : state.conductorMode;
  if (intent === 'greeting') {
    addConductorMessage('agent',
      'Hi. I can search your evidence, explain what it supports, design a production, or run an approved plan. What would you like to do?');
    await renderConductor();
    return;
  }
  if (intent === 'help') {
    addConductorMessage('agent',
      'Try “What can I create?”, “Find repeated webhook failures,” or “Create a tutorial and diagram from Orvantix evidence.” I will clarify anything ambiguous before creating a plan.');
    await renderConductor();
    return;
  }
  if (state.overview.stats.nuggets === 0) {
    addConductorMessage('agent',
      'I can see your session sources, but no reclaimed evidence is ready yet. Run the free Mine pass, then extract a small evidence scope. I will not create an unsupported plan.');
    await renderConductor();
    return;
  }
  if (intent === 'question' || intent === 'ask') {
    const localAnswer = simpleConductorAnswer(value, state.overview);
    if (localAnswer) {
      addConductorMessage('agent', localAnswer);
      await renderConductor();
      return;
    }
    addConductorMessage('agent',
      `I can answer that from ${state.overview.stats.nuggets} reclaimed evidence items. I will show the model estimate before making the call.`);
    await renderConductor();
    try {
      const preview = await runJob({op: 'ask', question: value, apply: false},
        'Estimating answer', 'The estimate uses Midden’s compressed evidence, not raw transcripts.');
      openSpendConfirmation('Answer from the evidence', preview, async () => {
        const run = await runJob({op: 'ask', question: value, apply: true},
          'Answering from your evidence', 'The selected CLI receives a bounded redacted brief.');
        addConductorMessage('agent', run.result?.answer || 'The model returned no answer.');
        await renderConductor();
      });
    } catch (error) {
      setConductorError(error.message);
    }
    return;
  }

  if (intent === 'design') {
    const lowered = value.toLowerCase();
    const matchedWorkspace = (state.overview?.workspaces || []).find((workspace) => {
      const name = String(workspace.name || '').toLowerCase();
      const id = String(workspace.id || '').toLowerCase();
      return workspace.nuggets > 0 && ((name.length > 2 && lowered.includes(name)) ||
        (id.length > 3 && lowered.includes(id)));
    });
    await renderConductor();
    try {
      const result = await post('/api/refinery/action', {
        action: 'preview_design', prompt: value, workspace: matchedWorkspace?.id || '',
      });
      addConductorMessage('agent',
        `I interpreted that as “${result.recipe.title}”. Review the outputs and evidence scope below; I will create nothing until you confirm.`,
        {plan: result, plan_status: 'preview'});
      await renderConductor();
    } catch (error) {
      setConductorError(error.message);
    }
    return;
  }
  addConductorMessage('agent',
    'I need a little more direction. Are you trying to search your evidence, create something from it, or run an approved plan?');
  await renderConductor();
}

async function renderOperations() {
  const root = clear($('#operations-content'));
  append(root, pageHead('Sources, cost, and audit', 'Operate the proven mining core.',
    'Session rescue, deterministic cleanup previews, cost accounting, jobs, and the append-only operation log remain available inside the refinery.'));
  const tabs = el('div', 'tabs');
  [
    ['sessions', 'Sessions'],
    ['cost', 'Cost'],
    ['jobs', 'Jobs'],
    ['log', 'Audit log'],
  ].forEach(([id, label]) => {
    tabs.append(button(label, `tab-button ${state.operationsTab === id ? 'active' : ''}`.trim(), () => {
      state.operationsTab = id;
      renderOperations().catch(showError);
    }));
  });
  root.append(tabs);
  const panel = el('section', 'panel');
  root.append(panel);
  if (state.operationsTab === 'sessions') await renderSessionsOperation(panel);
  if (state.operationsTab === 'cost') await renderCostOperation(panel);
  if (state.operationsTab === 'jobs') await renderJobsOperation(panel);
  if (state.operationsTab === 'log') await renderLogOperation(panel);
}

async function renderSessionsOperation(panel) {
  panel.append(sectionHead('Indexed sessions', 'Find, resume, rescue, mine, or clean one session',
    'Consequence-first ordering surfaces resume risk and large transcripts before routine history.'));
  const filters = el('div', 'filter-row');
  const tool = selectInput([
    {value: '', label: 'All tools'},
    {value: 'copilot', label: 'Copilot'},
    {value: 'claude', label: 'Claude'},
    {value: 'opencode', label: 'OpenCode'},
  ]);
  const days = selectInput([
    {value: '7', label: 'Last 7 days'},
    {value: '30', label: 'Last 30 days'},
    {value: '', label: 'Any time'},
  ], '7');
  const search = textInput('', 'filter title or workspace');
  append(filters, tool, days, search);
  panel.append(filters);
  const summary = el('p', 'note session-page-summary');
  const list = el('div');
  const pager = el('div', 'session-pager');
  panel.append(summary, list, pager);
  let page = 0;
  const pageSize = 20;
  let searchTimer;

  const load = async () => {
    const params = new URLSearchParams({
      limit: String(pageSize),
      offset: String(page * pageSize),
      sort: 'consequence',
    });
    if (tool.value) params.set('tool', tool.value);
    if (days.value) params.set('days', days.value);
    if (search.value.trim()) params.set('search', search.value.trim());
    const [sessions, stats] = await Promise.all([
      get(`/api/sessions?${params}`),
      get(`/api/session-stats?${params}`),
    ]);
    clear(list);
    sessions.forEach((session) => {
        const row = el('button', 'session-row');
        row.type = 'button';
        row.setAttribute('aria-label', `Open session: ${session.title || session.short}`);
        const head = el('div', 'panel-head');
        const copy = el('div');
        append(copy, el('h3', null, session.title || session.short),
          el('p', null, `${session.tool} · ${session.short} · ${session.dir}`));
        const badges = el('div', 'row-actions');
        append(badges, badge(formatBytes(session.bytes)),
          session.live ? badge('open now', 'free') : null,
          session.risk !== 'ok' ? badge(session.risk, session.risk === 'critical' ? 'danger' : 'spend') : null);
        append(head, copy, badges);
        row.append(head);
        row.addEventListener('click', () => openSessionDrawer(session));
        list.append(row);
      });
    if (!list.children.length) list.append(emptyState('No sessions match', 'Change the filters or refresh the source index.'));
    const first = stats.target ? page * pageSize + 1 : 0;
    const last = Math.min(stats.target, page * pageSize + sessions.length);
    summary.textContent = `${first}–${last} of ${stats.target} matching session(s)` +
      (stats.hidden_noise ? ` · ${stats.hidden_noise} automated hidden` : '') +
      (stats.indexed_at ? ` · ${indexAgeLabel(stats.indexed_at)}` : '');
    clear(pager);
    const previous = button('Previous', 'button ghost compact', () => {
      page = Math.max(0, page - 1);
      load().catch(showError);
    });
    previous.disabled = page === 0;
    const next = button('Next', 'button ghost compact', () => {
      page += 1;
      load().catch(showError);
    });
    next.disabled = last >= stats.target;
    append(pager, previous, el('span', null, `Page ${page + 1} of ${Math.max(1, Math.ceil(stats.target / pageSize))}`), next);
  };
  const resetAndLoad = () => {
    page = 0;
    load().catch(showError);
  };
  tool.addEventListener('change', resetAndLoad);
  days.addEventListener('change', resetAndLoad);
  search.addEventListener('input', () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(resetAndLoad, 220);
  });
  await load();
}

function indexAgeLabel(value) {
  return `indexed ${relativeAge(value)}`;
}

async function openSessionDrawer(session) {
  const body = openDrawer(session.title || session.short);
  body.append(el('p', 'note', `${session.tool} · ${session.id} · ${session.dir}`));
  const meta = metricGrid([
    {label: 'Transcript', value: formatBytes(session.bytes), copy: session.risk === 'ok' ? 'below resume-risk threshold' : `${session.risk} resume risk`},
    {label: 'Turns', value: session.turns, copy: `updated ${session.age}`},
    {label: 'Workspace', value: session.dir_exists ? 'present' : 'missing', copy: session.repo || session.dir},
    {label: 'State', value: session.live ? 'open' : 'closed', copy: session.live ? 'switch to the existing terminal' : 'safe to inspect'},
  ]);
  body.append(meta);
  const actions = el('div', 'row-actions');
  actions.append(button('Copy resume command', 'button ghost', () => copyText(session.resume)));
  actions.append(button('Rescue handoff', 'button good', () => rescueSession(session.id)));
  actions.append(button('Mine this session', 'button', () => openReclaimModal(session.dir)));
  actions.append(button('Preview prune', 'button ghost', () => previewSessionAction(session, 'prune')));
  actions.append(button('Preview archive', 'button ghost', () => previewSessionAction(session, 'archive')));
  body.append(actions);
  try {
    const detail = await get(`/api/session?id=${encodeURIComponent(session.id)}`);
    if (detail.harvest) {
      const pre = el('pre', 'provenance', JSON.stringify(detail.harvest, null, 2));
      const details = el('details');
      append(details, el('summary', null, 'Inspect bounded session harvest'), pre);
      body.append(details);
    }
  } catch (error) {
    body.append(el('div', 'warning', error.message));
  }
}

async function copyText(value) {
  try {
    await navigator.clipboard.writeText(value || '');
    toast('Copied to clipboard', 'good');
  } catch {
    toast('Clipboard access failed', 'bad');
  }
}

async function rescueSession(sessionID) {
  closeDrawer();
  try {
    const job = await runJob({op: 'brief', session_id: sessionID, records: 12},
      'Rescuing session', 'This is deterministic, read-only, and free. The handoff is saved as a local artifact.');
    const result = job.result || {};
    const body = openDrawer(`Handoff · ${result.title || result.session}`);
    body.append(el('p', 'note', result.saved ? `Saved to ${result.saved}` : 'Clipboard-ready handoff'));
    const pre = el('pre', 'provenance', result.body || '');
    body.append(pre);
    body.append(button('Copy handoff', 'button', () => copyText(result.body)));
  } catch (error) {
    showError(error);
  }
}

async function previewSessionAction(session, op) {
  closeDrawer();
  try {
    const preview = await runJob({op, session_id: session.id, apply: false},
      `Previewing ${op}`, 'Dry run only. No source transcript is changed.');
    const result = preview.result || {};
    const body = openModal(`${humanStatus(op)} preview`, 'Review the exact scope before applying.');
    const box = el('div', 'estimate-box');
    append(box, el('strong', null, op === 'prune' ? formatBytes(result.total_saved || 0) : `${result.rows?.length || 0} transcript(s)`),
      el('p', 'note', op === 'prune' ? 'estimated recoverable bytes' : 'eligible for archive'));
    body.append(box);
    const actions = el('div', 'modal-actions');
    actions.append(button('Cancel', 'button ghost', closeModal));
    actions.append(button(`Apply ${op}`, 'button danger', async () => {
      closeModal();
      try {
        await runJob({op, session_id: session.id, apply: true, confirm: true},
          `Applying ${op}`, 'Midden records the operation and verification result in its audit log.');
        toast(`${humanStatus(op)} complete`, 'good');
        await refreshOverview();
      } catch (error) {
        showError(error);
      }
    }));
    body.append(actions);
  } catch (error) {
    showError(error);
  }
}

async function renderCostOperation(panel) {
  const data = await get('/api/cost');
  panel.append(sectionHead('Cost ledger', 'Prediction and actual usage side by side',
    'Midden reads real usage back from the signed-in CLI session stores.'));
  panel.append(metricGrid([
    {label: 'Runs', value: data.totals.runs, copy: 'recorded model operations'},
    {label: 'Items', value: data.totals.items, copy: 'evidence or outputs produced'},
    {label: 'Tokens', value: formatCount(data.totals.tokens), copy: 'input, output, and cache movement'},
    {label: 'AIU', value: Number(data.totals.aiu || 0).toFixed(1), copy: data.totals.usd ? `$${Number(data.totals.usd).toFixed(2)} also reported` : 'subscription credits where available'},
  ]));
  const tableWrap = el('div', 'table-wrap');
  const table = el('table');
  const head = el('thead');
  const hrow = el('tr');
  ['Operation', 'Scope', 'Items', 'Actual', 'Started', 'Status'].forEach((label) => hrow.append(el('th', null, label)));
  head.append(hrow);
  const tbody = el('tbody');
  (data.runs || []).forEach((run) => {
    const row = el('tr');
    const actual = run.usage?.aiu
      ? `${Number(run.usage.aiu).toFixed(1)} AIU`
      : run.usage?.usd
        ? `$${Number(run.usage.usd).toFixed(2)}`
        : `${formatCount((run.usage?.input_tokens || 0) + (run.usage?.output_tokens || 0) + (run.usage?.cache_read_tokens || 0) + (run.usage?.cache_write_tokens || 0))} tok`;
    append(row, el('td', null, run.op), el('td', null, run.scope), el('td', null, run.items),
      el('td', null, actual), el('td', null, formatDate(run.started_at)),
      el('td', null, run.ok ? 'complete' : 'failed'));
    tbody.append(row);
  });
  append(table, head, tbody);
  tableWrap.append(table);
  panel.append(tableWrap);
}

async function renderJobsOperation(panel) {
  const jobs = await get('/api/jobs');
  panel.append(sectionHead('Background jobs', jobs.length ? `${jobs.length} recent operation(s)` : 'No recent jobs',
    'Long operations remain visible while the browser stays open.'));
  if (!jobs.length) {
    panel.append(emptyState('No jobs yet', 'Mine, extract evidence, or run a production to create one.'));
    return;
  }
  jobs.forEach((job) => {
    const card = el('div', 'run-card');
    const head = el('div', 'run-head');
    const copy = el('div');
    append(copy, el('div', 'eyebrow', job.op), el('h3', 'panel-title', job.scope || 'all'));
    append(head, copy, badge(humanStatus(job.status), statusKind(job.status)));
    card.append(head);
    card.append(el('p', 'note', job.error || job.progress || 'Waiting'));
    panel.append(card);
  });
}

async function renderLogOperation(panel) {
  const operations = await get('/api/ops');
  panel.append(sectionHead('Append-only operation log',
    operations.length ? `${operations.length} recent mutation record(s)` : 'No mutating operations recorded',
    'Derived writes, cleanup, exports, reviews, and production actions are auditable.'));
  if (!operations.length) {
    panel.append(emptyState('The log is empty', 'Read-only browsing does not create audit entries.'));
    return;
  }
  const tableWrap = el('div', 'table-wrap');
  const table = el('table');
  const head = el('thead');
  const hrow = el('tr');
  ['Time', 'Operation', 'Session', 'Before', 'After', 'Detail', 'Status'].forEach((label) => hrow.append(el('th', null, label)));
  head.append(hrow);
  const body = el('tbody');
  operations.forEach((operation) => {
    const row = el('tr');
    append(row, el('td', null, formatDate(operation.created_at)), el('td', null, operation.op),
      el('td', null, operation.session_id || '—'), el('td', null, formatBytes(operation.before)),
      el('td', null, formatBytes(operation.after)), el('td', null, operation.detail),
      el('td', null, operation.ok ? 'ok' : 'failed'));
    body.append(row);
  });
  append(table, head, body);
  tableWrap.append(table);
  panel.append(tableWrap);
}

async function renderActiveView() {
  await loadOverview();
  switch (state.activeView) {
  case 'home': return renderHome();
  case 'mine': return renderMine();
  case 'studio': return renderStudio();
  case 'knowledge': return renderKnowledge();
  case 'agent-forge': return renderAgentForge();
  case 'personalization': return renderPersonalization();
  case 'connections': return renderConnections();
  case 'conductor': return renderConductor();
  case 'operations': return renderOperations();
  }
}

loadOverview(true)
  .then(() => renderHome())
  .catch((error) => {
    console.error(error);
    clear($('#home-content')).append(emptyState('Midden could not load', friendlyError(error),
      button('Retry', 'button', () => location.reload())));
  });

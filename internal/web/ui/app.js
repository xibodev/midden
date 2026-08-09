const $ = (selector, root = document) => root.querySelector(selector);
const $$ = (selector, root = document) => Array.from(root.querySelectorAll(selector));

const state = {
  activeView: 'recover',
  overview: null,
  sessions: [],
  sessionStats: null,
  recoveryRuns: [],
  recoveryRunsTotal: 0,
  activityOperations: [],
  activityOperationsTotal: 0,
  jobs: [],
  jobsTotal: 0,
  activityJobs: [],
  jobsInitialized: false,
  jobCallbacks: new Map(),
  completedJobs: new Set(),
  selectedSessions: new Set(),
  selectedSessionMeta: new Map(),
  mineBuilderOpen: false,
  recoverPage: 0,
  recoverPageSize: 20,
  recoverFilters: {search: '', tool: '', days: ''},
  workItems: [],
  workTotal: 0,
  selectedWork: sessionStorage.getItem('midden.selectedWork') || '',
  workPage: 0,
  workPageSize: 12,
  workSearch: '',
  workDetail: null,
  messagePages: new Map(),
  conversationMode: 'chat',
  consoleHistory: new Map(),
  consoleFeedback: new Map(),
  selectedOutput: '',
  previewMode: 'rendered',
  outputDetail: null,
  recordPages: new Map(),
  provenancePages: new Map(),
  pendingChat: new Set(),
  libraryFilter: 'all',
  libraryPage: 0,
  libraryPageSize: 12,
  cleanup: null,
  cleanupPage: 0,
  cleanupPageSize: 20,
  cleanupFilters: {search: '', decision: ''},
  activityJobPage: 0,
  activityJobPageSize: 10,
  activityRecoveryPage: 0,
  activityRecoveryPageSize: 10,
  activityAuditPage: 0,
  activityAuditPageSize: 10,
  toolPage: 0,
  toolPageSize: 12,
  pluginPage: 0,
  pluginPageSize: 10,
  connections: null,
  integrations: null,
  plugins: null,
  serviceAvailable: true,
  serviceError: '',
  jobsSignature: '',
  activityRefreshPending: false,
};

const viewMeta = {
  recover: ['Recovery desk', 'Recover'],
  studio: ['Persistent workbench', 'Studio'],
  library: ['Owned outputs', 'Library'],
  cleanup: ['Recovery-aware storage', 'Cleanup'],
  activity: ['Background work', 'Activity'],
  tools: ['Capabilities and settings', 'Tools'],
};

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null) node.textContent = text;
  return node;
}

function clear(node) {
  while (node.firstChild) node.firstChild.remove();
  return node;
}

function append(parent, ...children) {
  children.flat().forEach((child) => {
    if (child !== undefined && child !== null) parent.append(child);
  });
  return parent;
}

function button(label, className = 'button', handler) {
  const node = el('button', className, label);
  node.type = 'button';
  if (handler) node.addEventListener('click', handler);
  return node;
}

function badge(label, kind = '') {
  return el('span', `badge ${kind}`.trim(), label);
}

function field(label, input) {
  const wrap = el('label', 'field');
  append(wrap, document.createTextNode(label), input);
  return wrap;
}

function textInput(value = '', placeholder = '') {
  const input = el('input');
  input.type = 'text';
  input.value = value;
  input.placeholder = placeholder;
  return input;
}

function textArea(value = '', placeholder = '') {
  const input = el('textarea');
  input.value = value;
  input.placeholder = placeholder;
  return input;
}

function selectInput(options, selected = '') {
  const select = el('select');
  options.forEach((option) => {
    const item = el('option');
    if (typeof option === 'string') {
      item.value = option;
      item.textContent = option;
    } else {
      item.value = option.value;
      item.textContent = option.label;
    }
    item.selected = item.value === selected;
    select.append(item);
  });
  return select;
}

function pageHead(kicker, title, copy, actions = []) {
  const head = el('header', 'page-head');
  const pageCopy = el('div', 'page-copy');
  append(pageCopy, el('div', 'eyebrow', kicker), el('h1', null, title), el('p', null, copy));
  const actionWrap = el('div', 'actions');
  actions.forEach((action) => actionWrap.append(action));
  append(head, pageCopy, actionWrap.children.length ? actionWrap : null);
  return head;
}

function panelHead(kicker, title, copy, actions = []) {
  const head = el('div', 'panel-head');
  const content = el('div');
  append(content, el('div', 'eyebrow', kicker), el('h2', null, title), copy ? el('p', null, copy) : null);
  const actionWrap = el('div', 'actions');
  actions.forEach((action) => actionWrap.append(action));
  append(head, content, actionWrap.children.length ? actionWrap : null);
  return head;
}

function metricGrid(items) {
  const grid = el('div', 'metric-grid');
  items.forEach((item) => {
    const card = el('article', 'metric-card');
    append(card, el('div', 'eyebrow', item.label), el('strong', null, String(item.value)),
      el('span', null, item.copy || ''));
    grid.append(card);
  });
  return grid;
}

function emptyState(title, copy, action) {
  const node = el('div', 'empty');
  append(node, el('strong', null, title), el('span', null, copy), action ? el('div', 'actions') : null);
  if (action) $('.actions', node).append(action);
  return node;
}

function pageSlice(items, page, pageSize) {
  const pages = Math.max(1, Math.ceil(items.length / pageSize));
  const safePage = Math.min(Math.max(0, page), pages - 1);
  return {
    items: items.slice(safePage * pageSize, safePage * pageSize + pageSize),
    page: safePage,
    pages,
    total: items.length,
  };
}

function pager(total, page, pageSize, onChange, label = 'items') {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const safePage = Math.min(Math.max(0, page), pages - 1);
  const wrap = el('div', 'pager');
  const first = total ? safePage * pageSize + 1 : 0;
  const last = Math.min(total, safePage * pageSize + pageSize);
  const previous = button('Previous', 'button ghost compact', () => onChange(safePage - 1));
  previous.disabled = safePage === 0;
  const next = button('Next', 'button ghost compact', () => onChange(safePage + 1));
  next.disabled = safePage >= pages - 1;
  append(wrap, previous,
    el('span', null, `${first}–${last} of ${total} ${label} · Page ${safePage + 1}/${pages}`),
    next);
  return wrap;
}

function formatBytes(value) {
  const bytes = Number(value || 0);
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GiB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(0)} MiB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KiB`;
  return `${bytes} B`;
}

function formatCount(value) {
  const count = Number(value || 0);
  if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`;
  if (count >= 1_000) return `${(count / 1_000).toFixed(0)}k`;
  return String(count);
}

function formatDate(value) {
  if (!value) return '--';
  return new Intl.DateTimeFormat(undefined, {
    month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit',
  }).format(new Date(value));
}

function relativeAge(value) {
  if (!value) return 'never';
  const seconds = Math.max(0, (Date.now() - new Date(value).getTime()) / 1000);
  if (seconds < 60) return 'just now';
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

function humanStatus(value) {
  return String(value || '').replaceAll('_', ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
}

function statusKind(value) {
  const status = String(value || '').toLowerCase();
  if (['done', 'complete', 'reviewed', 'exported', 'ready', 'eligible', 'connected'].includes(status)) return 'free';
  if (['failed', 'rejected', 'critical', 'protected'].includes(status)) return 'danger';
  if (['running', 'queued', 'review', 'evidence review', 'held', 'warning'].includes(status.replaceAll('_', ' '))) return 'spend';
  if (status.includes('agent') || status.includes('skill')) return 'purple';
  return '';
}

function truncate(value, length = 120) {
  const text = String(value || '').trim();
  return text.length <= length ? text : `${text.slice(0, length - 1)}…`;
}

function sessionKey(session) {
  return `${String(session.tool || '').toLowerCase()}:${session.id}`;
}

function escapeHTML(value) {
  return String(value || '')
    .replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;').replaceAll("'", '&#039;');
}

async function request(path, options = {}) {
  let response;
  try {
    response = await fetch(path, {
      ...options,
      headers: {
        ...(options.body ? {'Content-Type': 'application/json'} : {}),
        ...(options.method && options.method !== 'GET' ? {'X-Midden-Request': '1'} : {}),
        ...(options.headers || {}),
      },
    });
  } catch (error) {
    setServiceStatus(false, 'Local Midden service is unavailable');
    throw error;
  }
  setServiceStatus(true);
  if (!response.ok) {
    const body = (await response.text()).trim();
    throw new Error(body || `${response.status} ${response.statusText}`);
  }
  const type = response.headers.get('content-type') || '';
  return type.includes('application/json') ? response.json() : response.text();
}

const get = (path) => request(path);
const post = (path, body) => request(path, {method: 'POST', body: JSON.stringify(body)});

let toastTimer;
function toast(message, kind = 'good') {
  const node = $('#toast');
  node.textContent = message;
  node.classList.toggle('bad', kind === 'bad');
  node.hidden = false;
  requestAnimationFrame(() => node.classList.add('show'));
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    node.classList.remove('show');
    setTimeout(() => { node.hidden = true; }, 200);
  }, 2800);
}

function notice(message, kind = 'good') {
  const node = $('#notice-region');
  node.textContent = message;
  node.className = `notice-region ${kind === 'bad' ? 'bad' : ''}`.trim();
  node.hidden = false;
  node.scrollIntoView({block: 'nearest'});
}

function clearNotice() {
  $('#notice-region').hidden = true;
  $('#notice-region').textContent = '';
}

function showError(error) {
  console.warn(error);
  notice(error?.message || String(error), 'bad');
}

function setServiceStatus(available, message = '') {
  const changed = state.serviceAvailable !== available;
  state.serviceAvailable = available;
  state.serviceError = message;
  const card = $('.local-card');
  if (card) {
    card.classList.toggle('offline', !available);
    const strong = $('strong', card);
    const detail = $('span:not(.status-dot)', card);
    clear(strong);
    append(strong, el('span', 'status-dot'), document.createTextNode(
      available ? 'Local service ready' : 'Local service unavailable'));
    if (detail) {
      detail.textContent = available
        ? 'Source stores remain read-only. Jobs, chat, previews, and exports stay on this machine.'
        : 'The browser cannot reach the loopback service. Restart Midden, then retry.';
    }
  }
  if (!available) {
    const badges = clear($('#global-badges'));
    badges.append(badge('service unavailable', 'danger'));
  } else if (changed) {
    if (state.overview) {
      updateGlobalUI();
    } else {
      const badges = clear($('#global-badges'));
      badges.append(badge('service ready', 'free'));
    }
  }
}

function showViewLoading(view) {
  const root = $(`#${view}-content`);
  if (!root) return;
  clear(root);
  const shell = el('div', 'loading-shell');
  append(shell, el('span', 'loading-spinner'), el('p', 'loading-label', `Loading ${view}…`));
  root.append(shell);
}

let modalReturnFocus = null;
let drawerReturnFocus = null;

function openModal(title, copy) {
  modalReturnFocus = document.activeElement;
  const modal = $('#modal');
  const body = clear($('#modal-body'));
  const heading = el('h2', null, title);
  heading.id = 'modal-title';
  append(body, heading, copy ? el('p', 'note', copy) : null);
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
  drawerReturnFocus = document.activeElement;
  const drawer = $('#drawer');
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

function trapFocus(container, event) {
  const nodes = $$('button:not([disabled]),a[href],input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])', container)
    .filter((node) => !node.hidden && node.getClientRects().length);
  if (!nodes.length) return;
  if (event.shiftKey && document.activeElement === nodes[0]) {
    event.preventDefault();
    nodes[nodes.length - 1].focus();
  } else if (!event.shiftKey && document.activeElement === nodes[nodes.length - 1]) {
    event.preventDefault();
    nodes[0].focus();
  }
}

$('#modal-close').addEventListener('click', closeModal);
$('[data-close-modal]').addEventListener('click', closeModal);
$('#drawer-close').addEventListener('click', closeDrawer);
$('[data-close-drawer]').addEventListener('click', closeDrawer);

document.addEventListener('keydown', (event) => {
  if (event.key === 'Tab' && !$('#modal').hidden) return trapFocus($('#modal'), event);
  if (event.key === 'Tab' && !$('#drawer').hidden) return trapFocus($('#drawer'), event);
  if (event.key === 'Escape') {
    closeModal();
    closeDrawer();
    closeNavigation();
  }
});

function openNavigation() {
  document.body.classList.add('nav-open');
  $('#nav-backdrop').hidden = false;
}

function closeNavigation() {
  document.body.classList.remove('nav-open');
  $('#nav-backdrop').hidden = true;
}

$('#nav-toggle').addEventListener('click', openNavigation);
$('#nav-close').addEventListener('click', closeNavigation);
$('#nav-backdrop').addEventListener('click', closeNavigation);

$$('.nav-item[data-view]').forEach((item) => {
  item.addEventListener('click', () => activateView(item.dataset.view));
});

function configurePrimaryAction(view) {
  const action = $('#top-primary-action');
  action.hidden = false;
  action.onclick = null;
  switch (view) {
  case 'recover':
    action.textContent = 'New mine';
    action.onclick = () => {
      state.mineBuilderOpen = true;
      renderRecover().catch(showError);
    };
    break;
  case 'studio':
    action.textContent = 'New work item';
    action.onclick = openCreateWorkItem;
    break;
  case 'library':
    action.textContent = 'Open Studio';
    action.onclick = () => activateView('studio');
    break;
  case 'cleanup':
    action.textContent = 'Refresh eligibility';
    action.onclick = () => {
      state.cleanup = null;
      renderCleanup().catch(showError);
    };
    break;
  case 'activity':
    action.textContent = 'Refresh Activity';
    action.onclick = () => refreshActivity().catch(showError);
    break;
  case 'tools':
    action.textContent = 'Check tools';
    action.onclick = () => {
      state.connections = null;
      state.integrations = null;
      renderTools().catch(showError);
    };
    break;
  default:
    action.hidden = true;
  }
}

function activateView(view) {
  clearNotice();
  state.activeView = view;
  if (state.jobsInitialized) renderTaskDock();
  $$('.nav-item[data-view]').forEach((item) => item.classList.toggle('active', item.dataset.view === view));
  $$('.view').forEach((section) => section.classList.toggle('active', section.id === view));
  const meta = viewMeta[view];
  $('#view-kicker').textContent = meta[0];
  $('#view-title').textContent = meta[1];
  configurePrimaryAction(view);
  closeNavigation();
  window.scrollTo({top: 0, behavior: 'instant'});
  showViewLoading(view);
  renderViewWithError(view);
}

async function renderViewWithError(view) {
  try {
    await renderActiveView();
  } catch (error) {
    showError(error);
    const root = clear($(`#${view}-content`));
    root.append(emptyState(`${humanStatus(view)} could not load`,
      error?.message || 'The local service did not respond.',
      button('Retry', 'button', () => {
        showViewLoading(view);
        renderViewWithError(view);
      })));
  }
}

async function loadOverview(force = false) {
  if (!state.overview || force) state.overview = await get('/api/refinery');
  updateGlobalUI();
  return state.overview;
}

function updateGlobalUI() {
  const overview = state.overview;
  if (!overview) return;
  const badges = clear($('#global-badges'));
  if (!state.serviceAvailable) {
    badges.append(badge('service unavailable', 'danger'));
    return;
  }
  const readySources = (overview.sources || []).filter((source) => source.ready).length;
  append(badges, badge(`${readySources} sources ready`, 'free'),
    badge(`${formatCount(overview.stats?.nuggets)} evidence`),
    badge(`${formatCount(overview.stats?.outputs)} outputs`, 'blue'));
  $('#nav-studio-count').textContent = String(overview.stats?.recipes || 0);
  $('#nav-library-count').textContent = String(overview.stats?.outputs || 0);
  $('#nav-recover-count').textContent = String(overview.stats?.sessions || 0);
}

async function refreshAll() {
  state.overview = null;
  state.recoveryRuns = [];
  state.workItems = [];
  state.workDetail = null;
  state.cleanup = null;
  state.outputDetail = null;
  state.cleanup = null;
  await loadOverview(true);
  await renderActiveView();
}

const jobCallbacks = new Map();
async function startJob(requestBody, options = {}) {
  const job = await post('/api/action', requestBody);
  if (options.onDone || options.onFailed) jobCallbacks.set(job.id, options);
  $('#task-dock').hidden = false;
  $('#task-dock').classList.add('open');
  await refreshJobs();
  if (options.message) toast(options.message);
  return job;
}

async function refreshJobs() {
  const response = await get('/api/jobs?limit=40');
  const nextJobs = Array.isArray(response) ? response : (response.items || []);
  const signature = nextJobs.map((job) =>
    `${job.id}:${job.status}:${job.progress}:${job.ended || ''}`).join('|');
  const changed = signature !== state.jobsSignature;
  state.jobsSignature = signature;
  state.jobs = nextJobs;
  state.jobsTotal = Number(Array.isArray(response) ? state.jobs.length : (response.total ?? state.jobs.length));
  const active = state.jobs.filter((job) => ['queued', 'running'].includes(job.status));
  $('#nav-activity-count').textContent = String(active.length);
  renderTaskDock();

  if (!state.jobsInitialized) {
    state.jobs.forEach((job) => {
      if (['done', 'failed'].includes(job.status)) state.completedJobs.add(job.id);
    });
    state.jobsInitialized = true;
    return;
  }
  for (const job of state.jobs) {
    if (!['done', 'failed'].includes(job.status) || state.completedJobs.has(job.id)) continue;
    state.completedJobs.add(job.id);
    const callbacks = jobCallbacks.get(job.id);
    jobCallbacks.delete(job.id);
    if (job.status === 'done') {
      if (callbacks?.onDone) await callbacks.onDone(job);
      else if (['mine', 'reclaim', 'production', 'work_chat', 'refresh'].includes(job.op)) {
        state.overview = null;
        state.recoveryRuns = [];
        state.workItems = [];
        state.workDetail = null;
        if (state.activeView !== 'activity') toast(`${humanStatus(job.op)} complete`);
        await renderActiveView();
      }
    } else {
      if (callbacks?.onFailed) await callbacks.onFailed(job);
      else notice(job.error || `${humanStatus(job.op)} failed`, 'bad');
    }
  }
  if (changed && state.activeView === 'activity') scheduleActivityRefresh();
}

function scheduleActivityRefresh() {
  if (state.activityRefreshPending) return;
  state.activityRefreshPending = true;
  setTimeout(() => {
    state.activityRefreshPending = false;
    if (state.activeView === 'activity') renderActivity().catch(showError);
  }, 250);
}

function renderTaskDock() {
  const dock = $('#task-dock');
  const body = clear($('#task-dock-body'));
  const active = state.jobs.filter((job) => ['queued', 'running'].includes(job.status));
  if (state.activeView === 'activity' || active.length === 0) {
    dock.classList.remove('open');
    dock.hidden = true;
  } else {
    dock.hidden = false;
  }
  $('#task-dock-title').textContent = active.length
    ? `${active.length} background job${active.length === 1 ? '' : 's'}`
    : 'Background jobs';
  const status = $('#task-dock-status');
  status.textContent = active.length ? 'working' : 'idle';
  status.className = `badge ${active.length ? 'blue' : 'free'}`;
  const visible = state.jobs.slice(0, 8);
  if (!visible.length) {
    body.append(emptyState('No background jobs', 'Mine, recover evidence, chat, or run a production.'));
    return;
  }
  visible.forEach((job) => {
    const card = el('article', 'task-mini');
    const head = el('div', 'task-mini-head');
    append(head, el('strong', null, `${humanStatus(job.op)} · ${job.scope || 'all'}`),
      badge(humanStatus(job.status), statusKind(job.status)));
    const progress = el('p', null, job.error || job.progress || 'Waiting');
    append(card, head, progress);
    if (['queued', 'running'].includes(job.status)) {
      const bar = el('progress');
      bar.max = 100;
      bar.removeAttribute('value');
      card.append(bar);
    } else if (job.ended) {
      card.append(el('span', 'small-note', `Finished ${relativeAge(job.ended)}`));
    }
    body.append(card);
  });
}

$('#task-dock-toggle').addEventListener('click', () => $('#task-dock').classList.toggle('open'));
setInterval(() => {
  if (!document.hidden) refreshJobs().catch(() => {});
}, 1400);

function workspaceOptions(includeAll = true) {
  const options = [];
  if (includeAll) options.push({value: '', label: 'All workspaces'});
  (state.overview?.workspaces || []).slice(0, 100).forEach((workspace) => {
    options.push({
      value: workspace.id,
      label: `${workspace.name} · ${workspace.sessions} sessions · ${workspace.nuggets} evidence`,
    });
  });
  return options;
}

async function loadRecoveryData(force = false) {
  await loadOverview(force);
  if (force || !state.recoveryRuns.length) {
    const response = await get('/api/recovery-runs?limit=10&offset=0');
    state.recoveryRuns = Array.isArray(response) ? response : (response.items || []);
    state.recoveryRunsTotal = Number(Array.isArray(response) ? state.recoveryRuns.length : (response.total ?? state.recoveryRuns.length));
  }
  const filters = state.recoverFilters;
  const params = new URLSearchParams({
    limit: String(state.recoverPageSize),
    offset: String(state.recoverPage * state.recoverPageSize),
    sort: 'consequence',
  });
  if (filters.search) params.set('search', filters.search);
  if (filters.tool) params.set('tool', filters.tool);
  if (filters.days) params.set('days', filters.days);
  const [sessions, stats] = await Promise.all([
    get(`/api/sessions?${params}`), get(`/api/session-stats?${params}`),
  ]);
  const maxPage = Math.max(0, Math.ceil(Number(stats.target || 0) / state.recoverPageSize) - 1);
  if (state.recoverPage > maxPage) {
    state.recoverPage = maxPage;
    return loadRecoveryData(force);
  }
  state.sessions = sessions || [];
  state.sessionStats = stats;
}

async function renderRecover() {
  await loadRecoveryData();
  const root = clear($('#recover-content'));
  append(root, pageHead('Recover',
    'Find what matters before clearing what does not.',
    'Choose exact sessions or a saved scope. Every mine is durable, repeatable, and linked to the evidence and outputs it produced.',
    [
      button('Refresh sources', 'button ghost', () => startJob({op: 'refresh'}, {
        message: 'Source refresh started in the background.',
      }).catch(showError)),
      button('New mine', 'button', () => {
        state.mineBuilderOpen = true;
        renderRecover().catch(showError);
      }),
    ]));

  const sessions = state.sessions;
  const atRisk = sessions.filter((session) => session.risk && session.risk !== 'ok').length;
  append(root, metricGrid([
    {label: 'Indexed', value: state.overview.stats?.sessions || 0, copy: 'sessions across installed tools'},
    {label: 'In view', value: state.sessionStats?.target || sessions.length, copy: 'matching current inventory'},
    {label: 'At risk on page', value: atRisk, copy: `within ${state.recoverPageSize} displayed rows`},
    {label: 'Evidence', value: state.overview.stats?.nuggets || 0, copy: 'recovered items'},
    {label: 'Mine runs', value: state.recoveryRuns.length, copy: 'durable recovery history'},
  ]));

  if (state.mineBuilderOpen) root.append(renderMineBuilder());

  const layout = el('div', 'content-grid');
  layout.append(renderSessionInventory(), renderRecoveryHistory());
  root.append(layout);
}

function renderMineBuilder() {
  const panel = el('section', 'panel mine-builder open');
  const inner = el('div', 'panel-inner');
  inner.append(panelHead('New mine', 'Choose a precise recovery scope',
    'Assay is free. Evidence extraction previews its long-running cost before it starts.',
    [button('Close', 'button ghost compact', () => {
      state.mineBuilderOpen = false;
      renderRecover().catch(showError);
    })]));
  const source = selectInput([
    {value: '', label: 'All installed tools'},
    {value: 'copilot', label: 'Copilot CLI'},
    {value: 'claude', label: 'Claude Code'},
    {value: 'opencode', label: 'OpenCode'},
  ]);
  const workspace = selectInput(workspaceOptions(true));
  const days = selectInput([
    {value: '7', label: 'Last 7 days'},
    {value: '30', label: 'Last 30 days'},
    {value: '90', label: 'Last 90 days'},
    {value: '0', label: 'Any time'},
  ], '30');
  const depth = selectInput([
    {value: 'summary', label: 'Summary · low cost'},
    {value: 'deep', label: 'Deep · cross-session'},
    {value: 'xray', label: 'X-ray · broad context'},
  ], 'summary');
  const mode = selectInput([
    {value: 'mine', label: 'Assay only · free'},
    {value: 'reclaim', label: 'Extract evidence · model-backed'},
  ], 'mine');
  const backend = selectInput([
    {value: '', label: 'Auto-detect signed-in CLI'},
    {value: 'copilot', label: 'Copilot CLI'},
    {value: 'claude', label: 'Claude Code'},
    {value: 'opencode', label: 'OpenCode'},
  ]);
  const grid = el('div', 'form-grid');
  append(grid, field('Source', source), field('Workspace', workspace),
    field('Time range', days), field('Depth', depth), field('Operation', mode),
    field('Backend', backend));
  inner.append(grid);

  const summary = el('div', 'selected-bar');
  const copy = el('div');
  const selected = state.selectedSessions.size;
  append(copy, el('strong', null, selected ? `${selected} exact session(s) selected` : 'Scope filters will choose sessions'),
    el('div', 'small-note', mode.value === 'mine'
      ? 'Runs in the background without a model call.'
      : 'A cost preview appears before evidence extraction starts.'));
  const start = button('Start', 'button success', async () => {
    const requestBody = {
      op: mode.value, tool: source.value, workspace: workspace.value,
      days: Number(days.value), depth: depth.value, backend: backend.value,
      session_keys: Array.from(state.selectedSessions), apply: mode.value === 'mine',
    };
    state.mineBuilderOpen = false;
    if (mode.value === 'mine') {
      await startJob(requestBody, {message: 'Mine started in the background.'});
      renderRecover().catch(showError);
    } else {
      await previewEvidenceExtraction(requestBody);
    }
  });
  append(summary, copy, start);
  inner.append(summary);
  panel.append(inner);
  return panel;
}

async function previewEvidenceExtraction(requestBody) {
  await startJob({...requestBody, apply: false}, {
    message: 'Preparing the evidence extraction estimate.',
    onDone: async (job) => {
      const result = job.result || {};
      const estimate = result.estimate || job.estimate || {};
      const body = openModal('Start evidence extraction',
        'This is the one approval for the selected long-running recovery scope.');
      append(body, metricGrid([
        {label: 'Sessions', value: result.sessions || 0, copy: result.depth || 'summary'},
        {label: 'Estimate', value: result.estimate_text || `${formatCount(estimate.low)}–${formatCount(estimate.high)}`, copy: estimate.samples ? 'calibrated' : 'conservative first run'},
        {label: 'Backend', value: result.backend || 'auto', copy: result.model || 'backend default'},
      ]));
      const actions = el('div', 'modal-actions');
      actions.append(button('Not now', 'button ghost', closeModal),
        button('Start background extraction', 'button success', async () => {
          closeModal();
          await startJob({...requestBody, apply: true}, {
            message: 'Evidence extraction is running in the background.',
          });
        }));
      body.append(actions);
    },
    onFailed: (job) => notice(job.error || 'Estimate failed', 'bad'),
  });
}

function renderSessionInventory() {
  const panel = el('section', 'panel');
  const inner = el('div', 'panel-inner');
  inner.append(panelHead('Session inventory', 'Select what you actually want to recover',
    'Consequence-first ordering surfaces risk and large dormant transcripts before routine history.',
    [badge(`${state.sessionStats?.target || state.sessions.length} matching`)]));

  const filter = textInput(state.recoverFilters.search, 'Filter title, workspace, repository');
  const tool = selectInput([
    {value: '', label: 'All sources'}, {value: 'copilot', label: 'Copilot'},
    {value: 'claude', label: 'Claude'}, {value: 'opencode', label: 'OpenCode'},
  ], state.recoverFilters.tool);
  const range = selectInput([
    {value: '7', label: 'Last 7 days'}, {value: '30', label: 'Last 30 days'},
    {value: '', label: 'Any time'},
  ], state.recoverFilters.days);
  const filters = el('div', 'filter-bar');
  append(filters, filter, tool, range, button('Clear selection', 'button ghost compact', () => {
    state.selectedSessions.clear();
    state.selectedSessionMeta.clear();
    renderRecover().catch(showError);
  }));
  inner.append(filters);

  const wrap = el('div', 'table-wrap');
  const table = el('table');
  const head = el('thead');
  const header = el('tr');
  ['','Session','Source','Updated','Size','Risk',''].forEach((label) => header.append(el('th', null, label)));
  head.append(header);
  const tbody = el('tbody');

  const draw = () => {
    clear(tbody);
    state.sessions.forEach((session) => {
        const row = el('tr');
        const key = sessionKey(session);
        const check = el('input', 'row-check');
        check.type = 'checkbox';
        check.checked = state.selectedSessions.has(key);
        check.setAttribute('aria-label', `Select ${session.title || session.short}`);
        check.addEventListener('change', () => {
          if (check.checked) {
            state.selectedSessions.add(key);
            state.selectedSessionMeta.set(key, {bytes: Number(session.bytes || 0)});
          } else {
            state.selectedSessions.delete(key);
            state.selectedSessionMeta.delete(key);
          }
          updateSelectedSummary();
    });
        const title = el('td');
        append(title, el('span', 'session-title', session.title || session.short),
          el('span', 'session-path', session.dir));
        append(row, el('td'), title, el('td', null, humanStatus(session.tool)),
          el('td', null, session.age), el('td', null, formatBytes(session.bytes)),
          el('td'), el('td'));
        row.children[0].append(check);
        row.children[5].append(badge(session.risk || 'ok', session.risk === 'critical' ? 'danger' : session.risk === 'ok' ? '' : 'spend'));
        row.children[6].append(button('Inspect', 'button ghost compact', () => openSession(session)));
        tbody.append(row);
      });
    if (!tbody.children.length) {
      const row = el('tr');
      const cell = el('td');
      cell.colSpan = 7;
      cell.append(emptyState('No sessions match', 'Change the filters or refresh the source index.'));
      row.append(cell);
      tbody.append(row);
    }
  };
  let searchTimer;
  filter.addEventListener('input', () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      state.recoverFilters.search = filter.value.trim();
      state.recoverPage = 0;
      renderRecover().catch(showError);
    }, 220);
  });
  tool.addEventListener('change', () => {
    state.recoverFilters.tool = tool.value;
    state.recoverPage = 0;
    renderRecover().catch(showError);
  });
  range.addEventListener('change', () => {
    state.recoverFilters.days = range.value;
    state.recoverPage = 0;
    renderRecover().catch(showError);
  });
  draw();
  append(table, head, tbody);
  wrap.append(table);
  inner.append(wrap);
  inner.append(pager(Number(state.sessionStats?.target || 0), state.recoverPage,
    state.recoverPageSize, (page) => {
      state.recoverPage = page;
      renderRecover().catch(showError);
    }, 'sessions'));

  const selected = el('div', 'selected-bar');
  selected.id = 'selected-session-summary';
  inner.append(selected);
  panel.append(inner);
  requestAnimationFrame(updateSelectedSummary);
  return panel;
}

function updateSelectedSummary() {
  const selected = $('#selected-session-summary');
  if (!selected) return;
  clear(selected);
  const totalBytes = Array.from(state.selectedSessionMeta.values())
    .reduce((sum, session) => sum + Number(session.bytes || 0), 0);
  const copy = el('div');
  append(copy, el('strong', null, `${state.selectedSessions.size} session(s) selected`),
    el('div', 'small-note', `${formatBytes(totalBytes)} source footprint`));
  append(selected, copy, button('Mine selected', 'button compact', () => {
    state.mineBuilderOpen = true;
    renderRecover().catch(showError);
  }));
}

function renderRecoveryHistory() {
  const stack = el('div', 'stack');
  const panel = el('section', 'panel');
  const inner = el('div', 'panel-inner');
  inner.append(panelHead('Mine history', 'Runs remain available',
    'Open, extend, or repeat a previous recovery scope.',
    [button('Activity', 'button ghost compact', () => activateView('activity'))]));
  const list = el('div', 'run-list');
  (state.recoveryRuns || []).slice(0, 8).forEach((run) => {
    let scope = {};
    try { scope = JSON.parse(run.scope || '{}'); } catch {}
    const row = el('article', 'run-row');
    const copy = el('div');
    const selectedCount = scope.session_keys?.length || scope.session_ids?.length || 0;
    const label = selectedCount
      ? `${selectedCount} selected sessions`
      : scope.workspace || (scope.days ? `Last ${scope.days} days` : 'All indexed sessions');
    append(copy, el('h3', null, `${humanStatus(run.op)} · ${label}`),
      el('p', null, `${run.sessions || 0} sessions · ${run.depth || 'assay'} · ${run.backend || 'local'}`));
    const meta = el('div', 'run-meta');
    append(meta, badge(humanStatus(run.status), statusKind(run.status)),
      run.assayed ? badge(`${run.assayed} assayed`, 'free') : null,
      run.evidence ? badge(`${run.evidence} evidence`, 'blue') : null,
      badge(relativeAge(run.started)));
    append(copy, meta);
    append(row, copy, button('Repeat', 'button ghost compact', () => {
      state.mineBuilderOpen = true;
      renderRecover().catch(showError);
    }));
    list.append(row);
  });
  if (!list.children.length) list.append(emptyState('No recovery runs yet', 'Start with a free assay of one small scope.'));
  inner.append(list);
  panel.append(inner);

  const rule = el('section', 'panel');
  const ruleInner = el('div', 'panel-inner');
  append(ruleInner, el('div', 'eyebrow', 'Recovery rule'),
    el('h3', null, 'A result never replaces the controls.'),
    el('p', 'muted', 'Every run keeps its scope, source fingerprint, evidence count, errors, and cleanup implications.'));
  rule.append(ruleInner);
  append(stack, panel, rule);
  return stack;
}

function openSession(session) {
  const body = openDrawer(session.title || session.short);
  body.append(el('p', 'muted', `${humanStatus(session.tool)} · ${session.id} · ${session.dir}`));
  body.append(metricGrid([
    {label: 'Transcript', value: formatBytes(session.bytes), copy: session.risk === 'ok' ? 'below resume threshold' : `${session.risk} consequence`},
    {label: 'Turns', value: session.turns, copy: `updated ${session.age}`},
    {label: 'Workspace', value: session.dir_exists ? 'present' : 'missing', copy: session.repo || session.dir},
    {label: 'State', value: session.live ? 'open' : 'closed', copy: session.live ? 'do not resume' : 'available for recovery'},
  ]));
  const actions = el('div', 'actions');
  append(actions,
    button('Copy resume command', 'button ghost', () => copyText(session.resume)),
    button('Rescue handoff', 'button', () => {
      closeDrawer();
      startJob({op: 'brief', session_keys: [sessionKey(session)], records: 12}, {
        message: 'Session rescue started.',
        onDone: (job) => openHandoff(job.result),
      }).catch(showError);
    }),
    button('Mine this session', 'button success', () => {
      closeDrawer();
      startJob({op: 'mine', session_keys: [sessionKey(session)], days: 0, apply: true}, {
        message: 'Exact-session mine started.',
      }).catch(showError);
    }),
    button('Extract evidence', 'button ghost', () => {
      closeDrawer();
      previewEvidenceExtraction({
        op: 'reclaim', session_keys: [sessionKey(session)], days: 0,
        depth: 'summary', backend: '', apply: false,
      }).catch(showError);
    }),
    button('Preview archive', 'button warning', () => {
      closeDrawer();
      previewArchive(session);
    }));
  body.append(actions);
}

function openHandoff(result = {}) {
  const body = openDrawer(`Handoff · ${result.title || result.session || 'session'}`);
  const note = el('p', 'muted', result.saved ? `Saved to ${result.saved}` : 'Local handoff');
  const preview = el('pre', 'source-stage', 'Loading saved handoff…');
  const copy = button('Copy handoff', 'button');
  copy.disabled = true;
  append(body, note, preview, copy);
  if (!result.saved) {
    preview.textContent = 'The handoff could not be saved.';
    return;
  }
  get(`/api/artifact?path=${encodeURIComponent(result.saved)}`).then((artifact) => {
    const text = artifact.body || '';
    preview.textContent = text;
    copy.disabled = false;
    copy.addEventListener('click', () => copyText(text));
  }).catch((error) => {
    preview.textContent = error.message;
  });
}

async function previewArchive(session) {
  await startJob({op: 'archive', session_keys: [sessionKey(session)], apply: false}, {
    message: 'Preparing archive preview.',
    onDone: (job) => {
      const result = job.result || {};
      const body = openModal('Archive preview', 'Archive is reversible and remains separate from permanent removal.');
      append(body, metricGrid([
        {label: 'Sessions', value: result.rows?.length || 0, copy: 'closed source transcripts'},
        {label: 'Footprint', value: formatBytes((result.rows || []).reduce((sum, row) => sum + Number(row.bytes || 0), 0)), copy: 'moves into Midden archive'},
      ]));
      const actions = el('div', 'modal-actions');
      append(actions, button('Cancel', 'button ghost', closeModal),
        button('Archive now', 'button warning', async () => {
          closeModal();
          await startJob({op: 'archive', session_keys: [sessionKey(session)], apply: true, confirm: true}, {
            message: 'Reversible archive started.',
          });
        }));
      body.append(actions);
    },
  });
}

async function loadWorkItems(force = false) {
  await loadOverview(force);
  if (force || !state.workItems.length) {
    const params = new URLSearchParams({
      limit: String(state.workPageSize),
      offset: String(state.workPage * state.workPageSize),
    });
    if (state.workSearch) params.set('search', state.workSearch);
    const response = await get(`/api/work-items?${params}`);
    state.workItems = response.items || [];
    state.workTotal = Number(response.total || 0);
    const maxPage = Math.max(0, Math.ceil(state.workTotal / state.workPageSize) - 1);
    if (state.workPage > maxPage) {
      state.workPage = maxPage;
      state.workItems = [];
      return loadWorkItems(force);
    }
  }
  if (!state.selectedWork && state.workItems.length) state.selectedWork = state.workItems[0].recipe.uid;
}

async function selectWork(recipeID) {
  state.selectedWork = recipeID;
  sessionStorage.setItem('midden.selectedWork', recipeID);
  state.workDetail = null;
  state.selectedOutput = '';
  state.outputDetail = null;
  await renderStudio();
}

async function loadWorkDetail(force = false) {
  if (!state.selectedWork) return null;
  if (force || !state.workDetail || state.workDetail.recipe.uid !== state.selectedWork) {
    const messagePage = state.messagePages.get(state.selectedWork) || 0;
    const messageLimit = 30;
    const messageOffset = messagePage * messageLimit;
    state.workDetail = await get(`/api/work-item?id=${encodeURIComponent(state.selectedWork)}&message_limit=${messageLimit}&message_offset=${messageOffset}`);
  }
  return state.workDetail;
}

async function renderStudio() {
  await loadWorkItems();
  const root = clear($('#studio-content'));
  append(root, pageHead('Studio', 'One place to talk, operate, edit, and preview.',
    'Work items stay visible. Chat is persistent, Console is controlled, and every generated file has an appropriate preview and download path.',
    [
      button('Import evidence set', 'button ghost', openCreateWorkItem),
      button('New work item', 'button', openCreateWorkItem),
    ]));
  if (!state.workItems.length && !state.selectedWork) {
    root.append(emptyState('No work items yet',
      'Recover evidence, then create a focused output plan without leaving Studio.',
      button('Create the first work item', 'button', openCreateWorkItem)));
    return;
  }
  const detail = await loadWorkDetail();
  const shell = el('div', 'studio-shell');
  append(shell, renderWorkRail(), renderConversation(detail), renderPreview(detail));
  root.append(shell);
}

function renderWorkRail() {
  const rail = el('aside', 'work-rail');
  const head = el('div', 'rail-head');
  const top = el('div', 'actions');
  top.style.justifyContent = 'space-between';
  append(top, el('strong', null, 'Work items'), badge(`${state.workTotal} active`, 'blue'));
  const search = textInput(state.workSearch, 'Find work item');
  append(head, top, search);
  const list = el('div', 'work-list');
  const draw = () => {
    clear(list);
    const visible = [...state.workItems];
    if (state.workDetail && !visible.some((item) => item.recipe.uid === state.workDetail.recipe.uid)) {
      visible.unshift({recipe: state.workDetail.recipe, output_count: state.workDetail.outputs.length});
    }
    visible.forEach((item) => {
        const recipe = item.recipe;
        const node = el('button', `work-item ${recipe.uid === state.selectedWork ? 'active' : ''}`.trim());
        node.type = 'button';
        const description = recipe.outputs?.map((output) => output.title).slice(0, 3).join(', ') || 'Evidence work item';
        append(node, el('strong', null, recipe.title), el('span', null, truncate(description, 72)));
        const meta = el('div', 'work-item-meta');
        append(meta, badge(humanStatus(recipe.status), statusKind(recipe.status)),
          el('span', null, relativeAge(recipe.updated_at)));
        node.append(meta);
        node.addEventListener('click', () => selectWork(recipe.uid).catch(showError));
        list.append(node);
    });
    return {total: state.workTotal};
  };
  let searchTimer;
  search.addEventListener('input', () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      state.workSearch = search.value.trim();
      state.workPage = 0;
      state.workItems = [];
      renderStudio().catch(showError);
    }, 220);
  });
  let pageResult = draw();
  const foot = el('div', 'work-rail-foot');
  const renderFoot = (result) => {
    clear(foot);
    foot.append(pager(result.total, state.workPage, state.workPageSize, (page) => {
      state.workPage = page;
      state.workItems = [];
      renderStudio().catch(showError);
    }, 'work items'));
    foot.append(button('New work item', 'button ghost compact', openCreateWorkItem));
  };
  renderFoot(pageResult);
  append(rail, head, list, foot);
  return rail;
}

function renderConversation(detail) {
  const pane = el('section', 'conversation-pane');
  const head = el('header', 'conversation-head');
  const copy = el('div');
  append(copy, el('div', 'eyebrow', `${detail.recipe.evidence_ids.length} evidence · ${detail.outputs.length} outputs`),
    el('h2', null, detail.recipe.title));
  const budget = el('div', 'budget');
  const line = el('div', 'budget-line');
  const spent = Number(detail.thread?.estimated_spent || 0);
  const limit = Number(detail.thread?.budget_tokens || 1_200_000);
  append(line, el('span', null, detail.thread?.backend || 'AI CLI not selected'),
    el('span', null, `${Math.min(100, Math.round(spent / limit * 100))}% budget`));
  const progress = el('progress');
  progress.max = limit;
  progress.value = Math.min(limit, spent);
  append(budget, line, progress);
  append(head, copy, budget);

  const body = el('div', 'conversation-body');
  const chat = renderChat(detail);
  const consolePanel = renderConsole(detail);
  chat.hidden = state.conversationMode !== 'chat';
  consolePanel.hidden = state.conversationMode !== 'console';
  append(body, chat, consolePanel);
  const tabs = el('div', 'conversation-tabs');
  const switcher = el('div', 'segmented');
  ['chat', 'console'].forEach((mode) => {
    const item = button(mode, `segment ${state.conversationMode === mode ? 'active' : ''}`.trim(), () => {
      state.conversationMode = mode;
      renderStudio().catch(showError);
    });
    switcher.append(item);
  });
  tabs.append(switcher);
  append(pane, head, body, tabs);
  return pane;
}

function renderChat(detail) {
  const panel = el('div', 'chat-panel');
  const messages = el('div', 'messages');
  messages.id = 'work-messages';
  messages.append(el('div', 'message system',
    `Evidence is fixed to this work item. Routine turns use one persistent CLI session and the approved ${formatCount(detail.thread?.budget_tokens || 1_200_000)}-token envelope.`));
  const allMessages = detail.messages || [];
  const messageTotal = Number(detail.message_total || allMessages.length);
  const messagePage = state.messagePages.get(detail.recipe.uid) || 0;
  const messageLimit = Number(detail.message_limit || 30);
  const messageOffset = Number(detail.message_offset || 0);
  if (messageTotal > messageLimit) {
    const controls = el('div', 'pager');
    const newer = button('Newer messages', 'button ghost compact', () => {
      state.messagePages.set(detail.recipe.uid, Math.max(0, messagePage - 1));
      state.workDetail = null;
      renderStudio().catch(showError);
    });
    newer.disabled = messagePage === 0;
    const older = button('Older messages', 'button ghost compact', () => {
      state.messagePages.set(detail.recipe.uid, messagePage + 1);
      state.workDetail = null;
      renderStudio().catch(showError);
    });
    older.disabled = messageOffset + allMessages.length >= messageTotal;
    const first = Math.max(1, messageTotal - messageOffset - allMessages.length + 1);
    const last = messageTotal - messageOffset;
    append(controls, newer, el('span', null, `${first}–${last} of ${messageTotal} messages`), older);
    messages.append(controls);
  }
  if (!allMessages.length) {
    messages.append(el('div', 'message agent',
      'This work item is ready. Ask a question, request a revision, or inspect the current outputs on the right.'));
  }
  allMessages.slice(-messageLimit).forEach((message) => {
    const node = el('div', `message ${message.role === 'agent' ? 'agent' : message.role}`);
    append(node, document.createTextNode(message.body),
      el('small', null, `${message.role === 'agent' ? detail.thread?.backend || 'AI CLI' : 'operator'} · ${formatDate(message.created_at)}`));
    messages.append(node);
  });
  const composer = el('form', 'composer');
  const input = textArea('', 'Ask, revise, or create another output…');
  input.rows = 3;
  input.disabled = state.pendingChat.has(detail.recipe.uid);
  const foot = el('div', 'composer-foot');
  const send = button(state.pendingChat.has(detail.recipe.uid) ? 'Working…' : 'Send', 'button compact');
  send.disabled = state.pendingChat.has(detail.recipe.uid);
  append(foot, el('span', null, 'Same session · same evidence · no per-turn approval'), send);
  append(composer, input, foot);
  composer.addEventListener('submit', async (event) => {
    event.preventDefault();
    const question = input.value.trim();
    if (!question) return;
    input.value = '';
    const optimistic = el('div', 'message user', question);
    const typing = el('div', 'message agent typing', 'Thinking in the persistent work session');
    append(messages, optimistic, typing);
    messages.scrollTop = messages.scrollHeight;
    state.pendingChat.add(detail.recipe.uid);
    state.messagePages.set(detail.recipe.uid, 0);
    input.disabled = true;
    send.disabled = true;
    send.textContent = 'Working…';
    try {
      await startJob({
        op: 'work_chat', recipe_id: detail.recipe.uid, question,
        backend: detail.thread?.backend || '', model: detail.thread?.model || '',
        budget_tokens: detail.thread?.budget_tokens || 1_200_000,
      }, {
        message: 'Message sent. You can continue using Midden.',
        onDone: async () => {
          state.pendingChat.delete(detail.recipe.uid);
          state.workDetail = null;
          await renderStudio();
        },
        onFailed: async (job) => {
          state.pendingChat.delete(detail.recipe.uid);
          typing.remove();
          input.disabled = false;
          send.disabled = false;
          send.textContent = 'Send';
          notice(job.error || 'Work chat failed', 'bad');
        },
      });
    } catch (error) {
      state.pendingChat.delete(detail.recipe.uid);
      typing.remove();
      input.disabled = false;
      send.disabled = false;
      send.textContent = 'Send';
      showError(error);
    }
  });
  append(panel, messages, composer);
  requestAnimationFrame(() => { messages.scrollTop = messages.scrollHeight; });
  return panel;
}

function renderConsole(detail) {
  const panel = el('div', 'console-panel');
  const output = el('pre', 'terminal-output');
  const history = state.consoleHistory.get(detail.recipe.uid) || [
    'midden controlled console',
    `work item: ${detail.recipe.title}`,
    'commands: help · status · files · evidence · runs · openmontage status',
    '',
  ];
  output.textContent = history.join('\n');
  const feedback = el('div', 'console-feedback');
  const previousFeedback = state.consoleFeedback.get(detail.recipe.uid);
  if (previousFeedback) {
    feedback.textContent = previousFeedback.text;
    feedback.classList.add(previousFeedback.kind);
  } else {
    feedback.hidden = true;
  }
  const form = el('form', 'terminal-input');
  const prompt = el('span', null, '>');
  const input = textInput('', 'status');
  input.autocomplete = 'off';
  input.spellcheck = false;
  append(form, prompt, input);
  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    const command = input.value.trim();
    if (!command) return;
    history.push(`> ${command}`);
    input.value = '';
    try {
      const result = await post('/api/work-console', {
        recipe_id: detail.recipe.uid, command,
      });
      history.push(result.output || '(no output)', '');
      state.consoleFeedback.set(detail.recipe.uid, {
        kind: 'success', text: `Command completed: ${command}`,
      });
    } catch (error) {
      history.push(`error: ${error.message}`, '');
      state.consoleFeedback.set(detail.recipe.uid, {
        kind: 'error', text: `Command rejected: ${error.message}`,
      });
    }
    state.consoleHistory.set(detail.recipe.uid, history);
    output.textContent = history.join('\n');
    output.scrollTop = output.scrollHeight;
    const currentFeedback = state.consoleFeedback.get(detail.recipe.uid);
    feedback.hidden = false;
    feedback.textContent = currentFeedback.text;
    feedback.className = `console-feedback ${currentFeedback.kind}`;
  });
  append(panel, output, feedback, form);
  return panel;
}

function renderPreview(detail) {
  const pane = el('section', 'preview-pane');
  const selected = detail.outputs.find((output) => output.uid === state.selectedOutput) || detail.outputs[0];
  if (selected && selected.uid !== state.selectedOutput) {
    state.selectedOutput = selected.uid;
    state.outputDetail = null;
  }
  const head = el('header', 'preview-head');
  const copy = el('div');
  append(copy, el('div', 'eyebrow', 'Live output'),
    el('h2', null, selected?.title || 'Production plan'));
  const pills = el('div', 'pills');
  append(pills, badge(humanStatus(selected?.status || detail.recipe.status), statusKind(selected?.status || detail.recipe.status)),
    selected ? badge(selected.format) : null);
  append(head, copy, pills);
  pane.append(head);

  const tabs = el('div', 'output-tabs');
  detail.outputs.forEach((output) => {
    const tab = button(output.title, `output-tab ${output.uid === state.selectedOutput ? 'active' : ''}`.trim(), () => {
      state.selectedOutput = output.uid;
      state.outputDetail = null;
      state.previewMode = 'rendered';
      renderStudio().catch(showError);
    });
    tabs.append(tab);
  });
  if (!detail.outputs.length) tabs.append(button('+ Add output', 'output-tab', () => editWorkItemOutputs(detail)));
  pane.append(tabs);

  const toolbar = el('div', 'preview-toolbar');
  const modes = el('div', 'segmented');
  ['rendered', 'source', 'provenance'].forEach((mode) => {
    const modeButton = button(mode, `segment ${state.previewMode === mode ? 'active' : ''}`.trim(), () => {
      state.previewMode = mode;
      renderStudio().catch(showError);
    });
    if (!selected) modeButton.disabled = true;
    modes.append(modeButton);
  });
  const actions = el('div', 'actions');
  if (selected) {
    append(actions,
      button('Download', 'button ghost compact', () => downloadOutput(selected)),
      ['reviewed', 'exported'].includes(selected.status)
        ? button(selected.status === 'exported' ? 'Exported' : 'Export local', 'button compact', () => exportOutput(selected.uid))
        : null);
  } else {
    append(actions,
      ['draft', 'evidence_review'].includes(detail.recipe.status)
        ? button('Review evidence', 'button ghost compact', () => openEvidenceReview(detail))
        : null,
      ['approved', 'failed'].includes(detail.recipe.status)
        ? button('Preview run', 'button compact', () => previewProduction(detail.recipe.uid))
        : null);
  }
  append(toolbar, modes, actions);
  pane.append(toolbar);

  const content = el('div', 'preview-content');
  if (selected) {
    content.append(el('div', 'loading-shell', 'Loading output…'));
    loadOutputDetail(selected.uid).then(() => {
      clear(content).append(renderOutputContent(detail, selected));
    }).catch((error) => clear(content).append(emptyState('Output could not be loaded', error.message)));
  } else {
    content.append(renderPlanPreview(detail));
  }
  pane.append(content);
  return pane;
}

async function loadOutputDetail(outputID) {
  if (!state.outputDetail || state.outputDetail.output.uid !== outputID) {
    state.outputDetail = await get(`/api/refinery/output?id=${encodeURIComponent(outputID)}`);
  }
  return state.outputDetail;
}

function renderPlanPreview(detail) {
  const stage = el('article', 'document-stage');
  append(stage, el('div', 'eyebrow', humanStatus(detail.recipe.status)),
    el('h1', null, detail.recipe.title),
    el('p', null, detail.recipe.request || 'Saved evidence-grounded production plan.'));
  const list = el('ul');
  detail.recipe.outputs.forEach((output) => {
    list.append(el('li', null, `${output.title} · ${output.format} · ${output.requires_model ? 'model-backed' : 'deterministic'}`));
  });
  stage.append(list);
  const actions = el('div', 'actions');
  if (['draft', 'evidence_review'].includes(detail.recipe.status)) {
    append(actions, button('Review evidence', 'button', () => openEvidenceReview(detail)),
      button('Edit outputs', 'button ghost', () => editWorkItemOutputs(detail)));
  }
  if (['approved', 'failed'].includes(detail.recipe.status)) {
    actions.append(button('Preview cost and run', 'button success', () => previewProduction(detail.recipe.uid)));
  }
  stage.append(actions);
  return stage;
}

function renderOutputContent(detail, output) {
  const outputDetail = state.outputDetail;
  if (!outputDetail) return emptyState('Loading', 'Output is still loading.');
  if (state.previewMode === 'source') return renderSourceEditor(outputDetail);
  if (state.previewMode === 'provenance') return renderProvenance(outputDetail.provenance, output);
  return renderOwnedPreview(outputDetail.body || '', output.format, output);
}

function renderOwnedPreview(body, format, output) {
  if (format === 'jsonl') return renderJSONL(body, output);
  if (format === 'json') {
    try {
      const value = JSON.parse(body);
      const stage = el('div', 'record-list');
      Object.entries(value).forEach(([key, item]) => {
        const card = el('article', 'record-card');
        append(card, el('h3', null, humanStatus(key)),
          el('p', null, typeof item === 'object' ? JSON.stringify(item, null, 2) : String(item)));
        stage.append(card);
      });
      return stage;
    } catch {}
  }
  if (['markdown', 'marp'].includes(format)) {
    const stage = el('article', 'document-stage');
    stage.innerHTML = markdownToHTML(body);
    return stage;
  }
  if (format === 'd2') {
    const wrap = el('div', 'rendered-stage');
    const image = el('img', 'rendered-media');
    image.alt = output.title;
    image.src = `/api/output-rendered?id=${encodeURIComponent(output.uid)}`;
    image.addEventListener('error', () => {
      clear(wrap).append(emptyState('D2 source is ready',
        'D2 is not installed or could not render this file. Connect it under Tools, or edit and download the source.'));
      wrap.append(el('pre', 'source-stage', body));
    }, {once: true});
    wrap.append(image);
    return wrap;
  }
  if (['png', 'jpg', 'jpeg', 'gif', 'webp'].includes(format)) {
    const image = el('img', 'rendered-media');
    image.alt = output.title;
    image.src = `/api/output-rendered?id=${encodeURIComponent(output.uid)}`;
    return append(el('div', 'rendered-stage'), image);
  }
  if (['mp4', 'webm'].includes(format)) {
    const video = el('video', 'rendered-media');
    video.controls = true;
    video.src = `/api/output-rendered?id=${encodeURIComponent(output.uid)}`;
    return append(el('div', 'rendered-stage'), video);
  }
  return el('pre', 'source-stage', body || 'This output is empty.');
}

function markdownToHTML(markdown) {
  const lines = String(markdown || '').replace(/\r/g, '').split('\n');
  let html = '';
  let listOpen = false;
  let codeOpen = false;
  const inline = (value) => escapeHTML(value)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/\*([^*]+)\*/g, '<em>$1</em>');
  for (const line of lines) {
    if (/^```/.test(line)) {
      if (listOpen) { html += '</ul>'; listOpen = false; }
      html += codeOpen ? '</code></pre>' : '<pre><code>';
      codeOpen = !codeOpen;
      continue;
    }
    if (codeOpen) {
      html += `${escapeHTML(line)}\n`;
      continue;
    }
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    if (heading) {
      if (listOpen) { html += '</ul>'; listOpen = false; }
      const level = heading[1].length;
      html += `<h${level}>${inline(heading[2])}</h${level}>`;
      continue;
    }
    const list = line.match(/^\s*[-*]\s+(.+)$/);
    if (list) {
      if (!listOpen) { html += '<ul>'; listOpen = true; }
      html += `<li>${inline(list[1])}</li>`;
      continue;
    }
    if (listOpen) { html += '</ul>'; listOpen = false; }
    if (/^>\s?/.test(line)) {
      html += `<blockquote>${inline(line.replace(/^>\s?/, ''))}</blockquote>`;
    } else if (line.trim()) {
      html += `<p>${inline(line)}</p>`;
    }
  }
  if (listOpen) html += '</ul>';
  if (codeOpen) html += '</code></pre>';
  return html || '<p>This output is empty.</p>';
}

function renderJSONL(body, output) {
  const wrap = el('div');
  const list = el('div', 'record-list');
  const pagerWrap = el('div');
  const lines = String(body || '').split(/\r?\n/).filter((line) => line.trim());
  let page = state.recordPages.get(output.uid) || 0;
  const pageSize = 20;
  const draw = () => {
    clear(list);
    const result = pageSlice(lines, page, pageSize);
    page = result.page;
    state.recordPages.set(output.uid, page);
    result.items.forEach((line, localIndex) => {
      const index = page * pageSize + localIndex;
      const card = el('article', 'record-card');
      try {
        const value = JSON.parse(line);
        const title = value?.metadata?.title || value?.title || value?.description || `Record ${index + 1}`;
        const content = value?.text || value?.content || value?.response || value?.expected || JSON.stringify(value, null, 2);
        append(card, el('h3', null, title), el('p', null, content));
      } catch {
        append(card, el('h3', null, `Invalid line ${index + 1}`), el('p', null, line));
      }
      list.append(card);
    });
    if (!list.children.length) list.append(emptyState('No records', `${output.title} is empty.`));
    clear(pagerWrap).append(pager(lines.length, page, pageSize, (nextPage) => {
      page = nextPage;
      draw();
    }, 'records'));
  };
  draw();
  append(wrap, list, pagerWrap);
  return wrap;
}

function renderSourceEditor(detail) {
  const wrap = el('div', 'source-editor');
  const editor = textArea(detail.body || '');
  editor.className = 'document-editor';
  const actions = el('div', 'actions');
  append(actions,
    button('Reject', 'button danger compact', () => saveOutputReview(detail.output, editor.value, 'rejected')),
    button('Save draft', 'button ghost compact', () => saveOutputReview(detail.output, editor.value, 'draft')),
    button('Approve output', 'button success compact', () => saveOutputReview(detail.output, editor.value, 'reviewed')));
  append(wrap, editor, actions);
  return wrap;
}

function renderProvenance(body, output) {
  const wrap = el('div');
  const grid = el('div', 'provenance-grid');
  const pagerWrap = el('div');
  if (!body) return emptyState('No provenance sidecar', 'This output cannot be approved without provenance.');
  try {
    const value = JSON.parse(body);
    const items = value.evidence || [];
    let page = state.provenancePages.get(output.uid) || 0;
    const pageSize = 15;
    const draw = () => {
      clear(grid);
      const result = pageSlice(items, page, pageSize);
      page = result.page;
      state.provenancePages.set(output.uid, page);
      result.items.forEach((item) => {
        const card = el('article', 'provenance-card');
        append(card, el('h3', null, item.title || item.evidence_id),
          el('p', null, `${item.tool || 'source'} · ${String(item.session_id || '').slice(0, 8)} · ${Math.round(Number(item.confidence || 0) * 100)}% confidence`));
        grid.append(card);
      });
      clear(pagerWrap).append(pager(items.length, page, pageSize, (nextPage) => {
        page = nextPage;
        draw();
      }, 'evidence sources'));
    };
    if (!items.length) {
      grid.append(el('pre', 'source-stage', JSON.stringify(value, null, 2)));
    } else {
      draw();
    }
  } catch {
    grid.append(el('pre', 'source-stage', body));
  }
  append(wrap, grid, pagerWrap);
  return wrap;
}

async function saveOutputReview(output, body, decision) {
  try {
    await post('/api/refinery/action', {
      action: 'review_output', output_id: output.uid, decision, body,
    });
    state.workDetail = null;
    state.outputDetail = null;
    state.overview = null;
    state.workItems = [];
    await renderStudio();
    toast(`Output ${humanStatus(decision)}`);
  } catch (error) {
    showError(error);
  }
}

function downloadOutput(output) {
  window.location.href = `/api/output-download?id=${encodeURIComponent(output.uid)}`;
  toast('Download started.');
}

async function exportOutput(outputID) {
  try {
    const result = await post('/api/refinery/action', {
      action: 'export_output', output_id: outputID, destination: 'local_vault',
    });
    state.workDetail = null;
    state.outputDetail = null;
    state.overview = null;
    state.workItems = [];
    await renderStudio();
    notice(`Exported locally: ${result.path}`);
  } catch (error) {
    showError(error);
  }
}

function openEvidenceReview(detail) {
  const body = openDrawer(`Evidence · ${detail.recipe.title}`);
  body.append(el('p', 'muted', 'Control exactly what every output is allowed to claim.'));
  const selected = new Set(detail.recipe.evidence_ids || []);
  const search = textInput('', 'Search evidence');
  const list = el('div', 'record-list');
  const pagerWrap = el('div');
  let page = 0;
  const pageSize = 15;
  const draw = () => {
    clear(list);
    const query = search.value.trim().toLowerCase();
    const filtered = (detail.evidence || []).filter((nugget) => !query ||
      `${nugget.title} ${nugget.body} ${nugget.kind}`.toLowerCase().includes(query));
    const result = pageSlice(filtered, page, pageSize);
    page = result.page;
    result.items.forEach((nugget) => {
      const card = el('label', 'record-card');
      const check = el('input', 'row-check');
      check.type = 'checkbox';
      check.value = nugget.uid;
      check.checked = selected.has(nugget.uid);
      check.addEventListener('change', () => {
        if (check.checked) selected.add(nugget.uid);
        else selected.delete(nugget.uid);
      });
      const copy = el('div');
      append(copy, el('h3', null, nugget.title || humanStatus(nugget.kind)),
        el('p', null, truncate(nugget.body, 320)));
      append(card, check, copy);
      list.append(card);
    });
    clear(pagerWrap).append(pager(filtered.length, page, pageSize, (nextPage) => {
      page = nextPage;
      draw();
    }, 'evidence items'));
  };
  search.addEventListener('input', () => {
    page = 0;
    draw();
  });
  draw();
  append(body, search, list, pagerWrap);
  const actions = el('div', 'actions');
  append(actions,
    button('Save selection', 'button ghost', async () => {
      await post('/api/refinery/action', {
        action: 'save_evidence', recipe_id: detail.recipe.uid,
        evidence_ids: Array.from(selected),
      });
      closeDrawer();
      state.workDetail = null;
      state.workItems = [];
      await renderStudio();
    }),
    button('Approve evidence', 'button success', async () => {
      await post('/api/refinery/action', {
        action: 'approve_evidence', recipe_id: detail.recipe.uid,
        evidence_ids: Array.from(selected),
      });
      closeDrawer();
      state.workDetail = null;
      state.workItems = [];
      await renderStudio();
      toast('Evidence approved. The work item can run.');
    }));
  body.append(actions);
}

function editWorkItemOutputs(detail) {
  const body = openModal('Edit outputs', 'Changing the output mix returns the work item to evidence review.');
  const selected = new Set((detail.recipe.outputs || []).map((output) => output.kind));
  const templates = [
    ['tutorial','Technical tutorial'], ['adr','Decision record'], ['release_pack','Release pack'],
    ['slides','Presentation deck'], ['diagram','Architecture diagram'], ['video_brief','Video brief'],
    ['handbook','Field guide'], ['flashcards','Flashcards'], ['quiz','Scenario quiz'],
    ['notebook_pack','Notebook pack'], ['skill','Skill proposal'], ['eval_pack','Evaluation pack'],
    ['retrieval_pack','Retrieval pack'], ['provenance_manifest','Provenance manifest'],
  ];
  const grid = el('div', 'form-grid');
  templates.forEach(([kind, title]) => {
    const label = el('label', 'check-row');
    const input = el('input');
    input.type = 'checkbox';
    input.value = kind;
    input.checked = selected.has(kind);
    append(label, input, el('span', null, title));
    grid.append(label);
  });
  body.append(grid);
  const actions = el('div', 'modal-actions');
  append(actions, button('Cancel', 'button ghost', closeModal),
    button('Save outputs', 'button', async () => {
      const kinds = $$('input:checked', grid).map((input) => input.value);
      await post('/api/refinery/action', {
        action: 'update_recipe', recipe_id: detail.recipe.uid, output_kinds: kinds,
      });
      closeModal();
      state.workDetail = null;
      state.workItems = [];
      await renderStudio();
    }));
  body.append(actions);
}

async function previewProduction(recipeID) {
  await startJob({op: 'production', recipe_id: recipeID, apply: false}, {
    message: 'Preparing the production estimate.',
    onDone: (job) => {
      const result = job.result || {};
      const estimate = result.estimate || job.estimate || {};
      const body = openModal('Start production',
        'This approval creates local drafts only. Nothing is published or installed.');
      append(body, metricGrid([
        {label: 'Outputs', value: result.recipe?.outputs?.length || 0, copy: 'drafts'},
        {label: 'Evidence', value: result.evidence_report?.selected || 0, copy: `${result.evidence_report?.quality || 0}% quality`},
        {label: 'Estimate', value: result.estimate_text || formatCount(estimate.mid), copy: estimate.samples ? 'calibrated' : 'conservative'},
      ]));
      const backend = selectInput([
        {value: '', label: 'Auto-detect signed-in CLI'},
        {value: 'copilot', label: 'Copilot CLI'},
        {value: 'claude', label: 'Claude Code'},
        {value: 'opencode', label: 'OpenCode'},
      ]);
      body.append(field('Backend', backend));
      const actions = el('div', 'modal-actions');
      append(actions, button('Not now', 'button ghost', closeModal),
        button('Run in background', 'button success', async () => {
          closeModal();
          await startJob({
            op: 'production', recipe_id: recipeID, apply: true, backend: backend.value,
          }, {
            message: 'Production is running in the background.',
            onDone: async () => {
              state.workDetail = null;
              state.workItems = [];
              state.overview = null;
              await renderStudio();
              toast('Drafts are ready for review.');
            },
          });
        }));
      body.append(actions);
    },
  });
}

function openCreateWorkItem() {
  const body = openModal('New work item',
    'Choose an evidence scope and the finished outcome. Nothing runs until its evidence is approved.');
  const workspace = selectInput(workspaceOptions(false));
  const title = textInput('', 'Optional work-item name');
  const prompt = textArea('', 'Create a tutorial and architecture diagram from this evidence.');
  prompt.rows = 4;
  const kinds = selectInput([
    {value: '', label: 'Infer outputs from request'},
    {value: 'tutorial,diagram', label: 'Tutorial + diagram'},
    {value: 'adr,handbook', label: 'ADR + field guide'},
    {value: 'video_brief,provenance_manifest', label: 'Video brief + provenance'},
    {value: 'skill,eval_pack', label: 'Skill + evaluation pack'},
  ]);
  append(body, field('Evidence workspace', workspace), field('Name', title),
    field('Finished outcome', prompt), field('Output starter', kinds));
  const actions = el('div', 'modal-actions');
  append(actions, button('Cancel', 'button ghost', closeModal),
    button('Create work item', 'button', async () => {
      const outputKinds = kinds.value ? kinds.value.split(',') : [];
      const result = await post('/api/refinery/action', {
        action: 'design', workspace: workspace.value, prompt: prompt.value,
        title: title.value, output_kinds: outputKinds,
      });
      closeModal();
      state.workItems = [];
      state.overview = null;
      await loadWorkItems(true);
      await selectWork(result.recipe.uid);
      toast('Work item created. Review its evidence before running.');
    }));
  body.append(actions);
}

async function renderLibrary() {
  const overview = await loadOverview();
  const root = clear($('#library-content'));
  append(root, pageHead('Library', 'Everything recovered and created, in usable form.',
    'Browse rendered results, source files, versions, provenance, and destinations without reopening a whole production.',
    [button('Open Studio', 'button', () => activateView('studio'))]));
  const toolbar = el('div', 'library-toolbar');
  const filters = el('div', 'segmented');
  const categories = [
    ['all','All'], ['document','Documents'], ['visual','Visuals'],
    ['video','Video'], ['data','Data'], ['agent','Agent'],
  ];
  categories.forEach(([kind, label]) => {
    filters.append(button(label, `segment ${state.libraryFilter === kind ? 'active' : ''}`.trim(), () => {
      state.libraryFilter = kind;
      state.libraryPage = 0;
      renderLibrary().catch(showError);
    }));
  });
  append(toolbar, filters, badge(`${overview.outputs?.length || 0} outputs`));
  root.append(toolbar);
  const grid = el('div', 'library-grid');
  const filtered = (overview.outputs || [])
    .filter((output) => state.libraryFilter === 'all' || outputCategory(output) === state.libraryFilter);
  const page = pageSlice(filtered, state.libraryPage, state.libraryPageSize);
  state.libraryPage = page.page;
  page.items.forEach((output) => {
      const card = el('article', 'asset-card');
      const category = outputCategory(output);
      const thumb = el('div', `asset-thumb ${category}`,
        `${humanStatus(output.kind)}\n${String(output.format || '').toUpperCase()}`);
      thumb.style.whiteSpace = 'pre-line';
      const body = el('div', 'asset-body');
      const meta = el('div', 'asset-meta');
      append(meta, badge(humanStatus(output.status), statusKind(output.status)),
        el('span', 'muted', relativeAge(output.updated_at)));
      append(body, meta, el('h3', null, output.title),
        el('p', null, `${output.evidence_ids?.length || 0} evidence item(s) · ${output.quality || 0}% quality`));
      const actions = el('div', 'actions');
      append(actions,
        button('Open', 'button compact', async () => {
          await selectWork(output.recipe_id);
          activateView('studio');
        }),
        button('Download', 'button ghost compact', () => downloadOutput(output)));
      body.append(actions);
      append(card, thumb, body);
      grid.append(card);
  });
  if (!grid.children.length) root.append(emptyState('No outputs match', 'Change the Library filter or create a work item.'));
  else {
    root.append(grid);
    root.append(pager(filtered.length, state.libraryPage, state.libraryPageSize, (nextPage) => {
      state.libraryPage = nextPage;
      renderLibrary().catch(showError);
    }, 'outputs'));
  }
}

function outputCategory(output) {
  if (['tutorial','adr','release_pack','handbook','quiz','notebook_pack'].includes(output.kind)) return 'document';
  if (['slides','diagram'].includes(output.kind)) return 'visual';
  if (output.kind === 'video_brief' || ['mp4','webm','gif'].includes(output.format)) return 'video';
  if (['eval_pack','retrieval_pack','sft_pack','preference_pack','privacy_manifest','provenance_manifest'].includes(output.kind)) return 'data';
  if (['skill','instruction_patch','agent_profile'].includes(output.kind)) return 'agent';
  return 'document';
}

async function renderCleanup() {
  if (!state.cleanup) {
    const params = new URLSearchParams({
      limit: String(state.cleanupPageSize),
      offset: String(state.cleanupPage * state.cleanupPageSize),
    });
    if (state.cleanupFilters.search) params.set('search', state.cleanupFilters.search);
    if (state.cleanupFilters.decision) params.set('decision', state.cleanupFilters.decision);
    state.cleanup = await get(`/api/cleanup-candidates?${params}`);
    const maxPage = Math.max(0, Math.ceil(Number(state.cleanup.total || 0) / state.cleanupPageSize) - 1);
    if (state.cleanupPage > maxPage) {
      state.cleanupPage = maxPage;
      state.cleanup = null;
      return renderCleanup();
    }
  }
  const root = clear($('#cleanup-content'));
  const candidates = state.cleanup.candidates || [];
  const counts = state.cleanup.counts || {};
  append(root, pageHead('Cleanup', 'Clear source data only when recovery proves it is safe.',
    'Eligibility is explainable and reversible. A newer session is useful evidence, never sufficient proof by itself.',
    [button('Refresh eligibility', 'button ghost', () => {
      state.cleanup = null;
      renderCleanup().catch(showError);
    })]));
  append(root, metricGrid([
    {label: 'Eligible now', value: counts.eligible || 0, copy: formatBytes(state.cleanup.eligible_bytes)},
    {label: 'Held', value: counts.held || 0, copy: 'missing recovery gates'},
    {label: 'Protected', value: counts.protected || 0, copy: 'live, current, or referenced'},
    {label: 'Reviewed outputs', value: state.cleanup.reviewed_outputs || 0, copy: 'supporting cleanup decisions'},
    {label: 'Candidates', value: (counts.eligible || 0) + (counts.held || 0) + (counts.protected || 0), copy: 'closed source transcripts'},
  ]));
  const layout = el('div', 'split-grid');
  layout.append(renderCleanupTable(candidates), renderCleanupPolicy());
  root.append(layout);
  $('#nav-cleanup-count').textContent = String(counts.eligible || 0);
}

function renderCleanupTable(candidates) {
  const panel = el('section', 'panel');
  const inner = el('div', 'panel-inner');
  inner.append(panelHead('Eligibility queue', 'Every recommendation includes its proof',
    'Inspect the complete recovery chain before any archive preview.',
    [badge('archive first', 'free')]));
  const search = textInput(state.cleanupFilters.search, 'Filter session, workspace, or source');
  const decision = selectInput([
    {value: '', label: 'All decisions'},
    {value: 'eligible', label: 'Eligible'},
    {value: 'held', label: 'Held'},
    {value: 'protected', label: 'Protected'},
  ], state.cleanupFilters.decision);
  const filters = el('div', 'filter-row');
  append(filters, search, decision);
  inner.append(filters);
  const wrap = el('div', 'table-wrap');
  const table = el('table');
  const head = el('thead');
  const row = el('tr');
  ['Session','Dormant','Evidence','Used by','Footprint','Decision',''].forEach((label) => row.append(el('th', null, label)));
  head.append(row);
  const body = el('tbody');
  candidates.forEach((candidate) => {
      const item = el('tr');
      const sessionCell = el('td');
      append(sessionCell, el('span', 'session-title', candidate.session.title || candidate.session.short),
        el('span', 'session-path', `${candidate.session.tool} · ${candidate.session.dir}`));
      append(item, sessionCell, el('td', null, `${candidate.dormant_days}d`),
        el('td', null, String(candidate.evidence)),
        el('td', null, `${candidate.reviewed_outputs}/${candidate.outputs}`),
        el('td', null, formatBytes(candidate.session.bytes)), el('td'), el('td'));
      item.children[5].append(badge(candidate.decision, statusKind(candidate.decision)));
      item.children[6].append(button('Inspect', 'button ghost compact', () => openCleanupCandidate(candidate)));
      body.append(item);
  });
  if (!body.children.length) {
    const empty = el('tr');
    const cell = el('td');
    cell.colSpan = 7;
    cell.append(emptyState('No cleanup candidates', 'Change the filters or recover more evidence.'));
    empty.append(cell);
    body.append(empty);
  }
  let searchTimer;
  search.addEventListener('input', () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(() => {
      state.cleanupFilters.search = search.value.trim();
      state.cleanupPage = 0;
      state.cleanup = null;
      renderCleanup().catch(showError);
    }, 220);
  });
  decision.addEventListener('change', () => {
    state.cleanupFilters.decision = decision.value;
    state.cleanupPage = 0;
    state.cleanup = null;
    renderCleanup().catch(showError);
  });
  append(table, head, body);
  wrap.append(table);
  append(inner, wrap, pager(Number(state.cleanup.total || 0), state.cleanupPage,
    state.cleanupPageSize, (nextPage) => {
      state.cleanupPage = nextPage;
      state.cleanup = null;
      renderCleanup().catch(showError);
    }, 'sessions'));
  panel.append(inner);
  return panel;
}

function renderCleanupPolicy() {
  const panel = el('aside', 'panel');
  const inner = el('div', 'panel-inner');
  append(inner, el('div', 'eyebrow', 'Default policy'),
    el('h2', null, 'Recovery before removal'));
  const list = el('div', 'gate-list');
  [
    ['Source fingerprint matches','The session has not changed since its successful assay.'],
    ['Evidence exists','Recovered claims retain exact source provenance.'],
    ['Outputs are reviewed','Owned results exist before source cleanup is recommended.'],
    ['No active references','No running job or open work item depends on the session.'],
    ['Archive before purge','Permanent removal remains a separate later decision.'],
  ].forEach(([title, copy]) => {
    const gate = el('div', 'gate');
    append(gate, el('span', 'gate-mark', 'OK'), append(el('div'), el('strong', null, title), el('span', null, copy)));
    list.append(gate);
  });
  append(inner, list);
  panel.append(inner);
  return panel;
}

function openCleanupCandidate(candidate) {
  const body = openDrawer(candidate.session.title || candidate.session.short);
  append(body, badge(candidate.decision, statusKind(candidate.decision)),
    el('p', 'muted', `${candidate.dormant_days} days dormant · ${candidate.newer_sessions} newer workspace session(s)`));
  const gates = el('div', 'gate-list');
  candidate.gates.forEach((item) => {
    const gate = el('div', `gate ${item.pass ? '' : 'failed'}`.trim());
    append(gate, el('span', 'gate-mark', item.pass ? 'OK' : '!'),
      append(el('div'), el('strong', null, humanStatus(item.key)), el('span', null, item.detail)));
    gates.append(gate);
  });
  body.append(gates);
  const actions = el('div', 'actions');
  if (candidate.decision === 'eligible') {
    actions.append(button('Preview reversible archive', 'button warning', () => {
      closeDrawer();
      previewArchive(candidate.session).catch(showError);
    }));
  } else {
    actions.append(button('Open related work', 'button', () => {
      closeDrawer();
      activateView(candidate.active_refs ? 'studio' : 'recover');
    }));
  }
  body.append(actions);
}

async function renderActivity() {
  const jobOffset = state.activityJobPage * state.activityJobPageSize;
  const recoveryOffset = state.activityRecoveryPage * state.activityRecoveryPageSize;
  const auditOffset = state.activityAuditPage * state.activityAuditPageSize;
  const [jobResponse, recoveryResponse, costData, auditResponse] = await Promise.all([
    get(`/api/jobs?limit=${state.activityJobPageSize}&offset=${jobOffset}`),
    get(`/api/recovery-runs?limit=${state.activityRecoveryPageSize}&offset=${recoveryOffset}`),
    get('/api/cost'),
    get(`/api/ops?limit=${state.activityAuditPageSize}&offset=${auditOffset}`),
  ]);
  state.activityJobs = Array.isArray(jobResponse) ? jobResponse : (jobResponse.items || []);
  state.jobsTotal = Number(Array.isArray(jobResponse) ? state.activityJobs.length : (jobResponse.total ?? state.activityJobs.length));
  state.recoveryRuns = Array.isArray(recoveryResponse) ? recoveryResponse : (recoveryResponse.items || []);
  state.recoveryRunsTotal = Number(Array.isArray(recoveryResponse) ? state.recoveryRuns.length : (recoveryResponse.total ?? state.recoveryRuns.length));
  state.activityOperations = Array.isArray(auditResponse) ? auditResponse : (auditResponse.items || []);
  state.activityOperationsTotal = Number(Array.isArray(auditResponse) ? state.activityOperations.length : (auditResponse.total ?? state.activityOperations.length));
  const operations = state.activityOperations;
  const root = clear($('#activity-content'));
  append(root, pageHead('Activity', 'Long work continues without owning the screen.',
    'Jobs are durable, navigable, and independent from the page that started them.',
    [button('Refresh now', 'button ghost', () => refreshActivity().catch(showError))]));
  append(root, metricGrid([
    {label: 'Active jobs', value: state.jobs.filter((job) => ['queued','running'].includes(job.status)).length, copy: 'currently executing'},
    {label: 'Recovery runs', value: state.recoveryRunsTotal, copy: 'durable scopes'},
    {label: 'Model runs', value: costData.totals?.runs || 0, copy: 'cost ledger'},
    {label: 'Audit events', value: state.activityOperationsTotal, copy: 'append-only records'},
    {label: 'Outputs', value: state.overview?.stats?.outputs || 0, copy: 'owned local files'},
  ]));
  const layout = el('div', 'content-grid');
  const jobsPanel = el('section', 'panel');
  const jobsInner = el('div', 'panel-inner');
  jobsInner.append(panelHead('Jobs', 'Recent and running work',
    'Inspect progress, completion, or failure without returning to the originating page.'));
  const list = el('div', 'job-list');
  state.activityJobs.forEach((job) => {
    const active = ['queued', 'running'].includes(job.status);
    const card = el('article', `job-card activity-job ${active ? 'is-active' : ''}`.trim());
    const copy = el('div');
    append(copy, el('div', 'job-kicker', active ? 'Live background work' : 'Recorded outcome'),
      el('h3', null, `${humanStatus(job.op)} � ${job.scope || 'all'}`),
      el('p', null, job.error || job.progress || 'Waiting'),
      el('span', 'job-timestamp', active ? `Started ${relativeAge(job.started)}` : `Finished ${relativeAge(job.ended)}`));
    append(card, copy, badge(humanStatus(job.status), statusKind(job.status)));
    if (active) {
      const progress = el('progress', 'job-progress');
      progress.max = 100;
      progress.removeAttribute('value');
      card.append(progress);
    }
    list.append(card);
  });
  if (!list.children.length) list.append(emptyState('No jobs yet', 'Start a mine, chat turn, or production.'));
  append(jobsInner, list, pager(state.jobsTotal, state.activityJobPage,
    state.activityJobPageSize, (nextPage) => {
      state.activityJobPage = nextPage;
      renderActivity().catch(showError);
    }, 'jobs'));
  jobsPanel.append(jobsInner);

  const right = el('div', 'stack');
  const costPanel = el('section', 'panel');
  const costInner = el('div', 'panel-inner');
  append(costInner, panelHead('Cost ledger', 'Current measured usage',
    'Real usage is read back from the selected CLI where available.'),
    metricGrid([
      {label: 'Items', value: costData.totals?.items || 0, copy: 'evidence and outputs'},
      {label: 'Tokens', value: formatCount(costData.totals?.tokens || 0), copy: 'total movement'},
    ]));
  costPanel.append(costInner);
    const recoveryPanel = el('section', 'panel');
    const recoveryInner = el('div', 'panel-inner');
    recoveryInner.append(panelHead('Recovery history', 'Durable mine and extraction runs',
      'Every scope remains inspectable after the job completes.'));
    const recoveryList = el('div', 'run-list');
    state.recoveryRuns.forEach((run) => {
      const row = el('article', 'run-row');
      append(row, append(el('div'), el('h3', null, `${humanStatus(run.op)} · ${run.sessions || 0} session(s)`),
        el('p', null, `${run.depth || 'assay'} · ${run.evidence || run.assayed || 0} item(s) · ${formatDate(run.started)}`)),
        badge(humanStatus(run.status), statusKind(run.status)));
      recoveryList.append(row);
    });
    if (!recoveryList.children.length) {
      recoveryList.append(emptyState('No recovery runs', 'Start a mine from Recover.'));
    }
    append(recoveryInner, recoveryList, pager(state.recoveryRunsTotal,
      state.activityRecoveryPage, state.activityRecoveryPageSize, (nextPage) => {
        state.activityRecoveryPage = nextPage;
        renderActivity().catch(showError);
      }, 'runs'));
    recoveryPanel.append(recoveryInner);
    const auditPanel = el('section', 'panel');
  const auditInner = el('div', 'panel-inner');
  auditInner.append(panelHead('Audit', 'Recent owner-visible mutations',
    'Reviews, exports, production, and cleanup actions remain traceable.'));
  const auditList = el('div', 'run-list');
  operations.forEach((operation) => {
    const row = el('article', 'run-row');
    append(row, append(el('div'), el('h3', null, humanStatus(operation.op)),
      el('p', null, operation.detail || operation.session_id || 'recorded')),
      badge(operation.ok ? 'complete' : 'failed', operation.ok ? 'free' : 'danger'));
    auditList.append(row);
  });
  append(auditInner, auditList, pager(state.activityOperationsTotal, state.activityAuditPage,
    state.activityAuditPageSize, (nextPage) => {
      state.activityAuditPage = nextPage;
      renderActivity().catch(showError);
    }, 'audit events'));
  auditPanel.append(auditInner);
  append(right, costPanel, recoveryPanel, auditPanel);
  append(layout, jobsPanel, right);
  root.append(layout);
}

async function refreshActivity() {
  await refreshJobs();
  if (state.activeView === 'activity') await renderActivity();
}

async function loadTools(force = false) {
  if (force || !state.connections) {
    const [connections, integrations, plugins] = await Promise.all([
      get('/api/refinery/connections'), get('/api/integrations'), get('/api/plugins'),
    ]);
    state.connections = connections || [];
    state.integrations = integrations || [];
    state.plugins = plugins || [];
  }
}

async function renderTools() {
  await loadTools();
  const root = clear($('#tools-content'));
  append(root, pageHead('Tools and settings',
    'Capabilities connect once, then appear where work needs them.',
    'Plugins, tools, skills, viewers, and destinations have separate responsibilities. Missing software is explained contextually.',
    [button('Check advanced manifests', 'button ghost', () => probePlugins())]));
  const taxonomy = el('div', 'taxonomy-grid');
  [
    ['Plugin','Connects a runtime or service and declares setup, permissions, commands, and health.'],
    ['Tool','One callable action such as render video, build diagram, or send notebook source.'],
    ['Skill','Instructions and workflow knowledge a persistent work session can use.'],
    ['Viewer','Renders Markdown, D2, JSONL, diffs, images, and video inside Studio.'],
    ['Destination','Receives an explicitly approved output after local review.'],
  ].forEach(([title, copy]) => {
    const card = el('article', 'taxonomy-card');
    append(card, el('strong', null, title), el('p', null, copy));
    taxonomy.append(card);
  });
  root.append(taxonomy);

  const managedByID = new Map(state.integrations.map((item) => [item.id, item]));
  const grid = el('div', 'tool-grid');
  const toolPage = pageSlice(state.connections, state.toolPage, state.toolPageSize);
  state.toolPage = toolPage.page;
  toolPage.items.forEach((connection) => {
    const card = el('article', 'tool-card');
    const logo = el('div', 'tool-logo', connection.name.split(/\s+/).map((part) => part[0]).join('').slice(0, 2).toUpperCase());
    const copy = el('div');
    append(copy, el('h3', null, connection.name), el('p', null, connection.description));
    const pills = el('div', 'pills');
    append(pills, badge(humanStatus(connection.status), statusKind(connection.status)),
      badge(connection.managed ? 'plugin' : 'tool'), badge(connection.cost, connection.cost === 'spends' ? 'spend' : ''));
    copy.append(pills);
    const managed = managedByID.get(connection.id);
    let action;
    if (managed) {
      action = button(managed.state === 'not_set_up' ? 'Set up' : 'Edit setup', 'button ghost compact',
        () => configureIntegration(managed));
    } else if (connection.status === 'ready') {
      action = button('Use in Studio', 'button ghost compact', () => activateView('studio'));
    } else {
      action = button('View setup', 'button ghost compact', () => toast(connection.detail));
    }
    append(card, logo, copy, action);
    grid.append(card);
  });
  append(root, grid, pager(state.connections.length, state.toolPage,
    state.toolPageSize, (nextPage) => {
      state.toolPage = nextPage;
      renderTools().catch(showError);
    }, 'capabilities'));

  const advanced = el('section', 'panel');
  const inner = el('div', 'panel-inner');
  inner.append(panelHead('Advanced manifests', 'Declarative adapters remain available',
    'Listing is passive. Probes happen only after an explicit request.'));
  if (!state.plugins.length) inner.append(emptyState('No advanced manifests', 'Managed settings cover the common integrations.'));
  else {
    const list = el('div', 'run-list');
    const pluginPage = pageSlice(state.plugins, state.pluginPage, state.pluginPageSize);
    state.pluginPage = pluginPage.page;
    pluginPage.items.forEach((plugin) => {
      const row = el('article', 'run-row');
      append(row, append(el('div'), el('h3', null, plugin.name), el('p', null, plugin.detail)),
        badge(humanStatus(plugin.status), statusKind(plugin.status)));
      list.append(row);
    });
    append(inner, list, pager(state.plugins.length, state.pluginPage,
      state.pluginPageSize, (nextPage) => {
        state.pluginPage = nextPage;
        renderTools().catch(showError);
      }, 'manifests'));
  }
  advanced.append(inner);
  root.append(advanced);
  $('#nav-tools-count').textContent = String(state.connections.length);
}

function configureIntegration(item) {
  if (item.id === 'open-notebook') return configureOpenNotebook(item);
  if (item.id === 'openmontage') return configureOpenMontage(item);
}

function configureOpenNotebook(item) {
  const body = openModal('Set up Open Notebook',
    'Midden stores loopback URLs and whether a password is required. The password itself is never stored.');
  const api = textInput(item.settings?.api_url || 'http://127.0.0.1:5055/api');
  const ui = textInput(item.settings?.ui_url || 'http://127.0.0.1:8502');
  const required = el('input');
  required.type = 'checkbox';
  required.checked = Boolean(item.settings?.password_required);
  const check = el('label', 'check-row');
  append(check, required, el('span', null, 'Password required for explicit send actions'));
  append(body, field('API URL', api), field('UI URL', ui), check);
  const actions = el('div', 'modal-actions');
  append(actions, button('Cancel', 'button ghost', closeModal),
    button('Save setup', 'button', async () => {
      await post('/api/integrations/configure', {
        id: item.id, enabled: true, api_url: api.value, ui_url: ui.value,
        password_required: required.checked, replace_legacy: item.legacy,
      });
      closeModal();
      state.connections = null;
      state.integrations = null;
      await renderTools();
      toast('Open Notebook setup saved.');
    }));
  body.append(actions);
}

function configureOpenMontage(item) {
  const body = openModal('Set up OpenMontage',
    'Midden records the installed repository and controlled AI CLI profile. It does not install dependencies.');
  const home = textInput(item.settings?.home || '', 'Absolute path to OpenMontage');
  const backend = selectInput([
    {value: 'copilot', label: 'Copilot CLI'}, {value: 'claude', label: 'Claude Code'},
    {value: 'opencode', label: 'OpenCode'},
  ], item.settings?.backend || 'copilot');
  append(body, field('OpenMontage home', home), field('Backend', backend));
  const actions = el('div', 'modal-actions');
  append(actions, button('Cancel', 'button ghost', closeModal),
    button('Save setup', 'button', async () => {
      await post('/api/integrations/configure', {
        id: item.id, enabled: true, home: home.value, backend: backend.value,
        replace_legacy: item.legacy,
      });
      closeModal();
      state.connections = null;
      state.integrations = null;
      await renderTools();
      toast('OpenMontage setup saved.');
    }));
  body.append(actions);
}

async function probePlugins() {
  try {
    state.plugins = await post('/api/plugins/probe', {});
    await renderTools();
    toast('Advanced manifests checked.');
  } catch (error) {
    showError(error);
  }
}

async function copyText(value) {
  try {
    await navigator.clipboard.writeText(value || '');
    toast('Copied to clipboard.');
  } catch {
    toast('Clipboard access failed.', 'bad');
  }
}

async function renderActiveView() {
  await loadOverview();
  switch (state.activeView) {
  case 'recover': return renderRecover();
  case 'studio': return renderStudio();
  case 'library': return renderLibrary();
  case 'cleanup': return renderCleanup();
  case 'activity': return renderActivity();
  case 'tools': return renderTools();
  }
}

configurePrimaryAction('recover');
Promise.all([loadOverview(true), refreshJobs()])
  .then(() => renderRecover())
  .catch((error) => {
    console.error(error);
    clear($('#recover-content')).append(emptyState('Midden could not load', error.message,
      button('Retry', 'button', () => location.reload())));
  });

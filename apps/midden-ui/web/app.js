import sessionsView from "/js/sessions.js";
import evidenceView from "/js/evidence.js";
import sourcesView from "/js/sources.js";
import modelsView from "/js/models.js";
import { quoteCheck } from "/js/quote.js";

const $ = id => document.getElementById(id);
const ROUTES = ["sessions", "evidence", "sources", "files", "assistant", "models"];
const NAV = { evidence: "sessions" };
const views = { sessions: sessionsView, evidence: evidenceView, sources: sourcesView, models: modelsView };
const route = { name: "", params: [] };
const listeners = new Map();
const bus = {
  on(name, fn) { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn); return () => listeners.get(name).delete(fn); },
  emit(name, data) { for (const fn of listeners.get(name) || []) { try { fn(data); } catch (reason) { console.error(reason); } } },
};
const state = { csrf: "", current: null, sessions: [], files: [], cache: new Map(), active: null,
  completed: new Set(), seq: 0, revision: 0, connected: false, sending: false, creating: false,
  canceling: false, interrupted: false, source: null, selectedFile: null, fileRequest: 0, filesRequest: 0, statusRequest: 0,
  workspaceId: "", scope: 0, booting: true, selectionMade: false, modelConfigured: false, modelSetupError: "", drafts: new Map(), visiblePermission: null,
  status: null, context: [], contextOpen: false };
const entry = (id = state.current) => {
  if (!state.cache.has(id)) state.cache.set(id, { messages: [], outcomes: [], activity: [], live: null, loaded: false, loading: false, request: 0 });
  return state.cache.get(id);
};
function node(tag, className = "", text = "") {
  const element = document.createElement(tag);
  element.className = className;
  element.textContent = text;
  if (className === "error") element.setAttribute("role", "alert");
  return element;
}
function error(message, target = "notice") {
  $(target === "notice" ? "noticeText" : target).textContent = message instanceof Error ? message.message : String(message);
  $(target).hidden = false;
  if (target === "filesError" && $("previewDialog").open) error(message, "previewError");
}
const clearError = target => { $(target).hidden = true; if (target === "filesError") $("previewError").hidden = true; };
const run = (action, target = "notice") => action().catch(reason => error(reason, target));
const list = (data, key) => {
  if (!Array.isArray(data?.[key])) throw new Error(`Host returned an invalid ${key} list.`);
  return data[key];
};
function draft(id = state.current, workspace = state.workspaceId) {
  const key = `midden:draft:v1:${encodeURIComponent(workspace)}:${id === null ? "new" : `session:${encodeURIComponent(id)}`}`;
  if (!state.drafts.has(key)) {
    const record = { key, text: "", dirty: false, loadFailed: false, issue: "" };
    try {
      if (!workspace) throw new Error("Workspace identity unavailable");
      record.text = localStorage.getItem(key) || "";
    } catch (reason) { record.loadFailed = true; record.issue = reason.name; }
    state.drafts.set(key, record);
  }
  return state.drafts.get(key);
}
function persistDraft(record) {
  try {
    if (record.loadFailed) throw new Error("Draft storage could not be read");
    if (record.clearValue !== undefined) {
      const stored = localStorage.getItem(record.key);
      if (stored === record.clearValue) localStorage.removeItem(record.key);
      else if (stored) record.text = stored;
      delete record.clearValue;
    } else if (record.text) localStorage.setItem(record.key, record.text);
    else localStorage.removeItem(record.key);
    record.dirty = false; record.issue = "";
  } catch (reason) { record.dirty = true; record.issue = reason.name; }
  renderDraftStatus();
}
function setDraft(record, text) {
  record.text = text; record.dirty = true; delete record.clearValue;
  persistDraft(record);
}
function clearMatchingDraft(record, text) {
  if (record.text !== text) return;
  record.text = ""; record.clearValue = text; record.dirty = true;
  persistDraft(record);
}
function renderDraftStatus() {
  if (!state.workspaceId) return;
  const current = draft(), issue = [...state.drafts.values()].find(record => record.issue || record.dirty);
  $("draftState").hidden = !current.text && !issue;
  $("draftStatus").textContent = issue ? `Draft not saved (${issue.issue || "storage unavailable"}). Keep this tab open and retry.` : "Draft saved on this device.";
  $("draftStatus").classList.toggle("storage-error", !!issue);
  $("retryDrafts").hidden = !issue;
}
function retryDrafts() {
  for (const record of state.drafts.values()) {
    if (!record.dirty && !record.issue) continue;
    if (record.loadFailed) {
      try {
        const stored = localStorage.getItem(record.key);
        if (record.dirty && record.text && stored && stored !== record.text) {
          record.issue = "another draft is saved; copy this text before reloading"; continue;
        }
        if (!record.dirty) record.text = stored || "";
        record.loadFailed = false;
      } catch (reason) { record.issue = reason.name; continue; }
    }
    persistDraft(record);
  }
  $("message").value = draft().text; controls();
}
function bindWorkspace(id) {
  if (state.workspaceId === id) return false;
  const rebinding = !!state.workspaceId;
  state.scope++; state.workspaceId = id; state.booting = true; state.selectionMade = false;
  state.source?.close(); state.source = null; state.connected = false; state.interrupted = false;
  state.current = null; state.cache = new Map(); state.sessions = []; state.files = [];
  state.active = null; state.completed.clear(); state.seq = 0; state.revision++;
  state.sending = false; state.creating = false; state.canceling = false; state.visiblePermission = null;
  state.fileRequest++; state.filesRequest++; state.selectedFile = null; state.context = []; renderContext();
  $("previewDialog").close(); $("fileFrame").removeAttribute("src");
  $("filePreview").hidden = true; $("fileEmpty").hidden = false; clearError("notice");
  $("message").value = draft(null).text;
  if (rebinding) bus.emit("workspace-changed", id);
  return true;
}
async function request(path, method = "GET", body, signal) {
  if (method !== "GET" && !state.csrf) throw new Error("Host has not supplied a CSRF token. Refresh the host before trying again.");
  const headers = method === "GET" ? {} : { "X-Midden-CSRF": state.csrf };
  if (body !== undefined) headers["Content-Type"] = "application/json";
  const response = await fetch(path, { method, headers, credentials: "same-origin", cache: "no-store",
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: signal ? AbortSignal.any([signal, AbortSignal.timeout(30000)]) : AbortSignal.timeout(30000) });
  if (!response.ok) {
    let failure;
    try { failure = await response.json(); } catch { throw new Error(`HTTP ${response.status}: host returned a non-JSON error.`); }
    throw new Error(typeof failure.error === "string" ? failure.error : `HTTP ${response.status}: request failed.`);
  }
  return response;
}
async function api(path, method = "GET", body, signal) {
  const response = await request(path, method, body, signal);
  if (response.status === 204) return null;
  try { return await response.json(); } catch { throw new Error(`Invalid JSON response from ${path}.`); }
}
const date = value => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : "Date not reported";
const size = value => Number.isFinite(value) ? (value < 1024 ? `${value} B` : value < 1048576 ? `${(value / 1024).toFixed(1)} KB` : `${(value / 1048576).toFixed(1)} MB`) : "Size not reported";
const args = value => typeof value === "string" ? value : JSON.stringify(value ?? {}, null, 2);
const pending = item => item.activity.filter(event => event.type === "permission" && !event.result);
function active(raw, sessionId) {
  const turnId = typeof raw === "string" ? raw : raw?.turnId || raw?.id;
  return turnId && !state.completed.has(turnId) ? { turnId, sessionId: raw?.sessionId || sessionId } : null;
}
function controls() {
  const item = entry();
  $("message").disabled = !state.csrf || state.booting || state.creating || (!!state.current && !item.loaded);
  $("message").readOnly = state.sending;
  $("send").disabled = $("message").disabled || !state.modelConfigured || !state.connected || state.sending || item.loading || !!item.error || !!state.active || !$("message").value.trim();
  $("newSession").disabled = !state.csrf || state.booting || !state.modelConfigured || state.creating || state.sending;
  $("stop").hidden = !state.active;
  $("stop").disabled = state.canceling || !state.csrf;
  $("turnStatus").textContent = state.active ? (state.canceling ? "Stopping..." : pending(entry(state.active.sessionId)).length ? "Permission needed" :
    state.active.sessionId && state.active.sessionId !== state.current ? "Running in another conversation" : "Working...") :
    state.booting ? "Loading workspace..." : state.creating ? "Creating conversation..." : state.sending ? "Starting turn..." : item.loading ? "Loading conversation..." : item.error ? "Conversation unavailable" : state.modelSetupError ? "Model setup needed" : !state.modelConfigured ? "Set up model to send" : state.csrf ? "Ready" : "Host not loaded";
  if (state.interrupted) $("turnStatus").textContent = state.source?.readyState === EventSource.CLOSED ? "Live updates stopped. Refresh host to reconnect." :
    `Live updates interrupted. ${state.active ? "Stop is available." : "Reconnecting..."}`;
  $("activeSession").hidden = !state.active?.sessionId || state.active.sessionId === state.current;
  $("setupModel").hidden = !state.csrf || state.modelConfigured;
  document.body.classList.toggle("has-permissions", pending(item).length > 0);
  renderDraftStatus();
}
function renderModel(model) {
  state.modelSetupError = typeof model.setupError === "string" ? model.setupError.trim() : "";
  state.modelConfigured = model.configured === true && !state.modelSetupError;
  const chip = $("modelChip");
  chip.textContent = state.modelConfigured ? (model.summary || model.defaultModel || "Model selected") :
    state.modelSetupError ? "Model setup needed" : "No model connected";
  chip.title = state.modelConfigured ? `Model: ${chip.textContent}` : "Connect a model in Models";
  chip.classList.toggle("ok", state.modelConfigured);
  chip.classList.toggle("warn", !state.modelConfigured);
  $("modelSetupError").textContent = state.modelSetupError;
  $("modelSetupNotice").hidden = !state.modelSetupError;
}
async function loadStatus() {
  const version = ++state.statusRequest, revision = state.revision;
  const data = await api("/api/status");
  if (version !== state.statusRequest) return;
  if (typeof data?.csrfToken !== "string" || !data.csrfToken || !data.model || typeof data.workspaceId !== "string" || !data.workspaceId) throw new Error("Incomplete host status. A stable workspace ID, model status and CSRF token are required.");
  const bundles = list(data, "bundles");
  const changed = bindWorkspace(data.workspaceId);
  if (state.csrf && state.csrf !== data.csrfToken) { state.seq = 0; state.completed.clear(); }
  state.csrf = data.csrfToken;
  state.status = data;
  $("workspace").textContent = data.workspace;
  $("workspace").title = data.workspace;
  const coreVersion = String(data.coreVersion || "").replace(/^midden\s+/i, "");
  $("versions").textContent = `Midden ${data.uiVersion || "(version not reported)"} · powered by ${data.kernelName || "Compa"} ${data.kernelVersion || ""} · core ${coreVersion || "not reported"}`;
  $("hostNotice").textContent = typeof data.notice === "string" ? data.notice : "";
  $("hostNotice").hidden = !$("hostNotice").textContent;
  renderModel(data.model);
  $("bundles").replaceChildren(...bundles.map(bundle => {
    const li = node("li"); li.append(node("strong", "", bundle.name), node("p", "quiet", bundle.description)); return li;
  }));
  if (!bundles.length) $("bundles").append(node("li", "quiet", "No outcome guidance reported by the host."));
  if (changed || revision === state.revision) {
    const next = active(data.activeTurn, state.active?.sessionId);
    if (next?.turnId !== state.active?.turnId) state.canceling = false;
    state.active = next;
    for (const item of state.cache.values()) {
      pending(item).filter(event => event.turnId !== next?.turnId).forEach(event => { event.result = "No longer pending"; });
    }
    renderActivity(); renderSessions();
  }
  controls();
  bus.emit("status", data);
  return changed;
}
function renderSessions() {
  const focused = document.activeElement?.dataset.session;
  $("sessionCount").textContent = state.sessions.length ? String(state.sessions.length) : "";
  $("sessionList").replaceChildren(...state.sessions.map(session => {
    const button = node("button"); button.type = "button"; button.dataset.session = session.id; button.setAttribute("aria-current", String(session.id === state.current));
    const needs = pending(entry(session.id)).length;
    button.append(node("strong", "", session.title || "Untitled conversation"),
      node("small", "", needs ? `${needs} permission decision${needs === 1 ? "" : "s"} needed` : date(session.updated)));
    button.addEventListener("click", () => run(async () => { await choose(session.id); if (state.current === session.id) $("message").focus(); }));
    return button;
  }));
  if (!state.sessions.length) $("sessionList").append(node("p", "quiet", "No conversations yet. Start a new one."));
  if (focused) [...$("sessionList").children].find(button => button.dataset.session === focused)?.focus({ preventScroll: true });
  $("sessionTitle").textContent = state.sessions.find(session => session.id === state.current)?.title || "New conversation";
  $("sessionTitle").title = $("sessionTitle").textContent;
}
async function loadSessions() {
  const scope = state.scope;
  $("refreshSessions").disabled = true;
  try {
    const sessions = list(await api("/api/sessions"), "sessions");
    if (scope !== state.scope) return;
    state.sessions = sessions; renderSessions();
  }
  catch (reason) {
    if (scope !== state.scope) return;
    if (!state.sessions.length) $("sessionList").replaceChildren(node("p", "quiet", "Conversations unavailable. Refresh to retry."));
    throw reason;
  } finally { if (scope === state.scope) $("refreshSessions").disabled = false; }
}
function message(role, content, at) {
  const article = node("article", `message ${role === "user" ? "user" : "assistant"}`);
  const header = node("header");
  header.append(node("strong", "", role === "user" ? "You" : role === "assistant" ? "Midden" : role), node("span", "", at ? date(at) : ""));
  article.append(header, node("pre", "", content));
  return article;
}
const nearBottom = () => $("chatScroll").scrollHeight - $("chatScroll").scrollTop - $("chatScroll").clientHeight < 90;
const scrollBottom = () => { $("chatScroll").scrollTop = $("chatScroll").scrollHeight; };
function renderLive() {
  const item = entry(), live = item.live, last = item.messages.at(-1), pinned = nearBottom();
  let element = $("liveMessage");
  if (!live?.text || (last?.role === "assistant" && last.content === live.text)) { element?.remove(); return; }
  if (!element) { element = message("assistant", "", ""); element.id = "liveMessage"; $("messages").append(element); }
  element.querySelector("pre").textContent = live.text;
  $("welcome").hidden = true;
  if (pinned) scrollBottom();
}
function renderChat() {
  const item = entry(), pinned = nearBottom(), hasMessages = !!item.messages.length || !!item.live?.text;
  $("messages").replaceChildren(...item.messages.map((record, index) => {
    const article = message(record.role, record.content, record.at);
    for (const outcome of item.outcomes.filter(value => value.messageIndex === index && value.status !== "completed")) {
      const evidence = node("aside", "turn-outcome"); evidence.setAttribute("role", "note");
      evidence.dataset.status = outcome.status;
      evidence.append(node("strong", "", `Host status: ${outcome.status[0].toUpperCase()}${outcome.status.slice(1)}`), node("small", "", date(outcome.at)));
      if (outcome.error) evidence.append(node("p", "", outcome.error));
      article.append(evidence);
    }
    return article;
  }));
  const uncoveredError = item.turnError && !item.outcomes.some(outcome => outcome.turnId === item.turnError.turnId);
  $("sessionError").hidden = !uncoveredError;
  $("sessionError").textContent = uncoveredError ? `Host turn error: ${item.turnError.text}` : "";
  $("welcome").hidden = hasMessages || item.loading || !!item.error;
  $("sessionLoading").hidden = item.loaded || (!item.loading && !item.error);
  $("sessionLoading").textContent = item.error || "Loading conversation...";
  renderLive();
  if (!hasMessages) $("chatScroll").scrollTop = 0;
  else if (pinned) scrollBottom();
  controls();
}
async function loadSession(id) {
  const item = entry(id), version = ++item.request, revision = state.revision, scope = state.scope;
  item.loading = true; item.error = "";
  if (id === state.current) renderChat();
  try {
    const data = await api(`/api/sessions/${encodeURIComponent(id)}`);
    if (version !== item.request || scope !== state.scope) return;
    const messages = list(data, "messages"), outcomes = data.outcomes === undefined ? [] : list(data, "outcomes");
    if (outcomes.some(outcome => !["running", "completed", "failed", "cancelled", "interrupted"].includes(outcome.status) ||
        !Number.isInteger(outcome.messageIndex) || messages[outcome.messageIndex]?.role !== "user" || !outcome.turnId)) throw new Error("Host returned an invalid turn outcome.");
    item.messages = messages; item.outcomes = outcomes; item.loaded = true;
    outcomes.filter(outcome => outcome.status !== "running").forEach(outcome => state.completed.add(outcome.turnId));
    if (state.active && state.completed.has(state.active.turnId)) { state.active = null; state.canceling = false; }
    if (data.activeTurn && revision === state.revision) state.active = active(data.activeTurn, id);
  } catch (reason) {
    if (scope !== state.scope) return;
    if (version === item.request) item.error = reason.message;
    throw reason;
  } finally {
    if (version === item.request) item.loading = false;
    if (scope === state.scope && id === state.current) {
      state.visiblePermission = null; renderChat(); renderActivity();
    }
  }
}
async function choose(id, load = true) {
  state.current = id; state.selectionMade = true; state.visiblePermission = null;
  if (route.name === "assistant") history.replaceState(null, "", id === null ? "#/assistant" : `#/assistant/${encodeURIComponent(id)}`);
  $("message").value = draft(id).text;
  renderSessions(); renderChat(); renderActivity();
  if (load && id !== null) await loadSession(id);
}
async function createSession() {
  const scope = state.scope;
  const data = await api("/api/sessions", "POST", {});
  if (scope !== state.scope) return null;
  if (!data?.id) throw new Error("The host did not return a conversation ID.");
  if (!state.sessions.some(session => session.id === data.id)) state.sessions.unshift(data);
  entry(data.id).loaded = true;
  return data.id;
}
async function newSession() {
  if ($("newSession").disabled) return;
  const scope = state.scope;
  state.creating = true; controls(); clearError("notice");
  try { const id = await createSession(); if (scope === state.scope) await choose(id, false); }
  finally { if (scope === state.scope) { state.creating = false; controls(); } }
  if (scope === state.scope) $("message").focus();
}
function contextBlock() {
  if (!state.context.length) return "";
  return ["Context selected in Midden:", ...state.context.map(chip => `- ${chip.text}`)].join("\n");
}
function renderContext() {
  const chips = $("contextChips");
  chips.replaceChildren(...state.context.map((chip, index) => {
    const element = node("span", "chip ok"), remove = node("button", "chip-remove", "✕");
    element.append(node("span", "", chip.label));
    remove.type = "button"; remove.setAttribute("aria-label", `Remove ${chip.label} from the next message`);
    remove.addEventListener("click", () => { state.context.splice(index, 1); renderContext(); $("message").focus(); });
    element.append(remove);
    return element;
  }));
  $("contextTray").hidden = !state.context.length;
  $("contextPreview").textContent = contextBlock();
  $("contextPreview").hidden = !state.contextOpen || !state.context.length;
  $("contextPreviewToggle").setAttribute("aria-expanded", String(state.contextOpen && !!state.context.length));
}
function addContext(chip) {
  if (!chip || typeof chip.label !== "string" || typeof chip.text !== "string" || !chip.text.trim()) return;
  if (!state.context.some(existing => existing.text === chip.text)) state.context.push({ kind: chip.kind || "context", label: chip.label, text: chip.text });
  renderContext();
}
async function send() {
  if ($("send").disabled) return;
  const typed = $("message").value, block = contextBlock(), scope = state.scope, workspace = state.workspaceId;
  const text = block ? `${typed}\n\n${block}` : typed;
  const sourceDraft = state.current === null ? draft(null, workspace) : null;
  state.sending = true; controls(); clearError("notice");
  try {
    let id = state.current;
    if (!id) {
      id = await createSession();
      if (scope !== state.scope) return;
      const moved = draft(id, workspace); setDraft(moved, typed);
      if (!moved.dirty) clearMatchingDraft(draft(null, workspace), typed);
      await choose(id, false);
    }
    const item = entry(id), before = item.messages.length, savedDraft = draft(id, workspace);
    const data = await api(`/api/sessions/${encodeURIComponent(id)}/turn`, "POST", { message: text });
    if (!data?.turnId) throw new Error("The host did not return a turn ID. Refresh before retrying to avoid a duplicate turn.");
    clearMatchingDraft(savedDraft, typed);
    if (sourceDraft) clearMatchingDraft(sourceDraft, typed);
    if (scope !== state.scope) return;
    if (block) { state.context = []; state.contextOpen = false; renderContext(); }
    // SSE may finish the turn and refresh history before the HTTP acknowledgement.
    if (!item.messages.slice(before).some(record => record.role === "user" && record.content === text)) item.messages.push({ role: "user", content: text });
    if (item.live?.turnId !== data.turnId) item.live = null;
    if (!state.completed.has(data.turnId)) { state.active = { turnId: data.turnId, sessionId: id }; state.revision++; }
    if (state.current === id) { $("message").value = savedDraft.text; renderChat(); scrollBottom(); $("message").focus(); }
    run(() => loadSession(id));
  } finally { if (scope === state.scope) { state.sending = false; controls(); } }
}
function toolSummary(event) {
  const tool = event.tool || "Tool", status = event.status || event.phase || "activity";
  if (tool !== "midden" || !Array.isArray(event.arguments?.args)) return `${tool} - ${status}`;
  return `midden ${event.arguments.args.slice(0, 2).join(" ")} - ${status}${event.effect ? ` (${event.effect})` : ""}`;
}
function toolResult(event) {
  const holder = node("div", "tool-result");
  let data = null;
  if (event.tool === "midden" && typeof event.result === "string") { try { data = JSON.parse(event.result); } catch { data = null; } }
  if (data && Array.isArray(data.sessions)) {
    holder.append(node("p", "quiet", `${data.sessions.length} session${data.sessions.length === 1 ? "" : "s"} listed`));
    const ul = node("ul", "result-list");
    data.sessions.slice(0, 8).forEach(session => { ul.append(node("li", "", `${session.tool} · ${session.title || "Untitled"} · ${date(session.updated)}`)); });
    holder.append(ul);
    const open = node("button", "compact", "Open in Sessions"); open.type = "button";
    open.addEventListener("click", () => { location.hash = "#/sessions"; });
    holder.append(open);
  } else if (data && Array.isArray(data.records)) {
    holder.append(node("p", "quiet", `${data.records.length} record${data.records.length === 1 ? "" : "s"}${data.matched_records !== undefined ? ` of ${data.matched_records} matched` : ""}`));
    const ul = node("ul", "result-list");
    data.records.slice(0, 6).forEach(record => { ul.append(node("li", "", `#${record.reference?.record_index ?? "?"} ${record.role || record.kind || ""}: ${String(record.text || "").slice(0, 200)}`)); });
    holder.append(ul);
    if (data.view_id) {
      const open = node("button", "compact", "Open in Evidence"); open.type = "button";
      open.addEventListener("click", () => { location.hash = `#/evidence/${encodeURIComponent(data.view_id)}`; });
      holder.append(open);
    }
  } else if (data && typeof data.path === "string" && Number.isFinite(data.record_count)) {
    holder.append(node("p", "quiet", `Collection ${data.path}: ${data.record_count} records`));
    const open = node("button", "compact", "Open in Sources"); open.type = "button";
    open.addEventListener("click", () => { location.hash = "#/sources"; });
    holder.append(open);
  } else if (event.result) {
    holder.append(node("pre", "", event.result));
  }
  if (event.resultTruncated) holder.append(node("p", "quiet", "Result shortened for display."));
  return holder;
}
function renderActivity() {
  const item = entry(), outstanding = pending(item);
  const focused = document.activeElement?.dataset.activityFocus;
  const urgent = node("div", "pending-permissions"), previous = node("details", "activity-history");
  const historySummary = node("summary", "", `Previous activity (${item.activity.length - outstanding.length})`);
  historySummary.dataset.activityFocus = "history";
  previous.open = !!item.historyOpen;
  previous.addEventListener("toggle", () => { if (previous.isConnected) item.historyOpen = previous.open; });
  previous.append(historySummary);
  $("activity").hidden = !item.activity.length;
  if (outstanding.length) $("activity").open = true;
  $("activitySummary").textContent = `Tool activity (${item.activity.length})${pending(item).length ? " - permission needed" : ""}`;
  for (const event of item.activity) {
    if (event.type === "error") { previous.append(node("p", "error", event.error || event.text || "Turn failed.")); continue; }
    if (event.type !== "permission") {
      const details = node("details", "tool"), summary = node("summary", "", toolSummary(event));
      summary.dataset.activityFocus = JSON.stringify(["tool", event.seq]);
      details.open = !!event.open;
      details.addEventListener("toggle", () => { if (details.isConnected) event.open = details.open; });
      details.append(summary, node("pre", "", args(event.arguments)));
      if (event.result || event.text) details.append(event.result ? toolResult(event) : node("pre", "", event.text));
      previous.append(details); continue;
    }
    const card = node("section", "permission"), actions = node("div", "permission-actions");
    card.dataset.permissionId = event.permissionId;
    card.setAttribute("aria-label", `Permission for ${event.tool}`);
    const inspect = node("details", "permission-arguments"), inspectSummary = node("summary", "", "Inspect arguments");
    inspectSummary.dataset.activityFocus = JSON.stringify([event.permissionId, "arguments"]);
    inspect.open = !!event.argumentsOpen;
    inspect.append(inspectSummary, node("pre", "", args(event.arguments)));
    inspect.addEventListener("toggle", () => { if (inspect.isConnected) event.argumentsOpen = inspect.open; });
    card.append(node("strong", "", `${event.tool} requests permission`), inspect);
    for (const allow of [true, false]) {
      const button = node("button", allow ? "primary" : "", allow ? "Allow" : "Deny");
      button.dataset.activityFocus = JSON.stringify([event.permissionId, allow]);
      button.type = "button"; button.disabled = !!event.result || !!event.busy;
      button.addEventListener("click", () => run(() => decide(event, allow)));
      actions.append(button);
    }
    const status = node("p", "", event.result || (event.busy ? "Sending decision..." : "Only this action will be authorized."));
    status.setAttribute("role", "status"); actions.append(status); card.append(actions);
    if (event.error) card.append(node("p", "error", event.error));
    (event.result ? previous : urgent).append(card);
  }
  previous.hidden = previous.children.length === 1;
  $("activityList").replaceChildren(urgent, previous);
  if (focused) {
    const restored = [...$("activityList").querySelectorAll("[data-activity-focus]")].find(element => element.dataset.activityFocus === focused);
    if (restored && !restored.disabled) restored.focus({ preventScroll: true });
  }
  controls();
  const first = outstanding[0]?.permissionId, sessionId = state.current;
  if (!first) { state.visiblePermission = null; return; }
  requestAnimationFrame(() => {
    if (state.current !== sessionId || !item.loaded || item.loading || !$("chatScroll").clientHeight || !urgent.isConnected) return;
    const focusedCard = document.activeElement?.closest(".pending-permissions .permission");
    if (previous.contains(document.activeElement) || (state.visiblePermission === first && !focusedCard)) return;
    const card = focusedCard || urgent.firstElementChild;
    const target = card.offsetHeight <= $("chatScroll").clientHeight ? card : card.querySelector(".permission-actions");
    target.scrollIntoView({ block: "nearest" });
    state.visiblePermission = first;
  });
}
async function decide(event, allow) {
  if (event.busy || event.result) return;
  event.busy = true; event.error = ""; renderActivity();
  try {
    await api(`/api/permissions/${encodeURIComponent(event.permissionId)}`, "POST", { allow });
    event.result = allow ? "Allowed" : "Denied";
  } catch (reason) { event.error = reason.message; }
  finally {
    event.busy = false; renderActivity(); renderSessions(); controls();
    if (event.sessionId === state.current) ($("activityList").querySelector("button:not(:disabled)") || $("message")).focus();
  }
}
async function stop() {
  if (!state.active || state.canceling) return;
  const turnId = state.active.turnId;
  state.canceling = true; controls(); clearError("notice");
  try { await api("/api/cancel", "POST", { turnId }); }
  catch (reason) { state.canceling = false; controls(); throw reason; }
}
function receive(event) {
  if (!Number.isFinite(event.seq) || typeof event.type !== "string") throw new Error("Malformed live event. Refresh the host to resynchronize.");
  if (event.seq <= state.seq) return;
  state.seq = event.seq;
  if (event.type === "status") { run(async () => { if (await loadStatus()) await refreshWorkspace(); }); return; }
  if (event.type === "files_changed") { run(loadFiles, "filesError"); bus.emit("files-changed"); return; }
  if (state.completed.has(event.turnId) && event.type !== "permission_result") return;
  if (!event.sessionId || !event.turnId) {
    if (event.type === "error") { error(event.error || event.text || "Host error."); return; }
    throw new Error("Live turn event is missing its conversation or turn ID.");
  }
  const item = entry(event.sessionId);
  const otherTurn = state.active && state.active.turnId !== event.turnId;
  // Historical replays must not replace the host-reported active turn.
  if (otherTurn && ["delta", "message", "permission"].includes(event.type)) return;
  if (!otherTurn && ["delta", "message", "tool", "permission"].includes(event.type)) {
    if (item.live && item.live.turnId !== event.turnId) { item.live = null; if (event.sessionId === state.current) renderLive(); }
    state.active = { turnId: event.turnId, sessionId: event.sessionId }; state.revision++;
  }
  if (event.type === "delta" || event.type === "message") {
    if (typeof event.text !== "string") throw new Error("Live message is missing its text.");
    item.live = { turnId: event.turnId, text: event.text };
    if (event.sessionId === state.current) renderLive();
    if (event.type === "message") $("announcement").textContent = "Assistant response received.";
  } else if (event.type === "tool") {
    const earlier = event.callId && event.status !== "pending" ? item.activity.find(record => record.type === "tool" && record.callId === event.callId && record.status === "pending") : null;
    if (earlier) Object.assign(earlier, event); else item.activity.push(event);
  } else if (event.type === "permission") {
    if (!event.permissionId) throw new Error("Permission event is missing its ID.");
    if (!item.activity.some(record => record.permissionId === event.permissionId)) item.activity.push(event);
    if (event.sessionId === state.current) $("activity").open = true;
    $("announcement").textContent = "A tool needs your permission.";
  } else if (event.type === "permission_result") {
    const permission = item.activity.find(record => record.permissionId === event.permissionId);
    if (permission) permission.result = event.allow === true ? "Allowed" : event.allow === false ? "Denied" : "Decision recorded";
  } else if (event.type === "turn_done" || event.type === "error") {
    state.completed.add(event.turnId);
    if (state.active?.turnId === event.turnId) { state.revision++; state.active = null; state.canceling = false; }
    pending(item).filter(record => record.turnId === event.turnId).forEach(record => { record.result = "No longer pending"; });
    if (event.type === "error") {
      item.activity.push(event);
      item.turnError = { turnId: event.turnId, text: event.error || event.text || "Turn failed." };
    }
    if (event.sessionId === state.current) $("announcement").textContent = event.type === "error" ? "Turn failed." : "Turn finished.";
    run(() => loadSession(event.sessionId)); run(loadSessions); run(loadFiles, "filesError");
  }
  if (!["delta", "message"].includes(event.type) && event.sessionId === state.current) renderActivity();
  if (!["delta", "message", "tool"].includes(event.type)) renderSessions();
  controls();
}
function connect() {
  if (state.source && state.source.readyState !== EventSource.CLOSED) return;
  state.source?.close();
  const source = state.source = new EventSource("/api/events"), scope = state.scope;
  let opened = false;
  source.onopen = () => {
    if (scope !== state.scope) return;
    state.connected = true; state.interrupted = false; $("connection").textContent = "Live updates connected"; $("connection").classList.add("connected"); controls();
    if (opened) run(refresh);
    opened = true;
  };
  source.onerror = () => {
    if (scope !== state.scope) return;
    state.connected = false; state.interrupted = true; $("connection").textContent = "Live updates interrupted. Reconnecting; Stop is still available.";
    if (source.readyState === EventSource.CLOSED) {
      $("connection").textContent = "Live event stream closed. Refresh host to reconnect.";
      error($("connection").textContent);
    }
    $("connection").classList.remove("connected"); controls();
  };
  source.onmessage = message => {
    if (scope !== state.scope) return;
    try { receive(JSON.parse(message.data)); } catch (reason) { error(`Live update error: ${reason.message}`); }
  };
}
function renderFiles() {
  $("fileCount").textContent = String(state.files.length);
  $("fileList").replaceChildren(...state.files.map(file => {
    const li = node("li"), button = node("button"); button.type = "button";
    button.setAttribute("aria-current", String(file.path === state.selectedFile));
    button.append(node("strong", "", file.path), node("small", "", `${size(file.size)} - ${file.kind || "file"}`));
    button.addEventListener("click", () => run(() => selectFile(file.path), "filesError")); li.append(button); return li;
  }));
  if (!state.files.length) $("fileList").append(node("li", "quiet", "No workspace files yet."));
}
async function loadFiles() {
  const version = ++state.filesRequest, scope = state.scope;
  $("refreshFiles").disabled = true; clearError("filesError");
  try {
    const files = list(await api("/api/files"), "files");
    if (version !== state.filesRequest) return;
    state.files = files; renderFiles();
    if (state.selectedFile && files.some(file => file.path === state.selectedFile)) await selectFile(state.selectedFile);
    else if (state.selectedFile) {
      state.selectedFile = null; state.fileRequest++; $("filePreview").hidden = true; $("fileEmpty").hidden = false;
      $("previewDialog").close();
      $("fileFrame").removeAttribute("src"); throw new Error("The selected file is no longer listed in the workspace.");
    }
  } catch (reason) {
    if (scope !== state.scope) return;
    if (!state.files.length) $("fileList").replaceChildren(node("li", "quiet", "Files unavailable. Refresh to retry."));
    throw reason;
  } finally { if (version === state.filesRequest) $("refreshFiles").disabled = false; }
}
async function selectFile(path) {
  const version = ++state.fileRequest;
  state.selectedFile = path; renderFiles(); clearError("filesError");
  if (route.name === "files") history.replaceState(null, "", `#/files/${encodeURIComponent(path)}`);
  $("fileEmpty").hidden = true; $("filePreview").hidden = false;
  $("fileName").textContent = path; $("fileContent").hidden = false; $("fileContent").textContent = "Loading file...";
  $("fileFrame").hidden = true; $("fileFrame").removeAttribute("src"); $("fileHash").textContent = "";
  $("expandPreview").disabled = true; $("previewHint").textContent = "";
  const file = state.files.find(record => record.path === path);
  $("fileMeta").textContent = `${size(file?.size)} - ${date(file?.modified)}`;
  try {
    const data = await api(`/api/file?path=${encodeURIComponent(path)}`);
    if (version !== state.fileRequest) return;
    if (typeof data?.content !== "string") throw new Error("The host did not return text content. You can still download this file.");
    $("fileContent").textContent = data.content; $("fileHash").textContent = data.sha256 || "Fingerprint not reported";
    const html = /\.html?$/i.test(path) || file?.kind === "html";
    if (html) {
      const url = `/preview?path=${encodeURIComponent(path)}`;
      // Check host errors before letting an isolated frame render the artifact.
      await (await request(url)).arrayBuffer();
      if (version !== state.fileRequest) return;
      $("fileFrame").src = url; $("fileFrame").hidden = false; $("fileContent").hidden = true;
    }
    $("previewHint").textContent = html ? "Sandboxed HTML. Inline scripts enabled; network blocked." : "Plain text preview. Nothing is executed.";
    $("expandPreview").disabled = false;
    if ($("previewDialog").open) updateExpandedPreview();
  } catch (reason) {
    if (version === state.fileRequest) {
      $("fileContent").textContent = "Preview unavailable. The original file can still be downloaded."; error(reason, "filesError");
      if ($("previewDialog").open) updateExpandedPreview();
    }
  }
}
async function download() {
  const path = state.selectedFile;
  if (!path) return;
  $("downloadFile").disabled = true; clearError("filesError");
  try {
    const blob = await (await request(`/download?path=${encodeURIComponent(path)}`)).blob();
    const url = URL.createObjectURL(blob), anchor = node("a");
    anchor.href = url; anchor.download = path.split(/[\\/]/).pop(); anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 30000);
  } finally { $("downloadFile").disabled = false; }
}
function updateExpandedPreview() {
  $("previewTitle").textContent = state.selectedFile;
  $("expandedText").textContent = $("fileContent").textContent; $("expandedText").hidden = $("fileContent").hidden;
  $("expandedFrame").hidden = $("fileFrame").hidden;
  if (!$("fileFrame").hidden) $("expandedFrame").src = $("fileFrame").src;
  else $("expandedFrame").removeAttribute("src");
}
function expandPreview() {
  clearError("previewError"); updateExpandedPreview();
  $("previewDialog").showModal();
}
function parseRoute() {
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean).map(part => { try { return decodeURIComponent(part); } catch { return ""; } });
  const name = ROUTES.includes(parts[0]) ? parts[0] : "sessions";
  return { name, params: name === parts[0] ? parts.slice(1) : [] };
}
function applyRoute() {
  const next = parseRoute(), previous = route.name;
  route.name = next.name; route.params = next.params;
  document.body.dataset.route = next.name;
  document.querySelectorAll("main > [data-view]").forEach(section => { section.hidden = section.dataset.view !== next.name; });
  const navName = NAV[next.name] || next.name;
  document.querySelectorAll("nav.side a").forEach(link => {
    if (link.dataset.nav === navName) link.setAttribute("aria-current", "page"); else link.removeAttribute("aria-current");
  });
  if (previous && previous !== next.name) views[previous]?.hide?.();
  const view = views[next.name];
  if (view) {
    if (!view.mounted) { view.mount($(`view-${next.name}`), ctx); view.mounted = true; }
    view.show(next.params);
  }
  if (next.name === "assistant") {
    const id = next.params[0];
    if (id && id !== state.current && state.sessions.some(session => session.id === id)) run(() => choose(id));
    else if (!id && state.current) history.replaceState(null, "", `#/assistant/${encodeURIComponent(state.current)}`);
    renderChat(); renderActivity();
  }
  if (next.name === "files" && previous && previous !== "files") run(loadFiles, "filesError");
  if (next.name === "files" && next.params[0] && next.params[0] !== state.selectedFile && state.files.some(file => file.path === next.params[0])) run(() => selectFile(next.params[0]), "filesError");
}
async function refreshWorkspace() {
  const scope = state.scope;
  connect();
  try {
    await Promise.all([run(async () => {
      await loadSessions();
      if (scope !== state.scope) return;
      const requested = route.name === "assistant" ? route.params[0] : null;
      if (state.current) await loadSession(state.current);
      else if (!state.selectionMade) {
        const selected = state.sessions.find(session => session.id === requested)?.id;
        await choose(selected || (draft(null).text ? null : state.sessions[0]?.id) || null);
      }
    }), run(async () => {
      await loadFiles();
      if (route.name === "files" && route.params[0] && state.files.some(file => file.path === route.params[0])) await selectFile(route.params[0]);
    }, "filesError")]);
  } finally { if (scope === state.scope) { state.booting = false; controls(); } }
}
async function refresh() { await loadStatus(); await refreshWorkspace(); }
const ctx = {
  api, request, bus,
  status: () => state.status,
  workspaceId: () => state.workspaceId,
  navigate: hash => { if (location.hash === hash) applyRoute(); else location.hash = hash; },
  announce: text => { $("announcement").textContent = text; },
  notify: reason => error(reason),
  refreshStatus: () => loadStatus(),
  files: () => state.files,
  assistant: {
    addContext,
    open: () => ctx.navigate(state.current ? `#/assistant/${encodeURIComponent(state.current)}` : "#/assistant"),
    // Fills an empty composer only; never overwrites what the person typed.
    prefill: text => {
      if ($("message").value.trim() || typeof text !== "string") return;
      $("message").value = text; setDraft(draft(), text); controls();
    },
  },
};
$("message").addEventListener("input", () => { setDraft(draft(), $("message").value); controls(); });
$("retryDrafts").addEventListener("click", retryDrafts);
window.addEventListener("beforeunload", event => {
  if ([...state.drafts.values()].some(record => record.dirty)) { event.preventDefault(); event.returnValue = ""; }
});
$("message").addEventListener("keydown", event => {
  if (event.key === "Enter" && (event.ctrlKey || event.metaKey) && !event.isComposing) { event.preventDefault(); run(send); }
});
$("composer").addEventListener("submit", event => { event.preventDefault(); run(send); });
$("newSession").addEventListener("click", () => run(newSession));
$("stop").addEventListener("click", () => run(stop));
$("activeSession").addEventListener("click", () => { if (state.active?.sessionId) run(() => choose(state.active.sessionId)); });
$("refreshSessions").addEventListener("click", () => run(loadSessions));
$("refreshFiles").addEventListener("click", () => run(loadFiles, "filesError"));
$("retryHost").addEventListener("click", () => { clearError("notice"); run(refresh); });
$("dismissNotice").addEventListener("click", () => clearError("notice"));
$("setupModel").addEventListener("click", () => ctx.navigate("#/models"));
$("contextPreviewToggle").addEventListener("click", () => { state.contextOpen = !state.contextOpen; renderContext(); });
$("expandPreview").addEventListener("click", expandPreview);
$("closePreview").addEventListener("click", () => $("previewDialog").close());
$("previewDialog").addEventListener("close", () => $("expandedFrame").removeAttribute("src"));
$("downloadFile").addEventListener("click", () => run(download, "filesError"));
$("reviseFile").addEventListener("click", () => {
  if (!state.selectedFile) return;
  addContext({ kind: "file", label: state.selectedFile, text: `Workspace file ${state.selectedFile}` });
  ctx.assistant.open();
  $("message").focus();
});
window.addEventListener("hashchange", applyRoute);
quoteCheck($("quoteCheck"), ctx);
applyRoute();
run(refresh);

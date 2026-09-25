(() => {
  "use strict";
  const $ = id => document.getElementById(id);
  const state = { csrf: "", current: null, sessions: [], files: [], cache: new Map(), active: null,
    completed: new Set(), seq: 0, revision: 0, connected: false, sending: false, creating: false,
    canceling: false, interrupted: false, source: null, selectedFile: null, fileRequest: 0, filesRequest: 0, statusRequest: 0, modelRequest: 0, auth: null,
    workspaceId: "", scope: 0, booting: true, selectionMade: false, modelConfigured: false, drafts: new Map(), visiblePermission: null,
    formBusy: false, modelEdit: 0, modelOperation: null };
  const modelFields = ["provider", "model", "endpoint", "credentialRef"];
  const canonicalProvider = value => value === "github_copilot" ? "github-copilot" : value;
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
  const redact = (text, key) => key ? String(text).split(key).join("[redacted]") : String(text);
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
    state.scope++; state.workspaceId = id; state.booting = true; state.selectionMade = false;
    state.source?.close(); state.source = null; state.connected = false; state.interrupted = false;
    state.current = null; state.cache = new Map(); state.sessions = []; state.files = [];
    state.active = null; state.completed.clear(); state.seq = 0; state.revision++;
    state.sending = false; state.creating = false; state.canceling = false; state.visiblePermission = null;
    state.fileRequest++; state.filesRequest++; state.modelRequest++; state.selectedFile = null;
    $("settings").close(); $("previewDialog").close(); $("fileFrame").removeAttribute("src");
    $("filePreview").hidden = true; $("fileEmpty").hidden = false; clearError("notice");
    $("message").value = draft(null).text;
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
  const size = value => Number.isFinite(value) ? (value < 1024 ? `${value} B` : `${(value / 1024).toFixed(1)} KB`) : "Size not reported";
  const args = value => typeof value === "string" ? value : JSON.stringify(value ?? {}, null, 2);
  const pending = item => item.activity.filter(event => event.type === "permission" && !event.result);
  function active(raw, sessionId) {
    const turnId = typeof raw === "string" ? raw : raw?.turnId || raw?.id;
    return turnId && !state.completed.has(turnId) ? { turnId, sessionId: raw?.sessionId || sessionId } : null;
  }
  function panel(name) {
    document.body.dataset.panel = name;
    document.querySelectorAll(".mobile-nav button").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.panel === name)));
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
      state.booting ? "Loading workspace..." : state.creating ? "Creating conversation..." : state.sending ? "Starting turn..." : item.loading ? "Loading conversation..." : item.error ? "Conversation unavailable" : !state.modelConfigured ? "Set up model to send" : state.csrf ? "Ready" : "Host not loaded";
    if (state.interrupted) $("turnStatus").textContent = state.source?.readyState === EventSource.CLOSED ? "Live updates stopped. Refresh host to reconnect." :
      `Live updates interrupted. ${state.active ? "Stop is available." : "Reconnecting..."}`;
    $("activeSession").hidden = !state.active?.sessionId || state.active.sessionId === state.current;
    $("setupModel").hidden = !state.csrf || state.modelConfigured;
    document.body.classList.toggle("has-permissions", pending(item).length > 0);
    renderDraftStatus();
  }
  function renderModel(model) {
    state.modelConfigured = model.configured === true;
    $("modelSummary").textContent = `${canonicalProvider(model.provider) || "Provider not set"} / ${model.model || "Model not set"}`;
    $("modelSummary").title = $("modelSummary").textContent;
    $("modelCredential").textContent = modelStatus(model);
  }
  const modelStatus = model => `${model.configured ? "Model selected" : "Model not selected"} / ${model.authStatus === "verified" ? "authentication verified" : "authentication checked on use"}`;
  async function loadStatus() {
    const version = ++state.statusRequest, revision = state.revision;
    const data = await api("/api/status");
    if (version !== state.statusRequest) return;
    if (typeof data?.csrfToken !== "string" || !data.csrfToken || !data.model || typeof data.workspaceId !== "string" || !data.workspaceId) throw new Error("Incomplete host status. A stable workspace ID, model configuration and CSRF token are required.");
    const bundles = list(data, "bundles");
    const changed = bindWorkspace(data.workspaceId);
    if (state.csrf && state.csrf !== data.csrfToken) { state.seq = 0; state.completed.clear(); }
    state.csrf = data.csrfToken;
    $("workspace").textContent = data.workspace;
    $("workspace").title = data.workspace;
    $("coreVersion").textContent = `Core ${data.coreVersion || "not reported"}`;
    $("kernelVersion").textContent = `Kernel candidate ${data.kernelVersion || "not reported"}`;
    $("hostNotice").textContent = typeof data.notice === "string" ? data.notice : "";
    $("hostNotice").hidden = !$("hostNotice").textContent;
    renderModel(data.model);
    $("bundles").replaceChildren(...bundles.map(bundle => {
      const li = node("li"); li.append(node("strong", "", bundle.name), node("p", "quiet", bundle.description)); return li;
    }));
    if (!bundles.length) $("bundles").append(node("li", "quiet", "No shared bundles reported by the host."));
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
      if (scope === state.scope && id === state.current) { renderChat(); renderActivity(); }
    }
  }
  async function choose(id, load = true) {
    state.current = id; state.selectionMade = true; state.visiblePermission = null;
    history.replaceState(null, "", id === null ? `${location.pathname}${location.search}` : `#session=${encodeURIComponent(id)}`);
    $("message").value = draft(id).text;
    panel("chat"); renderSessions(); renderChat(); renderActivity();
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
  async function send() {
    if ($("send").disabled) return;
    const text = $("message").value, scope = state.scope, workspace = state.workspaceId;
    const sourceDraft = state.current === null ? draft(null, workspace) : null;
    state.sending = true; controls(); clearError("notice");
    try {
      let id = state.current;
      if (!id) {
        id = await createSession();
        if (scope !== state.scope) return;
        const moved = draft(id, workspace); setDraft(moved, text);
        if (!moved.dirty) clearMatchingDraft(draft(null, workspace), text);
        await choose(id, false);
      }
      const item = entry(id), before = item.messages.length, savedDraft = draft(id, workspace);
      const data = await api(`/api/sessions/${encodeURIComponent(id)}/turn`, "POST", { message: text });
      if (!data?.turnId) throw new Error("The host did not return a turn ID. Refresh before retrying to avoid a duplicate turn.");
      clearMatchingDraft(savedDraft, text);
      if (sourceDraft) clearMatchingDraft(sourceDraft, text);
      if (scope !== state.scope) return;
      // SSE may finish the turn and refresh history before the HTTP acknowledgement.
      if (!item.messages.slice(before).some(record => record.role === "user" && record.content === text)) item.messages.push({ role: "user", content: text });
      if (item.live?.turnId !== data.turnId) item.live = null;
      if (!state.completed.has(data.turnId)) { state.active = { turnId: data.turnId, sessionId: id }; state.revision++; }
      if (state.current === id) { $("message").value = savedDraft.text; renderChat(); scrollBottom(); $("message").focus(); }
      run(() => loadSession(id));
    } finally { if (scope === state.scope) { state.sending = false; controls(); } }
  }
  function renderActivity() {
    const item = entry(), outstanding = pending(item);
    $("activity").hidden = !item.activity.length;
    if (outstanding.length) $("activity").open = true;
    $("activitySummary").textContent = `Tool activity (${item.activity.length})${pending(item).length ? " - permission needed" : ""}`;
    $("activityList").replaceChildren(...item.activity.map(event => {
      if (event.type === "error") return node("p", "error", event.error || event.text || "Turn failed.");
      if (event.type !== "permission") {
        const details = node("details", "tool");
        details.open = !!event.open;
        details.addEventListener("toggle", () => { event.open = details.open; });
        details.append(node("summary", "", `${event.tool || "Tool"} - ${event.status || event.phase || "activity"}`),
          node("pre", "", args(event.arguments)));
        if (event.text) details.append(node("pre", "", event.text));
        return details;
      }
      const card = node("section", "permission"), actions = node("div", "permission-actions");
      card.setAttribute("aria-label", `Permission for ${event.tool}`);
      const inspect = node("details", "permission-arguments");
      inspect.open = !!event.argumentsOpen;
      inspect.append(node("summary", "", "Inspect arguments"), node("pre", "", args(event.arguments)));
      inspect.addEventListener("toggle", () => { event.argumentsOpen = inspect.open; });
      card.append(node("strong", "", `${event.tool} requests permission`), inspect);
      for (const allow of [true, false]) {
        const button = node("button", allow ? "primary" : "", allow ? "Allow" : "Deny");
        button.type = "button"; button.disabled = !!event.result || !!event.busy;
        button.addEventListener("click", () => run(() => decide(event, allow)));
        actions.append(button);
      }
      const status = node("p", "", event.result || (event.busy ? "Sending decision..." : "Only this action will be authorized."));
      status.setAttribute("role", "status"); actions.append(status); card.append(actions);
      if (event.error) card.append(node("p", "error", event.error));
      return card;
    }));
    controls();
    const first = outstanding[0]?.permissionId;
    if (first && state.visiblePermission !== first) {
      state.visiblePermission = first;
      requestAnimationFrame(() => $("activityList").querySelector(".permission button:not(:disabled)")?.scrollIntoView({ block: "nearest" }));
    }
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
    if (event.type === "files_changed") { run(loadFiles, "filesError"); return; }
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
    } else if (event.type === "tool") item.activity.push(event);
    else if (event.type === "permission") {
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
  async function loadModel(focus = true) {
    const version = ++state.modelRequest;
    invalidateModelTools();
    modelBusy(true);
    $("credentialStatus").textContent = "Loading configuration...";
    try {
      const model = await api("/api/model");
      if (version !== state.modelRequest) return;
      if (!model || typeof model.configured !== "boolean") throw new Error("Host returned an invalid model configuration.");
      for (const key of modelFields) $(key).value = key === "provider" ? canonicalProvider(model.provider) || "openai" : model[key] || "";
      if (!$("provider").value) error("This host reports an unsupported provider. Choose a listed provider before saving.", "modelError");
      $("credentialStatus").textContent = modelStatus(model) + (model.credentialConfigured === true ? ". Stored credential available." : "");
      renderModel(model); modelBusy(false); controls();
      if (focus) $("provider").focus();
      return model;
    } catch (reason) {
      if (version !== state.modelRequest) return;
      $("credentialStatus").textContent = "Configuration unavailable. Close and reopen to retry.";
      throw reason;
    }
  }
  async function openSettings() {
    clearSignIn(); clearError("modelError"); $("apiKey").value = "";
    $("settings").showModal();
    await loadModel();
  }
  function modelBusy(busy) {
    state.formBusy = busy;
    [...modelFields, "apiKey"].forEach(key => { $(key).disabled = busy || !!state.auth; });
    $("saveModel").disabled = busy || !!state.auth; $("modelForm").setAttribute("aria-busy", String(busy));
    authControls();
  }
  function authControls() {
    const copilot = $("provider").value === "github-copilot";
    const busy = state.formBusy || !!state.auth || !!state.modelOperation;
    $("copilotSignIn").hidden = !copilot;
    $("signInCopilot").disabled = !copilot || busy;
    $("listModels").disabled = busy || !$("provider").value;
    $("checkModel").disabled = busy || !$("provider").value || !$("model").value.trim();
    $("saveModel").disabled = busy;
  }
  function invalidateModelTools(keepCatalog = false) {
    state.modelEdit++; state.modelOperation?.controller.abort(); state.modelOperation = null;
    if (!keepCatalog) { $("modelCatalog").hidden = true; $("modelChoice").replaceChildren(); $("modelCatalogNote").textContent = ""; }
    $("modelCheckResult").hidden = true; $("modelCheckResult").textContent = "";
    $("modelToolStatus").hidden = true; $("modelToolStatus").textContent = "";
    clearError("modelError"); authControls();
  }
  function modelInput(requireModel) {
    const body = Object.fromEntries(modelFields.map(name => [name, $(name).value.trim()]));
    if (!body.provider) throw new Error("Choose a provider.");
    if (requireModel && !body.model) throw new Error("Enter a model identifier.");
    if (body.endpoint) {
      let url;
      try { url = new URL(body.endpoint); } catch { throw new Error("Endpoint must be an HTTP(S) URL without credentials."); }
      if (!["https:", "http:"].includes(url.protocol) || url.username || url.password) throw new Error("Endpoint must be an HTTP(S) URL without credentials.");
    }
    if ($("apiKey").value) body.apiKey = $("apiKey").value;
    return body;
  }
  function clearSuccessfulKey(key) {
    if (key && $("apiKey").value === key) $("apiKey").value = "";
  }
  async function modelOperation(kind) {
    if ($(kind === "catalog" ? "listModels" : "checkModel").disabled) return;
    invalidateModelTools();
    const operation = { controller: new AbortController(), edit: state.modelEdit, scope: state.scope };
    const key = $("apiKey").value;
    state.modelOperation = operation; authControls();
    $("modelToolStatus").hidden = false;
    $("modelToolStatus").textContent = kind === "catalog" ? "Finding models; this is not verified inference..." : "Running the requested small tool-capability check...";
    try {
      const result = await api(kind === "catalog" ? "/api/models" : "/api/model/check", "POST", modelInput(kind === "check"), operation.controller.signal);
      if (state.modelOperation !== operation || operation.edit !== state.modelEdit || operation.scope !== state.scope) return;
      if (kind === "catalog") {
        const models = list(result, "models");
        if (models.some(model => typeof model.id !== "string" || !model.id.trim())) throw new Error("Host returned an invalid model catalog.");
        const placeholder = node("option", "", "Keep the exact entered model, or choose one"); placeholder.value = "";
        $("modelChoice").replaceChildren(placeholder, ...models.map(model => {
          const option = node("option", "", redact(model.name ? `${model.name} (${model.id})` : model.id, key));
          option.value = model.id; return option;
        }));
        $("modelChoice").disabled = !models.length;
        $("modelCatalogNote").textContent = `Catalog listing is not verified inference. ${redact(typeof result.note === "string" ? result.note : "", key)}`;
        $("modelCatalog").hidden = false;
        $("modelToolStatus").textContent = models.length ? `Listed ${models.length} models. Your entered ID has not changed.` : "No models were listed. You can enter an exact model ID manually.";
      } else {
        if (result?.ok !== true || typeof result.message !== "string") throw new Error("Host returned an invalid model-check result.");
        $("modelCheckResult").textContent = `Model check succeeded: ${redact(result.message, key)}`;
        $("modelCheckResult").hidden = false; $("modelToolStatus").hidden = true;
      }
      clearSuccessfulKey(key);
    } catch (reason) {
      if (state.modelOperation !== operation) return;
      const message = redact(reason.message, key);
      error(`${kind === "catalog" ? "Model catalog" : "Model check"} failed: ${message}`, "modelError");
      $("modelToolStatus").hidden = true;
    } finally {
      if (state.modelOperation === operation) { state.modelOperation = null; authControls(); }
    }
  }
  function clearSignIn(message = "") {
    const flow = state.auth;
    state.auth = null;
    if (flow) { clearTimeout(flow.timer); clearTimeout(flow.expiryTimer); flow.controller.abort(); }
    $("authUserCode").textContent = ""; $("authExpiry").textContent = ""; $("authLink").removeAttribute("href");
    $("authInstructions").hidden = true; $("cancelCopilot").hidden = true;
    $("authStatus").textContent = message; $("authProgress").hidden = !message;
    modelBusy(false);
  }
  function finishSignIn(flow, message, failed = false) {
    if (state.auth !== flow) return;
    clearSignIn(message);
    if (failed) error(message, "modelError");
  }
  function authInterval(seconds) {
    if (!Number.isFinite(seconds) || seconds <= 0 || seconds * 1000 > 2147483647) throw new Error("The host returned an invalid sign-in polling interval.");
    return seconds * 1000;
  }
  function githubVerificationURL(value) {
    let url;
    try { url = new URL(value); } catch { throw new Error("The host returned an unsafe GitHub verification link."); }
    if (url.origin !== "https://github.com" || url.pathname !== "/login/device" || url.username || url.password || url.search || url.hash) {
      throw new Error("The host returned an unsafe GitHub verification link.");
    }
    return url.href;
  }
  function scheduleAuthPoll(flow) {
    $("authStatus").textContent = `Waiting for GitHub authorization. Checking every ${flow.interval / 1000} seconds.`;
    flow.timer = setTimeout(() => pollSignIn(flow), flow.interval);
  }
  async function startSignIn() {
    if ($("signInCopilot").disabled) return;
    invalidateModelTools();
    const flow = { model: $("model").value.trim(), controller: new AbortController() };
    state.auth = flow; clearError("modelError"); modelBusy(true);
    $("authProgress").hidden = false; $("cancelCopilot").hidden = false;
    $("authStatus").textContent = "Requesting a GitHub sign-in code...";
    try {
      const data = await api("/api/auth/copilot/start", "POST", { model: flow.model }, flow.controller.signal);
      if (state.auth !== flow) return;
      const expires = Date.parse(data?.expiresAt);
      if (typeof data?.id !== "string" || !data.id || typeof data.userCode !== "string" || !data.userCode || !Number.isFinite(expires)) {
        throw new Error("The host returned incomplete GitHub sign-in instructions.");
      }
      const link = githubVerificationURL(data.verificationURI);
      flow.interval = authInterval(data.interval); flow.id = data.id; flow.expires = expires;
      if (expires <= Date.now()) throw new Error("GitHub sign-in code expired. Start again.");
      $("authUserCode").textContent = data.userCode; $("authLink").href = link;
      $("authExpiry").textContent = `Code expires ${date(data.expiresAt)}.`;
      $("authInstructions").hidden = false; modelBusy(false);
      flow.expiryTimer = setTimeout(() => finishSignIn(flow, "GitHub sign-in code expired. Start again.", true), expires - Date.now());
      scheduleAuthPoll(flow); $("authLink").focus();
    } catch (reason) { finishSignIn(flow, `GitHub sign-in failed: ${reason.message}`, true); }
  }
  async function pollSignIn(flow) {
    if (state.auth !== flow) return;
    if (Date.now() >= flow.expires) { finishSignIn(flow, "GitHub sign-in code expired. Start again.", true); return; }
    try {
      const result = await api("/api/auth/copilot/poll", "POST", { id: flow.id }, flow.controller.signal);
      if (state.auth !== flow) return;
      if (result?.status === "pending") {
        if (result.interval !== undefined) flow.interval = authInterval(result.interval);
        scheduleAuthPoll(flow);
      } else if (result?.status === "success") {
        flow.succeeded = true; clearTimeout(flow.expiryTimer); $("cancelCopilot").hidden = true;
        $("authStatus").textContent = "GitHub sign-in complete. Refreshing host settings...";
        await loadStatus();
        if (state.auth !== flow) return;
        const model = await loadModel(false);
        if (state.auth !== flow) return;
        if (canonicalProvider(model?.provider) !== "github-copilot" || model.model !== flow.model) throw new Error("The host returned a different model after sign-in.");
        $("apiKey").value = "";
        finishSignIn(flow, flow.model ? "GitHub sign-in complete. Model authentication is checked on use." : "GitHub sign-in complete. Find or enter a model, then save settings.");
      } else if (result?.status === "failed") {
        throw new Error(result.error || "Authorization was not completed. Start again.");
      } else throw new Error("The host returned an unexpected sign-in status.");
    } catch (reason) {
      finishSignIn(flow, `${flow.succeeded ? "GitHub sign-in completed, but settings refresh failed" : "GitHub sign-in failed"}: ${reason.message}`, true);
    }
  }
  async function saveSettings() {
    if ($("saveModel").disabled) return;
    const key = $("apiKey").value;
    invalidateModelTools(); modelBusy(true); $("openSettings").disabled = true; clearError("modelError");
    try {
      await api("/api/model", "PUT", modelInput(true));
      clearSuccessfulKey(key);
      await loadStatus();
      $("settings").close(); $("announcement").textContent = "Model settings saved.";
    } catch (reason) { error(redact(reason.message, key), $("settings").open ? "modelError" : "notice"); }
    finally { modelBusy(false); $("openSettings").disabled = false; }
  }
  async function refreshWorkspace() {
    const scope = state.scope;
    connect();
    try {
      await Promise.all([run(async () => {
        await loadSessions();
        if (scope !== state.scope) return;
        const requested = new URLSearchParams(location.hash.slice(1)).get("session");
        if (state.current) await loadSession(state.current);
        else if (!state.selectionMade) {
          const selected = state.sessions.find(session => session.id === requested)?.id;
          await choose(selected || (draft(null).text ? null : state.sessions[0]?.id) || null);
        }
      }), run(loadFiles, "filesError")]);
    } finally { if (scope === state.scope) { state.booting = false; controls(); } }
  }
  async function refresh() { await loadStatus(); await refreshWorkspace(); }
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
  $("openSettings").addEventListener("click", () => run(openSettings, "modelError"));
  $("setupModel").addEventListener("click", () => run(openSettings, "modelError"));
  [...modelFields, "apiKey"].forEach(name => $(name).addEventListener(name === "provider" ? "change" : "input", () => invalidateModelTools()));
  $("listModels").addEventListener("click", () => run(() => modelOperation("catalog"), "modelError"));
  $("checkModel").addEventListener("click", () => run(() => modelOperation("check"), "modelError"));
  $("modelChoice").addEventListener("change", () => {
    if ($("modelChoice").value) { $("model").value = $("modelChoice").value; invalidateModelTools(true); }
  });
  $("signInCopilot").addEventListener("click", () => run(startSignIn, "modelError"));
  $("cancelCopilot").addEventListener("click", () => clearSignIn("Sign-in polling stopped. Unfinished requests expire automatically."));
  $("modelForm").addEventListener("submit", event => { event.preventDefault(); run(saveSettings, "modelError"); });
  document.querySelectorAll(".close-settings").forEach(button => button.addEventListener("click", () => $("settings").close()));
  $("settings").addEventListener("close", () => { state.modelRequest++; $("apiKey").value = ""; clearSignIn(); invalidateModelTools(); });
  $("expandPreview").addEventListener("click", expandPreview);
  $("closePreview").addEventListener("click", () => $("previewDialog").close());
  $("previewDialog").addEventListener("close", () => $("expandedFrame").removeAttribute("src"));
  $("downloadFile").addEventListener("click", () => run(download, "filesError"));
  document.querySelectorAll(".mobile-nav button").forEach(button => button.addEventListener("click", () => panel(button.dataset.panel)));
  run(refresh);
})();

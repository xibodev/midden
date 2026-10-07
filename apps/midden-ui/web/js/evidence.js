import { el, button, toolBadge, toolNames, emptyState, errorBox, loading, copyButton, shortId, recordRow, pager, dialog, field, formatDate } from "/js/components.js";

let root, ctx;
const view = { id: "", page: null, error: "", request: 0, contexts: new Map(), assets: null, message: "" };
// Selection survives view changes (search, context, paging) and lives per workspace.
const selection = new Map();

function storageKey() { return `midden:selection:v1:${ctx.workspaceId()}`; }
function loadSelection() {
  selection.clear();
  try {
    const raw = JSON.parse(sessionStorage.getItem(storageKey()) || "[]");
    for (const item of Array.isArray(raw) ? raw : []) if (item?.record?.id && item.viewId) selection.set(item.record.id, item);
  } catch { /* storage unavailable: selection stays in memory */ }
}
function saveSelection() {
  try { sessionStorage.setItem(storageKey(), JSON.stringify([...selection.values()])); } catch { /* memory only */ }
}
function toggle(record, viewId, title, selected) {
  if (selected) selection.set(record.id, { record: { id: record.id, text: String(record.text || "").slice(0, 400), role: record.role, kind: record.kind, reference: record.reference }, viewId, title });
  else selection.delete(record.id);
  saveSelection(); render();
}

function slug(text) {
  return String(text || "sources").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 40) || "sources";
}

function header(page) {
  const tool = page.source?.tool;
  return el("div", {},
    el("div", { class: "crumb" }, el("a", { text: "Sessions", attrs: { href: `#/sessions/${encodeURIComponent(tool)}/${encodeURIComponent(page.source?.session_id)}` } }), ` › ${page.title || page.source?.session_id || ""}`),
    el("div", { class: "view-heading" }, el("h1", { text: "Evidence" })),
    el("p", { class: "row sub" }, toolBadge(tool), `View `, el("span", { class: "mono", text: shortId(page.view_id, 6) }), copyButton(page.view_id),
      ` · ${page.selection} · pinned at ${page.boundary?.records ?? "?"} records`,
      page.first_time ? ` · ${formatDate(page.first_time)} – ${formatDate(page.last_time)}` : ""),
    el("p", { class: "row sub" }, "Newer activity needs a fresh view.", button("Refresh view", { small: true, onClick: () => refreshView(page) }),
      page.selection !== "representative" ? button("Back to sample", { small: true, link: true, onClick: () => refreshView(page) }) : null));
}

function searchForm(page) {
  const input = el("input", { attrs: { type: "search", placeholder: "Phrase, 3–300 characters", "aria-label": "Search this view", minlength: "3", maxlength: "300" } });
  const includeTools = el("input", { attrs: { type: "checkbox" } });
  return el("form", { class: "filters", on: { submit: event => { event.preventDefault(); search(page, input.value.trim(), includeTools.checked); } } },
    el("label", { class: "field grow" }, el("span", { text: "Search this view" }), input),
    el("label", { class: "check" }, includeTools, " Include tool output"),
    button("Search", { primary: true, type: "submit" }),
    el("span", { class: "quiet", text: "Each search pins a new view." }));
}

function records(page) {
  if (!page.records.length) return emptyState("No records here", page.selection === "search" ? "No record contains that phrase in the pinned part of the session." : "The session has no readable records in this view.");
  const list = el("div", { class: "records" });
  for (const record of page.records) {
    const expanded = view.contexts.get(record.id);
    list.append(recordRow(record, {
      selected: selection.has(record.id),
      onToggle: checked => toggle(record, page.view_id, page.title, checked),
      onContext: () => expanded ? (view.contexts.delete(record.id), render()) : showContext(page, record),
      contextLabel: expanded ? "Hide context" : "± context",
    }));
    if (expanded?.loading) list.append(loading("Reading surrounding records..."));
    else if (expanded?.error) list.append(errorBox(expanded.error));
    else if (expanded?.records) for (const neighbour of expanded.records.filter(item => item.id !== record.id)) {
      list.append(el("div", { class: "ctx" },
        el("label", { class: "check" }, (() => { const box = el("input", { attrs: { type: "checkbox", "aria-label": `Select record ${neighbour.reference?.record_index}` } }); box.checked = selection.has(neighbour.id); box.addEventListener("change", () => toggle(neighbour, expanded.viewId, page.title, box.checked)); return box; })(),
          ` #${neighbour.reference?.record_index ?? "?"} ${neighbour.role || neighbour.kind || ""}`), el("span", { text: ` ${String(neighbour.text || "").slice(0, 400)}` })));
    }
  }
  return list;
}

function tray() {
  const items = [...selection.values()];
  const box = el("div", { class: "card" }, el("h2", { text: `Selection (${items.length})` }));
  if (!items.length) box.append(el("p", { class: "quiet", text: "Tick records to collect them into your workspace or hand them to the assistant." }));
  else {
    box.append(el("ul", { class: "list" }, ...items.map(item => el("li", {},
      el("span", { class: "grow", text: `#${item.record.reference?.record_index ?? "?"} ${item.record.role || item.record.kind || ""} · ${String(item.record.text || "").slice(0, 60)}` }),
      button("Remove", { small: true, link: true, onClick: () => { selection.delete(item.record.id); saveSelection(); render(); } })))));
    box.append(el("div", { class: "row" },
      button("Collect into workspace…", { primary: true, onClick: () => collectDialog(items) }),
      button("Use in chat", { onClick: () => useInChat(items) }),
      button("Clear", { small: true, link: true, onClick: () => { selection.clear(); saveSelection(); render(); } })));
  }
  if (view.message) box.append(el("p", { class: "quiet", text: view.message, attrs: { role: "status" } }));
  return box;
}

function assetsCard(page) {
  const box = el("div", { class: "card" }, el("h2", { text: "Assets in this view" }));
  if (view.assets === null) { box.append(button("List assets", { small: true, onClick: () => loadAssets(page) })); return box; }
  if (view.assets.loading) { box.append(loading("Scanning the pinned records...")); return box; }
  if (view.assets.error) { box.append(errorBox(view.assets.error)); return box; }
  const assets = view.assets.assets || [];
  if (!assets.length) box.append(el("p", { class: "quiet", text: "No images or files recorded in this view." }));
  else box.append(el("div", { class: "assets" }, ...assets.slice(0, 24).map(asset => el("div", { class: "tile" },
    el("b", { text: asset.name || "asset" }), el("span", { class: asset.status === "unavailable" ? "warnt" : asset.status === "external_reference" ? "quiet" : "okt", text: asset.status.replace("_", " ") })))));
  return box;
}

function useInChat(items) {
  const byView = new Map();
  for (const item of items) { if (!byView.has(item.viewId)) byView.set(item.viewId, []); byView.get(item.viewId).push(item); }
  for (const [viewId, group] of byView) {
    const title = group[0].title || "a recorded session";
    ctx.assistant.addContext({ kind: "records", label: `${group.length} record${group.length === 1 ? "" : "s"} from “${title}”`,
      text: `Records from “${title}” (view ${viewId}): ${group.map(item => item.record.id).join(", ")}. Read them with: midden read --view ${viewId} --record <id>.` });
  }
  ctx.assistant.open();
}

function collectDialog(items) {
  const views = [...new Set(items.map(item => item.viewId))];
  const destination = el("input", { class: "mono", attrs: { value: `sources/${slug(items[0]?.title)}`, spellcheck: "false" } });
  const assets = el("input", { attrs: { type: "checkbox" } });
  const status = el("p", { class: "quiet", attrs: { role: "status" } });
  const modal = dialog(`Collect ${items.length} record${items.length === 1 ? "" : "s"}`);
  modal.body.append(field("New folder in the workspace", destination, "Always creates a new folder; an existing one is never overwritten."),
    el("label", { class: "check" }, assets, " Copy available images and files"),
    el("p", { class: "sub", text: `${items.length} record${items.length === 1 ? "" : "s"} from ${views.length} view${views.length === 1 ? "" : "s"}.` }), status);
  const collect = button("Collect", { primary: true, onClick: async () => {
    const out = destination.value.trim().replace(/\\/g, "/");
    if (!out) { status.textContent = "Choose a folder name."; return; }
    collect.disabled = true; status.textContent = "Collecting...";
    try {
      const result = await ctx.api("/api/core/collect", "POST", { views, records: items.map(item => item.record.id), assets: assets.checked, out });
      modal.close();
      view.message = `Collected ${result.record_count} records into ${out}.`;
      selection.clear(); saveSelection();
      ctx.announce(view.message);
      ctx.navigate(`#/sources/${encodeURIComponent(out)}`);
    } catch (reason) { status.textContent = reason.message; collect.disabled = false; }
  } });
  modal.actions.append(button("Cancel", { onClick: () => modal.close() }), collect);
  modal.open(); destination.focus();
}

async function showContext(page, record) {
  const state = { loading: true };
  view.contexts.set(record.id, state); render();
  try {
    const result = await ctx.api(`/api/core/views/${encodeURIComponent(page.view_id)}/context`, "POST", { records: [record.id], before: 2, after: 2 });
    Object.assign(state, { loading: false, records: result.records, viewId: result.view_id });
  } catch (reason) { Object.assign(state, { loading: false, error: reason.message }); }
  render();
}

async function search(page, query, includeTools) {
  if (query.length < 3) { view.message = "Search needs at least 3 characters."; render(); return; }
  view.message = "Searching..."; render();
  try {
    const result = await ctx.api(`/api/core/views/${encodeURIComponent(page.view_id)}/search`, "POST", { query, includeTools });
    view.message = `${result.matched_records} record${result.matched_records === 1 ? "" : "s"} contain “${query}”.`;
    ctx.navigate(`#/evidence/${encodeURIComponent(result.view_id)}`);
  } catch (reason) { view.message = reason.message; render(); }
}

async function refreshView(page) {
  view.message = "Opening a fresh view..."; render();
  try {
    const result = await ctx.api("/api/core/views", "POST", { tool: page.source.tool, session: page.source.session_id });
    view.message = "";
    ctx.navigate(`#/evidence/${encodeURIComponent(result.view_id)}`);
  } catch (reason) { view.message = reason.message; render(); }
}

async function loadAssets(page) {
  view.assets = { loading: true }; render();
  try { view.assets = await ctx.api(`/api/core/views/${encodeURIComponent(page.view_id)}/assets`); }
  catch (reason) { view.assets = { error: reason.message }; }
  render();
}

async function load(id, offset = 0) {
  const request = ++view.request;
  if (offset === 0) { view.page = null; view.error = ""; view.contexts.clear(); view.assets = null; render(); }
  try {
    const page = await ctx.api(`/api/core/views/${encodeURIComponent(id)}?offset=${offset}&limit=24`);
    if (request !== view.request) return;
    view.page = page; view.error = "";
  } catch (reason) {
    if (request !== view.request) return;
    view.error = reason.message;
  }
  render();
}

function render() {
  if (!root) return;
  if (!view.id) { root.replaceChildren(emptyState("No view open", "Open a session from Sessions, then choose Open evidence.")); return; }
  if (view.error) { root.replaceChildren(errorBox(view.error), el("p", {}, el("a", { text: "Back to Sessions", attrs: { href: "#/sessions" } }))); return; }
  const page = view.page;
  if (!page) { root.replaceChildren(loading("Reading the pinned view...")); return; }
  root.replaceChildren(el("div", { class: "grid2" },
    el("div", {}, header(page), searchForm(page), records(page),
      pager({ offset: page.offset, shown: page.records.length, total: page.matched_records, nextOffset: page.next_offset, label: "Records",
        onNext: () => load(page.view_id, page.next_offset), onPrevious: () => load(page.view_id, Math.max(0, page.offset - 24)) })),
    el("aside", { class: "stack tray" }, tray(), assetsCard(page),
      el("p", { class: "quiet", text: `${toolNames[page.source?.tool] || ""} session ${page.source?.session_id || ""}` }))));
}

export default {
  mount(element, context) {
    root = element; ctx = context;
    ctx.bus.on("status", () => { if (!selection.size) loadSelection(); });
    ctx.bus.on("workspace-changed", () => { loadSelection(); view.id = ""; view.page = null; });
  },
  show(params) {
    if (!selection.size) loadSelection();
    const id = params[0] || "";
    if (id !== view.id || !view.page) { view.id = id; if (id) load(id); else render(); }
    else render();
  },
  hide() { view.message = ""; },
};

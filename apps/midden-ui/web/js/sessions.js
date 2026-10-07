import { el, button, toolBadge, toolNames, chip, formatBytes, formatDate, relativeTime, formatCount, emptyState, errorBox, loading, copyButton, pager } from "/js/components.js";

const RISK = 300 * 1048576;
let root, ctx;
const view = {
  filters: { tool: "", days: "7", workspace: "", all: false },
  query: "", offset: 0, inventory: null, hits: null, selected: null, detail: null, request: 0,
};

function keyOf(session) { return `${session.tool}/${session.id}`; }

function banner() {
  const configured = ctx.status()?.model?.configured === true;
  if (configured) return null;
  return el("div", { class: "banner" }, el("span", { attrs: { "aria-hidden": "true" }, text: "ⓘ" }),
    el("span", {}, el("strong", { text: "No model connected. " }), "Browsing, reading and collecting work without a model."),
    el("span", { class: "spacer" }), el("a", { class: "button-link", text: "Connect a model", attrs: { href: "#/models" } }));
}

function filterForm() {
  const tool = el("select", { attrs: { "aria-label": "Tool" } },
    el("option", { text: "All tools", attrs: { value: "" } }),
    ...Object.entries(toolNames).map(([value, label]) => el("option", { text: label, attrs: { value } })));
  tool.value = view.filters.tool;
  const days = el("select", { attrs: { "aria-label": "Updated" } },
    el("option", { text: "Last 7 days", attrs: { value: "7" } }), el("option", { text: "Last 30 days", attrs: { value: "30" } }),
    el("option", { text: "Any time", attrs: { value: "0" } }));
  days.value = view.filters.days;
  const workspace = el("input", { attrs: { type: "search", placeholder: "e.g. orbit", "aria-label": "Workspace contains", size: "12" } });
  workspace.value = view.filters.workspace;
  const all = el("input", { attrs: { type: "checkbox" } }); all.checked = view.filters.all;
  const apply = () => {
    view.filters = { tool: tool.value, days: days.value, workspace: workspace.value.trim(), all: all.checked };
    view.offset = 0; view.query = ""; view.hits = null; load();
  };
  [tool, days, all].forEach(input => input.addEventListener("change", apply));
  const form = el("form", { class: "filters", on: { submit: event => { event.preventDefault(); apply(); } } },
    el("label", { class: "field" }, el("span", { text: "Tool" }), tool),
    el("label", { class: "field" }, el("span", { text: "Updated" }), days),
    el("label", { class: "field" }, el("span", { text: "Workspace contains" }), workspace),
    el("label", { class: "check" }, all, " Include automated"),
    button("Apply", { small: true, type: "submit" }));
  const query = el("input", { attrs: { type: "search", placeholder: "e.g. rollback", "aria-label": "Find text across sessions", size: "16" } });
  query.value = view.query;
  const find = el("form", { class: "filters find", on: { submit: event => {
    event.preventDefault(); view.query = query.value.trim(); view.offset = 0;
    if (view.query) runFind(); else { view.hits = null; load(); }
  } } },
    el("label", { class: "field grow" }, el("span", { text: "Find text across sessions" }), query),
    button("Find", { type: "submit" }),
    view.query ? button("Clear", { small: true, link: true, onClick: () => { view.query = ""; view.hits = null; load(); } }) : null);
  return [form, find, el("p", { class: "quiet", text: "Text find covers Copilot CLI and Claude Code transcripts. OpenCode sessions aren't text-searched." })];
}

function sessionRows(sessions) {
  const rows = sessions.map(session => {
    const size = formatBytes(session.bytes) || (session.tool === "opencode" ? "on request" : "");
    const row = el("tr", { class: view.selected === keyOf(session) ? "sel" : "", attrs: { tabindex: "0", "aria-selected": String(view.selected === keyOf(session)) } },
      el("td", {}, toolBadge(session.tool)),
      el("td", {}, el("span", { text: session.title || "Untitled session" }), session.live ? el("span", { class: "live", attrs: { title: "Live", "aria-label": "live" } }) : null),
      el("td", { class: "mono", text: session.dir || "" }),
      el("td", { text: relativeTime(session.updated), attrs: { title: formatDate(session.updated) } }),
      el("td", { class: "num", text: Number.isFinite(session.turns) ? String(session.turns) : "" }),
      el("td", { class: "num" }, el("span", { class: size === "on request" ? "quiet" : "", text: size }), session.bytes >= RISK ? el("span", { class: "flag", text: "watch" }) : null));
    const open = () => ctx.navigate(`#/sessions/${encodeURIComponent(session.tool)}/${encodeURIComponent(session.id)}`);
    row.addEventListener("click", open);
    row.addEventListener("keydown", event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); open(); } });
    return row;
  });
  return el("table", { class: "sessions" },
    el("thead", {}, el("tr", {}, ...["Tool", "Title", "Workspace", "Updated", "Turns", "Size"].map((label, index) => el("th", { class: index >= 4 ? "num" : "", text: label })))),
    el("tbody", {}, ...rows));
}

function results() {
  if (view.hits) {
    const hits = view.hits.hits || [];
    const box = el("div", { class: "hits" }, el("p", { class: "quiet", text: `${hits.length} session${hits.length === 1 ? "" : "s"} matched “${view.query}”. Scanned ${view.hits.scanned}${view.hits.truncated ? ", stopped early" : ""}.` }));
    if (!hits.length) box.append(emptyState("No matches", "Try other words or a wider time range."));
    for (const hit of hits) {
      const session = hit.session;
      box.append(el("button", { class: "hit", attrs: { type: "button" }, on: { click: () => ctx.navigate(`#/sessions/${encodeURIComponent(session.tool)}/${encodeURIComponent(session.id)}`) } },
        el("span", { class: "row" }, toolBadge(session.tool), el("strong", { text: session.title || "Untitled session" }), el("span", { class: "quiet", text: `${hit.matches} match${hit.matches === 1 ? "" : "es"} · ${relativeTime(session.updated)}` })),
        el("span", { class: "mono excerpt", text: hit.excerpt || "" })));
    }
    return box;
  }
  if (!view.inventory) return loading("Reading session stores...");
  const inventory = view.inventory;
  const hidden = !view.filters.all && inventory.excluded_noise > 0 ?
    el("p", { class: "quiet" }, `${inventory.excluded_noise} automated session${inventory.excluded_noise === 1 ? "" : "s"} hidden. `,
      button("Show them", { small: true, link: true, onClick: () => { view.filters.all = true; view.offset = 0; load(); } })) : null;
  if (!inventory.sessions.length) return el("div", {}, emptyState("No sessions in this range", "Widen the time range or include automated sessions."), hidden);
  return el("div", {}, sessionRows(inventory.sessions), hidden,
    pager({ offset: inventory.offset, shown: inventory.sessions.length, total: inventory.total, nextOffset: inventory.next_offset, label: "Sessions",
      onNext: () => { view.offset = inventory.next_offset; load(); },
      onPrevious: () => { view.offset = Math.max(0, view.offset - 50); load(); } }),
    inventory.warnings?.length ? el("ul", { class: "warnings" }, ...inventory.warnings.map(text => el("li", { class: "quiet", text }))) : null);
}

function detailPanel() {
  const detail = view.detail;
  if (!detail) return el("aside", { class: "stack detail" }, emptyState("Pick a session", "Its facts, usage and a brief appear here. Nothing here needs a model."));
  if (detail.error) return el("aside", { class: "stack detail" }, errorBox(detail.error));
  const session = detail.session;
  if (!session) return el("aside", { class: "stack detail" }, loading("Loading session..."));
  const facts = el("dl", { class: "facts" },
    el("dt", { text: "Tool" }), el("dd", { text: toolNames[session.tool] || session.tool }, session.live ? el("span", { class: "live" }) : null, session.live ? " live" : ""),
    el("dt", { text: "Session" }), el("dd", { class: "mono" }, session.id, " ", copyButton(session.id)),
    el("dt", { text: "Workspace" }), el("dd", { class: "mono", text: session.dir || "not recorded" }),
    session.repo ? el("dt", { text: "Repository" }) : null, session.repo ? el("dd", { class: "mono", text: session.repo }) : null,
    el("dt", { text: "Updated" }), el("dd", { text: `${formatDate(session.updated)} · started ${formatDate(session.created)}` }),
    el("dt", { text: "Turns" }), el("dd", { text: `${session.turns}${formatBytes(session.bytes) ? ` · transcript ${formatBytes(session.bytes)}` : ""}` }, session.bytes >= RISK ? el("span", { class: "flag", text: "watch" }) : null));
  const open = button("Open evidence", { primary: true, onClick: () => openEvidence(session, open) });
  const resume = button("Copy resume command", { onClick: () => copyResume(session, resume) });
  const ask = button("Ask the assistant about this session", { link: true, onClick: () => {
    ctx.assistant.addContext({ kind: "session", label: `${toolNames[session.tool] || session.tool}: ${session.title || session.id}`,
      text: `${toolNames[session.tool] || session.tool} session ${session.id} (“${session.title || "untitled"}”, workspace ${session.dir || "not recorded"}). Read it with the midden tool before answering.` });
    ctx.assistant.open();
  } });
  const usage = detail.usage;
  const usageCard = el("div", { class: "card" }, el("h2", { text: "Usage" }),
    usage === undefined ? loading("Measuring usage...") : usage?.error ? el("p", { class: "quiet", text: usage.error }) :
      el("div", { class: "kv" },
        el("div", {}, "Model", el("b", { text: usage.model || "not recorded" })),
        el("div", {}, "Input", el("b", { text: formatCount(usage.input_tokens) || "0" })),
        el("div", {}, "Output", el("b", { text: formatCount(usage.output_tokens) || "0" })),
        el("div", {}, "Cache read", el("b", { text: formatCount(usage.cache_read_tokens) || "0" })),
        el("div", {}, "Duration", el("b", { text: usage.duration_ms ? `${Math.round(usage.duration_ms / 60000)} min` : "not recorded" })),
        el("div", {}, "Cost", el("b", { text: Number.isFinite(usage.usd) ? `$${usage.usd.toFixed(2)}` : Number.isFinite(usage.aiu) ? `${usage.aiu} AIU` : "not recorded" }))));
  const brief = detail.brief;
  const briefCard = el("div", { class: "card" }, el("h2", { text: "Brief" }));
  if (brief === undefined) briefCard.append(loading("Reading the transcript..."));
  else if (brief?.error) briefCard.append(el("p", { class: "quiet", text: brief.error }));
  else {
    if (brief.goal) briefCard.append(el("p", { class: "sub", text: "Goal" }), el("p", { class: "quote", text: clip(brief.goal.text, 600) }));
    if (brief.last_assistant) briefCard.append(el("p", { class: "sub", text: "Last answer" }), el("p", { class: "quote", text: clip(brief.last_assistant.text, 600) }));
    if (brief.recent?.length) {
      const recent = el("details", {}, el("summary", { text: `${brief.recent.length} recent turns` }),
        ...brief.recent.map(turn => el("div", { class: "turn" }, el("strong", { text: turn.role === "user" ? "User" : "Assistant" }), el("p", { text: clip(turn.text, 400) }))));
      briefCard.append(recent);
    }
    if (!brief.goal && !brief.last_assistant) briefCard.append(el("p", { class: "quiet", text: "No readable turns." }));
  }
  return el("aside", { class: "stack detail" },
    el("div", { class: "card" }, el("h2", { text: session.title || "Untitled session" }), facts,
      el("div", { class: "row" }, open, resume), ask, detail.message ? el("p", { class: "quiet", text: detail.message, attrs: { role: "status" } }) : null),
    usageCard, briefCard);
}

function clip(text, max) { const value = String(text || ""); return value.length > max ? `${value.slice(0, max)}…` : value; }

async function openEvidence(session, control) {
  control.disabled = true; control.textContent = "Opening...";
  try {
    const page = await ctx.api("/api/core/views", "POST", { tool: session.tool, session: session.id });
    ctx.navigate(`#/evidence/${encodeURIComponent(page.view_id)}`);
  } catch (reason) { view.detail.message = reason.message; render(); }
  finally { control.disabled = false; control.textContent = "Open evidence"; }
}

async function copyResume(session, control) {
  try {
    const data = await ctx.api(`/api/core/resume?id=${encodeURIComponent(session.id)}`);
    await navigator.clipboard.writeText(data.command);
    control.textContent = "Copied";
  } catch (reason) { view.detail.message = `Resume command unavailable: ${reason.message}`; render(); }
  setTimeout(() => { control.textContent = "Copy resume command"; }, 1500);
}

async function load() {
  const request = ++view.request;
  view.inventory = null; render();
  const params = new URLSearchParams({ limit: "50", offset: String(view.offset) });
  if (view.filters.tool) params.set("tool", view.filters.tool);
  if (view.filters.days !== "0") params.set("days", view.filters.days);
  if (view.filters.workspace) params.set("workspace", view.filters.workspace);
  if (view.filters.all) params.set("all", "1");
  try {
    const inventory = await ctx.api(`/api/core/sessions?${params}`);
    if (request !== view.request) return;
    view.inventory = inventory;
  } catch (reason) {
    if (request !== view.request) return;
    view.inventory = { sessions: [], warnings: [reason.message], offset: 0, total: 0 };
  }
  render();
}

async function runFind() {
  const request = ++view.request;
  view.hits = null; view.inventory = null; render();
  const params = new URLSearchParams({ q: view.query, limit: "15" });
  if (view.filters.tool) params.set("tool", view.filters.tool);
  if (view.filters.days !== "0") params.set("days", view.filters.days);
  try {
    const hits = await ctx.api(`/api/core/find?${params}`);
    if (request !== view.request) return;
    view.hits = hits;
  } catch (reason) {
    if (request !== view.request) return;
    view.hits = { hits: [], scanned: 0, truncated: false };
    ctx.notify(reason);
  }
  render();
}

async function select(tool, id) {
  const key = `${tool}/${id}`;
  if (view.selected === key && view.detail) { render(); return; }
  view.selected = key;
  const detail = view.detail = { session: null, usage: undefined, brief: undefined };
  render();
  const query = `tool=${encodeURIComponent(tool)}&id=${encodeURIComponent(id)}`;
  try { detail.session = await ctx.api(`/api/core/show?${query}`); }
  catch (reason) { detail.error = reason.message; }
  if (view.detail === detail) render();
  if (detail.error) return;
  ctx.api(`/api/core/usage?${query}`).then(data => { detail.usage = data; }, reason => { detail.usage = { error: `Usage not available: ${reason.message}` }; })
    .finally(() => { if (view.detail === detail) render(); });
  ctx.api(`/api/core/brief?${query}`).then(data => { detail.brief = data; }, reason => { detail.brief = { error: `Brief not available: ${reason.message}` }; })
    .finally(() => { if (view.detail === detail) render(); });
}

function render() {
  if (!root) return;
  root.replaceChildren(banner() || "",
    el("div", { class: "grid2" },
      el("div", {}, el("div", { class: "view-heading" }, el("h1", { text: "Sessions" }),
        el("p", { class: "sub", text: "Recorded work from Copilot CLI, Claude Code and OpenCode on this computer. Read-only." })),
        ...filterForm(), results()),
      detailPanel()));
}

export default {
  mount(element, context) {
    root = element; ctx = context;
    ctx.bus.on("status", () => { if (!root.hidden) render(); });
    ctx.bus.on("workspace-changed", () => { view.inventory = null; view.detail = null; view.selected = null; });
  },
  show(params) {
    if (!view.inventory && !view.hits) load(); else render();
    if (params.length >= 2) select(params[0], params[1]);
  },
  hide() {},
};

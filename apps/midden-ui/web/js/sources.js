import { el, button, chip, emptyState, errorBox, loading, copyButton, shortId, recordRow, pager, dialog, field } from "/js/components.js";

let root, ctx;
const view = { collections: null, error: "", focus: "", open: new Map(), verify: new Map(), message: "", request: 0 };
const AUTO_VERIFY_LIMIT = 500;
const OUTCOMES = [
  ["Investigation", "What's worth explaining in these sources? Give grounded opportunities with record references and the gaps that remain."],
  ["Article", "Write an article from these sources. Ask me about the audience and length first if they're unclear."],
  ["Presentation", "Make a short presentation from these sources, with editable slides and a self-contained HTML copy."],
  ["Long-form", "Continue the long-form work using these sources, keeping the existing chapters consistent."],
];

function contextFor(info) {
  const status = view.verify.get(info.path);
  const integrity = status?.result ? (status.result.valid ? "integrity verified" : "integrity problems reported") : "integrity not checked";
  return { kind: "collection", label: info.path,
    text: `Collection ${info.path} (${info.record_count} records from ${info.source_count} source session${info.source_count === 1 ? "" : "s"}, ${integrity}). Read it with midden collection read/search ${info.path}.` };
}

function badge(info) {
  const status = view.verify.get(info.path);
  if (!status) return chip("Not checked");
  if (status.loading) return chip("Checking...");
  if (status.error) return chip("Check failed", "warn");
  return status.result.valid ? chip("✓ Integrity verified", "ok") : chip("Integrity problems", "warn");
}

function card(info) {
  const state = view.open.get(info.path);
  const status = view.verify.get(info.path);
  const element = el("div", { class: `card collection${view.focus === info.path ? " focus" : ""}`, dataset: { path: info.path } },
    el("div", { class: "row" }, el("h2", { class: "mono", text: info.path }), badge(info)),
    el("p", { class: "sub" }, `${info.record_count} record${info.record_count === 1 ? "" : "s"} · ${info.source_count} source session${info.source_count === 1 ? "" : "s"} · ${info.asset_count ? `${info.asset_count} asset${info.asset_count === 1 ? "" : "s"}` : "no assets"} · `,
      el("span", { class: "mono", text: shortId(info.records_digest, 10) }), copyButton(info.records_digest)),
    status?.result && !status.result.valid ? el("ul", { class: "warnings" }, ...status.result.problems.map(problem => el("li", { class: "error", text: problem }))) : null,
    status?.error ? errorBox(status.error) : null,
    info.warnings?.length ? el("ul", { class: "warnings" }, ...info.warnings.map(text => el("li", { class: "quiet", text }))) : null,
    el("div", { class: "row" },
      button(state?.mode === "read" ? "Hide records" : "Read", { small: true, onClick: () => state?.mode === "read" ? (view.open.delete(info.path), render()) : readRecords(info, 0) }),
      button("Search", { small: true, onClick: () => { view.open.set(info.path, { mode: "search", query: "", page: null }); render(); } }),
      button("Verify", { small: true, onClick: () => verify(info) }),
      button("Export…", { small: true, onClick: () => exportDialog(info) }),
      view.collections.length > 1 ? button("Merge…", { small: true, onClick: () => mergeDialog(info) }) : null,
      button("Use in chat", { small: true, link: true, onClick: () => { ctx.assistant.addContext(contextFor(info)); ctx.assistant.open(); } })),
    el("div", { class: "row starters" }, el("span", { class: "sub", text: "Start an outcome from this collection:" }),
      ...OUTCOMES.map(([label, prompt]) => button(label, { small: true, onClick: () => { ctx.assistant.addContext(contextFor(info)); ctx.assistant.prefill(prompt); ctx.assistant.open(); } }))));
  if (state) element.append(panel(info, state));
  return element;
}

function panel(info, state) {
  const box = el("div", { class: "collection-panel" });
  if (state.mode === "search") {
    const input = el("input", { attrs: { type: "search", placeholder: "Text to find in this collection", "aria-label": `Search ${info.path}` } });
    input.value = state.query || "";
    box.append(el("form", { class: "filters", on: { submit: event => { event.preventDefault(); searchRecords(info, input.value.trim(), 0); } } },
      el("label", { class: "field grow" }, el("span", { text: "Search this collection" }), input), button("Search", { primary: true, type: "submit" }),
      button("Close", { small: true, link: true, onClick: () => { view.open.delete(info.path); render(); } })));
  }
  if (state.loading) box.append(loading("Reading the collection..."));
  else if (state.error) box.append(errorBox(state.error));
  else if (state.page) {
    if (!state.page.records.length) box.append(emptyState("No records", state.mode === "search" ? "Nothing in this collection contains that text." : "This collection has no records."));
    box.append(...state.page.records.map(record => recordRow(record)));
    box.append(pager({ offset: state.page.offset, shown: state.page.records.length, total: state.page.total, nextOffset: state.page.next_offset, label: "Records",
      onNext: () => state.mode === "search" ? searchRecords(info, state.query, state.page.next_offset) : readRecords(info, state.page.next_offset),
      onPrevious: () => state.mode === "search" ? searchRecords(info, state.query, Math.max(0, state.page.offset - 10)) : readRecords(info, Math.max(0, state.page.offset - 10)) }));
  }
  return box;
}

async function readRecords(info, offset) {
  const state = { mode: "read", loading: true };
  view.open.set(info.path, state); render();
  try { state.page = await ctx.api(`/api/core/collection/records?path=${encodeURIComponent(info.path)}&offset=${offset}&limit=10`); }
  catch (reason) { state.error = reason.message; }
  state.loading = false; render();
}

async function searchRecords(info, query, offset) {
  const state = { mode: "search", query, loading: true };
  view.open.set(info.path, state); render();
  if (!query) { state.loading = false; state.error = "Enter text to search for."; render(); return; }
  try { state.page = await ctx.api("/api/core/collection/search", "POST", { path: info.path, query, offset, limit: 10 }); }
  catch (reason) { state.error = reason.message; }
  state.loading = false; render();
}

async function verify(info) {
  const status = { loading: true };
  view.verify.set(info.path, status); render();
  try { status.result = await ctx.api("/api/core/collection/verify", "POST", { path: info.path }); }
  catch (reason) { status.error = reason.message; }
  status.loading = false; render();
}

function exportDialog(info) {
  const base = info.path.split("/").pop() || "collection";
  const format = el("select", {}, el("option", { text: "Folder copy (with assets)", attrs: { value: "directory" } }),
    el("option", { text: "JSON Lines", attrs: { value: "jsonl" } }), el("option", { text: "Markdown", attrs: { value: "markdown" } }));
  const destination = el("input", { class: "mono", attrs: { value: `exports/${base}`, spellcheck: "false" } });
  const suffix = { directory: "", jsonl: ".jsonl", markdown: ".md" };
  format.addEventListener("change", () => { destination.value = `exports/${base}${suffix[format.value]}`; });
  const status = el("p", { class: "quiet", attrs: { role: "status" } });
  const modal = dialog(`Export ${info.path}`);
  modal.body.append(field("Format", format, "JSON Lines and Markdown are only available for collections without assets."),
    field("New file or folder in the workspace", destination, "Never overwrites an existing file."), status);
  const run = button("Export", { primary: true, onClick: async () => {
    run.disabled = true; status.textContent = "Exporting...";
    try {
      const result = await ctx.api("/api/core/collection/export", "POST", { path: info.path, out: destination.value.trim().replace(/\\/g, "/"), format: format.value });
      modal.close(); view.message = `Exported to ${result.path}.`; ctx.announce(view.message); render();
    } catch (reason) { status.textContent = reason.message; run.disabled = false; }
  } });
  modal.actions.append(button("Cancel", { onClick: () => modal.close() }), run);
  modal.open();
}

function mergeDialog(info) {
  const others = view.collections.filter(item => item.path !== info.path);
  const boxes = others.map(item => { const box = el("input", { attrs: { type: "checkbox" } }); return [item, box]; });
  const destination = el("input", { class: "mono", attrs: { value: `${info.path}-merged`, spellcheck: "false" } });
  const status = el("p", { class: "quiet", attrs: { role: "status" } });
  const modal = dialog(`Merge into a new collection`);
  modal.body.append(el("p", { class: "sub", text: `Starts from ${info.path}. Add:` }),
    ...boxes.map(([item, box]) => el("label", { class: "check" }, box, ` ${item.path} (${item.record_count} records)`)),
    field("New collection folder", destination, "Inputs stay unchanged."), status);
  const run = button("Merge", { primary: true, onClick: async () => {
    const paths = [info.path, ...boxes.filter(([, box]) => box.checked).map(([item]) => item.path)];
    if (paths.length < 2) { status.textContent = "Choose at least one more collection."; return; }
    run.disabled = true; status.textContent = "Merging...";
    try {
      const result = await ctx.api("/api/core/collection/merge", "POST", { paths, out: destination.value.trim().replace(/\\/g, "/") });
      modal.close(); view.message = `Merged ${paths.length} collections into ${result.path} (${result.record_count} records).`; ctx.announce(view.message);
      view.focus = result.path; load();
    } catch (reason) { status.textContent = reason.message; run.disabled = false; }
  } });
  modal.actions.append(button("Cancel", { onClick: () => modal.close() }), run);
  modal.open();
}

async function load() {
  const request = ++view.request;
  view.collections = null; view.error = ""; render();
  try {
    const data = await ctx.api("/api/core/collections");
    if (request !== view.request) return;
    view.collections = data.collections || [];
    for (const info of view.collections) if (!view.verify.has(info.path) && info.record_count <= AUTO_VERIFY_LIMIT) verify(info);
  } catch (reason) {
    if (request !== view.request) return;
    view.error = reason.message; view.collections = [];
  }
  render();
  if (view.focus) requestAnimationFrame(() => root.querySelector(`[data-path="${CSS.escape(view.focus)}"]`)?.scrollIntoView({ block: "nearest" }));
}

function render() {
  if (!root) return;
  const heading = el("div", { class: "view-heading" }, el("h1", { text: "Sources" }),
    el("p", { class: "sub" }, "Portable collections in this workspace. Each one is ordinary files: ", el("span", { class: "mono", text: "manifest.json" }), ", ",
      el("span", { class: "mono", text: "records.jsonl" }), ", ", el("span", { class: "mono", text: "assets/" }), "."));
  const body = [heading, el("div", { class: "row" }, button("Refresh", { small: true, onClick: load }))];
  if (view.message) body.push(el("p", { class: "quiet", text: view.message, attrs: { role: "status" } }));
  if (view.error) body.push(errorBox(view.error));
  if (view.collections === null) body.push(loading("Looking for collections..."));
  else if (!view.collections.length && !view.error) body.push(emptyState("No collections yet", "Select records in Evidence and choose “Collect into workspace”."));
  else body.push(el("div", { class: "stack" }, ...view.collections.map(card)));
  root.replaceChildren(...body);
}

export default {
  mount(element, context) {
    root = element; ctx = context;
    ctx.bus.on("files-changed", () => { if (!root.hidden) load(); else view.collections = null; });
    ctx.bus.on("workspace-changed", () => { view.collections = null; view.verify.clear(); view.open.clear(); });
  },
  show(params) {
    view.focus = params[0] || "";
    if (view.collections === null || view.focus && !view.collections.some(info => info.path === view.focus)) load();
    else render();
  },
  hide() { view.message = ""; },
};

// Shared, CSP-safe DOM builders. Text always goes through textContent.

export const toolNames = { copilot: "Copilot CLI", claude: "Claude Code", opencode: "OpenCode" };
const toolShort = { copilot: "CP", claude: "CC", opencode: "OC" };

export function el(tag, options = {}, ...children) {
  const element = document.createElement(tag);
  if (options.class) element.className = options.class;
  if (options.text !== undefined && options.text !== null) element.textContent = String(options.text);
  for (const [name, value] of Object.entries(options.attrs || {})) {
    if (value === false || value === null || value === undefined) continue;
    element.setAttribute(name, value === true ? "" : String(value));
  }
  for (const [name, value] of Object.entries(options.dataset || {})) element.dataset[name] = String(value);
  for (const [name, handler] of Object.entries(options.on || {})) element.addEventListener(name, handler);
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    element.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return element;
}

export function button(label, { primary = false, small = false, link = false, onClick, disabled = false, title, type = "button" } = {}) {
  const classes = [primary && "primary", small && "compact", link && "link"].filter(Boolean).join(" ");
  const element = el("button", { class: classes, text: label, attrs: { type, title, disabled } });
  if (onClick) element.addEventListener("click", onClick);
  return element;
}

export function toolBadge(tool) {
  return el("span", { class: `tool-badge ${toolShort[tool]?.toLowerCase() || "unknown"}`, text: toolShort[tool] || "?", attrs: { title: toolNames[tool] || tool, "aria-label": toolNames[tool] || tool } });
}

export function chip(text, kind = "") {
  return el("span", { class: `chip ${kind}`.trim(), text });
}

export function formatBytes(value) {
  if (!Number.isFinite(value) || value <= 0) return "";
  if (value < 1024) return `${value} B`;
  if (value < 1048576) return `${(value / 1024).toFixed(1)} KB`;
  if (value < 1073741824) return `${(value / 1048576).toFixed(1)} MB`;
  return `${(value / 1073741824).toFixed(1)} GB`;
}

export function formatDate(value) {
  return value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : "";
}

export function relativeTime(value) {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) return "";
  const minutes = Math.round((Date.now() - time) / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} h ago`;
  const days = Math.round(hours / 24);
  if (days === 1) return "yesterday";
  if (days < 30) return `${days} days ago`;
  return new Date(time).toLocaleDateString();
}

export function formatCount(value) {
  if (!Number.isFinite(value)) return "";
  if (value >= 1e6) return `${(value / 1e6).toFixed(value >= 1e7 ? 0 : 2)} M`;
  if (value >= 1e3) return `${(value / 1e3).toFixed(value >= 1e4 ? 0 : 1)} k`;
  return String(value);
}

export function emptyState(title, text = "") {
  return el("div", { class: "empty-state" }, el("strong", { text: title }), text ? el("p", { class: "quiet", text }) : null);
}

export function errorBox(message) {
  return el("p", { class: "error", text: message instanceof Error ? message.message : String(message), attrs: { role: "alert" } });
}

export function loading(text = "Loading...") {
  return el("p", { class: "quiet loading", text, attrs: { role: "status" } });
}

export function field(label, input, hint = "") {
  return el("label", { class: "field" }, el("span", { text: label }), input, hint ? el("small", { class: "quiet", text: hint }) : null);
}

export function copyButton(text, label = "Copy") {
  const element = button(label, { small: true, link: true });
  element.addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(text); element.textContent = "Copied"; }
    catch { element.textContent = "Copy failed"; }
    setTimeout(() => { element.textContent = label; }, 1500);
  });
  return element;
}

export function shortId(value, keep = 8) {
  const text = String(value || "");
  return text.length > keep * 2 + 1 ? `${text.slice(0, keep)}…${text.slice(-keep)}` : text;
}

// A modal built on <dialog>. Returns controls; callers wire their own actions.
export function dialog(title, { onClose } = {}) {
  const heading = el("h2", { text: title });
  const body = el("div", { class: "dialog-body" });
  const actions = el("div", { class: "dialog-actions" });
  const element = el("dialog", { class: "app-dialog", attrs: { "aria-label": title } }, el("div", { class: "dialog-heading" }, heading), body, actions);
  element.addEventListener("close", () => { onClose?.(); element.remove(); });
  document.body.append(element);
  return { element, body, actions, heading, open: () => element.showModal(), close: () => element.close() };
}

export function recordRow(record, { selected = false, onToggle, onContext, contextLabel = "± context" } = {}) {
  const index = record.reference?.record_index;
  const meta = el("div", { class: "meta" },
    el("span", { text: Number.isFinite(index) ? `#${index}` : "" }),
    el("span", { class: "kind", text: record.role || record.kind || "record" }),
    record.kind && record.kind !== record.role && record.kind !== "message" ? el("span", { class: "quiet", text: record.kind }) : null,
    el("span", { text: record.time ? new Date(record.time).toLocaleTimeString() : "" }),
    record.clipped ? el("span", { class: "flag", text: "clipped" }) : null,
    record.redacted ? el("span", { class: "flag", text: "redacted" }) : null);
  const body = el("div", { class: "rec-body" }, meta, el("p", { class: "rec-text", text: record.text || "" }), el("div", { class: "rid mono", text: record.id }));
  const checkbox = onToggle ? el("input", { attrs: { type: "checkbox", "aria-label": `Select record ${Number.isFinite(index) ? index : record.id}` } }) : null;
  if (checkbox) { checkbox.checked = selected; checkbox.addEventListener("change", () => onToggle(checkbox.checked)); }
  const row = el("div", { class: `rec${selected ? " selected" : ""}`, dataset: { record: record.id } }, checkbox || el("span"), body);
  if (onContext) row.append(button(contextLabel, { small: true, onClick: onContext }));
  else row.append(el("span"));
  return row;
}

export function pager({ offset = 0, shown = 0, total, nextOffset, onNext, onPrevious, label = "items" }) {
  const from = shown ? offset + 1 : 0, to = offset + shown;
  const text = Number.isFinite(total) ? `${label} ${from}–${to} of ${total}` : `${label} ${from}–${to}`;
  return el("div", { class: "pager" }, el("span", { class: "quiet", text }), el("span", { class: "spacer" }),
    onPrevious && offset > 0 ? button("Previous", { small: true, onClick: onPrevious }) : null,
    onNext && Number.isFinite(nextOffset) ? button("Next", { small: true, onClick: onNext }) : null);
}

import { el, button, field } from "/js/components.js";

// Checks that a quote in a draft really appears in a collected source record.
export function quoteCheck(root, ctx) {
  const collection = el("select", { attrs: { "aria-label": "Collection" } }, el("option", { text: "Open to load collections", attrs: { value: "" } }));
  const record = el("input", { class: "mono", attrs: { placeholder: "Record id, e.g. claude:…:63@0-38", spellcheck: "false" } });
  const quote = el("textarea", { attrs: { rows: "2", placeholder: "The exact words you quote" } });
  const result = el("p", { attrs: { role: "status" } });
  let loaded = false;
  async function loadCollections() {
    if (loaded) return;
    loaded = true;
    try {
      const data = await ctx.api("/api/core/collections");
      const items = data.collections || [];
      collection.replaceChildren(...(items.length ? items.map(info => el("option", { text: `${info.path} (${info.record_count})`, attrs: { value: info.path } })) :
        [el("option", { text: "No collections in this workspace", attrs: { value: "" } })]));
    } catch (reason) { loaded = false; result.textContent = reason.message; }
  }
  const check = button("Check", { onClick: async () => {
    if (!collection.value || !record.value.trim() || !quote.value.trim()) { result.textContent = "Choose a collection, a record id and the quoted text."; return; }
    check.disabled = true; result.textContent = "Checking...";
    try {
      const match = await ctx.api("/api/core/collection/verify", "POST", { path: collection.value, record: record.value.trim(), quote: quote.value });
      result.replaceChildren(el("span", { class: match.matched ? "okt" : "warnt", text: match.matched ? "✓ Matched" : "✗ Not found in that record" }), " ",
        el("span", { class: "quiet", text: match.meaning || "Text correspondence only, not a truth check." }));
    } catch (reason) { result.textContent = reason.message; }
    finally { check.disabled = false; }
  } });
  const details = el("details", { class: "card quote-check" }, el("summary", { text: "Check a quote against a source record" }),
    field("Collection", collection), field("Record", record), field("Quote", quote), el("div", { class: "row" }, check), result);
  details.addEventListener("toggle", () => { if (details.open) loadCollections(); });
  ctx.bus.on("files-changed", () => { loaded = false; if (details.open) loadCollections(); });
  root.replaceChildren(details);
}

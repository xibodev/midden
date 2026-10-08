import { el, button, chip, emptyState, errorBox, loading, dialog, field } from "/js/components.js";

let root, ctx;
const view = { state: null, error: "", busy: "", message: "", free: null, local: null, extension: null, flows: new Map(), route: null };
const FREE_STATUS = { answers_text: ["Answers text", "ok"], connected: ["Lists models, didn't answer", ""], busy: ["Busy (rate limited), try later", "warn"], failed: ["Failed", "warn"] };
const SOURCE = { free: "free", key: "API key", local: "local", extension: "extension" };
// Messages from Midden never get to show back a secret the person just typed.
const redact = (text, secret) => secret ? String(text).split(secret).join("[redacted]") : String(text);

function targets(state) {
  const list = [];
  for (const instance of state.instances) if (instance.state === "enabled") for (const model of instance.models) list.push(`${instance.id}/${model.id}`);
  return list;
}

async function act(label, work, secret = "") {
  view.busy = label; view.message = ""; render();
  try {
    const result = await work();
    if (result?.state) view.state = result.state; else await reload(false);
    ctx.refreshStatus().catch(() => {});
    return result;
  } catch (reason) { view.message = redact(reason.message, secret); return null; }
  finally { view.busy = ""; render(); }
}

async function reload(draw = true) {
  try { const data = await ctx.api("/api/models/state"); view.state = data?.state || data; view.error = ""; }
  catch (reason) { view.error = reason.message; }
  if (draw) render();
}

function defaultSection(state) {
  const choices = [...state.routes.map(route => ({ value: route.name, label: `Route “${route.name}” · ${route.targets.length} model${route.targets.length === 1 ? "" : "s"}` })),
    ...targets(state).map(target => ({ value: target, label: target }))];
  const select = el("select", { attrs: { "aria-label": "Default model" } },
    el("option", { text: choices.length ? "Choose the model the assistant uses" : "Connect a model first", attrs: { value: "" } }),
    ...choices.map(choice => el("option", { text: choice.label, attrs: { value: choice.value } })));
  select.value = state.defaultModel || "";
  const save = button("Use as default", { primary: true, disabled: !choices.length, onClick: () => act("default", () => ctx.api("/api/models/default", "PUT", { selection: select.value })) });
  return el("div", { class: "card" }, el("h2", { text: "Default model" }),
    el("p", { class: "sub", text: state.configured ? `The assistant uses ${state.defaultModel}.` : "No working default yet. Deterministic work (Sessions, Sources, Files) doesn't need one." }),
    state.setupError ? errorBox(state.setupError) : null,
    el("div", { class: "filters" }, el("label", { class: "field grow" }, el("span", { text: "Default" }), select), save));
}

function checkChip(instance, model) {
  const check = instance.checks?.[model.id];
  if (!check) return chip("Tool calling: not tested");
  return check.status === "tested" ? chip("Tool calling: tested", "ok") : chip("Tool calling: failed", "warn");
}

function instanceCard(instance, state) {
  const models = el("details", {}, el("summary", { text: `${instance.models.length} model${instance.models.length === 1 ? "" : "s"}` }),
    ...instance.models.slice(0, 200).map(model => {
      const target = `${instance.id}/${model.id}`;
      return el("div", { class: "model-row" }, el("span", { class: "mono grow", text: model.displayName ? `${model.displayName} (${model.id})` : model.id }), checkChip(instance, model),
        button("Test tool calling", { small: true, link: true, onClick: () => act("check", async () => {
          const result = await ctx.api(`/api/models/instances/${encodeURIComponent(instance.id)}/check`, "POST", { model: model.id });
          view.message = `${target}: ${result.message}`;
          return null;
        }) }),
        state.defaultModel === target ? chip("default", "ok") : button("Use as default", { small: true, link: true, onClick: () => act("default", () => ctx.api("/api/models/default", "PUT", { selection: target })) }));
    }));
  return el("div", { class: "card instance" },
    el("div", { class: "row" }, el("h2", { text: instance.label || instance.id }), chip(SOURCE[instance.source] || instance.source),
      instance.state === "enabled" ? chip("enabled", "ok") : chip(instance.credentialReady ? "disabled" : "sign-in needed", "warn")),
    el("p", { class: "sub mono", text: `${instance.id} · ${instance.endpoint || "default endpoint"}` }),
    models,
    el("div", { class: "row" },
      button("Refresh models", { small: true, onClick: () => act("sync", () => ctx.api(`/api/models/instances/${encodeURIComponent(instance.id)}/sync`, "POST", {})) }),
      instance.source === "extension" ? null : button("Remove", { small: true, onClick: () => act("remove", () => ctx.api(`/api/models/instances/${encodeURIComponent(instance.id)}`, "DELETE")) })));
}

function freeSection() {
  const outcomes = view.free?.outcomes || [];
  return el("section", { class: "section" }, el("h2", { text: "Free models, no key" }),
    el("div", { class: "row" }, button("Try free models", { primary: true, disabled: !!view.busy, onClick: () => act("free", async () => {
      const result = await ctx.api("/api/models/free", "POST", {});
      view.free = result; return result;
    }) }), el("span", { class: "sub", text: "Checks a few public services. Your prompts go to the service that answers." })),
    outcomes.length ? el("ul", { class: "list" }, ...outcomes.map(outcome => {
      const [label, kind] = FREE_STATUS[outcome.status] || [outcome.status, ""];
      return el("li", {}, el("span", { class: "grow" }, el("strong", { text: outcome.label || outcome.instanceId }), ` · ${outcome.models ? `${outcome.models} model${outcome.models === 1 ? "" : "s"}` : "no models"}`,
        outcome.error ? el("span", { class: "quiet", text: ` · ${outcome.error}` }) : null), chip(label, kind), chip("Tool calling: not tested"));
    })) : null,
    outcomes.length ? el("p", { class: "quiet", text: "“Answers text” means the service replied to a short check. Test tool calling before relying on a free model for outcomes." }) : null);
}

function localSection() {
  const servers = view.local?.servers;
  return el("section", { class: "section" }, el("h2", { text: "On this computer" }),
    el("div", { class: "row" }, button("Find local model servers", { disabled: !!view.busy, onClick: () => act("local", async () => {
      view.local = await ctx.api("/api/models/local", "POST", {}); return null;
    }) }), el("span", { class: "sub", text: "Looks for Ollama, LM Studio, llama.cpp, vLLM, LocalAI and Jan on their usual local ports." })),
    servers ? (servers.length ? el("ul", { class: "list" }, ...servers.map(server => el("li", {},
      el("span", { class: "grow" }, el("strong", { text: server.label }), " at ", el("span", { class: "mono", text: server.endpoint }), ` · ${server.models.length} model${server.models.length === 1 ? "" : "s"}`),
      server.connectedInstanceId ? chip("connected", "ok") : button("Connect", { small: true, onClick: () => act("connect", () => ctx.api("/api/models/instances", "POST", { providerKind: server.providerKind, endpoint: server.endpoint, label: server.label })) })))) :
      el("p", { class: "quiet", text: "No local model server answered." })) : null);
}

function connectDialog(entry) {
  const endpoint = el("input", { class: "mono", attrs: { value: entry.defaultEndpoint || "", placeholder: "https://…", spellcheck: "false" } });
  const key = el("input", { attrs: { type: "password", autocomplete: "new-password", spellcheck: "false" } });
  const label = el("input", { attrs: { placeholder: entry.label } });
  const status = el("p", { class: "quiet", attrs: { role: "status" } });
  const modal = dialog(`Connect ${entry.label}`, { onClose: () => { key.value = ""; } });
  modal.body.append(field(entry.requiresBaseUrl ? "Address" : "Address (optional)", endpoint),
    field(entry.requiresApiKey ? "API key" : "API key (optional)", key, "Saved in the App's data folder (kernel/auth.json) and never shown again."),
    field("Name (optional)", label), status);
  const save = button("Connect", { primary: true, onClick: async () => {
    if (entry.requiresBaseUrl && !endpoint.value.trim()) { status.textContent = "Enter the address."; return; }
    if (entry.requiresApiKey && !key.value) { status.textContent = "Enter the API key."; return; }
    save.disabled = true; status.textContent = "Connecting and loading models...";
    const body = { providerKind: entry.id };
    if (endpoint.value.trim() && endpoint.value.trim() !== entry.defaultEndpoint) body.endpoint = endpoint.value.trim();
    if (key.value) body.apiKey = key.value;
    if (label.value.trim()) body.label = label.value.trim();
    try {
      const result = await ctx.api("/api/models/instances", "POST", body);
      key.value = ""; modal.close();
      if (result?.state) view.state = result.state; else await reload(false);
      view.message = `Connected ${entry.label}.`; ctx.refreshStatus().catch(() => {}); render();
    } catch (reason) { status.textContent = redact(reason.message, body.apiKey); save.disabled = false; }
  } });
  modal.actions.append(button("Cancel", { onClick: () => modal.close() }), save);
  modal.open(); (entry.requiresBaseUrl ? endpoint : key).focus();
}

function keySection(state) {
  const entries = state.roster.filter(entry => !entry.keyless);
  return el("section", { class: "section" }, el("h2", { text: "With an API key or address" }),
    el("div", { class: "prov" }, ...entries.map(entry => {
      const connected = state.instances.some(instance => instance.providerKind === entry.id);
      return el("div", { class: "card prov-card" }, el("span", { text: entry.label }),
        connected ? chip("connected", "ok") : null, button("Connect", { small: true, onClick: () => connectDialog(entry) }));
    })));
}

function extensionSection(state) {
  const url = el("input", { class: "mono", attrs: { value: state.extension.url || "http://127.0.0.1:18888", spellcheck: "false", "aria-label": "Service address" } });
  const secret = el("input", { attrs: { type: "password", autocomplete: "new-password", placeholder: state.extension.connected ? "Unchanged" : "If the service needs one", "aria-label": "Secret" } });
  const providers = view.extension?.providers || [];
  const connect = button(state.extension.connected ? "Reconnect" : "Connect", { onClick: () => act("extension", async () => {
    const body = { url: url.value.trim() };
    if (secret.value) body.secret = secret.value;
    const result = await ctx.api("/api/models/extension", "PUT", body);
    secret.value = ""; view.extension = result; return result;
  }, secret.value) });
  return el("section", { class: "section" }, el("h2", { text: "From an extension service" }),
    el("p", { class: "sub", text: "Connect a separately running service that offers more model providers." }),
    el("div", { class: "filters" }, el("label", { class: "field" }, el("span", { text: "Service address" }), url),
      el("label", { class: "field" }, el("span", { text: "Secret" }), secret), connect,
      state.extension.connected ? button("Disconnect", { small: true, link: true, onClick: () => act("extension", async () => {
        const result = await ctx.api("/api/models/extension", "DELETE"); view.extension = null; return result;
      }) }) : null),
    providers.length ? el("ul", { class: "list" }, ...providers.map(provider => extensionProvider(provider))) :
      state.extension.connected ? el("p", { class: "quiet", text: "Connected. Use Reconnect to list its providers again." }) : null);
}

function extensionProvider(provider) {
  const flow = view.flows.get(provider.instanceId);
  const item = el("li", { class: "ext" }, el("span", { class: "grow" }, el("strong", { text: provider.name || provider.provider })), provider.ready ? chip("Ready", "ok") : chip("Sign-in needed", "warn"));
  if (provider.ready) return item;
  if (provider.credential === "token") {
    const token = el("input", { attrs: { type: "password", autocomplete: "new-password", placeholder: "Token", "aria-label": `Token for ${provider.name}` } });
    item.append(token, button("Save token", { small: true, onClick: () => act("token", async () => {
      const result = await ctx.api(`/api/models/extension/${encodeURIComponent(provider.instanceId)}/token`, "POST", { token: token.value });
      token.value = ""; provider.ready = true; return result;
    }, token.value) }));
  } else if (provider.credential === "oauth") {
    if (!flow) for (const method of provider.signInMethods || []) item.append(button(method === "device" ? "Sign in (device code)" : "Sign in (paste code)", { small: true, onClick: () => startSignIn(provider, method) }));
    else item.append(signInPanel(provider, flow));
  }
  return item;
}

function webLink(address, text) {
  // Defense in depth: midden-ui already refuses non-web sign-in addresses.
  return /^https?:\/\//i.test(address || "") ? el("a", { text, attrs: { href: address, target: "_blank", rel: "noreferrer noopener" } }) : el("span", { class: "mono", text: address || "" });
}

function signInPanel(provider, flow) {
  const box = el("div", { class: "signin" });
  if (flow.error) box.append(errorBox(flow.error));
  if (flow.userCode) box.append(el("p", {}, "Open ", webLink(flow.verificationUri, flow.verificationUri), " and enter ", el("strong", { class: "mono", text: flow.userCode })),
    el("p", { class: "quiet", text: "Waiting for the sign-in to finish..." }));
  if (flow.authorizationUrl) {
    const code = el("input", { attrs: { placeholder: "Paste the code or the full redirect address", "aria-label": "Sign-in code" } });
    box.append(el("p", {}, "Open ", webLink(flow.authorizationUrl, "the sign-in page"), ", then paste the code here."),
      el("div", { class: "row" }, code, button("Finish sign-in", { small: true, primary: true, onClick: () => finishSignIn(provider, flow, code.value) })));
  }
  box.append(button("Cancel", { small: true, link: true, onClick: () => { flow.cancelled = true; view.flows.delete(provider.instanceId); render(); } }));
  return box;
}

async function startSignIn(provider, method) {
  try {
    const flow = await ctx.api(`/api/models/extension/${encodeURIComponent(provider.instanceId)}/signin`, "POST", { method });
    view.flows.set(provider.instanceId, flow); render();
    if (flow.userCode) poll(provider, flow);
  } catch (reason) { view.message = reason.message; render(); }
}

async function poll(provider, flow) {
  while (!flow.cancelled && view.flows.get(provider.instanceId) === flow) {
    await new Promise(resolve => setTimeout(resolve, Math.max(2, flow.interval || 5) * 1000));
    if (flow.cancelled) return;
    try {
      const result = await ctx.api(`/api/models/extension/signin/${encodeURIComponent(flow.flowId)}/poll`, "POST", {});
      if (result.status === "complete") { signedIn(provider, result); return; }
      if (result.status === "failed") { flow.error = result.error || "Sign-in failed."; render(); return; }
    } catch (reason) { flow.error = reason.message; render(); return; }
  }
}

async function finishSignIn(provider, flow, code) {
  try {
    const result = await ctx.api(`/api/models/extension/signin/${encodeURIComponent(flow.flowId)}/complete`, "POST", { code });
    if (result.status === "complete") signedIn(provider, result);
    else { flow.error = result.error || "Sign-in did not finish."; render(); }
  } catch (reason) { flow.error = reason.message; render(); }
}

function signedIn(provider, result) {
  view.flows.delete(provider.instanceId);
  provider.ready = !result.error;
  view.message = result.error ? `Signed in, but models didn't load: ${result.error}` : `${provider.name || provider.provider} is ready.`;
  if (result.state) view.state = result.state;
  ctx.refreshStatus().catch(() => {});
  render();
}

function routesSection(state) {
  const all = targets(state);
  const editor = view.route;
  const box = el("section", { class: "section" }, el("h2", { text: "Routes" }),
    el("p", { class: "sub", text: "A route tries models in order: if one is busy or fails, the next one answers." }));
  if (state.routes.length) box.append(el("ul", { class: "list" }, ...state.routes.map(route => el("li", {},
    el("span", { class: "grow" }, el("strong", { text: route.name }), el("span", { class: "mono quiet", text: ` ${route.targets.join(" → ")}` })),
    state.defaultModel === route.name ? chip("default", "ok") : button("Use as default", { small: true, link: true, onClick: () => act("default", () => ctx.api("/api/models/default", "PUT", { selection: route.name })) }),
    button("Edit", { small: true, onClick: () => { view.route = { name: route.name, targets: [...route.targets], existing: true }; render(); } }),
    button("Delete", { small: true, link: true, onClick: () => act("route", () => ctx.api(`/api/models/routes/${encodeURIComponent(route.name)}`, "DELETE")) })))));
  if (!editor) { box.append(button("New route", { small: true, disabled: all.length < 1, onClick: () => { view.route = { name: "main", targets: [], existing: false }; render(); } })); return box; }
  const name = el("input", { attrs: { value: editor.name, "aria-label": "Route name", pattern: "[a-z][a-z0-9_-]*" } });
  name.disabled = editor.existing;
  const add = el("select", { attrs: { "aria-label": "Add a model to the route" } }, el("option", { text: "Add a model…", attrs: { value: "" } }),
    ...all.filter(target => !editor.targets.includes(target)).map(target => el("option", { text: target, attrs: { value: target } })));
  add.addEventListener("change", () => { if (add.value) { editor.targets.push(add.value); render(); } });
  const order = el("ol", { class: "route-targets" }, ...editor.targets.map((target, index) => el("li", {}, el("span", { class: "mono grow", text: target }),
    button("Up", { small: true, link: true, disabled: index === 0, onClick: () => { [editor.targets[index - 1], editor.targets[index]] = [editor.targets[index], editor.targets[index - 1]]; render(); } }),
    button("Down", { small: true, link: true, disabled: index === editor.targets.length - 1, onClick: () => { [editor.targets[index + 1], editor.targets[index]] = [editor.targets[index], editor.targets[index + 1]]; render(); } }),
    button("Remove", { small: true, link: true, onClick: () => { editor.targets.splice(index, 1); render(); } }))));
  box.append(el("div", { class: "card" }, field("Route name (lowercase)", name), order, add,
    el("div", { class: "row" }, button("Save route", { primary: true, disabled: !editor.targets.length, onClick: () => act("route", async () => {
      const result = await ctx.api(`/api/models/routes/${encodeURIComponent(name.value.trim())}`, "PUT", { targets: editor.targets });
      view.route = null; return result;
    }) }), button("Cancel", { small: true, link: true, onClick: () => { view.route = null; render(); } }))));
  return box;
}

function render() {
  if (!root) return;
  const state = view.state;
  const body = [el("div", { class: "view-heading" }, el("h1", { text: "Models" }),
    el("p", { class: "sub", text: "Model connections are managed by Compa. Midden uses whatever you connect; if a model can't use tools, the assistant says so." }))];
  if (view.busy) body.push(loading("Working..."));
  if (view.message) body.push(el("p", { class: "status-message", text: view.message, attrs: { role: "status" } }));
  if (view.error) body.push(errorBox(view.error));
  if (!state) { body.push(view.error ? button("Retry", { onClick: () => reload() }) : loading("Loading model connections...")); root.replaceChildren(...body); return; }
  body.push(defaultSection(state));
  body.push(el("section", { class: "section" }, el("h2", { text: "Connected" }),
    state.instances.length ? el("div", { class: "stack" }, ...state.instances.map(instance => instanceCard(instance, state))) : emptyState("Nothing connected yet", "Try the free models, connect a local server, or add a provider below.")));
  body.push(freeSection(), localSection(), keySection(state), extensionSection(state), routesSection(state));
  root.replaceChildren(...body);
}

export default {
  mount(element, context) { root = element; ctx = context; ctx.bus.on("workspace-changed", () => { view.state = null; }); },
  show() { render(); reload(); },
  hide() { view.message = ""; },
};

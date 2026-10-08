"""Mock-host browser tests, NOT live kernel/bundle acceptance.

Run: python -B -m unittest discover -s apps\\midden-ui\\webtest -v
Requires an existing Python Playwright installation and its Chromium browser;
set PLAYWRIGHT_BROWSERS_PATH if installed outside Playwright's default cache.
Optional MIDDEN_UI_SCREENSHOTS writes desktop/mobile PNGs to that directory.
Only loopback HTTP and synthetic fixtures are used. No model calls or accounts.

The host serves web/index.html, app.js, style.css and the /js view modules at /.
The page is a journey shell with hash routes (#/sessions default, #/evidence/<id>,
#/sources, #/files[/<path>], #/assistant[/<conversation>], #/models). API errors
are JSON. SSE uses real EventSource, including id/seq replay detection. activeTurn
accepts a turn ID (in a session response) or {turnId, sessionId} (in host status).
Model connections come from the host roster in GET /api/models/state, which never
contains secrets; blank API keys and default endpoints are omitted when connecting.
Host notices remain visible separately from dismissible request errors.
configured means the host reported a usable default model, shown by the model chip.
Preview fixtures use the backend's inline-script-only CSP with an opaque sandbox
origin and no network access. They exercise runtime artifacts, not live decks.
Call/state assertions use matching HTTP responses, not optimistic busy controls.
No-call assertions also inspect synchronous fetch starts, before network delivery.
The host must never return API keys, and must replay outstanding permissions
after reconnect/reload. Live provider execution, credential storage, cancellation
and file-scope enforcement remain parent-host end-to-end acceptance work.
"""

import asyncio
from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import queue
import threading
import unittest
from urllib.parse import parse_qs, unquote, urlsplit

from playwright.sync_api import sync_playwright, expect


WEB = Path(__file__).resolve().parents[1] / "web"
PREVIEW_CSP = (
    "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline'; "
    "style-src 'unsafe-inline'; img-src data:; font-src data:; connect-src 'none'; "
    "base-uri 'none'; form-action 'none'"
)
PREVIEW_HTML = """<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Synthetic runtime preview</title></head>
<body><h1>Starting preview runtime...</h1><p id="view">First view</p>
<button id="next" type="button">Next view</button>
<p id="parentAccess">Waiting for parent-access check...</p>
<p id="networkAccess">Waiting for network-policy check...</p>
<script>
document.querySelector("h1").textContent = "Rendered sample";
document.getElementById("next").onclick = () => {
  document.getElementById("view").textContent = "Second view";
};
try {
  parent.document.body.dataset.unsafe = "yes";
  document.getElementById("parentAccess").textContent = "Parent access allowed";
} catch {
  document.getElementById("parentAccess").textContent = "Parent access blocked";
}
document.addEventListener("securitypolicyviolation", event => {
  if (event.effectiveDirective === "connect-src") {
    document.getElementById("networkAccess").textContent = "Blocked by connect-src";
  }
});
fetch("https://preview-network.invalid/probe", {mode: "no-cors"})
  .then(() => { document.getElementById("networkAccess").textContent = "Network allowed"; })
  .catch(() => { document.body.dataset.fetchRejected = "true"; });
</script></body></html>"""


class RequestGate:
    def __init__(self):
        self.arrived = threading.Event()
        self.release = threading.Event()


class MockHost:
    def __init__(self):
        self.workspace_id = "workspace-a"
        self.workspace_name = "Synthetic workspace"
        self.csrf = "synthetic-csrf"
        self.tokens = {self.csrf}
        self.replay = []
        self.versions = dict(uiVersion="test-ui", coreVersion="test-core", kernelName="Compa", kernelVersion="3.0.0")
        self.model = dict(configured=True, defaultModel="synthetic/synthetic-model",
                          summary="synthetic/synthetic-model", setupError="")
        self.roster = [dict(id=kind, label=label, defaultEndpoint=f"https://{kind}.invalid/v1",
                            requiresApiKey=True, requiresBaseUrl=False, keyless=False)
                       for kind, label in (("openai", "OpenAI"), ("anthropic", "Anthropic"))]
        self.instances = [dict(id="synthetic", label="Synthetic service", providerKind="custom_openai",
                               endpoint="http://127.0.0.1:9/v1", state="enabled", credentialReady=True, source="local",
                               models=[dict(id="synthetic-model"), dict(id="other-model")], checks={})]
        # View tests extend this: (method, path) -> callable(handler, body or None) returning a JSON-able reply.
        self.routes = {
            ("GET", "/api/core/sessions"): lambda *_: dict(sessions=[], total=0, matched=0, excluded_noise=0, offset=0,
                                                            stores_read=[], warnings=[], partial=False),
            ("GET", "/api/core/collections"): lambda *_: dict(collections=[]),
            ("GET", "/api/models/state"): lambda *_: dict(state=self.model_state()),
            ("POST", "/api/models/instances"): self.connect_instance,
            ("POST", "/api/models/instances/synthetic/check"): self.check_model,
            ("PUT", "/api/models/default"): self.choose_default,
        }
        self.sessions = {
            "s1": dict(id="s1", title="Research notes", messages=[], updated="2026-01-01T12:00:00Z"),
            "s2": dict(id="s2", title="Earlier conversation", updated="2026-01-01T11:00:00Z",
                       messages=[dict(role="assistant", content="An earlier observation.", at="2026-01-01T11:00:00Z")]),
        }
        self.contents = {"draft & notes.txt": "<img src=x onerror=alert(1)>\nSynthetic notes.",
                         "sample.html": PREVIEW_HTML}
        self.active = None
        self.calls, self.clients, self.failures = [], [], {}
        self.gates = {}
        self.seq, self.turns = 0, 0
        self.fast_finish = None
        self.running = True

    def model_state(self):
        return dict(roster=self.roster, instances=self.instances, routes=[], activeModels=[],
                    defaultModel=self.model.get("defaultModel", ""), extension=dict(url="", connected=False),
                    configured=self.model.get("configured", False), setupError=self.model.get("setupError", ""))

    def connect_instance(self, _handler, body):
        entry = next(item for item in self.roster if item["id"] == body["providerKind"])
        self.instances.append(dict(id=entry["id"], label=body.get("label") or entry["label"], providerKind=entry["id"],
                                   endpoint=body.get("endpoint") or entry["defaultEndpoint"], state="enabled",
                                   credentialReady=True, source="key", models=[dict(id="connected-model")], checks={}))
        return dict(state=self.model_state())

    def check_model(self, _handler, body):
        self.instances[0]["checks"][body["model"]] = dict(status="tested")
        return dict(status="tested", message="Synthetic tool-calling probe succeeded.")

    def choose_default(self, _handler, body):
        self.model.update(configured=bool(body["selection"]), defaultModel=body["selection"],
                          summary=body["selection"], setupError="")
        return dict(state=self.model_state())

    def emit(self, kind, **fields):
        self.seq += 1
        event = dict(seq=self.seq, type=kind, sessionId="s1", turnId="t1")
        event.update(fields)
        if kind == "message":
            self.sessions[event["sessionId"]]["messages"].append(
                dict(role="assistant", content=event["text"], at="2026-01-01T12:01:00Z"))
        if kind in ("turn_done", "error"):
            if self.active and self.active["turnId"] == event["turnId"]:
                self.active = None
            for outcome in self.sessions.get(event["sessionId"], {}).get("outcomes", []):
                if outcome["turnId"] == event["turnId"]:
                    outcome.update(status="failed" if kind == "error" else event.get("status", "completed"),
                                   at="2026-01-01T12:01:00Z", error=event.get("error", ""))
        for client in self.clients.copy():
            client.put(f"id: {self.seq}\nevent: message\ndata: {json.dumps(event)}\n\n")

    def handler(self):
        host = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def wait_for_gate(self, path):
                gate = host.gates.get((self.command, path))
                if gate is not None:
                    gate.arrived.set()
                    gate.release.wait()

            def reply(self, data, status=200, mime="application/json"):
                body = json.dumps(data).encode() if mime == "application/json" else data
                self.send_response(status)
                self.send_header("Content-Type", mime)
                self.send_header("Content-Length", str(len(body)))
                if urlsplit(self.path).path == "/preview":
                    self.send_header("Content-Security-Policy", PREVIEW_CSP)
                self.end_headers()
                self.wfile.write(body)

            def do_GET(self):
                url = urlsplit(self.path)
                path = unquote(url.path)
                self.wait_for_gate(path)
                if path == "/api/events":
                    client = queue.Queue()
                    host.clients.append(client)
                    for index, record in enumerate(host.replay, 1):
                        event = dict(seq=index, **record)
                        client.put(f"id: {index}\nevent: message\ndata: {json.dumps(event)}\n\n")
                        host.seq = max(host.seq, index)
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.send_header("Cache-Control", "no-cache")
                    self.end_headers()
                    try:
                        self.wfile.write(b": connected\n\n")
                        self.wfile.flush()
                        while host.running:
                            try:
                                event = client.get(timeout=0.2)
                            except queue.Empty:
                                continue
                            if event is None:
                                break
                            self.wfile.write(event.encode())
                            self.wfile.flush()
                    except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                        pass
                    finally:
                        host.clients.remove(client)
                    return
                host.calls.append(("GET", path, None, None))
                if ("GET", path) in host.failures:
                    return self.reply({"error": host.failures["GET", path]}, 503)
                if path in ("/", "/app.js", "/style.css") or (path.startswith(("/js/", "/css/")) and path.count("/") == 2):
                    asset = WEB / (path[1:] or "index.html")
                    mime = {".html": "text/html", ".js": "text/javascript", ".css": "text/css"}.get(asset.suffix, "text/plain")
                    return self.reply(asset.read_bytes() if asset.exists() else b"UI not implemented", 200 if asset.exists() else 404, mime=mime)
                if ("GET", path) in host.routes:
                    return self.reply(host.routes["GET", path](self, None))
                if path == "/api/status":
                    host.tokens.add(host.csrf)
                    return self.reply(dict(workspace=host.workspace_name, workspaceId=host.workspace_id, **host.versions,
                        model=host.model, activeTurn=host.active,
                        notice="The assistant runs commands with your account; this is not an OS sandbox.",
                        csrfToken=host.csrf, skills=[
                            dict(name="Investigation", description="Understand source material and its limits."),
                            dict(name="Presentation", description="Create an editable, inspectable presentation.")]))
                if path == "/api/sessions":
                    return self.reply({"sessions": [{key: session[key] for key in ("id", "title", "updated")}
                                                   for session in host.sessions.values()]})
                if path.startswith("/api/sessions/"):
                    data = dict(host.sessions[path.rsplit("/", 1)[1]])
                    if host.active and host.active["sessionId"] == data["id"]:
                        data["activeTurn"] = host.active["turnId"]
                    return self.reply(data)
                if path == "/api/files":
                    return self.reply({"files": [dict(path=name, size=len(content), modified="2026-01-01T12:00:00Z",
                        kind="html" if name.endswith(".html") else "text") for name, content in host.contents.items()]})
                if path in ("/api/file", "/preview", "/download"):
                    name = parse_qs(url.query)["path"][0]
                    content = host.contents[name]
                    if path == "/api/file":
                        return self.reply(dict(path=name, content=content, sha256="a" * 64))
                    return self.reply(content.encode(), mime="text/html" if path == "/preview" else "application/octet-stream")
                self.reply({"error": "Unknown mock route"}, 404)

            def do_POST(self):
                path = unquote(urlsplit(self.path).path)
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                token = self.headers.get("X-Midden-CSRF")
                self.wait_for_gate(path)
                host.calls.append((self.command, path, body, token))
                if token != host.csrf:
                    return self.reply({"error": "Missing CSRF token"}, 403)
                if (self.command, path) in host.failures:
                    return self.reply({"error": host.failures[self.command, path]}, 409)
                if (self.command, path) in host.routes:
                    return self.reply(host.routes[self.command, path](self, body))
                if path == "/api/sessions":
                    sid = f"s{len(host.sessions) + 1}"
                    host.sessions[sid] = dict(id=sid, title="New conversation", messages=[], outcomes=[], updated="2026-01-01T12:00:00Z")
                    return self.reply(dict(id=sid, title="New conversation"))
                if path.endswith("/turn"):
                    sid = path.split("/")[3]
                    host.turns += 1
                    host.active = dict(turnId=f"t{host.turns}", sessionId=sid)
                    index = len(host.sessions[sid]["messages"])
                    host.sessions[sid]["messages"].append(dict(role="user", content=body["message"], at="2026-01-01T12:00:00Z"))
                    tid = host.active["turnId"]
                    host.sessions[sid].setdefault("outcomes", []).append(dict(
                        turnId=tid, status="running", messageIndex=index, at="2026-01-01T12:00:00Z"))
                    if host.fast_finish:
                        host.emit("message", sessionId=sid, turnId=tid, text="A fast response.")
                        host.emit("turn_done", sessionId=sid, turnId=tid)
                        host.fast_finish.arrived.set()
                        host.fast_finish.release.wait()
                    return self.reply({"turnId": tid}, 202)
                if path == "/api/cancel" or path.startswith("/api/permissions/"):
                    return self.reply({})
                if path == "/api/quit":
                    return self.reply({"stopping": True}, 202)
                self.reply({"error": "Unknown mock mutation"}, 404)

            do_PUT = do_POST

            def do_DELETE(self):
                path = unquote(urlsplit(self.path).path)
                token = self.headers.get("X-Midden-CSRF")
                self.wait_for_gate(path)
                host.calls.append((self.command, path, None, token))
                if token != host.csrf:
                    return self.reply({"error": "Missing CSRF token"}, 403)
                if (self.command, path) in host.failures:
                    return self.reply({"error": host.failures[self.command, path]}, 409)
                if (self.command, path) in host.routes:
                    return self.reply(host.routes[self.command, path](self, None))
                sid = path.rsplit("/", 1)[1]
                if path.startswith("/api/sessions/") and sid in host.sessions:
                    del host.sessions[sid]
                    return self.reply({"deleted": True})
                self.reply({"error": "Unknown mock deletion"}, 404)

        return Handler


class MockHTTPServer(ThreadingHTTPServer):
    # Chromium startup/preconnect bursts can exceed the stdlib's five-slot listen queue.
    request_queue_size = 64


class BrowserCase(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.playwright = sync_playwright().start()
        cls.browser = cls.playwright.chromium.launch()

    @classmethod
    def tearDownClass(cls):
        cls.browser.close()
        cls.playwright.stop()

    def setUp(self):
        self.host = MockHost()
        self.original_files = dict(self.host.contents)
        self.server = MockHTTPServer(("127.0.0.1", 0), self.host.handler())
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.context = self.browser.new_context(viewport={"width": 1440, "height": 960})
        self.waiting_gate = None

        async def gate_ready():
            return await asyncio.to_thread(self.waiting_gate.arrived.wait, 5)

        self.context.expose_function("__middenTestGateReady", gate_ready)
        self.context.add_init_script("""if (window === window.top) {
            window.__middenTestRequests = [];
            const originalFetch = window.fetch;
            window.fetch = function (input, options) {
                const request = input instanceof Request ? input : null;
                const url = new URL(request ? request.url : input, location.href);
                if (url.origin === location.origin && url.pathname.startsWith("/api/")) {
                    const method = String(options?.method || request?.method || "GET").toUpperCase();
                    window.__middenTestRequests.push([method, decodeURIComponent(url.pathname)]);
                }
                return originalFetch.apply(this, arguments);
            };
        }""")
        self.external_requests = []
        self.context.route("https://preview-network.invalid/**", self.external_probe)
        self.page = self.context.new_page()
        self.browser_requests = []
        self.page.on("request", lambda request: self.browser_requests.append(
            (request.method, unquote(urlsplit(request.url).path))))
        self.errors = []
        self.console = []
        self.page.on("pageerror", lambda error: self.errors.append(str(error)))
        self.page.on("console", lambda message: self.console.append(message.text))
        self.url = f"http://127.0.0.1:{self.server.server_port}"
        self.addCleanup(self.cleanup)

    def external_probe(self, route):
        self.external_requests.append(route.request.url)
        route.fulfill(status=200, body="Synthetic network response; should not be reached")

    def cleanup(self):
        self.host.running = False
        for client in self.host.clients.copy():
            client.put(None)
        self.context.close()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        self.assertEqual(self.errors, [])
        self.assertEqual(self.external_requests, [])
        self.assertFalse(any(call[1].startswith("/api/auth/") for call in self.host.calls))
        self.assertFalse(any(path.startswith("/api/auth/") for _, path in self.browser_requests))
        self.assertNotIn("synthetic-key-only", "\n".join(self.console))
        self.assertEqual(self.host.contents, self.original_files)
        for method, _, _, token in self.host.calls:
            if method != "GET":
                self.assertIn(token, self.host.tokens)

    def open(self, route="#/assistant"):
        self.page.goto(self.url + "/" + route)
        expect(self.page.locator("#connection")).to_have_text("Live updates connected")
        expect(self.page.locator("#sessionList button")).to_have_count(len(self.host.sessions))

    @contextmanager
    def api_response(self, method, path, status=200):
        with self.page.expect_response(
            lambda response: response.request.method == method and
            unquote(urlsplit(response.url).path) == path,
            timeout=5000,
        ) as response:
            yield response
        self.assertEqual(response.value.status, status, f"{method} {path}")

    @contextmanager
    def hold_api(self, method, path):
        key = (method, path)
        self.assertNotIn(key, self.host.gates)
        gate = self.host.gates[key] = RequestGate()
        try:
            yield gate
        finally:
            gate.release.set()
            del self.host.gates[key]

    def wait_for_gate(self, gate):
        self.assertIsNone(self.waiting_gate)
        self.waiting_gate = gate
        try:
            # Keep Playwright dispatching while the server condition is pending.
            self.assertTrue(self.page.evaluate("() => window.__middenTestGateReady()"),
                            "Request did not reach its server gate")
        finally:
            self.waiting_gate = None

    def assert_no_api_calls(self, method=None, path=None):
        calls = [(call[0], call[1]) for call in self.host.calls] + self.browser_requests
        calls += self.page.evaluate("window.__middenTestRequests || []")
        unexpected = [(verb, target) for verb, target in calls
                      if (verb == method if method else verb != "GET") and
                      (path is None or target == path)]
        self.assertEqual(unexpected, [], "Unexpected API request, including queued fetch starts")

    def nav(self, name):
        return self.page.get_by_role("navigation", name="Main").get_by_role("link", name=name, exact=True)

    def routed_session(self):
        route = [unquote(part) for part in urlsplit(self.page.url).fragment.split("/")]
        self.assertEqual(route[:2], ["", "assistant"], self.page.url)
        return route[2]

    def fits_width(self):
        return self.page.evaluate("document.documentElement.scrollWidth <= innerWidth")

    def screenshot(self, name):
        if directory := os.environ.get("MIDDEN_UI_SCREENSHOTS"):
            target = Path(directory)
            target.mkdir(parents=True, exist_ok=True)
            self.page.screenshot(path=str(target / name), full_page=True)

    def start_turn(self):
        self.page.get_by_label("Message", exact=True).fill("Help me turn these notes into a short explanation.")
        session_id = self.routed_session()
        with self.api_response("POST", f"/api/sessions/{session_id}/turn", 202):
            self.page.get_by_label("Message", exact=True).press("Control+Enter")
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_enabled()

    def answer_dialog(self, accept):
        """Answers the next confirm() and returns the list its message lands in."""
        messages = []

        def handle(dialog):
            messages.append(dialog.message)
            if accept:
                dialog.accept()
            else:
                dialog.dismiss()

        self.page.once("dialog", handle)
        return messages

class FrontendTests(BrowserCase):
    def test_boot_history_new_conversation_and_draft_preservation(self):
        self.open()
        expect(self.page.locator("#workspace")).to_have_text("Synthetic workspace")
        expect(self.page.locator("#versions")).to_contain_text("Compa 3.0.0")
        expect(self.page.locator("#appNotice")).to_be_visible()
        expect(self.page.locator("#appNotice")).to_contain_text("this is not an OS sandbox")
        expect(self.page.locator("#skills")).to_contain_text("Understand source material")
        expect(self.page.locator("#welcome")).to_contain_text("runs commands there without asking first")
        expect(self.page.locator("#fileList button")).to_have_count(2)
        self.screenshot("desktop.png")
        self.page.get_by_label("Message", exact=True).fill("Keep my unsent thought.")
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        expect(self.page.locator("#messages")).to_contain_text("An earlier observation.")
        self.assertEqual(self.routed_session(), "s2")
        self.page.reload()
        expect(self.page.locator("#messages")).to_contain_text("An earlier observation.")
        self.page.get_by_label("Message", exact=True).fill("Another unsent thought.")
        self.page.get_by_role("button", name="Research notes", exact=False).click()
        self.page.get_by_label("Message", exact=True).fill("Keep my unsent thought.")
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("Another unsent thought.")
        self.page.locator("#newSession").click()
        expect(self.page.locator("#sessionTitle")).to_have_text("New conversation")
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("")

    def test_pending_conversation_creation_blocks_composer(self):
        self.open()
        message = self.page.get_by_label("Message", exact=True)
        message.fill("Keep this draft in the original conversation.")
        with self.hold_api("POST", "/api/sessions") as gate:
            self.page.locator("#newSession").click()
            self.wait_for_gate(gate)
            expect(message).to_be_disabled()
            expect(self.page.locator("#send")).to_be_disabled()
            with self.api_response("POST", "/api/sessions"):
                gate.release.set()
        expect(self.page.locator("#sessionTitle")).to_have_text("New conversation")
        expect(message).to_be_enabled()
        expect(message).to_be_focused()
        message.fill("A draft for the new conversation.")
        self.page.get_by_role("button", name="Research notes", exact=False).click()
        expect(message).to_have_value("Keep this draft in the original conversation.")

    def test_failed_conversation_creation_restores_composer_and_draft(self):
        self.host.failures["POST", "/api/sessions"] = "Conversation creation unavailable"
        self.open()
        message = self.page.get_by_label("Message", exact=True)
        message.fill("Keep this unsent thought after a failed creation.")
        self.page.locator("#newSession").click()
        expect(self.page.locator("#notice")).to_contain_text("Conversation creation unavailable")
        expect(message).to_be_enabled()
        expect(message).to_have_value("Keep this unsent thought after a failed creation.")
        expect(self.page.locator("#send")).to_be_enabled()
        expect(self.page.locator("#newSession")).to_be_enabled()

    def test_full_text_events_individual_permissions_and_stop(self):
        self.open()
        self.start_turn()
        self.host.emit("delta", text="A")
        self.host.emit("delta", text="A complete draft.")
        expect(self.page.locator("#messages .assistant pre")).to_have_text("A complete draft.")
        self.host.emit("tool", tool="read_file", status="running", arguments={"path": "draft & notes.txt"})
        for pid in ("p1", "p2"):
            self.host.emit("permission", permissionId=pid, tool="write_file", arguments={"path": f"{pid}.txt"})
        expect(self.page.locator(".permission")).to_have_count(2)
        with self.api_response("POST", "/api/permissions/p1"):
            self.page.locator('[data-permission-id="p1"]').get_by_role("button", name="Allow", exact=True).click()
        with self.api_response("POST", "/api/permissions/p2"):
            self.page.locator('[data-permission-id="p2"]').get_by_role("button", name="Deny", exact=True).click()
        expect(self.page.locator('[data-permission-id="p1"]')).to_contain_text("Allowed")
        expect(self.page.locator('[data-permission-id="p2"]')).to_contain_text("Denied")
        decisions = [call[2] for call in self.host.calls if call[1].startswith("/api/permissions/")]
        self.assertEqual(decisions, [{"allow": True}, {"allow": False}])
        with self.hold_api("POST", "/api/cancel") as gate:
            self.page.get_by_role("button", name="Stop", exact=True).click()
            self.wait_for_gate(gate)
            expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_disabled()
            self.assertNotIn(("POST", "/api/cancel", {"turnId": "t1"}, "synthetic-csrf"), self.host.calls)
            with self.api_response("POST", "/api/cancel"):
                gate.release.set()
        self.assertIn(("POST", "/api/cancel", {"turnId": "t1"}, "synthetic-csrf"), self.host.calls)
        self.host.emit("message", text="A complete draft.")
        self.host.emit("turn_done")
        expect(self.page.locator("#turnStatus")).to_contain_text("Ready")
        expect(self.page.locator("#messages .assistant pre")).to_have_text("A complete draft.")
        self.host.emit("delta", text="Late stale output.")
        expect(self.page.locator("#messages")).not_to_contain_text("Late stale output.")

    def test_errors_keep_drafts_and_surface_failed_reads(self):
        self.host.failures["GET", "/api/files"] = "File listing unavailable"
        self.open("#/files")
        expect(self.page.locator("#filesError")).to_contain_text("File listing unavailable")
        del self.host.failures["GET", "/api/files"]
        self.page.get_by_role("button", name="Refresh files").click()
        expect(self.page.locator("#fileList button")).to_have_count(2)
        expect(self.page.locator("#filesError")).to_be_hidden()
        self.host.failures["POST", "/api/sessions/s1/turn"] = "Turn could not start"
        self.nav("Assistant").click()
        self.page.get_by_label("Message", exact=True).fill("Do not lose this message.")
        self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.page.locator("#notice")).to_contain_text("Turn could not start")
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("Do not lose this message.")
        expect(self.page.locator("#messages .user")).to_have_count(0)

    def test_model_keys_are_write_only_kept_for_retry_and_never_echoed(self):
        self.open("#/models")
        card = self.page.locator(".prov-card").filter(has_text="OpenAI")
        card.get_by_role("button", name="Connect", exact=True).click()
        dialog = self.page.get_by_role("dialog", name="Connect OpenAI")
        key = dialog.get_by_label("API key")
        expect(key).to_be_focused()
        expect(key).to_have_attribute("type", "password")
        expect(dialog).to_contain_text("Saved in the App's data folder (kernel/auth.json) and never shown again.")
        self.host.failures["POST", "/api/models/instances"] = "Rejected synthetic-key-only"
        key.fill("synthetic-key-only")
        with self.api_response("POST", "/api/models/instances", 409):
            dialog.get_by_role("button", name="Connect", exact=True).click()
        expect(dialog.get_by_role("status")).to_contain_text("Rejected")
        expect(dialog).not_to_contain_text("synthetic-key-only")
        expect(key).to_have_value("synthetic-key-only")
        del self.host.failures["POST", "/api/models/instances"]
        with self.api_response("POST", "/api/models/instances"):
            dialog.get_by_role("button", name="Connect", exact=True).click()
        expect(dialog).to_have_count(0)
        expect(self.page.locator("#view-models .status-message")).to_have_text("Connected OpenAI.")
        saved = [call[2] for call in self.host.calls if call[:2] == ("POST", "/api/models/instances")]
        self.assertEqual(saved[-1], {"providerKind": "openai", "apiKey": "synthetic-key-only"})
        self.assertNotIn("synthetic-key-only", self.page.evaluate("JSON.stringify([localStorage, sessionStorage])"))
        self.assertNotIn("synthetic-key-only", self.page.content())
        card.get_by_role("button", name="Connect", exact=True).click()
        expect(dialog.get_by_label("API key")).to_have_value("")
        self.page.keyboard.press("Escape")
        expect(dialog).to_have_count(0)
        expect(card.get_by_role("button", name="Connect", exact=True)).to_be_focused()

    def test_safe_text_sandbox_preview_and_download(self):
        self.open("#/files")
        self.page.get_by_role("button", name="draft & notes.txt", exact=False).click()
        expect(self.page.locator("#fileContent")).to_contain_text("<img src=x onerror=alert(1)>")
        expect(self.page.locator("#fileContent img")).to_have_count(0)
        self.assertEqual(self.page.evaluate("location.hash"), "#/files/draft%20%26%20notes.txt")
        with self.page.expect_download() as download:
            self.page.get_by_role("button", name="Download", exact=True).click()
        self.assertEqual(Path(download.value.path()).read_text(), self.host.contents["draft & notes.txt"])
        self.page.get_by_role("button", name="sample.html", exact=False).click()
        expect(self.page.locator("#fileFrame")).to_be_visible()
        expect(self.page.locator("#fileFrame")).to_have_attribute("sandbox", "allow-scripts")
        expect(self.page.frame_locator("#fileFrame").get_by_role("heading")).to_have_text("Rendered sample")
        self.assertIsNone(self.page.locator("body").get_attribute("data-unsafe"))
        self.page.get_by_role("button", name="Expand preview").click()
        expect(self.page.locator("#previewDialog")).to_be_visible()
        self.page.keyboard.press("Escape")
        self.host.failures["GET", "/preview"] = "Preview blocked by host"
        self.page.get_by_role("button", name="sample.html", exact=False).click()
        expect(self.page.locator("#filesError")).to_contain_text("Preview blocked by host")

    def test_preview_runtime_updates_itself_but_cannot_access_parent_or_network(self):
        self.open("#/files")
        self.page.get_by_role("button", name="sample.html", exact=False).click()
        for selector in ("#fileFrame", "#expandedFrame"):
            if selector == "#expandedFrame":
                self.page.get_by_role("button", name="Expand preview").click()
            frame = self.page.frame_locator(selector)
            expect(frame.get_by_role("heading")).to_have_text("Rendered sample")
            expect(frame.locator("#parentAccess")).to_have_text("Parent access blocked")
            expect(frame.locator("#networkAccess")).to_have_text("Blocked by connect-src")
            expect(frame.locator("body")).to_have_attribute("data-fetch-rejected", "true")
            frame.get_by_role("button", name="Next view").click()
            expect(frame.locator("#view")).to_have_text("Second view")
            expect(self.page.locator(selector)).to_have_attribute("sandbox", "allow-scripts")
        self.assertIsNone(self.page.locator("body").get_attribute("data-unsafe"))
        self.assertEqual(self.external_requests, [])
        self.screenshot("runtime-preview.png")

    def test_resumed_turn_and_background_permissions_do_not_leak_text(self):
        self.host.active = dict(turnId="resumed", sessionId="s2")
        self.open()
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_enabled()
        expect(self.page.get_by_role("button", name="Send", exact=True)).to_be_disabled()
        self.host.emit("delta", sessionId="s2", turnId="resumed", text="Only in the earlier conversation.")
        self.host.emit("permission", sessionId="s2", turnId="resumed", permissionId="resumed-p", tool="read_file", arguments={})
        expect(self.page.locator("#messages")).not_to_contain_text("Only in the earlier conversation.")
        self.page.get_by_role("button", name="Open active conversation").click()
        expect(self.page.locator("#messages")).to_contain_text("Only in the earlier conversation.")
        expect(self.page.locator(".permission")).to_contain_text("read_file")
        self.host.emit("permission_result", sessionId="s2", turnId="resumed", permissionId="resumed-p", allow=False)
        expect(self.page.locator(".permission")).to_contain_text("Denied")

    def test_fast_turn_completion_before_http_ack_is_not_resurrected(self):
        gate = self.host.fast_finish = RequestGate()
        self.open()
        self.page.get_by_label("Message", exact=True).fill("Give me a short explanation.")
        try:
            self.page.get_by_role("button", name="Send", exact=True).click()
            self.wait_for_gate(gate)
            expect(self.page.locator("#messages .assistant pre")).to_have_text("A fast response.")
            expect(self.page.get_by_label("Message", exact=True)).to_have_value("Give me a short explanation.")
            with self.api_response("POST", "/api/sessions/s1/turn", 202):
                gate.release.set()
        finally:
            gate.release.set()
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("")
        expect(self.page.locator("#turnStatus")).to_contain_text("Ready")
        expect(self.page.locator("#messages .user")).to_have_count(1)

    def test_second_turn_does_not_display_the_previous_reply_as_new_output(self):
        self.open()
        self.start_turn()
        self.host.emit("message", text="The first reply.")
        self.host.emit("turn_done")
        expect(self.page.locator("#turnStatus")).to_contain_text("Ready")
        self.start_turn()
        expect(self.page.locator("#messages .assistant pre")).to_have_count(1)
        self.host.emit("delta", turnId="t2", text="A different reply.")
        expect(self.page.locator("#messages .assistant pre")).to_have_text(["The first reply.", "A different reply."])

    def test_live_updates_preserve_sidebar_keyboard_focus_and_render_chat_as_text(self):
        self.open()
        self.start_turn()
        button = self.page.get_by_role("button", name="Earlier conversation", exact=False)
        button.focus()
        self.host.emit("tool", tool="read_file", status="complete", arguments={})
        for client in self.host.clients.copy():
            client.put('data: {"seq":1,"type":"tool","sessionId":"s1","turnId":"t1","tool":"replayed"}\n\n')
        self.host.emit("delta", text="<img src=x onerror=alert(1)>")
        expect(self.page.locator("#messages")).to_contain_text("<img src=x onerror=alert(1)>")
        expect(self.page.locator("#messages img")).to_have_count(0)
        expect(self.page.locator("#activityList .tool")).to_have_count(1)
        expect(button).to_be_focused()

    def test_reconnect_expires_permissions_and_allows_stopping_a_later_turn(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.open()
        self.start_turn()
        self.host.emit("permission", permissionId="stale", tool="read_file", arguments={})
        expect(self.page.locator(".permission")).to_be_visible()
        with self.api_response("POST", "/api/cancel"):
            self.page.get_by_role("button", name="Stop", exact=True).click()
        self.host.active = None
        for client in self.host.clients.copy():
            client.put(None)
        expect(self.page.locator("#turnStatus")).to_contain_text("Live updates interrupted")
        expect(self.page.locator(".permission")).to_contain_text("No longer pending", timeout=10000)
        self.start_turn()
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_enabled()

    def test_models_are_not_editable_before_their_state_loads(self):
        self.open()
        models = self.page.locator("#view-models")
        with self.hold_api("GET", "/api/models/state") as gate:
            self.nav("Models").click()
            self.wait_for_gate(gate)
            expect(models.get_by_role("status")).to_have_text("Loading model connections...")
            expect(models.get_by_role("button")).to_have_count(0)
            expect(models.get_by_role("combobox")).to_have_count(0)
            with self.api_response("GET", "/api/models/state"):
                gate.release.set()
        expect(self.page.get_by_label("Default model", exact=True)).to_be_enabled()
        expect(self.page.get_by_label("Default model", exact=True)).to_have_value("synthetic/synthetic-model")
        self.assert_no_api_calls()

    def test_failed_history_load_is_not_presented_as_a_loaded_conversation(self):
        self.host.failures["GET", "/api/sessions/s1"] = "Conversation could not be read"
        self.open()
        expect(self.page.locator("#notice")).to_contain_text("Conversation could not be read")
        expect(self.page.get_by_label("Message", exact=True)).to_be_disabled()
        del self.host.failures["GET", "/api/sessions/s1"]
        self.page.get_by_role("button", name="Reload Midden").click()
        expect(self.page.get_by_label("Message", exact=True)).to_be_enabled()

    def test_permission_and_kernel_errors_are_visible_and_recoverable(self):
        self.open()
        self.start_turn()
        self.host.failures["POST", "/api/permissions/p1"] = "Decision could not be recorded"
        self.host.emit("permission", permissionId="p1", tool="read_file", arguments={})
        self.page.locator(".permission").get_by_role("button", name="Allow", exact=True).click()
        expect(self.page.locator(".permission")).to_contain_text("Decision could not be recorded")
        expect(self.page.locator(".permission").get_by_role("button", name="Allow", exact=True)).to_be_enabled()
        self.host.emit("error", error="Synthetic kernel failure")
        expect(self.page.locator("#messages .turn-outcome")).to_contain_text("Synthetic kernel failure")
        expect(self.page.locator(".permission")).to_contain_text("No longer pending")
        expect(self.page.locator("#turnStatus")).to_contain_text("Ready")

    def test_host_and_closed_event_stream_failures_offer_a_working_retry(self):
        self.host.failures["GET", "/api/status"] = "Midden not ready"
        self.page.goto(self.url + "/#/assistant")
        expect(self.page.locator("#notice")).to_contain_text("Midden not ready")
        expect(self.page.get_by_label("Message", exact=True)).to_be_disabled()
        del self.host.failures["GET", "/api/status"]
        self.page.route("**/api/events", lambda route: route.fulfill(status=204))
        self.page.get_by_role("button", name="Reload Midden").click()
        expect(self.page.locator("#notice")).to_contain_text("Live event stream closed")
        self.page.unroute("**/api/events")
        self.page.get_by_role("button", name="Reload Midden").click()
        expect(self.page.locator("#connection")).to_have_text("Live updates connected")

    def test_file_and_status_events_refresh_an_open_artifact_and_model_summary(self):
        self.open("#/files")
        self.page.get_by_role("button", name="draft & notes.txt", exact=False).click()
        self.page.get_by_role("button", name="Expand preview").click()
        self.host.contents["draft & notes.txt"] = "Updated by the synthetic host."
        self.original_files = dict(self.host.contents)
        self.host.emit("files_changed", path="draft & notes.txt")
        expect(self.page.locator("#expandedText")).to_have_text("Updated by the synthetic host.")
        self.host.model["summary"] = "anthropic/synthetic-model"
        self.host.emit("status")
        expect(self.page.locator("#modelChip")).to_have_text("anthropic/synthetic-model")

    def test_mobile_navigation_keyboard_and_layout(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.open()
        self.assertEqual(self.page.locator("#chatScroll").evaluate("element => element.scrollTop"), 0)
        self.screenshot("mobile-chat.png")
        nav = self.page.get_by_role("navigation", name="Main")
        expect(nav.get_by_role("link")).to_have_text(["Sessions", "Sources", "Files", "Assistant", "Models"])
        bar, main = nav.bounding_box(), self.page.locator("main").bounding_box()
        self.assertLessEqual(bar["y"] + bar["height"], main["y"] + 1, "Navigation is a bar above the view")
        self.assertLess(bar["height"], 60, "Navigation links share one row")
        layout = self.page.locator(".assistant-layout").bounding_box()
        conversations, chat = self.page.locator("#sessionsPane").bounding_box(), self.page.locator("#chatPane").bounding_box()
        self.assertLessEqual(conversations["y"] + conversations["height"], chat["y"] + 1, "Conversations sit above the chat")
        self.assertLessEqual(conversations["height"], layout["height"] * 0.3 + 1)
        expect(self.page.locator("#sessionList")).to_be_visible()
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        expect(self.page.get_by_label("Message", exact=True)).to_be_focused()
        self.nav("Files").focus()
        self.page.keyboard.press("Enter")
        expect(self.nav("Files")).to_have_attribute("aria-current", "page")
        self.page.get_by_role("button", name="sample.html", exact=False).click()
        expect(self.page.locator("#fileFrame")).to_be_visible()
        self.screenshot("mobile-files.png")
        self.assertTrue(self.fits_width())
        self.nav("Models").focus()
        self.page.keyboard.press("Enter")
        connect = self.page.locator(".prov-card").filter(has_text="OpenAI").get_by_role("button", name="Connect", exact=True)
        connect.focus()
        self.page.keyboard.press("Enter")
        expect(self.page.get_by_role("dialog", name="Connect OpenAI").get_by_label("API key")).to_be_focused()
        self.page.keyboard.press("Escape")
        expect(connect).to_be_focused()
        self.assertTrue(self.fits_width())
        self.nav("Assistant").focus()
        self.page.keyboard.press("Enter")
        self.page.get_by_label("Message", exact=True).fill("First line")
        self.page.get_by_label("Message", exact=True).press("Enter")
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("First line\n")
        for width in (320, 768, 960, 1024):
            self.page.set_viewport_size({"width": width, "height": 844})
            self.assertTrue(self.fits_width(), width)

    def test_a_conversation_is_deleted_only_when_confirmed_and_takes_its_draft(self):
        self.open()
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        expect(self.page.locator("#messages")).to_contain_text("An earlier observation.")
        self.page.get_by_label("Message", exact=True).fill("A draft that goes with the conversation.")
        delete = self.page.get_by_role("button", name="Delete conversation")
        self.answer_dialog(accept=False)
        delete.click()
        expect(self.page.locator("#sessionList button")).to_have_count(2)
        self.assert_no_api_calls("DELETE")
        prompts = self.answer_dialog(accept=True)
        with self.api_response("DELETE", "/api/sessions/s2"):
            delete.click()
        self.assertIn("Earlier conversation", prompts[0])
        self.assertIn("Files it made stay", prompts[0])
        expect(self.page.locator("#sessionList button")).to_have_count(1)
        expect(self.page.locator("#sessionList")).not_to_contain_text("Earlier conversation")
        expect(self.page.locator("#sessionTitle")).to_have_text("Research notes")
        self.assertNotIn("s2", self.host.sessions)
        self.assertEqual(self.page.evaluate("Object.keys(localStorage).filter(key => key.includes('session:s2'))"), [])

    def test_a_conversation_with_a_running_turn_cannot_be_deleted(self):
        self.open()
        expect(self.page.get_by_role("button", name="Delete conversation")).to_be_enabled()
        self.start_turn()
        expect(self.page.get_by_role("button", name="Delete conversation")).to_be_hidden()
        self.host.emit("message", text="Done.")
        self.host.emit("turn_done")
        expect(self.page.get_by_role("button", name="Delete conversation")).to_be_enabled()

    def test_a_conversation_deleted_elsewhere_leaves_this_page(self):
        self.open()
        expect(self.page.locator("#sessionTitle")).to_have_text("Research notes")
        del self.host.sessions["s1"]
        self.host.emit("conversations_changed", sessionId="", turnId="")
        expect(self.page.locator("#sessionList button")).to_have_count(1)
        expect(self.page.locator("#sessionTitle")).to_have_text("Earlier conversation")

    def test_quit_stops_midden_after_confirming_and_says_how_to_start_it_again(self):
        self.open()
        self.start_turn()
        quit = self.page.get_by_role("button", name="Quit Midden")
        self.answer_dialog(accept=False)
        quit.click()
        expect(self.page.locator(".layout")).to_be_visible()
        self.assert_no_api_calls("POST", "/api/quit")
        prompts = self.answer_dialog(accept=True)
        with self.api_response("POST", "/api/quit", 202):
            quit.click()
        self.assertIn("The running turn will be stopped", prompts[0])
        self.assertIn("start it from its Start entry or run midden-ui", prompts[0])
        expect(self.page.get_by_role("heading", name="Midden has stopped")).to_be_visible()
        expect(self.page.locator(".layout")).to_be_hidden()
        expect(self.page.locator(".topbar")).to_be_hidden()
        self.assertEqual(self.page.evaluate("document.body.dataset.stopped"), "true")


if __name__ == "__main__":
    unittest.main()

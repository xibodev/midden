"""Mock-host browser tests, NOT live kernel/bundle acceptance.

Run: python -B -m unittest discover -s apps\\midden-ui\\webtest -v
Requires an existing Python Playwright installation and its Chromium browser;
set PLAYWRIGHT_BROWSERS_PATH if installed outside Playwright's default cache.
Optional MIDDEN_UI_SCREENSHOTS writes desktop/mobile PNGs to that directory.
Only loopback HTTP and synthetic fixtures are used. No model calls or accounts.

The host serves web/index.html, app.js and style.css at /. API errors are JSON.
SSE uses real EventSource, including id/seq replay detection. activeTurn accepts
a turn ID (in a session response) or {turnId, sessionId} (in host status).
Blank API keys are omitted from PUT /api/model to preserve stored credentials.
Compa exposes openai/anthropic compatible connections. Unsupported saved provider
values are reported and never silently normalized or saved.
Host notices remain visible separately from dismissible request errors.
configured means a model was selected, not that credentials were verified.
Only an explicit authStatus of "verified" is treated as verified authentication.
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
        self.catalog = {"models": [{"id": "synthetic/available", "name": "Available model"},
                                   {"id": "synthetic/other"}], "note": "Synthetic provider catalog."}
        self.model_check = {"ok": True, "message": "Synthetic tool-capability probe succeeded."}
        self.model = dict(configured=True, defaultModel="synthetic/synthetic-model",
                          summary="synthetic/synthetic-model", setupError="")
        # View tests extend this: (method, path) -> callable(handler, body or None) returning a JSON-able reply.
        self.routes = {
            ("GET", "/api/core/sessions"): lambda *_: dict(sessions=[], total=0, matched=0, excluded_noise=0, offset=0,
                                                            stores_read=[], warnings=[], partial=False),
            ("GET", "/api/core/collections"): lambda *_: dict(collections=[]),
            ("GET", "/api/models/state"): lambda *_: dict(state=dict(roster=[], instances=[], routes=[],
                defaultModel=self.model.get("defaultModel", ""), activeModels=[], extension=dict(url="", connected=False),
                configured=self.model.get("configured", False), setupError=self.model.get("setupError", ""))),
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
                    return self.reply(dict(workspace=host.workspace_name, workspaceId=host.workspace_id, coreVersion="test-core",
                        kernelName="Compa", kernelVersion="v1.0.0", model=host.model, activeTurn=host.active,
                        notice="Compa v1.0.0 released runtime. Approved shell commands run with your account; this is not an OS sandbox.",
                        csrfToken=host.csrf, bundles=[
                            dict(name="Investigation", description="Understand source material and its limits."),
                            dict(name="Presentation", description="Create an editable, inspectable presentation.")]))
                if path == "/api/model":
                    return self.reply(host.model)
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
                self.reply({"error": "Unknown mock mutation"}, 404)

            do_PUT = do_POST

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

    def open_settings(self):
        with self.api_response("GET", "/api/model"):
            self.page.get_by_role("button", name="Settings", exact=True).click()
        expect(self.page.get_by_label("Provider", exact=True)).to_be_enabled()

    def screenshot(self, name):
        if directory := os.environ.get("MIDDEN_UI_SCREENSHOTS"):
            target = Path(directory)
            target.mkdir(parents=True, exist_ok=True)
            self.page.screenshot(path=str(target / name), full_page=True)

    def start_turn(self):
        self.page.get_by_label("Message", exact=True).fill("Help me turn these notes into a short explanation.")
        session_id = parse_qs(urlsplit(self.page.url).fragment)["session"][0]
        with self.api_response("POST", f"/api/sessions/{session_id}/turn", 202):
            self.page.get_by_label("Message", exact=True).press("Control+Enter")
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_enabled()

class FrontendTests(BrowserCase):
    def test_boot_history_new_conversation_and_draft_preservation(self):
        self.open()
        expect(self.page.locator("#workspace")).to_have_text("Synthetic workspace")
        expect(self.page.locator("#kernelVersion")).to_contain_text("v1.0.0")
        expect(self.page.locator("#hostNotice")).to_be_visible()
        expect(self.page.locator("#hostNotice")).to_contain_text("this is not an OS sandbox")
        expect(self.page.locator("#bundles")).to_contain_text("Understand source material")
        expect(self.page.locator("#fileList button")).to_have_count(2)
        self.screenshot("desktop.png")
        self.page.get_by_label("Message", exact=True).fill("Keep my unsent thought.")
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        expect(self.page.locator("#messages")).to_contain_text("An earlier observation.")
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
        self.open()
        expect(self.page.locator("#filesError")).to_contain_text("File listing unavailable")
        del self.host.failures["GET", "/api/files"]
        self.page.get_by_role("button", name="Refresh files").click()
        expect(self.page.locator("#fileList button")).to_have_count(2)
        self.host.failures["POST", "/api/sessions/s1/turn"] = "Turn could not start"
        self.page.get_by_label("Message", exact=True).fill("Do not lose this message.")
        self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.page.locator("#notice")).to_contain_text("Turn could not start")
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("Do not lose this message.")
        expect(self.page.locator("#messages .user")).to_have_count(0)

    def test_settings_clear_secrets_and_report_configured_flag(self):
        self.open()
        self.open_settings()
        self.page.get_by_label("Provider", exact=True).select_option("anthropic")
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        with self.api_response("PUT", "/api/model"):
            self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        self.open_settings()
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("")
        self.assertNotIn("synthetic-key-only", self.page.evaluate("JSON.stringify([localStorage, sessionStorage])"))
        expect(self.page.get_by_label("API key", exact=True)).to_have_attribute("type", "password")
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("anthropic")
        expect(self.page.locator("#credentialStatus")).to_contain_text("Model selected")
        expect(self.page.locator("#credentialStatus")).to_contain_text("authentication checked on use")
        self.page.get_by_label("Credential reference", exact=True).fill("synthetic-reference")
        with self.api_response("PUT", "/api/model"):
            self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        saved = [call[2] for call in self.host.calls if call[:2] == ("PUT", "/api/model")]
        self.assertNotIn("apiKey", saved[-1])
        self.assertEqual(saved[-1]["credentialRef"], "synthetic-reference")
        self.open_settings()
        self.host.failures["PUT", "/api/model"] = "Rejected synthetic-key-only"
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        with self.api_response("PUT", "/api/model", 409):
            self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#modelError")).to_contain_text("Rejected")
        expect(self.page.locator("#modelError")).not_to_contain_text("synthetic-key-only")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("synthetic-key-only")
        self.page.keyboard.press("Escape")
        expect(self.page.get_by_role("button", name="Settings", exact=True)).to_be_focused()

    def test_blank_provider_defaults_without_an_unsupported_warning(self):
        self.host.model.update(provider="", model="", configured=False)
        self.open()
        expect(self.page.locator("#modelCredential")).to_contain_text("Model not selected")
        self.open_settings()
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("openai")
        expect(self.page.locator("#modelError")).not_to_be_visible()
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("")

    def test_selected_model_does_not_claim_authentication_is_verified(self):
        self.host.model.update(provider="openai", configured=True, credentialRef="")
        self.open()
        expect(self.page.locator("#modelCredential")).to_have_text("Model selected / authentication checked on use")
        self.host.model["credentialConfigured"] = True
        self.host.model["model"] = "synthetic-stored-credential-model"
        with self.api_response("GET", "/api/status"):
            self.host.emit("status")
        expect(self.page.locator("#modelSummary")).to_contain_text("synthetic-stored-credential-model")
        expect(self.page.locator("#modelCredential")).to_have_text("Model selected / authentication checked on use")
        self.host.model["authStatus"] = "verified"
        with self.api_response("GET", "/api/status"):
            self.host.emit("status")
        expect(self.page.locator("#modelCredential")).to_have_text("Model selected / authentication verified")

    def test_supported_providers_keep_credential_fields_available(self):
        self.open()
        self.open_settings()
        for provider in ("openai", "anthropic"):
            self.page.get_by_label("Provider", exact=True).select_option(provider)
            expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_have_count(0)
            expect(self.page.get_by_label("API key", exact=True)).to_be_enabled()
            expect(self.page.get_by_label("Credential reference", exact=True)).to_be_enabled()

    def test_safe_text_sandbox_preview_and_download(self):
        self.open()
        self.page.get_by_role("button", name="draft & notes.txt", exact=False).click()
        expect(self.page.locator("#fileContent")).to_contain_text("<img src=x onerror=alert(1)>")
        expect(self.page.locator("#fileContent img")).to_have_count(0)
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
        self.open()
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

    def test_settings_are_not_editable_before_configuration_loads(self):
        self.open()
        with self.hold_api("GET", "/api/model") as gate:
            self.page.get_by_role("button", name="Settings", exact=True).click()
            self.wait_for_gate(gate)
            expect(self.page.get_by_label("Provider", exact=True)).to_be_disabled()
            expect(self.page.get_by_role("button", name="Save settings")).to_be_disabled()
            with self.api_response("GET", "/api/model"):
                gate.release.set()
        expect(self.page.get_by_label("Provider", exact=True)).to_be_enabled()
        expect(self.page.get_by_label("Provider", exact=True)).to_be_focused()

    def test_failed_history_load_is_not_presented_as_a_loaded_conversation(self):
        self.host.failures["GET", "/api/sessions/s1"] = "Conversation could not be read"
        self.open()
        expect(self.page.locator("#notice")).to_contain_text("Conversation could not be read")
        expect(self.page.get_by_label("Message", exact=True)).to_be_disabled()
        del self.host.failures["GET", "/api/sessions/s1"]
        self.page.get_by_role("button", name="Refresh host").click()
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
        self.host.failures["GET", "/api/status"] = "Host not ready"
        self.page.goto(self.url)
        expect(self.page.locator("#notice")).to_contain_text("Host not ready")
        expect(self.page.get_by_label("Message", exact=True)).to_be_disabled()
        del self.host.failures["GET", "/api/status"]
        self.page.route("**/api/events", lambda route: route.fulfill(status=204))
        self.page.get_by_role("button", name="Refresh host").click()
        expect(self.page.locator("#notice")).to_contain_text("Live event stream closed")
        self.page.unroute("**/api/events")
        self.page.get_by_role("button", name="Refresh host").click()
        expect(self.page.locator("#connection")).to_have_text("Live updates connected")

    def test_file_and_status_events_refresh_an_open_artifact_and_model_summary(self):
        self.open()
        self.page.get_by_role("button", name="draft & notes.txt", exact=False).click()
        self.page.get_by_role("button", name="Expand preview").click()
        self.host.contents["draft & notes.txt"] = "Updated by the synthetic host."
        self.original_files = dict(self.host.contents)
        self.host.emit("files_changed", path="draft & notes.txt")
        expect(self.page.locator("#expandedText")).to_have_text("Updated by the synthetic host.")
        self.host.model["provider"] = "anthropic"
        self.host.emit("status")
        expect(self.page.locator("#modelSummary")).to_contain_text("anthropic")

    def test_mobile_navigation_keyboard_and_layout(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.open()
        self.assertEqual(self.page.locator("#chatScroll").evaluate("element => element.scrollTop"), 0)
        self.screenshot("mobile-chat.png")
        self.page.get_by_role("button", name="Conversations", exact=True).click()
        expect(self.page.locator("#sessionList")).to_be_visible()
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        expect(self.page.get_by_label("Message", exact=True)).to_be_visible()
        self.page.get_by_role("button", name="Files", exact=True).click()
        self.page.get_by_role("button", name="sample.html", exact=False).click()
        self.screenshot("mobile-files.png")
        self.page.get_by_role("button", name="Settings", exact=True).click()
        expect(self.page.get_by_label("Provider", exact=True)).to_be_focused()
        self.page.keyboard.press("Escape")
        self.assertTrue(self.page.evaluate("document.documentElement.scrollWidth <= innerWidth"))
        self.page.get_by_role("button", name="Chat", exact=True).click()
        self.page.get_by_label("Message", exact=True).fill("First line")
        self.page.get_by_label("Message", exact=True).press("Enter")
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("First line\n")
        for width in (320, 768, 960, 1024):
            self.page.set_viewport_size({"width": width, "height": 844})
            self.assertTrue(self.page.evaluate("document.documentElement.scrollWidth <= innerWidth"), width)


if __name__ == "__main__":
    unittest.main()

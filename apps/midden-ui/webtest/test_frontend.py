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
GitHub Copilot uses the canonical provider name github-copilot; older
github_copilot responses are normalized by the selector. Host notices remain
visible separately from dismissible request errors.
configured means a model was selected, not that credentials were verified.
Device-auth tests mock the start/poll API, NOT live OAuth or Copilot entitlement.
interval is seconds (including optional poll-response updates); expiresAt is an
RFC3339 timestamp. Only the public user code and opaque flow ID reach the UI.
Cancel/close stops client polling; unfinished server flows expire, not revoke.
Only an explicit authStatus of "verified" is treated as verified authentication.
Preview fixtures use the backend's inline-script-only CSP with an opaque sandbox
origin and no network access. They exercise runtime artifacts, not live decks.
The host must never return API keys, and must replay outstanding permissions
after reconnect/reload. Live provider execution, credential storage, cancellation
and file-scope enforcement remain parent-host end-to-end acceptance work.
"""

from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import queue
import threading
import time
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


class MockHost:
    def __init__(self):
        self.model = dict(provider="openai", model="synthetic-model", endpoint="",
                          credentialRef="", configured=True, credentialConfigured=False,
                          authStatus="Credentials are checked by the provider when used.")
        self.login = dict(id="synthetic-login", userCode="ABCD-EFGH",
                          verificationURI="https://github.com/login/device", interval=5,
                          expiresAt="2030-01-01T00:05:00Z")
        self.login_results = [dict(status="pending", interval=10), dict(status="success")]
        self.login_model = ""
        self.sessions = {
            "s1": dict(id="s1", title="Research notes", messages=[], updated="2026-01-01T12:00:00Z"),
            "s2": dict(id="s2", title="Earlier conversation", updated="2026-01-01T11:00:00Z",
                       messages=[dict(role="assistant", content="An earlier observation.", at="2026-01-01T11:00:00Z")]),
        }
        self.contents = {"draft & notes.txt": "<img src=x onerror=alert(1)>\nSynthetic notes.",
                         "sample.html": PREVIEW_HTML}
        self.active = None
        self.calls, self.clients, self.failures = [], [], {}
        self.seq, self.turns = 0, 0
        self.fast_finish = False
        self.running = True

    def emit(self, kind, **fields):
        self.seq += 1
        event = dict(seq=self.seq, type=kind, sessionId="s1", turnId="t1")
        event.update(fields)
        if kind == "message":
            self.sessions[event["sessionId"]]["messages"].append(
                dict(role="assistant", content=event["text"], at="2026-01-01T12:01:00Z"))
        if kind in ("turn_done", "error"):
            self.active = None
        for client in self.clients.copy():
            client.put(f"id: {self.seq}\nevent: message\ndata: {json.dumps(event)}\n\n")

    def handler(self):
        host = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

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
                if path == "/api/events":
                    client = queue.Queue()
                    host.clients.append(client)
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
                if path in ("/", "/app.js", "/style.css"):
                    asset = WEB / (path[1:] or "index.html")
                    mime = {".html": "text/html", ".js": "text/javascript", ".css": "text/css"}[asset.suffix]
                    return self.reply(asset.read_bytes() if asset.exists() else b"UI not implemented", mime=mime)
                if path == "/api/status":
                    return self.reply(dict(workspace="Synthetic workspace", coreVersion="test-core",
                        kernelVersion="test-candidate", model=host.model, activeTurn=host.active,
                        notice="Kernel candidate build. Approved shell commands run with your account; this is not an OS sandbox.",
                        csrfToken="synthetic-csrf", bundles=[
                            dict(name="Investigation", description="Understand source material and its limits."),
                            dict(name="Presentation", description="Create an editable, inspectable presentation.")]))
                if path == "/api/model":
                    return self.reply(host.model)
                if path == "/api/sessions":
                    return self.reply({"sessions": list(host.sessions.values())})
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
                host.calls.append((self.command, path, body, token))
                if token != "synthetic-csrf":
                    return self.reply({"error": "Missing CSRF token"}, 403)
                if (self.command, path) in host.failures:
                    return self.reply({"error": host.failures[self.command, path]}, 409)
                if path == "/api/model":
                    host.model = {key: value for key, value in body.items() if key != "apiKey"}
                    host.model["configured"] = bool(body.get("model"))
                    host.model["credentialConfigured"] = bool(body.get("apiKey") or body.get("credentialRef"))
                    return self.reply(host.model)
                if path == "/api/auth/copilot/start":
                    host.login_model = body["model"]
                    return self.reply(host.login)
                if path == "/api/auth/copilot/poll":
                    if body != {"id": host.login["id"]}:
                        return self.reply({"error": "Wrong synthetic sign-in ID"}, 400)
                    result = host.login_results.pop(0) if len(host.login_results) > 1 else host.login_results[0]
                    if result["status"] == "success":
                        host.model.update(provider="github-copilot", model=host.login_model,
                                          configured=True, credentialConfigured=True)
                    return self.reply(result)
                if path == "/api/sessions":
                    sid = f"s{len(host.sessions) + 1}"
                    host.sessions[sid] = dict(id=sid, title="New conversation", messages=[], updated="2026-01-01T12:00:00Z")
                    return self.reply(dict(id=sid, title="New conversation"))
                if path.endswith("/turn"):
                    sid = path.split("/")[3]
                    host.turns += 1
                    host.active = dict(turnId=f"t{host.turns}", sessionId=sid)
                    host.sessions[sid]["messages"].append(dict(role="user", content=body["message"], at="2026-01-01T12:00:00Z"))
                    tid = host.active["turnId"]
                    if host.fast_finish:
                        host.emit("message", sessionId=sid, turnId=tid, text="A fast response.")
                        host.emit("turn_done", sessionId=sid, turnId=tid)
                        time.sleep(0.15)
                    return self.reply({"turnId": tid}, 202)
                if path == "/api/cancel" or path.startswith("/api/permissions/"):
                    return self.reply({})
                self.reply({"error": "Unknown mock mutation"}, 404)

            do_PUT = do_POST

        return Handler


class FrontendTests(unittest.TestCase):
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
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), self.host.handler())
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.context = self.browser.new_context(viewport={"width": 1440, "height": 960})
        self.external_requests = []
        self.context.route("https://preview-network.invalid/**", self.external_probe)
        self.page = self.context.new_page()
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
        self.assertNotIn("synthetic-key-only", "\n".join(self.console))
        self.assertEqual(self.host.contents, self.original_files)
        for method, _, _, token in self.host.calls:
            if method != "GET":
                self.assertEqual(token, "synthetic-csrf")

    def open(self):
        self.page.goto(self.url)
        expect(self.page.locator("#connection")).to_have_text("Live updates connected")
        expect(self.page.locator("#sessionList button")).to_have_count(len(self.host.sessions))

    def screenshot(self, name):
        if directory := os.environ.get("MIDDEN_UI_SCREENSHOTS"):
            target = Path(directory)
            target.mkdir(parents=True, exist_ok=True)
            self.page.screenshot(path=str(target / name), full_page=True)

    def start_turn(self):
        self.page.get_by_label("Message", exact=True).fill("Help me turn these notes into a short explanation.")
        self.page.get_by_label("Message", exact=True).press("Control+Enter")
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_enabled()

    def test_boot_history_new_conversation_and_draft_preservation(self):
        self.open()
        expect(self.page.locator("#workspace")).to_have_text("Synthetic workspace")
        expect(self.page.locator("#kernelVersion")).to_contain_text("test-candidate")
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
        requests = []
        self.page.route("**/api/sessions", lambda route:
                        requests.append(route) if route.request.method == "POST" else route.continue_())
        with self.page.expect_request(lambda request:
                                      request.method == "POST" and request.url.endswith("/api/sessions")):
            self.page.locator("#newSession").click()
        try:
            expect(message).to_be_disabled()
            expect(self.page.locator("#send")).to_be_disabled()
        finally:
            requests[0].continue_()
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
        self.page.locator(".permission").nth(0).get_by_role("button", name="Allow", exact=True).click()
        self.page.locator(".permission").nth(1).get_by_role("button", name="Deny", exact=True).click()
        expect(self.page.locator(".permission").nth(0)).to_contain_text("Allowed")
        expect(self.page.locator(".permission").nth(1)).to_contain_text("Denied")
        decisions = [call[2] for call in self.host.calls if call[1].startswith("/api/permissions/")]
        self.assertEqual(decisions, [{"allow": True}, {"allow": False}])
        self.page.get_by_role("button", name="Stop", exact=True).click()
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_disabled()
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
        self.page.get_by_role("button", name="Settings", exact=True).click()
        self.page.get_by_label("Provider", exact=True).select_option("github-copilot")
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        self.page.get_by_role("button", name="Settings", exact=True).click()
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("")
        self.assertNotIn("synthetic-key-only", self.page.evaluate("JSON.stringify([localStorage, sessionStorage])"))
        expect(self.page.get_by_label("API key", exact=True)).to_have_attribute("type", "password")
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("github-copilot")
        expect(self.page.locator("#credentialStatus")).to_contain_text("Model selected")
        expect(self.page.locator("#credentialStatus")).to_contain_text("authentication checked on use")
        self.page.get_by_label("Credential reference", exact=True).fill("synthetic-reference")
        self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        saved = [call[2] for call in self.host.calls if call[:2] == ("PUT", "/api/model")]
        self.assertNotIn("apiKey", saved[-1])
        self.assertEqual(saved[-1]["credentialRef"], "synthetic-reference")
        self.page.get_by_role("button", name="Settings", exact=True).click()
        self.host.failures["PUT", "/api/model"] = "Rejected synthetic-key-only"
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#modelError")).to_contain_text("Rejected")
        expect(self.page.locator("#modelError")).not_to_contain_text("synthetic-key-only")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("")
        self.page.keyboard.press("Escape")
        expect(self.page.get_by_role("button", name="Settings", exact=True)).to_be_focused()

    def test_saved_copilot_provider_is_selected_without_an_unsupported_warning(self):
        self.open()
        for provider in ("github-copilot", "github_copilot"):
            self.host.model["provider"] = provider
            self.page.get_by_role("button", name="Settings", exact=True).click()
            expect(self.page.get_by_label("Provider", exact=True)).to_have_value("github-copilot")
            expect(self.page.locator("#modelError")).not_to_be_visible()
            self.page.keyboard.press("Escape")

    def test_blank_provider_defaults_without_an_unsupported_warning(self):
        self.host.model.update(provider="", model="", configured=False)
        self.open()
        expect(self.page.locator("#modelCredential")).to_contain_text("Model not selected")
        self.page.get_by_role("button", name="Settings", exact=True).click()
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("openai")
        expect(self.page.locator("#modelError")).not_to_be_visible()
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("")

    def test_selected_model_does_not_claim_authentication_is_verified(self):
        self.host.model.update(provider="github-copilot", configured=True, credentialRef="")
        self.open()
        expect(self.page.locator("#modelCredential")).to_have_text("Model selected / authentication checked on use")
        self.host.model["credentialConfigured"] = True
        self.host.emit("status")
        expect(self.page.locator("#modelCredential")).to_have_text("Model selected / authentication checked on use")
        self.host.model["authStatus"] = "verified"
        self.host.emit("status")
        expect(self.page.locator("#modelCredential")).to_have_text("Model selected / authentication verified")

    def copilot_settings(self):
        self.page.clock.install(time=datetime(2030, 1, 1, tzinfo=timezone.utc))
        self.page.clock.pause_at(datetime(2030, 1, 1, 0, 0, 1, tzinfo=timezone.utc))
        self.open()
        self.page.get_by_role("button", name="Settings", exact=True).click()
        self.page.get_by_label("Provider", exact=True).select_option("github-copilot")
        self.page.get_by_label("Model", exact=True).fill("synthetic-exact-model")
        expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_be_visible()

    def login_polls(self):
        return [call for call in self.host.calls if call[1] == "/api/auth/copilot/poll"]

    def test_copilot_sign_in_obeys_server_intervals_and_keeps_the_exact_model(self):
        self.copilot_settings()
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        expect(self.page.locator("#authLink")).to_have_attribute("href", "https://github.com/login/device")
        expect(self.page.locator("#authLink")).to_have_attribute("rel", "noopener noreferrer")
        expect(self.page.get_by_label("Model", exact=True)).to_be_disabled()
        self.screenshot("copilot-sign-in.png")
        self.page.clock.run_for(4999)
        self.assertEqual(self.login_polls(), [])
        self.page.clock.run_for(1)
        expect(self.page.locator("#authStatus")).to_contain_text("10 seconds")
        self.assertEqual(len(self.login_polls()), 1)
        self.page.clock.run_for(9999)
        self.assertEqual(len(self.login_polls()), 1)
        self.page.clock.run_for(1)
        expect(self.page.locator("#authStatus")).to_contain_text("GitHub sign-in complete")
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("github-copilot")
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("synthetic-exact-model")
        expect(self.page.locator("#modelSummary")).to_contain_text("synthetic-exact-model")
        expect(self.page.locator("#modelCredential")).to_have_text("Model selected / authentication checked on use")
        expect(self.page.locator("#authUserCode")).to_have_text("")
        self.assertIsNone(self.page.locator("#authLink").get_attribute("href"))
        self.assertIn(("POST", "/api/auth/copilot/start", {"model": "synthetic-exact-model"}, "synthetic-csrf"), self.host.calls)
        self.assertTrue(all(call[2] == {"id": "synthetic-login"} for call in self.login_polls()))
        self.assertFalse(any(call[0] == "PUT" for call in self.host.calls))
        self.assertNotIn("ABCD-EFGH", self.page.evaluate("JSON.stringify([localStorage, sessionStorage])"))
        self.page.clock.run_for(20000)
        self.assertEqual(len(self.login_polls()), 2)

    def test_sign_in_is_copilot_only_and_other_credential_fields_stay_available(self):
        self.open()
        self.page.get_by_role("button", name="Settings", exact=True).click()
        for provider in ("openai", "anthropic", "openai-codex"):
            self.page.get_by_label("Provider", exact=True).select_option(provider)
            expect(self.page.get_by_role("button", name="Sign in with GitHub")).not_to_be_visible()
            expect(self.page.get_by_label("API key", exact=True)).to_be_enabled()
            expect(self.page.get_by_label("Credential reference", exact=True)).to_be_enabled()

    def test_cancel_and_close_clear_codes_and_stop_future_polls(self):
        self.copilot_settings()
        for close in (False, True):
            self.page.get_by_role("button", name="Sign in with GitHub").click()
            expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
            if close:
                self.page.keyboard.press("Escape")
            else:
                self.page.get_by_role("button", name="Cancel sign-in").click()
            expect(self.page.locator("#authUserCode")).to_have_text("")
            self.assertIsNone(self.page.locator("#authLink").get_attribute("href"))
            self.page.clock.run_for(20000)
            self.assertEqual(self.login_polls(), [])
        self.page.get_by_role("button", name="Settings", exact=True).click()
        expect(self.page.locator("#authProgress")).not_to_be_visible()

    def test_device_code_expiry_stops_before_the_next_poll(self):
        self.host.login["expiresAt"] = "2030-01-01T00:00:05Z"
        self.copilot_settings()
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        self.page.clock.run_for(3999)
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        self.page.clock.run_for(1)
        expect(self.page.locator("#authStatus")).to_contain_text("expired")
        expect(self.page.locator("#authUserCode")).to_have_text("")
        expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_be_enabled()
        self.page.clock.run_for(20000)
        self.assertEqual(self.login_polls(), [])

    def test_unsafe_verification_links_are_rejected_without_polling(self):
        self.copilot_settings()
        for uri in ("http://github.com/login/device", "https://github.com.evil.test/login/device",
                    "https://github.com@evil.test/login/device", "javascript:alert(1)"):
            self.host.login["verificationURI"] = uri
            self.page.get_by_role("button", name="Sign in with GitHub").click()
            expect(self.page.locator("#modelError")).to_contain_text("unsafe GitHub verification link")
            expect(self.page.locator("#authUserCode")).to_have_text("")
            self.assertIsNone(self.page.locator("#authLink").get_attribute("href"))
            self.page.clock.run_for(10000)
            self.assertEqual(self.login_polls(), [])

    def test_sign_in_start_and_poll_errors_are_explicit_and_retryable(self):
        self.host.failures["POST", "/api/auth/copilot/start"] = "Synthetic sign-in unavailable"
        self.copilot_settings()
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#modelError")).to_contain_text("Synthetic sign-in unavailable")
        expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_be_enabled()
        del self.host.failures["POST", "/api/auth/copilot/start"]
        self.host.login_results = [dict(status="failed", error="Synthetic authorization denied")]
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        self.page.clock.run_for(5000)
        expect(self.page.locator("#modelError")).to_contain_text("Synthetic authorization denied")
        expect(self.page.locator("#authUserCode")).to_have_text("")
        expect(self.page.get_by_label("Model", exact=True)).to_be_enabled()
        self.page.clock.run_for(20000)
        self.assertEqual(len(self.login_polls()), 1)
        self.host.failures["POST", "/api/auth/copilot/poll"] = "Synthetic poll request failed"
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        self.page.clock.run_for(5000)
        expect(self.page.locator("#modelError")).to_contain_text("Synthetic poll request failed")
        expect(self.page.locator("#authUserCode")).to_have_text("")
        self.page.clock.run_for(20000)
        self.assertEqual(len(self.login_polls()), 2)

    def test_closing_during_start_does_not_restore_a_late_code(self):
        self.copilot_settings()
        requests = []
        self.page.route("**/api/auth/copilot/start", lambda route: requests.append(route))
        with self.page.expect_request("**/api/auth/copilot/start"):
            self.page.get_by_role("button", name="Sign in with GitHub").click()
        self.page.keyboard.press("Escape")
        for request in requests:
            request.fulfill(json=self.host.login)
        self.page.get_by_role("button", name="Settings", exact=True).click()
        expect(self.page.locator("#authProgress")).not_to_be_visible()
        expect(self.page.locator("#authUserCode")).to_have_text("")
        self.page.clock.run_for(20000)
        self.assertEqual(self.login_polls(), [])

    def test_pending_polls_do_not_overlap_and_cancel_ignores_a_late_success(self):
        self.copilot_settings()
        requests = []
        self.page.route("**/api/auth/copilot/poll", lambda route: requests.append(route))
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        with self.page.expect_request("**/api/auth/copilot/poll"):
            self.page.clock.run_for(5000)
        try:
            self.page.clock.run_for(20000)
            self.assertEqual(len(requests), 1)
            self.page.get_by_role("button", name="Cancel sign-in").click()
        finally:
            for request in requests:
                request.fulfill(json={"status": "success"})
        expect(self.page.locator("#authUserCode")).to_have_text("")
        expect(self.page.locator("#authStatus")).to_contain_text("polling stopped")
        self.page.clock.run_for(20000)
        self.assertEqual(len(requests), 1)
        self.assertEqual(len([call for call in self.host.calls if call[:2] == ("GET", "/api/model")]), 1)

    def test_mobile_sign_in_link_and_cancel_are_keyboard_accessible(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.copilot_settings()
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        expect(self.page.locator("#authLink")).to_be_focused()
        self.screenshot("copilot-sign-in-mobile.png")
        self.assertTrue(self.page.evaluate("document.documentElement.scrollWidth <= innerWidth"))
        self.page.keyboard.press("Tab")
        expect(self.page.get_by_role("button", name="Cancel sign-in")).to_be_focused()
        self.page.keyboard.press("Enter")
        expect(self.page.locator("#authUserCode")).to_have_text("")
        self.page.clock.run_for(20000)
        self.assertEqual(self.login_polls(), [])

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
        self.host.fast_finish = True
        self.open()
        self.page.get_by_label("Message", exact=True).fill("Give me a short explanation.")
        self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.page.locator("#messages .assistant pre")).to_have_text("A fast response.")
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
        requests = []
        self.page.route("**/api/model", lambda route: requests.append(route))
        self.page.get_by_role("button", name="Settings", exact=True).click()
        try:
            expect(self.page.get_by_label("Provider", exact=True)).to_be_disabled()
            expect(self.page.get_by_role("button", name="Save settings")).to_be_disabled()
        finally:
            for request in requests:
                request.fulfill(json=self.host.model)
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
        expect(self.page.locator("#notice")).to_contain_text("Synthetic kernel failure")
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

"""Exercise real loopback connection admission, not mocked fetch completion."""

import socket
import threading

from playwright.sync_api import expect

from test_frontend import BrowserCase


class HTTPTransportTests(BrowserCase):
    def test_gate_wait_allows_deferred_browser_requests_to_progress(self):
        self.open()
        routed = []

        def forward(route):
            routed.append(route.request)
            route.continue_()

        self.page.route("**/api/model", forward)
        try:
            with self.hold_api("GET", "/api/model") as gate:
                self.page.evaluate("""() => {
                    requestAnimationFrame(() => requestAnimationFrame(() => {
                        document.getElementById("openSettings").click();
                    }));
                }""")
                self.wait_for_gate(gate)
                self.assertEqual(len(routed), 1)
                expect(self.page.get_by_label("Model", exact=True)).to_be_disabled()
                with self.api_response("GET", "/api/model"):
                    gate.release.set()
            expect(self.page.get_by_label("Model", exact=True)).to_be_enabled()
        finally:
            self.page.unroute("**/api/model")

    def gated_exchange(self, method, path, action, status=200):
        before = len(self.host.calls)
        with self.hold_api(method, path) as gate:
            action()
            self.wait_for_gate(gate)
            self.assertFalse(any(call[:2] == (method, path) for call in self.host.calls[before:]))
            self.assertIn([method, path], self.page.evaluate("window.__middenTestRequests"))
            if method != "GET" and not any(call[:2] == (method, path) for call in self.host.calls[:before]):
                with self.assertRaisesRegex(AssertionError, "queued fetch starts"):
                    self.assert_no_api_calls(method, path)
            with self.api_response(method, path, status):
                gate.release.set()
        handled = [call for call in self.host.calls[before:] if call[:2] == (method, path)]
        self.assertEqual(len(handled), 1)
        return handled[0][2]

    def test_api_actions_use_responses_not_busy_state_as_receipts(self):
        self.open()
        self.gated_exchange("POST", "/api/sessions", self.page.locator("#newSession").click)
        expect(self.page.locator("#sessionTitle")).to_have_text("New conversation")
        self.assertIn("s3", self.host.sessions)
        message = "A synthetic request for the controlled transport test."
        self.page.get_by_label("Message", exact=True).fill(message)
        body = self.gated_exchange(
            "POST", "/api/sessions/s3/turn",
            self.page.get_by_role("button", name="Send", exact=True).click, 202)
        self.assertEqual(body, {"message": message})
        self.assertEqual(self.host.sessions["s3"]["messages"][-1]["content"], message)
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_enabled()

        for permission, allow in (("gated-allow", True), ("gated-deny", False)):
            self.host.emit("permission", sessionId="s3", permissionId=permission, tool=permission, arguments={})
        for permission, allow in (("gated-allow", True), ("gated-deny", False)):
            card = self.page.locator(f'[data-permission-id="{permission}"]')
            body = self.gated_exchange(
                "POST", f"/api/permissions/{permission}",
                card.get_by_role("button", name="Allow" if allow else "Deny", exact=True).click)
            self.assertEqual(body, {"allow": allow})
            expect(card).to_contain_text("Allowed" if allow else "Denied")
        body = self.gated_exchange(
            "POST", "/api/cancel", self.page.get_by_role("button", name="Stop", exact=True).click)
        self.assertEqual(body, {"turnId": "t1"})
        self.host.emit("turn_done", sessionId="s3")
        expect(self.page.locator("#turnStatus")).to_have_text("Ready")

        self.gated_exchange("GET", "/api/model", self.page.get_by_role("button", name="Settings", exact=True).click)
        expect(self.page.get_by_label("Model", exact=True)).to_be_enabled()
        self.page.get_by_label("Model", exact=True).fill("controlled/exact-model")
        self.assert_no_api_calls("POST", "/api/models")
        self.assert_no_api_calls("POST", "/api/model/check")
        self.assert_no_api_calls("PUT", "/api/model")
        for path, button, result in (
            ("/api/models", "Find models", "#modelCatalog"),
            ("/api/model/check", "Check model", "#modelCheckResult"),
        ):
            body = self.gated_exchange("POST", path, self.page.get_by_role("button", name=button, exact=True).click)
            self.assertEqual(body["model"], "controlled/exact-model")
            expect(self.page.locator(result)).to_be_visible()
            self.assertEqual(self.host.model["model"], "synthetic-model")
            self.assert_no_api_calls("PUT", "/api/model")
        body = self.gated_exchange(
            "PUT", "/api/model", self.page.get_by_role("button", name="Save settings", exact=True).click)
        self.assertEqual(body["model"], "controlled/exact-model")
        self.assertEqual(self.host.model["model"], "controlled/exact-model")
        expect(self.page.locator("#settings")).not_to_be_visible()

    def test_settings_response_survives_connection_burst_with_sse_open(self):
        self.open()
        expect(self.page.locator("#message")).to_be_enabled()
        expect(self.page.locator("#fileList button")).to_have_count(2)
        accept_gate, accept_waiting = threading.Event(), threading.Event()
        original_accept = self.server.get_request
        clients = []

        def gated_accept():
            accept_waiting.set()
            accept_gate.wait()
            return original_accept()

        self.server.get_request = gated_accept
        try:
            # Hold acceptance, not request handling, to reproduce a scheduler-delayed
            # burst of idle/preconnected sockets alongside the active browser SSE.
            for _ in range(5):
                clients.append(socket.create_connection(self.server.server_address, timeout=1))
            self.assertTrue(accept_waiting.wait(1), "The accept loop did not reach its gate")
            with self.page.expect_request("**/api/model") as model_request:
                self.page.get_by_role("button", name="Settings", exact=True).click()
            expect(self.page.get_by_label("Model", exact=True)).to_be_disabled()
            self.assertFalse(any(call[:2] == ("GET", "/api/model") for call in self.host.calls))

            # Observe whether another TCP handshake fits without sleeping or
            # extending the UI assertion deadline. This is a capacity probe.
            with socket.socket() as probe:
                probe.settimeout(0.25)
                try:
                    probe.connect(self.server.server_address)
                    admitted = True
                except TimeoutError:
                    admitted = False
            accept_gate.set()
            try:
                expect(self.page.get_by_label("Model", exact=True)).to_be_enabled()
            except AssertionError as failure:
                received = any(call[:2] == ("GET", "/api/model") for call in self.host.calls)
                raise AssertionError(
                    f"{failure}\nGET /api/model reached mock handler: {received}; "
                    f"browser network timing: {model_request.value.timing}"
                ) from failure
            self.assertTrue(admitted, "The mock listener rejected the concurrent connection burst")
            expect(self.page.get_by_label("Model", exact=True)).to_have_value("synthetic-model")
            expect(self.page.locator("#connection")).to_have_text("Live updates connected")
        finally:
            accept_gate.set()
            self.server.get_request = original_accept
            for client in clients:
                client.close()

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

        self.page.route("**/api/models/state", forward)
        try:
            with self.hold_api("GET", "/api/models/state") as gate:
                self.page.evaluate("""() => {
                    requestAnimationFrame(() => requestAnimationFrame(() => {
                        document.querySelector('nav.side a[data-nav="models"]').click();
                    }));
                }""")
                self.wait_for_gate(gate)
                self.assertEqual(len(routed), 1)
                expect(self.page.locator("#view-models").get_by_role("status")).to_have_text("Loading model connections...")
                with self.api_response("GET", "/api/models/state"):
                    gate.release.set()
            expect(self.page.get_by_label("Default model", exact=True)).to_be_enabled()
        finally:
            self.page.unroute("**/api/models/state")

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

        self.nav("Models").click()
        default = self.page.get_by_label("Default model", exact=True)
        expect(default).to_have_value("synthetic/synthetic-model")
        self.assert_no_api_calls("POST", "/api/models/instances/synthetic/check")
        self.assert_no_api_calls("PUT", "/api/models/default")
        instance = self.page.locator(".instance").filter(has_text="Synthetic service")
        instance.get_by_text("2 models", exact=True).click()
        row = instance.locator(".model-row").filter(has_text="other-model")
        body = self.gated_exchange("POST", "/api/models/instances/synthetic/check",
                                   row.get_by_role("button", name="Test tool calling", exact=True).click)
        self.assertEqual(body, {"model": "other-model"})
        expect(self.page.locator("#view-models .status-message")).to_have_text(
            "synthetic/other-model: Synthetic tool-calling probe succeeded.")
        expect(row).to_contain_text("Tool calling: tested")
        self.assertEqual(self.host.model["defaultModel"], "synthetic/synthetic-model")
        self.assert_no_api_calls("PUT", "/api/models/default")
        default.select_option("synthetic/other-model")
        choose = self.page.locator(".card").filter(has=self.page.get_by_role("heading", name="Default model"))
        body = self.gated_exchange(
            "PUT", "/api/models/default", choose.get_by_role("button", name="Use as default", exact=True).click)
        self.assertEqual(body, {"selection": "synthetic/other-model"})
        self.assertEqual(self.host.model["defaultModel"], "synthetic/other-model")
        expect(self.page.locator("#modelChip")).to_have_text("synthetic/other-model")
        expect(default).to_have_value("synthetic/other-model")

    def test_models_response_survives_connection_burst_with_sse_open(self):
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
            with self.page.expect_request("**/api/models/state") as state_request:
                self.nav("Models").click()
            expect(self.page.locator("#view-models").get_by_role("status")).to_have_text("Loading model connections...")
            self.assertFalse(any(call[:2] == ("GET", "/api/models/state") for call in self.host.calls))

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
                expect(self.page.get_by_label("Default model", exact=True)).to_be_enabled()
            except AssertionError as failure:
                received = any(call[:2] == ("GET", "/api/models/state") for call in self.host.calls)
                raise AssertionError(
                    f"{failure}\nGET /api/models/state reached mock handler: {received}; "
                    f"browser network timing: {state_request.value.timing}"
                ) from failure
            self.assertTrue(admitted, "The mock listener rejected the concurrent connection burst")
            expect(self.page.get_by_label("Default model", exact=True)).to_have_value("synthetic/synthetic-model")
            expect(self.page.locator("#connection")).to_have_text("Live updates connected")
        finally:
            accept_gate.set()
            self.server.get_request = original_accept
            for client in clients:
                client.close()

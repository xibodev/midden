"""Model connection UI checks against the synthetic host, not live inference.

Connections come from the host roster; the browser does not invent providers.
Keyless services are not offered as API-key connections.
No auth endpoint is provided by this fixture.
"""

from playwright.sync_api import expect

from test_frontend import BrowserCase


class CompaTests(BrowserCase):
    def connections(self):
        return [call[2] for call in self.host.calls if call[:2] == ("POST", "/api/models/instances")]

    def test_released_kernel_name_and_runtime_notice(self):
        self.open()
        expect(self.page.locator("#versions")).to_contain_text("powered by Compa v1.0.0")
        expect(self.page.locator("#versions")).not_to_contain_text("candidate")
        expect(self.page.locator("#hostNotice")).to_contain_text("released runtime")

    def test_supported_api_key_connections_keep_identity_and_allow_official_endpoint(self):
        self.host.roster.append(dict(id="free_service", label="Free service", defaultEndpoint="https://free.invalid/v1",
                                     requiresApiKey=False, requiresBaseUrl=False, keyless=True))
        self.open("#/models")
        cards = self.page.locator(".prov-card")
        expect(cards).to_have_count(2)
        expect(cards.filter(has_text="Free service")).to_have_count(0)
        for provider, label in (("openai", "OpenAI"), ("anthropic", "Anthropic")):
            with self.subTest(provider=provider):
                cards.filter(has_text=label).get_by_role("button", name="Connect", exact=True).click()
                dialog = self.page.get_by_role("dialog", name=f"Connect {label}")
                expect(dialog.get_by_label("Address")).to_have_value(f"https://{provider}.invalid/v1")
                dialog.get_by_label("API key").fill("synthetic-key-only")
                with self.api_response("POST", "/api/models/instances"):
                    dialog.get_by_role("button", name="Connect", exact=True).click()
                expect(dialog).to_have_count(0)
                expect(cards.filter(has_text=label)).to_contain_text("connected")
                self.assertEqual(self.connections()[-1], {"providerKind": provider, "apiKey": "synthetic-key-only"})
        self.assertNotIn("synthetic-key-only", self.page.evaluate("JSON.stringify([localStorage, sessionStorage])"))
        self.screenshot("compa-models.png")

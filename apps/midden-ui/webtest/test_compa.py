"""Model connection UI checks against the synthetic host, not live inference.

Only openai/anthropic compatible connections are available.
No auth endpoint is provided by this fixture.
"""

from playwright.sync_api import expect

from test_frontend import BrowserCase


class CompaTests(BrowserCase):
    def model_writes(self):
        return [call for call in self.host.calls if call[:2] == ("PUT", "/api/model")]

    def test_released_kernel_name_and_only_supported_provider_choices(self):
        self.open()
        expect(self.page.locator("#kernelVersion")).to_have_text("Compa v1.0.0")
        expect(self.page.locator("#kernelVersion")).not_to_contain_text("candidate")
        expect(self.page.locator("#hostNotice")).to_contain_text("released runtime")
        self.open_settings()
        self.assertEqual(self.page.locator("#provider option").evaluate_all(
            "options => options.map(option => option.value)"), ["openai", "anthropic"])
        expect(self.page.locator("#provider")).to_contain_text("OpenAI-compatible (API key or local server)")
        expect(self.page.locator("#provider")).to_contain_text("Anthropic-compatible")
        expect(self.page.locator("#endpointHelp")).to_contain_text("official provider")
        expect(self.page.locator("#endpointHelp")).to_contain_text("Local servers may not need an API key")
        self.screenshot("compa-model-settings.png")

    def test_supported_api_key_connections_keep_identity_and_allow_official_endpoint(self):
        self.open()
        for provider in ("openai", "anthropic"):
            with self.subTest(provider=provider):
                self.host.model.update(provider=provider, model="manual/exact-model", endpoint="",
                                       credentialRef="stored-api-key-reference", credentialConfigured=True)
                self.open_settings()
                expect(self.page.get_by_label("Provider", exact=True)).to_have_value(provider)
                expect(self.page.get_by_label("Credential reference", exact=True)).to_have_value("stored-api-key-reference")
                expect(self.page.locator("#providerNotice")).not_to_be_visible()
                self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
                with self.api_response("PUT", "/api/model"):
                    self.page.get_by_role("button", name="Save settings").click()
                expect(self.page.locator("#settings")).not_to_be_visible()
                saved = self.model_writes()[-1][2]
                self.assertEqual(saved["provider"], provider)
                self.assertEqual(saved["model"], "manual/exact-model")
                self.assertEqual(saved["endpoint"], "")
                self.assertEqual(saved["apiKey"], "synthetic-key-only")
        self.assertNotIn("synthetic-key-only", self.page.evaluate("JSON.stringify(localStorage)"))

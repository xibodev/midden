"""Compa migration UI regressions against the synthetic host, not live inference.

Only openai/anthropic compatible connections are available. Retired native
provider values remain visible until the operator explicitly saves a replacement.
No auth endpoint or extension application is provided by this fixture.
"""

from playwright.sync_api import expect

from test_frontend import BrowserCase


RECONNECT = (
    "This saved connection uses a retired native provider. Reconnect using an API-key "
    "or local compatible provider. Copilot/Codex would require a separately configured "
    "extension provider, which this host does not include."
)


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
        expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_have_count(0)
        expect(self.page.locator("#endpointHelp")).to_contain_text("official provider")
        expect(self.page.locator("#endpointHelp")).to_contain_text("Local servers may not need an API key")
        self.screenshot("compa-model-settings.png")

    def test_retired_native_values_require_reconnection_without_losing_work(self):
        self.open()
        for provider in ("github-copilot", "github_copilot", "openai-codex"):
            with self.subTest(provider=provider):
                self.host.model.update(provider=provider, model="saved-exact-model", credentialRef="legacy-reference",
                                       configured=False, credentialConfigured=True, authStatus="verified", setupError=RECONNECT)
                self.page.reload()
                expect(self.page.locator("#modelSetupError")).to_have_text(RECONNECT)
                expect(self.page.locator("#modelCredential")).to_contain_text("Reconnect required")
                expect(self.page.locator("#modelCredential")).not_to_contain_text("authentication verified")
                editor = self.page.get_by_label("Message", exact=True)
                editor.fill("Keep this draft while reconnecting.")
                expect(self.page.locator("#send")).to_be_disabled()
                expect(self.page.locator("#newSession")).to_be_disabled()
                editor.press("Control+Enter")
                self.open_settings()
                expect(self.page.get_by_label("Provider", exact=True)).to_have_value(provider)
                expect(self.page.locator("#provider option:checked")).to_be_disabled()
                expect(self.page.locator("#providerNotice")).to_have_text(RECONNECT)
                expect(self.page.get_by_role("button", name="Save settings")).to_be_disabled()
                expect(self.page.get_by_role("button", name="Find models", exact=True)).to_be_disabled()
                expect(self.page.get_by_role("button", name="Check model", exact=True)).to_be_disabled()
                expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_have_count(0)
                self.page.get_by_label("Model", exact=True).fill("edited-but-still-retired")
                expect(self.page.locator("#providerNotice")).to_be_visible()
                self.page.keyboard.press("Escape")
                self.page.reload()
                expect(editor).to_have_value("Keep this draft while reconnecting.")
                self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
                expect(self.page.locator("#messages")).to_contain_text("An earlier observation.")
                self.page.get_by_role("button", name="draft & notes.txt", exact=False).click()
                expect(self.page.locator("#fileContent")).to_contain_text("Synthetic notes.")
                self.page.get_by_role("button", name="Research notes", exact=False).click()
                self.assertEqual(self.host.model["provider"], provider)
        self.assert_no_api_calls()

    def test_retired_configuration_loaded_only_by_settings_is_not_silently_replaced(self):
        self.open()
        self.page.get_by_label("Message", exact=True).fill("Keep my connection-independent draft.")
        self.host.model.update(provider="openai-codex", configured=False, setupError=RECONNECT)
        self.open_settings()
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("openai-codex")
        expect(self.page.locator("#providerNotice")).to_have_text(RECONNECT)
        self.page.keyboard.press("Escape")
        expect(self.page.locator("#modelSetupError")).to_have_text(RECONNECT)
        expect(self.page.locator("#send")).to_be_disabled()
        self.assert_no_api_calls("PUT", "/api/model")

    def test_replacing_retired_connection_requires_explicit_supported_provider_save(self):
        self.host.model.update(provider="github-copilot", model="saved-exact-model", credentialRef="legacy-reference",
                               configured=False, credentialConfigured=True, setupError=RECONNECT)
        self.open()
        self.page.get_by_label("Message", exact=True).fill("Preserve this migration draft.")
        self.page.get_by_role("button", name="Set up model", exact=True).click()
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("github-copilot")
        self.page.get_by_label("Provider", exact=True).select_option("openai")
        expect(self.page.get_by_label("Credential reference", exact=True)).to_have_value("")
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("saved-exact-model")
        expect(self.page.locator("#providerNotice")).not_to_be_visible()
        self.assert_no_api_calls("PUT", "/api/model")
        self.page.get_by_label("Endpoint", exact=False).fill("http://127.0.0.1:1234/v1")
        self.page.get_by_label("Model", exact=True).fill("local/exact-model")
        with self.api_response("POST", "/api/models"):
            self.page.get_by_role("button", name="Find models", exact=True).click()
        expect(self.page.locator("#modelCatalog")).to_be_visible()
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("local/exact-model")
        with self.api_response("POST", "/api/model/check"):
            self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("succeeded")
        self.assertEqual(self.host.model["provider"], "github-copilot")
        self.assert_no_api_calls("PUT", "/api/model")
        with self.api_response("PUT", "/api/model"):
            self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        expect(self.page.locator("#modelSetupError")).not_to_be_visible()
        expect(self.page.locator("#send")).to_be_enabled()
        expect(self.page.get_by_label("Message", exact=True)).to_have_value("Preserve this migration draft.")
        saved = self.model_writes()[-1][2]
        self.assertEqual(saved, dict(provider="openai", model="local/exact-model",
                                     endpoint="http://127.0.0.1:1234/v1", credentialRef=""))
        for call in self.host.calls:
            if call[1] in ("/api/models", "/api/model/check"):
                self.assertNotIn("apiKey", call[2])
                self.assertEqual(call[2]["endpoint"], "http://127.0.0.1:1234/v1")

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

    def test_retired_reconnect_guidance_remains_readable_on_mobile(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.host.model.update(provider="openai-codex", configured=False, setupError=RECONNECT)
        self.open()
        expect(self.page.locator("#modelSetupError")).to_be_visible()
        expect(self.page.get_by_role("button", name="Set up model", exact=True)).to_be_in_viewport(ratio=1)
        expect(self.page.get_by_label("Message", exact=True)).to_be_in_viewport(ratio=1)
        self.assertTrue(self.page.evaluate("document.documentElement.scrollWidth <= innerWidth"))
        self.screenshot("compa-reconnect-mobile.png")
        self.page.get_by_role("button", name="Set up model", exact=True).click()
        expect(self.page.get_by_label("Provider", exact=True)).to_have_value("openai-codex")
        expect(self.page.locator("#providerNotice")).to_be_visible()

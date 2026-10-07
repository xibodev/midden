"""Journey shell checks against the synthetic host, not live core or kernel work.

Covers hash routes and the default view, navigation state, the model chip,
footer versions, deep links and the composer's context tray. Context chips are
appended to the sent message as a visible "Context selected in Midden:" block.
"""

import re

from playwright.sync_api import expect

from test_frontend import BrowserCase


VIEWS = ("sessions", "evidence", "sources", "files", "assistant", "models")
COLLECTION = dict(path="sources/synthetic", record_count=3, source_count=1, asset_count=0,
                  records_digest="c" * 64, warnings=[])
ARTICLE = "Write an article from these sources. Ask me about the audience and length first if they're unclear."
CONTEXT = ("Context selected in Midden:\n- Collection sources/synthetic (3 records from 1 source session, "
           "integrity verified). Read it with midden collection read/search sources/synthetic.")


class ShellTests(BrowserCase):
    def shows_only(self, name):
        for view in VIEWS:
            section = self.page.locator(f"#view-{view}")
            if view == name:
                expect(section).to_be_visible()
            else:
                expect(section).to_be_hidden()

    def test_default_route_shows_sessions(self):
        self.open("")
        self.shows_only("sessions")
        expect(self.page.get_by_role("heading", name="Sessions", level=1)).to_be_visible()
        expect(self.nav("Sessions")).to_have_attribute("aria-current", "page")
        self.page.evaluate("location.hash = '#/not-a-view'")
        self.shows_only("sessions")
        self.assert_no_api_calls()

    def test_navigation_marks_only_the_current_view(self):
        self.open("")
        current = self.page.locator("nav.side [aria-current]")
        for name in ("Sources", "Files", "Assistant", "Models", "Sessions"):
            with self.subTest(view=name):
                self.nav(name).click()
                self.shows_only(name.lower())
                expect(self.nav(name)).to_have_attribute("aria-current", "page")
                expect(current).to_have_count(1)
        self.page.evaluate("location.hash = '#/evidence'")
        self.shows_only("evidence")
        expect(self.page.get_by_text("No view open", exact=True)).to_be_visible()
        expect(self.nav("Sessions")).to_have_attribute("aria-current", "page")
        expect(current).to_have_count(1)
        self.page.go_back()
        self.shows_only("sessions")

    def test_model_chip_reports_configured_missing_and_failed_setup(self):
        self.open()
        chip = self.page.locator("#modelChip")
        expect(chip).to_have_text("synthetic/synthetic-model")
        expect(chip).to_have_class(re.compile(r"\bok\b"))
        expect(self.page.locator("#modelSetupNotice")).to_be_hidden()
        expect(self.page.locator("#setupModel")).to_be_hidden()
        self.host.model.update(configured=False, defaultModel="", summary="")
        with self.api_response("GET", "/api/status"):
            self.host.emit("status")
        expect(chip).to_have_text("No model connected")
        expect(chip).to_have_class(re.compile(r"\bwarn\b"))
        expect(self.page.locator("#modelSetupNotice")).to_be_hidden()
        expect(self.page.get_by_role("button", name="Set up model", exact=True)).to_be_visible()
        self.host.model.update(configured=True, defaultModel="synthetic/retired", summary="synthetic/retired",
                               setupError="The default model synthetic/retired is no longer offered.")
        with self.api_response("GET", "/api/status"):
            self.host.emit("status")
        expect(chip).to_have_text("Model setup needed")
        expect(self.page.locator("#modelSetupNotice")).to_be_visible()
        expect(self.page.locator("#modelSetupNotice")).to_contain_text("no longer offered")
        expect(self.page.locator("#turnStatus")).to_have_text("Model setup needed")
        self.page.get_by_label("Message", exact=True).fill("Wait until a model works.")
        expect(self.page.locator("#send")).to_be_disabled()
        chip.click()
        self.shows_only("models")
        expect(self.nav("Models")).to_have_attribute("aria-current", "page")
        expect(self.page.locator("#view-models .error")).to_contain_text("no longer offered")

    def test_footer_reports_versions_and_live_updates(self):
        self.open()
        footer = self.page.locator("footer")
        expect(footer.locator("#versions")).to_have_text("Midden test-ui · powered by Compa 3.0.0 · core test-core")
        expect(footer.locator("#connection")).to_have_text("Live updates connected")
        self.host.versions.update(uiVersion="", coreVersion="midden v9.9.9")
        with self.api_response("GET", "/api/status"):
            self.host.emit("status")
        expect(footer.locator("#versions")).to_have_text(
            "Midden (version not reported) · powered by Compa 3.0.0 · core v9.9.9")

    def test_deep_links_open_a_file_and_a_conversation(self):
        self.open("#/files/sample.html")
        self.shows_only("files")
        expect(self.page.locator("#fileName")).to_have_text("sample.html")
        expect(self.page.locator('#fileList [aria-current="true"]')).to_contain_text("sample.html")
        expect(self.page.frame_locator("#fileFrame").get_by_role("heading")).to_have_text("Rendered sample")
        self.page.evaluate("location.hash = '#/assistant/s2'")
        self.shows_only("assistant")
        expect(self.page.locator("#sessionTitle")).to_have_text("Earlier conversation")
        expect(self.page.locator("#messages")).to_contain_text("An earlier observation.")
        self.page.reload()
        expect(self.page.locator('#sessionList [aria-current="true"]')).to_contain_text("Earlier conversation")
        expect(self.page.locator("#messages")).to_contain_text("An earlier observation.")

    def collections(self):
        self.host.routes["GET", "/api/core/collections"] = lambda *_: dict(collections=[COLLECTION])
        self.host.routes["POST", "/api/core/collection/verify"] = lambda _handler, body: dict(
            valid=True, problems=[], path=body["path"])
        return self.page.locator(".collection").filter(has_text="sources/synthetic")

    def test_direct_sources_load_checks_integrity_once_the_host_supplies_csrf(self):
        card = self.collections()
        with self.hold_api("GET", "/api/status") as gate:
            with self.api_response("GET", "/api/core/collections"):
                self.page.goto(self.url + "/#/sources")
            self.wait_for_gate(gate)
            expect(card).to_contain_text("Not checked")
            self.assert_no_api_calls("POST", "/api/core/collection/verify")
            with self.api_response("POST", "/api/core/collection/verify"):
                gate.release.set()
        expect(card).to_contain_text("Integrity verified")
        self.assertEqual([call[2] for call in self.host.calls if call[1] == "/api/core/collection/verify"],
                         [{"path": "sources/synthetic"}])

    def test_sources_starter_hands_context_to_the_assistant(self):
        card = self.collections()
        self.open("#/sources")
        expect(card).to_contain_text("Integrity verified")
        card.get_by_role("button", name="Article", exact=True).click()
        self.shows_only("assistant")
        expect(self.nav("Assistant")).to_have_attribute("aria-current", "page")
        message = self.page.get_by_label("Message", exact=True)
        expect(message).to_have_value(ARTICLE)
        expect(self.page.locator("#contextChips")).to_contain_text("sources/synthetic")
        toggle = self.page.get_by_role("button", name="Preview what's sent")
        preview = self.page.locator("#contextPreview")
        expect(toggle).to_have_attribute("aria-expanded", "false")
        expect(preview).to_be_hidden()
        toggle.click()
        expect(toggle).to_have_attribute("aria-expanded", "true")
        expect(preview).to_be_visible()
        self.assertEqual(preview.text_content(), CONTEXT)
        with self.api_response("POST", "/api/sessions/s1/turn", 202):
            self.page.get_by_role("button", name="Send", exact=True).click()
        sent = [call[2] for call in self.host.calls if call[:2] == ("POST", "/api/sessions/s1/turn")]
        self.assertEqual(sent, [{"message": f"{ARTICLE}\n\n{CONTEXT}"}])
        expect(self.page.locator("#contextTray")).to_be_hidden()
        expect(message).to_have_value("")
        expect(self.page.locator("#messages .user pre")).to_have_text(f"{ARTICLE}\n\n{CONTEXT}")
        message.fill("Keep my own words.")
        self.nav("Sources").click()
        card.get_by_role("button", name="Presentation", exact=True).click()
        self.shows_only("assistant")
        expect(message).to_have_value("Keep my own words.")
        self.page.get_by_role("button", name="Remove sources/synthetic from the next message").click()
        expect(self.page.locator("#contextTray")).to_be_hidden()
        expect(message).to_be_focused()

"""Synthetic regressions for the operator assessment; no real data or inference.

Draft storage is scoped by stable workspaceId, never by CSRF or credentials.
Only composer text is persisted. Storage failures retain memory drafts and
activate an unload guard; migration saves the destination before removing its
source, and accepted messages clear only matching draft values.
Session GET is authoritative for outcomes; list responses deliberately omit them.
Catalog/check calls are explicit POSTs using ModelInput, without configuration
writes. Edits/close invalidate their results; keys remain on request failure and
clear on success or dialog close. Mocked results do not certify a live provider.
Mobile permission checks measure both button rectangles before test scrolling,
focus or clicks; replay may arrive before the conversation-history response.
"""

from playwright.sync_api import expect

from test_frontend import BrowserCase


class AssessmentTests(BrowserCase):
    def editor(self):
        return self.page.get_by_label("Message", exact=True)

    def open_model(self):
        self.open_settings()
        expect(self.page.get_by_label("Model", exact=True)).to_be_enabled()
        expect(self.page.get_by_role("button", name="Find models", exact=True)).to_be_visible()

    def calls_to(self, path):
        return [call for call in self.host.calls if call[1] == path]

    def unload_is_guarded(self):
        return self.page.evaluate("""() => {
            const event = new Event("beforeunload", {cancelable: true});
            window.dispatchEvent(event);
            return event.defaultPrevented;
        }""")

    def test_session_drafts_survive_reload_and_csrf_rotation(self):
        self.open()
        self.editor().fill("An unsent thought in the first conversation.")
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        self.editor().fill("A different unsent thought.")
        self.page.reload()
        expect(self.editor()).to_have_value("A different unsent thought.")
        self.page.get_by_role("button", name="Research notes", exact=False).click()
        expect(self.editor()).to_have_value("An unsent thought in the first conversation.")
        self.host.csrf = "synthetic-rotated-csrf"
        self.page.reload()
        expect(self.editor()).to_have_value("An unsent thought in the first conversation.")
        self.assertNotIn("synthetic-csrf", self.page.evaluate("JSON.stringify(Object.keys(localStorage))"))

    def test_unassigned_draft_restores_and_clears_only_after_acceptance(self):
        self.host.sessions.clear()
        self.open()
        self.editor().fill("Start from this unsent, unassigned thought.")
        self.page.reload()
        expect(self.editor()).to_have_value("Start from this unsent, unassigned thought.")
        self.assert_no_api_calls("POST", "/api/sessions/s1/turn")
        with self.api_response("POST", "/api/sessions/s1/turn", 202):
            self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.editor()).to_have_value("")
        self.page.reload()
        expect(self.editor()).to_be_editable()
        expect(self.editor()).to_have_value("")
        expect(self.page.locator("#messages .user")).to_have_count(1)
        expect(self.page.locator("#messages .user pre")).to_have_text("Start from this unsent, unassigned thought.")
        self.assertNotIn("Start from this unsent", self.page.evaluate("JSON.stringify(localStorage)"))

    def test_unassigned_draft_reload_waits_for_held_final_history(self):
        self.host.sessions.clear()
        self.open()
        text = "An accepted draft awaiting its restored history."
        self.editor().fill(text)
        # Finish the post-send refresh before gating only the reload's history GET.
        with self.api_response("GET", "/api/sessions/s1"):
            with self.api_response("POST", "/api/sessions/s1/turn", 202):
                self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.editor()).to_have_value("")
        with self.hold_api("GET", "/api/sessions/s1") as gate:
            self.page.reload()
            self.wait_for_gate(gate)
            expect(self.editor()).to_have_value("")
            expect(self.editor()).to_be_disabled()
            expect(self.page.locator("#messages .user")).to_have_count(0)
            self.assertEqual(len(self.host.sessions["s1"]["messages"]), 1)
            self.assertEqual(self.host.sessions["s1"]["messages"][0]["content"], text)
            with self.api_response("GET", "/api/sessions/s1"):
                gate.release.set()
        expect(self.editor()).to_be_editable()
        expect(self.editor()).to_have_value("")
        expect(self.page.locator("#messages .user")).to_have_count(1)
        expect(self.page.locator("#messages .user pre")).to_have_text(text)
        self.assertNotIn(text, self.page.evaluate("JSON.stringify(localStorage)"))

    def test_failed_send_preserves_the_persisted_draft(self):
        self.host.failures["POST", "/api/sessions/s1/turn"] = "Synthetic turn rejection"
        self.open()
        self.editor().fill("Keep this draft until the host accepts it.")
        with self.api_response("POST", "/api/sessions/s1/turn", 409):
            self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.page.locator("#notice")).to_contain_text("Synthetic turn rejection")
        self.page.reload()
        expect(self.editor()).to_have_value("Keep this draft until the host accepts it.")
        expect(self.editor()).to_be_editable()
        expect(self.page.locator("#messages .user")).to_have_count(0)

    def test_accepted_new_draft_clears_its_source_after_destination_storage_failure(self):
        self.host.sessions.clear()
        self.open()
        self.editor().fill("Accepted text must not reappear as an unassigned draft.")
        root_key = self.page.evaluate("Object.keys(localStorage).find(key => localStorage.getItem(key).startsWith('Accepted text'))")
        self.page.evaluate("""rootKey => {
            const original = Storage.prototype.setItem;
            Storage.prototype.setItem = function (key, value) {
                if (key !== rootKey) throw new DOMException("Synthetic quota failure", "QuotaExceededError");
                return original.call(this, key, value);
            };
        }""", root_key)
        with self.api_response("POST", "/api/sessions/s1/turn", 202):
            self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.editor()).to_have_value("")
        self.assertNotIn("Accepted text", self.page.evaluate("JSON.stringify(localStorage)"))

    def test_acceptance_does_not_clear_a_newer_matching_session_draft(self):
        self.open()
        self.editor().fill("The message being submitted.")
        with self.hold_api("POST", "/api/sessions/s1/turn") as gate:
            self.page.get_by_role("button", name="Send", exact=True).click()
            self.wait_for_gate(gate)
            self.editor().evaluate("""element => {
                element.value = "A newer unsent thought.";
                element.dispatchEvent(new Event("input", {bubbles: true}));
            }""")
            with self.api_response("POST", "/api/sessions/s1/turn", 202):
                gate.release.set()
        expect(self.editor()).to_be_editable()
        expect(self.editor()).to_have_value("A newer unsent thought.")
        self.assertEqual(self.host.sessions["s1"]["messages"][-1]["content"], "The message being submitted.")
        self.page.reload()
        expect(self.editor()).to_have_value("A newer unsent thought.")

    def test_boot_and_status_refresh_do_not_overwrite_a_restored_draft(self):
        self.open()
        self.editor().fill("Do not overwrite this during hydration.")
        with self.hold_api("GET", "/api/sessions") as gate:
            self.page.reload()
            self.wait_for_gate(gate)
            expect(self.editor()).to_be_disabled()
            with self.api_response("GET", "/api/sessions"):
                gate.release.set()
        expect(self.editor()).to_have_value("Do not overwrite this during hydration.")
        self.editor().fill("A freshly edited draft.")
        expect(self.page.locator("#connection")).to_have_text("Live updates connected")
        self.host.model["model"] = "synthetic-updated-model"
        self.host.emit("status")
        expect(self.page.locator("#modelSummary")).to_contain_text("synthetic-updated-model")
        expect(self.editor()).to_have_value("A freshly edited draft.")

    def test_workspace_reuse_never_restores_another_workspaces_draft(self):
        self.open()
        self.editor().fill("Only workspace A.")
        self.host.workspace_id = "workspace-b"
        self.host.workspace_name = "Synthetic workspace B"
        self.page.reload()
        expect(self.page.locator("#workspace")).to_have_text("Synthetic workspace B")
        expect(self.editor()).to_have_value("")
        self.editor().fill("Only workspace B.")
        self.host.workspace_id = "workspace-a"
        # Draft hydration can finish before the replacement live-only mock stream subscribes.
        with self.hold_api("GET", "/api/events") as gate:
            self.page.reload()
            self.wait_for_gate(gate)
            expect(self.editor()).to_have_value("Only workspace A.")
            expect(self.page.locator("#connection")).not_to_have_text("Live updates connected")
            with self.api_response("GET", "/api/events"):
                gate.release.set()
        expect(self.page.locator("#connection")).to_have_text("Live updates connected")
        self.host.workspace_id = "workspace-b"
        self.host.workspace_name = "Synthetic workspace B again"
        self.host.emit("status")
        expect(self.page.locator("#workspace")).to_have_text("Synthetic workspace B again")
        expect(self.editor()).to_have_value("Only workspace B.")

    def test_failed_storage_writes_keep_text_and_guard_unload_until_retry(self):
        self.page.add_init_script("""if (window === window.top) {
            window.originalSetItem = Storage.prototype.setItem;
            Storage.prototype.setItem = function () {
                throw new DOMException("Synthetic quota failure", "QuotaExceededError");
            }; }""")
        self.open()
        self.editor().fill("Unsaved because this browser rejected storage.")
        expect(self.page.locator("#draftStatus")).to_contain_text("not saved")
        self.assertTrue(self.unload_is_guarded())
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        self.page.get_by_role("button", name="Research notes", exact=False).click()
        expect(self.editor()).to_have_value("Unsaved because this browser rejected storage.")
        self.page.evaluate("() => { Storage.prototype.setItem = window.originalSetItem; }")
        self.page.get_by_role("button", name="Retry draft save").click()
        expect(self.page.locator("#draftStatus")).to_contain_text("saved on this device")
        self.assertFalse(self.unload_is_guarded())
        self.page.reload()
        expect(self.editor()).to_have_value("Unsaved because this browser rejected storage.")

    def test_unavailable_storage_reads_do_not_disable_drafting_or_hide_failure(self):
        self.page.add_init_script("""if (window === window.top) {
            window.originalStorage = window.localStorage;
            Object.defineProperty(window, "localStorage", {configurable: true, get() {
                throw new DOMException("Synthetic storage disabled", "SecurityError");
            }}); }""")
        self.open()
        self.editor().fill("Keep this text in the tab until storage is available.")
        expect(self.page.locator("#draftStatus")).to_contain_text("not saved")
        self.assertTrue(self.unload_is_guarded())
        self.page.evaluate("""() => { Object.defineProperty(window, "localStorage",
            {configurable: true, value: window.originalStorage}); }""")
        self.page.get_by_role("button", name="Retry draft save").click()
        expect(self.page.locator("#draftStatus")).to_contain_text("saved on this device")
        self.assertIn("Keep this text", self.page.evaluate("JSON.stringify(localStorage)"))

    def test_terminal_outcomes_are_durable_host_evidence_beside_user_messages(self):
        session = self.host.sessions["s1"]
        session["messages"] = [{"role": "user", "content": f"Synthetic request {index}."} for index in range(4)]
        session["messages"].append({"role": "assistant", "content": "Authored response."})
        session["outcomes"] = [
            dict(turnId=f"done-{index}", status=status, messageIndex=index,
                 at="2026-01-01T12:00:00Z", error="Synthetic host interruption" if status == "interrupted" else "")
            for index, status in enumerate(("failed", "cancelled", "interrupted", "completed"))
        ]
        self.open()
        for index, label in enumerate(("Failed", "Cancelled", "Interrupted")):
            expect(self.page.locator("#messages .user").nth(index).locator(".turn-outcome")).to_contain_text(f"Host status: {label}")
        expect(self.page.locator("#messages .turn-outcome")).to_have_count(3)
        expect(self.page.locator("#messages .assistant pre")).to_have_text("Authored response.")
        self.page.reload()
        expect(self.page.locator("#messages .turn-outcome").nth(2)).to_contain_text("Synthetic host interruption")

    def test_replayed_error_is_scoped_and_a_current_failure_survives_reload(self):
        self.host.sessions["s1"]["messages"] = [{"role": "user", "content": "Current request."}]
        self.host.sessions["s1"]["outcomes"] = [dict(turnId="current", status="running", messageIndex=0, at="2026-01-01T12:00:00Z")]
        self.host.sessions["s2"]["messages"] = [{"role": "user", "content": "Earlier request."}]
        self.host.sessions["s2"]["outcomes"] = [dict(turnId="old", status="failed", messageIndex=0, at="2026-01-01T11:00:00Z", error="Earlier synthetic failure")]
        self.host.active = dict(turnId="current", sessionId="s1")
        replay = [
            dict(type="delta", sessionId="s2", turnId="old", text="Earlier partial response."),
            dict(type="tool", sessionId="s2", turnId="old", tool="read_file", arguments={}),
            dict(type="permission", sessionId="s2", turnId="old", permissionId="old-permission", tool="write_file", arguments={}),
            dict(type="error", sessionId="s2", turnId="old", error="Earlier synthetic failure"),
        ]
        self.open()
        expect(self.editor()).to_be_enabled()
        with self.page.expect_response("**/api/sessions/s2"):
            for record in replay:
                self.host.emit(record["type"], **{key: value for key, value in record.items() if key != "type"})
        expect(self.page.locator("#notice")).not_to_be_visible()
        expect(self.page.locator("#turnStatus")).to_contain_text("Working")
        expect(self.page.locator("#messages")).not_to_contain_text("Earlier synthetic failure")
        self.host.emit("error", turnId="current", error="Current synthetic failure")
        expect(self.page.locator("#messages .user .turn-outcome")).to_contain_text("Current synthetic failure")
        self.host.replay.clear()
        self.page.reload()
        expect(self.page.locator("#messages .user .turn-outcome")).to_contain_text("Current synthetic failure")
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        expect(self.page.locator("#messages .turn-outcome")).to_contain_text("Earlier synthetic failure")
        expect(self.page.locator("#notice")).not_to_be_visible()

    def test_pending_permission_is_expanded_after_reload_and_switching(self):
        self.host.active = dict(turnId="pending", sessionId="s1")
        self.host.replay = [dict(type="permission", sessionId="s1", turnId="pending",
                                permissionId="write-pending", tool="write_file", arguments={"path": "synthetic-draft.txt"})]
        self.open()
        expect(self.page.locator("#activity")).to_have_attribute("open", "")
        expect(self.page.locator(".permission").get_by_role("button", name="Allow", exact=True)).to_be_enabled()
        self.page.get_by_role("button", name="Earlier conversation", exact=False).click()
        self.page.get_by_role("button", name="Research notes", exact=False).click()
        expect(self.page.locator("#activity")).to_have_attribute("open", "")
        self.page.reload()
        expect(self.page.locator("#activity")).to_have_attribute("open", "")
        expect(self.page.locator(".permission")).to_contain_text("synthetic-draft.txt")

    def test_first_run_allows_drafts_but_no_blank_chat_until_model_setup(self):
        self.host.sessions.clear()
        self.host.model.update(provider="", model="", configured=False)
        self.open()
        self.editor().fill("My first natural request, kept while I configure a model.")
        expect(self.page.get_by_role("button", name="Set up model", exact=True)).to_be_visible()
        expect(self.page.locator("#turnStatus")).to_contain_text("Set up model")
        expect(self.page.locator("#send")).to_be_disabled()
        expect(self.page.locator("#newSession")).to_be_disabled()
        self.editor().press("Control+Enter")
        self.assert_no_api_calls("POST")
        self.page.get_by_role("button", name="Set up model", exact=True).click()
        self.page.get_by_label("Model", exact=True).fill("manual/exact-model")
        with self.api_response("PUT", "/api/model"):
            self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        expect(self.page.locator("#send")).to_be_enabled()
        expect(self.editor()).to_have_value("My first natural request, kept while I configure a model.")
        self.assert_no_api_calls("POST")

    def test_catalog_and_check_are_explicit_and_do_not_change_configuration(self):
        self.open()
        self.open_model()
        self.page.get_by_label("Model", exact=True).fill("manual/exact-model")
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        self.assert_no_api_calls("POST", "/api/models")
        self.assert_no_api_calls("POST", "/api/model/check")
        with self.api_response("POST", "/api/models"):
            self.page.get_by_role("button", name="Find models", exact=True).click()
        expect(self.page.locator("#modelCatalog")).to_contain_text("Available model")
        expect(self.page.locator("#modelCatalogNote")).to_contain_text("not verified inference")
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("manual/exact-model")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("")
        self.assertEqual(self.host.model["model"], "synthetic-model")
        self.assert_no_api_calls("PUT", "/api/model")
        self.page.get_by_label("Discovered models", exact=True).select_option("synthetic/available")
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("synthetic/available")
        with self.api_response("POST", "/api/model/check"):
            self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("Synthetic tool-capability probe succeeded")
        self.assertEqual(self.host.model["model"], "synthetic-model")
        self.assert_no_api_calls("PUT", "/api/model")
        self.assertEqual(self.calls_to("/api/models")[0][2]["model"], "manual/exact-model")
        self.assertEqual(self.calls_to("/api/model/check")[0][2]["model"], "synthetic/available")
        self.assertNotIn("synthetic-key-only", self.page.evaluate("JSON.stringify(localStorage)"))

    def test_stale_catalog_response_cannot_replace_a_manual_model_or_clear_a_new_key(self):
        self.open()
        self.open_model()
        requests = []
        self.page.route("**/api/models", lambda route: requests.append(route))
        with self.page.expect_request("**/api/models"):
            self.page.get_by_role("button", name="Find models", exact=True).click()
        try:
            self.page.get_by_label("Model", exact=True).fill("new/manual-model")
            self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        finally:
            requests[0].fulfill(json=self.host.catalog)
        expect(self.page.locator("#modelCatalog")).not_to_be_visible()
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("new/manual-model")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("synthetic-key-only")

    def test_successful_model_notes_do_not_echo_a_supplied_key(self):
        self.open()
        self.open_model()
        self.host.catalog["note"] = "Synthetic note echoed synthetic-key-only."
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        with self.api_response("POST", "/api/models"):
            self.page.get_by_role("button", name="Find models", exact=True).click()
        expect(self.page.locator("#modelCatalogNote")).to_contain_text("Synthetic note")
        expect(self.page.locator("#modelCatalogNote")).not_to_contain_text("synthetic-key-only")
        self.host.model_check["message"] = "Synthetic probe echoed synthetic-key-only."
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        with self.api_response("POST", "/api/model/check"):
            self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("Synthetic probe")
        expect(self.page.locator("#modelCheckResult")).not_to_contain_text("synthetic-key-only")

    def test_provider_or_endpoint_changes_clear_credentials_before_find_check_and_save(self):
        self.open()
        for changed_field in ("provider", "endpoint"):
            with self.subTest(changed_field=changed_field):
                if self.page.locator("#settings").is_visible():
                    self.page.keyboard.press("Escape")
                self.host.model.update(provider="openai", endpoint="https://original.invalid/v1",
                                       credentialRef="original-service-reference", credentialConfigured=True)
                self.open_model()
                expect(self.page.get_by_label("Credential reference", exact=True)).to_have_value("original-service-reference")
                self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
                if changed_field == "provider":
                    self.page.get_by_label("Provider", exact=True).select_option("anthropic")
                else:
                    self.page.get_by_label("Endpoint", exact=False).fill("https://replacement.invalid/v1")
                expect(self.page.get_by_label("Credential reference", exact=True)).to_have_value("")
                expect(self.page.get_by_label("API key", exact=True)).to_have_value("")
                expect(self.page.locator("#credentialStatus")).to_contain_text("Credential fields cleared")
                with self.api_response("POST", "/api/models"):
                    self.page.get_by_role("button", name="Find models", exact=True).click()
                expect(self.page.locator("#modelCatalog")).to_be_visible()
                with self.api_response("POST", "/api/model/check"):
                    self.page.get_by_role("button", name="Check model", exact=True).click()
                expect(self.page.locator("#modelCheckResult")).to_contain_text("succeeded")
                with self.api_response("PUT", "/api/model"):
                    self.page.get_by_role("button", name="Save settings").click()
                expect(self.page.locator("#settings")).not_to_be_visible()
                for method, path in (("POST", "/api/models"), ("POST", "/api/model/check"), ("PUT", "/api/model")):
                    body = [call[2] for call in self.calls_to(path) if call[0] == method][-1]
                    self.assertEqual(body["credentialRef"], "")
                    self.assertNotIn("apiKey", body)

    def test_explicit_credentials_after_service_change_survive_model_only_edits(self):
        self.host.model.update(endpoint="https://original.invalid/v1",
                               credentialRef="original-service-reference", credentialConfigured=True)
        self.open()
        self.open_model()
        self.page.get_by_label("Endpoint", exact=False).fill("https://replacement.invalid/v1")
        self.page.get_by_label("Credential reference", exact=True).fill("operator-selected-reference")
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        self.page.get_by_label("Model", exact=True).fill("another/exact-model")
        self.page.get_by_label("Provider", exact=True).select_option("openai")
        self.page.get_by_label("Endpoint", exact=False).fill("https://replacement.invalid/v1")
        expect(self.page.get_by_label("Credential reference", exact=True)).to_have_value("operator-selected-reference")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("synthetic-key-only")
        with self.api_response("POST", "/api/model/check"):
            self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("succeeded")
        body = self.calls_to("/api/model/check")[-1][2]
        self.assertEqual(body["credentialRef"], "operator-selected-reference")
        self.assertEqual(body["apiKey"], "synthetic-key-only")
        self.assertEqual(body["model"], "another/exact-model")
        self.assertEqual(body["endpoint"], "https://replacement.invalid/v1")
        with self.api_response("PUT", "/api/model"):
            self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        saved = [call[2] for call in self.calls_to("/api/model") if call[0] == "PUT"][-1]
        self.assertEqual(saved["credentialRef"], "operator-selected-reference")
        self.assertNotIn("apiKey", saved)

    def test_check_result_is_invalidated_by_edits_and_errors_preserve_credentials(self):
        self.open()
        self.open_model()
        self.host.failures["POST", "/api/model/check"] = "Rejected synthetic-key-only"
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        with self.api_response("POST", "/api/model/check", 409):
            self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelError")).to_contain_text("Rejected")
        expect(self.page.locator("#modelError")).not_to_contain_text("synthetic-key-only")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("synthetic-key-only")
        del self.host.failures["POST", "/api/model/check"]
        requests = []
        self.page.route("**/api/model/check", lambda route: requests.append(route))
        with self.page.expect_request("**/api/model/check"):
            self.page.get_by_role("button", name="Check model", exact=True).click()
        try:
            self.page.get_by_label("Model", exact=True).fill("another/exact-model")
        finally:
            requests[0].fulfill(json=self.host.model_check)
        expect(self.page.locator("#modelCheckResult")).not_to_be_visible()
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("synthetic-key-only")
        self.page.unroute("**/api/model/check")
        with self.api_response("POST", "/api/model/check"):
            self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("succeeded")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("")

    def test_mobile_approval_has_room_despite_long_metadata_and_a_host_notice(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.host.model["model"] = "synthetic-model/" + "long-segment-" * 12
        self.host.sessions["s1"]["title"] = "Synthetic long conversation " + "title " * 40
        self.open()
        self.start_turn()
        self.host.emit("error", sessionId="", turnId="", error="Synthetic host notice. " + "detail " * 80)
        self.host.emit("permission", permissionId="mobile-one", tool="write_file",
                       arguments={"path": "synthetic-draft.txt", "content": "Synthetic editable line.\n" * 40})
        self.host.emit("permission", permissionId="mobile-two", tool="read_file", arguments={"path": "synthetic-notes.txt"})
        expect(self.page.locator("#activity")).to_have_attribute("open", "")
        area = self.page.locator("#chatScroll").bounding_box()
        self.assertGreaterEqual(area["height"], 240, "Approval must have a usable scrolling viewport")
        first = self.page.locator(".permission").nth(0)
        first.get_by_role("button", name="Allow", exact=True).scroll_into_view_if_needed()
        expect(first.get_by_role("button", name="Allow", exact=True)).to_be_in_viewport(ratio=1)
        self.assertGreaterEqual(first.get_by_role("button", name="Allow", exact=True).bounding_box()["height"], 44)
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_in_viewport(ratio=1)
        self.screenshot("mobile-approval-stress.png")
        first.get_by_text("Inspect arguments", exact=True).click()
        expect(first.locator("pre")).to_be_visible()
        expect(first.locator("pre")).to_contain_text("synthetic-draft.txt")
        first.get_by_role("button", name="Allow", exact=True).click()
        self.page.locator('[data-permission-id="mobile-two"]').get_by_role("button", name="Deny", exact=True).click()
        expect(self.page.locator('[data-permission-id="mobile-two"]')).to_contain_text("Denied")

    def assert_permission_buttons_inside_chat(self, card):
        self.page.evaluate("() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))")
        bounds = card.evaluate("""card => {
            const chat = document.getElementById("chatScroll").getBoundingClientRect();
            const composer = document.getElementById("composer").getBoundingClientRect();
            return {top: chat.top, bottom: Math.min(chat.bottom, composer.top),
                left: chat.left, right: chat.right,
                buttons: [...card.querySelectorAll(".permission-actions button")].map(button => {
                    const rect = button.getBoundingClientRect();
                    return {name: button.textContent, top: rect.top, bottom: rect.bottom,
                        left: rect.left, right: rect.right, height: rect.height};
                })};
        }""")
        self.assertEqual([button["name"] for button in bounds["buttons"]], ["Allow", "Deny"])
        for button in bounds["buttons"]:
            with self.subTest(button=button["name"], bounds=bounds):
                self.assertGreaterEqual(button["top"], bounds["top"])
                self.assertLessEqual(button["bottom"], bounds["bottom"])
                self.assertGreaterEqual(button["left"], bounds["left"])
                self.assertLessEqual(button["right"], bounds["right"])
                self.assertGreaterEqual(button["height"], 44)

    def test_mobile_allowed_history_does_not_hide_pending_write_after_replay(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.host.model["model"] = "synthetic-long-model-identifier-for-preview"
        self.host.sessions["s1"].update(
            title="Synthetic request with a read followed by a write",
            messages=[{"role": "user", "content": "Inspect synthetic material before writing a draft.\n" * 8}],
            outcomes=[dict(turnId="approval-turn", status="running", messageIndex=0, at="2026-01-01T12:00:00Z")])
        self.host.active = dict(turnId="approval-turn", sessionId="s1")
        sequence = [
            dict(type="tool", tool="midden", status="pending", arguments={"command": "ls"}),
            dict(type="permission", permissionId="allowed-read", tool="midden", arguments={"command": "ls"}),
            dict(type="permission_result", permissionId="allowed-read", allow=True),
            dict(type="tool", tool="midden", status="completed", arguments={"command": "ls"}),
            dict(type="tool", tool="write_file", status="pending", arguments={"path": "synthetic-draft.txt"}),
            dict(type="permission", permissionId="pending-write", tool="write_file",
                 arguments={"path": "synthetic-draft.txt", "content": "Synthetic draft.\n" * 20}),
        ]
        self.host.replay = [dict(sessionId="s1", turnId="approval-turn", **event) for event in sequence]
        write = self.page.get_by_role("region", name="Permission for write_file", exact=True)
        read = self.page.locator(".permission").filter(has_text="midden requests permission")
        self.open()
        expect(self.editor()).to_be_enabled()
        expect(write.get_by_role("button", name="Deny", exact=True)).to_be_enabled()
        with self.hold_api("GET", "/api/sessions/s1") as gate:
            self.page.reload()
            self.wait_for_gate(gate)
            expect(write.get_by_role("button", name="Deny", exact=True)).to_be_enabled()
            self.page.evaluate("() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))")
            with self.api_response("GET", "/api/sessions/s1"):
                gate.release.set()
        expect(self.editor()).to_be_enabled()
        expect(write.get_by_role("button", name="Deny", exact=True)).to_be_enabled()
        expect(read).to_contain_text("Allowed")
        expect(self.page.locator("#activityList .tool")).to_have_count(3)
        self.assert_permission_buttons_inside_chat(write)
        self.screenshot("mobile-pending-write-replay.png")
        expect(self.page.get_by_role("button", name="Stop", exact=True)).to_be_in_viewport(ratio=1)
        expect(read.get_by_role("button", name="Allow", exact=True)).not_to_be_visible()
        self.page.get_by_text("Previous activity (4)", exact=True).click()
        expect(read.get_by_role("button", name="Allow", exact=True)).to_be_disabled()
        expect(read).to_contain_text("Allowed")
        read.get_by_text("Inspect arguments", exact=True).click()
        expect(read.locator("pre")).to_contain_text('"command": "ls"')

    def test_mobile_live_permissions_retain_keyboard_focus_and_individual_decisions(self):
        self.page.set_viewport_size({"width": 390, "height": 844})
        self.open()
        self.start_turn()
        self.host.emit("permission", permissionId="read-first", tool="midden", arguments={"command": "ls"})
        read = self.page.get_by_role("region", name="Permission for midden", exact=True, include_hidden=True)
        with self.api_response("POST", "/api/permissions/read-first"):
            read.get_by_role("button", name="Allow", exact=True).click()
        expect(read).to_contain_text("Allowed")
        self.host.emit("tool", tool="midden", status="completed", arguments={"command": "ls"})
        self.host.emit("permission", permissionId="write-next", tool="write_file", arguments={"path": "synthetic-draft.txt"})
        write = self.page.get_by_role("region", name="Permission for write_file", exact=True)
        deny = write.get_by_role("button", name="Deny", exact=True)
        expect(deny).to_be_enabled()
        self.assert_permission_buttons_inside_chat(write)
        deny.focus()
        self.host.emit("permission", permissionId="read-next", tool="read_file", arguments={"path": "synthetic-notes.txt"})
        extra = self.page.get_by_role("region", name="Permission for read_file", exact=True)
        expect(extra.get_by_role("button", name="Allow", exact=True)).to_be_enabled()
        expect(deny).to_be_focused()
        self.assert_permission_buttons_inside_chat(write)
        with self.api_response("POST", "/api/permissions/write-next"):
            self.page.keyboard.press("Enter")
        expect(self.page.locator('[data-permission-id="write-next"]')).to_contain_text("Denied")
        expect(extra.get_by_role("button", name="Allow", exact=True)).to_be_focused()
        self.assert_permission_buttons_inside_chat(extra)
        with self.api_response("POST", "/api/permissions/read-next"):
            self.page.keyboard.press("Enter")
        expect(self.page.locator('[data-permission-id="read-next"]')).to_contain_text("Allowed")
        decisions = [(call[1], call[2]) for call in self.host.calls if call[1].startswith("/api/permissions/")]
        self.assertEqual(decisions, [
            ("/api/permissions/read-first", {"allow": True}),
            ("/api/permissions/write-next", {"allow": False}),
            ("/api/permissions/read-next", {"allow": True}),
        ])

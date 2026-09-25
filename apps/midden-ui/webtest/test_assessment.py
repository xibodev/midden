"""Synthetic regressions for the operator assessment; no real data or inference.

Draft storage is scoped by stable workspaceId, never by CSRF or credentials.
Only composer text is persisted. Storage failures retain memory drafts and
activate an unload guard; migration saves the destination before removing its
source, and accepted messages clear only matching draft values.
Session GET is authoritative for outcomes; list responses deliberately omit them.
Catalog/check calls are explicit POSTs using ModelInput, without configuration
writes. Edits/close invalidate their results; keys remain on request failure and
clear on success or dialog close. Mocked results do not certify a live provider.
"""

from playwright.sync_api import expect

from test_frontend import BrowserCase


class AssessmentTests(BrowserCase):
    def editor(self):
        return self.page.get_by_label("Message", exact=True)

    def open_model(self):
        self.page.get_by_role("button", name="Settings", exact=True).click()
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
        self.assertEqual(self.calls_to("/api/sessions/s1/turn"), [])
        self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.editor()).to_have_value("")
        self.page.reload()
        expect(self.editor()).to_have_value("")
        expect(self.page.locator("#messages .user")).to_have_count(1)
        self.assertNotIn("Start from this unsent", self.page.evaluate("JSON.stringify(localStorage)"))

    def test_failed_send_preserves_the_persisted_draft(self):
        self.host.failures["POST", "/api/sessions/s1/turn"] = "Synthetic turn rejection"
        self.open()
        self.editor().fill("Keep this draft until the host accepts it.")
        self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.page.locator("#notice")).to_contain_text("Synthetic turn rejection")
        self.page.reload()
        expect(self.editor()).to_have_value("Keep this draft until the host accepts it.")
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
        self.page.get_by_role("button", name="Send", exact=True).click()
        expect(self.editor()).to_have_value("")
        self.assertNotIn("Accepted text", self.page.evaluate("JSON.stringify(localStorage)"))

    def test_acceptance_does_not_clear_a_newer_matching_session_draft(self):
        self.open()
        self.editor().fill("The message being submitted.")
        requests = []
        self.page.route("**/api/sessions/s1/turn", lambda route: requests.append(route))
        with self.page.expect_request("**/api/sessions/s1/turn"):
            self.page.get_by_role("button", name="Send", exact=True).click()
        try:
            self.editor().evaluate("""element => {
                element.value = "A newer unsent thought.";
                element.dispatchEvent(new Event("input", {bubbles: true}));
            }""")
        finally:
            requests[0].continue_()
        expect(self.editor()).to_have_value("A newer unsent thought.")
        self.page.reload()
        expect(self.editor()).to_have_value("A newer unsent thought.")

    def test_boot_and_status_refresh_do_not_overwrite_a_restored_draft(self):
        self.open()
        self.editor().fill("Do not overwrite this during hydration.")
        requests = []
        self.page.route("**/api/sessions", lambda route: requests.append(route))
        with self.page.expect_request("**/api/sessions"):
            self.page.reload()
        try:
            expect(self.editor()).to_be_disabled()
        finally:
            requests[0].continue_()
            self.page.unroute("**/api/sessions")
        expect(self.editor()).to_have_value("Do not overwrite this during hydration.")
        self.editor().fill("A freshly edited draft.")
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
        self.page.reload()
        expect(self.editor()).to_have_value("Only workspace A.")
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
        self.assertFalse(any(call[0] == "POST" for call in self.host.calls))
        self.page.get_by_role("button", name="Set up model", exact=True).click()
        self.page.get_by_label("Model", exact=True).fill("manual/exact-model")
        self.page.get_by_role("button", name="Save settings").click()
        expect(self.page.locator("#settings")).not_to_be_visible()
        expect(self.page.locator("#send")).to_be_enabled()
        expect(self.editor()).to_have_value("My first natural request, kept while I configure a model.")
        self.assertFalse(any(call[0] == "POST" for call in self.host.calls))

    def test_catalog_and_check_are_explicit_and_do_not_change_configuration(self):
        self.open()
        self.open_model()
        self.page.get_by_label("Model", exact=True).fill("manual/exact-model")
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        self.assertEqual(self.calls_to("/api/models"), [])
        self.assertEqual(self.calls_to("/api/model/check"), [])
        self.page.get_by_role("button", name="Find models", exact=True).click()
        expect(self.page.locator("#modelCatalog")).to_contain_text("Available model")
        expect(self.page.locator("#modelCatalogNote")).to_contain_text("not verified inference")
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("manual/exact-model")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("")
        self.assertEqual(self.host.model["model"], "synthetic-model")
        self.page.get_by_label("Discovered models", exact=True).select_option("synthetic/available")
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("synthetic/available")
        self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("Synthetic tool-capability probe succeeded")
        self.assertEqual(self.host.model["model"], "synthetic-model")
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
        self.page.get_by_role("button", name="Find models", exact=True).click()
        expect(self.page.locator("#modelCatalogNote")).to_contain_text("Synthetic note")
        expect(self.page.locator("#modelCatalogNote")).not_to_contain_text("synthetic-key-only")
        self.host.model_check["message"] = "Synthetic probe echoed synthetic-key-only."
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
        self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("Synthetic probe")
        expect(self.page.locator("#modelCheckResult")).not_to_contain_text("synthetic-key-only")

    def test_check_result_is_invalidated_by_edits_and_errors_preserve_credentials(self):
        self.open()
        self.open_model()
        self.host.failures["POST", "/api/model/check"] = "Rejected synthetic-key-only"
        self.page.get_by_label("API key", exact=True).fill("synthetic-key-only")
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
        self.page.get_by_role("button", name="Check model", exact=True).click()
        expect(self.page.locator("#modelCheckResult")).to_contain_text("succeeded")
        expect(self.page.get_by_label("API key", exact=True)).to_have_value("")

    def test_copilot_can_sign_in_before_choosing_a_model_without_auto_inference(self):
        self.host.model.update(provider="github-copilot", model="", configured=False)
        self.copilot_settings()
        self.page.get_by_label("Model", exact=True).fill("")
        expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_be_enabled()
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        self.page.clock.run_for(5000)
        expect(self.page.locator("#authStatus")).to_contain_text("10 seconds")
        self.page.clock.run_for(10000)
        expect(self.page.locator("#authStatus")).to_contain_text("GitHub sign-in complete")
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("")
        expect(self.page.locator("#modelCredential")).to_contain_text("Model not selected")
        self.assertEqual(self.calls_to("/api/auth/copilot/start")[0][2], {"model": ""})
        self.assertEqual(self.calls_to("/api/models"), [])
        self.assertEqual(self.calls_to("/api/model/check"), [])
        self.page.get_by_role("button", name="Find models", exact=True).click()
        expect(self.page.locator("#modelCatalog")).to_be_visible()

    def test_empty_model_auth_failure_remains_explicit_and_retryable(self):
        self.copilot_settings()
        self.page.get_by_label("Model", exact=True).fill("")
        self.host.login_results = [{"status": "failed", "error": "Synthetic device authorization denied"}]
        expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_be_enabled()
        self.page.get_by_role("button", name="Sign in with GitHub").click()
        expect(self.page.locator("#authUserCode")).to_have_text("ABCD-EFGH")
        self.page.clock.run_for(5000)
        expect(self.page.locator("#modelError")).to_contain_text("Synthetic device authorization denied")
        expect(self.page.get_by_role("button", name="Sign in with GitHub")).to_be_enabled()
        expect(self.page.get_by_label("Model", exact=True)).to_have_value("")

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
        self.page.locator(".permission").nth(1).get_by_role("button", name="Deny", exact=True).click()
        expect(self.page.locator(".permission").nth(1)).to_contain_text("Denied")

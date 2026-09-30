"""Read-only plans exercise the real CLI under a no-write/no-execution audit."""

import json
from pathlib import Path

import test_install as fixtures


class PlanTests(fixtures.InstallerFixture):
    def plan(self, *extra):
        result = self.run_installer(*extra, launcher=fixtures.read_only_launcher())
        return json.loads(result.stdout)

    def test_explicit_source_plan_lists_exact_bindings_destinations_and_dependencies(self):
        before = fixtures.snapshot(self.root)
        for flag in ("--dry-run", "--plan", "-DryRun", "-Plan"):
            with self.subTest(flag=flag):
                plan = self.plan(flag)
                self.assertTrue(plan["dry_run"])
                self.assertEqual(plan["operation"], "install")
                self.assertEqual(plan["host"], "copilot")
                self.assertEqual(plan["scope"], "project")
                self.assertEqual(plan["project"], str(self.project))
                self.assertEqual(plan["bin_dir"], str(self.bin))
                self.assertEqual(plan["state_dir"], str(self.state))
                self.assertEqual(plan["skills_dir"], str(self.skills))
                expected = {str(self.bin / fixtures.BINARY), str(self.bin / fixtures.RECEIPT)}
                expected.update(
                    str(self.skills / Path(entry["destination"]))
                    for entry in json.loads(self.manifest.read_text())["files"]
                )
                self.assertEqual({entry["path"] for entry in plan["destinations"]}, expected)
                self.assertEqual({entry["action"] for entry in plan["destinations"]}, {"create"})
                dependencies = json.dumps(plan["dependencies"]).lower()
                self.assertIn("python", dependencies)
                self.assertIn("3.9", dependencies)
                self.assertIn("hard-link", dependencies)
                self.assertIn("not probed", dependencies)
                self.assertIn("not executed", json.dumps(plan["notes"]).lower())
        self.assertEqual(fixtures.snapshot(self.root), before)
        self.assertFalse(self.bin.exists())
        self.assertFalse(self.skills.exists())
        self.assertFalse(self.state.exists())
        self.assertFalse(self.log.exists())

    def test_personal_host_plan_respects_explicit_binary_and_state_paths(self):
        binary = self.root / "personal bin"
        state = self.root / "retained state"
        for host, folder in (("copilot", ".copilot"), ("claude", ".claude"), ("agents", ".agents")):
            with self.subTest(host=host):
                plan = self.plan(
                    "--plan", "--scope", "user", "--host", host,
                    "--bin-dir", binary, "--state-dir", state,
                )
                self.assertIsNone(plan["project"])
                self.assertEqual(plan["home"], str(self.home))
                self.assertEqual(plan["host"], host)
                self.assertEqual(plan["skills_dir"], str(self.home / folder / "skills"))
                self.assertEqual(plan["bin_dir"], str(binary))
                self.assertEqual(plan["state_dir"], str(state))
        self.assertFalse(binary.exists())
        self.assertFalse(state.exists())
        self.assertEqual(list(self.home.iterdir()), [])

    def test_upgrade_plan_preserves_state_binding_and_predicts_removals_and_additions(self):
        state = self.root / "custom state"
        self.run_installer("--state-dir", state)
        (self.bundle / "article" / "obsolete.md").unlink()
        (self.bundle / "article" / "new.md").write_text("Synthetic replacement.\n")
        fixtures.fixture_manifest(self.bundle, self.manifest)
        before = fixtures.snapshot(self.root)
        plan = self.plan("--upgrade", "--plan")
        self.assertEqual(plan["operation"], "upgrade")
        self.assertEqual(plan["state_dir"], str(state))
        actions = {entry["path"]: entry["action"] for entry in plan["destinations"]}
        self.assertEqual(actions[str(self.skills / "midden-article" / "obsolete.md")], "remove")
        self.assertEqual(actions[str(self.skills / "midden-article" / "new.md")], "create")
        self.assertEqual(actions[str(self.bin / fixtures.BINARY)], "replace")
        self.assertEqual(actions[str(self.bin / fixtures.RECEIPT)], "replace")
        self.assertEqual(fixtures.snapshot(self.root), before)

    def test_verify_and_uninstall_plans_do_not_execute_or_remove_files(self):
        self.run_installer()
        note = self.skills / "midden-article" / "note.md"
        note.write_text("Synthetic unowned note.\n")
        before = fixtures.snapshot(self.root)
        for operation, action in (("verify", "verify"), ("uninstall", "remove")):
            with self.subTest(operation=operation):
                plan = self.plan("--" + operation, "--dry-run")
                self.assertEqual(plan["operation"], operation)
                self.assertEqual({entry["action"] for entry in plan["destinations"]}, {action})
                self.assertNotIn(str(note), {entry["path"] for entry in plan["destinations"]})
                self.assertIn(
                    str(self.bin / fixtures.RECEIPT), {entry["path"] for entry in plan["destinations"]}
                )
        self.assertEqual(fixtures.snapshot(self.root), before)

    def test_plan_refuses_collisions_and_modified_owned_files_without_writes(self):
        collision = self.bin / fixtures.BINARY
        collision.parent.mkdir(parents=True)
        collision.write_bytes(self.core.read_bytes())
        result = self.run_installer(
            "--plan", success=False, launcher=fixtures.read_only_launcher()
        )
        self.assertIn("unowned collision", result.stderr)
        collision.unlink()
        self.run_installer()
        changed = self.skills / "midden-article" / "SKILL.md"
        changed.write_text("Keep synthetic local edits.\n")
        before = fixtures.snapshot(self.root)
        for operation in ("--upgrade", "--uninstall", "--verify"):
            with self.subTest(operation=operation):
                result = self.run_installer(
                    "--plan", operation, success=False, launcher=fixtures.read_only_launcher()
                )
                self.assertIn("modified", result.stderr)
        self.assertEqual(fixtures.snapshot(self.root), before)

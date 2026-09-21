"""Real, offline browser capture of synthetic HTML; no model or session access."""

import base64
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shutil
import struct
import subprocess
import sys
import tempfile
import threading
import unittest
from urllib.request import urlopen

from test_rendering import PIXEL


ROOT = Path(__file__).resolve().parents[2]
INSPECTOR = ROOT / "bundles" / "midden-shared" / "inspect_html.py"
SLIDES = ROOT / "bundles" / "presentation" / "templates" / "html.yaml"


class HTMLInspectionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.python = os.environ.get("HTML_INSPECTION_PYTHON", sys.executable)
        cls.pandoc = shutil.which(os.environ.get("PANDOC", "pandoc"))
        if not cls.pandoc:
            raise RuntimeError("Set PANDOC to an existing renderer for real inspection tests")

    def setUp(self):
        parent = Path(tempfile.gettempdir()).resolve()
        if parent == ROOT or ROOT in parent.parents:
            raise RuntimeError("Inspection fixtures must be outside the repository")
        self.temporary = tempfile.TemporaryDirectory(prefix="midden-html-unit-", dir=parent)
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)

    def deck(self, title=True, overflow=False, automatic_title=False):
        source = self.root / "synthetic.md"
        metadata = "title: Synthetic deck\nsubtitle: A reader takeaway\n" if title else "pagetitle: Synthetic deck\n"
        content = "# Observation\n\nA synthetic observation.\n\n# Limit\n\nThe conclusion remains bounded.\n"
        if overflow:
            content = '# Overflow\n\n<div style="height: 900px">Synthetic tall content.</div>\n'
        source.write_text("---\n" + metadata + "lang: en\n---\n\n" + content, encoding="utf-8")
        output = self.root / "slides.html"
        options = ["--defaults", str(SLIDES)]
        if automatic_title:
            options = [
                "--to", "dzslides", "--standalone", "--embed-resources",
                "--slide-level", "1", "--fail-if-warnings",
                "--css", str(SLIDES.with_name("slides.css")),
            ]
        result = subprocess.run(
            [self.pandoc, *options, str(source), "--output", str(output)],
            cwd=self.root, capture_output=True, text=True, timeout=60,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return output

    def inspect(self, html, mode="slides", code=0, output=None, expected=None):
        output = output or self.root / "inspection"
        command = [self.python, "-B", str(INSPECTOR), str(html),
                   "--mode", mode, "--out", str(output)]
        if expected is not None:
            command += ["--expected-slides", str(expected)]
        browser = os.environ.get("HTML_INSPECTION_BROWSER")
        if browser:
            command += ["--browser", browser]
        result = subprocess.run(
            command, capture_output=True, text=True, encoding="utf-8",
            errors="replace", timeout=90,
        )
        self.assertEqual(result.returncode, code, result.stdout + result.stderr)
        return output, result

    def assert_png(self, path, full_page=False):
        data = path.read_bytes()
        self.assertTrue(data.startswith(b"\x89PNG\r\n\x1a\n"))
        width, height = struct.unpack(">II", data[16:24])
        self.assertEqual(width, 1280)
        if full_page:
            self.assertGreaterEqual(height, 960)
        else:
            self.assertEqual(height, 960)
        self.assertGreater(len(data), 1000)
        return hashlib.sha256(data).hexdigest()

    def test_captures_every_legacy_auto_title_and_content_slide_not_subtitle(self):
        html = self.deck(automatic_title=True)
        original = html.read_bytes()
        output, _ = self.inspect(html)
        report = json.loads((output / "report.json").read_text())
        self.assertEqual(report["status"], "captured")
        self.assertEqual(report["slide_count"], 3)
        self.assertEqual(report["title_slide_count"], 1)
        self.assertEqual(report["visual_review"], "not_performed_by_helper")
        self.assertEqual(report["source_sha256"], hashlib.sha256(original).hexdigest())
        self.assertEqual(report["blocked_requests"], [])
        self.assertEqual(report["findings"], [])
        self.assertEqual(
            [item["title"] for item in report["screenshots"]],
            ["Synthetic deck", "Observation", "Limit"],
        )
        images = {self.assert_png(output / item["file"]) for item in report["screenshots"]}
        self.assertEqual(len(images), 3, "Each slide must actually be selected before capture")
        self.assertEqual(html.read_bytes(), original)

    def test_default_template_matches_the_requested_count_without_a_hidden_cover(self):
        output, _ = self.inspect(self.deck(), expected=2)
        report = json.loads((output / "report.json").read_text())
        self.assertEqual(report["status"], "captured")
        self.assertEqual(report["expected_slides"], 2)
        self.assertEqual(report["slide_count"], 2)
        self.assertEqual(report["title_slide_count"], 0)
        self.assertEqual(report["title"], "Synthetic deck")
        self.assertEqual(
            [frame["title"] for frame in report["screenshots"]], ["Observation", "Limit"]
        )

    def test_expected_count_mismatch_fails_but_keeps_all_rendered_screenshots(self):
        output, result = self.inspect(self.deck(automatic_title=True), expected=2, code=2)
        self.assertTrue((output / "report.json").is_file(), result.stderr)
        report = json.loads((output / "report.json").read_text())
        self.assertEqual(report["status"], "issues")
        self.assertEqual(report["expected_slides"], 2)
        self.assertEqual(report["slide_count"], 3)
        self.assertEqual(report["title_slide_count"], 1)
        self.assertTrue(any("Expected 2" in finding and "found 3" in finding
                            for finding in report["findings"]))
        self.assertEqual(len(report["screenshots"]), 3)
        for frame in report["screenshots"]:
            self.assert_png(output / frame["file"])

    def test_expected_count_requires_positive_slide_mode_input(self):
        html = self.deck()
        for mode, expected in (("slides", 0), ("slides", -1), ("document", 2)):
            with self.subTest(mode=mode, expected=expected):
                output = self.root / f"invalid-{mode}-{expected}"
                _, result = self.inspect(
                    html, mode=mode, expected=expected, code=1, output=output
                )
                self.assertIn("--expected-slides", result.stderr)
                self.assertFalse(output.exists())

    def test_counts_content_without_inventing_an_automatic_title(self):
        output, _ = self.inspect(self.deck(title=False))
        report = json.loads((output / "report.json").read_text())
        self.assertEqual(report["slide_count"], 2)
        self.assertEqual(report["title_slide_count"], 0)
        self.assertEqual(len(report["screenshots"]), 2)

    def test_network_resources_and_websockets_never_reach_the_server(self):
        requests = []

        class Handler(BaseHTTPRequestHandler):
            def do_GET(self):
                requests.append(self.path)
                self.send_response(200)
                self.send_header("Content-Type", "image/png")
                self.end_headers()
                self.wfile.write(PIXEL)

            def log_message(self, *_args):
                pass

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        base = "http://127.0.0.1:" + str(server.server_port)
        try:
            with urlopen(base + "/control", timeout=5) as response:
                self.assertEqual(response.read(), PIXEL)
            requests.clear()
            html = self.root / "network.html"
            html.write_text(
                '<!doctype html><html><head><title>Synthetic offline check</title>'
                f'<link rel="stylesheet" href="{base}/style.css"></head><body>'
                '<h1>Synthetic content</h1>'
                f'<img src="{base}/pixel.png" alt="Unavailable remote image">'
                f'<script>new WebSocket("ws://127.0.0.1:{server.server_port}/socket");</script>'
                '</body></html>',
                encoding="utf-8",
            )
            output, _ = self.inspect(html, mode="document", code=2)
            report = json.loads((output / "report.json").read_text())
            self.assertEqual(report["status"], "issues")
            self.assertEqual(requests, [], "Offline inspection must not contact even loopback resources")
            self.assertIn(base + "/pixel.png", report["blocked_requests"])
            self.assertIn(base + "/style.css", report["blocked_requests"])
            self.assertIn(
                f"ws://127.0.0.1:{server.server_port}/socket", report["blocked_requests"]
            )
            self.assertTrue(report["findings"])
            self.assert_png(output / "document.png", full_page=True)
        finally:
            server.shutdown()
            thread.join(timeout=5)
            server.server_close()

    def test_structural_overflow_is_reported_without_claiming_visual_approval(self):
        output, _ = self.inspect(self.deck(overflow=True), code=2)
        report = json.loads((output / "report.json").read_text())
        self.assertEqual(report["status"], "issues")
        self.assertTrue(any(frame["overflow"] for frame in report["screenshots"]))
        self.assertEqual(report["visual_review"], "not_performed_by_helper")
        self.assertEqual(len(report["screenshots"]), report["slide_count"])
        for frame in report["screenshots"]:
            self.assert_png(output / frame["file"])

    def test_document_sections_are_not_misclassified_as_slides(self):
        html = self.root / "book.html"
        image = base64.b64encode(PIXEL).decode("ascii")
        html.write_text(
            '<!doctype html><html><head><title>Synthetic book</title></head><body>'
            '<section><h1>Observation</h1><p>Synthetic chapter text.</p></section>'
            '<section><h1>Limit</h1><p>A second bounded chapter.</p></section>'
            f'<img alt="Embedded fixture" src="data:image/png;base64,{image}">'
            '</body></html>',
            encoding="utf-8",
        )
        output, _ = self.inspect(html, mode="document")
        report = json.loads((output / "report.json").read_text())
        self.assertIsNone(report["slide_count"])
        self.assertEqual([frame["file"] for frame in report["screenshots"]], ["document.png"])
        self.assertEqual(report["findings"], [])
        self.assert_png(output / "document.png", full_page=True)

    def test_existing_output_is_preserved_instead_of_recreated(self):
        html = self.deck()
        output = self.root / "existing inspection"
        output.mkdir()
        sentinel = output / "keep.txt"
        sentinel.write_text("A prior observation to retain.\n")
        _, result = self.inspect(html, code=1, output=output)
        self.assertIn("empty", result.stderr.lower())
        self.assertEqual(sentinel.read_text(), "A prior observation to retain.\n")
        self.assertEqual({path.name for path in output.iterdir()}, {"keep.txt"})

    def test_missing_slide_runtime_is_an_explicit_incomplete_capture(self):
        html = self.root / "not-a-deck.html"
        html.write_text("<html><body><section><h1>Static section</h1></section></body></html>")
        output, _ = self.inspect(html, code=1)
        report = json.loads((output / "report.json").read_text())
        self.assertEqual(report["status"], "failed")
        self.assertIn("dzslides", report["error"].lower())
        self.assertEqual(report["screenshots"], [])


if __name__ == "__main__":
    unittest.main()

"""Render synthetic content with the real bundle defaults; no sessions or models.

These checks cover artifact structure and local resources, not agent behavior,
visual quality, content sanitization, or factual correctness.
"""

import base64
from html.parser import HTMLParser
import os
from pathlib import Path
import posixpath
import re
import shutil
import subprocess
import tempfile
import unittest
from urllib.parse import unquote, urlsplit
import xml.etree.ElementTree as ET
import zipfile


ROOT = Path(__file__).resolve().parents[2]
BUNDLES = ROOT / "bundles"
DEFAULTS = {
    "slides": BUNDLES / "presentation" / "templates" / "html.yaml",
    "html": BUNDLES / "long-form" / "templates" / "html.yaml",
    "epub": BUNDLES / "long-form" / "templates" / "epub.yaml",
}
PIXEL = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNQ"
    "cGj4DwADRAHgMqq/hAAAAABJRU5ErkJggg=="
)


def css_resources(css):
    urls = re.findall(r"""url\(\s*['"]?([^'"\s)]+)""", css, re.IGNORECASE)
    imports = re.findall(r"""@import\s+['"]([^'"]+)['"]""", css, re.IGNORECASE)
    return urls + imports


class ArtifactHTML(HTMLParser):
    def __init__(self, text):
        super().__init__(convert_charrefs=True)
        self.elements = []
        self.resources = []
        self.styles = []
        self.text = []
        self.in_style = False
        self.in_title = False
        self.title = ""
        self.feed(text)
        self.close()

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        self.elements.append((tag, attrs))
        self.in_style = tag == "style" or self.in_style
        self.in_title = tag == "title" or self.in_title
        resource_attrs = {
            "link": ("href",),
            "script": ("src",),
            "img": ("src", "srcset"),
            "image": ("href", "xlink:href"),
            "use": ("href", "xlink:href"),
            "video": ("src", "poster"),
            "audio": ("src",),
            "source": ("src", "srcset"),
            "track": ("src",),
            "iframe": ("src",),
            "embed": ("src",),
            "object": ("data",),
        }
        for name in resource_attrs.get(tag, ()):
            if attrs.get(name):
                self.resources.append(attrs[name])
        self.resources.extend(css_resources(attrs.get("style", "")))

    def handle_endtag(self, tag):
        if tag == "style":
            self.in_style = False
        if tag == "title":
            self.in_title = False

    def handle_data(self, data):
        self.text.append(data)
        if self.in_title:
            self.title += data
        if self.in_style:
            self.styles.append(data)
            self.resources.extend(css_resources(data))


class BundleRenderingTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        requested = os.environ.get("PANDOC", "pandoc")
        cls.pandoc = shutil.which(requested)
        if cls.pandoc is None:
            raise RuntimeError(
                f"Pandoc executable not found: {requested!r}. "
                "Set PANDOC to an existing scoped executable or provide it on PATH. "
                "Rendering was not tested; nothing was installed."
            )

    def setUp(self):
        scratch_parent = Path(tempfile.gettempdir()).resolve()
        if scratch_parent == ROOT or ROOT in scratch_parent.parents:
            raise RuntimeError(
                "Rendering scratch must be outside the repository. "
                "Set TMPDIR, TEMP, or TMP to an external temporary directory."
            )
        self.temporary = tempfile.TemporaryDirectory(
            prefix="midden-bundle-render-", dir=scratch_parent
        )
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.work = self.root / "content with spaces"
        self.work.mkdir()
        self.output = self.root / "delivery"
        self.output.mkdir()
        media = self.work / "media"
        media.mkdir()
        (media / "pixel.png").write_bytes(PIXEL)

    def write(self, name, text):
        path = self.work / name
        path.write_text(text, encoding="utf-8")
        return path

    def render(self, kind, inputs, output, success=True):
        env = dict(os.environ)
        # Default rendering must not need to fetch fonts or other remote assets.
        for name in ("http_proxy", "https_proxy", "HTTP_PROXY", "HTTPS_PROXY"):
            env[name] = "http://127.0.0.1:9"
        result = subprocess.run(
            [self.pandoc, "--defaults", str(DEFAULTS[kind])]
            + [str(path) for path in inputs]
            + ["--output", str(output)],
            cwd=self.work,
            env=env,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=90,
            check=False,
        )
        if success:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertEqual(result.stderr.strip(), "", result.stderr)
            self.assertGreater(output.stat().st_size, 0)
        return result

    def assert_embedded_html(self, path):
        document = ArtifactHTML(path.read_text(encoding="utf-8"))
        for resource in document.resources:
            self.assertTrue(
                resource.startswith(("data:", "#")),
                f"HTML depends on an unembedded resource: {resource}",
            )
        return document

    def test_slides_embed_runtime_css_and_media_from_an_unrelated_directory(self):
        source = self.write(
            "slides.md",
            "---\ntitle: 'A & B'\nlang: en\n---\n\n"
            "# Observed result\n\nSynthetic observation, not a deployment claim.\n\n"
            "![Local evidence](media/pixel.png)\n\n"
            "# Limits\n\n[Attribution](https://example.invalid/source) is a link.\n",
        )
        original = source.read_bytes()
        output = self.output / "slides.html"
        self.render("slides", [source], output)
        self.assertEqual(source.read_bytes(), original)
        self.work.rename(self.root / "inputs no longer adjacent")
        document = self.assert_embedded_html(output)
        sections = [attrs for tag, attrs in document.elements if tag == "section"]
        self.assertEqual(len(sections), 2, "Only the two explicit source slides should render")
        self.assertIn("A & B", "".join(document.text))
        self.assertIn("Synthetic observation", "".join(document.text))
        self.assertTrue(
            any(
                tag == "script" and not attrs.get("src")
                for tag, attrs in document.elements
            )
            and any(
                attrs.get("id") == "progress-bar" for _, attrs in document.elements
            ),
            "The slide runtime must be included, not just static sections",
        )
        self.assertTrue(
            any(
                tag == "img" and attrs.get("src", "").startswith("data:image/png")
                for tag, attrs in document.elements
            ),
            "The local image must travel inside the HTML",
        )
        self.assertTrue(document.styles, "The slide style must be embedded")
        self.assertTrue(
            any(
                tag == "a" and attrs.get("href") == "https://example.invalid/source"
                for tag, attrs in document.elements
            ),
            "Ordinary attribution links must remain usable",
        )

    def test_title_metadata_and_five_sections_render_exactly_five_slides(self):
        headings = ("Opening", "Observation", "Technique", "Limit", "Next step")
        source = self.write(
            "five-slides.md",
            "---\ntitle: 'Synthetic & bounded'\nsubtitle: A synthetic subtitle\nlang: en\n---\n\n"
            + "\n\n".join(f"# {heading}\n\nSynthetic supporting text." for heading in headings),
        )
        original = source.read_bytes()
        output = self.output / "five-slides.html"
        self.render("slides", [source], output)
        document = self.assert_embedded_html(output)
        sections = [attrs for tag, attrs in document.elements if tag == "section"]
        self.assertEqual(len(sections), 5)
        self.assertFalse(any("title" in attrs.get("class", "").split() for attrs in sections))
        self.assertEqual(document.title, "Synthetic & bounded")
        self.assertEqual(source.read_bytes(), original)

    def test_explicit_opening_cover_counts_once_and_keeps_an_explicit_page_title(self):
        source = self.write(
            "explicit-cover.md",
            "---\ntitle: Metadata title\npagetitle: Browser title choice\nlang: en\n---\n\n"
            "# Opening question {.title}\n\nA cover written in the source.\n\n"
            "# Observation\n\nSynthetic observation.\n\n# Limit\n\nA bounded conclusion.\n",
        )
        output = self.output / "explicit-cover.html"
        self.render("slides", [source], output)
        document = self.assert_embedded_html(output)
        sections = [attrs for tag, attrs in document.elements if tag == "section"]
        self.assertEqual(len(sections), 3)
        self.assertEqual(
            sum("title" in attrs.get("class", "").split() for attrs in sections), 1
        )
        self.assertEqual(sections[0].get("id"), "opening-question")
        self.assertEqual(document.title, "Browser title choice")

    def chapters(self):
        first = self.write(
            "z-first.md",
            "---\ntitle: Synthetic handbook\nlang: en\n---\n\n"
            "# Evidence first\n\nFIRST_CHAPTER_OBSERVATION\n\n"
            "![Local evidence](media/pixel.png)\n",
        )
        second = self.write(
            "a-second.md",
            "# Limits second\n\nSECOND_CHAPTER_LIMIT\n\n"
            "[Attribution](https://example.invalid/source)\n",
        )
        return [first, second]

    def test_long_form_html_preserves_explicit_order_toc_and_embedded_media(self):
        output = self.output / "book.html"
        self.render("html", self.chapters(), output)
        document = self.assert_embedded_html(output)
        text = "".join(document.text)
        self.assertLess(
            text.index("FIRST_CHAPTER_OBSERVATION"), text.index("SECOND_CHAPTER_LIMIT")
        )
        ids = {attrs["id"] for _, attrs in document.elements if "id" in attrs}
        self.assertIn("TOC", ids)
        for target in ("#evidence-first", "#limits-second"):
            self.assertIn(target[1:], ids)
            self.assertTrue(
                any(
                    tag == "a" and attrs.get("href") == target
                    for tag, attrs in document.elements
                ),
                f"Missing working table-of-contents link: {target}",
            )
        self.assertTrue(
            any(
                tag == "img" and attrs.get("src", "").startswith("data:image/png")
                for tag, attrs in document.elements
            )
        )

    def test_epub_packages_ordered_chapters_navigation_and_local_resources(self):
        output = self.output / "book.epub"
        self.render("epub", self.chapters(), output)
        with zipfile.ZipFile(output) as book:
            self.assertEqual(book.namelist()[0], "mimetype")
            self.assertEqual(book.read("mimetype"), b"application/epub+zip")
            self.assertEqual(book.getinfo("mimetype").compress_type, zipfile.ZIP_STORED)
            container = ET.fromstring(book.read("META-INF/container.xml"))
            rootfile = container.find(
                ".//{urn:oasis:names:tc:opendocument:xmlns:container}rootfile"
            )
            self.assertIsNotNone(rootfile)
            package_path = rootfile.attrib["full-path"]
            package = ET.fromstring(book.read(package_path))
            ns = {"opf": "http://www.idpf.org/2007/opf"}
            manifest = package.findall("opf:manifest/opf:item", ns)
            items = {item.attrib["id"]: item.attrib for item in manifest}
            self.assertTrue(
                any("nav" in item.get("properties", "").split() for item in items.values())
            )
            self.assertTrue(
                any(item["media-type"] == "image/png" for item in items.values())
            )
            ordered_text = []
            for itemref in package.findall("opf:spine/opf:itemref", ns):
                item = items[itemref.attrib["idref"]]
                member = posixpath.normpath(
                    posixpath.join(posixpath.dirname(package_path), item["href"])
                )
                ordered_text.append(book.read(member).decode("utf-8"))
            text = "\n".join(ordered_text)
            self.assertLess(
                text.index("FIRST_CHAPTER_OBSERVATION"),
                text.index("SECOND_CHAPTER_LIMIT"),
            )
            for member in book.namelist():
                resources = []
                if member.endswith(".xhtml"):
                    resources = ArtifactHTML(
                        book.read(member).decode("utf-8")
                    ).resources
                elif member.endswith(".css"):
                    resources = css_resources(book.read(member).decode("utf-8"))
                for resource in resources:
                    if resource.startswith(("data:", "#")):
                        continue
                    parsed = urlsplit(resource)
                    self.assertFalse(
                        parsed.scheme or parsed.netloc,
                        f"EPUB depends on a remote resource: {resource}",
                    )
                    resolved = posixpath.normpath(
                        posixpath.join(
                            posixpath.dirname(member), unquote(parsed.path)
                        )
                    )
                    self.assertIn(resolved, book.namelist())

    def test_missing_local_media_fails_instead_of_delivering_a_broken_artifact(self):
        source = self.write(
            "broken.md",
            "---\ntitle: Missing media\nlang: en\n---\n\n"
            "# Check\n\n![Missing](absent-asset.png)\n",
        )
        for kind in DEFAULTS:
            with self.subTest(kind=kind):
                suffix = "epub" if kind == "epub" else "html"
                output = self.output / f"broken-{kind}.{suffix}"
                result = self.render(kind, [source], output, success=False)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn("absent-asset.png", result.stderr)

    def test_rerender_updates_the_chosen_target_without_changing_the_source(self):
        source = self.write(
            "slides.md", "---\ntitle: Revision\n---\n\n# Before\n\nOLD_DRAFT_TEXT\n"
        )
        output = self.output / "slides.html"
        self.render("slides", [source], output)
        revised = "---\ntitle: Revision\n---\n\n# After\n\nREVISED_DRAFT_TEXT\n"
        source.write_text(revised, encoding="utf-8")
        self.render("slides", [source], output)
        self.assertEqual(source.read_text(encoding="utf-8"), revised)
        rendered = output.read_text(encoding="utf-8")
        self.assertIn("REVISED_DRAFT_TEXT", rendered)
        self.assertNotIn("OLD_DRAFT_TEXT", rendered)
        self.assertEqual([path.name for path in self.output.iterdir()], ["slides.html"])


if __name__ == "__main__":
    unittest.main()

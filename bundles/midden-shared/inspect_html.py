"""Capture existing self-contained HTML; this is not a visual or factual reviewer."""

import argparse
import hashlib
import json
from pathlib import Path
import sys


VIEWPORT = {"width": 1280, "height": 960}
SLIDE_METRICS = """section => {
    const box = section.getBoundingClientRect();
    const outside = [...section.querySelectorAll('*')].some(element => {
        if (!element.getClientRects().length) return false;
        const style = getComputedStyle(element);
        if (style.visibility === 'hidden' || style.display === 'none') return false;
        const child = element.getBoundingClientRect();
        return child.width > 0 && child.height > 0 &&
            (child.left < box.left - 1 || child.right > box.right + 1 ||
             child.top < box.top - 1 || child.bottom > box.bottom + 1);
    });
    return {
        title: section.querySelector('h1, h2, h3')?.textContent.trim() || '',
        title_slide: section.classList.contains('title'),
        overflow: outside || section.scrollWidth > section.clientWidth + 1 ||
            section.scrollHeight > section.clientHeight + 1
    };
}"""


def capture(playwright, html: str, output: Path, mode: str, browser_path, report: dict):
    options = {"headless": True}
    if browser_path is not None:
        options["executable_path"] = str(browser_path)
    browser = playwright.chromium.launch(**options)
    try:
        context = browser.new_context(
            viewport=VIEWPORT, offline=True, service_workers="block", accept_downloads=False
        )

        def block_request(route):
            report["blocked_requests"].append(route.request.url)
            route.abort("blockedbyclient")

        def block_websocket(route):
            report["blocked_requests"].append(route.url)
            # Intercepted sockets never reach a server without connect_to_server().

        context.route("**/*", block_request)
        if not hasattr(context, "route_web_socket"):
            raise ValueError("Offline WebSocket interception requires Python Playwright 1.48+")
        context.route_web_socket("**/*", block_websocket)
        page = context.new_page()
        page.set_default_timeout(15000)
        page.on("pageerror", lambda error: report["page_errors"].append(str(error)))
        # Loading bytes into a blank page grants no file-origin access to other local files.
        page.set_content(html, wait_until="load")
        report["title"] = page.title()
        if mode == "slides":
            ready = page.evaluate(
                "() => typeof window.Dz === 'object' && typeof window.Dz.setCursor === 'function'"
            )
            if not ready:
                raise ValueError("Slides mode requires the self-contained Pandoc dzslides runtime")
            page.wait_for_function(
                "() => window.Dz.slides && document.querySelector('body > section[aria-selected]')"
            )
            report["slide_count"] = page.locator("body > section").count()
            report["title_slide_count"] = page.locator("body > section.title").count()
            if not report["slide_count"]:
                raise ValueError("The dzslides document contains no rendered slides")
            for index in range(report["slide_count"]):
                page.evaluate("index => window.Dz.setCursor(index + 1)", index)
                page.wait_for_function(
                    "index => document.querySelector('body > section[aria-selected]') === "
                    "document.querySelectorAll('body > section')[index]",
                    arg=index,
                )
                filename = f"slide-{index + 1:03}.png"
                page.screenshot(path=str(output / filename), animations="disabled")
                frame = page.locator("body > section[aria-selected]").evaluate(SLIDE_METRICS)
                frame.update(file=filename, number=index + 1)
                report["screenshots"].append(frame)
        else:
            filename = "document.png"
            page.screenshot(path=str(output / filename), full_page=True, animations="disabled")
            report["screenshots"].append(
                {
                    "file": filename,
                    "title": page.title(),
                    "overflow": page.evaluate(
                        "() => document.documentElement.scrollWidth > innerWidth + 1"
                    ),
                }
            )
        report["missing_images"] = page.evaluate(
            "() => [...document.images].flatMap((image, index) => "
            "image.complete && image.naturalWidth > 0 ? [] : [image.alt || `image ${index + 1}`])"
        )
        context.close()
    finally:
        browser.close()


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("html", type=Path, help="Existing self-contained HTML; source is not modified")
    parser.add_argument("--out", required=True, type=Path, help="New directory for screenshots and report.json")
    parser.add_argument("--mode", choices=("slides", "document"), default="slides")
    parser.add_argument("--expected-slides", type=int, metavar="N", help="Required total rendered slide count, including covers")
    parser.add_argument("--browser", type=Path, help="Existing Chromium-family executable, if not using Playwright's browser")
    args = parser.parse_args(argv)
    try:
        if args.expected_slides is not None and (
            args.mode != "slides" or args.expected_slides < 1
        ):
            raise ValueError("--expected-slides requires a positive count and --mode slides")
        source = args.html.resolve(strict=True)
        data = source.read_bytes()
        html = data.decode("utf-8-sig")
        output = args.out.resolve()
        if output.exists():
            raise ValueError("Output already exists; choose a new empty output directory")
        browser_path = args.browser.resolve(strict=True) if args.browser else None
        if browser_path is not None and not browser_path.is_file():
            raise ValueError("--browser must name an existing browser executable")
    except (OSError, ValueError) as error:
        print(f"HTML inspection: {error}", file=sys.stderr)
        return 1
    try:
        from playwright.sync_api import Error as PlaywrightError, sync_playwright
    except ImportError as error:
        print(
            "HTML inspection requires Python Playwright 1.48+ in the selected interpreter. "
            f"Nothing was installed. Dependency error: {error}",
            file=sys.stderr,
        )
        return 1
    report = {
        "source": source.name,
        "source_sha256": hashlib.sha256(data).hexdigest(),
        "mode": args.mode,
        "status": "failed",
        "visual_review": "not_performed_by_helper",
        "viewport": VIEWPORT,
        "slide_count": None,
        "title_slide_count": None,
        "expected_slides": args.expected_slides,
        "screenshots": [],
        "blocked_requests": [],
        "page_errors": [],
        "missing_images": [],
        "findings": [],
    }
    try:
        output.mkdir(parents=True)
    except OSError as error:
        print(f"HTML inspection cannot create its output directory: {error}", file=sys.stderr)
        return 1
    try:
        with sync_playwright() as playwright:
            capture(playwright, html, output, args.mode, browser_path, report)
        report["blocked_requests"] = sorted(set(report["blocked_requests"]))
        if args.expected_slides is not None and report["slide_count"] != args.expected_slides:
            report["findings"].append(
                f"Expected {args.expected_slides} rendered slides; found "
                f"{report['slide_count']} (all covers included)"
            )
        if report["blocked_requests"]:
            report["findings"].append("The document attempted blocked network access")
        if report["page_errors"]:
            report["findings"].append("The document raised browser script errors")
        if report["missing_images"]:
            report["findings"].append("Some images did not load")
        for frame in report["screenshots"]:
            if frame["overflow"]:
                report["findings"].append(f"Possible content overflow: {frame['file']}")
        report["status"] = "issues" if report["findings"] else "captured"
        code = 2 if report["findings"] else 0
    except (OSError, ValueError, PlaywrightError) as error:
        report["error"] = str(error)
        code = 1
    try:
        (output / "report.json").write_text(
            json.dumps(report, indent=2, ensure_ascii=True) + "\n", encoding="utf-8"
        )
    except OSError as error:
        print(f"HTML inspection could not save its report: {error}", file=sys.stderr)
        return 1
    message = (
        f"{report['status']}: {len(report['screenshots'])} screenshot(s). "
        f"Report: {output / 'report.json'}. Visual and factual review were not performed."
    )
    print(message, file=sys.stderr if code else sys.stdout)
    return code


if __name__ == "__main__":
    sys.exit(main())

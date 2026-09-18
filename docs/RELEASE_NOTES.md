# Midden 0.2.3 — Fix Linux “Both rendering capabilities” installation

Fixes the Linux wizard offering D2 and then aborting because apt has no D2 package.

- Linux x64/arm64 now install pinned D2 v0.9.0 from the upstream release,
  verified against SHA-256 digests in the shared installer manifest.
- D2 and license notices are installed beside Midden without sudo, tracked by
  its ownership receipt, retained on repeat installs, and removed by uninstall
  only when unchanged. Existing external D2 installations are reused.
- The preview shows the exact D2 download size; setup verifies the archive before
  running apt or writing installation files, then performs a real SVG render.
- Pandoc remains apt-managed on Linux. Its system package is preserved on uninstall.
- Release CI now installs **both real renderers on a clean Ubuntu container**,
  produces PPTX/HTML/SVG, and checks repeat, verify, uninstall, corrupted-checksum
  refusal and preservation of modified D2 files.

Run the one-liner from [the website](https://xibodev.github.io/midden/#install)
after its version pin is promoted, or download this release's installer,
`manifest.tsv`, and `SHA256SUMS` and run `bash ./install.sh --version v0.2.3`.

The terminal wizard and script-owned installation remain unchanged in purpose.
Agentic CLI is recommended; standalone is experimental. Live agent acceptance
is separate from installer and renderer validation.

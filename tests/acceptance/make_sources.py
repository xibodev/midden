"""Create isolated synthetic source material for uncoached host evaluations."""
import argparse
import base64
import json
from pathlib import Path
import struct
import zlib


def image():
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    width, height = 160, 90
    rows = []
    for y in range(height):
        row = bytearray()
        for x in range(width):
            color = (32, 113, 90) if 12 < x < 62 and 15 < y < 75 else (204, 222, 211)
            if 88 < x < 148 and 40 < y < 75:
                color = (89, 72, 132)
            row.extend(color)
        rows.append(b"\0" + bytes(row))
    return b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) + chunk(b"IDAT", zlib.compress(b"".join(rows))) + chunk(b"IEND", b"")


SESSIONS = {
    "sample-importer-core": [
        ("user", "Improve a sample CSV import tool while preserving explicit zero values. This is a synthetic engineering exercise."),
        ("assistant", "Plan: use a generic omit-empty serializer. This has not been implemented or verified."),
        ("user", "The fixture check found that zero-valued quantities disappear. Do not describe the plan as a successful migration."),
        ("assistant", "Correction: zero is a value; absence is a separate state. Use explicit presence checks in the mapper rather than blanket omission."),
        ("user", "The revised parser passed 42 local fixture checks. A full streaming import has not yet been tested."),
        ("assistant", "Recorded outcome: the local fixture checks passed, but large-file streaming remains unverified. The earlier generic omit-empty plan was rejected."),
        ("user", "Attached is a synthetic before/after diagram for illustrating the exercise; it is not a performance benchmark."),
    ],
    "sample-importer-streaming": [
        ("user", "Continue the sample importer's validation. Keep the local parser result distinct from full streaming behavior."),
        ("assistant", "Plan: exercise chunk boundaries and compare record identity across the stream. This is proposed work, not a completed result."),
        ("user", "The chunk-boundary fixture found one record repeated at the boundary. The parser correction was not the cause."),
        ("assistant", "Use the record's stable key when joining chunks. Deduplication must not merge different records merely because their text is equal."),
        ("user", "The revised streaming fixture passed the selected boundary cases with identities preserved. An interrupted-import restart is still untested."),
        ("assistant", "Final scope: local parsing and selected streaming boundary checks passed. Interrupted restart remains a gap. Do not generalize this result into production-wide readiness."),
    ],
}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    root = args.out.resolve()
    checkout = Path(__file__).resolve().parents[2]
    if root == checkout or checkout in root.parents:
        raise SystemExit("Acceptance sources must be outside the checkout")
    root.mkdir(parents=True, exist_ok=False)
    store = root / "claude"
    project = store / "projects" / "synthetic-work"
    project.mkdir(parents=True)
    (store / "settings.json").write_text("{}\n", encoding="utf-8")
    workspace = root / "source-workspace"
    workspace.mkdir()
    for number, (session, events) in enumerate(SESSIONS.items()):
        with (project / (session + ".jsonl")).open("w", encoding="utf-8", newline="\n") as output:
            for index, (role, text) in enumerate(events):
                content = text
                if number == 0 and index == len(events) - 1:
                    content = [{"type": "text", "text": text}, {"type": "image", "source": {
                        "type": "base64", "media_type": "image/png", "data": base64.b64encode(image()).decode("ascii")}}]
                record = {"type": role, "sessionId": session, "cwd": str(workspace),
                          "timestamp": f"2026-01-{1 + number * 10 + index:02d}T12:00:00Z",
                          "message": {"role": role, "content": content}}
                output.write(json.dumps(record) + "\n")
                for _ in range(150):
                    output.write(json.dumps({"type": "system.message", "sessionId": session,
                        "cwd": str(workspace), "timestamp": record["timestamp"],
                        "message": {"role": "system", "content": "Synthetic protocol bookkeeping."}}) + "\n")
    print(json.dumps({"source_root": str(store), "sessions": list(SESSIONS), "synthetic": True}))


if __name__ == "__main__":
    main()

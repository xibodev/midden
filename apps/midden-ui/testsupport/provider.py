"""Local deterministic protocol fixture; never a substitute for live model acceptance."""
import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_POST(self):
        if self.path not in {"/chat/completions", "/v1/chat/completions"}:
            self.send_error(404)
            return
        size = int(self.headers.get("Content-Length", "0"))
        if not 0 < size <= 4 * 1024 * 1024:
            self.send_error(413)
            return
        data = json.loads(self.rfile.read(size))
        messages = data.get("messages", [])
        last_user = max((i for i, value in enumerate(messages) if value.get("role") == "user"), default=0)
        turn = messages[last_user:]
        results = [str(value.get("content", "")) for value in turn if value.get("role") == "tool"]
        message = {"role": "assistant"}
        finish = "stop"
        if any("denied" in text.lower() or "reject" in text.lower() for text in results):
            message["content"] = "The operator denied the operation. I have not saved the requested file."
        elif not results:
            message["tool_calls"] = [{
                "id": "synthetic-list", "type": "function",
                "function": {"name": "midden", "arguments": json.dumps({"args": ["ls", "--all", "--json"]})},
            }]
            finish = "tool_calls"
        elif len(results) == 1:
            revision = "revise" in str(turn[0].get("content", "")).lower()
            title = "Synthetic revision" if revision else "Synthetic maintainer note"
            body = f"<!doctype html><html lang='en'><meta charset='utf-8'><title>{title}</title><h1>{title}</h1><p>Zero is a value; absence is a separate state. Historical checks were reported, not independently rerun.</p></html>"
            tool = "edit_file" if revision else "write_file"
            arguments = {"path": "maintainer-note.html", "content": body}
            if revision:
                arguments = {"path": "maintainer-note.html", "old_text": body.replace("Synthetic revision", "Synthetic maintainer note"), "new_text": body}
            message["tool_calls"] = [{
                "id": "synthetic-write", "type": "function",
                "function": {"name": tool, "arguments": json.dumps(arguments)},
            }]
            finish = "tool_calls"
        else:
            message["content"] = "Saved maintainer-note.html in the workspace. This is a deterministic test fixture, not a model-authored judgment."
        if data.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.end_headers()
            chunk = {"choices": [{"index": 0, "delta": message, "finish_reason": finish}]}
            self.wfile.write(("data: " + json.dumps(chunk) + "\n\ndata: [DONE]\n\n").encode())
        else:
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({"choices": [{"message": message, "finish_reason": finish}]}).encode())


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--port", type=int, default=18921)
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    print(f"Synthetic test provider: http://127.0.0.1:{server.server_port}", flush=True)
    server.serve_forever()

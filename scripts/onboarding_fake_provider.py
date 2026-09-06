#!/usr/bin/env python3

import json
import os
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        if self.path.startswith("/slow"):
            time.sleep(2)
        length = int(self.headers.get("Content-Length", "0"))
        request = json.loads(self.rfile.read(length))
        prompt = request["messages"][0]["content"]
        signal_path = os.environ.get("VETO_SMOKE_REQUEST_SIGNAL", "")
        if signal_path.startswith(os.path.sep) and os.path.isdir(os.path.dirname(signal_path)):
            with open(signal_path, "a", encoding="ascii") as signal:
                signal.write("request\n")
        if "suggested_alternative_model" in prompt:
            content = json.dumps({
                "accept": True,
                "confidence": 0.99,
                "reason_codes": [],
                "estimated_tokens": 32,
                "estimated_cost_usd": 0.0,
                "suggested_alternative_model": "",
                "required_task_changes": [],
            })
        elif "You are a QA reviewer." in prompt:
            content = json.dumps({
                "passed": True,
                "score": 1.0,
                "criteria": [{"criterion": "output is smoke", "met": True, "note": "verified"}],
            })
        else:
            content = "SMOKE EXECUTION OK"
        response = json.dumps({
            "choices": [{
                "message": {"content": content},
                "finish_reason": "stop",
            }],
            "usage": {
                "prompt_tokens": 12,
                "completion_tokens": 4,
                "total_tokens": 16,
            },
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        self.wfile.write(response)

    def log_message(self, _format, *_args):
        return


server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
with open(sys.argv[1], "w", encoding="ascii") as info:
    info.write(str(server.server_address[1]))
server.serve_forever()

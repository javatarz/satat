#!/usr/bin/env python3
"""Relay LiteLLM budget-alert webhooks to an ntfy topic.

LiteLLM posts a fixed JSON budget-event schema to ``WEBHOOK_URL``
(see docs/adr/0015-monitoring.md); ntfy expects its own publish shape. This tiny
sidecar receives LiteLLM's JSON and re-publishes a plain-text ntfy notification
using the header form (docs.ntfy.sh/publish).
"""

import json
import os
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

NTFY_URL = os.environ["NTFY_PUBLISH_URL"]
CANVAS_URL = f"https://{os.environ['SATAT_DOMAIN']}/canvas/"


def notify(payload: dict) -> None:
    message = payload.get("event_message") or payload.get("event") or "budget alert"
    scope = payload.get("event_group")
    spend = payload.get("spend")
    max_budget = payload.get("max_budget")

    detail = f"{message} ({scope})" if scope else message
    if spend is not None and max_budget:
        detail += f" — spend ${spend} of ${max_budget}"

    req = urllib.request.Request(NTFY_URL, data=detail.encode("utf-8"), method="POST")
    req.add_header("Title", "Satat budget warning")
    req.add_header("Priority", "high")
    req.add_header("Tags", "moneybag,warning")
    req.add_header("Click", CANVAS_URL)
    req.add_header("Actions", f"view, View Canvas, {CANVAS_URL}, clear=true")
    urllib.request.urlopen(req, timeout=10).read()


class Handler(BaseHTTPRequestHandler):
    def do_POST(self):  # noqa: N802 (http.server API)
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length) if length else b"{}"
        try:
            payload = json.loads(raw or b"{}")
            if isinstance(payload, dict):
                notify(payload)
        except Exception as exc:  # noqa: BLE001 - never fail LiteLLM's alert path
            # Log the exception type only: some exception messages can embed the
            # request URL, which contains the secret NTFY_TOPIC.
            print(f"relay error: {type(exc).__name__}", flush=True)
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def log_message(self, *args):  # silence per-request logging
        pass


if __name__ == "__main__":
    HTTPServer(("0.0.0.0", 8080), Handler).serve_forever()

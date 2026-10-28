# PROTOTYPE for #155: print what LiteLLM sends upstream; answer with a minimal Messages response.
import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers.get("Content-Length", 0)); body = json.loads(self.rfile.read(n) or b"{}")
        print(self.command, self.path, flush=True)
        for k, v in self.headers.items():
            print(f"  {k}: {v}", flush=True)
        print("  body keys:", sorted(body), "system starts:", str(body.get("system"))[:80], flush=True)
        if body.get("model") == "claude-rl-test":
            e = json.dumps({"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit."}}).encode()
            self.send_response(429); self.send_header("content-type","application/json"); self.send_header("retry-after","1234")
            self.send_header("anthropic-ratelimit-unified-status","rejected"); self.send_header("anthropic-ratelimit-unified-reset","1793000000")
            self.send_header("anthropic-ratelimit-unified-representative-claim","five_hour")
            self.send_header("content-length",str(len(e))); self.end_headers(); self.wfile.write(e); return
        out = json.dumps({"id":"msg_x","type":"message","role":"assistant","model":body.get("model"),
            "content":[{"type":"text","text":"wire-ok"}],"stop_reason":"end_turn","stop_sequence":None,
            "usage":{"input_tokens":1,"output_tokens":1}}).encode()
        self.send_response(200); self.send_header("content-type","application/json")
        self.send_header("anthropic-ratelimit-unified-status","allowed"); self.send_header("anthropic-ratelimit-unified-5h-utilization","0.42")
        self.send_header("content-length",str(len(out))); self.end_headers(); self.wfile.write(out)
    def log_message(self, *a): pass
http.server.HTTPServer(("", 4000), H).serve_forever()

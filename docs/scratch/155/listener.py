# PROTOTYPE for #155: log request lines and headers (auth values masked), answer 500.
import http.server, sys
class H(http.server.BaseHTTPRequestHandler):
    def _h(self):
        print(self.command, self.path, flush=True)
        for k, v in self.headers.items():
            if k.lower() in ("authorization", "x-api-key"):
                v = v[:13] + "...(masked)"
            print(f"  {k}: {v}", flush=True)
        self.send_response(500); self.end_headers()
    do_GET = do_POST = do_HEAD = _h
    def log_message(self, *a): pass
http.server.HTTPServer(("", 4000), H).serve_forever()

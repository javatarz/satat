#!/usr/bin/env bash
# PROTOTYPE for #155: call LiteLLM routes with a virtual key; report status, error gist and whether the token appears.
# Usage: TOKEN=... admin-probe.sh <virtual key>
key=$1
shape='sk-ant-oat[0-9][0-9]-[A-Za-z0-9_-]{20,}'
call() {
  local m=$1 route=$2 body=$3 out code
  out=$(curl -s -m 30 -X "$m" "localhost:4000$route" -H "Authorization: Bearer $key" -H 'content-type: application/json' ${body:+-d "$body"} -w '\n%{http_code}')
  code=$(tail -1 <<<"$out")
  printf '%-5s %-22s %s token=%s shape=%s %s\n' "$m" "$route" "$code" \
    "$(grep -c -F "$TOKEN" <<<"$out")" "$(grep -c -E "$shape" <<<"$out")" \
    "$(sed '$d' <<<"$out" | python3 -c 'import sys,json
try:
  d=json.load(sys.stdin); e=d.get("error") if isinstance(d,dict) else None
  print((e.get("message") if isinstance(e,dict) else str(e)) if e else "ok")
except Exception: print("non-json")' | tr '\n' ' ' | cut -c1-100)"
}
call GET /v1/models
call GET /model/info
call GET /v2/model/info
call GET /key/info
call GET /key/list
call POST /key/generate '{}'
call POST /key/update "{\"key\":\"$key\",\"max_budget\":1000}"
call POST /key/delete '{"keys":["x"]}'
call POST /user/new '{}'
call POST /team/new '{}'
call POST /model/new '{"model_name":"x","litellm_params":{"model":"anthropic/x"}}'
call POST /config/update '{}'
call GET /get/config/callbacks
call GET /global/spend/logs
call GET /spend/logs
call GET /health
call GET /routes
call POST /v1/messages '{"model":"claude-opus-5-5","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}'
call POST /v1/messages '{"model":"no-such-model","max_tokens":5,"messages":[{"role":"user","content":"hi"}]}'

#!/usr/bin/env bash
# PROTOTYPE for #155: how far a per-key max_budget overshoots under N concurrent calls (pass-through route).
set -u
N=${1:-20}; B=${2:-0.002}
K=$(./mint.sh "{\"duration\":\"15m\",\"max_budget\":$B,\"key_alias\":\"overshoot-$N-$B-$RANDOM\"}")
call() { curl -s -o /dev/null -w '%{http_code}\n' localhost:4000/anthropic/v1/messages -H "Authorization: Bearer $K" \
  -H 'content-type: application/json' -H 'anthropic-version: 2023-06-01' -H 'anthropic-beta: oauth-2025-04-20' \
  -d '{"model":"claude-haiku-4-5-20251001","max_tokens":300,"messages":[{"role":"user","content":"Count from 1 to 150, comma separated."}]}'; }
echo "wave 1: $N concurrent"; for i in $(seq 1 "$N"); do call & done | sort | uniq -c; wait
sleep 20
spend() { curl -s "localhost:4000/key/info?key=$K" -H 'Authorization: Bearer sk-placeholder-master' | python3 -c 'import sys,json;i=json.load(sys.stdin)["info"];print("spend",round(i["spend"],6),"max_budget",i["max_budget"])'; }
spend
echo "wave 2: $N concurrent after spend landed"; for i in $(seq 1 "$N"); do call & done | sort | uniq -c; wait
sleep 15; spend

#!/bin/sh
# PROTOTYPE for #155: run inside the lane. Looks for any Claude OAuth token and probes reach.
echo "== Q2 token search (prefix sk-ant-oat) =="
echo "env: $(env | grep -c sk-ant-oat)"
echo "proc environ+cmdline: $(cat /proc/[0-9]*/environ /proc/[0-9]*/cmdline 2>/dev/null | tr '\0' '\n' | grep -c sk-ant-oat)"
echo "files: $(grep -rlas sk-ant-oat / --exclude-dir=proc --exclude-dir=sys 2>/dev/null | wc -l)"
echo "ps: $(ps auxww | grep -c '[s]k-ant-oat')"
echo "== Q4 reach =="
for t in https://api.anthropic.com/ https://1.1.1.1/ http://db:5432/ http://otel:4318/ http://litellm:4000/health/liveliness; do
  printf '%s -> ' "$t"; curl -sS -m 5 -o /dev/null -w '%{http_code}\n' "$t" 2>&1 | tail -1
done

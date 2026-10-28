#!/bin/sh
# PROTOTYPE for #155: mint a virtual key with the master key. Usage: mint.sh '<json>'
curl -s localhost:4000/key/generate -H 'Authorization: Bearer sk-placeholder-master' -H 'content-type: application/json' -d "$1" | python3 -c 'import sys,json;print(json.load(sys.stdin)["key"])'

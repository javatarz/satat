#!/bin/bash
set -euo pipefail
cd /app
cat >> hello.py <<'PY'


def greet(name: str) -> str:
    if not name:
        raise ValueError("name must not be empty")
    return f"Hello, {name}!"
PY

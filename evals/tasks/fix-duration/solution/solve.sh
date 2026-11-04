#!/bin/bash
set -euo pipefail
cd /app
cat > durations.py <<'PY'
UNITS = {"h": 3600, "m": 60, "s": 1}


def parse_duration(text: str) -> int:
    """Parse a duration such as "1h30m" or "45s" into seconds."""
    if not text:
        raise ValueError("empty duration")
    total = 0
    number = ""
    for ch in text:
        if ch.isdigit():
            number += ch
        elif ch in UNITS:
            if not number:
                raise ValueError(f"{ch!r} has no number in {text!r}")
            total += int(number) * UNITS[ch]
            number = ""
        else:
            raise ValueError(f"unexpected {ch!r} in {text!r}")
    if number:
        raise ValueError(f"{number!r} has no unit in {text!r}")
    return total
PY

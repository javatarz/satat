UNITS = {"h": 3600, "m": 60, "s": 1}


def parse_duration(text: str) -> int:
    """Parse a duration such as "1h30m" or "45s" into seconds."""
    total = 0
    number = ""
    for ch in text:
        if ch.isdigit():
            number += ch
        elif ch in UNITS:
            total += int(number) * UNITS[ch]
        else:
            raise ValueError(f"unexpected {ch!r} in {text!r}")
    return total

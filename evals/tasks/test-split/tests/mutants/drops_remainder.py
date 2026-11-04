def split_amount(total_cents: int, parts: int) -> list[int]:
    if parts <= 0:
        raise ValueError("parts must be positive")
    if total_cents < 0:
        raise ValueError("total_cents must not be negative")
    return [total_cents // parts] * parts

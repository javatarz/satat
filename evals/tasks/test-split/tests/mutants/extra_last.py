def split_amount(total_cents: int, parts: int) -> list[int]:
    if parts <= 0:
        raise ValueError("parts must be positive")
    if total_cents < 0:
        raise ValueError("total_cents must not be negative")
    base, extra = divmod(total_cents, parts)
    return [base + 1 if i >= parts - extra else base for i in range(parts)]

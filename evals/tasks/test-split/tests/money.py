def split_amount(total_cents: int, parts: int) -> list[int]:
    """Split total_cents into `parts` shares that differ by at most one cent.

    The first shares get the extra cents, so split_amount(100, 3) is [34, 33, 33].
    """
    if parts <= 0:
        raise ValueError("parts must be positive")
    if total_cents < 0:
        raise ValueError("total_cents must not be negative")
    base, extra = divmod(total_cents, parts)
    return [base + 1 if i < extra else base for i in range(parts)]

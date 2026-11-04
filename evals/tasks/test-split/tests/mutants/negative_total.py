def split_amount(total_cents: int, parts: int) -> list[int]:
    if parts <= 0:
        raise ValueError("parts must be positive")
    base, extra = divmod(total_cents, parts)
    return [base + 1 if i < extra else base for i in range(parts)]

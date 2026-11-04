# parse_duration gets combined durations wrong

`parse_duration` in `durations.py` returns the wrong number of seconds when a duration has more
than one part. `parse_duration("1h30m")` returns `11400`, but it should return `5400`, and
`parse_duration("1h1m1s")` should return `3661`.

While fixing it, make these malformed inputs raise `ValueError` instead of returning a number:
- an empty string, `""`
- a number with no unit, such as `"90"` or the trailing `5` in `"1h5"`
- a unit with no number, such as `"h"`

Add tests for the fix to `test_durations.py`.

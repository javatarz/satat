# Add tests for split_amount

`split_amount` in `money.py` has no tests. Add them in a new file, `test_money.py`, using
`unittest`. Cover:

- an even split, such as `split_amount(90, 3)`
- a split with a remainder, where the first shares get the extra cents
- that the shares always add up to the total
- the errors: `parts` of zero or less, and a negative `total_cents`, raise `ValueError`

`split_amount` is correct as it is: don't change `money.py`. Run the tests with
`python -m unittest -v`.

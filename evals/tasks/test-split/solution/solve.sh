#!/bin/bash
set -euo pipefail
cd /app
cat > test_money.py <<'PY'
import unittest

from money import split_amount


class SplitAmountTest(unittest.TestCase):
    def test_even_split(self):
        self.assertEqual(split_amount(90, 3), [30, 30, 30])

    def test_first_shares_get_the_remainder(self):
        self.assertEqual(split_amount(100, 3), [34, 33, 33])
        self.assertEqual(split_amount(5, 4), [2, 1, 1, 1])

    def test_shares_add_up(self):
        for total, parts in [(0, 1), (1, 7), (999, 10), (101, 2)]:
            self.assertEqual(sum(split_amount(total, parts)), total)

    def test_invalid_parts(self):
        for parts in [0, -1]:
            with self.assertRaises(ValueError):
                split_amount(10, parts)

    def test_negative_total(self):
        with self.assertRaises(ValueError):
            split_amount(-1, 2)


if __name__ == "__main__":
    unittest.main()
PY

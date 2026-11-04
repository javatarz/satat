import unittest

from durations import parse_duration


class ParseDurationTest(unittest.TestCase):
    def test_seconds(self):
        self.assertEqual(parse_duration("45s"), 45)

    def test_hours(self):
        self.assertEqual(parse_duration("2h"), 7200)

    def test_unknown_unit(self):
        with self.assertRaises(ValueError):
            parse_duration("5x")


if __name__ == "__main__":
    unittest.main()

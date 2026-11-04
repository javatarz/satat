import unittest

from durations import parse_duration


class HiddenParseDurationTest(unittest.TestCase):
    def test_combined_parts(self):
        self.assertEqual(parse_duration("1h30m"), 5400)
        self.assertEqual(parse_duration("1h1m1s"), 3661)
        self.assertEqual(parse_duration("10m5s"), 605)

    def test_single_parts_still_work(self):
        self.assertEqual(parse_duration("45s"), 45)
        self.assertEqual(parse_duration("2h"), 7200)

    def test_malformed_inputs(self):
        for bad in ["", "90", "1h5", "h", "5x"]:
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                parse_duration(bad)

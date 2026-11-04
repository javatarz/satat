import unittest

from durations import parse_duration
from hello import hello


class HelloTest(unittest.TestCase):
    def test_hello(self):
        self.assertEqual(hello(), "Hello, world!")


class ParseDurationTest(unittest.TestCase):
    def test_seconds(self):
        self.assertEqual(parse_duration("45s"), 45)

    def test_hours(self):
        self.assertEqual(parse_duration("2h"), 7200)

    def test_unknown_unit(self):
        with self.assertRaises(ValueError):
            parse_duration("5x")

import unittest

from text import slugify


class HiddenSlugifyTest(unittest.TestCase):
    def test_examples_from_the_issue(self):
        self.assertEqual(slugify("Hello, World!"), "hello-world")
        self.assertEqual(slugify("  Satat  v0.2 "), "satat-v0-2")
        self.assertEqual(slugify(""), "")

    def test_runs_collapse_to_one_dash(self):
        self.assertEqual(slugify("a__b -- c"), "a-b-c")

    def test_trims_dashes(self):
        self.assertEqual(slugify("--Already-a-slug--"), "already-a-slug")

    def test_nothing_left(self):
        self.assertEqual(slugify("!!!"), "")

    def test_digits_kept(self):
        self.assertEqual(slugify("Release 2026"), "release-2026")

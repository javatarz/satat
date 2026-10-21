import unittest

from hello import greet, hello


class HiddenGreetTest(unittest.TestCase):
    def test_greets_by_name(self):
        self.assertEqual(greet("Karun"), "Hello, Karun!")
        self.assertEqual(greet("Ada Lovelace"), "Hello, Ada Lovelace!")

    def test_empty_name_is_an_error(self):
        with self.assertRaises(ValueError):
            greet("")

    def test_hello_still_works(self):
        self.assertEqual(hello(), "Hello, world!")


if __name__ == "__main__":
    unittest.main()

# Add slugify(text) helper

Add a new module `text.py` with a function `slugify(text)` that turns a title into a URL slug.

- Lowercase the text.
- Replace each run of characters that are not letters or digits with a single `-`.
- Trim `-` from both ends.
- For example, `slugify("Hello, World!")` returns `"hello-world"` and `slugify("  Satat  v0.2 ")` returns `"satat-v0-2"`.
- `slugify("")` returns `""`.

Add tests in `test_text.py` and run them with `python -m unittest -v`. Use only the standard library.

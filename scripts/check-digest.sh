#!/usr/bin/env bash
# Structural check of a meeting digest run's output, before anything is published.
#
# Checks the item list against .claude/skills/meeting-digest/items.schema.json, that every
# decision has an agreement quote, and that no quote leaks into a target. A leak is 8
# consecutive words of a quote, or the whole of a quote shorter than 8 words, after
# normalising (lowercase, apostrophes dropped, other non-alphanumerics as spaces). With a
# transcript it also checks that every quote occurs verbatim in one utterance, that each item's
# ts is the marker before an occurrence, and that no 8 consecutive words of an utterance, nor
# a whole 3-7 word utterance, appear in a target. A .json target is scanned string by string.
# No output line repeats item or transcript content: a schema error is reported as its JSON
# path and the schema keyword it breaks, and an item as its index, plus its id and ts when
# they are well formed.
#
# Prints `fail: <file>: <reason>` lines and exits 1, or prints `ok` and exits 0. Exits 2 on
# bad usage.
#
# usage:
#   scripts/check-digest.sh <YYYY-MM-DD>
#     items docs/scratch/calls/<date>.items.json, transcript docs/scratch/calls/<date>.txt;
#     targets, each when present: docs/scratch/calls/<date>.summary.md, docs/calls/<date>.md,
#     the lines added to docs/roadmap since the merge-base with origin/HEAD (plus untracked
#     docs/roadmap files) and every string in docs/scratch/calls/<date>.outbox.json
#   scripts/check-digest.sh --items <file> [--transcript <file>] [<target>...]
set -euo pipefail

usage() {
  echo "usage: $0 <YYYY-MM-DD> | --items <file> [--transcript <file>] [<target>...]" >&2
  exit 2
}

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly repo_root
readonly schema="$repo_root/.claude/skills/meeting-digest/items.schema.json"
readonly roadmap_label="docs/roadmap (added lines)"

for tool in jq python3; do
  if ! command -v "$tool" >/dev/null; then
    echo "error: $tool is required" >&2
    exit 1
  fi
done

items=""
transcript=""
roadmap_added=""
target_label_path_pairs=()
failed=0

fail() {
  echo "fail: $1: $2"
  failed=1
}

add_target() {
  target_label_path_pairs+=("$1" "$1")
}

add_target_if_present() {
  if [[ -f $1 ]]; then
    add_target "$1"
  fi
}

add_roadmap_targets() {
  local base untracked
  if ! base="$(git merge-base HEAD origin/HEAD 2>/dev/null)"; then
    fail docs/roadmap "no merge-base with origin/HEAD"
    return
  fi
  if ! roadmap_added="$(git diff --text --no-color -U0 "$base" -- docs/roadmap |
    LC_ALL=C awk '/^diff --git / { in_hunk = 0; next } /^@@/ { in_hunk = 1; next } in_hunk && /^\+/ { print substr($0, 2) }')"; then
    fail docs/roadmap "git diff failed"
    return
  fi
  target_label_path_pairs+=("$roadmap_label" -)
  while IFS= read -r untracked; do
    add_target "$untracked"
  done < <(git ls-files --others --exclude-standard -- docs/roadmap)
}

parse_default_form() {
  local date="$1"
  [[ $date =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || usage
  cd "$repo_root"
  items="docs/scratch/calls/$date.items.json"
  transcript="docs/scratch/calls/$date.txt"
  add_target_if_present "docs/scratch/calls/$date.summary.md"
  add_target_if_present "docs/calls/$date.md"
  add_roadmap_targets
  add_target_if_present "docs/scratch/calls/$date.outbox.json"
}

parse_explicit_form() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --items)
        [[ $# -ge 2 ]] || usage
        items="$2"
        shift 2
        ;;
      --transcript)
        [[ $# -ge 2 ]] || usage
        transcript="$2"
        shift 2
        ;;
      -*) usage ;;
      *)
        add_target "$1"
        shift
        ;;
    esac
  done
  [[ -n $items ]] || usage
}

check_schema() {
  local validator=(check-jsonschema)
  if ! command -v check-jsonschema >/dev/null; then
    validator=(uvx --quiet --from check-jsonschema==0.38.2 check-jsonschema)
  fi
  local report reasons reason reported=0
  if report="$("${validator[@]}" --output-format json --schemafile "$schema" "$items" 2>/dev/null)"; then
    return
  fi
  reasons="$(jq -r "$schema_report_reasons" <<<"$report" 2>/dev/null)" || reasons=""
  while IFS= read -r reason; do
    if [[ -n $reason ]]; then
      fail "$items" "schema: $reason"
      reported=1
    fi
  done <<<"$reasons"
  if [[ $reported -eq 0 ]]; then
    fail "$items" "schema check failed"
  fi
}

schema_report_reasons="$(cat <<'JQ'
def keyword:
  if startswith("Additional properties are not allowed") then "additionalProperties"
  elif test("is not of type '[^']*'(, '[^']*')*$") then "type"
  elif test("is a required property$") then "required"
  elif test("is not one of \\[.*\\]$") then "enum"
  elif test("does not match '.*'$") then "pattern"
  elif test("(should be non-empty|is too short)$") then "minLength"
  elif test("should not be valid under \\{.*\\}$") then "not"
  else "invalid"
  end;
def safe_path:
  if test("^\\$(\\.[a-z]+|\\[[0-9]+\\])*$") then . else "$" end;
(.parse_errors[]? | "not a JSON item list"),
(.errors[]? | "\(.path | safe_path): breaks \(.message | keyword)")
JQ
)"
readonly schema_report_reasons

checker="$(cat <<'PY'
import json
import re
import sys

LEAK_RUN = 8
SHORTEST_CHECKED_UTTERANCE = 3
MARKER = re.compile(r"^\d{2}:\d{2}:\d{2}$")
ITEM_ID = re.compile(r"^i\d{2,}$")
UTTERANCE = re.compile(r"^([^:]+): (.*)$")

failures = []


def fail(name, reason):
    failures.append(f"fail: {name}: {reason}")


def tokens(text):
    text = text.lower().replace("'", "").replace("\u2019", "")
    return "".join(c if c.isalnum() else " " for c in text).split()


def contains_run(haystack, needle):
    width = len(needle)
    return any(haystack[i:i + width] == needle for i in range(len(haystack) - width + 1))


def runs_of(stream):
    return {tuple(stream[i:i + LEAK_RUN]) for i in range(len(stream) - LEAK_RUN + 1)}


def leaks_into(source, stream, stream_runs):
    if len(source) < LEAK_RUN:
        return bool(source) and contains_run(stream, source)
    return not runs_of(source).isdisjoint(stream_runs)


def leak_kind(source, whole):
    return f"{LEAK_RUN} consecutive words" if len(source) >= LEAK_RUN else f"whole {whole}"


def json_strings(node):
    if isinstance(node, str):
        yield node
    elif isinstance(node, list):
        for child in node:
            yield from json_strings(child)
    elif isinstance(node, dict):
        for child in node.values():
            yield from json_strings(child)


def read_text(path):
    if path == "-":
        return sys.stdin.read()
    with open(path, encoding="utf-8") as handle:
        return handle.read()


def target_token_streams(label, path):
    try:
        text = read_text(path)
    except OSError as error:
        fail(label, f"unreadable ({error.strerror})")
        return []
    except UnicodeDecodeError:
        fail(label, "not UTF-8 text")
        return []
    if not label.endswith(".json"):
        return [tokens(text)]
    try:
        return [tokens(string) for string in json_strings(json.loads(text))]
    except json.JSONDecodeError:
        fail(label, "not valid JSON")
        return []


def read_utterances(path):
    utterances = []
    marker = None
    try:
        with open(path, encoding="utf-8") as handle:
            lines = [line.rstrip("\n") for line in handle]
    except UnicodeDecodeError:
        fail(path, "not UTF-8 text")
        return None
    for line_number, line in enumerate(lines, start=1):
        if MARKER.match(line):
            marker = line
            continue
        match = UTTERANCE.match(line)
        if marker and match:
            utterances.append((line_number, marker, match.group(2)))
    return utterances


def item_label(index, item):
    item_id = item.get("id")
    if isinstance(item_id, str) and ITEM_ID.match(item_id):
        return f"items[{index}] ({item_id})"
    return f"items[{index}]"


def agreement_quote(item):
    agreement = item.get("agreement")
    quote = agreement.get("quote") if isinstance(agreement, dict) else None
    return quote if isinstance(quote, str) else None


def labelled_quotes(index, item):
    if isinstance(item.get("quote"), str):
        yield f"{item_label(index, item)} quote", item["quote"]
    if agreement_quote(item) is not None:
        yield f"{item_label(index, item)} agreement", agreement_quote(item)


def markers_before(quote, utterances):
    if not (isinstance(quote, str) and quote):
        return []
    return [marker for _, marker, text in utterances if quote in text]


def check_decisions(items_path, items):
    for index, item in items:
        if item.get("type") == "decision" and not (agreement_quote(item) or "").strip():
            fail(items_path, f"{item_label(index, item)}: decision without an agreement quote")


def check_quotes_in_transcript(items_path, transcript_path, items, utterances):
    for index, item in items:
        for label, quote in labelled_quotes(index, item):
            if not markers_before(quote, utterances):
                fail(items_path, f"{label} does not occur verbatim in {transcript_path}")
        markers = markers_before(item.get("quote"), utterances)
        ts = item.get("ts")
        if markers and ts not in markers:
            shown_ts = f" {ts}" if isinstance(ts, str) and MARKER.match(ts) else ""
            fail(items_path, f"{item_label(index, item)}: ts{shown_ts} is not the marker before its quote ({markers[0]})")


def check_leaks(targets, quote_sources, utterance_sources):
    for label, path in targets:
        streams = [(stream, runs_of(stream)) for stream in target_token_streams(label, path)]
        for source_label, source in quote_sources:
            if any(leaks_into(source, *stream) for stream in streams):
                fail(label, f"quote leak: {source_label}, {leak_kind(source, 'quote')}")
        for line_number, source in utterance_sources:
            if any(leaks_into(source, *stream) for stream in streams):
                fail(label, f"transcript leak: line {line_number}, {leak_kind(source, 'utterance')}")


def load_items(items_path):
    try:
        with open(items_path, encoding="utf-8") as handle:
            document = json.load(handle)
        return [(index, item) for index, item in enumerate(document.get("items", [])) if isinstance(item, dict)]
    except UnicodeDecodeError:
        fail(items_path, "not UTF-8 text")
        return []
    except (json.JSONDecodeError, AttributeError, TypeError):
        fail(items_path, "not a valid item list")
        return []


def main(items_path, transcript_path, *target_args):
    items = load_items(items_path)
    check_decisions(items_path, items)
    quote_sources = [(label, tokens(quote)) for index, item in items for label, quote in labelled_quotes(index, item)]
    utterance_sources = []
    utterances = read_utterances(transcript_path) if transcript_path else None
    if utterances is not None:
        check_quotes_in_transcript(items_path, transcript_path, items, utterances)
        utterance_tokens = [(line_number, tokens(text)) for line_number, _, text in utterances]
        utterance_sources = [(n, t) for n, t in utterance_tokens if len(t) >= SHORTEST_CHECKED_UTTERANCE]
    check_leaks(zip(target_args[0::2], target_args[1::2]), quote_sources, utterance_sources)
    if failures:
        print("\n".join(failures))
    return 1 if failures else 0


sys.exit(main(*sys.argv[1:]))
PY
)"
readonly checker

if [[ $# -eq 1 && $1 != -* ]]; then
  parse_default_form "$1"
elif [[ $# -ge 1 ]]; then
  parse_explicit_form "$@"
else
  usage
fi

is_input_missing=0
if [[ ! -f $items ]]; then
  fail "$items" "items missing"
  is_input_missing=1
fi
if [[ -n $transcript && ! -f $transcript ]]; then
  fail "$transcript" "transcript missing"
  is_input_missing=1
fi
if [[ $is_input_missing -eq 1 ]]; then
  exit 1
fi

check_schema

if ! python3 -c "$checker" "$items" "$transcript" ${target_label_path_pairs[@]+"${target_label_path_pairs[@]}"} <<<"$roadmap_added"; then
  failed=1
fi

if [[ $failed -eq 1 ]]; then
  exit 1
fi
echo ok

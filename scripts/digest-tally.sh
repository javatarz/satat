#!/usr/bin/env bash
# Tally reviewer statuses in call records and report extraction precision.
#
# Reads only <dir>/YYYY-MM-DD*.md (so agenda-<date>.md is skipped) and counts
# `- status:` lines between `## Items` and the next `## ` heading.
# reviewed = accepted + edited + rejected; strict = accepted / reviewed;
# lenient = (accepted + edited) / reviewed; `-` when reviewed is 0.
#
# usage:
#   scripts/digest-tally.sh <dir>
#
# example:
#   scripts/digest-tally.sh docs/calls
set -euo pipefail

die() {
  echo "error: $*" >&2
  exit 1
}

dir="${1:-}"
[ -n "$dir" ] || die "usage: $0 <dir>"
[ -d "$dir" ] || die "not a directory: $dir"
# A relative operand like a=b/... would read as an awk variable assignment.
case $dir in
  /*) ;;
  *) dir="./$dir" ;;
esac

calls="$(
  for f in "$dir"/[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]*.md; do
    [ -f "$f" ] || continue
    name="${f##*/}"
    printf '%s\n' "${name%.md}"
  done | LC_ALL=C sort
)"

files=()
while IFS= read -r call; do
  [ -n "$call" ] && files+=("$dir/$call.md")
done <<<"$calls"

# LC_ALL=C keeps the decimal separator a dot whatever the user's locale.
LC_ALL=C awk '
  function ratio(n, d) { return d == 0 ? "-" : sprintf("%.2f", n / d) }
  function row(name, a, e, r, m, u,   rv) {
    rv = a + e + r
    print name, rv, a, e, r, m, u, ratio(a, rv), ratio(a + e, rv)
  }
  function flush() {
    if (call == "") return
    row(call, c["accepted"], c["edited"], c["rejected"], c["missed"], c["unreviewed"])
    for (k in c) { t[k] += c[k]; c[k] = 0 }
  }
  BEGIN {
    print "call reviewed accepted edited rejected missed unreviewed strict lenient"
    split("accepted edited rejected missed unreviewed", keys, " ")
    for (i in keys) { c[keys[i]] = 0; t[keys[i]] = 0 }
  }
  FNR == 1 {
    flush()
    call = FILENAME; sub(/^.*\//, "", call); sub(/\.md$/, "", call)
    on = 0
  }
  /^## Items$/ { on = 1; next }
  /^## / { on = 0 }
  on && /^- status:/ {
    s = $0; sub(/^- status:[ ]*/, "", s); sub(/[ \t]+$/, "", s)
    if (!(s in c)) {
      f = FILENAME; sub(/^\.\//, "", f)
      printf "error: %s:%d: unknown status \"%s\"\n", f, FNR, s > "/dev/stderr"
      err = 1
      exit 1
    }
    c[s]++
  }
  END {
    if (err) exit 1
    flush()
    row("all", t["accepted"], t["edited"], t["rejected"], t["missed"], t["unreviewed"])
  }
' ${files[@]+"${files[@]}"} </dev/null

# The published text of a digest, shared by render-summary.sh (the local preview) and
# scripts/publish-digest.sh (what is sent), so the preview is what gets published.
# Input: the outbox, {"date": ..., "pr": {"body": ...}}.

def banner:
  "> **Derived from call transcript, untrusted.** Drafted by an agent from a call transcript; review every line before merging or acting on it.";

def pr_title($date): "Digest call \($date)";

def pr_body($date): "\(banner)\n\n\(.pr.body)";

def pr_preview($date):
  "## PR draft\n\nTitle: \(pr_title($date))\n\n~~~~markdown\n\(pr_body($date))\n~~~~";

# The issue drafts. $items and $map are --slurpfile values: [<items.json>] and [<speaker map>].
# assignee and issue_body take one draft, {item, title, body}; issue_preview takes the outbox.

def item_of($id; $items): [$items[0].items[] | select(.id == $id)][0];

# The map's handle for the action's owner, when it is a valid GitHub handle, else null.
def assignee($map; $items):
  (item_of(.item; $items).owner) as $owner
  | (if ($owner | type) == "string" then $map[0][$owner] else null end)
  | if type == "string" and test("\\A[A-Za-z0-9-]{1,39}\\z") then . else null end;

def issue_body($date; $items):
  item_of(.item; $items) as $item
  | "\(banner)\n\n\(.body)\n\nRaised on the call by \($item.speaker); owner: \($item.owner // "none").\nCall record: docs/calls/\($date).md\n<!-- digest:\($date):\(.item) -->";

def issue_preview($date; $map; $items):
  "## Issue drafts\n\n"
  + ([(.issues // [])[]
      | "### Issue draft \(.item)\n\nTitle: \(.title)\nLabel: needs-triage\nAssignee: \(assignee($map; $items) // "none")\n\n~~~~markdown\n\(issue_body($date; $items))\n~~~~"]
    | if length == 0 then "- none" else join("\n\n") end);

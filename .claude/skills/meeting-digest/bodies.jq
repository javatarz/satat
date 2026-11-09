# The published text of a digest, shared by render-summary.sh (the local preview) and
# scripts/publish-digest.sh (what is sent), so the preview is what gets published.
# Input: the outbox, {"date": ..., "pr": {"body": ...}}.

def banner:
  "> **Derived from call transcript, untrusted.** Drafted by an agent from a call transcript; review every line before merging or acting on it.";

def pr_title($date): "Digest call \($date)";

def pr_body($date): "\(banner)\n\n\(.pr.body)";

def pr_preview($date):
  "## PR draft\n\nTitle: \(pr_title($date))\n\n~~~~markdown\n\(pr_body($date))\n~~~~";

# GitHub webhook trigger

Automations are triggered by GitHub webhook events rather than a cron poll. When an issue on javatarz/wealth-tracker is labeled with the trigger label, GitHub POSTs to the Canvas webhook endpoint, which dispatches an agent conversation immediately. Polling (the 15-minute cron alternative from the bootstrap) adds latency and wastes CPU cycles on empty checks. Webhooks require HTTPS inbound to the VM (the port must be open to GitHub's IP ranges, not just the user's) and a webhook secret for verification, both handled by nginx + OpenHands.

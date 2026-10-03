# Git Sync for automation-as-code

Automation definitions are stored as YAML files in the private `satat-automations` repo and synchronized bidirectionally with Agent Canvas via OpenHands' native Git Sync. Changes to automations in Canvas are exported to git; changes merged into the git branch are imported into Canvas. This is the native approach (no custom generator) and makes automations reviewable via PR. The custom `repos.yaml → generate automations` layer proposed in the bootstrap is dropped — it would duplicate functionality Git Sync already provides and add a maintenance burden.

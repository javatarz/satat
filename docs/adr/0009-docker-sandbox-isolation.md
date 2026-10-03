# Docker sandbox for agent isolation

The agent executes in a Docker sandbox with no mount of the host filesystem, no privileged access, and network reachability only to LiteLLM (localhost:4000) and GitHub. The host-process (no-sandbox) alternative was rejected because the agent writes and runs arbitrary code from GitHub issues; giving it full VM access would compromise the secrets in /etc/satat/, the host Docker socket, and the ability to trust the VM after any agent run. Docker sandboxes mean the agent can only affect its own workspace and the GitHub repo it has an installation token for.

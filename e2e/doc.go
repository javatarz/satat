// Package e2e brings the whole Compose stack up with `satat deploy up` and drives it the way an
// operator does, through the satat CLI, to prove the pieces are connected: Postgres, Hatchet Lite,
// the Docker socket proxy, the worker and a step container it launches (#224).
//
// The tests are behind the e2e build tag, so `go test ./...` skips them: `just e2e` runs them.
// They need Docker and no secrets: every lane runs a fixed command (agent: false), the poller and
// the webhook intake are off, and the worker's tokens are placeholders it never uses.
//
// The stack is the Compose project satat-e2e, configured in docs/scratch/e2e/. It publishes the
// same ports as a dev stack, and `deploy down` removes every Satat agent container on the host,
// so the suite refuses to start while a dev stack or any agent container exists.
package e2e

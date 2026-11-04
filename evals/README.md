# Evals

Harbor tasks that measure Satat (issue #172, plan in `docs/research/v2/evals.md`). Each task in
`tasks/` is a Harbor task folder: `instruction.md` is the issue, `environment/` the repo the agent
works on, `tests/` the hidden grading and `solution/` a reference answer.

An attempt passes when `solved` (the hidden tests pass) and `intact` (the repo's original tests
still pass) are both 1; `reward` in `reward.json` is that pass, so Harbor's mean of it is the pass
rate.

# Tests

The verification suite for the CCC BookStack platform. What each layer proves and
why it's at that layer is the [test plan](../docs/test-plans/bookstack-platform.md);
this file is how to *run* it. CI wiring is in [.github/workflows](../.github/workflows).

```
tests/
├── lib/common.sh                 shared bash helpers (HTTP, DB, session login, readiness)
├── integration/
│   ├── run.sh                    orchestrator: brings up an isolated stack, runs everything
│   ├── helpers/load.bash         bats loader + tiny assertion helpers (no submodules)
│   └── bats/*.bats               behavioral tests (gating, RBAC, revisions, agent-role, persistence, health, media, edge, negative, contact)
├── stress/stress.py             concurrency/load driver (Python stdlib only) — read, edit, mixed modes; optional --max-p95-ms PERF gate
└── stress/stress_selftest.py    offline unit tests for the driver's pure logic (percentile/gate/aggregation) — no stack needed
terraform/tests/plan.tftest.hcl   IaC plan-time security/edge assertions (mocked providers)
terraform/.tflint.hcl             tflint ruleset
```

## Prerequisites

- **Docker + Docker Compose** (the integration stack runs in containers).
- **bats** (`brew install bats-core` / `apt install bats`) — optional; the runner
  falls back to the `bats/bats` Docker image if it's not on PATH.
- `curl`, `jq`, `openssl`, `python3` — present on macOS dev boxes and CI runners.
- For the Terraform tests: **Terraform ≥ 1.7** (for `mock_provider`). No AWS
  credentials are needed — the providers are mocked.

## Integration suite

Brings up an **isolated** copy of `deploy/local/compose.yaml` (its own compose
project, volumes, and port `8089`), so it never touches a real local stack.

```bash
# fast loop — bring up + core behavioral bats only (00-07, skips contact/agent-role/health-DB-down/persistence)
tests/integration/run.sh --profile bats

# PR profile (default) — bats + contact + agent-role + health-DB-down + persistence + stress
tests/integration/run.sh

# full drill — adds backup/restore + the down -v durability boundary (slow)
tests/integration/run.sh --profile full

# debug — leave the stack running afterward
tests/integration/run.sh --profile bats --keep
```

Detect upstream image drift (what the **weekly** CI job does) by pinning to latest:

```bash
BOOKSTACK_TAG=latest MARIADB_TAG=latest tests/integration/run.sh --profile full
```

## Terraform tests

```bash
cd terraform
terraform init -backend=false
terraform test                      # plan-time assertions, no AWS creds
terraform fmt -recursive -check
tflint --init && tflint --recursive
```

## Stress driver (standalone)

```bash
python3 tests/stress/stress.py --base-url http://localhost:8089 \
  --token "<id:secret>" --mode read  --concurrency 50 --per-worker 20
python3 tests/stress/stress.py --base-url http://localhost:8089 \
  --token "<id:secret>" --mode edit  --page-id 1 --concurrency 8 --per-worker 5
python3 tests/stress/stress.py --base-url http://localhost:8089 \
  --token "<id:secret>" --mode mixed  --page-id 1 --concurrency 10 --per-worker 6

# add an optional p95 latency budget (fails with a non-zero exit if exceeded)
python3 tests/stress/stress.py --base-url http://localhost:8089 \
  --token "<id:secret>" --mode read --concurrency 50 --per-worker 20 --max-p95-ms 800

# the driver's pure logic (percentile/gate/aggregation) — offline, no stack:
python3 tests/stress/stress_selftest.py        # also: make stress-selftest
```

## Make targets

`make test` runs the lot the way CI does. `make stress-selftest` runs the offline
stress-driver unit tests (part of `make check`). See [`Makefile`](../Makefile).

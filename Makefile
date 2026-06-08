# CCC BookStack — local test entrypoints. Every tool runs via a pinned Docker
# image so a contributor needs only Docker + this Makefile, and runs the exact
# same versions CI does. See tests/README.md and docs/test-plans/.
#
#   make help              list targets
#   make check             all static/lint/IaC gates (the PR fast path)
#   make test              check + integration (PR profile)
#   make integration-full  the full drill incl. backup/restore + boundary

SHELL := bash
.DEFAULT_GOAL := help

# ---- pinned tool images (verified to exist; mirror .github/workflows) -------
TF_IMG        := hashicorp/terraform:1.13.3
TFLINT_IMG    := ghcr.io/terraform-linters/tflint:v0.63.1
SHELLCHECK_IMG:= koalaman/shellcheck:v0.10.0
ACTIONLINT_IMG:= rhysd/actionlint:1.7.12
TRIVY_IMG     := aquasec/trivy:0.71.0
CHECKOV_IMG   := bridgecrew/checkov:3.2.532
GITLEAKS_IMG  := ghcr.io/gitleaks/gitleaks:v8.30.1
LYCHEE_IMG    := lycheeverse/lychee:0.15.1
GO_IMG        := golang:1.23-alpine
PY_IMG        := python:3.12-alpine

DKR := docker run --rm -v "$(PWD)":/work -w /work
DKR_TF := docker run --rm -v "$(PWD)/terraform":/tf -w /tf

.PHONY: help
help: ## List targets
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-20s\033[0m %s\n",$$1,$$2}'

# ---- Terraform --------------------------------------------------------------
.PHONY: fmt
fmt: ## terraform fmt -check (recursive)
	$(DKR_TF) --entrypoint terraform $(TF_IMG) fmt -recursive -check -diff

.PHONY: validate
validate: ## terraform validate (no backend, no creds)
	$(DKR_TF) --entrypoint sh $(TF_IMG) -c 'terraform init -backend=false -input=false && terraform validate'

.PHONY: tflint
tflint: ## tflint (recursive) with the AWS ruleset
	# -e GITHUB_TOKEN: authenticate the ruleset download so `tflint --init` doesn't hit
	# the 60 req/hr unauthenticated GitHub API cap (see ci.yml). No-op if the var is unset.
	$(DKR_TF) -e GITHUB_TOKEN --entrypoint sh $(TFLINT_IMG) -c 'tflint --init && tflint --recursive --format=compact'

.PHONY: tf-test
tf-test: ## terraform test — plan-time security/edge assertions (mocked providers)
	$(DKR_TF) --entrypoint sh $(TF_IMG) -c 'terraform init -backend=false -input=false && terraform test'

# ---- IaC security scanners --------------------------------------------------
.PHONY: trivy
trivy: ## trivy config scan over terraform/
	$(DKR) $(TRIVY_IMG) config --exit-code 1 --severity HIGH,CRITICAL --ignorefile /work/.trivyignore terraform

.PHONY: checkov
checkov: ## checkov scan over terraform/
	$(DKR) --entrypoint checkov $(CHECKOV_IMG) -d terraform --quiet --compact

# ---- Shell / compose / workflows / secrets / links --------------------------
.PHONY: shellcheck
shellcheck: ## shellcheck all shell + the rendered user-data template
	./tests/lib/render_user_data.sh > /tmp/_user_data.rendered.sh
	$(DKR) -v /tmp/_user_data.rendered.sh:/work/_user_data.rendered.sh $(SHELLCHECK_IMG) \
	  --shell=bash --severity=warning \
	  deploy/local/verify.sh deploy/local/dev-up.sh deploy/local/deploy-remote.sh deploy/local/snapshot.sh deploy/local/apply-brand.sh deploy/local/apply-agent-role.sh \
	  tests/lib/common.sh tests/integration/run.sh \
	  tests/integration/helpers/load.bash tests/lib/render_user_data.sh \
	  tests/lib/check_pins.sh tests/lib/check_user_data_contract.sh tests/lib/check_theme_bridge.sh _user_data.rendered.sh

.PHONY: compose-config
compose-config: ## validate deploy/local/compose.yaml renders
	BOOKSTACK_TAG=test MARIADB_TAG=test APP_URL=http://localhost APP_KEY=base64:x \
	  DB_ROOT_PASSWORD=x DB_PASSWORD=x \
	  docker compose -f deploy/local/compose.yaml config -q && echo "compose OK"

.PHONY: actionlint
actionlint: ## lint GitHub Actions workflows
	$(DKR) $(ACTIONLINT_IMG) -color

.PHONY: secrets
secrets: ## gitleaks secret scan
	$(DKR) $(GITLEAKS_IMG) detect --source=/work --config=/work/.gitleaks.toml --redact --verbose

.PHONY: links
links: ## broken-link check over docs (offline: local files only)
	$(DKR) $(LYCHEE_IMG) --offline --no-progress --exclude-path .github '**/*.md'

.PHONY: pins
pins: ## assert pins don't float + Makefile tool pins == CI pins (single source of truth)
	./tests/lib/check_pins.sh

.PHONY: user-data-contract
user-data-contract: ## assert rendered user-data fetches secrets (bakes none)
	./tests/lib/check_user_data_contract.sh

.PHONY: theme-bridge
theme-bridge: ## assert the cross-origin theme-bridge contract is identical in both copies
	./tests/lib/check_theme_bridge.sh

.PHONY: contact-test
contact-test: ## gofmt + vet + go test the contact service (unit; no network/deps, pinned go image)
	$(DKR) -w /work/services/contact $(GO_IMG) sh -c 'test -z "$$(gofmt -l .)" || { echo "gofmt drift:"; gofmt -l .; exit 1; }; go vet ./... && go test ./...'

.PHONY: wiki-client-test
wiki-client-test: ## gofmt + vet + go test the shared wiki client core (unit; no network/deps, pinned go image)
	$(DKR) -w /work/services/wiki-client $(GO_IMG) sh -c 'test -z "$$(gofmt -l .)" || { echo "gofmt drift:"; gofmt -l .; exit 1; }; go vet ./... && go test ./...'

.PHONY: wiki-cli-test
wiki-cli-test: ## gofmt + vet + go test the ccc-wiki CLI (unit; no network/deps, pinned go image)
	$(DKR) -w /work/services/wiki-cli $(GO_IMG) sh -c 'test -z "$$(gofmt -l .)" || { echo "gofmt drift:"; gofmt -l .; exit 1; }; go vet ./... && go test ./...'

.PHONY: wiki-cli-build
wiki-cli-build: ## build a static ccc-wiki binary via the pinned go image -> services/wiki-cli/bin/ccc-wiki
	$(DKR) -w /work/services/wiki-cli -e CGO_ENABLED=0 $(GO_IMG) \
	  go build -trimpath -ldflags="-s -w" -o /work/services/wiki-cli/bin/ccc-wiki .

.PHONY: stress-mixed
stress-mixed: ## mixed read-write stress test (T-024)
	python3 tests/stress/stress.py --base-url http://localhost:8089 \
	  --token "<id:secret>" --mode mixed --page-id 1 --concurrency 10 --per-worker 6

.PHONY: stress-selftest
stress-selftest: ## offline unit tests for the stress driver's pure logic (percentile/gate/aggregation)
	# Via the pinned python image so `make check` needs only Docker (CI's lint job,
	# which already has python3, runs the same script directly).
	$(DKR) $(PY_IMG) python3 tests/stress/stress_selftest.py

# ---- deploy (developer action; touches a remote host, so NOT in `check`) ----
.PHONY: deploy
deploy: ## rsync working tree to connor-server + (re)launch the stack (reuses dev-up.sh)
	deploy/local/deploy-remote.sh

.PHONY: apply-theme
apply-theme: ## re-apply the CCC brand (head + logo/favicon/color) to the running stack (no restart; deploys do this automatically)
	bash deploy/local/apply-brand.sh

.PHONY: apply-agent-role
apply-agent-role: ## re-apply the least-privilege "Agent author" API role to the running stack (no restart; deploys do this automatically)
	bash deploy/local/apply-agent-role.sh

# NB: contact-test / wiki-client-test / wiki-cli-test run `go test` WITHOUT -race (the
# alpine image has no C toolchain). CI installs gcc/musl-dev and runs `go test -race
# -count=1`, so a data race can pass `make check` but fail CI. To reproduce CI locally:
#   docker run --rm -v "$(PWD)":/work -w /work/services/contact $(GO_IMG) \
#     sh -c 'apk add --no-cache gcc musl-dev >/dev/null && CGO_ENABLED=1 go test -race ./...'

# ---- aggregates -------------------------------------------------------------
.PHONY: check
check: fmt validate tflint tf-test trivy checkov shellcheck compose-config actionlint secrets links pins user-data-contract theme-bridge contact-test wiki-client-test wiki-cli-test stress-selftest ## all static/IaC gates

.PHONY: integration
integration: ## integration suite (PR profile)
	tests/integration/run.sh --profile pr

.PHONY: integration-full
integration-full: ## full integration drill (backup/restore + boundary)
	tests/integration/run.sh --profile full

.PHONY: test
test: check integration ## everything the PR pipeline runs

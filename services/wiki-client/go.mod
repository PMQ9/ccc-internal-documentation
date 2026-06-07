module github.com/PMQ9/ccc-internal-documentation/services/wiki-client

// Standard library only — no third-party dependencies. Mirrors services/contact:
// it keeps the CVE surface minimal (the repo gates on trivy/checkov/gitleaks),
// needs no go.sum/vendor, and means the CLI (#28) and MCP server (#29) that import
// this core inherit zero transitive deps. net/http + encoding/json + crypto/rand
// cover everything here. The go version is pin-locked to GO_IMG (see check_pins.sh).
go 1.23

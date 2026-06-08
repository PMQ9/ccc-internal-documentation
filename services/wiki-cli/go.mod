module github.com/PMQ9/ccc-internal-documentation/services/wiki-cli

// Standard library only — same posture as services/contact and services/wiki-client.
// The single require is the in-repo client core, resolved by the replace below to the
// sibling directory; because both modules are dependency-free, no go.sum is produced
// and nothing is fetched from the network (CI builds offline in golang:1.23-alpine).
// The go version is pin-locked to GO_IMG (see check_pins.sh). (issue #28)
go 1.23

require github.com/PMQ9/ccc-internal-documentation/services/wiki-client v0.0.0

replace github.com/PMQ9/ccc-internal-documentation/services/wiki-client => ../wiki-client

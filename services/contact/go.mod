module github.com/PMQ9/ccc-internal-documentation/services/contact

// Standard library only — no third-party dependencies. This keeps the image
// tiny, the CVE surface minimal (the repo gates on trivy/checkov), and the
// service auditable. net/smtp covers the relay; net/http covers Graph + GitHub.
go 1.23

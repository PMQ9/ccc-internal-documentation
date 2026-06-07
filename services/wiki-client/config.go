package wikiclient

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the client's runtime configuration, read once from the environment
// (12-factor) and passed to New; nothing deeper in the call tree reads the
// environment. The token is a secret: it is never logged and never placed in an
// error message (see errors.go and the no-secret-in-logs test).
type Config struct {
	BaseURL string // WIKI_BASE_URL, e.g. http://10.76.88.214 or http://localhost:8080 (no /api suffix)
	Token   string // WIKI_API_TOKEN, BookStack form "<token_id>:<secret>" — SECRET, never logged

	HTTPTimeout    time.Duration // per-request timeout (WIKI_HTTP_TIMEOUT, default 15s)
	MaxRetries     int           // retry budget on 5xx/transport errors (WIKI_MAX_RETRIES, default 3; 0 disables)
	RetryBaseDelay time.Duration // base for exponential backoff (WIKI_RETRY_BASE_DELAY, default 200ms)
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// envInt / envDuration return the default when the variable is unset, but a value
// that is PRESENT-but-unparseable is a typo the operator should hear about — they
// record it in errs (and still return the default) so Load can fail loud rather
// than silently run with a setting the user thinks they changed.
func envInt(k string, def int, errs *[]string) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s=%q is not an integer", k, v))
		return def
	}
	return n
}

func envDuration(k string, def time.Duration, errs *[]string) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s=%q is not a duration (e.g. 15s, 200ms)", k, v))
		return def
	}
	return d
}

// Load reads configuration from the environment and validates the parts that must
// be coherent to make a request at all. It does NOT make a network call. A present
// but malformed WIKI_* value is an error, not a silent fallback to the default.
func Load() (Config, error) {
	var errs []string
	c := Config{
		BaseURL:        strings.TrimRight(env("WIKI_BASE_URL", ""), "/"),
		Token:          os.Getenv("WIKI_API_TOKEN"),
		HTTPTimeout:    envDuration("WIKI_HTTP_TIMEOUT", 15*time.Second, &errs),
		MaxRetries:     envInt("WIKI_MAX_RETRIES", 3, &errs),
		RetryBaseDelay: envDuration("WIKI_RETRY_BASE_DELAY", 200*time.Millisecond, &errs),
	}
	if len(errs) > 0 {
		return c, fmt.Errorf("invalid WIKI_* configuration: %s", strings.Join(errs, "; "))
	}
	return c, c.validate()
}

func (c Config) validate() error {
	if c.BaseURL == "" {
		return fmt.Errorf("WIKI_BASE_URL is required")
	}
	if !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return fmt.Errorf("WIKI_BASE_URL must start with http:// or https://, got %q", c.BaseURL)
	}
	if c.Token == "" {
		return fmt.Errorf("WIKI_API_TOKEN is required")
	}
	// BookStack tokens are "<token_id>:<secret>"; reject the obvious bad shapes early
	// rather than after a 401. The token value is NEVER echoed — it's a secret.
	if id, secret, ok := strings.Cut(c.Token, ":"); !ok || id == "" || secret == "" {
		return fmt.Errorf("WIKI_API_TOKEN must be in the form <token_id>:<secret>")
	}
	if c.MaxRetries < 0 {
		return fmt.Errorf("WIKI_MAX_RETRIES must be >= 0, got %d", c.MaxRetries)
	}
	return nil
}

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

// resolveConfig builds the client config from (in precedence order) explicit flags, the
// environment, and the config file. The token is sourced ONLY from the environment or
// the file — never a flag (resolveConfig has no --token input). It does not make a
// network call; wikiclient.New validates the result.
//
//	base-url:    --base-url > WIKI_BASE_URL > config file > (error if empty)
//	token:       WIKI_API_TOKEN > config file > (error if empty)   [no flag]
//	timeout:     --timeout > WIKI_HTTP_TIMEOUT > 15s
//	max-retries: --max-retries > WIKI_MAX_RETRIES > 3
func resolveConfig(g *globalFlags, getenv func(string) string) (wikiclient.Config, error) {
	path, explicit := configPath(g, getenv)
	fileVals, err := loadConfigFile(path, explicit)
	if err != nil {
		return wikiclient.Config{}, err
	}

	baseURL := fileVals["WIKI_BASE_URL"]
	if v := getenv("WIKI_BASE_URL"); v != "" {
		baseURL = v
	}
	if g.seen["base-url"] {
		baseURL = g.baseURL
	}

	token := fileVals["WIKI_API_TOKEN"]
	if v := getenv("WIKI_API_TOKEN"); v != "" {
		token = v
	}

	timeout := 15 * time.Second
	if v := getenv("WIKI_HTTP_TIMEOUT"); v != "" {
		d, perr := time.ParseDuration(v)
		if perr != nil {
			return wikiclient.Config{}, fmt.Errorf("WIKI_HTTP_TIMEOUT=%q is not a duration (e.g. 15s, 200ms)", v)
		}
		timeout = d
	}
	if g.seen["timeout"] {
		timeout = g.timeout
	}

	maxRetries := 3
	if v := getenv("WIKI_MAX_RETRIES"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return wikiclient.Config{}, fmt.Errorf("WIKI_MAX_RETRIES=%q is not an integer", v)
		}
		maxRetries = n
	}
	if g.seen["max-retries"] {
		maxRetries = g.maxRetries
	}

	if baseURL == "" {
		return wikiclient.Config{}, fmt.Errorf("no wiki base URL: set WIKI_BASE_URL, pass --base-url, or add WIKI_BASE_URL to the config file")
	}
	if token == "" {
		// Never echo any token value — name the source instead.
		return wikiclient.Config{}, fmt.Errorf("no API token: set WIKI_API_TOKEN, or add WIKI_API_TOKEN to a 0600 config file (%s)", displayPath(path))
	}

	return wikiclient.Config{
		BaseURL:        baseURL,
		Token:          token,
		HTTPTimeout:    timeout,
		MaxRetries:     maxRetries,
		RetryBaseDelay: 200 * time.Millisecond,
	}, nil
}

// configPath resolves the config-file path and whether it was explicitly requested
// (--config or $CCC_WIKI_CONFIG). An explicit-but-missing file is an error; the default
// path being absent is not.
func configPath(g *globalFlags, getenv func(string) string) (path string, explicit bool) {
	// An explicit --config (even an empty one) is honored as explicit, so `--config ""`
	// surfaces a clear error in loadConfigFile rather than silently falling through.
	if g.seen["config"] {
		return g.configPath, true
	}
	if v := getenv("CCC_WIKI_CONFIG"); v != "" {
		return v, true
	}
	// Default (non-explicit) path. os.UserHomeDir reads $HOME, so under root in a
	// container/CI this is /root/.config/ccc-wiki/config; that's acceptable because the
	// default file is optional (env/flags can fully configure) and a token file there
	// still has to pass the 0600 perms check below.
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".config", "ccc-wiki", "config"), false
}

// loadConfigFile reads a KEY=VALUE config file. A missing default-path file yields an
// empty map (env/flags may fully configure); a missing EXPLICIT file is an error. If the
// file carries a token it must be mode 0600 (no group/other bits) — like ssh StrictModes
// — since it holds a write-capable credential; otherwise the CLI refuses to read it.
func loadConfigFile(path string, explicit bool) (map[string]string, error) {
	if path == "" {
		if explicit { // e.g. `--config ""` — a mistake, not "no config file"
			return nil, usagef("--config was given an empty path")
		}
		return map[string]string{}, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if explicit {
				return nil, usagef("config file %s does not exist", path)
			}
			return map[string]string{}, nil
		}
		return nil, usagef("config file %s: %v", path, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, usagef("read config file %s: %v", path, err)
	}
	vals := parseConfigFile(string(b))
	// Enforce perms only when a token is present (a base-url-only file is not a secret).
	// POSIX mode bits; Phase 0 is Linux/macOS (the LAN dev/staging hosts).
	if vals["WIKI_API_TOKEN"] != "" {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			return nil, usagef("config file %s holds a token but is accessible by group/others (mode %04o); run: chmod 600 %s", path, perm, path)
		}
	}
	return vals, nil
}

// parseConfigFile parses KEY=VALUE lines; blank lines and # comments are skipped, the
// value is everything after the first '=' (trimmed). No quoting/escaping — matches the
// repo's .env-style convention and keeps the parser stdlib-only.
func parseConfigFile(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// displayPath renders a path for an error message, or a placeholder when none resolved.
func displayPath(path string) string {
	if path == "" {
		return "no config file path resolved"
	}
	return path
}

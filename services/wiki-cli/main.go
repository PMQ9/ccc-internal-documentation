package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// version is overridden at build time via -ldflags="-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run is the composition root and the real entry point (main just wires os.* and exits
// with its return). It parses global flags, dispatches to a handler, and maps the
// handler's error to an exit code. Returning an int (not calling os.Exit) makes the whole
// dispatch path table-testable.
func run(argv []string, stdout, stderr io.Writer, getenv func(string) string) int {
	g := newGlobalFlags()

	// Help/version as the first token — handle before flag parsing so they always print
	// to stdout and exit 0 (flag otherwise treats -h/--help as a parse error).
	if len(argv) > 0 {
		switch argv[0] {
		case "help", "-h", "--help", "-help":
			usage(stdout)
			return codeOK
		case "version", "--version", "-version":
			fmt.Fprintln(stdout, version)
			return codeOK
		}
	}

	// Leading global parse: consumes flags up to the first positional (the resource).
	gfs := flag.NewFlagSet("ccc-wiki", flag.ContinueOnError)
	gfs.SetOutput(stderr)
	bindGlobal(gfs, g)
	gfs.Usage = func() {} // usage is rendered explicitly below; suppress flag's auto dump
	if err := gfs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) { // e.g. `--json -h`
			usage(stdout)
			return codeOK
		}
		usage(stderr)
		return codeUsage
	}
	markSeen(gfs, g)
	rest := gfs.Args()

	if len(rest) == 0 || rest[0] == "help" {
		usage(stdout)
		return codeOK
	}
	if rest[0] == "version" {
		fmt.Fprintln(stdout, version)
		return codeOK
	}
	if len(rest) < 2 {
		fmt.Fprintf(stderr, "ccc-wiki: expected '<resource> <action>'; run 'ccc-wiki help'\n")
		return codeUsage
	}

	resource, action := rest[0], rest[1]
	h, ok := lookup(resource, action)
	if !ok {
		fmt.Fprintf(stderr, "ccc-wiki: unknown command %q %q; run 'ccc-wiki help'\n", resource, action)
		return codeUsage
	}

	cc := &cmdContext{g: g, stdout: stdout, stderr: stderr, stdin: os.Stdin, getenv: getenv}
	if err := h(context.Background(), cc, rest[2:]); err != nil {
		writeError(stderr, g.json, err)
		return exitCode(err)
	}
	return codeOK
}

// usage prints the help text. It never prints any secret; it states the token rule.
func usage(w io.Writer) {
	fmt.Fprint(w, `ccc-wiki — headless client for the CCC BookStack wiki

usage: ccc-wiki [global flags] <resource> <action> [action flags]

resources and actions:
  book      list | get --id N | create --name S [--description S] | update --id N [...]
  chapter   list | get --id N | create --book N --name S [--description S] | update --id N [...]
  page      list | get --id N
            create --book N|--chapter N --name S (--markdown S|--markdown-file P|--html S|--html-file P)
            update --id N [--name S] [body flags]
  attachment upload --page N --name S --file P
  image      upload --page N --name S --file P [--type gallery|drawio]

  (no delete/admin commands — the Agent author role does not grant them)

global flags:
  --json              machine-readable JSON output (errors to stderr)
  --base-url URL      wiki base URL (overrides WIKI_BASE_URL)
  --timeout D         per-request timeout (default 15s; overrides WIKI_HTTP_TIMEOUT)
  --max-retries N     retry budget on 5xx/transport errors (default 3; overrides WIKI_MAX_RETRIES)
  --config PATH       config file (default $CCC_WIKI_CONFIG or ~/.config/ccc-wiki/config)
  help, version

auth (never a flag — would leak in ps/shell history/CI logs):
  WIKI_BASE_URL, WIKI_API_TOKEN ("<id>:<secret>") via env, or a 0600 config file.
  A body flag's "-" value (e.g. --markdown-file -) reads from stdin.

exit codes: 0 ok · 2 usage/config · 3 auth(401) · 4 forbidden/not-found(403/404) · 5 server(5xx) · 1 other
`)
}

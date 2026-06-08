package main

import (
	"context"
	"flag"
	"io"
	"time"

	wikiclient "github.com/PMQ9/ccc-internal-documentation/services/wiki-client"
)

// api is the slice of the wiki-client core the CLI uses. *wikiclient.Client satisfies
// it; tests back it with an httptest-server-backed real client (so they exercise the
// wire format) or a fake. Handlers depend on this interface, not on transport.
type api interface {
	ListBooks(ctx context.Context) ([]wikiclient.Book, error)
	GetBook(ctx context.Context, id int64) (wikiclient.Book, error)
	CreateBook(ctx context.Context, in wikiclient.Book) (wikiclient.Book, error)
	UpdateBook(ctx context.Context, id int64, in wikiclient.Book) (wikiclient.Book, error)

	ListChapters(ctx context.Context) ([]wikiclient.Chapter, error)
	GetChapter(ctx context.Context, id int64) (wikiclient.Chapter, error)
	CreateChapter(ctx context.Context, in wikiclient.Chapter) (wikiclient.Chapter, error)
	UpdateChapter(ctx context.Context, id int64, in wikiclient.Chapter) (wikiclient.Chapter, error)

	ListPages(ctx context.Context) ([]wikiclient.Page, error)
	GetPage(ctx context.Context, id int64) (wikiclient.Page, error)
	CreatePage(ctx context.Context, in wikiclient.Page) (wikiclient.Page, error)
	UpdatePage(ctx context.Context, id int64, in wikiclient.Page) (wikiclient.Page, error)

	UploadAttachment(ctx context.Context, pageID int64, name, fileName string, r io.Reader) (wikiclient.Attachment, error)
	UploadImage(ctx context.Context, pageID int64, name, imgType, fileName string, r io.Reader) (wikiclient.Image, error)
}

// globalFlags are the flags valid before any subcommand (and accepted again after it).
// seen records which were explicitly set on the command line, so resolveConfig can apply
// flag > env > file precedence without confusing an unset flag for a real value.
type globalFlags struct {
	json       bool
	baseURL    string
	timeout    time.Duration
	maxRetries int
	configPath string
	seen       map[string]bool
}

func newGlobalFlags() *globalFlags {
	return &globalFlags{timeout: 15 * time.Second, maxRetries: 3, seen: map[string]bool{}}
}

// bindGlobal registers the global flags on fs, bound to g's fields with defaults equal
// to g's current values. Registering them on BOTH the leading FlagSet and each
// subcommand FlagSet lets `--json` (etc.) appear in either position without re-reading.
func bindGlobal(fs *flag.FlagSet, g *globalFlags) {
	fs.BoolVar(&g.json, "json", g.json, "emit machine-readable JSON (errors go to stderr)")
	fs.StringVar(&g.baseURL, "base-url", g.baseURL, "wiki base URL (overrides WIKI_BASE_URL)")
	fs.DurationVar(&g.timeout, "timeout", g.timeout, "per-request timeout")
	fs.IntVar(&g.maxRetries, "max-retries", g.maxRetries, "retry budget on 5xx/transport errors")
	fs.StringVar(&g.configPath, "config", g.configPath, "config file path (default $CCC_WIKI_CONFIG or ~/.config/ccc-wiki/config)")
}

// markSeen records the flags actually provided on fs into g.seen.
func markSeen(fs *flag.FlagSet, g *globalFlags) {
	fs.Visit(func(f *flag.Flag) { g.seen[f.Name] = true })
}

// cmdContext carries everything a handler needs: the resolved global flags, the I/O
// streams, the environment accessor, and a lazily-built client (resolved after the
// subcommand's own flags are parsed, so a post-subcommand --base-url still applies).
type cmdContext struct {
	g      *globalFlags
	stdout io.Writer
	stderr io.Writer
	stdin  io.Reader
	getenv func(string) string

	// client memoization (and a test seam: set cached+resolved to inject a fake).
	cached    api
	cachedErr error
	resolved  bool
}

// newFlagSet creates a subcommand FlagSet that writes usage to stderr and accepts the
// global flags again (so they work after the subcommand too).
func (cc *cmdContext) newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(cc.stderr)
	bindGlobal(fs, cc.g)
	return fs
}

// parse parses the subcommand args and records which globals were (re)set. A parse error
// becomes a usageError (exit 2); flag has already printed the message.
func (cc *cmdContext) parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return &usageError{err}
	}
	markSeen(fs, cc.g)
	return nil
}

// client resolves config (env/file/flags) and builds the wiki-client once. Config or
// validation problems are usage errors (exit 2). The token never appears in the error.
func (cc *cmdContext) client() (api, error) {
	if cc.resolved {
		return cc.cached, cc.cachedErr
	}
	cc.resolved = true
	cfg, err := resolveConfig(cc.g, cc.getenv)
	if err != nil {
		cc.cachedErr = err // resolveConfig already returns usageErrors
		return nil, err
	}
	cl, err := wikiclient.New(cfg)
	if err != nil {
		cc.cachedErr = &usageError{err} // malformed config -> exit 2 (token never echoed by New)
		return nil, cc.cachedErr
	}
	cc.cached = cl
	return cl, nil
}

// handlerFunc runs one resource/action. It owns its own flag parsing (via cc.newFlagSet)
// and obtains the client via cc.client() AFTER parsing, so flag-supplied config applies.
type handlerFunc func(ctx context.Context, cc *cmdContext, args []string) error

// commands is the dispatch table: resource -> action -> handler. The surface is exactly
// the sanctioned-endpoints set — read/create/update + media upload, no delete/admin.
var commands = map[string]map[string]handlerFunc{
	"book": {
		"list": cmdBookList, "get": cmdBookGet, "create": cmdBookCreate, "update": cmdBookUpdate,
	},
	"chapter": {
		"list": cmdChapterList, "get": cmdChapterGet, "create": cmdChapterCreate, "update": cmdChapterUpdate,
	},
	"page": {
		"list": cmdPageList, "get": cmdPageGet, "create": cmdPageCreate, "update": cmdPageUpdate,
	},
	"attachment": {"upload": cmdAttachmentUpload},
	"image":      {"upload": cmdImageUpload},
}

// lookup returns the handler for a resource/action, or false if unknown.
func lookup(resource, action string) (handlerFunc, bool) {
	r, ok := commands[resource]
	if !ok {
		return nil, false
	}
	h, ok := r[action]
	return h, ok
}

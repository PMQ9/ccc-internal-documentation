// Command ccc-wiki is the headless CLI for CCC's BookStack wiki (issue #28). It is a
// thin client over the shared Go core in services/wiki-client/ — it reuses that core's
// auth, retries, typed models, and typed errors rather than re-implementing transport.
// Built for humans and CI: scriptable subcommands, a --json output mode, and
// exit codes that map to the failure class.
//
// Usage:
//
//	ccc-wiki [global flags] <resource> <action> [action flags]
//
//	resources/actions:
//	  book     list | get | create | update
//	  chapter  list | get | create | update
//	  page     list | get | create | update
//	  attachment upload          (multipart file -> a page)
//	  image      upload          (multipart image -> a page)
//
// The surface is deliberately the sanctioned-endpoints set of the least-privilege
// "Agent author" role (see docs/runbooks/agent-api.md): read/create/update plus media
// upload. There is NO delete or admin command — a capability the role never grants.
//
// Auth is BookStack's token scheme, exactly as the core defines it. The token is read
// from the WIKI_API_TOKEN environment variable or a config file — NEVER from a flag (a
// flag would leak the secret into the process table, shell history, and CI logs). The
// token is never printed or logged.
//
//	WIKI_BASE_URL   wiki base URL, e.g. http://10.76.88.214 (also --base-url)
//	WIKI_API_TOKEN  BookStack token "<id>:<secret>" (or in the config file; never a flag)
//	WIKI_HTTP_TIMEOUT, WIKI_MAX_RETRIES  optional (also --timeout, --max-retries)
//
// Config file (optional): KEY=VALUE lines (WIKI_BASE_URL, WIKI_API_TOKEN). Path:
// --config, else $CCC_WIKI_CONFIG, else ~/.config/ccc-wiki/config. If it holds a token
// it must be mode 0600 (no group/other access) or the CLI refuses to use it.
//
// Exit codes: 0 success; 2 usage/config error; 3 auth (401); 4 forbidden/not-found
// (403/404); 5 server error (5xx after retries); 1 any other error.
package main

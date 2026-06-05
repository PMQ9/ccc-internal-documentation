# Changelog

Notable, reader- or operator-facing changes to this repo — new behavior, infra, gates, docs, and
runbooks. Routine commits and refactors are left out. **Newest first.** This is config and
infrastructure, not versioned software, so entries are dated rather than tagged.

| Date | Change | PR |
|---|---|---|
| 2026-06-05 | Light/dark + login UX in the brand theme layer — default follows the OS (`prefers-color-scheme`), one toggle (the duplicate homepage one is hidden), and the login screen gets a theme toggle + show/hide password (icons in `deploy/branding/assets/`) | — |
| 2026-06-05 | connor-server is now a live, continuously-deployed Phase-0 instance (LAN-only, plain HTTP, seeded admin — not production) — on-demand `make deploy` and auto-on-merge GitOps via a self-hosted runner (`deploy.yml`), snapshot-before-deploy, paths/labels as repo Variables, `docs/runbooks/connor-server-deploy.md` | #14 |
| 2026-06-05 | Documentation policy (expand existing docs, table-first, concise, no emojis), this changelog, and a README "Updates" section | — |
| 2026-06-05 | Project status tracker — phases, decisions, open items, roadmap, risks (`docs/status.md`) | #10 |
| 2026-06-03 | Local-dev deploy script + VSCode run button | #9 |
| 2026-06-03 | CCC brand & theming — real logo lockup, favicon, BookStack theme layer, brand guidelines | #8 |
| 2026-06-03 | Architecture-review hardening — pin-drift gate, user-data contract gate, engineering conventions | #6 |
| 2026-06-03 | Dependency bumps — GitHub Actions + Terraform AWS provider (Dependabot) | #3, #4, #7 |
| 2026-06-03 | Comprehensive test suite + CI/CD — bats, stress driver, isolated integration runner, 3 workflows | #2 |
| 2026-06-03 | GitHub issue/PR templates + security policy | — |
| 2026-06-03 | Initial BookStack platform — validated local stack + AWS Terraform footprint | #1 |

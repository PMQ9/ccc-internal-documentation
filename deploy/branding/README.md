# CCC branding — apply & validate

The applied brand layer for the BookStack wiki. The design *intent* lives in
[docs/brand/ccc-brand-guidelines.md](../../docs/brand/ccc-brand-guidelines.md); this directory is what
actually ships.

```
deploy/branding/
  ccc-custom-head.html              # paste into Settings → Customization → Custom HTML Head Content
  assets/ccc-logo-placeholder.svg   # non-trademarked placeholder; replace with the authorized lockup
  README.md                         # this file
```

## Why branding is part config, not all code

BookStack theming splits across three levers. Two are file-based and version-controlled here; one is a
DB-stored UI setting applied once per environment — the same pattern this repo already uses for the
public-read toggle and the default registration role (see
[docs/architecture.md](../../docs/architecture.md#bookstack-configuration-the-load-bearing-bits)).

| Lever | Mechanism | Where it's defined |
|---|---|---|
| App name | `APP_NAME` env var | `deploy/local/.env.example`, `compose.yaml`, `terraform/user-data.sh.tftpl` |
| Colors, header, links, a11y CSS | Custom HTML Head Content (DB) | `ccc-custom-head.html` (paste once per env) |
| Logo / favicon / primary color | Settings → Customization (DB upload + picker) | applied once; asset in `assets/` |

We deliberately do **not** use BookStack's PHP theme system (`APP_THEME` + `themes/<name>/`): upstream
marks it "not stable, may change on any update," and it adds a mounted-volume moving part for no gain
over the supported custom-head path.

## Apply procedure

Do this on the local stack first (`connor-server`), confirm, then repeat on AWS after launch.

1. **App name** — already wired via `APP_NAME` (default `CCC Wiki`). Override in `.env` if desired.
2. **Custom head CSS** — Settings → Customization → **Custom HTML Head Content**. Paste the entire
   contents of [`ccc-custom-head.html`](./ccc-custom-head.html). Save.
3. **Primary color** — Settings → Customization → **Application primary color** → `#946E24` (Oak). The
   CSS sets this too, but the setting also drives a few server-rendered spots, so set both.
4. **Logo** — Settings → Customization → **Logo** → upload the authorized CCC lockup (PNG/SVG, ≥250 px
   wide). Until then, [`assets/ccc-logo-placeholder.svg`](./assets/ccc-logo-placeholder.svg) is a
   stand-in — **not** an official mark.
5. **Favicon** — Settings → Customization → **Favicon** → upload (32×32 or SVG).
6. Hard-refresh (Cmd/Ctrl+Shift+R) and run the validation checklist below.

## Validation checklist (WCAG 2.2 AA)

This is the **themed-deployment accessibility sign-off** the repo already tracks as Phase 3
([VUIT checklist](../../docs/runbooks/vuit-coordination-checklist.md)). It cannot be auto-run from this
repo (BookStack isn't running in CI), so it's a manual gate against the live instance.

- [ ] **Selectors bind.** Header is the black bar with a gold rule; links are Oak + underlined; primary
      buttons are Oak with white labels. If a surface didn't change, the selector drifted in this
      BookStack version — fix it in `ccc-custom-head.html`, don't paper over with the UI.
- [ ] **Contrast.** Run axe DevTools or Lighthouse on a logged-out page, a read page, and the editor.
      Zero contrast violations. Spot-check: body text, links, button labels, header.
- [ ] **Keyboard.** Tab through a page top to bottom: order is sensible, the focus ring is visible on
      **every** surface (white, cream, gold buttons, the black header), nothing is reachable that
      shouldn't be.
- [ ] **Focus not obscured.** The sticky header doesn't fully hide a focused element (WCAG 2.4.11).
- [ ] **Targets.** Icon buttons (sidebar, editor toolbar) are ≥ 24×24 px.
- [ ] **Reduced motion.** With OS "reduce motion" on, transitions are effectively instant.
- [ ] **Zoom.** At 200% browser zoom nothing is clipped or overlapping; the page reflows.
- [ ] **Color isn't the only cue.** Links are underlined; callout types are distinguishable without
      relying on hue alone.
- [ ] **Screen reader smoke test.** One full read flow with VoiceOver (macOS) or NVDA (Windows): the
      logo's accessible name reads sensibly, headings outline correctly, links aren't all "read more."

## Trademark / authorization

Vanderbilt marks are **for authorized use only**. This repo ships no official logo, Star V, seal, or
school lockup — only the text placeholder in `assets/`. Obtaining the authorized CCC school lockup
(clear-space-correct, ≥250 px) from Vanderbilt Brand Communications is a VUIT-coordination dependency,
tracked in the [VUIT checklist](../../docs/runbooks/vuit-coordination-checklist.md). Do not recreate,
trace, or approximate an official mark.

## Adopting a licensed brand font (later)

Headings and body currently use system stacks (no licensing or perf cost). To switch to a licensed
Vanderbilt typeface: self-host the woff2, add an `@font-face` (or `<link rel="preload">`) block above
the `<style>` in `ccc-custom-head.html`, and change the two `--ccc-font-*` variables. Keep
`font-display: swap` so text renders immediately on the system fallback.

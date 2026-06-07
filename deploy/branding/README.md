# CCC branding — apply & validate

The applied brand layer for the BookStack wiki. The design *intent* lives in
[docs/brand/ccc-brand-guidelines.md](../../docs/brand/ccc-brand-guidelines.md); this directory is what
actually ships.

```
deploy/branding/
  ccc-custom-head.html              # paste into Settings → Customization → Custom HTML Head Content
  assets/ccc-logo-reversed.svg      # CCC lockup, white text — for the dark header (upload this one)
  assets/ccc-logo.svg               # CCC lockup, black text — for light backgrounds / print
  assets/ccc-favicon.svg            # the gold V alone — favicon / app icon
  assets/eye.svg                    # login show-password icon  (Material Symbols, Apache-2.0)
  assets/eye-off.svg                # login hide-password icon  (Material Symbols, Apache-2.0)
  README.md                         # this file
```

The `eye*.svg` icons are the source of truth for the login show-password control. They're
**inlined** into `ccc-custom-head.html` (not `<link>`ed) to keep that block network-free for the
VPN-only deployment — same rule as the favicon. To restyle one, edit the asset and paste its `<path>`
into the matching `var` in the script. They're [Google Material Symbols](https://github.com/google/material-design-icons)
(`visibility` / `visibility_off`), Apache-2.0 — keep attribution if you swap them.

The logos are the **real CCC marks**, assembled from Vanderbilt's own vector art on the live college
site (the dimensional-metallic V + the "VANDERBILT" logotype, copied verbatim; college name set in a
serif). See [the guidelines §4](../../docs/brand/ccc-brand-guidelines.md#4-logos--lockups) for
provenance and usage rules.

## Why branding is part config, not all code

BookStack theming splits across three levers. Two are file-based and version-controlled here; one is a
DB-stored UI setting applied once per environment — the same pattern this repo already uses for the
public-read toggle and the default registration role (see
[docs/architecture.md](../../docs/architecture.md#bookstack-configuration-the-load-bearing-bits)).

| Lever | Mechanism | Where it's defined |
|---|---|---|
| App name | `APP_NAME` env var | `deploy/local/.env.example`, `compose.yaml`, `terraform/user-data.sh.tftpl` |
| Colors, header, links, a11y CSS, light/dark + login UX | Custom HTML Head Content (DB) | `ccc-custom-head.html` (paste once per env) |
| Logo / favicon / primary color | Settings → Customization (DB upload + picker) | applied once; asset in `assets/` |

We deliberately do **not** use BookStack's PHP theme system (`APP_THEME` + `themes/<name>/`): upstream
marks it "not stable, may change on any update," and it adds a mounted-volume moving part for no gain
over the supported custom-head path.

## Apply procedure

The brand is **applied automatically** by [`apply-brand.sh`](../local/apply-brand.sh) on every deploy
(and via `make apply-theme`). It is idempotent (writes a setting only when it differs) and the **repo
is the source of truth** — a brand edit made in the BookStack UI is reverted on the next deploy. From
this directory it sets:

| What | BookStack setting(s) | From |
|---|---|---|
| App name | `app-name` | the `APP_NAME` env (compose `.env`, default `CCC Wiki`) |
| Custom head (CSS/JS theme + UX features) | `app-custom-head` | [`ccc-custom-head.html`](./ccc-custom-head.html) |
| Primary color | `app-color` (+ light/dark tints) | CCC black `#1C1C1C` (BookStack paints the header with it; the head re-points `--color-primary` to Oak for buttons — issue #40) |
| Logo | `app-logo` | [`assets/ccc-logo-reversed.svg`](./assets/ccc-logo-reversed.svg), staged into the uploads volume |
| Favicon | `app-icon` (+ 180/128/64/32) | [`assets/ccc-favicon.svg`](./assets/ccc-favicon.svg) |

`ccc-custom-head.html` is **executable code** (it carries a `<script>` that runs as first-party JS on
every page, including login), not just CSS — review changes to it like code; a merge auto-ships it to
every user. See the deploy runbook's
[security notes](../../docs/runbooks/connor-server-deploy.md#security-notes).

**Manual fallback** (an environment without the deploy, e.g. the first AWS bring-up): do each in
Settings → Customization — paste `ccc-custom-head.html` into **Custom HTML Head Content**, set
**Application primary color** to `#1C1C1C` (black — so the header is correct on the `/settings/*`
pages that strip the custom head; the head re-paints buttons Oak elsewhere), and upload **Logo** =
[`assets/ccc-logo-reversed.svg`](./assets/ccc-logo-reversed.svg) (white text, for the dark header; use
[`assets/ccc-logo.svg`](./assets/ccc-logo.svg) on a light header) and **Favicon** =
[`assets/ccc-favicon.svg`](./assets/ccc-favicon.svg). If your build rejects SVG uploads (some lock down
SVG for security), export to a transparent PNG (~480 px logo / 32×32 favicon) and upload that. Then
hard-refresh (Cmd/Ctrl+Shift+R) and run the validation checklist below.

**Favicon note:** the logo and favicon are SVGs served as static files. The in-tab favicon works in
every modern browser (they fetch by URL and honor the real `image/svg+xml` type). But BookStack
hardcodes `type="image/png"` for the icon `<link>`s and the PWA manifest (and emits an
`apple-touch-icon`), so the **installed-PWA / iOS home-screen icon is best-effort** and may fall back
to a generic glyph. That's fine for this desktop/VPN wiki; if a crisp installed icon is ever needed,
stage a PNG export of `ccc-favicon.svg` for the `app-icon*` settings.

## Light/dark mode behavior

The `<script>` in `ccc-custom-head.html` owns light/dark on the client (BookStack's own preference
is server-side and can't see the user's OS). The model is small and has one source of truth:

| Question | Answer |
|---|---|
| Default for a new visitor? | The **OS** setting (`prefers-color-scheme`), and it tracks live OS changes. |
| What if the user picks one? | Their choice wins and is remembered **per device**, written to **both** a host-scoped `ccc-color-scheme` cookie and `localStorage`. Read order: cookie → localStorage → OS. |
| How many toggles? | **One.** BookStack renders a copy in the user dropdown *and* on the homepage; we hide the homepage copy and keep the dropdown one. The login page gets its own (no dropdown there). |
| Where is it applied? | In `<head>` before first paint, so there's no light-then-dark flash. |
| Why a cookie too? | Cookies are scoped by **host, not port**, so the choice bridges to the cross-port contact page (`:8081`) — `localStorage` can't (it's per-origin). The cookie is `path=/`, long-lived, `SameSite=Lax`, `Secure` under HTTPS, and **not** `HttpOnly` (client JS owns it; the value `dark`/`light` is non-sensitive). The contact service reads it server-side (issue #39). |

Why client-side and not BookStack's setting: BookStack only offers a fixed instance default (light *or*
dark) plus a per-user server toggle — neither follows the OS. Doing it in the head is the only lever
that gives "default to the OS" without forking the image. Trade-off: the preference is per-device, not
synced to the user's account. For an internal wiki that's the expected behavior and avoids any
server/CSRF coupling. This is **skipped on Settings → Customization** (BookStack omits custom head
there), so it never fights the settings editor.

**The color side is token-driven, so a change can't break a mode.** Colors live in three tiers
(palette -> mode-aware semantic tokens -> BookStack-variable mapping); surface rules reference a token
and carry **no literal color and no mode guard**. Light values are in `:root`; the `html.dark-mode`
block re-points *only* the tokens that differ. So:

- **To recolor a surface:** edit its semantic token (e.g. `--ccc-chrome-surface`, `--ccc-link`) in one
  place. Every surface using it updates; no other rule changes.
- **To add a mode** (e.g. high-contrast): add one block that re-points the tokens. No surface rule moves.
- Dark mode **defers to BookStack's own (AA) dark theme** for surfaces (chrome goes transparent over
  its `#111` page) and only re-points accent **text** to a lighter gold; `--color-primary` stays Oak
  because BookStack uses it as a white-label button background.

The full tier model and the dark-mode contrast pairings are in
[the brand guidelines §3 + §7](../../docs/brand/ccc-brand-guidelines.md#dark-mode).

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
- [ ] **Light/dark.** With OS set to dark, a fresh browser (no stored choice) loads dark with no
      light-then-dark flash; set OS to light and it follows. Toggle once — the choice sticks across
      reloads and the toggle label matches the screen (no "Dark Mode" label on an already-dark page).
- [ ] **Dark-mode contrast + no clash.** In dark mode the sidebars/panels are **dark** (not cream),
      links/accents are the lighter gold, primary buttons stay Oak with white labels, and the header
      stays black. Run axe/Lighthouse on a read page in dark mode: zero contrast violations. Spot-check
      a sidebar (no light-on-cream), a content link, and a callout.
- [ ] **One toggle.** The homepage no longer shows its own light/dark control; only the user-dropdown
      one remains. The Settings → Customization page is unaffected.
- [ ] **Cross-port theme bridge (issue #39).** Pick Dark on the wiki, then open the contact page
      (`:8081/contact`, or `/contact*` in prod) — it loads dark with no flash. Toggle it back to Light
      on the contact page, return to the wiki, reload — the wiki is Light. A fresh browser (no choice)
      follows the OS on both.
- [ ] **Login screen.** Logged out, the login card shows a working theme toggle (top-right) and a
      show/hide control on the password field. Show/hide is keyboard-operable, has a visible focus
      ring, doesn't block paste or autofill, and its target is >= 24x24 px.

## Trademark / usage

The marks in `assets/` are CCC's own, assembled from Vanderbilt's published web vectors — appropriate
**internal** use of the college's brand on its own wiki. Use them as-is: don't recolor or redraw the V,
and keep clear space around the lockup (roughly the width of the V's interior negative space). For
**print, large-format, or anything externally distributed**, get the master art file (with metallic-ink
and clear-space specs) from Vanderbilt Brand Communications — tracked in the
[VUIT checklist](../../docs/runbooks/vuit-coordination-checklist.md).

## Adopting a licensed brand font (later)

Headings and body currently use system stacks (no licensing or perf cost). To switch to a licensed
Vanderbilt typeface: self-host the woff2, add an `@font-face` (or `<link rel="preload">`) block above
the `<style>` in `ccc-custom-head.html`, and change the two `--ccc-font-*` variables. Keep
`font-display: swap` so text renders immediately on the system fallback.

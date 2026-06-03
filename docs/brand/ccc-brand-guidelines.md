# CCC Brand & Design Language

The brand reference for the **College of Connected Computing (CCC)** internal documentation wiki. It
distills Vanderbilt's institutional brand (color, logos, imagery) and the CCC marcomms voice into the
specific, contrast-checked decisions used to theme this BookStack instance.

This is the *spec*. The applied artifact is [`deploy/branding/`](../../deploy/branding/) — the custom
stylesheet, logo/favicon assets, and the apply/validation runbook. When the two disagree, this file is
the source of truth for *intent*; the CSS is the source of truth for *what ships*.

> **Brand-asset note (read first).** This repo ships the **official CCC lockup**, assembled from
> Vanderbilt's own vector art taken verbatim from the live college site
> ([computing.vanderbilt.edu](https://computing.vanderbilt.edu/)): the dimensional-metallic V and the
> "VANDERBILT" logotype, with the college name set in a serif. That is appropriate **internal** use of
> CCC's own mark. Use the marks as-is (don't recolor or redraw the V). For print, large-format, or
> externally distributed use, get the master art file from Vanderbilt Brand Communications (a VUIT item).
> See [§4 Logos & lockups](#4-logos--lockups) and the [VUIT checklist](../runbooks/vuit-coordination-checklist.md).

---

## 1. Who CCC is (the voice in one breath)

The College of Connected Computing is a new Vanderbilt college focused on AI, computing, and
**"Computing for All."** The brand is **collective** (we/our, not I), **people-first** (faculty,
students, and partners are the heroes; the college amplifies), and **mission-anchored** (the work
traces back to human impact). Pride without bragging; warmth without slang; substance over hype.

This matters for a docs site, not just social posts: microcopy, empty states, error messages, and
section intros should read in this voice. See [§6 Voice & tone](#6-voice--tone-for-ui-copy).

---

## 2. Color palette

Sourced from [brand.vanderbilt.edu/color](https://brand.vanderbilt.edu/color/). **Core colors carry
~90% of any surface; the saturated accents are the remaining ~10%.** Gold is an *accent and a
surface*, never body-text-on-white (see [§3](#3-accessible-color-pairings-the-load-bearing-part)).

### Core

| Name | HEX | RGB | CMYK | Pantone | Role |
|---|---|---|---|---|---|
| Black | `#1C1C1C` | 10/10/10 | 0/0/0/100 | Black C | Primary ink, headers, the workhorse text color |
| White | `#FFFFFF` | 255/255/255 | 0/0/0/0 | — | Primary surface (page/content background) |
| Flat Gold | `#CFAE70` | 207/174/112 | 20/29/64/0 | 4024 C | Accent surface (badges, rules) — **black text only** |
| Metallic Gold | `#FEEEB6`→`#B49248` | 254/238/182 → 180/146/72 | — | 871 C | Gradient accent; print uses metallic ink |

### Secondary — neutrals

| Name | HEX | RGB | CMYK | Role |
|---|---|---|---|---|
| Dark Gray | `#777777` | 119/119/119 | 0/0/0/53 | Secondary/large text only (borderline for body) |
| Light Gray | `#E4E4E4` | 228/228/228 | 0/0/0/11 | Borders, dividers, disabled fills |
| Sand | `#E0D5C0` | 224/213/192 | 12/13/23/0 | Warm panel fill |
| Cream | `#F5F3EF` | 245/243/239 | 3/2/4/0 | App chrome / sidebar background |

### Secondary — saturated accents

| Name | HEX | RGB | CMYK | Role |
|---|---|---|---|---|
| Oak | `#946E24` | 148/110/36 | 36/51/100/18 | **Accessible gold for links & primary buttons** (passes AA on white) |
| Highlight | `#ECB748` | 236/183/72 | 7/29/84/0 | Active-state accent, focus ring core, highlights — **black text only** |
| Sky | `#B3C9CD` | 179/203/205 | 30/12/16/0 | Info/notice tint (decorative) |
| Sage | `#8BA18E` | 139/161/142 | 49/26/47/0 | Success/eco tint (decorative) |

---

## 3. Accessible color pairings (the load-bearing part)

Gold-forward brands fail accessibility when gold becomes text on white. The resolution, which is also
what Vanderbilt's own brand book endorses: **black and white do the work; gold is an accent or a
surface with dark text.** Target is **WCAG 2.2 AA** (Vanderbilt's standard, and an ADA Title II
obligation already in force for 2026). Ratios below are computed; **AA needs 4.5:1 for normal text,
3:1 for large text and non-text/UI.**

| Foreground | Background | Ratio | Verdict |
|---|---|---|---|
| Black `#1C1C1C` | White `#FFFFFF` | **16.9 : 1** | ✅ AAA — body text |
| Black `#1C1C1C` | Cream `#F5F3EF` | **15.8 : 1** | ✅ AAA — text on chrome |
| Black `#1C1C1C` | Flat Gold `#CFAE70` | **8.1 : 1** | ✅ AAA — text on gold badges/buttons |
| Black `#1C1C1C` | Highlight `#ECB748` | **9.3 : 1** | ✅ AAA — text on highlight |
| White `#FFFFFF` | Oak `#946E24` | **4.65 : 1** | ✅ AA — button label on gold |
| Oak `#946E24` | White `#FFFFFF` | **4.65 : 1** | ✅ AA — link text on white content |
| `.text-muted` `#575757` | White | ~7 : 1 | ✅ AA — secondary text |

**Hard rules that fall out of this:**

- **Never white text on Flat Gold / Highlight / Metallic** (2.1 : 1 and 1.8 : 1 — fail). Use **black**.
- **Never gold text on white *except* Oak `#946E24`.** Brighter golds (Flat, Highlight) fail as text.
- **Oak links live on white content** (4.65 : 1). On Cream they drop to ~4.2 : 1 — borderline, so keep
  colored link text on white panels and use black for links on cream chrome.
- **Dark Gray `#777777` on white is ~4.47 : 1** — treat as large/secondary text, not primary body.
- **Never rely on color alone.** Links are underlined; states get an icon or label, not just a hue
  (WCAG 1.4.1).

---

## 4. Logos & lockups

From [brand.vanderbilt.edu/logos-and-lockups](https://brand.vanderbilt.edu/logos-and-lockups/).

**Variants:** primary lockup (logotype + dimensional metallic V, horizontal or centered), the
logotype alone (for less formal use), the V icon (space-constrained / department use), the official
seal (*official use only, requires approval*), and **school/college lockups** (the relevant family
for CCC — released in phases by Brand Communications).

**Mechanical rules:**

- **Clear space** = the height of the negative space between the two sides of the V icon, on all sides.
- **Minimum size (web):** primary horizontal lockup **250 px**; logotype only **150 px**; V icon **75 px**.
  Below the lockup minimum, fall back to the logotype only.
- **Formats:** PNG (RGB, screen) and EPS (CMYK, print). Metallic ink (PMS 871) / foil files for
  high-end print come from Brand Communications.

**What this repo ships:** the CCC lockup in two variants, built from the genuine Vanderbilt vectors on
the live site — [`ccc-logo-reversed.svg`](../../deploy/branding/assets/ccc-logo-reversed.svg) (white
text, for the dark header) and [`ccc-logo.svg`](../../deploy/branding/assets/ccc-logo.svg) (black text,
for light backgrounds), plus [`ccc-favicon.svg`](../../deploy/branding/assets/ccc-favicon.svg) (the V
alone). The V and the "VANDERBILT" logotype are official vectors copied verbatim; the college name is
set in a serif (the licensed brand face isn't embedded, so it renders in the system serif). Use the
marks as-is — don't recolor or redraw the V. A master/print art file should come from Brand
Communications.

---

## 5. Imagery

From Vanderbilt's [imagery guidance](https://brand.vanderbilt.edu/imagery/).

- **Style:** immersive and real — "a distinct moment conveying a real experience," with rich color,
  bold composition, and unique vantage points. A high degree of **collaboration and community** should
  be visible.
- **People-first:** show students, faculty, and alumni doing the work — overcoming obstacles, carving
  pathways for others. This mirrors the voice: named humans are the heroes.
- **Campus imagery feels timeless;** topical images may be licensed stock (Adobe, Getty, AP, Reuters)
  to tie to current events.
- **Unifying many portraits/headshots** (e.g., a contributors grid): treat them **black-and-white** to
  reconcile mismatched lighting and backgrounds — this also pairs cleanly with the gold/black palette.
- **Pairing with the palette:** let photographs carry the color; keep gold as a thin accent (rule,
  caption bar, badge) over or beside imagery, never a heavy gold wash on top of a photo.

**Permissions taxonomy (every image gets a status before it ships):**

- `cleared` — VU MarComm library, CCC-owned, or explicitly licensed. Note the source.
- `needs_check` — third-party, or any photo of an **identifiable student** (FERPA territory — starts
  here at minimum until a release is confirmed).
- `blocked` — known to require rights we don't have. Replace; don't publish.

All `alt` text describes the image's *content or function*, not "image of…" (WCAG 1.1.1).

---

## 6. Voice & tone (for UI copy)

Distilled from the CCC marcomms brand-voice profile (the `marcomms_brand_voice` skill,
`voices/default.md`). Applies to every string we author in the wiki: section intros, empty states,
error/help text, button labels, notices.

**Do:**

- **Collective and direct.** "We" and "our"; address the reader as "you." Contractions are fine
  ("we're," "can't").
- **Plain and substantive.** Say what a thing does. "Computing for All" is the one fixed mission phrase
  (capitalized exactly); use at most once where it genuinely fits.
- **Specific gratitude / attribution.** Name people and partners, not "everyone."

**Don't:**

- **No superlatives** ("best," "leading," "world-class," "premier").
- **No hard urgency** ("don't miss out," "act now") or corporate-speak ("leverage," "synergy,"
  "disrupt," "reimagine").
- **No slang/hype** ("lit," "fire," "crushing it"), no decorative emoji spray.
- **No em-dashes (`—`)** in UI copy — they read as an AI tell. Use a comma, colon, parentheses, or a
  connecting word. (Hyphens in compounds like "real-world" and en-dashes in ranges like "17–19" are
  fine.)

**Jargon test:** would a thoughtful Vanderbilt undergrad in any major understand it on first read? If
not, add a plain-English clause.

---

## 7. How the brand maps to the wiki UI

The translation of the above into BookStack surfaces. Implemented in
[`deploy/branding/ccc-custom-head.html`](../../deploy/branding/ccc-custom-head.html); each token below
maps to a BookStack CSS variable or a stable selector.

| Surface | Decision | Token / mechanism |
|---|---|---|
| **Primary color** (buttons, links) | Oak `#946E24` — the only gold that passes AA as text and as a white-label button | Settings → primary color **and** `--color-primary` |
| **Links in content** | Oak `#946E24`, **always underlined** | `--color-link` + `.page-content a` underline |
| **Header / top bar** | Vanderbilt-signature **black `#1C1C1C` bar, white text, thin gold accent rule** | `header.header` override |
| **App chrome / sidebars** | Cream `#F5F3EF` with black text | background overrides |
| **Body text** | Black `#1C1C1C` on white content | inherited |
| **Accent / active states** | Highlight `#ECB748` or Flat Gold `#CFAE70` with **black** text | badges, active nav, callouts |
| **Focus indicator** | Layered ring (gold core + ink halo) visible on white, cream, gold, and the black header | `:focus-visible` |
| **Typography** | System stack (no licensed-font dependency); serif headings for academic tone | `--ccc-font-*` |
| **Touch targets** | ≥ 24×24 px (WCAG 2.5.8) | min-size on icon controls |
| **Motion** | Honors `prefers-reduced-motion` | media query |

**Why system fonts:** Vanderbilt's brand typefaces are licensed; shipping them is a perf and licensing
trap (per the frontend playbook, the system stack is "often the right answer"). Headings use a serif
stack to echo the academic wordmark; body uses a sans-serif system stack. Swapping in a licensed brand
font later is a one-variable change, documented in the branding README.

---

## 8. Sources

- Vanderbilt color — <https://brand.vanderbilt.edu/color/>
- Vanderbilt logos & lockups — <https://brand.vanderbilt.edu/logos-and-lockups/>
- Vanderbilt imagery — <https://brand.vanderbilt.edu/imagery/>
- CCC marcomms voice — `ccc-marcomms-agent-v3/skills/marcomms_brand_voice/voices/default.md`
- Accessibility target — WCAG 2.2 AA (see [`deploy/branding/README.md`](../../deploy/branding/README.md))
</content>
</invoke>

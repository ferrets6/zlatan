# zlatan — brand and interface

Plain HTML, one stylesheet, five SVGs, one self-hosted font. No build step.
Open `index.html`: every screen, light and dark, phone and desktop, in iframes
at real device widths.

```
index.html   gallery
zlatan.css   tokens + components   → internal/web/static/style.css
assets/      space-grotesk.woff2 (latin, variable, 22 kB) + OFL.txt
brand/       mark · mark-google · wordmark · app-icon · favicon
screens/     01 entry · 02 both tracks · 03 + 03b Takeout guide
             04 upload · 05 waiting · 06 done · 07 stopped
```

## Mark

An arc you leave, a crossing, a flat bar you land on. The Z is the shape of the
job. Same compass as Nuno: 24 grid, 2 units of stroke, round caps, no fill, one
path. The arc carries Google's four colours and the colour stops at the
crossing.

| File | Use |
|---|---|
| `mark.svg` | `currentColor`; everything below 32 px |
| `mark-google.svg` | 32 px and up |
| `wordmark.svg` | outlined, for anywhere without the font |
| `app-icon.svg` | 512 px tile, dark ground, fixed colours |
| `favicon.svg` | monochrome, states its own colour, follows the theme |

Google's yellow `#FBBC05` is 1.6 : 1 on a light ground, so there it is
`#E3A008`. Both SVGs switch it themselves.

Quoting another company's colours can read as affiliation. Inside a household
that is a non-issue; in front of strangers, lead with `mark.svg`.

Drawn and rejected:

| | Why not |
|---|---|
| Ring with its slice moved out ahead | Best family argument, but not a Z |
| Arrow leaving through a gap in a ring | It is the stock "sign out" glyph |
| Ring cut twice, crossing threaded through | Reads **℮** before **z**, and a circle with a diagonal is the "forbidden" sign |
| Z with both arms bowed | Nothing in it is horizontal, so it softens at 16 px |

## Palette

| Role | Light | Dark | On the background |
|---|---|---|---|
| Foreground | `#1c1c1a` | `#e6e6e3` | 16.5 : 1 / 14.5 : 1 |
| Accent | `#3f7d4f` | `#6fae7d` | 4.8 : 1 / 6.9 : 1 |
| Background | `#fbfbfa` | `#15161a` | — |
| Attention *(added)* | `#9c5841` | `#d1886e` | 5.2 : 1 / 6.4 : 1 |

`--attention` is the accent's own OKLCH lightness and chroma (L 0.535,
C 0.097) at hue 40, so it carries the same weight as the green. It means one
thing: this needs you, or this stopped. No red anywhere.

Waiting has no colour. Half of this service is waiting and nothing is wrong
during it.

Surfaces, hairlines and muted text are `color-mix` of the four above. Muted is
the foreground at 65 %: 5.2 : 1 light, 6.7 : 1 dark.

Type is Space Grotesk throughout. One file, one rendering on every machine.

## The Takeout step

Google removed photo-library read access in March 2025, so one step stays
manual. What the screens do about it:

- **Named on the entry screen**, before anyone commits. A surprise mid-flow
  reads as a failure.
- **Your part / our part on both tracks**, the human side costed in minutes.
  Photos then looks like a job with a bigger share for you, not a broken one.
- **One sentence of why, with the date.** The longer version sits in a
  `<details>`.
- **One instruction at a time**, with Google's own words quoted in a chip.
- **The current step is server state.** Reload, another device, next morning:
  same step.
- **No fork.** Route A is the flow. Route B is an escape phrased as a condition
  ("if it will not fit"), not a choice to weigh up.
- **No progress on the waiting screen**, because there is none to measure. Last
  check, how often, how long it usually takes, and an email when it is done.

## Rules the copy follows

- A bar only with a denominator. Otherwise a moving line and a count.
- An estimate only when measured, and then as a range.
- "You can close this tab" everywhere except the upload, which runs in the
  browser. That screen says so.
- No "ERROR". What happened, what it means for the data, what to do, then the
  detail folded away for whoever runs the server.
- One track failing never makes the page look broken.

## Wiring

- Every action is a form POST or a link. `hx-*` upgrades the same endpoints.
  With JS off the pages work and a reload is the truth.
- `.state` blocks swap whole (`hx-swap="outerHTML"`), so a reading is never
  half-updated. They carry `aria-live="polite"`.
- Two scripts, both optional: copy-to-clipboard, and the resumable upload.
- Two animations, both off under `prefers-reduced-motion`.
- Touch targets 44 px. Focus is a 2 px accent outline.
- Pill classes map onto `core.DriveState` / `core.PhotosState`:
  `pill--idle · running · waiting · you · done · stopped`.

## Where the app differs (2026-09-25)

The wizard renders these screens; `wizard.html` picks one from the two track
states. Four deviations, each because the design shows a fixture where the app
has a fact:

- `[hidden] { display: none !important; }` was added: the upload script toggles
  `hidden`, which the drop component's `display: flex` would override.
- `.totals` uses `auto-fit`. Drive reports two numbers and Photos one, where
  the design draws three tiles.
- **No invented denominators.** The design shows "of 32,900 files" and an ETA;
  the runner computes neither, so the app shows the counts it has.
- **The done screen claims no check.** Independent verification is not
  implemented, so the app says the copy and the import finished without errors.
  `TestDoneScreenDoesNotClaimAnUnimplementedCheck` fails if the design's
  sentence creeps back in first.

Also: the guide always shows step 3 as current, because the database stores one
`takeout_guide` state rather than which of the five steps the person is on; and
the "Change this" swaps and the `/photos/check` button are left out, because
those routes do not exist.

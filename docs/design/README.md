# zlatan — brand and interface

Everything here is plain HTML, one CSS file and five SVGs. No build step, no
framework, no icon font, one self-hosted typeface. It is meant to be moved into
`internal/web/` as it is.

```
docs/design/
├── index.html          the gallery: every screen, light and dark, phone and desktop
├── zlatan.css          the whole visual system — tokens and components
├── assets/
│   ├── space-grotesk.woff2   latin subset, variable 300–700, 22 kB
│   └── OFL.txt               SIL Open Font License 1.1
├── brand/
│   ├── mark.svg        monochrome, currentColor — the file the UI uses
│   ├── mark-google.svg the arc in Google's colours — 32 px and above
│   ├── wordmark.svg    mark + "zlatan", outlined, needs no font
│   ├── app-icon.svg    512 px rounded tile
│   └── favicon.svg     monochrome, states its own colour, swaps with the theme
└── screens/            01 entry · 02 both tracks · 03 + 03b the Takeout guide
                        04 route B · 05 waiting · 06 done · 07 stopped
```

Open `index.html` in a browser. Every frame in it is the real screen file in an
iframe at a real device width, not a picture of one.

---

## The mark

**An arc you leave, a crossing, a bar you land on.**

The Z is taken literally rather than decoratively. It is the only letter that is
already a journey: a run along the top, a crossing, a run along the bottom. So
the mark is that journey, and the two runs are made of different things.

- The **top arm is an arc** — the orbit you are leaving.
- The **bottom arm is a flat bar** — the ground you land on.
- The **diagonal between them** is the part the server does for you.

It is drawn with Nuno's compass: the same 24 grid, two units of stroke, round
caps, no fill, one continuous path. Nuno's ring is *almost complete*; zlatan's
Z is *on its way*. They are the same hand.

Three other directions were drawn and rejected, and they are worth recording
because each failed for a different reason:

| Direction | Why not |
|---|---|
| A ring with its missing slice moved out ahead of it | The strongest family argument of the lot — it needs Nuno to exist — but it is not a Z. |
| An arrow leaving through the gap in a ring | It is the stock "sign out" glyph. A brand should not borrow a system icon. |
| Nuno's ring cut at two points, with the crossing threaded through it | Everyone reads **℮** before they read **z**, and a circle with a diagonal is also the "forbidden" sign. |

Among the Z drawings, the alternatives were a Z with both arms bowed (warmer,
more written, but nothing in it is horizontal so it softens at 16 px) and a Z
with a bowed spine (it drifts towards a 7). The flat bottom bar is what makes
this one snap to the pixel grid at favicon size.

### Google's colours on the arc

The arc carries Google's four colours, and **the colour stops at the crossing**.
Above the line the mark is Google's; from the crossing down it is yours. That is
the whole service in one drawing, and it costs nothing to explain.

Rules for it:

- **32 px and above**: `mark-google.svg`. Below that the four bands are three
  pixels of mud — use `mark.svg`.
- The published Google yellow `#FBBC05` is invisible as a 2 px stroke on a light
  ground (1.6 : 1). On light it is deepened to `#E3A008`; on dark it goes back to
  the published value. Both files do this by themselves with a media query.
- The colours are **quoted, not adopted**. They appear on eleven units of arc and
  nowhere else — not on a button, not on a chart, not in the palette.

One thing to decide with open eyes: quoting another company's brand colours can
read as affiliation. Inside a household this is a non-issue; if zlatan is ever
put in front of strangers, `mark.svg` is the file to lead with.

---

## The palette

Nuno's six values, unchanged, plus one addition.

| Role | Light | Dark | Source | Contrast on the background |
|---|---|---|---|---|
| Foreground | `#1c1c1a` | `#e6e6e3` | Nuno | 16.5 : 1 / 14.5 : 1 |
| Accent | `#3f7d4f` | `#6fae7d` | Nuno | 4.8 : 1 / 6.9 : 1 |
| Background | `#fbfbfa` | `#15161a` | Nuno | — |
| **Attention** | `#9c5841` | `#d1886e` | **added** | 5.2 : 1 / 6.4 : 1 |

**Why the addition, and why only one.** Four states needed an answer: success,
waiting, needs-you and stopped.

- *Success* is the accent. Nothing else was required.
- *Waiting* gets **no colour at all** — deliberately. Half of this service is
  waiting, and colouring it would mean the screen is shouting during the hours
  when nothing is wrong. Waiting is a grey dot with a slow pulse and a
  timestamp.
- *Needs you* and *stopped* share one colour, because in this interface they are
  the same message: something is on your side of the line.

`--attention` is the accent's own lightness and chroma in OKLCH (L 0.535,
C 0.097) rotated to hue 40, then converted back to sRGB. That is why it sits at
the same visual weight as the green instead of shouting over it, and why it
reads as clay rather than as an alarm. There is no red anywhere in this design.

Everything else — surfaces, hairlines, muted text, the washes behind the accent
and attention blocks — is the foreground or the accent mixed into the background
at a stated percentage, in the CSS, with `color-mix`. Muted text is the
foreground at 65 %, which lands at 5.2 : 1 on light and 6.7 : 1 on dark.

**Type.** Space Grotesk for everything, one variable file, latin subset, 22 kB,
self-hosted with a system fallback stack. One face rather than a display/body
pair: it is fewer bytes on a server where watts are counted, and it renders the
same on every machine in the house, which a system stack never does.

---

## The hard problem

> Google removed the read access that let any app read a photo library. The
> Photos route can never be fully automatic. Make that unavoidable human step
> feel calm and guided.

Seven decisions, in the order they matter.

**1. Name it before anyone commits.** The manual step is on the entry screen, in
the Photos card, before the person has clicked anything: *"There is one step only
you can do."* A surprise halfway through a flow reads as a failure. The same
sentence at the start reads as a plan.

**2. Frame it as a division of labour, not as a limitation.** Every track carries
the same component — **Your part / Our part** — with the person's side costed in
minutes and the server's side listed as four things it will do alone. The Photos
track does not look broken; it looks like a job with a bigger share for you. The
component is on the Drive track too, where the human part is one approval
screen, so the comparison is made by the interface rather than by the copy.

**3. Say why, once, in one sentence, with the date.** *"In March 2025 Google
closed the door that let apps read photo libraries — not just this one, every
app."* Underneath, a `<details>` for the person who wants the longer version. No
blame, no engineering vocabulary, no apology.

**4. One instruction on screen at a time.** The guide is a rail of five steps
with only the current one expanded. Every value the person has to choose is
quoted exactly as Google writes it — `Add to Drive`, `Export once`, `.zip`,
`50 GB` — in a chip, so it can be matched by eye rather than parsed from prose.

**5. The step is server state, so the page survives anything.** Which step you
are on lives in the database, not in the URL and not in a cookie. Close the tab,
come back on a phone the next morning, reload mid-way: you land on the step you
left. Every "I've done this" is a plain form POST that works without JavaScript;
HTMX only makes it swap in place instead of reloading.

**6. Do not offer a fork; offer a default and an exit.** A nervous person should
not be asked to choose between two migration routes. Route A is the flow. Route
B is a quiet, always-visible escape — *"Your Google storage might be too small"*
— phrased as a condition they can recognise, not as an option they must
evaluate. It reappears on the waiting screen, where the reason to switch usually
turns up.

**7. When they are done, prove you are still there.** The waiting screen has no
progress bar, because there is no progress to measure. It has a pulsing dot, the
time of the last check, how often we check, an honest horizon ("a few hours;
a very large library can take Google a day, and it often arrives in parts"), the
list of things that will happen without them, and an email promise. The email is
what actually lets someone walk away from the tab.

### The honesty rules this design holds itself to

- **No progress bar without a denominator.** A determinate bar appears only once
  the scan knows the totals. Before that it is a moving line labelled with what
  is actually happening, and a count that goes up.
- **No estimate that is not computed.** "About 3–4 hours left" is a range from a
  measured rate; the range is the honest part.
- **No "you can close this tab" where it is not true.** It is true everywhere
  except the upload in route B, which lives in the browser. That screen says so,
  and promises the right thing instead: what survives a closed tab is the
  *progress*, not the transfer.
- **No "ERROR".** The stopped screen says what happened, what it means for the
  person's data, and what to do — in that order. The technical detail is folded
  away and addressed to whoever runs the server, not to the person reading.
- **One failure does not make the page look broken.** The tracks are independent
  in the data model, so they are independent on screen: the stopped screen shows
  the other track carrying on, and says so.

---

## Implementation notes

- **Server-rendered first.** Every action is a form POST or a link. The `hx-*`
  attributes in the markup upgrade the same endpoints to swap a fragment in
  place. With JavaScript off the pages work, the state is whatever the server
  last rendered, and a reload is always the truth.
- **Live regions** are swapped whole (`hx-swap="outerHTML"` on the `.state`
  block) so a reading is never half-updated. They carry `aria-live="polite"`.
- **Two scripts only**, both progressive enhancement: the copy-to-clipboard
  button on the share step, and the resumable upload.
- **Motion** is two slow animations — a pulsing dot and the indeterminate scan
  line — and both stop under `prefers-reduced-motion`.
- **Touch targets** are 44 px minimum. Focus is a 2 px accent outline with an
  offset, on everything.
- **Moving it into the app:** copy `zlatan.css` and `assets/` into
  `internal/web/static/`, fix the `@font-face` URL to `/static/…`, and cut the
  masthead and the two track panels out of the screens into
  `templates/partials/`. The class names match the states already in
  `internal/core`: `pill--idle / running / waiting / you / done / stopped` maps
  onto `DriveState` and `PhotosState` without a translation table.

### One thing to fix in the current code

`internal/web/static/wizard.js` tells the person *"you can close the page, it
resumes from here"* during a Takeout upload. The upload runs in the tab, so
closing it stops the transfer — what resumes is the progress, when they come
back. Screen 04 has the wording this design would use instead.

---

## Ported into the app (2026-09-25)

The wizard now renders these screens: `zlatan.css` is the app's stylesheet,
the brand SVGs and the font are served from `/static/`, and
`internal/web/templates/wizard.html` picks one screen from the two track
states. The port is faithful, with three deliberate deviations, each because
the design shows a fixture where the app has a fact:

- **`[hidden]` and `.totals`.** The upload script toggles the `hidden`
  attribute, which the drop component's own `display: flex` would override, so
  `[hidden] { display: none !important; }` was added. `.totals` uses
  `auto-fit` because the app has two numbers for Drive and one for Photos,
  where the design draws three tiles.
- **No invented denominators.** The design's waiting screen and the tracks
  screen show a total ("of 32,900 files") and an ETA; the runner computes
  neither, so the app shows only the counts it actually has. This is the
  design's own rule — no progress bar without a denominator — applied to the
  data the backend really produces.
- **The done screen does not claim a check.** The design's `06-done` says
  "We compared 500 files picked at random … byte for byte". Independent
  verification is not implemented yet, so the app states what is true — the
  copy and the import finished without errors — and will adopt the design's
  sentence when the check exists. A test
  (`TestDoneScreenDoesNotClaimAnUnimplementedCheck`) fails if that claim
  creeps back in before the code backs it.

Two smaller notes: the guide always shows step 3 as current, because the
database stores one `takeout_guide` state and not which of the five steps the
person is on; and the design's HTMX "Change this" swaps and the `/photos/check`
button are omitted, because those endpoints do not exist and the port added no
routes.

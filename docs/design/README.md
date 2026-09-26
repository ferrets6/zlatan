# zlatan: brand and interface

Open `index.html`. Every frame in it is rendered by the template the service actually runs, through `make screens`, so the gallery cannot go stale.

```
index.html   the gallery
screens/     generated, do not edit, run `make screens`
```

The assets live with the code they serve, in `internal/web/static/`: `style.css`, `space-grotesk.woff2` (latin, variable, 22 kB, SIL OFL), and the five SVGs.

## Mark

An arc you leave, a crossing, a flat bar you land on. Same compass as Nuno: 24 grid, 2 units of stroke, round caps, no fill, one path. The arc carries Google's four colours and the colour stops at the crossing.

| File | Use |
|---|---|
| `mark.svg` | `currentColor`, everything below 32 px |
| `mark-google.svg` | 32 px and up |
| `wordmark.svg` | outlined, for anywhere without the font |
| `app-icon.svg` | 512 px tile, dark ground, fixed colours |
| `favicon.svg` | monochrome, follows the theme |

Google's yellow `#FBBC05` is 1.6 : 1 on a light ground, so there it is `#E3A008`. Both SVGs switch it themselves.

Drawn and rejected: a ring with its slice moved out ahead (best family argument, but not a Z); an arrow leaving through a gap in a ring (the stock "sign out" glyph); a ring cut twice with the crossing threaded through (reads **℮** before **z**, and a circle with a diagonal is the "forbidden" sign); a Z with both arms bowed (nothing horizontal, so it softens at 16 px).

## Palette

| Role | Light | Dark | On the background |
|---|---|---|---|
| Foreground | `#1c1c1a` | `#e6e6e3` | 16.5 : 1 / 14.5 : 1 |
| Accent | `#3f7d4f` | `#6fae7d` | 4.8 : 1 / 6.9 : 1 |
| Background | `#fbfbfa` | `#15161a` | |
| Attention *(added)* | `#9c5841` | `#d1886e` | 5.2 : 1 / 6.4 : 1 |

`--attention` is the accent's own OKLCH lightness and chroma (L 0.535, C 0.097) at hue 40, so it carries the same weight as the green. It means one thing: this needs you, or this stopped. No red anywhere.

Waiting has no colour. Half of this service is waiting and nothing is wrong during it.

Everything else is `color-mix` of those four. Muted text is the foreground at 65 %: 5.2 : 1 light, 6.7 : 1 dark. Type is Space Grotesk throughout.

## What the screens say, and what they do not

A line earns its place by being something the person cannot work out from the screen. Everything else was cut, including every description of what a migration is and every reassurance repeated twice.

- **The destination is named**, from `ZLATAN_NEXTCLOUD_PUBLIC_URL` and `ZLATAN_IMMICH_PUBLIC_URL`, never hardcoded: `Google Drive → nextcloud.example.org`.
- **The Photos route is one numbered procedure**, in the order it happens: connect Immich (the import runs as the person, so it needs their own key), connect Google (so the export can be collected from their Drive), then ask Google for the copy. Only the current step is open; the Takeout instructions are the body of step three, with Google's own words quoted in a chip. There is no separate guide screen and no paragraph explaining why the route exists: the procedure is the explanation.
- **A pill carries the state, numbers carry the detail.** There is no second sentence restating the pill.
- **No bar without a denominator.** The runner does not know the totals, so there is no bar and no estimate.
- **No claim the code does not back.** The waiting screen states the poll interval from configuration; it promises no email, because nothing sends one.
- **No "ERROR".** What it means for the data, then the detail folded away for whoever runs the server.
- **"Open Immich" prefers the app.** The Android app registers the `immich://` scheme, but verified app links exist only for `my.immich.app`, which needs the person to have saved their own server there first, so a self-hosted address cannot open the app on its own. The link carries the web address and a script tries the scheme first on a touch device, falling back after a second. With no script it is the web address, which is right on a desktop.

## Languages

English and Italian, in `internal/i18n`. The order is: an explicit choice in a cookie, then the browser's `Accept-Language`, then English. Geolocation is not used: the browser already says what its owner reads, and on a home network every request comes from the same address.

The switcher is two links in the masthead. `?lang=it` sets the cookie and redirects, so it works without JavaScript.

Everything a person reads is resolved on the server, including the phrases the upload script shows: the script reads them from `data-` attributes and fills in `{file}`, `{sent}` and `{total}`. There is no English in the JavaScript, and no plural rule anywhere: counts are always separated from their noun.

Two tests keep it honest: both catalogues must have exactly the same keys, and the same number of placeholders in every phrase.

## Wiring

- Every action is a form POST or a link. With JS off the pages work and a reload is the truth.
- The poller sends strings the server already rendered; the script sets three nodes and reloads when the state changes, because a different state means a different screen.
- One animation: the waiting pill's dot, off under `prefers-reduced-motion`. Touch targets 44 px. Focus is a 2 px accent outline.
- **Anything holding a `nowrap` or monospace line needs `min-width: 0`.** A grid or flex item defaults to `min-width: auto`, so one long unbreakable line (the runner's progress, an rclone error) makes the card as wide as the sentence and pushes the whole page sideways on a phone. `.track`, `.panel` and `.state` carry it; long tokens in `code` and `.mono` wrap with `overflow-wrap: anywhere`. The screens are checked at 360 px by loading them in an iframe of that width and comparing each element's `right` against `clientWidth`.
- Pill classes map onto `core.DriveState` / `core.PhotosState`: `pill--idle · running · waiting · you · done · stopped`.

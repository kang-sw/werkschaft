# Companion pack authoring guide

A companion pack is the art and the character data for the dashboard's
desktop companion: a `pack.json` file plus the PNG or WebP images it uses.
This guide shows how to make a pack, how to list packs in a marketplace, how
to host them, and how to check them before anyone installs them.

A pack is **data only**. It never carries code, and the dashboard never runs
anything from a pack.

Two JSON Schemas describe the two files you write. They are served next to
this guide and work with any offline JSON Schema tool (draft 2020-12):

- `pack.schema.json` describes `pack.json` (`ws-companion/v1`).
- `marketplace.schema.json` describes `marketplace.json`
  (`ws-companion-marketplace/v1`).

The schemas cover the structure. The dashboard applies more checks (files that
must exist, sizes, image headers), so also run the import preview described in
[Validate before you publish](#validate-before-you-publish).

## Pack layout

One pack is one flat directory. The directory name is the pack ID.

```text
sample-a/
  pack.json
  stand.png
  typing.webp
  face-happy.png
```

- `pack.json` is the manifest. Every other file is a `.png` or `.webp` raster
  that `pack.json` names.
- There are no sub-directories. File names are plain: letters, digits, `.`,
  `_` and `-`, starting with a letter or digit, at most 128 characters before the extension,
  no `..`.
- The pack ID is 1 to 64 characters: lowercase letters, digits, `-` and `_`,
  starting with a letter or digit. `default` and `auto` are reserved.

## A minimal pack

Five fields are required: `schema`, `id`, `name`, `canvas`, and `poses` with a
`stand` pose that has at least one layer. The `id` must equal the directory
name (and the pack's `id` in the marketplace).

```json example:pack-minimal
{
  "schema": "ws-companion/v1",
  "id": "sample-a",
  "name": "Sample A",
  "canvas": [512, 512],
  "poses": {
    "stand": { "layers": [{ "file": "stand.png" }] }
  }
}
```

`canvas` is the logical drawing size in pixels, each side from 64 to 2048.
Layer coordinates, such as `pivot`, use that canvas.

A fuller pack adds optional parts. Every optional key below can be left out.

```json example:pack-full
{
  "schema": "ws-companion/v1",
  "id": "sample-a",
  "name": "Sample A",
  "canvas": [512, 512],
  "autoMatch": { "harnesses": ["example-harness"], "models": ["example-model"] },
  "persona": { "name": "Sample", "voice": "Calm and brief.", "quipLanguage": "en" },
  "poses": {
    "stand": { "layers": [{ "file": "stand.png" }] },
    "typing": {
      "fallback": "stand",
      "layers": [
        { "file": "typing.webp" },
        { "frames": ["hands-1.png", "hands-2.png"], "fps": 4 }
      ]
    }
  },
  "gestures": {
    "wave": {
      "pose": "stand",
      "layers": [
        {
          "file": "wave-arm.png",
          "pivot": [300, 280],
          "motion": { "oscillate": { "rotDeg": [-10, 10], "hz": 1.5 } }
        }
      ]
    }
  },
  "expressions": { "happy": { "decal": "face-happy.png" } },
  "props": { "sample-prop": { "file": "prop.png", "z": 60 } },
  "presets": { "sample-look": {} }
}
```

What the optional parts do:

- `poses` is an open map. `stand` is required; other poses such as `typing`,
  `perched` and `dangle` are optional. A pose that is missing a part falls
  back to the pose named by `fallback` (default `stand`).
- `gestures` is an open map of layers that play over a pose. A gesture can
  name its `pose` and list layers it `replaces`.
- A layer draws one raster (`file`), tinted channels (`line` and the `*Fill`
  channels, plus `detail`), or a frame loop (`frames`, which needs `fps`).
  `mode` is optional and is inferred from those keys. `z` orders layers
  (default 20), `pivot` is the joint a `motion` rotates around, and `motion`
  is a named track or an inline `oscillate` or `keyframes` track.
- `head`, `hairs`, `expressions`, `props`, `fx`, `motions`, `presets` and
  `scenarios` are optional maps of named parts. An expression with a `decal`
  swaps the whole face for that raster.
- `autoMatch` lists up to 32 `harnesses` and 32 `models` tags (1 to 128
  printable ASCII characters each) that make the automatic pack choice prefer
  this pack.
- `persona` gives the character a voice. It is read as quoted text, never as
  instructions. A malformed or too long persona is dropped; the pack still
  loads. An optional `voice_by_lang` map adds a per-language voice overlay,
  keyed by a language tag such as `ko` or `pt-BR`; the overlay that matches the
  reply language is added after `voice`. A malformed entry is dropped alone.
  Until the user saves their own persona or picks a built-in preset in
  Settings, the companion follows the active pack's persona; without one it
  uses the built-in default.
- Timing and physics extensions (`easings`, `stageEffects`, `propMotions`,
  `physics`, `passive`) are optional open maps.

**Unknown fields are ignored and kept.** A field a reader does not know does
not fail the pack, so newer packs still load in older dashboards. Names inside
open maps (the pose, gesture, prop and expression IDs) are yours to choose.

## Kit packs: outfits, attachments and eye sets

A **kit pack** is a pack that lets the user dress the character: swap outfits,
eye sets and worn items, and recolor tinted parts. It is the same
`ws-companion/v1` file with more optional sections. A pack without them stays a
plain pack and draws the layers under `poses` as before.

The head set and the body set are separate. Hair, eyes, attachments and colors
combine freely. The body set is an **outfit**: art that already wears the
outfit, drawn per column. A column is a pose or gesture ID such as `stand`.

```json example:pack-kit
{
  "schema": "ws-companion/v1",
  "id": "fixture-fox",
  "name": "Fixture Fox",
  "canvas": [512, 512],
  "registration": { "headAnchor": [255, 184], "feetPivot": [256, 503], "tolerance": 4 },
  "palette": { "skin": "#f2c9a5", "hair": "#6b3f1d", "outfitPrimary": "#2a4d69" },
  "defaultOutfit": "casual",
  "head": {
    "egg": { "skinFill": "egg-fill.png", "line": "egg-line.png" },
    "eyes": { "sclera": "eyes-sclera.png" }
  },
  "poses": {
    "stand": {
      "headAnchor": [255, 184],
      "layers": [
        { "line": "casual-stand-line.png", "jacketFill": "casual-stand-jacket.png", "skinFill": "casual-stand-skin.png" }
      ]
    },
    "typing": { "fallback": "stand", "headAnchor": [255, 240] }
  },
  "gestures": { "wave": { "pose": "stand" } },
  "outfits": {
    "casual": {
      "palette": { "outfitPrimary": "#2a4d69" },
      "poses": {
        "stand": {
          "layers": [
            { "id": "arm", "line": "casual-stand-line.png", "skinFill": "casual-stand-skin.png", "fills": { "outfitPrimary": "casual-stand-jacket.png" } }
          ]
        },
        "typing": {
          "headAnchor": [255, 240],
          "layers": [{ "line": "casual-typing-line.png", "fills": { "skin": "casual-typing-skin.png" } }]
        }
      },
      "gestures": {
        "wave": { "replaces": ["arm"], "layers": [{ "line": "casual-wave-line.png", "fills": { "skin": "casual-wave-skin.png" } }] }
      }
    },
    "formal": {
      "poses": {
        "stand": { "layers": [{ "line": "formal-stand-line.png", "fills": { "skin": "formal-stand-skin.png", "outfitPrimary": "formal-stand-fill.png" } }] }
      }
    }
  },
  "attachments": {
    "cap": { "slot": "headdress", "bind": "head", "layers": [{ "line": "cap-line.png", "fills": { "capFill": "cap-fill.png" } }] },
    "tail": {
      "slot": "tail",
      "bind": "body",
      "layers": [{ "file": "tail.png" }],
      "columns": { "typing": { "layers": [{ "file": "tail-seated.png" }] } }
    }
  },
  "eyesets": { "round": { "sclera": "round-sclera.png", "irisFill": "round-iris.png" } }
}
```

### Outfits

`outfits.<id>` holds `poses` and `gestures` maps of columns, plus an optional
`palette`. Each column carries:

- `layers`, the outfit's art for that column. Every layer is validated like a
  pose layer, except that it may draw with `fills` alone.
- Optional `headAnchor`, `handAnchor` and `feetPivot` (`[x, y]` in canvas
  pixels), and for a gesture `replaces`.

The art is outfit-baked: each column's rasters already contain the outfit. Draw
the skin as a flat, pure-white `skinFill` mask so the `skin` color tints it, the
same as the head. The head is drawn above every body layer, so the body art
tucks its neck under the head.

**Column registry.** A column an outfit binds still needs an entry under the
top-level `poses` or `gestures`, even a layer-less one (`"typing"` above).
Scenarios and the choice of pose read those top-level maps. A field a column
leaves out (`headAnchor`, `handAnchor`, `feetPivot`, `replaces`, and the
`pose` and `fallback` names) is taken from the top-level entry of the same ID.
`layers` is never inherited: a column is drawn from an outfit only when its own
entry has `layers`.

**Resolution.** The wanted column is looked up in the worn outfit. If the outfit
does not bind it, the column's `fallback` chain is followed inside the same
outfit, ending at `stand`. If nothing is bound, the layers under top-level
`poses` are drawn. An outfit never borrows a column from another outfit, and the
character is never drawn empty. Bind `stand` in every outfit.

An outfit gesture column draws its own layers. Its `replaces` list removes
layers of the same outfit's pose column whose `id` or `slot` matches.

**Default and choice.** `defaultOutfit` names the outfit worn when no preset or
setting picks one; without it the first outfit ID in sorted order is worn.
Outfit, eye-set and attachment IDs use the pack ID characters (lowercase
letters, digits, `-`, `_`), and the attachment ID `none` is reserved because it
clears a slot. A preset may choose `outfit`, `eyes` (an eye-set ID),
`attachments` (`{ "<slot>": "<id>" }`, with `"none"` to clear a slot) and
`palette`. A setting the user makes beats the preset, and the preset beats the
pack default. An ID the pack does not have is ignored.

### Tint channels and the palette

`fills` is a map from a channel name to a raster, valid on a layer or a frame.
Each raster is a neutral white mask that is multiplied by the color of its
channel. The built-in channels are `skin`, `hair`, `eye`, `outfitPrimary` and
`outfitAccent`. Your own channel names start with a letter and use letters,
digits, `-` and `_`, up to 32 characters (`capFill` above).

`palette` maps a channel to a `#rrggbb` color. It may sit on the pack and on an
outfit. A channel's color comes from, lowest to highest: the engine default,
the pack `palette`, the worn outfit's `palette`, the preset's `palette`, the
user's setting. A channel with no color draws untinted.

Every raster named in `fills` is a file of the pack like any other, so it
counts toward the data-only rule and the limits below.

### Attachments

`attachments.<id>` is a worn item: `slot`, `bind`, `layers`, optional `z`, and
optional `columns`. One attachment fills one slot at a time.

- `slot` is `headdress`, `ears`, `accessory` or `tail`, or a slot name of your
  own (a lowercase letter, then up to 31 lowercase letters, digits, `-` or `_`).
- `bind: "head"` moves the item with the head. `bind: "body"` draws it with the
  body: for a body item, `columns.<column>.layers` is used for the column being
  drawn (the gesture column first, then the pose column), and `layers` is the
  fallback.
- `z` sets the stacking order. Without it the slot decides: `tail` 11, `ears`
  45, `headdress` 56, `accessory` 57; a slot of your own gets 57 when
  head-bound and 21 when body-bound.

The validator checks only the layer lists of an attachment. It never reads
`slot` or `bind`, and the schema describes them without enforcing them, so a
misspelled slot or binding is not caught: check them yourself.

### Eye sets

`eyesets.<id>` takes the same keys as `head.eyes` (`sclera`, `irisFill`,
`irisLine`, `frameLower`, `lidCoverL`, `lidCoverR`, `lidLashL`, `lidLashR`,
`browL`, `browR`, `closedL`, `closedR`). `head.eyes` stays the default set. The
user or a preset picks another by ID.

#### Closed-eye stickers

`closedL` and `closedR` are optional full-canvas rasters, positioned like every
other eye raster, drawn as authored with no tint. When both exist, a blink is a
two-frame swap: the open frame is the usual stack, and the closed frame hides
the sclera, iris, lid covers and lash and draws the two plates over the face
base. `frameLower` and the brows stay drawn in both frames, so a plate that must
cover them paints over them.

The swap happens when the blink value reaches 0.5, or when a held `lid`, `lidL`
and `lidR` value reaches 0.95 on both sides. A held half-lid stays below that
and keeps the lid slide. A pack without both plates, or an expression that
winks one side, blinks with the lid slide as before. Expressions that use
`decal` draw no eyes and never blink. The validator only checks the files are
safe rasters. The lint warns about a plate without a `sclera`, a single-sided
pair, and a missing registration (below).

Older dashboards refuse to install a pack with a file that only a new key
names. To keep the pack installable, also list each plate under `head.mouths`
with an `eyeclosed-` key, for example `"eyeclosed-default-L": "closed-l.png"`.
Older dashboards ignore that mouth and blink with the lid slide. The engine
never draws an `eyeclosed-` entry as a mouth; the new key is the only source.

### Registration

`registration` is optional: `headAnchor` and `feetPivot` as `[x, y]`, and a
`tolerance` in pixels (default 4). It states where your columns are meant to
line up. The engine does not move art by it, and the validator does not check
it; it is a reference for your own checks. Compare each column's own or
inherited `headAnchor` with it: on both axes for `stand`, on x for the other
columns, since seated columns anchor lower by design. Likewise `feetPivot` is
informational only; the engine aligns columns by their bounds.

### The legacy `poses` projection

A kit pack must still be a valid plain pack. The validator requires the
top-level `poses.stand` with at least one layer, and the layers under top-level
`poses` and `gestures` follow the plain-pack rules: each layer or frame needs a
fixed raster key (`file`, `line`, `jacketFill`, `accentFill`, `skinFill`,
`detail` or `hairFill`), and `fills` alone are not enough there. Fill your
`poses.stand.layers` from your default outfit using only those keys. A dashboard
that predates kit packs ignores the new sections and draws only that
projection, and it serves only files it can find there, so a layer that carries
only `fills` is rejected by it. The same layers are the final fallback of the
resolution above.

### Mesh deform

An outfit column may declare a `deform` object so one piece of body art bends
(a breathing shoulder, swinging knees) without being cut into parts. It is
optional, only read on outfit columns, and ignored on a top-level `poses` or
`gestures` entry. The validator does not check it, and a dashboard that does not
know it draws the column rigid.

```json
"deform": {
  "grid": [12, 16],
  "bones": {
    "hip": { "pivot": [235, 330], "region": [[200, 300], [260, 300], [260, 420], [200, 420]], "feather": 24,
             "motion": { "oscillate": { "rotDeg": [-4, 4], "hz": 0.8 } } },
    "knee": { "pivot": [238, 410], "parent": "hip", "region": [[200, 400], [260, 400], [260, 500], [200, 500]],
              "motion": { "oscillate": { "rotDeg": [-6, 6], "hz": 0.8, "phase": 0.17 } } }
  },
  "roll": { "pivot": [256, 184], "motion": { "oscillate": { "rotDeg": [-2, 2], "hz": 0.5 } } }
}
```

- `grid` is `[columns, rows]` of the mesh laid over the column's drawn area.
  The default is `[12, 16]`; each value is clamped to 2 to 32.
- `bones` is a map of at most 16 bones. A bone ID is a lowercase letter, then up
  to 31 letters, digits, `-` or `_`. `pivot` is a canvas point. `region` is a
  polygon of at least 3 canvas points (at most 64 are read). `parent` names
  another bone; a child's transform applies after its parent's. `feather` is the
  distance in pixels over which a region's influence fades out (default 24, 0 to
  256). Keep it no tighter than about one grid cell.
- A bone's `motion` is the name of an entry in `motions`, a built-in motion
  name, or an inline motion like a layer's. Only `tx`, `ty` (canvas pixels) and
  `rotDeg` apply.
- `roll` rotates the whole composed column, head and attachments included,
  about `pivot` (default the column's `headAnchor`). Only `rotDeg` applies.
- The head is not meshed, but it follows the deformed neck point so it stays
  seated.
- Layers with `frames`, a `motion` or `swingWeight`, body attachments and
  `replaces` removals are drawn rigidly and are never meshed. The meshed art
  draws at the lowest `z` of the column's static layers, so do not put a rigid
  layer's `z` between the static layers.
- Each column declares its own `deform`; nothing is inherited. Keep motions
  small: a bend of a few degrees or a few pixels stays clean, and large angles
  thin or tear the line art. Preview at rest and at the extreme of each motion.

## The marketplace manifest

A pack is installed from a **source**: a collection that holds a
`marketplace.json` listing its packs. The manifest is `marketplace.json` at the
root of the source. If a host wraps the files in one top-level folder, as
generated repository archives do, the manifest is read from that folder.

A self-contained marketplace keeps every pack in the same source. Each entry's
`path` names the pack directory:

```json example:marketplace
{
  "schema": "ws-companion-marketplace/v1",
  "name": "Example Packs",
  "owner": { "name": "Example Author", "url": "https://example.invalid/" },
  "packs": [
    {
      "id": "sample-a",
      "name": "Sample A",
      "version": "1.0.0",
      "author": "Example Author",
      "license": "CC-BY-4.0",
      "description": "A small example pack.",
      "path": "packs/sample-a"
    }
  ]
}
```

The source then holds this tree. Files outside the listed pack directories
(a README, a license, source art) are ignored and never stored.

```text
marketplace.json
packs/
  sample-a/
    pack.json
    stand.png
```

An external entry points at a separate pack zip instead of a directory. It
needs both `url` and `sha256`, the SHA-256 of that zip as 64 lowercase hex
characters (for example the output of `sha256sum sample-b.zip`):

```json example:marketplace-external-entry
{
  "id": "sample-b",
  "name": "Sample B",
  "version": "2.1.0",
  "license": "CC0-1.0",
  "url": "https://example.invalid/packs/sample-b.zip",
  "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}
```

The pack zip holds `pack.json` and its rasters at its root, or inside its one
top-level folder. The download is checked against `sha256` before it is
opened, so replace the hash whenever you republish the zip. One marketplace
can mix `path` and `url` entries.

Rules for entries:

- `id`, `name` and `version` are required. `author`, `license` and
  `description` are optional.
- Exactly one of `path` or `url` is set. `sha256` goes with `url` only.
- `path` is a clean relative directory: no leading `/`, drive letter,
  backslash or `..` segment.
- `url` is an absolute `https:` URL without a user name or password.
- The entry `id` equals the `id` in that pack's `pack.json`. An `id` is listed
  once.
- Text fields hold no control characters.
- An entry that fails does not hide the other entries; the dashboard reports
  the error next to that entry.

## Hosting

### A git repository

Put `marketplace.json` at the root of the repository (or in the folder the
host wraps it in) and give users the repository's https address:

```text
https://example.invalid/author/example-packs
https://example.invalid/author/example-packs#v1.0.0
```

The optional `#ref` after the address is a branch, a tag, or a full 40-hex
commit. Without it the default branch (`HEAD`) is used. The dashboard resolves
the ref to a commit and downloads a zip archive of that commit. It never runs
git.

The host must serve commit archives at one of these addresses, tried in this
order:

```text
<repo>/archive/<sha>.zip
<repo>/-/archive/<sha>/<name>-<sha>.zip
```

`<repo>` is the repository address, `<sha>` the commit, and `<name>` the last
segment of the repository path without a trailing `.git`. If your host serves
neither, publish a zip instead.

### An https zip

Build a zip with `marketplace.json` at its root (or in one top-level folder)
and the pack directories beside it, upload it anywhere that serves the file
directly over https, and give users the zip's address. The address must end in
`.zip`. A page that merely links to the file is not enough: if the address does
not return a zip, the dashboard says so and suggests importing the file
instead.

### A local zip

A user can also import a zip file from disk in the dashboard. It uses the same
layout and rules as an https zip, so one zip can carry a whole marketplace and
its packs (for example a bundle shared from a personal drive). Use this when a
share link opens a web page instead of downloading the file. A local import
has no address to check again, so the user imports the new zip to update.

A zip of a marketplace must always include `marketplace.json`, even when it
holds only one pack.

## The data-only rule

A pack directory may hold exactly `pack.json` and the `.png` or `.webp` files
that `pack.json` references. Anything else makes that pack fail and nothing of
it is stored:

- a file that is not `pack.json` or a referenced raster (a stray README,
  `.svg`, HTML, script, or an unused image);
- a symbolic link or any other non-regular entry;
- a file inside a nested directory;
- a raster whose content does not match its extension.

Source files, drafts and licenses belong outside the pack directories.

## Names

- Names that differ only in letter case, such as `Stand.png` and `stand.png`,
  are refused, because some file systems treat them as one file.
- Reserved device names are refused for a pack ID and for a file name, with
  or without an extension, in any case, because some file systems cannot
  create them: `con`, `prn`, `aux`, `nul`, `com1` to `com9`, `lpt1` to
  `lpt9`.
- Archive entry names must be UTF-8 relative paths with no backslash, `..`
  segment, drive letter or duplicate. Only stored or deflated, unencrypted
  entries are accepted.

## Limits

Limits are counted on the bytes actually unpacked, not on the sizes an archive
declares.

| Limit | Value |
|---|---|
| `marketplace.json` size | 256 KiB |
| Packs listed in one marketplace | 32 |
| Installed local packs | 32 |
| Entries in one zip | 4096 |
| Files in one pack directory | 256 |
| Compression ratio of one entry | 100:1 |
| Unpacked bytes in one install | 64 MiB |
| `pack.json` size | 512 KiB |
| One raster file | 2 MiB |
| One pack directory, total | 16 MiB |
| All installed local packs, total | 128 MiB |
| Raster width or height | 4096 px |
| Canvas width or height | 64 to 2048 px |
| Tags per `autoMatch` field | 32 |
| `autoMatch` tag length | 128 characters |
| Frame loop speed | 0.1 to 30 fps |
| Pack ID length | 64 characters |
| Raster file name length | 128 characters before the extension |
| URL length | 2048 characters |
| Archive path length | 512 characters |
| Marketplace and pack name | 64 characters |
| Pack version | 32 characters |
| Author and license | 64 characters |
| Pack description | 280 characters |
| Persona name | 40 characters |
| Persona voice | 800 characters |
| Persona `voice_by_lang` entries | 32 |
| Persona `voice_by_lang` overlay | 800 characters |
| Source zip download | 64 MiB |
| External pack zip download | 40 MiB |
| Download time | 60 s |
| Redirects followed (https only) | 5 |

## How updates are detected

An installed pack is compared by its **content**: a hash of `pack.json` and the
rasters it uses. A pack shows an update when the source now holds different
content for it. An external `url` pack shows an update when its entry `sha256`
changes. The `version` string is shown to the user and nothing more: change
the content and the update is found even with the same version, keep the
content and a new version number alone raises no update. An unrelated commit
in a repository with many packs does not make every pack show an update.

Checking and updating run only when the user asks. Nothing is fetched in the
background.

Installing a pack writes it, with an install record (`.ws-install.json`) that
remembers where it came from, into the user's companion pack folder. Only
directories that have an install record are ever replaced or uninstalled by a
source. A pack the user made by hand, a directory without that record, is
never replaced and never removed: an install that uses its ID is refused, and
the user has to delete the directory first.

## Validate before you publish

- **Import preview.** In the dashboard's companion settings, import your zip
  as a local file. The preview runs the same checks as an install without
  writing anything and lists each pack with its errors. It calls the endpoint
  `POST /api/companion/packs/validate` with the zip as the request body, which
  only the local dashboard session may use. For a git source, build the same
  zip from your checkout and preview that.
- **External packs are not downloaded in the preview.** An entry with `url`
  is listed with its status, and its zip is fetched and checked only when the
  user installs it. Check an external pack zip yourself by putting the same
  pack in a `path` entry of a local test marketplace and previewing that.
- **Schemas.** Run `pack.schema.json` against each `pack.json` and
  `marketplace.schema.json` against `marketplace.json` with any offline schema
  tool. This catches structure and typing errors early. It does not open the
  images or check file names against the folder.

## Trust

A source's packs are third-party content. The dashboard does not review them,
and only the user decides which sources to trust. Add a source only when you
trust its author. Pack text, such as names, descriptions and personas, is
treated as untrusted data and shown as plain text.

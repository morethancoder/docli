# Source rules (source.yaml)

Each source keeps its rules in `.docli/<name>/source.yaml`. `docli add` writes them, and every
later `sync` and stale check reuses them. Change them in one of these ways:

1. Preview with flags: `docli inspect <url|file|name> --file-level 3 --drop .sidebar`
   shows the tree, the sizes and the coverage without writing anything.
2. Save the rules you like: add `--save` to `inspect`, or edit `source.yaml` by hand.
3. Apply them: `docli sync <name>`.

## Fields

| field | default | meaning |
|---|---|---|
| `name` | from the URL (a local file: its file name, or its folder for `index`/`README`) | folder under `.docli/`. Letters, digits, dashes. |
| `url` | | `http(s)://` page or `file://` path. |
| `format` | `auto` | `auto`, `html` or `markdown`. `auto` decides from the content type and the text. |
| `render` | `auto` | `static` fetches the HTML. `browser` loads the page in headless Chrome, for docs built by JavaScript. `auto` tries static first and switches to the browser when the page has no real content. |
| `selector` | | CSS selector of the content area (HTML). Without it, docli takes `main`/`article`, or the smallest block that holds nearly all the headings. |
| `drop` | | CSS selectors removed before parsing (menus, footers, banners). |
| `file_level` | `0` | heading level written as one file each, e.g. `4` puts each `####` in its own file and makes shallower headings folders. `0` decides by size. |
| `collapse_tokens` | `1500` | with `file_level: 0`, a section whose whole subtree is under this size stays one file. |
| `max_tokens` | `6000` | a file over this size is split at its next heading level. |
| `kinds` | | labels for sections: a list of `{kind, title, under}`. `title` and `under` are regular expressions for the section title and its parent's title. The first match wins; unmatched sections are `section`. |
| `base` | | the real address of a saved page (`file://` copy), so links resolve to the site instead of the local file. Presets set it. |
| `changelog` | | title of the section whose children are dated entries; `docli changelog` reads them. |
| `check_every` | `24h` | how old the last stale check may be before a read starts a new one (Go duration). |

## Example: Telegram Bot API (the built-in preset)

```yaml
name: telegram-bot-api
url: https://core.telegram.org/bots/api
format: html
render: static
selector: '#dev_page_content'
file_level: 4
collapse_tokens: 1500
max_tokens: 6000
kinds:
  - kind: changelog-entry
    under: ^Recent changes$
  - kind: type
    title: ^[A-Z][A-Za-z0-9]+$
  - kind: method
    title: ^[a-z][A-Za-z0-9]+$
  - kind: guide
base: https://core.telegram.org/bots/api
changelog: Recent changes
check_every: 24h
```

Use a preset on a local copy with `docli add page.html --preset telegram-bot-api`.

## Safety

`add` and `sync` refuse to write a split that loses or duplicates text (the `coverage` check);
the error lists example words. Fix the rules (usually `selector` or `drop`) and try again, or
pass `--force` to write anyway.

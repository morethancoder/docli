# docli

Download a docs site once, split it into small Markdown files with YAML headers, and read only
the one you need.

docli is for agents that need docs without reading a whole page into context. It fetches the
docs into `.docli/` in your project, with one file per method, type or section. It also writes an
`INDEX.md` map at every folder level. A read costs a few hundred tokens instead of the whole
site. Humans get the same files, styled output and a terminal UI.

```
$ docli add https://core.telegram.org/bots/api
✓ telegram-bot-api v1 · 648 files · 155k tok → .docli/telegram-bot-api
  parsed as html
  coverage ok: all 82118 source words are in exactly one file

$ docli show sendMessage
---
title: sendMessage
kind: method
source: https://core.telegram.org/bots/api#sendmessage
path: available-methods/sendMessage.md
parent: available-methods/INDEX.md
version: 1
hash: b7cc59eb1321ca0e
tokens: 864
---

# sendMessage

Use this method to send text messages. On success, the sent [Message](../available-types/Message.md) is returned.
...
```

## Install

```sh
go install github.com/morethancoder/docli/cmd/docli@latest
```

Or from a clone: `make build` writes `bin/docli`, and `make install` installs it. Needs Go 1.27+.
Docs built by JavaScript also need Chrome or Chromium; set `DOCLI_CHROME` to its path if
docli can't find it.

## What it does

- **Splits by structure.** Headings become folders and files. Every file has a YAML header (title, kind,
  source URL, version, tokens), and every folder has an `INDEX.md` with a one-line summary
  and token count per entry. Links between sections become relative file links.
- **Loses no text.** Each split is checked word by word against the source and is refused
  if anything would be lost or duplicated.
- **Handles several source types:** static HTML, pages built by JavaScript (headless Chrome), and
  Markdown / `llms.txt`.
- **Syncs only what changed.** `docli sync` rewrites only the files that changed and records a version
  with diffs, so `docli diff --since 2026-11-01` works without git. Commit `.docli/` and git
  tracks the docs too.
- **Never syncs on its own.** A read checks upstream within a 1.5 s budget; slower
  checks go on in the background and report on the next command. `--latest` checks, syncs, then
  answers.
- **Reads changelogs.** `docli changelog` lists a source's dated changelog entries (for example,
  Telegram's "Recent changes").

## Commands

```
add <url|file>      download a source and split it into indexed files
inspect <url|name>  preview the split (tree, sizes, coverage) without writing
sync [name...]      re-fetch and rewrite only what changed
list                sources in this folder
tree [source]       folders and files of a source
show <ref>          one section by title, path or folder (folder → its INDEX)
search <query>      by name first, then by content
diff [source]       changes since a version or date
changelog [source]  the source's own dated changelog
check [name...]     check for upstream changes now (never syncs)
browse [source]     interactive terminal UI
remove <name>       delete a source
skill               write a Claude Code skill that teaches agents to use docli
```

Global flags: `--json`, `--path <dir>` (default `./.docli`), `--latest`, `--no-check`.

## For agents

Every command takes `--json` and prints one fixed envelope:

```json
{
  "ok": true,
  "command": "search",
  "data": [
    {
      "source": "telegram-bot-api",
      "path": "available-methods/copyMessage.md",
      "title": "copyMessage",
      "kind": "method",
      "tokens": 982,
      "match": "content",
      "line": 18,
      "snippet": "…Unique identifier of the message effect to be added to the message…"
    }
  ],
  "stale": []
}
```

Errors have stable codes (`usage`, `empty`, `not_found`, `ambiguous`, `coverage`, `error`) and
exit 1. See [docs/json.md](docs/json.md) for every command's output.

A good read loop:

1. `docli tree --json` or read `.docli/<source>/INDEX.md`.
2. `docli show <name> --json`, or open the file directly.
3. If `stale` reports `stale`, run `docli sync <source>`, or repeat the read with `--latest`.

`docli skill` writes `.claude/skills/docli/SKILL.md`, which teaches Claude Code this workflow.

## Tuning a source

If a site splits badly, preview the split with flags, save the rules, then sync:

```sh
docli inspect https://example.com/docs --selector main --drop .sidebar --file-level 3
docli inspect https://example.com/docs --selector main --drop .sidebar --file-level 3 --save
docli sync example-docs
```

The rules live in `.docli/<source>/source.yaml`; see [docs/config.md](docs/config.md).
Telegram's Bot API has a built-in preset (`--preset telegram-bot-api`), which also works on a
saved copy of the page.

## Development

```sh
make          # list targets
make test     # all tests (SHORT=1 skips the headless-browser test)
make lint     # gofmt check + go vet
make doctor   # check Go and the optional browser
```

## License

Not yet chosen.

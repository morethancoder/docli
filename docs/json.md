# docli --json

Every command run with `--json` prints one JSON object on stdout, and nothing else. Only
`browse` refuses `--json`, because it is the interactive TUI.

## Envelope

```json
{
  "ok": true,
  "command": "show",
  "data": { },
  "stale": [ ],
  "hint": "...",
  "error": { "code": "not_found", "message": "...", "details": null }
}
```

| field | meaning |
|---|---|
| `ok` | true on success. Exit code 0 when true, 1 when false. |
| `command` | the subcommand that ran. |
| `data` | the result. Its shape depends on the command (below). Left out on error. |
| `stale` | stale-check notes for the sources the command read. Always present, maybe `[]`. |
| `hint` | a next step for the agent, when docli has one (e.g. a search found nothing: which pages are downloaded and how to add more). Left out otherwise. |
| `error` | present only when `ok` is false. |

### Error codes

| code | when |
|---|---|
| `usage` | bad flags or arguments, unknown command, more than one source where one is needed |
| `empty` | no sources in the docs folder yet |
| `not_found` | no source, section or file matches the reference |
| `ambiguous` | several sections match. `details` lists them (`source`, `path`, `title`) |
| `coverage` | the split would lose or duplicate text, so nothing was written. For `add`, `details` is the coverage object; for `inspect`, it is the whole preview (`inspect` data below) |
| `error` | anything else (network, parse, disk) |

### Stale notes

```json
{ "source": "telegram-bot-api", "status": "stale", "checked_at": "2026-11-10T09:12:03Z", "message": "..." }
```

`status` is one of these:

- `fresh`: the last check found no change.
- `stale`: the docs changed upstream. Run `docli sync <source>`, or add `--latest` to the read.
- `pending`: the check did not finish within the budget (1.5 s). It goes on in the background,
  and the result shows on the next command.
- `error`: the check failed (for example, the network was down).

A check never changes the stored files. Only `sync`, `add` and `--latest` write.

## Data by command

| command | `data` |
|---|---|
| `add` | `{source, version, changed, added[], changed_files[], removed[], files_written, files, tokens, dir, parser, rendered, note?, coverage, warnings[], suggestions?[]}` |
| `sync` | a list of `add` objects, one per source |
| `inspect` | `{source (rules), saved, parser, rendered, note?, title, headings{h2: n}, files, folders, tokens, largest?, coverage, warnings[], tree[], suggestions?[]}` |
| `list` | `[{name, title, url, dir, version, files, tokens, synced_at}]` |
| `tree` | `[{name, path, title, kind, tokens, items?, children?[], more?}]`, folders first after `_overview.md` |
| `show` (`[source] <ref>`) | `{source, path, file, title, kind, tokens, bytes, version?, source_url, content}`. `content` is the whole file, YAML header included |
| `search` | `[{source, path, title, kind, tokens, match: "name"\|"content", line?, snippet?}]`, best first; `line` counts from the top of the stored file, header included |
| `diff` | `{source, since_version, current_version, versions[], files: [{path, title, status: added\|changed\|removed, diff}]}` |
| `changelog` | `[{date: "YYYY-MM-DD", title, path, items[]}]`, newest first |
| `check` | a list of stale notes, one per source checked |
| `remove` | `{removed}` (the source name) |
| `skill` | `{path}` |
| `version` | `{version, commit, date, go, os, arch}` |
| `update` | `{current, latest, newer, updated, path?, url?, check_only}` |

`suggestions` is `[{url, why}]`. It appears only when an HTML page outside any docs path
(`/docs`, `/guide`, `/components` …) split into 12 files or fewer, which usually means a landing
page. It lists whole-docs files the site serves (`/llms-full.txt`, `/llms.txt`, `/docs.md`) and
doc-like links on the page.

`coverage` is `{source_words, output_words, missing, duplicated, examples?[], ok}`. It compares
the words in the source text with the words written to files, and a split is accepted only when
`ok` is true.

## Stored file header

Every stored `.md` file starts with a YAML header:

```yaml
---
title: sendMessage
kind: method
source: https://core.telegram.org/bots/api#sendmessage
path: available-methods/sendMessage.md
parent: available-methods/INDEX.md
version: 1          # the version this file last changed in (not on INDEX files)
items: 142          # INDEX files only: entries listed
hash: 3f0c...       # content hash, used by sync
tokens: 864         # estimated tokens of the body
---
```

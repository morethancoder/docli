# Field test: can a small agent use docli cold?

Date: 2026-11-10. Build under test: round 1 `fa2f080`, round 2 `328fc51` (the fixes below).

## What was tested

Six subagents ran in empty folders: Haiku and Sonnet, one pair per docs source. Each source is a
different kind of docs:

| source | URL given | kind |
|---|---|---|
| Telegram Bot API | https://core.telegram.org/bots/api | one huge static HTML page |
| Datastar | https://data-star.dev | small landing page, docs on other pages, a `docs.md` with everything |
| Ant Design Mobile | https://mobile.ant.design | JavaScript-built site, one page per component |

Every agent got the same brief:

- Learn docli from `--help` only; no docs and no source.
- Download the docs.
- Answer three fixed questions, reading no more than about 5k tokens per question.
- Report every command, the files read, the tokens read, and each problem with its severity.

A script scored the runs (`fieldtest/score.py`). A run passes when `add` succeeded with coverage
OK, all three answers match the known values, and the budget held. The workflow ran through
`fieldtest/collect.py` to `fieldtest/round1.json` and `round2.json`.

## Results

| | round 1 (before) | round 2 (after fixes) |
|---|---|---|
| runs passed | 5 / 6 | **6 / 6** |
| answers correct | 18 / 18 | 18 / 18 |
| blocker problems | 3 | **0** |
| "wrong" problems | 4 | 3 (see below) |
| friction notes | 29 | 25 |
| commands run, all agents | 74 | 56 |
| largest read for one question | 1016 tok | 1000 tok |

Round 2, per run:

| source | model | add | answers | max tokens/question | pass |
|---|---|---|---|---|---|
| antd | haiku | ok | 3/3 | 490 | pass |
| antd | sonnet | ok | 3/3 | 600 | pass |
| datastar | haiku | ok | 3/3 | 713 | pass |
| datastar | sonnet | ok | 3/3 | 713 | pass |
| telegram | haiku | ok | 3/3 | 1000 | pass |
| telegram | sonnet | ok | 3/3 | 864 | pass |

Round 1's only failure was Datastar on Haiku. It added the landing page, found nothing, and then
read whole files looking for the reference docs, which broke the budget.

## What went wrong in round 1

1. **Coverage false positives blocked antd mobile** (blocker). Every component page "duplicated"
   the words `tsx` and `ts`. They came from code-fence labels (```` ```tsx ````), which are not
   page text.
2. **`by[XTech](…)` failed coverage on the antd home page.** The page shows one word, `byXTech`.
   Markdown splits it into `by` and `XTech` at the link edge.
3. **Landing pages were silent** (blocker for Datastar and antd). `add https://data-star.dev`
   reported success with 8 files of marketing text, and nothing said the docs were elsewhere.
4. **`inspect` exited 0 on a failed coverage check**, so scripts could not tell a bad preview
   from a good one.
5. **Telegram type tables vanished.** Any table with a line break in a cell (e.g.
   InlineKeyboardButton) was dropped whole by the table converter and replaced by loose text.
6. **Datastar code had line numbers baked in** (`1<div data-on-interval…`). These came from the
   Chroma line-number gutter.
7. Friction:
   - a search miss gave no clue that the term might be on a page not yet downloaded;
   - search snippets were too short;
   - `show` could not take a source name and a ref as two arguments;
   - the `--render` help did not say what `auto` does;
   - a transient 5xx failed `add` outright;
   - `add --force` gave no sign that text was lost.

## What got fixed

All fixes are in commit
[328fc51](https://github.com/morethancoder/docli/commit/328fc51). Each one has a regression test.

| # | fix | test |
|---|---|---|
| 1 | Coverage ignores code-fence lines and `<br>`. | `TestCoverageIgnoresMarkupEdges`, `TestFieldFixturesCoverage` (saved antd pages) |
| 2 | Coverage settles words that markdown glues or splits at a link edge (`by`+`xtech` = `byxtech`, `[Get Started][Preview]`). A real loss is still caught. | `TestCoverageIgnoresMarkupEdges` |
| 3 | An HTML page outside any docs path that splits into 12 files or fewer counts as a landing page. `add` and `inspect` then return `suggestions`: whole-docs files the site serves (`/llms-full.txt`, `/llms.txt`, `/docs.md`) and doc-like links on the page. A `hint` names the best one, and `add` prints "⚠ landing page only". | `TestSuggestOnLandingPage` (httptest site) |
| 4 | `inspect` exits 1 with code `coverage`; the preview is in `error.details`. | `TestInspectCoverageFailureExits1` |
| 5 | Tables keep line breaks as `<br />` instead of being dropped. | `TestTableWithLineBreakStaysATable` |
| 6 | Line-number gutters inside `<pre>` are removed (Chroma, highlight.js, Prism, and `user-select:none` spans). | `TestCodeHasNoLineNumbers` (saved Datastar page) |
| 7 | A static fetch retries once after a network error or 5xx; a 404 is not retried. | `TestStaticRetriesOnce` |
| 8 | `add --force` marks the header "⚠ written with --force". | `TestInspectCoverageFailureExits1` |
| 9 | `show [source] <ref>` and `tree <source>/<folder>` work. | `TestShowWithSourceArg`, `TestTreeSourceSlashFolder` |
| 10 | A search with no match hints which pages are downloaded and how to add another. | `TestSearchMissHint` |
| 11 | Search snippets are wider; the `--render` help explains `auto`; `add --help` explains one page per source; the agent skill covers suggestions, hints and `show <source> <ref>`. | |

How it looks now on Datastar:

```
$ docli add https://data-star.dev --name datastar
⚠ landing page only: datastar v1 · 8 files · 2.0k tok → .docli/datastar
  ...
  few files: this may be a landing page. The docs may be at:
    https://data-star.dev/docs.md  all docs in one Markdown file
    https://data-star.dev/guide  docs link on the page: Guide
hint: this looks like a landing page, not the docs. Try `docli add https://data-star.dev/docs.md` ...
```

In round 2, every Datastar agent followed that hint to `docs.md`: 145 files, coverage OK, one add.

## What remains

- **The three "wrong" reports in round 2:**
  - Two came from Haiku agents objecting that a landing-page `add` still printed a ✓. It now
    prints "⚠ landing page only", a change made after round 2.
  - The third is Telegram's own table, which marks `chat_id` Optional while its text says it is
    required. That is upstream's wording, not a parse error.
- **One page per source.** antd mobile has one page per component, so each needs its own `add`.
  A crawl mode was left out on purpose (docli is not a docs hub). The suggestions point agents at
  the right page instead.
- **Search ranking** for common words is noisy, and there is no list of built-in presets.
- **The harness**, not docli: the round-2 folders were not created before the agents started, so
  each agent ran `mkdir` first.

These are logged in the project's findings log for the next session.

## Reproduce

The run used the Workflow script described above. To score a round:

```sh
python3 fieldtest/collect.py <journal.jsonl> > fieldtest/round2.json
python3 fieldtest/score.py fieldtest/round2.json --md
```

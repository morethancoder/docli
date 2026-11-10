# Field test: can a small agent use docli cold?

Date: 2026-11-10. Build under test: round 1 `fa2f080`, round 2 `328fc51` (the fixes below), round 3
`633ab00` (v0.1.0) with its fixes in the commit after it.

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

## Round 3: held-out sites

Rounds 1 and 2 used the sites the fixes were made for. Round 3 used four sites docli had never seen,
each a different kind, to check that the fixes generalise:

| source | URL given | kind |
|---|---|---|
| tRPC | https://trpc.io/docs | Docusaurus; the docs start page is an overview |
| Resend | https://resend.com/docs | Mintlify; serves `.md` pages and `/docs/llms-full.txt` |
| Python datetime | https://docs.python.org/3/library/datetime.html | Sphinx static reference |
| Petstore | the swagger-petstore `openapi.yaml` on GitHub | an OpenAPI spec (scored separately: docli has no OpenAPI parser) |

The brief was the same, plus a second part: run `check`, then `sync`, and record a checksum of the
doc files before and after. A run now also needs check and sync to succeed with no files changed.
Each site had 2 Haiku runs and 1 Sonnet run (12 agents).

| | round 1 | round 2 | round 3 (first pass) | round 3 (after fixes) |
|---|---|---|---|---|
| runs passed | 5 / 6 | 6 / 6 | 8 / 9 | **9 / 9** |
| answers correct | 18 / 18 | 18 / 18 | 36 / 36 (OpenAPI included) | 3 / 3 rerun |
| adds that needed `--force` | 0 | 0 | 6 of 12 | 0 |
| blocker / wrong problems | 3 / 4 | 0 / 3 | 6 / 19 | 0 / 0 rerun |
| largest read for one question | 1016 tok | 1000 tok | 1015 tok | 361 tok |
| cost at list prices | n/a | ~$0.85 | $1.11 | +$0.24 |

Round 3 cost $1.35 in total: 8 Haiku runs $0.15, 4 Sonnet runs $0.96, and the Sonnet rerun $0.24.
The figures come from the subagent transcripts: 9.2M tokens, mostly cache reads.

The answers were all right, but half the agents had to pass `--force` to get anything. Every check
and sync left the doc files unchanged (same checksum before and after).

### What went wrong in round 3

1. **Coverage failed on good pages** (the cause of all 6 forced adds):
   - Numbered headings like `### 1. Create a router` lost their number in the file title. This hit
     trpc's `llms-full.txt` (38 "missing" digits) and Resend's (also in bold: `## **6. Verify**`).
   - A markdown escape (`24\*60\*60`) counted as different words from the page's `24*60*60`. This
     hit Python datetime.
2. **Real text loss, caught by coverage:**
   - A heading inside a quote (`> ## Documentation Index`, at the top of every Resend `.md` page) was
     treated as a setext heading, and the quoted line under it was dropped.
   - Code in a heading lost its underscores: `` ### `restricted_api_key` `` became `restrictedapikey`,
     so a search for the error code found nothing.
3. **A forced source could not sync.** A plain `sync` re-ran the coverage check and failed even when
   nothing had changed, so every routine sync needed `--force`. `sync --force` then gave no warning.
4. **Search line numbers were off** by the YAML header: "line 77" was line 90 in the stored file, so
   `sed -n` / `grep -n` on the file landed in the wrong place.

### What got fixed

| # | fix | test |
|---|---|---|
| 1 | Coverage ignores a list or heading number (`1.`, `### 2.`, `## **6.`) and reads markdown escapes as the plain character. | `TestCoverageIgnoresMarkupEdges` |
| 2 | Headings inside quotes and list items stay part of their block; heading titles keep code-span text and snake_case underscores. | `TestMarkdownHeadingTextAndQuotes` |
| 3 | `sync` of a source whose text has not changed since the last sync passes without `--force`, and says "⚠ coverage check fails". Changed text still needs `--force`. | `TestInspectCoverageFailureExits1` |
| 4 | Search line numbers count from the top of the stored file. | `TestFindAndSearch` |

After the fixes, all five real sources from round 3 pass coverage without `--force`:

| source | files | coverage before | after |
|---|---|---|---|
| trpc `llms-full.txt` | 431 | 38 missing | ok |
| Resend `llms-full.txt` | 1263 | 143 missing, 16 extra | ok |
| Resend `/docs` | 3 | 12 missing | ok |
| Resend idempotency `.md` page | 6 | 12 missing | ok |
| Python datetime | 17 | 1 missing, 3 extra | ok |

The failed run (trpc on Sonnet) was rerun on the fixed build. It added `llms-full.txt` (431 files)
with no `--force`, answered 3/3 at 361 tokens at most, and ran a plain `check` and `sync` with no
changed files. It reported no blocker and no "wrong" problem.

### Left for the owner (findings log)

- **OpenAPI:** the petstore spec became one 5.8k-token file. Agents still answered 9/9 through
  search, but one operation cannot be read cheaply. An `openapi` parser (one file per operation)
  would be a new feature.
- **An overview under `/docs` gets no suggestions.** `trpc.io/docs` gave 7 overview files and no hint
  that `/llms-full.txt` exists, because landing-page detection skips docs paths.
- Search shows one line per file and falls back to single words for a phrase. Sphinx `¶` links
  cost tokens. Python's class reference is one 11k-token section (members are `<dt>`, not headings).
  File names repeat a word (`httpbatchlink-options-options.md`) and mix case.

## Reproduce

The run used the Workflow script described above. To score a round:

```sh
python3 fieldtest/collect.py <journal.jsonl> > fieldtest/round2.json
python3 fieldtest/score.py fieldtest/round2.json --md
python3 fieldtest/score.py fieldtest/round3.json --md   # round 3; round3-rerun.json for the rerun
```

Round 3's questions and verified answers are in `fieldtest/round3-answers.json`.

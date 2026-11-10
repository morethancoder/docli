# docli

A Go CLI that lets agents download docs without reading them into context. It splits docs into
small Markdown files with YAML headers, organised as folders with an INDEX.md map, so any read is
cheap. It syncs only the changed parts, keeps versions, and runs stale checks.

## Rules (from the owner)

- docli is not a docs hub, and it does not copy other tools. Its single job is cheap,
  indexed, local doc reads for agents.
- Go, written source-first (read the installed library source with `opensrc` before using an
  API). Use reliable packages: cobra, Charm (bubbletea/bubbles/lipgloss/glamour v2 from
  `charm.land`), html-to-markdown, goquery, goldmark, chromedp.
- Agentic first: every command supports `--json` with the fixed envelope in `docs/json.md`.
  Human output and the `browse` TUI are the second audience.
- Docs live in `.docli/` in the folder where docli runs; `--path` overrides it. docli keeps its
  own version record in `.docli/<source>/.meta/`, and the project's git tracks the files too.
- Stale checks are never autonomous. A read checks within a 1.5 s budget, and anything slower
  continues in a background `docli check`, with the result shown on the next command.
  `--latest` checks, syncs, then answers. Checks never write doc files.
- Sources: HTML, JS-built pages (headless Chrome), Markdown / llms.txt.
- Never write a split that fails the coverage check unless `--force` is given.
- Split rules are previewed with `docli inspect` and saved per source (`source.yaml`,
  `docs/config.md`).

## Work

- `make` lists the targets: `build test lint fmt clean doctor install`.
  `make test SHORT=1` skips the headless-browser test.
- Fixtures are in `testdata/`: `telegram.html` (a saved Bot API page), `llms.md`, and
  `js.html`, which is served by httptest for the chromedp test.
- Bubbles v2 `list` defaults to dark styles: set `Styles` from `tea.BackgroundColorMsg`.
- gopls in this folder shows stale "not in workspace" errors; trust `go build` and `go test`.

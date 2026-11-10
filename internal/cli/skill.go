package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// SkillText is the agent skill written by `docli skill`.
const SkillText = `---
name: docli
description: Read third-party docs cheaply from .docli/ instead of loading whole pages. Use when you need an API, method, type, option or changelog entry from a docs source that docli has downloaded, or when you want to add a new docs source for this project.
---

# docli: small indexed docs

docli keeps docs in ` + "`.docli/<source>/`" + ` as small markdown files with YAML
headers, one per method, type or section, with an INDEX.md in every folder.
Never fetch or read a whole docs page into context; ask docli for one piece.

## Read

1. ` + "`docli show <name> --json`" + ` returns one section, e.g. ` + "`docli show sendMessage`" + `.
   A path (` + "`available-methods/sendMessage`" + `) or a folder name (returns its INDEX) works too.
   With several sources, name one first: ` + "`docli show <source> <name>`" + `.
   An ambiguous name fails with code ` + "`ambiguous`" + ` and lists the candidates in error.details.
2. ` + "`docli search <words> --json`" + ` when you do not know the name: title matches first, then content.
   No match? The reply's ` + "`hint`" + ` lists the pages downloaded; the term may be on a page not added yet.
3. To browse, read ` + "`.docli/INDEX.md`" + `, then ` + "`.docli/<source>/INDEX.md`" + `: each line has the
   item's kind, token count and a one-line summary. Open only the file you need.
4. ` + "`docli tree <source> --json`" + ` lists folders and files with token counts.

Links inside files are relative paths to other files, so follow them with Read.

## Keep it current

- Every --json reply has a ` + "`stale`" + ` list. If a source shows ` + "`\"status\": \"stale\"`" + `,
  run ` + "`docli sync <source> --json`" + ` (or add ` + "`--latest`" + ` to the read command) before relying on it.
- ` + "`docli diff <source> --since <version|YYYY-MM-DD> --json`" + ` shows what changed.
- ` + "`docli changelog <source> --since YYYY-MM-DD --json`" + ` lists the source's own dated changes.

## Add a source

1. ` + "`docli inspect <url> --json`" + ` previews the split (files, largest file, coverage) and writes nothing.
   If it returns ` + "`suggestions`" + `, the URL was a landing page: inspect a suggested URL instead
   (a whole-docs file like ` + "`/docs.md`" + ` or ` + "`/llms-full.txt`" + ` is best). Pages built by JavaScript
   need ` + "`--render browser`" + ` if auto does not pick it.
2. If the split looks wrong, retry with ` + "`--selector`, `--drop`, `--file-level`, `--max-tokens`" + `
   and ` + "`--save`" + ` to keep the rules.
3. ` + "`docli add <url> --json`" + `. A coverage failure (code ` + "`coverage`" + `) means text would be lost:
   fix the rules rather than passing --force.

## Output shape

` + "```json" + `
{"ok": true, "command": "show", "data": {...}, "stale": [...]}
{"ok": false, "command": "show", "error": {"code": "not_found", "message": "..."}, "stale": []}
` + "```" + `
Error codes: usage, not_found, ambiguous, coverage, empty, error.
`

func cmdSkill(a *app) *cobra.Command {
	var out string
	var force bool
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Write a Claude Code skill that teaches agents to use docli",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				wd, err := os.Getwd()
				if err != nil {
					return err
				}
				out = filepath.Join(wd, ".claude", "skills", "docli", "SKILL.md")
			}
			if old, err := os.ReadFile(out); err == nil && string(old) != SkillText && !force {
				return errf("usage", "%s exists and differs; pass --force to replace it", a.rel(out))
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(out, []byte(SkillText), 0o644); err != nil {
				return err
			}
			return a.emit(map[string]string{"path": out}, func(w io.Writer) {
				fmt.Fprintf(w, "%s wrote %s\n", styled(a, okS, "✓"), a.rel(out))
			})
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "file to write (default: ./.claude/skills/docli/SKILL.md)")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing, different file")
	return cmd
}

package cli

import (
	"os"
	"sync"

	"charm.land/lipgloss/v2"
)

// Styles are built only when output goes to a terminal: the background
// query talks to the terminal, so piped and --json runs never do it.
var (
	styleOnce                                    sync.Once
	isDark                                       = true
	sTitle, sDim, sWarn, sErr, sOK, sBold, sKind lipgloss.Style
	sAdded, sRemoved, sHunk                      lipgloss.Style
)

func initStyles() {
	styleOnce.Do(func() {
		if isTerminal(os.Stdout) && isTerminal(os.Stdin) {
			isDark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
		}
		ld := lipgloss.LightDark(isDark)
		accent := ld(lipgloss.Color("#5A3FD1"), lipgloss.Color("#A28BFF"))
		dim := ld(lipgloss.Color("#6B6B6B"), lipgloss.Color("#8A8A8A"))
		warn := ld(lipgloss.Color("#B35C00"), lipgloss.Color("#FFB454"))
		bad := ld(lipgloss.Color("#C0262D"), lipgloss.Color("#FF6B6B"))
		good := ld(lipgloss.Color("#1D7F3A"), lipgloss.Color("#5FD787"))
		sTitle = lipgloss.NewStyle().Bold(true).Foreground(accent)
		sDim = lipgloss.NewStyle().Foreground(dim)
		sWarn = lipgloss.NewStyle().Bold(true).Foreground(warn)
		sErr = lipgloss.NewStyle().Bold(true).Foreground(bad)
		sOK = lipgloss.NewStyle().Foreground(good)
		sBold = lipgloss.NewStyle().Bold(true)
		sKind = lipgloss.NewStyle().Foreground(accent)
		sAdded = lipgloss.NewStyle().Foreground(good)
		sRemoved = lipgloss.NewStyle().Foreground(bad)
		sHunk = lipgloss.NewStyle().Foreground(accent)
	})
}

// styled colours text for a terminal and leaves it plain otherwise.
func styled(a *app, s func() lipgloss.Style, text string) string {
	if !a.tty {
		return text
	}
	initStyles()
	return s().Render(text)
}

func stderrStyled(a *app, s func() lipgloss.Style, t string) string {
	if a.jsonOut || !isTerminal(os.Stderr) {
		return t
	}
	initStyles()
	return s().Render(t)
}

func styleErr(a *app, t string) string {
	return stderrStyled(a, func() lipgloss.Style { return sErr }, t)
}
func styleWarn(a *app, t string) string {
	return stderrStyled(a, func() lipgloss.Style { return sWarn }, t)
}
func styleDim(a *app, t string) string {
	return stderrStyled(a, func() lipgloss.Style { return sDim }, t)
}

// style getters, so styled() reads them after initStyles ran.
func titleS() lipgloss.Style { return sTitle }
func dimS() lipgloss.Style   { return sDim }
func okS() lipgloss.Style    { return sOK }
func boldS() lipgloss.Style  { return sBold }
func kindS() lipgloss.Style  { return sKind }
func addS() lipgloss.Style   { return sAdded }
func delS() lipgloss.Style   { return sRemoved }
func hunkS() lipgloss.Style  { return sHunk }
func warnS() lipgloss.Style  { return sWarn }

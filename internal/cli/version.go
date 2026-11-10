package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/morethancoder/docli/internal/update"
)

// Commit and Date are set at build time (-ldflags -X); see .goreleaser.yaml.
var (
	Commit = ""
	Date   = ""
)

// VersionInfo is the data of `docli version`.
type VersionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// buildInfo fills in what -ldflags did not: `go install ...@v1.2.3` records
// the module version, and `go build` in a git checkout records the commit.
func buildInfo() VersionInfo {
	v := VersionInfo{Version: Version, Commit: Commit, Date: Date,
		Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v.Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch {
			case s.Key == "vcs.revision" && v.Commit == "":
				v.Commit = s.Value
			case s.Key == "vcs.time" && v.Date == "":
				v.Date = s.Value
			}
		}
	}
	if len(v.Commit) > 12 {
		v.Commit = v.Commit[:12]
	}
	return v
}

func cmdVersion(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print docli's version, commit, build date and platform",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			v := buildInfo()
			return a.emit(v, func(w io.Writer) {
				fmt.Fprintf(w, "docli %s\n", v.Version)
				if v.Commit != "" {
					fmt.Fprintf(w, "  commit  %s\n", v.Commit)
				}
				if v.Date != "" {
					fmt.Fprintf(w, "  built   %s\n", v.Date)
				}
				fmt.Fprintf(w, "  go      %s %s/%s\n", v.Go, v.OS, v.Arch)
			})
		},
	}
}

// UpdateResult is the data of `docli update`.
type UpdateResult struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Newer   bool   `json:"newer"`
	Updated bool   `json:"updated"`
	Path    string `json:"path,omitempty"`
	URL     string `json:"url,omitempty"`
	Checked bool   `json:"check_only"`
}

func cmdUpdate(a *app) *cobra.Command {
	var check, force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Replace docli with the latest GitHub release (checksum-verified)",
		Long: `update looks up the latest release of github.com/` + update.Repo + `, and when it is
newer than this binary, downloads the build for this OS and CPU, checks its
SHA-256 against the release's checksums.txt, and replaces this binary.

--check only reports. A development build (version "dev") is not replaced
unless --force is given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cur := buildInfo().Version
			c := a.updater()
			ctx := context.Background()
			rel, err := c.Latest(ctx)
			if err != nil {
				return errf("error", "%v", err)
			}
			res := UpdateResult{Current: cur, Latest: rel.Tag, URL: rel.URL, Checked: check,
				Newer: update.Newer(rel.Tag, cur)}
			human := func(w io.Writer) {
				switch {
				case res.Updated:
					fmt.Fprintf(w, "%s updated docli %s → %s (%s)\n", styled(a, okS, "✓"), res.Current, res.Latest, res.Path)
				case res.Newer:
					fmt.Fprintf(w, "docli %s is available (you have %s). Run `docli update`.\n", res.Latest, res.Current)
				default:
					fmt.Fprintf(w, "%s docli %s is the latest (newest release: %s)\n", styled(a, okS, "✓"), res.Current, res.Latest)
				}
			}
			if check || (!res.Newer && !force) {
				return a.emit(res, human)
			}
			if cur == "dev" && !force {
				return errf("usage", "this is a development build; rebuild from source, or pass --force to install %s over it", rel.Tag)
			}
			bin, err := c.Download(ctx, rel)
			if err != nil {
				return errf("error", "%v", err)
			}
			exe := a.exePath
			if exe == "" {
				if exe, err = os.Executable(); err != nil {
					return errf("error", "find this binary: %v", err)
				}
			}
			if err := update.Replace(exe, bin); err != nil {
				return errf("error", "replace %s: %v", exe, err)
			}
			res.Updated, res.Path = true, exe
			return a.emit(res, human)
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "only report whether a newer release exists")
	cmd.Flags().BoolVar(&force, "force", false, "install even over a dev build or the same version")
	return cmd
}

func (a *app) updater() *update.Client {
	if a.newUpdater != nil {
		return a.newUpdater()
	}
	return update.New()
}

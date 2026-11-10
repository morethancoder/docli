// Package update finds the latest docli release on GitHub and replaces the
// running binary with it, after checking the archive's SHA-256.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is the GitHub repository releases come from.
const Repo = "morethancoder/docli"

// DefaultAPI is the GitHub API base. DOCLI_UPDATE_API overrides it (tests).
const DefaultAPI = "https://api.github.com"

// Release is the part of a GitHub release docli uses.
type Release struct {
	Tag    string  `json:"tag_name"`
	URL    string  `json:"html_url"`
	Assets []Asset `json:"assets"`
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// Client talks to the release API.
type Client struct {
	API  string
	HTTP *http.Client
	GOOS string
	ARCH string
}

// New returns a client for the real GitHub API, or DOCLI_UPDATE_API when set.
func New() *Client {
	api := os.Getenv("DOCLI_UPDATE_API")
	if api == "" {
		api = DefaultAPI
	}
	return &Client{API: strings.TrimRight(api, "/"), HTTP: &http.Client{Timeout: 2 * time.Minute},
		GOOS: runtime.GOOS, ARCH: runtime.GOARCH}
}

// Latest returns the newest published release.
func (c *Client) Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.API+"/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("no release published yet")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub releases: %s", resp.Status)
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("read release: %w", err)
	}
	if r.Tag == "" {
		return nil, errors.New("release has no tag")
	}
	return &r, nil
}

// ArchiveName is the GoReleaser archive name for a tag on this platform.
func (c *Client) ArchiveName(tag string) string {
	ext := ".tar.gz"
	if c.GOOS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("docli_%s_%s_%s%s", strings.TrimPrefix(tag, "v"), c.GOOS, c.ARCH, ext)
}

func (r *Release) asset(name string) *Asset {
	for i := range r.Assets {
		if r.Assets[i].Name == name {
			return &r.Assets[i]
		}
	}
	return nil
}

// Download fetches this platform's binary from the release and checks the
// archive against checksums.txt. It returns the binary's bytes.
func (c *Client) Download(ctx context.Context, r *Release) ([]byte, error) {
	name := c.ArchiveName(r.Tag)
	a := r.asset(name)
	if a == nil {
		return nil, fmt.Errorf("release %s has no build for %s/%s (%s)", r.Tag, c.GOOS, c.ARCH, name)
	}
	sums := r.asset("checksums.txt")
	if sums == nil {
		return nil, fmt.Errorf("release %s has no checksums.txt; refusing to install an unchecked binary", r.Tag)
	}
	sumText, err := c.get(ctx, sums.URL, 1<<20)
	if err != nil {
		return nil, err
	}
	want := ""
	sc := bufio.NewScanner(bytes.NewReader(sumText))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			want = strings.ToLower(f[0])
		}
	}
	if want == "" {
		return nil, fmt.Errorf("checksums.txt has no entry for %s", name)
	}
	archive, err := c.get(ctx, a.URL, 200<<20)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(archive)
	if got := hex.EncodeToString(sum[:]); got != want {
		return nil, fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, want)
	}
	return extract(archive, c.GOOS)
}

func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download %s: larger than %d bytes", url, limit)
	}
	return b, nil
}

// extract pulls the docli binary out of a .tar.gz or .zip archive.
func extract(archive []byte, goos string) ([]byte, error) {
	bin := "docli"
	if goos == "windows" {
		bin = "docli.exe"
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("open zip: %w", err)
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == bin {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 200<<20))
			}
		}
		return nil, fmt.Errorf("archive has no %s", bin)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("archive has no %s", bin)
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == bin {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// Replace writes bin over the executable at path atomically: it writes a
// temp file next to it, then renames it into place.
func Replace(path string, bin []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".docli-update-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()|0o111); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// A running .exe cannot be overwritten, but it can be renamed.
		old := path + ".old"
		_ = os.Remove(old)
		if err := os.Rename(path, old); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), path)
}

// Newer reports whether version a is newer than b. Both are "vX.Y.Z" with an
// optional "-pre" part; a Go pseudo-version (v0.0.0-2026...) counts as older
// than any release with the same numbers. Anything unparsable is oldest.
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka {
		return false
	}
	if !okb {
		return true
	}
	for i := 0; i < 3; i++ {
		if pa.n[i] != pb.n[i] {
			return pa.n[i] > pb.n[i]
		}
	}
	// Same numbers: a release beats a pre-release or pseudo-version.
	return pa.pre == "" && pb.pre != ""
}

type semver struct {
	n   [3]int
	pre string
}

func parse(v string) (semver, bool) {
	var s semver
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		s.pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return s, false
		}
		s.n[i] = n
	}
	return s, true
}

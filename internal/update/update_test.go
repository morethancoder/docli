package update_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/morethancoder/docli/internal/update"
	"github.com/morethancoder/docli/internal/update/updatetest"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.1.0", "dev", true},
		{"v0.1.0", "v0.0.0-20261110192923-fa2f0805cc3e", true},
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.1-0.20261111000000-abcdef", false},
		{"v0.2.0", "v0.1.9", true},
		{"v1.0.0", "v1.0.0-rc.1", true},
		{"v0.1.0", "v0.10.0", false},
		{"garbage", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := update.Newer(c.a, c.b); got != c.want {
			t.Errorf("update.Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func client(srv *httptest.Server, goos, arch string) *update.Client {
	return &update.Client{API: srv.URL, HTTP: srv.Client(), GOOS: goos, ARCH: arch}
}

func TestDownload(t *testing.T) {
	f := &updatetest.FakeRelease{Tag: "v0.1.0", GOOS: "linux", ARCH: "amd64", Binary: []byte("NEW BINARY")}
	srv := f.Server(t)
	c := client(srv, "linux", "amd64")
	rel, err := c.Latest(context.Background())
	if err != nil || rel.Tag != "v0.1.0" {
		t.Fatalf("latest: %v %v", rel, err)
	}
	bin, err := c.Download(context.Background(), rel)
	if err != nil || string(bin) != "NEW BINARY" {
		t.Fatalf("download: %q %v", bin, err)
	}
	// No build for this platform.
	if _, err := client(srv, "plan9", "arm").Download(context.Background(), rel); err == nil || !strings.Contains(err.Error(), "no build") {
		t.Errorf("want no-build error, got %v", err)
	}
}

func TestDownloadRejectsBadChecksum(t *testing.T) {
	f := &updatetest.FakeRelease{Tag: "v0.1.0", GOOS: "linux", ARCH: "amd64", Binary: []byte("EVIL"), BadSum: true}
	c := client(f.Server(t), "linux", "amd64")
	rel, _ := c.Latest(context.Background())
	if _, err := c.Download(context.Background(), rel); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "docli")
	if err := os.WriteFile(exe, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	_ = os.Symlink(exe, link)
	if err := update.Replace(link, []byte("NEW")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(exe)
	info, _ := os.Stat(exe)
	if string(b) != "NEW" || info.Mode().Perm()&0o100 == 0 {
		t.Errorf("got %q mode %v", b, info.Mode())
	}
	if l, _ := os.Lstat(link); l.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced instead of its target")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".docli-update-*"))
	if len(left) != 0 {
		t.Errorf("temp files left: %v", left)
	}
}

// Package updatetest serves a fake GitHub release for update tests.
package updatetest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/morethancoder/docli/internal/update"
)

// FakeRelease serves a GitHub-like latest release with one tar.gz build.
type FakeRelease struct {
	Tag, GOOS, ARCH string
	Binary          []byte
	BadSum          bool
}

func (f *FakeRelease) Server(t *testing.T) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte("hi"))
	_ = tw.WriteHeader(&tar.Header{Name: "docli", Mode: 0o755, Size: int64(len(f.Binary)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(f.Binary)
	_ = tw.Close()
	_ = gz.Close()
	archive := buf.Bytes()
	name := fmt.Sprintf("docli_%s_%s_%s.tar.gz", strings.TrimPrefix(f.Tag, "v"), f.GOOS, f.ARCH)
	sum := sha256.Sum256(archive)
	hexsum := hex.EncodeToString(sum[:])
	if f.BadSum {
		hexsum = strings.Repeat("0", 64)
	}
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/"+update.Repo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(update.Release{Tag: f.Tag, URL: "https://example/rel", Assets: []update.Asset{
			{Name: name, URL: srv.URL + "/dl/" + name},
			{Name: "checksums.txt", URL: srv.URL + "/dl/checksums.txt"},
		}})
	})
	mux.HandleFunc("/dl/"+name, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archive) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  other.zip\n", hexsum, name, strings.Repeat("1", 64))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

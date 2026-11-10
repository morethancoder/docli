package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/morethancoder/docli/internal/config"
)

const landing = `<!doctype html><html><head><title>Rocket</title></head><body>
<header><nav><a href="/guide">Guide</a> <a href="/reference">Reference</a> <a href="/shop">Shop</a>
<a href="https://other.example/docs">Elsewhere</a></nav></header>
<main><h1>Rocket</h1><p>The fastest way to build hypermedia apps. Small, quick and friendly.</p>
<h2>Why</h2><p>Because it is fun and it is quick to learn for everyone.</p></main></body></html>`

func site(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && !strings.HasPrefix(r.URL.Path, "/reference") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		page := landing
		if r.URL.Path != "/" {
			page = strings.Replace(landing, "<h1>Rocket</h1>", "<h1>Reference</h1>", 1)
		}
		fmt.Fprint(w, page)
	})
	mux.HandleFunc("/docs.md", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "# Docs")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func prep(t *testing.T, u string) *Loaded {
	src := &config.Source{Name: "r", URL: u, Render: "static"}
	src.Defaults()
	l, err := Prepare(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestSuggestOnLandingPage(t *testing.T) {
	srv := site(t)
	s := Suggest(context.Background(), prep(t, srv.URL+"/"))
	var urls []string
	for _, x := range s {
		urls = append(urls, strings.TrimPrefix(x.URL, srv.URL))
	}
	got := strings.Join(urls, " ")
	if got != "/docs.md /guide /reference" {
		t.Errorf("suggestions %q", got)
	}
	// A short page under a docs path is docs, not a landing page.
	if s := Suggest(context.Background(), prep(t, srv.URL+"/reference/x")); len(s) != 0 {
		t.Errorf("docs page got suggestions: %v", s)
	}
}

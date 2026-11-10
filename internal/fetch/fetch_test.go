package fetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestStaticRetriesOnce(t *testing.T) {
	RetryDelay = 10 * time.Millisecond
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# ok"))
	}))
	defer srv.Close()
	raw, err := Static(context.Background(), srv.URL)
	if err != nil || string(raw.Body) != "# ok" || hits.Load() != 2 {
		t.Fatalf("got %v %v after %d hits", raw, err, hits.Load())
	}
	// A 404 is an answer, not a hiccup: no retry.
	hits.Store(0)
	nf := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.NotFound(w, r)
	}))
	defer nf.Close()
	if _, err := Static(context.Background(), nf.URL); err == nil || hits.Load() != 1 {
		t.Fatalf("404: err %v, %d hits", err, hits.Load())
	}
}

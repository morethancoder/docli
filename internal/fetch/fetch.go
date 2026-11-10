// Package fetch downloads a source, either with a plain HTTP request or by
// loading it in headless Chrome for docs that JavaScript builds.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// Raw is a fetched source.
type Raw struct {
	URL         string
	Body        []byte
	ContentType string
	Rendered    bool // true when loaded through the browser
}

const userAgent = "github.com/morethancoder/docli/0.1 (+docs indexer)"

// MaxBody caps a download so a wrong URL can't fill the disk.
const MaxBody = 64 << 20

// Static fetches with a plain GET (http, https or file URLs).
func Static(ctx context.Context, raw string) (*Raw, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "file" {
		b, err := os.ReadFile(u.Path)
		if err != nil {
			return nil, err
		}
		ct := "text/html"
		if strings.HasSuffix(u.Path, ".md") || strings.HasSuffix(u.Path, ".txt") || strings.HasSuffix(u.Path, ".markdown") {
			ct = "text/markdown"
		}
		return &Raw{URL: raw, Body: b, ContentType: ct}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,text/markdown,text/plain;q=0.9,*/*;q=0.5")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", raw, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBody {
		return nil, fmt.Errorf("GET %s: body over %d MB", raw, MaxBody>>20)
	}
	return &Raw{URL: resp.Request.URL.String(), Body: b, ContentType: resp.Header.Get("Content-Type")}, nil
}

// ErrNoBrowser means no Chrome or Chromium was found.
var ErrNoBrowser = errors.New("no Chrome/Chromium found: install one or set DOCLI_CHROME to its path")

// FindBrowser returns a Chrome/Chromium path: DOCLI_CHROME, then the usual
// install places and PATH names.
func FindBrowser() (string, error) {
	if p := os.Getenv("DOCLI_CHROME"); p != "" {
		return p, nil
	}
	for _, p := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p, nil
		}
	}
	return "", ErrNoBrowser
}

// textLenJS measures the visible text so we can wait for it to settle.
const textLenJS = `document.body ? document.body.innerText.length : 0`

// Browser loads the page in headless Chrome, waits for the visible text to
// stop growing, and returns the rendered HTML.
func Browser(ctx context.Context, raw string) (*Raw, error) {
	exe, err := FindBrowser()
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "docli-chrome-")
	if err != nil {
		return nil, err
	}
	defer removeAllRetry(dir)

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(exe),
		chromedp.DisableGPU,
		chromedp.UserDataDir(dir),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	tabCtx, cancelTab := chromedp.NewContext(allocCtx)
	defer cancelTab()

	// First Run starts the browser: no timeout on this context (see chromedp.Run).
	if err := chromedp.Do(tabCtx, chromedp.Navigate(raw)); err != nil {
		return nil, fmt.Errorf("browser load %s: %w", raw, err)
	}
	waitCtx, cancelWait := context.WithTimeout(tabCtx, 20*time.Second)
	defer cancelWait()
	last, stable := -1, 0
	for stable < 3 { // stable for ~600ms
		n, err := chromedp.Run(waitCtx, chromedp.Evaluate[int](textLenJS))
		if err != nil {
			break // timeout: use what is there
		}
		if n == last && n > 0 {
			stable++
		} else {
			stable = 0
		}
		last = n
		select {
		case <-waitCtx.Done():
		case <-time.After(200 * time.Millisecond):
		}
		if waitCtx.Err() != nil {
			break
		}
	}
	html, err := chromedp.Run(tabCtx, chromedp.OuterHTML("html"))
	if err != nil {
		return nil, fmt.Errorf("browser read %s: %w", raw, err)
	}
	final, _ := chromedp.Run(tabCtx, chromedp.Location())
	if final == "" {
		final = raw
	}
	return &Raw{URL: final, Body: []byte(html), ContentType: "text/html", Rendered: true}, nil
}

func removeAllRetry(dir string) {
	for range 50 {
		if err := os.RemoveAll(dir); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

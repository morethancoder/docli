package config

import "testing"

func TestNameFromURL(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"https://core.telegram.org/bots/api", "telegram-bots-api"},
		{"https://docs.example.com/guide/index.html", "example-guide"},
		// Local files: the file name only, never the path to it.
		{"file:///Users/someone/Projects/x/testdata/telegram.html", "telegram"},
		{"file:///home/someone/notes/My_API.Reference.md", "my-api-reference"},
		{"file:///Users/someone/sdk-docs/README.md", "sdk-docs"},
		{"file:///Users/someone/site/index.html", "site"},
		{"file:///index.html", "index"},
	} {
		if got := NameFromURL(c.url); got != c.want {
			t.Errorf("NameFromURL(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

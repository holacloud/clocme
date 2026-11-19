package telegram

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBotTokenWithOrWithoutPrefix(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		io.Copy(io.Discard, r.Body)
	}))
	defer srv.Close()

	for _, bot := range []string{"123:ABC", "bot123:ABC"} {
		if err := New(Config{BaseURL: srv.URL, Bot: bot, Chat: "1"}).SendSync("hi"); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range paths {
		if p != "/bot123:ABC/sendMessage" {
			t.Errorf("path %s", p)
		}
	}
}

package raster

import (
	"bytes"
	"context"
	"testing"
)

func TestLRU(t *testing.T) {
	c := newLRU(2)
	c.put("a", []byte("1"))
	c.put("b", []byte("2"))
	c.get("a")
	c.put("c", []byte("3")) // evicts b, the least recently used
	if _, ok := c.get("b"); ok {
		t.Error("b should be evicted")
	}
	for _, k := range []string{"a", "c"} {
		if _, ok := c.get(k); !ok {
			t.Errorf("%s missing", k)
		}
	}
}

func TestPNG(t *testing.T) {
	r := New()
	if !r.Available() {
		if _, err := r.PNG(context.Background(), "", nil, 10, 10); err != ErrUnavailable {
			t.Fatalf("expected ErrUnavailable, got %v", err)
		}
		t.Skip("rsvg-convert not installed")
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="10"><rect width="20" height="10" fill="red"/><text x="1" y="9" font-size="8">hi</text></svg>`)
	b, err := r.PNG(context.Background(), "k", svg, 40, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("not a png")
	}
	if b2, _ := r.PNG(context.Background(), "k", nil, 40, 20); !bytes.Equal(b, b2) {
		t.Error("cache miss")
	}
}

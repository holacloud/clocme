// Package raster converts SVG to PNG for social previews (og:image), which
// most platforms do not accept as SVG. It shells out to rsvg-convert
// (librsvg), which renders our SVGs faithfully, text included; pure Go
// rasterizers do not render <text>.
package raster

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("rsvg-convert not available")

type Rasterizer struct {
	bin   string
	sem   chan struct{} // limits concurrent conversions
	cache *lru
}

// New finds rsvg-convert in PATH. A Rasterizer without the binary is valid:
// Available reports false and PNG returns ErrUnavailable.
func New() *Rasterizer {
	bin, _ := exec.LookPath("rsvg-convert")
	return &Rasterizer{bin: bin, sem: make(chan struct{}, 2), cache: newLRU(128)}
}

func (r *Rasterizer) Available() bool { return r != nil && r.bin != "" }

// PNG converts svg to a PNG of the given size. When key is not empty the
// result is cached under it (use something that changes with the content).
func (r *Rasterizer) PNG(ctx context.Context, key string, svg []byte, width, height int) ([]byte, error) {
	if !r.Available() {
		return nil, ErrUnavailable
	}
	if key != "" {
		if b, ok := r.cache.get(key); ok {
			return b, nil
		}
	}

	select {
	case r.sem <- struct{}{}:
		defer func() { <-r.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.bin, "-f", "png", "-w", strconv.Itoa(width), "-h", strconv.Itoa(height))
	cmd.Stdin = bytes.NewReader(svg)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("rsvg-convert: %w: %s", err, stderr.String())
	}
	b := out.Bytes()
	if key != "" {
		r.cache.put(key, b)
	}
	return b, nil
}

// lru is a tiny least-recently-used cache of rendered images.
type lru struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[string]*list.Element
}

type lruEntry struct {
	key string
	val []byte
}

func newLRU(max int) *lru {
	return &lru{max: max, order: list.New(), items: map[string]*list.Element{}}
}

func (c *lru) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.order.MoveToFront(e)
		return e.Value.(*lruEntry).val, true
	}
	return nil, false
}

func (c *lru) put(key string, val []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		e.Value.(*lruEntry).val = val
		c.order.MoveToFront(e)
		return
	}
	c.items[key] = c.order.PushFront(&lruEntry{key, val})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*lruEntry).key)
	}
}

package cache_test

import (
	"testing"
	"time"

	"currency_ta_go/internal/cache"
)

func TestCache_TTL(t *testing.T) {
	c := cache.New[string, int](50 * time.Millisecond)

	c.Set("a", 42)
	if v, ok := c.Get("a"); !ok || v != 42 {
		t.Fatalf("expected 42")
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatalf("expected expired")
	}
}

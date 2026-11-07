package lua

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatCache(t *testing.T) {
	defer func(ttl time.Duration) { StatCacheTTL = ttl }(StatCacheTTL)
	StatCacheTTL = 50 * time.Millisecond
	path := filepath.Join(t.TempDir(), "m.lua")
	if _, err := statFile(path); err == nil {
		t.Fatal("missing file found")
	}
	os.WriteFile(path, []byte("return 1"), 0o644)
	if _, err := statFile(path); err == nil {
		t.Fatal("a cached miss should be reused within the ttl")
	}
	time.Sleep(60 * time.Millisecond)
	if _, err := statFile(path); err != nil {
		t.Fatal("not found after the ttl:", err)
	}
}

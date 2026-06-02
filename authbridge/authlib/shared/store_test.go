package shared

import (
	"sync"
	"testing"
	"time"
)

func TestStore_PutGet(t *testing.T) {
	s := New()
	s.Put("k", "v", time.Minute)
	got, ok := s.Get("k")
	if !ok || got.(string) != "v" {
		t.Fatalf("Get = %v, %v; want v, true", got, ok)
	}
}

func TestStore_GetMissing(t *testing.T) {
	s := New()
	if _, ok := s.Get("nope"); ok {
		t.Fatal("expected miss")
	}
}

func TestStore_Expiry(t *testing.T) {
	s := New()
	now := time.Unix(1000, 0)
	s.now = func() time.Time { return now }
	s.Put("k", "v", time.Minute)
	now = now.Add(30 * time.Second)
	if _, ok := s.Get("k"); !ok {
		t.Fatal("should still be live at 30s")
	}
	now = now.Add(31 * time.Second)
	if _, ok := s.Get("k"); ok {
		t.Fatal("should be expired past 60s")
	}
}

func TestStore_Delete(t *testing.T) {
	s := New()
	s.Put("k", "v", time.Minute)
	s.Delete("k")
	if _, ok := s.Get("k"); ok {
		t.Fatal("expected deleted")
	}
}

func TestStore_ConcurrentAccess(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := string(rune('a' + n%26))
			s.Put(key, n, time.Minute)
			s.Get(key)
		}(i)
	}
	wg.Wait()
}

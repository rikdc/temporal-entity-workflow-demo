package workflow

import (
	"testing"
	"time"
)

func TestIdempotencyStore_Exists(t *testing.T) {
	s := newIdempotencyStore()
	s.Push("key-1", time.Now())

	if !s.Exists("key-1") {
		t.Error("expected key-1 to exist")
	}
	if s.Exists("key-2") {
		t.Error("expected key-2 to not exist")
	}
}

func TestIdempotencyStore_Push_DuplicateDoesNotGrowOrdered(t *testing.T) {
	s := newIdempotencyStore()
	now := time.Now()

	s.Push("key-1", now)
	s.Push("key-1", now.Add(time.Hour))

	if len(s.Ordered) != 1 {
		t.Errorf("expected len(Ordered) = 1, got %d", len(s.Ordered))
	}
}

func TestIdempotencyStore_Evict_RemovesExpiredKey(t *testing.T) {
	s := newIdempotencyStore()

	s.Push("old-key", time.Now())

	// evict from 4 days in the future with a 3-day window
	s.Evict(3, time.Now().AddDate(0, 0, 4))

	if s.Exists("old-key") {
		t.Error("expected old-key to be evicted")
	}
	if len(s.Ordered) != 0 {
		t.Errorf("expected len(Ordered) = 0, got %d", len(s.Ordered))
	}
}

func TestIdempotencyStore_Evict_KeepsUnexpiredKey(t *testing.T) {
	s := newIdempotencyStore()

	s.Push("new-key", time.Now())

	// evict from 2 days in the future with a 3-day window — key is only 2 days old
	s.Evict(3, time.Now().AddDate(0, 0, 2))

	if !s.Exists("new-key") {
		t.Error("expected new-key to still exist")
	}
	if len(s.Ordered) != 1 {
		t.Errorf("expected len(Ordered) = 1, got %d", len(s.Ordered))
	}
}

func TestIdempotencyStore_Evict_MixedKeys(t *testing.T) {
	s := newIdempotencyStore()
	now := time.Now()

	s.Push("old-key", now.AddDate(0, 0, -5)) // 5 days ago
	s.Push("new-key", now)                   // just now

	// evict from now with a 3-day window
	s.Evict(3, now)

	if s.Exists("old-key") {
		t.Error("expected old-key to be evicted")
	}
	if !s.Exists("new-key") {
		t.Error("expected new-key to still exist")
	}
	if len(s.Ordered) != 1 {
		t.Errorf("expected len(Ordered) = 1, got %d", len(s.Ordered))
	}
	if s.Ordered[0] != "new-key" {
		t.Errorf("expected Ordered[0] = new-key, got %s", s.Ordered[0])
	}
}

func TestIdempotencyStore_Evict_EmptyStore(t *testing.T) {
	s := newIdempotencyStore()

	// should not panic
	s.Evict(3, time.Now())

	if len(s.Ordered) != 0 {
		t.Errorf("expected len(Ordered) = 0, got %d", len(s.Ordered))
	}
}

func TestIdempotencyStore_Evict_AllKeysExpired(t *testing.T) {
	s := newIdempotencyStore()
	now := time.Now()

	s.Push("key-1", now.AddDate(0, 0, -10))
	s.Push("key-2", now.AddDate(0, 0, -8))
	s.Push("key-3", now.AddDate(0, 0, -5))

	s.Evict(3, now)

	if len(s.Keys) != 0 {
		t.Errorf("expected all keys evicted, got %d remaining", len(s.Keys))
	}
	if len(s.Ordered) != 0 {
		t.Errorf("expected Ordered to be empty, got %d", len(s.Ordered))
	}
}

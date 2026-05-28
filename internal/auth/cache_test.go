package auth

import (
	"testing"
	"time"
)

func TestVerifyCache_HitsOnce(t *testing.T) {
	callCount := 0
	mock := func(token string) (string, []string, bool, error) {
		callCount++
		return "alice", []string{"read"}, true, nil
	}

	c := NewVerifyCache(mock, 5*time.Minute)

	name, scopes, ok, err := c.Verify("tok")
	if err != nil || !ok || name != "alice" || len(scopes) != 1 {
		t.Fatalf("first Verify unexpected: name=%q ok=%v err=%v", name, ok, err)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 call after first Verify, got %d", callCount)
	}

	_, _, _, _ = c.Verify("tok")
	if callCount != 1 {
		t.Errorf("expected cache hit (still 1 call), got %d", callCount)
	}
}

func TestVerifyCache_ExpiredEntryRefetched(t *testing.T) {
	callCount := 0
	mock := func(token string) (string, []string, bool, error) {
		callCount++
		return "alice", []string{"read"}, true, nil
	}

	c := NewVerifyCache(mock, -1*time.Second) // already-expired TTL

	_, _, _, _ = c.Verify("tok")
	_, _, _, _ = c.Verify("tok")
	if callCount != 2 {
		t.Errorf("expected 2 calls after TTL expiry, got %d", callCount)
	}
}

func TestVerifyCache_FailureNotCached(t *testing.T) {
	callCount := 0
	mock := func(token string) (string, []string, bool, error) {
		callCount++
		return "", nil, false, nil
	}

	c := NewVerifyCache(mock, 5*time.Minute)

	_, _, _, _ = c.Verify("bad")
	_, _, _, _ = c.Verify("bad")
	if callCount != 2 {
		t.Errorf("expected failed results not to be cached (2 calls), got %d", callCount)
	}
}

func TestVerifyCache_DifferentTokensIndependent(t *testing.T) {
	callCount := 0
	mock := func(token string) (string, []string, bool, error) {
		callCount++
		return token, []string{"read"}, true, nil
	}

	c := NewVerifyCache(mock, 5*time.Minute)

	_, _, _, _ = c.Verify("tok-a")
	_, _, _, _ = c.Verify("tok-b")
	_, _, _, _ = c.Verify("tok-a") // cache hit
	_, _, _, _ = c.Verify("tok-b") // cache hit
	if callCount != 2 {
		t.Errorf("expected 2 calls for 2 distinct tokens, got %d", callCount)
	}
}

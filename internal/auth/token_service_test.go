package auth

import (
	"testing"
	"time"
)

func newTestTokenService(t *testing.T) *TokenService {
	t.Helper()
	ts, err := NewTokenService(t.TempDir(), "distroface", "distroface-registry", nil)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func (ts *TokenService) cachedSubjects() int {
	ts.subjects.mu.Lock()
	defer ts.subjects.mu.Unlock()
	return len(ts.subjects.entries)
}

func TestVerifyTokenSubjectVerifiesOncePerToken(t *testing.T) {
	ts := newTestTokenService(t)
	tok, err := ts.SignTokenAudience("distroface-registry", time.Hour, "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		sub, err := ts.VerifyTokenSubject(tok)
		if err != nil || sub != "alice" {
			t.Fatalf("VerifyTokenSubject = %q, %v", sub, err)
		}
	}
	if ts.cachedSubjects() != 1 {
		t.Errorf("cached %d subjects, want 1", ts.cachedSubjects())
	}

	// Forged and expired tokens are refused and never cached
	forged, err := newTestTokenService(t).SignTokenAudience("distroface-registry", time.Hour, "mallory", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.VerifyTokenSubject(forged); err == nil {
		t.Error("token signed by another key accepted")
	}
	expired, err := ts.SignTokenAudience("distroface-registry", -time.Minute, "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts.VerifyTokenSubject(expired); err == nil {
		t.Error("expired token accepted")
	}
	if _, err := ts.VerifyTokenSubject("not.a.jwt"); err == nil {
		t.Error("garbage accepted")
	}
	if ts.cachedSubjects() != 1 {
		t.Errorf("refused tokens were cached, %d entries", ts.cachedSubjects())
	}

	// A cached entry dies with the token and is verified again
	ts.subjects.mu.Lock()
	for k, entry := range ts.subjects.entries {
		entry.expires = time.Now().Add(-time.Second)
		ts.subjects.entries[k] = entry
	}
	ts.subjects.mu.Unlock()
	if sub, err := ts.VerifyTokenSubject(tok); err != nil || sub != "alice" {
		t.Fatalf("re-verify = %q, %v", sub, err)
	}
	ts.subjects.mu.Lock()
	for _, entry := range ts.subjects.entries {
		if !entry.expires.After(time.Now()) {
			t.Error("re-verified entry kept the stale expiry")
		}
	}
	ts.subjects.mu.Unlock()
}

func TestJobFromTokenUsesVerifiedSubject(t *testing.T) {
	ts := newTestTokenService(t)
	job, err := ts.SignTokenAudience("distroface-registry", time.Hour, JobSubject("job-1", "runner"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := ts.JobFromToken(job); !ok || id != "job-1" {
		t.Errorf("JobFromToken(job) = %q, %v", id, ok)
	}
	plain, err := ts.SignTokenAudience("distroface-registry", time.Hour, "alice", nil)
	if err != nil {
		t.Fatal(err)
	}
	if id, ok := ts.JobFromToken(plain); ok || id != "" {
		t.Errorf("JobFromToken(plain) = %q, %v", id, ok)
	}
	forgedJob, err := newTestTokenService(t).SignTokenAudience("distroface-registry", time.Hour, JobSubject("job-2", "runner"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := ts.JobFromToken(forgedJob); ok {
		t.Error("forged job token accepted")
	}
	if _, ok := ts.JobFromToken("garbage"); ok {
		t.Error("garbage accepted as a job token")
	}
}

func TestSubjectCacheStopsGrowingWhenFull(t *testing.T) {
	c := &subjectCache{}
	now := time.Now()
	for i := range subjectCacheMax {
		var key [32]byte
		key[0], key[1], key[2] = byte(i), byte(i>>8), byte(i>>16)
		c.put(key, "s", now.Add(time.Hour), now)
	}
	var extra [32]byte
	extra[31] = 1
	c.put(extra, "extra", now.Add(time.Hour), now)
	if _, ok := c.get(extra, now); ok {
		t.Error("full cache accepted another entry")
	}
	// Expired entries make room once the sweep interval has passed
	c.mu.Lock()
	for k, entry := range c.entries {
		entry.expires = now.Add(-time.Second)
		c.entries[k] = entry
	}
	c.mu.Unlock()
	c.put(extra, "extra", now.Add(time.Hour), now.Add(cacheSweepEvery))
	if sub, ok := c.get(extra, now); !ok || sub != "extra" {
		t.Errorf("after sweep get = %q, %v", sub, ok)
	}
	if len(c.entries) != 1 {
		t.Errorf("sweep left %d entries", len(c.entries))
	}
}

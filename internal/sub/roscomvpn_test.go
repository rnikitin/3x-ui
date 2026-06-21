package sub

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func withRoscomVPNTestState(t *testing.T, sourceURL string) {
	t.Helper()
	oldURLs := roscomvpnSourceURLs
	oldClient := roscomvpnClient
	roscomvpnSourceURLs = map[string]string{
		RoscomVPNSourceDefault: sourceURL,
	}
	roscomvpnClient = &http.Client{Timeout: time.Second}
	roscomvpnMu.Lock()
	roscomvpnCache = map[string]roscomvpnCacheEntry{}
	roscomvpnMu.Unlock()
	roscomvpnFetchLocks = sync.Map{}

	t.Cleanup(func() {
		roscomvpnSourceURLs = oldURLs
		roscomvpnClient = oldClient
		roscomvpnMu.Lock()
		roscomvpnCache = map[string]roscomvpnCacheEntry{}
		roscomvpnMu.Unlock()
		roscomvpnFetchLocks = sync.Map{}
	})
}

func TestResolveRoutingRulesCustomSource(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withRoscomVPNTestState(t, srv.URL)

	got := ResolveRoutingRules(RoscomVPNSourceCustom, "happ://routing/add/custom")
	if got != "happ://routing/add/custom" {
		t.Fatalf("ResolveRoutingRules(custom) = %q", got)
	}
	if hits.Load() != 0 {
		t.Fatalf("custom source made %d HTTP requests", hits.Load())
	}
}

func TestResolveRoutingRulesFetchesAndCaches(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(" happ://routing/onadd/default \n"))
	}))
	defer srv.Close()
	withRoscomVPNTestState(t, srv.URL)

	for i := 0; i < 2; i++ {
		got := ResolveRoutingRules(RoscomVPNSourceDefault, "fallback")
		if got != "happ://routing/onadd/default" {
			t.Fatalf("ResolveRoutingRules(default) = %q", got)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one HTTP request, got %d", hits.Load())
	}
}

func TestResolveRoutingRulesFallsBackToStaleOnFetchFailure(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("happ://routing/onadd/stale"))
	}))
	defer srv.Close()
	withRoscomVPNTestState(t, srv.URL)

	if got := ResolveRoutingRules(RoscomVPNSourceDefault, "fallback"); got != "happ://routing/onadd/stale" {
		t.Fatalf("initial ResolveRoutingRules(default) = %q", got)
	}

	roscomvpnMu.Lock()
	entry := roscomvpnCache[RoscomVPNSourceDefault]
	entry.fetchedAt = time.Now().Add(-roscomvpnCacheTTL - time.Second)
	roscomvpnCache[RoscomVPNSourceDefault] = entry
	roscomvpnMu.Unlock()
	fail.Store(true)

	if got := ResolveRoutingRules(RoscomVPNSourceDefault, "fallback"); got != "happ://routing/onadd/stale" {
		t.Fatalf("stale ResolveRoutingRules(default) = %q", got)
	}
}

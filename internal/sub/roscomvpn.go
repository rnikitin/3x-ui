package sub

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	RoscomVPNSourceDefault   = "default"
	RoscomVPNSourceJsonSub   = "jsonsub"
	RoscomVPNSourceWhitelist = "whitelist"
	RoscomVPNSourceCustom    = "custom"

	roscomvpnCacheTTL      = 10 * time.Minute
	roscomvpnNegativeCache = 30 * time.Second
	roscomvpnHTTPTimeout   = 4 * time.Second
	roscomvpnMaxBodyBytes  = 1 << 20
)

var roscomvpnSourceURLs = map[string]string{
	RoscomVPNSourceDefault:   "https://raw.githubusercontent.com/hydraponique/roscomvpn-routing/main/HAPP/DEFAULT.DEEPLINK",
	RoscomVPNSourceJsonSub:   "https://raw.githubusercontent.com/hydraponique/roscomvpn-routing/main/HAPP/JSONSUB.DEEPLINK",
	RoscomVPNSourceWhitelist: "https://raw.githubusercontent.com/hydraponique/roscomvpn-routing/main/HAPP/WHITELIST.DEEPLINK",
}

type roscomvpnCacheEntry struct {
	value     string
	fetchedAt time.Time
	lastFail  time.Time
}

var (
	roscomvpnMu         sync.RWMutex
	roscomvpnCache      = map[string]roscomvpnCacheEntry{}
	roscomvpnClient     = &http.Client{Timeout: roscomvpnHTTPTimeout}
	roscomvpnFetchLocks sync.Map
)

func roscomvpnLockFor(src string) *sync.Mutex {
	if m, ok := roscomvpnFetchLocks.Load(src); ok {
		return m.(*sync.Mutex)
	}
	m, _ := roscomvpnFetchLocks.LoadOrStore(src, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func ResolveRoutingRules(source, custom string) string {
	src := strings.ToLower(strings.TrimSpace(source))
	if src == "" {
		src = RoscomVPNSourceDefault
	}
	if src == RoscomVPNSourceCustom {
		return custom
	}
	url, ok := roscomvpnSourceURLs[src]
	if !ok {
		return custom
	}

	roscomvpnMu.RLock()
	entry, hit := roscomvpnCache[src]
	roscomvpnMu.RUnlock()
	if hit && time.Since(entry.fetchedAt) < roscomvpnCacheTTL {
		return entry.value
	}
	if hit && !entry.lastFail.IsZero() && time.Since(entry.lastFail) < roscomvpnNegativeCache {
		if entry.value != "" {
			return entry.value
		}
		return custom
	}

	mu := roscomvpnLockFor(src)
	mu.Lock()
	defer mu.Unlock()

	roscomvpnMu.RLock()
	entry, hit = roscomvpnCache[src]
	roscomvpnMu.RUnlock()
	if hit && time.Since(entry.fetchedAt) < roscomvpnCacheTTL {
		return entry.value
	}

	if v, err := fetchRoscomVPNDeepLink(url); err == nil {
		roscomvpnMu.Lock()
		roscomvpnCache[src] = roscomvpnCacheEntry{value: v, fetchedAt: time.Now()}
		roscomvpnMu.Unlock()
		return v
	}

	roscomvpnMu.Lock()
	prev := roscomvpnCache[src]
	prev.lastFail = time.Now()
	roscomvpnCache[src] = prev
	roscomvpnMu.Unlock()

	if hit && entry.value != "" {
		return entry.value
	}
	return custom
}

func fetchRoscomVPNDeepLink(url string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := roscomvpnClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("roscomvpn deeplink fetch failed: HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, roscomvpnMaxBodyBytes))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

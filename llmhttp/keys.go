package llmhttp

import (
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// APIKeys preserves legacy single keys and accepts one key per line.
func APIKeys(value string) []string {
	var keys []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(value, "\n") {
		key := strings.TrimSpace(line)
		if key != "" && !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	return keys
}

var keyCursors sync.Map

// NextAPIKey shares a cursor across provider rebuilds and concurrent callers.
func NextAPIKey(value string) string {
	keys := APIKeys(value)
	if len(keys) == 0 {
		return ""
	}
	if len(keys) == 1 {
		return keys[0]
	}
	cursor, _ := keyCursors.LoadOrStore(strings.Join(keys, "\n"), &atomic.Uint64{})
	return keys[(cursor.(*atomic.Uint64).Add(1)-1)%uint64(len(keys))]
}

type keyRotationTransport struct {
	base http.RoundTripper
	keys string
}

func RotateAPIKeys(base http.RoundTripper, keys string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return keyRotationTransport{base: base, keys: keys}
}

func (t keyRotationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	key := NextAPIKey(t.keys)
	if req.Header.Get("x-api-key") != "" {
		req.Header.Set("x-api-key", key)
	}
	if req.Header.Get("Authorization") != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return t.base.RoundTrip(req)
}

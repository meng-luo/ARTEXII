package llmhttp

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestAPIKeysNormalizeAndDeduplicate(t *testing.T) {
	if got := APIKeys(" key-a \r\n\nkey-b\nkey-a\n "); !reflect.DeepEqual(got, []string{"key-a", "key-b"}) {
		t.Fatalf("keys = %v", got)
	}
}

type keyRoundTripper func(*http.Request) (*http.Response, error)

func (f keyRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestKeyRotationConcurrentAndPreservesOriginalRequest(t *testing.T) {
	var mu sync.Mutex
	counts := map[string]int{}
	base := keyRoundTripper(func(req *http.Request) (*http.Response, error) {
		key := req.Header.Get("x-api-key")
		if req.Header.Get("Authorization") != "Bearer "+key {
			t.Error("authorization headers disagree")
		}
		mu.Lock()
		counts[key]++
		mu.Unlock()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	keys := t.Name() + "-a\n" + t.Name() + "-b\n" + t.Name() + "-c"
	req, _ := http.NewRequest(http.MethodPost, "http://example.test", nil)
	req.Header.Set("x-api-key", "original")
	req.Header.Set("Authorization", "Bearer original")
	var wg sync.WaitGroup
	for range 90 {
		wg.Go(func() {
			// Rebuilding transports must not reset their shared rotation cursor.
			resp, err := RotateAPIKeys(base, keys).RoundTrip(req)
			if err != nil {
				t.Error(err)
				return
			}
			resp.Body.Close()
		})
	}
	wg.Wait()
	if len(counts) != 3 {
		t.Fatalf("keys used = %v", counts)
	}
	for key, count := range counts {
		if count != 30 {
			t.Errorf("%s: calls=%d, want 30", key, count)
		}
	}
	if req.Header.Get("x-api-key") != "original" || req.Header.Get("Authorization") != "Bearer original" {
		t.Fatal("mutated original request headers")
	}
}

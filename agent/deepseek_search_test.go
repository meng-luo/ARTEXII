package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type deepSeekRoundTripper func(*http.Request) (*http.Response, error)

func (f deepSeekRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDeepSeekSearchToolUsesConfiguredKeyAndQuery(t *testing.T) {
	client := &http.Client{Transport: deepSeekRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != deepSeekToolsURL+"search" {
			t.Errorf("request URL = %q", r.URL)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"count":3,"query":"ARTEX"}` {
			t.Errorf("request body = %s", body)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"results":[{"title":"ARTEX","url":"https://example.test"}]}`)), Header: make(http.Header)}, nil
	})}
	tool := deepSeekTool("deepseek_search", "", deepSeekToolsURL+"search", "test-key", client, map[string]any{
		"query": map[string]any{"type": "string"}, "count": map[string]any{"type": "integer"},
	}, "query")
	result, err := tool.Call(context.Background(), []byte(`{"query":"ARTEX","count":3}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError || !strings.Contains(result.Flatten(), `"results"`) {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestDeepSeekCustomBaseURLForToolsAndProbe(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer custom-key" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		paths = append(paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"results":[{"title":"test"}]}`)
	}))
	defer srv.Close()
	opts := WebSearchOpts{Enabled: true, Backend: "deepseek", DeepSeekAPIKey: "custom-key", DeepSeekBaseURL: srv.URL + "/gateway/v1"}
	tools := deepSeekTools(opts)
	if len(tools) != 2 {
		t.Fatalf("tools = %d", len(tools))
	}
	for _, tool := range tools {
		result, err := tool.Call(context.Background(), []byte(`{"query":"test","url":"https://example.test"}`), nil)
		if err != nil || result.IsError {
			t.Fatalf("tool call failed: %+v %v", result, err)
		}
	}
	if count, err := DeepSeekSearchProbe(context.Background(), opts.DeepSeekBaseURL, opts.DeepSeekAPIKey, "", "test", 3); err != nil || count != 1 {
		t.Fatalf("probe: count=%d err=%v", count, err)
	}
	if strings.Join(paths, ",") != "/gateway/v1/tools/search,/gateway/v1/tools/fetch,/gateway/v1/tools/search" {
		t.Fatalf("request paths = %v", paths)
	}
}

func TestDeepSeekBaseURLNormalization(t *testing.T) {
	for _, base := range []string{"https://example.test", "https://example.test/v1/", "https://example.test/v1/tools/"} {
		got, err := DeepSeekToolsBaseURL(base)
		if err != nil || got != "https://example.test/v1/tools/" {
			t.Fatalf("base=%q got=%q err=%v", base, got, err)
		}
	}
	for _, base := range []string{"ftp://example.test", "invalid", "https://example.test/?query=1", "https://user:pass@example.test"} {
		if _, err := DeepSeekToolsBaseURL(base); err == nil {
			t.Fatalf("invalid base accepted: %q", base)
		}
	}
	got, err := DeepSeekToolsBaseURL("")
	if err != nil || got != deepSeekToolsURL {
		t.Fatalf("default base=%q err=%v", got, err)
	}
}

func TestDeepSeekToolsRespectGlobalAndBackendSettings(t *testing.T) {
	for _, opts := range []WebSearchOpts{
		{Enabled: false, Backend: "deepseek", DeepSeekAPIKey: "key"},
		{Enabled: true, Backend: "ddgs", DeepSeekAPIKey: "key"},
		{Enabled: true, Backend: "deepseek"},
	} {
		if got := deepSeekTools(opts); len(got) != 0 {
			t.Fatalf("deepseek tools enabled for opts %+v", opts)
		}
	}
}

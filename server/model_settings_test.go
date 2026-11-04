package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProfileAPIAppendsAndReplacesMultipleKeys(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	var id int64
	defer func() {
		if id > 0 {
			m.pg.DeleteProfile(id)
		}
	}()
	save := func(keys []string, appendKeys bool) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"id": id, "name": "multiple-key-api-test", "format": "openai", "model": "test",
			"api_keys": keys, "append_api_keys": appendKeys,
		})
		rec := httptest.NewRecorder()
		s.pgSaveProfile(rec, httptest.NewRequest(http.MethodPost, "/api/llm/profiles", strings.NewReader(string(body))))
		if rec.Code != http.StatusOK {
			t.Fatalf("save: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var response struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		id = response.ID
	}
	check := func(want string) {
		t.Helper()
		profile, err := m.pg.ProfileByID(id)
		if err != nil || profile == nil || profile.APIKey != want {
			t.Fatalf("stored keys mismatch: profile=%+v err=%v", profile, err)
		}
	}
	save([]string{"test-key-first", "test-key-second"}, false)
	check("test-key-first\ntest-key-second")
	save([]string{"test-key-third", "test-key-first"}, true)
	check("test-key-first\ntest-key-second\ntest-key-third")
	save([]string{}, false)
	check("test-key-first\ntest-key-second\ntest-key-third")
	save([]string{"test-key-replacement"}, false)
	check("test-key-replacement")
}

func TestDeepSeekSettingsPersistAndTestUnsavedBaseURL(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable: %v", err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, m, t.TempDir(), t.TempDir(), t.TempDir())
	oldBase := m.deepSeekSearchBaseURL()
	defer m.pg.SetSetting(settingDeepSeekSearchBaseURL, oldBase)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/custom/v1/tools/search" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected search request: %s", r.URL.Path)
		}
		io.WriteString(w, `{"results":[{"title":"test"}]}`)
	}))
	defer upstream.Close()
	body, _ := json.Marshal(map[string]any{"deepseek_search_base_url": upstream.URL + "/custom/v1"})
	rec := httptest.NewRecorder()
	s.putSettings(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(string(body))))
	if rec.Code != http.StatusOK || m.deepSeekSearchBaseURL() != upstream.URL+"/custom/v1" {
		t.Fatalf("save base: status=%d body=%s", rec.Code, rec.Body.String())
	}
	probe := func(base *string) {
		t.Helper()
		input := map[string]any{"web_search_backend": "deepseek", "deepseek_search_api_key": "test-key"}
		if base != nil {
			input["deepseek_search_base_url"] = *base
		}
		body, _ := json.Marshal(input)
		rec := httptest.NewRecorder()
		s.testWebSearch(rec, httptest.NewRequest(http.MethodPost, "/api/settings/web-search/test", strings.NewReader(string(body))))
		var result struct {
			OK bool `json:"ok"`
		}
		json.Unmarshal(rec.Body.Bytes(), &result)
		if rec.Code != http.StatusOK || !result.OK {
			t.Fatalf("probe: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	probe(nil)
	if err := m.pg.SetSetting(settingDeepSeekSearchBaseURL, "http://unused.invalid"); err != nil {
		t.Fatal(err)
	}
	draft := upstream.URL + "/custom"
	probe(&draft)
	rec = httptest.NewRecorder()
	s.putSettings(rec, httptest.NewRequest(http.MethodPut, "/api/settings", strings.NewReader(`{"deepseek_search_base_url":"invalid"}`)))
	if rec.Code != http.StatusBadRequest || m.deepSeekSearchBaseURL() != "http://unused.invalid" {
		t.Fatalf("invalid base changed settings: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

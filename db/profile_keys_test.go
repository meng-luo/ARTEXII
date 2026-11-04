package db

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProfileMultipleKeysPersistWithoutLeaking(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	p := &LLMProfile{Name: "multi-key-test", Format: "openai", Model: "test",
		APIKey: " secret-key-first \r\nsecret-key-second\nsecret-key-first\n\n"}
	id, err := d.SaveProfile(p)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Exec(`DELETE FROM llm_profiles WHERE id=$1`, id)
	got, err := d.ProfileByID(id)
	if err != nil || got.APIKey != "secret-key-first\nsecret-key-second" {
		t.Fatalf("keys not normalized: %+v err=%v", got, err)
	}
	got.APIKey = ""
	if _, err := d.SaveProfile(got); err != nil {
		t.Fatal(err)
	}
	got, err = d.ProfileByID(id)
	if err != nil || got.APIKey != "secret-key-first\nsecret-key-second" {
		t.Fatalf("blank update lost keys: %+v err=%v", got, err)
	}
	profiles, err := d.ListProfiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range profiles {
		if profile.ID != id {
			continue
		}
		raw, err := json.Marshal(profile)
		if err != nil || strings.Contains(string(raw), "secret-key") || profile.APIKeyHint != "…irst, …cond" {
			t.Fatalf("profile response leaks keys or has wrong hint: %s err=%v", raw, err)
		}
	}
}

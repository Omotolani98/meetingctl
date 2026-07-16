package catalog

import (
	"encoding/json"
	"testing"
)

func TestCuratedSupportedIncludesOpenAIAndSubscription(t *testing.T) {
	items := curatedSupported()
	if len(items) != 2 {
		t.Fatalf("len=%d", len(items))
	}
	if items[0].ID != "openai" || !items[0].Supported || items[0].Strategy != StrategyAPIKey {
		t.Fatalf("openai entry: %+v", items[0])
	}
	if items[1].ID != "chatgpt-subscription" || !items[1].Supported || items[1].Strategy != StrategySubscription {
		t.Fatalf("subscription entry: %+v", items[1])
	}
}

func TestParseModelsDev(t *testing.T) {
	raw := map[string]any{
		"anthropic": map[string]any{
			"id": "anthropic",
			"name": "Anthropic",
			"env": []string{"ANTHROPIC_API_KEY"},
			"api": "https://api.anthropic.com/v1",
			"doc": "https://docs.anthropic.com",
			"models": map[string]any{"claude": map[string]any{}},
		},
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	items, err := parseModelsDev(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len=%d", len(items))
	}
	if items[0].ID != "anthropic" || items[0].ModelCount != 1 || items[0].CredentialEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("item: %+v", items[0])
	}
}

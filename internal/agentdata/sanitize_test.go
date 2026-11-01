package agentdata

import "testing"

func TestSanitizeRedactsStructuredSecretsWithoutCorruptingOrdinaryFields(t *testing.T) {
	input := map[string]any{
		"access_token": "top-secret",
		"token_count":  42,
		"nested": map[string]any{
			"client-secret": "client-secret-value",
			"name":          "nginx",
		},
		"items": []any{
			map[string]any{"password": "pw", "image": "nginx:1.29"},
		},
		"json_text": `{"access_token":"nested-secret","target":"old-value"}`,
	}

	got := Sanitize(input).(map[string]any)
	if got["access_token"] != Redacted {
		t.Fatalf("access_token=%v, want redacted", got["access_token"])
	}
	if got["token_count"] != 42 {
		t.Fatalf("token_count=%v, want preserved", got["token_count"])
	}
	nested := got["nested"].(map[string]any)
	if nested["client-secret"] != Redacted || nested["name"] != "nginx" {
		t.Fatalf("nested=%#v", nested)
	}
	item := got["items"].([]any)[0].(map[string]any)
	if item["password"] != Redacted || item["image"] != "nginx:1.29" {
		t.Fatalf("item=%#v", item)
	}
	if got["json_text"] != `{"access_token":"[REDACTED]","target":"old-value"}` {
		t.Fatalf("json_text=%q", got["json_text"])
	}

	// Sanitization must not mutate the caller's data because the live ADK event
	// may still be consumed by the current invocation.
	if input["access_token"] != "top-secret" {
		t.Fatalf("input was mutated: %#v", input)
	}
}

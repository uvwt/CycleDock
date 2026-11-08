package agentdata

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

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

func TestNormalizeTextPreservesValidUnicodeAndReplacesNULAndInvalidUTF8(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		want        string
		wantChanged bool
	}{
		{
			name:        "ordinary ASCII",
			input:       "docker ps -a --format '{{.Names}}'",
			want:        "docker ps -a --format '{{.Names}}'",
			wantChanged: false,
		},
		{
			name:        "Chinese and multilingual",
			input:       "状态正常：CycleDock 生产验证通过 / 日本語 / 한국어 / Русский / العربية",
			want:        "状态正常：CycleDock 生产验证通过 / 日本語 / 한국어 / Русский / العربية",
			wantChanged: false,
		},
		{
			name:        "emoji and combining characters",
			input:       "🚀 Deployment green ✅ 👨‍💻 Café e\u0301",
			want:        "🚀 Deployment green ✅ 👨‍💻 Café e\u0301",
			wantChanged: false,
		},
		{
			name:        "newlines and tabs",
			input:       "line1\n\tindented\r\nline3\x1b[32mgreen\x1b[0m",
			want:        "line1\n\tindented\r\nline3\x1b[32mgreen\x1b[0m",
			wantChanged: false,
		},
		{
			name:        "literal backslash u0000 text",
			input:       `literal \u0000 in source code`,
			want:        `literal \u0000 in source code`,
			wantChanged: false,
		},
		{
			name:        "embedded NUL byte",
			input:       "stdout before\x00stdout after",
			want:        "stdout before\uFFFDstdout after",
			wantChanged: true,
		},
		{
			name:        "invalid UTF-8 bytes",
			input:       string([]byte{'b', 'a', 'd', ':', 0xff, 0xfe, '!', '\n'}),
			want:        "bad:\uFFFD!", // strings.ToValidUTF8 collapses adjacent invalid bytes into one replacement rune
			wantChanged: false,         // checked separately below for exact suffix
		},
		{
			name:        "UTF-8 encoded lone surrogate bytes",
			input:       "surrogate:" + string([]byte{0xed, 0xa0, 0x80}) + ":end",
			want:        "surrogate:\uFFFD:end",
			wantChanged: true,
		},
		{
			name:        "combined NUL and invalid UTF-8",
			input:       "mix\x00" + string([]byte{0x80}) + "中文🚀",
			want:        "mix\uFFFD\uFFFD中文🚀",
			wantChanged: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "invalid UTF-8 bytes" {
				tc.want = "bad:\uFFFD!\n"
				tc.wantChanged = true
			}
			got, changed := NormalizeText(tc.input)
			if got != tc.want || changed != tc.wantChanged {
				t.Fatalf("NormalizeText(%q) = (%q, %v), want (%q, %v)", tc.input, got, changed, tc.want, tc.wantChanged)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("NormalizeText(%q) produced invalid UTF-8: %q", tc.input, got)
			}
			if strings.IndexByte(got, 0) >= 0 {
				t.Fatalf("NormalizeText(%q) still contains NUL byte: %q", tc.input, got)
			}
		})
	}
}

func TestSanitizeAndMarshalJSONBHandleToolOutputsAndNestedJSON(t *testing.T) {
	largePrefix := strings.Repeat("日志输出正常行🚀\n", 1500)
	input := map[string]any{
		"key\x00with_nul": "val\x001",
		"stdout":          "normal line\n\tsecond\x00null-terminated\n",
		"stderr":          "err:" + string([]byte{0xff, 0xfe}) + "\x00end",
		"literal_escape":  `keep literal \u0000 text intact`,
		"nested_json":     `{"stdout":"inner\u0000value","stderr":"ok","access_token":"secret-tok"}`,
		"large_output":    largePrefix + "tail\x00" + string([]byte{0xed, 0xa0, 0x80}),
	}

	sanitized, changed := SanitizeWithReport(input)
	if !changed {
		t.Fatal("SanitizeWithReport reported changed=false, want true")
	}

	rawJSON, marshalChanged, err := MarshalJSONB(sanitized)
	if err != nil {
		t.Fatalf("MarshalJSONB error: %v", err)
	}
	if marshalChanged {
		t.Fatalf("MarshalJSONB reported additional changes after SanitizeWithReport: %s", rawJSON)
	}
	if !utf8.Valid(rawJSON) {
		t.Fatalf("MarshalJSONB produced invalid UTF-8")
	}
	if strings.Contains(string(rawJSON), `inner\u0000value`) {
		t.Fatalf("nested JSON still contains unescaped \\u0000: %s", rawJSON)
	}

	var roundTrip map[string]any
	if err := json.Unmarshal(rawJSON, &roundTrip); err != nil {
		t.Fatalf("Unmarshal sanitized JSONB: %v", err)
	}
	if roundTrip["key\uFFFDwith_nul"] != "val\uFFFD1" {
		t.Fatalf("key/value with NUL = %#v", roundTrip["key\uFFFDwith_nul"])
	}
	if roundTrip["stdout"] != "normal line\n\tsecond\uFFFDnull-terminated\n" {
		t.Fatalf("stdout = %q", roundTrip["stdout"])
	}
	if roundTrip["stderr"] != "err:\uFFFD\uFFFDend" {
		t.Fatalf("stderr = %q", roundTrip["stderr"])
	}
	if roundTrip["literal_escape"] != `keep literal \u0000 text intact` {
		t.Fatalf("literal_escape = %q, want literal \\u0000 preserved", roundTrip["literal_escape"])
	}

	var nested map[string]any
	if err := json.Unmarshal([]byte(roundTrip["nested_json"].(string)), &nested); err != nil {
		t.Fatalf("Unmarshal nested_json: %v", err)
	}
	if nested["stdout"] != "inner\uFFFDvalue" {
		t.Fatalf("nested stdout = %q, want inner\\uFFFDvalue", nested["stdout"])
	}
	if nested["access_token"] != Redacted {
		t.Fatalf("nested access_token = %q, want redacted", nested["access_token"])
	}
	if !strings.HasPrefix(roundTrip["large_output"].(string), largePrefix) ||
		!strings.HasSuffix(roundTrip["large_output"].(string), "tail\uFFFD\uFFFD") {
		t.Fatalf("large_output corrupted")
	}
}

func TestNormalizeJSONBReplacesOnlyActualNullEscapes(t *testing.T) {
	raw := []byte(`{"nul":"\u0000","upper":"\u0000","escaped_backslash":"\\\u0000","literal":"\\u0000","double_literal":"\\\\u0000"}`)
	normalized, changed := NormalizeJSONB(raw)
	if !changed {
		t.Fatal("NormalizeJSONB changed=false, want true")
	}

	var decoded map[string]string
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		t.Fatalf("Unmarshal normalized JSON: %v", err)
	}
	if decoded["nul"] != "\uFFFD" {
		t.Fatalf("nul = %q, want U+FFFD", decoded["nul"])
	}
	if decoded["upper"] != "\uFFFD" {
		t.Fatalf("upper = %q, want U+FFFD", decoded["upper"])
	}
	if decoded["escaped_backslash"] != "\\\uFFFD" {
		t.Fatalf("escaped_backslash = %q, want backslash + U+FFFD", decoded["escaped_backslash"])
	}
	if decoded["literal"] != `\u0000` {
		t.Fatalf("literal = %q, want literal \\u0000 preserved", decoded["literal"])
	}
	if decoded["double_literal"] != `\\u0000` {
		t.Fatalf("double_literal = %q, want literal \\\\u0000 preserved", decoded["double_literal"])
	}
}

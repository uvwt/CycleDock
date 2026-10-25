package agentdata

import (
	"encoding/json"
	"strings"
)

const Redacted = "[REDACTED]"

var sensitiveKeys = map[string]struct{}{
	"password":           {},
	"passwd":             {},
	"secret":             {},
	"clientsecret":       {},
	"apikey":             {},
	"apitoken":           {},
	"token":              {},
	"accesstoken":        {},
	"refreshtoken":       {},
	"sessiontoken":       {},
	"authorization":      {},
	"proxyauthorization": {},
	"cookie":             {},
	"setcookie":          {},
	"privatekey":         {},
	"credential":         {},
	"credentials":        {},
}

// Sanitize returns a deep copy of JSON-like data with structurally identified
// credentials removed. It deliberately does not try to regex arbitrary strings:
// doing so would corrupt ordinary tool evidence and still miss many real secrets.
func Sanitize(value any) any {
	switch current := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			if isSensitiveKey(key) {
				out[key] = Redacted
				continue
			}
			out[key] = Sanitize(item)
		}
		return out
	case []any:
		out := make([]any, len(current))
		for i, item := range current {
			out[i] = Sanitize(item)
		}
		return out
	case string:
		trimmed := strings.TrimSpace(current)
		if trimmed == "" || (trimmed[0] != '{' && trimmed[0] != '[') {
			return current
		}
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
			return current
		}
		safe, err := json.Marshal(Sanitize(decoded))
		if err != nil {
			return current
		}
		return string(safe)
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	normalized = replacer.Replace(normalized)
	_, ok := sensitiveKeys[normalized]
	return ok
}

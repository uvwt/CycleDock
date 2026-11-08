package agentconfig

import "testing"

func TestValidateEndpoint(t *testing.T) {
	valid := []string{
		"https://api.openai.com/v1",
		"https://api.deepseek.com/v1",
		"http://localhost:11434/v1",
		"http://127.0.0.1:8080/v1",
		"http://[::1]:8080/v1",
	}
	for _, raw := range valid {
		if err := ValidateEndpoint(raw); err != nil {
			t.Fatalf("expected %q to be valid, got %v", raw, err)
		}
	}

	invalid := []string{
		"",
		"not-a-url",
		"http://api.openai.com/v1",
		"https://10.0.0.1/v1",
		"https://172.16.0.10/v1",
		"https://192.168.1.50/v1",
		"https://169.254.169.254/latest/meta-data",
		"https://[fe80::1]/v1",
		"https://[fc00::1]/v1",
		"https://0.0.0.0/v1",
		"https://user:pass@api.openai.com/v1",
		"https://api.openai.com/v1?foo=bar",
	}
	for _, raw := range invalid {
		if err := ValidateEndpoint(raw); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

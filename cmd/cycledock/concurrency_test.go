package main

import "testing"

func TestConcurrencyEnvironmentRequiresPositiveInteger(t *testing.T) {
	for _, value := range []string{"", " 12 ", "0", "-1", "many", "1.5", "9999999999999999999999999"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CYCLEDOCK_AGENT_MAX_CONCURRENT", value)
			got, err := envPositiveInt("CYCLEDOCK_AGENT_MAX_CONCURRENT", 16)
			switch value {
			case "":
				if err != nil || got != 16 {
					t.Fatalf("default=%d err=%v", got, err)
				}
			case " 12 ":
				if err != nil || got != 12 {
					t.Fatalf("configured=%d err=%v", got, err)
				}
			default:
				if err == nil {
					t.Fatalf("invalid concurrency %q accepted", value)
				}
			}
		})
	}
}

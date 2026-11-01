package main

import (
	"testing"

	"github.com/uvwt/CycleDock/internal/agent"
)

func TestConcurrencyEnvironmentRequiresPositiveInteger(t *testing.T) {
	specs := []struct {
		envKey      string
		defaultVal  int
		expectedDef int
	}{
		{
			envKey:      "CYCLEDOCK_AGENT_MAX_CONCURRENT",
			defaultVal:  agent.DefaultMaxConcurrent,
			expectedDef: 16,
		},
		{
			envKey:      "CYCLEDOCK_AGENT_MAX_CONCURRENT_PER_WORKSPACE",
			defaultVal:  agent.DefaultMaxConcurrentPerWorkspace,
			expectedDef: 10,
		},
	}

	for _, spec := range specs {
		t.Run(spec.envKey, func(t *testing.T) {
			if spec.defaultVal != spec.expectedDef {
				t.Fatalf("unexpected default constant for %s: got %d, want %d", spec.envKey, spec.defaultVal, spec.expectedDef)
			}
			for _, value := range []string{"", " 12 ", "0", "-1", "many", "1.5", "9999999999999999999999999"} {
				t.Run(value, func(t *testing.T) {
					t.Setenv(spec.envKey, value)
					got, err := envPositiveInt(spec.envKey, spec.defaultVal)
					switch value {
					case "":
						if err != nil || got != spec.expectedDef {
							t.Fatalf("default=%d err=%v, want %d", got, err, spec.expectedDef)
						}
					case " 12 ":
						if err != nil || got != 12 {
							t.Fatalf("configured=%d err=%v, want 12", got, err)
						}
					default:
						if err == nil {
							t.Fatalf("invalid concurrency %q accepted", value)
						}
					}
				})
			}
		})
	}
}

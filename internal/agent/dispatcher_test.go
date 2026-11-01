package agent

import (
	"testing"

	"github.com/uvwt/CycleDock/internal/modelclient"
)

func TestNewDispatcherAppliesDefaultConcurrencyLimits(t *testing.T) {
	dispatcher := NewDispatcher(nil, modelclient.New(), nil, DispatcherConfig{})
	if dispatcher.maxConcurrent != 32 {
		t.Fatalf("default maxConcurrent = %d, want 32", dispatcher.maxConcurrent)
	}
	if dispatcher.maxConcurrentPerWorkspace != 10 {
		t.Fatalf("default maxConcurrentPerWorkspace = %d, want 10", dispatcher.maxConcurrentPerWorkspace)
	}

	custom := NewDispatcher(nil, modelclient.New(), nil, DispatcherConfig{
		MaxConcurrent:             24,
		MaxConcurrentPerWorkspace: 12,
	})
	if custom.maxConcurrent != 24 {
		t.Fatalf("configured maxConcurrent = %d, want 24", custom.maxConcurrent)
	}
	if custom.maxConcurrentPerWorkspace != 12 {
		t.Fatalf("configured maxConcurrentPerWorkspace = %d, want 12", custom.maxConcurrentPerWorkspace)
	}
}

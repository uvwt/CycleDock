package httpapi

import (
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStreamRequiresHumanSession(t *testing.T) {
	router := NewRouter(nil, nil, nil, Config{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/cards/"+uuid.NewString()+"/stream", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("stream without cookie: got %d", recorder.Code)
	}
}

package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A handler behind ThrottleBacklog(1, 0, ...) panics once inside Recoverer.
// The single processing slot must be available again for the next requests.
func TestThrottleSlotAvailableAfterRecoveredPanic(t *testing.T) {
	recovererErrorWriter = io.Discard

	calls := 0
	handler := Recoverer(ThrottleBacklog(1, 0, 200*time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			panic("handler failure")
		}
		w.WriteHeader(http.StatusOK)
	})))

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest("GET", "/", nil))
	if first.Code != http.StatusInternalServerError {
		t.Fatalf("first request: status %d, want 500 from Recoverer", first.Code)
	}

	for i := 2; i <= 3; i++ {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d after the recovered panic: status %d (%q), want 200",
				i, rec.Code, rec.Body.String())
		}
	}
}

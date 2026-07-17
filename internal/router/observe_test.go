package router

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestObserveHandlerPreservesFinalStatusAfterEarlyHints(t *testing.T) {
	var event RequestEvent
	handler := ObserveHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("created"))
	}), func(got RequestEvent) {
		event = got
	})
	w := &statusRecordingWriter{header: make(http.Header)}
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))

	wantStatuses := []int{http.StatusEarlyHints, http.StatusCreated}
	if len(w.statuses) != len(wantStatuses) {
		t.Fatalf("statuses = %v, want %v", w.statuses, wantStatuses)
	}
	for index, want := range wantStatuses {
		if w.statuses[index] != want {
			t.Fatalf("statuses = %v, want %v", w.statuses, wantStatuses)
		}
	}
	if event.Status != http.StatusCreated {
		t.Fatalf("event status = %d, want %d", event.Status, http.StatusCreated)
	}
}

type statusRecordingWriter struct {
	header   http.Header
	statuses []int
	body     bytes.Buffer
}

func (w *statusRecordingWriter) Header() http.Header {
	return w.header
}

func (w *statusRecordingWriter) WriteHeader(status int) {
	w.statuses = append(w.statuses, status)
}

func (w *statusRecordingWriter) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

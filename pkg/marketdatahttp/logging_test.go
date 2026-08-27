package marketdatahttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusWriterRetainsFirstExplicitStatus(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &statusWriter{ResponseWriter: recorder, status: http.StatusOK}
	w.WriteHeader(http.StatusAccepted)
	w.WriteHeader(http.StatusInternalServerError)

	if w.status != http.StatusAccepted || recorder.Code != http.StatusAccepted {
		t.Fatalf("statusWriter=%d response=%d", w.status, recorder.Code)
	}
}

func TestStatusWriterRecordsImplicitOK(t *testing.T) {
	recorder := httptest.NewRecorder()
	w := &statusWriter{ResponseWriter: recorder, status: http.StatusOK}
	if _, err := w.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	w.WriteHeader(http.StatusInternalServerError)

	if w.status != http.StatusOK || recorder.Code != http.StatusOK {
		t.Fatalf("statusWriter=%d response=%d", w.status, recorder.Code)
	}
}

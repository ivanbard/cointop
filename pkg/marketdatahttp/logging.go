package marketdatahttp

import (
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func WithRequestLogging(next http.Handler, logger *log.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		logger.WithFields(log.Fields{
			"method": r.Method, "path": r.URL.Path, "status": wrapped.status,
			"duration_ms": time.Since(started).Milliseconds(),
		}).Info("market data API request")
	})
}

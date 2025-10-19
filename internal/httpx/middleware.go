// internal/httpx/middleware.go
//
// Common HTTP middleware used by the service (logging, etc.).
// This file defines a request/response logger that records method, path,
// response status, and total duration for each request.

package httpx

import (
	"log"
	"net/http"
	"time"
)

// LogRequests wraps the provided handler and logs a single structured line
// for every request once it completes.
//
// Example log line:
//   GET /quotes/EUR/MXN 200 12.345ms
//
// Notes:
//   - The logger relies on a lightweight response writer wrapper to capture
//     the final HTTP status code.
//   - It does not log bodies or headers to avoid leaking sensitive data.
func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startTime := time.Now()

		// Wrap the original ResponseWriter to observe the status code.
		loggingWriter := &statusCapturingWriter{
			ResponseWriter: writer,
			statusCode:     http.StatusOK, // default if WriteHeader is never called
		}

		next.ServeHTTP(loggingWriter, request)

		elapsed := time.Since(startTime)
		log.Printf("%s %s %d %s",
			request.Method,
			request.URL.Path,
			loggingWriter.statusCode,
			elapsed,
		)
	})
}

// statusCapturingWriter wraps http.ResponseWriter and records the last status code
// written so it can be included in logs. If WriteHeader is never called by the
// downstream handler, the status defaults to 200 (OK).
type statusCapturingWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader records the status code and forwards the call to the underlying writer.
func (w *statusCapturingWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

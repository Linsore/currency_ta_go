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

func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startTime := time.Now()

		loggingWriter := &statusCapturingWriter{
			ResponseWriter: writer,
			statusCode:     http.StatusOK, 
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

type statusCapturingWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

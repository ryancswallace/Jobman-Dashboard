package observability

import (
	"net/http"
	"strings"
	"time"
)

type responseStatus struct {
	http.ResponseWriter
	status int
}

func (w *responseStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseStatus) WriteHeader(status int) {
	if status >= 200 && w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseStatus) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.ResponseWriter.Write(p)
}

// HTTP records only a fixed route family and status classification. It never
// retains request paths, query parameters, headers or identities.
func (r *Registry) HTTP(next http.Handler) http.Handler {
	if r == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		op := "other"
		switch {
		case strings.HasPrefix(q.URL.Path, "/api/"):
			op = "api"
		case strings.HasPrefix(q.URL.Path, "/auth/"):
			op = "auth"
		case q.Method == http.MethodGet:
			op = "static"
		}
		completed := false
		start := time.Now()
		record := &responseStatus{ResponseWriter: w}
		defer func() {
			status := record.status
			if status == 0 {
				status = 200
			}
			result := "ok"
			if !completed {
				status = 500
			}
			switch {
			case status == 401 || status == 403 || status == 404:
				result = "denied"
			case status == 409:
				result = "conflict"
			case status == 429:
				result = "rate_limited"
			case status >= 500:
				result = "unavailable"
			case status >= 400:
				result = "invalid"
			}
			r.Observe("http", op, "", "", result, time.Since(start))
		}()
		next.ServeHTTP(record, q)
		completed = true
	})
}

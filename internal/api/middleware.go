package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bookbee/air-infrastructure-service/internal/constants"
	"github.com/bookbee/air-infrastructure-service/internal/observability"
)

type requestIDKey struct{}

// RequestIDFrom returns the request id bound to ctx, or "" if there is none.
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Observe is the outer middleware: it assigns a request id, echoes it back,
// counts the request, and logs one access line.
//
// It wraps the whole mux rather than each route so that an unmatched path is
// counted and logged too, which is what the Python middleware did. The matched
// route template is read back from the request after dispatch, so the metric
// labels stay bounded — labelling by raw path would let a caller mint an
// unbounded number of time series.
func Observe(next http.Handler, m *observability.Metrics, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(constants.HeaderRequestID)
		if requestID == "" {
			requestID = newRequestID()
		}

		ctx := context.WithValue(r.Context(), requestIDKey{}, requestID)
		ctx = observability.WithLogger(ctx, log.With("request_id", requestID))
		r = r.WithContext(ctx)

		// Set before dispatch: a handler that writes its own header would
		// otherwise send the response without the id.
		w.Header().Set(constants.HeaderRequestID, requestID)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)
		elapsed := time.Since(start)

		m.ObserveRequest(routeLabel(r), r.Method, strconv.Itoa(rec.status))
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"ms", float64(elapsed.Microseconds())/1000.0,
			"request_id", requestID,
		)
	})
}

// newRequestID returns a 32-character hex id, the same shape as the Python's
// uuid4().hex, without taking a dependency to produce 16 random bytes.
func newRequestID() string {
	var b [16]byte
	// crypto/rand.Read never returns an error on any supported platform as of
	// Go 1.24; ignoring it keeps the request path free of a branch that cannot
	// be taken, and a request id is not a security boundary in any case.
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// routeLabel is the metric's route dimension: the matched pattern with its
// method and its /v1 prefix removed ("/resources/{name}/credentials"), falling
// back to the raw path when nothing matched.
//
// Dropping the version prefix looks arbitrary and is not — it is the label the
// Python service emitted, and any dashboard or alert built against it selects
// on that value. An unmatched request keeps its full path, also as before.
func routeLabel(r *http.Request) string {
	pattern := r.Pattern
	if _, path, found := strings.Cut(pattern, " "); found {
		pattern = path
	}
	// "/" is the catch-all that serves the 404 body, so it means no route
	// matched — not that the caller asked for the root. Labelling those by raw
	// path is what the Python did, and it is bounded in practice because an
	// unmatched path is an error a client fixes rather than a route it uses.
	if pattern == "" || pattern == "/" {
		return r.URL.Path
	}
	if trimmed := strings.TrimPrefix(pattern, "/v1"); trimmed != "" {
		return trimmed
	}
	return pattern
}

// statusRecorder remembers the status code so the access log and the metric
// can report it after the handler has written.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(status int) {
	if s.written {
		return
	}
	s.written = true
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// An implicit 200 from a handler that writes without calling WriteHeader.
	s.written = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer, so wrapping
// here does not quietly cost a future handler its Flush or Hijack. Nothing
// needs it today; it is three lines that stop this from becoming a puzzling
// bug the first time someone adds a streaming endpoint.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

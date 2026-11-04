package kit

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "sendwise",
		Name:      "http_requests_total",
		Help:      "Total HTTP requests handled by the service.",
	}, []string{"service", "method", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "sendwise",
		Name:      "http_request_duration_seconds",
		Help:      "HTTP request duration in seconds.",
		Buckets:   []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"service", "method"})
)

// Metrics returns middleware recording request counts and latency for the
// named service on the default Prometheus registry.
func Metrics(service string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			status := strconv.Itoa(rec.status)
			httpRequestsTotal.WithLabelValues(service, r.Method, status).Inc()
			httpRequestDuration.WithLabelValues(service, r.Method).Observe(time.Since(start).Seconds())
		})
	}
}

// MetricsHandler serves the Prometheus exposition for the default registry.
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

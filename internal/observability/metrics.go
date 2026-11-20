package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	HTTPRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"method", "route", "status"},
	)

	HTTPDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	DeliveryJobs = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "delivery_jobs_total",
			Help: "Delivery jobs processed by result.",
		},
		[]string{"result"},
	)

	ProviderFallbacks = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "delivery_provider_fallbacks_total",
			Help: "Delivery provider fallback decisions.",
		},
		[]string{"kind", "from", "to", "reason"},
	)

	QueuePolls = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "campaign_scheduler_polls_total",
			Help: "Number of times the campaign scheduler has polled for due campaigns.",
		},
	)

	SMTPBreakerOpen = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "smtp_circuit_breaker_open",
			Help: "Whether the worker SMTP circuit breaker is open.",
		},
	)

	DeliveryJourneyStuckMessages = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "delivery_journey_stuck_messages",
			Help: "Submitted or relaying recipient messages without a recent Stalwart event.",
		},
	)
)

func init() {
	prometheus.MustRegister(HTTPRequests, HTTPDuration, DeliveryJobs, ProviderFallbacks,
		QueuePolls, SMTPBreakerOpen, DeliveryJourneyStuckMessages)
}

func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		HTTPRequests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		HTTPDuration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

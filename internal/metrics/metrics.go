// Package metrics centralises every Prometheus collector used by the service.
// Collectors are registered on the default registry, which is exposed by
// /metrics on both API and worker.
package metrics

import (
	"runtime"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const namespace = "getcontact"

var (
	// HTTPRequests counts completed HTTP requests.
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "http", Name: "requests_total",
		Help: "Total number of HTTP requests by method, route and status.",
	}, []string{"method", "route", "status"})

	// HTTPDuration observes request latency.
	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Subsystem: "http", Name: "request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"method", "route"})

	// HTTPInFlight tracks concurrent requests.
	HTTPInFlight = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: namespace, Subsystem: "http", Name: "in_flight_requests",
		Help: "Number of HTTP requests currently being served.",
	})

	// HTTPResponseSize observes response payload sizes.
	HTTPResponseSize = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Subsystem: "http", Name: "response_size_bytes",
		Help:    "HTTP response size in bytes.",
		Buckets: prometheus.ExponentialBuckets(128, 4, 8),
	}, []string{"method", "route"})

	// DBQueryDuration observes database call latency.
	DBQueryDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: namespace, Subsystem: "db", Name: "query_duration_seconds",
		Help:    "Database query latency in seconds.",
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5},
	}, []string{"operation"})

	// CacheOperations counts Redis hits, misses, writes and errors.
	CacheOperations = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "cache", Name: "operations_total",
		Help: "Cache operations by operation and result.",
	}, []string{"operation", "result"})

	// KafkaEvents counts published and consumed events.
	KafkaEvents = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "kafka", Name: "events_total",
		Help: "Kafka events by direction, topic and result.",
	}, []string{"direction", "topic", "result"})

	// NumberLookups counts caller-ID lookups by cache result.
	NumberLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "business", Name: "number_lookups_total",
		Help: "Caller-ID lookups by result.",
	}, []string{"result"})

	// SpamReports counts accepted spam reports.
	SpamReports = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "business", Name: "spam_reports_total",
		Help: "Total accepted spam reports.",
	})

	// TagsAdded counts community tags.
	TagsAdded = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "business", Name: "tags_added_total",
		Help: "Total community tags added.",
	})

	// NamesAdded counts community name votes.
	NamesAdded = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "business", Name: "names_added_total",
		Help: "Total community name votes submitted.",
	})

	// AuthAttempts counts login/register/refresh outcomes.
	AuthAttempts = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace, Subsystem: "auth", Name: "attempts_total",
		Help: "Authentication attempts by operation and result.",
	}, []string{"operation", "result"})

	// BuildInfo exposes the running version.
	BuildInfo = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace, Name: "build_info",
		Help: "Build information of the running binary.",
	}, []string{"version", "go_version"})

	// StartedAt is the process start time (useful for uptime dashboards).
	StartedAt = time.Now()
)

// Init records build metadata once at process boot.
func Init(version string) {
	BuildInfo.WithLabelValues(version, runtime.Version()).Set(1)
}

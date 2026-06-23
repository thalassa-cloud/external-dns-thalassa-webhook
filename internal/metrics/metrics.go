package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	ThalassaRequestsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "thalassa_api_requests_total",
		Help: "Total number of requests to the Thalassa Cloud API",
	})

	ThalassaErrorsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "thalassa_api_errors_total",
		Help: "Total number of failed requests to the Thalassa Cloud API",
	})

	ThalassaRateLimitsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "thalassa_api_rate_limits_total",
		Help: "Total number of rate limit hits (429) from the Thalassa Cloud API",
	})
)

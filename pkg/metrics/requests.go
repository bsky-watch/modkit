package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var RequestStatus = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "modkit",
	Subsystem: "requests",
	Name:      "total",
	Help:      "Number of requests received",
}, []string{
	"type",
	"success",
	"statusCode",
})

var RequestDuration = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "modkit",
	Subsystem: "requests",
	Name:      "seconds_total",
	Help:      "Total amount of time spent on handling each request",
}, []string{
	"type",
	"success",
	"statusCode",
})

var RequestStats = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "modkit",
	Subsystem: "requests",
	Name:      "duration_seconds",
	Help:      "Time spent on handling each request",
	Buckets:   prometheus.ExponentialBucketsRange(0.001, 300, 50),
}, []string{
	"type",
	"success",
})

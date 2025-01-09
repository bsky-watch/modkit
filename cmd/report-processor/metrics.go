package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var reportsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: "modkit",
	Subsystem: "report_processor",
	Name:      "reports_processed_total",
	Help:      "Number of reports received (not excluding retries)",
}, []string{
	"remote",
	"success",
})

var processingStats = promauto.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "modkit",
	Subsystem: "report_processor",
	Name:      "report_processing_duration_seconds",
	Help:      "Time spent on processing reports",
}, []string{
	"remote",
	"success",
})

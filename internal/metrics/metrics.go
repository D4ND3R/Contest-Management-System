// Package metrics holds the Prometheus registry shared by a process and
// helpers to expose it.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry is the process-wide registry. A private registry (instead of the
// global default) keeps tests independent and output small.
var Registry = newRegistry()

func newRegistry() *prometheus.Registry {
	r := prometheus.NewRegistry()
	r.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return r
}

// Handler serves the registry in the Prometheus exposition format.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// MustRegister registers collectors, ignoring duplicates (which happen when
// a service is constructed twice in the same process, e.g. in tests).
func MustRegister(cs ...prometheus.Collector) {
	for _, c := range cs {
		if err := Registry.Register(c); err != nil {
			if _, ok := err.(prometheus.AlreadyRegisteredError); ok {
				continue
			}
			panic(err)
		}
	}
}

// NewHistogramVec creates and registers a histogram vector; when an equal
// collector is already registered the existing one is returned.
func NewHistogramVec(opts prometheus.HistogramOpts, labels []string) *prometheus.HistogramVec {
	h := prometheus.NewHistogramVec(opts, labels)
	if err := Registry.Register(h); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(*prometheus.HistogramVec)
		}
		panic(err)
	}
	return h
}

// NewCounterVec creates and registers a counter vector (idempotent).
func NewCounterVec(opts prometheus.CounterOpts, labels []string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(opts, labels)
	if err := Registry.Register(c); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(*prometheus.CounterVec)
		}
		panic(err)
	}
	return c
}

// NewGaugeVec creates and registers a gauge vector (idempotent).
func NewGaugeVec(opts prometheus.GaugeOpts, labels []string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(opts, labels)
	if err := Registry.Register(g); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			return are.ExistingCollector.(*prometheus.GaugeVec)
		}
		panic(err)
	}
	return g
}

// LatencyBuckets are tuned for sub-50ms web targets.
var LatencyBuckets = []float64{.0005, .001, .0025, .005, .0075, .01, .015, .025, .04, .05, .1, .25, .5, 1, 2.5}

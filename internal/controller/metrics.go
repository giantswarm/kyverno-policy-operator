package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var FailedControllerReconciliations = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "failed_controller_reconciliations_total",
		Help: "The total number of failed controller reconciliations",
	},
	[]string{"resource_type"},
)

// Increment the metric for a specific controller
func incrementFailedReconciliations(controllerName string) {
	FailedControllerReconciliations.With(prometheus.Labels{"controller": controllerName}).Inc()
}

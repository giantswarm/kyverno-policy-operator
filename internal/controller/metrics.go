package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// FailedClusterPolicyControllerReconciliations is a Prometheus counter to keep track of the number of failed Cluster Policy Controller reconciliations.
	FailedClusterPolicyControllerReconciliations = promauto.NewCounter(prometheus.CounterOpts{
		Name: "cluster_policy_controller_failed_reconciliations_total",
		Help: "The total number of failed cluster policy controller reconciliations",
	})
	// FailedPolicyExceptionControllerReconciliations is a Prometheus counter to keep track of the number of failed Policy Exception Controller reconciliations.
	FailedPolicyExceptionControllerReconciliations = promauto.NewCounter(prometheus.CounterOpts{
		Name: "policy_exception_controller_failed_reconciliations_total",
		Help: "The total number of failed policy exception controller reconciliations",
	})
	// FailedPolicyManifestControllerReconciliations is a Prometheus counter to keep track of the number of failed Policy Manifest Controller reconciliations.
	FailedPolicyManifestControllerReconciliations = promauto.NewCounter(prometheus.CounterOpts{
		Name: "policy_manifest_controller_failed_reconciliations_total",
		Help: "The total number of failed policy manifest controller reconciliations",
	})
)

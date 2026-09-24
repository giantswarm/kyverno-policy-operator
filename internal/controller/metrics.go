package controller

import (
	"context"
	"strings"
	"time"

	"github.com/go-logr/logr"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	APILegacy = "legacy"
	APICEL    = "cel"
)

var GenerationErrors = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "kyverno_policy_operator_generation_errors_total",
	Help: "Errors writing or deleting generated Kyverno PolicyExceptions.",
}, []string{"api", "reason"})

// sources are the policy.giantswarm.io/source values. The gauges report 0 for each one they
// counted and found none of, so dashboards show 0 instead of no data.
var sources = []string{SourceGSPolex, SourceExceptionRecommender, SourceChartOperator}

func init() {
	// Start every error series at 0, so increase() and rate() show 0 instead of no data.
	for _, api := range []string{APILegacy, APICEL} {
		for _, reason := range []string{"lookup_failed", "apply_failed", "delete_failed", "name_taken"} {
			GenerationErrors.WithLabelValues(api, reason)
		}
	}
}

// zeroCounts returns a count of 0 for every source.
func zeroCounts() map[string]int {
	counts := make(map[string]int, len(sources))
	for _, source := range sources {
		counts[source] = 0
	}
	return counts
}

var (
	exceptionsDesc = prometheus.NewDesc("kyverno_policy_operator_policyexceptions",
		"Generated Kyverno PolicyExceptions by API and source.", []string{"api", "source"}, nil)
	dualDesc = prometheus.NewDesc("kyverno_policy_operator_dual_policyexceptions",
		"Generated exceptions that exist both as kyverno.io/v2 and policies.kyverno.io PolicyExceptions.", []string{"source"}, nil)
	legacyEnabledDesc = prometheus.NewDesc("kyverno_policy_operator_legacy_exceptions_enabled",
		"Whether kyverno.io/v2 PolicyExceptions are being written (1) or not (0).", nil, nil)
	unresolvedDesc = prometheus.NewDesc("kyverno_policy_operator_unresolved_policy_refs",
		"Generated CEL exceptions referencing a policy name that matches no CEL policy.", []string{"policy"}, nil)
)

// ExceptionCollector computes the exception gauges at scrape time from the labelled objects KPO
// generated, so the numbers stay correct across restarts.
type ExceptionCollector struct {
	Reader     client.Reader
	LegacyMode LegacyMode
	Log        logr.Logger
}

func (c *ExceptionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- exceptionsDesc
	ch <- dualDesc
	ch <- legacyEnabledDesc
	ch <- unresolvedDesc
}

func (c *ExceptionCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	managed := client.MatchingLabels{ManagedBy: ComponentName}

	legacyEnabled := 0.0
	if c.LegacyMode == LegacyWrite {
		legacyEnabled = 1
	}
	ch <- prometheus.MustNewConstMetric(legacyEnabledDesc, prometheus.GaugeValue, legacyEnabled)

	// Dual exceptions are only counted, and reported as 0, once the legacy list succeeded.
	legacyKeys := map[types.NamespacedName]bool{}
	legacyListed := false
	if c.LegacyMode != LegacyAbsent {
		var legacy kyvernov2.PolicyExceptionList
		if err := c.Reader.List(ctx, &legacy, managed); err != nil {
			// Skip only the legacy gauges; the CEL ones do not depend on them.
			c.Log.Error(err, "unable to list kyverno.io PolicyExceptions for metrics")
		} else {
			legacyListed = true
			counts := zeroCounts()
			for _, p := range legacy.Items {
				counts[p.Labels[LabelSource]]++
				legacyKeys[types.NamespacedName{Namespace: p.Namespace, Name: p.Name}] = true
			}
			for source, n := range counts {
				ch <- prometheus.MustNewConstMetric(exceptionsDesc, prometheus.GaugeValue, float64(n), APILegacy, source)
			}
		}
	}

	var cel policiesv1.PolicyExceptionList
	if err := c.Reader.List(ctx, &cel, managed); err != nil {
		c.Log.Error(err, "unable to list policies.kyverno.io PolicyExceptions for metrics")
		return
	}
	counts, dual, unresolved := zeroCounts(), map[string]int{}, map[string]int{}
	if legacyListed {
		dual = zeroCounts()
	}
	for _, p := range cel.Items {
		source := p.Labels[LabelSource]
		counts[source]++
		if legacyKeys[types.NamespacedName{Namespace: p.Namespace, Name: p.Name}] {
			dual[source]++
		}
		for _, name := range strings.Split(p.Annotations[AnnotationUnresolvedPolicies], ",") {
			if name != "" {
				unresolved[name]++
			}
		}
	}
	for source, n := range counts {
		ch <- prometheus.MustNewConstMetric(exceptionsDesc, prometheus.GaugeValue, float64(n), APICEL, source)
	}
	for source, n := range dual {
		ch <- prometheus.MustNewConstMetric(dualDesc, prometheus.GaugeValue, float64(n), source)
	}
	for name, n := range unresolved {
		ch <- prometheus.MustNewConstMetric(unresolvedDesc, prometheus.GaugeValue, float64(n), name)
	}
}

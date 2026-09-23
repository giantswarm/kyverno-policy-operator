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

	legacyKeys := map[types.NamespacedName]bool{}
	if c.LegacyMode != LegacyAbsent {
		var legacy kyvernov2.PolicyExceptionList
		if err := c.Reader.List(ctx, &legacy, managed); err != nil {
			// Skip only the legacy gauges; the CEL ones do not depend on them.
			c.Log.Error(err, "unable to list kyverno.io PolicyExceptions for metrics")
		} else {
			counts := map[string]int{}
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
	counts, dual, unresolved := map[string]int{}, map[string]int{}, map[string]int{}
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

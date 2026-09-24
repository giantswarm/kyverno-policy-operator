package controller

import (
	"context"
	"slices"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// LegacyMode decides how KPO treats kyverno.io/v2 PolicyExceptions.
type LegacyMode int

const (
	// LegacyWrite writes kyverno.io/v2 PolicyExceptions next to the policies.kyverno.io ones.
	LegacyWrite LegacyMode = iota
	// LegacyCleanup deletes the kyverno.io/v2 PolicyExceptions KPO created instead of writing them.
	LegacyCleanup
	// LegacyAbsent means the kyverno.io ClusterPolicy or PolicyException CRDs are not installed.
	LegacyAbsent
)

func (m LegacyMode) String() string {
	return [...]string{"write", "cleanup", "absent"}[m]
}

// DetectLegacyMode decides from the API server's CRDs and the --legacy-exceptions switch how KPO
// treats kyverno.io/v2 PolicyExceptions. Kyverno 1.20 removes the ClusterPolicy and legacy
// PolicyException CRDs, and KPO must keep starting without them.
func DetectLegacyMode(mapper meta.RESTMapper, enabled bool) (LegacyMode, error) {
	for _, gvk := range []schema.GroupVersionKind{
		schema.GroupVersion(kyvernov1.GroupVersion).WithKind("ClusterPolicy"),
		schema.GroupVersion(kyvernov2.GroupVersion).WithKind("PolicyException"),
	} {
		if _, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version); err != nil {
			if meta.IsNoMatchError(err) {
				return LegacyAbsent, nil
			}
			return LegacyAbsent, err
		}
	}
	if !enabled {
		return LegacyCleanup, nil
	}
	return LegacyWrite, nil
}

// DeleteLegacyChartOperatorBypass removes the kyverno.io/v2 chart-operator bypass the ClusterPolicy
// controller maintained, once legacy exceptions are switched off.
func DeleteLegacyChartOperatorBypass(ctx context.Context, c client.Client) error {
	return deleteManaged(ctx, c, &kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Namespace: ChartOperatorBypassNamespace, Name: ChartOperatorBypassName}})
}

// reconcileLegacy keeps the kyverno.io/v2 PolicyException for a gspolex. It lists the policies that
// exist as ClusterPolicies, and keeps the existing entry of a policy whose ClusterPolicy is missing.
// It deletes the exception when no entry is left, the gspolex was migrated by exception-recommender,
// or legacy exceptions are switched off.
func (r *PolicyExceptionReconciler) reconcileLegacy(ctx context.Context, gspolex *policyAPI.PolicyException, namespace string) error {
	logger := log.FromContext(ctx).WithValues("gspolex", client.ObjectKeyFromObject(gspolex))

	if r.LegacyMode == LegacyAbsent {
		logger.V(1).Info("legacy exceptions skipped, kyverno.io CRDs not installed")
		return nil
	}

	legacy := kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: gspolex.Name, Namespace: namespace}}
	targets := dropEmptyNames(logger, gspolex.Spec.Targets)
	var exceptions []kyvernov2.Exception
	switch {
	case r.LegacyMode == LegacyCleanup:
		logger.V(1).Info("legacy exceptions switched off")
	case isMigrated(gspolex):
		logger.V(1).Info("gspolex migrated by exception-recommender, skipping legacy exception")
	case len(targets) == 0:
		// A legacy exception without resource filters would match every resource.
		logger.V(1).Info("gspolex has no usable targets, skipping legacy exception")
	default:
		var err error
		exceptions, err = r.legacyExceptions(ctx, gspolex, client.ObjectKeyFromObject(&legacy))
		if err != nil {
			GenerationErrors.WithLabelValues(APILegacy, "lookup_failed").Inc()
			return err
		}
	}

	if len(exceptions) == 0 {
		if err := deleteManaged(ctx, r.Client, &legacy); err != nil {
			GenerationErrors.WithLabelValues(APILegacy, "delete_failed").Inc()
			return err
		}
		return nil
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &legacy, func() error {
		setManagedLabels(&legacy, SourceGSPolex)
		if err := controllerutil.SetControllerReference(gspolex, &legacy, r.Scheme); err != nil {
			return err
		}
		legacy.Spec.Background = &r.Background
		legacy.Spec.Match.Any = translateTargetsToResourceFilters(targets)
		if !unorderedEqual(legacy.Spec.Exceptions, exceptions) {
			legacy.Spec.Exceptions = exceptions
		}
		return nil
	})
	if err != nil {
		logger.Error(err, "failed to reconcile legacy PolicyException")
		GenerationErrors.WithLabelValues(APILegacy, "apply_failed").Inc()
		return err
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("legacy PolicyException reconciled", "operation", op)
	} else {
		logger.V(1).Info("legacy PolicyException unchanged")
	}
	return nil
}

// legacyExceptions lists the gspolex's policies that exist as ClusterPolicies, with their rules. A
// policy whose ClusterPolicy is missing keeps its entry in the existing legacy exception, so a
// ClusterPolicy that is deleted and recreated, for example during an upgrade, stays exempted.
func (r *PolicyExceptionReconciler) legacyExceptions(ctx context.Context, gspolex *policyAPI.PolicyException, key types.NamespacedName) ([]kyvernov2.Exception, error) {
	logger := log.FromContext(ctx).WithValues("gspolex", client.ObjectKeyFromObject(gspolex))

	var existing kyvernov2.PolicyException
	if err := r.Get(ctx, key, &existing); err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "failed to get legacy PolicyException", "policyexception", key)
		return nil, err
	}

	var exceptions []kyvernov2.Exception
	for _, name := range gspolex.Spec.Policies {
		var clusterPolicy kyvernov1.ClusterPolicy
		// The manager's client reads from a synced cache, so NotFound is authoritative even right after a restart.
		err := r.Get(ctx, types.NamespacedName{Name: name}, &clusterPolicy)
		switch {
		case err == nil:
			exceptions = append(exceptions, kyvernov2.Exception{PolicyName: name, RuleNames: generatePolicyRules(clusterPolicy)})
		case errors.IsNotFound(err):
			if entry, ok := findException(existing.Spec.Exceptions, name); ok {
				logger.V(1).Info("ClusterPolicy not found, keeping its existing entry", "policy", name)
				exceptions = append(exceptions, entry)
			} else {
				logger.V(1).Info("not a ClusterPolicy", "policy", name)
			}
		default:
			logger.Error(err, "failed to get ClusterPolicy", "policy", name)
			return nil, err
		}
	}
	return exceptions, nil
}

// findException returns the entry for policyName.
func findException(exceptions []kyvernov2.Exception, policyName string) (kyvernov2.Exception, bool) {
	for _, exception := range exceptions {
		if exception.PolicyName == policyName {
			return exception, true
		}
	}
	return kyvernov2.Exception{}, false
}

// gspolexesForClusterPolicy lists the gspolexes that name a ClusterPolicy, so that any change to it,
// including its status.autogen rules or its deletion, refreshes their legacy exceptions.
func (r *PolicyExceptionReconciler) gspolexesForClusterPolicy(ctx context.Context, clusterPolicy client.Object) []reconcile.Request {
	logger := log.FromContext(ctx).WithValues("clusterpolicy", clusterPolicy.GetName())

	var gspolexes policyAPI.PolicyExceptionList
	if err := r.List(ctx, &gspolexes); err != nil {
		logger.Error(err, "failed to list gspolexes for ClusterPolicy")
		return nil
	}
	var requests []reconcile.Request
	for _, gspolex := range gspolexes.Items {
		if slices.Contains(gspolex.Spec.Policies, clusterPolicy.GetName()) {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&gspolex)})
		}
	}
	logger.V(1).Info("gspolexes naming the ClusterPolicy", "count", len(requests))
	return requests
}

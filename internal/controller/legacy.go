package controller

import (
	"context"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
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

// reconcileLegacy keeps the kyverno.io/v2 PolicyException for a gspolex. It lists only the policies
// that exist as ClusterPolicies, and deletes the exception when there are none, the gspolex was
// migrated by exception-recommender, or legacy exceptions are switched off.
func (r *PolicyExceptionReconciler) reconcileLegacy(ctx context.Context, gspolex *policyAPI.PolicyException, namespace string) error {
	logger := log.FromContext(ctx).WithValues("gspolex", client.ObjectKeyFromObject(gspolex))

	if r.LegacyMode == LegacyAbsent {
		logger.V(1).Info("legacy exceptions skipped, kyverno.io CRDs not installed")
		return nil
	}

	var policies []kyvernov1.ClusterPolicy
	switch {
	case r.LegacyMode == LegacyCleanup:
		logger.V(1).Info("legacy exceptions switched off")
	case isMigrated(gspolex):
		logger.V(1).Info("gspolex migrated by exception-recommender, skipping legacy exception")
	default:
		for _, name := range gspolex.Spec.Policies {
			var clusterPolicy kyvernov1.ClusterPolicy
			// The manager's client reads from a synced cache, so NotFound is authoritative even right after a restart.
			err := r.Get(ctx, types.NamespacedName{Name: name}, &clusterPolicy)
			switch {
			case err == nil:
				policies = append(policies, clusterPolicy)
			case errors.IsNotFound(err):
				logger.V(1).Info("not a ClusterPolicy", "policy", name)
			default:
				logger.Error(err, "failed to get ClusterPolicy", "policy", name)
				return err
			}
		}
	}

	legacy := kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: gspolex.Name, Namespace: namespace}}
	if len(policies) == 0 {
		return deleteManaged(ctx, r.Client, &legacy)
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &legacy, func() error {
		setManagedLabels(&legacy, SourceGSPolex)
		if err := controllerutil.SetControllerReference(gspolex, &legacy, r.Scheme); err != nil {
			return err
		}
		legacy.Spec.Background = &r.Background
		legacy.Spec.Match.Any = translateTargetsToResourceFilters(gspolex.Spec.Targets)
		if newExceptions := translatePoliciesToExceptions(policies); !unorderedEqual(legacy.Spec.Exceptions, newExceptions) {
			legacy.Spec.Exceptions = newExceptions
		}
		return nil
	})
	if err != nil {
		logger.Error(err, "failed to reconcile legacy PolicyException")
		return err
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("legacy PolicyException reconciled", "operation", op)
	} else {
		logger.V(1).Info("legacy PolicyException unchanged")
	}
	return nil
}

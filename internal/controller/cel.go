package controller

import (
	"context"
	"strings"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileCEL keeps the policies.kyverno.io PolicyException for a gspolex. Every policy name is
// referenced, whether or not a CEL policy of that name exists yet.
func (r *PolicyExceptionReconciler) reconcileCEL(ctx context.Context, gspolex *policyAPI.PolicyException, namespace string) error {
	logger := log.FromContext(ctx).WithValues("gspolex", client.ObjectKeyFromObject(gspolex))

	var refs []policiesv1.PolicyRef
	var unresolved []string
	for _, name := range gspolex.Spec.Policies {
		policyRefs, resolved, err := resolvePolicyRefs(ctx, r.Client, name)
		if err != nil {
			logger.Error(err, "failed to resolve policy refs", "policy", name)
			GenerationErrors.WithLabelValues(APICEL, "lookup_failed").Inc()
			return err
		}
		if !resolved {
			unresolved = append(unresolved, name)
		}
		refs = append(refs, policyRefs...)
	}
	if len(unresolved) > 0 {
		logger.V(1).Info("unresolved policy names", "policies", unresolved)
	}
	targets := dropEmptyNames(logger, gspolex.Spec.Targets)
	bridge := isMigrated(gspolex)
	if bridge {
		logger.V(1).Info("gspolex migrated by exception-recommender, matching only the kinds its targets list")
	}
	if unsupported := unsupportedTargetKinds(targets); len(unsupported) > 0 {
		logger.V(1).Info("target kinds a CEL exception cannot express are left out", "kinds", unsupported)
	}

	celException := policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: gspolex.Name, Namespace: namespace}}
	if len(refs) == 0 {
		// policyRefs is required; a gspolex without policies exempts nothing.
		if err := deleteManaged(ctx, r.Client, &celException); err != nil {
			GenerationErrors.WithLabelValues(APICEL, "delete_failed").Inc()
			return err
		}
		return nil
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &celException, func() error {
		if err := checkManaged(&celException); err != nil {
			return err
		}
		setManagedLabels(&celException, sourceOf(gspolex))
		setAnnotation(&celException, AnnotationMigratedFrom, gspolex.Annotations[AnnotationMigratedFrom])
		setAnnotation(&celException, AnnotationUnresolvedPolicies, strings.Join(unresolved, ","))
		if err := controllerutil.SetControllerReference(gspolex, &celException, r.Scheme); err != nil {
			return err
		}
		celException.Spec.PolicyRefs = refs
		celException.Spec.MatchConditions = translateTargetsToMatchConditions(targets, bridge)
		return nil
	})
	if err != nil {
		logger.Error(err, "failed to reconcile CEL PolicyException")
		GenerationErrors.WithLabelValues(APICEL, applyFailedReason(err)).Inc()
		return err
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("CEL PolicyException reconciled", "operation", op)
	} else {
		logger.V(1).Info("CEL PolicyException unchanged")
	}
	return nil
}

/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"sort"

	policieskyvernoio "github.com/kyverno/api/api/policies.kyverno.io"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const ChartOperatorBypassName = "chart-operator-generated-sa-bypass"

// ChartOperatorBypassReconciler keeps the CEL counterpart of the chart-operator bypass: one
// policies.kyverno.io PolicyException exempting chart-operator's CREATE and UPDATE of Kinds from
// every ValidatingPolicy. Referencing every policy matches the legacy bypass, because the match
// condition already limits it to Kinds.
type ChartOperatorBypassReconciler struct {
	client.Client
	Kinds     []string
	Namespace string
}

//+kubebuilder:rbac:groups=policies.kyverno.io,resources=validatingpolicies,verbs=get;list;watch

// Reconcile rebuilds the single bypass from all ValidatingPolicies, whichever one triggered it.
func (r *ChartOperatorBypassReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	bypass := policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: ChartOperatorBypassName, Namespace: r.Namespace}}
	logger := log.FromContext(ctx).WithValues("policyexception", client.ObjectKeyFromObject(&bypass))

	var policies policiesv1.ValidatingPolicyList
	if err := r.List(ctx, &policies); err != nil {
		logger.Error(err, "failed to list ValidatingPolicies")
		GenerationErrors.WithLabelValues(APICEL, "lookup_failed").Inc()
		return ctrl.Result{}, err
	}

	if len(policies.Items) == 0 {
		if err := deleteManaged(ctx, r.Client, &bypass); err != nil {
			GenerationErrors.WithLabelValues(APICEL, "delete_failed").Inc()
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	refs := make([]policiesv1.PolicyRef, 0, len(policies.Items))
	for _, p := range policies.Items {
		refs = append(refs, policiesv1.PolicyRef{Name: p.Name, Kind: policieskyvernoio.ValidatingPolicyKind})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	logger.V(1).Info("ValidatingPolicies referenced", "count", len(refs))

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &bypass, func() error {
		setManagedLabels(&bypass, SourceChartOperator)
		bypass.Spec.PolicyRefs = refs
		bypass.Spec.MatchConditions = chartOperatorMatchConditions(r.Kinds)
		return nil
	})
	if err != nil {
		logger.Error(err, "failed to reconcile chart-operator bypass")
		GenerationErrors.WithLabelValues(APICEL, "apply_failed").Inc()
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("chart-operator bypass reconciled", "operation", op)
	} else {
		logger.V(1).Info("chart-operator bypass unchanged")
	}
	return ctrl.Result{}, nil
}

func (r *ChartOperatorBypassReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("chartoperatorbypass").
		For(&policiesv1.ValidatingPolicy{}).
		Complete(r)
}

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
	"fmt"
	"slices"
	"sort"

	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kubeutils "github.com/kyverno/kyverno/pkg/utils/kube"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/kyverno-policy-operator/internal/utils"
)

// ClusterPolicyReconciler reconciles a ClusterPolicy object
type ClusterPolicyReconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	Log                         logr.Logger
	ChartOperatorExceptionKinds []string
	PolicyCache                 map[string]kyvernov1.ClusterPolicy
	MaxJitterPercent            int
}

//+kubebuilder:rbac:groups=kyverno.io,resources=clusterpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kyverno.io,resources=clusterpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kyverno.io,resources=clusterpolicies/finalizers,verbs=update

func (r *ClusterPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var clusterPolicy kyvernov1.ClusterPolicy

	if err := r.Get(ctx, req.NamespacedName, &clusterPolicy); err != nil {
		// Check if the ClusterPolicy was deleted
		if errors.IsNotFound(err) {
			r.Log.V(1).Info("ClusterPolicy not found", "clusterpolicy", req.Name)
			// Rebuild the chart-operator bypass without it
			return ctrl.Result{}, r.reconcileChartOperatorBypass(ctx)
		}

		log.Log.Error(err, "unable to fetch ClusterPolicy")
		return ctrl.Result{}, err
	}

	if !clusterPolicy.DeletionTimestamp.IsZero() {
		delete(r.PolicyCache, clusterPolicy.Name)
	} else {
		r.PolicyCache[clusterPolicy.Name] = clusterPolicy
		r.Log.Info(fmt.Sprintf("Updated cached ClusterPolicy %s", clusterPolicy.Name))
	}

	if err := r.reconcileChartOperatorBypass(ctx); err != nil {
		return ctrl.Result{}, err
	}
	return utils.JitterRequeue(DefaultRequeueDuration, r.MaxJitterPercent, r.Log), nil
}

// reconcileChartOperatorBypass keeps the legacy chart-operator bypass: one kyverno.io/v2
// PolicyException with an entry for every ClusterPolicy that validates one of
// ChartOperatorExceptionKinds. It is rebuilt from all cached ClusterPolicies, whichever one
// triggered the reconcile, and deleted when none match.
func (r *ClusterPolicyReconciler) reconcileChartOperatorBypass(ctx context.Context) error {
	if len(r.ChartOperatorExceptionKinds) == 0 {
		return nil
	}
	bypass := kyvernov2.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: ChartOperatorBypassName, Namespace: ChartOperatorBypassNamespace}}
	logger := r.Log.WithValues("policyexception", client.ObjectKeyFromObject(&bypass))

	var clusterPolicies kyvernov1.ClusterPolicyList
	if err := r.List(ctx, &clusterPolicies); err != nil {
		logger.Error(err, "failed to list ClusterPolicies")
		GenerationErrors.WithLabelValues(APILegacy, "lookup_failed").Inc()
		return err
	}
	var policies []kyvernov1.ClusterPolicy
	for _, clusterPolicy := range clusterPolicies.Items {
		if clusterPolicy.DeletionTimestamp.IsZero() && r.validatesChartOperatorKind(clusterPolicy) {
			policies = append(policies, clusterPolicy)
		}
	}

	if len(policies) == 0 {
		logger.V(1).Info("no ClusterPolicy validates a chart-operator exception kind")
		if err := deleteManaged(log.IntoContext(ctx, logger), r.Client, &bypass); err != nil {
			GenerationErrors.WithLabelValues(APILegacy, "delete_failed").Inc()
			return err
		}
		return nil
	}

	sort.Slice(policies, func(i, j int) bool { return policies[i].Name < policies[j].Name })
	logger.V(1).Info("ClusterPolicies in the chart-operator bypass", "count", len(policies))

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &bypass, func() error {
		setManagedLabels(&bypass, SourceChartOperator)
		// Background is off, because the bypass matches on the request's user.
		background := false
		bypass.Spec.Background = &background
		bypass.Spec.Match.All = templateResourceFilters(r.ChartOperatorExceptionKinds)
		bypass.Spec.Exceptions = translatePoliciesToExceptions(policies)
		return nil
	})
	if err != nil {
		logger.Error(err, "failed to reconcile legacy chart-operator bypass")
		GenerationErrors.WithLabelValues(APILegacy, "apply_failed").Inc()
		return err
	}
	if op != controllerutil.OperationResultNone {
		logger.Info("legacy chart-operator bypass reconciled", "operation", op)
	} else {
		logger.V(1).Info("legacy chart-operator bypass unchanged")
	}
	return nil
}

// validatesChartOperatorKind reports whether a validate rule of the ClusterPolicy matches one of
// ChartOperatorExceptionKinds. Rules may write the kind as "group/version/Kind", so only the kind is
// compared.
func (r *ClusterPolicyReconciler) validatesChartOperatorKind(clusterPolicy kyvernov1.ClusterPolicy) bool {
	for _, rule := range clusterPolicy.Spec.Rules {
		if !rule.HasValidate() {
			continue
		}
		for _, kind := range rule.MatchResources.GetKinds() {
			_, _, kind, _ = kubeutils.ParseKindSelector(kind)
			if slices.Contains(r.ChartOperatorExceptionKinds, kind) {
				return true
			}
		}
	}
	return false
}

func templateResourceFilters(kinds []string) kyvernov1.ResourceFilters {
	var resourceFilters kyvernov1.ResourceFilters
	translatedResourceFilter := kyvernov1.ResourceFilter{
		UserInfo: kyvernov1.UserInfo{
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      "chart-operator",
				Namespace: ChartOperatorBypassNamespace,
			}},
		},
		ResourceDescription: kyvernov1.ResourceDescription{
			Kinds:      kinds,
			Operations: []kyvernov1.AdmissionOperation{"CREATE", "UPDATE"},
		},
	}
	resourceFilters = append(resourceFilters, translatedResourceFilter)

	return resourceFilters
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&kyvernov1.ClusterPolicy{}).
		Complete(r)
}

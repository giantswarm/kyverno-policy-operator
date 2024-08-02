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

	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/kyverno-policy-operator/internal/utils"
)

// ClusterPolicyReconciler reconciles a ClusterPolicy object
type ClusterPolicyReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Log              logr.Logger
	ExceptionList    map[string]kyvernov1.ClusterPolicy
	ExceptionKinds   []string
	PolicyCache      map[string]kyvernov1.ClusterPolicy
	MaxJitterPercent int
}

//+kubebuilder:rbac:groups=kyverno.io,resources=clusterpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=kyverno.io,resources=clusterpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=kyverno.io,resources=clusterpolicies/finalizers,verbs=update

func (r *ClusterPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("clusterpolicy", req.NamespacedName)

	var clusterPolicy kyvernov1.ClusterPolicy

	if err := r.Get(ctx, req.NamespacedName, &clusterPolicy); err != nil {
		// Error fetching the report

		// Check if the ClusterPolicy was deleted
		if apierrors.IsNotFound(err) {
			// Ignore
			return ctrl.Result{}, nil
		}

		log.Log.Error(err, "unable to fetch ClusterPolicy")
		return ctrl.Result{}, client.IgnoreNotFound(err)

	}

	if !clusterPolicy.DeletionTimestamp.IsZero() {
		delete(r.PolicyCache, clusterPolicy.Name)
	} else {
		r.PolicyCache[clusterPolicy.Name] = clusterPolicy
		r.Log.Info(fmt.Sprintf("Updated cached ClusterPolicy %s", clusterPolicy.Name))
	}

	if len(r.ExceptionKinds) != 0 {
		// Check if the Policy has validate rules
		if !clusterPolicy.HasValidate() {
			return ctrl.Result{}, nil
		}

		// Inspect the Rules
		for _, rule := range clusterPolicy.Spec.Rules {
			// Check if the rule has a validate section
			if rule.HasValidate() {
				for _, kind := range rule.MatchResources.GetKinds() {
					// Check for Namespace validation
					for _, destinationKind := range r.ExceptionKinds {
						if kind == destinationKind {
							// Append exception to PolicyException
							if _, exists := r.ExceptionList[clusterPolicy.Name]; !exists {
								r.ExceptionList[clusterPolicy.Name] = clusterPolicy

								// Template Kyverno Polex
								policyException := kyvernov2beta1.PolicyException{}

								// Set namespace
								policyException.Namespace = "giantswarm"

								// Set name
								policyException.Name = "chart-operator-generated-sa-bypass"

								// Set labels
								policyException.Labels = generateLabels()

								// Set Background behaviour to false since this Polex is using Subjects
								background := false
								policyException.Spec.Background = &background

								// Set Spec.Match.All
								policyException.Spec.Match.All = templateResourceFilters(r.ExceptionKinds)

								policies := []kyvernov1.ClusterPolicy{clusterPolicy}

								// Set .Spec.Exceptions
								newExceptions := translatePoliciesToExceptions(policies)
								policyException.Spec.Exceptions = newExceptions

								// Patch PolicyException Kinds
								gvks, unversioned, err := r.Scheme.ObjectKinds(&policyException)
								if err != nil {
									return ctrl.Result{}, err
								}
								if !unversioned && len(gvks) == 1 {
									policyException.SetGroupVersionKind(gvks[0])
								}

								if err := r.CreateOrUpdate(ctx, &policyException); err != nil {
									log.Log.Error(err, "Error creating PolicyException")
								} else {
									log.Log.Info(fmt.Sprintf("ClusterPolicy %s triggered a PolicyException update: %s", clusterPolicy.Name, client.ObjectKeyFromObject(&policyException)))
								}

								return ctrl.Result{}, nil
							}
						}
					}
				}
			}
		}
	}
	return utils.JitterRequeue(DefaultRequeueDuration, r.MaxJitterPercent, r.Log), nil
}

// CreateOrUpdate attempts first to patch the object given but if an IsNotFound error
// is returned it instead creates the resource.
func (r *ClusterPolicyReconciler) CreateOrUpdate(ctx context.Context, obj client.Object) error {
	existingObj := unstructured.Unstructured{}
	existingObj.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())

	err := r.Get(ctx, client.ObjectKeyFromObject(obj), &existingObj)
	switch {
	case err == nil:
		// Update:
		obj.SetResourceVersion(existingObj.GetResourceVersion())
		obj.SetUID(existingObj.GetUID())
		return r.Patch(ctx, obj, client.MergeFrom(existingObj.DeepCopy()))
	case errors.IsNotFound(err):
		// Create:
		return r.Create(ctx, obj)
	default:
		return err
	}
}

func templateResourceFilters(kinds []string) kyvernov1.ResourceFilters {
	var resourceFilters kyvernov1.ResourceFilters
	translatedResourceFilter := kyvernov1.ResourceFilter{
		UserInfo: kyvernov1.UserInfo{
			Subjects: []rbacv1.Subject{{
				Kind:      "ServiceAccount",
				Name:      "chart-operator",
				Namespace: "giantswarm",
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

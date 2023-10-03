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

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2alpha1 "github.com/kyverno/kyverno/api/kyverno/v2alpha1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	giantswarmPolicy "github.com/giantswarm/kyverno-policy-operator/api/v1alpha1"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PolicyExceptionReconciler reconciles a PolicyException object
type PolicyExceptionReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
}

//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policyexceptions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policyexceptions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policyexceptions/finalizers,verbs=update

func (r *PolicyExceptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("policyexception", req.NamespacedName)

	var gsPolicyException giantswarmPolicy.PolicyException
	// This should be a flag
	background := false

	if err := r.Get(ctx, req.NamespacedName, &gsPolicyException); err != nil {
		// Error fetching the report

		// Check if the PolicyException was deleted
		if apierrors.IsNotFound(err) {
			// Ignore
			return ctrl.Result{}, nil
		}

		log.Log.Error(err, "unable to fetch PolicyException")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Define namespace
	var namespace string
	if r.DestinationNamespace == "" {
		namespace = gsPolicyException.Namespace
	} else {
		namespace = r.DestinationNamespace
	}

	// Create Kyverno exception
	// Create a policy map for storing cluster policies to extract rules later
	policyMap := make(map[string]kyvernov1.ClusterPolicy)
	for _, policy := range gsPolicyException.Spec.Policies {
		var kyvernoPolicy kyvernov1.ClusterPolicy
		if err := r.Get(ctx, types.NamespacedName{Namespace: "", Name: policy}, &kyvernoPolicy); err != nil {
			// Error fetching the report
			log.Log.Error(err, "unable to fetch Kyverno Policy")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		policyMap[policy] = kyvernoPolicy
	}
	// Translate GiantSwarm PolicyException to Kyverno's PolicyException schema
	policyException := kyvernov2alpha1.PolicyException{}
	// Set namespace
	policyException.Namespace = namespace
	// Set name
	policyException.Name = gsPolicyException.Name
	// Set Background behaviour
	policyException.Spec.Background = &background
	// Set labels
	policyException.Labels = make(map[string]string)
	policyException.Labels["app.kubernetes.io/managed-by"] = "kyverno-policy-operator"
	// Set ownerReferences
	if err := controllerutil.SetControllerReference(&gsPolicyException, &policyException, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Create PolicyException
	if op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &policyException, func() error {

		// Set .Spec.Match.Any targets
		policyException.Spec.Match.Any = translateTargetsToResourceFilters(gsPolicyException)

		// Set .Spec.Exceptions
		newExceptions := translatePoliciesToExceptions(policyMap)
		if !deepEquals(policyException.Spec.Exceptions, newExceptions) {
			policyException.Spec.Exceptions = newExceptions
		}

		return nil
	}); err != nil {
		log.Log.Error(err, fmt.Sprintf("Reconciliation failed for PolicyException %s", policyException.Name))
		return ctrl.Result{}, err
	} else {
		log.Log.Info(fmt.Sprintf("PolicyException %s: %s", policyException.Name, op))
	}

	return ctrl.Result{}, nil
}

func deepEquals(got []kyvernov2alpha1.Exception, want []kyvernov2alpha1.Exception) bool {
	// Check Length size first
	if len(got) != len(want) {
		return false
	}
	// Create an exceptions map with the new desired Exceptions
	exceptionMap := make(map[string][]string)
	for _, exception := range want {
		exceptionMap[exception.PolicyName] = exception.RuleNames
	}
	for _, exception := range got {
		// Check if the Policy Name is still present in the new Exceptions
		if _, exists := exceptionMap[exception.PolicyName]; !exists {
			// The Policy is not present in the new array
			// Arrays are not equals, exit
			return false
		} else {
			// Check if the same RuleNames are still present in the new Exceptions
			for _, oldRule := range exception.RuleNames {
				found := false
				// Check against every rule, exit if found
				for _, newRule := range exceptionMap[exception.PolicyName] {
					if newRule == oldRule {
						// Found, break for
						found = true
						break
					}
				}
				if !found {
					// The arrays are not equals, exit
					return false
				}
				// Rules are equals, continue
			}
		}
	}
	// Arrays are equals
	return true
}

func translateTargetsToResourceFilters(polex giantswarmPolicy.PolicyException) kyvernov1.ResourceFilters {
	resourceFilters := kyvernov1.ResourceFilters{}
	for _, target := range polex.Spec.Targets {
		trasnlatedResourceFilter := kyvernov1.ResourceFilter{
			ResourceDescription: kyvernov1.ResourceDescription{
				Namespaces: target.Namespaces,
				Names:      target.Names,
				Kinds:      generateExceptionKinds(target.Kind),
			},
		}
		resourceFilters = append(resourceFilters, trasnlatedResourceFilter)
	}
	return resourceFilters
}

// generateKinds creates the subresources necessary for top level controllers like Deployment or StatefulSet
func generateExceptionKinds(resourceKind string) []string {
	// Adds the subresources to the exception list for each Kind
	var exceptionKinds []string
	exceptionKinds = append(exceptionKinds, resourceKind)
	// Append ReplicaSets
	if resourceKind == "Deployment" {
		exceptionKinds = append(exceptionKinds, "ReplicaSet")
		// Append Jobs
	} else if resourceKind == "CronJob" {
		exceptionKinds = append(exceptionKinds, "Job")
	}
	// Always append Pods except if they are the initial resource Kind
	if resourceKind != "Pod" {
		exceptionKinds = append(exceptionKinds, "Pod")
	}

	return exceptionKinds
}

// translatePoliciesToExceptions takes a Giant Swarm Policies array and transforms it into a Kyverno Exception array
func translatePoliciesToExceptions(policies map[string]kyvernov1.ClusterPolicy) []kyvernov2alpha1.Exception {
	var exceptionArray []kyvernov2alpha1.Exception
	for policyName, kyvernoPolicy := range policies {
		kyvernoException := kyvernov2alpha1.Exception{
			PolicyName: policyName,
			RuleNames:  generatePolicyRules(kyvernoPolicy),
		}
		exceptionArray = append(exceptionArray, kyvernoException)
	}

	return exceptionArray
}

// generatePolicyRules takes a Kyverno Policy name and generates a list of rules owned by that policy
func generatePolicyRules(kyvernoPolicy kyvernov1.ClusterPolicy) []string {
	var rulesArray []string
	for _, rule := range kyvernoPolicy.Spec.Rules {
		rulesArray = append(rulesArray, rule.Name)
	}
	for _, autogenRule := range kyvernoPolicy.Status.Autogen.Rules {
		rulesArray = append(rulesArray, autogenRule.Name)
	}

	return rulesArray
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyExceptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&giantswarmPolicy.PolicyException{}).
		Owns(&kyvernov2alpha1.PolicyException{}).
		Complete(r)
}
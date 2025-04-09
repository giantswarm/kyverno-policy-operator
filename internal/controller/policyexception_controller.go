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

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/kyverno-policy-operator/internal/utils"
)

const (
	KindDeployment = "Deployment"
	KindReplicaSet = "ReplicaSet"
	KindCronJob    = "CronJob"
	KindJob        = "Job"
	KindPod        = "Pod"
)

type PolicyExceptionReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
	Background           bool
	MaxJitterPercent     int
}

//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policyexceptions,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policyexceptions/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.giantswarm.io,resources=policyexceptions/finalizers,verbs=update

func (r *PolicyExceptionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("policyexception", req.NamespacedName)

	var gsPolicyException policyAPI.PolicyException

	if err := r.Get(ctx, req.NamespacedName, &gsPolicyException); err != nil {
		// Error fetching the report

		// Check if the PolicyException was deleted
		if errors.IsNotFound(err) {
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
	// TODO: Take this block out and move it to utils
	var policies []kyvernov1.ClusterPolicy
	for _, policy := range gsPolicyException.Spec.Policies {
		var kyvernoPolicy kyvernov1.ClusterPolicy
		if err := r.Get(ctx, types.NamespacedName{Namespace: "", Name: policy}, &kyvernoPolicy); err != nil {
			// Error fetching the report
			log.Log.Error(err, "unable to fetch Kyverno Policy")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		policies = append(policies, kyvernoPolicy)
	}
	// Translate GiantSwarm PolicyException to Kyverno's PolicyException schema
	policyException := kyvernov2beta1.PolicyException{}
	// Set namespace
	policyException.Namespace = namespace
	// Set name
	policyException.Name = gsPolicyException.Name

	// Set labels
	policyException.Labels = generateLabels()
	// Set ownerReferences
	if err := controllerutil.SetControllerReference(&gsPolicyException, &policyException, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	// Create PolicyException
	if op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &policyException, func() error {

		// Set Background behaviour
		policyException.Spec.Background = &r.Background

		// Set .Spec.Match.Any targets
		policyException.Spec.Match.Any = translateTargetsToResourceFilters(gsPolicyException.Spec.Targets)

		// Set .Spec.Exceptions
		newExceptions := translatePoliciesToExceptions(policies)
		if !unorderedEqual(policyException.Spec.Exceptions, newExceptions) {
			policyException.Spec.Exceptions = newExceptions
		}

		return nil
	}); err != nil {
		log.Log.Error(err, fmt.Sprintf("Reconciliation failed for PolicyException %s", policyException.Name))
		return ctrl.Result{}, err
	} else {
		log.Log.Info(fmt.Sprintf("PolicyException %s: %s", policyException.Name, op))
	}

	return utils.JitterRequeue(DefaultRequeueDuration, r.MaxJitterPercent, r.Log), nil
}

// CreateOrUpdate attempts first to patch the object given but if an IsNotFound error
// is returned it instead creates the resource.
func (r *PolicyExceptionReconciler) CreateOrUpdate(ctx context.Context, obj client.Object) error {
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

// generateKinds creates the subresources necessary for top level controllers like Deployment or StatefulSet
func generateExceptionKinds(resourceKind string) []string {
	exceptionKinds := []string{resourceKind}

	switch resourceKind {
	case KindDeployment:
		exceptionKinds = append(exceptionKinds, KindReplicaSet)
	case KindCronJob:
		exceptionKinds = append(exceptionKinds, KindJob)
	case KindPod:
		// Special case: if resourceKind is Pod, don't add Pod again
		return exceptionKinds
	}

	// For all resource kinds except Pod, add Pod
	exceptionKinds = append(exceptionKinds, KindPod)
	return exceptionKinds
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyExceptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&policyAPI.PolicyException{}).
		Owns(&kyvernov2beta1.PolicyException{}).
		Complete(r)
}

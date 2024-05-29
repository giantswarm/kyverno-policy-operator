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
	"github.com/go-logr/logr"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	utils "github.com/giantswarm/kyverno-policy-operator/internal/utils"
)

// PolicyManifestReconciler reconciles a PolicyManifest object
type PolicyManifestReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
	Background           bool
	PolicyCache          map[string]kyvernov1.ClusterPolicy
	MaxJitterPercent     int
}

//+kubebuilder:rbac:groups=giantswarm.io,resources=policymanifests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=giantswarm.io,resources=policymanifests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=giantswarm.io,resources=policymanifests/finalizers,verbs=update

func (r *PolicyManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var polman policyAPI.PolicyManifest
	{
		if err := r.Get(ctx, req.NamespacedName, &polman); err != nil {
			// Error fetching the policy manifest

			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}

			log.Log.Error(err, "unable to fetch policy manifest")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		log.Log.Error(err, "unable to fetch policy manifest")
		FailedPolicyManifestControllerReconciliations.Inc()
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	kyvernoPolicyException := kyvernov2beta1.PolicyException{}
	// Set kyvernoPolicyException destination namespace.
	kyvernoPolicyException.Namespace = r.DestinationNamespace
	// Set kyvernoPolicyException name.
	kyvernoPolicyException.Name = fmt.Sprintf("gs-kpo-%s-exceptions", polman.ObjectMeta.Name)
	// Set labels.
	kyvernoPolicyException.Labels = generateLabels()
	kyvernoPolicyException.Labels["policy.giantswarm.io/policy"] = polman.ObjectMeta.Labels["policy.giantswarm.io/policy"]

	kyvernoPolicyException.Spec.Background = &r.Background

	allTargets := make([]policyAPI.Target, len(polman.Spec.Exceptions)+len(polman.Spec.AutomatedExceptions))
	copy(allTargets, polman.Spec.Exceptions)
	copy(allTargets[len(polman.Spec.Exceptions):], polman.Spec.AutomatedExceptions)

	var kyvernoPolicy kyvernov1.ClusterPolicy
	var ok bool

	if kyvernoPolicy, ok = r.PolicyCache[polman.Name]; !ok {
		log.Log.Error(fmt.Errorf("Policy %s not found in cache", polman.Name), "unable to fetch Kyverno Policy from cache")
		return ctrl.Result{Requeue: true}, nil
	}

	policies := []kyvernov1.ClusterPolicy{kyvernoPolicy}

	newExceptions := translatePoliciesToExceptions(policies)

	// create or update a Kyverno PolicyException.

	if op, err := controllerutil.CreateOrUpdate(ctx, r.Client, &kyvernoPolicyException, func() error {

		kyvernoPolicyException.Spec.Match.Any = translateTargetsToResourceFilters(allTargets)

		kyvernoPolicyException.Spec.Exceptions = newExceptions

		return nil
	}); err != nil {
		log.Log.Error(err, fmt.Sprintf("Reconciliation failed for PolicyException %s", kyvernoPolicyException.Name))
		FailedPolicyManifestControllerReconciliations.Inc()
		return ctrl.Result{}, err
	} else {
		log.Log.Info(fmt.Sprintf("PolicyException %s: %s", kyvernoPolicyException.Name, op))
	}

	return utils.JitterRequeue(DefaultRequeueDuration, r.MaxJitterPercent, r.Log), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		// For().
		For(&policyAPI.PolicyManifest{}).
		Complete(r)
}

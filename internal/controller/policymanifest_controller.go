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

	exceptionRecommender "github.com/giantswarm/exception-recommender/api/v1alpha1"
	"github.com/go-logr/logr"
	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PolicyManifestReconciler reconciles a PolicyManifest object
type PolicyManifestReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
}

//+kubebuilder:rbac:groups=giantswarm.io,resources=policymanifests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=giantswarm.io,resources=policymanifests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=giantswarm.io,resources=policymanifests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PolicyManifest object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.0/pkg/reconcile
func (r *PolicyManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var polman exceptionRecommender.PolicyManifest

	if err := r.Get(ctx, req.NamespacedName, &polman); err != nil {
		// Error fetching the policy manifest

		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		log.Log.Error(err, "unable to fetch policy manifest")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// TODO(user): your logic here

	// kpe stands for Kyverno Policy Exception
	kpe := kyvernov2beta1.PolicyException{}
	// Set kpe destination namespace.
	kpe.Namespace = r.DestinationNamespace
	// Set kpe name.
	kpe.Name = fmt.Sprintf("GS-KPO-%s-exceptions", polman.ObjectMeta.Name)
	// Set labels.
	kpe.Labels = make(map[string]string)
	kpe.Labels["app.kubernetes.io/managed-by"] = "kyverno-policy-operator"
	kpe.Labels["policy.giantswarm.io/policy"] = polman.ObjectMeta.Labels["policy.giantswarm.io/policy"]

	// Set owner references.
	if err := controllerutil.SetControllerReference(&polman, &kpe, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// create or update

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		// For().
		Complete(r)
}

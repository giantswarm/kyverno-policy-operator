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

	giantswarmExceptions "github.com/giantswarm/exception-recommender/api/v1alpha1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2alpha1 "github.com/kyverno/kyverno/api/kyverno/v2alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PolicyExceptionDraftReconciler reconciles a PolicyExceptionDraft object
type PolicyExceptionDraftReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
}

//+kubebuilder:rbac:groups=policy.giantswarm.io.giantswarm.io,resources=policyexceptiondrafts,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=policy.giantswarm.io.giantswarm.io,resources=policyexceptiondrafts/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=policy.giantswarm.io.giantswarm.io,resources=policyexceptiondrafts/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PolicyExceptionDraft object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.15.0/pkg/reconcile
func (r *PolicyExceptionDraftReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("policyexceptiondraft", req.NamespacedName)

	var exceptionDraft giantswarmExceptions.PolicyExceptionDraft
	background := false

	if err := r.Get(ctx, req.NamespacedName, &exceptionDraft); err != nil {
		// Error fetching the report
		log.Log.Error(err, "unable to fetch PolicyExceptionDraft")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Define namespace
	var namespace string
	if r.DestinationNamespace == "" {
		namespace = exceptionDraft.Namespace
	} else {
		namespace = r.DestinationNamespace
	}

	// Check if the draft hast been reconciled before
	if _, exists := exceptionDraft.Labels["kyverno-policy-operator/reconciled"]; !exists {
		// Label doesn't exist, add it
		if exceptionDraft.Labels == nil {
			exceptionDraft.Labels = make(map[string]string)
		}
		exceptionDraft.Labels["kyverno-policy-operator/reconciled"] = "true"

		// Update Kubernetes object
		if err := r.Client.Update(ctx, &exceptionDraft, &client.UpdateOptions{}); err != nil {
			r.Log.Error(err, "unable to update PolicyExceptionDraft")
		}

		// Also create Kyverno exception
		// Translate GiantSwarm PolicyExceptionDraft to Kyverno's PolicyException schema
		policyException := translateDraftToPolex(exceptionDraft)
		// Set namespace
		policyException.Namespace = namespace
		// Set name
		policyException.Name = exceptionDraft.Name
		// Set labels
		policyException.Labels = make(map[string]string)
		policyException.Labels["app.kubernetes.io/managed-by"] = "kyverno-policy-operator"
		// Set Background behaviour
		policyException.Spec.Background = &background

		// Set ownerReferences
		if err := controllerutil.SetControllerReference(&exceptionDraft, &policyException, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}

		// Create PolicyException
		if err := r.Create(ctx, &policyException); err != nil {
			if apierrors.IsAlreadyExists(err) {
				log.Log.Info(fmt.Sprintf("PolicyException %s/%s already exists", namespace, policyException.Name))
				return ctrl.Result{}, nil
			} else {
				log.Log.Error(err, "unable to create PolicyException")
			}
		} else {
			log.Log.Info(fmt.Sprintf("Created PolicyException %s/%s", namespace, policyException.Name))
		}
	} else {
		// Exception must exist since draft was previously reconciled
		var policyException kyvernov2alpha1.PolicyException

		if err := r.Get(ctx, types.NamespacedName{Namespace: namespace, Name: exceptionDraft.Name}, &policyException); err != nil {
			// Error fetching the report
			log.Log.Error(err, "unable to fetch PolicyException")
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}

		// Set new exceptions
		policyException.Spec.Exceptions = trasnlateExceptions(exceptionDraft.Spec.Exceptions)
		// Update Kubernetes object
		if err := r.Client.Update(ctx, &policyException, &client.UpdateOptions{}); err != nil {
			r.Log.Error(err, "unable to update PolicyException")
		} else {
			log.Log.Info(fmt.Sprintf("Updated PolicyException %s/%s", namespace, policyException.Name))
		}
	}

	return ctrl.Result{}, nil
}

func translateDraftToPolex(draft giantswarmExceptions.PolicyExceptionDraft) kyvernov2alpha1.PolicyException {
	polex := kyvernov2alpha1.PolicyException{}

	polex.Spec.Match.All = kyvernov1.ResourceFilters{kyvernov1.ResourceFilter{
		ResourceDescription: kyvernov1.ResourceDescription{
			Namespaces: draft.Spec.Match.Namespaces,
			Names:      draft.Spec.Match.Names,
			Kinds:      draft.Spec.Match.Kinds,
		}}}
	polex.Spec.Exceptions = trasnlateExceptions(draft.Spec.Exceptions)

	return polex
}

func trasnlateExceptions(exceptions []giantswarmExceptions.Exception) []kyvernov2alpha1.Exception {
	var kyvernoExceptions []kyvernov2alpha1.Exception
	for _, exception := range exceptions {
		kyvernoExceptions = append(kyvernoExceptions, kyvernov2alpha1.Exception(exception))
	}

	return kyvernoExceptions
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyExceptionDraftReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Uncomment the following line adding a pointer to an instance of the controlled resource as an argument
		For(&giantswarmExceptions.PolicyExceptionDraft{}).
		Owns(&kyvernov2alpha1.PolicyException{}).
		Complete(r)
}

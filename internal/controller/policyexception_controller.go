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

	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"

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

// PolicyExceptionReconciler reconciles a PolicyException object
type PolicyExceptionReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	Log                  logr.Logger
	DestinationNamespace string
	Background           bool
	MaxJitterPercent     int
	LegacyMode           LegacyMode
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

	if err := r.reconcileLegacy(ctx, &gsPolicyException, namespace); err != nil {
		log.Log.Error(err, fmt.Sprintf("Legacy reconciliation failed for PolicyException %s", gsPolicyException.Name))
		return ctrl.Result{}, err
	}

	return utils.JitterRequeue(DefaultRequeueDuration, r.MaxJitterPercent, r.Log), nil
}

// generateKinds creates the subresources necessary for top level controllers like Deployment or StatefulSet
func generateExceptionKinds(resourceKind string) []string {
	exceptionKinds := []string{resourceKind}

	switch resourceKind {
	case KindDeployment:
		exceptionKinds = append(exceptionKinds, KindReplicaSet)
	case KindCronJob:
		exceptionKinds = append(exceptionKinds, KindJob)
	}

	if resourceKind != KindPod {
		exceptionKinds = append(exceptionKinds, KindPod)
	}

	return exceptionKinds
}

// SetupWithManager sets up the controller with the Manager.
func (r *PolicyExceptionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := ctrl.NewControllerManagedBy(mgr).For(&policyAPI.PolicyException{})
	if r.LegacyMode == LegacyWrite {
		b = b.Owns(&kyvernov2.PolicyException{})
	}
	return b.Complete(r)
}

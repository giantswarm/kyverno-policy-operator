package controller

import (
	"context"
	"errors"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"github.com/go-logr/logr"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func testGSPolex() *policyAPI.PolicyException {
	return &policyAPI.PolicyException{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "policy-exceptions"},
		Spec: policyAPI.PolicyExceptionSpec{
			Policies: []string{"disallow-privileged"},
			Targets:  []policyAPI.Target{{Kind: "Deployment", Namespaces: []string{"team-a"}, Names: []string{"app"}}},
		},
	}
}

func newScheme(t *testing.T, install ...func(*runtime.Scheme) error) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	for _, fn := range install {
		if err := fn(s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestReconcileWritesLegacyWhenCELFails(t *testing.T) {
	s := newScheme(t, policyAPI.AddToScheme, policiesv1.Install, kyvernov1.Install, kyvernov2.Install)
	errCEL := errors.New("CEL write failed")
	failCEL := func(obj client.Object) error {
		if _, ok := obj.(*policiesv1.PolicyException); ok {
			return errCEL
		}
		return nil
	}
	c := fake.NewClientBuilder().WithScheme(s).
		WithObjects(testGSPolex(), &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "disallow-privileged"}}).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if err := failCEL(obj); err != nil {
					return err
				}
				return c.Create(ctx, obj, opts...)
			},
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if err := failCEL(obj); err != nil {
					return err
				}
				return c.Update(ctx, obj, opts...)
			},
		}).Build()

	r := &PolicyExceptionReconciler{Client: c, Scheme: s, Log: logr.Discard(), DestinationNamespace: "policy-exceptions", MaxJitterPercent: 10, LegacyMode: LegacyWrite}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}})
	if !errors.Is(err, errCEL) {
		t.Fatalf("got error %v, want the CEL write error", err)
	}

	var legacy kyvernov2.PolicyException
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}, &legacy); err != nil {
		t.Fatalf("legacy PolicyException not written: %v", err)
	}
	if len(legacy.Spec.Exceptions) != 1 || legacy.Spec.Exceptions[0].PolicyName != "disallow-privileged" {
		t.Errorf("unexpected legacy exceptions: %+v", legacy.Spec.Exceptions)
	}
}

// With LegacyAbsent the kyverno.io types are not in the scheme, so any call touching them fails.
func TestReconcileLegacyAbsent(t *testing.T) {
	s := newScheme(t, policyAPI.AddToScheme, policiesv1.Install)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(testGSPolex()).Build()

	r := &PolicyExceptionReconciler{Client: c, Scheme: s, Log: logr.Discard(), DestinationNamespace: "policy-exceptions", MaxJitterPercent: 10, LegacyMode: LegacyAbsent}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}}); err != nil {
		t.Fatal(err)
	}

	var cel policiesv1.PolicyException
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}, &cel); err != nil {
		t.Fatalf("CEL PolicyException not written: %v", err)
	}
}

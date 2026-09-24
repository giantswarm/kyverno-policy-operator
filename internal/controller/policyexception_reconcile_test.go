package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	"github.com/go-logr/logr"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"github.com/prometheus/client_golang/prometheus/testutil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// TestReconcileErrors covers reconciles that fail. Each case checks the error, the error counter,
// and which exceptions were still written.
func TestReconcileErrors(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}
	clusterPolicy := &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "disallow-privileged"}}
	errAPI := errors.New("API server unavailable")
	exists := func(t *testing.T, c client.Client, obj client.Object) bool {
		t.Helper()
		err := c.Get(ctx, key, obj)
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatal(err)
		}
		return err == nil
	}

	tests := []struct {
		name       string
		gspolex    func() *policyAPI.PolicyException
		objects    []client.Object
		funcs      interceptor.Funcs
		wantErr    string
		api        string
		reason     string
		wantCEL    bool
		wantLegacy bool
	}{
		{
			// A gspolex outside the destination namespace cannot own the generated exceptions.
			// This is also what a same-name gspolex in another namespace runs into.
			name: "gspolex outside the destination namespace",
			gspolex: func() *policyAPI.PolicyException {
				g := testGSPolex()
				g.Namespace = "team-a"
				return g
			},
			objects: []client.Object{clusterPolicy},
			wantErr: "cross-namespace owner references are disallowed",
			api:     APICEL, reason: "apply_failed",
		},
		{
			name:    "CEL policy lookup fails, the legacy exception is still written",
			gspolex: testGSPolex,
			objects: []client.Object{clusterPolicy},
			funcs: interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*policiesv1.ValidatingPolicy); ok {
					return errAPI
				}
				return c.Get(ctx, key, obj, opts...)
			}},
			wantErr: errAPI.Error(),
			api:     APICEL, reason: "lookup_failed",
			wantLegacy: true,
		},
		{
			name:    "legacy write fails, the CEL exception is still written",
			gspolex: testGSPolex,
			objects: []client.Object{clusterPolicy},
			funcs: interceptor.Funcs{Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				if _, ok := obj.(*kyvernov2.PolicyException); ok {
					return errAPI
				}
				return c.Create(ctx, obj, opts...)
			}},
			wantErr: errAPI.Error(),
			api:     APILegacy, reason: "apply_failed",
			wantCEL: true,
		},
		{
			name: "deleting the CEL exception of a gspolex without policies fails",
			gspolex: func() *policyAPI.PolicyException {
				g := testGSPolex()
				g.Spec.Policies = nil
				return g
			},
			objects: []client.Object{&policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{
				Name: key.Name, Namespace: key.Namespace, Labels: map[string]string{ManagedBy: ComponentName},
			}}},
			funcs: interceptor.Funcs{Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return errAPI
			}},
			wantErr: errAPI.Error(),
			api:     APICEL, reason: "delete_failed",
			wantCEL: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScheme(t, policyAPI.AddToScheme, policiesv1.Install, kyvernov1.Install, kyvernov2.Install)
			gspolex := tt.gspolex()
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(append(tt.objects, gspolex)...).WithInterceptorFuncs(tt.funcs).Build()
			counter := GenerationErrors.WithLabelValues(tt.api, tt.reason)
			before := testutil.ToFloat64(counter)

			r := &PolicyExceptionReconciler{Client: c, Scheme: s, Log: logr.Discard(), DestinationNamespace: "policy-exceptions", MaxJitterPercent: 10, LegacyMode: LegacyWrite}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gspolex)})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got error %v, want one containing %q", err, tt.wantErr)
			}
			if got := testutil.ToFloat64(counter) - before; got < 1 {
				t.Errorf("generation_errors_total{api=%q,reason=%q} did not increase", tt.api, tt.reason)
			}
			if got := exists(t, c, &policiesv1.PolicyException{}); got != tt.wantCEL {
				t.Errorf("CEL exception exists: got %v, want %v", got, tt.wantCEL)
			}
			if got := exists(t, c, &kyvernov2.PolicyException{}); got != tt.wantLegacy {
				t.Errorf("legacy exception exists: got %v, want %v", got, tt.wantLegacy)
			}
		})
	}
}

// A bridge's CEL exception covers only the kinds its targets list; any other gspolex's also covers
// the pods of a Deployment target.
func TestReconcileCELBridge(t *testing.T) {
	ctx := context.Background()
	pod := obj("Pod", "team-a", "", "app-5d4f8-")
	tests := []struct {
		name        string
		annotations map[string]string
		labels      map[string]string
		wantPod     bool
	}{
		{"bridge", map[string]string{AnnotationMigratedFrom: "team-a/app"}, map[string]string{ManagedBy: "exception-recommender"}, false},
		{"annotation without exception-recommender's label", map[string]string{AnnotationMigratedFrom: "team-a/app"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScheme(t, policyAPI.AddToScheme, policiesv1.Install)
			gspolex := testGSPolex()
			gspolex.Annotations = tt.annotations
			gspolex.Labels = tt.labels
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(gspolex).Build()
			r := &PolicyExceptionReconciler{Client: c, Scheme: s, Log: logr.Discard(), DestinationNamespace: "policy-exceptions", MaxJitterPercent: 10, LegacyMode: LegacyAbsent}
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gspolex)}); err != nil {
				t.Fatal(err)
			}
			var cel policiesv1.PolicyException
			if err := c.Get(ctx, client.ObjectKeyFromObject(gspolex), &cel); err != nil {
				t.Fatal(err)
			}
			if got := eval(t, cel.Spec.MatchConditions, obj("Deployment", "team-a", "app", ""), nil); !got {
				t.Error("the CEL exception does not match the target Deployment")
			}
			if got := eval(t, cel.Spec.MatchConditions, pod, nil); got != tt.wantPod {
				t.Errorf("matches the Deployment's pod: got %v, want %v", got, tt.wantPod)
			}
		})
	}
}

// Empty target names never panic and never widen an exception to every name.
func TestReconcileEmptyTargetNames(t *testing.T) {
	ctx := context.Background()
	key := types.NamespacedName{Namespace: "policy-exceptions", Name: "app"}
	tests := []struct {
		name       string
		names      []string
		wantLegacy bool
		matchesFoo bool
		matchesBar bool
	}{
		{"only an empty name", []string{""}, false, false, false},
		{"an empty name next to another", []string{"", "foo"}, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScheme(t, policyAPI.AddToScheme, policiesv1.Install, kyvernov1.Install, kyvernov2.Install)
			gspolex := testGSPolex()
			gspolex.Spec.Targets = []policyAPI.Target{{Kind: "ConfigMap", Namespaces: []string{"team-a"}, Names: tt.names}}
			c := fake.NewClientBuilder().WithScheme(s).
				WithObjects(gspolex, &kyvernov1.ClusterPolicy{ObjectMeta: metav1.ObjectMeta{Name: "disallow-privileged"}}).Build()
			r := &PolicyExceptionReconciler{Client: c, Scheme: s, Log: logr.Discard(), DestinationNamespace: "policy-exceptions", MaxJitterPercent: 10, LegacyMode: LegacyWrite}
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}

			var cel policiesv1.PolicyException
			if err := c.Get(ctx, key, &cel); err != nil {
				t.Fatal(err)
			}
			if got := eval(t, cel.Spec.MatchConditions, obj("ConfigMap", "team-a", "foo", ""), nil); got != tt.matchesFoo {
				t.Errorf("CEL exception matches foo: got %v, want %v", got, tt.matchesFoo)
			}
			if got := eval(t, cel.Spec.MatchConditions, obj("ConfigMap", "team-a", "bar", ""), nil); got != tt.matchesBar {
				t.Errorf("CEL exception matches bar: got %v, want %v", got, tt.matchesBar)
			}

			var legacy kyvernov2.PolicyException
			err := c.Get(ctx, key, &legacy)
			if !tt.wantLegacy {
				if !apierrors.IsNotFound(err) {
					t.Errorf("legacy exception written: %v %+v", err, legacy.Spec.Match.Any)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := legacy.Spec.Match.Any[0].Names; len(got) != 1 || got[0] != "foo*" {
				t.Errorf("legacy names: got %v, want [foo*]", got)
			}
		})
	}
}

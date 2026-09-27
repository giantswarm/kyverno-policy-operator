package controller

import (
	"context"
	"errors"
	"testing"

	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// The bypass watch passes every event of the bypass PolicyException, a delete included, and no
// event of any other PolicyException.
func TestBypassPredicate(t *testing.T) {
	p := (&ChartOperatorBypassReconciler{Namespace: ChartOperatorBypassNamespace}).bypassPredicate()
	tests := []struct {
		name, namespace, objectName string
		want                        bool
	}{
		{"the bypass", ChartOperatorBypassNamespace, ChartOperatorBypassName, true},
		{"a gspolex exception", "policy-exceptions", "app", false},
		{"the bypass name in another namespace", "policy-exceptions", ChartOperatorBypassName, false},
		{"another exception in the bypass namespace", ChartOperatorBypassNamespace, "app", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{Name: tt.objectName, Namespace: tt.namespace}}
			got := map[string]bool{
				"create":  p.Create(event.CreateEvent{Object: obj}),
				"update":  p.Update(event.UpdateEvent{ObjectOld: obj, ObjectNew: obj}),
				"delete":  p.Delete(event.DeleteEvent{Object: obj}),
				"generic": p.Generic(event.GenericEvent{Object: obj}),
			}
			for kind, passed := range got {
				if passed != tt.want {
					t.Errorf("%s event: got %v, want %v", kind, passed, tt.want)
				}
			}
		})
	}
}

var bypassKey = types.NamespacedName{Namespace: ChartOperatorBypassNamespace, Name: ChartOperatorBypassName}

// managedBypass is the CEL chart-operator bypass KPO wrote.
func managedBypass() *policiesv1.PolicyException {
	return &policiesv1.PolicyException{ObjectMeta: metav1.ObjectMeta{
		Name: bypassKey.Name, Namespace: bypassKey.Namespace, Labels: map[string]string{ManagedBy: ComponentName},
	}}
}

// When listing ValidatingPolicies or deleting the bypass fails, Reconcile returns the error, counts
// it, and leaves the bypass as it was.
func TestChartOperatorBypassReconcileErrors(t *testing.T) {
	errAPI := errors.New("API server unavailable")
	tests := []struct {
		name    string
		objects []client.Object
		funcs   interceptor.Funcs
		reason  string
	}{
		{
			name:    "listing ValidatingPolicies fails",
			objects: []client.Object{managedBypass(), &policiesv1.ValidatingPolicy{ObjectMeta: metav1.ObjectMeta{Name: "disallow-privileged"}}},
			funcs:   failList[*policiesv1.ValidatingPolicyList](errAPI),
			reason:  "lookup_failed",
		},
		{
			name:    "deleting the bypass after the last ValidatingPolicy fails",
			objects: []client.Object{managedBypass()},
			funcs:   failDelete[*policiesv1.PolicyException](errAPI),
			reason:  "delete_failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// arrange
			ctx := context.Background()
			base := fake.NewClientBuilder().WithScheme(newScheme(t, policiesv1.Install)).WithObjects(tt.objects...).Build()
			var before policiesv1.PolicyException
			require.NoError(t, base.Get(ctx, bypassKey, &before))
			r := &ChartOperatorBypassReconciler{Client: interceptor.NewClient(base, tt.funcs), Kinds: []string{"Namespace"}, Namespace: ChartOperatorBypassNamespace}
			errorsBefore := generationErrors(APICEL, tt.reason)

			// act
			_, err := r.Reconcile(ctx, ctrl.Request{})

			// assert
			require.ErrorIs(t, err, errAPI)
			assert.Equal(t, 1.0, generationErrors(APICEL, tt.reason)-errorsBefore)
			var after policiesv1.PolicyException
			require.NoError(t, base.Get(ctx, bypassKey, &after), "the bypass was deleted")
			assert.Equal(t, before.ResourceVersion, after.ResourceVersion)
		})
	}
}

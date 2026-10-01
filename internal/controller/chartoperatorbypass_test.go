package controller

import (
	"testing"

	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

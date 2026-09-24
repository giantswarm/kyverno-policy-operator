package controller

import (
	"testing"

	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsBypass(t *testing.T) {
	r := &ChartOperatorBypassReconciler{Namespace: ChartOperatorBypassNamespace}
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
			if got := r.isBypass(obj); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

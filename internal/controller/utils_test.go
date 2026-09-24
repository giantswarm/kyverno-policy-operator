package controller

import (
	"testing"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsMigrated(t *testing.T) {
	annotation := map[string]string{AnnotationMigratedFrom: "team-a/foo"}
	label := map[string]string{ManagedBy: "exception-recommender"}
	tests := []struct {
		name        string
		annotations map[string]string
		labels      map[string]string
		want        bool
	}{
		{"annotation and label", annotation, label, true},
		{"annotation only", annotation, nil, false},
		{"label only", nil, label, false},
		{"annotation and another managed-by", annotation, map[string]string{ManagedBy: "flux"}, false},
		{"neither", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &policyAPI.PolicyException{ObjectMeta: metav1.ObjectMeta{Annotations: tt.annotations, Labels: tt.labels}}
			if got := isMigrated(p); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

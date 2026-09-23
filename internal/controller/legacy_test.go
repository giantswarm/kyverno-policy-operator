package controller

import (
	"testing"

	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"
	kyvernov2 "github.com/kyverno/kyverno/api/kyverno/v2"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestDetectLegacyMode(t *testing.T) {
	both := []schema.GroupVersionKind{kyvernov1.SchemeGroupVersion.WithKind("ClusterPolicy"), kyvernov2.SchemeGroupVersion.WithKind("PolicyException")}
	tests := []struct {
		name    string
		kinds   []schema.GroupVersionKind
		enabled bool
		want    LegacyMode
	}{
		{"legacy CRDs and switch on", both, true, LegacyWrite},
		{"legacy CRDs and switch off", both, false, LegacyCleanup},
		{"Kyverno 1.20 without legacy CRDs", nil, true, LegacyAbsent},
		{"only ClusterPolicy left", both[:1], true, LegacyAbsent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := meta.NewDefaultRESTMapper(nil)
			for _, gvk := range tt.kinds {
				mapper.Add(gvk, meta.RESTScopeNamespace)
			}
			got, err := DetectLegacyMode(mapper, tt.enabled)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

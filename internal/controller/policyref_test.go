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
	"testing"

	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func fullScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := policiesv1.Install(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResolvePolicyRefs(t *testing.T) {
	const name = "disallow-privileged-containers"
	meta := metav1.ObjectMeta{Name: name}

	// A scheme that only knows ValidatingPolicy stands in for a cluster
	// where the other CEL CRDs are not installed.
	vpolOnly := runtime.NewScheme()
	vpolOnly.AddKnownTypes(schema.GroupVersion(policiesv1.GroupVersion), &policiesv1.ValidatingPolicy{}, &policiesv1.ValidatingPolicyList{})

	tests := []struct {
		name         string
		scheme       *runtime.Scheme
		objects      []client.Object
		wantKinds    []string
		wantResolved bool
	}{
		{"validating policy", fullScheme(t), []client.Object{&policiesv1.ValidatingPolicy{ObjectMeta: meta}}, []string{"ValidatingPolicy"}, true},
		{"validating and mutating policy", fullScheme(t), []client.Object{&policiesv1.ValidatingPolicy{ObjectMeta: meta}, &policiesv1.MutatingPolicy{ObjectMeta: meta}}, []string{"ValidatingPolicy", "MutatingPolicy"}, true},
		{"no policy falls back to ValidatingPolicy", fullScheme(t), nil, []string{"ValidatingPolicy"}, false},
		{"missing CRDs are not errors", vpolOnly, []client.Object{&policiesv1.ValidatingPolicy{ObjectMeta: meta}}, []string{"ValidatingPolicy"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := fake.NewClientBuilder().WithScheme(tt.scheme).WithObjects(tt.objects...).Build()
			refs, resolved, err := resolvePolicyRefs(context.Background(), c, name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resolved != tt.wantResolved {
				t.Errorf("resolved = %v, want %v", resolved, tt.wantResolved)
			}
			var kinds []string
			for _, ref := range refs {
				if ref.Name != name {
					t.Errorf("ref name = %q, want %q", ref.Name, name)
				}
				kinds = append(kinds, ref.Kind)
			}
			if len(kinds) != len(tt.wantKinds) {
				t.Fatalf("kinds = %v, want %v", kinds, tt.wantKinds)
			}
			for i := range kinds {
				if kinds[i] != tt.wantKinds[i] {
					t.Errorf("kinds = %v, want %v", kinds, tt.wantKinds)
				}
			}
		})
	}
}

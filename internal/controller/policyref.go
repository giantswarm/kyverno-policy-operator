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

	policieskyvernoio "github.com/kyverno/api/api/policies.kyverno.io"
	policiesv1 "github.com/kyverno/api/api/policies.kyverno.io/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// celPolicyKinds are the cluster-scoped CEL policy kinds a gspolex policy name can refer to.
var celPolicyKinds = []struct {
	kind string
	new  func() client.Object
}{
	{policieskyvernoio.ValidatingPolicyKind, func() client.Object { return &policiesv1.ValidatingPolicy{} }},
	{"MutatingPolicy", func() client.Object { return &policiesv1.MutatingPolicy{} }},
	{policieskyvernoio.ImageValidatingPolicyKind, func() client.Object { return &policiesv1.ImageValidatingPolicy{} }},
}

// resolvePolicyRefs returns a PolicyRef for every CEL policy kind that has a policy with the given name.
// When none exists yet it falls back to a ValidatingPolicy ref and reports resolved=false, so the
// exception is already in place when the policy gets installed.
func resolvePolicyRefs(ctx context.Context, c client.Reader, name string) ([]policiesv1.PolicyRef, bool, error) {
	logger := log.FromContext(ctx)

	var refs []policiesv1.PolicyRef
	for _, k := range celPolicyKinds {
		err := c.Get(ctx, types.NamespacedName{Name: name}, k.new())
		switch {
		case err == nil:
			refs = append(refs, policiesv1.PolicyRef{Name: name, Kind: k.kind})
		case errors.IsNotFound(err), meta.IsNoMatchError(err), runtime.IsNotRegisteredError(err):
			// No policy of this kind, or its CRD is not installed.
		default:
			logger.Error(err, "failed to look up CEL policy", "policy", name, "kind", k.kind)
			return nil, false, err
		}
	}
	if len(refs) == 0 {
		logger.V(1).Info("policy name resolved to no CEL policy kinds, falling back to ValidatingPolicy", "policy", name)
		return []policiesv1.PolicyRef{{Name: name, Kind: policieskyvernoio.ValidatingPolicyKind}}, false, nil
	}

	kinds := make([]string, len(refs))
	for i, ref := range refs {
		kinds[i] = ref.Kind
	}
	logger.V(1).Info("policy name resolved to CEL policy kinds", "policy", name, "kinds", kinds)
	return refs, true, nil
}

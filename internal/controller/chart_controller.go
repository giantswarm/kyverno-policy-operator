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
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"gopkg.in/yaml.v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/edgedb/edgedb-go"
	apiextensions "github.com/giantswarm/apiextensions-application/api/v1alpha1"
	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/giantswarm/kyverno-policy-operator/internal/utils"
)

// ChartReconciler reconciles a ClusterPolicy object
type ChartReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Log              logr.Logger
	MaxJitterPercent int
	EdgeDBClient     *edgedb.Client
}

type Manifest struct {
	Kind     string   `yaml:"kind"`
	Metadata Metadata `yaml:"metadata"`
}

type Metadata struct {
	Name      string `yaml:"name"`
	Namespace string `yaml:"namespace"`
}

func (r *ChartReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)
	_ = r.Log.WithValues("giantswarm chart", req.NamespacedName)

	var chart apiextensions.Chart

	if err := r.Get(ctx, req.NamespacedName, &chart); err != nil {
		// Error fetching the report

		// Check if the ClusterPolicy was deleted
		if apierrors.IsNotFound(err) {
			// Ignore
			return ctrl.Result{}, nil
		}

		log.Log.Error(err, "unable to fetch Giant Swarm Chart")
		return ctrl.Result{}, client.IgnoreNotFound(err)

	}
	if chart.Labels["app.kubernetes.io/name"] != "cluster-aws" {
		// Get the helm manifest
		manifest, err := getReleaseTemplate(chart.Spec.Name, chart.Spec.Namespace)
		if err != nil {
			log.Log.Error(err, "unable to fetch helm manifest")
			return ctrl.Result{}, err
		} else {
			fmt.Println("Manifests for release: ", chart.Spec.Name)
			for _, m := range manifest {
				fmt.Println(m)
			}
		}
	}

	return utils.JitterRequeue(DefaultRequeueDuration, r.MaxJitterPercent, r.Log), nil
}

func getReleaseTemplate(releaseName string, namespace string) ([]Manifest, error) {

	args := []string{"get", "manifest", "-n", namespace, releaseName}
	cmd := exec.Command("helm", args...)

	// Capture the output
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	var parsedManifests []Manifest

	// Execute the command
	err := cmd.Run()
	if err != nil {
		return parsedManifests, fmt.Errorf("command failed: %s, stderr: %s", err, stderr.String())
	}

	rawManifests := strings.Split(out.String(), "---\n")

	for _, manifest := range rawManifests {
		if strings.TrimSpace(manifest) == "" {
			continue // Skip empty segments
		}
		var parsed Manifest
		err := yaml.Unmarshal([]byte(manifest), &parsed)
		if err != nil {
			return nil, fmt.Errorf("failed to parse YAML: %w", err)
		}
		parsedManifests = append(parsedManifests, parsed)
	}

	return parsedManifests, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChartReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&apiextensions.Chart{}).
		Complete(r)
}

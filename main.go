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

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.

	kyvernov1 "github.com/kyverno/kyverno/api/kyverno/v1"

	policyAPI "github.com/giantswarm/policy-api/api/v1alpha1"

	"github.com/giantswarm/kyverno-policy-operator/internal/controller"

	kyvernov2beta1 "github.com/kyverno/kyverno/api/kyverno/v2beta1"

	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	err := kyvernov2beta1.AddToScheme(scheme)
	if err != nil {
		setupLog.Error(err, "unable to register kyverno schema")
	}

	err = kyvernov1.AddToScheme(scheme)
	if err != nil {
		setupLog.Error(err, "unable to register kyverno schema")
	}

	utilruntime.Must(policyAPI.AddToScheme(scheme))
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var destinationNamespace string
	var backgroundMode bool
	var chartOperatorExceptionKinds []string
	var maxJitterPercent int
	policyCache := make(map[string]kyvernov1.ClusterPolicy)

	// Flags
	flag.StringVar(&destinationNamespace, "destination-namespace", "", "The namespace where the Kyverno PolicyExceptions will be created. Defaults to GS PolicyException namespace.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: false,
	}
	flag.BoolVar(&backgroundMode, "background-mode", false,
		"Enable PolicyException background mode. If true, failing resources have a status of 'skip' in reports, instead of 'fail'. Defaults to false.",
	)
	flag.Func("chart-operator-exception-kinds",
		"A comma-separated list of kinds to be excluded from custom ClusterPolicies. This lets the chart-operator ServiceAccount to create protected objects.",
		func(input string) error {
			items := strings.Split(input, ",")

			chartOperatorExceptionKinds = append(chartOperatorExceptionKinds, items...)

			return nil
		})
	flag.IntVar(&maxJitterPercent, "max-jitter-percent", 10, "Spreads out re-queue interval by +/- this amount to spread load.")
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	if destinationNamespace == "" {
		fmt.Println("Error: The destination-namespace flag is required")
		os.Exit(2) // The flag package uses 2 as the exit code when the program is terminated due to a flag parsing error
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "71f505ec.giantswarm.io",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.PolicyExceptionReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		DestinationNamespace: destinationNamespace,
		Background:           backgroundMode,
		MaxJitterPercent:     maxJitterPercent,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PolicyException")
		os.Exit(1)
	}

	if err = (&controller.PolicyManifestReconciler{
		Client:               mgr.GetClient(),
		Scheme:               mgr.GetScheme(),
		DestinationNamespace: destinationNamespace,
		Background:           backgroundMode,
		PolicyCache:          policyCache,
		MaxJitterPercent:     maxJitterPercent,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PolicyManifest")
		os.Exit(1)
	}

	if err = (&controller.ClusterPolicyReconciler{
		Client:                      mgr.GetClient(),
		Scheme:                      mgr.GetScheme(),
		ExceptionList:               make(map[string]kyvernov1.ClusterPolicy),
		ChartOperatorExceptionKinds: chartOperatorExceptionKinds,
		PolicyCache:                 policyCache,
		MaxJitterPercent:            maxJitterPercent,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "PolicyException")
		os.Exit(1)
	}

	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

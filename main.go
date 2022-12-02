/*
Copyright 2022 The KCP Authors.

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
	"context"
	"flag"
	"fmt"
	"os"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	retrywatch "k8s.io/client-go/tools/watch"
	"k8s.io/klog/v2"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/kcp"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	apisv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/apis/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/util/conditions"

	// +kubebuilder:scaffold:imports

	datav1alpha1 "github.com/kcp-dev/controller-runtime-example/api/v1alpha1"
	"github.com/kcp-dev/controller-runtime-example/controllers"
)

var (
	scheme            = runtime.NewScheme()
	setupLog          = ctrl.Log.WithName("setup")
	kubeconfigContext string
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(datav1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme

	flag.StringVar(&kubeconfigContext, "context", "", "kubeconfig context")
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var apiExportName string
	flag.StringVar(&apiExportName, "api-export-name", "data.my.domain", "The name of the APIExport.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}

	opts.BindFlags(flag.CommandLine)
	klog.InitFlags(flag.CommandLine)

	flag.Parse()
	flag.Lookup("v").Value.Set("6")

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	ctx := ctrl.SetupSignalHandler()

	restConfig := ctrl.GetConfigOrDie()

	setupLog = setupLog.WithValues("api-export-name", apiExportName)

	var mgr ctrl.Manager
	var err error
	options := ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "68a0532d.my.domain",
		LeaderElectionConfig:   restConfig,
	}
	if kcpAPIsGroupPresent(restConfig) {
		setupLog.Info("Looking up virtual workspace URL")
		cfg, err := restConfigForAPIExport(ctx, restConfig, apiExportName)
		if err != nil {
			setupLog.Error(err, "error looking up virtual workspace URL")
		}

		setupLog.Info("Using virtual workspace URL", "url", cfg.Host)

		options.LeaderElectionConfig = restConfig
		mgr, err = kcp.NewClusterAwareManager(cfg, options)
		if err != nil {
			setupLog.Error(err, "unable to start cluster aware manager")
			os.Exit(1)
		}
	} else {
		setupLog.Info("The apis.kcp.dev group is not present - creating standard manager")
		mgr, err = ctrl.NewManager(restConfig, options)
		if err != nil {
			setupLog.Error(err, "unable to start manager")
			os.Exit(1)
		}
	}

	if err = (&controllers.ConfigMapReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
	}

	if err = (&controllers.WidgetReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Widget")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")

	if err := mgr.Start(ctx); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// +kubebuilder:rbac:groups="apis.kcp.dev",resources=apiexports,verbs=get;list;watch

// restConfigForAPIExport returns a *rest.Config properly configured to communicate with the endpoint for the
// APIExport's virtual workspace. It blocks until the controller APIExport VirtualWorkspaceURLsReady condition
// becomes truthy, which happens when the APIExport is bound for the first time.
func restConfigForAPIExport(ctx context.Context, cfg *rest.Config, apiExportName string) (*rest.Config, error) {
	apiExportClient, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("error creating APIExport client: %w", err)
	}

	list := &apisv1alpha1.APIExportList{}
	selector := fields.OneTermEqualSelector("metadata.name", apiExportName)
	err = apiExportClient.List(ctx, list, client.MatchingFieldsSelector{Selector: selector})
	if err != nil {
		return nil, fmt.Errorf("error watching for APIExport: %w", err)
	}
	if len(list.Items) > 0 && isAPIExportReady(&list.Items[0]) {
		cfg = rest.CopyConfig(cfg)
		// TODO: sharding support
		cfg.Host = list.Items[0].Status.VirtualWorkspaces[0].URL
		return cfg, nil
	}

	setupLog.Info("Watching for APIExport to become ready", "name", apiExportName)

	rw, err := retrywatch.NewRetryWatcher(list.ResourceVersion, watcher(apiExportClient.Watch).FilteredBy(selector))
	if err != nil {
		return nil, fmt.Errorf("error creating retry watcher for APIExport: %w", err)
	}
	defer rw.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case e := <-rw.ResultChan():
			switch e.Type {
			case watch.Error:
				return nil, fmt.Errorf("error watching for APIExport: %w", apierrors.FromObject(e.Object))

			case watch.Added, watch.Modified:
				apiExport, ok := e.Object.(*apisv1alpha1.APIExport)
				if !ok {
					return nil, fmt.Errorf("unexpected event object: %v", e.Object)
				}
				if !isAPIExportReady(apiExport) {
					continue
				}
				cfg = rest.CopyConfig(cfg)
				// TODO: sharding support
				cfg.Host = apiExport.Status.VirtualWorkspaces[0].URL
				return cfg, nil
			}
		}
	}
}

func kcpAPIsGroupPresent(restConfig *rest.Config) bool {
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		setupLog.Error(err, "failed to create discovery client")
		os.Exit(1)
	}
	apiGroupList, err := discoveryClient.ServerGroups()
	if err != nil {
		setupLog.Error(err, "failed to get server groups")
		os.Exit(1)
	}

	for _, group := range apiGroupList.Groups {
		if group.Name == apisv1alpha1.SchemeGroupVersion.Group {
			for _, version := range group.Versions {
				if version.Version == apisv1alpha1.SchemeGroupVersion.Version {
					return true
				}
			}
		}
	}
	return false
}

func isAPIExportReady(apiExport *apisv1alpha1.APIExport) bool {
	if !conditions.IsTrue(apiExport, apisv1alpha1.APIExportVirtualWorkspaceURLsReady) {
		setupLog.Info("APIExport virtual workspace URLs are not ready", "APIExport", apiExport.Name)
		return false
	}

	if len(apiExport.Status.VirtualWorkspaces) == 0 {
		setupLog.Info("APIExport does not have any virtual workspace URLs", "APIExport", apiExport.Name)
		return false
	}

	return true
}

type watcher func(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) (watch.Interface, error)

func (w watcher) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return w(context.TODO(), &apisv1alpha1.APIExportList{}, &client.ListOptions{Raw: &options})
}

func (w watcher) FilteredBy(selector fields.Selector) watcher {
	return func(ctx context.Context, obj client.ObjectList, opts ...client.ListOption) (watch.Interface, error) {
		return w(ctx, obj, append(opts, client.MatchingFieldsSelector{Selector: selector})...)
	}
}

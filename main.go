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
	"os"

	"github.com/fabianvf/kcp-cr-example/controllers"
	kcpcache "github.com/kcp-dev/apimachinery/pkg/cache"
	kcpclient "github.com/kcp-dev/apimachinery/pkg/client"
	"github.com/kcp-dev/logicalcluster"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
}

func main() {
	ctrl.SetLogger(zap.New())

	cfg := ctrl.GetConfigOrDie()

	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		setupLog.Error(err, "unable to build http client")
		os.Exit(1)
	}

	httpClient.Transport = kcpclient.NewClusterRoundTripper(httpClient.Transport)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:         scheme,
		LeaderElection: false,
		// LeaderElectionNamespace: "default",
		// LeaderElectionID:        "kcp-example",
		NewCache: func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
			c := rest.CopyConfig(config)
			c.Host += "/clusters/*"
			opts.KeyFunction = kcpcache.ClusterAwareKeyFunc
			return cache.New(c, opts)
		},
		NewClient: func(cache cache.Cache, config *rest.Config, opts client.Options, uncachedObjects ...client.Object) (client.Client, error) {
			opts.HTTPClient = httpClient
			return cluster.DefaultNewClient(cache, config, opts, uncachedObjects...)
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.ConfigMapReconciler{
		Client: mgr.GetClient(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ConfigMap")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(kcpclient.WithCluster(ctrl.SetupSignalHandler(), logicalcluster.Wildcard)); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

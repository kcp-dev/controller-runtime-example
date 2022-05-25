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
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	kcpcache "github.com/kcp-dev/apimachinery/pkg/cache"
	kcpclient "github.com/kcp-dev/apimachinery/pkg/client"
	"github.com/kcp-dev/logicalcluster"

	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"sigs.k8s.io/controller-runtime/pkg/kcp"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var (
	setupLog = ctrl.Log.WithName("setup")
)

type reconciler struct {
	client.Client
	scheme *runtime.Scheme
}

func (r *reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("cluster", req.ObjectKey.Cluster.String())

	ctx = kcpclient.WithCluster(ctx, req.ObjectKey.Cluster)

	var configmap corev1.ConfigMap
	if err := r.Get(ctx, req.ObjectKey, &configmap); err != nil {
		log.Error(err, "unable to get configmap")
		return ctrl.Result{}, err
	}
	log.Info("Retrieved configmap")

	return ctrl.Result{}, nil
}

func main() {

	ctrl.SetLogger(zap.New())

	cfg := ctrl.GetConfigOrDie()
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		setupLog.Error(err, "unable to build http client")
		os.Exit(1)
	}
	clusterRoundTripper := kcpclient.NewClusterRoundTripper(httpClient.Transport)
	httpClient.Transport = clusterRoundTripper
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		LeaderElection: false,
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

	err = corev1.AddToScheme(mgr.GetScheme())
	if err != nil {
		setupLog.Error(err, "unable to add scheme")
		os.Exit(1)
	}

	c, err := controller.New("kcp-controller", mgr, controller.Options{
		Reconciler: &reconciler{
			Client: mgr.GetClient(),
			scheme: mgr.GetScheme(),
		}})
	if err != nil {
		setupLog.Error(err, "unable to set up individual controller")
		os.Exit(1)
	}
	if err := c.Watch(&source.Kind{Type: &corev1.ConfigMap{}}, &kcp.EnqueueRequestForObject{}); err != nil {
		setupLog.Error(err, "unable to watch configmaps")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(kcpclient.WithCluster(ctrl.SetupSignalHandler(), logicalcluster.Wildcard)); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

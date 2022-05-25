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

package controllers

import (
	"context"
	"fmt"

	kcpclient "github.com/kcp-dev/apimachinery/pkg/client"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ConfigMapReconciler struct {
	client.Client
}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("cluster", req.ObjectKey.Cluster.String())

	ctx = kcpclient.WithCluster(ctx, req.ObjectKey.Cluster)

	// Test get
	var configMap corev1.ConfigMap

	if err := r.Get(ctx, req.ObjectKey, &configMap); err != nil {
		log.Error(err, "unable to get configmap")
		return ctrl.Result{}, nil
	}

	log.Info("Get: retrieved configMap")

	// Test list
	var configMapList corev1.ConfigMapList
	if err := r.List(ctx, &configMapList); err != nil {
		log.Error(err, "unable to list configmaps")
		return ctrl.Result{}, nil
	}
	for _, cm := range configMapList.Items {
		log.Info("List: got", "namespace", cm.Namespace, "name", cm.Name)
	}

	labels := configMap.Labels
	if labels == nil {
		return ctrl.Result{}, nil
	}

	// Test Update
	if labels["name"] == "" {
		return ctrl.Result{}, nil
	}

	response := fmt.Sprintf("hello-%s", labels["name"])

	if labels["response"] == response {
		return ctrl.Result{}, nil
	}

	labels["response"] = response

	if err := r.Update(ctx, &configMap); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(r)
}

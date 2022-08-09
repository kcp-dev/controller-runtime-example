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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kcp-dev/logicalcluster/v2"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ConfigMapReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps/finalizers,verbs=update

// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=namespaces/finalizers,verbs=update

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx).WithValues("cluster", req.ClusterName)

	ctx = logicalcluster.WithCluster(ctx, logicalcluster.New(req.ClusterName))

	// Test get
	var configMap corev1.ConfigMap

	if err := r.Get(ctx, req.NamespacedName, &configMap); err != nil {
		log.Error(err, "unable to get configmap")
		return ctrl.Result{}, nil
	}

	log.Info("Get: retrieved configMap")
	labels := configMap.Labels

	if labels["name"] != "" {
		response := fmt.Sprintf("hello-%s", labels["name"])

		if labels["response"] != response {
			labels["response"] = response

			// Test Update
			if err := r.Update(ctx, &configMap); err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Update: updated configMap")
			return ctrl.Result{}, nil
		}
	}

	// Test list
	var configMapList corev1.ConfigMapList
	if err := r.List(ctx, &configMapList); err != nil {
		log.Error(err, "unable to list configmaps")
		return ctrl.Result{}, nil
	}
	log.Info("List: got", "itemCount", len(configMapList.Items))
	found := false
	for _, cm := range configMapList.Items {
		if !logicalcluster.From(&cm).Empty() {
			log.Info("List: got", "clusterName", logicalcluster.From(&cm).String(), "namespace", cm.Namespace, "name", cm.Name)
		} else {
			if cm.Name == configMap.Name && cm.Namespace == configMap.Namespace {
				if found {
					return ctrl.Result{}, fmt.Errorf("there should be listed only one configmap with the given name '%s' for the given namespace '%s' when the clusterName is not available", cm.Name, cm.Namespace)
				}
				found = true
				log.Info("Found in listed configmaps", "namespace", cm.Namespace, "name", cm.Name)
			}
		}
	}

	// If the configmap has a namespace field, create the corresponding namespace
	nsName, exists := configMap.Data["namespace"]
	if exists {
		var namespace corev1.Namespace
		nsKey := types.NamespacedName{Name: nsName}

		if err := r.Get(ctx, nsKey, &namespace); err != nil {
			if !apierrors.IsNotFound(err) {
				log.Error(err, "unable to get namespace")
				return ctrl.Result{}, err
			}

			// Need to create ns
			namespace.SetName(nsName)
			if err = r.Create(ctx, &namespace); err != nil {
				log.Error(err, "unable to create namespace")
				return ctrl.Result{}, err
			}
			log.Info("Create: created ", "namespace", nsName)
			return ctrl.Result{RequeueAfter: time.Second * 5}, nil
		}
		log.Info("Exists", "namespace", nsName)
	}

	// If the configmap has a secretData field, create a secret in the same namespace
	// If the secret already exists but is out of sync, it will be non-destructively patched
	secretData, exists := configMap.Data["secretData"]
	if exists {
		var secret corev1.Secret

		secret.SetName(configMap.GetName())
		secret.SetNamespace(configMap.GetNamespace())
		secret.SetOwnerReferences([]metav1.OwnerReference{metav1.OwnerReference{
			Name:       configMap.GetName(),
			UID:        configMap.GetUID(),
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Controller: func() *bool { x := true; return &x }(),
		}})
		secret.Data = map[string][]byte{"dataFromCM": []byte(secretData)}

		operationResult, err := controllerutil.CreateOrPatch(ctx, r, &secret, func() error {
			secret.Data["dataFromCM"] = []byte(secretData)
			return nil
		})
		if err != nil {
			log.Error(err, "unable to create or patch secret")
			return ctrl.Result{}, err
		}
		log.Info(string(operationResult), "secret", secret.GetName())
	}

	return ctrl.Result{}, nil
}

func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

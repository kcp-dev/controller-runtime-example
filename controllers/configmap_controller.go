package controllers

import (
	"context"

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

	var configmap corev1.ConfigMap
	if err := r.Get(ctx, req.ObjectKey, &configmap); err != nil {
		log.Error(err, "unable to get configmap")
		return ctrl.Result{}, err
	}

	log.Info("Retrieved configmap")

	return ctrl.Result{}, nil
}

func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}).
		Complete(r)
}

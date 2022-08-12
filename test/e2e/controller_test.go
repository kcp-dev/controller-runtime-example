package e2e

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	kcpclienthelper "github.com/kcp-dev/apimachinery/pkg/client"

	apisv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/apis/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/util/conditions"

	"github.com/kcp-dev/logicalcluster/v2"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	datav1alpha1 "github.com/kcp-dev/controller-runtime-example/api/v1alpha1"
)

// The tests in this package expect to be called when:
// - kcp is running
// - a kind cluster is up and running
// - it is hosting a syncer, and the SyncTarget is ready to go
// - the controller-manager from this repo is deployed to kcp
// - that deployment is synced to the kind cluster
// - the deployment is rolled out & ready
//
// We can then check that the controllers defined here are working as expected.

var workspaceName string

func init() {
	rand.Seed(time.Now().Unix())
	flag.StringVar(&workspaceName, "workspace", "", "Workspace in which to run these tests.")
}

func parentWorkspace(t *testing.T) logicalcluster.Name {
	flag.Parse()
	if workspaceName == "" {
		t.Fatal("--workspace cannot be empty")
	}

	return logicalcluster.New(workspaceName)
}

func loadClusterConfig(t *testing.T, clusterName logicalcluster.Name) *rest.Config {
	t.Helper()
	restConfig, err := config.GetConfigWithContext("base")
	if err != nil {
		t.Fatalf("failed to load *rest.Config: %v", err)
	}
	return rest.AddUserAgent(kcpclienthelper.ConfigWithCluster(restConfig, clusterName), t.Name())
}

func loadClient(t *testing.T, clusterName logicalcluster.Name) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client go to scheme: %v", err)
	}
	if err := tenancyv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add %s to scheme: %v", tenancyv1alpha1.SchemeGroupVersion, err)
	}
	if err := datav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add %s to scheme: %v", datav1alpha1.GroupVersion, err)
	}
	if err := apisv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add %s to scheme: %v", apisv1alpha1.SchemeGroupVersion, err)
	}
	tenancyClient, err := client.New(loadClusterConfig(t, clusterName), client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("failed to create a client: %v", err)
	}
	return tenancyClient
}

func createWorkspace(t *testing.T, clusterName logicalcluster.Name) client.Client {
	t.Helper()
	parent, ok := clusterName.Parent()
	if !ok {
		t.Fatalf("cluster %s has no parent", clusterName)
	}
	c := loadClient(t, parent)
	t.Logf("creating workspace %s", clusterName)
	if err := c.Create(context.TODO(), &tenancyv1alpha1.ClusterWorkspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterName.Base(),
		},
		Spec: tenancyv1alpha1.ClusterWorkspaceSpec{
			Type: tenancyv1alpha1.ClusterWorkspaceTypeReference{
				Name: "universal",
				Path: "root",
			},
		},
	}); err != nil {
		t.Fatalf("failed to create workspace: %s: %v", clusterName, err)
	}

	t.Logf("waiting for workspace %s to be ready", clusterName)
	var workspace tenancyv1alpha1.ClusterWorkspace
	if err := wait.PollImmediate(100*time.Millisecond, wait.ForeverTestTimeout, func() (done bool, err error) {
		fetchErr := c.Get(context.TODO(), client.ObjectKey{Name: clusterName.Base()}, &workspace)
		if fetchErr != nil {
			t.Logf("failed to get workspace %s: %v", clusterName, err)
			return false, fetchErr
		}
		var reason string
		if actual, expected := workspace.Status.Phase, tenancyv1alpha1.ClusterWorkspacePhaseReady; actual != expected {
			reason = fmt.Sprintf("phase is %s, not %s", actual, expected)
			t.Logf("not done waiting for workspace %s to be ready: %s", clusterName, reason)
		}
		return reason == "", nil
	}); err != nil {
		t.Fatalf("workspace %s never ready: %v", clusterName, err)
	}

	return createAPIBinding(t, clusterName)
}

func createAPIBinding(t *testing.T, workspaceCluster logicalcluster.Name) client.Client {
	c := loadClient(t, workspaceCluster)
	apiName := "controller-runtime-example-data.my.domain"
	t.Logf("creating APIBinding %s|%s", workspaceCluster, apiName)
	if err := c.Create(context.TODO(), &apisv1alpha1.APIBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: apiName,
		},
		Spec: apisv1alpha1.APIBindingSpec{
			Reference: apisv1alpha1.ExportReference{
				Workspace: &apisv1alpha1.WorkspaceExportReference{
					Path:       parentWorkspace(t).String(),
					ExportName: apiName,
				},
			},
			AcceptedPermissionClaims: []apisv1alpha1.PermissionClaim{
				{GroupResource: apisv1alpha1.GroupResource{Resource: "configmaps"}},
				{GroupResource: apisv1alpha1.GroupResource{Resource: "secrets"}},
				{GroupResource: apisv1alpha1.GroupResource{Resource: "namespaces"}},
			},
		},
	}); err != nil {
		t.Fatalf("could not create APIBinding %s|%s: %v", workspaceCluster, apiName, err)
	}

	t.Logf("waiting for APIBinding %s|%s to be bound", workspaceCluster, apiName)
	var apiBinding apisv1alpha1.APIBinding
	if err := wait.PollImmediate(100*time.Millisecond, wait.ForeverTestTimeout, func() (done bool, err error) {
		fetchErr := c.Get(context.TODO(), client.ObjectKey{Name: apiName}, &apiBinding)
		if fetchErr != nil {
			t.Logf("failed to get APIBinding %s|%s: %v", workspaceCluster, apiName, err)
			return false, fetchErr
		}
		var reason string
		if !conditions.IsTrue(&apiBinding, apisv1alpha1.InitialBindingCompleted) {
			condition := conditions.Get(&apiBinding, apisv1alpha1.InitialBindingCompleted)
			if condition != nil {
				reason = fmt.Sprintf("%s: %s", condition.Reason, condition.Message)
			} else {
				reason = "no condition present"
			}
			t.Logf("not done waiting for APIBinding %s|%s to be bound: %s", workspaceCluster, apiName, reason)
		}
		return conditions.IsTrue(&apiBinding, apisv1alpha1.InitialBindingCompleted), nil
	}); err != nil {
		t.Fatalf("APIBinding %s|%s never bound: %v", workspaceCluster, apiName, err)
	}

	return c
}

const characters = "abcdefghijklmnopqrstuvwxyz"

func randomName() string {
	b := make([]byte, 10)
	for i := range b {
		b[i] = characters[rand.Intn(len(characters))]
	}
	return string(b)
}

// TestConfigMapController verifies that our ConfigMap behavior works.
func TestConfigMapController(t *testing.T) {
	t.Parallel()
	for i := 0; i < 3; i++ {
		t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
			t.Parallel()
			workspaceCluster := parentWorkspace(t).Join(randomName())
			c := createWorkspace(t, workspaceCluster)

			namespaceName := randomName()
			t.Logf("creating namespace %s|%s", workspaceCluster, namespaceName)
			if err := c.Create(context.TODO(), &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
			}); err != nil {
				t.Fatalf("failed to create a namespace: %v", err)
			}

			otherNamespaceName := randomName()
			data := randomName()
			configmapName := randomName()
			t.Logf("creating configmap %s|%s/%s", workspaceCluster, namespaceName, configmapName)
			if err := c.Create(context.TODO(), &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configmapName,
					Namespace: namespaceName,
					Labels: map[string]string{
						"name": "timothy",
					},
				},
				Data: map[string]string{
					"namespace":  otherNamespaceName,
					"secretData": data,
				},
			}); err != nil {
				t.Fatalf("failed to create a configmap: %v", err)
			}

			t.Logf("waiting for configmap %s|%s to have a response", workspaceCluster, configmapName)
			var configmap corev1.ConfigMap
			if err := wait.PollImmediate(100*time.Millisecond, wait.ForeverTestTimeout, func() (done bool, err error) {
				fetchErr := c.Get(context.TODO(), client.ObjectKey{Namespace: namespaceName, Name: configmapName}, &configmap)
				if fetchErr != nil {
					t.Logf("failed to get configmap %s|%s/%s: %v", workspaceCluster, namespaceName, configmapName, err)
					return false, fetchErr
				}
				response, ok := configmap.Labels["response"]
				if !ok {
					t.Logf("configmap %s|%s/%s has no response set", workspaceCluster, namespaceName, configmapName)
				}
				diff := cmp.Diff(response, "hello-timothy")
				if ok && diff != "" {
					t.Logf("configmap %s|%s/%s has an invalid response: %v", workspaceCluster, namespaceName, configmapName, diff)
				}
				return diff == "", nil
			}); err != nil {
				t.Fatalf("configmap %s|%s/%s never got a response: %v", workspaceCluster, namespaceName, configmapName, err)
			}

			t.Logf("waiting for namespace %s|%s to exist", workspaceCluster, otherNamespaceName)
			var otherNamespace corev1.Namespace
			if err := wait.PollImmediate(100*time.Millisecond, wait.ForeverTestTimeout, func() (done bool, err error) {
				fetchErr := c.Get(context.TODO(), client.ObjectKey{Name: otherNamespaceName}, &otherNamespace)
				if fetchErr != nil && !apierrors.IsNotFound(fetchErr) {
					t.Logf("failed to get namespace %s|%s: %v", workspaceCluster, otherNamespaceName, fetchErr)
					return false, fetchErr
				}
				return fetchErr == nil, nil
			}); err != nil {
				t.Fatalf("namespace %s|%s never created: %v", workspaceCluster, otherNamespaceName, err)
			}

			t.Logf("waiting for secret %s|%s/%s to exist and have correct data", workspaceCluster, namespaceName, configmapName)
			var secret corev1.Secret
			if err := wait.PollImmediate(100*time.Millisecond, wait.ForeverTestTimeout, func() (done bool, err error) {
				fetchErr := c.Get(context.TODO(), client.ObjectKey{Namespace: namespaceName, Name: configmapName}, &secret)
				if fetchErr != nil && !apierrors.IsNotFound(fetchErr) {
					t.Logf("failed to get secret %s|%s/%s: %v", workspaceCluster, namespaceName, configmapName, fetchErr)
					return false, fetchErr
				}
				response, ok := secret.Data["dataFromCM"]
				if !ok {
					t.Logf("secret %s|%s/%s has no data set", workspaceCluster, namespaceName, configmapName)
				}
				diff := cmp.Diff(string(response), data)
				if ok && diff != "" {
					t.Logf("secret %s|%s/%s has invalid data: %v", workspaceCluster, namespaceName, configmapName, diff)
				}
				return diff == "", nil
			}); err != nil {
				t.Fatalf("secret %s|%s/%s never created: %v", workspaceCluster, namespaceName, configmapName, err)
			}
		})
	}
}

// TestWidgetController verifies that our ConfigMap behavior works.
func TestWidgetController(t *testing.T) {
	t.Parallel()
	for i := 0; i < 3; i++ {
		t.Run(fmt.Sprintf("attempt-%d", i), func(t *testing.T) {
			t.Parallel()
			workspaceCluster := parentWorkspace(t).Join(randomName())
			c := createWorkspace(t, workspaceCluster)

			var totalWidgets int
			for i := 0; i < 3; i++ {
				namespaceName := randomName()
				t.Logf("creating namespace %s|%s", workspaceCluster, namespaceName)
				if err := c.Create(context.TODO(), &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: namespaceName},
				}); err != nil {
					t.Fatalf("failed to create a namespace: %v", err)
				}
				numWidgets := rand.Intn(10)
				for i := 0; i < numWidgets; i++ {
					if err := c.Create(context.TODO(), &datav1alpha1.Widget{
						ObjectMeta: metav1.ObjectMeta{Namespace: namespaceName, Name: fmt.Sprintf("widget-%d", i)},
						Spec:       datav1alpha1.WidgetSpec{Foo: fmt.Sprintf("intended-%d", i)},
					}); err != nil {
						t.Fatalf("failed to create widget: %v", err)
					}
				}
				totalWidgets += numWidgets
			}

			t.Logf("waiting for all widgets in cluster %s to have a correct status", workspaceCluster)
			var allWidgets datav1alpha1.WidgetList
			if err := wait.PollImmediate(100*time.Millisecond, wait.ForeverTestTimeout, func() (done bool, err error) {
				fetchErr := c.List(context.TODO(), &allWidgets)
				if fetchErr != nil {
					t.Logf("failed to get widgets in cluster %s: %v", workspaceCluster, err)
					return false, fetchErr
				}
				var errs []error
				for _, widget := range allWidgets.Items {
					if actual, expected := widget.Status.Total, totalWidgets; actual != expected {
						errs = append(errs, fmt.Errorf("widget %s|%s .status.total incorrect: %d != %d", workspaceCluster, widget.Name, actual, expected))
					}
				}
				validationErr := errors.NewAggregate(errs)
				if validationErr != nil {
					t.Logf("widgets in cluster %s invalid: %v", workspaceCluster, validationErr)
				}
				return validationErr == nil, nil
			}); err != nil {
				t.Fatalf("widgets in cluster %s never got correct statuses: %v", workspaceCluster, err)
			}
		})
	}
}

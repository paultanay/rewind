// Package kubernetes implements the Kubernetes API collector for Rewind.
//
// What it collects (per spec §8):
//   - Core/v1 Events in scope → mapped to model EventKind values
//   - ReplicaSet revision history → reconstructed as Deploy events
//   - Pod status (OOMKilled, Terminating, CrashLoopBackOff)
//   - Ownership chain: pod → replicaset → deployment → service → Entity graph
//
// Design constraints:
//   - Read-only: only List/Get verbs used, never write.
//   - Uses client-go with the user's kubeconfig; context is respected.
//   - No reflector/informer/cache — purely post-hoc queries against the API.
//   - Falls back gracefully: missing RBAC perms on one resource type
//     produce a warning, not a fatal error.
package kubernetes

import (
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// newClientset builds a Kubernetes clientset from the given kubeconfig path
// and optional context override.
// Falls back to in-cluster config if kubeconfig is empty and the binary is
// running inside a pod.
func newClientset(kubeconfigPath, contextName string) (kubernetes.Interface, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfigPath != "" {
		rules.ExplicitPath = kubeconfigPath
	} else if kc := os.Getenv("KUBECONFIG"); kc != "" {
		rules.ExplicitPath = kc
	} else {
		// Try the default location.
		if home, err := os.UserHomeDir(); err == nil {
			defaultKC := filepath.Join(home, ".kube", "config")
			if _, err := os.Stat(defaultKC); err == nil {
				rules.ExplicitPath = defaultKC
			}
		}
	}

	overrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		overrides.CurrentContext = contextName
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules, overrides,
	).ClientConfig()
	if err != nil {
		// If kubeconfig lookup fails entirely, try in-cluster.
		inCluster, icErr := rest.InClusterConfig()
		if icErr != nil {
			return nil, fmt.Errorf("kubernetes: cannot build config (kubeconfig: %w; in-cluster: %v)", err, icErr)
		}
		config = inCluster
	}

	// Rewind is read-only and non-interactive — reduce QPS to be polite.
	config.QPS = 20
	config.Burst = 30

	cs, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("kubernetes: build clientset: %w", err)
	}
	return cs, nil
}

// listOptions builds a ListOptions scoped to the given namespace and
// optional label selector.
func listOptions(labelSelector string) metav1.ListOptions {
	return metav1.ListOptions{
		LabelSelector: labelSelector,
		Limit:         500, // safety cap per API call
	}
}

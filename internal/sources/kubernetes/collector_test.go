package kubernetes_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/paultanay/rewind/internal/model"
	k8scol "github.com/paultanay/rewind/internal/sources/kubernetes"
)

var base = time.Date(2026, 7, 9, 14, 20, 0, 0, time.UTC)
var window = model.TimeRange{From: base, To: base.Add(45 * time.Minute)}

// newFakeCollector wires a fake clientset into the collector so no real
// cluster is needed. The fake clientset is pre-populated by each test.
func newFakeCollector(cs *fake.Clientset) *k8scol.Collector {
	c := &k8scol.Collector{Version: "test"}
	c.SetClientset(cs) // exposed via test helper
	return c
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestCollect_OOMKillEvent(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{
				Name: "oom-event", Namespace: "shop", UID: "uid-001",
			},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod", Name: "checkout-7d9f",
				Namespace: "shop",
			},
			Reason:        "OOMKilling",
			Message:       "memory limit exceeded",
			LastTimestamp: metav1.NewTime(base.Add(80 * time.Second)),
		},
	)

	c := newFakeCollector(cs)
	result, err := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}},
		window,
	)
	if err != nil {
		t.Fatalf("Collect error: %v", err)
	}

	oomEvents := filterByKind(result.Events, model.EventKindOOMKill)
	if len(oomEvents) == 0 {
		t.Fatal("expected OOMKill event, got none")
	}
	if oomEvents[0].Severity != model.SeverityCritical {
		t.Errorf("OOMKill severity = %v, want Critical", oomEvents[0].Severity)
	}
}

func TestCollect_RestartEvent(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "restart-evt", Namespace: "shop"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod", Name: "checkout-abc", Namespace: "shop",
			},
			Reason:        "BackOff",
			Message:       "Back-off restarting failed container",
			LastTimestamp: metav1.NewTime(base.Add(2 * time.Minute)),
		},
	)
	c := newFakeCollector(cs)
	result, _ := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}},
		window,
	)
	restartEvts := filterByKind(result.Events, model.EventKindRestart)
	if len(restartEvts) == 0 {
		t.Fatal("expected Restart event, got none")
	}
}

func TestCollect_DeployRollout(t *testing.T) {
	t.Parallel()
	// A new ReplicaSet created at base+1m with a changed image → Deploy event.
	cs := fake.NewSimpleClientset(
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "checkout-v2", Namespace: "shop",
				CreationTimestamp: metav1.NewTime(base.Add(1 * time.Minute)),
				Annotations: map[string]string{
					"deployment.kubernetes.io/revision": "2",
				},
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "Deployment", Name: "checkout"},
				},
			},
			Spec: appsv1.ReplicaSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Image: "checkout:v2.3.1"},
						},
					},
				},
			},
		},
		&appsv1.ReplicaSet{
			ObjectMeta: metav1.ObjectMeta{
				Name: "checkout-v1", Namespace: "shop",
				CreationTimestamp: metav1.NewTime(base.Add(-30 * time.Minute)),
				Annotations: map[string]string{
					"deployment.kubernetes.io/revision": "1",
				},
				OwnerReferences: []metav1.OwnerReference{
					{Kind: "Deployment", Name: "checkout"},
				},
			},
			Spec: appsv1.ReplicaSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{Image: "checkout:v2.3.0"},
						},
					},
				},
			},
		},
	)

	c := newFakeCollector(cs)
	result, err := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}},
		window,
	)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	deployEvts := filterByKind(result.Events, model.EventKindDeploy)
	if len(deployEvts) == 0 {
		t.Fatal("expected Deploy event from ReplicaSet rollout, got none")
	}
	if deployEvts[0].Severity != model.SeverityNotable {
		t.Errorf("Deploy severity = %v, want Notable", deployEvts[0].Severity)
	}
	// Image diff should mention the new image.
	if deployEvts[0].Detail == "" {
		t.Error("Deploy event Detail (image diff) is empty")
	}
}

func TestCollect_EventOutsideWindow(t *testing.T) {
	t.Parallel()
	// Event 2 hours before window — should be excluded.
	cs := fake.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: "old-event", Namespace: "shop"},
			InvolvedObject: corev1.ObjectReference{
				Kind: "Pod", Name: "checkout-old", Namespace: "shop",
			},
			Reason:        "OOMKilling",
			LastTimestamp: metav1.NewTime(base.Add(-2 * time.Hour)),
		},
	)
	c := newFakeCollector(cs)
	result, _ := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}},
		window,
	)
	if len(result.Events) != 0 {
		t.Errorf("expected 0 events outside window, got %d", len(result.Events))
	}
}

func TestCollect_NodePressure(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "node-pressure", Namespace: "shop"},
			InvolvedObject: corev1.ObjectReference{Kind: "Node", Name: "worker-1"},
			Reason:         "NodeMemoryPressure",
			Message:        "Node has memory pressure",
			LastTimestamp:  metav1.NewTime(base.Add(5 * time.Minute)),
		},
	)
	c := newFakeCollector(cs)
	result, _ := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}},
		window,
	)
	nodeEvts := filterByKind(result.Events, model.EventKindNodePressure)
	if len(nodeEvts) == 0 {
		t.Fatal("expected NodePressure event, got none")
	}
	if nodeEvts[0].Severity != model.SeverityCritical {
		t.Errorf("NodePressure severity = %v, want Critical", nodeEvts[0].Severity)
	}
}

func TestCollect_ServiceScopeFilter(t *testing.T) {
	t.Parallel()
	// Two events: one for "checkout", one for "frontend". Scope to checkout only.
	cs := fake.NewSimpleClientset(
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "evt-checkout", Namespace: "shop"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "checkout-abc", Namespace: "shop"},
			Reason:         "OOMKilling",
			LastTimestamp:  metav1.NewTime(base.Add(1 * time.Minute)),
		},
		&corev1.Event{
			ObjectMeta:     metav1.ObjectMeta{Name: "evt-frontend", Namespace: "shop"},
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "frontend-xyz", Namespace: "shop"},
			Reason:         "OOMKilling",
			LastTimestamp:  metav1.NewTime(base.Add(2 * time.Minute)),
		},
	)
	c := newFakeCollector(cs)
	result, _ := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}, Services: []string{"checkout"}},
		window,
	)
	for _, e := range result.Events {
		if e.EntityID != "" && containsStr(e.EntityID, "frontend") {
			t.Errorf("event for frontend leaked through service scope filter: %s", e.EntityID)
		}
	}
}

func TestCollect_EmptyCluster(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset()
	c := newFakeCollector(cs)
	result, err := c.Collect(context.Background(),
		model.Scope{Namespaces: []string{"shop"}},
		window,
	)
	if err != nil {
		t.Fatalf("Collect on empty cluster: %v", err)
	}
	if len(result.Events) != 0 || len(result.Entities) != 0 {
		t.Errorf("expected empty result, got %d events %d entities", len(result.Events), len(result.Entities))
	}
}

func TestCollect_ReportsNamespaceFailure(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("events API unavailable")
	})
	c := newFakeCollector(cs)
	_, err := c.Collect(context.Background(), model.Scope{Namespaces: []string{"shop"}}, window)
	if err == nil {
		t.Fatal("Collect returned nil error for a namespace collection failure")
	}
}

func TestCollect_ReportsReplicaSetFailure(t *testing.T) {
	t.Parallel()
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "replicasets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("replicaset API unavailable")
	})
	c := newFakeCollector(cs)
	_, err := c.Collect(context.Background(), model.Scope{Namespaces: []string{"shop"}}, window)
	if err == nil {
		t.Fatal("Collect returned nil error for a ReplicaSet collection failure")
	}
}

func TestCollect_Name(t *testing.T) {
	c := &k8scol.Collector{}
	if c.Name() != "kubernetes" {
		t.Errorf("Name() = %q, want kubernetes", c.Name())
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func filterByKind(events []model.Event, kind model.EventKind) []model.Event {
	var out []model.Event
	for _, e := range events {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && (s[:len(sub)] == sub || containsStr(s[1:], sub)))
}

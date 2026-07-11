package kubernetes

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/rewind-io/rewind/internal/model"
	"github.com/rewind-io/rewind/internal/sources"
)

const sourceName = "kubernetes"

// Collector implements sources.Collector for the Kubernetes API.
type Collector struct {
	// KubeconfigPath overrides the default kubeconfig discovery.
	KubeconfigPath string
	// ContextName selects a named context from kubeconfig.
	ContextName string
	// Version is the rewind binary version string.
	Version string

	cs kubernetes.Interface // set on first Collect call or via SetClientset
}

// SetClientset injects a pre-built clientset. Used in tests to provide a
// fake clientset without a real cluster.
func (c *Collector) SetClientset(cs kubernetes.Interface) {
	c.cs = cs
}

// Name implements sources.Collector.
func (c *Collector) Name() string { return sourceName }

// Check implements sources.Collector.
func (c *Collector) Check(ctx context.Context) error {
	cs, err := c.clientset()
	if err != nil {
		return err
	}
	_, err = cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	return err
}

func (c *Collector) clientset() (kubernetes.Interface, error) {
	if c.cs != nil {
		return c.cs, nil
	}
	cs, err := newClientset(c.KubeconfigPath, c.ContextName)
	if err != nil {
		return nil, err
	}
	c.cs = cs
	return cs, nil
}

// Collect implements sources.Collector.
func (c *Collector) Collect(ctx context.Context, scope model.Scope, window model.TimeRange) (sources.CollectResult, error) {
	cs, err := c.clientset()
	if err != nil {
		return sources.CollectResult{}, fmt.Errorf("kubernetes: %w", err)
	}

	namespaces := scope.Namespaces
	if len(namespaces) == 0 {
		// Discover all accessible namespaces.
		nsList, nsErr := cs.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if nsErr == nil {
			for _, ns := range nsList.Items {
				namespaces = append(namespaces, ns.Name)
			}
		}
	}

	var (
		allEvents   []model.Event
		allEntities []model.Entity
	)

	for _, ns := range namespaces {
		evts, ents, err := c.collectNamespace(ctx, cs, ns, scope.Services, window)
		if err != nil {
			// Log but continue — per-namespace errors should not abort all collection.
			fmt.Printf("kubernetes: namespace %s: %v\n", ns, err)
			continue
		}
		allEvents = append(allEvents, evts...)
		allEntities = append(allEntities, ents...)
	}

	return sources.CollectResult{
		Events:   allEvents,
		Entities: deduplicateEntities(allEntities),
	}, nil
}

func (c *Collector) collectNamespace(
	ctx context.Context,
	cs kubernetes.Interface,
	ns string,
	services []string,
	window model.TimeRange,
) ([]model.Event, []model.Entity, error) {
	var events []model.Event
	var entities []model.Entity

	// ── Core/v1 Events ───────────────────────────────────────────────────────
	k8sEvents, err := cs.CoreV1().Events(ns).List(ctx, listOptions(""))
	if err != nil {
		return nil, nil, fmt.Errorf("list events: %w", err)
	}
	for _, e := range k8sEvents.Items {
		at := eventTime(e)
		if at.IsZero() || !window.Contains(at) {
			continue
		}
		// Filter by service scope if provided.
		if len(services) > 0 && !serviceInScope(e.InvolvedObject.Name, services) {
			continue
		}
		mapped := mapK8sEvent(e, ns)
		if mapped != nil {
			events = append(events, *mapped)
		}
	}

	// ── ReplicaSet rollout reconstruction → Deploy events ────────────────────
	// A Deploy event is reconstructed when a new ReplicaSet revision appears
	// with an image change relative to its predecessor.
	deployEvents, deployEntities := c.reconstructRollouts(ctx, cs, ns, services, window)
	events = append(events, deployEvents...)
	entities = append(entities, deployEntities...)

	// ── Pod-level entities ───────────────────────────────────────────────────
	pods, podErr := cs.CoreV1().Pods(ns).List(ctx, listOptions(""))
	if podErr == nil {
		for _, pod := range pods.Items {
			if len(services) > 0 && !serviceInScope(pod.Name, services) {
				continue
			}
			entities = append(entities, podEntity(pod, ns))

			// OOMKill events from pod container statuses.
			for _, cs2 := range pod.Status.ContainerStatuses {
				if cs2.LastTerminationState.Terminated != nil &&
					cs2.LastTerminationState.Terminated.Reason == "OOMKilled" {
					at := cs2.LastTerminationState.Terminated.FinishedAt.Time
					if window.Contains(at) {
						events = append(events, model.Event{
							ID:        model.NewEventID(),
							At:        at,
							Kind:      model.EventKindOOMKill,
							EntityID:  model.NewEntityID(model.EntityKindPod, ns, pod.Name),
							Severity:  model.SeverityCritical,
							Title:     fmt.Sprintf("OOMKilled: %s/%s", pod.Name, cs2.Name),
							Detail:    fmt.Sprintf("Container: %s  Exit: %d", cs2.Name, cs2.LastTerminationState.Terminated.ExitCode),
							SourceRef: model.SourceRef{SourceName: sourceName},
						})
					}
				}
			}
		}
	}

	return events, entities, nil
}

// reconstructRollouts examines ReplicaSets to find deployment rollouts in the
// window and synthesises Deploy events with image diff in the Detail field.
func (c *Collector) reconstructRollouts(
	ctx context.Context,
	cs kubernetes.Interface,
	ns string,
	services []string,
	window model.TimeRange,
) ([]model.Event, []model.Entity) {
	var events []model.Event
	var entities []model.Entity

	rsList, err := cs.AppsV1().ReplicaSets(ns).List(ctx, listOptions(""))
	if err != nil {
		return nil, nil
	}

	// Group ReplicaSets by deployment name (annotation: deployment.kubernetes.io/revision).
	deployMap := map[string][]rsRecord{} // deployment name → []rsRecord

	for _, rs := range rsList.Items {
		// Only care about ReplicaSets owned by a Deployment.
		owner := deploymentOwner(&rs)
		if owner == "" {
			continue
		}
		if len(services) > 0 && !serviceInScope(owner, services) {
			continue
		}

		var revision int
		fmt.Sscanf(rs.Annotations["deployment.kubernetes.io/revision"], "%d", &revision)

		var imgs []string
		for _, c2 := range rs.Spec.Template.Spec.Containers {
			imgs = append(imgs, c2.Image)
		}

		deployMap[owner] = append(deployMap[owner], rsRecord{
			revision:  revision,
			createdAt: rs.CreationTimestamp.Time,
			images:    imgs,
			rsName:    rs.Name,
		})
	}

	// For each deployment, find the RS with the highest revision created within
	// the window (or just before it — deploys 2h before are prime suspects).
	lookback := window.From.Add(-2 * time.Hour)
	for deployName, records := range deployMap {
		// Sort by revision ascending.
		sortByRevision(records)

		for i, rec := range records {
			if rec.createdAt.Before(lookback) || rec.createdAt.After(window.To) {
				continue
			}

			// Build image diff vs predecessor.
			var prevImages []string
			if i > 0 {
				prevImages = records[i-1].images
			}

			entityID := model.NewEntityID(model.EntityKindDeployment, ns, deployName)
			events = append(events, model.Event{
				ID:       model.NewEventID(),
				At:       rec.createdAt,
				Kind:     model.EventKindDeploy,
				EntityID: entityID,
				Severity: model.SeverityNotable,
				Title:    fmt.Sprintf("Deployed %s (revision %d)", deployName, rec.revision),
				Detail:   imageDiff(prevImages, rec.images),
				SourceRef: model.SourceRef{
					SourceName: sourceName,
					NativeID:   rec.rsName,
				},
			})
			entities = append(entities, model.Entity{
				ID:          entityID,
				Kind:        model.EntityKindDeployment,
				DisplayName: deployName,
				Labels:      map[string]string{"namespace": ns},
			})
		}
	}
	return events, entities
}

// ─── K8s event mapping ────────────────────────────────────────────────────────

// mapK8sEvent converts a Kubernetes core Event to a model.Event.
// Returns nil for event types Rewind doesn't model.
func mapK8sEvent(e corev1.Event, ns string) *model.Event {
	kind, severity := classifyReason(e.Reason)
	if kind == model.EventKindUnknown {
		return nil
	}

	entityKind := model.EntityKindPod
	if e.InvolvedObject.Kind == "Node" {
		entityKind = model.EntityKindNode
	}

	entityID := model.NewEntityID(entityKind, ns, e.InvolvedObject.Name)

	return &model.Event{
		ID:       model.NewEventID(),
		At:       eventTime(e),
		Kind:     kind,
		EntityID: entityID,
		Severity: severity,
		Title:    fmt.Sprintf("%s: %s", e.Reason, e.InvolvedObject.Name),
		Detail:   e.Message,
		SourceRef: model.SourceRef{
			SourceName: sourceName,
			NativeID:   string(e.UID),
		},
	}
}

// classifyReason maps a Kubernetes event Reason string to a model EventKind.
var reasonMap = map[string]struct {
	kind     model.EventKind
	severity model.Severity
}{
	"OOMKilling":         {model.EventKindOOMKill, model.SeverityCritical},
	"OOMKilled":          {model.EventKindOOMKill, model.SeverityCritical},
	"Killing":            {model.EventKindPodKilled, model.SeverityNotable},
	"BackOff":            {model.EventKindRestart, model.SeverityNotable},
	"CrashLoopBackOff":   {model.EventKindCrashLoop, model.SeverityCritical},
	"Restarting":         {model.EventKindRestart, model.SeverityNotable},
	"ScalingReplicaSet":  {model.EventKindScaleChange, model.SeverityInfo},
	"SuccessfulRescale":  {model.EventKindScaleChange, model.SeverityInfo},
	"FailedMount":        {model.EventKindProbeFailed, model.SeverityNotable},
	"Unhealthy":          {model.EventKindProbeFailed, model.SeverityNotable},
	"NodeNotReady":       {model.EventKindNodePressure, model.SeverityCritical},
	"NodeMemoryPressure": {model.EventKindNodePressure, model.SeverityCritical},
	"NodeDiskPressure":   {model.EventKindNodePressure, model.SeverityCritical},
	"Evicted":            {model.EventKindPodKilled, model.SeverityCritical},
	"Evicting":           {model.EventKindPodKilled, model.SeverityNotable},
}

func classifyReason(reason string) (model.EventKind, model.Severity) {
	if v, ok := reasonMap[reason]; ok {
		return v.kind, v.severity
	}
	// Fuzzy match common patterns.
	r := strings.ToLower(reason)
	switch {
	case strings.Contains(r, "oom"):
		return model.EventKindOOMKill, model.SeverityCritical
	case strings.Contains(r, "kill"):
		return model.EventKindPodKilled, model.SeverityNotable
	case strings.Contains(r, "restart"):
		return model.EventKindRestart, model.SeverityNotable
	case strings.Contains(r, "scale"):
		return model.EventKindScaleChange, model.SeverityInfo
	case strings.Contains(r, "pressure") || strings.Contains(r, "evict"):
		return model.EventKindNodePressure, model.SeverityCritical
	}
	return model.EventKindUnknown, model.SeverityInfo
}

// ─── Entity helpers ───────────────────────────────────────────────────────────

func podEntity(pod corev1.Pod, ns string) model.Entity {
	owner := ""
	for _, ref := range pod.OwnerReferences {
		if ref.Kind == "ReplicaSet" {
			owner = model.NewEntityID(model.EntityKindDeployment, ns, ref.Name)
		}
	}
	labels := make(map[string]string, len(pod.Labels))
	for k, v := range pod.Labels {
		labels[k] = v
	}
	return model.Entity{
		ID:          model.NewEntityID(model.EntityKindPod, ns, pod.Name),
		Kind:        model.EntityKindPod,
		Owner:       owner,
		DisplayName: pod.Name,
		Labels:      labels,
	}
}

func deploymentOwner(rs interface {
	GetOwnerReferences() []metav1.OwnerReference
}) string {
	for _, ref := range rs.GetOwnerReferences() {
		if ref.Kind == "Deployment" {
			return ref.Name
		}
	}
	return ""
}

func eventTime(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}

func serviceInScope(name string, services []string) bool {
	for _, svc := range services {
		if strings.Contains(name, svc) {
			return true
		}
	}
	return false
}

func imageDiff(prev, next []string) string {
	if len(prev) == 0 {
		return fmt.Sprintf("Images: %s", strings.Join(next, ", "))
	}
	prevSet := map[string]bool{}
	for _, img := range prev {
		prevSet[img] = true
	}
	var changed []string
	for _, img := range next {
		if !prevSet[img] {
			changed = append(changed, img)
		}
	}
	if len(changed) == 0 {
		return fmt.Sprintf("Images (unchanged): %s", strings.Join(next, ", "))
	}
	return fmt.Sprintf("New images: %s", strings.Join(changed, ", "))
}

type rsRecord struct {
	revision  int
	createdAt time.Time
	images    []string
	rsName    string
}

func sortByRevision(records []rsRecord) {
	for i := 1; i < len(records); i++ {
		for j := i; j > 0 && records[j].revision < records[j-1].revision; j-- {
			records[j], records[j-1] = records[j-1], records[j]
		}
	}
}

func deduplicateEntities(entities []model.Entity) []model.Entity {
	seen := map[string]bool{}
	out := entities[:0]
	for _, e := range entities {
		if !seen[e.ID] {
			seen[e.ID] = true
			out = append(out, e)
		}
	}
	return out
}

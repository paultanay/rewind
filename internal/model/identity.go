package model

import (
	"fmt"
	"strings"
)

// CanonicalEntityID constructs the stable identifier used to join entities
// across observability sources. Namespaces are required for namespaced
// Kubernetes resources; nodes are cluster-scoped and therefore have no
// namespace component.
func CanonicalEntityID(kind EntityKind, namespace, name string) (string, error) {
	if !validIdentitySegment(name) {
		return "", fmt.Errorf("entity name must be non-empty and contain no path separators: %q", name)
	}

	switch kind {
	case EntityKindNode:
		if namespace != "" {
			return "", fmt.Errorf("node entity must not have a namespace: %q", namespace)
		}
		return "node/" + name, nil
	case EntityKindService, EntityKindDeployment, EntityKindPod, EntityKindQueue, EntityKindDatabase:
		if !validIdentitySegment(namespace) {
			return "", fmt.Errorf("namespace must be non-empty and contain no path separators: %q", namespace)
		}
		return entityKindPrefix(kind) + "/" + namespace + "/" + name, nil
	default:
		return "", fmt.Errorf("entity kind %q has no canonical identifier", kind)
	}
}

// NormalizeEntityID converts canonical and supported legacy IDs into the
// canonical form. It is intentionally strict: an unrecognized or ambiguous ID
// must not be allowed to create a false cross-source join.
func NormalizeEntityID(id string) (string, EntityKind, error) {
	parts := strings.Split(id, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return "", EntityKindUnknown, fmt.Errorf("invalid entity ID %q: want kind/name or kind/namespace/name", id)
	}

	kind, ok := legacyEntityKinds[parts[0]]
	if !ok {
		return "", EntityKindUnknown, fmt.Errorf("invalid entity ID %q: unknown kind %q", id, parts[0])
	}
	if kind == EntityKindNode {
		if len(parts) != 2 {
			return "", EntityKindUnknown, fmt.Errorf("invalid node entity ID %q", id)
		}
		canonical, err := CanonicalEntityID(kind, "", parts[1])
		return canonical, kind, err
	}
	if len(parts) != 3 {
		return "", EntityKindUnknown, fmt.Errorf("invalid namespaced entity ID %q", id)
	}
	canonical, err := CanonicalEntityID(kind, parts[1], parts[2])
	return canonical, kind, err
}

// NormalizeIncident rewrites known legacy entity references in an incident to
// canonical IDs. Unknown custom IDs are preserved so integrations can carry
// their own entity vocabulary without losing data; no fuzzy matching is done.
func NormalizeIncident(inc Incident) Incident {
	for i := range inc.Entities {
		inc.Entities[i].ID = normalizeKnownID(inc.Entities[i].ID)
		inc.Entities[i].Owner = normalizeKnownID(inc.Entities[i].Owner)
	}
	for i := range inc.Events {
		inc.Events[i].EntityID = normalizeKnownID(inc.Events[i].EntityID)
	}
	for i := range inc.Signals {
		inc.Signals[i].EntityID = normalizeKnownID(inc.Signals[i].EntityID)
	}
	return inc
}

func normalizeKnownID(id string) string {
	if id == "" {
		return id
	}
	canonical, _, err := NormalizeEntityID(id)
	if err != nil {
		return id
	}
	return canonical
}

func validIdentitySegment(value string) bool {
	return value != "" && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "/\\")
}

func entityKindPrefix(kind EntityKind) string {
	switch kind {
	case EntityKindService:
		return "service"
	case EntityKindDeployment:
		return "deployment"
	case EntityKindPod:
		return "pod"
	case EntityKindQueue:
		return "queue"
	case EntityKindDatabase:
		return "database"
	default:
		return ""
	}
}

var legacyEntityKinds = map[string]EntityKind{
	"service":    EntityKindService,
	"svc":        EntityKindService,
	"deployment": EntityKindDeployment,
	"deploy":     EntityKindDeployment,
	"pod":        EntityKindPod,
	"node":       EntityKindNode,
	"queue":      EntityKindQueue,
	"database":   EntityKindDatabase,
	"db":         EntityKindDatabase,
}

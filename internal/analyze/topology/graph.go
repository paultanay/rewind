// Package topology builds and queries the entity graph used by the correlation
// engine. The graph is a directed ownership DAG:
//
//	Pod → ReplicaSet/Deployment → Service
//	Node → (pods scheduled on it, via label/nodeName)
//
// Additionally, when Tempo call-graph data is present (Phase 5), service-to-
// service edges are added. The correlation rules use the graph to score
// proximity: effects on graph-adjacent entities are more likely caused by the
// same trigger than effects on unrelated entities.
//
// All operations are read-only and safe for concurrent use after construction.
package topology

import (
	"strings"

	"github.com/rewind-io/rewind/internal/model"
)

// Graph is an immutable directed entity graph. Build it once per analysis run.
type Graph struct {
	// nodes maps entity ID → Entity for O(1) lookup.
	nodes map[string]model.Entity
	// children maps parent ID → []child IDs (e.g. deployment → pods).
	children map[string][]string
	// parents maps child ID → parent ID.
	parents map[string]string
	// callEdges maps caller service ID → []callee service IDs (from Tempo).
	callEdges map[string][]string
}

// Build constructs a Graph from a flat slice of entities. The ownership chain
// is established via Entity.Owner: a pod's Owner is its deployment ID, a
// deployment's Owner is its service ID.
func Build(entities []model.Entity) *Graph {
	g := &Graph{
		nodes:     make(map[string]model.Entity, len(entities)),
		children:  make(map[string][]string, len(entities)),
		parents:   make(map[string]string, len(entities)),
		callEdges: make(map[string][]string),
	}
	for _, e := range entities {
		g.nodes[e.ID] = e
		if e.Owner != "" {
			g.parents[e.ID] = e.Owner
			g.children[e.Owner] = append(g.children[e.Owner], e.ID)
		}
	}
	return g
}

// AddCallEdge records a service-to-service call relationship (caller → callee).
// This is populated from Tempo trace data in Phase 5.
func (g *Graph) AddCallEdge(callerID, calleeID string) {
	g.callEdges[callerID] = append(g.callEdges[callerID], calleeID)
}

// Entity returns the entity for the given ID, or nil if not found.
func (g *Graph) Entity(id string) *model.Entity {
	if e, ok := g.nodes[id]; ok {
		return &e
	}
	return nil
}

// RootAncestor walks the ownership chain upward and returns the topmost
// ancestor ID (usually the Service). Returns id itself if no parent exists.
func (g *Graph) RootAncestor(id string) string {
	visited := map[string]bool{}
	current := id
	for {
		if visited[current] {
			break // cycle guard
		}
		visited[current] = true
		parent, ok := g.parents[current]
		if !ok || parent == "" {
			break
		}
		current = parent
	}
	return current
}

// ProximityScore returns a [0,1] score representing how topologically close
// two entities are. Used by correlation rules to weight (trigger→effect) edges.
//
// Score table:
//
//	Same entity           → 1.0
//	Direct parent/child   → 0.8
//	Same root ancestor    → 0.6
//	Upstream call-graph   → 0.5
//	Unrelated             → 0.0
func (g *Graph) ProximityScore(idA, idB string) float64 {
	if idA == idB {
		return 1.0
	}
	// Direct parent/child.
	if g.parents[idA] == idB || g.parents[idB] == idA {
		return 0.8
	}
	// Siblings (same parent).
	if pa, pb := g.parents[idA], g.parents[idB]; pa != "" && pa == pb {
		return 0.7
	}
	// Same root ancestor.
	if g.RootAncestor(idA) == g.RootAncestor(idB) {
		return 0.6
	}
	// Call-graph adjacency (A calls B or B calls A).
	for _, callee := range g.callEdges[idA] {
		if callee == idB {
			return 0.5
		}
	}
	for _, callee := range g.callEdges[idB] {
		if callee == idA {
			return 0.5
		}
	}
	// Namespace-level proximity: same namespace prefix.
	if nsOf(idA) == nsOf(idB) && nsOf(idA) != "" {
		return 0.2
	}
	return 0.0
}

// Descendants returns all direct and indirect descendants of id (BFS).
func (g *Graph) Descendants(id string) []string {
	var result []string
	queue := []string{id}
	visited := map[string]bool{id: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range g.children[cur] {
			if !visited[child] {
				visited[child] = true
				result = append(result, child)
				queue = append(queue, child)
			}
		}
	}
	return result
}

// Ancestors returns all direct and indirect ancestors of id (walk via parents).
func (g *Graph) Ancestors(id string) []string {
	var result []string
	current := id
	visited := map[string]bool{id: true}
	for {
		parent, ok := g.parents[current]
		if !ok || parent == "" || visited[parent] {
			break
		}
		visited[parent] = true
		result = append(result, parent)
		current = parent
	}
	return result
}

// AllEntities returns a copy of all entities in the graph.
func (g *Graph) AllEntities() []model.Entity {
	out := make([]model.Entity, 0, len(g.nodes))
	for _, e := range g.nodes {
		out = append(out, e)
	}
	return out
}

// nsOf extracts the namespace component from an entity ID like "svc/shop/checkout"
// or "pod/shop/checkout-abc". Returns empty string if the format is not recognised.
func nsOf(id string) string {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// Package topology builds and queries the entity graph used by the correlation
// engine. The graph is a directed ownership DAG:
//
//	Pod → ReplicaSet/Deployment → Service
//	Node → (pods scheduled on it, via label/nodeName)
//
// Additionally, when Tempo call-graph data is present , service-to-
// service edges are added. The correlation rules use the graph to score
// proximity: effects on graph-adjacent entities are more likely caused by the
// same trigger than effects on unrelated entities.
//
// All operations are read-only and safe for concurrent use after construction.
package topology

import (
	"sort"
	"strings"

	"github.com/paultanay/rewind/internal/model"
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
	for _, raw := range entities {
		e := canonicalEntity(raw)
		g.nodes[e.ID] = e
		if e.Owner != "" {
			g.parents[e.ID] = e.Owner
			g.children[e.Owner] = append(g.children[e.Owner], e.ID)
		}
	}
	for parent := range g.children {
		sort.Strings(g.children[parent])
	}
	for _, e := range g.nodes {
		for _, callee := range strings.Split(e.Labels["calls"], ",") {
			callee = strings.TrimSpace(callee)
			if callee != "" {
				g.AddCallEdge(e.ID, callee)
			}
		}
	}
	return g
}

// AddCallEdge records a service-to-service call relationship (caller → callee).
// This is populated from Tempo trace data.
func (g *Graph) AddCallEdge(callerID, calleeID string) {
	callerID = normalizeLookupID(callerID)
	calleeID = normalizeLookupID(calleeID)
	if callerID == "" || calleeID == "" || callerID == calleeID {
		return
	}
	for _, existing := range g.callEdges[callerID] {
		if existing == calleeID {
			return
		}
	}
	g.callEdges[callerID] = append(g.callEdges[callerID], calleeID)
	sort.Strings(g.callEdges[callerID])
}

// Entity returns the entity for the given ID, or nil if not found.
func (g *Graph) Entity(id string) *model.Entity {
	id = normalizeLookupID(id)
	if e, ok := g.nodes[id]; ok {
		return &e
	}
	return nil
}

// RootAncestor walks the ownership chain upward and returns the topmost
// ancestor ID (usually the Service). Returns id itself if no parent exists.
func (g *Graph) RootAncestor(id string) string {
	id = normalizeLookupID(id)
	visited := map[string]bool{}
	current := id
	for !visited[current] {
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
	idA = normalizeLookupID(idA)
	idB = normalizeLookupID(idB)
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
	id = normalizeLookupID(id)
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
	id = normalizeLookupID(id)
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
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// nsOf extracts the namespace component from an entity ID like "svc/shop/checkout"
// or "pod/shop/checkout-abc". Returns empty string if the format is not recognized.
func nsOf(id string) string {
	parts := strings.SplitN(id, "/", 3)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// Distance returns the shortest-path hop count between two entities in the
// undirected ownership+call graph. Returns math.MaxInt if no path exists.
// Same entity → 0. Direct parent/child → 1.
func (g *Graph) Distance(fromID, toID string) int {
	fromID = normalizeLookupID(fromID)
	toID = normalizeLookupID(toID)
	if fromID == toID {
		return 0
	}
	// BFS over the bidirectional ownership graph + call edges.
	type entry struct {
		id   string
		dist int
	}
	visited := map[string]bool{fromID: true}
	queue := []entry{{fromID, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range g.neighbors(cur.id) {
			if nb == toID {
				return cur.dist + 1
			}
			if !visited[nb] {
				visited[nb] = true
				queue = append(queue, entry{nb, cur.dist + 1})
			}
		}
	}
	return maxInt
}

// maxInt is the sentinel for "no path" in Distance.
const maxInt = int(^uint(0) >> 1)

// Reachable reports whether toID is reachable from fromID following directed
// ownership edges (parent→child) and call edges (caller→callee).
func (g *Graph) Reachable(fromID, toID string) bool {
	fromID = normalizeLookupID(fromID)
	toID = normalizeLookupID(toID)
	if fromID == toID {
		return true
	}
	visited := map[string]bool{fromID: true}
	queue := []string{fromID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nb := range g.directedNeighbors(cur) {
			if nb == toID {
				return true
			}
			if !visited[nb] {
				visited[nb] = true
				queue = append(queue, nb)
			}
		}
	}
	return false
}

// Adjacent returns all entity IDs directly connected to id in the undirected
// ownership+call graph (parent, children, call peers).
func (g *Graph) Adjacent(id string) []string {
	id = normalizeLookupID(id)
	return g.neighbors(id)
}

// neighbors returns all undirected (bidirectional) neighbors of id.
func (g *Graph) neighbors(id string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(nb string) {
		if nb != "" && !seen[nb] {
			seen[nb] = true
			out = append(out, nb)
		}
	}
	// Parent
	if p := g.parents[id]; p != "" {
		add(p)
	}
	// Children
	for _, c := range g.children[id] {
		add(c)
	}
	// Call edges (both directions)
	for _, callee := range g.callEdges[id] {
		add(callee)
	}
	for caller, callees := range g.callEdges {
		for _, callee := range callees {
			if callee == id {
				add(caller)
			}
		}
	}
	sort.Strings(out)
	return out
}

// directedNeighbors returns children and call-graph callees of id (directed).
func (g *Graph) directedNeighbors(id string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(nb string) {
		if nb != "" && !seen[nb] {
			seen[nb] = true
			out = append(out, nb)
		}
	}
	for _, c := range g.children[id] {
		add(c)
	}
	for _, callee := range g.callEdges[id] {
		add(callee)
	}
	sort.Strings(out)
	return out
}

func canonicalEntity(raw model.Entity) model.Entity {
	e := raw
	if canonical, kind, err := model.NormalizeEntityID(e.ID); err == nil {
		e.ID = canonical
		if e.Kind == model.EntityKindUnknown {
			e.Kind = kind
		}
	}
	e.Owner = normalizeLookupID(e.Owner)
	return e
}

func normalizeLookupID(id string) string {
	if canonical, _, err := model.NormalizeEntityID(id); err == nil {
		return canonical
	}
	return id
}

package topology_test

import (
	"testing"

	"github.com/rewind-io/rewind/internal/analyze/topology"
	"github.com/rewind-io/rewind/internal/model"
)

// ─── fixtures ─────────────────────────────────────────────────────────────────

// buildShopGraph builds a representative topology for the shop namespace:
//
//	svc/shop/checkout (Service)
//	  └─ deploy/shop/checkout (Deployment, owner=svc)
//	       └─ pod/shop/checkout-abc (Pod, owner=deploy)
//	       └─ pod/shop/checkout-def (Pod, owner=deploy)
//	svc/shop/frontend (Service)
//	  └─ deploy/shop/frontend (Deployment, owner=svc)
//	       └─ pod/shop/frontend-xyz (Pod, owner=deploy)
//	node/worker-1 (Node, no owner)
func buildShopGraph() *topology.Graph {
	entities := []model.Entity{
		{ID: "svc/shop/checkout", Kind: model.EntityKindService, DisplayName: "checkout"},
		{ID: "deploy/shop/checkout", Kind: model.EntityKindDeployment, Owner: "svc/shop/checkout"},
		{ID: "pod/shop/checkout-abc", Kind: model.EntityKindPod, Owner: "deploy/shop/checkout"},
		{ID: "pod/shop/checkout-def", Kind: model.EntityKindPod, Owner: "deploy/shop/checkout"},
		{ID: "svc/shop/frontend", Kind: model.EntityKindService, DisplayName: "frontend"},
		{ID: "deploy/shop/frontend", Kind: model.EntityKindDeployment, Owner: "svc/shop/frontend"},
		{ID: "pod/shop/frontend-xyz", Kind: model.EntityKindPod, Owner: "deploy/shop/frontend"},
		{ID: "node/worker-1", Kind: model.EntityKindNode},
	}
	return topology.Build(entities)
}

// ─── tests ────────────────────────────────────────────────────────────────────

func TestProximity_SameEntity(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	if score := g.ProximityScore("svc/shop/checkout", "svc/shop/checkout"); score != 1.0 {
		t.Errorf("same entity score = %.2f, want 1.0", score)
	}
}

func TestProximity_DirectParentChild(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	score := g.ProximityScore("pod/shop/checkout-abc", "deploy/shop/checkout")
	if score < 0.7 {
		t.Errorf("pod→deploy score = %.2f, want ≥0.7", score)
	}
}

func TestProximity_Siblings(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	score := g.ProximityScore("pod/shop/checkout-abc", "pod/shop/checkout-def")
	if score < 0.6 {
		t.Errorf("sibling pods score = %.2f, want ≥0.6", score)
	}
}

func TestProximity_SameRootAncestor(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	// pod and its root service should score 0.6.
	score := g.ProximityScore("pod/shop/checkout-abc", "svc/shop/checkout")
	if score < 0.55 {
		t.Errorf("pod→root-service score = %.2f, want ≥0.55", score)
	}
}

func TestProximity_DifferentServices(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	// checkout and frontend are in the same namespace but unrelated services.
	score := g.ProximityScore("svc/shop/checkout", "svc/shop/frontend")
	// Should score at most namespace-level proximity (0.2), not higher.
	if score > 0.5 {
		t.Errorf("unrelated service score = %.2f, want ≤0.5", score)
	}
}

func TestProximity_CallEdge(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	g.AddCallEdge("svc/shop/frontend", "svc/shop/checkout")
	score := g.ProximityScore("svc/shop/frontend", "svc/shop/checkout")
	if score < 0.5 {
		t.Errorf("call-edge score = %.2f, want ≥0.5", score)
	}
}

func TestProximity_Unrelated(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	// node and checkout service are completely unrelated.
	score := g.ProximityScore("node/worker-1", "svc/shop/checkout")
	if score > 0.3 {
		t.Errorf("unrelated entities score = %.2f, want ≤0.3", score)
	}
}

func TestRootAncestor(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	root := g.RootAncestor("pod/shop/checkout-abc")
	if root != "svc/shop/checkout" {
		t.Errorf("RootAncestor(pod) = %q, want svc/shop/checkout", root)
	}
	// Service has no parent — root is itself.
	root2 := g.RootAncestor("svc/shop/checkout")
	if root2 != "svc/shop/checkout" {
		t.Errorf("RootAncestor(svc) = %q, want itself", root2)
	}
}

func TestDescendants(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	desc := g.Descendants("svc/shop/checkout")
	if len(desc) < 3 { // deploy + 2 pods
		t.Errorf("Descendants(svc) = %d, want ≥3", len(desc))
	}
}

func TestAncestors(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	anc := g.Ancestors("pod/shop/checkout-abc")
	if len(anc) < 2 { // deploy + svc
		t.Errorf("Ancestors(pod) = %d, want ≥2", len(anc))
	}
}

func TestAllEntities(t *testing.T) {
	t.Parallel()
	g := buildShopGraph()
	all := g.AllEntities()
	if len(all) != 8 {
		t.Errorf("AllEntities() = %d, want 8", len(all))
	}
}

func TestBuild_Empty(t *testing.T) {
	t.Parallel()
	g := topology.Build(nil)
	if score := g.ProximityScore("a", "b"); score != 0.0 {
		t.Errorf("empty graph proximity = %.2f, want 0.0", score)
	}
}

func TestBuild_CycleGuard(t *testing.T) {
	t.Parallel()
	// Cyclic ownership — should not infinite-loop in RootAncestor.
	entities := []model.Entity{
		{ID: "a", Owner: "b"},
		{ID: "b", Owner: "a"},
	}
	g := topology.Build(entities)
	_ = g.RootAncestor("a") // must not hang
}

// Tests for path normalization and the cardinality collapse pass — the
// stage that catches slugs and usernames the per-segment heuristics
// cannot see.
package cluster

import (
	"fmt"
	"testing"
)

// routesFor runs the full pipeline over raw paths and returns each
// path's final route, in input order.
func routesFor(t *testing.T, opts Options, paths ...string) []string {
	t.Helper()
	c := New(opts)
	norm := make([]string, len(paths))
	seen := map[string]bool{}
	for i, p := range paths {
		norm[i] = c.Normalize(p)
		if !seen[norm[i]] {
			seen[norm[i]] = true
			c.Observe(norm[i])
		}
	}
	c.Finalize()
	out := make([]string, len(paths))
	for i, n := range norm {
		out[i] = c.Route(n)
	}
	return out
}

func TestNormalizeAppliesHeuristics(t *testing.T) {
	c := New(Options{})
	cases := map[string]string{
		"/users/123":            "/users/:id",
		"/users/123/orders/456": "/users/:id/orders/:id",
		"/api/v2/users/123":     "/api/v2/users/:id",
		"/files/2026-07-06.csv": "/files/:date.csv",
		"/commits/da39a3ee5e6b4b0d3255bfef95601890afd80709": "/commits/:hash",
		"/":       "/",
		"/health": "/health",
	}
	for in, want := range cases {
		if got := c.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeCleansSlashes(t *testing.T) {
	c := New(Options{})
	if got := c.Normalize("//users//7/"); got != "/users/:id" {
		t.Fatalf("Normalize(//users//7/) = %q", got)
	}
	if got := c.Normalize("/users/"); got != "/users" {
		t.Fatalf("trailing slash not stripped: %q", got)
	}
}

func TestNormalizeDisabledReturnsCleanedPath(t *testing.T) {
	c := New(Options{Disabled: true})
	if got := c.Normalize("/users/123/"); got != "/users/123" {
		t.Fatalf("disabled Normalize = %q, want cleaned raw path", got)
	}
}

func TestCollapseHighCardinalitySlugs(t *testing.T) {
	// 13 distinct product slugs under /products with threshold 4:
	// none is ID-like, so only the cardinality pass can merge them.
	paths := []string{"/products"}
	for i := 0; i < 13; i++ {
		paths = append(paths, fmt.Sprintf("/products/widget-%c", 'a'+i))
	}
	routes := routesFor(t, Options{Threshold: 4}, paths...)
	if routes[0] != "/products" {
		t.Fatalf("parent route mangled: %q", routes[0])
	}
	for _, r := range routes[1:] {
		if r != "/products/:param" {
			t.Fatalf("slug not collapsed: %q", r)
		}
	}
}

func TestNoCollapseBelowThreshold(t *testing.T) {
	// A handful of genuine sub-resources must never merge.
	paths := []string{"/api/users", "/api/orders", "/api/health", "/api/settings"}
	routes := routesFor(t, Options{Threshold: 12}, paths...)
	for i, r := range routes {
		if r != paths[i] {
			t.Errorf("route %q collapsed to %q; below threshold", paths[i], r)
		}
	}
}

func TestCollapseMergesSubtrees(t *testing.T) {
	// Every slug has a /reviews child; after collapsing, all of them
	// must share the single route /products/:param/reviews.
	var paths []string
	for i := 0; i < 6; i++ {
		paths = append(paths, fmt.Sprintf("/products/thing-%c/reviews", 'a'+i))
	}
	routes := routesFor(t, Options{Threshold: 3}, paths...)
	for _, r := range routes {
		if r != "/products/:param/reviews" {
			t.Fatalf("subtree not merged: %q", r)
		}
	}
}

func TestCollapseCountsDistinctValuesNotHits(t *testing.T) {
	// Three literals hit many times each stay literal: cardinality is
	// about distinct values, so hot routes are safe.
	c := New(Options{Threshold: 3})
	for i := 0; i < 100; i++ {
		for _, p := range []string{"/a", "/b", "/c"} {
			n := c.Normalize(p)
			if i == 0 {
				c.Observe(n)
			}
		}
	}
	c.Finalize()
	if got := c.Route("/a"); got != "/a" {
		t.Fatalf("hot literal collapsed: %q", got)
	}
}

func TestHeuristicParamsDoNotCountTowardThreshold(t *testing.T) {
	// /users/:id (already merged by heuristics) plus two literals is
	// fine at threshold 2 — only literals count.
	paths := []string{"/users/1", "/users/2", "/users/3", "/users/me", "/users/search"}
	routes := routesFor(t, Options{Threshold: 2}, paths...)
	want := []string{"/users/:id", "/users/:id", "/users/:id", "/users/me", "/users/search"}
	for i := range routes {
		if routes[i] != want[i] {
			t.Errorf("routes[%d] = %q, want %q", i, routes[i], want[i])
		}
	}
}

func TestCollapseMergesIntoExistingParamRecursively(t *testing.T) {
	// After /docs/<slug> collapses, the merged :param node itself has
	// high-cardinality children that must collapse in turn.
	var paths []string
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			paths = append(paths, fmt.Sprintf("/docs/page-%c/rev-%c", 'a'+i, 'a'+j))
		}
	}
	routes := routesFor(t, Options{Threshold: 3}, paths...)
	for _, r := range routes {
		if r != "/docs/:param/:param" {
			t.Fatalf("nested collapse missing: %q", r)
		}
	}
}

func TestDisabledClustererIsIdentity(t *testing.T) {
	routes := routesFor(t, Options{Disabled: true}, "/users/1", "/users/2")
	if routes[0] != "/users/1" || routes[1] != "/users/2" {
		t.Fatalf("disabled clusterer rewrote paths: %v", routes)
	}
}

func TestPathEdgeCases(t *testing.T) {
	// Root routes to itself; non-path targets (CONNECT host:port,
	// "(unparsed)" markers) must pass through untouched, never
	// clustered; Clean canonicalizes slashes.
	routes := routesFor(t, Options{}, "/")
	if routes[0] != "/" {
		t.Fatalf("root route = %q", routes[0])
	}
	c := New(Options{})
	if got := c.Normalize("(unparsed)"); got != "(unparsed)" {
		t.Fatalf("non-path normalized to %q", got)
	}
	cases := map[string]string{
		"":                 "/",
		"/":                "/",
		"//":               "/",
		"/a//b":            "/a/b",
		"/a/b/":            "/a/b",
		"example.test:443": "example.test:443",
	}
	for in, want := range cases {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDefaultThresholdApplied(t *testing.T) {
	c := New(Options{})
	if c.opts.Threshold != DefaultThreshold {
		t.Fatalf("threshold = %d, want default %d", c.opts.Threshold, DefaultThreshold)
	}
}

// Package cluster groups raw URL paths into route patterns. Two stages:
// per-segment heuristics (segment.go) catch structured values like
// numeric IDs and UUIDs immediately, then a cardinality pass over the
// path tree collapses any position where too many distinct literals
// survive — slugs, usernames, arbitrary keys — into a :param.
package cluster

import "strings"

// DefaultThreshold is the number of distinct literal siblings a tree
// position may hold before it is collapsed into :param. High enough
// that a REST API's genuine sub-resources (users, orders, settings, …)
// never merge, low enough that slug directories collapse quickly.
const DefaultThreshold = 12

// Options configure a Clusterer.
type Options struct {
	// Threshold overrides DefaultThreshold when > 0.
	Threshold int
	// Disabled turns clustering off entirely: Normalize and Route
	// return the cleaned path unchanged.
	Disabled bool
}

// Clusterer accumulates normalized paths and, once finalized, maps each
// of them to its final route pattern.
type Clusterer struct {
	opts      Options
	root      *node
	finalized bool
}

type node struct {
	children  map[string]*node
	collapsed bool // literals at this level were merged into ":param"
}

func newNode() *node { return &node{children: map[string]*node{}} }

// New returns a Clusterer ready to observe paths.
func New(opts Options) *Clusterer {
	if opts.Threshold <= 0 {
		opts.Threshold = DefaultThreshold
	}
	return &Clusterer{opts: opts, root: newNode()}
}

// Normalize cleans a raw path and applies per-segment heuristics. The
// result is the key callers should aggregate under and later pass to
// Observe and Route.
func (c *Clusterer) Normalize(path string) string {
	cleaned := Clean(path)
	if c.opts.Disabled || cleaned == "/" || !strings.HasPrefix(cleaned, "/") {
		return cleaned
	}
	segs := strings.Split(cleaned[1:], "/")
	for i, s := range segs {
		segs[i] = Classify(s)
	}
	return "/" + strings.Join(segs, "/")
}

// Observe records one normalized path in the tree. Call it once per
// distinct normalized path: cardinality is about distinct values, not
// hit counts, so feeding duplicates would only waste work.
func (c *Clusterer) Observe(normPath string) {
	if c.opts.Disabled || !strings.HasPrefix(normPath, "/") {
		return
	}
	n := c.root
	for _, seg := range splitSegments(normPath) {
		child, ok := n.children[seg]
		if !ok {
			child = newNode()
			n.children[seg] = child
		}
		n = child
	}
}

// Finalize runs the cardinality pass. After it returns, Route resolves
// normalized paths to their final patterns.
func (c *Clusterer) Finalize() {
	if !c.opts.Disabled {
		collapse(c.root, c.opts.Threshold)
	}
	c.finalized = true
}

// Route maps a previously observed normalized path to its final route.
func (c *Clusterer) Route(normPath string) string {
	if c.opts.Disabled || !c.finalized || !strings.HasPrefix(normPath, "/") {
		return normPath
	}
	segs := splitSegments(normPath)
	out := make([]string, 0, len(segs))
	n := c.root
	for _, seg := range segs {
		child, ok := n.children[seg]
		if !ok {
			if n.collapsed {
				seg = ":param"
				child = n.children[":param"]
			}
			if child == nil {
				// Unobserved path: emit remaining segments as-is.
				out = append(out, seg)
				n = newNode()
				continue
			}
		}
		out = append(out, seg)
		n = child
	}
	if len(out) == 0 {
		return "/"
	}
	return "/" + strings.Join(out, "/")
}

// collapse recursively merges literal children into ":param" wherever a
// node has more distinct literals than the threshold allows.
func collapse(n *node, threshold int) {
	literals := make([]string, 0, len(n.children))
	for name := range n.children {
		if !strings.HasPrefix(name, ":") {
			literals = append(literals, name)
		}
	}
	if len(literals) > threshold {
		merged := n.children[":param"]
		if merged == nil {
			merged = newNode()
		}
		for _, name := range literals {
			merged = mergeNodes(merged, n.children[name])
			delete(n.children, name)
		}
		n.children[":param"] = merged
		n.collapsed = true
	}
	for _, child := range n.children {
		collapse(child, threshold)
	}
}

// mergeNodes unions b into a and returns a.
func mergeNodes(a, b *node) *node {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	for name, bc := range b.children {
		a.children[name] = mergeNodes(a.children[name], bc)
	}
	return a
}

// Clean canonicalizes a raw path: ensures a leading slash, collapses
// duplicate slashes, and strips one trailing slash (except on root) so
// /users/7/ and /users/7 are the same endpoint.
func Clean(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		// Non-path targets (CONNECT host:port, "(unparsed)") pass
		// through untouched so they stay visibly separate.
		return path
	}
	var b strings.Builder
	b.Grow(len(path))
	prevSlash := false
	for i := 0; i < len(path); i++ {
		c := path[i]
		if c == '/' {
			if prevSlash {
				continue
			}
			prevSlash = true
		} else {
			prevSlash = false
		}
		b.WriteByte(c)
	}
	out := b.String()
	if len(out) > 1 && strings.HasSuffix(out, "/") {
		out = out[:len(out)-1]
	}
	return out
}

func splitSegments(p string) []string {
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

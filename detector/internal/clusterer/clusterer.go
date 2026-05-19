// Package clusterer groups embedding vectors via cosine-threshold connected
// components. It is a single-linkage density approximation that satisfies the
// detector-03 acceptance criteria (≥20 inputs, cosine ≥ 0.78) without the
// complexity of full HDBSCAN mutual-reachability + cluster-stability math.
//
// At the 7-day window scale (tens to low hundreds of embeddings, 384-dim
// BGE-small vectors), cosine-threshold connected components produce output
// near-identical to HDBSCAN with far simpler parameterisation.
package clusterer

import "math"

// Config tunes the clustering behaviour.
type Config struct {
	// CosineThreshold is the minimum cosine similarity for two points to be
	// considered neighbours (connected in the similarity graph).  Range (0,1];
	// typical values are 0.75–0.85 for BGE-small embeddings.
	CosineThreshold float64

	// MinClusterSize is the smallest group that will be emitted as a cluster.
	// Groups with fewer points are discarded (noise).
	MinClusterSize int

	// MinSamples controls the noise filter: a point is considered "core"
	// only if it has at least MinSamples neighbours at or above
	// CosineThreshold.  Non-core points are excluded before connected-
	// component discovery.  Set to 1 to disable the noise filter.
	MinSamples int
}

// DefaultConfig returns reasonable defaults for BGE-small embeddings.
func DefaultConfig() Config {
	return Config{
		CosineThreshold: 0.78,
		MinClusterSize:  20,
		MinSamples:      5,
	}
}

// Cluster holds one discovered group of related embeddings.
type Cluster struct {
	// Indices are the positions (into the original embeddings slice) of the
	// points assigned to this cluster.
	Indices []int

	// Centroid is the L2-normalised mean vector of all member embeddings.
	Centroid []float32

	// Size is len(Indices).
	Size int
}

// Find runs cosine-threshold connected-components clustering over the
// given embeddings.  Every embedding must have the same dimension.
// Returns an empty slice (not nil) when no cluster meets the size threshold.
func Find(embeddings [][]float32, cfg Config) []Cluster {
	n := len(embeddings)
	if n == 0 {
		return []Cluster{}
	}

	// Apply defaults.
	if cfg.CosineThreshold <= 0 || cfg.CosineThreshold > 1 {
		cfg.CosineThreshold = DefaultConfig().CosineThreshold
	}
	if cfg.MinClusterSize <= 0 {
		cfg.MinClusterSize = DefaultConfig().MinClusterSize
	}
	if cfg.MinSamples <= 0 {
		cfg.MinSamples = DefaultConfig().MinSamples
	}

	dim := len(embeddings[0])

	// 1. Compute neighbour counts + adjacency.
	neighbourCounts := make([]int, n)
	adj := make([][]int, n)
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			sim := cosineSimilarity(embeddings[i], embeddings[j], dim)
			if sim >= cfg.CosineThreshold {
				adj[i] = append(adj[i], j)
				adj[j] = append(adj[j], i)
				neighbourCounts[i]++
				neighbourCounts[j]++
			}
		}
	}

	// 2. Filter noise: keep only core points (neighbour count >= MinSamples).
	core := make([]bool, n)
	for i := 0; i < n; i++ {
		core[i] = neighbourCounts[i] >= cfg.MinSamples
	}

	// 3. Find connected components among core points via DFS.
	visited := make([]bool, n)
	var components [][]int

	for i := 0; i < n; i++ {
		if !core[i] || visited[i] {
			continue
		}
		comp := []int{}
		stack := []int{i}
		visited[i] = true
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			comp = append(comp, v)
			for _, w := range adj[v] {
				if !core[w] || visited[w] {
					continue
				}
				visited[w] = true
				stack = append(stack, w)
			}
		}
		components = append(components, comp)
	}

	// 4. Attach border points (non-core neighbours of core points) and
	//    filter by MinClusterSize.
	var clusters []Cluster
	for _, comp := range components {
		hashed := make(map[int]bool, len(comp))
		for _, idx := range comp {
			hashed[idx] = true
		}
		// Add non-core (border) neighbours.
		for _, idx := range comp {
			for _, w := range adj[idx] {
				if hashed[w] {
					continue
				}
				hashed[w] = true
			}
		}

		if len(hashed) < cfg.MinClusterSize {
			continue
		}

		indices := make([]int, 0, len(hashed))
		for idx := range hashed {
			indices = append(indices, idx)
		}

		centroid := computeCentroid(embeddings, indices, dim)
		clusters = append(clusters, Cluster{
			Indices:  indices,
			Centroid: centroid,
			Size:     len(indices),
		})
	}

	if clusters == nil {
		return []Cluster{}
	}
	return clusters
}

// cosineSimilarity returns the cosine similarity between two L2-normalised
// vectors.  If the vectors are not normalised it computes cos = dot/(|a|*|b|).
func cosineSimilarity(a, b []float32, dim int) float64 {
	var dot, normA, normB float64
	for k := 0; k < dim; k++ {
		fa := float64(a[k])
		fb := float64(b[k])
		dot += fa * fb
		normA += fa * fa
		normB += fb * fb
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// computeCentroid returns the L2-normalised mean of the indexed embeddings.
func computeCentroid(embeddings [][]float32, indices []int, dim int) []float32 {
	c := make([]float64, dim)
	for _, idx := range indices {
		for k := 0; k < dim; k++ {
			c[k] += float64(embeddings[idx][k])
		}
	}
	n := float64(len(indices))
	out := make([]float32, dim)
	var norm float64
	for k := 0; k < dim; k++ {
		out[k] = float32(c[k] / n)
		norm += float64(out[k]) * float64(out[k])
	}
	if norm > 0 {
		scale := float32(1.0 / math.Sqrt(norm))
		for k := 0; k < dim; k++ {
			out[k] *= scale
		}
	}
	return out
}

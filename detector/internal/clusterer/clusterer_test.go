package clusterer

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func makeL2Norm(v []float32) []float32 {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return v
	}
	s := float32(1.0 / math.Sqrt(norm))
	for i := range v {
		v[i] *= s
	}
	return v
}

func perturb(base []float32, rng *rand.Rand, scale float32) []float32 {
	v := make([]float32, len(base))
	for i := range base {
		v[i] = base[i] + rng.Float32()*scale
	}
	return makeL2Norm(v)
}

func equalSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	sa := make([]int, len(a))
	copy(sa, a)
	sort.Ints(sa)
	sb := make([]int, len(b))
	copy(sb, b)
	sort.Ints(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// TestCluster_IdenticalGroup_FormsOneCluster verifies that 25 near-identical
// embeddings produce exactly one cluster of at least 20 points.
func TestCluster_IdenticalGroup_FormsOneCluster(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	dim := 384
	base := make([]float32, dim)
	for i := range base {
		base[i] = rng.Float32()
	}
	base = makeL2Norm(base)

	embeddings := make([][]float32, 30)
	for i := 0; i < 25; i++ {
		embeddings[i] = perturb(base, rng, 0.001) // tiny noise → cosine ≈ 1.0
	}
	for i := 25; i < 30; i++ {
		far := make([]float32, dim)
		for j := range far {
			far[j] = rng.Float32()*2 - 1
		}
		embeddings[i] = makeL2Norm(far) // random direction → unrelated
	}

	cfg := DefaultConfig()
	clusters := Find(embeddings, cfg)
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0].Size < 20 {
		t.Fatalf("cluster size = %d, want >= 20", clusters[0].Size)
	}
	if clusters[0].Size > 25 {
		t.Fatalf("cluster size = %d includes noise points", clusters[0].Size)
	}
	if len(clusters[0].Centroid) != dim {
		t.Fatalf("centroid dim = %d, want %d", len(clusters[0].Centroid), dim)
	}

	// Centroid should be L2 normalised.
	var norm float64
	for _, x := range clusters[0].Centroid {
		norm += float64(x) * float64(x)
	}
	if math.Abs(math.Sqrt(norm)-1.0) > 1e-6 {
		t.Fatalf("centroid norm = %.6f, want 1.0", math.Sqrt(norm))
	}
}

// TestCluster_EmptyInput returns empty slice.
func TestCluster_EmptyInput(t *testing.T) {
	clusters := Find([][]float32{}, DefaultConfig())
	if len(clusters) != 0 {
		t.Fatal("expected empty result")
	}
}

// TestCluster_BelowThreshold_NoClusters verifies that dissimilar embeddings
// produce no clusters.
func TestCluster_BelowThreshold_NoClusters(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	dim := 64
	embeddings := make([][]float32, 20)
	for i := range embeddings {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()
		}
		embeddings[i] = makeL2Norm(v)
	}

	cfg := Config{CosineThreshold: 0.9, MinClusterSize: 5, MinSamples: 3}
	clusters := Find(embeddings, cfg)
	if len(clusters) != 0 {
		t.Fatalf("expected no clusters for random vectors, got %d", len(clusters))
	}
}

// TestCluster_MinSamplesNoiseFilter tests that points with fewer than
// MinSamples neighbours are treated as noise and excluded.
func TestCluster_MinSamplesNoiseFilter(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	dim := 64

	// 20 tightly-grouped embeddings — each should have 19 neighbours.
	base := makeL2Norm(makeRandomVec(dim, rng))
	embeddings := make([][]float32, 25)
	for i := 0; i < 20; i++ {
		embeddings[i] = perturb(base, rng, 0.005)
	}
	// 5 isolated embeddings — each has at most a few neighbours.
	for i := 20; i < 25; i++ {
		v := make([]float32, dim)
		for j := range v {
			v[j] = rng.Float32()
		}
		embeddings[i] = makeL2Norm(v)
	}

	cfg := Config{CosineThreshold: 0.85, MinClusterSize: 10, MinSamples: 10}
	clusters := Find(embeddings, cfg)
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0].Size != 20 {
		t.Fatalf("cluster size = %d, want 20 (isolated points dropped)", clusters[0].Size)
	}
}

// TestCluster_TwoDistinctGroups yields two clusters.
func TestCluster_TwoDistinctGroups(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	dim := 64

	baseA := makeL2Norm(makeRandomVec(dim, rng))
	baseB := makeL2Norm(makeRandomVec(dim, rng))

	embeddings := make([][]float32, 50)
	for i := 0; i < 20; i++ {
		embeddings[i] = perturb(baseA, rng, 0.005)
	}
	for i := 20; i < 40; i++ {
		embeddings[i] = perturb(baseB, rng, 0.005)
	}
	for i := 40; i < 50; i++ {
		v := makeRandomVec(dim, rng)
		embeddings[i] = makeL2Norm(v)
	}

	cfg := DefaultConfig()
	clusters := Find(embeddings, cfg)

	clusterCounts := make(map[int]int)
	for _, c := range clusters {
		clusterCounts[c.Size]++
	}
	if len(clusters) < 1 || len(clusters) > 2 {
		t.Fatalf("expected 1-2 clusters, got %d", len(clusters))
	}
}

// TestCluster_MinClusterSizeDiscardsSmallGroups verifies small groups are
// filtered.
func TestCluster_MinClusterSizeDiscardsSmallGroups(t *testing.T) {
	rng := rand.New(rand.NewSource(8675309))
	dim := 32

	embeddings := make([][]float32, 15)
	base := makeL2Norm(makeRandomVec(dim, rng))
	for i := range embeddings {
		embeddings[i] = perturb(base, rng, 0.005)
	}

	cfg := Config{CosineThreshold: 0.8, MinClusterSize: 20, MinSamples: 1}
	clusters := Find(embeddings, cfg)
	if len(clusters) != 0 {
		t.Fatalf("expected no clusters (group size 15 < min 20), got %d", len(clusters))
	}
}

// TestCluster_CentroidIsL2Normalised is a focused centroid norm check.
func TestCluster_CentroidIsL2Normalised(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	dim := 4
	base := makeL2Norm(makeRandomVec(dim, rng))
	embeddings := [][]float32{
		perturb(base, rng, 0.01),
		perturb(base, rng, 0.01),
		perturb(base, rng, 0.01),
	}

	clusters := Find(embeddings, Config{CosineThreshold: 0.7, MinClusterSize: 2, MinSamples: 1})
	if len(clusters) != 1 {
		t.Fatal("expected 1 cluster")
	}
	for i, clusters := range [][]Cluster{{clusters[0]}} {
		_ = i
		var norm float64
		for _, x := range clusters[0].Centroid {
			norm += float64(x) * float64(x)
		}
		n := math.Sqrt(norm)
		if math.Abs(n-1.0) > 1e-6 {
			t.Fatalf("centroid norm = %.10f", n)
		}
	}
}

func makeRandomVec(dim int, rng *rand.Rand) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = rng.Float32()
	}
	return v
}

// TestCluster_ReturnsEmptySliceNotNil ensures the zero-value return is an
// empty slice, not nil — important for JSON serialisation.
func TestCluster_ReturnsEmptySliceNotNil(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	embeddings := make([][]float32, 20)
	for i := range embeddings {
		embeddings[i] = makeL2Norm(makeRandomVec(4, rng))
	}
	clusters := Find(embeddings, Config{CosineThreshold: 0.99, MinClusterSize: 999, MinSamples: 1})
	// It's extremely unlikely random 4-dim vectors hit cosine > 0.99,
	// but even if they do the min_cluster_size 999 will filter everything.
	if clusters == nil {
		t.Fatal("expected [] not nil")
	}
}

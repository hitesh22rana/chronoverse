package countminsketch

import (
	"math"
	"sync"
)

// CountMinSketch is the main structure that holds the sketch data.
// It contains parameters for error rate (epsilon), confidence (delta), and the dimensions of the sketch (width and depth).
type CountMinSketch struct {
	epsilon float64    // Error rate (relative error)
	delta   float64    // Confidence parameter (probability of error)
	width   uint32     // Number of buckets per hash function
	depth   uint32     // Number of hash functions
	counts  [][]uint64 // 2D slice to hold counts for each hash function
	mu      sync.Mutex // Mutex to protect concurrent access
}

// NewCountMinSketch initializes a new CountMinSketch with the given epsilon and delta.
// It calculates the width and depth based on the provided parameters.
// The width is calculated as ceil(e / epsilon) where e is Euler's number (~2.718)
// The depth is calculated as ceil(ln(1/delta)).
// The counts slice is a 2D slice where each row corresponds to a hash function and
// each column corresponds to a bucket for that hash function.
func NewCountMinSketch(epsilon, delta float64) *CountMinSketch {
	width := uint32(math.Ceil(math.E / epsilon))
	depth := uint32(math.Ceil(math.Log(1.0 / delta)))

	// 2D slice to hold counts for each hash function
	counts := make([][]uint64, depth)
	for i := range counts {
		counts[i] = make([]uint64, width)
	}

	return &CountMinSketch{
		epsilon: epsilon,
		delta:   delta,
		width:   width,
		depth:   depth,
		counts:  counts,
	}
}

// Add increments the count for the given item in the sketch.
func (cms *CountMinSketch) Add(item string) {
	cms.mu.Lock()
	defer cms.mu.Unlock()

	for i := uint32(0); i < cms.depth; i++ {
		hashValue := hash(item, i) % cms.width
		cms.counts[i][hashValue]++
	}
}

// Estimate returns the estimated count for the given item.
func (cms *CountMinSketch) Estimate(item string) uint64 {
	cms.mu.Lock()
	defer cms.mu.Unlock()

	var estimate uint64 = math.MaxUint64 // Initialize to max uint64 value
	for i := uint32(0); i < cms.depth; i++ {
		hashValue := hash(item, i) % cms.width
		estimate = min(estimate, cms.counts[i][hashValue])
	}
	return estimate
}

// Reset clears the counts in the sketch, allowing it to be reused without creating a new instance.
// This is useful for scenarios where the sketch needs to be reused for different datasets.
func (cms *CountMinSketch) Reset() {
	cms.mu.Lock()
	defer cms.mu.Unlock()

	for i := range cms.counts {
		for j := range cms.counts[i] {
			cms.counts[i][j] = 0
		}
	}
}

// Copy creates a deep copy of the CountMinSketch instance.
// This is useful for scenarios where you want to preserve the state of the sketch.
func (cms *CountMinSketch) Copy() *CountMinSketch {
	cms.mu.Lock()
	defer cms.mu.Unlock()

	// Create a new CountMinSketch with the same parameters
	sketchCopy := NewCountMinSketch(cms.epsilon, cms.delta)

	// Copy the counts from the original sketch
	for i := range cms.counts {
		sketchCopy.counts[i] = make([]uint64, len(cms.counts[i]))
		copy(sketchCopy.counts[i], cms.counts[i])
	}

	return sketchCopy
}

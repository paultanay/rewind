package prometheus

import (
	"math"
	"sort"
	"time"

	"github.com/rewind-io/rewind/internal/model"
)

const maxPoints = 500

// downsample reduces pts to at most maxPoints samples using Largest-Triangle-
// Three-Buckets (LTTB) — a perceptually accurate downsampling algorithm that
// preserves the visual shape of the series, including spikes and step changes.
//
// If len(pts) ≤ maxPoints the input is returned unchanged.
//
// Reference: Sveinn Steinarsson, "Downsampling Time Series for Visual
// Representation", University of Iceland, 2013.
func downsample(pts []model.Point) []model.Point {
	if len(pts) <= maxPoints {
		return pts
	}

	// Sort by time first (defensive).
	sort.Slice(pts, func(i, j int) bool {
		return pts[i].T.Before(pts[j].T)
	})

	out := make([]model.Point, 0, maxPoints)
	// Always keep first and last.
	out = append(out, pts[0])

	bucketSize := float64(len(pts)-2) / float64(maxPoints-2)
	a := 0 // index of last selected point

	for i := 0; i < maxPoints-2; i++ {
		// Calculate the range for next bucket.
		bucketStart := int(math.Floor(float64(i+1)*bucketSize)) + 1
		bucketEnd := int(math.Floor(float64(i+2)*bucketSize)) + 1
		if bucketEnd >= len(pts) {
			bucketEnd = len(pts) - 1
		}

		// Calculate average point in the next bucket (for triangle area).
		var avgX, avgY float64
		count := float64(bucketEnd - bucketStart)
		for j := bucketStart; j < bucketEnd; j++ {
			avgX += float64(pts[j].T.UnixNano())
			avgY += pts[j].V
		}
		avgX /= count
		avgY /= count

		// Find the point in the current bucket that forms the largest triangle.
		curBucketStart := int(math.Floor(float64(i)*bucketSize)) + 1
		curBucketEnd := bucketStart

		maxArea := -1.0
		maxIdx := curBucketStart

		ax := float64(pts[a].T.UnixNano())
		ay := pts[a].V

		for j := curBucketStart; j < curBucketEnd; j++ {
			bx := float64(pts[j].T.UnixNano())
			by := pts[j].V
			// Triangle area = 0.5 * |ax*(by-avgY) + bx*(avgY-ay) + avgX*(ay-by)|
			area := math.Abs(ax*(by-avgY)+bx*(avgY-ay)+avgX*(ay-by)) * 0.5
			if area > maxArea {
				maxArea = area
				maxIdx = j
			}
		}

		out = append(out, pts[maxIdx])
		a = maxIdx
	}

	out = append(out, pts[len(pts)-1])
	return out
}

// chooseStep selects a Prometheus query step so that the resulting series has
// at most maxPoints samples in the given window.
func chooseStep(window model.TimeRange) time.Duration {
	durationSec := window.Duration().Seconds()
	// Minimum step: 15s (Prometheus default scrape interval).
	stepSec := math.Ceil(durationSec / maxPoints)
	if stepSec < 15 {
		stepSec = 15
	}
	return time.Duration(stepSec) * time.Second
}

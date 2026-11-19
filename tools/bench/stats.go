// Why: a ratio of two medians means nothing without its uncertainty, so
// every published comparison carries an interval and a three-way verdict.
//
// What: summary and summarize (a cell's statistics), median, ratioCI and
// pairedCI (bootstrap intervals), classify, and the noise threshold noisyRSD.
//
// How: a percentile bootstrap with a fixed seed and resample count, so the
// same raw times always give the same interval.

package main

import (
	"math"
	"math/rand/v2"
	"slices"
)

// bootstrapResamples and bootstrapSeed fix the bootstrap's resampling, so the
// same raw times always give the same interval.
const (
	bootstrapResamples = 10000
	bootstrapSeed      = 0x6d6b62656e6368 // "mkbench"
)

// noisyRSD is the noise threshold: a cell whose relative standard deviation is
// above it is rerun once and then marked noisy.
const noisyRSD = 0.03

// summary is what RESULTS.md prints for one cell. Times are seconds.
type summary struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	MAD    float64 `json:"mad"` // median absolute deviation from the median
	Mean   float64 `json:"mean"`
	RSD    float64 `json:"rsd"` // standard deviation over mean
}

func summarize(xs []float64) summary {
	if len(xs) == 0 {
		return summary{}
	}
	s := summary{N: len(xs), Median: median(xs), Min: slices.Min(xs), Max: slices.Max(xs)}
	dev := make([]float64, len(xs))
	var sum float64
	for i, x := range xs {
		dev[i] = math.Abs(x - s.Median)
		sum += x
	}
	s.MAD = median(dev)
	s.Mean = sum / float64(len(xs))
	if len(xs) > 1 && s.Mean > 0 {
		var ss float64
		for _, x := range xs {
			ss += (x - s.Mean) * (x - s.Mean)
		}
		s.RSD = math.Sqrt(ss/float64(len(xs)-1)) / s.Mean
	}
	return s
}

// median of xs; xs is not changed. The median of an even count is the mean of
// the two middle values.
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	c := slices.Clone(xs)
	slices.Sort(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

// interval is a two-sided 95 percent interval.
type interval struct {
	Lo float64 `json:"lo"`
	Hi float64 `json:"hi"`
}

// ratioCI is the bootstrap interval for median(num)/median(den): both samples are
// resampled with replacement, independently, bootstrapResamples times, and
// the 2.5th and 97.5th percentiles of the resampled ratios are the bounds.
func ratioCI(num, den []float64) interval {
	if len(num) == 0 || len(den) == 0 {
		return interval{math.NaN(), math.NaN()}
	}
	r := rand.New(rand.NewPCG(bootstrapSeed, uint64(len(num))<<32|uint64(len(den))))
	a := make([]float64, len(num))
	b := make([]float64, len(den))
	ratios := make([]float64, bootstrapResamples)
	for k := range ratios {
		for i := range a {
			a[i] = num[r.IntN(len(num))]
		}
		for i := range b {
			b[i] = den[r.IntN(len(den))]
		}
		ratios[k] = median(a) / median(b)
	}
	return percentiles(ratios)
}

// pairedCI is the interval for the median of paired ratios (compare mode):
// the pairs are resampled together.
func pairedCI(ratios []float64) interval {
	if len(ratios) == 0 {
		return interval{math.NaN(), math.NaN()}
	}
	r := rand.New(rand.NewPCG(bootstrapSeed, uint64(len(ratios))))
	a := make([]float64, len(ratios))
	meds := make([]float64, bootstrapResamples)
	for k := range meds {
		for i := range a {
			a[i] = ratios[r.IntN(len(ratios))]
		}
		meds[k] = median(a)
	}
	return percentiles(meds)
}

func percentiles(xs []float64) interval {
	slices.Sort(xs)
	at := func(p float64) float64 {
		i := int(p * float64(len(xs)-1))
		return xs[i]
	}
	return interval{at(0.025), at(0.975)}
}

// classify is the verdict rule against a threshold t on a ratio: "over t" when the
// whole interval is above t, "within t" when it is below, else inconclusive.
func classify(ci interval, t float64) string {
	switch {
	case ci.Lo > t:
		return "over"
	case ci.Hi < t:
		return "within"
	default:
		return "inconclusive"
	}
}

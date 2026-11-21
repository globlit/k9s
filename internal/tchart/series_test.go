package tchart_test

import (
	"testing"
	"time"

	"github.com/derailed/k9s/internal/tchart"
	"github.com/stretchr/testify/assert"
)

func TestSeriesAdd(t *testing.T) {
	type tuple struct {
		time.Time
		float64
	}
	uu := map[string]struct {
		tt []tuple
		e  int
	}{
		"one": {
			tt: []tuple{
				{time.Now(), 1000},
			},
			e: 6,
		},
	}

	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			ss := makeSeries()
			for _, tu := range u.tt {
				ss.Add(tu.Time, tu.float64)
			}
			assert.Len(t, ss, u.e)
		})
	}
}

func TestSeriesTruncate(t *testing.T) {
	uu := map[string]struct {
		n, e int
	}{
		"one": {
			n: 1,
			e: 1,
		},
		"some": {
			n: 3,
			e: 3,
		},
		"none": {
			n: 0,
			e: 0,
		},
		"more-than-len": {
			n: 10,
			e: 5,
		},
	}

	for k := range uu {
		u := uu[k]
		t.Run(k, func(t *testing.T) {
			ss := makeSeries()
			ss.Truncate(u.n)
			assert.Len(t, ss, u.e)
		})
	}
}

// Helpers...

func makeSeries() tchart.MetricSeries {
	return tchart.MetricSeries{
		time.Now(): -100,
		time.Now(): 0,
		time.Now(): 100,
		time.Now(): 50,
		time.Now(): 10,
	}
}

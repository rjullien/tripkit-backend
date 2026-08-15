package discovery

import (
	"math"
	"sort"
)

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0
	φ1 := lat1 * math.Pi / 180
	φ2 := lat2 * math.Pi / 180
	dφ := (lat2 - lat1) * math.Pi / 180
	dλ := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return r * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func rankItems(items []Item) []Item {
	out := append([]Item(nil), items...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].DistKm < out[j].DistKm
	})
	return out
}

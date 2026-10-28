package segeval

import (
	"math"
	"slices"
)

type pair struct {
	g, h int
	iou  float64
}

// assign maximizes weights for a rectangular matrix with zero-weight dummy nodes.
func assign(w [][]float64) []pair {
	n := len(w)
	if n == 0 || len(w[0]) == 0 {
		return nil
	}
	m := len(w[0])
	size := max(n, m)
	u, v := make([]float64, size+1), make([]float64, size+1)
	p, way := make([]int, size+1), make([]int, size+1)
	cost := func(i, j int) float64 {
		if i < n && j < m {
			return -w[i][j]
		}
		return 0
	}
	for i := 1; i <= size; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, size+1)
		used := make([]bool, size+1)
		for j := 1; j <= size; j++ {
			minv[j] = math.Inf(1)
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := math.Inf(1)
			j1 := 0
			for j := 1; j <= size; j++ {
				if used[j] {
					continue
				}
				cur := cost(i0-1, j-1) - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= size; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	var out []pair
	for j := 1; j <= size; j++ {
		i := p[j] - 1
		if i < n && j <= m && w[i][j-1] > 0 {
			out = append(out, pair{i, j - 1, w[i][j-1]})
		}
	}
	return out
}
func componentMatch(ng, nh int, edges []pair) []pair {
	adj := make([][]int, ng+nh)
	for _, e := range edges {
		adj[e.g] = append(adj[e.g], ng+e.h)
		adj[ng+e.h] = append(adj[ng+e.h], e.g)
	}
	seen := make([]bool, ng+nh)
	var out []pair
	for start := range adj {
		if seen[start] || len(adj[start]) == 0 {
			continue
		}
		queue := []int{start}
		seen[start] = true
		var gs, hs []int
		for len(queue) > 0 {
			x := queue[0]
			queue = queue[1:]
			if x < ng {
				gs = append(gs, x)
			} else {
				hs = append(hs, x-ng)
			}
			for _, y := range adj[x] {
				if !seen[y] {
					seen[y] = true
					queue = append(queue, y)
				}
			}
		}
		slices.Sort(gs)
		slices.Sort(hs)
		gi, hi := map[int]int{}, map[int]int{}
		for i, g := range gs {
			gi[g] = i
		}
		for i, h := range hs {
			hi[h] = i
		}
		w := make([][]float64, len(gs))
		for i := range w {
			w[i] = make([]float64, len(hs))
		}
		for _, e := range edges {
			if i, ok := gi[e.g]; ok {
				if j, ok := hi[e.h]; ok {
					w[i][j] = e.iou
				}
			}
		}
		for _, p := range assign(w) {
			out = append(out, pair{gs[p.g], hs[p.h], p.iou})
		}
	}
	return out
}
func orderScore(p []pair) float64 {
	if len(p) < 2 {
		return 1
	}
	p = slices.Clone(p)
	slices.SortFunc(p, func(a, b pair) int { return a.g - b.g })
	v := make([]int, len(p))
	for i, e := range p {
		v[i] = e.h
	}
	var inversions func([]int) int64
	inversions = func(v []int) int64 {
		if len(v) < 2 {
			return 0
		}
		mid := len(v) / 2
		n := inversions(v[:mid]) + inversions(v[mid:])
		tmp := make([]int, 0, len(v))
		i, j := 0, mid
		for i < mid && j < len(v) {
			if v[i] <= v[j] {
				tmp = append(tmp, v[i])
				i++
			} else {
				tmp = append(tmp, v[j])
				j++
				n += int64(mid - i)
			}
		}
		tmp = append(tmp, v[i:mid]...)
		tmp = append(tmp, v[j:]...)
		copy(v, tmp)
		return n
	}
	inv := inversions(v)
	return 1 - float64(inv)/float64(int64(len(p))*int64(len(p)-1)/2)
}

// Package chart renders value curves: braille for the terminal, SVG for the
// web. Pure string builders, no I/O.
package chart

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"finador/internal/perf"
)

// Braille draws points as a width×height cell chart (each braille cell holds
// 2×4 dots), with min/max labels and the date range underneath.
func Braille(points []perf.Point, width, height int) string {
	if len(points) == 0 {
		return ""
	}
	lo, hi := bounds(points)
	if hi == lo {
		hi = lo + 1 // série plate : on lui donne de l'épaisseur
	}
	cols, rows := width*2, height*4
	grid := make([][]bool, rows)
	for i := range grid {
		grid[i] = make([]bool, cols)
	}
	// y en dots, 0 = haut
	yOf := func(v float64) int {
		y := int(math.Round((hi - v) / (hi - lo) * float64(rows-1)))
		return min(max(y, 0), rows-1)
	}
	prevY := -1
	for x := range cols {
		idx := x * (len(points) - 1) / max(cols-1, 1)
		y := yOf(points[idx].Value)
		grid[y][x] = true
		if prevY >= 0 { // relie les colonnes pour les pentes raides
			for yy := min(prevY, y); yy <= max(prevY, y); yy++ {
				grid[yy][x] = true
			}
		}
		prevY = y
	}

	labels := []string{formatCompact(hi)}
	for range height - 2 {
		labels = append(labels, "")
	}
	if height > 1 {
		labels = append(labels, formatCompact(lo))
	}
	labelW := 0
	for _, l := range labels {
		labelW = max(labelW, len(l))
	}

	var b strings.Builder
	for row := range height {
		fmt.Fprintf(&b, "%*s ", labelW, labels[row])
		for col := range width {
			var bits rune
			for dy := range 4 {
				for dx := range 2 {
					if grid[row*4+dy][col*2+dx] {
						bits |= brailleBit(dx, dy)
					}
				}
			}
			b.WriteRune(0x2800 + bits)
		}
		b.WriteByte('\n')
	}
	from, to := points[0].Date.String(), points[len(points)-1].Date.String()
	gap := max(width-len(from)-len(to), 1)
	fmt.Fprintf(&b, "%*s %s%s%s\n", labelW, "", from, strings.Repeat(" ", gap), to)
	return b.String()
}

// brailleBit maps a (dx, dy) dot to its bit in the braille block.
func brailleBit(dx, dy int) rune {
	bits := [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}
	return bits[dy][dx]
}

func bounds(points []perf.Point) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, p := range points {
		lo, hi = math.Min(lo, p.Value), math.Max(hi, p.Value)
	}
	return lo, hi
}

// formatCompact shortens big numbers for axis labels: 473.9k, 1.23M.
func formatCompact(v float64) string {
	a := math.Abs(v)
	switch {
	case a >= 1e6:
		return trimZero(strconv.FormatFloat(v/1e6, 'f', 2, 64)) + "M"
	case a >= 1e3:
		return trimZero(strconv.FormatFloat(v/1e3, 'f', 1, 64)) + "k"
	default:
		return strconv.FormatFloat(v, 'f', 2, 64)
	}
}

func trimZero(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

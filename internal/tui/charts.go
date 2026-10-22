package tui

import (
	"fmt"
	"math"
	"strings"
)

type segment struct {
	label string
	value int64
	color string
}

func allocation(parts []segment, width int) string {
	var total int64
	for _, p := range parts {
		total += max(0, p.value)
	}
	if total == 0 {
		return ink(strings.Repeat("─", max(1, width)), "#656B80")
	}
	var b strings.Builder
	used := 0
	var sum int64
	for _, p := range parts {
		sum += max(0, p.value)
		end := int(float64(sum) / float64(total) * float64(width))
		b.WriteString(ink(strings.Repeat("█", max(0, end-used)), p.color))
		used = end
	}
	return b.String()
}
func breakdown(parts []segment, w int) string {
	lines := []string{allocation(parts, w)}
	for _, p := range parts {
		lines = append(lines, pair(ink("■ "+p.label, p.color), number(p.value), w))
	}
	return strings.Join(lines, "\n")
}

// Each Braille cell is a 2x4 dot canvas. Charts scale to allocated cells, not pixels.
var dotBits = [4][2]rune{{1, 8}, {2, 16}, {4, 32}, {64, 128}}

func donut(parts []segment, w, h int) string {
	var total int64
	for _, p := range parts {
		total += p.value
	}
	var lines []string
	for cy := 0; cy < h; cy++ {
		var line strings.Builder
		for cx := 0; cx < w; cx++ {
			bits := rune(0)
			ci := 0
			for dy := 0; dy < 4; dy++ {
				for dx := 0; dx < 2; dx++ {
					x := (float64(cx*2+dx)+.5)/float64(w*2)*2 - 1
					y := (float64(cy*4+dy)+.5)/float64(h*4)*2 - 1
					r := math.Hypot(x, y)
					if r < .57 || r > .97 {
						continue
					}
					bits |= dotBits[dy][dx]
					angle := math.Mod(math.Atan2(y, x)+math.Pi/2+2*math.Pi, 2*math.Pi) / (2 * math.Pi)
					var sum int64
					for i, p := range parts {
						sum += p.value
						if total > 0 && angle < float64(sum)/float64(total) {
							ci = i
							break
						}
					}
				}
			}
			color := "#656B80"
			if total > 0 && len(parts) > 0 {
				color = parts[ci].color
			}
			if bits == 0 {
				line.WriteByte(' ')
			} else {
				line.WriteString(ink(string(0x2800+bits), color))
			}
		}
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\n")
}
func lineChart(series [][]int64, colors []string, w, h int) string {
	w = max(1, w)
	h = max(1, h)
	pw, ph := w*2, h*4
	dots := make([][]rune, h)
	owner := make([][]int, h)
	for y := range dots {
		dots[y] = make([]rune, w)
		owner[y] = make([]int, w)
	}
	var peak int64
	for _, s := range series {
		for _, v := range s {
			peak = max(peak, v)
		}
	}
	put := func(x, y, si int) {
		if x >= 0 && x < pw && y >= 0 && y < ph {
			dots[y/4][x/2] |= dotBits[y%4][x%2]
			owner[y/4][x/2] = si
		}
	}
	for si, s := range series {
		if len(s) == 0 {
			continue
		}
		last := -1
		for x := 0; x < pw; x++ {
			pos := float64(x) * float64(len(s)-1) / float64(max(1, pw-1))
			lo := int(pos)
			hi := min(lo+1, len(s)-1)
			v := float64(s[lo]) + (float64(s[hi])-float64(s[lo]))*(pos-float64(lo))
			y := ph - 1
			if peak > 0 {
				y -= int(v / float64(peak) * float64(ph-1))
			}
			put(x, y, si)
			if last >= 0 {
				for yy := min(last, y); yy <= max(last, y); yy++ {
					put(x, yy, si)
				}
			}
			last = y
		}
	}
	lines := make([]string, h)
	for y := range dots {
		var b strings.Builder
		for x, v := range dots[y] {
			if v == 0 {
				b.WriteByte(' ')
			} else {
				b.WriteString(ink(string(0x2800+v), colors[owner[y][x]%len(colors)]))
			}
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}
func columnChart(values []int64, w, h int, color string) string {
	w = max(1, w)
	h = max(1, h)
	bins := make([]int64, w)
	if len(values) <= w && len(values) > 0 {
		for i := range bins {
			bins[i] = values[min(len(values)-1, i*len(values)/w)]
		}
	} else {
		for i, v := range values {
			bins[min(w-1, i*w/max(1, len(values)))] += v
		}
	}
	var peak int64
	for _, v := range bins {
		peak = max(peak, v)
	}
	glyph := []rune(" ▁▂▃▄▅▆▇█")
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for _, v := range bins {
			units := 0
			if peak > 0 {
				units = int(math.Ceil(float64(v) / float64(peak) * float64(h*8)))
			}
			b.WriteRune(glyph[max(0, min(8, units-(h-1-y)*8))])
		}
		rows[y] = ink(b.String(), color)
	}
	return strings.Join(rows, "\n")
}
func compact(n int64) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return number(n)
	}
}

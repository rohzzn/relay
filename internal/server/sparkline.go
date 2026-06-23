package server

import (
	"fmt"
	"strings"

	"github.com/rohzzn/relay/internal/db"
)

// sparklineSVG generates a minimal SVG line chart from recent check latencies.
// Width=120, Height=32. Returns empty string if fewer than 2 data points.
func sparklineSVG(checks []*db.Check, color string) string {
	// Collect valid latency values (up checks only).
	var vals []int64
	for _, c := range checks {
		if c.Status == "up" && c.LatencyMs.Valid {
			vals = append(vals, c.LatencyMs.Int64)
		}
	}
	if len(vals) < 2 {
		return ""
	}

	const (
		w  = 120
		h  = 32
		pad = 2
	)

	// Find min/max for scaling.
	min, max := vals[0], vals[0]
	for _, v := range vals {
		if v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	if max == min {
		max = min + 1
	}

	// Build SVG polyline points.
	points := make([]string, len(vals))
	for i, v := range vals {
		x := float64(pad) + float64(i)*(float64(w-2*pad)/float64(len(vals)-1))
		y := float64(h-pad) - float64(v-min)/float64(max-min)*float64(h-2*pad)
		points[i] = fmt.Sprintf("%.1f,%.1f", x, y)
	}

	if color == "" {
		color = "#6366f1"
	}

	return fmt.Sprintf(
		`<svg width="%d" height="%d" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg" style="overflow:visible">
  <polyline points="%s" fill="none" stroke="%s" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`,
		w, h, w, h,
		strings.Join(points, " "),
		color,
	)
}

// sparklineColor returns the appropriate color for a monitor's current status.
func sparklineColor(status string) string {
	switch status {
	case "up":
		return "#10b981"
	case "down":
		return "#ef4444"
	case "degraded":
		return "#f59e0b"
	default:
		return "#94a3b8"
	}
}

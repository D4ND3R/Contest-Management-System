package webkit

import (
	"html/template"
	"sort"
)

// iconPaths are the interface icons: 24×24 strokes drawn for this project,
// emitted inline (no requests, nothing for the CSP to allow) and coloured
// by the surrounding text.
var iconPaths = map[string]string{
	"logo":          `<path d="M8 4H6.5A2.5 2.5 0 0 0 4 6.5V10l-2 2 2 2v3.5A2.5 2.5 0 0 0 6.5 20H8M16 4h1.5A2.5 2.5 0 0 1 20 6.5V10l2 2-2 2v3.5a2.5 2.5 0 0 1-2.5 2.5H16"/><path d="M8.5 12.2l2.4 2.4 4.6-5.1"/>`,
	"dashboard":     `<rect x="3" y="3" width="7.5" height="8.5" rx="1.5"/><rect x="13.5" y="3" width="7.5" height="5.5" rx="1.5"/><rect x="13.5" y="11.5" width="7.5" height="9.5" rx="1.5"/><rect x="3" y="14.5" width="7.5" height="6.5" rx="1.5"/>`,
	"home":          `<path d="M3 11l9-7 9 7"/><path d="M5.5 9.5V20h4.5v-6h4v6h4.5V9.5"/>`,
	"file":          `<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5M9 13h6M9 17h6"/>`,
	"send":          `<path d="M21 3L10.5 13.5"/><path d="M21 3l-6.5 18-4-7.5L3 9.5z"/>`,
	"trophy":        `<path d="M7 4h10v5a5 5 0 0 1-10 0z"/><path d="M7 6H4v1a3 3 0 0 0 3 3M17 6h3v1a3 3 0 0 1-3 3M12 14v4M8 21h8M9 18h6"/>`,
	"calendar":      `<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M3 10h18M8 3v4M16 3v4"/>`,
	"cpu":           `<rect x="6" y="6" width="12" height="12" rx="2"/><rect x="9.5" y="9.5" width="5" height="5" rx=".5"/><path d="M9 2.5V6M15 2.5V6M9 18v3.5M15 18v3.5M2.5 9H6M2.5 15H6M18 9h3.5M18 15h3.5"/>`,
	"server":        `<rect x="3" y="4" width="18" height="7" rx="2"/><rect x="3" y="13" width="18" height="7" rx="2"/><path d="M7 7.5h.01M7 16.5h.01M11 7.5h6M11 16.5h6"/>`,
	"users":         `<circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0 1 13 0"/><circle cx="17" cy="9" r="2.5"/><path d="M17 14a4.5 4.5 0 0 1 4.5 5"/>`,
	"user":          `<circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/>`,
	"flag":          `<path d="M5 21V4"/><path d="M5 4h12l-2.5 4.5L17 13H5"/>`,
	"mail":          `<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3.5 7l8.5 6 8.5-6"/>`,
	"database":      `<ellipse cx="12" cy="5.5" rx="8" ry="3"/><path d="M4 5.5v13c0 1.7 3.6 3 8 3s8-1.3 8-3v-13M4 12c0 1.7 3.6 3 8 3s8-1.3 8-3"/>`,
	"clipboard":     `<rect x="5" y="4" width="14" height="17" rx="2"/><path d="M9 2.5h6V6H9zM9 13l2 2 4-4"/>`,
	"help":          `<circle cx="12" cy="12" r="9"/><path d="M9.5 9.2a2.6 2.6 0 1 1 3.6 2.4c-.7.3-1.1.9-1.1 1.6v.6M12 17h.01"/>`,
	"megaphone":     `<path d="M4 9.5v5h3l8 5v-15l-8 5z"/><path d="M18.5 9a4 4 0 0 1 0 6M7 14.5l1.5 5.5h3"/>`,
	"sliders":       `<path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/>`,
	"list":          `<path d="M9 6h12M9 12h12M9 18h12M4 6h.01M4 12h.01M4 18h.01"/>`,
	"cloud":         `<path d="M7 19a4.5 4.5 0 0 1-.6-9A6 6 0 0 1 18 11a4 4 0 0 1-.5 8z"/><path d="M12 12v5M9.5 14.5L12 12l2.5 2.5"/>`,
	"bell":          `<path d="M6 16.5V11a6 6 0 0 1 12 0v5.5l1.5 1.5h-15z"/><path d="M10 21h4"/>`,
	"chevron-down":  `<path d="M6 9l6 6 6-6"/>`,
	"chevron-left":  `<path d="M15 5l-7 7 7 7"/>`,
	"chevron-right": `<path d="M9 5l7 7-7 7"/>`,
	"arrow-right":   `<path d="M5 12h14M13 6l6 6-6 6"/>`,
	"clock":         `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3.5 2"/>`,
	"check":         `<path d="M5 12.5l4.5 4.5L19 7.5"/>`,
	"check-circle":  `<circle cx="12" cy="12" r="9"/><path d="M8 12.5l2.7 2.7L16 9.8"/>`,
	"x-circle":      `<circle cx="12" cy="12" r="9"/><path d="M9 9l6 6M15 9l-6 6"/>`,
	"minus-circle":  `<circle cx="12" cy="12" r="9"/><path d="M8 12h8"/>`,
	"half-circle":   `<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 1 0 18z" fill="currentColor"/>`,
	"dot-circle":    `<circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="2.5" fill="currentColor"/>`,
	"plus":          `<path d="M12 5v14M5 12h14"/>`,
	"plus-circle":   `<circle cx="12" cy="12" r="9"/><path d="M12 8v8M8 12h8"/>`,
	"upload":        `<path d="M12 16V4M7 9l5-5 5 5M4 15v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4"/>`,
	"download":      `<path d="M12 4v12M7 11l5 5 5-5M4 15v4a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-4"/>`,
	"bar-chart":     `<path d="M4 20h16"/><rect x="5" y="11" width="3.2" height="6.5" rx=".6"/><rect x="10.4" y="5.5" width="3.2" height="12" rx=".6"/><rect x="15.8" y="13" width="3.2" height="4.5" rx=".6"/>`,
	"activity":      `<path d="M3 20h18"/><path d="M4 16l4.5-5.5 4 3.5L20 6"/>`,
	"zap":           `<path d="M13 2L4.5 13.5H11l-1 8.5 8.5-11.5H12z"/>`,
	"pause":         `<rect x="6" y="5" width="4" height="14" rx="1"/><rect x="14" y="5" width="4" height="14" rx="1"/>`,
	"play":          `<path d="M7 4.5l12.5 7.5L7 19.5z"/>`,
	"edit":          `<path d="M4 20h4.5L19.5 9 15 4.5 4 15.5z"/><path d="M13 6.5l4.5 4.5"/>`,
	"printer":       `<path d="M7 9V3.5h10V9"/><rect x="3" y="9" width="18" height="8" rx="2"/><path d="M7 14h10v7H7z"/>`,
	"book":          `<path d="M4 5.5A2.5 2.5 0 0 1 6.5 3H20v15H6.5A2.5 2.5 0 0 0 4 20.5z"/><path d="M4 20.5v-15M20 18v3H6.5M8.5 7.5h7"/>`,
	"flask":         `<path d="M9 3h6M10 3v6.5L4.6 18.2A1.9 1.9 0 0 0 6.2 21h11.6a1.9 1.9 0 0 0 1.6-2.8L14 9.5V3M7 15h10"/>`,
	"message":       `<path d="M4 5h16v11.5H9.5L4 20.5z"/><path d="M8 9.5h8M8 12.5h5"/>`,
	"shield":        `<path d="M12 3l8 3v6c0 4.6-3.4 8-8 9-4.6-1-8-4.4-8-9V6z"/><path d="M9 12l2.2 2.2L15.5 10"/>`,
	"globe":         `<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.5 2.6 3.7 5.6 3.7 9s-1.2 6.4-3.7 9M12 3C9.5 5.6 8.3 8.6 8.3 12s1.2 6.4 3.7 9"/>`,
	"logout":        `<path d="M15 4h3a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2h-3M10 16.5L5.5 12 10 7.5M5.5 12H16"/>`,
	"menu":          `<path d="M4 6.5h16M4 12h16M4 17.5h16"/>`,
	"external":      `<path d="M14 4h6v6M20 4l-9 9"/><path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/>`,
	"map-pin":       `<path d="M12 21.5s-7-6.1-7-11.8a7 7 0 0 1 14 0c0 5.7-7 11.8-7 11.8z"/><circle cx="12" cy="9.7" r="2.6"/>`,
	"alert":         `<path d="M10.3 4.2L2.6 18a2 2 0 0 0 1.7 3h15.4a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0z"/><path d="M12 9.5v4.5M12 17.5h.01"/>`,
	"award":         `<circle cx="12" cy="9" r="6"/><path d="M8.6 14l-1.6 7.5 5-2.8 5 2.8-1.6-7.5"/>`,
	"copy":          `<rect x="8.5" y="8.5" width="12" height="12" rx="2"/><path d="M15.5 8.5V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v7.5a2 2 0 0 0 2 2h2.5"/>`,
	"balloon":       `<path d="M12 2.5a6.5 6.5 0 0 1 6.5 6.5c0 4.4-3.3 7.5-6.5 7.5S5.5 13.4 5.5 9A6.5 6.5 0 0 1 12 2.5z"/><path d="M12 16.5l-1 1.5h2l-1-1.5c0 2.5-2 2.5-2 5"/>`,
	"refresh":       `<path d="M20 12a8 8 0 1 1-2.4-5.7"/><path d="M20 4v5h-5"/>`,
	"eye":           `<path d="M2 12s3.6-7 10-7 10 7 10 7-3.6 7-10 7S2 12 2 12z"/><circle cx="12" cy="12" r="3"/>`,
	"key":           `<circle cx="8" cy="15" r="4.5"/><path d="M11.2 11.8L20 3M16.5 6.5l2.5 2.5M14 9l2 2"/>`,
	"code":          `<path d="M8 8l-4.5 4L8 16M16 8l4.5 4-4.5 4M13.5 5l-3 14"/>`,
	"inbox":         `<path d="M3 13l2.5-8h13L21 13v6a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1z"/><path d="M3 13h5l1.5 2.5h5L16 13h5"/>`,
	"image":         `<rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="9.5" r="2"/><path d="M21 16l-5.5-5.5L5 20"/>`,
	"hash":          `<path d="M4.5 9h15M4.5 15h15M10 4L8 20M16 4l-2 16"/>`,
	"layers":        `<path d="M12 3l9 5-9 5-9-5z"/><path d="M3 13l9 5 9-5"/>`,
	"lock":          `<rect x="4.5" y="10.5" width="15" height="10.5" rx="2"/><path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"/>`,
	"trash":         `<path d="M4 7h16M9.5 7V4.5h5V7M6 7l1 13.5h10L18 7"/>`,
	"sun":           `<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4"/>`,
	"moon":          `<path d="M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z"/>`,
	"volume":        `<path d="M4 9.5v5h3.5L13 19V5L7.5 9.5z"/><path d="M16.5 9a4 4 0 0 1 0 6M19 6.5a7.5 7.5 0 0 1 0 11"/>`,
	"heart-pulse":   `<path d="M3 12h4l2-4 3 8 2-4h7"/>`,
}

var icons = func() map[string]template.HTML {
	m := make(map[string]template.HTML, len(iconPaths))
	for name, paths := range iconPaths {
		m[name] = template.HTML(`<svg class="ic" viewBox="0 0 24 24" aria-hidden="true" focusable="false">` + paths + `</svg>`)
	}
	return m
}()

// Icon returns the inline SVG of an interface icon ("" for an unknown
// name; TestIconsExist keeps the templates honest).
func Icon(name string) template.HTML { return icons[name] }

// IconNames lists the icons (for tests).
func IconNames() []string {
	out := make([]string, 0, len(icons))
	for n := range icons {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

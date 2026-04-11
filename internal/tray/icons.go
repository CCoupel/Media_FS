package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
)

// TrayState represents the overall operational state of the application.
type TrayState int

const (
	TrayStateIdle    TrayState = iota // all unmounted, no errors — grey ⏸
	TrayStateActive                   // all enabled servers mounted OK — green ▶
	TrayStateWarning                  // ≥1 server unreachable or partial mount — orange ▶
	TrayStateError                    // all servers failed / critical error — red ✕
)

// generateTrayIcon returns an ICO-wrapped 32×32 PNG for the given state.
// Design: film strip with a state symbol in the center frame.
func generateTrayIcon(state TrayState) []byte {
	const size = 32
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// All pixels start transparent.

	bg   := color.RGBA{0x0f, 0x17, 0x2a, 0xff} // #0f172a — film strip body
	hole := color.RGBA{0x33, 0x41, 0x55, 0xff} // #334155 — sprocket holes
	sep  := color.RGBA{0x1e, 0x29, 0x3b, 0xff} // #1e293b — frame separators

	// Film strip body: rect(2,3,28,26) rx≈3
	fillRect(img, 2, 3, 28, 26, bg)
	clearCorners(img, 2, 3, 28, 26, 3)

	// Sprocket holes — top row (4 holes, each 5×5, rx≈1)
	for _, hx := range []int{4, 11, 18, 25} {
		fillRect(img, hx, 5, 5, 5, hole)
		clearCorners(img, hx, 5, 5, 5, 1)
	}
	// Sprocket holes — bottom row
	for _, hx := range []int{4, 11, 18, 25} {
		fillRect(img, hx, 22, 5, 5, hole)
		clearCorners(img, hx, 22, 5, 5, 1)
	}

	// Frame separators
	fillRect(img, 2, 12, 28, 2, sep)
	fillRect(img, 2, 19, 28, 2, sep)

	// Center symbol (center frame: y=13..19)
	switch state {
	case TrayStateActive:
		// ▶ green play — polygon (11,14)(11,18)(21,16)
		fillTriangle(img, 11, 14, 21, 16, 18, color.RGBA{0x22, 0xc5, 0x5e, 0xff})
	case TrayStateWarning:
		// ▶ orange play — same polygon
		fillTriangle(img, 11, 14, 21, 16, 18, color.RGBA{0xf9, 0x73, 0x16, 0xff})
	case TrayStateIdle:
		// ⏸ grey pause — two vertical bars
		c := color.RGBA{0x64, 0x74, 0x8b, 0xff}
		fillRect(img, 11, 14, 3, 4, c)
		fillRect(img, 18, 14, 3, 4, c)
	case TrayStateError:
		// ✕ red cross
		c := color.RGBA{0xef, 0x44, 0x44, 0xff}
		drawLine(img, 11, 14, 21, 18, c)
		drawLine(img, 21, 14, 11, 18, c)
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return wrapInICO(buf.Bytes(), size)
}

// ── drawing primitives ────────────────────────────────────────────────────────

// fillRect fills a w×h rectangle at (x,y) with color c.
func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			img.Set(x+dx, y+dy, c)
		}
	}
}

// clearCorners clears the r×r corner regions that lie outside a quarter-circle
// of radius r, rounding the corners of a previously-filled rectangle.
// Requires r ≤ min(w,h)/2.
func clearCorners(img *image.RGBA, x, y, w, h, r int) {
	clear := color.RGBA{}
	for dy := 0; dy < r; dy++ {
		for dx := 0; dx < r; dx++ {
			if (dx-r)*(dx-r)+(dy-r)*(dy-r) > r*r {
				img.Set(x+dx, y+dy, clear)           // top-left
				img.Set(x+w-1-dx, y+dy, clear)       // top-right
				img.Set(x+dx, y+h-1-dy, clear)       // bottom-left
				img.Set(x+w-1-dx, y+h-1-dy, clear)   // bottom-right
			}
		}
	}
}

// fillTriangle fills a right-pointing triangle with left vertices at
// (x0, yTop) and (x0, yBottom) and right tip at (xTip, yMid).
func fillTriangle(img *image.RGBA, x0, yTop, xTip, yMid, yBottom int, c color.RGBA) {
	for y := yTop; y <= yBottom; y++ {
		var xRight int
		switch {
		case yMid == yTop:
			xRight = xTip
		case y <= yMid:
			xRight = x0 + (xTip-x0)*(y-yTop)/(yMid-yTop)
		case yBottom == yMid:
			xRight = x0
		default:
			xRight = xTip - (xTip-x0)*(y-yMid)/(yBottom-yMid)
		}
		for x := x0; x <= xRight; x++ {
			img.Set(x, y, c)
		}
	}
}

// drawLine draws a thick line (~3px) using Bresenham's algorithm with a 3×3 brush.
func drawLine(img *image.RGBA, x0, y0, x1, y1 int, c color.RGBA) {
	dx := iabs(x1 - x0)
	dy := iabs(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for {
		for oy := -1; oy <= 1; oy++ {
			for ox := -1; ox <= 1; ox++ {
				img.Set(x0+ox, y0+oy, c)
			}
		}
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

func iabs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// wrapInICO wraps PNG bytes into a minimal ICO container (Vista+ PNG-in-ICO).
// fyne.io/systray on Windows requires ICO format; PNG-in-ICO is supported via
// CreateIconFromResourceEx on Windows Vista and later.
func wrapInICO(pngData []byte, size int) []byte {
	buf := make([]byte, 22+len(pngData))
	binary.LittleEndian.PutUint16(buf[0:], 0)                    // reserved
	binary.LittleEndian.PutUint16(buf[2:], 1)                    // type = ICO
	binary.LittleEndian.PutUint16(buf[4:], 1)                    // count = 1
	buf[6] = byte(size)                                           // width
	buf[7] = byte(size)                                           // height
	buf[8] = 0                                                    // color count
	buf[9] = 0                                                    // reserved
	binary.LittleEndian.PutUint16(buf[10:], 1)                   // planes
	binary.LittleEndian.PutUint16(buf[12:], 32)                  // bit depth
	binary.LittleEndian.PutUint32(buf[14:], uint32(len(pngData))) // data size
	binary.LittleEndian.PutUint32(buf[18:], 22)                  // data offset
	copy(buf[22:], pngData)
	return buf
}

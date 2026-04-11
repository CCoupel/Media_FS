// Package appicon generates the 256×256 application icon PNG programmatically.
// The icon is a film-strip frame containing a 3.5" floppy disk.
package appicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// GeneratePNG returns the PNG-encoded 256×256 application icon (active / green state).
func GeneratePNG() []byte {
	const w, h = 256, 256
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	// ── colours ──────────────────────────────────────────────────────────────
	outerBg  := c(0x1e, 0x29, 0x3b) // #1e293b — rounded-square background
	stripBg  := c(0x0f, 0x17, 0x2a) // #0f172a — film-strip inner area
	holeCol  := c(0x22, 0xc5, 0x5e) // #22c55e — sprocket holes (active / green)
	floppyBd := c(0x33, 0x41, 0x55) // #334155 — floppy plastic body
	labelBg  := c(0xe2, 0xe8, 0xf0) // #e2e8f0 — label sticker
	lineCol  := c(0x94, 0xa3, 0xb8) // #94a3b8 — label text lines & shutter
	shutterG := c(0x64, 0x74, 0x8b) // #64748b — shutter centre groove
	hlCol    := c(0xb5, 0xc1, 0xd0) // #b5c1d0 — shutter highlight (blend of #cbd5e1@0.6 over #94a3b8)

	// ── outer rounded square (256×256, rx=40) ────────────────────────────────
	fillRect(img, 0, 0, 256, 256, outerBg)
	clearCorners(img, 0, 0, 256, 256, 40)

	// ── film strip body (24,40,208,176 rx=14) ────────────────────────────────
	fillRect(img, 24, 40, 208, 176, stripBg)
	clearCorners(img, 24, 40, 208, 176, 14)

	// ── sprocket holes — top row (5 holes, 22×22, rx=5) ─────────────────────
	for _, hx := range []int{45, 81, 117, 153, 189} {
		fillRect(img, hx, 52, 22, 22, holeCol)
		clearCorners(img, hx, 52, 22, 22, 5)
	}
	// ── sprocket holes — bottom row ──────────────────────────────────────────
	for _, hx := range []int{45, 81, 117, 153, 189} {
		fillRect(img, hx, 182, 22, 22, holeCol)
		clearCorners(img, hx, 182, 22, 22, 5)
	}

	// ── frame separators ─────────────────────────────────────────────────────
	fillRect(img, 24, 82, 208, 2, outerBg)
	fillRect(img, 24, 172, 208, 2, outerBg)

	// ── floppy body (88,90,80,82 rx=6) ───────────────────────────────────────
	fillRect(img, 88, 90, 80, 82, floppyBd)
	clearCorners(img, 88, 90, 80, 82, 6)

	// ── top-right corner cut: polygon (152,90)(168,90)(168,106) ─────────────
	// The triangle is drawn in outerBg to simulate the characteristic 3.5" notch.
	for y := 90; y <= 106; y++ {
		xLeft := 152 + (y - 90) // hypotenuse: slope 1
		for x := xLeft; x <= 168; x++ {
			img.Set(x, y, outerBg)
		}
	}

	// ── label sticker (92,94,68,38 rx=3) ─────────────────────────────────────
	fillRect(img, 92, 94, 68, 38, labelBg)
	clearCorners(img, 92, 94, 68, 38, 3)

	// ── label lines ──────────────────────────────────────────────────────────
	fillRect(img, 98, 102, 44, 4, lineCol)
	fillRect(img, 98, 112, 32, 4, lineCol)

	// ── metal shutter (106,140,44,24 rx=3) ───────────────────────────────────
	fillRect(img, 106, 140, 44, 24, lineCol)
	clearCorners(img, 106, 140, 44, 24, 3)

	// ── shutter centre groove (124,140,8,24) ─────────────────────────────────
	fillRect(img, 124, 140, 8, 24, shutterG)

	// ── shutter metallic highlight (106,140,44,4) ────────────────────────────
	fillRect(img, 106, 140, 44, 4, hlCol)
	clearCorners(img, 106, 140, 44, 4, 3)

	// ── write-protect notch (92,140,8,10) ────────────────────────────────────
	fillRect(img, 92, 140, 8, 10, stripBg)
	clearCorners(img, 92, 140, 8, 10, 2)

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// ── drawing primitives ────────────────────────────────────────────────────────

func c(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 255} }

func fillRect(img *image.RGBA, x, y, w, h int, col color.RGBA) {
	for dy := 0; dy < h; dy++ {
		for dx := 0; dx < w; dx++ {
			img.Set(x+dx, y+dy, col)
		}
	}
}

// clearCorners removes corner pixels that lie outside a circle of radius r,
// rounding the corners of a previously-filled rectangle (requires r ≤ min(w,h)/2).
func clearCorners(img *image.RGBA, x, y, w, h, r int) {
	transparent := color.RGBA{}
	for dy := 0; dy < r; dy++ {
		for dx := 0; dx < r; dx++ {
			if (dx-r)*(dx-r)+(dy-r)*(dy-r) > r*r {
				img.Set(x+dx, y+dy, transparent)
				img.Set(x+w-1-dx, y+dy, transparent)
				img.Set(x+dx, y+h-1-dy, transparent)
				img.Set(x+w-1-dx, y+h-1-dy, transparent)
			}
		}
	}
}

// Package qr draws QR codes for short links, as PNG for download and SVG for
// print, where it stays sharp at any size.
package qr

import (
	"bytes"
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// Size limits in pixels.
const (
	MinSize     = 128
	MaxSize     = 1024
	DefaultSize = 512
)

// Medium error correction survives a scuffed or partly covered print while
// keeping the code small enough to scan from a distance.
const level = qrcode.Medium

// PNG encodes content as a size x size PNG.
func PNG(content string, size int) ([]byte, error) {
	b, err := qrcode.Encode(content, level, size)
	if err != nil {
		return nil, fmt.Errorf("qr: encode: %w", err)
	}
	return b, nil
}

// SVG encodes content as an SVG of size x size pixels. Each run of dark
// modules in a row becomes one rectangle of a single path, which keeps the
// file small.
func SVG(content string, size int) ([]byte, error) {
	q, err := qrcode.New(content, level)
	if err != nil {
		return nil, fmt.Errorf("qr: encode: %w", err)
	}
	bm := q.Bitmap() // includes the quiet zone scanners need
	n := len(bm)
	var b bytes.Buffer
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size, n, n)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, n, n)
	for y, row := range bm {
		for x := 0; x < len(row); {
			if !row[x] {
				x++
				continue
			}
			start := x
			for x < len(row) && row[x] {
				x++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", start, y, x-start, x-start)
		}
	}
	b.WriteString(`"/></svg>`)
	return b.Bytes(), nil
}

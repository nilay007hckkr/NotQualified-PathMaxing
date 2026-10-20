package qr

import (
	"bytes"
	"encoding/xml"
	"image/png"
	"strings"
	"testing"
)

func TestPNG(t *testing.T) {
	for _, size := range []int{MinSize, DefaultSize, MaxSize} {
		b, err := PNG("https://pm.example/diwali?s=qr", size)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatalf("size %d: not a PNG: %v", size, err)
		}
		if got := img.Bounds().Dx(); got != size {
			t.Fatalf("width %d, want %d", got, size)
		}
	}
}

func TestSVG(t *testing.T) {
	b, err := SVG("https://pm.example/diwali?s=qr", 256)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		XMLName xml.Name
		Width   string `xml:"width,attr"`
		Path    struct {
			D string `xml:"d,attr"`
		} `xml:"path"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("not well-formed XML: %v", err)
	}
	if doc.XMLName.Local != "svg" || doc.Width != "256" || strings.Count(doc.Path.D, "M") < 50 {
		t.Fatalf("unexpected SVG: %s", b[:200])
	}
	// Different content gives a different code.
	other, _ := SVG("https://pm.example/other?s=qr", 256)
	if bytes.Equal(b, other) {
		t.Fatal("two different links produced the same QR code")
	}
}

package vision

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"
)

// scene draws a deterministic synthetic "room": a few large luminance blocks
// (windows, board, furniture) on a gradient. Different seeds = different rooms.
func scene(seed int64, w, h int, brightness int, noise int, crop int) image.Image {
	r := rand.New(rand.NewSource(seed))
	type blk struct{ x0, y0, x1, y1, l int }
	var blks []blk
	for i := 0; i < 14; i++ {
		x0, y0 := r.Intn(w*3/4), r.Intn(h*3/4)
		blks = append(blks, blk{x0, y0, x0 + 20 + r.Intn(w/3), y0 + 20 + r.Intn(h/3), r.Intn(256)})
	}
	base := r.Intn(120) + 40
	nr := rand.New(rand.NewSource(seed + 1000))
	img := image.NewGray(image.Rect(0, 0, w-2*crop, h-2*crop))
	for y := 0; y < h-2*crop; y++ {
		for x := 0; x < w-2*crop; x++ {
			sx, sy := x+crop, y+crop
			l := base + sy*60/h
			for _, b := range blks {
				if sx >= b.x0 && sx < b.x1 && sy >= b.y0 && sy < b.y1 {
					l = b.l
				}
			}
			l += brightness
			if noise > 0 {
				l += nr.Intn(2*noise+1) - noise
			}
			if l < 0 {
				l = 0
			}
			if l > 255 {
				l = 255
			}
			img.SetGray(x, y, color.Gray{uint8(l)})
		}
	}
	return img
}

func encJPEG(img image.Image, q int) []byte {
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, &jpeg.Options{Quality: q})
	return b.Bytes()
}

func fp(t *testing.T, data []byte) []byte {
	t.Helper()
	f, err := Fingerprint(data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSamePlaceMatchesDifferentPlaceDoesNot(t *testing.T) {
	const w, h = 320, 240
	ref := fp(t, encJPEG(scene(1, w, h, 0, 0, 0), 90))

	same := map[string][]byte{
		"identical":         encJPEG(scene(1, w, h, 0, 0, 0), 90),
		"recompressed":      encJPEG(scene(1, w, h, 0, 0, 0), 40),
		"brighter":          encJPEG(scene(1, w, h, 35, 0, 0), 80),
		"darker":            encJPEG(scene(1, w, h, -35, 0, 0), 80),
		"sensor noise":      encJPEG(scene(1, w, h, 0, 14, 0), 80),
		"slightly reframed": encJPEG(scene(1, w, h, 0, 0, 6), 80),
		"dim and noisy":     encJPEG(scene(1, w, h, -25, 12, 3), 60),
	}
	for name, data := range same {
		d := Distance(ref, fp(t, data))
		t.Logf("same place, %-18s distance %3d", name, d)
		if d > MatchMaxDistance {
			t.Errorf("%s: distance %d exceeds threshold %d", name, d, MatchMaxDistance)
		}
	}

	minOther := 256
	for seed := int64(2); seed < 60; seed++ {
		d := Distance(ref, fp(t, encJPEG(scene(seed, w, h, 0, 0, 0), 85)))
		if d < minOther {
			minOther = d
		}
		if d <= MatchMaxDistance {
			t.Errorf("unrelated scene seed %d matched at distance %d", seed, d)
		}
	}
	t.Logf("closest of 58 unrelated scenes: %d (threshold %d)", minOther, MatchMaxDistance)
}

func TestMatchesAgainstSeveralReferences(t *testing.T) {
	a := fp(t, encJPEG(scene(10, 320, 240, 0, 0, 0), 85))
	b := fp(t, encJPEG(scene(11, 320, 240, 0, 0, 0), 85))
	probe := fp(t, encJPEG(scene(11, 320, 240, 20, 6, 2), 70))
	if !Matches([][]byte{a, b}, probe) {
		t.Fatal("should match the second reference angle")
	}
	if Matches([][]byte{a}, probe) {
		t.Fatal("must not match an unrelated reference")
	}
	if Matches(nil, probe) || Best(nil, probe) != -1 {
		t.Fatal("no references can never match")
	}
}

func TestRejectsUnusableAndHostileInput(t *testing.T) {
	black := image.NewGray(image.Rect(0, 0, 320, 240))
	if _, err := Fingerprint(encJPEG(black, 90)); err != ErrUnusable {
		t.Fatalf("black frame: %v", err)
	}
	flat := image.NewGray(image.Rect(0, 0, 320, 240))
	for i := range flat.Pix {
		flat.Pix[i] = 200
	}
	if _, err := Fingerprint(encJPEG(flat, 90)); err != ErrUnusable {
		t.Fatalf("blank wall: %v", err)
	}
	if _, err := Fingerprint(nil); err != ErrTooLarge {
		t.Fatalf("empty: %v", err)
	}
	if _, err := Fingerprint([]byte("GIF89a not an image at all............................")); err != ErrInvalid {
		t.Fatalf("garbage: %v", err)
	}
	if _, err := Fingerprint(make([]byte, MaxBytes+1)); err != ErrTooLarge {
		t.Fatalf("oversized: %v", err)
	}
	var tiny bytes.Buffer
	_ = png.Encode(&tiny, image.NewGray(image.Rect(0, 0, 8, 8)))
	if _, err := Fingerprint(tiny.Bytes()); err != ErrInvalid {
		t.Fatalf("tiny: %v", err)
	}
	// A small file that declares huge dimensions (decompression bomb) is refused
	// from its header, before any pixel memory is allocated.
	var bomb bytes.Buffer
	_ = png.Encode(&bomb, image.NewGray(image.Rect(0, 0, 4000, 4000)))
	if bomb.Len() < MaxBytes {
		if _, err := Fingerprint(bomb.Bytes()); err != ErrTooLarge {
			t.Fatalf("bomb: %v", err)
		}
	}
}

func TestDistanceProperties(t *testing.T) {
	a := fp(t, encJPEG(scene(3, 320, 240, 0, 0, 0), 85))
	if Distance(a, a) != 0 {
		t.Fatal("self distance")
	}
	if Distance(a, make([]byte, 5)) != totalBits {
		t.Fatal("length mismatch must be maximal")
	}
}

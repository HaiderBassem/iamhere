// Package vision implements the "visual presence" check: a perceptual
// fingerprint of a photo, compared against fingerprints of reference photos of
// a venue.
//
// This is deliberately simple and dependency-free (standard library only). A
// fingerprint is a 256-bit difference hash (dHash) of the photo's luminance:
// it records which neighbouring regions are brighter than which, so it is
// insensitive to overall brightness, exposure and re-compression, but changes
// when the scene changes. Comparisons between regions of almost equal
// brightness (plain walls, ceilings, sky) are noise, so each bit also carries a
// confidence flag and only bits that are reliable in BOTH photos are compared.
// It answers "does this photo look like the same place as a reference photo?"
// — it is evidence, not proof (see docs/BACKEND.md).
//
// Photos are never stored: only the 64-byte fingerprint is kept.
package vision

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg" // register decoders
	_ "image/png"
	"math"
	"math/bits"
)

const (
	// FingerprintLen is the size in bytes of a fingerprint: 32 bytes of
	// comparison bits followed by 32 bytes of confidence flags.
	FingerprintLen = 64
	bitsLen        = FingerprintLen / 2
	totalBits      = bitsLen * 8

	// minDelta is the luminance difference (0-255 scale) below which a
	// comparison between two neighbouring cells is treated as noise.
	minDelta = 3.0

	// minCommonBits is how many reliable bits two fingerprints must share
	// before they can be compared at all; below it a photo is uninformative
	// (e.g. almost everything is flat) and never counts as a match.
	minCommonBits = 64

	gridW, gridH = 17, 16 // 16 horizontal comparisons per row x 16 rows

	// MaxBytes and MaxPixels bound the work an untrusted upload can cause.
	MaxBytes  = 512 << 10
	MaxPixels = 2_000_000
	minSide   = 32

	// minContrast is the smallest luminance spread (0-255 scale, std-dev of the
	// grid cells) a photo needs. A covered lens or a blank wall is below it.
	minContrast = 4.0

	// MatchMaxDistance is the largest normalised distance (0-256: mismatching
	// share of the commonly reliable bits, scaled to 256) at which a photo
	// counts as showing the same place as a reference. Unrelated scenes land
	// around 100-130; the same place shot at another angle or in other light
	// lands well under this. It is a tuning knob: lower is stricter.
	// A non-match never rejects anyone, it only leaves the check-in in review.
	MatchMaxDistance = 64
)

var (
	ErrTooLarge = errors.New("vision: image too large")
	ErrInvalid  = errors.New("vision: not a usable JPEG or PNG image")
	// ErrUnusable means the photo decoded but carries no usable scene
	// (covered lens, black or blank frame).
	ErrUnusable = errors.New("vision: photo has too little detail")
)

// Fingerprint decodes a JPEG/PNG and returns its 32-byte dHash.
func Fingerprint(data []byte) ([]byte, error) {
	if len(data) == 0 || len(data) > MaxBytes {
		return nil, ErrTooLarge
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	if cfg.Width < minSide || cfg.Height < minSide {
		return nil, ErrInvalid
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return nil, ErrTooLarge
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrInvalid
	}
	grid := lumaGrid(img)

	var sum, sumSq float64
	for _, v := range grid {
		sum += v
		sumSq += v * v
	}
	n := float64(len(grid))
	mean := sum / n
	if math.Sqrt(math.Max(0, sumSq/n-mean*mean)) < minContrast {
		return nil, ErrUnusable
	}

	fp := make([]byte, FingerprintLen)
	bit, reliable := 0, 0
	for y := 0; y < gridH; y++ {
		for x := 0; x < gridW-1; x++ {
			l, r := grid[y*gridW+x], grid[y*gridW+x+1]
			if l < r {
				fp[bit/8] |= 1 << (bit % 8)
			}
			if math.Abs(l-r) >= minDelta {
				fp[bitsLen+bit/8] |= 1 << (bit % 8)
				reliable++
			}
			bit++
		}
	}
	if reliable < minCommonBits {
		return nil, ErrUnusable
	}
	return fp, nil
}

// lumaGrid box-averages the image's luminance into a gridW x gridH grid.
func lumaGrid(img image.Image) []float64 {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	sums := make([]float64, gridW*gridH)
	counts := make([]float64, gridW*gridH)
	for y := 0; y < h; y++ {
		gy := y * gridH / h
		for x := 0; x < w; x++ {
			gx := x * gridW / w
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			// Rec. 601 luma on 16-bit channels, scaled to 0-255.
			l := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(bl)) / 257
			i := gy*gridW + gx
			sums[i] += l
			counts[i]++
		}
	}
	for i := range sums {
		if counts[i] > 0 {
			sums[i] /= counts[i]
		}
	}
	return sums
}

// Distance compares two fingerprints on the bits that are reliable in both and
// returns the mismatching share scaled to 0-256 (0 = identical, ~128 = unrelated).
// Fingerprints with too few commonly reliable bits are maximally distant.
func Distance(a, b []byte) int {
	if len(a) != FingerprintLen || len(b) != FingerprintLen {
		return totalBits
	}
	common, diff := 0, 0
	for i := 0; i < bitsLen; i++ {
		both := a[bitsLen+i] & b[bitsLen+i]
		common += bits.OnesCount8(both)
		diff += bits.OnesCount8((a[i] ^ b[i]) & both)
	}
	if common < minCommonBits {
		return totalBits
	}
	return diff * totalBits / common
}

// Best returns the smallest distance from f to any reference, or -1 when there
// are no references.
func Best(refs [][]byte, f []byte) int {
	best := -1
	for _, r := range refs {
		if d := Distance(r, f); best < 0 || d < best {
			best = d
		}
	}
	return best
}

// Matches reports whether f is close enough to at least one reference.
func Matches(refs [][]byte, f []byte) bool {
	d := Best(refs, f)
	return d >= 0 && d <= MatchMaxDistance
}

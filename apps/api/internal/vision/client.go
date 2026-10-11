// Package vision talks to the vision service (apps/vision), which does the
// computer-vision work for strict mode's photo check: reading the check-in QR
// code inside an attendee's photo and recognising the room against reference
// photos. The service is internal (not exposed to the internet) and stateless
// apart from a feature cache keyed by the SHA-256 of each reference image.
package vision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/sync/singleflight"
)

// MaxPhotoBytes bounds every image sent to the service.
const MaxPhotoBytes = 1500 << 10

var (
	// ErrRefGone is what a Ref's Load returns when the reference was deleted
	// meanwhile; the check goes on without it.
	ErrRefGone = errors.New("vision: reference no longer exists")
	// ErrUnavailable means the service could not be reached or failed; the
	// caller treats the photo check as not done (never as passed).
	ErrUnavailable = errors.New("vision: service unavailable")
	// ErrNotConfigured means no service URL is set.
	ErrNotConfigured = errors.New("vision: not configured")
)

// PhotoError is a problem with the photo itself that the person can fix.
type PhotoError struct {
	Codes   []string
	Message string
}

func (e *PhotoError) Error() string { return "vision: " + e.Message }

// Ref is a reference known by its content hash: a photo, or (Features) a
// learned view of the room. Load is only called when the service does not have
// it cached yet.
type Ref struct {
	SHA256   [32]byte
	Features bool
	Load     func(context.Context) ([]byte, error)
}

func SHA256(b []byte) [32]byte { return sha256.Sum256(b) }

// RefScore is how strongly the probe matches one reference.
type RefScore struct {
	SHA256  string `json:"sha256"`
	Score   int    `json:"score"`
	Matched bool   `json:"matched"`
}

// Result of checking an attendee's photo.
type Result struct {
	Codes   []string `json:"codes"`   // QR code texts read in the photo
	Matched bool     `json:"matched"` // the room matches at least one reference
	Score   int      `json:"score"`   // best score over the references
	// Threshold is the score at which the service calls it the same room.
	Threshold int        `json:"threshold"`
	Refs      []RefScore `json:"refs"`
	Model     string     `json:"model"` // matcher name and version, for the evidence trail
	// ProbeID fetches this photo's room features for a short while (ProbeFeatures).
	ProbeID string `json:"probe_id"`
}

// ReferenceInfo describes a reference photo accepted by the service.
type ReferenceInfo struct {
	Width     int `json:"width"`
	Height    int `json:"height"`
	Keypoints int `json:"keypoints"`
}

type Client struct {
	base string
	hc   *http.Client
	// uploads of references the service has not cached yet, shared by the
	// check-ins that need the same one at the same time (e.g. after a restart)
	warming singleflight.Group
}

// New returns a client for the service at base (e.g. http://vision:8090), or
// nil when base is empty (photo check not configured).
func New(base string) *Client {
	if base == "" {
		return nil
	}
	return &Client{base: base, hc: &http.Client{Timeout: 30 * time.Second}}
}

type wireRef struct {
	SHA256 string `json:"sha256"`
}

type errBody struct {
	Error   string   `json:"error"`
	Message string   `json:"message"`
	Codes   []string `json:"codes"`
	Missing []string `json:"missing"`
}

func (c *Client) post(ctx context.Context, path string, in, out any) (int, *errBody, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if res.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, out); err != nil {
			return 0, nil, fmt.Errorf("%w: bad response: %v", ErrUnavailable, err)
		}
		return res.StatusCode, nil, nil
	}
	var e errBody
	_ = json.Unmarshal(body, &e)
	return res.StatusCode, &e, nil
}

// CheckReference validates a reference photo (sharpness, light, detail) and
// warms the service's cache. A *PhotoError explains what to fix.
func (c *Client) CheckReference(ctx context.Context, img []byte) (ReferenceInfo, error) {
	if c == nil {
		return ReferenceInfo{}, ErrNotConfigured
	}
	var info ReferenceInfo
	status, e, err := c.post(ctx, "/v1/reference", map[string]any{"image": base64.StdEncoding.EncodeToString(img)}, &info)
	if err != nil {
		return info, err
	}
	switch status {
	case http.StatusOK:
		return info, nil
	case http.StatusUnprocessableEntity:
		return info, &PhotoError{Codes: e.Codes, Message: e.Message}
	}
	return info, fmt.Errorf("%w: status %d", ErrUnavailable, status)
}

// Verify reads QR codes in the probe photo and scores it against refs. hint is
// the code's four corners as reported by the browser (may be nil).
func (c *Client) Verify(ctx context.Context, probe []byte, hint *[4][2]float64, refs []Ref) (Result, error) {
	if c == nil {
		return Result{}, ErrNotConfigured
	}
	req := map[string]any{"probe": base64.StdEncoding.EncodeToString(probe)}
	if hint != nil {
		req["hint"] = hint
	}
	wire := make([]wireRef, len(refs))
	byHash := make(map[string]Ref, len(refs))
	for i, r := range refs {
		h := hex.EncodeToString(r.SHA256[:])
		wire[i] = wireRef{SHA256: h}
		byHash[h] = r
	}
	req["refs"] = wire
	// The service answers 409 with the references it has no features for (first
	// use, or after a restart). Those are uploaded one by one, then it is asked again.
	for attempt := 0; attempt < 2; attempt++ {
		var res Result
		status, e, err := c.post(ctx, "/v1/verify", req, &res)
		if err != nil {
			return Result{}, err
		}
		switch status {
		case http.StatusOK:
			return res, nil
		case http.StatusUnprocessableEntity:
			return Result{}, &PhotoError{Codes: e.Codes, Message: e.Message}
		case http.StatusConflict:
			if attempt == 1 {
				return Result{}, fmt.Errorf("%w: references still missing", ErrUnavailable)
			}
			gone := map[string]bool{}
			for _, h := range e.Missing {
				r, ok := byHash[h]
				if !ok {
					return Result{}, fmt.Errorf("%w: asked for an unknown reference", ErrUnavailable)
				}
				if err := c.warm(ctx, h, r); errors.Is(err, ErrRefGone) {
					gone[h] = true
				} else if err != nil {
					return Result{}, err
				}
			}
			if len(gone) > 0 { // deleted since the list was made: compare with the rest
				kept := wire[:0]
				for _, w := range wire {
					if !gone[w.SHA256] {
						kept = append(kept, w)
					}
				}
				wire = kept
				req["refs"] = wire
			}
		default:
			return Result{}, fmt.Errorf("%w: status %d", ErrUnavailable, status)
		}
	}
	return Result{}, ErrUnavailable
}

// warm uploads one reference so the service caches its features. Concurrent
// callers for the same reference share one upload.
func (c *Client) warm(ctx context.Context, hash string, r Ref) error {
	_, err, _ := c.warming.Do(hash, func() (any, error) {
		img, err := r.Load(ctx)
		if err != nil {
			return nil, err
		}
		var info ReferenceInfo
		field := "image"
		if r.Features {
			field = "features"
		}
		status, _, err := c.post(ctx, "/v1/reference", map[string]any{field: base64.StdEncoding.EncodeToString(img)}, &info)
		if err != nil {
			return nil, err
		}
		// 422 (a quality complaint) still caches the features: the photo was
		// accepted when it was saved, so it is used as it is
		if status != http.StatusOK && status != http.StatusUnprocessableEntity {
			return nil, fmt.Errorf("%w: status %d", ErrUnavailable, status)
		}
		return nil, nil
	})
	return err
}

// ProbeFeatures returns the room features of a photo checked moments ago (by
// Result.ProbeID): the learned view kept instead of the photo. The service
// hands them out once, for about two minutes.
func (c *Client) ProbeFeatures(ctx context.Context, probeID string) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	var out struct {
		Features string `json:"features"`
	}
	status, _, err := c.post(ctx, "/v1/features", map[string]string{"probe_id": probeID}, &out)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, status)
	}
	return base64.StdEncoding.DecodeString(out.Features)
}

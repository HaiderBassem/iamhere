package vision

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// fakeService imitates apps/vision: it only knows reference hashes it has been
// sent an image for, and answers 409 with the missing ones otherwise.
func fakeService(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	known := map[string]bool{}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in struct {
			Probe string `json:"probe"`
			Image string `json:"image"`
			Refs  []struct{ SHA256, Image string }
		}
		json.NewDecoder(r.Body).Decode(&in)
		switch r.URL.Path {
		case "/v1/reference":
			if b, _ := base64.StdEncoding.DecodeString(in.Image); string(b) == "blurry" {
				w.WriteHeader(422)
				json.NewEncoder(w).Encode(map[string]any{"error": "photo_unusable", "message": "The photo is blurry.", "codes": []string{"blurry"}})
				return
			}
			json.NewEncoder(w).Encode(ReferenceInfo{Width: 1280, Height: 960, Keypoints: 2000})
		case "/v1/verify":
			if b, _ := base64.StdEncoding.DecodeString(in.Probe); string(b) == "dark" {
				w.WriteHeader(422)
				json.NewEncoder(w).Encode(map[string]any{"error": "photo_unusable", "message": "Too dark.", "codes": []string{"too_dark"}})
				return
			}
			var missing []string
			for _, ref := range in.Refs {
				if ref.Image != "" {
					img, _ := base64.StdEncoding.DecodeString(ref.Image)
					if h := SHA256(img); hex.EncodeToString(h[:]) != ref.SHA256 {
						t.Errorf("image does not match its hash")
					}
					known[ref.SHA256] = true
				}
				if !known[ref.SHA256] {
					missing = append(missing, ref.SHA256)
				}
			}
			if len(missing) > 0 {
				w.WriteHeader(409)
				json.NewEncoder(w).Encode(map[string]any{"error": "missing_refs", "missing": missing})
				return
			}
			json.NewEncoder(w).Encode(Result{Codes: []string{"code"}, Matched: true, Score: 120, Model: "fake"})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func ref(img string, loads *atomic.Int32) Ref {
	return Ref{SHA256: SHA256([]byte(img)), Load: func(context.Context) ([]byte, error) { loads.Add(1); return []byte(img), nil }}
}

func TestVerifySendsImagesOnlyWhenAsked(t *testing.T) {
	srv, calls := fakeService(t)
	c := New(srv.URL)
	var loads atomic.Int32
	refs := []Ref{ref("room-a", &loads), ref("room-b", &loads)}

	res, err := c.Verify(context.Background(), []byte("probe"), nil, refs)
	if err != nil || !res.Matched || res.Codes[0] != "code" {
		t.Fatalf("%+v %v", res, err)
	}
	if loads.Load() != 2 || calls.Load() != 2 {
		t.Fatalf("first use: want 2 image loads in 2 calls, got %d loads, %d calls", loads.Load(), calls.Load())
	}
	// now cached by the service: one call, no image loads
	if _, err := c.Verify(context.Background(), []byte("probe"), nil, refs); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 2 || calls.Load() != 3 {
		t.Fatalf("cached: want no extra loads, got %d loads, %d calls", loads.Load(), calls.Load())
	}
}

func TestPhotoProblemsAndOutages(t *testing.T) {
	srv, _ := fakeService(t)
	c := New(srv.URL)
	var pe *PhotoError
	if _, err := c.Verify(context.Background(), []byte("dark"), nil, nil); !errors.As(err, &pe) || pe.Codes[0] != "too_dark" {
		t.Fatalf("dark probe: %v", err)
	}
	if _, err := c.CheckReference(context.Background(), []byte("blurry")); !errors.As(err, &pe) || pe.Codes[0] != "blurry" {
		t.Fatalf("blurry reference: %v", err)
	}
	if info, err := c.CheckReference(context.Background(), []byte("fine")); err != nil || info.Keypoints != 2000 {
		t.Fatalf("%+v %v", info, err)
	}
	// an unreachable service is reported, never mistaken for a pass
	srv.Close()
	if _, err := c.Verify(context.Background(), []byte("probe"), nil, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("down: %v", err)
	}
	var nilc *Client
	if _, err := nilc.Verify(context.Background(), nil, nil, nil); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured: %v", err)
	}
	if New("") != nil {
		t.Fatal("empty URL must mean not configured")
	}
}

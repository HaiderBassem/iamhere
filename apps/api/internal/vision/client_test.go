package vision

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeService imitates apps/vision: it only knows references it has been sent
// to /v1/reference (even ones it complained about), and answers /v1/verify with
// 409 and the missing hashes otherwise.
func fakeService(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var mu sync.Mutex
	known := map[string]bool{}
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var in struct {
			Probe string `json:"probe"`
			Image string `json:"image"`
			Refs  []map[string]string
		}
		json.NewDecoder(r.Body).Decode(&in)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/reference":
			b, _ := base64.StdEncoding.DecodeString(in.Image)
			h := SHA256(b)
			known[hex.EncodeToString(h[:])] = true
			if string(b) == "blurry" {
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
				if len(ref) != 1 {
					t.Errorf("verify must carry hashes only: %v", ref)
				}
				if !known[ref["sha256"]] {
					missing = append(missing, ref["sha256"])
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
	refs := []Ref{ref("room-a", &loads), ref("room-b", &loads), ref("blurry", &loads)}

	res, err := c.Verify(context.Background(), []byte("probe"), nil, refs)
	if err != nil || !res.Matched || res.Codes[0] != "code" {
		t.Fatalf("%+v %v", res, err)
	}
	// verify (409), one upload per missing reference, verify again
	if loads.Load() != 3 || calls.Load() != 5 {
		t.Fatalf("first use: want 3 image loads in 5 calls, got %d loads, %d calls", loads.Load(), calls.Load())
	}
	// now cached by the service: one call, no image loads
	if _, err := c.Verify(context.Background(), []byte("probe"), nil, refs); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 3 || calls.Load() != 6 {
		t.Fatalf("cached: want no extra loads, got %d loads, %d calls", loads.Load(), calls.Load())
	}
}

// After a restart a whole class may check in at once: each missing reference is
// uploaded about once, not once per check-in.
func TestConcurrentCheckinsShareUploads(t *testing.T) {
	srv, _ := fakeService(t)
	c := New(srv.URL)
	var loads atomic.Int32
	release := make(chan struct{})
	slow := Ref{SHA256: SHA256([]byte("room-a")), Load: func(context.Context) ([]byte, error) {
		loads.Add(1)
		<-release
		return []byte("room-a"), nil
	}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Verify(context.Background(), []byte("probe"), nil, []Ref{slow}); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(200 * time.Millisecond) // let them all reach the upload
	close(release)
	wg.Wait()
	if n := loads.Load(); n > 2 {
		t.Fatalf("want the reference loaded about once, got %d", n)
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

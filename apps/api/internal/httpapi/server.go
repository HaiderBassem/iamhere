// Package httpapi is the thin HTTP layer: authentication, JSON, rate limits and
// routing. All business rules live in package app.
package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/iamhere/api/internal/app"
	"github.com/iamhere/api/internal/auth"
)

type Server struct {
	svc        *app.Service
	log        *slog.Logger
	ready      func(context.Context) error
	devAuth    bool
	trustProxy bool

	checkins *limiterSet[uuid.UUID] // per user
	creates  *limiterSet[uuid.UUID] // per user: sessions, orgs, invites, venues
	accepts  *limiterSet[uuid.UUID] // per user: invite redemption
	signins  *limiterSet[string]    // per IP: sign in
	signups  *limiterSet[string]    // per IP: new accounts
	mails    *limiterSet[string]    // per IP: anything that sends an email
	links    *limiterSet[string]    // per IP: following an emailed link (tokens are unguessable)
	sseEvery time.Duration
}

// PublicConfig is what the web app needs before the user is signed in.
type PublicConfig struct {
	Auth  string `json:"auth"`  // "local": built-in accounts
	Email bool   `json:"email"` // emailed links (reset password, confirm address) work
}

// Options are the optional parts of the HTTP layer.
type Options struct {
	// CORSOrigins lists exact browser origins that may call the API cross-origin
	// (empty = same-origin only).
	CORSOrigins []string
	// WebDir, when set, serves the web app from this directory (development and
	// single-binary setups). In production Caddy serves it instead.
	WebDir string
	// DevAuth also accepts "dev:<sub>:<name>" bearer tokens (tests and local
	// scripts; never in production).
	DevAuth bool
	// TrustProxy takes the client address from the last X-Forwarded-For entry
	// (set when the API is only reachable through the reverse proxy).
	TrustProxy bool
}

// New builds the HTTP handler.
func New(svc *app.Service, log *slog.Logger, ready func(context.Context) error, opt Options) http.Handler {
	s := &Server{
		svc: svc, log: log, ready: ready, devAuth: opt.DevAuth, trustProxy: opt.TrustProxy,
		checkins: newLimiterSet[uuid.UUID](rate.Every(3*time.Second), 10), // ~20/min sustained, burst 10
		creates:  newLimiterSet[uuid.UUID](rate.Every(6*time.Second), 10),
		accepts:  newLimiterSet[uuid.UUID](rate.Every(6*time.Second), 10),
		// a whole class may share one IP (school NAT) and sign up at once; guessing
		// is limited per account (app.maxFailedSignIns), not just per IP
		signins:  newLimiterSet[string](rate.Every(2*time.Second), 60),
		signups:  newLimiterSet[string](rate.Every(2*time.Second), 60),
		mails:    newLimiterSet[string](rate.Every(30*time.Second), 10),
		links:    newLimiterSet[string](rate.Every(2*time.Second), 30),
		sseEvery: 2 * time.Second,
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID, s.recoverer, s.accessLog, secureHeaders, cors(opt.CORSOrigins))

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ready(r.Context()); err != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	r.Route("/v1", func(r chi.Router) {
		r.Get("/public-config", func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, 200, PublicConfig{Auth: "local", Email: s.svc.MailEnabled()})
		})

		// accounts: no session needed
		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(20 * time.Second))
			r.Post("/auth/signup", s.signUp)
			r.Post("/auth/signin", s.signIn)
			r.Post("/auth/password/forgot", s.forgotPassword)
			r.Post("/auth/password/reset", s.resetPassword)
			r.Post("/auth/email/confirm", s.confirmEmail)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.authenticate)

			// The streaming endpoint is registered outside the request-timeout group.
			r.Get("/orgs/{org}/sessions/{sid}/events", s.h(s.events))

			r.Group(func(r chi.Router) {
				r.Use(middleware.Timeout(20 * time.Second))

				r.Get("/auth/account", s.h(s.account))
				r.Post("/auth/signout", s.h(s.signOut))
				r.Post("/auth/signout-others", s.h(s.signOutOthers))
				r.Post("/auth/password", s.h(s.changePassword))
				r.Post("/auth/email/resend", s.h(s.resendConfirmation))
				r.Get("/me", s.h(s.getMe))
				r.Patch("/me", s.h(s.patchMe))
				r.Delete("/me", s.h(s.deleteMe))
				r.Get("/me/attendance", s.h(s.myHistory))
				r.Post("/checkins/scan", s.h(s.scan))
				r.Post("/checkins", s.h(s.checkIn))
				r.Post("/invites/accept", s.h(s.acceptInvite))

				r.Get("/orgs", s.h(s.listOrgs))
				r.Post("/orgs", s.h(s.createOrg))
				r.Route("/orgs/{org}", func(r chi.Router) {
					r.Get("/settings", s.h(s.getSettings))
					r.Put("/settings", s.h(s.putSettings))
					r.Get("/members", s.h(s.listMembers))
					r.Put("/members/{uid}", s.h(s.setMember))
					r.Delete("/members/{uid}", s.h(s.removeMember))
					r.Get("/invites", s.h(s.listInvites))
					r.Post("/invites", s.h(s.createInvite))
					r.Delete("/invites/{iid}", s.h(s.revokeInvite))
					r.Get("/venues", s.h(s.listVenues))
					r.Post("/venues", s.h(s.createVenue))
					r.Delete("/venues/{vid}", s.h(s.deleteVenue))
					r.Get("/venues/{vid}/references", s.h(s.listReferences))
					r.Post("/venues/{vid}/references", s.h(s.addReference))
					r.Delete("/venues/{vid}/references/{rid}", s.h(s.deleteReference))
					r.Post("/venues/{vid}/test", s.h(s.testPlace))
					r.Get("/venues/{vid}/photo-stats", s.h(s.photoStats))
					r.Delete("/venues/{vid}/learned", s.h(s.forgetLearned))
					r.Get("/references/{rid}/image", s.h(s.referenceImage))

					r.Get("/sessions", s.h(s.listSessions))
					r.Post("/sessions", s.h(s.createSession))
					r.Get("/sessions/{sid}", s.h(s.getSession))
					r.Get("/sessions/{sid}/qr", s.h(s.sessionQR))
					r.Post("/sessions/{sid}/close", s.h(s.closeSession))
					r.Get("/sessions/{sid}/references", s.h(s.listSessionRefs))
					r.Post("/sessions/{sid}/references", s.h(s.addSessionRef))
					r.Delete("/sessions/{sid}/references/{rid}", s.h(s.deleteSessionRef))
					r.Get("/sessions/{sid}/attendance", s.h(s.listAttendance))
					r.Get("/sessions/{sid}/attendance.csv", s.h(s.exportCSV))
					r.Post("/sessions/{sid}/attendance/manual", s.h(s.manual))
					r.Post("/attendance/{rid}/review", s.h(s.review))
				})
			})
		})
	})

	if opt.WebDir != "" {
		r.NotFound(webApp(opt.WebDir, csp))
	}
	return r
}

// ---- middleware -----------------------------------------------------------

type ctxKey struct{}
type sessionKey struct{}

func userFrom(ctx context.Context) app.User { return ctx.Value(ctxKey{}).(app.User) }

// sessionFrom is the signed-in device's session (uuid.Nil for dev tokens).
func sessionFrom(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(sessionKey{}).(uuid.UUID)
	return id
}

// authenticate uses bearer tokens in the Authorization header (never cookies),
// so the API is not exposed to CSRF.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || tok == "" {
			s.writeErr(w, r, app.UnauthorizedErr())
			return
		}
		var u app.User
		var sid uuid.UUID
		var err error
		if s.devAuth && strings.HasPrefix(tok, "dev:") {
			var id auth.Identity
			if id, err = (auth.Dev{}).Verify(r.Context(), tok); err != nil {
				err = app.UnauthorizedErr()
			} else {
				u, err = s.svc.EnsureUser(r.Context(), id)
			}
		} else {
			u, sid, err = s.svc.SessionUser(r.Context(), tok)
		}
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		ctx := context.WithValue(context.WithValue(r.Context(), ctxKey{}, u), sessionKey{}, sid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// clientIP is the address rate limits are keyed on. Behind the reverse proxy
// (TrustProxy) that is the last X-Forwarded-For hop, which the proxy wrote;
// earlier entries come from the client and are ignored.
func (s *Server) clientIP(r *http.Request) string {
	if s.trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// cors answers browser preflights and tags responses for allow-listed origins.
// Authentication is a bearer header (no cookies), so credentials are never
// shared with the page and an exact-origin allow-list is sufficient.
func cors(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		allowed[o] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(allowed) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			// The response varies by Origin whether or not one was sent, so a
			// shared cache can never replay one origin's headers to another.
			w.Header().Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}
			if !allowed[origin] {
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Access-Control-Expose-Headers", "Retry-After, Content-Disposition")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(c int) { w.status = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// accessLog never logs query strings or headers (tokens, QR values).
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		s.log.Info("request", "method", r.Method, "route", chi.RouteContext(r.Context()).RoutePattern(),
			"status", sw.status, "ms", time.Since(start).Milliseconds(), "id", middleware.GetReqID(r.Context()))
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				panic(v) // deliberate abort (e.g. truncated CSV): let net/http reset the connection
			}
			s.log.Error("panic", "value", fmt.Sprint(v), "id", middleware.GetReqID(r.Context()))
			writeJSON(w, 500, errBody{"internal", "Something went wrong."})
		}()
		next.ServeHTTP(w, r)
	})
}

type limiterSet[K comparable] struct {
	mu    sync.Mutex
	m     map[K]*entry
	limit rate.Limit
	burst int
}
type entry struct {
	l    *rate.Limiter
	seen time.Time
}

func newLimiterSet[K comparable](l rate.Limit, burst int) *limiterSet[K] {
	return &limiterSet[K]{m: map[K]*entry{}, limit: l, burst: burst}
}

func (s *limiterSet[K]) allow(id K) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if len(s.m) > 10000 { // bound memory: drop idle entries
		for k, e := range s.m {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(s.m, k)
			}
		}
	}
	e, ok := s.m[id]
	if !ok {
		e = &entry{l: rate.NewLimiter(s.limit, s.burst)}
		s.m[id] = e
	}
	e.seen = now
	return e.l.Allow()
}

// ---- helpers --------------------------------------------------------------

type errBody struct {
	Code    string `json:"error"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *app.Error
	if errors.As(err, &ae) {
		writeJSON(w, ae.Status, errBody{ae.Code, ae.Message})
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	s.log.Error("request failed", "err", err.Error(), "id", middleware.GetReqID(r.Context()))
	writeJSON(w, 500, errBody{"internal", "Something went wrong."})
}

type handler func(w http.ResponseWriter, r *http.Request, u app.User) error

func (s *Server) h(fn handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r, userFrom(r.Context())); err != nil {
			s.writeErr(w, r, err)
		}
	}
}

const (
	maxJSONBody  = 64 << 10
	maxPhotoBody = 3 << 20 // a photo (<= ~2 MB) travels as base64 inside JSON
)

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeMax(w, r, dst, maxJSONBody)
}

func decodeMax(w http.ResponseWriter, r *http.Request, dst any, max int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil || dec.More() {
		return app.BadRequest("Malformed request body.")
	}
	return nil
}

func uuidParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, app.ErrNotFound
	}
	return id, nil
}

func limitParam(r *http.Request, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		return n
	}
	return def
}

func clampLimit(n, def, max int) int {
	if n < 1 || n > max {
		return def
	}
	return n
}

func encodeCursor(t time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(c string) (time.Time, uuid.UUID, bool, error) {
	if c == "" {
		return time.Time{}, uuid.Nil, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	ts, ids, ok := strings.Cut(string(raw), "|")
	if err != nil || !ok {
		return time.Time{}, uuid.Nil, false, app.BadRequest("Invalid cursor.")
	}
	t, err1 := time.Parse(time.RFC3339Nano, ts)
	id, err2 := uuid.Parse(ids)
	if err1 != nil || err2 != nil {
		return time.Time{}, uuid.Nil, false, app.BadRequest("Invalid cursor.")
	}
	return t, id, true, nil
}

// ---- handlers: me ---------------------------------------------------------

func (s *Server) getMe(w http.ResponseWriter, _ *http.Request, u app.User) error {
	writeJSON(w, 200, u)
	return nil
}

func (s *Server) patchMe(w http.ResponseWriter, r *http.Request, u app.User) error {
	var in struct {
		FullName *string `json:"full_name"`
		Phone    *string `json:"phone"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	nu, err := s.svc.UpdateProfile(r.Context(), u, in.FullName, in.Phone)
	if err != nil {
		return err
	}
	writeJSON(w, 200, nu)
	return nil
}

func (s *Server) deleteMe(w http.ResponseWriter, r *http.Request, u app.User) error {
	if err := s.svc.DeleteAccount(r.Context(), u); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) myHistory(w http.ResponseWriter, r *http.Request, u app.User) error {
	rows, err := s.svc.MyHistory(r.Context(), u, limitParam(r, 50))
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": rows})
	return nil
}

// ---- handlers: check-in ---------------------------------------------------

// decodePhoto accepts raw base64 or a data: URL, as produced by canvas.toDataURL.
func decodePhoto(v string) ([]byte, error) {
	if v == "" {
		return nil, nil
	}
	if k := strings.Index(v, ","); strings.HasPrefix(v, "data:") && k > 0 {
		v = v[k+1:]
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil {
		return nil, app.BadRequest("Malformed photo.")
	}
	return b, nil
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request, u app.User) error {
	if !s.checkins.allow(u.ID) {
		w.Header().Set("Retry-After", "5")
		return app.TooMany()
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	res, err := s.svc.Scan(r.Context(), u, in.Token)
	if err != nil {
		return err
	}
	writeJSON(w, 200, res)
	return nil
}

func (s *Server) checkIn(w http.ResponseWriter, r *http.Request, u app.User) error {
	if !s.checkins.allow(u.ID) {
		w.Header().Set("Retry-After", "5")
		return app.TooMany()
	}
	var in struct {
		Token  string        `json:"token"`
		Ticket string        `json:"ticket"`
		GPS    *app.GPSInput `json:"gps"`
		Photo  string        `json:"photo"`
		// where the browser saw the QR code in the photo (only a hint)
		PhotoHint *[4][2]float64 `json:"photo_hint"`
	}
	if err := decodeMax(w, r, &in, maxPhotoBody); err != nil {
		return err
	}
	photo, err := decodePhoto(in.Photo)
	if err != nil {
		return err
	}
	res, err := s.svc.CheckIn(r.Context(), u, app.CheckInInput{Token: in.Token, Ticket: in.Ticket, GPS: in.GPS, Photo: photo, PhotoHint: in.PhotoHint})
	if err != nil {
		return err
	}
	writeJSON(w, 200, res)
	return nil
}

// ---- handlers: organizations ---------------------------------------------

func (s *Server) listOrgs(w http.ResponseWriter, r *http.Request, u app.User) error {
	orgs, err := s.svc.ListOrgs(r.Context(), u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": orgs})
	return nil
}

func (s *Server) createOrg(w http.ResponseWriter, r *http.Request, u app.User) error {
	if !s.creates.allow(u.ID) {
		return app.TooMany()
	}
	var in struct {
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Country  string `json:"country"`
		Timezone string `json:"timezone"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	o, err := s.svc.CreateOrg(r.Context(), u, in.Name, in.Slug, in.Country, in.Timezone)
	if err != nil {
		return err
	}
	writeJSON(w, 201, o)
	return nil
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	st, err := s.svc.GetSettings(r.Context(), u, org)
	if err != nil {
		return err
	}
	writeJSON(w, 200, st)
	return nil
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	var in app.Settings
	if err := decode(w, r, &in); err != nil {
		return err
	}
	st, err := s.svc.UpdateSettings(r.Context(), u, org, in)
	if err != nil {
		return err
	}
	writeJSON(w, 200, st)
	return nil
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	ms, err := s.svc.ListMembers(r.Context(), u, org)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": ms})
	return nil
}

func (s *Server) setMember(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	uid, err := uuidParam(r, "uid")
	if err != nil {
		return err
	}
	var in struct {
		Role app.Role `json:"role"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := s.svc.SetMember(r.Context(), u, org, uid, in.Role); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	uid, err := uuidParam(r, "uid")
	if err != nil {
		return err
	}
	if err := s.svc.RemoveMember(r.Context(), u, org, uid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- handlers: invites & venues -------------------------------------------

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	if !s.creates.allow(u.ID) {
		return app.TooMany()
	}
	var in struct {
		Role           app.Role `json:"role"`
		ExpiresInHours int      `json:"expires_in_hours"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if in.ExpiresInHours < 0 || in.ExpiresInHours > 24*365 {
		return app.BadRequest("Expiry must be between 1 hour and 30 days.")
	}
	inv, err := s.svc.CreateInvite(r.Context(), u, org, in.Role, time.Duration(in.ExpiresInHours)*time.Hour)
	if err != nil {
		return err
	}
	writeJSON(w, 201, inv)
	return nil
}

func (s *Server) listInvites(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	items, err := s.svc.ListInvites(r.Context(), u, org)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	iid, err := uuidParam(r, "iid")
	if err != nil {
		return err
	}
	if err := s.svc.RevokeInvite(r.Context(), u, org, iid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request, u app.User) error {
	if !s.accepts.allow(u.ID) {
		w.Header().Set("Retry-After", "6")
		return app.TooMany()
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	o, err := s.svc.AcceptInvite(r.Context(), u, in.Token)
	if err != nil {
		return err
	}
	writeJSON(w, 200, o)
	return nil
}

func (s *Server) createVenue(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	if !s.creates.allow(u.ID) {
		return app.TooMany()
	}
	var in struct {
		Name    string  `json:"name"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
		RadiusM int     `json:"radius_m"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	v, err := s.svc.CreateVenue(r.Context(), u, org, in.Name, in.Lat, in.Lon, in.RadiusM)
	if err != nil {
		return err
	}
	writeJSON(w, 201, v)
	return nil
}

func (s *Server) listVenues(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	items, err := s.svc.ListVenues(r.Context(), u, org)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (s *Server) deleteVenue(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	vid, err := uuidParam(r, "vid")
	if err != nil {
		return err
	}
	if err := s.svc.DeleteVenue(r.Context(), u, org, vid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func venueParams(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	org, err := uuidParam(r, "org")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	vid, err := uuidParam(r, "vid")
	return org, vid, err
}

func (s *Server) addReference(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, vid, err := venueParams(r)
	if err != nil {
		return err
	}
	if !s.creates.allow(u.ID) {
		return app.TooMany()
	}
	photo, err := photoBody(w, r)
	if err != nil {
		return err
	}
	ref, err := s.svc.AddVenueReference(r.Context(), u, org, vid, photo)
	if err != nil {
		return err
	}
	writeJSON(w, 201, ref)
	return nil
}

func (s *Server) listReferences(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, vid, err := venueParams(r)
	if err != nil {
		return err
	}
	items, err := s.svc.ListVenueReferences(r.Context(), u, org, vid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (s *Server) deleteReference(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, vid, err := venueParams(r)
	if err != nil {
		return err
	}
	rid, err := uuidParam(r, "rid")
	if err != nil {
		return err
	}
	if err := s.svc.DeleteVenueReference(r.Context(), u, org, vid, rid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// photoBody reads {"photo": "<base64 or data URL>"}.
func photoBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	var in struct {
		Photo string `json:"photo"`
	}
	if err := decodeMax(w, r, &in, maxPhotoBody); err != nil {
		return nil, err
	}
	return decodePhoto(in.Photo)
}

func (s *Server) photoStats(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, vid, err := venueParams(r)
	if err != nil {
		return err
	}
	st, err := s.svc.VenuePhotoStats(r.Context(), u, org, vid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, st)
	return nil
}

func (s *Server) forgetLearned(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, vid, err := venueParams(r)
	if err != nil {
		return err
	}
	if err := s.svc.ForgetLearned(r.Context(), u, org, vid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) testPlace(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, vid, err := venueParams(r)
	if err != nil {
		return err
	}
	if !s.creates.allow(u.ID) {
		return app.TooMany()
	}
	photo, err := photoBody(w, r)
	if err != nil {
		return err
	}
	res, err := s.svc.TestVenuePhoto(r.Context(), u, org, vid, photo)
	if err != nil {
		return err
	}
	writeJSON(w, 200, res)
	return nil
}

func (s *Server) referenceImage(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	rid, err := uuidParam(r, "rid")
	if err != nil {
		return err
	}
	img, err := s.svc.ReferenceImage(r.Context(), u, org, rid)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", http.DetectContentType(img))
	w.Header().Set("Cache-Control", "private, max-age=3600") // immutable per id
	_, _ = w.Write(img)
	return nil
}

func (s *Server) listSessionRefs(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	items, err := s.svc.ListSessionReferences(r.Context(), u, org, sid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func (s *Server) addSessionRef(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	if !s.creates.allow(u.ID) {
		return app.TooMany()
	}
	photo, err := photoBody(w, r)
	if err != nil {
		return err
	}
	ref, err := s.svc.AddSessionReference(r.Context(), u, org, sid, photo)
	if err != nil {
		return err
	}
	writeJSON(w, 201, ref)
	return nil
}

func (s *Server) deleteSessionRef(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	rid, err := uuidParam(r, "rid")
	if err != nil {
		return err
	}
	if err := s.svc.DeleteSessionReference(r.Context(), u, org, sid, rid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---- handlers: sessions ---------------------------------------------------

func (s *Server) createSession(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	if !s.creates.allow(u.ID) {
		return app.TooMany()
	}
	var in struct {
		Title     string      `json:"title"`
		Anchor    *app.Anchor `json:"anchor"`
		VenueID   *uuid.UUID  `json:"venue_id"`
		DurationS int         `json:"duration_s"`
		Mode      string      `json:"mode"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	sess, err := s.svc.CreateSession(r.Context(), u, org, app.CreateSessionInput{
		Title: in.Title, Anchor: in.Anchor, VenueID: in.VenueID, DurationS: in.DurationS, Mode: in.Mode, IdempotencyKey: r.Header.Get("Idempotency-Key"),
	})
	if err != nil {
		return err
	}
	writeJSON(w, 201, sess)
	return nil
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	t, id, has, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return err
	}
	var cur *app.SessionCursor
	if has {
		cur = &app.SessionCursor{StartsAt: t, ID: id}
	}
	limit := limitParam(r, 25)
	items, err := s.svc.ListSessions(r.Context(), u, org, limit, cur)
	if err != nil {
		return err
	}
	out := map[string]any{"items": items}
	if len(items) > 0 && len(items) == clampLimit(limit, 25, 100) {
		last := items[len(items)-1]
		out["next_cursor"] = encodeCursor(last.StartsAt, last.ID)
	}
	writeJSON(w, 200, out)
	return nil
}

func orgSession(r *http.Request) (uuid.UUID, uuid.UUID, error) {
	org, err := uuidParam(r, "org")
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	sid, err := uuidParam(r, "sid")
	return org, sid, err
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	sess, err := s.svc.GetSession(r.Context(), u, org, sid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, sess)
	return nil
}

func (s *Server) sessionQR(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	q, err := s.svc.SessionQR(r.Context(), u, org, sid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, q)
	return nil
}

func (s *Server) closeSession(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	sess, err := s.svc.CloseSession(r.Context(), u, org, sid)
	if err != nil {
		return err
	}
	writeJSON(w, 200, sess)
	return nil
}

func (s *Server) listAttendance(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	t, id, has, err := decodeCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return err
	}
	var cur *app.AttendanceCursor
	if has {
		cur = &app.AttendanceCursor{At: t, ID: id}
	}
	limit := limitParam(r, 100)
	rows, err := s.svc.ListAttendance(r.Context(), u, org, sid, limit, cur)
	if err != nil {
		return err
	}
	out := map[string]any{"items": rows}
	if len(rows) > 0 && len(rows) == clampLimit(limit, 100, 500) {
		last := rows[len(rows)-1]
		out["next_cursor"] = encodeCursor(last.CheckedInAt, last.ID)
	}
	writeJSON(w, 200, out)
	return nil
}

// csvResponse sets download headers lazily on the first byte, so a failure
// before any output (authorization, missing session) is still a clean JSON error.
type csvResponse struct {
	w       http.ResponseWriter
	started bool
}

func (c *csvResponse) Write(p []byte) (int, error) {
	if !c.started {
		c.started = true
		c.w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		c.w.Header().Set("Content-Disposition", `attachment; filename="attendance.csv"`)
	}
	return c.w.Write(p)
}

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	cw := &csvResponse{w: w}
	if err := s.svc.ExportCSV(r.Context(), u, org, sid, cw); err != nil {
		if !cw.started {
			return err
		}
		s.log.Error("csv export aborted", "err", err.Error())
		panic(http.ErrAbortHandler) // truncated download must look broken, not complete
	}
	return nil
}

func (s *Server) manual(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	var in struct {
		UserID uuid.UUID `json:"user_id"`
		Reason string    `json:"reason"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	id, err := s.svc.ManualAttendance(r.Context(), u, org, sid, in.UserID, in.Reason)
	if err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": id})
	return nil
}

func (s *Server) review(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, err := uuidParam(r, "org")
	if err != nil {
		return err
	}
	rid, err := uuidParam(r, "rid")
	if err != nil {
		return err
	}
	var in struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := s.svc.ReviewRecord(r.Context(), u, org, rid, in.Status, in.Reason); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// events streams live counts for the organizer screen as Server-Sent Events.
// The client uses fetch streaming (EventSource cannot send Authorization).
// Every tick re-checks authorization, so removing a member ends their stream.
// Counts are idempotent snapshots, so a reconnect needs no replay.
func (s *Server) events(w http.ResponseWriter, r *http.Request, u app.User) error {
	org, sid, err := orgSession(r)
	if err != nil {
		return err
	}
	last, err := s.svc.Counts(r.Context(), u, org, sid)
	if err != nil {
		return err
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming unsupported")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(c app.LiveCounts) {
		b, _ := json.Marshal(c)
		fmt.Fprintf(w, "event: counts\ndata: %s\n\n", b)
		fl.Flush()
	}
	send(last)

	t := time.NewTicker(s.sseEvery)
	defer t.Stop()
	deadline := time.NewTimer(2 * time.Hour) // bound connection lifetime; the client reconnects
	defer deadline.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-deadline.C:
			return nil
		case <-t.C:
			c, err := s.svc.Counts(r.Context(), u, org, sid)
			if err != nil {
				return nil // access revoked or DB trouble: close; the reconnect gets a proper error
			}
			if c != last {
				send(c)
				last = c
			} else {
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			}
			if c.Status != "active" {
				return nil
			}
		}
	}
}

package httpapi

import (
	"net/http"

	"github.com/iamhere/api/internal/app"
)

// ---- handlers: accounts (no session needed) -----------------------------------

// public wraps a handler that runs without a signed-in user.
func (s *Server) public(w http.ResponseWriter, r *http.Request, lim *limiterSet[string], fn func() (int, any, error)) {
	if !lim.allow(s.clientIP(r)) {
		w.Header().Set("Retry-After", "30")
		s.writeErr(w, r, app.TooMany())
		return
	}
	status, body, err := fn()
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if body == nil {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, body)
}

func (s *Server) signUp(w http.ResponseWriter, r *http.Request) {
	s.public(w, r, s.signups, func() (int, any, error) {
		var in struct {
			Name     string `json:"name"`
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		res, err := s.svc.SignUp(r.Context(), in.Name, in.Email, in.Password, r.UserAgent())
		return http.StatusCreated, res, err
	})
}

func (s *Server) signIn(w http.ResponseWriter, r *http.Request) {
	s.public(w, r, s.signins, func() (int, any, error) {
		var in struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		res, err := s.svc.SignIn(r.Context(), in.Email, in.Password, r.UserAgent())
		return http.StatusOK, res, err
	})
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	s.public(w, r, s.mails, func() (int, any, error) {
		var in struct {
			Email string `json:"email"`
		}
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		return http.StatusAccepted, nil, s.svc.RequestPasswordReset(r.Context(), in.Email)
	})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	s.public(w, r, s.links, func() (int, any, error) {
		var in struct {
			Token    string `json:"token"`
			Password string `json:"password"`
		}
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		res, err := s.svc.ResetPassword(r.Context(), in.Token, in.Password, r.UserAgent())
		return http.StatusOK, res, err
	})
}

func (s *Server) confirmEmail(w http.ResponseWriter, r *http.Request) {
	s.public(w, r, s.links, func() (int, any, error) {
		var in struct {
			Token string `json:"token"`
		}
		if err := decode(w, r, &in); err != nil {
			return 0, nil, err
		}
		return http.StatusNoContent, nil, s.svc.ConfirmEmail(r.Context(), in.Token)
	})
}

// ---- handlers: accounts (signed in) --------------------------------------------

func (s *Server) account(w http.ResponseWriter, r *http.Request, u app.User) error {
	a, err := s.svc.AccountOf(r.Context(), u)
	if err != nil {
		return err
	}
	writeJSON(w, 200, a)
	return nil
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request, _ app.User) error {
	if err := s.svc.SignOut(r.Context(), sessionFrom(r.Context())); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) signOutOthers(w http.ResponseWriter, r *http.Request, u app.User) error {
	n, err := s.svc.SignOutOthers(r.Context(), u, sessionFrom(r.Context()))
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]int64{"signed_out": n})
	return nil
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, u app.User) error {
	if !s.signins.allow(s.clientIP(r)) {
		return app.TooMany()
	}
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(w, r, &in); err != nil {
		return err
	}
	if err := s.svc.ChangePassword(r.Context(), u, sessionFrom(r.Context()), in.Current, in.New); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) resendConfirmation(w http.ResponseWriter, r *http.Request, u app.User) error {
	if !s.mails.allow(s.clientIP(r)) {
		return app.TooMany()
	}
	if err := s.svc.ResendConfirmation(r.Context(), u); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

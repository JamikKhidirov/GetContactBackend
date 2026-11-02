package httptransport

import (
	"net/http"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/service"
)

type registerRequest struct {
	Phone       string `json:"phone"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.auth.Register(r.Context(), service.RegisterInput{
		Phone: req.Phone, Password: req.Password, DisplayName: req.DisplayName, Email: req.Email,
	}, metaFrom(r))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusCreated, res)
}

type loginRequest struct {
	Phone    string `json:"phone"`
	Password string `json:"password"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.auth.Login(r.Context(), req.Phone, req.Password, metaFrom(r))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, res)
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.auth.Refresh(r.Context(), req.RefreshToken, metaFrom(r))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, res)
}

type logoutRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	// Body is optional: an access token alone is enough to revoke the JWT.
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if err := a.auth.Logout(r.Context(), tokenIDFrom(r.Context()), tokenExpiryFrom(r.Context()), req.RefreshToken); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFrom(r.Context())
	if !ok {
		writeError(w, r, a.log, apperr.Unauthorized(""))
		return
	}
	user, err := a.auth.Me(r.Context(), uid)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, user.Public())
}

func metaFrom(r *http.Request) service.Meta {
	return service.Meta{
		IP:        clientIP(r),
		UserAgent: r.UserAgent(),
	}
}

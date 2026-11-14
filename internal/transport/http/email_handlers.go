package httptransport

import (
	"net/http"
	"strings"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/service"
	"github.com/google/uuid"
)

type emailCodeRequest struct {
	Email   string      `json:"email"`
	Purpose string      `json:"purpose"`
	Device  *deviceInfo `json:"device,omitempty"`
}

// requestEmailCode sends a one-time code. It is public for signup/login and
// optionally authenticated for verifying the address of an existing account.
func (a *API) requestEmailCode(w http.ResponseWriter, r *http.Request) {
	var req emailCodeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var userID *uuid.UUID
	if uid, ok := userIDFrom(r.Context()); ok {
		userID = &uid
	}
	in := service.RequestCodeInput{
		Email: req.Email, Purpose: req.Purpose, Meta: a.metaFrom(r, req.Device),
	}
	if userID != nil {
		in.UserID = userID
	}
	res, err := a.email.RequestCode(r.Context(), in)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusAccepted, res)
}

type emailLoginRequest struct {
	Email  string      `json:"email"`
	Code   string      `json:"code"`
	Device *deviceInfo `json:"device,omitempty"`
}

// emailLogin exchanges a code for a token pair.
func (a *API) emailLogin(w http.ResponseWriter, r *http.Request) {
	var req emailLoginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.email.LoginWithCode(r.Context(), req.Email, req.Code, a.metaFrom(r, req.Device))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, res)
}

type emailVerifyRequest struct {
	Email  string      `json:"email"`
	Device *deviceInfo `json:"device,omitempty"`
}

// requestEmailVerification sends a code to the signed-in user's address.
func (a *API) requestEmailVerification(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFrom(r.Context())
	if !ok {
		writeError(w, r, a.log, apperr.Unauthorized(""))
		return
	}
	var req emailVerifyRequest
	if err := decodeOptionalJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		user, err := a.auth.Me(r.Context(), uid)
		if err != nil {
			writeError(w, r, a.log, err)
			return
		}
		email = user.Email
	}
	if email == "" {
		writeError(w, r, a.log, apperr.Invalid(map[string]string{"email": "email is required"}))
		return
	}
	res, err := a.email.RequestCode(r.Context(), service.RequestCodeInput{
		Email: email, Purpose: service.EmailPurposeVerifyEmail, UserID: &uid,
		Meta: a.metaFrom(r, req.Device),
	})
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusAccepted, res)
}

type emailVerifyConfirmRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// confirmEmailVerification binds a proven address to the current account.
func (a *API) confirmEmailVerification(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFrom(r.Context())
	if !ok {
		writeError(w, r, a.log, apperr.Unauthorized(""))
		return
	}
	var req emailVerifyConfirmRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		user, err := a.auth.Me(r.Context(), uid)
		if err != nil {
			writeError(w, r, a.log, err)
			return
		}
		email = user.Email
	}
	user, err := a.email.ConfirmEmailVerification(r.Context(), uid, email, req.Code)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, user)
}

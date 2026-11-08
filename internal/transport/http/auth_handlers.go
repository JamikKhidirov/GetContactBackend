package httptransport

import (
	"net/http"
	"strings"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// deviceInfo is the optional, consent-based device block a mobile client may
// send in the JSON body. It is equivalent to the X-Device-* / X-OS-* / X-App-*
// headers and is never required. The API does not read contacts, photos, files,
// messages or any other data from the device.
type deviceInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Model        string `json:"model"`
	Manufacturer string `json:"manufacturer"`
	OS           string `json:"os"`
	OSVersion    string `json:"os_version"`
	AppVersion   string `json:"app_version"`
	AppBuild     string `json:"app_build"`
	Client       string `json:"client"`
	Locale       string `json:"locale"`
	Timezone     string `json:"timezone"`
	NetworkType  string `json:"network_type"`
}

type registerRequest struct {
	Phone       string      `json:"phone"`
	Password    string      `json:"password"`
	DisplayName string      `json:"display_name"`
	Email       string      `json:"email"`
	EmailCode   string      `json:"email_code,omitempty"`
	Device      *deviceInfo `json:"device,omitempty"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.auth.Register(r.Context(), service.RegisterInput{
		Phone: req.Phone, Password: req.Password, DisplayName: req.DisplayName,
		Email: req.Email, EmailCode: req.EmailCode,
	}, metaFrom(r, req.Device))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusCreated, res)
}

type loginRequest struct {
	Phone    string      `json:"phone"`
	Password string      `json:"password"`
	Device   *deviceInfo `json:"device,omitempty"`
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.auth.Login(r.Context(), req.Phone, req.Password, metaFrom(r, req.Device))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, res)
}

type refreshRequest struct {
	RefreshToken string      `json:"refresh_token"`
	Device       *deviceInfo `json:"device,omitempty"`
}

func (a *API) refresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.auth.Refresh(r.Context(), req.RefreshToken, metaFrom(r, req.Device))
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
	err := a.auth.Logout(r.Context(), tokenIDFrom(r.Context()), sessionIDFrom(r.Context()),
		tokenExpiryFrom(r.Context()), req.RefreshToken)
	if err != nil {
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

// listSessions returns every active device session for the caller.
func (a *API) listSessions(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFrom(r.Context())
	if !ok {
		writeError(w, r, a.log, apperr.Unauthorized(""))
		return
	}
	sessions, err := a.auth.Sessions(r.Context(), uid, sessionIDFrom(r.Context()))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"sessions": sessions, "total": len(sessions)})
}

// revokeSession ends one of the caller's own device sessions.
func (a *API) revokeSession(w http.ResponseWriter, r *http.Request) {
	uid, ok := userIDFrom(r.Context())
	if !ok {
		writeError(w, r, a.log, apperr.Unauthorized(""))
		return
	}
	sid, err := uuid.Parse(strings.TrimSpace(chi.URLParam(r, "sessionID")))
	if err != nil {
		writeError(w, r, a.log, apperr.Invalid(map[string]string{"session_id": "must be a valid UUID"}))
		return
	}
	if err := a.auth.RevokeSession(r.Context(), uid, sid, sessionIDFrom(r.Context())); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

// metaFrom merges the requested metadata sources in priority order:
// proxy-aware IP, then the X-Device-* headers, then the optional JSON device
// block. Later sources only fill empty values.
func metaFrom(r *http.Request, dev *deviceInfo) service.Meta {
	meta := service.Meta{
		IP:           clientIP(r),
		UserAgent:    r.UserAgent(),
		DeviceID:     r.Header.Get("X-Device-Id"),
		DeviceName:   r.Header.Get("X-Device-Name"),
		DeviceModel:  r.Header.Get("X-Device-Model"),
		Manufacturer: r.Header.Get("X-Device-Manufacturer"),
		OS:           r.Header.Get("X-OS"),
		OSVersion:    r.Header.Get("X-OS-Version"),
		AppVersion:   r.Header.Get("X-App-Version"),
		AppBuild:     r.Header.Get("X-App-Build"),
		Client:       r.Header.Get("X-Client"),
		Locale:       r.Header.Get("X-Locale"),
		Timezone:     r.Header.Get("X-Timezone"),
		NetworkType:  r.Header.Get("X-Network-Type"),
	}
	if dev != nil {
		fill(&meta.DeviceID, dev.ID)
		fill(&meta.DeviceName, dev.Name)
		fill(&meta.DeviceModel, dev.Model)
		fill(&meta.Manufacturer, dev.Manufacturer)
		fill(&meta.OS, dev.OS)
		fill(&meta.OSVersion, dev.OSVersion)
		fill(&meta.AppVersion, dev.AppVersion)
		fill(&meta.AppBuild, dev.AppBuild)
		fill(&meta.Client, dev.Client)
		fill(&meta.Locale, dev.Locale)
		fill(&meta.Timezone, dev.Timezone)
		fill(&meta.NetworkType, dev.NetworkType)
	}
	return meta
}

func fill(dst *string, value string) {
	if *dst == "" {
		*dst = strings.TrimSpace(value)
	}
}

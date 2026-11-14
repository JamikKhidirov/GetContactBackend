package httptransport

import (
	"net/http"

	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/platform"
	"github.com/getcontact/backend/internal/service"
)

// publicProfile is the only view of another member's account exposed by the
// API. Role, block state, verification flags and the raw phone stay private.
type publicProfile struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Phone       string `json:"phone"`
}

func (a *API) getUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	user, err := a.users.Get(r.Context(), id)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	caller, _ := userIDFrom(r.Context())
	if caller != id && roleFrom(r.Context()) != domain.RoleAdmin {
		writeData(w, http.StatusOK, publicProfile{
			ID:          user.ID.String(),
			DisplayName: user.DisplayName,
			Phone:       platform.MaskPhone(user.Phone),
		})
		return
	}
	writeData(w, http.StatusOK, user)
}

func (a *API) updateMe(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	var req service.UpdateMeInput
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	user, err := a.users.UpdateMe(r.Context(), uid, req)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, user)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	var req changePasswordRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if err := a.users.ChangePassword(r.Context(), uid, req.CurrentPassword, req.NewPassword); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) personalStats(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	stats, err := a.users.Stats(r.Context(), uid)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, stats)
}

func (a *API) deleteMe(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	if err := a.users.DeleteMe(r.Context(), uid); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

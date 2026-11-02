package httptransport

import (
	"net/http"

	"github.com/getcontact/backend/internal/apperr"
)

func (a *API) adminStats(w http.ResponseWriter, r *http.Request) {
	stats, err := a.admin.Stats(r.Context())
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, stats)
}

func (a *API) adminListUsers(w http.ResponseWriter, r *http.Request) {
	page, perPage := pageParams(r)
	result, err := a.admin.ListUsers(r.Context(), page, perPage, r.URL.Query().Get("q"))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writePage(w, result.Items, result.Total, result.Page, result.PerPage)
}

func (a *API) adminGetUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	user, err := a.admin.GetUser(r.Context(), id)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, user)
}

type blockUserRequest struct {
	Blocked *bool `json:"blocked"`
}

func (a *API) adminSetBlocked(w http.ResponseWriter, r *http.Request) {
	actor, _ := userIDFrom(r.Context())
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req blockUserRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if req.Blocked == nil {
		writeError(w, r, a.log, apperr.Invalid(map[string]string{"blocked": "is required"}))
		return
	}
	user, err := a.admin.SetBlocked(r.Context(), actor, id, *req.Blocked)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, user)
}

func (a *API) adminDeleteUser(w http.ResponseWriter, r *http.Request) {
	actor, _ := userIDFrom(r.Context())
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if err := a.admin.DeleteUser(r.Context(), actor, id); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) adminListReports(w http.ResponseWriter, r *http.Request) {
	page, perPage := pageParams(r)
	result, err := a.admin.ListReports(r.Context(), page, perPage)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writePage(w, result.Items, result.Total, result.Page, result.PerPage)
}

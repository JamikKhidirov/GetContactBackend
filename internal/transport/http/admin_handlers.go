package httptransport

import (
	"net/http"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/domain"
	"github.com/getcontact/backend/internal/service"
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

func (a *API) adminNumberDossier(w http.ResponseWriter, r *http.Request) {
	actor, _ := userIDFrom(r.Context())
	dossier, err := a.admin.NumberDossier(r.Context(), pathParam(r, "phone"), actor)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, dossier)
}

func (a *API) adminUpdateNumber(w http.ResponseWriter, r *http.Request) {
	actor, _ := userIDFrom(r.Context())
	var req service.UpdateNumberInput
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	number, err := a.admin.UpdateNumber(r.Context(), pathParam(r, "phone"), actor, req)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, number)
}

func (a *API) adminAudit(w http.ResponseWriter, r *http.Request) {
	page, perPage := pageParams(r)
	result, err := a.admin.Audit(r.Context(), domain.AuditFilter{
		EventType:   r.URL.Query().Get("event_type"),
		AggregateID: r.URL.Query().Get("aggregate_id"),
		Page:        page,
		PerPage:     perPage,
	})
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writePage(w, result.Items, result.Total, result.Page, result.PerPage)
}

func (a *API) adminUserSummary(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	summary, err := a.admin.UserSummary(r.Context(), id)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

// adminUserSessions exposes the device sessions of any user to administrators.
func (a *API) adminUserSessions(w http.ResponseWriter, r *http.Request) {
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	sessions, err := a.admin.UserSessions(r.Context(), id)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"user_id": id, "sessions": sessions, "total": len(sessions)})
}

// adminRevokeUserSessions ends every session of a user.
func (a *API) adminRevokeUserSessions(w http.ResponseWriter, r *http.Request) {
	actor, _ := userIDFrom(r.Context())
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	revoked, err := a.admin.RevokeUserSessions(r.Context(), actor, id)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, map[string]any{"user_id": id, "revoked": revoked})
}

func (a *API) adminDeleteReport(w http.ResponseWriter, r *http.Request) {
	actor, _ := userIDFrom(r.Context())
	id, err := pathUUID(r, "reportID")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if err := a.admin.DeleteReport(r.Context(), id, actor); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

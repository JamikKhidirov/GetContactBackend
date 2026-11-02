package httptransport

import (
	"net/http"

	"github.com/getcontact/backend/internal/service"
)

func (a *API) listContacts(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	query := readContactListQuery(r)
	page, err := a.contacts.List(r.Context(), uid, query)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writePage(w, page.Items, page.Total, page.Page, page.PerPage)
}

func (a *API) createContact(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	var req service.ContactInput
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	contact, err := a.contacts.Create(r.Context(), uid, req)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusCreated, contact)
}

type importContactsRequest struct {
	Contacts []service.ContactInput `json:"contacts"`
}

func (a *API) importContacts(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	var req importContactsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	res, err := a.contacts.Import(r.Context(), uid, req.Contacts)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, res)
}

func (a *API) getContact(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	contact, err := a.contacts.Get(r.Context(), uid, id)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, contact)
}

func (a *API) updateContact(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	var req service.UpdateContactInput
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	contact, err := a.contacts.Update(r.Context(), uid, id, req)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, contact)
}

func (a *API) deleteContact(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	id, err := pathUUID(r, "id")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if err := a.contacts.Delete(r.Context(), uid, id); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

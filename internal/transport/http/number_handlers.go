package httptransport

import (
	"net/http"

	"github.com/getcontact/backend/internal/domain"
	"github.com/google/uuid"
)

func (a *API) lookupNumber(w http.ResponseWriter, r *http.Request) {
	var uid *uuid.UUID
	if id, ok := userIDFrom(r.Context()); ok {
		uid = &id
	}
	result, err := a.numbers.Lookup(r.Context(), pathParam(r, "phone"), uid)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) listNumberTags(w http.ResponseWriter, r *http.Request) {
	tags, err := a.numbers.ListTags(r.Context(), pathParam(r, "phone"))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, tags)
}

func (a *API) listTagCatalog(w http.ResponseWriter, r *http.Request) {
	tags, err := a.numbers.Tags(r.Context(), limitParam(r, 20, 100))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, tags)
}

type addTagRequest struct {
	Tag string `json:"tag"`
}

func (a *API) addTag(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	var req addTagRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	tag, err := a.numbers.AddTag(r.Context(), pathParam(r, "phone"), uid, req.Tag)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusCreated, tag)
}

func (a *API) deleteTag(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	tagID, err := pathUUID(r, "tagID")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if err := a.numbers.DeleteTag(r.Context(), pathParam(r, "phone"), tagID, uid, domain.Role(roleFrom(r.Context()))); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

type reportSpamRequest struct {
	Category string `json:"category"`
	Comment  string `json:"comment"`
}

func (a *API) reportSpam(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	var req reportSpamRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	result, err := a.numbers.ReportSpam(r.Context(), pathParam(r, "phone"), uid, req.Category, req.Comment)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusCreated, result)
}

func (a *API) topSpam(w http.ResponseWriter, r *http.Request) {
	numbers, err := a.numbers.TopSpam(r.Context(), limitParam(r, 10, 100))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, numbers)
}

func (a *API) trendingNumbers(w http.ResponseWriter, r *http.Request) {
	numbers, err := a.numbers.Trending(r.Context(), limitParam(r, 10, 50))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, numbers)
}

func (a *API) numberNames(w http.ResponseWriter, r *http.Request) {
	names, err := a.numbers.Names(r.Context(), pathParam(r, "phone"))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, names)
}

type addNameRequest struct {
	Name string `json:"name"`
}

func (a *API) addNumberName(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	var req addNameRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	name, err := a.numbers.AddName(r.Context(), pathParam(r, "phone"), uid, req.Name)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusCreated, name)
}

func (a *API) deleteNumberName(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	nameID, err := pathUUID(r, "nameID")
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	if err := a.numbers.DeleteName(r.Context(), pathParam(r, "phone"), nameID, uid, domain.Role(roleFrom(r.Context()))); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeJSON(w, http.StatusNoContent, nil)
}

func (a *API) numberSpamSummary(w http.ResponseWriter, r *http.Request) {
	summary, err := a.numbers.SpamSummary(r.Context(), pathParam(r, "phone"))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, summary)
}

func (a *API) spamCategories(w http.ResponseWriter, r *http.Request) {
	writeData(w, http.StatusOK, a.numbers.SpamCategories())
}

type batchLookupRequest struct {
	Phones []string `json:"phones"`
}

func (a *API) batchLookup(w http.ResponseWriter, r *http.Request) {
	var uid *uuid.UUID
	if id, ok := userIDFrom(r.Context()); ok {
		uid = &id
	}
	var req batchLookupRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, r, a.log, err)
		return
	}
	result, err := a.numbers.BatchLookup(r.Context(), req.Phones, uid)
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

func (a *API) search(w http.ResponseWriter, r *http.Request) {
	uid, _ := userIDFrom(r.Context())
	result, err := a.searchSvc.Search(r.Context(), uid, r.URL.Query().Get("q"), limitParam(r, 20, 50))
	if err != nil {
		writeError(w, r, a.log, err)
		return
	}
	writeData(w, http.StatusOK, result)
}

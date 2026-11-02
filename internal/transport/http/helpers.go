package httptransport

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/getcontact/backend/internal/platform"
	"github.com/getcontact/backend/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func pathUUID(r *http.Request, name string) (uuid.UUID, error) {
	raw := chi.URLParam(r, name)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, apperr.BadRequest(name + " must be a valid UUID")
	}
	return id, nil
}

func pathParam(r *http.Request, name string) string {
	return chi.URLParam(r, name)
}

func pageParams(r *http.Request) (page, perPage int) {
	page = platform.ParseIntDefault(r.URL.Query().Get("page"), 1)
	perPage = platform.ParseIntDefault(r.URL.Query().Get("per_page"), 20)
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	return page, perPage
}

func limitParam(r *http.Request, def, max int) int {
	limit := platform.ParseIntDefault(r.URL.Query().Get("limit"), def)
	if limit < 1 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	return limit
}

func favoriteParam(r *http.Request) *bool {
	raw := strings.TrimSpace(r.URL.Query().Get("favorite"))
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &v
}

func readContactListQuery(r *http.Request) service.ContactListQuery {
	page, perPage := pageParams(r)
	return service.ContactListQuery{
		Q:        r.URL.Query().Get("q"),
		Favorite: favoriteParam(r),
		Page:     page,
		PerPage:  perPage,
	}
}

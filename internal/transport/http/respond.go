// Package httptransport exposes the REST API and HTTP middleware.
package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/getcontact/backend/internal/apperr"
	"github.com/go-chi/chi/v5/middleware"
)

const maxBodyBytes = 1 << 20 // 1 MiB

func writeRawJSON(w io.Writer, payload any) error {
	return json.NewEncoder(w).Encode(payload)
}

type errorPayload struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Details   any    `json:"details,omitempty"`
		RequestID string `json:"request_id,omitempty"`
	} `json:"error"`
}

type meta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if payload == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(payload)
}

func writeData(w http.ResponseWriter, status int, data any) {
	writeJSON(w, status, map[string]any{"data": data})
}

func writePage(w http.ResponseWriter, data any, total int64, page, perPage int) {
	totalPages := int64(0)
	if perPage > 0 {
		totalPages = (total + int64(perPage) - 1) / int64(perPage)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"data": data,
		"meta": meta{Page: page, PerPage: perPage, Total: total, TotalPages: totalPages},
	})
}

func writeError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	appErr := apperr.From(err)
	if appErr.Status >= 500 {
		log.Error("request failed", "error", appErr.Error(), "method", r.Method, "path", r.URL.Path,
			"request_id", middleware.GetReqID(r.Context()))
	}
	var payload errorPayload
	payload.Error.Code = appErr.Code
	payload.Error.Message = appErr.Message
	payload.Error.Details = appErr.Details
	payload.Error.RequestID = middleware.GetReqID(r.Context())
	writeJSON(w, appErr.Status, payload)
}

// decodeJSON strictly decodes a single JSON object with a 1 MiB limit.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		if mediaType := strings.TrimSpace(strings.Split(ct, ";")[0]); mediaType != "application/json" {
			return apperr.New(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Content-Type must be application/json")
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return apperr.New(http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE", "request body exceeds 1 MiB")
		}
		if errors.Is(err, io.EOF) {
			return apperr.BadRequest("request body is required")
		}
		return apperr.BadRequest("invalid JSON body: " + err.Error())
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return apperr.BadRequest("request body must contain a single JSON object")
	}
	return nil
}

// decodeOptionalJSON is used by endpoints whose body is optional (logout).
func decodeOptionalJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil || r.ContentLength == 0 {
		return nil
	}
	err := decodeJSON(w, r, dst)
	if err != nil {
		if appErr, ok := err.(*apperr.Error); ok && appErr.Code == "BAD_REQUEST" &&
			strings.Contains(appErr.Message, "request body is required") {
			return nil
		}
	}
	return err
}

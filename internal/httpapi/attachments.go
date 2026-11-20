package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/attachments"
)

func isAttachmentUploadRoute(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	p := r.URL.Path
	if !strings.HasPrefix(p, "/api/v1/workspaces/") || !strings.HasSuffix(p, "/attachments") {
		return false
	}
	trimmed := strings.TrimPrefix(p, "/api/v1/workspaces/")
	trimmed = strings.TrimSuffix(trimmed, "/attachments")
	return trimmed != "" && !strings.Contains(trimmed, "/")
}

func (api *API) uploadWorkspaceAttachment(w http.ResponseWriter, r *http.Request) {
	if api.attachments == nil {
		writeError(w, http.StatusServiceUnavailable, "attachments_unavailable", "image attachments are not configured")
		return
	}
	workspaceID, ok := parseUUIDParam(w, r, "workspaceID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if identity.WorkspaceID != workspaceID {
		writeError(w, http.StatusForbidden, "workspace_access_denied", "workspace membership is required")
		return
	}

	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		writeError(w, http.StatusUnsupportedMediaType, "multipart_required", "Content-Type must be multipart/form-data")
		return
	}

	maxUpload := api.attachments.MaxUploadBytes()
	// 预留 1 MiB 给 multipart 边界与可选表单字段（如 card_id）。
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload+(1<<20))
	if err := r.ParseMultipartForm(min(maxUpload, 8<<20)); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "image_too_large", fmt.Sprintf("image exceeds max size (%d MiB)", maxUpload>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_multipart", "failed to parse multipart upload")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", "multipart field 'file' is required")
		return
	}
	defer file.Close()

	rawData, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_failed", "failed to read uploaded image")
		return
	}
	if int64(len(rawData)) > maxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "image_too_large", fmt.Sprintf("image exceeds max size (%d MiB)", maxUpload>>20))
		return
	}

	var cardID *uuid.UUID
	if rawCardID := strings.TrimSpace(r.FormValue("card_id")); rawCardID != "" {
		parsed, err := uuid.Parse(rawCardID)
		if err != nil || parsed == uuid.Nil {
			writeError(w, http.StatusBadRequest, "invalid_card_id", "invalid card_id")
			return
		}
		cardID = &parsed
	}

	att, err := api.attachments.Upload(r.Context(), attachments.UploadParams{
		WorkspaceID:        workspaceID,
		CardID:             cardID,
		UploadedBySourceID: identity.SourceID,
		RawData:            rawData,
	})
	if err != nil {
		switch {
		case errors.Is(err, attachments.ErrEmptyImage),
			errors.Is(err, attachments.ErrUnsupportedFormat),
			errors.Is(err, attachments.ErrImageDimensions):
			writeError(w, http.StatusBadRequest, "invalid_image", err.Error())
		case errors.Is(err, attachments.ErrImageTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "image_too_large", err.Error())
		case errors.Is(err, attachments.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "card not found")
		default:
			api.handleError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

func (api *API) getWorkspaceAttachment(w http.ResponseWriter, r *http.Request) {
	if api.attachments == nil {
		writeError(w, http.StatusServiceUnavailable, "attachments_unavailable", "image attachments are not configured")
		return
	}
	workspaceID, ok := parseUUIDParam(w, r, "workspaceID")
	if !ok {
		return
	}
	attachmentID, ok := parseUUIDParam(w, r, "attachmentID")
	if !ok {
		return
	}
	identity := identityFromRequest(r)
	if identity.WorkspaceID != workspaceID {
		writeError(w, http.StatusForbidden, "workspace_access_denied", "workspace membership is required")
		return
	}

	opened, err := api.attachments.Open(r.Context(), workspaceID, attachmentID)
	if err != nil {
		switch {
		case errors.Is(err, attachments.ErrExpired):
			w.Header().Set("Cache-Control", "no-store")
			writeError(w, http.StatusGone, "attachment_expired", "image attachment has expired and been purged")
		case errors.Is(err, attachments.ErrNotFound):
			writeError(w, http.StatusNotFound, "not_found", "attachment not found")
		default:
			api.handleError(w, err)
		}
		return
	}
	defer opened.File.Close()

	etag := fmt.Sprintf(`"%s"`, opened.SHA256)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", opened.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm != "" && inm == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(w, r, opened.ID.String(), opened.CreatedAt, opened.File)
}

func parseAttachmentIDs(w http.ResponseWriter, raw []string) ([]uuid.UUID, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	if len(raw) > attachments.MaxAttachmentsPerTurn {
		writeError(w, http.StatusBadRequest, "too_many_attachments", fmt.Sprintf("at most %d image attachments are allowed per message", attachments.MaxAttachmentsPerTurn))
		return nil, false
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, item := range raw {
		parsed, err := uuid.Parse(strings.TrimSpace(item))
		if err != nil || parsed == uuid.Nil {
			writeError(w, http.StatusBadRequest, "invalid_attachment_id", "invalid attachment_id")
			return nil, false
		}
		out = append(out, parsed)
	}
	return out, true
}

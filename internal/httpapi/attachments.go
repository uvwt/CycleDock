package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/uvwt/CycleDock/internal/attachments"
	"github.com/uvwt/CycleDock/internal/store"
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
			writeError(w, http.StatusRequestEntityTooLarge, "attachment_too_large", fmt.Sprintf("attachment exceeds max size (%d MiB)", maxUpload>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "invalid_multipart", "failed to parse multipart upload")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing_file", "multipart field 'file' is required")
		return
	}
	defer file.Close()

	rawData, err := io.ReadAll(io.LimitReader(file, maxUpload+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_failed", "failed to read uploaded attachment")
		return
	}
	if int64(len(rawData)) > maxUpload {
		writeError(w, http.StatusRequestEntityTooLarge, "attachment_too_large", fmt.Sprintf("attachment exceeds max size (%d MiB)", maxUpload>>20))
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

	var fileName string
	if header != nil {
		fileName = header.Filename
	}

	att, err := api.attachments.Upload(r.Context(), attachments.UploadParams{
		WorkspaceID:        workspaceID,
		CardID:             cardID,
		UploadedBySourceID: identity.SourceID,
		FileName:           fileName,
		RawData:            rawData,
	})
	if err != nil {
		switch {
		case errors.Is(err, attachments.ErrEmptyImage),
			errors.Is(err, attachments.ErrEmptyFile),
			errors.Is(err, attachments.ErrUnsupportedFormat),
			errors.Is(err, attachments.ErrUnsupportedTextFile),
			errors.Is(err, attachments.ErrImageDimensions):
			writeError(w, http.StatusBadRequest, "invalid_attachment", err.Error())
		case errors.Is(err, attachments.ErrImageTooLarge),
			errors.Is(err, attachments.ErrTextTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, "attachment_too_large", err.Error())
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
			writeError(w, http.StatusGone, "attachment_expired", "attachment has expired and been purged")
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
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if opened.Kind == attachments.KindText {
		safeName := attachments.SanitizeFileName(opened.FileName)
		if safeName == "" {
			safeName = opened.ID.String() + ".txt"
		}
		dispositionType := "inline"
		if r.URL.Query().Get("download") == "1" {
			dispositionType = "attachment"
		}
		disposition := mime.FormatMediaType(dispositionType, map[string]string{"filename": safeName})
		if disposition == "" {
			disposition = dispositionType
		}
		// 文本类附件始终以纯文本返回并配合 nosniff，避免浏览器直接执行 .html/.xml/.js 导致 XSS。
		w.Header().Set("Content-Type", attachments.TextMimeType)
		w.Header().Set("Content-Disposition", disposition)
		if inm := strings.TrimSpace(r.Header.Get("If-None-Match")); inm != "" && inm == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		var data []byte
		if opened.StorageEncoding == attachments.EncodingGzip {
			data, err = attachments.DecompressGzip(opened.File, api.attachments.MaxTextUploadBytes())
		} else {
			data, err = io.ReadAll(io.LimitReader(opened.File, api.attachments.MaxTextUploadBytes()+1))
		}
		if err != nil {
			api.handleError(w, err)
			return
		}
		http.ServeContent(w, r, safeName, opened.CreatedAt, bytes.NewReader(data))
		return
	}

	w.Header().Set("Content-Type", opened.MimeType)
	w.Header().Set("Content-Disposition", "inline")

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
		writeError(w, http.StatusBadRequest, "too_many_attachments", fmt.Sprintf("at most %d attachments are allowed per message", attachments.MaxAttachmentsPerTurn))
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

// enrichTimelineAttachments 为时间线 comment 事件中的附件引用补全实时状态（active/expired/purged），
// 避免前端对已清理文本附件仍展示可打开状态。
func (api *API) enrichTimelineAttachments(ctx context.Context, workspaceID uuid.UUID, timeline []eventResponse) {
	if len(timeline) == 0 || api.store == nil || workspaceID == uuid.Nil {
		return
	}
	type parsedEntry struct {
		index   int
		payload store.CommentMessagePayload
	}
	var entries []parsedEntry
	seen := make(map[uuid.UUID]struct{})
	ids := make([]uuid.UUID, 0)
	for i := range timeline {
		ev := &timeline[i]
		if ev.Type != "comment" || len(ev.Message) == 0 {
			continue
		}
		var payload store.CommentMessagePayload
		if err := json.Unmarshal(ev.Message, &payload); err != nil || len(payload.Attachments) == 0 {
			continue
		}
		entries = append(entries, parsedEntry{index: i, payload: payload})
		for _, att := range payload.Attachments {
			if att.ID == uuid.Nil {
				continue
			}
			if _, ok := seen[att.ID]; !ok {
				seen[att.ID] = struct{}{}
				ids = append(ids, att.ID)
			}
		}
	}
	if len(ids) == 0 {
		return
	}
	statuses, err := api.store.AttachmentStatuses(ctx, workspaceID, ids)
	if err != nil {
		return
	}
	for _, entry := range entries {
		for j := range entry.payload.Attachments {
			id := entry.payload.Attachments[j].ID
			if status, ok := statuses[id]; ok {
				entry.payload.Attachments[j].Status = status
			} else {
				entry.payload.Attachments[j].Status = "purged"
			}
		}
		if raw, err := json.Marshal(entry.payload); err == nil {
			timeline[entry.index].Message = raw
		}
	}
}

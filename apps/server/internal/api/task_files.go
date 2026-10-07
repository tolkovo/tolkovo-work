package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
	"github.com/tolkovo/tolkovo/apps/server/internal/tasks"
)

// Files of tasks (migration 00033): screenshots, logs and documents in a
// task's description and its comments.

// CreateTaskFileUpload implements POST /task-files.
func (s *server) CreateTaskFileUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskFileUploadRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	up, err := s.tasks.RegisterFile(r.Context(), p.WorkspaceID, p.UserID, tasks.FileInput{
		FileName: req.FileName, ContentType: req.ContentType, Size: req.Size,
	})
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, oapi.TaskFileUpload{File: taskFileBody(up.File), Upload: signedBody(up.Request)})
}

// GetTaskFileDownloadLink implements GET /task-files/{id}/download-link.
func (s *server) GetTaskFileDownloadLink(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksView)
	if !ok {
		return
	}
	_, req, err := s.tasks.FileLink(r.Context(), p.WorkspaceID, id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, signedBody(req))
}

// AddTaskFiles implements POST /tasks/{id}/files.
func (s *server) AddTaskFiles(w http.ResponseWriter, r *http.Request, id oapi.Id) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	var req oapi.TaskFilesAdd
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	t, err := s.tasks.AddFiles(r.Context(), p.WorkspaceID, p.UserID, id, req.FileIds)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taskBody(t))
}

// RemoveTaskFile implements DELETE /tasks/{id}/files/{file_id}.
func (s *server) RemoveTaskFile(w http.ResponseWriter, r *http.Request, id oapi.Id, fileID uuid.UUID) {
	p, ok := s.requirePrincipal(w, r, authz.TasksEdit)
	if !ok {
		return
	}
	if err := s.tasks.RemoveFile(r.Context(), p.WorkspaceID, p.UserID, id, fileID); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func taskFileBody(f tasks.File) oapi.TaskFile {
	return oapi.TaskFile{Id: f.ID, FileName: f.FileName, ContentType: f.ContentType, Size: f.Size, UploadedBy: f.UploadedBy, CreatedAt: f.CreatedAt}
}

func taskFileList(files []tasks.File) []oapi.TaskFile {
	out := make([]oapi.TaskFile, 0, len(files))
	for _, f := range files {
		out = append(out, taskFileBody(f))
	}
	return out
}

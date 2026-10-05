package api

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// Problem is an RFC 9457 problem detail. Code is stable and machine-readable;
// Detail is for humans and never contains secrets or personal data.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail,omitempty"`
	Instance  string `json:"instance,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	// UploadID names the widget's file a message was refused for.
	UploadID string `json:"upload_id,omitempty"`
}

func writeProblem(w http.ResponseWriter, r *http.Request, p Problem) {
	if p.Type == "" {
		p.Type = "about:blank"
	}
	p.Instance = r.URL.Path
	p.RequestID = middleware.GetReqID(r.Context())
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

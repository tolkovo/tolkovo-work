package api

import (
	"net/http"

	"github.com/tolkovo/tolkovo/apps/server/internal/api/oapi"
	"github.com/tolkovo/tolkovo/apps/server/internal/notifications"
	"github.com/tolkovo/tolkovo/apps/server/internal/platform/authz"
)

// GetMyNotificationSettings implements GET /me/notifications.
func (s *server) GetMyNotificationSettings(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	p, err := s.notifications.Prefs(r.Context(), st.session.WorkspaceID, st.session.UserID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.notificationSettings(p))
}

// UpdateMyNotificationSettings implements PATCH /me/notifications.
func (s *server) UpdateMyNotificationSettings(w http.ResponseWriter, r *http.Request) {
	st, ok := authStateFrom(r.Context())
	if !ok {
		s.writeError(w, r, authz.ErrUnauthenticated)
		return
	}
	var req oapi.UpdateNotificationSettingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.writeError(w, r, err)
		return
	}
	in := notifications.PrefsPatch{EmailMissed: req.EmailMissed}
	if req.EmailDelayMinutes != nil {
		d := int(*req.EmailDelayMinutes)
		in.EmailDelay = &d
	}
	p, err := s.notifications.UpdatePrefs(r.Context(), st.session.WorkspaceID, st.session.UserID, in)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.notificationSettings(p))
}

func (s *server) notificationSettings(p notifications.Prefs) oapi.NotificationSettings {
	return oapi.NotificationSettings{
		EmailMissed: p.EmailMissed, EmailDelayMinutes: oapi.EmailDelayMinutes(p.EmailDelay),
		EmailAvailable: s.notifications.EmailEnabled(),
	}
}

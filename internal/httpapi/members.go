package httpapi

import (
	"net/http"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// Project membership routes. Only the owner and managers may read or change
// the member list; a viewer or runner gets 403 and a non-member 404 (the
// project's existence stays hidden). The owner cannot be changed or removed
// (409).

type memberView struct {
	UserID      string  `json:"user_id"`
	Username    *string `json:"username"`
	DisplayName *string `json:"display_name"`
	Role        string  `json:"role"`
	Disabled    bool    `json:"disabled"`
}

func viewMember(m postgres.Member) memberView {
	return memberView{UserID: m.UserID, Username: m.Username, DisplayName: m.DisplayName, Role: m.Role, Disabled: m.Disabled}
}

func (s *Server) listMembers(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	list, err := s.store.ListMembers(ctx, q.session.UserID, q.params[0])
	if err != nil {
		s.storeError(q, err)
		return
	}
	items := make([]memberView, 0, len(list))
	for _, m := range list {
		items = append(items, viewMember(m))
	}
	writeJSON(q.w, http.StatusOK, map[string]any{"items": items})
}

func decodeRole(q *request, withUsername bool) (domain.Role, string, bool) {
	body, ok := q.readBody()
	if !ok {
		return "", "", false
	}
	var in struct {
		Username *string `json:"username"`
		Role     string  `json:"role"`
	}
	if domain.DecodeStrict(body, &in) != nil || (in.Username != nil) != withUsername {
		msg := `body must be {"role": "manager"|"runner"|"viewer"}`
		if withUsername {
			msg = `body must be {"username": string, "role": "manager"|"runner"|"viewer"}`
		}
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", msg, false)
		return "", "", false
	}
	role := domain.Role(in.Role)
	if !role.Valid() {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "role must be manager, runner or viewer", false)
		return "", "", false
	}
	name := ""
	if in.Username != nil {
		name = *in.Username
	}
	return role, name, true
}

// POST /projects/{id}/members {username, role}: add or change a member named
// by username (a manager need not be an administrator to find colleagues).
func (s *Server) addMember(q *request) {
	role, name, ok := decodeRole(q, true)
	if !ok {
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	m, err := s.store.AddMemberByUsername(ctx, q.session.UserID, q.params[0], name, role)
	if err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, viewMember(m))
}

// PUT /projects/{id}/members/{user_id} {role}
func (s *Server) putMember(q *request) {
	role, _, ok := decodeRole(q, false)
	if !ok {
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	m, err := s.store.PutMember(ctx, q.session.UserID, q.params[0], q.params[1], role)
	if err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, viewMember(m))
}

// DELETE /projects/{id}/members/{user_id}: 204 also when the user was not a
// member.
func (s *Server) deleteMember(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RemoveMember(ctx, q.session.UserID, q.params[0], q.params[1]); err != nil {
		s.storeError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}

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

func decodeRole(q *request) (domain.Role, bool) {
	body, ok := q.readBody()
	if !ok {
		return "", false
	}
	var in struct {
		Role string `json:"role"`
	}
	if domain.DecodeStrict(body, &in) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"role": "manager"|"runner"|"viewer"}`, false)
		return "", false
	}
	role := domain.Role(in.Role)
	if !role.Valid() {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "role must be manager, runner or viewer", false)
		return "", false
	}
	return role, true
}

// PUT /projects/{id}/members/{user_id} {role}: add or change a member. The
// user is named by ID only; there is no lookup by username, so no project
// route tells whether a username exists.
func (s *Server) putMember(q *request) {
	role, ok := decodeRole(q)
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

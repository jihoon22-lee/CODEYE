package domain

// Role is a project membership role.
type Role string

const (
	RoleManager Role = "manager"
	RoleRunner  Role = "runner"
	RoleViewer  Role = "viewer"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleManager, RoleRunner, RoleViewer:
		return true
	}
	return false
}

// CanRun reports whether r may start and cancel runs.
func (r Role) CanRun() bool { return r == RoleManager || r == RoleRunner }

// CanManage reports whether r may change project settings and membership.
func (r Role) CanManage() bool { return r == RoleManager }

package domain

import "testing"

func TestRole(t *testing.T) {
	cases := []struct {
		role               Role
		valid, run, manage bool
	}{
		{RoleManager, true, true, true},
		{RoleRunner, true, true, false},
		{RoleViewer, true, false, false},
		{"", false, false, false},
		{"admin", false, false, false},
		{"Manager", false, false, false},
	}
	for _, c := range cases {
		if c.role.Valid() != c.valid || c.role.CanRun() != c.run || c.role.CanManage() != c.manage {
			t.Errorf("role %q: valid=%v run=%v manage=%v", c.role, c.role.Valid(), c.role.CanRun(), c.role.CanManage())
		}
	}
}

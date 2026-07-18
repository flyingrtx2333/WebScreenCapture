package app

import "testing"

func TestAllowedSignals(t *testing.T) {
	cases := []struct {
		role     Role
		typeName string
		allowed  bool
	}{
		{RoleAgent, "sdp.offer", true},
		{RoleAgent, "sdp.answer", false},
		{RoleViewer, "sdp.answer", true},
		{RoleViewer, "sdp.offer", false},
		{RoleViewer, "status", false},
		{RoleViewer, "ice.restart", true},
		{RoleViewer, "keyframe.request", true},
		{RoleAgent, "ice.restart", false},
	}
	for _, tc := range cases {
		if got := allowedSignal(tc.role, tc.typeName); got != tc.allowed {
			t.Fatalf("allowedSignal(%q, %q)=%v, want %v", tc.role, tc.typeName, got, tc.allowed)
		}
	}
}

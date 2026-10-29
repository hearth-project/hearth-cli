package client

import (
	"testing"
)

func TestMachineHealthDoesNotExposeSecrets(t *testing.T) {
	en := newEnv(t)
	m := en.machine("health")
	r := m.MachineHealth()
	if r.CheckedAt == 0 {
		t.Fatalf("incomplete report: %+v", r)
	}
	if len(r.Issues) == 0 && r.Status != "ready" {
		t.Fatalf("invalid status: %+v", r)
	}
}

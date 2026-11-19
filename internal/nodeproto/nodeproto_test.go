package nodeproto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCapabilityForms(t *testing.T) {
	good := []string{"analysis:deep-review", LocalSourceCapability("0b6e3c1e-6e6b-4c1e-9a55-3a1f6f0d2b11"), GitSourceCapability("GitHub.com"), "source:git:ghe.example:8443"}
	for _, c := range good {
		if !ValidCapability(c) {
			t.Errorf("rejected %q", c)
		}
	}
	bad := []string{"", "analysis:", "analysis:Deep", "source:local:../x", "source:local:0B6E3C1E-6E6B-4C1E-9A55-3A1F6F0D2B11", "source:git:Host.Example", "source:git:a b", "shell:anything"}
	for _, c := range bad {
		if ValidCapability(c) {
			t.Errorf("accepted %q", c)
		}
	}
	if !ValidTaskKey("go-vet") || ValidTaskKey("Go") || !ValidReason("lease_lost") || ValidReason("Lease") || !ValidArtifactRole("result_manifest") || ValidArtifactRole("x") {
		t.Fatal("key/reason/role validation")
	}
}

// The wire names agents depend on must not drift silently.
func TestWireFieldNames(t *testing.T) {
	raw, _ := json.Marshal(ClaimResponse{})
	for _, f := range []string{`"lease"`, `"job"`, `"run_version"`, `"lease_epoch"`, `"snapshot_id"`, `"resources"`, `"disk_bytes"`} {
		if !strings.Contains(string(raw), f) {
			t.Errorf("claim response lacks %s: %s", f, raw)
		}
	}
	raw, _ = json.Marshal(NodeHeartbeatRequest{})
	for _, f := range []string{`"last_command_sequence"`, `"memory_available_bytes"`, `"disk_available_bytes"`, `"cleanup_reports"`} {
		if !strings.Contains(string(raw), f) {
			t.Errorf("heartbeat lacks %s: %s", f, raw)
		}
	}
}

// Heartbeat fields are omitted by static agents (their bytes are unchanged) and
// keep their wire names for controller-scaled agents.
func TestScalingFieldsOmittedForStaticAgents(t *testing.T) {
	raw, _ := json.Marshal(NodeHeartbeatRequest{})
	if strings.Contains(string(raw), "worker_scaling") || strings.Contains(string(raw), "max_workers") {
		t.Fatalf("static heartbeat carries scaling fields: %s", raw)
	}
	raw, _ = json.Marshal(NodeHeartbeatRequest{WorkerScaling: ScalingController, MaxWorkers: 2})
	if !strings.Contains(string(raw), `"worker_scaling":"controller"`) || !strings.Contains(string(raw), `"max_workers":2`) {
		t.Fatalf("controller heartbeat: %s", raw)
	}
}

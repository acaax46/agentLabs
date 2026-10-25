package execution

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// VMRunner is the host-side gate for a student execution VM. The guest image
// currently boots a minimal OpenSandbox supervisor smoke image; production
// images replace it with the signed Linux VM containing the OpenSandbox server.
type VMRunner struct{ Image string }

func (v VMRunner) Start(ctx context.Context, id string) error {
	image := v.Image
	if image == "" {
		image = "agentlab/vm-smoke:alpine-3.22"
	}
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--name", "agentlab-student-vm-"+id, "--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--read-only", "--tmpfs", "/tmp:rw,nosuid,nodev,size=16m", "--memory", "512m", "--cpus", "1", image)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("student VM failed: %s", strings.TrimSpace(string(out)))
	}
	if !strings.Contains(string(out), "AGENTLAB_OPEN_SANDBOX_GUEST_READY") {
		return fmt.Errorf("student VM did not start its guest OpenSandbox supervisor")
	}
	return nil
}

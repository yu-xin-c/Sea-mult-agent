package engine

import "testing"

func TestDockerGPURequest(t *testing.T) {
	t.Setenv("SANDBOX_DOCKER_GPUS", " all ")
	if got := dockerGPURequest(); got != "all" {
		t.Fatalf("dockerGPURequest() = %q, want all", got)
	}

	t.Setenv("SANDBOX_DOCKER_GPUS", "none")
	if got := dockerGPURequest(); got != "" {
		t.Fatalf("dockerGPURequest() = %q, want empty", got)
	}
}

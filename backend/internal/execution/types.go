package execution

import "time"

type Mode string

const (
	ModeOI    Mode = "oi"
	ModeModel Mode = "model"
)

type Kind string

const (
	KindEvaluation Kind = "evaluation"
	KindAgent      Kind = "agent"
)

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Unknown   Status = "unknown"
)

type GPUDevice struct {
	Index         int    `json:"index"`
	Name          string `json:"name"`
	TotalMemoryMB int    `json:"totalMemoryMB"`
	FreeMemoryMB  int    `json:"freeMemoryMB"`
}
type Resources struct {
	CPUs            float64 `json:"cpus"`
	MemoryMB        int     `json:"memoryMB"`
	GPUCount        int     `json:"gpuCount"`
	GPUModel        string  `json:"gpuModel"`
	MinFreeMemoryMB int     `json:"minFreeMemoryMB"`
}
type EvaluationRequest struct {
	Mode       Mode      `json:"mode"`
	ProblemID  string    `json:"problemId"`
	Code       string    `json:"code"`
	Language   string    `json:"language"`
	Input      string    `json:"input"`
	Expected   string    `json:"expected"`
	ModelRef   string    `json:"modelRef"`
	WeightsRef string    `json:"weightsRef"`
	Resources  Resources `json:"resources"`
}
type TestCase struct {
	ID       string `json:"-"`
	Input    string `json:"-"`
	Expected string `json:"-"`
}
type AgentRequest struct {
	ConnectionID         string `json:"connectionId,omitempty"`
	connectionGeneration int
	userID               string
	Agent                string    `json:"agent"`
	Provider             string    `json:"provider"`
	Model                string    `json:"model"`
	Prompt               string    `json:"prompt"`
	Mode                 string    `json:"mode"`
	System               string    `json:"system,omitempty"`
	Resources            Resources `json:"resources"`
}
type Step struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	Label        string     `json:"label"`
	Status       Status     `json:"status"`
	Provider     string     `json:"provider,omitempty"`
	Model        string     `json:"model,omitempty"`
	InputTokens  *int64     `json:"inputTokens,omitempty"`
	OutputTokens *int64     `json:"outputTokens,omitempty"`
	DurationMS   int64      `json:"durationMs"`
	Error        string     `json:"error,omitempty"`
	StartedAt    time.Time  `json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}
type Run struct {
	ID            string         `json:"id"`
	Kind          Kind           `json:"kind"`
	Mode          Mode           `json:"mode,omitempty"`
	Status        Status         `json:"status"`
	Agent         string         `json:"agent,omitempty"`
	Provider      string         `json:"provider,omitempty"`
	Model         string         `json:"model,omitempty"`
	Resources     Resources      `json:"resources"`
	AllocatedGPU  []GPUDevice    `json:"allocatedGpu,omitempty"`
	Steps         []Step         `json:"steps"`
	Result        map[string]any `json:"result,omitempty"`
	Error         string         `json:"error,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	SourceDigest  string         `json:"sourceDigest,omitempty"`
	TestVersion   string         `json:"testVersion,omitempty"`
	ResultVersion int            `json:"resultVersion,omitempty"`
}

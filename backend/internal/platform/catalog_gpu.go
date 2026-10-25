package platform

import "time"

// The GPU template. It is kept apart from the CPU templates because its version carries a resource
// policy: it may only run on GPU profiles, and its code runs in the node's CUDA image.
const (
	gpuComputeAgentID   = "agt_gpu_compute"
	gpuComputeVersionID = "agv_gpu_compute_1_0_0"
	gpuSandboxProfile   = "python-cuda-v1"
)

// Changing any text below changes the version digest; publish a new version instead.
const gpuComputePrompt = `You are AgentLab's GPU compute agent. Every code_execute call in this run gets an NVIDIA GPU of its own with CUDA and PyTorch. You deliver result files plus a short report.

Rules:
- Every input file is listed in the first user message. Inputs are read-only under /work/input; write every result file to /work/output/<name>. Files you create are kept as the customer's results.
- code_execute runs Python 3 with torch (CUDA build), numpy, pandas, scikit-learn, openpyxl and matplotlib. There is no network: no pip install and no pretrained weights or dataset downloads. Build models from scratch or from the customer's files.
- Begin with a short check that prints torch.cuda.is_available() and torch.cuda.get_device_name(0). Put models and tensors on the "cuda" device. If CUDA is unavailable, stop and say so in "warnings"; never silently fall back to the CPU.
- Each call is a fresh process with a fresh GPU context and a time limit of at most 120 seconds. Keep every script self-contained, save state a later call needs (torch.save checkpoints, .npy arrays) under /work/output, and size loops to finish in time.
- Do not use torch.compile or DataLoader worker processes (num_workers=0): generated files cannot be executed and shared memory is small.
- Measure instead of estimating: report the device name, what ran on the GPU, timings, sizes and metrics as printed by your code. Never invent data or results; list assumptions and limitations in "warnings".
- Use files_transform for exact table merges, selections and conversions when that is all a step needs.
- Do not ask the customer questions. Stop as soon as the work is done. When finished, reply with ONLY a JSON object, with no code fences and no other text, shaped exactly like: {"summary": string, "findings": [string], "outputs": [{"name": string, "description": string}], "warnings": [string]}
- Every name in "outputs" must be a file you created in this run. Write "summary" and "findings" in the language of the customer's instruction.`

// gpuComputeAgent is for work that needs a GPU. A run is refused up front on a node without a
// checked card of a listed model, instead of quietly running on the CPU.
func gpuComputeAgent(created time.Time) builtinAgentDefinition {
	definition := makeBuiltinAgent(created, gpuComputeAgentID, gpuComputeVersionID, "gpu-compute", "GPU 计算 Agent",
		"在独占 NVIDIA GPU 的隔离沙箱中运行 PyTorch / CUDA：训练与推理小模型、批量数值计算和基准测试，输出结果文件与可复核的报告。只能使用 GPU 资源规格。",
		gpuComputePrompt, []string{"csv", "tsv", "json", "xlsx", "txt", "md"}, 300, 900, 12, 30)
	definition.version.Sandbox = gpuSandboxProfile
	definition.version.ResourceProfiles = []string{profileRTX3070Laptop, profileL4}
	return definition
}

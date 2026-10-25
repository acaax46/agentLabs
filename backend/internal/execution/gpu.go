package execution

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

type GPUInventory interface {
	List(context.Context) ([]GPUDevice, error)
}
type NvidiaInventory struct{}

func (NvidiaInventory) List(ctx context.Context) ([]GPUDevice, error) {
	cmd := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu=index,name,memory.total,memory.free", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return []GPUDevice{}, nil
	}
	var devices []GPUDevice
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, ", ")
		if len(parts) != 4 {
			continue
		}
		idx, e1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		total, e2 := strconv.Atoi(strings.TrimSpace(parts[2]))
		free, e3 := strconv.Atoi(strings.TrimSpace(parts[3]))
		if e1 == nil && e2 == nil && e3 == nil {
			devices = append(devices, GPUDevice{Index: idx, Name: strings.TrimSpace(parts[1]), TotalMemoryMB: total, FreeMemoryMB: free})
		}
	}
	return devices, nil
}

type GPULease struct {
	Devices []GPUDevice
	release func()
}

func (l GPULease) Release() {
	if l.release != nil {
		l.release()
	}
}

type GPUScheduler struct {
	mu   sync.Mutex
	inv  GPUInventory
	held map[int]string
}

func NewGPUScheduler(inv GPUInventory) *GPUScheduler {
	return &GPUScheduler{inv: inv, held: map[int]string{}}
}
func (s *GPUScheduler) Inventory(ctx context.Context) ([]GPUDevice, error) { return s.inv.List(ctx) }
func (s *GPUScheduler) Acquire(ctx context.Context, runID string, req Resources) (GPULease, error) {
	if req.GPUCount <= 0 {
		return GPULease{}, nil
	}
	devices, err := s.inv.List(ctx)
	if err != nil {
		return GPULease{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected []GPUDevice
	for _, d := range devices {
		if _, ok := s.held[d.Index]; ok {
			continue
		}
		if req.GPUModel != "" && !strings.Contains(strings.ToLower(d.Name), strings.ToLower(req.GPUModel)) {
			continue
		}
		if req.MinFreeMemoryMB > 0 && d.FreeMemoryMB < req.MinFreeMemoryMB {
			continue
		}
		selected = append(selected, d)
		if len(selected) == req.GPUCount {
			break
		}
	}
	if len(selected) != req.GPUCount {
		return GPULease{}, fmt.Errorf("GPU resources unavailable")
	}
	for _, d := range selected {
		s.held[d.Index] = runID
	}
	return GPULease{Devices: selected, release: func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, d := range selected {
			delete(s.held, d.Index)
		}
	}}, nil
}

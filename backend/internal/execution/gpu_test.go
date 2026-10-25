package execution

import (
	"context"
	"testing"
)

type fakeInventory struct{ devices []GPUDevice }

func (f fakeInventory) List(context.Context) ([]GPUDevice, error) { return f.devices, nil }
func TestGPUSchedulerLeasesByModelAndMemoryWithoutDoubleAllocation(t *testing.T) {
	scheduler := NewGPUScheduler(fakeInventory{devices: []GPUDevice{{Index: 0, Name: "RTX 3070", TotalMemoryMB: 8192, FreeMemoryMB: 7000}, {Index: 1, Name: "A100", TotalMemoryMB: 40960, FreeMemoryMB: 32000}}})
	first, err := scheduler.Acquire(context.Background(), "run-1", Resources{GPUCount: 1, GPUModel: "3070", MinFreeMemoryMB: 6000})
	if err != nil || len(first.Devices) != 1 || first.Devices[0].Index != 0 {
		t.Fatalf("first lease: %+v %v", first, err)
	}
	if _, err := scheduler.Acquire(context.Background(), "run-2", Resources{GPUCount: 1, GPUModel: "3070"}); err == nil {
		t.Fatal("same GPU allocated twice")
	}
	second, err := scheduler.Acquire(context.Background(), "run-3", Resources{GPUCount: 1, GPUModel: "A100", MinFreeMemoryMB: 30000})
	if err != nil || second.Devices[0].Index != 1 {
		t.Fatalf("second lease: %+v %v", second, err)
	}
	first.Release()
	third, err := scheduler.Acquire(context.Background(), "run-4", Resources{GPUCount: 1, GPUModel: "3070"})
	if err != nil || third.Devices[0].Index != 0 {
		t.Fatalf("released GPU not reusable: %+v %v", third, err)
	}
	second.Release()
	third.Release()
}

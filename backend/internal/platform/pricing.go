package platform

import (
	"context"
	"errors"
	"fmt"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/platform/money"
	"agentlab.local/backend/internal/platform/sandbox"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ResourceProfile is a fixed allocation, not a claim about observed CPU utilization.
// A GPU profile names one card model: GPUs are priced per card type, and a node runs the profile
// only with a device of that model that passed a real CUDA check.
type ResourceProfile struct {
	ID       string `json:"id" bson:"id"`
	Name     string `json:"name" bson:"name"`
	CPU      int    `json:"cpu" bson:"cpu"`
	MemoryMB int    `json:"memoryMB" bson:"memoryMB"`
	GPU      int    `json:"gpu" bson:"gpu"`
	GPUType  string `json:"gpuType,omitempty" bson:"gpuType,omitempty"`
	// GPUModel is matched word by word against the device names a node reports.
	GPUModel string `json:"gpuModel,omitempty" bson:"gpuModel,omitempty"`
}

const (
	profileRTX3070Laptop = "gpu-rtx3070-laptop-4-8"
	profileL4            = "gpu-l4-8-32"
)

func ResourceProfiles() []ResourceProfile {
	return []ResourceProfile{
		{ID: "cpu-1-1", Name: "基础", CPU: 1, MemoryMB: 1024},
		{ID: "cpu-2-4", Name: "标准", CPU: 2, MemoryMB: 4096},
		{ID: "cpu-4-8", Name: "计算", CPU: 4, MemoryMB: 8192},
		{ID: profileRTX3070Laptop, Name: "GPU · RTX 3070 Laptop（本机开发卡）", CPU: 4, MemoryMB: 8192, GPU: 1, GPUType: "NVIDIA GeForce RTX 3070 Laptop GPU 8 GiB", GPUModel: "RTX 3070 Laptop"},
		{ID: profileL4, Name: "GPU · L4", CPU: 8, MemoryMB: 32768, GPU: 1, GPUType: "NVIDIA L4 24 GiB", GPUModel: "L4"},
	}
}

// gpuRates are preset per-card-second rates in micro-units, by card model and currency. The local
// RTX 3070 Laptop rate exists to exercise metering on a development machine; it is not a quote.
var gpuRates = map[string]map[string]money.Micros{
	"L4":              {"USD": 400, "CNY": 2000},
	"RTX 3070 Laptop": {"USD": 120, "CNY": 600},
}

func (p ResourceProfile) sandboxResources() sandbox.Resources {
	return sandbox.Resources{CPUs: float64(p.CPU), MemoryMB: p.MemoryMB, GPU: p.GPU, GPUModel: p.GPUModel}
}

func resourceProfile(id string) (ResourceProfile, bool) {
	for _, profile := range ResourceProfiles() {
		if profile.ID == id {
			return profile, true
		}
	}
	return ResourceProfile{}, false
}

// PresetPrices is an explicit, versioned starting tariff. Importing it is an administrator action.
// OpenRouter prices are USD per million text tokens. CNY uses a fixed 7.20 conversion, no markup.
// These are platform tariffs, not supplier settlement amounts. No live price fetch occurs on a run.
func PresetPrices(provider string) []CreatePriceInput {
	at := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	models := []struct {
		id                    string
		input, cached, output money.Micros
	}{
		{"minimax/minimax-m3.1-flash", 150_000, 15_000, 600_000},
		{"deepseek/deepseek-v4.1-flash-1017", 150_000, 3_000, 600_000},
		{"z-ai/glm-5.3-flash", 150_000, 30_000, 500_000},
	}
	var prices []CreatePriceInput
	for _, currency := range []string{"USD", "CNY"} {
		convert := func(rate money.Micros) money.Micros {
			if currency == "USD" {
				return rate
			}
			value, _ := money.MulDivCeil(72, rate, 10)
			return value
		}
		for _, model := range models {
			prices = append(prices, CreatePriceInput{Kind: PriceModel, Name: "flash-20261024-" + currency + "-" + model.id, Currency: currency, Provider: provider, Model: model.id,
				InputMicros: convert(model.input), CachedInputMicros: convert(model.cached), OutputMicros: convert(model.output),
				ReferenceURL: "https://openrouter.ai/" + model.id, ReferenceAt: &at,
				Note: "OpenRouter /api/v1/models 基础价快照；非页面最低价或供应商结算；DeepSeek 时段变价不随运行浮动。CNY 按 1 USD = 7.20 CNY 固定换算，无加价。"})
		}
		// Sandbox allocations: CNY 100/vCPU-s + 25/GiB-s, USD 20/vCPU-s + 5/GiB-s, plus a per-card
		// rate for GPU profiles (see gpuRates). Integer micro-units throughout.
		cpu, memory := money.Micros(20), money.Micros(5)
		execution := money.Micros(10)
		if currency == "CNY" {
			cpu, memory, execution = 100, 25, 50
		}
		prices = append(prices, CreatePriceInput{Kind: PriceExecution, Name: "hosted-20261024-" + currency, Currency: currency, PerSecondMicros: execution, SandboxBilling: "separate", Note: "平台预设托管服务价：开始运行至结束按秒向上取整，含模型等待，不含排队；沙箱资源另列。"})
		for _, profile := range ResourceProfiles() {
			gpu := gpuRates[profile.GPUModel][currency]
			note := fmt.Sprintf("平台预设资源价，非云厂商报价；每次执行秒向上取整。vCPU/GiB 每秒微单位：%s/%s；无 GPU。", cpu, memory)
			if profile.GPU > 0 {
				note = fmt.Sprintf("平台预设资源价，非云厂商报价；每次执行秒向上取整。vCPU/GiB/%s 每卡每秒微单位：%s/%s/%s；GPU 按卡型定价，独占整卡。", profile.GPUModel, cpu, memory, gpu)
				if profile.ID == profileRTX3070Laptop {
					note += "本机开发卡，用于验证计量链路。"
				}
			}
			prices = append(prices, CreatePriceInput{Kind: PriceSandbox, Name: "sandbox-20261024-" + currency + "-" + profile.ID, Currency: currency, ResourceProfile: profile.ID,
				PerSecondMicros: money.Micros(profile.CPU)*cpu + money.Micros(profile.MemoryMB/1024)*memory + money.Micros(profile.GPU)*gpu,
				Note:            note})
		}
		for _, unit := range []string{"call", "second"} {
			rate := money.Micros(0)
			if unit == "second" {
				rate = cpu + memory
			}
			prices = append(prices, CreatePriceInput{Kind: PriceTool, Name: "code-20261024-" + currency + "-" + unit, Currency: currency, Tool: toolCodeExecute, Unit: unit, PerUnitMicros: rate,
				Note: "Agent 内调用不另收工具调度费，算力按所选沙箱规格计量；独立 Tools API 每秒价含 1 vCPU / 1 GiB 算力，不叠加沙箱或托管费。"})
		}
		prices = append(prices, CreatePriceInput{Kind: PriceTool, Name: "files-20261024-" + currency, Currency: currency, Tool: toolFilesTransform, Unit: "file", PerUnitMicros: 0, Note: "平台预设：结构化文件工具处理不另收费；内部读取、写入与调度不收费。"})
		prices = append(prices, CreatePriceInput{Kind: PriceTool, Name: "bundle-20261024-" + currency, Currency: currency, Tool: toolFilesBundle, Unit: "file", PerUnitMicros: 0, Note: "平台预设：工作区文件打包不另收费；不包含远程部署或第三方托管。"})
	}
	return prices
}

type ResourceRate struct {
	ResourceProfile
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Price     *Price `json:"price"`
}

type RateSheet struct {
	Execution *Price         `json:"execution"`
	Resources []ResourceRate `json:"resources"`
}

func (s *Service) sandboxPrice(ctx context.Context, tenant, profile, currency string) (Price, error) {
	p, err := s.currentPrice(ctx, bson.M{"tenantId": tenant, "kind": PriceSandbox, "resourceProfile": profile, "currency": currency})
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Price{}, priceMissing("沙箱规格 " + profile + "（" + currency + "）")
	}
	return p, err
}

func profileAvailable(profile ResourceProfile, info sandbox.Info, err error) (bool, string) {
	if err != nil {
		return false, "隔离执行环境不可用。"
	}
	if profile.GPU > 0 && info.MatchingGPUs(profile.GPUModel) < profile.GPU {
		if len(info.GPUs) == 0 && info.GPUReason != "" {
			return false, info.GPUReason
		}
		return false, "当前执行节点没有通过检查的 " + profile.GPUType + "。"
	}
	if info.CPUs < float64(profile.CPU) || info.MemoryMB < profile.MemoryMB {
		return false, "当前执行节点不支持此 CPU / 内存规格。"
	}
	return true, ""
}

func (s *Service) RateSheet(ctx context.Context, actor Actor, projectID string) (RateSheet, error) {
	granted, err := s.authorize(ctx, actor, projectID, "", false)
	if err != nil {
		return RateSheet{}, err
	}
	if !actor.can(ScopeUsageRead) && !actor.can(ScopeRunsRead) && !actor.can(ScopeRunsWrite) {
		return RateSheet{}, access.Denied()
	}
	sheet := RateSheet{Resources: []ResourceRate{}}
	if price, e := s.executionPrice(ctx, actor.TenantID, granted.Project.Currency); e == nil {
		sheet.Execution = &price
	} else if !isPriceMissing(e) {
		return sheet, e
	}
	info, probeErr := sandbox.Info{}, sandbox.ErrUnavailable
	if s.runner != nil {
		info, probeErr = s.runner.Probe(ctx)
	}
	for _, profile := range ResourceProfiles() {
		available, reason := profileAvailable(profile, info, probeErr)
		row := ResourceRate{ResourceProfile: profile, Available: available, Reason: reason}
		if sheet.Execution == nil || sheet.Execution.SandboxBilling != "separate" {
			row.Available, row.Reason = false, "当前托管价格包含算力；需管理员发布资源另列的托管新版本后才能选择。"
		}
		price, e := s.sandboxPrice(ctx, actor.TenantID, profile.ID, granted.Project.Currency)
		if e == nil {
			row.Price = &price
		} else if isPriceMissing(e) {
			row.Available, row.Reason = false, "管理员尚未发布此规格的价格。"
		} else {
			return sheet, e
		}
		sheet.Resources = append(sheet.Resources, row)
	}
	return sheet, nil
}

// PublishPresetPrices only fills missing currencies/dimensions; existing administrator prices win.
func (s *Service) PublishPresetPrices(ctx context.Context, actor Actor, provider string) ([]Price, error) {
	if !actor.valid() || actor.Kind != ActorUser || !actor.Admin {
		return nil, access.Denied()
	}
	if !providerPattern.MatchString(provider) {
		return nil, invalidFields("供应商标识不正确。", map[string]string{"provider": "请填写连接上的供应商标识"})
	}
	created := []Price{}
	for _, in := range PresetPrices(provider) {
		filter := bson.M{"tenantId": actor.TenantID, "kind": in.Kind, "currency": in.Currency}
		switch in.Kind {
		case PriceModel:
			filter["provider"], filter["model"] = in.Provider, in.Model
		case PriceSandbox:
			filter["resourceProfile"] = in.ResourceProfile
		case PriceTool:
			filter["tool"], filter["unit"] = in.Tool, in.Unit
		}
		// Include scheduled prices; defaults must not supersede an administrator's intent.
		if n, err := s.col(colPrices).CountDocuments(ctx, filter); err != nil {
			return nil, err
		} else if n > 0 {
			continue
		}
		price, err := s.normalizePrice(ctx, in)
		if err != nil {
			return nil, err
		}
		// Stable identity makes concurrent preset imports and response-loss retries harmless.
		identity := in.Name
		if in.Kind == PriceModel {
			identity = provider + "/" + identity
		}
		price.ID = "prc_" + sha256Hex(actor.TenantID + "/preset/" + identity)[:32]
		price.TenantID, price.CreatedBy, price.CreatedAt = actor.TenantID, actor.UserID, s.now()
		err = s.tx(ctx, func(tx context.Context) error {
			if _, e := s.col(colPrices).InsertOne(tx, price); e != nil {
				return e
			}
			return s.record(tx, actor, "price.create", "price", price.ID, "发布预设价格 "+price.Name, false)
		})
		if isDuplicate(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		created = append(created, price)
	}
	return created, nil
}

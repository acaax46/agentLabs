package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"agentlab.local/backend/internal/access"
	"agentlab.local/backend/internal/platform/ids"
	"agentlab.local/backend/internal/platform/money"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// publishCatalog stores the built-in tool and agent versions. A published version is immutable:
// if the code now describes something different under the same version, startup fails loudly
// instead of silently changing what existing integrations were promised.
func (s *Service) publishCatalog(ctx context.Context) error {
	now := s.now()
	for _, tool := range builtinTools(now) {
		tool.Digest = toolDigest(tool)
		if _, err := s.col(colTools).InsertOne(ctx, tool); err != nil {
			if !isDuplicate(err) {
				return err
			}
			var existing ToolVersion
			if err = s.col(colTools).FindOne(ctx, bson.M{"_id": tool.ID}).Decode(&existing); err != nil {
				return err
			}
			if existing.Digest != tool.Digest {
				return fmt.Errorf("tool %s is already published with different content; publish a new version instead", tool.ID)
			}
		}
	}
	for _, definition := range builtinAgents(now) {
		version := definition.version
		version.Digest = versionDigest(version)
		if _, err := s.col(colAgentVersions).InsertOne(ctx, version); err != nil {
			if !isDuplicate(err) {
				return err
			}
			var existing AgentVersion
			if err = s.col(colAgentVersions).FindOne(ctx, bson.M{"_id": version.ID}).Decode(&existing); err != nil {
				return err
			}
			if existing.Digest != version.Digest {
				return fmt.Errorf("agent version %s is already published with different content; publish a new version instead", version.ID)
			}
		}
		if _, err := s.col(colAgents).InsertOne(ctx, definition.agent); err != nil && !isDuplicate(err) {
			return err
		}
	}
	return nil
}

// AgentView is an agent with its newest published version.
type AgentView struct {
	Agent
	LatestVersion *AgentVersion `json:"latestVersion"`
}

// ListAgents returns the published agents every project can run.
func (s *Service) ListAgents(ctx context.Context, actor Actor) ([]AgentView, error) {
	if !actor.valid() {
		return nil, access.Unauthenticated()
	}
	found, err := s.col(colAgents).Find(ctx, bson.M{"status": "published"}, options.Find().SetSort(ordered("name", 1)))
	if err != nil {
		return nil, err
	}
	var agents []Agent
	if err = found.All(ctx, &agents); err != nil {
		return nil, err
	}
	views := make([]AgentView, 0, len(agents))
	for _, agent := range agents {
		view := AgentView{Agent: agent}
		var version AgentVersion
		switch err = s.col(colAgentVersions).FindOne(ctx, bson.M{"_id": agent.LatestVersionID}).Decode(&version); {
		case err == nil:
			view.LatestVersion = &version
		case !errors.Is(err, mongo.ErrNoDocuments):
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}

// GetAgent returns one agent with every published version, newest first.
func (s *Service) GetAgent(ctx context.Context, actor Actor, id string) (AgentView, []AgentVersion, error) {
	if !actor.valid() {
		return AgentView{}, nil, access.Unauthenticated()
	}
	var agent Agent
	if err := s.col(colAgents).FindOne(ctx, bson.M{"_id": id, "status": "published"}).Decode(&agent); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return AgentView{}, nil, notFound()
		}
		return AgentView{}, nil, err
	}
	found, err := s.col(colAgentVersions).Find(ctx, bson.M{"agentId": id, "status": "published"}, options.Find().SetSort(ordered("createdAt", -1)))
	if err != nil {
		return AgentView{}, nil, err
	}
	var versions []AgentVersion
	if err = found.All(ctx, &versions); err != nil {
		return AgentView{}, nil, err
	}
	view := AgentView{Agent: agent}
	for i := range versions {
		if versions[i].ID == agent.LatestVersionID {
			view.LatestVersion = &versions[i]
		}
	}
	return view, versions, nil
}

// GetAgentVersion returns one immutable published version.
func (s *Service) GetAgentVersion(ctx context.Context, actor Actor, id string) (AgentVersion, error) {
	if !actor.valid() {
		return AgentVersion{}, access.Unauthenticated()
	}
	return s.agentVersion(ctx, id)
}

func (s *Service) agentVersion(ctx context.Context, id string) (AgentVersion, error) {
	var version AgentVersion
	err := s.col(colAgentVersions).FindOne(ctx, bson.M{"_id": id, "status": "published"}).Decode(&version)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return version, notFound()
	}
	return version, err
}

// ToolView is a tool release with the prices currently in force for the caller's tenant.
type ToolView struct {
	ToolVersion
	Prices        []Price `json:"prices"`
	BillingPolicy string  `json:"billingPolicy,omitempty"`
}

func toolView(tool ToolVersion, prices []Price) ToolView {
	view := ToolView{ToolVersion: tool, Prices: prices}
	if tool.Name == toolCodeExecute {
		view.BillingPolicy = "当前计费策略：Agent 内按 call 计工具费；sandboxBilling=separate 时沙箱按冻结规格另列，旧托管价已含算力时不叠加。独立调用 second 单价含 1 vCPU / 1 GiB 算力，不叠加托管费。已发布 meters 描述保留为版本原文，实际收费以本策略和冻结价格为准。"
	}
	return view
}

// ListTools returns the newest release of every available tool.
func (s *Service) ListTools(ctx context.Context, actor Actor) ([]ToolView, error) {
	if !actor.valid() {
		return nil, access.Unauthenticated()
	}
	found, err := s.col(colTools).Find(ctx, bson.M{"status": bson.M{"$in": bson.A{"available", "beta"}}}, options.Find().SetSort(ordered("name", 1, "createdAt", -1)))
	if err != nil {
		return nil, err
	}
	var tools []ToolVersion
	if err = found.All(ctx, &tools); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	views := make([]ToolView, 0, len(tools))
	for _, tool := range tools {
		if seen[tool.Name] {
			continue
		}
		seen[tool.Name] = true
		prices, err := s.currentToolPrices(ctx, actor.TenantID, tool.Name)
		if err != nil {
			return nil, err
		}
		views = append(views, toolView(tool, prices))
	}
	return views, nil
}

// GetTool returns one tool release; an empty version means the newest.
func (s *Service) GetTool(ctx context.Context, actor Actor, name, version string) (ToolView, error) {
	if !actor.valid() {
		return ToolView{}, access.Unauthenticated()
	}
	filter := bson.M{"name": name}
	if version != "" {
		filter["version"] = version
	}
	var tool ToolVersion
	err := s.col(colTools).FindOne(ctx, filter, options.FindOne().SetSort(ordered("createdAt", -1))).Decode(&tool)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ToolView{}, notFound()
	}
	if err != nil {
		return ToolView{}, err
	}
	prices, err := s.currentToolPrices(ctx, actor.TenantID, tool.Name)
	if err != nil {
		return ToolView{}, err
	}
	return toolView(tool, prices), nil
}

func (s *Service) toolVersion(ctx context.Context, name, version string) (ToolVersion, error) {
	var tool ToolVersion
	err := s.col(colTools).FindOne(ctx, bson.M{"name": name, "version": version}).Decode(&tool)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return tool, notFound()
	}
	return tool, err
}

// currentToolPrices lists the newest in-force price per unit and currency for one tool.
func (s *Service) currentToolPrices(ctx context.Context, tenant, tool string) ([]Price, error) {
	found, err := s.col(colPrices).Find(ctx, bson.M{"tenantId": tenant, "kind": PriceTool, "tool": tool, "effectiveFrom": bson.M{"$lte": s.now()}}, options.Find().SetSort(ordered("effectiveFrom", -1)))
	if err != nil {
		return nil, err
	}
	var all []Price
	if err = found.All(ctx, &all); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	current := []Price{}
	for _, price := range all {
		key := price.Unit + "/" + price.Currency
		if !seen[key] {
			seen[key] = true
			current = append(current, price)
		}
	}
	return current, nil
}

// ---- prices ----

var (
	providerPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,79}$`)
	toolUnits       = []string{"call", "file", "second"}
)

// CreatePriceInput is the body of POST /v1/admin/prices. Rates are micro-units: per million
// tokens for models, per second for execution, per unit for tools. A cached input rate of 0
// means "same as the input rate".
type CreatePriceInput struct {
	Kind              string       `json:"kind"`
	Name              string       `json:"name"`
	Currency          string       `json:"currency"`
	Provider          string       `json:"provider"`
	Model             string       `json:"model"`
	InputMicros       money.Micros `json:"inputMicros"`
	CachedInputMicros money.Micros `json:"cachedInputMicros"`
	OutputMicros      money.Micros `json:"outputMicros"`
	PerSecondMicros   money.Micros `json:"perSecondMicros"`
	ResourceProfile   string       `json:"resourceProfile"`
	SandboxBilling    string       `json:"sandboxBilling"`
	ReferenceURL      string       `json:"referenceUrl"`
	ReferenceAt       *time.Time   `json:"referenceAt"`
	Tool              string       `json:"tool"`
	Unit              string       `json:"unit"`
	PerUnitMicros     money.Micros `json:"perUnitMicros"`
	Note              string       `json:"note"`
	EffectiveFrom     *time.Time   `json:"effectiveFrom"`
}

func (s *Service) normalizePrice(ctx context.Context, in CreatePriceInput) (Price, error) {
	fields := map[string]string{}
	name, ok := cleanText(in.Name, 1, 80)
	if !ok {
		fields["name"] = "价格版本名称为 1–80 个字符"
	}
	note, ok := cleanText(in.Note, 0, 300)
	if !ok {
		fields["note"] = "备注最多 300 个字符"
	}
	if in.Currency != "CNY" && in.Currency != "USD" {
		fields["currency"] = "必须是 CNY 或 USD"
	}
	for field, rate := range map[string]money.Micros{"inputMicros": in.InputMicros, "cachedInputMicros": in.CachedInputMicros, "outputMicros": in.OutputMicros, "perSecondMicros": in.PerSecondMicros, "perUnitMicros": in.PerUnitMicros} {
		if rate < 0 || rate > maxLimit {
			fields[field] = "必须是非负整数微单位"
		}
	}
	price := Price{ID: ids.New(ids.Price), Kind: in.Kind, Name: name, Currency: in.Currency, Note: note}
	if in.ReferenceURL != "" {
		u, err := url.Parse(in.ReferenceURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || len(in.ReferenceURL) > 500 {
			fields["referenceUrl"] = "价格来源必须是 HTTPS URL"
		}
		price.ReferenceURL = in.ReferenceURL
	}
	if in.ReferenceAt != nil {
		at := in.ReferenceAt.UTC().Truncate(time.Millisecond)
		if at.After(s.now()) {
			fields["referenceAt"] = "核对日期不能晚于当前时间"
		}
		price.ReferenceAt = &at
	}
	switch in.Kind {
	case PriceModel:
		if !providerPattern.MatchString(in.Provider) {
			fields["provider"] = "必须是连接上的供应商标识"
		}
		if strings.TrimSpace(in.Model) == "" || len(in.Model) > 160 {
			fields["model"] = "必填，最长 160 个字符"
		}
		price.Provider, price.Model = in.Provider, strings.TrimSpace(in.Model)
		price.InputMicros, price.CachedInputMicros, price.OutputMicros = in.InputMicros, in.CachedInputMicros, in.OutputMicros
	case PriceExecution:
		price.PerSecondMicros = in.PerSecondMicros
		if in.SandboxBilling != "" && in.SandboxBilling != "separate" {
			fields["sandboxBilling"] = "必须为空（包含算力）或 separate（资源另列）"
		}
		price.SandboxBilling = in.SandboxBilling
	case PriceSandbox:
		if _, ok := resourceProfile(in.ResourceProfile); !ok {
			fields["resourceProfile"] = "未知沙箱资源规格"
		}
		price.ResourceProfile, price.PerSecondMicros = in.ResourceProfile, in.PerSecondMicros
	case PriceTool:
		if known, err := s.col(colTools).CountDocuments(ctx, bson.M{"name": in.Tool}); err != nil {
			return Price{}, err
		} else if known == 0 {
			fields["tool"] = "未知工具"
		}
		if !slices.Contains(toolUnits, in.Unit) {
			fields["unit"] = "必须是 call、file 或 second"
		}
		price.Tool, price.Unit, price.PerUnitMicros = in.Tool, in.Unit, in.PerUnitMicros
	default:
		fields["kind"] = "必须是 model、execution、sandbox 或 tool"
	}
	now := s.now()
	price.EffectiveFrom = now
	if in.EffectiveFrom != nil {
		at := in.EffectiveFrom.UTC().Truncate(time.Millisecond)
		if at.Before(now.Add(-5 * time.Minute)) {
			fields["effectiveFrom"] = "价格版本不能追溯生效"
		}
		price.EffectiveFrom = at
	}
	if len(fields) > 0 {
		return Price{}, invalidFields("请检查价格版本。", fields)
	}
	return price, nil
}

// CreatePrice publishes an immutable price version. Prices change by adding versions, never by editing.
func (s *Service) CreatePrice(ctx context.Context, actor Actor, in CreatePriceInput) (Price, error) {
	if actor.Kind != ActorUser || !actor.Admin || !actor.valid() {
		return Price{}, access.Denied()
	}
	price, err := s.normalizePrice(ctx, in)
	if err != nil {
		return Price{}, err
	}
	price.TenantID, price.CreatedBy, price.CreatedAt = actor.TenantID, actor.UserID, s.now()
	err = s.tx(ctx, func(tx context.Context) error {
		if _, e := s.col(colPrices).InsertOne(tx, price); e != nil {
			return e
		}
		return s.record(tx, actor, "price.create", "price", price.ID, "发布价格版本 "+price.Name, false)
	})
	return price, err
}

// ListPrices pages through price versions, newest first. Prices are visible to every member of
// the tenant: transparency about what a run can cost is part of the product.
func (s *Service) ListPrices(ctx context.Context, actor Actor, kind, cursor string, limit int) (Page[Price], error) {
	if actor.Kind != ActorUser || !actor.valid() {
		return Page[Price]{}, access.Denied()
	}
	base := bson.M{"tenantId": actor.TenantID}
	if kind != "" {
		base["kind"] = kind
	}
	return findPage(ctx, s.col(colPrices), base, "createdAt", cursor, limit, func(p Price) (time.Time, string) { return p.CreatedAt, p.ID })
}

func priceMissing(what string) error {
	return problem("PRICE_NOT_CONFIGURED", what+"尚未配置价格版本，管理员需要先发布价格后才能计费运行。", 409)
}

func (s *Service) currentPrice(ctx context.Context, filter bson.M) (Price, error) {
	filter["effectiveFrom"] = bson.M{"$lte": s.now()}
	var price Price
	err := s.col(colPrices).FindOne(ctx, filter, options.FindOne().SetSort(ordered("effectiveFrom", -1, "createdAt", -1))).Decode(&price)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Price{}, err
	}
	return price, err
}

func (s *Service) modelPrice(ctx context.Context, tenant, provider, model, currency string) (Price, error) {
	price, err := s.currentPrice(ctx, bson.M{"tenantId": tenant, "kind": PriceModel, "provider": provider, "model": model, "currency": currency})
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Price{}, priceMissing(fmt.Sprintf("平台模型 %s（%s）", model, currency))
	}
	return price, err
}

func (s *Service) executionPrice(ctx context.Context, tenant, currency string) (Price, error) {
	price, err := s.currentPrice(ctx, bson.M{"tenantId": tenant, "kind": PriceExecution, "currency": currency})
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Price{}, priceMissing(fmt.Sprintf("托管执行（%s）", currency))
	}
	return price, err
}

func (s *Service) toolPrice(ctx context.Context, tenant, tool, unit, currency string) (Price, error) {
	price, err := s.currentPrice(ctx, bson.M{"tenantId": tenant, "kind": PriceTool, "tool": tool, "unit": unit, "currency": currency})
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Price{}, priceMissing(fmt.Sprintf("工具 %s 的 %s 计量（%s）", tool, unit, currency))
	}
	return price, err
}

// priceByID loads a frozen price version.
func (s *Service) priceByID(ctx context.Context, tenant, id string) (Price, error) {
	var price Price
	err := s.col(colPrices).FindOne(ctx, bson.M{"_id": id, "tenantId": tenant}).Decode(&price)
	return price, err
}

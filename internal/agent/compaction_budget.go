package agent

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uvwt/CycleDock/internal/modelclient"
	"github.com/uvwt/CycleDock/internal/store"
)

var (
	// ErrRouteContextCapacityExceeded 表示候选备用模型路由的已知上下文/输入上限不足以容纳当前 Prompt。
	ErrRouteContextCapacityExceeded = errors.New("model route context capacity exceeded")
	// ErrRouteContextCapacityUnknown 表示在故障切换时候选备用模型路由容量未知，且当前 Prompt 已超过未知模型的保守安全上限。
	ErrRouteContextCapacityUnknown = errors.New("model route context capacity unknown for oversized prompt")
)

const (
	defaultReservedOutputTokens  = 16384
	defaultSafetyReservePercent  = 15
	defaultMinSafetyReserve      = 16000
	defaultMinCompactAtTokens    = 8000
	defaultMaxCompactAtTokens    = 256000
	defaultUnknownInputMaxTokens = 32000
)

// CompactionBudgetPolicy 定义动态上下文压缩预算的计算策略。
// 支持按模型实际容量计算压缩点，避免所有模型固定在 24K 触发压缩；
// 同时通过 MaxCompactAtTokens 软上限避免 1M 等超大窗口模型攒到近百万 Token 才首次压缩。
type CompactionBudgetPolicy struct {
	UnknownThresholdTokens int
	UnknownMaxInputTokens  int
	DefaultReservedOutput  int
	SafetyReservePercent   int
	MinSafetyReserveTokens int
	MinCompactAtTokens     int
	MaxCompactAtTokens     int
}

func DefaultCompactionBudgetPolicy() CompactionBudgetPolicy {
	return CompactionBudgetPolicy{
		UnknownThresholdTokens: defaultCompactionTokenThreshold,
		UnknownMaxInputTokens:  defaultUnknownInputMaxTokens,
		DefaultReservedOutput:  defaultReservedOutputTokens,
		SafetyReservePercent:   defaultSafetyReservePercent,
		MinSafetyReserveTokens: defaultMinSafetyReserve,
		MinCompactAtTokens:     defaultMinCompactAtTokens,
		MaxCompactAtTokens:     defaultMaxCompactAtTokens,
	}
}

func (p CompactionBudgetPolicy) withDefaults() CompactionBudgetPolicy {
	def := DefaultCompactionBudgetPolicy()
	if p.UnknownThresholdTokens <= 0 {
		p.UnknownThresholdTokens = def.UnknownThresholdTokens
	}
	if p.UnknownMaxInputTokens <= 0 {
		p.UnknownMaxInputTokens = max(def.UnknownMaxInputTokens, p.UnknownThresholdTokens)
	}
	if p.DefaultReservedOutput <= 0 {
		p.DefaultReservedOutput = def.DefaultReservedOutput
	}
	if p.SafetyReservePercent <= 0 || p.SafetyReservePercent >= 80 {
		p.SafetyReservePercent = def.SafetyReservePercent
	}
	if p.MinSafetyReserveTokens <= 0 {
		p.MinSafetyReserveTokens = def.MinSafetyReserveTokens
	}
	if p.MinCompactAtTokens <= 0 {
		p.MinCompactAtTokens = def.MinCompactAtTokens
	}
	if p.MaxCompactAtTokens <= 0 {
		p.MaxCompactAtTokens = def.MaxCompactAtTokens
	}
	return p
}

// RouteBudget 描述单条模型路由解析后的有效输入上限、预留空间与压缩触发阈值。
type RouteBudget struct {
	Limits              modelclient.ModelLimits
	EffectiveInputLimit int
	ReservedOutput      int
	SafetyReserve       int
	CompactAtTokens     int
	UnknownCapacity     bool
}

// ResolveRouteModelLimits 按（1. user -> 2. provider -> 3. registry -> 4. unknown）解析路由容量。
func ResolveRouteModelLimits(route store.ModelRoute, client *modelclient.Client) modelclient.ModelLimits {
	var verifiedAt *time.Time
	if route.LimitsVerifiedAt.Valid {
		ts := route.LimitsVerifiedAt.Time.UTC()
		verifiedAt = &ts
	}
	stored := modelclient.ModelLimits{
		ContextWindowTokens: int(route.ContextWindowTokens.Int32),
		MaxInputTokens:      int(route.MaxInputTokens.Int32),
		MaxOutputTokens:     int(route.MaxOutputTokens.Int32),
		Source:              strings.TrimSpace(route.LimitsSource),
		VerifiedAt:          verifiedAt,
	}

	var userOverride modelclient.ModelLimits
	var providerLimits *modelclient.ModelLimits

	switch stored.Source {
	case modelclient.LimitsSourceUser:
		userOverride = stored
	case modelclient.LimitsSourceProvider:
		if stored.HasKnownCapacity() {
			copied := stored
			providerLimits = &copied
		}
	}

	if providerLimits == nil && client != nil && strings.TrimSpace(route.ProviderBaseUrl) != "" {
		if cached, ok := client.CachedProviderModelLimits(route.ProviderBaseUrl, route.Model); ok && cached != nil {
			providerLimits = cached
		}
	}

	return modelclient.ResolveModelLimits(
		userOverride,
		providerLimits,
		route.Model,
		route.BaseModel,
		time.Now().UTC(),
	)
}

// ComputeRouteCompactionBudget 根据模型上下文限制与预算策略计算动态压缩阈值：
//
//	inputLimit := min(model.MaxInputTokens, model.ContextWindowTokens - reservedOutput)
//	safetyReserve := max(inputLimit * 15 / 100, 16000)
//	compactAt := inputLimit - safetyReserve
//
// 同时兼容未知容量（走独立保守阈值，不伪造容量）、小窗口模型与共享输入/输出预算。
func ComputeRouteCompactionBudget(limits modelclient.ModelLimits, policy CompactionBudgetPolicy) RouteBudget {
	policy = policy.withDefaults()

	contextWindow := max(limits.ContextWindowTokens, 0)
	maxInput := max(limits.MaxInputTokens, 0)
	maxOutput := max(limits.MaxOutputTokens, 0)

	if contextWindow == 0 && maxInput == 0 {
		return RouteBudget{
			Limits: modelclient.ModelLimits{
				Source: modelclient.LimitsSourceUnknown,
			},
			EffectiveInputLimit: 0,
			ReservedOutput:      0,
			SafetyReserve:       max(policy.UnknownMaxInputTokens-policy.UnknownThresholdTokens, 0),
			CompactAtTokens:     policy.UnknownThresholdTokens,
			UnknownCapacity:     true,
		}
	}

	var reservedOutput int
	if contextWindow > 0 {
		reservedOutput = policy.DefaultReservedOutput
		if maxOutput > 0 && maxOutput < reservedOutput {
			reservedOutput = maxOutput
		}
		// 对小窗口模型，输出预留不超过总窗口的 25%，避免挤占全部输入空间。
		if maxQuarter := contextWindow / 4; maxQuarter > 0 && reservedOutput > maxQuarter {
			reservedOutput = maxQuarter
		}
	}

	var inputLimit int
	switch {
	case contextWindow > 0 && maxInput > 0:
		inputLimit = min(maxInput, max(contextWindow-reservedOutput, 1))
	case contextWindow > 0:
		inputLimit = max(contextWindow-reservedOutput, 1)
	default:
		inputLimit = maxInput
	}

	proportionalReserve := inputLimit * policy.SafetyReservePercent / 100
	safetyReserve := max(proportionalReserve, policy.MinSafetyReserveTokens)

	// 小窗口模型保护：若固定 16K 预留使压缩阈值过低甚至为负，退回按比例预留。
	if inputLimit-safetyReserve < policy.MinCompactAtTokens {
		safetyReserve = max(proportionalReserve, inputLimit/5, 1)
	}
	compactAt := inputLimit - safetyReserve
	if compactAt <= 0 {
		compactAt = max(inputLimit*3/4, 1)
	}
	// 对 1M 等大上下文路由限制软上限，让会话在 ~256K 即可平滑滚动压缩，
	// 同时保持 EffectiveInputLimit 为真实路由容量上限供发送前容量校验使用。
	if policy.MaxCompactAtTokens > 0 && compactAt > policy.MaxCompactAtTokens {
		compactAt = policy.MaxCompactAtTokens
	}

	return RouteBudget{
		Limits:              limits,
		EffectiveInputLimit: inputLimit,
		ReservedOutput:      reservedOutput,
		SafetyReserve:       safetyReserve,
		CompactAtTokens:     compactAt,
		UnknownCapacity:     false,
	}
}

// ValidateRoutePromptCapacity 检查候选路由是否能安全容纳当前估算的 Prompt Token 数。
// 1. 当候选路由具有已知容量但 promptTokens > EffectiveInputLimit 时，拒绝发送并返回可诊断错误。
// 2. 当处于故障切换（isFailover=true）且备用路由容量未知、同时 promptTokens 超过未知路由保守上限时，拒绝盲目发送。
func ValidateRoutePromptCapacity(
	route store.ModelRoute,
	budget RouteBudget,
	promptTokens int,
	isFailover bool,
	policy CompactionBudgetPolicy,
) error {
	if promptTokens <= 0 {
		return nil
	}
	policy = policy.withDefaults()

	if !budget.UnknownCapacity && budget.EffectiveInputLimit > 0 {
		if promptTokens > budget.EffectiveInputLimit {
			return fmt.Errorf(
				"%w: route %s (model %q, source=%s) effective input limit is %d tokens, but request prompt is ~%d tokens",
				ErrRouteContextCapacityExceeded,
				route.ID,
				strings.TrimSpace(route.Model),
				budget.Limits.Source,
				budget.EffectiveInputLimit,
				promptTokens,
			)
		}
		return nil
	}

	if isFailover && budget.UnknownCapacity && promptTokens > policy.UnknownMaxInputTokens {
		return fmt.Errorf(
			"%w: backup route %s (model %q) has unknown context window and cannot safely accept oversized prompt (~%d tokens > %d conservative limit)",
			ErrRouteContextCapacityUnknown,
			route.ID,
			strings.TrimSpace(route.Model),
			promptTokens,
			policy.UnknownMaxInputTokens,
		)
	}
	return nil
}

package decision

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// MaxMarginUsagePct 定义总保证金使用率的硬上限（百分比）
const MaxMarginUsagePct = 90.0

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol               string  `json:"symbol"`
	Side                 string  `json:"side"` // "long" or "short"
	EntryPrice           float64 `json:"entry_price"`
	MarkPrice            float64 `json:"mark_price"`
	Quantity             float64 `json:"quantity"`
	Leverage             int     `json:"leverage"`
	UnrealizedPnL        float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct     float64 `json:"unrealized_pnl_pct"`
	PeakUnrealizedPnLPct float64 `json:"peak_unrealized_pnl_pct,omitempty"`
	PeakUnrealizedPnLUSD float64 `json:"peak_unrealized_pnl_usd,omitempty"`
	DrawdownFromPeakPct  float64 `json:"drawdown_from_peak_pct,omitempty"`
	DrawdownFromPeakUSD  float64 `json:"drawdown_from_peak_usd,omitempty"`
	LiquidationPrice     float64 `json:"liquidation_price"`
	MarginUsed           float64 `json:"margin_used"`
	UpdateTime           int64   `json:"update_time"` // 持仓更新时间戳（毫秒）
}

// AccountInfo 账户信息
type AccountInfo struct {
	TotalEquity      float64 `json:"total_equity"`      // 账户净值
	AvailableBalance float64 `json:"available_balance"` // 可用余额
	TotalPnL         float64 `json:"total_pnl"`         // 总盈亏
	TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
	MarginUsed       float64 `json:"margin_used"`       // 已用保证金
	MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
	PositionCount    int     `json:"position_count"`    // 持仓数量
}

// CandidateCoin 候选币种（来自币种池）
type CandidateCoin struct {
	Symbol  string   `json:"symbol"`
	Sources []string `json:"sources"` // 来源: "ai500" 和/或 "oi_top"
}

// OITopData 持仓量增长Top数据（用于AI决策参考）
type OITopData struct {
	Rank              int     // OI Top排名
	OIDeltaPercent    float64 // 持仓量变化百分比（1小时）
	OIDeltaValue      float64 // 持仓量变化价值
	PriceDeltaPercent float64 // 价格变化百分比
	NetLong           float64 // 净多仓
	NetShort          float64 // 净空仓
}

// Context 交易上下文（传递给AI的完整信息）
type Context struct {
	CurrentTime      string                   `json:"current_time"`
	RuntimeMinutes   int                      `json:"runtime_minutes"`
	CallCount        int                      `json:"call_count"`
	Account          AccountInfo              `json:"account"`
	PerformanceState string                   `json:"-"` // derived risk state (drawdown/loss_streak/positive)
	SharpeCooling    string                   `json:"-"` // derived cooling level (only_high_confidence_trades/halt_xx)
	Positions        []PositionInfo           `json:"positions"`
	CandidateCoins   []CandidateCoin          `json:"candidate_coins"`
	MarketDataMap    map[string]*market.Data  `json:"-"` // 不序列化，但内部使用
	OITopDataMap     map[string]*OITopData    `json:"-"` // OI Top数据映射
	Performance      interface{}              `json:"-"` // 历史表现分析（logger.PerformanceAnalysis）
	BTCETHLeverage   int                      `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage  int                      `json:"-"` // 山寨币杠杆倍数（从配置读取）
	SystemPromptPath string                   `json:"-"` // 自定义系统提示词路径
	AuxOpinions      []AuxOpinion             `json:"-"` // 辅助模型意见
	AuxConsensus     *AuxConsensus            `json:"-"` // 辅助模型共识
	EnsembleMode     string                   `json:"-"`
	EnsembleSummary  string                   `json:"-"`
	RecentRiskAlerts []RiskFlag               `json:"-"`
	RecentGuardrails []MarketGuardrailWarning `json:"-"`
}

// AuxConsensus 描述辅助模型之间的总体意见
type AuxConsensus struct {
	TotalModels int
	OpenCount   int
	WaitCount   int
	Majority    string // open / wait / mixed

	OpenModels []string
	WaitModels []string

	SymbolVotes map[string]*AuxSymbolConsensus
}

// AuxSymbolConsensus 记录每个币种在不同方向上的支持模型
type AuxSymbolConsensus struct {
	LongModels  []string
	ShortModels []string
}

// ComputeAuxConsensus 统计辅助模型意见的整体状况
func ComputeAuxConsensus(opinions []AuxOpinion) *AuxConsensus {
	if len(opinions) == 0 {
		return nil
	}

	result := &AuxConsensus{
		TotalModels: len(opinions),
		SymbolVotes: make(map[string]*AuxSymbolConsensus),
	}

	for _, opinion := range opinions {
		modelName := strings.TrimSpace(opinion.ModelName)
		if modelName == "" {
			modelName = strings.TrimSpace(opinion.ModelID)
		}
		if modelName == "" {
			modelName = "aux_model"
		}

		hasOpenAction := false
		for _, vote := range opinion.Decisions {
			if !isDecisionOpenAction(vote.Action) || vote.Symbol == "" {
				continue
			}
			hasOpenAction = true
			symbol := strings.ToUpper(strings.TrimSpace(vote.Symbol))
			if symbol == "" {
				continue
			}

			slot, exists := result.SymbolVotes[symbol]
			if !exists {
				slot = &AuxSymbolConsensus{}
				result.SymbolVotes[symbol] = slot
			}

			switch vote.Action {
			case "open_long":
				slot.LongModels = appendIfMissing(slot.LongModels, modelName)
			case "open_short":
				slot.ShortModels = appendIfMissing(slot.ShortModels, modelName)
			}
		}

		if hasOpenAction {
			result.OpenCount++
			result.OpenModels = append(result.OpenModels, modelName)
		} else {
			result.WaitCount++
			result.WaitModels = append(result.WaitModels, modelName)
		}
	}

	switch {
	case result.WaitCount*2 > result.TotalModels:
		result.Majority = "wait"
	case result.OpenCount*2 > result.TotalModels:
		result.Majority = "open"
	default:
		result.Majority = "mixed"
	}

	if len(result.SymbolVotes) == 0 {
		result.SymbolVotes = nil
	}

	return result
}

// Decision AI的交易决策
type Decision struct {
	Symbol            string             `json:"symbol"`
	Action            string             `json:"action"` // "open_long", "open_short", "close_long", "close_short", "hold", "wait"
	Leverage          int                `json:"leverage,omitempty"`
	PositionSizeUSD   float64            `json:"position_size_usd,omitempty"`
	StopLoss          float64            `json:"stop_loss,omitempty"`
	TakeProfit        float64            `json:"take_profit,omitempty"`
	TakeProfitTargets []TakeProfitTarget `json:"tp_targets,omitempty"`
	StrategyHint      string             `json:"strategy_hint,omitempty"`
	Confidence        int                `json:"confidence,omitempty"` // 信心度 (0-100)
	RiskUSD           float64            `json:"risk_usd,omitempty"`   // 最大美元风险
	Reasoning         string             `json:"reasoning"`
}

type decisionAlias Decision

func (d *Decision) UnmarshalJSON(data []byte) error {
	type raw struct {
		*decisionAlias
		Leverage interface{} `json:"leverage"`
	}

	aux := &raw{
		decisionAlias: (*decisionAlias)(d),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	if aux.Leverage == nil {
		return nil
	}

	switch v := aux.Leverage.(type) {
	case float64:
		d.Leverage = int(math.Round(v))
	case json.Number:
		if f, err := v.Float64(); err == nil {
			d.Leverage = int(math.Round(f))
		}
	case int:
		d.Leverage = v
	case int64:
		d.Leverage = int(v)
	case uint64:
		d.Leverage = int(v)
	case string:
		if num, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			d.Leverage = int(math.Round(num))
		}
	default:
		// 保持原值，不抛错
	}

	if d.Leverage < 0 {
		d.Leverage = 0
	}

	return nil
}

// TakeProfitTarget 分批止盈目标
type TakeProfitTarget struct {
	Price   float64 `json:"price"`
	SizePct float64 `json:"size_pct,omitempty"`
	SizeUSD float64 `json:"size_usd,omitempty"`
	Kind    string  `json:"kind,omitempty"`
}

// AuxOpinion 辅助模型意见
type AuxOpinion struct {
	ModelID   string     `json:"model_id"`
	ModelName string     `json:"model_name,omitempty"`
	Provider  string     `json:"provider,omitempty"`
	Weight    float64    `json:"weight,omitempty"`
	Role      string     `json:"role,omitempty"`
	Summary   string     `json:"summary,omitempty"`
	Decisions []Decision `json:"decisions,omitempty"`
	Notes     string     `json:"notes,omitempty"`
}

// RiskFlag 风险告警信息（用于风控复核）
type RiskFlag struct {
	Symbol   string `json:"symbol"`
	Action   string `json:"action"`
	Issue    string `json:"issue"`
	Severity string `json:"severity"`         // high / medium / low
	Detail   string `json:"detail,omitempty"` // 额外描述
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	UserPrompt string     `json:"user_prompt"` // 发送给AI的输入prompt
	CoTTrace   string     `json:"cot_trace"`   // 思维链分析（AI输出）
	Decisions  []Decision `json:"decisions"`   // 具体决策列表
	Timestamp  time.Time  `json:"timestamp"`
}

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
func GetFullDecision(ctx *Context, mcpClient *mcp.Client) (*FullDecision, error) {
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPrompt(ctx.SystemPromptPath, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）
	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.MarketDataMap)
	if err != nil {
		return nil, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.UserPrompt = userPrompt // 保存输入prompt
	return decision, nil
}

// ReviewDecisions 二次风控复核（基于风险告警复查决策）
func ReviewDecisions(ctx *Context, baseDecision *FullDecision, flags []RiskFlag, mcpClient *mcp.Client) (*FullDecision, error) {
	if baseDecision == nil {
		return nil, fmt.Errorf("基础决策为空，无法复核")
	}
	if len(flags) == 0 {
		return baseDecision, nil
	}

	systemPrompt := buildReviewSystemPrompt()
	userPrompt := buildReviewUserPrompt(ctx, baseDecision, flags)

	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("调用风控复核AI失败: %w", err)
	}

	reviewedDecision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, ctx.MarketDataMap)
	if err != nil {
		return nil, fmt.Errorf("解析风控复核响应失败: %w", err)
	}

	reviewedDecision.Timestamp = time.Now()
	reviewedDecision.UserPrompt = userPrompt
	return reviewedDecision, nil
}

// fetchMarketDataForContext 为上下文中的所有币种获取市场数据和OI数据
func fetchMarketDataForContext(ctx *Context) error {
	ctx.MarketDataMap = make(map[string]*market.Data)
	ctx.OITopDataMap = make(map[string]*OITopData)

	// 收集所有需要获取数据的币种
	symbolSet := make(map[string]bool)

	// 1. 优先获取持仓币种的数据（这是必须的）
	for _, pos := range ctx.Positions {
		symbolSet[pos.Symbol] = true
	}

	// 2. 候选币种数量根据账户状态动态调整
	maxCandidates := calculateMaxCandidates(ctx)
	for i, coin := range ctx.CandidateCoins {
		if i >= maxCandidates {
			break
		}
		symbolSet[coin.Symbol] = true
	}

	// 并发获取市场数据
	// 持仓币种集合（用于判断是否跳过OI检查）
	positionSymbols := make(map[string]bool)
	for _, pos := range ctx.Positions {
		positionSymbols[pos.Symbol] = true
	}

	for symbol := range symbolSet {
		data, err := market.Get(symbol)
		if err != nil {
			// 单个币种失败不影响整体，只记录错误
			continue
		}

		// ⚠️ 流动性过滤：持仓价值低于15M USD的币种不做（多空都不做）
		// 持仓价值 = 持仓量 × 当前价格
		// 但现有持仓必须保留（需要决策是否平仓）
		isExistingPosition := positionSymbols[symbol]
		if !isExistingPosition && data.OpenInterest != nil && data.CurrentPrice > 0 {
			// 计算持仓价值（USD）= 持仓量 × 当前价格
			oiValue := data.OpenInterest.Latest * data.CurrentPrice
			oiValueInMillions := oiValue / 1_000_000 // 转换为百万美元单位
			if oiValueInMillions < 15 {
				log.Printf("⚠️  %s 持仓价值过低(%.2fM USD < 15M)，跳过此币种 [持仓量:%.0f × 价格:%.4f]",
					symbol, oiValueInMillions, data.OpenInterest.Latest, data.CurrentPrice)
				continue
			}
		}

		guardrails := GuardrailWarningsForMarket(data)
		if len(guardrails) > 0 {
			ctx.RecentGuardrails = appendGuardrailHistory(ctx.RecentGuardrails, guardrails...)
			if !isExistingPosition && hasHighSeverityGuardrail(guardrails) {
				log.Printf("🧭  %s 命中高风险 guardrail，仍保留候选但需关注: %s", symbol, summarizeGuardrails(guardrails))
			}
		}

		ctx.MarketDataMap[symbol] = data
	}

	// 加载OI Top数据（不影响主流程）
	oiPositions, err := pool.GetOITopPositions()
	if err == nil {
		for _, pos := range oiPositions {
			// 标准化符号匹配
			symbol := pos.Symbol
			ctx.OITopDataMap[symbol] = &OITopData{
				Rank:              pos.Rank,
				OIDeltaPercent:    pos.OIDeltaPercent,
				OIDeltaValue:      pos.OIDeltaValue,
				PriceDeltaPercent: pos.PriceDeltaPercent,
				NetLong:           pos.NetLong,
				NetShort:          pos.NetShort,
			}
		}
	}

	return nil
}

// calculateMaxCandidates 根据账户状态计算需要分析的候选币种数量
func calculateMaxCandidates(ctx *Context) int {
	// 直接返回候选池的全部币种数量
	// 因为候选池已经在 auto_trader.go 中筛选过了
	// 固定分析前20个评分最高的币种（来自AI500）
	return len(ctx.CandidateCoins)
}

// buildSystemPrompt 构建 System Prompt（从模板读取，失败时回退）
func buildSystemPrompt(customPromptPath string, accountEquity float64, btcEthLeverage, altcoinLeverage int) string {
	defaultPromptPath := filepath.Join("prompts", "system_prompt.txt")
	promptPath := strings.TrimSpace(customPromptPath)
	if promptPath == "" {
		promptPath = defaultPromptPath
	} else {
		promptPath = filepath.Clean(promptPath)
		if !filepath.IsAbs(promptPath) && !strings.ContainsRune(promptPath, os.PathSeparator) {
			// 若仅提供文件名，则默认使用 prompts 目录
			promptPath = filepath.Join("prompts", promptPath)
		}
	}

	templateBytes, err := os.ReadFile(promptPath)
	if err != nil {
		if promptPath != defaultPromptPath {
			log.Printf("⚠️  无法读取自定义系统提示词文件 %s: %v，回退默认模板", promptPath, err)
			templateBytes, err = os.ReadFile(defaultPromptPath)
		}
		if err != nil {
			log.Printf("⚠️  无法读取系统提示词文件 %s: %v，使用内置提示词", defaultPromptPath, err)
			return buildDefaultSystemPrompt(accountEquity, btcEthLeverage, altcoinLeverage)
		}
	}

	template := string(templateBytes)

	altcoinMin := accountEquity * 0.8
	altcoinMax := accountEquity * 1.5
	btcethMin := accountEquity * 5
	btcethMax := accountEquity * 10
	exampleSize := accountEquity * 5

	replacements := map[string]string{
		"{ALTCOIN_MIN}":      fmt.Sprintf("%.0f", altcoinMin),
		"{ALTCOIN_MAX}":      fmt.Sprintf("%.0f", altcoinMax),
		"{BTCETH_MIN}":       fmt.Sprintf("%.0f", btcethMin),
		"{BTCETH_MAX}":       fmt.Sprintf("%.0f", btcethMax),
		"{ALTCOIN_LEVERAGE}": fmt.Sprintf("%d", altcoinLeverage),
		"{BTCETH_LEVERAGE}":  fmt.Sprintf("%d", btcEthLeverage),
		"{EXAMPLE_SIZE}":     fmt.Sprintf("%.0f", exampleSize),
	}

	result := template
	for placeholder, value := range replacements {
		result = strings.ReplaceAll(result, placeholder, value)
	}

	return result
}

// buildDefaultSystemPrompt 构建默认的系统提示词（当模板文件缺失时使用）
func buildDefaultSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int) string {
	var sb strings.Builder

	// === 核心使命 ===
	sb.WriteString("你是专业的加密货币交易AI，在币安合约市场进行自主交易。\n\n")
	sb.WriteString("# 📥 输入格式\n\n")
	sb.WriteString("- 用户 prompt 会提供结构化 JSON 快照，字段含 runtime/account/risk_guardrails/performance/open_positions/market/candidate_priority/recent_risk_alerts/recent_guardrails。\n")
	sb.WriteString("- 先解析 JSON，确保所有仓位、风险与候选信号满足约束，再做决策。\n")
	sb.WriteString("- 若风险字段提示需要降频或观望（例如夏普为负），必须遵守。\n\n")
	sb.WriteString("# 🎯 核心目标\n\n")
	sb.WriteString("**最大化夏普比率（Sharpe Ratio）**\n\n")
	sb.WriteString("夏普比率 = 平均收益 / 收益波动率\n\n")
	sb.WriteString("**这意味着**：\n")
	sb.WriteString("- ✅ 高质量交易（高胜率、大盈亏比）→ 提升夏普\n")
	sb.WriteString("- ✅ 稳定收益、控制回撤 → 提升夏普\n")
	sb.WriteString("- ✅ 耐心持仓、让利润奔跑 → 提升夏普\n")
	sb.WriteString("- ❌ 频繁交易、小盈小亏 → 增加波动，严重降低夏普\n")
	sb.WriteString("- ❌ 过度交易、手续费损耗 → 直接亏损\n")
	sb.WriteString("- ❌ 过早平仓、频繁进出 → 错失大行情\n\n")
	sb.WriteString("**关键认知**: 系统每3分钟扫描一次，但不意味着每次都要交易！\n")
	sb.WriteString("大多数时候应该是 `wait` 或 `hold`，只在极佳机会时才开仓。\n\n")

	// === 硬约束（风险控制）===
	sb.WriteString("# ⚖️ 硬约束（风险控制）\n\n")
	sb.WriteString("1. **风险回报比**: 趋势脚本 ≥ 1:2.5；区间脚本首目标 ≥ 1:1.8，并计划通过分批扩展到 ≥ 1:2\n")
	sb.WriteString("2. **最多持仓**: 3个币种（质量>数量）\n")
	sb.WriteString(fmt.Sprintf("3. **单币仓位**: 山寨%.0f-%.0f U(%dx杠杆) | BTC/ETH %.0f-%.0f U(%dx杠杆)\n",
		accountEquity*0.8, accountEquity*1.5, altcoinLeverage, accountEquity*5, accountEquity*10, btcEthLeverage))
	sb.WriteString(fmt.Sprintf("4. **保证金**: 总使用率 ≤ %.0f%%\n\n", MaxMarginUsagePct))

	// === 做空激励 ===
	sb.WriteString("# 📉 做多做空平衡\n\n")
	sb.WriteString("**重要**: 下跌趋势做空的利润 = 上涨趋势做多的利润\n\n")
	sb.WriteString("- 上涨趋势 → 做多\n")
	sb.WriteString("- 下跌趋势 → 做空\n")
	sb.WriteString("- 震荡市场 → 观望\n\n")
	sb.WriteString("**不要有做多偏见！做空是你的核心工具之一**\n\n")

	// === 交易频率认知 ===
	sb.WriteString("# ⏱️ 交易频率认知\n\n")
	sb.WriteString("**量化标准**:\n")
	sb.WriteString("- 优秀交易员：每天2-4笔 = 每小时0.1-0.2笔\n")
	sb.WriteString("- 过度交易：每小时>2笔 = 严重问题\n")
	sb.WriteString("- 最佳节奏：开仓后持有至少30-60分钟\n\n")
	sb.WriteString("**自查**:\n")
	sb.WriteString("如果你发现自己每个周期都在交易 → 说明标准太低\n")
	sb.WriteString("如果你发现持仓<30分钟就平仓 → 说明太急躁\n\n")

	// === 开仓信号强度 ===
	sb.WriteString("# 🎯 开仓标准（严格）\n\n")
	sb.WriteString("只在**强信号**时开仓，不确定就观望。\n\n")
	sb.WriteString("**你拥有的完整数据**：\n")
	sb.WriteString("- 📊 **原始序列**：3分钟价格序列(MidPrices数组) + 4小时K线序列\n")
	sb.WriteString("- 📈 **技术序列**：EMA20序列、MACD序列、RSI7序列、RSI14序列\n")
	sb.WriteString("- 💰 **资金序列**：成交量序列、持仓量(OI)序列、资金费率\n")
	sb.WriteString("- 🎯 **筛选标记**：AI500评分 / OI_Top排名（如果有标注）\n\n")
	sb.WriteString("**分析方法**（完全由你自主决定）：\n")
	sb.WriteString("- 自由运用序列数据，你可以做但不限于趋势分析、形态识别、支撑阻力、技术阻力位、斐波那契、波动带计算\n")
	sb.WriteString("- 多维度交叉验证（价格+量+OI+指标+序列形态）\n")
	sb.WriteString("- 用你认为最有效的方法发现高确定性机会\n")
	sb.WriteString("- 综合信心度 ≥ 75 才开仓\n\n")
	sb.WriteString("**避免低质量信号**：\n")
	sb.WriteString("- 单一维度（只看一个指标）\n")
	sb.WriteString("- 相互矛盾（涨但量萎缩）\n")
	sb.WriteString("- 横盘震荡\n")
	sb.WriteString("- 刚平仓不久（<15分钟）\n\n")

	// === 夏普比率自我进化 ===
	sb.WriteString("# 🧬 夏普比率自我进化\n\n")
	sb.WriteString("每次你会收到**夏普比率**作为绩效反馈（周期级别）：\n\n")
	sb.WriteString("**夏普比率 < -0.5** (持续亏损):\n")
	sb.WriteString("  → 🛑 停止交易，连续观望至少6个周期（18分钟）\n")
	sb.WriteString("  → 🔍 深度反思：\n")
	sb.WriteString("     • 交易频率过高？（每小时>2次就是过度）\n")
	sb.WriteString("     • 持仓时间过短？（<30分钟就是过早平仓）\n")
	sb.WriteString("     • 信号强度不足？（信心度<75）\n")
	sb.WriteString("     • 是否在做空？（单边做多是错误的）\n\n")
	sb.WriteString("**夏普比率 -0.5 ~ 0** (轻微亏损):\n")
	sb.WriteString("  → ⚠️ 严格控制：只做信心度>80的交易\n")
	sb.WriteString("  → 减少交易频率：每小时最多1笔新开仓\n")
	sb.WriteString("  → 耐心持仓：至少持有30分钟以上\n\n")
	sb.WriteString("**夏普比率 0 ~ 0.7** (正收益):\n")
	sb.WriteString("  → ✅ 维持当前策略\n\n")
	sb.WriteString("**夏普比率 > 0.7** (优异表现):\n")
	sb.WriteString("  → 🚀 可适度扩大仓位\n\n")
	sb.WriteString("**关键**: 夏普比率是唯一指标，它会自然惩罚频繁交易和过度进出。\n\n")

	// === 决策流程 ===
	sb.WriteString("# 📋 决策流程\n\n")
	sb.WriteString("1. **分析夏普比率**: 当前策略是否有效？需要调整吗？\n")
	sb.WriteString("2. **评估持仓**: 趋势是否改变？是否该止盈/止损？\n")
	sb.WriteString("3. **寻找新机会**: 有强信号吗？多空机会？\n")
	sb.WriteString("4. **输出决策**: 思维链分析 + JSON\n\n")

	// === 输出格式 ===
	sb.WriteString("# 📤 输出格式\n\n")
	sb.WriteString("**第一步: 思维链（纯文本）**\n")
	sb.WriteString("简洁分析你的思考过程\n\n")
	sb.WriteString("**第二步: JSON决策数组**\n\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"下跌趋势+MACD死叉\"},\n", btcEthLeverage, accountEquity*5))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"止盈离场\"}\n")
	sb.WriteString("]\n```\n\n")
	sb.WriteString("**字段说明**:\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | hold | wait\n")
	sb.WriteString("- `confidence`: 0-100（开仓建议≥75）\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n\n")

	// === 关键提醒 ===
	sb.WriteString("---\n\n")
	sb.WriteString("**记住**: \n")
	sb.WriteString("- 目标是夏普比率，不是交易频率\n")
	sb.WriteString("- 做空 = 做多，都是赚钱工具\n")
	sb.WriteString("- 宁可错过，不做低质量交易\n")
	sb.WriteString("- 趋势脚本RR < 1:2.5 或 区间首目标RR < 1:1.8 → 不开仓\n")

	return sb.String()
}

func buildReviewSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString("你是加密货币量化交易团队的首席风控官。\n")
	sb.WriteString("- 你的任务是审查前一阶段 AI 拟定的交易决策。\n")
	sb.WriteString("- 若决策违反风险约束（仓位、风险预算、降频要求等）或缺乏说服力，你必须删除或调整。\n")
	sb.WriteString("- 优先保护资金安全：可以将高风险操作改为 wait/hold，或降低仓位、提高止损质量。\n")
	sb.WriteString("- 最终输出的 JSON 决策数组必须满足所有约束，并配合清晰的调整理由。\n")
	return sb.String()
}

func buildReviewUserPrompt(ctx *Context, baseDecision *FullDecision, flags []RiskFlag) string {
	payload := struct {
		Snapshot          promptSnapshot `json:"snapshot"`
		ProposedDecisions []Decision     `json:"proposed_decisions"`
		RiskFlags         []RiskFlag     `json:"risk_flags"`
	}{
		Snapshot:          buildPromptSnapshot(ctx),
		ProposedDecisions: baseDecision.Decisions,
		RiskFlags:         flags,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		data = []byte("{}")
	}

	var sb strings.Builder
	sb.WriteString("风控复核请求：请审查拟执行的交易，处理风险告警，确保输出的最终方案安全可靠。\n\n")
	sb.WriteString("```json\n")
	sb.Write(data)
	sb.WriteString("\n```\n\n")
	sb.WriteString("要求：\n")
	sb.WriteString("1. 针对 severity=\"high\" 的告警必须采取措施（删除、降仓、观望）。\n")
	sb.WriteString("2. 若夏普或账户状态要求降频/观望，严格执行。\n")
	sb.WriteString("3. 输出格式与主流程一致：先给出思维链，再给出 JSON 决策数组。\n")
	return sb.String()
}

const (
	maxPromptMarkets            = 18
	maxRecentTradesInPrompt     = 5
	MaxRecentGuardrailSnapshots = 16
)

type promptRuntime struct {
	CurrentTime    string `json:"current_time"`
	RuntimeMinutes int    `json:"runtime_minutes"`
	CallCount      int    `json:"call_count"`
}

type promptAccount struct {
	TotalEquity      float64 `json:"total_equity"`
	AvailableBalance float64 `json:"available_balance"`
	AvailablePct     float64 `json:"available_pct"`
	TotalPnLPct      float64 `json:"total_pnl_pct"`
	MarginUsedPct    float64 `json:"margin_used_pct"`
	PositionCount    int     `json:"position_count"`
}

type promptRisk struct {
	MaxPositions       int                `json:"max_positions"`
	OpenPositions      int                `json:"open_positions"`
	SlotsRemaining     int                `json:"slots_remaining"`
	MarginHeadroomPct  float64            `json:"margin_headroom_pct"`
	RiskBudgetUSD      float64            `json:"risk_budget_usd"`
	MaxPositionUSD     map[string]float64 `json:"max_position_usd"`
	MaxLeverage        map[string]int     `json:"max_leverage"`
	SharpeCoolingLevel string             `json:"sharpe_cooling_level,omitempty"`
	PerformanceState   string             `json:"performance_state,omitempty"`
}

type promptPosition struct {
	Symbol               string  `json:"symbol"`
	Side                 string  `json:"side"`
	EntryPrice           float64 `json:"entry_price"`
	MarkPrice            float64 `json:"mark_price"`
	Leverage             int     `json:"leverage"`
	Quantity             float64 `json:"quantity"`
	PositionValue        float64 `json:"position_value"`
	MarginUsed           float64 `json:"margin_used"`
	UnrealizedPnLPct     float64 `json:"unrealized_pnl_pct"`
	PeakUnrealizedPnLPct float64 `json:"peak_unrealized_pnl_pct,omitempty"`
	PeakUnrealizedPnLUSD float64 `json:"peak_unrealized_pnl_usd,omitempty"`
	DrawdownFromPeakPct  float64 `json:"drawdown_from_peak_pct,omitempty"`
	DrawdownFromPeakUSD  float64 `json:"drawdown_from_peak_usd,omitempty"`
	LiquidationPrice     float64 `json:"liquidation_price"`
	HoldMinutes          int     `json:"hold_minutes"`
}

type promptRecentTrade struct {
	Symbol      string  `json:"symbol"`
	Side        string  `json:"side"`
	PnL         float64 `json:"pnl"`
	PnLPct      float64 `json:"pnl_pct"`
	Duration    string  `json:"duration"`
	StopLossHit bool    `json:"stop_loss_hit"`
}

type promptPerformance struct {
	SharpeRatio      float64             `json:"sharpe_ratio"`
	TotalTrades      int                 `json:"total_trades"`
	WinRate          float64             `json:"win_rate"`
	ProfitFactor     float64             `json:"profit_factor"`
	AvgWin           float64             `json:"avg_win"`
	AvgLoss          float64             `json:"avg_loss"`
	RecentPnL        float64             `json:"recent_pn_l,omitempty"`
	RecentWinStreak  int                 `json:"recent_win_streak,omitempty"`
	RecentLossStreak int                 `json:"recent_loss_streak,omitempty"`
	BestSymbol       string              `json:"best_symbol,omitempty"`
	WorstSymbol      string              `json:"worst_symbol,omitempty"`
	RecentTrades     []promptRecentTrade `json:"recent_trades,omitempty"`
}

type promptOpenInterest struct {
	Latest  float64 `json:"latest"`
	Average float64 `json:"average"`
	Ratio   float64 `json:"ratio"`
}

type promptVolatility struct {
	ATR3     float64 `json:"atr3"`
	ATR14    float64 `json:"atr14"`
	ATR14Pct float64 `json:"atr14_pct"`
}

type promptTimeframe struct {
	EMA20     float64 `json:"ema20"`
	EMA50     float64 `json:"ema50"`
	MACD      float64 `json:"macd"`
	RSI14     float64 `json:"rsi14"`
	TrendBias string  `json:"trend_bias"`
}

type promptIntraday struct {
	MACD       float64 `json:"macd"`
	RSI7       float64 `json:"rsi7"`
	PriceSlope float64 `json:"price_slope_pct"`
}

type promptRangeState struct {
	Timeframe         string  `json:"timeframe"`
	LookbackCandles   int     `json:"lookback_candles"`
	LookbackMinutes   int     `json:"lookback_minutes"`
	AgeBars1h         int     `json:"age_bars_1h,omitempty"`
	High              float64 `json:"high"`
	Low               float64 `json:"low"`
	Mid               float64 `json:"mid"`
	Width             float64 `json:"width"`
	WidthPct          float64 `json:"width_pct"`
	WidthToATR14      float64 `json:"width_to_atr14"`
	ATR14             float64 `json:"atr14"`
	VWAP              float64 `json:"vwap"`
	TouchesHigh       int     `json:"touches_high"`
	TouchesLow        int     `json:"touches_low"`
	DistanceToHighPct float64 `json:"distance_to_high_pct"`
	DistanceToLowPct  float64 `json:"distance_to_low_pct"`
	PriceLocation     string  `json:"price_location"`
	Regime            string  `json:"regime"`
	ADX144h           float64 `json:"adx14_4h,omitempty"`
	BBP1h             float64 `json:"bbp_1h,omitempty"`
}

// MarketGuardrailWarning 市场硬性约束提示
type MarketGuardrailWarning struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Detail   string `json:"detail,omitempty"`
}

type promptMarket struct {
	Symbol            string                   `json:"symbol"`
	Sources           []string                 `json:"sources,omitempty"`
	TrendBias         string                   `json:"trend_bias"`
	Price             float64                  `json:"price"`
	ChangePct         map[string]float64       `json:"change_pct"`
	MomentumBias      map[string]string        `json:"momentum_bias,omitempty"`
	FundingRate       float64                  `json:"funding_rate"`
	OpenInterest      *promptOpenInterest      `json:"open_interest,omitempty"`
	Volatility        *promptVolatility        `json:"volatility,omitempty"`
	M15               *promptTimeframe         `json:"m15,omitempty"`
	H1                *promptTimeframe         `json:"h1,omitempty"`
	H4                *promptTimeframe         `json:"h4,omitempty"`
	Intraday          *promptIntraday          `json:"intraday,omitempty"`
	RangeState        *promptRangeState        `json:"range_state,omitempty"`
	ConfidenceFlags   []string                 `json:"confidence_flags,omitempty"`
	GuardrailWarnings []MarketGuardrailWarning `json:"guardrail_warnings,omitempty"`
}

type promptAuxOpinion struct {
	ModelID   string            `json:"model_id"`
	ModelName string            `json:"model_name,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Weight    float64           `json:"weight,omitempty"`
	Role      string            `json:"role,omitempty"`
	Summary   string            `json:"summary,omitempty"`
	Votes     []promptAuxAction `json:"votes,omitempty"`
	Notes     string            `json:"notes,omitempty"`
}

type promptAuxAction struct {
	Symbol       string  `json:"symbol"`
	Action       string  `json:"action"`
	StrategyHint string  `json:"strategy_hint,omitempty"`
	Confidence   int     `json:"confidence,omitempty"`
	RiskUSD      float64 `json:"risk_usd,omitempty"`
	Reasoning    string  `json:"reasoning,omitempty"`
}

type promptAuxConsensus struct {
	TotalModels int                            `json:"models_total"`
	ModelsOpen  int                            `json:"models_open"`
	ModelsWait  int                            `json:"models_wait"`
	Majority    string                         `json:"majority"`
	OpenModels  []string                       `json:"open_models,omitempty"`
	WaitModels  []string                       `json:"wait_models,omitempty"`
	SymbolVotes map[string]promptSymbolSupport `json:"symbol_votes,omitempty"`
}

type promptSymbolSupport struct {
	Long  []string `json:"long,omitempty"`
	Short []string `json:"short,omitempty"`
}

type promptEnsembleMeta struct {
	Mode        string `json:"mode"`
	SummaryMode string `json:"summary_mode,omitempty"`
}

type promptSnapshot struct {
	Runtime           promptRuntime            `json:"runtime"`
	Account           promptAccount            `json:"account"`
	Risk              promptRisk               `json:"risk_guardrails"`
	Performance       *promptPerformance       `json:"performance,omitempty"`
	OpenPositions     []promptPosition         `json:"open_positions"`
	Market            []promptMarket           `json:"market"`
	CandidatePriority []string                 `json:"candidate_priority,omitempty"`
	RecentRiskAlerts  []RiskFlag               `json:"recent_risk_alerts,omitempty"`
	RecentGuardrails  []MarketGuardrailWarning `json:"recent_guardrails,omitempty"`
	AuxOpinions       []promptAuxOpinion       `json:"auxiliary_opinions,omitempty"`
	AuxConsensus      *promptAuxConsensus      `json:"auxiliary_consensus,omitempty"`
	EnsembleMeta      *promptEnsembleMeta      `json:"ensemble_meta,omitempty"`
}

func buildPromptSnapshot(ctx *Context) promptSnapshot {
	availablePct := 0.0
	if ctx.Account.TotalEquity > 0 {
		availablePct = (ctx.Account.AvailableBalance / ctx.Account.TotalEquity) * 100
	}

	riskBudget := ctx.Account.TotalEquity * 0.03
	marginHeadroom := math.Max(0, MaxMarginUsagePct-ctx.Account.MarginUsedPct)

	runtime := promptRuntime{
		CurrentTime:    ctx.CurrentTime,
		RuntimeMinutes: ctx.RuntimeMinutes,
		CallCount:      ctx.CallCount,
	}

	account := promptAccount{
		TotalEquity:      ctx.Account.TotalEquity,
		AvailableBalance: ctx.Account.AvailableBalance,
		AvailablePct:     availablePct,
		TotalPnLPct:      ctx.Account.TotalPnLPct,
		MarginUsedPct:    ctx.Account.MarginUsedPct,
		PositionCount:    ctx.Account.PositionCount,
	}

	risk := promptRisk{
		MaxPositions:      3,
		OpenPositions:     len(ctx.Positions),
		SlotsRemaining:    int(math.Max(0, float64(3-len(ctx.Positions)))),
		MarginHeadroomPct: marginHeadroom,
		RiskBudgetUSD:     riskBudget,
		MaxPositionUSD: map[string]float64{
			"altcoin": ctx.Account.TotalEquity * 1.5,
			"btc_eth": ctx.Account.TotalEquity * 10,
		},
		MaxLeverage: map[string]int{
			"altcoin": ctx.AltcoinLeverage,
			"btc_eth": ctx.BTCETHLeverage,
		},
	}

	if ctx.SharpeCooling != "" {
		risk.SharpeCoolingLevel = ctx.SharpeCooling
	}
	if ctx.PerformanceState != "" {
		risk.PerformanceState = ctx.PerformanceState
	}

	performance := buildPerformanceSnapshot(ctx.Performance)
	if performance != nil && (risk.SharpeCoolingLevel == "" || risk.PerformanceState == "") {
		perfState := risk.PerformanceState
		cooling := risk.SharpeCoolingLevel

		if cooling == "" {
			if performance.RecentLossStreak >= 3 || performance.RecentPnL <= -riskBudget {
				cooling = "halt_3_cycles"
				if perfState == "" {
					perfState = "loss_streak"
				}
			} else if performance.RecentLossStreak >= 2 || performance.RecentPnL <= -riskBudget*0.5 {
				cooling = "only_high_confidence_trades"
				if perfState == "" {
					perfState = "drawdown"
				}
			}
		}

		if performance.TotalTrades >= 5 {
			if performance.SharpeRatio < -0.5 {
				if cooling == "" || cooling == "only_high_confidence_trades" {
					cooling = "halt_6_cycles"
				}
				if perfState == "" {
					perfState = "loss_streak"
				}
			} else if performance.SharpeRatio < 0 && cooling == "" {
				cooling = "only_high_confidence_trades"
				if perfState == "" {
					perfState = "drawdown"
				}
			}
		}

		if performance.RecentWinStreak >= 2 && performance.RecentPnL > 0 && cooling == "" {
			if perfState == "" {
				perfState = "positive"
			}
		}

		if perfState != "" && risk.PerformanceState == "" {
			risk.PerformanceState = perfState
		}
		if cooling != "" && risk.SharpeCoolingLevel == "" {
			risk.SharpeCoolingLevel = cooling
		}
	}

	positions := buildOpenPositionsSnapshot(ctx)
	if positions == nil {
		positions = []promptPosition{}
	}

	marketSnapshots, candidateOrder := buildMarketSnapshots(ctx, maxPromptMarkets)
	if marketSnapshots == nil {
		marketSnapshots = []promptMarket{}
	}

	var ensembleMeta *promptEnsembleMeta
	if ctx.EnsembleMode != "" || ctx.EnsembleSummary != "" {
		ensembleMeta = &promptEnsembleMeta{
			Mode:        ctx.EnsembleMode,
			SummaryMode: ctx.EnsembleSummary,
		}
	}

	return promptSnapshot{
		Runtime:           runtime,
		Account:           account,
		Risk:              risk,
		Performance:       performance,
		OpenPositions:     positions,
		Market:            marketSnapshots,
		CandidatePriority: candidateOrder,
		RecentRiskAlerts:  ctx.RecentRiskAlerts,
		RecentGuardrails:  ctx.RecentGuardrails,
		AuxOpinions:       buildAuxOpinionsSnapshot(ctx),
		AuxConsensus:      buildAuxConsensusSnapshot(ctx),
		EnsembleMeta:      ensembleMeta,
	}
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	snapshot := buildPromptSnapshot(ctx)
	payload, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		payload = []byte("{}")
	}

	var sb strings.Builder
	sb.WriteString("解析以下结构化快照，结合系统提示词中的约束，产出最优交易决策：\n\n")
	sb.WriteString("```json\n")
	sb.Write(payload)
	sb.WriteString("\n```\n\n")
	sb.WriteString("请先输出你的推理过程（思维链），随后给出严格符合指定字段的 JSON 决策数组。\n")

	return sb.String()
}

func buildPerformanceSnapshot(raw interface{}) *promptPerformance {
	if raw == nil {
		return nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}

	type rawRecentTrade struct {
		Symbol      string  `json:"symbol"`
		Side        string  `json:"side"`
		PnL         float64 `json:"pn_l"`
		PnLPct      float64 `json:"pn_l_pct"`
		Duration    string  `json:"duration"`
		WasStopLoss bool    `json:"was_stop_loss"`
	}

	type rawPerformance struct {
		SharpeRatio      float64          `json:"sharpe_ratio"`
		TotalTrades      int              `json:"total_trades"`
		WinRate          float64          `json:"win_rate"`
		ProfitFactor     float64          `json:"profit_factor"`
		AvgWin           float64          `json:"avg_win"`
		AvgLoss          float64          `json:"avg_loss"`
		RecentPnL        float64          `json:"recent_pn_l"`
		RecentWinStreak  int              `json:"recent_win_streak"`
		RecentLossStreak int              `json:"recent_loss_streak"`
		BestSymbol       string           `json:"best_symbol"`
		WorstSymbol      string           `json:"worst_symbol"`
		RecentTrades     []rawRecentTrade `json:"recent_trades"`
	}

	var perf rawPerformance
	if err := json.Unmarshal(data, &perf); err != nil {
		return nil
	}

	result := &promptPerformance{
		SharpeRatio:      perf.SharpeRatio,
		TotalTrades:      perf.TotalTrades,
		WinRate:          perf.WinRate,
		ProfitFactor:     perf.ProfitFactor,
		AvgWin:           perf.AvgWin,
		AvgLoss:          perf.AvgLoss,
		RecentPnL:        perf.RecentPnL,
		RecentWinStreak:  perf.RecentWinStreak,
		RecentLossStreak: perf.RecentLossStreak,
		BestSymbol:       perf.BestSymbol,
		WorstSymbol:      perf.WorstSymbol,
	}

	if len(perf.RecentTrades) > 0 {
		limit := maxRecentTradesInPrompt
		if len(perf.RecentTrades) < limit {
			limit = len(perf.RecentTrades)
		}
		result.RecentTrades = make([]promptRecentTrade, 0, limit)
		for i := 0; i < limit; i++ {
			trade := perf.RecentTrades[i]
			result.RecentTrades = append(result.RecentTrades, promptRecentTrade{
				Symbol:      trade.Symbol,
				Side:        trade.Side,
				PnL:         trade.PnL,
				PnLPct:      trade.PnLPct,
				Duration:    trade.Duration,
				StopLossHit: trade.WasStopLoss,
			})
		}
	}

	return result
}

func buildOpenPositionsSnapshot(ctx *Context) []promptPosition {
	if len(ctx.Positions) == 0 {
		return nil
	}

	positions := make([]promptPosition, 0, len(ctx.Positions))
	for _, pos := range ctx.Positions {
		positionValue := pos.Quantity * pos.MarkPrice
		positions = append(positions, promptPosition{
			Symbol:               pos.Symbol,
			Side:                 pos.Side,
			EntryPrice:           pos.EntryPrice,
			MarkPrice:            pos.MarkPrice,
			Leverage:             pos.Leverage,
			Quantity:             pos.Quantity,
			PositionValue:        positionValue,
			MarginUsed:           pos.MarginUsed,
			UnrealizedPnLPct:     pos.UnrealizedPnLPct,
			PeakUnrealizedPnLPct: pos.PeakUnrealizedPnLPct,
			PeakUnrealizedPnLUSD: pos.PeakUnrealizedPnLUSD,
			DrawdownFromPeakPct:  pos.DrawdownFromPeakPct,
			DrawdownFromPeakUSD:  pos.DrawdownFromPeakUSD,
			LiquidationPrice:     pos.LiquidationPrice,
			HoldMinutes:          deriveHoldMinutes(pos.UpdateTime),
		})
	}

	return positions
}

func deriveHoldMinutes(updateTime int64) int {
	if updateTime <= 0 {
		return 0
	}
	now := time.Now().UnixMilli()
	if now <= updateTime {
		return 0
	}
	return int((now - updateTime) / (1000 * 60))
}

func buildMarketSnapshots(ctx *Context, limit int) ([]promptMarket, []string) {
	candidateSources := make(map[string][]string, len(ctx.CandidateCoins))
	for _, coin := range ctx.CandidateCoins {
		candidateSources[coin.Symbol] = append([]string{}, coin.Sources...)
	}

	ordered := make([]string, 0, len(ctx.Positions)+len(ctx.CandidateCoins))
	seen := make(map[string]bool)

	for _, pos := range ctx.Positions {
		if !seen[pos.Symbol] {
			ordered = append(ordered, pos.Symbol)
			seen[pos.Symbol] = true
		}
	}

	for _, coin := range ctx.CandidateCoins {
		if !seen[coin.Symbol] {
			ordered = append(ordered, coin.Symbol)
			seen[coin.Symbol] = true
		}
	}

	if limit <= 0 {
		limit = len(ordered)
	}

	snapshots := make([]promptMarket, 0, minInt(limit, len(ordered)))
	for _, symbol := range ordered {
		if len(snapshots) >= limit {
			break
		}
		data, ok := ctx.MarketDataMap[symbol]
		if !ok || data == nil {
			continue
		}

		sources := append([]string{}, candidateSources[symbol]...)
		if hasOpenPosition(ctx.Positions, symbol) {
			sources = appendIfMissing(sources, "open_position")
		}

		snapshots = append(snapshots, buildMarketSnapshot(symbol, data, sources))
	}

	return snapshots, ordered
}

func buildMarketSnapshot(symbol string, data *market.Data, sources []string) promptMarket {
	snapshot := promptMarket{
		Symbol:    symbol,
		Sources:   sources,
		TrendBias: deriveTrendBias(data),
		Price:     data.CurrentPrice,
		ChangePct: map[string]float64{
			"15m": data.PriceChange15m,
			"1h":  data.PriceChange1h,
			"4h":  data.PriceChange4h,
		},
		MomentumBias: map[string]string{
			"15m": classifyMomentum(data.PriceChange15m),
			"1h":  classifyMomentum(data.PriceChange1h),
			"4h":  classifyMomentum(data.PriceChange4h),
		},
		FundingRate: data.FundingRate,
	}

	if data.OpenInterest != nil {
		ratio := 0.0
		if data.OpenInterest.Average > 0 {
			ratio = data.OpenInterest.Latest / data.OpenInterest.Average
		}
		snapshot.OpenInterest = &promptOpenInterest{
			Latest:  data.OpenInterest.Latest,
			Average: data.OpenInterest.Average,
			Ratio:   ratio,
		}
	}

	if data.LongerTermContext != nil {
		atrPct := 0.0
		if data.CurrentPrice > 0 {
			atrPct = (data.LongerTermContext.ATR14 / data.CurrentPrice) * 100
		}

		snapshot.Volatility = &promptVolatility{
			ATR3:     data.LongerTermContext.ATR3,
			ATR14:    data.LongerTermContext.ATR14,
			ATR14Pct: atrPct,
		}

		snapshot.H4 = &promptTimeframe{
			EMA20: data.LongerTermContext.EMA20,
			EMA50: data.LongerTermContext.EMA50,
			MACD:  lastFloat(data.LongerTermContext.MACDValues),
			RSI14: lastFloat(data.LongerTermContext.RSI14Values),
		}
		snapshot.H4.TrendBias = deriveTimeframeTrendBias(snapshot.H4)
	}

	if data.MidTermContext != nil {
		snapshot.M15 = &promptTimeframe{
			EMA20: data.MidTermContext.EMA20,
			EMA50: data.MidTermContext.EMA50,
			MACD:  data.MidTermContext.MACD,
			RSI14: data.MidTermContext.RSI14,
		}
		snapshot.M15.TrendBias = deriveTimeframeTrendBias(snapshot.M15)
	}

	if data.HourlyContext != nil {
		snapshot.H1 = &promptTimeframe{
			EMA20: data.HourlyContext.EMA20,
			EMA50: data.HourlyContext.EMA50,
			MACD:  data.HourlyContext.MACD,
			RSI14: data.HourlyContext.RSI14,
		}
		snapshot.H1.TrendBias = deriveTimeframeTrendBias(snapshot.H1)
	}

	if data.IntradaySeries != nil {
		snapshot.Intraday = &promptIntraday{
			MACD:       lastFloat(data.IntradaySeries.MACDValues),
			RSI7:       lastFloat(data.IntradaySeries.RSI7Values),
			PriceSlope: calcPriceSlope(data.IntradaySeries.MidPrices),
		}
	}

	if data.RangeState != nil {
		snapshot.RangeState = &promptRangeState{
			Timeframe:         data.RangeState.Timeframe,
			LookbackCandles:   data.RangeState.LookbackCandles,
			LookbackMinutes:   data.RangeState.LookbackMinutes,
			AgeBars1h:         data.RangeState.AgeBars1h,
			High:              data.RangeState.High,
			Low:               data.RangeState.Low,
			Mid:               data.RangeState.Mid,
			Width:             data.RangeState.Width,
			WidthPct:          data.RangeState.WidthPct,
			WidthToATR14:      data.RangeState.WidthToATR14,
			ATR14:             data.RangeState.ATR14,
			VWAP:              data.RangeState.VWAP,
			TouchesHigh:       data.RangeState.TouchesHigh,
			TouchesLow:        data.RangeState.TouchesLow,
			DistanceToHighPct: data.RangeState.DistanceToHighPct,
			DistanceToLowPct:  data.RangeState.DistanceToLowPct,
			PriceLocation:     data.RangeState.PriceLocation,
			Regime:            data.RangeState.Regime,
			ADX144h:           data.RangeState.ADX144h,
			BBP1h:             data.RangeState.BBP1h,
		}
	}

	snapshot.ConfidenceFlags = collectConfidenceFlags(data, snapshot)

	if guardrails := GuardrailWarningsForMarket(data); len(guardrails) > 0 {
		snapshot.GuardrailWarnings = guardrails
	}

	return snapshot
}

func buildAuxOpinionsSnapshot(ctx *Context) []promptAuxOpinion {
	if ctx == nil || len(ctx.AuxOpinions) == 0 {
		return nil
	}

	result := make([]promptAuxOpinion, 0, len(ctx.AuxOpinions))
	for _, aux := range ctx.AuxOpinions {
		if aux.ModelID == "" {
			continue
		}
		opinion := promptAuxOpinion{
			ModelID:   aux.ModelID,
			ModelName: aux.ModelName,
			Provider:  aux.Provider,
			Weight:    aux.Weight,
			Role:      aux.Role,
			Summary:   truncateString(aux.Summary, 480),
			Notes:     truncateString(aux.Notes, 200),
		}
		if len(aux.Decisions) > 0 {
			limit := len(aux.Decisions)
			if limit > 5 {
				limit = 5
			}
			opinion.Votes = make([]promptAuxAction, 0, limit)
			for i := 0; i < limit; i++ {
				d := aux.Decisions[i]
				vote := promptAuxAction{
					Symbol:       d.Symbol,
					Action:       d.Action,
					StrategyHint: d.StrategyHint,
					Confidence:   d.Confidence,
					RiskUSD:      d.RiskUSD,
					Reasoning:    truncateString(d.Reasoning, 200),
				}
				opinion.Votes = append(opinion.Votes, vote)
			}
		}
		result = append(result, opinion)
	}

	return result
}

func buildAuxConsensusSnapshot(ctx *Context) *promptAuxConsensus {
	if ctx == nil || ctx.AuxConsensus == nil {
		return nil
	}

	src := ctx.AuxConsensus
	snapshot := &promptAuxConsensus{
		TotalModels: src.TotalModels,
		ModelsOpen:  src.OpenCount,
		ModelsWait:  src.WaitCount,
		Majority:    src.Majority,
	}

	if len(src.OpenModels) > 0 {
		snapshot.OpenModels = append([]string(nil), src.OpenModels...)
	}
	if len(src.WaitModels) > 0 {
		snapshot.WaitModels = append([]string(nil), src.WaitModels...)
	}

	if len(src.SymbolVotes) > 0 {
		snapshot.SymbolVotes = make(map[string]promptSymbolSupport, len(src.SymbolVotes))
		for symbol, vote := range src.SymbolVotes {
			snapshot.SymbolVotes[symbol] = promptSymbolSupport{
				Long:  append([]string(nil), vote.LongModels...),
				Short: append([]string(nil), vote.ShortModels...),
			}
		}
	}

	return snapshot
}

func deriveTrendBias(data *market.Data) string {
	if data.LongerTermContext != nil {
		lastMACD := lastFloat(data.LongerTermContext.MACDValues)
		if data.LongerTermContext.EMA20 > data.LongerTermContext.EMA50 && lastMACD >= 0 {
			return "bullish"
		}
		if data.LongerTermContext.EMA20 < data.LongerTermContext.EMA50 && lastMACD <= 0 {
			return "bearish"
		}
	}

	if data.HourlyContext != nil {
		if data.HourlyContext.EMA20 > data.HourlyContext.EMA50 && data.HourlyContext.MACD >= 0 {
			return "bullish"
		}
		if data.HourlyContext.EMA20 < data.HourlyContext.EMA50 && data.HourlyContext.MACD <= 0 {
			return "bearish"
		}
	}

	if data.CurrentPrice > data.CurrentEMA20 {
		return "bullish"
	}
	if data.CurrentPrice < data.CurrentEMA20 {
		return "bearish"
	}

	return "range"
}

func deriveTimeframeTrendBias(tf *promptTimeframe) string {
	if tf == nil {
		return ""
	}
	if tf.EMA20 > tf.EMA50 && tf.MACD >= 0 {
		return "bullish"
	}
	if tf.EMA20 < tf.EMA50 && tf.MACD <= 0 {
		return "bearish"
	}
	return "range"
}

func classifyMomentum(change float64) string {
	switch {
	case change >= 1.5:
		return "strong_up"
	case change >= 0.2:
		return "up"
	case change <= -1.5:
		return "strong_down"
	case change <= -0.2:
		return "down"
	default:
		return "flat"
	}
}

func calcPriceSlope(series []float64) float64 {
	if len(series) < 2 {
		return 0
	}
	latest := series[len(series)-1]
	prev := series[len(series)-2]
	if prev == 0 {
		return 0
	}
	return ((latest - prev) / prev) * 100
}

func lastFloat(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	return values[len(values)-1]
}

func collectConfidenceFlags(data *market.Data, snapshot promptMarket) []string {
	flags := make([]string, 0, 12)

	if snapshot.TrendBias == "bullish" && snapshot.H1 != nil && snapshot.H1.TrendBias == "bullish" {
		flags = append(flags, "multi_tf_bullish")
	}
	if snapshot.TrendBias == "bearish" && snapshot.H1 != nil && snapshot.H1.TrendBias == "bearish" {
		flags = append(flags, "multi_tf_bearish")
	}

	if data.CurrentMACD > 0 {
		flags = append(flags, "macd_positive_3m")
	} else if data.CurrentMACD < 0 {
		flags = append(flags, "macd_negative_3m")
	}

	if data.CurrentRSI7 >= 70 {
		flags = append(flags, "rsi7_overbought")
	} else if data.CurrentRSI7 <= 30 {
		flags = append(flags, "rsi7_oversold")
	}

	if snapshot.OpenInterest != nil {
		switch {
		case snapshot.OpenInterest.Ratio >= 1.1:
			flags = append(flags, "oi_expanding")
		case snapshot.OpenInterest.Ratio > 0 && snapshot.OpenInterest.Ratio <= 0.9:
			flags = append(flags, "oi_contracting")
		}
	}

	if snapshot.RangeState != nil {
		switch snapshot.RangeState.Regime {
		case "range":
			flags = append(flags, "range_tradable")
		case "squeeze":
			flags = append(flags, "range_squeeze")
		case "breakout_up":
			flags = append(flags, "range_breakout_up")
		case "breakout_down":
			flags = append(flags, "range_breakout_down")
		case "trend_attempt":
			flags = append(flags, "range_trend_attempt")
		}

		if snapshot.RangeState.PriceLocation == "mid" && snapshot.RangeState.WidthToATR14 >= 1.5 {
			flags = append(flags, "range_wide_enough")
		}
		if snapshot.RangeState.PriceLocation == "mid" && math.Abs(snapshot.RangeState.DistanceToHighPct-snapshot.RangeState.DistanceToLowPct) <= 0.3 {
			flags = append(flags, "range_centered")
		}

		switch snapshot.RangeState.PriceLocation {
		case "near_low":
			flags = append(flags, "range_near_low")
		case "near_high":
			flags = append(flags, "range_near_high")
		case "outside_low":
			flags = append(flags, "range_breakdown_risk")
		case "outside_high":
			flags = append(flags, "range_breakout_risk")
		}

		if snapshot.RangeState.TouchesLow >= 2 {
			flags = append(flags, "range_low_confirmed")
		}
		if snapshot.RangeState.TouchesHigh >= 2 {
			flags = append(flags, "range_high_confirmed")
		}

		if snapshot.RangeState.ADX144h > 0 && snapshot.RangeState.ADX144h < 18 {
			flags = append(flags, "range_adx_low")
		}
	}

	return flags
}

func appendGuardrailHistory(history []MarketGuardrailWarning, additions ...MarketGuardrailWarning) []MarketGuardrailWarning {
	if len(additions) == 0 {
		return history
	}
	for _, item := range additions {
		normalized := item
		if normalized.Severity == "" {
			normalized.Severity = "low"
		}
		history = append(history, normalized)
		if len(history) > MaxRecentGuardrailSnapshots {
			history = history[len(history)-MaxRecentGuardrailSnapshots:]
		}
	}
	return history
}

func hasHighSeverityGuardrail(warnings []MarketGuardrailWarning) bool {
	for _, warn := range warnings {
		if strings.EqualFold(warn.Severity, "high") {
			return true
		}
	}
	return false
}

func summarizeGuardrails(warnings []MarketGuardrailWarning) string {
	if len(warnings) == 0 {
		return ""
	}
	codes := make([]string, 0, len(warnings))
	for _, warn := range warnings {
		entry := warn.Code
		if warn.Detail != "" {
			entry = fmt.Sprintf("%s(%s)", warn.Code, warn.Detail)
		}
		codes = append(codes, entry)
	}
	return strings.Join(codes, "; ")
}

// GuardrailWarningsForMarket 生成市场硬性约束提示
func GuardrailWarningsForMarket(data *market.Data) []MarketGuardrailWarning {
	warnings := make([]MarketGuardrailWarning, 0, 4)
	if data == nil {
		return warnings
	}

	if data.CurrentPrice > 0 && data.CurrentPrice < 0.05 {
		warnings = append(warnings, MarketGuardrailWarning{
			Code:     "low_unit_price",
			Severity: "medium",
			Detail:   fmt.Sprintf("symbol price %.5f < 0.05, 价格过低易受滑点影响", data.CurrentPrice),
		})
	}

	if data.LongerTermContext != nil && data.CurrentPrice > 0 {
		atrPct := (data.LongerTermContext.ATR14 / data.CurrentPrice) * 100
		if atrPct < 0.5 {
			warnings = append(warnings, MarketGuardrailWarning{
				Code:     "low_volatility_atr14",
				Severity: "low",
				Detail:   fmt.Sprintf("ATR14 %.4f -> %.2f%%，波动过低", data.LongerTermContext.ATR14, atrPct),
			})
		}
		if atrPct > 12 {
			warnings = append(warnings, MarketGuardrailWarning{
				Code:     "extreme_volatility_atr14",
				Severity: "high",
				Detail:   fmt.Sprintf("ATR14 %.4f -> %.2f%%，波动过大", data.LongerTermContext.ATR14, atrPct),
			})
		}
	}

	if math.Abs(data.FundingRate) >= 0.00075 {
		warnings = append(warnings, MarketGuardrailWarning{
			Code:     "funding_extreme",
			Severity: "high",
			Detail:   fmt.Sprintf("funding_rate %.5f，资金费率异常", data.FundingRate),
		})
	}

	if data.OpenInterest != nil && data.OpenInterest.Average > 0 {
		oiRatio := data.OpenInterest.Latest / data.OpenInterest.Average
		if oiRatio <= 0.85 {
			warnings = append(warnings, MarketGuardrailWarning{
				Code:     "open_interest_contracting",
				Severity: "medium",
				Detail:   fmt.Sprintf("oi_ratio %.2f，持仓量显著下降", oiRatio),
			})
		}
	}

	if math.Abs(data.PriceChange4h) >= 8 {
		warnings = append(warnings, MarketGuardrailWarning{
			Code:     "price_whipsaw_4h",
			Severity: "medium",
			Detail:   fmt.Sprintf("4h change %+.2f%%，价格剧烈波动", data.PriceChange4h),
		})
	}

	return warnings
}

func hasOpenPosition(positions []PositionInfo, symbol string) bool {
	for _, pos := range positions {
		if pos.Symbol == symbol {
			return true
		}
	}
	return false
}

func truncateString(input string, max int) string {
	if max <= 0 {
		return ""
	}
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= max {
		return trimmed
	}
	if max <= 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}

func appendIfMissing(items []string, candidate string) []string {
	for _, item := range items {
		if item == candidate {
			return items
		}
	}
	return append(items, candidate)
}

func isDecisionOpenAction(action string) bool {
	return action == "open_long" || action == "open_short"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// parseFullDecisionResponse 解析AI的完整决策响应
func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int, marketData map[string]*market.Data) (*FullDecision, error) {
	// 1. 提取思维链
	cotTrace := extractCoTTrace(aiResponse)

	// 2. 提取JSON决策列表
	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("提取决策失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	// 3. 根据市场数据自动补全必要的字段（例如 range 策略默认的分批止盈）
	autoFillRangeTakeProfitTargets(decisions, marketData)

	// 4. 验证决策
	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("决策验证失败: %w\n\n=== AI思维链分析 ===\n%s", err, cotTrace)
	}

	return &FullDecision{
		CoTTrace:  cotTrace,
		Decisions: decisions,
	}, nil
}

// extractCoTTrace 提取思维链分析
func extractCoTTrace(response string) string {
	// 查找JSON数组的开始位置
	jsonStart := strings.Index(response, "[")

	if jsonStart > 0 {
		// 思维链是JSON数组之前的内容
		return strings.TrimSpace(response[:jsonStart])
	}

	// 如果找不到JSON，整个响应都是思维链
	return strings.TrimSpace(response)
}

// extractDecisions 提取JSON决策列表
func extractDecisions(response string) ([]Decision, error) {
	// 直接查找JSON数组 - 找第一个完整的JSON数组
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return nil, fmt.Errorf("AI响应为空")
	}

	if strings.HasPrefix(trimmed, "{") {
		return extractDecisionsFromObject(trimmed)
	}

	// 优先解析 ```json fenced code block（通常是最终决策）
	if fenced := extractJSONCodeBlock(trimmed); fenced != "" {
		if decisions, err := decodeDecisionArray(fenced); err == nil {
			return decisions, nil
		}
	}

	arrayStart := strings.Index(trimmed, "[")
	if arrayStart == -1 {
		return nil, fmt.Errorf("无法找到JSON数组起始")
	}

	// 从 [ 开始，匹配括号找到对应的 ]
	arrayEnd := findMatchingBracket(trimmed, arrayStart)
	if arrayEnd == -1 {
		return nil, fmt.Errorf("无法找到JSON数组结束")
	}

	jsonContent := strings.TrimSpace(trimmed[arrayStart : arrayEnd+1])
	return decodeDecisionArray(jsonContent)
}

func decodeDecisionArray(jsonContent string) ([]Decision, error) {
	// 🔧 修复常见的JSON格式错误：缺少引号的字段值
	jsonContent = fixMissingQuotes(jsonContent)

	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
	}

	return decisions, nil
}

func extractJSONCodeBlock(s string) string {
	const fence = "```"
	searchStart := 0
	var candidate string

	for searchStart < len(s) {
		start := strings.Index(s[searchStart:], fence)
		if start == -1 {
			break
		}
		start += searchStart

		langStart := start + len(fence)
		langEnd := strings.IndexByte(s[langStart:], '\n')
		if langEnd == -1 {
			break
		}
		langEnd += langStart

		langLine := strings.TrimSpace(s[langStart:langEnd])
		blockStart := langEnd + 1
		end := strings.Index(s[blockStart:], fence)
		if end == -1 {
			break
		}
		end += blockStart

		block := strings.TrimSpace(s[blockStart:end])
		lang := strings.ToLower(langLine)

		if lang == "" || strings.Contains(lang, "json") || strings.Contains(lang, "decision") {
			if strings.HasPrefix(block, "[") || strings.HasPrefix(block, "{") {
				candidate = block
			}
		}

		searchStart = end + len(fence)
	}

	return candidate
}

func extractDecisionsFromObject(s string) ([]Decision, error) {
	type wrapper struct {
		Decisions json.RawMessage `json:"decisions"`
		Result    json.RawMessage `json:"result"`
		Response  json.RawMessage `json:"response"`
		Data      json.RawMessage `json:"data"`
	}

	var w wrapper
	if err := json.Unmarshal([]byte(s), &w); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w", err)
	}

	raw := w.Decisions
	if len(raw) == 0 {
		raw = firstNonEmptyRaw(w.Result, w.Response, w.Data)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("对象中未找到 decisions 数组")
	}

	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("decisions 数组为空")
	}

	if !strings.HasPrefix(trimmed, "[") {
		return nil, fmt.Errorf("decisions 字段不是数组")
	}

	var decisions []Decision
	if err := json.Unmarshal(raw, &decisions); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, trimmed)
	}

	return decisions, nil
}

func firstNonEmptyRaw(raws ...json.RawMessage) json.RawMessage {
	for _, raw := range raws {
		if len(bytes.TrimSpace(raw)) > 0 {
			return raw
		}
	}
	return nil
}

// fixMissingQuotes 替换中文引号为英文引号（避免输入法自动转换）
func fixMissingQuotes(jsonStr string) string {
	jsonStr = strings.ReplaceAll(jsonStr, "\u201c", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u201d", "\"") // "
	jsonStr = strings.ReplaceAll(jsonStr, "\u2018", "'")  // '
	jsonStr = strings.ReplaceAll(jsonStr, "\u2019", "'")  // '
	return jsonStr
}

// validateDecisions 验证所有决策（需要账户信息和杠杆配置）
func validateDecisions(decisions []Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	for i := range decisions {
		if err := validateDecision(&decisions[i], accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
			return fmt.Errorf("决策 #%d 验证失败: %w", i+1, err)
		}
	}
	return nil
}

// findMatchingBracket 查找匹配的右括号
func findMatchingBracket(s string, start int) int {
	if start >= len(s) || s[start] != '[' {
		return -1
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

const (
	autoRangeMidFraction   = 0.55
	autoRangeFinalFallback = 0.35
)

func autoFillRangeTakeProfitTargets(decisions []Decision, marketData map[string]*market.Data) {
	for i := range decisions {
		d := &decisions[i]
		if d == nil || len(d.TakeProfitTargets) > 0 || !isDecisionOpenAction(d.Action) {
			continue
		}
		if d.TakeProfit <= 0 {
			continue
		}

		strategyHint, ok := normalizeStrategyHint(d.StrategyHint)
		if !ok {
			continue
		}
		if strategyHint != "range" && strategyHint != "range_developing" {
			continue
		}

		targets := buildAutoRangeTargets(d, marketData)
		if len(targets) > 0 {
			d.TakeProfitTargets = targets
		}
	}
}

func buildAutoRangeTargets(decision *Decision, marketData map[string]*market.Data) []TakeProfitTarget {
	if decision == nil || decision.TakeProfit <= 0 {
		return nil
	}

	mid := estimateRangeMidPrice(decision.Symbol, marketData)
	var targets []TakeProfitTarget

	if mid > 0 {
		switch decision.Action {
		case "open_long":
			if mid < decision.TakeProfit {
				targets = append(targets, TakeProfitTarget{
					Price:   mid,
					SizePct: autoRangeMidFraction,
					Kind:    "mid_auto",
				})
			}
		case "open_short":
			if mid > decision.TakeProfit {
				targets = append(targets, TakeProfitTarget{
					Price:   mid,
					SizePct: autoRangeMidFraction,
					Kind:    "mid_auto",
				})
			}
		}
	}

	finalFraction := 1.0
	for _, t := range targets {
		finalFraction -= t.SizePct
	}
	if len(targets) > 0 {
		finalFraction = math.Max(autoRangeFinalFallback, finalFraction)
	}
	if finalFraction <= 0 || finalFraction > 1.0 {
		finalFraction = 1.0
	}

	targets = append(targets, TakeProfitTarget{
		Price:   decision.TakeProfit,
		SizePct: finalFraction,
		Kind:    "final_auto",
	})

	total := 0.0
	for _, t := range targets {
		total += t.SizePct
	}
	if total > 1.0 {
		scale := 1.0 / total
		for i := range targets {
			targets[i].SizePct *= scale
		}
	}

	return targets
}

func estimateRangeMidPrice(symbol string, marketData map[string]*market.Data) float64 {
	data := lookupMarketData(marketData, symbol)
	if data == nil || data.RangeState == nil {
		return 0
	}

	rs := data.RangeState
	mid := rs.Mid
	if mid <= 0 {
		if rs.VWAP > 0 {
			mid = rs.VWAP
		} else if rs.High > 0 && rs.Low > 0 {
			mid = (rs.High + rs.Low) / 2
		}
	}
	return mid
}

func lookupMarketData(marketData map[string]*market.Data, symbol string) *market.Data {
	if marketData == nil {
		return nil
	}
	sym := strings.TrimSpace(symbol)
	if sym == "" {
		return nil
	}

	candidates := []string{sym, strings.ToUpper(sym), strings.ToLower(sym)}
	seen := make(map[string]bool, len(candidates))
	for _, key := range candidates {
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		if data := marketData[key]; data != nil {
			return data
		}
	}
	return nil
}

func normalizeStrategyHint(input string) (string, bool) {
	cleaned := strings.ToLower(strings.TrimSpace(removeInvisibleRunes(input)))
	cleaned = strings.ReplaceAll(cleaned, "-", "_")
	cleaned = strings.ReplaceAll(cleaned, " ", "_")
	cleaned = strings.ReplaceAll(cleaned, "　", "_") // 全角空格
	cleaned = collapseUnderscores(cleaned)

	switch cleaned {
	case "", "auto":
		return "", true
	case "trend", "trending":
		return "trend", true
	case "range", "ranging", "range_trade":
		return "range", true
	case "range_developing", "range_develop", "range_dev", "developing_range":
		return "range_developing", true
	case "transitional", "neutral", "balancing":
		return "transitional", true
	case "mean_reversion", "meanreversion":
		return "range", true
	case "momentum":
		return "trend", true
	}

	return cleaned, false
}

func removeInvisibleRunes(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u200b', '\u200c', '\u200d', '\ufeff':
			return -1
		}
		return r
	}, s)
}

func collapseUnderscores(s string) string {
	if s == "" {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))

	prevUnderscore := false
	for _, r := range s {
		if r == '_' {
			if prevUnderscore {
				continue
			}
			prevUnderscore = true
		} else {
			prevUnderscore = false
		}
		b.WriteRune(r)
	}

	return strings.Trim(b.String(), "_")
}

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
	strategyHint, ok := normalizeStrategyHint(d.StrategyHint)
	if !ok {
		return fmt.Errorf("strategy_hint 不支持的取值: %s", d.StrategyHint)
	}

	// 验证action
	validActions := map[string]bool{
		"open_long":   true,
		"open_short":  true,
		"close_long":  true,
		"close_short": true,
		"hold":        true,
		"wait":        true,
	}

	if !validActions[d.Action] {
		return fmt.Errorf("无效的action: %s", d.Action)
	}

	// 开仓操作必须提供完整参数
	if d.Action == "open_long" || d.Action == "open_short" {
		// 根据币种使用配置的杠杆上限
		maxLeverage := altcoinLeverage          // 山寨币使用配置的杠杆
		maxPositionValue := accountEquity * 1.5 // 山寨币最多1.5倍账户净值
		if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
			maxLeverage = btcEthLeverage          // BTC和ETH使用配置的杠杆
			maxPositionValue = accountEquity * 10 // BTC/ETH最多10倍账户净值
		}
		isRangeDeveloping := strategyHint == "range_developing"
		riskBudget := accountEquity * 0.03

		if d.Leverage <= 0 {
			return fmt.Errorf("杠杆必须在1-%d之间（%s，当前配置上限%d倍）: %d", maxLeverage, d.Symbol, maxLeverage, d.Leverage)
		}
		if d.Leverage > maxLeverage {
			log.Printf("⚠️  杠杆超限: %s 请求 %dx，允许上限 %dx，自动收敛", d.Symbol, d.Leverage, maxLeverage)
			d.Leverage = maxLeverage
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}
		if d.StopLoss <= 0 {
			return fmt.Errorf("止损必须大于0")
		}

		hasTargets := len(d.TakeProfitTargets) > 0
		if !hasTargets && d.TakeProfit <= 0 {
			return fmt.Errorf("必须提供有效的止盈价格 (take_profit 或 tp_targets)")
		}

		if hasTargets && d.PositionSizeUSD <= 0 {
			return fmt.Errorf("提供 tp_targets 时 position_size_usd 必须大于0")
		}

		if hasTargets {
			remainingFraction := 1.0
			totalFraction := 0.0
			for i := range d.TakeProfitTargets {
				target := &d.TakeProfitTargets[i]
				if target.Price <= 0 {
					return fmt.Errorf("tp_targets[%d].price 必须大于0", i)
				}

				fraction := target.SizePct
				if fraction < 0 {
					return fmt.Errorf("tp_targets[%d].size_pct 不能为负数", i)
				}
				if fraction == 0 && target.SizeUSD > 0 && d.PositionSizeUSD > 0 {
					fraction = target.SizeUSD / d.PositionSizeUSD
				}
				if fraction > remainingFraction {
					fraction = remainingFraction
				}
				if fraction < 0 {
					fraction = 0
				}

				target.SizePct = fraction
				totalFraction += fraction
				remainingFraction -= fraction
				if remainingFraction < 0 {
					remainingFraction = 0
				}
			}

			if totalFraction > 1.02 {
				return fmt.Errorf("tp_targets 分配比例合计%.1f%%，不得超过100%%", totalFraction*100)
			}
		}

		takeProfitForRisk := d.TakeProfit
		if hasTargets {
			if d.Action == "open_long" {
				for _, target := range d.TakeProfitTargets {
					if target.Price > takeProfitForRisk {
						takeProfitForRisk = target.Price
					}
				}
			} else {
				if takeProfitForRisk <= 0 && len(d.TakeProfitTargets) > 0 {
					takeProfitForRisk = d.TakeProfitTargets[0].Price
				}
				for _, target := range d.TakeProfitTargets {
					if target.Price < takeProfitForRisk {
						takeProfitForRisk = target.Price
					}
				}
			}
		}

		if takeProfitForRisk <= 0 {
			return fmt.Errorf("缺少有效的止盈价格")
		}

		// 验证止损止盈的合理性（使用最终止盈价）
		if d.Action == "open_long" {
			if d.StopLoss >= takeProfitForRisk {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= takeProfitForRisk {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}
		d.TakeProfit = takeProfitForRisk

		// range 策略必须提供分批止盈
		if (strategyHint == "range" || strategyHint == "range_developing") && len(d.TakeProfitTargets) == 0 {
			return fmt.Errorf("range 策略必须提供 tp_targets 用于分批止盈")
		}

		// 验证风险回报比（根据策略类型设置最低阈值）
		// 计算入场价（假设当前市价）
		var entryPrice float64
		if d.Action == "open_long" {
			// 做多：入场价在止损和止盈之间
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // 假设在20%位置入场
		} else {
			// 做空：入场价在止损和止盈之间
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // 假设在20%位置入场
		}

		// 仓位上限和风险预算收敛
		tolerance := maxPositionValue * 0.01 // 1%容差
		if isRangeDeveloping {
			reducedMaxPosition := maxPositionValue * 0.75
			reducedTolerance := math.Max(tolerance, reducedMaxPosition*0.01)
			if d.PositionSizeUSD > reducedMaxPosition+reducedTolerance {
				log.Printf("⚠️  range_developing 仓位超限: %s 请求 %.2f USDT，允许上限 %.2f USDT，自动收敛", d.Symbol, d.PositionSizeUSD, reducedMaxPosition)
				d.PositionSizeUSD = reducedMaxPosition
			}
			if d.RiskUSD <= 0 {
				return fmt.Errorf("range_developing 策略必须提供 risk_usd，并缩减至风险预算60%%以内")
			}
			maxRiskAllowed := riskBudget * 0.6
			if d.RiskUSD > maxRiskAllowed+1e-6 {
				log.Printf("⚠️  range_developing risk_usd超限: %s 请求 %.2f USD，上限 %.2f USD，自动收敛", d.Symbol, d.RiskUSD, maxRiskAllowed)
				d.RiskUSD = maxRiskAllowed
			}
		}

		if d.PositionSizeUSD > maxPositionValue+tolerance {
			log.Printf("⚠️  决策仓位超限: %s 请求 %.2f USDT，允许上限 %.2f USDT，自动收敛", d.Symbol, d.PositionSizeUSD, maxPositionValue)
			d.PositionSizeUSD = maxPositionValue
		}

		if d.RiskUSD > 0 {
			maxRisk := riskBudget * 1.5
			if d.RiskUSD > maxRisk {
				if d.RiskUSD > riskBudget*3 {
					return fmt.Errorf("risk_usd %.2f 远超预算 %.2f，拒绝执行", d.RiskUSD, riskBudget)
				}
				log.Printf("⚠️  决策risk_usd超限: %s 请求 %.2f USD，预算 %.2f USD，放大上限 %.2f USD，自动收敛", d.Symbol, d.RiskUSD, riskBudget, maxRisk)
				d.RiskUSD = maxRisk
			} else if d.RiskUSD > riskBudget {
				log.Printf("⚠️  决策risk_usd高于预算: %s 请求 %.2f USD，预算 %.2f USD，自动收敛到预算", d.Symbol, d.RiskUSD, riskBudget)
				d.RiskUSD = riskBudget
			}

			if d.StopLoss > 0 && d.PositionSizeUSD > 0 {
				riskDistance := math.Abs(d.StopLoss - entryPrice)
				if riskDistance > 0 {
					maxPosition := (d.RiskUSD * entryPrice) / riskDistance
					if maxPosition > 0 && d.PositionSizeUSD > maxPosition {
						log.Printf("  🔧 risk守护: %s 调整仓位 %.2f -> %.2f 以维持风险 %.2f USD", d.Symbol, d.PositionSizeUSD, maxPosition, d.RiskUSD)
						d.PositionSizeUSD = maxPosition
					}
				}
			}
		}

		var riskPercent, rewardPercent, riskRewardRatio float64
		if d.Action == "open_long" {
			riskPercent = (entryPrice - d.StopLoss) / entryPrice * 100
			rewardPercent = (d.TakeProfit - entryPrice) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		} else {
			riskPercent = (d.StopLoss - entryPrice) / entryPrice * 100
			rewardPercent = (entryPrice - d.TakeProfit) / entryPrice * 100
			if riskPercent > 0 {
				riskRewardRatio = rewardPercent / riskPercent
			}
		}

		minRiskReward := 1.8
		switch strategyHint {
		case "trend":
			minRiskReward = 2.5
		case "transitional":
			minRiskReward = 2.0
		}

		if riskRewardRatio < minRiskReward {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥%.1f:1 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, minRiskReward, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	return nil
}

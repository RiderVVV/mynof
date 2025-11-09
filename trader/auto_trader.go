package trader

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"nofx/config"
	"nofx/decision"
	"nofx/logger"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	minOrderNotionalUSD      = 5.1   // 略高于交易所 5 USDT 的硬性下限，避免因边界舍入被拒单
	riskBudgetFraction       = 0.03  // 默认风险预算（3%净值）
	coolingRiskFraction      = 0.015 // 冷却阶段风险预算减半
	minStopDistancePct       = 0.8   // 止损至少距离现价0.8%
	maxSnapshotDriftPct      = 0.2   // 决策生成到执行的最大允许价格偏移(%)
	defaultMinRewardToRisk   = 3.0   // 默认最小盈亏比
	coolingMinRewardToRisk   = 4.0   // 冷却阶段最小盈亏比
	coolingConfidenceMinimum = 80    // 冷却阶段最小信心
)

// AutoTraderConfig 自动交易配置（简化版 - AI全权决策）
type AutoTraderConfig struct {
	// Trader标识
	ID               string // Trader唯一标识（用于日志目录等）
	Name             string // Trader显示名称
	AIModel          string // AI模型: "qwen" 或 "deepseek"
	SystemPromptPath string // 自定义系统提示词路径（可选，默认prompts/system_prompt.txt）

	// 交易平台选择
	Exchange string // "binance", "hyperliquid" 或 "aster"

	// 币安API配置
	BinanceAPIKey    string
	BinanceSecretKey string

	// Hyperliquid配置
	HyperliquidPrivateKey string
	HyperliquidWalletAddr string
	HyperliquidTestnet    bool

	// Aster配置
	AsterUser       string // Aster主钱包地址
	AsterSigner     string // Aster API钱包地址
	AsterPrivateKey string // Aster API钱包私钥

	CoinPoolAPIURL string

	// AI配置
	UseQwen     bool
	DeepSeekKey string
	QwenKey     string

	// 自定义AI API配置
	CustomAPIURL         string
	CustomAPIKey         string
	CustomModelName      string
	CustomAPIHTTPReferer string
	CustomAPIXTitle      string

	EnsembleMode        string
	EnsembleSummaryMode string
	EnsembleModels      []EnsembleModelConfig

	FocusSymbols    []string
	MaxTradeRiskUSD float64
	TradingWindow   config.TradingWindowConfig
	MajorEvents     []config.MajorEventConfig

	// 扫描配置
	ScanInterval  time.Duration // 扫描间隔（建议3分钟）
	GuardInterval time.Duration // 守护巡检间隔（默认1分钟）

	// 账户配置
	InitialBalance float64 // 初始金额（用于计算盈亏，需手动设置）

	// 杠杆配置
	BTCETHLeverage  int // BTC和ETH的杠杆倍数
	AltcoinLeverage int // 山寨币的杠杆倍数

	// 风险控制（仅作为提示，AI可自主决定）
	MaxDailyLoss    float64       // 最大日亏损百分比（提示）
	MaxDrawdown     float64       // 最大回撤百分比（提示）
	StopTradingTime time.Duration // 触发风控后暂停时长

	SimpleTrailingGuardEnabled bool    // 是否启用简单回撤守护
	SimpleTrailingFeePct       float64 // 回本所需收益阈值（默认0.03%即万五*2）
	RiskReviewEnabled          bool    // 是否启用AI风控复核
}

// EnsembleModelConfig 定义辅助模型的API参数
type EnsembleModelConfig struct {
	ID                   string
	Label                string
	AIModel              string
	CustomAPIURL         string
	CustomAPIKey         string
	CustomModelName      string
	CustomAPIHTTPReferer string
	CustomAPIXTitle      string
	Weight               float64
	Role                 string
	Notes                string
}

type ensembleModel struct {
	id        string
	label     string
	role      string
	weight    float64
	provider  string
	modelName string
	client    *mcp.Client
	notes     string
}

const (
	defaultProfitProtectActivationPct  = 12.0 // 默认触发盈利保护的峰值（%）
	defaultProfitProtectLockFloorPct   = 5.0  // 默认回撤保护最低保留利润（%）
	defaultProfitProtectMinRetracePct  = 3.0  // 默认保护触发的最小回撤幅度（%）
	defaultProfitProtectRetentionRatio = 0.5  // 默认保护时至少保留的利润比例
	simpleTrailingActivationPct        = 0.8  // 简易守护至少需0.8%峰值收益
	simpleTrailingDrawdownRatio        = 0.35 // 回吐金额达到风险锚点（initial_balance×3%）的35%时强制锁盈
	simpleTrailingDefaultFeePct        = 0.03 // 默认万五双向 ≈0.03% 回本线
	minProfitProtectPct                = 0.003
	minProfitProtectUSD                = 1.0
	minRangeTpProfitPct                = 0.003
	minCloseHoldMinutes                = 10
	minClosePnLPct                     = 0.2
	rangeMidAtrBumpRatio               = 0.5
)

type positionTargetState struct {
	Price    float64
	Quantity float64
	SizePct  float64
	Kind     string
	Filled   bool
}

type positionManagementState struct {
	StopLoss        float64
	InitialQuantity float64
	Targets         []*positionTargetState
}

type riskCheckResult struct {
	riskUSD      float64
	rewardToRisk float64
	riskLimitUSD float64
}

type majorEventWindow struct {
	Name  string
	Start time.Time
	End   time.Time
}

// AutoTrader 自动交易器
type AutoTrader struct {
	id                    string // Trader唯一标识
	name                  string // Trader显示名称
	aiModel               string // AI模型名称
	exchange              string // 交易平台名称
	config                AutoTraderConfig
	trader                Trader // 使用Trader接口（支持多平台）
	mcpClient             *mcp.Client
	decisionLogger        *logger.DecisionLogger // 决策日志记录器
	initialBalance        float64
	dailyPnL              float64
	lastResetTime         time.Time
	stopUntil             time.Time
	isRunning             bool
	startTime             time.Time        // 系统启动时间
	callCount             int              // AI调用次数
	positionFirstSeenTime map[string]int64 // 持仓首次出现时间 (symbol_side -> timestamp毫秒)
	positionPnLHigh       map[string]float64
	positionPnLHighUSD    map[string]float64
	positionMinHoldUntil  map[string]time.Time
	positionGuardStrategy map[string]string
	ensembleMode          string
	ensembleSummaryMode   string
	ensembleModels        []ensembleModel
	positionTargets       map[string]*positionManagementState
	lastMarketData        map[string]*market.Data
	activeContext         *decision.Context
	maxTradeRiskUSD       float64
	focusSymbols          []string
	focusSymbolSet        map[string]struct{}
	tradingWindow         config.TradingWindowConfig
	majorEvents           []majorEventWindow
}

type profitProtectionThresholds struct {
	activationPct float64
	retracePct    float64
	retentionRate float64
	lockFloorPct  float64
	atrPercent    float64
}

// NewAutoTrader 创建自动交易器
func NewAutoTrader(config AutoTraderConfig) (*AutoTrader, error) {
	// 设置默认值
	if config.ID == "" {
		config.ID = "default_trader"
	}
	if config.Name == "" {
		config.Name = "Default Trader"
	}
	if config.AIModel == "" {
		if config.UseQwen {
			config.AIModel = "qwen"
		} else {
			config.AIModel = "deepseek"
		}
	}

	mcpClient := mcp.New()

	// 初始化AI
	if config.AIModel == "custom" {
		// 使用自定义API
		mcpClient.SetCustomAPI(config.CustomAPIURL, config.CustomAPIKey, config.CustomModelName)
		if config.CustomAPIHTTPReferer != "" || config.CustomAPIXTitle != "" {
			headers := make(map[string]string, 2)
			if config.CustomAPIHTTPReferer != "" {
				headers["HTTP-Referer"] = config.CustomAPIHTTPReferer
			}
			if config.CustomAPIXTitle != "" {
				headers["X-Title"] = config.CustomAPIXTitle
			}
			mcpClient.SetExtraHeaders(headers)
		}
		log.Printf("🤖 [%s] 使用自定义AI API: %s (模型: %s)", config.Name, config.CustomAPIURL, config.CustomModelName)
	} else if config.UseQwen || config.AIModel == "qwen" {
		// 使用Qwen
		mcpClient.SetQwenAPIKey(config.QwenKey, "")
		log.Printf("🤖 [%s] 使用阿里云Qwen AI", config.Name)
	} else {
		// 默认使用DeepSeek
		mcpClient.SetDeepSeekAPIKey(config.DeepSeekKey)
		log.Printf("🤖 [%s] 使用DeepSeek AI", config.Name)
	}

	// 初始化币种池API
	if config.CoinPoolAPIURL != "" {
		pool.SetCoinPoolAPI(config.CoinPoolAPIURL)
	}

	// 设置默认交易平台
	if config.Exchange == "" {
		config.Exchange = "binance"
	}

	// 根据配置创建对应的交易器
	var trader Trader
	var err error

	switch config.Exchange {
	case "binance":
		log.Printf("🏦 [%s] 使用币安合约交易", config.Name)
		trader = NewFuturesTrader(config.BinanceAPIKey, config.BinanceSecretKey)
	case "hyperliquid":
		log.Printf("🏦 [%s] 使用Hyperliquid交易", config.Name)
		trader, err = NewHyperliquidTrader(config.HyperliquidPrivateKey, config.HyperliquidWalletAddr, config.HyperliquidTestnet)
		if err != nil {
			return nil, fmt.Errorf("初始化Hyperliquid交易器失败: %w", err)
		}
	case "aster":
		log.Printf("🏦 [%s] 使用Aster交易", config.Name)
		trader, err = NewAsterTrader(config.AsterUser, config.AsterSigner, config.AsterPrivateKey)
		if err != nil {
			return nil, fmt.Errorf("初始化Aster交易器失败: %w", err)
		}
	default:
		return nil, fmt.Errorf("不支持的交易平台: %s", config.Exchange)
	}

	// 验证初始金额配置
	if config.InitialBalance <= 0 {
		return nil, fmt.Errorf("初始金额必须大于0，请在配置中设置InitialBalance")
	}

	if config.GuardInterval <= 0 {
		config.GuardInterval = time.Minute
	}

	ensembleMode := strings.TrimSpace(config.EnsembleMode)
	if ensembleMode == "" {
		ensembleMode = "majority"
	}
	ensembleSummaryMode := strings.TrimSpace(config.EnsembleSummaryMode)

	var ensembleModels []ensembleModel
	if len(config.EnsembleModels) > 0 {
		ensembleModels = make([]ensembleModel, 0, len(config.EnsembleModels))
		for _, modelCfg := range config.EnsembleModels {
			em, err := initEnsembleModel(config, modelCfg)
			if err != nil {
				return nil, fmt.Errorf("[%s] 初始化辅助模型失败 (%s): %w", config.Name, modelCfg.ID, err)
			}
			ensembleModels = append(ensembleModels, em)
			logLabel := em.label
			if logLabel == "" {
				logLabel = em.id
			}
			log.Printf("🧠 [%s] 注册辅助模型: %s (provider=%s, model=%s, weight=%.2f)", config.Name, logLabel, em.provider, em.modelName, em.weight)
		}
		log.Printf("🧠 [%s] 启用多模型模式: %s（辅助模型 %d 个）", config.Name, ensembleMode, len(ensembleModels))
	}

	// 初始化决策日志记录器（使用trader ID创建独立目录）
	logDir := fmt.Sprintf("decision_logs/%s", config.ID)
	decisionLogger := logger.NewDecisionLogger(logDir)

	maxRisk := config.MaxTradeRiskUSD
	if maxRisk <= 0 {
		maxRisk = config.InitialBalance * riskBudgetFraction
	}

	focusSet := make(map[string]struct{})
	focusList := make([]string, 0, len(config.FocusSymbols))
	for _, sym := range config.FocusSymbols {
		normalized := market.Normalize(sym)
		if normalized == "" {
			continue
		}
		if _, exists := focusSet[normalized]; exists {
			continue
		}
		focusSet[normalized] = struct{}{}
		focusList = append(focusList, normalized)
	}

	var majorEvents []majorEventWindow
	for _, evt := range config.MajorEvents {
		start, err1 := time.Parse(time.RFC3339, strings.TrimSpace(evt.StartUTC))
		end, err2 := time.Parse(time.RFC3339, strings.TrimSpace(evt.EndUTC))
		if err1 != nil || err2 != nil {
			log.Printf("⚠️  解析重大事件时间失败 (%s): %v %v", evt.Name, err1, err2)
			continue
		}
		if end.Before(start) {
			start, end = end, start
		}
		window := majorEventWindow{
			Name:  evt.Name,
			Start: start.Add(-4 * time.Hour),
			End:   end.Add(4 * time.Hour),
		}
		majorEvents = append(majorEvents, window)
	}

	result := &AutoTrader{
		id:                    config.ID,
		name:                  config.Name,
		aiModel:               config.AIModel,
		exchange:              config.Exchange,
		config:                config,
		trader:                trader,
		mcpClient:             mcpClient,
		decisionLogger:        decisionLogger,
		initialBalance:        config.InitialBalance,
		lastResetTime:         time.Now(),
		startTime:             time.Now(),
		callCount:             0,
		isRunning:             false,
		positionFirstSeenTime: make(map[string]int64),
		positionPnLHigh:       make(map[string]float64),
		positionPnLHighUSD:    make(map[string]float64),
		positionMinHoldUntil:  make(map[string]time.Time),
		positionGuardStrategy: make(map[string]string),
		ensembleMode:          ensembleMode,
		ensembleSummaryMode:   ensembleSummaryMode,
		ensembleModels:        ensembleModels,
		positionTargets:       make(map[string]*positionManagementState),
		lastMarketData:        make(map[string]*market.Data),
		maxTradeRiskUSD:       maxRisk,
		focusSymbols:          focusList,
		focusSymbolSet:        focusSet,
		tradingWindow:         config.TradingWindow,
		majorEvents:           majorEvents,
	}
	if result.config.SimpleTrailingFeePct <= 0 {
		result.config.SimpleTrailingFeePct = simpleTrailingDefaultFeePct
	}

	return result, nil
}

func initEnsembleModel(parent AutoTraderConfig, cfg EnsembleModelConfig) (ensembleModel, error) {
	if cfg.ID == "" {
		return ensembleModel{}, fmt.Errorf("ensemble model id 未配置")
	}

	aiModel := strings.TrimSpace(cfg.AIModel)
	if aiModel == "" {
		aiModel = "custom"
	}

	label := strings.TrimSpace(cfg.Label)
	if label == "" {
		label = cfg.ID
	}

	weight := cfg.Weight
	if weight <= 0 {
		weight = 1.0
	}

	client := mcp.New()
	provider := aiModel
	modelName := ""

	switch aiModel {
	case "custom":
		apiURL := strings.TrimSpace(cfg.CustomAPIURL)
		if apiURL == "" {
			apiURL = strings.TrimSpace(parent.CustomAPIURL)
		}
		apiKey := strings.TrimSpace(cfg.CustomAPIKey)
		if apiKey == "" {
			apiKey = strings.TrimSpace(parent.CustomAPIKey)
		}
		modelName = strings.TrimSpace(cfg.CustomModelName)
		if modelName == "" {
			modelName = strings.TrimSpace(parent.CustomModelName)
		}
		if apiURL == "" || apiKey == "" || modelName == "" {
			return ensembleModel{}, fmt.Errorf("custom 模型缺少 api_url/api_key/model_name 配置")
		}
		client.SetCustomAPI(apiURL, apiKey, modelName)

		referer := strings.TrimSpace(cfg.CustomAPIHTTPReferer)
		if referer == "" {
			referer = strings.TrimSpace(parent.CustomAPIHTTPReferer)
		}
		xTitle := strings.TrimSpace(cfg.CustomAPIXTitle)
		if xTitle == "" {
			xTitle = strings.TrimSpace(parent.CustomAPIXTitle)
		}
		if referer != "" || xTitle != "" {
			headers := make(map[string]string, 2)
			if referer != "" {
				headers["HTTP-Referer"] = referer
			}
			if xTitle != "" {
				headers["X-Title"] = xTitle
			}
			client.SetExtraHeaders(headers)
		}
	default:
		return ensembleModel{}, fmt.Errorf("暂不支持的辅助模型 ai_model '%s'", aiModel)
	}

	return ensembleModel{
		id:        cfg.ID,
		label:     label,
		role:      strings.TrimSpace(cfg.Role),
		weight:    weight,
		provider:  provider,
		modelName: modelName,
		client:    client,
		notes:     strings.TrimSpace(cfg.Notes),
	}, nil
}

// Run 运行自动交易主循环
func (at *AutoTrader) Run() error {
	at.isRunning = true
	log.Println("🚀 AI驱动自动交易系统启动")
	log.Printf("💰 初始余额: %.2f USDT", at.initialBalance)
	log.Printf("⚙️  扫描间隔: %v", at.config.ScanInterval)
	log.Printf("🛡 守护巡检: %v", at.config.GuardInterval)
	log.Println("🤖 AI将全权决定杠杆、仓位大小、止损止盈等参数")
	log.Println("🛡 盈利回撤保护启用：动态阈值 + 峰值回撤20% 简易守护双层锁盈。")

	decisionTicker := time.NewTicker(at.config.ScanInterval)
	defer decisionTicker.Stop()
	guardTicker := time.NewTicker(at.config.GuardInterval)
	defer guardTicker.Stop()

	// 先跑一次守护巡检，再执行完整AI周期
	if err := at.runGuardCycle(); err != nil {
		log.Printf("⚠️ 守护巡检失败: %v", err)
	}
	if err := at.runCycle(); err != nil {
		log.Printf("❌ 执行失败: %v", err)
	}

	for at.isRunning {
		select {
		case <-guardTicker.C:
			if err := at.runGuardCycle(); err != nil {
				log.Printf("⚠️ 守护巡检失败: %v", err)
			}
		case <-decisionTicker.C:
			if err := at.runCycle(); err != nil {
				log.Printf("❌ 执行失败: %v", err)
			}
		}
	}

	return nil
}

// Stop 停止自动交易
func (at *AutoTrader) Stop() {
	at.isRunning = false
	log.Println("⏹ 自动交易系统停止")
}

// runGuardCycle 只执行守护巡检（不调用AI）
func (at *AutoTrader) runGuardCycle() error {
	ctx, err := at.buildTradingContext()
	if err != nil {
		return fmt.Errorf("守护巡检构建交易上下文失败: %w", err)
	}

	record := &logger.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
		InputPrompt:  "guard_cycle",
	}
	at.populateRecordFromContext(record, ctx)

	if handled, err := at.applyProfitProtection(ctx, record); err != nil {
		return fmt.Errorf("守护巡检执行盈利保护失败: %w", err)
	} else if handled {
		if logErr := at.decisionLogger.LogDecision(record); logErr != nil {
			log.Printf("⚠ 保存守护巡检记录失败: %v", logErr)
		}
	}

	return nil
}

// runCycle 运行一个交易周期（使用AI全权决策）
func (at *AutoTrader) runCycle() error {
	at.callCount++

	log.Print("\n" + strings.Repeat("=", 70))
	log.Printf("⏰ %s - AI决策周期 #%d", time.Now().Format("2006-01-02 15:04:05"), at.callCount)
	log.Print(strings.Repeat("=", 70))

	// 创建决策记录
	record := &logger.DecisionRecord{
		ExecutionLog: []string{},
		Success:      true,
	}

	// 1. 检查是否需要停止交易
	if time.Now().Before(at.stopUntil) {
		remaining := at.stopUntil.Sub(time.Now())
		log.Printf("⏸ 风险控制：暂停交易中，剩余 %.0f 分钟", remaining.Minutes())
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("风险控制暂停中，剩余 %.0f 分钟", remaining.Minutes())
		at.decisionLogger.LogDecision(record)
		return nil
	}

	// 2. 重置日盈亏（每天重置）
	if time.Since(at.lastResetTime) > 24*time.Hour {
		at.dailyPnL = 0
		at.lastResetTime = time.Now()
		log.Println("📅 日盈亏已重置")
	}

	// 3. 收集交易上下文
	ctx, err := at.buildTradingContext()
	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("构建交易上下文失败: %v", err)
		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("构建交易上下文失败: %w", err)
	}

	at.populateRecordFromContext(record, ctx)

	log.Printf("📊 账户净值: %.2f USDT | 可用: %.2f USDT | 持仓: %d",
		ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)

	// 分批止盈管理：优先处理既有持仓的目标落袋
	if managed, err := at.applyTargetManagement(ctx, record); err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("执行分批止盈管理失败: %v", err)
		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("执行分批止盈管理失败: %w", err)
	} else if managed {
		// 分批止盈可能改变仓位，重新获取上下文
		ctx, err = at.buildTradingContext()
		if err != nil {
			record.Success = false
			record.ErrorMessage = fmt.Sprintf("分批止盈后刷新上下文失败: %v", err)
			at.decisionLogger.LogDecision(record)
			return fmt.Errorf("分批止盈后刷新上下文失败: %w", err)
		}
		at.populateRecordFromContext(record, ctx)
		log.Printf("📊 分批止盈后账户净值: %.2f USDT | 可用: %.2f USDT | 持仓: %d",
			ctx.Account.TotalEquity, ctx.Account.AvailableBalance, ctx.Account.PositionCount)
	}

	// 4. 盈利回撤保护：在请求AI前先执行强制止盈检查
	if handled, err := at.applyProfitProtection(ctx, record); err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("执行盈利保护失败: %v", err)
		if logErr := at.decisionLogger.LogDecision(record); logErr != nil {
			log.Printf("⚠ 保存决策记录失败: %v", logErr)
		}
		return fmt.Errorf("执行盈利保护失败: %w", err)
	} else if handled {
		if logErr := at.decisionLogger.LogDecision(record); logErr != nil {
			log.Printf("⚠ 保存决策记录失败: %v", logErr)
		}
		return nil
	}

	at.activeContext = ctx
	defer func() {
		at.activeContext = nil
	}()

	ctx.AuxConsensus = nil
	if len(at.ensembleModels) > 0 {
		auxOpinions := at.collectAuxOpinions(ctx, record)
		if len(auxOpinions) > 0 {
			ctx.AuxOpinions = auxOpinions
			ctx.AuxConsensus = decision.ComputeAuxConsensus(auxOpinions)
			ctx.EnsembleMode = at.ensembleMode
			ctx.EnsembleSummary = at.ensembleSummaryMode
		} else {
			ctx.AuxOpinions = nil
			ctx.AuxConsensus = nil
			ctx.EnsembleMode = ""
			ctx.EnsembleSummary = ""
		}
	}

	// 5. 调用AI获取完整决策
	log.Println("🤖 正在请求AI分析并决策...")
	fullDecision, err := decision.GetFullDecision(ctx, at.mcpClient)

	// 即使有错误，也保存思维链、决策和输入prompt（用于debug）
	if fullDecision != nil {
		record.InputPrompt = fullDecision.UserPrompt
		record.CoTTrace = fullDecision.CoTTrace
		if len(fullDecision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(fullDecision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		}
	}

	if err != nil {
		record.Success = false
		record.ErrorMessage = fmt.Sprintf("获取AI决策失败: %v", err)

		// 打印AI思维链（即使有错误）
		if fullDecision != nil && fullDecision.CoTTrace != "" {
			log.Print("\n" + strings.Repeat("-", 70))
			log.Println("💭 AI思维链分析（错误情况）:")
			log.Println(strings.Repeat("-", 70))
			log.Println(fullDecision.CoTTrace)
			log.Print(strings.Repeat("-", 70) + "\n")
		}

		at.decisionLogger.LogDecision(record)
		return fmt.Errorf("获取AI决策失败: %w", err)
	}

	// 输出市场 guardrail 提示，便于执行前人工复核
	hasGuardrailWarnings := false
	for _, d := range fullDecision.Decisions {
		if !isOpenAction(d.Action) {
			continue
		}
		if data, ok := ctx.MarketDataMap[d.Symbol]; ok {
			if warnings := decision.GuardrailWarningsForMarket(data); len(warnings) > 0 {
				if !hasGuardrailWarnings {
					log.Println("🧭 市场硬约束提示（资格检查）：")
					hasGuardrailWarnings = true
				}
				for _, warn := range warnings {
					log.Printf("    - [%s %s] %s (%s) <%s>", d.Symbol, d.Action, warn.Code, warn.Detail, warn.Severity)
					severity := warn.Severity
					if severity == "" {
						severity = "low"
					}
					record.RiskFlags = append(record.RiskFlags, logger.RiskEvent{
						Symbol:   d.Symbol,
						Action:   d.Action,
						Issue:    "market_guardrail_" + warn.Code,
						Severity: severity,
						Detail:   warn.Detail,
					})
				}
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("🧭 %s %s guardrails: %d 条提示", d.Symbol, d.Action, len(warnings)))
			}
		}
	}

	// 风控复核：检测高风险操作
	riskFlags := at.generateRiskFlags(ctx, fullDecision.Decisions)
	if len(riskFlags) > 0 {
		var statusMsg string
		if at.config.RiskReviewEnabled {
			statusMsg = fmt.Sprintf("🛡 检测到 %d 条风险告警，启动风控复核流程...", len(riskFlags))
		} else {
			statusMsg = fmt.Sprintf("🛡 检测到 %d 条风险告警，但风控复核开关已关闭，保留原始决策", len(riskFlags))
		}
		log.Println(statusMsg)
		for _, flag := range riskFlags {
			log.Printf("    - [%s %s] %s (%s) <%s>", flag.Symbol, flag.Action, flag.Issue, flag.Detail, flag.Severity)
			record.RiskFlags = append(record.RiskFlags, logger.RiskEvent{
				Symbol:   flag.Symbol,
				Action:   flag.Action,
				Issue:    flag.Issue,
				Severity: flag.Severity,
				Detail:   flag.Detail,
			})
		}

		if at.config.RiskReviewEnabled {
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("🛡 风控复核触发：%d 条告警", len(riskFlags)))
			reviewedDecision, reviewErr := decision.ReviewDecisions(ctx, fullDecision, riskFlags, at.mcpClient)
			if reviewErr != nil {
				log.Printf("⚠ 风控复核失败，保留原始决策: %v", reviewErr)
				record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("⚠ 风控复核失败: %v", reviewErr))
			} else {
				fullDecision = reviewedDecision
				record.ExecutionLog = append(record.ExecutionLog, "🛡 风控复核完成，决策已根据风险告警更新")
			}
		} else {
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("🛡 风控复核已关闭：保留原始决策（%d 条告警）", len(riskFlags)))
		}
	}

	// 使用最新的决策内容更新记录
	if fullDecision != nil {
		record.InputPrompt = fullDecision.UserPrompt
		record.CoTTrace = fullDecision.CoTTrace
		if len(fullDecision.Decisions) > 0 {
			decisionJSON, _ := json.MarshalIndent(fullDecision.Decisions, "", "  ")
			record.DecisionJSON = string(decisionJSON)
		} else {
			record.DecisionJSON = ""
		}
	}

	// 6. 打印AI思维链
	log.Print("\n" + strings.Repeat("-", 70))
	log.Println("💭 AI思维链分析:")
	log.Println(strings.Repeat("-", 70))
	log.Println(fullDecision.CoTTrace)
	log.Print(strings.Repeat("-", 70) + "\n")

	// 7. 打印AI决策
	log.Printf("📋 AI决策列表 (%d 个):\n", len(fullDecision.Decisions))
	for i, d := range fullDecision.Decisions {
		log.Printf("  [%d] %s: %s - %s", i+1, d.Symbol, d.Action, d.Reasoning)
		if d.Action == "open_long" || d.Action == "open_short" {
			log.Printf("      杠杆: %dx | 仓位: %.2f USDT | 止损: %.4f | 止盈: %.4f",
				d.Leverage, d.PositionSizeUSD, d.StopLoss, d.TakeProfit)
		}
	}
	log.Println()

	// 8. 对决策排序：确保先平仓后开仓（防止仓位叠加超限）
	sortedDecisions := sortDecisionsByPriority(fullDecision.Decisions)

	log.Println("🔄 执行顺序（已优化）: 先平仓→后开仓")
	for i, d := range sortedDecisions {
		log.Printf("  [%d] %s %s", i+1, d.Symbol, d.Action)
	}
	log.Println()

	// 执行决策并记录结果
	for _, d := range sortedDecisions {
		if holdMinutes, pnlPct, deferClose := at.shouldDeferClose(&d, ctx); deferClose {
			msg := fmt.Sprintf("⏸ 延迟平仓: %s %s 持仓%d分钟 | 浮动%.2f%%，等待最小观察窗口", d.Symbol, d.Action, holdMinutes, pnlPct)
			log.Println(msg)
			record.ExecutionLog = append(record.ExecutionLog, msg)
			continue
		}

		if isOpenAction(d.Action) {
			if ok, reason := at.canOpenPositions(time.Now().UTC()); !ok {
				msg := fmt.Sprintf("⏸ %s %s 因 %s 暂停执行", d.Symbol, d.Action, reason)
				log.Println(msg)
				record.ExecutionLog = append(record.ExecutionLog, msg)
				continue
			}
		}

		actionRecord := logger.DecisionAction{
			Action:    d.Action,
			Symbol:    d.Symbol,
			Quantity:  0,
			Leverage:  d.Leverage,
			Price:     0,
			Timestamp: time.Now(),
			Success:   false,
		}

		if err := at.executeDecisionWithRecord(&d, &actionRecord); err != nil {
			log.Printf("❌ 执行决策失败 (%s %s): %v", d.Symbol, d.Action, err)
			actionRecord.Error = err.Error()
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("❌ %s %s 失败: %v", d.Symbol, d.Action, err))
		} else {
			actionRecord.Success = true
			record.ExecutionLog = append(record.ExecutionLog, fmt.Sprintf("✓ %s %s 成功", d.Symbol, d.Action))
			// 成功执行后短暂延迟
			time.Sleep(1 * time.Second)
		}

		record.Decisions = append(record.Decisions, actionRecord)
	}

	// 9. 保存决策记录
	if err := at.decisionLogger.LogDecision(record); err != nil {
		log.Printf("⚠ 保存决策记录失败: %v", err)
	}

	return nil
}

func (at *AutoTrader) collectAuxOpinions(ctx *decision.Context, record *logger.DecisionRecord) []decision.AuxOpinion {
	opinions := make([]decision.AuxOpinion, 0, len(at.ensembleModels))
	if len(at.ensembleModels) == 0 {
		return opinions
	}

	for _, model := range at.ensembleModels {
		displayName := model.label
		if displayName == "" {
			displayName = model.id
		}

		ctxCopy := *ctx
		ctxCopy.AuxOpinions = nil

		fd, err := decision.GetFullDecision(&ctxCopy, model.client)
		if err != nil {
			msg := fmt.Sprintf("⚠️ 辅助模型 %s 调用失败: %v", displayName, err)
			log.Println(msg)
			if record != nil {
				record.ExecutionLog = append(record.ExecutionLog, msg)
			}
			continue
		}

		opinion := summariseAuxDecision(model, fd, at.ensembleSummaryMode)
		opinions = append(opinions, opinion)

		msg := fmt.Sprintf("🧠 辅助模型 %s 建议: %s", displayName, opinion.Summary)
		log.Println(msg)
		if record != nil {
			record.ExecutionLog = append(record.ExecutionLog, msg)
		}
	}

	return opinions
}

func summariseAuxDecision(model ensembleModel, fd *decision.FullDecision, summaryMode string) decision.AuxOpinion {
	mode := strings.ToLower(strings.TrimSpace(summaryMode))
	var summary string

	switch mode {
	case "cot":
		summary = clipString(fd.CoTTrace, 360)
	case "mixed":
		summary = summariseDecisionList(fd.Decisions)
		if summary == "" {
			summary = clipString(fd.CoTTrace, 360)
		}
	default: // decisions
		summary = summariseDecisionList(fd.Decisions)
		if summary == "" {
			summary = clipString(fd.CoTTrace, 360)
		}
	}

	if summary == "" {
		summary = "无明确操作建议（wait）"
	}

	return decision.AuxOpinion{
		ModelID:   model.id,
		ModelName: model.label,
		Provider:  model.provider,
		Weight:    model.weight,
		Role:      model.role,
		Summary:   summary,
		Decisions: fd.Decisions,
		Notes:     model.notes,
	}
}

func summariseDecisionList(decisions []decision.Decision) string {
	if len(decisions) == 0 {
		return ""
	}

	lines := make([]string, 0, 3)
	count := 0
	total := len(decisions)

	for _, d := range decisions {
		if strings.EqualFold(d.Symbol, "MARKET") && (d.Action == "wait" || d.Action == "hold") {
			continue
		}
		desc := fmt.Sprintf("%s %s", d.Action, d.Symbol)
		if d.StrategyHint != "" {
			desc += fmt.Sprintf(" [%s]", d.StrategyHint)
		}
		if d.Confidence > 0 {
			desc += fmt.Sprintf(" conf=%d", d.Confidence)
		}
		if d.RiskUSD > 0 {
			desc += fmt.Sprintf(" risk=%.2f", d.RiskUSD)
		}
		lines = append(lines, desc)
		count++
		if count >= 3 {
			break
		}
	}

	if len(lines) == 0 {
		return ""
	}

	summary := strings.Join(lines, "; ")
	if total > count {
		summary += fmt.Sprintf("; …总计%d条建议", total)
	}

	return summary
}

func clipString(s string, max int) string {
	if max <= 0 {
		return ""
	}
	trimmed := strings.TrimSpace(s)
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

func (at *AutoTrader) populateRecordFromContext(record *logger.DecisionRecord, ctx *decision.Context) {
	record.AccountState = logger.AccountSnapshot{
		TotalBalance:          ctx.Account.TotalEquity,
		AvailableBalance:      ctx.Account.AvailableBalance,
		TotalUnrealizedProfit: ctx.Account.TotalPnL,
		PositionCount:         ctx.Account.PositionCount,
		MarginUsedPct:         ctx.Account.MarginUsedPct,
	}

	record.Positions = record.Positions[:0]
	for _, pos := range ctx.Positions {
		record.Positions = append(record.Positions, logger.PositionSnapshot{
			Symbol:           pos.Symbol,
			Side:             pos.Side,
			PositionAmt:      pos.Quantity,
			EntryPrice:       pos.EntryPrice,
			MarkPrice:        pos.MarkPrice,
			UnrealizedProfit: pos.UnrealizedPnL,
			Leverage:         float64(pos.Leverage),
			LiquidationPrice: pos.LiquidationPrice,
		})
	}

	record.CandidateCoins = record.CandidateCoins[:0]
	for _, coin := range ctx.CandidateCoins {
		record.CandidateCoins = append(record.CandidateCoins, coin.Symbol)
	}
}

// buildTradingContext 构建交易上下文
func (at *AutoTrader) buildTradingContext() (*decision.Context, error) {
	// 1. 获取账户信息
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取账户余额失败: %w", err)
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 2. 获取持仓信息
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var positionInfos []decision.PositionInfo
	totalMarginUsed := 0.0

	// 当前持仓的key集合（用于清理已平仓的记录）
	currentPositionKeys := make(map[string]bool)

	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity // 空仓数量为负，转为正数
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		// 计算占用保证金（估算）
		leverage := 10 // 默认值，实际应该从持仓信息获取
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed

		// 计算盈亏百分比
		pnlPct := 0.0
		if side == "long" {
			pnlPct = ((markPrice - entryPrice) / entryPrice) * float64(leverage) * 100
		} else {
			pnlPct = ((entryPrice - markPrice) / entryPrice) * float64(leverage) * 100
		}

		// 跟踪持仓首次出现时间
		posKey := symbol + "_" + side
		currentPositionKeys[posKey] = true
		if _, exists := at.positionFirstSeenTime[posKey]; !exists {
			// 新持仓，记录当前时间
			at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
		}
		updateTime := at.positionFirstSeenTime[posKey]

		peakPnL := pnlPct
		if maxPnL, exists := at.positionPnLHigh[posKey]; exists {
			if pnlPct > maxPnL {
				peakPnL = pnlPct
				at.positionPnLHigh[posKey] = pnlPct
			} else {
				peakPnL = maxPnL
			}
		} else {
			at.positionPnLHigh[posKey] = pnlPct
		}

		peakPnLUSD := unrealizedPnl
		if maxPnLUSD, exists := at.positionPnLHighUSD[posKey]; exists {
			if unrealizedPnl > maxPnLUSD {
				peakPnLUSD = unrealizedPnl
				at.positionPnLHighUSD[posKey] = unrealizedPnl
			} else {
				peakPnLUSD = maxPnLUSD
			}
		} else {
			at.positionPnLHighUSD[posKey] = unrealizedPnl
		}

		drawdownFromPeakUSD := peakPnLUSD - unrealizedPnl
		if drawdownFromPeakUSD < 0 {
			drawdownFromPeakUSD = 0
		}

		positionInfos = append(positionInfos, decision.PositionInfo{
			Symbol:               symbol,
			Side:                 side,
			EntryPrice:           entryPrice,
			MarkPrice:            markPrice,
			Quantity:             quantity,
			Leverage:             leverage,
			UnrealizedPnL:        unrealizedPnl,
			UnrealizedPnLPct:     pnlPct,
			PeakUnrealizedPnLPct: peakPnL,
			PeakUnrealizedPnLUSD: peakPnLUSD,
			DrawdownFromPeakUSD:  drawdownFromPeakUSD,
			LiquidationPrice:     liquidationPrice,
			MarginUsed:           marginUsed,
			UpdateTime:           updateTime,
		})
	}

	// 清理已平仓的持仓记录
	for key := range at.positionFirstSeenTime {
		if !currentPositionKeys[key] {
			delete(at.positionFirstSeenTime, key)
		}
	}
	for key := range at.positionPnLHigh {
		if !currentPositionKeys[key] {
			delete(at.positionPnLHigh, key)
		}
	}
	for key := range at.positionPnLHighUSD {
		if !currentPositionKeys[key] {
			delete(at.positionPnLHighUSD, key)
		}
	}
	for key := range at.positionMinHoldUntil {
		if !currentPositionKeys[key] {
			delete(at.positionMinHoldUntil, key)
			delete(at.positionGuardStrategy, key)
		}
	}
	for key := range at.positionTargets {
		if !currentPositionKeys[key] {
			delete(at.positionTargets, key)
		}
	}

	candidateCoins, err := at.buildCandidateCoins(positionInfos)
	if err != nil {
		return nil, err
	}

	// 4. 计算总盈亏
	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	// 5. 分析历史表现（最近100个周期，避免长期持仓的交易记录丢失）
	// 假设每3分钟一个周期，100个周期 = 5小时，足够覆盖大部分交易
	performance, err := at.decisionLogger.AnalyzePerformance(100)
	if err != nil {
		log.Printf("⚠️  分析历史表现失败: %v", err)
		// 不影响主流程，继续执行（但设置performance为nil以避免传递错误数据）
		performance = nil
	}

	recentRiskAlerts := make([]decision.RiskFlag, 0)
	recentGuardrails := make([]decision.MarketGuardrailWarning, 0)
	recentRiskUsage := 0.0
	if records, err := at.decisionLogger.GetLatestRecords(5); err == nil {
		for _, rec := range records {
			for _, evt := range rec.RiskFlags {
				riskFlag := decision.RiskFlag{
					Symbol:   evt.Symbol,
					Action:   evt.Action,
					Issue:    evt.Issue,
					Severity: evt.Severity,
					Detail:   evt.Detail,
				}

				if strings.HasPrefix(evt.Issue, "market_guardrail_") {
					code := strings.TrimPrefix(evt.Issue, "market_guardrail_")
					recentGuardrails = append(recentGuardrails, decision.MarketGuardrailWarning{
						Code:     code,
						Severity: evt.Severity,
						Detail:   evt.Detail,
					})
				} else {
					recentRiskAlerts = append(recentRiskAlerts, riskFlag)
				}
			}
			for _, act := range rec.Decisions {
				if act.RiskUSD <= 0 {
					continue
				}
				switch act.Action {
				case "open_long", "open_short":
					if act.RiskUSD > recentRiskUsage {
						recentRiskUsage = act.RiskUSD
					}
				}
			}
		}
	}

	limit := decision.MaxRecentGuardrailSnapshots
	if len(recentRiskAlerts) > limit {
		recentRiskAlerts = recentRiskAlerts[len(recentRiskAlerts)-limit:]
	}
	if len(recentGuardrails) > limit {
		recentGuardrails = recentGuardrails[len(recentGuardrails)-limit:]
	}

	riskBudget := at.baseRiskBudget(totalEquity)

	// 6. 构建上下文
	ctx := &decision.Context{
		CurrentTime:      time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes:   int(time.Since(at.startTime).Minutes()),
		CallCount:        at.callCount,
		BTCETHLeverage:   at.config.BTCETHLeverage,  // 使用配置的杠杆倍数
		AltcoinLeverage:  at.config.AltcoinLeverage, // 使用配置的杠杆倍数
		SystemPromptPath: at.config.SystemPromptPath,
		Account: decision.AccountInfo{
			TotalEquity:      totalEquity,
			AvailableBalance: availableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			MarginUsed:       totalMarginUsed,
			MarginUsedPct:    marginUsedPct,
			PositionCount:    len(positionInfos),
		},
		Positions:        positionInfos,
		CandidateCoins:   candidateCoins,
		Performance:      performance, // 添加历史表现分析
		RecentRiskAlerts: recentRiskAlerts,
		RecentGuardrails: recentGuardrails,
	}

	perfState, cooling := derivePerformanceCoolingState(performance, riskBudget, recentRiskUsage)
	ctx.PerformanceState = perfState
	ctx.SharpeCooling = cooling

	return ctx, nil
}

func (at *AutoTrader) buildCandidateCoins(positionInfos []decision.PositionInfo) ([]decision.CandidateCoin, error) {
	if len(at.focusSymbols) > 0 {
		result := make([]decision.CandidateCoin, 0, len(at.focusSymbols)+len(positionInfos))
		slots := make(map[string]*decision.CandidateCoin)
		order := make([]string, 0)

		addSymbol := func(symbol string, source string) {
			normalized := market.Normalize(symbol)
			if normalized == "" {
				return
			}
			slot, exists := slots[normalized]
			if !exists {
				slot = &decision.CandidateCoin{Symbol: normalized}
				slots[normalized] = slot
				order = append(order, normalized)
			}
			if source != "" && !containsString(slot.Sources, source) {
				slot.Sources = append(slot.Sources, source)
			}
		}

		for _, sym := range at.focusSymbols {
			addSymbol(sym, "whitelist")
		}
		for _, pos := range positionInfos {
			addSymbol(pos.Symbol, "position")
		}

		for _, symbol := range order {
			result = append(result, *slots[symbol])
		}

		log.Printf("📋 使用手动白名单候选: %d 个", len(result))
		return result, nil
	}

	const ai500Limit = 20
	mergedPool, err := pool.GetMergedCoinPool(ai500Limit)
	if err != nil {
		return nil, fmt.Errorf("获取合并币种池失败: %w", err)
	}

	var candidateCoins []decision.CandidateCoin
	for _, symbol := range mergedPool.AllSymbols {
		sources := mergedPool.SymbolSources[symbol]
		candidateCoins = append(candidateCoins, decision.CandidateCoin{
			Symbol:  symbol,
			Sources: sources,
		})
	}

	log.Printf("📋 合并币种池: AI500前%d + OI_Top20 = 总计%d个候选币种",
		ai500Limit, len(candidateCoins))

	return candidateCoins, nil
}

func (at *AutoTrader) baseRiskBudget(totalEquity float64) float64 {
	if at.maxTradeRiskUSD > 0 {
		return at.maxTradeRiskUSD
	}
	budget := totalEquity * riskBudgetFraction
	if budget <= 0 {
		budget = at.initialBalance * riskBudgetFraction
	}
	return budget
}

func derivePerformanceCoolingState(perf *logger.PerformanceAnalysis, riskBudget float64, recentRiskUsage float64) (string, string) {
	if perf == nil {
		return "", ""
	}

	perfState := ""
	cooling := ""

	if perf.RecentLossStreak >= 3 || perf.RecentPnL <= -riskBudget {
		cooling = "halt_3_cycles"
		perfState = "loss_streak"
	} else if perf.RecentLossStreak >= 2 || perf.RecentPnL <= -riskBudget*0.5 {
		cooling = "only_high_confidence_trades"
		perfState = "drawdown"
	}

	if perf.TotalTrades >= 5 {
		if perf.SharpeRatio < -0.5 {
			cooling = "halt_6_cycles"
			if perfState == "" {
				perfState = "loss_streak"
			}
		} else if perf.SharpeRatio < 0 && cooling == "" {
			cooling = "only_high_confidence_trades"
			if perfState == "" {
				perfState = "drawdown"
			}
		}
	}

	if perf.RecentWinStreak >= 2 && perf.RecentPnL > 0 && cooling == "" {
		perfState = "positive"
	}

	if riskBudget > 0 {
		switch {
		case recentRiskUsage >= riskBudget*1.2:
			cooling = "halt_3_cycles"
			if perfState == "" {
				perfState = "drawdown"
			}
		case recentRiskUsage >= riskBudget*0.95 && cooling == "":
			cooling = "only_high_confidence_trades"
		}
	}

	return perfState, cooling
}

func (at *AutoTrader) enforceOpenRisk(decision *decision.Decision, livePrice float64, side string) (*riskCheckResult, error) {
	if decision == nil {
		return nil, fmt.Errorf("决策为空")
	}
	if livePrice <= 0 {
		return nil, fmt.Errorf("无法获取有效价格")
	}
	if decision.PositionSizeUSD <= 0 {
		return nil, fmt.Errorf("position_size_usd 无效")
	}
	if decision.StopLoss <= 0 {
		return nil, fmt.Errorf("缺少有效止损")
	}
	ctx := at.activeContext
	totalEquity := at.initialBalance
	var snapshotPrice float64
	cooling := ""
	if ctx != nil {
		if ctx.Account.TotalEquity > 0 {
			totalEquity = ctx.Account.TotalEquity
		}
		cooling = ctx.SharpeCooling
		if ctx.MarketDataMap != nil {
			if data, ok := ctx.MarketDataMap[strings.ToUpper(decision.Symbol)]; ok && data != nil {
				snapshotPrice = data.CurrentPrice
			}
		}
	}

	riskLimit := at.baseRiskBudget(totalEquity)
	minRR := defaultMinRewardToRisk

	coolingKey := strings.ToLower(strings.TrimSpace(cooling))
	if strings.HasPrefix(coolingKey, "halt") {
		return nil, fmt.Errorf("冷却状态 %s 禁止新开仓", cooling)
	}
	if coolingKey == "only_high_confidence_trades" {
		if riskLimit > 0 {
			riskLimit = riskLimit * 0.5
		}
		if riskLimit <= 0 {
			tmpBudget := totalEquity * coolingRiskFraction
			if tmpBudget <= 0 {
				tmpBudget = at.initialBalance * coolingRiskFraction
			}
			riskLimit = tmpBudget
		}
		minRR = coolingMinRewardToRisk
		if decision.Confidence < coolingConfidenceMinimum {
			return nil, fmt.Errorf("冷却阶段需要信心≥%d (当前 %d)", coolingConfidenceMinimum, decision.Confidence)
		}
	}
	if riskLimit <= 0 {
		return nil, fmt.Errorf("无法计算风险预算")
	}

	var atr14 float64
	if ctx != nil && ctx.MarketDataMap != nil {
		if data, ok := ctx.MarketDataMap[strings.ToUpper(decision.Symbol)]; ok && data != nil && data.LongerTermContext != nil {
			atr14 = data.LongerTermContext.ATR14
		}
	}

	var stopDistance float64
	switch strings.ToLower(side) {
	case "long":
		if decision.StopLoss >= livePrice {
			return nil, fmt.Errorf("多单止损必须低于现价")
		}
		stopDistance = livePrice - decision.StopLoss
	case "short":
		if decision.StopLoss <= livePrice {
			return nil, fmt.Errorf("空单止损必须高于现价")
		}
		stopDistance = decision.StopLoss - livePrice
	default:
		return nil, fmt.Errorf("未知方向: %s", side)
	}

	stopDistancePct := (stopDistance / livePrice) * 100
	if stopDistancePct < minStopDistancePct {
		return nil, fmt.Errorf("止损距离 %.4f%% 低于最小阈值 %.2f%%", stopDistancePct, minStopDistancePct)
	}

	if atr14 > 0 && stopDistance < atr14 {
		return nil, fmt.Errorf("止损距离 %.4f 低于4h ATR %.4f", stopDistance, atr14)
	}

	riskUSD := decision.PositionSizeUSD * (stopDistance / livePrice)
	if riskUSD <= 0 {
		return nil, fmt.Errorf("计算risk_usd失败")
	}

	if riskUSD > riskLimit {
		scale := riskLimit / riskUSD
		adjustedSize := decision.PositionSizeUSD * scale
		if adjustedSize < minOrderNotionalUSD {
			return nil, fmt.Errorf("缩减后仓位 %.2f 低于最小名义金额 %.2f", adjustedSize, minOrderNotionalUSD)
		}
		log.Printf("  ⚖️ 风控: %s 风险%.2fUSD超限，自动缩仓 %.2f -> %.2f USDT", decision.Symbol, riskUSD, decision.PositionSizeUSD, adjustedSize)
		decision.PositionSizeUSD = adjustedSize
		if decision.RiskUSD > 0 {
			decision.RiskUSD = decision.RiskUSD * scale
		}
		riskUSD = decision.PositionSizeUSD * (stopDistance / livePrice)
	}

	if decision.TakeProfit <= 0 {
		return nil, fmt.Errorf("缺少有效止盈")
	}

	var rewardDistance float64
	switch strings.ToLower(side) {
	case "long":
		if decision.TakeProfit <= livePrice {
			return nil, fmt.Errorf("多单止盈必须高于现价")
		}
		rewardDistance = decision.TakeProfit - livePrice
	case "short":
		if decision.TakeProfit >= livePrice {
			return nil, fmt.Errorf("空单止盈必须低于现价")
		}
		rewardDistance = livePrice - decision.TakeProfit
	}

	rewardToRisk := rewardDistance / stopDistance
	if rewardToRisk < minRR {
		return nil, fmt.Errorf("盈亏比 %.2f 低于最低要求 %.2f", rewardToRisk, minRR)
	}

	if snapshotPrice > 0 {
		driftPct := math.Abs(livePrice-snapshotPrice) / snapshotPrice * 100
		if driftPct > maxSnapshotDriftPct {
			return nil, fmt.Errorf("价格偏移 %.2f%% 超过阈值 %.2f%% (%.4f→%.4f)", driftPct, maxSnapshotDriftPct, snapshotPrice, livePrice)
		}
	}

	decision.RiskUSD = riskUSD
	return &riskCheckResult{
		riskUSD:      riskUSD,
		rewardToRisk: rewardToRisk,
		riskLimitUSD: riskLimit,
	}, nil
}

func clampFloat(min, max, value float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func classifyMarketRegime(data *market.Data) string {
	if data == nil || data.RangeState == nil {
		return "transitional"
	}

	rs := data.RangeState
	if rs != nil {
		if reg := strings.ToLower(strings.TrimSpace(rs.Regime)); reg != "" {
			if strings.Contains(reg, "trend") {
				return "trend"
			}
			if strings.Contains(reg, "range") {
				return "range"
			}
		}

		adx := rs.ADX144h
		widthToATR := rs.WidthToATR14
		touchesHigh := rs.TouchesHigh
		touchesLow := rs.TouchesLow
		age := rs.AgeBars1h

		if adx >= 22 || (widthToATR > 0 && widthToATR < 2.2) {
			return "trend"
		}

		if touchesHigh >= 3 && touchesLow >= 3 && age >= 12 && widthToATR >= 3.5 && (adx == 0 || adx <= 20) {
			return "range"
		}
	}

	return "transitional"
}

func normaliseStrategyHint(hint, regime string) string {
	h := strings.ToLower(strings.TrimSpace(hint))
	switch h {
	case "", "auto":
		return defaultStrategyFromRegime(regime)
	case "trend", "range":
		return h
	case "range_break", "range_breakout", "range_breakdown":
		return "trend"
	case "transitional", "neutral", "balancing":
		return "transitional"
	case "mean_reversion":
		return "range"
	case "momentum":
		return "trend"
	default:
		return defaultStrategyFromRegime(regime)
	}
}

func defaultStrategyFromRegime(regime string) string {
	switch strings.ToLower(strings.TrimSpace(regime)) {
	case "trend", "trend_attempt", "strong_trend":
		return "trend"
	case "range", "range_tradable", "range_wide_enough":
		return "range"
	case "transitional", "transition":
		return "transitional"
	default:
		return "transitional"
	}
}

func (at *AutoTrader) fillActionRecordFromOrder(actionRecord *logger.DecisionAction, order map[string]interface{}) {
	if actionRecord == nil || order == nil {
		return
	}

	if orderID, ok := order["orderId"].(int64); ok {
		actionRecord.OrderID = orderID
	} else if idFloat, ok := getOrderFloat(order, "orderId"); ok {
		actionRecord.OrderID = int64(idFloat)
	}

	if price, ok := getOrderFloat(order, "avgPrice"); ok && price > 0 {
		actionRecord.Price = price
	}
	if qty, ok := getOrderFloat(order, "executedQty"); ok && qty > 0 {
		actionRecord.Quantity = qty
	} else if cumQuote, ok := getOrderFloat(order, "cumQuote"); ok {
		if price, ok := getOrderFloat(order, "avgPrice"); ok && price > 0 {
			actionRecord.Quantity = cumQuote / price
		}
	}
}

func containsString(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func (at *AutoTrader) canOpenPositions(now time.Time) (bool, string) {
	utcNow := now.UTC()
	if at.tradingWindow.Enabled {
		start := at.tradingWindow.StartHour
		end := at.tradingWindow.EndHour
		hour := utcNow.Hour()
		inWindow := false
		if start < end {
			inWindow = hour >= start && hour < end
		} else {
			// 跨午夜，如 20 -> 6
			if start == end {
				inWindow = true
			} else {
				inWindow = hour >= start || hour < end
			}
		}
		if !inWindow {
			return false, fmt.Sprintf("交易窗口(%02d:00-%02d:00 UTC)", start, end)
		}
	}

	for _, evt := range at.majorEvents {
		if evt.Start.IsZero() || evt.End.IsZero() {
			continue
		}
		if (utcNow.Equal(evt.Start) || utcNow.After(evt.Start)) && utcNow.Before(evt.End) {
			name := evt.Name
			if name == "" {
				name = "重大事件"
			}
			return false, fmt.Sprintf("%s 窗口", name)
		}
	}

	return true, ""
}

func getOrderFloat(order map[string]interface{}, key string) (float64, bool) {
	if order == nil {
		return 0, false
	}
	val, ok := order[key]
	if !ok {
		return 0, false
	}

	switch v := val.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f, true
		}
	case json.Number:
		f, err := v.Float64()
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

func (at *AutoTrader) applyOpenGuard(decision *decision.Decision, marketData *market.Data, side string) (time.Duration, string, error) {
	minHold := 45 * time.Minute
	regime := classifyMarketRegime(marketData)
	strategy := normaliseStrategyHint(decision.StrategyHint, regime)
	if strategy == "" {
		strategy = "transitional"
	}
	if strategy == "trend" && minHold > 20*time.Minute {
		minHold = 20 * time.Minute
	}

	if marketData == nil {
		return minHold, strategy, nil
	}

	entryPrice := marketData.CurrentPrice
	if entryPrice <= 0 {
		return minHold, strategy, nil
	}

	if side == "short" {
		if blocked, reason := counterTrendShortReason(marketData); blocked {
			log.Printf("  🛑 守护拒绝: %s 空单逆势 (%s)", decision.Symbol, reason)
			return 0, strategy, fmt.Errorf("counter-trend short guard: %s", reason)
		}
	}

	if strategy == "range" {
		if marketData.RangeState == nil {
			log.Printf("  ⚠ 策略hint=range 但缺少 range_state，降级为 transitional")
			strategy = "transitional"
		} else {
			rs := marketData.RangeState
			isRangeOpportunity := false
			switch side {
			case "long":
				if rs.PriceLocation == "near_low" || (rs.PriceLocation == "mid" && entryPrice <= rs.Mid) {
					isRangeOpportunity = true
				}
			case "short":
				if rs.PriceLocation == "near_high" || (rs.PriceLocation == "mid" && entryPrice >= rs.Mid) {
					isRangeOpportunity = true
				}
			}

			if !isRangeOpportunity {
				log.Printf("  ⚠ 策略hint=range 但当前价格不在区间边界附近，降级为 transitional (price_location=%s)", rs.PriceLocation)
				strategy = "transitional"
			} else {
				relevantTouches := rs.TouchesLow
				if side == "short" {
					relevantTouches = rs.TouchesHigh
				}
				if rs.AgeBars1h < 12 && relevantTouches < 3 {
					log.Printf("  🧭 守护拒绝: %s 区间确认不足 (touches=%d, age_1h=%d) 等待更多确认后再开仓", decision.Symbol, relevantTouches, rs.AgeBars1h)
					return 0, "range_gate_unconfirmed", fmt.Errorf("range guard: %s 需等待更多触碰或时间确认", decision.Symbol)
				}

				touchesStrongHigh := rs.TouchesHigh >= 3
				touchesStrongLow := rs.TouchesLow >= 3
				touchesSupportHigh := rs.TouchesHigh >= 2
				touchesSupportLow := rs.TouchesLow >= 2

				touchesBothConfirmed := touchesStrongHigh && touchesStrongLow
				touchesFlexible := false
				switch side {
				case "long":
					touchesFlexible = touchesStrongLow && touchesSupportHigh
				case "short":
					touchesFlexible = touchesStrongHigh && touchesSupportLow
				default:
					touchesFlexible = (touchesStrongHigh && touchesSupportLow) || (touchesStrongLow && touchesSupportHigh)
				}

				ageMature := rs.AgeBars1h >= 12
				ageDeveloping := rs.AgeBars1h >= 10
				adxMature := rs.ADX144h <= 0 || rs.ADX144h < 20
				adxDeveloping := rs.ADX144h <= 0 || rs.ADX144h < 25
				adxRelaxed := rs.ADX144h <= 0 || rs.ADX144h < 27

				isMatureRange := touchesBothConfirmed && ageMature && adxMature
				isDevelopingRange := (touchesBothConfirmed && ageDeveloping && adxDeveloping) ||
					(touchesFlexible && ageDeveloping && adxRelaxed)

				if !isDevelopingRange {
					atrPct := 0.0
					if rs.ATR14 > 0 && entryPrice > 0 {
						atrPct = (rs.ATR14 / entryPrice) * 100
					}
					rsi15 := 0.0
					if marketData.MidTermContext != nil {
						rsi15 = marketData.MidTermContext.RSI14
					}
					if rs.TouchesHigh >= 2 && rs.TouchesLow >= 2 && rs.AgeBars1h >= 8 && atrPct >= 2.0 {
						if (side == "short" && rsi15 >= 67) || (side == "long" && rsi15 <= 33) {
							log.Printf("  ⚠ 区间触顶/触底尚不足但满足RSI/ATR过滤 (touch_high=%d touch_low=%d rsi15=%.1f atr%%=%.2f)，降级为 range_developing", rs.TouchesHigh, rs.TouchesLow, rsi15, atrPct)
							isDevelopingRange = true
						}
					}
				}

				if !isMatureRange {
					if !isDevelopingRange {
						log.Printf("  🧭 守护拒绝: %s 区间信号尚未确认 (touch_high=%d touch_low=%d age_1h=%d adx4h=%.2f)", decision.Symbol, rs.TouchesHigh, rs.TouchesLow, rs.AgeBars1h, rs.ADX144h)
						return 0, "range_unconfirmed", fmt.Errorf("range guard: %s 区间往返不足或ADX过高", decision.Symbol)
					}

					log.Printf("  ⚠ 区间处于形成阶段: %s touch_high=%d touch_low=%d age_1h=%d adx4h=%.2f，缩减风险后允许尝试", decision.Symbol, rs.TouchesHigh, rs.TouchesLow, rs.AgeBars1h, rs.ADX144h)
					strategy = "range_developing"

					if minHold < 90*time.Minute {
						minHold = 90 * time.Minute
					}
					if decision.RiskUSD > 0 {
						reduced := decision.RiskUSD * 0.6
						if reduced < decision.RiskUSD {
							log.Printf("  🔧 开发中区间: 调整 risk_usd %.2f -> %.2f", decision.RiskUSD, reduced)
							decision.RiskUSD = reduced
						}
					}
					if decision.PositionSizeUSD > 0 {
						reduced := decision.PositionSizeUSD * 0.75
						if reduced < decision.PositionSizeUSD {
							log.Printf("  🔧 开发中区间: 调整仓位 %.2f -> %.2f", decision.PositionSizeUSD, reduced)
							decision.PositionSizeUSD = reduced
						}
					}
				} else {
					strategy = "range"
				}

				// 根据区间寿命动态调整最小持仓时间，最多延长至 3 小时
				baseHold := 45 * time.Minute
				if rs.AgeBars1h >= 6 {
					baseHold = 90 * time.Minute
				}
				if rs.AgeBars1h >= 12 {
					baseHold = 120 * time.Minute
				}
				if rs.AgeBars1h >= 18 {
					baseHold = 150 * time.Minute
				}
				if baseHold > 180*time.Minute {
					baseHold = 180 * time.Minute
				}
				if baseHold > minHold {
					minHold = baseHold
				}

				bufferMultiplier := 0.3 + 0.05*rs.WidthToATR14
				bufferMultiplier = clampFloat(0.3, 0.6, bufferMultiplier)
				buffer := bufferMultiplier * rs.ATR14

				if buffer > 0 {
					switch side {
					case "long":
						desiredStop := entryPrice - buffer
						if desiredStop > 0 && (decision.StopLoss <= 0 || decision.StopLoss > desiredStop) {
							if decision.StopLoss > 0 {
								log.Printf("  🔧 执行守护: %s 调整止损 %.4f -> %.4f (range buffer %.2fx ATR)", decision.Symbol, decision.StopLoss, desiredStop, bufferMultiplier)
							} else {
								log.Printf("  🔧 执行守护: %s 设置守护止损 %.4f (range buffer %.2fx ATR)", decision.Symbol, desiredStop, bufferMultiplier)
							}
							decision.StopLoss = desiredStop
						}
					case "short":
						desiredStop := entryPrice + buffer
						if desiredStop > 0 && (decision.StopLoss <= 0 || decision.StopLoss < desiredStop) {
							if decision.StopLoss > 0 {
								log.Printf("  🔧 执行守护: %s 调整止损 %.4f -> %.4f (range buffer %.2fx ATR)", decision.Symbol, decision.StopLoss, desiredStop, bufferMultiplier)
							} else {
								log.Printf("  🔧 执行守护: %s 设置守护止损 %.4f (range buffer %.2fx ATR)", decision.Symbol, desiredStop, bufferMultiplier)
							}
							decision.StopLoss = desiredStop
						}
					}
				}

				if tightened := applyRangeStopTightening(decision, marketData, side); tightened != "" {
					log.Printf("  🔒 区间止损收紧: %s", tightened)
				}

				if decision.RiskUSD > 0 && decision.StopLoss > 0 {
					riskDistance := math.Abs(entryPrice - decision.StopLoss)
					if riskDistance > 0 {
						allowedSize := (decision.RiskUSD * entryPrice) / riskDistance
						if allowedSize > 0 && allowedSize < decision.PositionSizeUSD {
							log.Printf("  🔧 执行守护: %s 调整仓位 %.2f -> %.2f 以维持风险 %.2f USD", decision.Symbol, decision.PositionSizeUSD, allowedSize, decision.RiskUSD)
							decision.PositionSizeUSD = allowedSize
						}
					}
				}

				if decision.PositionSizeUSD <= 0 {
					return 0, "range_unconfirmed", fmt.Errorf("range guard: %s 调整后仓位无效", decision.Symbol)
				}

				log.Printf("  🛡 执行守护: %s 设置最小持仓 %.0f 分钟 (策略=range)", decision.Symbol, minHold.Minutes())
				return minHold, "range", nil
			}
		}
	}

	if decision.StrategyHint != "" && strategy != strings.ToLower(strings.TrimSpace(regime)) && strategy != "transitional" {
		log.Printf("  ℹ️ 策略hint=%s 与市场识别=%s，按hint执行", strategy, regime)
	}

	if strategy == "trend" {
		if minHold < 60*time.Minute {
			minHold = 60 * time.Minute
		}
		if marketData.RangeState != nil && marketData.RangeState.ADX144h >= 28 {
			minHold = 90 * time.Minute
		}
	} else if strategy == "transitional" && regime == "range" {
		if minHold < 60*time.Minute {
			minHold = 60 * time.Minute
		}
	}

	if decision.RiskUSD > 0 && decision.StopLoss > 0 {
		riskDistance := math.Abs(entryPrice - decision.StopLoss)
		if riskDistance > 0 {
			allowedSize := (decision.RiskUSD * entryPrice) / riskDistance
			if allowedSize > 0 && allowedSize < decision.PositionSizeUSD {
				log.Printf("  🔧 执行守护: %s 调整仓位 %.2f -> %.2f 以维持风险 %.2f USD", decision.Symbol, decision.PositionSizeUSD, allowedSize, decision.RiskUSD)
				decision.PositionSizeUSD = allowedSize
			}
		}
	}

	log.Printf("  🛡 执行守护: %s 设置最小持仓 %.0f 分钟 (策略=%s, regime=%s)", decision.Symbol, minHold.Minutes(), strategy, regime)
	return minHold, strategy, nil
}

func applyRangeStopTightening(decision *decision.Decision, data *market.Data, side string) string {
	if decision == nil || data == nil || data.RangeState == nil {
		return ""
	}
	if decision.StopLoss <= 0 || data.CurrentPrice <= 0 {
		return ""
	}

	rs := data.RangeState
	entry := data.CurrentPrice

	buffer := rs.ATR14 * 0.25
	if buffer <= 0 {
		buffer = entry * 0.0025
	}
	if buffer <= 0 {
		return ""
	}

	switch side {
	case "short":
		if rs.High <= 0 {
			return ""
		}
		cap := rs.High + buffer
		if decision.StopLoss > cap {
			original := decision.StopLoss
			decision.StopLoss = cap
			return fmt.Sprintf("short: 止损 %.5f -> %.5f (high %.5f + buffer %.5f)", original, decision.StopLoss, rs.High, buffer)
		}
	case "long":
		if rs.Low <= 0 {
			return ""
		}
		cap := rs.Low - buffer
		if cap > 0 && decision.StopLoss < cap {
			original := decision.StopLoss
			decision.StopLoss = cap
			return fmt.Sprintf("long: 止损 %.5f -> %.5f (low %.5f - buffer %.5f)", original, decision.StopLoss, rs.Low, buffer)
		}
	}

	return ""
}

func counterTrendShortReason(data *market.Data) (bool, string) {
	if data == nil {
		return false, ""
	}

	var reasons []string
	if data.MidTermContext != nil && data.MidTermContext.RSI14 > 65 {
		reasons = append(reasons, fmt.Sprintf("15m RSI %.1f>65", data.MidTermContext.RSI14))
	}
	if data.HourlyContext != nil && data.HourlyContext.RSI14 > 65 {
		reasons = append(reasons, fmt.Sprintf("1h RSI %.1f>65", data.HourlyContext.RSI14))
	}

	bullCount := 0
	if isBullishSnapshot(data.MidTermContext) {
		bullCount++
	}
	if isBullishSnapshot(data.HourlyContext) {
		bullCount++
	}
	if bullCount >= 2 {
		reasons = append(reasons, "15m/1h 均为多头结构")
	}

	if len(reasons) == 0 {
		return false, ""
	}
	return true, strings.Join(reasons, "; ")
}

func isBullishSnapshot(tf *market.TimeframeSnapshot) bool {
	if tf == nil {
		return false
	}
	return tf.EMA20 > tf.EMA50 && tf.MACD >= 0
}

func (at *AutoTrader) applyDrawdownPositionControls(decision *decision.Decision, side string) {
	if decision == nil || at.activeContext == nil {
		return
	}

	state := strings.ToLower(strings.TrimSpace(at.activeContext.PerformanceState))
	if state == "" {
		return
	}

	hint := strings.ToLower(strings.TrimSpace(decision.StrategyHint))
	isTrend := hint == "trend" || hint == "momentum"

	var scale float64
	switch state {
	case "loss_streak":
		scale = 0.4
		if isTrend {
			scale = 0.5
		}
	case "drawdown":
		scale = 0.6
		if isTrend {
			scale = 0.75
		}
	default:
		return
	}

	if decision.PositionSizeUSD > 0 {
		original := decision.PositionSizeUSD
		decision.PositionSizeUSD = original * scale
		log.Printf("  ⚖️ Drawdown守护: %s %s 缩减仓位 %.2f -> %.2f USDT", decision.Symbol, side, original, decision.PositionSizeUSD)
	}
	if decision.RiskUSD > 0 {
		decision.RiskUSD = decision.RiskUSD * scale
	}

	isMajor := strings.EqualFold(decision.Symbol, "BTCUSDT") || strings.EqualFold(decision.Symbol, "ETHUSDT")
	maxLev := 1
	if isMajor {
		maxLev = at.config.BTCETHLeverage
		if state == "loss_streak" {
			maxLev = minInt(maxLev, 3)
		} else {
			maxLev = minInt(maxLev, 4)
		}
	} else {
		maxLev = at.config.AltcoinLeverage
		if state == "loss_streak" {
			maxLev = minInt(maxLev, 2)
		} else {
			maxLev = minInt(maxLev, 3)
		}
	}
	if maxLev < 1 {
		maxLev = 1
	}

	if decision.Leverage > maxLev {
		log.Printf("  ⚖️ Drawdown守护: %s %s 杠杆 %dx -> %dx", decision.Symbol, side, decision.Leverage, maxLev)
		decision.Leverage = maxLev
	}
}

func (at *AutoTrader) registerPositionTargets(decision *decision.Decision, quantity, entryPrice float64, side string) {
	posKey := decision.Symbol + "_" + side

	if quantity <= 0 {
		delete(at.positionTargets, posKey)
		return
	}

	targets := make([]*positionTargetState, 0, len(decision.TakeProfitTargets)+1)
	remaining := quantity
	carryPortion := 0.0

	strategy := strings.ToLower(at.positionGuardStrategy[posKey])
	marketData := at.lastMarketData[strings.ToUpper(decision.Symbol)]

	if len(decision.TakeProfitTargets) > 0 {
		for _, tp := range decision.TakeProfitTargets {
			if tp.Price <= 0 {
				continue
			}

			fraction := tp.SizePct
			if fraction <= 0 && tp.SizeUSD > 0 && decision.PositionSizeUSD > 0 {
				fraction = tp.SizeUSD / decision.PositionSizeUSD
			}
			if fraction <= 0 {
				continue
			}

			portion := fraction * quantity
			if portion > remaining {
				portion = remaining
			}
			if portion <= 0 {
				continue
			}

			price := at.adjustTargetPriceForRange(decision.Symbol, side, strategy, tp.Price, marketData)
			profitPct := calcProfitPct(side, entryPrice, price)
			if profitPct < minRangeTpProfitPct {
				carryPortion += portion
				continue
			}

			portion += carryPortion
			carryPortion = 0

			targets = append(targets, &positionTargetState{
				Price:    price,
				Quantity: portion,
				SizePct:  portion / quantity,
				Kind:     tp.Kind,
			})
			remaining -= portion
			if remaining <= quantity*0.001 {
				remaining = 0
				break
			}
		}
	}

	if remaining+carryPortion > 0 && decision.TakeProfit > 0 {
		price := at.adjustTargetPriceForRange(decision.Symbol, side, strategy, decision.TakeProfit, marketData)
		profitPct := calcProfitPct(side, entryPrice, price)
		if profitPct >= minRangeTpProfitPct {
			finalQty := remaining + carryPortion
			targets = append(targets, &positionTargetState{
				Price:    price,
				Quantity: finalQty,
				SizePct:  finalQty / quantity,
				Kind:     "final",
			})
			remaining = 0
			carryPortion = 0
		}
	}

	if carryPortion > 0 && len(targets) > 0 {
		last := targets[len(targets)-1]
		last.Quantity += carryPortion
		last.SizePct = last.Quantity / quantity
		carryPortion = 0
	}

	if len(targets) == 0 {
		delete(at.positionTargets, posKey)
		return
	}

	if side == "long" {
		sort.Slice(targets, func(i, j int) bool {
			return targets[i].Price < targets[j].Price
		})
	} else {
		sort.Slice(targets, func(i, j int) bool {
			return targets[i].Price > targets[j].Price
		})
	}

	at.positionTargets[posKey] = &positionManagementState{
		StopLoss:        decision.StopLoss,
		InitialQuantity: quantity,
		Targets:         targets,
	}

	for _, tgt := range targets {
		tag := tgt.Kind
		if tag == "" {
			tag = "partial"
		}
		log.Printf("  🎯 分批止盈计划: %s %s [%s] 目标价%.4f 覆盖≈%.1f%% (%.4f)",
			decision.Symbol, side, tag, tgt.Price, tgt.SizePct*100, tgt.Quantity)
	}
}

func (at *AutoTrader) adjustTargetPriceForRange(symbol, side, strategy string, price float64, data *market.Data) float64 {
	if price <= 0 || data == nil || data.RangeState == nil {
		return price
	}
	if strategy != "range_developing" {
		return price
	}

	rs := data.RangeState
	mid := rs.Mid
	if mid <= 0 || rs.ATR14 <= 0 && rs.Width <= 0 {
		return price
	}

	bump := rs.ATR14 * rangeMidAtrBumpRatio
	if bump <= 0 {
		bump = rs.Width * 0.1
	}
	if bump <= 0 {
		return price
	}

	switch strings.ToLower(side) {
	case "long":
		minPrice := mid + bump
		if price < minPrice {
			return minPrice
		}
	case "short":
		maxPrice := mid - bump
		if price > maxPrice {
			return maxPrice
		}
	}

	return price
}

func calcProfitPct(side string, entryPrice, targetPrice float64) float64 {
	if entryPrice <= 0 || targetPrice <= 0 {
		return 0
	}
	switch strings.ToLower(side) {
	case "short":
		return (entryPrice - targetPrice) / entryPrice
	default:
		return (targetPrice - entryPrice) / entryPrice
	}
}

func (at *AutoTrader) shouldDeferClose(decision *decision.Decision, ctx *decision.Context) (int, float64, bool) {
	if ctx == nil || decision == nil {
		return 0, 0, false
	}

	var side string
	switch decision.Action {
	case "close_long":
		side = "long"
	case "close_short":
		side = "short"
	default:
		return 0, 0, false
	}

	pos := findPositionInfo(ctx.Positions, decision.Symbol, side)
	if pos == nil {
		return 0, 0, false
	}

	holdMinutes := 0
	if pos.UpdateTime > 0 {
		now := time.Now()
		seen := time.UnixMilli(pos.UpdateTime)
		if now.After(seen) {
			holdMinutes = int(now.Sub(seen).Minutes())
		}
	}

	if holdMinutes >= minCloseHoldMinutes {
		return 0, 0, false
	}
	if math.Abs(pos.UnrealizedPnLPct) >= minClosePnLPct {
		return 0, 0, false
	}

	return holdMinutes, pos.UnrealizedPnLPct, true
}

func findPositionInfo(positions []decision.PositionInfo, symbol, side string) *decision.PositionInfo {
	for i := range positions {
		if !strings.EqualFold(positions[i].Symbol, symbol) {
			continue
		}
		if strings.EqualFold(positions[i].Side, side) {
			return &positions[i]
		}
	}
	return nil
}

func (at *AutoTrader) computeProfitProtectionThresholds(pos decision.PositionInfo, marketCache map[string]*market.Data) (profitProtectionThresholds, error) {
	thresholds := profitProtectionThresholds{
		activationPct: defaultProfitProtectActivationPct,
		retracePct:    defaultProfitProtectMinRetracePct,
		retentionRate: defaultProfitProtectRetentionRatio,
		lockFloorPct:  defaultProfitProtectLockFloorPct,
		atrPercent:    0,
	}

	leverage := math.Max(float64(pos.Leverage), 1)

	var data *market.Data
	var err error
	if marketCache != nil {
		if cached, ok := marketCache[pos.Symbol]; ok {
			data = cached
		}
	}
	if data == nil {
		data, err = market.Get(pos.Symbol)
		if err != nil {
			log.Printf("⚠️  获取 %s 市场数据失败（使用默认盈利保护阈值）: %v", pos.Symbol, err)
		} else if marketCache != nil {
			marketCache[pos.Symbol] = data
		}
	}

	atrPct := extractAtrPercent(data)
	if atrPct <= 0 {
		atrPct = 5.0 // fallback: assume moderate波动
	}
	thresholds.atrPercent = atrPct

	activation := 9.0 + atrPct*0.35
	if leverage > 5 {
		activation -= (leverage - 5) * 0.6
	} else {
		activation += (5 - leverage) * 0.4
	}
	thresholds.activationPct = clampFloat(6.0, 18.0, activation)

	retrace := 2.3 + atrPct*0.22
	retrace -= math.Max(leverage-8, 0) * 0.17
	thresholds.retracePct = clampFloat(1.5, 6.0, retrace)

	retention := 0.45 + math.Max(leverage-6, 0)*0.025
	retention -= math.Max(atrPct-8, 0) * 0.01
	thresholds.retentionRate = clampFloat(0.38, 0.65, retention)

	lockFloor := defaultProfitProtectLockFloorPct + atrPct*0.2
	if leverage > 10 {
		lockFloor += (leverage - 10) * 0.3
	}
	thresholds.lockFloorPct = clampFloat(3.0, 12.0, lockFloor)

	return thresholds, err
}

func extractAtrPercent(data *market.Data) float64 {
	if data == nil || data.CurrentPrice <= 0 {
		return 0
	}

	if data.LongerTermContext != nil {
		if data.LongerTermContext.ATR14 > 0 {
			return (data.LongerTermContext.ATR14 / data.CurrentPrice) * 100
		}
		if data.LongerTermContext.ATR3 > 0 {
			return (data.LongerTermContext.ATR3 / data.CurrentPrice) * 100
		}
	}

	return 0
}

// applyTargetManagement 执行分批止盈管理（命中目标价时部分平仓）
func (at *AutoTrader) applyTargetManagement(ctx *decision.Context, record *logger.DecisionRecord) (bool, error) {
	if len(at.positionTargets) == 0 || ctx == nil {
		return false, nil
	}

	managed := false
	priceCache := make(map[string]*market.Data)
	const qtyTolerance = 1e-6

	for _, pos := range ctx.Positions {
		posKey := pos.Symbol + "_" + pos.Side
		state, ok := at.positionTargets[posKey]
		if !ok || state == nil || len(state.Targets) == 0 {
			continue
		}

		currentQty := pos.Quantity
		if currentQty < 0 {
			currentQty = math.Abs(currentQty)
		}
		if currentQty <= qtyTolerance {
			continue
		}

		data, cached := priceCache[pos.Symbol]
		if !cached {
			md, err := market.Get(pos.Symbol)
			if err != nil {
				log.Printf("⚠️  获取 %s 市场数据失败（跳过分批止盈检查）: %v", pos.Symbol, err)
			} else {
				data = md
			}
			priceCache[pos.Symbol] = data
		}

		price := pos.MarkPrice
		if data != nil && data.CurrentPrice > 0 {
			price = data.CurrentPrice
		}
		if price <= 0 {
			continue
		}

		cancelledOrders := false
		executedThisRound := false
		remainingQty := currentQty

		for _, target := range state.Targets {
			if target == nil || target.Filled {
				continue
			}

			triggered := false
			switch pos.Side {
			case "long":
				if target.Price > 0 && price >= target.Price {
					triggered = true
				}
			case "short":
				if target.Price > 0 && price <= target.Price {
					triggered = true
				}
			}
			if !triggered {
				continue
			}

			if !cancelledOrders {
				if err := at.trader.CancelAllOrders(pos.Symbol); err != nil {
					log.Printf("  ⚠ 取消 %s 未完成委托失败: %v", pos.Symbol, err)
				}
				cancelledOrders = true
			}

			// 若这是最后一个未完成目标，则覆盖为剩余全部仓位
			isLast := true
			for _, other := range state.Targets {
				if other == nil || other == target || other.Filled {
					continue
				}
				isLast = false
				break
			}

			qtyToClose := target.Quantity
			if qtyToClose > remainingQty || isLast {
				qtyToClose = remainingQty
			}
			if qtyToClose <= qtyTolerance {
				target.Filled = true
				continue
			}

			action := "close_long_partial"
			if pos.Side == "short" {
				action = "close_short_partial"
			}

			actionRecord := logger.DecisionAction{
				Action:    action,
				Symbol:    pos.Symbol,
				Quantity:  qtyToClose,
				Leverage:  pos.Leverage,
				Price:     price,
				Timestamp: time.Now(),
			}

			var (
				order map[string]interface{}
				err   error
			)

			if pos.Side == "long" {
				order, err = at.trader.CloseLong(pos.Symbol, qtyToClose)
			} else {
				order, err = at.trader.CloseShort(pos.Symbol, qtyToClose)
			}

			if err != nil {
				log.Printf("❌ 分批止盈失败: %s %s 目标价%.4f 错误: %v", pos.Symbol, pos.Side, target.Price, err)
				actionRecord.Success = false
				actionRecord.Error = err.Error()
				record.Decisions = append(record.Decisions, actionRecord)
				record.ExecutionLog = append(record.ExecutionLog,
					fmt.Sprintf("❌ 分批止盈失败: %s %s 目标价%.4f -> %v", pos.Symbol, pos.Side, target.Price, err))
				continue
			}

			if orderID, ok := extractOrderID(order); ok {
				actionRecord.OrderID = orderID
			}
			actionRecord.Success = true
			record.Decisions = append(record.Decisions, actionRecord)

			tag := target.Kind
			if tag == "" {
				tag = "partial"
			}
			executedPct := 0.0
			if state.InitialQuantity > 0 {
				executedPct = (qtyToClose / state.InitialQuantity) * 100
			} else if target.SizePct > 0 {
				executedPct = target.SizePct * 100
			}
			log.Printf("  ✓ 分批止盈: %s %s [%s] 目标价%.4f 平仓 %.4f (≈%.1f%%)", pos.Symbol, pos.Side, tag, target.Price, qtyToClose, executedPct)
			record.ExecutionLog = append(record.ExecutionLog,
				fmt.Sprintf("✓ 分批止盈: %s %s [%s] 目标价%.4f 平仓%.4f (≈%.1f%%)",
					pos.Symbol, pos.Side, tag, target.Price, qtyToClose, executedPct))

			target.Filled = true
			target.Quantity = qtyToClose
			if state.InitialQuantity > 0 {
				target.SizePct = qtyToClose / state.InitialQuantity
			}

			remainingQty -= qtyToClose
			if remainingQty < 0 {
				remainingQty = 0
			}

			executedThisRound = true
			managed = true
		}

		if !executedThisRound {
			continue
		}

		if remainingQty > qtyTolerance && state.StopLoss > 0 {
			positionSide := "LONG"
			if pos.Side == "short" {
				positionSide = "SHORT"
			}
			if err := at.trader.SetStopLoss(pos.Symbol, positionSide, remainingQty, state.StopLoss); err != nil {
				log.Printf("  ⚠ 重新设置止损失败: %v", err)
				record.ExecutionLog = append(record.ExecutionLog,
					fmt.Sprintf("⚠ 重新设置止损失败 %s %s: %v", pos.Symbol, pos.Side, err))
			} else {
				record.ExecutionLog = append(record.ExecutionLog,
					fmt.Sprintf("🛡 重新设置止损: %s %s 剩余%.4f @ %.4f",
						pos.Symbol, pos.Side, remainingQty, state.StopLoss))
			}
		}

		if remainingQty <= qtyTolerance {
			at.clearPositionState(posKey)
		}
	}

	return managed, nil
}

func (at *AutoTrader) clearPositionState(posKey string) {
	delete(at.positionTargets, posKey)
	delete(at.positionMinHoldUntil, posKey)
	delete(at.positionGuardStrategy, posKey)
	delete(at.positionPnLHigh, posKey)
	delete(at.positionPnLHighUSD, posKey)
	delete(at.positionFirstSeenTime, posKey)
}

// applyProfitProtection 针对高收益回撤执行强制止盈保护
func (at *AutoTrader) applyProfitProtection(ctx *decision.Context, record *logger.DecisionRecord) (bool, error) {
	forcedClosures := 0
	failedClosures := 0
	var errorMessages []string

	marketCache := make(map[string]*market.Data)
	baseRiskUSD := at.initialBalance * 0.03
	if baseRiskUSD <= 0 && ctx != nil {
		baseRiskUSD = ctx.Account.TotalEquity * 0.03
	}
	if baseRiskUSD <= 0 {
		baseRiskUSD = 1
	}
	drawdownTriggerUSD := baseRiskUSD * simpleTrailingDrawdownRatio
	if drawdownTriggerUSD <= 0 {
		drawdownTriggerUSD = 1
	}

	for _, pos := range ctx.Positions {
		posKey := pos.Symbol + "_" + pos.Side
		currentPnL := pos.UnrealizedPnLPct
		currentPnLUSD := pos.UnrealizedPnL

		thresholds, _ := at.computeProfitProtectionThresholds(pos, marketCache)

		peakPnL, tracked := at.positionPnLHigh[posKey]
		if !tracked {
			at.positionPnLHigh[posKey] = currentPnL
			peakPnL = currentPnL
		}

		peakPnLUSD, trackedUSD := at.positionPnLHighUSD[posKey]
		if !trackedUSD {
			at.positionPnLHighUSD[posKey] = currentPnLUSD
			peakPnLUSD = currentPnLUSD
		}

		newPeak := false
		if currentPnL > peakPnL {
			at.positionPnLHigh[posKey] = currentPnL
			if peakPnL < thresholds.activationPct && currentPnL >= thresholds.activationPct {
				msg := fmt.Sprintf("🛡 盈利回撤保护就位: %s %s 当前%.2f%% (激活 ≥%.2f%% | ATR %.2f%% | 杠杆 %dx)",
					pos.Symbol, pos.Side, currentPnL, thresholds.activationPct, thresholds.atrPercent, pos.Leverage)
				log.Println(msg)
				record.ExecutionLog = append(record.ExecutionLog, msg)
			}
			peakPnL = currentPnL
			newPeak = true
		}
		if currentPnLUSD > peakPnLUSD {
			at.positionPnLHighUSD[posKey] = currentPnLUSD
			peakPnLUSD = currentPnLUSD
			newPeak = true
		}
		if newPeak {
			continue
		}

		profitEligible := currentPnL > 0 && (math.Abs(currentPnL) >= minProfitProtectPct || math.Abs(currentPnLUSD) >= minProfitProtectUSD)
		if !profitEligible {
			continue
		}

		minProfitPct := math.Max(at.config.SimpleTrailingFeePct*2, simpleTrailingDefaultFeePct)
		if at.config.SimpleTrailingGuardEnabled &&
			peakPnL >= simpleTrailingActivationPct &&
			peakPnLUSD >= baseRiskUSD &&
			currentPnL >= minProfitPct {

			retraceUSD := peakPnLUSD - currentPnLUSD
			if retraceUSD < 0 {
				retraceUSD = 0
			}

			if retraceUSD >= drawdownTriggerUSD {
				msg := fmt.Sprintf("⚡️ 简易回撤守护触发: %s %s 峰值%.2f%% (%.2f USD) → 当前%.2f%% (%.2f USD)，回吐%.2f USD ≥ %.2f USD (基于初始风险 %.2f USD)",
					pos.Symbol,
					pos.Side,
					peakPnL,
					peakPnLUSD,
					currentPnL,
					currentPnLUSD,
					retraceUSD,
					drawdownTriggerUSD,
					baseRiskUSD)
				log.Println(msg)
				record.ExecutionLog = append(record.ExecutionLog, msg)

				if err := at.trader.CancelAllOrders(pos.Symbol); err != nil {
					log.Printf("  ⚠ 取消 %s 未完成委托失败: %v", pos.Symbol, err)
				}

				var (
					order  map[string]interface{}
					err    error
					action string
				)

				switch pos.Side {
				case "long":
					order, err = at.trader.CloseLong(pos.Symbol, 0)
					action = "close_long"
				case "short":
					order, err = at.trader.CloseShort(pos.Symbol, 0)
					action = "close_short"
				default:
					log.Printf("  ⚠ 未知持仓方向 %s，跳过简易守护执行", pos.Side)
					continue
				}

				actionRecord := logger.DecisionAction{
					Action:    action,
					Symbol:    pos.Symbol,
					Quantity:  pos.Quantity,
					Leverage:  pos.Leverage,
					Price:     pos.MarkPrice,
					Timestamp: time.Now(),
				}

				if err != nil {
					failedClosures++
					errMsg := fmt.Sprintf("简易回撤守护平仓失败 %s %s: %v", pos.Symbol, pos.Side, err)
					log.Printf("❌ %s", errMsg)
					actionRecord.Success = false
					actionRecord.Error = err.Error()
					errorMessages = append(errorMessages, errMsg)
				} else {
					forcedClosures++
					if orderID, ok := extractOrderID(order); ok {
						actionRecord.OrderID = orderID
					}
					actionRecord.Success = true
					record.ExecutionLog = append(record.ExecutionLog,
						fmt.Sprintf("✓ 简易回撤守护平仓: %s %s 锁定利润%.2f%% (峰值%.2f%%)",
							pos.Symbol, pos.Side, math.Max(currentPnL, 0), peakPnL))
					at.clearPositionState(posKey)
				}

				record.Decisions = append(record.Decisions, actionRecord)
				continue
			}
		}

		if peakPnL < thresholds.activationPct {
			continue
		}

		retrace := peakPnL - currentPnL
		lockLevel := math.Max(peakPnL*thresholds.retentionRate, thresholds.lockFloorPct)
		if lockLevel > peakPnL {
			lockLevel = peakPnL
		}

		triggered := false
		if currentPnL <= 0 {
			triggered = true
		} else if currentPnL <= lockLevel && retrace >= thresholds.retracePct {
			triggered = true
		}

		if triggered {
			lockTarget := math.Max(thresholds.retentionRate*peakPnL, thresholds.lockFloorPct)
			logMsg := fmt.Sprintf("🛡 盈利回撤保护触发: %s %s 峰值%.2f%% → 当前%.2f%% (回撤%.2f%% | 激活 ≥%.2f%% | 回撤 ≥%.2f%% | 锁定线 %.2f%% [max %.0f%%峰值, %.2f%%] | ATR %.2f%% | 杠杆 %dx)",
				pos.Symbol,
				pos.Side,
				peakPnL,
				currentPnL,
				retrace,
				thresholds.activationPct,
				thresholds.retracePct,
				lockLevel,
				thresholds.retentionRate*100,
				lockTarget,
				thresholds.atrPercent,
				pos.Leverage)
			log.Println(logMsg)
			record.ExecutionLog = append(record.ExecutionLog, logMsg)

			if err := at.trader.CancelAllOrders(pos.Symbol); err != nil {
				log.Printf("  ⚠ 取消 %s 未完成委托失败: %v", pos.Symbol, err)
			}

			var (
				order  map[string]interface{}
				err    error
				action string
			)

			switch pos.Side {
			case "long":
				order, err = at.trader.CloseLong(pos.Symbol, 0)
				action = "close_long"
			case "short":
				order, err = at.trader.CloseShort(pos.Symbol, 0)
				action = "close_short"
			default:
				log.Printf("  ⚠ 未知持仓方向 %s，跳过盈利保护执行", pos.Side)
				continue
			}

			actionRecord := logger.DecisionAction{
				Action:    action,
				Symbol:    pos.Symbol,
				Quantity:  pos.Quantity,
				Leverage:  pos.Leverage,
				Price:     pos.MarkPrice,
				Timestamp: time.Now(),
			}

			if err != nil {
				failedClosures++
				errMsg := fmt.Sprintf("盈利回撤平仓失败 %s %s: %v", pos.Symbol, pos.Side, err)
				log.Printf("❌ %s", errMsg)
				actionRecord.Success = false
				actionRecord.Error = err.Error()
				errorMessages = append(errorMessages, errMsg)
			} else {
				forcedClosures++
				if orderID, ok := extractOrderID(order); ok {
					actionRecord.OrderID = orderID
				}
				actionRecord.Success = true
				record.ExecutionLog = append(record.ExecutionLog,
					fmt.Sprintf("✓ 盈利回撤保护平仓成功: %s %s 保留利润%.2f%% (锁定线%.2f%%)",
						pos.Symbol, pos.Side, math.Max(currentPnL, 0), lockLevel))
				at.clearPositionState(posKey)
			}

			record.Decisions = append(record.Decisions, actionRecord)
		}
	}

	totalAttempts := forcedClosures + failedClosures
	if totalAttempts == 0 {
		return false, nil
	}

	summary := fmt.Sprintf("🛡 盈利回撤保护：触发%d个持仓，成功%d个，失败%d个",
		totalAttempts, forcedClosures, failedClosures)
	log.Println(summary)
	record.ExecutionLog = append(record.ExecutionLog, summary)
	record.DecisionJSON = "[]"

	if failedClosures > 0 {
		record.Success = false
		record.ErrorMessage = strings.Join(errorMessages, "; ")
	}

	return true, nil
}

func (at *AutoTrader) generateRiskFlags(ctx *decision.Context, decisions []decision.Decision) []decision.RiskFlag {
	flags := make([]decision.RiskFlag, 0)

	const maxPositions = 3
	openSlots := maxPositions - ctx.Account.PositionCount
	if openSlots < 0 {
		openSlots = 0
	}

	riskBudget := ctx.Account.TotalEquity * 0.03
	marginUsed := ctx.Account.MarginUsedPct
	marginHeadroom := decision.MaxMarginUsagePct - marginUsed
	auxConsensus := ctx.AuxConsensus
	majorityWait := auxConsensus != nil && auxConsensus.Majority == "wait" && auxConsensus.TotalModels > 0
	perfState := strings.ToLower(strings.TrimSpace(ctx.PerformanceState))
	auxHistory := countRecentRiskAlerts(ctx.RecentRiskAlerts, "aux_majority_wait")

	openActions := 0
	for _, d := range decisions {
		if isOpenAction(d.Action) {
			openActions++
		}
	}

	if openActions > openSlots {
		for _, d := range decisions {
			if isOpenAction(d.Action) {
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "position_slot_exceeded",
					Severity: "high",
					Detail:   fmt.Sprintf("open_slots=%d, requested_opens=%d", openSlots, openActions),
				})
			}
		}
	}

	sharpe, hasSharpe := extractSharpeRatio(ctx.Performance)

	for _, d := range decisions {
		if !isOpenAction(d.Action) {
			continue
		}

		if majorityWait {
			severity := "high"
			if auxConsensus.TotalModels <= 1 || auxHistory >= 1 {
				severity = "medium"
			}
			if auxHistory >= 3 {
				severity = ""
			}
			if severity != "" {
				detail := fmt.Sprintf("%d/%d 个辅助模型建议观望 (历史触发%d次)", auxConsensus.WaitCount, auxConsensus.TotalModels, auxHistory)
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "aux_majority_wait",
					Severity: severity,
					Detail:   detail,
				})
			}
		} else if auxConsensus != nil && auxConsensus.OpenCount > 0 {
			if supporters, ok := auxDirectionalSupport(auxConsensus, d.Symbol, d.Action); !ok {
				openList := strings.Join(auxConsensus.OpenModels, ", ")
				if openList == "" {
					openList = "无"
				}
				detail := fmt.Sprintf("无辅助模型支持 %s %s；当前开仓模型: %s", d.Symbol, d.Action, openList)
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "aux_consensus_conflict",
					Severity: "medium",
					Detail:   detail,
				})
			} else if len(supporters) > 0 && len(supporters) < auxConsensus.OpenCount {
				detail := fmt.Sprintf("%s %s 仅获得 %d/%d 辅助模型支持", d.Symbol, d.Action, len(supporters), auxConsensus.OpenCount)
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "aux_support_partial",
					Severity: "low",
					Detail:   detail,
				})
			}
		}

		if riskBudget > 0 && d.RiskUSD > riskBudget*1.05 {
			flags = appendRiskFlagOnce(flags, decision.RiskFlag{
				Symbol:   d.Symbol,
				Action:   d.Action,
				Issue:    "risk_budget_exceeded",
				Severity: "high",
				Detail:   fmt.Sprintf("risk_usd %.2f > allowed %.2f", d.RiskUSD, riskBudget),
			})
		}

		if data, ok := ctx.MarketDataMap[d.Symbol]; ok {
			for _, guard := range decision.GuardrailWarningsForMarket(data) {
				if guard.Severity == "low" {
					continue
				}
				detail := guard.Detail
				if detail == "" {
					detail = "deterministic guardrail triggered"
				}
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "market_guardrail_" + guard.Code,
					Severity: guard.Severity,
					Detail:   detail,
				})
			}

			if d.Action == "open_short" {
				if blocked, reason := counterTrendShortReason(data); blocked {
					flags = appendRiskFlagOnce(flags, decision.RiskFlag{
						Symbol:   d.Symbol,
						Action:   d.Action,
						Issue:    "counter_trend_short",
						Severity: "high",
						Detail:   reason,
					})
				}
			}
		}

		if marginUsed >= decision.MaxMarginUsagePct {
			flags = appendRiskFlagOnce(flags, decision.RiskFlag{
				Symbol:   d.Symbol,
				Action:   d.Action,
				Issue:    "margin_usage_critical",
				Severity: "high",
				Detail:   fmt.Sprintf("margin_used_pct %.1f >= %.0f%%", marginUsed, decision.MaxMarginUsagePct),
			})
		} else if marginUsed >= decision.MaxMarginUsagePct-5 {
			flags = appendRiskFlagOnce(flags, decision.RiskFlag{
				Symbol:   d.Symbol,
				Action:   d.Action,
				Issue:    "margin_usage_high",
				Severity: "medium",
				Detail:   fmt.Sprintf("margin_used_pct %.1f >= %.0f%%", marginUsed, decision.MaxMarginUsagePct-5),
			})
		}

		if marginHeadroom <= 5 {
			flags = appendRiskFlagOnce(flags, decision.RiskFlag{
				Symbol:   d.Symbol,
				Action:   d.Action,
				Issue:    "margin_headroom_low",
				Severity: "medium",
				Detail:   fmt.Sprintf("margin_headroom_pct %.1f <= 5%%", marginHeadroom),
			})
		}

		if d.Confidence > 0 && d.Confidence < 75 {
			flags = appendRiskFlagOnce(flags, decision.RiskFlag{
				Symbol:   d.Symbol,
				Action:   d.Action,
				Issue:    "confidence_too_low",
				Severity: "medium",
				Detail:   fmt.Sprintf("confidence %d < 75", d.Confidence),
			})
		}

		if hasSharpe {
			switch {
			case perfState == "loss_streak":
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "strategy_loss_streak",
					Severity: "high",
					Detail:   fmt.Sprintf("loss_streak 状态，sharpe_ratio %.2f", sharpe),
				})
			case sharpe < -0.5:
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "strategy_in_drawdown",
					Severity: "high",
					Detail:   fmt.Sprintf("sharpe_ratio %.2f < -0.5，需要暂停新增仓位", sharpe),
				})
			case sharpe < 0 && perfState != "drawdown":
				flags = appendRiskFlagOnce(flags, decision.RiskFlag{
					Symbol:   d.Symbol,
					Action:   d.Action,
					Issue:    "strategy_underperforming",
					Severity: "medium",
					Detail:   fmt.Sprintf("sharpe_ratio %.2f < 0，仅允许高置信度交易", sharpe),
				})
			}
		}
	}

	return flags
}

func extractSharpeRatio(performance interface{}) (float64, bool) {
	if performance == nil {
		return 0, false
	}
	data, err := json.Marshal(performance)
	if err != nil {
		return 0, false
	}
	var payload struct {
		SharpeRatio float64 `json:"sharpe_ratio"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, false
	}
	return payload.SharpeRatio, true
}

func isOpenAction(action string) bool {
	return action == "open_long" || action == "open_short"
}

func appendRiskFlagOnce(flags []decision.RiskFlag, flag decision.RiskFlag) []decision.RiskFlag {
	for _, existing := range flags {
		if existing.Symbol == flag.Symbol && existing.Action == flag.Action && existing.Issue == flag.Issue {
			return flags
		}
	}
	return append(flags, flag)
}

func countRecentRiskAlerts(alerts []decision.RiskFlag, issue string) int {
	if len(alerts) == 0 || issue == "" {
		return 0
	}
	count := 0
	for _, flag := range alerts {
		if flag.Issue == issue {
			count++
		}
	}
	return count
}

func auxDirectionalSupport(ac *decision.AuxConsensus, symbol, action string) ([]string, bool) {
	if ac == nil || len(ac.SymbolVotes) == 0 || symbol == "" {
		return nil, false
	}
	key := strings.ToUpper(strings.TrimSpace(symbol))
	vote, ok := ac.SymbolVotes[key]
	if !ok || vote == nil {
		return nil, false
	}

	switch action {
	case "open_long":
		if len(vote.LongModels) > 0 {
			return vote.LongModels, true
		}
	case "open_short":
		if len(vote.ShortModels) > 0 {
			return vote.ShortModels, true
		}
	}

	return nil, false
}

// executeDecisionWithRecord 执行AI决策并记录详细信息
func (at *AutoTrader) executeDecisionWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	switch decision.Action {
	case "open_long":
		return at.executeOpenLongWithRecord(decision, actionRecord)
	case "open_short":
		return at.executeOpenShortWithRecord(decision, actionRecord)
	case "close_long":
		return at.executeCloseLongWithRecord(decision, actionRecord)
	case "close_short":
		return at.executeCloseShortWithRecord(decision, actionRecord)
	case "hold", "wait":
		// 无需执行，仅记录
		return nil
	default:
		return fmt.Errorf("未知的action: %s", decision.Action)
	}
}

// executeOpenLongWithRecord 执行开多仓并记录详细信息
func (at *AutoTrader) executeOpenLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📈 开多仓: %s", decision.Symbol)

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "long" {
				return fmt.Errorf("❌ %s 已有多仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_long 决策", decision.Symbol)
			}
		}
	}

	// 获取最新市场快照
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	at.lastMarketData[strings.ToUpper(decision.Symbol)] = marketData

	at.applyDrawdownPositionControls(decision, "long")

	minHoldDuration, guardStrategy, guardErr := at.applyOpenGuard(decision, marketData, "long")
	if guardErr != nil {
		if strings.Contains(guardErr.Error(), "range guard") {
			log.Printf("  ℹ️ 区间守护提示: %v，按AI方案继续执行", guardErr)
		} else {
			return guardErr
		}
	}
	livePrice := marketData.CurrentPrice
	if ask, err := at.trader.GetMarketPrice(decision.Symbol); err == nil && ask > 0 {
		livePrice = ask
	} else if err != nil {
		log.Printf("  ⚠️ 获取实时价格失败，使用快照价: %v", err)
	}
	if livePrice <= 0 {
		return fmt.Errorf("无法获取有效价格")
	}

	riskEval, err := at.enforceOpenRisk(decision, livePrice, "long")
	if err != nil {
		return err
	}
	actionRecord.RiskUSD = riskEval.riskUSD
	actionRecord.RiskLimitUSD = riskEval.riskLimitUSD
	actionRecord.RewardToRisk = riskEval.rewardToRisk

	if decision.PositionSizeUSD <= 0 {
		return fmt.Errorf("守护调整后仓位为0，取消开仓")
	}
	if decision.PositionSizeUSD < minOrderNotionalUSD {
		return fmt.Errorf("计划名义金额 %.2f USDT 低于交易所最小下单 %.2f USDT，取消开仓", decision.PositionSizeUSD, minOrderNotionalUSD)
	}

	quantity := decision.PositionSizeUSD / livePrice
	actionRecord.Quantity = quantity
	actionRecord.Price = livePrice

	// 开仓
	order, err := at.trader.OpenLong(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	at.fillActionRecordFromOrder(actionRecord, order)

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_long"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
	at.positionMinHoldUntil[posKey] = time.Now().Add(minHoldDuration)
	if guardStrategy != "" {
		at.positionGuardStrategy[posKey] = guardStrategy
	} else {
		at.positionGuardStrategy[posKey] = "trend"
	}

	if len(decision.TakeProfitTargets) > 0 {
		at.registerPositionTargets(decision, quantity, marketData.CurrentPrice, "long")
	} else {
		delete(at.positionTargets, posKey)
	}

	// 设置止损止盈
	if err := at.trader.SetStopLoss(decision.Symbol, "LONG", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if len(decision.TakeProfitTargets) == 0 {
		if decision.TakeProfit > 0 {
			if err := at.trader.SetTakeProfit(decision.Symbol, "LONG", quantity, decision.TakeProfit); err != nil {
				log.Printf("  ⚠ 设置止盈失败: %v", err)
			}
		}
	} else {
		if state, ok := at.positionTargets[posKey]; ok && state != nil {
			log.Printf("  🎯 已加载 %d 个分批止盈目标，由守护层动态执行", len(state.Targets))
		}
	}

	return nil
}

// executeOpenShortWithRecord 执行开空仓并记录详细信息
func (at *AutoTrader) executeOpenShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  📉 开空仓: %s", decision.Symbol)

	// ⚠️ 关键：检查是否已有同币种同方向持仓，如果有则拒绝开仓（防止仓位叠加超限）
	positions, err := at.trader.GetPositions()
	if err == nil {
		for _, pos := range positions {
			if pos["symbol"] == decision.Symbol && pos["side"] == "short" {
				return fmt.Errorf("❌ %s 已有空仓，拒绝开仓以防止仓位叠加超限。如需换仓，请先给出 close_short 决策", decision.Symbol)
			}
		}
	}

	// 获取最新市场快照
	marketData, err := market.Get(decision.Symbol)
	if err != nil {
		return err
	}
	at.lastMarketData[strings.ToUpper(decision.Symbol)] = marketData

	at.applyDrawdownPositionControls(decision, "short")

	minHoldDuration, guardStrategy, guardErr := at.applyOpenGuard(decision, marketData, "short")
	if guardErr != nil {
		if strings.Contains(guardErr.Error(), "range guard") {
			log.Printf("  ℹ️ 区间守护提示: %v，按AI方案继续执行", guardErr)
		} else {
			return guardErr
		}
	}
	livePrice := marketData.CurrentPrice
	if price, err := at.trader.GetMarketPrice(decision.Symbol); err == nil && price > 0 {
		livePrice = price
	} else if err != nil {
		log.Printf("  ⚠️ 获取实时价格失败，使用快照价: %v", err)
	}
	if livePrice <= 0 {
		return fmt.Errorf("无法获取有效价格")
	}

	riskEval, err := at.enforceOpenRisk(decision, livePrice, "short")
	if err != nil {
		return err
	}
	actionRecord.RiskUSD = riskEval.riskUSD
	actionRecord.RiskLimitUSD = riskEval.riskLimitUSD
	actionRecord.RewardToRisk = riskEval.rewardToRisk

	if decision.PositionSizeUSD <= 0 {
		return fmt.Errorf("守护调整后仓位为0，取消开仓")
	}
	if decision.PositionSizeUSD < minOrderNotionalUSD {
		return fmt.Errorf("计划名义金额 %.2f USDT 低于交易所最小下单 %.2f USDT，取消开仓", decision.PositionSizeUSD, minOrderNotionalUSD)
	}

	// 计算数量
	quantity := decision.PositionSizeUSD / livePrice
	actionRecord.Quantity = quantity
	actionRecord.Price = livePrice

	// 开仓
	order, err := at.trader.OpenShort(decision.Symbol, quantity, decision.Leverage)
	if err != nil {
		return err
	}

	at.fillActionRecordFromOrder(actionRecord, order)

	log.Printf("  ✓ 开仓成功，订单ID: %v, 数量: %.4f", order["orderId"], quantity)

	// 记录开仓时间
	posKey := decision.Symbol + "_short"
	at.positionFirstSeenTime[posKey] = time.Now().UnixMilli()
	at.positionMinHoldUntil[posKey] = time.Now().Add(minHoldDuration)
	if guardStrategy != "" {
		at.positionGuardStrategy[posKey] = guardStrategy
	} else {
		at.positionGuardStrategy[posKey] = "trend"
	}

	if len(decision.TakeProfitTargets) > 0 {
		at.registerPositionTargets(decision, quantity, marketData.CurrentPrice, "short")
	} else {
		delete(at.positionTargets, posKey)
	}

	// 设置止损止盈
	if err := at.trader.SetStopLoss(decision.Symbol, "SHORT", quantity, decision.StopLoss); err != nil {
		log.Printf("  ⚠ 设置止损失败: %v", err)
	}
	if len(decision.TakeProfitTargets) == 0 {
		if decision.TakeProfit > 0 {
			if err := at.trader.SetTakeProfit(decision.Symbol, "SHORT", quantity, decision.TakeProfit); err != nil {
				log.Printf("  ⚠ 设置止盈失败: %v", err)
			}
		}
	} else {
		if state, ok := at.positionTargets[posKey]; ok && state != nil {
			log.Printf("  🎯 已加载 %d 个分批止盈目标，由守护层动态执行", len(state.Targets))
		}
	}

	return nil
}

// executeCloseLongWithRecord 执行平多仓并记录详细信息
func (at *AutoTrader) executeCloseLongWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 平多仓: %s", decision.Symbol)

	posKey := decision.Symbol + "_long"
	if holdUntil, ok := at.positionMinHoldUntil[posKey]; ok {
		if time.Now().Before(holdUntil) {
			remaining := holdUntil.Sub(time.Now()).Minutes()
			log.Printf("  ⚠ 执行守护: %s 多仓仍处于最小持仓窗口 (剩余 %.1f 分钟)，按AI指令提前平仓。", decision.Symbol, remaining)
		}
		if strat, ok := at.positionGuardStrategy[posKey]; ok {
			log.Printf("  ℹ️ 守护策略类型: %s", strat)
		}
	}

	livePrice, priceErr := at.trader.GetMarketPrice(decision.Symbol)
	if priceErr != nil {
		log.Printf("  ⚠️ 获取实时价格失败，尝试使用快照: %v", priceErr)
		if marketData, err := market.Get(decision.Symbol); err == nil {
			livePrice = marketData.CurrentPrice
		}
	}
	actionRecord.Price = livePrice

	// 平仓
	order, err := at.trader.CloseLong(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	at.fillActionRecordFromOrder(actionRecord, order)

	log.Printf("  ✓ 平仓成功")
	at.clearPositionState(posKey)
	return nil
}

// executeCloseShortWithRecord 执行平空仓并记录详细信息
func (at *AutoTrader) executeCloseShortWithRecord(decision *decision.Decision, actionRecord *logger.DecisionAction) error {
	log.Printf("  🔄 平空仓: %s", decision.Symbol)

	posKey := decision.Symbol + "_short"
	if holdUntil, ok := at.positionMinHoldUntil[posKey]; ok {
		if time.Now().Before(holdUntil) {
			remaining := holdUntil.Sub(time.Now()).Minutes()
			log.Printf("  ⚠ 执行守护: %s 空仓仍处于最小持仓窗口 (剩余 %.1f 分钟)，按AI指令提前平仓。", decision.Symbol, remaining)
		}
		if strat, ok := at.positionGuardStrategy[posKey]; ok {
			log.Printf("  ℹ️ 守护策略类型: %s", strat)
		}
	}

	livePrice, priceErr := at.trader.GetMarketPrice(decision.Symbol)
	if priceErr != nil {
		log.Printf("  ⚠️ 获取实时价格失败，尝试使用快照: %v", priceErr)
		if marketData, err := market.Get(decision.Symbol); err == nil {
			livePrice = marketData.CurrentPrice
		}
	}
	actionRecord.Price = livePrice

	// 平仓
	order, err := at.trader.CloseShort(decision.Symbol, 0) // 0 = 全部平仓
	if err != nil {
		return err
	}

	at.fillActionRecordFromOrder(actionRecord, order)

	log.Printf("  ✓ 平仓成功")
	at.clearPositionState(posKey)
	return nil
}

func extractOrderID(order map[string]interface{}) (int64, bool) {
	if order == nil {
		return 0, false
	}

	value, ok := order["orderId"]
	if !ok {
		return 0, false
	}

	switch v := value.(type) {
	case int64:
		return v, true
	case int:
		return int64(v), true
	case uint64:
		return int64(v), true
	case float64:
		return int64(v), true
	case string:
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			return id, true
		}
	}

	return 0, false
}

// GetID 获取trader ID
func (at *AutoTrader) GetID() string {
	return at.id
}

// GetName 获取trader名称
func (at *AutoTrader) GetName() string {
	return at.name
}

// GetAIModel 获取AI模型
func (at *AutoTrader) GetAIModel() string {
	return at.aiModel
}

// GetDecisionLogger 获取决策日志记录器
func (at *AutoTrader) GetDecisionLogger() *logger.DecisionLogger {
	return at.decisionLogger
}

// GetStatus 获取系统状态（用于API）
func (at *AutoTrader) GetStatus() map[string]interface{} {
	aiProvider := "DeepSeek"
	if at.config.UseQwen {
		aiProvider = "Qwen"
	}

	return map[string]interface{}{
		"trader_id":       at.id,
		"trader_name":     at.name,
		"ai_model":        at.aiModel,
		"exchange":        at.exchange,
		"is_running":      at.isRunning,
		"start_time":      at.startTime.Format(time.RFC3339),
		"runtime_minutes": int(time.Since(at.startTime).Minutes()),
		"call_count":      at.callCount,
		"initial_balance": at.initialBalance,
		"scan_interval":   at.config.ScanInterval.String(),
		"stop_until":      at.stopUntil.Format(time.RFC3339),
		"last_reset_time": at.lastResetTime.Format(time.RFC3339),
		"ai_provider":     aiProvider,
	}
}

// GetAccountInfo 获取账户信息（用于API）
func (at *AutoTrader) GetAccountInfo() (map[string]interface{}, error) {
	balance, err := at.trader.GetBalance()
	if err != nil {
		return nil, fmt.Errorf("获取余额失败: %w", err)
	}

	// 获取账户字段
	totalWalletBalance := 0.0
	totalUnrealizedProfit := 0.0
	availableBalance := 0.0

	if wallet, ok := balance["totalWalletBalance"].(float64); ok {
		totalWalletBalance = wallet
	}
	if unrealized, ok := balance["totalUnrealizedProfit"].(float64); ok {
		totalUnrealizedProfit = unrealized
	}
	if avail, ok := balance["availableBalance"].(float64); ok {
		availableBalance = avail
	}

	// Total Equity = 钱包余额 + 未实现盈亏
	totalEquity := totalWalletBalance + totalUnrealizedProfit

	// 获取持仓计算总保证金
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	totalMarginUsed := 0.0
	totalUnrealizedPnL := 0.0
	for _, pos := range positions {
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		totalUnrealizedPnL += unrealizedPnl

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}
		marginUsed := (quantity * markPrice) / float64(leverage)
		totalMarginUsed += marginUsed
	}

	totalPnL := totalEquity - at.initialBalance
	totalPnLPct := 0.0
	if at.initialBalance > 0 {
		totalPnLPct = (totalPnL / at.initialBalance) * 100
	}

	marginUsedPct := 0.0
	if totalEquity > 0 {
		marginUsedPct = (totalMarginUsed / totalEquity) * 100
	}

	return map[string]interface{}{
		// 核心字段
		"total_equity":      totalEquity,           // 账户净值 = wallet + unrealized
		"wallet_balance":    totalWalletBalance,    // 钱包余额（不含未实现盈亏）
		"unrealized_profit": totalUnrealizedProfit, // 未实现盈亏（从API）
		"available_balance": availableBalance,      // 可用余额

		// 盈亏统计
		"total_pnl":            totalPnL,           // 总盈亏 = equity - initial
		"total_pnl_pct":        totalPnLPct,        // 总盈亏百分比
		"total_unrealized_pnl": totalUnrealizedPnL, // 未实现盈亏（从持仓计算）
		"initial_balance":      at.initialBalance,  // 初始余额
		"daily_pnl":            at.dailyPnL,        // 日盈亏

		// 持仓信息
		"position_count":  len(positions),  // 持仓数量
		"margin_used":     totalMarginUsed, // 保证金占用
		"margin_used_pct": marginUsedPct,   // 保证金使用率
	}, nil
}

// GetPositions 获取持仓列表（用于API）
func (at *AutoTrader) GetPositions() ([]map[string]interface{}, error) {
	positions, err := at.trader.GetPositions()
	if err != nil {
		return nil, fmt.Errorf("获取持仓失败: %w", err)
	}

	var result []map[string]interface{}
	for _, pos := range positions {
		symbol := pos["symbol"].(string)
		side := pos["side"].(string)
		entryPrice := pos["entryPrice"].(float64)
		markPrice := pos["markPrice"].(float64)
		quantity := pos["positionAmt"].(float64)
		if quantity < 0 {
			quantity = -quantity
		}
		unrealizedPnl := pos["unRealizedProfit"].(float64)
		liquidationPrice := pos["liquidationPrice"].(float64)

		leverage := 10
		if lev, ok := pos["leverage"].(float64); ok {
			leverage = int(lev)
		}

		pnlPct := 0.0
		if side == "long" {
			pnlPct = ((markPrice - entryPrice) / entryPrice) * float64(leverage) * 100
		} else {
			pnlPct = ((entryPrice - markPrice) / entryPrice) * float64(leverage) * 100
		}

		marginUsed := (quantity * markPrice) / float64(leverage)

		result = append(result, map[string]interface{}{
			"symbol":             symbol,
			"side":               side,
			"entry_price":        entryPrice,
			"mark_price":         markPrice,
			"quantity":           quantity,
			"leverage":           leverage,
			"unrealized_pnl":     unrealizedPnl,
			"unrealized_pnl_pct": pnlPct,
			"liquidation_price":  liquidationPrice,
			"margin_used":        marginUsed,
		})
	}

	return result, nil
}

// sortDecisionsByPriority 对决策排序：先平仓，再开仓，最后hold/wait
// 这样可以避免换仓时仓位叠加超限
func sortDecisionsByPriority(decisions []decision.Decision) []decision.Decision {
	if len(decisions) <= 1 {
		return decisions
	}

	// 定义优先级
	getActionPriority := func(action string) int {
		switch action {
		case "close_long", "close_short":
			return 1 // 最高优先级：先平仓
		case "open_long", "open_short":
			return 2 // 次优先级：后开仓
		case "hold", "wait":
			return 3 // 最低优先级：观望
		default:
			return 999 // 未知动作放最后
		}
	}
	getStrategyPriority := func(hint string) int {
		switch strings.ToLower(strings.TrimSpace(hint)) {
		case "trend", "momentum":
			return 1
		case "transitional", "auto":
			return 2
		case "range":
			return 3
		case "range_developing":
			return 4
		default:
			return 5
		}
	}

	// 复制决策列表
	sorted := make([]decision.Decision, len(decisions))
	copy(sorted, decisions)

	// 按优先级排序
	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			pi := getActionPriority(sorted[i].Action)
			pj := getActionPriority(sorted[j].Action)
			if getActionPriority(sorted[i].Action) > getActionPriority(sorted[j].Action) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
				pi, pj = pj, pi
			}
			if pi == pj && pi == 2 { // 同为开仓，按策略权重
				si := getStrategyPriority(sorted[i].StrategyHint)
				sj := getStrategyPriority(sorted[j].StrategyHint)
				if si > sj {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
	}

	return sorted
}

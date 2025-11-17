package api

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"nofx/consult"
	"nofx/manager"
	"nofx/trader"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const conversationHistoryLimit = 8

// Server HTTP API服务器
type Server struct {
	router        *gin.Engine
	traderManager *manager.TraderManager
	port          int
	consultStore  *consult.Store
}

type consultationPayload struct {
	TraderID    string   `json:"trader_id"`
	Symbols     []string `json:"symbols"`
	SymbolsText string   `json:"symbols_text"`
	Leverage    int      `json:"leverage"`
	Balance     float64  `json:"balance"`
	Note        string   `json:"note"`
}

type autoModePayload struct {
	TraderID string `json:"trader_id"`
	Enabled  bool   `json:"enabled"`
}

// NewServer 创建API服务器
func NewServer(traderManager *manager.TraderManager, port int, consultStore *consult.Store) *Server {
	// 设置为Release模式（减少日志输出）
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()

	// 启用CORS
	router.Use(corsMiddleware())

	s := &Server{
		router:        router,
		traderManager: traderManager,
		port:          port,
		consultStore:  consultStore,
	}

	// 设置路由
	s.setupRoutes()

	return s
}

// corsMiddleware CORS中间件
func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}

// setupRoutes 设置路由
func (s *Server) setupRoutes() {
	// 健康检查
	s.router.Any("/health", s.handleHealth)

	// API路由组
	api := s.router.Group("/api")
	{
		// 竞赛总览
		api.GET("/competition", s.handleCompetition)

		// Trader列表
		api.GET("/traders", s.handleTraderList)

		// 指定trader的数据（使用query参数 ?trader_id=xxx）
		api.GET("/status", s.handleStatus)
		api.GET("/account", s.handleAccount)
		api.GET("/positions", s.handlePositions)
		api.GET("/decisions", s.handleDecisions)
		api.GET("/decisions/latest", s.handleLatestDecisions)
		api.GET("/statistics", s.handleStatistics)
		api.GET("/equity-history", s.handleEquityHistory)
		api.GET("/performance", s.handlePerformance)

		// 咨询模式
		consultation := api.Group("/consultation")
		{
			consultation.GET("/settings", s.handleGetConsultationSettings)
			consultation.PUT("/settings", s.handleSaveConsultationSettings)
			consultation.POST("/request", s.handleConsultationRequest)
			consultation.GET("/history", s.handleConsultationHistory)
		}

		// 自动模式控制
		api.GET("/auto-mode", s.handleGetAutoMode)
		api.POST("/auto-mode", s.handleSetAutoMode)
	}
}

// handleHealth 健康检查
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   c.Request.Context().Value("time"),
	})
}

// getTraderFromQuery 从query参数获取trader
func (s *Server) getTraderFromQuery(c *gin.Context) (*manager.TraderManager, string, error) {
	traderID := c.Query("trader_id")
	if traderID == "" {
		// 如果没有指定trader_id，返回第一个trader
		ids := s.traderManager.GetTraderIDs()
		if len(ids) == 0 {
			return nil, "", fmt.Errorf("没有可用的trader")
		}
		traderID = ids[0]
	}
	return s.traderManager, traderID, nil
}

// handleCompetition 竞赛总览（对比所有trader）
func (s *Server) handleCompetition(c *gin.Context) {
	comparison, err := s.traderManager.GetComparisonData()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取对比数据失败: %v", err),
		})
		return
	}
	c.JSON(http.StatusOK, comparison)
}

// handleTraderList trader列表
func (s *Server) handleTraderList(c *gin.Context) {
	traders := s.traderManager.GetAllTraders()
	result := make([]map[string]interface{}, 0, len(traders))

	for _, t := range traders {
		result = append(result, map[string]interface{}{
			"trader_id":   t.GetID(),
			"trader_name": t.GetName(),
			"ai_model":    t.GetAIModel(),
		})
	}

	c.JSON(http.StatusOK, result)
}

// handleStatus 系统状态
func (s *Server) handleStatus(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	status := trader.GetStatus()
	c.JSON(http.StatusOK, status)
}

// handleAccount 账户信息
func (s *Server) handleAccount(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	log.Printf("📊 收到账户信息请求 [%s]", trader.GetName())
	account, err := trader.GetAccountInfo()
	if err != nil {
		log.Printf("❌ 获取账户信息失败 [%s]: %v", trader.GetName(), err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取账户信息失败: %v", err),
		})
		return
	}

	log.Printf("✓ 返回账户信息 [%s]: 净值=%.2f, 可用=%.2f, 盈亏=%.2f (%.2f%%)",
		trader.GetName(),
		account["total_equity"],
		account["available_balance"],
		account["total_pnl"],
		account["total_pnl_pct"])
	c.JSON(http.StatusOK, account)
}

// handlePositions 持仓列表
func (s *Server) handlePositions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	positions, err := trader.GetPositions()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取持仓列表失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, positions)
}

// handleDecisions 决策日志列表
func (s *Server) handleDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取所有历史决策记录（无限制）
	records, err := trader.GetDecisionLogger().GetLatestRecords(10000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, records)
}

// handleLatestDecisions 最新决策日志（最近5条，最新的在前）
func (s *Server) handleLatestDecisions(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	records, err := trader.GetDecisionLogger().GetLatestRecords(5)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取决策日志失败: %v", err),
		})
		return
	}

	// 反转数组，让最新的在前面（用于列表显示）
	// GetLatestRecords返回的是从旧到新（用于图表），这里需要从新到旧
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}

	c.JSON(http.StatusOK, records)
}

// handleStatistics 统计信息
func (s *Server) handleStatistics(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	stats, err := trader.GetDecisionLogger().GetStatistics()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取统计信息失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleEquityHistory 收益率历史数据
func (s *Server) handleEquityHistory(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 获取尽可能多的历史数据（几天的数据）
	// 每3分钟一个周期：10000条 = 约20天的数据
	records, err := trader.GetDecisionLogger().GetLatestRecords(10000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("获取历史数据失败: %v", err),
		})
		return
	}

	// 构建收益率历史数据点
	type EquityPoint struct {
		Timestamp        string  `json:"timestamp"`
		TotalEquity      float64 `json:"total_equity"`      // 账户净值（wallet + unrealized）
		AvailableBalance float64 `json:"available_balance"` // 可用余额
		TotalPnL         float64 `json:"total_pnl"`         // 总盈亏（相对初始余额）
		TotalPnLPct      float64 `json:"total_pnl_pct"`     // 总盈亏百分比
		PositionCount    int     `json:"position_count"`    // 持仓数量
		MarginUsedPct    float64 `json:"margin_used_pct"`   // 保证金使用率
		CycleNumber      int     `json:"cycle_number"`
	}

	// 从AutoTrader获取初始余额（用于计算盈亏百分比）
	initialBalance := 0.0
	if status := trader.GetStatus(); status != nil {
		if ib, ok := status["initial_balance"].(float64); ok && ib > 0 {
			initialBalance = ib
		}
	}

	// 如果无法从status获取，且有历史记录，则从第一条记录获取
	if initialBalance == 0 && len(records) > 0 {
		// 第一条记录的equity作为初始余额
		initialBalance = records[0].AccountState.TotalBalance
	}

	// 如果还是无法获取，返回错误
	if initialBalance == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "无法获取初始余额",
		})
		return
	}

	var history []EquityPoint
	for _, record := range records {
		// TotalBalance字段实际存储的是TotalEquity
		totalEquity := record.AccountState.TotalBalance
		// TotalUnrealizedProfit字段实际存储的是TotalPnL（相对初始余额）
		totalPnL := record.AccountState.TotalUnrealizedProfit

		// 计算盈亏百分比
		totalPnLPct := 0.0
		if initialBalance > 0 {
			totalPnLPct = (totalPnL / initialBalance) * 100
		}

		history = append(history, EquityPoint{
			Timestamp:        record.Timestamp.Format("2006-01-02 15:04:05"),
			TotalEquity:      totalEquity,
			AvailableBalance: record.AccountState.AvailableBalance,
			TotalPnL:         totalPnL,
			TotalPnLPct:      totalPnLPct,
			PositionCount:    record.AccountState.PositionCount,
			MarginUsedPct:    record.AccountState.MarginUsedPct,
			CycleNumber:      record.CycleNumber,
		})
	}

	c.JSON(http.StatusOK, history)
}

// handlePerformance AI历史表现分析（用于展示AI学习和反思）
func (s *Server) handlePerformance(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trader, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// 分析最近100个周期的交易表现（避免长期持仓的交易记录丢失）
	// 假设每3分钟一个周期，100个周期 = 5小时，足够覆盖大部分交易
	performance, err := trader.GetDecisionLogger().AnalyzePerformance(100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("分析历史表现失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, performance)
}

// handleGetConsultationSettings 返回咨询模式配置
func (s *Server) handleGetConsultationSettings(c *gin.Context) {
	if s.consultStore == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "咨询模式未启用"})
		return
	}

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	traderObj, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	defaults := traderObj.GetConsultationDefaults()
	response := gin.H{
		"trader_id": traderID,
		"symbols":   defaults.Symbols,
		"leverage":  defaults.Leverage,
		"balance":   defaults.Balance,
	}

	prefs, err := s.consultStore.GetPreferences(traderID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("加载咨询配置失败: %v", err),
		})
		return
	}
	if prefs != nil {
		if len(prefs.Symbols) > 0 {
			response["symbols"] = prefs.Symbols
		}
		if prefs.Leverage > 0 {
			response["leverage"] = prefs.Leverage
		}
		if prefs.Balance > 0 {
			response["balance"] = prefs.Balance
		}
		response["updated_at"] = prefs.UpdatedAt.Format(time.RFC3339)
	}

	if record, err := s.consultStore.GetLatestRecord(traderID); err == nil {
		if record != nil && record.Result != nil {
			latest := buildConsultationResultPayload(record.TraderID, record.Result)
			latest["record_id"] = record.ID
			latest["created_at"] = record.CreatedAt.Format(time.RFC3339)
			if strings.TrimSpace(record.Note) != "" {
				latest["note"] = record.Note
			}
			response["latest_result"] = latest
		}
	} else {
		log.Printf("⚠️  加载咨询历史失败: %v", err)
	}

	c.JSON(http.StatusOK, response)
}

// handleSaveConsultationSettings 保存咨询模式配置
func (s *Server) handleSaveConsultationSettings(c *gin.Context) {
	if s.consultStore == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "咨询模式未启用"})
		return
	}

	var payload consultationPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("请求体解析失败: %v", err)})
		return
	}

	traderID := payload.TraderID
	if traderID == "" {
		traderID = c.Query("trader_id")
	}
	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trader_id 必填"})
		return
	}

	if _, err := s.traderManager.GetTrader(traderID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	symbols := mergeConsultSymbols(payload.Symbols, payload.SymbolsText)
	if len(symbols) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请至少配置一个交易对"})
		return
	}

	record, err := s.consultStore.SavePreferences(consult.Preferences{
		TraderID: traderID,
		Symbols:  symbols,
		Leverage: payload.Leverage,
		Balance:  payload.Balance,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("保存咨询配置失败: %v", err),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"trader_id":  traderID,
		"symbols":    record.Symbols,
		"leverage":   record.Leverage,
		"balance":    record.Balance,
		"updated_at": record.UpdatedAt.Format(time.RFC3339),
	})
}

// handleConsultationRequest 执行一次咨询模式AI请求
func (s *Server) handleConsultationRequest(c *gin.Context) {
	if s.consultStore == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "咨询模式未启用"})
		return
	}

	var payload consultationPayload
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&payload); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("请求体解析失败: %v", err)})
			return
		}
	}

	traderID := payload.TraderID
	if traderID == "" {
		traderID = c.Query("trader_id")
	}
	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trader_id 必填"})
		return
	}

	traderObj, err := s.traderManager.GetTrader(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	note := strings.TrimSpace(payload.Note)

	symbols := mergeConsultSymbols(payload.Symbols, payload.SymbolsText)
	leverage := payload.Leverage
	balance := payload.Balance

	if prefs, err := s.consultStore.GetPreferences(traderID); err == nil && prefs != nil {
		if len(symbols) == 0 {
			symbols = append([]string(nil), prefs.Symbols...)
		}
		if leverage <= 0 && prefs.Leverage > 0 {
			leverage = prefs.Leverage
		}
		if balance <= 0 && prefs.Balance > 0 {
			balance = prefs.Balance
		}
	} else if err != nil {
		log.Printf("⚠️  加载咨询配置失败，将使用默认值: %v", err)
	}

	defaults := traderObj.GetConsultationDefaults()
	if len(symbols) == 0 {
		symbols = append([]string(nil), defaults.Symbols...)
	}
	if leverage <= 0 {
		leverage = defaults.Leverage
	}
	if balance <= 0 {
		balance = defaults.Balance
	}

	var convoHistory []trader.ConversationTurn
	if historyRecords, err := s.consultStore.ListRecords(traderID, conversationHistoryLimit); err == nil {
		convoHistory = buildConversationHistory(historyRecords)
	} else {
		log.Printf("⚠️  读取咨询对话历史失败: %v", err)
	}

	request := trader.ConsultationRequest{
		Symbols:  symbols,
		Leverage: leverage,
		Balance:  balance,
		Note:     note,
		History:  convoHistory,
	}

	result, err := traderObj.GenerateConsultation(request)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, trader.ErrNoConsultSymbols) {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{
			"error": fmt.Sprintf("获取AI建议失败: %v", err),
		})
		return
	}

	record, err := s.consultStore.AppendRecord(traderID, result, note)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("保存咨询记录失败: %v", err),
		})
		return
	}

	response := buildConsultationResultPayload(traderID, result)
	if record != nil {
		response["record_id"] = record.ID
		response["created_at"] = record.CreatedAt.Format(time.RFC3339)
		if record.Note != "" {
			response["note"] = record.Note
		}
	} else if note != "" {
		response["note"] = note
	}
	c.JSON(http.StatusOK, response)
}

// handleConsultationHistory 返回咨询模式历史记录
func (s *Server) handleConsultationHistory(c *gin.Context) {
	if s.consultStore == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "咨询模式未启用"})
		return
	}

	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if _, err := s.traderManager.GetTrader(traderID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	records, err := s.consultStore.ListRecords(traderID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("读取咨询历史失败: %v", err),
		})
		return
	}

	history := make([]gin.H, 0, len(records))
	for _, record := range records {
		if record == nil || record.Result == nil {
			continue
		}
		entry := buildConsultationResultPayload(record.TraderID, record.Result)
		entry["record_id"] = record.ID
		entry["created_at"] = record.CreatedAt.Format(time.RFC3339)
		if strings.TrimSpace(record.Note) != "" {
			entry["note"] = record.Note
		}
		history = append(history, entry)
	}

	c.JSON(http.StatusOK, gin.H{
		"trader_id": traderID,
		"records":   history,
	})
}

func buildConsultationResultPayload(traderID string, result *trader.ConsultationResult) gin.H {
	if result == nil {
		return gin.H{
			"trader_id": traderID,
		}
	}

	timestamp := result.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now()
	}

	return gin.H{
		"trader_id": traderID,
		"timestamp": timestamp.Format(time.RFC3339),
		"symbols":   result.Symbols,
		"leverage":  result.Leverage,
		"balance":   result.Balance,
		"decisions": result.Decisions,
		"cot_trace": result.CoTTrace,
		"prompt":    result.Prompt,
	}
}

func buildConversationHistory(records []*consult.Record) []trader.ConversationTurn {
	if len(records) == 0 {
		return nil
	}

	turns := make([]trader.ConversationTurn, 0, len(records)*2)
	// records are newest first, need oldest first for conversation
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record == nil {
			continue
		}
		if note := strings.TrimSpace(record.Note); note != "" {
			turns = append(turns, trader.ConversationTurn{
				Role:      "user",
				Content:   note,
				Timestamp: record.CreatedAt,
			})
		}
		if summary := summarizeConsultationRecord(record); summary != "" {
			turns = append(turns, trader.ConversationTurn{
				Role:      "assistant",
				Content:   summary,
				Timestamp: record.CreatedAt,
			})
		}
	}

	if len(turns) == 0 {
		return nil
	}
	return turns
}

func summarizeConsultationRecord(record *consult.Record) string {
	if record == nil || record.Result == nil {
		return ""
	}
	decisions := record.Result.Decisions
	builder := strings.Builder{}
	builder.WriteString(fmt.Sprintf("AI 建议（%s，杠杆 %dx，余额 %.0f USDT）:",
		strings.Join(record.Symbols, ", "),
		record.Leverage,
		record.Balance,
	))

	if len(decisions) == 0 {
		builder.WriteString(" 暂无操作。")
		return builder.String()
	}

	limit := len(decisions)
	if limit > 3 {
		limit = 3
	}
	for i := 0; i < limit; i++ {
		decision := decisions[i]
		action := strings.ToUpper(decision.Action)
		if action == "" {
			action = "WAIT"
		}
		builder.WriteString(fmt.Sprintf("\n• %s %s", action, decision.Symbol))
		if decision.Reasoning != "" {
			builder.WriteString(fmt.Sprintf(" · %s", truncateText(decision.Reasoning, 160)))
		}
	}
	if len(decisions) > limit {
		builder.WriteString(fmt.Sprintf("\n（另有 %d 条建议）", len(decisions)-limit))
	}
	return builder.String()
}

func truncateText(input string, limit int) string {
	text := strings.TrimSpace(input)
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func mergeConsultSymbols(list []string, raw string) []string {
	combined := make([]string, 0, len(list)+8)
	combined = append(combined, list...)
	if strings.TrimSpace(raw) != "" {
		parts := strings.FieldsFunc(raw, func(r rune) bool {
			switch r {
			case ',', ';', '\n', '\r', '\t', ' ':
				return true
			default:
				return false
			}
		})
		combined = append(combined, parts...)
	}
	return consult.NormalizeSymbols(combined)
}

// handleGetAutoMode 查询自动交易状态
func (s *Server) handleGetAutoMode(c *gin.Context) {
	_, traderID, err := s.getTraderFromQuery(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	enabled, err := s.traderManager.GetAutoMode(traderID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"trader_id":         traderID,
		"auto_mode_enabled": enabled,
	})
}

// handleSetAutoMode 切换自动交易状态
func (s *Server) handleSetAutoMode(c *gin.Context) {
	var payload autoModePayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("请求体解析失败: %v", err)})
		return
	}

	traderID := payload.TraderID
	if traderID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trader_id 必填"})
		return
	}

	if err := s.traderManager.SetAutoMode(traderID, payload.Enabled); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"trader_id":         traderID,
		"auto_mode_enabled": payload.Enabled,
	})
}

// Start 启动服务器
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%d", s.port)
	log.Printf("🌐 API服务器启动在 http://localhost%s", addr)
	log.Printf("📊 API文档:")
	log.Printf("  • GET  /api/competition      - 竞赛总览（对比所有trader）")
	log.Printf("  • GET  /api/traders          - Trader列表")
	log.Printf("  • GET  /api/status?trader_id=xxx     - 指定trader的系统状态")
	log.Printf("  • GET  /api/account?trader_id=xxx    - 指定trader的账户信息")
	log.Printf("  • GET  /api/positions?trader_id=xxx  - 指定trader的持仓列表")
	log.Printf("  • GET  /api/decisions?trader_id=xxx  - 指定trader的决策日志")
	log.Printf("  • GET  /api/decisions/latest?trader_id=xxx - 指定trader的最新决策")
	log.Printf("  • GET  /api/statistics?trader_id=xxx - 指定trader的统计信息")
	log.Printf("  • GET  /api/equity-history?trader_id=xxx - 指定trader的收益率历史数据")
	log.Printf("  • GET  /api/performance?trader_id=xxx - 指定trader的AI学习表现分析")
	log.Printf("  • GET  /health               - 健康检查")
	log.Println()

	return s.router.Run(addr)
}

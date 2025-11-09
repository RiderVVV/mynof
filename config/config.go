package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/adshao/go-binance/v2/futures"
)

// EnsembleModelConfig 辅助模型配置
type EnsembleModelConfig struct {
	ID                   string  `json:"id"`
	Label                string  `json:"label,omitempty"`
	AIModel              string  `json:"ai_model,omitempty"` // 默认 custom
	CustomAPIURL         string  `json:"custom_api_url,omitempty"`
	CustomAPIKey         string  `json:"custom_api_key,omitempty"`
	CustomModelName      string  `json:"custom_model_name,omitempty"`
	CustomAPIHTTPReferer string  `json:"custom_api_http_referer,omitempty"`
	CustomAPIXTitle      string  `json:"custom_api_x_title,omitempty"`
	Weight               float64 `json:"weight,omitempty"`
	Role                 string  `json:"role,omitempty"`
	Notes                string  `json:"notes,omitempty"`
}

// EnsembleConfig 多模型集成配置
type EnsembleConfig struct {
	Mode        string                `json:"mode,omitempty"`         // majority/weighted/cascade ...
	SummaryMode string                `json:"summary_mode,omitempty"` // 简单描述模式
	Models      []EnsembleModelConfig `json:"models,omitempty"`
}

// TradingWindowConfig 限定交易时段（UTC）
type TradingWindowConfig struct {
	Enabled   bool `json:"enabled"`
	StartHour int  `json:"start_hour"` // 0-23
	EndHour   int  `json:"end_hour"`   // 0-24
}

// MajorEventConfig 重大事件窗口
type MajorEventConfig struct {
	Name     string `json:"name"`
	StartUTC string `json:"start_utc"` // RFC3339
	EndUTC   string `json:"end_utc"`   // RFC3339
}

// TraderConfig 单个trader的配置
type TraderConfig struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`               // 是否启用该trader
	AIModel    string `json:"ai_model"`              // "qwen" or "deepseek"
	PromptPath string `json:"prompt_path,omitempty"` // 自定义系统提示词路径（默认prompts/system_prompt.txt）

	// 交易平台选择（二选一）
	Exchange string `json:"exchange"` // "binance" or "hyperliquid"

	// 币安配置
	BinanceAPIKey    string `json:"binance_api_key,omitempty"`
	BinanceSecretKey string `json:"binance_secret_key,omitempty"`

	// Hyperliquid配置
	HyperliquidPrivateKey string `json:"hyperliquid_private_key,omitempty"`
	HyperliquidWalletAddr string `json:"hyperliquid_wallet_addr,omitempty"`
	HyperliquidTestnet    bool   `json:"hyperliquid_testnet,omitempty"`

	// Aster配置
	AsterUser       string `json:"aster_user,omitempty"`        // Aster主钱包地址
	AsterSigner     string `json:"aster_signer,omitempty"`      // Aster API钱包地址
	AsterPrivateKey string `json:"aster_private_key,omitempty"` // Aster API钱包私钥

	// AI配置
	QwenKey     string `json:"qwen_key,omitempty"`
	DeepSeekKey string `json:"deepseek_key,omitempty"`

	// 自定义AI API配置（支持任何OpenAI格式的API）
	CustomAPIURL         string `json:"custom_api_url,omitempty"`
	CustomAPIKey         string `json:"custom_api_key,omitempty"`
	CustomModelName      string `json:"custom_model_name,omitempty"`
	CustomAPIHTTPReferer string `json:"custom_api_http_referer,omitempty"`
	CustomAPIXTitle      string `json:"custom_api_x_title,omitempty"`

	Ensemble EnsembleConfig `json:"ensemble,omitempty"`

	FocusSymbols    []string            `json:"focus_symbols,omitempty"`
	MaxTradeRiskUSD float64             `json:"max_trade_risk_usd,omitempty"`
	TradingWindow   TradingWindowConfig `json:"trading_window,omitempty"`
	MajorEvents     []MajorEventConfig  `json:"major_events,omitempty"`

	InitialBalance       float64 `json:"initial_balance"`
	ScanIntervalMinutes  int     `json:"scan_interval_minutes"`
	GuardIntervalMinutes int     `json:"guard_interval_minutes,omitempty"`

	SimpleTrailingGuardEnabled *bool   `json:"simple_trailing_guard_enabled,omitempty"`
	SimpleTrailingFeePct       float64 `json:"simple_trailing_fee_pct,omitempty"`
	RiskReviewEnabled          *bool   `json:"risk_review_enabled,omitempty"`
	GuardrailStrict            bool    `json:"guardrail_strict,omitempty"`
	ProfitGuardAnchorPct       float64 `json:"profit_guard_anchor_pct,omitempty"`
	ProfitGuardRetainRatio     float64 `json:"profit_guard_retain_ratio,omitempty"`
	ProfitGuardMinRetainUSD    float64 `json:"profit_guard_min_retain_usd,omitempty"`

	EntryMode           string  `json:"entry_mode,omitempty"`
	EntryWorkingType    string  `json:"entry_working_type,omitempty"`
	EntryTimeoutMinutes int     `json:"entry_timeout_minutes,omitempty"`
	EntryBufferPct      float64 `json:"entry_buffer_pct,omitempty"`
	EntryPriceProtect   bool    `json:"entry_price_protect,omitempty"`
}

// LeverageConfig 杠杆配置
type LeverageConfig struct {
	BTCETHLeverage  int `json:"btc_eth_leverage"` // BTC和ETH的杠杆倍数（主账户建议5-50，子账户≤5）
	AltcoinLeverage int `json:"altcoin_leverage"` // 山寨币的杠杆倍数（主账户建议5-20，子账户≤5）
}

// Config 总配置
type Config struct {
	Traders            []TraderConfig `json:"traders"`
	UseDefaultCoins    bool           `json:"use_default_coins"` // 是否使用默认主流币种列表
	DefaultCoins       []string       `json:"default_coins"`     // 默认主流币种池
	CoinPoolAPIURL     string         `json:"coin_pool_api_url"`
	OITopAPIURL        string         `json:"oi_top_api_url"`
	APIServerPort      int            `json:"api_server_port"`
	MaxDailyLoss       float64        `json:"max_daily_loss"`
	MaxDrawdown        float64        `json:"max_drawdown"`
	StopTradingMinutes int            `json:"stop_trading_minutes"`
	Leverage           LeverageConfig `json:"leverage"` // 杠杆配置
}

// LoadConfig 从文件加载配置
func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 设置默认值：如果use_default_coins未设置（为false）且没有配置coin_pool_api_url，则默认使用默认币种列表
	if !config.UseDefaultCoins && config.CoinPoolAPIURL == "" {
		config.UseDefaultCoins = true
	}

	// 设置默认币种池
	if len(config.DefaultCoins) == 0 {
		config.DefaultCoins = []string{
			"BTCUSDT",
			"ETHUSDT",
			"SOLUSDT",
			"BNBUSDT",
			"XRPUSDT",
			"DOGEUSDT",
			"ADAUSDT",
			"HYPEUSDT",
		}
	}

	// 验证配置
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("配置验证失败: %w", err)
	}

	return &config, nil
}

// Validate 验证配置有效性
func (c *Config) Validate() error {
	if len(c.Traders) == 0 {
		return fmt.Errorf("至少需要配置一个trader")
	}

	traderIDs := make(map[string]bool)
	for i, trader := range c.Traders {
		if trader.ID == "" {
			return fmt.Errorf("trader[%d]: ID不能为空", i)
		}
		if traderIDs[trader.ID] {
			return fmt.Errorf("trader[%d]: ID '%s' 重复", i, trader.ID)
		}
		traderIDs[trader.ID] = true

		if trader.Name == "" {
			return fmt.Errorf("trader[%d]: Name不能为空", i)
		}
		if trader.AIModel != "qwen" && trader.AIModel != "deepseek" && trader.AIModel != "custom" {
			return fmt.Errorf("trader[%d]: ai_model必须是 'qwen', 'deepseek' 或 'custom'", i)
		}

		// 验证交易平台配置
		if trader.Exchange == "" {
			trader.Exchange = "binance" // 默认使用币安
		}
		if trader.Exchange != "binance" && trader.Exchange != "hyperliquid" && trader.Exchange != "aster" {
			return fmt.Errorf("trader[%d]: exchange必须是 'binance', 'hyperliquid' 或 'aster'", i)
		}

		// 根据平台验证对应的密钥
		if trader.Exchange == "binance" {
			if trader.BinanceAPIKey == "" || trader.BinanceSecretKey == "" {
				return fmt.Errorf("trader[%d]: 使用币安时必须配置binance_api_key和binance_secret_key", i)
			}
		} else if trader.Exchange == "hyperliquid" {
			if trader.HyperliquidPrivateKey == "" {
				return fmt.Errorf("trader[%d]: 使用Hyperliquid时必须配置hyperliquid_private_key", i)
			}
		} else if trader.Exchange == "aster" {
			if trader.AsterUser == "" || trader.AsterSigner == "" || trader.AsterPrivateKey == "" {
				return fmt.Errorf("trader[%d]: 使用Aster时必须配置aster_user, aster_signer和aster_private_key", i)
			}
		}

		if trader.AIModel == "qwen" && trader.QwenKey == "" {
			return fmt.Errorf("trader[%d]: 使用Qwen时必须配置qwen_key", i)
		}
		if trader.AIModel == "deepseek" && trader.DeepSeekKey == "" {
			return fmt.Errorf("trader[%d]: 使用DeepSeek时必须配置deepseek_key", i)
		}
		if trader.AIModel == "custom" {
			if trader.CustomAPIURL == "" {
				return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_api_url", i)
			}
			if trader.CustomAPIKey == "" {
				return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_api_key", i)
			}
			if trader.CustomModelName == "" {
				return fmt.Errorf("trader[%d]: 使用自定义API时必须配置custom_model_name", i)
			}
			if strings.Contains(trader.CustomAPIURL, "openrouter.ai") {
				if trader.CustomAPIHTTPReferer == "" && trader.CustomAPIXTitle == "" {
					return fmt.Errorf("trader[%d]: 使用OpenRouter时必须至少配置custom_api_http_referer或custom_api_x_title", i)
				}
			}
		}
		if trader.MaxTradeRiskUSD < 0 {
			return fmt.Errorf("trader[%d]: max_trade_risk_usd 不能为负", i)
		}

		entryMode := strings.ToLower(strings.TrimSpace(trader.EntryMode))
		if entryMode == "" {
			entryMode = "market"
		}
		if entryMode != "market" && entryMode != "conditional" {
			return fmt.Errorf("trader[%d]: entry_mode 仅支持 'market' 或 'conditional'", i)
		}
		if entryMode == "conditional" && trader.Exchange != "binance" {
			return fmt.Errorf("trader[%d]: entry_mode=conditional 仅支持币安交易所", i)
		}
		if trader.EntryTimeoutMinutes < 0 {
			return fmt.Errorf("trader[%d]: entry_timeout_minutes 不能为负", i)
		}
		if trader.EntryBufferPct < 0 {
			return fmt.Errorf("trader[%d]: entry_buffer_pct 不能为负", i)
		}
		if trader.EntryWorkingType != "" {
			wt := strings.ToUpper(strings.TrimSpace(trader.EntryWorkingType))
			if wt != string(futures.WorkingTypeContractPrice) && wt != string(futures.WorkingTypeMarkPrice) {
				return fmt.Errorf("trader[%d]: entry_working_type 仅支持 CONTRACT_PRICE 或 MARK_PRICE", i)
			}
		}
		if trader.ProfitGuardRetainRatio != 0 && (trader.ProfitGuardRetainRatio <= 0 || trader.ProfitGuardRetainRatio >= 1) {
			return fmt.Errorf("trader[%d]: profit_guard_retain_ratio 需在 0-1 之间", i)
		}
		if trader.ProfitGuardAnchorPct < 0 {
			return fmt.Errorf("trader[%d]: profit_guard_anchor_pct 不能为负", i)
		}
		if trader.ProfitGuardMinRetainUSD < 0 {
			return fmt.Errorf("trader[%d]: profit_guard_min_retain_usd 不能为负", i)
		}
		if trader.TradingWindow.StartHour < 0 || trader.TradingWindow.StartHour > 23 {
			return fmt.Errorf("trader[%d]: trading_window.start_hour 需在0-23之间", i)
		}
		if trader.TradingWindow.EndHour < 0 || trader.TradingWindow.EndHour > 24 {
			return fmt.Errorf("trader[%d]: trading_window.end_hour 需在0-24之间", i)
		}
		if trader.TradingWindow.Enabled && trader.TradingWindow.StartHour == trader.TradingWindow.EndHour {
			return fmt.Errorf("trader[%d]: trading_window start_hour 与 end_hour 不能相同", i)
		}
		if trader.InitialBalance <= 0 {
			return fmt.Errorf("trader[%d]: initial_balance必须大于0", i)
		}
		if trader.ScanIntervalMinutes <= 0 {
			trader.ScanIntervalMinutes = 3 // 默认3分钟
		}

		if len(trader.Ensemble.Models) > 0 {
			seenModelIDs := make(map[string]bool)
			for j, model := range trader.Ensemble.Models {
				if model.ID == "" {
					return fmt.Errorf("trader[%d]: ensemble.models[%d] id不能为空", i, j)
				}
				if seenModelIDs[model.ID] {
					return fmt.Errorf("trader[%d]: ensemble.models[%d] id '%s' 重复", i, j, model.ID)
				}
				seenModelIDs[model.ID] = true

				aiModel := strings.TrimSpace(model.AIModel)
				if aiModel == "" {
					aiModel = "custom"
				}
				switch aiModel {
				case "custom":
					apiURL := model.CustomAPIURL
					apiKey := model.CustomAPIKey
					modelName := model.CustomModelName
					if apiURL == "" && trader.CustomAPIURL == "" {
						return fmt.Errorf("trader[%d]: ensemble.models[%d] 使用custom模型时必须配置custom_api_url", i, j)
					}
					if apiKey == "" && trader.CustomAPIKey == "" {
						return fmt.Errorf("trader[%d]: ensemble.models[%d] 使用custom模型时必须配置custom_api_key", i, j)
					}
					if modelName == "" && trader.CustomModelName == "" {
						return fmt.Errorf("trader[%d]: ensemble.models[%d] 使用custom模型时必须配置custom_model_name", i, j)
					}
					if strings.Contains(apiURL, "openrouter.ai") || strings.Contains(trader.CustomAPIURL, "openrouter.ai") {
						if model.CustomAPIHTTPReferer == "" && model.CustomAPIXTitle == "" &&
							trader.CustomAPIHTTPReferer == "" && trader.CustomAPIXTitle == "" {
							return fmt.Errorf("trader[%d]: ensemble.models[%d] 使用OpenRouter时必须配置custom_api_http_referer或custom_api_x_title", i, j)
						}
					}
				default:
					return fmt.Errorf("trader[%d]: ensemble.models[%d] 不支持的ai_model '%s'，目前仅支持'custom'", i, j, aiModel)
				}
				if model.Weight < 0 {
					return fmt.Errorf("trader[%d]: ensemble.models[%d] weight不能为负数", i, j)
				}
			}
		}
	}

	if c.APIServerPort <= 0 {
		c.APIServerPort = 8080 // 默认8080端口
	}

	// 设置杠杆默认值（适配币安子账户限制，最大5倍）
	if c.Leverage.BTCETHLeverage <= 0 {
		c.Leverage.BTCETHLeverage = 5 // 默认5倍（安全值，适配子账户）
	}
	if c.Leverage.BTCETHLeverage > 5 {
		fmt.Printf("⚠️  警告: BTC/ETH杠杆设置为%dx，如果使用子账户可能会失败（子账户限制≤5x）\n", c.Leverage.BTCETHLeverage)
	}
	if c.Leverage.AltcoinLeverage <= 0 {
		c.Leverage.AltcoinLeverage = 5 // 默认5倍（安全值，适配子账户）
	}
	if c.Leverage.AltcoinLeverage > 5 {
		fmt.Printf("⚠️  警告: 山寨币杠杆设置为%dx，如果使用子账户可能会失败（子账户限制≤5x）\n", c.Leverage.AltcoinLeverage)
	}

	return nil
}

// GetScanInterval 获取扫描间隔
func (tc *TraderConfig) GetScanInterval() time.Duration {
	return time.Duration(tc.ScanIntervalMinutes) * time.Minute
}

// GetGuardInterval 获取守护巡检间隔（默认1分钟）
func (tc *TraderConfig) GetGuardInterval() time.Duration {
	if tc.GuardIntervalMinutes <= 0 {
		return time.Minute
	}
	return time.Duration(tc.GuardIntervalMinutes) * time.Minute
}

// GetEntryMode 返回清洗后的下单模式
func (tc *TraderConfig) GetEntryMode() string {
	mode := strings.ToLower(strings.TrimSpace(tc.EntryMode))
	if mode == "conditional" {
		return "conditional"
	}
	return "market"
}

// GetEntryWorkingType 返回触发价使用的价格类型
func (tc *TraderConfig) GetEntryWorkingType() string {
	working := strings.ToUpper(strings.TrimSpace(tc.EntryWorkingType))
	if working == string(futures.WorkingTypeMarkPrice) {
		return string(futures.WorkingTypeMarkPrice)
	}
	return string(futures.WorkingTypeContractPrice)
}

// GetEntryTimeout 返回条件单默认超时时间
func (tc *TraderConfig) GetEntryTimeout() time.Duration {
	minutes := tc.EntryTimeoutMinutes
	if minutes <= 0 {
		minutes = 30
	}
	return time.Duration(minutes) * time.Minute
}

// GetEntryBufferPct 返回条件单触发价缓冲
func (tc *TraderConfig) GetEntryBufferPct() float64 {
	if tc.EntryBufferPct < 0 {
		return 0
	}
	return tc.EntryBufferPct
}

// EntryPriceProtectionEnabled 是否启用触发价保护
func (tc *TraderConfig) EntryPriceProtectionEnabled() bool {
	return tc.EntryPriceProtect
}

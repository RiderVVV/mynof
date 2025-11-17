package trader

import (
	"errors"
	"log"
	"math"
	"nofx/decision"
	"nofx/market"
	"strings"
	"time"
)

const (
	maxConsultSymbols  = 20
	maxConsultLeverage = 125
	minConsultBalance  = 100
	defaultConsultBal  = 1000
)

var (
	// ErrNoConsultSymbols indicates that no tradable symbols were provided or resolved.
	ErrNoConsultSymbols = errors.New("缺少可分析的交易对")
)

// ConsultationDefaults exposes sane fallback values for the UI.
type ConsultationDefaults struct {
	Symbols  []string
	Leverage int
	Balance  float64
}

// ConsultationRequest contains the inputs coming from the consultation UI.
type ConsultationRequest struct {
	Symbols  []string `json:"symbols"`
	Leverage int      `json:"leverage"`
	Balance  float64  `json:"balance"`
}

// ConsultationResult is a simplified view of the AI response.
type ConsultationResult struct {
	Timestamp time.Time           `json:"timestamp"`
	Symbols   []string            `json:"symbols"`
	Leverage  int                 `json:"leverage"`
	Balance   float64             `json:"balance"`
	Decisions []decision.Decision `json:"decisions"`
	CoTTrace  string              `json:"cot_trace"`
	Prompt    string              `json:"prompt"`
}

// GetConsultationDefaults returns default inputs for consult-only mode.
func (at *AutoTrader) GetConsultationDefaults() ConsultationDefaults {
	symbols := sanitizeConsultSymbols(at.focusSymbols)
	if len(symbols) == 0 {
		symbols = []string{"BTCUSDT", "ETHUSDT", "SOLUSDT"}
	}

	leverage := at.config.AltcoinLeverage
	if leverage <= 0 {
		leverage = at.config.BTCETHLeverage
	}
	if leverage <= 0 {
		leverage = 5
	}

	balance := at.initialBalance
	if balance <= 0 {
		balance = defaultConsultBal
	}

	return ConsultationDefaults{
		Symbols:  symbols,
		Leverage: leverage,
		Balance:  balance,
	}
}

// GenerateConsultation executes the decision pipeline without creating orders.
func (at *AutoTrader) GenerateConsultation(req ConsultationRequest) (*ConsultationResult, error) {
	defaults := at.GetConsultationDefaults()
	symbols := sanitizeConsultSymbols(req.Symbols)
	if len(symbols) == 0 {
		symbols = append([]string(nil), defaults.Symbols...)
	}
	if len(symbols) > maxConsultSymbols {
		symbols = symbols[:maxConsultSymbols]
	}

	leverage := req.Leverage
	if leverage <= 0 {
		leverage = defaults.Leverage
	}
	leverage = clamp(leverage, 1, maxConsultLeverage)

	balance := req.Balance
	if balance <= 0 {
		balance = defaults.Balance
	}
	if balance < minConsultBalance {
		balance = minConsultBalance
	}

	if len(symbols) == 0 {
		return nil, ErrNoConsultSymbols
	}

	log.Printf("📨 [%s] 咨询模式请求：交易对=%v | 杠杆=%dx | 参考余额=%.2f", at.name, symbols, leverage, balance)

	ctx := &decision.Context{
		CurrentTime:    time.Now().Format("2006-01-02 15:04:05"),
		RuntimeMinutes: int(time.Since(at.startTime).Minutes()),
		CallCount:      at.callCount,
		Account: decision.AccountInfo{
			TotalEquity:      balance,
			AvailableBalance: balance,
			TotalPnL:         0,
			TotalPnLPct:      0,
			MarginUsed:       0,
			MarginUsedPct:    0,
			PositionCount:    0,
		},
		Positions:        []decision.PositionInfo{},
		CandidateCoins:   buildConsultCandidates(symbols),
		BTCETHLeverage:   leverage,
		AltcoinLeverage:  leverage,
		SystemPromptPath: at.config.SystemPromptPath,
		PendingEntries:   nil,
	}

	fullDecision, err := decision.GetFullDecision(ctx, at.mcpClient)
	if err != nil {
		return nil, err
	}

	return &ConsultationResult{
		Timestamp: fullDecision.Timestamp,
		Symbols:   symbols,
		Leverage:  leverage,
		Balance:   balance,
		Decisions: fullDecision.Decisions,
		CoTTrace:  fullDecision.CoTTrace,
		Prompt:    fullDecision.UserPrompt,
	}, nil
}

func sanitizeConsultSymbols(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, raw := range input {
		norm := strings.TrimSpace(raw)
		if norm == "" {
			continue
		}
		norm = market.Normalize(norm)
		if norm == "" || norm == "USDT" {
			continue
		}
		if _, exists := seen[norm]; exists {
			continue
		}
		seen[norm] = struct{}{}
		result = append(result, norm)
		if len(result) >= maxConsultSymbols {
			break
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func buildConsultCandidates(symbols []string) []decision.CandidateCoin {
	result := make([]decision.CandidateCoin, 0, len(symbols))
	for _, sym := range symbols {
		result = append(result, decision.CandidateCoin{
			Symbol:  sym,
			Sources: []string{"user"},
		})
	}
	return result
}

func clamp(v, minVal, maxVal int) int {
	return int(math.Max(float64(minVal), math.Min(float64(maxVal), float64(v))))
}

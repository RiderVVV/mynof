package trader

import "errors"

// Trader 交易器统一接口
// 支持多个交易平台（币安、Hyperliquid等）
type Trader interface {
	// GetBalance 获取账户余额
	GetBalance() (map[string]interface{}, error)

	// GetPositions 获取所有持仓
	GetPositions() ([]map[string]interface{}, error)

	// OpenLong 开多仓
	OpenLong(symbol string, quantity float64, leverage int) (map[string]interface{}, error)

	// OpenShort 开空仓
	OpenShort(symbol string, quantity float64, leverage int) (map[string]interface{}, error)

	// CloseLong 平多仓（quantity=0表示全部平仓）
	CloseLong(symbol string, quantity float64) (map[string]interface{}, error)

	// CloseShort 平空仓（quantity=0表示全部平仓）
	CloseShort(symbol string, quantity float64) (map[string]interface{}, error)

	// SetLeverage 设置杠杆
	SetLeverage(symbol string, leverage int) error

	// GetMarketPrice 获取市场价格
	GetMarketPrice(symbol string) (float64, error)

	// SetStopLoss 设置止损单
	SetStopLoss(symbol string, positionSide string, quantity, stopPrice float64) error

	// SetTakeProfit 设置止盈单
	SetTakeProfit(symbol string, positionSide string, quantity, takeProfitPrice float64) error

	// CancelAllOrders 取消该币种的所有挂单
	CancelAllOrders(symbol string) error

	// FormatQuantity 格式化数量到正确的精度
	FormatQuantity(symbol string, quantity float64) (string, error)

	// PlaceConditionalOrder 使用交易所条件单接口（若支持）
	PlaceConditionalOrder(req *ConditionalOrderRequest) (*ConditionalOrderResponse, error)

	// QueryConditionalOrder 查询指定条件单状态
	QueryConditionalOrder(algoID int64, clientAlgoID string) (*ConditionalOrderResponse, error)

	// CancelConditionalOrder 取消指定条件单
	CancelConditionalOrder(algoID int64, clientAlgoID string) error

	// CancelAllConditionalOrders 取消某交易对下所有条件单
	CancelAllConditionalOrders(symbol string) error

	// ListOpenConditionalOrders 查询当前所有未触发的条件单（用于重启恢复）
	ListOpenConditionalOrders(symbol string) ([]*ConditionalOrderResponse, error)

	// GetSymbolTickSize 返回该交易对的最小价格步长（若不支持则返回0）
	GetSymbolTickSize(symbol string) (float64, error)
}

// ErrConditionalOrdersUnsupported 表示交易器未实现条件单接口
var ErrConditionalOrdersUnsupported = errors.New("conditional orders are not supported by this trader")

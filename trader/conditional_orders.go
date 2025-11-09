package trader

import "github.com/adshao/go-binance/v2/futures"

// ConditionalOrderRequest 封装币安条件单下单参数
type ConditionalOrderRequest struct {
	Symbol          string
	Side            futures.SideType
	PositionSide    futures.PositionSideType
	OrderType       futures.OrderType
	TimeInForce     futures.TimeInForceType
	Quantity        string
	Price           string
	TriggerPrice    string
	WorkingType     futures.WorkingType
	PriceMatch      futures.PriceMatchType
	ClientAlgoID    string
	PriceProtect    bool
	ReduceOnly      bool
	ClosePosition   bool
	ActivationPrice string
	CallbackRate    string
}

// ConditionalOrderResponse 公共响应结构
type ConditionalOrderResponse struct {
	AlgoID          int64  `json:"algoId"`
	ClientAlgoID    string `json:"clientAlgoId"`
	AlgoType        string `json:"algoType"`
	OrderType       string `json:"orderType"`
	Symbol          string `json:"symbol"`
	Side            string `json:"side"`
	PositionSide    string `json:"positionSide"`
	TimeInForce     string `json:"timeInForce"`
	Quantity        string `json:"quantity"`
	Price           string `json:"price"`
	TriggerPrice    string `json:"triggerPrice"`
	WorkingType     string `json:"workingType"`
	PriceMatch      string `json:"priceMatch"`
	AlgoStatus      string `json:"algoStatus"`
	TriggerStatus   string `json:"triggerStatus"`
	ActivationPrice string `json:"activationPrice"`
	CallbackRate    string `json:"callbackRate"`
	UpdateTime      int64  `json:"updateTime"`
	ErrorCode       int    `json:"code"`
	Message         string `json:"msg"`
}

package market

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Data 市场数据结构
type Data struct {
	Symbol            string
	CurrentPrice      float64
	PriceChange15m    float64 // 15分钟价格变化百分比
	PriceChange1h     float64 // 1小时价格变化百分比
	PriceChange4h     float64 // 4小时价格变化百分比
	CurrentEMA20      float64
	CurrentMACD       float64
	CurrentRSI7       float64
	OpenInterest      *OIData
	FundingRate       float64
	IntradaySeries    *IntradayData
	MidTermContext    *TimeframeSnapshot
	HourlyContext     *TimeframeSnapshot
	LongerTermContext *LongerTermData
	RangeState        *RangeState
}

// OIData Open Interest数据
type OIData struct {
	Latest  float64
	Average float64
}

// IntradayData 日内数据(3分钟间隔)
type IntradayData struct {
	MidPrices   []float64
	EMA20Values []float64
	MACDValues  []float64
	RSI7Values  []float64
	RSI14Values []float64
}

// TimeframeSnapshot 中短周期数据快照
type TimeframeSnapshot struct {
	EMA20        float64
	EMA50        float64
	MACD         float64
	RSI14        float64
	CloseSeries  []float64
	VolumeSeries []float64
	MACDSeries   []float64
	RSI14Series  []float64
}

// LongerTermData 长期数据(4小时时间框架)
type LongerTermData struct {
	EMA20         float64
	EMA50         float64
	ATR3          float64
	ATR14         float64
	CurrentVolume float64
	AverageVolume float64
	MACDValues    []float64
	RSI14Values   []float64
}

// RangeState 表示某个时间框架下的震荡区间信息
type RangeState struct {
	Timeframe         string
	LookbackCandles   int
	LookbackMinutes   int
	High              float64
	Low               float64
	Mid               float64
	Width             float64
	WidthPct          float64
	ATR14             float64
	WidthToATR14      float64
	VWAP              float64
	TouchesHigh       int
	TouchesLow        int
	DistanceToHighPct float64
	DistanceToLowPct  float64
	PriceLocation     string
	Regime            string
}

// Kline K线数据
type Kline struct {
	OpenTime  int64
	Open      float64
	High      float64
	Low       float64
	Close     float64
	Volume    float64
	CloseTime int64
}

// Get 获取指定代币的市场数据
func Get(symbol string) (*Data, error) {
	// 标准化symbol
	symbol = Normalize(symbol)

	// 获取3分钟K线数据 (最近80个)
	klines3m, err := getKlines(symbol, "3m", 80) // 多获取一些用于计算
	if err != nil {
		return nil, fmt.Errorf("获取3分钟K线失败: %v", err)
	}

	// 获取15分钟K线数据
	klines15m, err := getKlines(symbol, "15m", 120)
	if err != nil {
		return nil, fmt.Errorf("获取15分钟K线失败: %v", err)
	}

	// 获取1小时K线数据
	klines1h, err := getKlines(symbol, "1h", 120)
	if err != nil {
		return nil, fmt.Errorf("获取1小时K线失败: %v", err)
	}

	// 获取4小时K线数据 (最近10个)
	klines4h, err := getKlines(symbol, "4h", 60) // 多获取用于计算指标
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 计算当前指标 (基于3分钟最新数据)
	currentPrice := klines3m[len(klines3m)-1].Close
	currentEMA20 := calculateEMA(klines3m, 20)
	currentMACD := calculateMACD(klines3m)
	currentRSI7 := calculateRSI(klines3m, 7)

	// 计算价格变化百分比
	// 15分钟价格变化
	priceChange15m := 0.0
	if len(klines15m) >= 2 {
		price15mAgo := klines15m[len(klines15m)-2].Close
		current15m := klines15m[len(klines15m)-1].Close
		if price15mAgo > 0 {
			priceChange15m = ((current15m - price15mAgo) / price15mAgo) * 100
		}
	}

	// 1小时价格变化
	priceChange1h := 0.0
	if len(klines1h) >= 2 {
		price1hAgo := klines1h[len(klines1h)-2].Close
		current1h := klines1h[len(klines1h)-1].Close
		if price1hAgo > 0 {
			priceChange1h = ((current1h - price1hAgo) / price1hAgo) * 100
		}
	}

	// 4小时价格变化 = 1个4小时K线前的价格
	priceChange4h := 0.0
	if len(klines4h) >= 2 {
		price4hAgo := klines4h[len(klines4h)-2].Close
		if price4hAgo > 0 {
			priceChange4h = ((currentPrice - price4hAgo) / price4hAgo) * 100
		}
	}

	// 获取OI数据
	oiData, err := getOpenInterestData(symbol)
	if err != nil {
		// OI失败不影响整体,使用默认值
		oiData = &OIData{Latest: 0, Average: 0}
	}

	// 获取Funding Rate
	fundingRate, _ := getFundingRate(symbol)

	// 计算日内及多周期系列数据
	intradayData := calculateIntradaySeries(klines3m)
	midTermData := calculateTimeframeSnapshot(klines15m)
	hourlyData := calculateTimeframeSnapshot(klines1h)

	// 计算长期数据
	longerTermData := calculateLongerTermData(klines4h)
	atr15m := calculateATR(klines15m, 14)
	rangeState := calculateRangeState(klines15m, atr15m, currentPrice)

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange15m:    priceChange15m,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		CurrentEMA20:      currentEMA20,
		CurrentMACD:       currentMACD,
		CurrentRSI7:       currentRSI7,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		IntradaySeries:    intradayData,
		MidTermContext:    midTermData,
		HourlyContext:     hourlyData,
		LongerTermContext: longerTermData,
		RangeState:        rangeState,
	}, nil
}

// getKlines 从Binance获取K线数据
func getKlines(symbol, interval string, limit int) ([]Kline, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/klines?symbol=%s&interval=%s&limit=%d",
		symbol, interval, limit)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rawData [][]interface{}
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, err
	}

	klines := make([]Kline, len(rawData))
	for i, item := range rawData {
		openTime := int64(item[0].(float64))
		open, _ := parseFloat(item[1])
		high, _ := parseFloat(item[2])
		low, _ := parseFloat(item[3])
		close, _ := parseFloat(item[4])
		volume, _ := parseFloat(item[5])
		closeTime := int64(item[6].(float64))

		klines[i] = Kline{
			OpenTime:  openTime,
			Open:      open,
			High:      high,
			Low:       low,
			Close:     close,
			Volume:    volume,
			CloseTime: closeTime,
		}
	}

	return klines, nil
}

// calculateEMA 计算EMA
func calculateEMA(klines []Kline, period int) float64 {
	if len(klines) < period {
		return 0
	}

	// 计算SMA作为初始EMA
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += klines[i].Close
	}
	ema := sum / float64(period)

	// 计算EMA
	multiplier := 2.0 / float64(period+1)
	for i := period; i < len(klines); i++ {
		ema = (klines[i].Close-ema)*multiplier + ema
	}

	return ema
}

// calculateMACD 计算MACD
func calculateMACD(klines []Kline) float64 {
	if len(klines) < 26 {
		return 0
	}

	// 计算12期和26期EMA
	ema12 := calculateEMA(klines, 12)
	ema26 := calculateEMA(klines, 26)

	// MACD = EMA12 - EMA26
	return ema12 - ema26
}

// calculateRSI 计算RSI
func calculateRSI(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	gains := 0.0
	losses := 0.0

	// 计算初始平均涨跌幅
	for i := 1; i <= period; i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	// 使用Wilder平滑方法计算后续RSI
	for i := period + 1; i < len(klines); i++ {
		change := klines[i].Close - klines[i-1].Close
		if change > 0 {
			avgGain = (avgGain*float64(period-1) + change) / float64(period)
			avgLoss = (avgLoss * float64(period-1)) / float64(period)
		} else {
			avgGain = (avgGain * float64(period-1)) / float64(period)
			avgLoss = (avgLoss*float64(period-1) + (-change)) / float64(period)
		}
	}

	if avgLoss == 0 {
		return 100
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	return rsi
}

// calculateATR 计算ATR
func calculateATR(klines []Kline, period int) float64 {
	if len(klines) <= period {
		return 0
	}

	trs := make([]float64, len(klines))
	for i := 1; i < len(klines); i++ {
		high := klines[i].High
		low := klines[i].Low
		prevClose := klines[i-1].Close

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trs[i] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// 计算初始ATR
	sum := 0.0
	for i := 1; i <= period; i++ {
		sum += trs[i]
	}
	atr := sum / float64(period)

	// Wilder平滑
	for i := period + 1; i < len(klines); i++ {
		atr = (atr*float64(period-1) + trs[i]) / float64(period)
	}

	return atr
}

// calculateIntradaySeries 计算日内系列数据
func calculateIntradaySeries(klines []Kline) *IntradayData {
	data := &IntradayData{
		MidPrices:   make([]float64, 0, 10),
		EMA20Values: make([]float64, 0, 10),
		MACDValues:  make([]float64, 0, 10),
		RSI7Values:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	// 获取最近10个数据点
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		data.MidPrices = append(data.MidPrices, klines[i].Close)

		// 计算每个点的EMA20
		if i >= 19 {
			ema20 := calculateEMA(klines[:i+1], 20)
			data.EMA20Values = append(data.EMA20Values, ema20)
		}

		// 计算每个点的MACD
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}

		// 计算每个点的RSI
		if i >= 7 {
			rsi7 := calculateRSI(klines[:i+1], 7)
			data.RSI7Values = append(data.RSI7Values, rsi7)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// calculateTimeframeSnapshot 计算15分钟/1小时等周期的关键指标
func calculateTimeframeSnapshot(klines []Kline) *TimeframeSnapshot {
	if len(klines) == 0 {
		return nil
	}

	snapshot := &TimeframeSnapshot{
		CloseSeries:  extractRecentSeries(klines, func(k Kline) float64 { return k.Close }, 10),
		VolumeSeries: extractRecentSeries(klines, func(k Kline) float64 { return k.Volume }, 10),
	}

	if len(klines) >= 20 {
		snapshot.EMA20 = calculateEMA(klines, 20)
	}
	if len(klines) >= 50 {
		snapshot.EMA50 = calculateEMA(klines, 50)
	}
	if len(klines) >= 26 {
		snapshot.MACD = calculateMACD(klines)
		snapshot.MACDSeries = calculateMACDSeries(klines, 10)
	}
	if len(klines) > 14 {
		snapshot.RSI14 = calculateRSI(klines, 14)
		snapshot.RSI14Series = calculateRSISeries(klines, 14, 10)
	}

	return snapshot
}

// calculateLongerTermData 计算长期数据
func calculateLongerTermData(klines []Kline) *LongerTermData {
	data := &LongerTermData{
		MACDValues:  make([]float64, 0, 10),
		RSI14Values: make([]float64, 0, 10),
	}

	// 计算EMA
	data.EMA20 = calculateEMA(klines, 20)
	data.EMA50 = calculateEMA(klines, 50)

	// 计算ATR
	data.ATR3 = calculateATR(klines, 3)
	data.ATR14 = calculateATR(klines, 14)

	// 计算成交量
	if len(klines) > 0 {
		data.CurrentVolume = klines[len(klines)-1].Volume
		// 计算平均成交量
		sum := 0.0
		for _, k := range klines {
			sum += k.Volume
		}
		data.AverageVolume = sum / float64(len(klines))
	}

	// 计算MACD和RSI序列
	start := len(klines) - 10
	if start < 0 {
		start = 0
	}

	for i := start; i < len(klines); i++ {
		if i >= 25 {
			macd := calculateMACD(klines[:i+1])
			data.MACDValues = append(data.MACDValues, macd)
		}
		if i >= 14 {
			rsi14 := calculateRSI(klines[:i+1], 14)
			data.RSI14Values = append(data.RSI14Values, rsi14)
		}
	}

	return data
}

// getOpenInterestData 获取OI数据
func getOpenInterestData(symbol string) (*OIData, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/openInterest?symbol=%s", symbol)

	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		OpenInterest string `json:"openInterest"`
		Symbol       string `json:"symbol"`
		Time         int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	oi, _ := strconv.ParseFloat(result.OpenInterest, 64)

	return &OIData{
		Latest:  oi,
		Average: oi * 0.999, // 近似平均值
	}, nil
}

// getFundingRate 获取资金费率
func getFundingRate(symbol string) (float64, error) {
	url := fmt.Sprintf("https://fapi.binance.com/fapi/v1/premiumIndex?symbol=%s", symbol)

	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}

	var result struct {
		Symbol          string `json:"symbol"`
		MarkPrice       string `json:"markPrice"`
		IndexPrice      string `json:"indexPrice"`
		LastFundingRate string `json:"lastFundingRate"`
		NextFundingTime int64  `json:"nextFundingTime"`
		InterestRate    string `json:"interestRate"`
		Time            int64  `json:"time"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}

	rate, _ := strconv.ParseFloat(result.LastFundingRate, 64)
	return rate, nil
}

// calculateMACDSeries 计算最近若干点的MACD序列
func calculateMACDSeries(klines []Kline, count int) []float64 {
	if count <= 0 || len(klines) < 26 {
		return nil
	}

	start := len(klines) - count
	if start < 25 {
		start = 25
	}

	values := make([]float64, 0, len(klines)-start)
	for i := start; i < len(klines); i++ {
		values = append(values, calculateMACD(klines[:i+1]))
	}

	return values
}

// calculateRSISeries 计算最近若干点的RSI序列
func calculateRSISeries(klines []Kline, period, count int) []float64 {
	if count <= 0 || len(klines) <= period {
		return nil
	}

	start := len(klines) - count
	if start < period {
		start = period
	}

	values := make([]float64, 0, len(klines)-start)
	for i := start; i < len(klines); i++ {
		values = append(values, calculateRSI(klines[:i+1], period))
	}

	return values
}

// extractRecentSeries 获取最近count个数据点
func extractRecentSeries(klines []Kline, selector func(Kline) float64, count int) []float64 {
	if count <= 0 || len(klines) == 0 {
		return nil
	}

	start := len(klines) - count
	if start < 0 {
		start = 0
	}

	result := make([]float64, 0, len(klines)-start)
	for i := start; i < len(klines); i++ {
		result = append(result, selector(klines[i]))
	}

	return result
}

// Format 格式化输出市场数据
func Format(data *Data) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("current_price = %.2f, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period) = %.3f\n\n",
		data.CurrentPrice, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))
	sb.WriteString(fmt.Sprintf("price_change (15m/1h/4h) = %+.2f%% / %+.2f%% / %+.2f%%\n\n",
		data.PriceChange15m, data.PriceChange1h, data.PriceChange4h))

	sb.WriteString(fmt.Sprintf("In addition, here is the latest %s open interest and funding rate for perps:\n\n",
		data.Symbol))

	if data.OpenInterest != nil {
		sb.WriteString(fmt.Sprintf("Open Interest: Latest: %.2f Average: %.2f\n\n",
			data.OpenInterest.Latest, data.OpenInterest.Average))
	}

	sb.WriteString(fmt.Sprintf("Funding Rate: %.2e\n\n", data.FundingRate))

	if data.IntradaySeries != nil {
		sb.WriteString("Intraday series (3‑minute intervals, oldest → latest):\n\n")

		if len(data.IntradaySeries.MidPrices) > 0 {
			sb.WriteString(fmt.Sprintf("Mid prices: %s\n\n", formatFloatSlice(data.IntradaySeries.MidPrices)))
		}

		if len(data.IntradaySeries.EMA20Values) > 0 {
			sb.WriteString(fmt.Sprintf("EMA indicators (20‑period): %s\n\n", formatFloatSlice(data.IntradaySeries.EMA20Values)))
		}

		if len(data.IntradaySeries.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.IntradaySeries.MACDValues)))
		}

		if len(data.IntradaySeries.RSI7Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (7‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI7Values)))
		}

		if len(data.IntradaySeries.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.IntradaySeries.RSI14Values)))
		}
	}

	if data.MidTermContext != nil {
		sb.WriteString("Mid-term context (15-minute timeframe):\n\n")
		sb.WriteString(fmt.Sprintf("EMA20: %.3f vs. EMA50: %.3f | MACD: %.3f | RSI14: %.3f\n\n",
			data.MidTermContext.EMA20, data.MidTermContext.EMA50, data.MidTermContext.MACD, data.MidTermContext.RSI14))

		if len(data.MidTermContext.CloseSeries) > 0 {
			sb.WriteString(fmt.Sprintf("Close series: %s\n\n", formatFloatSlice(data.MidTermContext.CloseSeries)))
		}
		if len(data.MidTermContext.VolumeSeries) > 0 {
			sb.WriteString(fmt.Sprintf("Volume series: %s\n\n", formatFloatSlice(data.MidTermContext.VolumeSeries)))
		}
		if len(data.MidTermContext.MACDSeries) > 0 {
			sb.WriteString(fmt.Sprintf("MACD series: %s\n\n", formatFloatSlice(data.MidTermContext.MACDSeries)))
		}
		if len(data.MidTermContext.RSI14Series) > 0 {
			sb.WriteString(fmt.Sprintf("RSI14 series: %s\n\n", formatFloatSlice(data.MidTermContext.RSI14Series)))
		}
	}

	if data.HourlyContext != nil {
		sb.WriteString("Hourly context (1-hour timeframe):\n\n")
		sb.WriteString(fmt.Sprintf("EMA20: %.3f vs. EMA50: %.3f | MACD: %.3f | RSI14: %.3f\n\n",
			data.HourlyContext.EMA20, data.HourlyContext.EMA50, data.HourlyContext.MACD, data.HourlyContext.RSI14))

		if len(data.HourlyContext.CloseSeries) > 0 {
			sb.WriteString(fmt.Sprintf("Close series: %s\n\n", formatFloatSlice(data.HourlyContext.CloseSeries)))
		}
		if len(data.HourlyContext.VolumeSeries) > 0 {
			sb.WriteString(fmt.Sprintf("Volume series: %s\n\n", formatFloatSlice(data.HourlyContext.VolumeSeries)))
		}
		if len(data.HourlyContext.MACDSeries) > 0 {
			sb.WriteString(fmt.Sprintf("MACD series: %s\n\n", formatFloatSlice(data.HourlyContext.MACDSeries)))
		}
		if len(data.HourlyContext.RSI14Series) > 0 {
			sb.WriteString(fmt.Sprintf("RSI14 series: %s\n\n", formatFloatSlice(data.HourlyContext.RSI14Series)))
		}
	}

	if data.LongerTermContext != nil {
		sb.WriteString("Longer‑term context (4‑hour timeframe):\n\n")

		sb.WriteString(fmt.Sprintf("20‑Period EMA: %.3f vs. 50‑Period EMA: %.3f\n\n",
			data.LongerTermContext.EMA20, data.LongerTermContext.EMA50))

		sb.WriteString(fmt.Sprintf("3‑Period ATR: %.3f vs. 14‑Period ATR: %.3f\n\n",
			data.LongerTermContext.ATR3, data.LongerTermContext.ATR14))

		sb.WriteString(fmt.Sprintf("Current Volume: %.3f vs. Average Volume: %.3f\n\n",
			data.LongerTermContext.CurrentVolume, data.LongerTermContext.AverageVolume))

		if len(data.LongerTermContext.MACDValues) > 0 {
			sb.WriteString(fmt.Sprintf("MACD indicators: %s\n\n", formatFloatSlice(data.LongerTermContext.MACDValues)))
		}

		if len(data.LongerTermContext.RSI14Values) > 0 {
			sb.WriteString(fmt.Sprintf("RSI indicators (14‑Period): %s\n\n", formatFloatSlice(data.LongerTermContext.RSI14Values)))
		}
	}

	return sb.String()
}

func calculateRangeState(klines []Kline, atr float64, currentPrice float64) *RangeState {
	if len(klines) == 0 || currentPrice <= 0 {
		return nil
	}

	lookback := minInt(len(klines), 40)
	subset := klines[len(klines)-lookback:]

	high := subset[0].High
	low := subset[0].Low
	for _, k := range subset {
		if k.High > high {
			high = k.High
		}
		if k.Low < low {
			low = k.Low
		}
	}

	width := high - low
	if width <= 0 {
		return nil
	}

	mid := (high + low) / 2
	widthPct := 0.0
	if mid > 0 {
		widthPct = (width / mid) * 100
	}

	widthToATR := 0.0
	if atr > 0 {
		widthToATR = width / atr
	}

	vwap := calculateVWAP(subset)

	touchThreshold := math.Max(atr*0.3, width*0.05)
	if touchThreshold == 0 {
		touchThreshold = math.Max(high*0.001, currentPrice*0.001)
	}

	touchesHigh := 0
	touchesLow := 0
	for _, k := range subset {
		if math.Abs(k.High-high) <= touchThreshold || math.Abs(k.Close-high) <= touchThreshold {
			touchesHigh++
		}
		if math.Abs(k.Low-low) <= touchThreshold || math.Abs(k.Close-low) <= touchThreshold {
			touchesLow++
		}
	}

	distHighPct := 0.0
	distLowPct := 0.0
	if currentPrice > 0 {
		distHighPct = ((high - currentPrice) / currentPrice) * 100
		distLowPct = ((currentPrice - low) / currentPrice) * 100
	}

	priceLocation := "inside"
	if currentPrice > high {
		priceLocation = "above"
	} else if currentPrice < low {
		priceLocation = "below"
	}

	regime := "range"
	if priceLocation == "above" {
		regime = "breakout_up"
	} else if priceLocation == "below" {
		regime = "breakout_down"
	} else if widthToATR <= 1.0 {
		regime = "squeeze"
	} else if touchesHigh <= 1 && touchesLow <= 1 {
		regime = "trend_attempt"
	}

	return &RangeState{
		Timeframe:         "15m",
		LookbackCandles:   lookback,
		LookbackMinutes:   lookback * 15,
		High:              high,
		Low:               low,
		Mid:               mid,
		Width:             width,
		WidthPct:          widthPct,
		ATR14:             atr,
		WidthToATR14:      widthToATR,
		VWAP:              vwap,
		TouchesHigh:       touchesHigh,
		TouchesLow:        touchesLow,
		DistanceToHighPct: distHighPct,
		DistanceToLowPct:  distLowPct,
		PriceLocation:     priceLocation,
		Regime:            regime,
	}
}

func calculateVWAP(klines []Kline) float64 {
	var pvSum, volSum float64
	for _, k := range klines {
		typical := (k.High + k.Low + k.Close) / 3
		pvSum += typical * k.Volume
		volSum += k.Volume
	}
	if volSum <= 0 {
		return 0
	}
	return pvSum / volSum
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// formatFloatSlice 格式化float64切片为字符串
func formatFloatSlice(values []float64) string {
	strValues := make([]string, len(values))
	for i, v := range values {
		strValues[i] = fmt.Sprintf("%.3f", v)
	}
	return "[" + strings.Join(strValues, ", ") + "]"
}

// Normalize 标准化symbol,确保是USDT交易对
func Normalize(symbol string) string {
	symbol = strings.TrimSpace(symbol)
	symbol = strings.ToUpper(symbol)
	symbol = stripNonAlphanumeric(symbol)
	if !strings.HasSuffix(symbol, "USDT") {
		symbol += "USDT"
	}
	return symbol
}

// stripNonAlphanumeric 去除非字母数字字符，兼容 "BTC/USDT" 等写法
func stripNonAlphanumeric(s string) string {
	var builder strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		isDigit := c >= '0' && c <= '9'
		isUpper := c >= 'A' && c <= 'Z'
		if isDigit || isUpper {
			builder.WriteByte(c)
			continue
		}
		if c >= 'a' && c <= 'z' {
			builder.WriteByte(c - 'a' + 'A')
		}
	}
	return builder.String()
}

// parseFloat 解析float值
func parseFloat(v interface{}) (float64, error) {
	switch val := v.(type) {
	case string:
		return strconv.ParseFloat(val, 64)
	case float64:
		return val, nil
	case int:
		return float64(val), nil
	case int64:
		return float64(val), nil
	default:
		return 0, fmt.Errorf("unsupported type: %T", v)
	}
}

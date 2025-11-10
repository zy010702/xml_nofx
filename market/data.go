package market

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
)

// Get 获取指定代币的市场数据
func Get(symbol string) (*Data, error) {
	var klines3m, klines5m, klines30m, klines4h []Kline
	var err error
	// 标准化symbol
	symbol = Normalize(symbol)
	// 获取3分钟K线数据 (最近100个)
	klines3m, err = WSMonitorCli.GetCurrentKlines(symbol, "3m") // 多获取一些用于计算
	if err != nil {
		return nil, fmt.Errorf("获取3分钟K线失败: %v", err)
	}

	// 获取5分钟K线数据（如果失败，使用3分钟数据作为fallback）
	klines5m, err = WSMonitorCli.GetCurrentKlines(symbol, "5m")
	if err != nil {
		// 如果5分钟数据获取失败，使用3分钟数据作为fallback（但标记为数据不足）
		log.Printf("⚠️  获取 %s 5分钟K线失败，使用3分钟数据: %v", symbol, err)
		klines5m = klines3m // 使用3分钟数据作为fallback
	}

	// 获取30分钟K线数据（如果失败，使用4小时数据作为fallback）
	klines30m, err = WSMonitorCli.GetCurrentKlines(symbol, "30m")
	if err != nil {
		// 如果30分钟数据获取失败，使用4小时数据作为fallback
		log.Printf("⚠️  获取 %s 30分钟K线失败，使用4小时数据: %v", symbol, err)
		klines30m = klines4h // 使用4小时数据作为fallback
	}

	// 获取4小时K线数据 (最近100个)
	klines4h, err = WSMonitorCli.GetCurrentKlines(symbol, "4h") // 多获取用于计算指标
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 计算当前指标 (基于3分钟最新数据)
	currentPrice := klines3m[len(klines3m)-1].Close
	currentEMA20 := calculateEMA(klines3m, 20)
	currentMACD := calculateMACD(klines3m)
	currentRSI7 := calculateRSI(klines3m, 7)

	// 计算价格变化百分比
	// 1小时价格变化 = 20个3分钟K线前的价格
	priceChange1h := 0.0
	if len(klines3m) >= 21 { // 至少需要21根K线 (当前 + 20根前)
		price1hAgo := klines3m[len(klines3m)-21].Close
		if price1hAgo > 0 {
			priceChange1h = ((currentPrice - price1hAgo) / price1hAgo) * 100
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

	// 计算日内系列数据
	intradayData := calculateIntradaySeries(klines3m)

	// 计算长期数据
	longerTermData := calculateLongerTermData(klines4h)

	// 计算多时间框架的 Supertrend
	supertrendData := calculateSupertrendMultiTimeframe(klines3m, klines5m, klines30m, klines4h, currentPrice)

	// 计算量价关系数据
	volumePriceData := calculateVolumePriceData(klines3m, klines5m, klines30m, currentPrice)

	return &Data{
		Symbol:            symbol,
		CurrentPrice:      currentPrice,
		PriceChange1h:     priceChange1h,
		PriceChange4h:     priceChange4h,
		CurrentEMA20:      currentEMA20,
		CurrentMACD:       currentMACD,
		CurrentRSI7:       currentRSI7,
		OpenInterest:      oiData,
		FundingRate:       fundingRate,
		IntradaySeries:    intradayData,
		LongerTermContext: longerTermData,
		SupertrendData:    supertrendData,
		VolumePriceData:   volumePriceData,
	}, nil
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

	body, err := io.ReadAll(resp.Body)
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

	body, err := io.ReadAll(resp.Body)
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

// Format 格式化输出市场数据
func Format(data *Data) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("current_price = %.2f, current_ema20 = %.3f, current_macd = %.3f, current_rsi (7 period) = %.3f\n\n",
		data.CurrentPrice, data.CurrentEMA20, data.CurrentMACD, data.CurrentRSI7))

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

	// 添加 Supertrend 多时间框架数据
	if data.SupertrendData != nil {
		sb.WriteString("Supertrend Multi-Timeframe Analysis:\n\n")

		if data.SupertrendData.Timeframe3m != nil {
			st := data.SupertrendData.Timeframe3m
			sb.WriteString(fmt.Sprintf("3m: Trend=%s, Signal=%s, Value=%.4f, ATR=%.4f\n",
				st.Trend, st.Signal, st.Value, st.ATR))
		}

		if data.SupertrendData.Timeframe5m != nil {
			st := data.SupertrendData.Timeframe5m
			sb.WriteString(fmt.Sprintf("5m: Trend=%s, Signal=%s, Value=%.4f, ATR=%.4f\n",
				st.Trend, st.Signal, st.Value, st.ATR))
		}

		if data.SupertrendData.Timeframe30m != nil {
			st := data.SupertrendData.Timeframe30m
			sb.WriteString(fmt.Sprintf("30m: Trend=%s, Signal=%s, Value=%.4f, ATR=%.4f\n",
				st.Trend, st.Signal, st.Value, st.ATR))
		}

		if data.SupertrendData.Timeframe4h != nil {
			st := data.SupertrendData.Timeframe4h
			sb.WriteString(fmt.Sprintf("4h (Major Trend): Trend=%s, Signal=%s, Value=%.4f, ATR=%.4f\n\n",
				st.Trend, st.Signal, st.Value, st.ATR))
		}
	}

	// 添加量价关系数据
	if data.VolumePriceData != nil {
		sb.WriteString("Volume-Price Analysis:\n\n")
		sb.WriteString(fmt.Sprintf("Volume Ratio: 3m=%.2f, 5m=%.2f, 30m=%.2f\n",
			data.VolumePriceData.VolumeRatio3m, data.VolumePriceData.VolumeRatio5m, data.VolumePriceData.VolumeRatio30m))
		sb.WriteString(fmt.Sprintf("Volume Trend: %s\n", data.VolumePriceData.VolumeTrend))
		sb.WriteString(fmt.Sprintf("Price-Volume OK: %v\n\n", data.VolumePriceData.PriceVolumeOK))
	}

	return sb.String()
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
	symbol = strings.ToUpper(symbol)
	if strings.HasSuffix(symbol, "USDT") {
		return symbol
	}
	return symbol + "USDT"
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

// calculateSupertrend 计算单个时间框架的 Supertrend 指标
// period: ATR 周期，通常为 10
// multiplier: 乘数，通常为 3.0
func calculateSupertrend(klines []Kline, period int, multiplier float64, currentPrice float64) *SupertrendData {
	if len(klines) < period+1 {
		return &SupertrendData{
			Trend:  "none",
			Signal: "none",
		}
	}

	// 计算 ATR
	atr := calculateATR(klines, period)
	if atr == 0 {
		return &SupertrendData{
			Trend:  "none",
			Signal: "none",
		}
	}

	// 计算基础带（Basic Band）
	// 使用最近一根K线的 HL2 (High + Low) / 2
	lastKline := klines[len(klines)-1]
	hl2 := (lastKline.High + lastKline.Low) / 2.0

	upperBand := hl2 + (multiplier * atr)
	lowerBand := hl2 - (multiplier * atr)

	// 计算最终的 Supertrend 值（需要参考前一个 Supertrend）
	// 简化版本：使用基础带作为初始值，然后根据价格位置调整
	var finalUpperBand, finalLowerBand float64
	var prevTrend string = "up" // 默认假设前一个趋势是向上

	// 如果数据足够，尝试推断前一个趋势
	if len(klines) >= 2 {
		prevKline := klines[len(klines)-2]
		prevHL2 := (prevKline.High + prevKline.Low) / 2.0
		prevATR := calculateATR(klines[:len(klines)-1], period)
		if prevATR > 0 {
			prevUpper := prevHL2 + (multiplier * prevATR)
			prevLower := prevHL2 - (multiplier * prevATR)
			if prevKline.Close > prevUpper {
				prevTrend = "up"
			} else if prevKline.Close < prevLower {
				prevTrend = "down"
			}
		}
	}

	// 根据前一个趋势确定最终的 Supertrend 值
	if prevTrend == "up" {
		finalUpperBand = math.Max(upperBand, lowerBand) // 保持上升趋势时，使用较大的值
		finalLowerBand = lowerBand
	} else {
		finalUpperBand = upperBand
		finalLowerBand = math.Min(upperBand, lowerBand) // 保持下降趋势时，使用较小的值
	}

	// 确定当前趋势和信号
	var trend string
	var signal string
	var value float64

	if currentPrice > finalUpperBand {
		trend = "up"
		signal = "long"
		value = finalLowerBand
	} else if currentPrice < finalLowerBand {
		trend = "down"
		signal = "short"
		value = finalUpperBand
	} else {
		// 价格在上下轨之间，保持前一个趋势
		trend = prevTrend
		if trend == "up" {
			signal = "long"
			value = finalLowerBand
		} else {
			signal = "short"
			value = finalUpperBand
		}
	}

	return &SupertrendData{
		Trend:     trend,
		Value:     value,
		ATR:       atr,
		UpperBand: finalUpperBand,
		LowerBand: finalLowerBand,
		Signal:    signal,
	}
}

// calculateSupertrendMultiTimeframe 计算多时间框架的 Supertrend
func calculateSupertrendMultiTimeframe(klines3m, klines5m, klines30m, klines4h []Kline, currentPrice float64) *SupertrendMultiTimeframe {
	// 使用标准参数：ATR period = 10, multiplier = 3.0
	period := 10
	multiplier := 3.0

	// 确保每个时间框架都有数据，即使数据不足也返回一个有效的结构
	result := &SupertrendMultiTimeframe{
		Timeframe3m:  calculateSupertrend(klines3m, period, multiplier, currentPrice),
		Timeframe5m:  calculateSupertrend(klines5m, period, multiplier, currentPrice),
		Timeframe30m: calculateSupertrend(klines30m, period, multiplier, currentPrice),
		Timeframe4h:  calculateSupertrend(klines4h, period, multiplier, currentPrice),
	}

	// 确保所有字段都不为 nil
	if result.Timeframe3m == nil {
		result.Timeframe3m = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe5m == nil {
		result.Timeframe5m = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe30m == nil {
		result.Timeframe30m = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe4h == nil {
		result.Timeframe4h = &SupertrendData{Trend: "none", Signal: "none"}
	}

	return result
}

// calculateVolumePriceData 计算量价关系数据
func calculateVolumePriceData(klines3m, klines5m, klines30m []Kline, currentPrice float64) *VolumePriceData {
	data := &VolumePriceData{
		VolumeTrend:    "stable",
		PriceVolumeOK:  false, // 默认值
	}

	// 计算3分钟成交量比率
	if len(klines3m) >= 20 {
		currentVol := klines3m[len(klines3m)-1].Volume
		sum := 0.0
		for i := len(klines3m) - 20; i < len(klines3m)-1; i++ {
			sum += klines3m[i].Volume
		}
		avgVol := sum / 19.0
		if avgVol > 0 {
			data.VolumeRatio3m = currentVol / avgVol
		}
	}

	// 计算5分钟成交量比率
	if len(klines5m) >= 20 {
		currentVol := klines5m[len(klines5m)-1].Volume
		sum := 0.0
		for i := len(klines5m) - 20; i < len(klines5m)-1; i++ {
			sum += klines5m[i].Volume
		}
		avgVol := sum / 19.0
		if avgVol > 0 {
			data.VolumeRatio5m = currentVol / avgVol
		}
	}

	// 计算30分钟成交量比率
	if len(klines30m) >= 20 {
		currentVol := klines30m[len(klines30m)-1].Volume
		sum := 0.0
		for i := len(klines30m) - 20; i < len(klines30m)-1; i++ {
			sum += klines30m[i].Volume
		}
		avgVol := sum / 19.0
		if avgVol > 0 {
			data.VolumeRatio30m = currentVol / avgVol
		}
	}

	// 判断成交量趋势（基于3分钟数据）
	if len(klines3m) >= 5 {
		recentVols := make([]float64, 0, 5)
		for i := len(klines3m) - 5; i < len(klines3m); i++ {
			recentVols = append(recentVols, klines3m[i].Volume)
		}
		// 简单趋势判断：比较最近3根和之前2根的平均成交量
		recentAvg := (recentVols[3] + recentVols[4]) / 2.0
		olderAvg := (recentVols[0] + recentVols[1] + recentVols[2]) / 3.0
		if recentAvg > olderAvg*1.1 {
			data.VolumeTrend = "increasing"
		} else if recentAvg < olderAvg*0.9 {
			data.VolumeTrend = "decreasing"
		} else {
			data.VolumeTrend = "stable"
		}
	}

	// 判断量价关系是否健康
	// 价涨量增或价跌量减为健康
	if len(klines3m) >= 2 {
		priceChange := currentPrice - klines3m[len(klines3m)-2].Close
		volumeChange := klines3m[len(klines3m)-1].Volume - klines3m[len(klines3m)-2].Volume

		// 价涨量增 或 价跌量减
		if (priceChange > 0 && volumeChange > 0) || (priceChange < 0 && volumeChange < 0) {
			data.PriceVolumeOK = true
		} else {
			data.PriceVolumeOK = false
		}
	}

	return data
}

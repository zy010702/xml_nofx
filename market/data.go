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
	var klines3m, klines5m, klines15m, klines30m, klines1h, klines4h []Kline
	var err error
	// 标准化symbol
	symbol = Normalize(symbol)
	// 获取3分钟K线数据 (最近100个)
	klines3m, err = WSMonitorCli.GetCurrentKlines(symbol, "3m") // 多获取一些用于计算
	if err != nil {
		return nil, fmt.Errorf("获取3分钟K线失败: %v", err)
	}

	// 获取4小时K线数据 (最近100个) - 先获取，用于30分钟fallback
	klines4h, err = WSMonitorCli.GetCurrentKlines(symbol, "4h") // 多获取用于计算指标
	if err != nil {
		return nil, fmt.Errorf("获取4小时K线失败: %v", err)
	}

	// 获取5分钟K线数据（必须获取真实数据，不使用fallback）
	klines5m, err = WSMonitorCli.GetCurrentKlines(symbol, "5m")
	if err != nil {
		log.Printf("⚠️  获取 %s 5分钟K线失败: %v", symbol, err)
		// 不使用fallback，保持为空，后续会标记为数据不足
		klines5m = []Kline{}
	}

	// 获取15分钟K线数据（必须获取真实数据，不使用fallback）
	klines15m, err = WSMonitorCli.GetCurrentKlines(symbol, "15m")
	if err != nil {
		log.Printf("⚠️  获取 %s 15分钟K线失败: %v", symbol, err)
		// 不使用fallback，保持为空，后续会标记为数据不足
		klines15m = []Kline{}
	}

	// 获取30分钟K线数据（必须获取真实数据，不使用fallback）
	klines30m, err = WSMonitorCli.GetCurrentKlines(symbol, "30m")
	if err != nil {
		log.Printf("⚠️  获取 %s 30分钟K线失败: %v", symbol, err)
		// 不使用fallback，保持为空，后续会标记为数据不足
		klines30m = []Kline{}
	}

	// 获取1小时K线数据（必须获取真实数据，不使用fallback）
	klines1h, err = WSMonitorCli.GetCurrentKlines(symbol, "1h")
	if err != nil {
		log.Printf("⚠️  获取 %s 1小时K线失败: %v", symbol, err)
		// 不使用fallback，保持为空，后续会标记为数据不足
		klines1h = []Kline{}
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
	supertrendData := calculateSupertrendMultiTimeframe(klines3m, klines5m, klines15m, klines30m, klines1h, klines4h, currentPrice)

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

	// 添加 Supertrend 多时间框架数据（只显示核心时间框架：5m、15m、30m、4h）
	// 移除3m（太短期，噪音多）和1h（与4h重叠，冗余）
	if data.SupertrendData != nil {
		sb.WriteString("Supertrend Multi-Timeframe Analysis:\n\n")

		if data.SupertrendData.Timeframe5m != nil {
			st := data.SupertrendData.Timeframe5m
			sb.WriteString(fmt.Sprintf("5m: Trend=%s, Signal=%s, Value=%.4f, ATR=%.4f\n",
				st.Trend, st.Signal, st.Value, st.ATR))
		}

		if data.SupertrendData.Timeframe15m != nil {
			st := data.SupertrendData.Timeframe15m
			sb.WriteString(fmt.Sprintf("15m: Trend=%s, Signal=%s, Value=%.4f, ATR=%.4f\n",
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

// calculateSupertrend 计算单个时间框架的 Supertrend 指标（标准算法）
// period: ATR 周期，通常为 10
// multiplier: 乘数，通常为 3.0
func calculateSupertrend(klines []Kline, period int, multiplier float64, currentPrice float64) *SupertrendData {
	// 数据不足时返回none
	if len(klines) < period+1 {
		if len(klines) < 3 {
			return &SupertrendData{
				Trend:  "none",
				Signal: "none",
			}
		}
		// 如果数据不足但至少有3根，使用更小的周期
		period = len(klines) - 1
		if period < 3 {
			period = 3
		}
	}

	// 计算每根K线的ATR（使用滑动窗口）
	atrValues := make([]float64, len(klines))
	for i := period; i < len(klines); i++ {
		atrValues[i] = calculateATR(klines[i-period:i+1], period)
	}
	
	// 填充前面的ATR值
	if len(atrValues) > period {
		firstATR := atrValues[period]
		for i := 0; i < period; i++ {
			atrValues[i] = firstATR
		}
	} else {
		// 如果数据不足，使用整体ATR
		atr := calculateATR(klines, period)
		if atr == 0 {
			if len(klines) >= 2 {
				maxHigh := klines[0].High
				minLow := klines[0].Low
				for _, k := range klines {
					if k.High > maxHigh {
						maxHigh = k.High
					}
					if k.Low < minLow {
						minLow = k.Low
					}
				}
				atr = (maxHigh - minLow) / float64(len(klines))
				if atr == 0 {
					atr = currentPrice * 0.01
				}
			} else {
				return &SupertrendData{
					Trend:  "none",
					Signal: "none",
				}
			}
		}
		for i := range atrValues {
			atrValues[i] = atr
		}
	}

	// 计算每根K线的Supertrend值（标准算法）
	superTrendValues := make([]float64, len(klines))
	trends := make([]string, len(klines))

	// 初始化第一根K线
	if len(klines) > 0 {
		hl2 := (klines[0].High + klines[0].Low) / 2.0
		lowerBand := hl2 - (multiplier * atrValues[0])
		// 初始值设为下轨，趋势向上
		superTrendValues[0] = lowerBand
		trends[0] = "up"
	}

	// 计算后续K线的Supertrend值
	for i := 1; i < len(klines); i++ {
		hl2 := (klines[i].High + klines[i].Low) / 2.0
		atr := atrValues[i]
		if atr == 0 && i > 0 {
			atr = atrValues[i-1]
		}

		upperBand := hl2 + (multiplier * atr)
		lowerBand := hl2 - (multiplier * atr)

		// 获取前一个Supertrend值和趋势
		prevSuperTrend := superTrendValues[i-1]
		prevTrend := trends[i-1]

		// 计算最终的上轨和下轨（标准Supertrend算法）
		var finalUpperBand, finalLowerBand float64
		if prevTrend == "up" {
			// 上升趋势：上轨和下轨都要考虑前一个Supertrend值
			finalUpperBand = math.Max(upperBand, prevSuperTrend)
			finalLowerBand = math.Max(lowerBand, prevSuperTrend)
		} else {
			// 下降趋势
			finalUpperBand = math.Min(upperBand, prevSuperTrend)
			finalLowerBand = math.Min(lowerBand, prevSuperTrend)
		}

		// 确定当前趋势（基于收盘价）
		closePrice := klines[i].Close
		if closePrice > finalUpperBand {
			trends[i] = "up"
			superTrendValues[i] = finalLowerBand
		} else if closePrice < finalLowerBand {
			trends[i] = "down"
			superTrendValues[i] = finalUpperBand
		} else {
			// 价格在上下轨之间，保持前一个趋势
			trends[i] = prevTrend
			if prevTrend == "up" {
				superTrendValues[i] = finalLowerBand
			} else {
				superTrendValues[i] = finalUpperBand
			}
		}
	}

	// 获取最新的Supertrend值
	lastIndex := len(klines) - 1
	currentTrend := trends[lastIndex]
	currentSuperTrend := superTrendValues[lastIndex]
	lastATR := atrValues[lastIndex]
	if lastATR == 0 {
		lastATR = calculateATR(klines, period)
	}

	// 计算当前的基础带（用于显示）
	lastKline := klines[lastIndex]
	hl2 := (lastKline.High + lastKline.Low) / 2.0
	upperBand := hl2 + (multiplier * lastATR)
	lowerBand := hl2 - (multiplier * lastATR)

	// 确定信号（基于当前价格和Supertrend值）
	var signal string
	if currentPrice > currentSuperTrend {
		signal = "long"
	} else if currentPrice < currentSuperTrend {
		signal = "short"
	} else {
		// 价格等于Supertrend值，根据趋势判断
		if currentTrend == "up" {
			signal = "long"
		} else {
			signal = "short"
		}
	}

	return &SupertrendData{
		Trend:     currentTrend,
		Value:     currentSuperTrend,
		ATR:       lastATR,
		UpperBand: upperBand,
		LowerBand: lowerBand,
		Signal:    signal,
	}
}

// calculateSupertrendMultiTimeframe 计算多时间框架的 Supertrend
func calculateSupertrendMultiTimeframe(klines3m, klines5m, klines15m, klines30m, klines1h, klines4h []Kline, currentPrice float64) *SupertrendMultiTimeframe {
	// 使用标准参数：ATR period = 10, multiplier = 3.0
	period := 10
	multiplier := 3.0

	// 确保每个时间框架都有数据，即使数据不足也返回一个有效的结构
	result := &SupertrendMultiTimeframe{
		Timeframe3m:  calculateSupertrend(klines3m, period, multiplier, currentPrice),
		Timeframe5m:  calculateSupertrend(klines5m, period, multiplier, currentPrice),
		Timeframe15m: calculateSupertrend(klines15m, period, multiplier, currentPrice),
		Timeframe30m: calculateSupertrend(klines30m, period, multiplier, currentPrice),
		Timeframe1h:  calculateSupertrend(klines1h, period, multiplier, currentPrice),
		Timeframe4h:  calculateSupertrend(klines4h, period, multiplier, currentPrice),
	}

	// 确保所有字段都不为 nil
	if result.Timeframe3m == nil {
		result.Timeframe3m = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe5m == nil {
		result.Timeframe5m = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe15m == nil {
		result.Timeframe15m = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe30m == nil {
		result.Timeframe30m = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe1h == nil {
		result.Timeframe1h = &SupertrendData{Trend: "none", Signal: "none"}
	}
	if result.Timeframe4h == nil {
		result.Timeframe4h = &SupertrendData{Trend: "none", Signal: "none"}
	}

	return result
}

// calculateVolumePriceData 计算量价关系数据（使用真实数据）
func calculateVolumePriceData(klines3m, klines5m, klines30m []Kline, currentPrice float64) *VolumePriceData {
	data := &VolumePriceData{
		VolumeTrend:    "stable",
		PriceVolumeOK:  false, // 默认值
	}

	// 计算3分钟成交量比率（使用最近20根K线的平均成交量）
	if len(klines3m) >= 20 {
		currentVol := klines3m[len(klines3m)-1].Volume
		sum := 0.0
		count := 0
		for i := len(klines3m) - 20; i < len(klines3m)-1; i++ {
			if i >= 0 {
				sum += klines3m[i].Volume
				count++
			}
		}
		if count > 0 {
			avgVol := sum / float64(count)
			if avgVol > 0 {
				data.VolumeRatio3m = currentVol / avgVol
			}
		}
	} else if len(klines3m) >= 5 {
		// 如果数据不足20根，使用所有可用数据
		currentVol := klines3m[len(klines3m)-1].Volume
		sum := 0.0
		for i := 0; i < len(klines3m)-1; i++ {
			sum += klines3m[i].Volume
		}
		avgVol := sum / float64(len(klines3m)-1)
		if avgVol > 0 {
			data.VolumeRatio3m = currentVol / avgVol
		}
	}

	// 计算5分钟成交量比率（使用真实5分钟数据）
	if len(klines5m) >= 20 {
		currentVol := klines5m[len(klines5m)-1].Volume
		sum := 0.0
		count := 0
		for i := len(klines5m) - 20; i < len(klines5m)-1; i++ {
			if i >= 0 {
				sum += klines5m[i].Volume
				count++
			}
		}
		if count > 0 {
			avgVol := sum / float64(count)
			if avgVol > 0 {
				data.VolumeRatio5m = currentVol / avgVol
			}
		}
	} else if len(klines5m) >= 5 {
		// 如果数据不足20根，使用所有可用数据
		currentVol := klines5m[len(klines5m)-1].Volume
		sum := 0.0
		for i := 0; i < len(klines5m)-1; i++ {
			sum += klines5m[i].Volume
		}
		avgVol := sum / float64(len(klines5m)-1)
		if avgVol > 0 {
			data.VolumeRatio5m = currentVol / avgVol
		}
	}

	// 计算30分钟成交量比率（使用真实30分钟数据）
	if len(klines30m) >= 20 {
		currentVol := klines30m[len(klines30m)-1].Volume
		sum := 0.0
		count := 0
		for i := len(klines30m) - 20; i < len(klines30m)-1; i++ {
			if i >= 0 {
				sum += klines30m[i].Volume
				count++
			}
		}
		if count > 0 {
			avgVol := sum / float64(count)
			if avgVol > 0 {
				data.VolumeRatio30m = currentVol / avgVol
			}
		}
	} else if len(klines30m) >= 5 {
		// 如果数据不足20根，使用所有可用数据
		currentVol := klines30m[len(klines30m)-1].Volume
		sum := 0.0
		for i := 0; i < len(klines30m)-1; i++ {
			sum += klines30m[i].Volume
		}
		avgVol := sum / float64(len(klines30m)-1)
		if avgVol > 0 {
			data.VolumeRatio30m = currentVol / avgVol
		}
	}

	// 判断成交量趋势（基于3分钟数据，使用最近10根K线，更严谨）
	if len(klines3m) >= 10 {
		// 计算最近5根和之前5根的平均成交量
		recentSum := 0.0
		olderSum := 0.0
		for i := len(klines3m) - 5; i < len(klines3m); i++ {
			recentSum += klines3m[i].Volume
		}
		for i := len(klines3m) - 10; i < len(klines3m)-5; i++ {
			olderSum += klines3m[i].Volume
		}
		recentAvg := recentSum / 5.0
		olderAvg := olderSum / 5.0
		if olderAvg > 0 {
			ratio := recentAvg / olderAvg
			// 使用更严格的阈值：15%的变化才认为是趋势变化
			if ratio > 1.15 {
				data.VolumeTrend = "increasing"
			} else if ratio < 0.85 {
				data.VolumeTrend = "decreasing"
			} else {
				data.VolumeTrend = "stable"
			}
		}
	} else if len(klines3m) >= 5 {
		// 如果数据不足10根但至少有5根，使用最近3根和之前2根比较
		recentSum := 0.0
		olderSum := 0.0
		for i := len(klines3m) - 3; i < len(klines3m); i++ {
			recentSum += klines3m[i].Volume
		}
		for i := len(klines3m) - 5; i < len(klines3m)-3; i++ {
			olderSum += klines3m[i].Volume
		}
		recentAvg := recentSum / 3.0
		olderAvg := olderSum / 2.0
		if olderAvg > 0 {
			ratio := recentAvg / olderAvg
			if ratio > 1.2 {
				data.VolumeTrend = "increasing"
			} else if ratio < 0.8 {
				data.VolumeTrend = "decreasing"
			} else {
				data.VolumeTrend = "stable"
			}
		}
	} else if len(klines3m) >= 3 {
		// 如果数据不足5根，使用简单比较
		recentAvg := klines3m[len(klines3m)-1].Volume
		olderAvg := 0.0
		for i := 0; i < len(klines3m)-1; i++ {
			olderAvg += klines3m[i].Volume
		}
		olderAvg = olderAvg / float64(len(klines3m)-1)
		if olderAvg > 0 {
			ratio := recentAvg / olderAvg
			if ratio > 1.3 {
				data.VolumeTrend = "increasing"
			} else if ratio < 0.7 {
				data.VolumeTrend = "decreasing"
			} else {
				data.VolumeTrend = "stable"
			}
		}
	}

	// 判断量价关系是否健康（使用更严谨的方法）
	// 使用最近5-10根K线，计算价格和成交量的相关性
	if len(klines3m) >= 5 {
		// 使用最近5根K线计算量价关系
		windowSize := 5
		if len(klines3m) < 5 {
			windowSize = len(klines3m)
		}
		
		// 计算价格变化百分比和成交量变化
		priceChanges := make([]float64, 0, windowSize)
		volumeChanges := make([]float64, 0, windowSize)
		
		for i := len(klines3m) - windowSize; i < len(klines3m); i++ {
			if i > 0 {
				prevPrice := klines3m[i-1].Close
				currPrice := klines3m[i].Close
				prevVolume := klines3m[i-1].Volume
				currVolume := klines3m[i].Volume
				
				// 计算价格变化百分比（避免除零）
				if prevPrice > 0 {
					priceChangePct := ((currPrice - prevPrice) / prevPrice) * 100.0
					priceChanges = append(priceChanges, priceChangePct)
				}
				
				// 计算成交量变化百分比
				if prevVolume > 0 {
					volumeChangePct := ((currVolume - prevVolume) / prevVolume) * 100.0
					volumeChanges = append(volumeChanges, volumeChangePct)
				} else if currVolume > 0 {
					// 如果前一根成交量为0，当前有成交量，认为是增加
					volumeChanges = append(volumeChanges, 100.0)
				}
			}
		}
		
		if len(priceChanges) >= 3 && len(volumeChanges) >= 3 {
			// 计算价格和成交量的总体趋势（使用加权平均）
			priceTrendSum := 0.0
			volumeTrendSum := 0.0
			positiveMatches := 0  // 价涨量增的次数
			negativeMatches := 0  // 价跌量减的次数
			totalMatches := 0
			
			for i := 0; i < len(priceChanges); i++ {
				priceTrendSum += priceChanges[i]
				volumeTrendSum += volumeChanges[i]
				
				// 检查是否价涨量增或价跌量减
				if priceChanges[i] > 0.1 && volumeChanges[i] > 0 { // 价涨量增（价格涨幅>0.1%）
					positiveMatches++
					totalMatches++
				} else if priceChanges[i] < -0.1 && volumeChanges[i] < 0 { // 价跌量减（价格跌幅>0.1%）
					negativeMatches++
					totalMatches++
				} else if math.Abs(priceChanges[i]) <= 0.1 {
					// 价格变化很小（<0.1%），认为是横盘，不计算匹配
				} else {
					totalMatches++
				}
			}
			
			avgPriceTrend := priceTrendSum / float64(len(priceChanges))
			avgVolumeTrend := volumeTrendSum / float64(len(volumeChanges))
			
			// 判断量价关系是否健康
			// 1. 如果价格和成交量趋势方向一致（都上涨或都下跌）
			// 2. 或者匹配度超过60%（价涨量增或价跌量减的次数占比）
			matchRatio := 0.0
			if totalMatches > 0 {
				matchRatio = float64(positiveMatches+negativeMatches) / float64(totalMatches)
			}
			
			// 价涨量增 或 价跌量减 为健康
			if (avgPriceTrend > 0 && avgVolumeTrend > 0) || (avgPriceTrend < 0 && avgVolumeTrend < 0) {
				data.PriceVolumeOK = true
			} else if matchRatio >= 0.6 {
				// 如果匹配度超过60%，认为量价关系健康
				data.PriceVolumeOK = true
			} else if math.Abs(avgPriceTrend) < 0.2 && math.Abs(avgVolumeTrend) < 5.0 {
				// 如果价格和成交量变化都很小（横盘），认为基本健康
				data.PriceVolumeOK = true
			} else {
				data.PriceVolumeOK = false
			}
		}
	} else if len(klines3m) >= 3 {
		// 如果数据不足5根，使用最近3根K线
		priceChanges := make([]float64, 0, 2)
		volumeChanges := make([]float64, 0, 2)
		
		for i := len(klines3m) - 2; i < len(klines3m); i++ {
			if i > 0 {
				prevPrice := klines3m[i-1].Close
				currPrice := klines3m[i].Close
				prevVolume := klines3m[i-1].Volume
				currVolume := klines3m[i].Volume
				
				if prevPrice > 0 {
					priceChangePct := ((currPrice - prevPrice) / prevPrice) * 100.0
					priceChanges = append(priceChanges, priceChangePct)
				}
				
				if prevVolume > 0 {
					volumeChangePct := ((currVolume - prevVolume) / prevVolume) * 100.0
					volumeChanges = append(volumeChanges, volumeChangePct)
				}
			}
		}
		
		if len(priceChanges) >= 1 && len(volumeChanges) >= 1 {
			// 简单判断：价涨量增或价跌量减
			allMatch := true
			for i := 0; i < len(priceChanges); i++ {
				if !((priceChanges[i] > 0.1 && volumeChanges[i] > 0) || 
					 (priceChanges[i] < -0.1 && volumeChanges[i] < 0) ||
					 math.Abs(priceChanges[i]) <= 0.1) {
					allMatch = false
					break
				}
			}
			data.PriceVolumeOK = allMatch
		}
	} else if len(klines3m) >= 2 {
		// 如果只有2根K线，使用简单比较
		prevPrice := klines3m[len(klines3m)-2].Close
		currPrice := klines3m[len(klines3m)-1].Close
		prevVolume := klines3m[len(klines3m)-2].Volume
		currVolume := klines3m[len(klines3m)-1].Volume
		
		if prevPrice > 0 && prevVolume > 0 {
			priceChangePct := ((currPrice - prevPrice) / prevPrice) * 100.0
			volumeChangePct := ((currVolume - prevVolume) / prevVolume) * 100.0
			
			// 价涨量增或价跌量减（考虑最小变化阈值）
			if (priceChangePct > 0.1 && volumeChangePct > 0) || 
			   (priceChangePct < -0.1 && volumeChangePct < 0) ||
			   math.Abs(priceChangePct) <= 0.1 {
				data.PriceVolumeOK = true
			} else {
				data.PriceVolumeOK = false
			}
		}
	}

	return data
}

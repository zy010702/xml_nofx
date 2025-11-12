package decision

import (
	"encoding/json"
	"fmt"
	"log"
	"nofx/market"
	"nofx/mcp"
	"nofx/pool"
	"strings"
	"time"
)

// PositionInfo 持仓信息
type PositionInfo struct {
	Symbol           string  `json:"symbol"`
	Side             string  `json:"side"` // "long" or "short"
	EntryPrice       float64 `json:"entry_price"`
	MarkPrice        float64 `json:"mark_price"`
	Quantity         float64 `json:"quantity"`
	Leverage         int     `json:"leverage"`
	UnrealizedPnL    float64 `json:"unrealized_pnl"`
	UnrealizedPnLPct float64 `json:"unrealized_pnl_pct"`
	LiquidationPrice float64 `json:"liquidation_price"`
	MarginUsed       float64 `json:"margin_used"`
	UpdateTime       int64   `json:"update_time"` // 持仓更新时间戳（毫秒）
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
	CurrentTime     string                  `json:"current_time"`
	RuntimeMinutes  int                     `json:"runtime_minutes"`
	CallCount       int                     `json:"call_count"`
	Account         AccountInfo             `json:"account"`
	Positions       []PositionInfo          `json:"positions"`
	CandidateCoins  []CandidateCoin         `json:"candidate_coins"`
	MarketDataMap   map[string]*market.Data `json:"-"` // 不序列化，但内部使用
	OITopDataMap    map[string]*OITopData   `json:"-"` // OI Top数据映射
	Performance     interface{}             `json:"-"` // 历史表现分析（logger.PerformanceAnalysis）
	BTCETHLeverage  int                     `json:"-"` // BTC/ETH杠杆倍数（从配置读取）
	AltcoinLeverage int                     `json:"-"` // 山寨币杠杆倍数（从配置读取）
}

// Decision AI的交易决策
type Decision struct {
	Symbol          string  `json:"symbol"`
	Action          string  `json:"action"` // "open_long", "open_short", "close_long", "close_short", "hold", "wait"
	Leverage        int     `json:"leverage,omitempty"`
	PositionSizeUSD float64 `json:"position_size_usd,omitempty"`
	StopLoss        float64 `json:"stop_loss,omitempty"`
	TakeProfit      float64 `json:"take_profit,omitempty"`
	Confidence      int     `json:"confidence,omitempty"` // 信心度 (0-100)
	RiskUSD         float64 `json:"risk_usd,omitempty"`   // 最大美元风险
	Reasoning       string  `json:"reasoning"`
}

// FullDecision AI的完整决策（包含思维链）
type FullDecision struct {
	SystemPrompt string     `json:"system_prompt"` // 系统提示词（发送给AI的系统prompt）
	UserPrompt   string     `json:"user_prompt"`   // 发送给AI的输入prompt
	CoTTrace     string     `json:"cot_trace"`     // 思维链分析（AI输出）
	Decisions    []Decision `json:"decisions"`     // 具体决策列表
	Timestamp    time.Time  `json:"timestamp"`
}

// GetFullDecision 获取AI的完整交易决策（批量分析所有币种和持仓）
func GetFullDecision(ctx *Context, mcpClient *mcp.Client) (*FullDecision, error) {
	return GetFullDecisionWithCustomPrompt(ctx, mcpClient, "", false, "")
}

// GetFullDecisionWithCustomPrompt 获取AI的完整交易决策（支持自定义prompt和模板选择）
func GetFullDecisionWithCustomPrompt(ctx *Context, mcpClient *mcp.Client, customPrompt string, overrideBase bool, templateName string) (*FullDecision, error) {
	// 1. 为所有币种获取市场数据
	if err := fetchMarketDataForContext(ctx); err != nil {
		return nil, fmt.Errorf("获取市场数据失败: %w", err)
	}

	// 2. 构建 System Prompt（固定规则）和 User Prompt（动态数据）
	systemPrompt := buildSystemPromptWithCustom(ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage, customPrompt, overrideBase, templateName)
	userPrompt := buildUserPrompt(ctx)

	// 3. 调用AI API（使用 system + user prompt）
	aiResponse, err := mcpClient.CallWithMessages(systemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("调用AI API失败: %w", err)
	}

	// 4. 解析AI响应
	decision, err := parseFullDecisionResponse(aiResponse, ctx.Account.TotalEquity, ctx.BTCETHLeverage, ctx.AltcoinLeverage)
	if err != nil {
		return decision, fmt.Errorf("解析AI响应失败: %w", err)
	}

	decision.Timestamp = time.Now()
	decision.SystemPrompt = systemPrompt // 保存系统prompt
	decision.UserPrompt = userPrompt     // 保存输入prompt
	return decision, nil
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

// buildSystemPromptWithCustom 构建包含自定义内容的 System Prompt
func buildSystemPromptWithCustom(accountEquity float64, btcEthLeverage, altcoinLeverage int, customPrompt string, overrideBase bool, templateName string) string {
	// 如果覆盖基础prompt且有自定义prompt，只使用自定义prompt
	if overrideBase && customPrompt != "" {
		return customPrompt
	}

	// 获取基础prompt（使用指定的模板）
	basePrompt := buildSystemPrompt(accountEquity, btcEthLeverage, altcoinLeverage, templateName)

	// 如果没有自定义prompt，直接返回基础prompt
	if customPrompt == "" {
		return basePrompt
	}

	// 添加自定义prompt部分到基础prompt
	var sb strings.Builder
	sb.WriteString(basePrompt)
	sb.WriteString("\n\n")
	sb.WriteString("# 📌 个性化交易策略\n\n")
	sb.WriteString(customPrompt)
	sb.WriteString("\n\n")
	sb.WriteString("注意: 以上个性化策略是对基础规则的补充，不能违背基础风险控制原则。\n")

	return sb.String()
}

// buildSystemPrompt 构建 System Prompt（使用模板+动态部分）
func buildSystemPrompt(accountEquity float64, btcEthLeverage, altcoinLeverage int, templateName string) string {
	var sb strings.Builder

	// 1. 加载提示词模板（核心交易策略部分）
	if templateName == "" {
		templateName = "default" // 默认使用 default 模板
	}

	template, err := GetPromptTemplate(templateName)
	if err != nil {
		// 如果模板不存在，记录错误并使用 default
		log.Printf("⚠️  提示词模板 '%s' 不存在，使用 default: %v", templateName, err)
		template, err = GetPromptTemplate("default")
		if err != nil {
			// 如果连 default 都不存在，使用内置的简化版本
			log.Printf("❌ 无法加载任何提示词模板，使用内置简化版本")
			sb.WriteString("你是专业的加密货币交易AI。请根据市场数据做出交易决策。\n\n")
		} else {
			sb.WriteString(template.Content)
			sb.WriteString("\n\n")
		}
	} else {
		sb.WriteString(template.Content)
		sb.WriteString("\n\n")
	}

	// 2. Supertrend 多时间框架交易策略
	sb.WriteString("# 📈 Supertrend 多时间框架交易策略\n\n")
	sb.WriteString("## 核心交易规则：\n\n")
	sb.WriteString("1. **信号触发条件（优化后，短期策略优先5分钟信号，3分钟信号对短期获利至关重要）**：\n")
	sb.WriteString("   - 优先级策略：5m+15m一致（优先，最敏感，5分钟信号改变可能影响后续）> 15m+30m一致 > 5m+30m一致\n")
	sb.WriteString("   - 🔴 3分钟信号对短期获利至关重要：如果3m与主信号相反，需要非常谨慎（3分钟信号变化可能预示短期趋势变化）\n")
	sb.WriteString("   - ✅ 如果3m与主信号一致，信号更强，可以更积极开仓\n")
	sb.WriteString("   - 5分钟信号最重要：因为短期策略中，5分钟信号改变可能改变后续信号，需要优先关注\n")
	sb.WriteString("   - 大趋势验证（灵活策略）：1小时为主，4小时为辅\n")
	sb.WriteString("   - ✅ 只要1小时或4小时其中一个与交易信号一致，就允许开仓（更灵活）\n")
	sb.WriteString("   - ❌ 如果1小时和4小时都与交易信号相反，则阻止开仓（风险控制）\n\n")
	sb.WriteString("2. **短期盈利优势判断（新增）**：\n")
	sb.WriteString("   - 做多优势：RSI < 40（超卖反弹）、MACD转强、价格低于EMA20\n")
	sb.WriteString("   - 做空优势：RSI > 60（超买回调）、MACD转弱、价格高于EMA20\n")
	sb.WriteString("   - 有短期盈利优势时，信号更强，可以更积极开仓\n")
	sb.WriteString("   - 没有明显优势时，需谨慎但也可以开仓（信号统一即可）\n\n")
	sb.WriteString("3. **量价关系验证（放宽）**：\n")
	sb.WriteString("   - 优先关注量价关系健康（价涨量增或价跌量减）\n")
	sb.WriteString("   - 如果量价关系不够理想但信号较强，可以交易但需谨慎\n")
	sb.WriteString("   - 成交量比率建议在0.3-3.0之间（<0.3极低需谨慎，>3.0异常波动需注意）\n\n")
	sb.WriteString("4. **时间框架优先级（短期策略优化）**：\n")
	sb.WriteString("   - 5分钟：核心信号（最重要，5分钟信号改变可能影响后续信号）\n")
	sb.WriteString("   - 15分钟：核心确认（与5分钟信号一致，形成主要交易信号）\n")
	sb.WriteString("   - 🔴 3分钟：关键信号（对短期获利至关重要，如果与主信号相反，需要非常谨慎）\n")
	sb.WriteString("   - 30分钟：中期确认（与5-15分钟信号一致）\n")
	sb.WriteString("   - 1小时：大趋势判断（主要参考，必须与交易信号一致或至少1h/4h其中一个一致）\n")
	sb.WriteString("   - 4小时：大趋势参考（辅助参考，与1小时配合使用）\n\n")
	sb.WriteString("5. **开仓条件总结（优化后，短期策略优先5分钟信号，3分钟信号对短期获利至关重要）**：\n")
	sb.WriteString("   - ✅ 5m+15m一致（优先，最敏感，5分钟信号最重要）\n")
	sb.WriteString("   - ✅ 或 15m+30m一致（备选，但需注意5分钟信号）\n")
	sb.WriteString("   - ✅ 或 5m+30m一致（备选，但需注意15分钟信号）\n")
	sb.WriteString("   - 🔴 3分钟信号对短期获利至关重要：与主信号一致时信号更强，相反时需要非常谨慎（3分钟信号变化可能预示短期趋势变化）\n")
	sb.WriteString("   - ✅ 大趋势验证：1小时或4小时至少一个与交易信号一致（灵活策略）\n")
	sb.WriteString("   - ❌ 如果1小时和4小时都与交易信号相反，则阻止开仓（风险控制）\n")
	sb.WriteString("   - ✅ 有短期盈利优势时（RSI超买/超卖、MACD转强/转弱等），信号更强\n")
	sb.WriteString("   - ⚠️ 量价关系健康为佳，但不强制（信号强时可放宽）\n")
	sb.WriteString("   - ⚠️ 成交量比率>0.3为佳，<0.3极低需谨慎\n\n")

	// 2. 硬约束（风险控制）- 动态生成
	sb.WriteString("# 硬约束（风险控制）\n\n")
	sb.WriteString("1. 风险回报比: 必须 ≥ 1:3（冒1%风险，赚3%+收益）\n")
	sb.WriteString("2. 最多持仓: 3个币种（质量>数量）\n")
	sb.WriteString(fmt.Sprintf("3. 单币仓位: 山寨%.0f-%.0f U(%dx杠杆) | BTC/ETH %.0f-%.0f U(%dx杠杆)\n",
		accountEquity*0.8, accountEquity*1.5, altcoinLeverage, accountEquity*5, accountEquity*10, btcEthLeverage))
	sb.WriteString("4. 保证金: 总使用率 ≤ 90%\n\n")

	// 3. 输出格式 - 动态生成
	sb.WriteString("#输出格式\n\n")
	sb.WriteString("第一步: 思维链（纯文本）\n")
	sb.WriteString("简洁分析你的思考过程\n\n")
	sb.WriteString("第二步: JSON决策数组\n\n")
	sb.WriteString("```json\n[\n")
	sb.WriteString(fmt.Sprintf("  {\"symbol\": \"BTCUSDT\", \"action\": \"open_short\", \"leverage\": %d, \"position_size_usd\": %.0f, \"stop_loss\": 97000, \"take_profit\": 91000, \"confidence\": 85, \"risk_usd\": 300, \"reasoning\": \"下跌趋势+MACD死叉\"},\n", btcEthLeverage, accountEquity*5))
	sb.WriteString("  {\"symbol\": \"ETHUSDT\", \"action\": \"close_long\", \"reasoning\": \"止盈离场\"}\n")
	sb.WriteString("]\n```\n\n")
	sb.WriteString("字段说明:\n")
	sb.WriteString("- `action`: open_long | open_short | close_long | close_short | hold | wait\n")
	sb.WriteString("- `confidence`: 0-100（开仓建议≥75）\n")
	sb.WriteString("- 开仓时必填: leverage, position_size_usd, stop_loss, take_profit, confidence, risk_usd, reasoning\n\n")

	return sb.String()
}

// buildUserPrompt 构建 User Prompt（动态数据）
func buildUserPrompt(ctx *Context) string {
	var sb strings.Builder

	// 系统状态
	sb.WriteString(fmt.Sprintf("时间: %s | 周期: #%d | 运行: %d分钟\n\n",
		ctx.CurrentTime, ctx.CallCount, ctx.RuntimeMinutes))

	// BTC 市场
	if btcData, hasBTC := ctx.MarketDataMap["BTCUSDT"]; hasBTC {
		sb.WriteString(fmt.Sprintf("BTC: %.2f (1h: %+.2f%%, 4h: %+.2f%%) | MACD: %.4f | RSI: %.2f\n\n",
			btcData.CurrentPrice, btcData.PriceChange1h, btcData.PriceChange4h,
			btcData.CurrentMACD, btcData.CurrentRSI7))
	}

	// 账户
	sb.WriteString(fmt.Sprintf("账户: 净值%.2f | 余额%.2f (%.1f%%) | 盈亏%+.2f%% | 保证金%.1f%% | 持仓%d个\n\n",
		ctx.Account.TotalEquity,
		ctx.Account.AvailableBalance,
		(ctx.Account.AvailableBalance/ctx.Account.TotalEquity)*100,
		ctx.Account.TotalPnLPct,
		ctx.Account.MarginUsedPct,
		ctx.Account.PositionCount))

	// 持仓（完整市场数据）
	if len(ctx.Positions) > 0 {
		sb.WriteString("## 当前持仓\n")
		for i, pos := range ctx.Positions {
			// 计算持仓时长
			holdingDuration := ""
			if pos.UpdateTime > 0 {
				durationMs := time.Now().UnixMilli() - pos.UpdateTime
				durationMin := durationMs / (1000 * 60) // 转换为分钟
				if durationMin < 60 {
					holdingDuration = fmt.Sprintf(" | 持仓时长%d分钟", durationMin)
				} else {
					durationHour := durationMin / 60
					durationMinRemainder := durationMin % 60
					holdingDuration = fmt.Sprintf(" | 持仓时长%d小时%d分钟", durationHour, durationMinRemainder)
				}
			}

			sb.WriteString(fmt.Sprintf("%d. %s %s | 入场价%.4f 当前价%.4f | 盈亏%+.2f%% | 杠杆%dx | 保证金%.0f | 强平价%.4f%s\n\n",
				i+1, pos.Symbol, strings.ToUpper(pos.Side),
				pos.EntryPrice, pos.MarkPrice, pos.UnrealizedPnLPct,
				pos.Leverage, pos.MarginUsed, pos.LiquidationPrice, holdingDuration))

			// 使用FormatMarketData输出完整市场数据
			if marketData, ok := ctx.MarketDataMap[pos.Symbol]; ok {
				sb.WriteString(market.Format(marketData))
				sb.WriteString("\n")
			}
		}
	} else {
		sb.WriteString("当前持仓: 无\n\n")
	}

	// 候选币种（完整市场数据）
	sb.WriteString(fmt.Sprintf("## 候选币种 (%d个)\n\n", len(ctx.MarketDataMap)))
	displayedCount := 0
	for _, coin := range ctx.CandidateCoins {
		marketData, hasData := ctx.MarketDataMap[coin.Symbol]
		if !hasData {
			continue
		}
		displayedCount++

		sourceTags := ""
		if len(coin.Sources) > 1 {
			sourceTags = " (AI500+OI_Top双重信号)"
		} else if len(coin.Sources) == 1 && coin.Sources[0] == "oi_top" {
			sourceTags = " (OI_Top持仓增长)"
		}

		// 使用FormatMarketData输出完整市场数据
		sb.WriteString(fmt.Sprintf("### %d. %s%s\n\n", displayedCount, coin.Symbol, sourceTags))
		sb.WriteString(market.Format(marketData))
		
		// 添加 Supertrend 交易信号分析
		// 调试信息：检查数据是否存在
		if marketData.SupertrendData == nil {
			sb.WriteString("⚠️  Supertrend 数据为 nil（数据未计算）\n\n")
		} else if marketData.VolumePriceData == nil {
			sb.WriteString("⚠️  量价关系数据为 nil（数据未计算）\n\n")
		} else if marketData.SupertrendData != nil && marketData.VolumePriceData != nil {
			// 显示 Supertrend 状态信息（短期策略：3m、5m、15m为核心，30m、1h、4h为参考）
			sb.WriteString("📊 Supertrend 多时间框架分析:\n")
			st := marketData.SupertrendData
			if st.Timeframe3m != nil {
				sb.WriteString(fmt.Sprintf("  3m (关键): %s (信号: %s) - 短期获利依赖3分钟信号\n", st.Timeframe3m.Trend, st.Timeframe3m.Signal))
			}
			if st.Timeframe5m != nil {
				sb.WriteString(fmt.Sprintf("  5m (核心): %s (信号: %s)\n", st.Timeframe5m.Trend, st.Timeframe5m.Signal))
			}
			if st.Timeframe15m != nil {
				sb.WriteString(fmt.Sprintf("  15m (核心): %s (信号: %s)\n", st.Timeframe15m.Trend, st.Timeframe15m.Signal))
			}
			if st.Timeframe30m != nil {
				sb.WriteString(fmt.Sprintf("  30m (确认): %s (信号: %s)\n", st.Timeframe30m.Trend, st.Timeframe30m.Signal))
			}
			if st.Timeframe1h != nil {
				sb.WriteString(fmt.Sprintf("  1h (大趋势): %s (信号: %s)\n", st.Timeframe1h.Trend, st.Timeframe1h.Signal))
			}
			if st.Timeframe4h != nil {
				sb.WriteString(fmt.Sprintf("  4h (参考): %s (信号: %s)\n", st.Timeframe4h.Trend, st.Timeframe4h.Signal))
			}
			
			// 显示量价关系
			vp := marketData.VolumePriceData
			sb.WriteString(fmt.Sprintf("  量价关系: %v (成交量比率: %.2f)\n", vp.PriceVolumeOK, vp.VolumeRatio3m))
			
			// 分析交易信号（传入完整市场数据以判断短期盈利优势）
			signal := analyzeSupertrendSignal(marketData.SupertrendData, marketData.VolumePriceData, marketData)
			if signal != "" {
				sb.WriteString(fmt.Sprintf("  ✅ 交易信号: %s\n\n", signal))
			} else {
				sb.WriteString("  ⚠️  当前不满足开仓条件（需要5m+15m一致，或15m+30m一致，或5m+30m一致，且1h或4h大趋势至少一个支持）\n\n")
			}
		} else {
			sb.WriteString("⚠️  Supertrend 数据未计算（可能K线数据不足）\n\n")
		}
		
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	// 夏普比率（直接传值，不要复杂格式化）
	if ctx.Performance != nil {
		// 直接从interface{}中提取SharpeRatio
		type PerformanceData struct {
			SharpeRatio float64 `json:"sharpe_ratio"`
		}
		var perfData PerformanceData
		if jsonData, err := json.Marshal(ctx.Performance); err == nil {
			if err := json.Unmarshal(jsonData, &perfData); err == nil {
				sb.WriteString(fmt.Sprintf("## 📊 夏普比率: %.2f\n\n", perfData.SharpeRatio))
			}
		}
	}

	sb.WriteString("---\n\n")
	sb.WriteString("现在请分析并输出决策（思维链 + JSON）\n")

	return sb.String()
}

// parseFullDecisionResponse 解析AI的完整决策响应
func parseFullDecisionResponse(aiResponse string, accountEquity float64, btcEthLeverage, altcoinLeverage int) (*FullDecision, error) {
	// 1. 提取思维链
	cotTrace := extractCoTTrace(aiResponse)

	// 2. 提取JSON决策列表
	decisions, err := extractDecisions(aiResponse)
	if err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: []Decision{},
		}, fmt.Errorf("提取决策失败: %w", err)
	}

	// 3. 验证决策
	if err := validateDecisions(decisions, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
		return &FullDecision{
			CoTTrace:  cotTrace,
			Decisions: decisions,
		}, fmt.Errorf("决策验证失败: %w", err)
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
	arrayStart := strings.Index(response, "[")
	if arrayStart == -1 {
		return nil, fmt.Errorf("无法找到JSON数组起始")
	}

	// 从 [ 开始，匹配括号找到对应的 ]
	arrayEnd := findMatchingBracket(response, arrayStart)
	if arrayEnd == -1 {
		return nil, fmt.Errorf("无法找到JSON数组结束")
	}

	jsonContent := strings.TrimSpace(response[arrayStart : arrayEnd+1])

	// 🔧 修复常见的JSON格式错误：缺少引号的字段值
	// 匹配: "reasoning": 内容"}  或  "reasoning": 内容}  (没有引号)
	// 修复为: "reasoning": "内容"}
	// 使用简单的字符串扫描而不是正则表达式
	jsonContent = fixMissingQuotes(jsonContent)

	// 解析JSON
	var decisions []Decision
	if err := json.Unmarshal([]byte(jsonContent), &decisions); err != nil {
		return nil, fmt.Errorf("JSON解析失败: %w\nJSON内容: %s", err, jsonContent)
	}

	return decisions, nil
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
	for i, decision := range decisions {
		if err := validateDecision(&decision, accountEquity, btcEthLeverage, altcoinLeverage); err != nil {
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

// validateDecision 验证单个决策的有效性
func validateDecision(d *Decision, accountEquity float64, btcEthLeverage, altcoinLeverage int) error {
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

		if d.Leverage <= 0 || d.Leverage > maxLeverage {
			return fmt.Errorf("杠杆必须在1-%d之间（%s，当前配置上限%d倍）: %d", maxLeverage, d.Symbol, maxLeverage, d.Leverage)
		}
		if d.PositionSizeUSD <= 0 {
			return fmt.Errorf("仓位大小必须大于0: %.2f", d.PositionSizeUSD)
		}
		// 验证仓位价值上限（加1%容差以避免浮点数精度问题）
		tolerance := maxPositionValue * 0.01 // 1%容差
		if d.PositionSizeUSD > maxPositionValue+tolerance {
			if d.Symbol == "BTCUSDT" || d.Symbol == "ETHUSDT" {
				return fmt.Errorf("BTC/ETH单币种仓位价值不能超过%.0f USDT（10倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			} else {
				return fmt.Errorf("山寨币单币种仓位价值不能超过%.0f USDT（1.5倍账户净值），实际: %.0f", maxPositionValue, d.PositionSizeUSD)
			}
		}
		if d.StopLoss <= 0 || d.TakeProfit <= 0 {
			return fmt.Errorf("止损和止盈必须大于0")
		}

		// 验证止损止盈的合理性
		if d.Action == "open_long" {
			if d.StopLoss >= d.TakeProfit {
				return fmt.Errorf("做多时止损价必须小于止盈价")
			}
		} else {
			if d.StopLoss <= d.TakeProfit {
				return fmt.Errorf("做空时止损价必须大于止盈价")
			}
		}

		// 验证风险回报比（必须≥1:3）
		// 计算入场价（假设当前市价）
		var entryPrice float64
		if d.Action == "open_long" {
			// 做多：入场价在止损和止盈之间
			entryPrice = d.StopLoss + (d.TakeProfit-d.StopLoss)*0.2 // 假设在20%位置入场
		} else {
			// 做空：入场价在止损和止盈之间
			entryPrice = d.StopLoss - (d.StopLoss-d.TakeProfit)*0.2 // 假设在20%位置入场
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

		// 硬约束：风险回报比必须≥3.0
		if riskRewardRatio < 3.0 {
			return fmt.Errorf("风险回报比过低(%.2f:1)，必须≥3.0:1 [风险:%.2f%% 收益:%.2f%%] [止损:%.2f 止盈:%.2f]",
				riskRewardRatio, riskPercent, rewardPercent, d.StopLoss, d.TakeProfit)
		}
	}

	return nil
}

// analyzeSupertrendSignal 分析 Supertrend 多时间框架信号
// 返回交易信号描述字符串，如果满足开仓条件则返回具体信号，否则返回空字符串
// 优化策略：优先15m+30m一致（最稳定），即使5m相反也可以开仓；增加短期盈利优势判断
func analyzeSupertrendSignal(st *market.SupertrendMultiTimeframe, vp *market.VolumePriceData, marketData *market.Data) string {
	if st == nil || vp == nil {
		return ""
	}

	var signals []string

	// 检查各个时间框架的数据是否存在（短期策略：5m和15m是必需的）
	if st.Timeframe5m == nil || st.Timeframe15m == nil {
		return ""
	}

	signal3m := ""
	if st.Timeframe3m != nil {
		signal3m = st.Timeframe3m.Signal
	}
	signal5m := st.Timeframe5m.Signal
	signal15m := st.Timeframe15m.Signal
	signal30m := ""
	if st.Timeframe30m != nil {
		signal30m = st.Timeframe30m.Signal
	}
	signal1h := ""
	if st.Timeframe1h != nil {
		signal1h = st.Timeframe1h.Signal
	}
	signal4h := ""
	if st.Timeframe4h != nil {
		signal4h = st.Timeframe4h.Signal
	}

	// 1. 优化策略：短期策略优先5分钟和15分钟（5分钟信号改变可能影响后续信号）
	// 策略优先级：5m+15m一致（优先，最敏感）> 15m+30m一致 > 5m+30m一致
	var signalDirection string
	var validSignals []string

	// 优先检查5m和15m是否一致（短期策略的核心，5分钟信号最重要）
	if signal5m != "none" && signal15m != "none" && signal5m == signal15m {
		signalDirection = signal5m
		validSignals = append(validSignals, "5m", "15m")
		// 3分钟信号对短期获利至关重要：如果相反，需要非常谨慎
		if signal3m != "none" && signal3m == signalDirection {
			validSignals = append(validSignals, "3m")
			signals = append(signals, fmt.Sprintf("✅ 3m信号(%s)与5m+15m一致，信号强化（3分钟信号对短期获利至关重要）", signal3m))
		} else if signal3m != "none" && signal3m != signalDirection {
			// 3分钟信号相反，需要非常谨慎（3分钟信号变化可能预示短期趋势变化）
			signals = append(signals, fmt.Sprintf("🔴 3m信号(%s)与5m+15m相反，3分钟信号变化需非常谨慎（短期获利依赖3分钟信号）", signal3m))
			// 3分钟信号相反时，降低信号强度，但不完全阻止（给用户决策空间）
		}
		// 30分钟信号作为确认
		if signal30m != "none" && signal30m != signalDirection {
			signals = append(signals, fmt.Sprintf("⚠️ 30m信号(%s)与5m+15m相反，但5m+15m为主信号", signal30m))
		} else if signal30m == signalDirection {
			validSignals = append(validSignals, "30m")
		}
	} else if signal15m != "none" && signal30m != "none" && signal15m == signal30m {
		// 其次检查15m和30m是否一致（备选方案）
		signalDirection = signal15m
		validSignals = append(validSignals, "15m", "30m")
		// 如果5m与15m+30m相反，标记为冲突（5分钟信号很重要，需要谨慎）
		if signal5m != "none" && signal5m != signalDirection {
			signals = append(signals, fmt.Sprintf("⚠️ 5m信号(%s)与15m+30m相反，5分钟信号变化可能影响后续，需谨慎", signal5m))
		} else if signal5m == signalDirection {
			validSignals = append(validSignals, "5m")
		}
		// 3分钟信号对短期获利至关重要：如果相反，需要非常谨慎
		if signal3m != "none" && signal3m == signalDirection {
			validSignals = append(validSignals, "3m")
			signals = append(signals, fmt.Sprintf("✅ 3m信号(%s)与15m+30m一致，信号强化（3分钟信号对短期获利至关重要）", signal3m))
		} else if signal3m != "none" && signal3m != signalDirection {
			// 3分钟信号相反，需要非常谨慎
			signals = append(signals, fmt.Sprintf("🔴 3m信号(%s)与15m+30m相反，3分钟信号变化需非常谨慎（短期获利依赖3分钟信号）", signal3m))
		}
	} else if signal5m != "none" && signal30m != "none" && signal5m == signal30m {
		// 最后检查5m和30m是否一致（备选方案）
		signalDirection = signal5m
		validSignals = append(validSignals, "5m", "30m")
		if signal15m != "none" && signal15m != signalDirection {
			signals = append(signals, fmt.Sprintf("⚠️ 15m信号(%s)与5m+30m相反，15分钟信号缺失确认", signal15m))
		} else if signal15m == signalDirection {
			validSignals = append(validSignals, "15m")
		}
		// 3分钟信号对短期获利至关重要：如果相反，需要非常谨慎
		if signal3m != "none" && signal3m == signalDirection {
			validSignals = append(validSignals, "3m")
			signals = append(signals, fmt.Sprintf("✅ 3m信号(%s)与5m+30m一致，信号强化（3分钟信号对短期获利至关重要）", signal3m))
		} else if signal3m != "none" && signal3m != signalDirection {
			// 3分钟信号相反，需要非常谨慎
			signals = append(signals, fmt.Sprintf("🔴 3m信号(%s)与5m+30m相反，3分钟信号变化需非常谨慎（短期获利依赖3分钟信号）", signal3m))
		}
	} else {
		// 没有任何两个时间框架一致，不满足条件
		return "" // 信号不足或不一致
	}

	// 2. 检查短期盈利优势（新增：判断是否有短期盈利潜力）
	if marketData != nil {
		hasAdvantage := false
		advantageReasons := []string{}
		advantageScore := 0
		
		// 做多优势判断
		if signalDirection == "long" {
			// RSI < 40 表示超卖，有反弹潜力
			if marketData.CurrentRSI7 < 40 {
				hasAdvantage = true
				advantageReasons = append(advantageReasons, fmt.Sprintf("RSI超卖(%.1f)", marketData.CurrentRSI7))
				advantageScore++
			}
			// MACD 负值但趋势向上（MACD值在改善）
			if marketData.CurrentMACD < 0 && marketData.IntradaySeries != nil && len(marketData.IntradaySeries.MACDValues) >= 2 {
				recentMACD := marketData.IntradaySeries.MACDValues[len(marketData.IntradaySeries.MACDValues)-1]
				prevMACD := marketData.IntradaySeries.MACDValues[len(marketData.IntradaySeries.MACDValues)-2]
				if recentMACD > prevMACD {
					hasAdvantage = true
					advantageReasons = append(advantageReasons, "MACD转强")
					advantageScore++
				}
			}
			// 动量对齐：价格相对EMA20（短线顺势更优：多看价>=EMA20）
			if marketData.CurrentPrice >= marketData.CurrentEMA20 {
				hasAdvantage = true
				advantageReasons = append(advantageReasons, "价格站上EMA20")
				advantageScore++
			}
			// RSI 短期回升（更偏向反弹持续）
			if marketData.IntradaySeries != nil && len(marketData.IntradaySeries.RSI7Values) >= 2 {
				rsiNow := marketData.IntradaySeries.RSI7Values[len(marketData.IntradaySeries.RSI7Values)-1]
				rsiPrev := marketData.IntradaySeries.RSI7Values[len(marketData.IntradaySeries.RSI7Values)-2]
				if rsiNow > rsiPrev {
					hasAdvantage = true
					advantageReasons = append(advantageReasons, "RSI走强")
					advantageScore++
				}
			}
		}
		
		// 做空优势判断
		if signalDirection == "short" {
			// RSI > 60 表示超买，有回调潜力
			if marketData.CurrentRSI7 > 60 {
				hasAdvantage = true
				advantageReasons = append(advantageReasons, fmt.Sprintf("RSI超买(%.1f)", marketData.CurrentRSI7))
				advantageScore++
			}
			// MACD 正值但趋势向下（MACD值在恶化）
			if marketData.CurrentMACD > 0 && marketData.IntradaySeries != nil && len(marketData.IntradaySeries.MACDValues) >= 2 {
				recentMACD := marketData.IntradaySeries.MACDValues[len(marketData.IntradaySeries.MACDValues)-1]
				prevMACD := marketData.IntradaySeries.MACDValues[len(marketData.IntradaySeries.MACDValues)-2]
				if recentMACD < prevMACD {
					hasAdvantage = true
					advantageReasons = append(advantageReasons, "MACD转弱")
					advantageScore++
				}
			}
			// 动量对齐：价格相对EMA20（空看价<=EMA20）
			if marketData.CurrentPrice <= marketData.CurrentEMA20 {
				hasAdvantage = true
				advantageReasons = append(advantageReasons, "价格跌破EMA20")
				advantageScore++
			}
			// RSI 短期走弱
			if marketData.IntradaySeries != nil && len(marketData.IntradaySeries.RSI7Values) >= 2 {
				rsiNow := marketData.IntradaySeries.RSI7Values[len(marketData.IntradaySeries.RSI7Values)-1]
				rsiPrev := marketData.IntradaySeries.RSI7Values[len(marketData.IntradaySeries.RSI7Values)-2]
				if rsiNow < rsiPrev {
					hasAdvantage = true
					advantageReasons = append(advantageReasons, "RSI走弱")
					advantageScore++
				}
			}
		}
		
		// 需要至少2项优势成立，提升短期胜率
		if hasAdvantage && advantageScore >= 2 {
			signals = append(signals, fmt.Sprintf("✅ 短期盈利优势：%s", strings.Join(advantageReasons, "、")))
		} else {
			// 优势不足，直接放弃信号，避免低质量短线
			return "" // 放弃低质量短线机会
		}
	}

	// 3. 检查大趋势（1小时为主，4小时为辅）：只要1小时或4小时其中一个与交易信号一致，就允许开仓
	// 如果1小时和4小时都与交易信号相反，则阻止开仓（更灵活，提高短期胜率）
	majorTrendMatch := false
	majorTrendInfo := []string{}
	
	// 检查1小时趋势（主要参考）
	if signal1h != "" && signal1h != "none" {
		if signal1h == signalDirection {
			majorTrendMatch = true
			majorTrendInfo = append(majorTrendInfo, fmt.Sprintf("✅ 1h趋势同向(%s)", signal1h))
		} else {
			majorTrendInfo = append(majorTrendInfo, fmt.Sprintf("⚠️ 1h趋势相反(%s)", signal1h))
		}
	}
	
	// 检查4小时趋势（辅助参考）
	if signal4h != "" && signal4h != "none" {
		if signal4h == signalDirection {
			majorTrendMatch = true
			majorTrendInfo = append(majorTrendInfo, fmt.Sprintf("✅ 4h趋势同向(%s)", signal4h))
		} else {
			majorTrendInfo = append(majorTrendInfo, fmt.Sprintf("⚠️ 4h趋势相反(%s)", signal4h))
		}
	}
	
	// 如果1小时和4小时都与交易信号相反，阻止开仓
	if signal1h != "" && signal1h != "none" && signal4h != "" && signal4h != "none" {
		if signal1h != signalDirection && signal4h != signalDirection {
			return "" // 1小时和4小时都相反，阻止开仓
		}
	} else if signal1h != "" && signal1h != "none" && signal4h == "" {
		// 只有1小时数据，必须与交易信号一致
		if signal1h != signalDirection {
			return "" // 1小时相反，阻止开仓
		}
	} else if signal1h == "" && signal4h != "" && signal4h != "none" {
		// 只有4小时数据，必须与交易信号一致
		if signal4h != signalDirection {
			return "" // 4小时相反，阻止开仓
		}
	}
	
	// 添加大趋势信息到信号列表
	if len(majorTrendInfo) > 0 {
		if majorTrendMatch {
			signals = append(signals, strings.Join(majorTrendInfo, " | ")+" | 大趋势支持")
		} else {
			signals = append(signals, strings.Join(majorTrendInfo, " | ")+" | 大趋势部分支持")
		}
	}

	// 4. 检查量价关系（放宽条件：只要不是明显不健康即可）
	// 如果量价关系不健康，给出警告但不阻止交易
	if !vp.PriceVolumeOK {
		signals = append(signals, "⚠️ 量价关系不够理想，但信号较强")
	}

	// 5. 检查成交量比率（放宽条件：只要不是极端低即可）
	volumeRatio := vp.VolumeRatio3m
	if volumeRatio < 0.3 {
		// 成交量比率极低，给出警告但不阻止交易
		signals = append(signals, fmt.Sprintf("⚠️ 成交量比率极低(%.2f)，流动性不足，需谨慎", volumeRatio))
	} else if volumeRatio < 0.5 {
		// 成交量比率较低，给出警告但不阻止交易
		signals = append(signals, fmt.Sprintf("⚠️ 成交量比率较低(%.2f)，建议谨慎", volumeRatio))
	} else if volumeRatio > 3.0 {
		// 成交量比率过高，可能是异常波动
		signals = append(signals, fmt.Sprintf("⚠️ 成交量比率较高(%.2f)，注意风险", volumeRatio))
	}

	// 如果所有条件都满足，生成交易信号
	if signalDirection == "long" {
		timeframeStr := strings.Join(validSignals, "、")
		signals = append(signals, fmt.Sprintf("✅ 做多信号：%s信号统一为做多", timeframeStr))
		return strings.Join(signals, " | ")
	} else if signalDirection == "short" {
		timeframeStr := strings.Join(validSignals, "、")
		signals = append(signals, fmt.Sprintf("✅ 做空信号：%s信号统一为做空", timeframeStr))
		return strings.Join(signals, " | ")
	}

	return "" // 不满足开仓条件
}


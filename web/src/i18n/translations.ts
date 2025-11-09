export type Language = 'en' | 'zh';

export const translations = {
  en: {
    // Header
    appTitle: 'AI Trading Competition',
    subtitle: 'Qwen vs DeepSeek · Real-time',
    competition: 'Competition',
    details: 'Details',
    running: 'RUNNING',
    stopped: 'STOPPED',

    // Footer
    footerTitle: 'NOFX - AI Trading Competition System',
    footerWarning: '⚠️ Trading involves risk. Use at your own discretion.',

    // Stats Cards
    totalEquity: 'Total Equity',
    availableBalance: 'Available Balance',
    totalPnL: 'Total P&L',
    positions: 'Positions',
    margin: 'Margin',
    free: 'Free',

    // Positions Table
    currentPositions: 'Current Positions',
    active: 'Active',
    symbol: 'Symbol',
    side: 'Side',
    entryPrice: 'Entry Price',
    markPrice: 'Mark Price',
    quantity: 'Quantity',
    positionValue: 'Position Value',
    leverage: 'Leverage',
    unrealizedPnL: 'Unrealized P&L',
    liqPrice: 'Liq. Price',
    long: 'LONG',
    short: 'SHORT',
    noPositions: 'No Positions',
    noActivePositions: 'No active trading positions',
    actions: 'Actions',
    manualClose: 'Close Position',
    closingPosition: 'Closing...',
    manualRefreshAI: 'Manual AI Refresh',
    manualRefreshing: 'Refreshing...',
    manualRefreshHint: 'Trigger a fresh AI cycle without waiting for the timer.',
    manualRefreshSuccess: 'AI decision cycle started.',
    manualCloseSuccess: '{symbol} close order sent.',
    confirmManualClose: 'Close {symbol} {side} position now?',
    manualActionError: 'Request failed: {error}',

    // Recent Decisions
    recentDecisions: 'Recent Decisions',
    lastCycles: 'Last {count} trading cycles',
    noDecisionsYet: 'No Decisions Yet',
    aiDecisionsWillAppear: 'AI trading decisions will appear here',
    cycle: 'Cycle',
    success: 'Success',
    failed: 'Failed',
    inputPrompt: 'Input Prompt',
    aiThinking: 'AI Chain of Thought',
    collapse: 'Collapse',
    expand: 'Expand',

    // Equity Chart
    accountEquityCurve: 'Account Equity Curve',
    noHistoricalData: 'No Historical Data',
    dataWillAppear: 'Equity curve will appear after running a few cycles',
    initialBalance: 'Initial Balance',
    currentEquity: 'Current Equity',
    historicalCycles: 'Historical Cycles',
    displayRange: 'Display Range',
    recent: 'Recent',
    allData: 'All Data',
    cycles: 'Cycles',

    // Competition Page
    aiCompetition: 'AI Competition',
    traders: 'traders',
    liveBattle: 'Qwen vs DeepSeek · Live Battle',
    leader: 'Leader',
    leaderboard: 'Leaderboard',
    live: 'LIVE',
    performanceComparison: 'Performance Comparison',
    realTimePnL: 'Real-time PnL %',
    headToHead: 'Head-to-Head Battle',
    leadingBy: 'Leading by {gap}%',
    behindBy: 'Behind by {gap}%',
    equity: 'Equity',
    pnl: 'P&L',
    pos: 'Pos',

    // AI Learning
    aiLearning: 'AI Learning & Reflection',
    tradesAnalyzed: '{count} trades analyzed · Real-time evolution',
    latestReflection: 'Latest Reflection',
    fullCoT: 'Full Chain of Thought',
    totalTrades: 'Total Trades',
    winRate: 'Win Rate',
    avgWin: 'Avg Win',
    avgLoss: 'Avg Loss',
    profitFactor: 'Profit Factor',
    avgWinDivLoss: 'Avg Win ÷ Avg Loss',
    excellent: '🔥 Excellent - Strong profitability',
    good: '✓ Good - Stable profits',
    fair: '⚠️ Fair - Needs optimization',
    poor: '❌ Poor - Losses exceed gains',
    bestPerformer: 'Best Performer',
    worstPerformer: 'Worst Performer',
    symbolPerformance: 'Symbol Performance',
    tradeHistory: 'Trade History',
    completedTrades: 'Recent {count} completed trades',
    noCompletedTrades: 'No completed trades yet',
    completedTradesWillAppear: 'Completed trades will appear here',
    entry: 'Entry',
    exit: 'Exit',
    stopLoss: 'Stop Loss',
    latest: 'Latest',

    // AI Learning Description
    howAILearns: 'How AI Learns & Evolves',
    aiLearningPoint1: 'Analyzes last 20 trading cycles before each decision',
    aiLearningPoint2: 'Identifies best & worst performing symbols',
    aiLearningPoint3: 'Optimizes position sizing based on win rate',
    aiLearningPoint4: 'Avoids repeating past mistakes',

    // Loading & Error
    loading: 'Loading...',
    loadingError: '⚠️ Failed to load AI learning data',
    noCompleteData: 'No complete trading data (needs to complete open → close cycle)',
  },
  zh: {
    // Header
    appTitle: 'AI交易竞赛',
    subtitle: 'Qwen vs DeepSeek · 实时',
    competition: '竞赛',
    details: '详情',
    running: '运行中',
    stopped: '已停止',

    // Footer
    footerTitle: 'NOFX - AI交易竞赛系统',
    footerWarning: '⚠️ 交易有风险，请谨慎使用。',

    // Stats Cards
    totalEquity: '总净值',
    availableBalance: '可用余额',
    totalPnL: '总盈亏',
    positions: '持仓',
    margin: '保证金',
    free: '空闲',

    // Positions Table
    currentPositions: '当前持仓',
    active: '活跃',
    symbol: '币种',
    side: '方向',
    entryPrice: '入场价',
    markPrice: '标记价',
    quantity: '数量',
    positionValue: '仓位价值',
    leverage: '杠杆',
    unrealizedPnL: '未实现盈亏',
    liqPrice: '强平价',
    long: '多头',
    short: '空头',
    noPositions: '无持仓',
    noActivePositions: '当前没有活跃的交易持仓',
    actions: '操作',
    manualClose: '手动平仓',
    closingPosition: '执行中...',
    manualRefreshAI: '手动刷新AI',
    manualRefreshing: '刷新中...',
    manualRefreshHint: '无需等待轮询，立即重新请求AI决策',
    manualRefreshSuccess: '已触发AI最新决策周期',
    manualCloseSuccess: '{symbol} 平仓指令已发送',
    confirmManualClose: '确定立即平掉 {symbol} {side} 仓位？',
    manualActionError: '请求失败：{error}',

    // Recent Decisions
    recentDecisions: '最近决策',
    lastCycles: '最近 {count} 个交易周期',
    noDecisionsYet: '暂无决策',
    aiDecisionsWillAppear: 'AI交易决策将显示在这里',
    cycle: '周期',
    success: '成功',
    failed: '失败',
    inputPrompt: '输入提示',
    aiThinking: '💭 AI思维链分析',
    collapse: '▼ 收起',
    expand: '▶ 展开',

    // Equity Chart
    accountEquityCurve: '账户净值曲线',
    noHistoricalData: '暂无历史数据',
    dataWillAppear: '运行几个周期后将显示收益率曲线',
    initialBalance: '初始余额',
    currentEquity: '当前净值',
    historicalCycles: '历史周期',
    displayRange: '显示范围',
    recent: '最近',
    allData: '全部数据',
    cycles: '个',

    // Competition Page
    aiCompetition: 'AI竞赛',
    traders: '位交易者',
    liveBattle: 'Qwen vs DeepSeek · 实时对战',
    leader: '🥇 领先者',
    leaderboard: '🥇 排行榜',
    live: '直播',
    performanceComparison: '📈 表现对比',
    realTimePnL: '实时盈亏百分比',
    headToHead: '⚔️ 正面对决',
    leadingBy: '领先 {gap}%',
    behindBy: '落后 {gap}%',
    equity: '净值',
    pnl: '盈亏',
    pos: '仓位',

    // AI Learning
    aiLearning: 'AI学习与反思',
    tradesAnalyzed: '已分析 {count} 笔交易 · 实时演化',
    latestReflection: '最新反思',
    fullCoT: '📋 完整思维链',
    totalTrades: '总交易数',
    winRate: '胜率',
    avgWin: '平均盈利',
    avgLoss: '平均亏损',
    profitFactor: '盈亏比',
    avgWinDivLoss: '平均盈利 ÷ 平均亏损',
    excellent: '🔥 优秀 - 盈利能力强',
    good: '✓ 良好 - 稳定盈利',
    fair: '⚠️ 一般 - 需要优化',
    poor: '❌ 较差 - 亏损超过盈利',
    bestPerformer: '最佳表现',
    worstPerformer: '最差表现',
    symbolPerformance: '📊 币种表现',
    tradeHistory: '历史成交',
    completedTrades: '最近 {count} 笔已完成交易',
    noCompletedTrades: '暂无完成的交易',
    completedTradesWillAppear: '已完成的交易将显示在这里',
    entry: '入场',
    exit: '出场',
    stopLoss: '止损',
    latest: '最新',

    // AI Learning Description
    howAILearns: '💡 AI如何学习和进化',
    aiLearningPoint1: '每次决策前分析最近20个交易周期',
    aiLearningPoint2: '识别表现最好和最差的币种',
    aiLearningPoint3: '根据胜率优化仓位大小',
    aiLearningPoint4: '避免重复过去的错误',

    // Loading & Error
    loading: '加载中...',
    loadingError: '⚠️ 加载AI学习数据失败',
    noCompleteData: '暂无完整交易数据（需要完成开仓→平仓的完整周期）',
  }
};

export function t(key: string, lang: Language, params?: Record<string, string | number>): string {
  let text = translations[lang][key as keyof typeof translations['en']] || key;

  // Replace parameters like {count}, {gap}, etc.
  if (params) {
    Object.entries(params).forEach(([param, value]) => {
      text = text.replace(`{${param}}`, String(value));
    });
  }

  return text;
}

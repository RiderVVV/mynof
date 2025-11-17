export interface SystemStatus {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  is_running: boolean;
  start_time: string;
  runtime_minutes: number;
  call_count: number;
  initial_balance: number;
  scan_interval: string;
  stop_until: string;
  last_reset_time: string;
  ai_provider: string;
  entry_mode?: string;
  entry_working_type?: string;
  entry_price_protect?: boolean;
  entry_timeout_minutes?: number;
  pending_entries?: PendingEntry[];
}

export interface AccountInfo {
  total_equity: number;
  wallet_balance: number;
  unrealized_profit: number;
  available_balance: number;
  total_pnl: number;
  total_pnl_pct: number;
  total_unrealized_pnl: number;
  initial_balance: number;
  daily_pnl: number;
  position_count: number;
  margin_used: number;
  margin_used_pct: number;
}

export interface Position {
  symbol: string;
  side: string;
  entry_price: number;
  mark_price: number;
  quantity: number;
  leverage: number;
  unrealized_pnl: number;
  unrealized_pnl_pct: number;
  liquidation_price: number;
  margin_used: number;
}

export interface DecisionAction {
  action: string;
  symbol: string;
  quantity: number;
  leverage: number;
  price: number;
  order_id: number;
  timestamp: string;
  success: boolean;
  error?: string;
  entry_type?: string;
  entry_price?: number;
  entry_algo_id?: number;
  entry_client_algo_id?: string;
  entry_status?: string;
}

export interface PendingEntry {
  symbol: string;
  side: string;
  algo_id: number;
  client_algo_id: string;
  order_type: string;
  trigger_price?: number;
  limit_price?: number;
  quantity: number;
  status?: string;
  created_at?: string;
  expires_at?: string;
  working_type?: string;
  price_protect?: boolean;
  min_hold_minutes?: number;
}

export interface AccountSnapshot {
  total_balance: number;
  available_balance: number;
  total_unrealized_profit: number;
  position_count: number;
  margin_used_pct: number;
}

export interface DecisionRecord {
  timestamp: string;
  cycle_number: number;
  input_prompt: string;
  cot_trace: string;
  decision_json: string;
  account_state: AccountSnapshot;
  positions: any[];
  candidate_coins: string[];
  decisions: DecisionAction[];
  execution_log: string[];
  success: boolean;
  error_message?: string;
}

export interface Statistics {
  total_cycles: number;
  successful_cycles: number;
  failed_cycles: number;
  total_open_positions: number;
  total_close_positions: number;
}

// 新增：竞赛相关类型
export interface TraderInfo {
  trader_id: string;
  trader_name: string;
  ai_model: string;
}

export interface CompetitionTraderData {
  trader_id: string;
  trader_name: string;
  ai_model: string;
  total_equity: number;
  total_pnl: number;
  total_pnl_pct: number;
  position_count: number;
  margin_used_pct: number;
  call_count: number;
  is_running: boolean;
}

export interface CompetitionData {
  traders: CompetitionTraderData[];
  count: number;
}

export interface ConsultationSettings {
  trader_id: string;
  symbols: string[];
  leverage: number;
  balance: number;
  updated_at?: string;
}

export interface ConsultationPayload {
  trader_id: string;
  symbols: string[];
  leverage: number;
  balance: number;
}

export interface ConsultationTpTarget {
  price?: number;
  size_pct?: number;
  size_usd?: number;
  kind?: string;
}

export interface ConsultationDecision {
  symbol: string;
  action: string;
  leverage?: number;
  position_size_usd?: number;
  stop_loss?: number;
  take_profit?: number;
  tp_targets?: ConsultationTpTarget[];
  strategy_hint?: string;
  confidence?: number;
  risk_usd?: number;
  reasoning: string;
  entry_type?: string;
  entry_price?: number;
  entry_limit_price?: number;
  entry_activation_price?: number;
  entry_timeout_minutes?: number;
}

export interface ConsultationResult {
  trader_id: string;
  timestamp: string;
  symbols: string[];
  leverage: number;
  balance: number;
  decisions: ConsultationDecision[];
  cot_trace: string;
  prompt?: string;
}

export interface AutoModeInfo {
  trader_id: string;
  auto_mode_enabled: boolean;
}

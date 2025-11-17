package consult

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nofx/market"
	"nofx/trader"

	_ "modernc.org/sqlite"
)

const maxConsultationRecords = 100

// Preferences describes persisted consultation settings for a trader.
type Preferences struct {
	TraderID  string    `json:"trader_id"`
	Symbols   []string  `json:"symbols"`
	Leverage  int       `json:"leverage"`
	Balance   float64   `json:"balance"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store wraps the SQLite connection for consultation settings.
type Store struct {
	db *sql.DB
}

// Record 表示一次咨询模式请求的持久化结果。
type Record struct {
	ID        int64                      `json:"record_id"`
	TraderID  string                     `json:"trader_id"`
	Symbols   []string                   `json:"symbols"`
	Leverage  int                        `json:"leverage"`
	Balance   float64                    `json:"balance"`
	Result    *trader.ConsultationResult `json:"result"`
	Note      string                     `json:"note"`
	CreatedAt time.Time                  `json:"created_at"`
}

// NewStore opens (and creates if necessary) the SQLite database used to persist consultation settings.
func NewStore(dbPath string) (*Store, error) {
	if dbPath == "" {
		dbPath = filepath.Join("data", "consultation.db")
	}

	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("创建咨询模式数据库目录失败: %w", err)
	}

	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on", filepath.ToSlash(dbPath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开咨询模式数据库失败: %w", err)
	}

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close terminates the underlying database connection.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func initSchema(db *sql.DB) error {
	const settingsDDL = `
	CREATE TABLE IF NOT EXISTS consultation_settings (
		trader_id TEXT PRIMARY KEY,
		symbols   TEXT NOT NULL DEFAULT '[]',
		leverage  INTEGER NOT NULL DEFAULT 5,
		balance   REAL NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL
	);`

	if _, err := db.Exec(settingsDDL); err != nil {
		return fmt.Errorf("初始化咨询模式数据表失败: %w", err)
	}

	const recordsDDL = `
	CREATE TABLE IF NOT EXISTS consultation_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		trader_id TEXT NOT NULL,
		symbols TEXT NOT NULL DEFAULT '[]',
		leverage INTEGER NOT NULL DEFAULT 5,
		balance REAL NOT NULL DEFAULT 0,
		result_json TEXT NOT NULL,
		user_note TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL
	);`
	if _, err := db.Exec(recordsDDL); err != nil {
		return fmt.Errorf("初始化咨询模式历史表失败: %w", err)
	}

	const addNoteColumn = `ALTER TABLE consultation_records ADD COLUMN user_note TEXT NOT NULL DEFAULT ''`
	if _, err := db.Exec(addNoteColumn); err != nil {
		if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return fmt.Errorf("升级咨询模式历史表失败: %w", err)
		}
	}

	const indexDDL = `
	CREATE INDEX IF NOT EXISTS idx_consult_records_trader_created
	ON consultation_records(trader_id, created_at DESC);`
	if _, err := db.Exec(indexDDL); err != nil {
		return fmt.Errorf("初始化咨询模式历史索引失败: %w", err)
	}
	return nil
}

// GetPreferences returns the saved settings for the provided trader. When no rows exist it returns (nil, nil).
func (s *Store) GetPreferences(traderID string) (*Preferences, error) {
	if traderID == "" {
		return nil, fmt.Errorf("trader_id 不能为空")
	}

	const query = `SELECT symbols, leverage, balance, updated_at FROM consultation_settings WHERE trader_id = ?`
	row := s.db.QueryRow(query, traderID)

	var (
		rawSymbols string
		leverage   int
		balance    float64
		updatedAt  time.Time
	)

	if err := row.Scan(&rawSymbols, &leverage, &balance, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("读取咨询模式配置失败: %w", err)
	}

	var symbols []string
	if err := json.Unmarshal([]byte(rawSymbols), &symbols); err != nil {
		// 解析异常时忽略并返回空列表
		symbols = nil
	}

	return &Preferences{
		TraderID:  traderID,
		Symbols:   cleanSymbols(symbols),
		Leverage:  leverage,
		Balance:   balance,
		UpdatedAt: updatedAt,
	}, nil
}

// SavePreferences upserts the consultation settings for a trader.
func (s *Store) SavePreferences(p Preferences) (*Preferences, error) {
	if p.TraderID == "" {
		return nil, fmt.Errorf("trader_id 不能为空")
	}

	symbolsJSON, err := json.Marshal(cleanSymbols(p.Symbols))
	if err != nil {
		return nil, fmt.Errorf("序列化交易对列表失败: %w", err)
	}

	leverage := p.Leverage
	if leverage <= 0 {
		leverage = 5
	}

	now := time.Now()
	const upsert = `
	INSERT INTO consultation_settings (trader_id, symbols, leverage, balance, updated_at)
	VALUES (?, ?, ?, ?, ?)
	ON CONFLICT(trader_id) DO UPDATE SET
		symbols=excluded.symbols,
		leverage=excluded.leverage,
		balance=excluded.balance,
		updated_at=excluded.updated_at
	`

	if _, err := s.db.Exec(upsert, p.TraderID, string(symbolsJSON), leverage, p.Balance, now); err != nil {
		return nil, fmt.Errorf("保存咨询模式配置失败: %w", err)
	}

	p.Symbols = cleanSymbols(p.Symbols)
	p.Leverage = leverage
	p.UpdatedAt = now
	return &p, nil
}

// AppendRecord 保存一次新的咨询结果。
func (s *Store) AppendRecord(traderID string, result *trader.ConsultationResult, note string) (*Record, error) {
	if traderID == "" {
		return nil, fmt.Errorf("trader_id 不能为空")
	}
	if result == nil {
		return nil, fmt.Errorf("result 不能为空")
	}

	symbolsJSON, err := json.Marshal(cleanSymbols(result.Symbols))
	if err != nil {
		return nil, fmt.Errorf("序列化咨询交易对失败: %w", err)
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("序列化咨询结果失败: %w", err)
	}

	createdAt := result.Timestamp
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	const stmt = `
	INSERT INTO consultation_records (trader_id, symbols, leverage, balance, result_json, user_note, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	`
	trimmedNote := strings.TrimSpace(note)
	res, err := s.db.Exec(stmt, traderID, string(symbolsJSON), result.Leverage, result.Balance, string(resultJSON), trimmedNote, createdAt)
	if err != nil {
		return nil, fmt.Errorf("保存咨询记录失败: %w", err)
	}

	recordID, _ := res.LastInsertId()
	record := &Record{
		ID:        recordID,
		TraderID:  traderID,
		Symbols:   cleanSymbols(result.Symbols),
		Leverage:  result.Leverage,
		Balance:   result.Balance,
		Result:    cloneConsultationResult(result),
		Note:      trimmedNote,
		CreatedAt: createdAt,
	}

	if err := s.pruneRecords(traderID, maxConsultationRecords); err != nil {
		return nil, err
	}
	return record, nil
}

// GetLatestRecord 返回最新的一条咨询记录。
func (s *Store) GetLatestRecord(traderID string) (*Record, error) {
	records, err := s.ListRecords(traderID, 1)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, nil
	}
	return records[0], nil
}

// ListRecords 获取最近limit条咨询记录。
func (s *Store) ListRecords(traderID string, limit int) ([]*Record, error) {
	if traderID == "" {
		return nil, fmt.Errorf("trader_id 不能为空")
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	const queryTemplate = `
	SELECT id, symbols, leverage, balance, result_json, user_note, created_at
	FROM consultation_records
	WHERE trader_id = ?
	ORDER BY created_at DESC, id DESC
	LIMIT ?
	`

	rows, err := s.db.Query(queryTemplate, traderID, limit)
	if err != nil {
		return nil, fmt.Errorf("读取咨询记录失败: %w", err)
	}
	defer rows.Close()

	var records []*Record
	for rows.Next() {
		var (
			id         int64
			rawSymbols string
			leverage   int
			balance    float64
			resultJSON string
			noteText   string
			createdAt  time.Time
		)

		if err := rows.Scan(&id, &rawSymbols, &leverage, &balance, &resultJSON, &noteText, &createdAt); err != nil {
			return nil, fmt.Errorf("解析咨询记录失败: %w", err)
		}

		record, err := buildRecord(traderID, id, rawSymbols, leverage, balance, resultJSON, noteText, createdAt)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("迭代咨询记录失败: %w", err)
	}
	return records, nil
}

func buildRecord(traderID string, id int64, rawSymbols string, leverage int, balance float64, resultJSON string, note string, createdAt time.Time) (*Record, error) {
	var symbols []string
	if err := json.Unmarshal([]byte(rawSymbols), &symbols); err != nil {
		symbols = nil
	}

	var result trader.ConsultationResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return nil, fmt.Errorf("解析咨询结果失败: %w", err)
	}
	if result.Timestamp.IsZero() {
		result.Timestamp = createdAt
	}

	return &Record{
		ID:        id,
		TraderID:  traderID,
		Symbols:   cleanSymbols(symbols),
		Leverage:  leverage,
		Balance:   balance,
		Result:    cloneConsultationResult(&result),
		Note:      strings.TrimSpace(note),
		CreatedAt: createdAt,
	}, nil
}

func cloneConsultationResult(res *trader.ConsultationResult) *trader.ConsultationResult {
	if res == nil {
		return nil
	}
	data, err := json.Marshal(res)
	if err != nil {
		return res
	}
	var clone trader.ConsultationResult
	if err := json.Unmarshal(data, &clone); err != nil {
		return res
	}
	return &clone
}

func (s *Store) pruneRecords(traderID string, maxRecords int) error {
	if maxRecords <= 0 {
		return nil
	}

	const cleanup = `
	DELETE FROM consultation_records
	WHERE trader_id = ?
		AND id NOT IN (
			SELECT id FROM consultation_records
			WHERE trader_id = ?
			ORDER BY created_at DESC, id DESC
			LIMIT ?
		)
	`

	if _, err := s.db.Exec(cleanup, traderID, traderID, maxRecords); err != nil {
		return fmt.Errorf("清理咨询记录失败: %w", err)
	}
	return nil
}

func cleanSymbols(input []string) []string {
	if len(input) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))

	for _, sym := range input {
		normalized := market.Normalize(sym)
		if normalized == "" || normalized == "USDT" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// NormalizeSymbols exposes the same normalization logic for other packages.
func NormalizeSymbols(input []string) []string {
	cleaned := cleanSymbols(input)
	if len(cleaned) == 0 {
		return nil
	}
	return append([]string(nil), cleaned...)
}

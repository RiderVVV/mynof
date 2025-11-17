package consult

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"nofx/market"

	_ "modernc.org/sqlite"
)

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
	const ddl = `
	CREATE TABLE IF NOT EXISTS consultation_settings (
		trader_id TEXT PRIMARY KEY,
		symbols   TEXT NOT NULL DEFAULT '[]',
		leverage  INTEGER NOT NULL DEFAULT 5,
		balance   REAL NOT NULL DEFAULT 0,
		updated_at DATETIME NOT NULL
	);`

	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("初始化咨询模式数据表失败: %w", err)
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

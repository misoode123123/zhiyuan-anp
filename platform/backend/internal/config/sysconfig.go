package config

import (
	"context"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
)

// ConfigItem 系统配置项（业务配置入库，从系统配置页管理）。
type ConfigItem struct {
	Key         string    `json:"key" db:"key"`
	Value       string    `json:"value" db:"value"`
	Category    string    `json:"category" db:"category"`
	Description string    `json:"description" db:"description"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Store 系统配置仓库：从 DB 加载到内存缓存，支持热更新。
// 设计：基础运行配置（DB连接/端口等）留 .env；业务配置（模型/key/opencode 等）存此。
type Store struct {
	db    *sqlx.DB
	mu    sync.RWMutex
	cache map[string]string
}

// NewStore 构造 Store。
func NewStore(db *sqlx.DB) *Store {
	return &Store{db: db, cache: map[string]string{}}
}

// Load 从 DB 加载全部配置到缓存。
func (s *Store) Load(ctx context.Context) error {
	var items []ConfigItem
	if err := s.db.SelectContext(ctx, &items, `SELECT key, value, category, description, updated_at FROM system_config`); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = map[string]string{}
	for _, it := range items {
		s.cache[it.Key] = it.Value
	}
	return nil
}

// Get 读取配置，缺失返回 fallback。
func (s *Store) Get(key, fallback string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if v, ok := s.cache[key]; ok {
		return v
	}
	return fallback
}

// All 返回全部配置项（给系统配置页）。
func (s *Store) All() []ConfigItem {
	var items []ConfigItem
	_ = s.db.Select(&items, `SELECT key, value, category, description, updated_at FROM system_config ORDER BY category, key`)
	return items
}

// Set upsert 配置并刷新缓存。
func (s *Store) Set(ctx context.Context, key, value, category, description string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO system_config (key, value, category, description)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT(key) DO UPDATE SET
		   value=excluded.value, category=excluded.category,
		   description=excluded.description, updated_at=CURRENT_TIMESTAMP`,
		key, value, category, description)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cache[key] = value
	s.mu.Unlock()
	return nil
}

// SeedIfEmpty 若 system_config 为空，用 defaults 首次播种（从基础 env 迁移业务配置入库）。
// defaults: key -> {value, category}
func (s *Store) SeedIfEmpty(ctx context.Context, defaults map[string][2]string) error {
	var n int
	if err := s.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM system_config`); err != nil {
		return err
	}
	if n > 0 {
		return s.Load(ctx)
	}
	for k, vc := range defaults {
		if err := s.Set(ctx, k, vc[0], vc[1], ""); err != nil {
			return err
		}
	}
	return s.Load(ctx)
}

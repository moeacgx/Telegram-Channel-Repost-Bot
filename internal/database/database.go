package database

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"tg-channel-repost-bot/pkg/config"

	_ "github.com/mattn/go-sqlite3"
)

// DB represents the database connection
type DB struct {
	*sql.DB
}

// New creates a new database connection
func New(cfg *config.DatabaseConfig) (*DB, error) {
	db, err := sql.Open(cfg.Driver, cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	// Test connection
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{db}, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.DB.Close()
}

// Migrate runs database migrations
func (db *DB) Migrate() error {
	migrations := []string{
		createChannelGroupsTable,
		createChannelsTable,
		createMessageTemplatesTable,
		createSendRecordsTable,
		createRetryConfigsTable,
		createIndexes,
		// addEntitiesFieldToMessageTemplates, // Already added manually
	}

	// Run additional migrations that might fail if column already exists
	additionalMigrations := []string{
		addAutoPinFieldToChannelGroups,
		addScheduleFieldsToChannelGroups,
	}

	for _, migration := range additionalMigrations {
		if _, err := db.Exec(migration); err != nil {
			// Log but don't fail if column already exists
			log.Printf("Migration warning (may be expected): %v", err)
		}
	}

	for _, migration := range migrations {
		if _, err := db.Exec(migration); err != nil {
			return fmt.Errorf("failed to run migration: %w", err)
		}
	}

	return nil
}

const createChannelGroupsTable = `
CREATE TABLE IF NOT EXISTS channel_groups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    description TEXT,
    message_id INTEGER,
    frequency INTEGER NOT NULL DEFAULT 60,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    auto_pin BOOLEAN NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

const createChannelsTable = `
CREATE TABLE IF NOT EXISTS channels (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id TEXT NOT NULL,
    channel_name TEXT,
    group_id INTEGER NOT NULL,
    last_message_id TEXT,
    is_active BOOLEAN NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE CASCADE,
    UNIQUE(channel_id, group_id)
);`

const createMessageTemplatesTable = `
CREATE TABLE IF NOT EXISTS message_templates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    content TEXT NOT NULL,
    message_type TEXT NOT NULL DEFAULT 'text',
    media_url TEXT,
    buttons TEXT, -- JSON format
    entities TEXT, -- JSON format for message entities
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

const createSendRecordsTable = `
CREATE TABLE IF NOT EXISTS send_records (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL,
    channel_id TEXT NOT NULL,
    message_id TEXT,
    message_type TEXT NOT NULL, -- 'repost' or 'push'
    status TEXT NOT NULL DEFAULT 'pending',
    error_message TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    scheduled_at DATETIME NOT NULL,
    sent_at DATETIME,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE CASCADE
);`

const createRetryConfigsTable = `
CREATE TABLE IF NOT EXISTS retry_configs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    group_id INTEGER NOT NULL UNIQUE,
    max_retries INTEGER NOT NULL DEFAULT 3,
    retry_interval INTEGER NOT NULL DEFAULT 300,
    time_range_start TEXT, -- HH:MM format
    time_range_end TEXT,   -- HH:MM format
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (group_id) REFERENCES channel_groups(id) ON DELETE CASCADE
);`

const createIndexes = `
CREATE INDEX IF NOT EXISTS idx_channels_group_id ON channels(group_id);
CREATE INDEX IF NOT EXISTS idx_channels_channel_id ON channels(channel_id);
CREATE INDEX IF NOT EXISTS idx_send_records_group_id ON send_records(group_id);
CREATE INDEX IF NOT EXISTS idx_send_records_channel_id ON send_records(channel_id);
CREATE INDEX IF NOT EXISTS idx_send_records_status ON send_records(status);
CREATE INDEX IF NOT EXISTS idx_send_records_scheduled_at ON send_records(scheduled_at);
CREATE INDEX IF NOT EXISTS idx_retry_configs_group_id ON retry_configs(group_id);
`

const addEntitiesFieldToMessageTemplates = `
-- Add entities field only if it doesn't exist
ALTER TABLE message_templates ADD COLUMN entities TEXT DEFAULT '' WHERE NOT EXISTS (
    SELECT 1 FROM pragma_table_info('message_templates') WHERE name = 'entities'
);
`

const addAutoPinFieldToChannelGroups = `
-- Add auto_pin field to channel_groups table if it doesn't exist
ALTER TABLE channel_groups ADD COLUMN auto_pin BOOLEAN NOT NULL DEFAULT 0;
`

const addScheduleFieldsToChannelGroups = `
-- Add schedule_mode field to channel_groups table if it doesn't exist
ALTER TABLE channel_groups ADD COLUMN schedule_mode TEXT NOT NULL DEFAULT 'frequency';
-- Add schedule_timepoints field to channel_groups table if it doesn't exist
ALTER TABLE channel_groups ADD COLUMN schedule_timepoints TEXT DEFAULT '[]';
`

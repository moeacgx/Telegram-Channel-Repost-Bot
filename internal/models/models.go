package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ScheduleMode represents the scheduling mode for channel groups
type ScheduleMode string

const (
	ScheduleModeFrequency  ScheduleMode = "frequency"  // Every X minutes
	ScheduleModeTimepoints ScheduleMode = "timepoints" // At specific times
)

// TimePoint represents a specific time point for scheduling
type TimePoint struct {
	Hour   int `json:"hour"`   // 0-23
	Minute int `json:"minute"` // 0-59
}

// TimePoints represents a list of time points
type TimePoints []TimePoint

// Value implements driver.Valuer interface for database storage
func (tp TimePoints) Value() (driver.Value, error) {
	if tp == nil {
		return nil, nil
	}
	return json.Marshal(tp)
}

// Scan implements sql.Scanner interface for database retrieval
func (tp *TimePoints) Scan(value interface{}) error {
	if value == nil {
		*tp = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("cannot scan %T into TimePoints", value)
	}

	return json.Unmarshal(bytes, tp)
}

// ChannelGroup represents a group of channels
type ChannelGroup struct {
	ID                 int64        `json:"id" db:"id"`
	Name               string       `json:"name" db:"name"`
	Description        string       `json:"description" db:"description"`
	MessageID          int64        `json:"message_id" db:"message_id"`
	Frequency          int          `json:"frequency" db:"frequency"`                     // in minutes (for frequency mode)
	ScheduleMode       ScheduleMode `json:"schedule_mode" db:"schedule_mode"`             // scheduling mode
	ScheduleTimepoints TimePoints   `json:"schedule_timepoints" db:"schedule_timepoints"` // time points for timepoints mode
	IsActive           bool         `json:"is_active" db:"is_active"`
	AutoPin            bool         `json:"auto_pin" db:"auto_pin"` // Auto pin messages after sending
	CreatedAt          time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time    `json:"updated_at" db:"updated_at"`
}

// Channel represents a Telegram channel
type Channel struct {
	ID            int64     `json:"id" db:"id"`
	ChannelID     string    `json:"channel_id" db:"channel_id"`     // Telegram channel ID
	ChannelName   string    `json:"channel_name" db:"channel_name"` // Channel name/username
	GroupID       int64     `json:"group_id" db:"group_id"`
	LastMessageID string    `json:"last_message_id" db:"last_message_id"` // Last repost message ID
	IsActive      bool      `json:"is_active" db:"is_active"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// MessageTemplate represents a message template
type MessageTemplate struct {
	ID          int64          `json:"id" db:"id"`
	Title       string         `json:"title" db:"title"`
	Content     string         `json:"content" db:"content"`
	MessageType MessageType    `json:"message_type" db:"message_type"`
	MediaURL    string         `json:"media_url" db:"media_url"`
	Buttons     InlineKeyboard `json:"buttons" db:"buttons"`
	Entities    string         `json:"entities" db:"entities"` // JSON序列化的entities
	CreatedAt   time.Time      `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at" db:"updated_at"`
}

// SendRecord represents a message send record
type SendRecord struct {
	ID           int64      `json:"id" db:"id"`
	GroupID      int64      `json:"group_id" db:"group_id"`
	ChannelID    string     `json:"channel_id" db:"channel_id"`
	MessageID    string     `json:"message_id" db:"message_id"` // Telegram message ID
	MessageType  SendType   `json:"message_type" db:"message_type"`
	Status       SendStatus `json:"status" db:"status"`
	ErrorMessage *string    `json:"error_message" db:"error_message"`
	RetryCount   int        `json:"retry_count" db:"retry_count"`
	ScheduledAt  time.Time  `json:"scheduled_at" db:"scheduled_at"`
	SentAt       *time.Time `json:"sent_at" db:"sent_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at" db:"updated_at"`
}

// RetryConfig represents retry configuration for a channel group
type RetryConfig struct {
	ID             int64     `json:"id" db:"id"`
	GroupID        int64     `json:"group_id" db:"group_id"`
	MaxRetries     int       `json:"max_retries" db:"max_retries"`
	RetryInterval  int       `json:"retry_interval" db:"retry_interval"`     // in seconds
	TimeRangeStart string    `json:"time_range_start" db:"time_range_start"` // HH:MM format
	TimeRangeEnd   string    `json:"time_range_end" db:"time_range_end"`     // HH:MM format
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// MessageType represents the type of message
type MessageType string

const (
	MessageTypeText     MessageType = "text"
	MessageTypePhoto    MessageType = "photo"
	MessageTypeVideo    MessageType = "video"
	MessageTypeDocument MessageType = "document"
	MessageTypeAudio    MessageType = "audio"
)

// SendType represents the type of send operation
type SendType string

const (
	SendTypeRepost SendType = "repost" // 重发
	SendTypePush   SendType = "push"   // 推送
)

// SendStatus represents the status of a send operation
type SendStatus string

const (
	SendStatusPending SendStatus = "pending"
	SendStatusSent    SendStatus = "sent"
	SendStatusFailed  SendStatus = "failed"
	SendStatusRetry   SendStatus = "retry"
)

// InlineKeyboard represents Telegram inline keyboard
type InlineKeyboard [][]InlineKeyboardButton

// InlineKeyboardButton represents a button in inline keyboard
type InlineKeyboardButton struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
}

// Value implements driver.Valuer interface for database storage
func (ik InlineKeyboard) Value() (driver.Value, error) {
	if len(ik) == 0 {
		return nil, nil
	}
	return json.Marshal(ik)
}

// Scan implements sql.Scanner interface for database retrieval
func (ik *InlineKeyboard) Scan(value interface{}) error {
	if value == nil {
		*ik = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("cannot scan %T into InlineKeyboard", value)
	}

	return json.Unmarshal(bytes, ik)
}

// ChannelGroupWithDetails represents a channel group with its related data
type ChannelGroupWithDetails struct {
	ChannelGroup
	Channels    []Channel       `json:"channels"`
	Message     MessageTemplate `json:"message"`
	RetryConfig RetryConfig     `json:"retry_config"`
}

// SendStatistics represents send statistics for a channel group
type SendStatistics struct {
	GroupID      int64      `json:"group_id"`
	GroupName    string     `json:"group_name"`
	TotalSent    int        `json:"total_sent"`
	TotalFailed  int        `json:"total_failed"`
	TotalPending int        `json:"total_pending"`
	LastSentAt   *time.Time `json:"last_sent_at"`
}

// StringPtr returns a pointer to the given string
func StringPtr(s string) *string {
	return &s
}

// StringValue returns the value of a string pointer, or empty string if nil
func StringValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

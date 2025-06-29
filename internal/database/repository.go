package database

import (
	"database/sql"
	"fmt"
	"time"

	"tg-channel-repost-bot/internal/models"
)

// Repository provides database operations
type Repository struct {
	db *DB
}

// NewRepository creates a new repository
func NewRepository(db *DB) *Repository {
	return &Repository{db: db}
}

// ChannelGroup operations

// CreateChannelGroup creates a new channel group
func (r *Repository) CreateChannelGroup(group *models.ChannelGroup) error {
	// Set default values if not specified
	if group.ScheduleMode == "" {
		group.ScheduleMode = models.ScheduleModeFrequency
	}
	if group.ScheduleTimepoints == nil {
		group.ScheduleTimepoints = models.TimePoints{}
	}

	query := `
		INSERT INTO channel_groups (name, description, message_id, frequency, schedule_mode, schedule_timepoints, is_active, auto_pin)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	result, err := r.db.Exec(query, group.Name, group.Description, group.MessageID, group.Frequency, group.ScheduleMode, group.ScheduleTimepoints, group.IsActive, group.AutoPin)
	if err != nil {
		return fmt.Errorf("failed to create channel group: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	group.ID = id
	return nil
}

// GetChannelGroup gets a channel group by ID
func (r *Repository) GetChannelGroup(id int64) (*models.ChannelGroup, error) {
	query := `
		SELECT id, name, description, message_id, frequency, schedule_mode, schedule_timepoints, is_active, auto_pin, created_at, updated_at
		FROM channel_groups
		WHERE id = ?
	`
	var group models.ChannelGroup
	err := r.db.QueryRow(query, id).Scan(
		&group.ID, &group.Name, &group.Description, &group.MessageID,
		&group.Frequency, &group.ScheduleMode, &group.ScheduleTimepoints, &group.IsActive, &group.AutoPin, &group.CreatedAt, &group.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("channel group not found")
		}
		return nil, fmt.Errorf("failed to get channel group: %w", err)
	}

	return &group, nil
}

// GetChannelGroups gets all channel groups
func (r *Repository) GetChannelGroups() ([]models.ChannelGroup, error) {
	query := `
		SELECT id, name, description, message_id, frequency, schedule_mode, schedule_timepoints, is_active, auto_pin, created_at, updated_at
		FROM channel_groups
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel groups: %w", err)
	}
	defer rows.Close()

	var groups []models.ChannelGroup
	for rows.Next() {
		var group models.ChannelGroup
		err := rows.Scan(
			&group.ID, &group.Name, &group.Description, &group.MessageID,
			&group.Frequency, &group.ScheduleMode, &group.ScheduleTimepoints, &group.IsActive, &group.AutoPin, &group.CreatedAt, &group.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan channel group: %w", err)
		}
		groups = append(groups, group)
	}

	return groups, nil
}

// UpdateChannelGroup updates a channel group
func (r *Repository) UpdateChannelGroup(group *models.ChannelGroup) error {
	query := `
		UPDATE channel_groups
		SET name = ?, description = ?, message_id = ?, frequency = ?, schedule_mode = ?, schedule_timepoints = ?, is_active = ?, auto_pin = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, group.Name, group.Description, group.MessageID, group.Frequency, group.ScheduleMode, group.ScheduleTimepoints, group.IsActive, group.AutoPin, group.ID)
	if err != nil {
		return fmt.Errorf("failed to update channel group: %w", err)
	}

	return nil
}

// DeleteChannelGroup deletes a channel group
func (r *Repository) DeleteChannelGroup(id int64) error {
	query := `DELETE FROM channel_groups WHERE id = ?`
	_, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete channel group: %w", err)
	}

	return nil
}

// Channel operations

// CreateChannel creates a new channel
func (r *Repository) CreateChannel(channel *models.Channel) error {
	query := `
		INSERT INTO channels (channel_id, channel_name, group_id, last_message_id, is_active)
		VALUES (?, ?, ?, ?, ?)
	`
	result, err := r.db.Exec(query, channel.ChannelID, channel.ChannelName, channel.GroupID, channel.LastMessageID, channel.IsActive)
	if err != nil {
		return fmt.Errorf("failed to create channel: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	channel.ID = id
	return nil
}

// GetChannelsByGroupID gets all channels for a group
func (r *Repository) GetChannelsByGroupID(groupID int64) ([]models.Channel, error) {
	query := `
		SELECT id, channel_id, channel_name, group_id, last_message_id, is_active, created_at, updated_at
		FROM channels
		WHERE group_id = ? AND is_active = 1
		ORDER BY created_at ASC
	`
	rows, err := r.db.Query(query, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to get channels: %w", err)
	}
	defer rows.Close()

	var channels []models.Channel
	for rows.Next() {
		var channel models.Channel
		err := rows.Scan(
			&channel.ID, &channel.ChannelID, &channel.ChannelName, &channel.GroupID,
			&channel.LastMessageID, &channel.IsActive, &channel.CreatedAt, &channel.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan channel: %w", err)
		}
		channels = append(channels, channel)
	}

	return channels, nil
}

// UpdateChannelLastMessageID updates the last message ID for a channel
func (r *Repository) UpdateChannelLastMessageID(channelID string, messageID string) error {
	query := `
		UPDATE channels
		SET last_message_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE channel_id = ?
	`
	_, err := r.db.Exec(query, messageID, channelID)
	if err != nil {
		return fmt.Errorf("failed to update channel last message ID: %w", err)
	}

	return nil
}

// DeleteChannel deletes a channel
func (r *Repository) DeleteChannel(id int64) error {
	query := `DELETE FROM channels WHERE id = ?`
	_, err := r.db.Exec(query, id)
	if err != nil {
		return fmt.Errorf("failed to delete channel: %w", err)
	}

	return nil
}

// MessageTemplate operations

// CreateMessageTemplate creates a new message template
func (r *Repository) CreateMessageTemplate(template *models.MessageTemplate) error {
	query := `
		INSERT INTO message_templates (title, content, message_type, media_url, buttons, entities)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	result, err := r.db.Exec(query, template.Title, template.Content, template.MessageType, template.MediaURL, template.Buttons, template.Entities)
	if err != nil {
		return fmt.Errorf("failed to create message template: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	template.ID = id
	return nil
}

// GetMessageTemplate gets a message template by ID
func (r *Repository) GetMessageTemplate(id int64) (*models.MessageTemplate, error) {
	query := `
		SELECT id, title, content, message_type, media_url, buttons, entities, created_at, updated_at
		FROM message_templates
		WHERE id = ?
	`
	var template models.MessageTemplate
	err := r.db.QueryRow(query, id).Scan(
		&template.ID, &template.Title, &template.Content, &template.MessageType,
		&template.MediaURL, &template.Buttons, &template.Entities, &template.CreatedAt, &template.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("message template not found")
		}
		return nil, fmt.Errorf("failed to get message template: %w", err)
	}

	return &template, nil
}

// UpdateMessageTemplateContent updates the content of a message template
func (r *Repository) UpdateMessageTemplateContent(id int64, content string) error {
	query := `
		UPDATE message_templates
		SET content = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, content, id)
	if err != nil {
		return fmt.Errorf("failed to update message template content: %w", err)
	}

	return nil
}

// UpdateMessageTemplateContentAndEntities updates the content and entities of a message template
func (r *Repository) UpdateMessageTemplateContentAndEntities(id int64, content, entities string) error {
	query := `
		UPDATE message_templates
		SET content = ?, entities = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, content, entities, id)
	if err != nil {
		return fmt.Errorf("failed to update message template content and entities: %w", err)
	}

	return nil
}

// UpdateMessageTemplateComplete updates all fields of a message template
func (r *Repository) UpdateMessageTemplateComplete(id int64, content, messageType, mediaURL, entities string) error {
	query := `
		UPDATE message_templates
		SET content = ?, message_type = ?, media_url = ?, entities = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, content, messageType, mediaURL, entities, id)
	if err != nil {
		return fmt.Errorf("failed to update message template: %w", err)
	}

	return nil
}

// UpdateMessageTemplateButtons updates the buttons of a message template
func (r *Repository) UpdateMessageTemplateButtons(id int64, buttons models.InlineKeyboard) error {
	query := `
		UPDATE message_templates
		SET buttons = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, buttons, id)
	if err != nil {
		return fmt.Errorf("failed to update message template buttons: %w", err)
	}

	return nil
}

// UpdateChannelGroupName updates the name of a channel group
func (r *Repository) UpdateChannelGroupName(id int64, name string) error {
	query := `
		UPDATE channel_groups
		SET name = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, name, id)
	if err != nil {
		return fmt.Errorf("failed to update channel group name: %w", err)
	}

	return nil
}

// UpdateChannelGroupDescription updates the description of a channel group
func (r *Repository) UpdateChannelGroupDescription(id int64, description string) error {
	query := `
		UPDATE channel_groups
		SET description = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, description, id)
	if err != nil {
		return fmt.Errorf("failed to update channel group description: %w", err)
	}

	return nil
}

// UpdateChannelGroupFrequency updates the frequency of a channel group
func (r *Repository) UpdateChannelGroupFrequency(id int64, frequency int) error {
	query := `
		UPDATE channel_groups
		SET frequency = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, frequency, id)
	if err != nil {
		return fmt.Errorf("failed to update channel group frequency: %w", err)
	}

	return nil
}

// UpdateChannelGroupStatus updates the status (active/inactive) of a channel group
func (r *Repository) UpdateChannelGroupStatus(id int64, isActive bool) error {
	query := `
		UPDATE channel_groups
		SET is_active = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, isActive, id)
	if err != nil {
		return fmt.Errorf("failed to update channel group status: %w", err)
	}

	return nil
}

// UpdateChannelGroupAutoPin updates the auto pin setting of a channel group
func (r *Repository) UpdateChannelGroupAutoPin(id int64, autoPin bool) error {
	query := `
		UPDATE channel_groups
		SET auto_pin = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, autoPin, id)
	if err != nil {
		return fmt.Errorf("failed to update channel group auto pin: %w", err)
	}

	return nil
}

// SendRecord operations

// CreateSendRecord creates a new send record
func (r *Repository) CreateSendRecord(record *models.SendRecord) error {
	query := `
		INSERT INTO send_records (group_id, channel_id, message_id, message_type, status, scheduled_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`
	result, err := r.db.Exec(query, record.GroupID, record.ChannelID, record.MessageID, record.MessageType, record.Status, record.ScheduledAt)
	if err != nil {
		return fmt.Errorf("failed to create send record: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert id: %w", err)
	}

	record.ID = id
	return nil
}

// UpdateSendRecord updates a send record
func (r *Repository) UpdateSendRecord(record *models.SendRecord) error {
	query := `
		UPDATE send_records
		SET message_id = ?, status = ?, error_message = ?, retry_count = ?, sent_at = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`
	_, err := r.db.Exec(query, record.MessageID, record.Status, record.ErrorMessage, record.RetryCount, record.SentAt, record.ID)
	if err != nil {
		return fmt.Errorf("failed to update send record: %w", err)
	}

	return nil
}

// GetPendingSendRecords gets all pending send records
func (r *Repository) GetPendingSendRecords() ([]models.SendRecord, error) {
	query := `
		SELECT id, group_id, channel_id, message_id, message_type, status, error_message, retry_count, scheduled_at, sent_at, created_at, updated_at
		FROM send_records
		WHERE status IN ('pending', 'retry') AND scheduled_at <= ?
		ORDER BY scheduled_at ASC
	`
	rows, err := r.db.Query(query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to get pending send records: %w", err)
	}
	defer rows.Close()

	var records []models.SendRecord
	for rows.Next() {
		var record models.SendRecord
		err := rows.Scan(
			&record.ID, &record.GroupID, &record.ChannelID, &record.MessageID, &record.MessageType,
			&record.Status, &record.ErrorMessage, &record.RetryCount, &record.ScheduledAt,
			&record.SentAt, &record.CreatedAt, &record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan send record: %w", err)
		}
		records = append(records, record)
	}

	return records, nil
}

// GetPendingSendRecordsByGroupAndChannel gets pending send records for a specific group and channel
func (r *Repository) GetPendingSendRecordsByGroupAndChannel(groupID int64, channelID string) ([]models.SendRecord, error) {
	query := `
		SELECT id, group_id, channel_id, message_id, message_type, status, error_message, retry_count, scheduled_at, sent_at, created_at, updated_at
		FROM send_records
		WHERE group_id = ? AND channel_id = ? AND status IN ('pending', 'retry')
		ORDER BY created_at DESC
	`
	rows, err := r.db.Query(query, groupID, channelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending send records: %w", err)
	}
	defer rows.Close()

	var records []models.SendRecord
	for rows.Next() {
		var record models.SendRecord
		err := rows.Scan(
			&record.ID, &record.GroupID, &record.ChannelID, &record.MessageID, &record.MessageType,
			&record.Status, &record.ErrorMessage, &record.RetryCount, &record.ScheduledAt,
			&record.SentAt, &record.CreatedAt, &record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan send record: %w", err)
		}
		records = append(records, record)
	}

	return records, nil
}

// GetSendRecordsByGroupID gets send records for a group
func (r *Repository) GetSendRecordsByGroupID(groupID int64, limit int) ([]models.SendRecord, error) {
	query := `
		SELECT id, group_id, channel_id, message_id, message_type, status, error_message, retry_count, scheduled_at, sent_at, created_at, updated_at
		FROM send_records
		WHERE group_id = ?
		ORDER BY created_at DESC
		LIMIT ?
	`
	rows, err := r.db.Query(query, groupID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get send records: %w", err)
	}
	defer rows.Close()

	var records []models.SendRecord
	for rows.Next() {
		var record models.SendRecord
		err := rows.Scan(
			&record.ID, &record.GroupID, &record.ChannelID, &record.MessageID, &record.MessageType,
			&record.Status, &record.ErrorMessage, &record.RetryCount, &record.ScheduledAt,
			&record.SentAt, &record.CreatedAt, &record.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan send record: %w", err)
		}
		records = append(records, record)
	}

	return records, nil
}

// CleanupDuplicatePendingRecords removes duplicate pending records for the same group and channel
func (r *Repository) CleanupDuplicatePendingRecords() error {
	// First, get count of pending records
	var count int
	countQuery := `SELECT COUNT(*) FROM send_records WHERE status = 'pending' AND message_type = 'repost'`
	r.db.QueryRow(countQuery).Scan(&count)
	fmt.Printf("Found %d pending repost records before cleanup\n", count)

	// Delete ALL pending repost records - we'll recreate them as needed
	query := `DELETE FROM send_records WHERE status = 'pending' AND message_type = 'repost'`
	result, err := r.db.Exec(query)
	if err != nil {
		return fmt.Errorf("failed to cleanup pending records: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected > 0 {
		fmt.Printf("Cleaned up %d pending repost records\n", rowsAffected)
	}

	return nil
}

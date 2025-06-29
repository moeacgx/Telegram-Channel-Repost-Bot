package services

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"tg-channel-repost-bot/internal/database"
	"tg-channel-repost-bot/internal/models"
	"tg-channel-repost-bot/pkg/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// MessageService handles message operations
type MessageService struct {
	api    *tgbotapi.BotAPI
	repo   *database.Repository
	config *config.Config
}

// NewMessageService creates a new message service
func NewMessageService(api *tgbotapi.BotAPI, repo *database.Repository, config *config.Config) *MessageService {
	return &MessageService{
		api:    api,
		repo:   repo,
		config: config,
	}
}

// SendRepost sends a repost message to all channels in a group
func (s *MessageService) SendRepost(groupID int64) error {
	// Get channel group
	group, err := s.repo.GetChannelGroup(groupID)
	if err != nil {
		return fmt.Errorf("failed to get channel group: %w", err)
	}

	if !group.IsActive {
		return fmt.Errorf("channel group is not active")
	}

	// Get message template
	template, err := s.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get message template: %w", err)
	}

	// Get channels
	channels, err := s.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		return fmt.Errorf("failed to get channels: %w", err)
	}

	// Send to each channel
	for _, channel := range channels {
		if err := s.sendRepostToChannel(channel, template); err != nil {
			log.Printf("Failed to send repost to channel %s: %v", channel.ChannelID, err)
			// Record failure
			s.recordSendFailure(groupID, channel.ChannelID, models.SendTypeRepost, err.Error())
		}
	}

	return nil
}

// SendPush sends a push message to all channels in a group
func (s *MessageService) SendPush(groupID int64) error {
	// Get channel group
	group, err := s.repo.GetChannelGroup(groupID)
	if err != nil {
		return fmt.Errorf("failed to get channel group: %w", err)
	}

	if !group.IsActive {
		return fmt.Errorf("channel group is not active")
	}

	// Get message template
	template, err := s.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		return fmt.Errorf("failed to get message template: %w", err)
	}

	// Get channels
	channels, err := s.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		return fmt.Errorf("failed to get channels: %w", err)
	}

	// Send to each channel
	for _, channel := range channels {
		if err := s.sendPushToChannel(channel, template); err != nil {
			log.Printf("Failed to send push to channel %s: %v", channel.ChannelID, err)
			// Record failure
			s.recordSendFailure(groupID, channel.ChannelID, models.SendTypePush, err.Error())
		}
	}

	return nil
}

// DeleteGroupMessages deletes all messages for a channel group
func (s *MessageService) DeleteGroupMessages(groupID int64) error {
	// Get channels
	channels, err := s.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		return fmt.Errorf("failed to get channels: %w", err)
	}

	// Delete messages from each channel
	for _, channel := range channels {
		if err := s.deleteChannelMessages(channel); err != nil {
			log.Printf("Failed to delete messages from channel %s: %v", channel.ChannelID, err)
		}
	}

	return nil
}

// SendMessage sends a message to a channel (exported wrapper)
func (s *MessageService) SendMessage(channelID string, template *models.MessageTemplate) (string, error) {
	return s.sendMessage(channelID, template)
}

// DeleteMessage deletes a message from a channel (exported wrapper)
func (s *MessageService) DeleteMessage(channelID, messageID string) error {
	return s.deleteMessage(channelID, messageID)
}

// SendMessageWithEntities sends a message with entities to a channel
func (s *MessageService) SendMessageWithEntities(channelID, content string, entities []tgbotapi.MessageEntity) (string, error) {
	log.Printf("SendMessageWithEntities called for channel %s with %d entities", channelID, len(entities))

	chatID, err := strconv.ParseInt(channelID, 10, 64)
	if err != nil {
		// Try as username
		chatID = 0
	}

	var msg tgbotapi.Chattable

	if chatID != 0 {
		textMsg := tgbotapi.NewMessage(chatID, content)
		if entities != nil && len(entities) > 0 {
			log.Printf("Setting %d entities for numeric chat ID %d", len(entities), chatID)
			textMsg.Entities = entities
		}
		textMsg.DisableWebPagePreview = true // 关闭URL预览
		msg = textMsg
	} else {
		textMsg := tgbotapi.NewMessageToChannel(channelID, content)
		if entities != nil && len(entities) > 0 {
			log.Printf("Setting %d entities for channel username %s", len(entities), channelID)
			textMsg.Entities = entities
		}
		textMsg.DisableWebPagePreview = true // 关闭URL预览
		msg = textMsg
	}

	sentMsg, err := s.api.Send(msg)
	if err != nil {
		return "", fmt.Errorf("failed to send message to channel %s: %w", channelID, err)
	}

	log.Printf("Message sent successfully with ID %d", sentMsg.MessageID)
	return strconv.Itoa(sentMsg.MessageID), nil
}

// SendMessageWithTemplate sends a message with template (including buttons) and entities to a channel
func (s *MessageService) SendMessageWithTemplate(channelID string, template *models.MessageTemplate, entities []tgbotapi.MessageEntity) (string, error) {
	log.Printf("SendMessageWithTemplate called for channel %s with %d entities and %d button rows, message type: %s", channelID, len(entities), len(template.Buttons), template.MessageType)

	// Debug: Log content and entities details
	log.Printf("Message content: '%s' (length: %d bytes)", template.Content, len([]byte(template.Content)))
	if len(entities) > 0 {
		for i, entity := range entities {
			log.Printf("Entity %d: type=%s, offset=%d, length=%d, url=%s", i, entity.Type, entity.Offset, entity.Length, entity.URL)
			// Check if entity offset/length is within content bounds
			contentBytes := []byte(template.Content)
			if entity.Offset >= 0 && entity.Offset < len(contentBytes) && entity.Offset+entity.Length <= len(contentBytes) {
				entityText := string(contentBytes[entity.Offset : entity.Offset+entity.Length])
				log.Printf("Entity %d text: '%s'", i, entityText)
			} else {
				log.Printf("WARNING: Entity %d has invalid offset/length for content", i)
			}
		}
	}

	chatID, err := strconv.ParseInt(channelID, 10, 64)
	if err != nil {
		// Try as username
		chatID = 0
	}

	var msg tgbotapi.Chattable

	// Handle different message types
	switch template.MessageType {
	case models.MessageTypePhoto:
		if chatID != 0 {
			photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(template.MediaURL))
			photoMsg.Caption = template.Content
			// Use entities for formatting, no ParseMode needed
			if entities != nil && len(entities) > 0 {
				log.Printf("Setting %d caption entities for photo message to chat ID %d", len(entities), chatID)
				photoMsg.CaptionEntities = entities
			}
			if len(template.Buttons) > 0 {
				photoMsg.ReplyMarkup = s.createInlineKeyboard(template.Buttons)
				log.Printf("Added %d button rows to photo message", len(template.Buttons))
			}
			msg = photoMsg
		} else {
			photoMsg := tgbotapi.NewPhotoToChannel(channelID, tgbotapi.FileID(template.MediaURL))
			photoMsg.Caption = template.Content
			// Use entities for formatting, no ParseMode needed
			if entities != nil && len(entities) > 0 {
				log.Printf("Setting %d caption entities for photo message to channel %s", len(entities), channelID)
				photoMsg.CaptionEntities = entities
			}
			if len(template.Buttons) > 0 {
				photoMsg.ReplyMarkup = s.createInlineKeyboard(template.Buttons)
				log.Printf("Added %d button rows to photo message", len(template.Buttons))
			}
			msg = photoMsg
		}

	default: // MessageTypeText
		if chatID != 0 {
			textMsg := tgbotapi.NewMessage(chatID, template.Content)
			// Use entities for formatting, no ParseMode needed
			if entities != nil && len(entities) > 0 {
				log.Printf("Setting %d entities for text message to chat ID %d", len(entities), chatID)
				textMsg.Entities = entities
			}
			textMsg.DisableWebPagePreview = true // 关闭URL预览
			if len(template.Buttons) > 0 {
				textMsg.ReplyMarkup = s.createInlineKeyboard(template.Buttons)
				log.Printf("Added %d button rows to text message", len(template.Buttons))
			}
			msg = textMsg
		} else {
			textMsg := tgbotapi.NewMessageToChannel(channelID, template.Content)
			// Use entities for formatting, no ParseMode needed
			if entities != nil && len(entities) > 0 {
				log.Printf("Setting %d entities for text message to channel %s", len(entities), channelID)
				textMsg.Entities = entities
			}
			textMsg.DisableWebPagePreview = true // 关闭URL预览
			if len(template.Buttons) > 0 {
				textMsg.ReplyMarkup = s.createInlineKeyboard(template.Buttons)
				log.Printf("Added %d button rows to text message", len(template.Buttons))
			}
			msg = textMsg
		}
	}

	sentMsg, err := s.api.Send(msg)
	if err != nil {
		return "", fmt.Errorf("failed to send message to channel %s: %w", channelID, err)
	}

	log.Printf("Message sent successfully with ID %d", sentMsg.MessageID)
	return strconv.Itoa(sentMsg.MessageID), nil
}

// SendMediaGroup sends a media group to a channel
func (s *MessageService) SendMediaGroup(channelID string, mediaURLs []string, mediaTypes []string, caption string) error {
	if len(mediaURLs) == 0 {
		return fmt.Errorf("no media URLs provided")
	}

	if len(mediaURLs) != len(mediaTypes) {
		return fmt.Errorf("media URLs and types count mismatch")
	}

	chatID, err := strconv.ParseInt(channelID, 10, 64)
	if err != nil {
		// Try as username
		chatID = 0
	}

	// Create media group
	var mediaGroup []interface{}

	for i, mediaURL := range mediaURLs {
		mediaType := mediaTypes[i]

		switch mediaType {
		case "photo":
			media := tgbotapi.NewInputMediaPhoto(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				// Add caption to first media item
				media.Caption = caption
				media.ParseMode = "Markdown"
			}
			mediaGroup = append(mediaGroup, media)

		case "video":
			media := tgbotapi.NewInputMediaVideo(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				// Add caption to first media item
				media.Caption = caption
				media.ParseMode = "Markdown"
			}
			mediaGroup = append(mediaGroup, media)

		case "document":
			media := tgbotapi.NewInputMediaDocument(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				// Add caption to first media item
				media.Caption = caption
				media.ParseMode = "Markdown"
			}
			mediaGroup = append(mediaGroup, media)

		default:
			// Default to photo for unknown types
			media := tgbotapi.NewInputMediaPhoto(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				media.Caption = caption
				media.ParseMode = "Markdown"
			}
			mediaGroup = append(mediaGroup, media)
		}
	}

	// Send media group
	var mediaGroupConfig tgbotapi.MediaGroupConfig
	if chatID != 0 {
		mediaGroupConfig = tgbotapi.NewMediaGroup(chatID, mediaGroup)
	} else {
		mediaGroupConfig = tgbotapi.MediaGroupConfig{
			ChannelUsername: channelID,
			Media:           mediaGroup,
		}
	}

	_, err = s.api.SendMediaGroup(mediaGroupConfig)
	if err != nil {
		return fmt.Errorf("failed to send media group to channel %s: %w", channelID, err)
	}

	log.Printf("Successfully sent media group with %d items to channel %s", len(mediaURLs), channelID)
	return nil
}

// SendMediaGroupWithEntities sends a media group to a channel with entities
func (s *MessageService) SendMediaGroupWithEntities(channelID string, mediaURLs []string, mediaTypes []string, caption string, entities []tgbotapi.MessageEntity) error {
	if len(mediaURLs) == 0 {
		return fmt.Errorf("no media URLs provided")
	}

	if len(mediaURLs) != len(mediaTypes) {
		return fmt.Errorf("media URLs and types count mismatch")
	}

	log.Printf("SendMediaGroupWithEntities: caption='%s', entities count=%d", caption, len(entities))
	if len(entities) > 0 {
		for i, entity := range entities {
			log.Printf("Entity %d: type=%s, offset=%d, length=%d, url=%s", i, entity.Type, entity.Offset, entity.Length, entity.URL)
		}
	}

	chatID, err := strconv.ParseInt(channelID, 10, 64)
	if err != nil {
		// Try as username
		chatID = 0
	}

	// Create media group
	var mediaGroup []interface{}

	// Only add caption and entities to the first media item
	for i, mediaURL := range mediaURLs {
		mediaType := mediaTypes[i]

		switch mediaType {
		case "photo":
			media := tgbotapi.NewInputMediaPhoto(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				// Add caption to first media item with entities
				media.Caption = caption
				// IMPORTANT: Don't set ParseMode when using entities
				if len(entities) > 0 {
					media.CaptionEntities = entities
					log.Printf("Set %d entities to first photo media item", len(entities))
				}
			}
			mediaGroup = append(mediaGroup, media)

		case "video":
			media := tgbotapi.NewInputMediaVideo(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				// Add caption to first media item with entities
				media.Caption = caption
				// IMPORTANT: Don't set ParseMode when using entities
				if len(entities) > 0 {
					media.CaptionEntities = entities
					log.Printf("Set %d entities to first video media item", len(entities))
				}
			}
			mediaGroup = append(mediaGroup, media)

		case "document":
			media := tgbotapi.NewInputMediaDocument(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				// Add caption to first media item with entities
				media.Caption = caption
				// IMPORTANT: Don't set ParseMode when using entities
				if len(entities) > 0 {
					media.CaptionEntities = entities
					log.Printf("Set %d entities to first document media item", len(entities))
				}
			}
			mediaGroup = append(mediaGroup, media)

		default:
			// Default to photo for unknown types
			media := tgbotapi.NewInputMediaPhoto(tgbotapi.FileID(mediaURL))
			if i == 0 && caption != "" {
				media.Caption = caption
				// IMPORTANT: Don't set ParseMode when using entities
				if len(entities) > 0 {
					media.CaptionEntities = entities
					log.Printf("Set %d entities to first default media item", len(entities))
				}
			}
			mediaGroup = append(mediaGroup, media)
		}
	}

	// Send media group
	var mediaGroupConfig tgbotapi.MediaGroupConfig
	if chatID != 0 {
		mediaGroupConfig = tgbotapi.NewMediaGroup(chatID, mediaGroup)
	} else {
		mediaGroupConfig = tgbotapi.MediaGroupConfig{
			ChannelUsername: channelID,
			Media:           mediaGroup,
		}
	}

	log.Printf("Sending media group with %d items to channel %s", len(mediaGroup), channelID)
	_, err = s.api.SendMediaGroup(mediaGroupConfig)
	if err != nil {
		return fmt.Errorf("failed to send media group with entities to channel %s: %w", channelID, err)
	}

	log.Printf("Successfully sent media group with %d items and entities to channel %s", len(mediaURLs), channelID)
	return nil
}

// sendRepostToChannel sends a repost message to a specific channel
func (s *MessageService) sendRepostToChannel(channel models.Channel, template *models.MessageTemplate) error {
	// Delete previous message if exists
	if channel.LastMessageID != "" {
		if err := s.deleteMessage(channel.ChannelID, channel.LastMessageID); err != nil {
			log.Printf("Failed to delete previous message in channel %s: %v", channel.ChannelID, err)
		}
	}

	// Send new message
	messageID, err := s.sendMessage(channel.ChannelID, template)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	// Update last message ID
	if err := s.repo.UpdateChannelLastMessageID(channel.ChannelID, messageID); err != nil {
		log.Printf("Failed to update last message ID for channel %s: %v", channel.ChannelID, err)
	}

	// Record success
	s.recordSendSuccess(channel.GroupID, channel.ChannelID, messageID, models.SendTypeRepost)

	return nil
}

// sendPushToChannel sends a push message to a specific channel
func (s *MessageService) sendPushToChannel(channel models.Channel, template *models.MessageTemplate) error {
	// Send message (don't delete previous)
	messageID, err := s.sendMessage(channel.ChannelID, template)
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	// Record success
	s.recordSendSuccess(channel.GroupID, channel.ChannelID, messageID, models.SendTypePush)

	return nil
}

// sendMessage sends a message to a channel based on template
func (s *MessageService) sendMessage(channelID string, template *models.MessageTemplate) (string, error) {
	// Parse entities from template if they exist
	var entities []tgbotapi.MessageEntity
	if template.Entities != "" {
		log.Printf("Deserializing entities from template %d: %s", template.ID, template.Entities)
		if err := json.Unmarshal([]byte(template.Entities), &entities); err != nil {
			log.Printf("Failed to deserialize entities for template %d: %v", template.ID, err)
			entities = nil
		} else {
			log.Printf("Successfully deserialized %d entities for template %d", len(entities), template.ID)
			for i, entity := range entities {
				log.Printf("Deserialized entity %d: type=%s, offset=%d, length=%d, url=%s", i, entity.Type, entity.Offset, entity.Length, entity.URL)
			}
		}
	} else {
		log.Printf("No entities found in template %d", template.ID)
	}

	// Use the enhanced SendMessageWithTemplate method that properly handles entities
	return s.SendMessageWithTemplate(channelID, template, entities)
}

// deleteMessage deletes a message from a channel
func (s *MessageService) deleteMessage(channelID, messageID string) error {
	chatID, err := strconv.ParseInt(channelID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid channel ID: %s", channelID)
	}

	msgID, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %s", messageID)
	}

	deleteMsg := tgbotapi.NewDeleteMessage(chatID, msgID)
	_, err = s.api.Request(deleteMsg)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	return nil
}

// PinMessage pins a message in a channel and deletes the pin notification
func (s *MessageService) PinMessage(channelID string, messageID string) error {
	chatID, err := strconv.ParseInt(channelID, 10, 64)
	if err != nil {
		// Try as username
		chatID = 0
	}

	msgID, err := strconv.Atoi(messageID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}

	var pinConfig tgbotapi.PinChatMessageConfig
	if chatID != 0 {
		pinConfig = tgbotapi.PinChatMessageConfig{
			ChatID:              chatID,
			MessageID:           msgID,
			DisableNotification: true, // Pin silently to avoid spam
		}
	} else {
		pinConfig = tgbotapi.PinChatMessageConfig{
			ChannelUsername:     channelID,
			MessageID:           msgID,
			DisableNotification: true, // Pin silently to avoid spam
		}
	}

	// Pin the message
	_, err = s.api.Request(pinConfig)
	if err != nil {
		return fmt.Errorf("failed to pin message in channel %s: %w", channelID, err)
	}

	log.Printf("Successfully pinned message %s in channel %s", messageID, channelID)

	// Delete the pin notification message (next message ID after pinned message)
	// Pin notification always gets the next sequential message ID
	go s.deletePinNotificationPrecise(channelID, chatID, msgID+1)
	return nil
}

// deletePinNotificationPrecise deletes the pin notification with exact message ID
func (s *MessageService) deletePinNotificationPrecise(channelID string, chatID int64, notificationMsgID int) {
	log.Printf("Starting pin notification deletion for channel %s, expected notification ID: %d", channelID, notificationMsgID)

	// Try multiple times with increasing delays for high-concurrency scenarios
	maxRetries := 3
	baseDelay := 2 * time.Second

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Wait longer for high-concurrency scenarios
		waitTime := time.Duration(attempt) * baseDelay
		log.Printf("Attempt %d/%d: Waiting %v for pin notification to appear", attempt, maxRetries, waitTime)
		time.Sleep(waitTime)

		// Try to delete the exact notification message ID
		if s.tryDeleteMessage(channelID, chatID, notificationMsgID) {
			log.Printf("Successfully deleted pin notification %d in channel %s on attempt %d", notificationMsgID, channelID, attempt)
			return
		}

		// Also try nearby message IDs in case of ID calculation errors
		for offset := 1; offset <= 2; offset++ {
			if s.tryDeleteMessage(channelID, chatID, notificationMsgID+offset) {
				log.Printf("Successfully deleted pin notification %d (offset +%d) in channel %s on attempt %d",
					notificationMsgID+offset, offset, channelID, attempt)
				return
			}
			if s.tryDeleteMessage(channelID, chatID, notificationMsgID-offset) {
				log.Printf("Successfully deleted pin notification %d (offset -%d) in channel %s on attempt %d",
					notificationMsgID-offset, offset, channelID, attempt)
				return
			}
		}

		log.Printf("Attempt %d/%d failed to delete pin notification around ID %d in channel %s",
			attempt, maxRetries, notificationMsgID, channelID)
	}

	log.Printf("Failed to delete pin notification after %d attempts for channel %s, notification ID %d",
		maxRetries, channelID, notificationMsgID)
}

// deletePinNotification attempts to delete the "pinned a message" notification (legacy method)
func (s *MessageService) deletePinNotification(channelID string, chatID int64, pinnedMsgID int) {
	// Wait a moment for the pin notification to appear
	time.Sleep(2 * time.Second)

	// Try to delete potential pin notification messages
	// Pin notifications usually appear as the next message ID after the pinned message
	// We'll try a range of potential IDs since message IDs might not be strictly sequential

	maxAttempts := 5
	for i := 1; i <= maxAttempts; i++ {
		potentialNotificationID := pinnedMsgID + i

		if s.tryDeleteMessage(channelID, chatID, potentialNotificationID) {
			log.Printf("Successfully deleted pin notification %d in channel %s", potentialNotificationID, channelID)
			return // Found and deleted the notification, we're done
		}
	}

	log.Printf("Could not find pin notification to delete in channel %s", channelID)
}

// tryDeleteMessage attempts to delete a message and returns true if successful
func (s *MessageService) tryDeleteMessage(channelID string, chatID int64, messageID int) bool {
	var deleteConfig tgbotapi.DeleteMessageConfig
	if chatID != 0 {
		deleteConfig = tgbotapi.NewDeleteMessage(chatID, messageID)
	} else {
		deleteConfig = tgbotapi.DeleteMessageConfig{
			ChannelUsername: channelID,
			MessageID:       messageID,
		}
	}

	_, err := s.api.Request(deleteConfig)
	if err != nil {
		// Log at debug level to avoid spam
		log.Printf("Debug: Could not delete message %d in channel %s: %v", messageID, channelID, err)
		return false
	}

	return true
}

// deleteChannelMessages deletes all tracked messages from a channel
func (s *MessageService) deleteChannelMessages(channel models.Channel) error {
	// Delete last repost message if exists
	if channel.LastMessageID != "" {
		if err := s.deleteMessage(channel.ChannelID, channel.LastMessageID); err != nil {
			log.Printf("Failed to delete last message in channel %s: %v", channel.ChannelID, err)
		} else {
			// Clear last message ID
			s.repo.UpdateChannelLastMessageID(channel.ChannelID, "")
		}
	}

	// Get recent send records for this channel
	records, err := s.repo.GetSendRecordsByGroupID(channel.GroupID, 50)
	if err != nil {
		return fmt.Errorf("failed to get send records: %w", err)
	}

	// Delete messages from records
	for _, record := range records {
		if record.ChannelID == channel.ChannelID && record.MessageID != "" && record.Status == models.SendStatusSent {
			if err := s.deleteMessage(channel.ChannelID, record.MessageID); err != nil {
				log.Printf("Failed to delete message %s in channel %s: %v", record.MessageID, channel.ChannelID, err)
			}
		}
	}

	return nil
}

// createInlineKeyboard creates an inline keyboard from button data
func (s *MessageService) createInlineKeyboard(buttons models.InlineKeyboard) tgbotapi.InlineKeyboardMarkup {
	var keyboard [][]tgbotapi.InlineKeyboardButton

	for _, row := range buttons {
		var keyboardRow []tgbotapi.InlineKeyboardButton
		for _, button := range row {
			keyboardRow = append(keyboardRow, tgbotapi.NewInlineKeyboardButtonURL(button.Text, button.URL))
		}
		keyboard = append(keyboard, keyboardRow)
	}

	return tgbotapi.NewInlineKeyboardMarkup(keyboard...)
}

// recordSendSuccess records a successful send operation
func (s *MessageService) recordSendSuccess(groupID int64, channelID, messageID string, sendType models.SendType) {
	now := time.Now()
	record := &models.SendRecord{
		GroupID:     groupID,
		ChannelID:   channelID,
		MessageID:   messageID,
		MessageType: sendType,
		Status:      models.SendStatusSent,
		ScheduledAt: now,
		SentAt:      &now,
	}

	if err := s.repo.CreateSendRecord(record); err != nil {
		log.Printf("Failed to record send success: %v", err)
	}
}

// recordSendFailure records a failed send operation
func (s *MessageService) recordSendFailure(groupID int64, channelID string, sendType models.SendType, errorMsg string) {
	record := &models.SendRecord{
		GroupID:      groupID,
		ChannelID:    channelID,
		MessageType:  sendType,
		Status:       models.SendStatusFailed,
		ErrorMessage: models.StringPtr(errorMsg),
		ScheduledAt:  time.Now(),
	}

	if err := s.repo.CreateSendRecord(record); err != nil {
		log.Printf("Failed to record send failure: %v", err)
	}
}

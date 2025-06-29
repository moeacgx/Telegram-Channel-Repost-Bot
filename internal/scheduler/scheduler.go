package scheduler

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"tg-channel-repost-bot/internal/database"
	"tg-channel-repost-bot/internal/models"
	"tg-channel-repost-bot/internal/services"
	"tg-channel-repost-bot/pkg/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Scheduler handles scheduled message sending
type Scheduler struct {
	repo           *database.Repository
	messageService *services.MessageService
	config         *config.SchedulerConfig
	workers        chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

// New creates a new scheduler
func New(repo *database.Repository, messageService *services.MessageService, config *config.SchedulerConfig) *Scheduler {
	ctx, cancel := context.WithCancel(context.Background())

	return &Scheduler{
		repo:           repo,
		messageService: messageService,
		config:         config,
		workers:        make(chan struct{}, config.MaxWorkers),
		ctx:            ctx,
		cancel:         cancel,
	}
}

// Start starts the scheduler
func (s *Scheduler) Start() {
	log.Println("Starting scheduler...")

	// Clean up duplicate pending records on startup
	if err := s.repo.CleanupDuplicatePendingRecords(); err != nil {
		log.Printf("Failed to cleanup duplicate pending records: %v", err)
	}

	s.wg.Add(2)
	go s.scheduleRepostTasks()
	go s.processPendingTasks()

	log.Printf("Scheduler started with %d workers", s.config.MaxWorkers)
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	log.Println("Stopping scheduler...")
	s.cancel()
	s.wg.Wait()
	log.Println("Scheduler stopped")
}

// scheduleRepostTasks creates repost tasks for active channel groups
func (s *Scheduler) scheduleRepostTasks() {
	defer s.wg.Done()

	ticker := time.NewTicker(time.Duration(s.config.CheckInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.createRepostTasks()
		}
	}
}

// processPendingTasks processes pending send tasks
func (s *Scheduler) processPendingTasks() {
	defer s.wg.Done()

	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.processPendingRecords()
		}
	}
}

// createRepostTasks creates repost tasks for channel groups that need to send
func (s *Scheduler) createRepostTasks() {
	groups, err := s.repo.GetChannelGroups()
	if err != nil {
		log.Printf("Failed to get channel groups: %v", err)
		return
	}

	for _, group := range groups {
		if !group.IsActive {
			continue
		}

		if s.shouldCreateRepostTask(group) {
			s.createRepostTask(group)
		}
	}
}

// shouldCreateRepostTask determines if a repost task should be created for a group
func (s *Scheduler) shouldCreateRepostTask(group models.ChannelGroup) bool {
	// Handle different schedule modes
	switch group.ScheduleMode {
	case models.ScheduleModeFrequency:
		return s.shouldCreateFrequencyTask(group)
	case models.ScheduleModeTimepoints:
		return s.shouldCreateTimepointTask(group)
	default:
		// Default to frequency mode for backward compatibility
		return s.shouldCreateFrequencyTask(group)
	}
}

// shouldCreateFrequencyTask checks if a frequency-based task should be created
func (s *Scheduler) shouldCreateFrequencyTask(group models.ChannelGroup) bool {
	// Get the last successful repost for this group
	records, err := s.repo.GetSendRecordsByGroupID(group.ID, 1)
	if err != nil {
		log.Printf("Failed to get send records for group %d: %v", group.ID, err)
		return true // If we can't check, assume we should send
	}

	if len(records) == 0 {
		return true // No previous records, should send
	}

	lastRecord := records[0]
	if lastRecord.MessageType != models.SendTypeRepost || lastRecord.Status != models.SendStatusSent {
		return true // Last record wasn't a successful repost
	}

	// Check if enough time has passed based on frequency
	if lastRecord.SentAt == nil {
		return true
	}

	nextSendTime := lastRecord.SentAt.Add(time.Duration(group.Frequency) * time.Minute)
	return time.Now().After(nextSendTime)
}

// shouldCreateTimepointTask checks if a timepoint-based task should be created
func (s *Scheduler) shouldCreateTimepointTask(group models.ChannelGroup) bool {
	if len(group.ScheduleTimepoints) == 0 {
		log.Printf("Group %d has timepoint mode but no timepoints configured", group.ID)
		return false
	}

	now := time.Now()
	currentHour := now.Hour()
	currentMinute := now.Minute()
	today := now.Format("2006-01-02")

	// Check if current time matches any configured timepoint
	for _, timepoint := range group.ScheduleTimepoints {
		if timepoint.Hour == currentHour && timepoint.Minute == currentMinute {
			// Check if we already sent today at this timepoint
			if s.hasAlreadySentToday(group.ID, today, timepoint) {
				log.Printf("Already sent today (%s) at %02d:%02d for group %d", today, timepoint.Hour, timepoint.Minute, group.ID)
				continue
			}
			log.Printf("Time match found for group %d: %02d:%02d", group.ID, timepoint.Hour, timepoint.Minute)
			return true
		}
	}

	return false
}

// hasAlreadySentToday checks if we already sent a message today at the specified timepoint
func (s *Scheduler) hasAlreadySentToday(groupID int64, today string, timepoint models.TimePoint) bool {
	// Get today's records for this group
	records, err := s.repo.GetSendRecordsByGroupID(groupID, 50) // Get more records to check today
	if err != nil {
		log.Printf("Failed to get send records for group %d: %v", groupID, err)
		return false // If we can't check, assume we haven't sent
	}

	for _, record := range records {
		if record.MessageType != models.SendTypeRepost || record.Status != models.SendStatusSent {
			continue
		}
		if record.SentAt == nil {
			continue
		}

		// Check if this record is from today
		recordDate := record.SentAt.Format("2006-01-02")
		if recordDate != today {
			continue
		}

		// Check if this record is from the same timepoint (within 1 minute tolerance)
		recordHour := record.SentAt.Hour()
		recordMinute := record.SentAt.Minute()

		if recordHour == timepoint.Hour && abs(recordMinute-timepoint.Minute) <= 1 {
			return true // Already sent today at this timepoint
		}
	}

	return false
}

// abs returns the absolute value of an integer
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// createRepostTask creates a repost task for a channel group
func (s *Scheduler) createRepostTask(group models.ChannelGroup) {
	channels, err := s.repo.GetChannelsByGroupID(group.ID)
	if err != nil {
		log.Printf("Failed to get channels for group %d: %v", group.ID, err)
		return
	}

	// Create send records for each channel
	for _, channel := range channels {
		if !channel.IsActive {
			continue
		}

		// Check if there's already a pending record for this group and channel
		existingRecords, err := s.repo.GetPendingSendRecordsByGroupAndChannel(group.ID, channel.ChannelID)
		if err != nil {
			log.Printf("Failed to check existing records for group %d, channel %s: %v", group.ID, channel.ChannelID, err)
			continue
		}

		if len(existingRecords) > 0 {
			log.Printf("Skipping duplicate repost task for group %d, channel %s (found %d existing records)", group.ID, channel.ChannelID, len(existingRecords))
			continue
		}

		record := &models.SendRecord{
			GroupID:     group.ID,
			ChannelID:   channel.ChannelID,
			MessageType: models.SendTypeRepost,
			Status:      models.SendStatusPending,
			ScheduledAt: time.Now(),
		}

		if err := s.repo.CreateSendRecord(record); err != nil {
			log.Printf("Failed to create send record for channel %s: %v", channel.ChannelID, err)
		} else {
			log.Printf("Created repost task for group %d, channel %s", group.ID, channel.ChannelID)
		}
	}

	log.Printf("Created repost task for group: %s", group.Name)
}

// processPendingRecords processes pending send records
func (s *Scheduler) processPendingRecords() {
	records, err := s.repo.GetPendingSendRecords()
	if err != nil {
		log.Printf("Failed to get pending send records: %v", err)
		return
	}

	// Add rate limiting - process records with delay to avoid API limits
	for i, record := range records {
		select {
		case s.workers <- struct{}{}: // Acquire worker
			s.wg.Add(1)
			go s.processRecord(record)

			// Add delay between processing records to avoid API rate limits
			if i > 0 && i%5 == 0 { // Every 5 records, add a longer delay
				time.Sleep(2 * time.Second)
			} else {
				time.Sleep(200 * time.Millisecond) // Small delay between each record
			}
		case <-s.ctx.Done():
			return
		default:
			// No workers available, skip for now
			log.Printf("No workers available, skipping record %d", record.ID)
		}
	}
}

// processRecord processes a single send record
func (s *Scheduler) processRecord(record models.SendRecord) {
	defer func() {
		<-s.workers // Release worker
		s.wg.Done()
	}()

	log.Printf("Processing record %d: %s to %s", record.ID, record.MessageType, record.ChannelID)

	var err error
	switch record.MessageType {
	case models.SendTypeRepost:
		err = s.processRepostRecord(record)
	case models.SendTypePush:
		err = s.processPushRecord(record)
	default:
		log.Printf("Unknown message type: %s", record.MessageType)
		return
	}

	if err != nil {
		s.handleSendError(record, err)
	}
}

// processRepostRecord processes a repost record
func (s *Scheduler) processRepostRecord(record models.SendRecord) error {
	// Get channel group
	group, err := s.repo.GetChannelGroup(record.GroupID)
	if err != nil {
		return err
	}

	if !group.IsActive {
		// Mark as failed - group is inactive
		record.Status = models.SendStatusFailed
		record.ErrorMessage = models.StringPtr("Channel group is inactive")
		s.repo.UpdateSendRecord(&record)
		return nil
	}

	// Get message template
	template, err := s.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		return err
	}
	log.Printf("DEBUG: Loaded template %d for group %d, has %d button rows", template.ID, group.ID, len(template.Buttons))

	// Get channel info
	channels, err := s.repo.GetChannelsByGroupID(record.GroupID)
	if err != nil {
		return err
	}

	var targetChannel *models.Channel
	for _, channel := range channels {
		if channel.ChannelID == record.ChannelID {
			targetChannel = &channel
			break
		}
	}

	if targetChannel == nil {
		record.Status = models.SendStatusFailed
		record.ErrorMessage = models.StringPtr("Channel not found")
		s.repo.UpdateSendRecord(&record)
		return nil
	}

	// Delete previous message if exists
	if targetChannel.LastMessageID != "" {
		log.Printf("Deleting previous message %s from channel %s", targetChannel.LastMessageID, targetChannel.ChannelID)
		if err := s.messageService.DeleteMessage(targetChannel.ChannelID, targetChannel.LastMessageID); err != nil {
			log.Printf("Failed to delete previous message: %v", err)
		} else {
			log.Printf("Successfully deleted previous message %s", targetChannel.LastMessageID)
		}
	} else {
		log.Printf("No previous message to delete for channel %s", targetChannel.ChannelID)
	}

	// Send new message using complete template (with entities and buttons)
	var entities []tgbotapi.MessageEntity
	if template.Entities != "" {
		// Deserialize entities from JSON
		if err := json.Unmarshal([]byte(template.Entities), &entities); err != nil {
			log.Printf("Failed to deserialize entities for template %d: %v", template.ID, err)
			entities = nil
		} else {
			log.Printf("Using %d entities for repost message", len(entities))
		}
	}

	// Log button information
	if len(template.Buttons) > 0 {
		log.Printf("Using %d button rows for repost message", len(template.Buttons))
	}

	// Create a copy of template with entities for sending
	templateForSending := *template

	messageID, err := s.messageService.SendMessageWithTemplate(targetChannel.ChannelID, &templateForSending, entities)
	if err != nil {
		return err
	}

	log.Printf("Successfully sent new message %s to channel %s", messageID, targetChannel.ChannelID)

	// Pin message if auto pin is enabled
	if group.AutoPin {
		log.Printf("Auto pin is enabled for group %s, attempting to pin message %s", group.Name, messageID)
		if err := s.messageService.PinMessage(targetChannel.ChannelID, messageID); err != nil {
			log.Printf("Failed to pin message %s in channel %s: %v", messageID, targetChannel.ChannelID, err)
			// Don't fail the entire operation if pinning fails
		} else {
			log.Printf("Successfully pinned message %s in channel %s", messageID, targetChannel.ChannelID)
		}
	}

	// Update channel last message ID in database
	if err := s.repo.UpdateChannelLastMessageID(targetChannel.ChannelID, messageID); err != nil {
		log.Printf("Failed to update last message ID in database: %v", err)
	} else {
		log.Printf("Successfully updated last message ID to %s for channel %s", messageID, targetChannel.ChannelID)
	}

	// Update record
	now := time.Now()
	record.MessageID = messageID
	record.Status = models.SendStatusSent
	record.SentAt = &now
	record.ErrorMessage = nil

	return s.repo.UpdateSendRecord(&record)
}

// processPushRecord processes a push record
func (s *Scheduler) processPushRecord(record models.SendRecord) error {
	// Get channel group
	group, err := s.repo.GetChannelGroup(record.GroupID)
	if err != nil {
		return err
	}

	if !group.IsActive {
		// Mark as failed - group is inactive
		record.Status = models.SendStatusFailed
		record.ErrorMessage = models.StringPtr("Channel group is inactive")
		s.repo.UpdateSendRecord(&record)
		return nil
	}

	// Get message template
	template, err := s.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		return err
	}

	// Send message (don't delete previous)
	messageID, err := s.messageService.SendMessage(record.ChannelID, template)
	if err != nil {
		return err
	}

	// Update record
	now := time.Now()
	record.MessageID = messageID
	record.Status = models.SendStatusSent
	record.SentAt = &now
	record.ErrorMessage = nil

	return s.repo.UpdateSendRecord(&record)
}

// handleSendError handles send errors and implements retry logic
func (s *Scheduler) handleSendError(record models.SendRecord, err error) {
	log.Printf("Send error for record %d: %v", record.ID, err)

	record.RetryCount++
	record.ErrorMessage = models.StringPtr(err.Error())

	// Check if we should retry
	if record.RetryCount < s.config.RetryAttempts {
		record.Status = models.SendStatusRetry
		// Schedule retry after retry interval
		record.ScheduledAt = time.Now().Add(time.Duration(s.config.RetryInterval) * time.Second)
		log.Printf("Scheduling retry %d for record %d", record.RetryCount, record.ID)
	} else {
		record.Status = models.SendStatusFailed
		log.Printf("Max retries reached for record %d", record.ID)
	}

	if err := s.repo.UpdateSendRecord(&record); err != nil {
		log.Printf("Failed to update send record: %v", err)
	}
}

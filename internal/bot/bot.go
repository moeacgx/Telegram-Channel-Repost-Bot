package bot

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tg-channel-repost-bot/internal/database"
	"tg-channel-repost-bot/internal/models"
	"tg-channel-repost-bot/internal/services"
	"tg-channel-repost-bot/pkg/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// UserState represents user input state
type UserState struct {
	State string
	Data  map[string]interface{}
}

// Bot represents the Telegram bot
type Bot struct {
	api        *tgbotapi.BotAPI
	repo       *database.Repository
	service    *services.MessageService
	config     *config.Config
	updates    tgbotapi.UpdatesChannel
	userStates map[int64]*UserState
	stateMutex sync.RWMutex
	// Add operation locks to prevent concurrent operations on same group
	operationLocks map[int64]*sync.Mutex
	locksMutex     sync.RWMutex
	// Add user operation tracking to prevent rapid clicking
	userOperations map[int64]time.Time
	userOpMutex    sync.RWMutex
}

// New creates a new bot instance
func New(cfg *config.Config, repo *database.Repository, service *services.MessageService) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create bot API: %w", err)
	}

	api.Debug = cfg.Server.Debug

	log.Printf("Authorized on account %s", api.Self.UserName)

	return &Bot{
		api:            api,
		repo:           repo,
		service:        service,
		config:         cfg,
		userStates:     make(map[int64]*UserState),
		operationLocks: make(map[int64]*sync.Mutex),
		userOperations: make(map[int64]time.Time),
	}, nil
}

// Start starts the bot
func (b *Bot) Start() error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = b.config.Telegram.Timeout

	updates := b.api.GetUpdatesChan(u)
	b.updates = updates

	log.Println("Bot started, listening for updates...")

	for update := range updates {
		go b.handleUpdate(update)
	}

	return nil
}

// handleScheduleSettingsAction handles schedule settings action
func (b *Bot) handleScheduleSettingsAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "schedule_settings_")
	if groupID == 0 {
		return
	}

	b.showScheduleSettings(chatID, groupID)
}

// showScheduleSettings shows schedule settings for a group
func (b *Bot) showScheduleSettings(chatID int64, groupID int64) {
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Build current schedule info
	var scheduleInfo string
	switch group.ScheduleMode {
	case models.ScheduleModeFrequency:
		scheduleInfo = fmt.Sprintf("📅 当前模式：频率模式\n⏰ 发送频率：每 %d 分钟", group.Frequency)
	case models.ScheduleModeTimepoints:
		scheduleInfo = "📅 当前模式：时间点模式\n⏰ 发送时间："
		if len(group.ScheduleTimepoints) == 0 {
			scheduleInfo += " 未设置"
		} else {
			for _, tp := range group.ScheduleTimepoints {
				scheduleInfo += fmt.Sprintf(" %02d:%02d", tp.Hour, tp.Minute)
			}
		}
	default:
		scheduleInfo = "📅 当前模式：频率模式（默认）\n⏰ 发送频率：每 60 分钟"
	}

	text := fmt.Sprintf("⏰ *定时设置: %s*\n\n%s\n\n请选择操作：", group.Name, scheduleInfo)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📅 频率模式", fmt.Sprintf("schedule_mode_frequency_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🕐 时间点模式", fmt.Sprintf("schedule_mode_timepoints_%d", groupID)),
		),
	)

	// Add specific edit buttons based on current mode
	if group.ScheduleMode == models.ScheduleModeFrequency {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏰ 编辑频率", fmt.Sprintf("edit_freq_%d", groupID)),
		))
	} else if group.ScheduleMode == models.ScheduleModeTimepoints {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🕐 编辑时间点", fmt.Sprintf("edit_timepoints_%d", groupID)),
		))
	}

	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回编辑选项", fmt.Sprintf("edit_group_%d", groupID)),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// handleScheduleModeAction handles schedule mode change action
func (b *Bot) handleScheduleModeAction(chatID int64, data string) {
	// Parse data: schedule_mode_{mode}_{groupID}
	parts := strings.Split(data, "_")
	if len(parts) != 4 {
		b.sendMessage(chatID, "无效的模式切换操作。")
		return
	}

	mode := parts[2]
	groupID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Get current group
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Update schedule mode
	var newMode models.ScheduleMode
	var successMsg string

	switch mode {
	case "frequency":
		newMode = models.ScheduleModeFrequency
		successMsg = "✅ 已切换到频率模式"
		// Ensure frequency is set
		if group.Frequency <= 0 {
			group.Frequency = 60 // Default to 60 minutes
		}
	case "timepoints":
		newMode = models.ScheduleModeTimepoints
		successMsg = "✅ 已切换到时间点模式"
		// Initialize empty timepoints if not set
		if group.ScheduleTimepoints == nil {
			group.ScheduleTimepoints = models.TimePoints{}
		}
	default:
		b.sendMessage(chatID, "无效的定时模式。")
		return
	}

	// Update in database
	group.ScheduleMode = newMode
	err = b.repo.UpdateChannelGroup(group)
	if err != nil {
		b.sendMessage(chatID, "❌ 更新定时模式失败："+err.Error())
		return
	}

	b.sendMessage(chatID, successMsg)

	// Show updated schedule settings
	b.showScheduleSettings(chatID, groupID)
}

// handleEditTimepointsAction handles edit timepoints action
func (b *Bot) handleEditTimepointsAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "edit_timepoints_")
	if groupID == 0 {
		return
	}

	b.setState(chatID, "edit_timepoints", map[string]interface{}{
		"groupID": groupID,
	})

	helpText := "🕐 *编辑时间点*\n\n" +
		"请输入发送时间点，每行一个，格式为 HH:MM\n\n" +
		"**示例：**\n" +
		"```\n" +
		"03:00\n" +
		"05:00\n" +
		"10:00\n" +
		"20:00\n" +
		"```\n\n" +
		"⚠️ 使用24小时制，范围 00:00-23:59"

	msg := tgbotapi.NewMessage(chatID, helpText)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

// Stop stops the bot
func (b *Bot) Stop() {
	b.api.StopReceivingUpdates()
}

// handleUpdate handles incoming updates
func (b *Bot) handleUpdate(update tgbotapi.Update) {
	log.Printf("DEBUG: handleUpdate called")
	if update.Message != nil {
		log.Printf("DEBUG: Processing message update")
		b.handleMessage(update.Message)
	} else if update.CallbackQuery != nil {
		log.Printf("DEBUG: Processing callback query update with data='%s'", update.CallbackQuery.Data)
		b.handleCallbackQuery(update.CallbackQuery)
	} else {
		log.Printf("DEBUG: Unknown update type")
	}
}

// handleMessage handles incoming messages
func (b *Bot) handleMessage(message *tgbotapi.Message) {
	chatID := message.Chat.ID

	// Check if user is in a state first
	b.stateMutex.RLock()
	_, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	// If user is in a state, handle as text input (even if it's a command like /done)
	if exists {
		b.handleTextMessage(message)
		return
	}

	// Otherwise, handle normally
	if message.IsCommand() {
		b.handleCommand(message)
	} else {
		b.handleTextMessage(message)
	}
}

// handleCommand handles bot commands
func (b *Bot) handleCommand(message *tgbotapi.Message) {
	switch message.Command() {
	case "start":
		b.sendMainMenu(message.Chat.ID)
	case "help":
		b.sendHelp(message.Chat.ID)
	default:
		b.sendMessage(message.Chat.ID, "未知命令。使用 /start 查看可用选项。")
	}
}

// handleTextMessage handles regular text messages
func (b *Bot) handleTextMessage(message *tgbotapi.Message) {
	chatID := message.Chat.ID

	// Check if user is in a state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if exists {
		// Special handling for waiting_forward state to handle forwarded messages
		if userState.State == "waiting_forward" {
			b.handleForwardedMessage(chatID, message)
			return
		}
		// Special handling for input_push_message state to preserve entities
		if userState.State == "input_push_message" {
			b.handleInputPushMessageWithEntities(chatID, message, userState)
			return
		}
		// Special handling for edit_group_template state to preserve entities
		if userState.State == "edit_group_template" {
			log.Printf("DEBUG: Calling handleEditGroupTemplateWithEntities for user %d", chatID)
			b.handleEditGroupTemplateWithEntities(chatID, message, userState)
			return
		}
		b.handleUserInput(chatID, message.Text, userState)
		return
	}

	// Handle message for sending - show preview and options
	b.handleMessageForSending(chatID, message)
}

// handleCallbackQuery handles callback queries from inline keyboards
func (b *Bot) handleCallbackQuery(query *tgbotapi.CallbackQuery) {
	// Acknowledge the callback query
	callback := tgbotapi.NewCallback(query.ID, "")
	b.api.Request(callback)

	data := query.Data
	chatID := query.Message.Chat.ID

	log.Printf("DEBUG: handleCallbackQuery called with data='%s'", data)

	switch {
	case data == "main_menu":
		log.Printf("DEBUG: Matched main_menu")
		b.sendMainMenu(chatID)
	case data == "manage_groups":
		log.Printf("DEBUG: Matched manage_groups")
		b.sendGroupManagementMenu(chatID)
	case data == "send_messages":
		log.Printf("DEBUG: Matched send_messages")
		b.sendMessageMenu(chatID)
	case data == "view_records":
		log.Printf("DEBUG: Matched view_records")
		b.sendRecordsMenu(chatID)
	case data == "settings":
		log.Printf("DEBUG: Matched settings")
		b.sendSettingsMenu(chatID)
	case strings.HasPrefix(data, "group_layout_single_"):
		log.Printf("DEBUG: Matched group_layout_single_ prefix")
		b.handleGroupLayoutChoice(chatID, data, "single")
	case strings.HasPrefix(data, "group_layout_double_"):
		log.Printf("DEBUG: Matched group_layout_double_ prefix")
		b.handleGroupLayoutChoice(chatID, data, "double")
	case strings.HasPrefix(data, "group_"):
		log.Printf("DEBUG: Matched group_ prefix")
		b.handleGroupAction(chatID, data)
	case strings.HasPrefix(data, "edit_group_"):
		log.Printf("DEBUG: Matched edit_group_ prefix")
		b.handleEditGroupAction(chatID, data)
	case strings.HasPrefix(data, "send_"):
		log.Printf("DEBUG: Matched send_ prefix")
		b.handleSendAction(chatID, data)
	case strings.HasPrefix(data, "repost_"):
		log.Printf("DEBUG: Matched repost_ prefix")
		b.handleRepostAction(chatID, data)
	case strings.HasPrefix(data, "push_"):
		log.Printf("DEBUG: Matched push_ prefix")
		b.handlePushAction(chatID, data)
	case strings.HasPrefix(data, "confirm_delete_channel_"):
		log.Printf("DEBUG: Matched confirm_delete_channel_ prefix")
		b.handleConfirmDeleteChannelAction(chatID, data)
	case strings.HasPrefix(data, "delete_channel_"):
		log.Printf("DEBUG: Matched delete_channel_ prefix")
		b.handleDeleteChannelAction(chatID, data)
	case strings.HasPrefix(data, "delete_"):
		log.Printf("DEBUG: Matched delete_ prefix")
		b.handleDeleteAction(chatID, data)
	case data == "add_description" || data == "skip_description":
		log.Printf("DEBUG: Matched add/skip description")
		b.handleDescriptionCallback(chatID, data)
	case strings.HasPrefix(data, "add_channel_"):
		log.Printf("DEBUG: Matched add_channel_ prefix")
		b.handleAddChannelCallback(chatID, data)
	case data == "preview_push":
		log.Printf("DEBUG: Matched preview_push")
		b.handlePreviewPush(chatID)
	case data == "preview_repost":
		log.Printf("DEBUG: Matched preview_repost")
		b.handlePreviewRepost(chatID)
	case strings.HasPrefix(data, "custom_push_"):
		log.Printf("DEBUG: Matched custom_push_ prefix")
		b.handleCustomPushAction(chatID, data)
	case strings.HasPrefix(data, "custom_repost_"):
		log.Printf("DEBUG: Matched custom_repost_ prefix")
		b.handleCustomRepostAction(chatID, data)
	case strings.HasPrefix(data, "forward_"):
		log.Printf("DEBUG: Matched forward_ prefix")
		b.handleForwardAction(chatID, data)
	case strings.HasPrefix(data, "edit_name_"):
		log.Printf("DEBUG: Matched edit_name_ prefix")
		b.handleEditNameAction(chatID, data)
	case strings.HasPrefix(data, "edit_desc_"):
		log.Printf("DEBUG: Matched edit_desc_ prefix")
		b.handleEditDescAction(chatID, data)
	case strings.HasPrefix(data, "edit_freq_"):
		log.Printf("DEBUG: Matched edit_freq_ prefix")
		b.handleEditFreqAction(chatID, data)
	case strings.HasPrefix(data, "schedule_settings_"):
		log.Printf("DEBUG: Matched schedule_settings_ prefix")
		b.handleScheduleSettingsAction(chatID, data)
	case strings.HasPrefix(data, "schedule_mode_"):
		log.Printf("DEBUG: Matched schedule_mode_ prefix")
		b.handleScheduleModeAction(chatID, data)
	case strings.HasPrefix(data, "edit_timepoints_"):
		log.Printf("DEBUG: Matched edit_timepoints_ prefix")
		b.handleEditTimepointsAction(chatID, data)
	case strings.HasPrefix(data, "edit_template_"):
		log.Printf("DEBUG: Matched edit_template_ prefix")
		b.handleEditTemplateAction(chatID, data)
	case strings.HasPrefix(data, "manage_channels_"):
		log.Printf("DEBUG: Matched manage_channels_ prefix")
		b.handleManageChannelsAction(chatID, data)
	case strings.HasPrefix(data, "toggle_status_"):
		log.Printf("DEBUG: Matched toggle_status_ prefix")
		b.handleToggleStatusAction(chatID, data)
	case strings.HasPrefix(data, "toggle_pin_"):
		log.Printf("DEBUG: Matched toggle_pin_ prefix")
		b.handleTogglePinAction(chatID, data)
	case strings.HasPrefix(data, "manage_buttons_"):
		log.Printf("DEBUG: Matched manage_buttons_ prefix")
		b.handleManageButtonsAction(chatID, data)
	case strings.HasPrefix(data, "add_buttons_"):
		log.Printf("DEBUG: Matched add_buttons_ prefix")
		b.handleAddButtonsAction(chatID, data)
	case strings.HasPrefix(data, "skip_buttons_"):
		log.Printf("DEBUG: Matched skip_buttons_ prefix")
		b.handleSkipButtonsAction(chatID, data)
	case strings.HasPrefix(data, "add_button_"):
		log.Printf("DEBUG: Matched add_button_ prefix")
		b.handleAddButtonAction(chatID, data)
	case strings.HasPrefix(data, "clear_buttons_"):
		log.Printf("DEBUG: Matched clear_buttons_ prefix")
		b.handleClearButtonsAction(chatID, data)
	case strings.HasPrefix(data, "preview_message_"):
		log.Printf("DEBUG: Matched preview_message_ prefix")
		b.handlePreviewMessageAction(chatID, data)
	case data == "add_push_buttons":
		log.Printf("DEBUG: Matched add_push_buttons")
		b.handleAddPushButtonsAction(chatID)
	case data == "skip_push_buttons":
		log.Printf("DEBUG: Matched skip_push_buttons")
		b.handleSkipPushButtonsAction(chatID)
	case data == "layout_single":
		log.Printf("DEBUG: Matched layout_single")
		b.handleLayoutChoice(chatID, "single")
	case data == "layout_double":
		log.Printf("DEBUG: Matched layout_double")
		b.handleLayoutChoice(chatID, "double")
	default:
		log.Printf("DEBUG: No match found, going to default case")
		b.sendMessage(chatID, "未知操作。")
	}
}

// sendMainMenu sends the main menu
func (b *Bot) sendMainMenu(chatID int64) {
	text := "🤖 *Telegram 频道转发机器人*\n\n请选择功能："

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 管理频道组", "manage_groups"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📤 发送消息", "send_messages"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📊 查看记录", "view_records"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚙️ 设置", "settings"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// sendGroupManagementMenu sends the group management menu
func (b *Bot) sendGroupManagementMenu(chatID int64) {
	groups, err := b.repo.GetChannelGroups()
	if err != nil {
		log.Printf("ERROR: Failed to get channel groups: %v", err)
		b.sendMessage(chatID, fmt.Sprintf("加载频道组时出错：%v", err))
		return
	}

	text := "📋 *频道组管理*\n\n"

	var keyboard [][]tgbotapi.InlineKeyboardButton

	if len(groups) == 0 {
		text += "未找到频道组。"
	} else {
		text += "选择要管理的频道组："
		for _, group := range groups {
			status := "🔴"
			if group.IsActive {
				status = "🟢"
			}
			buttonText := fmt.Sprintf("%s %s", status, group.Name)
			buttonData := fmt.Sprintf("group_%d", group.ID)
			keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(buttonText, buttonData),
			))
		}
	}

	keyboard = append(keyboard,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ 添加新组", "group_add"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "main_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)

	b.api.Send(msg)
}

// sendMessageMenu sends the message sending menu
func (b *Bot) sendMessageMenu(chatID int64) {
	text := "📤 *发送消息*\n\n选择操作："

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 立即重发定时内容", "send_repost"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📢 推送消息", "send_push"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📤 无引用转发", "send_forward"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑️ 删除消息", "send_delete"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "main_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// sendRecordsMenu sends the records viewing menu
func (b *Bot) sendRecordsMenu(chatID int64) {
	text := "📊 *发送记录*\n\n查看发送统计和历史："

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📈 统计数据", "records_stats"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 最近记录", "records_recent"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "main_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// sendSettingsMenu sends the settings menu
func (b *Bot) sendSettingsMenu(chatID int64) {
	text := "⚙️ *设置*\n\n配置机器人设置："

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 重试设置", "settings_retry"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏰ 定时设置", "settings_schedule"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "main_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// sendHelp sends help information
func (b *Bot) sendHelp(chatID int64) {
	text := `🤖 *Telegram 频道转发机器人帮助*

此机器人帮助您管理多个 Telegram 频道的自动转发和手动推送消息。

*主要功能：*
• 管理频道组
• 定时自动转发
• 发送手动推送消息
• 查看发送统计
• 配置重试设置

*命令：*
/start - 显示主菜单
/help - 显示此帮助信息

使用内联键盘按钮浏览机器人的功能。`

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"

	b.api.Send(msg)
}

// sendMessage sends a simple text message
func (b *Bot) sendMessage(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	b.api.Send(msg)
}

// handleGroupAction handles group-related actions
func (b *Bot) handleGroupAction(chatID int64, data string) {
	if data == "group_add" {
		b.startAddGroupFlow(chatID)
		return
	}

	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 2 {
		b.sendMessage(chatID, "无效的组操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Show group details
	b.showGroupDetails(chatID, groupID)
}

// handleEditGroupAction handles edit group action
func (b *Bot) handleEditGroupAction(chatID int64, data string) {
	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 3 {
		b.sendMessage(chatID, "无效的编辑操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Show edit options for the group
	b.showGroupEditOptions(chatID, groupID)
}

// handleSendAction handles send-related actions
func (b *Bot) handleSendAction(chatID int64, data string) {
	switch data {
	case "send_repost":
		b.showGroupSelectionForRepost(chatID)
	case "send_push":
		b.showGroupSelectionForPush(chatID)
	case "send_forward":
		b.handleForwardRequest(chatID)
	case "send_delete":
		b.showGroupSelectionForDelete(chatID)
	}
}

// showGroupDetails shows details for a specific group
func (b *Bot) showGroupDetails(chatID int64, groupID int64) {
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组频道时出错。")
		return
	}

	text := fmt.Sprintf("📋 *组: %s*\n\n", group.Name)
	text += fmt.Sprintf("描述: %s\n", group.Description)
	text += fmt.Sprintf("频率: %d 分钟\n", group.Frequency)
	text += fmt.Sprintf("状态: %s\n", map[bool]string{true: "🟢 活跃", false: "🔴 非活跃"}[group.IsActive])
	text += fmt.Sprintf("自动置顶: %s\n", map[bool]string{true: "📌 启用", false: "📌 禁用"}[group.AutoPin])
	text += fmt.Sprintf("频道数: %d\n\n", len(channels))

	if len(channels) > 0 {
		text += "*频道:*\n"
		for _, channel := range channels {
			text += fmt.Sprintf("• %s (%s)\n", channel.ChannelName, channel.ChannelID)
		}
	}

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✏️ 编辑", fmt.Sprintf("edit_group_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "manage_groups"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// showGroupEditOptions shows edit options for a specific group
func (b *Bot) showGroupEditOptions(chatID int64, groupID int64) {
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Determine status button text and action
	statusText := "🔴 禁用组"
	statusAction := "disable"
	if !group.IsActive {
		statusText = "🟢 启用组"
		statusAction = "enable"
	}

	// Determine auto pin button text and action
	pinText := "📌 启用自动置顶"
	pinAction := "enable"
	if group.AutoPin {
		pinText = "📌 禁用自动置顶"
		pinAction = "disable"
	}

	text := fmt.Sprintf("✏️ *编辑频道组: %s*\n\n请选择要编辑的内容：", group.Name)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 编辑名称", fmt.Sprintf("edit_name_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📄 编辑描述", fmt.Sprintf("edit_desc_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏰ 定时设置", fmt.Sprintf("schedule_settings_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("💬 编辑模板", fmt.Sprintf("edit_template_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔘 按钮管理", fmt.Sprintf("manage_buttons_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📢 管理频道", fmt.Sprintf("manage_channels_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(statusText, fmt.Sprintf("toggle_status_%s_%d", statusAction, groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(pinText, fmt.Sprintf("toggle_pin_%s_%d", pinAction, groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回组详情", fmt.Sprintf("group_%d", groupID)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// handleToggleStatusAction handles toggle status action
func (b *Bot) handleToggleStatusAction(chatID int64, data string) {
	// Parse callback data: toggle_status_enable_1 or toggle_status_disable_1
	parts := strings.Split(data, "_")
	if len(parts) != 4 {
		b.sendMessage(chatID, "无效的操作。")
		return
	}

	action := parts[2] // "enable" or "disable"
	groupID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Get current group info
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Determine new status
	var newStatus bool
	var statusText string
	if action == "enable" {
		newStatus = true
		statusText = "启用"
	} else {
		newStatus = false
		statusText = "禁用"
	}

	// Update status in database
	err = b.repo.UpdateChannelGroupStatus(groupID, newStatus)
	if err != nil {
		b.sendMessage(chatID, "❌ 更新状态失败："+err.Error())
		return
	}

	// Send confirmation message
	confirmMsg := fmt.Sprintf("✅ 频道组 *%s* 已%s", group.Name, statusText)
	if !newStatus {
		confirmMsg += "\n\n⚠️ 自动重发功能已停止运行\n💡 手工操作（立即重发、无引用转发）不受影响"
	} else {
		confirmMsg += "\n\n🔄 自动重发功能已恢复运行"
	}

	msg := tgbotapi.NewMessage(chatID, confirmMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// handleTogglePinAction handles toggle auto pin action
func (b *Bot) handleTogglePinAction(chatID int64, data string) {
	// Parse data: toggle_pin_enable_123 or toggle_pin_disable_123
	parts := strings.Split(data, "_")
	if len(parts) != 4 {
		b.sendMessage(chatID, "❌ 无效的操作格式")
		return
	}

	action := parts[2] // "enable" or "disable"
	groupIDStr := parts[3]
	groupID, err := strconv.ParseInt(groupIDStr, 10, 64)
	if err != nil {
		b.sendMessage(chatID, "❌ 无效的组ID")
		return
	}

	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Determine new status
	var newAutoPin bool
	var statusText string
	if action == "enable" {
		newAutoPin = true
		statusText = "启用"
	} else {
		newAutoPin = false
		statusText = "禁用"
	}

	// Update auto pin setting in database
	err = b.repo.UpdateChannelGroupAutoPin(groupID, newAutoPin)
	if err != nil {
		b.sendMessage(chatID, "❌ 更新置顶设置失败："+err.Error())
		return
	}

	// Send confirmation message
	confirmMsg := fmt.Sprintf("✅ 频道组 *%s* 已%s自动置顶", group.Name, statusText)
	if newAutoPin {
		confirmMsg += "\n\n📌 重发消息后将自动置顶"
	} else {
		confirmMsg += "\n\n📌 重发消息后不会自动置顶"
	}

	msg := tgbotapi.NewMessage(chatID, confirmMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// Edit Group Action Handlers

// handleEditNameAction handles edit name action
func (b *Bot) handleEditNameAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "edit_name_")
	if groupID == 0 {
		return
	}

	b.setState(chatID, "edit_group_name", map[string]interface{}{
		"groupID": groupID,
	})
	b.sendMessage(chatID, "📝 *编辑频道组名称*\n\n请输入新的名称：")
}

// handleEditDescAction handles edit description action
func (b *Bot) handleEditDescAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "edit_desc_")
	if groupID == 0 {
		return
	}

	b.setState(chatID, "edit_group_desc", map[string]interface{}{
		"groupID": groupID,
	})
	b.sendMessage(chatID, "📄 *编辑频道组描述*\n\n请输入新的描述：")
}

// handleEditFreqAction handles edit frequency action
func (b *Bot) handleEditFreqAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "edit_freq_")
	if groupID == 0 {
		return
	}

	b.setState(chatID, "edit_group_freq", map[string]interface{}{
		"groupID": groupID,
	})
	b.sendMessage(chatID, "⏰ *编辑发送频率*\n\n请输入新的频率（分钟）：")
}

// handleEditTemplateAction handles edit template action
func (b *Bot) handleEditTemplateAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "edit_template_")
	if groupID == 0 {
		return
	}

	b.setState(chatID, "edit_group_template", map[string]interface{}{
		"groupID": groupID,
	})

	templateMsg := "💬 *编辑消息模板*\n\n" +
		"📝 **支持的消息类型：**\n" +
		"• 📄 文字消息（支持格式化）\n" +
		"• 📸 图片消息（图片+说明文字）\n\n" +
		"请发送新的模板内容："

	msg := tgbotapi.NewMessage(chatID, templateMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

// handleManageChannelsAction handles manage channels action
func (b *Bot) handleManageChannelsAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "manage_channels_")
	if groupID == 0 {
		return
	}

	b.showChannelManagement(chatID, groupID)
}

// extractGroupIDFromData extracts group ID from callback data
func (b *Bot) extractGroupIDFromData(data, prefix string) int64 {
	log.Printf("DEBUG: extractGroupIDFromData called with data='%s', prefix='%s'", data, prefix)

	// Remove the prefix from the data
	if !strings.HasPrefix(data, prefix) {
		log.Printf("DEBUG: Data does not have prefix. Data='%s', Prefix='%s'", data, prefix)
		b.sendMessage(0, "无效的操作前缀。")
		return 0
	}

	// Get the remaining part after prefix
	remaining := strings.TrimPrefix(data, prefix)
	log.Printf("DEBUG: Remaining part after prefix: '%s'", remaining)

	// Parse the group ID from the remaining part
	groupID, err := strconv.ParseInt(remaining, 10, 64)
	if err != nil {
		log.Printf("DEBUG: Failed to parse group ID from '%s': %v", remaining, err)
		b.sendMessage(0, "无效的组ID。")
		return 0
	}

	log.Printf("DEBUG: Successfully extracted group ID: %d", groupID)
	return groupID
}

// showChannelManagement shows channel management for a group
func (b *Bot) showChannelManagement(chatID int64, groupID int64) {
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	text := fmt.Sprintf("📢 *管理频道: %s*\n\n", group.Name)

	var keyboard [][]tgbotapi.InlineKeyboardButton

	if len(channels) == 0 {
		text += "该组暂无频道。"
	} else {
		text += "当前频道列表：\n"
		for _, channel := range channels {
			status := "🟢"
			if !channel.IsActive {
				status = "🔴"
			}
			text += fmt.Sprintf("%s %s (%s)\n", status, channel.ChannelName, channel.ChannelID)

			// Add delete button for each channel
			deleteButtonText := fmt.Sprintf("🗑️ 删除 %s", channel.ChannelName)
			deleteButtonData := fmt.Sprintf("delete_channel_%d_%d", groupID, channel.ID)
			keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(deleteButtonText, deleteButtonData),
			))
		}
	}

	// Add management buttons
	keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("➕ 添加频道", fmt.Sprintf("add_channel_%d", groupID)),
	))
	keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回编辑选项", fmt.Sprintf("edit_group_%d", groupID)),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)
	b.api.Send(msg)
}

// User state management functions

// setState sets user state
func (b *Bot) setState(chatID int64, state string, data map[string]interface{}) {
	b.stateMutex.Lock()
	defer b.stateMutex.Unlock()

	if data == nil {
		data = make(map[string]interface{})
	}

	b.userStates[chatID] = &UserState{
		State: state,
		Data:  data,
	}
}

// clearState clears user state
func (b *Bot) clearState(chatID int64) {
	b.stateMutex.Lock()
	defer b.stateMutex.Unlock()
	delete(b.userStates, chatID)
}

// getOperationLock gets or creates a lock for a specific group operation
func (b *Bot) getOperationLock(groupID int64) *sync.Mutex {
	b.locksMutex.Lock()
	defer b.locksMutex.Unlock()

	if lock, exists := b.operationLocks[groupID]; exists {
		return lock
	}

	lock := &sync.Mutex{}
	b.operationLocks[groupID] = lock
	return lock
}

// cleanupOperationLock removes unused operation locks
func (b *Bot) cleanupOperationLock(groupID int64) {
	b.locksMutex.Lock()
	defer b.locksMutex.Unlock()
	delete(b.operationLocks, groupID)
}

// checkUserOperation checks if user can perform operation (prevents rapid clicking)
func (b *Bot) checkUserOperation(chatID int64) bool {
	b.userOpMutex.Lock()
	defer b.userOpMutex.Unlock()

	now := time.Now()
	if lastOp, exists := b.userOperations[chatID]; exists {
		// Prevent operations within 2 seconds
		if now.Sub(lastOp) < 2*time.Second {
			return false
		}
	}

	b.userOperations[chatID] = now
	return true
}

// cleanupUserOperation removes old user operation records
func (b *Bot) cleanupUserOperation(chatID int64) {
	b.userOpMutex.Lock()
	defer b.userOpMutex.Unlock()
	delete(b.userOperations, chatID)
}

// adjustEntitiesForPreview adjusts entity offsets for preview messages
func (b *Bot) adjustEntitiesForPreview(entities []tgbotapi.MessageEntity, prefixLength int) []tgbotapi.MessageEntity {
	adjustedEntities := make([]tgbotapi.MessageEntity, len(entities))
	for i, entity := range entities {
		adjustedEntity := entity
		adjustedEntity.Offset = entity.Offset + prefixLength
		adjustedEntities[i] = adjustedEntity
		log.Printf("Adjusted entity %d: original offset=%d, new offset=%d, length=%d, type=%s",
			i, entity.Offset, adjustedEntity.Offset, adjustedEntity.Length, adjustedEntity.Type)
	}
	return adjustedEntities
}

// handleUserInput handles user input based on current state
func (b *Bot) handleUserInput(chatID int64, input string, userState *UserState) {
	switch userState.State {
	case "add_group_name":
		b.handleAddGroupName(chatID, input, userState)
	case "add_group_description":
		b.handleAddGroupDescription(chatID, input, userState)
	case "add_group_frequency":
		b.handleAddGroupFrequency(chatID, input, userState)
	case "add_template_content":
		b.handleAddTemplateContent(chatID, input, userState)
	case "add_channel_to_group":
		b.handleAddChannelToGroup(chatID, input, userState)
	case "custom_message_content":
		b.handleCustomMessageContent(chatID, input, userState)
	case "push_message":
		b.handlePushMessage(chatID, input, userState)
	case "input_push_message":
		b.handleInputPushMessage(chatID, input, userState)
	case "edit_group_name":
		b.handleEditGroupName(chatID, input, userState)
	case "edit_group_desc":
		b.handleEditGroupDesc(chatID, input, userState)
	case "edit_group_freq":
		b.handleEditGroupFreq(chatID, input, userState)
	case "edit_timepoints":
		b.handleEditTimepoints(chatID, input, userState)
	case "edit_group_template":
		b.handleEditGroupTemplate(chatID, input, userState)
	case "add_buttons":
		b.handleAddButtons(chatID, input, userState)
	case "add_single_button":
		b.handleAddSingleButton(chatID, input, userState)
	case "add_push_buttons":
		b.handleAddPushButtons(chatID, input, userState)
	case "waiting_forward":
		// This state is handled in handleTextMessage for forwarded messages
		b.sendMessage(chatID, "请转发一条消息给我，而不是发送文字。")
	default:
		b.clearState(chatID)
		b.sendMessage(chatID, "未知状态，已重置。")
		b.sendMainMenu(chatID)
	}
}

// handleDescriptionCallback handles description choice callback
func (b *Bot) handleDescriptionCallback(chatID int64, data string) {
	// Check if user is in the correct state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || userState.State != "add_group_description" {
		b.sendMessage(chatID, "❌ 操作已过期，请重新开始。")
		b.clearState(chatID)
		b.sendMainMenu(chatID)
		return
	}

	b.handleDescriptionChoice(chatID, data, userState)
}

// Add Group Flow Functions

// startAddGroupFlow starts the add group flow
func (b *Bot) startAddGroupFlow(chatID int64) {
	b.setState(chatID, "add_group_name", nil)
	b.sendMessage(chatID, "📋 *添加新频道组*\n\n请输入频道组名称：")
}

// handleAddGroupName handles group name input
func (b *Bot) handleAddGroupName(chatID int64, input string, userState *UserState) {
	if strings.TrimSpace(input) == "" {
		b.sendMessage(chatID, "❌ 频道组名称不能为空，请重新输入：")
		return
	}

	userState.Data["name"] = strings.TrimSpace(input)
	b.setState(chatID, "add_group_description", userState.Data)

	text := "✅ 频道组名称已设置为：" + input + "\n\n请选择是否添加描述："
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📝 添加描述", "add_description"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏭️ 跳过描述", "skip_description"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// handleAddGroupDescription handles group description input
func (b *Bot) handleAddGroupDescription(chatID int64, input string, userState *UserState) {
	userState.Data["description"] = strings.TrimSpace(input)
	b.setState(chatID, "add_group_frequency", userState.Data)
	b.sendMessage(chatID, "✅ 描述已设置\n\n请输入发送频率（分钟，例如：60）：")
}

// handleDescriptionChoice handles description choice buttons
func (b *Bot) handleDescriptionChoice(chatID int64, choice string, userState *UserState) {
	if choice == "skip_description" {
		userState.Data["description"] = ""
		b.setState(chatID, "add_group_frequency", userState.Data)
		b.sendMessage(chatID, "⏭️ 已跳过描述\n\n请输入发送频率（分钟，例如：60）：")
	} else if choice == "add_description" {
		b.sendMessage(chatID, "📝 请输入频道组描述：")
		// Keep the same state, wait for text input
	}
}

// handleAddGroupFrequency handles group frequency input
func (b *Bot) handleAddGroupFrequency(chatID int64, input string, userState *UserState) {
	frequency, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || frequency <= 0 {
		b.sendMessage(chatID, "❌ 请输入有效的数字（大于0的分钟数）：")
		return
	}

	userState.Data["frequency"] = frequency
	b.setState(chatID, "add_template_content", userState.Data)
	b.sendMessage(chatID, "✅ 发送频率已设置为："+input+" 分钟\n\n现在请输入消息模板内容：")
}

// handleAddTemplateContent handles template content input
func (b *Bot) handleAddTemplateContent(chatID int64, input string, userState *UserState) {
	if strings.TrimSpace(input) == "" {
		b.sendMessage(chatID, "❌ 消息模板不能为空，请重新输入：")
		return
	}

	userState.Data["template_content"] = strings.TrimSpace(input)

	// Create the group and template
	b.createChannelGroupWithTemplate(chatID, userState.Data)
}

// createChannelGroupWithTemplate creates a new channel group with template
func (b *Bot) createChannelGroupWithTemplate(chatID int64, data map[string]interface{}) {
	// Create message template first
	template := &models.MessageTemplate{
		Title:       data["name"].(string) + " 模板",
		Content:     data["template_content"].(string),
		MessageType: models.MessageTypeText,
		MediaURL:    "",
		Buttons:     models.InlineKeyboard{},
	}

	err := b.repo.CreateMessageTemplate(template)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 创建消息模板失败："+err.Error())
		return
	}

	// Create channel group
	group := &models.ChannelGroup{
		Name:        data["name"].(string),
		Description: data["description"].(string),
		MessageID:   template.ID,
		Frequency:   data["frequency"].(int),
		IsActive:    true,
	}

	err = b.repo.CreateChannelGroup(group)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 创建频道组失败："+err.Error())
		return
	}

	b.clearState(chatID)

	successMsg := fmt.Sprintf("✅ *频道组创建成功！*\n\n"+
		"📋 名称：%s\n"+
		"📝 描述：%s\n"+
		"⏰ 频率：%d 分钟\n"+
		"💬 消息模板已创建\n\n"+
		"现在可以为此频道组添加频道了。",
		group.Name,
		group.Description,
		group.Frequency)

	// Show options to manage the group
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ 添加频道", fmt.Sprintf("add_channel_%d", group.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 查看详情", fmt.Sprintf("group_%d", group.ID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回主菜单", "main_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// Send Functions

// showGroupSelectionForRepost shows group selection for repost
func (b *Bot) showGroupSelectionForRepost(chatID int64) {
	groups, err := b.repo.GetChannelGroups()
	if err != nil {
		b.sendMessage(chatID, "加载频道组时出错。")
		return
	}

	if len(groups) == 0 {
		b.sendMessage(chatID, "没有可用的频道组。请先创建频道组。")
		return
	}

	text := "🔄 *立即重发定时内容*\n\n选择要重发的频道组："
	var keyboard [][]tgbotapi.InlineKeyboardButton

	for _, group := range groups {
		status := "🔴"
		if group.IsActive {
			status = "🟢"
		}
		buttonText := fmt.Sprintf("%s %s", status, group.Name)
		buttonData := fmt.Sprintf("repost_%d", group.ID)
		keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText, buttonData),
		))
	}

	keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "send_messages"),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)
	b.api.Send(msg)
}

// showGroupSelectionForPush shows group selection for push
func (b *Bot) showGroupSelectionForPush(chatID int64) {
	// Set user state to input push message first
	b.setState(chatID, "input_push_message", map[string]interface{}{})

	pushMsg := "📢 *推送自定义消息*\n\n" +
		"📝 **支持的消息类型：**\n" +
		"• 📄 文字消息（支持格式化）\n" +
		"• 📸 图片消息（图片+说明文字）\n\n" +
		"请发送要推送的消息内容："

	msg := tgbotapi.NewMessage(chatID, pushMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)
}

// showGroupSelectionForDelete shows group selection for delete
func (b *Bot) showGroupSelectionForDelete(chatID int64) {
	groups, err := b.repo.GetChannelGroups()
	if err != nil {
		b.sendMessage(chatID, "加载频道组时出错。")
		return
	}

	if len(groups) == 0 {
		b.sendMessage(chatID, "没有可用的频道组。请先创建频道组。")
		return
	}

	text := "🗑️ *删除消息*\n\n选择要删除消息的频道组："
	var keyboard [][]tgbotapi.InlineKeyboardButton

	for _, group := range groups {
		if group.IsActive {
			status := "🟢"
			buttonText := fmt.Sprintf("%s %s", status, group.Name)
			buttonData := fmt.Sprintf("delete_%d", group.ID)
			keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(buttonText, buttonData),
			))
		}
	}

	keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "send_messages"),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)
	b.api.Send(msg)
}

// Action Handlers

// handleRepostAction handles repost action for a specific group
func (b *Bot) handleRepostAction(chatID int64, data string) {
	// Check if user can perform operation (prevent rapid clicking)
	if !b.checkUserOperation(chatID) {
		b.sendMessage(chatID, "⚠️ 操作过于频繁，请稍后再试。")
		return
	}
	defer func() {
		// Clean up user operation record after 5 seconds
		go func() {
			time.Sleep(5 * time.Second)
			b.cleanupUserOperation(chatID)
		}()
	}()

	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 2 {
		b.sendMessage(chatID, "无效的转发操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Get operation lock for this group to prevent concurrent operations
	lock := b.getOperationLock(groupID)
	if !lock.TryLock() {
		b.sendMessage(chatID, "⚠️ 该频道组正在处理其他操作，请稍后再试。")
		return
	}
	defer func() {
		lock.Unlock()
		// Clean up the lock after operation completes
		go func() {
			time.Sleep(1 * time.Second)
			b.cleanupOperationLock(groupID)
		}()
	}()

	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Get message template
	template, err := b.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		b.sendMessage(chatID, "加载消息模板时出错。")
		return
	}

	// Get channels for this group
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	if len(channels) == 0 {
		b.sendMessage(chatID, "该组没有绑定的频道。")
		return
	}

	// Send messages to all channels (repost - delete previous first) with rate limiting
	successCount := 0
	for i, channel := range channels {
		if channel.IsActive {
			// Rate limiting: delay between channels to avoid API limits
			if i > 0 {
				time.Sleep(400 * time.Millisecond) // 400ms delay for template repost
				log.Printf("Rate limiting: waiting 400ms before sending template repost to channel %s", channel.ChannelID)
			}

			// Delete previous message if exists (repost behavior)
			if channel.LastMessageID != "" {
				err := b.service.DeleteMessage(channel.ChannelID, channel.LastMessageID)
				if err != nil {
					log.Printf("Failed to delete previous message from channel %s: %v", channel.ChannelID, err)
				} else {
					log.Printf("Successfully deleted previous message %s from channel %s", channel.LastMessageID, channel.ChannelID)
				}
			}

			// Send new message
			messageID, err := b.service.SendMessage(channel.ChannelID, template)
			if err != nil {
				log.Printf("Failed to send message to channel %s: %v", channel.ChannelID, err)
			} else {
				successCount++
				log.Printf("Successfully sent new message %s to channel %s", messageID, channel.ChannelID)

				// Pin message if auto pin is enabled
				if group.AutoPin {
					log.Printf("Auto pin is enabled for group %s, attempting to pin message %s", group.Name, messageID)
					if err := b.service.PinMessage(channel.ChannelID, messageID); err != nil {
						log.Printf("Failed to pin message %s in channel %s: %v", messageID, channel.ChannelID, err)
						// Don't fail the entire operation if pinning fails
					} else {
						log.Printf("Successfully pinned message %s in channel %s", messageID, channel.ChannelID)
					}
				}

				// Update last message ID in database
				if err := b.repo.UpdateChannelLastMessageID(channel.ChannelID, messageID); err != nil {
					log.Printf("Failed to update last message ID for channel %s: %v", channel.ChannelID, err)
				} else {
					log.Printf("Successfully updated last message ID to %s for channel %s", messageID, channel.ChannelID)
				}
			}
		}
	}

	successMsg := fmt.Sprintf("✅ *转发完成*\n\n"+
		"📋 频道组：%s\n"+
		"📢 成功发送：%d/%d 个频道\n"+
		"💬 消息内容：%s",
		group.Name,
		successCount,
		len(channels),
		template.Content)

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	b.sendMainMenu(chatID)
}

// handlePushAction handles push action for a specific group
func (b *Bot) handlePushAction(chatID int64, data string) {
	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 2 {
		b.sendMessage(chatID, "无效的推送操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Set user state to input custom message
	b.setState(chatID, "push_message", map[string]interface{}{
		"groupID": groupID,
	})

	b.sendMessage(chatID, "📢 *推送自定义消息*\n\n请输入要推送的消息内容：")
}

// handleDeleteAction handles delete action for a specific group
func (b *Bot) handleDeleteAction(chatID int64, data string) {
	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 2 {
		b.sendMessage(chatID, "无效的删除操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Get channels for this group
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	if len(channels) == 0 {
		b.sendMessage(chatID, "该组没有绑定的频道。")
		return
	}

	// Delete last messages from all channels
	successCount := 0
	for _, channel := range channels {
		if channel.IsActive && channel.LastMessageID != "" {
			err := b.service.DeleteMessage(channel.ChannelID, channel.LastMessageID)
			if err != nil {
				log.Printf("Failed to delete message from channel %s: %v", channel.ChannelID, err)
			} else {
				successCount++
				// Clear last message ID in database
				if err := b.repo.UpdateChannelLastMessageID(channel.ChannelID, ""); err != nil {
					log.Printf("Failed to clear last message ID for channel %s: %v", channel.ChannelID, err)
				}
			}
		}
	}

	successMsg := fmt.Sprintf("🗑️ *删除完成*\n\n"+
		"📋 频道组：%s\n"+
		"📢 成功删除：%d/%d 个频道的消息",
		group.Name,
		successCount,
		len(channels))

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	b.sendMainMenu(chatID)
}

// handlePushMessage handles custom push message input
func (b *Bot) handlePushMessage(chatID int64, input string, userState *UserState) {
	if strings.TrimSpace(input) == "" {
		b.sendMessage(chatID, "❌ 消息内容不能为空，请重新输入：")
		return
	}

	groupID := userState.Data["groupID"].(int64)

	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Get channels for this group
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	if len(channels) == 0 {
		b.clearState(chatID)
		b.sendMessage(chatID, "该组没有绑定的频道。")
		return
	}

	messageContent := strings.TrimSpace(input)

	// Send messages to all channels
	successCount := 0
	for _, channel := range channels {
		if channel.IsActive {
			var messageID string
			var err error

			// Check if we have entities stored in user state
			if userState.Data["entities"] != nil {
				// Send with entities to preserve formatting
				entities := userState.Data["entities"].([]tgbotapi.MessageEntity)
				log.Printf("Sending push message with %d entities to channel %s", len(entities), channel.ChannelID)
				messageID, err = b.service.SendMessageWithEntities(channel.ChannelID, messageContent, entities)
			} else {
				// Create temporary message template for fallback
				template := &models.MessageTemplate{
					Title:       "临时推送消息",
					Content:     messageContent,
					MessageType: models.MessageTypeText,
					MediaURL:    "",
					Buttons:     models.InlineKeyboard{},
				}
				log.Printf("Sending push message without entities to channel %s", channel.ChannelID)
				messageID, err = b.service.SendMessage(channel.ChannelID, template)
			}

			if err != nil {
				log.Printf("Failed to send push message to channel %s: %v", channel.ChannelID, err)
			} else {
				successCount++
				// Update last message ID in database
				if err := b.repo.UpdateChannelLastMessageID(channel.ChannelID, messageID); err != nil {
					log.Printf("Failed to update last message ID for channel %s: %v", channel.ChannelID, err)
				}
			}
		}
	}

	b.clearState(chatID)

	successMsg := fmt.Sprintf("📢 *推送完成*\n\n"+
		"📋 频道组：%s\n"+
		"📢 成功推送：%d/%d 个频道\n"+
		"💬 消息内容：%s",
		group.Name,
		successCount,
		len(channels),
		messageContent)

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	b.sendMainMenu(chatID)
}

// handleInputPushMessage handles input push message and shows group selection
func (b *Bot) handleInputPushMessage(chatID int64, input string, userState *UserState) {
	if strings.TrimSpace(input) == "" {
		b.sendMessage(chatID, "❌ 消息内容不能为空，请重新输入：")
		return
	}

	messageContent := strings.TrimSpace(input)

	// Store message content and entities in user state
	messageData := map[string]interface{}{
		"message_content": messageContent,
		"message_type":    "text",
	}

	// Check if we have entities from the original message
	if userState.Data["entities"] != nil {
		messageData["entities"] = userState.Data["entities"]
		log.Printf("Preserving %d entities for push message", len(userState.Data["entities"].([]tgbotapi.MessageEntity)))
	} else {
		log.Printf("No entities found for push message")
	}

	// Update state to show group selection
	b.setState(chatID, "custom_message_content", messageData)

	// Show group selection for push
	b.showGroupSelectionForCustomPush(chatID, messageContent)
}

// handleInputPushMessageWithEntities handles input push message with entities preservation
func (b *Bot) handleInputPushMessageWithEntities(chatID int64, message *tgbotapi.Message, userState *UserState) {
	var messageContent string
	var messageType string
	var mediaURL string

	// Check message type and extract content
	if message.Photo != nil && len(message.Photo) > 0 {
		// Photo message
		messageType = "photo"
		messageContent = message.Caption
		// Get the largest photo size
		photo := message.Photo[len(message.Photo)-1]
		mediaURL = photo.FileID
		log.Printf("Received photo push message with FileID: %s", mediaURL)
	} else if message.Text != "" {
		// Text message
		messageType = "text"
		messageContent = strings.TrimSpace(message.Text)
		mediaURL = ""
	} else {
		b.sendMessage(chatID, "❌ 请发送文字消息或图片消息")
		return
	}

	// Store message content and entities in user state
	messageData := map[string]interface{}{
		"message_content": messageContent,
		"message_type":    messageType,
		"media_url":       mediaURL,
	}

	// Store entities if they exist (for preserving formatting like links)
	if messageType == "photo" && message.CaptionEntities != nil && len(message.CaptionEntities) > 0 {
		log.Printf("Storing %d caption entities for photo push message", len(message.CaptionEntities))
		messageData["entities"] = message.CaptionEntities
	} else if messageType == "text" && message.Entities != nil && len(message.Entities) > 0 {
		log.Printf("Storing %d text entities for push message", len(message.Entities))
		messageData["entities"] = message.Entities
	} else {
		log.Printf("No entities found in push message")
	}

	// Update state to show group selection
	b.setState(chatID, "custom_message_content", messageData)

	// Ask if user wants to add buttons for this push
	b.askForPushButtons(chatID, messageContent)
}

// Channel Management Functions

// handleAddChannelCallback handles add channel callback
func (b *Bot) handleAddChannelCallback(chatID int64, data string) {
	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 3 {
		b.sendMessage(chatID, "无效的操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Set user state to add channel
	b.setState(chatID, "add_channel_to_group", map[string]interface{}{
		"groupID": groupID,
	})

	b.sendMessage(chatID, "📢 *添加频道到组*\n\n请输入频道信息，支持批量添加：\n\n**单个频道格式：**\n`频道名称|频道ID`\n例如：`精品频道A|@channel1` 或 `测试频道|-1001234567890`\n\n**批量添加（一行一个）：**\n```\n精品频道A|@channel1\n测试频道B|@channel2\n备用频道|-1001234567890\n主频道|-1009876543210\n```\n\n**注意：** 如果只输入频道ID（不含|），将使用频道ID作为名称\n\n请输入：")
}

// handleAddChannelToGroup handles adding channel(s) to group (supports batch)
func (b *Bot) handleAddChannelToGroup(chatID int64, input string, userState *UserState) {
	input = strings.TrimSpace(input)
	if input == "" {
		b.sendMessage(chatID, "❌ 频道ID不能为空，请重新输入：")
		return
	}

	groupID := userState.Data["groupID"].(int64)

	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Parse channel information (support multiple lines)
	lines := strings.Split(input, "\n")
	var channelInfos []struct {
		Name string
		ID   string
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var channelName, channelID string

		// Check if line contains name|id format
		if strings.Contains(line, "|") {
			parts := strings.Split(line, "|")
			if len(parts) != 2 {
				b.sendMessage(chatID, fmt.Sprintf("❌ 无效的格式：%s\n\n请使用格式：频道名称|频道ID", line))
				return
			}
			channelName = strings.TrimSpace(parts[0])
			channelID = strings.TrimSpace(parts[1])

			if channelName == "" {
				b.sendMessage(chatID, fmt.Sprintf("❌ 频道名称不能为空：%s", line))
				return
			}
		} else {
			// Only channel ID provided, use ID as name
			channelID = line
			channelName = line
		}

		// Validate channel ID format
		if !strings.HasPrefix(channelID, "@") && !strings.HasPrefix(channelID, "-100") {
			b.sendMessage(chatID, fmt.Sprintf("❌ 无效的频道ID格式：%s\n\n频道ID应该以@开头（如@channel1）或以-100开头（如-1001234567890）", channelID))
			return
		}

		channelInfos = append(channelInfos, struct {
			Name string
			ID   string
		}{
			Name: channelName,
			ID:   channelID,
		})
	}

	if len(channelInfos) == 0 {
		b.sendMessage(chatID, "❌ 没有找到有效的频道信息，请重新输入：")
		return
	}

	// Process each channel
	var successChannels []string
	var failedChannels []string

	for _, info := range channelInfos {
		// Create channel
		channel := &models.Channel{
			ChannelID:     info.ID,
			ChannelName:   info.Name,
			GroupID:       groupID,
			LastMessageID: "",
			IsActive:      true,
		}

		err = b.repo.CreateChannel(channel)
		if err != nil {
			failedChannels = append(failedChannels, fmt.Sprintf("%s (%s)", info.Name, err.Error()))
		} else {
			successChannels = append(successChannels, info.Name)
		}
	}

	b.clearState(chatID)

	// Build result message
	var resultMsg string
	if len(successChannels) > 0 {
		resultMsg += fmt.Sprintf("✅ *成功添加 %d 个频道：*\n", len(successChannels))
		for _, channelName := range successChannels {
			resultMsg += fmt.Sprintf("📢 %s\n", channelName)
		}
		resultMsg += "\n"
	}

	if len(failedChannels) > 0 {
		resultMsg += fmt.Sprintf("❌ *添加失败 %d 个频道：*\n", len(failedChannels))
		for _, failedInfo := range failedChannels {
			resultMsg += fmt.Sprintf("📢 %s\n", failedInfo)
		}
		resultMsg += "\n"
	}

	resultMsg += fmt.Sprintf("📋 频道组：%s\n\n继续添加更多频道或查看组详情。", group.Name)

	// Show options to continue
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ 继续添加频道", fmt.Sprintf("add_channel_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📋 查看组详情", fmt.Sprintf("group_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回主菜单", "main_menu"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, resultMsg)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// handleDeleteChannelAction handles delete channel action
func (b *Bot) handleDeleteChannelAction(chatID int64, data string) {
	// Parse callback data: delete_channel_{groupID}_{channelID}
	parts := strings.Split(data, "_")
	if len(parts) != 4 {
		b.sendMessage(chatID, "无效的删除操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	channelID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的频道ID。")
		return
	}

	// Get channel details
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载频道信息时出错。")
		return
	}

	var targetChannel *models.Channel
	for _, channel := range channels {
		if channel.ID == channelID {
			targetChannel = &channel
			break
		}
	}

	if targetChannel == nil {
		b.sendMessage(chatID, "未找到指定的频道。")
		return
	}

	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Show confirmation dialog
	text := fmt.Sprintf("🗑️ *确认删除频道*\n\n"+
		"📋 频道组：%s\n"+
		"📢 频道：%s (%s)\n\n"+
		"⚠️ 此操作不可撤销，确定要删除这个频道吗？",
		group.Name,
		targetChannel.ChannelName,
		targetChannel.ChannelID)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ 确认删除", fmt.Sprintf("confirm_delete_channel_%d_%d", groupID, channelID)),
			tgbotapi.NewInlineKeyboardButtonData("❌ 取消", fmt.Sprintf("manage_channels_%d", groupID)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// handleConfirmDeleteChannelAction handles confirm delete channel action
func (b *Bot) handleConfirmDeleteChannelAction(chatID int64, data string) {
	// Parse callback data: confirm_delete_channel_{groupID}_{channelID}
	parts := strings.Split(data, "_")
	if len(parts) != 5 {
		b.sendMessage(chatID, "无效的删除操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	channelID, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的频道ID。")
		return
	}

	// Get channel details before deletion
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载频道信息时出错。")
		return
	}

	var targetChannel *models.Channel
	for _, channel := range channels {
		if channel.ID == channelID {
			targetChannel = &channel
			break
		}
	}

	if targetChannel == nil {
		b.sendMessage(chatID, "未找到指定的频道。")
		return
	}

	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Delete the channel from database
	err = b.repo.DeleteChannel(channelID)
	if err != nil {
		b.sendMessage(chatID, "❌ 删除频道失败："+err.Error())
		return
	}

	// Send success message
	successMsg := fmt.Sprintf("✅ *频道删除成功*\n\n"+
		"📋 频道组：%s\n"+
		"📢 已删除频道：%s (%s)",
		group.Name,
		targetChannel.ChannelName,
		targetChannel.ChannelID)

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	// Return to channel management
	b.showChannelManagement(chatID, groupID)
}

// Message Handling Functions

// handleMessageForSending handles message for sending - show preview and options
func (b *Bot) handleMessageForSending(chatID int64, message *tgbotapi.Message) {
	// Create preview text with actual message content
	previewText := fmt.Sprintf("📝 消息预览\n\n%s\n\n请选择操作：", message.Text)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📢 推送到频道组", "preview_push"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 重发到频道组", "preview_repost"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回主菜单", "main_menu"),
		),
	)

	// Store message content and entities in user state
	messageData := map[string]interface{}{
		"message_content": message.Text,
		"message_type":    "text",
	}

	// Store entities if they exist (for preserving formatting like links)
	if message.Entities != nil && len(message.Entities) > 0 {
		log.Printf("Storing %d entities for message: %v", len(message.Entities), message.Entities)
		messageData["entities"] = message.Entities
	} else {
		log.Printf("No entities found in message")
	}

	b.setState(chatID, "custom_message_content", messageData)

	msg := tgbotapi.NewMessage(chatID, previewText)
	// Use entities for preview to match actual message format
	if message.Entities != nil && len(message.Entities) > 0 {
		// Adjust entity offsets for preview prefix
		previewPrefix := "📝 消息预览\n\n"
		adjustedEntities := b.adjustEntitiesForPreview(message.Entities, len([]byte(previewPrefix)))
		msg.Entities = adjustedEntities
	}
	msg.DisableWebPagePreview = true
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// handleCustomMessageContent handles custom message content (placeholder)
func (b *Bot) handleCustomMessageContent(chatID int64, input string, userState *UserState) {
	// This function is called when user is in custom_message_content state
	// For now, just clear state and show main menu
	b.clearState(chatID)
	b.sendMessage(chatID, "操作已取消。")
	b.sendMainMenu(chatID)
}

// Preview Message Functions

// handlePreviewPush handles preview push action
func (b *Bot) handlePreviewPush(chatID int64) {
	// Check if user has message content in state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || userState.Data["message_content"] == nil {
		b.sendMessage(chatID, "❌ 没有找到消息内容，请重新发送消息。")
		b.sendMainMenu(chatID)
		return
	}

	// Show group selection for push with custom message
	b.showGroupSelectionForCustomPush(chatID, userState.Data["message_content"].(string))
}

// handlePreviewRepost handles preview repost action
func (b *Bot) handlePreviewRepost(chatID int64) {
	// Check if user has message content in state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || userState.Data["message_content"] == nil {
		b.sendMessage(chatID, "❌ 没有找到消息内容，请重新发送消息。")
		b.sendMainMenu(chatID)
		return
	}

	// Show group selection for repost with custom message
	b.showGroupSelectionForCustomRepost(chatID, userState.Data["message_content"].(string))
}

// showGroupSelectionForCustomPush shows group selection for custom push
func (b *Bot) showGroupSelectionForCustomPush(chatID int64, messageContent string) {
	groups, err := b.repo.GetChannelGroups()
	if err != nil {
		b.sendMessage(chatID, "加载频道组时出错。")
		return
	}

	if len(groups) == 0 {
		b.sendMessage(chatID, "没有可用的频道组。请先创建频道组。")
		return
	}

	text := "📢 *推送自定义消息*\n\n选择要推送的频道组："
	var keyboard [][]tgbotapi.InlineKeyboardButton

	for _, group := range groups {
		status := "🔴"
		if group.IsActive {
			status = "🟢"
		}
		buttonText := fmt.Sprintf("%s %s", status, group.Name)
		buttonData := fmt.Sprintf("custom_push_%d", group.ID)
		keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText, buttonData),
		))
	}

	keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "main_menu"),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)
	b.api.Send(msg)
}

// showGroupSelectionForCustomRepost shows group selection for custom repost
func (b *Bot) showGroupSelectionForCustomRepost(chatID int64, messageContent string) {
	groups, err := b.repo.GetChannelGroups()
	if err != nil {
		b.sendMessage(chatID, "加载频道组时出错。")
		return
	}

	if len(groups) == 0 {
		b.sendMessage(chatID, "没有可用的频道组。请先创建频道组。")
		return
	}

	text := "🔄 *重发自定义消息*\n\n选择要重发的频道组："
	var keyboard [][]tgbotapi.InlineKeyboardButton

	for _, group := range groups {
		status := "🔴"
		if group.IsActive {
			status = "🟢"
		}
		buttonText := fmt.Sprintf("%s %s", status, group.Name)
		buttonData := fmt.Sprintf("custom_repost_%d", group.ID)
		keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText, buttonData),
		))
	}

	keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "main_menu"),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)
	b.api.Send(msg)
}

// Custom Message Action Handlers

// handleCustomPushAction handles custom push action for a specific group
func (b *Bot) handleCustomPushAction(chatID int64, data string) {
	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 3 {
		b.sendMessage(chatID, "无效的推送操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Get operation lock for this group to prevent concurrent operations
	lock := b.getOperationLock(groupID)
	if !lock.TryLock() {
		b.sendMessage(chatID, "⚠️ 该频道组正在处理其他操作，请稍后再试。")
		return
	}
	defer func() {
		lock.Unlock()
		// Clean up the lock after operation completes
		go func() {
			time.Sleep(1 * time.Second)
			b.cleanupOperationLock(groupID)
		}()
	}()

	// Get message content from user state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || userState.Data["message_content"] == nil {
		b.sendMessage(chatID, "❌ 没有找到消息内容，请重新发送消息。")
		b.sendMainMenu(chatID)
		return
	}

	messageContent := userState.Data["message_content"].(string)

	// Execute custom push
	b.executeCustomPush(chatID, groupID, messageContent)
}

// handleCustomRepostAction handles custom repost action for a specific group
func (b *Bot) handleCustomRepostAction(chatID int64, data string) {
	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 3 {
		b.sendMessage(chatID, "无效的重发操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Get operation lock for this group to prevent concurrent operations
	lock := b.getOperationLock(groupID)
	if !lock.TryLock() {
		b.sendMessage(chatID, "⚠️ 该频道组正在处理其他操作，请稍后再试。")
		return
	}
	defer func() {
		lock.Unlock()
		// Clean up the lock after operation completes
		go func() {
			time.Sleep(1 * time.Second)
			b.cleanupOperationLock(groupID)
		}()
	}()

	// Get message content from user state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || userState.Data["message_content"] == nil {
		b.sendMessage(chatID, "❌ 没有找到消息内容，请重新发送消息。")
		b.sendMainMenu(chatID)
		return
	}

	messageContent := userState.Data["message_content"].(string)

	// Execute custom repost
	b.executeCustomRepost(chatID, groupID, messageContent)
}

// executeCustomPush executes custom push to a specific group
func (b *Bot) executeCustomPush(chatID int64, groupID int64, messageContent string) {
	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Get channels for this group
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	if len(channels) == 0 {
		b.clearState(chatID)
		b.sendMessage(chatID, "该组没有绑定的频道。")
		return
	}

	// Get user state to extract message data
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 消息数据丢失，请重新操作。")
		return
	}

	// Extract message data from user state
	messageType := "text"
	mediaURL := ""
	if userState.Data["message_type"] != nil {
		messageType = userState.Data["message_type"].(string)
	}
	if userState.Data["media_url"] != nil {
		mediaURL = userState.Data["media_url"].(string)
	}

	// Create temporary message template
	var templateMessageType models.MessageType
	if messageType == "photo" {
		templateMessageType = models.MessageTypePhoto
	} else {
		templateMessageType = models.MessageTypeText
	}

	template := &models.MessageTemplate{
		Title:       "自定义推送消息",
		Content:     messageContent,
		MessageType: templateMessageType,
		MediaURL:    mediaURL,
		Buttons:     models.InlineKeyboard{},
	}

	// Check if we have push buttons in user state
	if exists && userState.Data["push_buttons"] != nil {
		pushButtons := userState.Data["push_buttons"].([][]models.InlineKeyboardButton)
		template.Buttons = pushButtons
		log.Printf("Using %d button rows for push message", len(pushButtons))
	}

	// Send messages to all channels (push - don't delete previous) with rate limiting
	successCount := 0
	for i, channel := range channels {
		if channel.IsActive {
			// Rate limiting: delay between channels to avoid API limits
			if i > 0 {
				time.Sleep(300 * time.Millisecond) // 300ms delay for push messages
				log.Printf("Rate limiting: waiting 300ms before sending push to channel %s", channel.ChannelID)
			}

			var messageID string
			var err error

			// Check if we have entities stored in user state
			var entities []tgbotapi.MessageEntity
			if exists && userState.Data["entities"] != nil {
				entities = userState.Data["entities"].([]tgbotapi.MessageEntity)
				log.Printf("Using %d entities for push message", len(entities))
			}

			// Send with complete template (entities and buttons)
			messageID, err = b.service.SendMessageWithTemplate(channel.ChannelID, template, entities)

			if err != nil {
				log.Printf("Failed to send custom push message to channel %s: %v", channel.ChannelID, err)
			} else {
				successCount++
				// Update last message ID in database
				if err := b.repo.UpdateChannelLastMessageID(channel.ChannelID, messageID); err != nil {
					log.Printf("Failed to update last message ID for channel %s: %v", channel.ChannelID, err)
				}
			}
		}
	}

	b.clearState(chatID)

	// Create success message based on message type
	var typeIcon, typeText string
	if messageType == "photo" {
		typeIcon = "📸"
		typeText = "图片消息"
	} else {
		typeIcon = "📝"
		typeText = "文字消息"
	}

	successMsg := fmt.Sprintf("📢 *自定义推送完成*\n\n"+
		"📋 频道组：%s\n"+
		"📢 成功推送：%d/%d 个频道\n"+
		"%s 消息类型：%s\n"+
		"💬 消息内容：%s",
		group.Name,
		successCount,
		len(channels),
		typeIcon,
		typeText,
		messageContent)

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	b.sendMainMenu(chatID)
}

// executeCustomRepost executes custom repost to a specific group
func (b *Bot) executeCustomRepost(chatID int64, groupID int64, messageContent string) {
	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Get channels for this group
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	if len(channels) == 0 {
		b.clearState(chatID)
		b.sendMessage(chatID, "该组没有绑定的频道。")
		return
	}

	// Create temporary message template
	template := &models.MessageTemplate{
		Title:       "自定义重发消息",
		Content:     messageContent,
		MessageType: models.MessageTypeText,
		MediaURL:    "",
		Buttons:     models.InlineKeyboard{},
	}

	// Send messages to all channels (repost - delete previous first) with rate limiting
	successCount := 0
	for i, channel := range channels {
		if channel.IsActive {
			// Rate limiting: delay between channels to avoid API limits
			if i > 0 {
				time.Sleep(400 * time.Millisecond) // 400ms delay for repost messages (includes delete operation)
				log.Printf("Rate limiting: waiting 400ms before sending repost to channel %s", channel.ChannelID)
			}

			// Delete previous message if exists (repost behavior)
			if channel.LastMessageID != "" {
				err := b.service.DeleteMessage(channel.ChannelID, channel.LastMessageID)
				if err != nil {
					log.Printf("Failed to delete previous message from channel %s: %v", channel.ChannelID, err)
				}
			}

			// Send new message
			var messageID string
			var err error

			// Check if we have entities stored in user state
			b.stateMutex.RLock()
			userState, exists := b.userStates[chatID]
			b.stateMutex.RUnlock()

			if exists && userState.Data["entities"] != nil {
				// Send with entities to preserve formatting
				entities := userState.Data["entities"].([]tgbotapi.MessageEntity)
				messageID, err = b.service.SendMessageWithEntities(channel.ChannelID, messageContent, entities)
			} else {
				// Send as regular template
				messageID, err = b.service.SendMessage(channel.ChannelID, template)
			}

			if err != nil {
				log.Printf("Failed to send custom repost message to channel %s: %v", channel.ChannelID, err)
			} else {
				successCount++

				// Pin message if auto pin is enabled
				if group.AutoPin {
					log.Printf("Auto pin is enabled for group %s, attempting to pin message %s", group.Name, messageID)
					if err := b.service.PinMessage(channel.ChannelID, messageID); err != nil {
						log.Printf("Failed to pin message %s in channel %s: %v", messageID, channel.ChannelID, err)
						// Don't fail the entire operation if pinning fails
					} else {
						log.Printf("Successfully pinned message %s in channel %s", messageID, channel.ChannelID)
					}
				}

				// Update last message ID in database
				if err := b.repo.UpdateChannelLastMessageID(channel.ChannelID, messageID); err != nil {
					log.Printf("Failed to update last message ID for channel %s: %v", channel.ChannelID, err)
				}
			}
		}
	}

	b.clearState(chatID)

	successMsg := fmt.Sprintf("🔄 *自定义重发完成*\n\n"+
		"📋 频道组：%s\n"+
		"📢 成功重发：%d/%d 个频道\n"+
		"💬 消息内容：%s",
		group.Name,
		successCount,
		len(channels),
		messageContent)

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	b.sendMainMenu(chatID)
}

// Edit Group Input Handlers

// handleEditGroupName handles editing group name
func (b *Bot) handleEditGroupName(chatID int64, input string, userState *UserState) {
	if strings.TrimSpace(input) == "" {
		b.sendMessage(chatID, "❌ 名称不能为空，请重新输入：")
		return
	}

	groupID := userState.Data["groupID"].(int64)
	newName := strings.TrimSpace(input)

	// Update group name in database
	err := b.repo.UpdateChannelGroupName(groupID, newName)
	if err != nil {
		b.sendMessage(chatID, "❌ 更新名称失败："+err.Error())
		return
	}

	b.clearState(chatID)
	b.sendMessage(chatID, fmt.Sprintf("✅ 频道组名称已更新为：%s", newName))

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// handleEditGroupDesc handles editing group description
func (b *Bot) handleEditGroupDesc(chatID int64, input string, userState *UserState) {
	groupID := userState.Data["groupID"].(int64)
	newDesc := strings.TrimSpace(input)

	// Update group description in database
	err := b.repo.UpdateChannelGroupDescription(groupID, newDesc)
	if err != nil {
		b.sendMessage(chatID, "❌ 更新描述失败："+err.Error())
		return
	}

	b.clearState(chatID)
	b.sendMessage(chatID, "✅ 频道组描述已更新")

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// handleEditGroupFreq handles editing group frequency
func (b *Bot) handleEditGroupFreq(chatID int64, input string, userState *UserState) {
	frequency, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || frequency <= 0 {
		b.sendMessage(chatID, "❌ 请输入有效的数字（大于0的分钟数）：")
		return
	}

	groupID := userState.Data["groupID"].(int64)

	// Update group frequency in database
	err = b.repo.UpdateChannelGroupFrequency(groupID, frequency)
	if err != nil {
		b.sendMessage(chatID, "❌ 更新频率失败："+err.Error())
		return
	}

	b.clearState(chatID)
	b.sendMessage(chatID, fmt.Sprintf("✅ 发送频率已更新为：%d 分钟", frequency))

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// handleEditTimepoints handles editing timepoints
func (b *Bot) handleEditTimepoints(chatID int64, input string, userState *UserState) {
	input = strings.TrimSpace(input)
	if input == "" {
		b.sendMessage(chatID, "❌ 时间点不能为空，请重新输入：")
		return
	}

	groupID := userState.Data["groupID"].(int64)

	// Parse timepoints
	timepoints, err := b.parseTimepoints(input)
	if err != nil {
		b.sendMessage(chatID, "❌ "+err.Error()+"\n\n请重新输入，格式：HH:MM（每行一个）")
		return
	}

	// Get current group
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Update timepoints
	group.ScheduleTimepoints = timepoints
	err = b.repo.UpdateChannelGroup(group)
	if err != nil {
		b.sendMessage(chatID, "❌ 更新时间点失败："+err.Error())
		return
	}

	b.clearState(chatID)

	// Build success message
	var timepointsList string
	for _, tp := range timepoints {
		timepointsList += fmt.Sprintf(" %02d:%02d", tp.Hour, tp.Minute)
	}

	successMsg := fmt.Sprintf("✅ 时间点已更新\n\n🕐 发送时间：%s", timepointsList)
	b.sendMessage(chatID, successMsg)

	// Return to schedule settings
	b.showScheduleSettings(chatID, groupID)
}

// parseTimepoints parses timepoint input string
func (b *Bot) parseTimepoints(input string) (models.TimePoints, error) {
	lines := strings.Split(input, "\n")
	var timepoints models.TimePoints

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse HH:MM format
		parts := strings.Split(line, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("第%d行格式错误：%s（应为 HH:MM）", i+1, line)
		}

		hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || hour < 0 || hour > 23 {
			return nil, fmt.Errorf("第%d行小时无效：%s（应为 00-23）", i+1, parts[0])
		}

		minute, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || minute < 0 || minute > 59 {
			return nil, fmt.Errorf("第%d行分钟无效：%s（应为 00-59）", i+1, parts[1])
		}

		timepoints = append(timepoints, models.TimePoint{
			Hour:   hour,
			Minute: minute,
		})
	}

	if len(timepoints) == 0 {
		return nil, fmt.Errorf("至少需要输入一个时间点")
	}

	return timepoints, nil
}

// handleEditGroupTemplate handles editing group template
func (b *Bot) handleEditGroupTemplate(chatID int64, input string, userState *UserState) {
	log.Printf("DEBUG: Called OLD handleEditGroupTemplate for user %d", chatID)
	if strings.TrimSpace(input) == "" {
		b.sendMessage(chatID, "❌ 模板内容不能为空，请重新输入：")
		return
	}

	groupID := userState.Data["groupID"].(int64)
	newContent := strings.TrimSpace(input)

	// Get group to find template ID
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Check if we have entities stored in user state
	var entitiesJSON string
	if userState.Data["entities"] != nil {
		entities := userState.Data["entities"].([]tgbotapi.MessageEntity)
		if len(entities) > 0 {
			// Serialize entities to JSON
			entitiesBytes, err := json.Marshal(entities)
			if err != nil {
				log.Printf("Failed to serialize entities: %v", err)
			} else {
				entitiesJSON = string(entitiesBytes)
				log.Printf("Saving %d entities for template: %s", len(entities), entitiesJSON)
			}
		}
	}

	// Update template content and entities in database
	if entitiesJSON != "" {
		err = b.repo.UpdateMessageTemplateContentAndEntities(group.MessageID, newContent, entitiesJSON)
	} else {
		err = b.repo.UpdateMessageTemplateContent(group.MessageID, newContent)
	}

	if err != nil {
		b.sendMessage(chatID, "❌ 更新模板失败："+err.Error())
		return
	}

	b.clearState(chatID)
	b.sendMessage(chatID, "✅ 消息模板已更新")

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// handleEditGroupTemplateWithEntities handles editing group template with entities preservation
func (b *Bot) handleEditGroupTemplateWithEntities(chatID int64, message *tgbotapi.Message, userState *UserState) {
	groupID := userState.Data["groupID"].(int64)

	// Get group to find template ID
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	var messageType models.MessageType
	var content string
	var mediaURL string
	var entitiesJSON string

	// Check message type and extract content
	if message.Photo != nil && len(message.Photo) > 0 {
		// Photo message
		messageType = models.MessageTypePhoto
		content = message.Caption
		// Get the largest photo size
		photo := message.Photo[len(message.Photo)-1]
		mediaURL = photo.FileID
		log.Printf("Received photo message with FileID: %s", mediaURL)

		// Extract entities from caption
		if message.CaptionEntities != nil && len(message.CaptionEntities) > 0 {
			entitiesBytes, err := json.Marshal(message.CaptionEntities)
			if err != nil {
				log.Printf("Failed to serialize caption entities: %v", err)
			} else {
				entitiesJSON = string(entitiesBytes)
				log.Printf("Saving %d caption entities for photo template", len(message.CaptionEntities))
			}
		}
	} else if message.Text != "" {
		// Text message
		messageType = models.MessageTypeText
		content = strings.TrimSpace(message.Text)
		mediaURL = ""

		// Extract entities from text
		if message.Entities != nil && len(message.Entities) > 0 {
			entitiesBytes, err := json.Marshal(message.Entities)
			if err != nil {
				log.Printf("Failed to serialize text entities: %v", err)
			} else {
				entitiesJSON = string(entitiesBytes)
				log.Printf("Saving %d text entities for template", len(message.Entities))
			}
		}
	} else {
		b.sendMessage(chatID, "❌ 请发送文字消息或图片消息作为模板内容")
		return
	}

	// Update template with new content, type, and media
	err = b.repo.UpdateMessageTemplateComplete(group.MessageID, content, string(messageType), mediaURL, entitiesJSON)

	if err != nil {
		b.sendMessage(chatID, "❌ 更新模板失败："+err.Error())
		return
	}

	b.clearState(chatID)

	// Send success message based on message type
	if messageType == models.MessageTypePhoto {
		// For photo template, send the actual photo with success message
		if mediaURL != "" {
			photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(mediaURL))
			photoMsg.Caption = fmt.Sprintf("✅ 图片消息模板已更新\n\n📸 类型：图片消息\n💬 说明文字：%s", content)
			// Use entities for the caption part (adjust offset for prefix)
			if message.CaptionEntities != nil && len(message.CaptionEntities) > 0 {
				prefixText := fmt.Sprintf("✅ 图片消息模板已更新\n\n📸 类型：图片消息\n💬 说明文字：")
				adjustedEntities := b.adjustEntitiesForPreview(message.CaptionEntities, len([]byte(prefixText)))
				photoMsg.CaptionEntities = adjustedEntities
			}
			b.api.Send(photoMsg)
		} else {
			// Fallback to text if no media
			successMsg := "✅ 图片消息模板已更新\n\n📸 类型：图片消息\n💬 说明文字：" + content
			msg := tgbotapi.NewMessage(chatID, successMsg)
			b.api.Send(msg)
		}
	} else {
		// For text template, send text message with entities
		successMsg := fmt.Sprintf("✅ 文字消息模板已更新\n\n📝 类型：文字消息\n💬 内容：%s", content)
		msg := tgbotapi.NewMessage(chatID, successMsg)
		// Use entities for the content part (adjust offset for prefix)
		if message.Entities != nil && len(message.Entities) > 0 {
			prefixText := "✅ 文字消息模板已更新\n\n📝 类型：文字消息\n💬 内容："
			adjustedEntities := b.adjustEntitiesForPreview(message.Entities, len([]byte(prefixText)))
			msg.Entities = adjustedEntities
		}
		msg.DisableWebPagePreview = true
		b.api.Send(msg)
	}

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// askForButtons asks user if they want to add buttons to the template
func (b *Bot) askForButtons(chatID int64, groupID int64) {
	text := "🔘 *添加按钮（可选）*\n\n是否要为消息模板添加按钮？"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ 添加按钮", fmt.Sprintf("add_buttons_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏭️ 跳过", fmt.Sprintf("skip_buttons_%d", groupID)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// handleManageButtonsAction handles manage buttons action
func (b *Bot) handleManageButtonsAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "manage_buttons_")
	if groupID == 0 {
		return
	}

	// Get group to find template
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Get template to check current buttons
	template, err := b.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		b.sendMessage(chatID, "❌ 加载模板失败："+err.Error())
		return
	}

	// Build button list text
	var buttonText string
	if len(template.Buttons) == 0 {
		buttonText = "当前没有按钮"
	} else {
		buttonText = "当前按钮：\n"
		for _, row := range template.Buttons {
			for _, button := range row {
				buttonText += fmt.Sprintf("%s|%s\n", button.Text, button.URL)
			}
		}
	}

	text := fmt.Sprintf("🔘 *按钮管理: %s*\n\n%s", group.Name, buttonText)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ 添加按钮", fmt.Sprintf("add_button_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 预览消息", fmt.Sprintf("preview_message_%d", groupID)),
		),
	)

	// Add clear buttons option if there are buttons
	if len(template.Buttons) > 0 {
		keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🗑️ 清空按钮", fmt.Sprintf("clear_buttons_%d", groupID)),
			),
		)
	}

	// Add back button
	keyboard.InlineKeyboard = append(keyboard.InlineKeyboard,
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回编辑选项", fmt.Sprintf("edit_group_%d", groupID)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// handleAddButtonsAction handles add buttons action
func (b *Bot) handleAddButtonsAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "add_buttons_")
	if groupID == 0 {
		return
	}

	b.setState(chatID, "add_buttons", map[string]interface{}{
		"groupID": groupID,
		"buttons": [][]models.InlineKeyboardButton{},
		"layout":  "single", // default layout
	})

	b.sendMessage(chatID, "🔘 *批量添加按钮*\n\n请输入按钮信息，支持批量输入，一行一个按钮：\n\n**格式：**\n`按钮文字|链接URL`\n\n**示例：**\n```\n💎 站长仓库|https://t.me/zhanzhangck\n👀  站长交流群|https://t.me/vpsbbq\n🔥 更多资源|https://example.com\n```\n\n💡 **提示：**\n• 可以一次性输入多个按钮\n• 每行一个按钮\n• 空行会被忽略\n• 也支持单个按钮输入")
}

// handleSkipButtonsAction handles skip buttons action
func (b *Bot) handleSkipButtonsAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "skip_buttons_")
	if groupID == 0 {
		return
	}

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// handleAddButtons handles adding buttons to template (supports batch input)
func (b *Bot) handleAddButtons(chatID int64, input string, userState *UserState) {
	input = strings.TrimSpace(input)

	// Get layout preference
	layout := "single" // default
	if userState.Data["layout"] != nil {
		layout = userState.Data["layout"].(string)
	}

	// Parse batch buttons
	buttonRows, err := b.parseBatchButtons(input, layout)
	if err != nil {
		b.sendMessage(chatID, "❌ "+err.Error()+"\n\n**格式示例：**\n```\n💎 立即前往网站|https://t.me/xxxx/2\n👀 查看群组|https://t.me/vpsbbq\n```")
		return
	}

	// Replace all buttons with new ones
	userState.Data["buttons"] = buttonRows

	// Count total buttons
	totalButtons := 0
	for _, row := range buttonRows {
		totalButtons += len(row)
	}

	layoutText := "单列"
	if layout == "double" {
		layoutText = "双列"
	}

	b.sendMessage(chatID, fmt.Sprintf("✅ 成功添加 %d 个按钮（%s布局）\n\n按钮预览：", totalButtons, layoutText))

	// Show button preview
	previewText := ""
	for i, row := range buttonRows {
		previewText += fmt.Sprintf("第%d行：", i+1)
		for j, button := range row {
			if j > 0 {
				previewText += " | "
			}
			previewText += button.Text
		}
		previewText += "\n"
	}

	b.sendMessage(chatID, previewText)

	// Finish adding buttons automatically
	b.finishAddingButtons(chatID, userState)
}

// parseBatchButtons parses multiple buttons from input text
func (b *Bot) parseBatchButtons(input string, layout string) ([][]models.InlineKeyboardButton, error) {
	lines := strings.Split(strings.TrimSpace(input), "\n")
	var allButtons []models.InlineKeyboardButton

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue // Skip empty lines
		}

		// Parse button input: "text|url"
		parts := strings.Split(line, "|")
		if len(parts) != 2 {
			return nil, fmt.Errorf("第%d行格式错误：%s\n请使用格式：按钮文字|链接URL", i+1, line)
		}

		buttonText := strings.TrimSpace(parts[0])
		buttonURL := strings.TrimSpace(parts[1])

		if buttonText == "" || buttonURL == "" {
			return nil, fmt.Errorf("第%d行按钮文字和链接都不能为空：%s", i+1, line)
		}

		// Validate URL
		if !strings.HasPrefix(buttonURL, "http://") && !strings.HasPrefix(buttonURL, "https://") {
			return nil, fmt.Errorf("第%d行链接必须以 http:// 或 https:// 开头：%s", i+1, buttonURL)
		}

		allButtons = append(allButtons, models.InlineKeyboardButton{
			Text: buttonText,
			URL:  buttonURL,
		})
	}

	if len(allButtons) == 0 {
		return nil, fmt.Errorf("没有找到有效的按钮")
	}

	// Arrange buttons according to layout
	var buttonRows [][]models.InlineKeyboardButton
	if layout == "double" {
		// Double layout: 2 buttons per row
		for i := 0; i < len(allButtons); i += 2 {
			if i+1 < len(allButtons) {
				// Two buttons in this row
				buttonRows = append(buttonRows, []models.InlineKeyboardButton{allButtons[i], allButtons[i+1]})
			} else {
				// One button in this row
				buttonRows = append(buttonRows, []models.InlineKeyboardButton{allButtons[i]})
			}
		}
	} else {
		// Single layout: 1 button per row
		for _, button := range allButtons {
			buttonRows = append(buttonRows, []models.InlineKeyboardButton{button})
		}
	}

	return buttonRows, nil
}

// finishAddingButtons finishes adding buttons and saves to database
func (b *Bot) finishAddingButtons(chatID int64, userState *UserState) {
	groupID := userState.Data["groupID"].(int64)
	buttons := userState.Data["buttons"].([][]models.InlineKeyboardButton)

	// Get group to find template ID
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Convert to InlineKeyboard type
	inlineKeyboard := models.InlineKeyboard(buttons)

	// Update template buttons in database
	err = b.repo.UpdateMessageTemplateButtons(group.MessageID, inlineKeyboard)
	if err != nil {
		b.sendMessage(chatID, "❌ 保存按钮失败："+err.Error())
		return
	}

	b.clearState(chatID)

	if len(buttons) > 0 {
		b.sendMessage(chatID, fmt.Sprintf("✅ 已保存 %d 个按钮到模板", len(buttons)))
	} else {
		b.sendMessage(chatID, "✅ 已完成按钮设置（未添加按钮）")
	}

	// Return to group details
	b.showGroupDetails(chatID, groupID)
}

// handleAddButtonAction handles add single button action
func (b *Bot) handleAddButtonAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "add_button_")
	if groupID == 0 {
		return
	}

	// Ask user to choose button layout
	text := "🔘 *按钮布局选择*\n\n请选择按钮的排列方式："

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 单列（每行1个）", fmt.Sprintf("group_layout_single_%d", groupID)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱📱 双列（每行2个）", fmt.Sprintf("group_layout_double_%d", groupID)),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// handleGroupLayoutChoice handles button layout choice for group
func (b *Bot) handleGroupLayoutChoice(chatID int64, data string, layout string) {
	var groupID int64
	if layout == "single" {
		groupID = b.extractGroupIDFromData(data, "group_layout_single_")
	} else {
		groupID = b.extractGroupIDFromData(data, "group_layout_double_")
	}

	if groupID == 0 {
		return
	}

	b.setState(chatID, "add_single_button", map[string]interface{}{
		"groupID": groupID,
		"layout":  layout,
	})

	layoutText := "单列（每行1个）"
	if layout == "double" {
		layoutText = "双列（每行2个）"
	}

	b.sendMessage(chatID, fmt.Sprintf("🔘 *批量添加按钮 - %s*\n\n请输入按钮信息，支持批量输入：\n\n**格式：**\n`按钮文字|链接URL`\n\n**示例：**\n```\n💎 站长仓库|https://t.me/zhanzhangck\n👀  站长交流群|https://t.me/vpsbbq\n```\n\n💡 **提示：** 选择%s布局，按钮会自动按此方式排列", layoutText, layoutText))
}

// handleClearButtonsAction handles clear all buttons action
func (b *Bot) handleClearButtonsAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "clear_buttons_")
	if groupID == 0 {
		return
	}

	// Get group to find template ID
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Clear buttons by setting empty InlineKeyboard
	emptyKeyboard := models.InlineKeyboard{}
	err = b.repo.UpdateMessageTemplateButtons(group.MessageID, emptyKeyboard)
	if err != nil {
		b.sendMessage(chatID, "❌ 清空按钮失败："+err.Error())
		return
	}

	b.sendMessage(chatID, "✅ 已清空所有按钮")

	// Return to button management
	b.handleManageButtonsAction(chatID, fmt.Sprintf("manage_buttons_%d", groupID))
}

// handlePreviewMessageAction handles preview message action
func (b *Bot) handlePreviewMessageAction(chatID int64, data string) {
	groupID := b.extractGroupIDFromData(data, "preview_message_")
	if groupID == 0 {
		return
	}

	// Get group to find template
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Get template
	template, err := b.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		b.sendMessage(chatID, "❌ 加载模板失败："+err.Error())
		return
	}

	// Create inline keyboard from template buttons if they exist
	var keyboard tgbotapi.InlineKeyboardMarkup
	if len(template.Buttons) > 0 {
		var keyboardRows [][]tgbotapi.InlineKeyboardButton
		for _, row := range template.Buttons {
			var keyboardRow []tgbotapi.InlineKeyboardButton
			for _, button := range row {
				keyboardRow = append(keyboardRow, tgbotapi.NewInlineKeyboardButtonURL(button.Text, button.URL))
			}
			keyboardRows = append(keyboardRows, keyboardRow)
		}
		keyboard = tgbotapi.NewInlineKeyboardMarkup(keyboardRows...)
	}

	// Parse entities from template if they exist
	var entities []tgbotapi.MessageEntity
	if template.Entities != "" {
		if err := json.Unmarshal([]byte(template.Entities), &entities); err != nil {
			log.Printf("Failed to deserialize entities for preview: %v", err)
			entities = nil
		} else {
			log.Printf("Using %d entities for preview", len(entities))
		}
	}

	// Create preview message based on template type
	var msg tgbotapi.Chattable
	switch template.MessageType {
	case models.MessageTypePhoto:
		if template.MediaURL != "" {
			photoMsg := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(template.MediaURL))
			// Build the complete caption
			previewPrefix := fmt.Sprintf("📱 消息预览: %s\n\n", group.Name)
			photoMsg.Caption = previewPrefix + template.Content
			// Use entities for preview to match actual message format
			if entities != nil && len(entities) > 0 {
				// Adjust entity offsets for preview prefix
				adjustedEntities := b.adjustEntitiesForPreview(entities, len([]byte(previewPrefix)))
				photoMsg.CaptionEntities = adjustedEntities
				log.Printf("Photo preview: prefix='%s' (length=%d bytes), content='%s'",
					previewPrefix, len([]byte(previewPrefix)), template.Content)
			}
			if len(template.Buttons) > 0 {
				photoMsg.ReplyMarkup = keyboard
			}
			msg = photoMsg
		} else {
			// Fallback to text if no media URL
			textMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📱 消息预览: %s\n\n%s\n\n⚠️ 图片模板但无媒体文件", group.Name, template.Content))
			textMsg.ParseMode = "Markdown"
			textMsg.DisableWebPagePreview = true
			if len(template.Buttons) > 0 {
				textMsg.ReplyMarkup = keyboard
			}
			msg = textMsg
		}
	case models.MessageTypeVideo:
		if template.MediaURL != "" {
			videoMsg := tgbotapi.NewVideo(chatID, tgbotapi.FileID(template.MediaURL))
			// Build the complete caption
			previewPrefix := fmt.Sprintf("📱 消息预览: %s\n\n", group.Name)
			videoMsg.Caption = previewPrefix + template.Content
			// Use entities for preview to match actual message format
			if entities != nil && len(entities) > 0 {
				// Adjust entity offsets for preview prefix
				adjustedEntities := b.adjustEntitiesForPreview(entities, len([]byte(previewPrefix)))
				videoMsg.CaptionEntities = adjustedEntities
			}
			if len(template.Buttons) > 0 {
				videoMsg.ReplyMarkup = keyboard
			}
			msg = videoMsg
		} else {
			textMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📱 消息预览: %s\n\n%s\n\n⚠️ 视频模板但无媒体文件", group.Name, template.Content))
			textMsg.ParseMode = "Markdown"
			textMsg.DisableWebPagePreview = true
			if len(template.Buttons) > 0 {
				textMsg.ReplyMarkup = keyboard
			}
			msg = textMsg
		}
	case models.MessageTypeDocument:
		if template.MediaURL != "" {
			docMsg := tgbotapi.NewDocument(chatID, tgbotapi.FileID(template.MediaURL))
			// Build the complete caption
			previewPrefix := fmt.Sprintf("📱 消息预览: %s\n\n", group.Name)
			docMsg.Caption = previewPrefix + template.Content
			// Use entities for preview to match actual message format
			if entities != nil && len(entities) > 0 {
				// Adjust entity offsets for preview prefix
				adjustedEntities := b.adjustEntitiesForPreview(entities, len([]byte(previewPrefix)))
				docMsg.CaptionEntities = adjustedEntities
			}
			if len(template.Buttons) > 0 {
				docMsg.ReplyMarkup = keyboard
			}
			msg = docMsg
		} else {
			textMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📱 消息预览: %s\n\n%s\n\n⚠️ 文档模板但无媒体文件", group.Name, template.Content))
			textMsg.ParseMode = "Markdown"
			textMsg.DisableWebPagePreview = true
			if len(template.Buttons) > 0 {
				textMsg.ReplyMarkup = keyboard
			}
			msg = textMsg
		}
	case models.MessageTypeAudio:
		if template.MediaURL != "" {
			audioMsg := tgbotapi.NewAudio(chatID, tgbotapi.FileID(template.MediaURL))
			// Build the complete caption
			previewPrefix := fmt.Sprintf("📱 消息预览: %s\n\n", group.Name)
			audioMsg.Caption = previewPrefix + template.Content
			// Use entities for preview to match actual message format
			if entities != nil && len(entities) > 0 {
				// Adjust entity offsets for preview prefix
				adjustedEntities := b.adjustEntitiesForPreview(entities, len([]byte(previewPrefix)))
				audioMsg.CaptionEntities = adjustedEntities
			}
			if len(template.Buttons) > 0 {
				audioMsg.ReplyMarkup = keyboard
			}
			msg = audioMsg
		} else {
			textMsg := tgbotapi.NewMessage(chatID, fmt.Sprintf("📱 消息预览: %s\n\n%s\n\n⚠️ 音频模板但无媒体文件", group.Name, template.Content))
			textMsg.ParseMode = "Markdown"
			textMsg.DisableWebPagePreview = true
			if len(template.Buttons) > 0 {
				textMsg.ReplyMarkup = keyboard
			}
			msg = textMsg
		}
	default: // MessageTypeText
		// Build the complete message
		previewPrefix := fmt.Sprintf("📱 消息预览: %s\n\n", group.Name)
		textMsg := tgbotapi.NewMessage(chatID, previewPrefix+template.Content)
		// Use entities for preview to match actual message format
		if entities != nil && len(entities) > 0 {
			// Adjust entity offsets for preview prefix
			adjustedEntities := b.adjustEntitiesForPreview(entities, len([]byte(previewPrefix)))
			textMsg.Entities = adjustedEntities
		}
		textMsg.DisableWebPagePreview = true
		if len(template.Buttons) > 0 {
			textMsg.ReplyMarkup = keyboard
		}
		msg = textMsg
	}

	b.api.Send(msg)

	// Send a follow-up message with return button
	returnText := "👆 以上是消息预览效果"
	returnKeyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回按钮管理", fmt.Sprintf("manage_buttons_%d", groupID)),
		),
	)

	returnMsg := tgbotapi.NewMessage(chatID, returnText)
	returnMsg.ReplyMarkup = returnKeyboard
	b.api.Send(returnMsg)
}

// handleAddSingleButton handles adding buttons (supports batch input)
func (b *Bot) handleAddSingleButton(chatID int64, input string, userState *UserState) {
	input = strings.TrimSpace(input)
	groupID := userState.Data["groupID"].(int64)

	// Get layout preference
	layout := "single" // default
	if userState.Data["layout"] != nil {
		layout = userState.Data["layout"].(string)
	}

	// Parse batch buttons
	buttonRows, err := b.parseBatchButtons(input, layout)
	if err != nil {
		b.sendMessage(chatID, "❌ "+err.Error()+"\n\n**格式示例：**\n```\n💎 站长仓库|https://t.me/zhanzhangck\n👀  站长交流群|https://t.me/vpsbbq\n```")
		return
	}

	// Get group to find template ID
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 加载组信息失败："+err.Error())
		return
	}

	// Get current template to append buttons
	template, err := b.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 加载模板失败："+err.Error())
		return
	}

	// Append new buttons to existing ones
	newButtons := template.Buttons
	newButtons = append(newButtons, buttonRows...)

	// Count total buttons added
	totalButtons := 0
	for _, row := range buttonRows {
		totalButtons += len(row)
	}

	// Update template buttons in database
	log.Printf("DEBUG: Saving %d button rows to template %d", len(newButtons), group.MessageID)
	err = b.repo.UpdateMessageTemplateButtons(group.MessageID, newButtons)
	if err != nil {
		b.sendMessage(chatID, "❌ 保存按钮失败："+err.Error())
		return
	}

	// Verify the buttons were saved correctly
	verifyTemplate, err := b.repo.GetMessageTemplate(group.MessageID)
	if err != nil {
		log.Printf("DEBUG: Failed to verify template after saving buttons: %v", err)
	} else {
		log.Printf("DEBUG: Verified template has %d button rows after saving", len(verifyTemplate.Buttons))
	}

	layoutText := "单列"
	if layout == "double" {
		layoutText = "双列"
	}

	b.clearState(chatID)
	b.sendMessage(chatID, fmt.Sprintf("✅ 成功添加 %d 个按钮（%s布局）", totalButtons, layoutText))

	// Return to button management
	b.handleManageButtonsAction(chatID, fmt.Sprintf("manage_buttons_%d", groupID))
}

// handleForwardRequest handles the forward request
func (b *Bot) handleForwardRequest(chatID int64) {
	// Set user state to wait for forwarded message
	b.setState(chatID, "waiting_forward", map[string]interface{}{})

	text := "📤 *无引用转发*\n\n请转发一条消息给我，我将帮您转发到指定的频道组。\n\n⚠️ 注意：转发的消息将不会显示原始来源。"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "send_messages"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard
	b.api.Send(msg)
}

// askForPushButtons asks user if they want to add buttons for push message
func (b *Bot) askForPushButtons(chatID int64, messageContent string) {
	text := "🔘 *添加按钮（可选）*\n\n是否要为这条推送消息添加按钮？"

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ 添加按钮", "add_push_buttons"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⏭️ 跳过", "skip_push_buttons"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// handleAddPushButtonsAction handles add buttons for push message
func (b *Bot) handleAddPushButtonsAction(chatID int64) {
	// Get current user state to preserve message content
	b.stateMutex.RLock()
	currentState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || currentState.Data["message_content"] == nil {
		b.sendMessage(chatID, "❌ 状态丢失，请重新开始")
		b.sendMainMenu(chatID)
		return
	}

	// Ask user to choose button layout
	text := "🔘 *按钮布局选择*\n\n请选择按钮的排列方式："

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱 单列（每行1个）", "layout_single"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📱📱 双列（每行2个）", "layout_double"),
		),
	)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = keyboard

	b.api.Send(msg)
}

// handleLayoutChoice handles button layout choice
func (b *Bot) handleLayoutChoice(chatID int64, layout string) {
	// Get current user state to preserve message content
	b.stateMutex.RLock()
	currentState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || currentState.Data["message_content"] == nil {
		b.sendMessage(chatID, "❌ 状态丢失，请重新开始")
		b.sendMainMenu(chatID)
		return
	}

	// Preserve all existing data and add buttons array and layout
	newData := make(map[string]interface{})
	for k, v := range currentState.Data {
		newData[k] = v
	}
	newData["buttons"] = [][]models.InlineKeyboardButton{}
	newData["layout"] = layout

	b.setState(chatID, "add_push_buttons", newData)

	layoutText := "单列（每行1个）"
	if layout == "double" {
		layoutText = "双列（每行2个）"
	}

	b.sendMessage(chatID, fmt.Sprintf("🔘 *批量添加推送按钮 - %s*\n\n请输入按钮信息，支持批量输入：\n\n**格式：**\n`按钮文字|链接URL`\n\n**示例：**\n```\n💎 站长仓库|https://t.me/zhanzhangck\n👀  站长交流群|https://t.me/vpsbbq\n```\n\n💡 **提示：** 选择%s布局，按钮会自动按此方式排列", layoutText, layoutText))
}

// handleSkipPushButtonsAction handles skip buttons for push message
func (b *Bot) handleSkipPushButtonsAction(chatID int64) {
	// Get user state to retrieve message content
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists {
		b.sendMessage(chatID, "❌ 状态丢失，请重新开始")
		b.sendMainMenu(chatID)
		return
	}

	messageContent := userState.Data["message_content"].(string)

	// Show group selection for push
	b.showGroupSelectionForCustomPush(chatID, messageContent)
}

// handleAddPushButtons handles adding buttons for push message (supports batch input)
func (b *Bot) handleAddPushButtons(chatID int64, input string, userState *UserState) {
	input = strings.TrimSpace(input)

	// Get layout preference
	layout := "single" // default
	if userState.Data["layout"] != nil {
		layout = userState.Data["layout"].(string)
	}

	// Parse batch buttons
	buttonRows, err := b.parseBatchButtons(input, layout)
	if err != nil {
		b.sendMessage(chatID, "❌ "+err.Error()+"\n\n**格式示例：**\n```\n💎 站长仓库|https://t.me/zhanzhangck\n👀  站长交流群|https://t.me/vpsbbq\n```")
		return
	}

	// Replace all buttons with new ones
	userState.Data["buttons"] = buttonRows

	// Count total buttons
	totalButtons := 0
	for _, row := range buttonRows {
		totalButtons += len(row)
	}

	layoutText := "单列"
	if layout == "double" {
		layoutText = "双列"
	}

	b.sendMessage(chatID, fmt.Sprintf("✅ 成功添加 %d 个按钮（%s布局）\n\n按钮预览：", totalButtons, layoutText))

	// Show button preview
	previewText := ""
	for i, row := range buttonRows {
		previewText += fmt.Sprintf("第%d行：", i+1)
		for j, button := range row {
			if j > 0 {
				previewText += " | "
			}
			previewText += button.Text
		}
		previewText += "\n"
	}

	b.sendMessage(chatID, previewText)

	// Finish adding buttons automatically
	b.finishAddingPushButtons(chatID, userState)
}

// finishAddingPushButtons finishes adding buttons for push message
func (b *Bot) finishAddingPushButtons(chatID int64, userState *UserState) {
	buttons := userState.Data["buttons"].([][]models.InlineKeyboardButton)

	// Check if we have the original message content in current state
	if userState.Data["message_content"] == nil {
		b.sendMessage(chatID, "❌ 状态丢失，请重新开始")
		b.sendMainMenu(chatID)
		return
	}

	// Add buttons to the message data
	if len(buttons) > 0 {
		userState.Data["push_buttons"] = buttons
		b.sendMessage(chatID, fmt.Sprintf("✅ 已添加 %d 个按钮到推送消息", len(buttons)))
	} else {
		b.sendMessage(chatID, "✅ 已完成按钮设置（未添加按钮）")
	}

	// Restore original state and show group selection
	b.setState(chatID, "custom_message_content", userState.Data)
	messageContent := userState.Data["message_content"].(string)
	b.showGroupSelectionForCustomPush(chatID, messageContent)
}

// handleForwardedMessage handles forwarded messages for no-reference forwarding
func (b *Bot) handleForwardedMessage(chatID int64, message *tgbotapi.Message) {
	// Check if user is in waiting_forward state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || userState.State != "waiting_forward" {
		// User is not in forward waiting state, ignore this message
		return
	}

	// Check if this is part of a media group
	if message.MediaGroupID != "" {
		// Handle media group messages
		b.handleMediaGroupMessage(chatID, message)
		return
	}

	var messageContent string
	var messageType string
	var mediaURL string
	var entities []tgbotapi.MessageEntity

	// Determine message type and extract content
	if message.Text != "" {
		// Text message
		messageContent = message.Text
		messageType = "text"
		if message.Entities != nil {
			entities = message.Entities
		}
	} else if message.Photo != nil && len(message.Photo) > 0 {
		// Photo message
		messageContent = message.Caption
		messageType = "photo"
		// Get the highest resolution photo
		photo := message.Photo[len(message.Photo)-1]
		mediaURL = photo.FileID
		if message.CaptionEntities != nil {
			entities = message.CaptionEntities
		}
	} else if message.Video != nil {
		// Video message
		messageContent = message.Caption
		messageType = "video"
		mediaURL = message.Video.FileID
		if message.CaptionEntities != nil {
			entities = message.CaptionEntities
		}
	} else if message.Document != nil {
		// Document message
		messageContent = message.Caption
		messageType = "document"
		mediaURL = message.Document.FileID
		if message.CaptionEntities != nil {
			entities = message.CaptionEntities
		}
	} else if message.Audio != nil {
		// Audio message
		messageContent = message.Caption
		messageType = "audio"
		mediaURL = message.Audio.FileID
		if message.CaptionEntities != nil {
			entities = message.CaptionEntities
		}
	} else if message.Voice != nil {
		// Voice message
		messageContent = message.Caption
		messageType = "voice"
		mediaURL = message.Voice.FileID
		if message.CaptionEntities != nil {
			entities = message.CaptionEntities
		}
	} else if message.VideoNote != nil {
		// Video note message
		messageContent = ""
		messageType = "video_note"
		mediaURL = message.VideoNote.FileID
	} else if message.Sticker != nil {
		// Sticker message
		messageContent = ""
		messageType = "sticker"
		mediaURL = message.Sticker.FileID
	} else if message.Animation != nil {
		// GIF/Animation message
		messageContent = message.Caption
		messageType = "animation"
		mediaURL = message.Animation.FileID
		if message.CaptionEntities != nil {
			entities = message.CaptionEntities
		}
	} else {
		// Unsupported message type
		b.sendMessage(chatID, "❌ 不支持的消息类型。请转发文本、图片、视频或文档消息。")
		return
	}

	// Store the forwarded message data
	messageData := map[string]interface{}{
		"message_content": messageContent,
		"message_type":    messageType,
		"media_url":       mediaURL,
	}

	if len(entities) > 0 {
		messageData["entities"] = entities
		log.Printf("Storing %d entities from forwarded %s message", len(entities), messageType)
	}

	log.Printf("Processing forwarded %s message with content length: %d", messageType, len(messageContent))

	// Update state to show group selection
	b.setState(chatID, "forward_message_content", messageData)

	// Show group selection for forwarding
	previewContent := messageContent
	if previewContent == "" {
		previewContent = fmt.Sprintf("[%s消息]", messageType)
	}
	if len(previewContent) > 100 {
		previewContent = previewContent[:100] + "..."
	}

	b.showGroupSelectionForForward(chatID, previewContent)
}

// showGroupSelectionForForward shows group selection for forwarding
func (b *Bot) showGroupSelectionForForward(chatID int64, messageContent string) {
	groups, err := b.repo.GetChannelGroups()
	if err != nil {
		b.sendMessage(chatID, "加载频道组时出错。")
		return
	}

	if len(groups) == 0 {
		b.sendMessage(chatID, "没有可用的频道组。请先创建频道组。")
		return
	}

	text := "📤 *无引用转发*\n\n选择要转发到的频道组："
	var keyboard [][]tgbotapi.InlineKeyboardButton

	for _, group := range groups {
		status := "🔴"
		if group.IsActive {
			status = "🟢"
		}
		buttonText := fmt.Sprintf("%s %s", status, group.Name)
		buttonData := fmt.Sprintf("forward_%d", group.ID)
		keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(buttonText, buttonData),
		))
	}

	keyboard = append(keyboard, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回", "send_messages"),
	))

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(keyboard...)

	b.api.Send(msg)
}

// handleForwardAction handles forward action for a specific group
func (b *Bot) handleForwardAction(chatID int64, data string) {
	// Extract group ID from callback data
	parts := strings.Split(data, "_")
	if len(parts) != 2 {
		b.sendMessage(chatID, "无效的转发操作。")
		return
	}

	groupID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		b.sendMessage(chatID, "无效的组ID。")
		return
	}

	// Get operation lock for this group to prevent concurrent operations
	lock := b.getOperationLock(groupID)
	if !lock.TryLock() {
		b.sendMessage(chatID, "⚠️ 该频道组正在处理其他操作，请稍后再试。")
		return
	}
	defer func() {
		lock.Unlock()
		// Clean up the lock after operation completes
		go func() {
			time.Sleep(1 * time.Second)
			b.cleanupOperationLock(groupID)
		}()
	}()

	// Get message data from user state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	log.Printf("handleForwardAction: userState exists=%v", exists)
	if exists {
		log.Printf("handleForwardAction: userState.State=%s", userState.State)
		var keys []string
		for k := range userState.Data {
			keys = append(keys, k)
		}
		log.Printf("handleForwardAction: userState.Data keys=%v", keys)
		if userState.Data["media_urls"] != nil {
			log.Printf("handleForwardAction: Found media_urls in userState")
		}
		if userState.Data["message_content"] != nil {
			log.Printf("handleForwardAction: Found message_content in userState")
		}
	}

	if !exists {
		b.sendMessage(chatID, "❌ 没有找到消息数据，请重新转发消息。")
		b.sendMainMenu(chatID)
		return
	}

	// Check if this is a media group or regular message
	if userState.Data["media_urls"] != nil {
		// This is a media group
		log.Printf("handleForwardAction: Executing media group forward")
		b.executeMediaGroupForward(chatID, groupID, userState.Data)
	} else if userState.Data["message_content"] != nil {
		// This is a regular message
		log.Printf("handleForwardAction: Executing regular message forward")
		messageContent := userState.Data["message_content"].(string)
		b.executeForward(chatID, groupID, messageContent)
	} else {
		log.Printf("handleForwardAction: No valid message data found")
		b.sendMessage(chatID, "❌ 没有找到消息内容，请重新转发消息。")
		b.sendMainMenu(chatID)
		return
	}
}

// executeForward executes forward to a specific group
func (b *Bot) executeForward(chatID int64, groupID int64, messageContent string) {
	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Get channels for this group
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	if len(channels) == 0 {
		b.clearState(chatID)
		b.sendMessage(chatID, "该组没有绑定的频道。")
		return
	}

	// Get message data from user state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists {
		b.clearState(chatID)
		b.sendMessage(chatID, "❌ 没有找到消息数据，请重新转发消息。")
		b.sendMainMenu(chatID)
		return
	}

	messageType := userState.Data["message_type"].(string)

	// Send messages to all channels (forward - don't delete previous)
	// Add rate limiting for API safety
	successCount := 0
	for i, channel := range channels {
		if channel.IsActive {
			// Rate limiting: delay between channels to avoid API limits
			if i > 0 {
				time.Sleep(500 * time.Millisecond) // 500ms delay between channels
				log.Printf("Rate limiting: waiting 500ms before sending to channel %s", channel.ChannelID)
			}

			// Send new message
			var err error

			if messageType == "media_group" {
				// Handle media group forwarding
				err = b.forwardMediaGroup(channel.ChannelID, userState.Data)
			} else if messageType == "text" && userState.Data["entities"] != nil {
				// Send text with entities to preserve formatting
				entities := userState.Data["entities"].([]tgbotapi.MessageEntity)
				_, err = b.service.SendMessageWithEntities(channel.ChannelID, messageContent, entities)
			} else {
				// Send single media message
				mediaURL := ""
				if userState.Data["media_url"] != nil {
					mediaURL = userState.Data["media_url"].(string)
				}

				// Create temporary message template
				template := &models.MessageTemplate{
					Title:       "无引用转发消息",
					Content:     messageContent,
					MessageType: b.convertToModelMessageType(messageType),
					MediaURL:    mediaURL,
					Buttons:     models.InlineKeyboard{},
				}

				// Send as regular template (supports all media types)
				_, err = b.service.SendMessage(channel.ChannelID, template)
			}

			if err != nil {
				log.Printf("Failed to send forward message to channel %s: %v", channel.ChannelID, err)
			} else {
				successCount++
			}
		}
	}

	b.clearState(chatID)

	successMsg := fmt.Sprintf("📤 *无引用转发完成*\n\n"+
		"📋 频道组：%s\n"+
		"📢 成功转发：%d/%d 个频道\n"+
		"💬 消息内容：%s",
		group.Name,
		successCount,
		len(channels),
		messageContent)

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	b.sendMainMenu(chatID)
}

// executeMediaGroupForward executes media group forward to a specific group
func (b *Bot) executeMediaGroupForward(chatID int64, groupID int64, messageData map[string]interface{}) {
	// Get group details
	group, err := b.repo.GetChannelGroup(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载组详情时出错。")
		return
	}

	// Get channels for this group
	channels, err := b.repo.GetChannelsByGroupID(groupID)
	if err != nil {
		b.clearState(chatID)
		b.sendMessage(chatID, "加载频道列表时出错。")
		return
	}

	if len(channels) == 0 {
		b.clearState(chatID)
		b.sendMessage(chatID, "该组没有绑定的频道。")
		return
	}

	// Send media group to all channels with rate limiting
	successCount := 0
	for i, channel := range channels {
		if channel.IsActive {
			// Rate limiting: longer delay for media groups (more API calls)
			if i > 0 {
				time.Sleep(1 * time.Second) // 1 second delay for media groups
				log.Printf("Rate limiting: waiting 1s before sending media group to channel %s", channel.ChannelID)
			}

			err := b.forwardMediaGroup(channel.ChannelID, messageData)
			if err != nil {
				log.Printf("Failed to send media group to channel %s: %v", channel.ChannelID, err)
			} else {
				successCount++
			}
		}
	}

	b.clearState(chatID)

	// Get message content for display
	messageContent := ""
	if messageData["message_content"] != nil {
		messageContent = messageData["message_content"].(string)
	}
	if messageContent == "" {
		mediaURLs := messageData["media_urls"].([]string)
		messageContent = fmt.Sprintf("[媒体组 - %d个文件]", len(mediaURLs))
	}

	successMsg := fmt.Sprintf("📤 *无引用转发完成*\n\n"+
		"📋 频道组：%s\n"+
		"📢 成功转发：%d/%d 个频道",
		group.Name,
		successCount,
		len(channels))

	msg := tgbotapi.NewMessage(chatID, successMsg)
	msg.ParseMode = "Markdown"
	b.api.Send(msg)

	b.sendMainMenu(chatID)
}

// convertToModelMessageType converts string message type to models.MessageType
func (b *Bot) convertToModelMessageType(messageType string) models.MessageType {
	switch messageType {
	case "text":
		return models.MessageTypeText
	case "photo":
		return models.MessageTypePhoto
	case "video":
		return models.MessageTypeVideo
	case "document":
		return models.MessageTypeDocument
	case "audio":
		return models.MessageTypeAudio
	case "voice":
		return models.MessageTypeAudio // Voice messages are treated as audio
	case "video_note":
		return models.MessageTypeVideo // Video notes are treated as video
	case "sticker":
		return models.MessageTypePhoto // Stickers are treated as photo
	case "animation":
		return models.MessageTypeVideo // Animations/GIFs are treated as video
	default:
		return models.MessageTypeText
	}
}

// MediaGroupBuffer stores media group messages temporarily
type MediaGroupBuffer struct {
	Messages []*tgbotapi.Message
	Timer    *time.Timer
	ChatID   int64
	GroupID  string
}

var mediaGroupBuffers = make(map[string]*MediaGroupBuffer)
var mediaGroupMutex sync.RWMutex

// handleMediaGroupMessage handles messages that are part of a media group
func (b *Bot) handleMediaGroupMessage(chatID int64, message *tgbotapi.Message) {
	// Check if user is in waiting_forward state
	b.stateMutex.RLock()
	userState, exists := b.userStates[chatID]
	b.stateMutex.RUnlock()

	if !exists || userState.State != "waiting_forward" {
		// User is not in forward waiting state, ignore this media group message
		return
	}

	mediaGroupMutex.Lock()
	defer mediaGroupMutex.Unlock()

	groupID := message.MediaGroupID
	log.Printf("Processing media group message: %s", groupID)

	// Get or create buffer for this media group
	buffer, exists := mediaGroupBuffers[groupID]
	if !exists {
		buffer = &MediaGroupBuffer{
			Messages: []*tgbotapi.Message{},
			ChatID:   chatID,
			GroupID:  groupID,
		}
		mediaGroupBuffers[groupID] = buffer
	}

	// Add message to buffer
	buffer.Messages = append(buffer.Messages, message)

	// Reset timer (wait for more messages in the group)
	if buffer.Timer != nil {
		buffer.Timer.Stop()
	}

	// Set timer to process the group after 2 seconds of no new messages
	buffer.Timer = time.AfterFunc(2*time.Second, func() {
		b.processMediaGroup(groupID)
	})

	log.Printf("Added message to media group %s, total messages: %d", groupID, len(buffer.Messages))
}

// processMediaGroup processes a complete media group
func (b *Bot) processMediaGroup(groupID string) {
	mediaGroupMutex.Lock()
	buffer, exists := mediaGroupBuffers[groupID]
	if !exists {
		mediaGroupMutex.Unlock()
		return
	}

	// Remove from buffer map
	delete(mediaGroupBuffers, groupID)
	mediaGroupMutex.Unlock()

	log.Printf("Processing complete media group %s with %d messages", groupID, len(buffer.Messages))

	if len(buffer.Messages) == 0 {
		return
	}

	// Sort messages by MessageID to maintain original order
	sort.Slice(buffer.Messages, func(i, j int) bool {
		return buffer.Messages[i].MessageID < buffer.Messages[j].MessageID
	})
	log.Printf("Sorted %d messages by MessageID for correct order", len(buffer.Messages))

	// Find the message with caption (usually the first one)
	var mainMessage *tgbotapi.Message
	var messageContent string
	var entities []tgbotapi.MessageEntity

	for _, msg := range buffer.Messages {
		if msg.Caption != "" {
			mainMessage = msg
			messageContent = msg.Caption
			if msg.CaptionEntities != nil {
				entities = msg.CaptionEntities
			}
			break
		}
	}

	// If no message has caption, use the first message
	if mainMessage == nil {
		mainMessage = buffer.Messages[0]
	}

	// Collect all media URLs and types
	var mediaURLs []string
	var mediaTypes []string
	var messageType string

	for _, msg := range buffer.Messages {
		if msg.Photo != nil && len(msg.Photo) > 0 {
			photo := msg.Photo[len(msg.Photo)-1]
			mediaURLs = append(mediaURLs, photo.FileID)
			mediaTypes = append(mediaTypes, "photo")
			if messageType == "" {
				messageType = "photo"
			}
		} else if msg.Video != nil {
			mediaURLs = append(mediaURLs, msg.Video.FileID)
			mediaTypes = append(mediaTypes, "video")
			if messageType == "" {
				messageType = "video"
			}
		} else if msg.Document != nil {
			mediaURLs = append(mediaURLs, msg.Document.FileID)
			mediaTypes = append(mediaTypes, "document")
			if messageType == "" {
				messageType = "document"
			}
		}
	}

	// Store the media group data
	messageData := map[string]interface{}{
		"message_content": messageContent,
		"message_type":    "media_group",
		"media_urls":      mediaURLs,
		"media_types":     mediaTypes,
		"media_count":     len(mediaURLs),
		"group_id":        groupID,
	}

	if len(entities) > 0 {
		messageData["entities"] = entities
		log.Printf("Storing %d entities from media group", len(entities))
	}

	log.Printf("Processing media group with %d media items, content length: %d", len(mediaURLs), len(messageContent))

	// Update state to show group selection
	b.setState(buffer.ChatID, "forward_message_content", messageData)

	// Show group selection for forwarding
	previewContent := messageContent
	if previewContent == "" {
		previewContent = fmt.Sprintf("[媒体组 - %d个文件]", len(mediaURLs))
	}
	if len(previewContent) > 100 {
		previewContent = previewContent[:100] + "..."
	}

	b.showGroupSelectionForForward(buffer.ChatID, previewContent)
}

// forwardMediaGroup forwards a media group to a channel
func (b *Bot) forwardMediaGroup(channelID string, messageData map[string]interface{}) error {
	mediaURLs, ok := messageData["media_urls"].([]string)
	if !ok || len(mediaURLs) == 0 {
		return fmt.Errorf("no media URLs found in media group")
	}

	messageContent := ""
	if messageData["message_content"] != nil {
		messageContent = messageData["message_content"].(string)
	}

	// Get media types from stored data
	mediaTypes, ok := messageData["media_types"].([]string)
	if !ok || len(mediaTypes) != len(mediaURLs) {
		// Fallback: assume all are photos
		mediaTypes = make([]string, len(mediaURLs))
		for i := range mediaTypes {
			mediaTypes[i] = "photo"
		}
	}

	// Get entities from stored data
	var entities []tgbotapi.MessageEntity
	if messageData["entities"] != nil {
		entities = messageData["entities"].([]tgbotapi.MessageEntity)
		log.Printf("forwardMediaGroup: Found %d entities in messageData", len(entities))
		for i, entity := range entities {
			log.Printf("Entity %d: type=%s, offset=%d, length=%d, url=%s", i, entity.Type, entity.Offset, entity.Length, entity.URL)
		}
	} else {
		log.Printf("forwardMediaGroup: No entities found in messageData")
	}

	// Use the message service to send media group with entities
	return b.service.SendMediaGroupWithEntities(channelID, mediaURLs, mediaTypes, messageContent, entities)
}

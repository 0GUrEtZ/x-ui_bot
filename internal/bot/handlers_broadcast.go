package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Broadcast handlers for admin announcements and database backups

// handleBroadcastStart initiates broadcast message creation
func (b *Bot) handleBroadcastStart(chatID int64) {
	b.logger.Infof("Admin %d started broadcast creation", chatID)

	if err := b.setUserState(chatID, "awaiting_broadcast_message"); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения состояния")
		return
	}
	if err := b.setBroadcastState(chatID, &BroadcastState{
		Timestamp: time.Now(),
	}); err != nil {
		b.sendMessage(chatID, "❌ Ошибка сохранения состояния")
		return
	}

	msg := "📢 <b>Создание объявления</b>\n\n" +
		"Отправьте текст объявления, которое будет разослано всем зарегистрированным пользователям.\n\n" +
		"<i>Можно использовать HTML форматирование: &lt;b&gt;жирный&lt;/b&gt;, &lt;i&gt;курсив&lt;/i&gt;</i>"

	b.sendMessage(chatID, msg)
}

// handleBroadcastMessage handles broadcast message text input
func (b *Bot) handleBroadcastMessage(chatID int64, message string) {
	state, exists := b.getBroadcastState(chatID)
	if !exists {
		return
	}

	state.Message = message

	// Show confirmation with preview
	msg := fmt.Sprintf(
		"📢 <b>Подтверждение рассылки</b>\n\n"+
			"<b>Предпросмотр:</b>\n"+
			"──────────────\n"+
			"%s\n"+
			"──────────────\n\n"+
			"Разослать это объявление всем пользователям?",
		message,
	)

	keyboard := tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Отправить").WithCallbackData("broadcast_confirm"),
			tu.InlineKeyboardButton("❌ Отменить").WithCallbackData("broadcast_cancel"),
		),
	)

	b.sendMessageWithInlineKeyboard(chatID, msg, keyboard)
}

// handleBroadcastConfirm sends broadcast to all users
func (b *Bot) handleBroadcastConfirm(chatID int64, messageID int) {
	state, exists := b.getBroadcastState(chatID)
	if !exists {
		if err := b.bot.AnswerCallbackQuery(context.Background(), &telego.AnswerCallbackQueryParams{
			CallbackQueryID: fmt.Sprintf("%d", messageID),
			Text:            "Ошибка: состояние рассылки не найдено",
			ShowAlert:       true,
		}); err != nil {
			b.logger.Errorf("Failed to answer callback query: %v", err)
		}
		return
	}

	// Update message to show it's processing
	b.editMessageText(chatID, messageID, "⏳ Отправка объявления...")

	// Get all registered users
	inbounds, err := b.apiClient.GetInbounds()
	if err != nil {
		b.logger.Errorf("Failed to get inbounds for broadcast: %v", err)
		b.editMessageText(chatID, messageID, "❌ Ошибка при получении списка пользователей")
		if err := b.deleteBroadcastState(chatID); err != nil {
			b.logger.Errorf("Failed to delete broadcast state: %v", err)
		}
		if err := b.deleteUserState(chatID); err != nil {
			b.logger.Errorf("Failed to delete user state: %v", err)
		}
		return
	}

	// Collect unique Telegram IDs
	userIDs := make(map[int64]bool)
	for _, inbound := range inbounds {
		settings, ok := inbound["settings"].(string)
		if !ok {
			continue
		}

		var settingsData map[string]interface{}
		if err := json.Unmarshal([]byte(settings), &settingsData); err != nil {
			continue
		}

		clients, ok := settingsData["clients"].([]interface{})
		if !ok {
			continue
		}

		for _, clientInterface := range clients {
			client, ok := clientInterface.(map[string]interface{})
			if !ok {
				continue
			}

			if tgID, ok := client["tgId"].(float64); ok && tgID > 0 {
				userIDs[int64(tgID)] = true
			}
		}
	}

	// Send broadcast to all users
	successCount := 0
	failCount := 0

	broadcastMsg := fmt.Sprintf("📢 <b>Объявление</b>\n\n%s", state.Message)

	for userID := range userIDs {
		// Try to send message
		_, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
			ChatID:    tu.ID(userID),
			Text:      broadcastMsg,
			ParseMode: telego.ModeHTML,
		})
		if err != nil {
			b.logger.Warnf("Failed to send broadcast to user %d: %v", userID, err)
			failCount++
		} else {
			successCount++
		}
		time.Sleep(50 * time.Millisecond) // Rate limiting
	}

	// Update admin with results
	resultMsg := fmt.Sprintf(
		"✅ <b>Рассылка завершена</b>\n\n"+
			"📊 Отправлено: %d\n"+
			"❌ Ошибок: %d\n"+
			"👥 Всего пользователей: %d",
		successCount,
		failCount,
		len(userIDs),
	)
	b.editMessageText(chatID, messageID, resultMsg)

	// Clean up state
	if err := b.deleteBroadcastState(chatID); err != nil {
		b.logger.Errorf("Failed to delete broadcast state: %v", err)
	}
	if err := b.deleteUserState(chatID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}

	b.logger.Infof("Broadcast completed by admin %d: %d sent, %d failed", chatID, successCount, failCount)
}

// handleBroadcastCancel cancels broadcast creation
func (b *Bot) handleBroadcastCancel(chatID int64, messageID int) {
	if err := b.deleteBroadcastState(chatID); err != nil {
		b.logger.Errorf("Failed to delete broadcast state: %v", err)
	}
	if err := b.deleteUserState(chatID); err != nil {
		b.logger.Errorf("Failed to delete user state: %v", err)
	}

	b.editMessageText(chatID, messageID, "❌ Рассылка отменена")
	b.logger.Infof("Broadcast cancelled by admin %d", chatID)
}

// namedBytesReader wraps bytes data to implement NamedReader interface
type namedBytesReader struct {
	*strings.Reader
	name string
}

func (r *namedBytesReader) Name() string {
	return r.name
}

// sendBackupToAdmins sends database backup to all admins
func (b *Bot) sendBackupToAdmins() {
	b.logger.Info("Starting database backup...")

	// Download backup from panel
	backup, err := b.apiClient.GetDatabaseBackup()
	if err != nil {
		b.logger.Errorf("Failed to download backup: %v", err)
		for _, adminID := range b.config.Telegram.AdminIDs {
			b.sendMessage(adminID, fmt.Sprintf("❌ Ошибка создания бэкапа: %v", err))
		}
		return
	}

	// Send to all admins
	filename := fmt.Sprintf("x-ui_%s.db", time.Now().Format("2006-01-02_15-04"))
	for _, adminID := range b.config.Telegram.AdminIDs {
		reader := &namedBytesReader{
			Reader: strings.NewReader(string(backup)),
			name:   filename,
		}

		_, err := b.bot.SendDocument(context.Background(), &telego.SendDocumentParams{
			ChatID: tu.ID(adminID),
			Document: telego.InputFile{
				File: reader,
			},
			Caption:   fmt.Sprintf("📦 <b>Backup Database</b>\n\n🕐 Time: %s\n💾 Size: %.2f MB", time.Now().Format("2006-01-02 15:04:05"), float64(len(backup))/1024/1024),
			ParseMode: "HTML",
		})

		if err != nil {
			b.logger.Errorf("Failed to send backup to admin %d: %v", adminID, err)
		} else {
			b.logger.Infof("Backup sent to admin %d", adminID)
		}
	}
}

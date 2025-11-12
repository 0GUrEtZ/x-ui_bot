package bot

import (
	"context"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// Telegram messaging helpers
// These methods provide convenient wrappers for sending and editing messages

// sendMessage sends a text message
func (b *Bot) sendMessage(chatID int64, text string) {
	_, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:    tu.ID(chatID),
		Text:      text,
		ParseMode: "HTML",
	})
	if err != nil {
		b.logger.Errorf("Failed to send message to %d: %v", chatID, err)
	}
}

// sendMessageWithKeyboard sends a message with keyboard
func (b *Bot) sendMessageWithKeyboard(chatID int64, text string, keyboard *telego.ReplyKeyboardMarkup) {
	_, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatID),
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})
	if err != nil {
		b.logger.Errorf("Failed to send message with keyboard to %d: %v", chatID, err)
	}
}

// sendMessageWithInlineKeyboard sends a message with inline keyboard
func (b *Bot) sendMessageWithInlineKeyboard(chatID int64, text string, keyboard *telego.InlineKeyboardMarkup) {
	_, err := b.bot.SendMessage(context.Background(), &telego.SendMessageParams{
		ChatID:      tu.ID(chatID),
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})
	if err != nil {
		b.logger.Errorf("Failed to send message with inline keyboard to %d: %v", chatID, err)
	}
}

// editMessage edits an existing message
func (b *Bot) editMessage(chatID int64, messageID int, text string, keyboard *telego.InlineKeyboardMarkup) {
	_, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:      tu.ID(chatID),
		MessageID:   messageID,
		Text:        text,
		ParseMode:   "HTML",
		ReplyMarkup: keyboard,
	})
	if err != nil {
		b.logger.Errorf("Failed to edit message %d in chat %d: %v", messageID, chatID, err)
	}
}

// editMessageText edits a message text without keyboard
func (b *Bot) editMessageText(chatID int64, messageID int, text string) {
	if _, err := b.bot.EditMessageText(context.Background(), &telego.EditMessageTextParams{
		ChatID:    tu.ID(chatID),
		MessageID: messageID,
		Text:      text,
		ParseMode: "HTML",
	}); err != nil {
		b.logger.Errorf("Failed to edit message %d in chat %d: %v", messageID, chatID, err)
	}
}

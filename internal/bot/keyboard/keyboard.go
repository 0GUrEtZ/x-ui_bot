package keyboard

import (
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BuildAdminKeyboard creates the admin keyboard
func BuildAdminKeyboard() *telego.ReplyKeyboardMarkup {
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton("📊 Статус сервера"),
			tu.KeyboardButton("👥 Список клиентов"),
		),
		tu.KeyboardRow(
			tu.KeyboardButton("📢 Сделать объявление"),
			tu.KeyboardButton("💾 Бэкап БД"),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

// BuildUserKeyboard creates the user keyboard for registered clients
func BuildUserKeyboard(hasExpiry bool) *telego.ReplyKeyboardMarkup {
	if hasExpiry {
		// Limited subscription - show extend button
		return tu.Keyboard(
			tu.KeyboardRow(
				tu.KeyboardButton("📱 Моя подписка"),
				tu.KeyboardButton("⏰ Продлить подписку"),
			),
			tu.KeyboardRow(
				tu.KeyboardButton("⚙️ Настройки"),
				tu.KeyboardButton("💬 Связь с админом"),
			),
		).WithResizeKeyboard().WithIsPersistent()
	}

	// Unlimited subscription - no extend button
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton("📱 Моя подписка"),
			tu.KeyboardButton("⚙️ Настройки"),
		),
		tu.KeyboardRow(
			tu.KeyboardButton("💬 Связь с админом"),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

// BuildGuestKeyboard creates the keyboard for unregistered users
func BuildGuestKeyboard() *telego.ReplyKeyboardMarkup {
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton("📋 Ознакомиться с условиями"),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

// BuildTermsKeyboard creates the keyboard for terms acceptance
func BuildTermsKeyboard() *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Принять").WithCallbackData("terms_accept"),
			tu.InlineKeyboardButton("❌ Отклонить").WithCallbackData("terms_decline"),
		),
	)
}

// BuildSettingsKeyboard creates the settings keyboard
func BuildSettingsKeyboard() *telego.ReplyKeyboardMarkup {
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton("🔄 Обновить username"),
		),
		tu.KeyboardRow(
			tu.KeyboardButton("◀️ Назад"),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

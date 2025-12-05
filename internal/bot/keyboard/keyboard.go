package keyboard

import (
	"fmt"
	"x-ui-bot/internal/bot/constants"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
)

// BuildAdminKeyboard creates the admin keyboard
func BuildAdminKeyboard() *telego.ReplyKeyboardMarkup {
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnServerStatus),
			tu.KeyboardButton(constants.BtnTrafficForecast),
		),
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnClientList),
			tu.KeyboardButton(constants.BtnBroadcast),
		),
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnBackupDB),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

// BuildUserKeyboard creates the user keyboard for registered clients
func BuildUserKeyboard(hasExpiry bool) *telego.ReplyKeyboardMarkup {
	if hasExpiry {
		// Limited subscription - show extend button
		return tu.Keyboard(
			tu.KeyboardRow(
				tu.KeyboardButton(constants.BtnMySubscription),
				tu.KeyboardButton(constants.BtnExtendSubscription),
			),
			tu.KeyboardRow(
				tu.KeyboardButton(constants.BtnSettings),
				tu.KeyboardButton(constants.BtnContactAdmin),
			),
		).WithResizeKeyboard().WithIsPersistent()
	}

	// Unlimited subscription - no extend button
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnMySubscription),
			tu.KeyboardButton(constants.BtnSettings),
		),
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnContactAdmin),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

// BuildGuestKeyboard creates the keyboard for unregistered users
func BuildGuestKeyboard() *telego.ReplyKeyboardMarkup {
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnTerms),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

// BuildTermsKeyboard creates the keyboard for terms acceptance
func BuildTermsKeyboard() *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Принять").WithCallbackData(constants.CbTermsAccept),
			tu.InlineKeyboardButton("❌ Отклонить").WithCallbackData(constants.CbTermsDecline),
		),
	)
}

// BuildSettingsKeyboard creates the settings keyboard
func BuildSettingsKeyboard() *telego.ReplyKeyboardMarkup {
	return tu.Keyboard(
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnUpdateUsername),
		),
		tu.KeyboardRow(
			tu.KeyboardButton(constants.BtnBack),
		),
	).WithResizeKeyboard().WithIsPersistent()
}

// BuildConfirmDeleteKeyboard builds a confirmation inline keyboard for client deletion
func BuildConfirmDeleteKeyboard(inboundID int, clientIndex int) *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("✅ Да, удалить").WithCallbackData(fmt.Sprintf("%s%d_%d", constants.CbConfirmDeletePrefix, inboundID, clientIndex)),
			tu.InlineKeyboardButton("❌ Отмена").WithCallbackData(fmt.Sprintf("%s%d_%d", constants.CbCancelDeletePrefix, inboundID, clientIndex)),
		),
	)
}

// BuildReplyInlineKeyboard creates an inline keyboard with a reply button for admins to respond to users
func BuildReplyInlineKeyboard(userID int64) *telego.InlineKeyboardMarkup {
	return tu.InlineKeyboard(
		tu.InlineKeyboardRow(
			tu.InlineKeyboardButton("💬 Ответить").WithCallbackData(fmt.Sprintf("%s%d", constants.CbReplyPrefix, userID)),
		),
	)
}

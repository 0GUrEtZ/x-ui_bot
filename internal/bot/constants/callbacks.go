package constants

// Commands
const (
	CmdStart    = "start"
	CmdHelp     = "help"
	CmdStatus   = "status"
	CmdID       = "id"
	CmdUsage    = "usage"
	CmdClients  = "clients"
	CmdForecast = "forecast"
)

// Callback Prefixes and Data
const (
	// Forecast
	CbForecastTotal         = "forecast_total"
	CbForecastInboundPrefix = "forecast_inbound_"

	// Terms
	CbTermsAccept  = "terms_accept"
	CbTermsDecline = "terms_decline"

	// Instructions
	CbInstructionsMenu = "instructions_menu"
	CbInstrPrefix      = "instr_"
	CbInstrIOS         = "ios"
	CbInstrMacOS       = "macos"
	CbInstrAndroid     = "android"
	CbInstrWindows     = "windows"
	CbInstrBack        = "back"

	// Registration
	CbRegDurationBase   = "reg_duration"
	CbRegDurationPrefix = "reg_duration_"
	CbApproveRegPrefix  = "approve_reg_"
	CbRejectRegPrefix   = "reject_reg_"

	// Subscription Extension
	CbExtendPrefix     = "extend_"
	CbApproveExtPrefix = "approve_ext_"
	CbRejectExtPrefix  = "reject_ext_"

	// Client Management
	CbClientPrefix        = "client_"
	CbBackToClients       = "back_to_clients"
	CbDeletePrefix        = "delete_"
	CbConfirmDeletePrefix = "confirm_delete_"
	CbCancelDeletePrefix  = "cancel_delete_"

	// General
	CbContactAdmin = "contact_admin"
	CbReplyPrefix  = "reply_"

	// Broadcast
	CbBroadcastConfirm = "broadcast_confirm"
	CbBroadcastCancel  = "broadcast_cancel"
)

// User States
const (
	StateAwaitingUserMessage      = "awaiting_user_message"
	StateAwaitingAdminMessage     = "awaiting_admin_message"
	StateAwaitingEmail            = "awaiting_email"
	StateAwaitingDuration         = "awaiting_duration"
	StateAwaitingNewEmail         = "awaiting_new_email"
	StateAwaitingBroadcastMessage = "awaiting_broadcast_message"
)

// Button Texts
const (
	BtnServerStatus       = "📊 Статус сервера"
	BtnTrafficForecast    = "📊 Прогноз трафика"
	BtnClientList         = "👥 Список клиентов"
	BtnBroadcast          = "📢 Сделать объявление"
	BtnBackupDB           = "💾 Бэкап БД"
	BtnTerms              = "📋 Ознакомиться с условиями"
	BtnMySubscription     = "📱 Моя подписка и инструкции"
	BtnInstructions       = "инструкции" // Keep for backward compatibility or partial match if needed
	BtnExtendSubscription = "⏰ Продлить подписку"
	BtnSettings           = "⚙️ Настройки"
	BtnUpdateUsername     = "🔄 Обновить username"
	BtnBack               = "◀️ Назад"
	BtnContactAdmin       = "💬 Связь с админом"
)

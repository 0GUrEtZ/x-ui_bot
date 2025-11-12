package bot

// Helper methods for storage state access
// These methods provide convenient wrappers around storage operations

// getUserState retrieves the current state for a user
func (b *Bot) getUserState(userID int64) (string, bool) {
	state, err := b.storage.GetUserState(userID)
	if err != nil {
		return "", false
	}
	return state, true
}

// setUserState sets the state for a user
func (b *Bot) setUserState(userID int64, state string) error {
	return b.storage.SetUserState(userID, state)
}

// deleteUserState removes the state for a user
func (b *Bot) deleteUserState(userID int64) error {
	return b.storage.DeleteUserState(userID)
}

// getRegistrationRequest retrieves a pending registration request
func (b *Bot) getRegistrationRequest(userID int64) (*RegistrationRequest, bool) {
	req, err := b.storage.GetRegistrationRequest(userID)
	if err != nil {
		return nil, false
	}
	return req, true
}

// setRegistrationRequest saves a registration request
func (b *Bot) setRegistrationRequest(userID int64, req *RegistrationRequest) error {
	return b.storage.SetRegistrationRequest(userID, req)
}

// deleteRegistrationRequest removes a registration request
func (b *Bot) deleteRegistrationRequest(userID int64) error {
	return b.storage.DeleteRegistrationRequest(userID)
}

// getAdminMessageState retrieves the message state for an admin
func (b *Bot) getAdminMessageState(adminID int64) (*AdminMessageState, bool) {
	state, err := b.storage.GetAdminMessageState(adminID)
	if err != nil {
		return nil, false
	}
	return state, true
}

// setAdminMessageState saves the message state for an admin
func (b *Bot) setAdminMessageState(adminID int64, state *AdminMessageState) error {
	return b.storage.SetAdminMessageState(adminID, state)
}

// deleteAdminMessageState removes the message state for an admin
func (b *Bot) deleteAdminMessageState(adminID int64) error {
	return b.storage.DeleteAdminMessageState(adminID)
}

// getUserMessageState retrieves the message state for a user
func (b *Bot) getUserMessageState(userID int64) (*UserMessageState, bool) {
	state, err := b.storage.GetUserMessageState(userID)
	if err != nil {
		return nil, false
	}
	return state, true
}

// setUserMessageState saves the message state for a user
func (b *Bot) setUserMessageState(userID int64, state *UserMessageState) error {
	return b.storage.SetUserMessageState(userID, state)
}

// deleteUserMessageState removes the message state for a user
func (b *Bot) deleteUserMessageState(userID int64) error {
	return b.storage.DeleteUserMessageState(userID)
}

// getBroadcastState retrieves the broadcast state for an admin
func (b *Bot) getBroadcastState(adminID int64) (*BroadcastState, bool) {
	state, err := b.storage.GetBroadcastState(adminID)
	if err != nil {
		return nil, false
	}
	return state, true
}

// setBroadcastState saves the broadcast state for an admin
func (b *Bot) setBroadcastState(adminID int64, state *BroadcastState) error {
	return b.storage.SetBroadcastState(adminID, state)
}

// deleteBroadcastState removes the broadcast state for an admin
func (b *Bot) deleteBroadcastState(adminID int64) error {
	return b.storage.DeleteBroadcastState(adminID)
}

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"x-ui-bot/internal/logger"
	"x-ui-bot/pkg/client"
)

// ClientService handles client-related business logic
type ClientService struct {
	apiClient *client.APIClient
	logger    *logger.Logger
}

// NewClientService creates a new client service
func NewClientService(apiClient *client.APIClient, log *logger.Logger) *ClientService {
	return &ClientService{
		apiClient: apiClient,
		logger:    log,
	}
}

// ParseClients parses clients from inbound settings JSON
func (s *ClientService) ParseClients(settingsStr string) ([]map[string]string, error) {
	var clients []map[string]string

	if settingsStr == "" {
		return clients, nil
	}

	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsStr), &settings); err != nil {
		return nil, fmt.Errorf("failed to parse settings JSON: %w", err)
	}

	clientsArray, ok := settings["clients"].([]interface{})
	if !ok {
		return clients, nil
	}

	for _, c := range clientsArray {
		clientMap, ok := c.(map[string]interface{})
		if !ok {
			continue
		}

		client := make(map[string]string)
		clientJSON, _ := json.Marshal(clientMap)
		client["_raw_json"] = string(clientJSON)

		if email, ok := clientMap["email"].(string); ok {
			client["email"] = email
		}
		if id, ok := clientMap["id"].(string); ok {
			client["id"] = id
		}
		if totalGB, ok := clientMap["totalGB"].(float64); ok {
			client["totalGB"] = fmt.Sprintf("%.0f", totalGB)
		} else {
			client["totalGB"] = "0"
		}
		if expiryTime, ok := clientMap["expiryTime"].(float64); ok {
			client["expiryTime"] = fmt.Sprintf("%.0f", expiryTime)
		} else {
			client["expiryTime"] = "0"
		}
		if enable, ok := clientMap["enable"].(bool); ok {
			client["enable"] = fmt.Sprintf("%t", enable)
		} else {
			client["enable"] = "true"
		}
		if tgId, ok := clientMap["tgId"].(string); ok {
			client["tgId"] = tgId
		} else if tgId, ok := clientMap["tgId"].(float64); ok {
			client["tgId"] = fmt.Sprintf("%.0f", tgId)
		} else {
			client["tgId"] = ""
		}

		client["up"] = "0"
		client["down"] = "0"
		client["total"] = "0"

		clients = append(clients, client)
	}

	return clients, nil
}

// EnableClient enables a client
func (s *ClientService) EnableClient(inboundID int, email string, client map[string]string) error {
	s.logger.WithFields(map[string]interface{}{
		"inbound_id": inboundID,
		"email":      email,
	}).Info("Enabling client")

	rawJSON := client["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		return fmt.Errorf("failed to parse client data: %w", err)
	}

	clientData["enable"] = true
	s.fixNumericFields(clientData)

	return s.apiClient.UpdateClient(context.Background(), inboundID, email, clientData)
}

// DisableClient disables a client
func (s *ClientService) DisableClient(inboundID int, email string, client map[string]string) error {
	s.logger.WithFields(map[string]interface{}{
		"inbound_id": inboundID,
		"email":      email,
	}).Info("Disabling client")

	rawJSON := client["_raw_json"]
	var clientData map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &clientData); err != nil {
		return fmt.Errorf("failed to parse client data: %w", err)
	}

	clientData["enable"] = false
	s.fixNumericFields(clientData)

	return s.apiClient.UpdateClient(context.Background(), inboundID, email, clientData)
}

// FixNumericFields converts float64 to int64 for specific fields (public for external use)
func (s *ClientService) FixNumericFields(data map[string]interface{}) {
	numericFields := []string{"expiryTime", "totalGB", "reset", "limitIp", "tgId", "created_at", "updated_at"}
	for _, field := range numericFields {
		if val, ok := data[field].(float64); ok {
			data[field] = int64(val)
		}
	}
}

// fixNumericFields is an alias for FixNumericFields for internal use
func (s *ClientService) fixNumericFields(data map[string]interface{}) {
	s.FixNumericFields(data)
}

// FormatBytes formats bytes to human readable string
func (s *ClientService) FormatBytes(value interface{}) string {
	var bytes float64

	switch v := value.(type) {
	case string:
		if v == "" {
			return "0 B"
		}
		parsed, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "0 B"
		}
		bytes = parsed
	case float64:
		bytes = v
	case int:
		bytes = float64(v)
	case int64:
		bytes = float64(v)
	default:
		return "0 B"
	}

	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%.0f B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	units := []string{"KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.2f %s", bytes/float64(div), units[exp])
}

// IsClientBlocked checks if a client is blocked
func (s *ClientService) IsClientBlocked(userID int64) bool {
	clientInfo, err := s.apiClient.GetClientByTgID(context.Background(), userID)
	if err != nil || clientInfo == nil {
		return false
	}

	if enable, ok := clientInfo["enable"].(bool); ok {
		return !enable
	}
	return false
}

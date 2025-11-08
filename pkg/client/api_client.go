package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// APIClient handles communication with 3x-ui panel API
type APIClient struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
	sessionID  string
}

// NewAPIClient creates a new API client
func NewAPIClient(baseURL, username, password string) *APIClient {
	return &APIClient{
		baseURL:  baseURL,
		username: username,
		password: password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

// Login authenticates with the 3x-ui panel
func (c *APIClient) Login() error {
	fmt.Printf("[DEBUG] Attempting login to: %s/login\n", c.baseURL)
	loginData := map[string]string{
		"username": c.username,
		"password": c.password,
	}

	resp, err := c.doRequest("POST", "/login", loginData, false)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("[DEBUG] Login response status: %d\n", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed with status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Extract session cookie
	fmt.Printf("[DEBUG] Response cookies: %d\n", len(resp.Cookies()))
	for _, cookie := range resp.Cookies() {
		fmt.Printf("[DEBUG] Cookie: %s = %s\n", cookie.Name, cookie.Value)
		if cookie.Name == "session" || cookie.Name == "3x-ui" {
			c.sessionID = cookie.Value
			fmt.Printf("[DEBUG] Session ID set: %s\n", c.sessionID)
			return nil
		}
	}

	return fmt.Errorf("no session cookie found in %d cookies", len(resp.Cookies()))
}

// doRequest performs an HTTP request
func (c *APIClient) doRequest(method, path string, data interface{}, needAuth bool) (*http.Response, error) {
	var body io.Reader
	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal data: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	if needAuth && c.sessionID != "" {
		req.AddCookie(&http.Cookie{
			Name:  "3x-ui",
			Value: c.sessionID,
		})
	}

	return c.httpClient.Do(req)
}

// GetStatus gets server status
func (c *APIClient) GetStatus() (map[string]interface{}, error) {
	resp, err := c.doRequest("GET", "/panel/api/server/status", nil, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return nil, err
		}
		return c.GetStatus()
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

// GetInbounds gets all inbounds
func (c *APIClient) GetInbounds() ([]map[string]interface{}, error) {
	fmt.Printf("[DEBUG] Requesting inbounds from: %s/panel/api/inbounds/list\n", c.baseURL)
	fmt.Printf("[DEBUG] Session ID: %s\n", c.sessionID)

	resp, err := c.doRequest("GET", "/panel/api/inbounds/list", nil, true)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("[DEBUG] GetInbounds response status: %d\n", resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		fmt.Printf("[DEBUG] Unauthorized, attempting re-login\n")
		if err := c.Login(); err != nil {
			return nil, fmt.Errorf("re-login failed: %w", err)
		}
		return c.GetInbounds()
	}

	// Read body for better error reporting
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool                     `json:"success"`
		Obj     []map[string]interface{} `json:"obj"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w (body: %s)", err, string(body))
	}

	if !result.Success {
		return nil, fmt.Errorf("API returned success=false (body: %s)", string(body))
	}

	return result.Obj, nil
}

// GetInbound gets a specific inbound by ID
func (c *APIClient) GetInbound(id int) (map[string]interface{}, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/panel/api/inbounds/get/%d", id), nil, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return nil, err
		}
		return c.GetInbound(id)
	}

	var result struct {
		Success bool                   `json:"success"`
		Obj     map[string]interface{} `json:"obj"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API returned success=false")
	}

	return result.Obj, nil
}

// ResetClientTraffic resets traffic for a client by email
func (c *APIClient) ResetClientTraffic(email string) error {
	data := map[string]string{"email": email}
	resp, err := c.doRequest("POST", "/panel/api/inbounds/resetClientTraffic", data, true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return err
		}
		return c.ResetClientTraffic(email)
	}

	var result struct {
		Success bool `json:"success"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}

	if !result.Success {
		return fmt.Errorf("failed to reset client traffic")
	}

	return nil
}

// GetClientTraffics gets client traffic statistics by email
func (c *APIClient) GetClientTraffics(email string) (map[string]interface{}, error) {
	resp, err := c.doRequest("GET", fmt.Sprintf("/panel/api/inbounds/getClientTraffics/%s", email), nil, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return nil, err
		}
		return c.GetClientTraffics(email)
	}

	var result struct {
		Success bool                   `json:"success"`
		Obj     map[string]interface{} `json:"obj"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API returned success=false")
	}

	return result.Obj, nil
}

// GetClientTrafficsById gets all client traffic statistics for an inbound by ID
func (c *APIClient) GetClientTrafficsById(inboundID int) ([]map[string]interface{}, error) {
	path := fmt.Sprintf("/panel/api/inbounds/getClientTrafficsById/%d", inboundID)
	fmt.Printf("[DEBUG] Requesting traffic from: %s\n", path)

	resp, err := c.doRequest("GET", path, nil, true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return nil, err
		}
		return c.GetClientTrafficsById(inboundID)
	}

	// Read body for debugging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	fmt.Printf("[DEBUG] Traffic API response: %s\n", string(bodyBytes))

	var result struct {
		Success bool                     `json:"success"`
		Obj     []map[string]interface{} `json:"obj"`
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, fmt.Errorf("API returned success=false")
	}

	fmt.Printf("[DEBUG] Parsed %d traffic records\n", len(result.Obj))
	return result.Obj, nil
}

// UpdateClient updates a client configuration
func (c *APIClient) UpdateClient(inboundID int, clientID string, clientData map[string]interface{}) error {
	fmt.Printf("[DEBUG] Updating client %s in inbound %d\n", clientID, inboundID)
	fmt.Printf("[DEBUG] Client data: %+v\n", clientData)

	// Convert client data to JSON for embedding in clients array
	clientJSON, err := json.Marshal(clientData)
	if err != nil {
		fmt.Printf("[ERROR] Failed to marshal client data: %v\n", err)
		return fmt.Errorf("failed to marshal client data: %w", err)
	}

	// Format as '{"clients": [clientJSON]}' - this is what 3x-ui expects
	settingsString := fmt.Sprintf(`{"clients": [%s]}`, string(clientJSON))
	fmt.Printf("[DEBUG] Settings string: %s\n", settingsString)

	// Prepare request data
	data := map[string]interface{}{
		"id":       inboundID,
		"settings": settingsString,
	}

	fmt.Printf("[DEBUG] Sending POST to /panel/api/inbounds/updateClient/%s\n", clientID)
	resp, err := c.doRequest("POST", fmt.Sprintf("/panel/api/inbounds/updateClient/%s", clientID), data, true)
	if err != nil {
		fmt.Printf("[ERROR] Request failed: %v\n", err)
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	fmt.Printf("[DEBUG] Response status: %d\n", resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		fmt.Printf("[DEBUG] Unauthorized, re-logging in\n")
		if err := c.Login(); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.UpdateClient(inboundID, clientID, clientData)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("[ERROR] Failed to read response body: %v\n", err)
		return fmt.Errorf("failed to read response: %w", err)
	}
	fmt.Printf("[DEBUG] Response body: %s\n", string(body))

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Printf("[ERROR] Failed to parse JSON response: %v\n", err)
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	if !result.Success {
		fmt.Printf("[ERROR] API returned success=false: %s\n", result.Msg)
		return fmt.Errorf("API returned success=false: %s", result.Msg)
	}

	fmt.Printf("[DEBUG] Client updated successfully\n")
	return nil
}

// AddClient adds a new client to an inbound
func (c *APIClient) AddClient(inboundID int, clientData map[string]interface{}) error {
	fmt.Printf("[DEBUG] Adding new client to inbound %d\n", inboundID)

	clientJSON, _ := json.Marshal(clientData)
	data := map[string]interface{}{
		"id":       inboundID,
		"settings": fmt.Sprintf(`{"clients":[%s]}`, string(clientJSON)),
	}

	resp, err := c.doRequest("POST", "/panel/api/inbounds/addClient", data, true)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.AddClient(inboundID, clientData)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("API returned success=false: %s", result.Msg)
	}

	fmt.Printf("[DEBUG] Client added successfully\n")
	return nil
}

// GetClientByTgID returns client information by Telegram ID
func (c *APIClient) GetClientByTgID(tgID int64) (map[string]interface{}, error) {
	// Get all inbounds
	inbounds, err := c.GetInbounds()
	if err != nil {
		return nil, fmt.Errorf("failed to get inbounds: %w", err)
	}

	// Search through all inbounds for client with matching tgId
	for _, inbound := range inbounds {
		settingsStr, ok := inbound["settings"].(string)
		if !ok {
			continue
		}

		var settings map[string]interface{}
		if err := json.Unmarshal([]byte(settingsStr), &settings); err != nil {
			continue
		}

		clientsArray, ok := settings["clients"].([]interface{})
		if !ok {
			continue
		}

		for _, c := range clientsArray {
			clientMap, ok := c.(map[string]interface{})
			if !ok {
				continue
			}

			// Check tgId
			if clientTgID, ok := clientMap["tgId"].(string); ok {
				tgIDInt, _ := strconv.ParseInt(clientTgID, 10, 64)
				if tgIDInt == tgID {
					// Add inbound info
					clientMap["_inboundID"] = inbound["id"]
					return clientMap, nil
				}
			} else if clientTgID, ok := clientMap["tgId"].(float64); ok {
				if int64(clientTgID) == tgID {
					clientMap["_inboundID"] = inbound["id"]
					return clientMap, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("client not found")
}

// GetClientLink returns the subscription link for a specific client email
func (c *APIClient) GetClientLink(email string) (string, error) {
	// Get all inbounds to find the client
	inbounds, err := c.GetInbounds()
	if err != nil {
		return "", fmt.Errorf("failed to get inbounds: %w", err)
	}

	// Find the inbound containing this client
	for _, inbound := range inbounds {
		inboundID := int(inbound["id"].(float64))

		// Get clients for this inbound
		clients, err := c.GetClientTraffics(email)
		if err != nil {
			continue
		}

		// If client found in this inbound, construct subscription link
		if len(clients) > 0 {
			// Subscription link format: {baseURL}/sub/{inboundID}/{email}
			subLink := fmt.Sprintf("%s/sub/%d/%s", c.baseURL, inboundID, email)
			return subLink, nil
		}
	}

	return "", fmt.Errorf("client not found")
} // toJSON converts a map to JSON string
func toJSON(data interface{}) string {
	b, _ := json.Marshal(data)
	return string(b)
}

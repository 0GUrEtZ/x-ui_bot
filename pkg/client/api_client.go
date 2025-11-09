package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
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
	loginData := map[string]string{
		"username": c.username,
		"password": c.password,
	}

	resp, err := c.doRequest("POST", "/login", loginData, false)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("login failed with status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Extract session cookie
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "session" || cookie.Name == "3x-ui" {
			c.sessionID = cookie.Value
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

	resp, err := c.doRequest("GET", "/panel/api/inbounds/list", nil, true)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
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

	return result.Obj, nil
}

// UpdateClient updates an existing client in an inbound
func (c *APIClient) UpdateClient(inboundID int, clientID string, clientData map[string]interface{}) error {
	log.Printf("[INFO] UpdateClient called for inbound=%d, client=%s", inboundID, clientID)

	// Get current inbound data
	inbounds, err := c.GetInbounds()
	if err != nil {
		log.Printf("[ERROR] Failed to get inbounds: %v", err)
		return fmt.Errorf("failed to get inbounds: %w", err)
	}
	log.Printf("[INFO] Got %d inbounds", len(inbounds))

	var targetInbound map[string]interface{}
	for _, inbound := range inbounds {
		if int(inbound["id"].(float64)) == inboundID {
			targetInbound = inbound
			break
		}
	}

	if targetInbound == nil {
		return fmt.Errorf("inbound %d not found", inboundID)
	}

	// Parse current settings
	settingsStr, ok := targetInbound["settings"].(string)
	if !ok {
		return fmt.Errorf("invalid settings format")
	}

	var settings map[string]interface{}
	if err := json.Unmarshal([]byte(settingsStr), &settings); err != nil {
		return fmt.Errorf("failed to parse settings: %w", err)
	}

	clientsArray, ok := settings["clients"].([]interface{})
	if !ok {
		return fmt.Errorf("clients array not found")
	}

	// Find and update the target client
	found := false
	var clientUUID string
	var updatedClient map[string]interface{}
	for i, cl := range clientsArray {
		clientMap, ok := cl.(map[string]interface{})
		if !ok {
			continue
		}

		if email, ok := clientMap["email"].(string); ok && email == clientID {
			// Get UUID for API call
			if id, ok := clientMap["id"].(string); ok {
				clientUUID = id
			}
			// Merge new data with existing client data (preserve other fields)
			for key, value := range clientData {
				clientMap[key] = value
			}
			clientsArray[i] = clientMap
			updatedClient = clientMap // Save reference to updated client
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("client %s not found in inbound", clientID)
	}

	if clientUUID == "" {
		return fmt.Errorf("client UUID not found for %s", clientID)
	}

	// Convert client data to JSON (without array wrapper for single client update)
	clientJSON, err := json.Marshal(updatedClient)
	if err != nil {
		return fmt.Errorf("failed to marshal client data: %w", err)
	}

	// Prepare request data - format like AddClient but for update
	data := map[string]interface{}{
		"id":       inboundID,
		"settings": fmt.Sprintf(`{"clients":[%s]}`, string(clientJSON)),
	}

	log.Printf("[INFO] Sending updateClient request for %s (UUID: %s), inbound: %d", clientID, clientUUID, inboundID)
	resp, err := c.doRequest("POST", fmt.Sprintf("/panel/api/inbounds/updateClient/%s", clientUUID), data, true)
	if err != nil {
		log.Printf("[ERROR] doRequest failed: %v", err)
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	log.Printf("[INFO] Got response with status: %d", resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.UpdateClient(inboundID, clientID, clientData)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] Failed to read response body: %v", err)
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] API returned status %d: %s", resp.StatusCode, string(body))
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("[ERROR] Failed to parse JSON response: %v", err)
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	if !result.Success {
		log.Printf("[ERROR] API returned success=false: %s", result.Msg)
		return fmt.Errorf("API returned success=false: %s", result.Msg)
	}

	log.Printf("[INFO] UpdateClient successful for %s", clientID)
	return nil
}

// DeleteClient deletes a client from an inbound
func (c *APIClient) DeleteClient(inboundID int, clientID string) error {
	log.Printf("[INFO] DeleteClient called for inbound=%d, clientID=%s", inboundID, clientID)

	// According to 3x-ui API, delClient endpoint expects clientId (UUID for VMESS/VLESS)
	resp, err := c.doRequest("POST", fmt.Sprintf("/panel/api/inbounds/%d/delClient/%s", inboundID, clientID), map[string]interface{}{
		"id": inboundID,
	}, true)
	if err != nil {
		log.Printf("[ERROR] DeleteClient request failed: %v", err)
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	log.Printf("[INFO] DeleteClient got response with status: %d", resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.DeleteClient(inboundID, clientID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[ERROR] DeleteClient failed to read response: %v", err)
		return fmt.Errorf("failed to read response: %w", err)
	}

	log.Printf("[INFO] DeleteClient response body: %s", string(body))

	if resp.StatusCode != http.StatusOK {
		log.Printf("[ERROR] DeleteClient API returned status %d: %s", resp.StatusCode, string(body))
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		log.Printf("[ERROR] DeleteClient failed to parse JSON: %v", err)
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	if !result.Success {
		log.Printf("[ERROR] DeleteClient API returned success=false: %s", result.Msg)
		return fmt.Errorf("API returned success=false: %s", result.Msg)
	}

	log.Printf("[INFO] DeleteClient successful for clientID=%s", clientID)
	return nil
}

// AddClient adds a new client to an inbound
func (c *APIClient) AddClient(inboundID int, clientData map[string]interface{}) error {

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

// GetPanelSettings returns the panel settings including subscription configuration
func (c *APIClient) GetPanelSettings() (map[string]interface{}, error) {
	resp, err := c.doRequest("POST", "/panel/setting/all", nil, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get panel settings: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get settings, status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Success bool                   `json:"success"`
		Obj     map[string]interface{} `json:"obj"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("API returned success=false")
	}

	return result.Obj, nil
}

// GetClientLink returns the subscription link for a specific client email
func (c *APIClient) GetClientLink(email string) (string, error) {
	// Try to get the link directly from API
	resp, err := c.doRequest("GET", fmt.Sprintf("/panel/api/inbounds/getClientLink/%s", email), nil, true)
	if err == nil {
		defer resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			var result struct {
				Success bool   `json:"success"`
				Obj     string `json:"obj"`
			}
			if json.Unmarshal(body, &result) == nil && result.Success && result.Obj != "" {
				return result.Obj, nil
			}
		}
	} else {
	}

	// Fallback: construct link manually using panel settings

	// Get panel settings to find subURI
	panelSettings, err := c.GetPanelSettings()
	if err != nil {
		return "", fmt.Errorf("failed to get panel settings: %w", err)
	}

	// Find the client to get their subId
	inbounds, err := c.GetInbounds()
	if err != nil {
		return "", fmt.Errorf("failed to get inbounds: %w", err)
	}

	var clientSubID string
	// Find the client in inbounds
	for _, inbound := range inbounds {
		settingsStr, ok := inbound["settings"].(string)
		if !ok {
			continue
		}

		var inboundSettings map[string]interface{}
		if err := json.Unmarshal([]byte(settingsStr), &inboundSettings); err != nil {
			continue
		}

		clientsArray, ok := inboundSettings["clients"].([]interface{})
		if !ok {
			continue
		}

		// Find the client by email
		for _, client := range clientsArray {
			clientMap, ok := client.(map[string]interface{})
			if !ok {
				continue
			}

			if clientEmail, ok := clientMap["email"].(string); ok && clientEmail == email {
				// Found the client! Get their subId
				if subId, ok := clientMap["subId"].(string); ok && subId != "" {
					clientSubID = subId
					break
				}
			}
		}
		if clientSubID != "" {
			break
		}
	}

	if clientSubID == "" {
		return "", fmt.Errorf("client has no subId")
	}

	// Get subURI from panel settings (this is the subscription server URL)
	subURI := ""
	if uri, ok := panelSettings["subURI"].(string); ok && uri != "" {
		subURI = strings.TrimSuffix(uri, "/")

		// If subURI is complete (includes path), just append clientSubID
		// subURI format: https://subscribe.domain.com:port/path or https://subscribe.domain.com:port
		subURL := fmt.Sprintf("%s/%s", subURI, clientSubID)
		return subURL, nil
	}

	// If no subURI configured, build from subDomain + subPort + subPath
	subDomain := ""
	if domain, ok := panelSettings["subDomain"].(string); ok && domain != "" {
		subDomain = domain
	}

	subPort := 0
	if port, ok := panelSettings["subPort"].(float64); ok {
		subPort = int(port)
	}

	subKeyFile := ""
	if keyFile, ok := panelSettings["subKeyFile"].(string); ok {
		subKeyFile = keyFile
	}

	subCertFile := ""
	if certFile, ok := panelSettings["subCertFile"].(string); ok {
		subCertFile = certFile
	}

	// Get subPath from panel settings or use default
	subPath := "/sub/"
	if path, ok := panelSettings["subPath"].(string); ok && path != "" {
		subPath = path
		// Ensure path format
		if !strings.HasPrefix(subPath, "/") {
			subPath = "/" + subPath
		}
		if !strings.HasSuffix(subPath, "/") {
			subPath = subPath + "/"
		}
	}

	// Determine scheme
	scheme := "http"
	if subKeyFile != "" && subCertFile != "" {
		scheme = "https"
	}

	// Build URL from domain + port + path
	if subDomain != "" {
		var baseURL string
		if (scheme == "https" && subPort == 443) || (scheme == "http" && subPort == 80) {
			baseURL = fmt.Sprintf("%s://%s", scheme, subDomain)
		} else if subPort > 0 {
			baseURL = fmt.Sprintf("%s://%s:%d", scheme, subDomain, subPort)
		} else {
			baseURL = fmt.Sprintf("%s://%s", scheme, subDomain)
		}

		subURL := fmt.Sprintf("%s%s%s", baseURL, subPath, clientSubID)
		return subURL, nil
	}

	// Use baseURL as fallback
	subURL := fmt.Sprintf("%s%s%s", c.baseURL, subPath, clientSubID)
	return subURL, nil
}

// toJSON converts a map to JSON string
func toJSON(data interface{}) string {
	b, _ := json.Marshal(data)
	return string(b)
}

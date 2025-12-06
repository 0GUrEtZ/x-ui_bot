package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"
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
func (c *APIClient) Login(ctx context.Context) error {
	loginData := map[string]string{
		"username": c.username,
		"password": c.password,
	}

	resp, err := c.doRequest(ctx, "POST", "/login", loginData, false)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

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
func (c *APIClient) doRequest(ctx context.Context, method, path string, data interface{}, needAuth bool) (*http.Response, error) {
	var body io.Reader
	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal data: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
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
func (c *APIClient) GetStatus(ctx context.Context) (map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "GET", "/panel/api/server/status", nil, true)
	if err != nil {
		return nil, fmt.Errorf("status request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return nil, fmt.Errorf("re-login failed: %w", err)
		}
		return c.GetStatus(ctx)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result, nil
}

// GetInbounds gets list of inbounds
func (c *APIClient) GetInbounds(ctx context.Context) ([]map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "GET", "/panel/api/inbounds/list", nil, true)
	if err != nil {
		return nil, fmt.Errorf("inbounds request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return nil, fmt.Errorf("re-login failed: %w", err)
		}
		return c.GetInbounds(ctx)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("inbounds request failed with status: %d, body: %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if success, ok := result["success"].(bool); ok && success {
		if obj, ok := result["obj"].([]interface{}); ok {
			var inbounds []map[string]interface{}
			for _, item := range obj {
				if inbound, ok := item.(map[string]interface{}); ok {
					inbounds = append(inbounds, inbound)
				}
			}
			return inbounds, nil
		}
	}

	return nil, fmt.Errorf("invalid response format")
}

// GetInbound gets inbound by ID
func (c *APIClient) GetInbound(ctx context.Context, id int) (map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/panel/api/inbounds/get/%d", id), nil, true)
	if err != nil {
		return nil, fmt.Errorf("inbound request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return nil, fmt.Errorf("re-login failed: %w", err)
		}
		return c.GetInbound(ctx, id)
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
func (c *APIClient) ResetClientTraffic(ctx context.Context, email string) error {
	data := map[string]string{"email": email}
	resp, err := c.doRequest(ctx, "POST", "/panel/api/inbounds/resetClientTraffic", data, true)
	if err != nil {
		return fmt.Errorf("reset traffic request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.ResetClientTraffic(ctx, email)
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
func (c *APIClient) GetClientTraffics(ctx context.Context, email string) (map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/panel/api/inbounds/getClientTraffics/%s", email), nil, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		return c.GetClientTraffics(ctx, email)
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
func (c *APIClient) GetClientTrafficsById(ctx context.Context, inboundID int) ([]map[string]interface{}, error) {
	path := fmt.Sprintf("/panel/api/inbounds/getClientTrafficsById/%d", inboundID)

	resp, err := c.doRequest(ctx, "GET", path, nil, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
		return c.GetClientTrafficsById(ctx, inboundID)
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
		log.Printf("[ERROR] GetClientTrafficsById: API returned success=false for inbound %d, response: %s", inboundID, string(bodyBytes))
		return nil, fmt.Errorf("API returned success=false")
	}

	log.Printf("[DEBUG] GetClientTrafficsById: inbound %d returned %d traffic entries", inboundID, len(result.Obj))
	if len(result.Obj) > 0 {
		var emails []string
		for _, item := range result.Obj {
			if email, ok := item["email"].(string); ok {
				emails = append(emails, email)
			}
		}
		log.Printf("[DEBUG] Traffic emails for inbound %d: %v", inboundID, emails)
	}

	return result.Obj, nil
}

// UpdateClientTraffic updates traffic statistics for a specific client
func (c *APIClient) UpdateClientTraffic(ctx context.Context, email string, up int64, down int64) error {
	// URL encode the email to handle special characters
	encodedEmail := url.QueryEscape(email)
	path := fmt.Sprintf("/panel/api/inbounds/updateClientTraffic/%s", encodedEmail)

	payload := map[string]interface{}{
		"up":   up,
		"down": down,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	log.Printf("[DEBUG] UpdateClientTraffic: email=%s, path=%s, up=%d, down=%d", email, path, up, down)

	resp, err := c.doRequest(ctx, "POST", path, bytes.NewReader(body), true)
	if err != nil {
		log.Printf("[ERROR] UpdateClientTraffic request failed for %s: %v", email, err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return err
		}
		return c.UpdateClientTraffic(ctx, email, up, down)
	}

	// Read body for debugging
	bodyBytes, _ := io.ReadAll(resp.Body)
	log.Printf("[DEBUG] UpdateClientTraffic response for %s (status=%d): %s", email, resp.StatusCode, string(bodyBytes))

	var result struct {
		Success bool   `json:"success"`
		Msg     string `json:"msg"`
	}

	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	if !result.Success {
		return fmt.Errorf("API returned success=false: %s", result.Msg)
	}

	return nil
}

// UpdateClient updates an existing client in an inbound
func (c *APIClient) UpdateClient(ctx context.Context, inboundID int, clientID string, clientData map[string]interface{}) error {
	log.Printf("[INFO] UpdateClient called for inbound=%d, client=%s", inboundID, clientID)

	// Get current inbound data
	inbounds, err := c.GetInbounds(ctx)
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
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/panel/api/inbounds/updateClient/%s", clientUUID), data, true)
	if err != nil {
		log.Printf("[ERROR] doRequest failed: %v", err)
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	log.Printf("[INFO] Got response with status: %d", resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.UpdateClient(ctx, inboundID, clientID, clientData)
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
func (c *APIClient) DeleteClient(ctx context.Context, inboundID int, clientID string) error {
	log.Printf("[INFO] DeleteClient called for inbound=%d, clientID=%s", inboundID, clientID)

	// According to 3x-ui API, delClient endpoint expects clientId (UUID for VMESS/VLESS)
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/panel/api/inbounds/%d/delClient/%s", inboundID, clientID), map[string]interface{}{
		"id": inboundID,
	}, true)
	if err != nil {
		log.Printf("[ERROR] DeleteClient request failed: %v", err)
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	log.Printf("[INFO] DeleteClient got response with status: %d", resp.StatusCode)

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.DeleteClient(ctx, inboundID, clientID)
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
func (c *APIClient) AddClient(ctx context.Context, inboundID int, clientData map[string]interface{}) error {

	clientJSON, _ := json.Marshal(clientData)
	data := map[string]interface{}{
		"id":       inboundID,
		"settings": fmt.Sprintf(`{"clients":[%s]}`, string(clientJSON)),
	}

	resp, err := c.doRequest(ctx, "POST", "/panel/api/inbounds/addClient", data, true)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized {
		if err := c.Login(ctx); err != nil {
			return fmt.Errorf("re-login failed: %w", err)
		}
		return c.AddClient(ctx, inboundID, clientData)
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
func (c *APIClient) GetClientByTgID(ctx context.Context, tgID int64) (map[string]interface{}, error) {
	// Get all inbounds
	inbounds, err := c.GetInbounds(ctx)
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
func (c *APIClient) GetPanelSettings(ctx context.Context) (map[string]interface{}, error) {
	resp, err := c.doRequest(ctx, "POST", "/panel/setting/all", nil, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get panel settings: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

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
func (c *APIClient) GetClientLink(ctx context.Context, email string) (string, error) {
	// Try to get the link directly from API
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/panel/api/inbounds/getClientLink/%s", email), nil, true)
	if err == nil {
		defer func() { _ = resp.Body.Close() }()

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
	}

	// Fallback: construct link manually using panel settings

	// Get panel settings to find subURI
	panelSettings, err := c.GetPanelSettings(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get panel settings: %w", err)
	}

	// Find the client to get their subId
	inbounds, err := c.GetInbounds(ctx)
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

// GetClientQRCode generates a QR code image for the client's subscription link
func (c *APIClient) GetClientQRCode(ctx context.Context, email string) ([]byte, error) {
	// Get the subscription link
	link, err := c.GetClientLink(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription link: %w", err)
	}

	// Generate QR code
	qrCode, err := qrcode.Encode(link, qrcode.Medium, 512)
	if err != nil {
		return nil, fmt.Errorf("failed to generate QR code: %w", err)
	}

	return qrCode, nil
}

// GetDatabaseBackup downloads x-ui database backup
func (c *APIClient) GetDatabaseBackup(ctx context.Context) ([]byte, error) {
	url := c.baseURL + "/panel/api/server/getDb"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/octet-stream")
	if c.sessionID != "" {
		req.AddCookie(&http.Cookie{
			Name:  "3x-ui",
			Value: c.sessionID,
		})
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download backup: status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

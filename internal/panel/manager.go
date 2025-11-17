package panel

import (
	"fmt"
	"sync"
	"time"

	"x-ui-bot/internal/config"
	"x-ui-bot/pkg/client"
)

// PanelManager manages multiple 3X-UI panel connections
type PanelManager struct {
	panels  []*PanelInfo
	clients map[string]*client.APIClient // panelName -> client
	mu      sync.RWMutex
}

// PanelInfo contains information about a panel
type PanelInfo struct {
	Name            string
	URL             string
	Username        string
	Password        string
	Enabled         bool
	LimitIP         int
	TrafficLimitGB  int
	BackupDays      int
	LastHealthCheck time.Time
	IsHealthy       bool
	Error           string
}

// NewPanelManager creates a new panel manager
func NewPanelManager(cfg *config.Config) (*PanelManager, error) {
	pm := &PanelManager{
		panels:  make([]*PanelInfo, 0, len(cfg.Panels)),
		clients: make(map[string]*client.APIClient),
	}

	// Initialize panels
	for _, panelCfg := range cfg.Panels {
		if !panelCfg.Enabled {
			continue // Skip disabled panels
		}

		info := &PanelInfo{
			Name:           panelCfg.Name,
			URL:            panelCfg.URL,
			Username:       panelCfg.Username,
			Password:       panelCfg.Password,
			Enabled:        panelCfg.Enabled,
			LimitIP:        panelCfg.LimitIP,
			TrafficLimitGB: panelCfg.TrafficLimitGB,
			BackupDays:     panelCfg.BackupDays,
			IsHealthy:      false,
		}

		pm.panels = append(pm.panels, info)

		// Create API client
		apiClient := client.NewAPIClient(panelCfg.URL, panelCfg.Username, panelCfg.Password)
		pm.clients[panelCfg.Name] = apiClient
	}

	if len(pm.panels) == 0 {
		return nil, fmt.Errorf("no enabled panels found in configuration")
	}

	return pm, nil
}

// GetPanels returns all enabled panels
func (pm *PanelManager) GetPanels() []*PanelInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	panels := make([]*PanelInfo, len(pm.panels))
	copy(panels, pm.panels)
	return panels
}

// GetPanel returns panel info by name
func (pm *PanelManager) GetPanel(name string) (*PanelInfo, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, panel := range pm.panels {
		if panel.Name == name {
			return panel, nil
		}
	}
	return nil, fmt.Errorf("panel %s not found", name)
}

// GetClient returns API client for a panel
func (pm *PanelManager) GetClient(panelName string) (*client.APIClient, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	client, exists := pm.clients[panelName]
	if !exists {
		return nil, fmt.Errorf("client for panel %s not found", panelName)
	}
	return client, nil
}

// GetPanelIndex returns the index of a panel by name
func (pm *PanelManager) GetPanelIndex(name string) (int, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for i, panel := range pm.panels {
		if panel.Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("panel %s not found", name)
}

// GetPanelByIndex returns panel info by index
func (pm *PanelManager) GetPanelByIndex(index int) (*PanelInfo, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if index < 0 || index >= len(pm.panels) {
		return nil, fmt.Errorf("panel index %d out of range", index)
	}
	return pm.panels[index], nil
}

// LoginAll attempts to login to all panels
func (pm *PanelManager) LoginAll() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	var errors []string
	for _, panel := range pm.panels {
		client := pm.clients[panel.Name]
		if err := client.Login(); err != nil {
			panel.IsHealthy = false
			panel.Error = err.Error()
			errors = append(errors, fmt.Sprintf("%s: %v", panel.Name, err))
		} else {
			panel.IsHealthy = true
			panel.LastHealthCheck = time.Now()
			panel.Error = ""
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("failed to login to some panels: %v", errors)
	}

	return nil
}

// HealthCheck performs health check on all panels
func (pm *PanelManager) HealthCheck() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for _, panel := range pm.panels {
		client := pm.clients[panel.Name]

		// Try to get server status
		_, err := client.GetStatus()
		if err != nil {
			// If status fails, try to re-login
			if loginErr := client.Login(); loginErr != nil {
				panel.IsHealthy = false
				panel.Error = fmt.Sprintf("login failed: %v", loginErr)
			} else {
				// Try status again after login
				_, statusErr := client.GetStatus()
				if statusErr != nil {
					panel.IsHealthy = false
					panel.Error = fmt.Sprintf("status check failed: %v", statusErr)
				} else {
					panel.IsHealthy = true
					panel.LastHealthCheck = time.Now()
					panel.Error = ""
				}
			}
		} else {
			panel.IsHealthy = true
			panel.LastHealthCheck = time.Now()
			panel.Error = ""
		}
	}
}

// GetHealthyPanels returns only healthy panels
func (pm *PanelManager) GetHealthyPanels() []*PanelInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var healthy []*PanelInfo
	for _, panel := range pm.panels {
		if panel.IsHealthy {
			healthy = append(healthy, panel)
		}
	}
	return healthy
}

// IsPanelHealthy checks if a specific panel is healthy
func (pm *PanelManager) IsPanelHealthy(panelName string) bool {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, panel := range pm.panels {
		if panel.Name == panelName {
			return panel.IsHealthy
		}
	}
	return false
}

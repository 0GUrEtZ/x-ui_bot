package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"x-ui-bot/internal/config"
	"x-ui-bot/pkg/client"
)

func main() {
	// Load config
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create API client
	apiClient := client.NewAPIClient(cfg.Panel.URL, cfg.Panel.Username, cfg.Panel.Password)

	// Login
	if err := apiClient.Login(context.Background()); err != nil {
		log.Fatalf("Failed to login: %v", err)
	}

	fmt.Println("Logged in successfully")

	// Get inbounds
	inbounds, err := apiClient.GetInbounds(context.Background())
	if err != nil {
		log.Fatalf("Failed to get inbounds: %v", err)
	}

	fmt.Printf("Found %d inbounds\n\n", len(inbounds))

	// Test traffic for each inbound
	for _, inbound := range inbounds {
		inboundID := int(inbound["id"].(float64))
		remark := ""
		if r, ok := inbound["remark"].(string); ok {
			remark = r
		}

		fmt.Printf("=== Inbound %d (%s) ===\n", inboundID, remark)

		// Check if clientStats exists in inbound
		if clientStats, ok := inbound["clientStats"].([]interface{}); ok && len(clientStats) > 0 {
			fmt.Printf("Found %d clientStats entries\n", len(clientStats))

			// Print first 3 entries from clientStats
			for i, stat := range clientStats {
				if i >= 3 {
					break
				}
				statMap := stat.(map[string]interface{})
				email := statMap["email"]
				up := statMap["up"]
				down := statMap["down"]
				fmt.Printf("  [%d] email=%v, up=%v, down=%v\n", i+1, email, up, down)
			}

			// Pretty print first entry
			if len(clientStats) > 0 {
				jsonData, _ := json.MarshalIndent(clientStats[0], "", "  ")
				fmt.Printf("\nFirst clientStats entry:\n%s\n", string(jsonData))
			}
		} else {
			fmt.Println("No clientStats in inbound")
		}

		// Also try GetClientTrafficsById
		traffics, err := apiClient.GetClientTrafficsById(context.Background(), inboundID)
		if err != nil {
			fmt.Printf("ERROR getting traffic via API: %v\n", err)
		} else {
			fmt.Printf("GetClientTrafficsById returned %d entries\n", len(traffics))
		}

		fmt.Println()
	}
}

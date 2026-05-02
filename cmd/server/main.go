package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"scratchpad/internal/browser"
)

func main() {
	log.Println("Starting Browser Engine MVP...")

	// Initialize the headless browser
	engine := browser.NewEngine()
	defer engine.Close()

	targetURL := "https://news.ycombinator.com/"
	log.Printf("Navigating to %s...\n", targetURL)

	// Run the Observation loop
	obs, err := engine.Observe(targetURL)
	if err != nil {
		log.Fatalf("Failed to observe page: %v", err)
	}

	log.Println("Observation complete!")

	// 1. Save the Screenshot to disk to verify it visually
	imgBytes, _ := base64.StdEncoding.DecodeString(obs.Visual)
	err = os.WriteFile("debug_screenshot.jpg", imgBytes, 0644)
	if err != nil {
		log.Fatalf("Failed to save screenshot: %v", err)
	}
	log.Println("Saved debug_screenshot.jpg to disk.")

	// 2. Print out the first 3 nodes from the Spatial Tree
	log.Println("--- Spatial Tree Snippet ---")
	previewCount := 3
	if len(obs.SpatialTree) < 3 {
		previewCount = len(obs.SpatialTree)
	}

	for i := 0; i < previewCount; i++ {
		nodeJSON, _ := json.MarshalIndent(obs.SpatialTree[i], "", "  ")
		fmt.Printf("%s\n", string(nodeJSON))
	}
	fmt.Printf("... and %d more interactable nodes found.\n", len(obs.SpatialTree)-previewCount)
}

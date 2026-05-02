package main

import (
	"encoding/base64"
	"log"
	"os"
	"scratchpad/internal/browser"
	"scratchpad/internal/protocol"
	"time"
)

func main() {
	log.Println("Starting Browser Engine MVP (Phase 4)...")

	engine := browser.NewEngine()
	defer engine.Close()

	// 1. Initial Navigation
	targetURL := "https://news.ycombinator.com/"
	log.Printf("Navigating to %s...\n", targetURL)
	if err := engine.Navigate(targetURL); err != nil {
		log.Fatalf("Failed to navigate: %v", err)
	}

	// 2. First Observation
	log.Println("Capturing initial state...")
	obs1, err := engine.Observe()
	if err != nil {
		log.Fatalf("Failed to observe: %v", err)
	}
	saveScreenshot(obs1.Visual, "debug_1_initial.jpg")

	// 3. Execute Action (Clicking "new")
	// Based on your JSON tree: node_2 ("new") is at X: 230, Y: 12, W: 27, H: 16
	// Center calculation: X = 230 + (27/2) = 243.5 | Y = 12 + (16/2) = 20
	targetX, targetY := 244, 20
	log.Printf("Executing Click Action at X: %d, Y: %d...\n", targetX, targetY)

	action := protocol.ActionRequest{
		Action: protocol.ActionClick,
		X:      targetX,
		Y:      targetY,
	}

	if err := engine.ExecuteAction(action); err != nil {
		log.Fatalf("Failed to execute action: %v", err)
	}

	// Wait a moment for the new page to load (in production, the AI agent handles waiting)
	log.Println("Waiting for page load...")
	time.Sleep(3 * time.Second)

	// 4. Second Observation
	log.Println("Capturing new state...")
	obs2, err := engine.Observe()
	if err != nil {
		log.Fatalf("Failed to observe again: %v", err)
	}
	saveScreenshot(obs2.Visual, "debug_2_after_click.jpg")

	log.Println("Success! Check your folder for both screenshots.")
}

// Helper function to keep main clean
func saveScreenshot(b64 string, filename string) {
	imgBytes, _ := base64.StdEncoding.DecodeString(b64)
	if err := os.WriteFile(filename, imgBytes, 0644); err != nil {
		log.Fatalf("Failed to save screenshot: %v", err)
	}
}

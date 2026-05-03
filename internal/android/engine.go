package android

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"regexp"
	"scratchpad/internal/protocol"
	"strconv"
)

var boundsRegex = regexp.MustCompile(`\[(\d+),(\d+)]\[(\d+),(\d+)]`)

type AndroidEngine struct{}

func NewEngine() *AndroidEngine {
	return &AndroidEngine{}
}

func (e *AndroidEngine) Observe() (*protocol.ObservationResponse, error) {
	// Force android to dump the UI layout into a temp file
	_, err := runADB("shell", "uiautomator", "dump", "/data/local/tmp/window_dump.xml")
	if err != nil {
		return nil, fmt.Errorf("failed to dump UI: %v", err)
	}

	// Read the XML file directly from the device
	xmlData, err := runADB("shell", "cat", "/data/local/tmp/window_dump.xml")
	if err != nil {
		return nil, fmt.Errorf("failed to read UI dump: %v", err)
	}

	// Parse the XML data
	var hierarchy protocol.Hierarchy
	if err := xml.Unmarshal([]byte(xmlData), &hierarchy); err != nil {
		return nil, fmt.Errorf("failed to parse XML: %v", err)
	}

	// Flatten the Android tree into our universal Spatial Node format
	var spatialTree []protocol.SpatialNode
	flattenAndroidTree(hierarchy.Node, &spatialTree)

	// Grab the screenshot
	imgBytes, _ := captureScreen()
	b64Img := base64.StdEncoding.EncodeToString(imgBytes)

	// Fetch the actual viewport size
	viewport := getViewport()

	return &protocol.ObservationResponse{
		Type: "observation",
		SystemState: protocol.SystemState{
			DocumentStatus: "interactive",
		},
		Viewport:    viewport,
		Visual:      b64Img,
		SpatialTree: spatialTree,
		Delta:       nil,
		Logs:        nil,
	}, nil
}

func flattenAndroidTree(node protocol.UINode, tree *[]protocol.SpatialNode) {
	// Only add nodes that the AI can actually interact with or read
	if node.Clickable == "true" || node.Text != "" || node.Desc != "" || node.Scrollable == "true" {
		name := node.Text
		if name == "" {
			name = node.Desc
		}

		// Convert [x1,y1][x2,y2] to X, Y, W, H
		matches := boundsRegex.FindStringSubmatch(node.Bounds)
		if len(matches) == 5 {
			x1, _ := strconv.Atoi(matches[1])
			y1, _ := strconv.Atoi(matches[2])
			x2, _ := strconv.Atoi(matches[3])
			y2, _ := strconv.Atoi(matches[4])

			isScrollable := node.Scrollable == "true"

			*tree = append(*tree, protocol.SpatialNode{
				NodeID: fmt.Sprintf("android_%d_%d", x1, y1),
				Role:   node.Class,
				Name:   name,
				Bounds: protocol.Bounds{X: float64(x1), Y: float64(y1), Width: float64(x2 - x1), Height: float64(y2 - y1)},
				ScrollState: protocol.ScrollState{
					CanScrollDown: isScrollable,
					CanScrollUp:   isScrollable,
				},
				Children: []protocol.SpatialNode{},
			})
		}
	}

	// Recursively parse children
	for _, child := range node.Children {
		flattenAndroidTree(child, tree)
	}
}

func getViewport() protocol.Viewport {
	out, err := runADB("shell", "wm", "size")
	if err != nil {
		// Matches "Physical size: 1080x2400" or "Override size: 1080x1920"
		re := regexp.MustCompile(`size:\s*(\d+)x(\d+)`)
		matches := re.FindAllStringSubmatch(out, -1)

		if len(matches) > 0 {
			// Get the last match in case the emulator has an "Override size"
			lastMatch := matches[len(matches)-1]
			w, _ := strconv.Atoi(lastMatch[1])
			h, _ := strconv.Atoi(lastMatch[2])
			return protocol.Viewport{Width: w, Height: h}
		}
	}
	// Safe fallback if ADB fails
	return protocol.Viewport{Width: 1000, Height: 1920}
}

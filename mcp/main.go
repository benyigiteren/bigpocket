package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// MCP Protocol structures (JSON-RPC 2.0)
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type Tool struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

type CallToolParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
}

type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

var (
	apiURL   = "http://127.0.0.1:8085"
	apiToken = ""
	client   = &http.Client{Timeout: 8 * time.Second}
)

func main() {
	urlFlag := flag.String("url", "http://127.0.0.1:8085", "BigPocket Desktop HTTP API base URL")
	apiKeyFlag := flag.String("apikey", "", "BigPocket API Key / Password (optional, overrides BIGPOCKET_API_KEY env)")
	flag.Parse()

	if *urlFlag != "" {
		apiURL = strings.TrimRight(*urlFlag, "/")
	}

	if *apiKeyFlag != "" {
		apiToken = *apiKeyFlag
	} else if envKey := os.Getenv("BIGPOCKET_API_KEY"); envKey != "" {
		apiToken = envKey
	}

	scanner := bufio.NewScanner(os.Stdin)
	// Allow large lines
	buf := make([]byte, 1024*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			sendError(nil, -32700, "Parse error", err.Error())
			continue
		}

		handleRequest(&req)
	}
}

func sendResponse(id interface{}, result interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}

func sendError(id interface{}, code int, message string, data interface{}) {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &RPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
	bytes, _ := json.Marshal(resp)
	fmt.Println(string(bytes))
}

func handleRequest(req *JSONRPCRequest) {
	switch req.Method {
	case "initialize":
		sendResponse(req.ID, map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
			"serverInfo": map[string]interface{}{
				"name":    "bigpocket-streamdeck-mcp",
				"version": "1.0.0",
			},
		})

	case "notifications/initialized":
		// No response required for notifications

	case "tools/list":
		tools := []Tool{
			{
				Name:        "get_system_status",
				Description: "Gets BigPocket PC server status, IP addresses, ports, and system resource statistics.",
				InputSchema: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
			{
				Name:        "get_streamdeck_config",
				Description: "Retrieves current Stream Deck configuration, grid layout (rows, cols), and all buttons.",
				InputSchema: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
			{
				Name:        "set_streamdeck_button",
				Description: "Configures or edits a specific Stream Deck button slot (e.g. set label, hotkey combination, CMD application path, icon).",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"button_id": map[string]interface{}{
							"type":        "integer",
							"description": "Button slot index (0-based: 0 is Slot #1, 1 is Slot #2, etc.)",
						},
						"label": map[string]interface{}{
							"type":        "string",
							"description": "Display label for the button (e.g. 'Mute Mic', 'Open Chrome')",
						},
						"type": map[string]interface{}{
							"type":        "string",
							"enum":        []string{"hotkey", "command"},
							"description": "Action type: 'hotkey' for keyboard shortcuts, 'command' for launching apps or commands",
						},
						"value": map[string]interface{}{
							"type":        "string",
							"description": "Hotkey string (e.g. 'ctrl+shift+m', 'f13', 'alt+tab') or Command/App path (e.g. 'calc.exe', 'cmd.exe /c start chrome')",
						},
						"icon": map[string]interface{}{
							"type":        "string",
							"description": "Unicode icon key (e.g. 'mic', 'desktop', 'volume_up', 'volume_mute', 'play', 'pause', 'terminal', 'camera', 'gear') or 'custom:icons/filename.png'",
						},
					},
					"required": []string{"button_id", "label", "type", "value"},
				},
			},
			{
				Name:        "set_grid_layout",
				Description: "Changes Stream Deck grid dimensions (number of rows and columns) and auto-resizes buttons list.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"rows": map[string]interface{}{
							"type":        "integer",
							"description": "Number of rows (1 to 5)",
						},
						"cols": map[string]interface{}{
							"type":        "integer",
							"description": "Number of columns (2 to 8)",
						},
					},
					"required": []string{"rows", "cols"},
				},
			},
			{
				Name:        "trigger_button",
				Description: "Directly triggers and executes a Stream Deck button action on the PC (simulates its hotkey or runs its command).",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"button_id": map[string]interface{}{
							"type":        "integer",
							"description": "The button slot index to trigger (0-based)",
						},
					},
					"required": []string{"button_id"},
				},
			},
			{
				Name:        "list_installed_apps",
				Description: "Fetches list of all installed Windows applications and desktop shortcuts with their executable paths and icons.",
				InputSchema: map[string]interface{}{
					"type":       "object",
					"properties": map[string]interface{}{},
				},
			},
		}
		sendResponse(req.ID, map[string]interface{}{"tools": tools})

	case "tools/call":
		var params CallToolParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			sendError(req.ID, -32602, "Invalid params", err.Error())
			return
		}

		result, isErr := executeTool(params.Name, params.Arguments)
		sendResponse(req.ID, ToolCallResult{
			Content: []ToolContent{
				{
					Type: "text",
					Text: result,
				},
			},
			IsError: isErr,
		})

	default:
		sendError(req.ID, -32601, "Method not found", fmt.Sprintf("Unsupported method: %s", req.Method))
	}
}

func executeTool(name string, args map[string]interface{}) (string, bool) {
	switch name {
	case "get_system_status":
		data, err := doAPIRequest("GET", "/system_info", nil)
		if err != nil {
			return fmt.Sprintf("Error fetching system info: %v", err), true
		}
		return string(data), false

	case "get_streamdeck_config":
		data, err := doAPIRequest("GET", "/config", nil)
		if err != nil {
			return fmt.Sprintf("Error fetching config: %v", err), true
		}
		return string(data), false

	case "set_streamdeck_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Missing button_id", true
		}
		btnID := int(btnIDFloat)
		label, _ := args["label"].(string)
		btnType, _ := args["type"].(string)
		val, _ := args["value"].(string)
		icon, _ := args["icon"].(string)
		if icon == "" {
			if btnType == "hotkey" {
				icon = "gear"
			} else {
				icon = "terminal"
			}
		}

		// Fetch current config first
		data, err := doAPIRequest("GET", "/config", nil)
		if err != nil {
			return fmt.Sprintf("Failed to load config: %v", err), true
		}
		var cfgResp map[string]interface{}
		json.Unmarshal(data, &cfgResp)
		cfg, ok := cfgResp["config"].(map[string]interface{})
		if !ok {
			return "Invalid config format received from BigPocket", true
		}

		buttons, ok := cfg["stream_deck_buttons"].([]interface{})
		if !ok {
			buttons = []interface{}{}
		}

		// Update or append
		found := false
		for i, b := range buttons {
			bMap, ok := b.(map[string]interface{})
			if !ok {
				continue
			}
			if int(bMap["id"].(float64)) == btnID {
				bMap["label"] = label
				bMap["type"] = btnType
				bMap["value"] = val
				bMap["icon"] = icon
				buttons[i] = bMap
				found = true
				break
			}
		}

		if !found {
			buttons = append(buttons, map[string]interface{}{
				"id":    btnID,
				"label": label,
				"type":  btnType,
				"value": val,
				"icon":  icon,
			})
		}
		cfg["stream_deck_buttons"] = buttons

		// Save updated config
		postData, _ := json.Marshal(cfg)
		saveResp, err := doAPIRequest("POST", "/config", postData)
		if err != nil {
			return fmt.Sprintf("Failed to save config: %v", err), true
		}
		return fmt.Sprintf("Successfully updated button %d (%s):\n%s", btnID, label, string(saveResp)), false

	case "set_grid_layout":
		rowsFloat, _ := args["rows"].(float64)
		colsFloat, _ := args["cols"].(float64)
		rows := int(rowsFloat)
		cols := int(colsFloat)
		if rows < 1 || rows > 5 || cols < 2 || cols > 8 {
			return "Invalid dimensions. Rows must be 1-5, Cols must be 2-8.", true
		}

		// Fetch current config
		data, err := doAPIRequest("GET", "/config", nil)
		if err != nil {
			return fmt.Sprintf("Failed to fetch config: %v", err), true
		}
		var cfgResp map[string]interface{}
		json.Unmarshal(data, &cfgResp)
		cfg, ok := cfgResp["config"].(map[string]interface{})
		if !ok {
			return "Invalid config format", true
		}

		cfg["stream_deck_rows"] = rows
		cfg["stream_deck_cols"] = cols

		// Ensure total buttons match rows * cols
		totalNeeded := rows * cols
		buttons, _ := cfg["stream_deck_buttons"].([]interface{})
		for len(buttons) < totalNeeded {
			buttons = append(buttons, map[string]interface{}{
				"id":    len(buttons),
				"label": fmt.Sprintf("Slot %d", len(buttons)+1),
				"type":  "hotkey",
				"value": "",
				"icon":  "gear",
			})
		}
		cfg["stream_deck_buttons"] = buttons

		postData, _ := json.Marshal(cfg)
		saveResp, err := doAPIRequest("POST", "/config", postData)
		if err != nil {
			return fmt.Sprintf("Failed to update grid size: %v", err), true
		}
		return fmt.Sprintf("Successfully resized grid to %dx%d (%d buttons total):\n%s", rows, cols, totalNeeded, string(saveResp)), false

	case "trigger_button":
		btnIDFloat, ok := args["button_id"].(float64)
		if !ok {
			return "Missing button_id", true
		}
		btnID := int(btnIDFloat)
		postData, _ := json.Marshal(map[string]interface{}{"button_id": btnID})
		resp, err := doAPIRequest("POST", "/stream_deck_trigger", postData)
		if err != nil {
			return fmt.Sprintf("Failed to trigger button %d: %v", btnID, err), true
		}
		return fmt.Sprintf("Triggered button %d response:\n%s", btnID, string(resp)), false

	case "list_installed_apps":
		data, err := doAPIRequest("GET", "/installed_apps", nil)
		if err != nil {
			return fmt.Sprintf("Failed to list installed apps: %v", err), true
		}
		return string(data), false

	default:
		return fmt.Sprintf("Unknown tool name: %s", name), true
	}
}

func doAPIRequest(method, path string, body []byte) ([]byte, error) {
	reqURL := apiURL + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, reqURL, reader)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if apiToken != "" {
		req.Header.Set("X-Password", apiToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP connection to BigPocket at %s failed: %w", reqURL, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBytes))
	}

	return respBytes, nil
}

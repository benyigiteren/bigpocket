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
	// Never let a malformed payload crash the long-lived stdio bridge.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "bigpocket-mcp: recovered from panic handling %q: %v\n", req.Method, r)
			if req.ID != nil {
				sendError(req.ID, -32603, "Internal error", fmt.Sprint(r))
			}
		}
	}()

	// Preferred path: forward everything (initialize, tools/list, tools/call, ping, ...)
	// to the live BigPocket desktop server so the toolset stays perfectly in sync.
	if respBytes, handled := forwardToDesktop(req); handled {
		if len(respBytes) > 0 {
			fmt.Println(string(respBytes))
		}
		return
	}

	// Local fallback used only when the desktop server cannot be reached.
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
				"version": "1.1.0",
			},
		})

	case "notifications/initialized":
		// No response required for notifications

	case "ping":
		sendResponse(req.ID, map[string]interface{}{})

	case "tools/list":
		sendResponse(req.ID, map[string]interface{}{"tools": fallbackTools()})

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

// forwardToDesktop relays the raw JSON-RPC request to the BigPocket desktop MCP
// endpoint (/mcp). It returns (responseBytes, handled).
//   - handled == true: the request was answered here (responseBytes may be nil
//     when an error response was already emitted).
//   - handled == false: the desktop server could not be reached, so the caller
//     should use its local fallback.
func forwardToDesktop(req *JSONRPCRequest) ([]byte, bool) {
	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, false
	}
	respBytes, err := doAPIRequest("POST", "/mcp", reqBytes)
	if err != nil {
		// Genuine connectivity failures fall through to the local fallback,
		// but auth/HTTP errors from a reachable server are surfaced verbatim.
		if req.ID != nil && !strings.Contains(err.Error(), "HTTP connection") {
			sendError(req.ID, -32603, "BigPocket desktop server error", err.Error())
			return nil, true
		}
		return nil, false
	}
	if len(respBytes) == 0 {
		// 204 No Content (e.g. notifications/initialized): nothing to relay.
		return nil, false
	}
	var parsed JSONRPCResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, false
	}
	if parsed.Result == nil && parsed.Error == nil {
		return nil, false
	}
	return respBytes, true
}

// ---------------------------------------------------------------
// Fallback tool registry (mirrors the BigPocket desktop toolset)
// ---------------------------------------------------------------

func prop(kind, desc string) map[string]interface{} {
	return map[string]interface{}{"type": kind, "description": desc}
}

func enumProp(kind string, values []string, desc string) map[string]interface{} {
	return map[string]interface{}{"type": kind, "enum": values, "description": desc}
}

func schema(props map[string]interface{}, required ...string) map[string]interface{} {
	if props == nil {
		props = map[string]interface{}{}
	}
	s := map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// widgetProps returns the shared visual/behaviour properties supported by the
// desktop widget engine (buttons, sliders, clocks, live info, toggles, media).
func widgetProps() map[string]interface{} {
	return map[string]interface{}{
		"label":           prop("string", "Display label for the button"),
		"type":            enumProp("string", []string{"hotkey", "command", "url", "text_snippet", "toggle", "volume_slider", "app_volume_slider", "brightness_slider", "live_info", "clock_widget", "media_widget"}, "Action or widget type"),
		"value":           prop("string", "Hotkey ('ctrl+shift+m'), command ('calc.exe'), URL, snippet text or widget parameter ('master_volume', 'display_brightness', 'cpu', 'ram', 'net_speed')"),
		"icon":            prop("string", "Icon key ('mic', 'volume', 'play', 'calc', 'web', 'lock', 'task', 'cpu', 'ram', 'clock', 'desktop', 'link', 'terminal', 'folder', 'wifi', 'monitor', 'bell') or 'custom:icons/file.png'"),
		"page":            prop("integer", "Page index (0-based)"),
		"col_span":        prop("integer", "Width span in grid cells (1 to 4)"),
		"row_span":        prop("integer", "Height span in grid cells (1 to 2)"),
		"color":           prop("string", "Accent hex color (e.g. '#818CF8', '#10B981', '#38BDF8')"),
		"bg_style":        enumProp("string", []string{"default", "transparent", "glass", "outline"}, "Card background style"),
		"font_family":     enumProp("string", []string{"mono", "sans", "lcd", "serif", "tech"}, "Typography style"),
		"clock_style":     enumProp("string", []string{"apple_lockscreen", "apple_stacked", "analog", "digital_seconds", "digital_clean"}, "Clock display mode (clock_widget only)"),
		"clock_font_size": enumProp("string", []string{"auto", "compact", "regular", "large", "huge"}, "Clock text size"),
	}
}

func buttonRef(desc string) map[string]interface{} {
	return prop("integer", desc)
}

func fallbackTools() []Tool {
	return []Tool{
		{
			Name:        "get_system_status",
			Description: "Gets BigPocket PC server status, IP addresses, ports, and system resource statistics.",
			InputSchema: schema(nil),
		},
		{
			Name:        "get_streamdeck_config",
			Description: "Retrieves current Stream Deck configuration, grid layout (rows, cols), active page, all pages, and all buttons with full properties.",
			InputSchema: schema(nil),
		},
		{
			Name:        "get_streamdeck_presets",
			Description: "Returns all supported widget types, presets, clock styles, font families, background styles and studio templates.",
			InputSchema: schema(nil),
		},
		{
			Name:        "set_streamdeck_button",
			Description: "Edits an existing button/widget in place with extreme precision (label, type, value, icon, page, col_span, row_span, color, bg_style, clock_style, font_family, clock_font_size). Unspecified fields are preserved.",
			InputSchema: schema(widgetProps(), "button_id"),
		},
		{
			Name:        "add_streamdeck_button",
			Description: "Creates and appends a brand new button/widget to a page, optionally based on a preset ('clock', 'clock_analog', 'clock_seconds', 'mini_clock', 'telemetry_combo', 'cpu', 'ram', 'volume', 'app_session', 'brightness', 'media', 'toggle_mic', 'toggle_mute', 'toggle_monitor', 'app_calc', 'app_lock', 'app_terminal', 'app_discord', 'app_spotify').",
			InputSchema: schema(withPreset(widgetProps())),
		},
		{
			Name:        "create_custom_widget",
			Description: "Convenience tool that builds a customized widget/button from scratch (hotkey, command, URL, snippet, live info, clock, volume/brightness slider, toggle or media) with full visual parameters.",
			InputSchema: schema(widgetProps(), "label", "type", "value"),
		},
		{
			Name:        "batch_create_streamdeck_buttons",
			Description: "Creates multiple buttons/widgets in one single operation. Ideal for generating entire dashboards (gaming, coding, streaming, audio control).",
			InputSchema: schema(map[string]interface{}{
				"page":            prop("integer", "Page index to add buttons to (defaults to the active page)"),
				"clear_page_first": prop("boolean", "If true, clears existing buttons on the target page first"),
				"buttons":         prop("array", "Array of button/widget definitions (preset key or full property objects)"),
			}, "buttons"),
		},
		{
			Name:        "duplicate_streamdeck_button",
			Description: "Duplicates an existing button to a target page or the active page.",
			InputSchema: schema(map[string]interface{}{
				"button_id":   buttonRef("Button ID to duplicate"),
				"target_page": prop("integer", "Target page index (optional, defaults to the same page)"),
			}, "button_id"),
		},
		{
			Name:        "delete_streamdeck_button",
			Description: "Deletes a specific button by its ID.",
			InputSchema: schema(map[string]interface{}{
				"button_id": buttonRef("The button ID to remove"),
			}, "button_id"),
		},
		{
			Name:        "swap_streamdeck_buttons",
			Description: "Swaps the visual position of two buttons.",
			InputSchema: schema(map[string]interface{}{
				"button_id_1": buttonRef("First button ID"),
				"button_id_2": buttonRef("Second button ID"),
			}, "button_id_1", "button_id_2"),
		},
		{
			Name:        "set_grid_layout",
			Description: "Changes Stream Deck grid dimensions (number of rows and columns) and updates the layout.",
			InputSchema: schema(map[string]interface{}{
				"rows": prop("integer", "Number of rows (1 to 5)"),
				"cols": prop("integer", "Number of columns (2 to 8)"),
			}, "rows", "cols"),
		},
		{
			Name:        "trigger_button",
			Description: "Directly triggers and executes a button action on the PC (simulates its hotkey or runs its command).",
			InputSchema: schema(map[string]interface{}{
				"button_id": buttonRef("The button slot index to trigger (0-based)"),
			}, "button_id"),
		},
		{
			Name:        "streamdeck_toggle_button",
			Description: "Toggles the state ('on' <-> 'off') and triggers the action of a toggle-type button (mic mute, speaker mute, virtual monitor or custom).",
			InputSchema: schema(map[string]interface{}{
				"button_id": buttonRef("The button slot index to toggle (0-based)"),
			}, "button_id"),
		},
		{
			Name:        "streamdeck_switch_page",
			Description: "Switches the active Stream Deck tab/page across mobile and desktop interfaces.",
			InputSchema: schema(map[string]interface{}{
				"page": prop("integer", "Page index (0, 1, 2...)"),
			}, "page"),
		},
		{
			Name:        "pomodoro_toggle",
			Description: "Starts, pauses or resets a Pomodoro countdown widget on the Stream Deck canvas and returns its remaining time.",
			InputSchema: schema(map[string]interface{}{
				"button_id": prop("integer", "The Pomodoro widget button ID (0-based)"),
				"action":    enumProp("string", []string{"start", "pause", "reset", "toggle"}, "Countdown action (defaults to 'toggle')"),
			}),
		},
		{
			Name:        "create_streamdeck_page",
			Description: "Creates a brand new Stream Deck page tab.",
			InputSchema: schema(map[string]interface{}{
				"title": prop("string", "Title of the new page (e.g. 'Oyun Masası', 'Canlı Yayın')"),
			}, "title"),
		},
		{
			Name:        "rename_streamdeck_page",
			Description: "Renames an existing Stream Deck page.",
			InputSchema: schema(map[string]interface{}{
				"page_id": prop("integer", "The page ID to rename"),
				"title":   prop("string", "New title for the page"),
			}, "page_id", "title"),
		},
		{
			Name:        "delete_streamdeck_page",
			Description: "Deletes a Stream Deck page and all buttons belonging to it.",
			InputSchema: schema(map[string]interface{}{
				"page_id": prop("integer", "The page ID to delete"),
			}, "page_id"),
		},
		{
			Name:        "clear_streamdeck_page",
			Description: "Clears all buttons on a specific page.",
			InputSchema: schema(map[string]interface{}{
				"page_id": prop("integer", "The page ID to clear (defaults to the active page when omitted)"),
			}),
		},
		{
			Name:        "apply_streamdeck_template",
			Description: "Applies a curated studio template to a page ('pro_studio', 'media_lounge', 'gaming_obs', 'developer', 'system_control').",
			InputSchema: schema(map[string]interface{}{
				"template": enumProp("string", []string{"pro_studio", "media_lounge", "gaming_obs", "developer", "system_control"}, "Template design name"),
				"page":     prop("integer", "Page index to apply the template to (defaults to the active page)"),
			}, "template"),
		},
		{
			Name:        "streamdeck_set_volume",
			Description: "Controls the master PC volume ('up', 'down', 'mute' or 'set' with a percentage 0-100).",
			InputSchema: schema(map[string]interface{}{
				"action": enumProp("string", []string{"set", "up", "down", "mute"}, "Volume action to perform"),
				"level":  prop("number", "Volume percentage (0-100), used when action is 'set'"),
			}, "action"),
		},
		{
			Name:        "streamdeck_set_brightness",
			Description: "Controls the PC display brightness percentage (0-100).",
			InputSchema: schema(map[string]interface{}{
				"level": prop("integer", "Brightness percentage (0-100)"),
			}, "level"),
		},
		{
			Name:        "list_installed_apps",
			Description: "Fetches the list of all installed Windows applications and desktop shortcuts with their paths.",
			InputSchema: schema(nil),
		},
		{
			Name:        "send_pc_notification",
			Description: "Displays a desktop toast/balloon notification on the PC.",
			InputSchema: schema(map[string]interface{}{
				"title":   prop("string", "Notification title"),
				"message": prop("string", "Notification body text"),
			}, "title", "message"),
		},
		{
			Name:        "sync_clipboard",
			Description: "Sets the PC clipboard text and syncs it with connected mobile devices.",
			InputSchema: schema(map[string]interface{}{
				"text": prop("string", "The text to copy to the PC clipboard"),
			}, "text"),
		},
		{
			Name:        "get_system_stats",
			Description: "Retrieves instantaneous real-time PC CPU and RAM usage percentages.",
			InputSchema: schema(nil),
		},
		{
			Name:        "get_network_stats",
			Description: "Retrieves live network throughput (download/upload kbps) plus CPU, RAM and CPU temperature of the PC.",
			InputSchema: schema(nil),
		},
		{
			Name:        "net_speed",
			Description: "Returns the instantaneous network throughput of the PC as net_down_kbps / net_up_kbps (kilobits per second) for display in the Ağ Hızı widget.",
			InputSchema: schema(nil),
		},
		{
			Name:        "get_active_window",
			Description: "Returns the title and process name of the Windows application currently in the foreground.",
			InputSchema: schema(nil),
		},
		{
			Name:        "capture_screen",
			Description: "Captures a screenshot of the PC display (or a specific monitor) and returns it as a base64 PNG image.",
			InputSchema: schema(map[string]interface{}{
				"monitor":   prop("integer", "Monitor index to capture (0-based, defaults to the primary monitor)"),
				"save_path": prop("string", "Optional absolute path to also save the PNG file on disk"),
			}),
		},
		{
			Name:        "set_autostart",
			Description: "Enables or disables launching BigPocket automatically with Windows (HKCU Run key).",
			InputSchema: schema(map[string]interface{}{
				"enable": prop("boolean", "True to launch BigPocket on Windows startup, false to disable it"),
			}, "enable"),
		},
		{
			Name:        "setup_usb_reverse",
			Description: "Sets up ADB reverse port forwarding over a physical USB cable for 1ms ultra-low latency.",
			InputSchema: schema(nil),
		},
	}
}

func withPreset(props map[string]interface{}) map[string]interface{} {
	out := map[string]interface{}{
		"preset": prop("string", "Optional preset key to copy defaults from ('clock', 'cpu', 'ram', 'volume', 'brightness', 'media', 'toggle_mic', 'toggle_mute', 'toggle_monitor', 'app_calc', ...)"),
	}
	for k, v := range props {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------
// Fallback tool execution (HTTP endpoints of the desktop server)
// ---------------------------------------------------------------

func executeTool(name string, args map[string]interface{}) (string, bool) {
	switch name {
	case "get_system_status":
		return getTool("/system_info")
	case "get_streamdeck_config":
		return getTool("/config")
	case "get_streamdeck_presets":
		return getTool("/api/streamdeck/presets")
	case "get_system_stats":
		return getTool("/api/system/stats")
	case "get_network_stats", "net_speed":
		return getTool("/api/system/network")
	case "get_active_window":
		return getTool("/api/system/active_window")
	case "list_installed_apps":
		return getTool("/api/apps")

	case "set_streamdeck_button":
		// The desktop merges only the supplied fields, so page/row_span/col_span/
		// color/clock_font_size of untouched properties are never overwritten.
		return postTool("/api/streamdeck/button", args)
	case "add_streamdeck_button", "create_custom_widget":
		return postTool("/api/streamdeck/button", withAction(args, "add"))
	case "batch_create_streamdeck_buttons":
		return postTool("/api/streamdeck/button", withAction(args, "batch"))
	case "duplicate_streamdeck_button":
		return postTool("/api/streamdeck/button", withAction(args, "duplicate"))
	case "delete_streamdeck_button":
		return postTool("/api/streamdeck/button", withAction(args, "delete"))

	case "set_grid_layout":
		return postTool("/api/streamdeck/layout", args)
	case "trigger_button", "streamdeck_toggle_button":
		return postTool("/api/streamdeck/trigger", args)
	case "pomodoro_toggle":
		return postTool("/api/pomodoro/toggle", args)
	case "streamdeck_set_volume":
		return postTool("/api/streamdeck/volume", args)
	case "streamdeck_switch_page", "streamdeck_set_active_page":
		return postTool("/api/streamdeck/pages", args)
	case "send_pc_notification":
		return postTool("/api/notify", args)
	case "sync_clipboard":
		return postTool("/api/clipboard", args)
	case "set_autostart":
		return postTool("/api/autostart", args)
	case "capture_screen":
		return postTool("/api/screen/capture", args)
	case "setup_usb_reverse":
		return postTool("/api/usb/forward", map[string]interface{}{})

	case "swap_streamdeck_buttons":
		return swapStreamDeckButtons(args)

	default:
		return fmt.Sprintf("Tool '%s' is served by the BigPocket desktop MCP endpoint (%s/mcp), which is currently unreachable. Start the BigPocket desktop app and retry.", name, apiURL), true
	}
}

func withAction(args map[string]interface{}, action string) map[string]interface{} {
	payload := make(map[string]interface{}, len(args)+1)
	for k, v := range args {
		payload[k] = v
	}
	payload["action"] = action
	return payload
}

func getTool(path string) (string, bool) {
	data, err := doAPIRequest("GET", path, nil)
	if err != nil {
		return err.Error(), true
	}
	return string(data), false
}

func postTool(path string, payload interface{}) (string, bool) {
	data, err := doAPIRequest("POST", path, payload)
	if err != nil {
		return err.Error(), true
	}
	return string(data), false
}

// swapStreamDeckButtons reorders two buttons in the stored config. Only the array
// order changes, so every other field (page, spans, color, clock_font_size, ...)
// is preserved untouched.
func swapStreamDeckButtons(args map[string]interface{}) (string, bool) {
	idA, okA := args["button_id_1"].(float64)
	idB, okB := args["button_id_2"].(float64)
	if !okA || !okB {
		return "Missing button_id_1 / button_id_2 (integers required)", true
	}

	data, err := doAPIRequest("GET", "/config", nil)
	if err != nil {
		return err.Error(), true
	}
	var cfgResp map[string]interface{}
	if err := json.Unmarshal(data, &cfgResp); err != nil {
		return fmt.Sprintf("Failed to parse config: %v", err), true
	}
	cfg, ok := cfgResp["config"].(map[string]interface{})
	if !ok {
		return "Invalid config format received from BigPocket", true
	}
	buttons, ok := cfg["stream_deck_buttons"].([]interface{})
	if !ok {
		return "No stream_deck_buttons array in config", true
	}

	idxA, idxB := -1, -1
	for i, raw := range buttons {
		bm, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, ok := bm["id"].(float64) // safe assertion: never panics on malformed data
		if !ok {
			continue
		}
		if int(id) == int(idA) {
			idxA = i
		}
		if int(id) == int(idB) {
			idxB = i
		}
	}
	if idxA < 0 || idxB < 0 {
		return fmt.Sprintf("Button not found (requested ids: %d, %d)", int(idA), int(idB)), true
	}

	buttons[idxA], buttons[idxB] = buttons[idxB], buttons[idxA]
	cfg["stream_deck_buttons"] = buttons

	postData, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Sprintf("Failed to encode config: %v", err), true
	}
	if _, err := doAPIRequest("POST", "/config", postData); err != nil {
		return err.Error(), true
	}
	return fmt.Sprintf("Successfully swapped buttons %d and %d", int(idA), int(idB)), false
}

func doAPIRequest(method, path string, body interface{}) ([]byte, error) {
	reqURL := apiURL + path
	var reader io.Reader
	hasBody := false

	switch b := body.(type) {
	case nil:
		// no body
	case []byte:
		reader = bytes.NewReader(b)
		hasBody = true
	case json.RawMessage:
		reader = bytes.NewReader(b)
		hasBody = true
	default:
		encoded, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("failed to encode request body: %w", err)
		}
		reader = bytes.NewReader(encoded)
		hasBody = true
	}

	req, err := http.NewRequest(method, reqURL, reader)
	if err != nil {
		return nil, err
	}

	if hasBody {
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

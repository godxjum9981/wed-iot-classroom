package discord

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// ── Discord Webhook structs ───────────────────────────────────────────────

type webhookPayload struct {
	Username  string  `json:"username,omitempty"`
	AvatarURL string  `json:"avatar_url,omitempty"`
	Embeds    []Embed `json:"embeds"`
}

type Embed struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	Color       int          `json:"color"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      *EmbedFooter `json:"footer,omitempty"`
	Timestamp   string       `json:"timestamp,omitempty"`
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

type EmbedFooter struct {
	Text string `json:"text"`
}

// Color palette
const (
	ColorGreen  = 0x4ade80
	ColorRed    = 0xf43f5e
	ColorAmber  = 0xfbbf24
	ColorCyan   = 0x38bdf8
	ColorTeal   = 0x2dd4bf
	ColorIndigo = 0x818cf8
)

// send posts a payload to the given webhook URL
func send(webhookURL string, payload webhookPayload) {
	if webhookURL == "" {
		return
	}

	b, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[Discord] Marshal error: %v", err)
		return
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("[Discord] Send error: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("[Discord] HTTP %d from webhook", resp.StatusCode)
	}
}

// ── Public helpers ────────────────────────────────────────────────────────

// SendSensorAlert fires when sensor data crosses a critical threshold.
func SendSensorAlert(webhookURL, esp32ID, alertType string, value float64, threshold float64) {
	emoji := "⚠️"
	color := ColorAmber
	title := "Sensor Alert"
	var desc string

	switch alertType {
	case "temp_critical":
		emoji = "🌡️"
		color = ColorRed
		title = "🌡️ Critical Temperature"
		desc = fmt.Sprintf("Temperature **%.1f°C** exceeded critical threshold of **%.1f°C**", value, threshold)
	case "temp_high":
		emoji = "🌡️"
		color = ColorAmber
		title = "🌡️ High Temperature"
		desc = fmt.Sprintf("Temperature **%.1f°C** — AC should activate", value)
	case "lux_low":
		emoji = "💡"
		color = ColorIndigo
		title = "💡 Low Light Detected"
		desc = fmt.Sprintf("Lux dropped to **%.0f lux** — Lights should activate", value)
	case "no_presence":
		emoji = "🚶"
		color = ColorTeal
		title = "🚶 Room Vacant"
		desc = "No IR motion for 5 minutes — All AUTO devices turned OFF"
	case "room_entry":
		emoji = "🏫"
		color = ColorGreen
		title = "🏫 Room Occupied"
		desc = "IR motion detected — AUTO devices activated"
	}

	send(webhookURL, webhookPayload{
		Username:  "SmartRoom 🏫",
		AvatarURL: "https://cdn-icons-png.flaticon.com/512/2784/2784459.png",
		Embeds: []Embed{{
			Title:       fmt.Sprintf("%s %s", emoji, title),
			Description: desc,
			Color:       color,
			Fields: []EmbedField{
				{Name: "📡 Device", Value: fmt.Sprintf("`%s`", esp32ID), Inline: true},
				{Name: "🔢 Value", Value: fmt.Sprintf("%.2f", value), Inline: true},
			},
			Footer:    &EmbedFooter{Text: "Smart Classroom IoT"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}},
	})
}

// SendDeviceEvent fires when a device changes state (entry_on, absent_off, auto_on, auto_off)
func SendDeviceEvent(webhookURL string, esp32ID, event, device, reason string, temp, lux float64) {
	colorMap := map[string]int{
		"entry_on":   ColorGreen,
		"absent_off": ColorRed,
		"auto_on":    ColorCyan,
		"auto_off":   ColorAmber,
	}
	emojiMap := map[string]string{
		"fan": "🌀", "light": "💡", "ac": "❄️", "tv": "📽️",
	}
	color := ColorCyan
	if c, ok := colorMap[event]; ok {
		color = c
	}
	devEmoji := emojiMap[device]

	action := "ON"
	if event == "absent_off" || event == "auto_off" {
		action = "OFF"
	}

	send(webhookURL, webhookPayload{
		Username: "SmartRoom 🏫",
		Embeds: []Embed{{
			Title:       fmt.Sprintf("%s %s → **%s**", devEmoji, device, action),
			Description: fmt.Sprintf("Event: `%s`  Reason: `%s`", event, reason),
			Color:       color,
			Fields: []EmbedField{
				{Name: "📡 ESP32", Value: fmt.Sprintf("`%s`", esp32ID), Inline: true},
				{Name: "🌡️ Temp", Value: fmt.Sprintf("%.1f°C", temp), Inline: true},
				{Name: "☀️ Lux", Value: fmt.Sprintf("%.0f lx", lux), Inline: true},
			},
			Footer:    &EmbedFooter{Text: "Smart Classroom IoT — Events"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}},
	})
}

// SendHourlySummary fires every hour with energy breakdown.
func SendHourlySummary(webhookURL string, esp32ID string, kwFan, kwLight, kwAC, kwTV, kwTotal float64, motions int, costPerKWh float64) {
	totalCost := kwTotal * costPerKWh

	send(webhookURL, webhookPayload{
		Username: "SmartRoom 🏫",
		Embeds: []Embed{{
			Title:       "🕐 Hourly Energy Summary",
			Description: fmt.Sprintf("**%s** — Energy report for the past hour", esp32ID),
			Color:       ColorTeal,
			Fields: []EmbedField{
				{Name: "🌀 Fan",   Value: fmt.Sprintf("%.4f kWh", kwFan),   Inline: true},
				{Name: "💡 Light", Value: fmt.Sprintf("%.4f kWh", kwLight), Inline: true},
				{Name: "❄️ AC",    Value: fmt.Sprintf("%.4f kWh", kwAC),    Inline: true},
				{Name: "📽️ TV*",   Value: fmt.Sprintf("%.4f kWh", kwTV),    Inline: true},
				{Name: "⚡ Total", Value: fmt.Sprintf("**%.4f kWh**", kwTotal), Inline: true},
				{Name: "💰 Cost",  Value: fmt.Sprintf("**฿%.2f**", totalCost), Inline: true},
				{Name: "🚶 Motions", Value: fmt.Sprintf("%d detections", motions), Inline: true},
			},
			Footer:    &EmbedFooter{Text: "Smart Classroom IoT — Hourly Report"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}},
	})
}

// SendHighPowerAlert fires when wattage exceeds a threshold.
func SendHighPowerAlert(webhookURL, esp32ID string, wattTotal float64, threshold float64) {
	send(webhookURL, webhookPayload{
		Username: "SmartRoom 🏫",
		Embeds: []Embed{{
			Title:       "⚡ High Power Consumption Alert",
			Description: fmt.Sprintf("Total power **%.1f W** exceeded threshold of **%.0f W**", wattTotal, threshold),
			Color:       ColorRed,
			Fields: []EmbedField{
				{Name: "📡 Device", Value: fmt.Sprintf("`%s`", esp32ID), Inline: true},
				{Name: "⚡ Watts",  Value: fmt.Sprintf("%.1f W", wattTotal), Inline: true},
			},
			Footer:    &EmbedFooter{Text: "Smart Classroom IoT — Power Alert"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}},
	})
}

// SendDailyBillingSummary fires at midnight with daily cost.
func SendDailyBillingSummary(webhookURL, userID string, totalKWh, totalCost float64, month, year int) {
	send(webhookURL, webhookPayload{
		Username: "SmartRoom 🏫",
		Embeds: []Embed{{
			Title:       fmt.Sprintf("🧾 Monthly Bill — %02d/%d", month, year),
			Description: "Monthly energy billing summary",
			Color:       ColorAmber,
			Fields: []EmbedField{
				{Name: "⚡ Total kWh", Value: fmt.Sprintf("%.2f kWh", totalKWh), Inline: true},
				{Name: "💰 Total Cost", Value: fmt.Sprintf("**฿%.2f**", totalCost), Inline: true},
			},
			Footer:    &EmbedFooter{Text: "Smart Classroom IoT — Billing"},
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}},
	})
}
package handlers

import (
	"log"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/classroom/go-example/internal/db"
	"github.com/classroom/go-example/internal/models"
	"github.com/classroom/go-example/internal/mqttc"
	"github.com/classroom/go-example/internal/services"
)

// GET /api/devices?esp32_id=...
func GetDevices(c fiber.Ctx) error {
	esp32ID := c.Query("esp32_id", "esp32-classroom-01")

	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
	}

	var ownerCount int
	if err := db.DB.Db.QueryRow(
		`SELECT COUNT(*) FROM esp32_devices WHERE esp32_id=$1 AND user_id=$2`,
		esp32ID, userID,
	).Scan(&ownerCount); err != nil || ownerCount == 0 {
		return c.Status(403).JSON(fiber.Map{"error": "forbidden"})
	}

	var devs []models.Device
	if _, err := db.DB.Select(&devs,
		`SELECT * FROM devices WHERE esp32_id=$1 ORDER BY name`, esp32ID,
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(devs)
}

// GET /api/devices/:id
func GetDevice(c fiber.Ctx) error {
	id := c.Params("id")
	var dev models.Device
	if err := db.DB.SelectOne(&dev, `SELECT * FROM devices WHERE id=$1`, id); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "not found"})
	}
	return c.JSON(dev)
}

// POST /api/devices/:id/control
//
// Body: {"command":"on"|"off"|"set_mode", "device":"fan"|"light"|"ac"|"tv", "mode":"auto"|"manual"}
//
// Session check rules (matching firmware v11-FINAL):
//   - "off"      → ผ่านเสมอ
//   - "set_mode" → ผ่านเสมอ
//   - "on" + tv  → ผ่านเสมอ [projector อิสระจาก session]
//   - "on" + fan/light/ac + mode=="manual" → ผ่านเสมอ [FIX-MANUAL-2]
//     MANUAL mode ไม่ต้องการ session — ผู้ใช้สั่งได้เลยทันที
//   - "on" + fan/light/ac + mode=="auto"   → ต้องมี session active
func ControlDevice(c fiber.Ctx) error {
	id := c.Params("id")

	var dev models.Device
	if err := db.DB.SelectOne(&dev, `SELECT * FROM devices WHERE id=$1`, id); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "device not found"})
	}

	var req models.ControlPayload
	if err := c.Bind().JSON(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid request"})
	}

	switch req.Command {
	case "on", "off", "set_mode":
	default:
		return c.Status(400).JSON(fiber.Map{"error": "unknown command: " + req.Command})
	}

	// firmware ต้องการ field "device" ใน JSON เสมอ
	if req.Device == "" {
		req.Device = dev.Type
	}

	if req.Command == "set_mode" {
		if req.Mode != "auto" && req.Mode != "manual" {
			return c.Status(400).JSON(fiber.Map{"error": "mode must be 'auto' or 'manual'"})
		}
	}

	// ── Session check ─────────────────────────────────────────────────────
	//
	// [FIX-MANUAL-2] MANUAL mode bypasses session check entirely.
	// In MANUAL mode the user can turn devices on/off freely without IR motion.
	// Session check only applies when:
	//   1. command is "on"
	//   2. device is fan / light / ac  (not tv — tv is always free)
	//   3. the device's current mode is "auto"  (not "manual")
	//
	// When the frontend sends set_mode→manual first, the DB mode becomes "manual"
	// so subsequent "on" commands bypass the check.
	// When mode is unknown / empty we also bypass (safe default: let firmware decide).
	isAutoModeDevice := dev.Mode == "auto"
	needsSessionCheck := req.Command == "on" &&
		dev.Type != "tv" &&
		isAutoModeDevice

	if needsSessionCheck {
		sessionInfo := checkSessionActive(dev.ESP32ID)
		if !sessionInfo.active {
			log.Printf("[control] ON rejected — no active session esp32=%s type=%s mode=%s",
				dev.ESP32ID, dev.Type, dev.Mode)
			return c.Status(400).JSON(fiber.Map{
				"error":       "cannot turn on — no active IR session",
				"hint":        "Either wait for IR to detect motion (AUTO), or switch device to Manual mode first",
				"esp32_id":    dev.ESP32ID,
				"device_type": dev.Type,
				"device_mode": dev.Mode,
			})
		}
		log.Printf("[control] session active via method=%s esp32=%s type=%s",
			sessionInfo.method, dev.ESP32ID, dev.Type)
	} else {
		log.Printf("[control] session check skipped — cmd=%s type=%s mode=%s",
			req.Command, dev.Type, dev.Mode)
	}

	// ── Update device state ────────────────────────────────────────────────
	switch req.Command {
	case "on":
		dev.IsActive = true
		// When turned on manually, keep current mode or default to manual
		if req.Mode != "" {
			dev.Mode = req.Mode
		} else {
			dev.Mode = "manual"
		}
	case "off":
		dev.IsActive = false
		if req.Mode != "" {
			dev.Mode = req.Mode
		} else {
			dev.Mode = "manual"
		}
	case "set_mode":
		dev.Mode = req.Mode
		// is_active ไม่เปลี่ยนเมื่อเปลี่ยน mode
	}

	if _, err := db.DB.Exec(
		`UPDATE devices SET is_active=$1, mode=$2 WHERE id=$3`,
		dev.IsActive, dev.Mode, dev.ID,
	); err != nil {
		log.Printf("[control] DB update failed device=%s: %v", dev.ID, err)
		return c.Status(500).JSON(fiber.Map{"error": "db update failed"})
	}
	services.InvalidateDeviceCache(dev.ESP32ID)

	// ── MQTT publish → ESP32 ──────────────────────────────────────────────
	mqttOK := false
	mqttWarning := ""

	mq := mqttc.GetMQ()
	switch {
	case mq == nil:
		mqttWarning = "MQTT client not initialized"
		log.Printf("[control] MQTT nil esp32=%s", dev.ESP32ID)
	case !mq.IsConnected():
		mqttWarning = "MQTT broker not connected — DB saved only"
		log.Printf("[control] MQTT disconnected esp32=%s", dev.ESP32ID)
	default:
		if err := mq.Publish(dev.ESP32ID, req); err != nil {
			mqttWarning = "MQTT publish failed: " + err.Error()
			log.Printf("[control] MQTT publish failed esp32=%s: %v", dev.ESP32ID, err)
		} else {
			mqttOK = true
			log.Printf("[control] MQTT OK esp32=%s cmd=%s device=%s mode=%s",
				dev.ESP32ID, req.Command, req.Device, req.Mode)
		}
	}

	resp := fiber.Map{
		"ok":           true,
		"device_id":    dev.ID,
		"esp32_id":     dev.ESP32ID,
		"device_type":  dev.Type,
		"device":       req.Device,
		"is_active":    dev.IsActive,
		"mode":         dev.Mode,
		"mqtt_publish": mqttOK,
		"command":      req.Command,
	}
	if mqttWarning != "" {
		resp["warning"] = mqttWarning
	}
	return c.JSON(resp)
}

// sessionCheckResult holds session check result with diagnostic info
type sessionCheckResult struct {
	active bool
	method string // "device_sessions" | "ir_state_realtime" | "ir_state_recent" | "none"
}

// checkSessionActive — คืน true ถ้า esp32 นั้นมี session active
//
// ใช้ 3 วิธีควบคู่กัน เรียงตาม reliability:
//
//  1. device_sessions: stopped_at IS NULL
//
//  2. ir_readings.ir_state = true ภายใน 30 วินาที (realtime)
//
//  3. ir_readings.ir_state = true ภายใน 120 วินาที (safety net)
func checkSessionActive(esp32ID string) sessionCheckResult {
	// method 1 — device_sessions
	var sessCount int
	if err := db.DB.Db.QueryRow(`
		SELECT COUNT(*)
		FROM device_sessions ds
		JOIN devices d ON d.id = ds.device_id
		WHERE d.esp32_id = $1 AND ds.stopped_at IS NULL
	`, esp32ID).Scan(&sessCount); err == nil && sessCount > 0 {
		log.Printf("[session-check] esp32=%s active via device_sessions (count=%d)", esp32ID, sessCount)
		return sessionCheckResult{active: true, method: "device_sessions"}
	}

	// method 2 — ir_state realtime (within 30s)
	var irActive bool
	if err := db.DB.Db.QueryRow(`
		SELECT ir_state
		FROM ir_readings ir
		JOIN devices d ON d.id = ir.device_id
		WHERE d.esp32_id = $1
		  AND ir.ir_state = true
		  AND ir.recorded_at >= NOW() - INTERVAL '30 seconds'
		ORDER BY ir.recorded_at DESC
		LIMIT 1
	`, esp32ID).Scan(&irActive); err == nil && irActive {
		log.Printf("[session-check] esp32=%s active via ir_state realtime", esp32ID)
		return sessionCheckResult{active: true, method: "ir_state_realtime"}
	}

	// method 3 — ir_state recent (safety net, wider window)
	if err := db.DB.Db.QueryRow(`
		SELECT ir_state
		FROM ir_readings ir
		JOIN devices d ON d.id = ir.device_id
		WHERE d.esp32_id = $1
		  AND ir.ir_state = true
		  AND ir.recorded_at >= NOW() - INTERVAL '120 seconds'
		ORDER BY ir.recorded_at DESC
		LIMIT 1
	`, esp32ID).Scan(&irActive); err == nil && irActive {
		log.Printf("[session-check] esp32=%s active via ir_state recent (120s window)", esp32ID)
		return sessionCheckResult{active: true, method: "ir_state_recent"}
	}

	log.Printf("[session-check] esp32=%s NO active session", esp32ID)
	return sessionCheckResult{active: false, method: "none"}
}

// GET /api/devices/:id/sessions?limit=10
func DeviceSessions(c fiber.Ctx) error {
	devID := c.Params("id")
	limit := queryInt(c, "limit", 10)

	var sessions []models.DeviceSession
	if _, err := db.DB.Select(&sessions,
		`SELECT * FROM device_sessions WHERE device_id=$1 ORDER BY started_at DESC LIMIT $2`,
		devID, limit,
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(sessions)
}

// GET /api/devices/:id/status
func DeviceStatus(c fiber.Ctx) error {
	id := c.Params("id")
	var dev models.Device
	if err := db.DB.SelectOne(&dev, `SELECT * FROM devices WHERE id=$1`, id); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "not found"})
	}

	var pwr models.PowerReading
	hasPower := false
	if err := db.DB.SelectOne(&pwr,
		`SELECT * FROM power_readings WHERE device_id=$1 ORDER BY recorded_at DESC LIMIT 1`,
		dev.ID); err == nil {
		hasPower = true
	}

	resp := fiber.Map{
		"device":    dev,
		"is_active": dev.IsActive,
		"mode":      dev.Mode,
	}
	if hasPower {
		resp["watt_total"] = pwr.WattTotal
		resp["watt_measured"] = pwr.WattMeasured
		// ACS712 negative line: current = currentSim from firmware
		resp["current_sim_a"] = pwr.Current
		resp["kwh_total"] = pwr.KWhTotal
		resp["power_recorded_at"] = pwr.RecordedAt
	}
	return c.JSON(resp)
}

// POST /api/devices/:id/force-off — emergency off, no session check
func ForceOffDevice(c fiber.Ctx) error {
	id := c.Params("id")
	var dev models.Device
	if err := db.DB.SelectOne(&dev, `SELECT * FROM devices WHERE id=$1`, id); err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "device not found"})
	}

	req := models.ControlPayload{
		Command: "off",
		Device:  dev.Type,
		Mode:    "manual",
	}

	if _, err := db.DB.Exec(
		`UPDATE devices SET is_active=false, mode='manual' WHERE id=$1`, dev.ID,
	); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "db update failed"})
	}
	services.InvalidateDeviceCache(dev.ESP32ID)

	mqttOK := false
	if mq := mqttc.GetMQ(); mq != nil && mq.IsConnected() {
		if err := mq.Publish(dev.ESP32ID, req); err == nil {
			mqttOK = true
		}
	}

	return c.JSON(fiber.Map{
		"ok":           true,
		"device_id":    dev.ID,
		"device_type":  dev.Type,
		"is_active":    false,
		"mqtt_publish": mqttOK,
		"command":      "force_off",
	})
}

// GET /api/devices/esp32/:esp32_id/session-status
func SessionStatus(c fiber.Ctx) error {
	esp32ID := c.Params("esp32_id")
	if esp32ID == "" {
		esp32ID = c.Query("esp32_id", "esp32-classroom-01")
	}

	result := checkSessionActive(esp32ID)

	type ActiveSession struct {
		DeviceType string     `db:"device_type" json:"device_type"`
		StartedAt  time.Time  `db:"started_at" json:"started_at"`
		StoppedAt  *time.Time `db:"stopped_at" json:"stopped_at"`
	}
	var sessions []ActiveSession

	var irState bool
	var irAge float64 = -1
	var irRecordedAt time.Time
	if err := db.DB.Db.QueryRow(`
		SELECT ir_state, recorded_at
		FROM ir_readings ir JOIN devices d ON d.id = ir.device_id
		WHERE d.esp32_id = $1
		ORDER BY ir.recorded_at DESC LIMIT 1
	`, esp32ID).Scan(&irState, &irRecordedAt); err == nil {
		irAge = time.Since(irRecordedAt).Seconds()
	}

	return c.JSON(fiber.Map{
		"esp32_id":         esp32ID,
		"session_active":   result.active,
		"detection_method": result.method,
		"ir_state":         irState,
		"ir_state_age_sec": irAge,
		"active_sessions":  sessions,
	})
}

var _ = services.InvalidateDeviceCache
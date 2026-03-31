package handlers

import (
	"math"
	"time"

	"github.com/gofiber/fiber/v3"

	"github.com/classroom/go-example/internal/db"
	"github.com/classroom/go-example/internal/models"
	"github.com/classroom/go-example/internal/services"
)

// [FIX-IR-4] sessionDurationSec ต้องตรงกับ firmware SESSION_DURATION = 1800000ms = 30 นาที
// เดิมเป็น 3600 (1 ชั่วโมง) ซึ่งผิด
const sessionDurationSec = 1800

// GET /api/dashboard/latest?esp32_id=esp32-classroom-01
func DashboardLatest(c fiber.Ctx) error {
	esp32ID := c.Query("esp32_id", "esp32-classroom-01")

	devMap, err := getDeviceMap(esp32ID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "device not found"})
	}

	result := fiber.Map{}

	// ── DHT22 (fan device) ────────────────────────────────────────────
	if devID, ok := devMap["fan"]; ok {
		var dht models.DHT22Reading
		if e := db.DB.SelectOne(&dht,
			`SELECT * FROM dht22_readings WHERE device_id=$1 ORDER BY recorded_at DESC LIMIT 1`, devID); e == nil {
			result["dht22"] = dht
		} else {
			result["dht22"] = nil
		}
	}

	// ── Power (fan device) ────────────────────────────────────────────
	if devID, ok := devMap["fan"]; ok {
		var pwr models.PowerReading
		if e := db.DB.SelectOne(&pwr,
			`SELECT * FROM power_readings WHERE device_id=$1 ORDER BY recorded_at DESC LIMIT 1`, devID); e == nil {
			result["power"] = pwr
		} else {
			result["power"] = nil
		}
	}

	// ── LDR (light device) ────────────────────────────────────────────
	if devID, ok := devMap["light"]; ok {
		var ldr models.LDRReading
		if e := db.DB.SelectOne(&ldr,
			`SELECT * FROM ldr_readings WHERE device_id=$1 ORDER BY recorded_at DESC LIMIT 1`, devID); e == nil {
			result["ldr"] = ldr
		} else {
			result["ldr"] = nil
		}
	} else {
		result["ldr"] = nil
	}

	// ── IR — current hour window, fallback ประวัติล่าสุด ──────────────
	if devID, ok := devMap["fan"]; ok {
		currentHour := time.Now().Truncate(time.Hour)
		var ir models.IRReading
		err := db.DB.SelectOne(&ir,
			`SELECT * FROM ir_readings WHERE device_id=$1 AND hour_window=$2 LIMIT 1`,
			devID, currentHour)
		if err != nil {
			err2 := db.DB.SelectOne(&ir,
				`SELECT * FROM ir_readings WHERE device_id=$1 ORDER BY hour_window DESC LIMIT 1`,
				devID)
			if err2 != nil {
				result["ir"] = nil
			} else {
				result["ir"] = ir
			}
		} else {
			result["ir"] = ir
		}
	} else {
		result["ir"] = nil
	}

	// ── [FIX-IR-5] IR active state (realtime) ─────────────────────────
	// เดิม: query จาก ir_events ซึ่งไม่มีใน schema → ir_active เป็น false เสมอ
	// แก้:  query จาก ir_readings โดยตรง ใช้ ir_state column
	//       ir_state = true  หมายความว่า firmware รายงานว่ามีคนอยู่ ณ ขณะนั้น
	//       ตรวจ recorded_at ด้วยว่าข้อมูลสดพอ (ไม่เกิน IR_ACTIVE_WINDOW_SEC วินาที)
	//       ถ้าข้อมูลเก่าเกินไปให้ถือว่า ir_active = false (sensor อาจ offline)
	const irActiveWindowSec = 30 // ถ้า ESP32 ไม่ส่งข้อมูลเกิน 30 วินาที ถือว่า stale
	if devID, ok := devMap["fan"]; ok {
		currentHour := time.Now().Truncate(time.Hour)
		var ir models.IRReading
		err := db.DB.SelectOne(&ir,
			`SELECT * FROM ir_readings WHERE device_id=$1 AND hour_window=$2 LIMIT 1`,
			devID, currentHour)

		irActive := false
		if err == nil {
			// ir_state = true AND ข้อมูลสดพอ
			dataAge := time.Since(ir.RecordedAt).Seconds()
			irActive = ir.IRState && dataAge <= float64(irActiveWindowSec)
			if !ir.IRState {
				// ir_state = false แปลว่า firmware บอกว่าไม่มีคน — ไม่ต้องตรวจอายุ
				irActive = false
			}
		}
		result["ir_active"] = irActive
		result["ir_state_age_sec"] = func() float64 {
			if err != nil {
				return -1
			}
			return time.Since(ir.RecordedAt).Seconds()
		}()
	} else {
		result["ir_active"] = false
		result["ir_state_age_sec"] = float64(-1)
	}

	// ── Devices ───────────────────────────────────────────────────────
	var devs []models.Device
	if _, err := db.DB.Select(&devs, `SELECT * FROM devices WHERE esp32_id=$1 ORDER BY name`, esp32ID); err == nil {
		result["devices"] = devs
	} else {
		result["devices"] = []models.Device{}
	}

	// ── Active sessions ───────────────────────────────────────────────
	var sessions []struct {
		models.DeviceSession
		DeviceName string `db:"device_name" json:"device_name"`
		DeviceType string `db:"device_type" json:"device_type"`
		DeviceMode string `db:"device_mode" json:"device_mode"`
	}
	if _, err := db.DB.Select(&sessions, `
		SELECT ds.*, d.name as device_name, d.type as device_type, d.mode as device_mode
		FROM device_sessions ds
		JOIN devices d ON d.id = ds.device_id
		WHERE d.esp32_id=$1 AND ds.stopped_at IS NULL
		ORDER BY ds.started_at DESC
	`, esp32ID); err == nil {
		result["active_sessions"] = sessions
	} else {
		result["active_sessions"] = []interface{}{}
		sessions = nil
	}

	// ── [FIX-IR-4] Session remaining time (server-side) ───────────────
	// คำนวณจาก SESSION_DURATION = 1800 วินาที (30 นาที) ตรงกับ firmware
	// เดิม sessionDurationSec = 3600 ทำให้ remaining time ผิดเพี้ยน 2 เท่า
	remainingSec := 0
	if len(sessions) > 0 {
		elapsed := time.Since(sessions[0].StartedAt).Seconds()
		remaining := math.Max(0, float64(sessionDurationSec)-elapsed)
		remainingSec = int(remaining)
	}
	result["session_remaining_sec"] = remainingSec

	return c.JSON(result)
}

// GET /api/dashboard/power-chart?esp32_id=...&hours=24
func PowerChart(c fiber.Ctx) error {
	esp32ID := c.Query("esp32_id", "esp32-classroom-01")
	hours := queryInt(c, "hours", 24)

	devMap, err := getDeviceMap(esp32ID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "device not found"})
	}
	devID, ok := devMap["fan"]
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "fan device not found"})
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var rows []models.PowerReading
	if _, err := db.DB.Select(&rows,
		`SELECT * FROM power_readings WHERE device_id=$1 AND recorded_at >= $2 ORDER BY recorded_at ASC`,
		devID, since); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if rows == nil {
		rows = []models.PowerReading{}
	}
	return c.JSON(rows)
}

// GET /api/dashboard/dht-chart?esp32_id=...&days=7
func DHTChart(c fiber.Ctx) error {
	esp32ID := c.Query("esp32_id", "esp32-classroom-01")
	days := queryInt(c, "days", 7)

	devMap, err := getDeviceMap(esp32ID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "device not found"})
	}
	devID, ok := devMap["fan"]
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "fan device not found"})
	}

	since := time.Now().AddDate(0, 0, -days)
	var rows []models.DHT22Reading
	if _, err := db.DB.Select(&rows,
		`SELECT * FROM dht22_readings WHERE device_id=$1 AND recorded_at >= $2 ORDER BY recorded_at ASC`,
		devID, since); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if rows == nil {
		rows = []models.DHT22Reading{}
	}
	return c.JSON(rows)
}

// GET /api/dashboard/ldr-chart?esp32_id=...&hours=12
func LDRChart(c fiber.Ctx) error {
	esp32ID := c.Query("esp32_id", "esp32-classroom-01")
	hours := queryInt(c, "hours", 12)

	devMap, err := getDeviceMap(esp32ID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "device not found"})
	}
	devID, ok := devMap["light"]
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "light device not found"})
	}

	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	var rows []models.LDRReading
	if _, err := db.DB.Select(&rows,
		`SELECT * FROM ldr_readings WHERE device_id=$1 AND recorded_at >= $2 ORDER BY recorded_at ASC`,
		devID, since); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if rows == nil {
		rows = []models.LDRReading{}
	}
	return c.JSON(rows)
}

// GET /api/dashboard/sessions?esp32_id=...&limit=20
func Sessions(c fiber.Ctx) error {
	esp32ID := c.Query("esp32_id", "esp32-classroom-01")
	limit := queryInt(c, "limit", 20)

	type SessionRow struct {
		models.DeviceSession
		DeviceName string `db:"device_name" json:"device_name"`
		DeviceType string `db:"device_type" json:"device_type"`
		DeviceMode string `db:"device_mode" json:"device_mode"`
	}

	var rows []SessionRow
	if _, err := db.DB.Select(&rows, `
		SELECT ds.*, d.name as device_name, d.type as device_type, d.mode as device_mode
		FROM device_sessions ds
		JOIN devices d ON d.id = ds.device_id
		WHERE d.esp32_id = $1
		ORDER BY ds.started_at DESC
		LIMIT $2
	`, esp32ID, limit); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if rows == nil {
		rows = []SessionRow{}
	}
	return c.JSON(rows)
}

// GET /api/dashboard/billing
func Billing(c fiber.Ctx) error {
	userID, ok := c.Locals("user_id").(string)
	if !ok || userID == "" {
		return c.Status(401).JSON(fiber.Map{"error": "unauthorized"})
	}

	var rows []models.MonthlyBilling
	if _, err := db.DB.Select(&rows,
		`SELECT * FROM monthly_billing WHERE user_id=$1 ORDER BY year DESC, month DESC LIMIT 12`,
		userID); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if rows == nil {
		rows = []models.MonthlyBilling{}
	}
	return c.JSON(rows)
}

// GET /api/dashboard/ir-hourly?esp32_id=...
func IRHourly(c fiber.Ctx) error {
	esp32ID := c.Query("esp32_id", "esp32-classroom-01")

	devMap, err := getDeviceMap(esp32ID)
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "device not found"})
	}
	devID, ok := devMap["fan"]
	if !ok {
		return c.Status(404).JSON(fiber.Map{"error": "fan device not found"})
	}

	since := time.Now().Add(-24 * time.Hour)
	var rows []models.IRReading
	if _, err := db.DB.Select(&rows,
		`SELECT * FROM ir_readings WHERE device_id=$1 AND hour_window >= $2 ORDER BY hour_window ASC`,
		devID, since); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if rows == nil {
		rows = []models.IRReading{}
	}
	return c.JSON(rows)
}

var _ = services.InvalidateDeviceCache
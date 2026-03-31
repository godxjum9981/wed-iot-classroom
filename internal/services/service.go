package services

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/classroom/go-example/internal/config"
	"github.com/classroom/go-example/internal/db"
	"github.com/classroom/go-example/internal/models"
	"github.com/classroom/go-example/pkg/discord"
)

// ── Errors ────────────────────────────────────────────────────────────────
var ErrDeviceNotFound = errors.New("device not found")

// ── Device cache ──────────────────────────────────────────────────────────
var (
	deviceCacheMu sync.Mutex
	deviceCache   = make(map[string]map[string]string)
)

func InvalidateDeviceCache(esp32ID string) {
	deviceCacheMu.Lock()
	defer deviceCacheMu.Unlock()
	delete(deviceCache, esp32ID)
	log.Printf("[cache] invalidated device cache for %s", esp32ID)
}

func GetDeviceMap(esp32ID string) (map[string]string, error) {
	deviceCacheMu.Lock()
	if m, ok := deviceCache[esp32ID]; ok {
		deviceCacheMu.Unlock()
		return m, nil
	}
	deviceCacheMu.Unlock()

	var devs []models.Device
	if _, err := db.DB.Select(&devs, `SELECT * FROM devices WHERE esp32_id=$1`, esp32ID); err != nil {
		return nil, err
	}
	if len(devs) == 0 {
		return nil, ErrDeviceNotFound
	}

	m := make(map[string]string, len(devs))
	for _, d := range devs {
		m[d.Type] = d.ID
	}

	deviceCacheMu.Lock()
	deviceCache[esp32ID] = m
	deviceCacheMu.Unlock()

	return m, nil
}

// ── ProcessSensor ─────────────────────────────────────────────────────────
func ProcessSensor(p models.SensorPayload) error {
	devMap, err := GetDeviceMap(p.DeviceID)
	if err != nil {
		return err
	}
	if len(devMap) == 0 {
		return ErrDeviceNotFound
	}

	now := time.Now()

	// ── DHT22 ─────────────────────────────────────────────────────────────
	if p.Temperature > 0 || p.Humidity > 0 {
		if dhtDevID := firstDevID(devMap, "fan", "ac", "light", "tv"); dhtDevID != "" {
			if err := db.DB.Insert(&models.DHT22Reading{
				ID:          uuid.New().String(),
				DeviceID:    dhtDevID,
				Temperature: p.Temperature,
				Humidity:    p.Humidity,
				RecordedAt:  now,
			}); err != nil {
				log.Printf("[sensor] DHT22 insert error: %v", err)
			}
		}
	}

	// ── Power ──────────────────────────────────────────────────────────────
	// ACS712 ต่อบน negative line: firmware คำนวณ diff = offset - voltage แล้ว
	// currentSim = currentRaw * ACS712_SIM_MULT, wattMeasured = currentSim * 220V
	// backend รับค่าที่คำนวณแล้ว — ไม่ต้องแปลงเพิ่ม แค่เก็บตรงๆ
	if powerDevID := firstDevID(devMap, "fan", "ac", "light", "tv"); powerDevID != "" {
		if err := db.DB.Insert(&models.PowerReading{
			ID:           uuid.New().String(),
			DeviceID:     powerDevID,
			Voltage:      p.Voltage,
			Current:      p.Current,   // currentSim (already × ACS712_SIM_MULT from firmware)
			Watt:         p.Watt,
			WattMeasured: p.WattMeasured,
			WattTVSim:    p.WattTVSim,
			WattTotal:    p.WattTotal,
			KWhTotal:     p.KWhTotal,
			KWhFan:       p.KWhFan,
			KWhLight:     p.KWhLight,
			KWhAC:        p.KWhAC,
			KWhTV:        p.KWhTV,
			KWhHourFan:   p.KWhHourFan,
			KWhHourLight: p.KWhHourLight,
			KWhHourAC:    p.KWhHourAC,
			KWhHourTV:    p.KWhHourTV,
			RecordedAt:   now,
		}); err != nil {
			log.Printf("[sensor] Power insert error: %v", err)
		}
	}

	// ── LDR ───────────────────────────────────────────────────────────────
	if devID, ok := devMap["light"]; ok {
		if err := db.DB.Insert(&models.LDRReading{
			ID:         uuid.New().String(),
			DeviceID:   devID,
			LuxValue:   p.LuxValue,
			RecordedAt: now,
		}); err != nil {
			log.Printf("[sensor] LDR insert error: %v", err)
		}
	}

	// ── IR upsert ─────────────────────────────────────────────────────────
	if irDevID := firstDevID(devMap, "fan", "ac", "light", "tv"); irDevID != "" {
		hourWindow := now.Truncate(time.Hour)
		if _, err := db.DB.Db.Exec(`
			INSERT INTO ir_readings (id, device_id, motion_count, ir_state, hour_window, recorded_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (device_id, hour_window)
			DO UPDATE SET
				motion_count = EXCLUDED.motion_count,
				ir_state     = EXCLUDED.ir_state,
				recorded_at  = EXCLUDED.recorded_at
		`, uuid.New().String(), irDevID, p.MotionCount, p.IRState, hourWindow, now); err != nil {
			log.Printf("[sensor] IR upsert error: %v", err)
		}
	}

	// ── Sync relay status from ESP32 (source of truth) ───────────────────
	autoMode := func(a bool) string {
		if a {
			return "auto"
		}
		return "manual"
	}
	type devStatus struct {
		active bool
		mode   string
	}
	statusMap := map[string]devStatus{
		"fan":   {p.RelayFan, autoMode(p.AutoFan)},
		"light": {p.RelayLight, autoMode(p.AutoLight)},
		"ac":    {p.RelayAC, autoMode(p.AutoAC)},
		"tv":    {p.TVOn, "manual"},
	}
	for devType, s := range statusMap {
		if devID, ok := devMap[devType]; ok {
			if _, err := db.DB.Exec(
				`UPDATE devices SET is_active=$1, mode=$2 WHERE id=$3`,
				s.active, s.mode, devID,
			); err != nil {
				log.Printf("[sensor] device status update error (%s): %v", devType, err)
			}
		}
	}
	InvalidateDeviceCache(p.DeviceID)

	// ── Discord alerts ────────────────────────────────────────────────────
	total := p.WattTotal
	if total <= 0 {
		total = p.Watt + p.WattTVSim
	}
	if total > 2000 {
		go discord.SendHighPowerAlert(config.C.DiscordWebhookAlert, p.DeviceID, total, 2000)
	}
	if p.Temperature > 35 {
		go discord.SendSensorAlert(config.C.DiscordWebhookAlert, p.DeviceID, "temp_critical", p.Temperature, 35)
	}

	return nil
}

// firstDevID returns the first device ID matching any priority key
func firstDevID(devMap map[string]string, priority ...string) string {
	for _, k := range priority {
		if id, ok := devMap[k]; ok {
			return id
		}
	}
	return ""
}

// ── ProcessEvent ──────────────────────────────────────────────────────────
//
// Event flow from firmware v11-FINAL:
//
//	IR trigger → "session_start" (device="system")
//	Auto logic → "auto_on" / "auto_off"     (device="fan"|"light"|"ac")
//	Manual cmd → "manual_on" / "manual_off" (device="fan"|"light"|"ac"|"tv")
//	Relay off  → "relay_off" (with kwh/cost) — billing source of truth
//	Session end → "session_end" (device="system")
//
// [FIX-SESSION-1] auto_off / manual_off NO LONGER close device_sessions.
//
//	Session lifetime is managed ONLY by session_start / session_end.
//	Individual device on/off events just sync is_active in the devices table.
//	This prevents the 1-second session bug where auto_off (fired immediately
//	when temp < threshold right after session_start) was killing the session.
func ProcessEvent(p models.EventPayload) error {
	devMap, _ := GetDeviceMap(p.DeviceID)

	// ── IR events ─────────────────────────────────────────────────────────
	if p.Event == "ir_motion" || p.Event == "ir_idle" {
		if devID := firstDevID(devMap, "fan", "ac", "light", "tv"); devID != "" {
			hourWindow := time.Now().Truncate(time.Hour)
			irStateNow := (p.Event == "ir_motion")
			if _, err := db.DB.Db.Exec(`
				INSERT INTO ir_readings (id, device_id, motion_count, ir_state, hour_window, recorded_at)
				VALUES ($1, $2, $3, $4, $5, NOW())
				ON CONFLICT (device_id, hour_window)
				DO UPDATE SET
					motion_count = EXCLUDED.motion_count,
					ir_state     = EXCLUDED.ir_state,
					recorded_at  = NOW()
			`, uuid.New().String(), devID, p.MotionCount, irStateNow, hourWindow); err != nil {
				log.Printf("[event] IR upsert error: %v", err)
			}
		}
		go discord.SendDeviceEvent(
			config.C.DiscordWebhookEvents,
			p.DeviceID, p.Event, p.Device, p.Reason, p.Temp, p.Lux,
		)
		return nil
	}

	// ── session_start / session_on ────────────────────────────────────────
	if p.Event == "session_start" || p.Event == "session_on" {
		now := time.Now()

		// Upsert ir_state = true ทันที (realtime signal สำหรับ checkSessionActive)
		if devID := firstDevID(devMap, "fan", "ac", "light", "tv"); devID != "" {
			hourWindow := now.Truncate(time.Hour)
			if _, err := db.DB.Db.Exec(`
				INSERT INTO ir_readings (id, device_id, motion_count, ir_state, hour_window, recorded_at)
				VALUES ($1, $2, $3, true, $4, NOW())
				ON CONFLICT (device_id, hour_window)
				DO UPDATE SET ir_state = true, motion_count = EXCLUDED.motion_count, recorded_at = NOW()
			`, uuid.New().String(), devID, p.MotionCount, hourWindow); err != nil {
				log.Printf("[event] IR state upsert on session_start error: %v", err)
			}
		}

		inserted := 0
		for _, devType := range []string{"fan", "light", "ac"} {
			devID, ok := devMap[devType]
			if !ok {
				continue
			}
			// ปิด session เก่าที่ยังค้างอยู่ก่อน (ป้องกัน duplicate open sessions)
			if _, err := db.DB.Exec(
				`UPDATE device_sessions SET stopped_at=$1 WHERE device_id=$2 AND stopped_at IS NULL`,
				now, devID,
			); err != nil {
				log.Printf("[event] session_start close stale sessions (%s): %v", devType, err)
			}
			if err := db.DB.Insert(&models.DeviceSession{
				ID:        uuid.New().String(),
				DeviceID:  devID,
				StartedAt: now,
			}); err != nil {
				log.Printf("[event] session_start DeviceSession insert (%s): %v", devType, err)
			} else {
				inserted++
				log.Printf("[event] session_start DeviceSession created — type=%s device_id=%s", devType, devID)
			}
		}
		log.Printf("[event] session_start done — inserted %d rows for esp32=%s", inserted, p.DeviceID)

		go discord.SendDeviceEvent(
			config.C.DiscordWebhookEvents,
			p.DeviceID, p.Event, p.Device, p.Reason, p.Temp, p.Lux,
		)
		go discord.SendSensorAlert(config.C.DiscordWebhookAlert, p.DeviceID, "room_entry", 1, 1)
		return nil
	}

	// ── session_end ────────────────────────────────────────────────────────
	if p.Event == "session_end" {
		now := time.Now()
		for _, devType := range []string{"fan", "light", "ac"} {
			devID, ok := devMap[devType]
			if !ok {
				continue
			}
			if _, err := db.DB.Exec(
				`UPDATE device_sessions SET stopped_at=$1 WHERE device_id=$2 AND stopped_at IS NULL`,
				now, devID,
			); err != nil {
				log.Printf("[event] session_end stopped_at (%s): %v", devType, err)
			} else {
				log.Printf("[event] session_end closed sessions for type=%s device_id=%s", devType, devID)
			}
		}

		// Reset ir_state = false
		if devID := firstDevID(devMap, "fan", "ac", "light", "tv"); devID != "" {
			hourWindow := now.Truncate(time.Hour)
			if _, err := db.DB.Db.Exec(`
				UPDATE ir_readings SET ir_state = false, recorded_at = NOW()
				WHERE device_id = $1 AND hour_window = $2
			`, devID, hourWindow); err != nil {
				log.Printf("[event] IR state reset on session_end error: %v", err)
			}
		}

		// Update all device is_active = false (session ended, relay all off from firmware)
		for _, devType := range []string{"fan", "light", "ac"} {
			if devID, ok := devMap[devType]; ok {
				if _, err := db.DB.Exec(
					`UPDATE devices SET is_active=false WHERE id=$1`,
					devID,
				); err != nil {
					log.Printf("[event] session_end device deactivate (%s): %v", devType, err)
				}
			}
		}
		InvalidateDeviceCache(p.DeviceID)

		go discord.SendDeviceEvent(
			config.C.DiscordWebhookEvents,
			p.DeviceID, p.Event, p.Device, p.Reason, p.Temp, p.Lux,
		)
		go discord.SendSensorAlert(config.C.DiscordWebhookAlert, p.DeviceID, "no_presence", 0, 0)
		return nil
	}

	// ── relay / manual / auto events ──────────────────────────────────────
	// device = "fan" | "light" | "ac" | "tv"
	devID, devExists := devMap[p.Device]
	if devExists {
		now := time.Now()

		switch p.Event {

		// manual_on / auto_on → เปิด DeviceSession ใหม่
		// [FIX-MANUAL] ปิด session เก่าก่อนเสมอ ป้องกัน multi-open
		case "manual_on", "auto_on":
			// ปิด session ที่ค้างก่อน
			if _, err := db.DB.Exec(
				`UPDATE device_sessions SET stopped_at=$1 WHERE device_id=$2 AND stopped_at IS NULL`,
				now, devID,
			); err != nil {
				log.Printf("[event] %s close stale sessions (%s): %v", p.Event, p.Device, err)
			}
			if err := db.DB.Insert(&models.DeviceSession{
				ID:        uuid.New().String(),
				DeviceID:  devID,
				StartedAt: now,
			}); err != nil {
				log.Printf("[event] DeviceSession insert error (%s %s): %v", p.Event, p.Device, err)
			}
			// Sync device state
			mode := "manual"
			if p.Event == "auto_on" {
				mode = "auto"
			}
			if _, err := db.DB.Exec(
				`UPDATE devices SET is_active=true, mode=$1 WHERE id=$2`,
				mode, devID,
			); err != nil {
				log.Printf("[event] device activate error (%s): %v", p.Device, err)
			}
			InvalidateDeviceCache(p.DeviceID)

		// relay_off → billing source of truth: update DeviceSession with kwh/cost
		case "relay_off":
			var sess models.DeviceSession
			if err := db.DB.SelectOne(&sess,
				`SELECT * FROM device_sessions WHERE device_id=$1 AND stopped_at IS NULL ORDER BY started_at DESC LIMIT 1`,
				devID); err == nil {
				log.Printf("[event] relay_off %s kwh=%.5f cost=%.4f mode=%s",
					p.Device, p.KWh, p.Cost, p.Mode)
				if _, err := db.DB.Exec(
					`UPDATE device_sessions SET stopped_at=$1, total_kwh=$2, total_cost=$3 WHERE id=$4`,
					now, p.KWh, p.Cost, sess.ID,
				); err != nil {
					log.Printf("[event] DeviceSession update error: %v", err)
				}
				go UpdateMonthlyBilling(devID, p.KWh, p.Cost)
			} else {
				log.Printf("[event] relay_off %s — no open session found (may have been closed already)", p.Device)
			}
			// Sync device state
			if _, err := db.DB.Exec(
				`UPDATE devices SET is_active=false WHERE id=$1`,
				devID,
			); err != nil {
				log.Printf("[event] device deactivate error (%s): %v", p.Device, err)
			}
			InvalidateDeviceCache(p.DeviceID)

		// [FIX-SESSION-1] manual_off / auto_off:
		// DO NOT close device_sessions here.
		// Session is kept alive for the full 30-minute window (closed only by session_end).
		// We only sync the device's is_active state so the dashboard shows correct relay state.
		case "manual_off", "auto_off":
			// Only update device active state — session stays open until session_end
			if _, err := db.DB.Exec(
				`UPDATE devices SET is_active=false WHERE id=$1`,
				devID,
			); err != nil {
				log.Printf("[event] device deactivate error (%s %s): %v", p.Event, p.Device, err)
			}
			InvalidateDeviceCache(p.DeviceID)
		}
	}

	go discord.SendDeviceEvent(
		config.C.DiscordWebhookEvents,
		p.DeviceID, p.Event, p.Device, p.Reason, p.Temp, p.Lux,
	)

	return nil
}

// ── ProcessHourly ─────────────────────────────────────────────────────────
func ProcessHourly(p models.HourlyPayload) error {
	go discord.SendHourlySummary(
		config.C.DiscordWebhookHourly,
		p.DeviceID,
		p.KWhHourFan, p.KWhHourLight, p.KWhHourAC, p.KWhHourTV, p.KWhHourTotal,
		p.MotionCount,
		config.C.CostPerKWh,
	)
	return nil
}

// ── EstimateKWh ───────────────────────────────────────────────────────────
func EstimateKWh(deviceType string, hours float64) float64 {
	watts := map[string]float64{
		"fan": 60, "light": 36, "ac": 1200, "tv": 150,
	}
	if w, ok := watts[deviceType]; ok {
		return (w / 1000) * hours
	}
	return 0
}

// ── UpdateMonthlyBilling ──────────────────────────────────────────────────
func UpdateMonthlyBilling(deviceID string, kwh, cost float64) {
	now := time.Now()
	var dev models.Device
	if err := db.DB.SelectOne(&dev, `SELECT * FROM devices WHERE id=$1`, deviceID); err != nil {
		log.Printf("[billing] device not found: %v", err)
		return
	}
	if _, err := db.DB.Exec(`
		INSERT INTO monthly_billing (id, user_id, year, month, total_kwh, total_cost, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
		ON CONFLICT (user_id, year, month)
		DO UPDATE SET
			total_kwh  = monthly_billing.total_kwh  + EXCLUDED.total_kwh,
			total_cost = monthly_billing.total_cost + EXCLUDED.total_cost
	`, uuid.New().String(), dev.UserID, now.Year(), int(now.Month()), kwh, cost); err != nil {
		log.Printf("[billing] upsert error: %v", err)
	}
}
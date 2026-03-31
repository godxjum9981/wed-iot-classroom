package models

import "time"

// ── Database models ───────────────────────────────────────────────────────

type User struct {
	ID           string     `db:"id"             json:"id"`
	Username     string     `db:"username"       json:"username"`
	PasswordHash string     `db:"password_hash"  json:"-"`
	CreatedAt    time.Time  `db:"created_at"     json:"created_at"`
	LastLogin    *time.Time `db:"last_login"     json:"last_login,omitempty"`
}

type ESP32Device struct {
	ID        string    `db:"id"         json:"id"`
	UserID    string    `db:"user_id"    json:"user_id"`
	ESP32ID   string    `db:"esp32_id"   json:"esp32_id"`
	Name      string    `db:"name"       json:"name"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

type Device struct {
	ID        string    `db:"id"         json:"id"`
	UserID    string    `db:"user_id"    json:"user_id"`
	ESP32ID   string    `db:"esp32_id"   json:"esp32_id"`
	Name      string    `db:"name"       json:"name"`
	Type      string    `db:"type"       json:"type"`
	Mode      string    `db:"mode"       json:"mode"`
	IsActive  bool      `db:"is_active"  json:"is_active"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

type DHT22Reading struct {
	ID          string    `db:"id"          json:"id"`
	DeviceID    string    `db:"device_id"   json:"device_id"`
	Temperature float64   `db:"temperature" json:"temperature"`
	Humidity    float64   `db:"humidity"    json:"humidity"`
	RecordedAt  time.Time `db:"recorded_at" json:"recorded_at"`
}

type PowerReading struct {
	ID           string    `db:"id"              json:"id"`
	DeviceID     string    `db:"device_id"       json:"device_id"`
	Voltage      float64   `db:"voltage"         json:"voltage"`
	Current      float64   `db:"current"         json:"current"`
	Watt         float64   `db:"watt"            json:"watt"`
	WattMeasured float64   `db:"watt_measured"   json:"watt_measured"`
	WattTVSim    float64   `db:"watt_tv_sim"     json:"watt_tv_sim"`
	WattTotal    float64   `db:"watt_total"      json:"watt_total"`
	KWhTotal     float64   `db:"kwh_total"       json:"kwh_total"`
	KWhFan       float64   `db:"kwh_fan"         json:"kwh_fan"`
	KWhLight     float64   `db:"kwh_light"       json:"kwh_light"`
	KWhAC        float64   `db:"kwh_ac"          json:"kwh_ac"`
	KWhTV        float64   `db:"kwh_tv"          json:"kwh_tv"`
	KWhHourFan   float64   `db:"kwh_hour_fan"    json:"kwh_hour_fan"`
	KWhHourLight float64   `db:"kwh_hour_light"  json:"kwh_hour_light"`
	KWhHourAC    float64   `db:"kwh_hour_ac"     json:"kwh_hour_ac"`
	KWhHourTV    float64   `db:"kwh_hour_tv"     json:"kwh_hour_tv"`
	RecordedAt   time.Time `db:"recorded_at"     json:"recorded_at"`
}

type LDRReading struct {
	ID         string    `db:"id"          json:"id"`
	DeviceID   string    `db:"device_id"   json:"device_id"`
	LuxValue   float64   `db:"lux_value"   json:"lux_value"`
	RecordedAt time.Time `db:"recorded_at" json:"recorded_at"`
}

// [FIX-IR-6] เพิ่ม IRState column ใน IRReading struct
// ir_state = true  → IR sensor ตรวจพบคนอยู่ ณ ขณะนั้น (beam break)
// ir_state = false → IR sensor ไม่พบคน (beam clear)
// แยกออกจาก motion_count ซึ่งเป็น cumulative count ต่อชั่วโมง
type IRReading struct {
	ID          string    `db:"id"           json:"id"`
	DeviceID    string    `db:"device_id"    json:"device_id"`
	MotionCount int       `db:"motion_count" json:"motion_count"`
	IRState     bool      `db:"ir_state"     json:"ir_state"` // [FIX-IR-6] NEW: realtime state
	HourWindow  time.Time `db:"hour_window"  json:"hour_window"`
	RecordedAt  time.Time `db:"recorded_at"  json:"recorded_at"`
}

type DeviceSession struct {
	ID        string     `db:"id"         json:"id"`
	DeviceID  string     `db:"device_id"  json:"device_id"`
	StartedAt time.Time  `db:"started_at" json:"started_at"`
	StoppedAt *time.Time `db:"stopped_at" json:"stopped_at,omitempty"`
	TotalKWh  float64    `db:"total_kwh"  json:"total_kwh"`
	TotalCost float64    `db:"total_cost" json:"total_cost"`
}

type MonthlyBilling struct {
	ID        string    `db:"id"         json:"id"`
	UserID    string    `db:"user_id"    json:"user_id"`
	Year      int       `db:"year"       json:"year"`
	Month     int       `db:"month"      json:"month"`
	TotalKWh  float64   `db:"total_kwh"  json:"total_kwh"`
	TotalCost float64   `db:"total_cost" json:"total_cost"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
}

// ── MQTT / HTTP Payloads ──────────────────────────────────────────────────

// SensorPayload — ESP32 publishes ทุก interval บน /sensors topic
// [FIX-IR-7] ir_state ต้องส่งมาจาก firmware ใน publishSensors():
//
//	doc["ir_state"] = irStableState;
type SensorPayload struct {
	DeviceID            string  `json:"device_id"`
	Temperature         float64 `json:"temperature"`
	Humidity            float64 `json:"humidity"`
	LuxValue            float64 `json:"lux_value"`
	Voltage             float64 `json:"voltage"`
	Current             float64 `json:"current"`
	Watt                float64 `json:"watt"`
	WattMeasured        float64 `json:"watt_measured"`
	WattTVSim           float64 `json:"watt_tv_sim"`
	WattTotal           float64 `json:"watt_total"`
	KWhTotal            float64 `json:"kwh_total"`
	KWhFan              float64 `json:"kwh_fan"`
	KWhLight            float64 `json:"kwh_light"`
	KWhAC               float64 `json:"kwh_ac"`
	KWhTV               float64 `json:"kwh_tv"`
	KWhHourFan          float64 `json:"kwh_hour_fan"`
	KWhHourLight        float64 `json:"kwh_hour_light"`
	KWhHourAC           float64 `json:"kwh_hour_ac"`
	KWhHourTV           float64 `json:"kwh_hour_tv"`
	MotionCount         int     `json:"motion_count"`
	RoomOccupied        bool    `json:"room_occupied"`
	SessionActive       bool    `json:"session_active"`
	SessionRemainingSec int64   `json:"session_remaining_sec"`
	AutoFan             bool    `json:"auto_fan"`
	AutoLight           bool    `json:"auto_light"`
	AutoAC              bool    `json:"auto_ac"`
	RelayFan            bool    `json:"relay_fan"`
	RelayLight          bool    `json:"relay_light"`
	RelayAC             bool    `json:"relay_ac"`
	TVOn                bool    `json:"tv_on"`
	TVMode              string  `json:"tv_mode"`
	// [FIX-IR-7] ir_state — true = IR ตรวจพบคนอยู่ขณะนี้ (irStableState จาก firmware)
	// firmware ต้องส่ง: doc["ir_state"] = irStableState; ใน publishSensors()
	IRState bool `json:"ir_state"`
}

// EventPayload — ESP32 publishes on /events topic
// รองรับ event ทุกประเภทรวมถึง ir_motion / ir_idle
type EventPayload struct {
	DeviceID string  `json:"device_id"`
	Event    string  `json:"event"`
	Device   string  `json:"device"`
	Reason   string  `json:"reason"`
	Temp     float64 `json:"temp"`
	Lux      float64 `json:"lux"`
	// fields สำหรับ event "relay_off" เท่านั้น (อื่น = 0/"")
	KWh  float64 `json:"kwh"`
	Cost float64 `json:"cost"`
	Mode string  `json:"mode"`
	// [FIX-IR-7] fields สำหรับ event "ir_motion" / "ir_idle"
	// ir_state = true  → ir_motion (มีคน)
	// ir_state = false → ir_idle   (ไม่มีคน)
	IRState     bool `json:"ir_state"`
	MotionCount int  `json:"motion_count"`
	// session remaining — ใช้กับ event ที่เกี่ยวกับ session
	SessionRemainingSec int64 `json:"session_remaining_sec"`
}

type HourlyPayload struct {
	DeviceID     string  `json:"device_id"`
	KWhHourFan   float64 `json:"kwh_hour_fan"`
	KWhHourLight float64 `json:"kwh_hour_light"`
	KWhHourAC    float64 `json:"kwh_hour_ac"`
	KWhHourTV    float64 `json:"kwh_hour_tv"`
	KWhHourTotal float64 `json:"kwh_hour_total"`
	MotionCount  int     `json:"motion_count"`
}

type ControlPayload struct {
	Command string `json:"command"` // "on" | "off" | "set_mode"
	Device  string `json:"device"`  // "fan" | "light" | "ac" | "tv"
	Mode    string `json:"mode"`    // "auto" | "manual" | tv mode string
}

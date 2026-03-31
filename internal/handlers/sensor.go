package handlers

import (
	"errors"
	"log"
	"strconv"

	"github.com/gofiber/fiber/v3"

	"github.com/classroom/go-example/internal/models"
	"github.com/classroom/go-example/internal/services"
)

// queryInt reads a query param as int with a default fallback.
func queryInt(c fiber.Ctx, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// POST /api/sensor/ingest
func IngestSensor(c fiber.Ctx) error {
	var p models.SensorPayload
	if err := c.Bind().JSON(&p); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid payload"})
	}
	if p.DeviceID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "device_id is required"})
	}
	log.Printf("[ingest] sensor device=%s T=%.1f H=%.1f lux=%.0f relay_fan=%v relay_light=%v relay_ac=%v",
		p.DeviceID, p.Temperature, p.Humidity, p.LuxValue, p.RelayFan, p.RelayLight, p.RelayAC)

	if err := services.ProcessSensor(p); err != nil {
		if errors.Is(err, services.ErrDeviceNotFound) {
			return c.Status(404).JSON(fiber.Map{"error": "device not found: " + p.DeviceID})
		}
		log.Printf("[ingest] ProcessSensor error device=%s: %v", p.DeviceID, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// POST /api/sensor/event
func IngestEvent(c fiber.Ctx) error {
	var p models.EventPayload
	if err := c.Bind().JSON(&p); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid payload"})
	}
	if p.DeviceID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "device_id is required"})
	}
	log.Printf("[ingest] event device=%s event=%s dev=%s reason=%s",
		p.DeviceID, p.Event, p.Device, p.Reason)

	if err := services.ProcessEvent(p); err != nil {
		if errors.Is(err, services.ErrDeviceNotFound) {
			return c.Status(404).JSON(fiber.Map{"error": "device not found: " + p.DeviceID})
		}
		log.Printf("[ingest] ProcessEvent error device=%s: %v", p.DeviceID, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// POST /api/sensor/hourly
func IngestHourly(c fiber.Ctx) error {
	var p models.HourlyPayload
	if err := c.Bind().JSON(&p); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "invalid payload"})
	}
	if p.DeviceID == "" {
		return c.Status(400).JSON(fiber.Map{"error": "device_id is required"})
	}
	log.Printf("[ingest] hourly device=%s fan=%.4f light=%.4f ac=%.4f tv=%.4f kWh",
		p.DeviceID, p.KWhHourFan, p.KWhHourLight, p.KWhHourAC, p.KWhHourTV)

	if err := services.ProcessHourly(p); err != nil {
		if errors.Is(err, services.ErrDeviceNotFound) {
			return c.Status(404).JSON(fiber.Map{"error": "device not found: " + p.DeviceID})
		}
		log.Printf("[ingest] ProcessHourly error device=%s: %v", p.DeviceID, err)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"ok": true})
}

// getDeviceMap is used by dashboard.go
func getDeviceMap(esp32ID string) (map[string]string, error) {
	return services.GetDeviceMap(esp32ID)
}
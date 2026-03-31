package main

import (
	"log"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/recover"
	"github.com/gofiber/fiber/v3/middleware/static"

	"github.com/classroom/go-example/internal/config"
	"github.com/classroom/go-example/internal/db"
	"github.com/classroom/go-example/internal/handlers"
	"github.com/classroom/go-example/internal/middleware"
	"github.com/classroom/go-example/internal/mqttc"
)

func main() {
	config.Load()
	db.Connect()
	mqttc.Connect()

	app := fiber.New(fiber.Config{
		AppName:      "Smart Classroom API v1.1",
		ErrorHandler: errorHandler,
	})

	app.Use(recover.New())
	app.Use(logger.New(logger.Config{
		Format: "[${time}] ${status} ${method} ${path} — ${latency}\n",
	}))
	app.Use(cors.New(cors.Config{
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{"Origin", "Content-Type", "Authorization"},
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
	}))
	app.Use("/", static.New("./view"))

	app.Get("/health", func(c fiber.Ctx) error {
		mq := mqttc.GetMQ()
		return c.JSON(fiber.Map{
			"status":         "ok",
			"service":        "smart-classroom-api",
			"mqtt_connected": mq != nil && mq.IsConnected(),
		})
	})

	api := app.Group("/api")

	// ── Auth ──────────────────────────────────────────────────────────────
	auth := api.Group("/auth")
	auth.Post("/login", handlers.Login)
	auth.Post("/register", handlers.Register)

	// ── Sensor ingestion (HTTP alternative to MQTT) ───────────────────────
	sensor := api.Group("/sensor")
	sensor.Post("/ingest", handlers.IngestSensor)
	sensor.Post("/event", handlers.IngestEvent)
	sensor.Post("/hourly", handlers.IngestHourly)

	// ── Protected routes ──────────────────────────────────────────────────
	protected := api.Group("/", middleware.JWTProtected())

	protected.Get("/auth/me", handlers.Me)

	// Dashboard
	dash := protected.Group("/dashboard")
	dash.Get("/latest", handlers.DashboardLatest)
	dash.Get("/power-chart", handlers.PowerChart)
	dash.Get("/dht-chart", handlers.DHTChart)
	dash.Get("/ldr-chart", handlers.LDRChart)
	dash.Get("/sessions", handlers.Sessions)
	dash.Get("/billing", handlers.Billing)
	dash.Get("/ir-hourly", handlers.IRHourly)

	// Devices
	devices := protected.Group("/devices")
	devices.Get("/", handlers.GetDevices)

	// Session status check (ก่อน :id เพื่อหลีกเลี่ยง route conflict)
	devices.Get("/esp32/:esp32_id/session-status", handlers.SessionStatus)

	devices.Get("/:id", handlers.GetDevice)
	devices.Get("/:id/status", handlers.DeviceStatus)
	devices.Get("/:id/sessions", handlers.DeviceSessions)

	// Control endpoints
	// POST /api/devices/:id/control  — standard control (session-checked for fan/light/ac ON)
	devices.Post("/:id/control", handlers.ControlDevice)

	// POST /api/devices/:id/force-off — emergency off, no session check required
	devices.Post("/:id/force-off", handlers.ForceOffDevice)

	addr := ":" + config.C.Port
	log.Printf("🏫 Smart Classroom API v1.1 starting on %s (env: %s)", addr, config.C.AppEnv)
	log.Fatal(app.Listen(addr))
}

func errorHandler(c fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}
	return c.Status(code).JSON(fiber.Map{"error": err.Error()})
}
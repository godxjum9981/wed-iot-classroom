package db

import (
	"database/sql"
	"fmt"
	"log"

	gorp "github.com/go-gorp/gorp/v3"
	_ "github.com/lib/pq"

	"github.com/classroom/go-example/internal/config"
	"github.com/classroom/go-example/internal/models"
)

var DB *gorp.DbMap

func Connect() {
	cfg := config.C
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBPassword, cfg.DBName, cfg.DBSSLMode,
	)

	sqlDB, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("[DB] Open failed: %v", err)
	}

	if err = sqlDB.Ping(); err != nil {
		log.Fatalf("[DB] Ping failed: %v", err)
	}

	sqlDB.SetMaxOpenConns(25)
	sqlDB.SetMaxIdleConns(5)

	DB = &gorp.DbMap{Db: sqlDB, Dialect: gorp.PostgresDialect{}}

	DB.AddTableWithName(models.User{}, "users").SetKeys(false, "ID")
	DB.AddTableWithName(models.ESP32Device{}, "esp32_devices").SetKeys(false, "ID") // [FIX] was missing — GetDevices ownership check panics without it
	DB.AddTableWithName(models.Device{}, "devices").SetKeys(false, "ID")
	DB.AddTableWithName(models.DHT22Reading{}, "dht22_readings").SetKeys(false, "ID")
	DB.AddTableWithName(models.PowerReading{}, "power_readings").SetKeys(false, "ID")
	DB.AddTableWithName(models.LDRReading{}, "ldr_readings").SetKeys(false, "ID")
	DB.AddTableWithName(models.IRReading{}, "ir_readings").SetKeys(false, "ID")
	DB.AddTableWithName(models.DeviceSession{}, "device_sessions").SetKeys(false, "ID")
	DB.AddTableWithName(models.MonthlyBilling{}, "monthly_billing").SetKeys(false, "ID")

	if config.C.AppEnv == "development" {
		DB.TraceOn("[gorp]", log.New(log.Writer(), "", log.LstdFlags))
	}

	log.Printf("[DB] Connected to PostgreSQL — %s:%s/%s", cfg.DBHost, cfg.DBPort, cfg.DBName)
}
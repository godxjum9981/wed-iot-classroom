package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	JWTSecret string
	Port      string
	AppEnv    string

	DiscordWebhookAlert  string
	DiscordWebhookHourly string
	DiscordWebhookEvents string

	MQTTBroker   string
	MQTTClientID string
	MQTTUser     string
	MQTTPass     string

	CostPerKWh float64
}

var C *Config

func Load() {
	if err := godotenv.Load(); err != nil {
		log.Println("[Config] No .env file found, using environment variables")
	}

	cost, _ := strconv.ParseFloat(getEnv("COST_PER_KWH", "5.2"), 64)

	C = &Config{
		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "postgres"),
		DBPassword: getEnv("DB_PASSWORD", ""),
		DBName:     getEnv("DB_NAME", "cla"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		JWTSecret: getEnv("JWT_SECRET", "change_me_in_production"),
		Port:      getEnv("PORT", "8080"),
		AppEnv:    getEnv("APP_ENV", "development"),

		DiscordWebhookAlert:  getEnv("DISCORD_WEBHOOK_ALERT", ""),
		DiscordWebhookHourly: getEnv("DISCORD_WEBHOOK_HOURLY", ""),
		DiscordWebhookEvents: getEnv("DISCORD_WEBHOOK_EVENTS", ""),

		MQTTBroker:   getEnv("MQTT_BROKER", "tcp://localhost:1883"),
		MQTTClientID: getEnv("MQTT_CLIENT_ID", "smartclassroom-server"),
		MQTTUser:     getEnv("MQTT_USER", ""),
		MQTTPass:     getEnv("MQTT_PASS", ""),

		CostPerKWh: cost,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
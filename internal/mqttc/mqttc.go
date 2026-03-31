package mqttc

import (
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/classroom/go-example/internal/config"
	"github.com/classroom/go-example/internal/models"
	"github.com/classroom/go-example/internal/services"
)

type Client struct {
	inner mqtt.Client
}

var (
	MQ   *Client
	mqMu sync.Mutex
)

// GetMQ คืน client ปัจจุบัน — nil-safe
func GetMQ() *Client {
	mqMu.Lock()
	defer mqMu.Unlock()
	return MQ
}

func Connect() {
	cfg := config.C

	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.MQTTBroker)
	opts.SetClientID(cfg.MQTTClientID)

	if cfg.MQTTUser != "" {
		opts.SetUsername(cfg.MQTTUser)
		opts.SetPassword(cfg.MQTTPass)
	}

	opts.SetAutoReconnect(true)
	opts.SetMaxReconnectInterval(10 * time.Second)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetPingTimeout(5 * time.Second)

	opts.SetOnConnectHandler(func(c mqtt.Client) {
		log.Printf("[mqtt] connected to %s", cfg.MQTTBroker)
		subscribeAll(c)
	})
	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("[mqtt] connection lost: %v", err)
	})
	opts.SetReconnectingHandler(func(c mqtt.Client, _ *mqtt.ClientOptions) {
		log.Println("[mqtt] reconnecting...")
	})

	client := mqtt.NewClient(opts)

	mqMu.Lock()
	MQ = &Client{inner: client}
	mqMu.Unlock()

	go func() {
		tok := client.Connect()
		tok.Wait()
		if err := tok.Error(); err != nil {
			log.Printf("[mqtt] initial connect failed: %v (auto-retry enabled)", err)
		}
	}()
}

// Publish ส่ง control command ไปยัง ESP32 device
// [FIX] ตรวจสอบ IsConnected ก่อนเสมอ และใช้ WaitTimeout เพื่อไม่บล็อกตลอดไป
func (c *Client) Publish(esp32ID string, payload models.ControlPayload) error {
	if c == nil {
		return fmt.Errorf("mqtt client is nil")
	}
	if !c.inner.IsConnected() {
		return fmt.Errorf("mqtt not connected (broker: %s)", config.C.MQTTBroker)
	}

	topic := fmt.Sprintf("classroom/%s/control", esp32ID)
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("json marshal error: %w", err)
	}

	log.Printf("[mqtt] publish -> %s : %s", topic, string(b))

	tok := c.inner.Publish(topic, 1, false, b)
	if !tok.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("mqtt publish timeout (topic=%s)", topic)
	}
	if err := tok.Error(); err != nil {
		return fmt.Errorf("mqtt publish error: %w", err)
	}

	log.Printf("[mqtt] published OK -> %s", topic)
	return nil
}

// IsConnected คืนสถานะการเชื่อมต่อ
func (c *Client) IsConnected() bool {
	if c == nil {
		return false
	}
	return c.inner.IsConnected()
}

// Disconnect ส่ง DISCONNECT packet ก่อน shutdown
func (c *Client) Disconnect() {
	if c == nil {
		return
	}
	c.inner.Disconnect(500)
	log.Println("[mqtt] disconnected")
}

// subscribeAll subscribe topics ทั้งหมด
// [FIX] ใช้ slice แทน map เพื่อ deterministic order
//       ใช้ WaitTimeout(5s) เพื่อป้องกัน hung broker บล็อก reconnect handler
func subscribeAll(c mqtt.Client) {
	type sub struct {
		topic   string
		handler mqtt.MessageHandler
	}
	subs := []sub{
		{"classroom/+/sensors", onSensors},
		{"classroom/+/events", onEvent},
		{"classroom/+/hourly", onHourly},
	}

	failCount := 0
	for _, s := range subs {
		tok := c.Subscribe(s.topic, 1, s.handler)
		if !tok.WaitTimeout(5 * time.Second) {
			log.Printf("[mqtt] subscribe timeout: %s", s.topic)
			failCount++
			continue
		}
		if err := tok.Error(); err != nil {
			log.Printf("[mqtt] subscribe failed %s: %v", s.topic, err)
			failCount++
			continue
		}
		log.Printf("[mqtt] subscribed -> %s", s.topic)
	}
	if failCount > 0 {
		log.Printf("[mqtt] WARNING: %d/%d topic(s) failed to subscribe", failCount, len(subs))
	}
}

func onSensors(_ mqtt.Client, msg mqtt.Message) {
	var p models.SensorPayload
	if err := json.Unmarshal(msg.Payload(), &p); err != nil {
		log.Printf("[mqtt] sensors parse error: %v", err)
		return
	}
	log.Printf("[mqtt] sensors <- %s  T=%.1fC H=%.1f%% Lux=%.0f W=%.1f relay_fan=%v relay_light=%v relay_ac=%v",
		p.DeviceID, p.Temperature, p.Humidity, p.LuxValue, p.WattTotal, p.RelayFan, p.RelayLight, p.RelayAC)
	if err := services.ProcessSensor(p); err != nil {
		log.Printf("[mqtt] ProcessSensor error: %v", err)
	}
}

func onEvent(_ mqtt.Client, msg mqtt.Message) {
	var p models.EventPayload
	if err := json.Unmarshal(msg.Payload(), &p); err != nil {
		log.Printf("[mqtt] events parse error: %v", err)
		return
	}
	log.Printf("[mqtt] event <- %s  event=%s device=%s reason=%s",
		p.DeviceID, p.Event, p.Device, p.Reason)
	if err := services.ProcessEvent(p); err != nil {
		log.Printf("[mqtt] ProcessEvent error: %v", err)
	}
}

func onHourly(_ mqtt.Client, msg mqtt.Message) {
	var p models.HourlyPayload
	if err := json.Unmarshal(msg.Payload(), &p); err != nil {
		log.Printf("[mqtt] hourly parse error: %v", err)
		return
	}
	log.Printf("[mqtt] hourly <- %s  total=%.4fkWh motions=%d",
		p.DeviceID, p.KWhHourTotal, p.MotionCount)
	if err := services.ProcessHourly(p); err != nil {
		log.Printf("[mqtt] ProcessHourly error: %v", err)
	}
}
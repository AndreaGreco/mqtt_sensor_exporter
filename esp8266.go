package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type Ds28b20Temperautre struct {
	unix_time   uint64
	temperature float32
	gap         uint32
}

type ESP8266_obj struct {
	sensor     map[uint64]Ds28b20Temperautre
	LastRescan time.Time
}

var nodes map[uint64]*ESP8266_obj

var NodesMutex *sync.Mutex

var (
	messagesPublished = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "esp8266_temperature_value",
			Help: "ESP8266 temperature node",
		}, []string{"mac", "address"})
)

func init() {
	nodes = make(map[uint64]*ESP8266_obj)
	prometheus.MustRegister(messagesPublished)
	NodesMutex = &sync.Mutex{}
	ESP8266init()
}

func (esp *ESP8266_obj) UpdateSensor(Address uint64, Sensor Ds28b20Temperautre) {
	if esp.sensor == nil {
		esp.sensor = make(map[uint64]Ds28b20Temperautre)
	}
	esp.sensor[Address] = Sensor
}

func RemoveSensorFromNode(mac uint64, node *ESP8266_obj) {
	// If elapsed mode then 60 second from last
	elapsed := time.Now().Sub(node.LastRescan)

	if elapsed.Seconds() > conf.Ds18b20Timeout.Seconds() {
		for DS18B20Address, _ := range node.sensor {
			StrDS18B20MAC := fmt.Sprintf("%X", DS18B20Address)
			StrMAC := fmt.Sprintf("%X", mac)

			done := messagesPublished.DeleteLabelValues(StrMAC, StrDS18B20MAC)
			delete(nodes, mac)

			if !done {
				logger.Printf("DELETE:esp8266:%s, Sensor:%s\n", StrMAC, StrDS18B20MAC)
			}
		}
	}
}

func ESP8266init() {
	ticker := time.NewTicker(1 * time.Second)

	go func() {
		for {
			select {
			case <-ticker.C:
				// Every ticker
				for mac, node := range nodes {
					RemoveSensorFromNode(mac, node)
				}
			}
		}
	}()
}

func DS18b20ReadTemperature(mac uint64, payload []byte) {
	var data Ds28b20Temperautre
	var DS18B20Address uint64
	var CurNode *ESP8266_obj
	var ok bool
	// Create IO reader
	buf := bytes.NewReader(payload)

	// Unpack struct
	binary.Read(buf, binary.LittleEndian, &DS18B20Address)
	binary.Read(buf, binary.LittleEndian, &data.unix_time)
	binary.Read(buf, binary.LittleEndian, &data.temperature)

	// Create object
	if CurNode, ok = nodes[mac]; !ok {
		CurNode = new(ESP8266_obj)
		nodes[mac] = CurNode
	}

	CurNode.UpdateSensor(DS18B20Address, data)
	CurNode.LastRescan = time.Now()

	StrMAC := fmt.Sprintf("%X", mac)
	StrDS18B20MAC := fmt.Sprintf("%X", DS18B20Address)

	messagesPublished.WithLabelValues(StrMAC, StrDS18B20MAC).Set(float64(data.temperature))
}

func ESP_Handler() http.Handler {
	NodesMutex.Lock()
	ret := prometheus.Handler()
	NodesMutex.Unlock()
	return ret
}

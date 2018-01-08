package main

import (
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"gopkg.in/yaml.v2"
)

type config struct {
	Name           string        `yaml:"name"`
	Broker         string        `yaml:"broker_url"`
	Topic          string        `yaml:"topic"`
	ClientName     string        `yaml:"client_name"`
	Username       string        `yaml:"username"`
	Password       string        `yaml:"password"`
	ClientCert     string        `yaml:"client_cert"`
	ClientKey      string        `yaml:"client_key"`
	CAChain        string        `yaml:"ca_chain"`
	Ds18b20Timeout time.Duration `yaml:"ds18b20_timeout"`
}

var build string
var conf config

var (
	logger        = log.New(os.Stderr, "", log.Lmicroseconds|log.Ltime|log.Lshortfile)
	configFile    = flag.String("config.file", "config.yaml", "Exporter configuration file.")
	listenAddress = flag.String("web.listen-address", ":9214", "The address to listen on for HTTP requests.")
)

func NewTlsConfig(conf *config) *tls.Config {
	// Import trusted certificates from CAChain - purely for verification - not sent to TLS server
	certpool := x509.NewCertPool()
	pemCerts, err := ioutil.ReadFile(conf.CAChain)
	if err == nil {
		certpool.AppendCertsFromPEM(pemCerts)
	}

	// Import client certificate/key pair
	// If you want the chain certs to be sent to the server, concatenate the leaf,
	//  intermediate and root into the ClientCert file
	cert, err := tls.LoadX509KeyPair(conf.ClientCert, conf.ClientKey)
	if err != nil {
		return &tls.Config{}
	}

	// Create tls.Config with desired tls properties
	return &tls.Config{
		// RootCAs = certs used to verify server cert.
		RootCAs: certpool,
		// ClientAuth = whether to request cert from server.
		// Since the server is set up for SSL, this happens
		// anyways.
		ClientAuth: tls.NoClientCert,
		// InsecureSkipVerify = verify that cert contents
		// match server. IP matches what is in cert etc.
		InsecureSkipVerify: false,
		// Certificates = list of certs client sends to server.
		Certificates: []tls.Certificate{cert},
	}
}

func StartMqtt(conf *config) {
	// testTimeout := 10 * time.Second
	queue := make(chan mqtt.Message)

	tlsconfig := NewTlsConfig(conf)

	subscriberOptions := mqtt.NewClientOptions().SetClientID(fmt.Sprintf("%s-promethues", conf.ClientName))
	subscriberOptions.SetUsername(conf.Username)
	subscriberOptions.SetPassword(conf.Password)
	subscriberOptions.SetTLSConfig(tlsconfig)
	subscriberOptions.AddBroker(conf.Broker)

	MqttClient := mqtt.NewClient(subscriberOptions)

	OnMqttMessage := func(client mqtt.Client, message mqtt.Message) {
		queue <- message
	}

	if token := MqttClient.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	SubscribeString := fmt.Sprintf("%s/#", conf.Topic)
	if token := MqttClient.Subscribe(SubscribeString, 0, OnMqttMessage); token.Wait() && token.Error() != nil {
		fmt.Println(token.Error())
	}

	ReTemperature := regexp.MustCompile(fmt.Sprintf("%s/(?P<Esp_MAC>[0-9ABCDEF]+)/temperature", conf.Topic))
	//	ReRescan := regexp.MustCompile(fmt.Sprintf("%s/(?P<Esp_MAC>[0-9ABCDEF]+)/rescan_temperature", conf.Topic))

	for {
		select {
		case msg := <-queue:

			group := ReTemperature.FindStringSubmatch(msg.Topic())
			if group != nil {
				mac, err := strconv.ParseInt(group[1], 16, 64)
				if err != nil {
					log.Fatalf("%v", err)
				}

				DS18b20ReadTemperature(uint64(mac), msg.Payload())
				continue
			}
		}
	}
}

func main() {
	flag.Parse()
	yamlFile, err := ioutil.ReadFile(*configFile)

	if err != nil {
		logger.Fatalf("Error reading config file: %s", err)
	}

	conf = config{}

	err = yaml.Unmarshal(yamlFile, &conf)
	if err != nil {
		logger.Fatalf("Error parsing config file: %s", err)
	}

	logger.Printf("Starting mqtt_blackbox_exporter (build: %s)\n", build)

	logger.Printf("Name:%s\n", conf.Name)
	logger.Printf("Borker:%s\n", conf.Broker)
	logger.Printf("Topic:%s\n", conf.Topic)
	logger.Printf("Client Name:%s\n", conf.ClientName)
	logger.Printf("Username:%s\n", conf.Username)
	logger.Printf("Password:%s\n", conf.Password)
	logger.Printf("Sensor Timeout:%s\n", conf.Ds18b20Timeout)

	go StartMqtt(&conf)

	http.Handle("/metrics", ESP_Handler())
	http.ListenAndServe(*listenAddress, nil)
}

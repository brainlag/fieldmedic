package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"
)

type Health struct {
	Status string  `json:"status"`
	Checks []Check `json:"checks"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Data   Data   `json:"data"`
}

type Data struct {
	Description string `json:"description"`
}

type Config struct {
	Interval        time.Duration `yaml:"interval"`
	AlertmanagerURL string        `yaml:"alertmanager_url"`
	Services        []Service     `yaml:"services"`
}

type Service struct {
	Name    string   `yaml:"name"`
	Path    string   `yaml:"path"`
	Port    string   `yaml:"port"`
	Hosts   []string `yaml:"hosts"`
	Warning []string `yaml:"warning"`
	Ignore  []string `yaml:"ignore"`
}

type ServiceEndpoint struct {
	Name    string
	Path    string
	Host    string
	Port    string
	Warning []string
	Ignore  []string
}

type Diagnosis struct {
	Health   *Health
	Endpoint *ServiceEndpoint
}

type Alert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	GeneratorURL string            `json:"generatorURL"`
}

var done = make(chan bool, 1)

func main() {
	f, err := os.Open("fieldmedic.yml")
	if err != nil {
		log.Fatalln(err)
	}
	defer f.Close()

	var cfg Config
	decoder := yaml.NewDecoder(f)
	err = decoder.Decode(&cfg)
	if err != nil {
		log.Fatalln(err)
	}
	log.Printf("Check Interval: %s", cfg.Interval)
	go checkHealth(cfg)

	sigs := make(chan os.Signal, 1)

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		fmt.Println()
		fmt.Print("Time to go ... ")
		fmt.Println(sig)
		done <- true
	}()

	mainloop(cfg)
}

var netTransport = &http.Transport{
	Dial: (&net.Dialer{
		Timeout: 5 * time.Second,
	}).Dial,
	TLSHandshakeTimeout: 5 * time.Second,
}
var netClient = &http.Client{
	Timeout:   time.Second * 10,
	Transport: netTransport,
}

var checks = make(chan *Diagnosis)

func queryHealthCheckEndpoint(service *ServiceEndpoint) {
	endpointURL := buildURL(service)
	log.Printf("Querying Endpoing: %s", endpointURL)
	resp, err := netClient.Get(endpointURL)
	if err != nil {
		log.Println(err)
		checks <- &Diagnosis{
			Endpoint: service,
			Health: &Health{
				Status: "DOWN",
			},
		}
	}

	defer resp.Body.Close()

	var result Health
	json.NewDecoder(resp.Body).Decode(&result)
	checks <- &Diagnosis{
		Endpoint: service,
		Health:   &result,
	}
}

func buildURL(service *ServiceEndpoint) string {
	url := "http://" + service.Host
	if service.Port != "" {
		url += ":" + service.Port
	}
	url += service.Path
	return url
}

func checkHealth(cfg Config) {
	for diagnose := range checks {
		var alerts []Alert
		if diagnose.Health.Status == "DOWN" && len(diagnose.Health.Checks) == 0 {
			summary := fmt.Sprintf("Service %s on %s is down!", diagnose.Endpoint.Name, diagnose.Endpoint.Host)
			log.Println(summary)
			alerts = append(alerts, Alert{
				Status: "firing",
				Labels: map[string]string{
					"alertname": diagnose.Endpoint.Name,
					"service":   diagnose.Endpoint.Name,
					"severity":  "error",
					"instance":  diagnose.Endpoint.Host},
				Annotations:  map[string]string{"summary": summary},
				GeneratorURL: "http://implemented.not",
			})
		}
		for _, hc := range diagnose.Health.Checks {
			if hc.Status == "DOWN" && !contains(diagnose.Endpoint.Ignore, hc.Name) {
				log.Printf("HealthCheck %s faild on %s for %s: %s", hc.Name, diagnose.Endpoint.Host, diagnose.Endpoint.Name, hc.Data.Description)
				alerts = append(alerts, Alert{
					Status: "firing",
					Labels: map[string]string{
						"alertname": hc.Name,
						"service":   diagnose.Endpoint.Name,
						"severity":  getSeverity(diagnose.Endpoint.Warning, hc.Name),
						"instance":  diagnose.Endpoint.Host},
					Annotations:  map[string]string{"summary": hc.Data.Description},
					GeneratorURL: "http://implemented.not",
				})
			}
		}
		body, err := json.Marshal(alerts)
		if err != nil {
			log.Fatalln(err)
		}

		resp, err := netClient.Post(cfg.AlertmanagerURL, "application/json", bytes.NewBuffer(body))
		if err != nil {
			log.Fatalln(err)
		}

		defer resp.Body.Close()
		// TODO check body?
	}
}

func getSeverity(s []string, e string) string {
	if contains(s, e) {
		return "warning"
	}
	return "error"
}

func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func mainloop(cfg Config) {
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case t := <-ticker.C:
			var _ = t
			log.Printf("Run Checks")
			runChecks(cfg.Services)
		}
	}
}

func runChecks(services []Service) {
	for _, service := range services {
		for _, endpoint := range service.Hosts {
			go queryHealthCheckEndpoint(&ServiceEndpoint{
				Name:    service.Name,
				Path:    service.Path,
				Port:    service.Port,
				Host:    endpoint,
				Warning: service.Warning,
				Ignore:  service.Ignore,
			})
		}
	}
}

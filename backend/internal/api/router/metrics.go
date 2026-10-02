package router

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/im-faix/sentinel/backend/internal/config"
)

type metricSample struct {
	Labels map[string]string `json:"metric"`
	Value  []json.RawMessage `json:"value"`
}

type prometheusResponse struct {
	Status string `json:"status"`
	Data   struct {
		Result []metricSample `json:"result"`
	} `json:"data"`
}

type environmentMetrics struct {
	Name         string                     `json:"name"`
	Status       string                     `json:"status"`
	Message      string                     `json:"message,omitempty"`
	Hosts        []hostMetrics              `json:"hosts"`
	Application  appMetrics                 `json:"application"`
	Applications []applicationTargetMetrics `json:"applications"`
}

type hostMetrics struct {
	Instance      string   `json:"instance"`
	CPUPercent    *float64 `json:"cpuPercent"`
	MemoryPercent *float64 `json:"memoryPercent"`
	DiskPercent   *float64 `json:"diskPercent"`
}

type appMetrics struct {
	RequestRate     *float64 `json:"requestRate"`
	ServerErrorRate *float64 `json:"serverErrorRate"`
	Goroutines      *float64 `json:"goroutines"`
	HeapAllocBytes  *float64 `json:"heapAllocBytes"`
}

type applicationTargetMetrics struct {
	Job         string   `json:"job"`
	Instance    string   `json:"instance"`
	Status      string   `json:"status"`
	CPUCoreRate *float64 `json:"cpuCoreRate"`
	MemoryBytes *float64 `json:"memoryBytes"`
}

var prometheusQueries = map[string]string{
	"cpu":             `100 * (1 - avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])))`,
	"memory":          `100 * (1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`,
	"disk":            `100 * (1 - node_filesystem_avail_bytes{mountpoint="/",fstype!~"tmpfs|overlay"} / node_filesystem_size_bytes{mountpoint="/",fstype!~"tmpfs|overlay"})`,
	"requestRate":     `sum(rate(sentinel_http_requests_total[5m]))`,
	"serverErrorRate": `sum(rate(sentinel_http_requests_total{status=~"5.."}[5m])) or vector(0)`,
	"goroutines":      `go_goroutines`,
	"heapAllocBytes":  `go_memstats_heap_alloc_bytes`,
	"appTargets":      `up{job!~"sentinel|node"}`,
	"appCPU":          `rate(process_cpu_seconds_total{job!~"sentinel|node"}[5m])`,
	"appMemory":       `process_resident_memory_bytes{job!~"sentinel|node"}`,
}

func (a *api) metrics(w http.ResponseWriter, r *http.Request) {
	if a.cfg.MetricsEnvironmentsError != "" {
		write(w, http.StatusServiceUnavailable, map[string]string{"error": a.cfg.MetricsEnvironmentsError})
		return
	}
	results := make([]environmentMetrics, 0, len(a.cfg.MetricsEnvironments))
	for _, environment := range a.cfg.MetricsEnvironments {
		results = append(results, queryEnvironment(r.Context(), environment))
	}
	write(w, http.StatusOK, map[string]any{"environments": results})
}

func queryEnvironment(ctx context.Context, environment config.MetricsEnvironment) environmentMetrics {
	result := environmentMetrics{
		Name: environment.Name, Status: "ok",
		Hosts: []hostMetrics{}, Applications: []applicationTargetMetrics{},
	}
	endpoint, err := prometheusEndpoint(environment.URL)
	parsedURL, parseErr := url.Parse(environment.URL)
	if err != nil || parseErr != nil || !securePrometheusEndpoint(parsedURL, environment.Token) {
		result.Status = "error"
		result.Message = "Prometheus endpoint configuration is invalid."
		return result
	}
	tlsConfig, err := prometheusTLSConfig(environment.CAFile)
	if err != nil {
		result.Status = "error"
		result.Message = "Prometheus TLS configuration is invalid."
		return result
	}

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer httpClient.CloseIdleConnections()
	type queryResult struct {
		name    string
		samples []metricSample
		err     error
	}
	results := make(chan queryResult, len(prometheusQueries))
	var workers sync.WaitGroup
	for name, query := range prometheusQueries {
		workers.Add(1)
		go func(name, query string) {
			defer workers.Done()
			samples, err := queryPrometheus(ctx, httpClient, endpoint, environment.Token, query)
			results <- queryResult{name: name, samples: samples, err: err}
		}(name, query)
	}
	workers.Wait()
	close(results)

	hosts := map[string]*hostMetrics{}
	applications := map[string]*applicationTargetMetrics{}
	getApplication := func(job, instance string) *applicationTargetMetrics {
		key := job + "\x00" + instance
		if applications[key] == nil {
			applications[key] = &applicationTargetMetrics{Job: job, Instance: instance, Status: "unknown"}
		}
		return applications[key]
	}
	for response := range results {
		if response.err != nil {
			result.Status = "degraded"
			result.Message = "Some Prometheus metrics are unavailable."
			continue
		}
		if len(response.samples) == 0 && response.name != "serverErrorRate" &&
			response.name != "appTargets" && response.name != "appCPU" && response.name != "appMemory" {
			result.Status = "degraded"
			result.Message = "Some metrics are unavailable; verify Prometheus scrape targets."
		}
		switch response.name {
		case "cpu", "memory", "disk":
			for _, sample := range response.samples {
				instance := sample.Labels["instance"]
				if instance == "" {
					continue
				}
				if hosts[instance] == nil {
					hosts[instance] = &hostMetrics{Instance: instance}
				}
				value := sampleValue(sample)
				switch response.name {
				case "cpu":
					hosts[instance].CPUPercent = value
				case "memory":
					hosts[instance].MemoryPercent = value
				case "disk":
					hosts[instance].DiskPercent = value
				}
			}
		case "requestRate":
			result.Application.RequestRate = firstSample(response.samples)
		case "serverErrorRate":
			result.Application.ServerErrorRate = firstSample(response.samples)
		case "goroutines":
			result.Application.Goroutines = firstSample(response.samples)
		case "heapAllocBytes":
			result.Application.HeapAllocBytes = firstSample(response.samples)
		case "appTargets":
			for _, sample := range response.samples {
				job, instance := sample.Labels["job"], sample.Labels["instance"]
				if job == "" || instance == "" {
					continue
				}
				target := getApplication(job, instance)
				target.Status = "down"
				if up := sampleValue(sample); up != nil && *up == 1 {
					target.Status = "ok"
				}
			}
		case "appCPU", "appMemory":
			for _, sample := range response.samples {
				job, instance := sample.Labels["job"], sample.Labels["instance"]
				if job == "" || instance == "" {
					continue
				}
				target := getApplication(job, instance)
				value := sampleValue(sample)
				if response.name == "appCPU" {
					target.CPUCoreRate = value
				} else {
					target.MemoryBytes = value
				}
			}
		}
	}
	for _, host := range hosts {
		result.Hosts = append(result.Hosts, *host)
	}
	for _, application := range applications {
		result.Applications = append(result.Applications, *application)
	}
	sort.Slice(result.Hosts, func(i, j int) bool {
		return result.Hosts[i].Instance < result.Hosts[j].Instance
	})
	sort.Slice(result.Applications, func(i, j int) bool {
		if result.Applications[i].Job == result.Applications[j].Job {
			return result.Applications[i].Instance < result.Applications[j].Instance
		}
		return result.Applications[i].Job < result.Applications[j].Job
	})
	complete := len(result.Hosts) > 0 &&
		result.Application.RequestRate != nil &&
		result.Application.ServerErrorRate != nil &&
		result.Application.Goroutines != nil &&
		result.Application.HeapAllocBytes != nil
	for _, host := range result.Hosts {
		complete = complete && host.CPUPercent != nil && host.MemoryPercent != nil && host.DiskPercent != nil
	}
	for _, application := range result.Applications {
		complete = complete && application.Status == "ok"
	}
	if !complete {
		result.Status = "degraded"
		if result.Message == "" {
			result.Message = "Some metrics are unavailable; verify Prometheus scrape targets."
		}
	}
	return result
}

func securePrometheusEndpoint(endpoint *url.URL, token string) bool {
	if endpoint == nil {
		return false
	}
	if endpoint.Scheme == "https" {
		return token != ""
	}
	if endpoint.Scheme != "http" || token != "" {
		return false
	}
	hostname := endpoint.Hostname()
	if net.ParseIP(hostname) != nil {
		return net.ParseIP(hostname).IsLoopback()
	}
	return hostname == "localhost" || (hostname != "" && !strings.Contains(hostname, "."))
}

func prometheusEndpoint(raw string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("invalid Prometheus URL")
	}
	return strings.TrimSuffix(raw, "/") + "/api/v1/query", nil
}

func queryPrometheus(ctx context.Context, client *http.Client, endpoint, token, query string) ([]metricSample, error) {
	requestURL := endpoint + "?" + url.Values{"query": {query}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Prometheus returned status %d", response.StatusCode)
	}
	var payload prometheusResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&payload); err != nil {
		return nil, err
	}
	if payload.Status != "success" {
		return nil, fmt.Errorf("Prometheus query failed")
	}
	return payload.Data.Result, nil
}

func sampleValue(sample metricSample) *float64 {
	if len(sample.Value) != 2 {
		return nil
	}
	var encoded string
	if err := json.Unmarshal(sample.Value[1], &encoded); err != nil {
		return nil
	}
	value, err := strconv.ParseFloat(encoded, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	return &value
}

func firstSample(samples []metricSample) *float64 {
	if len(samples) == 0 {
		return nil
	}
	return sampleValue(samples[0])
}

func prometheusTLSConfig(caFile string) (*tls.Config, error) {
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile == "" {
		return config, nil
	}
	rootCAs, err := x509.SystemCertPool()
	if err != nil || rootCAs == nil {
		rootCAs = x509.NewCertPool()
	}
	caBundle, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	if !rootCAs.AppendCertsFromPEM(caBundle) {
		return nil, fmt.Errorf("CA bundle contains no certificates")
	}
	config.RootCAs = rootCAs
	return config, nil
}

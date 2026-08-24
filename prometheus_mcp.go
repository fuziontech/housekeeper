package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/spf13/viper"
)

const defaultPromEndpoint = "default"

// promClients are keyed by endpoint name.
var promClients = map[string]v1.API{}

// prometheusArgs defines the arguments for Prometheus queries.
type prometheusArgs struct {
	Query string `json:"query"`           // PromQL query string
	Start string `json:"start,omitempty"` // Start time in RFC3339 format or relative ("-30m")
	End   string `json:"end,omitempty"`   // End time in RFC3339 format or relative; defaults to now()
	Step  string `json:"step,omitempty"`  // Step duration (e.g. "15s", "1m", "1h")
}

func buildPromBaseURL(configKey string) string {
	host := viper.GetString(configKey + ".host")
	port := viper.GetInt(configKey + ".port")
	baseURL := fmt.Sprintf("http://%s:%d", host, port)

	if viper.GetBool(configKey + ".vm_cluster_mode") {
		tenantID := viper.GetString(configKey + ".vm_tenant_id")
		pathPrefix := viper.GetString(configKey + ".vm_path_prefix")
		if pathPrefix == "" {
			pathPrefix = "prometheus"
		}
		baseURL = fmt.Sprintf("%s/select/%s/%s", baseURL, tenantID, pathPrefix)
	}
	return baseURL
}

func initPromClient(configKey string) (v1.API, error) {
	cfg := api.Config{Address: buildPromBaseURL(configKey)}
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating prometheus client for %q: %v", configKey, err)
	}
	return v1.NewAPI(client), nil
}

func initPrometheus() error {
	defaultClient, err := initPromClient("prometheus")
	if err != nil {
		return err
	}
	promClients[defaultPromEndpoint] = defaultClient
	return nil
}

func queryPrometheus(endpoint, query string, start, end time.Time, step time.Duration) (interface{}, error) {
	client, ok := promClients[endpoint]
	if !ok {
		return nil, fmt.Errorf("prometheus endpoint %q not configured", endpoint)
	}

	ctx := context.Background()
	r := v1.Range{Start: start, End: end, Step: step}

	result, _, err := client.QueryRange(ctx, query, r)
	if err != nil {
		return nil, fmt.Errorf("error querying prometheus (%s): %v", endpoint, err)
	}
	return summarizePromResult(result)
}

func validateAndParseTimeRange(start, end string) (time.Time, time.Time, error) {
	parsed_start, err := parseTime(start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid start time format: %v", err)
	}

	parsed_end := time.Now()
	if end != "" {
		parsed_end, err = parseTime(end)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid end time format: %v", err)
		}
	}

	// Reject future ranges: Prometheus silently returns empty for them, which
	// is indistinguishable from missing data. 30s skew tolerance for clients
	// whose clocks are slightly ahead.
	now := time.Now().UTC()
	const futureSkewTolerance = 30 * time.Second
	if parsed_start.After(now.Add(futureSkewTolerance)) {
		return time.Time{}, time.Time{}, fmt.Errorf(
			"start time %s is in the future; current UTC is %s",
			parsed_start.UTC().Format(time.RFC3339),
			now.Format(time.RFC3339),
		)
	}
	if parsed_end.After(now.Add(futureSkewTolerance)) {
		return time.Time{}, time.Time{}, fmt.Errorf(
			"end time %s is in the future; current UTC is %s",
			parsed_end.UTC().Format(time.RFC3339),
			now.Format(time.RFC3339),
		)
	}

	if parsed_start.After(parsed_end) {
		return time.Time{}, time.Time{}, fmt.Errorf("start time must be before end time")
	}

	return parsed_start, parsed_end, nil
}

func parseTime(timeStr string) (time.Time, error) {
	if strings.HasPrefix(timeStr, "-") {
		d, err := time.ParseDuration(timeStr)
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid relative time: %v", err)
		}
		return time.Now().Add(d), nil
	}
	return time.Parse(time.RFC3339, timeStr)
}

// summarizePromResult processes the raw Prometheus result
func summarizePromResult(result interface{}) (interface{}, error) {
	matrix, ok := result.(model.Matrix)
	if !ok {
		// For non-matrix results (scalar, vector), return as is
		return result, nil
	}

	if len(matrix) == 0 {
		return result, nil
	}

	// For matrix results, just get the last value from each series
	var lastValues []map[string]interface{}
	for _, series := range matrix {
		if len(series.Values) > 0 {
			lastPoint := series.Values[len(series.Values)-1]
			lastValues = append(lastValues, map[string]interface{}{
				"metric": series.Metric,
				"value":  lastPoint.Value,
				"time":   lastPoint.Timestamp.Time(),
			})
		}
	}

	return map[string]interface{}{
		"raw_result":  result,
		"last_values": lastValues,
	}, nil
}

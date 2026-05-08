package routes

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/mc256/honkai-rule-server/internal/fetcher"
	"github.com/mc256/honkai-rule-server/internal/merge"
)

// HealthDeps bundles what the /health handler needs.
type HealthDeps struct {
	Coordinator *fetcher.Coordinator
	Pipeline    *merge.Pipeline
	Logger      *slog.Logger
}

// HealthResponse mirrors contracts/health.openapi.yaml.
type HealthResponse struct {
	Bootstrap      string                 `json:"bootstrap"`
	Sources        []fetcher.SourceState  `json:"sources"`
	DailyAllowance merge.DailyAllowance   `json:"dailyAllowance"`
}

// Health returns an http.HandlerFunc for GET /health. Per FR-015 + the
// contracts file: 200 when bootstrap completed cleanly (or degraded but
// still serving); 503 when warming_up OR any enabled source's bootstrap
// failed.
//
// Not auth-gated in v1: in K8s deployment this endpoint is exposed only on
// the cluster-internal Service, not behind the public Ingress.
func Health(deps HealthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		bootstrap := "warming_up"
		select {
		case <-deps.Coordinator.Ready():
			if deps.Coordinator.AllSucceeded() {
				bootstrap = "succeeded"
			} else {
				bootstrap = "failed"
			}
		default:
		}

		body := HealthResponse{
			Bootstrap:      bootstrap,
			Sources:        deps.Coordinator.SourceStates(),
			DailyAllowance: deps.Pipeline.ComputeDailyAllowance(),
		}

		status := http.StatusOK
		if bootstrap != "succeeded" {
			status = http.StatusServiceUnavailable
		}
		w.WriteHeader(status)

		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(body); err != nil {
			deps.Logger.Warn("encode /health body failed", "err", err.Error())
		}
	}
}

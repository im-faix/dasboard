package health

import (
	"net/http"
	"time"

	"github.com/im-faix/sentinel/backend/internal/api/response"
)

type HealthResponse struct {
	Status    string    `json:"status"`
	Service   string    `json:"service"`
	Version   string    `json:"version"`
	Timestamp time.Time `json:"timestamp"`
}

func Health(w http.ResponseWriter, r *http.Request) {

	resp := HealthResponse{
		Status:    "UP",
		Service:   "Sentinel",
		Version:   "0.1.0-alpha",
		Timestamp: time.Now().UTC(),
	}

	response.JSON(w, http.StatusOK, resp)
}

func Live(w http.ResponseWriter, r *http.Request) {

	response.JSON(w,
		http.StatusOK,
		map[string]string{
			"status": "alive",
		},
	)
}

func Ready(w http.ResponseWriter, r *http.Request) {

	response.JSON(w,
		http.StatusOK,
		map[string]string{
			"status": "ready",
		},
	)
}

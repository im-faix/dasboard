package version

import (
	"net/http"
	"runtime"

	"github.com/im-faix/sentinel/backend/internal/api/response"
)

type VersionResponse struct {
	Application string `json:"application"`
	Version     string `json:"version"`
	GoVersion   string `json:"goVersion"`
}

func Get(w http.ResponseWriter, r *http.Request) {

	resp := VersionResponse{
		Application: "Sentinel",
		Version:     "0.1.0-alpha",
		GoVersion:   runtime.Version(),
	}

	response.JSON(w,
		http.StatusOK,
		resp,
	)
}

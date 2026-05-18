package ipc

import "time"

// StatusResponse is the DTO returned by GET /v1/status (SPEC §10.5).
type StatusResponse struct {
	Version   string          `json:"version"`
	State     string          `json:"state"`
	StartedAt time.Time       `json:"started_at"`
	SQLite    StoreHealth     `json:"sqlite"`
	TSDB      StoreHealth     `json:"tsdb"`
	Scheduler SchedulerStatus `json:"scheduler"`
	Monitors  MonitorCounts   `json:"monitors"`
}

// StoreHealth reports whether a storage backend is healthy.
type StoreHealth struct {
	OK bool `json:"ok"`
}

// SchedulerStatus reports the scheduler's running state and worker count.
type SchedulerStatus struct {
	Running bool `json:"running"`
	Workers int  `json:"workers"`
}

// MonitorCounts reports total and active monitor counts.
type MonitorCounts struct {
	Total  int `json:"total"`
	Active int `json:"active"`
}

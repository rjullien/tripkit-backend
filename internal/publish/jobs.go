package publish

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"

	StageQueued     = "queued"
	StageFetching   = "fetching"
	StageParsing    = "parsing"
	StageValidating = "validating"
	StageApplying   = "applying"
	StageACL        = "acl"
)

// CreateJobRequest is the POST /publish/jobs body.
type CreateJobRequest struct {
	SourceID      string `json:"sourceId"`
	TripID        string `json:"tripId"`
	ExpectedSHA   string `json:"expectedSha"`
	ConfirmCreate bool   `json:"confirmCreate"`
}

// JobView is the API representation of a publish job.
type JobView struct {
	ID          string          `json:"id"`
	SourceID    string          `json:"sourceId"`
	TripID      string          `json:"tripId"`
	SeedPath    string          `json:"seedPath"`
	Operation   string          `json:"operation"`
	Status      string          `json:"status"`
	Stage       string          `json:"stage"`
	Progress    int             `json:"progress"`
	RequestedBy string          `json:"requestedBy"`
	ExpectedSHA string          `json:"expectedSha,omitempty"`
	GitSHA      string          `json:"gitSha,omitempty"`
	Errors      []JobError      `json:"errors"`
	Warnings    []string        `json:"warnings"`
	Summary     json.RawMessage `json:"summary,omitempty"`
	DataVersion *int64          `json:"dataVersion,omitempty"`
	ErrorCode   string          `json:"errorCode,omitempty"`
	CreatedAt   time.Time       `json:"createdAt"`
	StartedAt   *time.Time      `json:"startedAt,omitempty"`
	CompletedAt *time.Time      `json:"completedAt,omitempty"`
}

// JobError is a structured publish/QA error.
type JobError struct {
	Code    string `json:"code"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// CreateJob enqueues a publish job after authz checks by the caller.
func CreateJob(db *gorm.DB, reg *Registry, req CreateJobRequest, username string, isAdmin bool) (*models.PublishJob, error) {
	req.SourceID = strings.TrimSpace(req.SourceID)
	req.TripID = strings.TrimSpace(req.TripID)
	if req.SourceID == "" || req.TripID == "" {
		return nil, fmt.Errorf("sourceId and tripId required")
	}
	if strings.TrimSpace(os.Getenv("TRIPKIT_GITHUB_TOKEN")) == "" {
		return nil, ErrNoGitHubToken
	}
	if !reg.CanPublish(req.SourceID, username, isAdmin) {
		return nil, ErrForbidden
	}
	src, _ := reg.Get(req.SourceID)
	seed, ok := src.FindSeed(req.TripID)
	if !ok {
		return nil, fmt.Errorf("trip %q not in source %q allowlist", req.TripID, req.SourceID)
	}

	// Active job lock
	var active int64
	db.Model(&models.PublishJob{}).
		Where("source_id = ? AND trip_id = ? AND status IN ?", req.SourceID, req.TripID, []string{StatusQueued, StatusRunning}).
		Count(&active)
	if active > 0 {
		return nil, ErrAlreadyRunning
	}

	var trip models.Trip
	exists := db.First(&trip, "id = ?", req.TripID).Error == nil
	op := "create"
	if exists {
		op = "update"
	} else if !req.ConfirmCreate {
		return nil, ErrConfirmCreate
	}

	job := &models.PublishJob{
		ID:            "pub_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		SourceID:      req.SourceID,
		TripID:        req.TripID,
		SeedPath:      seed.Path,
		Operation:     op,
		Status:        StatusQueued,
		Stage:         StageQueued,
		Progress:      0,
		RequestedBy:   strings.ToLower(username),
		ExpectedSHA:   req.ExpectedSHA,
		ConfirmCreate: req.ConfirmCreate,
		ErrorsJSON:    "[]",
		WarningsJSON:  "[]",
	}
	if err := db.Create(job).Error; err != nil {
		return nil, err
	}
	return job, nil
}

// GetJob loads a job by id.
func GetJob(db *gorm.DB, id string) (*models.PublishJob, error) {
	var job models.PublishJob
	if err := db.First(&job, "id = ?", id).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

// ToView converts a DB job to API JSON.
func ToView(job *models.PublishJob) JobView {
	var errs []JobError
	_ = json.Unmarshal([]byte(job.ErrorsJSON), &errs)
	if errs == nil {
		errs = []JobError{}
	}
	var warns []string
	_ = json.Unmarshal([]byte(job.WarningsJSON), &warns)
	if warns == nil {
		warns = []string{}
	}
	var summary json.RawMessage
	if job.SummaryJSON != "" {
		summary = json.RawMessage(job.SummaryJSON)
	}
	return JobView{
		ID:          job.ID,
		SourceID:    job.SourceID,
		TripID:      job.TripID,
		SeedPath:    job.SeedPath,
		Operation:   job.Operation,
		Status:      job.Status,
		Stage:       job.Stage,
		Progress:    job.Progress,
		RequestedBy: job.RequestedBy,
		ExpectedSHA: job.ExpectedSHA,
		GitSHA:      job.GitSHA,
		Errors:      errs,
		Warnings:    warns,
		Summary:     summary,
		DataVersion: job.DataVersion,
		ErrorCode:   job.ErrorCode,
		CreatedAt:   job.CreatedAt,
		StartedAt:   job.StartedAt,
		CompletedAt: job.CompletedAt,
	}
}

// Sentinel errors mapped by handlers.
var (
	ErrForbidden      = fmt.Errorf("forbidden")
	ErrAlreadyRunning = fmt.Errorf("already_running")
	ErrConfirmCreate  = fmt.Errorf("confirm_create_required")
	ErrSourceChanged  = fmt.Errorf("source_changed")
	ErrNoGitHubToken  = fmt.Errorf("TRIPKIT_GITHUB_TOKEN not configured")
)

package publish

import (
	"encoding/json"
	"log"
	"path"
	"strings"
	"time"

	"github.com/rjullien/tripkit-backend/internal/models"
	"gorm.io/gorm"
)

// Worker processes queued publish jobs in-process (progressive V1).
type Worker struct {
	DB      *gorm.DB
	Reg     *Registry
	GitHub  *GitHubClient
	Log     *log.Logger
	Every   time.Duration
	stopCh  chan struct{}
}

// Start launches the poll loop.
func (w *Worker) Start() {
	if w.Every <= 0 {
		w.Every = 2 * time.Second
	}
	if w.Log == nil {
		w.Log = log.Default()
	}
	if w.GitHub == nil {
		w.GitHub = NewGitHubClientFromEnv()
	}
	w.stopCh = make(chan struct{})
	go func() {
		t := time.NewTicker(w.Every)
		defer t.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-t.C:
				w.tick()
			}
		}
	}()
	w.Log.Println("publish worker started")
}

// Stop stops the poll loop.
func (w *Worker) Stop() {
	if w.stopCh != nil {
		close(w.stopCh)
	}
}

func (w *Worker) tick() {
	var job models.PublishJob
	err := w.DB.Where("status = ?", StatusQueued).Order("created_at asc").First(&job).Error
	if err != nil {
		return
	}
	w.Process(&job)
}

// Process runs one job to completion.
func (w *Worker) Process(job *models.PublishJob) {
	now := time.Now()
	job.Status = StatusRunning
	job.StartedAt = &now
	job.Stage = StageFetching
	job.Progress = 10
	w.DB.Save(job)

	fail := func(code string, errs []JobError) {
		b, _ := json.Marshal(errs)
		done := time.Now()
		job.Status = StatusFailed
		job.ErrorCode = code
		job.ErrorsJSON = string(b)
		job.CompletedAt = &done
		w.DB.Save(job)
	}

	src, ok := w.Reg.Get(job.SourceID)
	if !ok || !src.Enabled {
		fail("source_disabled", []JobError{{Code: "source_disabled", Message: "source not enabled"}})
		return
	}
	seedRef, ok := src.FindSeed(job.TripID)
	if !ok {
		fail("seed_not_found", []JobError{{Code: "seed_not_found", Message: "trip not in allowlist"}})
		return
	}

	paths := []string{seedRef.Path, "people.js", "checklist-config.js"}
	paths = append(paths, seedRef.Assets...)

	zipBytes, sha, err := w.GitHub.FetchRepoZip(src.Repo, src.Ref)
	if err != nil {
		fail("fetch_failed", []JobError{{Code: "fetch_failed", Message: err.Error()}})
		return
	}
	if job.ExpectedSHA != "" && sha != "" && !strings.HasPrefix(sha, job.ExpectedSHA) && sha != job.ExpectedSHA {
		fail("source_changed", []JobError{{Code: "source_changed", Message: "git sha changed since preview", Path: sha}})
		return
	}
	job.GitSHA = sha
	job.Stage = StageParsing
	job.Progress = 35
	w.DB.Save(job)

	tree, err := ExtractAllowlisted(zipBytes, paths)
	if err != nil {
		fail("extract_failed", []JobError{{Code: "extract_failed", Message: err.Error()}})
		return
	}

	seed, err := ParseSeedFile(string(tree[seedRef.Path]))
	if err != nil {
		fail("parse_failed", []JobError{{Code: "parse_failed", Message: err.Error(), Path: seedRef.Path}})
		return
	}
	people, err := ParsePeopleFile(string(tree["people.js"]))
	if err != nil {
		fail("parse_failed", []JobError{{Code: "parse_failed", Message: err.Error(), Path: "people.js"}})
		return
	}
	cfg, err := ParseChecklistConfig(string(tree["checklist-config.js"]))
	if err != nil {
		fail("parse_failed", []JobError{{Code: "parse_failed", Message: err.Error(), Path: "checklist-config.js"}})
		return
	}

	job.Stage = StageValidating
	job.Progress = 55
	w.DB.Save(job)

	if verrs := StructuralValidate(seed, job.TripID, src.ExpectedFamily, cfg.Family); len(verrs) > 0 {
		var je []JobError
		for _, m := range verrs {
			je = append(je, JobError{Code: "qa_failed", Message: m})
		}
		fail("qa_failed", je)
		return
	}

	var assets []AssetFile
	for _, name := range seedRef.Assets {
		data := tree[name]
		assets = append(assets, AssetFile{
			Filename:    path.Base(name),
			ContentType: guessContentType(name),
			Data:        data,
		})
	}

	payload, err := BuildCanonical(seed, people, cfg.Family, src.ID, sha, assets)
	if err != nil {
		fail("build_failed", []JobError{{Code: "build_failed", Message: err.Error()}})
		return
	}

	job.Stage = StageApplying
	job.Progress = 75
	w.DB.Save(job)

	result, err := ApplyCanonical(w.DB, payload, src.OwnerLogins)
	if err != nil {
		fail("apply_failed", []JobError{{Code: "apply_failed", Message: err.Error()}})
		return
	}

	job.Stage = StageACL
	job.Progress = 95
	summary, _ := json.Marshal(map[string]any{
		"created":    result.Created,
		"days":       result.Days,
		"hotels":     result.Hotels,
		"lists":      result.Lists,
		"assets":     result.Assets,
		"aclMembers": result.ACLMembers,
		"groupId":    result.GroupID,
		"gitSha":     sha,
	})
	done := time.Now()
	job.Status = StatusSucceeded
	job.Progress = 100
	job.SummaryJSON = string(summary)
	job.DataVersion = &result.DataVersion
	job.CompletedAt = &done
	job.ErrorsJSON = "[]"
	w.DB.Save(job)
	w.Log.Printf("publish job %s succeeded trip=%s sha=%s", job.ID, job.TripID, sha)
}

func guessContentType(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".svg":
		return "image/svg+xml"
	default:
		return "application/octet-stream"
	}
}

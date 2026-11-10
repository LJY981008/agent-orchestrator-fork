package ladologwatch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// The API methods of the log watch (controllers.LadoLogWatchService). They
// return apierr errors with the codes the controller documents.

// The error codes of the API methods.
const (
	codeInvalid           = "LADO_LOG_WATCH_INVALID"
	codeCertInvalid       = "LADO_LOG_CERT_INVALID"
	codeCertKeyEncrypted  = "LADO_LOG_CERT_KEY_ENCRYPTED"
	codeSourceNotFound    = "LADO_LOG_SOURCE_NOT_FOUND"
	codeFingerprintAbsent = "LADO_LOG_FINGERPRINT_NOT_FOUND"
	codeCINotFound        = "LADO_LOG_CI_NOT_FOUND"
	codeCIUnsupported     = "LADO_LOG_CI_UNSUPPORTED"
	codeFailed            = "LADO_LOG_WATCH_FAILED"
)

// serviceCIFile is the file ImportFromCI reads in each repository folder.
const serviceCIFile = ".gitlab-ci.yml"

// serviceCIMaxFile caps a .gitlab-ci.yml ImportFromCI reads.
const serviceCIMaxFile = 256 << 10

// failed is a 500 LADO_LOG_WATCH_FAILED. The cause goes to the log only: a
// store or file error may name a path.
func (s *Service) failed(what string, err error) error {
	s.deps.Log.Warn("lado log watch: "+what, "err", err)
	return apierr.New(apierr.KindInternal, codeFailed, "로그 감시를 처리하지 못했습니다: "+what, nil)
}

// settings reads a project's settings, or the defaults of one that never
// saved any.
func (s *Service) settings(ctx context.Context, project domain.ProjectID) (domain.LadoLogWatchSettings, error) {
	settings, ok, err := s.deps.Store.LadoLogWatchSettings(ctx, project)
	if err != nil {
		return domain.LadoLogWatchSettings{}, s.failed("설정 읽기", err)
	}
	if !ok {
		return domain.DefaultLadoLogWatchSettings(project), nil
	}
	settings.ProjectID = project
	return settings, nil
}

// Settings is a project's log watch settings with what the runner knows of
// its sources. A project that never saved any gets the defaults.
func (s *Service) Settings(ctx context.Context, project domain.ProjectID) (domain.LadoLogWatchView, error) {
	settings, err := s.settings(ctx, project)
	if err != nil {
		return domain.LadoLogWatchView{}, err
	}
	return s.view(ctx, settings), nil
}

func (s *Service) view(ctx context.Context, settings domain.LadoLogWatchSettings) domain.LadoLogWatchView {
	project := settings.ProjectID
	on, _ := s.ladoMode(ctx, project)
	view := domain.LadoLogWatchView{
		Settings:  settings,
		LadoMode:  on,
		TLSStored: make(map[domain.LadoLogSourceID]domain.LadoLogTLSStored, len(settings.Sources)),
		Statuses:  make([]domain.LadoLogSourceStatus, 0, len(settings.Sources)),
	}
	watching := settings.Enabled && on
	for _, src := range settings.Sources {
		var stored domain.LadoLogTLSStored
		if s.deps.Certs != nil {
			stored.CA, stored.Cert, stored.Key = s.deps.Certs.Stored(serviceCertKey(project, src))
		}
		view.TLSStored[src.ID] = stored
		view.Statuses = append(view.Statuses, s.sourceStatus(project, src, watching))
	}
	return view
}

// UpdateSettings validates and saves a project's settings. New sources and
// targets get ids; the TLS copies of sources the settings no longer have are
// deleted. The runner picks the change up at once.
func (s *Service) UpdateSettings(ctx context.Context, project domain.ProjectID, in domain.LadoLogWatchInput) (domain.LadoLogWatchView, error) {
	if err := domain.ValidateLadoLogWatchInput(in); err != nil {
		return domain.LadoLogWatchView{}, serviceInvalid(err)
	}
	current, err := s.settings(ctx, project)
	if err != nil {
		return domain.LadoLogWatchView{}, err
	}
	known := make(map[domain.LadoLogSourceID]domain.LadoLogSource, len(current.Sources))
	for _, src := range current.Sources {
		known[src.ID] = src
	}
	next := current
	next.ProjectID = project
	next.Enabled = in.Enabled
	next.Repository = in.Repository
	next.Include = append([]string{}, in.Include...)
	next.Exclude = append([]string{}, in.Exclude...)
	next.HourlyIssueCap = in.HourlyIssueCap
	next.AutoPlan = in.AutoPlan
	next.AutoPlanDailyCap = in.AutoPlanDailyCap
	next.AutoPlanMinCount = in.AutoPlanMinCount
	next.DisconnectAlertMinutes = in.DisconnectAlertMinutes
	next.UpdatedAt = s.deps.Clock()
	next.Sources = make([]domain.LadoLogSource, 0, len(in.Sources))
	usedSources := map[domain.LadoLogSourceID]bool{}
	for _, srcIn := range in.Sources {
		old, existing := known[srcIn.ID]
		// An id the project does not have (or one given twice) is a new
		// source: the server gives ids.
		if !existing || usedSources[srcIn.ID] {
			old, existing = domain.LadoLogSource{ID: domain.LadoLogSourceID(s.deps.NewID())}, false
		}
		usedSources[old.ID] = true
		src := old
		src.Kind = srcIn.Kind
		if src.Kind == "" {
			src.Kind = domain.LadoLogSourceKindDockerTCP
		}
		src.DisplayName = srcIn.DisplayName
		src.Endpoint = srcIn.Endpoint
		src.TLS = srcIn.TLS
		src.Paused = srcIn.Paused
		if !existing || src.CertKey == "" {
			src.CertKey = CertName(string(project), string(src.ID))
		}
		knownTargets := map[domain.LadoLogTargetID]bool{}
		for _, t := range old.Targets {
			knownTargets[t.ID] = true
		}
		usedTargets := map[domain.LadoLogTargetID]bool{}
		src.Targets = make([]domain.LadoLogTarget, 0, len(srcIn.Targets))
		for _, tIn := range srcIn.Targets {
			id := tIn.ID
			if !knownTargets[id] || usedTargets[id] {
				id = domain.LadoLogTargetID(s.deps.NewID())
			}
			usedTargets[id] = true
			src.Targets = append(src.Targets, domain.LadoLogTarget{
				ID: id, SourceID: src.ID, Kind: tIn.Kind, Value: tIn.Value,
				ComposeProject: tIn.ComposeProject, Service: tIn.Service, Paused: tIn.Paused,
			})
		}
		next.Sources = append(next.Sources, src)
	}
	removed, err := s.deps.Store.PutLadoLogWatchSettings(ctx, next)
	if err != nil {
		return domain.LadoLogWatchView{}, s.failed("설정 저장", err)
	}
	for _, id := range removed {
		src, ok := known[id]
		if !ok || s.deps.Certs == nil {
			continue
		}
		if err := s.deps.Certs.Delete(serviceCertKey(project, src)); err != nil {
			s.deps.Log.Warn("lado log watch: delete TLS copies of a removed source", "source", id, "err", err)
		}
	}
	s.Kick()
	return s.Settings(ctx, project)
}

// serviceInvalid turns a refused input into LADO_LOG_WATCH_INVALID.
func serviceInvalid(err error) error {
	var invalid *domain.LadoLogWatchInvalidError
	if errors.As(err, &invalid) {
		return apierr.Invalid(codeInvalid, invalid.Message, map[string]any{"field": invalid.Field})
	}
	return apierr.Invalid(codeInvalid, err.Error(), nil)
}

// source finds a saved source of a project.
func (s *Service) source(ctx context.Context, project domain.ProjectID, id domain.LadoLogSourceID) (domain.LadoLogSource, error) {
	settings, err := s.settings(ctx, project)
	if err != nil {
		return domain.LadoLogSource{}, err
	}
	for _, src := range settings.Sources {
		if src.ID == id {
			return src, nil
		}
	}
	return domain.LadoLogSource{}, apierr.NotFound(codeSourceNotFound, "로그 출처를 찾지 못했습니다")
}

// ImportTLS copies a source's CA and, optionally, its client certificate
// and key under ~/.ao after checking them. The paths are only read; neither
// they nor the files' content go back out.
func (s *Service) ImportTLS(ctx context.Context, project domain.ProjectID, source domain.LadoLogSourceID, in domain.LadoLogTLSImport) (domain.LadoLogWatchView, error) {
	src, err := s.source(ctx, project, source)
	if err != nil {
		return domain.LadoLogWatchView{}, err
	}
	if s.deps.Certs == nil {
		return domain.LadoLogWatchView{}, s.failed("인증서 저장소 없음", errors.New("no certificate store"))
	}
	key := serviceCertKey(project, src)
	if err := s.deps.Certs.Import(key, in.CAPath, in.CertPath, in.KeyPath); err != nil {
		return domain.LadoLogWatchView{}, s.certError(err)
	}
	if src.CertKey != key {
		if err := s.deps.Store.SetLadoLogSourceCertKey(ctx, source, key); err != nil {
			return domain.LadoLogWatchView{}, s.failed("인증서 기록", err)
		}
	}
	s.restartSource(source)
	return s.Settings(ctx, project)
}

// certError gives a refused TLS file its code. The CertStore's
// messages never name a path.
func (s *Service) certError(err error) error {
	switch {
	case errors.Is(err, ErrCertKeyEncrypted):
		return apierr.Invalid(codeCertKeyEncrypted, "암호가 걸린 개인 키는 쓸 수 없습니다. 암호를 푼 키를 가져오세요", nil)
	case errors.Is(err, ErrCertInvalid):
		return apierr.Invalid(codeCertInvalid, err.Error(), nil)
	}
	return s.failed("인증서 가져오기", err)
}

// DeleteTLS deletes LADO's copies of a source's TLS files.
func (s *Service) DeleteTLS(ctx context.Context, project domain.ProjectID, source domain.LadoLogSourceID) (domain.LadoLogWatchView, error) {
	src, err := s.source(ctx, project, source)
	if err != nil {
		return domain.LadoLogWatchView{}, err
	}
	if s.deps.Certs != nil {
		if err := s.deps.Certs.Delete(serviceCertKey(project, src)); err != nil {
			return domain.LadoLogWatchView{}, s.failed("인증서 삭제", err)
		}
	}
	s.restartSource(source)
	return s.Settings(ctx, project)
}

// restartSource makes a source's Follower start over with its new TLS
// copies.
func (s *Service) restartSource(id domain.LadoLogSourceID) {
	s.mu.Lock()
	s.restart[id] = true
	s.mu.Unlock()
	s.Kick()
}

// Check connects to a saved source: the Engine's versions and the instances
// each of its targets selects, paused ones included. A failed connection is
// OK false with the reason.
func (s *Service) Check(ctx context.Context, project domain.ProjectID, source domain.LadoLogSourceID) (domain.LadoLogWatchCheck, error) {
	src, err := s.source(ctx, project, source)
	if err != nil {
		return domain.LadoLogWatchCheck{}, err
	}
	cfg, _, detail := s.sourceConfig(project, src, true)
	if detail != "" {
		return domain.LadoLogWatchCheck{OK: false, Detail: detail, Insecure: !src.TLS}, nil
	}
	cctx, cancel := context.WithTimeout(ctx, serviceCheckTimeout)
	defer cancel()
	found, err := s.deps.Check(cctx, cfg)
	if err != nil {
		return domain.LadoLogWatchCheck{OK: false, Detail: dockerClip(err.Error(), serviceMaxDetail), Insecure: !src.TLS}, nil
	}
	check := domain.LadoLogWatchCheck{
		OK:            true,
		EngineVersion: found.EngineVersion,
		APIVersion:    found.APIVersion,
		Insecure:      found.Insecure || !src.TLS,
		Instances:     make([]domain.LadoLogInstance, 0, len(found.Instances)),
	}
	for _, inst := range found.Instances {
		check.Instances = append(check.Instances, domain.LadoLogInstance{
			TargetID: inst.TargetID, Name: inst.Name, ID: inst.ID, Image: inst.Image, Service: inst.Service,
			// Discover lists running containers only.
			State: "running",
		})
	}
	return check, nil
}

// ImportFromCI reads the .gitlab-ci.yml of the project's folder, and of
// each repository folder of a workspace project, and returns the Docker
// hosts and compose projects it finds there. It saves nothing.
func (s *Service) ImportFromCI(ctx context.Context, project domain.ProjectID) (domain.LadoLogCIImport, error) {
	if s.deps.Projects == nil {
		return domain.LadoLogCIImport{}, apierr.Conflict(codeCIUnsupported, "이 프로젝트의 폴더를 알 수 없습니다", nil)
	}
	records, err := s.deps.Projects.ListProjects(ctx)
	if err != nil {
		return domain.LadoLogCIImport{}, s.failed("프로젝트 읽기", err)
	}
	var record domain.ProjectRecord
	found := false
	for _, p := range records {
		if domain.ProjectID(p.ID) == project {
			record, found = p, true
			break
		}
	}
	switch {
	case !found:
		return domain.LadoLogCIImport{}, apierr.NotFound("PROJECT_NOT_FOUND", "Unknown project")
	case !record.ArchivedAt.IsZero():
		return domain.LadoLogCIImport{}, apierr.Conflict(codeCIUnsupported, "보관된 프로젝트입니다", nil)
	case record.Kind.WithDefault() == domain.ProjectKindScratch:
		return domain.LadoLogCIImport{}, apierr.Conflict(codeCIUnsupported, "저장소가 없는 프로젝트입니다", nil)
	case record.Path == "":
		return domain.LadoLogCIImport{}, apierr.Conflict(codeCIUnsupported, "프로젝트 폴더가 없습니다", nil)
	}
	if info, err := os.Stat(record.Path); err != nil || !info.IsDir() {
		return domain.LadoLogCIImport{}, apierr.Conflict(codeCIUnsupported, "프로젝트 폴더가 없습니다", nil)
	}
	type folder struct{ repo, dir string }
	folders := []folder{{repo: "", dir: record.Path}}
	if record.Kind.WithDefault() == domain.ProjectKindWorkspace {
		repos, err := s.deps.Projects.ListWorkspaceRepos(ctx, record.ID)
		if err != nil {
			return domain.LadoLogCIImport{}, s.failed("워크스페이스 저장소 읽기", err)
		}
		sort.Slice(repos, func(i, j int) bool { return repos[i].RelativePath < repos[j].RelativePath })
		for _, repo := range repos {
			rel := filepath.FromSlash(repo.RelativePath)
			if rel == "" || rel == "." || !filepath.IsLocal(rel) {
				continue
			}
			folders = append(folders, folder{repo: repo.RelativePath, dir: filepath.Join(record.Path, rel)})
		}
	}
	out := domain.LadoLogCIImport{Files: []domain.LadoLogCIFile{}}
	for _, f := range folders {
		data, note, ok := serviceReadCI(filepath.Join(f.dir, serviceCIFile))
		if !ok {
			continue
		}
		file := domain.LadoLogCIFile{Repo: f.repo, Path: serviceCIFile, Hosts: []domain.LadoLogCIHost{}, ComposeProjects: []string{}, Notes: []string{}}
		if note != "" {
			file.Notes = append(file.Notes, note)
		} else {
			c := ParseGitLabCI(data)
			for _, h := range c.Hosts {
				file.Hosts = append(file.Hosts, domain.LadoLogCIHost{
					Variable: h.Variable, DisplayName: h.DisplayName, Endpoint: h.Endpoint, Unresolved: h.Unresolved,
				})
			}
			file.ComposeProjects = append(file.ComposeProjects, c.ComposeProjects...)
			file.Notes = append(file.Notes, c.Notes...)
		}
		out.Files = append(out.Files, file)
	}
	if len(out.Files) == 0 {
		return domain.LadoLogCIImport{}, apierr.NotFound(codeCINotFound, "프로젝트 폴더에 .gitlab-ci.yml 이 없습니다")
	}
	return out, nil
}

// serviceReadCI reads a .gitlab-ci.yml without following a symbolic link.
// ok is false when there is no such file; note says why a file that is
// there was not read.
func serviceReadCI(path string) (data []byte, note string, ok bool) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", false
	}
	if !info.Mode().IsRegular() {
		return nil, ".gitlab-ci.yml 이 일반 파일이 아니라(심볼릭 링크 등) 읽지 않았습니다.", true
	}
	if info.Size() > serviceCIMaxFile {
		return nil, fmt.Sprintf(".gitlab-ci.yml 이 %d KiB 를 넘어 읽지 않았습니다.", serviceCIMaxFile>>10), true
	}
	f, err := os.Open(path) //nolint:gosec // G304: the project's own CI file, only read.
	if err != nil {
		return nil, ".gitlab-ci.yml 을 읽지 못했습니다.", true
	}
	defer func() { _ = f.Close() }()
	data, err = io.ReadAll(io.LimitReader(f, serviceCIMaxFile+1))
	if err != nil {
		return nil, ".gitlab-ci.yml 을 읽지 못했습니다.", true
	}
	if len(data) > serviceCIMaxFile {
		return nil, fmt.Sprintf(".gitlab-ci.yml 이 %d KiB 를 넘어 읽지 않았습니다.", serviceCIMaxFile>>10), true
	}
	return data, "", true
}

// Incidents lists a project's fingerprints: every one an open task leads to,
// and the newest 200 others.
func (s *Service) Incidents(ctx context.Context, project domain.ProjectID) ([]domain.LadoLogFingerprint, error) {
	fps, err := s.deps.Store.ListLadoLogIncidents(ctx, project, serviceIncidentLimit)
	if err != nil {
		return nil, s.failed("사건 읽기", err)
	}
	if fps == nil {
		fps = []domain.LadoLogFingerprint{}
	}
	return fps, nil
}

// SetIgnored puts a fingerprint on the project's ignore list, or takes it
// off. An ignored fingerprint is only counted: it makes no issue.
func (s *Service) SetIgnored(ctx context.Context, project domain.ProjectID, id domain.LadoLogFingerprintID, ignored bool) (domain.LadoLogFingerprint, error) {
	fp, ok, err := s.deps.Store.LadoLogFingerprintByID(ctx, id)
	if err != nil {
		return domain.LadoLogFingerprint{}, s.failed("지문 읽기", err)
	}
	if !ok || fp.ProjectID != project {
		return domain.LadoLogFingerprint{}, apierr.NotFound(codeFingerprintAbsent, "지문을 찾지 못했습니다")
	}
	fp, err = s.deps.Store.SetLadoLogFingerprintIgnored(ctx, id, ignored, s.deps.Clock())
	if errors.Is(err, domain.ErrLadoLogFingerprintNotFound) {
		return domain.LadoLogFingerprint{}, apierr.NotFound(codeFingerprintAbsent, "지문을 찾지 못했습니다")
	}
	if err != nil {
		return domain.LadoLogFingerprint{}, s.failed("무시 목록 저장", err)
	}
	return fp, nil
}

package evaluation

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	sourceFileLimit       = 4096
	sourceByteLimit int64 = 128 << 20
	sourceLineLimit       = 4 << 20
	exportByteLimit       = 16 << 20
)

var publicVersion = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
var sourceIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,159}$`)
var sealIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
var sha256Digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Gather reads only registered sources. It returns an append-only delta; the
// caller commits facts and private bindings together under the store lock.
// An incomplete source never silently becomes an empty, successful collection.
func Gather(ctx context.Context, snap Snapshot, now time.Time) (tasks []Task, events []Event, bindings []Binding, receipt Receipt) {
	g := &gatherer{ctx: ctx, snap: snap, now: now.UTC(), aliases: map[string]Binding{}, taskByRaw: map[string]string{}, allTasks: map[string]Task{}, latest: map[string]Event{}, reviewedSources: map[string]bool{}, seenThisPass: map[string]bool{}, seenSealDigests: map[string]string{}, issues: map[string]bool{}}
	g.receipt = Receipt{Schema: Schema, ExperimentID: snap.Experiment.ID, CollectedAt: g.now, Complete: true, Issues: []Issue{}}
	for _, b := range snap.Bindings {
		g.aliases[b.Kind+"\x00"+b.Key] = b
		if b.Kind == "task" {
			g.taskByRaw[b.Key] = b.ID
		}
	}
	for id, t := range snap.Tasks {
		g.allTasks[id] = t
	}
	for _, e := range snap.Events {
		if old, ok := g.latest[e.SourceID]; !ok || old.Revision < e.Revision {
			g.latest[e.SourceID] = e
		}
	}
	for _, incident := range latestReviews(snap).incidents {
		if !incident.Active {
			continue
		}
		if _, knownTask := snap.Tasks[incident.TaskID]; !knownTask {
			continue
		}
		for _, eventID := range incident.EventIDs {
			if event, exists := snap.Events[eventID]; exists {
				g.reviewedSources[eventSource(event)] = true
			}
		}
	}
	for _, repo := range snap.Experiment.Config.Repositories {
		g.alias("repository", filepath.Clean(repo), repo)
	}
	g.collectSessions()
	for _, dir := range snap.Experiment.Config.WardDirs {
		if ctx.Err() != nil {
			break
		}
		g.collectWard(dir)
	}
	for _, repo := range g.sealRepositories() {
		if ctx.Err() != nil {
			break
		}
		g.collectSeal(repo)
	}
	if ctx.Err() != nil {
		g.issue("", "collection_canceled")
	}
	for _, task := range g.tasks {
		if _, ok := snap.Tasks[task.ID]; ok {
			g.receipt.TasksUpdated++
		} else {
			g.receipt.TasksAdded++
		}
	}
	g.receipt.EventsAdded = len(g.events)
	sort.Slice(g.receipt.Issues, func(i, j int) bool {
		a, b := g.receipt.Issues[i], g.receipt.Issues[j]
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.Code < b.Code
	})
	return g.tasks, g.events, g.bindings, g.receipt
}

type gatherer struct {
	ctx             context.Context
	snap            Snapshot
	now             time.Time
	aliases         map[string]Binding
	taskByRaw       map[string]string
	allTasks        map[string]Task
	latest          map[string]Event
	reviewedSources map[string]bool
	seenThisPass    map[string]bool
	seenSealDigests map[string]string
	issues          map[string]bool
	tasks           []Task
	events          []Event
	bindings        []Binding
	receipt         Receipt
	bytesRead       int64
	filesRead       int
}

func (g *gatherer) issue(source, code string) {
	k := source + "\x00" + code
	if !g.issues[k] {
		g.issues[k] = true
		g.receipt.Issues = append(g.receipt.Issues, Issue{SourceID: source, Code: code})
	}
	g.receipt.Complete = false
}
func (g *gatherer) alias(kind, key, reference string) string {
	k := kind + "\x00" + key
	if b, ok := g.aliases[k]; ok {
		return b.ID
	}
	id, err := NewID()
	if err != nil {
		g.issue("", "random_id_unavailable")
		return ""
	}
	b := Binding{ID: id, Kind: kind, Key: key, Reference: reference}
	g.aliases[k] = b
	g.bindings = append(g.bindings, b)
	return id
}
func (g *gatherer) addEvent(kind, key, reference string, e Event) {
	e.SourceID = g.alias(kind, key, reference)
	if e.SourceID == "" {
		return
	}
	e.ObservedAt = time.Time{}
	if e.SourceTime != nil {
		canonical := e.SourceTime.UTC()
		e.SourceTime = &canonical
	}
	e.ID = ""
	e.Revision = 0
	e.Fingerprint = ""
	data, err := json.Marshal(e)
	if err != nil {
		g.issue(e.SourceID, "invalid_normalized_event")
		return
	}
	hash := sha256.Sum256(data)
	e.Fingerprint = hex.EncodeToString(hash[:])
	seenThisPass := g.seenThisPass[e.SourceID]
	g.seenThisPass[e.SourceID] = true
	e.Revision = 1
	if old, ok := g.latest[e.SourceID]; ok {
		// Only the latest snapshot determines whether this is unchanged. A
		// previously seen value can legitimately return after a later revision.
		if old.Fingerprint == e.Fingerprint {
			g.receipt.Duplicates++
			return
		}
		if kind == "ward_record" && !sameWardFacts(old, e) {
			g.issue(e.SourceID, "ward_record_conflict")
			return
		}
		if seenThisPass {
			g.issue(e.SourceID, "source_conflicting_snapshot")
			return
		}
		e.Revision = old.Revision + 1
	}
	e.ID, err = NewID()
	if err != nil {
		g.issue(e.SourceID, "random_id_unavailable")
		return
	}
	e.ObservedAt = g.now
	g.events = append(g.events, e)
	g.latest[e.SourceID] = e
	g.seenThisPass[e.SourceID] = true
	// Diagnostic correlation hints are not human-confirmed task membership.
	if !g.reviewedSources[e.SourceID] {
		g.receipt.Unmatched++
	}
}

// The root capability prevents a concurrent source-directory replacement from
// escaping the configured tree. Symlink entries are never traversed.
func (g *gatherer) sourceFiles(dir, kind string, recursive bool) (*os.Root, []string, string) {
	source := g.alias(kind, filepath.Clean(dir), dir)
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		g.issue(source, "source_unavailable")
		return nil, nil, source
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		g.issue(source, "source_unavailable")
		return nil, nil, source
	}
	names := []string{}
	g.filesRead = 0
	scanned := 0
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if g.ctx.Err() != nil {
			return g.ctx.Err()
		}
		scanned++
		if scanned > 65536 {
			g.issue(source, "source_scan_limit")
			return fs.SkipAll
		}
		if err != nil {
			g.issue(source, "source_scan_failed")
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			g.issue(source, "source_symlink_rejected")
			return nil
		}
		if d.IsDir() {
			if p != "." && (!recursive || oldSessionPartition(p, g.snap.Experiment.StartedAt)) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".jsonl") {
			names = append(names, p)
		}
		return nil
	})
	if err != nil {
		g.issue(source, "source_scan_failed")
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > sourceFileLimit {
		g.issue(source, "source_file_limit")
		names = names[:sourceFileLimit]
	}
	return root, names, source
}
func (g *gatherer) readLines(root *os.Root, name, source string, fn func([]byte) bool) {
	if g.ctx.Err() != nil {
		return
	}
	if g.filesRead >= sourceFileLimit || g.bytesRead >= sourceByteLimit {
		g.issue(source, "source_read_limit")
		return
	}
	g.filesRead++
	st, err := root.Lstat(name)
	if err != nil || !st.Mode().IsRegular() {
		g.issue(source, "source_file_unavailable")
		return
	}
	f, err := root.Open(name)
	if err != nil {
		g.issue(source, "source_file_unavailable")
		return
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		g.issue(source, "source_changed")
		return
	}
	reader := bufio.NewReaderSize(io.LimitReader(f, sourceByteLimit-g.bytesRead+1), 64<<10)
	for {
		line, err := reader.ReadSlice('\n')
		g.bytesRead += int64(len(line))
		if errors.Is(err, bufio.ErrBufferFull) {
			// Most lines are small, but a first turn can contain a large permission
			// description. Bound memory without decoding unrelated conversation text.
			full := append([]byte(nil), line...)
			for errors.Is(err, bufio.ErrBufferFull) && len(full) <= sourceLineLimit {
				line, err = reader.ReadSlice('\n')
				g.bytesRead += int64(len(line))
				full = append(full, line...)
			}
			line = full
		}
		if len(line) > sourceLineLimit || g.bytesRead > sourceByteLimit {
			g.issue(source, "source_read_limit")
			return
		}
		if len(line) > 0 {
			if errors.Is(err, io.EOF) && line[len(line)-1] != '\n' {
				g.issue(source, "source_truncated_line")
				return
			}
			if !fn(line) {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				g.issue(source, "source_read_failed")
			}
			return
		}
		if g.ctx.Err() != nil {
			return
		}
	}
}

type sessionMeta struct {
	ID             string          `json:"id"`
	Timestamp      string          `json:"timestamp"`
	CWD            string          `json:"cwd"`
	Source         json.RawMessage `json:"source"`
	ParentThreadID string          `json:"parent_thread_id"`
}
type sessionTurn struct {
	Model             string `json:"model"`
	PermissionProfile struct {
		Type string `json:"type"`
		Name string `json:"name"`
	} `json:"permission_profile"`
}
type sessionCandidate struct {
	meta      sessionMeta
	turn      sessionTurn
	hasTurn   bool
	validMeta bool
	file      string
	source    string
}

func (g *gatherer) collectSessions() {
	candidates := []sessionCandidate{}
	for _, dir := range g.snap.Experiment.Config.SessionDirs {
		root, names, source := g.sourceFiles(dir, "session_directory", true)
		if root == nil {
			continue
		}
		for _, name := range names {
			var c sessionCandidate
			c.file = filepath.Join(dir, name)
			c.source = source
			g.readLines(root, name, source, func(line []byte) bool {
				var envelope struct {
					Type    string          `json:"type"`
					Payload json.RawMessage `json:"payload"`
				}
				if json.Unmarshal(line, &envelope) != nil {
					g.issue(source, "session_invalid_json")
					return false
				}
				switch envelope.Type {
				case "session_meta":
					var meta sessionMeta
					if json.Unmarshal(envelope.Payload, &meta) != nil {
						g.issue(source, "session_invalid_metadata")
						return false
					}
					if c.meta.ID != "" {
						// Repeated equivalent metadata is harmless. A different
						// identity can be inherited fork history; its first turn
						// must not be attributed to this session.
						if sameSessionMetadata(c.meta, meta) {
							return true
						}
						g.issue(source, "session_duplicate_metadata")
						return false
					}
					c.meta = meta
					at, err := time.Parse(time.RFC3339Nano, c.meta.Timestamp)
					if err != nil || !sourceIdentifier.MatchString(c.meta.ID) {
						g.issue(source, "session_invalid_metadata")
						return false
					}
					if at.Before(g.snap.Experiment.StartedAt) || at.After(g.now) {
						return false
					}
					c.validMeta = true
				case "turn_context":
					c.hasTurn = json.Unmarshal(envelope.Payload, &c.turn) == nil
					return false
				}
				return true
			})
			if c.validMeta {
				at, err := time.Parse(time.RFC3339Nano, c.meta.Timestamp)
				if err == nil && !at.Before(g.snap.Experiment.StartedAt) && !at.After(g.now) {
					candidates = append(candidates, c)
				}
			}
		}
		root.Close()
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].meta.Timestamp == candidates[j].meta.Timestamp {
			return candidates[i].meta.ID < candidates[j].meta.ID
		}
		return candidates[i].meta.Timestamp < candidates[j].meta.Timestamp
	})
	// Allocate aliases before resolving parents so creation order is irrelevant.
	for _, c := range candidates {
		if repo := g.repoFor(c.meta.CWD); repo != "" {
			g.taskByRaw[c.meta.ID] = g.alias("task", c.meta.ID, "codex://threads/"+c.meta.ID)
		}
	}
	for _, c := range candidates {
		repo := g.repoFor(c.meta.CWD)
		if repo == "" {
			continue
		}
		id := g.taskByRaw[c.meta.ID]
		if id == "" {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, c.meta.Timestamp)
		t := Task{ID: id, Revision: 1, RepositoryID: g.alias("repository", repo, repo), CreatedAt: at.UTC(), Profile: "unknown", Model: "unknown", Kind: "unknown"}
		if c.hasTurn {
			switch c.turn.PermissionProfile.Type {
			case "managed", "disabled", "named":
				t.Profile = c.turn.PermissionProfile.Type
			}
			// A managed sandbox alone is not proof that the named Ward profile ran.
			if c.turn.PermissionProfile.Type == "named" && c.turn.PermissionProfile.Name == "ward" {
				t.Profile = "ward"
			}
			if knownModel(c.turn.Model) {
				t.Model = c.turn.Model
			}
		}
		if t.Profile == "unknown" {
			g.issue(id, "session_first_profile_unknown")
		}
		if t.Profile == "disabled" {
			g.issue(id, "session_unprotected_profile")
		}
		var origin string
		if json.Unmarshal(c.meta.Source, &origin) == nil {
			switch origin {
			case "cli", "vscode", "exec", "app", "codex_desktop":
				t.Kind = "root"
			}
		} else {
			var origin struct {
				Subagent struct {
					ThreadSpawn struct {
						ParentThreadID string `json:"parent_thread_id"`
					} `json:"thread_spawn"`
				} `json:"subagent"`
			}
			if json.Unmarshal(c.meta.Source, &origin) == nil && sourceIdentifier.MatchString(origin.Subagent.ThreadSpawn.ParentThreadID) {
				t.Kind = "child"
				t.ExclusionReason = "child"
				parent := origin.Subagent.ThreadSpawn.ParentThreadID
				if c.meta.ParentThreadID != "" && c.meta.ParentThreadID != parent {
					t.Kind = "unknown"
					t.ExclusionReason = ""
					g.issue(id, "session_parent_conflict")
				} else {
					t.ParentID = g.alias("task", parent, "codex://threads/"+parent)
				}
			}
		}
		if t.Kind == "unknown" {
			g.issue(id, "session_origin_unknown")
		}
		if g.snap.Experiment.Config.SelfRepository != "" && within(g.snap.Experiment.Config.SelfRepository, repo) {
			t.ExclusionReason = "eval_self"
		}
		if old, ok := g.allTasks[id]; ok {
			if old.RepositoryID != t.RepositoryID || !old.CreatedAt.Equal(t.CreatedAt) {
				g.issue(id, "session_identity_changed")
				continue
			}
			merged := old
			if old.Profile == "unknown" && t.Profile != "unknown" {
				merged.Profile = t.Profile
			}
			if old.Model == "unknown" && t.Model != "unknown" {
				merged.Model = t.Model
			}
			if old.Kind == "unknown" && t.Kind != "unknown" {
				merged.Kind = t.Kind
				merged.ParentID = t.ParentID
				merged.ExclusionReason = t.ExclusionReason
			}
			if old.Profile != "unknown" && t.Profile != "unknown" && old.Profile != t.Profile {
				g.issue(id, "session_first_profile_changed")
			}
			if merged == old {
				g.receipt.Duplicates++
				continue
			}
			merged.Revision = old.Revision + 1
			t = merged
		}
		g.tasks = append(g.tasks, t)
		g.allTasks[id] = t
	}
}
func knownModel(s string) bool {
	// Public product identifiers only; custom/provider model paths remain unknown.
	if len(s) > 80 {
		return false
	}
	ok := regexp.MustCompile(`^(gpt-[0-9][a-z0-9.-]*|o[1-9](?:-[a-z0-9.-]+)?)$`).MatchString(s)
	return ok
}
func within(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func (g *gatherer) repoFor(cwd string) string {
	if !filepath.IsAbs(cwd) {
		return ""
	}
	physical, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return ""
	}
	best, registered := "", ""
	for _, repo := range g.snap.Experiment.Config.Repositories {
		resolved, err := filepath.EvalSymlinks(repo)
		if err == nil && within(resolved, physical) && len(resolved) > len(best) {
			best, registered = resolved, repo
		}
	}
	if best == "" {
		return ""
	}
	// Recognize a physically nested checkout without consulting ambient Git state.
	for p := physical; within(best, p); p = filepath.Dir(p) {
		st, err := os.Lstat(filepath.Join(p, ".git"))
		if err == nil && st.Mode()&os.ModeSymlink == 0 && (st.IsDir() || st.Mode().IsRegular()) {
			if p == best {
				return registered
			}
			return p
		}
		if p == best {
			break
		}
	}
	return registered
}

type wardRecord struct {
	Schema     string `json:"schema"`
	ReceivedAt string `json:"received_at"`
	Generation string `json:"collector_generation"`
	Sequence   uint64 `json:"sequence"`
	Event      struct {
		Schema     string   `json:"schema"`
		Version    string   `json:"ward_version"`
		Tool       string   `json:"tool"`
		Stage      string   `json:"stage"`
		Outcome    string   `json:"outcome"`
		DurationUS *float64 `json:"duration_us"`
		SessionID  string   `json:"session_id,omitempty"`
		TurnID     string   `json:"turn_id,omitempty"`
		ToolUseID  string   `json:"tool_use_id,omitempty"`
		RuleID     string   `json:"rule_id,omitempty"`
		ErrorCode  string   `json:"error_code,omitempty"`
		GapCode    string   `json:"gap_code,omitempty"`
	} `json:"event"`
}

func strictJSON(data []byte, target any) error { return DecodeStrict(data, target) }

func (g *gatherer) collectWard(dir string) {
	root, names, source := g.sourceFiles(dir, "ward_directory", false)
	if root == nil {
		return
	}
	defer root.Close()
	if len(g.snap.Receipts) == 0 && g.now.Sub(g.snap.Experiment.StartedAt) > 7*24*time.Hour {
		g.issue(source, "ward_retention_window_exceeded")
	}
	existing := map[string]bool{}
	seqs := map[string][]uint64{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		existing[path] = true
		g.readLines(root, name, source, func(line []byte) bool {
			var r wardRecord
			if strictJSON(line, &r) != nil || r.Schema != "ward-diagnostic-record/v1" || r.Event.Schema != "ward-diagnostic-event/v1" || !sourceIdentifier.MatchString(r.Generation) || r.Sequence == 0 {
				g.issue(source, "ward_invalid_record")
				return true
			}
			at, err := time.Parse(time.RFC3339Nano, r.ReceivedAt)
			if err != nil {
				g.issue(source, "ward_invalid_time")
				return true
			}
			if at.After(g.now) {
				g.issue(source, "ward_future_record")
				return true
			}
			seqs[r.Generation] = append(seqs[r.Generation], r.Sequence)
			if at.Before(g.snap.Experiment.StartedAt) {
				return true
			}
			g.alias("ward_file", path, path)
			if !oneOf(r.Event.Stage, "evaluate", "decode", "adapter", "hook", "input", "output", "normalize") || !oneOf(r.Event.Outcome, "defer", "deny", "error") {
				g.issue(source, "ward_unknown_enum")
				return true
			}
			version := r.Event.Version
			if !publicVersion.MatchString(version) || len(version) > 100 {
				version = "unknown"
				g.issue(source, "ward_invalid_version")
			}
			e := Event{Module: "ward", Version: version, Source: "ward-diagnostic-record/v1", SourceTime: &at, Outcome: r.Event.Outcome, Stage: r.Event.Stage}
			if r.Event.DurationUS != nil && finiteNonnegative(*r.Event.DurationUS) {
				d := *r.Event.DurationUS / 1000
				e.DurationMS = &d
			} else {
				g.issue(source, "ward_invalid_duration")
			}
			e.RuleID = g.wardCode(source, "rule", r.Event.RuleID)
			e.ErrorCode = g.wardCode(source, "error", r.Event.ErrorCode)
			e.GapCode = g.wardCode(source, "gap", r.Event.GapCode)
			if id := g.taskByRaw[r.Event.SessionID]; id != "" {
				if t, ok := g.allTasks[id]; ok {
					e.HintTaskID = id
					e.RepositoryID = t.RepositoryID
				}
			}
			key := filepath.Clean(dir) + "\x00" + r.Generation + "\x00" + strconv.FormatUint(r.Sequence, 10)
			g.addEvent("ward_record", key, path, e)
			return true
		})
	}
	for gen, seq := range seqs {
		sort.Slice(seq, func(i, j int) bool { return seq[i] < seq[j] })
		for i := 1; i < len(seq); i++ {
			if seq[i] > seq[i-1]+1 {
				g.issue(source, "ward_sequence_gap")
				break
			}
		}
		if len(seq) > 0 && seq[0] > 1 {
			prefix := filepath.Clean(dir) + "\x00" + gen + "\x00"
			knownEarlier := false
			for _, b := range g.snap.Bindings {
				if b.Kind == "ward_record" && strings.HasPrefix(b.Key, prefix) {
					n, _ := strconv.ParseUint(strings.TrimPrefix(b.Key, prefix), 10, 64)
					if n < seq[0] {
						knownEarlier = true
						break
					}
				}
			}
			if !knownEarlier {
				g.issue(source, "ward_prefix_unverified")
			}
		}
	}
	for _, b := range g.snap.Bindings {
		if b.Kind == "ward_file" && filepath.Dir(b.Key) == filepath.Clean(dir) && !existing[b.Key] {
			g.issue(source, "ward_source_rotated_or_removed")
		}
	}
}
func (g *gatherer) wardCode(source, kind, value string) string {
	if value == "" {
		return ""
	}
	allowed := map[string]string{
		"rule":  "WARD_DESTRUCTIVE_FILESYSTEM WARD_DESTRUCTIVE_GIT WARD_DESTRUCTIVE_DATABASE WARD_DESTRUCTIVE_INFRASTRUCTURE",
		"error": "invalid_input invalid_request input_read_failed input_too_large decode_failed evaluation_failed internal_error output_failed unsupported_tool context_unavailable policy_unavailable",
		"gap":   "complex_command_wrapper dynamic_interpreter_payload dynamic_shell_word find_command_action inline_shell_input interpreter_payload opaque_command_dispatch unsupported_shell_syntax unsupported_tool unsupported_platform unresolved_path dynamic_path indirect_command shell_parse_failed command_parse_failed",
	}
	if oneOf(value, strings.Fields(allowed[kind])...) {
		return value
	}
	g.issue(source, "ward_unknown_"+kind+"_code")
	return "unknown"
}
func oneOf(s string, choices ...string) bool {
	for _, v := range choices {
		if s == v {
			return true
		}
	}
	return false
}
func finiteNonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

type sealExport struct {
	Schema          string `json:"schema"`
	ExporterVersion string `json:"exporter_version"`
	Complete        bool   `json:"scan_complete"`
	Tasks           []struct {
		TaskID string `json:"task_id"`
	} `json:"tasks"`
	Runs []struct {
		TaskID              string      `json:"task_id"`
		RunID               string      `json:"run_id"`
		RunVersion          *string     `json:"run_version"`
		EvidenceSHA256      string      `json:"evidence_sha256"`
		Timestamp           *string     `json:"timestamp"`
		MechanicalResult    string      `json:"mechanical_result"`
		RequiredChecksPass  bool        `json:"required_checks_pass"`
		ScopePass           bool        `json:"scope_pass"`
		SourceStable        bool        `json:"source_stable_during_checks"`
		ScopeViolationCount int         `json:"scope_violation_count"`
		Checks              []SealCheck `json:"checks"`
		CompletionRecord    Completion  `json:"completion_record"`
	} `json:"runs"`
	Issues []struct {
		Code   string  `json:"code"`
		TaskID *string `json:"task_id"`
		RunID  *string `json:"run_id"`
	} `json:"issues"`
}
type boundedOutput struct {
	data     []byte
	overflow bool
}

func (w *boundedOutput) Write(b []byte) (int, error) {
	n := len(b)
	if len(w.data)+n > exportByteLimit {
		w.overflow = true
		room := exportByteLimit - len(w.data)
		w.data = append(w.data, b[:room]...)
		return n, nil
	}
	w.data = append(w.data, b...)
	return n, nil
}
func (g *gatherer) collectSeal(repo string) {
	source := g.alias("seal_repository", filepath.Clean(repo), repo)
	rootInfo, rootErr := os.Lstat(repo)
	if rootErr != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		g.issue(source, "seal_repository_unavailable")
		return
	}
	st, err := os.Lstat(filepath.Join(repo, ".seal"))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		g.issue(source, "seal_state_unavailable")
		return
	}
	bin := g.snap.Experiment.Config.SealBinary
	if bin == "" || !filepath.IsAbs(bin) {
		g.issue(source, "seal_export_unavailable")
		return
	}
	timeout := g.snap.Experiment.Config.ExportTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	if timeout > 120 {
		timeout = 120
	}
	ctx, cancel := context.WithTimeout(g.ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "run", "export", "--format", "json")
	cmd.Dir = repo
	cmd.WaitDelay = 2 * time.Second
	cmd.Stderr = io.Discard
	var out boundedOutput
	cmd.Stdout = &out
	err = cmd.Run()
	if ctx.Err() != nil {
		g.issue(source, "seal_export_timeout")
		return
	}
	partial := false
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 8 {
			partial = true
		} else {
			g.issue(source, "seal_export_failed")
			return
		}
	}
	if out.overflow {
		g.issue(source, "seal_export_size_limit")
		return
	}
	var export sealExport
	if strictJSON(out.data, &export) != nil || export.Schema != "seal-run-export/v1" || !validSealShape(out.data) {
		g.issue(source, "seal_export_invalid")
		return
	}
	if partial || !export.Complete || len(export.Issues) > 0 {
		g.issue(source, "seal_export_incomplete")
	}
	for _, issue := range export.Issues {
		issueSource := source
		if issue.TaskID != nil && issue.RunID != nil && sealIdentifier.MatchString(*issue.TaskID) && sealIdentifier.MatchString(*issue.RunID) {
			key := filepath.Clean(repo) + "\x00" + *issue.TaskID + "\x00" + *issue.RunID
			issueSource = g.alias("seal_run", key, repo)
		}
		if oneOf(issue.Code, "invalid_identity", "unsafe_entry", "unreadable", "invalid_task", "invalid_evidence", "invalid_completion", "invalid_metric", "scan_limit", "concurrent_change") {
			g.issue(issueSource, "seal_"+issue.Code)
		} else {
			g.issue(issueSource, "seal_unknown_issue")
		}
	}
	if !publicVersion.MatchString(export.ExporterVersion) || len(export.ExporterVersion) > 100 {
		g.issue(source, "seal_invalid_exporter_version")
	}
	for _, task := range export.Tasks {
		if !sealIdentifier.MatchString(task.TaskID) {
			g.issue(source, "seal_invalid_identity")
			continue
		}
		g.alias("seal_task", filepath.Clean(repo)+"\x00"+task.TaskID, repo)
	}
	for _, run := range export.Runs {
		if !sealIdentifier.MatchString(run.TaskID) || !sealIdentifier.MatchString(run.RunID) || !sha256Digest.MatchString(run.EvidenceSHA256) {
			g.issue(source, "seal_invalid_identity")
			continue
		}
		runSource := g.alias("seal_run", filepath.Clean(repo)+"\x00"+run.TaskID+"\x00"+run.RunID, repo)
		// Evidence is immutable for this repository/Task/Run identity. Check
		// before time filtering so a replacement cannot hide the conflict by
		// changing its timestamp. Completion updates with the same digest may
		// still produce an ordinary revision below.
		previousDigest, seen := g.seenSealDigests[runSource]
		if old, ok := g.latest[runSource]; ok {
			previousDigest, seen = old.EvidenceSHA256, true
		}
		if seen && previousDigest != run.EvidenceSHA256 {
			g.issue(runSource, "seal_evidence_conflict")
			continue
		}
		g.seenSealDigests[runSource] = run.EvidenceSHA256
		// The executable reading saved Evidence is not its producer. Current
		// Evidence has no producer version; exporter upgrades must not revise
		// historical events or create execution-version cohorts.
		version := "unknown"
		if run.RunVersion != nil {
			if publicVersion.MatchString(*run.RunVersion) && len(*run.RunVersion) <= 100 {
				version = *run.RunVersion
			} else {
				g.issue(runSource, "seal_invalid_version")
			}
		}
		var at *time.Time
		if run.Timestamp != nil {
			parsed, e := time.Parse(time.RFC3339Nano, *run.Timestamp)
			if e == nil {
				at = &parsed
			}
		}
		if at == nil {
			// A verified run with unavailable time is retained as temporal
			// unknown, never attributed to the post-activation population.
			g.issue(runSource, "seal_invalid_time")
		}
		if at != nil && at.After(g.now) {
			g.issue(source, "seal_future_record")
			continue
		}
		// A new Completion on an old run is relevant after activation. The underlying
		// run is retained as historical context and never counted as a new invocation.
		completedAfterStart := false
		switch run.CompletionRecord.State {
		case "absent", "invalid":
			if run.CompletionRecord.CompletedAt != nil {
				g.issue(runSource, "seal_invalid_completion_record")
				continue
			}
		case "recorded_pass":
			if run.CompletionRecord.CompletedAt == nil {
				g.issue(runSource, "seal_invalid_completion_record")
				continue
			}
			t, e := time.Parse(time.RFC3339Nano, *run.CompletionRecord.CompletedAt)
			if e != nil || t.After(g.now) {
				g.issue(runSource, "seal_invalid_completion_time")
				continue
			}
			completedAfterStart = !t.Before(g.snap.Experiment.StartedAt)
			canonical := t.UTC().Format(time.RFC3339Nano)
			run.CompletionRecord.CompletedAt = &canonical
		default:
			g.issue(runSource, "seal_invalid_completion_record")
			continue
		}
		if at != nil && at.Before(g.snap.Experiment.StartedAt) && !completedAfterStart {
			continue
		}
		if !oneOf(run.MechanicalResult, "pass", "fail") || run.ScopeViolationCount < 0 {
			g.issue(source, "seal_invalid_result")
			continue
		}
		valid := true
		for i := range run.Checks {
			c := &run.Checks[i]
			if c.Index != i {
				valid = false
			}
			if c.DurationSeconds != nil && !finiteNonnegative(*c.DurationSeconds) {
				c.DurationSeconds = nil
				g.issue(source, "seal_invalid_duration")
			}
		}
		if !valid {
			g.issue(source, "seal_invalid_checks")
			continue
		}
		facts := &SealFacts{MechanicalResult: run.MechanicalResult, RequiredChecksPass: run.RequiredChecksPass, ScopePass: run.ScopePass, SourceStableDuringChecks: run.SourceStable, ScopeViolationCount: run.ScopeViolationCount, Checks: run.Checks, CompletionRecord: run.CompletionRecord}
		e := Event{Module: "seal", Version: version, Source: "seal-run-export/v1", RepositoryID: g.alias("repository", filepath.Clean(repo), repo), SourceTime: at, Outcome: strings.ToLower(run.MechanicalResult), Seal: facts, EvidenceSHA256: run.EvidenceSHA256}
		key := filepath.Clean(repo) + "\x00" + run.TaskID + "\x00" + run.RunID
		g.addEvent("seal_run", key, repo, e)
	}
}

// Go's decoder accepts omitted booleans and JSON null as zero values. Enforce
// mandatory exporter fields separately so malformed evidence cannot turn into
// a fabricated failure, success, or zero-second measurement.
func validSealShape(data []byte) bool {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil || len(top) != 6 || !requiredJSON(top, "schema", "exporter_version", "scan_complete", "tasks", "runs", "issues") {
		return false
	}
	var tasks []map[string]json.RawMessage
	if json.Unmarshal(top["tasks"], &tasks) != nil {
		return false
	}
	for _, task := range tasks {
		if len(task) != 1 || !requiredJSON(task, "task_id") {
			return false
		}
	}
	var issues []map[string]json.RawMessage
	if json.Unmarshal(top["issues"], &issues) != nil {
		return false
	}
	for _, issue := range issues {
		if len(issue) != 3 || !requiredJSON(issue, "code") {
			return false
		}
		for _, key := range []string{"task_id", "run_id"} {
			if _, ok := issue[key]; !ok {
				return false
			}
		}
	}
	var runs []map[string]json.RawMessage
	if json.Unmarshal(top["runs"], &runs) != nil {
		return false
	}
	for _, run := range runs {
		if len(run) != 12 || !requiredJSON(run, "task_id", "run_id", "evidence_sha256", "mechanical_result", "required_checks_pass", "scope_pass", "source_stable_during_checks", "scope_violation_count", "checks", "completion_record") {
			return false
		}
		for _, key := range []string{"timestamp", "run_version"} {
			if _, ok := run[key]; !ok {
				return false
			}
		}
		var checks []map[string]json.RawMessage
		if json.Unmarshal(run["checks"], &checks) != nil || len(checks) > 10000 {
			return false
		}
		for _, check := range checks {
			if len(check) != 6 || !requiredJSON(check, "index", "required", "passed", "timed_out") {
				return false
			}
			for _, key := range []string{"exit_code", "duration_seconds"} {
				if _, ok := check[key]; !ok {
					return false
				}
			}
		}
		var completion map[string]json.RawMessage
		if json.Unmarshal(run["completion_record"], &completion) != nil || len(completion) != 2 || !requiredJSON(completion, "state") {
			return false
		}
		if _, ok := completion["completed_at"]; !ok {
			return false
		}
	}
	return true
}
func requiredJSON(fields map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		value, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	return true
}

// Native session partitions are date directories. Keep an extra UTC day around
// activation for local-time naming; metadata timestamps remain authoritative.
func oldSessionPartition(path string, start time.Time) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 1 || len(parts) > 3 || len(parts[0]) != 4 {
		return false
	}
	layout := "2006"
	text := parts[0]
	if len(parts) >= 2 {
		if len(parts[1]) != 2 {
			return false
		}
		layout += "/01"
		text += "/" + parts[1]
	}
	if len(parts) >= 3 {
		if len(parts[2]) != 2 {
			return false
		}
		layout += "/02"
		text += "/" + parts[2]
	}
	at, err := time.Parse(layout, text)
	if err != nil {
		return false
	}
	cutoff := start.UTC().Add(-24 * time.Hour)
	switch len(parts) {
	case 1:
		return at.AddDate(1, 0, 0).Before(cutoff)
	case 2:
		return at.AddDate(0, 1, 0).Before(cutoff)
	default:
		return at.AddDate(0, 0, 1).Before(cutoff)
	}
}

func (g *gatherer) sealRepositories() []string {
	repos := append([]string(nil), g.snap.Experiment.Config.Repositories...)
	seen := map[string]bool{}
	for _, repo := range repos {
		seen[repo] = true
	}
	for _, binding := range g.aliases {
		if binding.Kind != "repository" || seen[binding.Key] {
			continue
		}
		// These aliases come only from session metadata discovered within registered
		// trees. Revalidate the exact checkout on every pass before starting export.
		physical, err := filepath.EvalSymlinks(binding.Key)
		if err != nil || physical != binding.Key || g.repoFor(physical) != physical {
			g.issue(binding.ID, "nested_checkout_unavailable")
			continue
		}
		git, err := os.Lstat(filepath.Join(physical, ".git"))
		if err != nil || git.Mode()&os.ModeSymlink != 0 || (!git.IsDir() && !git.Mode().IsRegular()) {
			g.issue(binding.ID, "nested_checkout_unavailable")
			continue
		}
		seen[binding.Key] = true
		repos = append(repos, binding.Key)
	}
	sort.Strings(repos)
	return repos
}

// Ward generation/sequence identifies an immutable diagnostic fact. A later
// exact task correlation may refine its hint without rewriting that fact.
func sameWardFacts(a, b Event) bool {
	for _, e := range []*Event{&a, &b} {
		e.ID = ""
		e.Revision = 0
		e.ObservedAt = time.Time{}
		e.Fingerprint = ""
		e.HintTaskID = ""
		e.RepositoryID = ""
	}
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}

func sameSessionMetadata(a, b sessionMeta) bool {
	if a.ID != b.ID || a.Timestamp != b.Timestamp || a.CWD != b.CWD || a.ParentThreadID != b.ParentThreadID {
		return false
	}
	var first, second any
	if DecodeStrict(a.Source, &first) != nil || DecodeStrict(b.Source, &second) != nil {
		return false
	}
	x, errX := json.Marshal(first)
	y, errY := json.Marshal(second)
	return errX == nil && errY == nil && bytes.Equal(x, y)
}

package evaluation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"time"

	"github.com/jgoneit/eval/internal/store"
)

var (
	ErrInvalid = errors.New("invalid evaluation data")
	ErrFull    = errors.New("experiment storage limit reached")
	idPattern  = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)
)

func NewID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// DecodeStrict rejects unknown fields, duplicate keys and trailing values.
func DecodeStrict(data []byte, target any) error {
	if int64(len(data)) > MaxBytes {
		return ErrFull
	}
	d := json.NewDecoder(bytes.NewReader(data))
	// This pass checks structure only. Converting numeric tokens to float64
	// would reject valid arbitrary-precision historical Seal exit codes.
	d.UseNumber()
	var walk func() error
	walk = func() error {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return e
				}
				key, ok := k.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if e = walk(); e != nil {
					return e
				}
			}
		case '[':
			for d.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		default:
			return ErrInvalid
		}
		_, err = d.Token()
		return err
	}
	if err := walk(); err != nil {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return ErrInvalid
	}
	return nil
}

type Manager struct {
	Root    string
	Options store.Options
}

func (m Manager) stores(id string) (*store.Store, *store.Store, error) {
	if !idPattern.MatchString(id) {
		return nil, nil, ErrInvalid
	}
	options := m.Options
	options.MaxBytes = MaxBytes
	base := "jgoneit/eval-experiment/v2/" + id + "/"
	journal, err := store.New(m.Root, base+"journal.jsonl", options)
	if err != nil {
		return nil, nil, err
	}
	private, err := store.New(m.Root, base+"private.jsonl", options)
	return journal, private, err
}

func ValidateConfig(c Config) error {
	if c.SchemaVersion != 1 || len(c.Repositories) == 0 || len(c.Repositories) > 128 || len(c.SessionDirs) > 32 || len(c.WardDirs) > 32 || c.ExportTimeoutSeconds < 0 || c.ExportTimeoutSeconds > 300 {
		return ErrInvalid
	}
	paths := append(append(append([]string{}, c.Repositories...), c.SessionDirs...), c.WardDirs...)
	if c.SealBinary != "" {
		paths = append(paths, c.SealBinary)
	}
	if c.SelfRepository != "" {
		paths = append(paths, c.SelfRepository)
	}
	for _, p := range paths {
		if len(p) > 4096 || !filepath.IsAbs(p) || filepath.Clean(p) != p || filepath.Dir(p) == p {
			return ErrInvalid
		}
	}
	for _, group := range [][]string{c.Repositories, c.SessionDirs, c.WardDirs} {
		seen := map[string]bool{}
		for _, p := range group {
			if seen[p] {
				return ErrInvalid
			}
			seen[p] = true
		}
	}
	return nil
}

func appendLine(existing []byte, v any) ([]byte, error) {
	line, err := json.Marshal(v)
	if err != nil {
		return nil, ErrInvalid
	}
	out := append(bytes.Clone(existing), line...)
	return append(out, '\n'), nil
}

func (m Manager) Init(ctx context.Context, c Config, now time.Time) (Experiment, store.Commit, error) {
	if err := ctx.Err(); err != nil {
		return Experiment{}, store.Commit{}, err
	}
	if err := ValidateConfig(c); err != nil {
		return Experiment{}, store.Commit{}, err
	}
	if now.IsZero() {
		return Experiment{}, store.Commit{}, ErrInvalid
	}
	id, err := NewID()
	if err != nil {
		return Experiment{}, store.Commit{}, err
	}
	e := Experiment{Schema: Schema, ID: id, StartedAt: now.UTC(), Config: c}
	pb, err := appendLine(nil, PrivateBatch{Schema: Schema, Experiment: &e})
	if err != nil {
		return Experiment{}, store.Commit{}, err
	}
	jb, err := appendLine(nil, Batch{Schema: Schema, At: now.UTC()})
	if err != nil {
		return Experiment{}, store.Commit{}, err
	}
	if int64(len(pb))+int64(len(jb)) > MaxBytes {
		return Experiment{}, store.Commit{}, ErrFull
	}
	j, _, err := m.stores(id)
	if err != nil {
		return Experiment{}, store.Commit{}, err
	}
	// Publish both files together: readers must never observe a private manifest
	// without its initial journal. Existing destinations are never repaired.
	hooks := m.Options.Hooks
	beforeReplace := hooks.BeforeReplace
	hooks.BeforeReplace = func(staging, target string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if beforeReplace != nil {
			if err := beforeReplace(staging, target); err != nil {
				return err
			}
		}
		return ctx.Err()
	}
	commit, err := store.CreateArtifactBundleWithHooks(filepath.Dir(j.Path()), map[string][]byte{
		"private.jsonl": pb,
		"journal.jsonl": jb,
	}, hooks)
	if !commit.Committed {
		return Experiment{}, commit, err
	}
	return e, commit, err
}

func lines(data []byte, visit func([]byte) error) error {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return ErrInvalid
	}
	for _, line := range bytes.Split(data[:len(data)-1], []byte{'\n'}) {
		if err := visit(line); err != nil {
			return err
		}
	}
	return nil
}

func parseSnapshot(private, journal []byte, id string) (Snapshot, error) {
	s := Snapshot{Tasks: map[string]Task{}, Events: map[string]Event{}}
	if int64(len(private))+int64(len(journal)) > MaxBytes {
		return s, ErrFull
	}
	bindingKeys := map[string]string{}
	bindingIDs := map[string]string{}
	err := lines(private, func(line []byte) error {
		var b PrivateBatch
		if err := DecodeStrict(line, &b); err != nil {
			return err
		}
		if b.Schema != Schema {
			return ErrInvalid
		}
		if b.Experiment != nil {
			if s.Experiment.ID != "" || b.Experiment.ID != id || b.Experiment.Schema != Schema || b.Experiment.StartedAt.IsZero() {
				return ErrInvalid
			}
			if err := ValidateConfig(b.Experiment.Config); err != nil {
				return err
			}
			s.Experiment = *b.Experiment
		}
		for _, v := range b.Bindings {
			key := v.Kind + "\x00" + v.Key
			if !idPattern.MatchString(v.ID) || v.Kind == "" || v.Key == "" || bindingKeys[key] != "" || bindingIDs[v.ID] != "" {
				return ErrInvalid
			}
			bindingKeys[key] = v.ID
			bindingIDs[v.ID] = key
			s.Bindings = append(s.Bindings, v)
		}
		return nil
	})
	if err != nil || s.Experiment.ID == "" {
		return s, ErrInvalid
	}
	err = lines(journal, func(line []byte) error {
		var b Batch
		if err := DecodeStrict(line, &b); err != nil {
			return err
		}
		if b.Schema != Schema || b.At.IsZero() {
			return ErrInvalid
		}
		for _, t := range b.Tasks {
			if t.ID == "" || t.Revision != s.Tasks[t.ID].Revision+1 {
				return ErrInvalid
			}
			s.Tasks[t.ID] = t
		}
		for _, e := range b.Events {
			if e.ID == "" || e.SourceID == "" || e.Revision < 1 {
				return ErrInvalid
			}
			if _, exists := s.Events[e.ID]; exists {
				return ErrInvalid
			}
			s.Events[e.ID] = e
		}
		if b.Receipt != nil {
			s.Receipts = append(s.Receipts, *b.Receipt)
		}
		if b.Review != nil {
			if err := ValidateReview(s, *b.Review); err != nil {
				return err
			}
			s.Reviews = append(s.Reviews, *b.Review)
		}
		return nil
	})
	return s, err
}

func (m Manager) Load(id string) (Snapshot, error) {
	j, p, err := m.stores(id)
	if err != nil {
		return Snapshot{}, err
	}
	// Bindings publish before facts. Read facts first so every referenced alias
	// must already exist in the private snapshot read afterwards.
	jb, err := j.Read()
	if err != nil {
		return Snapshot{}, err
	}
	pb, err := p.Read()
	if err != nil {
		return Snapshot{}, err
	}
	return parseSnapshot(pb, jb, id)
}

func (m Manager) Collect(ctx context.Context, id string, now time.Time) (Receipt, error) {
	receipt := Receipt{Schema: Schema, ExperimentID: id, CollectedAt: now.UTC(), Durability: "unconfirmed"}
	j, p, err := m.stores(id)
	if err != nil {
		return receipt, err
	}
	commit, err := j.Update(ctx, func(old []byte) ([]byte, error) {
		pb, err := p.Read()
		if err != nil {
			return nil, err
		}
		s, err := parseSnapshot(pb, old, id)
		if err != nil {
			return nil, err
		}
		tasks, events, bindings, r := Gather(ctx, s, now.UTC())
		receipt = r
		receipt.Schema = Schema
		receipt.ExperimentID = id
		receipt.CollectedAt = now.UTC()
		receipt.Committed = true
		receipt.Durability = "unconfirmed"
		next, err := appendLine(old, Batch{Schema: Schema, At: now.UTC(), Tasks: tasks, Events: events, Receipt: &receipt})
		if err != nil {
			return nil, err
		}
		privateNext := pb
		if len(bindings) > 0 {
			privateNext, err = appendLine(pb, PrivateBatch{Schema: Schema, Bindings: bindings})
			if err != nil {
				return nil, err
			}
		}
		if int64(len(next))+int64(len(privateNext)) > MaxBytes {
			return nil, ErrFull
		}
		if len(bindings) > 0 {
			pc, pe := p.Update(ctx, func(current []byte) ([]byte, error) {
				if !bytes.Equal(current, pb) {
					return nil, ErrInvalid
				}
				return privateNext, nil
			})
			if !pc.Committed {
				return nil, pe
			}
		}
		return next, nil
	})
	receipt.Committed = commit.Committed
	if commit.DurabilityConfirmed {
		receipt.Durability = "confirmed"
	} else {
		receipt.Durability = "unconfirmed"
	}
	return receipt, err
}

func (m Manager) Apply(ctx context.Context, id string, r Review, now time.Time) (store.Commit, error) {
	j, p, err := m.stores(id)
	if err != nil {
		return store.Commit{}, err
	}
	return j.Update(ctx, func(old []byte) ([]byte, error) {
		pb, err := p.Read()
		if err != nil {
			return nil, err
		}
		s, err := parseSnapshot(pb, old, id)
		if err != nil {
			return nil, err
		}
		if err = ValidateReview(s, r); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		next, err := appendLine(old, Batch{Schema: Schema, At: now.UTC(), Review: &r})
		if err != nil {
			return nil, err
		}
		if int64(len(next))+int64(len(pb)) > MaxBytes {
			return nil, ErrFull
		}
		return next, nil
	})
}

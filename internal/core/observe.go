package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jgoneit/eval/internal/contract"
	"github.com/jgoneit/eval/internal/state"
	"github.com/jgoneit/eval/internal/store"
)

var (
	ErrInvalidDraft = errors.New("invalid observe draft")
	ErrInvalidState = errors.New("invalid observation state")
	ErrOperational  = errors.New("observe operational failure")
)

type ObserveResult struct {
	Status        string `json:"status"`
	ObservationID string `json:"observation_id"`
	TaskID        string `json:"task_id"`
	Revision      int    `json:"revision"`
}

type Observer struct {
	Now          func() time.Time
	NewUUID      func() (string, error)
	StoreOptions store.Options
}

func (o Observer) Observe(ctx context.Context, root string, draftData []byte) (ObserveResult, error) {
	draft, err := contract.ParseDraft(draftData)
	if err != nil {
		return ObserveResult{}, fmt.Errorf("%w: %v", ErrInvalidDraft, err)
	}
	v1, err := readOptional(ctx, root, state.V1RelativePath)
	if err != nil {
		return ObserveResult{}, err
	}
	v2Store, err := store.New(root, state.V2RelativePath, o.StoreOptions)
	if err != nil {
		return ObserveResult{}, err
	}

	now := o.Now
	if now == nil {
		now = time.Now
	}
	newUUID := o.NewUUID
	if newUUID == nil {
		newUUID = randomUUIDv4
	}
	legacy := ParseStores(v1, nil)
	// terminal_on is supplied as a Host-local calendar date. Keep recorded_on
	// in the same calendar domain so local tasks near midnight are not rejected
	// merely because UTC is still on the previous day.
	recordedOn := now().Format(time.DateOnly)
	taskID := ""
	if draft.TaskID != nil {
		taskID = *draft.TaskID
	} else {
		taskID, err = newUUID()
		if err != nil {
			return ObserveResult{}, fmt.Errorf("%w: generate task id: %v", ErrOperational, err)
		}
	}
	observationID, err := newUUID()
	if err != nil {
		return ObserveResult{}, fmt.Errorf("%w: generate observation id: %v", ErrOperational, err)
	}
	var result ObserveResult
	err = v2Store.Update(ctx, func(existing []byte) ([]byte, error) {
		current := ParseStores(nil, existing)
		if !current.Validation.Valid() {
			return nil, fmt.Errorf("%w: existing v2 log", ErrInvalidState)
		}

		if draft.TaskID != nil {
			for _, observation := range legacy.Validation.ValidObservations {
				if observation.TaskID == taskID {
					return nil, fmt.Errorf("%w: v1 and v2 correction chains cannot be mixed", ErrInvalidDraft)
				}
			}
		}
		revision := 1
		var supersedes *string
		matchedCorrection := false
		for _, observation := range current.Validation.ValidObservations {
			if observation.TaskID != taskID {
				continue
			}
			matchedCorrection = true
			if observation.Revision >= revision {
				revision = observation.Revision + 1
				predecessor := observation.ObservationID
				supersedes = &predecessor
			}
		}
		if draft.TaskID != nil && !matchedCorrection {
			return nil, fmt.Errorf("%w: task_id does not match an existing valid v2 chain", ErrInvalidDraft)
		}

		identifierExists := func(candidate string) bool {
			for _, dataset := range []contract.LogValidation{legacy.Validation, current.Validation} {
				for _, observation := range dataset.ValidObservations {
					if observation.ObservationID == candidate || observation.TaskID == candidate {
						return true
					}
				}
			}
			return false
		}
		if draft.TaskID == nil {
			for attempts := 0; identifierExists(taskID) && attempts < 4; attempts++ {
				taskID, err = newUUID()
				if err != nil {
					return nil, fmt.Errorf("%w: regenerate task id: %v", ErrOperational, err)
				}
			}
			if identifierExists(taskID) {
				return nil, fmt.Errorf("%w: repeated task identifier collision", ErrOperational)
			}
		}
		for attempts := 0; (observationID == taskID || identifierExists(observationID)) && attempts < 4; attempts++ {
			observationID, err = newUUID()
			if err != nil {
				return nil, fmt.Errorf("%w: regenerate observation id: %v", ErrOperational, err)
			}
		}
		if observationID == taskID || identifierExists(observationID) {
			return nil, fmt.Errorf("%w: repeated observation identifier collision", ErrOperational)
		}
		observation := draft.BuildObservation(
			observationID, taskID, revision, supersedes, recordedOn,
		)
		if issues := contract.ValidateObservation(observation); len(issues) > 0 {
			return nil, fmt.Errorf("%w: generated observation: %s", ErrInvalidDraft, issues[0].Code)
		}
		row, err := contract.CanonicalJSON(observation)
		if err != nil {
			return nil, fmt.Errorf("%w: encode observation: %v", ErrOperational, err)
		}
		prospective := make([]byte, 0, len(existing)+len(row)+1)
		prospective = append(prospective, existing...)
		prospective = append(prospective, row...)
		prospective = append(prospective, '\n')
		validated := ParseStores(nil, prospective)
		if !validated.Validation.Valid() {
			return nil, fmt.Errorf("%w: prospective v2 log", ErrInvalidState)
		}
		result = ObserveResult{
			Status: "recorded", ObservationID: observationID,
			TaskID: taskID, Revision: revision,
		}
		return prospective, nil
	})
	if err != nil {
		return ObserveResult{}, err
	}
	return result, nil
}

func randomUUIDv4() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := make([]byte, 36)
	hex.Encode(encoded[0:8], value[0:4])
	encoded[8] = '-'
	hex.Encode(encoded[9:13], value[4:6])
	encoded[13] = '-'
	hex.Encode(encoded[14:18], value[6:8])
	encoded[18] = '-'
	hex.Encode(encoded[19:23], value[8:10])
	encoded[23] = '-'
	hex.Encode(encoded[24:36], value[10:16])
	return string(encoded), nil
}

func IsStoreMissing(err error) bool { return errors.Is(err, fs.ErrNotExist) }

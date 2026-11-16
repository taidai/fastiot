package calculation

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

type Mutation struct {
	ID               string
	Actor            string
	ExpectedRevision uint64
	Now              time.Time
}

type Change struct {
	ID          string    `json:"id"`
	Action      string    `json:"action"`
	Actor       string    `json:"actor"`
	At          time.Time `json:"at"`
	Fingerprint string    `json:"fingerprint"`
}

type Document struct {
	Schema     int         `json:"schema"`
	Revision   uint64      `json:"revision"`
	Version    uint64      `json:"version"`
	Draft      Definition  `json:"draft"`
	Applied    *Definition `json:"applied,omitempty"`
	LastChange *Change     `json:"last_change,omitempty"`
}

func cloneDefinition(d Definition) Definition {
	d.Inputs = slices.Clone(d.Inputs)
	if d.MaxSkewMS != nil {
		value := *d.MaxSkewMS
		d.MaxSkewMS = &value
	}
	return d
}
func mutationFingerprint(action string, payload any, c Mutation) (string, error) {
	if c.ID == "" || len(c.ID) > 128 || c.Actor == "" || c.Now.IsZero() {
		return "", errors.New("invalid_operation")
	}
	raw, err := json.Marshal(struct {
		Action, Actor string
		Revision      uint64
		Payload       any
	}{action, c.Actor, c.ExpectedRevision, payload})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
func (d Document) check(action string, payload any, c Mutation) (string, bool, error) {
	fp, err := mutationFingerprint(action, payload, c)
	if err != nil {
		return "", false, err
	}
	if d.LastChange != nil && d.LastChange.ID == c.ID {
		if d.LastChange.Fingerprint != fp {
			return "", false, errors.New("operation_conflict")
		}
		return fp, true, nil
	}
	if d.Revision != c.ExpectedRevision {
		return "", false, errors.New("revision_conflict")
	}
	return fp, false, nil
}
func (d *Document) changed(action, fp string, c Mutation) {
	d.Revision++
	d.LastChange = &Change{ID: c.ID, Action: action, Actor: c.Actor, At: c.Now.UTC(), Fingerprint: fp}
}
func NewDocument(draft Definition, c Mutation) (Document, error) {
	if err := draft.Validate(); err != nil {
		return Document{}, err
	}
	fp, err := mutationFingerprint("create", draft, c)
	if err != nil {
		return Document{}, err
	}
	d := Document{Schema: 1, Draft: cloneDefinition(draft)}
	d.changed("create", fp, c)
	return d, nil
}
func (d Document) SaveDraft(draft Definition, c Mutation) (Document, error) {
	fp, retry, err := d.check("draft", draft, c)
	if err != nil || retry {
		return d, err
	}
	if err := draft.ValidateDraft(); err != nil {
		return d, err
	}
	if reflect.DeepEqual(d.Draft, draft) {
		return d, nil
	}
	d.Draft = cloneDefinition(draft)
	d.changed("draft", fp, c)
	return d, nil
}
func (d Document) Apply(c Mutation) (Document, error) {
	fp, retry, err := d.check("apply", nil, c)
	if err != nil || retry {
		return d, err
	}
	if err := d.Draft.Validate(); err != nil {
		return d, err
	}
	if d.Applied != nil && reflect.DeepEqual(*d.Applied, d.Draft) {
		return d, nil
	}
	applied := cloneDefinition(d.Draft)
	d.Applied = &applied
	d.Version++
	d.changed("apply", fp, c)
	return d, nil
}
